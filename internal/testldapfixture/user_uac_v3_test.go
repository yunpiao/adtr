//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

const uacTestNonce = "0123456789abcdef0123456789abcdef"

func uacTestEnvironment(name string) string {
	if name == "ADTR_LDAP_FIXTURE_UAC_RUN_NONCE" {
		return uacTestNonce
	}
	return fixtureEnvironment(name)
}
func TestUserUACConfigurationClosedAndDefaultOff(t *testing.T) {
	base := []string{"-cert", "synthetic.crt", "-key", "synthetic.key", "-directory-mode", "-directory-v3", "-user-uac-v3-profile=mixed-33-v1", "-user-uac-evidence-dir=/uac-evidence"}
	cfg, err := readConfiguration(base, uacTestEnvironment)
	if err != nil || cfg.userUACV3Profile != userUACMixed33 {
		t.Fatal("explicit C configuration rejected")
	}
	for _, flag := range []string{"-directory-mode=false", "-directory-v3=false", "-directory-v2", "-directory-v4", "-user-assets-v2", "-computer-assets-v3", "-group-assets-v3", "-directory-empty", "-directory-slow", "-group-members-v4-pagination", "-group-members-v4-refresh", "-user-export-failure-v3", "-group-export-failure-v3", "-computer-export-failure-v3", "-control-dir=/private", "-empty-transition-dir=/private", "-user-uac-v3-profile=other", "-user-uac-evidence-dir="} {
		t.Run(flag, func(t *testing.T) {
			if _, err := readConfiguration(append(append([]string{}, base...), flag), uacTestEnvironment); err == nil {
				t.Fatal("mixed or invalid C mode accepted")
			}
		})
	}
	if _, err := readConfiguration(base, fixtureEnvironment); err == nil {
		t.Fatal("missing nonce accepted")
	}
	cfg, err = readConfiguration([]string{"-cert", "synthetic.crt", "-key", "synthetic.key"}, fixtureEnvironment)
	if err != nil || cfg.userUACV3Profile != userUACNone || cfg.userUACEvidenceDir != "" {
		t.Fatal("default changed")
	}
	for _, value := range []string{"true", "mixed-33", "MIXED-33-V1"} {
		if _, err := parseUserUACV3Profile(value); err == nil {
			t.Fatal("profile not closed")
		}
	}
	if r, err := newUserUACWireRecorder(userUACNone, "", ""); err != nil || r != nil {
		t.Fatal("default recorder allocated")
	}
}

