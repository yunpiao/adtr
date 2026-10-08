package credentialuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/tasks"
)

func validInput() Input {
	return Input{AccountID: "account", RoleID: "platform_admin", Purpose: Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "key"}
}
func TestInputExactPurposeAndCanonicalRevisions(t *testing.T) {
	in := validInput()
	if e := ValidateInput("/grant", &in); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Input){
		func(i *Input) { i.Purpose = "ldap.query" }, func(i *Input) { i.RoleID = "admin" }, func(i *Input) { i.AccountID = "platform" }, func(i *Input) { i.IdempotencyKey = "schedule_reserved" },
		func(i *Input) { i.ExpectedAccountRevision = "01" }, func(i *Input) { i.ExpectedCredentialRevision = "+1" }, func(i *Input) { i.ExpectedGrantRevision = "-1" }, func(i *Input) { i.ExpectedGrantRevision = "9223372036854775808" },
	} {
		bad := in
		change(&bad)
		if ValidateInput("/grant", &bad) == nil {
			t.Fatal("invalid mutation accepted")
		}
	}
	if ValidateInput("/revoke", &in) == nil {
		t.Fatal("absence cannot be revoked")
	}
	in.ExpectedGrantRevision = "9223372036854775807"
	if e := ValidateInput("/revoke", &in); e != nil {
		t.Fatal(e)
	}
}
func TestMetadataFingerprintBindsFullIntentAndPrincipal(t *testing.T) {
	in := validInput()
	p := tasks.Principal{TenantID: "tenant", ActorID: 1}
	first := fingerprint(p, "/grant", in)
	if len(first) != 64 || fingerprint(p, "/grant", in) != first {
		t.Fatal("invalid digest")
	}
	for _, change := range []func(*Input){func(i *Input) { i.AccountID = "other" }, func(i *Input) { i.RoleID = strings.Repeat("r", 24) }, func(i *Input) { i.Purpose = "other" }, func(i *Input) { i.ExpectedAccountRevision = "2" }, func(i *Input) { i.ExpectedCredentialRevision = "2" }, func(i *Input) { i.ExpectedGrantRevision = "2" }, func(i *Input) { i.IdempotencyKey = "other" }} {
		next := in
		change(&next)
		if fingerprint(p, "/grant", next) == first {
			t.Fatal("intent omitted from digest")
		}
	}
	for _, other := range []tasks.Principal{{TenantID: "other", ActorID: 1}, {TenantID: "tenant", ActorID: 2}} {
		if fingerprint(other, "/grant", in) == first {
			t.Fatal("principal omitted")
		}
	}
	if fingerprint(p, "/revoke", in) == first {
		t.Fatal("operation omitted")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", in, in), in.AccountID) {
		t.Fatal("accidental request logging")
	}
	b, e := json.Marshal(in)
	if e != nil || string(b) != `"[credential use mutation]"` {
		t.Fatal(string(b), e)
	}
}
func TestCleanupFilterRejectsAmbiguousAndUnboundedViews(t *testing.T) {
	for _, q := range []string{"pageIdx=01", "pageIdx=0", "pageSize=0", "pageSize=-1", "pageSize=15", "pageSize=60", "pageSize=10&pageSize=20", "roleId=x", "keyword=%00"} {
		v, e := url.ParseQuery(q)
		if e == nil {
			_, e = ParseFilter(v)
		}
		if e == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	f, e := ParseFilter(url.Values{"pageIdx": {"2"}, "pageSize": {"30"}, "keyword": {"example"}})
	if e != nil || f.PageIdx != 2 || f.PageSize != 30 || f.Keyword != "example" {
		t.Fatal(f, e)
	}
}

type contextTx struct {
	pgx.Tx
	calls int
	query string
	args  []any
}

func (x *contextTx) Exec(_ context.Context, q string, a ...any) (pgconn.CommandTag, error) {
	x.calls++
	x.query = q
	x.args = a
	return pgconn.CommandTag{}, nil
}
func TestGovernanceContextsAreNarrowAndTransactionLocal(t *testing.T) {
	ctx := context.Background()
	p := tasks.Principal{TenantID: "tenant", ActorID: 7}
	tx := &contextTx{}
	if e := SetGovernanceContext(ctx, tx, p, GrantUse); e != nil {
		t.Fatal(e)
	}
	if tx.calls != 1 || len(tx.args) != 3 || tx.args[0] != "tenant" || tx.args[1] != "7" || tx.args[2] != "grant" || !strings.Contains(tx.query, "credential_governance_v1") || !strings.Contains(tx.query, "'adtr.credential_use_scope','',true") {
		t.Fatal("context lacks identity/local scope reset")
	}
	for _, op := range []GovernanceOperation{ManageRoles, ManageTenant, ResetPassword, MutateAccount, RevokeUse} {
		if e := SetGovernanceContext(ctx, tx, p, op); e != nil {
			t.Fatal(e)
		}
	}
	for _, op := range []SelfOperation{ChangePassword, BeginMFA, ConfirmMFA, DisableMFA} {
		if e := SetSelfContext(ctx, tx, p, op); e != nil {
			t.Fatal(e)
		}
	}
	calls := tx.calls
	if SetGovernanceContext(ctx, tx, p, GovernanceOperation(ChangePassword)) == nil || SetSelfContext(ctx, tx, p, SelfOperation(ManageRoles)) == nil || SetGovernanceContext(ctx, tx, tasks.Principal{}, GrantUse) == nil || tx.calls != calls {
		t.Fatal("invalid context reached SQL")
	}
	if !IsGovernanceError(&pgconn.PgError{Code: "42501", ConstraintName: "credential_use_governance"}) || IsGovernanceError(&pgconn.PgError{Code: "42501", Message: "other"}) {
		t.Fatal("unsafe error classification")
	}
}

func TestCleanupKeywordRejectsControlsButKeepsLiteralUnicode(t *testing.T) {
	for _, keyword := range []string{"line\nbreak", "tab\tkey", "delete\x7f", "next\u0085line", "zero\x00"} {
		if _, err := ParseFilter(url.Values{"keyword": {keyword}}); err == nil {
			t.Fatalf("accepted control keyword %q", keyword)
		}
	}
	for _, keyword := range []string{"", "数据库", "%_\\", strings.Repeat("界", 50)} {
		f, err := ParseFilter(url.Values{"keyword": {keyword}})
		if err != nil || f.Keyword != keyword {
			t.Fatalf("literal keyword changed: %q %v", keyword, err)
		}
	}
}
