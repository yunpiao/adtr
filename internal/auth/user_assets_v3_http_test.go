package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserAssetsV3StrictReadOnlyRoutesAndQuery(t *testing.T) {
	s := &Service{origin: "http://example.test"}
	good := "domainId=domain&expectedRevision=1&expectedCredentialRevision=2"
	for _, item := range []struct {
		method, path, query string
		status              int
	}{
		{"GET", userAssetsV3Path, good, 503},
		{"GET", userAssetsV3Path, good + "&pageSize=25", 400},
		{"GET", userAssetsV3Path, good + "&pageIdx=2", 400},
		{"GET", userAssetsV3Path, good + "&dictionaryVersion=3", 400},
		{"GET", userAssetsV3Path, good + "&primaryGroupID=0", 400},
		{"GET", userAssetsV3Path, good + "&servicePrincipalName=x", 400},
		{"GET", userAssetsV3Path, good + "&search=x&search=x", 400},
		{"GET", userAssetsV3Path, good + "&userStatus=0", 422},
		{"GET", userAssetsV3Path, good + "&sAMAccountName=User&startTm=0001-01-01T00:00:00Z&endTm=0001-01-01T00:00:01Z&creatTm=desc", 503},
		{"GET", userAssetsV3Path + "/unknown", good, 404},
		{"GET", "/api/user-assets/v2", good, 404},
		{"POST", userAssetsV3Path, good, 405},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(item.method, item.path+"?"+item.query, nil)
		s.UserAssetV3Handler(nil).ServeHTTP(w, r)
		if w.Code != item.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s %d want %d", item.method, item.path, w.Code, item.status)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", userAssetsV3Path+"?"+good, strings.NewReader("{}"))
	s.UserAssetV3Handler(nil).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("GET body accepted")
	}
	for _, grants := range []map[string]AccessAuth{{}, {"domains": {Readable: true}}, {"directory_assets": {Readable: true}}} {
		if userAssetV3PathAllowed("GET", userAssetsV3Path, grants) {
			t.Fatal("permission intersection widened")
		}
	}
}
