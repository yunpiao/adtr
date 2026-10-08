package directoryassets

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// MaxStoredObjectV2Bytes is a local safety limit on a single stored JSON object.
// Observation envelopes, source metadata and aggregate limits belong to callers.
const MaxStoredObjectV2Bytes = 64 << 10

// RawSupplementalV2 preserves the original directory bytes. JSON represents
// bytes as padded base64, which is encoding, not encryption. Nil means omitted;
// nonnil empty values are invalid and must never be collapsed into omission.
type RawSupplementalV2 struct {
	ObjectSIDBytes   []byte   `json:"objectSidBytes"`
	MailBytes        []byte   `json:"mailBytes"`
	DescriptionBytes [][]byte `json:"descriptionBytes"`
	WhenCreatedBytes []byte   `json:"whenCreatedBytes"`
}

// StoredObjectV2 deliberately does not widen the immutable dictionary-1 Object.
// Its base64 supplemental fields keep valid NUL-containing text out of JSONB
// strings. Storage readers must use DecodeStoredObjectV2, not json.Unmarshal.
type StoredObjectV2 struct {
	Base         Object            `json:"base"`
	Supplemental RawSupplementalV2 `json:"supplemental"`
}

// PublicObjectV2 is the flat ten-field public projection, never a storage DTO.
// WhenCreated is UTC whole seconds, including a present year 0001 value.
type PublicObjectV2 struct {
	Object
	ObjectSID   *string  `json:"objectSid"`
	Mail        *string  `json:"mail"`
	Description []string `json:"description"`
	WhenCreated *string  `json:"whenCreated"`
}

func (RawSupplementalV2) String() string     { return "directory raw supplemental fields v2 (redacted)" }
func (r RawSupplementalV2) GoString() string { return r.String() }
func (StoredObjectV2) String() string        { return "directory stored object v2 (redacted)" }
func (o StoredObjectV2) GoString() string    { return o.String() }
func (PublicObjectV2) String() string        { return "directory public object v2 (redacted)" }
func (o PublicObjectV2) GoString() string    { return o.String() }

// DecodeV2 accepts only the fixed eight-attribute principal dictionary. It
// validates the entire descriptor set before splitting it between the unchanged
// core and supplemental decoders. All successful mutable values are owned.
func DecodeV2(e Entry) (StoredObjectV2, error) {
	if len(e.Attributes) < 2 || len(e.Attributes) > 8 {
		return StoredObjectV2{}, ErrEntry
	}
	var seen [8]bool
	var core, supplemental [4]Attribute
	coreCount, supplementalCount := 0, 0
	for _, a := range e.Attributes {
		field, ok := fieldV2(a.Name)
		if !ok || seen[field] {
			return StoredObjectV2{}, ErrAttribute
		}
		seen[field] = true
		if field < 4 {
			core[coreCount] = a
			coreCount++
		} else {
			supplemental[supplementalCount] = a
			supplementalCount++
		}
	}
	base, err := Decode(Entry{DN: e.DN, Attributes: core[:coreCount]})
	if err != nil {
		return StoredObjectV2{}, err
	}
	attrs := supplemental[:supplementalCount]
	if _, err := decodePrincipalSupplementalV2(attrs); err != nil {
		return StoredObjectV2{}, err
	}
	out := StoredObjectV2{Base: base}
	for _, a := range attrs {
		field, _ := supplementalField(a.Name)
		switch field {
		case supplementalSID:
			out.Supplemental.ObjectSIDBytes = bytes.Clone(a.Values[0])
		case supplementalMail:
			out.Supplemental.MailBytes = bytes.Clone(a.Values[0])
		case supplementalDescription:
			out.Supplemental.DescriptionBytes = [][]byte{bytes.Clone(a.Values[0])}
		case supplementalWhenCreated:
			out.Supplemental.WhenCreatedBytes = bytes.Clone(a.Values[0])
		}
	}
	return out, nil
}

func fieldV2(name string) (int, bool) {
	var lower [18]byte
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
	case "objectguid":
		return 0, true
	case "objectclass":
		return 1, true
	case "samaccountname":
		return 2, true
	case "useraccountcontrol":
		return 3, true
	case "objectsid":
		return 4, true
	case "mail":
		return 5, true
	case "description":
		return 6, true
	case "whencreated":
		return 7, true
	default:
		return 0, false
	}
}

