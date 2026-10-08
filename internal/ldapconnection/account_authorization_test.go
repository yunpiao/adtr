package ldapconnection

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestAuthorizationDenialPrecedesDNS(t *testing.T) {
	cfg := fixtureConfig(t, LDAPS, nil)
	cfg.DialIP = netip.Addr{}
	cfg.Authorize = func(_ context.Context, stage Stage) error {
		if stage != StageDNS {
			t.Errorf("unexpected phase %s", stage)
		}
		return errors.New("revoked")
	}
	network := transport{
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			t.Fatal("DNS after authorization denial")
			return nil, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial after authorization denial")
			return nil, nil
		},
	}
	_, err := probe(context.Background(), cfg, fixtureCredential(), network)
	assertError(t, err, CodeAuthorizationRevoked, StageDNS)
}

func TestAuthorizationDenialPrecedesTLSAndJoinsConnection(t *testing.T) {
	for _, mode := range []Mode{LDAPS, StartTLS} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := fixtureConfig(t, mode, nil)
			cfg.Authorize = func(_ context.Context, stage Stage) error {
				if stage == StageTLS {
					return errors.New("revoked")
				}
				if stage != StageDial {
					t.Errorf("unauthorized later phase %s", stage)
				}
				return nil
			}
			closed := make(chan struct{})
			network := pipeTransport(t, mode, func(conn net.Conn) error {
				defer close(closed)
				return expectNoApplicationBytes(conn)
			})
			_, err := probe(context.Background(), cfg, fixtureCredential(), network)
			assertError(t, err, CodeAuthorizationRevoked, StageTLS)
			// The adapter returned after closing its transport and joining its watcher;
			// the peer can observe closure without any TLS or StartTLS bytes.
			<-closed
		})
	}
}
