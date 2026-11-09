# Asset export recovery during temporary storage failures

Engineering issue #303; parent #30 / AD-F-053, EXP-006 / EXP-008 / EXP-007.
Base main: `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`.

## Existing contract and confirmed failure

The [user export lifecycle](user-asset-export-v3-contract.md) permits only the
five-field actor/domain/original-idempotency-key locator, with strict validation
and actor mismatch cleanup. Group and computer export contracts reuse that
lifecycle. Recovery reads the original receipt, never infers successful execution
or automatically creates a replacement task. Existing recovery modules retain
an in-memory fallback when browser storage is unavailable.

The actual user-export component demonstrates this failure:

1. A valid locator A is persisted and recovered through its receipt endpoint.
2. Synthetic storage get/set/remove operations temporarily throw SecurityError.
3. The user clicks `放弃原回执恢复` and explicitly confirms abandonment. Local
   component state is cleared; the durable removal fails, leaving A behind.
4. Storage becomes available again and the export component remounts in the same
   application runtime. The old raw A overrides the local cleared state, and the
   component reads A's receipt again.

A second component regression creates B after that explicit abandonment while
storage is still unavailable, then receives an ambiguous network failure. On
remount after storage returns, the old durable A replaces B's in-memory locator.
The original code thereby loses the correct recovery path for that new operation.
No business request is automatically repeated in these tests.

The relevant condition is stale durable data surviving a failed clear or write.
A quota failure alone does not reproduce the ordinary serial UI path if the old
locator was successfully removed first. That healthy-clear/failed-set contrast
is retained as a passing regression, not described as a new failure.

## Bounded implementation and trade-off

Each existing kind-specific module keeps `pendingStorage`, an internal boolean
indicating that its most recent local write/clear has not succeeded in storage.
It is not a user setting, authorization state, recovery result or pending task.

Write and clear set this flag before accessing storage and clear it only after
the operation succeeds. While set, read validates and uses the current memory
value (or cleared-state marker), without preferring an older durable value.
Removing the flag/gate restores stale-record resurrection and replacement of a
newer original key. It is needed only across a failed persistence transition;
healthy writes/reads keep their existing strict durable-record validation.

The existing invalid/mismatched/empty-value path still clears the locator. A
read of the local cleared marker may therefore retry removal; if storage is now
available, that removal succeeds and ordinary durable restoration resumes.
An explicit successful subsequent write likewise resumes normal storage reads.
An unavailable initial read with no memory value is different: it returns no
locator without creating a clear marker or attempting deletion. The durable
record is still unknown, not known invalid or explicitly abandoned. On a later
successful read, it can be recovered normally. Malformed bytes that were actually
read still fail strict validation and follow the existing cleanup path.
There is no automatic write of an uncertain locator, task submission, request
retry, receipt fetch or cancellation in these helpers.

Side effect: while a newer in-memory locator remains unpersisted, external
changes to that stale storage slot are not imported into this runtime. This
prevents an obsolete record from silently replacing the user's latest local
intent. Identity and shape checks still apply to the memory value; a different
actor cannot restore it. Each asset kind has a separate flag, value and key.

This is a **same-runtime** recovery fix. It does not make a failed browser write
or deletion durable. A full page reload/process loss can lose the memory marker
or the newer locator while stale durable bytes remain. No new persistence store,
permission change, security-setting change or cross-process guarantee is added.
The strict localStorage/Web Locks cancellation barriers are separate and untouched.

## Verification

- Original three export component suites: 119 tests passed.
- First 47 new cases on the original code: 23 failed, 24 passed.
- Independent review found an initial-read regression in the first repair:
  unavailable get/remove with empty memory was incorrectly treated as an explicit
  clear. Three actual helper baseline/candidate comparisons confirmed it. The
  final repair separates unread/unknown storage from observed invalid data, and
  six added cases verify recovery with removal both available and unavailable.
- Final 53 new cases exercise healthy copies/projection, successful clear plus failed set,
  failed clear and replacement, read access returning, repeated reads, explicit
  successful persistence, malformed/oversized/expanded records, actor/kind
  isolation and two actual user-component remount flows.
- The component tests use real React recovery/lifecycle code with API spies;
  the independent discovery additionally exercises the actual receipt HTTP
  adapter with synthetic fetch. Neither is real browser/storage-policy/PG proof.

Commands from `web`, with cached locked dependencies and Node 24.19.0:

```sh
node node_modules/vitest/vitest.mjs run src/AssetExportRecoveryStorage.test.tsx src/UserAssetExportV3.test.tsx src/GroupAssetExportV3.test.tsx src/ComputerAssetExportV3.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/AssetExportRecoveryStorage.test.tsx src/user-asset-export-v3-recovery.ts src/group-asset-export-v3-recovery.ts src/computer-asset-export-v3-recovery.ts
```

Repository checks: `python3 scripts/verify_requirements.py` and
`git diff --check`. Exact final results and independent review are in #303.
No audit, make check, installation, PR/CI, merge or deployment is run. Only a
non-triggering backup branch is published. Actual browser/PG/Worker/XLSX and
full product acceptance remain unverified for this change; 0/209 is unchanged.
