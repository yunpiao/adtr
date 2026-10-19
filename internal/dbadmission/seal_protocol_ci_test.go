//go:build adtr_ci_dbadmission

package dbadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSealProtocolClosedFramesAndPrivacy(t *testing.T) {
	g, target := newSealProtocolTestGate()
	var output bytes.Buffer
	input := fmt.Sprintf("{\"version\":1,\"epoch\":1,\"op\":\"pin\",\"target\":%d}\n{\"version\":1,\"epoch\":1,\"op\":\"release\"}\n", target)
	if runSealProtocol(context.Background(), strings.NewReader(input), &output, g) != nil {
		t.Fatal("protocol failed")
	}
	decoder := json.NewDecoder(&output)
	var one, two sealFrame
	if decoder.Decode(&one) != nil || decoder.Decode(&two) != nil || one.Status != "sealed" || two.Status != "released" {
		t.Fatal("unexpected protocol states")
	}
	for _, frame := range []sealFrame{one, two} {
		data, _ := json.Marshal(frame)
		if strings.Contains(string(data), "private") || strings.Contains(string(data), "backend") || strings.Contains(string(data), "identity") {
			t.Fatal("private identity leaked")
		}
	}
}

func TestSealProtocolMalformedEOFAndReplayFailClosed(t *testing.T) {
	for _, tail := range []string{"", "{", "{\"version\":1,\"epoch\":2,\"op\":\"release\"}\n", "{\"version\":1,\"epoch\":1,\"op\":\"status\",\"sql\":\"private-value\"}\n", strings.Repeat("x", 1025) + "\n", "{\"version\":1,\"epoch\":1,\"op\":\"pin\",\"target\":2}\n"} {
		g, target := newSealProtocolTestGate()
		var output bytes.Buffer
		input := fmt.Sprintf("{\"version\":1,\"epoch\":1,\"op\":\"pin\",\"target\":%d}\n", target) + tail
		if runSealProtocol(context.Background(), strings.NewReader(input), &output, g) == nil || g.frame(1).Status != "invalid" {
			t.Fatal("malformed or incomplete stream accepted")
		}
		if !g.closing || strings.Contains(output.String(), "private-value") {
			t.Fatal("invalid stream reopened or leaked data")
		}
	}
}

type shortSealWriter struct{}

func (shortSealWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestSealProtocolLostAcknowledgementCannotSucceed(t *testing.T) {
	g, target := newSealProtocolTestGate()
	input := fmt.Sprintf("{\"version\":1,\"epoch\":1,\"op\":\"pin\",\"target\":%d}\n", target)
	if runSealProtocol(context.Background(), strings.NewReader(input), shortSealWriter{}, g) == nil || !g.closing || g.frame(1).Status != "invalid" {
		t.Fatal("lost acknowledgement accepted")
	}
}

func TestSealWaitDoesNotDispatchConnector(t *testing.T) {
	g := installTestGate(t)
	ticket, _ := g.acquire(context.Background())
	conn := attachTestTicket(ticket)
	_, _ = g.register(conn, testSealIdentity())
	if g.pin(1, g.candidates()[0].generation, nil) != nil {
		t.Fatal("pin failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := connectWith(ctx, nil, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
			calls.Add(1)
			return nil, errors.New("unexpected connector")
		})
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("admission returned while sealed")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	if <-done != context.Canceled || calls.Load() != 0 {
		t.Fatal("canceled sealed wait dispatched or changed error")
	}
}

type observedSealPipeWriter struct {
	file    *os.File
	entered chan struct{}
	once    sync.Once
}

func (w *observedSealPipeWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	return w.file.Write(p)
}
func (w *observedSealPipeWriter) Close() error { return w.file.Close() }

func TestSealProtocolBlockedOwnedPipeWriteInterruptedByContext(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("owned pipe failed")
	}
	defer reader.Close()
	defer writer.Close()
	// Fill this test's pipe without a reader, using a bounded native write.
	payload := bytes.Repeat([]byte{'x'}, 1<<20)
	if writer.SetWriteDeadline(time.Now().Add(20*time.Millisecond)) != nil {
		t.Fatal("pipe deadline unavailable")
	}
	written, err := writer.Write(payload)
	if written == 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("pipe did not reach bounded blocked state")
	}
	if writer.SetWriteDeadline(time.Time{}) != nil {
		t.Fatal("pipe deadline reset failed")
	}
	observed := &observedSealPipeWriter{file: writer, entered: make(chan struct{})}
	g, target := newSealProtocolTestGate()
	input := fmt.Sprintf("{\"version\":1,\"epoch\":1,\"op\":\"pin\",\"target\":%d}\n", target)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runSealProtocol(ctx, strings.NewReader(input), observed, g) }()
	select {
	case <-observed.entered:
	case <-time.After(time.Second):
		t.Fatal("protocol write did not start")
	}
	select {
	case <-done:
		t.Fatal("full pipe did not block protocol write")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted write reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("context failed to interrupt owned pipe")
	}
	if g.frame(1).Status != "invalid" || !g.closing {
		t.Fatal("transport interruption reopened admission")
	}
}
