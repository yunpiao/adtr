package simulationrisk

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
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

func valid(v string, max int) bool {
	if v == "" || len(v) > max || !utf8.ValidString(v) || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func authorize(tx pgx.Tx, s Scope, manage bool) error {
	if tx == nil || !valid(s.Tenant, 128) || !valid(s.Domain, 128) || !s.Read || (manage && !s.Manage) {
		return ErrDenied
	}
	return nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func lock(ctx context.Context, tx pgx.Tx, parts ...string) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "simulation-risk:"+digest(parts))
	return e
}
func dataset(ctx context.Context, tx pgx.Tx, s Scope, id string) (simulationevents.Dataset, []securityevents.Event, error) {
	var d simulationevents.Dataset
	if !valid(id, 128) {
		return d, nil, ErrInvalid
	}
	e := tx.QueryRow(ctx, `SELECT dataset_id,fixture,fixture_hash,contract,simulation,event_count FROM adtr.simulation_event_datasets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3`, s.Tenant, s.Domain, id).Scan(&d.ID, &d.Fixture, &d.FixtureHash, &d.Contract, &d.Simulation, &d.EventCount)
	if errors.Is(e, pgx.ErrNoRows) {
		return d, nil, ErrNotFound
	}
	if e != nil {
		return d, nil, e
	}
	hash, he := hex.DecodeString(d.FixtureHash)
	if d.ID != id || he != nil || len(hash) != 32 || strings.ToLower(d.FixtureHash) != d.FixtureHash || d.EventCount < 0 || d.EventCount > securityevents.MaxEvents {
		return d, nil, ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT event_id,payload FROM adtr.simulation_events WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 ORDER BY event_id LIMIT 4097`, s.Tenant, s.Domain, id, d.FixtureHash)
	if e != nil {
		return d, nil, e
	}
	defer rows.Close()
	raw := []securityevents.RawEvent{}
	for rows.Next() {
		var id string
		var b []byte
		var r securityevents.RawEvent
		if e = rows.Scan(&id, &b); e != nil {
			return d, nil, e
		}
		if len(b) > 4096 {
			return d, nil, ErrLimit
		}
		if json.Unmarshal(b, &r) != nil || r.ID != id {
			return d, nil, ErrInvalid
		}
		raw = append(raw, r)
		if len(raw) > securityevents.MaxEvents {
			return d, nil, ErrLimit
		}
	}
	if e = rows.Err(); e != nil {
		return d, nil, e
	}
	if len(raw) != d.EventCount {
		return d, nil, ErrInvalid
	}
	scope := securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true}
	core, e := securityevents.Normalize(scope, securityevents.Provenance{Contract: d.Contract, Fixture: d.Fixture, Synthetic: d.Simulation}, raw)
	if e != nil {
		return d, nil, e
	}
	p, e := core.Query(scope, securityevents.Filter{Limit: securityevents.MaxEvents})
	return d, p.Events, e
}
func mapped(s Scope, subject string, events []securityevents.Event) ([]riskscore.Event, int, error) {
	out := []riskscore.Event{}
	n := 0
	found := false
	for _, e := range events {
		if e.Actor != subject {
			continue
		}
		found = true
		kind := ""
		switch e.Kind {
		case "login_failure":
			kind = "low"
		case "service_access":
			kind = "medium"
		case "login_success":
			n++
			continue
		default:
			return nil, 0, ErrInvalid
		}
		out = append(out, riskscore.Event{ID: "event:" + digest(e.ID), Scope: riskscore.Scope{TenantID: s.Tenant, DomainID: s.Domain, UserID: subject}, Kind: kind, OccurredAt: e.OccurredAt})
	}
	if !found {
		return nil, 0, ErrNotFound
	}
	return out, n, nil
}
func loadResets(ctx context.Context, tx pgx.Tx, s Scope, d simulationevents.Dataset, subject string) ([]riskscore.Reset, error) {
	rows, e := tx.Query(ctx, `SELECT revision,reset_id,at,reason,actor,operation_id FROM adtr.simulation_risk_resets WHERE tenant_id=$1 AND domain_id=$2 AND dataset_id=$3 AND fixture_hash=$4 AND subject=$5 ORDER BY revision LIMIT 1025`, s.Tenant, s.Domain, d.ID, d.FixtureHash, subject)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []riskscore.Reset{}
	for rows.Next() {
		var rev int64
		var r riskscore.Reset
		var actor, op string
		if e = rows.Scan(&rev, &r.ID, &r.At, &r.Reason, &actor, &op); e != nil {
			return nil, e
		}
		if rev != int64(len(out)+2) || !valid(actor, 128) || !valid(op, 128) || !valid(r.Reason, 512) || r.ID != "reset:"+digest([]string{actor, op}) || r.At.IsZero() || (len(out) > 0 && r.At.Before(out[len(out)-1].At)) {
			return nil, ErrInvalid
		}
		r.Scope = riskscore.Scope{TenantID: s.Tenant, DomainID: s.Domain, UserID: subject}
		out = append(out, r)
		if len(out) > MaxResets {
			return nil, ErrLimit
		}
	}
	return out, rows.Err()
}
func evaluate(s Scope, d simulationevents.Dataset, subject string, events []securityevents.Event, resets []riskscore.Reset, at time.Time) (Result, error) {
	ev, n, e := mapped(s, subject, events)
	if e != nil {
		return Result{}, e
	}
	core, e := riskscore.Evaluate(riskscore.Request{Scope: riskscore.Scope{TenantID: s.Tenant, DomainID: s.Domain, UserID: subject}, AsOf: at, Events: ev, Resets: resets})
	if e != nil {
		return Result{}, ErrInvalid
	}
	return Result{Result: core, Dataset: d, Revision: uint64(len(resets) + 1), Contract: Contract, MappingVersion: MappingVersion, SourceEventCount: len(ev) + n, NoRiskEventCount: n}, nil
}

// liveBoundary rejects a regressing database clock before evaluating live state.
// Historical receipt replay intentionally bypasses it and uses its recorded
// timestamp and immutable reset prefix, independent of the current wall clock.
func liveBoundary(resets []riskscore.Reset, now time.Time) error {
	if len(resets) > 0 && now.Before(resets[len(resets)-1].At) {
		return ErrConflict
	}
	return nil
}

// EvaluateTx reads complete bounded evidence and history under the reset lock.
func EvaluateTx(ctx context.Context, tx pgx.Tx, s Scope, id, subject string) (Result, error) {
	if e := authorize(tx, s, false); e != nil {
		return Result{}, e
	}
	if !valid(subject, 128) || !valid(id, 128) {
		return Result{}, ErrInvalid
	}
	if e := lock(ctx, tx, "scope", s.Tenant, s.Domain, id, subject); e != nil {
		return Result{}, e
	}
	d, events, e := dataset(ctx, tx, s, id)
	if e != nil {
		return Result{}, e
	}
	resets, e := loadResets(ctx, tx, s, d, subject)
	if e != nil {
		return Result{}, e
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return Result{}, e
	}
	if e = liveBoundary(resets, now); e != nil {
		return Result{}, e
	}
	return evaluate(s, d, subject, events, resets, now)
}
