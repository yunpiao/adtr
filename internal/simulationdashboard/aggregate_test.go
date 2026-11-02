package simulationdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
)

func fixture(t *testing.T) (Scope, Input, source) {
	t.Helper()
	at := time.Date(2026, 11, 2, 12, 0, 30, 0, time.UTC)
	p := securityevents.Provenance{Contract: securityevents.ContractVersion, Fixture: "synthetic-dashboard", Synthetic: true}
	s := Scope{Tenant: "tenant", Domain: "domain", ActorID: 1, AuthorizationVersion: "epoch", Read: true}
	src := source{AsOf: at, Dataset: simulationevents.Dataset{ID: "dataset", Fixture: p.Fixture, FixtureHash: strings.Repeat("a", 64), Contract: p.Contract, Simulation: true, EventCount: 4}, Cases: []caseSource{{ID: "case", State: "open", Revision: 1, EvidenceCount: 1}}}
	for i, delta := range []time.Duration{-time.Hour, -time.Minute, 0, time.Second} {
		r := securityevents.RawEvent{ID: string(rune('a' + i)), Tenant: s.Tenant, Domain: s.Domain, Kind: "login_failure", Actor: "actor", Device: "device", Session: "session", SourceIP: "192.0.2.1", OccurredAt: at.Add(delta).Format(time.RFC3339Nano), Provenance: p}
		b, _ := json.Marshal(r)
		src.Events = append(src.Events, eventSource{ID: r.ID, Payload: b, Bytes: len(b)})
	}
	return s, Input{DatasetID: "dataset", Subject: "actor", Window: "1h"}, src
}
func TestAggregateWindowAndUnavailable(t *testing.T) {
	s, in, src := fixture(t)
	out, e := aggregate(context.Background(), s, in, src)
	if e != nil {
		t.Fatal(e)
	}
	if out.EventActivity.Total != 2 || out.Coverage.ExcludedFutureEvents != 2 || out.Cases.Open != 1 || out.CaseCloseThroughput.WindowTotal != 0 || out.Risk.Score == nil || *out.Risk.Score != 15 {
		t.Fatalf("wrong bounded facts: %+v", out)
	}
	if len(out.EventActivity.Buckets) != 61 || len(out.CaseCloseThroughput.Buckets) != 61 || len(out.Unavailable) != 11 || out.LatestManualDetection.Status != "no_result" || out.LatestManualDetection.EventCount != nil {
		t.Fatal("coverage/unavailable", out)
	}
	again, e := aggregate(context.Background(), s, in, src)
	if e != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("not deterministic", e)
	}
	in.Subject = ""
	out, e = aggregate(context.Background(), s, in, src)
	if e != nil || out.Risk.Status != "not_selected" || out.Risk.Score != nil {
		t.Fatal(out.Risk, e)
	}
	in.Subject = "absent"
	out, e = aggregate(context.Background(), s, in, src)
	if e != nil || out.Risk.Status != "subject_not_found" || out.Risk.Subject != "absent" {
		t.Fatal(out.Risk, e)
	}
}
func TestAggregateCloseAuditBinding(t *testing.T) {
	s, in, src := fixture(t)
	p := securityevents.Provenance{Contract: src.Dataset.Contract, Fixture: src.Dataset.Fixture, Synthetic: true}
	core, _ := securityevents.NewCase(securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true, Manage: true}, p, "case")
	at := src.AsOf.Add(-10 * time.Second)
	_, a, e := core.Apply(securityevents.Scope{Tenant: s.Tenant, Domain: s.Domain, Read: true, Manage: true}, securityevents.Change{Action: "close", Actor: "reviewer", Reason: "synthetic", At: at, ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(a)
	src.Cases[0] = caseSource{ID: "case", State: "closed", Revision: 2, UpdatedAt: &at, Audit: b, AuditBytes: len(b), EvidenceCount: 1}
	out, e := aggregate(context.Background(), s, in, src)
	if e != nil || out.Cases.Closed != 1 || out.CaseCloseThroughput.Last60Seconds != 1 || out.CaseCloseThroughput.WindowTotal != 1 {
		t.Fatal(out.Cases, e)
	}
	a.Action = "mark_benign"
	src.Cases[0].Audit, _ = json.Marshal(a)
	if _, e = aggregate(context.Background(), s, in, src); !errors.Is(e, ErrInvalid) {
		t.Fatal("forged close accepted", e)
	}
}
func TestAggregateRejectsCorruptAndOversizedSources(t *testing.T) {
	tests := map[string]func(*source){"duplicate": func(s *source) { s.Events[1] = s.Events[0] }, "wrong count": func(s *source) { s.Dataset.EventCount++ }, "bytes": func(s *source) { s.Events[0].Bytes = 4097 }, "case overflow": func(s *source) { s.Cases = make([]caseSource, 101) }, "reset overflow": func(s *source) { s.Oversized = true }, "invalid hash": func(s *source) { s.Dataset.FixtureHash = "x" }, "missing case evidence": func(s *source) { s.Cases[0].EvidenceCount = 0 }, "bad revision": func(s *source) { s.Cases[0].Revision = 0 }}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			s, in, src := fixture(t)
			change(&src)
			if _, e := aggregate(context.Background(), s, in, src); e == nil {
				t.Fatal("corrupt source accepted")
			}
		})
	}
}
func TestRanksAndEmptySources(t *testing.T) {
	got := ranked(map[string]int{"z": 1, "a": 1, "c": 2, "d": 3, "f": 4, "e": 5})
	if len(got) != 5 || got[0].Name != "e" || got[4].Name != "a" {
		t.Fatal(got)
	}
	s, in, src := fixture(t)
	src.Events = nil
	src.Cases = nil
	src.Dataset.EventCount = 0
	in.Subject = ""
	out, e := aggregate(context.Background(), s, in, src)
	if e != nil || out.EventActivity.Total != 0 || out.Coverage.SourceFirstAt != nil || len(out.EventActivity.TopActors) != 0 {
		t.Fatal(out, e)
	}
	for _, p := range []string{"1h", "24h", "7d"} {
		in.Window = p
		out, e = aggregate(context.Background(), s, in, src)
		if e != nil || len(out.CaseCloseThroughput.Buckets) > 10081 {
			t.Fatal(p, e)
		}
	}
}

