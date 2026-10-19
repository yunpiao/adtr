# Activity candidate identity verification (no admission gate)

This slice associates a committed task attempt with one specific health activity
connection. It does not seal admission, select a capture target, stop a worker,
change capture timeouts, or authorize a new SQL/lock shape. The existing strict
PostgreSQL guard remains the final capture authority.

## What the candidate establishes

Only the three asset-export kind names may produce an opaque candidate after a
successful Engine.WithTx commit. The immutable Start-returned lease must identify
attempt 1, running state, matching slot owner and positive fencing token. The
work must actually return success with progress 10. Its expected version must
equal the Start-returned stable result version, and commit version must be exactly
one higher. Start already increments result_version: no hardcoded 0/1 version
assumption is valid. Nil work retains the existing invalid_progress behavior.

The phase is named `first-fenced-progress-10`. It establishes a candidate first
fenced commit at progress 10, not a snapshot proof. A future target selection MUST
also match the exact task/attempt/owner/fence/version to an independent strict PG
sample with one snapshot, zero chunks/manifests and sufficient live lease. The
synthetic integration tests deliberately demonstrate candidate creation without
an exporter snapshot table, so no consumer can mistake the token for eligibility.

## Capability lifetime and routing

ActivityCandidate is a sealed interface with a private concrete implementation.
The proof contains task/attempt/slot/fence/version identity but exposes no getter.
JSON and fmt String/GoString output are fixed strings, never identifiers.

WorkerActivity retains its original four fields. The optional candidate callback
carries the opaque proof as a separate parameter; a single event invokes at most
one callback. If the candidate hook is absent, the original activity callback
still runs. Heartbeat, Checkpoint, recovery, scheduler and completed-cycle events
have no candidate. Ordinary and candidate callbacks share the same existing
five-second recorder coalescing state and two-second observation context.

Main sends only the original WorkerActivity to the operational journal. Only the
dedicated health activity call receives the candidate. That call requires a valid
queue/progress/empty-code observation and binds the proof to the same process's
exact slot owner. The typed checker restricts mint, consume and register object
references to their approved functions; a package-wide exception is insufficient.

Binding spends the proof once, including failed owner/connection binding. It
associates the proof with one existing opaque Conn handle and its backend identity.
It grants no future connection allowance. Registration is internal, with no public
enumeration or control transport. The health transaction releases it on all exits;
the minting callback scope also revokes it on normal return, coalescing skip, error
or panic. A repeated release is harmless; a repeated bind is rejected. No raw
connection, DSN, SQL, parameter, backend ID or task identity enters diagnostics.

## Build behavior and impact

The `adtr_ci_dbadmission` tag is required only for candidate identity verification.
Ordinary builds call the original WithTx unchanged, produce nil candidates and
keep registration inactive. Tagged builds register identity but still provide no
application admission coordinator installation API. Removing the tag disables
identity evidence; it does not weaken any existing guard. Its costs are bounded
per-callback metadata, synchronization and a dedicated candidate health transaction
path with the same SQL/schema/context/commit/cleanup semantics as the original.

Production changes are limited to tasks activity dispatch and tagged/default
helpers, explicit main recorder forwarding, the dedicated health entrypoint, and
new dbadmission identity files. No exporter SQL, schema, credential, permission,
heartbeat failure interpretation or connection waiting policy is changed.

## Verification and remaining limits

Tests cover default callback selection, three kind names, wrong attempt/fence/
owner/version/phase inputs, JSON/fmt privacy, physical handle isolation, real
Start followed by committed WithTx, deferred-constraint commit failure, rollback,
nil work, later commits, replayed bindings, and callback skip/panic revocation.
The health integration fixture starts four executors behind a test-only barrier
before releasing their fenced work, then verifies ordinary recording cannot spend
proofs, invalid status cannot bind, and the correct health call binds once.

The existing isolated PostgreSQL runner retains its default all-package and auth
coverage. It additionally runs the candidate tests under integration plus the CI
tag. No fixture configuration means failure, not a skipped pass. Local evidence
is source review, standalone formatting and 95 focused offline Python contracts.
Go/race and PG behavior still require controlled CI; written tests are not runtime
proof until they pass.

The companion checker scans the complete binary dependency set and directly
resolved typed APIs as a regression contract. It is not a security sandbox or
arbitrary data-flow proof: nested user aggregates containing configuration
pointers and subsequently converted to any, and closure-capture flows, remain
unproved. CompleteWorkerClosure stays false.

Full worker-entry closure, a controller protocol and live capture selection are
separate obligations. In particular, later activation must account for all pending
connections and physical lifetimes, independently verify PG residuals, and avoid
turning admission refusal into heartbeat lost-lease cancellation. The 1.5-second
capture deadline, one STOP, strict post-stop validation and CONT-before-release
cleanup remain mandatory. Identity registration alone resolves none of those
activation decisions.
