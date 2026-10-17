# CI-only seal protocol verification slice

This slice provides isolated test helpers. Neither `cmd/adtr` nor the running-cancel guard installs or invokes this protocol. Default builds retain direct pgx delegation; a tagged application without the package-test coordinator remains inactive.

## What the local state asserts

Startup registers a ticket before the connector runs. Successful startup associates a unique handle and backend PID before returning the managed connection. A ticket remains open across second and subsequent transactions until wrapper Close observes native cleanup. Candidate registration, ticket association, pinning, revocation and lifecycle state share the gate mutex. Active registration never falls back to the inactive identity map.

One exact existing candidate generation can be pinned for epoch 1. `sealed` means no pending startup, one open physical connection, and the same live candidate registration on that connection. It does not mean an exporter snapshot, safe SQL, a correct lease, or stopped threads. Every such property requires independent PostgreSQL validation. The fixture's synthetic identity cannot grant production capture eligibility.

New connections wait while sealed and never call the connector. Cancellation retains the original context error. Active-epoch invalidation is published once outside the registry mutex before the error or lifecycle invalidation returns. Close marks a target invalid before calling native Close. Publication failure closes the control stream; the reader must treat EOF, partial frames and any INVALID as terminal, never let a later stale SEALED overwrite INVALID.

Protocol operations have bounded closed fields, one epoch and one pin attempt. Release is explicit and idempotent at the state-machine layer. No timeout or EOF reopens admission. The protocol returns after a release acknowledgement. A broken target control stream ends that helper protocol in a failed state; this slice does not claim it can subsequently release through the broken stream.

Frame size is bounded; arbitrary `io.Writer` completion time is not. The helper's inherited pipes, controller deadlines and owned-process cleanup provide its tested transport lifetime. A future application transport must prove that a blocked writer can be interrupted before relying on publication-before-error behavior. Closed/failed tickets are retained in this finite helper's map; pruning references to old connections/configuration is required before a long-running application installation.

## Evidence exercised by the CI tests

- Pending connector startup, second BEGIN, whole-connection Close and concurrent pin/revoke/Close accounting
- Every lifetime-invalidating route publishes INVALID, and a canceled waiter cannot return before publication
- No connector dispatch from a sealed/canceled wait; a synthetic one-second heartbeat context invalidates the attempt
- Unknown fields, wrong epoch, duplicate pin, partial frame, EOF and lost acknowledgement remain failed closed
- Real PostgreSQL row-barrier observation matches the registered backend, with a pending startup and another connection's second transaction preventing seal until clean Close
- Canceled PostgreSQL query/native asynchronous cleanup cannot yield a seal; CleanupDone and backend disappearance are observed separately
- Independent rescue process survives controller death, verifies its exact isolated target PID/starttime/executable, continues it before release, receives RELEASED and reaps it

The rescue helper's supervisor-pipe EOF/partial-frame scenarios retain a functioning target control pipe. They are distinct from a broken target protocol. Its registry is synthetic; the PostgreSQL tests establish real backend/lifetime behavior separately. Signals and terminal cleanup target only processes created by that test. No application process is attached.

Run in the existing isolated PostgreSQL CI group:

`go test -race -count=1 -tags=integration,adtr_ci_dbadmission -timeout=10m ./internal/dbadmission`

Local work runs only standalone formatting and offline static/Python checks. Go, race, subprocess supervision and PostgreSQL outcomes must be reported from the exact-head CI run.

## Activation remains blocked

The baseline fixed-source audit covers the prior exact tree. This slice changes the managed boundary and requires incremental review plus a renewed complete source manifest. The former 47-file wiring manifest alone does not prove closure. General `CompleteWorkerClosure` remains false.

Before application wiring, additionally prove current cfg/closure/boxed-value flows, native pgx startup/fallback/auxiliary cleanup accounting, exact candidate-plus-task resultVersion and snapshot/lock-chain selection, all owned-backend equality, actual three-kind five-second heartbeat behavior within the unchanged 1.5-second capture window, and real resumed cancellation/lease outcomes. Main's journal isolation must be exercised with a nonnil candidate, beyond nil-token coalescing tests.

The future parent must retain one absolute capture deadline and one STOP, inspect buffered INVALID/EOF/partial frames after all threads stop, run unchanged strict PG validation, and CONT before admission release on every recovery path. The existing parent-thread watchdog does not survive parent death. The separate helper rescue proof is a prerequisite, not an installed production supervisor.
