//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"
)

const (
	directoryBase            = "dc=synthetic,dc=invalid"
	pagingOID                = "1.2.840.113556.1.4.319"
	directoryRequestPageSize = 1000
	directoryPageEntries     = 15
	directoryObjectCount     = 30
	directorySlowPages       = 5
	directoryPageDelay       = 2 * time.Second
)

// This is a dictionary response to exactly the production reader's fixed
// request, not a filter evaluator or general LDAP server. Computers inherit
// user and are therefore included by the fixed user/group OR filter.
var directorySearchBody = bytes.Join([][]byte{
	octets(directoryBase), integer(0x0a, 2), integer(0x0a, 0),
	integer(0x02, 0), integer(0x02, 3), element(0x01, []byte{0}),
	element(0xa1,
		element(0xa3, octets("objectClass"), octets("user")),
		element(0xa3, octets("objectClass"), octets("group"))),
	element(0x30, octets("objectGUID"), octets("objectClass"), octets("sAMAccountName"), octets("userAccountControl")),
}, nil)

type directorySession struct {
	v2     bool
	empty  bool
	slow   bool
	page   int
	cookie []byte
}

func (d *directorySession) pages() int {
	if d.slow {
		return directorySlowPages
	}
	if d.empty {
		return 1
	}
	return directoryObjectCount / directoryPageEntries
}

func (d *directorySession) objects() int {
	if d.empty {
		return 0
	}
	return directoryObjectCount
}

func (d *directorySession) reset() {
	clear(d.cookie)
	d.cookie = nil
	d.page = 0
}

func pagingRequestControl(cookie []byte) []byte {
	return element(0x30, octets(pagingOID), element(0x01, []byte{0xff}),
		element(0x04, element(0x30, integer(0x02, directoryRequestPageSize), element(0x04, cookie))))
}

func (d *directorySession) matches(req request) bool {
	body := directorySearchBody
	if d.v2 {
		body = directoryV2SearchBody
	}
	if d.page >= d.pages() || !bytes.Equal(req.body, body) {
		return false
	}
	// Exact byte validation also rejects missing/noncritical/duplicate/unknown
	// controls, oversized or stale cookies, other page sizes and extra fields.
	expected := pagingRequestControl(d.cookie)
	defer clear(expected)
	return bytes.Equal(req.controls, expected)
}

func (d *directorySession) writePage(ctx context.Context, conn io.Writer, id int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.slow {
		// Fixed transport behavior only: no caller-supplied durations. The
		// existing connection context bounds this wait and shutdown joins it.
		timer := time.NewTimer(directoryPageDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	var next []byte
	if d.page+1 < d.pages() {
		// Binary prefix requires clients to treat this as opaque bytes. Random
		// suffix binds continuation to this connection, bind and current page.
		next = make([]byte, 20)
		defer clear(next)
		copy(next, []byte{0, 0xff, 0x80, 0})
		if _, err := rand.Read(next[4:]); err != nil {
			return errProtocol
		}
	}
	entries := d.objects() / d.pages()
	first := d.page*entries + 1
	for row := first; row < first+entries; row++ {
		if err := writeAll(conn, directoryEntryForDictionary(id, row, d.v2)); err != nil {
			return err
		}
	}
	control := element(0x30, octets(pagingOID), element(0x04,
		element(0x30, integer(0x02, d.objects()), element(0x04, next))))
	defer clear(control)
	done := element(0x30, integer(0x02, id),
		element(0x65, integer(0x0a, 0), octets(""), octets("")), element(0xa0, control))
	defer clear(done)
	if err := writeAll(conn, done); err != nil {
		return err
	}
	d.page++
	clear(d.cookie)
	d.cookie = append([]byte(nil), next...)
	return nil
}

func directoryAttribute(name string, values ...string) []byte {
	var encoded []byte
	for _, value := range values {
		encoded = append(encoded, octets(value)...)
	}
	return element(0x30, octets(name), element(0x31, encoded))
}

// Thirty immutable synthetic rows are deliberately split into partial pages
// despite a requested maximum of 1000. GUIDs are byte n repeated 16
// times. Row 1 has present UAC zero; every group has absent UAC and row 24
// (group-12) also has absent SAM. Missing fields must survive as nulls.
func directoryEntry(id, row int) []byte {
	return directoryEntryForDictionary(id, row, false)
}

func directoryEntryForDictionary(id, row int, v2 bool) []byte {
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
	if v2 && row != 24 {
		attributes = append(attributes, directoryV2Supplemental(row)...)
	}
	return message(id, 0x64, octets(dn), element(0x30, attributes))
}
