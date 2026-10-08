package ldapconnection

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/directoryassets"
)

func TestDirectoryCookieEnvelopeDoesNotWidenRootDSE(t *testing.T) {
	cookie := bytes.Repeat([]byte{0x80}, 65536)
	controls := element(0x30, octets(pagedResultsOID), element(4, element(0x30, directoryInteger(0), element(4, cookie))))
	packet := element(0x30, directoryInteger(260), element(0x65, []byte{10, 1, 0, 4, 0, 4, 0}), element(0xa0, controls))
	if _, _, err := readMessage(bytes.NewReader(packet), 260); err == nil {
		t.Fatal("RootDSE message bound was widened")
	}
	m, err := readDirectoryMessage(bytes.NewReader(packet), 260, maxDirectoryMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(m.packet)
	got, err := directoryCookie(m.controls, len(cookie))
	if err != nil || !bytes.Equal(got, cookie) {
		t.Fatal("bounded opaque cookie did not survive its envelope")
	}
	clear(got)
	if _, err := directoryCookie(m.controls, len(cookie)-1); err == nil {
		t.Fatal("cookie cap ignored")
	}
}

func TestDirectoryMessageIDEncodingAcrossByteBoundary(t *testing.T) {
	for _, id := range []int{4, 127, 128, 255, 256, 10003} {
		packet := directorySearchPacket("dc=synthetic,dc=test", id, 1000, []byte{0, 0x80, 0xff})
		m, err := readDirectoryMessage(bytes.NewReader(packet), id, maxDirectoryMessageBytes)
		if err != nil || m.tag != 0x63 {
			t.Fatalf("message ID %d: %v", id, err)
		}
		clear(m.packet)
		clear(packet)
	}
}

type directoryHeaderOnlyReader struct {
	header   []byte
	bodyRead bool
}

func (r *directoryHeaderOnlyReader) Read(p []byte) (int, error) {
	if len(r.header) == 0 {
		r.bodyRead = true
		return 0, errors.New("forbidden body allocation/read")
	}
	n := copy(p, r.header)
	r.header = r.header[n:]
	return n, nil
}

func TestDirectoryRejectsWireBoundsBeforeBodyRead(t *testing.T) {
	for _, tc := range []struct {
		header    []byte
		remaining int
	}{
		{[]byte{0x30, 0x83, 2, 0, 1}, maxDirectoryMessageBytes},
		{[]byte{0x30, 0x82, 0x10, 0}, 4095},
		{[]byte{0x30, 0x80}, maxDirectoryMessageBytes},
		{[]byte{0x30, 0x84}, maxDirectoryMessageBytes},
		{[]byte{0x30, 0x82, 0, 0x80}, maxDirectoryMessageBytes},
	} {
		r := &directoryHeaderOnlyReader{header: tc.header}
		if _, err := readDirectoryMessage(r, 4, tc.remaining); err == nil || r.bodyRead {
			t.Fatal("invalid envelope reached body allocation/read")
		}
	}
}

type directoryPartialReader struct {
	header      bool
	retained    []byte
	panicOnRead bool
}

func (r *directoryPartialReader) Read(p []byte) (int, error) {
	if !r.header {
		r.header = true
		copy(p, []byte{0x30, 20})
		return 2, nil
	}
	r.retained = p
	copy(p, []byte("private-partial-data"))
	if r.panicOnRead {
		panic("synthetic read panic")
	}
	return len(p) - 1, io.ErrUnexpectedEOF
}

func TestDirectoryReadFailureClearsPacketBuffer(t *testing.T) {
	r := &directoryPartialReader{}
	if _, err := readDirectoryMessage(r, 4, maxDirectoryMessageBytes); err == nil {
		t.Fatal("partial message accepted")
	}
	if len(r.retained) == 0 || !bytes.Equal(r.retained, make([]byte, len(r.retained))) {
		t.Fatal("partial packet buffer survived failure")
	}
}

func TestResponseReadersClearBuffersOnFailureAndPanic(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, panics := range []bool{false, true} {
			r := &directoryPartialReader{panicOnRead: panics}
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				if directory {
					if _, err := readDirectoryMessage(r, 4, maxDirectoryMessageBytes); err == nil {
						t.Error("partial message accepted")
					}
				} else {
					if _, _, err := readMessage(r, 4); err == nil {
						t.Error("partial RootDSE message accepted")
					}
				}
			}()
			if panicked != panics {
				t.Fatal("panic fixture did not run as expected")
			}
			if len(r.retained) == 0 || !bytes.Equal(r.retained, make([]byte, len(r.retained))) {
				t.Fatal("partial packet survived failed or panicking read")
			}
		}
	}
}

