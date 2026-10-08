//go:build integration

package main

import (
	"errors"
	"io"
)

const maxMessageBytes = 64 * 1024

var errProtocol = errors.New("invalid synthetic LDAP request")

// Only definite-length, single-byte-tag BER is needed by the fixed protocol.
// The parser never recurses and every request allocation is bounded.
type berReader []byte

func berLength(data []byte) (header, length int, err error) {
	if len(data) == 0 {
		return 0, 0, errProtocol
	}
	if data[0] < 128 {
		return 1, int(data[0]), nil
	}
	count := int(data[0] & 0x7f)
	if count == 0 || count > 3 || len(data) < count+1 || data[1] == 0 {
		return 0, 0, errProtocol
	}
	for _, b := range data[1 : count+1] {
		length = length<<8 | int(b)
	}
	if length < 128 || length > maxMessageBytes {
		return 0, 0, errProtocol
	}
	return count + 1, length, nil
}

func (r *berReader) take() (byte, []byte, error) {
	if len(*r) < 2 || (*r)[0]&0x1f == 0x1f {
		return 0, nil, errProtocol
	}
	tag := (*r)[0]
	header, length, err := berLength((*r)[1:])
	if err != nil || length > len(*r)-1-header {
		return 0, nil, errProtocol
	}
	value := (*r)[1+header : 1+header+length]
	*r = (*r)[1+header+length:]
	return tag, value, nil
}

func (r *berReader) expect(tag byte) ([]byte, error) {
	actual, value, err := r.take()
	if err != nil || actual != tag {
		return nil, errProtocol
	}
	return value, nil
}

func number(value []byte) (int, error) {
	if len(value) == 0 || len(value) > 4 || value[0]&0x80 != 0 ||
		(len(value) > 1 && value[0] == 0 && value[1]&0x80 == 0) {
		return 0, errProtocol
	}
	result := 0
	for _, b := range value {
		result = result<<8 | int(b)
	}
	return result, nil
}

func (r *berReader) integer(tag byte) (int, error) {
	value, err := r.expect(tag)
	if err != nil {
		return 0, err
	}
	return number(value)
}

type request struct {
	id       int
	tag      byte
	body     []byte
	controls []byte
	packet   []byte
}

func readRequest(reader io.Reader) (request, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:2]); err != nil {
		return request{}, err
	}
	if header[0] != 0x30 {
		return request{}, errProtocol
	}
	count := 0
	if header[1] >= 128 {
		count = int(header[1] & 0x7f)
		if count == 0 || count > 3 {
			return request{}, errProtocol
		}
		if _, err := io.ReadFull(reader, header[2:2+count]); err != nil {
			return request{}, err
		}
	}
	_, length, err := berLength(header[1 : 2+count])
	if err != nil {
		return request{}, err
	}
	packet := make([]byte, length)
	if _, err := io.ReadFull(reader, packet); err != nil {
		clear(packet)
		return request{}, err
	}
	fields := berReader(packet)
	id, err := fields.integer(0x02)
	if err != nil || id < 1 {
		clear(packet)
		return request{}, errProtocol
	}
	tag, body, err := fields.take()
	if err != nil {
		clear(packet)
		return request{}, errProtocol
	}
	var controls []byte
	if len(fields) != 0 {
		controls, err = fields.expect(0xa0)
		if err != nil || len(controls) == 0 || len(fields) != 0 {
			clear(packet)
			return request{}, errProtocol
		}
	}
	return request{id: id, tag: tag, body: body, controls: controls, packet: packet}, nil
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
	for _, part := range parts {
		header = append(header, part...)
	}
	return header
}

func integer(tag byte, value int) []byte {
	var bytes [5]byte
	i := len(bytes) - 1
	bytes[i] = byte(value)
	for value >>= 8; value > 0; value >>= 8 {
		i--
		bytes[i] = byte(value)
	}
	if bytes[i]&0x80 != 0 {
		i--
	}
	return element(tag, bytes[i:])
}

func message(id int, tag byte, parts ...[]byte) []byte {
	return element(0x30, integer(0x02, id), element(tag, parts...))
}

func octets(value string) []byte { return element(0x04, []byte(value)) }

func writeAll(writer io.Writer, packet []byte) error {
	for len(packet) != 0 {
		n, err := writer.Write(packet)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(packet) {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}

func result(writer io.Writer, id int, tag byte, code int, diagnostic string) error {
	return writeAll(writer, message(id, tag, integer(0x0a, code), octets(""), octets(diagnostic)))
}
