//go:build integration && adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type stoppedGapWitness struct {
	actual         *ciWitness
	ready, proceed *os.File
	before         bool
}

func (w *stoppedGapWitness) invalidate() {
	if !w.before {
		w.actual.invalidate()
	}
	if _, err := w.ready.Write([]byte{1}); err != nil {
		os.Exit(3)
	}
	var token [1]byte
	if _, err := w.proceed.Read(token[:]); err != nil {
		os.Exit(4)
	}
	if w.before {
		w.actual.invalidate()
	}
}
func TestApplicationStickyProcessHelper(t *testing.T) {
	mode := os.Getenv("ADTR_TEST_STICKY_CHILD")
	if mode == "" {
		return
	}
	page := os.NewFile(3, "owned-page")
	ready := os.NewFile(4, "owned-ready")
	proceed := os.NewFile(5, "owned-proceed")
	var nonce [16]byte
	nonce[0] = 9
	w, err := mapCIWitness(page, nonce)
	if err != nil {
		os.Exit(2)
	}
	g := &lifecycleGate{witness: &stoppedGapWitness{w, ready, proceed, mode == "before"}}
	g.invalidateTransport()
	if !g.invalid || atomic.LoadUint32(w.invalid) != 1 {
		os.Exit(5)
	}
	os.Exit(0)
}
func TestIntegrationApplicationStickyStopPublicationGap(t *testing.T) {
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
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
			cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationStickyProcessHelper$")
			cmd.Env = []string{"ADTR_TEST_STICKY_CHILD=" + mode}
			cmd.ExtraFiles = []*os.File{page, readyW, proceedR}
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
			readyW.Close()
			proceedR.Close()
			readyR.SetReadDeadline(time.Now().Add(5 * time.Second))
			var ready [1]byte
			if _, err = readyR.Read(ready[:]); err != nil {
				t.Fatal("child ready")
			}
			deadline := time.Now().Add(1500 * time.Millisecond)
			identity, _, err := sealRescueReadIdentity(cmd.Process.Pid)
			if err != nil {
				t.Fatal("identity")
			}
			if !time.Now().Before(deadline) {
				t.Fatal("capture expired")
			}
			if cmd.Process.Signal(syscall.SIGSTOP) != nil {
				t.Fatal("stop")
			}
			if waitStickyAllThreads(identity, deadline) != nil {
				t.Fatal("all threads stop")
			}
			first, second := make([]byte, 4096), make([]byte, 4096)
			if _, err = page.ReadAt(first, 0); err != nil {
				t.Fatal("page read")
			}
			if _, err = page.ReadAt(second, 0); err != nil {
				t.Fatal("page reread")
			}
			want := uint32(1)
			if mode == "before" {
				want = 0
			}
			if binary.LittleEndian.Uint32(first[24:28]) != want || string(first) != string(second) {
				t.Fatal("stopped witness")
			}
			if cmd.Process.Signal(syscall.SIGCONT) != nil || sealRescueWaitState(identity, false, time.Now().Add(time.Second)) != nil {
				t.Fatal("continue")
			}
			if _, err = proceedW.Write([]byte{1}); err != nil {
				t.Fatal("release hook")
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
				t.Fatal("child wait")
			}
			if err != nil {
				t.Fatal("child failed")
			}
			if _, err = page.ReadAt(first, 0); err != nil || binary.LittleEndian.Uint32(first[24:28]) != 1 {
				t.Fatal("post-Wait invalid lost")
			}
		})
	}
}

func waitStickyAllThreads(id sealRescueIdentity, deadline time.Time) error {
	for time.Now().Before(deadline) {
		if _, err := sealRescueCheck(id); err != nil {
			return err
		}
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", id.pid))
		if err != nil {
			return err
		}
		all := len(entries) > 0
		for _, entry := range entries {
			data, e := os.ReadFile(filepath.Join(fmt.Sprintf("/proc/%d/task", id.pid), entry.Name(), "status"))
			if e != nil {
				return e
			}
			stopped := false
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "State:") {
					fields := strings.Fields(line)
					stopped = len(fields) > 1 && fields[1] == "T"
				}
			}
			all = all && stopped
		}
		if all {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errSealProtocol
}
