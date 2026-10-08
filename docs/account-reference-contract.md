# Registered-account connection sources and safe task completion

Implementation contract for F01/F03 B2, 2026-10-07 UTC. Local implementation and test artifacts do not establish PostgreSQL/browser execution, legacy parity, real AD authorization or product acceptance. Follow delivery evidence separately.

References: Issues #16/#17/#18, domain, operation-account, credential-use and task contracts.

## 1. Frozen decisions

1. Keep `domain.connection_test`, payload version 1, custom envelope version 1, validator and meaning unchanged. Add `domain.account_connection_test`, payload version 1, using only F03 envelope version 2. Both share the existing fixed LDAP transport and one per-domain lease. The use-grant purpose remains exactly `domain.connection_test`.
2. F01 has exactly three sources: `custom`, `operation_account`, `unconfigured`. Only the first owns domain ciphertext. Only the second holds a same-tenant, same-domain account pointer. No copying, caching or plaintext-reading HTTP API.
3. Existing `domain_connections.credential_revision` is the **connection credential generation**, also the **binding generation**. It never means an F03 credential revision. Do not introduce a second independently mutable binding counter. New names use `connectionCredentialGeneration`; retain existing public `credentialRevision` aliases for old F01/F02 consumers and old custom payloads.
4. Account record revision is an admission/CAS input only. Account credential revision and explicit grant incarnation remain execution pins. A label-only edit cannot invalidate an otherwise authorized submitted account test.
5. A binding belongs to the connection, not its creator. Every test actor, including admin, requires its own role's current explicit allow row. A binding is neither a grant nor a delegation.
6. Add a separate `/api/domains/create-unconfigured` route. This avoids entering the same AD pair into F01 and F03 just to bootstrap. Existing `/create` remains custom-only. The source UI calls the new route only for the explicit unconfigured/bootstrap choice.
7. Terminal status is not quiescence. Persist reserved/opened/quiesced use evidence and preserve unresolved dependency blockers after expiry, revocation, detach, and process crashes. No force-release endpoint or automatic crash reconciliation in B2.
8. Account-reference metadata is not added to ordinary F01/F02/task/audit projections. Account pointer enrichment requires current `operation_accounts.readable` and exact domain access. Source mode, generation and configured/not-configured are safe domain metadata.
9. Do not assign meanings to legacy radio IDs, invent a legacy owner, or add F05, remote account, install, arbitrary LDAP, WinRM, SMB, scheduled execution, or AD mutation capabilities.

## 2. HTTP contracts

All new routes use the exact schema gate before authentication/business bookkeeping, current server-derived actor/tenant/role, existing origin/cookie/CSRF/rate/fresh-password-and-unused-TOTP rules, no-store, 32 KiB limits, strict singleton queries, exact JSON fields, duplicate/unknown/null rejection and existing opaque-ID grammars. Revisions are positive canonical int64 decimal strings. All successful new-route responses include `X-ADTR-User-ID`; the frontend checks it against its current profile. Input String/GoString/MarshalJSON must redact proof, username and password.

### Source mutation requests

POST `/api/domains/credential-source/reference` requires exactly:

```json
{
  "domainId": "domain-id",
  "expectedRevision": "7",
  "expectedConnectionCredentialGeneration": "3",
  "accountId": "account-id",
  "expectedAccountRevision": "4",
  "expectedAccountCredentialRevision": "2",
  "expectedGrantRevision": "19",
  "idempotencyKey": "reference-intent-key",
  "actorPassword": "write-only proof",
  "totpCode": "123456"
}
```

`expectedRevision` is the F01 connection/parent revision, consistent with existing F01 endpoints. `expectedConnectionCredentialGeneration` pins the old source/binding incarnation. The other three revisions identify the selected account record, its pair, and the actor's current role grant. The request cannot choose role, purpose, tenant, actor, source string, endpoint or credentials. Require domains read+write, operation_accounts read, exact domain scope, live tenant eligibility, current account/pair and current eligible explicit allow. The role comes from persisted authenticated identity. Grant eligibility already includes normal task/domain rights. Metadata-readable but ungranted or foreign/missing accounts all yield 404 before revealing CAS comparisons. A visible and currently granted stale version yields 409 `revision_conflict`.

POST `/api/domains/credential-source/custom` requires exactly:

```json
{
  "domainId": "domain-id",
  "expectedRevision": "7",
  "expectedConnectionCredentialGeneration": "3",
  "username": "SYNTHETIC\\reader",
  "password": "new supplied pair",
  "idempotencyKey": "custom-intent-key",
  "actorPassword": "write-only proof",
  "totpCode": "123456"
}
```

