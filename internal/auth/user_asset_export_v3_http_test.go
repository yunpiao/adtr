package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestUserAssetExportV3PermissionsDoNotPromoteReadersOrCollectors(t *testing.T) {
	for mask := 0; mask < 256; mask++ {
		g := map[string]AccessAuth{"domains": {Readable: mask&1 != 0}, "directory_assets": {Readable: mask&2 != 0, Writeable: mask&4 != 0}, "tasks": {Readable: mask&8 != 0, Writeable: mask&16 != 0}, "user_asset_exports": {Readable: mask&32 != 0, Writeable: mask&64 != 0}, "audit_exports": {Readable: true, Writeable: mask&128 != 0}}
		read := g["domains"].Readable && g["directory_assets"].Readable && g["tasks"].Readable && g["user_asset_exports"].Readable
		write := read && g["tasks"].Writeable && g["user_asset_exports"].Writeable
		for path, route := range userAssetExportV3Routes {
			want := read
			if route.write {
				want = write
			}
			if userAssetExportV3PathAllowed(route.method, userAssetExportV3Path+path, g) != want {
				t.Fatalf("mask=%d path=%s", mask, path)
			}
			if userAssetExportV3PathAllowed("DELETE", userAssetExportV3Path+path, g) {
				t.Fatal("unknown method")
			}
		}
	}
	if dedicatedTaskRouteCode(domains.UserAssetExportV3KindName) != "user_asset_export_route_required" {
		t.Fatal("generic bypass")
	}
	task := publicTask(tasks.Task{Kind: domains.UserAssetExportV3KindName, Result: []byte(`{"private":"digest"}`), Cursor: []byte(`{"fence":1}`)})
	if string(task.Result) != "{}" || string(task.Cursor) != "{}" {
		t.Fatal("private task metadata exposed")
	}
}
func TestUserAssetExportV3HTTPRejectsAmbiguousInputBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://synthetic.invalid"}
	h := s.UserAssetExportV3Handler(nil, nil)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", userAssetExportV3Path, "", 405}, {"GET", userAssetExportV3Path + "/unknown", "", 404},
		{"GET", userAssetExportV3Path + "/detail?taskID=one&taskID=two", "", 400}, {"GET", userAssetExportV3Path + "/detail?taskUUID=one", "", 400},
		{"GET", userAssetExportV3Path + "/receipt?domainId=one&idempotencyKey=k", "{}", 400},
		{"POST", userAssetExportV3Path, `{"screenColumns":[],"actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", userAssetExportV3Path + "/cancel", `{"taskID":"one","taskID":"two","actorPassword":"x","totpCode":"123456"}`, 400},
		{"POST", userAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"\ud800","totpCode":"123456"}`, 400},
		{"POST", userAssetExportV3Path + "/cancel", `{"taskID":"one","actorPassword":"x","totpCode":"123456","tenantId":"foreign"}`, 400},
		{"GET", userAssetExportV3Path + "/detail?taskID=one", "", 503},
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
func TestUserAssetExportV3DecoderExpandsColumnsAndRedactsProof(t *testing.T) {
	raw := []byte(`{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`)
	r := httptest.NewRequest(http.MethodPost, userAssetExportV3Path, bytes.NewReader(raw))
	in, err := decodeUserAssetExportV3Request(httptest.NewRecorder(), r, "")
	if err != nil || len(in.ScreenColumns) != 6 {
		t.Fatal(err)
	}
	safe, _ := in.MarshalJSON()
	if strings.Contains(string(safe), "secret") {
		t.Fatal("proof exposed")
	}
}

func TestUserAssetExportV3HTTPUACPresenceAndClosedEnum(t *testing.T) {
	const base = `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"1","observationId":"obs","screenColumns":[],"idempotencyKey":"key","actorPassword":"secret","totpCode":"123456"}`
	s := &Service{origin: "https://synthetic.invalid"}
	for _, value := range []string{`"set"`, `"clear"`, `"not_returned"`, `""`, `null`, `2`, `true`, `[]`, `["set"]`, `{}`, `"SET"`, `"clear "`, `"disabled"`} {
		t.Run(value, func(t *testing.T) {
			raw := strings.TrimSuffix(base, "}") + `,"uacDisableBit":` + value + `}`
			r := httptest.NewRequest("POST", userAssetExportV3Path, strings.NewReader(raw))
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.UserAssetExportV3Handler(nil, nil).ServeHTTP(w, r)
			want := 400
			if value == `"set"` || value == `"clear"` || value == `"not_returned"` {
				want = 503
			}
			if w.Code != want {
				t.Fatalf("HTTP %d want %d; explicit invalid filter must not disappear", w.Code, want)
			}
			if want == 503 {
				in, err := decodeUserAssetExportV3Request(httptest.NewRecorder(), httptest.NewRequest("POST", userAssetExportV3Path, strings.NewReader(raw)), "")
				encoded, _ := json.Marshal(in.UserAssetExportV3Payload)
				if err != nil || in.UACDisableBit == nil || !bytes.Contains(encoded, []byte(`"uacDisableBit":`+value)) {
					t.Fatal("filter erased during HTTP canonicalization", err)
				}
			}
		})
	}
	for _, tail := range []string{`,"uacDisableBit":"set","uacDisableBit":"set"}`, `,"uacDisableBit":"set","uacDisableBit":"clear"}`} {
		_, err := decodeUserAssetExportV3Request(httptest.NewRecorder(), httptest.NewRequest("POST", userAssetExportV3Path, strings.NewReader(strings.TrimSuffix(base, "}")+tail)), "")
		if status, code := domainHTTPError(err); status != 400 || code != "invalid_input" {
			t.Fatal("duplicate filter accepted", err)
		}
	}
	cancel := `{"taskID":"one","actorPassword":"x","totpCode":"123456","uacDisableBit":"set"}`
	if _, err := decodeUserAssetExportV3Request(httptest.NewRecorder(), httptest.NewRequest("POST", userAssetExportV3Path+"/cancel", strings.NewReader(cancel)), "/cancel"); err == nil {
		t.Fatal("cancel accepted submission-only field")
	}
}
