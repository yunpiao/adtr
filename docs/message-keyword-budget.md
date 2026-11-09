# Preserve literal message keywords within existing byte budgets

Offline engineering candidate #313 for F40 #50 and the existing #105 query UI.
Baseline remote main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`. Product acceptance stays **0/209**.

## Reproduction and existing contract

`soc-message-ui.md` requires explicit query confirmation and literal,
case-sensitive title matching. Existing `messageQueryBody` accepts at most
16000 UTF-8 keyword bytes; the existing API tests reject 16001 ASCII bytes.
`message-runtime-contract.md` and `internal/auth/message_http.go` independently
bound the complete POST JSON body to 32768 bytes. Neither budget is a capacity
or source-title-length promise.

Actual `MessagesWorkspace` plus `userEvent.paste` and actual message API, with
synthetic transport, reproduced silent truncation: 4001 ASCII characters became
4000 in both the input and outgoing request. 2001 emoji (8004 UTF-8 bytes) became
2000 emoji (8000 bytes). Both original values were accepted by the existing
query builder. Native `maxLength=4000` measures UTF-16 code units rather than
UTF-8 bytes and changes the literal the person intended to confirm.

This is an input-preservation defect, not a demonstration that real health
messages have long titles or a real backend query returned incorrect rows.

## Implementation and why both limits remain

Remove only the keyword input's native length restriction. Do not replace it
with another truncating native cap: even a 16000-code-unit cap could turn an
oversized draft into a different valid query. Users can keep the whole draft,
receive the existing invalid-input error on confirmation, then correct it.
The existing 16000-byte keyword validation still runs before any query POST.
No automatic query, trimming, normalization or substring replacement is added.

The completed wire object is also measured as UTF-8 JSON before return from
`messageQueryBody`; more than 32768 bytes raises the existing `invalid_input`.
JSON escaping matters: 6000 NUL characters are only 6000 raw bytes but the
existing default query expands to 36107 JSON bytes. Raising input accessibility
without the wire guard would allow such drafts to hit the server's body limit.
Exactly 32768 bytes remains accepted. Time/subtype fields are included in the
measurement, so adding filters can make a previously valid draft too large.

The two constants serve distinct existing constraints. Raw keyword bytes retain
the frontend's established input budget; serialized bytes match the mandatory
HTTP body limit. Removing the raw check changes accepted client inputs; removing
the wire check allows locally valid inputs to fail remotely after submission.
Removing the native-cap deletion restores silent truncation. The new checks do
not raise either existing limit, and no new configuration is required.

Side effects: oversized drafts remain visible until corrected, and serialized
oversize is rejected locally instead of sending a doomed non-idempotent snapshot
request. Each validation serializes and encodes the bounded draft, a small extra
allocation. Existing confirm validation and API validation both remain in place.

Only `MessagesWorkspace.tsx` (one deleted line) and `message-query.ts` (complete
body preflight) change production behavior. Request fields, shared transport,
query matching, snapshot identity/counts/paging, authorization, cancellation,
read/bulk mutations and recovery remain unchanged. No dependency on the separate
#306 bulk-budget candidate is introduced.

## Offline validation

`MessageKeywordBudget.test.tsx` exercises actual APIs and components, with
synthetic responses. Cases cover ASCII, emoji, CJK, combining characters,
literal escapes, 16000 raw-byte edges and exact 32767/32768/32769 JSON bytes,
with and without nanosecond time/subtype fields. The expected JSON and size
oracle is spelled independently rather than calling the production builder.

Actual paste/type/selection replacement tests check the input and the exact
outgoing value. Oversized drafts are not truncated or submitted and can be
corrected. Existing fixed results survive invalid draft confirmation. Repeated
confirm, cancellation with late response, StrictMode and identity remount retain
existing behavior. All old tests remain unchanged.

Commands use cached dependencies and Node 24.19.0 only:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/MessageKeywordBudget.test.tsx src/message-api.test.ts src/MessagesWorkspace.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/message-query.ts src/MessageKeywordBudget.test.tsx ../docs/message-keyword-budget.md
cd ..
python3 scripts/verify_requirements.py
```

Original focused baseline: 65 tests passed. Final results, identical-new-test
old-main controls, independent review, source hashes and backup are in #313.
`MessagesWorkspace.tsx` already contains unrelated timeline-format drift in the
baseline. An initial formatter touched that region; those formatting edits were
reverted before final validation, keeping only the single input-line deletion.
Whole-file Prettier still reports the original drift; it is not claimed passing.
New/other changed files and the local changed input fragment are format-checked.

No audit, install, `make check`, PR, CI, PostgreSQL, real browser E2E, AD,
deployment or merge is executed. This is a non-triggering source backup, not a
CI pass. The Issue remains open for later authorized gates and product evidence.
