# One-shot CI application admission

This candidate wires the previously isolated controller into exactly the user,
group and computer running-cancel CI suites. It is not a production deployment.
The default build has a no-op bootstrap, no descriptor/environment reads and no
controller goroutine. Its signal context remains at the original location.

## Qualification

The explicitly tagged Linux/amd64 worker installs before configuration consumers
or database constructors. Missing opt-in leaves tagged API/migration processes
unchanged; partial opt-in, the wrong mode/platform, reused witness or prior
managed connection prevents installation. The build target first runs the typed
boundary and complete fixed-source certificate checks. The two complete source
manifests are reviewed snapshots, not a claim that the checker proves arbitrary
Go dataflow. CompleteWorkerClosure remains false for arbitrary future programs.

A private inherited normal socket exposes the complete pending/open/closing
registry, including registration generation and the opaque first-fenced-progress-10
candidate. The parent must match task, kind, attempt, slot owner, fence, committed
result version, exact backend and original ACTIVITY_SQL/barrier chain against a
fresh PostgreSQL sample. Candidate identity alone proves no snapshot. Original
snapshot=1/chunk=0/manifest=0, real lease and strict SQL/lock checks remain required.
The original 1.5-second absolute capture deadline is never reset. After pin, no
new ConnectConfig is dispatched. Every other connection must complete its entire
lifetime (including second BEGIN and CleanupDone). Sealed pending=0/open=1 and the
complete registry backend set must equal the complete owned application-name PG
set in both directions. Inherited PG* options are removed from this CI child.

After the sole SIGSTOP and verification that every owned thread is T, the parent
checks its existing shared page twice and drains normal INVALID/EOF/partial
frames, then repeats the original strict PostgreSQL qualification. It sends no
request to the stopped worker. Any mismatch fails closed; there is no stop/resume
selection loop or SQL/lock whitelist expansion.

## Sticky witness and private transports

The parent creates one anonymous 4096-byte shared page for this exact worker.
Layout: magic ADTRGATE at 0, uint32 version=1 at 8, length=4096 at 12, uint64 epoch=1
at 16, aligned uint32 invalid at 24, aligned uint32 bound at 28, 16-byte launch
nonce at 32, zero reserved bytes from 48. The worker validates and CAS-binds once.
Every invalidating mutation first calls the sole markInvalidLocked, which stores
sticky 1 before modifying eligibility, propagating a context error or publishing.
No reset exists. The parent reads qualification only with all threads stopped;
two reads are not represented as an atomic observation of a running process.
After worker Wait it reads the same page again. A released ACK cannot erase an
invalid bit or make a failed run successful.

Actual application I/O uses inherited pollable os.File socket endpoints, full
single-write checking and a 500ms write deadline, not an arbitrary io.Writer.
SOCK_STREAM gives no frame atomicity; partial frames/writes fail. Normal requests
are at most 1024 bytes, responses 65536 and complete active records 128; rescue
requests are at most 256 and replies 1024. Overflow is fatal, never truncation.
Private identities travel only over the owned normal socket. Errors and artifacts
contain no raw frame, SQL, connection string or nonce.

## Recovery and lifetime

An independent sibling rescuer authenticates PID/starttime/executable with a
pidfd and is armed before the capture STOP. Normal and rescue channels are
independent. Normal EOF first invalidates and requests business cancellation;
it does not close rescue. The rescuer confirms CONT/non-stopped before release
or abort. On rescuer failure the parent must kill/Wait its own rescuer before
assuming the single ACK-reader role. Parent death before ARM also recovers and
terminates the owned child rather than leaving queued STOP behind. The rescuer's
fixed watchdog is tied to ARM, not a new capture deadline.

The health callback's original 2s and export heartbeat's 5s contexts are not
extended or hidden. A pause beyond them may produce post-resume INVALID and an
overall failed test. Capture-time proof is not permission to suppress that
terminal result. Final witness validation happens only after worker Wait.
Simultaneous loss of both parent and rescuer is not claimed recoverable here.

This is a finite CI process: at most 4096 tickets may remain retained at once.
Proven clean, unregistered, non-epoch terminal tickets are reclaimed as detailed
below; unresolved and epoch-referenced tickets remain until process exit.
Long-running production installation is outside this CI-only policy. The child's page stays
mapped to process exit so invalidators never retain an unmapped pointer. The
parent disposes its mapping only after Wait.

## Evidence layers and remaining validation

- Offline guard contracts exercise full identity mismatch, registry mismatch,
  single STOP, original deadline, stopped-witness failure and CONT-before-release.
  The original validate_sessions and validate_checkpoint bytes remain unchanged.
