# Synthetic learning configuration value contract

Refs [Issue #57](https://github.com/yunpiao/adtr/issues/57), AD-F-096.
This is offline configuration groundwork, not a learned model, training job,
legacy configuration importer, or completed AD-F-096 implementation. Product
acceptance stays **0/209**. No runtime consumer is connected to this package.

## Source evidence and deliberately unknown semantics

Issue #57 lists `ruleId`, `dataSource`, `machineTime`, and `machineFields`
(FLD-1348–1351), and list/status fields (FLD-1355–1374). UI-0759 shows a
1–30 numeric range. Those records do not establish duration units, fractional
precision, status meanings, valid learning fields, or authorization semantics.
The existing rule catalog PR #264 explicitly excludes learning implementation.
This package does not interpret those legacy fields or copy their names into an
apparently compatible wire format. Source mapping and full behavior remain open.

## Construction and output

`New(Input)` returns an immutable `Configuration` or an error wrapping
`ErrInvalid`. It does not trim, coerce, default, deduplicate or repair inputs.
There is no JSON input decoder: callers must explicitly construct the typed
engineering input. Floating-point durations cannot be passed to its integer
field without an explicit caller conversion; a future adapter must independently
reject fractional or unsupported legacy values, not silently truncate them.

All parameters are required for this engineering format:

- `RuleLabel`, `DataSourceLabel`, and `DomainLabel` describe synthetic fixtures.
  Each has 1–128 ASCII characters, starts alphanumeric, and thereafter permits
  alphanumerics, underscore, dot and hyphen. They are case-sensitive labels, not
  verified resource IDs or authorization. Missing labels fail construction.
- `WindowSeconds` explicitly expresses integer seconds from 1 through 2,592,000
  (30 × 24 × 60 × 60). This independently chosen synthetic workload bound is
  **not** a conversion of legacy `machineTime`, a default or a scheduled interval.
  It avoids ambiguous units and unbounded future inputs. Removing the bound
  would remove that validation guarantee; it does not itself limit any executor.
  The bound excludes longer experimental windows, which require a new contract.
- `AllowedFields` is a nonempty caller-declared vocabulary of at most 256 unique
  field names. It is not an allowlist granting data access. Each name has 1–64
  ASCII characters, begins with a lowercase letter, and otherwise contains only
  lowercase letters, digits, underscore or dot. Names are opaque tokens, not
  expressions, paths to resolve, or code. No wildcard or case normalization exists.
- `SelectedFields` is a nonempty set of at most 32 unique members of that exact
  vocabulary. Unknown names fail even when another name differs only in case.
  Both count bounds cap validation and canonical output size; removing them
  permits larger allocations. They deliberately reject larger vocabularies and
  selections; no legacy capacity equivalence is claimed.

`CanonicalJSON()` returns freshly allocated bytes containing format
`synthetic-learning-config-v1`, `synthetic: true`, the three labels, explicit
seconds, and both complete field sets sorted in ASCII order. Every successful
output carries the synthetic marker. JSON field order is fixed by the versioned
snapshot definition. Reordering either input set does not change the value;
changing an unused vocabulary member does change it. Input slices and returned
byte slices cannot mutate a constructed configuration. The zero value rejects
both output operations, and failed construction returns that invalid zero value.
Concurrent reads of a constructed value are safe; callers must not concurrently
mutate the input slices while construction is reading them.

`Digest()` is lowercase hex SHA-256 of exactly those canonical JSON bytes. The
format version, synthetic marker, all labels, seconds, allowed vocabulary and
selection are all bound. It proves value equality only. It is not a signature,
authorization token, training completion receipt, or measure of model quality.
Exported configuration fields are deliberately unavailable; changes require a
new constructor call and therefore revalidation.

## Verification and remaining delivery gates

Tests assert exact canonical bytes and digest, set-order independence, every
input's digest binding, mutation isolation, concurrent reads under race checking,
zero-value rejection, all numeric/count/text boundaries, duplicate and unknown
fields, controls, invalid UTF-8, and case-sensitive vocabulary rejection.

There is no persistence, CRUD lifecycle, tenant/session authorization, domain
resolution, UI, API, scheduler, learned result, actual AD access, or external I/O.
Caller-provided vocabulary and fixture labels cannot replace live authorization
when a future consumer is built. PostgreSQL/browser/Windows/real learning evidence
is absent and is not claimed by unit tests. Independent source review, exact-head
remote CI, normal merge and new-main verification remain separate gates. Issue
#57 stays open until its complete requirements and real-chain evidence are met.
