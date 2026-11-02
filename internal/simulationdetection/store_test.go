package simulationdetection

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/tasks"
	"testing"
)

func TestMappingAndRules(t *testing.T) {
	s := Scope{Tenant: "tenant", Domain: "domain"}
	ev := mapped(s, []securityevents.Event{{ID: "event", Kind: "login_failure", Actor: "alice", Device: "dc", SourceIP: "192.0.2.1"}})
	r := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: ev[0].Scope, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}, {Field: "event.category", Operator: "eq", Values: []string{"login_failure"}}, {Field: "actor.name", Operator: "eq", Values: []string{"alice"}}}}
	p, e := detectionrules.Compile(ev[0].Scope, []detectionrules.Rule{r})
	if e != nil {
		t.Fatal(e)
	}
	m, e := p.Evaluate(context.Background(), ev)
	if e != nil || len(m) != 1 {
		t.Fatal(m, e)
	}
	if _, ok := ev[0].Fields["event.code"]; ok {
		t.Fatal("fabricated event code")
	}
	ev[0].Scope.Domain = "other"
	if _, e = p.Evaluate(context.Background(), ev); !errors.Is(e, detectionrules.ErrScope) {
		t.Fatal(e)
	}
}
func TestPinnedPayloadAndPolicy(t *testing.T) {
	p := Payload{TemplateID: "template", TemplateVersion: 1, TemplateHash: digest("template"), DatasetID: "dataset", FixtureHash: digest("fixture"), MappingVersion: MappingVersion}
	b, _ := json.Marshal(p)
	if _, e := validate(b); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{}`, string(b[:len(b)-1]) + `,"sql":"DROP TABLE"}`, string(b) + `{}`} {
		if _, e := validate([]byte(bad)); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	k := NewKind(nil)
	if _, e := tasks.NewRegistry(k); e != nil {
		t.Fatal(e)
	}
	if k.Schedulable || !k.OwnerScoped || !k.CancelDiscardsResult || k.MaxAttempts != 1 {
		t.Fatal("unsafe policy")
	}
	o := k.Execute(context.Background(), tasks.Execution{})
	if o.State != tasks.Failed {
		t.Fatal(o)
	}
}
func TestFencingAndCancellationFailures(t *testing.T) {
	p := Payload{TemplateID: "template", TemplateVersion: 1, TemplateHash: digest("t"), DatasetID: "dataset", FixtureHash: digest("d"), MappingVersion: MappingVersion}
	b, _ := json.Marshal(p)
	for _, err := range []error{tasks.ErrLeaseLost, tasks.ErrCancelRequested, tasks.ErrAuthorization, ErrLimit} {
		k := NewKind(nil)
		o := k.Execute(context.Background(), tasks.Execution{Task: tasks.Task{Payload: b}, WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) { return 0, err }})
		if o.State == tasks.Succeeded || len(o.Result) > 0 {
			t.Fatal(o)
		}
		if err == tasks.ErrCancelRequested && o.State != tasks.Cancelled {
			t.Fatal(o)
		}
		if err == ErrLimit && o.Code != "budget_exceeded" {
			t.Fatal(o)
		}
	}
}
func TestLiveSuccessGateRevocation(t *testing.T) {
	k := NewKind(func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "new-epoch", nil
	})
	code, e := k.ValidateSuccess(context.Background(), nil, tasks.Task{AuthorizationVersion: "old-epoch"}, nil)
	if e != nil || code != "authorization_revoked" {
		t.Fatal(code, e)
	}
}
func TestDefaultDeny(t *testing.T) {
	if _, e := ListTx(context.Background(), nil, Scope{}); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if _, e := MutateTx(context.Background(), nil, Scope{}, Mutation{}, nil); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
}

func TestTemplateRuleVersionBinding(t *testing.T) {
	s := Scope{Tenant: "tenant", Domain: "domain"}
	r := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	if err := validateRules(s, 1, []detectionrules.Rule{r}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int64{0, 2, MaxVersions + 1} {
		if err := validateRules(s, v, []detectionrules.Rule{r}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("version %d accepted: %v", v, err)
		}
	}
	if r.Version != 1 || r.Contract != detectionrules.ContractVersion || r.Provenance != detectionrules.Provenance || r.Scope.Domain != s.Domain {
		t.Fatal("validator rewrote server metadata")
	}
	r.Version = 2
	if err := validateRules(s, 2, []detectionrules.Rule{r}); err != nil {
		t.Fatal(err)
	}
	r.Scope.Domain = "other"
	if err := validateRules(s, 2, []detectionrules.Rule{r}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-scope declaration accepted", err)
	}
}

type versionRow struct {
	hash string
	body []byte
}

func (r versionRow) Scan(dest ...any) error {
	*dest[0].(*string) = r.hash
	*dest[1].(*[]byte) = r.body
	return nil
}

type versionTx struct {
	pgx.Tx
	row versionRow
}

func (tx versionTx) QueryRow(context.Context, string, ...any) pgx.Row { return tx.row }
func TestPersistedRuleVersionMismatchRejected(t *testing.T) {
	s := Scope{Tenant: "tenant", Domain: "domain"}
	r := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "rule", Version: 1, Scope: detectionrules.Scope{Tenant: s.Tenant, Domain: s.Domain}, Enabled: true, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}}}}
	body := Template{ID: "template", Name: "Synthetic", Version: 2, Rules: []detectionrules.Rule{r}, Simulation: true}
	body.Hash = digest(body)
	raw, _ := json.Marshal(body)
	if _, err := version(context.Background(), versionTx{row: versionRow{body.Hash, raw}}, s, "template", 2); !errors.Is(err, ErrInvalid) {
		t.Fatal("persisted mismatched version accepted", err)
	}
	body.Rules[0].Version = 2
	body.Hash = ""
	body.Hash = digest(body)
	raw, _ = json.Marshal(body)
	if got, err := version(context.Background(), versionTx{row: versionRow{body.Hash, raw}}, s, "template", 2); err != nil || got.Rules[0].Version != 2 {
		t.Fatal(got, err)
	}
}

func TestAuthorizationFailureClassificationAndPublicationRollback(t *testing.T) {
	infrastructure := errors.New("injected non-aborting infrastructure failure")
	tests := []struct {
		name, epoch string
		err         error
		denied      bool
	}{
		{name: "infrastructure", err: infrastructure},
		{name: "cancelled-context", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "explicit-denial", err: tasks.ErrAuthorization, denied: true},
		{name: "module-denial", err: ErrDenied, denied: true},
		{name: "http-forbidden", err: &tasks.Error{Status: 403, Code: "forbidden"}, denied: true},
		{name: "epoch-mismatch", epoch: "new", denied: true},
		{name: "empty-epoch", denied: true},
	}
	p := Payload{TemplateID: "template", TemplateVersion: 1, TemplateHash: digest("template"), DatasetID: "dataset", FixtureHash: digest("fixture"), MappingVersion: MappingVersion}
	raw, _ := json.Marshal(p)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := NewKind(func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
				return tt.epoch, tt.err
			})
			task := tasks.Task{Payload: raw, AuthorizationVersion: "old"}
			var transactionError error
			out := k.Execute(context.Background(), tasks.Execution{Task: task, WithTx: func(ctx context.Context, _ int64, work tasks.FencedWork) (int64, error) {
				_, _, _, transactionError = work(ctx, nil)
				return 0, transactionError
			}})
			if tt.err != nil && !errors.Is(transactionError, tt.err) {
				t.Fatalf("transaction error was replaced: %v", transactionError)
			}
			if tt.denied {
				if out.Code != "authorization_revoked" {
					t.Fatalf("explicit denial not classified: %+v", out)
				}
			} else if out.Code == "authorization_revoked" {
				t.Fatalf("infrastructure became revocation: %+v", out)
			}
			code, err := k.ValidateSuccess(context.Background(), nil, task, nil)
			if tt.denied {
				if code != "authorization_revoked" || err != nil {
					t.Fatalf("denial gate=(%q,%v)", code, err)
				}
			} else {
				// The engine's contract rolls back Finish only for non-nil gate errors.
				// A code with nil error would incorrectly persist a terminal auth failure.
				if code != "" || !errors.Is(err, tt.err) {
					t.Fatalf("publication must roll back: gate=(%q,%v)", code, err)
				}
			}
		})
	}
}
