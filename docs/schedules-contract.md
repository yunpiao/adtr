# Periodic health schedules (F49 / AD-F-190)

Refs Issue #15. This slice schedules the implemented `infrastructure.health`
database/queue check. Detection, synchronization, correlation, AD operations,
Windows integration and source-runtime parity remain blocked on their own
executor/input/laboratory contracts. The source workbook has no confirmed
complete scheduling fields. The following are explicit local engineering
decisions, not claims of source-field parity or full F49 acceptance.

## Frozen definition and grants

The only eligible kind is `infrastructure.health`, payload version 1,
`domainId:"platform"`, payload `{}`. The task registry's scheduling eligibility
defaults to false; audit exports and future kinds do not inherit eligibility.
No cron, command, URL, credentials, AD target, retry override or local-time/DST
interpretation is accepted. Each health task retains F48's existing five-attempt
read-only policy. Scheduler wakeups do not consume a business attempt.

A schedule contains a server-generated schedule UUID; server-derived tenant,
creator and pinned authorization epoch; a label of 1–80 Unicode characters
without surrounding whitespace or controls; an explicit RFC3339 anchor with
timezone and whole-second precision; and an interval of 60–86,400 integer
seconds. Offsets normalize to UTC. These interval bounds are safety budgets,
not a source capacity promise. The definition, creator, epoch and creation key
are immutable. Changing them requires a new authorized schedule UUID.

Creation starts paused and requires the anchor at least 60 seconds after the
database clock. Exact same-key creation replay is resolved before that temporal
check, so a successful request can still be retried after its anchor passes.
Creation keys are scoped to tenant and creator. Changed definitions conflict.
An old-epoch create replay returns `409 authorization_epoch_changed`.

Every route requires current `tasks.readable` and `schedules.readable`.
Mutations and scheduler admission additionally require both writeable grants.
New custom roles and existing grants have no scheduling rights by default.
Schedules are creator-scoped within the tenant; platform administration does
not transfer ownership. Foreign-owner and foreign-tenant IDs return 404 and
are excluded before list counts/pagination. Reads after an epoch change can
show the creator's old schedule when the current read grants allow it.

Schedule grants participate in the monotonic actor-authorization epoch.
Revoke/regrant, password/security changes and all F48 epoch invalidators cannot
reauthorize an existing schedule. The scheduler rechecks live durable grants
and the pinned epoch every admission. Definitive loss persists
`authorization_blocked` once; restored grants require a fresh schedule. An
enable/pause request against an old epoch returns 409 without consuming proof.
Ordinary logout does not revoke a background schedule. No password, TOTP,
session token or reusable authorization proof is stored in a definition.

## Timing and atomic admission

Occurrence n is always `anchor + n * interval`, with a nonnegative integer
cursor. It is never based on actual execution completion. Enabling a paused
schedule records elapsed slots as one `skipped_paused` range and selects the
first grid instant strictly after the database clock. Pausing blocks future
admission; an admitted task retains its ordinary cancellation rules.

For an enabled due schedule, the scheduler samples the database clock after
locking the schedule, computes latest due index k, and:

1. Records unprocessed slots before k as one immutable `skipped_misfire` range
2. Records k as `skipped_overlap` if any prior task admitted by this schedule is
   queued, running, retry_wait or cancel_requested, including an expired lease
3. Otherwise admits exactly k through `tasks.SubmitScheduledTx`, with the
   schedule's pinned epoch, and records its task link
4. Advances the cursor to k+1 in the same transaction

Downtime thus admits at most one current occurrence per schedule, without an
unbounded catch-up queue. A skip range stores first/last indices; its count and
UTC boundaries are derived from the immutable definition. History is paginated.
Manual tasks and explicit task recovery are separate operations. The existing
domain execution lease can delay actual execution after scheduled admission.

The application runs a separate scheduler loop, independent of task execution
slots. `Tick` discovers at most 32 candidates in deterministic due-time/ID order.
Discovery takes no schedule row locks. Each candidate uses a separate operation
bounded to five seconds; connection/rollback cleanup is separately bounded.
The process polls at a one-second nominal interval and may apply bounded
infrastructure-error backoff. This is not another business retry policy.