Require ordinary live F01 read+write/exact-scope authorization and an available vault, but neither operation-account read nor a use grant. Always accept a freshly supplied pair; never extract it from the old source. No account ID/revision is accepted. A same-source custom replacement is a real new generation, even if its unseen values happen to match.

POST `/api/domains/credential-source/detach` requires exactly:

```json
{
  "domainId": "domain-id",
  "expectedRevision": "7",
  "expectedConnectionCredentialGeneration": "3",
  "idempotencyKey": "detach-intent-key",
  "actorPassword": "write-only proof",
  "totpCode": "123456"
}
```

Detach changes either configured source to `unconfigured`. Require current authenticated identity, domains read+write, fresh proof and persisted exact membership of an active domain. Do not require account read/use grant, account key, LDAP enabled, or current tenant entitlement. This mirrors B1's explicit cleanup exception, narrowly limited to reducing the source. Detach never deletes an F03 account, revokes someone else's grants, cancels/quiesces an opened use, or releases task-use dependencies.

Same reference target already bound or detach already unconfigured returns 409 `no_change` for a new key. Exact committed replays return their receipt before current CAS/no-change checks.

### Source receipts and replay

GET `/api/domains/credential-source/mutation?idempotencyKey=...` returns `{ "receipt": SourceReceipt }`. Source POST success returns SourceReceipt directly. Exact public shape:

```json
{
  "result": "SUCCESS",
  "operation": "reference",
  "domainId": "domain-id",
  "revision": "8",
  "connectionCredentialGeneration": "4",
  "credentialSource": "operation_account",
  "replayed": false,
  "deleted": false,
  "currentRevision": "8",
  "currentConnectionCredentialGeneration": "4",
  "currentCredentialSource": "operation_account"
}
```

Operation is `reference|custom|detach`; source is the resulting source, so detach returns `unconfigured`. Unprefixed fields are immutable original outcome; current fields describe the current authorized connection/tombstone. No account pointer, account label/revision, grant role/revision, username, ciphertext, proof, fingerprint or task pins are returned. This deliberately permits safe recovery and cleanup after account permission is lost.

Receipts are keyed `(tenant, original_actor, idempotency_key)` across all three source operations, with operation and canonical request fingerprint retained. Include operation and all nonproof request/CAS fields. Custom fingerprint uses the existing vault's purpose-separated keyed fingerprint over the canonical pair+metadata; do not store a plain password hash. Reference/detach use a purpose-separated SHA-256 over nonsecret metadata. Proof never enters either fingerprint. Receipts and audit are immutable and do not cascade with grants/roles/account liveness.

Before replay, enforce current identity, F01 write permission and persisted active exact domain scope; a reference POST replay additionally requires account metadata read before interpreting its supplied pointer. It does not require the now-revoked use grant because returning a committed receipt is not fresh use. GET receipt recovery does not expose the pointer and needs no account read. Match original actor/tenant/key and fingerprint, then return original outcome before fresh proof consumption, old CAS, or use-grant checks. A changed body/operation under the same key is 409 `idempotency_conflict`. The custom POST needs the fingerprint key; key-unavailable users recover with GET, which needs no key or pair. Never auto-create a replacement key following an uncertain response.

All new writes atomically consume proof, update source/pair/dependencies/counters, clear latest observation, invalidate epochs, insert receipt and immutable audit. Roll back everything if any guard, proof, receipt or audit step fails. There is no network work in a source mutation.

### Source detail and visibility

GET `/api/domains/credential-source?domainId=...` returns:

```json
{
  "domainId": "domain-id",
  "revision": "8",
  "connectionCredentialGeneration": "4",
  "credentialSource": "operation_account",
  "credentialConfigured": true,
  "testEligible": false,
  "reference": null
}
```

Base gate: current identity, domains readable, persisted active exact scope. This safe projection works during entitlement lapse for detach preparation. `reference` remains null unless source=operation_account AND actor has current normal live account-metadata read authorization in that exact domain. It is not populated merely because the actor knows or previously bound the ID. When authorized it is exactly:

```json
{
  "accountId": "account-id",
  "label": "Synthetic reader",
  "accountRevision": "4",
  "accountCredentialRevision": "2",
  "purpose": "domain.connection_test",
  "explicitlyGranted": true,
  "eligible": true,
  "grantRevision": "19"
}
```

This is the actor's effective grant only; absent grantRevision is string `"0"`. `testEligible` is a current snapshot, not an authorization token: require all ordinary test permissions, live tenant/domain identity, configured exact source, deployment enablement/target policy and, for reference, explicit current account use. No pointer is returned to a caller lacking account read. SQL/database failures are errors, not a fabricated false result.

