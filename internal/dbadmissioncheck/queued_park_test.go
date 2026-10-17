package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

func queuedParkFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	files, imp := finalQuiesceFixtures(t)
	data, err := os.ReadFile("../dbadmission/queued_park_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	files[queuedParkSource] = string(data)
	files["fixture.go"] = strings.Replace(files["fixture.go"], "mu sync.Mutex;application", "mu sync.Mutex;queuedParked,closing bool;queuedState string;selection int;nonce [16]byte;selectionTask,selectionOwner,selectionKey string;application", 1)
	files["fixture.go"] += `func(*lifecycleGate)prepareActivitySelection(string)error{return nil}
 func selectionID(string)bool{return true};func selectionWorkerID(string)bool{return true}
 func activityRowKey([16]byte,string)string{return ""}`
	files["activity_selection_ci.go"] = `package dbadmission;func(g *lifecycleGate)configureActivitySelection(taskID,workerID string)error ` + queuedParkBodies["lifecycleGate.configureActivitySelection"].body
	return files, imp
}

// Match repository parsing, including local declaration Doc comments.
func checkQueuedFiles(t *testing.T, sources map[string]string, imp fixtureImporter) []Diagnostic {
	t.Helper()
	fs := token.NewFileSet()
	var files []*ast.File
	for name, source := range sources {
		f, err := parser.ParseFile(fs, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Importer: imp}
	if _, err := config.Check(managed, fs, files, info); err != nil {
		t.Fatal(err)
	}
	return Check(fs, managed, files, info)
}
func TestQueuedParkingRetainsPhysicalDrainAndACKBoundary(t *testing.T) {
	original, imp := queuedParkFixtures(t)
	for _, tc := range []struct{ name, file, old, new string }{
		{"reviewed real source", queuedParkSource, "", ""},
		{"unbounded budget", queuedParkSource, "budgetMillis > 2000", "budgetMillis > 30000"},
		{"skip root cancellation", queuedParkSource, "g.workerContext.Err() != nil", "false"},
		{"skip physical drain", queuedParkSource, "g.finalDrainedLocked()", "true"},
		{"never park", queuedParkSource, "g.queuedParked = true", "g.queuedParked = false"},
		{"timeout without sticky", queuedParkSource, "g.invalidateBecauseLocked(invalidExplicit)", "g.notifyLocked()"},
		{"no deadline", queuedParkSource, "!time.Now().Before(deadline)", "false"},
		{"wrong completion state", queuedParkSource, `g.queuedState != "configured"`, `g.queuedState != "drained"`},
		{"completion skips drain", queuedParkSource, "!g.finalDrainedLocked()", "false"},
		{"completion keeps park", queuedParkSource, `g.queuedState = "complete"`, `g.queuedState = "configured"`},
		{"configure skips drain", "activity_selection_ci.go", "!g.finalDrainedLocked()", "false"},
		{"configure reopens early", "activity_selection_ci.go", `g.queuedState = "configured"`, `g.queuedState = "configured";g.queuedParked=false`},
		{"async selection prepare", queuedParkSource, "if err := g.prepareActivitySelection(kind); err != nil {", "go g.prepareActivitySelection(kind);if err := g.prepareActivitySelection(kind); err != nil {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files[tc.file] = strings.Replace(files[tc.file], tc.old, tc.new, 1)
				if files[tc.file] == original[tc.file] {
					t.Fatal("unchanged mutation")
				}
			}
			ds := checkQueuedFiles(t, files, imp)
			if (len(ds) > 0) != (tc.old != "") {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}
func TestQueuedParkingStateAndCallsCannotEscape(t *testing.T) {
	original, imp := queuedParkFixtures(t)
	for _, source := range []string{
		`func escape(g *lifecycleGate){g.queuedParked=false}`,
		`func escape(g *lifecycleGate){g.queuedState="complete"}`,
		`func escape(g *lifecycleGate){_= &g.queuedParked}`,
		`func escape(g *lifecycleGate){_= &g.queuedState}`,
		`func escape(){_=lifecycleGate{queuedParked:true}}`,
		`func escape(){_=lifecycleGate{queuedState:"drained"}}`,
		`func escape(g *lifecycleGate){_=*g}`,
		`func escape(g *lifecycleGate){*g=lifecycleGate{}}`,
		`func escape(g *lifecycleGate){_=g.prepareQueuedSelection("queued",1000)}`,
		`func escape(g *lifecycleGate){_=g.completeQueuedSelection()}`,
		`func escape(g *lifecycleGate){g.abortQueuedSelection()}`,
		`func escape(g *lifecycleGate){_=g.prepareQueuedSelection}`,
		`func escape(g *lifecycleGate){go g.abortQueuedSelection()}`,
		`func escape(g *lifecycleGate){defer g.abortQueuedSelection()}`,
		`func serveApplicationNormal(g *lifecycleGate){_=g.prepareActivitySelection("queued")}`,
		`func serveApplicationNormal(g *lifecycleGate){_=g.configureActivitySelection("task","worker")}`,
	} {
		t.Run(source, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["escape.go"] = "package dbadmission;" + source
			if ds := checkQueuedFiles(t, files, imp); len(ds) == 0 {
				t.Fatal("escape accepted")
			}
		})
	}
}

func TestQueuedBodiesMatchProductionWithComments(t *testing.T) {
	for name, expected := range queuedParkBodies {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("../dbadmission/" + expected.file)
			if err != nil {
				t.Fatal(err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), expected.file, data, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				candidate := fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					candidate = receiverName(fn.Recv.List[0].Type) + "." + candidate
				}
				if candidate == name {
					found = true
					if !exactQueuedParkFunction(managed, expected.file, name, fn) {
						t.Fatal("production body drift")
					}
				}
			}
			if !found {
				t.Fatal("production function absent")
			}
		})
	}
}
