# Computer assets v3 engineering validation

Refs #131 / #30 / AD-F-040. This record does not close either Issue and does not
count as product acceptance.

## Version and completed local layers

- Clean baseline main `6beb495329e226ede42004a1063a1a57857d3216`: make check exit0,
  1643 DOM / 109 Python; Go race/vet/build, frontend type/build, audit0.
- Independent backend first commit `81218c9`, normal local integration of PR126
  exact head `3950997b282c530e6fcdb13feead4953820a4804` in `5f44f9a`; v3 fact parser
  is reused directly. No production publication from these local commit names.
- Focused backend Go race (domains/auth/cmd),18 new DOM/parser/transport cases,
  TypeScript and all-package integration-tag compilation passed before final
  independent review corrections. These are not final-head aggregate evidence.
- Dedicated fixture real StartTLS/LDAPS BER pages and production v3 codec pass:
  23 computer / 3 user / 4 group records, two pages, hostile/raw/null facts.
  This in-process fixture layer is not the registered Worker/PG/browser chain.

## Independent review and real failures

Independent actual-source review identified an accepted unfiltered count mismatch,
malformed-query precedence, and mux/error boundary mismatch. Corrected strict
count equality for no-predicate queries, validate known input before unsupported
422, and exact+prefix route registration with mux tests. Review also corrected
explicit desktop viewport, actual login heading, and required clipboard/source
revocation cases in the new E2E harness. Final review status belongs to the exact
candidate/PR record, not this historical paragraph.

The first full-check attempt failed five existing Python CI inventory assertions
after adding four required computer gates. Their exact inventories now require
43 jobs instead of39 on the PR126 integration baseline: original27 browser→29,
original3 fixed-engine asset→5; all original gates retained. No continue-on-error,
weakened production assertion or removed case.115 Python contracts then passed.

## Required gates still pending

Actual real PG store/
HTTP cases; registered Worker→owned TLS LDAP→PG→API→browser tests for both normal
and fixed browser engines; desktop/mobile/native receipts and independent pixel
review; current-main combined-head independent review/CI and merge/post-merge.

Local Docker is absent. Attempt/result logs must identify unavailable execution
as blocked rather than pass. No real AD, eight-Windows, capacity, DNS/IP/OS/login/
relationship compatibility, production deployment or complete AD-F-040 claim.

## Final local aggregate and execution blockers

Final local code plus explicit navigation-inventory expectation passed make check:
1800 DOM tests in44 files,115 Python contracts, Go race/vet/build, TypeScript,
frontend build and audit0. All-package integration-tag compile/vet and dedicated
computer StartTLS/LDAPS fixture race tests also passed. The second aggregate
attempt had failed only the exact primary-navigation test inventory; adding the
new computer entry preserved all existing expected buttons and the next aggregate
passed. No production code or assertion was removed to resolve that failure.

Independent reviewer inspected frozen `c0ca73d` against PR126 `3950997` and ran
focused backend race, both TLS fixture modes,18 DOM tests and2 E2E discovery.
Source review has no confirmed remaining blocker, conditional on real runtime,
visual, current-main and exact-head CI verification. Subsequent navigation-test
inventory change and this evidence record require final candidate confirmation.

Actual local real PG attempt (`go test -tags=integration -count=1
./internal/domains ./internal/auth -run '^TestComputerAssetsV3'`) exited1 because
ADTR_TEST_DATABASE_URL was absent. Actual browser harness attempt (`python3
scripts/test_auth_e2e.py --suite computer-assets-v3`) exited1 because docker was
not found. These executions are blocked, not passed. The required four CI browser
gates remain the way to establish the registered producer/PG/browser evidence.

This draft depends on PR126's exact v3 parser, not PR128's collection UI. Final
retarget to main must preserve PR128's additive navigation/harness wiring if it
has merged; the resulting union then has46 required jobs rather than this43.
