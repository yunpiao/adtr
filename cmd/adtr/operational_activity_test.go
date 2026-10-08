package main

import (
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/tasks"
	"testing"
)

type observedOperational struct {
	code    operationallogs.EventCode
	outcome operationallogs.Outcome
	reason  operationallogs.Reason
}
type operationalCapture struct{ events []observedOperational }

func (c *operationalCapture) Emit(code operationallogs.EventCode, outcome operationallogs.Outcome, reason operationallogs.Reason) bool {
	c.events = append(c.events, observedOperational{code, outcome, reason})
	return true
}
func TestOperationalWorkerAdapterReportsObservedCycles(t *testing.T) {
	c := &operationalCapture{}
	for _, a := range []tasks.WorkerActivity{{WorkerID: "private-owner", Cycle: "queue", Status: "success"}, {WorkerID: "private-owner", Cycle: "queue", Status: "progress"}, {WorkerID: "private-owner", Cycle: "scheduler", Status: "failure", Code: "database_unavailable"}, {WorkerID: "private-owner", Cycle: "recovery", Status: "success"}} {
		recordOperationalWorkerActivity(c, a)
	}
	if len(c.events) != 4 || c.events[0].code != operationallogs.QueueCycle || c.events[0].outcome != operationallogs.Completed || c.events[1].code != operationallogs.QueueProgress || c.events[2].reason != operationallogs.CycleFailed || c.events[3].code != operationallogs.RecoveryCycle {
		t.Fatal(c.events)
	}
	// Unknown/raw strings never become an operational record or error reason.
	for _, a := range []tasks.WorkerActivity{{Cycle: "raw/path", Status: "success"}, {Cycle: "queue", Status: "failure", Code: "postgres://synthetic-secret"}, {Cycle: "recovery", Status: "progress"}, {Cycle: "queue", Status: "success", Code: "unexpected"}} {
		recordOperationalWorkerActivity(c, a)
	}
	if len(c.events) != 4 {
		t.Fatal("invalid source facts were emitted")
	}
}
