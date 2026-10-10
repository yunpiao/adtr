# F14: stored dictionary-2 user search and pinned detail

Refs [F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-038/039.
This is a bounded read-only engineering slice, not source-system parity or product
acceptance. Implementation starts from main `26f1463e98d2064182ad3f89e8663af7a0d9ef24`
(tree `38fcc8d98fda747a6ee882e816cba3573caeb9d3`). The earlier design inspected
`1d2930ca9cc146fbc83226f7effb0c488435141c`; it was design evidence only.

## Source and dependencies

The source workbook is `2026-10-03-AD功能清单.xlsx`, SHA-256
`d59ccdf3eac3865b20c1f919f2cbc881df625714b2c499342b4cc1bcf2efae54`.
AD-F-038/039 are rows 42/43; original field/control references remain in the
catalog and full definitions in Issue #30. None is silently marked implemented
by a shared component, existing field name or synthetic fixture.

F12 [#25](https://github.com/yunpiao/adtr/issues/25) supplies the immutable v2
producer/observation, F47 [#11](https://github.com/yunpiao/adtr/issues/11) supplies
current tenant/domain authority, and F38 [#26](https://github.com/yunpiao/adtr/issues/26)
remains a dependency for the broader export feature. These issues and G01/G02/G03
remain open. This slice consumes existing producer/resource contracts and has no
export path; it does not imply those wider dependencies are product-accepted.

No schema migration, materialized asset table, JSONB text projection or index is
added. Reads reuse the exact directory-v2 eligible observation SQL and full codec
validation. Source storage remains bounded at 10,000 objects/16 MiB and public
responses at 8 MiB. Scanning this bounded body is not large-directory acceptance.

## API and exact identity

- GET `/api/user-assets/v2`: user-only list
- GET `/api/user-assets/v2/detail`: one user selected by exact observation/GUID

Only GET is allowed (405 plus `Allow: GET` otherwise); unknown paths are 404.
GET bodies, duplicate/unknown parameters, invalid UTF-8/percent encodings, raw
query length above 32 KiB and noncanonical/overflow numbers are 400
`invalid_input`. All responses are `Cache-Control: no-store`. Success is bounded
before any response bytes, with JSON/nosniff and authenticated `X-ADTR-User-ID`.
No task engine, credential decryptor, task submission or LDAP dependency is
injected into either handler.

Both require singleton nonempty `domainId`, `expectedRevision` and
`expectedCredentialRevision`. The opaque ID grammar is the existing domain ID
grammar, and revisions are canonical decimal strings 1..9223372036854775807.
Current source pins are validated in the same authorization transaction as the
observation read. A caller's tenant, role, actor or row body is never accepted.

The list also accepts `observationId`, `search`, `pageIdx`, `pageSize` only:

- `observationId` may be omitted on initial page 1 or explicit refresh; it is
  required for page 2+, all subsequent UI reads after capturing a pin, and detail
- `pageIdx` defaults to 1, range 1..10000; `pageSize` defaults to 10 and is exactly
  10/20/30/40/50. Out-of-range pages within the grammar return empty rows and the
  actual filtered total, not a coerced page. `-1` is never fetch-all
- `search` is omitted/empty for no predicate, otherwise at most 50 UTF-16 units
  and 200 UTF-8 bytes. Whitespace, valid controls/NUL, punctuation and Unicode
  composition are preserved; no trim, normalization, tokenization or wildcard
  language is applied. Search is `strings.Contains(strings.ToLower(field),
  strings.ToLower(search))` independently on non-null SAM/SID/mail/DN fields
- Search runs only in bounded Go memory over validated public raw values, never
  PostgreSQL text/JSONB, SQL, LDAP, regex, base64 storage or display escapes
- GUID, description and time are not searched; a match cannot span field borders
- `userStatus`, `domainName`, `startTm`, `endTm`, `updateTm`, `creatTm`, `userType`,
  `sAMAccountName`, `scoreSort`, `overviewType` return 422
  `unsupported_user_asset_filter` after singleton/UTF-8 structure validation

List order: current authority → source pins → newest eligible observation (or
exact supplied pin) → complete body validation → user-only literal filter →
GUID ascending → filtered total/page. Corruption outside the requested page or
filter invalidates the full response. Cancellation never yields partial rows.
A newer observation cannot silently replace a supplied pin. The latest empty
observation cannot fall back to an older nonempty one.

List success has exactly `dictionaryVersion:2`, `selection`, `available`, `list`,
`page`, plus `observationId` and `source` only when available. Selection has exactly
`domainId`, `revision`, `credentialRevision`. Page has `pageIdx`, `pageSize`,
`total`, `totalPage`. No eligible unpinned observation is `available:false`, empty
list/zero totals without observation/source. Real empty/no-match is
`available:true` with its actual observation/source. Unavailable totals are not
observed user counts.

Detail requires exactly the three source pins, `observationId` and `objectGUID`.
GUID is canonical lower-case 8-4-4-4-12 hex, without invented UUID version bits.
Success has exactly `dictionaryVersion:2`, `selection`, `observationId`, `source`,
`object`. Only a user from that complete validated observation may satisfy it;
no SAM/DN/name, latest observation or other-domain fallback is permitted.

## Factual values and authorization

List rows and detail objects contain the unchanged ten `PublicObjectV2` keys:
`objectGUID`, `distinguishedName`, `kind` (always `user`), `objectClass`,
`samAccountName`, `userAccountControl`, `objectSid`, `mail`, `description`,
`whenCreated`. Null means requested but not returned; it is not zero, empty,
unauthorized or deleted. UAC 0 and year 0001 UTC remain real present values.
Description retains one-string-array semantics; mail retains raw accepted text.
The source's `completed_at` is observation completion, never last login/current
reachability or an AD point-in-time snapshot. Absence is not deletion evidence.

Every read uses the existing exact schema gate, schema→tenant→actor lock order,
live non-forced session, `domains.read` AND `directory_assets.read`, eligible
unexpired tenant within its domain limit, and current active F47 scope. Admins
have no bypass. Task permissions/ownership, account metadata rights, producer
use grants, password/TOTP proofs and collection flags are not read prerequisites.

The shared loader retains dictionary/purpose/task-kind/payload-version/succeeded
state, all tenant/domain/actor/opener/fence/attempt joins, current connection,
credential/account/policy pins, digest/count/canonical validation and decoded
source equality. Raw bodies and decoded failure values are discarded. Disabling
both default-off producer flags or revoking producer-use grants does not remove
independently authorized stored reads. Reads do not create tasks/uses or mutate
business data; existing sanitized infrastructure failure attribution may write.

- 401/403 `password_change_required`: discard private view/revalidate session
- 403 forbidden/tenant eligibility: discard protected view, no stale fallback
- 404 `not_found`: indistinguishable inaccessible domain/missing or non-user GUID;
  discard source/list/detail and ask for source reselection, never claim deletion
- 409 `selection_changed`: current source changed; discard selection/pin/detail
- 409 `directory_observation_unavailable`: exact pin is invalid/ineligible; no
  partial success or silent fallback; explicit source revalidation/refresh only
- 422 `directory_limit_exceeded`: complete encoded result exceeds fixed cap
- 503 schema/dependency or network: safe bounded error; retry preserves established
  observation/source identity. An initial unpinned request still means latest

Source revision conflict precedes observation/policy invalidation after access
is known. Inaccessible IDs cannot disclose revision conflicts. No age-based
freshness TTL or real-time status is invented.

## Browser state

The separate `user-assets-v2` page uses SavedSourcePicker choose→resolve, with
exact permission introspection. Source selection is not authorization. Searches,
page size and pages retain the captured observation; only explicit refresh can
request newest. A source switch resets query/page/pin/detail. Draft and applied
search are separate, with stale controls disabled while they differ.

Both parsers validate exact shape, source/selection/observation/GUID identity,
page arithmetic/row length, increasing unique GUIDs, user-only objects, factual
null semantics, actor header and streaming 8 MiB/UTF-8 bounds. Generation and
abort protections reject delayed responses after source/query/navigation/session
changes, including ignored abort and repeated same-parameter refreshes.

Inline details fetch the pinned endpoint. Close/Escape, source/filter/page/
refresh changes and unmount invalidate detail; Close returns focus when its
originating row still exists. App Back/Forward and cross-tab session invalidation
cannot resurrect a private panel. React text nodes and `directoryV2DisplayText`
render hostile/control text inertly and visibly, with no automatic links.

## Verification and limits

The clean base's `make check` passed (759 DOM tests; Go race/vet/build and Python
contracts; dependency audit zero vulnerabilities). New tests and exact candidate
execution results are recorded separately in the PR and delivery ledger; this
baseline result is not evidence that new code passed.

New tests include `internal/domains/user_assets_v2*_test.go`,
`internal/auth/user_assets_v2*_test.go`, `web/src/UserAssetsV2*.test.tsx`, and two
real suites `user-assets-v2` / `user-assets-v2-readers`. The independent fixture
mode produces actual TLS LDAP responses with more than 10 users, null/hostile
values and non-user inheritance decoys while retaining old v2 fixtures. The
reader suite uses real headed native tabs. HTTP/PG synthetic-executor tests are
explicitly labeled and do not substitute for the Worker/TLS LDAP/browser chain.

Required gates remain full `make check`, real PostgreSQL non-auth/auth shards,
container lifecycle, both new browser suites plus existing browser regressions,
independent actual-diff review, exact-head required CI and current-main merge
compatibility. Missing Docker/browser is a blocked stage, not a pass.

Not implemented/accepted: broader source filters/aliases, name/displayName,
active/disabled/locked business status, login/password times, risk/score,
applications, ACL/SPN/control labels, group/relationship counts, reset operations,
exports, complete F14/source compatibility, real AD/eight Windows versions,
capacity, production deployment or product acceptance. The catalog remains
0/209 product accepted and Issue #30 must stay open.
