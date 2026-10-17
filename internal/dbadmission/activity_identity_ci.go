//go:build adtr_ci_dbadmission

package dbadmission

import (
	"errors"
	"sync"
)

type activityRegistration struct {
	identity   ActivityIdentity
	backend    uint32
	generation uint64
}

// No public enumeration, gate installation, or control transport exists here.
// The registration remains internal and cannot grant capture eligibility.
var activityIdentities = struct {
	sync.Mutex
	entries map[*Conn]*activityRegistration
}{entries: make(map[*Conn]*activityRegistration)}

func RegisterActivityIdentity(conn *Conn, identity ActivityIdentity) (func(), error) {
	if conn == nil || conn.raw == nil || identity.Phase != "first-fenced-progress-10" || identity.TaskID == "" || identity.Attempt != 1 || identity.FencingToken <= 0 || identity.ResultVersion <= 0 || identity.LeaseOwner == "" {
		return nil, errors.New("activity identity unavailable")
	}
	if identity.Kind != "computer_assets.v3.export" && identity.Kind != "group_assets.v3.export" && identity.Kind != "user_assets.v3.export" {
		return nil, errors.New("activity identity unavailable")
	}
	coordinator.Lock()
	gate := coordinator.gate
	coordinator.Unlock()
	if gate != nil {
		return gate.register(conn, identity)
	}
	if conn.raw.IsClosed() || conn.permit != nil {
		return nil, errors.New("activity identity unavailable")
	}
	registration := &activityRegistration{identity: identity, backend: conn.raw.PgConn().PID()}
	activityIdentities.Lock()
	if _, exists := activityIdentities.entries[conn]; exists {
		activityIdentities.Unlock()
		return nil, errors.New("activity identity unavailable")
	}
	activityIdentities.entries[conn] = registration
	activityIdentities.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			activityIdentities.Lock()
			defer activityIdentities.Unlock()
			if activityIdentities.entries[conn] == registration {
				delete(activityIdentities.entries, conn)
			}
		})
	}, nil
}
