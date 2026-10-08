//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func validArm() armRecord {
	return armRecord{1, strings.Repeat("a", 32), "default", "12", "domain-1", "account-1", "browser-test-1"}
}

func TestProtocolStrictArm(t *testing.T) {
	a := validArm()
	raw, _ := json.Marshal(a)
	for name, body := range map[string]string{
		"valid":              string(raw),
		"unknown":            strings.TrimSuffix(string(raw), "}") + `,"password":"not-accepted"}`,
		"duplicate":          strings.TrimSuffix(string(raw), "}") + `,"ticket":"` + a.Ticket + `"}`,
		"null":               strings.Replace(string(raw), `"actorId":"12"`, `"actorId":null`, 1),
		"missing":            strings.Replace(string(raw), `"actorId":"12",`, "", 1),
		"trailing":           string(raw) + ` {}`,
		"version-string":     strings.Replace(string(raw), `"version":1`, `"version":"1"`, 1),
		"ticket-case":        strings.Replace(string(raw), a.Ticket, strings.ToUpper(a.Ticket), 1),
		"actor-leading-zero": strings.Replace(string(raw), `"actorId":"12"`, `"actorId":"012"`, 1),
		"actor-overflow":     strings.Replace(string(raw), `"actorId":"12"`, `"actorId":"9223372036854775808"`, 1),
		"path":               strings.Replace(string(raw), "domain-1", "../domain-1", 1),
		"reserved-key":       strings.Replace(string(raw), "browser-test-1", "schedule_1", 1),
		"oversize":           string(raw) + strings.Repeat(" ", 4096),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "barrier-arm.json"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			got, exists, err := readArm(dir)
			if name == "valid" {
				if err != nil || !exists || got != a {
					t.Fatal("valid arm rejected")
				}
			} else if err == nil {
				t.Fatal("invalid arm accepted")
			}
		})
	}
}

func TestProtocolMarkerAndSymlink(t *testing.T) {
	dir := t.TempDir()
	a := validArm()
	for _, m := range []marker{{2, a.Ticket, "release"}, {1, strings.Repeat("b", 32), "release"}, {1, a.Ticket, "reached"}} {
		if err := writeAtomic(dir, "barrier-release.json", m); err != nil {
			t.Fatal(err)
		}
		if _, err := readMarker(dir, "barrier-release.json", a.Ticket, "release"); err == nil {
			t.Fatal("mismatched marker accepted")
		}
	}
	if err := os.Symlink(filepath.Join(dir, "barrier-release.json"), filepath.Join(dir, "barrier-arm.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readArm(dir); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestConfigurationOnlyEnvironmentDSN(t *testing.T) {
	dir := t.TempDir()
	getenv := func(key string) string {
		if key != "ADTR_BARRIER_DATABASE_URL" {
			t.Fatal("unexpected environment access")
		}
		return "synthetic-invalid"
	}
	if _, err := readConfiguration([]string{"-control-dir", dir}, getenv); err != nil {
		t.Fatal("valid configuration rejected")
	}
	for _, args := range [][]string{{}, {"-control-dir", "relative"}, {"-control-dir", dir, "-dsn", "secret"}, {"-control-dir", dir, "extra"}} {
		if _, err := readConfiguration(args, getenv); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if _, err := readConfiguration([]string{"-control-dir", dir}, func(string) string { return "" }); err == nil {
		t.Fatal("missing DSN accepted")
	}
}

type fakeDatabase struct {
	mu                      sync.Mutex
	available, held, closed bool
	releases                int
	s                       snapshot
	observeError            bool
	closeError              bool
}

func (f *fakeDatabase) tryLock(context.Context, armRecord) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = f.available
	return f.held, nil
}
func (f *fakeDatabase) observe(context.Context, armRecord) (snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observeError {
		return snapshot{}, errors.New("observer_failed")
	}
	return f.s, nil
}
func (f *fakeDatabase) release() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = false
	f.releases++
	return nil
}
func (f *fakeDatabase) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = false
	f.closed = true
	if f.closeError {
		return errors.New("rollback_failed")
	}
	return nil
}
func (f *fakeDatabase) mutate(fn func(*fakeDatabase)) { f.mu.Lock(); defer f.mu.Unlock(); fn(f) }
func newFake() *fakeDatabase {
	return &fakeDatabase{s: snapshot{TaskID: "real-task", TaskKind: "domain.account_connection_test", TaskState: "running", UseState: "opened", OpenedAtPresent: true, OpenerPresent: true, OpenerUnchanged: true, ExactTaskDependencyCount: 1, TotalDependencyCount: 2}}
}

