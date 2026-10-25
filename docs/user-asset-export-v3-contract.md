# User asset XLSX export v3: implemented bounded first slice

Interface/lifecycle freeze, 2026-10-13; evidence reconciled 2026-10-14.
PR159 and PR160 are merged. AD-F-053 is in_progress and product acceptance remains 0/209.
Original implementation base: actual remote main `325f28095b9fb5a60c7e43e698ac2013d78d4a59`, tree
`d52cc6084bf765e7d768f1d037ef7c435f5ab3d7`.
References: [F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-053 / EXP-006,
[F38 #26](https://github.com/yunpiao/adtr/issues/26),
[v3 user list](user-assets-v3-contract.md), [task contract](task-contract.md).
This bounded slice is not complete legacy EXP-006 compatibility or product acceptance.

## Scope and source identity

Only dictionary-3 users from one immutable, already published observation. No LDAP,
credential resolution, collection flag, domain writing or v1/v2 fallback. A request
must carry domainId, expectedRevision, expectedCredentialRevision and observationId.
Each pin is required: without it export could silently move away from the displayed
source. All matching users are exported, not only the current page. Pagination input
is rejected. Filters and ordering use the very same helper as the existing v3 list:
search (SAM/SID/mail/DN, Go simple lowercase literal contains), exact sAMAccountName,
paired startTm/endTm half-open whenCreated range, optional uacDisableBit
set/clear/not_returned, creatTm asc/desc with null-last and GUID tie-breaking.
Omitted optional predicates broaden the match; omitted creatTm
uses GUID order. Unsupported status/overviewType/updateTm and other legacy filters
are rejected, never silently ignored.

The full observation codec is validated before filtering, including excluded objects and
unselected fields. Corruption fails the whole task. An observed empty source or zero
matches yields a real header-only workbook. Unavailable/stale source is an error,
not an empty workbook and never a switch to latest.

## Explicit permissions and isolation

New mark `user_asset_exports`, with readable and writeable capabilities. Migration
21 registers the mark and epoch trigger but grants no existing custom role rights.
Read operations require domains.readable, directory_assets.readable,
user_asset_exports.readable and tasks.readable. Submit/cancel additionally require
user_asset_exports.writeable and tasks.writeable, same-origin JSON and existing
fresh password/TOTP proof. directory_assets.writeable is NOT required: export does
not authorize a collector. Audit permissions have no effect on these routes.
Platform administrator follows the existing function-grant convention, but still
requires explicit current domain resource scope. No resource or role assignment is
made by implementation, migration or UI.

Every API/Worker transaction checks current tenant entitlement, domain active/scope,
durable actor state, authorization epoch and the exact application schema. Task kind
`user_assets.v3.export` is domain-scoped, owner-scoped, not schedulable. Only its
creator can read its task/receipt/artifact, including through generic task APIs.
An epoch change permanently invalidates the old artifact even if grants return.
Receipt/detail and same-key replay inspect the original task without revalidating
source availability: source changes cannot conceal the original operation result.
They still require current owner/epoch/resource/function authority. Revoked route
permissions return403; inaccessible owner/epoch/task returns404 without revealing it.
Current source revisions and observation eligibility are checked before new capture,
each persisted artifact transaction, final publication and download. Clock-based
entitlement/password expiry is rechecked at transaction completion.

## HTTP contract for the frontend

Prefix `/api/user-assets/v3/exports`. Every successful response, including download,
carries `X-ADTR-User-ID`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.
All JSON reads reject bodies, duplicate/unknown query keys and noncanonical values.
POST body is UTF-8, <=32768 bytes, exact allowed fields, no duplicate keys/nulls,
unpaired JSON surrogates or unknown values. GET metadata cap: 65536 bytes.

1. POST prefix (create): required domainId, expectedRevision,
   expectedCredentialRevision, observationId, screenColumns, idempotencyKey,
   actorPassword, totpCode. Optional search, sAMAccountName, startTm, endTm, creatTm,
   uacDisableBit (exactly set, clear or not_returned when present).
   All pins/filter fields are strings; screenColumns is a string array.
   Password/proof never enter task payload, fingerprint, logs or storage.
   Response 200: `{taskID, task, request, replayed}`. `taskID` equals task.taskUUID.
   `task` is the existing sanitized public task DTO. `request` is the canonical
   immutable payload with resolved columns and no idempotency key or proof.
2. GET prefix + `/receipt?domainId=...&idempotencyKey=...`: original-operation lookup,
   no resubmission and no proof. 200 `{taskID, task, request}`; 404 not_found means
   no visible receipt, not proof that a lost request never committed. Exact
   tenant/actor/domain/kind/key and current authority/epoch are mandatory.
3. GET prefix + `/detail?taskID=...`: 200 `{taskID, task, request, downloadReady}`;
   always also `downloadStatus` (see below); only when ready also
   `artifact:{rowCount,byteCount,sha256,filename}`. No arbitrary
   storage path or external URL. Clients derive the fixed download route themselves.
4. POST prefix + `/cancel`: exactly taskID, actorPassword, totpCode. 200
   `{taskID, task}`. queued/retry_wait -> cancelled; running -> cancel_requested,
   then Worker confirms cancelled. Cancellation cannot be reported complete until
   the persisted task says cancelled. Repeated cancel is idempotent; a finished
   successful task returns 409 task_terminal. Read detail to resolve lost responses.
5. GET prefix + `/download?taskID=...`: complete XLSX after all validation. Fixed
   filename `user-assets-v3-<taskID>.xlsx`; MIME spreadsheetml.sheet;
   Content-Length, X-ADTR-Artifact-SHA256 and X-ADTR-Row-Count match detail.

Detail `downloadStatus` is exactly one of `pending`, `ready`, `failed`, `cancelled`,
`source_changed`, `source_unavailable`, `artifact_invalid`. queued/running/retry_wait/
cancel_requested map to pending; failed/partial_failed/dead_letter map to failed;
cancelled maps to cancelled. For succeeded, source revision conflict maps to
source_changed, missing/ineligible observation to source_unavailable, and malformed
manifest/result to artifact_invalid, all with downloadReady=false and no artifact.
Database/schema errors remain503. Valid succeeded artifacts use ready/true. Download
itself returns409 selection_changed/directory_observation_unavailable/export_not_ready
or500 artifact_invalid as applicable; no bytes are returned on failure.
Receipt returns the same public task and request for queued, cancelled, failed or
succeeded operations. It does not erase an original receipt because the source changed.
Same-key POST replay also returns the original task/request after live authority and
proof, before new-source validation. Cancel source pins are not rechecked: cancelling
an obsolete task remains safe. Generic task result/cursor must be sanitized for this
new kind too. This slice does not expand F49 archive kinds; anomalously archived task
rows are inaccessible (404).

Canonical request shape without the UAC filter remains
`{domainId,expectedRevision,expectedCredentialRevision,
observationId,search,sAMAccountName,startTm,endTm,creatTm,screenColumns}`.
When present, `uacDisableBit` is appended after screenColumns. Only the original
optional filter strings canonicalize to empty strings; the new key must be omitted
or contain a valid nonempty enum. Allowed columns, preserving caller order:
`guid`, `sAMAccountName`, `sid`, `mail`, `dn`, `whenCreated`. Empty array chooses all
six in that order; absent/null/duplicates/unknown columns are invalid. Headings:
`GUID`, `SAM`, `SID`, `mail`, `DN`, `whenCreated`. Unavailable scalar values become
empty cells; no status/display name/application/login time is invented. Missingness
is documented, not replaced by guessed business values.

Original-operation recovery may persist only actorID, domainId, original idempotency
key and fixed version/kind in sessionStorage, with strict type/size validation and
expiry/mismatch cleanup. Never persist filter/request/username/proof/token. A different
actor cannot restore an old operation. Receipt reconstructs the canonical request;
404/network ambiguity must not automatically resubmit or generate a fresh key.

Client binds requests/results to the captured actor/domain/revisions/observation/
filters. Preserve the original idempotency key and canonical request after ambiguous
submission, use receipt, and never silently create a new key. Refresh/source/version/
account change invalidates old pending response application. Proof is cleared after
submission and never saved. Export UI must explicitly say all matching users and
show fixed observation, selected columns, persistent progress and pending/failed/
cancelled/ready states. Download requires current session validation, bounded body,
exact actor/kind/task metadata, byte length and SHA256 validation before a Blob URL.
Revoke object URLs immediately after use and discard data after authority loss.

## UAC filter binding and compatibility (2026-10-25)

Optional `uacDisableBit=set|clear|not_returned` selects the saved ACCOUNTDISABLE
mask `0x00000002`, with exactly the [v3 list semantics](user-assets-v3-contract.md#observed-uac-disabled-bit-filter-2026-10-25).
It is needed only to request this filter; omission preserves unrestricted exports.
Removing it broadens a new request, never changes a previously submitted task.
`not_returned` means a genuinely absent UAC, not corrupt or invalid input. No
activity, lockout or login conclusion follows, and the six export columns remain
unchanged. Full source validation precedes filtering, so excluded-object corruption
still fails; zero matches produce a header-only workbook.

Explicit null, empty, duplicate, wrong-type or unknown values are invalid400, not
normalized to omission. Create, payload validation and receipt/detail parsing retain
closed fields: canonical request is exactly the original ten keys, or those ten
plus this valid key. TypeScript also rejects an own-property undefined/null/empty;
request constructors conditionally omit the field rather than insert undefined.
Legacy `userStatus` remains unsupported422. No v2 or asset-detail filter is added.

Request/receipt JSON and client cancellation copies retain the original ten-key
order and bytes when the new key is omitted. Separately, the task engine's existing
`canonicalObject` sorts keys before hashing its version/parent-prefixed payload;
that serialization is not the client marker's fixed-order request JSON. Omission
preserves both identities: task payload hash and cancellation-marker request digest.
When present, the value participates in both digests and immutable request comparison;
same actor/key with a different filter conflicts instead of reusing or creating
another task. Task kind, payload version, limits, retry policy, schema, permissions
and storage remain unchanged.

List-to-export capture, expected receipt/detail binding, both client-side
cancellation-request copies and cancellation reauthorization carry the filter.
The dedicated cancel POST body remains taskID plus proof, not new filter fields.
A restored task displays
its frozen request, including its saved filter, rather than the current list draft.
Clearing/changing list filters does not rewrite that task or its recovery locator;
only an explicit new export captures new applied filters. The existing marker
format stores the bound request digest, not new filter plaintext. Historical
receipts and markers without the key retain their original identity.

New API/Worker code accepts old omitted-key payloads; an old Worker rejects a new
payload carrying this field. Future rollout must coordinate API/Worker versions
before accepting filtered exports; mixed-version support is not established here.
This change introduces no deployment action or permission/schema change.

New-filter runtime checks and independent review are separate from the historical
evidence below. The unchanged TLS LDAP browser fixture has only UAC0/512 users,
not nonempty set/null user cases. Synthetic unit or PostgreSQL producer coverage
cannot establish that missing LDAP/browser chain or real AD acceptance. Full
EXP-006/AD-F-053 and product acceptance0/209 remain open.

## Idempotency, execution and lifecycle

Reuse tasks' atomic submit/event/outbox, canonical payload digest, durable lease,
owner isolation, authorization-version binding and result-version fencing. Existing
unique scope is tenant/domain/kind/key; another actor's identical key fails conflict,
without returning their task. Same actor/key/same payload returns original task;
different payload or epoch conflicts. Generic submit/recover/cancel cannot bypass
the dedicated source/proof checks. Recovery means a deliberate new submission after
the user resolves failure; no generic reanimation of an old artifact.

Worker policy: replay-safe, maxAttempts=3 (first + at most two retries), 30-second
lease, 5-second heartbeat, 5-minute task timeout; retries only classified transient
database errors, exponential 5s then 10s (cap60s). Required positive bounds avoid
runaway retention/work; no retries for authorization, stale source, corrupt input,
XML-invalid text, row/byte bounds or deterministic generation failures.

Capture immutable matching rows and the fully verified observation body SHA256
under one fenced transaction. Persist the digest in snapshot provenance and require
that exact digest on every later source check, preventing same-ID replacement.
Then write a
text-only XLSX through a capped chunk sink. Source decode is bounded but is not an
end-to-end streaming reader. Store chunks <=1MiB each by task + fencing token +
ordinal, with per-chunk SHA256. Final manifest stores exact row count, bytes, chunk
count and whole-file SHA256 only after ZIP close succeeds. Progress checkpoints are
durable and monotonic (capture10, generation10..90, staged95, committed success100).
A new optional Kind.ValidateSuccess read-only gate runs in Engine.Finish after
cancel/revocation precedence and with the locked Task and canonical proposed result.
It is restricted to owner-scoped, replay-safe, cancel-discards-result, unschedulable
kinds; existing nil-gate kinds keep existing behavior. It cannot commit/rollback or
perform external actions. It revalidates exact source/digest and complete manifest.
A safe nonempty code changes the outcome to nonretryable failed with empty result;
SQL/timeout errors roll back rather than impersonating stale source. After the gate,
Worker authority/time eligibility and final fenced transition are rechecked in the
same transaction. Test rotation between staged95 and Finish: never succeeded.
Only Engine Finish may make the artifact visible; a staged manifest alone is not
downloadable. Cancellation wins via CancelDiscardsResult, never a partial workbook.

Selected rows remain in bounded worker memory; the immutable source observation is
the durable row snapshot, and only source digest/count/provenance are duplicated.
Generated data is module-owned temporary material, separate from immutable source,
task receipts and audit events. Before another attempt, discard obsolete attempts'
chunks/manifests under current fencing. A task-state trigger removes generated
snapshot metadata/chunks/manifests on cancelled/failed/dead_letter, including queued cancellation,
revocation and lease recovery without a live executor; receipts/events remain.
Success retains exactly its winning artifact with no time-based deletion in this
slice; new archive support is outside scope. This avoids inventing a retention period.
Cancelled task cleanup must be demonstrated in PostgreSQL, not inferred from UI.

## Output safety and bounds

Independent `internal/userassetxlsx` writer with fixed `UserAssets` sheet and six
maximum columns. Do not weaken/refactor Audit's fixed Audit sheet/eight-column/XML
rejection contract. All cells are inline strings, so `=`, `+`, `-`, `@`, tabs followed
by formulas, URLs, leading zeros and timestamps remain literal text. No formula,
hyperlink, macro, external relationship or numeric/time conversion. Preserve XML
metacharacters, CR/tab/LF and literal `_xNNNN_` through OOXML-safe encoding.
XML text validation applies only to selected generated cells; a valid observation
may contain NUL in an unselected field without making the observation corrupt.
XML1.0-illegal characters or invalid UTF-8 in selected cells fail the export with safe
`export_invalid_text`; never strip/replace/truncate data or echo cell contents.

Bounds: v3 observation <=10000 total objects and <=16MiB encoded source; export
<=10000 data rows, six columns, <=32767 UTF16 units per cell, <=32MiB XLSX.
Bounds are guardrails, not capacity acceptance. Overflow fails, never truncates.
The writer's memory is row/buffer-bounded, while source decoding plus selected-row
projection and complete prevalidated download are explicitly bounded allocations.
Download verifies succeeded task, owner/epoch/current scope/current source, exact
manifest-result agreement, contiguous chunks, chunk hashes, aggregate length/hash
before success headers/bytes. Incomplete/corrupt storage never returns a partial200.

The HTTP boundary maps domain invalid_input422 to invalid_input400; unsupported
filters/bounds retain422. Errors retain invalid_input(400), forbidden(403), not_found(404),
selection_changed/directory_observation_unavailable/idempotency_conflict/
authorization_epoch_changed/task_terminal/export_not_ready(409), bounds(422),
schema/dependency_unavailable(503). Safe terminal task codes distinguish
authorization_revoked, export_invalid_text, export_cell_too_long,
artifact_too_large, directory_observation_unavailable and export_timeout.

## Implementation files and verification boundaries

Backend implementation: internal/domains/user_asset_export_v3*.go and shared
user_assets_v3.go predicate/order extraction; new internal/userassetxlsx;
internal/auth/user_asset_export_v3_http*.go, permission schema/catalog/check,
task_authorization.go, task_public.go sanitization and dedicated route guard; store migration21 and schema gate;
cmd/adtr wiring; matching unit/HTTP/real-PostgreSQL tests and this contract.
Frontend implementation: new user-asset-export-v3 API/component/tests, v3 workspace integration,
browser suite/fixtures/evidence. Coordinate before editing shared CI/config files.

Tests must cover full-vs-current-page equality, all filters/order, empty observation,
unavailable/stale pins, excluded-object corruption, nullable selected scalars,
formula/XML/Unicode fidelity, row/byte bounds, duplicate concurrent submissions,
ambiguous receipt recovery, tenant/actor/resource/epoch isolation, revoked grants,
crash/retry/fencing, all cancellation transitions/cleanup, manifest/chunk corruption,
current-permission download and exact head synthetic LDAP->Worker->DB->API->browser.
No local Node/Go full-chain execution or local PostgreSQL/browser retry is authorized
in this work; static and pure Python checks are distinct from required GitHub CI.

## Dedicated CI evidence gate

The independent user-asset-export-v3 job is job60. It requires dictionary3 plus
ADTR_E2E_USER_ASSET_EXPORT_V3=true, rejects v2/v4/group/computer/empty/slow mixing,
and explicitly uses stable-body. The original59 jobs and8/2/2 concurrency/rate
budgets are unchanged. The existing user-v3 budgets remain: spec900s, runner930s,
job20minutes. Synthetic bootstrap sensitive-ledger count7->9, producer9->9,
exactly two real exports and one source observation/use are asserted. Independently
capture clean commit/tree/run/sourceSHA provenance before browser execution;
validate it afterward. Success artifacts are only validated JSON, two XLSX, two
PNG and provenance; failure retains only existing sanitized diagnostics. These
are synthetic evidence gates, not real AD or production acceptance. Local reconciliation is static/Python only. The completed GitHub CI runs below
provide compilation, PostgreSQL and normal/empty browser evidence for PR159;
they do not verify later changes. PR160 terminal-path evidence is recorded separately below.


## Recorded implementation evidence (2026-10-14)

[PR159](https://github.com/yunpiao/adtr/pull/159) is merged at
`370e405b50a23a85a63126c562bb1790dd4eca87`.
Its final (third) head is `9a8d5b10b1c68b7355604d7eb85274677479421d`.
[PR CI 38483191968](https://github.com/yunpiao/adtr/actions/runs/38483191968)
passed 60/60 jobs on attempt 1; the actual PR checkout commit was
`6aeea9ed7bb0bf03f1faac8f976bd31e03ce283b`, distinct from the head.
[Post-merge CI 38487379365](https://github.com/yunpiao/adtr/actions/runs/38487379365)
passed 60/60 jobs on attempt 1 at the merge commit. Both actual checkouts have tree
`7c768d2649ef741d9ec371e69f49c8f6e3a211c9`.
These runs are evidence only for those exact revisions, not for this update or
any later recovery/cancellation implementation.

Each run independently produced and verified two real XLSX downloads and two
1440x900 PNG ready-state screenshots through synthetic TLS LDAP, the real Worker,
PostgreSQL, HTTP API and browser. The normal case contains all 12 matching users
while the first list page contains 10; the zero-match case contains only headings.
Each artifact set binds observation bytes, task/receipt, database chunks,
download headers/length/hash, workbook values and source/checkout/run provenance.
This is synthetic-source end-to-end evidence, not a real AD/Windows experiment.

- PR-run artifact: `user-asset-export-v3-38483191968-1`, ID 11858158563,
  archive SHA256 `f6ccbfb8e2cf8a7fe57fcd4b89e281673e42bf79cb2130eabed4d745e600dc44`
- Main-run artifact: `user-asset-export-v3-38487379365-1`, ID 11860810594,
  archive SHA256 `fb35f43e5cfbf9e232f6ef6e58ec10b153575c04f059ade504abccb28ae87c29`
- In each run the 12-row workbook is 2751 bytes, SHA256
  `66dc1925de87c16e2bddda8cee2145b0250834d639c97117a3d1cb9145d1ec73`;
  the zero-row workbook is 2092 bytes, SHA256
  `e70eedde7b671cc00f6b749da5dcb8bda486cc9777e5438b49ce980d41b2bf24`
- PR PNG SHA256: allMatched
  `82f0b6e489e0b8cfdf9581bbdc76d4d36b17215f3304f839544debf44dc9022b`, empty
  `bb402582a42886921ce6a7df99ba5b6dc54e6d3ca56b07419f835ab2020eaa55`
- Main PNG SHA256: allMatched
  `3fa0cd822cfc9a449a79671cbc538668a0aea3f48d4ae1fac7e6a9b6c4d557f4`, empty
  `de1310f3311dab4dfb1db475dba18aef3e12f06268098ebe95a049a8fe630fbe`

## Recorded failure/recovery and queued-cancel evidence (PR160)

[PR160](https://github.com/yunpiao/adtr/pull/160) merged at
`9f91f7121a00b02101f53c545e3da53da1ec27ed`; final head
`53ba4154bfc8708e79bfd31dac506c9b057f3178`, tree
`6625e1166634a9627a07f28a5e6007007aaacc94`.
[Exact-head CI38497165578](https://github.com/yunpiao/adtr/actions/runs/38497165578)
and separate [main-push CI38501083953](https://github.com/yunpiao/adtr/actions/runs/38501083953)
each passed62/62 jobs on attempt1. The PR run actually checked out merge ref
`13f579fee59ffd595b91b5ce34a8d9c4345894e4`, distinct from its head; both
actual checkouts have the recorded tree. The actual browser/Worker/PostgreSQL chain
verified export_invalid_text, durable original failed receipt and zero generated
failed-task snapshot/manifest/chunk rows. Explicit new safe SAM/GUID submission
produced all12 matched rows and a genuine two-column XLSX. Complete failed/ready
screenshots, workbook cells/order/download metadata and source provenance were
independently reviewed, not inferred from HTTP or DOM tests.

Queued-cancel evidence bound the exact owned Worker to its actual database identity,
verified all threads stopped, zero connections and zero active transactions,
then resumed that same Worker to readiness200. Two strictly later actual queue
successes left the original task cancelled, attempt0, with no execution/artifact.
PR artifacts11864880540(failure)/11864223677(cancel) and fresh main artifacts
11866141622(failure)/11866660781(cancel) each passed independent allowlist,
865-source Git-blob/provenance and actual three-PNG pixel review. Failure sets have
exactly5 files; cancellation sets3. Main ZIP SHA256 values are respectively
`924c1baa557adc7e534030a2b1bb68e833200b80752f73b1be0b1cd0fae5e97d` and
`f5ce285fae5de239573f5ccabc41936cbf9a1103ba12559f720bd2f06cc9114b`.

Historical [run38493130354](https://github.com/yunpiao/adtr/actions/runs/38493130354)
at initial head `8f5c8d091d32e3fc957905ef607fdf6d59647e2e` remains failed:
60 successful jobs, queued-cancel failure and aggregate verify failure. The old
pause check did not meet its connections>0 quiescence condition within10seconds;
PG counts were not captured, so the actual count is unknown. Its failure-recovery
API/PG/XLSX passed, but its failed-state screenshot cropped the terminal message.
The final repair accepts a legitimately zero-connection quiescent owned Worker
only after actual identity/database/all-thread checks and captures full terminal
viewports; it does not retrospectively convert old failed evidence into success.
Running/cancel_requested browser scenarios remain unverified by these artifacts.
These results apply only to the recorded revisions, not subsequent group exports.

## Historical candidate: running cancellation after capture (2026-10-14, then not runtime verified)

The new test-only `user-asset-export-v3-running-cancel` slice starts from merged
PR162 main `94b098d6acf5187e1ecf9aa659f5abed00122ce7`, tree
`0c669bd750c2ec137311aad08b6a05744ddff907`. It preserves the prior 68 CI jobs
and adds exactly one (69 expected). Exact-delta contracts restore the previous
runner/workflow bytes before checking historical digests and counts. No production
code, schema, lease, heartbeat, timeout or export limit changes are authorized.
The candidate has no exact-head runtime pass or independently accepted artifacts yet.
Static and explicitly allowlisted pure-Python checks must not start subprocesses or
use the network; local Go, Node, PostgreSQL, browser and build execution is forbidden.

The target is a real `user_assets.v3.export` execution with its capture snapshot and
progress10 already committed. `running` means the owned Worker has acquired and
started that attempt; `cancel_requested` means the dedicated API has durably requested
cancellation of the running task, with Worker confirmation still pending; `cancelled`
means the Worker/engine has completed the terminal transition. The test must observe
all three states and their durable task/event/receipt evidence in that order. A queued
attempt0 cancellation does not establish this boundary.

The synthetic harness binds the exact owned Worker and its database identity, then
uses a temporary activity-row lock to hold execution after capture. The lock is a
test coordination device, required to expose this normally short-lived checkpoint;
without it this small export can finish before the cancellation request. It delays
the test Worker only, must check all PostgreSQL lock relationships before pausing,
and must release its transaction on success or failure. It must not hold task-row
locks that prevent the API from durably requesting cancellation. The intended pause
requires more than20seconds of live lease and CONT of that same Worker within10seconds;
these bounds preserve lease headroom rather than exercising lease expiry. Removing
them could turn this test into lease recovery and invalidate the claimed execution
identity. Failure of a guard must fail evidence collection, not silently relax it.
Fresh MFA is prepared before the pause so proof preparation does not consume this
bounded window; credentials and proof must never enter retained evidence.

Required evidence includes the running snapshot/progress10 checkpoint, durable
cancel_requested and cancelled transitions, original-operation receipt identity,
no available download, and zero generated snapshot/manifest/chunk rows after
cancellation. The same Worker must resume and demonstrate subsequent real activity;
cleanup must resume it and release test locks even when assertions fail. The success
artifact allowlist is exactly provenance JSON, evidence JSON, running-checkpoint.png,
cancel-requested.png and cancelled-terminal.png. No XLSX, trace, raw logs or proof
inputs are retained. Provenance must bind the actual checkout/tree/run/source bytes;
PNG and database evidence must be independently inspected after CI runs.

This candidate covers cancellation after capture, before chunk-generation evidence.
It does not establish chunk-stage cancellation, concurrent fencing races, or all
cancellation boundaries. Group/computer browser cancellation remains queued-only.
PR162's recorded68/68 results verify its computer slice, not this candidate; PR159/160
history above remains unchanged. AD-F-053 remains in_progress, product_accepted=false;
Issue30/151 and product acceptance0/209 remain open pending their full gates.

AD-F-053 therefore remains `in_progress`, `product_accepted=false`.
EXP-006 legacy business/default columns and full filter semantics, EXP-007 computer,
EXP-008 group and EXP-009 DNS/IP exports are not delivered by this bounded user slice.
Separate bounded group/computer implementations are merged in PR161/162; their
full legacy compatibility and product acceptance remain open.
Real AD, eight Windows versions, target capacity/peak memory and production gates
remain open. [Issue30](https://github.com/yunpiao/adtr/issues/30) and
[Issue151](https://github.com/yunpiao/adtr/issues/151) stay open; product acceptance
remains **0/209**.


## PR163 snapshot-stage verification closure (2026-10-14)

The historical candidate above was merged with head
`4d5bdcc4e29351ee49cc3f3d2d2ee8ad8e29462f`, main
`ec0da2018cc5914b8d0ca345c1c2c4cd5078d177`, and shared tree
`ec801e6ffc0bce09cd89714bb1be3586d2781fad`. Exact-head CI38579256706
attempt2 and separate main CI38588506730 attempt1 each passed69/69.
PR artifact11902942969 and fresh main artifact11907692240 were independently
validated, including actual PNGs and repository/source identities. Historical
failed attempts and full artifact hashes remain in [delivery](delivery.md).
These results cover user cancellation after snapshot capture only. New group/computer
candidates require their own71-job exact-head CI and artifact review; chunk-stage
cancellation, fencing races and product acceptance remain open.

## AD-F-152 / AD-F-151：从后台任务只读查看原用户导出

仅精确 `user_assets.v3.export` 的 TaskDetails 增加“查看原导出结果”。专用入口
单独检查 domains/directory_assets/user_asset_exports/tasks readable 与两个既有
GET detail/download路径；不需要创建、取消或receipt权限。专用未获准只禁该入口，
不会缩减原通用任务查看。权限检查复用 `/api/access/check` 的只读POST协议；
导出业务请求只发GET，不提交创建/取消/恢复写入，也不生成幂等键。

首次 `detailByTask(taskID, domainId, actor, signal)` 对原taskID调用现有detail：
参数ID/domain用于绑定通用任务，actor核对响应身份头，signal中止过期请求，缺少
这些绑定不得接受结果。原传输层继续核对身份/no-store/nosniff、大小与严格JSON；
原parser核验taskID、精确kind、domain、canonical request、状态及文件元数据。
后端继续按当前会话/actor/tenant、域权限、授权版本及来源规则检查；前端没有
新增或伪造epoch响应字段。取到原request后固定于该任务面板，手动刷新/重开只用
相同request的detail读取；不能用最新revision/observation替换，source_changed
只显示原来源失效并禁止下载。

面板展示原请求（含筛选/排序/列顺序）、原域/版本/观测、任务/下载状态及文件
元数据。failed/cancelled/source_changed等非ready均不允许下载。下载逐字复用
原API的前置detail、完整字节上限/长度/SHA256校验、后置detail与元数据复核；
最终仍属于当前task/domain/account/session代次时才创建浏览器Blob下载。

关闭/停止读取、身份/会话/任务/域变更、通用详情刷新或读取失败会卸载或清空
私有结果并中止旧请求，迟到metadata/bytes不能回填或触发下载。专用403/epoch
失效清空专用结果，401交会话复核；没有自动重试/轮询新面板。通用任务已有
状态轮询保持。原sessionStorage导出恢复记录和unknown intent完全不读写；
创建和未知创建仍由资产页原面板处理；本只读结果组件本身不承担取消。下节的
独立专用取消入口不改变这个GET-only契约，computer/group入口说明保持。
删除此入口只失去按原taskID取结果的便利，不影响原导出操作；可见副作用是
任务详情增加权限核对和只读面板，刷新通用详情后需重新打开原结果。

新具名UserAssetExportTaskResult DOM及API契约覆盖专用授权、原来源固定、无业务
POST、状态/metadata拒绝、身份/域切换、撤权epoch、迟到与完整下载；原严格下载、
未知恢复和各资产任务控制测试保留。合成DOM/API不替代真实链路。

既有user-asset-export-v3真实两案例（12行及空结果）保留创建/MFA、Worker、PG、
原下载与XLSX精确验证，再清除持久恢复记录，在无内存intent的新标签从后台任务
打开同一taskID并重下，逐字节等于原已验证XLSX；观察到的导出请求必须仅为
detail/detail/download/detail四GET。两张既有1440×900案例图改为展示新只读
原结果面板，保持原案例响应、文件和严格证据validator；不以同名文件替代来源
及字节检查。窄屏未实测。当前切片的运行/截图结论以精确head CI与独立验图为准；
#26、完整EXP006/AD-F-151/152及产品验收继续开放，不代表真实AD/容量验收。

## AD-F-151 / AD-F-189：无本标签页 locator 的原用户导出取消

[工程 Issue202](https://github.com/yunpiao/adtr/issues/202) 是既有取消能力的可达性与
未知结果保护集成，不增加 producer、任务种类、服务端状态或权限。来源为
[Issue26](https://github.com/yunpiao/adtr/issues/26) 的任务状态与文件工作流及
[Issue12](https://github.com/yunpiao/adtr/issues/12) 的原任务状态；精确取消语义仍是
本文既有专用 `/cancel` 契约。基线 main
`86d7c10234b53f647a279e17c3adda6dc06fb5f0`。本节是实现与验收约束，实际运行、
PNG和清理结论须由最终精确head的CI证据建立；产品验收仍为0/209。

### 入口、原任务绑定与权限

通用任务详情仅对精确 `user_assets.v3.export` 增加独立折叠区“任务原用户导出取消”。
初始不显示proof，不读专用业务详情，不创建locator。用户点“核对原用户导出取消”
后才核验专用读取与取消能力，按原taskID读取并固定完整规范请求。原
UserAssetExportTaskResult仍只有读取/下载GET；通用任务cancel/recover继续禁止此kind。
原任务ID不是任意任务取消授权：服务端专用TaskTx先调用Engine.DetailTx，校验原
actor/tenant、owner-scoped授权epoch、kind、域和非归档；CancelTx后再DetailTx并提交。
仅有readable不能取消，仍需tasks/user_asset_exports writeable、当前域scope、
entitlement、CSRF/Origin及本次密码/未使用TOTP。取消不重新验证来源pins；不得替换
为当前列表的新revision/observation。没有新增grant、角色、调度或凭据授权。

“读取原用户导出当前状态”仅显式GET同一原任务。running不能说已停止，
cancel_requested表示请求已登记但Worker尚未确认，只有真实cancelled可显示
“服务器已确认取消，没有可下载文件”；其它终态竞争如实展示，不断言取消获胜。
GET状态不是取消回执，也不能用queued/running/retry_wait证明早先未知POST不会提交。

### 两个取消入口共享的持久未知屏障

旧资产页取消与新任务入口共用localStorage marker和Web Lock。key/lock name固定为
`adtr.user-export-cancel.v1.<actorID>.<taskID>`，kind固定user_assets.v3.export；不把
domain或digest放进锁名，防止不同内容变成同一任务的另一把锁。marker精确字段为
version、kind、actorID、taskID、domainId、requestSHA256，固定键顺序、类型和大小，
拒绝额外/重复键、非规范JSON或绑定冲突。requestSHA256按本文完整规范请求的固定
字段顺序计算，保留screenColumns顺序；它只用于一致性，不是授权、服务端状态或回执。
不保存原filter/request正文、username、幂等键、proof、CSRF、token或响应。hash不使
筛选条件匿名，只持久化必要的标识符和不透明摘要。

显式提交先清proof输入，再取得exclusive/ifAvailable Web Lock，不排队保留旧proof，
不steal。锁内再次核当前生命周期与marker、重新授权并GET原detail；若已终态或
cancel_requested则0 POST，防另一tab清终态marker后本tab旧running表单再次提交。
无marker且仍允许取消，先写入规范marker并回读完全相同字节，才能调用一次原专用
cancel。持久化/回读失败、缺锁或锁被占均0 POST；不退回内存、sessionStorage或租约。
输入证明只存在瞬时表单/当次请求闭包，不写入共享恢复结果或诊断。所有await后均核
signal/generation；卸载、关闭、来源/任务/身份变化、focus/visibility/storage变化
清私有显示和proof，中止旧请求，迟到响应不能恢复控制或清marker。30秒deadline只
限制UI等待，不代表服务器回滚或停止。

未知POST、超时、断连、畸形响应、撤权或其它401/403/404保留只读恢复屏障，不自动
重发。marker跨重挂载、新标签和重新登录保存；同actor重新进入仍要授权读取原task。
没有自动过期、清空所有记录或“忘掉未知取消”按钮；放弃原创建locator不删除marker。
匹配且当前有权限的专用终态可持锁精确删除并确认缺席；cancel_requested保留至终态。
删除/读回不确定时当前生命周期仍只读，不能声称已remove后marker必然仍存在。
崩溃发生在写marker后、实际发出请求前也可能留下保守屏障；以后GET非终态不能证明
当次没有发出，没有凭空制造的取消回执API可以解锁。

### 精确proof拒绝的安全纠正

只有当次专用cancel的完整、最多4096字节UTF-8/无重复键JSON
`{error:"invalid_credentials"}`，精确HTTP401及application/json、no-store、nosniff，
才是闭集负向证据。后端requireAccessProof在cancel dispatch前返回该错误并rollback，
所以该次请求没有取消效果，但仍可能有速率桶/失败审计效果。错误响应不要求不存在的
actor身份头；若意外出现且不匹配则拒绝。body读取失败、超限、UTF-8/JSON/schema或
metadata错误仍按401失效私有UI，不能以status0绕过失效，也不能清屏障。

API私有brand绑定当次signal、actor、task和完整原请求，不是任意error.code即可仿造。
同一锁callback从dispatch持续到可信拒绝后的auth/me、menu/check、原detail、最终
auth/me，核当前身份字段/CSRF、生命周期、专用读取响应身份头与完整原绑定。成功后
仅可删除本invocation刚写入的精确marker并确认缺席，才提示重新显式输入fresh proof。
不能释放再抢锁凭相同字节删除，避免ABA清掉另一attempt；任何授权/绑定/读回失败只读，
其他未知401不例外。若fresh状态已cancel_requested或terminal，不重新打开取消表单。
没有自动二次POST，也不清除更早未知请求或其他任务的marker。

Web Locks/localStorage是本客户端两入口跨标签保护的能力依赖，不是后端授权或幂等
机制的必要条件。支持这些能力的浏览器保留正常旧取消；不支持/不可用时只禁这两个
取消入口并解释原因，读取、下载、创建及其它业务不受影响。不修改浏览器/系统权限。
保证只覆盖同新版、同origin与存储分区的协作入口，不覆盖仍开着的旧版页、另一设备、
站点数据被手动清除或隐私窗口结束；不承诺服务器exactly-once。

### 有界验证与证据

新增DOM/API契约验证原owner/session/task/request、默认折叠GET-only、严格proof拒绝
纠正、未知跨remount/session/两入口屏障、存储/锁失败0POST、原请求每字段摘要、ABA、
迟到/撤权清空和固定安全错误。不支持锁/存储时其它操作不退化；正常旧取消继续覆盖。
合成DOM状态不替代真实浏览器/Web Locks/Worker证明。

既有queued-cancel真实套件保留TLS fixture采集、原创建、专用Worker暂停、queued
attempt0与零snapshot/manifest/chunk。另开无opener、无sessionStorage locator的同会话
新标签，从真实任务列表进入原详情；先验证默认没有proof/通用取消，再显式进入专用
控制并一次取消。原通用非终态轮询继续运行；真实状态转换可使专用准备失效，测试
等待通用页真实cancelled后再显式GET原详情，不把POST回包当新读取回执。窄native
fetch前置只读观察器只比较完整预期marker bytes，保存布尔
值和计数，不读proof/body，不改原fetch参数、缓存或响应，不route/伪造/重放请求。
旧tab原locator逐字不变；新tab不得创建/receipt/download/通用cancel/recover。

原资产面板继续通过真实receipt/detail恢复，保留取消状态、无下载、原ID与新建按钮
可视断言；owned Worker恢复后两轮队列仍无执行，原数据库及attempt0不变。proof
计数仍由owner7/producer9起始，create+cancel后owner9/producer9/exportTasks1，10上限
不变；900秒测试上限、120秒初始暂停和所有owned-worker deadlines不扩大。
新增等待分支settle/join后才收尾，新tab关闭与owned Worker恢复均有finally保障；失败
只输出固定checkpoint，原始异常、Cookie、proof或cause不进入报告。进程/容器清理仍
由实际runner证据独立证明，不能用取消终态或零artifact推断。

现三文件allowlist与严格证据schema不变：provenance JSON、cancel evidence JSON、
cancelled-terminal.png。PNG在恢复及不可变数据库验证后真实截取新任务取消入口，
保留1440×900、原taskID与确认终态、无proof；这是同一suite截图主体的明确改变，
旧资产面板保留DOM/可视检查，不声称同时保留其新的像素证据。validator继续验证PNG
SHA/头部与当前checkout/run/source，独立review仍须实际看图，不能用PNG头替代像素。
原running-cancel真实套件保留为旧入口/后端回归；新入口running/retry_wait/未知响应
仅DOM/API覆盖，不声称新增running入口真实checkpoint或丢回复artifact。真实AD、
Windows、容量及完整AD-F/EXP验收继续开放。
