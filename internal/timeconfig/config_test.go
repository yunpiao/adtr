package timeconfig

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func ptr[T any](value T) *T { return &value }
func testPolicy() Policy    { return Policy{MaxServers: 32, MaxServerBytes: 253, Location: time.UTC} }
func requireError(t *testing.T, input Input, policy Policy, field, code string) {
	t.Helper()
	got, errors := Parse(input, policy)
	if !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("nonzero result on error: %#v", got)
	}
	for _, e := range errors {
		if e == (FieldError{field, code}) {
			return
		}
	}
	t.Fatalf("missing %s/%s: %#v", field, code, errors)
}

func TestPresenceAndModeConflicts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       Input
		field, code string
	}{
		{"missing_mode", Input{}, "syncType", "required"},
		{"negative_mode", Input{SyncType: ptr(-1)}, "syncType", "invalid_mode"},
		{"unknown_mode", Input{SyncType: ptr(2)}, "syncType", "invalid_mode"},
		{"automatic_absent_servers", Input{SyncType: ptr(Automatic)}, "ntpServers", "required"},
		{"automatic_empty_servers", Input{SyncType: ptr(Automatic), NTPServers: []string{}}, "ntpServers", "required"},
		{"automatic_empty_present_date", Input{SyncType: ptr(Automatic), Date: ptr(""), NTPServers: []string{"ntp.example"}}, "date", "mode_conflict"},
		{"manual_absent_date", Input{SyncType: ptr(Manual)}, "date", "required"},
		{"manual_empty_date", Input{SyncType: ptr(Manual), Date: ptr("")}, "date", "invalid_date"},
		{"manual_empty_present_servers", Input{SyncType: ptr(Manual), Date: ptr("2024-01-01 00:00:00"), NTPServers: []string{}}, "ntpServers", "mode_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) { requireError(t, tc.input, testPolicy(), tc.field, tc.code) })
	}
}

func TestPolicyBoundaries(t *testing.T) {
	input := Input{SyncType: ptr(Automatic), NTPServers: []string{"a.b"}}
	for _, n := range []int{-1, 0, 33} {
		p := testPolicy()
		p.MaxServers = n
		requireError(t, input, p, "policy.maxServers", "invalid_policy")
	}
	for _, n := range []int{-1, 0, 254} {
		p := testPolicy()
		p.MaxServerBytes = n
		requireError(t, input, p, "policy.maxServerBytes", "invalid_policy")
	}
	p := testPolicy()
	p.MaxServers = 1
	p.MaxServerBytes = 3
	if _, e := Parse(input, p); len(e) != 0 {
		t.Fatal(e)
	}
	input.NTPServers = append(input.NTPServers, "c.d")
	requireError(t, input, p, "ntpServers", "too_many")
	input.NTPServers = []string{"aa.b"}
	requireError(t, input, p, "ntpServers[0]", "too_long")
	input.NTPServers = []string{"a.b."}
	requireError(t, input, p, "ntpServers[0]", "too_long")
	p.MaxServerBytes = 1
	input.NTPServers = []string{"a"}
	requireError(t, input, p, "ntpServers[0]", "invalid_host")
}

func TestStrictGregorianDate(t *testing.T) {
	for _, raw := range []string{"0001-01-01 00:00:00", "9999-12-31 23:59:59", "2000-02-29 12:30:59", "2024-02-29 12:30:59"} {
		t.Run(raw, func(t *testing.T) {
			got, e := Parse(Input{SyncType: ptr(Manual), Date: ptr(raw)}, testPolicy())
			if len(e) != 0 || got.ManualDate != raw || got.ManualTime.Format(dateLayout) != raw || got.Timezone != "UTC" {
				t.Fatalf("%#v %#v", got, e)
			}
		})
	}
	for _, raw := range []string{"0000-01-01 00:00:00", "10000-01-01 00:00:00", "1900-02-29 00:00:00", "2023-02-29 00:00:00", "2024-13-01 00:00:00", "2024-04-31 00:00:00", "2024-00-01 00:00:00", "2024-01-00 00:00:00", "2024-01-01 24:00:00", "2024-01-01 00:60:00", "2024-01-01 00:00:60", "2024-1-01 00:00:00", "2024-01-01T00:00:00", "2024-01-01 00:00:00Z", "2024-01-01 00:00:00.1", " 2024-01-01 00:00:00", "２０２４-01-01 00:00:00", "2024-01-01\x0000:00:00"} {
		t.Run(raw, func(t *testing.T) {
			requireError(t, Input{SyncType: ptr(Manual), Date: ptr(raw)}, testPolicy(), "date", "invalid_date")
		})
	}
}

