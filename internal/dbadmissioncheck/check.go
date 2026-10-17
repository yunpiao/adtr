// Package dbadmissioncheck checks a deliberately bounded, typed source contract.
// It is not a call-graph proof or permission to enable a database admission gate.
package dbadmissioncheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const module = "github.com/yunpiao/adtr"
const pgx = "github.com/jackc/pgx/v5"
const managed = module + "/internal/dbadmission"

// CompleteWorkerClosure remains false until dynamic dispatch, initialization,
// external callbacks and every worker-reachable dependency have a separate proof.
// Consumers must not interpret a passing slice check as admission-gate readiness.
const CompleteWorkerClosure = false

type Diagnostic struct {
	Position token.Position
	Reason   string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%s: %s", d.Position, d.Reason) }

// Check examines every object reference, including function aliases and method
// values, rather than just CallExprs. files and info must come from a successful
// type check of the same package. Unknown protected operations are rejected.
func Check(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if imported == "unsafe" || imported == "database/sql" || strings.HasPrefix(imported, pgx+"/pgxpool") || strings.HasPrefix(imported, pgx+"/stdlib") {
				emit(spec, "unproved alternate database or unsafe import")
			}
		}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(comment.Text, "//go:linkname") {
					emit(comment, "linkname is outside the typed boundary")
				}
			}
		}
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			// Only Ready belongs to this slice; migration is a separate process mode.
			if path == module+"/internal/store" && (fn == nil || fn.Name.Name != "Ready") {
				if fn != nil && fn.Name.Name == "init" {
					emit(fn, "UNPROVEN store initialization outside Ready")
				}
				if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR && !safeStoreVariables(gen, info) {
					emit(gen, "UNPROVEN store package variable initialization outside Ready")
				}
				continue
			}
			name := ""
			if fn != nil {
				name = fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					name = receiverName(fn.Recv.List[0].Type) + "." + name
				}
			}
			trusted := path == managed && filepath.Base(fset.Position(file.Pos()).Filename) == "conn.go"
			// The sole nil-return implementation satisfies pgx.Tx without exposing a
			// connection. Validate its complete body, rather than allowlisting a name.
			nilConn := path == module+"/internal/tasks" && name == "executorTx.Conn" && nilReturn(fn)
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					return true
				}
				if id, ok := n.(*ast.Ident); ok {
					if instance, ok := info.Instances[id]; ok && !trusted {
						for i := 0; i < instance.TypeArgs.Len(); i++ {
							if protectedValue(instance.TypeArgs.At(i)) {
								emit(id, "generic instantiation across the connection/configuration boundary is unproved")
							}
						}
					}
					obj := info.Uses[id]
					if obj != nil && obj.Pkg() != nil {
						p := obj.Pkg().Path()
						if path == module+"/internal/store" && p == path && obj.Parent() == obj.Pkg().Scope() {
							switch obj.(type) {
							case *types.Func, *types.Var:
								if obj.Name() != "Ready" {
									emit(id, "UNPROVEN store.Ready dependency outside the checked function")
								}
							}
						}
						if p == "unsafe" || (p == "reflect" && obj.Name() != "DeepEqual") || p == "database/sql" || strings.HasPrefix(p, pgx+"/pgxpool") || strings.HasPrefix(p, pgx+"/stdlib") {
							emit(id, "unproved reflective, unsafe, or alternate database route: "+p+"."+obj.Name())
						}
						if (p == pgx || p == pgx+"/pgconn") && strings.HasPrefix(obj.Name(), "Connect") {
							if !(trusted && p == pgx && obj.Name() == "ConnectConfig" && name == "ConnectConfig") {
								emit(id, "connection creation must use dbadmission.ConnectConfig")
							}
						}
						if (p == pgx || p == pgx+"/pgconn") && strings.HasPrefix(obj.Name(), "ParseConfig") {
							emit(id, "configuration reparsing is outside the admitted configuration boundary")
						}
						if _, ok := obj.(*types.TypeName); ok && rawType(obj.Type()) && !trusted && !nilConn {
							emit(id, "raw database connection type escapes the managed boundary")
						}
					}
				}
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if s := info.Selections[sel]; s != nil {
						if rawType(s.Recv()) || rawResult(s.Obj().Type()) {
							allowed := trusted && ((name == "Conn.Begin" && sel.Sel.Name == "Begin") || (name == "Conn.QueryRow" && sel.Sel.Name == "QueryRow") || (name == "Conn.Close" && (sel.Sel.Name == "Close" || sel.Sel.Name == "PgConn" || sel.Sel.Name == "CleanupDone" || sel.Sel.Name == "IsClosed")))
							if !allowed {
								emit(sel, "raw connection method or transaction Conn escape")
							}
						}
						if protectedConfig(s.Recv()) && (s.Kind() == types.FieldVal || sel.Sel.Name != "Copy") {
							emit(sel, "configuration field access or unknown configuration operation")
						}
					}
				}
				if lit, ok := n.(*ast.CompositeLit); ok && protectedValue(info.TypeOf(lit)) && !trusted {
					emit(lit, "constructing managed connections or replacement configurations is unproved")
				}
				if star, ok := n.(*ast.StarExpr); ok && info.Types[star].IsValue() && protectedValue(info.TypeOf(star)) && !trusted {
					emit(star, "manual connection/configuration value copy is forbidden; use configuration Copy")
				}
				if call, ok := n.(*ast.CallExpr); ok && info.Types[call.Fun].IsType() && (protectedValue(info.TypeOf(call)) || (len(call.Args) > 0 && protectedValue(info.TypeOf(call.Args[0])))) && !trusted {
					emit(call, "conversion across the connection/configuration boundary")
				}
				return true
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Offset != b.Position.Offset {
			return a.Position.Offset < b.Position.Offset
		}
		return a.Reason < b.Reason
	})
	return out
}
func receiverName(e ast.Expr) string {
	if p, ok := e.(*ast.StarExpr); ok {
		return receiverName(p.X)
	}
	if n, ok := e.(*ast.Ident); ok {
		return n.Name
	}
	return "?"
}
func nilReturn(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	r, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(r.Results) != 1 {
		return false
	}
	id, ok := r.Results[0].(*ast.Ident)
	return ok && id.Name == "nil"
}
func named(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, _ := t.(*types.Named)
	return n
}
func isNamed(t types.Type, path, name string) bool {
	n := named(t)
	return n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == path && n.Obj().Name() == name
}
func rawType(t types.Type) bool {
	return isNamed(t, pgx, "Conn") || isNamed(t, pgx+"/pgconn", "PgConn")
}
func rawResult(t types.Type) bool {
	sig, ok := t.(*types.Signature)
	if !ok {
		return false
	}
	for i := 0; i < sig.Results().Len(); i++ {
		if rawType(sig.Results().At(i).Type()) {
			return true
		}
	}
	return false
}
func protectedConfig(t types.Type) bool {
	return isNamed(t, pgx, "ConnConfig") || isNamed(t, pgx+"/pgconn", "Config")
}
func protectedValue(t types.Type) bool {
	return rawType(t) || protectedConfig(t) || isNamed(t, managed, "Conn")
}

// Migration error sentinels and a constant regexp are harmless initialization;
// unknown package initializers require review rather than silently escaping.
func safeStoreVariables(gen *ast.GenDecl, info *types.Info) bool {
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok || len(value.Values) == 0 {
			return false
		}
		for _, expr := range value.Values {
			if info.Types[expr].Value != nil {
				continue
			}
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return false
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			obj := info.Uses[sel.Sel]
			if obj == nil || obj.Pkg() == nil {
				return false
			}
			if !((obj.Pkg().Path() == "errors" && obj.Name() == "New") || (obj.Pkg().Path() == "regexp" && obj.Name() == "MustCompile")) || len(call.Args) != 1 || info.Types[call.Args[0]].Value == nil {
				return false
			}
		}
	}
	return true
}
