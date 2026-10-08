package domains

import "github.com/yunpiao/adtr/internal/credentialuse"

// DirectoryV2KindName names a separately authorized, deliberately unregistered
// dictionary-2 task. Its task envelope version remains 1.
const DirectoryV2KindName = "domain.directory_read.v2"

// directoryTaskProfile is a closed selector, never a caller-provided purpose or
// serializer. A zero or unknown value fails before database or credential work.
type directoryTaskProfile uint8

const (
	directoryTaskV1 directoryTaskProfile = iota + 1
	directoryTaskV2
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
	default:
		return directoryTaskIdentity{}, false
	}
}
