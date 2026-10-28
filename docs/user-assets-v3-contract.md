# F14: versioned dictionary-3 user workspace

Status: bounded scope approved by the coordinating developer on 2026-10-11;
implementation in progress, independent code review pending. Product acceptance remains 0/209.
Engineering [#125](https://github.com/yunpiao/adtr/issues/125).
Actual implementation base: main `524dc4b537985874b3a7159c9c13ef2c91e7c17d`,
same tree as research base PR120 candidate `6060139593fff54b0ff5976b992983a66a90e9fb`.
Publication must incorporate actual merged main and rerun current-head checks.
Refs: [F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-038/039,
[F12 #25](https://github.com/yunpiao/adtr/issues/25),
[dictionary-3 producer and storage](directory-v3-contract.md),
[v2 search](user-assets-v2-contract.md), [exact filters](user-assets-v2-filters-contract.md).

## Source evidence and bounded scope

Issue #30 FLD-0395 identifies SPN as a string array; FLD-0397 identifies a
primaryGroupID text field but supplies no source semantics. The v3 producer's
reviewed explicit local profile supplies uint32/null and raw Unicode array/null.
The UI labels primaryGroupID as a numeric RID, preserving zero and the entire
uint32 range rather than claiming legacy text API compatibility. No name,
relationship, group count, risk, SPN host ownership or authentication inference.
FLD-0330/0334 and the approved v2 time-filter contract retain their exact semantics.
The source workbook identity remains the one recorded in the v2 contract.

This slice adds a read-only user-list endpoint and a SOC user browsing/detail
workspace. Collection and credential-governance v3 UI are separate work: they
involve fresh proof, receipts and mutation recovery, whereas stored reads need
no producer grant or enabled collection gate. No new schema, migration, grant,
credential, production setting or write operation is introduced. All producer
flags stay default-off; their deletion/disablement only affects production of
new observations, not currently authorized reads of already eligible facts.

## Server contract

GET `/api/user-assets/v3` adds the version-3 user-only list. Existing GET
`/api/user-assets/v3/detail` keeps its request/response and authorization contract.
Existing v2 routes and representations are unchanged. Version is selected only
by the fixed route; caller `dictionaryVersion` parameters are rejected.

The list reuses v2 structural/query validation and filtering, with the v3-only
`uacDisableBit` extension below: required domainId,
expectedRevision and expectedCredentialRevision; optional exact observationId;
search is bounded case-mapped literal contains independently across SAM/SID/mail/DN;
sAMAccountName is case-sensitive exact raw equality; paired startTm/endTm are
half-open whenCreated bounds; creatTm is asc/desc with null last and GUID ties.
These filters are optional. Omitting one broadens the result; omitting creatTm
restores GUID order. New RID/SPN facts are not searched or sorted. Unsupported
business filters retain 422. PageIdx defaults 1, pageSize defaults10 and accepts
10/20/30/40/50 only; page2+ requires an observation pin. Query numbers, duplicate
keys, unknown keys, malformed encodings and GET bodies retain strict rejection.

Order is current reader/domain authority -> current source revision pins ->
newest eligible v3 observation or exact requested v3 observation -> FULL body
validation -> user-only projection -> all predicates -> stable order -> count
and page -> complete bounded response. Corruption in an excluded/off-page object
invalidates the entire response. No v1/v2 fallback, no old nonempty fallback for
an empty latest observation, no latest fallback for a supplied stale pin.

Success uses exactly dictionaryVersion:3, selection, available, list, page and
conditional observationId/source, matching the v2 envelope with twelve-field
v3 user rows. Unavailable is distinct from an observed empty result. Detail
requires exact domain/source/observation/GUID identity and cannot return a group.
Existing complete-observation decoder, current scope/tenant/session/actor,
revision/policy/publication checks, schema locks and body cap remain mandatory.
Public response is at most8MiB including the reserved newline budget, and is
encoded completely before success bytes. Reads cannot submit tasks or touch LDAP.

Errors preserve existing invalid_input, forbidden, selection_changed,
directory_observation_unavailable, not_found and dependency semantics. Lost
current authority discards protected views; stale exact pin never changes to
latest. A temporary failure allows retry with exactly the same source/query/pin.
Only explicit refresh may discard an established observation pin.

## Observed UAC disabled-bit filter (2026-10-25)

GET `/api/user-assets/v3` accepts optional, single-valued
`uacDisableBit=set|clear|not_returned`. It filters the saved nullable uint32
`userAccountControl` fact, without adding a response field or collection attribute:

- `set`: UAC is present and its ACCOUNTDISABLE mask `0x00000002` is set.
- `clear`: UAC is present and that mask is clear, including UAC zero.
- `not_returned`: UAC is genuinely absent, represented by valid JSON null.
  A missing response key, malformed number or corrupt observation is an error,
  never a member of this category.

Omission means unrestricted and preserves the old query. This parameter is needed
only when this filter is requested; removing it broadens the result. Empty values,
duplicate values (even equal ones), `null`, numeric aliases, arrays, unknown values,
whitespace and case variants return400 `invalid_input`. Detail and v2 routes
reject the new key. Legacy `userStatus` remains unsupported with422; this is not
an alias for its active/disabled/locked business states.

The new predicate is ANDed with search, exact SAM and time bounds, after complete
source validation and user-only projection, before stable ordering/count/paging.
Excluded or off-page corruption still fails the whole read. Zero matching rows
retain the available observation and its pin. The export uses the same predicate
and ordering across all matches, not only the current page.

The UI labels the saved fact as `本次观测的禁用位`, with `禁用位已设置`,
`禁用位未设置` or `UAC未返回`, alongside raw UAC and source provenance. A clear bit
does not establish activity, login ability or absence of lockout; a set bit does
not mean this platform performed a disable operation. Null is not zero. The
client first validates uint32/null, then rejects an entire list response if a row
contradicts the requested bit filter; this cannot prove omitted rows or totals.
Draft/apply separation, page reset with retained pin, clear-to-unrestricted,
dirty-filter export blocking and stale-response invalidation remain required.
The visible side effect is another filter dimension, which can produce zero
matches; it does not imply that the directory has no users.

This extension changes no schema, permission, producer gate or deployment.
Its validation is tracked separately from the historical results below. The
existing TLS LDAP browser fixture has only UAC0/512 users, so it can exercise
clear and empty set/not_returned results, not nonempty set/null user ingestion.
Synthetic unit or PostgreSQL producer tests are not that LDAP/browser proof or
real AD acceptance. Exact-head checks and independent review remain required;
AD-F-038/039, full userStatus semantics and product acceptance0/209 remain open.

### Filtered-export evidence consumer candidate

The separately scoped [success export evidence profile](user-asset-export-v3-contract.md#filtered-success-browser-evidence-profile-2026-10-25-candidate)
keeps the unfiltered12-row export and keyword-only unmatched GET, then applies
`user-` plus UI-selected `set` for its second and final export. Duplicate-preserving
GET query pairs and an independently constructed canonical request bind the same
actor/observation/source pins through receipt/detail and the stored workbook.
A dirty selector cannot fetch or create; applying Search restores the explicit
new-export transition. No production list, fixture or permission changes are part
of this consumer slice.

The former unmatched-keyword-to-XLSX browser transaction is replaced, not equivalent
coverage. Default LDAP user UAC0/512 establishes only filtered-set zero matches;
nonempty set, null/not_returned LDAP users and positive filtered-clear downloads
remain unverified here. Mixed offline fixture cells and synthetic-producer PG
partition coverage are separate layers, not those missing browser/LDAP chains.
Format2 artifacts, exact-head CI, independent pixel/content review and runner cleanup
are still required; offline preparation does not change product acceptance0/209.

## UI-0494 keyword-only draft clear (2026-10-28)

Engineering [#245](https://github.com/yunpiao/adtr/issues/245) implements only the
`clearable` part of Issue30's keyword control. The explicit `清空关键词` button
(accessible name `清除用户资产关键词`) empties the keyword draft, removes only its
search paste/drop rejection, clears the shared submit-validation notice as ordinary
editing does, and returns focus to the keyword input. Other invalid drafts are
retained and must pass the unchanged validation on the next explicit Search.
The button is disabled for a stale observation, or when both the keyword and its
transfer error are empty; an empty keyword with a rejected transfer remains clearable.

This is a local edit, not a query: no GET/POST or list cancellation occurs.
SAM, paired creation times, ordering and UAC drafts, other transfer errors, selected
source, accepted query, observation pin, page/page size, visible rows and page-local
row numbers remain unchanged. Explicit `搜索用户` applies the remaining drafts on
page1 with the same pin and page size. The original `清除用户筛选` still clears all
filters and performs its explicit read; it is not replaced by the new control.
A dirty draft keeps paging, details and creation of a new export blocked; recovery
of an existing export retains its original locator and existing rules.

Like ordinary field editing, keyword clear closes/invalidates selected details,
cancels their retry timer and rejects late success or error callbacks, without
cancelling the independent list channel. A pending list may still finish for the
old accepted query; the empty keyword then remains unapplied. If the user explicitly
applies it first, the superseded list cannot replace the newer result. An open
modal drawer makes the background controls inert; actual users close it before
editing, while synthetic DOM tests also exercise the invalidation transition directly.

The action is optional convenience for editing one field, not a required API
parameter. Removing it would remove only this explicit single-field affordance;
manual editing and full-filter clear would remain. Its side effects are the local
keyword/error change, detail invalidation and focus transfer described above. The
page-size field retains its original second-child position and CSS; the new button
follows it without changing shared CSS or other workspaces. The original50 UTF-16-unit submit validation is unchanged. Input-length/IME enforcement,
v2 and full UI-0494/product equivalence are not included.

DOM and Python source contracts are synthetic verification. The existing real
`user-assets-v3` CI suite adds exact-query, native transfer and superseded-response
checks plus source-bound1440/390 keyword-clear screenshots. Separate control and
list captures avoid claiming the keyword and preserved rows share a viewport when
expanded filters push the rows below it. Each list capture is taken while still
dirty, before Apply, after its response-bound GUID and page-local position1 are
verified and the position is fully in view. Actual execution and pixel review
are required before claiming browser verification. No fixture, workflow,
selector, runner, API/schema or timing budget changes. Existing user exports and the
mixed33 consumer closure remain required. Product acceptance stays0/209.

## Versioned UI and safe facts

The existing default `#user-assets-v2` remains v2. New `#user-assets-v3` is explicit.
The asset workspace exposes a clear v2/v3 navigation choice. Switching version
unmounts the old source/list/detail, cancels/invalidate reads and requires source
resolution for the new route. It never copies old private response data, draft
filters or observation pins across versions. Back/Forward and refresh revalidate
the session and start a clean workspace on the exact historical version route;
no stored rows or automatic latest fallback masquerades as a prior observation.
The user must reselect the source after returning. Every successful list/detail
shows its selected version and exact source/observation provenance.

The v3 workspace follows the existing PR89 SOC list and right-hand drawer.
Source selection and current permissions are resolved using the existing picker.
List/search/page keep the captured observation; explicit refresh requests newest.
Domain switch, source conflict, changed/invalid session, lost authority, navigation
and repeated reads invalidate earlier generations even when AbortSignal is ignored.
A same-identity focus/visibility verification hides/inerts the workspace while checking,
then resumes its existing pinned view; this is distinct from Back/hash navigation,
which remounts and requires source reselection.
No cross-tab event can resurrect stale private data. Drawer focus/inertness,
Close/Escape, cancellation and origin-focus restoration retain current rules.

primaryGroupID displays decimal0 through4294967295 or `未返回`. SPN null displays
`未返回`; present arrays preserve every value and received order. Values remain
React text with visible escaping of controls/bidi characters and prior escaping of
original backslashes, so literal backslash-u text cannot look like a received control.
The UI explicitly labels both as presentation-only escaping. There is no HTML parsing,
URL linkification, trimming, normalization or hidden truncation. A bounded scroll
region/collapsible group allows all accepted values to be inspected. Labels state
that receive order has no directory priority meaning. Old v2 views identify these
fields as `未采集（字典2）`, never fill v3 null or read a newer v3 observation.
The current v1 directory page remains v1 and is not promoted into user assets.
Keyword and exact-filter inputs reject control-bearing paste/drop before native
single-line sanitization, retain the prior draft and block submission until that
field is edited or all filters explicitly cleared. A rejected transfer never
becomes an empty predicate or a normalized query.

Client parsing validates exact v3 shape, all twelve fields, uint32/null,
SPN array cardinality1..128, each nonempty UTF-8 string <=1024UTF16 units AND
4096UTF8 bytes, total<=32768UTF8 bytes, exact duplicates rejected, no unpaired
surrogates. It validates actor header, streaming8MiB cap, selection/version/pin,
page arithmetic, unique GUIDs, user kind and filter/order consistency. Old v2
payloads cannot satisfy a v3 request even if the first ten fields match.

## Verification and delivery

Record clean baseline before production edits. Unit/HTTP/DOM tests cover canonical
queries, exact projections, full-read corruption, zero/null/max RID, Unicode,
NUL/controls/markup, SPN order and caps, actor/source/version mismatch, retry pins,
ignored abort, version/domain changes, history, session and current scope loss.
Real isolated Worker -> synthetic TLS LDAP -> PostgreSQL -> API -> browser tests
must generate observations through the registered producer, never insert a
successful observation or render fabricated production rows. Actual desktop1440
and mobile390 screenshots, native cross-tab revalidation/revocation, Back/refresh
and interrupted reads remain mandatory. Existing v2 chain tests must stay green.

Local Docker/PostgreSQL/browser restrictions are blocked stages, never passes;
actual GitHub CI runs the real suites. Full exact-head CI, independent actual-diff
review and current-main compatibility precede any normal merge. Main-push CI is
separately verified. Parent and engineering Issues stay open pending separate
closure approval. No source parity, real AD/eight Windows versions, capacity,
production deployment or complete F14 acceptance is claimed by this slice.

Clean base make check passed: 1585 DOM tests,109 Python contracts, Go race/vet/build,
TypeScript/build and dependency audit0. This is baseline evidence only.

## Keyword response binding follow-up

Both user dictionary-2 and dictionary-3 list parsers must independently reject an
entire response if any row does not satisfy the requested keyword. This is an
additional response/query consistency check; current server authorization and
whole-observation validation remain authoritative. It cannot reconstruct an
omitted row or independently verify a server's total count.

The predicate uses only SAM, SID, mail and DN as independent fields, never GUID,
description, object class, primaryGroupID or SPN. Empty search matches; otherwise
each field and the raw search use the repository-pinned Go Unicode simple
lowercase followed by literal substring matching. No trimming, normalization,
locale rules, regex/wildcards, cross-field concatenation or multi-character
case folding is introduced. JavaScript's default full/contextual lowercase is
not interchangeable (for example U+0130 and final sigma).

`directory-search-case-map.ts` is generated offline from Go 1.27.1's Unicode
17.0.0 CaseRanges. Regenerate with `go run
scripts/generate_directory_search_case_map.go`; the offline Python contract
checks the exact generated bytes against that toolchain. The TypeScript test
hashes lowercase output for every Unicode scalar and compares it to Go's
independently generated SHA256. A Go/Unicode upgrade must explicitly regenerate
and review this small table rather than inheriting browser-specific ICU rules.
The shared helper accepts a field list so other readers can retain narrower
contracts; computer assets use SAM/SID/DN without mail.

## UI-0494 / FLD-0330 input-length follow-up (Issue247)

This adds only dictionary3 user's keyword input enforcement after PR246's
keyword-only clear slice. The historical PR246 scope above remains a record of
that slice, not the scope of this follow-up. Issue30 says "最长50字符" and
`maxlength=50` but does not define Unicode counting or IME behavior. This bounded
engineering interpretation uses the already-established **50 UTF-16 code units**
(50 BMP characters or25 supplementary emoji), not grapheme clusters. The API's
existing <=200 UTF-8-byte and no-unpaired-surrogate validation remains unchanged.
It is not a claim of original OS IME parity or product acceptance; product0/209.

Ordinary overlength typing, paste replacement or final drop change rejects the
**whole change** and restores the latest length-compliant draft plus selection
start/end/direction. There is deliberately no native `maxlength`: it could
silently truncate before React sees the intended input. No prefix truncation,
trim, normalization, or implicit GET/POST occurs. Length-compliant does not mean
API-valid: direct programmatic raw controls still use the original literal query
and safely escaped applied display; lone surrogates still fail explicit submit.

The original Cc-bearing paste/drop rejection runs first so single-line native
sanitization cannot transform raw input into another query. Paste length accounts
for selection replacement; drop position is not assumed to equal that selection.
An oversized external drop payload is rejected upfront, and every resulting
ordinary input is checked against the final value. The search error persists
until a distinct valid edit or an explicit clear; unrelated SAM/time transfer
errors and drafts remain intact. Other list errors remain in their own channel
and become visible again when the keyword error is cleared.

Composition may temporarily exceed50 units. During composition, search submit,
refresh and both clear actions are disabled; dirty state also blocks pagination,
page-size changes, detail and new export. `compositionend` and blur fallback use
one idempotent finisher. A rejected composition restores the pre-composition
draft and selection, and repeated trailing input containing either rejected or
restored text cannot silently clear its error. A new distinct edit recovers.
Both clear actions reset accepted/composition/rejection/selection references while
retaining their original draft-only versus full-filter-query behavior. Source,
actor/session remount, fatal read failure and unmount invalidate old composition.

Editing, rejecting and composition start invalidate only the independent detail
channel. They do not cancel the pending list, replace its accepted query or pin,
or alter existing rows and page-local positions. Late detail success or 403
cannot reopen detail or invalidate the newer workspace. Current API, shared CSS,
v2/other workspaces, source fixtures, workflows, selectors and budgets stay fixed.

DOM contracts cover50/51,25/26 emoji, whole replacement, backward selection,
transfer precedence/drop uncertainty, end/blur/duplicate composition events,
clear reset, independent errors, pending list/late detail and source/session/
authorization/unmount isolation. The existing real browser suite adds native
keyboard and trusted clipboard checks at1440/390 plus actual selection and emoji
boundaries, with original response/observation-bound screenshots. Its dispatched
composition events are explicitly **synthetic**, not OS-native IME acceptance.
Local browser launch remains permission-blocked; discovery is not execution.
Exact-head CI, independent review and fresh main verification remain separate
required evidence before this engineering slice can be declared complete.
