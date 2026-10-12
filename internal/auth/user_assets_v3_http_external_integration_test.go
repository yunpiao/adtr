//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
)

const userAssetsV3HTTPPath = "/api/user-assets/v3"

func mountUserAssetsV3HTTP(f *directoryV3HTTPFixture) {
	mux := http.NewServeMux()
	mux.Handle(userAssetsV3HTTPPath, f.service.UserAssetV3Handler(f.store))
	mux.Handle(userAssetsV3HTTPPath+"/", f.service.UserAssetV3Handler(f.store))
	mux.Handle("/api/operation-accounts/", f.service.OperationAccountsHandler(operationaccounts.New(f.runtime)))
	mux.Handle("/", f.handler)
	f.handler = mux
}
func userAssetsV3HTTPQuery(revision string) string {
	return "domainId=domain-one&expectedRevision=" + revision + "&expectedCredentialRevision=" + revision
}
func userAssetsV3HTTPDetail(revision, pin, guid string) string {
	return userAssetsV3HTTPPath + "/detail?" + userAssetsV3HTTPQuery(revision) + "&observationId=" + pin + "&objectGUID=" + guid
}
func assertUserAssetsV3HTTPHeaders(t *testing.T, w *httptest.ResponseRecorder, id int64) {
	t.Helper()
	if w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(id, 10) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private user-assets response protections missing", w.Header())
	}
}

