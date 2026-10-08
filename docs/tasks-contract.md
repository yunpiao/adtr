# Durable background tasks (F48)

Refs Issue #12, AD-F-189/191/192, task-contract.md, source FLD-3131–3149,
DTL-030/051. This is a PostgreSQL queue/API/worker foundation with one real
infrastructure executor. Detection, export, management restart, notifications,
AD operations and Windows integration are not silently registered or accepted.
Their task schemas, credentials, effects and laboratory gates remain open. F39
adds the separately documented `audit.export` database-artifact executor in
[audit-contract.md](audit-contract.md); this does not implement AD exports.

## Explicit executable scope

The original infrastructure kind is `infrastructure.health`, payload
version 1, platform scope, domain ID `platform`, and payload `{}`. It performs a
real context-aware PostgreSQL query checking schema metadata and queue existence.
Success returns `{"database":"ready","queue":"ready"}`. It accepts no URL,
command, credential, delay, AD target or behavior override. Unknown kinds,
versions and payload fields fail closed. All synthetic executors are in tests.
The platform scope is separate from the AD catalogue and grants no AD access.

This kind has maximum 5 attempts including its first execution, lease 30 seconds,
heartbeat 5 seconds, execution timeout 10 seconds, and transient database retry
waits 5/10/20/40 seconds (cap 60 seconds, no jitter for this local policy).
`max_attempts=1` means no retries. Error retryability requires both the executor's
explicit indication and the registered code allowlist. Authorization, input,
certificate and credential errors are never automatically retried. A stopped
worker may safely retry this read-only kind within the same attempt bound.
Infrastructure polling/reconnect backoff (up to 5 seconds) is not a business retry.
Each internal database operation has a 5-second deadline inherited by every
query and authorization callback, preserving tighter caller deadlines. Connection
and rollback cleanup are bounded separately, so a pre-execution blackhole cannot
pin a worker polling slot indefinitely.

No network subprocess or external side-effect executor can be registered: all
registered kinds must declare safe replay or the explicit SingleAttemptOnly policy described below.
Future subprocess work needs process-group termination and reconciliation support
before registration, rather than inheriting this executor's cancellation claim.

## API and source field mapping

Prefix `/api/tasks`; live session identity supplies tenant and actor. Body/query
identity assertions are invalid. Auth adapter owns strict JSON/query decoding,
Origin, Content-Type, CSRF, rate limits and fresh password/TOTP proof on all
mutations. All Tx engine methods must be rolled back on error.

- GET `/kinds`: `{kinds:[{taskName,payloadVersion,scope,maxAttempts,timeoutSeconds}]}`
- GET prefix itself: pageIdx=1, pageSize=20 (1–100), optional domainId, taskName,
  state. Result `{page:{pageIdx,pageSize,total,totalPage},tasks:[],exhausted}`
- GET `/detail?taskUUID=...`: `{task,events:[]}`; chronological append-only events
- POST `/submit`: taskName, domainId, payloadVersion, payload, idempotencyKey,
  actorPassword, totpCode; result `{task,replayed}`
- POST `/cancel`: taskUUID, actorPassword, totpCode; result `{task}`
- POST `/recover`: taskUUID, new idempotencyKey, actorPassword, totpCode;
  result `{task,replayed}` with immutable parentTaskUUID

Task output has taskUUID, taskName, domainId, payloadVersion, state, sourceState,
error (safe code, never raw driver output), createdAt/updatedAt (UTC RFC3339),
attempt, maxAttempts, progress (0–100), resultVersion, result, cursor,
parentTaskUUID and optional nextAttemptAt. Internal identity, payload, keys and
lease credentials are not serialized to the browser. Empty lists are `[]`.
List filtering excludes unauthorized scopes *before* count and pagination;
foreign task IDs are indistinguishable from absent IDs. Results are ordered by
creation time then ID. Detail fails explicitly with result_too_large above 1,000
events; no unbounded history response or hidden truncation is promised.

Canonical state is queued/running/retry_wait/cancel_requested/succeeded/failed/
partial_failed/dead_letter/cancelled. Source compatibility labels map to
PENDING/STARTED/RETRY/SUCCESS/FAILURE; cancelled adds local `REVOKED`. Legacy
RECEIVED has no separate persistence state. `engineRestart` remains unimplemented
and rejected; exposing a health task does not pretend to implement restart.

