# Operational-log read status recovery

Issue #309, within #55 / AD-F-205 and the existing
[operational-log contract](operational-log-contract.md). Base main:
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`.

## Confirmed defect and repair

The shared operational-log JSON helper previously parsed the body before
interpreting a failed HTTP response. An empty, HTML or broken JSON 401 therefore
became `invalid_response` with status 0. `useLogRead` recognizes session loss by
the `unauthenticated` or `password_change_required` code, so this lost the
existing session-revalidation callback. Independent actual API and React DOM
reproduction compared JSON 401 (callback once) with HTML 401 (callback zero).
HTML 403 also lost the specific permission-denied presentation. Both error paths
already cleared old list data; this is not evidence of a stale-data disclosure.
Normal 403 does not itself require logout.

The helper now receives a required internal `readOnly` boolean. All five GET
paths pass true: event list, source observations, bundle history, bundle detail
and bundle download errors. If an error body cannot supply a usable code, these
paths recover fixed meanings from the known HTTP status:

| Status | Fallback code     | Existing caller behavior                                                                                         |
| ------ | ----------------- | ---------------------------------------------------------------------------------------------------------------- |
| 401    | `unauthenticated` | Revalidate the current session through its existing callback.                                                    |
| 403    | `forbidden`       | Show the existing explicit permission-denied message; no automatic logout.                                       |
| 404    | `not_found`       | Preserve the existing unavailable-resource classification; not proof that an earlier submission never committed. |

“Unusable” includes JSON reading failure and JSON with no valid error code. A
code is valid under the existing `[a-z][a-z0-9_]{0,63}` rule. Valid server codes
take precedence, including 403 `password_change_required`, which must still
invoke session revalidation instead of being flattened into `forbidden`.
No submitted or server body text is copied into a fallback error.

`readOnly` is an implementation discriminator, not a configurable permission,
feature flag or additional request field. Explicit true/false arguments are
required at every helper call so a future caller must choose the correct
response semantics. Removing the distinction would either restore the GET bug
or also reinterpret ambiguous POST/proof failures. Its intended behavioral
effect is limited to unusable GET 401/403/404 error bodies; there are no added
requests or automatic retries.

## Unchanged boundaries

- Submit and cancel pass false. Malformed POST responses still produce
  `invalid_response/status 0`; valid proof/server error codes and statuses remain
  intact. An unusable JSON error code still becomes `internal` with its status.
- An uncertain submission retains the attempted input and original idempotency
  key. No fallback clears that record, changes the key, automatically replays,
  or establishes whether a write committed.
- Malformed successful responses remain invalid, never usable data. Successful
  response actor binding, list/detail DTOs, ZIP headers, lengths, stream bounds
  and signature checks are unchanged.
- Other malformed error statuses, including 429 and 5xx, retain their existing
  `invalid_response/status 0` behavior. This slice does not generalize transport
  or retry policy.
- Request-generation cleanup still suppresses stale callbacks even for a mock
  transport that ignores AbortSignal. A rejected aborted transport preserves its
  original abort error. No workspace or shared hook is changed.

## Verification method

Use the already available Node 24.19.0 and cached dependencies; no installation
or audit is part of this slice:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/OperationalLogReadStatus.test.tsx src/OperationalLogs.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/operational-log-api.ts src/OperationalLogReadStatus.test.tsx ../docs/operational-log-read-status.md
```

The dedicated suite adds 238 cases: five GET transports with unusable bodies,
valid server codes, failed body readers, actor mismatches, invalid success and
unrelated error controls; POST/proof and original-key retention controls; real
workspace session/permission paths, row removal after refresh, download failure
without browser artifacts or business POSTs, superseded generation and abort
controls. The existing 30-case operational-log suite is unchanged.

An unchanged-main control runs the identical new tests against the old API.
Keep its assertion failures separate from candidate passes, typecheck and build.
An initial new test helper inferred only `Promise<string>` for a mixed response
fixture and failed TypeScript TS2322; specifying `useLogRead<unknown>` fixes the
test's type annotation without modifying production behavior. Do not count the
initial typecheck failure as a pass.

An initial build invocation incorrectly supplied Vite's unsupported `--root`
option and exited before building. The documented command above runs from
`web` instead; the invocation failure remains in the evidence separately.

The independent discovery harness used a cached #302 workspace whose API and
common hook hashes exactly matched main. Its unrelated recent-range button
change was not exercised. Final candidate and unchanged-main regression runs
instead use independent main-tree workspaces, with no #302 dependency.

## Delivery and acceptance limits

The final Issue evidence records exact counts, command results, independent
review hashes and the remote backup tree. Source changes are limited to
`operational-log-api.ts` plus this new test/document; existing tests, workspace
components, other offline candidates, backend and CI files are unchanged.

These are synthetic transport and React/JSDOM checks. They do not establish
actual reverse-proxy occurrence frequency, PostgreSQL/API/worker production
behavior, browser download integration, real AD compatibility or product
acceptance. No deployment, PR, audit or CI is performed. The non-triggering
backup is not a merge-ready PR; Issue #309 and parent #55 remain open and product
acceptance remains 0/209.
