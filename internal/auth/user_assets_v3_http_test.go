package auth

import (
	"net/http/httptest"
	"net/url"
	"reflect"
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

func TestUserAssetsV3UACQueryClosedAndDoesNotMutateV2Query(t *testing.T) {
	base := "domainId=domain&expectedRevision=1&expectedCredentialRevision=2"
	for _, bit := range []string{"set", "clear", "not_returned"} {
		q, err := url.ParseQuery(base + "&uacDisableBit=" + bit)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := url.ParseQuery(q.Encode())
		f, _, err := userAssetsV3Query(false, q)
		if err != nil || f.UACDisableBit != bit || !reflect.DeepEqual(q, before) {
			t.Fatal("v3 query parsing mutated input or lost bit", err)
		}
		if _, _, err := userAssetsV2Query(false, q); err == nil {
			t.Fatal("v2 accepted v3 filter")
		}
		q.Set("observationId", "pinned")
		q.Set("objectGUID", "00112233-4455-6677-8899-aabbccddeeff")
		if _, _, err := userAssetsV3Query(true, q); err == nil {
			t.Fatal("detail accepted list-only filter")
		}
	}
	for _, values := range [][]string{nil, {}, {""}, {"set", "set"}, {"set", "clear"}, {"SET"}, {"clear "}, {" not_returned"}, {"disabled"}, {"0"}, {"2"}, {"null"}, {"[set]"}, {"\xff"}} {
		q, _ := url.ParseQuery(base)
		q["uacDisableBit"] = values
		_, _, err := userAssetsV3Query(false, q)
		status, code := domainHTTPError(err)
		if status != 400 || code != "invalid_input" {
			t.Fatalf("accepted %#v: %v", values, err)
		}
	}
	q, _ := url.ParseQuery(base + "&uacDisableBit=set&userStatus=1")
	_, _, err := userAssetsV3Query(false, q)
	status, code := domainHTTPError(err)
	if status != 422 || code != "unsupported_user_asset_filter" {
		t.Fatal("legacy userStatus no longer deferred", err)
	}
}

func TestUserAssetsV3UACHTTPValidationBeforeDatabase(t *testing.T) {
	s := &Service{}
	base := userAssetsV3Path + "?domainId=domain&expectedRevision=1&expectedCredentialRevision=2"
	for _, tc := range []struct {
		suffix string
		status int
	}{
		{"&uacDisableBit=set", 503}, {"&uacDisableBit=clear", 503}, {"&uacDisableBit=not_returned", 503},
		{"&uacDisableBit=", 400}, {"&uacDisableBit", 400}, {"&uacDisableBit=set&uacDisableBit=set", 400},
		{"&uacDisableBit[]=set", 400}, {"&uacDisableBit=unknown", 400}, {"&uacDisableBit=SET", 400},
		{"&uacDisableBit=+set", 400}, {"&uacDisableBit=null", 400}, {"&uacDisableBit=2", 400},
		{"&uacDisableBit=set&userStatus=1", 422},
	} {
		w := httptest.NewRecorder()
		s.UserAssetV3Handler(nil).ServeHTTP(w, httptest.NewRequest("GET", base+tc.suffix, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d want %d", tc.suffix, w.Code, tc.status)
		}
	}
	w := httptest.NewRecorder()
	s.UserAssetsV2Handler(nil).ServeHTTP(w, httptest.NewRequest("GET", strings.Replace(base, "/v3?", "/v2?", 1)+"&uacDisableBit=set", nil))
	if w.Code != 400 {
		t.Fatal("v2 filter contract widened")
	}
}
