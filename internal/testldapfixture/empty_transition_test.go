//go:build integration && linux

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func testEmptyTransition(t *testing.T) (*fixtureEmptyTransition, string) {
	t.Helper()
	path := t.TempDir()
	transition, err := openFixtureEmptyTransition(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transition.root.Close() })
	return transition, path
}

func writeEmptyTransition(t *testing.T, path string) {
	t.Helper()
	// Match the Node producer: publish one complete immutable file atomically,
	// without replacing a preexisting marker or exposing partial contents.
	pending := filepath.Join(path, ".empty-v1.pending")
	f, err := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAll(f, []byte(emptyTransitionBytes)); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pending, filepath.Join(path, emptyTransitionMarker)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyTransitionMarkerStrictAndFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{"empty", ""}, {"missing newline", "empty-v1"}, {"wrong version", "empty-v2\n"},
		{"trailing newline", "empty-v1\n\n"}, {"nul", "empty-v1\x00"},
		{"leading space", " empty-v1\n"}, {"trailing space", "empty-v1 \n"},
		{"oversized", string(bytes.Repeat([]byte{'x'}, 4096))},
	} {
		t.Run(test.name, func(t *testing.T) {
			transition, path := testEmptyTransition(t)
			if empty, err := transition.sample(); empty || err != nil {
				t.Fatal("absence changed the original nonempty dictionary", err)
			}
			if err := os.WriteFile(filepath.Join(path, emptyTransitionMarker), []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if empty, err := transition.sample(); empty || err != errEmptyTransition {
				t.Fatal("invalid marker did not fail closed", err)
			}
		})
	}
	t.Run("exact value only", func(t *testing.T) {
		transition, path := testEmptyTransition(t)
		// A staging file and arbitrary directory contents do not arm transition.
		if err := os.WriteFile(filepath.Join(path, ".empty-v1.pending"), []byte(emptyTransitionBytes), 0600); err != nil {
			t.Fatal(err)
		}
		if empty, err := transition.sample(); empty || err != nil {
			t.Fatal("unpublished staging file armed transition")
		}
		if err := os.Rename(filepath.Join(path, ".empty-v1.pending"), filepath.Join(path, emptyTransitionMarker)); err != nil {
			t.Fatal(err)
		}
		if empty, err := transition.sample(); !empty || err != nil {
			t.Fatal("exact marker rejected", err)
		}
	})
}

func TestEmptyTransitionRejectsUnsafeFilesystemInputs(t *testing.T) {
	for _, kind := range []string{"directory", "fifo", "symlink inside", "symlink outside", "dangling symlink", "unreadable", "closed root"} {
		t.Run(kind, func(t *testing.T) {
			transition, path := testEmptyTransition(t)
			marker := filepath.Join(path, emptyTransitionMarker)
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(marker, 0700)
			case "fifo":
				err = syscall.Mkfifo(marker, 0600)
			case "symlink inside", "symlink outside":
				target := filepath.Join(path, "target")
				if kind == "symlink outside" {
					target = filepath.Join(t.TempDir(), "target")
				}
				if err = os.WriteFile(target, []byte(emptyTransitionBytes), 0600); err == nil {
					err = os.Symlink(target, marker)
				}
			case "dangling symlink":
				err = os.Symlink("missing", marker)
			case "unreadable":
				err = os.WriteFile(marker, []byte(emptyTransitionBytes), 0000)
			case "closed root":
				err = transition.root.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if empty, err := transition.sample(); empty || err != errEmptyTransition {
				t.Fatal("unsafe marker did not fail closed", err)
			}
		})
	}
}

func TestEmptyTransitionDirectoryRemainsAnchored(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "original")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	transition, err := openFixtureEmptyTransition(path)
	if err != nil {
		t.Fatal(err)
	}
	defer transition.root.Close()
	renamed := filepath.Join(parent, "renamed")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	writeEmptyTransition(t, path)
	if empty, err := transition.sample(); empty || err != nil {
		t.Fatal("path replacement escaped the original directory", err)
	}
	writeEmptyTransition(t, renamed)
	if empty, err := transition.sample(); !empty || err != nil {
		t.Fatal("original anchored directory became inaccessible", err)
	}
}