Ordinary domain Connection responses may add `credentialSource` and `connectionCredentialGeneration`; retain `credentialRevision` with the identical connection-generation value. They do not add account/grant details. F02 retains its existing response shape and source=`configured_connection`, with source-aware credentialConfigured calculation. No reference pointer or inferred remote credential validity appears there.

### Create unconfigured / old route compatibility

POST `/api/domains/create-unconfigured` requires `domain`, `dcHostName`, `port`, `idempotencyKey`, `actorPassword`, `totpCode`; only `ldapAddr` is optional, matching F01 normalization. It forbids username, password, account, caller IDs and source fields. Same admin/enrollment/capacity/uniqueness checks and endpoint validation as `/create`; no vault or LDAP adapter needed. Create active local domain/catalogue identity with revision=1, connection generation=1, diagnostic generation=1, source=unconfigured, no pair and no binding dependency. Source remains unverified, configured=false.

Use existing creation receipt/key space and `/creation` recovery: add an internal creation-operation discriminator/default `custom` for old receipts; purpose-separate `create-unconfigured` fingerprints and conflict with custom `/create` reuse. Return the existing Created/Receipt public shape unchanged, including requiresResourceAssignment=true. Preserve original `/create` custom request, ciphertext, fingerprint semantics and responses. A bootstrap follows create-unconfigured → explicit F47 resource assignment → F03 create using the current expectedDomainRevision → explicit B1 grant → reference mutation using freshly read parent/account/grant versions → explicit F01 test. No step implies the next authorization or silently performs it.

Existing `/update`: endpoint-only edits remain valid for every source and preserve its generation/binding/pair. A credential-bearing legacy `/update` succeeds only when current source is custom, with the old request/response meaning; return 409 `credential_source_changed` for operation_account/unconfigured, requiring explicit new custom-source route. Do not silently turn reference/unconfigured into custom. Existing custom rows/tasks need no grant. Existing `/delete` remains blocked by live F03/domain dependencies; do not broaden its old custom-task exception to the new kind.

Existing `/test` body is unchanged: domainId, expectedRevision, idempotencyKey, actorPassword, totpCode. Server selects the source/kind and constructs pins. Source=unconfigured returns 409 `credential_unconfigured` with zero task, dependency, pointer or transport effects.

## 3. Migration 13 and SQL invariants

Implement new migration constant, e.g. `domains.AccountReferenceSchema`, appended after B1; bump `schemaversion.Current` to 13 and rebuild the latest audit view. Do not rewrite historical Schema constants to pretend old installations contained B2. The real migrator is the sole DDL owner. Preserve all existing v1 custom tasks/payloads/hashes/epochs and diagnostic/creation history byte-for-byte, except adding defaults to connection metadata. No new account, grant, task use or binding is inferred from old data.

### Connection source

Expand existing credential_mode check to three values; default existing rows to custom. Add nullable `operation_account_id` and `operation_account_credential_revision`. Add deferred composite FK `(tenant_id,domain_id,operation_account_id,operation_account_credential_revision)` to the account's exact credential identity. For nonreference rows both pointer columns must be SQL NULL; reference requires both non-NULL and positive revision. New deletion clears to source=unconfigured, no pair/pointer/binding. Preserve historical deleted custom rows without rewriting their immutable tombstones: the deleted-row consistency branch permits legacy custom mode with no pair/pointer/binding and is evaluated before live-source requirements.

Deferred cross-table consistency must prove, for each affected parent at commit:

- Live custom: exactly one domain_credentials row matching connection generation; no pointer or typed F01 binding dependency
- Live reference: no domain_credentials row at any generation; live same-domain account and matching F03 credential row; exact pointer revision; exactly one matching typed binding dependency
- Live unconfigured or deleted: no domain pair, pointer or binding dependency
- Pair/binding child mutations cannot evade checks by mutating only the child; triggers are installed on all participating tables, not only domain_connections
- Existing unknown operation_account_dependencies remain blockers and are not reclassified, removed, or assigned invented provenance

Strengthen domain_connection_guard to include source/pointer changes, exact +1 generation and connection revision for every effective source switch, and diagnostic generation +1 plus latest=NULL. Existing endpoint-only changes advance connection/diagnostic revisions, not credential generation. Direct deletion/replacement of domain ciphertext at the same generation must be impossible. Guard current live pair/source correspondence even for direct SQL. Preserve durable identity, no revive, no truncate, immutable receipts and audit.

