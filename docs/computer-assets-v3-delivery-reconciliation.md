# AD-F-040 computer delivery reconciliation

## Decision and fixed scope

AD-F-040 moves from `not_started` to `in_progress`: merged, bounded computer-list
implementation had no catalog evidence. `in_progress` means implementation exists
but full source requirements and acceptance gates remain open. It is the correct
ledger state while those gaps are resolved and independently verified.
Product acceptance stays **0/209**. Counts become **19 implemented_verified_slice,
36 in_progress, 154 not_started**; no other feature changes status or evidence.

This reconciliation is based on main
[`91e05b9d5790e1b9e4654ef6f03deb55195c925b`](https://github.com/yunpiao/adtr/commit/91e05b9d5790e1b9e4654ef6f03deb55195c925b),
tree `79723cf4435305fb27ebbe51f8944af9732db7be`.
The source workbook remains `2026-10-03-AD功能清单.xlsx`, SHA256
`d59ccdf3eac3865b20c1f919f2cbc881df625714b2c499342b4cc1bcf2efae54`,
sheet AD 功能清单, row44. All FLD-0412–0458 (47 fields) and UI-0437–0445
(9 controls), priorities, source rows and Issue links remain intact. The original
workbook bytes and old application source were not re-read for this reconciliation.
Original definitions remain in [F14 #30](https://github.com/yunpiao/adtr/issues/30);
[engineering #131](https://github.com/yunpiao/adtr/issues/131) remains bounded.
Neither parent requirement nor engineering Issue is closed by this document.

## Registered implementation

The [current local contract](computer-assets-v3-contract.md) defines one currently
authorized source and complete eligible immutable dictionary3 observation, followed
by computer restriction, SAM/SID/DN literal search, creation-time filtering/order,
filtered totals and bounded pagination. The list exposes the existing closed
12-fact projection and inline facts, with source/observation pins, current authority,
late-response rejection and revocation clearing. No observation, observed zero
computers and nonzero computers with zero matches remain distinct.

Those 12 facts are **not 12 fully compatible original fields out of 47**. The
projection and the original field records have different roles and JSON keys.
ObjectGUID agrees, while original `sid`, `dn`, `sAMAccountName`, `created` differ
from current `objectSid`, `distinguishedName`, `samAccountName`, `whenCreated`.

- PR132 supplies the bounded stored computer list and real synthetic producer-chain
  test evidence. Synthetic Worker/TLS LDAP/PostgreSQL/API/browser execution is
  distinct from real AD and Windows product acceptance.
- PR175 supplies UI-0438 page-local numbering: positions1..N in the accepted page,
  restarting each page. GUID remains row identity. Original cross-page behavior is
  not frozen. The local `pageRowNumber` rendering prop supplies that display only;
  removing it loses row references, and the extra column can widen mobile scrolling.
- PR176 supplies the UI-0437 keyword-only draft clear and focus restoration. No query
  is sent until explicit Apply; time/order drafts, source/pin and applied query are
  retained. An already-valid applied read may still complete. Its local input ref
  is needed only for focus restoration; removing it loses that focus behavior.
  The extra button consumes layout space. Neither UI slice adds API/configuration
  parameters, permissions or storage.

Required source IDs/revisions bind authorization and prevent stale-source reads;
observationId is required on later pages to prevent mixing observations. Removing
those checks would lose source/pagination consistency. Page-size limits bound reads
and response work: currently10/20/30/40/50, not original `pageSize=-1` fetch-all.
The limits deliberately prevent full-list retrieval through that parameter; they
must not be removed merely to claim compatibility. The original integer `creatTm`
versus local `asc`/`desc`, paired RFC3339 time interval, and search semantics remain
local contracts pending original-system evidence.

## Evidence chain: historical, PR head, and merged main

The following records distinguish the actual runtime revision from this later
documentation change. PR bodies are mutable records; their linked exact commits
and Actions runs identify the execution. This reconciliation reads the contracts,
source audit and recorded PR evidence; it does not rerun those runtime suites or
independently redownload the old artifacts.

### PR132: original bounded list

[PR132](https://github.com/yunpiao/adtr/pull/132) records final head
`bf3ef84f53da7fcd9ba7dc2c2d3fd6b116f221cb`, tree
`ff11f0141015655f57256f2ed0ab68b53919aa3d`, and
[PR run38197305276](https://github.com/yunpiao/adtr/actions/runs/38197305276),
46/46 successful. Its recorded review covers the23computer/3user/4group fixture,
four computer artifacts,116 source hashes,8 PNGs and two native reader engines.
[Independent historical review](https://github.com/yunpiao/adtr/pull/132#issuecomment-6117645011)
records those exact-head checks. Merge commit:
`3b3c590338d9f36f4571fb6c81c5c39afda48755`.
The earlier audit found its body still described main
[run38203133039](https://github.com/yunpiao/adtr/actions/runs/38203133039) as running;
that historical wording is not a certified terminal result here. Later exact-head
and fresh-main evidence below supplies its own regression coverage.

### PR175: UI-0438

[PR175](https://github.com/yunpiao/adtr/pull/175) final head
`1f5a952168187e475978c2dc86fcc81d1a5ca7b1`, tree
`ccdb069b94fbc26aae33960228ec2d30df9b5675`, passed
[39190197219](https://github.com/yunpiao/adtr/actions/runs/39190197219)
attempt1,71/71. Separate merge `b4a589c810bc635566a9fe790d9bda9dce9fb3c1`
passed main [39194622353](https://github.com/yunpiao/adtr/actions/runs/39194622353)
attempt1,71/71. The record includes14 row-number cases within2759 DOM tests,
713 Python checks, default/fixed computer/readers execution, desktop per-page1..10,
mobile row1 and all four reader revocation scenarios. Fresh main artifacts and
all eight screenshot hashes were separately reviewed, rather than substituting
PR artifacts for main execution.

Earlier PR175 attempts retain registry HTTP502, a user-reader secret-substring
assertion failure, and an owned-worker identity failure before browser execution.
Later success does not identify the matched secret category or the failed identity
predicate. Those causes remain unknown; strict rejection predicates and closed
safe diagnostics remain in place. Interrupted jobs do not count as passed.

### PR176: UI-0437 and latest baseline

[PR176](https://github.com/yunpiao/adtr/pull/176) final head
`0d34a88882207eb3e9bd99d7388bc8ea35f3e1fc`, tree
`79723cf4435305fb27ebbe51f8944af9732db7be`, completed
[39205201921](https://github.com/yunpiao/adtr/actions/runs/39205201921)
with terminal success on **attempt2**, latest71/71 checks successful.
Attempt1 directory-v3-readers job117906971640 failed with GET socket hangup;
API/worker were alive before cleanup, exited0 afterward, and recorded no HTTP panic
or forced kill. One failed-jobs retry passed directory job117921072664
(1 passed,10.5m) and verify117924716010 using the unchanged same-run attempt1
PostgreSQL producer with strict digest verification. The connection-close cause
remains unknown. This is retry success, not first-attempt full success.

Attempt/all-check history retains four earlier same-name queued entries with no
runner but completed steps, inconsistent metadata. The actual successful job
supersedes them in the latest71 set. These entries are not four extra successful
executions; the discrepancy remains recorded.

The PR review records default/fixed computer/readers source/artifact binding,
four new clear-state desktop/mobile screenshots, keyboard focus, no automatic
request, preserved filters/pin on explicit Apply and reader revocation scenarios.
Contracts include15 clear-keyword and14 row-number cases within2774 DOM tests.
An earlier head failed at candidate_strict/unowned_barrier_blocker. The actual
strict predicate remains unknown: a synthetic waitType=None reproduction of the
generic diagnostic event is not evidence of the real cause. Original validation,
one-sample acceptance, deadlines, pins and STOP rules were not relaxed.

Merged main `91e05b9d5790e1b9e4654ef6f03deb55195c925b`, same tree as final head,
passed its **separate fresh main**
[39211561445](https://github.com/yunpiao/adtr/actions/runs/39211561445),
**attempt1,71/71**. Independent review checked fresh main default/fixed computer
and reader artifacts, source/tree binding, four new clear-state plus two revocation
screenshots, keyboard focus, no automatic request, explicit Apply preservation,
and all four consumed-response revocation scenarios in both reader engines.
This main execution does not erase the PR retry or historical unknown failures.

## Remaining source and acceptance gaps

1. DNSName/DNSAddress, OS, login times, business status/type/application,
   per-row business domain/name/id and address relationships remain missing or
   unaccepted. SPN/UAC/GUID/source-domain values cannot establish those facts.
2. Unsupported computerStatus/domainName/updateTm/computerType filters remain
   explicit422 after structural validation. A single domainId is not multi-domainName
   filtering; current SAM/SID/DN search is not device-name/DNS/IP search.
3. Original JSON-key, integer enum, time interval and search compatibility remain
   open. Independent export of matching rows does not validate list `pageSize=-1`
   or original list-checkbox behavior.
4. The second17-field group FLD-0442–0458 has no established original entry point.
   Do not reuse first-group success or infer a particular address endpoint. Obtain
   original handler/route and per-field definitions before claiming its coverage.
5. Dynamic columns UI-0439–0444 lack original column/slot definitions; UI-0445
   item.prop checkbox purpose is not frozen. The current facts button does not
   establish the old operation column. Obtain original parent table configuration.
6. UI-0437 maxlength remains unaligned: overlong drafts are allowed and rejected
   at submission; there is no maxlength truncation. Original clear-event query
   behavior is unknown. UI-0438 only establishes the explicit page-local contract.
7. Existing sticky full-page mobile screenshots have a visibility limitation:
   they do not show all text simultaneously. Recorded focus/overflow assertions
   and selected screenshots are bounded evidence, not universal visual acceptance.
8. Real AD, all eight Windows versions, original target capacity, complete legacy
   compatibility and production gates remain unaccepted. Fixture limits and shared
   CI passing do not establish those gates. Prototype PR89 read-only-fixture
   device/risk/address samples remain design references, not production facts.
9. Historical secret-substring, identity-predicate, candidate_strict and socket
   causes remain unknown. Future diagnosis needs the corresponding safe categories
   and exact failing execution evidence; a clean rerun alone is not a root-cause fix.

## Validation of this reconciliation

Only requirements/catalog.json (AD-F-040 status/evidence), requirements/README.md
(counts and this link), and this document change. Schema, validator, CI, other208
requirements and all product_accepted flags remain unchanged. Offline structural
verification must preserve209 requirements,57 groups,3258 field IDs,804 control IDs,
source workbook SHA, priorities, Issue links and product acceptance0.

No Node/Go/PostgreSQL/browser runtime was executed for this documentation work.
The new documentation revision must obtain its own checks; those checks validate
that revision and cannot be called a fresh AD-F-040 runtime acceptance exercise
unless the relevant runtime actually runs against its exact commit. Historical
runtime results above remain attached to their own heads and merges.
