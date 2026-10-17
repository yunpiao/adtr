//go:build adtr_ci_dbadmission && linux && amd64

package main

import (
	"encoding/hex"
	"errors"
	"github.com/yunpiao/adtr/internal/dbadmission"
	"os"
	"strconv"
	"sync"
	"syscall"
)

func configureCIAdmission(mode string) (<-chan struct{}, func(), error) {
	keys := []string{"ADTR_CI_DB_NORMAL_FD", "ADTR_CI_DB_RESCUE_FD", "ADTR_CI_DB_WITNESS_FD"}
	present := 0
	for _, key := range keys {
		if _, ok := os.LookupEnv(key); ok {
			present++
		}
	}
	if present == 0 {
		return nil, func() {}, nil
	}
	failure := errors.New("CI database controller unavailable")
	if present != 3 || mode != "worker" {
		return nil, nil, failure
	}
	rawNonce, err := hex.DecodeString(os.Getenv("ADTR_E2E_EXPORT_WORKER_NONCE"))
	if err != nil || len(rawNonce) != 16 {
		return nil, nil, failure
	}
	var nonce [16]byte
	copy(nonce[:], rawNonce)
	files := make([]*os.File, 0, 3)
	used := make(map[int]bool)
	closeFiles := func() {
		for _, f := range files {
			f.Close()
		}
	}
	for i, key := range keys {
		fd, e := strconv.Atoi(os.Getenv(key))
		if e != nil || fd < 3 || used[fd] {
			closeFiles()
			return nil, nil, failure
		}
		used[fd] = true
		if i < 2 && syscall.SetNonblock(fd, true) != nil {
			closeFiles()
			return nil, nil, failure
		}
		f := os.NewFile(uintptr(fd), "ci-owned-control")
		if f == nil {
			closeFiles()
			return nil, nil, failure
		}
		files = append(files, f)
	}
	failed := make(chan struct{})
	var failOnce sync.Once
	cleanup, err := dbadmission.StartCIController(files[0], files[1], files[2], nonce, func() { failOnce.Do(func() { close(failed) }) })
	if err != nil {
		closeFiles()
		return nil, nil, failure
	}
	return failed, func() { cleanup(); closeFiles() }, nil
}
