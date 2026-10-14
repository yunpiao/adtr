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
paired startTm/endTm half-open whenCreated range, creatTm asc/desc with null-last and
GUID tie-breaking. Omitted optional predicates broaden the match; omitted creatTm
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
   actorPassword, totpCode. Optional search, sAMAccountName, startTm, endTm, creatTm.
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

Canonical request shape: `{domainId,expectedRevision,expectedCredentialRevision,
observationId,search,sAMAccountName,startTm,endTm,creatTm,screenColumns}`. Optional
strings canonicalize to empty strings. Allowed columns, preserving caller order:
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