func TestExplicitTimezone(t *testing.T) {
	input := Input{SyncType: ptr(Manual), Date: ptr("2024-01-01 00:00:00")}
	for _, loc := range []*time.Location{nil, time.Local, time.FixedZone("Local", 3600)} {
		p := testPolicy()
		p.Location = loc
		requireError(t, input, p, "policy.location", "explicit_timezone_required")
	}
	p := testPolicy()
	p.Location = time.FixedZone("explicit +0530", 19800)
	got, e := Parse(input, p)
	if len(e) != 0 || got.ManualTime.Format(time.RFC3339) != "2023-12-31T18:30:00Z" {
		t.Fatalf("%#v %#v", got, e)
	}
}

func TestFixedZoneOffsetsBeyondTZif(t *testing.T) {
	for _, offset := range []int{1 << 32, -(1 << 32), 1 << 40, -(1 << 40)} {
		p := testPolicy()
		p.Location = time.FixedZone("explicit large offset", offset)
		raw := "2024-01-01 00:00:00"
		got, e := Parse(Input{SyncType: ptr(Manual), Date: ptr(raw)}, p)
		if len(e) != 0 || got.ManualTime.In(p.Location).Format(dateLayout) != raw {
			t.Fatalf("offset %d: %#v %#v", offset, got, e)
		}
	}
}

func TestServerCountMaximum(t *testing.T) {
	servers := make([]string, 32)
	for i := range servers {
		servers[i] = fmt.Sprintf("ntp%d.example", i)
	}
	if _, e := Parse(Input{SyncType: ptr(Automatic), NTPServers: servers}, testPolicy()); len(e) != 0 {
		t.Fatal(e)
	}
	servers = append(servers, "ntp32.example")
	requireError(t, Input{SyncType: ptr(Automatic), NTPServers: servers}, testPolicy(), "ntpServers", "too_many")
}

func TestDSTGapFoldAndUnusualTransitions(t *testing.T) {
	for _, tc := range []struct{ zone, raw, code string }{
		{"America/New_York", "2024-03-10 02:30:00", "nonexistent_time"},
		{"America/New_York", "2024-11-03 01:30:00", "ambiguous_time"},
		{"Australia/Lord_Howe", "2024-10-06 02:15:00", "nonexistent_time"},
		{"Australia/Lord_Howe", "2024-04-07 01:45:00", "ambiguous_time"},
		{"Pacific/Apia", "2011-12-30 12:00:00", "nonexistent_time"},
		{"America/New_York", "2024-03-10 03:00:00", ""},
		{"America/New_York", "2024-11-03 02:00:00", ""},
		{"Australia/Lord_Howe", "2024-10-06 02:30:00", ""},
		{"Australia/Lord_Howe", "2024-04-07 02:00:00", ""},
		{"Pacific/Apia", "2011-12-31 00:00:00", ""},
		{"America/New_York", "2040-12-31 12:00:00", ""},
		{"Australia/Lord_Howe", "2040-12-31 12:00:00", ""},
		{"America/New_York", "0001-01-01 00:00:00", ""},
		{"America/New_York", "9999-12-31 23:59:59", ""},
	} {
		t.Run(tc.zone+"/"+tc.raw, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			p := testPolicy()
			p.Location = loc
			in := Input{SyncType: ptr(Manual), Date: ptr(tc.raw)}
			if tc.code != "" {
				requireError(t, in, p, "date", tc.code)
				return
			}
			got, e := Parse(in, p)
			if len(e) != 0 || got.ManualTime.In(loc).Format(dateLayout) != tc.raw {
				t.Fatalf("%#v %#v", got, e)
			}
		})
	}
}

