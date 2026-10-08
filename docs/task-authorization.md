# Task HTTP and live authorization contract (F48)

Refs Issue #12, AD-F-189/191/192; queue behavior is specified in
[tasks-contract.md](tasks-contract.md), existing identity/role/resource contracts
remain authoritative. This slice adds local infrastructure execution, not an AD
adapter, engine restart, detection/export/notification executor or product acceptance.

## HTTP surface and authenticated transaction

The API process mounts `Service.TasksHandler(engine)` at both exact `/api/tasks`
and subtree `/api/tasks/`, only when identity is configured. There is no redirect
between the canonical list route and a trailing slash. The worker mounts neither.

- GET `/api/tasks`: optional pageIdx (1–1,000,000; default 1), pageSize
  (1–100; default 20), domainId, taskName and exact local state
- GET `/api/tasks/kinds`: `{kinds:[...]}` with immutable registered metadata
- GET `/api/tasks/detail?taskUUID=...`: task plus bounded ordered events
- POST `/api/tasks/submit`: taskName, domainId, payloadVersion, payload,
  idempotencyKey, actorPassword, totpCode
- POST `/api/tasks/cancel`: taskUUID, actorPassword, totpCode
- POST `/api/tasks/recover`: taskUUID, idempotencyKey, actorPassword, totpCode

All listed POST fields are required, exact and non-null. No extra query/body
fields, JSON aliases/duplicate keys, trailing JSON, malformed query encoding or
repeated scalar query are accepted. Bodies are bounded to 128 KiB; registered
payload canonicalization applies a separate 64 KiB limit. No body may supply
actor, tenant, authorization version, platform-scope flag, retry policy, state,
lease, result or fencing token. `engineRestart` is not a registered kind.

Every task transaction first calls `engine.CheckSchemaTx`, acquiring shared
migration lock 734192801 and requiring the exact compiled schema version, before
any identity/task lock or rate-limit/proof write. Missing or incompatible schema
returns 503 without task, proof, rate-limit or denied-audit writes; compatibility
cannot change until the transaction releases its migration lock. The task engine
constructor receives the compiled store schema version explicitly.

Every route authenticates the existing HttpOnly cookie, acquires the shared
tenant lock before actor lock, rechecks the live session, and applies the forced
password-change/90-day gate. The fixed `tasks` function mark requires readable;
POST additionally requires writeable, exact trusted Origin, JSON, session-bound
X-CSRF-Token, current password and a fresh unused TOTP via `requireAccessProof`.
Login and enrollment consume TOTP steps; an old cookie or previously used code
cannot authorize another write. The existing per-IP and sensitive-account rate
limits apply. `/api/access/check` and menu paths must use `taskPathAllowed` and
`taskRoutes`, so introspection and actual routes agree.

Only the server constructs `tasks.Principal` from the verified actor. Engine Tx
methods run in that same authentication/proof transaction: task, audit/outbox
and TOTP consumption commit together, or all roll back. A failed engine mutation
may leave only the normal denied-operation audit and rate-limit bookkeeping;
it must not consume proof or change task authorization. Responses preserve
engine errors; unknown DB errors are `500 {error:"internal"}`, not permission
denials. No SQL details, password, MFA secret, session token or private actor
metadata are returned. Task IDs from other tenants or unauthorized domains are indistinguishable from
missing IDs. Route-level missing tasks grants are 403; denied existing-object
scope lookups are 404. Domain authorization applies before list counts/pagination.

## Worker authorization

`auth.NewTaskAuthorizer()` returns `tasks.Authorizer` without creating an identity
Service, reading a session, decrypting MFA, or requiring ADTR_AUTH_KEY. It reads
the persisted actor in the supplied transaction and locks tenant then actor
before the engine locks a task. It rejects deleted, cross-tenant, disabled,
forced-change or password-expired actors, unknown actions/scopes, and missing
current tasks grants. Builtin platform_admin has tasks function permission;
viewer does not. A custom role must have a current explicit tasks grant.

Read requires readable; write and execute require readable and writeable.
Cookie claims and snapshots are never authority. Logout/normal session expiry
alone does not kill authorized background work. Password/MFA/role/security
changes do invalidate its submission epoch, even if a replacement session exists.

The sole production platform kind is `infrastructure.health`, version 1,
`domainId:"platform"`, with `{}` payload. It proves only the local queue/database
path and needs no AD tenant configuration. Other platform names are denied until
explicitly reviewed; a caller cannot turn a domain task into a platform task.
A future registered domain kind must additionally pass all current conditions:

1. Configured tenant with expire_time strictly after now and max_ad_count > 0
2. Active domain catalogue count no larger than that cap
3. The exact active domain in the same tenant
4. Explicit role → resource group → domain membership

These checks also apply to platform administrators. A domain grant does not
imply a registered AD executor exists or authorize real AD activity. No external
AD, notifications, real credentials or production deployment occurs here.
Database/connection errors propagate and abort; they must never be converted
into durable authorization-revoked outcomes.

## Persistent monotonic authorization epochs

