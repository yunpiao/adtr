//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

func (f *domainFixture) selectionRequest(c *resourceTestClient, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/domain-selection"+path, nil)
	r.RemoteAddr = "127.0.0.1:4488"
	// Client assertions cannot select the response owner.
	r.Header.Set("X-ADTR-User-ID", "999999999")
	if c != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	f.s.DomainSelectionHandler(f.store).ServeHTTP(w, r)
	return w
}

func (f *domainFixture) callSelection(c *resourceTestClient, path string, status int) map[string]any {
	f.t.Helper()
	w := f.selectionRequest(c, path)
	if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatalf("selection %s: %d want %d: %s", path, w.Code, status, w.Body)
	}
	if status == 200 {
		if c == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(c.id, 10) {
			f.t.Fatal("selection response is not bound to its authenticated actor")
		}
	} else if w.Header().Get("X-ADTR-User-ID") != "" {
		f.t.Fatal("failure response exposed an actor binding")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	if status != 200 && len(out) != 1 {
		f.t.Fatal("error contains metadata", out)
	}
	return out
}

func selectionResolvePath(id, revision, credential string) string {
	return "/resolve?" + url.Values{"domainId": {id}, "expectedRevision": {revision}, "expectedCredentialRevision": {credential}}.Encode()
}

func assertSelectionChoice(t *testing.T, row map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !reflect.DeepEqual(keys, strings.Fields("connectionState credentialConfigured credentialRevision dcHostName domain domainId lastTest ldapAddr mode port revision source")) || row["source"] != "configured_connection" {
		t.Fatal("unsafe selection fields", row)
	}
	for _, key := range []string{"domainId", "revision", "credentialRevision", "port"} {
		if _, ok := row[key].(string); !ok {
			t.Fatal("non-string identifier or revision", key, row)
		}
	}
	if row["lastTest"] != nil {
		d, ok := row["lastTest"].(map[string]any)
		if !ok || len(d) != 3 || d["observedAt"] == nil || d["dcHostName"] == nil || d["code"] == nil {
			t.Fatal("unsafe observation fields", row)
		}
	}
}

// These rows are synthetic saved configurations, not directory inventory. Rows
// without credentials exercise the truthful credentialConfigured=false surface.
func (f *domainFixture) seedSelectionConnection(tenant, id, domain, dc string) {
	f.t.Helper()
	f.exec("INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES($1,$2,$3)", tenant, id, domain)
	f.exec("INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,created_at,credential_mode) VALUES($1,$2,$3,$4,'starttls','389',$5,'unconfigured')", tenant, id, domain, dc, f.now)
}

