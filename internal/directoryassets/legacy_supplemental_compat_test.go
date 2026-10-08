package directoryassets

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These bytes are the existing observation dictionary, independent of the
// supplemental component. Adding even null supplemental fields would change it.
func TestLegacyObjectJSONRemainsExact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs []Attribute
		want  string
	}{
		{
			name: "absent optionals stay null",
			want: `{"objectGUID":"00000001-0000-0000-0000-000000000000","distinguishedName":"CN=Synthetic,DC=example,DC=test","kind":"user","objectClass":["top","user"],"samAccountName":null,"userAccountControl":null}`,
		},
		{
			name: "present name and zero stay observed",
			attrs: []Attribute{
				{Name: "sAMAccountName", Values: [][]byte{[]byte("Case.Preserved")}},
				{Name: "userAccountControl", Values: [][]byte{[]byte("0")}},
			},
			want: `{"objectGUID":"00000001-0000-0000-0000-000000000000","distinguishedName":"CN=Synthetic,DC=example,DC=test","kind":"user","objectClass":["top","user"],"samAccountName":"Case.Preserved","userAccountControl":0}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := entry(1, "User", "top")
			e.Attributes = append(e.Attributes, tc.attrs...)
			object, err := Decode(e)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("legacy observation JSON changed\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestLegacyDecodeRejectsSupplementalAttributes(t *testing.T) {
	for _, attr := range []Attribute{
		{Name: "objectSid", Values: [][]byte{{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}}},
		{Name: "mail", Values: [][]byte{[]byte("Synthetic@Example.Test")}},
		{Name: "description", Values: [][]byte{[]byte("synthetic description")}},
		{Name: "whenCreated", Values: [][]byte{[]byte("20261007120000.0Z")}},
	} {
		for _, name := range []string{attr.Name, strings.ToUpper(attr.Name)} {
			t.Run(name, func(t *testing.T) {
				e := entry(1, "top", "user")
				attr.Name = name
				// Three attributes avoid masking a widened allowlist behind the
				// existing four-attribute aggregate limit.
				e.Attributes = append(e.Attributes, attr)
				got, err := Decode(e)
				if !errors.Is(err, ErrAttribute) {
					t.Fatalf("supplemental attribute crossed the legacy dictionary: %v", err)
				}
				if !reflect.DeepEqual(got, Object{}) {
					t.Fatal("rejected supplemental attribute exposed a partial legacy object")
				}
			})
		}
	}
}
