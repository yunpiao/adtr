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
	scanners := scanOnlyParameters(files, info)
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if imported == "unsafe" || imported == "plugin" || imported == "os/exec" || imported == "database/sql" || strings.HasPrefix(imported, pgx+"/pgxpool") || strings.HasPrefix(imported, pgx+"/stdlib") {
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
			name := ""
			if fn != nil {
				name = fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					name = receiverName(fn.Recv.List[0].Type) + "." + name
				}
			}
			factory := path == module+"/internal/runtime" && filepath.Base(fset.Position(file.Pos()).Filename) == "config.go" && name == "LoadConfig"
			trusted := path == managed && filepath.Base(fset.Position(file.Pos()).Filename) == "conn.go"
			// The sole nil-return implementation satisfies pgx.Tx without exposing a
			// connection. Validate its complete body, rather than allowlisting a name.
			nilConn := path == module+"/internal/tasks" && name == "executorTx.Conn" && nilReturn(fn)
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				if id, ok := n.(*ast.Ident); ok {
					if instance, ok := info.Instances[id]; ok && !trusted {
						for i := 0; i < instance.TypeArgs.Len(); i++ {
							if sensitiveDynamic(instance.TypeArgs.At(i)) {
								emit(id, "generic instantiation across the connection/configuration boundary is unproved")
							}
						}
					}
					obj := info.Uses[id]
					if obj != nil && obj.Pkg() != nil {
						p := obj.Pkg().Path()
						if protectedActivityOperation(p, obj.Name()) && !allowedActivityCaller(path, filepath.Base(fset.Position(file.Pos()).Filename), name, p, obj.Name()) {
							emit(id, "activity identity mint or registration outside exact caller")
						}

						if transportConstructor(p, obj) && !reviewedTransport(path, filepath.Base(fset.Position(file.Pos()).Filename), name, p, obj.Name()) {
							emit(id, "unreviewed network transport constructor or method")
						}

						if p == "unsafe" || p == "plugin" || p == "os/exec" || (p == "syscall" && !safeSystemObject(path, obj.Name()) && !safeSystemField(path, filepath.Base(fset.Position(file.Pos()).Filename), name, id, ancestors, info)) || (p == "reflect" && obj.Name() != "DeepEqual") || p == "database/sql" || strings.HasPrefix(p, pgx+"/pgxpool") || strings.HasPrefix(p, pgx+"/stdlib") {
							emit(id, "unproved reflective, unsafe, or alternate database route: "+p+"."+obj.Name())
						}
						if f, ok := obj.(*types.Func); ok && (p == pgx || p == pgx+"/pgconn") && rawResult(f.Type()) {
							sig := f.Type().(*types.Signature)
							if sig.Recv() == nil && !(trusted && p == pgx && obj.Name() == "ConnectConfig" && name == "ConnectConfig") {
								emit(id, "raw database connection constructor or factory")
							}
						}
						if (p == pgx || p == pgx+"/pgconn") && strings.HasPrefix(obj.Name(), "Connect") {
							if !(trusted && p == pgx && obj.Name() == "ConnectConfig" && name == "ConnectConfig") {
								emit(id, "connection creation must use dbadmission.ConnectConfig")
							}
						}
						if (p == pgx || p == pgx+"/pgconn") && strings.HasPrefix(obj.Name(), "ParseConfig") && !(factory && p == pgx && obj.Name() == "ParseConfig") {
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
							allowed := trusted && ((name == "Conn.Begin" && sel.Sel.Name == "Begin") || (name == "Conn.QueryRow" && sel.Sel.Name == "QueryRow") || (name == "Conn.Exec" && sel.Sel.Name == "Exec") || (name == "Conn.Close" && (sel.Sel.Name == "Close" || sel.Sel.Name == "PgConn" || sel.Sel.Name == "CleanupDone" || sel.Sel.Name == "IsClosed")))
							if path == managed && filepath.Base(fset.Position(file.Pos()).Filename) == "activity_identity_ci.go" && name == "RegisterActivityIdentity" && (sel.Sel.Name == "IsClosed" || sel.Sel.Name == "PgConn" || sel.Sel.Name == "PID") {
								allowed = true
							}
							if !allowed {
								emit(sel, "raw connection method or transaction Conn escape")
							}
						}
						if protectedConfig(s.Recv()) && !((s.Kind() != types.FieldVal && sel.Sel.Name == "Copy") || (factory && s.Kind() == types.FieldVal && configurationReadField(sel.Sel.Name))) {
							emit(sel, "configuration field access or unknown configuration operation")
						}
					}
				}
				if lit, ok := n.(*ast.CompositeLit); ok && isNamed(info.TypeOf(lit), managed, "ActivityIdentity") && !(path == module+"/internal/tasks" && filepath.Base(fset.Position(file.Pos()).Filename) == "activity_candidate_ci.go" && name == "mintActivityCandidate") {
					emit(lit, "activity identity construction outside exact mint")
				}
				if lit, ok := n.(*ast.CompositeLit); ok && protectedValue(info.TypeOf(lit)) && !trusted {
					emit(lit, "constructing managed connections or replacement configurations is unproved")
				}
				if star, ok := n.(*ast.StarExpr); ok && info.Types[star].IsValue() && protectedValue(info.TypeOf(star)) && !trusted {
					emit(star, "manual connection/configuration value copy is forbidden; use configuration Copy")
				}
				if call, ok := n.(*ast.CallExpr); ok && info.Types[call.Fun].IsType() && (protectedValue(info.TypeOf(call)) || (len(call.Args) > 0 && sensitiveDynamic(info.TypeOf(call.Args[0])))) && !trusted {
					emit(call, "conversion across the connection/configuration boundary")
				}

				if !trusted {
					switch x := n.(type) {
					case *ast.CallExpr:
						if id, ok := x.Fun.(*ast.Ident); ok {
							if builtin, ok := info.Uses[id].(*types.Builtin); ok && (builtin.Name() == "new" || builtin.Name() == "make") && sensitiveDynamic(info.TypeOf(x)) {
								emit(x, "allocation of protected database value")
							}
						}
						if sig := callableSignature(info.TypeOf(x.Fun)); sig != nil {
							for i, actual := range expandedValues(x.Args, info) {
								arg := actual.expr
								j := i
								if j >= sig.Params().Len() {
									j = sig.Params().Len() - 1
								}
								if j < 0 {
									continue
								}
								dest := sig.Params().At(j).Type()
								if sig.Variadic() && j == sig.Params().Len()-1 && !x.Ellipsis.IsValid() {
									dest = dest.(*types.Slice).Elem()
								}
								if dynamicEscape(actual.typ, dest) && !scanOnlyCall(x, i, actual.typ, dest, scanners, info) {
									emit(arg, "protected database value passed through dynamic interface")
								}
							}
						}
					case *ast.AssignStmt:
						for _, lhs := range x.Lhs {
							if sel, ok := lhs.(*ast.SelectorExpr); ok {
								if selection := info.Selections[sel]; selection != nil && protectedConfig(selection.Recv()) {
									emit(lhs, "configuration field replacement is forbidden")
								}
							}
						}
						values := expandedValues(x.Rhs, info)
						if len(x.Lhs) == len(values) {
							for i, actual := range values {
								rhs := actual.expr
								if dynamicEscape(actual.typ, info.TypeOf(x.Lhs[i])) {
									emit(rhs, "protected database value assigned to dynamic interface")
								}
							}
						}
					case *ast.ValueSpec:
						if x.Type != nil && interfaceType(info.TypeOf(x.Type)) {
							for _, actual := range expandedValues(x.Values, info) {
								v := actual.expr
								if dynamicEscape(actual.typ, info.TypeOf(x.Type)) {
									emit(v, "protected database value initialized through dynamic interface")
								}
							}
						}
					case *ast.ReturnStmt:
						sig := enclosingSignature(ancestors, info)
						values := expandedValues(x.Results, info)
						if sig != nil && sig.Results().Len() == len(values) {
							for i, actual := range values {
								result := actual.expr
								if dynamicEscape(actual.typ, sig.Results().At(i).Type()) {
									emit(result, "protected database value returned through dynamic interface")
								}
							}
						}
					case *ast.SendStmt:
						if ch, ok := info.TypeOf(x.Chan).Underlying().(*types.Chan); ok && dynamicEscape(info.TypeOf(x.Value), ch.Elem()) {
							emit(x.Value, "protected database value sent through dynamic channel")
						}
					case *ast.TypeAssertExpr:
						if sensitiveDynamic(info.TypeOf(x)) {
							emit(x, "protected database value recovered from dynamic interface")
						}
					case *ast.CompositeLit:
						for i, elt := range x.Elts {
							value := elt
							var key ast.Expr
							if kv, ok := elt.(*ast.KeyValueExpr); ok {
								value, key = kv.Value, kv.Key
							}
							if dynamicEscape(info.TypeOf(value), literalElement(info.TypeOf(x), key, i)) {
								emit(value, "protected database value stored in dynamic element")
							}
						}

					}
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
	return isNamed(t, pgx, "Conn") || isNamed(t, pgx+"/pgconn", "PgConn") || isNamed(t, pgx+"/pgconn", "HijackedConn")
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

func interfaceType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}
func sensitiveDynamic(t types.Type) bool {
	return protectedValue(t) || isNamed(t, pgx, "Tx") || isNamed(t, pgx, "Rows") || isNamed(t, pgx, "Row")
}

