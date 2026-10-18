//go:build adtr_ci_dbadmission && linux && amd64

package dbadmission

import (
	"bytes"
	"encoding/binary"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Mapping ownership lasts until process exit; the parent retains its mapping until Wait.
// No unmap while an invalidator can still run.
type ciWitness struct {
	page    []byte
	invalid *uint32
	bound   *uint32
}

func mapCIWitness(file *os.File, nonce [16]byte) (*ciWitness, error) {
	info, err := file.Stat()
	if err != nil || info.Size() != 4096 {
		return nil, errSealProtocol
	}
	page, err := syscall.Mmap(int(file.Fd()), 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, errSealProtocol
	}
	if len(page) != 4096 || string(page[:8]) != "ADTRGATE" || binary.LittleEndian.Uint32(page[8:12]) != 1 || binary.LittleEndian.Uint32(page[12:16]) != 4096 || binary.LittleEndian.Uint64(page[16:24]) != 1 || !bytes.Equal(page[32:48], nonce[:]) || !bytes.Equal(page[48:], make([]byte, 4096-48)) {
		return nil, errSealProtocol
	}
	// mmap is page aligned; both offsets are multiples of the uint32 alignment.
	if uintptr(unsafe.Pointer(&page[24]))%4 != 0 || uintptr(unsafe.Pointer(&page[28]))%4 != 0 {
		return nil, errSealProtocol
	}
	invalid := (*uint32)(unsafe.Pointer(&page[24]))
	bound := (*uint32)(unsafe.Pointer(&page[28]))
	if atomic.LoadUint32(invalid) != 0 || !atomic.CompareAndSwapUint32(bound, 0, 1) {
		return nil, errSealProtocol
	}
	return &ciWitness{page: page, invalid: invalid, bound: bound}, nil
}
func (w *ciWitness) invalidate() { atomic.StoreUint32(w.invalid, 1) }