Lock order is exact schema gate → tenant authorization lock → creator actor →
schedule → task. Discovery reads only immutable candidate identity, then the
admission transaction obtains live authorization before locking and rechecking
the schedule. Concurrent schedulers serialize on that row; they never retain a
schedule row while waiting for a previously unheld tenant/actor lock. There is
no separate scheduler reservation lease to strand after a crash. Task execution
uses F48's actual durable lease, fencing and authorization checks.

Task, task event/outbox, immutable occurrence binding, schedule decision evidence
and cursor advancement commit together. Failure at any write rolls them all
back. A lost commit response is resolved by the durable cursor/occurrence on
the next pass. Same-slot replay must match creator, epoch and frozen task
identity. Reserved `schedule_` keys cannot be created by ordinary submission or
recovery and cannot cause a legacy manual task to be adopted as an occurrence.

Database/serialization/deadline/schema failures roll back and are not persisted
as privilege revocation. A backward database clock cannot rewind the cursor;
forward movement uses bounded coalescing. Integral seconds avoid duration
overflow during very long downtime. An unrepresentable next date pauses the
schedule with `schedule_time_overflow`; it does not wrap or admit ambiguous work.
Schedule definitions, skip/admission evidence and control receipts reject
destructive mutation. Physical retention/deletion is not implemented.

## HTTP and control idempotency

Canonical routes have no trailing-slash alias:

- GET `/api/tasks/schedules`: pageIdx 1–1,000,000 (default 1), pageSize 1–100
  (default 20), optional exact state paused/enabled/authorization_blocked
- GET `/api/tasks/schedules/detail`: scheduleUUID and optional pageIdx/pageSize
  for newest-first history
- POST `/api/tasks/schedules/create`: label, taskName, domainId, payloadVersion,
  payload, startAt, intervalSeconds, idempotencyKey, actorPassword, totpCode
- POST `/api/tasks/schedules/enable` and `/pause`: scheduleUUID,
  expectedControlVersion, idempotencyKey, actorPassword, totpCode

Every listed mutation field is required and non-null. Exact field names,
duplicate keys, invalid UTF-8, trailing JSON, malformed/repeated query fields,
integer bounds and unknown fields are enforced. Bodies are capped at 128 KiB;
payload validation retains the 64 KiB task boundary. Caller identity, epoch,
state, cursor, occurrence, lease or retry fields are rejected.

Every transaction takes the exact schema-8 gate before identity, rate-limit,
proof or module writes. Missing/older/newer schemas return 503 without denied
audit or proof consumption. The usual live HttpOnly session, forced-change and
password-expiry gates, trusted Origin, JSON Content-Type, CSRF, request/account
rate limits and fresh password plus unused TOTP apply to every mutation.

Create returns `{schedule,replayed}`. Control returns
`{receipt,schedule,replayed}`. Receipt is immutable and records the original
operation UUID, action, resulting state and controlVersion. Schedule is the
current state. Replaying an old enable after a later pause returns its original
receipt and the currently paused schedule; it does not enable again. Operation
keys are tenant/creator-scoped and conflicting bodies return 409. Fresh controls
use expectedControlVersion CAS and increment it once, including same-state
controls. Scheduler cursor movement does not increment controlVersion.

Mutations, their immutable receipts, fresh-proof consumption and safe successful
`auth_audit` records commit together. Successful audit actions are
`schedule.create:<UUID>`, `schedule.enable:<UUID>` and `schedule.pause:<UUID>`;
exact operation replays do not duplicate them. Failed mutations may retain only
normal denied-operation/rate-limit bookkeeping. Unknown database errors are
`500 internal`, never raw SQL details or false authorization denials.

Public schedule values include the definition except its payload, state,
controlVersion, next nominal UTC time when enabled, last admitted task/time,
safe error and timestamps. Internal tenant/creator/epoch, keys, hashes and
cursor are omitted. List/detail always use arrays for empty results. Detail
history uses the same page envelope and bounded 100-row maximum.

