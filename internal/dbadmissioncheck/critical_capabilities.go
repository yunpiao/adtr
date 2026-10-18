package dbadmissioncheck

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"unicode"
)

const witnessSource = "witness_ci_linux_amd64.go"

func sourceText(node ast.Node) string {
	var out bytes.Buffer
	if format.Node(&out, token.NewFileSet(), node) != nil {
		return ""
	}
	return out.String()
}
func directCallFor(id *ast.Ident, ancestors []ast.Node) *ast.CallExpr {
	if len(ancestors) < 2 {
		return nil
	}
	var expression ast.Expr = id
	parent := len(ancestors) - 2
	if sel, ok := ancestors[parent].(*ast.SelectorExpr); ok && sel.Sel == id {
		expression = sel
		parent--
	}
	if parent < 0 {
		return nil
	}
	call, ok := ancestors[parent].(*ast.CallExpr)
	if !ok || call.Fun != expression {
		return nil
	}
	for _, ancestor := range ancestors {
		switch ancestor.(type) {
		case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
			return nil
		}
	}
	return call
}
func objectCall(call *ast.CallExpr, info *types.Info, pkg, name string) bool {
	if call == nil {
		return false
	}
	var id *ast.Ident
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	default:
		return false
	}
	obj := info.Uses[id]
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkg && obj.Name() == name
}
func witnessUnsafe(path, file, function string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path != managed || file != witnessSource || function != "mapCIWitness" || id.Name != "Pointer" {
		return false
	}
	call := directCallFor(id, ancestors)
	if !objectCall(call, info, "unsafe", "Pointer") || len(call.Args) != 1 {
		return false
	}
	arg, ok := call.Args[0].(*ast.UnaryExpr)
	if !ok || arg.Op != token.AND {
		return false
	}
	index, ok := arg.X.(*ast.IndexExpr)
	if !ok || (sourceText(index) != "page[24]" && sourceText(index) != "page[28]") {
		return false
	}
	// The sole page variable must be the result of the audited Mmap call.
	page, ok := index.X.(*ast.Ident)
	if !ok {
		return false
	}
	var declaration *ast.FuncDecl
	for _, ancestor := range ancestors {
		if f, ok := ancestor.(*ast.FuncDecl); ok {
			declaration = f
		}
	}
	if declaration == nil {
		return false
	}
	mapped := false
	for _, stmt := range declaration.Body.List {
		if assignment, ok := stmt.(*ast.AssignStmt); ok && len(assignment.Lhs) == 2 && len(assignment.Rhs) == 1 {
			if lhs, ok := assignment.Lhs[0].(*ast.Ident); ok && info.Defs[lhs] == info.Uses[page] {
				rhs, _ := assignment.Rhs[0].(*ast.CallExpr)
				mapped = validMmapCall(rhs, info)
			}
		}
	}
	if !mapped || !validatedWitnessHeader(declaration, call.Pos()) {
		return false
	}
	for i, node := range ancestors {
		if node == call && i > 0 {
			outer, ok := ancestors[i-1].(*ast.CallExpr)
			if !ok || len(outer.Args) != 1 || outer.Args[0] != call || !info.Types[outer.Fun].IsType() {
				return false
			}
			text := sourceText(outer.Fun)
			return text == "uintptr" || (text == "(*uint32)" && validatedWitnessAlignment(declaration, call.Pos()))
		}
	}
	return false
}
func validMmapCall(call *ast.CallExpr, info *types.Info) bool {
	return objectCall(call, info, "syscall", "Mmap") && sourceText(call) == "syscall.Mmap(int(file.Fd()), 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)"
}
func witnessSyscall(path, file, function string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path == module+"/cmd/adtr" && file == "db_admission_ci.go" && function == "configureCIAdmission" && id.Name == "SetNonblock" {
		call := directCallFor(id, ancestors)
		if !objectCall(call, info, "syscall", "SetNonblock") || sourceText(call) != "syscall.SetNonblock(fd, true)" {
			return false
		}
		for _, ancestor := range ancestors {
			if condition, ok := ancestor.(*ast.IfStmt); ok && sourceText(condition.Cond) == "i != 2 && syscall.SetNonblock(fd, true) != nil" {
				return true
			}
		}
		return false
	}
	if path != managed || file != witnessSource || function != "mapCIWitness" {
		return false
	}
	switch id.Name {
	case "Mmap", "PROT_READ", "PROT_WRITE", "MAP_SHARED":
	default:
		return false
	}
	for _, ancestor := range ancestors {
		if call, ok := ancestor.(*ast.CallExpr); ok && validMmapCall(call, info) {
			return true
		}
	}
	return false
}
func allowedAtomic(path, file, function string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	call := directCallFor(id, ancestors)
	if path != managed || file != witnessSource || call == nil {
		return false
	}
	switch function {
	case "ciWitness.invalidate":
		return objectCall(call, info, "sync/atomic", "StoreUint32") && sourceText(call) == "atomic.StoreUint32(w.invalid, 1)"
	case "mapCIWitness":
		return (objectCall(call, info, "sync/atomic", "LoadUint32") && sourceText(call) == "atomic.LoadUint32(invalid)") || (objectCall(call, info, "sync/atomic", "CompareAndSwapUint32") && sourceText(call) == "atomic.CompareAndSwapUint32(bound, 0, 1)")
	}
	return false
}
func gateInvalid(sel *ast.SelectorExpr, info *types.Info) bool {
	selection := info.Selections[sel]
	return selection != nil && selection.Kind() == types.FieldVal && isNamed(selection.Recv(), managed, "lifecycleGate") && selection.Obj().Name() == "invalid"
}
func exactInvalidation(fn *ast.FuncDecl, info *types.Info) bool {
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 2 {
		return false
	}
	first, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, _ := first.X.(*ast.CallExpr)
	return objectCall(call, info, managed, "invalidateWitness") && sourceText(first) == "invalidateWitness(g.witness)" && sourceText(fn.Body.List[1]) == "g.invalid = true"
}
func criticalDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			if path == managed && name == "lifecycleGate.writeFinalInvalidationDiagnostic" && !(filename == "application_diagnostic_ci.go" && exactFinalDiagnosticWriter(fn)) {
				emit(decl, "final diagnostic writer must only serialize its closed projection to stderr")
			}
			if path == managed && name == "lifecycleGate.invalidateBecauseLocked" && !(filename == "application_diagnostic_ci.go" && exactDiagnosticInvalidation(fn)) {
				emit(decl, "diagnostic invalidator must preserve sticky-first and first-reason-only ordering")
			}
			if path == managed && name == "invalidateWitness" && !(filename == "lifecycle_ci.go" && exactWitnessDispatcher(fn, info)) {
				emit(decl, "witness dispatcher must only synchronously invalidate its nonnil argument")
			}
			if path == managed && filename == witnessSource && name == "ciWitness.invalidate" && !(fn.Body != nil && len(fn.Body.List) == 1 && sourceText(fn.Body.List[0]) == "atomic.StoreUint32(w.invalid, 1)") {
				emit(decl, "sticky invalid writer must only atomically store one")
			}
			canonical := path == managed && filename == "lifecycle_ci.go" && name == "lifecycleGate.markInvalidLocked" && exactInvalidation(fn, info)
			if path == managed && name == "lifecycleGate.markInvalidLocked" && !canonical {
				emit(decl, "gate invalidation must synchronously publish sticky invalid before its sole true assignment")
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				switch x := n.(type) {
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection != nil && selection.Kind() == types.FieldVal && isNamed(selection.Recv(), managed, "ciWitness") {
						allowed := false
						if len(ancestors) >= 2 {
							if call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr); ok {
								allowed = path == managed && filename == witnessSource && name == "ciWitness.invalidate" && objectCall(call, info, "sync/atomic", "StoreUint32") && sourceText(call) == "atomic.StoreUint32(w.invalid, 1)" && selection.Obj().Name() == "invalid"
							}
						}
						if !allowed {
							emit(x, "shared witness fields cannot escape the sole atomic sticky writer")
						}
					}
				case *ast.RangeStmt:
					for _, target := range []ast.Expr{x.Key, x.Value} {
						if sel, ok := target.(*ast.SelectorExpr); ok && gateInvalid(sel, info) {
							emit(sel, "range cannot write the protected gate invalid field")
						}
					}
				case *ast.AssignStmt:
					for _, lhs := range x.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && gateInvalid(sel, info) && (!canonical || fn.Body.List[1] != x) {
							emit(lhs, "gate invalid field has only one reviewed sticky-first writer")
						}
					}
				case *ast.UnaryExpr:
					if x.Op == token.AND {
						if sel, ok := x.X.(*ast.SelectorExpr); ok && gateInvalid(sel, info) {
							emit(x, "gate invalid field address escapes unique writer")
						}
					}
				case *ast.StarExpr:
					if info.Types[x].IsValue() && isNamed(info.TypeOf(x), managed, "lifecycleGate") {
						emit(x, "whole gate value copies can bypass sticky invalidation")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), managed, "lifecycleGate") {
						for _, element := range x.Elts {
							kv, ok := element.(*ast.KeyValueExpr)
							if !ok {
								emit(element, "positional gate construction bypasses invalidation review")
								continue
							}
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "invalid" && sourceText(kv.Value) != "false" {
								emit(element, "gate invalid state must be published through the unique writer")
							}
						}
					}
					if isNamed(info.TypeOf(x), managed, "ciWitness") && !(path == managed && filename == witnessSource && name == "mapCIWitness") {
						emit(x, "shared witness construction outside reviewed mapper")
					}
				case *ast.Ident:
					if obj, ok := info.Defs[x].(*types.Const); ok && isNamed(obj.Type(), managed, "invalidationReason") && !reviewedInvalidationConstant(obj) {
						emit(x, "invalidation reason constant is outside the reviewed closed set")
					}
					obj := info.Uses[x]
					if obj == nil || obj.Pkg() == nil {
						return true
					}
					pkg := obj.Pkg().Path()
					if function, ok := obj.(*types.Func); ok && pkg == managed && finalDiagnosticOperation(function) && !allowedFinalDiagnosticCaller(path, filename, name, function.Name(), x, ancestors, info) {
						emit(x, "final diagnostic capability outside its exact reviewed caller")
					}
					if function, ok := obj.(*types.Func); ok && pkg == managed && protectedSelectionOperation(function) && !allowedSelectionCaller(path, filename, name, function.Name(), x, ancestors) {
						emit(x, "activity selection capability outside its exact synchronous caller")
					}
					if function, ok := obj.(*types.Func); ok && pkg == managed {
						signature := callableSignature(function.Type())
						if signature != nil && signature.Recv() != nil && isNamed(signature.Recv().Type(), managed, "lifecycleGate") {
							if obj.Name() == "markInvalidLocked" && !(path == managed && filename == "application_diagnostic_ci.go" && name == "lifecycleGate.invalidateBecauseLocked" && exactDiagnosticInvalidation(fn) && directCallFor(x, ancestors) != nil) {
								emit(x, "sticky mark must only be called by the reviewed diagnostic invalidator")
							}
							if obj.Name() == "invalidateBecauseLocked" && !constantInvalidationCall(x, ancestors, info) {
								emit(x, "invalidation requires a direct call with a reviewed reason constant")
							}
						}
					}
					if pkg == module+"/cmd/adtr" && obj.Name() == "configureCIAdmission" && !(path == pkg && filename == "main.go" && name == "run" && directCallFor(x, ancestors) != nil) {
						emit(x, "CI admission configuration outside exact process bootstrap")
					}
					if pkg == managed && obj.Name() == "StartCIController" && !(path == module+"/cmd/adtr" && filename == "db_admission_ci.go" && name == "configureCIAdmission" && directCallFor(x, ancestors) != nil) {
						emit(x, "CI controller installation outside exact tagged bootstrap")
					}
					if pkg == managed && obj.Name() == "mapCIWitness" && !(path == managed && filename == "application_controller_ci.go" && name == "StartCIController" && directCallFor(x, ancestors) != nil) {
						emit(x, "shared witness mapper outside exact controller startup")
					}
					if pkg == managed && obj.Name() == "invalidateWitness" && !(canonical && directCallFor(x, ancestors) != nil) {
						emit(x, "sticky witness publication outside unique gate invalidator")
					}
					if pkg == "sync/atomic" {
						if function, ok := obj.(*types.Func); ok {
							signature := callableSignature(function.Type())
							if signature != nil && signature.Recv() == nil && !allowedAtomic(path, filename, name, x, ancestors, info) {
								emit(x, "unreviewed atomic shared-memory capability")
							}
						}
					}
				}
				return true
			})
		}
	}
	return out
}

