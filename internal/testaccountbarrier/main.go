//go:build integration

// testaccountbarrier is a disposable integration-only row-lock coordinator.
// It never changes application data or exports credentials or opener evidence.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type configuration struct{ controlDir, dsn string }

func readConfiguration(args []string, getenv func(string) string) (configuration, error) {
	var c configuration
	f := flag.NewFlagSet("adtr-account-barrier", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&c.controlDir, "control-dir", "", "absolute disposable control directory")
	if f.Parse(args) != nil || f.NArg() != 0 || !filepath.IsAbs(c.controlDir) {
		return configuration{}, errors.New("configuration_invalid")
	}
	info, err := os.Lstat(c.controlDir)
	if err != nil || !info.IsDir() {
		return configuration{}, errors.New("configuration_invalid")
	}
	c.dsn = getenv("ADTR_BARRIER_DATABASE_URL")
	if c.dsn == "" {
		return configuration{}, errors.New("configuration_invalid")
	}
	return c, nil
}

type barrierDatabase interface {
	tryLock(context.Context, armRecord) (bool, error)
	observe(context.Context, armRecord) (snapshot, error)
	release() error
	close() error
}

type deadlines struct{ acquisition, held, observation, poll time.Duration }

var productionDeadlines = deadlines{15 * time.Second, 60 * time.Second, 100 * time.Millisecond, 10 * time.Millisecond}

