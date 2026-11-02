# Synthetic detection exceptions (AD-F-103–110 / Issue #253)

## Scope and evidence

This is an offline engineering slice based on the explicit synthetic contract
`adtr.detection-exceptions.simulated.v1`, provenance
`synthetic-business-contract-2026-11-01`. It implements rule-bound and
same-explicit-tenant/domain global exception snapshots, not verified legacy
behavior, persistent server endpoints, production RBAC or product acceptance.
The catalog maps 103/108 to listing, 104/109 to creation, 105/110 to editing and
removal, 106 to event preview and 107 to transfer. Removal is reversible-data
preserving archival here; restoration, paginated/filter UI, database integration,
real AD collection and end-to-end product acceptance remain open.

## Core API

Package `internal/detectionexceptions` uses `detectionrules.Scope`, `Condition`,
`Event`, and `Match` aliases so rules and exception evaluation share evidence.

- `New(scope)` creates revision 1, no entries. Scope is explicit, never inferred.
- `List(access, snapshot)` returns a deep copy including disabled/archived entries.
- `Add(access, snapshot, expectedRevision, entry)` creates a disabled draft.
- `Edit(access, snapshot, expectedRevision, entry)` explicitly replaces an existing
  nonarchived entry, including enable/disable. ID is immutable by lookup.
- `Archive(access, snapshot, expectedRevision, id)` preserves the entry and reason,
  marking it archived and disabled. Archived entries cannot be edited.
- `Import(access, snapshot, expectedRevision, entries)` atomically appends disabled
  unarchived entries. Existing or duplicate IDs fail the complete operation.
- `Export(access, snapshot)` produces deterministic JSON in snapshot entry order.
  Export preserves all state. An exported enabled/archived entry cannot silently
  become active through import: explicitly prepare new disabled/unarchived drafts
  and noncolliding IDs before importing. Export/import is not snapshot restore.
- `PreviewFromEvent(access, snapshot, event, id, reason, ruleID, ruleDigest,
  notBefore, expiresAt)` returns a disabled, rule-bound draft whose sorted exact
  conditions copy event fields. It never writes or grants global scope.
- `Evaluate(ctx, access, snapshot, events, matches, asOf)` returns each original
  `Match` unchanged and in input order, with zero or more simulated annotations
  containing exception ID and reason. No match is deleted or suppressed; no
  snapshot or event is mutated. Match authenticity is the upstream evaluator's
  responsibility; this pure package validates shape/scope/event references but
  has neither source rules nor an authenticated provenance attestation.

A mutation returns a new deep-copied snapshot and increments revision once.
Errors return a zero snapshot/nil results, not partial success. The caller owns
serialization and persistence; revision comparison on an in-memory value is not
an atomic database CAS or cross-process concurrency control. Inputs must not be
concurrently mutated by callers. There is no hidden mutable package state.

## Required controls and consequences

`Access.Read` is required for every operation; mutations also need `Write` and an
exact caller scope match. These are synthetic test inputs, not trusted server
credentials. Production callers must derive authorization from authenticated
current permissions, never accept these booleans from request bodies. Removing
these checks would permit cross-scope or unprivileged fixture operations.

Every entry needs an explicit same-snapshot scope, nonempty reason, start and
expiry, one to seven unique allowlisted fields, and exact `eq` or `in` comparisons.
Fields are `event.code`, `event.category`, `event.outcome`, `actor.name`,
`target.name`, `source.ip`, `object.kind`. Conditions are combined with AND;
`in` values are OR. Comparison is case-sensitive string equality; no regex,
substring, wildcard, CIDR parsing or fuzzy matching. Missing fields never match.
The detection-rule validator enforces value counts (32 maximum) and string
bounds (512 bytes per value); IDs/scopes are bounded to 128 bytes, reasons to 512.

A rule-bound entry requires both rule ID and lowercase SHA-256 digest. Digest
changes stop annotation until an explicit edit updates the binding. `Global=true`
requires empty rule ID/digest and still only applies in the exact snapshot scope;
it is not cross-tenant/domain. Keeping global scope explicit prevents accidental
broadening when a rule binding is omitted. Broad exact values can still annotate
many matches, so global enabling needs review before any production integration.

`Enabled` defaults false; add/import reject true, requiring a separate explicit
edit to activate. `Archived` always excludes an entry. `NotBefore`, `ExpiresAt`
and `asOf` are required: active time is `[NotBefore, ExpiresAt)`. No system clock
is consulted. Expiry excludes annotations without deleting historical entries.
Omitting temporal bounds would allow indefinite exceptions and nondeterministic
replay; explicit as-of means callers must supply a trustworthy observation time.

Limits: 128 stored entries including archived entries; 1,000 events; 10,000 input
matches and 10,000 resulting annotations. Archived entries consume capacity as
an intentional preservation tradeoff. Malformed disabled or expired entries
still fail validation, rather than hiding invalid state. Cancellation and any
invalid event, scope, reference or limit failure discard all results.

## Verification

Synthetic tests cover immutable CRUD/archive, exact/in matching, missing fields,
rule-digest binding, same-scope global behavior, deterministic disabled preview
and export, atomic import, revision conflicts, deny-by-default permission and
scope checks, time boundaries and expiry, input validation, cancellation and
bounded all-or-nothing evaluation. They do not establish browser, PostgreSQL,
real AD, authorization-server or product acceptance results.

Run with the repository's Go 1.27.1 toolchain:

```sh
go test -race -cover ./internal/detectionexceptions
go vet ./internal/detectionexceptions
```
