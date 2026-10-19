package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func phaseFixtures(t *testing.T) fixtureImporter {
	t.Helper()
	_, imp := connectFailureFixtures(t)
	p, _ := fixtureCheck(t, pgx, "fixture.go", `package pgx;import "context";type Conn struct{};type ConnConfig struct{};func ConnectConfig(context.Context,*ConnConfig)(*Conn,error){return nil,nil}`, imp)
	imp[pgx] = p
	p, _ = fixtureCheck(t, managed, "fixture.go", `package dbadmission;import("context";"github.com/jackc/pgx/v5");type Conn struct{};func ConnectConfig(context.Context,*pgx.ConnConfig)(*Conn,error){return nil,nil};func ConnectConfigWithCIPhase(context.Context,*pgx.ConnConfig)(*Conn,error,string){return nil,nil,"none"}`, imp)
	imp[managed] = p
	return imp
}

func TestJournalConnectPhaseEntryIsNarrow(t *testing.T) {
	original, imp := connectFailureFixtures(t)
	p, _ := fixtureCheck(t, pgx, "fixture.go", `package pgx;import "context";type Conn struct{};type ConnConfig struct{};func ConnectConfig(context.Context,*ConnConfig)(*Conn,error){return nil,nil}`, imp)
	imp[pgx] = p
	data, err := os.ReadFile("../dbadmission/connect_phase_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	entry := string(data)
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"exact entry", "connect_phase_ci.go", entry, false},
		{"wrong file", "other.go", entry, true},
		{"native alias", "connect_phase_ci.go", strings.Replace(entry, "return connectWithPhase(ctx, config, pgx.ConnectConfig)", "connect := pgx.ConnectConfig; return connectWithPhase(ctx, config, connect)", 1), true},
		{"invented phase", "connect_phase_ci.go", strings.Replace(entry, "return connectWithPhase(ctx, config, pgx.ConnectConfig)", `conn,err,_:=connectWithPhase(ctx,config,pgx.ConnectConfig);return conn,err,"native"`, 1), true},
		{"entry alias", "connect_phase_ci.go", entry + `;var alias=ConnectConfigWithCIPhase`, true},
		{"other entry caller", "connect_phase_ci.go", entry + `;func other(ctx context.Context,c *pgx.ConnConfig){_,_,_=ConnectConfigWithCIPhase(ctx,c)}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.file == "connect_phase_ci.go" && tc.source == entry {
				t.Fatal("mutation unchanged")
			}
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			files["conn.go"] = `package dbadmission;import("context";"errors";"github.com/jackc/pgx/v5");type Conn struct{raw *pgx.Conn;permit lifecyclePermit};var acquireLifecycle func(context.Context)(lifecyclePermit,error);var observeLifecycleConfig func(*pgx.ConnConfig,lifecyclePermit)*pgx.ConnConfig;func connectWithPhase(ctx context.Context,config *pgx.ConnConfig,connect func(context.Context,*pgx.ConnConfig)(*pgx.Conn,error))(*Conn,error,string)` + connectFailureBodies["connectWithPhase"].body
			files[tc.file] = tc.source
			ds := checkCaptureFiles(t, files, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestJournalConnectPhaseHelpersAndCallers(t *testing.T) {
	imp := phaseFixtures(t)
	prefix := `package operationallogs;import("context";"github.com/jackc/pgx/v5";"github.com/yunpiao/adtr/internal/dbadmission");type appendDiagnostic struct{connectPhase string};type AppendError struct{diagnostic appendDiagnostic};func newStoreAppendError(context.Context,string,error,bool)*AppendError{return &AppendError{}};`
	ci := prefix + `func connectJournal(ctx context.Context,config *pgx.ConnConfig)(*dbadmission.Conn,error,string)` + journalCIConnectBody + `;func newStoreConnectError(ctx context.Context,err error,phase string)*AppendError` + journalCIConnectErrorBody
	normal := prefix + `func connectJournal(ctx context.Context,config *pgx.ConnConfig)(*dbadmission.Conn,error,string)` + journalDefaultConnectBody + `;func newStoreConnectError(ctx context.Context,err error,_ string)*AppendError` + journalDefaultConnectErrorBody
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"CI helpers", "append_diagnostic_ci.go", ci, false},
		{"default helpers", "append_diagnostic_default.go", normal, false},
		{"CI body in default", "append_diagnostic_default.go", ci, true},
		{"alias CI entry", "append_diagnostic_ci.go", strings.Replace(ci, "return dbadmission.ConnectConfigWithCIPhase(ctx, config)", "f:=dbadmission.ConnectConfigWithCIPhase;return f(ctx,config)", 1), true},
		{"other CI entry caller", "append_diagnostic_ci.go", ci + `;func other(ctx context.Context,c *pgx.ConnConfig){_,_,_=dbadmission.ConnectConfigWithCIPhase(ctx,c)}`, true},
		{"helper alias", "append_diagnostic_ci.go", ci + `;var other=connectJournal`, true},
		{"error helper alias", "append_diagnostic_ci.go", ci + `;var other=newStoreConnectError`, true},
		{"phase relabelled", "append_diagnostic_ci.go", strings.Replace(ci, "out.diagnostic.connectPhase = phase", `out.diagnostic.connectPhase = "native"`, 1), true},
		{"wrong error context", "append_diagnostic_ci.go", strings.Replace(ci, `newStoreAppendError(ctx, "connect", err, false)`, `newStoreAppendError(nil, "connect", err, false)`, 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.file == "append_diagnostic_ci.go" && tc.source == ci {
				t.Fatal("mutation unchanged")
			}
			_, ds := fixtureCheck(t, journalPackage, tc.file, tc.source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestConnectPhaseBodiesMatchProductionWithComments(t *testing.T) {
	for _, expected := range []struct{ file, name, body string }{
		{"../dbadmission/conn.go", "connectWith", connectFailureBodies["connectWith"].body},
		{"../dbadmission/conn.go", "connectWithPhase", connectFailureBodies["connectWithPhase"].body},
		{"../dbadmission/connect_phase_ci.go", "ConnectConfigWithCIPhase", ciPhaseEntryBody},
		{"../operationallogs/append_diagnostic_ci.go", "connectJournal", journalCIConnectBody},
		{"../operationallogs/append_diagnostic_default.go", "connectJournal", journalDefaultConnectBody},
		{"../operationallogs/append_diagnostic_ci.go", "newStoreConnectError", journalCIConnectErrorBody},
		{"../operationallogs/append_diagnostic_default.go", "newStoreConnectError", journalDefaultConnectErrorBody},
	} {
		t.Run(expected.file+"/"+expected.name, func(t *testing.T) {
			data, err := os.ReadFile(expected.file)
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
				if ok && fn.Recv == nil && fn.Name.Name == expected.name {
					found = true
					if !captureBodyMatches(fn, expected.body) {
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
