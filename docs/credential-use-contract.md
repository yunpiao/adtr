# Explicit credential use: Stage B1 governance

Refs #17 (F03, AD-F-008–011), with F01/F47/F48 integration boundaries. This is a
new local authorization policy, not verified legacy ownership or radio semantics.
B1 implements durable use grants and governance controls. B2 adds the one fixed
connection-test consumer described in [account-reference-contract.md](account-reference-contract.md).

## Authority

The tenant owns the registered credential. Its uploader is audit provenance, not
an implicit permanent owner. Metadata CRUD never grants use. Every role, including
platform_admin, needs an explicit current allow row for the exact account/domain,
account credential revision and purpose. The only purpose in B1 is
`domain.connection_test`; it does not authorize installation, WinRM, SMB, arbitrary
LDAP queries, password changes, remote account operations or a scheduled task.

Only a live builtin platform_admin may issue/revoke grants, using fresh password
and unused TOTP proof and exact F47 membership for the account domain. Issuance
requires current tenant eligibility. Revocation and admin cleanup reads (`/accounts`,
`/grants`, `/mutation`) use persisted active scope even during expiry/overquota, so
existing authority can be reduced. Grant issuance, eligible-role listing and the
self-effective route retain live eligibility. The target is a role, so authorization applies to its current and
future members. The target role must already have domains/tasks read+write,
operation_accounts read and exact domain scope. Granting adds none of those rights.
Custom delegation is disabled. Admin self-grant is explicit, proof-bearing and
audited; builtin normal function grants never substitute for credential use.

The B1-only version returned `consumerEnabled:false`. With B2 installed, the
compiled capability is true; this does not imply a current grant, live tenant
eligibility, configured key, enabled probe, reachable destination or verified
credential. Effective authorization and source test eligibility remain separate.

## Frozen HTTP contract

Prefix `/api/credential-use`. New routes use current schema before authentication,
server-derived identity/tenant, no-store responses, strict singleton queries and
JSON without duplicate/unknown/null fields. Successful responses include
`X-ADTR-User-ID` from the authenticated actor; UI verifies its current profile.
Errors are `{error:code}` with non-success HTTP status. Query/body limits 32 KiB.

GET routes:
- `/accounts`: admin cleanup catalogue of accounts with at least one allowed grant.
  Optional singleton pageIdx (1..1000000, default1), pageSize (10/20/30/40/50,
  default20), keyword (literal case-insensitive domain/label substring, <=50 code
  points, no controls). Return `{page,List:Account[],exhausted,consumerEnabled:false}`.
  Apply persisted active exact scope before count/filter/page. This route remains
  available during tenant expiry or overquota solely for permission cleanup.
- `/grants?accountId=...`: admin-only, exact scope; returns `{account:Account,
  grants:Grant[],consumerEnabled:false}`. At most1000 roles/grants; excess returns
  422 result_too_large, never a truncated authorization view.
- `/roles?accountId=...`: same admin gate; returns `{roles:Role[],consumerEnabled:false}`.
  Roles include only current eligible targets, including explicitly scoped builtin
  admin; never viewer. Role is `{roleId,roleName,memberCount}`; roleId/name strings,
  memberCount a nonnegative integer. Current and future members are disclosed in UI.
- `/effective?accountId=...`: operation_accounts.readable plus current exact scope;
  returns `{accountId,domainId,purpose,explicitlyGranted,eligible,grantRevision,
  accountCredentialRevision,consumerEnabled:false}`. Missing grant uses revision"0";
  no other role's grant is returned. Eligibility includes normal consumer rights,
  current account revision and all tenant/domain constraints; it is not execution.
- `/mutation?idempotencyKey=...`: original actor's safe receipt, still admin/scoped.
  Account/role tombstones do not erase a committed original result. Returns
  `{receipt:Receipt,consumerEnabled:false}`; inaccessible receipt is404.

POST `/grant` and `/revoke` require exactly `accountId`, `roleId`, `purpose`,
`expectedAccountRevision`, `expectedCredentialRevision`, `expectedGrantRevision`,
`idempotencyKey`, `actorPassword`, `totpCode`. All IDs/revisions are strings. Existing
opaque account/role/key grammars apply; purpose is the exact supported value.
Account/credential revisions are positive canonical decimals; grant revision"0"
means absent only for grant, otherwise positive. No caller tenant, actor, admin flag,
credential value, target address or command is accepted. Origin, cookie, CSRF, rate
limits, identity/forced-change and fresh proof are mandatory.

