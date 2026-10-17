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
	ImportPath, Dir, Export                                      string
	GoFiles, CgoFiles, Imports                                   []string
	SFiles, CFiles, CXXFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
	Standard                                                     bool
	Module                                                       *listedModule
}

type listedModule struct {
	Path, Version string
	Replace       *listedModule
}

// Versions are paired with the go.mod/go.sum review tripwire below. Any newly
// imported external module, including a blank-import driver, fails closed.
var reviewedModules = map[string]string{
	"github.com/jackc/pgx/v5":        "v5.11.0",
	"github.com/jackc/pgpassfile":    "v1.0.0",
	"github.com/jackc/pgservicefile": "v0.0.0-20240606120523-5a60cdf6a761",
	"golang.org/x/text":              "v0.29.0",
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
	externalBytes, err := os.ReadFile("testdata/external_packages.txt")
	if err != nil {
		t.Fatal(err)
	}
	external := map[string]bool{}
	for _, path := range strings.Fields(string(externalBytes)) {
		external[path] = true
	}
	queue := []string{module + "/cmd/adtr"}
	for path, p := range packages {
		if strings.HasPrefix(path, module+"/") {
			continue
		}
		if p.Standard {
			continue
		}
		if !reviewedExternal(p, external) {
			t.Fatalf("UNPROVEN external dependency %s: review database constructors and initialization before changing the module allowlist", path)
		}
	}
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
		if foreignCode(p) {
			t.Fatalf("unproved cgo, assembly or foreign code in %s", path)
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
		// The actual production build graph includes blank imports and init dependencies.
		for _, dep := range p.Imports {
			if strings.HasPrefix(dep, module+"/") {
				queue = append(queue, dep)
			}
		}

	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("typed complete binary packages: %s; dynamic/external behavior closure: UNPROVEN", strings.Join(names, ", "))
	if CompleteWorkerClosure {
		t.Fatal("this slice cannot authorize a gate without a separate complete-worker proof")
	}
}

// The entire binary is typed above. These reviewed wiring, factory, transport
// and private-wrapper sources additionally fail closed on changes (including
// added files); typed API membership alone cannot prove their internal behavior.
// This tripwire is not presented as a dynamic call-graph proof.
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
	for _, dir := range []string{"cmd/adtr", "internal/runtime", "internal/store", "internal/ldapconnection", "internal/dbadmission"} {
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
	for _, name := range []string{"go.mod", "go.sum", "internal/tasks/worker.go", "internal/tasks/activity_candidate.go", "internal/tasks/activity_candidate_ci.go", "internal/tasks/activity_candidate_default.go", "internal/systemhealth/persistence_store.go", "internal/systemhealth/persistence_worker.go"} {
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

func reviewedExternal(p listedPackage, packages map[string]bool) bool {
	return !foreignCode(p) && packages[p.ImportPath] && p.Module != nil && p.Module.Replace == nil && reviewedModules[p.Module.Path] == p.Module.Version
}

func TestExternalDependencyReviewFailsClosed(t *testing.T) {
	allowed := map[string]bool{pgx: true}
	cases := []struct {
		p    listedPackage
		want bool
	}{
		{listedPackage{ImportPath: pgx, Module: &listedModule{Path: pgx, Version: "v5.11.0"}}, true},
		{listedPackage{ImportPath: pgx + "/stdlib", Module: &listedModule{Path: pgx, Version: "v5.11.0"}}, false},
		{listedPackage{ImportPath: "example.com/unknown/driver", Module: &listedModule{Path: "example.com/unknown", Version: "v1.0.0"}}, false},
		{listedPackage{ImportPath: pgx}, false},
		{listedPackage{ImportPath: pgx, Module: &listedModule{Path: pgx, Version: "v5.12.0"}}, false},
		{listedPackage{ImportPath: pgx, Module: &listedModule{Path: pgx, Version: "v5.11.0", Replace: &listedModule{Path: "local"}}}, false},
	}
	for _, tc := range cases {
		if got := reviewedExternal(tc.p, allowed); got != tc.want {
			t.Fatalf("dependency %s accepted=%v want=%v", tc.p.ImportPath, got, tc.want)
		}
	}
}

func foreignCode(p listedPackage) bool {
	return len(p.CgoFiles)+len(p.SFiles)+len(p.CFiles)+len(p.CXXFiles)+len(p.SwigFiles)+len(p.SwigCXXFiles)+len(p.SysoFiles) > 0
}