Take schema → tenant → actor where applicable before business locks. Existing task execution already locks task before connection. Trusted source/admission code holds tenant serialization and uses deterministic connection/account ID order; triggers use task_auth_lock try-lock and NOWAIT parent/actor-row patterns rather than waiting backwards. Task claim/recovery currently hold task rows without tenant authorization: do not embed resource cleanup or tenant acquisition into their generic terminal transitions. Reserved-only reconciliation runs in a separate transaction outside those locks. Never introduce blocking tenant acquisition while holding a task/account row acquired first.

Source mutations conservatively bump tenant actor epochs under existing deterministic NOWAIT rules. Grant changes retain B1 invalidation. Ordinary account label edits do not bump use epochs. Source fingerprints and receipts are immutable; source mode/pointer changes cannot be smuggled through endpoint-only or grant governance context.

### Dependency registry

Add nullable `account_credential_revision`, `connection_credential_generation` to operation_account_dependencies. Legacy/unknown rows keep NULL metadata and remain blockers. Reserve exactly two typed kinds:

- `domain.connection_binding`, object_id=domainId: matches that connection's current operation_account pointer, account pair and connection generation
- `domain.account_connection_test`, object_id=task UUID: matches one unresolved durable task-use ledger row

Typed rows require positive exact metadata, tenant/domain/account identity and no arbitrary reassignment. Known types cannot be inserted without their matching binding/ledger, nor removed while the binding/use still requires them. Deferred bidirectional checks permit the complete atomic source/use transition. Do not use a blanket cascade or terminal-state delete to release them. F03's existing account_in_use guard continues to reject pair replacement/deletion while ANY dependency exists. Label edits remain allowed.

### Task-use ledger

New table `domain_account_task_uses` retains at least:

- tenant_id, domain_id, task_id (one use per single-attempt task; task_id unique)
- account_id, account_credential_revision, connection_revision, connection_credential_generation, diagnostic_generation, policy_revision
- grant_role_id, grant_revision, original actor_id; exact purpose is fixed
- state=`reserved|opened|quiesced`, reserved_at, nullable opened_at/quiesced_at
- nullable opener_owner, opener_fencing_token, opener_attempt
- nullable quiescence_reason=`executor_returned|never_opened_terminal`

All admission/source/account/grant pins are immutable. Use identity-only account FK, not an enduring FK to the mutable current credential revision: quiesced history must survive later account replacement/deletion. Enforce exact current revision for reservation/opened states through deferred checks. Ledger task identity must match tenant/domain/kind/version/actor/payload; task_id-only FK is not enough. Record no account pair, ciphertext or result.

Allowed transitions: reserve INSERT; reserved→opened once; opened→quiesced with exact saved opener evidence; reserved→quiesced only when terminal task state proves no later authorized open. No backwards transition, opener replacement, delete or truncate. Reserved has no opener/opened time; opened has all opener fields/time and no quiesced time; quiesced preserves whether it ever opened. Duplicate exact acknowledgements are idempotent; mismatched owner/token/attempt cannot change any row.

Every unresolved reserved/opened row requires exactly its matching typed dependency; quiesced rows have none. Source detach/replacement removes only the binding dependency and leaves all task-use dependencies intact. Account replacement/deletion cannot proceed until all bindings and uses, including unknown dependency types, are absent. The existing live-account domain pin keeps domain deletion blocked until safe account deletion.

### Family idempotency

Add named partial unique index, e.g. `domain_test_family_idempotency`, on tasks `(tenant_id,domain_id,idempotency_key)` WHERE kind IN ('domain.connection_test','domain.account_connection_test'). Retain per-kind uniqueness. The scope does not include actor: another actor cannot take over a key.

The dedicated admission route checks the full family before inserting/selecting a new kind. Reuse across source/kind/current configuration or by another actor is 409 idempotency_conflict. Exact replay returns original task, original actor epoch, original source/account/grant/policy/generation pins after current read/route authorization; it never repins grants, reserves a new use, increments diagnostics, or restarts the old task. Check existing family intent before an obsolete config comparison so key reuse under another source cannot be silently interpreted as a new test. Named index violation maps to idempotency_conflict, not current domainHTTPError's broad unique→domain_conflict mapping.

## 4. Admission, use authorization and executor

The new account payload has exactly these nonsecret fields, all strings:

```json
{
  "credentialSource": "operation_account",
  "connectionRevision": "8",
  "connectionCredentialGeneration": "4",
  "accountId": "account-id",
  "accountCredentialRevision": "2",
  "grantRoleId": "persisted-role",
  "grantRevision": "19",
  "policyRevision": "deployment-policy-revision",
  "diagnosticGeneration": "6"
}
```

