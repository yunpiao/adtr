//go:build integration

package main

import (
	"errors"
	"io"
	"os"
)

const (
	emptyTransitionMarker = "empty-v1"
	emptyTransitionBytes  = "empty-v1\n"
)

var errEmptyTransition = errors.New("synthetic LDAP fixture empty transition unavailable")

// This opt-in fixture input has exactly one filename and one value. It cannot
// issue commands or publish a response. The launcher owns a fresh private
// directory, mounts it read-only, and cleans it up after the fixture exits.
type fixtureEmptyTransition struct {
	root *os.Root
}

func openFixtureEmptyTransition(path string) (*fixtureEmptyTransition, error) {
	if path == "" {
		return nil, nil
	}
	if !emptyTransitionSupported {
		return nil, errEmptyTransition
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errEmptyTransition
	}
	return &fixtureEmptyTransition{root: root}, nil
}

// Absence is the original nonempty dictionary. Every other read failure is an
// error, never a successful empty observation or a fallback to stale rows.
func (e *fixtureEmptyTransition) sample() (bool, error) {
	f, err := openEmptyTransitionMarker(e.root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errEmptyTransition
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 || info.Size() != int64(len(emptyTransitionBytes)) {
		return false, errEmptyTransition
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(len(emptyTransitionBytes)+1)))
	if err != nil || string(data) != emptyTransitionBytes {
		return false, errEmptyTransition
	}
	return true, nil
}
