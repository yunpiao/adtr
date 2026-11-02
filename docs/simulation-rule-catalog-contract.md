# Synthetic rule lifecycle/editor — Issues #36 and #40

This is an explicitly simulated engineering contract, not original product acceptance.
Learning (#57 / AD-F-096), real AD efficacy, Windows compatibility and production
capacity remain unsupported/unverified. No training or blocking flags are stored as
if they were executable features. Product acceptance remains 0/209.

## State and identities

`internal/simulationrulecatalog` wraps the existing `rulecatalog.Execute` authority.
Each tenant/domain/rule has an independently versioned one-entry core snapshot. A
create initializes core revision 1 then creates the draft at revision **2**; clients
must use the returned revision, never assume the next number. Draft version starts
at 1 and editing advances it. Successful tests save both positive and negative
fixture expectations and actual evaluator results; a legitimate failed test is a
persisted failed report, while invalid input has no state change. Only a passing,
unchanged candidate may be published. Published versions remain immutable.

Actor and scope come from authenticated server context, never client snapshots.
Publishing requires current catalog read plus the independent publish permission;
it deliberately does not require draft-write permission. The reviewer must differ
from the last draft editor. This two-person requirement is a documented synthetic
policy, not asserted source-system parity. Test and publish record the authenticated
tester, editor, reviewer and server timestamps separately. Existing application
session/MFA enforcement still applies; this module does not grant or bypass it.

Enable selects the newest published version; edit/test/new publication never
silently changes an already active version. Disable clears that pointer. Archive
clears it and permanently prevents further catalog edits while preserving all data;
there is no unarchive or physical-delete API. A draft/test/published/active status
must be presented separately because these are different states.

## Persistent transaction and recovery

The explicit migration owner installs schema29 (when assigned): heads, immutable
full revisions (including actual test evidence), authenticated publications,
operation receipts and copy origins. The module never runs DDL on startup. A
publication's test revision has a composite scope/rule/revision foreign key. Copy
origins reference both the immutable source publication and target template version.
Immutable rows reject UPDATE/DELETE/TRUNCATE; heads only advance one revision.

All writes require caller-owned PostgreSQL transactions and a nonnil audit callback;
any error, including audit failure, requires rollback. Scope advisory locking and
exact expectedRevision prevent lost updates; receipt and state/audit commit together.
The operation key is tenant/domain/actor/operationId. Same normalized input returns
the original immutable receipt (with requestHash); changed input conflicts. Each
receipt also carries a fixed-shape typed `intent` containing domain, rule, action,
operation ID, expected revision, declaration/fixtures and every copy source/target
field. The server hashes this descriptor and revalidates it on receipt reads.
Browser recovery compares the entire descriptor semantically against its retained
request, not Go/JavaScript JSON key order or merely a hash-shaped string. An old
receipt describes that operation, not the current head. Read current detail to know
whether a historical enabled state remains active. A timed-out client must resolve
or retry the original operation and content, not invent a new operation ID.

Catalog read/write, independent publish and existing detection permissions default
to denied, including administrators. The auth layer must revalidate identity,
function and current same-domain access before calling the store; Scope is an
internal verified context, not an HTTP DTO. Writes preserve Origin/CSRF checks and
actor-bound responses. API feature flags gate availability, never create grants.

## Explicit copy bridge, NOT a live binding

`CopyTemplateTx` requires catalog read/write AND existing detection read/write,
current same-domain authority, exact current revision/publication version/digest,
active non-archived source, a NEW target ID and the exact acknowledgement
`independent-template-copy-no-cascade`. The UI must show what this means before the
user confirms: **later catalog changes/disable/archive do not change existing
copied templates, queued tasks or schedules**. Use their existing authorized
workspaces to manage those independent objects.

The transaction locks catalog then detection, verifies the durable active pointer,
and derives Enabled=true only in a private execution copy. It rewrites Rule.Version
to 1 to match a new existing template's version convention. Published bytes remain
Enabled=false with their original version/digest. Source publication identity/hash,
catalog revision and resulting template ID/version/hash are retained together in
immutable origin evidence. Different source and target hashes are expected because
execution/version metadata differ. Newness is checked under the detection lock;
retry uses the original catalog receipt and cannot create a second template.

The bridge invokes existing `simulationdetection.MutateTx`, including its audit
callback, inside the same transaction. No task payload, manual receipt guard,
scheduled occurrence intent, authorization epoch, fencing, lease or cancellation
rule changes. Normal manual runs pin the copied template and synthetic dataset;
existing scheduled runs retain their stricter current-template validation. No new
worker, scheduler or background loop is introduced for rule management.

## Bounds and parameters

32 retained catalog rules per domain (archives count), 64 revisions per rule,
8 published versions (existing core), 2–8 test fixtures and existing declarative
field/operator/condition limits. Ordinary mutations may advance only through
revision62; revision63 is reserved for disable/archive and revision64 only for
archive. Repeated no-op disable cannot consume reserved revisions. This ensures
an active rule at the last ordinary revision can always be disabled then archived;
re-enabling/editing/testing/publishing cannot escape the cap. History remains intact.
Limits fail explicitly; history is never deleted to make space. List uses bounded
metadata only, at most 32 per page, with literal ID substring search; detail loads
and validates only the requested rule. A large rule cannot poison the list page.

Record bodies are at most 256 KiB and response/export/receipt envelopes 512 KiB.
Writes preflight both compact JSON and PostgreSQL JSONB separator expansion BEFORE
business DML; aggregate publication export is checked before publishing. This
prevents accepting a record which the documented readers cannot read. Budgets are
required defensive resource bounds, not throughput guarantees; removing them
increases storage/CPU exposure. Core history is re-evaluated when detail is read,
so cancellation and request timeouts remain necessary. Failure leaves no partial
state when the caller correctly rolls back.

The proposed `ADTR_SIMULATION_RULE_CATALOG_ENABLED` defaults false, is required
only for the new API/UI, and does not gate old detection tasks. Its removal loses
independent disable control. Copy additionally requires existing detection feature
availability; activation does not configure deployment, create grants or seed data.

Invalid input means fix declaration/fixture/acknowledgement (400); revision conflict
means another committed state or a reused operation ID disagrees (409), so reload
and reconcile rather than blindly overwrite. Invalid transition means the current
state lacks a draft/test/publication or is archived (409); show the missing step.
Budget excess means the retained/request/response bound was reached (422), not a
truncated success. Missing scope/object and authorization retain existing 404/403
policy. DB/schema errors never become empty successful data or passing tests.

## Requirement mapping and evidence gates

AD-F-086..090/093: bounded synthetic catalog list/edit/test/activation/JSON export
subset; delete is retention-only archive, no complete legacy field/export parity.
AD-F-091/092/094: only generic declarative lifecycle overlap; full alert/behavior
classes, multi-domain config and blockEnable/blockDelay execution remain absent.
AD-F-097..102: structured UI declaration editing/generation, real compile/evaluate,
persistence/publication and explicit active-copy eligibility; not arbitrary code,
SQL, regex or full original rule library. AD-F-096 learning remains unsupported.

Backend unit/race tests exercise state transitions, test failure, distinct reviewer,
published-byte retention, cancellation, default deny and JSONB envelope budgets.
Tagged PostgreSQL tests define persistent lifecycle, CAS competition, audit rollback,
scope denial, immutable DML rejection, copy replay and a REAL existing Worker with
a positive event producing exactly one expected rule/event match and a negative
event producing none, even after the original catalog rule is disabled. The
original task operation replay must return the same succeeded task. Compilation of
these tagged tests is not their execution. Missing ADTR_TEST_DATABASE_URL is a
failure, not a skip. API/browser, exact head CI, independent review and post-merge
main verification remain separate required evidence; consult the actual delivery
record rather than treating this contract as proof they passed.

### Manual dataset mapping capability
The existing manual mapper v1 supplies exactly event.category, event.outcome,
actor.name, target.name and source.ip. Catalog tests/export can still use the core's
event.code and object.kind fields, but COPY rejects any declaration containing
those unavailable fields with `ErrUnsupportedCopy` (even when an `any` branch might
match another field). Record/list metadata exposes `copySupported` and
`copyUnsupportedFields` for the currently active publication. This conservative
boundary prevents a positively tested rule becoming an inert task through a missing
source field; it does not alter the existing mapper. Missing fields do not satisfy
negative comparisons. UI should show this concrete reason rather than an unexplained
disabled button. Test cases include that missing-field behavior and copy rejection.

### Copy receipt access
A copy receipt contains a full manual detection template. Reading it therefore
requires current detection read authority in addition to catalog read and domain
authority; revocation denies the entire receipt, and restoration permits it again.
Ordinary catalog receipts still require only catalog read. The API must also pass
DetectionRead=false when the detection feature is disabled, so retained receipts
do not bypass that availability boundary. Mutating copy replay retains the stricter
catalog write plus detection read/write requirements.
