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

Current product acceptance remains 0/209. Authentication work has actual layered
CI evidence, but source-system 1:1 and production gates have not been substituted
with mock, compilation or health checks. Priority never removes a requirement.

`python3 scripts/verify_requirements.py` is part of `make check`; it fails on scope
reduction, duplicate requirement IDs, missing field/control references or an
acceptance claim with unresolved gates. Source definitions remain in the linked
Issues; no omitted field is silently treated as implemented.
