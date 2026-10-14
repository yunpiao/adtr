package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestGroupAssetExportV3PermissionsDoNotPromoteReadersOrCollectors(t *testing.T) {
	for mask := 0; mask < 256; mask++ {
		g := map[string]AccessAuth{"domains": {Readable: mask&1 != 0}, "directory_assets": {Readable: mask&2 != 0, Writeable: mask&4 != 0}, "tasks": {Readable: mask&8 != 0, Writeable: mask&16 != 0}, "group_asset_exports": {Readable: mask&32 != 0, Writeable: mask&64 != 0}, "user_asset_exports": {Readable: true, Writeable: true}, "audit_exports": {Readable: true, Writeable: mask&128 != 0}}
		read := g["domains"].Readable && g["directory_assets"].Readable && g["tasks"].Readable && g["group_asset_exports"].Readable
		write := read && g["tasks"].Writeable && g["group_asset_exports"].Writeable
		for path, route := range groupAssetExportV3Routes {
			want := read
			if route.write {
				want = write
			}
			if groupAssetExportV3PathAllowed(route.method, groupAssetExportV3Path+path, g) != want {
				t.Fatalf("mask=%d path=%s", mask, path)
			}
			if groupAssetExportV3PathAllowed("DELETE", groupAssetExportV3Path+path, g) {
				t.Fatal("unknown method")
			}
		}
	}
	if dedicatedTaskRouteCode(domains.GroupAssetExportV3KindName) != "group_asset_export_route_required" {
		t.Fatal("generic bypass")
	}
	task := publicTask(tasks.Task{Kind: domains.GroupAssetExportV3KindName, Result: []byte(`{"private":"digest"}`), Cursor: []byte(`{"fence":1}`)})
	if string(task.Result) != "{}" || string(task.Cursor) != "{}" {
		t.Fatal("private task metadata exposed")
	}
}
func TestGroupAssetExportV3HTTPRejectsAmbiguousInputBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://synthetic.invalid"}
	h := s.GroupAssetExportV3Handler(nil, nil)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", groupAssetExportV3Path, "", 405}, {"GET", groupAssetExportV3Path + "/unknown", "", 404},
		{"GET", groupAssetExportV3Path + "/detail?taskID=one&taskID=two", "", 400}, {"GET", groupAssetExportV3Path + "/detail?taskUUID=one", "", 400},
		{"GET", groupAssetExportV3Path + "/receipt?domainId=one&idempotencyKey=k", "{}", 400},
		{"POST", groupAssetExportV3Path, `{"screenColumns":[],"actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", groupAssetExportV3Path + "/cancel", `{"taskID":"one","taskID":"two","actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", groupAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"\ud800","totpCode":"123456"}`, 400},
		{"POST", groupAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"x","totpCode":"123456","tenantId":"foreign"}`, 400},
		{"GET", groupAssetExportV3Path + "/detail?taskID=one", "", 503},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
func TestGroupAssetExportV3DecoderExpandsColumnsAndRedactsProof(t *testing.T) {
	raw := []byte(`{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`)
	r := httptest.NewRequest(http.MethodPost, groupAssetExportV3Path, bytes.NewReader(raw))
	in, err := decodeGroupAssetExportV3Request(httptest.NewRecorder(), r, "")
	if err != nil || len(in.ScreenColumns) != 5 {
		t.Fatal(err)
	}
	safe, _ := in.MarshalJSON()
	if strings.Contains(string(safe), "secret") {
		t.Fatal("proof exposed")
	}
}

func TestGroupExportKnownUnsupportedFilterClassification(t *testing.T) {
	s := &Service{origin: "https://synthetic.invalid"}
	h := s.GroupAssetExportV3Handler(nil, nil)
	base := `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`
	for _, key := range []string{"status", "domainName", "groupMold", "groupScope", "groupType", "startTm", "endTm", "updateTm", "overviewType", "sAMAccountName", "scoreSort", "userStatus", "userType", "assetsType", "domain"} {
		t.Run(key, func(t *testing.T) {
			raw := strings.TrimSuffix(base, "}") + `,"` + key + `":"x"}`
			r := httptest.NewRequest(http.MethodPost, groupAssetExportV3Path, strings.NewReader(raw))
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 422 || !strings.Contains(w.Body.String(), `"error":"unsupported_group_asset_filter"`) {
				t.Fatalf("%s: %d %s", key, w.Code, w.Body.String())
			}
		})
	}
	for _, key := range []string{"arbitraryField", "pageIdx", "pageSize", "GroupMold"} {
		raw := strings.TrimSuffix(base, "}") + `,"` + key + `":"x"}`
		r := httptest.NewRequest(http.MethodPost, groupAssetExportV3Path, strings.NewReader(raw))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s widened: %d", key, w.Code)
		}
	}
}
