package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func deadlineAdmissionFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for _, entry := range []struct{ path, source string }{
		{"time", `package time;type Time struct{};func Now()Time{return Time{}};func(Time)Before(Time)bool{return false};func(Time)After(Time)bool{return false}`},
		{"context", `package context;import "time";type Context interface{Err()error;Done()<-chan struct{};Deadline()(time.Time,bool)};var Canceled,DeadlineExceeded error`},
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	data, err := os.ReadFile("../dbadmission/lifecycle_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "lifecycle_ci.go", data, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var declaration *ast.FuncDecl
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "acquire" && fn.Recv != nil && receiverName(fn.Recv.List[0].Type) == "lifecycleGate" {
			declaration = fn
		}
	}
	if declaration == nil {
		t.Fatal("production acquire missing")
	}
	if !exactRecoveryFunction(managed, "lifecycle_ci.go", "lifecycleGate.acquire", declaration) {
		t.Fatal("production acquire body drift")
	}
	return map[string]string{
		"lifecycle_ci.go": `package dbadmission;import("context";"time");` + sourceText(declaration),
		"fixture.go": `package dbadmission;import "sync"
 var errAdmissionClosed error
 type invalidationReason string;const invalidCapacity invalidationReason="record_capacity"
 type lifecycleGate struct{
 mu sync.Mutex;seal *sealAttempt;invalid,closing,finalParked,queuedParked,recoveryFinalized bool
 changed chan struct{};witness *struct{};tickets map[uint64]*lifecycleTicket;next,total uint64;pending int
 invalidateWaitLocked func()func();invalidateBecauseLocked func(invalidationReason);notifyLocked func();unlockAndPublishInvalid func();diagnosticPhaseLocked func()string
 }
 type lifecycleTicket struct{gate *lifecycleGate;generation uint64;state byte;birthPhase string;birthCaptureEnded bool;recoveryEpoch uint64;recoveryAuthorized bool}
 type sealAttempt struct{released,recoveryAuthorized,captureEnded bool;epoch uint64}
 `,
	}, imp
}
func TestAdmissionDeadlineRetainsErrPriorityAndLockedPreTicketCheck(t *testing.T) {
	original, imp := deadlineAdmissionFixtures(t)
	source := original["lifecycle_ci.go"]
	for _, tc := range []struct{ name, old, new string }{
		{"real acquire with comments", "", ""},
		{"deadline reads before lock", "g.mu.Lock()", "_,_=ctx.Deadline();g.mu.Lock()"},
		{"clock reads before lock", "g.mu.Lock()", "_=time.Now();g.mu.Lock()"},
		{"existing cancellation overwritten", "if err == nil {", "if true {"},
		{"deadline presence ignored", "ok && !time.Now().Before(deadline)", "!time.Now().Before(deadline) || ok"},
		{"equal deadline admitted", "!time.Now().Before(deadline)", "time.Now().After(deadline)"},
		{"future deadline refused", "!time.Now().Before(deadline)", "time.Now().Before(deadline)"},
		{"wrong deadline error", "err = context.DeadlineExceeded", "err = context.Canceled"},
		{"original error ignored", "err := ctx.Err()", "err := error(nil)"},
		{"ticket count before deadline", "err := ctx.Err()", "g.total++;err := ctx.Err()"},
		{"pending before deadline", "err := ctx.Err()", "g.pending++;err := ctx.Err()"},
		{"existing invalidation skipped", "publish := g.invalidateWaitLocked()", "publish := (func())(nil)"},
		{"publication skipped", "publish()", "_ = publish"},
		{"cancellation swallowed", "return nil, err", "return nil, nil"},
		{"park wake bypasses recheck", "continue", "return nil,nil"},
		{"extra deadline read before loop", "for {", "_,_=ctx.Deadline();for {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files["lifecycle_ci.go"] = strings.Replace(source, tc.old, tc.new, 1)
				if files["lifecycle_ci.go"] == source {
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
func TestAdmissionDeadlineCannotBeRemovedWithProtectedFields(t *testing.T) {
	files, imp := deadlineAdmissionFixtures(t)
	files["lifecycle_ci.go"] = `package dbadmission;import "context";func(g *lifecycleGate)acquire(ctx context.Context)(*lifecycleTicket,error){return nil,nil}`
	if ds := checkQueuedFiles(t, files, imp); len(ds) == 0 {
		t.Fatal("removing the entire admission boundary was accepted")
	}
}
