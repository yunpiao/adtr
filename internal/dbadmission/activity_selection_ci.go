//go:build adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"strconv"
	"strings"
)

func activityRowKey(nonce [16]byte, taskID string) string {
	data := append([]byte("adtr-ci-activity-v1\x00"), nonce[:]...)
	data = append(data, 0)
	data = append(data, []byte(taskID)...)
	sum := sha256.Sum256(data)
	return "ci-activity-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}

func selectionID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func selectionWorkerID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (g *lifecycleGate) prepareActivitySelection(kind string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.application || g.selection != 0 || g.invalid || g.closing || g.seal != nil ||
		(kind != "user_assets.v3.export" && kind != "group_assets.v3.export" && kind != "computer_assets.v3.export") {
		return errSealProtocol
	}
	for _, ticket := range g.tickets {
		if ticket.registration != nil {
			return errSealProtocol
		}
	}
	g.selection, g.selectionKind = 1, kind
	g.notifyLocked()
	return nil
}

func (g *lifecycleGate) configureActivitySelection(taskID, workerID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.application || g.selection != 1 || g.invalid || g.closing || g.seal != nil || !selectionID(taskID) || !selectionWorkerID(workerID) {
		return errSealProtocol
	}
	if g.queuedState != "" && (g.queuedState != "drained" || !g.queuedParked || !g.finalDrainedLocked()) {
		return errSealProtocol
	}
	key := activityRowKey(g.nonce, taskID)
	if key == workerID {
		return errSealProtocol
	}
	g.selectionTask, g.selectionOwner, g.selectionKey = taskID, workerID, key
	g.selection = 2
	if g.queuedParked {
		g.queuedState = "configured"
	}
	g.notifyLocked()
	return nil
}

// AwaitActivitySelection runs before the health callback acquires any connection.
// Nil-controller and unprepared paths preserve the ordinary callback behavior.
func AwaitActivitySelection(ctx context.Context, identity ActivityIdentity) error {
	coordinator.Lock()
	g := coordinator.gate
	coordinator.Unlock()
	if g == nil {
		return nil
	}
	return g.awaitActivitySelection(ctx, identity)
}

func (g *lifecycleGate) awaitActivitySelection(ctx context.Context, identity ActivityIdentity) error {
	waited := false
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.invalidateBecauseLocked(invalidWaitCancelled)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return err
		}
		if g.invalid || g.closing {
			g.mu.Unlock()
			return errAdmissionClosed
		}
		if g.selection != 1 {
			selected := g.selection == 2 && identity.TaskID == g.selectionTask
			owned := false
			for slot := 0; slot < 4; slot++ {
				owned = owned || identity.LeaseOwner == g.selectionOwner+"-"+strconv.Itoa(slot)
			}
			if (waited && !selected) || (selected && (identity.Kind != g.selectionKind || !owned)) {
				g.invalidateBecauseLocked(invalidExplicit)
				g.notifyLocked()
				g.unlockAndPublishInvalid()
				return errSealProtocol
			}
			g.mu.Unlock()
			return nil
		}
		waited = true
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
	}
}
