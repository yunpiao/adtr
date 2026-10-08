//go:build integration

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fixtureTicket = "0123456789abcdef0123456789abcdef"

func controlledServer(t *testing.T) (*server, *tls.Config, string) {
	t.Helper()
	s, config := testServer(t)
	dir := t.TempDir()
	var err error
	s.control, err = openFixtureControl(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.control.root.Close() })
	return s, config, dir
}

func controlWrite(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func armControl(t *testing.T, dir string) {
	t.Helper()
	controlWrite(t, dir, "fixture-arm.json", `{"version":1,"ticket":"`+fixtureTicket+`","operation":"hold_rootdse"}`)
}

func releaseControl(t *testing.T, dir string) {
	t.Helper()
	controlWrite(t, dir, markerName(fixtureTicket, "release"), `{"version":1,"ticket":"`+fixtureTicket+`","phase":"release"}`)
}

func awaitMarker(t *testing.T, dir, phase string) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		data, err := os.ReadFile(filepath.Join(dir, markerName(fixtureTicket, phase)))
		if err == nil {
			var marker map[string]any
			if json.Unmarshal(data, &marker) != nil || len(marker) != 3 || marker["version"] != float64(1) || marker["ticket"] != fixtureTicket || marker["phase"] != phase {
				t.Fatal("invalid marker shape")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %s marker", phase)
}

func noMarker(t *testing.T, dir, phase string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, markerName(fixtureTicket, phase))); !os.IsNotExist(err) {
		t.Fatalf("unexpected %s marker", phase)
	}
}

func bindControlled(t *testing.T, conn net.Conn) {
	t.Helper()
	send(t, conn, bindRequest(1, testUsername, testPassword))
	requireCode(t, conn, 1, 0x61, 0)
}

func TestControlRealTLSHoldReleaseAndSingleUse(t *testing.T) {
	for _, ldaps := range []bool{false, true} {
		t.Run(map[bool]string{false: "StartTLS", true: "LDAPS"}[ldaps], func(t *testing.T) {
			s, config, dir := controlledServer(t)
			armControl(t, dir)
			conn := securePipe(t, s, config, ldaps)
			bindControlled(t, conn)
			send(t, conn, searchRequest(2, "", 0))
			awaitMarker(t, dir, "reached")
			first := make(chan error, 1)
			go func() {
				response, err := readRequest(conn)
				if err == nil && response.tag != 0x64 {
					err = errProtocol
				}
				first <- err
			}()
			// Neither a stale ticket file nor mismatched content releases this hold.
			controlWrite(t, dir, "fixture-ffffffffffffffffffffffffffffffff-release.json", `{"version":1,"ticket":"ffffffffffffffffffffffffffffffff","phase":"release"}`)
			controlWrite(t, dir, markerName(fixtureTicket, "release"), `{"version":1,"ticket":"ffffffffffffffffffffffffffffffff","phase":"release"}`)
			select {
			case err := <-first:
				t.Fatalf("response before matching release: %v", err)
			case <-time.After(60 * time.Millisecond):
			}
			releaseControl(t, dir)
			select {
			case err := <-first:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("release did not return response")
			}
			requireCode(t, conn, 2, 0x65, 0)
			awaitMarker(t, dir, "released")
			awaitMarker(t, dir, "response_written")
			_ = conn.Close()
			awaitMarker(t, dir, "handler_closed")
			noMarker(t, dir, "expired")
			// The unchanged arm cannot consume the ticket again.
			second := securePipe(t, s, config, ldaps)
			bindControlled(t, second)
			send(t, second, searchRequest(2, "", 0))
			receive(t, second, 2, 0x64)
			requireCode(t, second, 2, 0x65, 0)
		})
	}
}