func TestUserAssetsV3MigratedHTTPAuthorityAndIntrospection(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	mountUserAssetsV3HTTP(f)
	list := userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("1")
	detail := userAssetsV3HTTPDetail("1", "unknown-observation", "00000000-0000-0000-0000-000000000001")
	for _, path := range []string{list, detail} {
		f.request("", path, nil, 401)
		for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
			f.request(actor, path, nil, 403)
		}
		f.request("directory-http-no-scope", path, nil, 404)
		f.request("directory-admin", strings.Replace(path, "domain-one", "domain-two", 1), nil, 404)
	}
	// A real current source without v3 evidence is unavailable, not observed empty.
	out := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes())
	if out.Available || out.DictionaryVersion != 3 || out.ObservationID != "" || out.Source != nil || out.List == nil || len(out.List) != 0 || out.Page.Total != 0 || out.Selection.DomainID != "domain-one" || out.Selection.Revision != "1" {
		t.Fatal("unavailable source misrepresented", out)
	}
	f.error(f.request("directory-http-reader", detail, nil, 409), "directory_observation_unavailable")
	f.error(f.request("directory-http-reader", strings.Replace(list, "expectedRevision=1", "expectedRevision=2", 1), nil, 409), "selection_changed")
	f.error(f.request("directory-http-no-scope", strings.Replace(list, "expectedRevision=1", "expectedRevision=2", 1), nil, 404), "not_found")
	paths := []string{"GET /api/user-assets/v3", "GET /api/user-assets/v3/detail", "GET /api/user-assets/v3/", "POST /api/user-assets/v3", "GET /api/directory/v3/task", "POST /api/directory/v3/sync", "POST /api/directory-credential-use/v3/grant"}
	checks := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": paths}, 200).Body.Bytes())
	if !reflect.DeepEqual(checks.Results, []bool{true, true, false, false, false, false, false}) {
		t.Fatal("user-asset introspection widened authority", checks.Results)
	}
	if f.count(`SELECT count(*) FROM adtr.tasks`)+f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`)+f.count(`SELECT count(*) FROM adtr.operation_account_use_grants`) != 0 {
		t.Fatal("stored reads created tasks/uses/grants")
	}
}

func TestUserAssetsV3MigratedHTTPValuesSearchDetailAndPinnedRefresh(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	grant := f.grantV3("directory-grant", "platform_admin", "user-assets-producer-grant")
	first := f.submitV3("directory-submit-one", "user-assets-first")
	o := directoryV3HTTPObservation(12)
	defer o.Discard()
	// Every observed value is persisted through the actual guarded task/SQL path.
	// Only the producer Execute is synthetic here; TLS/LDAP evidence is separate.
	for i := range o.Objects {
		n := len(o.Objects) - i
		sam := fmt.Sprintf("user-%02d", n)
		o.Objects[i].Base.SAMAccountName = &sam
		if n == 12 {
			o.Objects[i].Supplemental.MailBytes = []byte("needle+%_?*&@one.invalid")
			o.Objects[i].Supplemental.ObjectSIDBytes = []byte{1, 1, 0, 0, 0, 0, 0, 5, 99, 0, 0, 0}
		}
	}
	o.Objects = append(o.Objects, directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "10000000-0000-0000-0000-000000000001", DN: "CN=group,DC=one,DC=invalid", Kind: directoryassets.Group, Classes: []string{"group", "top"}}}, directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "20000000-0000-0000-0000-000000000001", DN: "CN=computer,DC=one,DC=invalid", Kind: directoryassets.Computer, Classes: []string{"computer", "top", "user"}}})
	run := f.stageV3(o)
	mountUserAssetsV3HTTP(f)
	list := userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("2")
	if staged := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes()); staged.Available {
		t.Fatal("staged observation visible before succeeded")
	}
	f.finishV3(run, first, tasks.Succeeded)
	w := f.request("directory-http-reader", list, nil, 200)
	assertUserAssetsV3HTTPHeaders(t, w, f.clients["directory-http-reader"].id)
	page := operationalDecode[domains.UserAssetsV3List](t, w.Body.Bytes())
	if !page.Available || page.ObservationID != first.ID || len(page.List) != 10 || page.Page.Total != 12 || page.Page.Pages != 2 || page.Page.Size != 10 || page.Source == nil || page.Selection.Revision != "2" || page.Selection.CredentialRevision != "2" {
		t.Fatal("user filtering/default pagination", page)
	}
	for i, object := range page.List {
		if object.GUID != fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1) || object.Kind != directoryassets.User {
			t.Fatal("unstable/user-decoy list", object)
		}
	}
	one := page.List[0]
	if one.PrimaryGroupID == nil || *one.PrimaryGroupID != 0 || !reflect.DeepEqual(one.ServicePrincipalName, []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}) {
		t.Fatal("v3 identity changed")
	}
	if page.List[1].PrimaryGroupID != nil || page.List[1].ServicePrincipalName != nil {
		t.Fatal("v3 missing became zero/empty")
	}
	if one.Mail == nil || *one.Mail != " Raw\x00😀@one.invalid " || one.WhenCreated == nil || *one.WhenCreated != "0001-01-01T00:00:00Z" || one.UserAccountControl == nil || *one.UserAccountControl != 0 || !reflect.DeepEqual(one.Description, []string{"line\r\n\t\x00<script>😀"}) {
		t.Fatal("factual raw/zero/year semantics", one)
	}
	pin := "&observationId=" + first.ID
	for _, tc := range []struct {
		extra string
		total int
	}{
		{"&sAMAccountName=user-12", 1}, {"&sAMAccountName=USER-12", 0}, {"&sAMAccountName=user", 0},
		{"&sAMAccountName=user-12&search=absent", 0},
		{"&startTm=0001-01-01T00:00:00Z&endTm=0001-01-01T00:00:00.000000001Z&creatTm=desc", 1},
		{"&startTm=0001-01-01T00:00:00.000000001Z&endTm=2026-01-01T00:00:00Z", 0},
	} {
		result := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+tc.extra, nil, 200).Body.Bytes())
		if result.Page.Total != tc.total || result.ObservationID != first.ID {
			t.Fatal("HTTP exact/time predicate mismatch", tc, result.Page)
		}
		f.request("directory-http-no-scope", list+pin+tc.extra, nil, 404)
	}
	for _, order := range []string{"asc", "desc"} {
		result := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+"&creatTm="+order, nil, 200).Body.Bytes())
		if result.List[0].WhenCreated == nil || result.List[0].GUID != one.GUID || result.List[1].WhenCreated != nil {
			t.Fatal("year1/null sorting mismatch", order)
		}
	}
	for _, term := range []string{"USER-12", "S-1-5-99", "needle+%_?*&", "CN=synthetic-12"} {
		result := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+"&search="+url.QueryEscape(term), nil, 200).Body.Bytes())
		if result.Page.Total != 1 || len(result.List) != 1 || result.List[0].GUID != "00000000-0000-0000-0000-00000000000c" {
			t.Fatal("search did not match after first page", term, result)
		}
	}
	nul := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+"&search=%00", nil, 200).Body.Bytes())
	if nul.Page.Total != 1 || nul.List[0].GUID != one.GUID {
		t.Fatal("NUL search not on raw value", nul)
	}
	for _, term := range []string{"<script>", "00000000-0000-0000-0000-000000000001", "%does-not-exist%"} {
		result := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+"&search="+url.QueryEscape(term), nil, 200).Body.Bytes())
		if !result.Available || result.Page.Total != 0 || len(result.List) != 0 {
			t.Fatal("accidental description/GUID/wildcard search", term)
		}
	}
	dpath := userAssetsV3HTTPDetail("2", first.ID, one.GUID)
	dw := f.request("directory-http-reader", dpath, nil, 200)
	assertUserAssetsV3HTTPHeaders(t, dw, f.clients["directory-http-reader"].id)
	detail := operationalDecode[domains.UserAssetV3Detail](t, dw.Body.Bytes())
	if detail.ObservationID != first.ID || !reflect.DeepEqual(detail.Object, one) || !reflect.DeepEqual(detail.Source, *page.Source) {
		t.Fatal("detail not pinned factual list row", detail)
	}
	nullDetail := operationalDecode[domains.UserAssetV3Detail](t, f.request("directory-http-reader", userAssetsV3HTTPDetail("2", first.ID, page.List[1].GUID), nil, 200).Body.Bytes())
	if nullDetail.Object.Mail != nil || nullDetail.Object.Description != nil || nullDetail.Object.ObjectSID != nil || nullDetail.Object.WhenCreated != nil {
		t.Fatal("null supplemental values invented", nullDetail)
	}
	for _, guid := range []string{"30000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000001"} {
		f.error(f.request("directory-http-reader", userAssetsV3HTTPDetail("2", first.ID, guid), nil, 404), "not_found")
	}
	f.request("directory-foreign", userAssetsV3HTTPDetail("1", first.ID, one.GUID), nil, 409)
	newer := f.submitV3("directory-submit-two", "user-assets-new-empty")
	f.finishV3(f.stageV3(directoryV3HTTPObservation(0)), newer, tasks.Succeeded)
	mountUserAssetsV3HTTP(f)
	pinned := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list+pin+"&pageIdx=2", nil, 200).Body.Bytes())
	latest := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes())
	if len(pinned.List) != 2 || pinned.ObservationID != first.ID || !latest.Available || latest.ObservationID != newer.ID || latest.Page.Total != 0 || len(latest.List) != 0 {
		t.Fatal("pinned continuation/newest empty changed", pinned, latest)
	}
	f.request("directory-http-reader", dpath, nil, 200)
	f.installV3(f.runtimeWithGates(false, false), nil)
	mountUserAssetsV3HTTP(f)
	f.request("directory-revoke", directoryV3HTTPUsePrefix+"/revoke", f.proof("directory-revoke", directoryV3HTTPGrantBody("platform_admin", grant, "user-assets-revoke-producer")), 200)
	tasksBefore, usesBefore := f.count(`SELECT count(*) FROM adtr.tasks`), f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`)
	f.request("directory-http-reader", list+pin, nil, 200)
	f.request("directory-http-reader", dpath, nil, 200)
	f.error(f.request("directory-admin", directoryV3HTTPPrefix+"/sync", f.proof("directory-admin", directoryHTTPSyncBody("user-assets-disabled-collection")), 503), "directory_read_disabled")
	if f.count(`SELECT count(*) FROM adtr.tasks`) != tasksBefore || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`) != usesBefore {
		t.Fatal("read mutated task/credential-use ledger")
	}
	f.request("directory-detach", "/api/domains/credential-source/detach", f.proof("directory-detach", map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedConnectionCredentialGeneration": "2", "idempotencyKey": "user-assets-detach"}), 200)
	f.error(f.request("directory-http-reader", list+pin, nil, 409), "selection_changed")
	f.error(f.request("directory-http-reader", dpath, nil, 409), "selection_changed")
}

func TestUserAssetsV3MigratedHTTPLiveReaderRevocation(t *testing.T) {
	cases := []struct {
		name, sql string
		args      []any
		status    int
		code      string
	}{
		{"disabled", `UPDATE adtr.users SET disabled=true WHERE username='directory-http-reader'`, nil, 401, "unauthenticated"},
		{"forced-password", `UPDATE adtr.users SET must_change=true WHERE username='directory-http-reader'`, nil, 403, "password_change_required"},
		{"session", `DELETE FROM adtr.sessions WHERE user_id=(SELECT id FROM adtr.users WHERE username='directory-http-reader')`, nil, 401, "unauthenticated"},
		{"domain-inactive", `UPDATE adtr.resource_domains SET active=false WHERE tenant_id=$1 AND id='domain-one'`, []any{governanceHTTPTenant}, 404, "not_found"},
		{"scope", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, []any{governanceHTTPTenant, directoryHTTPReaderRole}, 404, "not_found"},
		{"expired-tenant", `UPDATE adtr.resource_tenant_config SET expire_time=extract(epoch FROM clock_timestamp()-interval '1 hour')::bigint WHERE tenant_id=$1`, []any{governanceHTTPTenant}, 403, "tenant_expired"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newDirectoryV3HTTPFixture(t)
			mountUserAssetsV3HTTP(f)
			if c.name == "domain-inactive" {
				// Respect the production parent/account guard: remove the
				// unbound fixture account via its protected API first.
				f.request("directory-admin", "/api/operation-accounts/delete", f.proof("directory-admin", map[string]any{"accountId": governanceHTTPAccount, "expectedRevision": "1", "confirmAccountId": governanceHTTPAccount, "idempotencyKey": "user-assets-remove-before-inactivate"}), 200)
			}
			list := userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("1")
			f.request("directory-http-reader", list, nil, 200)
			f.exec(c.sql, c.args...)
			for _, path := range []string{list, list + "&sAMAccountName=exact&startTm=0001-01-01T00:00:00Z&endTm=2026-01-01T00:00:00Z&creatTm=desc", userAssetsV3HTTPDetail("1", "missing-observation", "00000000-0000-0000-0000-000000000001")} {
				w := f.request("directory-http-reader", path, nil, c.status)
				f.error(w, c.code)
				if w.Header().Get("X-ADTR-User-ID") != "" {
					t.Fatal("denial exposed actor header")
				}
			}
		})
	}
}

func TestUserAssetsV3MigratedHTTPRechecksQueuedReaderAfterRevocation(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	mountUserAssetsV3HTTP(f)
	// Hold the same tenant lock used by live identity/permission changes, then
	// observe a real waiting query before committing the revocation.
	blocker, err := pgx.ConnectConfig(f.ctx, f.conn.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(f.ctx)
	tx, err := blocker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, governanceHTTPTenant); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", userAssetsV3HTTPPath+"?"+userAssetsV3HTTPQuery("1"), nil).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:8735"
	r.AddCookie(f.clients["directory-http-reader"].cookie)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); f.handler.ServeHTTP(w, r) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("blocked user read did not exit")
		}
	})
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for !waiting && time.Now().Before(deadline) {
		if err = f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory' AND position('adtr-access:' in query)>0)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case <-done:
				t.Fatal("read bypassed tenant lock")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if !waiting {
		t.Fatal("read not observed waiting on tenant lock")
	}
	tag, err := tx.Exec(f.ctx, `UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, governanceHTTPTenant, directoryHTTPReaderRole)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("permission revocation", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("read failed to resume", ctx.Err())
	}
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != 403 || len(out) != 1 || out["error"] != "forbidden" || w.Header().Get("X-ADTR-User-ID") != "" {
		t.Fatal("queued read retained old grants", w.Code, w.Body.String())
	}
}

