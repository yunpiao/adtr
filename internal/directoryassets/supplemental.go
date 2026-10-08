package directoryassets

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

var (
	// ErrSupplementalLimit denotes an explicit local representation bound,
	// not proof that the source directory value is malformed.
	ErrSupplementalLimit = errors.New("unsupported_directory_supplemental_limit")
	ErrSID               = errors.New("invalid_directory_sid")
	// ErrSIDProfile distinguishes a structurally valid zero-subauthority SID
	// from the canonical textual profile, which requires a subauthority.
	ErrSIDProfile = errors.New("unsupported_directory_sid_profile")
	// ErrWhenCreated denotes invalid dates or digits in the supported shape.
	ErrWhenCreated = errors.New("invalid_directory_creation_time")
	// ErrWhenCreatedProfile does not assert validity of other GeneralizedTime
	// representations; it only says that this component cannot interpret them.
	ErrWhenCreatedProfile = errors.New("unsupported_directory_creation_time_profile")
)

const (
	supplementalSID = iota
	supplementalMail
	supplementalDescription
	supplementalWhenCreated
	supplementalAttributeCount

	maxSupplementalBytes = 68 * 1024
	maxDescriptionValues = 16
	maxMailBytes         = 1024
	maxDescriptionBytes  = 4096
	maxObjectSIDBytes    = 28
	whenCreatedBytes     = 17
)

// Supplemental is an independent, in-memory projection, not part of Object or
// its stored/API dictionary. Nil means not supplied, without claiming why.
// Descriptions preserve multiplicity; class-specific SAM constraints and the
// business scalar/alias mappings must be handled by a future integration.
type Supplemental struct {
	ObjectSID    *string
	Mail         *string
	Descriptions []string
	WhenCreated  *time.Time
}

func (Supplemental) String() string     { return "directory supplemental fields (redacted)" }
func (s Supplemental) GoString() string { return s.String() }

// DecodeSID renders a revision-1 binary Windows SID using a big-endian 48-bit
// authority and little-endian uint32 subauthorities. Exact length and the
// structural limit of 15 subauthorities are checked before allocation. The
// textual profile requires at least one subauthority; this function does not
// impose the narrower AD objectSid attribute's 28-byte bound or infer authority.
func DecodeSID(raw []byte) (string, error) {
	if len(raw) < 8 || len(raw) > 68 || raw[0] != 1 || raw[1] > 15 || len(raw) != 8+4*int(raw[1]) {
		return "", ErrSID
	}
	if raw[1] == 0 {
		return "", ErrSIDProfile
	}
	var authority uint64
	for _, b := range raw[2:8] {
		authority = authority<<8 | uint64(b)
	}
	// At most 183 bytes: prefix, 0x plus 12 hex digits, and 15 uint32s.
	out := make([]byte, 0, 4+14+11*int(raw[1]))
	out = append(out, "S-1-"...)
	if authority < 1<<32 {
		out = strconv.AppendUint(out, authority, 10)
	} else {
		out = append(out, "0x"...)
		out = hex.AppendEncode(out, raw[2:8])
	}
	for offset := 8; offset < len(raw); offset += 4 {
		out = append(out, '-')
		out = strconv.AppendUint(out, uint64(binary.LittleEndian.Uint32(raw[offset:offset+4])), 10)
	}
	return string(out), nil
}

