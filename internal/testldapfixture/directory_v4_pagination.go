//go:build integration

package main

import (
	"bytes"
	"fmt"
)

// Separate test-only dataset: existing dictionary1/2/3/4 bytes remain untouched.
// 26 groups cross the UI's 25-row boundary; four non-groups prove filtering.
func groupMembersV4PaginationEntry(id, row int) []byte {
	kind, number, ou := "group", row, "Groups"
	classes := []string{"top", "group"}
	if row > 26 {
		kind, number, ou = "user", row-26, "Users"
		classes = []string{"top", "person", "organizationalPerson", "user"}
	}
	if row > 28 {
		kind, number, ou = "computer", row-28, "Computers"
		classes = []string{"top", "person", "organizationalPerson", "user", "computer"}
	}
	name := fmt.Sprintf("pagination-%s-%02d", kind, number)
	sam := name
	if kind == "computer" {
		sam += "$"
	}
	attrs := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))), directoryAttribute("objectClass", classes...)...)
	attrs = append(attrs, directoryAttribute("sAMAccountName", sam)...)
	if kind == "group" {
		switch row {
		case 1: // not returned
		case 2:
			attrs = append(attrs, directoryAttribute("member")...)
		case 26:
			attrs = append(attrs, directoryAttribute("member", "CN=page-two-only,OU=Users,DC=synthetic,DC=invalid", "CN=raw\x00\n😀\u202e<script>,DC=synthetic,DC=invalid", "\\u0000", "CN=page-two-last,OU=Users,DC=synthetic,DC=invalid")...)
		default:
			attrs = append(attrs, directoryAttribute("member", fmt.Sprintf("CN=page-member-%02d,OU=Users,DC=synthetic,DC=invalid", row))...)
		}
	}
	return message(id, 0x64, octets(fmt.Sprintf("CN=%s,OU=%s,DC=synthetic,DC=invalid", name, ou)), element(0x30, attrs))
}
