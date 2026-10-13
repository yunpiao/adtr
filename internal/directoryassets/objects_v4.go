package directoryassets

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	MaxStoredObjectV4Bytes = 262144
	MaxMemberValuesV4      = 256
	MaxMemberBytesV4       = 4096
	MaxMemberTotalBytesV4  = 65536
)

// ErrMembershipProfile marks a representation outside this bounded raw-member
// profile: ranged attributes, non-group membership, or per-object member caps.
var ErrMembershipProfile = errors.New("unsupported_membership_profile")

type RawMembershipV4 struct {
	MemberBytes [][]byte `json:"memberBytes"`
}
type StoredObjectV4 struct {
	Base         Object            `json:"base"`
	Supplemental RawSupplementalV2 `json:"supplemental"`
	Identity     RawIdentityV3     `json:"identity"`
	Membership   RawMembershipV4   `json:"membership"`
}
type PublicObjectV4 struct {
	PublicObjectV3
	RawMember           []string `json:"rawMember"`
	ReturnedMemberCount *int     `json:"returnedMemberCount"`
	MemberReadState     string   `json:"memberReadState"`
}

func (RawMembershipV4) String() string     { return "directory raw membership v4 (redacted)" }
func (o RawMembershipV4) GoString() string { return o.String() }
func (StoredObjectV4) String() string      { return "directory stored object v4 (redacted)" }
func (o StoredObjectV4) GoString() string  { return o.String() }
func (PublicObjectV4) String() string      { return "directory public object v4 (redacted)" }
func (o PublicObjectV4) GoString() string  { return o.String() }

