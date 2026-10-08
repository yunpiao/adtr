//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

func TestDirectoryModePageShapesAndOpaqueContinuations(t *testing.T) {
	for _, test := range []struct {
		name        string
		empty, slow bool
		pages, rows int
	}{
		{"default", false, false, 2, 30},
		{"empty", true, false, 1, 0},
		{"slow", false, true, 5, 30},
		{"empty slow", true, true, 5, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				session := directorySession{empty: test.empty, slow: test.slow}
				var objects []directoryassets.Object
				var cookie, stale []byte
				for page := range test.pages {
					req := request{body: testDirectoryBody(), controls: testPagingControl(1000, cookie)}
					if !session.matches(req) {
						t.Fatalf("page %d rejected its issued opaque continuation", page+1)
					}
					if page > 0 && session.matches(request{body: testDirectoryBody(), controls: testPagingControl(1000, stale)}) {
						t.Fatal("stale continuation rewound the session")
					}
					var wire bytes.Buffer
					started := time.Now()
					if err := session.writePage(context.Background(), &wire, page+4); err != nil {
						t.Fatal(err)
					}
					wantDelay := time.Duration(0)
					if test.slow {
						wantDelay = 2 * time.Second
					}
					if elapsed := time.Since(started); elapsed != wantDelay {
						t.Fatalf("page delay=%s, want %s", elapsed, wantDelay)
					}
					rows, next := receiveDirectoryPageForCounts(t, &wire, page+4, test.rows/test.pages, test.rows)
					if wire.Len() != 0 {
						t.Fatal("page emitted trailing data")
					}
					objects = append(objects, rows...)
					if page+1 < test.pages {
						if len(next) != 20 || !bytes.Equal(next[:4], []byte{0, 255, 128, 0}) || bytes.Equal(next, cookie) {
							t.Fatal("continuation must be fresh opaque bytes")
						}
					} else if len(next) != 0 || len(session.cookie) != 0 {
						t.Fatal("final page did not terminate and clear its cookie")
					}
					stale, cookie = cookie, next
				}
				if test.rows != 0 {
					assertDictionary(t, objects)
				} else if len(objects) != 0 {
					t.Fatal("empty mode returned synthetic objects")
				}
				for _, replay := range [][]byte{nil, stale} {
					if session.matches(request{body: testDirectoryBody(), controls: testPagingControl(1000, replay)}) {
						t.Fatal("completed session accepted another page")
					}
				}
			})
		})
	}
}

func TestDirectorySlowWaitHonorsContextWithoutWriting(t *testing.T) {
	for _, alreadyCancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(alreadyCancelled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if alreadyCancelled {
					cancel()
				}
				session := directorySession{empty: true, slow: true}
				var wire bytes.Buffer
				done := make(chan error, 1)
				started := time.Now()
				go func() { done <- session.writePage(ctx, &wire, 4) }()
				synctest.Wait()
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("slow wait ignored cancellation: %v", err)
				}
				if time.Since(started) != 0 || wire.Len() != 0 || session.page != 0 || session.cookie != nil {
					t.Fatal("cancelled wait delayed shutdown, wrote a response or advanced the session")
				}
			})
		})
	}
}

// The dial is the only injected part of the production reader: net.Pipe carries
// real certificate-verified TLS, bind, RootDSE and RFC2696 BER. These tests do
// not exercise an OS listener, network routing, a database or a real AD server.
func readModeFixture(ctx context.Context, fixture *server, clientTLS *tls.Config, mode ldapconnection.Mode,
	credential ldapconnection.Credential, authorize ldapconnection.DirectoryReadAuthorization,
	wrap func(net.Conn) net.Conn) (ldapconnection.DirectoryObservation, error, <-chan struct{}) {
	cfg := ldapconnection.DirectoryConfig{
		Mode: mode, ServerName: "dc.synthetic.invalid", Domain: "SYNTHETIC.INVALID.",
		DialIP: netip.MustParseAddr("127.0.0.1"), Roots: clientTLS.RootCAs,
		AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Limits: directoryassets.Limits{MaxRows: 10000, MaxPages: 100, MaxPageEntries: 1000,
			MaxBytes: 16 << 20, MaxCookieBytes: 65536},
		Authorize: authorize,
	}
	done := make(chan struct{})
	dials := 0
	result, err := ldapconnection.ReadDirectoryWithDialForIntegrationTest(ctx, cfg, credential,
		func(_ context.Context, network, address string) (net.Conn, error) {
			dials++
			port := "636"
			if mode == ldapconnection.StartTLS {
				port = "389"
			}
			if network != "tcp" || address != "127.0.0.1:"+port || dials != 1 {
				return nil, fmt.Errorf("unexpected reader destination")
			}
			client, peer := net.Pipe()
			if wrap != nil {
				peer = wrap(peer)
			}
			go func() {
				defer close(done)
				// Reader cancellation must close its transport independently of
				// the fixture's existing 15-second connection context.
				fixture.handle(context.Background(), peer, mode == ldapconnection.LDAPS)
			}()
			return client, nil
		})
	return result, err, done
}

