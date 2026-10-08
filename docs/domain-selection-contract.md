# F02 configured domain selection, Stage A

Refs #18, AD-F-006. AD-F-007 directory controller inventory remains pending a real
inventory producer. This slice consumes persisted F01 configuration and matching
historical connection observations under F47 scope. It performs no DNS, LDAP,
decryption, collector deployment, remote connection, or license inference.

## Frozen API

All routes require a live non-forced session, current schema, `domains.readable`,
a configured unexpired tenant within its domain limit, and exact active F47 domain
grants, including administrators. Reads use the same tenant/user serialization as
F01. No mutation proof is required for these reads. No new permission or migration.

- GET `/api/domain-selection`: optional `pageIdx` (canonical 1..1000000, default 1),
  `pageSize` (10/20/30/40/50, default 20), `keyword` (UTF-8, <=50 code points, no
  control characters), `observationState` (unverified/testing/verified/error).
  Every parameter is singleton; raw query is at most 32 KiB. Unknown/duplicate/malformed parameters return 400.
  Legacy `appType`, `application`, `type`, `ModuleType`, `status`, `statusList`,
  `scanType` filters are explicitly 422 `unsupported_source_filter`, not ignored.
  Search is literal case-insensitive configured domain/DC hostname substring.
  Scope is applied before filtering, count, ordering and pagination.
- GET `/api/domain-selection/resolve`: exactly `domainId`, `expectedRevision`,
  `expectedCredentialRevision`. `domainId` follows F01's opaque identifier grammar;
  both revisions are positive canonical decimal strings. Inaccessible,
  inactive, deleted or unknown IDs return 404. A current accessible row with changed
  revisions returns 409 `selection_changed`. Response is `{selection: Choice,
  checkedAt: UTC RFC3339}`. It is a fresh read, never a bearer authorization grant.
  Consumers must reauthorize and apply their own current revision/proof checks.

List response: `{page:{pageIdx,pageSize,total,totalPage},List:Choice[],exhausted}`.
Choice has exactly `domainId`, `domain`, `revision`, `credentialRevision`,
`dcHostName`, `ldapAddr`, `port`, `mode`, `credentialConfigured`, `connectionState`,
`source` (always `configured_connection`) and `lastTest` (null or exactly
`observedAt`, `dcHostName`, `code`). All IDs/revisions are strings. `ldapAddr` is
an explicitly configured address or empty, never an inferred/resolved address.
`connectionState` retains F01's matching config/credential/policy/generation and
terminal-task semantics. `verified` means a historical successful observation;
it has no live reachability, collector, inventory, license or execution implication.
A new probe or policy can change that observation without changing chosen config
revisions; resolve returns the current observation instead of trusting client state.
Successful list and resolve responses carry `X-ADTR-User-ID` from the authenticated
server actor. The picker must match it to its current profile before accepting a
response, and refresh identity on a missing/mismatched header. No caller identity
header or query is accepted. Error responses carry no successful actor binding.
Errors are `{error: code}`, safe status codes and `Cache-Control: no-store`.

## Consumer and security boundary

The first real consumer is the F01 connection workspace: choose an authorized
saved source, resolve its revisions, then load its actual detail/testing workflow.
The existing test submission still requires both domains/tasks write grants,
current scope/config and fresh password/TOTP proof. Selection cannot submit a task.
The F03 minimal domain picker keeps its own operation_accounts permission and DTO;
rich domain metadata is never exposed by an OR of different module grants.

Selection/query/loading state is session-bound, cancellable and discarded on Back,
logout, changed identity or revoked permission. Late responses cannot resurrect it.
A 409 resolves by clearing stale selection and refreshing choices; no auto mutation.

## Remaining source and acceptance gates

Original AD-F-006 includes application-number/license/module/scan/collector filters.
The workbook contains an AD=1 reference, but does not establish the complete legacy
endpoint/module/license behavior or supply those producers. This Stage A explicitly
rejects those filters instead of treating a numeric clue as an authorization rule. AD-F-007 includes actual DC
inventory/roles/IPs, installed collectors and remote connectivity; configured one-DC
connection metadata cannot satisfy it. Shared UI references to scan templates,
response controls, MFA/profile and collector configuration are not assumed to belong
to this picker. The complete workbook field/control mapping remains in requirements.

Required tests cover actual PostgreSQL scoped counts/paging/literal search, empty
scope, non-bypass admin, cross-tenant/inactive/deleted IDs, config/credential changes,
revocation while blocked on tenant lock, natural expiry, schema gates, current
observation refresh, and real browser choose→resolve→existing detail consumption.
Local compile/DOM evidence is separate from real CI and source-system acceptance.

## Local validation

Inherited baseline: F42 integration commit 344e3ce90e3580e8b50cf3d52facdbc46f8398aa,
271 DOM tests plus Go/type/build checks. This slice passed `make lint test build`,
all-package integration-tag compilation and vet, 333 DOM tests (62 new), frontend
TypeScript/build/format and an npm audit with zero vulnerabilities. Independent
review covers backend, SQL, permissions, async UI and the consumer transition.

The real PostgreSQL test command was attempted and failed explicitly because
`ADTR_TEST_DATABASE_URL` is unavailable locally. No integration tests were skipped
or counted as runtime passes. The existing real domains browser suite now includes
the selector before and after a successful synthetic LDAP observation; its runtime
execution remains pending exact-head CI. AD-F-006 remains in progress and AD-F-007
inventory is still unimplemented. Source parity and Windows/AD acceptance are open.
