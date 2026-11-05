package domains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func rawDNFixture(t *testing.T) ldapconnection.DirectoryV4Observation {
	t.Helper()
	o := sampleDirectoryV4StorageObservation()
	base := o.Objects[0]
	o.Objects = nil
	for i := 1; i <= 31; i++ {
		row := base
		row.Base.GUID = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		row.Base.DN = fmt.Sprintf("CN=g%d,DC=example,DC=test", i)
		row.Membership.MemberBytes = [][]byte{[]byte("CN=Target,DC=example,DC=test")}
		o.Objects = append(o.Objects, row)
	}
	for i, kind := range []directoryassets.Kind{directoryassets.User, directoryassets.User, directoryassets.Computer} {
		row := base
		row.Base.GUID = fmt.Sprintf("00000000-0000-0000-0000-%012d", 100+i)
		row.Base.Kind = kind
		row.Base.Classes = []string{string(kind)}
		if kind == directoryassets.Computer {
			row.Base.Classes = []string{"computer", "user"}
		}
		row.Base.DN = "CN=Target,DC=example,DC=test"
		if kind == directoryassets.Computer {
			row.Base.DN = "CN=Computer,DC=example,DC=test"
		}
		row.Membership.MemberBytes = nil
		o.Objects = append(o.Objects, row)
	}
	o.Objects[0].Base.DN = "CN=Target,DC=example,DC=test" // Self-reference, also shared by two distinct user GUIDs.
	o.Objects[1].Membership.MemberBytes = nil
	o.Objects[2].Membership.MemberBytes = [][]byte{}
	o.Objects[3].Membership.MemberBytes = [][]byte{[]byte("cn=Target,DC=example,DC=test")}
	o.Objects[4].Membership.MemberBytes = [][]byte{[]byte("CN=\\54arget,DC=example,DC=test")}
	o.Objects[5].Membership.MemberBytes = [][]byte{[]byte(" CN=Target,DC=example,DC=test")}
	return o
}
func rawDNInput(guid string) RawDNReferencesV4Input {
	return RawDNReferencesV4Input{UserAssetV2Input: UserAssetV2Input{DomainID: "domain-a", ExpectedRevision: "1", ExpectedCredentialRevision: "1", ObservationID: "observation-a", ObjectGUID: guid}, PageIdx: 1, PageSize: 25}
}
func TestRawDNReferencesV4ExactTextKindsCoverageAndPaging(t *testing.T) {
	o := rawDNFixture(t)
	defer o.Discard()
	// Validate the full immutable encoding first, just as the production loader.
	raw, err := encodeDirectoryObservationV4(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	sum := sha256.Sum256(raw)
	validated, err := decodeDirectoryObservationV4(raw, hex.EncodeToString(sum[:]), len(o.Objects))
	if err != nil {
		t.Fatal(err)
	}
	defer validated.Discard()
	selection := UserAssetsV2Selection{DomainID: "domain-a", Revision: "1", CredentialRevision: "1"}
	var first RawDNReferencesV4
	for _, idx := range []int{0, 31, 32} {
		in := rawDNInput(o.Objects[idx].Base.GUID)
		out, err := rawDNReferencesV4Page(context.Background(), selection, in.ObservationID, validated, in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Page != (Page{Index: 1, Size: 25, Total: 26, Pages: 2}) || len(out.List) != 25 || out.Coverage != (RawDNReferencesV4Coverage{31, 30, 1}) {
			t.Fatal("page/whole observation coverage mismatch", out.Page, out.Coverage)
		}
		if out.List[0].GUID != o.Objects[0].Base.GUID || out.List[1].GUID != o.Objects[6].Base.GUID {
			t.Fatal("exact text matching or sort mismatch")
		}
		if idx == 0 {
			first = out
		} else if !reflect.DeepEqual(first.List, out.List) {
			t.Fatal("shared DN was incorrectly resolved to a GUID")
		}
		in.PageIdx = 2
		out, err = rawDNReferencesV4Page(context.Background(), selection, in.ObservationID, validated, in)
		if err != nil || len(out.List) != 1 || out.List[0].GUID != o.Objects[30].Base.GUID {
			t.Fatal("second page", err)
		}
		in.PageIdx = 10000
		out, err = rawDNReferencesV4Page(context.Background(), selection, in.ObservationID, validated, in)
		if err != nil || out.List == nil || len(out.List) != 0 || out.Page.Total != 26 {
			t.Fatal("out of range page", err)
		}
	}
	in := rawDNInput(o.Objects[33].Base.GUID)
	out, err := rawDNReferencesV4Page(context.Background(), selection, in.ObservationID, validated, in)
	if err != nil || out.Page.Total != 0 || out.List == nil || out.Coverage.GroupCount != 31 {
		t.Fatal("zero computer", err)
	}
	raw, err = json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || len(fields) != 8 {
		t.Fatal("response keys", err)
	}
	for _, key := range []string{"dictionaryVersion", "selection", "observationId", "source", "object", "list", "page", "coverage"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing key", key)
		}
	}
	// Input ordering cannot alter a GUID-sorted page.
	for i, j := 0, len(validated.Objects)-1; i < j; i, j = i+1, j-1 {
		validated.Objects[i], validated.Objects[j] = validated.Objects[j], validated.Objects[i]
	}
	in = rawDNInput(o.Objects[0].Base.GUID)
	out, err = rawDNReferencesV4Page(context.Background(), selection, in.ObservationID, validated, in)
	if err != nil || !reflect.DeepEqual(first.List, out.List) {
		t.Fatal("unstable input order", err)
	}
}
func TestRawDNReferencesV4InputCancellationAndCap(t *testing.T) {
	o := rawDNFixture(t)
	defer o.Discard()
	in := rawDNInput(o.Objects[0].Base.GUID)
	for _, size := range []int{25, 50, 100} {
		in.PageSize = size
		if err := ValidateRawDNReferencesV4Input(in); err != nil {
			t.Fatal(err)
		}
	}
	for _, size := range []int{-1, 0, 10, 26, 101} {
		in.PageSize = size
		if ValidateRawDNReferencesV4Input(in) == nil {
			t.Fatal("invalid size")
		}
	}
	in = rawDNInput(o.Objects[0].Base.GUID)
	for _, idx := range []int{0, -1, 10001} {
		in.PageIdx = idx
		if ValidateRawDNReferencesV4Input(in) == nil {
			t.Fatal("invalid page")
		}
	}
	in = rawDNInput(o.Objects[0].Base.GUID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rawDNReferencesV4Page(ctx, UserAssetsV2Selection{}, in.ObservationID, o, in); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel", err)
	}
	in.ObjectGUID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if _, err := rawDNReferencesV4Page(context.Background(), UserAssetsV2Selection{}, in.ObservationID, o, in); err == nil {
		t.Fatal("absent target")
	}
	// Public JSON expansion can exceed the cap although stored member bytes fit.
	in = rawDNInput(o.Objects[0].Base.GUID)
	in.PageSize = 100
	base := o.Objects[0]
	o.Objects = o.Objects[:1]
	for i := 2; i <= 26; i++ {
		row := base
		row.Base.GUID = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		row.Membership.MemberBytes = [][]byte{[]byte(base.Base.DN)}
		for j := 0; j < 15; j++ {
			row.Membership.MemberBytes = append(row.Membership.MemberBytes, []byte(fmt.Sprintf("%02d", j)+strings.Repeat("\x00", 4094)))
		}
		o.Objects = append(o.Objects, row)
	}
	raw, err := encodeDirectoryObservationV4(o)
	if err != nil {
		t.Fatal("valid stored cap fixture", err)
	}
	clear(raw)
	if _, err := rawDNReferencesV4Page(context.Background(), UserAssetsV2Selection{}, in.ObservationID, o, in); err == nil {
		t.Fatal("expanded response cap accepted")
	}
}
