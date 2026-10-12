//go:build integration && linux

package main

import (
	"os"
	"syscall"
)

const emptyTransitionSupported = true

func openEmptyTransitionMarker(root *os.Root) (*os.File, error) {
	// Root.OpenFile follows in-root symlinks, even with O_NOFOLLOW supplied.
	// Open the anchored directory itself, then reject symlinks in the kernel
	// when opening our single constant basename. NONBLOCK prevents a replaced
	// FIFO from stalling before f.Stat can reject all nonregular files.
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	fd, err := syscall.Openat(int(directory.Fd()), emptyTransitionMarker,
		syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), emptyTransitionMarker), nil
}
