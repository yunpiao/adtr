package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dc "github.com/yunpiao/adtr/internal/directorychanges"
)

func directoryTestRequest(mode string) map[string]any {
	return map[string]any{"contract": directoryCLIContract, "synthetic": true, "mode": mode}
}
func directoryTestRun(t *testing.T, in any, success bool) []byte {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return directoryTestRaw(t, data, success)
}
func directoryTestRaw(t *testing.T, data []byte, success bool) []byte {
	t.Helper()
	var out, stderr bytes.Buffer
	code := run([]string{"directory-changes"}, bytes.NewReader(data), &out, &stderr)
	if success {
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stderr=%s input=%s", code, stderr.String(), data)
		}
	} else if code != 2 || out.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("failure leaked output: %d %s %s", code, out.String(), stderr.String())
	}
	return out.Bytes()
}
func directoryFixture(t *testing.T) (dc.Baseline, []dc.Batch) {
	t.Helper()
	baseline := dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{{ID: "keep", Kind: dc.Group, Version: 1, Fields: dc.Fields{Name: "Keep", Enabled: true}}}}
	state, err := dc.New(baseline)
	if err != nil {
		t.Fatal(err)
	}
	var batches []dc.Batch
	for i, op := range []dc.Operation{dc.Create, dc.Update, dc.Tombstone} {
		fields := dc.Fields{Name: "Created", Enabled: true}
		if i == 1 {
			fields = dc.Fields{Name: "Updated", Enabled: false}
		}
		if i == 2 {
			fields = dc.Fields{}
		}
		b := dc.Batch{SourceEpoch: "epoch-1", Sequence: uint64(i + 1), PreviousHash: state.Snapshot().Cursor.Hash, Complete: true, Changes: []dc.Change{{ID: "user-1", Operation: op, Kind: dc.User, ExpectedVersion: uint64(i), Fields: fields}}}
		raw, _ := json.Marshal(b)
		var obj map[string]any
		_ = json.Unmarshal(raw, &obj)
		delete(obj, "BatchHash")
		req := directoryTestRequest("hash")
		req["batch"] = obj
		output := directoryTestRun(t, req, true)
		var env struct {
			Result struct {
				Batch          dc.Batch
				StateValidated bool
			}
		}
		if err := json.Unmarshal(output, &env); err != nil {
			t.Fatal(err)
		}
		if env.Result.StateValidated || len(env.Result.Batch.BatchHash) != 64 {
			t.Fatal(string(output))
		}
		b = env.Result.Batch
		state, _, err = state.Apply(b)
		if err != nil {
			t.Fatal(err)
		}
		batches = append(batches, b)
	}
	return baseline, batches
}
func TestDirectoryChangesWorkflow(t *testing.T) {
	baseline, batches := directoryFixture(t)
	req := directoryTestRequest("replay")
	req["baseline"] = baseline
	req["batches"] = append(batches, batches[0])
	output := directoryTestRun(t, req, true)
	var env struct {
		Result struct {
			Snapshot dc.Snapshot
			Trace    []directoryTrace
		}
	}
	if err := json.Unmarshal(output, &env); err != nil {
		t.Fatal(err)
	}
	s := env.Result.Snapshot
	if len(s.Objects) != 2 || s.Objects[0].ID != "keep" || s.Objects[0].Deleted || s.Objects[1].Version != 3 || !s.Objects[1].Deleted || s.Objects[1].Fields.Name != "Updated" || s.Objects[1].Fields.Enabled || len(s.Records) != 3 {
		t.Fatal(string(output))
	}
	if s.Records[0].Before != nil || s.Records[1].Before.Fields.Name != "Created" || s.Records[2].Before.Fields.Name != "Updated" || s.Records[2].After.Fields.Name != "Updated" {
		t.Fatal(string(output))
	}
	if len(env.Result.Trace) != 4 || env.Result.Trace[3].Status != "replayed" || env.Result.Trace[3].NewRecords != 0 || env.Result.Trace[3].Cursor.Sequence != 3 {
		t.Fatal(string(output))
	}
	if !bytes.Equal(output, directoryTestRun(t, req, true)) {
		t.Fatal("nondeterministic")
	}
	// Complete empty batches still advance; no changes is not evidence of replay.
	empty := dc.Batch{SourceEpoch: "epoch-1", Sequence: 4, PreviousHash: s.Cursor.Hash, Complete: true, Changes: []dc.Change{}}
	empty.BatchHash, _ = dc.HashBatch(empty)
	req["batches"] = append(batches, empty)
	if !bytes.Contains(directoryTestRun(t, req, true), []byte(`"status":"applied","newRecords":0`)) {
		t.Fatal("empty batch misclassified")
	}
	// Bound duplicate invocations independently from accepted batch receipts.
	repeats := make([]dc.Batch, maxDirectoryInvocations)
	for i := range repeats {
		repeats[i] = batches[0]
	}
	req["batches"] = repeats
	directoryTestRun(t, req, true)
	req["batches"] = append(repeats, batches[0])
	directoryTestRun(t, req, false)
}
func TestDirectoryChangesStrictCodec(t *testing.T) {
	baseline, _ := directoryFixture(t)
	req := directoryTestRequest("replay")
	req["baseline"] = baseline
	req["batches"] = []dc.Batch{}
	raw, _ := json.Marshal(req)
	mutations := []struct{ old, new string }{
		{`"Name":"Keep"`, `"Name":null`}, {`"Name":"Keep",`, ``}, {`,"Enabled":true`, ``}, {`"Enabled":true`, `"Enabled":null`},
		{`,"Deleted":false`, ``}, {`"Deleted":false`, `"Deleted":null`}, {`"Objects":[`, `"Objects":null,"ignored":[`},
		{`"batches":[]`, `"batches":null`}, {`"batches":[]`, `"batches":[],"batch":{}`},
		{`"mode":"replay"`, `"mode":"hash"`}, {`"mode":"replay"`, `"mode":"replay","Mode":"replay"`},
		{`"Name":"Keep"`, `"name":"Keep"`}, {`"Name":"Keep"`, `"Name":"Keep","unknown":false`},
		{`"synthetic":true`, `"synthetic":false`}, {`"contract":"` + directoryCLIContract + `"`, `"contract":"v0"`},
	}
	for _, m := range mutations {
		t.Run(m.new, func(t *testing.T) {
			changed := strings.Replace(string(raw), m.old, m.new, 1)
			if changed == string(raw) {
				t.Fatalf("ineffective mutation %s", m.old)
			}
			directoryTestRaw(t, []byte(changed), false)
		})
	}
	for _, raw := range []string{`null`, `{}`, `[]`, `{"contract":null}`, string(raw) + ` {}`} {
		directoryTestRaw(t, []byte(raw), false)
	}
	// Explicit known zero values are legal, unlike omission/null.
	baseline.Objects[0].Fields = dc.Fields{}
	req["baseline"] = baseline
	directoryTestRun(t, req, true)
}
func TestDirectoryChangesRejectWholeInvocation(t *testing.T) {
	baseline, batches := directoryFixture(t)
	for _, kind := range []string{"conflict", "hash", "epoch", "sequence", "previous", "version", "incomplete", "deleted", "missing-fields", "null-fields", "missing-enabled", "hash-mode-extra"} {
		t.Run(kind, func(t *testing.T) {
			req := directoryTestRequest("replay")
			req["baseline"] = baseline
			bad := batches[0]
			bad.Changes = append([]dc.Change(nil), bad.Changes...)
			switch kind {
			case "conflict":
				bad.Changes[0].Fields.Name = "conflict"
			case "hash":
				bad.BatchHash = strings.Repeat("0", 64)
			case "epoch":
				bad.SourceEpoch = "another"
			case "sequence":
				bad.Sequence = 6
			case "previous":
				bad.Sequence = 4
				bad.PreviousHash = strings.Repeat("0", 64)
			case "version":
				bad.Sequence = 4
				bad.PreviousHash = batches[2].BatchHash
				bad.Changes[0].Operation = dc.Update
				bad.Changes[0].ExpectedVersion = 99
			case "incomplete":
				bad.Complete = false
			case "deleted":
				bad.Sequence = 4
				bad.PreviousHash = batches[2].BatchHash
			}
			if kind != "hash" {
				bad.BatchHash, _ = dc.HashBatch(bad)
			}
			req["batches"] = append(batches, bad)
			raw, _ := json.Marshal(req)
			switch kind {
			case "missing-fields":
				raw = []byte(strings.Replace(string(raw), `,"Fields":{"Name":"Created","Enabled":true}`, "", 1))
			case "null-fields":
				raw = []byte(strings.Replace(string(raw), `"Fields":{"Name":"Created","Enabled":true}`, `"Fields":null`, 1))
			case "missing-enabled":
				raw = []byte(strings.Replace(string(raw), `,"Enabled":true`, "", 1))
			case "hash-mode-extra":
				req = directoryTestRequest("hash")
				req["batch"] = batches[0]
				raw, _ = json.Marshal(req)
			}
			directoryTestRaw(t, raw, false)
		})
	}
	// Hash preparation deliberately accepts a structurally valid wrong previous cursor.
	req := directoryTestRequest("hash")
	req["batch"] = map[string]any{"SourceEpoch": "elsewhere", "Sequence": 900, "PreviousHash": strings.Repeat("0", 64), "Complete": true, "Changes": []any{}}
	directoryTestRun(t, req, true)
}

