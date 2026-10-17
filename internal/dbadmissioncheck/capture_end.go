package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

const captureEndSource = "capture_end_ci_linux_amd64.go"
const captureEndPrepareBody = `{
 info, err := file.Stat()
 if err != nil || info.Mode()&os.ModeNamedPipe == 0 { return 0, errSealProtocol }
 fd := int(file.Fd())
 if fd < 3 || syscall.SetNonblock(fd, true) != nil { return 0, errSealProtocol }
 return fd, nil
}`
const captureEndReadBody = `{
 s := g.seal
 if s == nil || g.invalid { return false }
 if s.captureEnded { return true }
 if g.captureEndFD < 3 { return false }
 var data [49]byte
 n, err := syscall.Read(g.captureEndFD, data[:])
 if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK { return false }
 if err != nil || n != 48 || !bytes.Equal(data[:8], []byte("ADTREND1")) ||
 !bytes.Equal(data[8:24], g.nonce[:]) || binary.BigEndian.Uint64(data[24:32]) != s.epoch ||
 binary.BigEndian.Uint64(data[32:40]) != s.target.generation ||
 binary.BigEndian.Uint64(data[40:48]) != s.registration.generation ||
 s.released || !g.closing || g.pending != 0 || g.open != 1 ||
 s.target.state != 'o' || s.target.registration != s.registration {
 g.invalidateBecauseLocked(invalidExplicit)
 g.notifyLocked()
 return false
 }
 s.captureEnded = true
 return true
}`

