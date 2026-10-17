//go:build adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"context"
	"encoding/binary"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func testWitnessFile(t *testing.T, edit func([]byte)) (*os.File, [16]byte) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "owned-page")
	if err != nil {
		t.Fatal("page create")
	}
	t.Cleanup(func() { f.Close() })
	page := make([]byte, 4096)
	copy(page, "ADTRGATE")
	binary.LittleEndian.PutUint32(page[8:], 1)
	binary.LittleEndian.PutUint32(page[12:], 4096)
	binary.LittleEndian.PutUint64(page[16:], 1)
	var nonce [16]byte
	nonce[0] = 9
	copy(page[32:], nonce[:])
	if edit != nil {
		edit(page)
	}
	if _, err = f.Write(page); err != nil {
		t.Fatal("page initialize")
	}
	return f, nonce
}
func TestApplicationWitnessFormatAndSingleBinding(t *testing.T) {
	for _, offset := range []int{0, 8, 12, 16, 24, 28, 32, 48, 4095} {
		t.Run(string(rune('a'+offset%26)), func(t *testing.T) {
			f, n := testWitnessFile(t, func(p []byte) { p[offset] ^= 2 })
			if _, err := mapCIWitness(f, n); err == nil {
				t.Fatal("malformed page accepted")
			}
		})
	}
	f, n := testWitnessFile(t, nil)
	w, err := mapCIWitness(f, n)
	if err != nil {
		t.Fatal("valid page rejected")
	}
	if _, err = mapCIWitness(f, n); err == nil {
		t.Fatal("reused page accepted")
	}
	w.invalidate()
	w.invalidate()
	if atomic.LoadUint32(w.invalid) != 1 || atomic.LoadUint32(w.bound) != 1 {
		t.Fatal("sticky state changed")
	}
	f, n = testWitnessFile(t, nil)
	if f.Truncate(64) != nil {
		t.Fatal("short fixture")
	}
	if _, err = mapCIWitness(f, n); err == nil {
		t.Fatal("short page accepted")
	}
}

type pausedInvalidation struct {
	bit              atomic.Uint32
	entered, proceed chan struct{}
	before           bool
}

func (w *pausedInvalidation) invalidate() {
	if !w.before {
		w.bit.Store(1)
	}
	close(w.entered)
	<-w.proceed
	if w.before {
		w.bit.Store(1)
	}
}
func TestApplicationStickyPrecedesTargetMutationAndPublication(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, operation := range []string{"close-start", "close-finish", "revoke", "transport", "cancel"} {
			t.Run(operation, func(t *testing.T) {
				g := &lifecycleGate{}
				ticket, _ := g.acquire(context.Background())
				conn := attachTestTicket(ticket)
				revoke, err := g.register(conn, testSealIdentity())
				if err != nil {
					t.Fatal("registration")
				}
				generation := g.candidates()[0].generation
				w := &pausedInvalidation{entered: make(chan struct{}), proceed: make(chan struct{}), before: before}
				g.witness = w
				if g.pin(1, generation, nil) != nil {
					t.Fatal("pin")
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch operation {
					case "close-start":
						ticket.closingConnection()
					case "close-finish":
						ticket.closed(true)
					case "revoke":
						revoke()
					case "transport":
						g.invalidateTransport()
					case "cancel":
						ctx, cancel := context.WithCancel(context.Background())
						cancel()
						g.acquire(ctx)
					}
				}()
				select {
				case <-w.entered:
				case <-time.After(time.Second):
					t.Fatal("invalidation missing")
				}
				want := uint32(1)
				if before {
					want = 0
				}
				if w.bit.Load() != want {
					t.Fatal("witness order")
				}
				if ticket.state != 'o' || ticket.registration == nil || g.open != 1 {
					t.Fatal("mutation preceded sticky store")
				}
				close(w.proceed)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("invalidation blocked")
				}
				if w.bit.Load() != 1 || g.frame(1).Status != "invalid" {
					t.Fatal("terminal invalid lost")
				}
			})
		}
	}
}
