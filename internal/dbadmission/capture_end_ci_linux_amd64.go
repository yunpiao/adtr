//go:build adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"bytes"
	"encoding/binary"
	"os"
	"syscall"
)

// The independent rescuer is the sole writer. Its one nonblocking write is
// below PIPE_BUF and precedes every capture-related CONT from that process.
// No shared-memory atomic-write assumption is needed for this authorization.
func prepareCaptureEnd(file *os.File) (int, error) {
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 0, errSealProtocol
	}
	fd := int(file.Fd())
	if fd < 3 || syscall.SetNonblock(fd, true) != nil {
		return 0, errSealProtocol
	}
	return fd, nil
}

// Caller holds g.mu. Only three capture-liveness predicates call this method;
// transport, dirty/native Close and all other hard failures never consult it.
func (g *lifecycleGate) captureObligationsEndedLocked() bool {
	s := g.seal
	if s == nil || g.invalid {
		return false
	}
	if s.captureEnded {
		return true
	}
	if g.captureEndFD < 3 {
		return false
	}
	var data [49]byte
	n, err := syscall.Read(g.captureEndFD, data[:])
	if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
		return false
	}
	// EINTR is a failed authorization rather than an unbounded retry under mu.
	if err != nil || n != 48 || !bytes.Equal(data[:8], []byte("ADTREND1")) ||
		!bytes.Equal(data[8:24], g.nonce[:]) || binary.BigEndian.Uint64(data[24:32]) != s.epoch ||
		binary.BigEndian.Uint64(data[32:40]) != s.target.generation ||
		binary.BigEndian.Uint64(data[40:48]) != s.registration.generation ||
		s.released || !g.closing || g.pending != 0 || g.open != 1 ||
		s.target.state != 'o' || s.target.registration != s.registration {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return false
	}
	s.captureEnded = true
	return true
}
