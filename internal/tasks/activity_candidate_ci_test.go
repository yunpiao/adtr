//go:build adtr_ci_dbadmission

package tasks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func candidateLease(kind string) Lease {
	return Lease{Task: Task{ID: "private-task-canary", Kind: kind, Attempt: 1, State: Running, ResultVersion: 1, LeaseOwner: "private-worker-2", FencingToken: 17}, Owner: "private-worker-2", Token: 17}
}

func TestActivityCandidateMintBoundariesAndPrivacy(t *testing.T) {
	for _, kind := range []string{"computer_assets.v3.export", "group_assets.v3.export", "user_assets.v3.export"} {
		lease := candidateLease(kind)
		proof := mintActivityCandidate(lease, 1, 2, 10, true)
		if proof == nil || proof.identity.TaskID != lease.Task.ID || proof.identity.LeaseOwner != lease.Owner || proof.identity.Phase != "first-fenced-progress-10" {
			t.Fatal("candidate identity mismatch")
		}
		encoded, err := json.Marshal(proof)
		if err != nil {
			t.Fatal("candidate JSON failed")
		}
		for _, text := range []string{string(encoded), fmt.Sprintf("%v", proof), fmt.Sprintf("%+v", proof), fmt.Sprintf("%#v", proof)} {
			if strings.Contains(text, "private-") || strings.Contains(text, kind) || strings.Contains(text, "17") {
				t.Fatal("candidate identity leaked")
			}
		}
		proof.revoke()
		if _, _, err := BindActivityCandidate(proof, nil, "private-worker"); err == nil {
			t.Fatal("revoked candidate consumed")
		}
	}
	valid := candidateLease("user_assets.v3.export")
	for _, mutate := range []func(*Lease){
		func(l *Lease) { l.Task.Kind = "synthetic" },
		func(l *Lease) { l.Task.Attempt = 2 },
		func(l *Lease) { l.Task.State = Queued },
		func(l *Lease) { l.Task.ResultVersion = 2 },
		func(l *Lease) { l.Task.ID = "" },
		func(l *Lease) { l.Owner = "wrong-slot" },
		func(l *Lease) { l.Token++ },
	} {
		lease := valid
		mutate(&lease)
		if mintActivityCandidate(lease, 1, 2, 10, true) != nil {
			t.Fatal("invalid attempt minted candidate")
		}
	}
	for _, args := range []struct {
		expected, version int64
		progress          int
		completed         bool
	}{{2, 3, 10, true}, {1, 3, 10, true}, {1, 2, 11, true}, {1, 2, 10, false}} {
		if mintActivityCandidate(valid, args.expected, args.version, args.progress, args.completed) != nil {
			t.Fatal("noninitial commit minted candidate")
		}
	}
}

func TestActivityCandidateWrongWorkerConsumesOnce(t *testing.T) {
	proof := mintActivityCandidate(candidateLease("user_assets.v3.export"), 1, 2, 10, true)
	if _, _, err := BindActivityCandidate(proof, nil, "another-worker"); err == nil {
		t.Fatal("wrong worker bound candidate")
	}
	if !proof.consumed {
		t.Fatal("failed bind left reusable candidate")
	}
	if _, _, err := BindActivityCandidate(proof, nil, "private-worker"); err == nil {
		t.Fatal("consumed candidate reused")
	}
}
