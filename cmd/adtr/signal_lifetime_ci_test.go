//go:build integration && adtr_ci_dbadmission && linux && amd64

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The report has a closed field set and only fixed labels, counters, and booleans.
// In particular, child output cannot disclose process identities or configuration.
type ciSignalLifetimeReport struct {
	Stage            string `json:"stage"`
	StopCalls        int32  `json:"stop_calls"`
	CleanupComplete  bool   `json:"cleanup_complete"`
	ProbeCount       int    `json:"probe_count"`
	LogicalCancelled bool   `json:"logical_cancelled"`
	RawCancelled     bool   `json:"raw_cancelled"`
}

func TestActivityCandidateSignalLifetimeSynchronousCancel(t *testing.T) {
	raw, rawCancel := context.WithCancel(context.Background())
	defer rawCancel()
	stops, cleanups := 0, 0
	logical, cancel, cleanup := splitCISignalLifetime(raw, func() {
		stops++
		rawCancel()
	}, func() {
		if stops != 0 || raw.Err() != nil {
			t.Error("signal registration released before cleanup finished")
		}
		cleanups++
	})
	children := make([]context.Context, 32)
	for i := range children {
		parent := logical
		if i > 0 {
			parent = children[i-1]
		}
		child, stopChild := context.WithCancel(parent)
		defer stopChild()
		children[i] = child
	}
	cancel()
	// This check is deliberately immediate: a goroutine forwarding cancellation
	// would leave a window in which a worker could admit another operation.
	if logical.Err() != context.Canceled || raw.Err() != nil || stops != 0 {
		t.Fatal("logical cancellation was not synchronous and independent")
	}
	for _, child := range children {
		if child.Err() != context.Canceled {
			t.Fatal("logical cancellation returned before descendant barrier")
		}
	}
	select {
	case <-logical.Done():
	default:
		t.Fatal("logical cancellation barrier remained open")
	}
	cleanup()
	if stops != 1 || cleanups != 1 || raw.Err() != context.Canceled {
		t.Fatal("cleanup did not release signal registration exactly once")
	}
}

func TestActivityCandidateSignalLifetimeCleanupPanic(t *testing.T) {
	stops := 0
	_, cancel, cleanup := splitCISignalLifetime(context.Background(), func() { stops++ }, func() {
		if stops != 0 {
			t.Error("signal registration released before cleanup")
		}
		panic("synthetic cleanup failure")
	})
	defer cancel()
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		cleanup()
	}()
	if !panicked || stops != 1 {
		t.Fatal("cleanup panic did not preserve deferred signal release")
	}
}

func TestActivityCandidateSignalLifetimeOwnedChild(t *testing.T) {
	if os.Getenv("ADTR_CI_SIGNAL_LIFETIME_CHILD") == "1" {
		ciSignalLifetimeChild(t)
		return
	}
	for _, entry := range []string{"logicalCancel", "admissionFailure", "externalSIGTERM"} {
		t.Run(entry, func(t *testing.T) {
			deadline := time.Now().Add(15 * time.Second)
			if outer, ok := t.Deadline(); ok && outer.Add(-2*time.Second).Before(deadline) {
				deadline = outer.Add(-2 * time.Second)
			}
			if time.Until(deadline) < 2*time.Second {
				t.Fatal("insufficient child test deadline")
			}
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			controlRead, controlWrite, err := os.Pipe()
			if err != nil {
				t.Fatal("child control pipe")
			}
			defer controlRead.Close()
			defer controlWrite.Close()
			reportRead, reportWrite, err := os.Pipe()
			if err != nil {
				t.Fatal("child report pipe")
			}
			defer reportRead.Close()
			defer reportWrite.Close()
			if controlWrite.SetDeadline(deadline) != nil || reportRead.SetDeadline(deadline) != nil {
				t.Fatal("parent pipe deadline")
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal("test executable")
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestActivityCandidateSignalLifetimeOwnedChild$", "-test.timeout=20s")
			cmd.Env = []string{"ADTR_CI_SIGNAL_LIFETIME_CHILD=1", "ADTR_CI_SIGNAL_LIFETIME_ENTRY=" + entry,
				"ADTR_CI_SIGNAL_LIFETIME_DEADLINE=" + strconv.FormatInt(deadline.Add(-time.Second).UnixNano(), 10)}
			cmd.ExtraFiles = []*os.File{controlRead, reportWrite}
			// Nil stdout/stderr go to the null device; no output-copy goroutines,
			// child diagnostics, inherited secrets, or process IDs reach the log.
			cmd.WaitDelay = time.Second
			if cmd.Start() != nil {
				t.Fatal("owned child start")
			}
			controlRead.Close()
			reportWrite.Close()
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }() // The only Wait call, including failures.
			joined := false
			defer func() {
				if !joined {
					_ = cmd.Process.Kill()
					select {
					case <-waited:
					case <-time.After(2 * time.Second):
						t.Error("owned child reap deadline")
					}
				}
			}()
			decoder := json.NewDecoder(reportRead)
			decoder.DisallowUnknownFields()
			read := func(stage string, probes int, complete bool, stops int32) ciSignalLifetimeReport {
				t.Helper()
				var report ciSignalLifetimeReport
				if decoder.Decode(&report) != nil {
					t.Fatal("owned child report unavailable")
				}
				if report.Stage != stage || report.ProbeCount != probes || report.CleanupComplete != complete || report.StopCalls != stops {
					t.Fatal("owned child closed report mismatch")
				}
				return report
			}
			write := func(command byte) {
				t.Helper()
				if _, err := controlWrite.Write([]byte{command}); err != nil {
					t.Fatal("owned child control unavailable")
				}
			}
			ready := read("ready", 0, false, 0)
			if ready.LogicalCancelled || ready.RawCancelled {
				t.Fatal("child signal lifetime started cancelled")
			}
			if entry == "externalSIGTERM" {
				if cmd.Process.Signal(syscall.SIGTERM) != nil {
					t.Fatal("owned child initial signal")
				}
			} else {
				write('S')
			}
			entered := read("cleanup_entered", 0, false, 0)
			if !entered.LogicalCancelled || entered.RawCancelled != (entry == "externalSIGTERM") {
				t.Fatal("entry did not preserve independent cancellation")
			}
			for probe := 1; probe <= 2; probe++ {
				if cmd.Process.Signal(syscall.SIGTERM) != nil {
					t.Fatal("owned child cleanup signal")
				}
				write('P')
				alive := read("cleanup_alive", probe, false, 0)
				if !alive.LogicalCancelled || !alive.RawCancelled {
					t.Fatal("signal was not observed during cleanup")
				}
			}
			write('R')
			final := read("cleanup_complete", 2, true, 1)
			if !final.LogicalCancelled || !final.RawCancelled {
				t.Fatal("final cancellation record incomplete")
			}
			var extra ciSignalLifetimeReport
			if decoder.Decode(&extra) != io.EOF {
				t.Fatal("unexpected extra child report")
			}
			select {
			case err := <-waited:
				joined = true
				if err != nil {
					t.Fatal("owned child did not exit successfully")
				}
			case <-ctx.Done():
				t.Fatal("owned child exit deadline")
			}
		})
	}
}