- Go unit contracts cover complete registry states, over-limit failure, closed
  protocol schema and sticky-before-mutation for revoke/close/cancel/transport.
- Isolated real child tests stop on both sides of the sticky store and prove the
  post-Wait invalid bit survives. These process hooks use transport invalidation;
  the other invalidation paths have separate in-process ordering tests.
- The real StartCIController transport test breaks normal while rescue remains
  usable. Another isolated child holds original 2s/5s waiter contexts across a
  >5s stop, then checks original deadline errors, INVALID, release and final Wait.
  Its target is synthetic; it does not pretend to execute a full exporter.
- Existing real-PG lifecycle/barrier tests also verify the v2 registry backend.
- Only the three actual running-cancel E2Es can prove main → non-nil candidate →
  designated health connection → original activity SQL → PG snapshot qualification.
  Their evidence validator requires admissionSealed, stickyWitnessValid and the
  committed result version. Runner success additionally requires final Wait's
  zero sticky witness. After successful worker Wait and journal.Close, the same guard requires exactly one new
  persisted worker queue_progress journal event since the queued boundary, with
  its four original business fields and exact existing metadata columns. The initial
  target is the only nonterminal task; final state is cancelled with no nonterminal
  task. The matching unique worker report must be acceptance-stopped, fully ACKed
  and have zero rejected/queue-full/write-failure/unacknowledged/abandoned counts. It emits
  only a closed verification event, no task/worker/candidate identifiers.

Local Go/PG/process execution was not performed. All new runtime assertions must
run in the exact reviewed CI tree. A capture timeout, extra held activity connection,
post-resume invalidation or cleanup failure must remain a failure and be diagnosed
from retained closed diagnostics, not converted to success by retrying or relaxing
qualification.

### Running observations and frozen qualification

