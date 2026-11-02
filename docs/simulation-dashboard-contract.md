# Synthetic dashboard contract v1 — Issue #60 / AD-F142–148

This is a bounded synthetic engineering slice, not original-field acceptance or real AD/Windows/product validation. It introduces no schema, grants, collectors, response actions, notifications, or background work. Existing real host-health UI is separate and must not be described as synthetic domain connectivity.

## Request and authorization

`GET /api/simulation/dashboard/summary?domainId=…&datasetId=…&window=1h|24h|7d&subject=…`

The HTTP adapter requires an explicit window and dataset. Subject is optional; omission means no selected actor. Live session, all three existing `simulation_events`, `simulation_risk`, `simulation_detection` readable grants, live domain membership, exact schema and current actor's detection epoch are required. There is no admin bypass or new permission vocabulary. `ADTR_SIMULATION_DASHBOARD_ENABLED` is optional and defaults false; enabling it requires all three constituent flags. Removing/disabling it hides this endpoint without changing existing features. Its only side effect is enabling bounded reads and CPU evaluation, never executions or writes.

`simulationdashboard.SummaryTx(ctx, tx, Scope, Input)` receives trusted tenant/domain/actor/epoch and a caller-owned READ COMMITTED transaction. `Scope.Read` means the adapter already checked all three readable grants. Auth locks and session freshness follow existing behavior; changing authentication to REPEATABLE READ would require separate logout-race proof and is not done here. Caller must roll back any failure and commit before publishing. Errors never become successful zero summaries.

## Snapshot and budgets

One SQL statement reads the dataset, events, current cases with final audit, selected actor resets and latest visible successful detection task plus its immutable pinned template. `statement_timestamp()` is the shared UTC `asOf`. Each source uses the same MVCC snapshot, tenant/domain/dataset/fixture binding. No per-card requests or later source queries occur. Existing authentication holds current identity/grant locks through the response transaction.

Limits: 4,096 events (4,096 JSON bytes each), 100 cases, 1,024 selected-subject resets, one latest task. LIMIT+1 detects overflow; data is not silently truncated. Per-task persisted JSONB spacing is allowed up to 120,000 bytes for template/result, then canonical result must satisfy the executor's 60,000-byte limit. Oversize bodies are replaced with null sentinels before SQL aggregation and rejected. Source envelope cap is 24 MiB; encoded summary cap is 2 MiB. No budget in an existing service was changed.

`snapshotId` is SHA-256 of the response with an empty snapshotId, including asOf and the validated source-derived facts. It is a content fingerprint, not a durable replay token. A later refresh has a new timestamp/fingerprint even if evidence is unchanged. Corrupt source fields, malformed audits, clock regression, missing tables and database errors fail the entire request rather than becoming unavailable metrics. No raw SQL/errors or task payload/authorization epoch is exposed.

## Fields and exact denominators

The JSON envelope is `adtr.dashboard.simulated.v1`, `simulation:true`, `productAccepted:false`; Go wire definitions are in `internal/simulationdashboard/types.go`.

- `dataset` exposes immutable fixture ID/hash/contract and complete event count.
- `coverage.eventCount` is all dataset events; `caseCount` is all current cases. Source timestamps cover all dataset events, including future/out-of-window events; empty source timestamps are null. `excludedFutureEvents` counts timestamps **greater than or equal to window end** (the exclusive event-activity boundary).
- `window.start/end` is `[asOf-duration, asOf)`. Activity buckets align to UTC minute/hour/day for 1h/24h/7d; first and last buckets can be partial and include only events in this exact window. `bucketSeconds` describes activity buckets, not close buckets.
- `eventActivity.total`, kind counts, observed actor/device/source-IP frequency rankings, and distinct actor/device counts use only events in that window. All three kinds (`login_failure`, `login_success`, `service_access`) are always present in every bucket, including evidence-backed zero counts. Top five ties sort by name; remainder is `total-sum(top counts)` for each dimension. Observed identities are not asset inventory, victims or compromised users. IPs are normalized by the existing core.
- `cases.scope=dataset_current`: current inventory only. Total = open + closed = sum of mark counts. No historical backlog is inferred because no creation clock exists. Every mutated case's final immutable audit is replay-validated against its state, mark, revision, timestamp and provenance.
- `caseCloseThroughput` counts terminal close operations, one per closed case, from validated final audits. `windowTotal` is all closes in the selected interval; `last60Seconds` is `[asOf-60s,asOf)`. Buckets are UTC minute buckets, clipped to the selected window; values are **case closes per minute**, not requests, events processed, average rate, or duration. Denominator is the exact selected window (and 60 seconds for the trailing value); partial minute buckets are not extrapolated.
- `risk` uses the existing illustrative event mapping and reset replay, for the selected actor over the complete dataset as of the snapshot (not only the activity window). Existing core semantics include events exactly at asOf and inclusive reset boundaries. Status is `available`, `not_selected`, or `subject_not_found`; only available includes score/revision/mapping. Unavailable score is absent, never zero. Trend has existing `{at,score}` points. Reset time later than asOf fails closed. Same-clock reset digest/revision ordering remains unchanged.
- `latestManualDetection` is the latest successful non-archived task for **current actor and current authorization epoch**, exact dataset/hash, ordered by terminal time then ID. It is all-time, not activity-window restricted. `available` exposes taskUUID/completedAt/template ID/version/hash/eventCount/matchCount. All returned and absent matches are re-evaluated against the pinned template and complete normalized fixture; payload version, admission receipt, hash, result version and scope must bind. `no_result` omits numeric counts. This is a generic manual synthetic result, not baseline/CVE/password coverage. Other actors, old epochs and archived tasks do not influence emptiness or counts.

## Original requirement coverage gaps (explicit `unavailable`)

AD142 asset inventory, connection health and collectors have no producer. AD145 original `lastMinuteCount` is request throughput; it is not mapped to case closes. Handling duration lacks a creation clock. AD146 baseline, vulnerability and weak-password summaries have no producers. AD147 compromise attribution and blocked user/IP counts have no classification/response producer. AD148 attack/high-risk heat has no validated classification; event-kind activity is a separate factual chart. These fields use fixed reason enums, not fake zeros. Issue #60 remains open for original requirements; simulated assumptions do not claim 1:1 coverage.

## Verification obligations

Pure tests cover boundaries, empty data, ranking ties, unavailable omission, reset binding, audit replay, corrupt/oversize sources, pinned detection match re-evaluation and exactly one source query. `TestPostgresDashboardSnapshot` (integration build) requires real PostgreSQL and fails if it is missing. It produces a task through declaration/admission/actual executor, verifies owner/epoch/archive filtering, and gates a snapshot while atomic close+reset commits to prove entirely pre/post facts. Compilation with `-run '^$'` is not PostgreSQL execution. HTTP and browser validation plus independent review and exact-head CI remain separate evidence; this contract does not assert those checks passed.