func TestNTPNormalizationAndDeduplication(t *testing.T) {
	input := Input{SyncType: ptr(Automatic), NTPServers: []string{"NTP.Example.", "192.0.2.1", "2001:0db8:0:0::1", "::ffff:192.0.2.2", "XN--BCHER-KVA.Example"}}
	got, e := Parse(input, testPolicy())
	want := []string{"ntp.example", "192.0.2.1", "2001:db8::1", "192.0.2.2", "xn--bcher-kva.example"}
	if len(e) != 0 || !reflect.DeepEqual(got.NTPServers, want) {
		t.Fatalf("%#v %#v", got, e)
	}
	for _, pair := range [][]string{{"ntp.example", "NTP.Example."}, {"192.0.2.1", "::ffff:192.0.2.1"}, {"2001:db8::1", "2001:0db8:0:0:0:0:0:1"}} {
		requireError(t, Input{SyncType: ptr(Automatic), NTPServers: pair}, testPolicy(), "ntpServers[1]", "duplicate")
	}
}

func TestNTPInvalidHosts(t *testing.T) {
	for _, host := range []string{"", "localhost", "a..b", "ntp.example..", ".ntp.example", "-ntp.example", "ntp-.example", "ntp.ex_ample", "https://ntp.example", "ntp.example/path", "ntp.example:123", "192.0.2.1:123", "[2001:db8::1]", "[2001:db8::1]:123", "fe80::1%eth0", "*.example", " ntp.example", "ntp.example ", "ntp.\nexample", "ntp.\x00example", "é.example", "127.1", "2130706433", "999.1.1.1", "192.000.2.1", "123.456.", strings.Repeat("a", 64) + ".example"} {
		t.Run(host, func(t *testing.T) {
			requireError(t, Input{SyncType: ptr(Automatic), NTPServers: []string{host}}, testPolicy(), "ntpServers[0]", "invalid_host")
		})
	}
	max := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	if len(max) != 253 {
		t.Fatal("bad fixture")
	}
	if _, e := Parse(Input{SyncType: ptr(Automatic), NTPServers: []string{max}}, testPolicy()); len(e) != 0 {
		t.Fatal(e)
	}
	requireError(t, Input{SyncType: ptr(Automatic), NTPServers: []string{max + "."}}, testPolicy(), "ntpServers[0]", "too_long")
}

func TestDeterminismErrorOrderingAndNoMutation(t *testing.T) {
	input := Input{SyncType: ptr(Automatic), Date: ptr(""), NTPServers: []string{"", "ntp.example", "NTP.example.", "a:123"}}
	want := []FieldError{{"date", "mode_conflict"}, {"ntpServers[0]", "invalid_host"}, {"ntpServers[2]", "duplicate"}, {"ntpServers[3]", "invalid_host"}}
	for i := 0; i < 5; i++ {
		got, e := Parse(input, testPolicy())
		if !reflect.DeepEqual(got, Config{}) || !reflect.DeepEqual(e, want) {
			t.Fatalf("%#v %#v", got, e)
		}
	}
	input = Input{SyncType: ptr(Automatic), NTPServers: []string{"NTP.Example."}}
	policy := testPolicy()
	before := policy
	got, e := Parse(input, policy)
	if len(e) != 0 {
		t.Fatal(e)
	}
	got.NTPServers[0] = "changed.example"
	if input.NTPServers[0] != "NTP.Example." || policy != before || *input.SyncType != Automatic {
		t.Fatal("mutated input/policy")
	}
	input.NTPServers[0] = "other.example"
	if got.NTPServers[0] != "changed.example" {
		t.Fatal("aliased output")
	}
}

func FuzzParseHostDeterminism(f *testing.F) {
	for _, v := range []string{"ntp.example", "::ffff:192.0.2.1", "", "a..b", "xn--x.example"} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, value string) {
		input := Input{SyncType: ptr(Automatic), NTPServers: []string{value}}
		a, e := Parse(input, testPolicy())
		b, d := Parse(input, testPolicy())
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(e, d) {
			t.Fatal("nondeterministic")
		}
		if len(e) > 0 && !reflect.DeepEqual(a, Config{}) {
			t.Fatal("partial result")
		}
		if input.NTPServers[0] != value {
			t.Fatal("mutated input")
		}
	})
}
