//go:build integration

package main

import (
	"bytes"
	"fmt"
)

// Dedicated immutable thirty-row mode: 23 groups, three users, four computers.
// Distractor computers inherit user; group rows carry only group classes.
// U+FFFE is valid UTF-8 and permitted by the directory codec, but forbidden in XML.
const groupExportFailureV3SAM = "group-invalid\ufffe"

func groupAssetsV3Entry(id, row int) []byte {
	return groupAssetsV3EntryWithExportFailure(id, row, false)
}

func groupAssetsV3EntryWithExportFailure(id, row int, exportFailure bool) []byte {
	kind, ou := "group", "Groups"
	classes := []string{"top", "group"}
	if row > 23 {
		kind, ou = "user", "Users"
		classes = []string{"top", "person", "organizationalPerson", "user"}
	}
	if row > 26 {
		kind, ou = "computer", "Computers"
		classes = []string{"top", "person", "organizationalPerson", "user", "computer"}
	}
	name := fmt.Sprintf("%s-%02d", kind, row)
	dn := fmt.Sprintf("CN=%s,OU=%s,DC=synthetic,DC=invalid", name, ou)
	attrs := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))), directoryAttribute("objectClass", classes...)...)
	if row != 23 {
		sam := name
		if exportFailure && row == 1 {
			sam = groupExportFailureV3SAM
		}
		if row == 20 {
			sam = "İΣ𐐀 literal.*"
		}
		if kind == "computer" {
			sam += "$"
		}
		attrs = append(attrs, directoryAttribute("sAMAccountName", sam)...)
	}
	if row <= 23 {
		uac := "4096"
		if row == 1 {
			uac = "0"
		}
		attrs = append(attrs, directoryAttribute("userAccountControl", uac)...)
	}
	if row != 23 {
		sid := []byte{1, 1, 0, 0, 0, 0, 0, 5, byte(row), 0, 0, 0}
		attrs = append(attrs, directoryAttribute("objectSid", string(sid))...)
		attrs = append(attrs, directoryAttribute("mail", "mail-only@example.test")...)
		attrs = append(attrs, directoryAttribute("description", "observed\x00\n<script>😀")...)
		created := "20260101000000.0Z"
		if row == 1 {
			created = "00010101000000.0Z"
		}
		attrs = append(attrs, directoryAttribute("whenCreated", created)...)
		rid := "515"
		if row == 1 {
			rid = "0"
		}
		attrs = append(attrs, directoryAttribute("primaryGroupID", rid)...)
		spns := []string{fmt.Sprintf("HOST/group-%02d.synthetic.invalid", row), "HTTP/fixture"}
		if row == 1 {
			spns = []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}
		}
		attrs = append(attrs, directoryAttribute("servicePrincipalName", spns...)...)
	}
	return message(id, 0x64, octets(dn), element(0x30, attrs))
}
