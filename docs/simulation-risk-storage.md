# Synthetic risk storage (Issue #257)

Migration 25 adds only `simulation_risk_resets` and `simulation_risk_receipts`.
It depends on migration 24's immutable simulation event datasets. No fixture is
created by risk evaluation; no HTTP ingestion or real AD connection exists.

## Explicit illustrative basis

`adtr.risk.simulated.v1` / `illustrative-event-risk-v1` maps a dataset event's
**Actor** to the risk subject: `login_failure` → low (5), `service_access` → medium
(15), `login_success` → no risk points. These are deliberately illustrative
weights, **not an AD detection algorithm or a production threat verdict**.
`riskscore` caps the score at 100 and returns distribution, full records, trend,
future-evidence count and inclusive reset boundary. Dataset fixture/hash and
scoring/mapping versions accompany every result. Event record identifiers are
`event:` plus SHA-256 of JSON-encoded source event ID; reset identifiers are
`reset:` plus SHA-256 of JSON-encoded `[actor, operationId]`. This namespace avoids
source/reset collisions while retaining deterministic source traceability.

The subject must occur in the canonical dataset, even if all its events are
login_success. Unknown subjects return not found rather than fabricated zero
risk. `sourceEventCount` counts all subject events including future evidence;
`noRiskEventCount` counts all its login_success events. They are inventory counts,
not the current post-reset `evidence_count`.

## Transactions and authority

`Scope` is server derived and requires the new explicit risk read/manage grants;
event-management permission must not be reused as risk-management permission.
`EvaluateTx`, `ResetTx`, `OperationTx` neither authorize sessions nor commit. The
HTTP caller must perform live session/domain authorization and schema gate in the
same transaction, and **roll back every error**, including audit failures.
`AuditFunc` is mandatory for reset and must insert application audit through the
supplied transaction. The reset, audit and receipt commit atomically.

Reset input requires dataset, subject, operation ID, trusted actor, reason and
`expectedRevision`. Initial revision is 1; every reset appends exactly one
revision. Expected revision prevents stale overwrite; omitting it is invalid.
The operation ID is an actor-bound idempotency key. Reuse with changed subject,
reason or revision conflicts; exact retry returns the original durable receipt
without another audit. Database-selected reset time is not request identity.
Receipt lookup is scoped by tenant/domain/dataset/**authenticated actor**.

A transaction advisory operation lock precedes a shared dataset+subject scope
lock. Evaluations and resets use the same scope lock, serializing concurrent
updates and avoiding partial history. All lock keys hash unambiguous JSON arrays.
Every successful reset stores its server-selected inclusive boundary, including
resets from zero. Regressing server clock returns conflict for both live evaluation and reset
instead of ignoring a committed reset or moving the boundary backward. Historical
receipt recovery uses its recorded timestamp and reset prefix, not the live clock. Application callers use read-committed transactions; higher
isolation serialization failures require whole-transaction retry.

## Complete bounded evidence and immutable recovery

Each read validates the entire dataset's canonical tenant/domain/provenance,
source row ID, supported kind, timestamp and count. It reads at most 4097 rows
and errors above 4096; it does not score an incomplete page. Complete reset
history is read at most 1025 rows and errors above 1024. Contiguous revisions,
reset identities and nondecreasing boundaries are validated. Bounds prevent
unbounded CPU/memory and write growth; reaching them returns an explicit limit
error, never silent truncation. Removing them would permit unbounded processing.

Receipt recovery reconstructs the canonical score at its original server time
using exactly the original reset prefix. Later resets do not change old receipts.
Stored receipt/result JSON and request identity must match reconstructed state.
Append-only database triggers reject update/delete/truncate of both tables.
Fixture-bound foreign keys include tenant/domain/dataset/hash; no default grants
or existing role assignments are changed by schema migration.

## Verification

Unit tests cover mapping, score/distribution/trend, inclusive reset, future
handling, zero-risk/unknown subject, bounds, identifiers, authorization and
request identity. The integration-tag test requires isolated PostgreSQL via
`ADTR_TEST_DATABASE_URL`; missing infrastructure is a failure, never a skip.
It exercises durable reset/recovery, races, rollback, authorization scope and
immutable rows. Local unit/race/vet results and unrun PostgreSQL stages must be
reported separately; a compiled integration suite is not an executed suite.
