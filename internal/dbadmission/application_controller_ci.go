//go:build adtr_ci_dbadmission

package dbadmission

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Private wire identities are deliberately distinct from redacted ActivityIdentity.
// Only inherited, owned descriptors may transport these records.
type applicationCandidate struct {
	Generation    uint64 `json:"generation"`
	TaskID        string `json:"taskID"`
	Kind          string `json:"kind"`
	Attempt       int    `json:"attempt"`
	LeaseOwner    string `json:"leaseOwner"`
	FencingToken  int64  `json:"fencingToken"`
	ResultVersion int64  `json:"resultVersion"`
	Phase         string `json:"phase"`
	ActivityKey   string `json:"activityKey"`
}
type applicationRecord struct {
	Generation uint64                `json:"generation"`
	State      string                `json:"state"`
	Backend    uint32                `json:"backend"`
	Candidate  *applicationCandidate `json:"candidate"`
}
type applicationFrame struct {
	Version  int                 `json:"version"`
	Epoch    uint64              `json:"epoch"`
	Seq      uint64              `json:"seq"`
	Kind     string              `json:"kind"`
	Status   string              `json:"status"`
	Pending  int                 `json:"pending"`
	Open     int                 `json:"open"`
	Revision uint64              `json:"revision"`
	Target   uint64              `json:"target"`
	Records  []applicationRecord `json:"records"`
}
type applicationRequest struct {
	Version      int    `json:"version"`
	Epoch        uint64 `json:"epoch"`
	Seq          uint64 `json:"seq"`
	Op           string `json:"op"`
	Target       uint64 `json:"target,omitempty"`
	Kind         string `json:"kind,omitempty"`
	TaskID       string `json:"taskID,omitempty"`
	WorkerID     string `json:"workerID,omitempty"`
	BudgetMillis int64  `json:"budgetMillis,omitempty"`
}

func (g *lifecycleGate) applicationSnapshot(seq uint64, kind string) (applicationFrame, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f := applicationFrame{Version: 2, Epoch: 1, Seq: seq, Kind: kind, Status: "open", Pending: g.pending, Open: g.open, Revision: g.revision, Records: make([]applicationRecord, 0)}
	for _, t := range g.tickets {
		var state string
		switch t.state {
		case 'p':
			state = "pending"
		case 'o':
			state = "open"
		case 'x':
			state = "closing"
		default:
			continue
		}
		r := applicationRecord{Generation: t.generation, State: state, Backend: t.backend}
		if a := t.registration; a != nil {
			id := a.identity
			r.Candidate = &applicationCandidate{a.generation, id.TaskID, id.Kind, id.Attempt, id.LeaseOwner, id.FencingToken, id.ResultVersion, id.Phase, a.activityKey}
		}
		f.Records = append(f.Records, r)
	}
	if len(f.Records) > 128 {
		g.invalidateBecauseLocked(invalidRecordCapacity)
		g.notifyLocked()
		return f, errSealProtocol
	}
	sort.Slice(f.Records, func(i, j int) bool { return f.Records[i].Generation < f.Records[j].Generation })
	if s := g.seal; s != nil {
		f.Target = s.registration.generation
		f.Status = "draining"
		if s.released {
			f.Status = "released"
		} else if g.closing && g.pending == 0 && g.open == 1 && s.target.state == 'o' && s.target.registration == s.registration {
			f.Status = "sealed"
		}
	}
	if g.invalid {
		f.Status = "invalid"
	}
	return f, nil
}

