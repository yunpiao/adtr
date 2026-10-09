package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestDirectoryV2PermissionsDoNotBroadenV1OrCredentialAuthority(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		grants := map[string]AccessAuth{
			"domains":            {Readable: mask&1 != 0, Writeable: mask&2 != 0},
			"directory_assets":   {Readable: mask&4 != 0, Writeable: mask&8 != 0},
			"tasks":              {Readable: mask&16 != 0, Writeable: mask&32 != 0},
			"operation_accounts": {Readable: true, Writeable: true},
		}
		for suffix, route := range directoryRoutes {
			want := mask&1 != 0 && mask&4 != 0
			if suffix != "/observation" {
				want = want && mask&16 != 0
			}
			if route.write {
				want = want && mask&8 != 0 && mask&32 != 0
			}
			v1, v2 := "/api/directory"+suffix, "/api/directory/v2"+suffix
			if directoryPathAllowed(route.method, v1, grants) != want || directoryV2PathAllowed(route.method, v2, grants) != want {
				t.Fatalf("permission intersection changed at mask=%d suffix=%s", mask, suffix)
			}
			if directoryPathAllowed(route.method, v2, grants) || directoryV2PathAllowed(route.method, v1, grants) || directoryPathAllowedForProfile(route.method, v2, grants, 0) {
				t.Fatal("request path selected a different trusted profile")
			}
		}
		wantEffective := mask&1 != 0 && mask&4 != 0
		if directoryV2CredentialUsePathAllowed("GET", "/api/directory-credential-use/v2/effective", "viewer", grants) != wantEffective {
			t.Fatal("v2 effective read bypassed independent data permissions")
		}
		if directoryV2CredentialUsePathAllowed("POST", "/api/directory-credential-use/v2/grant", "viewer", grants) {
			t.Fatal("data permissions conferred grant administration")
		}
	}
}

func TestDirectoryV2HandlerPinsProfileBeforeDatabaseAccess(t *testing.T) {
	s := &Service{origin: "http://localhost:8080"}
	endpoints := []struct {
		prefix  string
		handler http.Handler
	}{
		{"/api/directory", s.DirectoryHandler(nil, nil)},
		{"/api/directory/v2", s.DirectoryV2Handler(nil, nil)},
	}
	for _, serving := range endpoints {
		for _, requested := range endpoints {
			for suffix, route := range directoryRoutes {
				query, body := "", ""
				switch suffix {
				case "/observation":
					query = "?domainId=domain-a"
				case "/task":
					query = "?taskUUID=directory-task-1"
				case "/receipt":
					query = "?domainId=domain-a&idempotencyKey=directory-key"
				case "/sync":
					body = directorySyncBody
				case "/cancel":
					body = directoryCancelBody
				}
				r := httptest.NewRequest(route.method, requested.prefix+suffix+query, strings.NewReader(body))
				r.Header.Set("Origin", s.origin)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				serving.handler.ServeHTTP(w, r)
				want := 404
				if serving.prefix == requested.prefix {
					want = 503
				}
				if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-ADTR-User-ID") != "" {
					t.Fatalf("trusted profile %s request %s%s returned%d", serving.prefix, requested.prefix, suffix, w.Code)
				}
			}
		}
	}
	for _, body := range []string{
		strings.TrimSuffix(directorySyncBody, "}") + `,"dictionaryVersion":2}`,
		strings.TrimSuffix(directorySyncBody, "}") + `,"purpose":"domain.directory_read.v2"}`,
		strings.TrimSuffix(directorySyncBody, "}") + `,"attributes":["unicodePwd"]}`,
	} {
		r := httptest.NewRequest("POST", "/api/directory/v2/sync", strings.NewReader(body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.DirectoryV2Handler(nil, nil).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("caller-supplied profile metadata accepted", w.Code)
		}
	}
	for _, query := range []string{"dictionaryVersion=2", "purpose=domain.directory_read.v2", "pageSize=50&pageSize=25"} {
		w := httptest.NewRecorder()
		s.DirectoryV2Handler(nil, nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/directory/v2/observation?domainId=domain-a&"+query, nil))
		if w.Code != 400 {
			t.Fatal("unexpected profile/ambiguous query accepted", w.Code)
		}
	}
}

func TestDirectoryV2GenericTaskMetadataAndControlsRemainProtected(t *testing.T) {
	for _, kind := range []string{domains.DirectoryKindName, domains.DirectoryV2KindName} {
		out := publicTask(tasks.Task{Kind: kind, Result: json.RawMessage(`{"private":"secret"}`), Cursor: json.RawMessage(`{"cookie":"secret"}`)})
		if string(out.Result) != "{}" || string(out.Cursor) != "{}" || dedicatedTaskRouteCode(kind) != "directory_route_required" {
			t.Fatal("generic directory task leaked data or bypassed dedicated controls")
		}
	}
	if isDirectoryTaskKind("domain.directory_read.v3") || isDirectoryTaskKind("domain.directory_read.v2 ") {
		t.Fatal("unknown kind selected directory policy")
	}
	prefix, enabled := credentialUseEndpointForPurpose(credentialuse.DirectoryV2Purpose)
	if prefix != "/api/directory-credential-use/v2" || !enabled {
		t.Fatal("versioned purpose capability unavailable")
	}
}