// Every supplied field in either mode is required, including scalar zero values.
func TestDirectoryChangesEveryFieldRequired(t *testing.T) {
	baseline, batches := directoryFixture(t)
	replay := directoryTestRequest("replay")
	replay["baseline"] = baseline
	replay["batches"] = batches
	hash := directoryTestRequest("hash")
	encoded, _ := json.Marshal(batches[0])
	var batch map[string]any
	_ = json.Unmarshal(encoded, &batch)
	delete(batch, "BatchHash")
	hash["batch"] = batch
	for _, request := range []map[string]any{replay, hash} {
		encoded, _ := json.Marshal(request)
		var root any
		_ = json.Unmarshal(encoded, &root)
		var visit func(any)
		visit = func(node any) {
			switch node := node.(type) {
			case map[string]any:
				for key, value := range node {
					delete(node, key)
					directoryTestRun(t, root, false)
					node[key] = nil
					directoryTestRun(t, root, false)
					node[key] = value
					visit(value)
				}
			case []any:
				for _, value := range node {
					visit(value)
				}
			}
		}
		visit(root)
	}
}
func TestDirectoryChangesBoundsAndTypes(t *testing.T) {
	baseline, batches := directoryFixture(t)
	req := directoryTestRequest("replay")
	req["baseline"] = baseline
	req["batches"] = batches
	raw, _ := json.Marshal(req)
	for _, m := range []struct{ old, new string }{
		{`"Version":1`, `"Version":1.0`}, {`"Version":1`, `"Version":-1`},
		{`"Sequence":1`, `"Sequence":18446744073709551616`},
		{`"Enabled":true`, `"Enabled":"true"`}, {`"Name":"Keep"`, `"Name":42`},
		{`"Name":"Keep"`, `"Name":"` + strings.Repeat("x", 257) + `"`},
		{`"Changes":[`, `"Changes":[null,`},
		{`"Objects":[`, `"Objects":[null,`},
		{`"batches":[`, `"batches":{},"invalid":[`},
		{`"mode":"replay"`, `"mode":"unknown"`}, {`"Complete":true`, `"Complete":false`},
		{`"Fields":{"Name":"","Enabled":false}`, `"Fields":{"Name":"ignored","Enabled":false}`},
	} {
		changed := strings.Replace(string(raw), m.old, m.new, 1)
		if changed == string(raw) {
			t.Fatal("ineffective mutation", m.old)
		}
		directoryTestRaw(t, []byte(changed), false)
	}
	invalidUTF8 := bytes.Replace(raw, []byte("Keep"), []byte{0xff}, 1)
	directoryTestRaw(t, invalidUTF8, false)
	oversized := append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), maxInput)...)
	directoryTestRaw(t, oversized, false)
	baseline.Objects = make([]dc.Object, dc.MaxObjects+1)
	req["baseline"] = baseline
	req["batches"] = []dc.Batch{}
	directoryTestRun(t, req, false)
	req = directoryTestRequest("hash")
	req["batch"] = map[string]any{"SourceEpoch": "epoch-1", "Sequence": 1, "PreviousHash": strings.Repeat("0", 64), "Complete": true, "Changes": make([]dc.Change, dc.MaxChanges+1)}
	directoryTestRun(t, req, false)
	// Accepted empty batches consume the core's 32-batch budget; repetitions do not.
	baseline = dc.Baseline{SourceEpoch: "epoch-1", Complete: true, Objects: []dc.Object{}}
	state, _ := dc.New(baseline)
	many := []dc.Batch{}
	for i := 1; i <= dc.MaxBatches+1; i++ {
		b := dc.Batch{SourceEpoch: "epoch-1", Sequence: uint64(i), PreviousHash: state.Snapshot().Cursor.Hash, Complete: true, Changes: []dc.Change{}}
		b.BatchHash, _ = dc.HashBatch(b)
		many = append(many, b)
		if i <= dc.MaxBatches {
			state, _, _ = state.Apply(b)
		}
	}
	req = directoryTestRequest("replay")
	req["baseline"] = baseline
	req["batches"] = many[:dc.MaxBatches]
	directoryTestRun(t, req, true)
	req["batches"] = many
	directoryTestRun(t, req, false)
}

