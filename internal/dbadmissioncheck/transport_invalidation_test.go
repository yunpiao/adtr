package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func transportInvalidationFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	files, imp := queuedParkFixtures(t)
	for _, entry := range []struct{ path, source string }{
		{"sync", `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){};type Once struct{};func(*Once)Do(func()){}`},
		{"sync/atomic", `package atomic;type Bool struct{};func(*Bool)Load()bool{return false}`},
		{"os", `package os;import "time";type File struct{};var ErrClosed error;func(*File)Close()error{return nil};func(*File)SetWriteDeadline(time.Time)error{return nil};func(*File)Write([]byte)(int,error){return 0,nil}`},
		{"errors", `package errors;func Is(error,error)bool{return false}`},
		{"io", `package io;type Reader interface{Read([]byte)(int,error)};type Writer interface{Write([]byte)(int,error)};type Closer interface{Close()error};var EOF error`},
		{"bytes", `package bytes;import "io";func NewReader([]byte)io.Reader{return nil}`},
		{"bufio", `package bufio;import "io";type Reader struct{};func NewReaderSize(io.Reader,int)*Reader{return nil};func(*Reader)ReadSlice(byte)([]byte,error){return nil,nil}`},
		{"encoding/json", `package json;import "io";func Marshal(any)([]byte,error){return nil,nil};type Decoder struct{};func NewDecoder(io.Reader)*Decoder{return nil};func(*Decoder)DisallowUnknownFields(){};func(*Decoder)Decode(any)error{return nil}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	files["fixture.go"] = strings.Replace(files["fixture.go"], "mu sync.Mutex;queuedParked", "mu sync.Mutex;pin func(uint64,uint64,func())error;release func(uint64)error;frame func(uint64)sealFrame;queuedParked", 1)
	files["fixture.go"] += `
const invalidTransport invalidationReason="transport"`
	files["seal_ci.go"] = `package dbadmission;func(g *lifecycleGate)invalidateTransport()` + transportInvalidationBody
	files["application_controller_ci.go"] = strings.Replace(files["application_controller_ci.go"], `import("os";"time";"encoding/json")`, `import("os";"time";"encoding/json";"sync/atomic";"errors")`, 1) + `
 func recordApplicationFailure(file *os.File,g *lifecycleGate,stopping *atomic.Bool,onFailure func(),original error)` + finalQuiesceBodies["recordApplicationFailure"].body
	data, err := os.ReadFile("../dbadmission/seal_protocol_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	files["seal_protocol_ci.go"] = string(data)
	return files, imp
}
func TestTransportFailureNeverRepublishesItsOwnFailure(t *testing.T) {
	original, imp := transportInvalidationFixtures(t)
	for _, tc := range []struct{ name, file, old, new string }{
		{"real failure and legacy helper", "seal_ci.go", "", ""},
		{"publisher reentry", "seal_ci.go", "g.mu.Unlock()", "g.unlockAndPublishInvalid()"},
		{"unpark before sticky", "seal_ci.go", "g.mu.Lock()", "g.mu.Lock();g.finalParked=false"},
		{"unconditional final state", "seal_ci.go", "if g.finalParked {", "if true {"},
		{"unconditional queued state", "seal_ci.go", "if g.queuedParked {", "if true {"},
		{"wrong final state", "seal_ci.go", `g.finalQuiesce.state = "failed"`, `g.finalQuiesce.state = "cancelled"`},
		{"wrong queued state", "seal_ci.go", `g.queuedState = "failed"`, `g.queuedState = "complete"`},
		{"sticky omitted", "seal_ci.go", "g.invalidateBecauseLocked(invalidTransport)", "g.notifyLocked()"},
		{"notification omitted", "seal_ci.go", "g.notifyLocked()", "_ = g.notifyLocked"},
		{"failure final abort reentry", "application_controller_ci.go", "g.invalidateTransport()", "g.invalidateTransport();g.abortFinalQuiesce()"},
		{"failure queued abort reentry", "application_controller_ci.go", "g.invalidateTransport()", "g.invalidateTransport();g.abortQueuedSelection()"},
		{"failure callback omitted", "application_controller_ci.go", "onFailure()", "_ = onFailure"},
		{"failure close omitted", "application_controller_ci.go", "file.Close()", "_ = file"},
		{"failure sticky after callback", "application_controller_ci.go", "g.invalidateTransport()", "if onFailure!=nil{onFailure()};g.invalidateTransport()"},
		{"async transport invalidation", "application_controller_ci.go", "g.invalidateTransport()", "go g.invalidateTransport()"},
		{"legacy method alias", "seal_protocol_ci.go", "gate.invalidateTransport()", "f:=gate.invalidateTransport;f()"},
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
func TestTransportInvalidationCannotEscapeReviewedCallers(t *testing.T) {
	original, imp := transportInvalidationFixtures(t)
	for _, source := range []string{
		`func escape(g *lifecycleGate){g.invalidateTransport()}`,
		`func escape(g *lifecycleGate){_=g.invalidateTransport}`,
		`func escape(g *lifecycleGate){go g.invalidateTransport()}`,
		`func escape(g *lifecycleGate){defer g.invalidateTransport()}`,
		`func escape(g *lifecycleGate){g.finalParked=false}`,
		`func escape(g *lifecycleGate){g.queuedParked=false}`,
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
func TestTransportBodiesMatchProductionWithComments(t *testing.T) {
	for _, tc := range []struct{ file, name, body string }{{"seal_ci.go", "invalidateTransport", transportInvalidationBody}, {"seal_protocol_ci.go", "runSealProtocol", legacySealTransportBody}} {
		data, err := os.ReadFile("../dbadmission/" + tc.file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), tc.file, data, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == tc.name {
				found = true
				if !captureBodyMatches(fn, tc.body) {
					t.Fatal("transport body drift")
				}
			}
		}
		if !found {
			t.Fatal("transport function missing")
		}
	}
}
