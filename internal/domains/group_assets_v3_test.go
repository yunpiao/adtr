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

func groupFilter() GroupAssetsV3Filter {
	return GroupAssetsV3Filter{DomainID: "domain", ExpectedRevision: "1", ExpectedCredentialRevision: "2", PageIdx: 1, PageSize: 10}
}
func groupObservation() ldapconnection.DirectoryV3Observation {
	o := sampleDirectoryV3Observation()
	base := o.Objects[0]
	o.Objects = nil
	for i := 23; i >= 1; i-- {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		v.Base.Kind = directoryassets.Group
		v.Base.Classes = []string{"group", "top"}
		sam := fmt.Sprintf("group-%02d", i)
		v.Base.SAMAccountName = &sam
		v.Base.DN = fmt.Sprintf("CN=group-%02d,DC=example,DC=test", i)
		v.Supplemental.WhenCreatedBytes = []byte("20260101000000.0Z")
		if i == 1 {
			v.Supplemental.WhenCreatedBytes = nil
		}
		if i == 2 {
			v.Supplemental.WhenCreatedBytes = []byte("00010101000000.0Z")
		}
		o.Objects = append(o.Objects, v)
	}
	for i, k := range []directoryassets.Kind{directoryassets.User, directoryassets.Computer} {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 100+i)
		v.Base.Kind = k
		v.Base.Classes = []string{string(k), "top"}
		o.Objects = append(o.Objects, v)
	}
	return o
}
func TestGroupAssetsV3BoundsAndNoSourceFallback(t *testing.T) {
	for _, change := range []func(*GroupAssetsV3Filter){
		func(f *GroupAssetsV3Filter) { f.DomainID = "" }, func(f *GroupAssetsV3Filter) { f.ExpectedRevision = "01" },
		func(f *GroupAssetsV3Filter) { f.ExpectedCredentialRevision = "0" }, func(f *GroupAssetsV3Filter) { f.PageIdx = 2 },
		func(f *GroupAssetsV3Filter) { f.PageSize = -1 }, func(f *GroupAssetsV3Filter) { f.PageIdx = 10001 },
		func(f *GroupAssetsV3Filter) { f.Search = strings.Repeat("😀", 11) }, func(f *GroupAssetsV3Filter) { f.Search = string([]byte{255}) },
		func(f *GroupAssetsV3Filter) { f.CreatTm = "1" },
	} {
		f := groupFilter()
		change(&f)
		_, e := New(nil).GroupAssetsV3ListTx(context.Background(), nil, "t", []string{"domain"}, f)
		requireUserAssetsV2Error(t, e, 400, "invalid_input")
	}
	for _, search := range []string{"", " \x00\r\n", strings.Repeat("😀", 10)} {
		f := groupFilter()
		f.Search = search
		if e := ValidateGroupAssetsV3Filter(f); e != nil {
			t.Fatal(e)
		}
	}
	f := groupFilter()
	_, e := New(nil).GroupAssetsV3ListTx(context.Background(), nil, "t", nil, f)
	requireUserAssetsV2Error(t, e, 404, "not_found")
}
func TestGroupAssetsV3LiteralSearchExcludesUncollectedAndUnpromisedFields(t *testing.T) {
	sam, sid, mail := "É SigmaΣ literal.*$", "S-1-5-21", "mail-only"
	o := directoryassets.PublicObjectV3{PublicObjectV2: directoryassets.PublicObjectV2{Object: directoryassets.Object{DN: "CN=raw\\0A,DC=example,DC=test", SAMAccountName: &sam}, ObjectSID: &sid, Mail: &mail, Description: []string{"description-only"}}, ServicePrincipalName: []string{"HOST/spn-only"}}
	for _, q := range []string{"é", "sigmaσ", ".*$", "s-1-5-21", "raw\\0a", ""} {
		if !groupAssetsV3Match(o, strings.ToLower(q)) {
			t.Fatal("literal match lost", q)
		}
	}
	for _, q := range []string{"mail-only", "spn-only", "description-only", "e\u0301", "sigmaς", "a.*b", "s-1-5-21cn="} {
		if groupAssetsV3Match(o, strings.ToLower(q)) {
			t.Fatal("unpromised search", q)
		}
	}
}
func TestGroupAssetsV3CountPinPagingAndFacts(t *testing.T) {
	o := groupObservation()
	defer o.Discard()
	f := groupFilter()
	selection := UserAssetsV2Selection{DomainID: "domain", Revision: "1", CredentialRevision: "2"}
	first, e := groupAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || !first.Available || first.DictionaryVersion != 3 || *first.ObservedGroupCount != 23 || first.Page.Total != 23 || first.Page.Pages != 3 || len(first.List) != 10 || first.ObservationID != "obs" {
		t.Fatal(first, e)
	}
	f.PageIdx = 2
	f.ObservationID = "obs"
	second, e := groupAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || len(second.List) != 10 || second.List[0].GUID <= first.List[9].GUID {
		t.Fatal(second, e)
	}
	for _, row := range first.List {
		if row.Kind != directoryassets.Group || row.PrimaryGroupID == nil || *row.PrimaryGroupID != 0 || len(row.ServicePrincipalName) != 2 {
			t.Fatal("facts lost")
		}
	}
	f.PageIdx = 1
	f.Search = "group-23"
	only, e := groupAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || only.Page.Total != 1 || *only.ObservedGroupCount != 23 || len(only.List) != 1 {
		t.Fatal(only, e)
	}
	f.Search = "not present"
	none, e := groupAssetsV3Page(context.Background(), selection, "obs", o, f)
	if e != nil || !none.Available || none.Page.Total != 0 || *none.ObservedGroupCount != 23 || len(none.List) != 0 {
		t.Fatal(none, e)
	}
	o.Objects = nil
	zero, e := groupAssetsV3Page(context.Background(), selection, "empty", o, f)
	if e != nil || !zero.Available || zero.ObservedGroupCount == nil || *zero.ObservedGroupCount != 0 {
		t.Fatal(zero, e)
	}
	absent := GroupAssetsV3List{DictionaryVersion: 3, Selection: selection, List: []directoryassets.PublicObjectV3{}, Page: Page{Index: 1, Size: 10}}
	raw, _ := json.Marshal(absent)
	if !strings.Contains(string(raw), `"observedGroupCount":null`) || strings.Contains(string(raw), `"source"`) {
		t.Fatal(string(raw))
	}
}
func TestGroupAssetsV3CreationNullLastAndCancellation(t *testing.T) {
	o := groupObservation()
	defer o.Discard()
	f := groupFilter()
	f.PageSize = 50
	for _, order := range []string{"asc", "desc"} {
		f.CreatTm = order
		out, e := groupAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, e := groupAssetsV3Page(ctx, UserAssetsV2Selection{}, "obs", o, f)
	if e != context.Canceled || out.Available || len(out.List) != 0 {
		t.Fatal("partial cancellation", e)
	}
}

func TestGroupAssetsV3UnicodeLowercasePredicateVectors(t *testing.T) {
	for _, tc := range []struct {
		value, query string
		match        bool
	}{
		{"İ", "i", true}, {"İ", "i\u0307", false},
		{"οσ", "ΟΣ", true}, {"ος", "ΟΣ", false},
		{"e\u0301", "é", false}, {"𐐨", "𐐀", true},
		{"prefixA%_?*[x]suffix", "a%_?*[x]", true},
		{"axxxb", "a.*b", false}, {"literal", " literal ", false},
	} {
		t.Run(tc.value+"/"+tc.query, func(t *testing.T) {
			o := directoryassets.PublicObjectV3{}
			o.DN = "CN=X"
			o.SAMAccountName = &tc.value
			if got := groupAssetsV3Match(o, strings.ToLower(tc.query)); got != tc.match {
				t.Fatalf("literal predicate mismatch: got %v, want %v", got, tc.match)
			}
		})
	}
}

func TestGroupAssetsV3CompleteResponseCapNeverLeaksPartialRows(t *testing.T) {
	o := groupObservation()
	defer o.Discard()
	base := o.Objects[0]
	base.Identity.ServicePrincipalNameBytes = nil
	for i := 0; i < 32; i++ {
		raw := make([]byte, 1024)
		raw[1023] = byte(33 + i)
		base.Identity.ServicePrincipalNameBytes = append(base.Identity.ServicePrincipalNameBytes, raw)
	}
	o.Objects = nil
	for i := 1; i <= 50; i++ {
		v := base
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		o.Objects = append(o.Objects, v)
	}
	f := groupFilter()
	f.PageSize = 50
	out, e := groupAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
	requireUserAssetsV2Error(t, e, 422, "directory_limit_exceeded")
	if out.Available || out.List != nil {
		t.Fatal("oversize group response leaked partial rows")
	}
	f.PageSize = 10
	if out, e = groupAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f); e != nil || len(out.List) != 10 {
		t.Fatal("bounded group page failed", e)
	}
}
