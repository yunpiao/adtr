# Synthetic sensitive labels: PostgreSQL / API / UI contract

Engineering continuation of [#38](https://github.com/yunpiao/adtr/issues/38)
(AD-F-045/046/047) and [#253](https://github.com/yunpiao/adtr/issues/253).
This slice persists actual synthetic observations, rules, evaluations, historical
changes, operation receipts and **in-app-only simulated change alerts**. It does
not classify real AD privilege, connect to AD, dispatch notifications, implement
honeypot facts or cover Windows versions. Product acceptance remains 0/209.

## Explicit simulated business basis

The user authorized development using explicit simulated assumptions when the
original business basis is missing. These field limits, exact-name semantics,
manual actions and alert aggregation are engineering assumptions, not frozen
legacy semantics or source-system acceptance. Original F12 #25 / F21 #37 and
real-chain product gates remain open. Never close #38 solely from this slice.

`internal/simulationlabels` wraps `sensitivelabels.Evaluate` and `Compare`; it is
not a replacement algorithm. Trusted isolated fixture setup calls `SeedFixtureTx`
only. There is no arbitrary browser upload, runtime import, real directory-table
adapter, automatic production seeding, external notification or AD write.

Fixture version is `adtr.simulated-sensitive-assets.v1`, with explicit simulated
marker, producer ID, dataset ID, ordered observations, groupgraph contract/scope,
complete flag and one name/nullable description for every object ID. Names need
not be unique. Scope comes from trusted setup/live authorization, not a browser
label saying synthetic. Seed validates the complete dataset before insertion,
canonicalizes copies without mutating input, hashes all graph/metadata/coverage
facts, and refuses to overwrite an existing dataset. Reads revalidate hashes and
stored identities; identifiers and scope remain bound by composite constraints.
Observations are immutable and numbered 1..N with distinct observation IDs.

## Rules and actual evaluation

A rule has an ID, name, selected explicit sensitive-group IDs and exact manual
object/label assignments. Create revision1; update/archive/restore require exact
expectedRevision and append an immutable version. Archive is reversible, never
physical deletion. Only active rules accept update/archive/new evaluation;
restoring an archived rule creates a new active revision. Rule references must
be valid in the dataset's explicit first baseline. Evaluating another observation
rejects absent references; no substitute identities are invented.

Evaluation pins the requested current active rule revision/hash and observation
hash, calls the existing core with fixed server budgets, then saves the actual
result plus immutable provenance and actor/time. Historical evaluations remain
readable after rule changes/archive. A later rule never rewrites an old result.

Selected groups propagate `sensitive` through known direct/transitive membership.
Manual group labels do not propagate. Display states are `labelled`,
`unlabelled_within_snapshot`, or `unknown`, never safe. A positive label is not
proof all labels are known. Unknown group membership/broken references anywhere
conservatively invalidate completeness. Core validation and budget failures leave
no success record or partial alerts.

## Comparison and local alerts

Comparisons require two persisted evaluations of the same dataset and **same
policy revision/hash**, strictly increasing trusted observation sequence, and
all original core compatibility rules: complete observations, different IDs,
identical object-ID/kind coverage and selected sensitive groups. Incomplete data
returns `ErrIncomplete`; other incompatible bases return `ErrIncompatible`.
These are failed comparison requests, not evidence that there were no changes.
A rule edit is policy history, not an asset-change alert.

Each changed object receives one immutable in-app alert grouping its exact
provenance additions/removals. Alerts are persisted inside the comparison JSON,
not synthesized from a mutable current view. `delivery=in_app_only` and
`simulation=true` are explicit. Zero changes creates a durable comparison with an
empty alert array. Removed source/distance does not prove an object is no longer
sensitive if other evidence remains. Exact before/after evaluation pair is unique
across operation IDs; replay returns the existing comparison and does not emit
another alert. A new pair of separately evaluated records is a new comparison,
not deduplicated by timestamps or a guessed source event identity.

## Scope, transactions, receipts and historical truth

HTTP integration requires independent `simulation_labels` read/write grants,
current authenticated actor/session, current tenant/domain-resource authorization,
CSRF/client identity checks and the exact schema gate before authorization locks.
No existing admin, directory, task, events, risk or detection grant upgrades to
this permission. The store's Scope is a trusted server argument, not JSON input.
All methods deny nil transactions or missing read authorization; mutations also
require Manage. Actual access rechecks and transaction admission belong to the
HTTP adapter, which must use existing authorization locking contracts.

All mutations serialize a tenant/domain advisory transaction lock, validate CAS,
then persist result, application audit and receipt in the **same transaction**.
A nonnil AuditFunc is mandatory; it must insert audit using that transaction.
The caller MUST roll back on every returned error and commit only success.
An audit callback error must not be caught and followed by commit. Historical
rows reject UPDATE/DELETE/TRUNCATE; only a current policy revision pointer changes.
Explicit migration installs schema; startup performs no DDL and no grant writes.

Receipt key is tenant/domain/dataset/actor/operationID. Same key/canonical request
returns the original receipt; changing request/action conflicts. Recovery requires
fresh authorized same-actor reads and is not a secret bearer URL. Lost HTTP
responses are resolved by checking the original operation ID before retrying;
abort does not prove transaction rollback. A receipt may describe an old revision,
so the UI must separately refresh current policy state, never overwrite it with
historical receipt content. Comparison dedup can reference an older actor/time;
receipt actor/time describes the current request and is shown separately.

## Bounds and display mapping

Fixed server limits are mandatory safeguards, not caller-tunable defaults:
256 nodes,1024 edges,64 selected groups,256 manual labels,depth16,4096 emitted
label provenance records; 128 datasets/domain,128 observations/dataset,128
policies/dataset,1024 revisions/policy,4096 evaluations and comparisons/dataset,
16384 receipts/dataset. Exceeding limits rejects atomically, never evicts history.
Serialized writes also preflight existing reader limits: dataset4KiB,
observation1MiB, policy/request128KiB and evaluation/comparison/complete receipt8MiB.
This includes the receipt envelope and a conservative allowance for jsonb wire
separator spaces. Oversize returns ErrLimit **before business writes or audit**;
no reader cap is raised. Large legal escaped IDs can otherwise expand comparison
provenance beyond the declared serialized response budget. No claimed throughput derives from these caps. Their side effect is rejection of
larger inputs; raising/removing them requires budget/performance review, not a
client parameter. All module IDs, including graph node/edge/metadata and policy object/group
references, are max128 bytes to match the HTTP detail contract; the standalone
core retains its own broader limit. Overlong IDs reject without truncation. Names,
search and labels max256 bytes; reasons/descriptions max512; trimmed valid UTF-8
without control characters. Optional empty description is allowed; null remains
unknown. Reason is required for every mutation. List limit1..100 and offset0..10000
are explicit. Serialized list pages are capped at 8 MiB; larger pages reject with
ErrPageTooLarge (`simulation_page_too_large`) and may be requested with a smaller
limit (no silent truncation). Graph, quota, persisted-record and receipt limits
remain ErrLimit and must not trigger automatic page-size retries.
Search is case-sensitive literal substring of synthetic name;
empty name/kind means no filter. Kinds only user/group/computer. No regex or SQL
wildcard interpretation. Lists are deterministic within immutable result IDs.

AD-F-046 mapping: domain is current authorized domain; assetsType/assetType maps
only to these kinds; name comes from fixture metadata; labels are actual core
provenance. Asset detail `isClick=true` means this simulation detail is available,
not an AD permission. `isHoneypot=null`, `honeypotAvailability=unavailable`: no
source establishes that fact, and unknown is never silently false. `reason`
explains exact manual/group source and distance. Group names/descriptions resolve
from the pinned observation. Original boolean honeypot compatibility and other
unsupported fields remain unimplemented, not fake successful defaults.

## Verification status and gates

Baseline core/risk/events Go tests passed before implementation. New pure tests
cover canonical fixtures, validation, defensive ownership, explicit unknowns,
actual core comparison/provenance and compatibility, authorization and pages.
The initial defensive-copy test failed because json.Unmarshal reused an existing
slice backing array; reconstruction into a fresh Observation corrected the
aliasing, and the test passed on retry. Failure evidence must remain in delivery
notes, not be represented as first-attempt success.

`go test -tags integration ./internal/simulationlabels` defines an actual isolated
PostgreSQL ledger test, including CAS racing connections, same-op conflict,
comparison-pair dedup, audit rollback, restart durability, immutable triggers,
actor/domain isolation, historical receipts and incomplete comparison rejection.
It intentionally fails if ADTR_TEST_DATABASE_URL is absent; compiling it with
`-run '^$'` is **not** execution. Local environment currently has no configured
PostgreSQL test DSN or Docker; actual PG execution is blocked here until the
existing authorized CI environment runs it. No mock substitutes for PG evidence.

API/browser tests, integrated migration exact-version checks, independent review,
current-head CI, normal merge and new-main validation are separate gates handled
by integrator. This document does not claim those have passed. Existing task
budgets, allowlists, proof guards, TLS and original CI suites must stay unchanged.

Independent review additionally found two cross-layer gaps: core-accepted129-byte
object IDs could not be addressed by the128-byte HTTP detail contract, and legal
128-byte HTML-sensitive IDs could expand a comparison to25,623,103 bytes while
readers allow8MiB. The escaped specimen establishes Go/HTTP serialized overflow;
PostgreSQL jsonb may unescape HTML characters, so it is not evidence of observed
PG raw-wire overflow or permanent unreadability. Both were corrected in this new module without changing the
core: uniform128-byte IDs and preflight of every stored payload/request/receipt.
Regression definitions cover128 accepted/129 rejected seed→rule→detail, legal
escaped comparison rejection, envelope-only overflow, exact byte boundaries,
jsonb separator overhead, and PG checks for zero committed business/audit/receipt
rows. Pure regression tests pass; PG atomicity checks remain unexecuted locally.

Additional named real-PG regressions are
`TestPostgresLabelsLedger/ScopedCompositeFKRejectsTampering` (cross-domain,
policy-hash and observation-hash FK attacks must fail SQLSTATE23503 with no rows)
and `TestPostgresLabelsLedger/ConcurrentCreateAtFixedPolicyQuota` (127 existing
policies, two separate connections, exactly one128th creation and one ErrLimit;
policy/version/receipt/audit counts all128). Production quota is unchanged.
