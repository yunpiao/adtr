package dnsipxlsx_test

import (
	"bytes"
	"context"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/dnsipxlsx"
	"strings"
	"testing"
)

func TestRecordExportProvenanceAndFilter(t *testing.T) {
	f := dnsipassets.Filter{Domain: "example.test", Observation: "o", Family: "ipv4"}
	r := dnsipassets.Record{ID: "a", Domain: "EXAMPLE.TEST", DNSName: "host.example.test", IP: "::ffff:192.0.2.1", Observation: "o", Source: "synthetic", Version: dnsipassets.Version, Simulation: true}
	v6 := r
	v6.ID = "b"
	v6.IP = "2001:db8::1"
	var b bytes.Buffer
	if e := dnsipxlsx.WriteRecords(context.Background(), &b, []dnsipassets.Record{v6, r}, f); e != nil {
		t.Fatal(e)
	}
	p := parts(t, b.Bytes())
	s := p["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"192.0.2.1", "dnsip-sim-v1", "synthetic", "Simulation", ">true<"} {
		if !strings.Contains(s, want) {
			t.Fatal("missing", want)
		}
	}
	if strings.Contains(s, "2001:db8") || strings.Count(s, "<row ") != 2 {
		t.Fatal(s)
	}
	b.Reset()
	if e := dnsipxlsx.WriteRecords(context.Background(), &b, nil, f); e != nil {
		t.Fatal(e)
	}
	if strings.Count(parts(t, b.Bytes())["xl/worksheets/sheet1.xml"], "<row ") != 1 {
		t.Fatal("empty workbook")
	}
	r.Simulation = false
	b.Reset()
	if e := dnsipxlsx.WriteRecords(context.Background(), &b, []dnsipassets.Record{r}, f); e == nil || b.Len() != 0 {
		t.Fatal("unmarked record emitted")
	}
}
