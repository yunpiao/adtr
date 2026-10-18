//go:build adtr_ci_dbadmission

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/operationallogs"
)

func TestActivityCandidateFinalJournalClosedProjection(t *testing.T) {
	s := operationallogs.RecorderObservation{ProcessID: "private-process-value", Module: operationallogs.Worker,
		Accepted: 8, Acknowledged: 8, AcceptanceStopped: true}
	proof := ciJournalProjection(s, true)
	raw, err := json.Marshal(proof)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 11 || strings.Contains(string(raw), s.ProcessID) {
		t.Fatal("journal projection privacy")
	}
	sum := sha256.Sum256([]byte(s.ProcessID))
	if proof.ProcessIDSHA256 != hex.EncodeToString(sum[:]) || !proof.CloseOK || proof.Accepted != 8 || proof.Acknowledged != 8 || !proof.AcceptanceStopped {
		t.Fatal("journal projection mismatch")
	}
	if ciJournalProjection(s, false).CloseOK {
		t.Fatal("failed Close accepted")
	}
	s.Module = operationallogs.API
	if ciJournalProjection(s, true).CloseOK {
		t.Fatal("nonworker accepted")
	}
}
