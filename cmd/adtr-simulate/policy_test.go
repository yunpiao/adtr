package main

import (
	"bytes"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/detectionrules"
	"github.com/yunpiao/adtr/internal/rulecatalog"
	"strings"
	"testing"
)

func catalogCLI(t *testing.T, in rulecatalog.Request) rulecatalog.Response {
	t.Helper()
	b, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	var out, errout bytes.Buffer
	if run([]string{"rule-catalog"}, bytes.NewReader(b), &out, &errout) != 0 {
		t.Fatal(errout.String(), string(b))
	}
	var envelope struct {
		Simulation bool
		Result     rulecatalog.Response
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || !envelope.Simulation {
		t.Fatal(err)
	}
	return envelope.Result
}
func TestCatalogLifecycleCLI(t *testing.T) {
	scope := detectionrules.Scope{Tenant: "t", Domain: "d"}
	r := catalogCLI(t, rulecatalog.Request{Scope: scope, Action: "init"})
	rule := detectionrules.Rule{Contract: detectionrules.ContractVersion, Provenance: detectionrules.Provenance, ID: "r", Version: 1, Scope: scope, Combine: "all", Conditions: []detectionrules.Condition{{Field: "event.code", Operator: "eq", Values: []string{"4625"}}}}
	r = catalogCLI(t, rulecatalog.Request{Scope: scope, Snapshot: &r.Snapshot, Action: "create", ExpectedRevision: r.Snapshot.Revision, ID: "r", Rule: &rule})
	cases := []rulecatalog.TestCase{{Event: detectionrules.Event{Provenance: detectionrules.Provenance, ID: "positive", Scope: scope, Fields: map[string]string{"event.code": "4625"}}, WantMatch: true}, {Event: detectionrules.Event{Provenance: detectionrules.Provenance, ID: "negative", Scope: scope, Fields: map[string]string{"event.code": "4624"}}, WantMatch: false}}
	r = catalogCLI(t, rulecatalog.Request{Scope: scope, Snapshot: &r.Snapshot, Action: "test", ExpectedRevision: r.Snapshot.Revision, ID: "r", Cases: cases})
	r = catalogCLI(t, rulecatalog.Request{Scope: scope, Snapshot: &r.Snapshot, Action: "publish-simulation", ExpectedRevision: r.Snapshot.Revision, ID: "r", Reviewer: "synthetic-reviewer"})
	r = catalogCLI(t, rulecatalog.Request{Scope: scope, Snapshot: &r.Snapshot, Action: "enable", ExpectedRevision: r.Snapshot.Revision, ID: "r"})
	if r.Snapshot.Entries[0].ActiveVersion != 1 {
		t.Fatal("not activated")
	}
	r = catalogCLI(t, rulecatalog.Request{Scope: scope, Snapshot: &r.Snapshot, Action: "archive", ExpectedRevision: r.Snapshot.Revision, ID: "r"})
	if !r.Snapshot.Entries[0].Archived || len(r.Snapshot.Entries[0].Published) != 1 {
		t.Fatal("archive discarded published evidence")
	}
}
func TestLabelsCLI(t *testing.T) {
	input := `{"version":"adtr.simulated-sensitive-labels.v1","simulated":true,"scope":{"tenantId":"t","domainId":"d"},"snapshot":{"version":"adtr.simulated-groupgraph.v1","simulated":true,"scope":{"tenantId":"t","domainId":"d"},"observationId":"o","nodes":[{"id":"g","kind":"group","membersKnown":true},{"id":"u","kind":"user"}],"edges":[{"groupId":"g","memberId":"u"}]},"complete":true,"policyId":"p","sensitiveGroupIds":["g"],"maxDepth":8,"maxLabels":10}`
	var out, e bytes.Buffer
	if run([]string{"labels"}, strings.NewReader(input), &out, &e) != 0 {
		t.Fatal(e.String())
	}
	if !strings.Contains(out.String(), `"label":"sensitive"`) {
		t.Fatal(out.String())
	}
}

func TestExceptionsCLI(t *testing.T) {
	input := `{"action":"init","access":{"scope":{"tenant":"t","domain":"d"},"read":true,"write":true}}`
	var out, e bytes.Buffer
	if run([]string{"rule-exceptions"}, strings.NewReader(input), &out, &e) != 0 {
		t.Fatal(e.String())
	}
	var response struct{ Result json.RawMessage }
	json.Unmarshal(out.Bytes(), &response)
	list := `{"action":"list","access":{"scope":{"tenant":"t","domain":"d"},"read":true},"snapshot":` + string(response.Result) + `}`
	out.Reset()
	if run([]string{"rule-exceptions"}, strings.NewReader(list), &out, &e) != 0 {
		t.Fatal(e.String())
	}
	// An init action must never quietly discard an existing snapshot.
	input = strings.TrimSuffix(input, "}") + `,"snapshot":` + string(response.Result) + `}`
	out.Reset()
	if run([]string{"rule-exceptions"}, strings.NewReader(input), &out, &e) != 2 || out.Len() != 0 {
		t.Fatal("ignored field")
	}
}
