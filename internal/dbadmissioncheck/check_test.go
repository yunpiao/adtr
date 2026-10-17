package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

type fixtureImporter map[string]*types.Package

func (i fixtureImporter) Import(path string) (*types.Package, error) { return i[path], nil }
func fixtureCheck(t *testing.T, path, filename, source string, imp fixtureImporter) (*types.Package, []Diagnostic) {
	t.Helper()
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, filename, source, 0)
	if e != nil {
		t.Fatal(e)
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	cfg := types.Config{Importer: imp}
	p, e := cfg.Check(path, fs, []*ast.File{f}, info)
	if e != nil {
		t.Fatalf("fixture must type-check before analysis: %v\n%s", e, source)
	}
	return p, Check(fs, path, []*ast.File{f}, info)
}
func fixtures(t *testing.T) fixtureImporter {
	t.Helper()
	imp := fixtureImporter{}
	p, _ := fixtureCheck(t, pgx+"/pgconn", "pgconn.go", `package pgconn
 type Config struct { RuntimeParams map[string]string }
 type PgConn struct{}
 func Connect(string)(*PgConn,error){return nil,nil}
 func ConnectConfig(*Config)(*PgConn,error){return nil,nil}
 func (p *PgConn) Close()error{return nil}
 func (p *PgConn) CleanupDone()<-chan struct{}{return nil}
 `, imp)
	imp[pgx+"/pgconn"] = p
	p, _ = fixtureCheck(t, pgx, "pgx.go", `package pgx
 import "github.com/jackc/pgx/v5/pgconn"
 type ConnConfig struct { pgconn.Config; Tracer any }
 func(c *ConnConfig)Copy()*ConnConfig{return c}
 type Conn struct{}
 func Connect(string)(*Conn,error){return nil,nil}
 func ConnectConfig(*ConnConfig)(*Conn,error){return nil,nil}
 func ParseConfig(string)(*ConnConfig,error){return nil,nil}
 func(c *Conn)Close()error{return nil}
 func(c *Conn)IsClosed()bool{return false}
 func(c *Conn)PgConn()*pgconn.PgConn{return nil}
 type Tx interface {Conn()*Conn; Commit()error}
 `, imp)
	imp[pgx] = p
	p, _ = fixtureCheck(t, managed, "managed.go", `package dbadmission
 import "github.com/jackc/pgx/v5"
 type Conn struct{ raw *pgx.Conn }
 func ConnectConfig(*pgx.ConnConfig)(*Conn,error){return nil,nil}
 func(c *Conn)Close()error{return nil}
 `, imp)
	imp[managed] = p
	return imp
}
func TestTypedBoundaryCases(t *testing.T) {
	imp := fixtures(t)
	cases := []struct {
		name, source string
		bad          bool
	}{
		{"renamed import", `import p "github.com/jackc/pgx/v5";func f(){_,_=p.Connect("")}`, true},
		{"dot import", `import . "github.com/jackc/pgx/v5";func f(){_,_=Connect("")}`, true},
		{"function alias", `import p "github.com/jackc/pgx/v5";var connect=p.ConnectConfig`, true},
		{"wrapped connect", `import p "github.com/jackc/pgx/v5";func wrap(c *p.ConnConfig){f:=p.ConnectConfig;_,_=f(c)}`, true},
		{"low level connect", `import p "github.com/jackc/pgx/v5/pgconn";var connect=p.Connect`, true},
		{"low level config", `import p "github.com/jackc/pgx/v5/pgconn";var connect=p.ConnectConfig`, true},
		{"raw close method expression", `import p "github.com/jackc/pgx/v5";var close=(*p.Conn).Close`, true},
		{"raw pgconn", `import p "github.com/jackc/pgx/v5";func f(c *p.Conn){_=c.PgConn}`, true},
		{"transaction method value", `import p "github.com/jackc/pgx/v5";func f(tx p.Tx){_=tx.Conn}`, true},
		{"interface conversion escape", `import p "github.com/jackc/pgx/v5";func f(tx p.Tx){_=any(tx).(interface{Conn()*p.Conn}).Conn()}`, true},
		{"alias type escape", `import p "github.com/jackc/pgx/v5";type Alias=p.Conn;func f(c *Alias){_=c.Close}`, true},
		{"parse config alias", `import p "github.com/jackc/pgx/v5";var parse=p.ParseConfig`, true},
		{"replace tracer", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){c.Tracer=nil}`, true},
		{"copied config field mutation", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){d:=c.Copy();d.RuntimeParams=nil}`, true},
		{"manual config copy", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){d:=*c;_=d}`, true},
		{"config literal", `import p "github.com/jackc/pgx/v5";var c=p.ConnConfig{}`, true},
		{"config conversion", `import p "github.com/jackc/pgx/v5";type local p.ConnConfig;func f(c *local){_=(*p.ConnConfig)(c)}`, true},
		{"managed value copy", `import m "github.com/yunpiao/adtr/internal/dbadmission";func f(c *m.Conn){d:=*c;_=d}`, true},
		{"managed alias value copy", `import m "github.com/yunpiao/adtr/internal/dbadmission";type alias=m.Conn;func f(c *alias){_=*c}`, true},
		{"inferred generic managed copy", `import m "github.com/yunpiao/adtr/internal/dbadmission";func clone[T any](v *T)T{return *v};func f(c *m.Conn){_=clone(c)}`, true},
		{"explicit generic config copy", `import p "github.com/jackc/pgx/v5";func clone[T any](v *T)T{return *v};func f(c *p.ConnConfig){_=clone[p.ConnConfig](c)}`, true},
		{"managed conversion", `import m "github.com/yunpiao/adtr/internal/dbadmission";type local m.Conn;func f(c *local){_=(*m.Conn)(c)}`, true},
		{"config copy admitted", `import p "github.com/jackc/pgx/v5";import m "github.com/yunpiao/adtr/internal/dbadmission";func f(c *p.ConnConfig){conn,_:=m.ConnectConfig(c.Copy());defer conn.Close()}`, false},
		{"managed method value", `import m "github.com/yunpiao/adtr/internal/dbadmission";func f(c *m.Conn){close:=c.Close;_=close()}`, false},
		{"ordinary same names", `type Conn struct{};func(*Conn)Close(){};func Connect(){};func f(c *Conn){Connect();c.Close()}`, false},
		{"ordinary struct copy", `type Config struct{N int};func f(c *Config){d:=*c;_=d}`, false},
		{"ordinary transaction operation", `import p "github.com/jackc/pgx/v5";func f(tx p.Tx){_=tx.Commit()}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ds := fixtureCheck(t, module+"/internal/example", "example.go", "package example\n"+tc.source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v; diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestWrapperAllowanceIsExact(t *testing.T) {
	imp := fixtures(t)
	src := `package dbadmission;import p "github.com/jackc/pgx/v5";func ConnectConfig(c *p.ConnConfig){f:=p.ConnectConfig;_,_=f(c)}`
	for _, tc := range []struct {
		path, file, source string
		bad                bool
	}{
		{managed, "conn.go", src, false},
		{managed, "other.go", src, true},
		{module + "/internal/other", "conn.go", src, true},
		{managed, "conn.go", strings.ReplaceAll(src, "func ConnectConfig(", "func Other("), true},
	} {
		_, ds := fixtureCheck(t, tc.path, tc.file, tc.source, imp)
		if (len(ds) > 0) != tc.bad {
			t.Fatalf("path=%s file=%s bad=%v: %v", tc.path, tc.file, tc.bad, ds)
		}
	}
}
func TestNilTransactionConnStubIsNotAnEscape(t *testing.T) {
	imp := fixtures(t)
	src := `package tasks;import p "github.com/jackc/pgx/v5";type executorTx struct{};func(executorTx)Conn()*p.Conn{return nil}`
	_, ds := fixtureCheck(t, module+"/internal/tasks", "fenced_work.go", src, imp)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	_, ds = fixtureCheck(t, module+"/internal/tasks", "fenced_work.go", strings.ReplaceAll(src, "return nil", "var c *p.Conn;return c"), imp)
	if len(ds) == 0 {
		t.Fatal("nontrivial stub must fail")
	}
}

func TestManagedCloseAllowance(t *testing.T) {
	imp := fixtures(t)
	source := `package dbadmission;import p "github.com/jackc/pgx/v5";type Conn struct{raw *p.Conn};func(c *Conn)Close(){_=c.raw.IsClosed();_=c.raw.Close();_=c.raw.PgConn().CleanupDone()}`
	_, ds := fixtureCheck(t, managed, "conn.go", source, imp)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	_, ds = fixtureCheck(t, managed, "conn.go", strings.ReplaceAll(source, "func(c *Conn)Close()", "func(c *Conn)Other()"), imp)
	if len(ds) == 0 {
		t.Fatal("raw methods outside exact wrapper must fail")
	}
}
func TestStoreReadyUnknownDependenciesFail(t *testing.T) {
	for _, source := range []string{
		`package store;func Ready(){helper()};func helper(){}`,
		`package store;var callback=func(){};func Ready(){callback()}`,
		`package store;func Ready(){};func init(){}`,
	} {
		_, ds := fixtureCheck(t, module+"/internal/store", "store.go", source, fixtureImporter{})
		if len(ds) == 0 {
			t.Fatalf("unproved dependency accepted: %s", source)
		}
	}
}
