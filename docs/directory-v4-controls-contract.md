# Dictionary 4 controls: stage A contract

Status: source implementation and synthetic tests authored; **no tests, typecheck,
build, package installation, application, browser or runtime launched**. Local
execution remains on HOLD after the earlier cancelled approval attempt. This is
not UI, CI, runtime acceptance or product acceptance. Issues #25 / AD-F-034 and
#30 / AD-F-043 remain open to their wider requirements.

Base: `2c3727405bc9a1fa5cd5c5f5ab4c08b4cff89e7b`, tree
`03a5393943eccb1143ce03c2d271537c8683bb18`. Frozen design SHA256:
`6759314447856e27c892a3b18f799886e5b0937a3f1f992d1167ca30e8abf9d2`.
The parent explicitly authorized stage A only after independent hash reconciliation.

## Owned additive files

- `web/src/directory-v4-controls-api.ts`: strict task DTO, protected operation
  controller, original receipt binding, bounded source adapters, pinned readers.
- `web/src/directory-v4-intent.ts`: closed nonsecret hint types and parsers.
  Mutable per-tab storage is private to the controls module.
- `web/src/directory-v4-controls-json.ts`: v4-local native bounded JSON reader.
- Matching `*.test.ts`: synthetic contract tests, not executed.
- This contract. No old v3/member-reader, backend, workspace, App, navigation,
  harness, CI, deployment or credential/permission files changed.

## Exported integration manifest

`directory-v4-intent.ts` exports:

- `DirectoryV4Input`: exactly domainId, expectedRevision,
  expectedCredentialGeneration and idempotencyKey. Revisions stay decimal strings.
- `DirectoryV4Owner`: ID and username only.
- `DirectoryV4Intent`: input plus storageVersion=1, dictionaryVersion=4,
  kind=domain.directory_read.v4, payloadVersion=1, owner, receiptOnly:boolean,
  optional taskUUID and task-bound cancelState unknown/accepted/terminal.
- `DirectoryV4IntentRead`: empty, valid+intent, unusable, storage_unavailable.
- `DirectoryV4Storage`: getItem/setItem/removeItem adapter. Browser integration
  supplies sessionStorage; obtaining that property can itself throw and must
  result in a storage-unavailable view, never a nonpersistent mutation fallback.
- The mutable store implementation is lexical and unexported inside the controller
  module. No marker removal, binding, restriction or arbitrary-task acknowledgement
  primitive is accessible to B.
- `directoryV4IntentPrefix`, `directoryV4OwnerKey`, `directoryV4Input`,
  `sameDirectoryV4Input`, `validDirectoryV4Input`, `parseDirectoryV4Intent`.

`directory-v4-controls-api.ts` exports:

- `parseDirectoryV4Task(value, domainId, taskUUID?)`, `directoryV4TaskClass(task)`
  returning active/terminal/diagnostic; `directoryV4SourceInput(choice,key)`.
- `DirectoryV4Scope`: source/receipt/task/sync/cancel/page/detail;
  exact per-operation access paths are private and cannot be rewritten by B.
- `DirectoryV4Epoch`: required `assertCurrent():void` callback.
- `createDirectoryV4RecoveryContext():DirectoryV4RecoveryContext`: opaque,
  frozen, tab-memory-only handle with no hint or mutation methods. Its private
  per-owner stores retain only nonsecret original pins/markers and failure state,
  never Profile, CSRF or proof.
- `createDirectoryV4Controls(profile, storage, epoch, recoveryContext?)` returning
  `DirectoryV4Controls`, with the following methods:
  - readIntent(): closed hint status; recoveryIntent(): nonsecret recovery copy only.
  - recoveryDurability(): durable/memory_only/none/unusable. This describes hint
    persistence, not current authorization or mutation eligibility.
  - authorize(scope, signal): current session/menu/path revalidation.
  - listSources(SourceQuery, signal): bounded source page, pre/post source access.
  - resolveSource(DirectoryV4Input, signal): exact three-pin SourceChoice.
  - receipt(signal): original domain/key only; binds UUID once. A contradictory
    persisted or returned UUID is rejected. A completed receipt 404 creates one
    in-memory retry opportunity only when no bound task/receiptOnly conflict exists.
  - task(signal): serialized dedicated GET, original receipt before and after,
    post-operation authority; returns fresher receipt snapshot, never delayed GET.
    Receipt/task may return a fully verified task despite metadata storage failure;
    recoveryDurability then reports memory_only and mutations stay blocked.
  - sync(input, proof, signal): controller-owned duplicate lock, prechecks, durable
    exact readback before POST, exact wire body, post-original receipt. Existing
    unresolved intent requires the single fresh receipt-404 opportunity and
    unchanged pins/key. A found receipt disables sync, irrespective server replay.
  - cancel(proof, signal): current original receipt/task and cancel authority,
    durable unknown marker before POST. No source resolve, grant introspection or
    producer gates. Exact proof rejection is handled inside this request closure.
  - acknowledge(signal): fresh original task query; normal terminal only; removes
    whole matching intent and verifies removal. No partial marker reset affordance.
  - page(pageIdx,pageSize,signal): requires this controller's verified original
    success, observationId=original task UUID, kind=group; exact source resolve and
    reader checks before/after. New page generation invalidates old page/detail.
  - detail(objectGUID,signal): only a group on the most recently accepted page;
    fixed five-pin detail, source and reader checks before/after, generation guard.