func ciSignalLifetimeChild(t *testing.T) {
	t.Helper()
	nanos, err := strconv.ParseInt(os.Getenv("ADTR_CI_SIGNAL_LIFETIME_DEADLINE"), 10, 64)
	if err != nil {
		t.Fatal("child absolute deadline")
	}
	deadline := time.Unix(0, nanos)
	bound, stopBound := context.WithDeadline(context.Background(), deadline)
	defer stopBound()
	// ExtraFiles arrive as blocking descriptors. Set O_NONBLOCK before NewFile
	// so Go's poller can enforce the same absolute deadline on both directions.
	if syscall.SetNonblock(3, true) != nil || syscall.SetNonblock(4, true) != nil {
		t.Fatal("child nonblocking descriptors")
	}
	control := os.NewFile(3, "signal-control")
	reports := os.NewFile(4, "signal-reports")
	if control == nil || reports == nil {
		t.Fatal("child inherited descriptors")
	}
	defer control.Close()
	defer reports.Close()
	if control.SetDeadline(deadline) != nil || reports.SetDeadline(deadline) != nil {
		t.Fatal("child pipe deadline")
	}
	// This must remain the only SIGTERM registration. An independent observer
	// registered with signal.Notify would hide an early signalStop regression.
	raw, rawStop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer rawStop() // Emergency cleanup only; normal release is counted below.
	var stops atomic.Int32
	var logical context.Context
	probes, complete := 0, false
	encoder := json.NewEncoder(reports)
	report := func(stage string) {
		if encoder.Encode(ciSignalLifetimeReport{stage, stops.Load(), complete, probes, logical.Err() != nil, raw.Err() != nil}) != nil {
			t.Fatal("child report write")
		}
	}
	read := func() byte {
		var command [1]byte
		if _, err := io.ReadFull(control, command[:]); err != nil {
			t.Fatal("child control read deadline")
		}
		return command[0]
	}
	var cancel context.CancelFunc
	var cleanup func()
	logical, cancel, cleanup = splitCISignalLifetime(raw, func() {
		stops.Add(1)
		rawStop()
	}, func() {
		if stops.Load() != 0 {
			t.Fatal("signal registration stopped before cleanup")
		}
		report("cleanup_entered")
		for probes < 2 {
			if read() != 'P' {
				t.Fatal("child cleanup probe command")
			}
			// Await actual signal cancellation, then leave a bounded scheduling
			// interval for default termination to manifest if registration was lost.
			select {
			case <-raw.Done():
			case <-bound.Done():
				t.Fatal("child signal delivery deadline")
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-bound.Done():
				timer.Stop()
				t.Fatal("child cleanup survival deadline")
			}
			if stops.Load() != 0 {
				t.Fatal("signal registration stopped during cleanup")
			}
			probes++
			report("cleanup_alive")
		}
		if read() != 'R' {
			t.Fatal("child cleanup release command")
		}
		complete = true
	})
	defer cancel()
	report("ready")
	switch os.Getenv("ADTR_CI_SIGNAL_LIFETIME_ENTRY") {
	case "logicalCancel":
		if read() != 'S' {
			t.Fatal("child logical start command")
		}
		cancel()
		if logical.Err() != context.Canceled {
			t.Fatal("child logical cancellation was asynchronous")
		}
	case "admissionFailure":
		failure, watcherDone := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(watcherDone)
			select {
			case <-failure:
				cancel()
			case <-bound.Done():
			}
		}()
		defer func() {
			// Also join on a control-read failure or an assertion before the
			// normal watcher join. Cancellation alone is not a cleanup barrier.
			stopBound()
			select {
			case <-watcherDone:
			case <-time.After(time.Second):
				t.Error("child admission watcher cleanup deadline")
			}
		}()
		if read() != 'S' {
			t.Fatal("child admission start command")
		}
		close(failure)
		select {
		case <-watcherDone:
		case <-bound.Done():
			t.Fatal("child admission watcher deadline")
		}
	case "externalSIGTERM":
	default:
		t.Fatal("child entry invalid")
	}
	select {
	case <-logical.Done():
	case <-bound.Done():
		t.Fatal("child logical cancellation deadline")
	}
	cleanup()
	if !complete || stops.Load() != 1 {
		t.Fatal("child final cleanup state incomplete")
	}
	report("cleanup_complete")
}