func TestUserUACV3PreservesOriginalIdentityAndIndependentUACSlots(t *testing.T) {
	// Independent slot lists, not the generator's modulo predicate.
	set := map[int]bool{1: true, 4: true, 7: true, 10: true, 31: true, 34: true, 37: true, 40: true, 43: true, 46: true, 49: true}
	clear := map[int]string{2: "0", 5: "512", 8: "512", 11: "512", 32: "0", 35: "512", 38: "512", 41: "512", 44: "0", 47: "512", 50: "512"}
	absent := map[int]bool{3: true, 6: true, 9: true, 12: true, 33: true, 36: true, 39: true, 42: true, 45: true, 48: true, 51: true}
	counts := map[string]int{}
	for row := 1; row <= 51; row++ {
		frame := userUACV3Entry(4, row)
		if len(frame) > 2048 {
			t.Fatal("entry cap")
		}
		req, err := readRequest(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		entry := decodeTestEntry(t, req.body)
		attrs := map[string][][]byte{}
		for _, a := range entry.Attributes {
			attrs[a.Name] = a.Values
		}
		if row >= 13 && row <= 30 {
			if !bytes.Equal(frame, legacyMainDirectoryV3Entry(4, row)) {
				t.Fatal("decoy bytes changed")
			}
			continue
		}
		expected := ""
		if set[row] {
			expected = "514"
			counts["set"]++
		} else if value, ok := clear[row]; ok {
			expected = value
			counts["clear"]++
		} else if absent[row] {
			counts["absent"]++
		} else {
			t.Fatal("missing independently authored slot")
		}
		if expected == "" {
			if _, ok := attrs["userAccountControl"]; ok {
				t.Fatal("absent UAC populated")
			}
		} else if !reflect.DeepEqual(attrs["userAccountControl"], [][]byte{[]byte(expected)}) {
			t.Fatal("UAC mismatch")
		}
		if row <= 12 {
			oldReq, _ := readRequest(bytes.NewReader(legacyMainDirectoryV3Entry(4, row)))
			old := decodeTestEntry(t, oldReq.body)
			strip := func(value directoryassets.Entry) directoryassets.Entry {
				var result []directoryassets.Attribute
				for _, a := range value.Attributes {
					if a.Name != "userAccountControl" {
						result = append(result, a)
					}
				}
				value.Attributes = result
				return value
			}
			if !reflect.DeepEqual(strip(old), strip(entry)) {
				t.Fatal("original non-UAC bytes changed")
			}
		} else {
			name := fmt.Sprintf("user-%02d", row-18)
			if entry.DN != "CN="+name+",OU=Users,DC=synthetic,DC=invalid" || !reflect.DeepEqual(attrs["sAMAccountName"], [][]byte{[]byte(name)}) || !reflect.DeepEqual(attrs["objectGUID"], [][]byte{bytes.Repeat([]byte{byte(row)}, 16)}) {
				t.Fatal("new identity mismatch")
			}
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"set": 11, "clear": 11, "absent": 11}) {
		t.Fatal("partitions")
	}
	for _, args := range [][2]int{{3, 1}, {8, 1}, {4, 0}, {4, 52}} {
		if userUACV3Entry(args[0], args[1]) != nil {
			t.Fatal("out of range accepted")
		}
	}
}

func TestUserUACFourVerifiedTLSPagesAndJoinedSeal(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(fmt.Sprintf("ldaps=%t", ldaps), func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode = true
			fixture.directoryV3 = true
			fixture.userUACV3Profile = userUACMixed33
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			r, err := newUserUACWireRecorder(userUACMixed33, root, uacTestNonce)
			if err != nil {
				t.Fatal(err)
			}
			defer r.root.Close()
			fixture.uacRecorder = r
			listener := newPipeListener()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			var serveErr error
			go func() { serveErr = fixture.serve(ctx, []endpoint{{listener: listener, ldaps: ldaps}}); close(done) }()
			client, peer := net.Pipe()
			listener.connections <- peer
			defer func() { client.Close(); cancel(); <-done }()
			client.SetDeadline(time.Now().Add(3 * time.Second))
			if !ldaps {
				send(t, client, message(1, 0x77, element(0x80, []byte(startTLSOID))))
				requireCode(t, client, 1, 0x78, 0)
			}
			conn := tls.Client(client, cfg)
			if err := conn.Handshake(); err != nil {
				t.Fatal(err)
			}
			send(t, conn, bindRequest(2, testUsername, testPassword))
			requireCode(t, conn, 2, 0x61, 0)
			send(t, conn, searchRequest(3, "", 0))
			receive(t, conn, 3, 0x64)
			requireCode(t, conn, 3, 0x65, 0)
			var cookie []byte
			total := 0
			for page, count := range []int{15, 15, 15, 6} {
				send(t, conn, testDirectoryRequest(page+4, directoryV3SearchBody, testPagingControl(1000, cookie)))
				rows, next := receiveUACPage(t, conn, page+4, count)
				total += len(rows)
				cookie = next
				if page < 3 && len(cookie) != 20 || page == 3 && len(cookie) != 0 {
					t.Fatal("cookie shape")
				}
			}
			if total != 51 {
				t.Fatal("non-divisible page lost rows")
			}
			deadline := time.Now().Add(time.Second)
			for {
				if _, err := os.Stat(filepath.Join(root, "candidate-wire.json")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("candidate not published")
				}
				time.Sleep(time.Millisecond)
			}
			// Match the production reader: close the raw transport at the TLS
			// record boundary, then race owned shutdown. Drain must be marked
			// before cancellation reaches the handler transport watcher.
			client.Close()
			cancel()
			<-done
			if serveErr != nil {
				t.Fatal("joined server rejected completed exchange", serveErr, r.poison, r.candidate.EntryCount, r.candidate.PageCount, r.attempts)
			}
			candidate, err := os.ReadFile(filepath.Join(root, "candidate-wire.json"))
			if err != nil || len(candidate) > 163840 {
				t.Fatal("candidate unavailable or oversized")
			}
			seal, err := os.ReadFile(filepath.Join(root, "terminal-seal.json"))
			if err != nil || len(seal) > 8192 {
				t.Fatal("terminal unavailable or oversized")
			}
			if bytes.Contains(candidate, []byte(testUsername)) || bytes.Contains(candidate, []byte(testPassword)) || bytes.Contains(seal, []byte(testPassword)) {
				t.Fatal("credential leaked")
			}
			t.Logf("candidate=%d seal=%d raw=%d done=%d", len(candidate), len(seal), r.rawBytes, r.doneBytes)
		})
	}
}

