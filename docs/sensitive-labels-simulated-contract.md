# Sensitive labels: simulated contract v1

Scope: engineering slice for AD-F-045 (identify/mark), AD-F-046 (view labels),
AD-F-047 (change records), Issue #253. This package supplies deterministic local
computation over explicitly synthetic inputs. It is not AD classification,
permission evaluation, production acceptance, or an external alert sender.

## API and required inputs

`internal/sensitivelabels.Evaluate(Input)` returns a `Result`;
`Compare(before, after Input)` returns a `ChangeSet`. Both return zero output on
error; neither writes files, mutates input, accesses a network, nor persists state.

- `version` must be `adtr.simulated-sensitive-labels.v1`, and `simulated` must be
  true. The nested `snapshot` must independently satisfy groupgraph v1, including
  its synthetic marker, scope, observation identity, bounded nodes/edges, and
  explicit known-membership flags. These mandatory fields prevent accidental
  interpretation of unversioned or real-world input.
- `scope` must exactly equal the nested snapshot tenant/domain. Comparisons must
  remain within the same scope. Scope values are provenance, not authentication:
  any future server integration still requires independent current authorization.
- `policyId` is a required nonempty bounded identifier for caller-supplied policy.
  No group name, SID, DN, privilege, or default built-in group is guessed.
- `sensitiveGroupIds` is the explicit, duplicate-free list of known group IDs.
  An empty list means no group-derived label policy. An unknown/non-group ID is
  invalid, rather than silently treated as nonsensitive.
- `manualLabels` contains exact `(objectId,label)` assignments for existing
  objects, without duplicates. Labels are UTF-8, at most 256 bytes, nonempty,
  trimmed, and contain no control characters. Manual group labels do not inherit
  to members; only explicitly selected sensitive groups propagate `sensitive`.
- `complete` is an explicit synthetic coverage attestation. Omitting it means
  false, permitting positive evidence but preventing negative conclusions and
  change comparisons. Even true cannot override unknown membership or broken
  references. It does not attest actual AD coverage.
- `maxDepth` is required in 1–64 and `maxLabels` in 1–16384. They bound traversal
  and the total number of provenance records, respectively. Smaller values can
  reject otherwise valid input; removing/zeroing them fails validation. No result
  is silently truncated. Graph maxima are 4096 nodes and 16384 edges. Input manual
  labels are capped at 16384, sensitive groups at 4096; comparisons emit at most
  32768 provenance changes across two bounded observations.

## Identification and inspection

Each selected group receives `sensitive_group` provenance, distance 0. Its known
members receive `direct_membership` at distance 1, or `transitive_membership` at
shortest distance greater than 1. Cycles terminate through groupgraph's visited
set; alternative paths do not create duplicate provenance. Different selected
groups remain distinct evidence sources. Manual assignments use `manual`
provenance, distance 0, and no group ID. Outputs sort objects by literal ID and
provenance by label/source/group/distance, independent of input ordering.

An object with positive evidence is `labelled`; this does not mean all its labels
are known. With no positive evidence, it is `unlabelled_within_snapshot` only when
the observation is complete; otherwise it is `unknown`. No output says `safe`.
Any unknown group membership or broken member reference anywhere conservatively
makes the observation incomplete, even if not reachable from selected groups.
The output lists these unknown groups/references and retains scope, observation,
policy, version, and the synthetic marker. Broken targets are not invented as
objects. Unknowns require better synthetic evidence before claiming absence.

## Change records

Comparison recomputes both inputs rather than trusting serialized Result flags.
It requires both to be complete, different observation IDs, equal scope/policy,
identical selected sensitive groups (order-independent), and identical object
IDs/kinds. Coverage or object-kind changes return `ErrIncompatible`, requiring a
new explicit comparable baseline. Incomplete input returns `ErrIncomplete`, with
no partial additions or removals. This deliberately avoids false removals caused
by missing objects, missing memberships, or dangling references.

For each object, records describe exact provenance added or removed. A changed
shortest distance/source is removal of the old provenance and addition of the
new, not proof that the object ceased being sensitive. Manual label edits are
supported. Caller argument order defines before/after; no wall-clock ordering,
alert delivery, subscriptions, acknowledgement, persistence, or deduplication
across invocations is claimed. Records are returned in object order, removals
before additions, each in provenance order.

## Verification and remaining gates

`go test -race -cover ./internal/sensitivelabels` verifies direct/transitive and
manual evidence, cycle handling, explicit unknown states, incomplete comparison
rejection in both directions, deterministic output, scope and policy isolation,
coverage compatibility, validation, depth/label limits, and defensive ownership.
Real AD identity/privilege semantics, production data authorization, durable label
editing/audit, UI, real alert lifecycle and product acceptance remain open.