// StartCIController is called only by the tagged worker bootstrap, before any
// managed connection. It is one-shot: no uninstall/reinstall and no page reuse.
// Cleanup closes transports, but the witness remains mapped until process exit.
func StartCIController(normal, rescue, witness, captureEnd *os.File, nonce [16]byte, onFailure func()) (func(), error) {
	coordinator.Lock()
	defer coordinator.Unlock()
	if coordinator.used || coordinator.gate != nil || normal == nil || rescue == nil || witness == nil || captureEnd == nil {
		return nil, errSealProtocol
	}
	endFD, err := prepareCaptureEnd(captureEnd)
	if err != nil {
		return nil, err
	}
	w, err := mapCIWitness(witness, nonce)
	if err != nil {
		return nil, err
	}
	g := &lifecycleGate{witness: w, application: true, nonce: nonce, captureEndFD: endFD}
	coordinator.used = true
	coordinator.gate = g
	var stopping atomic.Bool
	var stopOnce, reportOnce, recoveryOnce sync.Once
	var wg sync.WaitGroup
	cleanup := func() {
		g.finalTransportMu.Lock()
		stopOnce.Do(func() { stopping.Store(true); normal.Close(); rescue.Close() })
		g.finalTransportMu.Unlock()
		wg.Wait()
		recoveryOnce.Do(func() { g.finishRecoveryCleanup() })
		// Retain the reader through controller join and serialize raw-FD teardown
		// with every consumer, so a late callback cannot read a reused descriptor.
		g.mu.Lock()
		g.captureEndFD = 0
		captureEnd.Close()
		g.mu.Unlock()
		reportOnce.Do(func() { g.writeFinalInvalidationDiagnostic(); g.writeRecoveryCleanupDiagnostic() })
	}
	// Each channel has one reader. Normal EOF cannot close the rescue channel.
	wg.Add(2)
	go func() { defer wg.Done(); serveApplicationNormal(normal, g, &stopping, onFailure) }()
	go func() { defer wg.Done(); serveApplicationRescue(rescue, g, &stopping, onFailure) }()
	return cleanup, nil
}

