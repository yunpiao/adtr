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

const DirectoryV4PageMaxBytes = 8 << 20

// DirectoryV4List cannot widen the legacy DTO accidentally. Its fixed integer
// pin is present even when no current observation is available.
type DirectoryV4List struct {
	DictionaryVersion int                              `json:"dictionaryVersion"`
	Available         bool                             `json:"available"`
	ObservationID     string                           `json:"observationId,omitempty"`
	Source            *ldapconnection.DirectorySource  `json:"source,omitempty"`
	List              []directoryassets.PublicObjectV4 `json:"list"`
	Page              Page                             `json:"page"`
}

func directoryV4Page(observationID string, o ldapconnection.DirectoryV4Observation, f DirectoryFilter) (DirectoryV4List, error) {
	rows := make([]int, 0, len(o.Objects))
	for i, object := range o.Objects {
		if f.Kind == "" || object.Base.Kind == f.Kind {
			rows = append(rows, i)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return o.Objects[rows[i]].Base.GUID < o.Objects[rows[j]].Base.GUID })
	out := DirectoryV4List{DictionaryVersion: 4, Available: true, ObservationID: observationID, Source: &o.Source, List: []directoryassets.PublicObjectV4{}, Page: Page{Index: f.PageIdx, Size: f.PageSize, Total: len(rows)}}
	if len(rows) > 0 {
		out.Page.Pages = (len(rows) + f.PageSize - 1) / f.PageSize
	}
	start := (f.PageIdx - 1) * f.PageSize
	if start < len(rows) {
		for _, index := range rows[start:min(start+f.PageSize, len(rows))] {
			object, err := directoryassets.ProjectV4(o.Objects[index])
			if err != nil {
				return DirectoryV4List{}, problem(409, "directory_observation_unavailable")
			}
			out.List = append(out.List, object)
		}
	}
	if err := validateDirectoryV4PageSize(out); err != nil {
		return DirectoryV4List{}, err
	}
	return out, nil
}

// Marshal before returning any rows to HTTP. The public text projection can be
// larger than stored base64, particularly for JSON-escaped control characters.
func validateDirectoryV4PageSize(out DirectoryV4List) error {
	raw, err := json.Marshal(out)
	defer clear(raw)
	if err != nil || len(raw)+1 > DirectoryV4PageMaxBytes { // Encoder's newline.
		return problem(422, "directory_limit_exceeded")
	}
	return nil
}

// DirectoryV4ListTx requires the caller's live reader/domain authorization and
// schema/tenant/actor lock. Reading stored data does not require a use grant or
// either deployment gate. V1 observations never satisfy this query.
func (s *Store) DirectoryV4ListTx(ctx context.Context, tx pgx.Tx, tenant string, allowed []string, f DirectoryFilter) (DirectoryV4List, error) {
	if err := ValidateDirectoryFilter(f); err != nil {
		return DirectoryV4List{}, err
	}
	if !slices.Contains(allowed, f.DomainID) {
		return DirectoryV4List{}, problem(404, "not_found")
	}
	if _, err := getRow(ctx, tx, tenant, f.DomainID, false); err != nil {
		return DirectoryV4List{}, err
	}
	id, observation, err := s.loadDirectoryV4ObservationTx(ctx, tx, tenant, f.DomainID, f.ObservationID)
	defer observation.Discard()
	if err != nil {
		return DirectoryV4List{}, err
	}
	if id == "" {
		return DirectoryV4List{DictionaryVersion: 4, List: []directoryassets.PublicObjectV4{}, Page: Page{Index: f.PageIdx, Size: f.PageSize}}, nil
	}
	return directoryV4Page(id, observation, f)
}

// loadDirectoryV4ObservationTx is the one eligible immutable-observation loader
// for stored dictionary-4 reads. Callers first establish live tenant/domain
// authority under the schema/tenant/actor locks. Success transfers ownership of
// the decoded observation; callers must defer Discard immediately. Absence is
// represented by an empty id; a supplied pin never falls back to another row.
// Raw database bytes and decoded failure values are cleared on every exit.
func (s *Store) loadDirectoryV4ObservationTx(ctx context.Context, tx pgx.Tx, tenant, domainID, observationID string) (id string, observation ldapconnection.DirectoryV4Observation, err error) {
	defer func() {
		if err != nil {
			observation.Discard()
		}
	}()
	var digest, domain, server string
	var body []byte
	defer func() { clear(body) }()
	var count int
	err = tx.QueryRow(ctx, `SELECT o.task_id,o.body,o.sha256,o.object_count,c.canonical_domain,c.dc_hostname
 FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id AND t.tenant_id=o.tenant_id AND t.domain_id=o.domain_id AND t.actor_id=o.actor_id
 JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id AND u.tenant_id=o.tenant_id AND u.domain_id=o.domain_id
 JOIN adtr.domain_connections c ON c.tenant_id=o.tenant_id AND c.domain_id=o.domain_id
 WHERE o.tenant_id=$1 AND o.domain_id=$2 AND ($3='' OR o.task_id=$3)
 AND o.dictionary_version=4 AND u.dictionary_version=4 AND u.purpose='domain.directory_read.v4'
 AND t.kind='domain.directory_read.v4' AND t.payload_version=1 AND t.state='succeeded'
 AND u.actor_id=o.actor_id AND o.opener_owner=u.opener_owner AND o.opener_fencing_token=u.opener_fencing_token AND o.opener_attempt=u.opener_attempt
 AND adtr.domain_directory_use_pins(u)
 AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND u.connection_revision=c.connection_revision AND u.connection_credential_generation=c.credential_revision
 AND u.account_id=c.operation_account_id AND u.account_credential_revision=c.operation_account_credential_revision AND u.policy_revision=$4
 ORDER BY o.created_at DESC,o.task_id DESC LIMIT 1`, tenant, domainID, observationID, s.policyRevision()).Scan(&id, &body, &digest, &count, &domain, &server)
	if errors.Is(err, pgx.ErrNoRows) {
		if observationID != "" {
			return "", observation, problem(409, "directory_observation_unavailable")
		}
		return "", observation, nil
	}
	if err != nil {
		return "", observation, err
	}
	observation, err = decodeDirectoryObservationV4(body, digest, count)
	if err != nil {
		return "", observation, err
	}
	if observation.Source.Domain != domain || observation.Source.ServerName != server {
		return "", observation, problem(409, "directory_observation_unavailable")
	}
	return id, observation, nil
}