func run(ctx context.Context, dir string, db barrierDatabase, timing deadlines) (result error) {
	s := status{Version: 1, Phase: "ready"}
	var arm armRecord
	var armedAt, lockedAt, observedAt time.Time
	publish := func() error { s.Sequence++; return writeAtomic(dir, "barrier-status.json", s) }
	fixtureRelease := func() error {
		return writeAtomic(dir, "fixture-"+arm.Ticket+"-release.json", marker{1, arm.Ticket, "release"})
	}
	defer func() {
		// Closure always releases a lock, even if rollback cannot reach the server.
		if err := db.close(); err != nil && result == nil {
			result = err
		}
		s.LockHeld = false
		if s.Armed && !s.FixtureReleased {
			if err := fixtureRelease(); err != nil && result == nil {
				result = errors.New("fixture_release_failed")
			}
		}
		if result != nil {
			s.Phase = "failed"
			s.ErrorCode = result.Error()
		}
		if err := publish(); err != nil && result == nil {
			result = errors.New("status_write_failed")
		}
	}()
	if err := publish(); err != nil {
		return errors.New("status_write_failed")
	}
	if err := writeAtomic(dir, "barrier-ready.json", struct {
		Version int    `json:"version"`
		Phase   string `json:"phase"`
	}{1, "ready"}); err != nil {
		return errors.New("ready_write_failed")
	}
	ticker := time.NewTicker(timing.poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return contextResult(ctx)
		}
		now := time.Now()
		changed := false
		if s.LockHeld && now.Sub(lockedAt) >= timing.held {
			return errors.New("lock_watchdog")
		}
		if s.Armed && !s.LedgerLocked && now.Sub(armedAt) >= timing.acquisition {
			return errors.New("acquisition_timeout")
		}
		a, exists, err := readArm(dir)
		if err != nil {
			return errors.New("arm_invalid")
		}
		if exists {
			if s.Armed && a != arm {
				return errors.New("arm_changed")
			}
			if !s.Armed {
				arm = a
				if _, exists, err := readControl(dir, "fixture-arm.json"); err != nil || exists {
					return errors.New("fixture_arm_stale")
				}
				if err = writeAtomic(dir, "fixture-arm.json", fixtureArm{1, a.Ticket, "hold_rootdse"}); err != nil {
					return errors.New("fixture_arm_failed")
				}
				s.Ticket, s.Armed, s.Phase = arm.Ticket, true, "armed"
				armedAt = now
				// Publish at the end of this iteration, after the first lock poll,
				// so the browser cannot continue before both controls are active.
				changed = true
			}
		} else if s.Armed {
			return errors.New("arm_removed")
		}
		if s.Armed {
			for _, phase := range []string{"expired", "reached", "released", "response_written", "handler_closed"} {
				seen, err := readMarker(dir, "fixture-"+arm.Ticket+"-"+phase+".json", arm.Ticket, phase)
				if err != nil {
					return errors.New("fixture_marker_invalid")
				}
				if !seen {
					continue
				}
				switch phase {
				case "expired":
					return errors.New("fixture_expired")
				case "reached":
					if !s.FixtureReached {
						s.FixtureReached, changed = true, true
					}
				case "response_written":
					if !s.FixtureResponseWritten {
						s.FixtureResponseWritten, changed = true, true
					}
				case "handler_closed":
					if !s.FixtureHandlerClosed {
						s.FixtureHandlerClosed, changed = true, true
					}
				}
			}
			release, err := readMarker(dir, "barrier-release.json", arm.Ticket, "release")
			if err != nil {
				return errors.New("release_invalid")
			}
			if release && !s.Released {
				if !s.LockHeld || !s.FixtureReleased {
					return errors.New("release_premature")
				}
				if err = db.release(); err != nil {
					return errors.New("rollback_failed")
				}
				s.LockHeld, s.Released, s.Phase, changed = false, true, "released", true
			}
			if !s.LedgerLocked {
				qctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
				locked, err := db.tryLock(qctx, arm)
				cancel()
				if ctx.Err() != nil {
					return contextResult(ctx)
				}
				if err != nil {
					return err
				}
				if locked {
					s.LedgerLocked, s.LockHeld, s.Phase, changed = true, true, "ledger_locked", true
					lockedAt = time.Now()
				}
			}
			// Do not spend an observer round trip inside the fixture's 2s hold.
			if s.LedgerLocked && s.FixtureReached && !s.FixtureReleased {
				if err = fixtureRelease(); err != nil {
					return errors.New("fixture_release_failed")
				}
				s.FixtureReleased, s.Phase, changed = true, "fixture_released", true
			}
			if s.LedgerLocked && (observedAt.IsZero() || now.Sub(observedAt) >= timing.observation || changed) {
				qctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
				v, err := db.observe(qctx, arm)
				cancel()
				if ctx.Err() != nil {
					return contextResult(ctx)
				}
				if err != nil {
					return err
				}
				if s.LockHeld && v.UseState != "opened" {
					return errors.New("locked_use_changed")
				}
				if s.Snapshot == nil || *s.Snapshot != v {
					changed = true
				}
				s.Snapshot = &v
				observedAt = now
				if s.Released && v.isQuiesced() && !s.Quiesced {
					s.Quiesced, s.Phase, changed = true, "quiesced", true
				}
			}
		} else if _, exists, err := readControl(dir, "barrier-release.json"); err != nil || exists {
			return errors.New("release_premature")
		}
		if changed {
			if err := publish(); err != nil {
				return errors.New("status_write_failed")
			}
		}
		select {
		case <-ctx.Done():
			return contextResult(ctx)
		case <-ticker.C:
		}
	}
}

func contextResult(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("lifetime_expired")
	}
	return nil
}

func execute(args []string, getenv func(string) string, stdin io.Reader) error {
	c, err := readConfiguration(args, getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, stdin); cancel() }()
	startup, done := context.WithTimeout(ctx, 10*time.Second)
	db, err := connectDatabase(startup, c.dsn)
	done()
	if err != nil {
		return err
	}
	return run(ctx, c.controlDir, db, productionDeadlines)
}

func main() {
	if err := execute(os.Args[1:], os.Getenv, os.Stdin); err != nil {
		// Never print errors from flags, connection parsing, SQL or filesystem paths.
		_, _ = os.Stderr.WriteString("account barrier failed\n")
		os.Exit(1)
	}
}
