# Bounded eight-slot CI validation

Refs [#139](https://github.com/yunpiao/adtr/issues/139). This is a new candidate for independent
measurement, based on the independently reviewed 50-job diagnostics/group-assets head
`cc5a79170e7bb8389a11406b589bd59cd8ca4a6c` (PR #138, tree
`5bf0623e0012c370c9166687bfe69c2e7e78b68b`). Its own four-slot 50-job run is
pending; the inherited group-assets head `a42645f39ecde4342f4359f4b9b790fbe3ce7dc8`
had already passed 50 jobs. Four-slot success in #97 or #177
is not evidence that eight slots pass or improve runtime.

## Scope and parameter

Only `browser.strategy.max-parallel` changes from 4 to 8. It caps simultaneous
jobs within that matrix and targets time waiting for a browser slot. Eight is
not functionally required by the application or tests; the explicit bound is
required for this controlled resource budget. Returning to 4 keeps the tests
and restores the smaller concurrency allowance. Deleting `max-parallel` does
not restore 4: it removes this matrix-specific bound and may cause a larger
burst. Both changes require a new reviewed head rather than a silent rollback.

Auth and fixed-engine matrices remain 2. The potential within-run overlap is
15 standard runners: 8 browser + 2 auth + 2 fixed + 1 contracts + 2 native
jobs, versus 11 before. Preparation/native startup is at most 3; verify follows
all dependencies. This is a workflow upper bound, not an account capacity
measurement. Other PR/main runs can overlap. Actual scheduling can provide
fewer slots and add queueing.

All 50 jobs, suites, matrix order, commands, dependencies, success guards,
all original per-job timeouts, fail-fast false, Playwright workers 1/retries 0,
permissions, pinned actions/images/browsers, cache settings and artifact
retention remain unchanged. More simultaneous runners/downloads can increase
instantaneous transfer or service contention; per-job isolation and immutable
same-run image verification still apply. Diagnose any new failure before
attributing it to concurrency. Preserve original failures and retry history;
an unchanged retry passing cannot establish the original cause.

## Strict scope contract and future maintenance

`test_ci_parallelism_contract.py` restores only browser 8 to 4 in memory and
compares SHA256 with the exact baseline workflow bytes. It therefore guards
all fields, ordering and uploads, not only selected strings. This is the
current canary's reviewed-scope guard, not a permanently frozen workflow.
Future legitimate suites, diagnostic uploads or integration changes will
fail it. Review those changes, choose and record the new trusted baseline,
update the explicit hash/expected counts and revalidate the combined head.
Do not remove or bypass the guard to integrate another change. In particular,
PR #138's diagnostic upload changes have been explicitly incorporated here:
the reviewed baseline hash is now
`46476c8dc9cc49cd589eddfd1319fa96aa4192fc0bc9bd165e10e0e59d454323`.
Only the browser cap differs from that baseline; its failure-only diagnostic
upload, seven fixed suites and all original evidence gates remain intact.
The combined candidate requires its own independent review and exact-head CI.

## Evidence and interpretation

Run `python3 scripts/replay_ci_parallelism.py` from the repository root.
The committed [input](evidence/ci-eight-slot-model-input.json) preserves run IDs,
head, workflow digest, job IDs/links, timestamps and declared matrix orders from
GitHub API snapshots. #175 (46 jobs) models 57m02s → 30m44s; #177 (50 jobs)
models 62m12s → 34m14s. Observed four-slot wall times were 57m19s and 62m31s;
the idealized model excludes dispatch gaps. The unchanged #177 fixed-matrix
work alone models 32m54s, before preparation and final verification.

The offline replay keeps each measured job duration and original matrix order,
uses auth/fixed caps 2, waits for preparation and then every verify dependency.
It assumes independent runners, zero additional dispatch delay and no new
contention. Model results are projections, not observed eight-slot timings.
The fixed-engine tail can become critical, so halving total wall time is not
guaranteed. #175 is an older smaller job set; #177 is the historical 50-job group-assets
run and predates PR #138 diagnostic additions.

A normal draft pull request triggers one complete candidate run. Record exact
head and merge checkout, attempt, all 50 individual outcomes, total elapsed,
browser span, actual peak, runner execution and dispatch/wait separately,
fixed-engine tail and source/image artifacts. Compare with #177 while noting
hosted-runner and cross-run contention. Unknown platform queue causes do not
prove either success or failure of the concurrency hypothesis. Local Docker
availability and local versus hosted validation must remain explicit.

Publication remains draft until independent review, exact-current-head full CI,
current-main compatibility and dependency gates are satisfied. Merge and
post-merge validation are coordinated separately; no deployment or product
acceptance is implied.

## Runners and cost boundary

Every workflow job remains `ubuntu-24.04`, a standard hosted runner, in the
public repository. GitHub documents standard hosted compute for public repos
as free; larger runners have separate charges. This makes no blanket claim
about artifact/cache storage. No billing, account-wide concurrency, paid
runner, retention, credentials or permission setting is changed.

Official references:
- [Matrix concurrency semantics](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/run-job-variations#defining-the-maximum-number-of-concurrent-jobs)
- [Actions billing](https://docs.github.com/en/billing/concepts/product-billing/github-actions)
