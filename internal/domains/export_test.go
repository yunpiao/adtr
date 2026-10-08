package domains

import (
	"context"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/ldapconnection"
)

// This constructor exists only in the test binary. Production has no transport
// override; the adapter separately tests real synthetic TLS/LDAP handshakes.
func NewProbeForTest(r *domainconfig.Runtime, probe func(context.Context, ldapconnection.Config, ldapconnection.Credential) (ldapconnection.Result, error)) *Store {
	return &Store{runtime: r, probe: probe}
}
