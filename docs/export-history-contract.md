# F38 local export history contract

Local implementation contract, 2026-10-07 UTC. Refs Issue #26, AD-F-151/152, FLD-2452–2477 and UI-0540–0550. This describes the existing Audit/XLSX producer; it does not establish source parity or product acceptance.

## Scope and implementation boundary

Add durable discovery of the existing real `audit.export` v1 producer and its protected XLSX downloads. Existing task rows are authoritative; do not generate history records, copy artifacts, register producers, change retries, create a schema migration, or infer readiness from an empty catalogue. Existing protected audit submission events remain unchanged. Do not join those one-to-many events into the history list or use an event count as an export count.

Only Audit/XLSX is available. Alert, Bashline (source spelling), System, Leak, alternative formats, and appType remain unsupported. The history is the current user's eligible persisted tasks, not the whole tenant's exports. A password/role/security epoch change intentionally makes old submissions undiscoverable even after permissions return. This is an authorization boundary, not a retention/deletion feature.

## HTTP surface and permissions

NEW `GET /api/audit/exports/history` returns the DTO below. This avoids changing the existing one-method-per-path `auditRoutes` contract. Add it to the same route registry/access introspection and browser operations list. `/history/` is not an alias. Wrong method returns 405 with Allow: GET; unknown route returns 404.

EXISTING `POST /api/audit/exports`, `GET /api/audit/exports/detail?taskUUID=...`, and `GET /api/audit/exports/download?taskUUID=...` remain the only submit/detail/byte routes. Preserve dedicated submit proof/idempotency and cancel behavior. No history write route, recovery shortcut, file_name/file_type argument, filesystem path, bearer URL, new download authority or format conversion.

History requires a current authenticated session, `audit.readable`, and `audit_exports.readable`. It does not require tasks.readable/writeable or audit_exports.writeable. Download uses those same existing dedicated read permissions. Permissions must agree between `/api/access/check` and actual routing. Generic task APIs remain their existing redacted control surface.

Preserve schema gate -> shared tenant authorization lock -> current actor lock/session/epoch/grants in the HTTP transaction. History uses that authenticated transaction and calls requireSchema before its own query. No per-history-row actor/task locks or calls to DetailTx. GET has no fresh password/TOTP, CSRF, or mutation proof consumption. Preserve no-store and nosniff headers, 30-second HTTP deadline, schema failures as 503, unknown DB failures as 500/internal rather than an empty list, missing session as 401, and missing function permission as 403.

## Exact query contract

All keys are case-sensitive. Parse URL encoding strictly. Reject unknown keys, malformed encoding, invalid UTF-8, empty supplied values, invalid values, and repeated scalars with 400 `invalid_input`. Omitted filters are unrestricted; empty strings are not aliases for omission. Canonical integer spelling only: reject whitespace, plus signs, leading zeros, fractions, and explicit 0.

| Key | Values/default | Semantics |
| --- | --- | --- |
| pageIdx | default 1; 1..1000000 | One-based page; never silently clamp |
| pageSize | default 20; 1..100 or exactly -1 | -1 only with pageIdx=1; see bounded-all below |
| modelType | repeatable array; omitted or exactly one `Audit` | Only real Audit producer; duplicate entries invalid |
| status | repeatable array; 0..9 distinct canonical states | OR within status; omitted means all states |
| startTm | optional RFC3339 with explicit Z/offset | Inclusive task created_at lower bound |
| endTm | optional RFC3339 with explicit Z/offset | Exclusive task created_at upper bound |
| sortTm | default -1; exactly -1 or 1 | created_at and task_id C-collation, both same direction |
| appType | recognized but unsupported | Any supplied appType returns 400 `unsupported_app_type`; never assign an AD=1 meaning |

Canonical status values: `queued`, `running`, `retry_wait`, `cancel_requested`, `succeeded`, `failed`, `partial_failed`, `dead_letter`, `cancelled`. The field name `status` is retained from FLD-2455, but values are the canonical local task states. Source aliases `padding`, `start`, `success`, uppercase task compatibility labels, comma-delimited arrays, JSON arrays in a string, `state` alias and repeated duplicate states are invalid_input. Do not collapse cancelled into failure, cancel_requested into cancelled, or retry_wait into running/success. A valid state may currently have no real rows.

