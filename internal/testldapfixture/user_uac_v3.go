//go:build integration

package main

import (
	"bytes"
	"fmt"
)

// This profile is synthetic evidence input, not a production directory option.
// The empty default preserves the original dictionary and allocates no recorder.
type userUACV3Profile string

const (
	userUACNone    userUACV3Profile = ""
	userUACMixed33 userUACV3Profile = "mixed-33-v1"
)

func parseUserUACV3Profile(value string) (userUACV3Profile, error) {
	switch userUACV3Profile(value) {
	case userUACNone, userUACMixed33:
		return userUACV3Profile(value), nil
	default:
		return userUACNone, errUACConfiguration
	}
}

// row is the global wire slot, not the user ordinal. Slots 13..30 remain
// byte-identical group/computer decoys; new users use the unused slots 31..51.
// Actual data is authored here, never imported from the independent oracle.
func userUACV3Entry(id, row int) []byte {
	if id < 4 || id > 7 || row < 1 || row > 51 {
		return nil
	}
	if row >= 13 && row <= 30 {
		return directoryV3Entry(id, row)
	}
	number := row
	if row > 30 {
		number = row - 18
	}
	name := fmt.Sprintf("user-%02d", number)
	dn := fmt.Sprintf("CN=%s,OU=Users,DC=synthetic,DC=invalid", name)
	attributes := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))), directoryAttribute("objectClass", "top", "person", "organizationalPerson", "user")...)
	attributes = append(attributes, directoryAttribute("sAMAccountName", name)...)
	switch number % 3 {
	case 1:
		attributes = append(attributes, directoryAttribute("userAccountControl", "514")...)
	case 2:
		value := "512"
		if number == 2 || number == 14 || number == 26 {
			value = "0"
		}
		attributes = append(attributes, directoryAttribute("userAccountControl", value)...)
	}
	attributes = append(attributes, directoryV2Supplemental(row)...)
	if number != 12 {
		primary := "513"
		if number == 2 {
			primary = "0"
		}
		if number == 3 {
			primary = "4294967295"
		}
		values := []string{fmt.Sprintf("HOST/row-%02d.synthetic.invalid", row), fmt.Sprintf("HTTP/row-%02d.synthetic.invalid", row)}
		if number == 1 {
			values = []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}
		}
		attributes = append(attributes, directoryAttribute("primaryGroupID", primary)...)
		attributes = append(attributes, directoryAttribute("servicePrincipalName", values...)...)
	}
	return message(id, 0x64, octets(dn), element(0x30, attributes))
}
