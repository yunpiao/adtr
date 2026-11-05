package attackpaths

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func jsonFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../examples/attackpaths/request.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func requestFixture(t *testing.T) jsonRequest {
	t.Helper()
	var req jsonRequest
	if err := json.Unmarshal(jsonFixture(t), &req); err != nil {
		t.Fatal(err)
	}
	return req
}
func encoded(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func requireJSONError(t *testing.T, data []byte, want error) {
	t.Helper()
	result, err := EvaluateJSON(data)
	if !errors.Is(err, want) || !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("got result=%+v error=%v; want zero result and %v", result, err, want)
	}
}
func TestEvaluateJSONResultAndDeterminism(t *testing.T) {
	req := requestFixture(t)
	got, err := EvaluateJSON(encoded(t, req))
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Version: Version, Simulated: true, Scope: req.Snapshot.Scope,
		ObservationID: req.Snapshot.ObservationID, Found: true,
		Nodes: req.Snapshot.Nodes, Edges: req.Snapshot.Edges}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result: %+v", got)
	}
	for i, j := 0, len(req.Snapshot.Nodes)-1; i < j; i, j = i+1, j-1 {
		req.Snapshot.Nodes[i], req.Snapshot.Nodes[j] = req.Snapshot.Nodes[j], req.Snapshot.Nodes[i]
	}
	req.Snapshot.Edges[0], req.Snapshot.Edges[1] = req.Snapshot.Edges[1], req.Snapshot.Edges[0]
	again, err := EvaluateJSON(encoded(t, req))
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("order changes result: %+v %v", again, err)
	}
}
func TestEvaluateJSONQueryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*jsonRequest)
		want      error
		found     bool
		nodeCount int
	}{
		{"unknown rule", func(r *jsonRequest) { r.Query.RuleIDs = []string{"unknown"} }, nil, false, 0},
		{"end exclusive", func(r *jsonRequest) { r.Query.End = r.Snapshot.Edges[1].OccurredAt }, nil, false, 0},
		{"start exclusive edge", func(r *jsonRequest) { r.Query.Start = r.Snapshot.Edges[0].OccurredAt.Add(time.Nanosecond) }, nil, false, 0},
		{"depth exhausted", func(r *jsonRequest) { r.Query.MaxDepth = 1 }, ErrLimit, false, 0},
		{"scope mismatch", func(r *jsonRequest) { r.Query.Scope.DomainID = "other" }, ErrScope, false, 0},
		{"unknown endpoint", func(r *jsonRequest) { r.Query.To = "absent" }, ErrNotFound, false, 0},
		{"zero edge path", func(r *jsonRequest) { r.Query.To = r.Query.From }, nil, true, 1},
		{"empty rules", func(r *jsonRequest) { r.Query.RuleIDs = []string{} }, ErrInvalid, false, 0},
		{"duplicate rules", func(r *jsonRequest) { r.Query.RuleIDs = append(r.Query.RuleIDs, r.Query.RuleIDs[0]) }, ErrInvalid, false, 0},
		{"empty graph", func(r *jsonRequest) { r.Snapshot.Nodes = []Node{}; r.Snapshot.Edges = []Edge{} }, ErrNotFound, false, 0},
		{"not simulated", func(r *jsonRequest) { r.Snapshot.Simulated = false }, ErrInvalid, false, 0},
		{"duplicate node", func(r *jsonRequest) { r.Snapshot.Nodes = append(r.Snapshot.Nodes, r.Snapshot.Nodes[0]) }, ErrInvalid, false, 0},
		{"dangling edge", func(r *jsonRequest) { r.Snapshot.Edges[0].To = "absent" }, ErrInvalid, false, 0},
		{"invalid depth", func(r *jsonRequest) { r.Query.MaxDepth = 0 }, ErrLimit, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFixture(t)
			tc.change(&r)
			data := encoded(t, r)
			if tc.want != nil {
				requireJSONError(t, data, tc.want)
				return
			}
			got, err := EvaluateJSON(data)
			if err != nil || got.Found != tc.found || len(got.Nodes) != tc.nodeCount || got.Nodes == nil || got.Edges == nil {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
func TestEvaluateJSONStrictSyntax(t *testing.T) {
	good := string(jsonFixture(t))
	r := requestFixture(t)
	duplicateRoot := fmt.Sprintf(`{"snapshot":%s,"snapshot":%s,"query":%s}`, encoded(t, r.Snapshot), encoded(t, r.Snapshot), encoded(t, r.Query))
	for name, data := range map[string]string{
		"empty": "", "root null": "null", "root array": "[]", "truncated": good[:len(good)-2],
		"trailing": good + "{}", "trailing garbage": good + "x", "bom": "\ufeff" + good,
		"duplicate root":      duplicateRoot,
		"duplicate leaf":      strings.Replace(good, `"id": "workstation"`, `"id":"workstation","id":"workstation"`, 1),
		"escaped duplicate":   strings.Replace(good, `"id": "workstation"`, `"id":"workstation","\u0069d":"workstation"`, 1),
		"case alias":          strings.Replace(good, `"domainId"`, `"DomainId"`, 1),
		"unknown":             strings.Replace(good, `"snapshot": {`, `"snapshot": {"unknown":"secret",`, 1),
		"array null":          strings.Replace(good, `"ruleIds": ["synthetic-rule"]`, `"ruleIds": null`, 1),
		"array scalar":        strings.Replace(good, `"ruleIds": ["synthetic-rule"]`, `"ruleIds": "synthetic-rule"`, 1),
		"element null":        strings.Replace(good, `["synthetic-rule"]`, `[null]`, 1),
		"boolean number":      strings.Replace(good, `"simulated": true`, `"simulated": 1`, 1),
		"integer float":       strings.Replace(good, `"maxDepth": 2`, `"maxDepth": 2.0`, 1),
		"integer exponent":    strings.Replace(good, `"maxDepth": 2`, `"maxDepth": 2e0`, 1),
		"integer overflow":    strings.Replace(good, `"maxDepth": 2`, `"maxDepth": 2147483648`, 1),
		"integer string":      strings.Replace(good, `"maxDepth": 2`, `"maxDepth": "2"`, 1),
		"invalid UTF8":        strings.Replace(good, "Synthetic account", string([]byte{0xff}), 1),
		"lone high surrogate": strings.Replace(good, "Synthetic account", `\ud800`, 1),
		"lone low surrogate":  strings.Replace(good, "Synthetic account", `\udc00`, 1),
		"bad pair":            strings.Replace(good, "Synthetic account", `\ud800\u0041`, 1),
		"invalid escape":      strings.Replace(good, "Synthetic account", `\uZZZZ`, 1),
		"deep object":         strings.Replace(good, `"Synthetic account"`, strings.Repeat(`{"x":`, 100)+`0`+strings.Repeat(`}`, 100), 1),
	} {
		t.Run(name, func(t *testing.T) { requireJSONError(t, []byte(data), ErrInvalid) })
	}
	for _, name := range []string{`\ud83d\ude00`, `\\ud800`, `Unicode 名称`, `\uFFFD`} {
		if _, err := EvaluateJSON([]byte(strings.Replace(good, "Synthetic account", name, 1))); err != nil {
			t.Fatalf("valid string %q: %v", name, err)
		}
	}
}
func TestEvaluateJSONEveryFieldRequiredAndNonNull(t *testing.T) {
	// Traverse all object shapes, including array elements, to guard every field.
	var root map[string]any
	if err := json.Unmarshal(jsonFixture(t), &root); err != nil {
		t.Fatal(err)
	}
	var visit func(any, string)
	visit = func(value any, path string) {
		switch v := value.(type) {
		case map[string]any:
			for key, original := range v {
				delete(v, key)
				t.Run(path+"/"+key+" missing", func(t *testing.T) { requireJSONError(t, encoded(t, root), ErrInvalid) })
				v[key] = nil
				t.Run(path+"/"+key+" null", func(t *testing.T) { requireJSONError(t, encoded(t, root), ErrInvalid) })
				v[key] = original
				visit(original, path+"/"+key)
			}
		case []any:
			for i, x := range v {
				visit(x, fmt.Sprintf("%s/%d", path, i))
			}
		}
	}
	visit(root, "")
}
func TestEvaluateJSONCanonicalTimes(t *testing.T) {
	good := string(jsonFixture(t))
	for _, stamp := range []string{"2026-11-01T10:00:00+00:00", "2026-11-01T11:00:00+01:00", "2026-11-01T10:00:00.0Z", "2026-11-01T10:00:00,1Z", "2026-11-01T10:00:00.1234567891Z", "0001-01-01T00:00:00Z", "2026-11-01T10:00:60Z", "not-a-time"} {
		t.Run(stamp, func(t *testing.T) {
			requireJSONError(t, []byte(strings.Replace(good, "2026-11-01T10:00:00Z", stamp, 1)), ErrInvalid)
		})
	}
	if _, err := EvaluateJSON([]byte(strings.Replace(good, "2026-11-01T10:00:00Z", "2026-11-01T10:00:00.123456789Z", 1))); err != nil {
		t.Fatal(err)
	}
}
func TestEvaluateJSONCollectionLimits(t *testing.T) {
	for _, kind := range []string{"nodes", "edges", "rules"} {
		t.Run(kind, func(t *testing.T) {
			r := requestFixture(t)
			limit := MaxNodes
			switch kind {
			case "edges":
				limit = MaxEdges
			case "rules":
				limit = MaxRules
			}
			for n := 0; n <= limit; n++ {
				switch kind {
				case "nodes":
					if n == 0 {
						r.Snapshot.Nodes = nil
					}
					r.Snapshot.Nodes = append(r.Snapshot.Nodes, Node{ID: fmt.Sprintf("n%d", n), Name: "Synthetic"})
				case "edges":
					if n == 0 {
						r.Snapshot.Edges = nil
					}
					r.Snapshot.Edges = append(r.Snapshot.Edges, Edge{ID: fmt.Sprintf("e%d", n), From: r.Query.From, To: r.Query.To, RuleID: r.Query.RuleIDs[0], OccurredAt: r.Query.Start})
				case "rules":
					if n == 0 {
						r.Query.RuleIDs = nil
					}
					r.Query.RuleIDs = append(r.Query.RuleIDs, fmt.Sprintf("r%d", n))
				}
				if n == limit-1 {
					if kind == "nodes" {
						r.Query.From = "n0"
						r.Query.To = "n1"
						r.Snapshot.Edges = []Edge{}
					}
					if _, err := EvaluateJSON(encoded(t, r)); err != nil {
						t.Fatalf("exact limit: %v", err)
					}
				}
			}
			requireJSONError(t, encoded(t, r), ErrLimit)
		})
	}
}
func TestEvaluateJSONByteBudget(t *testing.T) {
	data := jsonFixture(t)
	padded := append(data, []byte(strings.Repeat(" ", MaxJSONBytes-len(data)))...)
	if _, err := EvaluateJSON(padded); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	requireJSONError(t, append(padded, ' '), ErrLimit)
}
func TestEvaluateJSONConcurrent(t *testing.T) {
	data := jsonFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result, err := EvaluateJSON(data); err != nil || !result.Found {
				t.Errorf("%+v %v", result, err)
			}
		}()
	}
	wg.Wait()
}
func FuzzEvaluateJSON(f *testing.F) {
	data, err := os.ReadFile("../../examples/attackpaths/request.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte(`{"snapshot":null}`))
	f.Add([]byte(`"\ud800"`))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := EvaluateJSON(data)
		if err != nil {
			if !reflect.DeepEqual(r, Result{}) {
				t.Fatal("error exposed partial result")
			}
			return
		}
		if r.Version != Version || !r.Simulated || !scopeValid(r.Scope) || r.Nodes == nil || r.Edges == nil || len(r.Edges) > MaxDepth {
			t.Fatalf("invalid result: %+v", r)
		}
	})
}
