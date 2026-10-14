# Full-scope acceptance ledger

`catalog.json` preserves all 209 AD-F requirements, 57 feature groups, 3,258 field
record IDs and 804 UI control IDs from the source workbook. Each record links to
its detailed GitHub Issue and source sheet/row. The workbook SHA-256 identifies
the inspected source. A UI control may be related to multiple features; a shared
component reference does not prove the control appears in every parent page.

Statuses are deliberately distinct:

- `not_started`: no implementation evidence recorded
- `in_progress`: implementation is active, not accepted
- `implemented_verified_slice`: runnable implementation and specific tests exist,
  with outstanding compatibility or release gates listed in `evidence.remaining`
- `accepted`: all feature acceptance gates have been verified, with no remaining
  items; this is not the same as a PR, merge or deploy status

Current product acceptance remains **0/209**: **19 implemented_verified_slice**
requirements, **34 in_progress**, and **156 not_started**. These are requirement
status counts, not PR counts or 19 fully accepted features. AD-F-042 is now
in_progress for [the bounded raw-group workspace](../docs/group-assets-v3-contract.md);
its classification fields, source compatibility and real acceptance remain open.
AD-F-043 is now in_progress for [the bounded raw-member backend](../docs/directory-v4-contract.md); legacy count semantics and complete membership/UI remain open.
Only AD-F-157/158/159
change status in the [2026-10-11 reconciliation](../docs/delivery-reconciliation-20261011.md):
real health-message runtime/UI, single/bulk read recovery and fixed-query counts
now have implementation evidence, while full F40 compatibility remains open.
AD-F-034 and AD-F-038/039 remain in_progress with corrected v2/runtime/filter
scope; B2, task, audit and system evidence is refreshed without product acceptance.
The reconciliation binds each relevant PR head, merge commit and actual Actions
run separately. Baseline `6f7f44a4997f78b6a50eab1ba657ffcc15769c1a` passed all 36
jobs in [main-push CI 38154440236](https://github.com/yunpiao/adtr/actions/runs/38154440236).
That success is baseline evidence, never a pass for a future documentation commit.
The catalog records narrow implemented scope and remaining gates; passing a shared
suite does not verify every source field. Original evidence is retained in
`previous_evidence` or supplemented by `latest_regression` where applicable.
AD-F-053 is now `in_progress` for the merged
[bounded EXP-006 user XLSX export](../docs/user-asset-export-v3-contract.md).
[PR159](https://github.com/yunpiao/adtr/pull/159) and its exact-head/post-merge
60/60 CI runs verify the normal 12-row and header-only synthetic browser paths.
This records implementation evidence, not full feature acceptance: only six
collected scalar columns from one pinned dictionary3 domain/observation are covered.
Failure recovery and queued cancellation in a real browser remain planned,
not verified by those two runs; HTTP/PostgreSQL/DOM coverage is distinct.
EXP-007 computer, EXP-008 group and EXP-009 DNS/IP exports remain outside this slice.
Issue30 and Issue151 remain open. The new documentation/code revision must pass
its own checks; old CI success cannot be transferred to it.
Source-system compatibility, real AD/Windows and production gates remain open.
Priority never removes a requirement.

`python3 scripts/verify_requirements.py` is part of `make check`; it fails on scope
reduction, duplicate requirement IDs, missing field/control references or an
acceptance claim with unresolved gates. Source definitions remain in the linked
Issues; no omitted field is silently treated as implemented.
