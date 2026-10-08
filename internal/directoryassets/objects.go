// Package directoryassets validates a bounded observation. It does not perform
// LDAP searches, authorize credential use, or infer that absent objects were deleted.
package directoryassets

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEntry     = errors.New("invalid_directory_entry")
	ErrAttribute = errors.New("invalid_directory_attribute")
	ErrGUID      = errors.New("invalid_directory_guid")
	ErrNumber    = errors.New("invalid_directory_number")
	ErrKind      = errors.New("unsupported_directory_object")
)

type Kind string

const (
	User     Kind = "user"
	Group    Kind = "group"
	Computer Kind = "computer"
)

type Attribute struct {
	Name   string
	Values [][]byte
}
type Entry struct {
	DN         string
	Attributes []Attribute
}

func (Entry) String() string         { return "directory entry (redacted)" }
func (e Entry) GoString() string     { return e.String() }
func (Attribute) String() string     { return "directory attribute (redacted)" }
func (a Attribute) GoString() string { return a.String() }

type Object struct {
	GUID               string   `json:"objectGUID"`
	DN                 string   `json:"distinguishedName"`
	Kind               Kind     `json:"kind"`
	Classes            []string `json:"objectClass"`
	SAMAccountName     *string  `json:"samAccountName"`
	UserAccountControl *uint32  `json:"userAccountControl"`
}

func boundedText(s string, maxBytes int) bool {
	if s == "" || len(s) > maxBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// AD's binary GUID uses little-endian Data1/2/3 and verbatim Data4 bytes.
func DecodeGUID(raw []byte) (string, error) {
	if len(raw) != 16 {
		return "", ErrGUID
	}
	return fmt.Sprintf("%08x-%04x-%04x-%s-%s", binary.LittleEndian.Uint32(raw[:4]), binary.LittleEndian.Uint16(raw[4:6]), binary.LittleEndian.Uint16(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:])), nil
}
func Decode(e Entry) (Object, error) {
	var out Object
	if !boundedText(e.DN, 4096) || len(e.Attributes) < 2 || len(e.Attributes) > 4 {
		return out, ErrEntry
	}
	attrs := make(map[string][][]byte, len(e.Attributes))
	for _, a := range e.Attributes {
		if len(a.Name) == 0 || len(a.Name) > 64 {
			return Object{}, ErrAttribute
		}
		for _, c := range []byte(a.Name) {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
				return Object{}, ErrAttribute
			}
		}
		name := strings.ToLower(a.Name)
		switch name {
		case "objectguid", "objectclass", "samaccountname", "useraccountcontrol":
		default:
			return Object{}, ErrAttribute
		}
		if _, exists := attrs[name]; exists {
			return Object{}, ErrAttribute
		}
		attrs[name] = a.Values
	}
	guid := attrs["objectguid"]
	if len(guid) != 1 {
		return Object{}, ErrGUID
	}
	var err error
	out.GUID, err = DecodeGUID(guid[0])
	if err != nil {
		return Object{}, err
	}
	classes := attrs["objectclass"]
	if len(classes) == 0 || len(classes) > 32 {
		return Object{}, ErrAttribute
	}
	seen := make(map[string]bool, len(classes))
	for _, raw := range classes {
		if len(raw) == 0 || len(raw) > 64 {
			return Object{}, ErrAttribute
		}
		for _, c := range raw {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
				return Object{}, ErrAttribute
			}
		}
		value := strings.ToLower(string(raw))
		if seen[value] {
			return Object{}, ErrAttribute
		}
		seen[value] = true
		out.Classes = append(out.Classes, value)
	}
	sort.Strings(out.Classes)
	switch {
	case seen["group"] && (seen["user"] || seen["computer"]):
		return Object{}, ErrKind
	case seen["computer"]:
		out.Kind = Computer
	case seen["group"]:
		out.Kind = Group
	case seen["user"]:
		out.Kind = User
	default:
		return Object{}, ErrKind
	}
	if values, present := attrs["samaccountname"]; present {
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 1024 {
			return Object{}, ErrAttribute
		}
		value := string(values[0])
		if !boundedText(value, 1024) || utf8.RuneCountInString(value) > 256 {
			return Object{}, ErrAttribute
		}
		out.SAMAccountName = &value
	}
	if values, present := attrs["useraccountcontrol"]; present {
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 10 {
			return Object{}, ErrNumber
		}
		value := string(values[0])
		for _, c := range value {
			if c < '0' || c > '9' {
				return Object{}, ErrNumber
			}
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != value {
			return Object{}, ErrNumber
		}
		bits := uint32(n)
		out.UserAccountControl = &bits
	}
	out.DN = e.DN
	return out, nil
}
func cloneObject(o Object) Object {
	o.Classes = append([]string(nil), o.Classes...)
	if o.SAMAccountName != nil {
		v := *o.SAMAccountName
		o.SAMAccountName = &v
	}
	if o.UserAccountControl != nil {
		v := *o.UserAccountControl
		o.UserAccountControl = &v
	}
	return o
}
