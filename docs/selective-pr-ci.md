# Change-scoped pull-request CI

Pull requests select checks from changed files and their consumers. Main pushes,
merge groups and the explicit **Run workflow** action retain the complete existing
suite. No test assertion, retry policy, timeout, evidence validator or browser
engine is weakened. A workflow/selector change intentionally selects the full
suite once, because it changes how every check is reached.

## How the plan is derived

`plan` fetches complete Git history with read-only checkout and no persisted
credentials. It uses the event's immutable base/head revisions, computes their
unique merge base, and parses `git diff --name-status -z --find-renames`.
Renames include both old and new paths; deleted files retain their old consumers.
Missing objects, ambiguous merge bases, unreadable diffs, malformed paths and empty
PR diffs fail planning rather than producing an empty test selection.

The changed-file range is merge-base to PR head. Dependency graphs include the
merge base, PR head, event base and the actual tested merge commit (`github.sha`).
This also catches consumers introduced on the target branch after the PR forked.
The repository's locked TypeScript parser reads source without executing it.
Imports, re-exports, import-equals, literal dynamic imports/require and relative
`new URL(..., import.meta.url)` source readers are included. Unresolved/computed
imports and syntax errors cause conservative selection; they cannot prove a narrow
plan. The plan and final verifier each install the same locked parser. This adds
short setup work in exchange for independently checking a correct selection.

## Current selection boundaries

- Ordinary `docs/**/*.md` and `README.md`: mandatory selector/parser contracts,
  current dependency-map checks and requirements-ledger checks; no fabricated
  browser result. Executable docs, requirements data and unknown paths are not
  classified as documentation-only
- Frontend/e2e source: actual transitive dependencies select affected suites and
  their fixed-engine variants. A standalone unit test uses the original contracts
  job unless production/e2e imports it or its ownership is uncertain
- Reviewed page roots: MessagesWorkspace; User, Computer and GroupAssetsV3Workspace;
  TaskWorkspace. Their dependency closures cover API modules and helper components.
  Other production consumers, App/global composition, shared APIs and unknown
  frontend dependencies conservatively expand to all browser/fixed/native checks
- UserAssetExportV3 is rendered by UserAssetsV3Workspace. Its API is also consumed
  by UserAssetExportTaskResult inside TaskWorkspace, so API edits include task-page
  consumers as well as export acceptance
- TaskWorkspace has 15 reviewed browser consumers: tasks, messages, maintenance,
  user-assets-v2, directory-v3, group-members-v4 and normal/failure/queued-cancel
  export suites for each of user/computer/group. The fixed user-assets-v2 variant
  follows automatically
- Each of the three v3 asset roots includes directory-v3-controls, whose empty
  observation helper navigates all three asset pages
- `messages-bulk.spec.ts` belongs to the messages job: the real runner executes it
  recursively after messages. `auth.spec.ts` belongs to both authentication and
  expired-password. `resource.spec.ts` belongs to resources. The native-stream
  spec selects both engines. Inventory checks cover all 52 specs, 51 browser jobs
  and seven fixed-engine variants in both directions
- The owned running-cancel controller/guard selects its three real running-cancel
  consumers plus contracts. Other shared runners/fixtures conservatively expand
- Shared Go/auth/DB/tasks, build files, dependency locks, CI/selector files and
  otherwise unknown paths select contracts, all eight auth shards and all browser
  variants. Eight auth shards remain indivisible because test-name insertion can
  move tests between shards. Reasons are printed for every changed path

`contracts` keeps its original complete lint/race/type/build/non-auth PostgreSQL
and lifecycle checks whenever selected. This first change scopes expensive
acceptance jobs; it does not invent a new reduced unit-test assertion set.
PostgreSQL preparation runs only when a selected existing consumer needs it.

## Final acceptance

`verify` always runs. It independently rebuilds the complete plan from the same
immutable revisions, tested commit and inventory, then requires exact equality.
This rejects a schema-valid but incomplete selection. Every selected job must
report success; skipped, cancelled or failed selected jobs fail verification.
Unselected jobs must report skipped; unexpected execution/failure also fails.
Fixed variants must be exactly the selected-browser/fixed-inventory intersection.
The summary labels unselected suites as **not run**, rather than passed.

Existing workflow contracts use an exact, reversible projection of this reviewed
CI-only delta. The original full-workflow hash still protects all old commands,
permissions, concurrency budgets and evidence steps. Separate contracts mutate
selection conditions, dependencies, matrices and verification to ensure changes
cannot be erased by that projection. Future intentional workflow edits must
update this boundary under review.

## Validation

Offline Python tests cover rename/copy/delete, multi-path order independence,
documentation, shared and unknown paths, base/merge consumers, uncertain imports,
missing diff, plan tampering and all selected/unselected job outcomes. CI also runs
the real TypeScript parser fixtures and exact current-repository selection probes
for messages, bulk messages, user export, computer assets and task pages. Local
mocked tests do not substitute for those real CI executions.

## Recorded rollout evidence and reading a run

The first infrastructure rollout is PR #184. Its final commit
`68084bfbaac58d695caba8b379979080300430b4` selected the complete suite because it
changed the workflow and selector. Run
[39356052487](https://github.com/yunpiao/adtr/actions/runs/39356052487) completed
successfully on attempt 2, with all 72 latest checks successful. The plan included
51 browser suites, seven fixed-engine variants and eight auth shards. The real
parser's seven fixtures and current-repository dependency probes passed. The final
verifier rebuilt the plan from all four immutable revisions; the retry's complete
plan was identical to the first attempt's plan.

Keep both earlier failures in the rollout record. The initial implementation
missed the reviewed workflow projection at two old empty-asset contract entry
points; a test-only correction retained their original hash and assertions and
added a regression scan. On the corrected commit, the first directory execution
lost one HTTP connection while reading credential-use state. The verifier
correctly rejected that browser failure even though plan equality passed. A single
failed-jobs retry passed without changing code or acceptance criteria. Safe
cleanup diagnostics showed normal API/worker exits, but the connection loss's
underlying cause remains unknown.

When reviewing an ordinary PR, inspect the `plan` result and the final `verify`
summary together. Confirm the immutable head and tested revision, reasons for each
changed path, selected matrices, and the exact selected-success/unselected-skipped
result. A skipped browser suite has not exercised that PR. A shared dependency or
an uncertain consumer can legitimately expand a small textual edit to the full
suite; line count alone does not determine coverage.

A documentation-only follow-up records this rollout without changing the selector
or workflow. Its purpose is also to obtain the first real narrow-plan execution:
mandatory selection/parser/current-map and requirements checks should run while
business acceptance jobs remain skipped. Its outcome and observed duration must be
recorded from that follow-up's own run. At the time of this entry, that runtime
result and the merged rollout's fresh main-push result are still pending. Any
measured documentation-only saving must not be generalized to all code changes.
