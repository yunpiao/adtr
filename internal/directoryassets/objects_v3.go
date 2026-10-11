package directoryassets

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

const (
	MaxStoredObjectV3Bytes = 128 << 10
	MaxSPNValuesV3         = 128
	MaxSPNBytesV3          = 4096
	MaxSPNTotalBytesV3     = 32768
)

// ErrPrimaryGroupProfile denotes the supported unsigned-decimal RID profile,
// not a judgment about another LDAP integer representation.
var ErrPrimaryGroupProfile = errors.New("unsupported_primary_group_profile")

type RawIdentityV3 struct {
	PrimaryGroupIDBytes       []byte   `json:"primaryGroupIDBytes"`
	ServicePrincipalNameBytes [][]byte `json:"servicePrincipalNameBytes"`
}

// Separate closed storage DTO; the immutable v2 DTO and codecs stay unchanged.
type StoredObjectV3 struct {
	Base         Object            `json:"base"`
	Supplemental RawSupplementalV2 `json:"supplemental"`
	Identity     RawIdentityV3     `json:"identity"`
}

type PublicObjectV3 struct {
	PublicObjectV2
	PrimaryGroupID       *uint32  `json:"primaryGroupID"`
	ServicePrincipalName []string `json:"servicePrincipalName"`
}

func (RawIdentityV3) String() string      { return "directory raw identity v3 (redacted)" }
func (o RawIdentityV3) GoString() string  { return o.String() }
func (StoredObjectV3) String() string     { return "directory stored object v3 (redacted)" }
func (o StoredObjectV3) GoString() string { return o.String() }
func (PublicObjectV3) String() string     { return "directory public object v3 (redacted)" }
func (o PublicObjectV3) GoString() string { return o.String() }

func fieldV3(name string) (int, bool) {
	if f, ok := fieldV2(name); ok {
		return f, true
	}
	if len(name) > 20 || len(name) == 0 {
		return 0, false
	}
	for _, c := range []byte(name) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return 0, false
		}
	}
	switch strings.ToLower(name) {
	case "primarygroupid":
		return 8, true
	case "serviceprincipalname":
		return 9, true
	}
	return 0, false
}

func primaryGroupV3(raw []byte) (*uint32, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) < 1 || len(raw) > 10 || len(raw) > 1 && raw[0] == '0' {
		return nil, ErrPrimaryGroupProfile
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return nil, ErrPrimaryGroupProfile
		}
	}
	n, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil {
		return nil, ErrPrimaryGroupProfile
	}
	v := uint32(n)
	return &v, nil
}

// Validation precedes any retained value/string copies. Receive order is only
// provenance; no SPN matching, grammar, group membership or risk is inferred.
func validateIdentityV3(raw RawIdentityV3) error {
	if _, err := primaryGroupV3(raw.PrimaryGroupIDBytes); err != nil {
		return err
	}
	values := raw.ServicePrincipalNameBytes
	if values == nil {
		return nil
	}
	if len(values) < 1 {
		return ErrAttribute
	}
	if len(values) > MaxSPNValuesV3 {
		return ErrSupplementalLimit
	}
	used := 0
	for i, value := range values {
		if len(value) > MaxSPNBytesV3 || len(value) > MaxSPNTotalBytesV3-used {
			return ErrSupplementalLimit
		}
		used += len(value)
		if err := supplementalText(value, 1024); err != nil {
			return err
		}
		for _, prior := range values[:i] {
			if bytes.Equal(prior, value) {
				return ErrAttribute
			}
		}
	}
	return nil
}

func DecodeV3(e Entry) (StoredObjectV3, error) {
	if len(e.Attributes) < 2 || len(e.Attributes) > 10 {
		return StoredObjectV3{}, ErrEntry
	}
	var seen [10]bool
	var core [8]Attribute
	n := 0
	var identity RawIdentityV3
	for _, a := range e.Attributes {
		f, ok := fieldV3(a.Name)
		if !ok || seen[f] {
			return StoredObjectV3{}, ErrAttribute
		}
		seen[f] = true
		switch f {
		case 8:
			if len(a.Values) != 1 || len(a.Values[0]) == 0 {
				return StoredObjectV3{}, ErrAttribute
			}
			identity.PrimaryGroupIDBytes = a.Values[0]
		case 9:
			if len(a.Values) == 0 {
				return StoredObjectV3{}, ErrAttribute
			}
			identity.ServicePrincipalNameBytes = a.Values
		default:
			core[n] = a
			n++
		}
	}
	if err := validateIdentityV3(identity); err != nil {
		return StoredObjectV3{}, err
	}
	legacy, err := DecodeV2(Entry{DN: e.DN, Attributes: core[:n]})
	if err != nil {
		return StoredObjectV3{}, err
	}
	out := StoredObjectV3{Base: legacy.Base, Supplemental: legacy.Supplemental, Identity: cloneIdentityV3(identity)}
	return out, nil
}

