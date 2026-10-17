package dbadmissioncheck

import (
	"go/ast"
	"go/types"
)

func finalDiagnosticOperation(function *types.Func) bool {
	signature := callableSignature(function.Type())
	return signature != nil && signature.Recv() != nil && isNamed(signature.Recv().Type(), managed, "lifecycleGate") && (function.Name() == "writeFinalInvalidationDiagnostic" || function.Name() == "finalInvalidationDiagnostic" || function.Name() == "writeUncleanCloseDiagnostic" || function.Name() == "uncleanCloseDiagnostic")
}
func exactFinalDiagnosticWriter(fn *ast.FuncDecl) bool {
	return fn != nil && fn.Body != nil && len(fn.Body.List) == 3 && compactSource(fn.Body.List[0]) == "data,err:=json.Marshal(g.finalInvalidationDiagnostic())" && compactSource(fn.Body.List[1]) == `iferr==nil{_,_=os.Stderr.Write(append(data,'\n'))}` && compactSource(fn.Body.List[2]) == "g.writeUncleanCloseDiagnostic()"
}
func allowedFinalDiagnosticCaller(path, file, function, operation string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path != managed {
		return false
	}
	if operation == "writeUncleanCloseDiagnostic" {
		return file == "application_diagnostic_ci.go" && function == "lifecycleGate.writeFinalInvalidationDiagnostic" && directCallFor(id, ancestors) != nil
	}
	if operation == "uncleanCloseDiagnostic" {
		return file == "application_diagnostic_ci.go" && function == "lifecycleGate.writeUncleanCloseDiagnostic" && directCallFor(id, ancestors) != nil
	}
	if operation == "finalInvalidationDiagnostic" {
		return file == "application_diagnostic_ci.go" && function == "lifecycleGate.writeFinalInvalidationDiagnostic" && directCallFor(id, ancestors) != nil
	}
	if file != "application_controller_ci.go" || function != "StartCIController" || len(ancestors) < 3 {
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
	once := false
	for _, ancestor := range ancestors {
		switch node := ancestor.(type) {
		case *ast.GoStmt, *ast.DeferStmt:
			return false
		case *ast.CallExpr:
			if compactSource(node) != "reportOnce.Do(func(){g.writeFinalInvalidationDiagnostic()})" {
				continue
			}
			method, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			selection := info.Selections[method]
			once = selection != nil && selection.Kind() == types.MethodVal && isNamed(selection.Recv(), "sync", "Once") && selection.Obj().Name() == "Do"
		}
	}
	return once
}
