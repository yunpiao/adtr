//go:build adtr_ci_dbadmission

package dbadmission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
)

type sealRequest struct {
	Version int    `json:"version"`
	Epoch   uint64 `json:"epoch"`
	Op      string `json:"op"`
	Target  uint64 `json:"target,omitempty"`
}

type sealFrame struct {
	Version int    `json:"version"`
	Epoch   uint64 `json:"epoch"`
	Status  string `json:"status"`
	Pending int    `json:"pending"`
	Open    int    `json:"open"`
}

// This transport has no application caller or exported installation API.
// Its only callers are isolated CI helper tests. A caller owns pipe lifetime.
func runSealProtocol(ctx context.Context, requests io.Reader, replies io.Writer, gate *lifecycleGate) error {
	var writeMu sync.Mutex
	var invalidOnce sync.Once
	done := make(chan struct{})
	defer close(done)
	closePipes := func() {
		if c, ok := requests.(io.Closer); ok {
			_ = c.Close()
		}
		if c, ok := replies.(io.Closer); ok {
			_ = c.Close()
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			gate.invalidateTransport()
			closePipes()
		case <-done:
		}
	}()
	send := func(frame sealFrame) error {
		data, err := json.Marshal(frame)
		if err != nil {
			return errSealProtocol
		}
		data = append(data, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		n, err := replies.Write(data)
		if err != nil || n != len(data) {
			gate.invalidateTransport()
			closePipes()
			return errSealProtocol
		}
		return nil
	}
	publishInvalid := func() {
		invalidOnce.Do(func() { frame := gate.frame(1); frame.Status = "invalid"; _ = send(frame) })
	}
	reader := bufio.NewReaderSize(requests, 1024)
	for count := 0; count < 128; count++ {
		line, err := reader.ReadSlice('\n')
		if err != nil || len(line) > 512 {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		var request sealRequest
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.Version != 1 || request.Epoch != 1 || ctx.Err() != nil {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		switch request.Op {
		case "status":
			if request.Target != 0 {
				err = errSealProtocol
			}
		case "pin":
			err = gate.pin(request.Epoch, request.Target, publishInvalid)
		case "release":
			if request.Target != 0 {
				err = errSealProtocol
			} else {
				err = gate.release(request.Epoch)
			}
		default:
			err = errSealProtocol
		}
		if err != nil {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		if err = send(gate.frame(request.Epoch)); err != nil {
			return err
		}
		if request.Op == "release" {
			return nil
		}
	}
	gate.invalidateTransport()
	publishInvalid()
	return errSealProtocol
}
