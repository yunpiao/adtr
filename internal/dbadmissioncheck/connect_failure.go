package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

const connectFailureSource = "connect_failure_ci.go"

var connectFailureBodies = map[string]struct{ file, body string }{
	"observeLifecycleConnectFailure": {"connect_failure_ci.go", `{
	if t, ok := permit.(*lifecycleTicket); ok {
		t.failedObserved(ctx, err, rawNil)
	} else if permit != nil {
		permit.failed()
	}
}`},
	"diagnosticConnectErrorCategory": {"connect_failure_ci.go", `{
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError != nil && len(pgError.Code) == 5 && pgError.Code[:2] == "28" {
		return "authentication"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return "network_error"
	}
	return "other"
}`},
	"lifecycleTicket.failedObserved": {"connect_failure_ci.go", `{
	// Classify the returned error before sampling cancellation. A later cancel
	// must not relabel an unrelated network/authentication error as cancellation.
	category := diagnosticConnectErrorCategory(err)
	contextState := diagnosticContextState(ctx)
	matches := (contextState == "canceled" && errors.Is(err, context.Canceled)) || (contextState == "deadline_exceeded" && errors.Is(err, context.DeadlineExceeded))
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	first := g.firstInvalidReason == ""
	g.invalidateBecauseLocked(invalidConnectFailed)
	if first {
		g.firstConnectFailure = &connectFailureDiagnostic{
			"owned_ci_first_connect_failure", t.birthPhase, t.birthCaptureEnded, t.recoveryAuthorized,
			g.firstInvalidPhase, contextState, category, matches, diagnosticContextState(g.workerContext), rawNil,
		}
	}
	if t.state == 'p' {
		g.pending--
	}
	t.state = 'f'
	g.notifyLocked()
}`},
	"lifecycleGate.connectFailureDiagnostic": {"connect_failure_ci.go", `{
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.firstInvalidReason != invalidConnectFailed || g.firstConnectFailure == nil {
		return nil
	}
	d := *g.firstConnectFailure
	if d.Event != "owned_ci_first_connect_failure" || !validOperationPhase(d.BirthPhase) || !validOperationPhase(d.FailurePhase) || !validOperationContext(d.ContextState) || !validOperationContext(d.RootContextState) {
		return nil
	}
	switch d.ErrorCategory {
	case "none", "canceled", "deadline_exceeded", "authentication", "network_error", "other":
	default:
		return nil
	}
	return &d
}`},
	"lifecycleGate.writeConnectFailureDiagnostic": {"connect_failure_ci.go", `{
	d := g.connectFailureDiagnostic()
	if d == nil {
		return
	}
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}`},
	"connectWith": {"conn.go", `{
	permit, err := acquireLifecycle(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := connect(ctx, observeLifecycleConfig(config, permit))
	if err != nil || raw == nil {
		if permit != nil {
			// A failed startup may leave asynchronous cleanup. Never claim drain.
			observeLifecycleConnectFailure(ctx, permit, err, raw == nil)
		}
		if err == nil {
			err = errors.New("database connection unavailable")
		}
		return nil, err
	}
	conn := &Conn{raw: raw, permit: permit}
	if permit != nil {
		permit.connected(conn)
	}
	return conn, nil
}`},
}

const connectFailureType = "struct {\n\tEvent                   string `json:\"event\"`\n\tBirthPhase              string `json:\"birthPhase\"`\n\tBirthCaptureEnded       bool   `json:\"birthCaptureEnded\"`\n\tBirthRecoveryAuthorized bool   `json:\"birthRecoveryAuthorized\"`\n\tFailurePhase            string `json:\"failurePhase\"`\n\tContextState            string `json:\"contextState\"`\n\tErrorCategory           string `json:\"errorCategory\"`\n\tErrorMatchesContext     bool   `json:\"errorMatchesContext\"`\n\tRootContextState        string `json:\"rootContextState\"`\n\tRawNil                  bool   `json:\"rawNil\"`\n}"

func exactConnectFailureFunction(path, file, name string, fn *ast.FuncDecl) bool {
	expected, ok := connectFailureBodies[name]
	return ok && path == managed && file == expected.file && captureBodyMatches(fn, expected.body)
}
func connectFailureDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		filename := filepath.Base(fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			name := ""
			if fn != nil {
				name = fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					name = receiverName(fn.Recv.List[0].Type) + "." + name
				}
			}
			exact := exactConnectFailureFunction(path, filename, name, fn)
			if expected, ok := connectFailureBodies[name]; ok && path == managed && expected.file == connectFailureSource && !exact {
				if !(name == "observeLifecycleConnectFailure" && filename == "connect_failure_default.go" && captureBodyMatches(fn, `{if permit!=nil{permit.failed()}}`)) {
					emit(decl, "connect failure observation must retain its exact sticky-first closed diagnostic body")
				}
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				switch x := n.(type) {
				case *ast.TypeSpec:
					if path == managed && x.Name.Name == "connectFailureDiagnostic" {
						expected, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package expected;type expected "+connectFailureType, 0)
						if filename != connectFailureSource || err != nil || compactSource(x.Type) != compactSource(expected.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type) {
							emit(x, "connect failure diagnostic must retain exactly ten closed fields")
						}
					}
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection != nil && selection.Kind() == types.FieldVal && ((isNamed(selection.Recv(), managed, "lifecycleGate") && selection.Obj().Name() == "firstConnectFailure") || isNamed(selection.Recv(), managed, "connectFailureDiagnostic")) && !exact {
						emit(x, "connect failure facts outside their sole first capture or fixed projection")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), managed, "connectFailureDiagnostic") && !(exact && name == "lifecycleTicket.failedObserved") {
						emit(x, "connect failure facts may only be captured after first sticky invalidation")
					}
				case *ast.StarExpr:
					if info.Types[x].IsValue() && isNamed(info.TypeOf(x), managed, "connectFailureDiagnostic") && !(exact && name == "lifecycleGate.connectFailureDiagnostic") {
						emit(x, "connect failure records cannot be replaced or copied outside fixed projection")
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed {
						break
					}
					allowed := false
					protected := true
					switch obj.Name() {
					case "observeLifecycleConnectFailure":
						allowed = exact && name == "connectWith" && directCallFor(x, ancestors) != nil
					case "failedObserved":
						sig := callableSignature(obj.Type())
						if sig == nil || sig.Recv() == nil || !isNamed(sig.Recv().Type(), managed, "lifecycleTicket") {
							protected = false
							break
						}
						allowed = exact && name == "observeLifecycleConnectFailure" && directCallFor(x, ancestors) != nil
					case "diagnosticConnectErrorCategory":
						allowed = exact && name == "lifecycleTicket.failedObserved" && directCallFor(x, ancestors) != nil
					default:
						protected = false
					}
					if protected && !allowed {
						emit(x, "connect failure helper outside its exact synchronous failed-connect call chain")
					}
				}
				return true
			})
		}
	}
	return out
}
