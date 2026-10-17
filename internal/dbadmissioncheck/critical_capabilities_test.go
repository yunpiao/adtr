package dbadmissioncheck

import (
	"go/types"
	"os"
	"strings"
	"testing"
)

func witnessFixtureImports(t *testing.T) fixtureImporter {
	t.Helper()
	imp := fixtureImporter{"unsafe": types.Unsafe}
	packages := []struct{ path, source string }{
		{"os", `package os;type File struct{};type FileInfo interface{Size()int64};func(*File)Stat()(FileInfo,error){return nil,nil};func(*File)Fd()uintptr{return 0}`},
		{"bytes", `package bytes;func Equal([]byte,[]byte)bool{return true}`},
		{"encoding/binary", `package binary;type endian struct{};var LittleEndian endian;func(endian)Uint32([]byte)uint32{return 0};func(endian)Uint64([]byte)uint64{return 0}`},
		{"syscall", `package syscall;const(PROT_READ=1;PROT_WRITE=2;MAP_SHARED=3);func Mmap(int,int64,int,int,int)([]byte,error){return nil,nil};func SetNonblock(int,bool)error{return nil}`},
		{"sync/atomic", `package atomic;func LoadUint32(*uint32)uint32{return 0};func CompareAndSwapUint32(*uint32,uint32,uint32)bool{return true};func StoreUint32(*uint32,uint32){}`},
	}
	for _, entry := range packages {
		p, _ := fixtureCheck(t, entry.path, "fixture.go", entry.source, imp)
		imp[entry.path] = p
	}
	return imp
}
func TestSharedWitnessCapabilitiesAreExact(t *testing.T) {
	imp := witnessFixtureImports(t)
	source, err := os.ReadFile("../dbadmission/witness_ci_linux_amd64.go")
	if err != nil {
		t.Fatal(err)
	}
	original := string(source) + "\nvar errSealProtocol error\n"
	cases := []struct {
		name, file, source string
		bad                bool
	}{
		{"reviewed mapper", witnessSource, original, false},
		{"wrong source file", "other.go", original, true},
		{"offset drift", witnessSource, strings.ReplaceAll(original, "&page[24]", "&page[25]"), true},
		{"mapping size drift", witnessSource, strings.Replace(original, "0, 4096, syscall.PROT_READ", "0, 8192, syscall.PROT_READ", 1), true},
		{"header validation drift", witnessSource, strings.Replace(original, "len(page) != 4096", "len(page) == 4096", 1), true},
		{"alignment drift", witnessSource, strings.ReplaceAll(original, "%4", "%8"), true},
		{"direct sticky pointer writer", witnessSource, original + "\nfunc other(w *ciWitness){*w.invalid=0}\n", true},
		{"page escape", witnessSource, original + "\nfunc other(w *ciWitness)[]byte{return w.page}\n", true},
		{"sticky reset", witnessSource, strings.ReplaceAll(original, "atomic.StoreUint32(w.invalid, 1)", "atomic.StoreUint32(w.invalid, 0)"), true},
		{"bound writer targets invalid", witnessSource, strings.ReplaceAll(original, "atomic.CompareAndSwapUint32(bound, 0, 1)", "atomic.CompareAndSwapUint32(invalid, 0, 1)"), true},
		{"atomic function alias", witnessSource, original + "\nvar store=atomic.StoreUint32\n", true},
		{"unsafe type alias", witnessSource, original + "\ntype unsafeAlias=unsafe.Pointer\n", true},
		{"extra sticky effect", witnessSource, strings.ReplaceAll(original, "atomic.StoreUint32(w.invalid, 1)", "atomic.StoreUint32(w.invalid, 1); _=w.bound"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.file == witnessSource && tc.source == original {
				t.Fatal("mutation did not change fixture")
			}
			_, ds := fixtureCheck(t, managed, tc.file, tc.source, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestStickyInvalidationHasOneOrderedWriter(t *testing.T) {
	original := `package dbadmission
 type invalidationWitness interface{invalidate()}
 type lifecycleGate struct{invalid bool;witness invalidationWitness}
 func invalidateWitness(w invalidationWitness){if w!=nil{w.invalidate()}}
 func(g *lifecycleGate)markInvalidLocked(){invalidateWitness(g.witness);g.invalid=true}
 `
	for _, tc := range []struct {
		name, source string
		bad          bool
	}{
		{"sticky first", original, false},
		{"state before sticky", strings.Replace(original, "invalidateWitness(g.witness);g.invalid=true", "g.invalid=true;invalidateWitness(g.witness)", 1), true},
		{"direct secondary writer", original + `func other(g *lifecycleGate){g.invalid=true}`, true},
		{"state reset", original + `func other(g *lifecycleGate){g.invalid=false}`, true},
		{"field address", original + `func other(g *lifecycleGate){_=&g.invalid}`, true},
		{"whole gate copy", original + `func other(g *lifecycleGate){_=*g}`, true},
		{"initial invalid true", original + `var g=lifecycleGate{invalid:true}`, true},
		{"zero construction", original + `var g=lifecycleGate{}`, false},
		{"dispatcher async", strings.Replace(original, "w.invalidate()", "go w.invalidate()", 1), true},
		{"dispatcher no-op", strings.Replace(original, "w.invalidate()", "_ = w", 1), true},
		{"dispatcher alias", original + `var leak=invalidateWitness`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds := fixtureCheck(t, managed, "lifecycle_ci.go", tc.source, fixtureImporter{})
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestCIControllerHasOnlyTaggedDirectBootstrap(t *testing.T) {
	imp := fixtureImporter{}
	p, _ := fixtureCheck(t, managed, "fixture.go", `package dbadmission;func StartCIController(){}`, imp)
	imp[managed] = p
	for _, tc := range []struct {
		name, file, body string
		bad              bool
	}{
		{"direct call", "db_admission_ci.go", `dbadmission.StartCIController()`, false},
		{"wrong file", "other.go", `dbadmission.StartCIController()`, true},
		{"function alias", "db_admission_ci.go", `f:=dbadmission.StartCIController;f()`, true},
		{"defer installation", "db_admission_ci.go", `defer dbadmission.StartCIController()`, true},
		{"async installation", "db_admission_ci.go", `go dbadmission.StartCIController()`, true},
		{"closure installation", "db_admission_ci.go", `func(){dbadmission.StartCIController()}()`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds := fixtureCheck(t, module+"/cmd/adtr", tc.file, `package main;import "github.com/yunpiao/adtr/internal/dbadmission";func configureCIAdmission(){`+tc.body+`}`, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestOwnedDescriptorNonblockIsExact(t *testing.T) {
	imp := witnessFixtureImports(t)
	for _, tc := range []struct {
		name, file, body string
		bad              bool
	}{
		{"first two owned descriptors", "db_admission_ci.go", `if i<2 && syscall.SetNonblock(fd,true)!=nil{}`, false},
		{"missing index bound", "db_admission_ci.go", `_=syscall.SetNonblock(fd,true)`, true},
		{"blocking mode", "db_admission_ci.go", `if i<2 && syscall.SetNonblock(fd,false)!=nil{}`, true},
		{"wrong file", "other.go", `if i<2 && syscall.SetNonblock(fd,true)!=nil{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ds := fixtureCheck(t, module+"/cmd/adtr", tc.file, `package main;import "syscall";func configureCIAdmission(fd,i int){`+tc.body+`}`, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