func TestDirectoryChangesArrayTypes(t *testing.T) {
	for _, payload := range []string{
		`"baseline":{"SourceEpoch":"lab","Complete":true,"Objects":{}},"batches":[]`,
		`"baseline":{"SourceEpoch":"lab","Complete":true,"Objects":[]},"batches":{}`,
	} {
		directoryTestRaw(t, []byte(`{"contract":"`+directoryCLIContract+`","synthetic":true,"mode":"replay",`+payload+`}`), false)
	}
	directoryTestRaw(t, []byte(`{"contract":"`+directoryCLIContract+`","synthetic":true,"mode":"hash","batch":{"SourceEpoch":"lab","Sequence":1,"PreviousHash":"`+strings.Repeat("0", 64)+`","Complete":true,"Changes":{}}}`), false)
}

func TestDirectoryChangesSurrogatesBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "adtr-simulate")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, tc := range []struct {
		name, encoded, want string
		valid               bool
	}{
		{"lone-high", `\ud800`, "", false},
		{"lone-low", `\udfff`, "", false},
		{"high-high", `\ud800\ud800`, "", false},
		{"high-scalar", `\ud800\u0061`, "", false},
		{"high-text", `\ud800x`, "", false},
		{"high-escaped-backslash", `\ud800\\udc00`, "", false},
		{"low-high", `\udc00\ud800`, "", false},
		{"valid-pair", `\ud83d\ude00`, "😀", true},
		{"literal-replacement", `�`, "�", true},
		{"escaped-replacement", `\ufffd`, "�", true},
		{"escaped-backslash", `\\ud800`, `\ud800`, true},
		{"escaped-quote", `quoted\"text`, `quoted"text`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{"contract":"` + directoryCLIContract + `","synthetic":true,"mode":"replay","baseline":{"SourceEpoch":"lab","Complete":true,"Objects":[{"ID":"u","Kind":"user","Version":1,"Fields":{"Name":"` + tc.encoded + `","Enabled":false},"Deleted":false}]},"batches":[]}`)
			directoryTestRaw(t, data, tc.valid)
			command := exec.Command(binary, "directory-changes")
			command.Stdin = bytes.NewReader(data)
			var out, stderr bytes.Buffer
			command.Stdout = &out
			command.Stderr = &stderr
			err := command.Run()
			if !tc.valid {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || out.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("invalid: %v stdout=%s stderr=%s", err, out.String(), stderr.String())
				}
				return
			}
			if err != nil || stderr.Len() != 0 {
				t.Fatalf("valid: %v %s", err, stderr.String())
			}
			var envelope struct {
				Result struct{ Snapshot dc.Snapshot }
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if got := envelope.Result.Snapshot.Objects[0].Fields.Name; got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
