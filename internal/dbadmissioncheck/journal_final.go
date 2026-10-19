package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const journalPackage = module + "/internal/operationallogs"
const journalWriterBody = `{
 snapshot := ciJournalProjection(recorder.Stats(), closeErr == nil)
 data, err := json.Marshal(snapshot)
 if err == nil { _, _ = os.Stderr.Write(append(data, '\n')) }
}`

func journalFinalDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			if path == module+"/cmd/adtr" && name == "emitCIJournalFinal" && !((filename == "db_journal_ci.go" && captureBodyMatches(fn, journalWriterBody)) || (filename == "db_journal_default.go" && captureBodyMatches(fn, `{}`))) {
				emit(decl, "final journal writer must only serialize the reviewed projection")
			}
			if path == module+"/cmd/adtr" && name == "ciJournalProjection" && !(filename == "db_journal_ci.go" && captureBodyMatches(fn, journalProjectionBody)) {
				emit(decl, "final journal projection must hash the process ID and emit only the reviewed counters")
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
					if !ok || obj.Pkg() == nil {
						break
					}
					pkg := obj.Pkg().Path()
					if pkg == module+"/cmd/adtr" && obj.Name() == "emitCIJournalFinal" && !(path == pkg && filename == "main.go" && name == "run" && journalAfterClose(x, ancestors, info)) {
						emit(x, "final journal emission must follow Close in the installed CI worker branch")
					}
					if pkg == module+"/cmd/adtr" && obj.Name() == "ciJournalProjection" && !(path == pkg && filename == "db_journal_ci.go" && name == "emitCIJournalFinal" && directCallFor(x, ancestors) != nil) {
						emit(x, "final journal projection outside the private final writer")
					}
					sig := callableSignature(obj.Type())
					if pkg == journalPackage && obj.Name() == "Stats" && sig != nil && sig.Recv() != nil && isNamed(sig.Recv().Type(), journalPackage, "Recorder") {
						allowed := directCallFor(x, ancestors) != nil && ((path == journalPackage && filename == "recorder.go" && name == "Recorder.run") || (path == module+"/cmd/adtr" && filename == "db_journal_ci.go" && name == "emitCIJournalFinal" && captureBodyMatches(fn, journalWriterBody)))
						if !allowed {
							emit(x, "recorder snapshot outside existing Append or reviewed post-Close writer")
						}
					}
				case *ast.TypeSpec:
					if path == module+"/cmd/adtr" && x.Name.Name == "ciJournalFinal" && !(filename == "db_journal_ci.go" && journalShape(info.TypeOf(x.Type))) {
						emit(x, "final journal record must have the exact eleven reviewed JSON fields")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), module+"/cmd/adtr", "ciJournalFinal") && !(path == module+"/cmd/adtr" && filename == "db_journal_ci.go" && name == "ciJournalProjection") {
						emit(x, "final journal payload construction outside the closed projection")
					}
				}
				return true
			})
		}
	}
	return out
}
func journalAfterClose(id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if len(ancestors) < 2 {
		return false
	}
	call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr)
	if !ok || call.Fun != id || sourceText(call) != "emitCIJournalFinal(journal, closeErr)" {
		return false
	}
	var guard *ast.IfStmt
	for _, ancestor := range ancestors {
		if _, async := ancestor.(*ast.GoStmt); async {
			return false
		}
		if branch, ok := ancestor.(*ast.IfStmt); ok && compactSource(branch.Cond) == `admissionFailure!=nil&&*mode=="worker"` && branch.Init == nil && branch.Else == nil && len(branch.Body.List) == 1 {
			expression, ok := branch.Body.List[0].(*ast.ExprStmt)
			if ok && expression.X == call {
				guard = branch
			}
		}
	}
	if guard == nil {
		return false
	}
	for _, ancestor := range ancestors {
		block, ok := ancestor.(*ast.BlockStmt)
		if !ok {
			continue
		}
		for i, statement := range block.List {
			if statement != guard || i == 0 {
				continue
			}
			assignment, ok := block.List[i-1].(*ast.AssignStmt)
			if !ok || compactSource(assignment) != "closeErr:=journal.Close(drain)" {
				return false
			}
			closing, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok || !objectCall(closing, info, journalPackage, "Close") {
				return false
			}
			method, ok := closing.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			receiver, ok := method.X.(*ast.Ident)
			if !ok {
				return false
			}
			arg, ok := call.Args[0].(*ast.Ident)
			if !ok {
				return false
			}
			result, ok := assignment.Lhs[0].(*ast.Ident)
			if !ok {
				return false
			}
			resultArg, ok := call.Args[1].(*ast.Ident)
			return ok && info.Uses[receiver] == info.Uses[arg] && info.Defs[result] == info.Uses[resultArg]
		}
	}
	return false
}

const journalProjectionBody = `{
 sum := sha256.Sum256([]byte(s.ProcessID))
 return ciJournalFinal{"owned_ci_final_journal", hex.EncodeToString(sum[:]), closeOK && s.Module == operationallogs.Worker,
 s.Accepted, s.Acknowledged, s.Rejected, s.QueueFull, s.WriteFailures, s.Unacknowledged, s.Abandoned, s.AcceptanceStopped}
}`

func journalShape(typ types.Type) bool {
	if typ == nil {
		return false
	}
	structure, ok := typ.Underlying().(*types.Struct)
	if !ok || structure.NumFields() != 11 {
		return false
	}
	fields := []struct {
		name, key string
		kind      types.BasicKind
	}{
		{"Event", "event", types.String}, {"ProcessIDSHA256", "processIDSHA256", types.String}, {"CloseOK", "closeOK", types.Bool},
		{"Accepted", "accepted", types.Int64}, {"Acknowledged", "acknowledged", types.Int64}, {"Rejected", "rejected", types.Int64},
		{"QueueFull", "queueFull", types.Int64}, {"WriteFailures", "writeFailures", types.Int64}, {"Unacknowledged", "unacknowledged", types.Int64},
		{"Abandoned", "abandoned", types.Int64}, {"AcceptanceStopped", "acceptanceStopped", types.Bool},
	}
	for i, field := range fields {
		if structure.Field(i).Name() != field.name || !types.Identical(structure.Field(i).Type(), types.Typ[field.kind]) || structure.Tag(i) != `json:"`+field.key+`"` {
			return false
		}
	}
	return true
}
