package simulationdnsip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/dnsipxlsx"
	"github.com/yunpiao/adtr/internal/tasks"
	"time"
)

func columns() []string {
	return []string{"ID", "Domain", "DNS name", "IP", "Observation", "Source", "Contract version", "Simulation"}
}
func validate(raw json.RawMessage) (json.RawMessage, error) {
	var p Payload
	if e := strict(raw, 2048, &p); e != nil {
		return nil, e
	}
	if p.Contract != Contract || !identifier.MatchString(p.ObservationID) || !hashPattern.MatchString(p.ObservationHash) {
		return nil, ErrInvalid
	}
	if _, e := dnsipassets.Select(nil, dnsipassets.Filter{Domain: p.DNSDomain, Observation: p.ObservationID, Keyword: p.Keyword, Family: p.Family}); e != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(p)
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxArtifactBytes-b.Len() {
		return 0, ErrLimit
	}
	return b.Buffer.Write(p)
}
func render(ctx context.Context, t tasks.Task, v storedObservation, p Payload) (Manifest, []byte, error) {
	var m Manifest
	if v.Observation.Hash != p.ObservationHash || v.Observation.ID != p.ObservationID || v.Observation.DNSDomain != p.DNSDomain {
		return m, nil, ErrConflict
	}
	f := dnsipassets.Filter{Domain: p.DNSDomain, Observation: p.ObservationID, Keyword: p.Keyword, Family: p.Family}
	rows, e := dnsipassets.Select(v.Records, f)
	if e != nil {
		return m, nil, ErrInvalid
	}
	var out boundedBuffer
	if e = dnsipxlsx.WriteRecords(ctx, &out, v.Records, f); e != nil {
		return m, nil, e
	}
	b := out.Bytes()
	m = Manifest{Contract: Contract, Simulation: true, TaskID: t.ID, Payload: p, RowCount: len(rows), Columns: columns(), Bytes: len(b), SHA256: sum(b), Filename: "SIMULATED-dns-ip-" + t.ID + ".xlsx"}
	return m, b, nil
}
func intended(ctx context.Context, tx pgx.Tx, t tasks.Task, p Payload) error {
	var op string
	e := tx.QueryRow(ctx, `SELECT operation_id FROM adtr.simulation_dns_ip_receipts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND task_id=$4 AND action='export'`, t.TenantID, t.DomainID, t.ActorID, t.ID).Scan(&op)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrDenied
	}
	if e != nil {
		return e
	}
	r, e := receipt(ctx, tx, Scope{Tenant: t.TenantID, Domain: t.DomainID, ActorID: t.ActorID}, op)
	if e != nil {
		return e
	}
	in := r.Intent.Export
	if in == nil || in.ObservationID != p.ObservationID || in.ObservationHash != p.ObservationHash || in.Keyword != p.Keyword || in.Family != p.Family {
		return ErrInvalid
	}
	return nil
}
func denied(e error) bool {
	var x *tasks.Error
	return errors.Is(e, ErrDenied) || errors.Is(e, tasks.ErrAuthorization) || errors.As(e, &x) && x.Status == 403
}