// IsMemberRangeV4 recognizes the range option independent of attribute order,
// option order, letter case or whether the terminal range is 0-*. No ranges are
// fetched or interpreted. Other malformed/unknown descriptors remain invalid.
func IsMemberRangeV4(name string) bool {
	base, options, _ := strings.Cut(name, ";")
	if !strings.EqualFold(base, "member") {
		return false
	}
	for options != "" {
		option, rest, _ := strings.Cut(options, ";")
		if len(option) >= 6 && strings.EqualFold(option[:6], "range=") {
			return true
		}
		options = rest
	}
	return false
}
func validateMembershipV4(kind Kind, raw RawMembershipV4) error {
	if raw.MemberBytes == nil {
		return nil
	}
	if kind != Group || len(raw.MemberBytes) > MaxMemberValuesV4 {
		return ErrMembershipProfile
	}
	used := 0
	for i, value := range raw.MemberBytes {
		if len(value) > MaxMemberBytesV4 || len(value) > MaxMemberTotalBytesV4-used {
			return ErrMembershipProfile
		}
		used += len(value)
		if len(value) == 0 || !utf8.Valid(value) {
			return ErrAttribute
		}
		for _, prior := range raw.MemberBytes[:i] {
			if bytes.Equal(prior, value) {
				return ErrAttribute
			}
		}
	}
	return nil
}
func DecodeV4(e Entry) (StoredObjectV4, error) {
	// Scan all descriptors first so a plain empty placeholder cannot hide a range.
	for _, a := range e.Attributes {
		if IsMemberRangeV4(a.Name) {
			return StoredObjectV4{}, ErrMembershipProfile
		}
	}
	if len(e.Attributes) < 2 || len(e.Attributes) > 11 {
		return StoredObjectV4{}, ErrEntry
	}
	var core [10]Attribute
	var membership RawMembershipV4
	found, n := false, 0
	for _, a := range e.Attributes {
		if strings.EqualFold(a.Name, "member") {
			if found {
				return StoredObjectV4{}, ErrAttribute
			}
			found = true
			membership.MemberBytes = a.Values
			if membership.MemberBytes == nil {
				membership.MemberBytes = make([][]byte, 0)
			}
		} else {
			if n == len(core) {
				return StoredObjectV4{}, ErrAttribute
			}
			core[n] = a
			n++
		}
	}
	legacy, err := DecodeV3(Entry{DN: e.DN, Attributes: core[:n]})
	if err != nil {
		if found && errors.Is(err, ErrKind) {
			return StoredObjectV4{}, ErrMembershipProfile
		}
		return StoredObjectV4{}, err
	}
	if err = validateMembershipV4(legacy.Base.Kind, membership); err != nil {
		clearStoredObjectV3(&legacy)
		return StoredObjectV4{}, err
	}
	return StoredObjectV4{Base: legacy.Base, Supplemental: legacy.Supplemental, Identity: legacy.Identity, Membership: cloneMembershipV4(membership)}, nil
}
func ProjectV4(o StoredObjectV4) (PublicObjectV4, error) {
	if err := validateMembershipV4(o.Base.Kind, o.Membership); err != nil {
		return PublicObjectV4{}, err
	}
	base, err := ProjectV3(StoredObjectV3{Base: o.Base, Supplemental: o.Supplemental, Identity: o.Identity})
	if err != nil {
		return PublicObjectV4{}, err
	}
	out := PublicObjectV4{PublicObjectV3: base, MemberReadState: "not_returned"}
	if o.Membership.MemberBytes != nil {
		count := len(o.Membership.MemberBytes)
		out.ReturnedMemberCount, out.MemberReadState = &count, "returned"
		out.RawMember = make([]string, count)
		for i, value := range o.Membership.MemberBytes {
			out.RawMember[i] = string(value)
		}
	}
	return out, nil
}
func EncodeStoredObjectV4(o StoredObjectV4) ([]byte, error) {
	if _, err := ProjectV4(o); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, ErrEntry
	}
	if len(raw) > MaxStoredObjectV4Bytes {
		clear(raw)
		return nil, ErrLimit
	}
	return raw, nil
}
func DecodeStoredObjectV4(raw []byte) (out StoredObjectV4, err error) {
	defer func() {
		if err != nil {
			clearStoredObjectV4(&out)
		}
	}()
	if len(raw) == 0 || len(raw) > MaxStoredObjectV4Bytes {
		return out, ErrEntry
	}
	parts, err := exactFieldsV2(raw, []string{"base", "supplemental", "identity", "membership"})
	defer clearJSONPiecesV2(parts)
	if err != nil {
		return out, err
	}
	// Delegate the immutable first three components to the exact v3 codec.
	size := len(parts[0]) + len(parts[1]) + len(parts[2]) + 37
	if size > MaxStoredObjectV3Bytes {
		return out, ErrEntry
	}
	legacy := make([]byte, 0, size)
	legacy = append(legacy, `{"base":`...)
	legacy = append(legacy, parts[0]...)
	legacy = append(legacy, `,"supplemental":`...)
	legacy = append(legacy, parts[1]...)
	legacy = append(legacy, `,"identity":`...)
	legacy = append(legacy, parts[2]...)
	legacy = append(legacy, '}')
	defer clear(legacy)
	prior, e := DecodeStoredObjectV3(legacy)
	if e != nil {
		return out, e
	}
	out.Base, out.Supplemental, out.Identity = prior.Base, prior.Supplemental, prior.Identity
	fields, e := exactFieldsV2(parts[3], []string{"memberBytes"})
	defer clearJSONPiecesV2(fields)
	if e != nil {
		return out, e
	}
	values, e := boundedArrayV2(fields[0], MaxMemberValuesV4, true)
	defer clearJSONPiecesV2(values)
	if e != nil {
		if errors.Is(e, ErrSupplementalLimit) {
			return out, ErrMembershipProfile
		}
		return out, e
	}
	used := 0
	for _, value := range values {
		if bytes.Equal(value, []byte("null")) || preflightBase64V2(value, MaxStoredObjectV4Bytes) != nil {
			return out, ErrAttribute
		}
		size, ok := decodedBase64LengthV2(value[1 : len(value)-1])
		if !ok {
			return out, ErrAttribute
		}
		if size > MaxMemberBytesV4 || size > MaxMemberTotalBytesV4-used {
			return out, ErrMembershipProfile
		}
		used += size
	}
	if values != nil {
		out.Membership.MemberBytes = make([][]byte, len(values))
		for i, value := range values {
			if out.Membership.MemberBytes[i], e = decodeBase64V2(value, MaxMemberBytesV4); e != nil {
				return out, e
			}
		}
	}
	canonical, e := EncodeStoredObjectV4(out)
	defer clear(canonical)
	if e != nil {
		return out, e
	}
	if !bytes.Equal(canonical, raw) {
		return out, ErrEntry
	}
	return out, nil
}
func cloneMembershipV4(o RawMembershipV4) RawMembershipV4 {
	if o.MemberBytes != nil {
		values := make([][]byte, len(o.MemberBytes))
		for i, v := range o.MemberBytes {
			values[i] = bytes.Clone(v)
		}
		o.MemberBytes = values
	}
	return o
}
func cloneStoredObjectV4(o StoredObjectV4) StoredObjectV4 {
	old := cloneStoredObjectV3(StoredObjectV3{Base: o.Base, Supplemental: o.Supplemental, Identity: o.Identity})
	return StoredObjectV4{Base: old.Base, Supplemental: old.Supplemental, Identity: old.Identity, Membership: cloneMembershipV4(o.Membership)}
}
func ClearStoredObjectsV4(objects []StoredObjectV4) {
	for i := range objects {
		clearStoredObjectV4(&objects[i])
	}
}
func clearStoredObjectV4(o *StoredObjectV4) {
	old := StoredObjectV3{Base: o.Base, Supplemental: o.Supplemental, Identity: o.Identity}
	clearStoredObjectV3(&old)
	for _, v := range o.Membership.MemberBytes {
		clear(v)
	}
	clear(o.Membership.MemberBytes)
	*o = StoredObjectV4{}
}
