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
  exact original receipt queries, no-store/current-actor headers, browser Back/Forward/Back,
  reload, and explicit current-source re-selection. There is no fabricated API
  response, database write, or altered clock. The producer uses nine sensitive
  actions, within the existing ten-action limit.

Local browser execution was attempted with
`python3 scripts/test_auth_e2e.py --suite directory-v2` and is blocked because
Docker is not installed. Playwright discovery is a separate source/discovery
check, not browser execution. Exact-head required CI must run the real
browser/API/worker/PostgreSQL/synthetic TLS LDAP scenario before merge.
Real AD, Windows compatibility, capacity, full product acceptance, and
production deployment are not established by this repair.
