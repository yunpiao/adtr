# Authenticated, nonempty message isolation evidence

## Status and source baseline

This change adds a dedicated `messages-isolation` browser acceptance suite. No passing run or screenshot inspection is claimed by this document. Implementation was prepared against main `39eb1077df159c6d83cd7f0c4f5e504cd335066e`, tree `a65e834d829c16e5866e745f1cde812ac9e897a2`. Independent source review and exact-candidate CI, followed by inspection of successful evidence, remain required before acceptance. Local project execution is on HOLD; authoring and hash checks are not test results.

## Intended proof

Two isolated browser contexts authenticate synthetic A and B using real login, password change and MFA enrollment. A creates B with the existing tasks-readable/writeable role flow. Each identity submits three distinct `infrastructure.health/platform` tasks over HTTP with its own proof. The ordinary production worker projects their successful terminal events. Read-only queries of the owned PostgreSQL fixture bind tenant, actor, task, event, outbox and message, with no direct insertion of messages or read state.

Both identities must have three own messages and one persisted own read before cross-user probes. The suite checks both directions: own page/locate/receipts, foreign result and operation 404 `not_found`, own result with foreign IDs 400 `target_not_in_result`, atomic mixed selection rejection, selected/all bulk totals, and protection of the other user's complete ordered persistence baseline. A foreign cancel is checked against a completed owned operation; deterministic active cancellation is not covered here. Same-context second tabs and Back/Forward must show only their own data, explicitly querying if React-local snapshot state was lost.

Existing `messages` and chained `messages-bulk` suites remain mandatory regression coverage, including receipt recovery, session rotation, reader roles, close/reopen recovery, authorization-version invalidation and active cancellation. This suite neither replaces them nor claims new cross-session revocation, cross-tenant, reader-only, LDAP, notification-delivery or whole-product acceptance. Offset 0/1 with limit 1 proves API pagination, not two 20-row UI pages. HTTP platform message `domainId` is empty; persisted task/event domain is `platform`. Projection and authorization versions are checked from the owned database, not invented HTTP fields.

## Runner and CI interface

Run in the authorized CI environment with:

```sh
python3 scripts/test_auth_e2e.py --suite messages-isolation
```

The explicit suite choice receives `ADTR_E2E_USERNAME`, `ADTR_E2E_PASSWORD`, `ADTR_E2E_BASE_URL` and `ADTR_E2E_DB_CONTAINER` for the fresh owned harness. The DB container handle is required for independent read-only persistence evidence; removing it must fail setup rather than permit weaker HTTP-only evidence. Credentials remain in process memory and are not evidence. No fixture binary, worker pause/controller or LDAP fixture is enabled for this suite.

The Playwright output is `web/test-results/messages-isolation`. The suite is a separate browser matrix entry, not appended to the existing messages-to-bulk chain. Changes to MessagesWorkspace and TaskWorkspace select the new entry in addition to every existing mapped suite. Unknown/shared/auth/backend changes retain conservative full selection.

Only this suite receives the 720-second runner timeout, with a 660-second test budget inside the existing 20-minute matrix job. These bounds accommodate real TOTP waits, six terminal task waits and teardown; removing the special runner timeout restores the insufficient 420-second default. Raising them indiscriminately would mask slow or stalled behavior and consume more CI capacity. All snapshot-bearing assertions have an additional 60-second combined deadline after projection; the longer outer timeout never extends snapshot lifetime. Each identity plans at most seven snapshot admissions, never exceeds the existing hard limit eight, and preserves the existing sensitive-action budgets A8/B6. Exhaustion or expiry fails, not an isolation denial.

## Successful evidence and privacy

After all assertions succeed, the suite writes `messages-soc-isolation-evidence.json` with `status: passed`, checkout commit/tree/dirty state, workflow commit/run/attempt, source and built artifact SHA256, browser version, synthetic identities, real task provenance, positive histories, safe request statuses/bodies, invariant deltas and quota/proof usage. The runner validates this successful evidence after the browser command exits successfully; finding a matching artifact filename alone is not passage.

Four `messages-soc-isolation-*` PNGs show A and B at 1440×900 and 390×844 after exactly one own read. Each full-page image has its actual browser viewport, identity and SHA256 record; PNG height may exceed viewport height to retain the complete history. `proofEmpty` records the DOM safety gate, not human pixel inspection. A reviewer must inspect actual pixels before publishing or treating the screenshots as acceptance evidence.

Trace, video and automatic failure screenshots are disabled. Explicit screenshots follow closure of proof dialogs and verification that credential inputs are empty/absent. Transport and proof-action failures use fixed safe categories; raw errors, call logs, headers, cookies, passwords, TOTP, CSRF, storage state, DSNs and authentication request bodies must not be logged or attached. No hidden transport retries convert uncertain writes into apparent denial.

The existing successful-only `messages-soc-*` upload glob and seven-day retention remain, with matrix/run/attempt-specific artifact names separating old messages evidence from isolation evidence. No raw report, network trace or failure artifact is added.

## Acceptance record still needed

Record the reviewed candidate commit, exact CI run/attempt and job conclusions; inspect and hash the four safe screenshots and manifest; distinguish observed runtime results from planned assertions. Only after current-commit required checks pass and independent review clears the implementation may the authorized merge proceed. Verify merged main and its required checks separately. Until these records exist, runtime response bodies, actual phase duration, admission counts and screenshot pixels remain unverified.

## First CI observation and bounded repair

PR #197 head `e846ca96`, CI run `39527647131` attempt 1 (tested checkout `563114a9`) completed the new browser test successfully in 3.8 minutes, then failed the runner evidence validator with its fixed error category. This is a failed acceptance job, not accepted evidence or screenshot review. The retained safe runtime diagnostics reported normal API/worker startup and cleanup without panic; no accepted manifest or PNG was uploaded. The generic failure did not identify the first rejected condition.

Source inspection found a deterministic artifact-set mismatch: Playwright `testInfo.attach` copies each path attachment into an additional `attachments/` file with the same `messages-soc-*` prefix, while the upload-wide validator correctly requires exactly the four original PNGs and one manifest. The repair removes redundant report attachments while keeping those original five files, their validation, the workflow upload glob and retention unchanged. This source conflict is established; whether it was the first branch reached in that failed run is not recoverable from the generic log.

The validator now reports only a fixed, source-literal validation phase (file discovery, checkout/source/bundle binding, PNG, payload/artifact/ledger schema, provenance, negative matrix, bulk ledger or negative ledger). It still fails closed, suppresses the underlying exception and never prints manifest values, raw exceptions or failed evidence. Phase diagnostics do not bypass validation or authorize uploading failed artifacts. The corrected candidate still requires independent source review, a fresh exact-commit CI result, and actual screenshot pixel review.