func TestDomainSelectionScopeBeforeCountsPagesAndLiteralSearch(t *testing.T) {
	f := newDomainFixture(t)
	f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=50 WHERE tenant_id='one'")
	id := f.createDomain("selection-source")
	allowed := []string{id}
	for i := range 11 {
		next := fmt.Sprintf("selection-%02d", i)
		f.seedSelectionConnection("one", next, fmt.Sprintf("source-%02d.test", i), fmt.Sprintf("dc-%02d.internal.test", i))
		allowed = append(allowed, next)
	}
	f.seedSelectionConnection("one", "hidden", "hidden.test", "hidden-dc.test")
	f.seedSelectionConnection("one", "inactive", "inactive.test", "inactive-dc.test")
	f.seedSelectionConnection("one", "deleted", "deleted.test", "deleted-dc.test")
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id='inactive'")
	f.exec("UPDATE adtr.domain_connections SET deleted_at=clock_timestamp(),connection_revision=connection_revision+1,credential_revision=credential_revision+1,diagnostic_generation=diagnostic_generation+1,latest_test_task_id=NULL WHERE tenant_id='one' AND domain_id='deleted'")
	f.seedGroup("one", "selection-scope", narrowResourceRole, append(slices.Clone(allowed), "inactive", "deleted")...)
	admin, operator, foreign := f.client(f.admin), f.client(f.operator), f.client(f.other)
	for _, c := range []*resourceTestClient{admin, foreign} {
		out := f.callSelection(c, "", 200)
		if out["page"].(map[string]any)["total"] != float64(0) || len(out["List"].([]any)) != 0 || out["exhausted"] != true {
			t.Fatal("administrator or cross-tenant scope bypass", out)
		}
		f.callSelection(c, selectionResolvePath(id, "999", "999"), 404)
	}
	seen := map[string]bool{}
	for page, count := range map[int]int{1: 10, 2: 2, 3: 0} {
		out := f.callSelection(operator, fmt.Sprintf("?pageIdx=%d&pageSize=10", page), 200)
		meta, rows := out["page"].(map[string]any), out["List"].([]any)
		if len(out) != 3 || len(meta) != 4 || meta["total"] != float64(12) || meta["totalPage"] != float64(2) || meta["pageIdx"] != float64(page) || meta["pageSize"] != float64(10) || len(rows) != count || out["exhausted"] != (page >= 2) {
			t.Fatal("scope/page/count mismatch", out)
		}
		for _, value := range rows {
			row := value.(map[string]any)
			assertSelectionChoice(t, row)
			rowID := row["domainId"].(string)
			if !slices.Contains(allowed, rowID) || seen[rowID] {
				t.Fatal("ungranted or duplicate page row", row)
			}
			seen[rowID] = true
			if rowID != id && (row["credentialConfigured"] != false || row["ldapAddr"] != "") {
				t.Fatal("invented credentials or resolved address", row)
			}
		}
	}
	if len(seen) != 12 {
		t.Fatal("missing authorized choices")
	}
	for _, keyword := range []string{"hidden", "inactive", "deleted", "%", "_", "\\"} {
		out := f.callSelection(operator, "?keyword="+url.QueryEscape(keyword), 200)
		if out["page"].(map[string]any)["total"] != float64(0) {
			t.Fatal("scope or literal keyword leak", keyword, out)
		}
	}
	for _, keyword := range []string{"SOURCE-00", "DC-00.INTERNAL"} {
		out := f.callSelection(operator, "?keyword="+url.QueryEscape(keyword), 200)
		if out["page"].(map[string]any)["total"] != float64(1) || out["List"].([]any)[0].(map[string]any)["domainId"] != "selection-00" {
			t.Fatal("configured hostname search", out)
		}
	}
	for _, state := range []string{"unverified", "testing", "verified", "error"} {
		out := f.callSelection(operator, "?observationState="+state, 200)
		want := float64(0)
		if state == "unverified" {
			want = 12
		}
		if out["page"].(map[string]any)["total"] != want {
			t.Fatal("observation filter", out)
		}
	}
	if f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_diagnostics") != 0 {
		t.Fatal("selection created network work or observations")
	}
}

func TestDomainSelectionResolveCASAndConcealedResources(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("selection-cas")
	f.grantDomain(id)
	admin, operator := f.client(f.admin), f.client(f.operator)
	path := selectionResolvePath(id, "1", "1")
	out := f.callSelection(operator, path, 200)
	row := out["selection"].(map[string]any)
	assertSelectionChoice(t, row)
	if len(out) != 2 || row["domainId"] != id || row["connectionState"] != "unverified" || row["lastTest"] != nil {
		t.Fatal(out)
	}
	if checked, err := time.Parse(time.RFC3339, out["checkedAt"].(string)); err != nil || checked.Location() != time.UTC {
		t.Fatal("checkedAt is not UTC RFC3339", out)
	}
	f.callDomain(admin, "/update", f.proof(map[string]any{"domainId": id, "expectedRevision": "1", "dcHostName": "dc1.example.test", "ldapAddr": "10.20.0.9", "port": "389"}), 200)
	if got := f.callSelection(operator, path, 409); got["error"] != "selection_changed" {
		t.Fatal(got)
	}
	f.callSelection(operator, selectionResolvePath(id, "2", "1"), 200)
	f.callDomain(admin, "/update", f.proof(map[string]any{"domainId": id, "expectedRevision": "2", "dcHostName": "dc1.example.test", "ldapAddr": "10.20.0.9", "port": "389", "username": "replacement@example.test", "password": "Replacement synthetic secret"}), 200)
	// A current config revision still cannot conceal a stale credential pin.
	if got := f.callSelection(operator, selectionResolvePath(id, "3", "1"), 409); got["error"] != "selection_changed" {
		t.Fatal(got)
	}
	f.callSelection(operator, selectionResolvePath(id, "3", "2"), 200)
	for _, inaccessible := range []string{id, "unknown", "domain-a", "foreign-only"} {
		f.callSelection(f.client(f.other), selectionResolvePath(inaccessible, "999", "999"), 404)
	}
	f.seedSelectionConnection("two", "foreign-config", "foreign.test", "dc.foreign.test")
	f.seedGroup("two", "foreign-selection", "platform_admin", "foreign-config")
	f.callSelection(operator, selectionResolvePath("foreign-config", "999", "999"), 404)
	f.exec("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='one' AND id=$1", id)
	f.callSelection(operator, selectionResolvePath(id, "1", "1"), 404)
	f.exec("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='one' AND id=$1", id)
	f.callDomain(admin, "/delete", f.proof(map[string]any{"domainId": id, "expectedRevision": "3", "confirmDomain": "example.test"}), 200)
	f.callSelection(operator, selectionResolvePath(id, "1", "1"), 404)
	if f.scalar("SELECT count(*) FROM adtr.tasks") != 0 {
		t.Fatal("resolve submitted a task")
	}
}

