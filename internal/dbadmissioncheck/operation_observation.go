package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

const observationSource = "operation_observation_ci.go"

// Exact reviewed observer bodies: the native/configuration exceptions below
// apply only while these no-mutation and closed-output bodies remain intact.
var observationBodies = map[string]string{
	"observeLifecycleConfig": `{
	ticket, ok := permit.(*lifecycleTicket)
	if !ok || config == nil || config.Tracer != nil {
		return config
	}
	copy := config.Copy()
	copy.Tracer = &operationObserver{ticket: ticket}
	return copy
}`,
	"ObserveCIWorkerContext": `{
	coordinator.Lock()
	defer coordinator.Unlock()
	g := coordinator.gate
	if g == nil || !g.application || ctx == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.workerContext == nil {
		g.workerContext = ctx
	}
}`,
	"diagnosticContextState": `{
	if ctx == nil {
		return "unknown"
	}
	switch ctx.Err() {
	case nil:
		return "active"
	case context.Canceled:
		return "canceled"
	case context.DeadlineExceeded:
		return "deadline_exceeded"
	default:
		return "other"
	}
}`,
	"diagnosticErrorCategory": `{
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "other"
	}
}`,
	"operationObserver.TraceQueryStart": `{
	operation := "statement"
	switch data.SQL {
	case "begin":
		operation = "begin"
	case "commit":
		operation = "commit"
	case "rollback":
		operation = "rollback"
	}
	g := o.ticket.gate
	g.mu.Lock()
	phase := g.diagnosticPhaseLocked()
	g.mu.Unlock()
	// Retain only the original context and a closed category, never SQL/arguments.
	return context.WithValue(ctx, operationTraceKey{}, operationTrace{o, ctx, operation, phase})
}`,
	"operationObserver.TraceQueryEnd": `{
	if data.Err == nil {
		return
	}
	// Classify the returned error independently before looking at cancellation.
	category := diagnosticErrorCategory(data.Err)
	trace, ok := ctx.Value(operationTraceKey{}).(operationTrace)
	if !ok || trace.observer != o {
		return
	}
	state := diagnosticContextState(trace.ctx)
	matches := (state == "canceled" && category == "canceled") || (state == "deadline_exceeded" && category == "deadline_exceeded")
	nativeClosed := conn != nil && conn.IsClosed()
	g := o.ticket.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	ticket := o.ticket
	matched := ticket.conn != nil && ticket.conn.raw == conn && ticket.state == 'o'
	d := &operationErrorDiagnostic{trace.operation, trace.startPhase, g.diagnosticPhaseLocked(), state, category, matches, nativeClosed, diagnosticContextState(g.workerContext), matched}
	if ticket.firstOperationError == nil {
		ticket.firstOperationError = d
	}
	if nativeClosed && ticket.firstNativeClosedError == nil {
		ticket.firstNativeClosedError = d
	}
}`,
	"validOperationPhase": `{
	switch phase {
	case "open", "draining", "sealed", "released", "unknown":
		return true
	}
	return false
}`,
	"validOperationContext": `{
	switch state {
	case "active", "canceled", "deadline_exceeded", "other", "unknown":
		return true
	}
	return false
}`,
	"validOperationError": `{
	if d == nil {
		return true
	}
	if !validOperationPhase(d.StartPhase) || !validOperationPhase(d.EndPhase) || !validOperationContext(d.ContextState) || !validOperationContext(d.RootContextState) {
		return false
	}
	switch d.Operation {
	case "begin", "commit", "rollback", "statement":
	default:
		return false
	}
	switch d.ErrorCategory {
	case "none", "canceled", "deadline_exceeded", "other":
	default:
		return false
	}
	return true
}`,
	"lifecycleGate.uncleanOperationDiagnostic": `{
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.firstInvalidReason != invalidUncleanClose || g.firstUncleanOperation == nil {
		return nil
	}
	d := *g.firstUncleanOperation
	if d.Event != "owned_ci_unclean_operation" || !validOperationPhase(d.BirthPhase) || !validOperationContext(d.RootAtClose) || !validOperationError(d.FirstError) || !validOperationError(d.FirstNativeClosedError) {
		return nil
	}
	return &d
}`,
	"lifecycleGate.writeUncleanOperationDiagnostic": `{
	d := g.uncleanOperationDiagnostic()
	if d == nil {
		return
	}
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}`,
}
var observationTypes = map[string]string{
	"operationObserver":          "struct{ ticket *lifecycleTicket }",
	"operationTraceKey":          "struct{}",
	"operationTrace":             "struct {\n\tobserver              *operationObserver\n\tctx                   context.Context\n\toperation, startPhase string\n}",
	"operationErrorDiagnostic":   "struct {\n\tOperation           string `json:\"operation\"`\n\tStartPhase          string `json:\"startPhase\"`\n\tEndPhase            string `json:\"endPhase\"`\n\tContextState        string `json:\"contextState\"`\n\tErrorCategory       string `json:\"errorCategory\"`\n\tErrorMatchesContext bool   `json:\"errorMatchesContext\"`\n\tNativeClosedAtEnd   bool   `json:\"nativeClosedAtEnd\"`\n\tRootContextState    string `json:\"rootContextState\"`\n\tConnectionMatched   bool   `json:\"connectionMatched\"`\n}",
	"uncleanOperationDiagnostic": "struct {\n\tEvent                  string                    `json:\"event\"`\n\tBirthPhase             string                    `json:\"birthPhase\"`\n\tBirthCaptureEnded      bool                      `json:\"birthCaptureEnded\"`\n\tFirstError             *operationErrorDiagnostic `json:\"firstError\"`\n\tFirstNativeClosedError *operationErrorDiagnostic `json:\"firstNativeClosedError\"`\n\tRootAtClose            string                    `json:\"rootAtClose\"`\n}",
}