type snapshotTx struct {
	pgx.Tx
	calls int
	data  []byte
	query string
	args  []any
	err   error
}

func (t *snapshotTx) QueryRow(_ context.Context, q string, a ...any) pgx.Row {
	t.calls++
	t.query = q
	t.args = a
	return snapshotRow{t.data, t.err}
}

type snapshotRow struct {
	b []byte
	e error
}

func (r snapshotRow) Scan(dest ...any) error {
	if r.e != nil {
		return r.e
	}
	*dest[0].(*[]byte) = r.b
	return nil
}
func TestSummarySingleSourceStatementAndScope(t *testing.T) {
	s, in, src := fixture(t)
	b, _ := json.Marshal(src)
	tx := &snapshotTx{data: b}
	if _, e := SummaryTx(context.Background(), tx, s, in); e != nil {
		t.Fatal(e)
	}
	if tx.calls != 1 || tx.query != sourceSQL || !reflect.DeepEqual(tx.args, []any{s.Tenant, s.Domain, in.DatasetID, in.Subject, s.ActorID, s.AuthorizationVersion}) {
		t.Fatal("source scope/query count")
	}
	for _, fragment := range []string{"statement_timestamp()", "LIMIT 4097", "LIMIT 101", "LIMIT 1025", "t.actor_id=$5 AND t.authorization_version=$6", "NOT COALESCE(v.archived,false)"} {
		if !strings.Contains(tx.query, fragment) {
			t.Fatal("missing", fragment)
		}
	}
	s.Read = false
	tx.calls = 0
	if _, e := SummaryTx(context.Background(), tx, s, in); !errors.Is(e, ErrDenied) || tx.calls != 0 {
		t.Fatal(e)
	}
	s.Read = true
	in.Window = "forever"
	if _, e := SummaryTx(context.Background(), tx, s, in); !errors.Is(e, ErrInvalid) || tx.calls != 0 {
		t.Fatal(e)
	}
	in.Window = "1h"
	tx.err = pgx.ErrNoRows
	if _, e := SummaryTx(context.Background(), tx, s, in); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