func dynamicEscape(from, to types.Type) bool {
	return interfaceType(to) && sensitiveDynamic(from) && !types.Identical(from, to)
}

func safeSystemObject(path, name string) bool {
	return (path == module+"/cmd/adtr" && name == "SIGTERM") || (path == module+"/internal/systemhealth" && (name == "Statfs" || name == "Statfs_t" || name == "Stat_t"))
}
func transportConstructor(path string, obj types.Object) bool {
	name := obj.Name()
	if path == "net" || path == "crypto/tls" {
		return strings.HasPrefix(name, "Dial") || strings.HasPrefix(name, "Listen") || name == "Client" || name == "FileConn" || name == "FilePacketConn"
	}
	if path == "net/http" {
		if fn, ok := obj.(*types.Func); ok {
			if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
				recv := sig.Recv().Type()
				if !isNamed(recv, "net/http", "Client") && !isNamed(recv, "net/http", "Transport") && !isNamed(recv, "net/http", "Server") && !isNamed(recv, "net/http", "RoundTripper") {
					return false
				}
			}
		}
		switch name {
		case "Get", "Head", "Post", "PostForm", "Do", "RoundTrip", "ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS", "DefaultTransport", "DefaultClient":
			return true
		}
	}
	return false
}
func reviewedTransport(path, file, function, imported, object string) bool {
	if path == module+"/internal/runtime" && file == "health.go" && function == "Serve" && imported == "net/http" && object == "ListenAndServe" {
		return true
	}
	if path == module+"/cmd/adtr" && file == "main.go" && function == "run" && imported == "net/http" {
		return object == "Get"
	}
	if path != module+"/internal/ldapconnection" {
		return false
	}
	if imported == "crypto/tls" {
		return file == "connection.go" && function == "withBoundConnection" && object == "Client"
	}
	if imported != "net" || (object != "Dialer" && object != "DialContext") {
		return false
	}
	return map[string]string{"probe.go": "Probe", "directory.go": "ReadDirectory", "directory_v2.go": "ReadDirectoryV2", "directory_v3.go": "ReadDirectoryV3", "directory_v4.go": "ReadDirectoryV4"}[file] == function
}