## Occurrence history and original task navigation

An admitted history row exposes **查看该次实际任务** for its own canonical task
UUID. Its accessible name includes that exact UUID; the original ID text remains
visible. The action never substitutes the plan's lastTaskUUID. The separate
**查看最近实际任务** action retains its existing latest-task behavior. Skip,
control, unknown-action, empty-ID and noncanonical-ID rows do not acquire a task
navigation action.

This is read-only integration of the existing health/platform/version-1
producer, not a new schedule kind or executor. The source schedule detail still
requires current schedule access and its original owner. A local read token
binds the new action to the current actor/session, schedule ID, requested page
and completed read object. Refresh, pagination, background polling/retry and
unmount invalidate readiness; stale or abort-insensitive late responses cannot
reactivate an old row. The returned page must match the requested page and page
size 20, and duplicate event IDs are not valid navigation evidence. Activation
rechecks the current token, row membership, exact UUID and existing GET gates,
not merely the rendered button's disabled attribute.

Navigation uses the existing actor-bound task history state and current task
detail permission. The destination GET checks server authority again and must
return the same task UUID. A visible history record is not a new authorization
grant. Authorized archive visibility uses visibility=all; this does not assert
that the task is archived. Other readers keep active-only behavior. A forbidden
or missing destination remains an error, with no alternate task, automatic
restore or permission change. Paused and authorization-blocked plans can retain
readable original-task history.

Unknown attempted maintenance operations do not disable this read-only action.
Navigation does not clear, rewrite or confirm a maintenance intent or task
recovery record. It does not fill authentication proof, submit recovery/cancel,
download a result, or treat a detail GET as a mutation receipt. Existing
destination controls retain their own explicit-action and permission rules.

DOM tests distinguish multiple admitted rows from an unrelated latest task and
cover invalid rows, read gates, archive visibility, refresh/page/session races,
late responses and unchanged pending intent bytes. These tests establish UI
contracts, not a real scheduler result.

The real maintenance browser scenario reuses its existing admitted occurrence
after the original latest-task and archive/restore/history assertions. It opens
the history row's original task, binds the first newly observed exact Request
to its same Response, and checks the returned UUID, health result and rendered
identity. The selected response is parsed once; a body or identity failure is
not replaced by a later response. Both click and response validation receive
immediate rejection handlers and are joined before their listeners are removed
on normal completion; failure cleanup also removes listeners and joins started
work. This remains Playwright/CDP response evidence, not a claim about the
application's native parser or a separate hard body-cancellation deadline.

No second natural occurrence, TOTP, business write or download is added. The
existing bootstrap 9-of-10 and producer 10-of-10 proof usage, 480-second browser
test and 540-second runner budget remain unchanged. The archive summary schema,
source-key set and its two PNGs remain unchanged; those PNGs document archive
target observation, not pixels of the new history action. New navigation must
pass on the final integrated head before it is accepted as real browser evidence.

## Verification and limits

Local unit tests cover exact definition/UTC validation, integer grid boundaries,
long downtime and overflow, pagination, private-field serialization and HTTP
trust boundaries. Integration-tag tests create actual isolated PostgreSQL
databases and fail when `ADTR_TEST_DATABASE_URL` is absent; they never skip.

Named database cases cover concurrent schedulers and one occurrence, downtime
ranges, all nonterminal overlap states, expired task-lease fencing, evidence
rollback, revocation/restored epochs, database-error classification, pause races,
durable control replay, late creation replay, owner isolation, schema pregating
and immutable evidence. Auth integration adds actual session/proof/CSRF/grants,
strict bodies, schema-before-identity and shared-audit/proof rollback coverage.

Unit/race/vet and integration compilation are separate from actual PostgreSQL,
worker-process and built-browser execution. Those real chains must be verified
against the final integrated head in authorized CI. This contract does not
claim full F49, source parity, original business executors, AD/Windows execution,
production capacity or retention acceptance. F49's message-cleanup inputs remain
unresolved; immutable task events/outbox are not disposable user messages.
