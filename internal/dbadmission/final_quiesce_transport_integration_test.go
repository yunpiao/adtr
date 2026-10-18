//go:build integration && adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// A synthetic released authorization isolates the actual installer/transport
// shutdown protocol. Full capture and PostgreSQL qualification are separate.
func finalTransportFixture(t *testing.T) (*lifecycleGate, *os.File, *os.File, *bufio.Reader, func()) {
	t.Helper()
	normal, parent := applicationTestPair(t)
	rescue, _ := applicationTestPair(t)
	coordinator.Lock()
	oldGate, oldUsed := coordinator.gate, coordinator.used
	coordinator.gate, coordinator.used = nil, false
	coordinator.Unlock()
	t.Cleanup(func() {
		coordinator.Lock()
		coordinator.gate, coordinator.used = oldGate, oldUsed
		coordinator.Unlock()
	})
	page, nonce := testWitnessFile(t, nil)
	endReader, endWriter, err := os.Pipe()
	if err != nil {
		t.Fatal("capture pipe")
	}
	t.Cleanup(func() { endReader.Close(); endWriter.Close() })
	cleanup, err := StartCIController(normal, rescue, page, endReader, nonce, nil)
	if err != nil {
		t.Fatal("controller install")
	}
	t.Cleanup(cleanup)
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	parent.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(parent)
	if _, err := reader.ReadBytes('\n'); err != nil {
		t.Fatal("ready")
	}
	g.mu.Lock()
	g.seal = &sealAttempt{epoch: 1, released: true, captureEnded: true, recoveryAuthorized: true}
	g.mu.Unlock()
	return g, normal, parent, reader, cleanup
}

func TestIntegrationFinalQuiesceHandlerProtectsACKFromCleanup(t *testing.T) {
	g, _, parent, reader, cleanup := finalTransportFixture(t)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, proceed := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-proceed:
		default:
			close(proceed)
		}
	}()
	ObserveCIWorkerContext(root)
	ConfigureCIShutdown(func() { cancel(); close(entered); <-proceed })
	if g.prepareFinalQuiesce(3000) != nil {
		t.Fatal("prepare")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "final_cancel"}, 1024) != nil {
		t.Fatal("cancel request")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stop not entered")
	}
	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	select {
	case <-done:
		t.Fatal("cleanup crossed in-flight cancellation/ACK")
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal("cleanup cut final ACK")
	}
	var ack map[string]any
	if json.Unmarshal(line, &ack) != nil || len(ack) != 7 || ack["version"] != float64(2) || ack["epoch"] != float64(1) || ack["seq"] != float64(1) || ack["kind"] != "quiesce" || ack["status"] != "cancelled" || ack["pending"] != float64(0) || ack["open"] != float64(0) {
		t.Fatal("final ACK schema")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not join")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.invalid || g.finalParked || g.finalQuiesce.state != "complete" {
		t.Fatal("successful handshake did not complete cleanly")
	}
}

func TestIntegrationFinalQuiesceCleanupFirstDoesNotCancelOrACK(t *testing.T) {
	_, _, parent, reader, cleanup := finalTransportFixture(t)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	ObserveCIWorkerContext(root)
	ConfigureCIShutdown(func() { calls.Add(1); cancel() })
	cleanup()
	_ = writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "final_cancel"}, 1024)
	if _, err := reader.ReadBytes('\n'); err == nil {
		t.Fatal("closed controller supplied an ACK")
	}
	if calls.Load() != 0 {
		t.Fatal("cleanup-first invoked shutdown callback")
	}
}

func TestIntegrationFinalQuiesceBlockedACKFailsAndUnparks(t *testing.T) {
	g, normal, parent, _, cleanup := finalTransportFixture(t)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	ObserveCIWorkerContext(root)
	ConfigureCIShutdown(func() { cancel(); close(entered) })
	if g.prepareFinalQuiesce(1000) != nil {
		t.Fatal("prepare")
	}
	payload := make([]byte, 8<<20)
	normal.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
	n, err := normal.Write(payload)
	if err == nil || n <= 0 {
		t.Fatal("owned output saturation fixture failed")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "final_cancel"}, 1024) != nil {
		t.Fatal("cancel request")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stop not entered")
	}
	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked ACK prevented bounded cleanup")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.invalid || g.finalParked || g.finalQuiesce.state != "failed" {
		t.Fatal("failed ACK erased failure or retained park")
	}
}

func TestIntegrationFinalQuiesceNormalEOFAfterPrepareUnparks(t *testing.T) {
	g, _, parent, _, _ := finalTransportFixture(t)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	ObserveCIWorkerContext(root)
	ConfigureCIShutdown(cancel)
	if g.prepareFinalQuiesce(2000) != nil {
		t.Fatal("prepare")
	}
	parent.Close()
	deadline := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		ready := g.invalid && !g.finalParked && g.finalQuiesce.state == "failed"
		changed := g.changed
		g.mu.Unlock()
		if ready {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("normal EOF left final admission parked")
		}
		select {
		case <-changed:
		case <-time.After(10 * time.Millisecond):
		}
	}
}
