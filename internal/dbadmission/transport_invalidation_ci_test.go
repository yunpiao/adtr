//go:build adtr_ci_dbadmission

package dbadmission

import (
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitTransportInvalidationTestLatch(t *testing.T, done <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
}

func TestTransportPublicationFailureReturnsWithoutReenteringPublisher(t *testing.T) {
	g, generation := newSealProtocolTestGate()
	file, err := os.CreateTemp(t.TempDir(), "owned-control-")
	if err != nil {
		t.Fatal("owned control file fixture failed")
	}
	t.Cleanup(func() { file.Close() })
	var stopping atomic.Bool
	var publications, failures atomic.Int32
	failureRecorded := make(chan struct{})
	allowPublicationReturn := make(chan struct{})
	var allowReturnOnce sync.Once
	allowReturn := func() { allowReturnOnce.Do(func() { close(allowPublicationReturn) }) }
	// A regression may leave a goroutine inside sync.Once forever. Cleanup only
	// releases our own latch and file; it must not join or reenter that publisher.
	t.Cleanup(allowReturn)
	secondInvalidationEntered := make(chan struct{})
	witness := &diagnosticWitness{}
	witness.before = func() {
		// The explicit invalidation and its synchronous transport failure are
		// the first two stores. This observes the second ordinary invalidator
		// in the real locked path, rather than merely observing goroutine start.
		if witness.stores == 2 {
			close(secondInvalidationEntered)
		}
	}
	g.witness = witness
	if g.pin(1, generation, func() {
		if publications.Add(1) != 1 {
			return
		}
		// Exercise the actual controller failure handler synchronously while
		// the sole invalid publication is still inside its sync.Once callback.
		recordApplicationFailure(file, g, &stopping, func() { failures.Add(1) }, io.ErrClosedPipe)
		close(failureRecorded)
		<-allowPublicationReturn
	}) != nil {
		t.Fatal("pin fixture failed")
	}
	publicationDone := g.seal.publishDone
	firstReturned := make(chan struct{})
	go func() {
		g.invalidate()
		close(firstReturned)
	}()
	waitTransportInvalidationTestLatch(t, failureRecorded, "synchronous application failure reentered the in-progress publisher")
	if publications.Load() != 1 || failures.Load() != 1 {
		t.Fatal("synchronous failure did not invoke one publication and one failure callback")
	}
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("application failure returned without closing its owned transport")
	}
	secondReturned := make(chan struct{})
	go func() {
		g.invalidate()
		close(secondReturned)
	}()
	waitTransportInvalidationTestLatch(t, secondInvalidationEntered, "second invalidator did not reach the sticky store while publication was pending")
	// Entry is established by the store latch above. This bounded negative
	// observation supplements that synchronization; it is not a scheduling proof.
	select {
	case <-firstReturned:
		t.Fatal("first invalidator returned before its publication callback completed")
	case <-secondReturned:
		t.Fatal("second invalidator bypassed the shared pending publication")
	case <-publicationDone:
		t.Fatal("publication completion was announced while its callback was still running")
	case <-time.After(25 * time.Millisecond):
	}
	allowReturn()
	waitTransportInvalidationTestLatch(t, firstReturned, "first invalidator did not return after publication completed")
	waitTransportInvalidationTestLatch(t, secondReturned, "second invalidator did not return after the shared publication completed")
	waitTransportInvalidationTestLatch(t, publicationDone, "shared publication completion was not recorded")
	if publications.Load() != 1 || failures.Load() != 1 || witness.stores != 3 {
		t.Fatal("concurrent invalidation repeated publication, failure callback, or sticky mutation")
	}
	proof := g.applicationDiagnostic(1)
	if !g.invalid || proof.Reason != invalidExplicit || proof.Phase != "sealed" {
		t.Fatal("transport failure replaced the original invalidation or cleared its sticky state")
	}
	stores := witness.stores
	if g.release(1) != nil || !g.invalid || witness.stores != stores || g.seal.recoveryAuthorized {
		t.Fatal("rescue release erased invalidation evidence or authorized recovery")
	}
	proof = g.applicationDiagnostic(2)
	if proof.Reason != invalidExplicit || proof.Phase != "sealed" || publications.Load() != 1 || failures.Load() != 1 {
		t.Fatal("rescue release changed the first cause or repeated failure publication")
	}
}

