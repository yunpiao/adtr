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

Current product acceptance remains 0/209: 19 verified slices, 28 in-progress
requirements and 162 with no implementation evidence recorded. AD-F-038/039
are now in progress for the narrow stored-v2 user search/detail slice; its
[contract](../docs/user-assets-v2-contract.md) keeps broader fields, filters,
relations, export and real AD acceptance open. This does not increase accepted
requirements or borrow earlier CI as evidence for new code. AD-F-151/152
previously changed to in_progress for the audit-only export history slice. Exact-head [PR #77 CI](https://github.com/yunpiao/adtr/actions/runs/37772558021)
passed all 25 jobs at `4f64d90880b28b3cf7a749a9ba1da57049736dd9`.
The catalog records narrow implemented scope and remaining gates; passing a shared
suite does not verify every source field. Original evidence is retained in
`previous_evidence` or supplemented by `latest_regression` where applicable.
Source-system compatibility, real AD/Windows and production gates remain open.
Priority never removes a requirement.

`python3 scripts/verify_requirements.py` is part of `make check`; it fails on scope
reduction, duplicate requirement IDs, missing field/control references or an
acceptance claim with unresolved gates. Source definitions remain in the linked
Issues; no omitted field is silently treated as implemented.
