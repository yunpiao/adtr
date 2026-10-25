package domains

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestUserAssetsV3PagePreservesIdentityAndVersion(t *testing.T) {
	observation := sampleDirectoryV3Observation()
	defer observation.Discard()
	base := observation.Objects[0]
	observation.Objects = nil
	for i := 23; i >= 1; i-- {
		row := base
		row.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		name := fmt.Sprintf("User %02d", i)
		row.Base.SAMAccountName = &name
		if i == 1 {
			row.Identity.PrimaryGroupIDBytes = []byte("4294967295")
		}
		if i == 2 {
			row.Identity = directoryassets.RawIdentityV3{}
		}
		observation.Objects = append(observation.Objects, row)
	}
	group := base
	group.Base.GUID = "00000099-4455-6677-8899-aabbccddeeff"
	group.Base.Kind = directoryassets.Group
	group.Base.Classes = []string{"group", "top"}
	observation.Objects = append(observation.Objects, group)
	selection := UserAssetsV2Selection{DomainID: "domain", Revision: "1", CredentialRevision: "2"}
	f := validUserAssetsV3Filter()
	page, err := userAssetsV3Page(context.Background(), selection, "pinned", observation, f)
	if err != nil || page.DictionaryVersion != 3 || page.ObservationID != "pinned" || page.Selection != selection || page.Page.Total != 23 || len(page.List) != 10 {
		t.Fatal("versioned page mismatch", err)
	}
	if page.List[0].PrimaryGroupID == nil || *page.List[0].PrimaryGroupID != 4294967295 || page.List[1].PrimaryGroupID != nil || page.List[1].ServicePrincipalName != nil || *page.List[2].PrimaryGroupID != 0 {
		t.Fatal("RID null/zero/max confused")
	}
	if !reflect.DeepEqual(page.List[0].ServicePrincipalName, []string{"HOST/Z", "host/a\x00\n😀"}) {
		t.Fatal("SPN raw order changed")
	}
	f.Search = "uSeR 23"
	page, err = userAssetsV3Page(context.Background(), selection, "pinned", observation, f)
	if err != nil || page.Page.Total != 1 || *page.List[0].SAMAccountName != "User 23" {
		t.Fatal("filter after page", err)
	}
	f.Search = "HOST/Z"
	page, err = userAssetsV3Page(context.Background(), selection, "pinned", observation, f)
	if err != nil || page.Page.Total != 0 || !page.Available {
		t.Fatal("SPN unexpectedly searched", err)
	}
	f.Search = ""
	f.SAMAccountName = "User 23"
	f.StartTm = "0001-01-01T00:00:00Z"
	f.EndTm = "0001-01-01T00:00:00.000000001Z"
	f.CreatTm = "desc"
	page, err = userAssetsV3Page(context.Background(), selection, "pinned", observation, f)
	if err != nil || page.Page.Total != 1 {
		t.Fatal("exact/time predicate changed", err)
	}
	f.SAMAccountName = "user 23"
	page, err = userAssetsV3Page(context.Background(), selection, "pinned", observation, f)
	if err != nil || page.Page.Total != 0 {
		t.Fatal("exact SAM was folded", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = userAssetsV3Page(cancelled, selection, "pinned", observation, f); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
}

func TestUserAssetsV3InvalidQueryRejectsBeforeSQL(t *testing.T) {
	for _, mutate := range []func(*UserAssetsV3Filter){func(f *UserAssetsV3Filter) { f.DomainID = "" }, func(f *UserAssetsV3Filter) { f.PageIdx = 2 }, func(f *UserAssetsV3Filter) { f.PageSize = 25 }, func(f *UserAssetsV3Filter) { f.StartTm = "2026-01-01T00:00:00Z" }} {
		f := validUserAssetsV3Filter()
		mutate(&f)
		_, err := New(nil).UserAssetsV3ListTx(context.Background(), nil, "tenant", []string{"domain"}, f)
		requireUserAssetsV2Error(t, err, 400, "invalid_input")
	}
}

func TestUserAssetsV3CompleteResponseCapAndNoPartialRows(t *testing.T) {
	observation := sampleDirectoryV3Observation()
	defer observation.Discard()
	base := observation.Objects[0]
	base.Identity.ServicePrincipalNameBytes = nil
	for i := 0; i < 32; i++ {
		value := make([]byte, 1024)
		value[1023] = byte(33 + i)
		base.Identity.ServicePrincipalNameBytes = append(base.Identity.ServicePrincipalNameBytes, value)
	}
	observation.Objects = nil
	for i := 1; i <= 50; i++ {
		row := base
		row.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		observation.Objects = append(observation.Objects, row)
	}
	f := validUserAssetsV3Filter()
	f.PageSize = 50
	out, err := userAssetsV3Page(context.Background(), UserAssetsV2Selection{"domain", "1", "2"}, "pinned", observation, f)
	requireUserAssetsV2Error(t, err, 422, "directory_limit_exceeded")
	if out.Available || out.List != nil {
		t.Fatal("overbudget response leaked partial result")
	}
	f.PageSize = 10
	if out, err = userAssetsV3Page(context.Background(), UserAssetsV2Selection{"domain", "1", "2"}, "pinned", observation, f); err != nil || len(out.List) != 10 {
		t.Fatal("bounded page rejected", err)
	}
}

func TestUserAssetsV3TimeOrderingUsesGUIDTiesAndNullLast(t *testing.T) {
	observation := sampleDirectoryV3Observation()
	defer observation.Discard()
	base := observation.Objects[0]
	observation.Objects = nil
	for i, when := range []string{"", "20260101000000.0Z", "00010101000000.0Z", "20260101000000.0Z"} {
		row := base
		row.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i+1)
		if when == "" {
			row.Supplemental.WhenCreatedBytes = nil
		} else {
			row.Supplemental.WhenCreatedBytes = []byte(when)
		}
		observation.Objects = append(observation.Objects, row)
	}
	for direction, want := range map[string][]int{"asc": {3, 2, 4, 1}, "desc": {2, 4, 3, 1}} {
		f := validUserAssetsV3Filter()
		f.CreatTm = direction
		page, err := userAssetsV3Page(context.Background(), UserAssetsV2Selection{"domain", "1", "2"}, "pinned", observation, f)
		if err != nil {
			t.Fatal(err)
		}
		for i, n := range want {
			if page.List[i].GUID != fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", n) {
				t.Fatal("time order lost null-last or GUID tie")
			}
		}
	}
}

