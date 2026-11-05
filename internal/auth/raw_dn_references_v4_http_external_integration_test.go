//go:build integration

package auth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const rawDNReferencesHTTPPath = "/api/directory-assets/v4/raw-dn-references"

func mountRawDNReferencesV4HTTP(f *directoryV4HTTPFixture) {
	mux := http.NewServeMux()
	mux.Handle(rawDNReferencesHTTPPath, f.service.RawDNReferencesV4Handler(f.store))
	mux.Handle(rawDNReferencesHTTPPath+"/", f.service.RawDNReferencesV4Handler(f.store))
	mux.Handle("/", f.handler)
	f.handler = mux
}
func rawDNReferencesV4HTTPPath(revision, pin, guid string) string {
	return rawDNReferencesHTTPPath + "?" + userAssetsV3HTTPQuery(revision) + "&observationId=" + pin + "&objectGUID=" + guid + "&pageIdx=1&pageSize=25"
}

func TestRawDNReferencesV4MigratedHTTPRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, sql, code string
		args            []any
		status          int
	}{
		{"disabled", `UPDATE adtr.users SET disabled=true WHERE username='directory-http-reader'`, "unauthenticated", nil, 401},
		{"permission-assets", `UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets'`, "forbidden", []any{governanceHTTPTenant, directoryHTTPReaderRole}, 403},
		{"permission-domains", `UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='domains'`, "forbidden", []any{governanceHTTPTenant, directoryHTTPReaderRole}, 403},
		{"session", `DELETE FROM adtr.sessions WHERE user_id=(SELECT id FROM adtr.users WHERE username='directory-http-reader')`, "unauthenticated", nil, 401},
		{"scope", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, "not_found", []any{governanceHTTPTenant, directoryHTTPReaderRole}, 404},
		{"expired", `UPDATE adtr.resource_tenant_config SET expire_time=extract(epoch FROM clock_timestamp()-interval '1 hour')::bigint WHERE tenant_id=$1`, "tenant_expired", []any{governanceHTTPTenant}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDirectoryV4HTTPFixture(t)
			f.bind()
			f.grantV4("directory-grant", "platform_admin", "revoke-v4-grant")
			task := f.submitV4("directory-submit-one", "revoke-v4-observe")
			o := groupMembersV4HTTPObservation()
			defer o.Discard()
			f.finishV4(f.stageV4(o), task, tasks.Succeeded)
			mountRawDNReferencesV4HTTP(f)
			path := rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[0].Base.GUID)
			f.request("directory-http-reader", path, nil, 200)
			if tc.name == "expired" {
				// An allowed producer grant makes tenant expiry a governed
				// mutation. Keep the production guard exercised before using
				// the real proof-protected management route to expire it.
				before := f.count(`SELECT expire_time FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant)
				_, err := f.conn.Exec(f.ctx, tc.sql, tc.args...)
				var denied *pgconn.PgError
				if !errors.As(err, &denied) || denied.Code != "42501" || denied.ConstraintName != "credential_use_governance" {
					t.Fatal("unguarded tenant expiry must be rejected by credential governance", err)
				}
				if f.count(`SELECT expire_time FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant) != before {
					t.Fatal("rejected tenant expiry changed persisted configuration")
				}
				f.request("directory-http-reader", path, nil, 200)
				expires := time.Now().Add(-time.Hour).Unix()
				out := f.mutate("tenant", "/api/resources/tenant/save", map[string]any{"maxAdCount": 10, "expireTime": expires, "uid": "synthetic-http", "name": "HTTP governance"}, 200)
				if out["sessionRevoked"] != true || f.count(`SELECT expire_time FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant) != expires {
					t.Fatal("governed tenant expiry was not persisted with session revocation")
				}
				// Tenant management also revokes sessions. Verify that effect,
				// then issue the fixture's usual authenticated session so the
				// final response specifically exercises current tenant expiry.
				f.error(f.request("directory-http-reader", path, nil, 401), "unauthenticated")
				reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
			} else {
				f.exec(tc.sql, tc.args...)
			}
			f.error(f.request("directory-http-reader", path, nil, tc.status), tc.code)
		})
	}
}
func TestRawDNReferencesV4HTTPWholeObservationBeforeSelectedGroup(t *testing.T) {
	f := newDirectoryV4HTTPFixture(t)
	f.bind()
	f.grantV4("directory-grant", "platform_admin", "corrupt-v4-grant")
	task := f.submitV4("directory-submit-one", "corrupt-v4-observe")
	o := groupMembersV4HTTPObservation()
	defer o.Discard()
	f.finishV4(f.stageV4(o), task, tasks.Succeeded)
	mountRawDNReferencesV4HTTP(f)
	path := rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[2].Base.GUID)
	f.request("directory-http-reader", path, nil, 200)
	var raw []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	// Corrupt the last, nonselected USER membership and recompute digest. This
	// proves full validation precedes group/GUID selection, not hash alone.
	needle := []byte(`"membership":{"memberBytes":null}`)
	index := bytes.LastIndex(raw, needle)
	if index < 0 {
		t.Fatal("missing corruption fixture")
	}
	raw = append(append(append([]byte{}, raw[:index]...), []byte(`"membership":{"memberBytes":[]}`)...), raw[index+len(needle):]...)
	sum := sha256.Sum256(raw)
	// Owned disposable database corruption case only; never a production action.
	f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
	f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, task.ID, raw, hex.EncodeToString(sum[:]))
	f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	f.error(f.request("directory-http-reader", "/api/directory/v4/observation?domainId=domain-one&observationId="+task.ID+"&kind=group", nil, 409), "directory_observation_unavailable")
}

func TestRawDNReferencesV4MigratedHTTPCurrentAuthorityAndFacts(t *testing.T) {
	f := newDirectoryV4HTTPFixture(t)
	mountRawDNReferencesV4HTTP(f)
	path := rawDNReferencesV4HTTPPath("1", "unknown-observation", "00000000-0000-0000-0000-000000000001")
	f.request("", path, nil, 401)
	for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", strings.Replace(path, "domain-one", "domain-two", 1), nil, 404)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	checks := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": []string{"GET " + rawDNReferencesHTTPPath, "POST " + rawDNReferencesHTTPPath, "GET " + rawDNReferencesHTTPPath + "/"}}, 200).Body.Bytes())
	if !reflect.DeepEqual(checks.Results, []bool{true, false, false}) {
		t.Fatal("access introspection", checks.Results)
	}
	f.bind()
	f.grantV4("directory-grant", "platform_admin", "raw-dn-grant")
	task := f.submitV4("directory-submit-one", "raw-dn-observe")
	o := groupMembersV4HTTPObservation()
	defer o.Discard()
	// Independent known-text oracle: the third group references itself and the
	// user DN, and only that group. Unknown and returned-empty groups stay distinct.
	o.Objects[2].Membership.MemberBytes = [][]byte{[]byte(o.Objects[2].Base.DN), []byte(o.Objects[3].Base.DN)}
	run := f.stageV4(o)
	mountRawDNReferencesV4HTTP(f)
	path = rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[3].Base.GUID)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	f.finishV4(run, task, tasks.Succeeded)
	for _, index := range []int{2, 3} {
		path = rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[index].Base.GUID)
		w := f.request("directory-http-reader", path, nil, 200)
		assertUserAssetsV3HTTPHeaders(t, w, f.clients["directory-http-reader"].id)
		out := operationalDecode[domains.RawDNReferencesV4](t, w.Body.Bytes())
		want, err := directoryassets.ProjectV4(o.Objects[index])
		group, groupErr := directoryassets.ProjectV4(o.Objects[2])
		if err != nil || groupErr != nil || out.DictionaryVersion != 4 || out.ObservationID != task.ID || !reflect.DeepEqual(out.Source, o.Source) || !reflect.DeepEqual(out.Object, want) || !reflect.DeepEqual(out.List, []directoryassets.PublicObjectV4{group}) || out.Page != (domains.Page{Index: 1, Size: 25, Total: 1, Pages: 1}) || out.Coverage != (domains.RawDNReferencesV4Coverage{GroupCount: 3, MemberReturnedGroupCount: 2, MemberNotReturnedGroupCount: 1}) {
			t.Fatal("stored reference facts mismatch")
		}
	}
	f.request("directory-http-reader", rawDNReferencesV4HTTPPath("2", task.ID, "ffffffff-ffff-ffff-ffff-ffffffffffff"), nil, 404)
	f.error(f.request("directory-http-reader", rawDNReferencesV4HTTPPath("1", task.ID, o.Objects[3].Base.GUID), nil, 409), "selection_changed")
	f.error(f.request("directory-http-reader", strings.Replace(path, "expectedCredentialRevision=2", "expectedCredentialRevision=1", 1), nil, 409), "selection_changed")
	f.error(f.request("directory-http-reader", rawDNReferencesV4HTTPPath("2", "missing", o.Objects[3].Base.GUID), nil, 409), "directory_observation_unavailable")
	f.request("directory-foreign", rawDNReferencesV4HTTPPath("1", task.ID, o.Objects[3].Base.GUID), nil, 409)
	f.installV4(f.runtimeWithGates(false, false), nil)
	mountRawDNReferencesV4HTTP(f)
	f.request("directory-http-reader", path, nil, 200)
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='domain.directory_read.v4'`) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE purpose='domain.directory_read.v4'`) != 1 {
		t.Fatal("read mutated producer state")
	}
}

func TestRawDNReferencesV4MigratedHTTPStablePagesAndTextIdentity(t *testing.T) {
	f := newDirectoryV4HTTPFixture(t)
	f.bind()
	f.grantV4("directory-grant", "platform_admin", "raw-dn-pages-grant")
	task := f.submitV4("directory-submit-one", "raw-dn-pages-observe")
	o := directoryV4HTTPObservation(35)
	defer o.Discard()
	targetDN := o.Objects[31].Base.DN
	for i := 0; i < 31; i++ {
		o.Objects[i].Base.Kind = directoryassets.Group
		o.Objects[i].Base.Classes = []string{"group", "top"}
		o.Objects[i].Membership.MemberBytes = [][]byte{[]byte(targetDN)}
	}
	o.Objects[0].Base.DN = targetDN
	o.Objects[1].Membership.MemberBytes = nil
	o.Objects[2].Membership.MemberBytes = [][]byte{}
	o.Objects[3].Membership.MemberBytes = [][]byte{[]byte(strings.ToUpper(targetDN))}
	o.Objects[4].Membership.MemberBytes = [][]byte{[]byte(" " + targetDN)}
	o.Objects[5].Membership.MemberBytes = [][]byte{[]byte(strings.Replace(targetDN, "CN=", "CN=\\73", 1))}
	o.Objects[32].Base.DN = targetDN
	o.Objects[33].Base.Kind = directoryassets.Computer
	o.Objects[33].Base.Classes = []string{"computer", "top", "user"}
	// Reverse stored group order. Expected GUIDs below are derived from fixture
	// identity indices, not the reader's selection or sorting implementation.
	expected := []string{}
	for i := 30; i >= 6; i-- {
		expected = append(expected, o.Objects[i].Base.GUID)
	}
	expected = append(expected, o.Objects[0].Base.GUID)
	for i, j := 0, 30; i < j; i, j = i+1, j-1 {
		o.Objects[i], o.Objects[j] = o.Objects[j], o.Objects[i]
	}
	f.finishV4(f.stageV4(o), task, tasks.Succeeded)
	mountRawDNReferencesV4HTTP(f)
	for _, idx := range []int{30, 31, 32} {
		path := rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[idx].Base.GUID)
		out := operationalDecode[domains.RawDNReferencesV4](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
		if out.Page != (domains.Page{Index: 1, Size: 25, Total: 26, Pages: 2}) || len(out.List) != 25 || out.Coverage != (domains.RawDNReferencesV4Coverage{GroupCount: 31, MemberReturnedGroupCount: 30, MemberNotReturnedGroupCount: 1}) {
			t.Fatal("first page or complete coverage")
		}
		for i, row := range out.List {
			if row.GUID != expected[i] {
				t.Fatal("stable GUID ordering")
			}
		}
		out = operationalDecode[domains.RawDNReferencesV4](t, f.request("directory-http-reader", strings.Replace(path, "pageIdx=1", "pageIdx=2", 1), nil, 200).Body.Bytes())
		if len(out.List) != 1 || out.List[0].GUID != expected[25] {
			t.Fatal("second page")
		}
		out = operationalDecode[domains.RawDNReferencesV4](t, f.request("directory-http-reader", strings.Replace(path, "pageIdx=1", "pageIdx=10000", 1), nil, 200).Body.Bytes())
		if out.List == nil || len(out.List) != 0 || out.Page.Total != 26 {
			t.Fatal("out of range page")
		}
	}
	out := operationalDecode[domains.RawDNReferencesV4](t, f.request("directory-http-reader", rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[33].Base.GUID), nil, 200).Body.Bytes())
	if len(out.List) != 0 || out.List == nil || out.Page.Total != 0 || out.Coverage.GroupCount != 31 {
		t.Fatal("computer zero match")
	}
	// A newer empty successful observation must never replace the fixed pin.
	newer := f.submitV4("directory-submit-two", "raw-dn-pages-newer")
	f.finishV4(f.stageV4(directoryV4HTTPObservation(0)), newer, tasks.Succeeded)
	mountRawDNReferencesV4HTTP(f)
	old := operationalDecode[domains.RawDNReferencesV4](t, f.request("directory-http-reader", rawDNReferencesV4HTTPPath("2", task.ID, o.Objects[31].Base.GUID), nil, 200).Body.Bytes())
	if old.ObservationID != task.ID || old.Page.Total != 26 {
		t.Fatal("fixed pin fell back")
	}
	f.request("directory-http-reader", rawDNReferencesV4HTTPPath("2", newer.ID, o.Objects[31].Base.GUID), nil, 404)
}
