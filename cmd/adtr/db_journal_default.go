//go:build !adtr_ci_dbadmission

package main

import "github.com/yunpiao/adtr/internal/operationallogs"

func emitCIJournalFinal(*operationallogs.Recorder, error) {}