func exactObserverFunction(path, file, name string, fn *ast.FuncDecl) bool {
	body, ok := observationBodies[name]
	return ok && path == managed && file == observationSource && captureBodyMatches(fn, body)
}
func observedTracerField(path, file, name string, fn *ast.FuncDecl, sel *ast.SelectorExpr) bool {
	return name == "observeLifecycleConfig" && exactObserverFunction(path, file, name, fn) && (sourceText(sel) == "config.Tracer" || sourceText(sel) == "copy.Tracer")
}
func observedBirthCapture(path, file, name string, sel *ast.SelectorExpr, ancestors []ast.Node, info *types.Info) bool {
	if path != managed || file != "lifecycle_ci.go" || name != "lifecycleGate.acquire" || sourceText(sel) != "g.seal.captureEnded" {
		return false
	}
	for i, node := range ancestors {
		if kv, ok := node.(*ast.KeyValueExpr); ok && i > 0 && sourceText(kv.Key) == "birthCaptureEnded" && compactSource(kv.Value) == "g.seal!=nil&&g.seal.captureEnded" {
			if literal, ok := ancestors[i-1].(*ast.CompositeLit); ok && isNamed(info.TypeOf(literal), managed, "lifecycleTicket") {
				return true
			}
		}
	}
	return false
}
func operationObservationDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	emit := func(n ast.Node, s string) { out = append(out, Diagnostic{fset.Position(n.Pos()), s}) }
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
			_, expected := observationBodies[name]
			if path == managed && expected && !exactObserverFunction(path, filename, name, fn) && !(name == "observeLifecycleConfig" && filename == "operation_observation_default.go" && captureBodyMatches(fn, `{return config}`)) {
				emit(decl, "operation observer must retain its exact context/configuration and closed-diagnostic behavior")
			}
			if path == module+"/cmd/adtr" && name == "observeCIWorkerContext" && !((filename == "db_observation_ci.go" && captureBodyMatches(fn, `{dbadmission.ObserveCIWorkerContext(ctx)}`)) || (filename == "db_observation_default.go" && captureBodyMatches(fn, `{}`))) {
				emit(decl, "worker context bridge must only forward the existing context when tagged")
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
					if expected, ok := observationTypes[x.Name.Name]; ok && path == managed {
						parsed, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package expected;type expected "+expected, 0)
						if filename != observationSource || err != nil || compactSource(x.Type) != compactSource(parsed.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type) {
							emit(x, "observer records must retain their exact closed fields and JSON schema")
						}
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil {
						break
					}
					pkg := obj.Pkg().Path()
					if pkg == managed && obj.Name() == "observeLifecycleConfig" && !(path == managed && filename == "conn.go" && name == "connectWith" && directCallFor(x, ancestors) != nil) {
						emit(x, "observer configuration outside the sole managed connect entrypoint")
					}
					if pkg == managed && obj.Name() == "ObserveCIWorkerContext" && !(path == module+"/cmd/adtr" && filename == "db_observation_ci.go" && name == "observeCIWorkerContext" && directCallFor(x, ancestors) != nil) {
						emit(x, "root context observation outside the exact tagged bridge")
					}
					if pkg == module+"/cmd/adtr" && obj.Name() == "observeCIWorkerContext" {
						allowed := path == pkg && filename == "main.go" && name == "run" && directCallFor(x, ancestors) != nil
						installed := false
						for _, node := range ancestors {
							if branch, ok := node.(*ast.IfStmt); ok && compactSource(branch.Cond) == "admissionFailure!=nil" {
								installed = true
							}
						}
						if !allowed || !installed {
							emit(x, "worker context observation must remain in the installed worker startup branch")
						}
					}
				}
				return true
			})
		}
	}
	return out
}
