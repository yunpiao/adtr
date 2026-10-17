package dbadmissioncheck

import (
	"strings"
	"testing"
)

func TestAwaitActivityBoundaries(t *testing.T) {
	for _, boundary := range []struct{ target, operation, path, file, declaration string }{
		{module + "/internal/tasks", "AwaitActivityCandidate", module + "/internal/systemhealth", "persistence_store.go", "type Store struct{};func(*Store)withActivityTx()"},
		{managed, "AwaitActivitySelection", module + "/internal/tasks", "activity_candidate_ci.go", "func AwaitActivityCandidate()"},
	} {
		imp := fixtureImporter{}
		p, _ := fixtureCheck(t, boundary.target, "fixture.go", "package target;func "+boundary.operation+"(){}", imp)
		imp[boundary.target] = p
		call := "target." + boundary.operation + "()"
		for _, tc := range []struct {
			name, file, body string
			bad              bool
		}{
			{"direct", boundary.file, call, false},
			{"wrong file", "other.go", call, true},
			{"alias", boundary.file, "f:=target." + boundary.operation + ";f()", true},
			{"closure", boundary.file, "func(){" + call + "}()", true},
			{"go", boundary.file, "go " + call, true},
			{"defer", boundary.file, "defer " + call, true},
		} {
			t.Run(boundary.operation+"/"+tc.name, func(t *testing.T) {
				source := `package fixture;import target "` + boundary.target + `";` + boundary.declaration + "{" + tc.body + "}"
				_, ds := fixtureCheck(t, boundary.path, tc.file, source, imp)
				if (len(ds) > 0) != tc.bad {
					t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
				}
			})
		}
	}
}
func TestPrivateSelectionCapabilities(t *testing.T) {
	declarations := `package dbadmission;type lifecycleGate struct{};
 func(*lifecycleGate)prepareActivitySelection(){}
 func(*lifecycleGate)configureActivitySelection(){}
 func(*lifecycleGate)awaitActivitySelection(){}
 func(*lifecycleGate)registerActivityRow(){}
 func activityRowKey(){}
 `
	for _, boundary := range []struct{ operation, file, caller string }{
		{"prepareActivitySelection", "application_controller_ci.go", "func serveApplicationNormal(g *lifecycleGate)"},
		{"configureActivitySelection", "application_controller_ci.go", "func serveApplicationNormal(g *lifecycleGate)"},
		{"awaitActivitySelection", "activity_selection_ci.go", "func AwaitActivitySelection(g *lifecycleGate)"},
		{"registerActivityRow", "activity_identity_ci.go", "func RegisterActivityIdentity(g *lifecycleGate)"},
		{"activityRowKey", "activity_selection_ci.go", "func(g *lifecycleGate)configureActivitySelection()"},
	} {
		sourceDeclarations := declarations
		callee := "g." + boundary.operation
		if boundary.operation == "activityRowKey" {
			callee = "activityRowKey"
			sourceDeclarations = strings.Replace(sourceDeclarations, "func(*lifecycleGate)configureActivitySelection(){}", "", 1)
		}
		for _, tc := range []struct {
			name, file, body string
			bad              bool
		}{
			{"direct", boundary.file, callee + "()", false},
			{"wrong file", "other.go", callee + "()", true},
			{"alias", boundary.file, "f:=" + callee + ";f()", true},
			{"closure", boundary.file, "func(){" + callee + "()}()", true},
			{"go", boundary.file, "go " + callee + "()", true},
			{"defer", boundary.file, "defer " + callee + "()", true},
		} {
			t.Run(boundary.operation+"/"+tc.name, func(t *testing.T) {
				_, ds := fixtureCheck(t, managed, tc.file, sourceDeclarations+boundary.caller+"{"+tc.body+"}", fixtureImporter{})
				if (len(ds) > 0) != tc.bad {
					t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
				}
			})
		}
	}
}
