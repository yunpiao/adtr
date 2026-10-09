package messagecore

import "time"

// MarkRead applies explicit frozen targets to the caller's CURRENT same-identity
// state. Every validation finishes before a new state is created; an error
// returns a zero MarkResult, never a partial success. The result and all inputs
// remain unchanged. Freshness and atomic persistence remain caller obligations.
func (r *Result) MarkRead(current ReadState, targets []string, at time.Time) (MarkResult, error) {
	if err := r.check(); err != nil {
		return MarkResult{}, err
	}
	if len(targets) > r.limits.MaxMarkTargets {
		return MarkResult{}, invalid(BudgetExceeded, "mark_targets")
	}
	// Bound current state and target bytes before scanning or copying them. A
	// current state can grow after the snapshot, so its budget is checked afresh.
	b := byteBudget{remaining: r.limits.MaxInputBytes}
	if err := b.add(256); err != nil {
		return MarkResult{}, err
	}
	if err := b.state(current); err != nil {
		return MarkResult{}, err
	}
	for _, id := range targets {
		if err := b.add(64); err != nil {
			return MarkResult{}, err
		}
		if err := b.strings(id); err != nil {
			return MarkResult{}, err
		}
	}
	if err := validateReadState(current, r.scope); err != nil {
		return MarkResult{}, err
	}
	if at.IsZero() {
		return MarkResult{}, invalid(InvalidFilter, "marked_at")
	}
	seen := make(map[string]struct{}, len(targets))
	for _, id := range targets {
		if id == "" {
			return MarkResult{}, invalid(InvalidTarget, "targets.empty_id")
		}
		if _, duplicate := seen[id]; duplicate {
			return MarkResult{}, invalid(InvalidTarget, "targets.duplicate_id")
		}
		if _, member := r.positions[id]; !member {
			return MarkResult{}, invalid(TargetNotInResult, "targets.membership")
		}
		seen[id] = struct{}{}
	}
	output := MarkResult{State: cloneReadState(current)}
	output.Progress.Target = len(targets)
	output.Progress.Processed = len(targets)
	at = utc(at)
	for _, id := range targets {
		if _, already := output.State.FirstReadAt[id]; already {
			output.Progress.AlreadyRead++
		} else {
			output.State.FirstReadAt[id] = at
			output.Progress.NewlyRead++
		}
	}
	return output, nil
}
