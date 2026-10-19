package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const signalSplitBody = `{
	logicalContext, logicalCancel := context.WithCancel(signalContext)
	return logicalContext, logicalCancel, func() {
		defer signalStop()
		cleanup()
	}
}`
const signalDefaultBody = `{
	return signalContext, signalStop, cleanup
}`
const signalStartupBody = `{	if admissionFailure == nil {
		defer stop()
	} else {
		logicalContext, logicalCancel, wrappedCleanup := splitCISignalLifetime(ctx, stop, admissionCleanup)
		ctx = logicalContext
		cancelRoot = logicalCancel
		admissionCleanup = wrappedCleanup
		observeCIWorkerContext(ctx, logicalCancel)
		// A startup transport failure must be handled synchronously before any DB constructor.
		select {
		case <-admissionFailure:
			cancelRoot()
			return fmt.Errorf("CI database controller unavailable")
		default:
		}
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select {
			case <-admissionFailure:
				cancelRoot()
			case <-ctx.Done():
			}
		}()
		defer func() { cancelRoot(); <-watchDone }()
	}}`
const signalLateCleanup = `{defer func(){admissionCleanup()}()}`

var signalBusinessBodies = []string{
	`{go func(){messageDone<-messageStore.Run(ctx);cancelRoot()}()}`,
	`{go func(){bulkDone<-messageStore.RunBulk(ctx,auth.NewTaskAuthorizer());cancelRoot()}()}`,
	`{go func(){result<-worker.Run(ctx);cancelRoot()}()}`,
	`{serverErr:=app.Serve(ctx,server);cancelRoot()}`,
	`{serveErr:=app.Serve(ctx,server);cancelRoot()}`,
}

func signalStatementsMatch(statements []ast.Stmt, body string) bool {
	return captureBodyMatches(&ast.FuncDecl{Body: &ast.BlockStmt{List: statements}}, body)
}
func signalObject(expr ast.Expr, info *types.Info) types.Object {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return nil
	}
	if obj := info.Defs[id]; obj != nil {
		return obj
	}
	return info.Uses[id]
}
func signalWithin(n, container ast.Node) bool {
	return container != nil && n.Pos() >= container.Pos() && n.End() <= container.End()
}
func signalBusinessSites(fn *ast.FuncDecl, ctx types.Object, info *types.Info) ([]ast.Node, bool) {
	var sites []ast.Node
	counts := make([]int, len(signalBusinessBodies))
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if g, ok := n.(*ast.GoStmt); ok {
			for i := 0; i < 3; i++ {
				if signalStatementsMatch([]ast.Stmt{g}, signalBusinessBodies[i]) {
					valid := true
					ast.Inspect(g, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && id.Name == "ctx" && info.Uses[id] != ctx {
							valid = false
						}
						return true
					})
					if valid {
						counts[i]++
						sites = append(sites, g)
					}
				}
			}
		}
		if block, ok := n.(*ast.BlockStmt); ok {
			for j := 1; j < len(block.List); j++ {
				for i := 3; i < 5; i++ {
					if signalStatementsMatch(block.List[j-1:j+1], signalBusinessBodies[i]) {
						assignment := block.List[j-1].(*ast.AssignStmt)
						call := assignment.Rhs[0].(*ast.CallExpr)
						if objectCall(call, info, module+"/internal/runtime", "Serve") && signalObject(call.Args[0], info) == ctx {
							counts[i]++
							sites = append(sites, block.List[j])
						}
					}
				}
			}
		}
		return true
	})
	for _, count := range counts {
		if count != 1 {
			return nil, false
		}
	}
	return sites, true
}

