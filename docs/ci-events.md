# CI event coverage

The same `engineering-ci / verify` job and all checks run for:

- Every opened, synchronized or reopened pull request, including drafts and stacked
  pull requests targeting another feature branch. There is no draft/base filter
- Pushes to main, preserving post-merge validation
- Merge-queue `checks_requested` events, if a queue is configured

Feature/docs branch pushes do not independently launch a second identical workflow.
Open a draft PR to validate an unpublished feature branch. Pull-request checkout
keeps the default merge ref, so tests validate the proposed merge result. No test,
permission limit, timeout, required-check name or failure condition is removed.
The event contract test also guards all real PostgreSQL/lifecycle/browser stages.

Repository branch rules are not modified by this change. Actual required-check
configuration and any merge queue settings remain repository-owner controls.

## Parallel real-browser acceptance

F39 adds an isolated audit/API/Worker/PostgreSQL browser suite. Browser suites run
in a matrix with at most two concurrent jobs and fail-fast disabled, so every
suite reports its outcome. Each job retains the 20-minute budget and uses its
own fresh database. The audit browser subprocess has a 600-second budget for
its additional real TOTP windows; prior suites retain 420 seconds.

The final check remains named `verify`. It always runs and depends on both
`contracts` and the complete `browser` matrix; failed, cancelled or skipped
dependencies fail the gate. No repository required-check settings are changed.
`contracts` still runs make check, real PostgreSQL migrations/concurrency and
API/Worker lifecycle acceptance. No previous browser suite was removed.

F52 adds a separate system browser matrix case with the same 20-minute job
budget and two-job concurrency bound. The harness passes only the PID of its
own synthetic worker; the test stops that process and verifies stale worker
status while authenticated API/PostgreSQL checks continue. No product process
control endpoint or monitoring timer fabricates this evidence.

F49 adds the maintenance browser case under the same bounded matrix. It waits
for real UTC grid occurrences and unused TOTP windows; its 480-second test runs
inside a 540-second harness budget, preserving the 20-minute job limit.

F01 adds a domains browser suite against an isolated synthetic LDAP container on fixed 389/636 ports. The same bounded browser matrix and final verify gate apply. Its ephemeral CA and credentials are test-owned; this is protocol integration evidence, not Windows/AD acceptance.

F03 adds the operations browser suite with a storage-only domain key and probes disabled. It exercises real API/PostgreSQL credential-registration lifecycle and committed-response-loss recovery, not LDAP or remote account administration. The browser timeout and concurrency gate remain bounded.
