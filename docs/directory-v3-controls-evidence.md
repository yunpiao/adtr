# Dictionary 3 collection/governance verification record

2026-10-11. Refs [engineering #127](https://github.com/yunpiao/adtr/issues/127),
[F12 #25 / AD-F-034](https://github.com/yunpiao/adtr/issues/25).
Scope: [approved controls contract](directory-v3-controls-contract.md).
This slice depends on [PR #126](https://github.com/yunpiao/adtr/pull/126)'s shared
version3 factual parser. Its initial dependent base is
`7a49dc10af33222a9138951fae97b782409c49fe`, tree
`fbbd1f2ee2e2f1dbe9c3ba7001bd68c028d234a7`. A dependent draft is not main-ready.

## Local checks actually executed

- Unchanged main `524dc4b537985874b3a7159c9c13ef2c91e7c17d`: full `make check`
  passed Go race/vet/build, 109 Python contracts, 1585 frontend tests,
  TypeScript/build and dependency audit with zero vulnerabilities
- Initial candidate `b430a14d49ec16262477d925d4b2c5fc2618eb1a`: full aggregate
  passed 115 Python contracts, 1853 frontend tests and the same Go/type/build/audit
  gates. Independent review identified a recovered revision-conflict retry loop
  and missing version-specific Forward evidence
- Reviewed source candidate `76463ce73b82c8c723a1d551b539023647f9c648`, tree
  `e8c9b5ddff842ce4f112937d588942de4965681c`: fixed original unknown sync conflict
  to preserve original pins as receipt-only; added fresh-storage/remount/source
  reselection regression and real Back/Forward scenario without mutation replay
- Revised full aggregate passed 115 Python contracts, 1854 frontend tests,
  Go race/vet/build, TypeScript/build and dependency audit with zero vulnerabilities
- Full frontend/browser Prettier check and `git diff --check` passed; the three
  additional Playwright tests were discovered. Separate TypeScript checking of
  their actual entrypoints was executed, not inferred from frontend compilation
- Independent actual-diff review cleared the final source candidate with 234
  focused tests and three controls static contracts passed. Source approval does
  not establish actual browser/database behavior

The first added reselection regression mistakenly returned resolve revisions that
were not advertised by the source list and correctly failed closed. The fixture
was corrected to the advertised pins; the original recovery revisions remain
unchanged. No production validation, timing or assertion was weakened.

## Blocked or pending gates

Local attempts of `python3 scripts/test_auth_e2e.py --suite directory-v3` and
`--suite directory-v3-controls` failed before runtime setup because Docker is
absent. `--suite directory-v3-readers` failed its explicit headed Chromium/display
prerequisite. These failures are blocked acceptance, not successful execution.

Additive CI preserves the existing 39 jobs and adds three browser jobs, for 42.
It must establish real UI/API/Worker/owned synthetic TLS LDAP/PostgreSQL sync,
lost committed grant/sync receipt recovery, explicit revoke, cancellation with
executor-return witness and zero dependencies, read-only/native cross-tab
isolation, interrupted proof/Back/Forward, and actual 1440×900 / 390×844 pixel
inspection. Screenshot hashes are bound to the independently hashed stored
observation and browser-consumed response witness. No successful API response is
fabricated; the reader interruption releases only a previously fetched genuine
response. Existing native stream guards and fixed-browser jobs remain intact.

Exact remote head CI, actual screenshot inspection, dependency #126's acceptance,
current-main integration, final-base/source review and coordinated normal merge
are pending. Postmerge content/parents and main-push CI are separate checks.
No deployment, real AD/Windows acceptance, gate/grant/credential change or Issue
closure is claimed. Product acceptance remains 0/209.

## First remote runtime checkpoint and bounded corrections

[CI38171279774](https://github.com/yunpiao/adtr/actions/runs/38171279774), head
`24aca1bbb013e20897bcc770d53560d19afd56aa`, executed the real version3 controls
suite successfully (job114564568550): actual empty TLS LDAP observation,
cancellation during paged I/O, retained/cleared proof, original executor-return
witness and zero dependencies. The existing version3 backend suite also passed.
This is partial runtime evidence, not an all-42-job pass.

The collection suite reached provisioning and then exposed an incorrect added
Back URL expectation: existing operation-account navigation retains
`#operation-accounts/detail` while the workspace remounts its authorized list.
The correction captures that exact original URL and separately requires the
current authorized list, no proof fields and no mutation replay on both Back
entries; Forward assertions remain unchanged.

The reader suite timed out before opening its first collection view: the separate
test navigation catalogue omitted the two new labels, so its helper did not open
the initially collapsed collection group after fresh reader login. The production
navigation already included the entries. The additive catalogue correction is
covered by three fresh collapsed-group DOM regressions for admin/reader collection
and administrator governance. No timeout, permission, native lifecycle guard or
visibility assertion was relaxed; actual reader/browser acceptance remains pending.

The inherited user-assets-v3 job failed its already-diagnosed SPN viewport-ratio
assertion at 0.9928819537162781. The independently reviewed dependency fix
`f8a576a479ec7b5991f066dbe5e8fa17025818fe` uses native centered scrolling at two
existing test steps and retains ratio=1. It is normally integrated for the next
candidate. No production CSS, API or display data was changed by these corrections.

## Second remote runtime checkpoint and mobile diagnostic

[CI38174461337](https://github.com/yunpiao/adtr/actions/runs/38174461337) identifies
event head `21218238d3aad702f07fe0c10b1810921f8ff75e` and event base
`f8a576a479ec7b5991f066dbe5e8fa17025818fe`, but checkout logs establish the actual
tested merge `594dcc128bf6229676cfba663d0a777337c3d81d` (head2121823 into
base3950997). These identities must not be conflated.

- Controls job114575546223 passed (5.7 minutes), again establishing real cancel,
  executor-return witness and zero dependencies.
- Readers job114575546302 passed (10.5 minutes): actual read-only account,
  native cross-tab interruption, genuine delayed response isolation and revoke.
- Collection job114575546319 passed the earlier navigation, unknown grant/sync
  receipt recovery and real Worker/TLS LDAP/PostgreSQL 30-object observation
  checks, then failed its unchanged document-width assertion at the 390px
  governance screenshot. Collection desktop/mobile/facts captures preceded that
  failure. This run did not retain the suite's images, so no pixel acceptance or
  exact overflow cause is claimed.
- Both inherited user-assets-v3 engines failed final scope withdrawal with403;
  dependency e60a507 adds precise connection-grant cleanup before withdrawal.
  Its actual corrected acceptance remains a dependency gate.

The next diagnostic keeps proof-absence and safe synthetic-content checks before
capture, then persists clean screenshots, hash bindings, actual checkout commit
and tree, separate event pins and numeric overflowing-element geometry before
the original strict width assertion. Geometry is bounded to256 entries with a
separate total count. Only this suite's existing narrow evidence pattern is
retained on failure. No product layout change or assertion relaxation is included.
Dependency e60a507 and actual main6beb495 are normally integrated; final exact
candidate CI and pixel inspection are still required.

## Actual mobile layout diagnosis and scoped repair

[Run38177868096](https://github.com/yunpiao/adtr/actions/runs/38177868096), event
head b7f4466, actually checked out merge a505f69b0e5859e4c5de42ae9e40e10cf76809fe,
tree abe100752622753fafa9021c515a7926fe82b371. Artifact11716279902 contains four
clean screenshots and their bindings; each PNG SHA256 was independently verified.
Desktop collection width1440/1440 and mobile collection/facts390/390 pass. Actual
pixels show readable v3 headings, source controls and primaryGroupID/SPN facts.
Mobile governance measures document463/viewport390: its uncontained grant table
is431.875px wide, right edge462.875px; the revoke button reaches452.875px and is
visibly cropped. This is a product layout defect, not an assertion/fixture issue.

The repair wraps only the two v3 governance tables in existing local horizontal
scroll containers with accessible region names and keyboard focus. A scoped
focus-visible outline identifies the focused surface. All authority data and
actions remain present; no global overflow hiding or v1/v2 layout change occurs.
A DOM regression checks both named regions, table containment and keyboard entry.
Real acceptance adds ArrowRight scroll, visible keyboard focus and Tab access to
the complete revoke button with ratio1, a rightmost-action screenshot and desktop
no-inner-overflow regression. These corrected pixels/runtime checks remain pending.

The normal integration of dependency9280049 includes actual mainaf9d9f5's safe
CI diagnostics. Runner conflict resolution retains both original diagnostic
phases and existing v3 time budgets. No native guard or failure threshold changes.

## Native keyboard focus diagnosis

Run38183997679 tested checkout4bf505a4f62b11791413688ca15e78694955907a,
tree3dbd413de567a00a0b6a6de88e499890cc2fa111. Artifact11719851394's five PNG
hashes were verified. After the original native ArrowRight then Tab, the revoke
button was the actual focused element with visible outline styling. It was
vertically inside the viewport but horizontally clipped: button left339.875,
right414.875; local container left31/right359, scrollLeft38 of max104. Page
width remained390. Actual pixels confirm the cropped focused action.

The bounded LTR-only correction responds to focus inside either v3 table by
adjusting only that table container's horizontal position to the focused control,
with6px focus-ring clearance (3px outline plus3px offset) and native scroll-range clamping. It never moves
page scroll, changes focus or initiates a request. Three DOM regressions cover
right clipping, left clipping and already-visible/no movement; container focus
itself is a no-op. The genuine browser keyboard sequence and ratio1 remain
unchanged. Corrected real browser and pixel acceptance are still required.

The same run's inherited fixed user-assets-v3 job114603021440 failed a read-only
administrator /api/auth/me GET with socket_hang_up at attempt1/2. Original logs
and artifact11719696334 are retained. Its fixed-engine artifact contains safe
screenshots/provenance but no runtime process-liveness report, so the transport
cause remains unproved. No authentication/native-guard change was made for it.
