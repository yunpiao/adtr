package simulationevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
)

func identifier(v string) bool {
	if v == "" || len(v) > 128 || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func authorize(tx pgx.Tx, s securityevents.Scope, manage bool) error {
	if tx == nil || !identifier(s.Tenant) || !identifier(s.Domain) || !s.Read || (manage && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func pagination(offset, limit int) error {
	if offset < 0 || offset > securityevents.MaxEvents || limit < 1 || limit > MaxPage {
		return ErrInvalid
	}
	return nil
}
func hash(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func provenance(d Dataset) securityevents.Provenance {
	return securityevents.Provenance{Contract: d.Contract, Fixture: d.Fixture, Synthetic: d.Simulation}
}
func getDataset(ctx context.Context, tx pgx.Tx, s securityevents.Scope, id string) (Dataset, error) {
	d := Dataset{}
	if err := authorize(tx, s, false); err != nil {
		return d, err
	}
	if !identifier(id) {
		return d, ErrInvalid
	}
	err := tx.QueryRow(ctx, `SELECT dataset_id,fixture,fixture_hash,contract,simulation,event_count FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, id).Scan(&d.ID, &d.Fixture, &d.FixtureHash, &d.Contract, &d.Simulation, &d.EventCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if _, err = securityevents.Normalize(s, provenance(d), nil); err != nil {
		return Dataset{}, err
	}
	if d.EventCount < 0 || d.EventCount > securityevents.MaxEvents || len(d.FixtureHash) != 64 {
		return Dataset{}, ErrInvalid
	}
	return d, nil
}
func ListDatasetsTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, offset, limit int) (DatasetPage, error) {
	out := DatasetPage{Contract: securityevents.ContractVersion, Simulation: true, Datasets: []Dataset{}}
	if err := authorize(tx, s, false); err != nil {
		return out, err
	}
	if err := pagination(offset, limit); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2`, s.Tenant, s.Domain).Scan(&out.Total); err != nil {
		return out, err
	}
	if out.Total > securityevents.MaxEvents {
		return out, ErrLimit
	}
	rows, err := tx.Query(ctx, `SELECT dataset_id,fixture,fixture_hash,contract,simulation,event_count FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2 ORDER BY dataset_id OFFSET $3 LIMIT $4`, s.Tenant, s.Domain, offset, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Dataset
		if err = rows.Scan(&d.ID, &d.Fixture, &d.FixtureHash, &d.Contract, &d.Simulation, &d.EventCount); err != nil {
			return out, err
		}
		if _, err = securityevents.Normalize(s, provenance(d), nil); err != nil {
			return out, err
		}
		out.Datasets = append(out.Datasets, d)
	}
	return out, rows.Err()
}
func loadDataset(ctx context.Context, tx pgx.Tx, s securityevents.Scope, d Dataset) (*securityevents.Dataset, error) {
	rows, err := tx.Query(ctx, `SELECT event_id,payload FROM adtr.simulation_events WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 ORDER BY event_id LIMIT 4097`, s.Tenant, s.Domain, d.ID, d.FixtureHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := []securityevents.RawEvent{}
	for rows.Next() {
		var id string
		var b []byte
		var r securityevents.RawEvent
		if err = rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		if len(b) > 4096 {
			return nil, ErrLimit
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, ErrInvalid
		}
		if r.ID != id {
			return nil, ErrInvalid
		}
		raw = append(raw, r)
		if len(raw) > securityevents.MaxEvents {
			return nil, ErrLimit
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(raw) != d.EventCount {
		return nil, ErrInvalid
	}
	return securityevents.Normalize(s, provenance(d), raw)
}
func loadCase(ctx context.Context, tx pgx.Tx, s securityevents.Scope, d Dataset, id string, lock bool) (securityevents.Case, error) {
	if !identifier(id) {
		return securityevents.Case{}, ErrInvalid
	}
	q := `SELECT state,mark,revision,updated_at FROM adtr.simulation_event_cases WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND case_id=$5`
	if lock {
		q += ` FOR UPDATE`
	}
	v := securityevents.CaseSnapshot{ID: id, Tenant: s.Tenant, Domain: s.Domain, Provenance: provenance(d)}
	var at *time.Time
	var revision int64
	err := tx.QueryRow(ctx, q, s.Tenant, s.Domain, d.ID, d.FixtureHash, id).Scan(&v.State, &v.Mark, &revision, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return securityevents.Case{}, ErrNotFound
	}
	if err != nil {
		return securityevents.Case{}, err
	}
	if revision < 1 || revision > MaxHistory+1 {
		return securityevents.Case{}, ErrInvalid
	}
	v.Revision = uint64(revision)
	if at != nil {
		v.UpdatedAt = *at
	}
	return securityevents.RestoreCase(s, provenance(d), v)
}
func QueryTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, id string, f securityevents.Filter) (QueryResult, error) {
	out := QueryResult{Simulation: true, Cases: []securityevents.CaseView{}}
	if err := pagination(f.Offset, f.Limit); err != nil {
		return out, err
	}
	d, err := getDataset(ctx, tx, s, id)
	if err != nil {
		return out, err
	}
	out.Dataset = d
	core, err := loadDataset(ctx, tx, s, d)
	if err != nil {
		return out, err
	}
	out.Page, err = core.Query(s, f)
	if err != nil {
		return out, err
	}
	// Cases are dataset-wide and bounded independently; no hidden truncation.
	rows, err := tx.Query(ctx, `SELECT case_id FROM adtr.simulation_event_cases WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 ORDER BY case_id LIMIT 101`, s.Tenant, s.Domain, d.ID, d.FixtureHash)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var cid string
		if err = rows.Scan(&cid); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, cid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > MaxPage {
		return out, ErrLimit
	}
	for _, cid := range ids {
		c, e := loadCase(ctx, tx, s, d, cid, false)
		if e != nil {
			return out, e
		}
		v, e := c.View(s)
		if e != nil {
			return out, e
		}
		out.Cases = append(out.Cases, v)
	}
	return out, nil
}
func DetailTx(ctx context.Context, tx pgx.Tx, s securityevents.Scope, id, caseID string, offset, limit int) (Detail, error) {
	out := Detail{Simulation: true, Evidence: []securityevents.Event{}, History: []securityevents.Audit{}}
	if err := pagination(offset, limit); err != nil {
		return out, err
	}
	d, err := getDataset(ctx, tx, s, id)
	if err != nil {
		return out, err
	}
	out.Dataset = d
	// A row lock stabilizes the state and its paged history against mutations.
	c, err := loadCase(ctx, tx, s, d, caseID, true)
	if err != nil {
		return out, err
	}
	out.Case, err = c.View(s)
	if err != nil {
		return out, err
	}
	core, err := loadDataset(ctx, tx, s, d)
	if err != nil {
		return out, err
	}
	p, err := core.Query(s, securityevents.Filter{Limit: securityevents.MaxEvents})
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT event_id FROM adtr.simulation_event_case_evidence WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND case_id=$5 ORDER BY event_id LIMIT 4097`, s.Tenant, s.Domain, id, d.FixtureHash, caseID)
	if err != nil {
		return out, err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var eid string
		if err = rows.Scan(&eid); err != nil {
			rows.Close()
			return out, err
		}
		ids[eid] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) == 0 || len(ids) > securityevents.MaxEvents {
		return out, ErrInvalid
	}
	for _, e := range p.Events {
		if ids[e.ID] {
			out.Evidence = append(out.Evidence, e)
		}
	}
	if len(out.Evidence) != len(ids) {
		return out, ErrInvalid
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM adtr.simulation_event_transitions WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND case_id=$5`, s.Tenant, s.Domain, id, d.FixtureHash, caseID).Scan(&out.HistoryTotal); err != nil {
		return out, err
	}
	if out.HistoryTotal != int(out.Case.Revision)-1 {
		return out, ErrInvalid
	}
	// Read the bounded complete history and validate it by replay, then page the
	// response. Stored JSON is never trusted as a state-machine instruction.
	rows, err = tx.Query(ctx, `SELECT revision,audit FROM adtr.simulation_event_transitions WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND case_id=$5 ORDER BY revision LIMIT 1025`, s.Tenant, s.Domain, id, d.FixtureHash, caseID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	replayScope := s
	replayScope.Manage = true // validation only, never a persistence grant
	replay, err := securityevents.NewCase(replayScope, provenance(d), caseID)
	if err != nil {
		return out, err
	}
	n := 0
	for rows.Next() {
		var b []byte
		var rev int64
		var a securityevents.Audit
		if err = rows.Scan(&rev, &b); err != nil {
			return out, err
		}
		if len(b) > 4096 || json.Unmarshal(b, &a) != nil || rev != int64(n+2) || a.Revision != uint64(rev) {
			return out, ErrInvalid
		}
		next, expected, e := replay.Apply(replayScope, securityevents.Change{Action: a.Action, Actor: a.Actor, Reason: a.Reason, At: a.At, ExpectedRevision: uint64(n + 1)})
		if e != nil || expected != a {
			return out, ErrInvalid
		}
		replay = next
		if n >= offset && len(out.History) < limit {
			out.History = append(out.History, a)
		}
		n++
		if n > MaxHistory {
			return out, ErrLimit
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	actual, err := c.Snapshot(s)
	if err != nil {
		return out, err
	}
	restored, err := replay.Snapshot(s)
	if err != nil {
		return out, err
	}
	if n != out.HistoryTotal || actual != restored {
		return out, ErrInvalid
	}
	return out, nil
}
