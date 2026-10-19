//go:build !adtr_ci_dbadmission

package main

import "context"

func splitCISignalLifetime(signalContext context.Context, signalStop context.CancelFunc, cleanup func()) (context.Context, context.CancelFunc, func()) {
	return signalContext, signalStop, cleanup
}
