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

func journalFixtures(t *testing.T) (string, fixtureImporter) {
	t.Helper()
	imp := fixtureImporter{}
	for path, source := range map[string]string{
		"crypto/sha256": `package sha256;func Sum256([]byte)[32]byte{return [32]byte{}}`,
		"encoding/hex":  `package hex;func EncodeToString([]byte)string{return "hash"}`,
		"encoding/json": `package json;func Marshal(any)([]byte,error){return nil,nil}`,
		"os":            `package os;type File struct{};var Stderr *File;func(*File)Write([]byte)(int,error){return 0,nil}`,
		journalPackage:  `package operationallogs;type Module string;const Worker Module="worker";type RecorderObservation struct{ProcessID string;Module Module;Accepted,Acknowledged,Rejected,QueueFull,WriteFailures,Unacknowledged,Abandoned int64;AcceptanceStopped bool};type Recorder struct{};func(*Recorder)Stats()RecorderObservation{return RecorderObservation{}};func(*Recorder)Close(int)error{return nil};type CIJournalFailure struct{};func(*Recorder)CIJournalFirstFailure()CIJournalFailure{return CIJournalFailure{}}`,
	} {
		p, _ := fixtureCheck(t, path, "fixture.go", source, imp)
		imp[path] = p
	}
	data, err := os.ReadFile("../../cmd/adtr/db_journal_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(data), imp
}
func checkJournalFiles(t *testing.T, sources map[string]string, imp fixtureImporter) []Diagnostic {
	t.Helper()
	fs := token.NewFileSet()
	var files []*ast.File
	for name, source := range sources {
		f, err := parser.ParseFile(fs, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Importer: imp}
	if _, err := config.Check(module+"/cmd/adtr", fs, files, info); err != nil {
		t.Fatal(err)
	}
	return Check(fs, module+"/cmd/adtr", files, info)
}
func TestJournalFinalClosedWriter(t *testing.T) {
	original, imp := journalFixtures(t)
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"fixed projection", original, false},
		{"raw ID alongside hash", strings.Replace(original, "hex.EncodeToString(sum[:])", "s.ProcessID + hex.EncodeToString(sum[:])", 1), true},
		{"renamed public field", strings.Replace(original, `json:"processIDSHA256"`, `json:"processID"`, 1), true},
		{"invented close success", strings.Replace(original, "closeErr == nil", "true", 1), true},
		{"extra output", strings.Replace(original, "snapshot :=", `_,_=os.Stderr.Write([]byte("extra"));snapshot :=`, 1), true},
		{"snapshot alias", original + `func other(r *operationallogs.Recorder){f:=r.Stats;_=f()}`, true},
		{"snapshot outside writer", original + `func other(r *operationallogs.Recorder){_=r.Stats()}`, true},
		{"projection alias", original + `var other=ciJournalProjection`, true},
		{"failure writer alias", original + `var other=emitCIJournalFirstFailure`, true},
		{"failure writer outside final", original + `func other(r *operationallogs.Recorder){emitCIJournalFirstFailure(r)}`, true},
		{"failure getter alias", original + `func other(r *operationallogs.Recorder){f:=r.CIJournalFirstFailure;_=f()}`, true},
		{"failure getter outside writer", original + `func other(r *operationallogs.Recorder){_=r.CIJournalFirstFailure()}`, true},
		{"async failure writer", strings.Replace(original, "emitCIJournalFirstFailure(recorder)", "go emitCIJournalFirstFailure(recorder)", 1), true},
		{"deferred failure writer", strings.Replace(original, "emitCIJournalFirstFailure(recorder)", "defer emitCIJournalFirstFailure(recorder)", 1), true},
		{"substituted failure recorder", strings.Replace(original, "emitCIJournalFirstFailure(recorder)", "emitCIJournalFirstFailure(new(operationallogs.Recorder))", 1), true},
		{"missing failure writer", strings.Replace(original, "emitCIJournalFirstFailure(recorder)", "", 1), true},
		{"failure writer raw output", strings.Replace(original, "data, err := json.Marshal(recorder.CIJournalFirstFailure())", `data, err := json.Marshal("private")`, 1), true},
		{"failure getter substitute", strings.Replace(original, "recorder.CIJournalFirstFailure()", "new(operationallogs.Recorder).CIJournalFirstFailure()", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			ds := checkJournalFiles(t, map[string]string{"db_journal_ci.go": tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestJournalFinalRequiresActualCloseResult(t *testing.T) {
	writer, imp := journalFixtures(t)
	original := `package main;import "github.com/yunpiao/adtr/internal/operationallogs";func run(journal *operationallogs.Recorder,admissionFailure <-chan struct{},mode *string){drain:=0;defer func(){closeErr:=journal.Close(drain);if admissionFailure!=nil && *mode=="worker"{emitCIJournalFinal(journal,closeErr)};_=closeErr}()}`
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"post close installed worker", "main.go", original, false},
		{"wrong file", "other.go", original, true},
		{"wrong caller", "main.go", strings.Replace(original, "func run(", "func other(", 1), true},
		{"missing installed check", "main.go", strings.Replace(original, "admissionFailure!=nil && ", "", 1), true},
		{"missing worker check", "main.go", strings.Replace(original, ` && *mode=="worker"`, "", 1), true},
		{"wrong close error", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "emitCIJournalFinal(journal,nil)", 1), true},
		{"different recorder", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "emitCIJournalFinal(new(operationallogs.Recorder),closeErr)", 1), true},
		{"shadow close error", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "var closeErr error;emitCIJournalFinal(journal,closeErr)", 1), true},
		{"shadow recorder", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "journal:=new(operationallogs.Recorder);emitCIJournalFinal(journal,closeErr)", 1), true},
		{"writer alias", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "f:=emitCIJournalFinal;f(journal,closeErr)", 1), true},
		{"async writer", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "go emitCIJournalFinal(journal,closeErr)", 1), true},
		{"deferred writer", "main.go", strings.Replace(original, "emitCIJournalFinal(journal,closeErr)", "defer emitCIJournalFinal(journal,closeErr)", 1), true},
		{"synthetic close result", "main.go", strings.Replace(original, "closeErr:=journal.Close(drain)", "_=drain;var closeErr error", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.file == "main.go" && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			ds := checkJournalFiles(t, map[string]string{"db_journal_ci.go": writer, tc.file: tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestJournalDefaultAndExistingSnapshot(t *testing.T) {
	_, imp := journalFixtures(t)
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"default no-op", `package main;import "github.com/yunpiao/adtr/internal/operationallogs";func emitCIJournalFinal(*operationallogs.Recorder,error){}`, false},
		{"default snapshots forbidden", `package main;import "github.com/yunpiao/adtr/internal/operationallogs";func emitCIJournalFinal(r *operationallogs.Recorder,err error){_=r.Stats()}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkJournalFiles(t, map[string]string{"db_journal_default.go": tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
	_, ds := fixtureCheck(t, journalPackage, "recorder.go", `package operationallogs;type Recorder struct{};func(*Recorder)Stats()int{return 0};func(r *Recorder)run(){_=r.Stats()}`, fixtureImporter{})
	if len(ds) > 0 {
		t.Fatalf("existing recorder snapshot rejected: %v", ds)
	}
}

func TestJournalFailureClosedGetter(t *testing.T) {
	data, err := os.ReadFile("../operationallogs/append_diagnostic_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "append_diagnostic_ci.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := "package operationallogs;type mutex struct{};func(*mutex)Lock(){};func(*mutex)Unlock(){};type appendDiagnostic struct{stage,category,contextState,recorderContextState string};type recorderDiagnostic struct{first appendDiagnostic;set bool};type Recorder struct{mu mutex;diagnostic recorderDiagnostic};"
	foundType, foundGetter := false, false
	for _, decl := range file.Decls {
		if general, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range general.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok && typ.Name.Name == "CIJournalFailure" {
					original += "type " + sourceText(typ) + ";"
					foundType = true
				}
			}
		}
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "CIJournalFirstFailure" {
			original += sourceText(fn)
			foundGetter = true
		}
	}
	if !foundType || !foundGetter {
		t.Fatal("missing reviewed getter or shape")
	}
	for _, tc := range []struct {
		name, filename, source string
		bad                    bool
	}{
		{"reviewed getter", "append_diagnostic_ci.go", original, false},
		{"wrong file", "other.go", original, true},
		{"extra field", "append_diagnostic_ci.go", strings.Replace(strings.Replace(original, "CIJournalFailure struct {", "CIJournalFailure struct { Private string;", 1), `CIJournalFailure{"owned_ci_first_journal_failure"`, `CIJournalFailure{"private", "owned_ci_first_journal_failure"`, 1), true},
		{"renamed JSON field", "append_diagnostic_ci.go", strings.Replace(original, `json:"recorderContextState"`, `json:"rawError"`, 1), true},

		{"getter outside writer", "append_diagnostic_ci.go", original + `;func other(r *Recorder){_=r.CIJournalFirstFailure()}`, true},
		{"getter alias", "append_diagnostic_ci.go", original + `;func other(r *Recorder){f:=r.CIJournalFirstFailure;_=f()}`, true},
		{"payload outside getter", "append_diagnostic_ci.go", original + `;var other=CIJournalFailure{}`, true},
		{"invented stage", "append_diagnostic_ci.go", strings.Replace(original, `out.Stage = d.stage`, `out.Stage = "private"`, 1), true},
		{"unknown category accepted", "append_diagnostic_ci.go", strings.Replace(original, `"conflict", "other"`, `"conflict", "other", "private"`, 1), true},
		{"unlocked snapshot", "append_diagnostic_ci.go", strings.Replace(original, "r.mu.Lock()", "", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.filename == "append_diagnostic_ci.go" && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			_, diagnostics := fixtureCheck(t, journalPackage, tc.filename, tc.source, fixtureImporter{})
			if (len(diagnostics) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, diagnostics)
			}
		})
	}
}
