# Restricted LDAP connection probe

`Probe` performs only an LDAPv3 simple bind and an empty-DN, base-object RootDSE
search. It uses Go's standard library for transport, TLS and certificate
verification; there are no new dependencies or generic LDAP client options.

The trusted caller must supply a DNS FQDN, `StartTLS` or `LDAPS`, an explicit
nonempty CA pool, nonempty deployment-owned `AllowedNetworks`, and an `Authorize`
callback. Nil or empty CA pools are rejected before any DNS lookup or dial;
there is no implicit fallback to the operating system trust store. The callback
must honor its context and check the pinned tenant/domain authorization and
configuration/credential revisions before dial, bind and search. It must release
database locks before returning. A denied stage emits no bytes for that stage.
Matching the returned naming context to the domain remains the caller's job.

Port 389 always completes StartTLS before binding; port 636 starts with TLS.
The TLS minimum is 1.2, with normal chain, DNS identity, validity-time and server
EKU verification. A configured IP only pins the transport destination; it never
changes the certificate identity. Refusals and TLS failures terminate the probe.

DNS resolves once. All answers (maximum 16) must fall inside allowed networks;
IPv4-mapped addresses are normalized. Unspecified, multicast, scoped IPv6 and
link-local addresses are denied regardless of prefix. Only one literal address
is attempted, with no retry, fallback, referral following or separate preflight.
The deployment policy must additionally prohibit any nonapproved destinations,
including loopback; socketless tests explicitly allow a synthetic loopback IP.

Bounds are 10 seconds overall, 2 seconds shared by DNS and dial, and 3 seconds
each for TLS (including StartTLS negotiation), bind and search. Earlier parent
deadlines win. Authorization checks use the corresponding bounded context.
Cancellation closes the connection and joins the one owned cancellation watcher.
The callback must cooperate with cancellation; no detached callback is spawned.

The fixed-shape BER decoder does not recurse and rejects indefinite lengths,
oversized messages before reading their bodies, unexpected controls and malformed
operations. Each message body is at most 64 KiB. Search accepts one RootDSE entry
and one final response, at most four named attributes, 4 KiB per value, 64
capability OIDs of at most 128 bytes each, and eight LDAP version values. LDAPv3,
a DNS host name and a nonempty default naming context are required. Returned
values are untrusted until the caller performs its domain checks.

Public errors contain only fixed `Code` and `Stage`. Server diagnostics and raw
transport/TLS errors are not retained. Credentials cannot be JSON serialized and
common formatting is redacted. Probe wipes owned bind buffers; the caller owns
and clears the supplied password slice after return. No credentials are logged.

Tests use real Go TLS over `net.Pipe` and synthetic certificates/credentials.
They do not open listeners or contact any network or real AD. They exercise
LDAPS/StartTLS success, strict certificate failures, zero bind on failure,
authorization denials, bounded DNS/BER/attributes, wrong credentials, referrals,
parent deadlines and cancellation. Socket-backed system integration and real
AD/Windows compatibility remain separate validation gates.

Primary specifications consulted:

- [RFC 4511](https://www.rfc-editor.org/rfc/rfc4511.html), LDAP messages, bind,
  search, StartTLS, and BER restrictions
- [Go crypto/tls](https://pkg.go.dev/crypto/tls), verified TLS and handshake context
- [Go net](https://pkg.go.dev/net), context-aware resolution/dialing and deadlines
