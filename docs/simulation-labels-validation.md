# SIMULATED sensitive-label vertical: dedicated acceptance

Tracks #38 (AD-F-045/046/047) and #253. This is synthetic engineering, **not AD/Windows product acceptance (0/209)**. `simulation-labels` CI is independent of the frozen engineering browser inventory. It checks the exact PR head, full-SHA pinned actions, Go 1.27.1 / Node 24.19.0, and owned disposable PostgreSQL; it never connects to real AD or sends external notifications.

## Independent oracle

The setup-only Go producer calls actual `simulationlabels.SeedFixtureTx` after the current schema gate. Three trusted observations contain identical `g`, `nested`, `u`, `c` identities: O1 has g→nested→u; O2 has g→nested→c; O3 repeats O2 but explicitly lacks complete coverage. Names are synthetic, description unavailable. Server computes the fixture hash. No direct evaluation/comparison/alert inserts, runtime upload endpoint or startup seed are used.

A g-sensitive policy must yield exactly two O1→O2 persisted in-app alerts: c added and u removed, distance 2. No selected groups yields a successful zero-alert comparison. Incomplete O3 and changed policy revisions must reject comparison, never pretend to establish an asset removal. Honeypot is null/unavailable, never false. API assertions additionally cover unmatched filter zero, CAS/create replay/conflicting reuse/archive/restore/history, same-actor receipt recovery, independent grant denial, live read/write/domain revocation, tenant isolation and API process restart persistence.

The UI lists use bounded page windows and lazily load pinned details. This small browser fixture has fewer than 20 records per list; it does not establish a runtime capacity or cross-page browser acceptance claim. Unit/source contracts cover limits and asynchronous state guards separately. The incomplete browser comparison awaits both actual detail GET responses and asserts their complete/incomplete flags before checking the disabled action.

The standalone browser actually creates a policy, commits then drops its HTTP response, reloads and recovers the original operation receipt. It evaluates pinned observations through UI, checks unknown coverage and unavailable honeypot/provenance, generates stored alerts, and reloads their persisted detail. There are no fabricated success responses. Screenshots cover only synthetic alert output; traces/video are off and runtime secrets are stripped from Chromium's launch environment.

## Commands and evidence

```
python3 -m unittest discover -s scripts -p test_simulation_labels_workflow_contract.py
go build -o bin/adtr ./cmd/adtr
npm ci --prefix web
npm run build --prefix web
cd web
npx --no-install tsc --noEmit --strict --target ES2022 --module ESNext --moduleResolution Bundler --skipLibCheck simulation-labels.playwright.config.ts simulation-labels-e2e/labels.spec.ts
npx --no-install playwright install --with-deps chromium # dedicated CI only
cd ..
python3 scripts/test_simulation_labels_integration.py
```

Five top-level PostgreSQL gates: `TestSimulationLabelsHTTPAuthorityCASAndReceipts`, `TestPostgresLabelsLedger`, and schema28 `TestSchema28UpgradePreservesHistoryAndGrants`, `TestSchema28FailureRollsBackPermissionAndTables`, `TestSchema28ConcurrentUpgrade`. The ledger runs once, with four additionally required nested passes: `TestPostgresLabelsLedger/Identifier128SeedPolicyDetailAnd129Rejection`, `/OversizedPayloadsLeaveNoWrites`, `/ScopedCompositeFKRejectsTampering`, and `/ConcurrentCreateAtFixedPolicyQuota`. These cover ID bounds, rejected oversized writes leaving no records, scoped composite-FK rejection and serialized quota concurrency; their presence/compile checks are source evidence until actual dedicated PostgreSQL runs. The runner requires each exact parent and full nested named pass and rejects missing/any skipped test; exit 0 alone is insufficient. Failed Go tests expose only allowlisted source filenames/line numbers, SQLSTATE and exit code—not raw database rows, DSNs or logs. `t.Helper()` preserves actual failing callsites.

`artifacts/simulation-labels/result.json` reports the exact git head, simulation marker, status and completed checks. An absent Docker executable produces nonzero exit and `status: not-run`, not a skip/pass. Containers are removed only after their random ownership label is verified. Credentials are ephemeral and raw API logs stay in an unpublished temporary file.

Local 2026-11-02 validation: Go labels/auth baseline passed; 10 harness contracts passed after correcting the initial workflow-filename test typo (`engineering-ci.yml` → actual `ci.yml`); trusted Go fixture compiled; strict standalone TypeScript and Playwright discovery passed (1 test). Actual Docker/PostgreSQL/browser acceptance is **not run locally**: Docker is absent, harness exits nonzero with checks empty. Independent source review caught a grant-fixture CHECK violation before runtime: read revocation now clears both read/write atomically and restoration restores both; a regression contract enforces it. Exact-head dedicated CI remains required before merge. Source contracts and browser discovery are not runtime acceptance.

## Final integrated local review, 2026-11-02

The independently reviewed candidate passed full Go race tests (count=1) and vet,
1,264 Python contracts, and 112 frontend files / 5,093 tests. Frozen labels-focused
43 tests, TypeScript and production build passed. Earlier type/fixture failures
and an aggregate run overlapping shared navigation edits were preserved; these
are corrected/repeated runs, not assertions that the first attempts passed.
The bundle-size advisory remains.

Independent review found and rechecked corrections for identifier length parity,
complete recovered-intent fingerprints, serialized write/envelope budgets,
bounded lazy UI pagination, a policy-selection loading race, and a test grant
CHECK conflict. The page-too-large error is distinct from other limits and is
the only condition allowing a bounded page-size fallback. All persistent
payloads and receipts are checked before business writes/audit; a conservative
JSONB text-spacing bound is included. The 25.6MB escaped comparison reproduction
proves Go/HTTP serialization growth, not an observed PostgreSQL wire overflow.

The independent source audit reviewed 337 default / 348 CI production files and
74 wiring entries, then updated only three source certificates. Both-tag
boundary/vet and source hash stability passed. No guard, allowlist, existing
budget, credential, deployment or old CI selection changes were made.
Actual new-feature PostgreSQL/API/browser acceptance is still pending the
dedicated exact-head run; tagged compilation and test inventory are not runtime
passes. Original real AD/Windows/capacity requirements remain unaccepted.
