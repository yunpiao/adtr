package tasks

// ActivityCandidate is an opaque, one-use association with a committed fenced
// operation. It is NOT proof of a snapshot or permission to freeze a worker.
// Independent PostgreSQL snapshot and barrier validation remains mandatory.
type ActivityCandidate interface {
	activityCandidate()
}
