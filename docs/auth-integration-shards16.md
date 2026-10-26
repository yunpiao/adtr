# Auth integration: sixteen deterministic, bounded shards

Auth integration uses sixteen sorted-stride subsets instead of eight. This
changes work allocation, not acceptance: discovered tests, examples and fuzz seed
cases execute exactly once, and each top-level case retains all its subtests.
All prior non-auth, browser, fixed-engine and native gates remain required.

## Why split

PR #227 run [40247060717](https://github.com/yunpiao/adtr/actions/runs/40247060717)
reached the explicit Go package 600-second timeout in auth shard 2 on attempt 1
and its one unchanged retry. The same 43 completed top-level tests consumed
561.38 and 568.46 seconds; the last case was interrupted. On the parent main
run, those 43 took 438.43 seconds. The final stack is only an instantaneous
fixture/connection sample; neither a particular SQL lock nor a CPU/host cause
has been established. No third unchanged retry is a prerequisite for this split.

The change addresses demonstrated package-allocation headroom, not a claimed
host/DB fix. It does not raise the timeout, remove slow tests, disable race
instrumentation, reduce password proof cost or relax fixture/assertion checks.

## Coverage and parameters

`AUTH_SHARDS = 16` controls both the CLI shard domain and sorted-stride dispatch.
For sorted names `names`, new shard `i` gets `names[i::16]`. For each old index
`i` from 0 through 7, old `names[i::8]` is the disjoint union of new `i` and
`i + 8`, preserving the exact old parent coverage. At the reviewed source
revision there are 352 eligible top-level names: sixteen sets of 22. Runtime
Go discovery remains authoritative and dynamic; 352 is not a permanent limit
or a checked-in allowlist. Insertions, examples and fuzz seeds stay covered.
Duplicate/malformed discovery and any empty shard fail closed, including fewer
than sixteen names. All-mode still executes three non-auth groups and all
sixteen auth groups, returning failure without skipping later groups.

The raw workflow matrix, runner, selector and independently recomputed summary
must agree on ordered IDs 1–16. Selecting auth selects its complete matrix;
a name insertion can change later stride membership, so path-based exclusion
of individual auth subsets is not safe. Unknown/shared changes still select
all gates. Historical eight-shard/source-hash contracts retain their original
bytes through one strictly bounded exact inverse; separate raw-workflow tests
prevent that historical projection from hiding an incomplete current matrix.

These bounds remain required:

- `-race -count=1 -tags=integration -timeout=10m`: uncached real integration
  tests with race detection and a 600-second per-process limit. Removing them
  would weaken acceptance or lose bounded failure evidence.
- Auth `max-parallel: 2`, `fail-fast: false`, and 20-minute job timeout: no more
  than two auth jobs execute simultaneously, every shard can report after a
  peer fails, and setup plus execution stays bounded. More shards do not grant
  more concurrent auth DB/process work. Removing the parallelism bound can
  increase resource contention; changing fail-fast can suppress later evidence.
- Per-case/request/cleanup limits, fixture costs, image provenance, permissions,
  other matrix order and all other workflow bytes remain unchanged.

## Cost and limits

Full gate expansion is **85 jobs**, previously 77: 56 browser, 7 fixed-engine,
16 auth, 2 native and 4 scalar jobs (plan, prepare, contracts, verify). Eight
additional auth jobs repeat isolated container/discovery/build/image-consumer
setup. With at most two concurrent auth jobs, there can be eight waves rather
than four. Total CI duration and billed work may increase; no overall speedup
is promised. Per-process budgets stay 600 seconds, while aggregate potential
process time grows with the number of jobs.

For the previously heavy old shard 2, complete historical main per-test sums
split into 187.39 and 282.52 seconds for new shards 2 and 10. The failed retry's
completed cases split into 236.64 and 331.82 seconds; the latter excludes its
unfinished last case. These are measured subdivisions, not predicted or passed
new shards. One indivisible future test could still exceed 600 seconds, and
host/caching/scheduling behavior may differ. Fresh current-head CI is required.

## Acceptance

Offline contracts cover raw matrix omissions/duplicates/reordering/old-eight
regressions, unchanged flags/budgets, exact historical byte inverses, selector
and summary closure, and synthetic odd/new/undersized inventories. A local
source-name simulation proves the current 352-name partition without launching
PostgreSQL or Go test bodies; it is not runtime acceptance.

Before merge, inspect the current run's actual checkout, tree and parents;
require all 85 current-head gates and independently review the final PLAN/NEEDS.
Collect the sixteen actual auth logs and reconcile discovered/selected/executed
names exactly once for that source revision. Preserve every failure and all
mandatory cleanup/evidence requirements. Successful historical runs or local
simulation do not replace these gates. Rebind dependent product PRs against
actual main after merge and run their own fresh current-head checks.
