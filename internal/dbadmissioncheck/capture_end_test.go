package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

func captureFixture(t *testing.T) (string, fixtureImporter) {
	t.Helper()
	imp := witnessFixtureImports(t)
	for path, source := range map[string]string{
		"encoding/binary": `package binary;type endian struct{};var BigEndian endian;func(endian)Uint64([]byte)uint64{return 0}`,
		"os":              `package os;type File struct{};type FileMode uint32;const ModeNamedPipe FileMode=1;type FileInfo interface{Mode()FileMode};func(*File)Stat()(FileInfo,error){return nil,nil};func(*File)Fd()uintptr{return 0};func(*File)Close()error{return nil}`,
		"sync":            `package sync;type Mutex struct{};func(*Mutex)Lock(){};func(*Mutex)Unlock(){};type Once struct{};func(*Once)Do(func()){};type WaitGroup struct{};func(*WaitGroup)Wait(){}`,
		"syscall":         `package syscall;type Errno int;func(Errno)Error()string{return "error"};const(EAGAIN Errno=1;EWOULDBLOCK Errno=1);func SetNonblock(int,bool)error{return nil};func Read(int,[]byte)(int,error){return 0,nil}`,
	} {
		p, _ := fixtureCheck(t, path, "fixture.go", source, imp)
		imp[path] = p
	}
	data, err := os.ReadFile("../dbadmission/capture_end_ci_linux_amd64.go")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "import (", "import (\n \"sync\"", 1) + `
 var errSealProtocol error
 type invalidationReason string
 const invalidExplicit invalidationReason="explicit_invalidation"
 type activityRegistration struct{generation uint64}
 type lifecycleTicket struct{gate *lifecycleGate;generation uint64;state byte;registration *activityRegistration}
 type sealAttempt struct{captureEnded,released bool;epoch uint64;target *lifecycleTicket;registration *activityRegistration}
 type lifecycleGate struct{mu sync.Mutex;writeFinalInvalidationDiagnostic func();seal *sealAttempt;invalid,closing bool;captureEndFD int;nonce [16]byte;pending,open int;invalidateBecauseLocked func(invalidationReason);notifyLocked func()}
 `
	return source, imp
}
func checkCaptureFiles(t *testing.T, sources map[string]string, imp fixtureImporter) []Diagnostic {
	t.Helper()
	fs := token.NewFileSet()
	var files []*ast.File
	for name, source := range sources {
		f, err := parser.ParseFile(fs, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}, Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Importer: imp}
	if _, err := config.Check(managed, fs, files, info); err != nil {
		t.Fatal(err)
	}
	return Check(fs, managed, files, info)
}
func TestCaptureEndReaderIsExact(t *testing.T) {
	original, imp := captureFixture(t)
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"reviewed frame", captureEndSource, original, false},
		{"wrong source file", "other.go", original, true},
		{"short buffer", captureEndSource, strings.Replace(original, "[49]byte", "[48]byte", 1), true},
		{"wrong frame size", captureEndSource, strings.Replace(original, "n != 48", "n != 47", 1), true},
		{"wrong binding", captureEndSource, strings.Replace(original, "s.registration.generation", "s.target.generation", 1), true},
		{"reset authorization", captureEndSource, strings.Replace(original, "s.captureEnded = true", "s.captureEnded = false", 1), true},
		{"blocking descriptor", captureEndSource, strings.Replace(original, "SetNonblock(fd, true)", "SetNonblock(fd, false)", 1), true},
		{"skip FIFO", captureEndSource, strings.Replace(original, "info.Mode()&os.ModeNamedPipe == 0", "info.Mode()&os.ModeNamedPipe != 0", 1), true},
		{"external field write", captureEndSource, original + `func other(s *sealAttempt){s.captureEnded=true}`, true},
		{"descriptor escape", captureEndSource, original + `func other(g *lifecycleGate)int{return g.captureEndFD}`, true},
		{"preauthorized seal", captureEndSource, original + `var other=sealAttempt{captureEnded:true}`, true},
		{"positional seal", captureEndSource, original + `var other=sealAttempt{true,false,0,nil,nil}`, true},
		{"whole seal copy", captureEndSource, original + `func other(s *sealAttempt){_=*s}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.file == captureEndSource && tc.source == original {
				t.Fatal("mutation unchanged")
			}
			ds := checkCaptureFiles(t, map[string]string{tc.file: tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
func TestCaptureEndConsumersAreLimited(t *testing.T) {
	original, imp := captureFixture(t)
	for _, tc := range []struct {
		name, file, source string
		bad                bool
	}{
		{"clean close", "lifecycle_ci.go", `func(t *lifecycleTicket)closedObserved(){_=t.gate.captureObligationsEndedLocked()}`, false},
		{"closing", "lifecycle_ci.go", `func(t *lifecycleTicket)closingConnection(){_=t.gate.captureObligationsEndedLocked()}`, false},
		{"revoke closure", "seal_ci.go", `func(g *lifecycleGate)registerActivityRow()func(){return func(){_=g.captureObligationsEndedLocked()}}`, false},
		{"wait cancel", "seal_ci.go", `func(g *lifecycleGate)invalidateWaitLocked(){_=g.captureObligationsEndedLocked()}`, false},
		{"hard failure", "seal_ci.go", `func(g *lifecycleGate)invalidateTransport(){_=g.captureObligationsEndedLocked()}`, true},
		{"wrong file", "other.go", `func(g *lifecycleGate)invalidateWaitLocked(){_=g.captureObligationsEndedLocked()}`, true},
		{"method alias", "seal_ci.go", `func(g *lifecycleGate)invalidateWaitLocked(){f:=g.captureObligationsEndedLocked;_=f()}`, true},
		{"async", "seal_ci.go", `func(g *lifecycleGate)invalidateWaitLocked(){go g.captureObligationsEndedLocked()}`, true},
		{"defer", "seal_ci.go", `func(g *lifecycleGate)invalidateWaitLocked(){defer g.captureObligationsEndedLocked()}`, true},
		{"startup prepare", "application_controller_ci.go", `import "os";func StartCIController(f *os.File){_,_=prepareCaptureEnd(f)}`, false},
		{"prepare alias", "application_controller_ci.go", `func StartCIController(){_=prepareCaptureEnd}`, true},
		{"prepare go", "application_controller_ci.go", `import "os";func StartCIController(f *os.File){go prepareCaptureEnd(f)}`, true},
		{"prepare defer", "application_controller_ci.go", `import "os";func StartCIController(f *os.File){defer prepareCaptureEnd(f)}`, true},
		{"prepare closure", "application_controller_ci.go", `import "os";func StartCIController(f *os.File){func(){_,_=prepareCaptureEnd(f)}()}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := checkCaptureFiles(t, map[string]string{captureEndSource: original, tc.file: "package dbadmission;" + tc.source}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}

func TestCaptureEndDescriptorCleanupIsExact(t *testing.T) {
	original, imp := captureFixture(t)
	prefix := `package dbadmission;import("os";"sync");type stopper struct{};func(*stopper)Store(bool){};func StartCIController(g *lifecycleGate,normal,rescue,captureEnd *os.File){var stopOnce,reportOnce sync.Once;var wg sync.WaitGroup;var stopping stopper;cleanup:=func()`
	suffix := `;cleanup()}`
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"serialized cleanup", captureEndCleanupBody, false},
		{"nonzero reset", strings.Replace(captureEndCleanupBody, "g.captureEndFD = 0", "g.captureEndFD = 9", 1), true},
		{"close before reset", strings.Replace(captureEndCleanupBody, "g.captureEndFD = 0\n captureEnd.Close()", "captureEnd.Close()\n g.captureEndFD = 0", 1), true},
		{"unlock before close", strings.Replace(captureEndCleanupBody, "captureEnd.Close()\n g.mu.Unlock()", "g.mu.Unlock()\n captureEnd.Close()", 1), true},
		{"extra field read", strings.Replace(captureEndCleanupBody, "g.captureEndFD = 0", "_=g.captureEndFD;g.captureEndFD = 0", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bad && tc.body == captureEndCleanupBody {
				t.Fatal("mutation unchanged")
			}
			ds := checkCaptureFiles(t, map[string]string{captureEndSource: original, "application_controller_ci.go": prefix + tc.body + suffix}, imp)
			if (len(ds) > 0) != tc.bad {
				t.Fatalf("bad=%v diagnostics=%v", tc.bad, ds)
			}
		})
	}
}
