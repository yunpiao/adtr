package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageReadAuthorityDoesNotRequireWriteOrProof(t *testing.T) {
	for mask := 0; mask < 4; mask++ {
		g := map[string]AccessAuth{"tasks": {Readable: mask&1 != 0, Writeable: mask&2 != 0}}
		for path, method := range messageRoutes {
			if messagePathAllowed(method, "/api/messages"+path, g) != (mask&1 != 0) {
				t.Fatal(mask, path)
			}
			if messagePathAllowed("DELETE", "/api/messages"+path, g) {
				t.Fatal("unexpected method")
			}
		}
		for _, path := range []string{"/api/messages", "/api/messages/", "/api/messages/page/", "/api/messages/query?all=true", "/api/messages/read/all"} {
			if messagePathAllowed("GET", path, g) || messagePathAllowed("POST", path, g) {
				t.Fatal("nonexact route")
			}
		}
	}
	nodes := permissionNodes(map[string]AccessAuth{"tasks": {Readable: true}}, true)
	seen := map[string]string{}
	for _, n := range nodes {
		for _, p := range n.Paths {
			seen[p.URL] = p.Auth
		}
	}
	for p, m := range messageRoutes {
		if seen[m+" /api/messages"+p] != "readable" {
			t.Fatal("missing read-only metadata", p)
		}
	}
}
func TestMessageStrictBodyContract(t *testing.T) {
	cases := []struct {
		path, body string
		valid      bool
	}{
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":200}`, true},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":20,"subTypes":[{"type":"task_result","subType":"succeeded"}]}`, true},
		{"/read", `{"resultId":"result-1","messageId":"msg-1","operationId":"op-1"}`, true},
		{"/read", `{"resultId":"r","messageId":"m","operationId":"o","actorPassword":"secret"}`, false},
		{"/read", `{"resultId":"r","messageId":"m","operationId":"o","userId":1}`, false},
		{"/read", `{"resultId":"r","messageId":"m","operationId":"o","all":true}`, false},
		{"/read", `{"resultId":"r","messageId":"m","operationId":"o","messageId":"n"}`, false},
		{"/read", `{"resultId":"r","messageId":"m"}`, false},
		{"/read", `{"resultId":"r","messageId":[],"operationId":"o"}`, false},
		{"/read", `{"resultId":"r","messageId":"m","operationId":""}`, false},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":20,"confirmedAt":"2026-01-01"}`, false},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":20,"subTypes":[{"type":"x","subType":"y","tenantId":"other"}]}`, false},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":20,"subTypes":null}`, false},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0}`, false},
		{"/query", `{"readFilter":"all","sort":"asc","offset":0,"limit":20} {}`, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/messages"+c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeMessageInput(httptest.NewRecorder(), r, c.path)
		if (err == nil) != c.valid {
			t.Fatalf("%s %s: %v", c.path, c.body, err)
		}
	}
}
func TestMessageStrictPageAndOperationURL(t *testing.T) {
	for _, c := range []struct {
		path, query string
		ok          bool
	}{
		{"/page", "resultId=r&offset=0&limit=200", true}, {"/page", "resultId=r&offset=999999&limit=1", true},
		{"/locate", "resultId=r&messageId=m&limit=20", true}, {"/read-operation", "operationId=o", true},
		{"/page", "resultId=r&offset=0&limit=201", false}, {"/page", "resultId=r&offset=0&limit=-1", false}, {"/page", "resultId=r&offset=0&limit=0", false},
		{"/page", "resultId=r&offset=00&limit=20", false}, {"/page", "resultId=r&offset=-1&limit=20", false}, {"/page", "resultId=r&offset=0&limit=20&limit=50", false},
		{"/page", "resultId=r&offset=0&limit=20&userId=1", false}, {"/locate", "resultId=r&messageId=m&limit=20&keyword=a", false},
		{"/read-operation", "operationId=o&resultId=r", false}, {"/page", "resultId=r&offset=0&limit=20&", true},
	} {
		r := httptest.NewRequest("GET", "/api/messages"+c.path+"?"+c.query, nil)
		_, err := parseMessageURL(r, c.path)
		if (err == nil) != c.ok {
			t.Fatal(c, err)
		}
	}
}
func TestMessageHTTPRejectsBeforeDatabase(t *testing.T) {
	s := &Service{origin: "https://app.invalid"}
	h := s.MessagesHandler(nil)
	for _, c := range []struct {
		method, path, body, origin, media string
		status                            int
	}{
		{"GET", "/api/messages", "", "", "", 404}, {"GET", "/api/messages/", "", "", "", 404}, {"DELETE", "/api/messages/read", "", "", "", 405},
		{"POST", "/api/messages/read", `{}`, "https://evil.invalid", "application/json", 403},
		{"POST", "/api/messages/read", `{}`, s.origin, "text/plain", 400},
		{"POST", "/api/messages/read", `{"resultId":"r","messageId":"m","operationId":"o"}`, s.origin, "application/json", 503},
		{"GET", "/api/messages/page?resultId=r&offset=0&limit=200", "", "", "", 503},
		{"GET", "/api/messages/page?resultId=r&offset=0&limit=200", "body", "", "", 400},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Content-Type", c.media)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(c, w.Code, w.Body.String())
		}
	}
}