func enclosingSignature(nodes []ast.Node, info *types.Info) *types.Signature {
	for i := len(nodes) - 1; i >= 0; i-- {
		switch n := nodes[i].(type) {
		case *ast.FuncDecl:
			if obj := info.Defs[n.Name]; obj != nil {
				sig, _ := obj.Type().(*types.Signature)
				return sig
			}
		case *ast.FuncLit:
			sig, _ := info.TypeOf(n).(*types.Signature)
			return sig
		}
	}
	return nil
}
func literalElement(t types.Type, key ast.Expr, index int) types.Type {
	if t == nil {
		return nil
	}
	switch t := t.Underlying().(type) {
	case *types.Slice:
		return t.Elem()
	case *types.Array:
		return t.Elem()
	case *types.Map:
		return t.Elem()
	case *types.Struct:
		if key == nil && index < t.NumFields() {
			return t.Field(index).Type()
		}
		if id, ok := key.(*ast.Ident); ok {
			for i := 0; i < t.NumFields(); i++ {
				if t.Field(i).Name() == id.Name {
					return t.Field(i).Type()
				}
			}
		}
	}
	return nil
}

func configurationReadField(name string) bool {
	return name == "Host" || name == "Port" || name == "TLSConfig" || name == "Fallbacks"
}

func protectedActivityOperation(path, name string) bool {
	return (path == module+"/internal/tasks" && (name == "mintActivityCandidate" || name == "BindActivityCandidate")) || (path == managed && name == "RegisterActivityIdentity")
}
func allowedActivityCaller(path, file, function, target, operation string) bool {
	switch operation {
	case "mintActivityCandidate":
		return target == module+"/internal/tasks" && path == target && file == "activity_candidate_ci.go" && function == "Engine.withActivityCandidate"
	case "BindActivityCandidate":
		return target == module+"/internal/tasks" && path == module+"/internal/systemhealth" && file == "persistence_store.go" && function == "Store.withActivityTx"
	case "RegisterActivityIdentity":
		return target == managed && path == module+"/internal/tasks" && file == "activity_candidate_ci.go" && function == "BindActivityCandidate"
	}
	return false
}

type typedExpression struct {
	expr ast.Expr
	typ  types.Type
}

