# F49 task-archive receipt binding

Engineering issue: [#300](https://github.com/yunpiao/adtr/issues/300).
Parent [#15](https://github.com/yunpiao/adtr/issues/15), AD-F-193, remains open.
This corrects the existing [reversible task-archive contract](task-archive-contract.md).
It adds no physical cleanup, automatic retention, task execution or new authority.

## Defect and user-visible effect

The previous client accepted matching target IDs with any boolean `archived`
value and any nonnegative version. It did not compare the receipt's reason or
archive cutoff with the submitted intent. A contradictory HTTP success could
therefore reach the confirmation page's success callback, discard the pending
original intent, and announce that the visibility operation was recorded.

The API now requires each returned target to have the state implied by the
action and exactly its own original expected version plus one. Receipt reason
must match the original reason without trimming or normalization. Archive
receipts must carry the same valid UTC cutoff instant; restore receipts must
omit `before`. Existing target-set/uniqueness, safe integer and action checks,
server error propagation, transport, authorization and proof handling remain.
The comparison fields and target objects are copied before sending, so caller
changes while awaiting a response cannot rebind the receipt to unsent intent.
This private copy contains no proof or session credentials; the existing
outgoing request shape and backend validation of unknown fields are unchanged.

A mismatched success raises the existing `invalid_response` error. The existing
confirmation flow then retains the original key and complete input, clears the
proof fields, and requires an explicit same-intent retry with fresh proof. It
does not automatically retry a mutation, invent a replacement key or claim that
the server rolled back. A transport success is not enough to confirm the result.

## Why these comparisons are required

- `archived` is true for an archive receipt and false for a restore receipt.
  Accepting its opposite would acknowledge a different visibility change.
- `visibilityVersion` is the original request version plus one for that target,
  not a positionally paired version or any later current version. Different
  target ordering is valid because the server sorts targets before producing
  the immutable receipt. The next version must be exactly representable by the
  existing safe-integer client contract; unsafe values cannot be confirmed.
- `reason` identifies the recorded intent and must match exactly. It is not
  silently trimmed or replaced by another operation's reason.
- `before` is required only for archive and is forbidden in restore receipts.
  Both cutoff values must pass actual UTC calendar and 1–6 fractional-digit
  validation, with years 1–9999. Equivalent trailing zero forms such as
  `.123400Z` and `.1234Z` are accepted. String comparison of six-digit fractional
  keys preserves PostgreSQL microseconds; `Date.parse` alone would merge
  `.123400Z` and `.123401Z` and accept a different cutoff.

Removing these checks restores false confirmation of contradictory receipts.
The intentional compatibility effect is that incomplete or inconsistent mocks,
proxies or servers now remain unconfirmed. Normal backend receipts and their
historical replays remain accepted. No timestamp rule for other maintenance
APIs is changed.

## Replay remains historical

`internal/taskarchive/engine.go` returns the stored immutable receipt when the
tenant/actor-scoped key and normalized request match. A later independent
restore may already have changed the task's current visibility. The client must
still accept the original archive receipt with its original next version,
then use the existing detail navigation to reread current state. Receipt state
is not a current-state claim or a reason to reapply the old operation.

## Offline evidence

On unchanged main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, the existing
maintenance suite passed 140 tests. The new suite first produced 58 failures
and 52 passes against unchanged production code. With the correction, the
114 new API/DOM tests and original 140 tests pass. One old replay fixture now
includes its contract-required archive cutoff; its original historical-replay
and freshly-restored-state assertions are preserved.

The new tests cover archive and restore, first response and replay, per-target
states/versions, sorted target correspondence, reason mismatch, exact UTC
microsecond comparison, malformed/missing/unexpected cutoffs, safe integer
limits, existing target guards and original server errors. Both actual React
confirmation flows retain persisted pending input after contradiction, reopen
without automatic POST, and only clear it after explicit valid same-key replay
using a fresh synthetic verification code. Independent review identified the
in-flight mutable-input boundary; four additional deferred-response regressions
failed before the snapshot fix and passed afterward. They verify original sent
receipts are accepted and receipts matching later unsent edits are rejected.

Reproduce with cached locked dependencies and Node 24.19.0:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/ArchiveReceiptBinding.test.tsx src/Maintenance.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
```

Full offline results, exact file hashes and independent review are recorded in
#300. These are synthetic API/DOM tests, not real browser/PostgreSQL/AD/Windows
acceptance. Audit, aggregate `make check`, PR/head CI, merge and deployment are
not run under the current restriction. The branch is backup only; product
acceptance remains 0/209.
