# Independent model verification for synthetic group relationships

Engineering Issue #308; simulation batch #251; existing AD-F-043 (#30) and
AD-F-044 (#29) engineering contracts. Base main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`.

## Purpose and limits

This adds independent executable evidence for
[the existing synthetic contract](simulated-group-assumptions.md). It does not
change the production algorithm, claim a newly discovered production defect,
resolve original-product ambiguity, or turn dictionary4 raw member strings into
an effective-permission graph. The original seven test functions remain intact.

The external `groupgraph_test` package calls only public `Build`, `Diagnostics`
and `Query`. Expected values use a fixed adjacency matrix and Floyd-style
all-pairs shortest-path dynamic programming, not production's BFS traversal.
Cyclic strongly connected components are equivalence classes of mutually
reachable groups, not a copy of production's Tarjan implementation. Singleton
cycles require an explicit self edge. Provenance, returned nodes, distances,
direct flags, ordering and all unknown/completeness fields are compared, not
just counts or the presence of fields.

## Exhaustive finite model

There are three groups, one user and one absent target. Each group has 33 legal
states: one unobserved-membership state with no supplied outgoing edges, or each
of the 32 explicit edge subsets over the three groups, user and absent target.
Known-empty and unknown are distinct states. This exhausts **33³ = 35,937** legal
snapshots in that finite universe, including self loops, diamonds, cycles,
multiple SCCs, isolated groups, unknown groups and absent references together.

For each snapshot:

- Whole-graph unknown-edge and SCC diagnostics are checked once.
- Forward members queries use each of three group roots; reverse member-of uses
  all four roots. Each is checked as direct and transitive: 14 query shapes.
- Each shape uses depth/results budgets `(3,3)`, `(1,3)` and `(3,1)`, giving
  **1,509,354 query comparisons**. A depth/result limit failure must have the
  exact `ErrLimit` class and a completely zero `Result`, not partial success.

Forward unknown diagnostics follow only the inspected root/reachable groups;
reverse queries list every unobserved group, even when unreachable by known
edges. Absent targets remain diagnostics, never invented nodes or confirmed
relationships. The root is excluded even through cycles. Limits apply to known
distinct relationships and shortest distance, not repeated visits or absent
reference diagnostics. Completeness has only the supplied-snapshot meaning.

The small model does not replace existing 4096-node/64-depth and malformed-input
coverage, nor prove every larger graph, identity encoding or transport behavior.
Its asserted coverage totals prevent accidentally reducing the enumerated loop.

## Computer, ordering, ownership and errors

Every 97th model code gives 371 additional deterministic Computer-leaf snapshots.
Each reverses input node/edge order, mutates the caller-owned input after Build,
and mutates returned cycle/unknown/relationship collections. It then compares
all 14 query shapes to the independent expected result: **5,194 comparisons**.
This section issues two Query calls per comparison (obtain/mutate, then recheck),
so its actual Query invocation count is 10,388, not 5,194.

Separate cases check cross-tenant/domain scope, absent root, user-as-members-root,
invalid direction, zero budgets and result-limit failure; valid queries after
failure must still agree with the unchanged model. Mixed-case and composed vs.
decomposed opaque IDs retain their distinct exact identities. This is not runtime
RBAC, DN resolution, primary-group or cross-domain authorization evidence.

## Compiled negative controls

Run with the existing Go 1.27.1 toolchain and cached environment:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=1 ./internal/groupgraph
python3 scripts/verify_groupgraph_model_mutations.py \
  --output /path/to/new-evidence-directory
```

`--output` is required and must not exist. It stores isolated minimal modules,
source/test copies, binaries, commands, compile/run logs and a SHA256 manifest;
no output is deleted. An existing path is rejected instead of overwritten.
Copies consume disk and execution time; choose an evidence directory outside the
checkout. The script sets `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off` and
`GOWORK=off`, requires Go 1.27.1 and contains no dependency installation or CI call.
These parameters make the control run independent of downloads and surrounding
workspaces; removing them could change which toolchain/module environment runs.

The unchanged baseline must compile and pass. Each exact-anchor mutation is
compiled separately, then must exit with a normal model-test assertion failure.
Build failures, timeouts, panic/fatal exits, unexpectedly surviving mutations or
changed anchors fail the verification rather than count as detection. Per-copy
compile/run limits bound a failed harness; they do not alter production limits.

The 16 controls alter distance, direct flag, root exclusion, reverse direction,
unknown reference identity, forward/reverse unknown membership, completeness,
depth/result off-by-one, truncated-success errors, singleton/all-cycle reporting,
relationship sort, returned-cycle ownership and scope equality. These controls
show that these errors are detected; they do not prove the oracle finds every
possible implementation defect.

## Evidence status

Original groupgraph race baseline passed. The new finite model passed against
unchanged production without requiring a production fix. All 16 controls compiled
and were detected by assertion failure. Final full-Go race/vet/build,
requirements, independent review and exact non-triggering remote backup results
are recorded in Issue #308 after they finish, rather than predicted here.

Only new tests, the isolated negative-control script and this document are added.
No audit, installation, `make check`, CI, PR, merge, deployment, real AD/LDAP,
credential use or security-permission change is performed. APT/probe/R4/CI
selection and PR225 remain untouched. Real browser/PostgreSQL, original product
semantics, Windows, capacity and precise-head integration remain separate gates.
Issue #308 stays open and product acceptance remains **0/209**.
