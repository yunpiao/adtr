# Safe browser failure evidence

Refs engineering [#123](https://github.com/yunpiao/adtr/issues/123).

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
- `browser`: currently instrumented only in the directory-v2 acceptance spec.
  Each readJSON GET records start and either response status or the fixed
  `transport-error` class, without classifying exception text. Exact known
  pathnames become route templates; any other pathname is `unknown`, and all
  queries are dropped. TOTP wait start/completion and v2 grant completion are
  also fixed phases. `at_ms` uses the browser test module's own monotonic
  origin; it must not be compared directly to the Python clock origin.
  `elapsed_ms` measures that operation in this clock, not server processing.
  A response record proves headers/status were obtained, not successful JSON
  decoding or assertion completion. Transport-error rethrows the original
  error unchanged: there are no request retries or altered timeouts.
- `browser_capture`: `present` means the validator read the event file;
  `missing` means no event file was present (expected for other suites or an
  early startup failure); `pending` occurs only before upload validation.

Both inputs and the final output have a 65,536-byte budget. Browser evidence
keeps the most recent 256 records within its byte cap; earlier records are
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
bounded file rewrites add diagnostic I/O overhead to this test only.
The harness clears its two fixed input files at the start of each invocation;
for the messages job, the nested messages-bulk run replaces them with the most
recent suite's evidence. Each matrix job has its own isolated workspace.

The recurring directory-v2 socket failure has no verified root cause yet.
These diagnostics distinguish observed process death, recovered HTTP panic,
transport failure before a response, and time spent in authentication waits.
A connection idle timeout remains a hypothesis until a failed run provides
supporting transport and server-side evidence. This change does not claim to
fix the transient failure or change product behavior.
