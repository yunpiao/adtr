package blockexceptions

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func reviewPolicy() Policy { return Policy{128, 128, 40, 10, 100} }
func reviewRule() Rule {
	return Rule{RuleID: "id", Scope: Scope{"tenant", "domain", 1}, RuleName: "safe", ValueType: User, RuleValue: "alice"}
}
func reviewError(t *testing.T, e error, code Code) {
	t.Helper()
	var fe *FieldError
	if !errors.As(e, &fe) || fe.Code != code {
		t.Fatalf("want %s got %v", code, e)
	}
}
func TestIndependentNoScopeBypass(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	c := Candidate{r.Scope, r.ValueType, r.RuleValue}
	for _, edit := range []func(*Candidate){func(c *Candidate) { c.Scope.TenantID = "" }, func(c *Candidate) { c.Scope.DataSourceID = "\xff" }, func(c *Candidate) { c.Scope.AppType = 0 }, func(c *Candidate) { c.ValueType = 0 }, func(c *Candidate) { c.RuleValue = "\x00secret" }} {
		cc := c
		edit(&cc)
		ok, e := Match(p, r, cc)
		if ok || e == nil {
			t.Fatal("invalid candidate was accepted")
		}
		if strings.Contains(e.Error(), "secret") {
			t.Fatal("value leaked")
		}
	}
	for _, s := range []Scope{{"other", "domain", 1}, {"tenant", "other", 1}, {"tenant", "domain", 2}} {
		cc := c
		cc.Scope = s
		ok, e := Match(p, r, cc)
		if ok || e != nil {
			t.Fatalf("valid isolated scope %v %v", ok, e)
		}
	}
}
func TestIndependentLiteralIdentity(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	for _, s := range []string{"*", ".*", "?", "a|b", "a:b", "DOMAIN\\alice", "alice@domain", "e\u0301", "é"} {
		r.RuleValue = s
		c := Candidate{r.Scope, User, s}
		ok, e := Match(p, r, c)
		if !ok || e != nil {
			t.Fatalf("literal %q %v", s, e)
		}
		c.RuleValue = "other"
		ok, e = Match(p, r, c)
		if ok || e != nil {
			t.Fatal("pattern expansion")
		}
	}
	r.RuleValue = "é"
	ok, e := Match(p, r, Candidate{r.Scope, User, "e\u0301"})
	if ok || e != nil {
		t.Fatal("unicode folded")
	}
	r.RuleValue = "Alice"
	ok, e = Match(p, r, Candidate{r.Scope, User, "alice"})
	if ok || e != nil {
		t.Fatal("case folded")
	}
}
func TestIndependentIPForms(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	r.ValueType = IP
	for _, s := range []string{"::ffff:192.0.2.1", "::ffff:c000:201", "fe80::1%eth0", "192.0.2.0/24", "192.0.2.1-192.0.2.2"} {
		r.RuleValue = s
		ok, e := Match(p, r, Candidate{r.Scope, IP, s})
		if ok {
			t.Fatal("matched unsupported")
		}
		reviewError(t, e, Unsupported)
	}
	for _, s := range []string{"127.1", "2130706433", "010.0.0.1", "[::1]", "http://127.0.0.1", "192.0.2.1:80", "example.test", " ::1"} {
		r.RuleValue = s
		ok, e := Match(p, r, Candidate{r.Scope, IP, s})
		if ok || e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	r.RuleValue = "2001:0DB8:0:0:0:0:0:1"
	ok, e := Match(p, r, Candidate{r.Scope, IP, "2001:db8::1"})
	if !ok || e != nil {
		t.Fatal("equivalent IPv6 mismatch")
	}
	k, e := DuplicateKey(p, r)
	if e != nil {
		t.Fatal(e)
	}
	r.RuleValue = "2001:db8::1"
	k2, e := DuplicateKey(p, r)
	if k != k2 || e != nil {
		t.Fatal("IPv6 keys unequal")
	}
}
func TestIndependentKeyAndBudgets(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	r.Scope.TenantID = "a|b"
	r.Scope.DataSourceID = "c"
	k, e := DuplicateKey(p, r)
	if e != nil {
		t.Fatal(e)
	}
	r.Scope.TenantID = "a"
	r.Scope.DataSourceID = "b|c"
	k2, e := DuplicateKey(p, r)
	if e != nil || k == k2 {
		t.Fatal("ambiguous key")
	}
	r = reviewRule()
	r.RuleValue = strings.Repeat("é", 65)
	reviewError(t, ValidateRule(p, r), BudgetExceeded)
	r = reviewRule()
	r.RuleName = strings.Repeat("😀", 50)
	if e := ValidateRule(p, r); e != nil {
		t.Fatal(e)
	}
	r.RuleName += "x"
	reviewError(t, ValidateRule(p, r), Invalid)
	r = reviewRule()
	r.Remark = "line\n\tsecond"
	if e := ValidateRule(p, r); e != nil {
		t.Fatal(e)
	}
	r.Remark = "secret\x7f"
	e = ValidateRule(p, r)
	reviewError(t, e, Invalid)
	if strings.Contains(e.Error(), "secret") {
		t.Fatal("leak")
	}
	p.MaxIDBytes = math.MaxInt
	p.MaxUserValueBytes = math.MaxInt
	if e := ValidateRule(p, reviewRule()); e != nil {
		t.Fatal(e)
	}
}
func TestIndependentEditAndDelete(t *testing.T) {
	p := reviewPolicy()
	old := reviewRule()
	snapshot := old
	n, e := ApplyEdit(p, old, Edit{"id", IP, "2001:0db8::1", "new"})
	if e != nil || n.RuleValue != "2001:db8::1" || n.Scope != old.Scope || n.RuleName != old.RuleName || old != snapshot {
		t.Fatalf("edit invalid %v", e)
	}
	_, e = ApplyEdit(p, old, Edit{"other", User, "alice", ""})
	reviewError(t, e, Invalid)
	old.RuleName = ""
	_, e = ApplyEdit(p, old, Edit{"id", User, "alice", ""})
	if e == nil {
		t.Fatal("invalid old accepted")
	}
	ids := []string{"a", "b"}
	out, e := ValidateDeleteIDs(p, ids)
	if e != nil {
		t.Fatal(e)
	}
	out[0] = "z"
	if ids[0] != "a" {
		t.Fatal("alias")
	}
	_, e = ValidateDeleteIDs(p, []string{"a", "a"})
	reviewError(t, e, Duplicate)
}
func TestQueryBoundaries(t *testing.T) {
	p := reviewPolicy()
	iptr := func(i int) *int { return &i }
	empty := ""
	for _, q := range []Query{{CreateStartTime: &empty}, {CreateEndTime: &empty}, {ModifyStartTime: &empty}, {ModifyEndTime: &empty}} {
		_, e := ValidateQuery(p, q)
		reviewError(t, e, Unsupported)
	}
	for _, field := range []string{"index", "size"} {
		for _, n := range []int{0, -1, -2} {
			q := Query{}
			if field == "index" {
				q.PageIdx = iptr(n)
			} else {
				q.PageSize = iptr(n)
			}
			_, e := ValidateQuery(p, q)
			code := Invalid
			if n == -1 {
				code = Unsupported
			}
			reviewError(t, e, code)
		}
	}
	out, e := ValidateQuery(p, Query{})
	if e != nil || out.PageIdx != 1 || out.PageSize != 10 || out.Offset != 0 || out.SearchValueList != nil {
		t.Fatalf("defaults %v %v", out, e)
	}
	_, e = ValidateQuery(p, Query{PageIdx: iptr(math.MaxInt), PageSize: iptr(2)})
	reviewError(t, e, BudgetExceeded)
	_, e = ValidateQuery(p, Query{PageSize: iptr(101)})
	reviewError(t, e, BudgetExceeded)
	out, e = ValidateQuery(p, Query{PageIdx: iptr(math.MaxInt), PageSize: iptr(1)})
	if e != nil || out.Offset != math.MaxInt-1 {
		t.Fatal("safe offset")
	}
	for _, mode := range []SearchMode{Partial, Unselected, All} {
		for _, v := range [][]string{nil, {}} {
			q := Query{SearchValueList: []Selection{{User, mode, v}}}
			out, e := ValidateQuery(p, q)
			if e != nil || (out.SearchValueList[0].ValueList == nil) != (v == nil) {
				t.Fatal("nil/empty combination lost")
			}
		}
	}
	q := Query{AppTypeList: []int{1}, DataSrcList: []string{"d"}, SearchValueList: []Selection{{User, Partial, []string{"a"}}}, SortField: []Sort{{"modifyTime", -1}, {"createTime", 1}}}
	out, e = ValidateQuery(p, q)
	if e != nil {
		t.Fatal(e)
	}
	out.AppTypeList[0] = 3
	out.DataSrcList[0] = "x"
	out.SearchValueList[0].ValueList[0] = "x"
	out.SortField[0].Field = "x"
	if q.AppTypeList[0] != 1 || q.DataSrcList[0] != "d" || q.SearchValueList[0].ValueList[0] != "a" || q.SortField[0].Field != "modifyTime" {
		t.Fatal("alias")
	}
	p.MaxListItems = 5
	_, e = ValidateQuery(p, q)
	reviewError(t, e, BudgetExceeded)
	p.MaxListItems = 6
	if _, e = ValidateQuery(p, q); e != nil {
		t.Fatal(e)
	}
}
func TestQueryInvalidMatrix(t *testing.T) {
	p := reviewPolicy()
	cases := []struct {
		q    Query
		code Code
	}{
		{Query{Keyword: "\xff"}, Invalid}, {Query{Keyword: strings.Repeat("😀", 51)}, Invalid},
		{Query{AppTypeList: []int{0}}, Invalid}, {Query{AppTypeList: []int{1, 1}}, Duplicate},
		{Query{DataSrcList: []string{""}}, Required}, {Query{DataSrcList: []string{"a", "a"}}, Duplicate},
		{Query{SearchValueList: []Selection{{0, Partial, nil}}}, Invalid}, {Query{SearchValueList: []Selection{{User, 0, nil}}}, Invalid},
		{Query{SearchValueList: []Selection{{User, Partial, nil}, {User, All, nil}}}, Duplicate},
		{Query{SearchValueList: []Selection{{User, Partial, []string{"a", "a"}}}}, Duplicate},
		{Query{SearchValueList: []Selection{{User, Partial, []string{"\xff"}}}}, Invalid},
		{Query{SortField: []Sort{{"x", 1}}}, Invalid}, {Query{SortField: []Sort{{"createTime", 0}}}, Invalid},
		{Query{SortField: []Sort{{"createTime", 1}, {"createTime", -1}}}, Duplicate},
	}
	for _, c := range cases {
		_, e := ValidateQuery(p, c.q)
		reviewError(t, e, c.code)
	}
}
func TestZeroPoliciesAndRules(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	c := Candidate{r.Scope, User, r.RuleValue}
	for _, pp := range []Policy{{}, {0, 128, 40, 10, 100}, {128, 0, 40, 10, 100}, {128, 128, 0, 10, 100}, {128, 128, 40, 0, 100}, {128, 128, 40, 10, 9}} {
		if ok, e := Match(pp, r, c); ok || e == nil {
			t.Fatal("zero policy matched")
		}
		if _, e := ValidateQuery(pp, Query{}); e == nil {
			t.Fatal("zero policy query")
		}
		if _, e := ValidateDeleteIDs(pp, []string{"id"}); e == nil {
			t.Fatal("zero policy delete")
		}
	}
	if ok, e := Match(p, Rule{}, c); ok || e == nil {
		t.Fatal("zero rule")
	}
	for _, mut := range []func(*Rule){func(r *Rule) { r.RuleID = "\xff" }, func(r *Rule) { r.RuleID = " id" }, func(r *Rule) { r.Scope.TenantID = "x\n" }, func(r *Rule) { r.Scope.DataSourceID = "" }, func(r *Rule) { r.Scope.AppType = 4 }, func(r *Rule) { r.RuleName = "\xff" }, func(r *Rule) { r.RuleName = "x\x00" }, func(r *Rule) { r.RuleName = " x" }, func(r *Rule) { r.ValueType = 0 }, func(r *Rule) { r.RuleValue = "" }, func(r *Rule) { r.Remark = "\xff" }, func(r *Rule) { r.Remark = strings.Repeat("x", 151) }} {
		rr := r
		mut(&rr)
		if ok, e := Match(p, rr, c); ok || e == nil {
			t.Fatal("invalid rule matched")
		}
	}
	r.RuleID = ""
	if e := ValidateRule(p, r); e != nil {
		t.Fatal("creation needs ID")
	}
	if e := ValidateDetailID(p, ""); e == nil {
		t.Fatal("empty detail")
	}
	_, e := ValidateDeleteIDs(p, nil)
	reviewError(t, e, Required)
	p.MaxDeleteTargets = 1
	_, e = ValidateDeleteIDs(p, []string{"a", "b"})
	reviewError(t, e, BudgetExceeded)
}
func FuzzMatchFailClosed(f *testing.F) {
	for _, s := range []string{"alice", "", "\xff", "::ffff:c000:201", "2001:db8::1", "*"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p := reviewPolicy()
		for _, typ := range []ValueType{User, IP} {
			r := reviewRule()
			r.ValueType = typ
			r.RuleValue = s
			c := Candidate{r.Scope, typ, s}
			ok, e := Match(p, r, c)
			if e != nil && ok {
				t.Fatal("error matched")
			}
			if e == nil && !ok {
				t.Fatal("self mismatch")
			}
			if e == nil {
				n, e := NormalizeRule(p, r)
				if e != nil {
					t.Fatal(e)
				}
				nn, e := NormalizeRule(p, n)
				if e != nil || n != nn {
					t.Fatal("not idempotent")
				}
			}
		}
	})
}
func TestDirectValidationErrors(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	if _, e := NormalizeCandidate(Policy{}, Candidate{}); e == nil {
		t.Fatal("zero candidate policy")
	}
	if _, e := DuplicateKey(p, Rule{}); e == nil {
		t.Fatal("invalid key")
	}
	if e := ValidateDetailID(Policy{}, "id"); e == nil {
		t.Fatal("zero detail policy")
	}
	old := r
	old.RuleID = ""
	if _, e := ApplyEdit(p, old, Edit{}); e == nil {
		t.Fatal("old without ID")
	}
	if _, e := ApplyEdit(p, r, Edit{}); e == nil {
		t.Fatal("edit without ID")
	}
	if _, e := ValidateDeleteIDs(p, []string{"\x00"}); e == nil {
		t.Fatal("invalid delete")
	}
	p.MaxListItems = 1
	if _, e := ValidateQuery(p, Query{AppTypeList: []int{1, 2}}); e == nil {
		t.Fatal("top-level budget")
	}
}
func TestUnicodeScopeAndTypeIsolation(t *testing.T) {
	p := reviewPolicy()
	r := reviewRule()
	c := Candidate{r.Scope, User, r.RuleValue}
	for _, space := range []string{"\u00a0", "\u2003", "\u202f", "\u3000"} {
		rr := r
		rr.Scope.TenantID = space + "tenant"
		if ok, e := Match(p, rr, c); ok || e == nil {
			t.Fatal("Unicode boundary allowed")
		}
		rr = r
		rr.RuleValue = "alice" + space
		if ok, e := Match(p, rr, c); ok || e == nil {
			t.Fatal("Unicode user boundary allowed")
		}
	}
	cc := c
	cc.Scope.TenantID = "Tenant"
	if ok, e := Match(p, r, cc); ok || e != nil {
		t.Fatal("scope case folded")
	}
	cc = c
	cc.Scope.DataSourceID = "Domain"
	if ok, e := Match(p, r, cc); ok || e != nil {
		t.Fatal("source case folded")
	}
	r.RuleValue = "192.0.2.1"
	cc = Candidate{r.Scope, IP, r.RuleValue}
	if ok, e := Match(p, r, cc); ok || e != nil {
		t.Fatal("types conflated")
	}
}
