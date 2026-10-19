package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const ciPhaseEntryBody = `{
	return connectWithPhase(ctx, config, pgx.ConnectConfig)
}`

const journalCIConnectBody = `{
	return dbadmission.ConnectConfigWithCIPhase(ctx, config)
}`

const journalDefaultConnectBody = `{
	conn, err := dbadmission.ConnectConfig(ctx, config)
	return conn, err, "none"
}`

const journalCIConnectErrorBody = `{
	out := newStoreAppendError(ctx, "connect", err, false)
	switch phase {
	case "admission", "native":
		out.diagnostic.connectPhase = phase
	}
	return out
}`

const journalDefaultConnectErrorBody = `{
	return newStoreAppendError(ctx, "connect", err, false)
}`

func exactCIPhaseEntry(path, file, name string, fn *ast.FuncDecl) bool {
	return path == managed && file == "connect_phase_ci.go" && name == "ConnectConfigWithCIPhase" && captureBodyMatches(fn, ciPhaseEntryBody)
}
func exactJournalConnect(path, file, name string, fn *ast.FuncDecl) bool {
	if path != journalPackage {
		return false
	}
	if name == "connectJournal" {
		return (file == "append_diagnostic_ci.go" && captureBodyMatches(fn, journalCIConnectBody)) || (file == "append_diagnostic_default.go" && captureBodyMatches(fn, journalDefaultConnectBody))
	}
	if name == "newStoreConnectError" {
		return (file == "append_diagnostic_ci.go" && captureBodyMatches(fn, journalCIConnectErrorBody)) || (file == "append_diagnostic_default.go" && captureBodyMatches(fn, journalDefaultConnectErrorBody))
	}
	return false
}
func connectPhaseDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		filename := filepath.Base(fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			name := ""
			if fn != nil {
				name = fn.Name.Name
				if fn.Recv != nil {
					name = receiverName(fn.Recv.List[0].Type) + "." + name
				}
			}
			entry := exactCIPhaseEntry(path, filename, name, fn)
			core := exactConnectFailureFunction(path, filename, name, fn)
			journal := exactJournalConnect(path, filename, name, fn)
			if path == managed && name == "ConnectConfigWithCIPhase" && !entry {
				emit(decl, "CI connect phase entry must retain its exact delegate")
			}
			if path == managed && (name == "connectWith" || name == "connectWithPhase") && !core {
				emit(decl, "connect phase must retain original context, error identity and permit order")
			}
			if path == journalPackage && (name == "connectJournal" || name == "newStoreConnectError") && !journal {
				emit(decl, "journal connect phase helper must retain its exact tagged/default body")
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj, ok := info.Uses[id].(*types.Func)
				if !ok || obj.Pkg() == nil {
					return true
				}
				pkg := obj.Pkg().Path()
				allowed, protected := false, true
				switch {
				case pkg == managed && obj.Name() == "ConnectConfigWithCIPhase":
					allowed = journal && filename == "append_diagnostic_ci.go" && name == "connectJournal"
				case pkg == managed && obj.Name() == "connectWithPhase":
					allowed = entry || (core && name == "connectWith")
				case pkg == journalPackage && (obj.Name() == "connectJournal" || obj.Name() == "newStoreConnectError"):
					allowed = path == journalPackage && filename == "store.go" && name == "Store.Append"
				default:
					protected = false
				}
				if protected && (!allowed || directCallFor(id, ancestors) == nil) {
					emit(id, "connect phase capability outside its exact synchronous caller")
				}
				return true
			})
		}
	}
	return out
}
