# SIMULATED dashboard validation (Issue #60)

## Evidence boundary

This suite validates a synthetic observer of existing producers, not production
AD acceptance. `simulation=true`, `productAccepted=false`, and unavailable
producer metrics must remain explicit. Missing asset, baseline, vulnerability,
password, compromise or blocking data is not a zero measurement. No live AD
connection, deployment or scheduled task is used.

## Dedicated acceptance pipeline

`.github/workflows/simulation-dashboard.yml` checks out the exact PR head (or
push SHA), builds the API and locked frontend, installs CI-only Chromium, and
runs `scripts/test_simulation_dashboard_integration.py`. Actions and the shared
PostgreSQL image are pinned; credentials are generated for a disposable owned
container bound to loopback. Caller database and AD environments are excluded.
Existing engineering-ci browser selection is unchanged.

The harness migrates twice and verifies the current schema. Trusted
`simulationevents.SeedFixtureTx` produces two events and two cases per isolated
tenant/domain. The independently specified oracle is:

- two current-window events: one login failure, one service access;
- two distinct actors, one observed device (not an asset inventory);
- two unmarked, initially open cases;
- actor risk 5 and browser-actor risk 15 under the existing example mapping;
- zero closes initially, then exactly one through HTTP, then two after the
  browser drives the second real case transition;
- both real risk resets yield score 0 and revision 2;
- a real manual detection submission is executed by the actual worker on a
  separate listen port; its category-exists rule matches exactly two events.
  No successful task row/result is inserted by the fixture.

GET summary is checked for anonymous/admin/domain denial, malformed/duplicate
query rejection, rejection of client actor override, and live conjunction of all
three read grants. HTTP close idempotency, cross-tenant separation and API
restart persistence are checked before the browser runs.

These actual PostgreSQL tests must each execute and pass (missing tests and any
skips fail acceptance):

- `./internal/auth`: `TestSimulationDashboardHTTPGrantScopeSessionAndNoMutation`
- `./internal/auth`: `TestSimulationDashboardRechecksSessionAfterIdentityLock`
- `./internal/simulationdashboard`: `TestPostgresDashboardSnapshot`

The separate Playwright config runs one dashboard spec. It logs in through the
real API, resolves the saved source, verifies counts and unavailable indicators,
checks a deliberately injected 503 retains the old snapshot and permits retry,
then uses real producer APIs to close/reset and refresh. It verifies actor-bound
response headers, reload and route-back persistence. Only cropped synthetic
summary screenshots are intentionally saved; trace/video/automatic screenshots
and raw server logs are not uploaded.

## Local evidence, 2026-11-02

- Seven dedicated workflow/harness contract tests passed after review fixes for
  the complete three-kind oracle and the closed detection-rule request DTO.
- Strict standalone TypeScript check of the config and spec passed.
- Playwright discovery listed exactly one test in one dedicated file.
- Attempting the real harness exited 1 because Docker is unavailable in this
  dot environment. `artifacts/simulation-dashboard/result.json` records
  `status: not-run`, with no completed acceptance checks. The traceback is
  retained in the execution transcript/local diagnostic log.

Therefore real PostgreSQL/TCP/browser acceptance remains **pending dedicated
CI on the submitted revision**, not locally passed. Contract tests establish
wiring and safeguards; discovery establishes inventory, not browser execution.
An eventual CI retry pass must be reported alongside its original failure.
CI publishes only `artifacts/simulation-dashboard/` for seven days, keyed by the
exact tested SHA. Merge requires independent review and all current-head checks;
main must then be verified separately.

## Independent review and local regression

Independent review identified two test-oracle defects before CI publication:
missing explicit `login_success: 0` and an invalid detection-rule request DTO.
Both were fixed in harness/tests only, with AST regressions; independent re-review
found no outstanding P1/P2 finding. Production authorization, SQL snapshot,
source limits, result re-evaluation and UI stale-scope handling were reviewed.

The final frontend run passed 110 files / 5,011 tests, with 31 dashboard tests;
typecheck and build passed. Earlier typecheck and strict-fixture failures were
preserved and fixed before that run. The bundle-size advisory remains.

The first full Python contract invocation failed one generated-case-map test
because its shell selected the unrelated system `go` program. It ran 1,243 tests
and ended with one error; this is not a passing run. A fresh complete run with
verified Go 1.27.1 was started, with separate failure/retry logs retained.

Final whole-repository rerun with Go 1.27.1 passed: 1,245 Python contracts in
151.950 seconds; full `go test -race -count=1 ./...` and `go vet ./...` passed.
Independent selected-source audit reviewed 312 default / 323 CI Go sources,
then refreshed only three source certificates; both-tag boundary tests and vet
passed. Original failures remain in separate logs. No actual local PostgreSQL
or browser acceptance is claimed by these successes.
