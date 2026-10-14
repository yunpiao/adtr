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

func TestComputerAssetExportV3PermissionsDoNotPromoteReadersOrCollectors(t *testing.T) {
	for mask := 0; mask < 256; mask++ {
		g := map[string]AccessAuth{"domains": {Readable: mask&1 != 0}, "directory_assets": {Readable: mask&2 != 0, Writeable: mask&4 != 0}, "tasks": {Readable: mask&8 != 0, Writeable: mask&16 != 0}, "computer_asset_exports": {Readable: mask&32 != 0, Writeable: mask&64 != 0}, "user_asset_exports": {Readable: true, Writeable: true}, "audit_exports": {Readable: true, Writeable: mask&128 != 0}}
		read := g["domains"].Readable && g["directory_assets"].Readable && g["tasks"].Readable && g["computer_asset_exports"].Readable
		write := read && g["tasks"].Writeable && g["computer_asset_exports"].Writeable
		for path, route := range computerAssetExportV3Routes {
			want := read
			if route.write {
				want = write
			}
			if computerAssetExportV3PathAllowed(route.method, computerAssetExportV3Path+path, g) != want {
				t.Fatalf("mask=%d path=%s", mask, path)
			}
			if computerAssetExportV3PathAllowed("DELETE", computerAssetExportV3Path+path, g) {
				t.Fatal("unknown method")
			}
		}
	}
	if dedicatedTaskRouteCode(domains.ComputerAssetExportV3KindName) != "computer_asset_export_route_required" {
		t.Fatal("generic bypass")
	}
	task := publicTask(tasks.Task{Kind: domains.ComputerAssetExportV3KindName, Result: []byte(`{"private":"digest"}`), Cursor: []byte(`{"fence":1}`)})
	if string(task.Result) != "{}" || string(task.Cursor) != "{}" {
		t.Fatal("private task metadata exposed")
	}
}
func TestComputerAssetExportV3HTTPRejectsAmbiguousInputBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://synthetic.invalid"}
	h := s.ComputerAssetExportV3Handler(nil, nil)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", computerAssetExportV3Path, "", 405}, {"GET", computerAssetExportV3Path + "/unknown", "", 404},
		{"GET", computerAssetExportV3Path + "/detail?taskID=one&taskID=two", "", 400}, {"GET", computerAssetExportV3Path + "/detail?taskUUID=one", "", 400},
		{"GET", computerAssetExportV3Path + "/receipt?domainId=one&idempotencyKey=k", "{}", 400},
		{"POST", computerAssetExportV3Path, `{"screenColumns":[],"actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", computerAssetExportV3Path + "/cancel", `{"taskID":"one","taskID":"two","actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", computerAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"\ud800","totpCode":"123456"}`, 400},
		{"POST", computerAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"x","totpCode":"123456","tenantId":"foreign"}`, 400},
		{"GET", computerAssetExportV3Path + "/detail?taskID=one", "", 503},
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
func TestComputerAssetExportV3DecoderExpandsColumnsAndRedactsProof(t *testing.T) {
	raw := []byte(`{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`)
	r := httptest.NewRequest(http.MethodPost, computerAssetExportV3Path, bytes.NewReader(raw))
	in, err := decodeComputerAssetExportV3Request(httptest.NewRecorder(), r, "")
	if err != nil || len(in.ScreenColumns) != 5 {
		t.Fatal(err)
	}
	safe, _ := in.MarshalJSON()
	if strings.Contains(string(safe), "secret") {
		t.Fatal("proof exposed")
	}
}

func TestComputerExportKnownUnsupportedFilterClassification(t *testing.T) {
	s := &Service{origin: "https://synthetic.invalid"}
	h := s.ComputerAssetExportV3Handler(nil, nil)
	base := `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`
	for _, key := range []string{"computerStatus", "computerType", "status", "domainName", "groupMold", "groupScope", "groupType", "updateTm", "overviewType", "sAMAccountName", "scoreSort", "userStatus", "userType", "assetsType", "domain"} {
		t.Run(key, func(t *testing.T) {
			raw := strings.TrimSuffix(base, "}") + `,"` + key + `":"x"}`
			r := httptest.NewRequest(http.MethodPost, computerAssetExportV3Path, strings.NewReader(raw))
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 422 || !strings.Contains(w.Body.String(), `"error":"unsupported_computer_asset_filter"`) {
				t.Fatalf("%s: %d %s", key, w.Code, w.Body.String())
			}
		})
	}
	for _, key := range []string{"arbitraryField", "pageIdx", "pageSize", "GroupMold"} {
		raw := strings.TrimSuffix(base, "}") + `,"` + key + `":"x"}`
		r := httptest.NewRequest(http.MethodPost, computerAssetExportV3Path, strings.NewReader(raw))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s widened: %d", key, w.Code)
		}
	}
}
