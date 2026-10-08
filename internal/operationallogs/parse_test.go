package operationallogs

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestParseFilterCanonicalWindowAndModules(t *testing.T) {
	f, err := ParseFilter(url.Values{"startTm": {"2026-10-07T10:00:00.000001+08:00"}, "endTm": {"2026-10-07T11:00:00+08:00"}, "systemType": {"worker", "api"}, "pageSize": {"-1"}})
	if err != nil || f.StartTm != "2026-10-07T02:00:00.000001Z" || f.EndTm != "2026-10-07T03:00:00Z" || !reflect.DeepEqual(f.SystemType, []string{"api", "worker"}) || f.PageIdx != 1 || f.PageSize != -1 {
		t.Fatalf("unexpected normalized filter: %+v %v", f, err)
	}
	f, err = ParseFilter(nil)
	if err != nil || f.PageIdx != 1 || f.PageSize != 20 || f.StartTm != "" || f.EndTm != "" || !reflect.DeepEqual(f.SystemType, []string{"api", "worker"}) {
		t.Fatalf("default bounds must remain unresolved until database statement: %+v %v", f, err)
	}
}
func TestParseFilterRejectsAmbiguityAndUnimplementedSources(t *testing.T) {
	for _, raw := range []string{
		"pageIdx=0", "pageIdx=1000001", "pageIdx=01", "pageIdx=-1", "pageIdx=1&pageIdx=1", "pageSize=0", "pageSize=101", "pageSize=-2", "pageSize=-1&pageIdx=2", "pageSize=+1",
		"systemType=api&systemType=api", "systemType=api&systemType=db_syncer", "systemType=API", "systemType=", "userInfo=1", "tenant=default", "src=api", "fileName=log",
		"startTm=2026-10-07T00:00:00Z", "endTm=2026-10-07T00:00:00Z", "startTm=&endTm=", "startTm=2026-10-07T00:00:00&endTm=2026-10-08T00:00:00",
		"startTm=2026-10-07T00:00:00.0000001Z&endTm=2026-10-08T00:00:00Z", "startTm=2026-10-07T00:00:00Z&endTm=2026-10-08T00:00:00.000001Z",
		"startTm=2026-10-07T00:00:00Z&endTm=2026-10-07T00:00:00Z", "startTm=2026-10-08T00:00:00Z&endTm=2026-10-07T00:00:00Z",
		"startTm=2026-10-07T00:00:00%2B24:00&endTm=2026-10-08T00:00:00%2B24:00", "startTm=2026-10-07T00:00:00,1Z&endTm=2026-10-08T00:00:00Z",
	} {
		t.Run(raw, func(t *testing.T) {
			q, err := url.ParseQuery(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ParseFilter(q); err == nil {
				t.Fatal("malformed filter accepted")
			}
		})
	}
	for _, q := range []url.Values{{"systemType": nil}, {"pageIdx": nil}, {"pageSize": {""}}} {
		if _, err := ParseFilter(q); err == nil {
			t.Fatal("empty field accepted")
		}
	}
}
func TestNormalizeSelectionRequiresExplicitWindowAndIndependentSlice(t *testing.T) {
	modules := []string{"worker", "api"}
	s, err := NormalizeSelection("2026-10-07T00:00:00Z", "2026-10-08T00:00:00Z", modules)
	if err != nil {
		t.Fatal(err)
	}
	modules[0] = "private"
	if s.SystemType[1] != "worker" {
		t.Fatal("caller changed normalized intent")
	}
	for _, args := range []Selection{{StartTm: s.StartTm, EndTm: s.EndTm}, {SystemType: []string{"api"}}, {StartTm: s.StartTm, EndTm: s.EndTm, SystemType: []string{"worker", "worker"}}} {
		if _, err := NormalizeSelection(args.StartTm, args.EndTm, args.SystemType); err == nil {
			t.Fatal("invalid bundle selection accepted")
		}
	}
}

func TestSelectionAcceptsHistoricRFC3339AndRejectsUTCYearOverflow(t *testing.T) {
	for _, bounds := range [][2]string{
		{"0001-01-01T00:00:00Z", "0001-01-02T00:00:00Z"},
		{"1969-12-31T12:00:00Z", "1970-01-01T00:00:00Z"},
		{"9999-12-31T22:00:00Z", "9999-12-31T23:59:59.999999Z"},
	} {
		if _, err := NormalizeSelection(bounds[0], bounds[1], []string{"api"}); err != nil {
			t.Fatalf("valid historical/future query rejected: %v %v", bounds, err)
		}
	}
	for _, bounds := range [][2]string{
		{"0000-01-01T00:00:00Z", "0000-01-02T00:00:00Z"},
		{"0001-01-01T00:00:00+01:00", "0001-01-02T00:00:00+01:00"},
		{"9999-12-31T23:00:00-01:00", "9999-12-31T23:59:59-01:00"},
	} {
		if _, err := NormalizeSelection(bounds[0], bounds[1], []string{"api"}); err == nil {
			t.Fatalf("unsupported normalized year accepted: %v", bounds)
		}
	}
}
func TestEventWhitelistAndExactPrivateDataFreeDTO(t *testing.T) {
	valid := []struct {
		m Module
		c EventCode
		o Outcome
		r Reason
	}{{API, ServiceStartRequested, Attempted, NoReason}, {API, ServiceStopped, Completed, NoReason}, {API, ServiceFailed, Failed, ServeFailed}, {Worker, ServiceFailed, Failed, WorkerFailed}, {Worker, QueueCycle, Completed, NoReason}, {Worker, RecoveryCycle, Failed, CycleFailed}, {Worker, SchedulerCycle, Completed, NoReason}, {Worker, QueueProgress, Progress, NoReason}}
	for _, v := range valid {
		if _, ok := eventSeverity(v.m, v.c, v.o, v.r); !ok {
			t.Fatalf("valid producer event denied %+v", v)
		}
	}
	invalid := []struct {
		m Module
		c EventCode
		o Outcome
		r Reason
	}{{API, QueueCycle, Completed, NoReason}, {Worker, ServiceFailed, Failed, ServeFailed}, {API, ServiceFailed, Failed, WorkerFailed}, {Worker, QueueProgress, Completed, NoReason}, {Worker, QueueCycle, Failed, NoReason}, {API, ServiceStopped, Completed, Reason("secret")}, {Module("health"), ServiceStopped, Completed, NoReason}, {API, EventCode("request/path"), Completed, NoReason}}
	for _, v := range invalid {
		if _, ok := eventSeverity(v.m, v.c, v.o, v.r); ok {
			t.Fatalf("unsafe event accepted %+v", v)
		}
	}
	e := testEvent(t)
	e.RecordedAt = e.ObservedAt
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"schemaVersion", "eventId", "processId", "module", "code", "outcome", "severity", "reason", "observedAt", "recordedAt"}
	if len(fields) != len(want) {
		t.Fatalf("unexpected public event fields: %s", data)
	}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing event field", key)
		}
	}
	if validateEvent(e, true) != nil {
		t.Fatal("valid event rejected")
	}
	e.ObservedAt = e.ObservedAt.Add(time.Nanosecond)
	if validateEvent(e, true) == nil {
		t.Fatal("lossy event time accepted")
	}
}
