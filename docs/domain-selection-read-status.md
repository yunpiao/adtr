# Configured-source read denial recovery

Issue #310, within #18 / AD-F-006 and the existing
[domain-selection contract](domain-selection-contract.md). Base main:
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`.

## Confirmed behavior and correction

The actual `sourceAPI` and `SavedSourcePicker` list and resolve paths lost the
existing session-revalidation callback on non-JSON HTTP 401. A JSON
`unauthenticated` 401 invoked `sessionChanged` once; an HTML 401 became
`invalid_response/status 0` and invoked it zero times. HTML 403 similarly lost
the explicit lost-permission message. Every observed failure already cleared
rows/selection, disabled opening and left `onResolved` uncalled. This is not an
authorization bypass or stale-data disclosure finding.

The API helper now uses a fixed HTTP-status fallback **only when reading or
decoding the error body fails**. Both consumers are GETs, so no mutation/proof
discriminator or new public parameter is needed:

| Status | Fallback          | Existing caller behavior                                              |
| ------ | ----------------- | --------------------------------------------------------------------- |
| 401    | `unauthenticated` | Request the existing session revalidation.                            |
| 403    | `forbidden`       | Show lost permission and keep selection cleared; no automatic logout. |
| 404    | `not_found`       | Show an unavailable/out-of-scope source and keep selection cleared.   |

This local mapping is required only for an unreadable body at these statuses.
It is not an authorization grant, permission setting or runtime configuration.
Removing it restores the observed failure classification bug. Its intentional
side effect is using a safe HTTP classification instead of a JSON-decoding error;
it adds no request, retry, refresh, selection or connection attempt.

## Preserved error and selection boundaries

Parsed JSON follows the original exact `{error: code}` envelope and fixed code
allowlist unchanged. Recognized `password_change_required`, tenant errors and
`selection_changed` retain their meaning and status. Parsed null, arrays,
missing/wrong/unknown codes or extra keys still produce `internal` with the HTTP
status. No arbitrary error text is displayed. The fallback does not widen the
trusted JSON error vocabulary.

There is deliberately no status-only mapping for 409. Only the recognized exact
409 `selection_changed` response clears and refreshes choices automatically.
Malformed 409 remains an invalid response with no automatic refresh or resolve.
Other malformed statuses such as 400, 422, 429 and 5xx retain their prior
classification. A non-JSON 403 cannot reveal a specific tenant or password-change
reason; those details require a recognized JSON code.

Success actor-header validation, list pagination, exact source DTO, original
domain/configuration/credential revisions and current observation parsing are
unchanged. Selecting or resolving a source is not task submission or a grant to
the consumer. Failed selection cannot call `onResolved`. Clearing a pending
resolve or changing the list's identity generation suppresses its late result,
including a transport that ignores AbortSignal. Fetch rejection after abort
keeps the original abort error; a failed body reader after an HTTP denial uses
that known denial unless the caller's generation has already been discarded.

## Regression and validation method

Only `web/src/domain-selection-api.ts`, the new
`web/src/SourceReadStatus.test.tsx` and this document change. The original
62-case `DomainSelection.test.tsx`, SavedSourcePicker, DomainsWorkspace, shared
hooks, system API, backend, workflows and other offline candidates are unchanged.

The new 120 cases cover both GET transports; non-JSON bodies and failed readers;
the unchanged parsed JSON allowlist/shape rules; legitimate password/tenant/409
codes; invalid successes, actor binding, exact request fields and network/abort
controls. Actual picker tests exercise initial-list and selected-resolve denials,
refresh of a prior selection, valid versus malformed 409, explicit success,
Cancel and changed-identity stale-response suppression. All transport fixtures
are synthetic; the API and React components are actual code.

Use cached dependencies and Node 24.19.0, without installing or auditing:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/SourceReadStatus.test.tsx src/DomainSelection.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/domain-selection-api.ts src/SourceReadStatus.test.tsx ../docs/domain-selection-read-status.md
```

An initial broader implementation also reclassified parsed unknown JSON error
codes. The existing privacy/allowlist regression correctly failed because it
requires `internal`. The fix and new tests were narrowed to the confirmed
body-reading/decoding defect instead; the original regression was not edited or
relaxed. Initial source, tests and failing logs remain separate evidence. Final
unchanged-main controls use the final test version, not the broader first draft.
The final Issue comment records counts, commands, review hashes and backup SHA.

## Remaining gates

No live reverse-proxy occurrence rate, real browser/API/PostgreSQL chain, AD,
Windows or product acceptance is established by these synthetic DOM tests.
There is no source inventory, authorization change, remote connection, PR,
audit, CI trigger, deployment or merge in this slice. The non-triggering backup
is not a claim of merge readiness. #310 and #18 remain open, and product
acceptance stays 0/209.
