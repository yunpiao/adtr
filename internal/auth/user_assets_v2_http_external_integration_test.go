//go:build integration

package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
)

const userAssetsHTTPPath = "/api/user-assets/v2"

func mountUserAssetsHTTP(f *directoryV2HTTPFixture) {
	mux := http.NewServeMux()
	mux.Handle(userAssetsHTTPPath, f.service.UserAssetsV2Handler(f.store))
	mux.Handle(userAssetsHTTPPath+"/", f.service.UserAssetsV2Handler(f.store))
	mux.Handle("/api/operation-accounts/", f.service.OperationAccountsHandler(operationaccounts.New(f.runtime)))
	mux.Handle("/", f.handler)
	f.handler = mux
}
func userAssetsHTTPQuery(revision string) string {
	return "domainId=domain-one&expectedRevision=" + revision + "&expectedCredentialRevision=" + revision
}
func userAssetsHTTPDetail(revision, pin, guid string) string {
	return userAssetsHTTPPath + "/detail?" + userAssetsHTTPQuery(revision) + "&observationId=" + pin + "&objectGUID=" + guid
}
func assertUserAssetsHTTPHeaders(t *testing.T, w *httptest.ResponseRecorder, id int64) {
	t.Helper()
	if w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(id, 10) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private user-assets response protections missing", w.Header())
	}
}

func TestUserAssetsV2MigratedHTTPAuthorityAndIntrospection(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	mountUserAssetsHTTP(f)
	list := userAssetsHTTPPath + "?" + userAssetsHTTPQuery("1")
	detail := userAssetsHTTPDetail("1", "unknown-observation", "00000000-0000-0000-0000-000000000001")
	for _, path := range []string{list, detail} {
		f.request("", path, nil, 401)
		for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
			f.request(actor, path, nil, 403)
		}
		f.request("directory-http-no-scope", path, nil, 404)
		f.request("directory-admin", strings.Replace(path, "domain-one", "domain-two", 1), nil, 404)
	}
	// A real current source without v2 evidence is unavailable, not observed empty.
	out := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes())
	if out.Available || out.DictionaryVersion != 2 || out.ObservationID != "" || out.Source != nil || out.List == nil || len(out.List) != 0 || out.Page.Total != 0 || out.Selection.DomainID != "domain-one" || out.Selection.Revision != "1" {
		t.Fatal("unavailable source misrepresented", out)
	}
	f.error(f.request("directory-http-reader", detail, nil, 409), "directory_observation_unavailable")
	f.error(f.request("directory-http-reader", strings.Replace(list, "expectedRevision=1", "expectedRevision=2", 1), nil, 409), "selection_changed")
	f.error(f.request("directory-http-no-scope", strings.Replace(list, "expectedRevision=1", "expectedRevision=2", 1), nil, 404), "not_found")
	paths := []string{"GET /api/user-assets/v2", "GET /api/user-assets/v2/detail", "GET /api/user-assets/v2/", "POST /api/user-assets/v2", "GET /api/directory/v2/task", "POST /api/directory/v2/sync", "POST /api/directory-credential-use/v2/grant"}
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