func TestUserAssetsV3UACFilterBeforePaginationAndSharedExportRows(t *testing.T) {
	o := sampleDirectoryV3Observation()
	defer o.Discard()
	base := o.Objects[0]
	o.Objects = nil
	for i := 69; i >= 1; i-- {
		row := base
		row.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		name := fmt.Sprintf("User %02d", i)
		row.Base.SAMAccountName = &name
		switch i % 3 {
		case 0:
			row.Base.UserAccountControl = nil
		case 1:
			value := uint32(514)
			row.Base.UserAccountControl = &value
		case 2:
			value := uint32(16)
			row.Base.UserAccountControl = &value
		}
		o.Objects = append(o.Objects, row)
	}
	for _, kind := range []directoryassets.Kind{directoryassets.Group, directoryassets.Computer} {
		row := base
		row.Base.Kind = kind
		row.Base.GUID = "00000099-4455-6677-8899-aabbccddeeff"
		o.Objects = append(o.Objects, row)
	}
	selection := UserAssetsV2Selection{"domain", "1", "2"}
	for _, filter := range []string{"", "set", "clear", "not_returned"} {
		t.Run("filter="+filter, func(t *testing.T) {
			f := validUserAssetsV3Filter()
			f.ObservationID, f.UACDisableBit, f.CreatTm = "pinned", filter, "desc"
			f.Search, f.StartTm, f.EndTm = "uSeR", "0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000000001Z"
			wantTotal := 23
			if filter == "" {
				wantTotal = 69
			}
			all, err := userAssetsV3Rows(context.Background(), o, f)
			if err != nil || len(all) != wantTotal {
				t.Fatal("all matching rows", err, len(all))
			}
			var paged []directoryassets.PublicObjectV3
			for f.PageIdx = 1; f.PageIdx <= (wantTotal+9)/10; f.PageIdx++ {
				page, err := userAssetsV3Page(context.Background(), selection, "pinned", o, f)
				if err != nil || page.Page.Total != wantTotal || page.Page.Pages != (wantTotal+9)/10 || !page.Available || page.ObservationID != "pinned" || page.Selection != selection {
					t.Fatal("filtered page", err, page.Page)
				}
				paged = append(paged, page.List...)
			}
			if !reflect.DeepEqual(all, paged) {
				t.Fatal("export rows differ from concatenated pages")
			}
			for i, row := range all {
				if !userAssetsV3UACDisableBitMatches(row.UserAccountControl, filter) || i > 0 && all[i-1].GUID >= row.GUID {
					t.Fatal("wrong bit partition or GUID tie order")
				}
			}
			f.PageIdx, f.SAMAccountName = 1, *all[len(all)-1].SAMAccountName
			page, err := userAssetsV3Page(context.Background(), selection, "pinned", o, f)
			if err != nil || page.Page.Total != 1 || page.List[0].GUID != all[len(all)-1].GUID {
				t.Fatal("AND exact SAM", err)
			}
			f.Search = "not-present"
			page, err = userAssetsV3Page(context.Background(), selection, "pinned", o, f)
			if err != nil || !page.Available || page.ObservationID != "pinned" || page.Source == nil || page.Page.Total != 0 || page.Page.Pages != 0 || len(page.List) != 0 {
				t.Fatal("empty filtered result lost availability or pins", err)
			}
		})
	}
}