func TestUserAssetsV3HTTPUninitializedSchemaGatePrecedesIdentity(t *testing.T) {
	f := newOperationalHTTPFixture(t, false)
	cfg := f.cfg.Copy()
	cfg.Tracer = f.trace
	s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = s.UserAssetV3Handler(domains.New(nil))
	f.trace.take()
	for _, path := range []string{userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("1"), userAssetsV3HTTPDetail("1", "missing-observation", "00000000-0000-0000-0000-000000000001")} {
		f.error(f.call(nil, path, nil, 503), "schema_incompatible")
	}
	for _, q := range f.trace.take() {
		lower := strings.ToLower(q)
		for _, forbidden := range []string{"adtr.users", "adtr.sessions", "insert ", "update ", "delete "} {
			if strings.Contains(lower, forbidden) {
				t.Fatal("schema failure reached identity/business/writes", q)
			}
		}
	}
}

// Synthetic HTTP/SQL evidence only: actual Worker/TLS browser tests are separate.
func (f *directoryV3HTTPFixture) stageV3(observation ldapconnection.DirectoryV3Observation) *directoryHTTPSyntheticRun {
	f.t.Helper()
	objects := make([]json.RawMessage, 0, len(observation.Objects))
	for _, object := range observation.Objects {
		raw, err := directoryassets.EncodeStoredObjectV3(object)
		if err != nil {
			f.t.Fatal("validate synthetic object", err)
		}
		objects = append(objects, raw)
	}
	raw, err := json.Marshal(struct {
		DictionaryVersion int                            `json:"dictionaryVersion"`
		Objects           []json.RawMessage              `json:"objects"`
		Source            ldapconnection.DirectorySource `json:"source"`
	}{3, objects, observation.Source})
	for _, object := range objects {
		clear(object)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { clear(raw) })
	digest := sha256.Sum256(raw)
	staged, release, done := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	run := &directoryHTTPSyntheticRun{release: func() { once.Do(func() { close(release) }) }, done: done}
	f.installV3(f.runtime, func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved' AND purpose='domain.directory_read.v3' AND dictionary_version=3`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = fmt.Errorf("synthetic opened-use transition affected %d rows", tag.RowsAffected())
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err == nil {
			_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				_, err := tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,3)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.ActorID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt, len(observation.Objects), raw, hex.EncodeToString(digest[:]))
				if err == nil {
					// This synthetic executor stages both outputs that the real LDAP
					// producer commits atomically. The audit view must still wait for
					// the real engine finish before exposing a successful result.
					var tag pgconn.CommandTag
					tag, err = tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result)
 SELECT tenant_id,actor_id,domain_id,'domain_directory_v3_result',(payload->>'connectionRevision')::bigint,(payload->>'connectionRevision')::bigint,(payload->>'connectionCredentialGeneration')::bigint,task_id,'success'
 FROM adtr.tasks WHERE task_id=$1 AND kind='domain.directory_read.v3'`, ex.Task.ID)
					if err == nil && tag.RowsAffected() != 1 {
						err = fmt.Errorf("synthetic v3 audit stage did not match the admitted task")
					}
				}
				return 95, json.RawMessage(`{"policyRevision":"synthetic-v3-private-cursor"}`), json.RawMessage(`{"objectGUID":"synthetic-v3-private-result"}`), err
			})
		}
		staged <- err
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_stage_failed"}
		}
		select {
		case <-release:
			return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"objectGUID":"synthetic-v3-private-result"}`)}
		case <-ctx.Done():
			<-release
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_cancelled"}
		}
	})
	ctx, cancel := context.WithCancel(f.ctx)
	engine := f.engine
	go func() {
		worked, err := engine.RunOne(ctx, "synthetic-directory-v3-http")
		if err == nil && !worked {
			err = fmt.Errorf("synthetic v3 executor found no admitted task")
		}
		done <- err
	}()
	f.t.Cleanup(func() {
		run.release()
		cancel()
		if !run.joined {
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
				f.t.Error("synthetic v3 executor did not return during cleanup")
			}
		}
	})
	select {
	case err := <-staged:
		if err != nil {
			f.t.Fatal("real guarded v3 publication", err)
		}
	case err := <-done:
		run.joined = true
		f.t.Fatal("real engine returned before v3 staging", err)
	case <-f.ctx.Done():
		f.t.Fatal("v3 staging timed out", f.ctx.Err())
	}
	return run
}

