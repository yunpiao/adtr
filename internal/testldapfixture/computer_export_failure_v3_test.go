//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/computerassetxlsx"
	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestComputerExportFailureV3RequiresExactIsolatedMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-directory-mode", "-directory-v3", "-computer-assets-v3", "-computer-export-failure-v3"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || !cfg.computerExportFailureV3 {
		t.Fatal("explicit export fixture rejected", err)
	}
	for _, flag := range []string{"-directory-v2", "-directory-v4", "-user-assets-v2", "-group-assets-v3", "-computer-assets-v3=false", "-user-export-failure-v3", "-group-export-failure-v3", "-group-members-v4-pagination", "-group-members-v4-refresh", "-directory-empty", "-directory-slow", "-control-dir=/private", "-empty-transition-dir=/private", "-directory-mode=false", "-directory-v3=false"} {
		t.Run(flag, func(t *testing.T) {
			if _, err := readConfiguration(append(append([]string{}, base...), flag), fixtureEnvironment); err == nil {
				t.Fatal("mixed export fixture accepted")
			}
		})
	}
}

func TestComputerExportFailureV3ChangesOnlyFirstSAM(t *testing.T) {
	for row := 1; row <= 30; row++ {
		original := legacyComputerAssetsV3Entry(4, row)
		if !bytes.Equal(original, computerAssetsV3Entry(4, row)) {
			t.Fatal("legacy main wire bytes changed", row)
		}
		if !bytes.Equal(original, computerAssetsV3EntryWithExportFailure(4, row, false)) {
			t.Fatal("disabled mode changed legacy wire", row)
		}
		changed := computerAssetsV3EntryWithExportFailure(4, row, true)
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
			if oldEntry.Attributes[i].Name == "sAMAccountName" {
				if !reflect.DeepEqual(newEntry.Attributes[i].Values, [][]byte{[]byte(computerExportFailureV3SAM)}) {
					t.Fatal("invalid failure scalar")
				}
				newEntry.Attributes[i] = oldEntry.Attributes[i]
				changedAttributes++
			}
		}
		if changedAttributes != 1 || !reflect.DeepEqual(oldEntry, newEntry) {
			t.Fatal("fixture changed fields other than single SAM")
		}
	}
}

func TestComputerExportFailureV3TLSStorageAndSelectedWriter(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(fmt.Sprintf("LDAPS=%t", ldaps), func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode, fixture.directoryV3, fixture.computerAssetsV3, fixture.computerExportFailureV3 = true, true, true, true
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
				if projected.Kind != directoryassets.Computer {
					continue
				}
				text := func(p *string) string {
					if p == nil {
						return ""
					}
					return *p
				}
				full = append(full, []string{projected.GUID, text(projected.SAMAccountName), text(projected.ObjectSID), projected.DN, text(projected.WhenCreated)})
				safe = append(safe, []string{projected.DN, projected.GUID})
			}
			if counts[directoryassets.User] != 3 || counts[directoryassets.Computer] != 23 || counts[directoryassets.Group] != 4 {
				t.Fatal("changed fixture population", counts)
			}
			if full[0][1] != computerExportFailureV3SAM {
				t.Fatal("storage/projection lost invalid XML scalar")
			}
			write := func(headers []string, values [][]string) ([]byte, error) {
				var out bytes.Buffer
				index := 0
				err := computerassetxlsx.Write(context.Background(), &out, headers, func() ([]string, error) {
					if index == len(values) {
						return nil, io.EOF
					}
					v := values[index]
					index++
					return v, nil
				})
				return out.Bytes(), err
			}
			if _, err := write([]string{"guid", "sAMAccountName", "sid", "dn", "whenCreated"}, full); !errors.Is(err, computerassetxlsx.ErrInvalidText) {
				t.Fatal("selected scalar did not fail real XLSX writer", err)
			}
			workbook, err := write([]string{"dn", "guid"}, safe)
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
				if bytes.Contains(sheet, []byte(computerExportFailureV3SAM)) || bytes.Count(sheet, []byte("<row ")) != 24 {
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

// Frozen pre-export computer wire oracle; disabled mode stays byte exact.
func legacyComputerAssetsV3Entry(id, row int) []byte {
	kind, ou := "computer", "Computers"
	classes := []string{"top", "person", "organizationalPerson", "user", "computer"}
	if row > 23 {
		kind, ou = "user", "Users"
		classes = []string{"top", "person", "organizationalPerson", "user"}
	}
	if row > 26 {
		kind, ou = "group", "Groups"
		classes = []string{"top", "group"}
	}
	name := fmt.Sprintf("%s-%02d", kind, row)
	dn := fmt.Sprintf("CN=%s,OU=%s,DC=synthetic,DC=invalid", name, ou)
	attrs := append(directoryAttribute("objectGUID", string(bytes.Repeat([]byte{byte(row)}, 16))), directoryAttribute("objectClass", classes...)...)
	if row != 23 {
		sam := name
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
		spns := []string{fmt.Sprintf("HOST/computer-%02d.synthetic.invalid", row), "HTTP/fixture"}
		if row == 1 {
			spns = []string{"HOST/Z", "host/a\x00\n😀<script>", "HOST/a"}
		}
		attrs = append(attrs, directoryAttribute("servicePrincipalName", spns...)...)
	}
	return message(id, 0x64, octets(dn), element(0x30, attrs))
}