func ProjectV3(o StoredObjectV3) (PublicObjectV3, error) {
	if err := validateIdentityV3(o.Identity); err != nil {
		return PublicObjectV3{}, err
	}
	base, err := ProjectV2(StoredObjectV2{Base: o.Base, Supplemental: o.Supplemental})
	if err != nil {
		return PublicObjectV3{}, err
	}
	id, _ := primaryGroupV3(o.Identity.PrimaryGroupIDBytes)
	out := PublicObjectV3{PublicObjectV2: base, PrimaryGroupID: id}
	if o.Identity.ServicePrincipalNameBytes != nil {
		out.ServicePrincipalName = make([]string, len(o.Identity.ServicePrincipalNameBytes))
		for i, v := range o.Identity.ServicePrincipalNameBytes {
			out.ServicePrincipalName[i] = string(v)
		}
	}
	return out, nil
}

func EncodeStoredObjectV3(o StoredObjectV3) ([]byte, error) {
	if _, err := ProjectV3(o); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, ErrEntry
	}
	if len(raw) > MaxStoredObjectV3Bytes {
		clear(raw)
		return nil, ErrLimit
	}
	return raw, nil
}

func DecodeStoredObjectV3(raw []byte) (out StoredObjectV3, err error) {
	defer func() {
		if err != nil {
			clearStoredObjectV3(&out)
		}
	}()
	if len(raw) == 0 || len(raw) > MaxStoredObjectV3Bytes {
		return out, ErrEntry
	}
	parts, err := exactFieldsV2(raw, []string{"base", "supplemental", "identity"})
	defer clearJSONPiecesV2(parts)
	if err != nil {
		return out, err
	}
	if len(parts[0]) > 32768 || len(parts[1]) > MaxStoredObjectV2Bytes || len(parts[0])+len(parts[1])+27 > MaxStoredObjectV2Bytes {
		return out, ErrEntry
	}
	// Feed the unchanged strict v2 component codec an exact bounded wrapper.
	legacy := make([]byte, 0, len(parts[0])+len(parts[1])+27)
	legacy = append(legacy, `{"base":`...)
	legacy = append(legacy, parts[0]...)
	legacy = append(legacy, `,"supplemental":`...)
	legacy = append(legacy, parts[1]...)
	legacy = append(legacy, '}')
	defer clear(legacy)
	base, e := DecodeStoredObjectV2(legacy)
	if e != nil {
		return out, e
	}
	out.Base = base.Base
	out.Supplemental = base.Supplemental
	fields, e := exactFieldsV2(parts[2], []string{"primaryGroupIDBytes", "servicePrincipalNameBytes"})
	defer clearJSONPiecesV2(fields)
	if e != nil {
		return out, e
	}
	if e = preflightBase64V2(fields[0], 10); e != nil {
		return out, e
	}
	values, e := boundedArrayV2(fields[1], MaxSPNValuesV3, true)
	defer clearJSONPiecesV2(values)
	if e != nil {
		return out, e
	}
	used := 0
	for _, v := range values {
		if bytes.Equal(v, []byte("null")) || preflightBase64V2(v, MaxSPNBytesV3) != nil {
			return out, ErrAttribute
		}
		size, ok := decodedBase64LengthV2(v[1 : len(v)-1])
		if !ok || size > MaxSPNTotalBytesV3-used {
			return out, ErrSupplementalLimit
		}
		used += size
	}
	if out.Identity.PrimaryGroupIDBytes, e = decodeBase64V2(fields[0], 10); e != nil {
		return out, e
	}
	if values != nil {
		out.Identity.ServicePrincipalNameBytes = make([][]byte, len(values))
		for i, v := range values {
			if out.Identity.ServicePrincipalNameBytes[i], e = decodeBase64V2(v, MaxSPNBytesV3); e != nil {
				return out, e
			}
		}
	}
	canonical, e := EncodeStoredObjectV3(out)
	defer clear(canonical)
	if e != nil {
		return out, e
	}
	if !bytes.Equal(canonical, raw) {
		return out, ErrEntry
	}
	return out, nil
}

func cloneIdentityV3(o RawIdentityV3) RawIdentityV3 {
	o.PrimaryGroupIDBytes = bytes.Clone(o.PrimaryGroupIDBytes)
	if o.ServicePrincipalNameBytes != nil {
		values := make([][]byte, len(o.ServicePrincipalNameBytes))
		for i, v := range o.ServicePrincipalNameBytes {
			values[i] = bytes.Clone(v)
		}
		o.ServicePrincipalNameBytes = values
	}
	return o
}
func cloneStoredObjectV3(o StoredObjectV3) StoredObjectV3 {
	old := cloneStoredObjectV2(StoredObjectV2{Base: o.Base, Supplemental: o.Supplemental})
	return StoredObjectV3{Base: old.Base, Supplemental: old.Supplemental, Identity: cloneIdentityV3(o.Identity)}
}
func ClearStoredObjectsV3(objects []StoredObjectV3) {
	for i := range objects {
		clearStoredObjectV3(&objects[i])
	}
}
func clearStoredObjectV3(o *StoredObjectV3) {
	old := StoredObjectV2{Base: o.Base, Supplemental: o.Supplemental}
	clearStoredObjectV2(&old)
	clear(o.Identity.PrimaryGroupIDBytes)
	for _, v := range o.Identity.ServicePrincipalNameBytes {
		clear(v)
	}
	clear(o.Identity.ServicePrincipalNameBytes)
	*o = StoredObjectV3{}
}
