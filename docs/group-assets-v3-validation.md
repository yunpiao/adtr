# AD-F-042 bounded group workspace validation

Engineering [#135](https://github.com/yunpiao/adtr/issues/135), parent
[#30](https://github.com/yunpiao/adtr/issues/30). Product acceptance0/209;
AD-F-042 in_progress, no Issue closed, no deployment. No source-system parity
or full F14 acceptance. See [scope and parameters](group-assets-v3-contract.md).

## Exact local evidence (2026-10-12)

Baseline main `8d559dd1c1bb888a49c2e1f044d64e78feff42f1` completed `make check`:
1834 DOM tests,125 Python contracts, Go race/vet/build, TypeScript/build,
dependency audit0. Baseline success does not verify new code.

Implementation `f7fac439a8d95e520ddc440026503c5723cf53fb`, tree
`a9e83ada785a3b595d788edeeb8941046acd6f9a`, completed `make check`:1895 DOM
tests,128 Python contracts, Go race/vet/build, TypeScript/build, audit0. Its61
new group DOM tests include closed parser,20 UTF-16 boundary, pinned keyword
binding with exact Unicode helper, current menu/route intersection, actual App
route/session/BackForward/focus/storage behavior, malformed-body rejection,
late responses and explicit stale-pin recovery. These are synthetic DOM tests,
not browser-runtime evidence.

Focused `go test -tags=integration ./internal/testldapfixture -run TestGroupAssets`
passed both real local StartTLS and LDAPS BER tests: two wire pages,23 groups,
3 users,4 computers, original canonical group classes, raw null/zero/control
facts. This is local TLS/codec evidence, not a PostgreSQL/Worker/browser pass.
`go test -tags=integration ./internal/auth ./internal/domains -run '^$'` passed
compilation only. Unit cases cover count-before-filter, GUID/time stability,
literal Unicode search, source pins, strict route permissions and full-response
budget. Three real-DB corruption cases target excluded user/computer and off-page
group rows and remain runtime-pending locally.

Actual local PostgreSQL attempt:
`go test -tags=integration ./internal/domains -run TestGroupAssetsV3StoredPinsCountsAndSourceAuthority -count=1`
failed with `ADTR_TEST_DATABASE_URL required; real PostgreSQL is not skipped`.
Actual local browser attempt:
`python3 scripts/test_auth_e2e.py --suite group-assets-v3`
failed because the Docker executable is unavailable. Neither is a pass. Real
screenshots, current revocation and native response-consumption receipts must
be produced by the existing isolated GitHub CI harness.

The new real-chain suites use the registered v3 producer and synthetic TLS LDAP,
never insert successful observations. They check all23 group rows against exact
canonical values, raw persisted payload hash, no user/computer leakage,
Unicodeİ/Σ/Deseret/literal search, old observation pins, explicit refresh,
read-only role revocation, native cross-tab lifecycle and genuine held-response
consumption. Screenshots are explicitly1440px desktop and390px mobile; fixed
engine native completion is distinguished from stable engine body completion.
These describe authored assertions, not yet-passed runtime results.

## Review and delivery state

Independent actual-diff review of all35 implementation files found no blocking
code issue. Reviewer separately passed79 group/navigation DOM tests,3 group
Python contracts, group/domain/HTTP and both TLS fixture cases. A copied fixture
comment was corrected. The docs/catalog-only follow-up was independently clear.

The branch normally merged remote PR132 candidate
`bf3ef84f53da7fcd9ba7dc2c2d3fd6b116f221cb` (which includes PR128) into local
`0912aee1c33c18973f005f5d4f1e9d0c761281e5`, tree
`1ef7f5c2786b9e6a44bdf5306e464e2f4b69e5ea`. The18 shared-file conflict
resolutions preserve all governance/computer/group registrations and tests;
no computer/governance-specific file differs from that incoming candidate.
Group/computer synthetic fixture modes now reject simultaneous selection in
Go and Python; both group CLI choices are tested through actual --help.

Independent combined-source review found no blocking issue and verified all50
CI jobs:34 browser+7 fixed-engine+4 auth shards+5 scalar. Existing32 browser and5
fixed suites, permissions, timeouts, parallelism, artifact and verify gates
remain. Reviewer independently passed8 Python/CI checks, both Group/Computer
StartTLS/LDAPS BER fixtures and diff whitespace validation.

This exact local joint code candidate passed make check:2069 DOM tests in49
files,136 Python contracts, Go race/vet/build, TypeScript/build and audit0.
Integration-tag auth/domain compilation and both group browser suite discovery
also passed; discovery does not run browser behavior. Source/results remain
separate from the later documentation-only commit and remote commit metadata.

No group PR or candidate CI has yet been published. PR128 and PR132 are still
unmerged dependencies; a draft group PR will initially target the verified
PR132 branch. Full exact-head real CI, browser screenshots/native receipts,
final-main/base compatibility and separate main-push verification remain gates.
Future results are not evidence. Local commits use dot bot; the supported
GitHub object API selects the connected account's author/committer. Publication
must verify matching tree and parents and must not misstate remote authorship.

The catalog change marks only AD-F-042 in_progress and preserves all original
field/control mappings,209 requirement scope and product_accepted=false.
Counts are computed from this branch and must be recomputed if later integration
changes other statuses. Group classification, missing legacy fields, members,
relationships, exports, real AD/eight Windows/capacity remain unaccepted.
