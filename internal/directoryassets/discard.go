package directoryassets

// Discard invalidates the accumulator and clears its retained opaque cookie.
// Callers that can stop before a terminal page must defer Discard immediately
// after construction. Result copies already returned remain caller-owned.
// Discard is idempotent and also safe during error or panic cleanup.
func (a *Accumulator) Discard() {
	if a != nil {
		a.fail(ErrClosed)
	}
}
