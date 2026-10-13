//go:build integration

package main

import (
	"bytes"
	"fmt"
)

var directoryV4SearchBody = bytes.Join([][]byte{
	octets(directoryBase), integer(0x0a, 2), integer(0x0a, 0),
	integer(0x02, 0), integer(0x02, 3), element(0x01, []byte{0}),
	element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group"))),
	element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl"),
		octets("objectSid"), octets("mail"), octets("description"), octets("whenCreated"), octets("primaryGroupID"), octets("servicePrincipalName"), octets("member")),
}, nil)

func directoryV4Entry(id, row int) []byte {
	kind, number, ou := "user", row, "Users"
	classes := []string{"top", "person", "organizationalPerson", "user"}
	uac := "512"
	if row == 1 {
		uac = "0"
	}
	if row > 12 {
		kind, number, ou = "group", row-12, "Groups"
		classes, uac = []string{"top", "group"}, ""
	}
	if row > 24 {
		kind, number, ou = "computer", row-24, "Computers"
		classes, uac = []string{"top", "person", "organizationalPerson", "user", "computer"}, "4096"
	}
	name := fmt.Sprintf("%s-%02d", kind, number)
	dn := fmt.Sprintf("CN=%s,OU=%s,DC=synthetic,DC=invalid", name, ou)
	attributes := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))),
		directoryAttribute("objectClass", classes...)...)
	if row != 24 {
		sam := name
		if kind == "computer" {
			sam += "$"
		}
		attributes = append(attributes, directoryAttribute("sAMAccountName", sam)...)
	}
	if uac != "" {
		attributes = append(attributes, directoryAttribute("userAccountControl", uac)...)
	}
	if row != 24 {
		attributes = append(attributes, directoryV2Supplemental(row)...)
	}
	if row <= 12 && row != 12 || row > 24 {
		idValue := "513"
		if row == 2 {
			idValue = "0"
		}
		if row == 3 {
			idValue = "4294967295"
		}
		values := []string{fmt.Sprintf("HOST/row-%02d.synthetic.invalid", row), fmt.Sprintf("HTTP/row-%02d.synthetic.invalid", row)}
		if row == 1 {
			values = []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}
		}
		attributes = append(attributes, directoryAttribute("primaryGroupID", idValue)...)
		attributes = append(attributes, directoryAttribute("servicePrincipalName", values...)...)
	}
	if kind == "group" {
		switch row {
		case 13: // Missing descriptor: unknown absence/ACL omission.
		case 14:
			attributes = append(attributes, directoryAttribute("member")...)
		case 15:
			attributes = append(attributes, directoryAttribute("member",
				"CN=user-01,OU=Users,DC=synthetic,DC=invalid",
				"CN=group-02,OU=Groups,DC=synthetic,DC=invalid",
				"CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid",
				"CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid")...)
		default:
			attributes = append(attributes, directoryAttribute("member", fmt.Sprintf("CN=member-%02d,OU=Users,DC=synthetic,DC=invalid", row))...)
		}
	}
	return message(id, 0x64, octets(dn), element(0x30, attributes))
}
