# SIMULATED DNS/IP validation

This is fixed, synthetic observation acceptance, not live DNS discovery, AD synchronization,
production capacity evidence, or product acceptance. No date filter is advertised.

## Dedicated checks

Run `python3 -m unittest discover -s scripts -p test_simulation_dns_ip_workflow_contract.py`
for static wiring and independent workbook-oracle mutation checks. Run the dedicated
`.github/workflows/simulation-dns-ip.yml` on the exact PR head for actual acceptance.
The runtime requires the built API/UI, Docker, PostgreSQL, and Playwright Chromium;
a missing prerequisite is a failing **not-run**, never a passing skip.

The harness creates only an owner-labelled disposable PostgreSQL container with a
loopback published port and separate loopback API/Worker listeners. It ignores inherited
ADTR/PG settings, generates temporary credentials, and checks container ownership before
cleanup. Trusted Go fixture seeding uses `SeedFixtureTx` and the current schema check;
it does not forge success rows or write directly into task state. The fixture has 38 rows:
35 IPv4 hosts, one IPv4-mapped IPv6 input normalized to IPv4, one IPv6 row, and a second
address for the first DNS name. Uppercase/trailing-dot DNS inputs are normalized.

HTTP acceptance combines both pages against an independently constructed expected list;
then the real asynchronous Worker exports **all 38 matching rows**, not the current page.
It checks source hash/filter binding, owner/tenant isolation, CSRF, unknown fields,
response replay, queued cancellation, API/Worker restart, permission revocation and old
permission-epoch rejection after regrant. Separate exports cover IPv6-only and negative
keyword/header-only results. Request task identity is `taskId`; task response identity
is `taskUUID`.

Every downloaded workbook is opened as ZIP/XML. The independent oracle checks all eight
column names and positions, every cell/row/order (including first and last), literal
inline strings, SHA-256 and byte count, and absence of formula, external-link, macro and
unsafe ZIP path content. A successful task status alone is insufficient. The browser
performs a real download, parses it with the same independent oracle, loses only the
actual submission response via `route.fetch`/abort, recovers the original receipt, and
verifies byte-identical download after reload/reselection. The synthetic result screenshot
uses a naturally visible small region and asserts geometry below the sticky header;
it does not hide or restyle the interface.

## Required named PostgreSQL inventory

All nine must be discovered and pass, with zero skips. The static inventory check matches
the backend's actual test declarations; runtime JSON parsing rejects absent names/skips.

- `TestPostgresDNSIPWorkerDownloadAndReplay`
- `TestPostgresDNSIPIsolationQuotaAndAudit`
- `TestPostgresDNSIPRejectedFinalizationAndCancellation`
- `TestPostgresDNSIPSourceTamperingAndIntegrity`
- `TestPostgresDNSIPFenceAndForgedSuccess`
- `TestSimulationDNSIPHTTPWorkerDownloadAuthorityAndRevocation`
- `TestSchema31UpgradePreservesHistoryAndGrants`
- `TestSchema31FailureRollsBackPermissionAndTables`
- `TestSchema31ConcurrentUpgrade`

A failed finalization may retain an unpublished immutable candidate under quota; it must
never be downloadable without a current-authorized succeeded task. This is not cleanup
or deletion acceptance. The task kind remains unscheduled.

## Local evidence (2026-11-02)

- Static contract/oracle suite: **9 passed** after correction. The first run caught an
  overlong role ID; the corrected role is exactly 24 ASCII characters.
- Review caught request `taskUUID` versus required `taskId`; requests were corrected and
  a route-schema regression added, while response `taskUUID` stayed unchanged.
- Strict TypeScript compile: **passed**; browser discovery: **1 test**.
- Trusted Go fixture program: **compiled** against the implementation.
- Actual Docker/PostgreSQL/TCP/browser run: **not-run**, exit 1, because this cloud
  workspace has no Docker executable. No PostgreSQL or browser success is claimed.
- Current-head remote CI and product acceptance: **pending**.

CI artifacts contain only closed-schema result JSON and an explicitly named SIMULATED
result screenshot. Raw logs, credentials, browser traces/videos/storage state and workbook
copies are not uploaded. Safe failure diagnostics contain only allowlisted source
locations, SQLSTATE and exit status. Local failure/retry logs remain available to the
coordinator; passing static checks do not erase earlier failures or replace runtime proof.

## Candidate review and local verification (2026-11-02)

Independent review found and rechecked three corrections: fresh-session cancellation
receipt recovery; E2E request taskId versus response taskUUID; and the auth PG fixture's
incorrect raw equality of first submission and replay (the replay flag intentionally
changes). The corrected test checks typed receipt identity and exactly one task,
receipt and audit. Final PostgreSQL inventory includes all nine tests, including
fencing/forged-success rejection. The final fixture grants only the specified synthetic
role; anonymous/admin/cross-domain/owner/revocation assertions remain intact.

Full Go race/vet, final UI 115 files/5172 tests, typecheck/build, 209-record requirements
integrity and formatting/diff checks passed. Independent source-boundary audit admitted
367 default/378 CI sources and the unchanged 74-member wiring set; only three digest
certificates changed, not the checker, old guards, budgets or CI selector.

Inherited exception acceptance failed on its parent PR before browser setup because
Playwright APIRequestContext omitted Origin on source POSTs. The independently reviewed
two-file fixture correction supplies the current page origin with existing CSRF and
keeps the required 200 assertion. It does not relax production authorization. This
candidate incorporates that correction; the parent's original failed evidence remains
separate, and actual corrected browser execution is still a remote CI gate.

The initial local Python aggregate passed1289 tests; the final aggregate is rerun after
that inherited correction and final fixture scope update. PostgreSQL/browser attempts
remain explicitly not-run locally because Docker is absent. No product acceptance or
complete source-system/date/capacity equivalence is claimed.

Final local aggregate after inherited Origin correction and final fixture update:
1290 contract tests passed in147.764 seconds. Final source also passed full Go race/vet,
115 UI files/5172 tests, final typecheck/build, requirements integrity and diff checks.
The current draft may initially show the two inherited Origin-fix files until its
parent exception branch receives the identical reviewed correction; those bytes are
included in this tested candidate, not omitted while waiting on parent publication.
