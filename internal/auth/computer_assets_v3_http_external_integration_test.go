//go:build integration

package auth_test

import (
	"fmt"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
	"strings"
	"testing"
)

const computerHTTPPath = "/api/computer-assets/v3"

func mountComputerHTTP(f *directoryV3HTTPFixture) {
	mux := http.NewServeMux()
	mux.Handle(computerHTTPPath, f.service.ComputerAssetsV3Handler(f.store))
	mux.Handle(computerHTTPPath+"/", f.service.ComputerAssetsV3Handler(f.store))
	mux.Handle("/api/operation-accounts/", f.service.OperationAccountsHandler(operationaccounts.New(f.runtime)))
	mux.Handle("/", f.handler)
	f.handler = mux
}
func TestComputerAssetsV3MigratedHTTPCurrentAuthorityAndObservedFacts(t *testing.T) {
	f := newDirectoryV3HTTPFixture(t)
	mountComputerHTTP(f)
	path := computerHTTPPath + "?" + userAssetsV3HTTPQuery("1")
	f.request("", path, nil, 401)
	for _, a := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(a, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", strings.Replace(path, "domain-one", "domain-two", 1), nil, 404)
	absent := operationalDecode[domains.ComputerAssetsV3List](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if absent.Available || absent.ObservedComputerCount != nil {
		t.Fatal("absence lost")
	}
	f.bind()
	f.grantV3("directory-grant", "platform_admin", "computer-http-grant")
	task := f.submitV3("directory-submit-one", "computer-http-first")
	o := directoryV3HTTPObservation(12)
	defer o.Discard()
	for i := range o.Objects {
		o.Objects[i].Base.Kind = directoryassets.Computer
		o.Objects[i].Base.Classes = []string{"computer", "top", "user"}
		sam := fmt.Sprintf("computer-%02d$", 12-i)
		o.Objects[i].Base.SAMAccountName = &sam
	}
	o.Objects = append(o.Objects, directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "ffffffff-0000-0000-0000-000000000001", DN: "CN=user,DC=one,DC=invalid", Kind: directoryassets.User, Classes: []string{"top", "user"}}})
	// stageV3 installs the synthetic executor and rebuilds the fixture mux.
	// Restore this endpoint before testing staged and committed reads.
	run := f.stageV3(o)
	mountComputerHTTP(f)
	path = computerHTTPPath + "?" + userAssetsV3HTTPQuery("2")
	staged := operationalDecode[domains.ComputerAssetsV3List](t, f.request("directory-http-reader", path, nil, 200).Body.Bytes())
	if staged.Available {
		t.Fatal("staged visible")
	}
	f.finishV3(run, task, tasks.Succeeded)
	w := f.request("directory-http-reader", path, nil, 200)
	assertUserAssetsV3HTTPHeaders(t, w, f.clients["directory-http-reader"].id)
	first := operationalDecode[domains.ComputerAssetsV3List](t, w.Body.Bytes())
	if !first.Available || *first.ObservedComputerCount != 12 || first.Page.Total != 12 || len(first.List) != 10 || first.ObservationID != task.ID {
		t.Fatal(first)
	}
	for _, r := range first.List {
		if r.Kind != directoryassets.Computer {
			t.Fatal("user inheritance leaked")
		}
	}
	second := operationalDecode[domains.ComputerAssetsV3List](t, f.request("directory-http-reader", path+"&observationId="+task.ID+"&pageIdx=2", nil, 200).Body.Bytes())
	if len(second.List) != 2 || second.List[0].GUID <= first.List[9].GUID {
		t.Fatal(second)
	}
	f.error(f.request("directory-http-reader", path+"&observationId=missing", nil, 409), "directory_observation_unavailable")
	f.error(f.request("directory-http-reader", computerHTTPPath+"?"+userAssetsV3HTTPQuery("1"), nil, 409), "selection_changed")
	f.error(f.request("directory-http-no-scope", computerHTTPPath+"?"+userAssetsV3HTTPQuery("1"), nil, 404), "not_found")
}
func TestComputerAssetsV3MigratedHTTPRevocationClearsAccess(t *testing.T) {
	for _, tc := range []struct {
		name, sql, code string
		args            []any
		status          int
	}{
		{"disabled", `UPDATE adtr.users SET disabled=true WHERE username='directory-http-reader'`, "unauthenticated", nil, 401},
		{"forced-password", `UPDATE adtr.users SET must_change=true WHERE username='directory-http-reader'`, "password_change_required", nil, 403},
		{"session", `DELETE FROM adtr.sessions WHERE user_id=(SELECT id FROM adtr.users WHERE username='directory-http-reader')`, "unauthenticated", nil, 401},
		{"scope", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, "not_found", []any{governanceHTTPTenant, directoryHTTPReaderRole}, 404},
		{"expired", `UPDATE adtr.resource_tenant_config SET expire_time=extract(epoch FROM clock_timestamp()-interval '1 hour')::bigint WHERE tenant_id=$1`, "tenant_expired", []any{governanceHTTPTenant}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDirectoryV3HTTPFixture(t)
			mountComputerHTTP(f)
			path := computerHTTPPath + "?" + userAssetsV3HTTPQuery("1")
			f.request("directory-http-reader", path, nil, 200)
			f.exec(tc.sql, tc.args...)
			for _, p := range []string{path, path + "&observationId=missing&pageIdx=2", path + "&search=computer&startTm=0001-01-01T00:00:00Z&endTm=2026-01-01T00:00:00Z&creatTm=desc"} {
				w := f.request("directory-http-reader", p, nil, tc.status)
				f.error(w, tc.code)
				if w.Header().Get("X-ADTR-User-ID") != "" {
					t.Fatal("denial actor leak")
				}
			}
		})
	}
}
