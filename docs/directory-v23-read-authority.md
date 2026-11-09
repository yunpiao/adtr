# Directory v2/v3 bounded-read authority classification

Offline engineering candidate #316, within F12/#25 and existing versioned read
contracts. Baseline main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`. Product acceptance remains **0/209**.

## Confirmed defect, not a new permission policy

Actual v2/v3 APIs and Observation components with synthetic fetch both showed:
JSON401/unauthenticated makes one GET and one session callback; HTML401 makes two
GETs in 2.2 seconds without a callback. HTML403 similarly retries with a generic
malformed-response notice, while JSON403/forbidden stops with permission text.
No stale data or authorization bypass was observed. This is client behavior
under controlled malformed responses, not evidence of a production proxy fault.

The shared bounded reader already preserves HTTP status, but `useTaskRead`
classifies session errors and retryable reads by code. Failed decoding produces
invalid_response, which causes the incorrect retry and missed session callback.
Independent checks found v2/v3 pagination, observed-empty versus unavailable,
wrong-page rejection and fixed-observation matching consistent with contract;
those behaviors are unchanged.

## Narrow change and necessary guards

Only the two versioned transports catch rejection of `readDirectoryV2JSON`.
They map HTTP401/403 to existing unauthenticated/forbidden errors if all apply:

- `body === undefined`, the same discriminator that selects GET. It is necessary
  to exclude sync/cancel POSTs whose outcome may already have committed.
- `!signal.aborted`: cancellation remains cancellation, not a session callback.
- `!response.ok`: successful responses still require valid DTOs and actor headers.
- The thrown value is `ApiError` with code `invalid_response`. Resource-limit
  errors and unexpected reader-acquisition errors retain their original meaning.

The reader is still used in full, with its unchanged 8 MiB cap, fatal UTF-8
handling, stream cancellation, abort checks and reader-lock cleanup. No unbounded
json/text fallback is introduced. Invalid Content-Length and actual excess bytes
remain directory_limit_exceeded, even when HTTP status is 401/403. Removing that
error-code guard would hide a distinct resource-limit failure. This patch does
not change whether existing POST limit errors are considered uncertain.

Decoded JSON retains the exact existing error envelope and code policy. A
password-change error is not replaced by generic403. Other malformed statuses,
including404, retain invalid_response and their original HTTP status. Recovery
still requires a structured404/not_found before offering explicit same-key retry;
a missing receipt does not prove the original operation failed. No automatic POST
is added, and original recovery intent/key remain preserved.

Both POSTs retain malformed401/403/404 uncertainty, existing classification and
proof clearing. No Workspace, hook, intent, shared reader, v1 or R4 file changes.
The candidate is independent of #315 and other unpublished branches.

The intended side effect is to stop automatic retry of known unauthorized reads.
An explicit refresh remains available after access is restored. Without this
fallback the reproduced behavior returns; no new configuration is required.

## Offline tests and limits

`DirectoryV23ReadAuthority.test.tsx` exercises the real APIs and full Workspaces
with synthetic fetch/ReadableStream rather than mocked API modules. It covers
both versions' observation/receipt/task GETs and sync/cancel POSTs, malformed and
structured bodies, UTF-8 failures, valid/invalid successes, actor mismatch,
permission refresh, six fake seconds without incorrect retry, late unmounted
results, original intent retention and structured-versus-malformed404 recovery.
An actual sync form sends only one POST, then queries the original receipt key.

Bound-specific controls check advertised and streamed over-budget responses,
exact-cap structured JSON, cancellation during a pending read, canceled/released
readers, locked-reader errors and no unbounded json/text reads. Shared reader,
old tests and production limits remain unchanged. Old-main identical-test results,
full suite/build and exact independent-review hashes are recorded in #316.

```sh
cd web
node node_modules/vitest/vitest.mjs run src/DirectoryV23ReadAuthority.test.tsx src/DirectoryV2.test.tsx src/DirectoryV3.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
cd ..
python3 scripts/verify_requirements.py
```

Cached Node24.19.0 and dependencies only. Original v3 file has a pre-existing
Prettier wrapping difference outside this patch; it is preserved rather than
mixing an unrelated edit. New files, v2 and changed transport fragments are
formatted; whole-file baseline/candidate comparison is retained in evidence.

No audit, install, make check, PR, CI, Go/database/browser E2E, real AD/Windows,
deployment or merge. The backup is not CI or product acceptance. Issue stays open
for later authorized exact-base/head gates and actual-system evidence.
