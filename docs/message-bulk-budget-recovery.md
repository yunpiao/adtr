# Bulk-message selection recovery after request-budget rejection

Engineering Issue #306; parent #115 / #50 / AD-F-158. Based on main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`.
This is an offline frontend correction, not product acceptance or a new bulk API.

## Existing requirement and reproduced problem

`message-bulk-workspace-contract.md` requires preserving cross-page selections
within one fixed result and, when selected IDs exceed the existing 32 KiB request
budget, rejecting the request while allowing the user to reduce the selection or
use fixed-result `all`. The 1000-target count bound does not promise that all
selected-ID requests fit the independent byte bound.

The old preflight correctly stopped before persistence/POST, but recorded this
local input rejection in `storageError`. That disabled the editing controls and
presented “重新读取批量记录” even when storage was working. A user could clear all
selections and start again, or discover that an unrelated storage reread happened
to unlock editing. The problem was misleading recovery and unnecessary loss of
selection work, not an oversized request reaching the server.

The DOM reproduction uses 1000 distinct 36-character synthetic UUIDs, five valid
200-item pages, current-page selection on each page, then preview/confirmation.
All snapshots pass the existing snapshot validator. The final full-workspace
case also goes through the real menu/query/page/bulk HTTP adapters with mocked
HTTP responses: 400 contract-valid 108-character IDs in two 200-item pages.
These are synthetic fixtures, not observed production data or browser/PG runs.

## Change and boundaries

The private `oversizedResult` state binds the byte-budget rejection to the
current `resultId`, independently of actual storage errors. It is necessary only
for the local oversized-selection recovery; removing it would conflate input
correction with storage recovery again. No configuration, route, persisted field,
authorization or backend behavior is added.

The rejected preview remains blocked. “返回调整批量选择” preserves all selected IDs
but clears the local budget error and old preview. This action performs no
storage read/write, query, operation persistence or POST. The user can then
remove individual/current-page selections or explicitly choose `all`, and must
preview and confirm again. The extra explicit return and fresh preview are the
visible interaction cost; the action does not silently change the target or
submit it.

A repeated unchanged oversized selection still fails the original 32768-byte
check before persistence. Successful smaller/all submission still rereads other
tabs' original intents, persists and reads back the exact original request, and
uses the existing API byte check. Storage failures, unknown operations, lost
permission and in-flight page reads cannot be bypassed with the new action.
Revocation clears the local failure; another result does not inherit its warning.
Focus/storage notifications do not falsely resolve a selection-budget failure.
Existing start/cancel/replay, progress, first-read times and fixed snapshot counts
are unchanged. No existing test, threshold or workflow was changed.

## Offline verification

Commands use the already verified Node 24.19.0 toolchain and existing locked
`node_modules`, with no installation or audit. Vitest uses `--pool=forks
--maxWorkers=2`.

- Baseline `message-bulk.test.tsx` and `message-read.test.tsx`: 106 passed.
- First four new recovery regressions against original production: four failed.
- Final new file: 12 cases covering page/single-item edits, `all`, fresh preview,
  repeated rejection, zero I/O on local recovery, quota failure before POST,
  focus/storage events, in-flight reads, new result, revocation, another tab's
  uncertain operation, StrictMode and the actual workspace/API chain.
- Final focused suite: 118 passed, including every unchanged original test.
- TypeScript and Vite build passed. Vite retains the existing >500 kB chunk warning.
- Scoped formatting of the new test/document passed. Whole-file Prettier on
  `message-bulk.tsx` fails on both untouched main and the candidate; the existing
  formatting debt is not silently counted as a pass or bulk-reformatted here.
- Requirements: 209 unique AD-F, 57 groups, 3258 fields, 804 controls passed.
- Full-suite, independent review and publication results are recorded in #306's
  evidence comment after they finish; this file does not predict their outcome.

The first full-workspace test attempted the five-page/1000-row fixture and hit
its unchanged 5-second test deadline twice. Those failure logs are retained. No
profiling established the precise timing cause. The final workspace test uses
two valid pages/400 longer IDs to exercise the same byte rejection and recovery
with less DOM work; the five-page/1000-UUID hook cases are retained. No timeout was
raised. Production behavior was not changed to accommodate the harness.

The first complete suite had 5200 passes and one new-test failure: after the
workspace's accepted POST, its normal read-only polling reached a receipt GET
omitted from the test transport. The retained DOM showed the prior queued state
and a new unknown-read status. The final transport now validates the original
operation ID and returns the same accepted receipt for that GET, and the case
explicitly exercises a further progress read while asserting only one POST.
The initial failure log is retained; no production polling behavior was changed.

No `npm audit`, `make check`, install, CI, PR, merge or deployment was run.
Actual browser/PostgreSQL, precise-head CI, integration of the independent
candidates and product acceptance remain open. Engineering Issue #306 stays
open and product acceptance remains 0/209. No APT/probe/R4/CI-selection or PR225
files were touched.
