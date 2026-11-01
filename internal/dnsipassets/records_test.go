package dnsipassets

import (
	"reflect"
	"strings"
	"testing"
)

func sample(id, ip string) Record {
	return Record{ID: id, Domain: "Example.TEST.", DNSName: "HOST.Example.TEST.", IP: ip, Observation: "obs-1", Source: "synthetic-fixture", Version: Version, Simulation: true}
}
func filter() Filter { return Filter{Domain: "example.test", Observation: "obs-1"} }
func TestNormalize(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{"2001:0db8:0:0::1", "2001:db8::1"}, {"::ffff:192.0.2.1", "192.0.2.1"}, {"192.0.2.2", "192.0.2.2"}} {
		r := sample("a", tt.in)
		got, e := Normalize(r)
		if e != nil || got.IP != tt.want || got.Domain != "example.test" || got.DNSName != "host.example.test" || r.IP != tt.in {
			t.Fatalf("normalize: %+v %v", got, e)
		}
	}
}
func TestRejectInputs(t *testing.T) {
	mutations := []func(*Record){func(r *Record) { r.Simulation = false }, func(r *Record) { r.Version = "legacy" }, func(r *Record) { r.Source = "" }, func(r *Record) { r.ID = "secret\n" }, func(r *Record) { r.Observation = "" }, func(r *Record) { r.IP = "fe80::1%eth0" }, func(r *Record) { r.IP = "999.0.0.1" }, func(r *Record) { r.IP = "192.0.2.01" }, func(r *Record) { r.DNSName = "=evil" }, func(r *Record) { r.DNSName = "a..b" }, func(r *Record) { r.DNSName = "-a.b" }, func(r *Record) { r.DNSName = "a-.b" }, func(r *Record) { r.DNSName = strings.Repeat("a", 64) + ".test" }, func(r *Record) { r.Domain = "é.test" }}
	for i, m := range mutations {
		r := sample("a", "192.0.2.1")
		m(&r)
		_, e := Normalize(r)
		if e != ErrInvalid || strings.Contains(e.Error(), "secret") {
			t.Fatalf("case %d: %v", i, e)
		}
	}
}
func TestSelectionPaginationAndScope(t *testing.T) {
	input := []Record{sample("v6", "2001:db8::1"), sample("b", "192.0.2.10"), sample("a", "192.0.2.2")}
	other := sample("foreign", "192.0.2.1")
	other.Domain = "other.test"
	input = append(input, other)
	other = sample("old", "192.0.2.1")
	other.Observation = "obs-0"
	input = append(input, other)
	before := append([]Record(nil), input...)
	all, e := Select(input, filter())
	if e != nil || len(all) != 3 || all[0].ID != "a" || all[1].ID != "b" || all[2].ID != "v6" {
		t.Fatal(all, e)
	}
	var pages []Record
	for i := 0; i < 3; i++ {
		p, n, e := List(input, filter(), i, 1)
		if e != nil || n != 3 {
			t.Fatal(e, n)
		}
		pages = append(pages, p...)
	}
	if !reflect.DeepEqual(all, pages) || !reflect.DeepEqual(before, input) {
		t.Fatal("paging/mutation")
	}
	f := filter()
	f.Family = "ipv4"
	f.Keyword = "HOST"
	out, e := Select(input, f)
	if e != nil || len(out) != 2 {
		t.Fatal(out, e)
	}
	f.Family = "ipv6"
	f.Keyword = "db8"
	out, e = Select(input, f)
	if e != nil || len(out) != 1 {
		t.Fatal(out, e)
	}
	out, n, e := List(input, filter(), int(^uint(0)>>1), 1000)
	if e != nil || n != 3 || len(out) != 0 {
		t.Fatal(n, e)
	}
}
func TestBoundsAndFailClosed(t *testing.T) {
	r := sample("a", "192.0.2.1")
	if _, e := Select([]Record{r, r}, filter()); e != ErrInvalid {
		t.Fatal(e)
	}
	bad := r
	bad.Source = ""
	if out, e := Select([]Record{r, bad}, filter()); e != ErrInvalid || out != nil {
		t.Fatal(out, e)
	}
	for _, f := range []Filter{{}, {Domain: "example.test", Observation: "obs-1", Family: "all"}, {Domain: "example.test", Observation: "obs-1", Keyword: "\n"}, {Domain: "example.test", Observation: "obs-1", Keyword: strings.Repeat("a", 254)}} {
		if _, e := Select(nil, f); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	for _, p := range [][2]int{{-1, 1}, {0, 0}, {0, 1001}} {
		if _, _, e := List(nil, filter(), p[0], p[1]); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	if _, e := Select(make([]Record, MaxRecords+1), filter()); e != ErrInvalid {
		t.Fatal(e)
	}
	out, e := Select(nil, filter())
	if e != nil || out == nil || len(out) != 0 {
		t.Fatal(out, e)
	}
}

func TestOriginalUnicodeDNSRejectedBeforeCaseFolding(t *testing.T) {
	for _, name := range []string{"K.example", "É.example", "é.example", "ｅ.example", "host。example", "\xff.example"} {
		t.Run(name, func(t *testing.T) {
			r := sample("a", "192.0.2.1")
			r.Domain = name
			if _, err := Normalize(r); err != ErrInvalid {
				t.Fatalf("Unicode record domain accepted: %v", err)
			}
			r = sample("a", "192.0.2.1")
			r.DNSName = name
			if _, err := Normalize(r); err != ErrInvalid {
				t.Fatalf("Unicode DNS name accepted: %v", err)
			}
			f := filter()
			f.Domain = name
			if _, err := Select(nil, f); err != ErrInvalid {
				t.Fatalf("Unicode filter domain accepted: %v", err)
			}
		})
	}
	r := sample("a", "192.0.2.1")
	r.Domain = "K.EXAMPLE."
	r.DNSName = "HOST.K.EXAMPLE."
	f := Filter{Domain: "k.example", Observation: "obs-1"}
	rows, err := Select([]Record{r}, f)
	if err != nil || len(rows) != 1 || rows[0].Domain != "k.example" || rows[0].DNSName != "host.k.example" {
		t.Fatalf("ASCII normalization regressed: %+v %v", rows, err)
	}
	f.Domain = "K.example"
	if rows, err := Select([]Record{r}, f); err != ErrInvalid || rows != nil {
		t.Fatal("Unicode scope alias accepted")
	}
}
