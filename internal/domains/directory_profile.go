package domains

import "github.com/yunpiao/adtr/internal/credentialuse"

// DirectoryV2KindName names a separately authorized dictionary-2 task.
// Its task envelope version remains 1.
const DirectoryV2KindName = "domain.directory_read.v2"
const DirectoryV3KindName = "domain.directory_read.v3"

// directoryTaskProfile is a closed selector, never a caller-provided purpose or
// serializer. A zero or unknown value fails before database or credential work.
type directoryTaskProfile uint8

const (
	directoryTaskV1 directoryTaskProfile = iota + 1
	directoryTaskV2
	directoryTaskV3
)

type directoryTaskIdentity struct {
	kind              string
	purpose           string
	dictionaryVersion int
}

func (p directoryTaskProfile) identity() (directoryTaskIdentity, bool) {
	switch p {
	case directoryTaskV1:
		return directoryTaskIdentity{DirectoryKindName, credentialuse.DirectoryPurpose, 1}, true
	case directoryTaskV2:
		return directoryTaskIdentity{DirectoryV2KindName, credentialuse.DirectoryV2Purpose, 2}, true
	case directoryTaskV3:
		return directoryTaskIdentity{DirectoryV3KindName, credentialuse.DirectoryV3Purpose, 3}, true
	default:
		return directoryTaskIdentity{}, false
	}
}

// Profile selection is a package-owned constant. Requests never provide a kind,
// purpose, decoder, query or dictionary pin.
func (p directoryTaskProfile) payload(pins directoryPinnedPayload) []byte {
	switch p {
	case directoryTaskV1:
		return pins.json()
	case directoryTaskV2:
		return (directoryV2PinnedPayload{directoryPinnedPayload: pins, DictionaryVersion: 2}).json()
	case directoryTaskV3:
		return (directoryV3PinnedPayload{directoryPinnedPayload: pins, DictionaryVersion: 3}).json()
	default:
		return nil
	}
}

func (s *Store) directoryEnabled(profile directoryTaskProfile) bool {
	switch profile {
	case directoryTaskV1:
		return s.runtime.DirectoryReadEnabled()
	case directoryTaskV2:
		return s.runtime.DirectoryReadEnabled() && s.runtime.DirectoryReadV2Enabled()
	case directoryTaskV3:
		return s.runtime.DirectoryReadEnabled() && s.runtime.DirectoryReadV3Enabled()
	default:
		return false
	}
}

func (p directoryTaskProfile) auditAction(suffix string) string {
	switch p {
	case directoryTaskV1:
		return "domain_directory_" + suffix
	case directoryTaskV2:
		return "domain_directory_v2_" + suffix
	case directoryTaskV3:
		return "domain_directory_v3_" + suffix
	default:
		return ""
	}
}
