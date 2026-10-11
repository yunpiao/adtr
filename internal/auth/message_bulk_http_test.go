package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageBulkStrictHTTPInput(t *testing.T) {
	for _, c := range []struct {
		path, body string
		valid      bool
	}{
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"all"}`, true},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"selected","messageIds":["m1","m2"]}`, true},
		{"/bulk-read-cancel", `{"operationId":"o"}`, true},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"all","messageIds":[]}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"selected","messageIds":[]}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"selected","messageIds":["m","m"]}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"selected","messageIds":["other/id"]}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"selected","messageIds":null}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"all","authorizationVersion":1}`, false},
		{"/bulk-read", `{"resultId":"r","operationId":"o","selection":"all","selection":"selected"}`, false},
		{"/bulk-read-cancel", `{"operationId":"o","resultId":"r"}`, false},
		{"/bulk-read-cancel", `{"operationId":""}`, false},
	} {
		r := httptest.NewRequest("POST", "/api/messages"+c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		_, err := decodeMessageInput(httptest.NewRecorder(), r, c.path)
		if (err == nil) != c.valid {
			t.Fatal(c, err)
		}
	}
	for _, raw := range []string{"operationId=o", "operationId=o&operationId=p", "operationId=o&userId=2"} {
		r := httptest.NewRequest("GET", "/api/messages/bulk-read-operation?"+raw, nil)
		_, err := parseMessageURL(r, "/bulk-read-operation")
		if (err == nil) != (raw == "operationId=o") {
			t.Fatal(raw, err)
		}
	}
}