func TestDomainSelectionAuthorizationGatesAndSafeFailureAudit(t *testing.T) {
	for _, gate := range []string{"anonymous", "session", "forced", "function", "tenant_missing", "tenant_expired", "tenant_capacity"} {
		t.Run(gate, func(t *testing.T) {
			f := newDomainFixture(t)
			id := f.createDomain("selection-gate")
			f.grantDomain(id)
			operator := f.client(f.operator)
			want := 403
			code := "forbidden"
			switch gate {
			case "anonymous":
				operator, want, code = nil, 401, "unauthenticated"
			case "session":
				f.exec("DELETE FROM adtr.sessions WHERE user_id=$1", f.operator)
				want, code = 401, "unauthenticated"
			case "forced":
				f.exec("UPDATE adtr.users SET must_change=true WHERE id=$1", f.operator)
				code = "password_change_required"
			case "function":
				f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
			case "tenant_missing":
				f.exec("DELETE FROM adtr.resource_tenant_config WHERE tenant_id='one'")
				want, code = 409, "tenant_not_configured"
			case "tenant_expired":
				f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", f.now.Unix())
				code = "tenant_expired"
			case "tenant_capacity":
				f.exec("UPDATE adtr.resource_tenant_config SET max_ad_count=1 WHERE tenant_id='one'")
				code = "tenant_domain_limit_exceeded"
			}
			before := f.scalar("SELECT count(*) FROM adtr.auth_audit")
			for _, path := range []string{"?keyword=synthetic-private-marker", selectionResolvePath(id, "1", "1")} {
				if got := f.callSelection(operator, path, want); got["error"] != code {
					t.Fatal(got)
				}
			}
			if f.scalar("SELECT count(*) FROM adtr.auth_audit") != before+2 || f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_diagnostics") != 0 {
				t.Fatal("missing denial audit or unexpected work")
			}
			var unsafe bool
			if err := f.conn.QueryRow(f.ctx, "SELECT EXISTS(SELECT FROM adtr.auth_audit a WHERE row_to_json(a)::text LIKE '%synthetic-private-marker%')").Scan(&unsafe); err != nil || unsafe {
				t.Fatal("query material reached audit", err)
			}
		})
	}
}

func TestDomainSelectionSchemaBeforeAuthenticationWithoutSideEffects(t *testing.T) {
	for _, gate := range []string{"old", "new", "missing_row", "missing_table", "missing_column", "malformed_version"} {
		t.Run(gate, func(t *testing.T) {
			f := newDomainFixture(t)
			code := "schema_incompatible"
			switch gate {
			case "old":
				f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current-1)
			case "new":
				f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
			case "missing_row":
				f.exec("DELETE FROM adtr.schema_version")
			case "missing_table":
				f.exec("ALTER TABLE adtr.schema_version RENAME TO hidden_schema_version")
			case "missing_column":
				f.exec("ALTER TABLE adtr.schema_version RENAME COLUMN version TO hidden_version")
			case "malformed_version":
				f.exec("ALTER TABLE adtr.schema_version DROP CONSTRAINT schema_version_version_check; ALTER TABLE adtr.schema_version ALTER COLUMN version TYPE text USING version::text; UPDATE adtr.schema_version SET version='invalid'")
				code = "schema_unavailable"
			}
			before := f.scalar("SELECT count(*) FROM adtr.auth_audit")
			// Anonymous 503, rather than 401, proves the migration gate runs first.
			for _, path := range []string{"", selectionResolvePath("opaque-id", "1", "1")} {
				if got := f.callSelection(nil, path, 503); got["error"] != code {
					t.Fatal(got)
				}
			}
			if f.scalar("SELECT count(*) FROM adtr.auth_audit") != before || f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_diagnostics") != 0 {
				t.Fatal("schema failure wrote audit or task state")
			}
		})
	}
}

