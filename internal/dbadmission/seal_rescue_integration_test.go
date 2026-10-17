//go:build linux && integration && adtr_ci_dbadmission

package dbadmission

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This is an isolated test-helper supervisor, not an application bootstrap or
// capture guard. Its target uses a synthetic registry: PostgreSQL safety is
// established separately by the database integration tests.
const sealRescueRole = "ADTR_CI_SEAL_RESCUE_TEST_ROLE"

// Child stderr is bounded and only closed diagnostic vocabularies are emitted
// into CI output. Unexpected runtime output is never copied into artifacts.
type sealRescueDiagnostic struct {
	mu   sync.Mutex
	data []byte
}

func (d *sealRescueDiagnostic) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	if remaining := 4096 - len(d.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		d.data = append(d.data, p...)
	}
	return n, nil
}

func (d *sealRescueDiagnostic) summary() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	roles := map[string]bool{"target": true, "controller": true, "rescuer": true, "deadline_probe": true}
	stages := map[string]bool{"helper": true, "control_setup": true, "pipes": true, "target_start": true, "identity": true, "ready": true, "pin": true, "stop": true, "control_deadline": true, "control_read": true, "resume": true, "release": true, "reap": true}
	reasons := map[string]bool{"deadline_unsupported": true, "deadline_exceeded": true, "eof": true, "partial": true, "other": true}
	var accepted []string
	for _, line := range strings.Split(string(d.data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 4 && fields[0] == "seal_helper" && roles[fields[1]] && stages[fields[2]] && reasons[fields[3]] {
			accepted = append(accepted, strings.Join(fields[1:], ":"))
		}
	}
	if len(accepted) == 0 {
		return "unclassified"
	}
	return strings.Join(accepted, ",")
}

func sealRescueErrorClass(err error) string {
	switch {
	case errors.Is(err, os.ErrNoDeadline):
		return "deadline_unsupported"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "partial"
	default:
		return "other"
	}
}

type sealRescueIdentity struct {
	pid   int
	start string
	exe   string
}

func sealRescueReadIdentity(pid int) (sealRescueIdentity, byte, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return sealRescueIdentity{}, 0, errors.New("identity unavailable")
	}
	// comm may contain spaces and parentheses. Fields after its final ')' start
	// at stat field 3; starttime is field 22, hence index 19 below.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return sealRescueIdentity{}, 0, errors.New("invalid identity")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 20 || len(fields[0]) != 1 {
		return sealRescueIdentity{}, 0, errors.New("invalid identity")
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return sealRescueIdentity{}, 0, errors.New("executable unavailable")
	}
	return sealRescueIdentity{pid: pid, start: fields[19], exe: exe}, fields[0][0], nil
}

func sealRescueCheck(id sealRescueIdentity) (byte, error) {
	got, state, err := sealRescueReadIdentity(id.pid)
	if err != nil || got != id {
		return 0, errors.New("owned child identity mismatch")
	}
	return state, nil
}