func readStatus(t *testing.T, dir string) status {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "barrier-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s status
	if json.Unmarshal(b, &s) != nil {
		t.Fatal("invalid status")
	}
	return s
}
func awaitStatus(t *testing.T, dir string, condition func(status) bool) status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(dir, "barrier-status.json"))
		var s status
		if err == nil && json.Unmarshal(b, &s) == nil && condition(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("status condition timed out")
	return status{}
}
func startRun(t *testing.T, db *fakeDatabase, timing deadlines) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, dir, db, timing) }()
	t.Cleanup(func() { cancel() })
	awaitStatus(t, dir, func(s status) bool { return s.Phase == "ready" })
	return dir, cancel, done
}
func put(t *testing.T, dir, name string, v any) {
	t.Helper()
	if err := writeAtomic(dir, name, v); err != nil {
		t.Fatal(err)
	}
}

var testDeadlines = deadlines{time.Second, time.Second, time.Millisecond, time.Millisecond}

func TestCoordinatorBothOrdersAndRealQuiescenceSignal(t *testing.T) {
	for _, order := range []string{"lock-first", "reached-first"} {
		t.Run(order, func(t *testing.T) {
			f := newFake()
			f.available = order == "lock-first"
			dir, cancel, done := startRun(t, f, testDeadlines)
			a := validArm()
			put(t, dir, "barrier-arm.json", a)
			awaitStatus(t, dir, func(s status) bool { return s.Armed })
			b, err := os.ReadFile(filepath.Join(dir, "fixture-arm.json"))
			var fa fixtureArm
			if err != nil || json.Unmarshal(b, &fa) != nil || fa != (fixtureArm{1, a.Ticket, "hold_rootdse"}) {
				t.Fatal("armed published before valid fixture arm")
			}
			if order == "lock-first" {
				awaitStatus(t, dir, func(s status) bool { return s.LedgerLocked })
				if _, err := os.Stat(filepath.Join(dir, "fixture-"+a.Ticket+"-release.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("fixture released without reached")
				}
			}
			put(t, dir, "fixture-"+a.Ticket+"-reached.json", marker{1, a.Ticket, "reached"})
			if order == "reached-first" {
				awaitStatus(t, dir, func(s status) bool { return s.FixtureReached })
				if _, err := os.Stat(filepath.Join(dir, "fixture-"+a.Ticket+"-release.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("fixture released without lock")
				}
				f.mutate(func(f *fakeDatabase) { f.available = true })
			}
			s := awaitStatus(t, dir, func(s status) bool { return s.FixtureReleased })
			if !s.LockHeld || !s.LedgerLocked || !s.FixtureReached || s.Released {
				t.Fatal("incorrect cumulative lock evidence")
			}
			for _, phase := range []string{"released", "response_written", "handler_closed"} {
				put(t, dir, "fixture-"+a.Ticket+"-"+phase+".json", marker{1, a.Ticket, phase})
			}
			f.mutate(func(f *fakeDatabase) {
				f.s.TaskState = "succeeded"
				f.s.DiagnosticCode = "success"
				f.s.DiagnosticSuccessful = true
			})
			s = awaitStatus(t, dir, func(s status) bool { return s.FixtureHandlerClosed && s.Snapshot.TaskState == "succeeded" })
			if s.Quiesced || s.Snapshot.UseState != "opened" || !s.LockHeld {
				t.Fatal("terminal task or fixture closure treated as quiescence")
			}
			put(t, dir, "barrier-release.json", marker{1, a.Ticket, "release"})
			s = awaitStatus(t, dir, func(s status) bool { return s.Released })
			if s.LockHeld || s.Quiesced {
				t.Fatal("rollback fabricated acknowledgement")
			}
			f.mutate(func(f *fakeDatabase) {
				f.s.UseState = "quiesced"
				f.s.QuiescedAtPresent = true
				f.s.QuiescenceReason = "executor_returned"
				f.s.ExactTaskDependencyCount = 0
				f.s.TotalDependencyCount = 0
			})
			s = awaitStatus(t, dir, func(s status) bool { return s.Quiesced })
			if !s.LedgerLocked || !s.Released || s.Phase != "quiesced" {
				t.Fatal("lost cumulative evidence")
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.closed || f.held || f.releases != 1 {
				t.Fatal("cleanup or explicit rollback incorrect")
			}
		})
	}
}

func TestCoordinatorFailureAlwaysClosesLock(t *testing.T) {
	for _, scenario := range []string{"cancel", "cancel-rollback-failed", "expired", "watchdog", "bad-release", "changed-arm", "observer"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFake()
			f.available = true
			timing := testDeadlines
			if scenario == "watchdog" {
				timing.held = 40 * time.Millisecond
			}
			dir, cancel, done := startRun(t, f, timing)
			a := validArm()
			put(t, dir, "barrier-arm.json", a)
			put(t, dir, "fixture-"+a.Ticket+"-reached.json", marker{1, a.Ticket, "reached"})
			awaitStatus(t, dir, func(s status) bool { return s.LockHeld && s.FixtureReleased })
			switch scenario {
			case "cancel":
				cancel()
			case "cancel-rollback-failed":
				f.mutate(func(f *fakeDatabase) { f.closeError = true })
				cancel()
			case "expired":
				put(t, dir, "fixture-"+a.Ticket+"-expired.json", marker{1, a.Ticket, "expired"})
			case "bad-release":
				put(t, dir, "barrier-release.json", marker{1, strings.Repeat("b", 32), "release"})
			case "changed-arm":
				a.ActorID = "13"
				put(t, dir, "barrier-arm.json", a)
			case "observer":
				f.mutate(func(f *fakeDatabase) { f.observeError = true })
			}
			select {
			case err := <-done:
				if (scenario == "cancel") != (err == nil) {
					t.Fatal("unexpected completion outcome")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("bounded cleanup failed")
			}
			s := readStatus(t, dir)
			if s.LockHeld || s.Released {
				t.Fatal("failure counted as explicit release")
			}
			if scenario != "cancel" && (s.Phase != "failed" || s.ErrorCode == "") {
				t.Fatal("failure not reported")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.closed || f.held {
				t.Fatal("lock not closed")
			}
		})
	}
}

func TestCoordinatorAcquisitionTimeoutAndPrematureRelease(t *testing.T) {
	for _, scenario := range []string{"timeout", "premature"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFake()
			timing := testDeadlines
			timing.acquisition = 40 * time.Millisecond
			dir, _, done := startRun(t, f, timing)
			a := validArm()
			put(t, dir, "barrier-arm.json", a)
			awaitStatus(t, dir, func(s status) bool { return s.Armed })
			if scenario == "premature" {
				put(t, dir, "barrier-release.json", marker{1, a.Ticket, "release"})
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("invalid control completed")
				}
			case <-time.After(time.Second):
				t.Fatal("acquisition unbounded")
			}
			if s := readStatus(t, dir); s.Released || s.LedgerLocked || s.Phase != "failed" {
				t.Fatal("fabricated lock/release")
			}
		})
	}
}

func TestSnapshotAcknowledgementRequiresEvidence(t *testing.T) {
	s := snapshot{UseState: "quiesced", OpenedAtPresent: true, OpenerPresent: true, OpenerUnchanged: true, QuiescedAtPresent: true, QuiescenceReason: "executor_returned"}
	if !s.isQuiesced() {
		t.Fatal("genuine acknowledgement rejected")
	}
	for _, change := range []func(*snapshot){func(s *snapshot) { s.UseState = "opened" }, func(s *snapshot) { s.OpenerUnchanged = false }, func(s *snapshot) { s.QuiescenceReason = "never_opened_terminal" }, func(s *snapshot) { s.ExactTaskDependencyCount = 1 }, func(s *snapshot) { s.QuiescedAtPresent = false }} {
		bad := s
		change(&bad)
		if bad.isQuiesced() {
			t.Fatal("incomplete acknowledgement accepted")
		}
	}
}
