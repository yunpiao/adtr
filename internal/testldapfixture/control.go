//go:build integration

package main

// This optional control is confined to the disposable integration fixture. It
// exposes only a single-use RootDSE hold, never commands or arbitrary paths.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

const controlHoldLimit = 2 * time.Second

type fixtureControl struct {
	root   *os.Root
	mu     sync.Mutex
	used   map[string]bool
	active bool
}

type controlMarker struct {
	Version int    `json:"version"`
	Ticket  string `json:"ticket"`
	Phase   string `json:"phase"`
}

func openFixtureControl(path string) (*fixtureControl, error) {
	if path == "" {
		return nil, nil
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.New("synthetic LDAP fixture control unavailable")
	}
	return &fixtureControl{root: root, used: make(map[string]bool)}, nil
}

func validControlTicket(ticket string) bool {
	if len(ticket) != 32 {
		return false
	}
	for _, b := range ticket {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

// Reject unknown fields, duplicate keys, oversized/nonregular inputs and trailing
// data. File names always come from constants and validated ticket characters.
func (c *fixtureControl) read(name, kind, value string) (string, bool) {
	info, err := c.root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256 {
		return "", false
	}
	f, err := c.root.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil || len(data) > 256 {
		return "", false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return "", false
	}
	fields := make(map[string]json.RawMessage, 3)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return "", false
		}
		name, ok := key.(string)
		if !ok || (name != "version" && name != "ticket" && name != kind) || fields[name] != nil {
			return "", false
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return "", false
		}
		fields[name] = raw
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') || len(fields) != 3 {
		return "", false
	}
	if _, err := d.Token(); err != io.EOF {
		return "", false
	}
	var version int
	var ticket, operation string
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 ||
		json.Unmarshal(fields["ticket"], &ticket) != nil || !validControlTicket(ticket) ||
		json.Unmarshal(fields[kind], &operation) != nil || operation != value {
		return "", false
	}
	return ticket, true
}

func markerName(ticket, phase string) string { return "fixture-" + ticket + "-" + phase + ".json" }

func (c *fixtureControl) mark(ticket, phase string) error {
	data, err := json.Marshal(controlMarker{Version: 1, Ticket: ticket, Phase: phase})
	if err != nil {
		return err
	}
	name := markerName(ticket, phase)
	tmp := name + ".tmp"
	f, err := c.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer c.root.Remove(tmp)
	err = writeAll(f, data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return c.root.Rename(tmp, name)
}

func (c *fixtureControl) claim() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active || len(c.used) >= 1024 {
		return ""
	}
	ticket, ok := c.read("fixture-arm.json", "operation", "hold_rootdse")
	if !ok || c.used[ticket] {
		return ""
	}
	for _, phase := range []string{"release", "reached", "released", "response_written", "handler_closed", "expired"} {
		if _, err := c.root.Lstat(markerName(ticket, phase)); !errors.Is(err, os.ErrNotExist) {
			return ""
		}
	}
	c.used[ticket], c.active = true, true
	return ticket
}

func (c *fixtureControl) wait(ctx context.Context, ticket string) bool {
	expires := time.Now().Add(controlHoldLimit)
	deadline := time.NewTimer(controlHoldLimit)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	if c.mark(ticket, "reached") != nil {
		return false
	}
	for {
		select {
		case <-ctx.Done():
			_ = c.mark(ticket, "expired")
			return false
		case <-deadline.C:
			_ = c.mark(ticket, "expired")
			return false
		case <-poll.C:
			// A release racing a deadline must not turn expiry into success.
			if ctx.Err() != nil || !time.Now().Before(expires) {
				_ = c.mark(ticket, "expired")
				return false
			}
			if got, ok := c.read(markerName(ticket, "release"), "phase", "release"); ok && got == ticket {
				return c.mark(ticket, "released") == nil
			}
		}
	}
}

func (c *fixtureControl) closed(ticket string) {
	_ = c.mark(ticket, "handler_closed")
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
}