func compactSource(node ast.Node) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, sourceText(node))
}
func exactWitnessDispatcher(fn *ast.FuncDecl, info *types.Info) bool {
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	if compactSource(fn.Body.List[0]) != "ifw!=nil{w.invalidate()}" || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	field := fn.Type.Params.List[0]
	if len(field.Names) != 1 || !isNamed(info.TypeOf(field.Type), managed, "invalidationWitness") {
		return false
	}
	guard := fn.Body.List[0].(*ast.IfStmt)
	statement, ok := guard.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := statement.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	selection := info.Selections[selector]
	return ok && info.Uses[receiver] == info.Defs[field.Names[0]] && selection != nil && selection.Kind() == types.MethodVal && isNamed(selection.Recv(), managed, "invalidationWitness")
}
func validatedWitnessHeader(fn *ast.FuncDecl, before token.Pos) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	expected := `len(page)!=4096||string(page[:8])!="ADTRGATE"||binary.LittleEndian.Uint32(page[8:12])!=1||binary.LittleEndian.Uint32(page[12:16])!=4096||binary.LittleEndian.Uint64(page[16:24])!=1||!bytes.Equal(page[32:48],nonce[:])||!bytes.Equal(page[48:],make([]byte,4096-48))`
	for _, statement := range fn.Body.List {
		guard, ok := statement.(*ast.IfStmt)
		if !ok || guard.End() >= before || guard.Init != nil || guard.Else != nil || len(guard.Body.List) != 1 {
			continue
		}
		if compactSource(guard.Cond) == expected && compactSource(guard.Body.List[0]) == "returnnil,errSealProtocol" {
			return true
		}
	}
	return false
}

