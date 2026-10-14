//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/userassetxlsx"
)

func TestUserExportFailureV3RequiresExactIsolatedMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-directory-mode", "-directory-v3", "-user-export-failure-v3"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || !cfg.userExportFailureV3 {
		t.Fatal("explicit export fixture rejected", err)
	}
	for _, flag := range []string{"-directory-v2", "-directory-v4", "-user-assets-v2", "-computer-assets-v3", "-group-assets-v3", "-group-members-v4-pagination", "-group-members-v4-refresh", "-directory-empty", "-directory-slow", "-control-dir=/private", "-empty-transition-dir=/private", "-directory-mode=false", "-directory-v3=false"} {
		t.Run(flag, func(t *testing.T) {
			if _, err := readConfiguration(append(append([]string{}, base...), flag), fixtureEnvironment); err == nil {
				t.Fatal("mixed export fixture accepted")
			}
		})
	}
}

func TestUserExportFailureV3ChangesOnlyFirstMail(t *testing.T) {
	for row := 1; row <= 30; row++ {
		original := legacyMainDirectoryV3Entry(4, row)
		if !bytes.Equal(original, directoryV3Entry(4, row)) {
			t.Fatal("legacy main wire bytes changed", row)
		}
		if !bytes.Equal(original, directoryV3EntryWithExportFailure(4, row, false)) {
			t.Fatal("disabled mode changed legacy wire", row)
		}
		changed := directoryV3EntryWithExportFailure(4, row, true)
		if row != 1 {
			if !bytes.Equal(original, changed) {
				t.Fatal("non-target row changed", row)
			}
			continue
		}
		before, err := readRequest(bytes.NewReader(original))
		if err != nil {
			t.Fatal(err)
		}
		after, err := readRequest(bytes.NewReader(changed))
		if err != nil {
			t.Fatal(err)
		}
		oldEntry, newEntry := decodeTestEntry(t, before.body), decodeTestEntry(t, after.body)
		changedAttributes := 0
		for i := range oldEntry.Attributes {
			if oldEntry.Attributes[i].Name == "mail" {
				if !reflect.DeepEqual(newEntry.Attributes[i].Values, [][]byte{[]byte(userExportFailureV3Mail)}) {
					t.Fatal("invalid failure scalar")
				}
				newEntry.Attributes[i] = oldEntry.Attributes[i]
				changedAttributes++
			}
		}
		if changedAttributes != 1 || !reflect.DeepEqual(oldEntry, newEntry) {
			t.Fatal("fixture changed fields other than single mail")
		}
	}
}

func TestUserExportFailureV3TLSStorageAndSelectedWriter(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(fmt.Sprintf("LDAPS=%t", ldaps), func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode, fixture.directoryV3, fixture.userExportFailureV3 = true, true, true
			conn := directoryConnection(t, fixture, cfg, ldaps)
			send(t, conn, testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil)))
			first, cookie := receiveGroupPage(t, conn, 4)
			if len(cookie) != 20 {
				t.Fatal("missing opaque continuation")
			}
			send(t, conn, testDirectoryRequest(5, directoryV3SearchBody, testPagingControl(1000, cookie)))
			second, end := receiveGroupPage(t, conn, 5)
			if len(end) != 0 {
				t.Fatal("nonterminal continuation")
			}
			rows := append(first, second...)
			defer directoryassets.ClearStoredObjectsV3(rows)
			counts := map[directoryassets.Kind]int{}
			var full, safe [][]string
			for _, row := range rows {
				counts[row.Base.Kind]++
				stored, err := directoryassets.EncodeStoredObjectV3(row)
				if err != nil {
					t.Fatal(err)
				}
				restored, err := directoryassets.DecodeStoredObjectV3(stored)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := directoryassets.ProjectV3(restored)
				if err != nil {
					t.Fatal(err)
				}
				if projected.Kind != directoryassets.User {
					continue
				}
				text := func(p *string) string {
					if p == nil {
						return ""
					}
					return *p
				}
				full = append(full, []string{projected.GUID, text(projected.SAMAccountName), text(projected.ObjectSID), text(projected.Mail), projected.DN, text(projected.WhenCreated)})
				safe = append(safe, []string{text(projected.SAMAccountName), projected.GUID})
			}
			if counts[directoryassets.User] != 12 || counts[directoryassets.Group] != 12 || counts[directoryassets.Computer] != 6 {
				t.Fatal("changed fixture population", counts)
			}
			if full[0][3] != userExportFailureV3Mail {
				t.Fatal("storage/projection lost invalid XML scalar")
			}
			write := func(headers []string, values [][]string) ([]byte, error) {
				var out bytes.Buffer
				index := 0
				err := userassetxlsx.Write(context.Background(), &out, headers, func() ([]string, error) {
					if index == len(values) {
						return nil, io.EOF
					}
					v := values[index]
					index++
					return v, nil
				})
				return out.Bytes(), err
			}
			if _, err := write([]string{"guid", "sAMAccountName", "sid", "mail", "dn", "whenCreated"}, full); !errors.Is(err, userassetxlsx.ErrInvalidText) {
				t.Fatal("selected scalar did not fail real XLSX writer", err)
			}
			workbook, err := write([]string{"sAMAccountName", "guid"}, safe)
			if err != nil {
				t.Fatal("safe column recovery failed", err)
			}
			archive, err := zip.NewReader(bytes.NewReader(workbook), int64(len(workbook)))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, part := range archive.File {
				if part.Name != "xl/worksheets/sheet1.xml" {
					continue
				}
				found = true
				r, err := part.Open()
				if err != nil {
					t.Fatal(err)
				}
				sheet, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(sheet, []byte(userExportFailureV3Mail)) || bytes.Count(sheet, []byte("<row ")) != 13 {
					t.Fatal("invalid safe worksheet")
				}
				for i, values := range safe {
					for column, value := range values {
						cell := fmt.Sprintf(`<c r="%c%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, 'A'+column, i+2, value)
						if !strings.Contains(string(sheet), cell) {
							t.Fatal("safe column order/value mismatch", i, column)
						}
					}
				}
			}
			if !found {
				t.Fatal("missing real worksheet")
			}
		})
	}
}

// Frozen test-only legacy wire oracle, copied verbatim except function names from
// main 370e405b50a23a85a63126c562bb1790dd4eca87. Do not route it through the
// new optional override helpers: all 30 rows must retain their original bytes.
func legacyMainDirectoryV3Entry(id, row int) []byte {
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
		attributes = append(attributes, legacyMainDirectoryV2Supplemental(row)...)
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
	return message(id, 0x64, octets(dn), element(0x30, attributes))
}

func legacyMainDirectoryV2Supplemental(row int) []byte {
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
