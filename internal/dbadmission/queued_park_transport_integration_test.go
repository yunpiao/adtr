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

func TestIntegrationQueuedParkingEOFReleasesParkAndKeepsRescueLive(t *testing.T) {
	normal, parent := applicationTestPair(t)
	rescue, supervisor := applicationTestPair(t)
	coordinator.Lock()
	priorGate, priorUsed := coordinator.gate, coordinator.used
	coordinator.gate, coordinator.used = nil, false
	coordinator.Unlock()
	defer func() {
		coordinator.Lock()
		coordinator.gate, coordinator.used = priorGate, priorUsed
		coordinator.Unlock()
	}()
	page, nonce := testWitnessFile(t, nil)
	endReader, endWriter, err := os.Pipe()
	if err != nil {
		t.Fatal("owned capture pipe")
	}
	defer endReader.Close()
	defer endWriter.Close()
	failed := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := StartCIController(normal, rescue, page, endReader, nonce, func() {
		cancel()
		select {
		case failed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal("controller installation")
	}
	defer cleanup()
	ObserveCIWorkerContext(ctx)
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	if parent.SetDeadline(time.Now().Add(2*time.Second)) != nil {
		t.Fatal("normal deadline")
	}
	reader := bufio.NewReader(parent)
	read := func() applicationFrame {
		t.Helper()
		line, err := reader.ReadBytes('\n')
		var frame applicationFrame
		if err != nil || json.Unmarshal(line, &frame) != nil {
			t.Fatal("normal frame")
		}
		return frame
	}
	if frame := read(); frame.Kind != "ready" || frame.Status != "open" {
		t.Fatal("ready frame")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "prepare_selection", Kind: testSealIdentity().Kind, BudgetMillis: 1000}, 1024) != nil {
		t.Fatal("prepare request")
	}
	frame := read()
	if frame.Version != 2 || frame.Epoch != 1 || frame.Seq != 1 || frame.Kind != "reply" || frame.Status != "open" || frame.Pending != 0 || frame.Open != 0 || frame.Target != 0 || len(frame.Records) != 0 {
		t.Fatal("prepare did not prove empty admission")
	}
	g.mu.Lock()
	parked := g.queuedParked && g.queuedState == "drained"
	g.mu.Unlock()
	if !parked {
		t.Fatal("ACK lost park")
	}
	parent.Close()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("normal EOF did not notify failure")
	}
	g.mu.Lock()
	opened := !g.queuedParked && g.queuedState == "failed" && g.invalid
	g.mu.Unlock()
	if !opened || atomic.LoadUint32(g.witness.(*ciWitness).invalid) != 1 {
		t.Fatal("EOF did not permanently fail and wake parking")
	}
	if supervisor.SetDeadline(time.Now().Add(time.Second)) != nil || writeApplicationFrame(supervisor, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "abort"}, 256) != nil {
		t.Fatal("independent rescue request")
	}
	line, err := bufio.NewReader(supervisor).ReadBytes('\n')
	var ack map[string]any
	if err != nil || json.Unmarshal(line, &ack) != nil || len(ack) != 5 || ack["version"] != float64(2) || ack["epoch"] != float64(1) || ack["seq"] != float64(1) || ack["kind"] != "reply" || ack["status"] != "released" {
		t.Fatal("independent rescue ACK")
	}
	if atomic.LoadUint32(g.witness.(*ciWitness).invalid) != 1 {
		t.Fatal("rescue erased failure")
	}
}