// This is a fixed-tree ownership check, not general alias or closure analysis.
func signalLifetimeBinding(fn *ast.FuncDecl, info *types.Info) (*ast.CallExpr, *ast.CallExpr, bool) {
	if fn == nil || fn.Body == nil {
		return nil, nil, false
	}
	var configure, notify, root *ast.AssignStmt
	var startup *ast.IfStmt
	var cleanup *ast.DeferStmt
	for _, stmt := range fn.Body.List {
		switch x := stmt.(type) {
		case *ast.AssignStmt:
			if signalStatementsMatch([]ast.Stmt{x}, `{cancelRoot:=stop}`) {
				if root != nil {
					return nil, nil, false
				}
				root = x
			}
			if len(x.Rhs) != 1 {
				continue
			}
			call, ok := x.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			if len(x.Lhs) == 3 && objectCall(call, info, module+"/cmd/adtr", "configureCIAdmission") {
				if configure != nil {
					return nil, nil, false
				}
				configure = x
			}
			if len(x.Lhs) == 2 && objectCall(call, info, "os/signal", "NotifyContext") {
				if notify != nil {
					return nil, nil, false
				}
				notify = x
			}
		case *ast.IfStmt:
			if signalStatementsMatch([]ast.Stmt{x}, signalStartupBody) {
				if startup != nil {
					return nil, nil, false
				}
				startup = x
			}
		case *ast.DeferStmt:
			if signalStatementsMatch([]ast.Stmt{x}, signalLateCleanup) {
				if cleanup != nil {
					return nil, nil, false
				}
				cleanup = x
			}
		}
	}
	if configure == nil || notify == nil || root == nil || startup == nil || cleanup == nil || !(configure.End() < cleanup.Pos() && cleanup.End() < notify.Pos() && notify.End() < root.Pos() && root.End() < startup.Pos()) {
		return nil, nil, false
	}
	failure, cleanupObj := signalObject(configure.Lhs[0], info), signalObject(configure.Lhs[1], info)
	ctx, stop, cancel := signalObject(notify.Lhs[0], info), signalObject(notify.Lhs[1], info), signalObject(root.Lhs[0], info)
	if failure == nil || cleanupObj == nil || ctx == nil || stop == nil || cancel == nil || !isNamed(stop.Type(), "context", "CancelFunc") || !isNamed(cancel.Type(), "context", "CancelFunc") || signalObject(root.Rhs[0], info) != stop {
		return nil, nil, false
	}
	installed := startup.Else.(*ast.BlockStmt)
	splitAssignment := installed.List[0].(*ast.AssignStmt)
	split := splitAssignment.Rhs[0].(*ast.CallExpr)
	child, logical, wrapped := signalObject(splitAssignment.Lhs[0], info), signalObject(splitAssignment.Lhs[1], info), signalObject(splitAssignment.Lhs[2], info)
	if !objectCall(split, info, module+"/cmd/adtr", "splitCISignalLifetime") || child == nil || logical == nil || wrapped == nil || !isNamed(logical.Type(), "context", "CancelFunc") || signalObject(split.Args[0], info) != ctx || signalObject(split.Args[1], info) != stop || signalObject(split.Args[2], info) != cleanupObj {
		return nil, nil, false
	}
	assignments := []*ast.AssignStmt{installed.List[1].(*ast.AssignStmt), installed.List[2].(*ast.AssignStmt), installed.List[3].(*ast.AssignStmt)}
	for i, pair := range [][2]types.Object{{ctx, child}, {cancel, logical}, {cleanupObj, wrapped}} {
		a := assignments[i]
		if signalObject(a.Lhs[0], info) != pair[0] || signalObject(a.Rhs[0], info) != pair[1] {
			return nil, nil, false
		}
	}
	observe := installed.List[4].(*ast.ExprStmt).X.(*ast.CallExpr)
	if !objectCall(observe, info, module+"/cmd/adtr", "observeCIWorkerContext") || signalObject(observe.Args[0], info) != ctx || signalObject(observe.Args[1], info) != logical {
		return nil, nil, false
	}
	business, ok := signalBusinessSites(fn, ctx, info)
	if !ok {
		return nil, nil, false
	}
	names := map[string]types.Object{"admissionFailure": failure, "admissionCleanup": cleanupObj, "ctx": ctx, "stop": stop, "cancelRoot": cancel, "logicalContext": child, "logicalCancel": logical, "wrappedCleanup": wrapped}
	valid := true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if !valid {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			obj := info.Uses[x]
			if obj == nil {
				return true
			}
			if obj == stop && !signalWithin(x, root) && !signalWithin(x, startup.Body) && !signalWithin(x, split) {
				valid = false
			}
			if obj == cleanupObj && !signalWithin(x, cleanup) && !signalWithin(x, split) && !signalWithin(x, assignments[2]) {
				valid = false
			}
			if obj == cancel && !signalWithin(x, installed) {
				allowed := false
				for _, site := range business {
					if signalWithin(x, site) {
						allowed = true
					}
				}
				if !allowed {
					valid = false
				}
			}
			if (obj == child || obj == logical || obj == wrapped) && !signalWithin(x, installed) {
				valid = false
			}
			if signalWithin(x, startup) {
				if expected, ok := names[x.Name]; ok && obj != expected {
					valid = false
				}
			}
		case *ast.AssignStmt:
			if x == configure || x == notify || x == root || x == splitAssignment || x == assignments[0] || x == assignments[1] || x == assignments[2] {
				return true
			}
			for _, lhs := range x.Lhs {
				obj := signalObject(lhs, info)
				for _, protected := range names {
					if obj == protected {
						valid = false
					}
				}
			}
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				obj := signalObject(x.X, info)
				for _, protected := range names {
					if obj == protected {
						valid = false
					}
				}
			}
		}
		return true
	})
	return split, observe, valid
}
func exactSignalLifetimeHelper(file string, fn *ast.FuncDecl, info *types.Info) bool {
	body := ""
	if file == "signal_lifetime_ci.go" {
		body = signalSplitBody
	}
	if file == "signal_lifetime_default.go" {
		body = signalDefaultBody
	}
	if body == "" || !captureBodyMatches(fn, body) {
		return false
	}
	obj, ok := info.Defs[fn.Name].(*types.Func)
	if !ok {
		return false
	}
	sig := callableSignature(obj.Type())
	if sig == nil || sig.Recv() != nil || sig.Params().Len() != 3 || sig.Results().Len() != 3 || !isNamed(sig.Params().At(0).Type(), "context", "Context") || !isNamed(sig.Params().At(1).Type(), "context", "CancelFunc") || !isNamed(sig.Results().At(0).Type(), "context", "Context") || !isNamed(sig.Results().At(1).Type(), "context", "CancelFunc") {
		return false
	}
	for _, typ := range []types.Type{sig.Params().At(2).Type(), sig.Results().At(2).Type()} {
		f := callableSignature(typ)
		if f == nil || f.Params().Len() != 0 || f.Results().Len() != 0 {
			return false
		}
	}
	if file == "signal_lifetime_ci.go" {
		assignment := fn.Body.List[0].(*ast.AssignStmt)
		call := assignment.Rhs[0].(*ast.CallExpr)
		return objectCall(call, info, "context", "WithCancel") && signalObject(call.Args[0], info) == sig.Params().At(0)
	}
	return true
}
func signalLifetimeDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	if path != module+"/cmd/adtr" {
		return out
	}
	emit := func(n ast.Node, why string) { out = append(out, Diagnostic{fset.Position(n.Pos()), why}) }
	for _, file := range files {
		filename := filepath.Base(fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Name.Name == "splitCISignalLifetime" && !exactSignalLifetimeHelper(filename, fn, info) {
				emit(fn, "signal lifetime helper must retain typed child cancellation and cleanup-before-unregister")
			}

			if filename == "main.go" && fn.Name.Name == "run" {
				hasController := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok && objectCall(call, info, path, "configureCIAdmission") {
						hasController = true
					}
					return true
				})
				if hasController {
					if _, _, valid := signalLifetimeBinding(fn, info); !valid {
						emit(fn, "signal cancellation and cleanup must retain their exact startup and five business routes")
					}
				}
			}
			var ancestors []ast.Node
			ast.Inspect(fn, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				if id, ok := n.(*ast.Ident); ok {
					if obj, ok := info.Uses[id].(*types.Func); ok && obj.Pkg() != nil && obj.Pkg().Path() == path && obj.Name() == "splitCISignalLifetime" {
						split, _, valid := signalLifetimeBinding(fn, info)
						if filename != "main.go" || fn.Name.Name != "run" || !valid || directCallFor(id, ancestors) != split {
							emit(id, "signal lifetime split outside exact installed startup")
						}
					}
				}
				return true
			})
		}
	}
	return out
}
