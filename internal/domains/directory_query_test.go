package domains

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestDirectoryPagingRequiresPinnedContinuationAndBounds(t *testing.T) {
	valid := DirectoryFilter{DomainID: "domain-one", PageIdx: 1, PageSize: 25}
	if err := ValidateDirectoryFilter(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*DirectoryFilter){func(f *DirectoryFilter) { f.PageIdx = 2 }, func(f *DirectoryFilter) { f.PageIdx = 0 }, func(f *DirectoryFilter) { f.PageSize = 1000 }, func(f *DirectoryFilter) { f.Kind = "managed-service-account" }, func(f *DirectoryFilter) { f.ObservationID = "a/b" }} {
		f := valid
		change(&f)
		if ValidateDirectoryFilter(f) == nil {
			t.Fatal("accepted invalid directory filter", f)
		}
	}
	valid.PageIdx = 2
	valid.ObservationID = "task-one"
	if err := ValidateDirectoryFilter(valid); err != nil {
		t.Fatal(err)
	}
}
func TestDirectoryPageFiltersBeforeStablePagination(t *testing.T) {
	o := sampleDirectoryObservation()
	base := o.Objects[0]
	o.Objects = make([]directoryassets.Object, 60)
	for i := range o.Objects {
		o.Objects[i] = base
		o.Objects[i].GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 59-i)
		if i%2 == 0 {
			o.Objects[i].Kind = directoryassets.Group
		}
	}
	out := directoryPage("snapshot", o, DirectoryFilter{Kind: directoryassets.User, PageIdx: 2, PageSize: 25})
	if !out.Available || out.ObservationID != "snapshot" || out.Page.Total != 30 || out.Page.Pages != 2 || len(out.List) != 5 {
		t.Fatal("incorrect filtered page", out.Page)
	}
	for i := 1; i < len(out.List); i++ {
		if out.List[i-1].GUID >= out.List[i].GUID {
			t.Fatal("unstable GUID ordering")
		}
	}
	empty := directoryPage("snapshot", o, DirectoryFilter{Kind: directoryassets.Computer, PageIdx: 1, PageSize: 25})
	if !empty.Available || empty.List == nil || len(empty.List) != 0 {
		t.Fatal("observed empty category became unavailable")
	}
}
func TestDirectoryScopeDeniedBeforeDatabase(t *testing.T) {
	_, err := (&Store{}).DirectoryListTx(context.Background(), nil, "tenant", nil, DirectoryFilter{DomainID: "other", PageIdx: 1, PageSize: 25})
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 404 {
		t.Fatal("unscoped read reached database", err)
	}
}
func TestDirectoryAdmissionInputPinsBothSourceRevisions(t *testing.T) {
	in := DirectoryInput{DomainID: "one", ExpectedRevision: "2", ExpectedCredentialGeneration: "3", IdempotencyKey: "intent"}
	if err := ValidateDirectoryInput(in); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0", "01", "+2", "-1", "9223372036854775808"} {
		bad := in
		bad.ExpectedCredentialGeneration = value
		if ValidateDirectoryInput(bad) == nil {
			t.Fatal("bad source generation accepted")
		}
		bad = in
		bad.ExpectedRevision = value
		if ValidateDirectoryInput(bad) == nil {
			t.Fatal("bad connection revision accepted")
		}
	}
	in.IdempotencyKey = "schedule_other"
	if ValidateDirectoryInput(in) == nil {
		t.Fatal("reserved scheduler key accepted")
	}
}