The connection generation is explicitly the binding generation. Account record revision is absent; it was only reference-selection CAS. Reject duplicate/unknown/null/missing keys, numeric JSON revisions, unsupported source and invalid IDs. Never accept this payload from HTTP task-submit. Payload remains hidden in Task JSON.

Admission, under existing schema/tenant/actor locks, locks current connection, selected account and exact actor-role grant in a deterministic order, checks all current rights/eligibility/pins and deployment target policy, and inserts task + reserved ledger + task dependency + latest pointer/next diagnostic generation + audit atomically. Account kind policy matches custom: version1, maxAttempts1, singleAttemptOnly=true, replaySafe=false, schedulable=false, ownerScoped=false, 30s lease, 1s heartbeat, 10s execution budget, no retry codes. No automatic retry or generic recovery.

`taskAuthorizer`: both kinds require ordinary domains/tasks rights and exact live domain scope. Account kind Execute additionally checks operation_accounts readable, persisted actor role and current live same-domain source/account/pair/explicit eligible grant. Builtin normal-function bypass must not skip explicit grant checks. Read/cancel remain safe metadata/control operations and need no live use grant. Write remains normal route authorization; dedicated admission does full secret authorization before task creation. Immutable payload matching belongs to executor checks, since current Authorizer receives Scope only.

Account executor's current-check verifies source, connection revision/generation, exact account/pair, latest task/diagnostic generation, current deployment policy, current persisted actor role and exact allowed grant incarnation before snapshot, each external phase and publication. Regrant with a new revision cannot revive old queued/running work. Account label edit alone is irrelevant. Old custom executor also explicitly rejects noncustom current source; it never falls back to an account pair, and account executor never falls back to custom.

Initial authorized `WithTx` atomically changes reserved→opened with stable lease owner/fence/attempt and obtains the exact sealed F03 snapshot. Do not expose/use the sealed snapshot unless this transaction committed successfully. Prefer validate/update opened evidence before copying the sealed row inside that callback; failure clears any local ciphertext. Decrypt only through OpenOperationCredential(tenant,domain,account,exact credential revision), validate exact v2 plaintext shape and credential bounds. Use existing fixed Probe and its authorization callbacks; no new target or operation is accepted from account metadata. All secret buffers owned by the executor are cleared on every return/panic path. Avoid retaining immutable string copies longer than required by Go's existing JSON/LDAP APIs; do not claim cryptographic erasure of unowned runtime copies.

Do not hold a database transaction through LDAP. Probe must guarantee transport close and joining its owned goroutines before return. Record phase errors safely. After Probe returns, clear owned pair buffers before publication locks; publication still needs current grant/epoch/source/lease checks and exact latest pins. Audit/diagnostic writes are staged evidence, never enough to display success without terminal task success.

## 5. Quiescence callback and failure semantics

The engine seam is `Kind.OnQuiesced func(context.Context, pgx.Tx, QuiescedAttempt) error`; the task engine defines `QuiescedAttempt` with immutable getters. Only the account kind supplies a credential-use callback. The callback receives immutable original started-attempt identity, never client JSON. Task engine must call it only from `runOne` after receiving completion of `runExecutor` and after all executor defers have finished. Retain immutable evidence immediately after executor return, attempt ordinary Finish when lease is not known lost, then invoke the independent callback before any final return. Finish must not discard a failed acknowledgement; cleanup accepts terminal tasks. This prevents the cleanup budget itself from expiring an otherwise valid Finish lease. Do not expose it on Execution or public HTTP, and do not invoke it when a timer fires or cancellation was merely requested.

Engine owns a fresh bounded background transaction via its normal exact schema gate and delegates the module's schema → tenant → task lock order to its trusted callback; it deliberately does not call normal actor authorization/workerLock, compare current role/grant/epoch, or require unexpired/current lease. Compare immutable task admission to QuiescedAttempt's original immutable identity; the module then matches ledger task/account/pins and exact saved opener owner/fence/attempt. Task terminalization may have cleared/incremented the current task lease, so matching against current task fencing_token is wrong. Saved opener identity is the authority to acknowledge only that prior stopped use. Keep exact schema compatibility; future upgrades must account for pending acknowledgements, not add an unbounded cleanup schema bypass.