No raw sync/cancel HTTP helper is exported. UI must use these controller methods;
no generic task cancel/recover, sourceAPI's unbounded reads, latest observations,
current-source replacement, historical UUID reconstruction or hidden retry path.

`directory-v4-controls-json.ts` exports `directoryV4ControlsBodyLimit` (8 MiB),
`parseDirectoryV4UniqueJSON(text)` and `readDirectoryV4ControlsJSON(response,signal)`.
The latter is the direct native-runtime target for C's no-store tests; synthetic
Response tests alone cannot establish browser native completion behavior.

## Required B lifecycle responsibilities

The controller snapshots Profile's in-memory identity. Instantiate it once per
security epoch, not every status poll. `assertCurrent` must reject old generations
on navigation, Back/Forward, account/session change, hidden/blur, source/task
replacement and explicit close. Increment generation before aborting; abort alone
is not sufficient against an ignored signal. Reentry starts with current authority
and original receipt, even if a persisted UUID exists. Never display a stored hint
as currently authorized private facts. Task-read denial retains only a generic
recovery notice; the independent member reader remains available on reader grants.

Hold one opaque recovery context in App/session lifetime across workspace route
unmount/remount, and pass the same storage adapter object to every epoch factory.
Creating a new controller invalidates all prior controllers using that context,
including prior owners. Per-owner recovery remains private and is not adopted by
another owner; returning to the original owner still starts with fresh authority
and original receipt checks. A replacement adapter is rejected as
invalid_recovery_context, rather than silently switching persistence destinations.
The optional-context default creates an isolated one-controller lifetime; B must
pass its long-lived context for route and epoch continuity. Context is never
serialized. A full browser reload cannot preserve RAM-only failed storage writes;
only the last successfully stored record survives that boundary.

On activation inspect both readIntent and recoveryIntent internally before choosing
a fresh-operation view. A retained fallback means recovery even when durable
storage is empty or unavailable. Do not display hint pins before authorization.
Old-controller hint getters also reject stale epochs. When memory_only, display
that the current task was verified but recovery cannot yet be durably saved;
do not claim reload durability. Continue original read-only queries and let the
controller repair storage only from the original verified receipt. No UI write,
marker reset or synthetic Task is permitted.

Proof is held only in uncontrolled DOM and the current in-flight caller closure.
Clear DOM synchronously before calling sync/cancel, on all security boundaries,
and on explicit close/collapse. Do not key/remount proof inputs on harmless
heartbeat, updatedAt or successful authority rechecks. Do not persist Profile,
CSRF, password, TOTP, request/response/result bodies or members. No logging, tracing,
analytics or automatic screenshots of proof-bearing flows. Clearing references is
not a claim of cryptographic JS memory zeroization.

Handle errors by clearing stale private views on 401/403, obsolete views on 409,
and preserving original recovery. Known status is retained even when body parsing
fails. `invalid_credentials` with status401 alone is NOT a UI marker-reset permit;
only the controller's private exact current-request evidence can attempt removal.
Storage failure is `recovery_storage_failed`; no POST is sent when the preceding
required intent/marker write or exact readback fails. Failed UUID metadata writes
retain the original durable key and conservative memory; renewed original receipt
can repair metadata. Fully verified receipt/task results remain available read-only
with memory_only status if that repair fails. Sync/cancel still report persistence
uncertainty. The UI must report persistence failure, not server failure, and must
not swallow authentication, parser or original-identity errors as storage failures.