func decodePrincipalSupplementalV2(attrs []Attribute) (Supplemental, error) {
	for _, a := range attrs {
		if field, ok := supplementalField(a.Name); ok && field == supplementalDescription && len(a.Values) > 1 {
			return Supplemental{}, ErrSupplementalLimit
		}
	}
	return DecodeSupplemental(attrs)
}

func supplementalAttributesV2(raw RawSupplementalV2) []Attribute {
	attrs := make([]Attribute, 0, 4)
	if raw.ObjectSIDBytes != nil {
		attrs = append(attrs, Attribute{"objectSid", [][]byte{raw.ObjectSIDBytes}})
	}
	if raw.MailBytes != nil {
		attrs = append(attrs, Attribute{"mail", [][]byte{raw.MailBytes}})
	}
	if raw.DescriptionBytes != nil {
		attrs = append(attrs, Attribute{"description", raw.DescriptionBytes})
	}
	if raw.WhenCreatedBytes != nil {
		attrs = append(attrs, Attribute{"whenCreated", [][]byte{raw.WhenCreatedBytes}})
	}
	return attrs
}

// validateBaseV2 reconstructs only the original four core attributes, then uses
// Decode to check canonical GUID, class order/case, kind and optional values.
// Domain membership is intentionally a separate caller responsibility.
func validateBaseV2(o Object) error {
	if len(o.GUID) != 36 || o.GUID[8] != '-' || o.GUID[13] != '-' || o.GUID[18] != '-' || o.GUID[23] != '-' || len(o.DN) > 4096 || len(o.Classes) < 1 || len(o.Classes) > 32 {
		return ErrEntry
	}
	for _, class := range o.Classes {
		if len(class) > 64 {
			return ErrEntry
		}
	}
	if o.SAMAccountName != nil && len(*o.SAMAccountName) > 1024 {
		return ErrEntry
	}
	guid, err := hex.DecodeString(strings.ReplaceAll(o.GUID, "-", ""))
	defer clear(guid)
	if err != nil || len(guid) != 16 {
		return ErrEntry
	}
	binary.LittleEndian.PutUint32(guid[:4], binary.BigEndian.Uint32(guid[:4]))
	binary.LittleEndian.PutUint16(guid[4:6], binary.BigEndian.Uint16(guid[4:6]))
	binary.LittleEndian.PutUint16(guid[6:8], binary.BigEndian.Uint16(guid[6:8]))
	classes := make([][]byte, len(o.Classes))
	defer func() {
		for _, value := range classes {
			clear(value)
		}
	}()
	for i, class := range o.Classes {
		classes[i] = []byte(class)
	}
	e := Entry{DN: o.DN, Attributes: []Attribute{{"objectGUID", [][]byte{guid}}, {"objectClass", classes}}}
	if o.SAMAccountName != nil {
		value := []byte(*o.SAMAccountName)
		defer clear(value)
		e.Attributes = append(e.Attributes, Attribute{"sAMAccountName", [][]byte{value}})
	}
	if o.UserAccountControl != nil {
		value := []byte(strconv.FormatUint(uint64(*o.UserAccountControl), 10))
		defer clear(value)
		e.Attributes = append(e.Attributes, Attribute{"userAccountControl", [][]byte{value}})
	}
	decoded, err := Decode(e)
	if err != nil || !reflect.DeepEqual(decoded, o) {
		return ErrEntry
	}
	return nil
}

// ProjectV2 revalidates even caller-constructed stored values and returns an
// independent public projection. It never reconstructs binary SID from text.
func ProjectV2(o StoredObjectV2) (PublicObjectV2, error) {
	if err := validateBaseV2(o.Base); err != nil {
		return PublicObjectV2{}, err
	}
	s, err := decodePrincipalSupplementalV2(supplementalAttributesV2(o.Supplemental))
	if err != nil {
		return PublicObjectV2{}, err
	}
	out := PublicObjectV2{Object: cloneObject(o.Base), ObjectSID: s.ObjectSID, Mail: s.Mail, Description: s.Descriptions}
	if s.WhenCreated != nil {
		value := s.WhenCreated.Format("2006-01-02T15:04:05Z")
		out.WhenCreated = &value
	}
	return out, nil
}

// EncodeStoredObjectV2 validates before encoding the canonical storage DTO.
func EncodeStoredObjectV2(o StoredObjectV2) ([]byte, error) {
	if _, err := ProjectV2(o); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, ErrEntry
	}
	if len(raw) > MaxStoredObjectV2Bytes {
		clear(raw)
		return nil, ErrLimit
	}
	return raw, nil
}

