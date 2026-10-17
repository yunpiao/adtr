package dbadmissioncheck

import (
	"os"
	"strings"
	"testing"
)

func observerFixtures(t *testing.T) (string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for _, entry := range []struct{ path, source string }{
		{"context", `package context;type Context interface{Err()error;Value(any)any};var Canceled,DeadlineExceeded error;func WithValue(c Context,k,v any)Context{return c}`},
		{"encoding/json", `package json;func Marshal(any)([]byte,error){return nil,nil}`},
		{"errors", `package errors;func Is(error,error)bool{return false}`},
		{"os", `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`},
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`},
		{pgx + "/pgconn", `package pgconn;var ErrConnClosed error`},
		{pgx, `package pgx;import "context";type Conn struct{};func(*Conn)IsClosed()bool{return false};type TraceQueryStartData struct{SQL string;Args []any};type TraceQueryEndData struct{Err error};type QueryTracer interface{TraceQueryStart(context.Context,*Conn,TraceQueryStartData)context.Context;TraceQueryEnd(context.Context,*Conn,TraceQueryEndData)};type ConnConfig struct{Tracer QueryTracer};func(c *ConnConfig)Copy()*ConnConfig{return c}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	data, err := os.ReadFile("../dbadmission/operation_observation_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "import (", "import (\n\"sync\"", 1) + `
 type invalidationReason string;const invalidUncleanClose invalidationReason="unclean_close"
 type lifecycleGate struct{mu sync.Mutex;application bool;workerContext context.Context;firstInvalidReason invalidationReason;firstUncleanOperation *uncleanOperationDiagnostic;recoveryFinalized bool}
 type lifecycleTicket struct{gate *lifecycleGate;conn *Conn;state byte;firstOperationError,firstNativeClosedError *operationErrorDiagnostic;operationInFlight int;operationSequence,activeOperation uint64;contaminateRecoveryLocked func()}
 var coordinator struct{sync.Mutex;gate *lifecycleGate}
 func(g *lifecycleGate)diagnosticPhaseLocked()string{return "open"}
 `
	return source, imp
}
func checkObserverFiles(t *testing.T, source string, extra map[string]string, imp fixtureImporter) []Diagnostic {
	files := map[string]string{observationSource: source, "conn.go": `package dbadmission;import "github.com/jackc/pgx/v5";type Conn struct{raw *pgx.Conn};type lifecyclePermit interface{}`}
	for k, v := range extra {
		files[k] = v
	}
	return checkCaptureFiles(t, files, imp)
}
func TestObserverNativeAndConfigCapabilitiesAreExact(t *testing.T) {
	original, imp := observerFixtures(t)
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"reviewed observer", original, false},
		{"overlap ignored", strings.Replace(original, "t.operationInFlight != 0", "t.operationInFlight == 0", 1), true},
		{"sequence mismatch ignored", strings.Replace(original, "trace.sequence != t.activeOperation", "trace.sequence == t.activeOperation", 1), true},
		{"rollback wrong sentinel", strings.Replace(original, "pgconn.ErrConnClosed", "context.Canceled", 1), true},
		{"rollback permitted while open", strings.Replace(original, `operation == "rollback" && nativeClosed`, `operation == "rollback" && !nativeClosed`, 1), true},
		{"forged trace identity", original + `func other(){_=operationTraceKey{}}`, true},
		{"trace sequence mutation", original + `func other(trace *operationTrace){trace.sequence=2}`, true},
		{"first error altered", original + `func other(d *operationErrorDiagnostic){d.NativeClosedAtEnd=true}`, true},
		{"overwrite existing tracer", strings.Replace(original, "config.Tracer != nil", "config.Tracer == nil", 1), true},
		{"mutate caller config", strings.Replace(original, "copy := config.Copy()", "copy := config", 1), true},
		{"extra config effect", strings.Replace(original, "copy.Tracer =", "config.Tracer=nil;copy.Tracer =", 1), true},
		{"native method alias", strings.Replace(original, "nativeClosed := conn != nil && conn.IsClosed()", "isClosed:=conn.IsClosed;nativeClosed := conn != nil && isClosed()", 1), true},
		{"raw connection escape", strings.Replace(original, "nativeClosed := conn != nil && conn.IsClosed()", "var leaked any=conn;_=leaked;nativeClosed := conn != nil && conn.IsClosed()", 1), true},
		{"SQL disclosure", strings.Replace(original, `operation := "statement"`, `operation := data.SQL`, 1), true},
		{"false context category", strings.Replace(original, `return "unknown"`, `return "canceled"`, 1), true},
		{"native result invented", strings.Replace(original, "nativeClosed := conn != nil && conn.IsClosed()", "nativeClosed := conn != nil", 1), true},
		{"wrong trace owner", strings.Replace(original, "trace.observer != o", "trace.observer == o", 1), true},
		{"extra diagnostic key", strings.Replace(original, `json:"operation"`, `json:"sql"`, 1), true},
		{"dirty projection gate", strings.Replace(original, "g.firstInvalidReason != invalidUncleanClose", "g.firstInvalidReason == invalidUncleanClose", 1), true},
		{"writer additional output", strings.Replace(original, "d := g.uncleanOperationDiagnostic()", `_,_=os.Stderr.Write([]byte("extra"));d := g.uncleanOperationDiagnostic()`, 1), true},
		{"writer escapes", original + `func other(g *lifecycleGate){g.writeUncleanOperationDiagnostic()}`, true},
		{"root observer alias", original + `var other=ObserveCIWorkerContext`, true},
		{"config observer alias", original + `var other=observeLifecycleConfig`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			ds := checkObserverFiles(t, tc.source, nil, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestObserverDefaultConfigurationRemainsPassthrough(t *testing.T) {
	_, imp := observerFixtures(t)
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"default passthrough", `package dbadmission;import "github.com/jackc/pgx/v5";type lifecyclePermit interface{};func observeLifecycleConfig(config *pgx.ConnConfig,_ lifecyclePermit)*pgx.ConnConfig{return config}`, false},
		{"default copy forbidden", `package dbadmission;import "github.com/jackc/pgx/v5";type lifecyclePermit interface{};func observeLifecycleConfig(config *pgx.ConnConfig,_ lifecyclePermit)*pgx.ConnConfig{return config.Copy()}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds := fixtureCheck(t, managed, "operation_observation_default.go", tc.source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestObserverRootBridgeIsInstalledOnly(t *testing.T) {
	_, imp := observerFixtures(t)
	managedPackage, _ := fixtureCheck(t, managed, "fixture.go", `package dbadmission;import "context";func ObserveCIWorkerContext(context.Context){}`, imp)
	imp[managed] = managedPackage
	wrapper := `package main;import("context";"github.com/yunpiao/adtr/internal/dbadmission");func observeCIWorkerContext(ctx context.Context){dbadmission.ObserveCIWorkerContext(ctx)}`
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"installed context", `if admissionFailure!=nil{observeCIWorkerContext(ctx)}`, false},
		{"uninstalled context", `observeCIWorkerContext(ctx)`, true},
		{"root alias", `if admissionFailure!=nil{f:=observeCIWorkerContext;f(ctx)}`, true},
		{"root closure", `if admissionFailure!=nil{func(){observeCIWorkerContext(ctx)}()}`, true},
		{"root async", `if admissionFailure!=nil{go observeCIWorkerContext(ctx)}`, true},
		{"root deferred", `if admissionFailure!=nil{defer observeCIWorkerContext(ctx)}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkJournalFiles(t, map[string]string{"db_observation_ci.go": wrapper, "main.go": `package main;import "context";func run(ctx context.Context,admissionFailure <-chan struct{}){` + tc.body + `}`}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestBirthCaptureObservationCannotAuthorize(t *testing.T) {
	source, imp := captureFixture(t)
	source = strings.Replace(source, "type lifecycleTicket struct{", "type lifecycleTicket struct{birthCaptureEnded bool;", 1)
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"cached bit into new ticket", `func(g *lifecycleGate)acquire(){_=lifecycleTicket{birthCaptureEnded:g.seal!=nil&&g.seal.captureEnded}}`, false},
		{"cache outside ticket", `func(g *lifecycleGate)acquire(){_=g.seal.captureEnded}`, true},
		{"different destination", `type other struct{birthCaptureEnded bool};func(g *lifecycleGate)acquire(){_=other{birthCaptureEnded:g.seal!=nil&&g.seal.captureEnded}}`, true},
		{"authorization mutation", `func(g *lifecycleGate)acquire(){g.seal.captureEnded=true}`, true},
		{"authorization consumer", `func(g *lifecycleGate)acquire(){_=g.captureObligationsEndedLocked()}`, true},
		{"wrong caller", `func(g *lifecycleGate)other(){_=lifecycleTicket{birthCaptureEnded:g.seal!=nil&&g.seal.captureEnded}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkCaptureFiles(t, map[string]string{captureEndSource: source, "lifecycle_ci.go": "package dbadmission;" + tc.body}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestObserverConfigHasOnlyConnectCaller(t *testing.T) {
	source, imp := observerFixtures(t)
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"managed connect", `_=observeLifecycleConfig(config,permit)`, false},
		{"config alias", `f:=observeLifecycleConfig;_=f(config,permit)`, true},
		{"config async", `go observeLifecycleConfig(config,permit)`, true},
		{"config defer", `defer observeLifecycleConfig(config,permit)`, true},
		{"config closure", `func(){_=observeLifecycleConfig(config,permit)}()`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]string{"conn.go": `package dbadmission;import "github.com/jackc/pgx/v5";type Conn struct{raw *pgx.Conn};type lifecyclePermit interface{};func connectWith(config *pgx.ConnConfig,permit lifecyclePermit){` + tc.body + `}`}
			ds := checkObserverFiles(t, source, extra, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
