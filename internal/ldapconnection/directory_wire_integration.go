//go:build integration

package ldapconnection

import (
	"context"
	"net"
)

// ReadDirectoryWithDialForIntegrationTest exists only in integration builds. It
// preserves production TLS, bind, paging, authorization and cleanup while
// mapping the fixed logical destination to an isolated synthetic transport.
// It does not replace decoding, authentication, authorization or publication.
func ReadDirectoryWithDialForIntegrationTest(ctx context.Context, cfg DirectoryConfig, credential Credential, dial func(context.Context, string, string) (net.Conn, error)) (DirectoryObservation, error) {
	return readDirectory(ctx, cfg, credential, transport{lookup: net.DefaultResolver.LookupNetIP, dial: dial})
}