func TestIntegrationQueuedParkingConfigureACKFailureUnparksAndFails(t *testing.T) {
	normal, parent := applicationTestPair(t)
	rescue, supervisor := applicationTestPair(t)
	coordinator.Lock()
	priorGate, priorUsed := coordinator.gate, coordinator.used
	coordinator.gate, coordinator.used = nil, false
	coordinator.Unlock()
	defer func() {
		coordinator.Lock()
		coordinator.gate, coordinator.used = priorGate, priorUsed
		coordinator.Unlock()
	}()
	page, nonce := testWitnessFile(t, nil)
	endReader, endWriter, err := os.Pipe()
	if err != nil {
		t.Fatal("owned capture pipe")
	}
	defer endReader.Close()
	defer endWriter.Close()
	failed := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup, err := StartCIController(normal, rescue, page, endReader, nonce, func() {
		cancel()
		select {
		case failed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal("controller installation")
	}
	defer cleanup()
	ObserveCIWorkerContext(ctx)
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	if parent.SetDeadline(time.Now().Add(2*time.Second)) != nil {
		t.Fatal("normal deadline")
	}
	reader := bufio.NewReader(parent)
	read := func() applicationFrame {
		t.Helper()
		line, err := reader.ReadBytes('\n')
		var frame applicationFrame
		if err != nil || json.Unmarshal(line, &frame) != nil {
			t.Fatal("normal frame")
		}
		return frame
	}
	if frame := read(); frame.Kind != "ready" || frame.Status != "open" {
		t.Fatal("ready frame")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "prepare_selection", Kind: testSealIdentity().Kind, BudgetMillis: 1000}, 1024) != nil {
		t.Fatal("prepare request")
	}
	frame := read()
	if frame.Version != 2 || frame.Epoch != 1 || frame.Seq != 1 || frame.Kind != "reply" || frame.Status != "open" || frame.Pending != 0 || frame.Open != 0 || frame.Target != 0 || len(frame.Records) != 0 {
		t.Fatal("prepare did not prove empty admission")
	}
	g.mu.Lock()
	parked := g.queuedParked && g.queuedState == "drained"
	g.mu.Unlock()
	if !parked {
		t.Fatal("ACK lost park")
	}
	payload := make([]byte, 8<<20)
	normal.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
	n, saturated := normal.Write(payload)
	if saturated == nil || n <= 0 {
		t.Fatal("owned output saturation fixture")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 2, Op: "configure_target", TaskID: testSealIdentity().TaskID, WorkerID: "private-worker"}, 1024) != nil {
		t.Fatal("configuration request")
	}
	configuredDeadline := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		configured := g.queuedState == "configured" && g.queuedParked && !g.invalid
		changed := g.changed
		g.mu.Unlock()
		if configured {
			break
		}
		if !time.Now().Before(configuredDeadline) {
			t.Fatal("configuration did not retain its admission park")
		}
		select {
		case <-changed:
		case <-time.After(5 * time.Millisecond):
		}
	}
	admitCtx, admitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer admitCancel()
	if ticket, err := g.acquire(admitCtx); ticket != nil || err != context.DeadlineExceeded {
		t.Fatal("configuration admitted a connection before ACK")
	}
	g.mu.Lock()
	allocated := g.total
	g.mu.Unlock()
	if allocated != 0 {
		t.Fatal("parked admission allocated a ticket")
	}
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked configuration ACK did not fail")
	}
	g.mu.Lock()
	opened := !g.queuedParked && g.queuedState == "failed" && g.invalid
	g.mu.Unlock()
	if !opened || atomic.LoadUint32(g.witness.(*ciWitness).invalid) != 1 {
		t.Fatal("failed configuration ACK did not permanently fail and wake parking")
	}
	if supervisor.SetDeadline(time.Now().Add(time.Second)) != nil || writeApplicationFrame(supervisor, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "abort"}, 256) != nil {
		t.Fatal("independent rescue request")
	}
	line, err := bufio.NewReader(supervisor).ReadBytes('\n')
	var ack map[string]any
	if err != nil || json.Unmarshal(line, &ack) != nil || len(ack) != 5 || ack["version"] != float64(2) || ack["epoch"] != float64(1) || ack["seq"] != float64(1) || ack["kind"] != "reply" || ack["status"] != "released" {
		t.Fatal("independent rescue ACK")
	}
	if atomic.LoadUint32(g.witness.(*ciWitness).invalid) != 1 {
		t.Fatal("rescue erased failure")
	}
}
