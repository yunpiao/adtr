//go:build adtr_ci_dbadmission

package dbadmission

import (
	"encoding/json"
	"testing"
)

// Synthetic registry tests retain their boolean fixture interface. Production
// receives the three independently observed facts from Conn.Close only.
func (t *lifecycleTicket) closed(clean bool) {
	t.closedObserved(closeObservation{alreadyClosed: !clean, cleanupDone: clean})
}

func TestUncleanCloseFactsKeepOriginalPredicateAndFirstCause(t *testing.T) {
	for _, target := range []bool{false, true} {
		for bits := 0; bits < 8; bits++ {
			g, generation := newSealProtocolTestGate()
			var ticket *lifecycleTicket
			for _, candidate := range g.tickets {
				ticket = candidate
			}
			if target {
				if g.pin(1, generation, nil) != nil || g.release(1) != nil {
					t.Fatal("fixture release")
				}
			}
			observation := closeObservation{bits&1 != 0, bits&2 != 0, bits&4 != 0}
			clean := !observation.alreadyClosed && !observation.closeFailed && observation.cleanupDone
			w := &diagnosticWitness{before: func() {
				if g.firstUncleanClose != nil {
					t.Fatal("close diagnostic preceded sticky")
				}
			}}
			g.witness = w
			ticket.closedObserved(observation)
			d := g.uncleanCloseDiagnostic()
			if clean {
				if g.invalid || d != nil || w.stores != 0 {
					t.Fatal("clean close changed")
				}
				continue
			}
			if !g.invalid || w.stores != 1 || d == nil || d.AlreadyClosed != observation.alreadyClosed || d.CloseFailed != observation.closeFailed || d.CleanupDone != observation.cleanupDone {
				t.Fatal("dirty close facts lost")
			}
			role := "other"
			if target {
				role = "epoch_target"
			}
			if d.Role != role {
				t.Fatal("role not derived from epoch target")
			}
			raw, err := json.Marshal(d)
			var fields map[string]json.RawMessage
			if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 5 {
				t.Fatal("closed schema")
			}
			for _, key := range []string{"event", "alreadyClosed", "closeFailed", "cleanupDone", "role"} {
				if _, ok := fields[key]; !ok {
					t.Fatal("closed field")
				}
			}
			w.before = nil
			g.invalidateTransport()
			if got := g.uncleanCloseDiagnostic(); got == nil || *got != *d {
				t.Fatal("later error replaced first close")
			}
		}
	}
}

func TestUncleanCloseCannotReplaceEarlierFailure(t *testing.T) {
	g, _ := newSealProtocolTestGate()
	var ticket *lifecycleTicket
	for _, candidate := range g.tickets {
		ticket = candidate
	}
	g.invalidateTransport()
	ticket.closedObserved(closeObservation{true, true, false})
	if g.firstInvalidReason != invalidTransport || g.uncleanCloseDiagnostic() != nil {
		t.Fatal("first failure replaced")
	}
	g.firstInvalidReason = invalidUncleanClose
	if g.uncleanCloseDiagnostic() != nil {
		t.Fatal("missing facts invented")
	}
	g.firstUncleanClose = &uncleanCloseDiagnostic{Event: "private", Role: "other"}
	if g.uncleanCloseDiagnostic() != nil {
		t.Fatal("untrusted event emitted")
	}
	g.firstUncleanClose = &uncleanCloseDiagnostic{Event: "owned_ci_first_unclean_close", Role: "private"}
	if g.uncleanCloseDiagnostic() != nil {
		t.Fatal("untrusted role emitted")
	}
}

func TestUncleanCloseTargetRoleBeforeRelease(t *testing.T) {
	g, generation := newSealProtocolTestGate()
	if g.pin(1, generation, nil) != nil {
		t.Fatal("pin")
	}
	g.seal.target.closedObserved(closeObservation{alreadyClosed: true, cleanupDone: true})
	d := g.uncleanCloseDiagnostic()
	if d == nil || d.Role != "epoch_target" || g.firstInvalidPhase != "sealed" || !g.invalid {
		t.Fatal("sealed target facts lost")
	}
	if g.release(1) != nil {
		t.Fatal("release")
	}
	if after := g.uncleanCloseDiagnostic(); after == nil || *after != *d || !g.invalid {
		t.Fatal("release erased close failure")
	}
}