func (f *directoryV3HTTPFixture) finishV3(run *directoryHTTPSyntheticRun, task tasks.Task, state tasks.State) {
	f.t.Helper()
	run.release()
	select {
	case err := <-run.done:
		run.joined = true
		if err != nil {
			f.t.Fatal("real v3 engine finish/return acknowledgement", err)
		}
	case <-f.ctx.Done():
		f.t.Fatal("actual v3 executor return not observed", f.ctx.Err())
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state=$2`, task.ID, string(state)) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND purpose='domain.directory_read.v3' AND dictionary_version=3 AND state='quiesced' AND quiescence_reason='executor_returned'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v3' AND object_id=$1`, task.ID) != 0 {
		f.t.Fatal("actual v3 return did not persist the outcome and original-opener acknowledgement")
	}
}

func directoryV3HTTPObservation(users int) ldapconnection.DirectoryV3Observation {
	old := directoryV2HTTPObservation(users)
	out := ldapconnection.DirectoryV3Observation{Source: old.Source, Objects: []directoryassets.StoredObjectV3{}}
	for _, row := range old.Objects {
		identity := directoryassets.RawIdentityV3{}
		if row.Base.GUID == "00000000-0000-0000-0000-000000000001" {
			identity = directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("0"), ServicePrincipalNameBytes: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀<script>"), []byte("HOST/a")}}
		}
		out.Objects = append(out.Objects, directoryassets.StoredObjectV3{Base: row.Base, Supplemental: row.Supplemental, Identity: identity})
	}
	return out
}