// DecodeStoredObjectV2 rejects unknown/duplicate keys and noncanonical JSON or
// base64. The total JSON, each raw piece, array cardinality and encoded lengths
// are bounded before typed or binary allocations; decoded lengths and complete
// dictionary semantics are then checked. Failure returns the zero object.
func DecodeStoredObjectV2(raw []byte) (out StoredObjectV2, err error) {
	defer func() {
		if err != nil {
			clearStoredObjectV2(&out)
		}
	}()
	if len(raw) == 0 || len(raw) > MaxStoredObjectV2Bytes {
		return out, ErrEntry
	}
	parts, err := exactFieldsV2(raw, []string{"base", "supplemental"})
	defer clearJSONPiecesV2(parts)
	if err != nil || len(parts[0]) > 32768 {
		return out, ErrEntry
	}
	if err := preflightBaseV2(parts[0]); err != nil {
		return out, err
	}
	fields, err := exactFieldsV2(parts[1], []string{"objectSidBytes", "mailBytes", "descriptionBytes", "whenCreatedBytes"})
	defer clearJSONPiecesV2(fields)
	if err != nil {
		return out, err
	}
	descriptions, err := boundedArrayV2(fields[2], 1, true)
	defer clearJSONPiecesV2(descriptions)
	if err != nil {
		return out, err
	}
	encoded := []json.RawMessage{fields[0], fields[1], fields[3]}
	limits := []int{maxObjectSIDBytes, maxMailBytes, whenCreatedBytes}
	for i, value := range encoded {
		if err := preflightBase64V2(value, limits[i]); err != nil {
			return out, err
		}
	}
	for _, value := range descriptions {
		if bytes.Equal(value, []byte("null")) || preflightBase64V2(value, maxDescriptionBytes) != nil {
			return out, ErrAttribute
		}
	}
	if json.Unmarshal(parts[0], &out.Base) != nil {
		return out, ErrEntry
	}
	if err = validateBaseV2(out.Base); err != nil {
		return out, err
	}
	targets := []*[]byte{&out.Supplemental.ObjectSIDBytes, &out.Supplemental.MailBytes, &out.Supplemental.WhenCreatedBytes}
	for i, value := range encoded {
		if *targets[i], err = decodeBase64V2(value, limits[i]); err != nil {
			return out, err
		}
	}
	if descriptions != nil {
		out.Supplemental.DescriptionBytes = make([][]byte, len(descriptions))
		for i, value := range descriptions {
			if out.Supplemental.DescriptionBytes[i], err = decodeBase64V2(value, maxDescriptionBytes); err != nil {
				return out, err
			}
		}
	}
	canonical, err := EncodeStoredObjectV2(out)
	if err != nil {
		return out, err
	}
	defer clear(canonical)
	if !bytes.Equal(canonical, raw) {
		return out, ErrEntry
	}
	return out, nil
}

// exactFieldsV2 allocates only bounded raw JSON pieces, never typed values.
func exactFieldsV2(raw []byte, names []string) (out []json.RawMessage, returnedErr error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrEntry
	}
	fields := make([]json.RawMessage, len(names))
	defer func() {
		if returnedErr != nil {
			clearJSONPiecesV2(fields)
		}
	}()
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, ErrEntry
		}
		name, ok := token.(string)
		index := -1
		if ok {
			for i, allowed := range names {
				if name == allowed {
					index = i
					break
				}
			}
		}
		if index < 0 || fields[index] != nil || d.Decode(&fields[index]) != nil {
			return nil, ErrEntry
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrEntry
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrEntry
	}
	for _, value := range fields {
		if value == nil {
			return nil, ErrEntry
		}
	}
	return fields, nil
}

func boundedArrayV2(raw []byte, limit int, nullable bool) (out []json.RawMessage, returnedErr error) {
	if nullable && bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return nil, ErrEntry
	}
	values := make([]json.RawMessage, 0, limit)
	defer func() {
		if returnedErr != nil {
			clearJSONPiecesV2(values)
		}
	}()
	for d.More() {
		if len(values) >= limit {
			return nil, ErrSupplementalLimit
		}
		var piece json.RawMessage
		if d.Decode(&piece) != nil {
			clear(piece)
			return nil, ErrEntry
		}
		values = append(values, piece)
	}
	if token, err = d.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrEntry
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrEntry
	}
	return values, nil
}

