package main

import (
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/tasks"
)

type operationalEmitter interface {
	Emit(operationallogs.EventCode, operationallogs.Outcome, operationallogs.Reason) bool
}

// An actual coalesced queue poll may have found no work. Never label this as a
// successful business task, and never forward its private worker lease owner.
func recordOperationalWorkerActivity(sink operationalEmitter, a tasks.WorkerActivity) {
	if sink == nil {
		return
	}
	var code operationallogs.EventCode
	switch a.Cycle {
	case "queue":
		code = operationallogs.QueueCycle
	case "recovery":
		code = operationallogs.RecoveryCycle
	case "scheduler":
		code = operationallogs.SchedulerCycle
	default:
		return
	}
	outcome, reason := operationallogs.Completed, operationallogs.NoReason
	switch a.Status {
	case "success":
		if a.Code != "" {
			return
		}
	case "progress":
		if a.Cycle != "queue" || a.Code != "" {
			return
		}
		code = operationallogs.QueueProgress
		outcome = operationallogs.Progress
	case "failure":
		switch a.Code {
		case "cycle_failed", "database_unavailable", "execution_failed", "schema_incompatible":
		default:
			return
		}
		outcome, reason = operationallogs.Failed, operationallogs.CycleFailed
	default:
		return
	}
	sink.Emit(code, outcome, reason)
}
