package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestFinalDiagnosticCapabilityIsExact(t *testing.T) {
	imp := fixtureImporter{}
	for path, source := range map[string]string{
		"sync":          `package sync;type Once struct{};func(*Once)Do(f func()){f()};type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){};type WaitGroup struct{};func(*WaitGroup)Wait(){}`,
		"encoding/json": `package json;func Marshal(any)([]byte,error){return nil,nil}`,
		"os":            `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`,
	} {
		p, _ := fixtureCheck(t, path, "fixture.go", source, imp)
		imp[path] = p
	}
	writer := `package dbadmission
 import("encoding/json";"os";"sync")
 type lifecycleGate struct{mu sync.Mutex;finalTransportMu sync.Mutex;captureEndFD int;finishRecoveryCleanup func();writeUncleanCloseDiagnostic func();writeUncleanOperationDiagnostic func();writeConnectFailureDiagnostic func();writeRecoveryCleanupDiagnostic func()}
 func(g *lifecycleGate)finalInvalidationDiagnostic()struct{}{return struct{}{}}
 func(g *lifecycleGate)writeFinalInvalidationDiagnostic(){
 data,err:=json.Marshal(g.finalInvalidationDiagnostic())
 if err==nil{_,_=os.Stderr.Write(append(data,'\n'))}
 g.writeUncleanCloseDiagnostic()
 g.writeUncleanOperationDiagnostic()
 g.writeConnectFailureDiagnostic()
 }
 `
	caller := `package dbadmission;import "sync";type shutdownFlag struct{};func(*shutdownFlag)Store(bool){};type closer struct{};func(*closer)Close(){};func StartCIController(g *lifecycleGate){var stopOnce,reportOnce,recoveryOnce sync.Once;var wg sync.WaitGroup;var stopping shutdownFlag;normal,rescue,captureEnd:=&closer{},&closer{},&closer{};cleanup:=func()` + recoveryCleanupBody + `;cleanup()}`
	for _, tc := range []struct {
		name, file, writer, caller string
		bad                        bool
	}{
		{"once closed projection", "application_controller_ci.go", writer, caller, false},
		{"newline statement separators", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic();g.writeRecoveryCleanupDiagnostic()", "g.writeFinalInvalidationDiagnostic()\ng.writeRecoveryCleanupDiagnostic()", 1), false},
		{"reversed final writers", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic();g.writeRecoveryCleanupDiagnostic()", "g.writeRecoveryCleanupDiagnostic();g.writeFinalInvalidationDiagnostic()", 1), true},
		{"missing recovery writer", "application_controller_ci.go", writer, strings.Replace(caller, ";g.writeRecoveryCleanupDiagnostic()", "", 1), true},
		{"extra final writer", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic();", "g.writeFinalInvalidationDiagnostic();g.writeFinalInvalidationDiagnostic();", 1), true},
		{"wrong caller file", "other.go", writer, caller, true},
		{"wrong function", "application_controller_ci.go", writer, strings.Replace(caller, "StartCIController", "other", 1), true},
		{"async writer", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic()", "go g.writeFinalInvalidationDiagnostic()", 1), true},
		{"deferred writer", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic()", "defer g.writeFinalInvalidationDiagnostic()", 1), true},
		{"method alias", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic()", "f:=g.writeFinalInvalidationDiagnostic;f()", 1), true},
		{"extra output", "application_controller_ci.go", strings.Replace(writer, "data,err:=json.Marshal", `_,_=os.Stderr.Write([]byte("extra"));data,err:=json.Marshal`, 1), caller, true},
		{"different payload", "application_controller_ci.go", strings.Replace(writer, "json.Marshal(g.finalInvalidationDiagnostic())", `json.Marshal("private")`, 1), caller, true},
		{"projection escapes", "application_controller_ci.go", writer, strings.Replace(caller, "g.writeFinalInvalidationDiagnostic()", "_=g.finalInvalidationDiagnostic()", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := token.NewFileSet()
			var files []*ast.File
			for name, source := range map[string]string{"application_diagnostic_ci.go": tc.writer, tc.file: tc.caller} {
				f, err := parser.ParseFile(fs, name, source, 0)
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
			ds := Check(fs, managed, files, info)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
