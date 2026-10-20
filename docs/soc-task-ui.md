# SOC production task workspace

Refs [engineering #93](https://github.com/yunpiao/adtr/issues/93), [F48 #12](https://github.com/yunpiao/adtr/issues/12) / AD-F-189, [design #87](https://github.com/yunpiao/adtr/issues/87), and merged [prototype PR89](https://github.com/yunpiao/adtr/pull/89). Base: main `a8cc62aab884292e2747704968a7f9fe988c719a`, following production shell PR92.

## Design and actual data

The actual prototype workbench, desktop investigation and 390px mobile investigation images were inspected from [prototype QA artifact 11668832031](https://github.com/yunpiao/adtr/actions/runs/38048701866/artifacts/11668832031). The task slice adapts its compact queue, stable object identity, factual summary and vertical timeline. It does not import prototype runtime code or sample threats into production.

The queue uses only existing `GET /api/tasks` filters: exact task kind, domain ID, local state, visibility and bounded page index/size. Counts come from the server's permission-filtered pagination, not a current-page estimate of global states. The collapsible filter panel separates draft controls from applied conditions. Desktop rows become mobile cards with the same task identity and actions.

The full execution detail uses `GET /api/tasks/detail`, fixed task UUID, current state/error/progress and the ordered persistent events already returned by the server. Each event displays its actual ID/action/state/attempt/result version/time. The API has no event actor or per-event error cause; the UI does not invent either. `finished` means a business attempt ended and can accompany `retry_wait`, not only a terminal task. `cancel_requested` means the cancellation request is registered, while the displayed resulting state establishes whether stopping has been acknowledged. The existing API bounds event lists at 1,000 and returns `result_too_large` beyond that; the UI does not truncate or invent event pagination.

No new full-text search, sort, time filter, multi-state filter, global aggregate, backend endpoint, executor, database migration or permission is introduced. Platform health success only establishes that executor's database/queue check. Broader detection/business executors, source parity, real AD/eight Windows versions, capacity and product acceptance remain separate requirements.

## Navigation and identity contract

`task-view-state.ts` projects an exact, versioned `history.state` namespace containing the current actor's ID and username, the exact corresponding hash, a known view, applied list query and a bounded return offset/task ID. It stores no task/event response, credential, CSRF/session token, proof, mutation draft or input payload. Browser state is untrusted: unknown fields, invalid object shapes, account mismatch, hash/view mismatch, invalid UUIDs, state/visibility/page bounds and invalid filter encoding reset to the safe default list. A raw or copied detail hash without its matching account-bound state also falls back to that list.

The query bounds follow the existing API: pageIdx 1–1,000,000; supported UI page sizes 10/20/50/100; domain ID at most 128 allowed ASCII characters; taskName at most 64 UTF-8 bytes; one known task state; active/default, archived or all visibility. Invalid draft filters remain editable with an explicit notice instead of silently changing the applied query. Clearing also resets unapplied draft fields when the applied query was already empty.

Opening a task records the applied list entry's task ID and scroll position, then creates a separate detail history entry. Returning preserves the applied filters/page, restores a still-present row's focus and scroll, and discards unapplied drafts. Detail entry starts at the top and focuses its heading once; polling does not steal focus. Restoration never substitutes cached task data for a server read.

App already revalidates `/api/auth/me` and remounts the workspace on browser history navigation. TaskWorkspace additionally keys its internal lifetime to actor ID, username and current CSRF token, so old reads and proof forms cannot survive an identity/session replacement. Refresh and Back/Forward may restore only read views. Submit/archive/maintenance-confirm reset to list; schedule-create resets to schedules. Existing actor-scoped unresolved-operation storage remains the only source of an explicit recovery/reconciliation entrypoint. The navigation module neither replays nor forgets these operations.

If history APIs are unavailable/rate-limited, the current clean in-memory view remains usable; refresh/history restoration is then unavailable. There is no localStorage/sessionStorage fallback for navigation that could mix entries or accounts. History binding controls presentation only, never authorization.

## Permission and existing action boundaries

Server menu plus exact operation checks remain mandatory. A restored detail cannot fetch without `GET /api/tasks/detail`; a restored archived selection or archived/all list query cannot read until the current tasks-write/task_archive-read gate allows that visibility. Denied saved visibility offers an explicit return to default-visible tasks. A 404 active lookup does not automatically retry with `visibility=all`.

All submit, cancel, recover, schedule, archive, restore and unconfirmed-action controls retain their existing proof/idempotency/permission and server-write contracts. Dedicated audit, connection, directory and runtime-log entrypoints retain their special restrictions. Generic task APIs still redact private result/cursor metadata. The original UI restrictions covered directory v1/v2; the later five-kind presentation gap and its candidate repair are recorded below. Task events remain execution history, not a claimed AD threat-evidence chain.

### 2026-10-14 specialized-task controls candidate

Refs [F48 #12 / AD-F-189 issue-first record](https://github.com/yunpiao/adtr/issues/12#issuecomment-6180459935), [#26](https://github.com/yunpiao/adtr/issues/26) and [#30](https://github.com/yunpiao/adtr/issues/30). Baseline main is `7140933be2459dbfc4b824aa6d6d6c46a9f4f8da`, tree `0a770734095ea8561f872c74e1234dba21e619f2`; its 1,084 source blobs and file modes were checked before the candidate edits. This is a separate candidate checkpoint; historical verification below does not establish this changed UI.

The confirmed defect is an incomplete frontend kind classification. The generic workspace recognized directory v1/v2, but omitted these exact dedicated kinds:

- `domain.directory_read.v3`
- `domain.directory_read.v4`
- `user_assets.v3.export`
- `group_assets.v3.export`
- `computer_assets.v3.export`

The backend already rejects their generic cancel/recover requests with `directory_route_required` or the corresponding asset-export route-required error. Nevertheless, the old UI offered generic actions/proof forms and displayed redacted result/cursor metadata as ordinary results. The candidate extends exact-kind classification, suppresses generic cancel/recover controls and their password/MFA forms, and hides private result/cursor fields for all five kinds. It retains the actual task UUID, state, progress, error, persistent events and refresh. An internal `TaskAction` guard also blocks a generic write if a stale or programmatic action reaches the component. This frontend guard complements the existing server boundary; no backend, schema, permission, policy or CI-job change is included.

After the current detail read identifies a dedicated kind, the candidate clears only an old generic recover intent matching both the current actor and task. It does not clear another actor's intent, another task's intent, or a dedicated export recovery record; it does not replay a write. Refresh, Back/Forward, delayed reads and session replacement must preserve the existing current-account/read-only navigation boundary.

Navigation describes the destination's actual capability:

- Directory v3 links to `#directory-v3`, the existing dedicated directory workspace
- Directory v4 links to `#group-members-v4` to read saved results only; this page has no collection/cancellation UI, so those operations still require the dedicated API
- The three asset-export kinds link to `#user-assets-v3`, `#group-assets-v3` and `#computer-assets-v3`, respectively, where their dedicated export panels exist; the link does not promise to select or resume an arbitrary task ID

Existing authorized health/audit/domain-connection actions, directory v1/v2 restrictions and runtime-log restrictions remain separate kind-specific contracts. A task's state and event history retain their existing meaning; suppressed generic controls do not rewrite a task or imply collection, cancellation, recovery or download success.

Candidate test coverage targets all 45 combinations of five exact kinds and nine task states, private-field/action/proof negative assertions, zero generic mutation requests, actor/task-scoped intent handling, stale/session/navigation boundaries and the existing legitimate-action regressions. Existing actual-task browser suites are being extended rather than adding CI jobs. At this checkpoint the candidate's DOM/runtime tests, formatting, typecheck/build and real browser/API/Worker/PostgreSQL execution have not run locally because of the execution restrictions. Exact-head verification must run the unchanged 71-job matrix; independent review, publication, merge and separate main-push verification remain pending. Test source and static checks alone are not runtime evidence.

The explicit offline Python allowlist passed 19 existing tests: synthetic membership-evidence validator units, membership runner contracts and directory-v3 static contracts. The complete 209-feature requirements mapping and `git diff --check` also passed. An audit hook denied subprocesses, network and signals. These results do not execute the 105 added DOM cases or the 11 added real-browser checkpoints.

AD-F-189 retains `implemented_verified_slice` for its previously verified narrower slices; this candidate does not upgrade that status or replace the PR72/77/94 evidence. Issue #12 remains open and product acceptance remains 0/209. Complete source-state compatibility, `engineRestart`, capacity/retention, real AD/Windows and full product acceptance remain open.

## Verification and open gates

Unmodified base `make check` passed: 60 Python contracts, Go race/vet/build, 33 frontend files / 1,043 tests, TypeScript/build and dependency audit with zero vulnerabilities. The final candidate `make check` also passed: the same 60 Python and Go race/vet/build checks, 36 frontend files / 1,298 tests, TypeScript/build and dependency audit with zero vulnerabilities. Full formatting, discovery of all 31 existing Playwright tests and the unchanged prototype's 27 static/VM checks passed. These checks do not substitute for executing those browser tests. Independent review separately passed the 52 existing task/maintenance tests and 255 new navigation/presentation tests, corrected the backend cancellation action label and attempt-level `finished` wording, and found no remaining confirmed source blocker in the reviewed snapshot. The navigation validator and UI regression tests cover malformed/unowned history, read-only restoration, query/draft separation, page/selection return, identity/session races, exact permissions and existing mutation paths. Independent actual-diff review and final candidate results are recorded on the linked PR.

Local container integration, lifecycle and the real tasks browser harness were actually attempted but cannot start because this cloud executor has no `docker` executable. A local Chromium launch was also attempted and failed at the operating system's `socket() ... Operation not permitted` boundary. No local DOM/unit result is called a PostgreSQL/browser pass. The unchanged full CI matrix is responsible for exact-commit real API/Worker/PostgreSQL and all existing regression suites.

The tasks browser suite retains its original real execution, same-key idempotency and read-only role assertions. It adds browser-originated GET response checks for filtered list/detail, reload and Back/Forward, actual event-by-event timeline assertions, desktop 1440×900 and mobile 390×844 queue/detail captures (first-screen and full-page), no-page-overflow and first-real-task geometry. A success-only artifact contains eight safe screenshots plus source/commit/runtime/image hashes and real read/role evidence. Automatic proof-bearing failure screenshots/traces are disabled for this suite; explicit captures require proof inputs to be absent. Existing timeouts, retry limits, CI matrix/max-parallel and verify gates are unchanged.

These are engineering gates. This slice does not close F48 #12 or design #87; product acceptance remains 0/209. No production deployment, real AD access or credential/security-setting change is part of this work.

### Visibility regression found by the full native matrix

Initial head `747703a` passed the real tasks suite and its eight-image independent review, but CI 38072753448's unchanged maintenance suite exposed a real presentation regression: after a successful restore receipt, `默认可见` existed only inside the collapsed metadata disclosure. The native visibility assertion failed at maintenance.spec.ts:427; this was not an infrastructure failure. The repair places current visibility beside the fixed task identity/state and removes the duplicate collapsed field. The maintenance assertion and its timeout remain unchanged; two DOM cases cover active/archived visibility with metadata closed, and the tasks native suite now also requires the visible current status while metadata is closed. Fresh exact-head full CI and screenshots are required for this changed UI; the earlier artifact does not establish the repaired head.

The repaired candidate passed a fresh complete `make check`: 60 Python contracts, Go race/vet/build, 36 frontend files / 1,300 tests, TypeScript/build and zero audit vulnerabilities. Formatting and all 31 Playwright test discovery checks also passed; independent repair review passed 54 focused task-navigation/maintenance tests. These local results do not replace the fresh native matrix.

## Audit task to original export detail (bounded candidate)

Issue #26 AD-F-151/152 supports viewing an export task's status and downloading an
already generated, currently authorized file. This small usability integration
does not claim that the legacy requirements explicitly mandated this link or that
audit export was previously absent.

After a successful current task-detail read identifies exactly `audit.export`,
the task center offers the accessible `查看原审计导出` anchor. Its target is the
existing `#audit/detail/{taskUUID}` route, using the existing `validExportID`
validator and an exact match with the requested task ID. Pending/failed reads,
invalid/mismatched identities and other task kinds cannot expose this entry.
Every task state can lead to the original status page; the link itself does not
mean a file is downloadable. Failed-task guidance still explicitly requires a
new export from the original audit page.

This is navigation only. It creates no export/recovery intent or idempotency key,
does not submit/cancel/retry/recover a task, and does not auto-download. The existing
App hash/history session check, AuditWorkspace, AuditExportDetail and audit API are
reused, rather than copying a fourth result panel. Existing asset-export result
components are unchanged. The original endpoint still enforces current
`audit.readable` plus `audit_exports.readable`, actor/tenant/grant/domain/metadata
boundaries and download eligibility. No new independent frontend detail gate,
backend API, DTO, producer, format or permission is introduced.

Added test source extends Tasks, TaskNavigation and the existing real audit suite:
exact kind/ID and all task states, malformed/mismatched responses, pending/error/
session guards, original endpoint denial, zero business POST/automatic download,
Back/Forward/reload, and original task query/selected-row return. The real suite
reopens both its actual nonempty and zero-row exports through this link, explicitly
downloads their original XLSX, and compares filename, bytes and SHA-256 with the
same original task's artifact. Existing security/visibility/revocation assertions
remain in place.

New safe captures use `audit-soc-task-link-{nonempty,zero-row}-{entry,result}-{1440,390}.png`
and a separate `audit-soc-task-link-evidence.json`. They retain source/build/runtime/
commit and screenshot digests and are discovered by the existing `audit-soc-*`
artifact upload. The earlier four audit screenshots and their manifest are not
replaced.

The selective-CI inventory explicitly adds audit to TaskWorkspace's existing
15 browser consumers (16 total). Browser navigation is not an import dependency,
so a regression in the already-discovered selector contract checks a
TaskWorkspace-only change still selects audit and all previous consumers, without
relying on this PR's audit-spec edit or selecting every suite. This inventory
change itself retains full shared-CI validation; jobs, commands and artifact
discovery stay unchanged.

At preparation time, local tests, formatting, typecheck/build, Playwright discovery,
runtime launches and screenshots were not run because local execution is on hold.
The exact published head must receive authorized CI and independent source/pixel
review before any merge claim. New-tab/copy-link task-list restoration and expired/
archived audit artifacts are not newly accepted by this slice; existing route and
server contracts still apply. Real AD/Windows, full legacy equivalence and product
acceptance remain open.
