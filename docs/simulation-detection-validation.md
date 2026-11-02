# Issue #259 — synthetic detection validation

Date: 2026-11-01 (UTC). This record concerns the detection working tree based on
`631dbe3f12c3b5ce24f0b2ce7fe2a54338c65b27`, including the reviewed current-schema
and reduced-auth-fixture test repairs. It is local engineering evidence, not a
published-head CI result or real AD/product acceptance.

## Local results

Commands ran from the repository root after sourcing
`../adtr-recovered-toolchains/dev-env.sh` (Go 1.27.1 linux/amd64; Node 24.22.1).

| Check | Result | Evidence |
| --- | --- | --- |
| `go test -race ./...` | PASS; 39 package pass lines (some cached) | `/tmp/issue259-final-go-race.log` |
| `go vet ./...` | PASS; exit 0, no diagnostics | `/tmp/issue259-final-go-vet.log` |
| `python3 -m unittest discover -s scripts -p '*_contract.py'` | PASS; 1,234 tests | `/tmp/adtr-simulated-detection-20261101-auth-contract.log` |
| `go test -tags=integration ./internal/auth ./internal/store -run '^$'` | PASS compilation only; no tests run | Independent review tool output |
| `go test -tags=integration ./internal/simulationdetection ./internal/auth -run '^$'` | PASS compilation only; no tests run | Independent review tool output |
| Dedicated detection workflow contracts | PASS; 7 tests | Independent review tool output |
| Dedicated detection Playwright config/spec strict TypeScript check | PASS | Independent review tool output |
| Dedicated detection Playwright `--list` | PASS discovery; one test | Independent review tool output |

The Python log was inspected independently; its owner confirmed the exact command,
completion at 11:06 UTC, and execution after the six fixture repairs and rollback-only
permission-vocabulary regression. Mocked negative-case diagnostics in that contract
log are not actual database/browser execution.

Final fresh web checks also passed (chained command exit 0):

| Command | Result | Evidence |
| --- | --- | --- |
| `npm --prefix web test -- --run` | PASS; 109 files, 4,980 tests | `/tmp/issue259-final-web-test.log` |
| `npm --prefix web run typecheck` | PASS | `/tmp/issue259-final-web-typecheck.log` |
| `npm --prefix web run build` | PASS; TypeScript and Vite build | `/tmp/issue259-final-web-build.log` |

These are DOM/unit, compiler and build results, not Playwright browser execution.
The build reports a bundle-size advisory; it does not fail the build.

## Findings fixed and independently rechecked

1. **P2 authorizer error classification.** The executor previously converted all
   authorizer errors into `authorization_revoked`. It now preserves infrastructure
   and context errors. Only explicit denial or an empty/mismatched admission epoch
   is a revocation verdict. The success validator returns infrastructure errors to
   `Engine.Finish`, whose transaction rolls back publication instead of recording a
   false terminal authorization failure. Injected sentinel, cancellation, deadline,
   explicit denial/403 and epoch regressions passed under the race detector.
2. **P3 rule/template version binding.** Mutation and historical version reads now
   share validation requiring each rule version to match its containing template.
   A correctly rehashed but version-inconsistent persisted body is rejected. The
   update/rollback fixtures deep-copy rules and use versions 2/3 without mutating
   the original version-1 declaration.
3. **Harness epoch expectation.** A grant change invalidates the old owner-scoped
   task epoch. The harness now expects 404 both after revocation and after regrant,
   separately verifies retained template read access, and submits a new-epoch task
   for the successful worker scenario. Production authorization was not relaxed.
4. **Shared test compatibility repairs.** Upgrade assertions still require the
   exact application schema, history/catalog preservation, readiness and no
   automatic grants. Python/TypeScript expectations come from the bounded,
   independently parsed checkout `version.go`, not runtime responses or environment
   variables. Evidence budgets and strict integer/bool rejection remain unchanged.
   Reduced auth fixtures accept current permission names; their rollback-only test
   stores explicit deny rows for every mark. Historical task/system fixtures remain
   unchanged. Independent focused runs passed 68 existing computer-export contracts
   and four new schema contracts; the four schema contracts also passed separately
   on the event, risk and detection trees.

## Runtime boundaries and outstanding validation

An actual PostgreSQL fixture command was attempted:

```sh
go test -tags integration ./internal/auth \
  -run '^(TestCurrentAuthFixturePermissionVocabulary|TestAccessCustomRoleCannotElevate)$' \
  -count=1
```

It **failed closed because `ADTR_TEST_DATABASE_URL` was absent**; all six vocabulary
fixture subtests reported this prerequisite. The log is
`/tmp/adtr-simulated-detection-20261101-auth-postgres.log`. This is an environment-blocked
runtime check, not a passing or skipped PostgreSQL test.

The dedicated detection PostgreSQL/TCP/worker/browser harness has not been executed
locally. Its workflow requires actual named Go integration passes with no skips,
real worker success and browser completion before reporting success. It uses an
owned disposable database, generated credentials, synthetic region screenshots,
and no retained login traces. The deterministic cancellation scenario is **queued
cancellation**; it does not establish detection-specific running-cancellation or
archive-before-execution race coverage. Existing engine fencing tests and reviewed
code do not substitute for those runtime scenarios.

The independent fixed-source reviewer refreshed only the three reviewed certificate
snapshots after the executor fix and reran both-tag boundary checks/vet. The report
is `/tmp/issue259-boundary-audit.txt` (P2 addendum): 306 default / 317 CI selected
sources. No guard, controller, allowlist, deadline, budget or CI-selection logic was
changed. This fixed-source audit is not dynamic running-cancellation proof.

Real AD/Windows behavior, production detection effectiveness and product acceptance
remain unverified; simulated business assumptions are not production acceptance.

## Reviewed source fingerprints

SHA-256:

- `internal/simulationdetection/executor.go`:
  `f981af9bf3fb00cf175002e570fd67c6cc4ce66141ac8106c525af3dbcac5474`
- `internal/simulationdetection/store.go`:
  `5b58e54b7b70609e4e5c714a522b9f951429164f04595ab326975b50852f02a9`
- `scripts/test_simulation_detection_integration.py`:
  `42835af97fe4b61208b4ea7ab3b3c9b63b1906af1703a21279c2130c301139fc`

Local `/tmp` paths identify session logs, not durable uploaded CI artifacts. Exact
published-head CI outcomes must be recorded separately.
