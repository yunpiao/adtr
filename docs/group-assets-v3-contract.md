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
