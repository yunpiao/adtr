//go:build integration

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Dictionary 2 is a separate exact request. It does not widen the legacy
// dictionary or accept caller-selected attributes, filters or search bounds.
var directoryV2SearchBody = bytes.Join([][]byte{
	octets(directoryBase), integer(0x0a, 2), integer(0x0a, 0),
	integer(0x02, 0), integer(0x02, 3), element(0x01, []byte{0}),
	element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group"))),
	element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"),
		octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated")),
}, nil)

func directoryV2Supplemental(row int) []byte {
	// S-1-5-21-1-2-3-(1000+row): authority is big endian, subauthorities
	// are little endian. These identifiers describe synthetic fixtures only.
	sid := []byte{1, 5, 0, 0, 0, 0, 0, 5}
	for _, subauthority := range []uint32{21, 1, 2, 3, uint32(1000 + row)} {
		sid = binary.LittleEndian.AppendUint32(sid, subauthority)
	}
	mail := fmt.Sprintf("row-%02d@example.test", row)
	description := fmt.Sprintf("Synthetic row %02d", row)
	created := "20261001020304.0Z"
	if row == 1 {
		mail = " Mixed@example.test "
		description = "Line 1\x00\n\t😀\u202e"
		created = "00010101000000.0Z"
	}
	return bytes.Join([][]byte{
		directoryAttribute("objectSid", string(sid)),
		directoryAttribute("mail", mail),
		directoryAttribute("description", description),
		directoryAttribute("whenCreated", created),
	}, nil)
}
