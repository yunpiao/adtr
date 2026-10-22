# Group asset XLSX export v3: bounded EXP-008 slice

Contract freeze 2026-10-14; PR161 merged and bounded engineering evidence verified.
Base main `9f91f7121a00b02101f53c545e3da53da1ec27ed`, tree
`6625e1166634a9627a07f28a5e6007007aaacc94`. Refs
[F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-053 / EXP-008,
[group list contract](group-assets-v3-contract.md), and
[user export lifecycle](user-asset-export-v3-contract.md).
AD-F-053 remains in_progress; Issues30/151 remain open; product acceptance is0/209.

## Verified publication checkpoint

[PR161](https://github.com/yunpiao/adtr/pull/161) final head
`5c9b0ee673e35955bcaf4147cae075015bae4cf7`, tree
`bdfc54e0982cb07a4679c5fa022f9324da31832a`, passed all65 jobs in
[38530232594](https://github.com/yunpiao/adtr/actions/runs/38530232594), attempt1.
Merge `7ed455aecee1352f0261e7064253891af1c31c79` passed separate main-push
[38535766672](https://github.com/yunpiao/adtr/actions/runs/38535766672), attempt1,
65/65. Each run's three actual artifacts were independently reviewed:8/5/3 files,
six PNGs,901 source objects, four XLSX with120/48/2/48 cells and23/23/0/23 data rows,
plus matching PostgreSQL/HTTP digests. Failure uses selected U+FFFE text followed
by a new-key safe-column recovery; queued cancellation records attempt0.

Historical [38520290658](https://github.com/yunpiao/adtr/actions/runs/38520290658)
retains62 passing/3 failing jobs; [38525081993](https://github.com/yunpiao/adtr/actions/runs/38525081993)
retains63 passing/2 failing jobs. Test-only UI timing, strict DTO and real MFA-window
repairs preceded the final success; those earlier failures are not erased.
Evidence summary is also recorded in [Issue30](https://github.com/yunpiao/adtr/issues/30#issuecomment-6165117314).
These runs verify that exact group-export revision, not future computer-export or
documentation changes. Full legacy parity, running cancellation and acceptance
gates below remain open.

## Frozen source, filters and columns

One immutable published dictionary3 observation, one explicitly authorized domain.
Required domainId, expectedRevision, expectedCredentialRevision and observationId
pin the displayed source; absent or stale pins cannot select latest implicitly.
The complete observation is validated before kind selection/filtering, including
excluded users/computers. No LDAP operation, credential use or new observation.
Dictionary4 member data is never joined into this export.

The exporter and existing group list share the exact group-row predicate/order
helper. Optional search uses Go simple-lowercase literal contains independently
on SAM/SID/DN, maximum20 UTF-16 units and80 UTF-8 bytes; no trim, normalization,
regex or mail search. Optional creatTm=asc|desc uses canonical whenCreated,
null-last and GUID ascending ties; omission uses GUID ascending. Empty search
matches all groups. Export includes ALL matching rows, regardless of current page.
Pagination fields and unsupported groupType/groupScope/groupMold/status/domainName/
overviewType/updateTm/startTm/endTm/sAMAccountName/scoreSort are rejected rather
than silently ignored. Full old EXP-008 filter/default-field parity remains open.

Required screenColumns is an array. Empty array resolves to exactly
`guid,sAMAccountName,sid,dn,whenCreated` in that order. Nonempty arrays preserve
caller order; absent/null, duplicates and unknown columns fail validation.
Headings are `GUID,SAM,SID,DN,whenCreated`. Null scalar values become empty cells.
Only these five collected scalar facts are supported; no inferred group name,
classification/scope/type, business memberCount, application or membership values.
Observed zero groups and zero matches produce a genuine header-only XLSX;
unavailable/stale/corrupt source fails, never masquerades as empty success.

## Separate authority, task and storage identity

Migration22 registers `group_asset_exports` readable/writeable capabilities and
its epoch invalidation trigger. Existing custom roles receive no grants; exact
permission catalogs gain an explicit false/false mark. The established built-in
platform-admin capability convention remains, with explicit domain resource scope.
This registration is required for independent export authorization: omitting it
would fail closed, not inherit user/audit export authority. No deployment toggle,
role assignment, resource scope or credential-use grant is enabled by this slice.

Read operations require domains.readable, directory_assets.readable,
group_asset_exports.readable and tasks.readable. Submit/cancel additionally require
group_asset_exports.writeable and tasks.writeable, same-origin JSON and fresh
password/TOTP proof. directory_assets.writeable is not required. User-export and
audit-export marks do not authorize group export. Every API/Worker transaction
checks actor, tenant entitlement, current domain/resource scope, exact schema and
authorization epoch. Only the creator may read task, receipt and artifact, including
via generic task APIs. Epoch changes invalidate old artifacts permanently.

Fixed task kind `group_assets.v3.export`, separate snapshot/chunk/manifest storage,
fixed `GroupAssets` worksheet and `group-assets-v3-<taskID>.xlsx` filename prevent
cross-kind task/receipt/artifact confusion. Generic task payload/results remain
sanitized; generic submit/recover/cancel cannot bypass dedicated checks. No new
scheduler or archive kind. Inaccessible owner/epoch/task is404; revoked route
permission is403. Another actor cannot recover an identical idempotency key.

## HTTP and UI lifecycle

Prefix `/api/group-assets/v3/exports`; response envelopes, headers, strict JSON/
query limits, idempotency, receipt recovery, status mapping, download validation
and cancellation semantics follow the user-export contract with group identities.
Successful replies include X-ADTR-User-ID, no-store and nosniff. Proof never enters
payload, fingerprint, logs, browser storage or evidence.

- POST prefix: required pins, screenColumns, idempotencyKey, actorPassword,
  totpCode; optional search/creatTm. Returns200 `{taskID,task,request,replayed}`.
- GET `/receipt?domainId=...&idempotencyKey=...`: original-operation lookup,
  returns200 `{taskID,task,request}`;404 does not prove a lost submit never committed.
- GET `/detail?taskID=...`: `{taskID,task,request,downloadReady,downloadStatus}`;
  only ready includes artifact `{rowCount,byteCount,sha256,filename}`.
- POST `/cancel`: exactly taskID, actorPassword, totpCode;200 `{taskID,task}`.
  queued/retry_wait becomes cancelled; running becomes cancel_requested until
  the Worker actually confirms cancelled. Succeeded task is409 task_terminal.
- GET `/download?taskID=...`: complete validated XLSX with exact Content-Length,
  X-ADTR-Artifact-SHA256 and X-ADTR-Row-Count. No partial success response.

Canonical request is `{domainId,expectedRevision,expectedCredentialRevision,
observationId,search,creatTm,screenColumns}`; optional strings canonicalize empty.
Status pending means queued/running/retry_wait/cancel_requested, ready means a
succeeded eligible manifest, failed means failed/partial_failed/dead_letter,
cancelled means persisted cancellation. For succeeded stale pins return
source_changed; unavailable observation returns source_unavailable; malformed
manifest/result returns artifact_invalid. All latter states disable downloads;
DB/schema failures stay503. Receipt and same-key replay preserve original outcome
when the source changes, after current owner/epoch/authority checks. Cancel does
not require obsolete source pins. Resolve uncertain cancellation by reading detail.

UI binds every request/result to actor/domain/revisions/observation/filter generation,
shows all-matching scope and selected columns, and invalidates stale responses.
Ambiguous submission retains its original key; receipt lookup never auto-resubmits.
Session recovery stores only actorID, domainId, original key and fixed version/kind
with strict size/type/actor/kind/schema checks and sessionStorage lifetime. It never stores filters, proof or tokens.
Cross-actor/source changes cannot resurrect private data. Download revalidates
session, exact actor/kind/task metadata, bounded byte count and SHA256 before a
Blob URL; revoke URLs, discard protected results and prevent download on
authority loss. JavaScript buffers are not claimed to be explicitly zeroed.

## Execution, cleanup and limits

Replay-safe task: maxAttempts3, lease30seconds, heartbeat5seconds, timeout5minutes;
ordinary RetryCodes allow database_unavailable after5/10seconds (cap60seconds).
The existing replay-safe Engine also recovers worker_interrupted and expired
leases under the same maxAttempts3 budget.
These positive bounds prevent unbounded execution/retention. Authorization loss,
stale source, corrupt observations, invalid selected text and deterministic
row/byte bounds fail without retries; removing bounds is outside this contract.
Capture immutable matching rows and the complete observation SHA256 under fencing.
Every persisted transaction and final Engine.Finish success gate rechecks exact
source/digest/authority/time eligibility. Staged95 is not downloadable; only
fenced succeeded state publishes. Cancellation/revocation wins over success.

Separate chunks are at most1MiB with ordinal and digest. Manifest is created only
after ZIP close; download validates contiguous chunks, per-chunk/whole SHA256,
length and exact task-result agreement before sending bytes. Failed/cancelled/
dead_letter cleanup removes generated snapshot metadata/chunks/manifests even
without a live executor, while retaining original receipts/events. Success retains
its winning artifact without inventing a retention period. Obsolete attempts are
removed under fencing before retry; source observations remain immutable.

All cells are inline text, preserving formula-like prefixes, XML metacharacters,
CR/tab/LF, Unicode and literal OOXML escape sequences. No formula/hyperlink/macro,
external relationship or implicit number/time conversion. Invalid XML text in a
selected cell fails safely; do not strip/truncate data or echo it in diagnostics.
Bounds: source<=10000 total objects/16MiB encoded, export<=10000 data rows/five
columns, cell<=32767 UTF-16 units, XLSX<=32MiB. Overflow fails, never truncates.
These are protective limits, NOT legacy100000-row or peak-memory capacity
acceptance. Decoding/selection and fully prevalidated download use bounded memory;
no claim of end-to-end streaming source processing.

## Verification gates and evidence boundaries

Required unit/HTTP/real-PostgreSQL/DOM tests include shared list-export matching and
order, all pages, empty/header-only, optional/invalid columns, stale pins, whole-body
corruption, formula/XML fidelity, limits, actor/tenant/kind/epoch isolation, receipt
ambiguity, lease/fencing/retry, final source rotation, cleanup and artifact integrity.
Dedicated GitHub CI browser suites must exercise normal/all-matching plus empty,
failed export followed by explicit safe-column recovery, and queued cancellation
with owned-Worker quiescence, resumed queue activity and durable zero-artifact proof.
Success artifacts bind actual XLSX/PNG and database/source/checkout/run provenance.
Old62 checks and historical failure evidence remain required; new evidence cannot
borrow PR159/160 success. No local Go/Node/PostgreSQL/browser execution is claimed:
local validation is static/gofmt and explicit pure-Python with native spawn denied.
Independent actual-diff review, exact candidate/current-main CI and real artifact
review are mandatory before normal merge; main-push verification is separate.
Running/cancel_requested browser acceptance, full legacy semantics, dictionary4
membership, real AD/eight Windows,100000-row capacity and production remain open.


## Candidate: snapshot-stage running cancellation (2026-10-14)

The test-only `group-asset-export-v3-running-cancel` suite extends PR163 main
`ec0da2018cc5914b8d0ca345c1c2c4cd5078d177` (tree
`ec801e6ffc0bce09cd89714bb1be3586d2781fad`). Together the group and computer
suites add exactly two isolated stable-body jobs to the prior69, yielding71.
The new reversible contract restores all prior runner/workflow bytes. The old
terminal suites remain separate, with no double configuration or validation path.
Exact-head runtime verification and independent artifact inspection are pending.

The fixed runner-selected kind binds the owned Worker guard to the group export
identity, snapshot tables, and original source. The guard's default remains user;
the HTTP checkpoint accepts only taskID/workerID, never a caller-selected kind.
`running` here requires a durable snapshot and progress10 for an executing attempt;
`cancel_requested` requires a persisted request while that Worker remains paused;
`cancelled` requires its genuine resumed terminal transition and cleanup. A short
activity-row lock exposes this checkpoint; it must not block the API task-row write.
More than20seconds live lease is required before STOP, with CONT within10seconds
and owned-process identity rechecked. These test-only guards are necessary to keep
the observation within the same live execution; removing them can test completion
or lease recovery instead. Failure cleanup resumes the Worker and releases locks.
Production lease/heartbeat/timeout values and security limits remain unchanged.

A successful case must prove the original receipt, durable transitions/events,
no downloadable file, zero snapshot/manifest/chunk rows, and subsequent activity
by the same Worker. A freshly completed group source and kind-specific sensitive
operation budgets are required. Evidence formatVersion1/suite runningCancel is
bound to actual checkout/tree/run/source hashes. Only the exact provenance JSON,
evidence JSON, running-checkpoint.png, cancel-requested.png and cancelled-terminal.png
are staged after validation; raw logs, proof inputs, XLSX and traces are excluded.
CI screenshots must be inspected independently, not inferred from validator success.

Local checks are static or explicitly allowlisted offline Python with subprocess,
network and signals rejected; local Go/Node/PG/browser/build are not run. This
candidate does not establish chunk-stage cancellation, fencing races, legacy field
parity, capacity or real AD/Windows acceptance. AD-F-053 stays in_progress;
Issue30/151 and product acceptance0/209 remain open.


## Task-ID original result read (AD-F-152 / AD-F-151, Issue #26)

This bounded follow-on to AD-F-053 / EXP-008 exposes an independent read-only
`GroupAssetExportTaskResult` only for exact `group_assets.v3.export` task
records. It does not extend the five permitted columns or the group search
predicate. The group canonical request remains exactly seven keys: domainId,
expectedRevision, expectedCredentialRevision, observationId, search, creatTm and
screenColumns. It has no startTm/endTm; extra time keys are rejected. This does not add a producer, API, permission, database change or LDAP use.
User and computer result behavior is unchanged. Generic task viewing remains governed
by its original permissions; denied dedicated detail disables only this entrance.

The dedicated gate probes only the existing detail/download read paths, and requires
readable domains, directory_assets, group_asset_exports and tasks. Download is
independently disabled if its path is denied. The access/check POST is a permission
probe, not an export mutation; export traffic is GET only. Current actor headers,
server tenant/domain ownership and authorization epoch checks remain mandatory.
First detail-by-task validates the exact task ID, domain and kind plus the complete
canonical request/artifact. That first request is locked for subsequent reads,
including close/reopen; refresh cannot substitute a new observation or revision.
The panel displays original source pins, canonical filters/sort/ordered columns,
status and artifact. source_changed keeps the original source visible and disables
download rather than adopting the latest observation. Pending, failed, cancelled,
source_unavailable and artifact_invalid are also non-downloadable.

The original download implementation is unchanged: fresh detail, strict headers,
bounded complete bytes and SHA256, then another fresh detail binding the same
request and manifest before a browser file may be created. Close, task/domain,
account/session change, unmount or parent generic-task refresh abort and invalidate
outstanding metadata/body/postcheck work. Authority failure clears private detail;
401/session loss requests session refresh. Permission recheck is manual; there is
no polling or automatic retry. This read path does not read or write recovery
storage, generate an idempotency key, submit, cancel or replace unknown intent.
Original creation and submission-receipt recovery remain in the group asset export
panel. The independent task-ID cancellation entrance below does not change this
result component's GET-only behavior.

Named DOM/API tests exercise first binding, changed sources, independent permissions,
all non-ready statuses, late metadata, actual streamed-body/postcheck interruption,
byte/manifest corruption and postcheck revocation. These synthetic tests are not
real source evidence. The existing group export browser suite retains its
three real default/allMatched/empty producer flows, source pins, database/sensitive
operation budgets, XLSX validations and original ready-status/task-ID/download
viewport assertions. After removing the persisted recovery locator, a new tab
opens each actual task, reads its original result and downloads identical bytes;
its export traffic must be exactly detail/detail/download/detail GETs with no
receipt lookup or export POST. The original XLSX artifact remains independently
validated, and the task-view copy must be byte-for-byte equal.

The existing validator and upload allowlist admit only default-ready.png,
allMatched-ready.png and empty-ready.png. Those three 1440 x 900 evidence slots
now show the task original-result panel, while retaining the original flow/layout
assertions before navigation. They do not establish new pixel acceptance for the
old ready panel or mobile/narrow layouts. Evidence must be generated and reviewed
from the exact submitted CI head; local runtime was not executed for this change.
Issue #26 and Issue #30 remain open; this is not full product acceptance.


## Original-task group cancellation (Issue #214)

This bounded usability integration exposes cancellation of the same existing
`group_assets.v3.export` task from its generic task detail. It uses the existing
producer and dedicated `/api/group-assets/v3/exports/detail` and `/cancel` APIs;
it does not add a task kind, backend cancellation semantics, grants or source
collection. Generic task visibility alone does not authorize group cancellation.
The backend still requires the original owner, tenant, domain/resource scope,
current authorization epoch and dedicated read/write permissions. Stale original
source pins do not themselves block cancellation. Generic task cancel stays absent.

The new section is `任务原组导出取消`. Opening `核对原组导出取消` checks current
identity/permissions and reads the original task before exposing proof inputs.
`读取原组导出当前状态` is an explicit read-only recovery action; it never retries a
POST. The bound request is exactly domainId, expectedRevision,
expectedCredentialRevision, observationId, search, creatTm and screenColumns,
including the original ordered columns. The group valid-ID grammar is retained;
no user/computer-only filter, UUID-only restriction, latest-source adoption or
new tab submission locator is introduced. The sibling original-result panel
remains GET-only and does not read proof, automatically download or cancel.

Both cancellation entrances share the group-specific exclusive Web Lock and
localStorage marker keyed by actor/task. Before a POST, the complete canonical
marker is written and read back under the lock. Its fixed fields are version,
kind, actorID, taskID, domainId and the SHA256 of the seven-field original request.
It contains no password, MFA code, CSRF/session token or raw filter values. This
client guard prevents duplicate unknown-intent POSTs across cooperating tabs and
remounts in the same browser profile, not across devices or untrusted clients.
It is not a new backend security requirement: the backend cancel remains
idempotent. Removing it would lose this client unknown-outcome protection.

If Web Locks or durable storage is unavailable, only these two cancellation
entrances fail closed with a reason. Existing reads, downloads, create and ordinary
GET polling remain available; no browser/system permission change is requested.
An ambiguous POST remains query-only across remounts. A freshly authorized exact
terminal detail can remove only matching marker bytes under the lock. The other
narrow clearance is this same invocation's verified pre-dispatch401
invalid_credentials proof rejection: bounded unique JSON and required metadata,
private API-origin proof, successful current-actor/epoch reauthentication and
pinned detail checks, followed by matched removal and null readback, all while
continuously holding the same lock. Other401, malformed/unknown outcomes and
nonterminal states never clear it. A failed removal/readback keeps the current
lifecycle query-only; it does not prove a removed persistent marker still exists.

Task/account/domain/authorization changes, focus/visibility/storage invalidation
and parent refresh abort or disregard stale private work and destroy proof.
Explicit authorized read restores the entry; it does not resurrect old proof.
Ordinary same-task receipt/detail reads and GET polling are not such invalidation:
the old running flow retains already-prepared MFA through those reads. After
cancel_requested its existing `取消组导出任务` button remains present and natively
disabled. Existing real running compatibility is still required; the new running
entrance has DOM/API coverage only, not new real running-pixel acceptance.

The existing real queued suite now opens a same-session tab with no opener and
no cloned/seeded recovery locator, filters the real task list and clicks the exact
original task row. The collapsed detail performs no group-export business read
or mutation and exposes no proof. It opens the dedicated original detail and
submits exactly one fresh-proof cancel. A narrow read-only native-fetch observer
compares only the selected marker's complete expected bytes before that original
POST, retaining only match/count/error flags. It forwards the original receiver,
arguments and promise, never reads the request body, and cannot suppress the
request on an observation error. It adds no route, mock response, replay or cache
change. Closing its owned tab disposes that document; shared capture hooks and
business storage are not rewritten for cleanup.

Both subsequent old-panel receipt/detail reads observe two newly emitted host
Request objects around one original button action. Identity-bound responses and
action branches are joined and listeners/timers are detached before resource
cleanup. This is browser response evidence, not a stronger claim about native
fetch start time or application consumption. The original submission locator must
remain byte-for-byte unchanged. Original terminal UI/no-download assertions,
attempt0 and zero snapshot/manifest/chunk rows remain. After the same owned Worker
resumes and completes two queue cycles, receipt/detail, database and unavailable
download are read once in their original order and all invariants are rechecked.

The suite keeps setup owner7/producer9, one create plus one cancel yielding
owner9/producer9/exportTasks1, its10-proof ceiling,900-second test and120-second
initial pause. Shared helper5/10/15/20-second bounds and original running guards
are unchanged. Twenty-one fixed phase labels distinguish the awaited operation
on failure, including individual post-resume reads, invariants, scroll and each
old-panel viewport check. They add no requests, proof or retries and do not identify
a Worker/focus/load root cause. Only a closed phase and cleanup_failed flag are
reported; raw errors, causes, IDs, URLs, headers, bodies and proof are not attached.
New-tab close and owned-Worker restore are independently attempted; either failure
fails the suite. Process/container cleanup needs its independent runner evidence.

The same three success artifacts remain: group-asset-export-v3-cancel-provenance.json,
group-asset-export-v3-cancel-evidence.json and cancelled-terminal.png, with the
existing schema, strict validation, run/attempt/source binding and14-day retention.
The1440x900 PNG now depicts the actual new task-cancel section, original ID and
confirmed cancelled status, with direct section-and-page proof-input count0
immediately before capture. The prior old-panel DOM/viewport checks remain but
this single image does not claim new pixel evidence for both layouts. No mocked
running screenshot, extra artifact, automatic download or business write is added.

The terminal source contract preserves its original artifact specimens and
corruption cases, strengthens complete cleanup/paired-observer/marker/phase order
checks, and adds unique-target negative mutations including CRLF/trailing bytes.
It is static regression coverage, not browser execution. Independent actual-diff
review, exact-head CI and artifact/pixel/cleanup review remain mandatory. This
integration does not complete full legacy semantics, real AD/Windows, capacity or
product acceptance; the209-item acceptance ledger is unchanged.
