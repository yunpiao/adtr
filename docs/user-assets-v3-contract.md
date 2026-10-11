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

The list reuses v2 structural/query validation and filtering: required domainId,
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