// All actual application writes use pollable owned files with a fixed deadline.
// A short write is terminal; it is never repaired by appending another frame.
func writeApplicationFrame(file *os.File, value any, limit int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data)+1 > limit {
		return errSealProtocol
	}
	data = append(data, '\n')
	if err := file.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		return err
	}
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errSealProtocol
	}
	return nil
}
func readApplicationRequest(reader *bufio.Reader, limit int, last uint64) (applicationRequest, error) {
	var r applicationRequest
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return r, err
	}
	if len(line) > limit {
		return r, errSealProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return r, errSealProtocol
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || r.Version != 2 || r.Epoch != 1 || r.Seq <= last {
		return r, errSealProtocol
	}
	// JSON duplicate keys are not permitted, even when values are equal.
	var fields map[string]json.RawMessage
	if json.Unmarshal(line, &fields) != nil {
		return r, errSealProtocol
	}
	d := json.NewDecoder(bytes.NewReader(line))
	if _, err = d.Token(); err != nil {
		return r, errSealProtocol
	}
	seen := make(map[string]bool)
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return r, errSealProtocol
		}
		s, ok := key.(string)
		if !ok || seen[s] {
			return r, errSealProtocol
		}
		seen[s] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return r, errSealProtocol
		}
	}
	expected := map[string]bool{"version": true, "epoch": true, "seq": true, "op": true}
	switch r.Op {
	case "final_prepare":
		expected["budgetMillis"] = true
	case "pin":
		expected["target"] = true
	case "prepare_selection":
		expected["kind"] = true
	case "configure_target":
		expected["taskID"], expected["workerID"] = true, true
	}
	if len(fields) != len(expected) {
		return r, errSealProtocol
	}
	for field := range fields {
		if !expected[field] {
			return r, errSealProtocol
		}
	}
	return r, nil
}
func recordApplicationFailure(file *os.File, g *lifecycleGate, stopping *atomic.Bool, onFailure func(), original error) {
	// Only the actual local Close error may be suppressed. A previously observed
	// EOF/partial/timeout cannot be relabelled by a later cleanup Close.
	if !(stopping.Load() && errors.Is(original, os.ErrClosed)) {
		g.invalidateTransport()
		g.abortFinalQuiesce()
		if onFailure != nil {
			onFailure()
		}
	}
	file.Close()
}
func serveApplicationNormal(file *os.File, g *lifecycleGate, stopping *atomic.Bool, onFailure func()) {
	var writeMu sync.Mutex
	send := func(seq uint64, kind string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		f, err := g.applicationSnapshot(seq, kind)
		if err != nil {
			return err
		}
		return writeApplicationFrame(file, f, 65536)
	}
	terminal := error(errSealProtocol)
	defer func() { recordApplicationFailure(file, g, stopping, onFailure, terminal) }()
	if err := send(0, "ready"); err != nil {
		terminal = err
		return
	}
	reader := bufio.NewReaderSize(file, 1024)
	var last uint64
	for {
		r, err := readApplicationRequest(reader, 1024, last)
		if err != nil {
			terminal = err
			return
		}
		last = r.Seq
		if r.Op != "prepare_selection" && r.Op != "configure_target" && (r.Kind != "" || r.TaskID != "" || r.WorkerID != "") {
			return
		}
		if r.Op != "final_prepare" && r.BudgetMillis != 0 {
			return
		}
		switch r.Op {
		case "final_prepare":
			if r.Target != 0 || g.prepareFinalQuiesce(r.BudgetMillis) != nil {
				return
			}
			writeMu.Lock()
			err := writeFinalQuiesceFrame(file, g, r.Seq, "drained")
			writeMu.Unlock()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "final_cancel":
			if r.Target != 0 || !g.finalTransportMu.TryLock() {
				return
			}
			err := func() error {
				defer g.finalTransportMu.Unlock()
				if stopping.Load() {
					return errSealProtocol
				}
				if err := g.cancelFinalQuiesce(); err != nil {
					return err
				}
				writeMu.Lock()
				err := writeFinalQuiesceFrame(file, g, r.Seq, "cancelled")
				writeMu.Unlock()
				if err != nil {
					g.abortFinalQuiesce()
					return err
				}
				return g.completeFinalQuiesce()
			}()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "prepare_selection":
			if r.Target != 0 || r.TaskID != "" || r.WorkerID != "" || g.prepareActivitySelection(r.Kind) != nil {
				return
			}
		case "configure_target":
			if r.Target != 0 || r.Kind != "" || g.configureActivitySelection(r.TaskID, r.WorkerID) != nil {
				return
			}
		case "diagnostic":
			if r.Target != 0 {
				return
			}
			writeMu.Lock()
			err := writeApplicationFrame(file, g.applicationDiagnostic(r.Seq), 1024)
			writeMu.Unlock()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "snapshot", "status":
			if r.Target != 0 {
				return
			}
		case "pin":
			if g.pin(1, r.Target, func() {
				if err := send(0, "invalid"); err != nil {
					recordApplicationFailure(file, g, stopping, onFailure, err)
				}
			}) != nil {
				return
			}
		default:
			return
		}
		if err := send(r.Seq, "reply"); err != nil {
			terminal = err
			return
		}
	}
}
func serveApplicationRescue(file *os.File, g *lifecycleGate, stopping *atomic.Bool, onFailure func()) {
	terminal := error(errSealProtocol)
	defer func() { recordApplicationFailure(file, g, stopping, onFailure, terminal) }()
	reader := bufio.NewReaderSize(file, 256)
	var last uint64
	for {
		r, err := readApplicationRequest(reader, 256, last)
		if err != nil {
			terminal = err
			return
		}
		last = r.Seq
		if r.Target != 0 || r.Kind != "" || r.TaskID != "" || r.WorkerID != "" || r.BudgetMillis != 0 {
			return
		}
		switch r.Op {
		case "release":
			if g.release(1) != nil {
				return
			}
		case "abort":
			g.abortFinalQuiesce()
			g.mu.Lock()
			g.invalidateBecauseLocked(invalidAbort)
			if g.seal != nil {
				g.seal.released = true
			}
			g.closing = false
			g.notifyLocked()
			g.mu.Unlock()
		default:
			return
		}
		ack := struct {
			Version int    `json:"version"`
			Epoch   uint64 `json:"epoch"`
			Seq     uint64 `json:"seq"`
			Kind    string `json:"kind"`
			Status  string `json:"status"`
		}{2, 1, r.Seq, "reply", "released"}
		if err := writeApplicationFrame(file, ack, 1024); err != nil {
			terminal = err
			return
		}
	}
}

func writeFinalQuiesceFrame(file *os.File, g *lifecycleGate, seq uint64, status string) error {
	g.mu.Lock()
	deadline := g.finalQuiesce.deadline
	valid := !g.invalid && g.finalParked && g.finalDrainedLocked() && g.finalQuiesce.state == status
	g.mu.Unlock()
	if !valid || !time.Now().Before(deadline) {
		return errSealProtocol
	}
	frame := struct {
		Version int    `json:"version"`
		Epoch   uint64 `json:"epoch"`
		Seq     uint64 `json:"seq"`
		Kind    string `json:"kind"`
		Status  string `json:"status"`
		Pending int    `json:"pending"`
		Open    int    `json:"open"`
	}{2, 1, seq, "quiesce", status, 0, 0}
	data, err := json.Marshal(frame)
	if err != nil || len(data)+1 > 1024 {
		return errSealProtocol
	}
	data = append(data, '\n')
	if bound := time.Now().Add(500 * time.Millisecond); bound.Before(deadline) {
		deadline = bound
	}
	if err := file.SetWriteDeadline(deadline); err != nil {
		return err
	}
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errSealProtocol
	}
	return nil
}