func TestEmptyTransitionStartupErrorsDoNotExposePaths(t *testing.T) {
	if _, err := openFixtureEmptyTransition(filepath.Join(t.TempDir(), "absent")); err != errEmptyTransition {
		t.Fatal("unexpected missing-directory error", err)
	}
	file := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openFixtureEmptyTransition(file); err != errEmptyTransition {
		t.Fatal("regular file accepted as transition directory", err)
	}
	err := run(context.Background(), []string{"-cert", "synthetic.crt", "-key", "synthetic.key",
		"-directory-mode", "-directory-v3", "-group-assets-v3", "-empty-transition-dir", file}, fixtureEnvironment, io.Discard)
	if err != errEmptyTransition {
		t.Fatal("startup did not report the fixed transition error", err)
	}
}

func TestEmptyTransitionSnapshotLatchesAndResetPreservesFixedEmptyMode(t *testing.T) {
	transition, path := testEmptyTransition(t)
	session := directorySession{v3: true, groupAssetsV3: true, emptyTransition: transition}
	if err := session.sampleEmptyTransition(); err != nil || session.empty || !session.transitionSampled {
		t.Fatal("first snapshot must be nonempty", err)
	}
	writeEmptyTransition(t, path)
	if err := session.sampleEmptyTransition(); err != nil || session.empty {
		t.Fatal("ongoing snapshot changed after marker publication", err)
	}
	session.reset()
	if session.empty || session.transitionSampled || session.page != 0 || session.cookie != nil {
		t.Fatal("rebind did not clear all snapshot state")
	}
	if err := session.sampleEmptyTransition(); err != nil || !session.empty || !session.transitionSampled {
		t.Fatal("new bind did not sample the empty marker", err)
	}
	if err := os.Remove(filepath.Join(path, emptyTransitionMarker)); err != nil {
		t.Fatal(err)
	}
	if err := session.sampleEmptyTransition(); err != nil || !session.empty {
		t.Fatal("latched empty snapshot changed when the marker disappeared", err)
	}
	fixed := directorySession{empty: true}
	fixed.reset()
	if !fixed.empty {
		t.Fatal("reset changed the existing fixed-empty fixture")
	}
}

func transitionServer(t *testing.T, group bool) (*server, *tls.Config, string) {
	t.Helper()
	fixture, clientTLS := testServer(t)
	transition, path := testEmptyTransition(t)
	fixture.directoryMode, fixture.directoryV3 = true, true
	fixture.groupAssetsV3, fixture.computerAssetsV3 = group, !group
	fixture.emptyTransition = transition
	return fixture, clientTLS, path
}

