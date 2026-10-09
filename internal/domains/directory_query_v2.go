package domains

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

const DirectoryV2PageMaxBytes = 8 << 20

// DirectoryV2List cannot widen the legacy DTO accidentally. Its fixed integer
// pin is present even when no current observation is available.
type DirectoryV2List struct {
	DictionaryVersion int                              `json:"dictionaryVersion"`
	Available         bool                             `json:"available"`
	ObservationID     string                           `json:"observationId,omitempty"`
	Source            *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List              []directoryassets.PublicObjectV2 `json:"list"`
	Page              Page                             `json:"page"`
}

func directoryV2Page(observationID string, o ldapconnection.DirectoryV2Observation, f DirectoryFilter) (DirectoryV2List, error) {
	rows := make([]int, 0, len(o.Objects))
	for i, object := range o.Objects {
		if f.Kind == "" || object.Base.Kind == f.Kind {
			rows = append(rows, i)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return o.Objects[rows[i]].Base.GUID < o.Objects[rows[j]].Base.GUID })
	out := DirectoryV2List{DictionaryVersion: 2, Available: true, ObservationID: observationID, Source: &o.Source, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		for _, index := range rows[start:min(start+f.PageSize, len(rows))] {
			object, err := directoryassets.ProjectV2(o.Objects[index])
			if err != nil {
				return DirectoryV2List{}, problem(409, "directory_observation_unavailable")
			}
			out.List = append(out.List, object)
		}
	}
	if err := validateDirectoryV2PageSize(out); err != nil {
		return DirectoryV2List{}, err
	}
	return out, nil
}

// Marshal before returning any rows to HTTP. The public text projection can be
// larger than stored base64, particularly for JSON-escaped control characters.
func validateDirectoryV2PageSize(out DirectoryV2List) error {
	raw, err := json.Marshal(out)
	defer clear(raw)
	if err != nil || len(raw)+1 > DirectoryV2PageMaxBytes { // Encoder's newline.
		return problem(422, "directory_limit_exceeded")
	}
	return nil
}

// DirectoryV2ListTx requires the caller's live reader/domain authorization and
// schema/tenant/actor lock. Reading stored data does not require a use grant or
// either deployment gate. V1 observations never satisfy this query.
func (s *Store) DirectoryV2ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f DirectoryFilter) (DirectoryV2List, error) {
	if err := ValidateDirectoryFilter(f); err != nil {
		return DirectoryV2List{}, err
	}
	if !slices.Contains(allowed, f.DomainID) {
		return DirectoryV2List{}, problem(404, "not_found")
	}
	if _, err := getRow(ctx, tx, tenant, f.DomainID, false); err != nil {
		return DirectoryV2List{}, err
	}
	var id, digest, domain, server string
	var body []byte
	defer func() { clear(body) }()
	var count int
	err := tx.QueryRow(ctx, `SELECT o.task_id,o.body,o.sha256,o.object_count,c.canonical_domain,c.dc_hostname
 FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id AND t.tenant_id=o.tenant_id AND t.domain_id=o.domain_id AND t.actor_id=o.actor_id
 JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id AND u.tenant_id=o.tenant_id AND u.domain_id=o.domain_id
 JOIN adtr.domain_connections c ON c.tenant_id=o.tenant_id AND c.domain_id=o.domain_id
 WHERE o.tenant_id=$1 AND o.domain_id=$2 AND ($3='' OR o.task_id=$3)
 AND o.dictionary_version=2 AND u.dictionary_version=2 AND u.purpose='domain.directory_read.v2'
 AND t.kind='domain.directory_read.v2' AND t.payload_version=1 AND t.state='succeeded'
 AND u.actor_id=o.actor_id AND o.opener_owner=u.opener_owner AND o.opener_fencing_token=u.opener_fencing_token AND o.opener_attempt=u.opener_attempt
 AND adtr.domain_directory_use_pins(u)
 AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND u.connection_revision=c.connection_revision AND u.connection_credential_generation=c.credential_revision
 AND u.account_id=c.operation_account_id AND u.account_credential_revision=c.operation_account_credential_revision AND u.policy_revision=$4
 ORDER BY o.created_at DESC,o.task_id DESC LIMIT 1`, tenant, f.DomainID, f.ObservationID, s.policyRevision()).Scan(&id, &body, &digest, &count, &domain, &server)
	if errors.Is(err, pgx.ErrNoRows) {
		if f.ObservationID != "" {
			return DirectoryV2List{}, problem(409, "directory_observation_unavailable")
		}
		return DirectoryV2List{DictionaryVersion: 2, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}, nil
	}
	if err != nil {
		return DirectoryV2List{}, err
	}
	observation, err := decodeDirectoryObservationV2(body, digest, count)
	defer observation.Discard()
	if err != nil {
		return DirectoryV2List{}, err
	}
	if observation.Source.Domain != domain || observation.Source.ServerName != server {
		return DirectoryV2List{}, problem(409, "directory_observation_unavailable")
	}
	return directoryV2Page(id, observation, f)
}
