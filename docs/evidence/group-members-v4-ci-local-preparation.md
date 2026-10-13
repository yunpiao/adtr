# Group membership v4 primary runner: integrated local preparation

Date: 2026-10-13 UTC. Issue #30 / AD-F-043 remains open.
Verified main base: `bd302da441323891cec156d951b06f4f4369181d`, tree
`f6cb0661fffb40b563834cd96916590fb405a2ea`.

## Scope and current baseline

This candidate applies the original 25-file UI/parser/route/E2E/CI follow-on
change to the actual merged backend/main tree, preserving all intervening
backend test fixes and CI changes. It does not replace main with the older
recovered tree. The 17 UI/E2E and evidence-validator files remain byte-identical
to the previously reviewed tree `00dac29d7e548883828f4871208023abc10cccae`;
the current integration's changed CI baseline requires fresh independent review.

The suite is one additional primary browser matrix entry:
**55 existing jobs + 1 = 56 intended jobs, not executed jobs**. Auth remains eight
shards with max-parallel two; browser entries increase from 35 to 36 with the
existing max-parallel eight. The official digest-pinned Go image early identity
check/artifact, dynamic matrix counting, and all existing evidence gates remain.

The new contract mechanically removes only the new entry and its evidence block
to recover the merged-main workflow SHA-256
`d2d4f3bd2c783c137e1c9195ba1c865d6bfe2eb1df54aa2347dff993dc187295`.
Reversing only the runner additions likewise recovers main runner SHA-256
`c928e95aa401f6d6ec5a84b7cfe6a40f84311f4a5a3cc7075d76f1c44e19d56d`.

The suite belongs exclusively to the existing dictionary4 fixture mode and
requires the real registered Worker, isolated PostgreSQL, synthetic TLS LDAP
and production UI. Existing 630-second Playwright and 660-second runner bounds
remain; no existing authorization, retries, concurrency slots, fixture/backend
implementation or production configuration are weakened.

## Mandatory evidence and failure preservation

The runner requires the exact-checkout membership evidence validator after a
successful browser process. CI independently requires the same validator before
its SHA-pinned upload action. Evidence includes one membership JSON and six real
1440/390-width PNGs bound to stored bytes, source digests, head/tree and CI run.
The artifact step uses `always()` and scoped JSON/PNG paths to retain partial
failure artifacts. Missing files warn only in the upload; successful tests
cannot pass without the complete validator. No validator runs in cleanup after
a browser failure, preserving the originating error. Existing safe diagnostics
are unchanged; no traces or raw error DOM are added.

## Current local checks

Actual checks on this integrated implementation using existing Go 1.27.1,
Node 24.19.0 and installed dependencies:

- `make lint test build`: passed; requirement verifier, gofmt guard, Go vet,
  216 Python contracts, full `go test -race -count=1 ./...`, production Go build
- `npm run typecheck --prefix web`: passed
- `npm run build --prefix web`: passed (existing >500 kB chunk warning)
- `npm test --prefix web`: passed, 59 files / 2263 tests
- Original 25-file scope and byte-exact preservation checks: passed
- `git diff --check`: passed

These are available `make check` constituent checks, not an unqualified full
`make check` pass: dependency installation was not run and networked npm audit
was skipped, not reported as passed. npm was offline and reused existing local
dependencies. Unit/mock specimens are contract tests, never real acceptance.

No real PostgreSQL, browser acceptance, upload, PR or remote CI execution was
performed for this candidate. The backend/main's separate successful gates do
not validate this new UI suite. Current exact-published-head 56-job CI, real
browser/PG evidence and independent integration review remain required before
any merge claim. Publication waits for main-push CI and explicit coordination;
this report is local preparation, not evidence of publication or product signoff.

## Remaining scope

The E2E actor remains platform_admin after credential-use revoke; no read-only
role acceptance is claimed. Ranged-member wire responses, zero-group observations,
>25-group UI pagination, Windows/real AD and production capacity remain gaps.
Raw member values exclude primary-group-only relationships; nested expansion,
target resolution and cross-domain completeness are outside this slice. Multi-page
observations are not a single-time AD snapshot. Issue #30 / AD-F-043 and broader
product acceptance gaps remain open; no production deployment was performed.
