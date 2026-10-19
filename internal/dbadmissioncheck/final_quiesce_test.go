package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func finalQuiesceFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for _, entry := range []struct{ path, source string }{
		{"context", `package context;type CancelFunc func();type Context interface{Err()error;Done()<-chan struct{}};var Canceled error`},
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`},
		{"time", `package time;type Duration int64;const Millisecond Duration=1000000;type Time struct{};func Now()Time{return Time{}};func(Time)Add(Duration)Time{return Time{}};func(Time)Before(Time)bool{return false};func Until(Time)Duration{return 0};type Timer struct{C <-chan Time};func NewTimer(Duration)*Timer{return nil};func(*Timer)Stop()bool{return false}`},
		{"encoding/json", `package json;func Marshal(any)([]byte,error){return nil,nil}`},
		{"os", `package os;import "time";type File struct{};func(*File)SetWriteDeadline(time.Time)error{return nil};func(*File)Write([]byte)(int,error){return 0,nil}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	data, err := os.ReadFile("../dbadmission/final_quiesce_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		finalQuiesceSource: string(data),
		"fixture.go": `package dbadmission;import("context";"sync")
 var errSealProtocol error
 type invalidationReason string;const invalidExplicit invalidationReason="explicit_invalidation"
 type lifecycleGate struct{mu sync.Mutex;application,invalid,finalParked bool;finalQuiesce finalQuiesce;workerContext context.Context;pending,open int;tickets map[uint64]*lifecycleTicket;seal *sealAttempt;changed chan struct{};invalidateBecauseLocked func(invalidationReason);notifyLocked func();unlockAndPublishInvalid func()}
 type lifecycleTicket struct{state byte;operationInFlight int}
 type sealAttempt struct{released,recoveryAuthorized bool}
 var coordinator struct{sync.Mutex;gate *lifecycleGate}
 `,
		"application_controller_ci.go": `package dbadmission;import("os";"time";"encoding/json");func writeFinalQuiesceFrame(file *os.File,g *lifecycleGate,seq uint64,status string)error ` + finalQuiesceBodies["writeFinalQuiesceFrame"].body,
	}, imp
}
func TestFinalQuiesceRetainsPhysicalDrainAndStopBarrier(t *testing.T) {
	original, imp := finalQuiesceFixtures(t)
	source := original[finalQuiesceSource]
	for _, tc := range []struct{ name, old, new string }{
		{"reviewed final quiesce", "", ""},
		{"replace root cancel", "g.finalQuiesce.stop = stop", "g.finalQuiesce.stop = func(){}"},
		{"duplicate configuration accepted", "if g.finalQuiesce.stop != nil {", "if false {"},
		{"physical q omitted", " || t.state == 'q'", ""},
		{"inflight omitted", " || t.operationInFlight != 0", ""},
		{"park omitted", "g.finalParked = true", "g.finalParked = false"},
		{"unbounded prepare", "budgetMillis > 30000", "budgetMillis > 60000"},
		{"no capture permission", "!g.seal.recoveryAuthorized ||", ""},
		{"cancel before drain", `g.finalQuiesce.state != "drained"`, `g.finalQuiesce.state != "parking"`},
		{"asynchronous cancel", "\n\tstop()", "\n\tgo stop()"},
		{"early reopen", "\n\tstop()", "\n\tg.finalParked=false;stop()"},
		{"wrong final root state", "g.workerContext.Err() != context.Canceled", "g.workerContext.Err() == context.Canceled"},
		{"completion before cancellation", `g.finalQuiesce.state != "cancelled"`, `g.finalQuiesce.state != "cancelling"`},
		{"abort clears sticky", "g.invalidateBecauseLocked(invalidExplicit)", "g.invalid=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files[finalQuiesceSource] = strings.Replace(source, tc.old, tc.new, 1)
				if files[finalQuiesceSource] == source {
					t.Fatal("mutation unchanged")
				}
			}
			ds := checkCaptureFiles(t, files, imp)
			if (len(ds) > 0) != (tc.old != "") {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}
func TestFinalQuiesceStateAndCallbacksCannotEscape(t *testing.T) {
	original, imp := finalQuiesceFixtures(t)
	for _, source := range []string{
		`func escape(){_=ConfigureCIShutdown}`,
		`func escape(g *lifecycleGate){_=g.prepareFinalQuiesce(1000)}`,
		`func escape(g *lifecycleGate){_=g.cancelFinalQuiesce()}`,
		`func escape(g *lifecycleGate){g.abortFinalQuiesce()}`,
		`func escape(g *lifecycleGate){_=g.completeFinalQuiesce()}`,
		`func escape(g *lifecycleGate){_=g.finalDrainedLocked()}`,
		`func escape(g *lifecycleGate){g.finalQuiesce.stop()}`,
		`func escape(g *lifecycleGate){_= &g.finalQuiesce.stop}`,
		`func escape(g *lifecycleGate){g.finalParked=false}`,
		`func escape(g *lifecycleGate){g.finalQuiesce.state="drained"}`,
		`func escape(){_=finalQuiesce{}}`,
		`func escape(q *finalQuiesce){_=*q}`,
		`func escape(){_=lifecycleGate{finalParked:true}}`,
	} {
		t.Run(source, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["other.go"] = "package dbadmission;" + source
			if ds := checkCaptureFiles(t, files, imp); len(ds) == 0 {
				t.Fatal("quiescence capability escaped")
			}
		})
	}
}
func TestFinalQuiesceACKIsBoundedAndClosed(t *testing.T) {
	original, imp := finalQuiesceFixtures(t)
	source := original["application_controller_ci.go"]
	for _, tc := range []struct{ name, old, new string }{
		{"reviewed ACK", "", ""},
		{"different key", `json:"open"`, `json:"identity"`},
		{"wrong epoch", `{2, 1, seq, "quiesce", status, 0, 0}`, `{2, 2, seq, "quiesce", status, 0, 0}`},
		{"skip short write", "n != len(data)", "n > len(data)"},
		{"longer ACK budget", "500 * time.Millisecond", "1000 * time.Millisecond"},
		{"different state", "g.finalQuiesce.state == status", "g.finalQuiesce.state != status"},
		{"skip physical drain", "g.finalDrainedLocked()", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files["application_controller_ci.go"] = strings.Replace(source, tc.old, tc.new, 1)
				if files["application_controller_ci.go"] == source {
					t.Fatal("mutation unchanged")
				}
			}
			ds := checkCaptureFiles(t, files, imp)
			if (len(ds) > 0) != (tc.old != "") {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}

func TestFinalProtocolCompleteBodiesMatchProduction(t *testing.T) {
	for name, expected := range finalQuiesceBodies {
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
					if !exactFinalQuiesceFunction(managed, expected.file, name, fn) {
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
func TestFinalProtocolOrderingCannotBeWeakened(t *testing.T) {
	for _, tc := range []struct{ name, function, old, new string }{
		{"queued prepare bypass", "serveApplicationNormal", "g.prepareQueuedSelection(r.Kind, r.BudgetMillis)", "g.prepareActivitySelection(r.Kind)"},
		{"queued ACK omitted", "serveApplicationNormal", `send(r.Seq, "reply")`, `send(r.Seq, "invalid")`},
		{"queued reopen omitted", "serveApplicationNormal", "g.completeQueuedSelection()", "g.completeFinalQuiesce()"},
		{"failure publisher reentry", "recordApplicationFailure", "g.invalidateTransport()", "g.invalidateTransport();g.abortQueuedSelection()"},
		{"queued rescue unlock omitted", "serveApplicationRescue", "g.abortQueuedSelection()", "g.abortFinalQuiesce()"},
		{"ACK omitted", "serveApplicationNormal", `writeFinalQuiesceFrame(file, g, r.Seq, "cancelled")`, `writeFinalQuiesceFrame(file, g, r.Seq, "drained")`},
		{"reopen omitted", "serveApplicationNormal", "return g.completeFinalQuiesce()", "return nil"},
		{"unserialized cancel", "serveApplicationNormal", "!g.finalTransportMu.TryLock()", "false"},
		{"close lock removed", "StartCIController", "g.finalTransportMu.Lock()", ""},
		{"join removed", "StartCIController", "wg.Wait()", ""},
		{"budget key omitted", "readApplicationRequest", `expected["budgetMillis"] = true`, `expected["budgetMillis"] = false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := finalQuiesceBodies[tc.function]
			body := strings.Replace(expected.body, tc.old, tc.new, 1)
			if body == expected.body {
				t.Fatal("mutation unchanged")
			}
			file, err := parser.ParseFile(token.NewFileSet(), expected.file, "package dbadmission;func changed()"+body, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if exactFinalQuiesceFunction(managed, expected.file, tc.function, file.Decls[0].(*ast.FuncDecl)) {
				t.Fatal("protocol weakening accepted")
			}
		})
	}
}

func TestFinalRequestCommentsCannotChangeProtocolSyntax(t *testing.T) {
	expected := finalQuiesceBodies["readApplicationRequest"]
	for _, tc := range []struct {
		name, old, new string
		matches        bool
	}{
		{"reviewed declaration comment", "", "", true},
		{"changed declaration comment", "// JSON duplicate keys are not permitted, even when values are equal.", "// Same closed duplicate-key policy.", true},
		{"block declaration comment", "// JSON duplicate keys are not permitted, even when values are equal.", "/* Same closed duplicate-key policy. */", true},
		{"duplicate keys still rejected", "!ok || seen[s]", "!ok && seen[s]", false},
		{"exact fields still required", "len(fields) != len(expected)", "len(fields) > len(expected)", false},
		{"line bound still required", "len(line) > limit", "len(line) < limit", false},
		{"comment-like literal retained", `"op": true`, `"//op": true`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := expected.body
			if tc.old != "" {
				body = strings.Replace(body, tc.old, tc.new, 1)
				if body == expected.body {
					t.Fatal("mutation unchanged")
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), expected.file, "package actual;func actual()"+body, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if got := exactFinalQuiesceFunction(managed, expected.file, "readApplicationRequest", file.Decls[0].(*ast.FuncDecl)); got != tc.matches {
				t.Fatalf("matches=%v want=%v", got, tc.matches)
			}
		})
	}
}
