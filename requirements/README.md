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
requirements, **35 in_progress**, and **155 not_started**. These are requirement
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
[PR160](https://github.com/yunpiao/adtr/pull/160) is merged at
`9f91f7121a00b02101f53c545e3da53da1ec27ed`. Its exact-head
[38497165578](https://github.com/yunpiao/adtr/actions/runs/38497165578) and separate
main-push [38501083953](https://github.com/yunpiao/adtr/actions/runs/38501083953)
each passed62/62 jobs, with independently reviewed actual failure/recovery and
queued-cancellation artifacts. The first run38493130354 still records its
queued-cancel/verify failure and truncated failed-state screenshot; missing PG
counts leave its precise connection count unknown. Running/cancel_requested
browser coverage remains open.
The [bounded EXP-008 group slice](../docs/group-asset-export-v3-contract.md) is merged
in PR161: five dictionary3 scalar columns, shared group-list search/order,
all matching rows, separate permission/task/storage and no automatic custom grants.
Exact final head `5c9b0ee673e35955bcaf4147cae075015bae4cf7` passed
[38530232594](https://github.com/yunpiao/adtr/actions/runs/38530232594), and merge
`7ed455aecee1352f0261e7064253891af1c31c79` passed
[38535766672](https://github.com/yunpiao/adtr/actions/runs/38535766672), each65/65.
Both runs' normal/empty, failure/recovery and queued-cancel artifacts were reviewed.
Earlier runs38520290658 and38525081993 retain their3 and2 failures respectively.
The10000-row/32MiB guardrails do not verify
legacy100000-row capacity.
The [bounded EXP-007 computer slice](../docs/computer-asset-export-v3-contract.md)
is merged in PR162 at main `94b098d6acf5187e1ecf9aa659f5abed00122ce7`, tree
`0c669bd750c2ec137311aad08b6a05744ddff907`: five collected scalar facts,
shared computer-list search/time-range/order, all matching rows and independent
permission/task/schema23 storage. Final head `09e1c52f8ceac2a2d85cdc8b4c71fbf515fa078a`
passed [38550970449](https://github.com/yunpiao/adtr/actions/runs/38550970449)
attempt3 with68/68; separate main-push
[38561279766](https://github.com/yunpiao/adtr/actions/runs/38561279766) attempt1
passed68/68, with fresh normal/empty, failure/recovery and queued-cancel artifacts
independently reviewed. Historical run38545054964 remains54 successful/14 failed;
38550970449 attempt1 remains66 successful/2 failed after reader socket-hangup
(root cause unestablished). Attempt2's passed reader/verify and duplicate queued
rollup are preserved separately from final attempt3 success.

The historical user-only running-cancel candidate added one job to these68 (69 expected),
without production/schema changes. It targets real running after snapshot/progress10,
then cancel_requested and cancelled, with exact owned-Worker/PG-lock/lease guards.
It is not runtime verified; exact-head CI and independent artifact review are pending.
It does not prove chunk-stage cancellation or fencing races; group/computer retain
queued-only cancellation browser coverage. See the
[historical candidate contract](../docs/user-asset-export-v3-contract.md).
Full EXP-007 legacy parity/capacity and EXP-009 DNS/IP exports remain open.
Issue30 and Issue151 remain open. The new documentation/code revision must pass
its own checks; old CI success cannot be transferred to it.
Source-system compatibility, real AD/Windows and production gates remain open.
Priority never removes a requirement.

`python3 scripts/verify_requirements.py` is part of `make check`; it fails on scope
reduction, duplicate requirement IDs, missing field/control references or an
acceptance claim with unresolved gates. Source definitions remain in the linked
Issues; no omitted field is silently treated as implemented.


The next group/computer snapshot-stage running-cancel candidate starts from PR163
main `ec0da2018cc5914b8d0ca345c1c2c4cd5078d177`, tree
`ec801e6ffc0bce09cd89714bb1be3586d2781fad`, whose separate main CI38588506730
passed69/69. It preserves those69 jobs and adds two isolated kind-specific slices
(71 expected). Exact-head CI and independent artifacts are pending for this new
candidate; the historical user-only candidate paragraph above describes its prior
checkpoint. Production code/schema/lease/timeout and acceptance counts are unchanged.

PR163 subsequently completed its user snapshot-stage slice: final head
`4d5bdcc4e29351ee49cc3f3d2d2ee8ad8e29462f`, PR CI38579256706 attempt2
69/69 and fresh main CI38588506730 attempt1 69/69; both current artifact sets
were independently reviewed. Historical failed attempts remain in the delivery
ledger. This closes that user engineering checkpoint, not full product acceptance.

AD-F-052 is now `in_progress` for the candidate
[bounded observed-count slice](../docs/directory-v3-contract.md), FLD-0586/0588/0590.
Counts describe one validated dictionary3 observation before kind/page filters;
unavailable null and successful empty zero counts stay distinct. Legacy asset
total parity, risk/security summaries, charts and full source acceptance remain
open. Candidate runtime CI and independent review are pending; product0/209.