func validatedWitnessAlignment(fn *ast.FuncDecl, before token.Pos) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	for _, statement := range fn.Body.List {
		guard, ok := statement.(*ast.IfStmt)
		if !ok || guard.End() >= before || guard.Init != nil || guard.Else != nil || len(guard.Body.List) != 1 {
			continue
		}
		if compactSource(guard.Cond) == "uintptr(unsafe.Pointer(&page[24]))%4!=0||uintptr(unsafe.Pointer(&page[28]))%4!=0" && compactSource(guard.Body.List[0]) == "returnnil,errSealProtocol" {
			return true
		}
	}
	return false
}

var reviewedInvalidationReasons = map[string]string{
	"invalidCapacity": "capacity", "invalidConnectFailed": "connect_failed",
	"invalidConnectState": "invalid_connect_state", "invalidDuplicateBackend": "duplicate_backend",
	"invalidCloseState": "invalid_close_state", "invalidUncleanClose": "unclean_close",
	"invalidTargetClosed": "target_closed", "invalidTargetClosing": "target_closing",
	"invalidTargetRevoked": "target_revoked", "invalidWaitCancelled": "wait_cancelled",
	"invalidTransport": "transport", "invalidRecordCapacity": "record_capacity",
	"invalidAbort": "abort", "invalidExplicit": "explicit_invalidation",
}

