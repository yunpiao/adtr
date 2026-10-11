//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messages"
	"github.com/yunpiao/adtr/internal/tasks"
)

func decodeBulk(t *testing.T, raw []byte) messages.BulkOperation {
	t.Helper()
	var out messages.BulkOperation
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestMessageBulkRealHTTPUnknownAcceptanceAndNewSessionRecovery(t *testing.T) {
	f := newMessageFixture(t)
	operator := f.client(f.operator)
	f.realHealth(operator, "bulk-health-one")
	f.realHealth(operator, "bulk-health-two")
	// Personal reads need live read permission, not task mutation/MFA proof.
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	f.exec("UPDATE adtr.users SET mfa_secret='' WHERE id=$1", f.operator)
	snap := f.query(operator, "all")
	body := map[string]any{"resultId": snap.ResultID, "operationId": "http-bulk", "selection": "all"}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/messages/bulk-read", strings.NewReader(string(raw)))
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", operator.csrf)
	r.AddCookie(operator.cookie)
	lost := &lostMessageResponse{header: make(http.Header)}
	f.s.MessagesHandler(f.inbox).ServeHTTP(lost, r)
	if lost.status != 200 {
		t.Fatal("unknown acceptance not committed", lost.status)
	}
	queued := decodeBulk(t, f.message(operator, "/bulk-read-operation?operationId=http-bulk", nil, 200))
	if queued.State != "queued" || queued.Progress.Target != 2 {
		t.Fatal(queued)
	}
	for _, other := range []*resourceTestClient{f.client(f.admin), f.client(f.other)} {
		f.message(other, "/bulk-read-operation?operationId=http-bulk", nil, 404)
		f.message(other, "/bulk-read-cancel", map[string]any{"operationId": "http-bulk"}, 404)
		f.message(other, "/bulk-read", body, 404)
	}
	invalid := *operator
	invalid.csrf = "wrong"
	f.message(&invalid, "/bulk-read", body, 403)
	f.message(nil, "/bulk-read-operation?operationId=http-bulk", nil, 401)
	// Logout, old result cleanup and new session do not cancel accepted background work.
	f.exec("DELETE FROM adtr.sessions WHERE token_hash=$1", digest(operator.cookie.Value))
	f.exec("DELETE FROM adtr.message_results WHERE result_id=$1", snap.ResultID)
	f.message(operator, "/bulk-read", body, 401)
	renewed := f.client(f.operator)
	if replay := decodeBulk(t, f.message(renewed, "/bulk-read", body, 200)); replay != queued {
		t.Fatal("original acceptance lost", replay, queued)
	}
	if err := f.inbox.BulkOnce(f.ctx, taskAuthorizer(f.s.now)); err != nil {
		t.Fatal(err)
	}
	done := decodeBulk(t, f.message(renewed, "/bulk-read-operation?operationId=http-bulk", nil, 200))
	if done.State != "completed" || done.Progress.Processed != 2 || done.Progress.NewlyRead != 2 || done.Receipt != queued.Receipt {
		t.Fatal(done)
	}
	cancelled := decodeBulk(t, f.message(renewed, "/bulk-read-cancel", map[string]any{"operationId": "http-bulk"}, 200))
	if cancelled != done {
		t.Fatal("completed cancel changed outcome")
	}
	fresh := f.query(renewed, "unread")
	if fresh.Total != 0 || fresh.Counts.Read != 2 {
		t.Fatal(fresh)
	}
}
func TestMessageBulkLiveActorAuthorityAndEpochAreRechecked(t *testing.T) {
	for _, kind := range []string{"permission", "disabled", "forced_password", "expired_password", "epoch_restore"} {
		t.Run(kind, func(t *testing.T) {
			f := newMessageFixture(t)
			c := f.client(f.operator)
			f.realHealth(c, "live-bulk")
			snap := f.query(c, "all")
			body := map[string]any{"resultId": snap.ResultID, "operationId": "revoked-bulk", "selection": "all"}
			f.message(c, "/bulk-read", body, 200)
			switch kind {
			case "permission":
				f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			case "disabled":
				f.exec("UPDATE adtr.users SET disabled=true WHERE id=$1", f.operator)
			case "forced_password":
				f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.operator)
			case "expired_password":
				f.exec("UPDATE adtr.users SET password_updated_at=$2 WHERE id=$1", f.operator, f.now.Add(-91*24*time.Hour))
			case "epoch_restore":
				f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
				f.exec("UPDATE adtr.access_permissions SET readable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			}
			if err := f.inbox.BulkOnce(f.ctx, taskAuthorizer(f.s.now)); err != nil {
				t.Fatal(err)
			}
			var state string
			var processed, remaining, reads int
			if err := f.conn.QueryRow(f.ctx, `SELECT state,processed,target-processed,(SELECT count(*) FROM adtr.message_reads) FROM adtr.message_bulk_operations WHERE operation_id='revoked-bulk'`).Scan(&state, &processed, &remaining, &reads); err != nil || state != "authorization_blocked" || processed != 0 || remaining != 1 || reads != 0 {
				t.Fatal(state, processed, remaining, reads, err)
			}
			if kind == "epoch_restore" {
				op := decodeBulk(t, f.message(c, "/bulk-read", body, 200))
				if op.State != "authorization_blocked" {
					t.Fatal(op)
				}
			}
		})
	}
}
func TestMessageBulkLiveAuthorizationLockLinearizesRevocation(t *testing.T) {
	f := newMessageFixture(t)
	c := f.client(f.operator)
	f.realHealth(c, "lock-bulk")
	snap := f.query(c, "all")
	f.message(c, "/bulk-read", map[string]any{"resultId": snap.ResultID, "operationId": "locked-bulk", "selection": "all"}, 200)
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	authorize := func(ctx context.Context, tx pgx.Tx, p tasks.Principal, s tasks.Scope, a tasks.Action) (string, error) {
		epoch, err := taskAuthorizer(f.s.now)(ctx, tx, p, s, a)
		if err == nil {
			close(locked)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return epoch, err
	}
	go func() { done <- f.inbox.BulkOnce(f.ctx, authorize) }()
	select {
	case <-locked:
	case <-time.After(3 * time.Second):
		t.Fatal("authorization did not lock")
	}
	// A separate permission mutation cannot overtake the live actor lock.
	conn, err := pgx.ConnectConfig(f.ctx, f.s.database)
	if err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	revoke := make(chan error, 1)
	go func() {
		tx, err := conn.Begin(f.ctx)
		if err == nil {
			defer tx.Rollback(context.Background())
			err = lockIdentityTenant(f.ctx, tx, "one")
			if err == nil {
				_, err = tx.Exec(f.ctx, "UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
			}
			if err == nil {
				err = tx.Commit(f.ctx)
			}
		}
		revoke <- err
	}()
	select {
	case err := <-revoke:
		close(release)
		<-done
		t.Fatal("revocation bypassed held authority", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-revoke; err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.message_reads").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	f.message(c, "/bulk-read-operation?operationId=locked-bulk", nil, 403)
}