// DecodeSupplemental accepts an explicitly selected set of objectSid, mail,
// description, and whenCreated attributes. It does no I/O or inference. All
// descriptor, count, byte, UTF-16 and aggregate bounds precede value copies;
// successful text and slices are owned copies. Any error returns a zero result.
//
// Mail is limited locally to 256 UTF-16 code units and descriptions to 1024
// units per value, with at most 16 values. Every valid UTF-8 byte is preserved,
// including whitespace and control characters. These conservative local limits
// and this decoder's partial GeneralizedTime profile do not prove AD parity.
func DecodeSupplemental(attributes []Attribute) (Supplemental, error) {
	if len(attributes) > supplementalAttributeCount {
		return Supplemental{}, ErrSupplementalLimit
	}
	var values [supplementalAttributeCount][][]byte
	var seen [supplementalAttributeCount]bool
	total := 0
	for _, a := range attributes {
		field, ok := supplementalField(a.Name)
		if !ok || seen[field] {
			return Supplemental{}, ErrAttribute
		}
		seen[field] = true
		if len(a.Values) == 0 || field != supplementalDescription && len(a.Values) != 1 {
			return Supplemental{}, ErrAttribute
		}
		if field == supplementalDescription && len(a.Values) > maxDescriptionValues {
			return Supplemental{}, ErrSupplementalLimit
		}
		limit := [...]int{maxObjectSIDBytes, maxMailBytes, maxDescriptionBytes, whenCreatedBytes}[field]
		for _, raw := range a.Values {
			if len(raw) > limit {
				if field == supplementalWhenCreated {
					return Supplemental{}, ErrWhenCreatedProfile
				}
				return Supplemental{}, ErrSupplementalLimit
			}
			if len(raw) > maxSupplementalBytes-total {
				return Supplemental{}, ErrSupplementalLimit
			}
			total += len(raw)
			if field == supplementalMail || field == supplementalDescription {
				maxUnits := 256
				if field == supplementalDescription {
					maxUnits = 1024
				}
				if err := supplementalText(raw, maxUnits); err != nil {
					return Supplemental{}, err
				}
			}
		}
		values[field] = a.Values
	}
	var out Supplemental
	if seen[supplementalSID] {
		value, err := DecodeSID(values[supplementalSID][0])
		if err != nil {
			return Supplemental{}, err
		}
		out.ObjectSID = &value
	}
	if seen[supplementalMail] {
		value := string(values[supplementalMail][0])
		out.Mail = &value
	}
	if seen[supplementalDescription] {
		descriptions := values[supplementalDescription]
		for i, raw := range descriptions {
			for _, prior := range descriptions[:i] {
				if bytes.Equal(prior, raw) {
					return Supplemental{}, ErrAttribute
				}
			}
		}
		out.Descriptions = make([]string, len(descriptions))
		for i, raw := range descriptions {
			out.Descriptions[i] = string(raw)
		}
		sort.Strings(out.Descriptions)
	}
	if seen[supplementalWhenCreated] {
		value, err := supplementalCreationTime(values[supplementalWhenCreated][0])
		if err != nil {
			return Supplemental{}, err
		}
		out.WhenCreated = &value
	}
	return out, nil
}

// A fixed stack buffer bounds case folding and excludes Unicode lookalikes,
// descriptor options, OIDs, wildcards and arbitrary input before allocation.
func supplementalField(name string) (int, bool) {
	var lower [11]byte
	if len(name) == 0 || len(name) > len(lower) {
		return 0, false
	}
	for i := range len(name) {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		} else if c < 'a' || c > 'z' {
			return 0, false
		}
		lower[i] = c
	}
	switch string(lower[:len(name)]) {
	case "objectsid":
		return supplementalSID, true
	case "mail":
		return supplementalMail, true
	case "description":
		return supplementalDescription, true
	case "whencreated":
		return supplementalWhenCreated, true
	default:
		return 0, false
	}
}

func supplementalText(raw []byte, maxUnits int) error {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return ErrAttribute
	}
	units := 0
	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		raw = raw[size:]
		units++
		if r > 0xffff {
			units++
		}
		if units > maxUnits {
			return ErrSupplementalLimit
		}
	}
	return nil
}

func supplementalCreationTime(raw []byte) (time.Time, error) {
	if len(raw) == 0 {
		return time.Time{}, ErrWhenCreated
	}
	if len(raw) != whenCreatedBytes || raw[14] != '.' || raw[15] != '0' || raw[16] != 'Z' {
		return time.Time{}, ErrWhenCreatedProfile
	}
	for _, c := range raw[:14] {
		if c < '0' || c > '9' {
			return time.Time{}, ErrWhenCreated
		}
	}
	decimal := func(raw []byte) int {
		value := 0
		for _, c := range raw {
			value = value*10 + int(c-'0')
		}
		return value
	}
	year, month, day := decimal(raw[:4]), decimal(raw[4:6]), decimal(raw[6:8])
	hour, minute, second := decimal(raw[8:10]), decimal(raw[10:12]), decimal(raw[12:14])
	if year == 0 || month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 60 {
		return time.Time{}, ErrWhenCreated
	}
	value := time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
	if value.Year() != year || int(value.Month()) != month || value.Day() != day {
		return time.Time{}, ErrWhenCreated
	}
	// RFC 4517 permits leap-second syntax. It is outside this profile, and
	// must not be normalized by time.Date or labelled a malformed AD value.
	if second == 60 {
		return time.Time{}, ErrWhenCreatedProfile
	}
	return value.Add(time.Duration(second) * time.Second), nil
}
