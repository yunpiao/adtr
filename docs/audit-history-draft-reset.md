# Export-history clear action resets unapplied drafts

Engineering issue: [#301](https://github.com/yunpiao/adtr/issues/301).
Parent [#26](https://github.com/yunpiao/adtr/issues/26), AD-F-151/152.
The existing [export-history contract](export-history-contract.md#frontend-contract-and-navigation)
requires independent history filters and apply/clear actions that return to page 1.
This correction changes no filter semantics, API, permission, export or download.

## Reproduced defect

The history form uses uncontrolled inputs with defaults from the applied query.
Its React key is the serialized applied query. Previously, Clear only installed
`emptyHistoryQuery()` and removed any form error.

When the applied query was already default, editing a time, task status, sort or
page-size draft and then clearing it did not change that key. React kept the
same controls and their unsent values. Pressing Filter afterward submitted those
supposedly cleared values. Clearing a previously applied nondefault query did
work because the new query changed the form key, which is why the older test did
not catch the draft-only path.

## Correction and boundaries

The existing Clear button now resets its own HTML form before installing the
default query. Native form reset restores the controls' current default values.
If the applied query was already default, those are the desired empty times,
no selected statuses, descending order and page size 20. If it was nondefault,
the existing query-key remount then installs the new defaults and page 1.

This reset is necessary while the fields remain uncontrolled and the form key
depends only on applied query values. Removing it restores the draft-only bug.
It adds no state, effect, request or synthetic submit. It intentionally discards
unsent history drafts on Clear, but does not reset another form. When the query
does not change, the existing form/button and focus remain; an outstanding
default history read is neither replaced nor aborted solely to clear drafts.
When the applied query changes, the existing read hook requests that new default
query and preserves its normal stale-response and authorization handling.

The existing page-size choices, nine statuses, UTC validation, error messages,
account binding, detail navigation, polling and protected downloads are unchanged.
There is no mutation, new permission, dependency or workflow change.

## Offline evidence

On main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, the original 20 history
tests passed. The new 12-case suite produced 10 failures and 2 passes before the
correction. With it, all 12 new and 20 original tests pass without changing any
old assertion or fixture.

`web/src/AuditHistoryReset.test.tsx` uses the actual React history component,
query serializer/parser and read hook, with synthetic HTTP responses. It covers
each draft control, all controls together followed by Filter, invalid time
draft/error clearing, repeated edit/clear cycles, unchanged-query focus and
request count, applied nondefaults and later-page reset, and a pending default
read. Assertions check actual control values and outgoing requests, not just
the query-state callback. Only the existing history GET is permitted by the
test fixture; no POST, download or per-row request is introduced.

Reproduce using cached locked dependencies and Node 24.19.0:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/AuditHistoryReset.test.tsx src/AuditHistory.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
```

Exact offline results and independent review are recorded in #301. DOM/mock
evidence is not real browser/PostgreSQL/AD/Windows acceptance. No audit,
aggregate `make check`, PR/head CI, merge or deployment is run under the current
restriction. A non-triggering branch is backup only; #26 and product acceptance
remain open at 0/209.
