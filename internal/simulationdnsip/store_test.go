package simulationdnsip

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"strings"
	"testing"
)

func sample(n int) Fixture {
	f := Fixture{ID: "observation", DNSDomain: "EXAMPLE.TEST.", Source: "SIMULATED-unit"}
	for i := 0; i < n; i++ {
		r := dnsipassets.Record{ID: string(rune('a' + i)), Domain: "example.test", DNSName: string(rune('A'+i)) + ".EXAMPLE.TEST.", IP: "192.0.2.1", Observation: f.ID, Source: f.Source, Version: dnsipassets.Version, Simulation: true}
		if i == 1 {
			r.IP = "2001:db8::1"
		}
		if i == 2 {
			r.IP = "::ffff:192.0.2.2"
		}
		f.Records = append(f.Records, r)
	}
	return f
}
func TestNormalizeFixtureBindingAndDuplicate(t *testing.T) {
	v, e := normalizeFixture(sample(3))
	if e != nil || v.Observation.RecordCount != 3 || v.Observation.DNSDomain != "example.test" || v.Records[2].IP != "192.0.2.2" {
		t.Fatal(v, e)
	}
	for _, mutate := range []func(*Fixture){func(f *Fixture) { f.Records[0].Domain = "other.test" }, func(f *Fixture) { f.Records[0].Observation = "other" }, func(f *Fixture) { f.Records[0].Source = "other" }, func(f *Fixture) { f.Records[1].ID = f.Records[0].ID }, func(f *Fixture) { f.DNSDomain = "K.example" }, func(f *Fixture) { f.Records[0].Simulation = false }} {
		f := sample(3)
		mutate(&f)
		if _, e = normalizeFixture(f); e == nil {
			t.Fatal("accepted invalid fixture")
		}
	}
	if _, e = normalizeFixture(sample(0)); e != nil {
		t.Fatal(e)
	}
}
func TestRenderingAllRowsAndEmptyHeader(t *testing.T) {
	v, _ := normalizeFixture(sample(3))
	p := Payload{ObservationID: v.Observation.ID, ObservationHash: v.Observation.Hash, DNSDomain: v.Observation.DNSDomain, Contract: Contract}
	task := tasks.Task{ID: "task"}
	m, b, e := render(context.Background(), task, v, p)
	if e != nil || m.RowCount != 3 || m.Bytes != len(b) || m.SHA256 != sum(b) {
		t.Fatal(m, e)
	}
	m2, b2, e := render(context.Background(), task, v, p)
	if e != nil || digest(m) != digest(m2) || !bytes.Equal(b, b2) {
		t.Fatal("not deterministic")
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range z.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			r, _ := f.Open()
			data, _ := io.ReadAll(r)
			r.Close()
			if bytes.Count(data, []byte("<row ")) != 4 || bytes.Contains(data, []byte("<f>")) {
				t.Fatal(string(data))
			}
		}
	}
	p.Keyword = "nomatch"
	m, _, e = render(context.Background(), task, v, p)
	if e != nil || m.RowCount != 0 {
		t.Fatal(m, e)
	}
	p.Keyword = ""
	p.Family = "ipv4"
	m, _, e = render(context.Background(), task, v, p)
	if e != nil || m.RowCount != 2 {
		t.Fatal(m, e)
	}
}
func TestStrictPayloadAndOutputBudgets(t *testing.T) {
	v, _ := normalizeFixture(sample(1))
	p := Payload{ObservationID: v.Observation.ID, ObservationHash: v.Observation.Hash, DNSDomain: v.Observation.DNSDomain, Contract: Contract}
	b, _ := json.Marshal(p)
	if _, e := validate(b); e != nil {
		t.Fatal(e)
	}
	for _, raw := range [][]byte{append(append([]byte{}, b...), b...), []byte(strings.TrimSuffix(string(b), "}") + `,"rows":[]}`), []byte(`{}`)} {
		if _, e := validate(raw); e == nil {
			t.Fatal("accepted bad payload")
		}
	}
	var w boundedBuffer
	if _, e := w.Write(make([]byte, MaxArtifactBytes)); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Write([]byte{1}); !errors.Is(e, ErrLimit) || w.Len() != MaxArtifactBytes {
		t.Fatal(e)
	}
	if _, e := bounded(map[string]string{"x": strings.Repeat("a", 100)}, 100); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestKindDefaultGuardsAndCancellation(t *testing.T) {
	k := NewKind(nil)
	if k.Schedulable || !k.OwnerScoped || !k.ReplaySafe || !k.CancelDiscardsResult || k.ValidateSuccess == nil {
		t.Fatal("unsafe registry")
	}
	if _, e := tasks.NewRegistry(k); e != nil {
		t.Fatal(e)
	}
	out := k.Execute(context.Background(), tasks.Execution{})
	if out.State != tasks.Failed {
		t.Fatal(out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, _ := normalizeFixture(sample(1))
	_, _, e := render(ctx, tasks.Task{}, v, Payload{ObservationID: v.Observation.ID, ObservationHash: v.Observation.Hash, DNSDomain: v.Observation.DNSDomain})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if denied(context.DeadlineExceeded) || !denied(tasks.ErrAuthorization) {
		t.Fatal("infrastructure incorrectly classified")
	}
}
