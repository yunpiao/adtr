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
in a matrix with at most four concurrent jobs and fail-fast disabled, so every
suite reports its outcome. The four-slot bound is a measured-rollout canary;
authentication and fixed-engine matrices retain their two-slot bounds. Each job retains the 20-minute budget and uses its
own fresh database. The audit browser subprocess has a 600-second budget for
its additional real TOTP windows; prior suites retain 420 seconds.

The final check remains named `verify`. It always runs and depends on `contracts`,
the complete `auth-integration` matrix and the complete `browser` matrix; failed,
cancelled or skipped dependencies fail the gate. No repository required-check
settings are changed.
`contracts` still runs make check, non-auth real PostgreSQL migrations/concurrency and
API/Worker lifecycle acceptance. No previous browser suite was removed.

Authentication/HTTP PostgreSQL contracts use four deterministic shards, with at
most two concurrent jobs, fail-fast disabled and the same 20-minute job limit.
Every shard discovers the actual integration-tagged Go test names, then takes
its sorted round-robin partition. Tests, examples and fuzz seed cases are retained;
subtests run with their parent. Discovery errors, duplicate names and empty shards
fail. Each invocation keeps `-race -count=1 -timeout=10m`; password work factors and
test assertions are unchanged. Verbose output records the actual executed names.
The normal `python3 scripts/test_integration.py` and `make integration` entry points
run all non-auth packages and all four auth shards, even if an earlier group fails.

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

## Same-run image preparation

The original 34 jobs remain; required `prepare-postgres` makes 35. The 31
PostgreSQL consumers load the same-run immutable artifact after strict source,
digest, checkout and archive checks, then use the verified image ID without
pulling. `verify` additionally requires preparation success. The browser matrix now has a bounded four-slot canary; auth/fixed matrices
remain at two. A separate two-job workflow runs only on explicit
`ci/image-transfer-probe/**` branch pushes; it does not replace any full-CI gate.
Trust boundaries, parameters, evidence limits and measurement sequence are in
[ci-image-sharing.md](ci-image-sharing.md).