For modelType, syntactically valid nonempty unsupported strings of at most32 Unicode characters (including Alert/Bashline/System/Leak and wrong casing) return 400 `unsupported_model_type`. Limit the array to the five source types' maximum cardinality; duplicates, controls, empty items, malformed/overlong text or >5 entries return invalid_input. Any unsupported member rejects the whole request, including Audit+Alert; do not return Audit results while silently ignoring another requested producer. A single Audit is accepted; omission also means the only supported producer. The UI need not offer a misleading model selector.

Time is an intentional local contract: uppercase RFC3339 date-time with mandatory explicit timezone, seconds, and optional 1..6 fractional digits (PostgreSQL precision); years 0001..9999 before and after UTC normalization. Reject naive legacy `yyyy-MM-dd HH:mm:ss`, leap seconds, invalid offsets/calendar dates and >6 fractional digits rather than letting the database round query boundaries. Normalize valid offsets to UTC RFC3339. If both bounds are supplied, require start < end. No invented maximum window. Filtering is by task creation, not snapshot time, updated time or task terminal time.

All fields combine with AND. SQL sorts `created_at DESC, task_id COLLATE "C" DESC` by default; both ASC for sortTm=1. Different exports with equal timestamps stay distinct and deterministically ordered. Counts, page rows, state/progress, visibility revision and artifact metadata are read by ONE SQL statement; no separate count query with another MVCC snapshot. A subsequent refresh can legitimately change rows/total because states, authorization and exports change; no cross-request frozen cursor is promised.

For pageSize=-1, limit the returned page internally to 1001 rows, count only authorized filtered tasks, and return 422 `result_too_large` if total >1000. Do not return a truncated success. The response echoes pageSize=-1; totalPage=1 for nonempty results, 0 for empty. Standard pages: totalPage=ceil(total/pageSize), 0 if empty. Out-of-range pages return [] and the true authorized total, not a rewritten page. exhausted means offset+returnedCount >= total (true for empty and out-of-range pages).

## Exact history response

```ts
interface ExportHistory {
  page: { pageIdx: number; pageSize: number; total: number; totalPage: number };
  list: ExportHistoryRow[]; // always [], never null
  exhausted: boolean;
}
interface ExportHistoryRow {
  taskUUID: string;
  fileName: string;             // audit.ArtifactFilename(taskUUID)
  modelType: "Audit";
  fileType: "xlsx";
  state: TaskState;              // exact persisted state, not SourceState
  progress: number;              // exact persisted integer 0..100
  error: string;                 // exact persisted safe error_code, "" when absent
  createdAt: string;             // UTC RFC3339
  updatedAt: string;             // UTC RFC3339
  attempt: number;               // exact persisted attempt
  maxAttempts: number;           // existing kind policy, not recomputed
  nextAttemptAt?: string;        // exact persisted value if present, UTC
  downloadReady: boolean;
  downloadStatus: "eligible" | "not_ready" | "snapshot_changed" | "artifact_invalid";
  rowCount?: number;             // only when eligible; 0 is valid
  snapshotAt?: string;           // only when eligible; UTC RFC3339
  downloadPath?: string;         // only when eligible; fixed relative dedicated URL
}
```

No ID/TaskID aliases, task payload/result/cursor JSON, hash, byte/chunk storage paths, actor/tenant IDs, authorization epoch, lease/fencing token, secret, guessed application ID, appType=[] placeholder or arbitrary raw error text in this DTO. `taskUUID` maps both source identifiers to the one real immutable task identity, with the deliberate difference recorded below. The relative download path is `/api/audit/exports/download?taskUUID=<server-task-id>`; frontend should invoke its typed ID-based download client, not blindly follow a supplied URL.

