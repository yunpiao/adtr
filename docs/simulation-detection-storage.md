# Synthetic detection templates and manual runs — Issue #259

This slice uses **simulated business evidence**, not an accepted production detection
algorithm. Migration 26 adds persistent templates, immutable versions and operation
receipts. It depends on migration 24's immutable synthetic event datasets. Existing
versions/evidence are never overwritten or deleted; archive only prevents new runs
and edits. The application must acquire live identity/function/domain authorization
before calling storage APIs and commit/rollback the entire caller-owned transaction.
A nil transactional audit callback fails closed. Failed audit means rollback.

## API and transactional contract

`MutateTx` creates at expectedVersion=0, updates with exact current version, or
archives with exact current version and no rule/name replacement. Each version has
a SHA256 of its canonical JSON (hash field empty while hashing); the historical
body remains unchanged. Create/update/archive and their receipt and audit share one
transaction. Domain advisory locks serialize CAS and operation replay. Receipt keys
are tenant/domain/actor/operationId: exact retry returns original evidence; changed
content conflicts. At most 100 templates and 1024 versions per template are retained.
These are required defensive budgets, not retention deletion instructions.

`SubmitRunTx` requires a current non-archived template version and immutable dataset.
It inserts the existing engine task, durable run receipt and audit in the same
transaction. The payload pins template ID/version/hash, dataset ID/fixture SHA256 and
mapping version. Editing or archiving a template does not change a queued run.
Generic engine submissions without a module run receipt fail closed at execution.
`ReadRunTx` and `CancelRunTx` require owner, domain and exact task kind, and use the
existing engine for live authorization, cancellation state and lease handling.
Cancellation is idempotent by operationId; running cancellation remains a request
until the engine confirms termination. Receipt reads require current live read
permission, and return immutable historical receipts rather than claiming current
state. Run replay obtains current task status through the engine.

## Execution and authorization

Register `NewKind(authorizer)` only when the synthetic detection feature is explicitly
enabled. `simulation.detection` is owner-scoped, unschedulable and replay-safe, with
one attempt, 30-second lease, 5-second heartbeat and 10-second timeout. These required
bounds prevent unbounded execution; they do not enable background schedules. There
are no new goroutines, subprocesses, SQL/code interpreters, AD adapters or external
notifications. Generic tasks authorization must dispatch this kind to the dedicated
`simulation_detection` authorizer; no existing grant implies this permission.

The injected callback rechecks current actor/function/domain and authorization epoch
inside the existing engine's fenced `WithTx` both before and after evaluation and
again in the success gate. Epoch changes fail even if a grant is later restored.
Result reads reauthorize through the same engine callback. Lease owner/token, lease
expiry, cancellation, result-version CAS and terminal publication remain engine-owned.
Database errors do not become successful results; bounded evaluation returns no
partial matches. No retryable codes or automatic business retries are registered.

## Fixture mapping v1

Only complete normalized immutable `simulationevents` datasets are evaluated:

| Declarative field | Synthetic event source |
| --- | --- |
| event.category | Kind: login_failure, login_success, service_access |
| event.outcome | failure for login_failure; success for login_success/service_access |
| actor.name | Actor |
| target.name | Device (synthetic target device, not a fabricated account) |
| source.ip | normalized SourceIP |

`event.code` and `object.kind` are absent because the fixture has no such evidence.
Missing fields cannot satisfy negative comparisons. Session and occurrence time are
not exposed as unsupported rule fields. Rule scope must exactly match server scope.
The existing `detectionrules` allowlist supplies all/any and eq/neq/contains/prefix/in/
exists; arbitrary SQL, regex or source code is never executed. At most 1000 source
events, 128 rules, 32 conditions/rule and 10000 matches are accepted. A further 60000
byte result cap leaves headroom under the engine's 65536-byte JSON object limit;
large results fail `budget_exceeded`, not truncate. Payload is limited to 2048 bytes.

## Verification boundaries

Unit tests cover mapping, scope mismatch, payload pin validation, allowlisted policy,
invalid payload, stale lease/cancel/revocation error propagation, publication epoch
recheck and default deny. `TestPostgresSimulationDetection` (integration build tag)
uses actual PostgreSQL to cover immutable versions, CAS, duplicate operation replay,
changed-input conflict, atomic audit rollback, cross-domain reads, archive preservation
and mutation/truncate protection. API/worker lifecycle tests are a separate harness.

Local unit and tagged-test compilation are distinct from actual PostgreSQL execution.
No Docker/PostgreSQL execution was available for this implementation pass; the tagged
runtime suite must run in CI before claiming database validation. Real AD/Windows,
production throughput and product acceptance remain unverified (0/209 acceptance).
