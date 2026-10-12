# Safe browser failure evidence

Refs engineering [#123](https://github.com/yunpiao/adtr/issues/123) and
[#137](https://github.com/yunpiao/adtr/issues/137).

The browser matrix retains only `.e2e-validated/diagnostics.json` after a failed
job and a successful independent validation step. Retention is seven days.
The schema version, fixed phase/outcome/route strings, booleans and bounded
integers are the complete allowlist. Unknown fields, unexpected values,
malformed JSON and oversized inputs fail closed without publishing a file.
No raw logs, exception text/cause/call logs, headers, cookies, environment,
request/response bodies, URL origins, query strings or identifiers are copied.
This is structural validation and fresh encoding, not regex-based redaction.
The existing Go HTTP panic prefix detector emits only a boolean and never
retains the matching log line; unmatched log lines are discarded entirely.

## Interpretation

- `phases[].at_ms`: elapsed monotonic milliseconds from Python harness start.
  `before-cleanup` is recorded immediately on leaving the main test body,
  before termination requests; `after-cleanup` means the cleanup block has
  exited, including exceptional exits. It does not imply cleanup succeeded.
- `processes.api/worker.started`: whether an owned process was created.
  `before_cleanup` is its `poll()` result before any harness shutdown signal:
  null with started=true means still running at that snapshot; a number is its
  observed exit status (negative values are terminating signals on POSIX).
  `after_cleanup` is the result returned by `wait()` after termination; null
  means no successful wait was recorded. `forced_kill` identifies escalation
  after the unchanged graceful-shutdown timeout. A zero cleanup code cannot
  establish that a request completed or that the process stayed healthy before
  the snapshot. No continuous liveness sampling is claimed.
- `http_panic`: existing strict recovered HTTP panic detection, true/false after
  log inspection; null means that inspection did not finish. API and worker
  currently share the temporary log, so this flag does not attribute a panic
  to one process. Raw logs stay temporary and are not uploaded.
- `browser`: directory-v2 retains its original seven-field readJSON/TOTP/grant
  event contract. Directory readJSON and every fixture GET attempt add the
  separately validated request contract below. User-assets-v3 also records
  read-only page request lifecycle events and fixed step checkpoints. Each
  exact allowlisted pathname is retained as a template; all query strings,
  origins and unknown paths are discarded. Response records prove status was
  obtained, not successful body decoding or assertions. Original GET results,
  errors, fixture error sanitization, request options and retry eligibility
  remain unchanged.
- `browser_capture`: `present` means the validator read the event file;
  `missing` means no event file was present (expected for other suites or an
  early startup failure); `pending` occurs only before upload validation.

## Request and step contract

Request events contain exactly `origin`, `phase`, `operation`, `attempt`,
`method`, `route`, `outcome`, `status`, `error_class`, `at_ms`, `elapsed_ms` and
`snapshot`. Python repeats the strict schema independently before upload.
The legacy directory-v2 shape remains accepted only with its exact seven keys.
Unknown keys and invalid cross-field combinations fail closed.

- `origin=api-request`: `phase=directory-get` or `fixture-get`, GET only.
  An operation ordinal (1 through 1,000,000) groups one logical call; fixture
  attempts 1 and 2 share it, while directory GET always has attempt 1.
  Outcomes are start, response, or transport-error. A transport failure has
  null status; error_class is a fixed classification from the complete first
  error line (or the existing TimeoutError/deadline classification). Original
  messages/cause/call logs never enter events. Only the unchanged strict
  ECONNRESET rule authorizes a fixture retry; socket_hang_up does not.
- `origin=page-request`: fixed phase, attempt 0, one ordinal per request.
  GET/POST are recorded explicitly; all other methods become `other`.
  Read-only request/response/requestfinished/requestfailed listeners retain
  no request body, header or error message. HTTP 500 has status 500. Failure
  before headers has null status; failure after headers keeps the received
  status and error_class=page_request_failed. At most 256 in-flight known
  requests are tracked; new requests beyond that cap are not sampled.
- `origin=step`: test-start/test-finish/administrator-login/version-switch/
  source-select, method=none, route=unknown, attempt=0, null status, and
  error_class=none. Start/complete/failed describe the wrapped action only.
  A pre-action checkpoint is retained before the action can fail. Snapshots
  are one immediate, read-only page evaluation with a 100ms diagnostic wait
  ceiling, never locator waits or changed test/request timeouts. They expose
  only view (v2/v3/login/other), picker (visible/hidden/absent), source
  (loading/empty/radio-present/absent), and alert/permission-denied booleans.
  Closed/unavailable pages use only unavailable states and flags. DOM text is
  compared to fixed UI labels inside the page; no text, HTML, form values or
  source names are returned. Failed or delayed snapshots cannot replace the
  original action error. Listeners are removed in afterEach.

Ordinals are local to the test process and cease capture at exhaustion rather
than wrap. The bounded ring can omit the start of a retained operation;
absence of a matching start is not proof the request never began. Browser
monotonic timing is process-local, unrelated to Python's harness time; page
request elapsed time is not server processing time. No connection lifecycle
or server-side root cause is inferred from these client events.

Both baseline and fixed-engine user asset jobs validate only after failure
and upload exactly `.e2e-validated/diagnostics.json` only if validation succeeds.
Fixed artifact names use `safe-runtime-fixed-<suite>-<run>-<attempt>` to avoid
collision with baseline artifacts. Existing synthetic evidence stays unchanged.

Both inputs and the final output have a 65,536-byte budget. Browser evidence
keeps at most the most recent 256 records within a 61,440-byte event budget
(reserving room for the Python envelope); earlier records are
dropped so a late transport failure is retained. `browser_dropped` records the
number discarded (saturating at one million), so incomplete histories are
explicit. `collection_errors` can contain only the fixed `snapshot` value when
pre-cleanup evidence collection fails; cleanup still proceeds. Diagnostic file
write failures emit a fixed warning and preserve the original test outcome. Python phases are limited to the nine named lifecycle phases.
Times cap at 24 hours; exit statuses and HTTP statuses have explicit integer
bounds, with booleans rejected where integers are required. A failed artifact
validation is additional evidence failure, never a passing acceptance result.

## Collection controls and side effects

`ADTR_E2E_SAFE_DIAGNOSTICS` is set by the owned test harness to the fixed
browser event file. It is optional for standalone Playwright use; absent or
unwritable means no browser event capture. It is required only to retain these
extra request timing facts. Removing it does not change assertions or request
behavior, but makes browser evidence missing. It is removed from the API and
worker environment along with all other `ADTR_E2E_` variables. Synchronous
bounded file rewrites and optional 100ms-bounded snapshot reads add diagnostic
I/O and observation overhead only to instrumented tests. Without the variable,
page listeners and checkpoints are disabled.
The harness clears its two fixed input files at the start of each invocation;
for the messages job, the nested messages-bulk run replaces them with the most
recent suite's evidence. Each matrix job has its own isolated workspace.

The recurring directory-v2 socket failure has no verified root cause yet.
These diagnostics distinguish observed process death, recovered HTTP panic,
transport failure before a response, and time spent in authentication waits.
A connection idle timeout remains a hypothesis until a failed run provides
supporting transport and server-side evidence. This change does not claim to
fix the transient failure or change product behavior.

The historical directory socket hang up and v3-to-v2 source radio failure are
not yet diagnosed. The latter exhausted the entire test's 900,000ms budget;
that does not establish a 900-second radio wait. Before-cleanup snapshots had
running API/worker processes and no detected HTTP panic. New evidence can
separate missing HTTP responses, HTTP errors, and loading/empty/hidden/login
UI states; it cannot establish keepalive, rate limiting or process failure.


## Credential grant submit checkpoints (#141)

The first observed fixed v2-readers failure after #138 was main run
[38218794284](https://github.com/yunpiao/adtr/actions/runs/38218794284), job
[114709876817](https://github.com/yunpiao/adtr/actions/runs/38218794284/job/114709876817),
head `7e12fec40ac006ee8f36ed95251628977d8a3952`. Its grant submit's response
wait exhausted the **whole test** 900,000ms budget. That stack does not prove
how long this one wait ran, whether click settled, or whether a POST was sent.
The genuine safe artifact was validated/uploaded: ZIP 11733986150 has SHA256
`1fa115562ca3600f59b498d2d6e355757cc493c8183bf2967224b107d9ca4807` and contains
only `diagnostics.json`. It records four first-attempt fixture GETs, all 200,
with no dropped events; no page POST was sampled in that suite. API/worker
were alive at the before-cleanup snapshot, then exited 0 without a forced kill;
no HTTP panic was detected. Fixed evidence ZIP 11734535722 has SHA256
`7d0a531bd7b52deeee02463a30d186e9b0b7fc4b171df476103b2a6847556996`, contains
only provenance, and its 16 source hashes match that head. This verifies the
real failure-only artifact path, not the test's success or failure cause.

For the exact `/api/credential-use/grant` submit in v2-readers, the same strict
bounded recorder now adds `origin=step` phases `grant-wait` and `grant-click`.
Both require method=none, that exact route, attempt=0, null status/snapshot and
error_class=none. Their operation ordinal is shared; elapsed_ms is measured
from entry to the scoped submit. Outcomes are start/complete/failed. Only
`grant-wait` additionally accepts `registered`: the original wait callback has
returned its promise. This is not proof a request was sent or received. A
complete wait means its original promise resolved; complete click means its
original click promise resolved. Failed records contain no exception text or
classification and the original rejection object is preserved.

The wait callback is invoked before click, retaining the original matcher,
options and Promise.all result. Only during this submit, existing read-only
page listeners also record the exact grant route's request/response/finished/
failed lifecycle and safe status. They do not read bodies, DOM, form values or
headers; no semantic page snapshot is taken. HTTP failures retain their actual
status, whereas a failure before any response has null status. Unknown routes
are not sampled by this scoped observer. Neither request failure nor click
completion alone establishes why an expected response was absent.

No timer, timeout increase, retry, new interception or assertion is added.
The existing optional `ADTR_E2E_SAFE_DIAGNOSTICS` control remains required only
for evidence: absent means the original callbacks and Promise.all run without
listeners or checkpoint writes. Synchronous bounded evidence I/O adds small
observation overhead. Finally always deactivates sampling and independently
attempts listener removal; late rejected waits remain handled without changing
the original failed result or recording events outside the submit's scope.
The buffer, byte budget and dropped counter are unchanged. Root cause remains
unknown; this is a narrow evidence extension, not a claimed repair.

## Directory-v2 verification GET recovery

Only directory-v2 `readJSON` uses `directoryV2VerificationGET`. It makes at most
two immediate `request.get(path)` calls on the same context, without changing
arguments or adding native retries, timers, deadlines or cached responses.
The existing framework test budget remains authoritative. Do not reuse this
helper for arbitrary contexts with a finite per-call timeout: it does not know
or extend their original overall deadline. The existing `fixtureGET` retains
its separate 30-second and sanitized-error contract.

A retry requires an ordinary Error with the exact first line
`apiRequestContext.get: socket hang up`, or the existing fixed ECONNRESET forms
(optionally `read `, `write ` or `connect ` before ECONNRESET). Timeout and
cancellation/closed-context subclasses never qualify, even with reset-like
text. Unknown errors do not qualify. Any HTTP response is returned unchanged,
including 401/403/429/500; caller status assertions and JSON parsing stay outside
the retry catch. The actual terminal error object is rethrown, including a
second failure; no third request is sent.

Each actual call records start and response/transport-error under the strict
`directory-v2-verification-get` phase, attempts 1/2 and one shared operation
ordinal. Existing directory-get remains limited to attempt 1. Count distinct
operations with attempt-2 response events to count recovered transport reads;
that response may still be a denied HTTP status. A fixed
`Directory-v2 verification GET recovered: attempt=2/2` receipt also counts such
returns without adding identifiers or raw exception text. Diagnostic storage
keeps its existing bounded ring, byte cap and dropped counter.

Run 38259865516 attempt 2 failed before headers in 9ms after a successful grant.
Its validated artifact 11753466229 has ZIP SHA256
218362191675e1436e78565edae2f1661c0b69f35bf32c2c773ac5563ff950a3.
Matching-version local Go/Playwright probes reproduced a stale keep-alive reset
at accelerated idle boundaries; 26 observed first failures all recovered with
an immediate new-socket GET. Real 30-second idle probes did not reproduce.
The CI root cause remains unknown. This is bounded idempotent test verification
transport recovery, not a demonstrated root-cause fix. Its cost is at most one
additional read; preserving both attempt events prevents silently losing the
first transport failure. A disposed-context probe produced no second network
request, but does not establish exact CI framework teardown scheduling.
