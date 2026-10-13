# User asset XLSX export v3: proposed first slice

Interface/lifecycle freeze, 2026-10-13, following independent contract review.
Implementation and actual-diff review pending.
Base: actual remote main `325f28095b9fb5a60c7e43e698ac2013d78d4a59`, tree
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

## Planned files and verification

Backend owner: internal/domains/user_asset_export_v3*.go and shared
user_assets_v3.go predicate/order extraction; new internal/userassetxlsx;
internal/auth/user_asset_export_v3_http*.go, permission schema/catalog/check,
task_authorization.go, task_public.go sanitization and dedicated route guard; store migration21 and schema gate;
cmd/adtr wiring; matching unit/HTTP/real-PostgreSQL tests and this contract.
Frontend owner: new user-asset-export-v3 API/component/tests, v3 workspace integration,
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
are synthetic evidence gates, not real AD or production acceptance. Local work
is static/Python only; compilation, PostgreSQL and browser execution await GitHub CI.
