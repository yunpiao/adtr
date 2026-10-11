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