func TestTransportInvalidationUnparksOnlyExistingBarriersAfterStickyStore(t *testing.T) {
	for _, idle := range []struct {
		name, state string
	}{{"fresh", ""}, {"complete", "complete"}} {
		for _, parked := range []struct {
			name          string
			final, queued bool
		}{
			{"neither", false, false},
			{"final", true, false},
			{"queued", false, true},
			{"both", true, true},
		} {
			t.Run(idle.name+"/"+parked.name, func(t *testing.T) {
				g, generation := newSealProtocolTestGate()
				g.finalParked, g.queuedParked = parked.final, parked.queued
				g.finalQuiesce.state, g.queuedState = idle.state, idle.state
				if parked.final {
					g.finalQuiesce.state = "drained"
				}
				if parked.queued {
					g.queuedState = "configured"
				}
				initialFinal, initialQueued := g.finalQuiesce.state, g.queuedState
				var publications atomic.Int32
				if g.pin(1, generation, func() { publications.Add(1) }) != nil {
					t.Fatal("pin fixture failed")
				}
				changed, revision := g.changed, g.revision
				pending, open, total := g.pending, g.open, g.total
				target, registration := g.seal.target, g.seal.registration
				stickyBeforeMutation := false
				witness := &diagnosticWitness{before: func() {
					// Called with g.mu held. Observe without reentering the lock:
					// sticky persistence must precede both admission-barrier changes.
					stickyBeforeMutation = !g.invalid && g.firstInvalidReason == "" &&
						g.finalParked == parked.final && g.queuedParked == parked.queued &&
						g.finalQuiesce.state == initialFinal && g.queuedState == initialQueued &&
						g.changed == changed && g.revision == revision
				}}
				g.witness = witness
				returned := make(chan struct{})
				go func() {
					g.invalidateTransport()
					close(returned)
				}()
				waitTransportInvalidationTestLatch(t, returned, "transport invalidation did not return")
				if !stickyBeforeMutation || witness.stores != 1 || !g.invalid || g.firstInvalidReason != invalidTransport || g.firstInvalidPhase != "sealed" {
					t.Fatal("transport invalidation changed state before committing its sticky witness")
				}
				wantFinal, wantQueued := initialFinal, initialQueued
				if parked.final {
					wantFinal = "failed"
				}
				if parked.queued {
					wantQueued = "failed"
				}
				if g.finalParked || g.queuedParked || g.finalQuiesce.state != wantFinal || g.queuedState != wantQueued {
					t.Fatal("transport failure retained a parked barrier or invented a failure state for an unparked barrier")
				}
				if g.revision != revision+1 || g.changed == changed {
					t.Fatal("transport invalidation did not issue one admission notification")
				}
				select {
				case <-changed:
				default:
					t.Fatal("transport invalidation left the previous admission waiters asleep")
				}
				if publications.Load() != 0 {
					t.Fatal("transport invalidation tried to publish on the broken transport")
				}
				select {
				case <-g.seal.publishDone:
					t.Fatal("transport invalidation consumed the ordinary publication completion")
				default:
				}
				if g.pending != pending || g.open != open || g.total != total || len(g.tickets) != 1 || target.state != 'o' || target.registration != registration || g.seal.released || !g.closing {
					t.Fatal("transport failure altered a registered lifetime or silently released the seal")
				}
				if g.release(1) != nil || !g.invalid || witness.stores != 1 || g.seal.recoveryAuthorized {
					t.Fatal("rescue release cleared sticky transport failure or authorized recovery")
				}
				witness.before = nil
				repeated := make(chan struct{})
				go func() {
					g.invalidateTransport()
					close(repeated)
				}()
				waitTransportInvalidationTestLatch(t, repeated, "repeated transport invalidation did not return")
				if !g.invalid || witness.stores != 2 || g.firstInvalidReason != invalidTransport || g.firstInvalidPhase != "sealed" || g.finalParked || g.queuedParked || g.finalQuiesce.state != wantFinal || g.queuedState != wantQueued || publications.Load() != 0 {
					t.Fatal("repeated transport failure erased first-cause evidence or changed unparked states")
				}
			})
		}
	}
}
