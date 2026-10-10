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

// userAssetsV2Entry is an opt-in, immutable evidence fixture. The legacy and
// dictionary-v2 rows above keep their exact bytes. There are twelve users,
// twelve groups and six computers (whose classes also contain "user").
func userAssetsV2Entry(id, row int) []byte {
	if row != 1 && row != 12 {
		return directoryEntryForDictionary(id, row, true)
	}
	name := fmt.Sprintf("user-%02d", row)
	dn := fmt.Sprintf("CN=%s,OU=Users,DC=synthetic,DC=invalid", name)
	attributes := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))),
		directoryAttribute("objectClass", "top", "person", "organizationalPerson", "user")...)
	if row == 1 {
		// Preserve all accepted hostile/control text and boundary values.
		attributes = append(attributes, directoryAttribute("sAMAccountName", name)...)
		attributes = append(attributes, directoryAttribute("userAccountControl", "0")...)
		sid := []byte{1, 5, 0, 0, 0, 0, 0, 5}
		for _, value := range []uint32{21, 1, 2, 3, 1001} {
			sid = binary.LittleEndian.AppendUint32(sid, value)
		}
		attributes = append(attributes, directoryAttribute("objectSid", string(sid))...)
		attributes = append(attributes, directoryAttribute("mail", " Mixed+%_&*?'\"\\()[]@example.test \x00\n\t😀\u202e")...)
		attributes = append(attributes, directoryAttribute("description", "<img src=x onerror=alert(1)>\x00\n\t😀\u202e")...)
		attributes = append(attributes, directoryAttribute("whenCreated", "00010101000000.0Z")...)
	}
	// Row twelve deliberately has absent SAM/UAC and all supplemental values.
	return message(id, 0x64, octets(dn), element(0x30, attributes))
}
