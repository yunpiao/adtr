//go:build adtr_ci_dbadmission

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/dbadmission"
)

var errActivityCandidate = errors.New("activity candidate unavailable")

type activityCandidateProof struct {
	mu       sync.Mutex
	identity dbadmission.ActivityIdentity
	consumed bool
	revoked  bool
	release  func()
}

func (*activityCandidateProof) activityCandidate() {}
func (*activityCandidateProof) String() string     { return "activity-candidate" }
func (*activityCandidateProof) GoString() string   { return "activity-candidate" }
func (*activityCandidateProof) MarshalJSON() ([]byte, error) {
	return []byte(`"activity-candidate"`), nil
}

func (e *Engine) withActivityCandidate(ctx context.Context, lease Lease, expected int64, work FencedWork, activity func(context.Context, ActivityCandidate)) (int64, error) {
	wrapped := work
	progress := -1
	completed := false
	if work != nil {
		wrapped = func(c context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			p, cursor, result, err := work(c, tx)
			if err == nil {
				progress, completed = p, true
			}
			return p, cursor, result, err
		}
	}
	version, err := e.WithTx(ctx, lease, expected, wrapped)
	if err == nil && activity != nil {
		candidate := mintActivityCandidate(lease, expected, version, progress, completed)
		if candidate != nil {
			defer candidate.revoke()
		}
		if candidate == nil {
			activity(ctx, nil)
		} else {
			activity(ctx, candidate)
		}
	}
	return version, err
}

func mintActivityCandidate(lease Lease, expected, version int64, progress int, completed bool) *activityCandidateProof {
	kind := lease.Task.Kind
	if kind != "computer_assets.v3.export" && kind != "group_assets.v3.export" && kind != "user_assets.v3.export" {
		return nil
	}
	if !completed || progress != 10 || lease.Task.Attempt != 1 || lease.Task.State != Running || expected != lease.Task.ResultVersion || expected < 0 || version <= expected || version != expected+1 || lease.Token <= 0 || lease.Token != lease.Task.FencingToken || lease.Owner == "" || lease.Owner != lease.Task.LeaseOwner || lease.Task.ID == "" {
		return nil
	}
	return &activityCandidateProof{identity: dbadmission.ActivityIdentity{
		TaskID: lease.Task.ID, Kind: kind, Attempt: lease.Task.Attempt,
		LeaseOwner: lease.Owner, FencingToken: lease.Token, ResultVersion: version,
		Phase: "first-fenced-progress-10",
	}}
}

func (p *activityCandidateProof) revoke() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revoked = true
	if p.release != nil {
		p.release()
		p.release = nil
	}
}

// BindActivityCandidate may be called only by the dedicated health activity
// transaction entrypoint, as enforced by the typed repository checker.
func BindActivityCandidate(candidate ActivityCandidate, conn *dbadmission.Conn, workerID string) (func(), error) {
	if candidate == nil {
		return func() {}, nil
	}
	proof, ok := candidate.(*activityCandidateProof)
	if !ok || proof == nil {
		return nil, errActivityCandidate
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.revoked || proof.consumed {
		return nil, errActivityCandidate
	}
	proof.consumed = true
	owned := false
	for slot := 0; slot < 4; slot++ {
		owned = owned || proof.identity.LeaseOwner == workerID+"-"+strconv.Itoa(slot)
	}
	if !owned {
		return nil, errActivityCandidate
	}
	release, err := dbadmission.RegisterActivityIdentity(conn, proof.identity)
	if err != nil {
		return nil, errActivityCandidate
	}
	proof.release = release
	return release, nil
}
