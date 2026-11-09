# Credential-source read denial recovery

Engineering candidate for #312, within #16/#17/#18 F01/F03 B2. Baseline remote
main is `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`. This is not a product-acceptance
claim; acceptance remains **0/209**.

## Reproduced failure and contract

`account-reference-contract.md` defines current-identity/exact-scope source
metadata and original-actor-bound source receipts. `domain-contract.md` defines
401 unauthenticated, 403 permission rejection and 404 absent/foreign/ungranted
objects. A missing receipt does not prove the original write failed.

In actual `DomainCredentialSource` and `SourceRecovery`, synthetic JSON 401
`unauthenticated` caused one GET and one `sessionChanged` callback. The same
HTTP status with HTML caused `invalid_response/401`, no session callback and a
second GET after two seconds. HTML 403 similarly retried and displayed generic
uncertainty instead of the permission message. Both components use `useTaskRead`,
which classifies session changes and retries by error code, not status alone.
The HTTP status was retained; its read-denial meaning was lost at JSON decoding.

This establishes behavior under an unreadable response, not evidence that the
current backend or a production gateway emits HTML. Existing failures already
removed old data and editing controls; no permission bypass or successful write
was observed.

## Narrow change and boundaries

Only `domain-source-api.ts` changes. When `response.json()` rejects, GET plus a
non-success 401/403/404 maps to `unauthenticated`/`forbidden`/`not_found`. This
restores existing session handling, permission text and receipt-not-found text,
and stops automatic retries for these definite read failures. Explicit refresh
remains available; 403 is not treated as a forced logout.

The GET guard is necessary because the same transport serves reference, custom,
detach and create-unconfigured POSTs. Removing it would classify an unreadable
write response as definitive and could discard the original intent even though
the write may have committed. Those POSTs retain `invalid_response` uncertainty
and same-key GET recovery. A 404 recovery lookup never proves non-submission,
never clears an unresolved intent, and never creates a new mutation key.

Removing the new fallback restores the reproduced callback/retry defect. Its
intentional side effect is that known read denials no longer automatically retry;
the user can explicitly re-read after correcting the session or access. No new
configuration, permission, storage, dependency or server capability is added.

Parsed JSON errors retain the original policy verbatim, including arbitrary
string codes, password-change/tenant/conflict errors and malformed JSON shapes
that decode successfully. Other HTTP statuses remain `invalid_response` when
unreadable. Successful DTO, actor-header and request/receipt identity validation,
network errors, cancellation, request-generation suppression, POST contents,
CSRF, grants, credentials and shared hooks/components remain unchanged.

## Offline verification

New `DomainSourceReadStatus.test.tsx` uses actual APIs and components with
synthetic fetch responses, not mocked API modules. It covers both reads across
empty/HTML/truncated JSON and body-stream failure, exact JSON error policy,
success/actor mismatch/invalid DTO, other statuses, network/abort; all four POST
routes retain uncertainty and the original idempotency key. DOM coverage verifies
session callbacks, no denial retry, explicit recovery, retryable 503, stale-data
removal, unmount, new request/key generations, repeated receipt clicks, no POST
replay, intent retention and successful same-key recovery.

Commands use cached dependencies and the existing Node 24.19.0 toolchain:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/DomainSourceReadStatus.test.tsx src/DomainCredentialSource.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/domain-source-api.ts src/DomainSourceReadStatus.test.tsx ../docs/domain-source-read-status.md
cd ..
python3 scripts/verify_requirements.py
```

Baseline original suite: 23 passing tests. Final results, old-main red controls,
independent review and remote file hashes are recorded in #312 evidence. No old
test is changed. This branch is independent of the selection-reader (#310) and
credential-catalogue pagination (#311) candidates.

No audit, install, aggregate `make check`, PR, CI, database, browser E2E, real AD,
deployment or merge is run for this candidate. A new non-triggering backup branch
is a source backup, not a CI pass. The Issue remains open for subsequent gates.
