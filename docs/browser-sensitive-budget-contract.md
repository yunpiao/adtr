# Browser fixtures and real sensitive-action limits

The application allows ten sensitive actions per database user ID in a fifteen-minute window. Password change, MFA enrollment and MFA confirmation consume three slots. Proof-checked business failures also consume a slot because rate accounting commits independently from the business transaction. Replay accounting depends on handler order: schedule/task/audit POST replays reach proof and consume a slot; committed domain-source and credential-use replays return before proof. Logout, another tab, a new browser context or a session rotation does not clear an actor's rate bucket. Decoder or permission rejection before proof does not consume this bucket.

Source: internal/auth/service.go (rate and authenticated action dispatch), internal/auth/access_http.go (requireAccessProof), and each actual HTTP handler's ordering. The IP-level bucket is a separate constraint.

A source audit of the fifteen non-directory browser suites found five deterministic setup failures under their unchanged sub-fifteen-minute deadlines:

- operations: planned thirteen actions; action eleven is the expected-conflict domain deletion
- account-references: planned fifteen actions; action eleven is the real binding whose committed response is deliberately lost
- account-reference-barrier: bootstrap eleven, existing manager eight; bootstrap task admission is action eleven
- maintenance: planned twelve actions; reader-role creation is action eleven
- audit: planned thirteen actions; snapshot restoration is action eleven

These counts describe the old unexecuted source paths, not observed browser results. Other suites were at or below ten per actor by the audited source, which does not prove their runtime passes. Directory suites were corrected separately.

The correction must use real UI-created and enrolled actors, or redistribute writes between existing authorized actors. Keep all scenario assertions, current-password/TOTP proofs, idempotency ownership, task ownership, response-loss recovery and live permission checks. Never clear rate records, change clocks, relax production limits or omit denied writes to manufacture a pass. Never replace actual responses or task/ledger states with fabricated data.

Only local source/static/unit evidence is available until the real isolated API/worker/PostgreSQL/browser suites run. No real AD, production acceptance or remote CI result is implied.

## Saved corrections

The account-reference barrier now uses its already-created manager for binding. Bootstrap remains the task and SQL evidence owner. Its ten actions are setup3, domain/group/assignment3, account/grant2, manager creation1 and task admission1. The manager uses nine: setup3, binding/detach2, denied replacement/removal2 and successful replacement/removal2. All forms and next-proof timing remain prepared outside the actual acknowledgement lock.

Operations now uses bootstrap7 and operator10. Bootstrap performs its password/MFA setup, domain/group/assignment and operator creation. The distinct operator performs its own setup, account creation, metadata update, stale update rejection, credential replacement, blocked domain deletion, account deletion and domain deletion. Lost-response recovery and stale tabs remain with that operator.

Account references now uses bootstrap9 and consumer10. Bootstrap performs setup, domain/group/assignment, account creation, role grant and consumer creation. The distinct consumer performs setup, metadata update, stale reference rejection, committed binding, connection test, grant revocation, detach and custom source change. The original one-member grant review remains asserted; the new same-role member is explicitly verified eligible before it consumes the grant. Recovery remains with its submitting actor.

Maintenance uses bootstrap6 and producer10. Bootstrap performs setup, producer creation and reader-role/user creation. The producer owns every schedule and task operation: its setup, schedule create/enable/pause, enable replay, archive/restore and create replay.

Audit keeps the original bootstrap export owner and stable login filter: bootstrap9 covers setup, manager creation, three exports and snapshot hide/restore. The distinct manager8 covers setup, initial hide/restore/protected rejection and reader-role/user creation. Separate contexts are fixture-managed and closed; no bootstrap re-login adds a second login row.

Audit and maintenance explicitly disable automatic trace, screenshot and video capture, and sanitize password/TOTP fill failures. This preserves real authentication proofs while preventing automatic failure artifacts from recording proof inputs. All original assertions and deadlines remain.

## Verification

Final local make check passed: all Go race/vet/build checks, fifteen Python contracts, 559 frontend tests, TypeScript/build and zero npm audit vulnerabilities. Focused formatting and Playwright discovery passed for all five files. Independent source review confirmed all 472 original expect calls and all deadlines remain, with no unresolved source finding. No real browser/PostgreSQL/AD execution or remote publication occurred; these results are not product acceptance.