Migration 5 first extends the function mark constraint to include tasks, then
runs `auth.TaskAuthorizationSchema`, then `tasks.Schema`, atomically. Existing
identities receive distinct positive sequence values; subsequent identity
inserts receive a new server-generated value. `users.authorization_version`
is serialized as an opaque decimal string in task rows. It is not a reusable
cookie, hash of current grants, timestamp, or cache of permission decisions.

The engine pins the submission epoch. Start, heartbeat, progress and finish
must compare the live version with that pinned value, as well as recheck live
permission. Any mismatch rejects the old work even when revoke and regrant
occurred between checks. Only a fresh authorized submission/recovery binds a new
epoch; retries must not silently refresh the old one.

Trigger behavior:

- A change to a user's password hash, forced-change flag, disabled flag, password
  update time, active MFA secret, base role or custom role advances that actor
- Any effective INSERT/UPDATE/DELETE of the tasks function grant advances the
  affected role's users; other function marks are not dependencies of this kind
- Resource role/group relation changes advance users of the old/new roles
- Group membership changes advance users of the group's associated roles;
  deleting a group invalidates its associated users before cascades
- Domain insertion/deletion, identity or activation changes advance every actor
  in affected tenants, because domain counts affect capacity eligibility
- Tenant config insertion/deletion or expiry/capacity changes advance every actor
  in affected tenants. UID/name-only edits do not
- Descriptive user profile fields, TOTP last-step consumption, pending enrollment,
  login/session creation, ordinary logout and session deletion do not advance epochs
- User ID/tenant reassignment is forbidden. Direct user-version assignment and
  nonzero version on INSERT are forbidden. Grant-trigger nested updates are
  allowed only if the version strictly increases
- TRUNCATE of identity/grant/resource tables is rejected to prevent bypassing
  row-level version invalidation. Database-owner DDL remains a release boundary

Multiple row changes can advance the same actor more than once. Conservative
invalidation during delete/reinsert grant replacement is deliberate, even if a
replacement ends at the same grant set. Actor epochs only need strict monotonicity,
not contiguous numbering. Sequence numbers can be consumed by a rolled-back
transaction; the stored actor epoch remains unchanged, so an audited failure
cannot revoke existing task authorization as a side effect.

## Locking and database privilege boundary

All supported application mutations already acquire
`pg_advisory_xact_lock(hashtextextended('adtr-access:' || tenant,0))` before actor
locks. Trigger-driven grant invalidation locks affected actors in ascending ID
order under that tenant lock. Engine worker paths must acquire shared migration lock, tenant, actor,
then task. Claim/recovery paths that lock tasks without identity locks must never
subsequently acquire identity locks in the same transaction.

PostgreSQL may acquire a modified row lock before its BEFORE ROW trigger runs.
For direct SQL lacking the normal lock protocol, triggers use nonblocking
`pg_try_advisory_xact_lock`, raising SQLSTATE 40001 if the tenant is busy. They do
not wait while holding a row and form a tenant/user inversion. Cross-tenant direct
mutations try tenant locks in lexical order and abort rather than wait. Such SQL
must be retried as a new transaction with tenant locks acquired first; HTTP does
not automatically replay proof-bearing writes. Supported single-tenant API
transactions retain their existing deterministic lock order.

The migration/runtime role separation from the identity contract remains a
production gate: a database owner able to alter/drop triggers, reset sequences
or install custom triggers is not an untrusted HTTP client. No SECURITY DEFINER
function or caller-selectable security flag is introduced. An ordinary caller
cannot assign/roll back authorization versions through the HTTP surface.

## Verification

`task_test.go` covers strict routes/JSON/query parsing, spoofed identity/epoch/
scope, exact task grants, bounded pagination and safe error mapping.
`task_integration_test.go` creates a uniquely named PostgreSQL database through
the existing isolated fixture and fails when ADTR_TEST_DATABASE_URL is missing.
It covers actual HTTP authentication/proof and session handling, current-role
reads, same-key replay, foreign tenant secrecy, unsupported kinds, no admin data
bypass, live domain/tenant eligibility, all epoch invalidators, direct version
spoofing, TRUNCATE rejection, actual login/logout behavior, rollback after audit
failure, database-error distinction, fail-fast lock ordering, queued revocation,
and restored-grant rejection at start/heartbeat/progress/finish.

Local Go unit, race, vet and integration-tag compilation are separate from
actual PostgreSQL execution. The current dot workspace lacks PostgreSQL/Docker;
real database and built-browser evidence must come from the isolated CI runs.
Source-runtime parity, AD/Windows execution, capacity and production controls
remain separate gates; this document makes no full-product acceptance claim.

## Audit export integration (schema 6)

The platform kind `audit.export` additionally requires `audit.readable` and
`audit_exports.readable`; writes/execution require `audit_exports.writeable`
in addition to the existing task grants. Changes to either audit mark bump
actor authorization epochs under the same tenant/user lock order. Existing
custom roles gain no new rights automatically.

Artifact reads use dedicated audit endpoints and recheck the creator, current
session/grants/epoch, every snapshot domain's live expiry/capacity/membership,
and the audit visibility revision. Generic task interfaces retain owner-scoped
control state, but never disclose private artifact result/cursor metadata.
