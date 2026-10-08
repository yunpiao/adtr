# F12 directory transport and grant components

Current local integration status (2026-10-07): migration15, production registry/HTTP/task-authority wiring and default-denied purpose APIs are now present in source. The compiled consumer capability is true, while the independent deployment switch remains defaultfalse. The component-stage limitations below describe their original checkpoints; actual PostgreSQL, browser and AD execution remain unverified. Current UI contract: [directory-api-contract.md](directory-api-contract.md).

This local increment follows the pure dictionary checkpoint for AD-F-034. At the original b702b20 checkpoint it was not a registered task; migration15 and the later API/UI integration now install and register it. `DirectoryConsumerEnabled` reports compiled support while deployment remains default-off. No existing grant is copied or reinterpreted.

## Network primitive

`ldapconnection.ReadDirectory` accepts a distinct `DirectoryConfig` and typed directory authorization callback. Trusted callers must provide explicit bounds and separately prove current directory-read authority. The primitive has no HTTP adapter and does not accept caller-selected base, filter, attributes, port or referral policy. It uses verified LDAPS or StartTLS, the existing egress constraints, exact AD DS RootDSE/domain checks, a fixed user/group subtree search (computer inherits user), and four dictionary attributes. Each page uses critical RFC2696 controls with bounded opaque cookies; repeated cookie bytes are valid and page limits terminate loops.

Authorization occurs before validation/DNS/dial/TLS/bind/RootDSE, every page, and the final return. I/O is closed and its cancellation watcher joined before final authorization. The directory operation has a 120-second total bound and three-second network phases/pages; existing connection-probe deadlines remain unchanged. Invalid responses, referrals, duplicate identities, incomplete paging or exceeded budgets return no observation. No retries, plaintext fallback, AD mutation, point-in-time consistency or deletion inference are provided.

The supplied password is consumed and cleared on every return, including panic. Response buffers and accumulator cookie copies are explicitly discarded, including partial-read errors and panics. The caller remains responsible for any separate source credential buffer. Returned objects are intentionally retained only after successful bounded completion.

## Deployment and grants

`ADTR_DIRECTORY_READ_ENABLED` is read once at startup and defaults false independently of the connection-test switch. Enabling it requires the independent domain vault key, explicit CA trust and nonempty egress configuration. Loading this flag does not register a consumer or grant any actor permission.

The new exact purpose is `domain.directory_read`. Purpose-aware internal store methods use it explicitly; existing methods and HTTP validation remain pinned to `domain.connection_test`. The migration15 SQL fragment expands only the allowed purpose values, preserves historical governance guards and the old eligibility function, and introduces purpose-specific eligibility plus directory permission epoch invalidation. A custom role needs current domain membership, domains read, directory_assets read/write and tasks read/write; administrator role does not imply an actual use grant. Operation-account metadata visibility is not a prerequisite for the new narrow consumer. Account/pair revisions, current security state, tenant entitlement, explicit grant, domain scope and governance proof remain required.

Migration15 adds the directory_assets mark before installing the fragment and stamps only after the entire transaction commits. The fragment itself does not stamp a schema version. Historical component tests apply the fragment after the real schema14 baseline; current runtime tests use production15. Separate upgrade tests exercise the real14→15 migrator. These PostgreSQL cases remain unexecuted locally.

## Remaining integration and acceptance

Separately typed task/payload/use-ledger and immutable observation storage components now exist locally, with an unregistered executor. Migration preflight/collision checks, audit, HTTP/UI and worker registration are now implemented locally; actual database/browser/AD execution remains required. Existing B2 diagnostic generation/latest-test identity must remain unchanged. Account replacement/removal must remain blocked until the real executor-return acknowledgement for every opened consumer. AD-F-035 incremental behavior, AD-F-041 address relationships, complete source field parity and eight-version Windows/AD validation remain open.

Synthetic TLS/BER fixture tests prove the local transport behavior under tested responses. They do not establish real AD compatibility, source-system parity, production capacity or complete product acceptance.

## Executed local checks

On 2026-10-07, the recovered dot cloud checkout passed `make lint test build` (all Go packages under race/vet/build, formatting/requirements validation and nine Python contracts). The full ldapconnection/directoryassets race run passed, and the focused BER fuzz run executed26,586 cases. Independent transport re-review verified the buffer/cookie cleanup corrections; independent grant/configuration source review found no confirmed defect. Real PostgreSQL execution, a directory migration and product/browser/AD verification remain outstanding. Frontend tests were not rerun for this backend-only increment.

All packages also passed integration-tag compilation and vet. Eight directory-purpose component test groups were actually invoked; each stopped at fixture setup because `ADTR_TEST_DATABASE_URL` is absent. None ran against PostgreSQL and none were skipped as success. These include two-connection CAS/regrant, pair-replacement contention and lock-inversion cases; their presence and compilation are not execution evidence.
