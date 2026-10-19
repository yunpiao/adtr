package dbadmissioncheck

import (
	"os"
	"strings"
	"testing"
)

func recoveryFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for _, entry := range []struct{ path, source string }{
		{"context", `package context;type Context interface{Err()error;Value(any)any};var Canceled,DeadlineExceeded error`},
		{"encoding/json", `package json;func Marshal(any)([]byte,error){return nil,nil}`},
		{"os", `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`},
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`},
		{"time", `package time;type Duration int64;const Second Duration=1000000000;type Time struct{};func Now()Time{return Time{}};func(Time)Add(Duration)Time{return Time{}};func(Time)Before(Time)bool{return false};func Until(Time)Duration{return 0};type Timer struct{C <-chan Time};func NewTimer(Duration)*Timer{return nil};func(*Timer)Stop()bool{return false}`},
		{pgx + "/pgconn", `package pgconn;type PgConn struct{};func(*PgConn)CleanupDone()<-chan struct{}{return nil}`},
		{pgx, `package pgx;import "github.com/jackc/pgx/v5/pgconn";type Conn struct{};func(*Conn)IsClosed()bool{return false};func(*Conn)PgConn()*pgconn.PgConn{return nil}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	data, err := os.ReadFile("../dbadmission/recovery_cleanup_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"recovery_cleanup_ci.go": string(data),
		"conn.go":                `package dbadmission;import "github.com/jackc/pgx/v5";type Conn struct{raw *pgx.Conn};type closeObservation struct{alreadyClosed,closeFailed,cleanupDone bool}`,
		"fixture.go": `package dbadmission;import("sync";"context")
 type invalidationReason string
 const invalidUncleanClose invalidationReason="unclean_close"
 const invalidCloseState invalidationReason="invalid_close_state"
 type lifecycleGate struct{mu sync.Mutex;invalid,closing,recoveryFinalized bool;seal *sealAttempt;recoveryQualified,recoveryCompleted uint64;workerContext context.Context;tickets map[uint64]*lifecycleTicket;pending,open int;invalidateBecauseLocked func(invalidationReason);notifyLocked func()}
 type lifecycleTicket struct{gate *lifecycleGate;generation,recoveryEpoch uint64;state byte;recoveryAuthorized,recoveryContaminated,recoveryEverRegistered bool;operationInFlight int;conn *Conn;registration *struct{};firstOperationError,firstNativeClosedError *operationErrorDiagnostic;cleanupDone <-chan struct{}}
 type sealAttempt struct{released,recoveryAuthorized bool;epoch uint64;target *lifecycleTicket}
 `,
		observationSource: `package dbadmission;import "context";type operationErrorDiagnostic ` + observationTypes["operationErrorDiagnostic"] + `;func diagnosticContextState(ctx context.Context)string ` + observationBodies["diagnosticContextState"],
	}, imp
}
func TestRecoveryQualificationAndCleanupBodiesAreExact(t *testing.T) {
	original, imp := recoveryFixtures(t)
	source := original["recovery_cleanup_ci.go"]
	for _, tc := range []struct{ name, old, new string }{
		{"reviewed", "", ""},
		{"authority omitted", "!t.recoveryAuthorized ||", ""},
		{"epoch inverted", "t.recoveryEpoch != g.seal.epoch", "t.recoveryEpoch == g.seal.epoch"},
		{"past registration permitted", " || t.recoveryEverRegistered", ""},
		{"target permitted", "g.seal.target == t", "g.seal.target != t"},
		{"first observation identity", "first == t.firstNativeClosedError", "first != t.firstNativeClosedError"},
		{"begin permitted", `first.Operation == "statement"`, `first.Operation == "begin"`},
		{"deadline permitted", `first.ContextState == "canceled"`, `first.ContextState == "deadline_exceeded"`},
		{"root active permitted", `first.RootContextState == "canceled"`, `first.RootContextState == "active"`},
		{"dirty close permitted", "observation.closeFailed ||", ""},
		{"larger resource budget", "2 * time.Second", "3 * time.Second"},
		{"foreign native channel", "case <-t.cleanupDone:", "case <-make(chan struct{}):"},
		{"native close unverified", "!t.conn.raw.IsClosed()", "t.conn.raw.IsClosed()"},
		{"generation unverified", "g.tickets[t.generation] != t", "g.tickets[t.generation] == t"},
		{"pending misclassified", "t.state = 'r'", "t.state = 'c'"},
		{"incomplete success", "d.Qualified == d.Completed", "d.Qualified >= d.Completed"},
		{"summary leaked key", `json:"qualified"`, `json:"identity"`},
		{"invented completion", "g.recoveryCompleted++", "g.recoveryCompleted += 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files["recovery_cleanup_ci.go"] = strings.Replace(source, tc.old, tc.new, 1)
				if files["recovery_cleanup_ci.go"] == source {
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
func TestRecoveryQualificationCannotEscape(t *testing.T) {
	original, imp := recoveryFixtures(t)
	for _, body := range []string{
		`func escape(t *lifecycleTicket){t.recoveryAuthorized=true}`,
		`func escape(t *lifecycleTicket){_= &t.recoveryEpoch}`,
		`func escape(t *lifecycleTicket){t.recoveryEverRegistered=false}`,
		`func escape(t *lifecycleTicket){t.cleanupDone=make(chan struct{})}`,
		`func escape(t *lifecycleTicket){t.state='q'}`,
		`func escape(t *lifecycleTicket){t.state='r'}`,
		`func escape(){_=lifecycleTicket{state:'q'}}`,
		`func escape(s *sealAttempt){_=sealAttempt{false,true,0,nil}}`,
		`func escape(t *lifecycleTicket){copy:=*t;_=copy}`,
		`func escape(t *lifecycleTicket){*t=lifecycleTicket{}}`,
		`func escape(){_=lifecycleTicket{recoveryAuthorized:true}}`,
		`func escape(s *sealAttempt){s.recoveryAuthorized=true}`,
		`func escape(g *lifecycleGate){g.recoveryCompleted++}`,
		`func escape(g *lifecycleGate){g.recoveryFinalized=false}`,
		`func escape(d *operationErrorDiagnostic){d.NativeClosedAtEnd=true}`,
		`func escape(g *lifecycleGate){g.finishRecoveryCleanup()}`,
		`func escape(g *lifecycleGate){g.writeRecoveryCleanupDiagnostic()}`,
		`func escape(t *lifecycleTicket){f:=t.contaminateRecoveryLocked;f()}`,
	} {
		t.Run(body, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["other.go"] = "package dbadmission;" + body
			if ds := checkCaptureFiles(t, files, imp); len(ds) == 0 {
				t.Fatal("recovery escape accepted")
			}
		})
	}
}

func TestReleaseAuthorizationRequiresExactFirstRelease(t *testing.T) {
	original, imp := captureFixture(t)
	original = strings.Replace(original, "captureEnded,released bool", "captureEnded,released,recoveryAuthorized bool", 1)
	body := recoveryBodies["lifecycleGate.release"].body
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"first release", body, false},
		{"skip invalid check", strings.Replace(body, " && !g.invalid", "", 1), true},
		{"repeated release gains qualification", strings.Replace(body, "if g.seal.released {", "if false {", 1), true},
		{"deferred authorization", strings.Replace(body, "g.seal.recoveryAuthorized = g.captureObligationsEndedLocked() && !g.invalid", "defer g.captureObligationsEndedLocked();g.seal.recoveryAuthorized=true", 1), true},
		{"alias consumer", strings.Replace(body, "g.seal.recoveryAuthorized = g.captureObligationsEndedLocked() && !g.invalid", "f:=g.captureObligationsEndedLocked;g.seal.recoveryAuthorized=f()&&!g.invalid", 1), true},
		{"release before consume", strings.Replace(body, "g.seal.recoveryAuthorized =", "g.seal.released=true;g.seal.recoveryAuthorized =", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.body == body {
				t.Fatal("mutation unchanged")
			}
			ds := checkCaptureFiles(t, map[string]string{captureEndSource: original, "seal_ci.go": "package dbadmission;func(g *lifecycleGate)release(epoch uint64)error" + tc.body}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}

func TestRecoveryLocalDrainIncludesPhysicalPending(t *testing.T) {
	original, imp := recoveryFixtures(t)
	body := recoveryBodies["lifecycleGate.locallyDrained"].body
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"reviewed physical drain", body, false},
		{"q omitted", strings.Replace(body, " || t.state == 'q'", "", 1), true},
		{"r confused with pending", strings.Replace(body, "t.state == 'q'", "t.state == 'r'", 1), true},
		{"inflight omitted", strings.Replace(body, " || t.operationInFlight != 0", "", 1), true},
		{"unlocked scan", strings.Replace(body, "defer g.mu.Unlock()", "g.mu.Unlock()", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.body == body {
				t.Fatal("mutation unchanged")
			}
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["lifecycle_ci.go"] = "package dbadmission;func(g *lifecycleGate)locallyDrained()bool" + tc.body
			ds := checkCaptureFiles(t, files, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}
