//go:build integration && linux

package main

import (
	"github.com/yunpiao/adtr/internal/directoryassets"
	"testing"
)

func TestGroupMembersV4RefreshConfiguration(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-directory-mode", "-directory-v4", "-group-members-v4-refresh", "-empty-transition-dir", "/private-fixture"}
	cfg, err := readConfiguration(base, fixtureEnvironment)
	if err != nil || !cfg.groupMembersV4Refresh || cfg.emptyTransitionDir != "/private-fixture" {
		t.Fatal("explicit refresh mode rejected", err)
	}
	for _, extra := range [][]string{{"-directory-empty"}, {"-directory-slow"}, {"-directory-v2"}, {"-directory-v3"}, {"-group-members-v4-pagination"}, {"-control-dir", "/control"}} {
		if _, err := readConfiguration(append(append([]string(nil), base...), extra...), fixtureEnvironment); err == nil {
			t.Fatalf("mixed refresh accepted: %v", extra)
		}
	}
	for _, args := range [][]string{base[:len(base)-2], {"-cert", "synthetic.crt", "-key", "synthetic.key", "-group-members-v4-refresh", "-empty-transition-dir", "/private-fixture"}} {
		if _, err := readConfiguration(args, fixtureEnvironment); err == nil {
			t.Fatal("incomplete refresh mode accepted")
		}
	}
}

func TestGroupMembersV4RefreshSnapshotPinsUntilNextBind(t *testing.T) {
	transition, path := testEmptyTransition(t)
	session := directorySession{v4: true, emptyTransition: transition}
	if err := session.sampleEmptyTransition(); err != nil || session.empty {
		t.Fatal("initial v4 not nonempty", err)
	}
	writeEmptyTransition(t, path)
	if err := session.sampleEmptyTransition(); err != nil || session.empty {
		t.Fatal("v4 continuation changed", err)
	}
	session.reset()
	if err := session.sampleEmptyTransition(); err != nil || !session.empty {
		t.Fatal("new v4 bind did not become empty", err)
	}
}

func TestGroupMembersV4RefreshTLSStablePagesAndRebind(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			fixture, clientTLS := testServer(t)
			transition, path := testEmptyTransition(t)
			fixture.directoryMode, fixture.directoryV4, fixture.emptyTransition = true, true, transition
			conn := directoryConnection(t, fixture, clientTLS, ldaps)
			send(t, conn, testDirectoryRequest(4, directoryV4SearchBody, testPagingControl(1000, nil)))
			first, cookie := receiveDirectoryV4Page(t, conn, 4)
			if len(cookie) != 20 {
				t.Fatal("nonempty v4 first page lost continuation")
			}
			writeEmptyTransition(t, path)
			send(t, conn, testDirectoryRequest(5, directoryV4SearchBody, testPagingControl(1000, cookie)))
			second, end := receiveDirectoryV4Page(t, conn, 5)
			rows := append(first, second...)
			defer directoryassets.ClearStoredObjectsV4(rows)
			assertDirectoryV4Rows(t, rows)
			if len(end) != 0 {
				t.Fatal("v4 second page did not terminate")
			}
			send(t, conn, bindRequest(6, testUsername, testPassword))
			requireCode(t, conn, 6, 0x61, 0)
			send(t, conn, searchRequest(7, "", 0))
			receive(t, conn, 7, 0x64)
			requireCode(t, conn, 7, 0x65, 0)
			send(t, conn, testDirectoryRequest(8, directoryV4SearchBody, testPagingControl(1000, nil)))
			_, end = receiveDirectoryPageForCounts(t, conn, 8, 0, 0)
			if len(end) != 0 {
				t.Fatal("new v4 bind did not terminate empty")
			}
		})
	}
}