func TestDirectoryActualReaderEmptyAndSlowOverTLS(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		for _, test := range []struct {
			name        string
			empty, slow bool
			pages       int
		}{
			{"empty", true, false, 1},
			{"empty slow", true, true, 5},
			{"nonempty slow", false, true, 5},
		} {
			t.Run(string(mode)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				fixture, clientTLS := testServer(t)
				fixture.directoryMode, fixture.directoryEmpty, fixture.directorySlow = true, test.empty, test.slow
				var stages []ldapconnection.DirectoryStage
				credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
				started := time.Now()
				result, err, done := readModeFixture(context.Background(), fixture, clientTLS, mode, credential,
					func(_ context.Context, stage ldapconnection.DirectoryStage) error {
						stages = append(stages, stage)
						return nil
					}, nil)
				if err != nil {
					t.Fatalf("production reader rejected mode: %v", err)
				}
				if test.slow && time.Since(started) < 10*time.Second {
					t.Fatal("five slow pages did not each incur the fixed delay")
				}
				if !bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
					t.Fatal("reader did not consume and clear credential")
				}
				if test.empty {
					if len(result.Objects) != 0 {
						t.Fatal("empty mode returned objects")
					}
				} else {
					assertDictionary(t, result.Objects)
				}
				if result.Source.Pages != test.pages || result.Source.Domain != "synthetic.invalid" ||
					result.Source.NamingContext != "dc=synthetic,dc=invalid" || result.Source.DCHostName != "DC.Synthetic.Invalid." {
					t.Fatalf("unexpected directory source: %+v", result.Source)
				}
				want := []ldapconnection.DirectoryStage{ldapconnection.DirectoryValidate, ldapconnection.DirectoryDial,
					ldapconnection.DirectoryTLS, ldapconnection.DirectoryBind, ldapconnection.DirectoryRootDSE}
				for range test.pages {
					want = append(want, ldapconnection.DirectoryPage)
				}
				want = append(want, ldapconnection.DirectoryReturn)
				if !reflect.DeepEqual(stages, want) {
					t.Fatalf("unexpected actual page authorization sequence: %v", stages)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("production reader did not close the fixture transport")
				}
			})
		}
	}
}

type directoryReadSignal struct {
	net.Conn
	armed, read chan struct{}
	once        sync.Once
}

func (c *directoryReadSignal) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		select {
		case <-c.armed:
			c.once.Do(func() { close(c.read) })
		default:
		}
	}
	return n, err
}

func TestDirectoryActualReaderCancellationDuringFourthSlowEmptyPage(t *testing.T) {
	for _, mode := range []ldapconnection.Mode{ldapconnection.StartTLS, ldapconnection.LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			fixture, clientTLS := testServer(t)
			fixture.directoryMode, fixture.directoryEmpty, fixture.directorySlow = true, true, true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			armed, pageRead := make(chan struct{}), make(chan struct{})
			type outcome struct {
				result ldapconnection.DirectoryObservation
				err    error
				done   <-chan struct{}
			}
			returned := make(chan outcome, 1)
			pages := 0
			credential := ldapconnection.Credential{Username: testUsername, Password: []byte(testPassword)}
			go func() {
				result, err, done := readModeFixture(ctx, fixture, clientTLS, mode, credential,
					func(_ context.Context, stage ldapconnection.DirectoryStage) error {
						if stage == ldapconnection.DirectoryPage {
							pages++
							if pages == 4 {
								close(armed)
							}
						}
						return nil
					}, func(conn net.Conn) net.Conn { return &directoryReadSignal{Conn: conn, armed: armed, read: pageRead} })
				returned <- outcome{result, err, done}
			}()
			select {
			case <-pageRead:
			case result := <-returned:
				t.Fatalf("reader returned before the fourth page request: %v", result.err)
			case <-time.After(12 * time.Second):
				t.Fatal("reader did not reach actual fourth-page I/O")
			}
			cancel()
			var got outcome
			select {
			case got = <-returned:
			case <-time.After(time.Second):
				t.Fatal("reader failed to cancel blocked page I/O promptly")
			}
			var failure *ldapconnection.Error
			if !errors.As(got.err, &failure) || failure.Code != ldapconnection.CodeCancelled || failure.Stage != ldapconnection.StageSearch {
				t.Fatalf("unexpected cancellation failure: %v", got.err)
			}
			if pages != 4 || !reflect.DeepEqual(got.result, ldapconnection.DirectoryObservation{}) {
				t.Fatal("cancelled reader advanced paging or returned a partial observation")
			}
			if !bytes.Equal(credential.Password, make([]byte, len(testPassword))) {
				t.Fatal("cancelled reader retained the credential")
			}
			select {
			case <-got.done:
			case <-time.After(3 * time.Second):
				t.Fatal("fixture handler did not join after its bounded page delay")
			}
		})
	}
}