The filename is a server-derived EXPECTED name for every row, even pending/failed exports. It is not proof that a file exists. Match Content-Disposition on actual downloads. Preserve safe error_code values validated by the task engine (`^[a-z][a-z0-9_]{0,63}$` or empty); do not return driver exceptions. If historical storage contains an invalid code, suppress it to `export_failed` for display and keep the real state; do not emit the stored arbitrary string. Never rewrite progress as 100 for a cancelled/failed row or as a guessed percentage.

## Authorization predicate: filter before count and page

Normative parameters `$1=verified tenant`, `$2=verified actor ID`. Role and epoch are read from the current user in the same authenticated transaction. No query parameter can supply any of these.

```sql
SELECT t.*
FROM adtr.tasks t
JOIN adtr.users u
  ON u.tenant_id=t.tenant_id AND u.id=t.actor_id
WHERE t.tenant_id=$1
  AND t.actor_id=$2
  AND t.kind='audit.export'
  AND t.domain_id='platform'
  AND t.authorization_version=u.authorization_version::text
  AND NOT EXISTS (
    SELECT FROM adtr.task_visibility v
    WHERE v.tenant_id=t.tenant_id AND v.task_id=t.task_id AND v.archived
  )
  AND NOT EXISTS (
    SELECT FROM adtr.audit_export_rows r
    WHERE r.task_id=t.task_id
      AND r.domain_id IS NOT NULL
      AND NOT (r.source='task' AND r.domain_id='platform')
      AND NOT EXISTS (
        SELECT FROM adtr.resource_tenant_config c
        JOIN adtr.resource_domains d ON d.tenant_id=c.tenant_id
        JOIN adtr.resource_group_members m
          ON m.tenant_id=d.tenant_id AND m.domain_id=d.id
        JOIN adtr.resource_role_groups g
          ON g.tenant_id=m.tenant_id AND g.group_id=m.group_id
        WHERE c.tenant_id=$1
          AND g.role_id=COALESCE(NULLIF(u.role_id,''),u.role)
          AND d.id=r.domain_id AND d.active
          AND c.max_ad_count>0
          AND c.expire_time>extract(epoch FROM clock_timestamp())
          AND (SELECT count(*) FROM adtr.resource_domains counted
               WHERE counted.tenant_id=c.tenant_id AND counted.active)<=c.max_ad_count
      )
  )
  -- append validated state and half-open created_at filters
```

This is the existing authorizeSnapshotScope rule, expressed as a set predicate. Do not relax the exact role/group/domain joins for admins. Do not broaden the exception to every `domain_id='platform'` row: only source=task/platform is exempt. Every domain-bearing snapshot source matters, including domain/operation-account/account-reference/credential-use sources, not only task rows. Use the same shared predicate builder or regression assertions to prevent list/detail drift.

A task with no snapshot or zero snapshot rows has no captured AD domains to deny. Such queued, running-before-capture, zero-row, failed-before-snapshot or platform-only exports remain visible even when the tenant's AD entitlement expires, subject to epoch/function/session/owner checks. Never invent planned snapshot domains or add a blanket active-tenant predicate. Conversely, one now-denied domain hides the whole export and its count, regardless of state. Natural expiry without an epoch mutation must remove domain-bearing rows. Revoke/regrant cannot revive an old epoch.

Use `WITH selected AS MATERIALIZED (<the full predicate plus filters>), page AS MATERIALIZED (SELECT ... FROM selected ORDER BY ... LIMIT ... OFFSET ...)`, then select count(selected) and a stable ordered JSON aggregate of the bounded page. Obtain snapshot/manifest metadata by LEFT JOINs inside that same statement after paging, using one current tenant visibility-revision scalar. Never inner-join snapshots: that would erase queued and failed-before-snapshot tasks. The correlated anti-join is a database set operation, not N+1 application calls or locks.

Bound even corrupted private metadata at the SQL projection: return raw result only with `CASE WHEN octet_length(p.result::text)<=4096 THEN p.result ELSE NULL END`, plus an internal oversized-result flag. Oversized result is artifact_invalid for succeeded rows; do not transfer an arbitrary 64KiB (or externally corrupted larger) object per row. Typed decoding must require exactly artifactToken, rowCount, snapshotAt, sha256 and byteCount, reject absent/extra fields or wrong types, and validate lowercase 64-hex SHA256.

