//go:build integration && adtr_ci_dbadmission

package dbadmission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestActivityIdentityScopedToExistingConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config := integrationConfig(t)
	conn, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("identity connection failed")
	}
	defer conn.Close(context.Background())
	other, err := ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("other identity connection failed")
	}
	defer other.Close(context.Background())
	identity := ActivityIdentity{TaskID: "private-task", Kind: "user_assets.v3.export", Attempt: 1, LeaseOwner: "private-owner-0", FencingToken: 23, ResultVersion: 2, Phase: "first-fenced-progress-10"}
	release, err := RegisterActivityIdentity(conn, identity)
	if err != nil {
		t.Fatal("identity registration failed")
	}
	defer release()
	activityIdentities.Lock()
	registration := activityIdentities.entries[conn]
	otherRegistration := activityIdentities.entries[other]
	activityIdentities.Unlock()
	if registration == nil || registration.identity != identity || registration.backend == 0 || otherRegistration != nil {
		t.Fatal("identity attached to incorrect handle")
	}
	if _, err := RegisterActivityIdentity(conn, identity); err == nil {
		t.Fatal("handle rebound")
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal("identity serialization failed")
	}
	for _, text := range []string{string(encoded), fmt.Sprintf("%+v", identity), fmt.Sprintf("%#v", identity)} {
		if strings.Contains(text, "private-") || strings.Contains(text, "23") || strings.Contains(text, identity.Kind) {
			t.Fatal("identity leaked")
		}
	}
	release()
	release()
	activityIdentities.Lock()
	_, exists := activityIdentities.entries[conn]
	activityIdentities.Unlock()
	if exists {
		t.Fatal("released identity remained registered")
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatal("identity connection close failed")
	}
	if _, err := RegisterActivityIdentity(conn, identity); err == nil {
		t.Fatal("closed connection accepted identity")
	}
}