func receiveUACPage(t *testing.T, conn net.Conn, id, count int) ([]directoryassets.StoredObjectV3, []byte) {
	t.Helper()
	var rows []directoryassets.StoredObjectV3
	for range count {
		req, err := readRequest(conn)
		if err != nil || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {
			t.Fatal("invalid C entry")
		}
		row, err := directoryassets.DecodeV3(decodeTestEntry(t, req.body))
		if err != nil {
			t.Fatal("actual v3 decoder rejected C entry")
		}
		rows = append(rows, row)
	}
	_, cookie := receiveDirectoryPageForCounts(t, conn, id, 0, 51)
	return rows, cookie
}

func TestUserUACRejectedWireAttemptsAndTruncatedReadsPoison(t *testing.T) {
	for _, scenario := range []string{"wrong_cookie", "replayed_page", "missing_control", "duplicate_control", "noncritical", "changed_body", "rebind", "participant_root", "participant_starttls", "participant_other", "partial_after_root", "partial_after_complete", "second_exchange"} {
		t.Run(scenario, func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode = true
			fixture.directoryV3 = true
			fixture.userUACV3Profile = userUACMixed33
			r, root, _ := uacRecorderForTest(t)
			// The helper's unopened synthetic connection is closed before the actual
			// TLS handler; it must not count as a directory exchange.
			r.CloseConnection(1, uacCleanEOF)
			fixture.uacRecorder = r
			client, peer := net.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); fixture.handle(ctx, peer, true) }()
			defer func() { client.Close(); cancel(); <-done }()
			client.SetDeadline(time.Now().Add(3 * time.Second))
			conn := tls.Client(client, cfg)
			if err := conn.Handshake(); err != nil {
				t.Fatal(err)
			}
			send(t, conn, bindRequest(2, testUsername, testPassword))
			requireCode(t, conn, 2, 0x61, 0)
			send(t, conn, searchRequest(3, "", 0))
			receive(t, conn, 3, 0x64)
			requireCode(t, conn, 3, 0x65, 0)
			var cookie []byte
			pages := 1
			if scenario == "partial_after_root" {
				pages = 0
			}
			if scenario == "partial_after_complete" || scenario == "second_exchange" {
				pages = 4
			}
			for page := 1; page <= pages; page++ {
				send(t, conn, testDirectoryRequest(page+3, directoryV3SearchBody, testPagingControl(1000, cookie)))
				count := 15
				if page == 4 {
					count = 6
				}
				rows, next := receiveUACPage(t, conn, page+3, count)
				directoryassets.ClearStoredObjectsV3(rows)
				cookie = next
			}
			var packet []byte
			switch scenario {
			case "wrong_cookie":
				packet = testDirectoryRequest(5, directoryV3SearchBody, testPagingControl(1000, bytes.Repeat([]byte{42}, 20)))
			case "replayed_page":
				packet = testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil))
			case "missing_control":
				packet = testDirectoryRequest(5, directoryV3SearchBody, nil)
			case "duplicate_control":
				packet = testDirectoryRequest(5, directoryV3SearchBody, bytes.Repeat(testPagingControl(1000, cookie), 2))
			case "noncritical":
				packet = testDirectoryRequest(5, directoryV3SearchBody, bytes.Replace(testPagingControl(1000, cookie), []byte{1, 1, 255}, []byte{1, 1, 0}, 1))
			case "changed_body":
				packet = testDirectoryRequest(5, bytes.Replace(directoryV3SearchBody, []byte("synthetic"), []byte("different"), 1), testPagingControl(1000, cookie))
			case "rebind":
				packet = bindRequest(5, testUsername, testPassword)
			case "participant_root":
				packet = searchRequest(5, "", 0)
			case "participant_starttls":
				packet = message(5, 0x77, element(0x80, []byte(startTLSOID)))
			case "participant_other":
				packet = message(5, 0x42)
			case "partial_after_root", "partial_after_complete":
				packet = []byte{0x30, 0x20, 2}
			case "second_exchange":
				packet = testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil))
			}
			send(t, conn, packet)
			client.Close()
			<-done
			r.BeginDrain()
			if r.SealAfterJoin() == nil || r.poison == "none" {
				t.Fatal("rejected actual wire attempt did not poison")
			}
			if _, err := os.Stat(filepath.Join(root, "terminal-seal.json")); !os.IsNotExist(err) {
				t.Fatal("invalid transcript sealed")
			}
		})
	}
}

