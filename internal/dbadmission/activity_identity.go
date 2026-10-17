package dbadmission

// ActivityIdentity is private control evidence, never a diagnostic payload.
// The typed checker limits construction/registration to the task proof bridge.
// It identifies a first fenced progress-10 commit, not a verified snapshot.
type ActivityIdentity struct {
	TaskID        string
	Kind          string
	Attempt       int
	LeaseOwner    string
	FencingToken  int64
	ResultVersion int64
	Phase         string
}

func (ActivityIdentity) String() string   { return "activity-identity" }
func (ActivityIdentity) GoString() string { return "activity-identity" }
func (ActivityIdentity) MarshalJSON() ([]byte, error) {
	return []byte(`"activity-identity"`), nil
}