func TestUserAssetsV3HTTPValidatesWholeObservationBeforeFilteredPage(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	f.grantV3("directory-grant", "platform_admin", "user-v3-corruption-grant")
	task := f.submitV3("directory-submit-one", "user-v3-corruption")
	observation := directoryV3HTTPObservation(12)
	defer observation.Discard()
	f.finishV3(f.stageV3(observation), task, tasks.Succeeded)
	mountUserAssetsV3HTTP(f)
	query := userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("2") + "&observationId=" + task.ID
	f.request("directory-http-reader", query, nil, 200)
	var raw []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	// Corrupt an object outside the first GUID page and exact filter; compute a
	// matching digest so the test proves complete body validation, not hash alone.
	// Only this isolated disposable corruption fixture bypasses immutability.
	raw = bytes.Replace(raw, []byte(`"primaryGroupIDBytes":null`), []byte(`"primaryGroupIDBytes":"@@@="`), 1)
	sum := sha256.Sum256(raw)
	f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
	f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, task.ID, raw, hex.EncodeToString(sum[:]))
	f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
	for _, path := range []string{query, query + "&sAMAccountName=missing", query + "&pageIdx=10000", userAssetsV3HTTPDetail("2", task.ID, "00000000-0000-0000-0000-000000000001")} {
		f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	}
	if f.count(`SELECT count(*) FROM adtr.auth_audit WHERE action='user_assets_v3_list_denied'`) != 3 || f.count(`SELECT count(*) FROM adtr.auth_audit WHERE action='user_assets_v3_detail_denied'`) != 1 || f.count(`SELECT count(*) FROM adtr.auth_audit WHERE action IN ('user_assets_v2_list_denied','user_assets_v2_detail_denied')`) != 0 {
		t.Fatal("v3 failures misattributed to v2")
	}
}
