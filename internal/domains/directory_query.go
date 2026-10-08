package domains

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

type DirectoryFilter struct {
	DomainID, ObservationID string
	Kind                    directoryassets.Kind
	PageIdx, PageSize       int
}
type DirectoryList struct {
	Available     bool                            `json:"available"`
	ObservationID string                          `json:"observationId,omitempty"`
	Source        *ldapconnection.DirectorySource `json:"source,omitempty"`
	List          []directoryassets.Object        `json:"list"`
	Page          Page                            `json:"page"`
}

func ValidateDirectoryFilter(f DirectoryFilter) error {
	if !ValidID(f.DomainID) || f.ObservationID != "" && !ValidID(f.ObservationID) || f.PageIdx < 1 || f.PageIdx > 10000 || (f.PageSize != 25 && f.PageSize != 50 && f.PageSize != 100) || (f.PageIdx > 1 && f.ObservationID == "") {
		return problem(422, "invalid_input")
	}
	if f.Kind != "" && f.Kind != directoryassets.User && f.Kind != directoryassets.Group && f.Kind != directoryassets.Computer {
		return problem(422, "invalid_input")
	}
	return nil
}
func directoryPage(observationID string, o ldapconnection.DirectoryObservation, f DirectoryFilter) DirectoryList {
	rows := make([]directoryassets.Object, 0, len(o.Objects))
	for _, object := range o.Objects {
		if f.Kind == "" || object.Kind == f.Kind {
			rows = append(rows, object)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].GUID < rows[j].GUID })
	out := DirectoryList{Available: true, ObservationID: observationID, Source: &o.Source, List: []directoryassets.Object{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		out.List = rows[start:min(start+f.PageSize, len(rows))]
	}
	return out
}

// DirectoryListTx requires the caller's schema/tenant/actor lock, live directory
// read permission and domain scope. Data readers need no credential-use grant.
// Only succeeded tasks with currently matching source/policy pins are visible;
// staged/cancelled/old-binding observations cannot masquerade as current data.
func (s *Store) DirectoryListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f DirectoryFilter) (DirectoryList, error) {
	if err := ValidateDirectoryFilter(f); err != nil {
		return DirectoryList{}, err
	}
	if !slices.Contains(allowed, f.DomainID) {
		return DirectoryList{}, problem(404, "not_found")
	}
	if _, err := getRow(ctx, tx, tenant, f.DomainID, false); err != nil {
		return DirectoryList{}, err
	}
	var id, digest string
	var body []byte
	var count int
	err := tx.QueryRow(ctx, `SELECT o.task_id,o.body,o.sha256,o.object_count
 FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id AND t.tenant_id=o.tenant_id AND t.domain_id=o.domain_id AND t.actor_id=o.actor_id
 JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id AND u.tenant_id=o.tenant_id AND u.domain_id=o.domain_id
 JOIN adtr.domain_connections c ON c.tenant_id=o.tenant_id AND c.domain_id=o.domain_id
 WHERE o.tenant_id=$1 AND o.domain_id=$2 AND ($3='' OR o.task_id=$3)
 AND t.kind='domain.directory_read' AND t.payload_version=1 AND t.state='succeeded'
 AND adtr.domain_directory_use_pins(u)
 AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND u.connection_revision=c.connection_revision AND u.connection_credential_generation=c.credential_revision
 AND u.account_id=c.operation_account_id AND u.account_credential_revision=c.operation_account_credential_revision AND u.policy_revision=$4
 ORDER BY o.created_at DESC,o.task_id DESC LIMIT 1`, tenant, f.DomainID, f.ObservationID, s.policyRevision()).Scan(&id, &body, &digest, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		if f.ObservationID != "" {
			return DirectoryList{}, problem(409, "directory_observation_unavailable")
		}
		return DirectoryList{List: []directoryassets.Object{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}, nil
	}
	if err != nil {
		return DirectoryList{}, err
	}
	observation, err := decodeDirectoryObservation(body, digest, count)
	if err != nil {
		return DirectoryList{}, err
	}
	return directoryPage(id, observation, f), nil
}
