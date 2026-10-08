package domains

// ValidateSourceInput also rejects mixed-source values for internal callers.
// The HTTP adapter separately enforces exact fields and duplicate/null rejection.
func ValidateSourceInput(op string, in *SourceInput) error {
	bad := func() error { return problem(400, "invalid_input") }
	if !ValidID(in.DomainID) || !ValidKey(in.IdempotencyKey) {
		return bad()
	}
	for _, revision := range []string{in.ExpectedRevision, in.ExpectedConnectionCredentialGeneration} {
		if _, err := Revision(revision); err != nil {
			return err
		}
	}
	switch op {
	case "reference":
		if !ValidID(in.AccountID) || in.Username != nil || in.Password != nil {
			return bad()
		}
		for _, revision := range []string{in.ExpectedAccountRevision, in.ExpectedAccountCredentialRevision, in.ExpectedGrantRevision} {
			if _, err := Revision(revision); err != nil {
				return err
			}
		}
	case "custom", "detach":
		if in.AccountID != "" || in.ExpectedAccountRevision != "" || in.ExpectedAccountCredentialRevision != "" || in.ExpectedGrantRevision != "" {
			return bad()
		}
		if op == "custom" {
			if in.Username == nil || in.Password == nil || !credentialValid(*in.Username, *in.Password) {
				return bad()
			}
		} else if in.Username != nil || in.Password != nil {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}
