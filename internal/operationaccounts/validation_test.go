package operationaccounts

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ptr(v string) *string { return &v }
func TestOperationAccountPairAndLabelBoundaries(t *testing.T) {
	base := Input{DomainID: "domain-a", ExpectedDomainRevision: "1", IdempotencyKey: "request", Username: ptr("reader@alternate.test"), Password: ptr("  exact * secret\n")}
	if e := ValidateInput("/create", &base); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Input){
		func(i *Input) { i.Password = nil }, func(i *Input) { i.Username = ptr("") }, func(i *Input) { i.Password = ptr("") }, func(i *Input) { i.Password = ptr("nul\x00") },
		func(i *Input) { i.Password = ptr(strings.Repeat("x", 51)) }, func(i *Input) { i.Username = ptr(" bare ") }, func(i *Input) { i.Label = ptr(" edge") }, func(i *Input) { i.Label = ptr("line\n") }, func(i *Input) { i.Label = ptr(strings.Repeat("界", 51)) },
		func(i *Input) { i.ExpectedDomainRevision = "01" }, func(i *Input) { i.ExpectedDomainRevision = "9223372036854775808" }, func(i *Input) { i.IdempotencyKey = "schedule_unsafe" },
	} {
		in := base
		change(&in)
		if ValidateInput("/create", &in) == nil {
			t.Fatal("invalid pair/metadata accepted")
		}
	}
	in := Input{AccountID: "account-a", ExpectedRevision: "1", IdempotencyKey: "edit", Label: ptr("")}
	if e := ValidateInput("/update", &in); e != nil {
		t.Fatal(e)
	}
	in.Password = ptr("*")
	in.Username = ptr("reader@example.test")
	if e := ValidateInput("/update", &in); e != nil {
		t.Fatal("literal asterisk must be a supplied password", e)
	}
	in.Password = ptr(strings.Repeat("😀", 50))
	if e := ValidateInput("/update", &in); e != nil {
		t.Fatal("200-byte password boundary", e)
	}
	in.ConfirmAccountID = "wrong"
	if e := ValidateInput("/delete", &in); e == nil {
		t.Fatal("typed confirmation bypass")
	}
}
func TestOperationAccountPagingContract(t *testing.T) {
	for _, query := range []string{"", "pageIdx=1&pageSize=100", "pageSize=-1", "filterDomain=EXAMPLE.TEST.&filterDomain=example.test", "filterKeyword=%25_"} {
		q, e := url.ParseQuery(query)
		if e != nil {
			t.Fatal(e)
		}
		f, e := ParseFilter(q)
		if e != nil {
			t.Fatal(query, e)
		}
		if f.PageIdx < 1 {
			t.Fatal(f)
		}
	}
	for _, query := range []string{"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageSize=0", "pageSize=101", "pageSize=-1&pageIdx=2", "pageIdx=1&pageIdx=2", "isPlaintext=01", "isPlaintext=2", "isPlaintext=", "filterStatus=", "tmSort=1", "filterDomain=127.0.0.1", "filterKeyword=%00", "filterDomain=K.example"} {
		q, _ := url.ParseQuery(query)
		_, e := ParseFilter(q)
		if e == nil || e.(*Error).Status != 400 {
			t.Fatal(query, e)
		}
	}
	for _, test := range []struct{ query, code string }{{"isPlaintext=1", "plaintext_unavailable"}, {"filterStatus=unverified", "unsupported_status_filter"}, {"filterStatus=unknown&isPlaintext=1", "plaintext_unavailable"}} {
		q, _ := url.ParseQuery(test.query)
		_, e := ParseFilter(q)
		if e == nil || e.(*Error).Status != 422 || e.Error() != test.code {
			t.Fatal(test, e)
		}
	}
	if _, _, e := page(1001, Filter{PageIdx: 1, PageSize: -1}); e == nil || e.Error() != "result_too_large" {
		t.Fatal(e)
	}
	p, _, e := page(0, Filter{PageIdx: 1, PageSize: 20})
	if e != nil || p.Pages != 0 {
		t.Fatal(p, e)
	}
}
func TestOperationAccountSecretsHaveNoPublicProjection(t *testing.T) {
	sentinel := "synthetic-never-project"
	in := Input{Username: &sentinel, Password: &sentinel, Label: &sentinel}
	b, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{string(b), fmt.Sprint(in), fmt.Sprintf("%#v", in)} {
		if strings.Contains(s, sentinel) {
			t.Fatal("input disclosed")
		}
	}
	public, e := json.Marshal(Account{})
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"username", "password", "ciphertext", "keyId", "userInfo", "port"} {
		if strings.Contains(string(public), `"`+key+`"`) {
			t.Fatal("unsafe DTO field", key)
		}
	}
}
func TestOperationAccountNoNetworkOrConsumerImports(t *testing.T) {
	files, e := filepath.Glob("*.go")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		f, e := parser.ParseFile(token.NewFileSet(), name, raw, parser.ImportsOnly)
		if e != nil {
			t.Fatal(e)
		}
		for _, imp := range f.Imports {
			for _, forbidden := range []string{`"net"`, `"net/http"`, `"github.com/yunpiao/adtr/internal/ldapconnection"`, `"github.com/yunpiao/adtr/internal/domains"`} {
				if imp.Path.Value == forbidden {
					t.Fatal("network/consumer import", name, forbidden)
				}
			}
		}
	}
}