Account is `{accountId,domainId,domain,label,revision,credentialRevision}`. Grant is
`{roleId,roleName,purpose,allowed,grantRevision,accountCredentialRevision,updatedAt}`.
Dates are UTC RFC3339. No credential, ciphertext, proof or secret hash is returned.

Receipt is `{result:"SUCCESS",operation:"grant"|"revoke",accountId,domainId,roleId,
purpose,grantRevision,accountCredentialRevision,allowed,replayed,accountDeleted,
currentGrantRevision,currentAllowed}`. POST returns this receipt directly plus
`consumerEnabled:false`. Grant revisions are globally monotonic decimal strings;
currentGrantRevision is"0" if the current row no longer exists. Revoke and regrant
cannot restore an old grant incarnation. Replays return the original outcome and
current status, never renew authority or consume proof a second time. Check current
actor/scope before recovery, canonical nonproof metadata fingerprint before replay,
and replay before obsolete CAS. A same key with different metadata is409 conflict.
No-op grant/revoke may return a durable receipt for unchanged current state, but
must not advance an effective grant revision or authorization epoch artificially.

Foreign/ungranted IDs are404 before revision comparisons. Stale authorized account,
credential or grant versions are409 revision_conflict. Invalid target/purpose is422
credential_use_target_unavailable or unsupported_credential_purpose. Admin/scope
failures retain existing safe error codes. Guard failures map to403
credential_use_governance_required, without raw PostgreSQL messages.

## Persistence and invariants

Migration12 creates use grants, immutable metadata-only mutation receipts and
protected audit events. Composite identity is tenant/domain/account/purpose/role.
Bind allow rows to the exact account credential revision. Builtin role references
use F47's nullable generated custom-role FK pattern; no writable builtin role row.
Use a noncycling global sequence for every effective grant change, including
regrant after revocation/deletion. Grant mutations atomically consume proof, update
state, store the receipt/audit and bump durable actor epochs (tenant-wide is safe).

Pair replacement or account deletion invalidates all existing allows atomically;
new supplied credentials never inherit old grants. Label-only edits retain them.
Existing persistent/unknown dependencies continue blocking replacement/deletion.
Grants alone are not an execution dependency. No plaintext read/decrypt is needed
for grant management, including revocation with the domain key unavailable.

Explicit grant/revoke requires compatible admin governance. Record automatic
revocation due to pair replacement/deletion and role deletion without erasing
history. SQL guards enforce identity/revision immutability, current allow binding,
tenant locking, no truncate, and epoch invalidation. Use existing try-lock/NOWAIT
patterns for nonconforming direct SQL to avoid row-lock/tenant-lock inversion.
Safe nonsecret request fingerprints can use SHA-256; proof is excluded entirely.
No grant or default allow is invented during migration.

## Old-binary and indirect delegation barrier

Application-only checks are insufficient. New trusted handlers set a fixed versioned
transaction-local protocol, authenticated actor ID/tenant and operation class only
after the existing proof succeeds, before business mutations. The protocol is not
client-configurable. SQL guards independently verify its exact version and current
same-tenant builtin admin eligibility plus exact affected-account domain scope.
A context does not grant credential use. No SECURITY DEFINER or production bypass.

Protect credential-enabled roles even when normal rights or scope are dormant:
- User creation/assignment/reactivation into a role with current allowed grants
- Function-permission management for such roles, including delete/reinsert APIs
- Role/resource bindings and membership changes in groups linked to such roles
- Role deletion/cascades and sensitive account authentication changes
- Tenant entitlement changes that can reactivate dormant allowed grants

For B1, these administrative role-management paths require compatible scoped admin
context; do not attempt fragile final-state expansion inference across multiple SQL
statements. Unrelated profile/label fields and authentication/epoch bookkeeping stay
available. User removal/disable may remain a clearly separate reducing operation.

Password reset already requires builtin admin and MFA; there is no current custom
reset bypass. A reset of a credential-enabled user must also carry compatible scoped
admin context. Legitimate self password/MFA operations use a distinct server-verified
self context after their existing credential checks, limited to the authenticated
user and their exact self-service fields. Self context cannot change roles, reactivate
a user, manage grants or satisfy an administrative guard. Forced/expired users must
still be able to change their own password under existing rules. Mixed sensitive
field updates cannot exploit the self exception.

