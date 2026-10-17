//go:build integration && adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func applicationTestPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal("owned socketpair")
	}
	for _, fd := range fds {
		if syscall.SetNonblock(fd, true) != nil {
			t.Fatal("owned socket mode")
		}
	}
	a, b := os.NewFile(uintptr(fds[0]), "owned-a"), os.NewFile(uintptr(fds[1]), "owned-b")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func TestIntegrationApplicationNormalEOFLeavesRescueLive(t *testing.T) {
	normal, parent := applicationTestPair(t)
	rescue, supervisor := applicationTestPair(t)
	coordinator.Lock()
	priorGate, priorUsed := coordinator.gate, coordinator.used
	coordinator.gate = nil
	coordinator.used = false
	coordinator.Unlock()
	defer func() {
		coordinator.Lock()
		coordinator.gate = priorGate
		coordinator.used = priorUsed
		coordinator.Unlock()
	}()
	page, nonce := testWitnessFile(t, nil)
	failed := make(chan struct{}, 2)
	endReader, endWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal("capture end pipe")
	}
	defer endReader.Close()
	defer endWriter.Close()
	cleanup, err := StartCIController(normal, rescue, page, endReader, nonce, func() { failed <- struct{}{} })
	if err != nil {
		t.Fatal("application install")
	}
	defer cleanup()
	if _, err := StartCIController(normal, rescue, page, endReader, nonce, nil); err == nil {
		t.Fatal("second installation accepted")
	}
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	w := g.witness.(*ciWitness)
	parent.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(parent)
	read := func() applicationFrame {
		t.Helper()
		line, e := reader.ReadBytes('\n')
		if e != nil {
			t.Fatal("normal reply")
		}
		var frame applicationFrame
		if json.Unmarshal(line, &frame) != nil {
			t.Fatal("normal frame")
		}
		return frame
	}
	if f := read(); f.Kind != "ready" || f.Status != "open" || len(f.Records) != 0 {
		t.Fatal("ready incomplete")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "diagnostic"}, 1024) != nil {
		t.Fatal("diagnostic send")
	}
	diagnosticLine, diagnosticErr := reader.ReadBytes('\n')
	var diagnosticFields map[string]json.RawMessage
	var diagnostic applicationDiagnostic
	if diagnosticErr != nil || json.Unmarshal(diagnosticLine, &diagnosticFields) != nil ||
		json.Unmarshal(diagnosticLine, &diagnostic) != nil || len(diagnosticFields) != 10 {
		t.Fatal("diagnostic reply schema")
	}
	for _, name := range []string{"version", "epoch", "seq", "kind", "reason", "phase", "pending", "open", "total", "retained"} {
		if _, ok := diagnosticFields[name]; !ok {
			t.Fatal("diagnostic field missing")
		}
	}
	if diagnostic.Version != 2 || diagnostic.Epoch != 1 || diagnostic.Seq != 1 || diagnostic.Kind != "diagnostic" ||
		diagnostic.Reason != "none" || diagnostic.Phase != "open" || diagnostic.Total != 0 || diagnostic.Retained != 0 || diagnostic.Pending != 0 || diagnostic.Open != 0 {
		t.Fatal("fresh diagnostic values")
	}

	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 2, Op: "prepare_selection", Kind: testSealIdentity().Kind}, 1024) != nil {
		t.Fatal("prepare send")
	}
	if f := read(); f.Seq != 2 || f.Kind != "reply" || f.Status != "open" {
		t.Fatal("prepare ack")
	}
	waiting := make(chan error, 1)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	go func() { waiting <- AwaitActivitySelection(waitCtx, testSealIdentity()) }()
	select {
	case <-waiting:
		t.Fatal("prepare did not await")
	case <-time.After(20 * time.Millisecond):
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 3, Op: "configure_target", TaskID: testSealIdentity().TaskID, WorkerID: "private-worker"}, 1024) != nil {
		t.Fatal("configure send")
	}
	if f := read(); f.Seq != 3 || f.Kind != "reply" || f.Status != "open" {
		t.Fatal("configure ack")
	}
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatal("configured wait")
		}
	case <-waitCtx.Done():
		t.Fatal("configure did not wake")
	}
	ticket, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal("admission")
	}
	conn := attachTestTicket(ticket)
	if _, key, e := g.registerActivityRow(conn, testSealIdentity(), "private-worker"); e != nil || key != activityRowKey(nonce, testSealIdentity().TaskID) {
		t.Fatal("registration")
	}
	generation := g.candidates()[0].generation
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 4, Op: "pin", Target: generation}, 1024) != nil {
		t.Fatal("pin send")
	}
	if f := read(); f.Status != "sealed" || f.Target != generation {
		t.Fatal("pin reply")
	}
	parent.Close()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("EOF did not cancel business")
	}
	if atomic.LoadUint32(w.invalid) != 1 {
		t.Fatal("EOF did not stick")
	}
	supervisor.SetDeadline(time.Now().Add(time.Second))
	if writeApplicationFrame(supervisor, applicationRequest{Version: 2, Epoch: 1, Seq: 2, Op: "abort"}, 256) != nil {
		t.Fatal("rescue unavailable")
	}
	line, err := bufio.NewReader(supervisor).ReadBytes('\n')
	if err != nil {
		t.Fatal("rescue ack")
	}
	var ack map[string]any
	if json.Unmarshal(line, &ack) != nil || ack["status"] != "released" || len(ack) != 5 {
		t.Fatal("rescue frame")
	}
	g.mu.Lock()
	closing := g.closing
	g.mu.Unlock()
	if atomic.LoadUint32(w.invalid) != 1 || closing {
		t.Fatal("release erased failure or stayed closed")
	}
}