Do not infer initial deployment status from consumerEnabled or connection test.
Describe the two default-off producer flags as unknown unless the backend reports
disabled. Collection needs current explicit `domain.directory_read.v4` account,
role/grant, domain and source pins; connection-test/v1/v2/v3 grants do not substitute.
403 is current authority unavailable, not a proven missing-v4-grant diagnosis.
Cancellation is separate and does not require producer eligibility.

## Closed state, storage and proof semantics

Normal task grammar: queued/0/PENDING; running or cancel_requested/1/STARTED;
succeeded/1/SUCCESS/progress100; failed/0-or-1/FAILURE;
cancelled/0-or-1/REVOKED. Active terminalAt is null, terminal is valid UTC.
Always exact kind, payloadVersion1, maxAttempts1, empty parent/result/cursor,
closed task fields, safe error code, valid UUID/domain and numeric bounds. Reject
retry_wait and every nextAttemptAt, even undefined as an extra own key. Shape-valid
partial_failed/dead_letter are diagnostic-only, never result/cancel/reset eligible.
Task terminal time is independently assigned by a database trigger; it need not
be less than updatedAt, so no invented timestamp-order restriction is applied.

Storage namespace is `adtr.directory-sync-intent.v4:ID:encodedUsername` only.
One unresolved intent per actor/tab. Parse closed fields, owner and versions;
reject duplicates, wrong profile, malformed or unknown fields without deleting
or reclassifying as empty. No legacy namespace migration. Hints never authorize
server access. Explicit normal-terminal acknowledgement is the only reset.

Sync transport errors never prove failure. Recover original receipt; found means
no POST replay; 404 means not found or not currently accessible, not proven absence.
Every retry needs an explicit fresh receipt check and fresh proof, unchanged pins
and key. Source/version/idempotency conflict makes receiptOnly sticky; later proof
rejection cannot clear it. Unknown receipt-only operations have no abandonment.

Cancel marker unknown is durable before dispatch. Queued/running reads never
unlock it. cancel_requested becomes accepted; normal terminal becomes terminal;
only whole-intent acknowledgement clears completion. UI may state actual task
outcome, never executor-return or dependency release: those witnesses are absent
from the public DTO. Lost/malformed/stale error always leaves query-only recovery.
Only bounded unique-key HTTP401 body exactly `{error:"invalid_credentials"}` with
application/json, exact no-store and nosniff metadata from this cancel closure
qualifies for renewed same-session/original-receipt/cancel-authority checks and
verified marker removal. No generic401/403/409 or future old response qualifies.

## Limits: purpose, necessity and consequences

8 MiB is required on every new response, including source/auth/access and error
responses, to prevent unbounded chunked or dishonest Content-Length reads. Fatal
UTF8, duplicate-key scan and full JSON parse are required before DTO validation;
removing them admits ambiguous/normalized data. Structural depth is additionally
limited to64, above these shallow DTOs; unexpectedly deep responses fail closed.
These limits can reject otherwise valid oversized backend data rather than showing
partial data. All streams check abort before/after each read and EOF, cancel on
failure and release locks. Requests use same-origin credentials, no-store,
redirect:error and AbortSignal; removing those changes session/cache/redirect or
lifecycle guarantees. Native no-store behavior remains a C execution gate.

Source limits retain pages1..1,000,000, sizes10/20/30/40/50, keyword50 Unicode code
points and existing controls/surrogate rejection; configured source may be
unverified. Reader sizes25/50/100, max10,000 rows, exact v4 parser and raw-member
limits are unchanged. Removing them weakens resource/semantic bounds; larger
legitimate results remain unavailable, never silently truncated/range-followed.

## Source bindings and remaining gates

Individual existing source blobs were read and hashed, not copied as a current
checkout. Additional dependencies were compared against the persisted recursive
published tree `03a5393943eccb1143ce03c2d271537c8683bb18`, tied to fixed main by its
persisted Git commit metadata:

- api.ts: `3f1efab64d9ccd61b28a6125bf13bd9f77f1fadc`
- access-api.ts: `b19c9a6cf5df6e256ca80cf7d91b1a951c6cec16`
- domain-api.ts: `1b2a1051d148c3ac40e5c93cd0bf9291bf9f7773`
- task-api.ts: `ad94d6ca30cf30f8147887ba89ec2dafdcf26e83`
- domain-selection-api.ts: `437d93205649704f3480df679476e27368f52de5`
- directory-v2-api.ts: `32b311bf5736d808494f231a2a8694472c54f922`
- directory-v4-facts.ts: `d1d7ef0c53ba3e86e24e7a1d2f0a71626237fda2`
- group-members-v4-api.ts: `2217bcb4c3cfc762eccd3c1b18b2a5359810149a`
- group-members-v4-read.ts: `b0e6521818cdf9f6154cfd6d144171bb360a9613`