type retainedResponseReader struct {
	*bytes.Reader
	buffers [][]byte
}

func (r *retainedResponseReader) Read(p []byte) (int, error) {
	if len(p) > 5 {
		r.buffers = append(r.buffers, p)
	}
	return r.Reader.Read(p)
}

func TestRootResponseReaderTransfersBodyAndClearsWirePacket(t *testing.T) {
	for _, valid := range []bool{false, true} {
		packet := message(4, 0x64, []byte("private directory response"))
		r := &retainedResponseReader{Reader: bytes.NewReader(packet)}
		id := 5
		if valid {
			id = 4
		}
		_, body, err := readMessage(r, id)
		if valid {
			if err != nil || string(body) != "private directory response" {
				t.Fatal("body was lost during transfer")
			}
			clear(body)
		} else if err == nil {
			t.Fatal("incorrect ID accepted")
		}
		if len(r.buffers) != 1 || !bytes.Equal(r.buffers[0], make([]byte, len(r.buffers[0]))) {
			t.Fatal("wire response survived successful or failed parse")
		}
	}
}

type directoryPanicWriter struct {
	net.Conn
	retained  []byte
	sawCookie bool
}

func (w *directoryPanicWriter) SetDeadline(time.Time) error { return nil }
func (w *directoryPanicWriter) Write(p []byte) (int, error) {
	w.retained = p
	w.sawCookie = bytes.Contains(p, []byte("opaque-cookie"))
	panic("synthetic directory write panic")
}

func TestDirectoryPageClearsPacketOnPanic(t *testing.T) {
	w := &directoryPanicWriter{}
	cfg := DirectoryConfig{Limits: directoryassets.Limits{MaxRows: 10, MaxPages: 10, MaxPageEntries: 10, MaxBytes: 10000, MaxCookieBytes: 100}, Authorize: func(context.Context, DirectoryStage) error { return nil }}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, _, _ = directoryPage(context.Background(), w, cfg, "dc=synthetic,dc=test", 4, []byte("opaque-cookie"))
	}()
	if !panicked || !w.sawCookie || len(w.retained) == 0 {
		t.Fatal("panic fixture did not run")
	}
	if !bytes.Equal(w.retained, make([]byte, len(w.retained))) {
		t.Fatal("page request cookie survived panic unwind")
	}
}

func FuzzDirectoryMessage(f *testing.F) {
	f.Add(directorySearchPacket("dc=synthetic,dc=test", 4, 10, nil))
	f.Add([]byte{0x30, 0x80})
	f.Fuzz(func(t *testing.T, packet []byte) {
		m, err := readDirectoryMessage(bytes.NewReader(packet), 4, maxDirectoryMessageBytes)
		if err != nil {
			return
		}
		defer clear(m.packet)
		if len(m.packet) > maxDirectoryMessageBytes {
			t.Fatal("packet exceeds allocation limit")
		}
		_, _ = directoryCookie(m.controls, 65536)
		if m.tag == 0x64 {
			_, _ = directoryEntry(m.body, "dc=synthetic,dc=test")
		}
	})
}
