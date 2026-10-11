package ldapconnection

import (
	"bytes"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"reflect"
	"strings"
	"testing"
)

func TestDirectoryV3WireRejectsUnknownMalformedAndOverBudgetIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs [][]byte
		code  Code
	}{
		{"unknown", [][]byte{fixtureAttribute("primaryGroupRID", "513")}, CodeInvalidResponse},
		{"range", [][]byte{fixtureAttribute("servicePrincipalName;range=0-*", "HOST/a")}, CodeInvalidResponse},
		{"option", [][]byte{fixtureAttribute("servicePrincipalName;binary", "HOST/a")}, CodeInvalidResponse},
		{"duplicatealias", [][]byte{fixtureAttribute("primaryGroupID", "513"), fixtureAttribute("PRIMARYGROUPID", "513")}, CodeInvalidResponse},
		{"primarymulti", [][]byte{fixtureAttribute("primaryGroupID", "513", "514")}, CodeInvalidResponse},
		{"primarynegative", [][]byte{fixtureAttribute("primaryGroupID", "-1")}, CodeUnsupportedProfile},
		{"primaryoverflow", [][]byte{fixtureAttribute("primaryGroupID", "4294967296")}, CodeUnsupportedProfile},
		{"primaryleadingzero", [][]byte{fixtureAttribute("primaryGroupID", "0513")}, CodeUnsupportedProfile},
		{"SPNempty", [][]byte{fixtureAttribute("servicePrincipalName", "")}, CodeInvalidResponse},
		{"SPNinvalidUTF8", [][]byte{fixtureAttribute("servicePrincipalName", string([]byte{0xed, 0xa0, 0x80}))}, CodeInvalidResponse},
		{"SPNduplicate", [][]byte{fixtureAttribute("servicePrincipalName", "HOST/a", "HOST/a")}, CodeInvalidResponse},
		{"SPNunits", [][]byte{fixtureAttribute("servicePrincipalName", strings.Repeat("😀", 513))}, CodeUnsupportedProfile},
		{"SPNbytes", [][]byte{fixtureAttribute("servicePrincipalName", strings.Repeat("a", 4097))}, CodeUnsupportedProfile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fixtureDirectoryV3Body(1, "user", tc.attrs...)
			prior := bytes.Clone(body)
			got, err := directoryEntryV3(body, fixtureDirectoryBase)
			assertError(t, err, tc.code, StageSearch)
			if !reflect.DeepEqual(got, directoryassets.Entry{}) || !bytes.Equal(body, prior) {
				t.Fatal("error retained data or changed input")
			}
		})
	}
}
func TestDirectoryV3WireOwnsNewIdentityBytesAndOldProfilesReject(t *testing.T) {
	body := fixtureDirectoryV3Body(1, "user", fixtureV3Supplemental()...)
	entry, err := directoryEntryV3(body, fixtureDirectoryBase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = directoryEntryV2(body, fixtureDirectoryBase); err == nil {
		t.Fatal("v2 widened")
	}
	if _, err = directoryEntry(body, fixtureDirectoryBase); err == nil {
		t.Fatal("v1 widened")
	}
	clear(body)
	o, err := directoryassets.DecodeV3(entry)
	if err != nil {
		t.Fatal(err)
	}
	clearDirectoryEntries([]directoryassets.Entry{entry})
	p, err := directoryassets.ProjectV3(o)
	if err != nil || p.PrimaryGroupID == nil || *p.PrimaryGroupID != 513 || !reflect.DeepEqual(p.ServicePrincipalName, []string{"HOST/Z", "host/a\x00\n😀<script>"}) {
		t.Fatal("raw identity ownership broken")
	}
	directoryassets.ClearStoredObjectsV3([]directoryassets.StoredObjectV3{o})
}
