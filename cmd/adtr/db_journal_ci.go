//go:build adtr_ci_dbadmission

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/yunpiao/adtr/internal/operationallogs"
)

type ciJournalFinal struct {
	Event             string `json:"event"`
	ProcessIDSHA256   string `json:"processIDSHA256"`
	CloseOK           bool   `json:"closeOK"`
	Accepted          int64  `json:"accepted"`
	Acknowledged      int64  `json:"acknowledged"`
	Rejected          int64  `json:"rejected"`
	QueueFull         int64  `json:"queueFull"`
	WriteFailures     int64  `json:"writeFailures"`
	Unacknowledged    int64  `json:"unacknowledged"`
	Abandoned         int64  `json:"abandoned"`
	AcceptanceStopped bool   `json:"acceptanceStopped"`
}

func ciJournalProjection(s operationallogs.RecorderObservation, closeOK bool) ciJournalFinal {
	sum := sha256.Sum256([]byte(s.ProcessID))
	return ciJournalFinal{"owned_ci_final_journal", hex.EncodeToString(sum[:]), closeOK && s.Module == operationallogs.Worker,
		s.Accepted, s.Acknowledged, s.Rejected, s.QueueFull, s.WriteFailures, s.Unacknowledged, s.Abandoned, s.AcceptanceStopped}
}

// Only the installed CI worker invokes this after the recorder Close returns.
// This private JSONL is parsed after Wait; it is never copied to public logs.
func emitCIJournalFinal(recorder *operationallogs.Recorder, closeErr error) {
	emitCIJournalFirstFailure(recorder)
	snapshot := ciJournalProjection(recorder.Stats(), closeErr == nil)
	data, err := json.Marshal(snapshot)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}

// Separate from the delivery proof: this closed observation never grants success.
func emitCIJournalFirstFailure(recorder *operationallogs.Recorder) {
	data, err := json.Marshal(recorder.CIJournalFirstFailure())
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}
