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
	f := validUserAssetsV2Filter()
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
	for _, mutate := range []func(*UserAssetsV2Filter){func(f *UserAssetsV2Filter) { f.DomainID = "" }, func(f *UserAssetsV2Filter) { f.PageIdx = 2 }, func(f *UserAssetsV2Filter) { f.PageSize = 25 }, func(f *UserAssetsV2Filter) { f.StartTm = "2026-01-01T00:00:00Z" }} {
		f := validUserAssetsV2Filter()
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
	f := validUserAssetsV2Filter()
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
		f := validUserAssetsV2Filter()
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
