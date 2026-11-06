# Domain query position: native evidence preparation

This is unpublished source/offline preparation for
[Issue #16](https://github.com/yunpiao/adtr/issues/16), AD-F-001 / UI-0390.
The issue was freshly read on 2026-11-06 and remains open. Its original record is
`UI-0390｜el-table-column｜序号｜未声明直接绑定｜{"type": "index"}`.
The later mapping in `docs/domain-contract.md` is "Paged connection row index
from actual page offset". The implementation follows that explicit mapping,
not the page-local numbering contract used by some other lists.

After the previous workspace became unavailable, this candidate was rebuilt
from verified remote PR #296 commit
`21fcba85fb057a1327be33955edd0d8aa84f416e`, based on main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`. Historical local results are not
used as current evidence. Existing dependencies were copied from a matching
lockfile with SHA-256
`fb73eca2ec9a028891dd12cb85086d51663c25677651d007dfc798e751c60261`.
No dependency installation was performed.

## Added observations

The existing `applyStatusDraft` waits for the actual browser-originated
`GET /api/domains` response, checks its request parameters and status 200,
applies the original safe-data check and verifies the persisted connection.
The additions consume this same response without another business request:

- Bind response metadata to the already checked request's page 1 and size 10,
  with the existing one-domain/empty expected total and total-page count.
- Compute expected positions in response array order as
  `(response.page.pageIdx - 1) * response.page.pageSize + index + 1`.
- Match each rendered ordinal, visible domain ID and detail button's
  `data-domain-id` to the same returned row; reject duplicate response IDs.
- Check all eight column labels and each `scope="col"`, with domain first and
  actions last. Existing mobile CSS clips `thead`, so this verifies semantic
  headers rather than claiming visible mobile headers.
- For empty results require no table, data row, ordinal cell or detail-ID
  action; retain all existing empty-state and write-count assertions.
- Add the ordinal cell to the original `measureListFact` observations at
  1440×900 and 390×844. Existing text-fragment, wrapping, clipping and table/page
  overflow thresholds remain unchanged. All seven original screenshots retain
  their order and safe-capture checks.

The existing successful manifest gains `listIndex`: safe request method/path
and duplicate-preserving query entries; actual status/page metadata; expected
domain-ID/position pairs; actual rendered ID/identity-text/position-text; and
the original capture checkpoints' ordinal/identity geometry. Its source SHA
map additionally binds the domain contract, this document, position DOM tests
and both offline guard scripts. Existing source and artifact bindings remain.
No credentials, proofs or additional response payloads are included by these
new evidence fields.

The additions attribute a future ordinal result to the actual response and
source. Removing them loses that evidence and is rejected by the inverse
guard. The cost is additional read-only DOM assertions and a larger existing
manifest; timeout and retry budgets are not increased.

## Exact preservation and offline checks

`scripts/domain_list_index_native_source_contract.py` pins six additive blocks
by unique original anchor, unique start/end marker, byte size and SHA-256.
It removes only those exact blocks and requires all 43,844 original spec bytes
to hash to:

`8a987a9405764e2c3c5c24b70fece9db7ee9db04ed707335b19c928a48afd7d8`

The rebuilt native spec contains 142 additive lines (5,433 bytes), with SHA-256:

`761cc1e8c5d6c4531a74ea2d8531fdd8dc373d525fe0b8e1764a852efe67eece`

`scripts/test_domain_list_index_native_contract.py` checks omission,
duplication, relocation and alteration of every added line/block/anchor, plus
changes to old proof, capture, write-count, timeout and geometry checks. It also
executes the exact added TypeScript helper against synthetic locator values in
Node. Eleven positive cases cover response order, all five exposed page sizes,
later-page offsets, empty/out-of-range pages and upper page-index/size bounds.
Thirteen negative cases reject wrong position, row order/identity/count,
missing/hidden ordinal, missing/wrong-scope header, wrong action, leftover empty
rows and duplicate IDs. This is a fake-locator unit harness, not a browser,
HTTP, PostgreSQL or LDAP run.

Focused commands, using only existing local dependencies:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/test_domain_list_index_native_contract.py -v
node web/node_modules/typescript/bin/tsc --noEmit -p web/tsconfig.json
node web/node_modules/prettier/bin/prettier.cjs --check web/e2e/domains.spec.ts
node web/node_modules/vitest/vitest.mjs run --root web src/DomainListIndex.test.tsx src/Domains.test.tsx
git diff --check -- web/e2e/domains.spec.ts
```

Fresh execution logs and exact source hashes are recorded in the rebuilt
candidate's separate evidence report. Old workspace passes are not inherited.

## Remaining gates and exclusions

The unchanged native fixture has one isolated synthetic domain. Even after a
future native pass, this flow establishes only page-1 position 1 and empty
results, not real second-page or multi-domain ordering. Synthetic later-page
checks and DOM fixtures do not close that gap.

No native suite, browser or PNG review was performed for this preparation.
The added mobile field and reduced desktop column width still need actual
rendering and independent image review. Any layout assertion failure must be
investigated rather than weakened. Old helpers, authentication, proofs, writes,
recovery flows, native runner, selection, CI and screenshots are unmodified.
No fixture is enlarged and no new business operation is added.

`npm audit` remains explicitly denied. No install, audit, `make check`, PR,
CI execution or GitHub write is triggered. APT/B/probe/R4, CI-ordering/
leaf-selection and PR225 denied-blob stop lines remain. No unverified system
Chromium, user computer, real AD, deployment, force-push, destructive deletion,
credential transmission or security permission change is used. Independent
exact-diff review, authorized full checks, exact-head CI and native/image
evidence remain separate gates. Issue #16 stays open; product acceptance 0/209.