The `tasks` function mark is default deny for existing/custom grants. The
permission editor must round-trip it explicitly. Read uses tasks.readable;
submit/cancel/recover and worker execution use tasks.writeable (which implies
readable). Platform administration is not an AD wildcard. Domain executors, once
implemented, must additionally pass the same live F47 resource checks.

## Atomicity, identity and admission

`SubmitTx` writes task + audit event + durable outbox in one transaction. A unique
key on tenant/domain/kind/idempotencyKey covers concurrent requests. Same key
and canonical payload/version/parent returns the existing task; a changed body
returns 409 idempotency_conflict. No duplicate event or attempt is produced by a
replay. JSON duplicate keys, invalid UTF-8, trailing data and non-object/oversize
payloads are rejected before kind validation and hashing. Canonical encoding is
also bounded after escaping, so JSON expansion cannot exceed storage limits. Payload/result/cursor
are each bounded to 64 KiB. Kind validators define their exact semantic schema.
Keys and IDs are 1–128 ASCII letters/digits/underscore/hyphen/dot. No automatic
idempotency, history or occurrence cleanup is implemented without retention input.

The trusted scheduler-only `SubmitScheduledTx` binds one
(tenant, schedule ID, UTC microsecond occurrence) to one task transactionally;
conflicting task identity fails. The HTTP API does not create schedules.

An Authorizer callback reads current durable actor/function/domain policy inside
the provided transaction. It returns a nonempty monotonic actor authorization
epoch. Admission pins that epoch; Start, checkpoint, heartbeat and finalization
require the same epoch plus current grants. Revoke-and-regrant cannot resurrect
old work. Fresh-proof recovery creates a new task using the current epoch.
Caller-supplied identity and `/check` responses are never authorization evidence.

Engine construction requires the exact expected schema version supplied by the
application's explicit migrator. Every worker transaction and health probe takes
shared advisory lock `734192801` and verifies exact `schema_version` equality,
with a row-share lock held to commit. The migrator uses the matching exclusive
lock. An absent, older or newer schema is 503 `schema_incompatible`; no claim,
attempt, recovery, checkpoint, result or history mutation is permitted.

API adapters call `engine.CheckSchemaTx` immediately after Begin, before identity,
rate-limit, proof or tenant/user locks. Public Tx methods verify this earlier
shared gate rather than acquiring it after identity locks; an omitted gate fails
with 503 `schema_gate_required`. The lock order is schema gate → tenant → actor →
task. A version change between Claim and Start produces zero execution attempts;
returning to the expected compatible version allows normal admission again.

Every authenticated path takes the tenant advisory authorization lock before
user and task rows. Workers observe immutable task identity without row lock,
invoke the authorizer (tenant then actor locks), then lock/recheck task and lease.
Task admission identity, scope, payload and epoch are immutable database fields;
workers recheck them after acquiring the task lock. Claim uses a fresh locked-row
eligibility read and state/lease/version CAS, including after READ COMMITTED
candidate selection races. Claim/recovery never acquire tenant locks or execute work, so they cannot invert
this order. No transaction holds authorization locks for the whole executor run.

## Execution, isolation and recovery

A claim reserves a queued lease, increments fencing token, and appends an event;
it does not increment attempt. Persisted authorized queued→running increments
attempt exactly once. Duplicate wakeups/claims do not consume attempts. Candidate
selection chooses the oldest eligible task per domain, then an advisory domain
gate plus live-lease query prevents two current leases in the same domain across
processes. Distinct domains execute in independent slots. Default worker capacity
is 4 (configurable 1–32); a failing/panicking domain cannot fail other tasks.
The database is authoritative; outbox rows are durable wake/event records and
polling does not depend on any notification delivery.

Heartbeat, progress and completion require owner/token, live database-clock lease,
expected state and result version. Checkpoint writes progress/result/cursor in
one transaction. Stale updates are rejected; progress cannot move backwards.
The engine fences persistence, not an unsupported exactly-once external effect.
Automatic replay is limited to explicitly safe operations or fenced immutable
database artifact staging. An expired executor cannot authorize
new effects. Expired running leases enter retry_wait or dead_letter within the
attempt budget; queued claims can be reacquired without consuming an attempt.