Before pin, every PG row is checked for structure, owned PID identity and an
acyclic blocker graph contained in the complete owned sample plus the barrier.
Runtime lock types and their projected fields are the closed PostgreSQL 17.6
set from [lockfuncs.c](https://raw.githubusercontent.com/postgres/postgres/REL_17_6/src/backend/utils/adt/lockfuncs.c)
(lines 25–38, 231–335 and 378–411); modes follow
[lock.c](https://raw.githubusercontent.com/postgres/postgres/REL_17_6/src/backend/storage/lmgr/lock.c)
(lines 97–108), plus the predicate SIReadLock projection. Unknown types, modes,
malformed OIDs/sub-IDs and external or cyclic blockers fail immediately.

Pending registrations or unequal complete PG/registry backend sets wait within
the original deadline without pinning. Once pending is zero and sets agree, only
the uniquely matched candidate's singleton PG row passes the original strict
validator before pin. Other recognized owned connections may finish their entire
lifetime; their SQL or locks receive no freeze qualification. After a sealed reply,
a fresh full runtime scan and equal backend sets are required before the original
strict validators qualify the sole target. A backend still visible during cleanup
therefore waits, while a malformed row still fails. After STOP, all strict checks
are immediate and no further capture attempt is allowed.

### Pre-capture failure diagnosis

PR173's first application run reached worker readiness in all three running-cancel
suites, then fixture directory tasks remained queued. The worker was still alive
before cleanup; its cleanup exit code was 1. At that point those facts did not identify the first
invalidating branch. The lifetime allocation limit of 4096 was a source-supported
hypothesis because four 250 ms polling slots and background work continually open
connections during the multi-minute fixture. Failed connection startup or unclean
connection closure can also invalidate the same pre-pin gate; the subsequent
closed diagnosis below confirmed the capacity branch for these failures.

The private normal channel therefore supports a separate `diagnostic` operation.
It returns only the first closed reason, its phase and current pending/open/total/
retained counts, without registry records or identities. The sticky store remains
before the reason/phase mutation; subsequent failures cannot overwrite the first
reason. `unclean_close` names the existing `closed(false)` branch and does not
claim to distinguish native closure from incomplete asynchronous cleanup.

On an existing fixture failure, the runner may request this bounded diagnostic
only while the worker is alive, unarmed, unreleased, identity-verified and all its
threads are running. Otherwise it records `unavailable` and proceeds to cleanup.
It never waits for this request on a stopped worker, uses the sole reader mutex
and sequence, and prints only the closed fields. The diagnostic is not admission
or capture evidence. That diagnostic-only change preserved the 4096 limit, sticky failure policy and
fixture's 30-second expectation. The subsequent exact-tree CI established the cause below.

### Confirmed retained-ticket exhaustion and safe reclamation

The next PR173 run recorded the same first cause in all three real suites:
capacity, phase open, pending/open zero, total/retained 4096. This establishes
that completed lifetimes retained by the registry exhausted its cumulative cap
before capture. The capacity now remains 4096 retained tickets, rather than 4096
successful lifetime allocations. The cumulative diagnostic count remains monotonic
and may exceed 4096; both it and the shared generation counter fail closed before
uint64 overflow.

A ticket is reclaimed only on the existing clean Close result (native connection
was not already closed, Close returned no error and CleanupDone is complete),
while the gate is valid, its original registration is nil and it is not the target
of any epoch, including a released epoch. Under the same registry mutex, accounting
and terminal state are updated, the ticket is deleted, and connection/backend
references are cleared. Failed, dirty, active, pending, closing, still-registered
and epoch-target tickets remain retained. Invalidation is permanent; an in-flight
invalid publisher therefore prevents reclamation. Neither the sticky bit nor the
first failure is cleared.

A revoked but never-pinned candidate may be reclaimed after clean Close. Its stale
release closure only compares the old ticket's registration pointer, and cannot
change a new ticket; generations never repeat. Tests cover stale revoke, attempted
rebind and pin, release replay, retained limits, counter overflow and publication
in flight. Existing real-PG tests additionally require clean Close to reclaim its
ticket and native error closure to retain it. Exact-tree CI must still verify that
fixture processing and the full capture/rescue chain now complete.

### Capture-path closed diagnostics

After reclamation, all three actual suites passed the fixture boundary that had
exhausted capacity. Capture then failed: two reported `capture_readiness` with an
unclassified reason, and one reported the unchanged capture deadline without a
last-wait category. The existing sanitizer omitted registry/candidate fixed
messages and treated the controller's exact ControlFailure subclass as unknown;
these events therefore did not establish which capture check failed.

The guard now maps every existing registry/checkpoint require message and uses
closed substages for registry snapshot/matching, candidate binding/strict checks,
pin and sealed observation. Deadline evidence includes the count of completed PG
observations and the last fixed wait reason, including a pending task boundary or
missing candidate. No additional observation or control request is issued.

Controller diagnostics are accepted only from the exact owned ControlFailure
class and an exact three-field mapping of enum-produced operation, step and cause.
Unknown subclasses, malformed mappings, extra fields and arbitrary strings remain
unclassified. Neither exception arguments nor private frames are serialized. This
instrumentation retains the original absolute capture deadline, refusal behavior
and recovery order. The next exact-tree CI must distinguish a target-chain mismatch,
registry inconsistency, protocol rejection, or a checkpoint that never became ready.

### Candidate registration precedes the activity statement

PR173 run 39039571691 reported `candidate_binding/candidate_chain_mismatch`
for all three export kinds. This establishes that the combined activity predicate
was false; it does not establish which member failed. In the fixed source,
`withActivityTx` registers the candidate before `Begin`, schema validation, and
the activity statement. The observer also samples PostgreSQL before requesting
the registry snapshot, so registration may be newer than the sampled SQL.

Before pinning, a complete trusted sample and complete registry/PG set must first
identify one authoritative candidate. The guard retains its registration and
connection generations, backend, task, kind, attempt, slot owner, fence, committed
version, and phase. Disappearance or replacement fails, including while the next
registry set is incomplete. Only known BEGIN, schema, schema-gate, empty query,
or activity statements that have not reached the original direct activity/barrier
predicate may wait within the original absolute 1.5-second window. Unknown SQL
fails with closed diagnostic fields. Waiting never grants pin or STOP eligibility.
The same singleton strict checks remain mandatory before pin, and sealed/frozen
samples retain immediate strict rejection.

An indirect owned activity chain is only a waiting condition. If it persists,
the attempt expires without STOP. It may reveal a shared-row drain obstruction;
this patch does not authorize additional exempt connections. Timeout diagnostics
classify only query/state/wait enums, transaction boolean, and blocker category
from the already sampled data. No identifier, query text, new RPC, or extra window
is introduced. Actual CI must determine whether early observation or an indirect
chain caused the recorded failures.

### Selected-task CI activity row (shared-row obstruction)

Run 39041724007 established a persistent indirect owned activity chain for the
computer and user cases (238 and 243 observations respectively); neither pinned
or stopped the worker. The group case expired while sending a snapshot request,
which does not independently establish that chain. A shared activity-row barrier
can hold an earlier ordinary callback while the target waits behind it. Requiring
all non-target lifetimes to close while holding that barrier is then unsatisfiable.

The three CI suites now prepare selection while the worker is live, before the
existing queued pause. Preparation requires no nonterminal tasks and receives an
ACK. Only candidate-bearing health callbacks await configuration, before any
connection/ticket/BEGIN; ordinary queue/recovery/scheduler/heartbeat calls retain
the original path. Once the task is created during queued STOP, the parent sends
one configuration without waiting for a stopped process. After CONT, configuration
ACK and all capture work share the unchanged absolute 1.5-second window. Invalid,
repeated, cancelled, or broken configuration fails closed. A candidate that was
waiting during preparation but does not match the selected task invalidates the
attempt; ordinary unselected callbacks are not made barrier participants.

For an installed controller and the selected, consumed authoritative proof only,
the actual activity SQL receives a 64-byte key:
`ci-activity-` plus lowercase, unpadded Base32 of SHA-256 over the fixed bytes
`adtr-ci-activity-v1\0`, the 16-byte launch nonce, `\0`, and the UTF-8 task ID.
Python and Go share a fixed cross-language vector. The original worker owner still
checks slot ownership. Task/kind/attempt/fence/commit-version/phase remain unchanged.
The registration generation includes the actual SQL key in private IPC; the parent
independently derives and matches it. Configuration cannot be replayed after a
registration has consumed the selection. Journal events, throttling, callback
identity, default builds, and tagged builds without an installed controller keep
the original worker identity. This is an explicit CI parameter variant, not a
claim that production writes use the alias.

Before CONT, the harness commits one synthetic `failure/cycle_failed` queue row,
rejecting any insertion collision without overwriting it. It then locks exactly
that row. All ordinary activity writes remain on their original row and can finish;
the target uses the unchanged ACTIVITY_SQL, transaction and schema-check sequence.
No SQL or frozen lock allowlist is enlarged. The original strict direct-chain,
unique retained target, complete PG/registry equality, all-thread STOP and sticky
checks remain necessary. The sentinel deliberately causes the real health reader
to report `unavailable/worker_cycle_failed` during this isolated CI interval; it
must not fabricate success or bypass existing health assertions. The new PG
health test checks that exact side effect and restoration after precise removal.

After worker Wait, including failure paths, cleanup removes only the successfully
inserted nonce/task alias in tenant `default`, cycle `queue`, verifies no alias row
remains and the original queue row still exists. An insertion that failed or whose
success was not confirmed grants no cleanup ownership. The isolated test database
is still torn down by the existing runner. Guard diagnostics and journal verification expose only closed categories/counts,
never the alias, task identity or SQL parameters. The existing health API does
return the opaque alias in its workerId field while the fixture row exists; this
explicit CI read-side effect is removed by cleanup, not hidden by a reader change. Real PG tests
contrast shared-row indirect blocking with isolated-row ordinary completion and
unique-target drain; real three-kind CI remains the application-level acceptance.

### Python fixture connection regression

Run 39046143973 reached `initial_boundary/database_session_ended` in all three
real application suites. Source inspection found that the alias INSERT and
post-Wait DELETE used a Psql connection configured with
`default_transaction_read_only=on`; `ON_ERROR_STOP=1` terminates that session on
a rejected write. The artifact contains no SQLSTATE, so the exact server error
was not independently recorded. The deterministic connection-mode defect is
fixed by dedicated short-lived fixture writer connections, each closed in
`finally`. The persistent qualification observer remains read-only, with the
same timeouts and strict validators. Collision rejection and cleanup ownership
checks are unchanged. A real Python Psql regression in the existing disposable
CI PostgreSQL container covers these actual options and fixture methods before
browser validation; offline contracts also require the connection modes at
each call site. The new regression's runtime outcome remains subject to CI.

### Resume-time activity row scope

Run 39048248387 passed capture and reached terminal cancellation checks before
user/computer resume-cycle verification failed with two queue rows rather than
one. The existing helper counted the whole queue table, while the approved CI
alias intentionally remains until worker Wait. Running-cancel verification now
uses the same authenticated launch nonce and target task to derive that exact
alias privately. Each queue sample must contain exactly one original owner row,
exactly one expected alias row and zero other queue rows. Both increasing success
timestamps still come exclusively from the original owner. The initial queued
pause and other suites retain the original whole-table single-row assertion.
A prefix match or a relaxed count is never sufficient. The opaque alias is
already visible through the CI health row; this helper also places it in its
existing local docker/psql query argument, visible to local process inspection.
It is not added to test evidence or diagnostic output. Post-Wait cleanup still
requires deleting only the owned alias and retaining the original row.

The reported final witness/cleanup failure remains a separate unverified cause;
this row-selection correction does not waive final sticky, worker exit, cleanup
or journal acknowledgement requirements.

After the worker's completed Wait, `owned_ci_final_state` reports only existing
local exit-zero, sticky category, controller-invalid/released flags and cleanup
stage. A malformed page is `other`, never a clear witness. This diagnostic sends
no control request, preserves the original acceptance expression and cleanup
order, and does not make successful process exit equivalent to valid capture.