func TestEmptyTransitionTLSStablePagesAndRebind(t *testing.T) {
	for _, group := range []bool{false, true} {
		for _, ldaps := range []bool{false, true} {
			t.Run(fmt.Sprintf("group=%t/ldaps=%t", group, ldaps), func(t *testing.T) {
				fixture, clientTLS, path := transitionServer(t, group)
				conn := directoryConnection(t, fixture, clientTLS, ldaps)
				receivePage := receiveComputerPage
				if group {
					receivePage = receiveGroupPage
				}
				send(t, conn, testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil)))
				first, cookie := receivePage(t, conn, 4)
				defer directoryassets.ClearStoredObjectsV3(first)
				if len(cookie) != 20 {
					t.Fatal("first nonempty page did not issue a continuation")
				}
				writeEmptyTransition(t, path)
				send(t, conn, testDirectoryRequest(5, directoryV3SearchBody, testPagingControl(1000, cookie)))
				second, end := receivePage(t, conn, 5)
				defer directoryassets.ClearStoredObjectsV3(second)
				if len(first) != 15 || len(second) != 15 || len(end) != 0 {
					t.Fatal("marker publication mixed dictionary snapshots across pages")
				}
				send(t, conn, bindRequest(6, testUsername, testPassword))
				requireCode(t, conn, 6, 0x61, 0)
				send(t, conn, searchRequest(7, "", 0))
				receive(t, conn, 7, 0x64)
				requireCode(t, conn, 7, 0x65, 0)
				send(t, conn, testDirectoryRequest(8, directoryV3SearchBody, testPagingControl(1000, nil)))
				_, end = receiveDirectoryPageForCounts(t, conn, 8, 0, 0)
				if len(end) != 0 {
					t.Fatal("empty page must terminate pagination")
				}
				// A fresh authenticated connection independently observes empty.
				fresh := directoryConnection(t, fixture, clientTLS, ldaps)
				send(t, fresh, testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil)))
				receiveDirectoryPageForCounts(t, fresh, 4, 0, 0)
			})
		}
	}
}

func TestEmptyTransitionTLSSamplesOnlyFirstValidAuthenticatedDirectoryPage(t *testing.T) {
	for _, stage := range []string{"after RootDSE", "invalid body", "wrong page size", "duplicate control", "before RootDSE", "unbound", "failed bind"} {
		t.Run(stage, func(t *testing.T) {
			fixture, clientTLS, path := transitionServer(t, true)
			conn := securePipe(t, fixture, clientTLS, true)
			id := 1
			bind := func(password string, code int) {
				send(t, conn, bindRequest(id, testUsername, password))
				requireCode(t, conn, id, 0x61, code)
				id++
			}
			root := func() {
				send(t, conn, searchRequest(id, "", 0))
				receive(t, conn, id, 0x64)
				requireCode(t, conn, id, 0x65, 0)
				id++
			}
			if stage == "failed bind" {
				bind("wrong synthetic password", 49)
			} else if stage != "unbound" {
				bind(testPassword, 0)
			}
			if stage != "before RootDSE" && stage != "unbound" && stage != "failed bind" {
				root()
			}
			if stage != "after RootDSE" {
				body, control, code := directoryV3SearchBody, testPagingControl(1000, nil), 53
				switch stage {
				case "invalid body":
					body = testDirectoryBody()
				case "wrong page size":
					control = testPagingControl(999, nil)
				case "duplicate control":
					control = append(control, control...)
				case "unbound", "failed bind":
					code = 50
				}
				send(t, conn, testDirectoryRequest(id, body, control))
				requireCode(t, conn, id, 0x65, code)
				id++
			}
			writeEmptyTransition(t, path)
			if stage == "unbound" || stage == "failed bind" {
				bind(testPassword, 0)
			}
			if stage == "before RootDSE" || stage == "unbound" || stage == "failed bind" {
				root()
			}
			if id > maxRequests {
				t.Fatal("test exceeded fixture request budget")
			}
			// An early sample of absence would latch old rows and fail this.
			send(t, conn, testDirectoryRequest(id, directoryV3SearchBody, testPagingControl(1000, nil)))
			receiveDirectoryPageForCounts(t, conn, id, 0, 0)
		})
	}
}

func TestEmptyTransitionMalformedMarkerClosesTLSWithoutSuccess(t *testing.T) {
	fixture, clientTLS, path := transitionServer(t, true)
	if err := os.WriteFile(filepath.Join(path, emptyTransitionMarker), []byte("wrong-v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Bind and RootDSE still succeed; neither reads the marker.
	conn := directoryConnection(t, fixture, clientTLS, true)
	send(t, conn, testDirectoryRequest(4, directoryV3SearchBody, testPagingControl(1000, nil)))
	if _, err := readRequest(conn); !errors.Is(err, io.EOF) {
		t.Fatal("malformed marker must close without data, success or a blocked search", err)
	}
}
