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

func realSignalRouteFragments(t *testing.T) (string, []string) {
	t.Helper()
	data, err := os.ReadFile("../../cmd/adtr/main.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", data, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("run missing")
	}
	var startup string
	sites := make([]string, 5)
	counts := make([]int, 5)
	stopCalls, rootCalls := 0, 0
	ast.Inspect(run.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				if id.Name == "stop" {
					stopCalls++
				}
				if id.Name == "cancelRoot" {
					rootCalls++
				}
			}
		}
		if branch, ok := n.(*ast.IfStmt); ok && signalStatementsMatch([]ast.Stmt{branch}, signalStartupBody) {
			if startup != "" {
				t.Fatal("duplicate startup")
			}
			startup = sourceText(branch)
		}
		if goStmt, ok := n.(*ast.GoStmt); ok {
			for i := 0; i < 3; i++ {
				if signalStatementsMatch([]ast.Stmt{goStmt}, signalBusinessBodies[i]) {
					counts[i]++
					sites[i] = sourceText(goStmt)
				}
			}
		}
		if block, ok := n.(*ast.BlockStmt); ok {
			for j := 1; j < len(block.List); j++ {
				for i := 3; i < 5; i++ {
					if signalStatementsMatch(block.List[j-1:j+1], signalBusinessBodies[i]) {
						counts[i]++
						sites[i] = sourceText(block.List[j-1]) + ";" + sourceText(block.List[j])
					}
				}
			}
		}
		return true
	})
	if startup == "" || stopCalls != 1 || rootCalls != 8 {
		t.Fatalf("actual routes startup=%v stop=%d root=%d", startup != "", stopCalls, rootCalls)
	}
	for i, count := range counts {
		if count != 1 {
			t.Fatalf("business route %d count %d", i, count)
		}
	}
	return startup, sites
}
func signalLifetimeFixtures(t *testing.T) (map[string]string, fixtureImporter) {
	t.Helper()
	_, imp := observerFixtures(t)
	for _, entry := range []struct{ path, source string }{
		{"fmt", `package fmt;func Errorf(string,...any)error{return nil}`},
		{module + "/internal/runtime", `package runtime;import "context";func Serve(context.Context,any)error{return nil}`},
		{module + "/internal/auth", `package auth;func NewTaskAuthorizer()any{return nil}`},
		{managed, `package dbadmission;import "context";func ObserveCIWorkerContext(context.Context){};func ConfigureCIShutdown(context.CancelFunc){}`},
	} {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	startup, sites := realSignalRouteFragments(t)
	data, err := os.ReadFile("../../cmd/adtr/signal_lifetime_ci.go")
	if err != nil {
		t.Fatal(err)
	}
	main := `package main;import("context";"os/signal";"fmt";app "github.com/yunpiao/adtr/internal/runtime";"github.com/yunpiao/adtr/internal/auth")
 type fakeWorker struct{};func(*fakeWorker)Run(context.Context)error{return nil};func(*fakeWorker)RunBulk(context.Context,any)error{return nil}
 func configureCIAdmission()(chan struct{},func(),error){return nil,func(){},nil}
 func run()error{
 admissionFailure,admissionCleanup,err:=configureCIAdmission();if err!=nil{return err}
 defer func(){admissionCleanup()}()
 ctx,stop:=signal.NotifyContext(nil);cancelRoot:=stop
 ` + startup + `
 var server any;messageStore:=&fakeWorker{};worker:=&fakeWorker{}
 messageDone:=make(chan error,1);` + sites[0] + `
 bulkDone:=make(chan error,1);` + sites[1] + `
 result:=make(chan error,1);` + sites[2] + `
 {` + sites[3] + `;_=serverErr}
 {` + sites[4] + `;_=serveErr}
 ;return nil}`
	return map[string]string{"main.go": main, "signal_lifetime_ci.go": string(data), "db_observation_ci.go": `package main;import("context";"github.com/yunpiao/adtr/internal/dbadmission");func observeCIWorkerContext(ctx context.Context,stop context.CancelFunc){dbadmission.ObserveCIWorkerContext(ctx);dbadmission.ConfigureCIShutdown(stop)}`}, imp
}
func checkSignalFiles(t *testing.T, sources map[string]string, imp fixtureImporter) []Diagnostic {
	t.Helper()
	fs := token.NewFileSet()
	var files []*ast.File
	for name, source := range sources {
		f, err := parser.ParseFile(fs, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	conf := types.Config{Importer: imp}
	if _, err := conf.Check(module+"/cmd/adtr", fs, files, info); err != nil {
		t.Fatal(err)
	}
	return Check(fs, module+"/cmd/adtr", files, info)
}
func TestSignalLifetimeRealFiveRoutesAndObjectBinding(t *testing.T) {
	original, imp := signalLifetimeFixtures(t)
	for _, tc := range []struct{ name, file, old, new string }{
		{"real startup and all five business routes", "main.go", "", ""},
		{"original stop observed", "main.go", "observeCIWorkerContext(ctx, logicalCancel)", "observeCIWorkerContext(ctx, stop)"},
		{"original signal context observed", "main.go", "ctx = logicalContext", "_ = logicalContext"},
		{"original stop retained", "main.go", "cancelRoot = logicalCancel", "cancelRoot = stop"},
		{"cleanup not wrapped", "main.go", "admissionCleanup = wrappedCleanup", "_ = wrappedCleanup"},
		{"eager cleanup capture", "main.go", "defer func(){admissionCleanup()}()", "defer admissionCleanup()"},
		{"wrong cleanup supplied", "main.go", "splitCISignalLifetime(ctx, stop, admissionCleanup)", "splitCISignalLifetime(ctx, stop, func(){})"},
		{"wrong parent supplied", "main.go", "splitCISignalLifetime(ctx, stop, admissionCleanup)", "splitCISignalLifetime(nil, stop, admissionCleanup)"},
		{"wrong unregister supplied", "main.go", "splitCISignalLifetime(ctx, stop, admissionCleanup)", "splitCISignalLifetime(ctx, func(){}, admissionCleanup)"},
		{"observe asynchronously", "main.go", "observeCIWorkerContext(ctx, logicalCancel)", "go observeCIWorkerContext(ctx, logicalCancel)"},
		{"observe deferred", "main.go", "observeCIWorkerContext(ctx, logicalCancel)", "defer observeCIWorkerContext(ctx, logicalCancel)"},
		{"observe alias", "main.go", "observeCIWorkerContext(ctx, logicalCancel)", "f:=observeCIWorkerContext;f(ctx,logicalCancel)"},
		{"logical cancel shadow", "main.go", "observeCIWorkerContext(ctx, logicalCancel)", "{logicalCancel:=stop;observeCIWorkerContext(ctx,logicalCancel)}"},
		{"split alias", "main.go", "logicalContext, logicalCancel, wrappedCleanup := splitCISignalLifetime", "split:=splitCISignalLifetime;logicalContext, logicalCancel, wrappedCleanup := split"},
		{"early extra unregister", "main.go", ";return nil}", ";stop();return nil}"},
		{"extra root cancellation", "main.go", ";return nil}", ";cancelRoot();return nil}"},
		{"callback escape", "main.go", ";return nil}", ";_=cancelRoot;return nil}"},
		{"callback replacement", "main.go", ";return nil}", ";cancelRoot=stop;return nil}"},
		{"callback address", "main.go", ";return nil}", ";_= &cancelRoot;return nil}"},
		{"context address", "main.go", ";return nil}", ";_= &ctx;return nil}"},
		{"extra cleanup", "main.go", ";return nil}", ";admissionCleanup();return nil}"},
		{"helper no child", "signal_lifetime_ci.go", "context.WithCancel(signalContext)", "signalContext,signalStop"},
		{"helper unregister early", "signal_lifetime_ci.go", "defer signalStop()", "signalStop()"},
		{"helper async cleanup", "signal_lifetime_ci.go", "cleanup()", "go cleanup()"},
		{"helper cleanup omitted", "signal_lifetime_ci.go", "cleanup()", "_ = cleanup"},
		{"helper returns unregister", "signal_lifetime_ci.go", "return logicalContext, logicalCancel, func()", "_ = logicalCancel;return logicalContext, signalStop, func()"},
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
			ds := checkSignalFiles(t, files, imp)
			if (len(ds) > 0) != (tc.old != "") {
				t.Fatalf("diagnostics=%v", ds)
			}
		})
	}
	_, sites := realSignalRouteFragments(t)
	for i, site := range sites {
		t.Run("business route cannot unregister "+string(rune('0'+i)), func(t *testing.T) {
			files := map[string]string{}
			for k, v := range original {
				files[k] = v
			}
			bad := strings.Replace(site, "cancelRoot()", "stop()", 1)
			files["main.go"] = strings.Replace(files["main.go"], site, bad, 1)
			if files["main.go"] == original["main.go"] {
				t.Fatal("unchanged real route mutation")
			}
			if ds := checkSignalFiles(t, files, imp); len(ds) == 0 {
				t.Fatal("original stop route accepted")
			}
		})
	}
}
func TestSignalLifetimeDefaultAndHelperBodies(t *testing.T) {
	files, imp := signalLifetimeFixtures(t)
	for file, body := range map[string]string{"signal_lifetime_ci.go": signalSplitBody, "signal_lifetime_default.go": signalDefaultBody} {
		data, err := os.ReadFile("../../cmd/adtr/" + file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, data, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "splitCISignalLifetime" {
				found = true
				if !captureBodyMatches(fn, body) {
					t.Fatal("helper body drift")
				}
			}
		}
		if !found {
			t.Fatal("helper absent")
		}
		if file == "signal_lifetime_default.go" {
			delete(files, "signal_lifetime_ci.go")
			files[file] = string(data)
		}
	}
	if ds := checkSignalFiles(t, files, imp); len(ds) != 0 {
		t.Fatalf("default diagnostics=%v", ds)
	}
	files["signal_lifetime_default.go"] = strings.Replace(files["signal_lifetime_default.go"], "return signalContext, signalStop, cleanup", "signalStop();return signalContext, signalStop, cleanup", 1)
	if ds := checkSignalFiles(t, files, imp); len(ds) == 0 {
		t.Fatal("default side effect accepted")
	}
}
