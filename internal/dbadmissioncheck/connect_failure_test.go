package dbadmissioncheck

import (
	"os"
	"strings"
	"testing"
)

func connectFailureFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for _, entry := range []struct{ path, source string }{
		{"context", `package context;type Context interface{Err()error;Value(any)any};var Canceled,DeadlineExceeded error`},
		{"encoding/json", `package json;func Marshal(any)([]byte,error){return nil,nil}`},
		{"errors", `package errors;func Is(error,error)bool{return false};func As(error,any)bool{return false};func New(string)error{return nil}`},
		{"net", `package net;type Error interface{error;Timeout()bool}`},
		{"os", `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`},
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`},
		{pgx + "/pgconn", `package pgconn;type PgError struct{Code string};func(*PgError)Error()string{return ""}`},
		{pgx, `package pgx;type Conn struct{};type ConnConfig struct{}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	data, err := os.ReadFile("../dbadmission/connect_failure_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		connectFailureSource: string(data),
		"fixture.go": `package dbadmission;import("sync";"context")
 type invalidationReason string
 const invalidConnectFailed invalidationReason="connect_failed"
 type lifecycleGate struct{mu sync.Mutex;firstInvalidReason invalidationReason;firstInvalidPhase string;firstConnectFailure *connectFailureDiagnostic;workerContext context.Context;pending int;invalidateBecauseLocked func(invalidationReason);unlockAndPublishInvalid func();notifyLocked func()}
 type lifecycleTicket struct{gate *lifecycleGate;birthPhase string;birthCaptureEnded,recoveryAuthorized bool;state byte}
 type lifecyclePermit interface{failed();connected(*Conn)}
 func(t *lifecycleTicket)failed(){}
 func(t *lifecycleTicket)connected(*Conn){}
 `,
		"conn.go":         `package dbadmission;import "github.com/jackc/pgx/v5";type Conn struct{raw *pgx.Conn;permit lifecyclePermit}`,
		observationSource: `package dbadmission;import "context";func diagnosticContextState(ctx context.Context)string ` + observationBodies["diagnosticContextState"] + `;func validOperationPhase(phase string)bool ` + observationBodies["validOperationPhase"] + `;func validOperationContext(state string)bool ` + observationBodies["validOperationContext"],
	}, imp
}
func TestConnectFailureFactsStayClosedAndStickyFirst(t *testing.T) {
	original, imp := connectFailureFixtures(t)
	source := original[connectFailureSource]
	for _, tc := range []struct{ name, old, new string }{
		{"reviewed complete helper", "", ""},
		{"extra JSON identity", `json:"rawNil"`, `json:"identity"`},
		{"network mislabeled", `return "network_error"`, `return "canceled"`},
		{"authentication prefix", `pgError.Code[:2] == "28"`, `pgError.Code[:2] == "57"`},
		{"later cancel relabels error", "category := diagnosticConnectErrorCategory(err)", `category := diagnosticContextState(ctx)`},
		{"skip sticky", "g.invalidateBecauseLocked(invalidConnectFailed)", ""},
		{"overwrite first record", "if first {", "if !first {"},
		{"wrong failure event", "owned_ci_first_connect_failure", "other_event"},
		{"invented raw nil", "diagnosticContextState(g.workerContext), rawNil,", "diagnosticContextState(g.workerContext), true,"},
		{"failed becomes clean", "t.state = 'f'", "t.state = 'c'"},
		{"wrong reason projection", "g.firstInvalidReason != invalidConnectFailed", "g.firstInvalidReason == invalidConnectFailed"},
		{"extra output", "d := g.connectFailureDiagnostic()", `_,_=os.Stderr.Write([]byte("extra"));d := g.connectFailureDiagnostic()`},
		{"unknown category accepted", `case "none", "canceled", "deadline_exceeded", "authentication", "network_error", "other":`, `case "none", "canceled", "deadline_exceeded", "authentication", "network_error", "other", "unknown":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			if tc.old != "" {
				files[connectFailureSource] = strings.Replace(source, tc.old, tc.new, 1)
				if files[connectFailureSource] == source {
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
func TestConnectFailureCapabilitiesHaveSoleCallers(t *testing.T) {
	original, imp := connectFailureFixtures(t)
	for _, source := range []string{
		`func escape(){_=observeLifecycleConnectFailure}`,
		`func escape(t *lifecycleTicket){t.failedObserved(nil,nil,true)}`,
		`func escape(t *lifecycleTicket){f:=t.failedObserved;f(nil,nil,true)}`,
		`func escape(){_=diagnosticConnectErrorCategory(nil)}`,
		`func escape(g *lifecycleGate){g.firstConnectFailure=nil}`,
		`func escape(){_=connectFailureDiagnostic{RawNil:true}}`,
		`func escape(d *connectFailureDiagnostic){d.ErrorCategory="canceled"}`,
		`func escape(d *connectFailureDiagnostic){*d=connectFailureDiagnostic{}}`,
		`func escape(g *lifecycleGate){g.writeConnectFailureDiagnostic()}`,
		`func escape(g *lifecycleGate){_=g.connectFailureDiagnostic()}`,
	} {
		t.Run(source, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["other.go"] = "package dbadmission;" + source
			if ds := checkCaptureFiles(t, files, imp); len(ds) == 0 {
				t.Fatal("connect diagnostic capability escaped")
			}
		})
	}
}
func TestConnectFailureHelperOnlyAtExactFailedConnect(t *testing.T) {
	original, imp := connectFailureFixtures(t)
	body := connectFailureBodies["connectWithPhase"].body
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"real managed connect", body, false},
		{"admission phase relabelled", strings.Replace(body, `return nil, err, "admission"`, `return nil, err, "native"`, 1), true},
		{"native phase relabelled", strings.Replace(body, `return nil, err, "native"`, `return nil, err, "admission"`, 1), true},
		{"error identity substituted", strings.Replace(body, `return nil, err, "native"`, `return nil, errors.New("changed"), "native"`, 1), true},
		{"success invented phase", strings.Replace(body, `return conn, nil, "none"`, `return conn, nil, "native"`, 1), true},
		{"wrong failure predicate", strings.Replace(body, "err != nil || raw == nil", "err != nil && raw == nil", 1), true},
		{"raw fact inverted", strings.Replace(body, "err, raw == nil", "err, raw != nil", 1), true},
		{"async failure observation", strings.Replace(body, "observeLifecycleConnectFailure(", "go observeLifecycleConnectFailure(", 1), true},
		{"deferred failure observation", strings.Replace(body, "observeLifecycleConnectFailure(", "defer observeLifecycleConnectFailure(", 1), true},
		{"nil error substituted", strings.Replace(body, "ctx, permit, err, raw == nil", "ctx, permit, nil, raw == nil", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.body == body {
				t.Fatal("mutation unchanged")
			}
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["conn.go"] = `package dbadmission;import("context";"errors";"github.com/jackc/pgx/v5");type Conn struct{raw *pgx.Conn;permit lifecyclePermit};var acquireLifecycle func(context.Context)(lifecyclePermit,error);var observeLifecycleConfig func(*pgx.ConnConfig,lifecyclePermit)*pgx.ConnConfig;func connectWithPhase(ctx context.Context,config *pgx.ConnConfig,connect func(context.Context,*pgx.ConnConfig)(*pgx.Conn,error))(*Conn,error,string)` + tc.body
			ds := checkCaptureFiles(t, files, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
}
func TestConnectFailureDefaultRemainsOriginalFailure(t *testing.T) {
	_, imp := connectFailureFixtures(t)
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`{if permit!=nil{permit.failed()}}`, false},
		{`{}`, true},
		{`{if permit!=nil{go permit.failed()}}`, true},
	} {
		_, ds := fixtureCheck(t, managed, "connect_failure_default.go", `package dbadmission;import "context";type lifecyclePermit interface{failed()};func observeLifecycleConnectFailure(_ context.Context,permit lifecyclePermit,_ error,_ bool)`+tc.body, imp)
		if (len(ds) > 0) != tc.bad {
			t.Fatalf("diagnostics=%v", ds)
		}
	}
}