For the manifest join, compare stored token text safely: `m.task_id=p.task_id AND m.fencing_token::text=p.result->>'artifactToken'`. Do not cast untrusted result JSON text to bigint/numeric/timestamp in SQL; malformed JSON types/strings must become row-level artifact_invalid, not abort the entire history request. Pass raw result and nullable internal snapshot/manifest metadata ONLY into Go-side projection. No joins to chunk bytea are needed for history. This exact-token join has at most one matching row because the manifest PK is (task_id,fencing_token); no unbounded manifest JSON aggregation is necessary. If the implementation instead fetches manifest candidates for Go-side token matching, use a lateral ordered LIMIT4: audit maxAttempts is3, so a fourth candidate marks artifact_invalid rather than returning arbitrary corrupt manifest sets. A result object with numbers encoded as strings must fail Go's typed result decode.

Existing tasks indexes, snapshot PK, manifest PK and export-row task_id prefix are sufficient for a bounded functional slice. Migration9 replaced the original task-only audit_export_domains index with (task_id,domain_id) WHERE domain_id IS NOT NULL, covering later domain-bearing sources. No throughput claim is made; an EXPLAIN/capacity follow-up can justify a migration separately. No new retention, cached authorization or per-row database lock.

## Exact archive policy

Active-only, with no visibility query parameter. Existing F49 permits archiving/restoring only infrastructure.health, so normal audit exports are never archived. Nevertheless, if an unexpected archived export row exists, history excludes it before total/page and dedicated DetailTx returns 404/not_found; ReadArtifactTx inherits that denial. Add the same NOT EXISTS archived condition to the existing detail lookup. This is an explicit defensive local policy approved by the parent, not accidental reuse of generic task-list defaults or source parity. `task_archive` permissions do not reveal exports. Do not add archive/restore buttons or enable audit.export eligibility in F49.

## Artifact projection and actual byte authority

Projection order for an authorized history row:

1. If task.state != succeeded: downloadReady=false, downloadStatus=not_ready. Preserve exact state/progress/error/attempt values. Omit rowCount, snapshotAt and downloadPath even if an old staged manifest exists. In particular, complete chunks staged before a cancel/Finish race never imply published output.
2. For succeeded: typed-decode exportResult. Invalid JSON value types, nonpositive artifactToken, or absent snapshot -> artifact_invalid. Snapshot must belong to this task/tenant/actor and its stored epoch must equal task epoch.
3. If the genuine snapshot visibility_revision differs from the current tenant revision (missing current row means zero), use snapshot_changed, with no download fields. Do not erase/change succeeded task state or leak stale rowCount after a hidden/restored revision.
4. Check result token identifies the joined manifest, result SHA256 is exactly 64 lowercase hexadecimal characters, result SHA256/byteCount agree with it, manifest rowCount=snapshot rowCount=result rowCount, capturedAt=result snapshotAt, rowCount 0..100000, byteCount 1..134217728, and a positive manifest chunkCount. Decode failure or mismatch -> artifact_invalid. Metadata fields are internal; expose no raw JSON in an error.
5. Only this matching committed succeeded metadata produces eligible/true plus rowCount, snapshotAt and the dedicated path. Share the pure metadata validation with DetailTx where practical so list and detail have one acceptance definition; preserve detail's existing 409 export_snapshot_changed / 500 artifact_invalid responses. Add any defensive validation to both, not just list.

`eligible` is metadata-based eligibility at the list read. It DOES NOT claim that all chunk bytes have already been loaded and hashed or that a future download is guaranteed. A corrupt/missing chunk can remain metadata-eligible until the download validates actual bytes. UI may enable the action and explain that permissions/file integrity are rechecked on download; do not label history as already byte-verified. Do not download/hash up to 128MiB per history row to compute readiness.

