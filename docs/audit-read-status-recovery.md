# Audit read failures: HTTP status fallback

Refs #305, #13 (F39 / AD-F-153–156) and #26 (F38 export history).
The existing [audit contract](audit-contract.md) specifies unauthenticated 401,
forbidden 403 and unavailable-target 404. The
[export history contract](export-history-contract.md) specifies that its GETs
consume no password/TOTP proof, must handle session expiry and clear old data.
This change fixes error recovery within those existing endpoints, not a new API
or an authorization grant.

## Verified failure and correction

The audit adapter previously parsed JSON before considering a failed HTTP status.
Empty, HTML or truncated bodies became `invalid_response`, including on HTTP
401/403/404. `useAuditRead` clears its data on failure, but treats that code as
retryable and schedules another read after two seconds. An unusable 401 body
therefore did not call `sessionChanged`, and the workspace repeatedly fetched a
known denied request instead of letting the owner revalidate the session.
Valid JSON without a string `error` became `internal`, losing the same status
meaning even though that particular code did not schedule a retry.

`audit-api.ts` now supplies a read-only classification to its private JSON
adapter. For a read with an unparseable body or no string error code, it uses:

| HTTP status | Fallback code | Existing reader behavior |
|---|---|---|
| 401 | `unauthenticated` | Clear data, notify the session owner, no automatic retry |
| 403 | `forbidden` | Clear data, show denied access, no automatic retry |
| 404 | `not_found` | Clear data, show unavailable target, no automatic retry |

Manual refresh remains available. A 403 is not relabeled as an expired session,
and a 404 does not claim deletion or a successful empty result. Other statuses,
network failures, successful-response validation and retryable server errors keep
their existing behavior. A valid string error code from the server is retained.
In particular, **403 `password_change_required` still triggers session
revalidation**, and `authorization_revoked` retains its existing explanation.

The required internal `readOnly` argument distinguishes GETs from proof-bearing
POSTs. List/types/columns derive it from the absence of a request body;
history/detail/download pass `true` explicitly. It is not an HTTP parameter or
user-configurable setting. Omitting this classification would restore the bad
read fallback; misclassifying a write could incorrectly treat an uncertain proof
response as session or permission evidence. No write is reclassified here.
Hide/restore/export POSTs retain `invalid_credentials`, `invalid_totp`, malformed
response uncertainty and the original idempotency keys. No mutation is retried
automatically and no proof is stored by this change.

The adapter still reads the error body to preserve meaningful server codes. It
does not add a body-size limit, body timeout or cancellation policy, and does not
promise progress for a body that never finishes. HTTP delivery failures in the
tests are synthetic: no production proxy incident or root cause is asserted.

## Review correction and regression coverage

An initial candidate discarded all 401/403/404 read bodies. Independent review
found that the existing read guard in `internal/auth/access_http.go` returns
403/`password_change_required`; discarding that valid code suppressed session
revalidation. The final implementation instead uses status **only as fallback**.
The independent counterexample and first candidate logs are retained separately.
New tests assert both transport preservation and the actual reader callback for
this case. Passing the initial 226 focused tests was not sufficient evidence for
that missing path.

`web/src/AuditReadStatus.test.tsx` contains 153 synthetic regressions:

- Six real adapter methods × three statuses × six malformed/missing-code bodies.
- Body-read failures, valid server codes, successful validation and unchanged
  409 snapshot errors, network/server retry behavior.
- Actual `useAuditRead` and `AuditWorkspace`: prior private row removal,
  no false “zero matches”, no scheduled retry over six seconds, correct session
  callback, explicit refresh recovery and ignored late old-generation 401.
- Proof-bearing submit/hide/restore failures retain their codes and uncertainty;
  the real mutation hook neither logs out nor replays a proof rejection.

Run from `web` using the cached locked dependencies and repository Node 24.19.0:

```sh
node node_modules/vitest/vitest.mjs run src/AuditReadStatus.test.tsx src/Audit.test.tsx src/AuditHistory.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/audit-api.ts src/AuditReadStatus.test.tsx
```

The unchanged Audit/AuditHistory baseline has 76 passing tests; final focused
coverage is 229 tests. Exact final full-suite results, failed controls, source
hashes, independent review and verified backup commit are recorded on #305.
Initial mock-function TypeScript errors were corrected without weakening
assertions or widening production types; their logs remain in the evidence.

## Integration and remaining gates

This independent slice is based on main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`. Its three files do not overlap the
25 currently open PRs, reserved parallel work or #297–304 offline candidates.
It does not change #301's AuditHistory reset or #299/#300 API files. Shared
reader/mutation hooks, authentication guards, permissions and workflow files are
unchanged. A future combined branch still requires compatibility verification.

Only a non-triggering backup branch is published. No audit/install/CI/PR/merge or
deployment is performed. APT/probe/R4/CI-selection frozen work and the denied
PR225 blob are untouched. These DOM/transport tests are not real browser,
PostgreSQL, AD/Windows or production acceptance. The Issue remains open for
permitted downstream gates and product acceptance remains 0/209.