func expandedValues(expressions []ast.Expr, info *types.Info) []typedExpression {
	var out []typedExpression
	for _, e := range expressions {
		if tuple, ok := info.TypeOf(e).(*types.Tuple); ok && len(expressions) == 1 {
			for i := 0; i < tuple.Len(); i++ {
				out = append(out, typedExpression{e, tuple.At(i).Type()})
			}
		} else {
			out = append(out, typedExpression{e, info.TypeOf(e)})
		}
	}
	return out
}

func callableSignature(t types.Type) *types.Signature {
	if t == nil {
		return nil
	}
	sig, _ := t.Underlying().(*types.Signature)
	return sig
}

// Narrowing Rows to Row is safe only for a statically resolved local scanner
// whose parameter is used solely by synchronous, direct Scan calls. Checking
// every typed parameter reference rejects aliases, assertions, storage, return,
// forwarding and closure capture; method-set compatibility alone is insufficient.
func scanOnlyParameters(files []*ast.File, info *types.Info) map[types.Object]map[int]bool {
	out := map[types.Object]map[int]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			obj := info.Defs[fn.Name]
			if obj == nil {
				continue
			}
			sig := callableSignature(obj.Type())
			if sig == nil || sig.Recv() != nil {
				continue
			}
			for i := 0; i < sig.Params().Len(); i++ {
				param := sig.Params().At(i)
				if !isNamed(param.Type(), pgx, "Row") {
					continue
				}
				valid, uses := true, 0
				var stack []ast.Node
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if n == nil {
						stack = stack[:len(stack)-1]
						return false
					}
					stack = append(stack, n)
					id, ok := n.(*ast.Ident)
					if !ok || info.Uses[id] != param {
						return true
					}
					uses++
					if len(stack) < 3 {
						valid = false
						return true
					}
					sel, selector := stack[len(stack)-2].(*ast.SelectorExpr)
					call, calling := stack[len(stack)-3].(*ast.CallExpr)
					if !selector || !calling || sel.X != id || call.Fun != sel || sel.Sel.Name != "Scan" || call.Ellipsis.IsValid() {
						valid = false
						return true
					}
					selection := info.Selections[sel]
					if selection == nil || selection.Kind() != types.MethodVal || !isNamed(selection.Recv(), pgx, "Row") {
						valid = false
					}
					for _, ancestor := range stack {
						switch ancestor.(type) {
						case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
							valid = false
						}
					}
					return true
				})
				if valid && uses > 0 {
					if out[obj] == nil {
						out[obj] = map[int]bool{}
					}
					out[obj][i] = true
				}
			}
		}
	}
	return out
}
func scanOnlyCall(call *ast.CallExpr, index int, from, to types.Type, scanners map[types.Object]map[int]bool, info *types.Info) bool {
	if !isNamed(from, pgx, "Rows") || !isNamed(to, pgx, "Row") {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	return scanners[info.Uses[id]][index]
}

// These are read-only filesystem observations, not syscall entrypoints. Keep
// receiver type, field and reviewed source location together; same-named fields
// or mutations in other code must not inherit this exception.
func safeSystemField(path, file, function string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path != module+"/internal/systemhealth" || len(ancestors) < 2 {
		return false
	}
	sel, ok := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
	if !ok || sel.Sel != id {
		return false
	}
	selection := info.Selections[sel]
	if selection == nil || selection.Kind() != types.FieldVal {
		return false
	}
	allowed := false
	if file == "cgroup_linux.go" && function == "cgroupIdentity" {
		allowed = (isNamed(selection.Recv(), "syscall", "Stat_t") && (id.Name == "Dev" || id.Name == "Ino" || id.Name == "Ctim")) || (isNamed(selection.Recv(), "syscall", "Timespec") && (id.Name == "Sec" || id.Name == "Nsec"))
	}
	if file == "platform_linux.go" && function == "statFS" && isNamed(selection.Recv(), "syscall", "Statfs_t") {
		allowed = id.Name == "Bsize" || id.Name == "Blocks" || id.Name == "Bfree" || id.Name == "Bavail" || id.Name == "Type"
	}
	if !allowed {
		return false
	}
	contains := func(n ast.Node) bool { return n != nil && n.Pos() <= sel.Pos() && n.End() >= sel.End() }
	for _, ancestor := range ancestors {
		switch n := ancestor.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if contains(lhs) {
					return false
				}
			}
		case *ast.IncDecStmt:
			if contains(n.X) {
				return false
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND && contains(n.X) {
				return false
			}
		case *ast.RangeStmt:
			if contains(n.Key) || contains(n.Value) {
				return false
			}
		}
	}
	return true
}
