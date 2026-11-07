# Offline recovery validation, 2026-11-07

This bundle preserves a new execution after checkout recovery. Source HEAD is
`503022338044ddfaffb26836449da58db2a798ea`, tree
`8fa959a7f56eea7493f745141893ea2fd9fec0a9`. The tested worktree was clean.
It is an evidence backup, not a PR, product delivery, release or merge approval.

## Verified in this execution

- DOM: 117/117 files and 5371/5371 tests, exit 0, 134.41 seconds.
- Python contracts: system 6, domain index 7, domain source fixture 1,
  access search clear 4, access row numbers 4: 22 total, all exit 0.
- TypeScript no-emit, Vite build, candidate formatting, requirements mapping
  and diff whitespace checks: exit 0. Vite reports its existing >500 kB chunk warning.
- Restored 1529 blobs and 21 candidate-manifest files were checked.
  The 38 older evidence containers were checked for integrity only; their tests
  are not counted as this execution. Two recovery-verifier invocation attempts
  failed before hash checks due to path/cwd errors, then were corrected without
  source changes. See recovery-verification.json.
- Independent source/restoration review found no new blocking defect within
  its reviewed scope and separately checked the final current DOM receipt.
  The private review report is intentionally not included.

## Runtime and outstanding gates

Node was v24.22.1, while README and CI prescribe 24.19.0. The copied offline
lockfile matched byte for byte; locked package engine constraints accept the
observed runtime. These results are valid observations on v24.22.1, not proof
of the same behavior on 24.19.0. No observed failure was attributed to this
difference; rerunning under a verified prescribed runtime remains necessary
before claiming prescribed-environment verification.

No dependency installation/download or audit, full make check, native
API/Worker/PostgreSQL/browser execution, PNG capture/pixel review, remote CI,
PR, merge or deployment is represented here. PostgreSQL was unavailable and
browser provenance was insufficient for execution. Native and product
acceptance were not performed in this execution. These offline results add no
new requirements-acceptance conclusions.

The remaining prescribed native prerequisites include Go 1.27.1, Node 24.19.0,
the pinned PostgreSQL 17.6-alpine image under isolated Docker, and Playwright
1.56.1 Chromium 141/revision 1194. The system/domain suites require the real
Worker; domain additionally requires synthetic TLS LDAP. Docker, the prescribed
Node runtime and a verified browser are currently unavailable. Bare PostgreSQL
would not substitute for the existing runner and its isolation/source bindings.
No code was changed by this recovery validation; no real E2E run occurred.

## Evidence format and verification

Each logs/*.evidence.json is a UTF-8 line container. Concatenating `lines`
exactly reconstructs the preserved bytes, including whitespace and newline
endings. Check those bytes against preservedBytes/preservedSHA256. Original
byte lengths and SHA256 digests are retained. All original logs and receipts
were verified against the finalized producer manifest before packaging.
The DOM banner's local absolute checkout path is replaced with <CHECKOUT>;
that log is explicitly sanitized, not claimed to be unchanged raw output.
Other logs and exit receipts are byte-exact. Every .exit receipt is `0\n`.

Metadata path substitutions/summaries are documented in the individual JSON
files and source-transforms.json. commands.txt is a historical execution
record with the dependency-copy source replaced by a role placeholder.
It is not a runnable recipe authorizing restricted checks. No original private
reports, account data, credentials, environment dumps or local absolute paths
are included in this new bundle. Existing historical repository records are
not rewritten. evidence-manifest.json hashes every other file in this bundle;
the manifest itself can be pinned by the containing Git commit or its hash.

## Backup preflight

workflow-trigger-check.json records current main/candidate workflow content
identities, all subscribed events and branch/path filters, plus target absence.
Only docs/evidence/offline-validation-20261107/ is proposed for addition on a
new branch based exactly on the tested HEAD. Source/workflows remain unchanged.
The trigger conclusion is limited to the observed repository declarations;
refs, target state and applicable filters must be rechecked before publication.
No remote write has been performed while preparing this bundle.
