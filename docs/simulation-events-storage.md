# Simulated event persistence (Issue #254)

This is an isolated **simulation**, not an AD collector or an attack verdict.
Every adapter result carries `simulation:true`, with the core contract
`adtr.securityevents.simulated.v1`, fixture name and dataset fixture SHA-256.
There is no ingestion HTTP endpoint and no automatic production seed.
`SeedFixtureTx` exists for explicit isolated integration-test setup only.

## Storage and ownership

Migration 24 installs `simulationevents.SchemaSQL` followed by
`simulationevents.ImmutableSchemaSQL`. Runtime calls do not migrate. The HTTP
caller must acquire the shared schema gate, revalidate the live session and
current resource/permission grants, derive `securityevents.Scope` and actor on
the server, then use the same `pgx.Tx` throughout. `Read` and `Manage` are trusted
caller inputs, never request JSON. APIs neither open nor commit transactions.

Datasets, raw synthetic events, cases, evidence links, transitions and receipts
are separate tables. Composite foreign keys include tenant, domain, dataset and
fixture hash; evidence additionally references the exact case and event. The
hash covers the complete admitted fixture (including cases/evidence). Existing
datasets, events, evidence, transitions and receipts reject UPDATE, DELETE and
TRUNCATE. Cases reject deletion, identity changes, revision jumps, reopening and
backward timestamps. Dataset JSON is normalized again on read; private core
`Case` state is never unmarshaled. `RestoreCase` validates persisted typed fields,
source, scope, revision, state, mark and timestamp. Detail and transition validate
all bounded history by replay and compare it to the restored snapshot.

## APIs and bounds

- `ListDatasetsTx(ctx,tx,scope,offset,limit)` lists one authorized domain.
- `QueryTx(ctx,tx,scope,datasetID,securityevents.Filter)` returns exact-filtered
  event pages plus the dataset's bounded case list. Core event ordering is
  `(OccurredAt, ID)`; cases and datasets are ordered by ID.
- `DetailTx(ctx,tx,scope,datasetID,caseID,historyOffset,historyLimit)` returns all
  bounded case evidence, a history page and explicit total. History ordering is
  revision ascending. It row-locks the case to stabilize state/history.
- `TransitionTx(ctx,tx,scope,TransitionInput,AuditFunc)` performs one mutation.
- `OperationTx(ctx,tx,scope,datasetID,actor,operationID)` recovers only that
  currently authorized actor's stored receipt; caller derives actor from session.

Page size is mandatory, 1–100, offset 0–4096. A dataset has at most 4096 events,
100 cases and 1024 transitions per case. A domain admits at most 4096 datasets.
All returned evidence is bounded by the dataset size. Limit overflow is an error,
not truncated success. Input identifiers/reason are nonempty, trimmed UTF-8,
control-free and at most 128 bytes. Core kinds, IPs and timestamps are validated.
These limits prevent unbounded hydration, replay and response memory. Removing
or widening them requires revisiting query budgets and UI behavior; they are not
optional runtime parameters. No retention/deletion behavior is introduced.

## Atomicity, retries and audit

Transition first serializes `(tenant,domain,dataset,actor,operationID)` with a
transaction-scoped advisory lock, then checks an immutable receipt. Same key and
same request hash returns the original result without a new transition or audit;
changed action/reason/case/expected revision conflicts. The request hash excludes
server time so an uncertain request can be retried unchanged. The actor is part
of identity; another actor cannot retrieve the receipt. Advisory hash collisions
only serialize unrelated requests; they cannot weaken identity checks.

A new operation locks its case with `SELECT ... FOR UPDATE`, validates the entire
history and evidence, checks expected revision through the core, obtains database
clock time, updates the case, inserts the transition, invokes mandatory
`AuditFunc(ctx,tx,receipt)`, and inserts the receipt. The callback must write the
application audit in the supplied transaction; nil is rejected. **The caller must
roll back on any error**, including callback failure, and commit only success.
No out-of-transaction audit is considered successful. A stale competing operation
conflicts after the first commit. Failure before commit leaves no durable partial
case, history, audit or receipt when the caller follows this transaction contract.

## Verification and limitations

Unit tests cover restore invariants, cross-scope/provenance rejection, malformed
inputs before SQL, request-hash identity, fixture evidence validation and schema
constraints. SQL string contract tests establish intended SQL, not PostgreSQL
execution. Required PostgreSQL integration scenarios are fresh/repeat migration,
fixture persistence and reconnect, changed-key conflict, concurrent revision
winner, callback rollback, history/receipt immutability and cross-tenant/domain
isolation; HTTP integration also checks live auth, CSRF, revoked grants and disabled
feature behavior. Actual PostgreSQL/Docker execution must be reported separately;
a compiled integration test is not a database pass. No real AD acceptance or
product acceptance count is implied.
