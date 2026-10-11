package domains

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"strings"
	"testing"
)

func computerFilter() ComputerAssetsV3Filter {
	return ComputerAssetsV3Filter{DomainID: "domain", ExpectedRevision: "1", ExpectedCredentialRevision: "2", PageIdx: 1, PageSize: 10}
}
func computerObservation() ldapconnection.DirectoryV3Observation {
	o := sampleDirectoryV3Observation()
	base := o.Objects[0]
	o.Objects = nil
	for i := 23; i >= 1; i-- {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		v.Base.Kind = directoryassets.Computer
		v.Base.Classes = []string{"computer", "top", "user"}
		sam := fmt.Sprintf("computer-%02d$", i)
		v.Base.SAMAccountName = &sam
		v.Base.DN = fmt.Sprintf("CN=computer-%02d,DC=example,DC=test", i)
		v.Supplemental.WhenCreatedBytes = []byte("20260101000000.0Z")
		if i == 1 {
			v.Supplemental.WhenCreatedBytes = nil
		}
		if i == 2 {
			v.Supplemental.WhenCreatedBytes = []byte("00010101000000.0Z")
		}
		o.Objects = append(o.Objects, v)
	}
	for i, k := range []directoryassets.Kind{directoryassets.User, directoryassets.Group} {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 100+i)
		v.Base.Kind = k
		v.Base.Classes = []string{string(k), "top"}
		o.Objects = append(o.Objects, v)
	}
	return o
}
func TestComputerAssetsV3BoundsAndNoSourceFallback(t *testing.T) {
	for _, change := range []func(*ComputerAssetsV3Filter){
		func(f *ComputerAssetsV3Filter) { f.DomainID = "" }, func(f *ComputerAssetsV3Filter) { f.ExpectedRevision = "01" },
		func(f *ComputerAssetsV3Filter) { f.ExpectedCredentialRevision = "0" }, func(f *ComputerAssetsV3Filter) { f.PageIdx = 2 },
		func(f *ComputerAssetsV3Filter) { f.PageSize = -1 }, func(f *ComputerAssetsV3Filter) { f.PageIdx = 10001 },
		func(f *ComputerAssetsV3Filter) { f.Search = strings.Repeat("😀", 26) }, func(f *ComputerAssetsV3Filter) { f.Search = string([]byte{255}) },
		func(f *ComputerAssetsV3Filter) { f.CreatTm = "1" }, func(f *ComputerAssetsV3Filter) { f.StartTm = "2026-01-01T00:00:00Z" },
	} {
		f := computerFilter()
		change(&f)
		_, e := New(nil).ComputerAssetsV3ListTx(context.Background(), nil, "t", []string{"domain"}, f)
		requireUserAssetsV2Error(t, e, 400, "invalid_input")
	}
	for _, search := range []string{"", " \x00\r\n", strings.Repeat("😀", 25)} {
		f := computerFilter()
		f.Search = search
		if e := ValidateComputerAssetsV3Filter(f); e != nil {
			t.Fatal(e)
		}
	}
	f := computerFilter()
	_, e := New(nil).ComputerAssetsV3ListTx(context.Background(), nil, "t", nil, f)
	requireUserAssetsV2Error(t, e, 404, "not_found")
}
func TestComputerAssetsV3LiteralSearchExcludesUncollectedAndUnpromisedFields(t *testing.T) {
	sam, sid, mail := "É SigmaΣ literal.*$", "S-1-5-21", "mail-only"
	o := directoryassets.PublicObjectV3{PublicObjectV2: directoryassets.PublicObjectV2{Object: directoryassets.Object{DN: "CN=raw\\0A,DC=example,DC=test", SAMAccountName: &sam}, ObjectSID: &sid, Mail: &mail, Description: []string{"description-only"}}, ServicePrincipalName: []string{"HOST/spn-only"}}
	for _, q := range []string{"é", "sigmaσ", ".*$", "s-1-5-21", "raw\\0a", ""} {
		if !computerAssetsV3Match(o, strings.ToLower(q)) {
			t.Fatal("literal match lost", q)
		}
	}
	for _, q := range []string{"mail-only", "spn-only", "description-only", "e\u0301", "sigmaς", "a.*b", "s-1-5-21cn="} {
		if computerAssetsV3Match(o, strings.ToLower(q)) {
			t.Fatal("unpromised search", q)
		}
	}
}
func TestComputerAssetsV3CountPinPagingAndFacts(t *testing.T) {
	o := computerObservation()
	defer o.Discard()
	f := computerFilter()
	selection := UserAssetsV2Selection{DomainID: "domain", Revision: "1", CredentialRevision: "2"}
	first, e := computerAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || !first.Available || first.DictionaryVersion != 3 || *first.ObservedComputerCount != 23 || first.Page.Total != 23 || first.Page.Pages != 3 || len(first.List) != 10 || first.ObservationID != "obs" {
		t.Fatal(first, e)
	}
	f.PageIdx = 2
	f.ObservationID = "obs"
	second, e := computerAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || len(second.List) != 10 || second.List[0].GUID <= first.List[9].GUID {
		t.Fatal(second, e)
	}
	for _, row := range first.List {
		if row.Kind != directoryassets.Computer || row.PrimaryGroupID == nil || *row.PrimaryGroupID != 0 || len(row.ServicePrincipalName) != 2 {
			t.Fatal("facts lost")
		}
	}
	f.PageIdx = 1
	f.Search = "computer-23"
	only, e := computerAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || only.Page.Total != 1 || *only.ObservedComputerCount != 23 || len(only.List) != 1 {
		t.Fatal(only, e)
	}
	f.Search = "not present"
	none, e := computerAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || !none.Available || none.Page.Total != 0 || *none.ObservedComputerCount != 23 || len(none.List) != 0 {
		t.Fatal(none, e)
	}
	o.Objects = nil
	zero, e := computerAssetsV3Page(context.Background(), selection, "empty", o, f)
	if e != nil || !zero.Available || zero.ObservedComputerCount == nil || *zero.ObservedComputerCount != 0 {
		t.Fatal(zero, e)
	}
	absent := ComputerAssetsV3List{DictionaryVersion: 3, Selection: selection, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: 1, Size: 10}}
	raw, _ := json.Marshal(absent)
	if !strings.Contains(string(raw), `"observedComputerCount":null`) || strings.Contains(string(raw), `"source"`) {
		t.Fatal(string(raw))
	}
}
func TestComputerAssetsV3CreationHalfOpenNullLastAndCancellation(t *testing.T) {
	o := computerObservation()
	defer o.Discard()
	f := computerFilter()
	f.PageSize = 50
	for _, order := range []string{"asc", "desc"} {
		f.CreatTm = order
		out, e := computerAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
		if e != nil || out.List[22].WhenCreated != nil {
			t.Fatal(e)
		}
		for i := 1; i < len(out.List)-1; i++ {
			a, b := out.List[i-1], out.List[i]
			if *a.WhenCreated == *b.WhenCreated && a.GUID >= b.GUID {
				t.Fatal("unstable tie")
			}
		}
	}
	f.StartTm = "2026-01-01T00:00:00Z"
	f.EndTm = "2026-01-01T00:00:00.000000001Z"
	out, e := computerAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
	if e != nil || out.Page.Total != 21 || *out.ObservedComputerCount != 23 {
		t.Fatal(out, e)
	}
	f.StartTm = "2025-12-31T23:59:59.999999999Z"
	f.EndTm = "2026-01-01T00:00:00Z"
	out, e = computerAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
	if e != nil || out.Page.Total != 0 {
		t.Fatal(out, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, e = computerAssetsV3Page(ctx, UserAssetsV2Selection{}, "obs", o, f)
	if e != context.Canceled || out.Available || len(out.List) != 0 {
		t.Fatal("partial cancellation", e)
	}
}