The hook's sole capability is opened→quiesced and removal of its exact dependency, with immutable quiescence evidence. It cannot update task status/lease/epoch, publish diagnostics, reopen/use/decrypt, release another attempt, erase an unknown dependency, or revive a cancelled task. It remains usable after user disable, grant revoke/regrant, account-read loss, tenant entitlement lapse, domain source detach and lease expiry. No current actor row is needed for cleanup.

A trusted transaction-local versioned cleanup context can enforce the SQL transition with exact task/owner/token/attempt. It is an internal application protocol, not an HTTP authority claim; do not reuse B1 admin-governance context. Existing trusted-DB threat boundary applies; no SECURITY DEFINER or production bypass.

Retry cleanup only while the trusted in-process completion evidence is still present. Each attempt is an independent bounded transaction; use the engine's per-owner retained acknowledgement/retry/backpressure mechanism. A bounded immediate retry cycle can use three attempts with short bounded backoff, but exhaustion must not silently discard in-process evidence and immediately run another task in that owner slot. Retain the pending acknowledgement and retry at bounded cadence under backpressure until confirmed, shutdown ends the process, or an unrecoverable compatibility condition is reported. Exact retry after uncertain commit is safe. A revoked actor or expired lease is not a retry blocker. Preserve opened ledger and dependency throughout failure and report sanitized worker cleanup failure. Finish behavior follows the engine's contract under its normal fence; an account stays in use while cleanup is unresolved. Do not repeatedly rerun the executor to fix cleanup.

Never-opened reservations: add a narrow reserved-only reconciler in the domains module. It selects candidate task IDs without retaining row locks, then uses a fresh exact-schema transaction in schema → tenant → task → connection → account → use order, consistent with opened cleanup and existing account parent guards. Re-read the task and ledger under those locks. Only terminal immutable task admission plus state=reserved and absent opener fields proves that a later authorized WithTx cannot open. Atomically set quiesced(reason=never_opened_terminal) and remove the exact typed dependency; if state changed to opened, do nothing. Cover cancellation before claim, Start authorization rejection, kind rejection, timeout/recovery and every terminal outcome. Call this bounded reconciler from an explicit worker maintenance seam or after relevant terminal operations have committed, outside Claim/RecoverExpired transactions and locks. Do not call it inside F03 replacement after that path already acquired connection/account rows; that reverses task-first ordering. A separate bounded domains maintenance loop is an acceptable alternative if it is started/stopped with the worker and uses exact schema gates. Never use generic task terminal triggers to mutate domain/account resources. It must never touch opened rows. If OnQuiesced sees reserved before Finish it does nothing; subsequent terminal reconciliation supplies the separate proof. The actual candidate discovery/continuation mechanism must be wired and tested; a helper that is never invoked is not complete.

Crash or uncertain close/join: no callback proof exists, so opened rows stay quarantined. Expired task, terminal state, timeout, process death indication, source detach, revoke or a new worker does not authorize their release. B2 exposes no force-release/reconcile API and makes no claim that all crashes self-heal. Document blocked replacement/deletion as `account_in_use`, with safe UI explanation that unresolved use must be investigated. Panic cleanup is allowed only because the registered executor's installed defers guarantee pair clearing and adapter close/join while unwinding; any adapter violating that contract must not enable the callback.

## 6. Projections, audit and compatibility

Define one shared classification helper for precisely the two F01 kinds and use it in dedicated HTTP guards, task redaction/detail UI, test-result authorization, generic submit rejection and relevant SQL joins. Do not broad-match `domain.*`. Register both in cmd/adtr. Prohibit scheduling, recovery and generic submit for both; direct engine insertion of new reference task without its required reserved ledger/dependency must fail the deferred SQL invariant.

Replace the current `payload->>'credentialRevision' <> ...` comparisons with fail-closed per-kind/version/source predicates using `IS NOT DISTINCT FROM`, or invert using IS DISTINCT FROM. SQL NULL must never fall through to testing/verified. Match tenant, domain, exact kind/version, source, connection generation, connection revision, latest task, diagnostic generation and policy. Reference evidence also matches account ID/pair and stored grant provenance; do not make a historical observation disappear merely because the observer later lost grant. Current test permission is separate from observed status.

`credentialConfigured` is source-aware existence/integrity, not actor permission: custom has exact local pair; reference has exact same-domain live account/pair plus binding; unconfigured false. Apply consistently to list/detail and F02 single-statement list/resolve. F02 resolve keeps expectedRevision and expectedCredentialRevision=connection generation. Old custom observations remain valid without grants.