// NewKind uses existing engine-owned fencing/transactions; no separate pool or scheduler.
func NewKind(auth tasks.Authorizer) tasks.Kind {
	k := tasks.Kind{Name: KindName, Version: 1, OwnerScoped: true, ReplaySafe: true, CancelDiscardsResult: true, MaxAttempts: 1, Lease: 30 * time.Second, Heartbeat: 5 * time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second, Validate: validate}
	check := func(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
		if auth == nil {
			return ErrDenied
		}
		v, e := auth(ctx, tx, tasks.Principal{TenantID: t.TenantID, ActorID: t.ActorID}, tasks.Scope{DomainID: t.DomainID, TaskName: KindName}, tasks.Execute)
		if e != nil {
			return e
		}
		if v == "" || v != t.AuthorizationVersion {
			return tasks.ErrAuthorization
		}
		return nil
	}
	prepare := func(ctx context.Context, tx pgx.Tx, t tasks.Task) (Manifest, []byte, error) {
		var m Manifest
		raw, e := validate(t.Payload)
		if e != nil {
			return m, nil, e
		}
		var p Payload
		json.Unmarshal(raw, &p)
		if e = check(ctx, tx, t); e != nil {
			return m, nil, e
		}
		if e = intended(ctx, tx, t, p); e != nil {
			return m, nil, e
		}
		v, e := observation(ctx, tx, Scope{Tenant: t.TenantID, Domain: t.DomainID, ActorID: t.ActorID}, p.ObservationID)
		if e != nil {
			return m, nil, e
		}
		return render(ctx, t, v, p)
	}
	k.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		if ex.WithTx == nil {
			return tasks.Outcome{State: tasks.Failed, Code: "invalid_input"}
		}
		var result []byte
		_, e := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			m, _, e := prepare(ctx, tx, ex.Task)
			if e != nil {
				return 0, nil, nil, e
			}
			result, e = json.Marshal(m)
			if e != nil {
				return 0, nil, nil, e
			}
			if e = check(ctx, tx, ex.Task); e != nil {
				return 0, nil, nil, e
			}
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), nil
		})
		if e != nil {
			state, code := tasks.Failed, "export_failed"
			if denied(e) {
				code = "authorization_revoked"
			}
			if errors.Is(e, tasks.ErrCancelRequested) {
				state, code = tasks.Cancelled, "cancelled"
			}
			if errors.Is(e, ErrLimit) {
				code = "budget_exceeded"
			}
			return tasks.Outcome{State: state, Code: code}
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: result}
	}
	k.ValidateSuccess = func(ctx context.Context, tx pgx.Tx, t tasks.Task, result json.RawMessage) (string, error) {
		m, b, e := prepare(ctx, tx, t)
		if e != nil {
			if denied(e) {
				return "authorization_revoked", nil
			}
			if errors.Is(e, ErrInvalid) || errors.Is(e, ErrConflict) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrLimit) {
				return "invalid_result", nil
			}
			return "", e
		}
		var supplied Manifest
		if strict(result, 16384, &supplied) != nil || digest(supplied) != digest(m) {
			return "invalid_result", nil
		}
		raw, _ := json.Marshal(m)
		var existing []byte
		var manifest []byte
		e = tx.QueryRow(ctx, `SELECT manifest,workbook FROM adtr.simulation_dns_ip_artifacts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND task_id=$4`, t.TenantID, t.DomainID, t.ActorID, t.ID).Scan(&manifest, &existing)
		if e == nil {
			var old Manifest
			if strict(manifest, 16384, &old) != nil || digest(old) != digest(m) || !bytes.Equal(existing, b) {
				return "invalid_result", nil
			}
			return "", nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return "", e
		}
		_, e = tx.Exec(ctx, `INSERT INTO adtr.simulation_dns_ip_artifacts VALUES($1,$2,$3,$4,$5,$6)`, t.TenantID, t.DomainID, t.ActorID, t.ID, raw, b)
		return "", e
	}
	return k
}

// DownloadTx never exposes an intermediate or terminal-failed candidate. The same
// engine authorizer checks current owner/epoch; regrant cannot revive an old task.
func DownloadTx(ctx context.Context, tx pgx.Tx, s Scope, engine *tasks.Engine, id string) (Manifest, []byte, error) {
	var m Manifest
	d, e := ReadExportTx(ctx, tx, s, engine, id)
	if e != nil {
		return m, nil, e
	}
	if d.Task.State != tasks.Succeeded {
		return m, nil, ErrConflict
	}
	if strict(d.Task.Result, 16384, &m) != nil {
		return Manifest{}, nil, ErrInvalid
	}
	var b, raw []byte
	e = tx.QueryRow(ctx, `SELECT manifest,workbook FROM adtr.simulation_dns_ip_artifacts WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND task_id=$4`, s.Tenant, s.Domain, s.ActorID, id).Scan(&raw, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return Manifest{}, nil, ErrNotFound
	}
	if e != nil {
		return Manifest{}, nil, e
	}
	var stored Manifest
	if strict(raw, 16384, &stored) != nil || digest(stored) != digest(m) || m.TaskID != id || m.Contract != Contract || !m.Simulation || m.Bytes != len(b) || len(b) > MaxArtifactBytes || sum(b) != m.SHA256 {
		return Manifest{}, nil, ErrInvalid
	}
	var p Payload
	if strict(d.Task.Payload, 2048, &p) != nil || p != m.Payload {
		return Manifest{}, nil, ErrInvalid
	}
	if e = intended(ctx, tx, d.Task, p); e != nil {
		return Manifest{}, nil, e
	}
	v, e := observation(ctx, tx, s, p.ObservationID)
	if e != nil {
		return Manifest{}, nil, e
	}
	expected, _, e := render(ctx, d.Task, v, p)
	if e != nil {
		return Manifest{}, nil, e
	}
	if digest(expected) != digest(m) {
		return Manifest{}, nil, ErrInvalid
	}
	return m, b, nil
}
