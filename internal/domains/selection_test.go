package domains

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSelectionFilterStrictVocabulary(t *testing.T) {
	for _, test := range []struct {
		query string
		want  SelectionFilter
	}{
		{"", SelectionFilter{PageIdx: 1, PageSize: 20}},
		{"pageIdx=1000000&pageSize=50&keyword=DC%25_&observationState=verified", SelectionFilter{PageIdx: 1000000, PageSize: 50, Keyword: "DC%_", ObservationState: "verified"}},
		{"keyword=" + url.QueryEscape(strings.Repeat("界", 50)), SelectionFilter{PageIdx: 1, PageSize: 20, Keyword: strings.Repeat("界", 50)}},
	} {
		q, e := url.ParseQuery(test.query)
		if e != nil {
			t.Fatal(e)
		}
		got, e := ParseSelectionFilter(q)
		if e != nil || got != test.want {
			t.Fatalf("query %q: %+v, %v", test.query, got, e)
		}
	}
	for _, query := range []string{
		"pageIdx=0", "pageIdx=01", "pageIdx=%2B1", "pageIdx=1000001", "pageIdx=1.0", "pageIdx=1e2", "pageIdx=9223372036854775808",
		"pageSize=1", "pageSize=-1", "pageSize=100", "pageSize=0", "pageSize=01", "pageSize=20&pageSize=20", "pageIdx=1&pageIdx=1",
		"keyword=a&keyword=b", "keyword=%00", "keyword=%09", "keyword=%0A", "keyword=%7F", "keyword=%C2%85", "keyword=%FF", "keyword=" + strings.Repeat("a", 51),
		"observationState=", "observationState=success", "observationState=VERIFIED", "observationState=verified&observationState=testing",
		"PageIdx=1", "unknown=1", "filterKeyword=a", "filterDomain=example.test", "appType=1&unknown=2", "appType=1&pageSize=1", "appType=1&appType=2",
	} {
		q, e := url.ParseQuery(query)
		if e != nil {
			t.Fatal(e)
		}
		_, e = ParseSelectionFilter(q)
		selectionError(t, e, 400, "invalid_input")
	}
	for _, key := range []string{"appType", "application", "type", "ModuleType", "status", "statusList", "scanType"} {
		_, e := ParseSelectionFilter(url.Values{key: {""}})
		selectionError(t, e, 422, "unsupported_source_filter")
	}
	_, e := ParseSelectionFilter(url.Values{"keyword": {}})
	selectionError(t, e, 400, "invalid_input")
	for _, size := range []string{"10", "20", "30", "40", "50"} {
		for _, state := range []string{"unverified", "testing", "verified", "error"} {
			if _, e := ParseSelectionFilter(url.Values{"pageSize": {size}, "observationState": {state}}); e != nil {
				t.Fatal(size, state, e)
			}
		}
	}
}

func TestSelectionResolveExactIdentityAndRevisions(t *testing.T) {
	valid := url.Values{"domainId": {"domain-opaque_1"}, "expectedRevision": {"9223372036854775807"}, "expectedCredentialRevision": {"1"}}
	in, e := ParseSelectionInput(valid)
	if e != nil || in.DomainID != "domain-opaque_1" || in.ExpectedRevision != "9223372036854775807" || in.ExpectedCredentialRevision != "1" {
		t.Fatal(in, e)
	}
	for _, key := range []string{"domainId", "expectedRevision", "expectedCredentialRevision"} {
		for _, value := range []string{"", "0", "+1", "01", "1.0", "1e1", "-1", " 1", "9223372036854775808", "platform", "invalid/id"} {
			if key == "domainId" && ValidID(value) {
				continue
			}
			q := url.Values{}
			for k, vs := range valid {
				q[k] = slices.Clone(vs)
			}
			q[key] = []string{value}
			_, e = ParseSelectionInput(q)
			selectionError(t, e, 400, "invalid_input")
		}
		q, _ := url.ParseQuery(valid.Encode())
		q.Add(key, q.Get(key))
		_, e = ParseSelectionInput(q)
		selectionError(t, e, 400, "invalid_input")
		delete(q, key)
		_, e = ParseSelectionInput(q)
		selectionError(t, e, 400, "invalid_input")
	}
	valid.Set("extra", "value")
	_, e = ParseSelectionInput(valid)
	selectionError(t, e, 400, "invalid_input")
}

func TestSelectionProjectionOnlyFrozenFields(t *testing.T) {
	c := Choice{DomainID: "opaque-id", Domain: "example.test", Revision: "9007199254740993", CredentialRevision: "2", DCHostName: "dc.example.test", Port: "389", Mode: "starttls", CredentialConfigured: true, ConnectionState: "verified", Source: "configured_connection"}
	for _, last := range []*SelectionTest{nil, {ObservedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.FixedZone("test", 3600)), DCHostName: "dc.example.test", Code: "ok"}} {
		c.LastTest = last
		selectionUTC(&c)
		raw, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		var got map[string]any
		if e = json.Unmarshal(raw, &got); e != nil {
			t.Fatal(e)
		}
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := strings.Fields("connectionState credentialConfigured credentialRevision dcHostName domain domainId lastTest ldapAddr mode port revision source")
		if !reflect.DeepEqual(keys, want) || got["revision"] != "9007199254740993" || got["ldapAddr"] != "" {
			t.Fatal("unsafe or lossy choice projection", string(raw))
		}
		if last == nil {
			if got["lastTest"] != nil {
				t.Fatal("missing observation should be explicit null")
			}
		} else {
			d := got["lastTest"].(map[string]any)
			if len(d) != 3 || d["observedAt"] != "2026-10-07T11:00:00Z" || d["dcHostName"] != c.DCHostName || d["code"] != "ok" {
				t.Fatal("unsafe diagnostic projection", d)
			}
		}
	}
}

func selectionError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var problem *Error
	if !errors.As(err, &problem) || problem.Status != status || problem.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}
