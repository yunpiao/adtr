package dbadmission

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// A single return of the native call preserves CommandTag and error identity;
// no injected transport or alternate production connection path is needed.
func TestExecIsExactNativeDelegate(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "conn.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Exec" {
			continue
		}
		if fn.Body == nil || len(fn.Body.List) != 1 {
			t.Fatal("Exec must only return the native call")
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			t.Fatal("Exec must preserve both native results")
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 3 || !call.Ellipsis.IsValid() {
			t.Fatal("Exec must forward context, SQL and variadic args")
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != "Exec" {
			t.Fatal("Exec must call native Exec")
		}
		raw, ok := method.X.(*ast.SelectorExpr)
		if !ok || raw.Sel.Name != "raw" {
			t.Fatal("Exec must use owned native connection")
		}
		recv, ok := raw.X.(*ast.Ident)
		if !ok || recv.Name != "c" {
			t.Fatal("Exec receiver changed")
		}
		for i, want := range []string{"ctx", "sql", "args"} {
			id, ok := call.Args[i].(*ast.Ident)
			if !ok || id.Name != want {
				t.Fatalf("argument %d altered", i)
			}
		}
		return
	}
	t.Fatal("Exec delegate missing")
}
