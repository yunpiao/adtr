//go:build !adtr_ci_dbadmission

package main

import "context"

func observeCIWorkerContext(context.Context, context.CancelFunc) {}
