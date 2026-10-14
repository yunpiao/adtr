# Group asset XLSX export v3: bounded EXP-008 slice

Contract freeze 2026-10-14; implementation and exact-revision CI pending.
Base main `9f91f7121a00b02101f53c545e3da53da1ec27ed`, tree
`6625e1166634a9627a07f28a5e6007007aaacc94`. Refs
[F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-053 / EXP-008,
[group list contract](group-assets-v3-contract.md), and
[user export lifecycle](user-asset-export-v3-contract.md).
AD-F-053 remains in_progress; Issues30/151 remain open; product acceptance is0/209.

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
