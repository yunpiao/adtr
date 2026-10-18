//go:build adtr_ci_dbadmission

package dbadmission

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplicationRequestClosedSchema(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"epoch":1,"seq":2,"op":"snapshot","seq":3}`,
		`{"version":2,"epoch":1,"seq":1,"op":"snapshot"}`,
		`{"version":2,"epoch":2,"seq":2,"op":"snapshot"}`,
		`{"version":2,"epoch":1,"seq":2,"op":"snapshot","secret":1}`,
		`{"version":2,"epoch":1,"seq":2,"op":"snapshot"} {}`,
	} {
		if _, err := readApplicationRequest(bufio.NewReaderSize(strings.NewReader(raw+"\n"), 1024), 1024, 1); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	r, err := readApplicationRequest(bufio.NewReader(strings.NewReader("{\"version\":2,\"epoch\":1,\"seq\":3,\"op\":\"snapshot\"}\n")), 1024, 1)
	if err != nil || r.Seq != 3 {
		t.Fatal("monotonic gap rejected")
	}
}
func TestApplicationSnapshotCompleteStatesAndLimits(t *testing.T) {
	g, target := newSealProtocolTestGate()
	pending, _ := g.acquire(context.Background())
	other, _ := g.acquire(context.Background())
	attachTestTicket(other)
	other.closingConnection()
	frame, err := g.applicationSnapshot(1, "reply")
	if err != nil || len(frame.Records) != 3 || frame.Pending != 1 || frame.Open != 2 || frame.Records[0].Candidate == nil || frame.Records[1].State != "pending" || frame.Records[2].State != "closing" {
		t.Fatal("incomplete registry")
	}
	if g.pin(1, target, nil) != nil {
		t.Fatal("pin")
	}
	frame, _ = g.applicationSnapshot(2, "reply")
	if frame.Status != "draining" {
		t.Fatal("premature seal")
	}
	attachTestTicket(pending)
	pending.closed(true)
	other.closed(true)
	frame, _ = g.applicationSnapshot(3, "reply")
	if frame.Status != "sealed" || len(frame.Records) != 1 || frame.Target != target {
		t.Fatal("seal target lost")
	}
	if g.release(1) != nil || g.release(1) != nil {
		t.Fatal("release replay")
	}
	g.seal.target.closed(true)
	frame, _ = g.applicationSnapshot(4, "reply")
	if frame.Status != "released" || len(frame.Records) != 0 {
		t.Fatal("released lifecycle changed")
	}
	g = &lifecycleGate{}
	for i := 0; i < 129; i++ {
		g.acquire(context.Background())
	}
	if _, err = g.applicationSnapshot(1, "reply"); err == nil || !g.invalid {
		t.Fatal("registry truncated")
	}
}
func TestApplicationWitnessNeverClearedOnRelease(t *testing.T) {
	g, target := newSealProtocolTestGate()
	if g.pin(1, target, nil) != nil {
		t.Fatal("pin")
	}
	g.invalidate()
	if g.release(1) != nil {
		t.Fatal("release")
	}
	f, err := g.applicationSnapshot(1, "reply")
	if err != nil || f.Status != "invalid" {
		t.Fatal("release masked invalid")
	}
}

func TestApplicationCleanupCannotErasePreviouslyObservedEOF(t *testing.T) {
	for _, remoteEOF := range []bool{true, false} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal("owned pipe")
		}
		defer reader.Close()
		defer writer.Close()
		if remoteEOF {
			writer.Close()
		} else {
			reader.Close()
		}
		_, original := readApplicationRequest(bufio.NewReader(reader), 1024, 0)
		if remoteEOF && original != io.EOF {
			t.Fatal("EOF fixture")
		}
		if !remoteEOF && !errors.Is(original, os.ErrClosed) {
			t.Fatal("local Close fixture")
		}
		// The read has completed. Cleanup starts before the saved error is handled.
		var stopping atomic.Bool
		stopping.Store(true)
		g := &lifecycleGate{}
		failures := 0
		recordApplicationFailure(reader, g, &stopping, func() { failures++ }, original)
		if g.invalid != remoteEOF || failures != map[bool]int{true: 1, false: 0}[remoteEOF] {
			t.Fatal("cleanup relabelled terminal error")
		}
	}
}