func TestDomainSelectionRechecksAfterTenantLock(t *testing.T) {
	for _, change := range []string{"scope", "permission", "session", "tenant_expiry", "session_expiry"} {
		t.Run(change, func(t *testing.T) {
			f := newDomainFixture(t)
			id := f.createDomain("blocked-selection")
			f.grantDomain(id)
			operator := f.client(f.operator)
			base := f.now
			var current atomic.Int64
			current.Store(base.Unix())
			f.s.now = func() time.Time { return time.Unix(current.Load(), 0).UTC() }
			if change == "tenant_expiry" {
				f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", base.Add(time.Hour).Unix())
			}
			conn, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(f.ctx)
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			if err = lockIdentityTenant(f.ctx, tx, "one"); err != nil {
				t.Fatal(err)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- f.selectionRequest(operator, selectionResolvePath(id, "1", "1")) }()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if f.scalar("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'") > 0 {
					blocked = true
					break
				}
				select {
				case w := <-done:
					t.Fatalf("selection bypassed tenant lock: %d %s", w.Code, w.Body)
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !blocked {
				t.Fatal("selection did not wait for tenant authorization lock")
			}
			want, code := 404, "not_found"
			switch change {
			case "scope":
				_, err = tx.Exec(f.ctx, "DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id=$1", narrowResourceRole)
			case "permission":
				_, err = tx.Exec(f.ctx, "UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
				want, code = 403, "forbidden"
			case "session":
				_, err = tx.Exec(f.ctx, "DELETE FROM adtr.sessions WHERE user_id=$1", f.operator)
				want, code = 401, "unauthenticated"
			case "tenant_expiry":
				current.Store(base.Add(2 * time.Hour).Unix())
				want, code = 403, "tenant_expired"
			case "session_expiry":
				current.Store(base.Add(9 * time.Hour).Unix())
				want, code = 401, "unauthenticated"
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case w := <-done:
				var out map[string]string
				if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != want || out["error"] != code || len(out) != 1 {
					t.Fatal("stale authorization survived lock", w.Code, w.Body, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("selection did not finish after releasing tenant lock")
			}
			if f.scalar("SELECT count(*) FROM adtr.tasks") != 0 || f.scalar("SELECT count(*) FROM adtr.domain_diagnostics") != 0 {
				t.Fatal("denied resolve submitted work")
			}
		})
	}
}

func TestDomainSelectionResolveRefreshesCurrentTerminalObservation(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("observation-selection")
	f.grantDomain(id)
	operator := f.client(f.operator)
	path := selectionResolvePath(id, "1", "1")
	f.callSelection(operator, path, 200)
	taskID := taskOutputID(t, f.callDomain(operator, "/test", f.proof(map[string]any{"domainId": id, "expectedRevision": "1", "idempotencyKey": "observation-one"}), 200))
	lease, err := f.engine.Claim(f.ctx, "synthetic-selection-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil || lease.Task.ID != taskID {
		t.Fatal("start selection fixture", err)
	}
	var pins map[string]string
	if err = json.Unmarshal(lease.Task.Payload, &pins); err != nil {
		t.Fatal(err)
	}
	// Persist synthetic immutable evidence through the real fenced checkpoint.
	// No executor or probe is invoked; only the real task terminal transition
	// below can make this historical observation visible.
	_, err = f.engine.WithTx(f.ctx, lease, lease.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		_, e := tx.Exec(ctx, "INSERT INTO adtr.domain_diagnostics(tenant_id,domain_id,task_id,connection_revision,credential_revision,policy_revision,diagnostic_generation,stage,code,successful_observation,dc_hostname,naming_context,observed_at,elapsed_ms) SELECT tenant_id,domain_id,$2,connection_revision,credential_revision,$3,diagnostic_generation,'identity','success',true,'dc1.example.test','DC=example,DC=test',$4,12 FROM adtr.domain_connections WHERE tenant_id='one' AND domain_id=$1", id, taskID, pins["policyRevision"], f.now)
		return 80, json.RawMessage("{}"), json.RawMessage("{}"), e
	})
	if err != nil {
		t.Fatal(err)
	}
	out := f.callSelection(operator, path, 200)["selection"].(map[string]any)
	if out["connectionState"] != "testing" || out["lastTest"] != nil {
		t.Fatal("staged evidence published before terminal state", out)
	}
	if _, err = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Succeeded}); err != nil {
		t.Fatal(err)
	}
	out = f.callSelection(operator, path, 200)["selection"].(map[string]any)
	assertSelectionChoice(t, out)
	if out["connectionState"] != "verified" || out["lastTest"].(map[string]any)["code"] != "ok" || out["revision"] != "1" || out["credentialRevision"] != "1" {
		t.Fatal("resolve did not refresh terminal observation", out)
	}
	filtered := f.callSelection(operator, "?observationState=verified", 200)
	if filtered["page"].(map[string]any)["total"] != float64(1) || len(filtered["List"].([]any)) != 1 {
		t.Fatal("list state disagreed with current terminal observation", filtered)
	}
	// New startup trust policy invalidates historical diagnostics without changing
	// either selection revision and without decrypting any saved credential.
	original := f.store
	f.store = domains.New(domainTestRuntime(t))
	out = f.callSelection(operator, path, 200)["selection"].(map[string]any)
	if out["connectionState"] != "unverified" || out["lastTest"] != nil {
		t.Fatal("old policy observation survived", out)
	}
	f.store = domains.New(nil)
	out = f.callSelection(operator, path, 200)["selection"].(map[string]any)
	if out["credentialConfigured"] != true || out["connectionState"] != "unverified" {
		t.Fatal("selection unexpectedly needed the credential key", out)
	}
	f.store = original
	second := taskOutputID(t, f.callDomain(operator, "/test", f.proof(map[string]any{"domainId": id, "expectedRevision": "1", "idempotencyKey": "observation-two"}), 200))
	out = f.callSelection(operator, path, 200)["selection"].(map[string]any)
	if out["connectionState"] != "testing" || out["lastTest"] != nil || second == taskID {
		t.Fatal("superseded generation observation survived", out)
	}
	lease, err = f.engine.Claim(f.ctx, "synthetic-selection-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Finish(f.ctx, lease, tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}); err != nil {
		t.Fatal(err)
	}
	out = f.callSelection(operator, path, 200)["selection"].(map[string]any)
	if out["connectionState"] != "error" || out["lastTest"] != nil {
		t.Fatal("terminal failure reused an old diagnostic", out)
	}
	beforeTasks, beforeEvents, beforeDiagnostics := f.scalar("SELECT count(*) FROM adtr.tasks"), f.scalar("SELECT count(*) FROM adtr.task_events"), f.scalar("SELECT count(*) FROM adtr.domain_diagnostics")
	for range 3 {
		f.callSelection(operator, "", 200)
		f.callSelection(operator, path, 200)
	}
	if beforeTasks != 2 || beforeDiagnostics != 1 || f.scalar("SELECT count(*) FROM adtr.tasks") != beforeTasks || f.scalar("SELECT count(*) FROM adtr.task_events") != beforeEvents || f.scalar("SELECT count(*) FROM adtr.domain_diagnostics") != beforeDiagnostics {
		t.Fatal("read changed task or diagnostic state")
	}
}

func TestDomainSelectionAccessCheckAndIndependentReadPermission(t *testing.T) {
	f := newDomainFixture(t)
	id := f.createDomain("permission-selection")
	f.grantDomain(id)
	operator := f.client(f.operator)
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	paths := []string{"GET /api/domain-selection", "GET /api/domain-selection/resolve", "POST /api/domain-selection", "POST /api/domain-selection/resolve", "GET /api/domain-selection/test", "GET /api/domain-selection-extra"}
	check := func(want []bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"paths": paths})
		r := httptest.NewRequest("POST", "/api/access/check", strings.NewReader(string(raw)))
		r.RemoteAddr = "127.0.0.1:4499"
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", operator.csrf)
		r.AddCookie(operator.cookie)
		w := httptest.NewRecorder()
		f.s.ServeAccessHTTP(w, r)
		var got struct{ Results []bool }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || !reflect.DeepEqual(got.Results, want) {
			t.Fatal("access/check disagrees with selection authority", w.Code, w.Body, err)
		}
	}
	check([]bool{true, true, false, false, false, false})
	f.callSelection(operator, "", 200)
	f.callSelection(operator, selectionResolvePath(id, "1", "1"), 200)
	for _, query := range []string{"appType=0", "application=a", "type=x", "ModuleType=0", "status=0", "statusList=0", "scanType=0"} {
		if got := f.callSelection(operator, "?"+query, 422); got["error"] != "unsupported_source_filter" {
			t.Fatal(got)
		}
	}
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'operation_accounts',true,true)", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='domains'", narrowResourceRole)
	check([]bool{false, false, false, false, false, false})
	f.callSelection(operator, "", 403)
}
