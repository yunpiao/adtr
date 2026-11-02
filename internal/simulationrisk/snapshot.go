package simulationrisk

import (
	"github.com/yunpiao/adtr/internal/riskscore"
	"github.com/yunpiao/adtr/internal/securityevents"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"time"
)

// SnapshotReset is persisted reset evidence from the caller's single source snapshot.
type SnapshotReset struct {
	Revision    int64     `json:"revision"`
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
	Actor       string    `json:"actor"`
	OperationID string    `json:"operationId"`
}

// EvaluateSnapshot reuses the existing mapping and evaluator without additional IO.
// Caller must supply already normalized complete events and dataset from one snapshot.
func EvaluateSnapshot(s Scope, d simulationevents.Dataset, subject string, events []securityevents.Event, history []SnapshotReset, at time.Time) (Result, error) {
	if !s.Read || !valid(s.Tenant, 128) || !valid(s.Domain, 128) {
		return Result{}, ErrDenied
	}
	if !valid(subject, 128) || at.IsZero() || len(events) != d.EventCount || d.Contract != securityevents.ContractVersion || !d.Simulation {
		return Result{}, ErrInvalid
	}
	if len(history) > MaxResets || len(events) > securityevents.MaxEvents {
		return Result{}, ErrLimit
	}
	resets := make([]riskscore.Reset, 0, len(history))
	for i, h := range history {
		if h.Revision != int64(i+2) || !valid(h.Actor, 128) || !valid(h.OperationID, 128) || !valid(h.Reason, 512) || h.ID != "reset:"+digest([]string{h.Actor, h.OperationID}) || h.At.IsZero() || (i > 0 && h.At.Before(history[i-1].At)) {
			return Result{}, ErrInvalid
		}
		resets = append(resets, riskscore.Reset{ID: h.ID, At: h.At, Reason: h.Reason, Scope: riskscore.Scope{TenantID: s.Tenant, DomainID: s.Domain, UserID: subject}})
	}
	for _, e := range events {
		if e.Tenant != s.Tenant || e.Domain != s.Domain || e.Provenance != (securityevents.Provenance{Contract: d.Contract, Fixture: d.Fixture, Synthetic: true}) {
			return Result{}, ErrInvalid
		}
	}
	if e := liveBoundary(resets, at); e != nil {
		return Result{}, e
	}
	return evaluate(s, d, subject, events, resets, at)
}