func TestControlInvalidArmCannotHoldRealSearch(t *testing.T) {
	for name, arm := range map[string]string{
		"operation":   `{"version":1,"ticket":"` + fixtureTicket + `","operation":"execute"}`,
		"unknown":     `{"version":1,"ticket":"` + fixtureTicket + `","operation":"hold_rootdse","path":"ignored"}`,
		"duplicate":   `{"version":1,"version":1,"ticket":"` + fixtureTicket + `","operation":"hold_rootdse"}`,
		"ticket path": `{"version":1,"ticket":"../../escape","operation":"hold_rootdse"}`,
		"trailing":    `{"version":1,"ticket":"` + fixtureTicket + `","operation":"hold_rootdse"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, config, dir := controlledServer(t)
			controlWrite(t, dir, "fixture-arm.json", arm)
			conn := securePipe(t, s, config, true)
			bindControlled(t, conn)
			send(t, conn, searchRequest(2, "", 0))
			receive(t, conn, 2, 0x64)
			requireCode(t, conn, 2, 0x65, 0)
			noMarker(t, dir, "reached")
		})
	}
}

func TestControlRejectsStaleTicketFiles(t *testing.T) {
	for _, phase := range []string{"release", "reached", "released", "response_written", "handler_closed", "expired"} {
		t.Run(phase, func(t *testing.T) {
			s, config, dir := controlledServer(t)
			armControl(t, dir)
			controlWrite(t, dir, markerName(fixtureTicket, phase), `{}`)
			conn := securePipe(t, s, config, true)
			bindControlled(t, conn)
			send(t, conn, searchRequest(2, "", 0))
			receive(t, conn, 2, 0x64)
			requireCode(t, conn, 2, 0x65, 0)
			s.control.mu.Lock()
			claimed := s.control.used[fixtureTicket]
			s.control.mu.Unlock()
			if claimed {
				t.Fatal("stale ticket was claimed")
			}
		})
	}
}

func TestControlCannotClaimBeforeTLS(t *testing.T) {
	s, _, dir := controlledServer(t)
	armControl(t, dir)
	conn := startPipe(t, s, false)
	send(t, conn, bindRequest(1, testUsername, testPassword))
	requireCode(t, conn, 1, 0x61, 13)
	send(t, conn, searchRequest(2, "", 0))
	requireCode(t, conn, 2, 0x65, 50)
	noMarker(t, dir, "reached")
}

func TestControlRequiresExactBindAndSupportedRootDSE(t *testing.T) {
	s, config, dir := controlledServer(t)
	armControl(t, dir)
	conn := securePipe(t, s, config, true)
	send(t, conn, bindRequest(1, testUsername, "wrong"))
	requireCode(t, conn, 1, 0x61, 49)
	send(t, conn, searchRequest(2, "", 0))
	requireCode(t, conn, 2, 0x65, 50)
	noMarker(t, dir, "reached")
	bindControlled(t, conn)
	send(t, conn, searchRequest(3, "DC=synthetic,DC=invalid", 0))
	requireCode(t, conn, 3, 0x65, 53)
	noMarker(t, dir, "reached")
	send(t, conn, searchRequest(4, "", 0))
	awaitMarker(t, dir, "reached")
	releaseControl(t, dir)
	receive(t, conn, 4, 0x64)
	requireCode(t, conn, 4, 0x65, 0)
}

func TestControlConcurrentConnectionsConsumeTicketOnce(t *testing.T) {
	s, config, dir := controlledServer(t)
	armControl(t, dir)
	first := securePipe(t, s, config, true)
	second := securePipe(t, s, config, true)
	bindControlled(t, first)
	bindControlled(t, second)
	send(t, first, searchRequest(2, "", 0))
	awaitMarker(t, dir, "reached")
	send(t, second, searchRequest(2, "", 0))
	receive(t, second, 2, 0x64)
	requireCode(t, second, 2, 0x65, 0)
	noMarker(t, dir, "released")
	releaseControl(t, dir)
	receive(t, first, 2, 0x64)
	requireCode(t, first, 2, 0x65, 0)
}

func TestControlTimeoutClosesRealTLSWithoutResponse(t *testing.T) {
	s, config, dir := controlledServer(t)
	armControl(t, dir)
	conn := securePipe(t, s, config, true)
	bindControlled(t, conn)
	started := time.Now()
	send(t, conn, searchRequest(2, "", 0))
	awaitMarker(t, dir, "reached")
	if _, err := readRequest(conn); err == nil {
		t.Fatal("expired hold wrote response")
	}
	if elapsed := time.Since(started); elapsed < controlHoldLimit || elapsed > controlHoldLimit+time.Second {
		t.Fatalf("unexpected hold duration: %v", elapsed)
	}
	awaitMarker(t, dir, "expired")
	awaitMarker(t, dir, "handler_closed")
	noMarker(t, dir, "released")
	noMarker(t, dir, "response_written")
}

func TestControlCancellationDeadlineAndShutdownJoinHeldHandler(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			s, config, dir := controlledServer(t)
			armControl(t, dir)
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			client, peer := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			if mode == "shutdown" {
				listener := newPipeListener()
				go func() { defer close(done); _ = s.serve(ctx, []endpoint{{listener: listener, ldaps: true}}) }()
				listener.connections <- peer
			} else {
				go func() { defer close(done); s.handle(ctx, peer, true) }()
			}
			conn := tls.Client(client, config)
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if err := conn.Handshake(); err != nil {
				t.Fatal(err)
			}
			bindControlled(t, conn)
			send(t, conn, searchRequest(2, "", 0))
			awaitMarker(t, dir, "reached")
			if mode != "deadline" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancelled handler did not join")
			}
			if _, err := readRequest(conn); err == nil {
				t.Fatal("cancelled hold wrote response")
			}
			awaitMarker(t, dir, "handler_closed")
			awaitMarker(t, dir, "expired")
			noMarker(t, dir, "response_written")
		})
	}
}
