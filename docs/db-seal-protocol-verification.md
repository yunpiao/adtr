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

## Same-connection operation diagnostics

The normal Worker.Run shutdown counterexample passed the controlled tagged CI
suite on run 39062068286. Four empty-queue slots and recovery had five dispatched
queries blocked at the migration lock; cancellation returned normally while pgx
had already marked the native connections closed. CleanupDone and backend absence
were subsequently verified. This establishes that normal cancellation can produce
native asynchronous close, but does not identify the operation behind the two
actual running-cancel failures in that run.

The CI-only operation observer adds evidence without changing the dirty-close
acceptance rule. With no existing tracer, it copies the connection configuration
and installs a QueryTracer covering Conn and delegated Tx queries. Existing
tracers remain untouched; absent records mean unobserved, not error-free. The
default build returns the original configuration unchanged. The observer retains
only the first failing operation and the first failing operation observed with a
closed native connection. The latter is an association, not necessarily the cause
of closure: a later rollback can observe a connection closed by an earlier error.
SQL, parameters, error objects, and identities are not retained in these records.

Returned errors are classified independently before sampling the original query
context. Cancellation matches require the corresponding context state and error
sentinel; a network error with a cancelled context remains an other error. Root
context and lifecycle phases are snapshots, not causal shutdown proof. In
particular birthCaptureEnded is the already-consumed authorization cache at
connection birth: false does not prove that the parent never committed capture.
The two records are immutable so rollback cannot overwrite earlier evidence.

After Wait, only closed categories and booleans are projected from the private
log. Missing, duplicate, malformed, or excessive records produce unknown evidence.
The original sticky failure remains fatal. Any future recovery rule still needs
same-connection cancellation evidence, a correctly bound post-capture lifetime,
actual CleanupDone and backend disappearance, and unchanged business/journal
terminal proofs; this diagnostic change supplies no such exemption.

## Restricted recovery cancellation and final resource proof (candidate)

This candidate distinguishes a narrowly witnessed post-capture cancellation from
an unresolved physical lifetime. Only a new ordinary ticket born after the
existing authorization was consumed and release completed can qualify. Earlier
tickets, any previously registered candidate, and the target cannot qualify. Release consumes once before its state
transition; an abort without authorization still releases for rescue, but cannot
later gain recovery permission from a late frame.

The first error must itself be the first native-closed error (same immutable
record), a statement whose original context and returned error match Canceled,
with the observed root also canceled and the same raw connection. A deadline,
unknown/missing observer, earlier other error, or BEGIN whose native die happens
later stays fatal. Any later business operation or overlap contaminates the
candidate. Only an already-native-closed cleanup rollback with the precise
pgconn.ErrConnClosed sentinel is permitted afterward; fixed dependency source
shows that this branch does not dispatch SQL.

A qualifying wrapper Close enters a retained pending-cleanup state, not clean
closure. At controller cleanup, after application producers and recorder Close,
all pending CleanupDone channels share one fixed two-second budget. The registry
mutex is not held while waiting. The parent still enforces its original 30-second
Wait ceiling; the 1.5-second capture budget is unchanged. Missing completion,
remaining open/closing/pending connections, active traces, or contamination
remains a hard sticky failure. Existing sticky history is never cleared.

A final private summary is necessary but insufficient. After actual Wait and
sticky reread, the parent independently checks that the complete startup-verified
worker application_name has zero backends in the expected isolated database.
It also retains the original business terminal and journal final delivery proofs.
Query failure, a malformed/missing summary, or a remaining backend rejects the
run. No PID subset is used to stand in for the full owned set.

Default builds retain the original pgx context, query result and Close behavior.
This CI candidate adds bounded shutdown waiting, retained cleanup metadata and
extra observational synchronization. It can conservatively reject other genuine
cancellations. It addresses the independently demonstrated normal Worker.Run
shutdown/native-close combination, not a proven cause of every historical
running-cancel failure. New race/PG/whole-suite runtime evidence is still required.

The new recovery PG fixture uses a synthetically authorized target-only registry
and a real capture-end pipe as its starting state; it does not reproduce the
ACTIVITY barrier/strict capture prerequisite. It then uses real dispatched SQL,
root cancellation and a latch on only the primary socket's native Close to hold
actual CleanupDone deterministically. Its independent full-owned PG observation
polls within the test deadline. The application guard instead makes a single
strict observation after Wait and rejects even transient remaining backends.
The three real main/browser suites remain required for the full capture and final
resource proof on the exact candidate tree.