Implementation review must inspect actual diff, particularly cancellation marker
recovery under storage failures, authority changes and concurrent delayed reads.
C must execute full parser/storage/authority tests, native bounded no-store stream
coverage, real UI committed loss and queued/running cancel loss, exact original
ledger/quiescence witnesses, independent read-only UI, late authority/session
responses and safe desktop/mobile pixels. Add CI/provenance/consumer registrations
only with actual executable coverage. No C code is included here. No runtime
claims, product acceptance, deployment enablement or Issue closure follows.

## Reader native identity evidence: explicitly split witnesses

The reader scenario now declares a fixed third **App-only `#account` page** in
advance, in the same browser context and native window as the original Workspace
and login switcher. Both notification transports are unavailable on the original
Workspace and the App-only page. Before actor change, the extra page must display
the settled producer account, with no loading/checking gate or mounted Workspace;
Account and SocNavigation's source closure contains no other `/me` producer.
This prevents choosing a convenient page after seeing a response.

The App-only page must begin hidden/unfocused, then undergo one real trusted
native focus/visible activation. The unchanged native helper still accepts only
the first eligible marked canonical 200 **original Response**, calls its original
`json()` once synchronously, and keeps its 15-second deadline and first-failure
rule. The root account independently must display the actual reader with its gate
open. The fixed purpose header remains caller intent, not server-trusted authority.

The original Workspace is a separate security witness. Its own hidden/unfocused
to trusted focus/visible transition must settle the root App as the reader without
an artificial navigation. Its valid Workspace-session-change callback may preempt
an obsolete passive request and force refresh; this behavior is not disabled or
reclassified as a body-reader pass. Held genuine old-actor success, proof clearing,
non-restoration of intent/results, no browser business POST, original reader GET,
two separately proved 403 write denials and unchanged database counts all remain.
The same four safe Workspace PNGs are retained; no account-profile image is added.

The genuine context `/me` checks use the reader header identity and compare ID,
username, role, priv, MFA, needChangePwd, isExpired and the exact nonempty CSRF
value only in memory at four declared boundaries. Browser auth mutations must be
zero from the first reader check through both witnesses and final authority check.
The browser request listener cannot see APIRequestContext writes: a separate source
contract closes the harness interval to declared GETs and the original two non-auth
business denial probes. New token values enter the existing leak guard; neither
tokens, token hashes nor raw profiles are emitted. Current GET/me derives CSRF from
the current session and does not rotate it. This supports source-bound sampled
session continuity, not direct cookie-byte measurement or an atomic cross-page
snapshot immune to an external change-and-restore between samples.

The closed `identityWitness.kind` is `split-app-account-and-workspace-v1`; roles,
actor digests, four samples, zero browser auth mutations, source-closed harness
writes and `samePageAtomicBinding: false` are mandatory. Missing/unknown/old
same-page atomic claims are rejected. **This is not equivalent to proving that the
original Response on the original Workspace page is the epoch whose identity
finally committed there. That same-page Response-to-commit atomic linkage is no
longer claimed.** Direct root UI/authority checks remain additional obligations,
not a substitute mislabeled as that linkage.

Every added page is registered for owned cleanup immediately after creation.
A shared 20-second finally waiting budget drains held work, removes all owned
native observers and independently attempts every teardown action and both page
closes. After exhaustion, each remaining action gets a bounded best-effort attempt;
this is not a strict total wall-clock guarantee under event-loop delay. The original
failure is retained if cleanup also fails.
Artifact cleanup semantics say `source-bound-after-finally`, never an early
`closed: true`; acceptance still requires the successful runner after finally.
The 630-second test / 660-second wrapper limits, 15-second body witness, screenshots,
proof work, backend, production App/API/Workspace, helper and CI are unchanged.
Removing the extra witness or session checks would lose the separated native-body
or authority/session evidence; removing the explicit non-atomic declaration would
misstate the weaker combined claim. The added tab consumes time inside the same
budget and still needs fresh native CI acceptance.

The earlier `identity_deadline` with no selected response and the older
`cdp_body_no_data` failure remain distinct historical evidence. Offline reproduction
shows a lawful Workspace-first preemption ordering, not the precise interleaving
of either CI failure. Source/contract tests do not establish native runtime success,
product acceptance, merge clearance or deployment readiness.