func preflightBaseV2(raw []byte) error {
	fields, err := exactFieldsV2(raw, []string{"objectGUID", "distinguishedName", "kind", "objectClass", "samAccountName", "userAccountControl"})
	defer clearJSONPiecesV2(fields)
	if err != nil {
		return err
	}
	// A byte can expand to at most six bytes in a JSON string (e.g. \u0000).
	for _, bound := range []struct{ index, size int }{{0, 36}, {1, 4096}, {2, 8}, {4, 1024}} {
		value := fields[bound.index]
		if bound.index == 4 && bytes.Equal(value, []byte("null")) {
			continue
		}
		if len(value) < 2 || len(value) > 6*bound.size+2 || value[0] != '"' || value[len(value)-1] != '"' {
			return ErrEntry
		}
	}
	if len(fields[5]) > 10 {
		return ErrEntry
	}
	classes, err := boundedArrayV2(fields[3], 32, false)
	defer clearJSONPiecesV2(classes)
	if err != nil || len(classes) == 0 {
		return ErrEntry
	}
	for _, class := range classes {
		if len(class) < 2 || len(class) > 64*6+2 || class[0] != '"' || class[len(class)-1] != '"' {
			return ErrEntry
		}
	}
	return nil
}

func preflightBase64V2(raw []byte, limit int) error {
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if len(raw) < 2 || len(raw) > base64.StdEncoding.EncodedLen(limit)+2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return ErrAttribute
	}
	for _, c := range raw[1 : len(raw)-1] {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return ErrAttribute
		}
	}
	if size, ok := decodedBase64LengthV2(raw[1 : len(raw)-1]); !ok || size > limit {
		return ErrAttribute
	}
	return nil
}

func decodedBase64LengthV2(encoded []byte) (int, bool) {
	if len(encoded)%4 != 0 {
		return 0, false
	}
	padding := 0
	if len(encoded) > 0 && encoded[len(encoded)-1] == '=' {
		padding++
		if encoded[len(encoded)-2] == '=' {
			padding++
		}
	}
	for _, c := range encoded[:len(encoded)-padding] {
		if c == '=' {
			return 0, false
		}
	}
	return base64.StdEncoding.DecodedLen(len(encoded)) - padding, true
}

func decodeBase64V2(raw []byte, limit int) ([]byte, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	encoded := raw[1 : len(raw)-1]
	size, ok := decodedBase64LengthV2(encoded)
	if !ok || size > limit {
		return nil, ErrAttribute
	}
	decoded := make([]byte, size)
	n, err := base64.StdEncoding.Strict().Decode(decoded, encoded)
	if err != nil || n > limit {
		clear(decoded)
		return nil, ErrAttribute
	}
	return decoded[:n], nil
}

// cloneStoredObjectV2 copies an already-validated, bounded object independently.
// It is not a validation path. Slice presence is preserved without normalization.
func cloneStoredObjectV2(o StoredObjectV2) StoredObjectV2 {
	o.Base = cloneObject(o.Base)
	o.Supplemental.ObjectSIDBytes = bytes.Clone(o.Supplemental.ObjectSIDBytes)
	o.Supplemental.MailBytes = bytes.Clone(o.Supplemental.MailBytes)
	o.Supplemental.WhenCreatedBytes = bytes.Clone(o.Supplemental.WhenCreatedBytes)
	if o.Supplemental.DescriptionBytes != nil {
		values := make([][]byte, len(o.Supplemental.DescriptionBytes))
		for i, value := range o.Supplemental.DescriptionBytes {
			values[i] = bytes.Clone(value)
		}
		o.Supplemental.DescriptionBytes = values
	}
	return o
}

func clearJSONPiecesV2(pieces []json.RawMessage) {
	for _, piece := range pieces {
		clear(piece)
	}
	clear(pieces)
}

// ClearStoredObjectsV2 clears all owned raw byte buffers and drops each object.
// Callers must own these values exclusively; immutable Go strings cannot be
// scrubbed, and aliases deliberately made by callers observe cleared buffers.
func ClearStoredObjectsV2(objects []StoredObjectV2) {
	for i := range objects {
		clearStoredObjectV2(&objects[i])
	}
}

func clearStoredObjectV2(o *StoredObjectV2) {
	clear(o.Supplemental.ObjectSIDBytes)
	clear(o.Supplemental.MailBytes)
	for _, value := range o.Supplemental.DescriptionBytes {
		clear(value)
	}
	clear(o.Supplemental.DescriptionBytes)
	clear(o.Supplemental.WhenCreatedBytes)
	*o = StoredObjectV2{}
}
