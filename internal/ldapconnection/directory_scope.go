package ldapconnection

// DirectoryDNInDomain validates the same restricted domain scope used by the
// wire reader. It performs no resolution, I/O, authentication or authorization.
func DirectoryDNInDomain(dn, domain string) bool {
	base, ok := directoryBaseDN(domain)
	return ok && directoryDNWithin(dn, base)
}

// DirectoryNamingContextMatchesDomain requires the exact domain naming context,
// without child-domain components or caller-selected search scope.
func DirectoryNamingContextMatchesDomain(dn, domain string) bool {
	base, ok := directoryBaseDN(domain)
	return ok && directoryDNEqual(dn, base)
}
