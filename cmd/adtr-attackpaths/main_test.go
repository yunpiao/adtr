package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/attackpaths"
)

func example(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../examples/attackpaths/request.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestExecuteValidExample(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := execute(nil, bytes.NewReader(example(t)), &out, &diagnostic); code != 0 || diagnostic.Len() != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, &diagnostic)
	}
	var got attackpaths.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Simulated || !got.Found || got.ObservationID != "synthetic-observation-1" || len(got.Nodes) != 3 || len(got.Edges) != 2 || got.Edges[0].ID != "event-1" || got.Edges[1].ID != "event-2" {
		t.Fatalf("result: %+v", got)
	}
	if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		t.Fatal("missing newline")
	}
}
func TestExecuteFailuresHaveNoResult(t *testing.T) {
	good := string(example(t))
	for name, input := range map[string]string{
		"invalid":   "{private-input-value}",
		"scope":     strings.Replace(good, `"domainId": "synthetic-domain"`, `"domainId": "private-input-value"`, 1),
		"limit":     strings.Replace(good, `"maxDepth": 2`, `"maxDepth": 1`, 1),
		"missing":   strings.Replace(good, `"to": "server"`, `"to": "private-input-value"`, 2),
		"trailing":  good + `{"secret":"private-input-value"}`,
		"oversized": strings.Repeat(" ", attackpaths.MaxJSONBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			if code := execute(nil, strings.NewReader(input), &out, &diagnostic); code != 1 || out.Len() != 0 || diagnostic.Len() == 0 || strings.Contains(diagnostic.String(), "private-input-value") {
				t.Fatalf("code=%d out=%q diagnostic=%q", code, &out, &diagnostic)
			}
		})
	}
}
func TestExecuteNoPathIsSuccessfulEmptyResult(t *testing.T) {
	input := strings.Replace(string(example(t)), `"ruleIds": ["synthetic-rule"]`, `"ruleIds": ["unmatched"]`, 1)
	var out, diagnostic bytes.Buffer
	if code := execute(nil, strings.NewReader(input), &out, &diagnostic); code != 0 {
		t.Fatalf("code=%d %s", code, &diagnostic)
	}
	var got attackpaths.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Found || got.Nodes == nil || got.Edges == nil || len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("%+v", got)
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("private-read-error") }

type failedWriter struct{ short bool }

func (f failedWriter) Write(p []byte) (int, error) {
	if f.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("private-write-error")
}

type countingReader struct{ n int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.n += len(p)
	return len(p), nil
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("arguments must fail before reading") }

func TestExecuteInputOutputFailuresAreSanitized(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := execute(nil, failedReader{}, &out, &diagnostic); code != 1 || out.Len() != 0 || strings.Contains(diagnostic.String(), "private-read-error") || !strings.Contains(diagnostic.String(), errRead.Error()) {
		t.Fatalf("code=%d %s", code, &diagnostic)
	}
	for _, writer := range []io.Writer{failedWriter{}, failedWriter{short: true}} {
		diagnostic.Reset()
		if code := execute(nil, bytes.NewReader(example(t)), writer, &diagnostic); code != 1 || strings.Contains(diagnostic.String(), "private-write-error") || !strings.Contains(diagnostic.String(), errWrite.Error()) {
			t.Fatalf("code=%d %s", code, &diagnostic)
		}
	}
}
func TestRunReadBound(t *testing.T) {
	reader := &countingReader{}
	var out bytes.Buffer
	if err := run(reader, &out); !errors.Is(err, attackpaths.ErrLimit) || reader.n != attackpaths.MaxJSONBytes+1 || out.Len() != 0 {
		t.Fatalf("err=%v read=%d out=%d", err, reader.n, out.Len())
	}
}
func TestExecuteArgumentsRejectedWithoutRead(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := execute([]string{"private-file-path"}, panicReader{}, &out, &diagnostic); code != 2 || out.Len() != 0 || strings.Contains(diagnostic.String(), "private-file-path") {
		t.Fatalf("code=%d %s", code, &diagnostic)
	}
}
