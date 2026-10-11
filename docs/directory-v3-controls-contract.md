# Dictionary 3 collection and exact-purpose governance UI

Status: coordinating review approved, implementation and independent review in
progress, 2026-10-11. No real-browser or product acceptance claimed. Base main `524dc4b537985874b3a7159c9c13ef2c91e7c17d`,
schema19. Refs [F12 #25 / AD-F-034](https://github.com/yunpiao/adtr/issues/25).
Backend facts and representation limits: [dictionary3 contract](directory-v3-contract.md).
The separate F14 user-asset list/detail workspace is outside this slice.

## Surface and bounded authority

Add explicit `#directory-v3` collection and `#directory-credential-use-v3` cleanup
pages under existing collection navigation, and an explicitly labelled dictionary3
panel on operation-account detail. Existing v1/v2 URLs, default navigation,
responses, local recovery keys and controls remain unchanged. No schema, purpose,
permission, backend capability, grant migration or production configuration change.

Collection selects an existing current saved domain. Versioned reads use only
`/api/directory/v3/{observation,receipt,task}`; explicit fresh-proof mutations use
only `/api/directory/v3/{sync,cancel}`. Route fixes dictionary3. Read access requires
current domains/directory-assets scope; task and receipt reads additionally require
tasks read. Sync/cancel require current directory-assets/tasks write and the existing
same-origin, CSRF and password/TOTP checks. Strict parsers enforce dictionary3,
exact fields, current actor header, source and task pins, bounded UTF-8/JSON reads.
All twelve factual fields may be rendered as safe text; primaryGroupID zero remains
zero, missing is 未返回, SPN ordering and visibly escaped control text preserve
provenance without membership, risk, ownership or authentication inference.

Governance uses only `/api/directory-credential-use/v3/{accounts,grants,roles,effective,mutation,grant,revoke}`
and fixed `domain.directory_read.v3`. Builtin-administrator and existing current
scope checks still govern saved grant metadata and grant/revoke; effective-self
introspection follows existing reader permissions. The cleanup catalogue retains
revoke access under supported tenant-expiry/capacity conditions independently of
live role eligibility. Saving a grant does not connect, decrypt or start collection.
V1/v2/probe grants never imply v3 authority and are never copied.

`consumerEnabled` reports registered code capability only. The UI explicitly
separates this capability, current explicit grant/eligibility and deployment state.
Both `ADTR_DIRECTORY_READ_ENABLED` and `ADTR_DIRECTORY_READ_V3_ENABLED` default
false and are required for collection; the current API does not expose their live
values, so the UI states that deployment state is unavailable rather than claiming
enabled/disabled. No toggle is added. A disabled gate prevents new collection while
current-authorized historical observation, receipt, task and cancellation paths
remain available. An API rejection is explained using its actual error code.

## Unknown outcome, cancellation and navigation

When required for original-operation recovery, persist only an explicit nonsecret allowlist of original actor,
v3 profile, domain/account/role, revisions and operation key in a v3-only namespace.
Never store password, TOTP, CSRF, private response rows or full request bodies.
Unknown sync or grant/revoke responses keep the original intent, query its exact
receipt and never automatically replay. A user-requested sync retry preserves the original
key and pins and requires fresh proof. Governance recovery remains receipt-only,
matching the existing v2 boundary; it offers no mutation replay or new key. A
not-found receipt is not proof of failure.
Version conflict removes obsolete forms and requires explicit current-state review.
An unknown original sync whose explicit retry conflicts becomes receipt-only; its
original key/pins remain recoverable across navigation, without another stale retry.
A different domain/account/version cannot silently replace a pending intent.

Cancel requests require fresh proof and retain the original task UUID. An unknown
cancel outcome reloads task status; it does not submit another cancellation. Task
terminal status is not evidence of executor return or released dependencies. The
UI explains cancel_requested and continues bounded existing status polling.

Closing/collapsing forms, workspace navigation and browser Back/Forward discard
proof drafts and private response state. Re-entry rechecks current identity and
permissions; it may recover only the original nonsecret v3 intent. Session/account
change, cross-tab invalidation and trusted focus/visibility revalidation abort old
reads and prevent delayed old-actor responses restoring controls or facts. Existing
native stream/no-store completion guards remain unchanged.

## Files and integration

New v3 collection/governance API, intent, workspace and focused test modules;
new v3 browser scenarios and isolated suite registrations. Narrow registrations in
App, SOC navigation and operation-account detail follow current main. Concurrent
F14 changes own shared files until coordinated integration; this slice must not
rewrite the user-asset list/detail or its facts implementation. Reuse the existing
bounded reader and safe-text semantics without changing old parsers. New contract
and evidence documents are slice-local; global delivery/requirements updates stay
with their assigned owner.

## Mandatory verification gates

- Record unchanged-base checks, then full make check, format and browser discovery
  on the final source; focused strict parsing/purpose/recovery/identity/Back tests
- Real browser → protected API → registered Worker → owned synthetic TLS LDAP →
  isolated PostgreSQL → successful v3 observation, exact field values and task pins
- Real committed sync and governance response loss, exact receipt recovery without
  duplicate POST or key substitution, revoke and old-purpose isolation
- Empty observation; real delayed paged I/O cancellation, proof retention during
  progress, executor-return witness and zero dependencies using SELECT-only observer
- Actual custom read-only actor; forbidden write/grant metadata; cross-tab account
  switch with delayed genuine response, trusted native focus/visibility fallback,
  interrupted forms and browser Back/Forward; no fabricated successful API bodies
- Actual desktop 1440 and mobile 390 pixel inspection with proofs cleared; retain
  all existing suites, time/resource/security limits, parallelism and verify gate
- Independent actual-diff security review, exact-head full CI, current-main/base
  compatibility and coordinated normal merge; postmerge evidence is separate

Local missing Docker/PostgreSQL/browser capability is a blocked gate, never a pass.
All mutating fixtures are explicitly disposable synthetic accounts/databases/LDAP.
Real AD, eight Windows versions, full source parity, capacity, incremental/deletion,
production enablement and product acceptance remain open. No deployment or real
grant/credential/gate changes. F12 and all unaccepted Issues remain open, 0/209.
