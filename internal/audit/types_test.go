package audit

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFilterBoundariesAndLiteralInputs(t *testing.T) {
	f, e := ParseFilter(url.Values{})
	if e != nil || f.PageIdx != 1 || f.PageSize != 20 || f.CreateSort != -1 || f.Visibility != "visible" {
		t.Fatal(f, e)
	}
	for _, q := range []string{"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageSize=0", "pageSize=101", "pageSize=-1&pageIdx=2", "createSort=0", "createSort=2", "pageIdx=1&pageIdx=2", "unknown=x", "keyword=", "keyword=%00", "keyword=" + strings.Repeat("界", 51), "startTm=2026-01-01", "startTm=2026-01-01T00:00:00Z&endTm=2026-01-01T00:00:00Z", "startTm=2026-01-02T00:00:00Z&endTm=2026-01-01T00:00:00Z", "filterEvent=x&filterEvent=x", "filterEvent=%0A", "logTypeList=0", "logTypeList=10", "logTypeList=8&logTypeList=8", "visibility=deleted"} {
		t.Run(q, func(t *testing.T) {
			v, _ := url.ParseQuery(q)
			if _, e := ParseFilter(v); e == nil {
				t.Fatal("accepted", q)
			}
		})
	}
	q := url.Values{"keyword": {"%_\\"}, "filterEvent": {"含义未明", "future.operation"}, "logTypeList": {"8", "9"}, "startTm": {"2026-10-07T09:00:00+02:00"}, "endTm": {"2026-10-07T10:00:00+02:00"}, "pageSize": {"-1"}}
	f, e = ParseFilter(q)
	if e != nil || f.Keyword != "%_\\" || f.StartTm != "2026-10-07T07:00:00Z" {
		t.Fatal(f, e)
	}
	sql, args := scopedQuery(Principal{"tenant", 1, "role"}, f)
	if strings.Contains(sql, "%_\\") || !strings.Contains(sql, "strpos(") || len(args) < 5 {
		t.Fatal("keyword must be a literal bound parameter", sql, args)
	}
}
func TestExportColumnsStrictAndInRequestedOrder(t *testing.T) {
	for _, v := range [][]string{nil, {}, {"event", "event"}, {"unknown"}, {"userId", "loginUser", "sourceIp", "logTypeName", "event", "eventArgs", "eventResult", "CreateTm", "ninth"}} {
		if ValidateColumns(v) == nil {
			t.Fatal("accepted", v)
		}
	}
	name := "=1+1"
	uid := int64(99)
	row := Row{UserID: &uid, LoginUser: &name, Event: "known", CreateTm: time.Date(2026, 10, 7, 7, 0, 0, 0, time.FixedZone("other", 7200))}
	cells := row.Cells([]string{"event", "loginUser", "sourceIp", "userId", "CreateTm"})
	want := []string{"known", "=1+1", "", "99", "2026-10-07T05:00:00Z"}
	for i := range want {
		if cells[i] != want[i] {
			t.Fatal(cells)
		}
	}
	raw, e := ValidateExportPayload(json.RawMessage(`{"selectColumn":["event"],"filterEvent":[],"logTypeList":[]}`))
	if e != nil || !strings.Contains(string(raw), `"createSort":-1`) {
		t.Fatal(string(raw), e)
	}
	for _, bad := range []string{`null`, `{"selectColumn":["event"],"selectColumn":["userId"]}`, `{"selectColumn":[]}`, `{"selectColumn":["event"],"password":"secret"}`, `{"selectColumn":["event"]} {}`} {
		if _, e := ValidateExportPayload(json.RawMessage(bad)); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}
func TestStableTargetsAndVisibilityReason(t *testing.T) {
	for _, id := range []string{"auth.1", "resource.9", "task.42", "audit.9223372036854775807", "credential_use.42"} {
		if _, _, e := ParseID(id); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"1", "auth.0", "auth.01", "task.-1", "AUTH.1", "task.9223372036854775808", "audit.1;DROP"} {
		if _, _, e := ParseID(id); e == nil {
			t.Fatal("accepted", id)
		}
	}
	for _, x := range []struct {
		ids    []string
		reason string
	}{{nil, "reason"}, {[]string{"auth.1", "auth.1"}, "reason"}, {[]string{"auth.1"}, "  "}, {[]string{"auth.1"}, "a\nreason"}} {
		if ValidateTargets(x.ids, x.reason) == nil {
			t.Fatal("accepted", x)
		}
	}
	err := ValidateTargets([]string{"auth.1"}, strings.Repeat("界", 501))
	var ae *Error
	if !errors.As(err, &ae) || ae.Status != 400 {
		t.Fatal(err)
	}
}
func TestKindPinsFencedArtifactCancellation(t *testing.T) {
	k := Kind()
	if k.Name != ExportKind || !k.Platform || !k.CancelDiscardsResult || !k.OwnerScoped || !k.ReplaySafe || k.MaxAttempts != 3 {
		t.Fatal(k)
	}
}