func TestUserUACNonparticipantRootReadySurvivesRebindAndTruncatedWire(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(fmt.Sprintf("ldaps=%t", ldaps), func(t *testing.T) {
			fixture, cfg := testServer(t)
			fixture.directoryMode = true
			fixture.directoryV3 = true
			fixture.userUACV3Profile = userUACMixed33
			r, root, unused := uacRecorderForTest(t)
			r.CloseConnection(unused, uacCleanEOF)
			fixture.uacRecorder = r
			open := func() (*tls.Conn, net.Conn, <-chan struct{}) {
				t.Helper()
				client, peer := net.Pipe()
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() { defer close(done); fixture.handle(ctx, peer, ldaps) }()
				t.Cleanup(func() { client.Close(); cancel(); <-done })
				client.SetDeadline(time.Now().Add(3 * time.Second))
				if !ldaps {
					send(t, client, message(1, 0x77, element(0x80, []byte(startTLSOID))))
					requireCode(t, client, 1, 0x78, 0)
				}
				conn := tls.Client(client, cfg)
				if err := conn.Handshake(); err != nil {
					t.Fatal(err)
				}
				send(t, conn, bindRequest(2, testUsername, testPassword))
				requireCode(t, conn, 2, 0x61, 0)
				send(t, conn, searchRequest(3, "", 0))
				receive(t, conn, 3, 0x64)
				requireCode(t, conn, 3, 0x65, 0)
				return conn, client, done
			}
			// Complete the sole participant through the real four-page write path.
			participant, participantRaw, participantDone := open()
			var cookie []byte
			for page, count := range []int{15, 15, 15, 6} {
				send(t, participant, testDirectoryRequest(page+4, directoryV3SearchBody, testPagingControl(1000, cookie)))
				rows, next := receiveUACPage(t, participant, page+4, count)
				directoryassets.ClearStoredObjectsV3(rows)
				cookie = next
			}
			participantRaw.Close()
			<-participantDone
			if _, err := os.Stat(filepath.Join(root, "candidate-wire.json")); err != nil {
				t.Fatal("complete participant did not publish candidate")
			}
			// A different connection never claims. The first rebind resets the
			// live bind-session RootDSE state, and the next request observes false.
			// Neither may erase the recorder's historical post-RootDSE obligation.
			other, otherRaw, otherDone := open()
			for _, id := range []int{4, 5} {
				send(t, other, bindRequest(id, testUsername, testPassword))
				requireCode(t, other, id, 0x61, 0)
			}
			send(t, other, []byte{0x30, 0x20, 2})
			otherRaw.Close()
			<-otherDone
			r.BeginDrain()
			if r.SealAfterJoin() == nil {
				t.Fatal("post-RootDSE truncation sealed after nonparticipant rebinds")
			}
			if r.poison != "ambiguous_post_root_read" || r.attempts != 4 || r.candidate.EntryCount != 51 || r.candidate.PageCount != 4 || len(r.connections) != 0 {
				t.Fatal("truncated nonparticipant did not poison the complete joined exchange")
			}
			if _, err := os.Stat(filepath.Join(root, "terminal-seal.json")); !os.IsNotExist(err) {
				t.Fatal("poisoned transcript has a terminal seal")
			}
		})
	}
}
