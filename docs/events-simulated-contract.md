# Security events: simulated v1 engineering contract

## Evidence and scope

`internal/securityevents` is a deterministic, offline core based on an **invented,
versioned simulation contract**, not recovered production semantics. Its required
provenance is `Contract=adtr.securityevents.simulated.v1`, `Synthetic=true` and an
explicit fixture identifier. Every input event must carry the identical provenance;
query results, correlations, case views and returned audit records retain it.
A caller-provided label is not proof that a real-world event is synthetic: only
trusted isolated fixture producers may use this package. No production route,
collector, database migration, credential or remote response is introduced.

Concrete reusable slices:

- AD-F-050/051/084: exact actor/device/session association and activity filtering;
  no inferred identity resolution, asset joins or relationship visualization.
- AD-F-083/085: a synthetic repeated-login-failure window correlator; no validated
  AD detection rule, threat confidence or real Windows event parser.
- AD-F-111/113/130/132: deterministic filtered event pages and correlated evidence
  IDs; not complete threat lists, raw-log viewers or device inventory integration.
- AD-F-112/115/116: pure revision-checked mark/close state machine and returned
  transition audit values; not persistent case management.
- AD-F-114/117–120/131/133–137 remain outside this slice: no raw-log retention,
  attack graph, trend aggregation, export, dedicated details API, saved templates,
  retention settings or advanced query language. Catalog completion and product
  acceptance are unchanged.

## Normalization and bounds

`Normalize(scope, provenance, raw)` rejects the entire batch on any invalid row.
There are at most 4,096 events; an empty batch is valid. Tenant/domain come from a
trusted scope and every event must match; there is no cross-domain partial result.
IDs are unique within the batch. Actor, device and session are mandatory exact
identifiers: no case folding, whitespace trimming or identity guessing. Identifiers
are valid UTF-8, 1–128 bytes, with no controls or surrounding whitespace. Timestamps
are RFC3339Nano, years 2000–2100, and normalized to UTC; input length is at most 64.
IP input is at most 64 bytes, zones are forbidden, IPv4-mapped IPv6 is unmapped.
Kinds are exactly `login_failure`, `login_success`, `service_access`.

The dataset owns copies and returns copies. Sorting is chronological ascending,
then event ID ascending; input permutations produce identical results. The batch
limit is mandatory to bound CPU/memory; removing it allows unbounded work. It is
an engineering safety budget, not a production capacity assertion, and larger
batches are rejected rather than silently truncated.

## Query and correlation semantics

`Query` combines optional exact kind/actor/device/session/IP filters with AND.
Time bounds are inclusive `From`, exclusive `Until`; if both are supplied they
must be increasing. Missing bounds mean no bound. `Limit` is required, 1–4,096;
`Offset` is 0–4,096. `Total` counts all matches before pagination. Stable pagination
only applies to this immutable dataset, not changing external event streams.

`CorrelateFailures` accepts a required duration from one second through 24 hours
and threshold 2–4,096. These parameters define the simulated burst signal; omitting
or zeroing them fails rather than selecting an undocumented default. Shorter
windows or larger thresholds reduce matches. Failures group by exact
actor/device/session within the dataset's one tenant/domain. A group window starts
at its earliest unconsumed failure and spans `[start,start+window)`. All failures
inside that window are consumed, even if below threshold; the next remaining
failure anchors the next window. Events at the upper boundary enter a new window.
Successes do not reset a window. This is deliberately **not a sliding-window
algorithm** and can miss a burst spanning two anchored windows. The rule ID is
`synthetic.login_failure_burst.v1`; evidence IDs are deterministic and copied.

## Authorization and case transitions

`Scope` is a trusted-call-site adapter, not an HTTP authorization implementation.
All reads require explicit `Read` and exact tenant/domain. Creation/mutation also
requires `Manage`; no administrator bypass exists. A production adapter would have
to derive these grants afresh from authenticated server-side state, never JSON.
The CLI simulation's grants exercise denial behavior, not production authorization.

`NewCase` starts `open` at revision 1. `Apply` requires current expected revision,
actor, reason and a valid timestamp no earlier than the preceding transition.
Allowed actions on open cases are `mark_suspicious`, `mark_benign`, `unmark` and
`close`. Marking is an analyst annotation, not a detection verdict. Closing is
terminal and preserves the mark. A repeated no-op mark is a conflict; stale
retries also conflict, including repeated close. A failure returns the unchanged
case and no audit. Success returns a new case and audit including prior/new state,
prior/new mark, actor/reason, UTC time, revision and synthetic provenance.

Cases are standalone simulation values and do not assert that an event/detection
exists. A persistence integration must validate those relationships and atomically
compare/store revision and audit; this package's value semantics alone do not
prevent competing callers from saving stale values. No reopen, batch mutation,
MFA/session integration, durable audit or crash recovery is claimed.

## Verification

Baseline: the package did not exist (`go test ./internal/securityevents` returned
setup failure before implementation). Synthetic tests cover normal operation,
invalid input, exact bounds, cross-tenant/domain and absent-grant denials,
immutable copies, order-independent correlation, half-open windows, empty batches,
mark/close transitions, stale revisions, no-op conflicts and backward audit time.
Run from the repository with the recovered toolchain environment:

```sh
source ../adtr-recovered-toolchains/dev-env.sh
go test -race -cover ./internal/securityevents
go vet ./internal/securityevents
```

These are local core tests, not PostgreSQL/browser/real AD/Windows/production or
full-product acceptance evidence. Production adapters and full feature contracts
remain independent gates.
