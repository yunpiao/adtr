package ldapconnection

import (
	"errors"
	"io"
)

const (
	maxMessageBytes = 64 * 1024
	maxValueBytes   = 4096
	maxAttributes   = 4
	maxCapabilities = 64
	maxOIDBytes     = 128
)

var errBER = errors.New("invalid BER")

// This is deliberately not a recursive BER decoder. Each caller recognizes a
// fixed LDAP shape, so nesting depth, allocations and iteration are bounded.
type berReader []byte

func (r *berReader) take() (byte, []byte, error) {
	if len(*r) < 2 {
		return 0, nil, errBER
	}
	tag := (*r)[0]
	if tag&0x1f == 0x1f {
		return 0, nil, errBER
	}
	header, length, err := berLength((*r)[1:])
	if err != nil || length > len(*r)-1-header {
		return 0, nil, errBER
	}
	value := (*r)[1+header : 1+header+length]
	*r = (*r)[1+header+length:]
	return tag, value, nil
}

func (r *berReader) expect(want byte) ([]byte, error) {
	tag, value, err := r.take()
	if err != nil || tag != want {
		return nil, errBER
	}
	return value, nil
}

func berLength(data []byte) (header, length int, err error) {
	if len(data) == 0 {
		return 0, 0, errBER
	}
	if data[0] < 128 {
		return 1, int(data[0]), nil
	}
	count := int(data[0] & 0x7f)
	// LDAP prohibits indefinite lengths. Three bytes already cover the limit.
	if count == 0 || count > 3 || len(data) < 1+count || data[1] == 0 {
		return 0, 0, errBER
	}
	for _, b := range data[1 : 1+count] {
		length = length<<8 | int(b)
	}
	if length < 128 || length > maxMessageBytes {
		return 0, 0, errBER
	}
	return 1 + count, length, nil
}

func number(data []byte) (int, error) {
	if len(data) == 0 || len(data) > 4 || data[0]&0x80 != 0 ||
		(len(data) > 1 && data[0] == 0 && data[1]&0x80 == 0) {
		return 0, errBER
	}
	n := 0
	for _, b := range data {
		n = n<<8 | int(b)
	}
	return n, nil
}

func readMessage(reader io.Reader, expectedID int) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:2]); err != nil {
		return 0, nil, err
	}
	if header[0] != 0x30 {
		return 0, nil, errBER
	}
	count := 0
	if header[1] >= 128 {
		count = int(header[1] & 0x7f)
		if count == 0 || count > 3 {
			return 0, nil, errBER
		}
		if _, err := io.ReadFull(reader, header[2:2+count]); err != nil {
			return 0, nil, err
		}
	}
	_, length, err := berLength(header[1 : 2+count])
	if err != nil || length > maxMessageBytes {
		return 0, nil, errBER
	}
	body := make([]byte, length)
	defer clear(body)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	fields := berReader(body)
	idBytes, err := fields.expect(0x02)
	if err != nil {
		return 0, nil, errBER
	}
	id, err := number(idBytes)
	if err != nil || id != expectedID {
		return 0, nil, errBER
	}
	tag, op, err := fields.take()
	// No controls are requested or accepted by this restricted operation.
	if err != nil || len(fields) != 0 {
		return 0, nil, errBER
	}
	// Return a separately owned operation body; always clear the wire buffer,
	// including malformed, partial and panicking reads. Protocol callers clear
	// the operation body after consuming it.
	owned := make([]byte, len(op))
	copy(owned, op)
	return tag, owned, nil
}

func element(tag byte, parts ...[]byte) []byte {
	length := 0
	for _, part := range parts {
		length += len(part)
	}
	header := []byte{tag}
	switch {
	case length < 128:
		header = append(header, byte(length))
	case length < 256:
		header = append(header, 0x81, byte(length))
	case length < 65536:
		header = append(header, 0x82, byte(length>>8), byte(length))
	default:
		header = append(header, 0x83, byte(length>>16), byte(length>>8), byte(length))
	}
	out := make([]byte, len(header), len(header)+length)
	copy(out, header)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func message(id byte, tag byte, body []byte) []byte {
	return element(0x30, []byte{0x02, 0x01, id}, element(tag, body))
}

func octets(value string) []byte { return element(0x04, []byte(value)) }