func sealRescueWaitState(id sealRescueIdentity, stopped bool, deadline time.Time) error {
	for time.Now().Before(deadline) {
		state, err := sealRescueCheck(id)
		if err != nil {
			return err
		}
		isStopped := state == 'T' || state == 't'
		if isStopped == stopped && state != 'Z' && state != 'X' {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("owned child state deadline")
}

func sealRescueCommand(role, scenario string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSealRescueProcessHelper$")
	// Do not inherit fixture credentials or application configuration. The
	// executable is already built by the CI runner, and uses absolute paths.
	cmd.Env = []string{sealRescueRole + "=" + role, "ADTR_CI_SEAL_RESCUE_SCENARIO=" + scenario}
	return cmd
}

func TestSealRescueProcessHelper(t *testing.T) {
	role := os.Getenv(sealRescueRole)
	if role == "" {
		return
	}
	var err error
	switch role {
	case "target":
		gate, target := newSealProtocolTestGate()
		ready := os.NewFile(3, "owned-target-ready")
		_, err = fmt.Fprintln(ready, target)
		ready.Close()
		if err == nil {
			err = runSealProtocol(context.Background(), os.Stdin, os.Stdout, gate)
		}
		if err == nil {
			if gate.frame(1).Status != "released" {
				err = errors.New("target did not release admission")
			} else {
				// Verify actual admission after RELEASE, without dialing PG.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				ticket, acquireErr := gate.acquire(ctx)
				cancel()
				if acquireErr != nil || ticket == nil {
					err = errors.New("released target admission remained closed")
				} else {
					ticket.failed() // Synthetic startup has no physical connection.
				}
			}
		}
	case "deadline_probe":
		var control *os.File
		control, err = sealRescueInheritedControl()
		if err == nil {
			defer control.Close()
			err = control.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			if err == nil {
				var one [1]byte
				n, readErr := control.Read(one[:])
				if n != 0 || !errors.Is(readErr, os.ErrDeadlineExceeded) {
					err = errors.New("inherited control did not enforce deadline")
				}
			}
		}
	case "controller":
		err = sealRescueController()
	case "rescuer":
		err = sealRescueSupervisor(os.Getenv("ADTR_CI_SEAL_RESCUE_SCENARIO"))
	default:
		err = errors.New("unknown helper role")
	}
	if err != nil {
		// Role and error class are closed vocabularies; never print err itself.
		if role == "target" || role == "controller" || role == "rescuer" || role == "deadline_probe" {
			fmt.Fprintln(os.Stderr, "seal_helper", role, "helper", sealRescueErrorClass(err))
		}
		os.Exit(1)
	}
	os.Exit(0)
}

func sealRescueController() error {
	control := os.NewFile(3, "owned-rescue-control")
	defer control.Close()
	input := bufio.NewReader(os.Stdin)
	if _, err := input.ReadByte(); err != nil {
		return err
	}
	switch os.Getenv("ADTR_CI_SEAL_RESCUE_SCENARIO") {
	case "normal", "wrong_starttime", "wrong_executable":
		_, err := io.WriteString(control, "release\n")
		return err
	case "partial":
		_, err := io.WriteString(control, "rel")
		return err
	case "eof", "deadline_expired":
		return nil
	case "timeout":
		// Keep the pipe open without completing any frame. The rescuer's own
		// absolute read deadline must recover the target without parent help.
		_, err := input.ReadByte()
		return err
	default:
		return errors.New("invalid controller scenario")
	}
}

func sealRescueInheritedControl() (*os.File, error) {
	// ExtraFiles inherits a blocking descriptor. NewFile only registers such
	// inherited pipes with Go's poller when O_NONBLOCK is already set. Without
	// this, SetReadDeadline returns ErrNoDeadline rather than bounding rescue.
	// This descriptor is the private read end, never a shared application FD.
	if err := syscall.SetNonblock(3, true); err != nil {
		return nil, err
	}
	return os.NewFile(3, "owned-rescue-control"), nil
}

func sealRescueSupervisor(scenario string) (result error) {
	stage := "control_setup"
	defer func() {
		if result != nil {
			fmt.Fprintln(os.Stderr, "seal_helper", "rescuer", stage, sealRescueErrorClass(result))
		}
	}()
	// Keep the Linux parent thread alive until its target is reaped. If the
	// rescuer itself is lost, Pdeathsig terminally cleans its own target.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	control, err := sealRescueInheritedControl()
	if err != nil {
		return err
	}
	defer control.Close()
	stage = "pipes"
	requestsR, requestsW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer requestsR.Close()
	defer requestsW.Close()
	repliesR, repliesW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer repliesR.Close()
	defer repliesW.Close()
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyR.Close()
	defer readyW.Close()
	worker := sealRescueCommand("target", scenario)
	worker.ExtraFiles = []*os.File{readyW}
	worker.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	worker.Stdin, worker.Stdout = requestsR, repliesW
	worker.Stderr = os.Stderr
	stage = "target_start"
	if err := worker.Start(); err != nil {
		return err
	}
	requestsR.Close()
	repliesW.Close()
	readyW.Close()
	waited := false
	defer func() {
		// This handle belongs to the exact child created above. An unreaped
		// direct child cannot have its PID reused before Wait.
		if !waited {
			_ = worker.Process.Kill()
			_ = worker.Wait()
		}
	}()
	stage = "identity"
	id, _, err := sealRescueReadIdentity(worker.Process.Pid)
	if err != nil {
		return err
	}
	self, err := os.Readlink("/proc/self/exe")
	if err != nil || id.exe != self {
		return errors.New("unexpected child executable")
	}
	// Obtain the actual target generation over a separate inherited readiness
	// pipe, never by assuming process-global counters match.
	stage = "ready"
	if err := readyR.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	readyBytes, err := io.ReadAll(io.LimitReader(readyR, 32))
	if err != nil || len(readyBytes) == 32 || !strings.HasSuffix(string(readyBytes), "\n") {
		return errors.New("invalid target readiness")
	}
	target, err := strconv.ParseUint(strings.TrimSuffix(string(readyBytes), "\n"), 10, 64)
	if err != nil || target == 0 {
		return errors.New("invalid target generation")
	}
	captureDeadline := time.Now().Add(1500 * time.Millisecond)
	encoder, decoder := json.NewEncoder(requestsW), json.NewDecoder(repliesR)
	exchange := func(op string, want string, deadline time.Time) error {
		if err := repliesR.SetReadDeadline(deadline); err != nil {
			return err
		}
		if err := requestsW.SetWriteDeadline(deadline); err != nil {
			return err
		}
		request := sealRequest{Version: 1, Epoch: 1, Op: op}
		if op == "pin" {
			request.Target = target
		}
		if err := encoder.Encode(request); err != nil {
			return err
		}
		var frame sealFrame
		if err := decoder.Decode(&frame); err != nil {
			return err
		}
		if frame.Version != 1 || frame.Epoch != 1 || frame.Status != want {
			return errors.New("unexpected protocol state")
		}
		return nil
	}
	stage = "pin"
	if err := exchange("pin", "sealed", captureDeadline); err != nil {
		return err
	}
	stage = "stop"
	stopCount := 0
	errCaptureExpired := errors.New("capture deadline elapsed")
	stopOwned := func() error {
		if !time.Now().Before(captureDeadline) {
			return errCaptureExpired
		}
		if _, err := sealRescueCheck(id); err != nil {
			return err
		}
		// Identity verification consumes the same budget; never reset it.
		if !time.Now().Before(captureDeadline) {
			return errCaptureExpired
		}
		stopCount++
		return worker.Process.Signal(syscall.SIGSTOP)
	}
	if scenario == "deadline_expired" {
		time.Sleep(time.Until(captureDeadline))
		if err := stopOwned(); !errors.Is(err, errCaptureExpired) || stopCount != 0 {
			return errors.New("expired capture attempted STOP")
		}
		cleanupDeadline := time.Now().Add(2 * time.Second)
		if err := sealRescueWaitState(id, false, cleanupDeadline); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "deadline_expired_zero_stop")
		if err := exchange("release", "released", cleanupDeadline); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "no_stop_release_acknowledged")
		requestsW.Close()
		if err := worker.Wait(); err != nil {
			return err
		}
		waited = true
		fmt.Fprintln(os.Stdout, "target_reaped")
		return nil
	}
	if err := stopOwned(); err != nil {
		return err
	}
	if stopCount != 1 {
		return errors.New("capture did not use exactly one STOP")
	}
	if err := sealRescueWaitState(id, true, captureDeadline); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "armed_one_stop")
	stage = "control_deadline"
	if err := control.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	stage = "control_read"
	frame := make([]byte, len("release\n"))
	_, readErr := io.ReadFull(control, frame)
	switch {
	case readErr == nil && string(frame) == "release\n":
		fmt.Fprintln(os.Stdout, "normal_release")
	case errors.Is(readErr, io.EOF):
		fmt.Fprintln(os.Stdout, "control_eof")
	case errors.Is(readErr, io.ErrUnexpectedEOF):
		fmt.Fprintln(os.Stdout, "control_partial")
	case errors.Is(readErr, os.ErrDeadlineExceeded):
		fmt.Fprintln(os.Stdout, "control_timeout")
	default:
		return errors.New("unexpected control result")
	}
	// Recovery has its own safety bound; it cannot extend capture eligibility.
	stage = "resume"
	recoveryDeadline := time.Now().Add(2 * time.Second)
	expected := id
	if scenario == "wrong_starttime" {
		expected.start += "0"
	}
	if scenario == "wrong_executable" {
		expected.exe += ".wrong"
	}
	if _, err := sealRescueCheck(expected); err != nil {
		if scenario != "wrong_starttime" && scenario != "wrong_executable" {
			return err
		}
		// Rejected identity must never reach CONT or RELEASE. Observe that the
		// actual owned child is still stopped before safe terminal cleanup.
		if err := sealRescueWaitState(id, true, recoveryDeadline); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "identity_rejected_still_stopped")
		if err := worker.Process.Kill(); err != nil {
			return err
		}
		_ = worker.Wait()
		waited = true
		fmt.Fprintln(os.Stdout, "target_reaped")
		return nil
	}
	if err := worker.Process.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	if err := sealRescueWaitState(id, false, recoveryDeadline); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "continued_and_running")
	stage = "release"
	if err := exchange("release", "released", recoveryDeadline); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "release_acknowledged")
	stage = "reap"
	requestsW.Close()
	if err := worker.Wait(); err != nil {
		return err
	}
	waited = true
	fmt.Fprintln(os.Stdout, "target_reaped")
	return nil
}

