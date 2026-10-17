package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
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
 type HijackedConn struct{}
 func Construct(*HijackedConn)(*PgConn,error){return nil,nil}
 func(p *PgConn)PID()uint32{return 1}
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
 type Row interface {Scan(...any)error}
 type Rows interface {Row;Conn()*Conn}
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

		{"implicit config boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){var x any=c;_=x}`, true},
		{"implicit config assignment", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){var x any;x=c;_=x}`, true},
		{"implicit config argument", `import p "github.com/jackc/pgx/v5";func sink(any){};func f(c *p.ConnConfig){sink(c)}`, true},
		{"variadic config argument", `import p "github.com/jackc/pgx/v5";func sink(...any){};func f(c *p.ConnConfig){sink(1,c)}`, true},
		{"implicit config return", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig)any{return c}`, true},
		{"nested config return", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){_=func()any{return c}}`, true},
		{"config slice boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){_=[]any{c}}`, true},
		{"config map boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){_=map[string]any{"c":c}}`, true},
		{"config struct boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){_=struct{C any}{C:c}}`, true},
		{"config positional boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig){_=struct{C any}{c}}`, true},
		{"config channel boxing", `import p "github.com/jackc/pgx/v5";func f(c *p.ConnConfig,ch chan any){ch<-c}`, true},
		{"transaction dynamic boxing", `import p "github.com/jackc/pgx/v5";func f(tx p.Tx){var x any=tx;_=x}`, true},
		{"transaction same interface", `import p "github.com/jackc/pgx/v5";func sink(p.Tx){};func f(tx p.Tx){sink(tx)}`, false},
		{"type assertion recovery", `import p "github.com/jackc/pgx/v5";func f(x any){_=x.(*p.ConnConfig)}`, true},
		{"config allocation", `import p "github.com/jackc/pgx/v5";func f(){_=new(p.ConnConfig)}`, true},
		{"reconstructed connection alias", `import p "github.com/jackc/pgx/v5/pgconn";var construct=p.Construct`, true},
		{"reconstructed inferred connection", `import p "github.com/jackc/pgx/v5/pgconn";func f(){_,_=p.Construct(nil)}`, true},
		{"tuple return boxing", `import p "github.com/jackc/pgx/v5";func source()(*p.ConnConfig,error){return nil,nil};func f()(any,error){return source()}`, true},
		{"tuple argument boxing", `import p "github.com/jackc/pgx/v5";func source()(*p.ConnConfig,error){return nil,nil};func sink(any,error){};func f(){sink(source())}`, true},
		{"tuple assignment boxing", `import p "github.com/jackc/pgx/v5";func source()(*p.ConnConfig,error){return nil,nil};func f(){var x any;var e error;x,e=source();_,_=x,e}`, true},
		{"named callback boxing", `import p "github.com/jackc/pgx/v5";type callback func(any);func f(c *p.ConnConfig,sink callback){sink(c)}`, true},
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
func TestStoreAllDeclarationsChecked(t *testing.T) {
	imp := fixtures(t)
	for _, source := range []string{
		`package store; import p "github.com/jackc/pgx/v5";func Ready(){};func migrate(){_,_=p.Connect("")}`,
		`package store; import p "github.com/jackc/pgx/v5";func Ready(){};func init(){_,_=p.Connect("")}`,
		`package store; import p "github.com/jackc/pgx/v5";var connect=p.ConnectConfig;func Ready(){}`,
	} {
		_, ds := fixtureCheck(t, module+"/internal/store", "store.go", source, imp)
		if len(ds) == 0 {
			t.Fatal("unchecked store declaration")
		}
	}
}

func TestConfigurationFactoryIsExact(t *testing.T) {
	imp := fixtures(t)
	for _, tc := range []struct {
		path, file, source string
		bad                bool
	}{
		{module + "/internal/runtime", "config.go", `package runtime;import p "github.com/jackc/pgx/v5";func LoadConfig(){_,_=p.ParseConfig("")}`, false},
		{module + "/internal/runtime", "other.go", `package runtime;import p "github.com/jackc/pgx/v5";func LoadConfig(){_,_=p.ParseConfig("")}`, true},
		{module + "/internal/runtime", "config.go", `package runtime;import p "github.com/jackc/pgx/v5";func Other(){_,_=p.ParseConfig("")}`, true},
		{module + "/internal/runtime", "config.go", `package runtime;import p "github.com/jackc/pgx/v5";func LoadConfig(c *p.ConnConfig){c.Tracer=nil}`, true},
	} {
		_, ds := fixtureCheck(t, tc.path, tc.file, tc.source, imp)
		if (len(ds) > 0) != tc.bad {
			t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
		}
	}
}

func TestActivityCallerBoundary(t *testing.T) {
	imp := fixtures(t)
	p, _ := fixtureCheck(t, module+"/internal/tasks", "fixture.go", `package tasks;func BindActivityCandidate(){}`, imp)
	imp[module+"/internal/tasks"] = p
	source := `package systemhealth;import "github.com/yunpiao/adtr/internal/tasks";type Store struct{};func (*Store) withActivityTx(){tasks.BindActivityCandidate()}`
	for _, tc := range []struct {
		path, file, source string
		bad                bool
	}{
		{module + "/internal/systemhealth", "persistence_store.go", source, false},
		{module + "/internal/systemhealth", "other.go", source, true},
		{module + "/internal/auth", "persistence_store.go", source, true},
		{module + "/internal/systemhealth", "persistence_store.go", strings.ReplaceAll(source, "withActivityTx()", "other()"), true},
		{module + "/internal/systemhealth", "persistence_store.go", `package systemhealth;import "github.com/yunpiao/adtr/internal/tasks";var leak=tasks.BindActivityCandidate`, true},
	} {
		_, ds := fixtureCheck(t, tc.path, tc.file, tc.source, imp)
		if (len(ds) > 0) != tc.bad {
			t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
		}
	}
}

func TestUnreviewedTransportAndImports(t *testing.T) {
	imp := fixtures(t)
	for _, path := range []string{"database/sql", "plugin", "os/exec", "unsafe"} {
		p := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
		p.MarkComplete()
		imp[path] = p
		_, ds := fixtureCheck(t, module+"/internal/example", "example.go", "package example;import _ "+strconv.Quote(path), imp)
		if len(ds) == 0 {
			t.Fatalf("unreviewed blank import %s", path)
		}
	}
	p, _ := fixtureCheck(t, "net", "net.go", `package net;func Dial(string,string){}`, imp)
	imp["net"] = p
	_, ds := fixtureCheck(t, module+"/internal/example", "example.go", `package example;import "net";var dial=net.Dial`, imp)
	if len(ds) == 0 {
		t.Fatal("transport alias escaped")
	}
}

func TestRowsToVerifiedLocalScanner(t *testing.T) {
	imp := fixtures(t)
	cases := []struct {
		name, body, call string
		bad              bool
	}{
		{"direct synchronous Scan", `func scan(row p.Row)error{var value int;return row.Scan(&value)}`, `scan(rows)`, false},
		{"multiple columns", `func scan(row p.Row)error{var value int;var name string;return row.Scan(&value,&name)}`, `scan(rows)`, false},
		{"static scanner second parameter", `func scan(n int,row p.Row)error{var value int;return row.Scan(&value,&n)}`, `scan(1,rows)`, false},
		{"scanner function value", `func scan(row p.Row)error{return row.Scan()}`, `func(){alias:=scan;_=alias(rows)}()`, true},
		{"assert Rows", `func scan(row p.Row)error{_=row.(p.Rows);return row.Scan()}`, `scan(rows)`, true},
		{"return row", `func scan(row p.Row)p.Row{return row}`, `scan(rows)`, true},
		{"store row", `var saved p.Row;func scan(row p.Row)error{saved=row;return row.Scan()}`, `scan(rows)`, true},
		{"alias row", `func scan(row p.Row)error{alias:=row;return alias.Scan()}`, `scan(rows)`, true},
		{"method value", `func scan(row p.Row)error{method:=row.Scan;return method()}`, `scan(rows)`, true},
		{"closure capture", `func scan(row p.Row)error{return func()error{return row.Scan()}()}`, `scan(rows)`, true},
		{"goroutine", `func scan(row p.Row)error{go row.Scan();return nil}`, `scan(rows)`, true},
		{"defer", `func scan(row p.Row)error{defer row.Scan();return nil}`, `scan(rows)`, true},
		{"forward variadic", `func sink(...any){};func scan(row p.Row)error{sink(row);return row.Scan()}`, `scan(rows)`, true},
		{"Scan variadic forwarding", `func scan(row p.Row)error{args:=[]any{};return row.Scan(args...)}`, `scan(rows)`, true},
		{"Scan receives row", `func scan(row p.Row)error{return row.Scan(row)}`, `scan(rows)`, true},
		{"Row to any", `func scan(row p.Row)error{var value any=row;_=value;return row.Scan()}`, `scan(rows)`, true},
		{"unused row", `func scan(row p.Row)error{return nil}`, `scan(rows)`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := `package example;import p "github.com/jackc/pgx/v5";` + tc.body + `;func f(rows p.Rows){` + tc.call + `}`
			_, ds := fixtureCheck(t, module+"/internal/example", "example.go", source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestRowRemainsProtectedOutsideScanner(t *testing.T) {
	imp := fixtures(t)
	for _, body := range []string{
		`func f(row p.Row){var x any=row;_=x}`,
		`func f(row p.Row)any{return row}`,
		`func f(row p.Row){_=any(row)}`,
		`func f(row p.Row){_=row.(p.Rows)}`,
	} {
		_, ds := fixtureCheck(t, module+"/internal/example", "example.go", `package example;import p "github.com/jackc/pgx/v5";`+body, imp)
		if len(ds) == 0 {
			t.Fatal("Row escaped dynamic protection")
		}
	}
}

func TestFilesystemObservationFieldsAreExactReadOnly(t *testing.T) {
	imp := fixtures(t)
	p, _ := fixtureCheck(t, "syscall", "syscall.go", `package syscall
 type Timespec struct {Sec,Nsec int64}
 type Stat_t struct {Dev,Ino uint64;Ctim Timespec}
 type Statfs_t struct {Bsize int64;Blocks,Bfree,Bavail uint64;Type int64}
 type Other struct {Bsize int64}
 func Socket(){}
 `, imp)
	imp["syscall"] = p
	cases := []struct {
		name, file, function, body string
		bad                        bool
	}{
		{"stat observations", "cgroup_linux.go", "cgroupIdentity", `var stat syscall.Stat_t;_=stat.Dev;_=stat.Ino;_=stat.Ctim.Sec;_=stat.Ctim.Nsec`, false},
		{"statfs observations", "platform_linux.go", "statFS", `var stat syscall.Statfs_t;_=stat.Bsize;_=stat.Blocks;_=stat.Bfree;_=stat.Bavail;_=stat.Type`, false},
		{"wrong function", "platform_linux.go", "other", `var stat syscall.Statfs_t;_=stat.Bsize`, true},
		{"wrong file", "other.go", "statFS", `var stat syscall.Statfs_t;_=stat.Bsize`, true},
		{"same named other field", "platform_linux.go", "statFS", `var stat syscall.Other;_=stat.Bsize`, true},
		{"field mutation", "platform_linux.go", "statFS", `var stat syscall.Statfs_t;stat.Bsize=1`, true},
		{"field increment", "platform_linux.go", "statFS", `var stat syscall.Statfs_t;stat.Bsize++`, true},
		{"field address", "platform_linux.go", "statFS", `var stat syscall.Statfs_t;_=&stat.Bsize`, true},
		{"nested field mutation", "cgroup_linux.go", "cgroupIdentity", `var stat syscall.Stat_t;stat.Ctim.Sec=1`, true},
		{"nested field address", "cgroup_linux.go", "cgroupIdentity", `var stat syscall.Stat_t;_=&stat.Ctim.Sec`, true},
		{"range field mutation", "platform_linux.go", "statFS", `var stat syscall.Statfs_t;for _,stat.Bsize=range []int64{1}{}`, true},
		{"field literal", "platform_linux.go", "statFS", `_=syscall.Statfs_t{Bsize:1}`, true},
		{"unreviewed syscall", "platform_linux.go", "statFS", `syscall.Socket()`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := `package systemhealth;import "syscall";func ` + tc.function + `(){` + tc.body + `}`
			_, ds := fixtureCheck(t, module+"/internal/systemhealth", tc.file, source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