func TestUserAssetsV2MigratedHTTPValuesSearchDetailAndPinnedRefresh(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	f.bind()
	grant := f.grantV2("directory-grant", "platform_admin", "user-assets-producer-grant")
	first := f.submitV2("directory-submit-one", "user-assets-first")
	o := directoryV2HTTPObservation(12)
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
	o.Objects = append(o.Objects, directoryassets.StoredObjectV2{Base: directoryassets.Object{GUID: "10000000-0000-0000-0000-000000000001", DN: "CN=group,DC=one,DC=invalid", Kind: directoryassets.Group, Classes: []string{"group", "top"}}}, directoryassets.StoredObjectV2{Base: directoryassets.Object{GUID: "20000000-0000-0000-0000-000000000001", DN: "CN=computer,DC=one,DC=invalid", Kind: directoryassets.Computer, Classes: []string{"computer", "top", "user"}}})
	run := f.stageV2(o)
	mountUserAssetsHTTP(f)
	list := userAssetsHTTPPath + "?" + userAssetsHTTPQuery("2")
	if staged := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes()); staged.Available {
		t.Fatal("staged observation visible before succeeded")
	}
	f.finishV2(run, first, tasks.Succeeded)
	w := f.request("directory-http-reader", list, nil, 200)
	assertUserAssetsHTTPHeaders(t, w, f.clients["directory-http-reader"].id)
	page := operationalDecode[domains.UserAssetsV2List](t, w.Body.Bytes())
	if !page.Available || page.ObservationID != first.ID || len(page.List) != 10 || page.Page.Total != 12 || page.Page.Pages != 2 || page.Page.Size != 10 || page.Source == nil || page.Selection.Revision != "2" || page.Selection.CredentialRevision != "2" {
		t.Fatal("user filtering/default pagination", page)
	}
	for i, object := range page.List {
		if object.GUID != fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1) || object.Kind != directoryassets.User {
			t.Fatal("unstable/user-decoy list", object)
		}
	}
	one := page.List[0]
	if one.Mail == nil || *one.Mail != " Raw\x00😀@one.invalid " || one.WhenCreated == nil || *one.WhenCreated != "0001-01-01T00:00:00Z" || one.UserAccountControl == nil || *one.UserAccountControl != 0 || !reflect.DeepEqual(one.Description, []string{"line\r\n\t\x00<script>😀"}) {
		t.Fatal("factual raw/zero/year semantics", one)
	}
	pin := "&observationId=" + first.ID
	for _, term := range []string{"USER-12", "S-1-5-99", "needle+%_?*&", "CN=synthetic-12"} {
		result := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list+pin+"&search="+url.QueryEscape(term), nil, 200).Body.Bytes())
		if result.Page.Total != 1 || len(result.List) != 1 || result.List[0].GUID != "00000000-0000-0000-0000-00000000000c" {
			t.Fatal("search did not match after first page", term, result)
		}
	}
	nul := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list+pin+"&search=%00", nil, 200).Body.Bytes())
	if nul.Page.Total != 1 || nul.List[0].GUID != one.GUID {
		t.Fatal("NUL search not on raw value", nul)
	}
	for _, term := range []string{"<script>", "00000000-0000-0000-0000-000000000001", "%does-not-exist%"} {
		result := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list+pin+"&search="+url.QueryEscape(term), nil, 200).Body.Bytes())
		if !result.Available || result.Page.Total != 0 || len(result.List) != 0 {
			t.Fatal("accidental description/GUID/wildcard search", term)
		}
	}
	dpath := userAssetsHTTPDetail("2", first.ID, one.GUID)
	dw := f.request("directory-http-reader", dpath, nil, 200)
	assertUserAssetsHTTPHeaders(t, dw, f.clients["directory-http-reader"].id)
	detail := operationalDecode[domains.UserAssetV2Detail](t, dw.Body.Bytes())
	if detail.ObservationID != first.ID || !reflect.DeepEqual(detail.Object, one) || !reflect.DeepEqual(detail.Source, *page.Source) {
		t.Fatal("detail not pinned factual list row", detail)
	}
	nullDetail := operationalDecode[domains.UserAssetV2Detail](t, f.request("directory-http-reader", userAssetsHTTPDetail("2", first.ID, page.List[1].GUID), nil, 200).Body.Bytes())
	if nullDetail.Object.Mail != nil || nullDetail.Object.Description != nil || nullDetail.Object.ObjectSID != nil || nullDetail.Object.WhenCreated != nil {
		t.Fatal("null supplemental values invented", nullDetail)
	}
	for _, guid := range []string{"30000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000001"} {
		f.error(f.request("directory-http-reader", userAssetsHTTPDetail("2", first.ID, guid), nil, 404), "not_found")
	}
	f.request("directory-foreign", userAssetsHTTPDetail("1", first.ID, one.GUID), nil, 409)
	newer := f.submitV2("directory-submit-two", "user-assets-new-empty")
	f.finishV2(f.stageV2(directoryV2HTTPObservation(0)), newer, tasks.Succeeded)
	mountUserAssetsHTTP(f)
	pinned := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list+pin+"&pageIdx=2", nil, 200).Body.Bytes())
	latest := operationalDecode[domains.UserAssetsV2List](t, f.request("directory-http-reader", list, nil, 200).Body.Bytes())
	if len(pinned.List) != 2 || pinned.ObservationID != first.ID || !latest.Available || latest.ObservationID != newer.ID || latest.Page.Total != 0 || len(latest.List) != 0 {
		t.Fatal("pinned continuation/newest empty changed", pinned, latest)
	}
	f.request("directory-http-reader", dpath, nil, 200)
	f.installV2(f.runtimeWithGates(false, false), nil)
	mountUserAssetsHTTP(f)
	f.request("directory-revoke", directoryV2HTTPUsePrefix+"/revoke", f.proof("directory-revoke", directoryV2HTTPGrantBody("platform_admin", grant, "user-assets-revoke-producer")), 200)
	tasksBefore, usesBefore := f.count(`SELECT count(*) FROM adtr.tasks`), f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`)
	f.request("directory-http-reader", list+pin, nil, 200)
	f.request("directory-http-reader", dpath, nil, 200)
	f.error(f.request("directory-admin", directoryV2HTTPPrefix+"/sync", f.proof("directory-admin", directoryHTTPSyncBody("user-assets-disabled-collection")), 503), "directory_read_disabled")
	if f.count(`SELECT count(*) FROM adtr.tasks`) != tasksBefore || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses`) != usesBefore {
		t.Fatal("read mutated task/credential-use ledger")
	}
	f.request("directory-detach", "/api/domains/credential-source/detach", f.proof("directory-detach", map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedConnectionCredentialGeneration": "2", "idempotencyKey": "user-assets-detach"}), 200)
	f.error(f.request("directory-http-reader", list+pin, nil, 409), "selection_changed")
	f.error(f.request("directory-http-reader", dpath, nil, 409), "selection_changed")
}

func TestUserAssetsV2MigratedHTTPLiveReaderRevocation(t *testing.T) {
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
			f := newDirectoryV2HTTPFixture(t)
			mountUserAssetsHTTP(f)
			if c.name == "domain-inactive" {
				// Respect the production parent/account guard: remove the
				// unbound fixture account via its protected API first.
				f.request("directory-admin", "/api/operation-accounts/delete", f.proof("directory-admin", map[string]any{"accountId": governanceHTTPAccount, "expectedRevision": "1", "confirmAccountId": governanceHTTPAccount, "idempotencyKey": "user-assets-remove-before-inactivate"}), 200)
			}
			list := userAssetsHTTPPath + "?" + userAssetsHTTPQuery("1")
			f.request("directory-http-reader", list, nil, 200)
			f.exec(c.sql, c.args...)
			for _, path := range []string{list, userAssetsHTTPDetail("1", "missing-observation", "00000000-0000-0000-0000-000000000001")} {
				w := f.request("directory-http-reader", path, nil, c.status)
				f.error(w, c.code)
				if w.Header().Get("X-ADTR-User-ID") != "" {
					t.Fatal("denial exposed actor header")
				}
			}
		})
	}
}

func TestUserAssetsV2MigratedHTTPRechecksQueuedReaderAfterRevocation(t *testing.T) {
	f := newDirectoryV2HTTPFixture(t)
	mountUserAssetsHTTP(f)
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
	r := httptest.NewRequest("GET", userAssetsHTTPPath+"?"+userAssetsHTTPQuery("1"), nil).WithContext(ctx)
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

func TestUserAssetsV2HTTPUninitializedSchemaGatePrecedesIdentity(t *testing.T) {
	f := newOperationalHTTPFixture(t, false)
	cfg := f.cfg.Copy()
	cfg.Tracer = f.trace
	s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = s.UserAssetsV2Handler(domains.New(nil))
	f.trace.take()
	for _, path := range []string{userAssetsHTTPPath + "?" + userAssetsHTTPQuery("1"), userAssetsHTTPDetail("1", "missing-observation", "00000000-0000-0000-0000-000000000001")} {
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