func TestIntegrationSealRescueIndependentParentDeath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatal("isolated Linux CI runner with procfs required")
	}
	for _, scenario := range []string{"normal", "parent_death", "eof", "partial", "timeout", "wrong_starttime", "wrong_executable", "deadline_expired"} {
		t.Run(scenario, func(t *testing.T) {
			sealRescueRunScenario(t, scenario)
		})
	}
}

func sealRescueRunScenario(t *testing.T, scenario string) {
	t.Helper()
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal("control pipe failed")
	}
	defer controlR.Close()
	defer controlW.Close()
	rescuer := sealRescueCommand("rescuer", scenario)
	rescuerDiagnostic := &sealRescueDiagnostic{}
	rescuer.Stderr = rescuerDiagnostic
	rescuer.ExtraFiles = []*os.File{controlR}
	events, err := rescuer.StdoutPipe()
	if err != nil {
		t.Fatal("event pipe failed")
	}
	controller := sealRescueCommand("controller", scenario)
	controllerDiagnostic := &sealRescueDiagnostic{}
	controller.Stderr = controllerDiagnostic
	controller.ExtraFiles = []*os.File{controlW}
	commands, err := controller.StdinPipe()
	if err != nil {
		t.Fatal("command pipe failed")
	}
	defer commands.Close()
	if err := controller.Start(); err != nil {
		t.Fatal("controller start failed")
	}
	controllerWaited := false
	defer func() {
		if !controllerWaited {
			_ = controller.Process.Kill()
			_ = controller.Wait()
		}
	}()
	if err := rescuer.Start(); err != nil {
		t.Fatal("rescuer start failed")
	}
	controlR.Close()
	controlW.Close() // Only the controller holds the writer now.
	lines := make(chan string, 16)
	rescuerDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(events)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
		// Read EOF before Wait closes the stdout pipe.
		rescuerDone <- rescuer.Wait()
	}()
	rescuerWaited := false
	defer func() {
		if rescuerWaited {
			return
		}
		// Closing controller ownership triggers independent rescue even when an
		// assertion fails. Give that path its bounded cleanup window first.
		_ = controller.Process.Kill()
		select {
		case <-time.After(7 * time.Second):
			_ = rescuer.Process.Kill()
			<-rescuerDone
		case <-rescuerDone:
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok {
				select {
				case <-rescuerDone:
					rescuerWaited = true
					t.Fatalf("rescuer exited before required evidence: exit=%d diagnostic=%s", rescuer.ProcessState.ExitCode(), rescuerDiagnostic.summary())
				case <-time.After(3 * time.Second):
					t.Fatalf("rescuer output closed before exit: diagnostic=%s", rescuerDiagnostic.summary())
				}
			}
			return line
		case <-time.After(8 * time.Second):
			t.Fatal("rescue evidence deadline exceeded")
			return ""
		}
	}
	expired := scenario == "deadline_expired"
	firstEvent := next()
	if expired {
		if firstEvent != "deadline_expired_zero_stop" {
			t.Fatal("expired capture was not rejected before STOP")
		}
	} else if firstEvent != "armed_one_stop" {
		t.Fatal("exactly one target stop was not confirmed")
	}
	if scenario == "parent_death" {
		if err := controller.Process.Kill(); err != nil {
			t.Fatal("owned controller death injection failed")
		}
		if err := controller.Wait(); err == nil {
			t.Fatal("controller death injection was not observed")
		}
		controllerWaited = true
	} else if _, err := io.WriteString(commands, "x"); err != nil {
		t.Fatal("controller command failed")
	}
	if expired {
		if next() != "no_stop_release_acknowledged" {
			t.Fatal("expired capture did not release without STOP")
		}
	} else {
		wantTrigger := map[string]string{
			"normal": "normal_release", "parent_death": "control_eof", "eof": "control_eof",
			"partial": "control_partial", "timeout": "control_timeout",
			"wrong_starttime": "normal_release", "wrong_executable": "normal_release",
		}[scenario]
		if next() != wantTrigger {
			t.Fatal("incorrect recovery trigger")
		}
		if strings.HasPrefix(scenario, "wrong_") {
			if next() != "identity_rejected_still_stopped" {
				t.Fatal("identity mismatch did not reject resume")
			}
		} else {
			if next() != "continued_and_running" || next() != "release_acknowledged" {
				t.Fatal("CONT did not precede acknowledged release")
			}
		}
	}
	if next() != "target_reaped" {
		t.Fatal("owned target was not reaped")
	}
	select {
	case _, ok := <-lines:
		if ok {
			t.Fatal("unexpected additional rescue event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rescue output remained open")
	}
	// The helper reports no raw process identities in artifacts. Direct Wait
	// establishes both helper exits; target Wait was asserted above.
	if err := <-rescuerDone; err != nil {
		rescuerWaited = true
		t.Fatalf("rescue helper failed: exit=%d diagnostic=%s", rescuer.ProcessState.ExitCode(), rescuerDiagnostic.summary())
	}
	rescuerWaited = true
	if !controllerWaited {
		if scenario == "timeout" {
			_, _ = io.WriteString(commands, "x")
		}
		if err := controller.Wait(); err != nil {
			t.Fatalf("controller cleanup failed: exit=%d diagnostic=%s", controller.ProcessState.ExitCode(), controllerDiagnostic.summary())
		}
		controllerWaited = true
	}
}

// Exercise the actual inherited ExtraFiles descriptor, not an os.Pipe file
// created in this process. Keeping the writer open excludes EOF as success.
func TestIntegrationSealRescueInheritedPipeDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal("probe pipe failed")
	}
	defer r.Close()
	defer w.Close()
	cmd := sealRescueCommand("deadline_probe", "")
	cmd.ExtraFiles = []*os.File{r}
	diagnostic := &sealRescueDiagnostic{}
	cmd.Stderr = diagnostic
	if err := cmd.Start(); err != nil {
		t.Fatal("deadline probe start failed")
	}
	r.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("inherited pipe deadline probe failed: exit=%d diagnostic=%s", cmd.ProcessState.ExitCode(), diagnostic.summary())
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("inherited pipe ignored its deadline")
	}
}

// Keep the stat field interpretation explicit and independently reviewable.
func TestSealRescueIdentityStatStarttimeIsNumeric(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatal("Linux procfs required")
	}
	id, _, err := sealRescueReadIdentity(os.Getpid())
	if err != nil {
		t.Fatal("test process identity unavailable")
	}
	if _, err := strconv.ParseUint(id.start, 10, 64); err != nil {
		t.Fatal("process starttime is not a kernel clock count")
	}
}
