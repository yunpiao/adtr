package dbadmissioncheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type listedPackage struct {
	ImportPath, Dir, Export    string
	GoFiles, CgoFiles, Imports []string
}

// This test deliberately uses the running Go toolchain's export data, not a
// text-based approximation of imports or selector names. It runs in CI; it
// neither opens a database nor executes application code.
func TestRepositoryTypedBoundary(t *testing.T) {
	for _, tags := range []string{"", "adtr_ci_dbadmission"} {
		name := tags
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) { checkRepositoryBuild(t, tags) })
	}
}

func checkRepositoryBuild(t *testing.T, tags string) {
	root := filepath.Join("..", "..")
	cmd := exec.Command("go", "list", "-tags="+tags, "-deps", "-export", "-json", "./cmd/adtr")
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			t.Fatalf("load typed dependency graph: %v\n%s", err, e.Stderr)
		}
		t.Fatal(err)
	}
	packages := map[string]listedPackage{}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		packages[p.ImportPath] = p
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		p, ok := packages[path]
		if !ok || p.Export == "" {
			return nil, fmt.Errorf("missing export data for %s", path)
		}
		return os.Open(p.Export)
	})
	queue := []string{module + "/internal/tasks", module + "/internal/schedules", module + "/internal/messages", module + "/internal/systemhealth", module + "/internal/operationallogs", module + "/internal/domains", module + "/internal/store", managed}
	seen := map[string]bool{}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if seen[path] {
			continue
		}
		seen[path] = true
		p, ok := packages[path]
		if !ok {
			t.Fatalf("required boundary package absent: %s", path)
		}
		if len(p.CgoFiles) > 0 {
			t.Fatalf("unproved cgo in %s", path)
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, e := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
			if e != nil {
				t.Fatal(e)
			}
			files = append(files, f)
		}
		info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		conf := types.Config{Importer: imp}
		if _, e := conf.Check(path, fset, files, info); e != nil {
			t.Fatalf("type check %s: %v", path, e)
		}
		for _, d := range Check(fset, path, files, info) {
			t.Error(d.String())
		}
		// Include module imports even when only their init functions are used.
		if path != module+"/internal/store" {
			for _, dep := range p.Imports {
				if strings.HasPrefix(dep, module+"/") {
					queue = append(queue, dep)
				}
			}
		}
		// Follow typed object references in checked declarations. In particular,
		// store's migration-only auth imports do not silently enlarge this slice.
		for _, f := range files {
			for _, decl := range f.Decls {
				if path == module+"/internal/store" {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Name.Name != "Ready" {
						continue
					}
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					o := info.Uses[id]
					if o == nil || o.Pkg() == nil {
						return true
					}
					dep := o.Pkg().Path()
					if dep != path && strings.HasPrefix(dep, module+"/") {
						queue = append(queue, dep)
					}
					return true
				})
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("typed slice packages: %s; store: Ready only; complete worker closure: UNPROVEN", strings.Join(names, ", "))
	if CompleteWorkerClosure {
		t.Fatal("this slice cannot authorize a gate without a separate complete-worker proof")
	}
}

// The bootstrap mixes worker, API, bootstrap and migration branches. Until a
// sound entrypoint/dispatch proof exists, any production wiring change must
// fail for review, including added source files. This tripwire is intentionally
// conservative and is not presented as a dynamic call-graph proof.
func TestWorkerWiringRequiresBoundaryReview(t *testing.T) {
	root := filepath.Join("..", "..")
	manifestBytes, err := os.ReadFile("testdata/worker_wiring.sha256")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(manifestBytes)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatalf("invalid wiring manifest line %q", line)
		}
		expected[parts[1]] = parts[0]
	}
	actual := map[string]string{}
	for _, dir := range []string{"cmd/adtr", "internal/runtime", "internal/store"} {
		entries, e := os.ReadDir(filepath.Join(root, dir))
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			name := filepath.ToSlash(filepath.Join(dir, entry.Name()))
			b, e := os.ReadFile(filepath.Join(root, name))
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(b)
			actual[name] = hex.EncodeToString(sum[:])
		}
	}
	// External dependency/toolchain changes also invalidate this reviewed slice.
	for _, name := range []string{"go.mod", "go.sum"} {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		actual[name] = hex.EncodeToString(sum[:])
	}
	for path, hash := range actual {
		if expected[path] != hash {
			t.Errorf("UNPROVEN worker wiring change %s: review initialization and dynamic dependencies before updating the manifest", path)
		}
	}
	for path := range expected {
		if _, ok := actual[path]; !ok {
			t.Errorf("UNPROVEN removed worker wiring source %s", path)
		}
	}
}