func reviewedInvalidationConstant(obj *types.Const) bool {
	expected, ok := reviewedInvalidationReasons[obj.Name()]
	return ok && obj.Pkg() != nil && obj.Pkg().Path() == managed && isNamed(obj.Type(), managed, "invalidationReason") && obj.Val().Kind() == constant.String && constant.StringVal(obj.Val()) == expected
}
func constantInvalidationCall(id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	// Existing registration cleanup is a reviewed closure. The call itself must
	// still be direct; no method value, conversion or dynamic reason is accepted.
	for _, ancestor := range ancestors {
		switch ancestor.(type) {
		case *ast.GoStmt, *ast.DeferStmt:
			return false
		}
	}
	if len(ancestors) < 3 {
		return false
	}
	selector, ok := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
	if !ok || selector.Sel != id {
		return false
	}
	call, ok := ancestors[len(ancestors)-3].(*ast.CallExpr)
	if !ok || call.Fun != selector || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	argument, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return false
	}
	obj, ok := info.Uses[argument].(*types.Const)
	return ok && reviewedInvalidationConstant(obj)
}
func exactDiagnosticInvalidation(fn *ast.FuncDecl) bool {
	return fn != nil && fn.Body != nil && len(fn.Body.List) == 3 && compactSource(fn.Body.List[0]) == "phase:=g.diagnosticPhaseLocked()" && compactSource(fn.Body.List[1]) == "g.markInvalidLocked()" && compactSource(fn.Body.List[2]) == `ifg.firstInvalidReason==""{g.firstInvalidReason=reasong.firstInvalidPhase=phase}`
}

func protectedSelectionOperation(function *types.Func) bool {
	signature := callableSignature(function.Type())
	if signature == nil {
		return false
	}
	if signature.Recv() == nil {
		return function.Name() == "activityRowKey"
	}
	if !isNamed(signature.Recv().Type(), managed, "lifecycleGate") {
		return false
	}
	switch function.Name() {
	case "prepareActivitySelection", "configureActivitySelection", "awaitActivitySelection", "registerActivityRow":
		return true
	}
	return false
}
func allowedSelectionCaller(path, file, function, operation string, id *ast.Ident, ancestors []ast.Node) bool {
	if path != managed || directCallFor(id, ancestors) == nil {
		return false
	}
	switch operation {
	case "prepareActivitySelection":
		if file != queuedParkSource || function != "lifecycleGate.prepareQueuedSelection" {
			return false
		}
		for _, node := range ancestors {
			if fn, ok := node.(*ast.FuncDecl); ok {
				return exactQueuedParkFunction(path, file, function, fn)
			}
		}
		return false
	case "configureActivitySelection":
		if file == "application_controller_ci.go" && function == "serveApplicationNormal" {
			for _, node := range ancestors {
				if fn, ok := node.(*ast.FuncDecl); ok {
					return exactFinalQuiesceFunction(path, file, function, fn)
				}
			}
		}
		return false
	case "awaitActivitySelection":
		return file == "activity_selection_ci.go" && function == "AwaitActivitySelection"
	case "registerActivityRow":
		return file == "activity_identity_ci.go" && function == "RegisterActivityIdentity"
	case "activityRowKey":
		return file == "activity_selection_ci.go" && function == "lifecycleGate.configureActivitySelection"
	}
	return false
}
