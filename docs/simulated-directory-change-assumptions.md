# Synthetic directory change replay — pure-core contract

## Evidence and scope

[Issue #25, AD-F-035](https://github.com/yunpiao/adtr/issues/25) requires listening
for directory changes and updating assets, but lacks a frozen complete field and
behavior contract. These are **synthetic assumptions**, not recovered product
requirements or real AD evidence. `internal/directoryassets/objects.go` decodes
bounded observations; `docs/directory-api-contract.md` explicitly does not supply
incremental sync. An LDAP pagination cookie continues one search; it is **not**
this cross-sync cursor. No v1–v4 codec/hash, executor, reader, permission,
migration or configuration is changed by this isolated package.

Implemented: `internal/directorychanges` is a standalone pure in-memory algorithm.
It does not watch, schedule, connect, persist, authorize, or alter deployed assets.
AD-F-035 and product acceptance remain open. Test values are invented.

## Frozen API and assumptions

- `New(Baseline) (State, error)` accepts only Complete=true, a source epoch and
  zero to 64 distinct live objects at version 1. It fixes sequence 0 and a
  deterministic baseline hash. An incomplete baseline fails in full.
- `HashBatch(Batch) (string, error)` checks structural input and hashes canonical
  content; it does not check against State. BatchHash is excluded from hashing.
- `State.Apply(Batch) (State, []Record, error)` returns a new immutable state and
  new records. Every error returns zero State and nil records: the receiver and
  cursor remain unchanged. Callers must retain the old state on error, not
  replace it with the invalid zero result.
- `State.Snapshot()` returns detached ID-ordered objects (including tombstones),
  cursor and sequence/ID-ordered records. Record Before is nil only for create;
  Before/After retain complete typed field-change evidence including unchanged
  fields, identity, kind, version and logical deletion status. Old versions
  remain in these records; they are never physically removed.
- Zero State is invalid for Apply. Internals are unexported. State copies may
  share read-only storage; returned snapshots/records and input slices never
  alias mutable internal storage.
- IDs and epochs are exact lowercase ASCII `[a-z0-9_-]{1,64}` tokens. These are
  stable synthetic IDs, not AD GUIDs, USNs or invocation IDs. There is no trim,
  case folding, locale or Unicode normalization.
- Kinds are exactly user/group/computer and immutable across updates. Scalars
  are `Name string` and `Enabled bool`. Name may be empty; it must be valid UTF-8,
  at most 256 bytes and without Unicode control characters. Empty/false are
  known values, not null/missing. Enabled is not derived from AD UAC.

## Transitions, replay and failure

Canonical batch identity is (SourceEpoch, Sequence). Only the baseline epoch is
accepted; epoch reset requires an explicitly separate New baseline. A new batch
must have sequence current+1 and PreviousHash equal to the cursor hash. Sequences
start at 1. Gaps, out-of-order inputs, unknown old sequences, incorrect hashes and
wrong epochs fail in full. Validation happens before sorting/hashing payloads.

At most one change per ID is allowed per batch, regardless of operation. Create
uses ExpectedVersion=0 and requires a never-seen ID. Update supplies the entire
scalar payload (not a patch) and requires the live object's exact version/kind.
Tombstone requires the exact live version/kind and an empty Fields envelope to
reject ignored payloads. It preserves previous scalar values and marks Deleted.
Update/tombstone increment version by one. Deleted IDs cannot be updated,
recreated or tombstoned again. No resurrection or physical deletion is modeled.

Exact canonical replay returns the **current** state and no new records, even
after subsequent batches. The same identity with changed valid content and a
recomputed hash conflicts. Supplied hash tampering is a hash error. Input order
permutations are identical content; duplicate IDs are not. All accepted receipt
hashes are retained in the bounded state.

Complete empty batches advance sequence/hash without object changes. Incomplete
batches are rejected even when empty. Missing objects never imply deletion;
unmentioned objects remain unchanged.

## Required budgets and cancellation

Constants are not deployment settings: 64 distinct lifetime object IDs including
tombstones; 32 accepted batches; 32 changes per batch; 256 UTF-8 bytes per Name;
64 bytes per ID/epoch; lowercase 64-hex hashes. All are required for this finite
model. Removing them permits unbounded work/history or changes the encoding
contract. Their side effect is fail-whole rejection, never truncation or eviction.
Use a smaller valid fixture or explicitly separate baseline; there is no silent
reset. Exact replay succeeds at the batch budget because it adds no new record.
At most 1024 change records are retained; versions cannot overflow under the
budgets. Names/IDs are length-checked before character scans/sort/hash.

No context API is necessary for finite synchronous work: there is no I/O or wait.
Future wrappers must independently handle cancellation and transaction boundaries.
Callers must not concurrently mutate an input slice during a call; post-call
mutation of inputs or returned data is safe. Concurrent read-only State operations
are safe because existing internal storage is never mutated.

## Deterministic hash format v1

SHA-256 lowercase hex over Go encoding/json.Marshal compact UTF-8 bytes, without
trailing newline. JSON keys use the exact declaration order below, including
zero/false/empty fields (no omitempty). Go JSON escaping, including HTML escaping
of `<`, `>`, `&`, U+2028/U+2029, is part of v1. Arrays sort by exact ASCII ID;
empty input arrays encode as [] even for nil input. Duplicate IDs fail.

Baseline envelope:

```text
{"Format":"adtr.synthetic.directorychanges.baseline.v1","SourceEpoch":E,"Objects":[O...]}
O = {"ID":I,"Kind":K,"Version":1,"Fields":{"Name":N,"Enabled":B},"Deleted":false}
```

Batch envelope (BatchHash excluded, Complete always true):

```text
{"Format":"adtr.synthetic.directorychanges.batch.v1","SourceEpoch":E,"Sequence":S,"PreviousHash":H,"Complete":true,"Changes":[C...]}
C = {"ID":I,"Operation":OP,"Kind":K,"ExpectedVersion":V,"Fields":{"Name":N,"Enabled":B}}
```

Epoch, sequence, previous hash and every change field bind identity to content.
Hashes check content consistency, **not authenticity or authorization**. No
caller Scope grants permission. Schema changes need a new format version; these
hashes are not compatible with directory v1–v4 observation hashes.

## Integration deliberately not implemented

Persistence, transaction/crash recovery, server-determined owner/tenant/domain
and epoch binding, operation receipts, authorization, HTTP/Worker/UI wiring are
separate future work. In-memory batch receipts do not replace operation receipts.
No ACL, DNS mapping, downstream cascade, watcher, scheduler, credentials, real
LDAP/AD, Windows compatibility, production deployment or end-to-end acceptance
is claimed. Unit/race/vet evidence must be reported separately from these missing
integration and product acceptance gates.

## Local verification (2026-11-03)

Base commit: `a2688fda8724c4af65cb0f95dbd7687f3d469ba6`; Go 1.27.1 linux/amd64.
Baseline `go test ./internal/directoryassets` passed before implementation.
Final focused commands passed:

```sh
go test -race -count=1 ./internal/directorychanges ./internal/directoryassets
go vet ./internal/directorychanges ./internal/directoryassets
go test -cover ./internal/directorychanges  # initial focused result: 96.3% statements
git diff --check
```

External-package tests use handwritten two-batch final projection and history,
literal JSON hash oracles, complete/empty/incomplete cases, duplicate/version/
epoch/hash/sequence failures, exact replay and changed-content conflict, lifetime
budgets, whole-batch atomicity, detached inputs/outputs and concurrent immutable
branches. An initial static review found malformed IDs were sorted
before length validation; this was corrected before the final rerun.
Full repository make check, PostgreSQL, browser, LDAP/Windows and remote CI were
not run for this isolated uncommitted core. No publication or integration is
claimed by the focused checks.


## Final standalone main-based verification

The independent core is based directly on main
`80969671fa383ea9ddcfaa0e0ea758209c9596eb`, not on pending service wiring.
The entire diff is this document and the two new pure Go files. It contains no
authorization, migration, runtime/API/UI or permission-definition changes.
Production implementation SHA-256 remains
`93fc4e0eb2fcdef24674f1a262a4087abf016ea1b6fc1f358ceacf4d60c5c6d8`.

Additional independent literal escaping, nil/empty, malformed input and uint64-max
regressions passed with focused race/vet; package coverage is now99.3%. Full
repository Go race/vet and the canonical Python contract aggregate passed on the
main-based candidate (1202 tests,151.920 seconds). Requirements integrity remains
a separate209-record check. No browser, PostgreSQL or real directory integration
is implemented or claimed, and full make check was not invoked. Remote current-head
CI and normal merge/postmerge checks remain required after publication.
