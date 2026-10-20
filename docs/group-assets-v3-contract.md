# F14: dictionary-3 raw group asset workspace

Engineering [#135](https://github.com/yunpiao/adtr/issues/135), parent
[#30](https://github.com/yunpiao/adtr/issues/30), AD-F-042. Base main
`8d559dd1c1bb888a49c2e1f044d64e78feff42f1`, schema19. Implementation in progress;
product acceptance remains 0/209. This is a bounded new contract, not source
parity or complete F14 acceptance. The source workbook's FLD-0459–0480 and
UI-0446 describe pagination, search(max20), group classification and legacy fields;
they do not establish missing producer facts or integer-sort meanings.

## Request and response

GET `/api/group-assets/v3` is the only new read-only endpoint. Required
`domainId`, `expectedRevision`, `expectedCredentialRevision` fix the current
source selection. `observationId` is optional only on page1; page2+ requires it.
Omitting it selects the newest eligible v3 observation. A supplied pin never
falls back to latest; only explicit UI refresh discards an established pin.
Default `pageIdx=1`, maximum10000. Default `pageSize=10`; only10/20/30/40/50;
-1 is rejected rather than meaning unbounded output.

Optional `search` is raw literal substring independently over SAM/SID/DN,
case-mapped using pinned Go simple lowercase. No trimming, normalization,
regex, wildcards, mail search or cross-field concatenation. Maximum20 UTF-16
units AND80 UTF-8 bytes, valid Unicode; the measurement is an explicit new
engineering convention. Omission/empty string matches all groups. The client
reuses the reviewed Unicode helper and rejects each nonmatching response row.
Single-line paste/drop controls are rejected before native sanitization.
Optional `creatTm=asc|desc` sorts canonical UTC creation times with nulls last
and GUID ascending ties. Omitting it restores GUID ascending. No time range.

Duplicate/unknown keys, noncanonical numbers, malformed encoding, invalid
required pins, invalid bounds or GET bodies fail400. Known unsupported
`domainName`, `groupMold`, `groupScope`, `groupType`, `startTm`, `endTm`, `status`,
`updateTm`, `overviewType` fail422 after structural validation. No request
parameter selects dictionary version, object kind or actor/tenant.

Response: exact dictionaryVersion3, selection, available, observedGroupCount,
list and page; observationId/source only when available. observedGroupCount
is null without an observation, otherwise the real complete-observation group
count BEFORE search; page.total is after search. Without search these counts
must agree. Distinguish no observation, observed zero groups, zero matches and
out-of-range empty page. Each row is the existing canonical twelve-field v3
projection, kind=group. Null, zero, empty, ordered values and raw strings retain
the existing codec meaning. No inferred name, application, groupMold/scope/type,
sensitivity, membership, relationship or export fields. Canonical facts may be
shown inline; no separate detail or membership endpoint is introduced.

## UI-0446 independent keyword clear

Issue #30 records `UI-0446｜el-input｜输入关键词搜索组名或SID｜search｜{"maxlength": "20", "clearable": null}`.
This establishes a clearable-keyword clue, not verified legacy auto-query behavior.
The current workspace explicitly keeps its draft/Apply model: **清除组关键词**
clears only the search draft through the existing edit path, removes that field's
rejected paste/drop and input-validation errors, closes expanded facts and focuses
the keyword input. It never submits, fetches, cancels a pending read or changes
creation-time sorting, applied query, source revisions, observation pin, page size
or accepted rows. Empty/repeated clears are permitted, including recovery from a
rejected transfer whose draft is empty. Loading/stale guards remain unchanged.

If the cleared draft differs from the applied search, the existing dirty state
continues to pause pagination, facts and new export creation. Explicit Apply sends
empty search with the retained sort/source/pin/page size and pageIdx1. Pending
responses still belong to their applied query; clearing does not promote the draft
to an applied query. Existing **清除组筛选** still clears search and sorting and
immediately queries. Parser/API/producer/permissions, export/recovery and read
cancellation behavior remain unchanged; input-limit interception is described below.

The native `type="button"` prevents accidental form submission and supplies
keyboard activation. The local input ref restores focus after clear and selection on rejected edits;
deleting it removes those restorations, not query behavior. Removing this
control removes the keyword-only shortcut; manual editing and full clear remain.
The added control occupies form space, so desktop1440/mobile390 geometry and
keyboard behavior are covered in the existing real group E2E, retaining all prior
assertions and screenshots. Synthetic focused cases live in
`GroupAssetsV3KeywordClear.test.tsx`; real-chain execution must be reported
separately. Source parity and complete product acceptance remain unverified.

## UI-0447 page-local row numbers

Issue #30 records `UI-0447｜el-table-column｜序号｜未声明直接绑定｜{"type": "index"}`.
That source establishes an index-column clue, not a verified cross-page numbering
rule. This slice explicitly adopts the local display contract **本页序号**:
the accepted current page is numbered 1..N in returned order, resetting to 1 on
every page. With 23 groups at page size 10, pages show 1..10, 1..10 and 1..3.
Changing page size, applied search/sort or refreshing recomputes positions from
the newly accepted rows. Unapplied drafts retain current row numbers while the
existing pagination/facts pause remains in force.

The column helps locate a row within the visible page. Its required local
`position` component prop is derived from the array index plus one; it is not a
query parameter or stored field. Removing the display removes only that locator.
It has no effect on object identity, ordering, observation pinning or export.
GUID remains the React key and facts target; expanded facts span all six columns
without receiving a number. Empty/loading/error/revoked states create no rows.
Late responses remain subject to the existing generation, abort and authority
checks. No API, storage, filters, sorting, permissions, cancellation, export or
recovery contract changes. The extra column increases table width, so existing
horizontal scrolling and desktop/mobile geometry checks remain required.

Focused synthetic DOM coverage is in `GroupAssetsV3RowNumbers.test.tsx`; real
ordinal assertions augment `group-assets-v3.spec.ts` without replacing its
Worker/TLS LDAP/PostgreSQL/API, facts, pinning, invalidation or screenshot checks.
Local synthetic tests and real browser evidence must be reported separately;
source parity, real AD/Windows and product acceptance remain unverified.

## Authority and failure handling

Current session/actor/tenant, domains.read AND directory_assets.read, current
domain scope/enrollment and exact source revisions are checked transactionally.
Schema locks, eligibility, immutable publication/source metadata, full body
validation and 8MiB complete-response cap remain in force. Entire observations
are validated BEFORE kind selection/count/filter/page: corrupt user/computer or
off-page rows invalidate the entire response. Reads cannot submit tasks, use
credentials or reach LDAP. No migration, producer flag, grant or gate changes.

The independent SOC route `#group-assets-v3` uses the existing source picker.
Domain/navigation/identity/source changes invalidate generations even if abort
is ignored. Temporary failures retry the identical source/query/pin. Revocation
clears protected data. Same-identity focus revalidation preserves the pin only
after verifying authority; Back/Forward/remount require source selection again.
Draft search changes hide facts and block stale pagination. Native cross-tab
session events cannot resurrect private data. Text is React-escaped, controls
and bidi shown as visible escapes; all twelve facts are inspectable, including
null versus zero RID and ordered SPN. This display makes no AD classification.

## Required verification and remaining scope

Baseline and candidate make check, unit/HTTP/query/parser/DOM tests, genuine
registered Worker→synthetic TLS LDAP→PostgreSQL→API→browser, at least23 groups
plus user/computer distractors across pages, exact fact values, whole-body
corruption, old pins/revocation/late responses/history/cross-tab, desktop1440
and mobile390 screenshots, stable/fixed native reader receipts. Real browser
and DB are blocked locally and must run in CI. No blocked stage counts as pass.
Independent actual-diff review and full exact-head/current-main CI are required
before normal merge. Shared wiring is coordinated with ongoing computer and
v3-governance slices, preserving every existing suite. Main-push CI is separately
verified. Parent and engineering Issues remain open. Real AD, eight Windows
versions, large-scale capacity, full old field parity and deployment are not
accepted by this slice.


## UI-0446 / FLD-0464 bounded keyword input limit

Issue #30's maxlength20 clue is implemented as a current engineering interaction,
not verified legacy behavior. Ordinary edits exceeding 20 UTF-16 code units are
rejected whole, restoring the previous accepted draft and selection without
truncation, normalization or requests. Native maxLength is intentionally absent:
it can silently truncate a paste instead of explaining rejection. This input
length guard supplements, never replaces, the unchanged Apply/parser/API checks
for 20 UTF-16 units, 80 UTF-8 bytes and legal Unicode. Lone surrogates still reach
the Apply validation boundary; input interception does not reject every surrogate
code unit (which would also reject valid emoji).

Paste checks the actual replacement selection. Drop rejects payloads above the
entire cap, then validates the actual input result rather than assuming the drop
caret equals selection. Existing control-character transfer rejection takes
priority. A rejected edit marks the field invalid, closes facts, blocks Apply,
paging, facts and new export until a valid edit or clear; the accepted query,
source, sort, observation pin, pending read/retry and export recovery stay intact.
Refresh retains its existing applied-query behavior outside composition.

IME may stage an over-limit draft while composing; Apply, refresh, both clears,
paging, facts and new export are paused until compositionend or blur fallback.
Completion accepts the whole valid draft or restores the accepted value/selection;
trailing rejected-composition inputs cannot erase the error. Unmount/session loss
discards local composing state. Independent clear resumes its existing always-
available behavior after composition, including empty/repeated/stale recovery;
full clear still resets sorting and immediately queries.

Accepted-value, selection and composition refs/state are required only for this
whole-edit/IME interaction. Removing the guard restores oversized drafts until
Apply; removing composition staging risks rejecting partial IME input. The tradeoff
is temporary control suspension and an explicit correction step after rejection.
Synthetic tests cover IME/drop, blur, session loss and pending responses; the real
E2E adds native Chromium keyboard/clipboard/selection/emoji and 1440/390 error
screenshots without removing existing checks. Native OS IME/drop, full real-chain
execution and produced pixels must be reported separately, not inferred from DOM
tests or test discovery. No legacy parity or product acceptance claim is made.