Add nullable reference/source provenance to diagnostics and domain_audit, or a task-keyed metadata companion table. Existing diagnostic credential_revision remains connection generation. Every new account observation retains account pair/binding/grant pins internally. Do not include account/grant pointers in ordinary public Diagnostic or domain audit/export details unless a separate account-read-authorized projection is deliberately added. Existing task Result/Cursor are always `{}` for both kinds; sentinel data must stay redacted on list/detail/cancel/replay/test-result and browser rendering.

Add explicit source mutation audit actions (`domain_credential_reference`, `domain_credential_custom`, `domain_credential_detach`), plus create-unconfigured represented by domain_create with safe source provenance. Extend SQL action constraints and projection result/action allowlists. Task joins in audit must match tenant+domain+task and exact family. Protected domain audit visibility/export rules remain intact; record source/pin provenance without widening ordinary metadata disclosure. Source receipt/audit failure is transaction failure.

Schema13 is additive for queued custom-v1 compatibility, not a zero-downtime guarantee. Old binaries reject schema13 at gated operations. New binaries accept unchanged queued custom v1; they must not expect account grants/ledger for those tasks. Migration while old worker is in flight may fence/interupt it; normal rollout quiesces old workers when preserving active attempts matters. No old payload rewrite, replay or drain-only requirement for queued custom tasks.

B1 consumerEnabled must now truthfully report whether this B2 consumer implementation is installed, not continue hard-coded false. Use one compiled feature capability across all B1 response types and update frontend exact validators from literal false to supported boolean. It is not per-account eligibility, key availability, runtime readiness or remote installation support. Retain separate effective eligible and safe testEligible signals. B1 docs must identify that B2 enabled only this purpose.

## 7. Migration collisions and operational limits

Version12 allowed unknown dependency/task kinds. If a prior row already uses a newly reserved B2 task kind or typed dependency name, migration13 refuses atomically and preserves its original data/version. It does not infer provenance, populate pins or remove blockers. Reconciliation of such a collision requires a separately reviewed migration plan.

The worker invokes reserved-only reconciliation in a separate bounded loop, outside engine claim/recovery transactions, and once after executors join on shutdown. Each cycle has a five-second budget and bounded candidate count. Development Compose and browser harness give workers 30 seconds of shutdown grace for the acknowledgement drain and final reserved-only pass; a forced process kill still preserves unresolved blockers. Opened use requires the exact in-process completion witness; a restarted process cannot force-release it.

## 8. Required evidence and stopping criteria

These are required tests, not evidence already obtained. Record baseline commands before implementation. Missing isolated PostgreSQL/Docker/browser prerequisites are blocked gates, never passing skips. No real AD or external notifications.

### Unit/DOM

- Exact source/create bodies, unknown/null/duplicate rejection, mixed-source rejection, ID/revision canonicalization, proof redaction, metadata-only receipts, keyed custom fingerprint/replay conflicts
- Both payloads stay separate; old custom v1 accepts exactly historical keys; new account v1 never uses custom Open; full tenant/domain/account/revision substitution denial
- Shared family classification covers backend and frontend; all generic Result/Cursor render paths redact both kinds
- Source dialog respects grant/account/domain/actor revisions, account label-only reread, role/grant change, source stale CAS, custom pair clearing, no old source pointer in storage
- Nonsecret actor-bound intent only; no proof/pair/raw body in local/session storage; uncertain response → same key receipt recovery; repeated clicks, navigation/Back/Forward, close/cancel, profile switch and late responses cannot commit new UI state

### Actual-Migrate PostgreSQL and HTTP

Use external-package fixture patterned after credential_governance_http_external_integration_test.go, calling store.Migrate against an isolated database. Do not concatenate incomplete new schema and set version13, disable guards, inject privileged production bypass, or test proof/context only with direct SQL.

