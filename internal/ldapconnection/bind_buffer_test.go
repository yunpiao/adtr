package ldapconnection

import (
	"bytes"
	"context"
	"net"
	"testing"
)

type panickingBindWriter struct {
	net.Conn
	retained      []byte
	sawCredential bool
}

func (c *panickingBindWriter) Write(p []byte) (int, error) {
	c.retained = p
	c.sawCredential = bytes.Contains(p, []byte("synthetic-panic-password"))
	panic("synthetic write panic")
}

func TestBindClearsOwnedPacketWhenTransportPanics(t *testing.T) {
	writer := &panickingBindWriter{}
	password := []byte("synthetic-panic-password")
	defer clear(password)
	recovered := false
	func() {
		defer func() {
			if recover() != nil {
				recovered = true
			}
		}()
		_ = bind(context.Background(), writer, Credential{Username: "synthetic-panic-reader", Password: password})
	}()
	if !recovered || !writer.sawCredential || len(writer.retained) == 0 {
		t.Fatal("fixture did not panic while owning the bind packet")
	}
	for _, b := range writer.retained {
		if b != 0 {
			t.Fatal("bind request credential bytes survived panic unwind")
		}
	}
	if !bytes.Equal(password, []byte("synthetic-panic-password")) {
		t.Fatal("adapter mutated caller-owned password")
	}
}