Old handlers do not set the new protocol and therefore fail closed for protected
writes after migration. Existing partial component fixtures may accept set_config
without new tables because they model no credential-use authority. New governance
integration tests install the actual tables and triggers; no missing-table fallback
or fabricated migration version is allowed to bypass production protections.

## UI and evidence

Add an account-detail use-governance panel for admins, showing current grants,
eligible role/member count and explicit role-wide scope before confirmation/proof.
A self-permission view must distinguish a saved allow, current eligibility and the
still-disabled consumer. Keep mutation intent nonsecret and actor-bound for receipt
recovery after uncertain transport; no automatic replay with a new key. Revocation
must remain available when a role/account lost ordinary eligibility. Session/Back/
late response handling follows existing F03 rules and verifies actor-bound responses.

Tests must prove default deny including admin, explicit self-grant, exact purpose/
revision, target eligibility, role-wide policy, revoke/regrant/rotation epochs,
scoped receipts, failed audit/proof rollback, tenant and cross-role isolation,
concurrent mutation CAS, no key/decrypt/network use, and immutable audit controls.
Install real guards to test old-style writes, custom/foreign/disabled/forced/expired
contexts, missing domain scope, transaction-context cleanup, dormant grants and
self-service exceptions. Add an actual v11→12 migration test preserving records and
creating no grants, plus protected domain-scoped audit/export regression.

B2 remains separately reviewed: same-domain reference/custom/unconfigured source,
a separate account-reference task kind, family-level idempotency across both kinds,
exact source/grant/credential/policy pins, explicit use checks for every actor,
and durable reserved/opened/quiesced dependencies. Terminal or expired task state
alone cannot prove an executor has stopped using a secret. No F05 executor is implied.

## Reviewed governance refinements

- Take the tenant try-lock before even checking whether a grant exists, including
  user insertion and every permission mark; an AFTER epoch trigger is insufficient.
- Capture prewrite admin scope through installed BEFORE-statement guards, bound to
  protocol/actor/tenant and operation class, reset by each trusted context helper.
  Parent role/group guards capture affected relationships before cascades. This
  avoids losing authority halfway through existing delete/reinsert operations.
- A deferred final-state invariant requires persisted platform_admin membership
  for every domain with any remaining allowed grant, including custom-role grants.
  Revoke first before removing the final management scope; temporary replacement
  gaps inside one authorized transaction do not cause permanent lockout.
- ManageTenant is a distinct proof-bound class for `/resources/tenant/save`.
  Validate the live admin/MFA and persisted affected-domain scope plus proposed
  entitlement under existing validation; do not require old entitlement eligibility
  for renewal. Disabling/reducing entitlement remains possible. Old context-free
  renewal cannot revive dormant authority.
- Trigger-originated epoch bumps acquire actor rows in ID order with NOWAIT, not
  a blocking lock after holding grant/account rows. Explicit inversion tests verify
  bounded failure and complete rollback.
- Automatic pair/account revocation is derived from the actual parent transition,
  distinct from explicit admin grant/revoke. Preserve old revision on denied rows
  with identity-only FK and record the authenticated writer when available; otherwise
  use explicit system attribution, never the original grant creator.
- Committed receipt recovery precedes fresh-proof consumption, after current
  authorization and fingerprint validation. Audit/receipt role identities have no
  cascading live-role FK. Exact self-service field checks run before, or explicitly
  account for, the existing generated user authorization epoch.

## Validation state before publication

The inherited F02 baseline had 333 DOM tests. The integrated B1 tree passed
`make check` with 355 DOM tests, Go race/vet/build, TypeScript/production build,
five Python contracts and zero dependency audit findings. The input-control issue
found in independent review was fixed and passed focused race regression checks.
All-package integration-tag compilation and vet passed. These commands do not run
PostgreSQL-tagged tests or establish browser acceptance.

Real-migrator SQL guard and v11 upgrade suites are present, along with HTTP and
protected audit/XLSX regressions. An additional actual-Migrate shared-handler suite now exercises proof/context
wiring rather than only direct SQL or partial component fixtures; its construction
and assertions passed independent review. Direct Back/popstate and revision-conflict
regressions found and fixed stale role selection after a version conflict. After
that production UI change, all 357 DOM tests, TypeScript/build/format and focused
Go race checks passed. The real
credential-use browser suite is registered in the bounded CI matrix; no local
PostgreSQL/browser execution is claimed. B1 remains unaccepted until exact-head runtime evidence and product gates are satisfied.
