//go:build integration

package main

import (
	"bytes"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"io"
	"slices"
	"testing"
)

func TestGroupAssetsV3FixtureRequiresExactIsolatedMode(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key"}
	for _, args := range [][]string{{"-directory-mode", "-directory-v3", "-group-assets-v3", "-computer-assets-v3"}, {"-group-assets-v3"}, {"-directory-mode", "-group-assets-v3"}, {"-directory-mode", "-directory-v2", "-group-assets-v3"}, {"-directory-mode", "-directory-v3", "-group-assets-v3", "-directory-empty"}, {"-directory-mode", "-directory-v3", "-group-assets-v3", "-directory-slow"}} {
		if _, e := readConfiguration(append(append([]string{}, base...), args...), fixtureEnvironment); e == nil {
			t.Fatal(args)
		}
	}
	cfg, e := readConfiguration(append(base, "-directory-mode", "-directory-v3", "-group-assets-v3"), fixtureEnvironment)
	if e != nil || !cfg.groupAssetsV3 {
		t.Fatal(e)
	}
}
func receiveGroupPage(t *testing.T, conn io.Reader, id int) ([]directoryassets.StoredObjectV3, []byte) {
	t.Helper()
	var rows []directoryassets.StoredObjectV3
	for range 15 {
		r, e := readRequest(conn)
		if e != nil || r.id != id || r.tag != 0x64 || len(r.controls) != 0 {
			t.Fatal("invalid entry", e)
		}
		v, e := directoryassets.DecodeV3(decodeTestEntry(t, r.body))
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, v)
	}
	_, cookie := receiveDirectoryPageForCounts(t, conn, id, 0, 30)
	return rows, cookie
}
func TestGroupAssetsV3RealTLSBERClosedFactsAndPages(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			f, cfg := testServer(t)
			f.directoryMode, f.directoryV3, f.groupAssetsV3 = true, true, true
			conn := directoryConnection(t, f, cfg, ldaps)
			send(t, conn, testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil)))
			first, cookie := receiveGroupPage(t, conn, 4)
			if len(cookie) != 20 || !bytes.Equal(cookie[:4], []byte{0, 255, 128, 0}) {
				t.Fatal("lost opaque cookie")
			}
			send(t, conn, testDirectoryRequest(5, directoryV3SearchBody, testPagingControl(1000, cookie)))
			second, end := receiveGroupPage(t, conn, 5)
			if len(end) != 0 {
				t.Fatal("nonterminal page")
			}
			rows := append(first, second...)
			defer directoryassets.ClearStoredObjectsV3(rows)
			counts := map[directoryassets.Kind]int{}
			for _, v := range rows {
				counts[v.Base.Kind]++
				if _, e := directoryassets.ProjectV3(v); e != nil {
					t.Fatal(e)
				}
			}
			if counts[directoryassets.Group] != 23 || counts[directoryassets.User] != 3 || counts[directoryassets.Computer] != 4 {
				t.Fatal(counts)
			}
			firstRow, _ := directoryassets.ProjectV3(rows[0])
			if !slices.Equal(firstRow.Classes, []string{"group", "top"}) {
				t.Fatal("objectClass must use existing decoder lowercase/sort contract", firstRow.Classes)
			}
			if firstRow.PrimaryGroupID == nil || *firstRow.PrimaryGroupID != 0 || firstRow.UserAccountControl == nil || *firstRow.UserAccountControl != 0 || firstRow.WhenCreated == nil || *firstRow.WhenCreated != "0001-01-01T00:00:00Z" || firstRow.ServicePrincipalName[1] != "host/a\x00\n😀<script>" {
				t.Fatal("lost boundary facts")
			}
			null, _ := directoryassets.ProjectV3(rows[22])
			if null.SAMAccountName != nil || null.PrimaryGroupID != nil || null.ServicePrincipalName != nil || null.WhenCreated != nil {
				t.Fatal("missing values were filled")
			}
		})
	}
}
