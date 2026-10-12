# Dictionary-v2 conflicted original recovery

## Reproduced defect and bounded repair

On main `524dc4b537985874b3a7159c9c13ef2c91e7c17d`, a sync with an
unknown outcome can first return an exact missing receipt, then reject a
manual same-key retry with `revision_conflict`. The original recovery path
retained the intent but offered the same obsolete pins and proof form after
the next receipt 404. The added focused regression fails on that unchanged
production code and passes after this repair.

Persist optional boolean `receiptOnly: true` alongside the existing nonsecret
original domain, operation key, and exact revision/generation strings when a
recovered retry returns `revision_conflict`. The marker prevents retry form
creation after further missing receipts, including remounts, session rotation,
source re-selection, and reload. Legacy intents without it retain their prior
recovery flow. Nonboolean marker values are rejected by the storage projector.
No proof, CSRF token, response body, or source data is added to storage.

This marker is necessary to keep the server's stale-pin rejection effective
across navigation and reload. Removing it restores the obsolete retry loop.
Its intentional consequence is that an unresolved original remains receipt-only:
there is no automatic replay, replacement operation key, or implicit discard.
A later authorized receipt for the original key can still advance to task
status and normal terminal-task dismissal. Source re-selection can inspect the
current configuration but does not overwrite the unresolved original.

The marker is a UI recovery constraint, never a server authorization decision.
Existing current-actor response validation, route/menu gates, CSRF, no-store,
abort handling, and server revision checks remain unchanged. This change does
not modify backend, schema, task registration, native guards, or product scope.

## Verification scope

- DOM regression: exact original key/pins and current CSRF/no-store on the
  conflicting retry; removal of both proof inputs and retry control; source
  re-selection, remount, fresh-module persisted read, repeated receipt 404,
  and eventual original receipt recovery
- Additional DOM tests: actor isolation, fresh access/receipt read on session
  rotation, expired-session and foreign-actor response rejection, strict marker
  validation, and nonsecret storage projection
- Existing real dictionary-v2 browser scenario extended: an authorized second
  actor changes the actual synthetic source; the stale form sends a real POST
  whose 409 delivery alone is dropped; receipt 404 and manual original retry
  409 come from the real API. Then verify proof cleanup, no replacement POST,
  exact original receipt queries, no-store error responses, authenticated current
  actor checks, browser Back/Forward/Back,
  reload, and explicit current-source re-selection. There is no fabricated API
  response, database write, or altered clock. Account creation and connection
  grant use the existing authorized bootstrap actor; each actor uses exactly
  ten sensitive actions, including password/MFA setup and rejected syncs.
  Login/logout does not reset the unchanged fifteen-minute production budget.

Local browser execution was attempted with
`python3 scripts/test_auth_e2e.py --suite directory-v2` and is blocked because
Docker is not installed. Playwright discovery is a separate source/discovery
check, not browser execution. Exact-head required CI must run the real
browser/API/worker/PostgreSQL/synthetic TLS LDAP scenario before merge.
Real AD, Windows compatibility, capacity, full product acceptance, and
production deployment are not established by this repair.


## Initial CI failure and harness correction

[Run 38171401742, directory-v2 job 114564893753](https://github.com/yunpiao/adtr/actions/runs/38171401742/job/114564893753)
failed on head `060952825667f5681f5c7d217df4dfd3a3279669`: the first new
stale-sync request returned HTTP 429, where the test required the real 409
revision conflict. The producer's original scenario already spent ten sensitive
actions, including password change and both MFA setup calls. The two new syncs
exceeded that budget; the earlier nine-action estimate omitted setup calls.

The harness now performs account creation and its connection-purpose grant
with the existing authorized bootstrap actor. This moves two setup actions
without changing resource scope or the producer that owns the original and
recovered v2 sync requests. Both actors then stay within ten actions. No rate
limit, authentication check, clock, native response guard, or expected response
was weakened. This source correction still requires fresh exact-head CI; the
failed run remains evidence and is not reclassified as passing.


## Error-envelope assertion correction

[Run 38173666375, directory-v2 job 114573301415](https://github.com/yunpiao/adtr/actions/runs/38173666375/job/114573301415)
reached the expected real 409/no-store response after the quota correction,
then failed because the new test expected `X-ADTR-User-ID` on an error.
The existing directory server emits that header only for successful responses;
error responses contain the bounded error envelope without it. The production
client likewise validates actor headers on successful responses only.

The two new 409/404 assertions now require the error header to be absent.
Authenticated `/api/auth/me` reads before and after recovery verify the same
producer ID; successful directory response actor-header guards, exact errors,
no-store checks, native completion checks, and backend behavior are unchanged.
A focused client regression covers headerless 409/404 while existing tests
still reject every successful route with a missing or foreign actor header.
This harness correction needs a new exact-head CI run; neither prior failed
run is claimed as passing.
