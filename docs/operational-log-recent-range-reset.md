# Recent operational-log range: clear unapplied drafts

Engineering issue #302; parent #55 / AD-F-205. Based on main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`.

## Existing behavior and reproduced defect

The [operational journal contract](operational-log-contract.md) defines page
1/20 and both `api` / `worker` modules by default. Absent time bounds are resolved
by the database to its latest 24 hours. The accepted response echoes that exact
selection, and bundle review uses that selection rather than unsent form values.
This change does not define a new clock, time range or bundle contract.

When the applied query is already default, editing the uncontrolled time fields
or page size and then clicking `查询最近 24 小时` makes a new default GET, but
leaves those draft controls unchanged. The form key, `JSON.stringify(query)`, did
not change. Clicking `查询事件` then submits the old draft again. Thus visible
controls disagree with the new result. The backend was not shown to package an
incorrect range: bundle review still used the validated server selection.

The full-workspace regression on the original source passes its isolated module
reset case. A separate discovery harness reports different module retention;
the difference has not been explained. The confirmed defect and fix do not
depend on claiming that every control independently fails.

## Change and required scope

The existing recent-range button resets its owning form before applying the
existing empty query and explicit refresh. This is necessary when an unchanged
query key cannot remount the form. Removing the native reset restores the draft
retention bug. When a non-default query was applied, the existing changed form
key still remounts with default controls. No additional React state/effect or
forced remount is introduced. The button remains focused for an unchanged key;
controls in other forms are not reset.

The deliberate effect is to discard this query form's unsubmitted edits when
requesting the recent default range. The existing refresh still creates exactly
one replacement GET, even when the query parameters were already default. It
still cancels an earlier read and ignores its late result. No extra query is
made merely by native form reset, and no task is submitted or downloaded.

Only this event-query action changes. History filters, parser, transport,
authority, diagnostic-bundle intent, proof inputs, source selection, emitters,
SQL, workflows and budgets are unchanged.

## Offline evidence

- Original `OperationalLogs.test.tsx`: 30 tests pass.
- First 12 new regressions before the fix: 8 fail, 4 pass. Failures reproduce
  retained time/page-size drafts and their next submission, not a mock-only
  parser mismatch. Existing assertions and tests are not removed.
- Final new suite includes individual controls, next submit, invalid input,
  repeated reset/focus, applied custom queries, pending custom and default
  reads, refresh failure/retry, bundle review and permission denial.
- Tests mount the actual workspace and use the actual API decoder/read hook
  with synthetic HTTP responses. They check exact GET parameters and prohibit
  unexpected endpoints. Bundle review retains the server-confirmed range and
  does not make a bundle POST or download request.

Commands from `web`, using the cached locked dependencies and Node 24.19.0:

```sh
node node_modules/vitest/vitest.mjs run src/OperationalLogRecentRange.test.tsx src/OperationalLogs.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/OperationalLogsWorkspace.tsx src/OperationalLogRecentRange.test.tsx
```

At the repository root: `python3 scripts/verify_requirements.py` and
`git diff --check`. Exact final results and independent review are recorded in
#302. No dependency installation, audit, aggregate `make check`, PR or CI is
run; only a non-triggering backup branch is published. No actual PostgreSQL,
producer, browser, worker or ZIP chain has been executed for this change.
Engineering readiness and product acceptance remain separate; #55 remains open
and product acceptance remains 0/209.
