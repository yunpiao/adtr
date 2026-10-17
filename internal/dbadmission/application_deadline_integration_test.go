//go:build integration && adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// This process uses the real installer and transport. Its synthetic target
// isolates cancellation semantics; exporter/health/PG qualification is separate.
func TestApplicationDeadlineProcessHelper(t *testing.T) {
	if os.Getenv("ADTR_TEST_DEADLINE_CHILD") != "1" {
		return
	}
	for _, fd := range []int{3, 4} {
		if syscall.SetNonblock(fd, true) != nil {
			os.Exit(2)
		}
	}
	normal, rescue, page := os.NewFile(3, "normal"), os.NewFile(4, "rescue"), os.NewFile(5, "page")
	ready, proceed := os.NewFile(6, "ready"), os.NewFile(7, "proceed")
	var nonce [16]byte
	nonce[0] = 9
	endReader, endWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal("capture end pipe")
	}
	defer endReader.Close()
	defer endWriter.Close()
	cleanup, err := StartCIController(normal, rescue, page, endReader, nonce, nil)
	if err != nil {
		os.Exit(3)
	}
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	ticket, err := g.acquire(context.Background())
	if err != nil {
		os.Exit(4)
	}
	conn := attachTestTicket(ticket)
	if _, err = g.register(conn, testSealIdentity()); err != nil {
		os.Exit(5)
	}
	if _, err = ready.Write([]byte{1}); err != nil {
		os.Exit(6)
	}
	var token [1]byte
	if _, err = proceed.Read(token[:]); err != nil {
		os.Exit(7)
	}
	results := make(chan error, 2)
	for _, duration := range []time.Duration{2 * time.Second, 5 * time.Second} {
		ctx, cancel := context.WithTimeout(context.Background(), duration)
		defer cancel()
		go func() { _, e := g.acquire(ctx); results <- e }()
	}
	if _, err = ready.Write([]byte{1}); err != nil {
		os.Exit(8)
	}
	for i := 0; i < 2; i++ {
		if <-results != context.DeadlineExceeded {
			os.Exit(9)
		}
	}
	if _, err = proceed.Read(token[:]); err != nil {
		os.Exit(10)
	}
	if atomic.LoadUint32(g.witness.(*ciWitness).invalid) != 1 {
		os.Exit(11)
	}
	cleanup()
	os.Exit(0)
}
func TestIntegrationApplicationStoppedPastCallbackAndHeartbeat(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	normal, parent := applicationTestPair(t)
	rescue, supervisor := applicationTestPair(t)
	page, _ := testWitnessFile(t, nil)
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal("pipe")
	}
	defer readyR.Close()
	defer readyW.Close()
	proceedR, proceedW, err := os.Pipe()
	if err != nil {
		t.Fatal("pipe")
	}
	defer proceedR.Close()
	defer proceedW.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationDeadlineProcessHelper$")
	cmd.Env = []string{"ADTR_TEST_DEADLINE_CHILD=1"}
	cmd.ExtraFiles = []*os.File{normal, rescue, page, readyW, proceedR}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if cmd.Start() != nil {
		t.Fatal("child start")
	}
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Signal(syscall.SIGCONT)
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	normal.Close()
	rescue.Close()
	readyW.Close()
	proceedR.Close()
	readyR.SetReadDeadline(time.Now().Add(5 * time.Second))
	var token [1]byte
	if _, err = readyR.Read(token[:]); err != nil {
		t.Fatal("child registered")
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	parent.SetDeadline(deadline)
	reader := bufio.NewReader(parent)
	read := func() applicationFrame {
		t.Helper()
		line, e := reader.ReadBytes('\n')
		if e != nil {
			t.Fatal("normal frame")
		}
		var f applicationFrame
		if json.Unmarshal(line, &f) != nil {
			t.Fatal("frame decode")
		}
		return f
	}
	if read().Kind != "ready" {
		t.Fatal("ready")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "snapshot"}, 1024) != nil {
		t.Fatal("snapshot send")
	}
	f := read()
	if len(f.Records) != 1 || f.Records[0].Candidate == nil {
		t.Fatal("candidate")
	}
	if writeApplicationFrame(parent, applicationRequest{Version: 2, Epoch: 1, Seq: 2, Op: "pin", Target: f.Records[0].Candidate.Generation}, 1024) != nil {
		t.Fatal("pin send")
	}
	if read().Status != "sealed" {
		t.Fatal("seal")
	}
	if _, err = proceedW.Write([]byte{1}); err != nil {
		t.Fatal("waiter start")
	}
	readyR.SetReadDeadline(deadline)
	if _, err = readyR.Read(token[:]); err != nil {
		t.Fatal("waiters ready")
	}
	identity, _, err := sealRescueReadIdentity(cmd.Process.Pid)
	if err != nil || !time.Now().Before(deadline) {
		t.Fatal("capture identity deadline")
	}
	if cmd.Process.Signal(syscall.SIGSTOP) != nil || waitStickyAllThreads(identity, deadline) != nil {
		t.Fatal("all threads stopped")
	}
	data := make([]byte, 4096)
	if _, err = page.ReadAt(data, 0); err != nil || binary.LittleEndian.Uint32(data[24:28]) != 0 {
		t.Fatal("initial witness")
	}
	// Deliberately exceeds the unmodified health callback and heartbeat contexts.
	time.Sleep(5500 * time.Millisecond)
	if cmd.Process.Signal(syscall.SIGCONT) != nil || sealRescueWaitState(identity, false, time.Now().Add(time.Second)) != nil {
		t.Fatal("continue")
	}
	parent.SetDeadline(time.Now().Add(2 * time.Second))
	if read().Kind != "invalid" {
		t.Fatal("expired waiter publication")
	}
	supervisor.SetDeadline(time.Now().Add(time.Second))
	if writeApplicationFrame(supervisor, applicationRequest{Version: 2, Epoch: 1, Seq: 1, Op: "abort"}, 256) != nil {
		t.Fatal("abort")
	}
	line, err := bufio.NewReader(supervisor).ReadBytes('\n')
	var ack map[string]any
	if err != nil || json.Unmarshal(line, &ack) != nil || ack["status"] != "released" {
		t.Fatal("release ack")
	}
	if _, err = proceedW.Write([]byte{1}); err != nil {
		t.Fatal("finish")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		waited = true
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
		waited = true
		t.Fatal("join")
	}
	if err != nil {
		t.Fatal("child terminal")
	}
	if _, err = page.ReadAt(data, 0); err != nil || binary.LittleEndian.Uint32(data[24:28]) != 1 {
		t.Fatal("final invalid lost")
	}
}
