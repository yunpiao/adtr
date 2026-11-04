package querytemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func specimen() Template {
	return Template{ID: "synthetic-1", Name: "示例", Params: Params{AppName: []int{0, 2}, RulesID: []string{"rule-1"}, Time: []string{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"}, DC: []string{"dc.example.invalid"}, DataSource: []DataSource{{Server: "synthetic.example.invalid", AppType: 0}}, UserList: []string{"synthetic-user"}, IPList: []string{"192.0.2.1", "2001:db8::1"}, Types: []string{"synthetic-type"}}}
}
func TestLifecycleAndIsolation(t *testing.T) {
	original := specimen()
	want := clone(original)
	builtin := Template{ID: "builtin-1", Name: "内置"}
	s, e := New([]Template{builtin})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Create(original); e != nil {
		t.Fatal(e)
	}
	original.Params.AppName[0] = 99
	original.Params.DataSource[0].Server = "mutated"
	original.Params.RulesID[0] = "mutated"
	original.Params.Time[0] = "mutated"
	original.Params.DC[0] = "mutated"
	original.Params.UserList[0] = "mutated"
	original.Params.IPList[0] = "mutated"
	original.Params.Types[0] = "mutated"
	list, e := s.List("示")
	if e != nil || len(list.Customize) != 1 || !reflect.DeepEqual(list.Customize[0], want) {
		t.Fatalf("%+v %v", list, e)
	}
	list.Customize[0].Params.DataSource[0].Server = "mutated"
	list.Customize[0].Params.AppName[0] = 99
	fresh, _ := s.List("")
	if !reflect.DeepEqual(fresh.Customize[0], want) {
		t.Fatal("result alias")
	}
	for i := 0; i < 2; i++ {
		if e = s.Delete(want.ID); e != nil {
			t.Fatal(e)
		}
	}
	hidden, _ := s.List("")
	if len(hidden.Customize) != 0 {
		t.Fatal("deleted visible")
	}
	sameName := want
	sameName.ID = "other"
	if !errors.Is(s.Create(sameName), ErrExists) {
		t.Fatal("deleted name not reserved")
	}
	sameID := want
	sameID.Name = "other"
	if !errors.Is(s.Create(sameID), ErrExists) {
		t.Fatal("deleted ID not reserved")
	}
	for i := 0; i < 2; i++ {
		if e = s.Restore(want.ID); e != nil {
			t.Fatal(e)
		}
	}
	restored, _ := s.List("")
	if !reflect.DeepEqual(restored.Customize[0], want) {
		t.Fatal("restore changed value")
	}
	if !errors.Is(s.Delete(builtin.ID), ErrBuiltin) || !errors.Is(s.Restore(builtin.ID), ErrBuiltin) {
		t.Fatal("builtin mutable")
	}
	if !errors.Is(s.Delete("missing"), ErrNotFound) || !errors.Is(s.Restore("missing"), ErrNotFound) {
		t.Fatal("missing accepted")
	}
}
func TestOrderingNamesAndCapacity(t *testing.T) {
	var s Store
	for _, v := range []string{"z", "A", "a"} {
		if e := s.Create(Template{ID: v, Name: v}); e != nil {
			t.Fatal(e)
		}
	}
	list, _ := s.List("")
	for i, n := range []string{"A", "a", "z"} {
		if list.Customize[i].Name != n {
			t.Fatal(list)
		}
	}
	for _, name := range []string{"", " space", "line\n", strings.Repeat("界", 51), string([]byte{255})} {
		if !errors.Is(s.Create(Template{ID: "bad", Name: name}), ErrInvalid) {
			t.Fatal(name)
		}
	}
	if e := s.Create(Template{ID: "emoji", Name: strings.Repeat("😀", 50)}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.List(strings.Repeat("a", 51)); e == nil {
		t.Fatal("long search")
	}
	for i := 4; i < MaxTemplates; i++ {
		if e := s.Create(Template{ID: fmt.Sprint(i), Name: fmt.Sprint(i)}); e != nil {
			t.Fatal(e)
		}
	}
	if !errors.Is(s.Create(Template{ID: "overflow", Name: "overflow"}), ErrCapacity) {
		t.Fatal("capacity")
	}
	s.Delete("emoji")
	if !errors.Is(s.Create(Template{ID: "overflow", Name: "overflow"}), ErrCapacity) {
		t.Fatal("deleted capacity reused")
	}
}
func TestValidateConditions(t *testing.T) {
	cases := map[string]Params{
		"negative app": {AppName: []int{-1}}, "duplicate app": {AppName: []int{1, 1}},
		"large app": {AppName: []int{2147483648}}, "empty rule": {RulesID: []string{""}},
		"duplicate dc": {DC: []string{"x", "x"}}, "control user": {UserList: []string{"x\x00"}},
		"long type": {Types: []string{strings.Repeat("a", 257)}},
		"too many":  {RulesID: make([]string, MaxValues+1)},
		"ip cidr":   {IPList: []string{"192.0.2.0/24"}}, "ip zone": {IPList: []string{"fe80::1%eth0"}},
		"ip bad": {IPList: []string{"999.1.1.1"}}, "single time": {Time: []string{"2026-01-01T00:00:00Z"}},
		"reversed":     {Time: []string{"2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z"}},
		"equal":        {Time: []string{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"}},
		"bad time":     {Time: []string{"today", "tomorrow"}},
		"source empty": {DataSource: []DataSource{{}}}, "source negative": {DataSource: []DataSource{{Server: "x", AppType: -1}}},
		"source duplicate": {DataSource: []DataSource{{Server: "x"}, {Server: "x"}}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			var s Store
			if e := s.Create(Template{ID: "x", Name: "x", Params: p}); !errors.Is(e, ErrInvalid) {
				t.Fatal(e)
			}
			l, _ := s.List("")
			if len(l.Customize) != 0 {
				t.Fatal("failed create mutated")
			}
		})
	}
	if e := Validate(specimen().Params); e != nil {
		t.Fatal(e)
	}
	if e := Validate(Params{}); e != nil {
		t.Fatal(e)
	}
}
func TestStrictDecode(t *testing.T) {
	cases := []string{
		`null`, `{}` + " {}", `{"name":null}`, `{"Name":"x"}`, `{"unknown":1}`,
		`{"name":"a","name":"b"}`, `{"name":"a","\u006eame":"b"}`,
		`{"params":{"dc":[],"dc":[]}}`, `{"params":{"dc":[null]}}`,
		`{"params":{"dataSource":[{"server":"x","server":"y"}]}}`,
		`{"params":{"appName":[1.0]}}`, `{"params":{"appName":[2147483648]}}`,
		`{"name":"\ud800"}`, `{"name":"\udc00"}`, `{"name":"\ud800\u0041"}`,
		`{"name":"` + string([]byte{255}) + `"}`, strings.Repeat(" ", MaxJSONBytes+1),
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			v := specimen()
			before := clone(v)
			if e := Decode([]byte(raw), &v); e == nil {
				t.Fatal(raw)
			}
			if !reflect.DeepEqual(v, before) {
				t.Fatal("failed decode changed destination")
			}
		})
	}
	for _, raw := range []string{`{"name":"\ud83d\ude00"}`, `{"name":"\\ud800"}`, `{"name":"�"}`} {
		var v Template
		if e := Decode([]byte(raw), &v); e != nil {
			t.Fatal(raw, e)
		}
	}
	raw, _ := json.Marshal(clone(specimen()))
	var v Template
	if e := Decode(raw, &v); e != nil || !reflect.DeepEqual(v, specimen()) {
		t.Fatal(v, e)
	}
	if Decode([]byte("{}"), nil) == nil {
		t.Fatal("nil destination")
	}
}
func TestConcurrentLifecycle(t *testing.T) {
	var s Store
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			if e := s.Create(Template{ID: id, Name: id}); e != nil {
				t.Error(e)
			}
			s.List("")
			s.Delete(id)
			s.Restore(id)
		}(i)
	}
	wg.Wait()
	l, _ := s.List("")
	if len(l.Customize) != 20 {
		t.Fatal(len(l.Customize))
	}
}
func TestNewFailureAndDefaultCopy(t *testing.T) {
	v := specimen()
	s, e := New([]Template{v})
	if e != nil {
		t.Fatal(e)
	}
	v.Params.DC[0] = "changed"
	l, _ := s.List("")
	if l.Default[0].Params.DC[0] != "dc.example.invalid" {
		t.Fatal("alias")
	}
	if s, e := New([]Template{v, v}); s != nil || !errors.Is(e, ErrExists) {
		t.Fatal(s, e)
	}
}

func TestValueBudgetsAndIDFailures(t *testing.T) {
	for _, p := range []Params{{AppName: make([]int, 101)}, {DataSource: make([]DataSource, 101)}} {
		if Validate(p) == nil {
			t.Fatal("array budget")
		}
	}
	var s Store
	for _, id := range []string{"", strings.Repeat("a", 129), "bad\n"} {
		if s.Create(Template{ID: id, Name: "valid"}) == nil || s.Delete(id) == nil || s.Restore(id) == nil {
			t.Fatal("invalid ID")
		}
	}
	if Validate(Params{DataSource: []DataSource{{Server: "x", AppType: 2147483648}}}) == nil {
		t.Fatal("appType overflow")
	}
}
func FuzzDecode(f *testing.F) {
	for _, s := range []string{`{}`, `{"params":{"appName":[1]}}`, `{"name":"\ud800"}`, `{"name":"x","name":"y"}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v := specimen()
		before := clone(v)
		if e := Decode(b, &v); e != nil && !reflect.DeepEqual(v, before) {
			t.Fatal("failed decode changed destination")
		}
	})
}

func TestStrictTimeLexicalForm(t *testing.T) {
	for _, start := range []string{
		"2026-01-01T0:00:00Z", "2026-01-01T00:00:00,1Z",
		"2026-01-01T00:00:00+24:00", "2026-01-01T00:00:00+00:60",
		"2026-01-01T00:00:00.1234567890Z", "2026-02-30T00:00:00Z",
	} {
		var s Store
		if s.Create(Template{ID: "x", Name: "x", Params: Params{Time: []string{start, "2026-03-01T00:00:00Z"}}}) == nil {
			t.Fatal(start)
		}
		l, _ := s.List("")
		if len(l.Customize) != 0 {
			t.Fatal("invalid time stored")
		}
	}
	for _, start := range []string{"2026-01-01T00:00:00.123456789Z", "2026-01-01T00:00:00+05:30", "2026-01-01T00:00:00-23:59"} {
		if Validate(Params{Time: []string{start, "2026-03-01T00:00:00Z"}}) != nil {
			t.Fatal(start)
		}
	}
}
