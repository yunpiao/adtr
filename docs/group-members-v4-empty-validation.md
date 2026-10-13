# Dictionary 4 membership: real empty observation validation

Scope: Issue #151 and F14 #30; a new independent `group-members-v4-empty`
suite proves only an entirely empty saved dictionary presented as zero groups.
It does not cover a nonempty dictionary with no groups, nonempty-to-empty
refresh, more than 25 groups, real AD, or complete product acceptance.
Existing membership suite, its 12 PNGs and null/returned-empty semantics remain.
No production implementation, schema, purpose, permission or default changes.

## Chain and acceptance artifacts

The runner must create its own fresh PostgreSQL, API, registered Worker and
certificate-checked TLS synthetic LDAP fixture. The existing fixture switches
`directory_enabled=True`, `directory_v4=True`, `directory_empty=True` are all
required: the last switches fixture output to one terminal page and zero objects;
omitting it restores the old nonempty fixture and must fail this acceptance.
No SQL inserts/updates of observations or request interception are used. SQL
queries run in READ ONLY transactions against the owned random test container.

1. Real producer UI selection returns `available=false` before collection
2. Existing explicit v4 grant and sync produce `objects=[]`, count 0, source
   pages 1, successful task and quiesced use with `executor_returned` and no
   remaining dependency
3. Revoke producer v4 use grant through its production route and verify both
   effective denial and zero matching allowed grants in PostgreSQL
4. A different custom reader has exactly domains/directory_assets read, no
   write and no use grants, and access only to this synthetic domain. First
   password change, MFA enrollment and subsequent logout/login with MFA are real
5. Its actual UI response is `available=true`, pinned observation/source,
   page 1/25 and total/totalPage 0. The UI says “本页观测未返回组对象；成员情况未知。”;
   both paging buttons are disabled and neither a group nor member detail exists
6. Reread pins the same observation. Task/use/observation counts, saved bytes/hash
   and reader authority are identical before/after. No business write or member
   detail request is emitted by the reader UI

Output is `group-members-v4-empty-evidence.json` with closed root fields
`format`, `provenance`, `noObservation`, `backend`, `producer`, `reader`, `ui`.
The independent validator binds the exact checkout/tree/event/run/attempt and
47 source digests, saved body SHA-256, raw API facts and rendered source values.
Screenshots are `group-members-v4-empty-reader-1440.png` (1440×900) and
`group-members-v4-empty-reader-390.png` (390×844), with hash and size checks.
Actual pixel review remains necessary; a matching PNG hash is not visual review.

## Authentication and timing budget

Production `Service.rate` uses the same per-actor sensitive bucket for password,
MFA and privileged access operations: 10 attempts in a 15-minute window. The
suite uses the already validated three-actor bootstrap, without bucket resets:

- Bootstrap 10: password/MFA 3; domain/group/association 3; reader role/scope/user
  3; producer user 1
- Producer 10: password/MFA 3; operation account/connection grant/source 3;
  v4 denied sync, grant, successful sync and revoke 4
- Reader 3: password/MFA; the later MFA login uses the separate login bucket

Fresh TOTP counters are awaited; server clocks, replay protection and rate limits
are unchanged. Test/runner bounds are 630/660 seconds. These are ceilings, not a
measured runtime. A timeout requires stage/MFA diagnosis, not a relaxed limit.

## Local verification and remaining gates

In the dot cloud checkout, focused Workspace DOM: 37/37, combined Workspace and
response-observer regression: 59/59, TypeScript noEmit, Vite build, formatting
and Playwright discovery (one test) passed. Vite retained its existing >500 kB
chunk-size warning; no threshold was changed. DOM uses synthetic transport to exercise
the real component branch; it is not LDAP/PostgreSQL/browser acceptance.

Real suite and screenshots have not run locally: PostgreSQL/Chromium socket
access was denied in this environment. No retry through another route, npm ci,
audit network fetch, fabricated artifacts or acceptance claim is made. The
new validator/runner require independent review; final integrated checks, real
CI artifacts, pixel review, exact-head CI and postmerge CI are separate gates.
No publication or deployment was performed for this local candidate.