Existing download remains authoritative: recheck current session/grants/owner/epoch/active visibility/all captured domains/current clock/revision; require succeeded and exact immutable artifact metadata; load bounded chunks in order; check sequence, per-chunk digest, manifest count/size and final SHA256 BEFORE sending response bytes. Do not catch those errors into an empty success, reuse stale blobs across users, redirect to a bearer URL, or expose storage paths. Retain XLSX MIME, server filename, no-store, nosniff. The real byte pipeline remains auditxlsx and immutable database chunks.

Do not alter existing not-found/authorization error behavior except the explicit archived 404. Another tenant/owner/missing ID -> 404; existing dedicated epoch mismatch ->403 authorization_revoked; live snapshot-scope denial currently maps to403 forbidden. Non-succeeded download ->409 export_not_ready. Readiness list errors are per-row states, while actual detail/download keep their existing HTTP failures. Database failures never become artifact_invalid.

## Frontend contract and navigation

Use the history endpoint automatically on reopening the audit page; manual UUID entry can remain secondary, but must no longer be the only discovery route. Show created UTC, expected filename, Audit/XLSX, exact task state and persisted progress, safe error when present, download-status explanation, and open-detail action. Preserve current detail polling and protected ID-based download. Do not fetch per-row details to build the table.

Separate history filters from audit-record export filters. Labels explicitly say creation time and UTC. The new datetime-local controls intentionally represent UTC wall-clock input, matching existing audit UI; append Z only under that visible UTC label, validate and send canonical UTC. Never interpret unlabeled local time as UTC or silently impose the browser's timezone. Use page size choices 10,20,30,40,50 (source UI); API supports 1..100 and bounded -1, but UI need not expose -1. Apply/clear filters reset page to1. All nine canonical state choices have distinct labels, including retry waiting, cancellation requested and cancelled.

Preserve account-bound read generation/abort protection, stale poll rejection, StrictMode setup/cleanup, navigation Back/Forward/reload and current detail closing. On user/session change, clear old rows/detail/blob/selection immediately. A completed stale request must not refill another user's view. Refresh after a known export submission; lost submission response retains the original idempotency intent/key and uses existing resolution logic, never a new key just because the history read is empty. Count/page can change on refresh; no silent truncation or fabricated empty success on fetch error. Preserve detail URL navigation; history filters may stay component state unless existing routing architecture already persists them.

No format selector implying unavailable conversions, no unsupported appType filter, no fake Alert/Bashline/System/Leak rows. State/format controls missing real backend support remain explicitly out of scope rather than source parity achieved.

## Source-to-local field/control ledger

| Source | Local mapping / deliberate difference |
| --- | --- |
| FLD-2452/2453/2460–2464 | pageIdx/pageSize/page output preserved; defaults, bounds and 1000 all-limit are local frozen policy |
| FLD-2454 | modelType only Audit available; other source producers explicitly rejected |
| FLD-2455/2471 | request status array -> response state with all nine local states; padding/start/success aliases rejected; cancellation/retry not collapsed |
| FLD-2456/2457/2458/2472 | startTm/endTm/sortTm; createdAt UTC RFC3339, half-open bounds, stable tie-break; legacy timezone/format not claimed |
| FLD-2459/2475 | appType unsupported and rejected/omitted; definition-only source evidence does not justify enum or [] value |
| FLD-2465 | lowercase list, always an array |
| FLD-2466/2467 | taskUUID is one persisted task identity; no fabricated separate history record ID |
| FLD-2468 | fileName from ArtifactFilename; source field is definition-only; expected name while pending |
| FLD-2469/2470 | exact Audit and xlsx from real producer; no conversion |
| FLD-2473 | filePath replaced by relative authenticated downloadPath only when eligible; no server path/bearer reference |
| FLD-2474 | error is a bounded safe persisted code; no unfiltered errMsg/driver data |
| FLD-2476/2477 | file_name/file_type definition-only inputs rejected; dedicated taskUUID download retained |
| UI-0540/0548 | intentional UTC inputs/display and real creation time; source naive date format not reproduced |
| UI-0541 | presentation row index optional; never identity |
| UI-0542–0544 | expected filename, Audit, XLSX |
| UI-0545/0546 | no format selector until real alternate producers/conversion exists |
| UI-0547 | nine canonical statuses + actual progress; no four-label lossy mapping |
| UI-0549 | actual dedicated readable permission gates read/download, existing submit/cancel keep write/proof rules; legacy authInfo.writeable visibility alone is not authority |
| UI-0550 | source 10/20/30/40/50 choices supported; local API bounds explicit |

