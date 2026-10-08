//go:build integration

package ldapconnection

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestAccountWireFixtureExercisesRealTCPAndTLS(t *testing.T) {
	for _, mode := range []Mode{StartTLS, LDAPS} {
		t.Run(string(mode), func(t *testing.T) {
			certificate, roots := trustedFixture(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			credential := fixtureCredential()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
				done <- ServeAccountWireForIntegrationTest(ctx, conn, mode, certificate, credential.Username, string(credential.Password))
			}()
			result, err := ProbeWithDialForIntegrationTest(ctx, fixtureConfig(t, mode, roots), credential, func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.DCHostName != "dc1.example.test" || result.DefaultNamingContext != "DC=example,DC=test" {
				t.Fatal("synthetic rootDSE not parsed")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("synthetic server did not observe connection close")
			}
		})
	}
}