1. Genuine v12→13 upgrade preserves custom queued/terminal tasks, pair ciphertext and hashes/epochs/diagnostic history; no grants/bindings/task uses synthesized; new worker executes eligible queued custom v1. Old version12 engine rejects schema13 claim/checkpoint. Actual migrator repeat/concurrent/failure atomicity remains covered.
2. Bootstrap via new HTTP create-unconfigured → F47 scope → F03 one pair → explicit admin self-grant → reference → test. Assert no domain ciphertext exists during reference; source/grant mutations make zero transport calls. Test new create receipt replay, source-crossing creation key conflict, capacity and no-key creation.
3. Default deny admin/custom role, exact tenant/domain/purpose/pair/grant, no grant from metadata rights/binding/role assignment; B1 indirect-delegation protections remain installed. Foreign and ungranted existence errors are indistinguishable before CAS.
4. Source mutation proof/audit/receipt/dependency failure rolls back source, ciphertext, counters, proof consumption and epochs. Repeat after uncertain commit returns original safe receipt without proof reuse or grant renewal. Account label-only edit invalidates reference-selection CAS but not an already submitted valid task.
5. Bind vs account replacement/delete, detach/switch vs submit, grant revoke/regrant vs snapshot, and different-actor/same-key cross-kind submission have a valid serialized outcome. Assert named family index independently prevents dual kinds. Test source switch while queued custom/reference work exists: no fallback to the new pair.
6. Direct SQL tests: source/pointer/ciphertext exclusivity; same-generation pair swap rejected; missing/wrong typed dependency; deleting blocker while binding/opened use remains; inserting reference task without reservation; wrong role/grant pins; opened ledger identity mutation/backwards state/delete/truncate; unknown dependency retained. Inversion tests must finish boundedly with rollback rather than deadlock.
7. Runtime revocation at snapshot, DNS/dial, TLS, bind, rootDSE and publication barriers prevents each later unauthorized phase and stale diagnostic publication. A successful staged observation alone never appears verified. Zero transport for admission/auth/key failure.
8. While a synthetic probe is blocked, expire/recover/terminalize its task, revoke use and detach the connection. F03 replacement/delete remains account_in_use despite terminal status. Release probe, wait for actual return/close/join/buffer cleanup, then exact saved opener callback succeeds despite changed actor/lease and only then replacement becomes eligible. Match old saved opener token, not current terminal task token.
9. Wrong owner/token/attempt callback cannot release; exact duplicate can; callback never runs before Execute returns, including cancellation/timeout/shutdown/panic defers. Callback DB failure/retry and uncertain commit preserve blocker until confirmed; each transaction/retry burst is bounded and owner backpressure retains the in-process acknowledgement rather than silently dropping it. Simulated process loss leaves opened blocker across worker restart. There is no force-release route. Never-opened cancel/Start-denial/expiry is cleaned only by the separately invoked reserved reconciler after terminal commit; prove it runs outside generic Claim/RecoverExpired locks and cannot race an open into releasing opened use.
10. Read/cancel/history after use revoke remains safe under normal rights. Source base/detail/receipt under lost account-read/tenant lapse exposes no pointer; detach still works with current domain-write/proof/persisted scope. All ordinary F01/F02/task/audit/export responses stay pointer/secret-free for callers lacking account read.
11. List/detail/F02 list+resolve cover both kinds: queued/running/succeeded/failed/cancelled, detached/switched source, wrong kind/version, NULL/missing source-specific pins, wrong account/generation and stale policy. Missing JSON keys fail closed. No grant is required to preserve old custom observations; historical valid account observations are not represented as current test permission.

### Isolated LDAP evidence

Add an actual HTTP → PostgreSQL task → Engine.RunOne → synthetic TLS LDAP fixture → diagnostic flow, rather than only stubbing Store.probe. Fixture verifies the precise registered account username/password was bound and that no duplicate F01 custom pair exists. Cover StartTLS389 and trusted LDAPS636 as existing harness permits; preserve certificate mismatch/untrusted/expired rejection, no plaintext bind, fixed rootDSE scope, referral refusal and allowlisted destination. Use synthetic credentials only; fixture can assert equality internally without logging the pair. Verify socket/goroutine close before quiescence callback and blocked replacement.

### Browser/CI

New `account-references` suite must use actual API/database and worker, plus isolated synthetic LDAP service or explicitly report LDAP-through-browser absent while dedicated integration supplies it. Existing credential-use harness currently disables probes and does not start the worker for that suite; reusing it unchanged does not prove consumption. Wire suite choice, env/fixture/worker startup, bounded script timeout and required CI matrix check.

Browser acceptance: unconfigured bootstrap without double pair entry; account picker scoped to current domain; explicit grant status; reference save and exact source display; successful synthetic test; revoke → no new test → detach without grant; blocked replacement while use unresolved; custom transition requires fresh pair; old custom editing still works; source/parent/account/grant stale conflict; uncertain save receipt recovery; actor change/Back/late reply; no username/password/proof/ciphertext in DOM/storage/task details. Include screenshot evidence for the main source states when available.

Final local gates: make check, focused/new race tests, all integration-tag compile/vet; real PostgreSQL migration/governance/executor/LDAP suites; registered browser suite plus impacted domains/operations/credential-use/tasks/F02 regressions; lifecycle checks as required by repository. Then inspect actual diff and exact-head CI, record independent review separately. Build, SQL/LDAP/browser evidence, merge and product acceptance remain distinct; full-product acceptance stays 0/209 absent its separate gates.