func captureBodyMatches(fn *ast.FuncDecl, body string) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package expected;func expected()"+body, 0)
	return err == nil && compactSource(fn.Body) == compactSource(parsed.Decls[0].(*ast.FuncDecl).Body)
}
func captureEndSyscall(path, file, function string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path != managed || file != captureEndSource {
		return false
	}
	var fn *ast.FuncDecl
	for _, node := range ancestors {
		if declaration, ok := node.(*ast.FuncDecl); ok {
			fn = declaration
		}
	}
	if function == "prepareCaptureEnd" && id.Name == "SetNonblock" && captureBodyMatches(fn, captureEndPrepareBody) {
		return objectCall(directCallFor(id, ancestors), info, "syscall", "SetNonblock")
	}
	if function != "lifecycleGate.captureObligationsEndedLocked" || !captureBodyMatches(fn, captureEndReadBody) {
		return false
	}
	switch id.Name {
	case "Read":
		return objectCall(directCallFor(id, ancestors), info, "syscall", "Read")
	case "EAGAIN", "EWOULDBLOCK":
		return true
	}
	return false
}
func captureConsumerCall(id *ast.Ident, ancestors []ast.Node, allowClosure bool) bool {
	if len(ancestors) < 3 {
		return false
	}
	selector, ok := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
	if !ok || selector.Sel != id {
		return false
	}
	call, ok := ancestors[len(ancestors)-3].(*ast.CallExpr)
	if !ok || call.Fun != selector || len(call.Args) != 0 {
		return false
	}
	for _, node := range ancestors {
		switch node.(type) {
		case *ast.GoStmt, *ast.DeferStmt:
			return false
		case *ast.FuncLit:
			if !allowClosure {
				return false
			}
		}
	}
	return true
}
func captureEndDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			reader := path == managed && filename == captureEndSource && name == "lifecycleGate.captureObligationsEndedLocked" && captureBodyMatches(fn, captureEndReadBody)
			if path == managed && name == "prepareCaptureEnd" && !(filename == captureEndSource && captureBodyMatches(fn, captureEndPrepareBody)) && !(filename == "capture_end_ci_unsupported.go" && captureBodyMatches(fn, `{return 0,errSealProtocol}`)) {
				emit(decl, "capture-end preparation must validate FIFO and nonblocking ownership")
			}
			if path == managed && name == "lifecycleGate.captureObligationsEndedLocked" && !reader && !(filename == "capture_end_ci_unsupported.go" && captureBodyMatches(fn, `{return false}`)) {
				emit(decl, "capture-end authorization must consume exactly the reviewed bound frame")
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				switch x := n.(type) {
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed {
						break
					}
					sig := callableSignature(obj.Type())
					if sig == nil {
						break
					}
					if obj.Name() == "prepareCaptureEnd" && sig.Recv() == nil && !(path == managed && filename == "application_controller_ci.go" && name == "StartCIController" && directCallFor(x, ancestors) != nil) {
						emit(x, "capture-end preparation outside exact controller startup")
					}
					if obj.Name() == "captureObligationsEndedLocked" && sig.Recv() != nil && isNamed(sig.Recv().Type(), managed, "lifecycleGate") {
						allowed := path == managed && ((filename == "lifecycle_ci.go" && (name == "lifecycleTicket.closedObserved" || name == "lifecycleTicket.closingConnection")) || (filename == "seal_ci.go" && (name == "lifecycleGate.registerActivityRow" || name == "lifecycleGate.invalidateWaitLocked")))
						if !allowed || !captureConsumerCall(x, ancestors, name == "lifecycleGate.registerActivityRow") {
							emit(x, "capture-end authorization outside the four reviewed liveness call sites")
						}
					}
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection == nil || selection.Kind() != types.FieldVal {
						break
					}
					protected := (isNamed(selection.Recv(), managed, "sealAttempt") && selection.Obj().Name() == "captureEnded") || (isNamed(selection.Recv(), managed, "lifecycleGate") && selection.Obj().Name() == "captureEndFD")
					if protected && !reader && !captureEndCleanup(path, filename, name, x, ancestors) && !observedBirthCapture(path, filename, name, x, ancestors, info) {
						emit(x, "capture-end state or descriptor escapes the unique reviewed consumer")
					}
				case *ast.StarExpr:
					if info.Types[x].IsValue() && isNamed(info.TypeOf(x), managed, "sealAttempt") {
						emit(x, "whole seal copies can bypass capture-end authorization")
					}
				case *ast.CompositeLit:
					for _, element := range x.Elts {
						kv, ok := element.(*ast.KeyValueExpr)
						if !ok {
							if isNamed(info.TypeOf(x), managed, "sealAttempt") {
								emit(element, "positional seal construction bypasses capture-end authorization")
							}
							continue
						}
						key, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						if isNamed(info.TypeOf(x), managed, "sealAttempt") && key.Name == "captureEnded" {
							emit(kv, "capture-end authorization cannot be prepopulated")
						}
						if isNamed(info.TypeOf(x), managed, "lifecycleGate") && key.Name == "captureEndFD" && !(path == managed && filename == "application_controller_ci.go" && name == "StartCIController" && sourceText(kv.Value) == "endFD") {
							emit(kv, "capture-end descriptor installation outside exact bootstrap")
						}
					}
				}
				return true
			})
		}
	}
	return out
}

const captureEndCleanupBody = `{
 stopOnce.Do(func(){stopping.Store(true);normal.Close();rescue.Close()})
 wg.Wait()
 g.mu.Lock()
 g.captureEndFD = 0
 captureEnd.Close()
 g.mu.Unlock()
 reportOnce.Do(func(){g.writeFinalInvalidationDiagnostic()})
}`

func captureEndCleanup(path, file, function string, field *ast.SelectorExpr, ancestors []ast.Node) bool {
	if path != managed || file != "application_controller_ci.go" || function != "StartCIController" || field.Sel.Name != "captureEndFD" || len(ancestors) < 2 {
		return false
	}
	assignment, ok := ancestors[len(ancestors)-2].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 1 || assignment.Lhs[0] != field {
		return false
	}
	for i, node := range ancestors {
		switch expression := node.(type) {
		case *ast.GoStmt, *ast.DeferStmt:
			return false
		case *ast.FuncLit:
			if i < 1 || !captureBodyMatches(&ast.FuncDecl{Body: expression.Body}, captureEndCleanupBody) {
				continue
			}
			declaration, ok := ancestors[i-1].(*ast.AssignStmt)
			if !ok || len(declaration.Lhs) != 1 || len(declaration.Rhs) != 1 || declaration.Rhs[0] != expression || sourceText(declaration.Lhs[0]) != "cleanup" {
				continue
			}
			return true
		}
	}
	return false
}
