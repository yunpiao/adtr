package detectionrules

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var testScope = Scope{Tenant: "synthetic-tenant", Domain: "synthetic.invalid"}

func fixtureRule() Rule {
	return Rule{Contract: ContractVersion, Provenance: Provenance, ID: "rule-1", Version: 1, Scope: testScope, Enabled: true, Combine: "all", Conditions: []Condition{{Field: "event.code", Operator: "eq", Values: []string{"4625"}}}}
}
func fixtureEvent() Event {
	return Event{Provenance: Provenance, ID: "event-1", Scope: testScope, Fields: map[string]string{"event.code": "4625"}}
}
func mustCompile(t *testing.T, rules ...Rule) *Program {
	t.Helper()
	p, err := Compile(testScope, rules)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestOperators(t *testing.T) {
	for _, tc := range []struct {
		op     string
		values []string
		value  string
		want   bool
	}{
		{"eq", []string{"4625"}, "4625", true}, {"eq", []string{"4625"}, "4624", false},
		{"neq", []string{"4625"}, "4624", true}, {"neq", []string{"4625"}, "4625", false},
		{"contains", []string{"62"}, "4625", true}, {"contains", []string{"62"}, "4624", true},
		{"contains", []string{"99"}, "4624", false},
		{"prefix", []string{"46"}, "4625", true}, {"prefix", []string{"46"}, "246", false},
		{"in", []string{"4624", "4625"}, "4625", true}, {"in", []string{"4624", "4625"}, "9999", false},
		{"exists", nil, "4625", true},
	} {
		t.Run(tc.op+tc.value+fmt.Sprint(tc.want), func(t *testing.T) {
			r := fixtureRule()
			r.Conditions[0].Operator = tc.op
			r.Conditions[0].Values = tc.values
			e := fixtureEvent()
			e.Fields["event.code"] = tc.value
			got, err := mustCompile(t, r).Evaluate(context.Background(), []Event{e})
			if err != nil || (len(got) == 1) != tc.want {
				t.Fatalf("got %v %v", got, err)
			}
			e.Fields = map[string]string{}
			got, err = mustCompile(t, r).Evaluate(context.Background(), []Event{e})
			if err != nil || len(got) != 0 {
				t.Fatalf("missing evidence matched: %v %v", got, err)
			}
		})
	}
}
func TestCombinationDisabledAndProvenance(t *testing.T) {
	r := fixtureRule()
	r.Conditions = append(r.Conditions, Condition{Field: "event.outcome", Operator: "eq", Values: []string{"failure"}})
	e := fixtureEvent()
	got, err := mustCompile(t, r).Evaluate(context.Background(), []Event{e})
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	r.Combine = "any"
	p := mustCompile(t, r)
	got, err = p.Evaluate(context.Background(), []Event{e})
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if got[0].Contract != ContractVersion || got[0].Provenance != Provenance || got[0].RuleVersion != 1 || len(got[0].RuleDigest) != 64 || got[0].Scope != testScope {
		t.Fatal(got)
	}
	r.Enabled = false
	got, err = mustCompile(t, r).Evaluate(context.Background(), []Event{e})
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
func TestInvalidRules(t *testing.T) {
	cases := map[string]func(*Rule){
		"contract":            func(r *Rule) { r.Contract = "production" },
		"provenance":          func(r *Rule) { r.Provenance = "real" },
		"id":                  func(r *Rule) { r.ID = "" },
		"zero version":        func(r *Rule) { r.Version = 0 },
		"tenant":              func(r *Rule) { r.Scope.Tenant = "other" },
		"domain":              func(r *Rule) { r.Scope.Domain = "other" },
		"combiner":            func(r *Rule) { r.Combine = "eval" },
		"no conditions":       func(r *Rule) { r.Conditions = nil },
		"too many conditions": func(r *Rule) { r.Conditions = make([]Condition, MaxConditions+1) },
		"field injection":     func(r *Rule) { r.Conditions[0].Field = "password" },
		"regex":               func(r *Rule) { r.Conditions[0].Operator = "regex" },
		"shell":               func(r *Rule) { r.Conditions[0].Operator = "exec" },
		"missing value":       func(r *Rule) { r.Conditions[0].Values = nil },
		"excess eq":           func(r *Rule) { r.Conditions[0].Values = []string{"a", "b"} },
		"exists value":        func(r *Rule) { r.Conditions[0].Operator = "exists" },
		"empty in":            func(r *Rule) { r.Conditions[0].Operator = "in"; r.Conditions[0].Values = nil },
		"too many values":     func(r *Rule) { r.Conditions[0].Operator = "in"; r.Conditions[0].Values = make([]string, MaxValues+1) },
		"large value":         func(r *Rule) { r.Conditions[0].Values = []string{strings.Repeat("x", MaxValueBytes+1)} },
		"control":             func(r *Rule) { r.Conditions[0].Values = []string{"x\n"} },
		"invalid utf8":        func(r *Rule) { r.Conditions[0].Values = []string{string([]byte{255})} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := fixtureRule()
			mutate(&r)
			if p, err := Compile(testScope, []Rule{r}); err == nil || p != nil {
				t.Fatal("accepted invalid rule")
			}
		})
	}
	r := fixtureRule()
	for _, rules := range [][]Rule{nil, {r, r}, make([]Rule, MaxRules+1)} {
		if _, err := Compile(testScope, rules); err == nil {
			t.Fatal("invalid batch")
		}
	}
	if _, err := Compile(Scope{}, []Rule{r}); err == nil {
		t.Fatal("empty scope")
	}
}
func TestEventValidationAtomic(t *testing.T) {
	cases := map[string]func(*Event){
		"tenant":        func(e *Event) { e.Scope.Tenant = "other" },
		"domain":        func(e *Event) { e.Scope.Domain = "other" },
		"real":          func(e *Event) { e.Provenance = "real" },
		"empty id":      func(e *Event) { e.ID = "" },
		"unknown field": func(e *Event) { e.Fields["secret"] = "s" },
		"long value":    func(e *Event) { e.Fields["event.code"] = strings.Repeat("x", MaxValueBytes+1) },
		"control":       func(e *Event) { e.Fields["event.code"] = "a\n" },
		"excess fields": func(e *Event) {
			for i := 0; i < 8; i++ {
				e.Fields[fmt.Sprint(i)] = "x"
			}
		},
	}
	p := mustCompile(t, fixtureRule())
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := fixtureEvent()
			e.ID = "event-2"
			mutate(&e)
			got, err := p.Evaluate(context.Background(), []Event{fixtureEvent(), e})
			if err == nil || got != nil {
				t.Fatal("partial or invalid success", got, err)
			}
		})
	}
	if got, err := p.Evaluate(context.Background(), []Event{fixtureEvent(), fixtureEvent()}); err == nil || got != nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := p.Evaluate(context.Background(), make([]Event, MaxEvents+1)); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func TestDeterminismOwnershipAndConcurrency(t *testing.T) {
	r := fixtureRule()
	p := mustCompile(t, r)
	e := fixtureEvent()
	original, err := p.Evaluate(context.Background(), []Event{e})
	if err != nil {
		t.Fatal(err)
	}
	r.Conditions[0].Values[0] = "mutated"
	r.Conditions[0].Field = "unknown"
	again, err := p.Evaluate(context.Background(), []Event{e})
	if err != nil || !reflect.DeepEqual(original, again) {
		t.Fatal("input alias")
	}
	p2 := mustCompile(t, fixtureRule())
	again, _ = p2.Evaluate(context.Background(), []Event{e})
	if !reflect.DeepEqual(original, again) {
		t.Fatal("nondeterministic")
	}
	revised := fixtureRule()
	revised.Version = 2
	other, _ := mustCompile(t, revised).Evaluate(context.Background(), []Event{e})
	if other[0].RuleDigest == original[0].RuleDigest {
		t.Fatal("version not bound")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.Evaluate(context.Background(), []Event{e})
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Error("concurrent mismatch")
			}
		}()
	}
	wg.Wait()
}
func TestCancellationAndResultBound(t *testing.T) {
	p := mustCompile(t, fixtureRule())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, events := range [][]Event{nil, {fixtureEvent()}} {
		if got, err := p.Evaluate(ctx, events); !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal(got, err)
		}
	}
	if _, err := p.Evaluate(nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var absent *Program
	if _, err := absent.Evaluate(context.Background(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	rules := make([]Rule, 11)
	for i := range rules {
		rules[i] = fixtureRule()
		rules[i].ID = fmt.Sprint(i)
	}
	events := make([]Event, MaxEvents)
	for i := range events {
		events[i] = fixtureEvent()
		events[i].ID = fmt.Sprint(i)
	}
	if got, err := mustCompile(t, rules...).Evaluate(context.Background(), events); !errors.Is(err, ErrLimit) || got != nil {
		t.Fatal("unbounded result", len(got), err)
	}
	rules = rules[:10]
	if got, err := mustCompile(t, rules...).Evaluate(context.Background(), events); err != nil || len(got) != MaxMatches {
		t.Fatal("boundary", len(got), err)
	}
}
func TestCodeLikeStringsAreLiteral(t *testing.T) {
	for _, value := range []string{"(a+)+$", "$(touch /tmp/no-execution)", "'; DROP TABLE rules; --", "<script>alert(1)</script>"} {
		r := fixtureRule()
		r.Conditions[0].Values = []string{value}
		e := fixtureEvent()
		e.Fields["event.code"] = value
		got, err := mustCompile(t, r).Evaluate(context.Background(), []Event{e})
		if err != nil || len(got) != 1 {
			t.Fatal(got, err)
		}
	}
}

func TestZeroProgramAndOrdering(t *testing.T) {
	if _, err := (&Program{}).Evaluate(context.Background(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero program", err)
	}
	rules := make([]Rule, MaxRules)
	for i := range rules {
		rules[i] = fixtureRule()
		rules[i].ID = fmt.Sprintf("r-%03d", i)
	}
	r := &rules[0]
	r.Conditions = make([]Condition, MaxConditions)
	for i := range r.Conditions {
		r.Conditions[i] = Condition{Field: "event.code", Operator: "eq", Values: []string{"4625"}}
	}
	e := fixtureEvent()
	e2 := fixtureEvent()
	e2.ID = "event-2"
	got, err := mustCompile(t, rules...).Evaluate(context.Background(), []Event{e, e2})
	if err != nil || len(got) != 2*MaxRules {
		t.Fatal(len(got), err)
	}
	for i, m := range got {
		if m.RuleID != rules[i%MaxRules].ID {
			t.Fatal("rule ordering")
		}
		if (i < MaxRules && m.EventID != e.ID) || (i >= MaxRules && m.EventID != e2.ID) {
			t.Fatal("event ordering")
		}
	}
}
func FuzzLiteralEquality(f *testing.F) {
	f.Add("4625")
	f.Add("$(echo synthetic)")
	f.Add("")
	f.Add(string([]byte{255}))
	f.Fuzz(func(t *testing.T, value string) {
		r := fixtureRule()
		r.Conditions[0].Values = []string{value}
		p, err := Compile(testScope, []Rule{r})
		if !validText(value, MaxValueBytes) {
			if err == nil {
				t.Fatal("invalid string accepted")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		e := fixtureEvent()
		e.Fields["event.code"] = value
		got, err := p.Evaluate(context.Background(), []Event{e})
		if err != nil || len(got) != 1 {
			t.Fatal("literal equality failed", err)
		}
	})
}
