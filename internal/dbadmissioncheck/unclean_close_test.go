package dbadmissioncheck

import (
	"os"
	"strings"
	"testing"
)

func uncleanFixtures(t *testing.T) (string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for path, source := range map[string]string{
		"context":       `package context;type Context interface{Err()error;Value(any)any};var Canceled,DeadlineExceeded error`,
		"sync":          `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){}`,
		"encoding/json": `package json;func Marshal(any)([]byte,error){return nil,nil}`,
		"os":            `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`,
	} {
		p, _ := fixtureCheck(t, path, "fixture.go", source, imp)
		imp[path] = p
	}
	data, err := os.ReadFile("../dbadmission/application_diagnostic_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "type uncleanCloseDiagnostic struct")
	if start < 0 {
		t.Fatal("unclean diagnostic absent")
	}
	source := `package dbadmission;import("encoding/json";"os";"sync";"context")
 type invalidationReason string;const invalidUncleanClose invalidationReason="unclean_close"
 type lifecycleGate struct{mu sync.Mutex;firstUncleanOperation *uncleanOperationDiagnostic;workerContext context.Context;writeUncleanOperationDiagnostic func();firstInvalidReason invalidationReason;firstUncleanClose *uncleanCloseDiagnostic;seal *sealAttempt;invalidateBecauseLocked func(invalidationReason)}
 type sealAttempt struct{target *lifecycleTicket}
 type lifecycleTicket struct{gate *lifecycleGate;birthPhase string;birthCaptureEnded bool;firstOperationError,firstNativeClosedError *operationErrorDiagnostic}
 type closeObservation struct{alreadyClosed,closeFailed,cleanupDone bool}
 func(g *lifecycleGate)finalInvalidationDiagnostic()struct{}{return struct{}{}}
 ` + string(data)[start:]
	return source, imp
}
func TestUncleanDiagnosticProjectionIsClosed(t *testing.T) {
	original, imp := uncleanFixtures(t)
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"reviewed closed record", original, false},
		{"extra key", strings.Replace(original, `json:"role"`, `json:"identity"`, 1), true},
		{"wrong event", strings.Replace(original, `d.Event != "owned_ci_first_unclean_close"`, `d.Event != "other"`, 1), true},
		{"wrong cause gate", strings.Replace(original, "g.firstInvalidReason != invalidUncleanClose", "g.firstInvalidReason == invalidUncleanClose", 1), true},
		{"skip nil projection", strings.Replace(original, "if d == nil", "if d != nil", 1), true},
		{"extra output", strings.Replace(original, "d := g.uncleanCloseDiagnostic()", `_,_=os.Stderr.Write([]byte("extra"));d := g.uncleanCloseDiagnostic()`, 1), true},
		{"writer alias", original + `var writer=(*lifecycleGate).writeUncleanCloseDiagnostic`, true},
		{"projection escape", original + `func other(g *lifecycleGate){_=g.uncleanCloseDiagnostic()}`, true},
		{"first record reset", original + `func other(g *lifecycleGate){g.firstUncleanClose=nil}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			ds := checkUncleanFiles(t, map[string]string{"application_diagnostic_ci.go": tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestUncleanFirstRecordFollowsStickyInvalidation(t *testing.T) {
	original, imp := uncleanFixtures(t)
	prefix := `package dbadmission;func(t *lifecycleTicket)closedObserved(observation closeObservation){g:=t.gate;clean:=!observation.alreadyClosed&&!observation.closeFailed&&observation.cleanupDone;if !clean`
	for _, tc := range []struct {
		name, file, body string
		bad              bool
	}{
		{"first record after sticky", "lifecycle_ci.go", uncleanCaptureBody, false},
		{"wrong source", "other.go", uncleanCaptureBody, true},
		{"wrong event value", "lifecycle_ci.go", strings.Replace(uncleanCaptureBody, "owned_ci_first_unclean_close", "other_event", 1), true},
		{"overwrite first record", "lifecycle_ci.go", strings.Replace(uncleanCaptureBody, "if first {", "if !first {", 1), true},
		{"omit sticky", "lifecycle_ci.go", strings.Replace(uncleanCaptureBody, "g.invalidateBecauseLocked(invalidUncleanClose)", "", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkUncleanFiles(t, map[string]string{"application_diagnostic_ci.go": original, tc.file: prefix + tc.body + "}"}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestObservedCloseHasOnlyManagedCaller(t *testing.T) {
	original, imp := uncleanFixtures(t)
	native := `package dbadmission;type lifecyclePermit interface{closedObserved(closeObservation)};type Conn struct{permit lifecyclePermit};func(c *Conn)Close(){alreadyClosed:=false;var err error;cleanupDone:=true;c.permit.closedObserved(closeObservation{alreadyClosed,err!=nil,cleanupDone})}`
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"native observed close", "conn.go", native, false},
		{"wrong caller file", "other.go", native, true},
		{"inverted close observation", "conn.go", strings.Replace(native, "err!=nil", "err==nil", 1), true},
		{"async close observation", "conn.go", strings.Replace(native, "c.permit.closedObserved(", "go c.permit.closedObserved(", 1), true},
		{"caller alias", "conn.go", strings.Replace(native, "c.permit.closedObserved(closeObservation", "f:=c.permit.closedObserved;f(closeObservation", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkUncleanFiles(t, map[string]string{"application_diagnostic_ci.go": original, tc.file: tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func checkUncleanFiles(t *testing.T, sources map[string]string, imp fixtureImporter) []Diagnostic {
	sources[observationSource] = `package dbadmission;import "context";type operationErrorDiagnostic ` + observationTypes["operationErrorDiagnostic"] + `;type uncleanOperationDiagnostic ` + observationTypes["uncleanOperationDiagnostic"] + `;func diagnosticContextState(ctx context.Context)string ` + observationBodies["diagnosticContextState"]
	return checkCaptureFiles(t, sources, imp)
}
