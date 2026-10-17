package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const uncleanProjectionBody = `{
 g.mu.Lock()
 defer g.mu.Unlock()
 if g.firstInvalidReason != invalidUncleanClose || g.firstUncleanClose == nil { return nil }
 d := *g.firstUncleanClose
 if d.Event != "owned_ci_first_unclean_close" || (d.Role != "epoch_target" && d.Role != "other") { return nil }
 return &d
}`
const uncleanWriterBody = `{
 d := g.uncleanCloseDiagnostic()
 if d == nil { return }
 data, err := json.Marshal(d)
 if err == nil { _, _ = os.Stderr.Write(append(data, '\n')) }
}`
const uncleanCaptureBody = `{
 first := g.firstInvalidReason == ""
 g.invalidateBecauseLocked(invalidUncleanClose)
 if first {
 role := "other"
 if g.seal != nil && g.seal.target == t { role = "epoch_target" }
 g.firstUncleanClose = &uncleanCloseDiagnostic{"owned_ci_first_unclean_close", observation.alreadyClosed, observation.closeFailed, observation.cleanupDone, role}
 }
}`

func uncleanCapture(path, file, name string, ancestors []ast.Node) bool {
	if path != managed || file != "lifecycle_ci.go" || name != "lifecycleTicket.closedObserved" {
		return false
	}
	for _, node := range ancestors {
		if branch, ok := node.(*ast.IfStmt); ok && compactSource(branch.Cond) == "!clean" && captureBodyMatches(&ast.FuncDecl{Body: branch.Body}, uncleanCaptureBody) {
			return true
		}
	}
	return false
}
func uncleanShape(typ types.Type) bool {
	if typ == nil {
		return false
	}
	s, ok := typ.Underlying().(*types.Struct)
	if !ok || s.NumFields() != 5 {
		return false
	}
	names := []string{"Event", "AlreadyClosed", "CloseFailed", "CleanupDone", "Role"}
	keys := []string{"event", "alreadyClosed", "closeFailed", "cleanupDone", "role"}
	for i, name := range names {
		kind := types.Bool
		if i == 0 || i == 4 {
			kind = types.String
		}
		if s.Field(i).Name() != name || !types.Identical(s.Field(i).Type(), types.Typ[kind]) || s.Tag(i) != `json:"`+keys[i]+`"` {
			return false
		}
	}
	return true
}
func uncleanCloseDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			projection := path == managed && filename == "application_diagnostic_ci.go" && name == "lifecycleGate.uncleanCloseDiagnostic" && captureBodyMatches(fn, uncleanProjectionBody)
			if path == managed && name == "lifecycleGate.uncleanCloseDiagnostic" && !projection {
				emit(decl, "unclean-close projection must return only the locked first-cause closed record")
			}
			if path == managed && name == "lifecycleGate.writeUncleanCloseDiagnostic" && !(filename == "application_diagnostic_ci.go" && captureBodyMatches(fn, uncleanWriterBody)) {
				emit(decl, "unclean-close writer must only serialize its nonnil closed projection")
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
					if path == managed && x.Name.Name == "uncleanCloseDiagnostic" && !(filename == "application_diagnostic_ci.go" && uncleanShape(info.TypeOf(x.Type))) {
						emit(x, "unclean-close diagnostic must have exactly five reviewed JSON fields")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), managed, "uncleanCloseDiagnostic") && !uncleanCapture(path, filename, name, ancestors) {
						emit(x, "unclean-close record construction must follow the first sticky invalidation")
					}
					if isNamed(info.TypeOf(x), managed, "closeObservation") && !(path == managed && filename == "conn.go" && name == "Conn.Close" && compactSource(x) == "closeObservation{alreadyClosed,err!=nil,cleanupDone}") {
						emit(x, "close observation must contain only the existing three close facts")
					}
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection != nil && selection.Kind() == types.FieldVal && isNamed(selection.Recv(), managed, "lifecycleGate") && selection.Obj().Name() == "firstUncleanClose" && !projection && !uncleanCapture(path, filename, name, ancestors) {
						emit(x, "first unclean-close record outside its exact capture or projection")
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed || obj.Name() != "closedObserved" {
						break
					}
					signature := callableSignature(obj.Type())
					if signature == nil || signature.Recv() == nil {
						break
					}
					if (isNamed(signature.Recv().Type(), managed, "lifecycleTicket") || isNamed(signature.Recv().Type(), managed, "lifecyclePermit")) && !(path == managed && filename == "conn.go" && name == "Conn.Close" && directCallFor(x, ancestors) != nil) {
						emit(x, "observed native close outside the managed Close entrypoint")
					}
				}
				return true
			})
		}
	}
	return out
}
