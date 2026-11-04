package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestScenario(t *testing.T) {
	input := `{"operations":[{"op":"create","id":"synthetic-1","tplName":"合成模板","content":{"ipList":["192.0.2.1"]}},{"op":"list","search":"合成"},{"op":"delete","id":"synthetic-1"},{"op":"list"},{"op":"restore","id":"synthetic-1"},{"op":"list"}]}`
	var out bytes.Buffer
	if e := run(strings.NewReader(input), &out); e != nil {
		t.Fatal(e)
	}
	var r result
	if e := json.Unmarshal(out.Bytes(), &r); e != nil {
		t.Fatal(e)
	}
	if !r.Simulation || len(r.Lists) != 3 || len(r.Lists[0].Customize) != 1 || len(r.Lists[1].Customize) != 0 || len(r.Lists[2].Customize) != 1 {
		t.Fatal(out.String())
	}
	if r.Lists[2].Customize[0].Params.IPList[0] != "192.0.2.1" {
		t.Fatal("not restored")
	}
	// Each invocation starts empty, without filesystem state.
	out.Reset()
	if e := run(strings.NewReader(`{"operations":[{"op":"list"}]}`), &out); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), "synthetic-1") {
		t.Fatal("persisted")
	}
}
func TestFailureNoPartialOutput(t *testing.T) {
	for _, input := range []string{
		`{}`, `{"operations":[]}`, `{"operations":null}`, `{"operations":[{"op":"execute"}]}`,
		`{"operations":[{"op":"create","id":"x","tplName":"x"}]}`,
		`{"operations":[{"op":"list","id":"x"}]}`,
		`{"operations":[{"op":"delete","id":"missing"}]}`,
		`{"operations":[{"op":"create","id":"x","tplName":"x","content":{"ipList":["bad"]}}]}`,
		`{"operations":[{"op":"list"},{"op":"create","id":"x","tplName":"x","content":{}},{"op":"create","id":"x","tplName":"different","content":{}}]}`,
		`{"operations":[{"op":"list","op":"delete"}]}`,
		`{"operations":[{"op":"list","search":"\ud800"}]}`,
		`{"operations":[{"op":"list","search":null}]}`,
		`{"operations":[{"op":"restore","id":"x","content":{}}]}`,
		`{"operations":[{"op":"list","unknown":1}]}`,
	} {
		var out bytes.Buffer
		if e := run(strings.NewReader(input), &out); e == nil {
			t.Fatal(input)
		}
		if out.Len() != 0 {
			t.Fatal("partial output")
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failure") }
func TestIOFailures(t *testing.T) {
	var out bytes.Buffer
	if run(failingReader{}, &out) == nil || out.Len() != 0 {
		t.Fatal("read failure lost")
	}
	if run(strings.NewReader(`{"operations":[{"op":"list"}]}`), failingWriter{}) == nil {
		t.Fatal("write failure lost")
	}
}
func TestBudgets(t *testing.T) {
	for _, raw := range []string{strings.Repeat(" ", (1<<20)+1), `{"operations":[` + strings.Repeat(`{"op":"list"},`, 100) + `{"op":"list"}]}`} {
		var out bytes.Buffer
		if run(strings.NewReader(raw), &out) == nil || out.Len() != 0 {
			t.Fatal("budget accepted")
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestShortWrite(t *testing.T) {
	if run(strings.NewReader(`{"operations":[{"op":"list"}]}`), shortWriter{}) == nil {
		t.Fatal("short write accepted")
	}
}

func TestInvalidTimeNoPartialOutput(t *testing.T) {
	var out bytes.Buffer
	raw := `{"operations":[{"op":"list"},{"op":"create","id":"x","tplName":"x","content":{"time":["2026-01-01T0:00:00Z","2026-01-02T00:00:00Z"]}}]}`
	if run(strings.NewReader(raw), &out) == nil || out.Len() != 0 {
		t.Fatal("invalid time emitted")
	}
}
