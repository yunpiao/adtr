//go:build integration && !linux

package main

import "os"

// The transition is a Linux-container fixture capability. Existing fixture
// modes remain portable; requesting this input elsewhere fails closed.
const emptyTransitionSupported = false

func openEmptyTransitionMarker(_ *os.Root) (*os.File, error) {
	return nil, errEmptyTransition
}
