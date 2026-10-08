package credentialuse

// DirectoryPurpose is a separate, default-denied permission for the bounded
// directory reader. A connection-test grant never grants this purpose.
const DirectoryPurpose = "domain.directory_read"

// DirectoryV2Purpose authorizes only the fixed dictionary 2 directory reader.
// Neither a connection-test nor dictionary 1 grant grants this purpose.
const DirectoryV2Purpose = "domain.directory_read.v2"

// DirectoryConsumerEnabled reports the compiled consumer capability. Deployment
// still requires the independent, default-false ADTR_DIRECTORY_READ_ENABLED
// switch and an explicit current purpose grant; it is not runtime verification.
const DirectoryConsumerEnabled = true

// DirectoryV2ConsumerEnabled remains false until the versioned producer and its
// protected routes are registered. Compiled support never grants credential use.
const DirectoryV2ConsumerEnabled = false

func validatePurpose(purpose string) error {
	if purpose != Purpose && purpose != DirectoryPurpose && purpose != DirectoryV2Purpose {
		return problem(422, "unsupported_credential_purpose")
	}
	return nil
}

func consumerEnabledForPurpose(purpose string) bool {
	switch purpose {
	case Purpose:
		return ConsumerEnabled
	case DirectoryPurpose:
		return DirectoryConsumerEnabled
	case DirectoryV2Purpose:
		return DirectoryV2ConsumerEnabled
	default:
		return false
	}
}

// isDirectoryPurpose selects the shared directory eligibility rules only for
// known exact purposes. Every store entry point validates before selecting SQL.
func isDirectoryPurpose(purpose string) bool {
	return purpose == DirectoryPurpose || purpose == DirectoryV2Purpose
}
