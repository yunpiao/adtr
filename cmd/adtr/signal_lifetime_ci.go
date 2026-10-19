//go:build adtr_ci_dbadmission

package main

import "context"

// Keep signal registration alive through the final controller reports. Logical
// cancellation still returns only after the root's child cancellation barrier.
func splitCISignalLifetime(signalContext context.Context, signalStop context.CancelFunc, cleanup func()) (context.Context, context.CancelFunc, func()) {
	logicalContext, logicalCancel := context.WithCancel(signalContext)
	return logicalContext, logicalCancel, func() {
		defer signalStop()
		cleanup()
	}
}
