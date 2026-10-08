package tasks

import (
	"encoding/json"
	"testing"
	"time"
)

type taskTimeRow func(...any) error

func (r taskTimeRow) Scan(dest ...any) error { return r(dest...) }

func TestScanTaskTerminalTimeUsesUTCInPublicJSON(t *testing.T) {
	local := time.Date(2026, 10, 7, 17, 30, 12, 123456000, time.FixedZone("synthetic-offset", 8*60*60))
	for _, tc := range []struct {
		name string
		at   *time.Time
		want string
	}{
		{"known", &local, `"2026-10-07T09:30:12.123456Z"`},
		{"unknown", nil, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, err := scanTask(taskTimeRow(func(dest ...any) error {
				if len(dest) != 26 {
					t.Fatalf("task scan destination count=%d, want 26", len(dest))
				}
				*dest[4].(*State) = Succeeded
				*dest[25].(**time.Time) = tc.at
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if tc.at != nil && (task.TerminalAt == nil || !task.TerminalAt.Equal(*tc.at) || task.TerminalAt.Location() != time.UTC) {
				t.Fatalf("terminal time=%v, want same instant %v in UTC", task.TerminalAt, tc.at)
			}
			encoded, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}
			var public map[string]json.RawMessage
			if err = json.Unmarshal(encoded, &public); err != nil {
				t.Fatal(err)
			}
			if got := string(public["terminalAt"]); got != tc.want {
				t.Fatalf("public terminalAt=%s, want %s", got, tc.want)
			}
		})
	}
}