Queued/retry_wait cancellation is immediate. Running cancellation persists
cancel_requested, cancels executor context, and remains pending until executor
returns. A completion that wins the cancellation race records its actual terminal
state and a `completed-with-cancel-race` event. A lost cancel_requested executor
becomes failed/cancellation_unconfirmed, never a false cancellation success.
Authorization loss similarly requests context stop and becomes failed only after
return; a late successful result cannot override revocation. Worker shutdown
stops intake, cancels work and waits for the supported context-aware executors.
No local subprocess termination capability is claimed by the in-process executor.

partial_failed freezes `{objects:[{objectId,state,error?}]}` evidence, with unique
IDs and succeeded plus failed/uncertain objects. It cannot be recovered wholesale;
a future business schema must explicitly select failed objects for a new submit.
Failed/dead-letter recovery creates a reauthorized child and never resets history.
Audit/outbox/occurrence rows reject update, delete and truncate through triggers;
production split migration/runtime database roles remain a separate deployment
gate, because a database owner can change triggers.

## Verification and open gates

Unit/race tests cover strict payload/registry validation, retry bounds, state
mapping, safe defaults and panic isolation. Build-tag integration tests require
an actual ADTR_TEST_DATABASE_URL and create fresh synthetic databases. Missing
PostgreSQL is a fatal failure, never a skipped pass. They exercise atomic audit /
outbox rollback, concurrent submit/claim, uniqueness, permissions/epochs,
checkpoint fencing, crashes, retry exhaustion, schedule occurrence uniqueness,
cancellation acknowledgment and races, failure isolation and lock ordering.

Local environment has no Docker/PostgreSQL: actual database, separate process,
and browser chains must run in authorized CI against the exact integrated head.
No 209-function acceptance, original source parity, notification recovery,
production readiness or Windows/AD laboratory acceptance is implied by this slice.

## F39 private artifact integration

`audit.export` is owner-scoped. Same-key replay cannot cross actors or revive an
old authorization epoch; list counts and pages exclude other actors and stale
epochs before pagination. Generic task responses expose control state only,
with artifact result/cursor replaced by empty objects. Generic creation/recovery
rejects this kind: the dedicated audit submission route writes its protected
audit event atomically. Generic cancellation remains supported. A failed export
is retried as a new dedicated submission.

`Execution.WithTx` is the only fenced module-write path for registered
artifact executors. It checks schema, authorization epoch, owner/fencing token,
DB-clock lease, state and result version; callback writes and the checkpoint
commit together, or roll back together. Its context is bounded to five seconds.
Callbacks cannot manage the transaction through Commit/Rollback/Begin/Conn.
Registered code is trusted and must not issue transaction-control SQL.

An artifact kind's `CancelDiscardsResult` policy resolves a cancel request that
arrives before final publication as cancelled with no result. This policy is
only for staged artifacts, never for concealing completed external effects.
Private snapshot metadata and bytes are available only through the dedicated
audit endpoint's current creator, permission, epoch, domain and visibility
checks. Time-based domain expiry is checked live, not inferred from epochs.

## Single-attempt domain diagnostics

`domain.connection_test` is not marked replay-safe: even an authentication bind can affect directory lockout/audit state. Its explicit `SingleAttemptOnly` policy requires MaxAttempts=1, no retry codes, ReplaySafe=false and Schedulable=false. A lost started worker records failed/executor_lost; the engine rejects recovery after current authorization checks. A fresh dedicated domain submission needs new proof and a new idempotency key. Replaying the same key returns the original task and cannot start a second bind. Claim alone still does not execute or authorize side effects; Start and each probe stage check the live epoch and pinned revisions. Generic task result/cursor are masked for this kind; the domain endpoint publishes only fenced terminal evidence.

The executor commits short WithTx checks before network stages and again before staging the result; no database transaction spans network I/O. Joined socket cancellation bounds stopping, but cannot revoke bytes already sent or roll back server authentication counters.
