//go:build integration

package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	computerExportMultichunkV3ObjectCount   = 1024
	computerExportMultichunkV3PageEntries   = 256
	computerExportMultichunkV3ComputerCount = 1020
	computerExportMultichunkV3OUCount       = 48
)

// This separate, opt-in synthetic population exercises the existing export
// chunk boundary without changing production limits or the thirty-row fixture.
// Every input is a bounded local row index; there is no external data or seed.
func computerExportMultichunkV3Entry(id, row int) []byte {
	if row < 1 || row > computerExportMultichunkV3ObjectCount {
		return nil
	}
	kind, ou := "computer", ""
	classes := []string{"top", "person", "organizationalPerson", "user", "computer"}
	if row > computerExportMultichunkV3ComputerCount {
		kind, ou = "user", "Users"
		classes = []string{"top", "person", "organizationalPerson", "user"}
	}
	if row > computerExportMultichunkV3ComputerCount+2 {
		kind, ou = "group", "Groups"
		classes = []string{"top", "group"}
	}
	name := fmt.Sprintf("%s-capacity-%04d", kind, row)
	var dn strings.Builder
	dn.WriteString("CN=")
	dn.WriteString(name)
	if kind == "computer" {
		for index := 0; index < computerExportMultichunkV3OUCount; index++ {
			digest := sha256.Sum256([]byte(fmt.Sprintf("adtr-computer-capacity-v1|%04d|%02d", row, index)))
			fmt.Fprintf(&dn, ",OU=%x", digest)
		}
	} else {
		dn.WriteString(",OU=")
		dn.WriteString(ou)
	}
	dn.WriteString(",DC=synthetic,DC=invalid")

	// A 32-bit row ordinal, rather than repeated one-byte GUIDs, remains unique
	// across the 255 boundary and has the frozen AD mixed-endian projection.
	guid := []byte{0, 0, 0, 0, 0x22, 0x11, 0x44, 0x33, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc}
	binary.LittleEndian.PutUint32(guid[:4], uint32(row))
	attributes := append(directoryAttribute("objectGUID", string(guid)), directoryAttribute("objectClass", classes...)...)
	sam := name
	if kind == "computer" {
		sam += "$"
	}
	attributes = append(attributes, directoryAttribute("sAMAccountName", sam)...)
	if kind == "computer" {
		attributes = append(attributes, directoryAttribute("userAccountControl", "4096")...)
	}
	sid := []byte{1, 1, 0, 0, 0, 0, 0, 5, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(sid[8:], uint32(row))
	attributes = append(attributes, directoryAttribute("objectSid", string(sid))...)
	attributes = append(attributes, directoryAttribute("whenCreated", "20260101000000.0Z")...)
	// Mail, description, primaryGroupID and SPNs stay genuinely absent.
	return message(id, 0x64, octets(dn.String()), element(0x30, attributes))
}