Workbook evidence was read directly from grouped-plan F38 and workbook-extracted business fields/UI/options. Important definition-only rows: FLD-2459,2463,2464,2468,2475,2476,2477. Source code labels are leads, not complete handlers/runtime evidence. Source legacy status labels are 待导出/padding, 导出中/start, 导出成功/success, 导出失败/failed. This slice intentionally keeps canonical task semantics.

## Meaningful verification matrix

| Layer | Required assertions |
| --- | --- |
| Parser/DTO unit | exact defaults/bounds, canonical ints, repeated scalars, duplicates/unknowns/empty/bad encoding/UTF8; every canonical state and rejected alias; supported/unsupported mixed model types; appType rejected; UTC offset equivalence, half-open bounds, invalid/naive/overprecision timestamps; safe error projection; DTO omits internal fields |
| Artifact projection unit | all nine states preserve progress/errors; missing/malformed result tokens and type mismatches; zero-row workbook eligible; missing snapshot/manifest; epoch/owner mismatch; visibility changed; row/byte/time/digest mismatch; no SQL cast failure; full staged manifest with running/cancelled remains not_ready; eligible means metadata only |
| HTTP unit | history route and access-introspection agreement, GET-only/404 slash/405 Allow, read grants required but task/write grants unnecessary; no proof consumed on read; no-store/nosniff; invalid filters rejected |
| Real PostgreSQL scope/count | two actors same tenant + second tenant; different epochs/revoke-regrant; known expired AD entitlement WITHOUT epoch change; live domain disable/cap/group loss; mixed snapshots with one denied domain; all relevant source kinds; authorized filters applied before count/page/-1 sentinel; no snapshot and platform-only still visible during AD entitlement lapse |
| Real PostgreSQL list fidelity | real protected submissions appear before capture; failed before capture remains visible; equal created_at stable both directions; exact state/progress/attempt/updated timestamps; empty and out-of-range page math; 1000 versus1001 bounded-all; list/count same statement under concurrent task progress/submission; query has no task locks/per-row detail calls |
| Archive defense | ordinary audit task archive/restore remains rejected by F49; anomalous archived fixture absent from totals/list and404 detail/download; no visibility=all bypass |
| Real XLSX integration | submit existing audit producer -> worker -> reopen history -> detail -> actual bytes; inspect ZIP/XLSX header and real first/last rows, byte equality/SHA256 with manifest; header-only zero-row success; failed/cancelled/retry states truthfully shown; existing cancel-before-Finish staged artifact cannot publish |
| Download revocation/integrity | ready history then natural expiry/epoch revoke/hide or restore => current download denied; missing/corrupt/misordered chunk/digest => no partial successful bytes; unknown/foreign ID no artifact leakage; list can remain metadata-eligible for corrupt chunks but UI surfaces actual failed download |
| DOM/browser | page reopening requires no remembered UUID; filter apply/clear/page/status/time, pending filename labels, no fake type/format, row->detail->download, Back/Forward/reload/close; stale response/poll/download cannot cross account or navigation generation; session expiry; lost-submit recovery reuses key; genuine empty versus error; negative and zero row counts validated |
| Regression/aggregate | existing audit/task/access/archive tests, focused race, TypeScript/DOM/build, applicable make check; actual PostgreSQL/browser/lifecycle and exact-head CI recorded separately if executable. Missing environment means blocked, not passing. No external AD or code publication |

Local checks and actual runtime evidence are recorded separately in delivery.md. Current all-209/product acceptance accounting is unchanged; complete F38 and Issue #26 stay open for unsupported types/appType/source parity and genuine runtime evidence.
