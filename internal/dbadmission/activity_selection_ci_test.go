//go:build adtr_ci_dbadmission

package dbadmission

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"
)

func (g *lifecycleGate) register(conn *Conn, identity ActivityIdentity) (func(), error) {
	release, _, err := g.registerActivityRow(conn, identity, identity.LeaseOwner)
	return release, err
}

func TestActivityRowKeyCrossLanguageVector(t *testing.T) {
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	key := activityRowKey(nonce, "task-one")
	if key != "ci-activity-xjhrbwphlzfwschmfnm4kqsunp2qeowpnarubert6q2qx5ospmha" || len(key) != 64 || !selectionWorkerID(key) {
		t.Fatal("activity key vector")
	}
	nonce[0]++
	if key == activityRowKey(nonce, "task-one") || key == activityRowKey(nonce, "task-two") {
		t.Fatal("key identity collision")
	}
}

func TestActivitySelectionAwaitsBeforeAnyConnection(t *testing.T) {
	g := &lifecycleGate{application: true}
	identity := testSealIdentity()
	if g.prepareActivitySelection(identity.Kind) != nil {
		t.Fatal("prepare")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.awaitActivitySelection(ctx, identity) }()
	select {
	case <-done:
		t.Fatal("selection bypassed configuration")
	case <-time.After(20 * time.Millisecond):
	}
	g.mu.Lock()
	zero := g.pending == 0 && g.open == 0 && len(g.tickets) == 0
	g.mu.Unlock()
	if !zero {
		t.Fatal("selection allocated connection")
	}
	if g.configureActivitySelection(identity.TaskID, "private-worker") != nil {
		t.Fatal("configure")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("await failed")
		}
	case <-ctx.Done():
		t.Fatal("await leak")
	}
	ticket, err := g.acquire(ctx)
	if err != nil {
		t.Fatal("acquire")
	}
	conn := attachTestTicket(ticket)
	release, key, err := g.registerActivityRow(conn, identity, "private-worker")
	if err != nil || key != activityRowKey(g.nonce, identity.TaskID) || ticket.registration.activityKey != key {
		t.Fatal("bound activity key")
	}
	release()
	// The spent selection cannot attach a fresh registration or alias again.
	if _, _, err := g.registerActivityRow(conn, identity, "private-worker"); err == nil || !g.invalid {
		t.Fatal("selection replay")
	}
}

func TestActivitySelectionCancelAndTransportUnblockWithoutTickets(t *testing.T) {
	for _, transport := range []bool{false, true} {
		g := &lifecycleGate{application: true}
		identity := testSealIdentity()
		if g.prepareActivitySelection(identity.Kind) != nil {
			t.Fatal("prepare")
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- g.awaitActivitySelection(ctx, identity) }()
		select {
		case <-done:
			t.Fatal("early return")
		case <-time.After(20 * time.Millisecond):
		}
		if transport {
			g.invalidateTransport()
		} else {
			cancel()
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("failed selection accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("selection leaked")
		}
		cancel()
		if !g.invalid || g.pending != 0 || g.open != 0 || len(g.tickets) != 0 {
			t.Fatal("failure changed admission or hid invalid")
		}
	}
}

func TestActivitySelectionConfigurationAndKeysAreOneShot(t *testing.T) {
	for _, worker := range []string{"", "UPPER", "worker.with.dot", "private-worker:"} {
		g := &lifecycleGate{application: true}
		_ = g.prepareActivitySelection(testSealIdentity().Kind)
		if g.configureActivitySelection("task-one", worker) == nil {
			t.Fatal("invalid owner accepted")
		}
	}
	g := &lifecycleGate{application: true}
	identity := testSealIdentity()
	if g.configureActivitySelection(identity.TaskID, "private-worker") == nil {
		t.Fatal("unprepared configure")
	}
	if g.prepareActivitySelection(identity.Kind) != nil || g.prepareActivitySelection(identity.Kind) == nil {
		t.Fatal("prepare replay")
	}
	if g.configureActivitySelection(identity.TaskID, "private-worker") != nil || g.configureActivitySelection(identity.TaskID, "private-worker") == nil {
		t.Fatal("configure replay")
	}
	for _, test := range []struct {
		application bool
		selected    bool
		target      bool
	}{{false, false, true}, {true, false, true}, {true, true, false}} {
		gate := &lifecycleGate{application: test.application}
		if test.selected {
			_ = gate.prepareActivitySelection(identity.Kind)
			_ = gate.configureActivitySelection("other-task", "private-worker")
		}
		ticket, _ := gate.acquire(context.Background())
		conn := attachTestTicket(ticket)
		release, key, err := gate.registerActivityRow(conn, identity, "private-worker")
		if err != nil || key != "private-worker" {
			t.Fatal("ordinary key changed")
		}
		release()
	}
}

func TestActivitySelectionWrongPreparedCandidateFailsBeforeConnection(t *testing.T) {
	g := &lifecycleGate{application: true}
	identity := testSealIdentity()
	_ = g.prepareActivitySelection(identity.Kind)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.awaitActivitySelection(ctx, identity) }()
	select {
	case <-done:
		t.Fatal("await bypass")
	case <-time.After(20 * time.Millisecond):
	}
	if g.configureActivitySelection("different-task", "private-worker") != nil {
		t.Fatal("configure")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("non-target selected")
		}
	case <-ctx.Done():
		t.Fatal("await leak")
	}
	if !g.invalid || g.total != 0 {
		t.Fatal("wrong candidate acquired connection")
	}
}

func TestSelectionWireRejectsExtraAndMissingFields(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"epoch":1,"seq":1,"op":"prepare_selection","kind":"user_assets.v3.export","taskID":""}`,
		`{"version":2,"epoch":1,"seq":1,"op":"configure_target","taskID":"task"}`,
		`{"version":2,"epoch":1,"seq":1,"op":"configure_target","taskID":"task","workerID":"worker","kind":""}`,
		`{"version":2,"epoch":1,"seq":1,"op":"abort","taskID":"task"}`,
	} {
		if _, err := readApplicationRequest(bufio.NewReader(strings.NewReader(raw+"\n")), 1024, 0); err == nil {
			t.Fatal("selection wire shape accepted")
		}
	}
}
