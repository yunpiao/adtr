# F03 域管理操作账户生命周期：实现前契约与字段覆盖

Stage A local implementation contract, 2026-10-07 UTC. Refs [Issue #17](https://github.com/yunpiao/adtr/issues/17), AD-F-008–011; roadmap sequence 11/57. All 209 requirements remain in scope. This document defines the local slice and records the remaining source/real-AD acceptance work; it does not mark any feature accepted or authorize remote operations.

## 1. Recommendation and decisive boundaries

Implement F03 first as a tenant/domain-scoped registry of credentials supplied for an existing domain account. The four source descriptions support listing, registering, editing configuration, and removing the registration. They do not establish that “新增” creates an AD user, that “编辑” changes its remote password, or that “删除” deletes/disables the AD user. Do not implement those remote meanings by inference.

Use the existing separate domain encryption runtime, but add an account-specific authenticated envelope. Do not place F03 secrets into F01's one-row-per-domain `domain_credentials`, use an account ID as a pretend domain ID, or reuse the current domain-only AAD for multiple account records. Keep F01 custom credentials in their existing ownership model. A future explicit F01 reference to an F03 account must consume the single F03 secret by reference and require a separate, explicit permission to use that account for that consumer.

The first local F03 slice must not perform DNS, socket creation, LDAP bind, password checks against AD, remote account creation/reset/deletion, WinRM, SMB, installation, or automatic validation. A successful database commit means “saved locally, not verified.” F05 remains blocked for real installation even after local F03 CRUD passes.

Three distinct changes must never share an ambiguous “rotation” operation:

1. **Replace the stored pair:** save newly supplied username/password bytes locally, increment the credential revision; no remote effect
2. **Change a remote AD account password or lifecycle:** a separate future authorized AD write with identity, privilege, uncertainty and reconciliation contracts; blocked here
3. **Re-encrypt with a new vault key:** an operator key-migration workflow preserving logical credential identity/revision; not implemented by swapping an environment key

## 2. Evidence read and current implementation boundary

Primary local sources:

- Issue #17 supplies the full F03 field/control contract; official dependencies are F43/F45/F46. F05 (#19) depends on F01/F03/G02/G03
- `requirements/catalog.json`: AD-F-008–011 are source sheet `AD 功能清单`, rows 12–15, 32 distinct fields FLD-0145–0176, and UI-0315/UI-0316; all four are in progress, with `product_accepted:false`
- Original workbook 2026-10-03-AD功能清单.xlsx; field/control rows were cross-checked against the issue inventory
- Repo `AGENTS.md`, README, `docs/delivery.md`, `docs/architecture.md`, `docs/domain-contract.md`, `docs/access-contract.md`, and `docs/task-authorization.md`
- Actual F01 files: `internal/domains/{types,schema,validation,store,query,admission,executor}.go`, `internal/domainconfig/{vault,config}.go`, `internal/auth/{domain_http,domain_permissions,access_http,access_types,access_schema,resource_store,task_authorization}.go`, `internal/audit/domain_schema.go`, and `web/src/{domain-api,domain-intent,DomainsWorkspace}.tsx/ts`

F01 is the preceding local domain-connection slice. This registry is introduced by migration10; migration9 remains the domain baseline.

Concrete F01 facts relevant to F03:

- `domain_connections` is `(tenant_id,domain_id)`, holds positive `connection_revision`, `credential_revision`, diagnostic generation, and currently has `CHECK(credential_mode='custom')`
- `domain_credentials` has primary key `(tenant_id,domain_id)` and one current encrypted pair. Updating it replaces the previous ciphertext; it is not a historical secret archive
- `Vault.Seal/Open(tenant,domain,revision,...)` authenticates purpose `ADTR/domain-credential/v1`, key ID, tenant, domain ID and credential revision. It does not authenticate an operation-account ID
- The complete versioned username/password object is encrypted. Public F01 DTOs omit both username and password
- Domain credential replacement increments both F01 revisions, clears latest test, advances diagnostic generation and triggers tenant authorization-epoch invalidation
- F01 tests pin connection/credential/policy/generation and recheck them through fenced transactions; they use a single attempt and have no generic automatic recovery
- Live F01 deletion already checks `domain_dependencies` and other nonterminal domain tasks before destroying local custom ciphertext
- F01 currently validates account names/passwords at 1–50 Unicode codepoints and ≤200 UTF-8 bytes, with a nonempty pair and explicit account syntax. Those bounds came from F01 source/UI decisions; F03 itself has no equivalent declared limit
- `loadAccessGrants` gives builtin `platform_admin` every registered mark; `taskAuthorizer` separately bypasses custom-role function-grant rows for builtin admin. `access_permissions` rows reference custom roles only. Therefore simply adding a new “use credentials” mark would silently give admins that use permission, and inserting builtin-role rows into the existing table is not a workable fix

Real PostgreSQL/browser/CI execution and AD validation remain separate evidence gates. Local unit/race, crypto fuzz, and frontend tests do not establish those gates.

## 3. Source ambiguity that must stay visible

`FLD-0150 isPlaintext` is explicitly described in the workbook as a plaintext display/export switch: 0 hidden, 1 shown. The underlying `userInfo` map's keys and contents are not supplied. FLD-0160's evidence says only its definition was seen; no matching read/assignment was found in the inspected source method. It is therefore unsafe to claim either that the map is certainly a password object or that it is harmless. The local API below returns a typed safe projection and rejects plaintext retrieval; that is an intentional security difference, not claimed parity.

UI-0315 and UI-0316 are shared references to `itdr_frontend/src/pages/applicationAccess/AD/components/account.vue:4` and `:5`, labels `1`/`2`, binding `radio`; the second has a static disabled attribute. They are also linked to AD-F-006. Their meanings, candidate labels, parent-page visibility and permissions have not been verified. Do not call them “custom/system account,” “show/hide password,” or “create existing/new AD user” without the actual component and runtime evidence.

Other unresolved source semantics include:

- Whether there is one management credential per domain, multiple registered records, or a unique principal constraint
- Whether `domain` is DNS text, a connection ID or another source-system identity
- Whether `filterStatus` describes the domain, registration, credential validation or a task; its comment says domain state but no enum exists
- Search fields, case/normalization and ordering; no F03 sort field is declared
- The origin and meaning of the result `port`; no F03 input sets it
- Username forms, lengths and alias equivalence; password bounds; omission/null/empty update semantics
- The exact integer `status`/`msg` and delete `result` behavior on errors, transaction loss and concurrent updates
- Plaintext display audience, authorization, redaction, retention and whether any nonsecret subset must be shown
- Operational account privilege requirements, domain identity pinning, actual remote side effects and recovery policy

The missing source repositories/commits and authorized runtime/lab evidence remain G01/G05 blockers. Source discrepancies must be recorded and resolved, not silently removed from the 209-item scope.

## 4. Stage A: durable local registry contract

Module `internal/operationaccounts`; it owns its tables and encrypted credential lifecycle. HTTP authentication/proof stays in `internal/auth`; crypto stays in `internal/domainconfig`. The module does not import a transport or register an AD task executor in Stage A.

### Identity and scope

- Generate a random opaque `accountId` using the existing resource-safe alphabet, distinct from local platform users and AD principal identifiers. Never derive the ID from a username or password
- Every account is immutable within `(tenant_id,domain_id,account_id)`. Use composite foreign keys and include all three values in secret lookup, audit and future consumer references
- The parent must be an existing, live F01 domain connection and active F47 catalogue entry. F03 cannot enroll a domain, consume another domain-capacity slot, activate a deleted domain or create resource grants
- Resolve `domainId` only under the authenticated tenant and explicit exact-domain resource membership. The first local API uses that ID, not a caller-supplied DNS name as authority
- Keep the schema capable of multiple stored credential records per domain. Do not deduplicate by password equality, make an unkeyed username/password hash, infer AD principal equivalence, or assert a source one-account limit. Local same-principal duplicates are not remotely detectable without an independently specified identity check
- A local optional `label` (1–50 non-control Unicode codepoints, ≤200 UTF-8 bytes, no edge whitespace) may identify the record without decrypting/displaying usernames. This is explicitly a new nonsecret UI label, not an AD name or source field. Omitted label displays a shortened account ID with full ID available in detail. Do not put label or credential inputs in logs/audit bodies

### Stored model

Proposed tables, introduced in a versioned migration after the final F01 baseline:

| Table | Required contents and constraints |
| --- | --- |
| `operation_accounts` | tenant, domain, account ID; optional label; positive record revision and credential revision; created/updated timestamps; deleted timestamp. PK `(tenant_id,domain_id,account_id)`, unique `(tenant_id,account_id)`, FK `(tenant_id,domain_id)` to live-parent identity. No plaintext username/password and no remote-status boolean |
| `operation_account_credentials` | tenant, domain, account ID, matching credential revision, envelope version, key ID, ciphertext, created time. Exactly one live row per account; no prior-password history. Composite FK to its account |
| `operation_account_mutations` | tenant, actor, idempotency key, operation, keyed request fingerprint, account/domain IDs and immutable safe mutation receipt. Identity-revision/result metadata only; no raw request, proofs or encrypted secret copy |
| `operation_account_audit` | tenant/actor/domain/account IDs, allowlisted action/result, old/new record and credential revisions, request correlation, timestamp. Append-only with update/delete/truncate guards and existing request metadata capture |
| F01 `domain_dependencies` | Add one `module='operation_accounts'`, `object_id=accountId` row for every live registry account, atomically with creation. Remove only on local deletion. A domain cannot be disconnected while its registered accounts would become inaccessible |

Do not model current verification by `status='success'` on save. The Stage A DTO has `storageState:'saved'`, `verificationState:'unverified'` and `credentialConfigured:true`; deleted records appear only through authorized receipts/history, not the ordinary list. Failure to read configuration/DB/key availability is an error, not an empty list or a fabricated remote account status.

Record and credential revisions are positive canonical decimal strings in JSON, backed by bigint. Never reset/reuse an ID/revision; reject overflow before writing. Metadata-only label changes increment the record revision, not credential revision. Replacing the pair increments both even if the submitted bytes happen to equal the old pair. Do not compare passwords to identify a no-op. A request that changes neither label nor pair is `409 no_change`. An omitted label preserves it on update; an explicitly empty label clears it, while nonempty labels follow the bounds above.

### Local lifecycle and atomicity

Create: acquire the schema gate → authenticate/authorize under tenant/actor/domain locks → check the original intent before applying current expected-parent-revision validation to a new request → consume fresh proof → seal supplied pair → insert account, ciphertext, domain dependency, safe receipt and audit → commit. An authorized exact replay returns the original receipt rather than sealing again. No network callback is allowed in that transaction or after commit.

Edit: require exact `expectedRevision`; no domain/tenant/ID reassignment. Omit both credential fields to preserve the pair for a label-only edit; supply both to replace. A single field, null, empty pair or magic asterisks-as-placeholder has no preservation semantics. A literal password containing asterisks is treated only as explicitly supplied password bytes, never as a stored-secret token.

Delete: require exact revision, typed account-ID confirmation and fresh proof; reject active consumer references/nonterminal usage described in §7. Tombstone the local record, advance both revisions, delete live ciphertext, remove the F01 domain dependency, write receipt/audit and commit together. Do not remove the remote AD account, modify its password, revoke its domain privileges, uninstall software or delete domain grants. No restore endpoint is included; re-registration creates a new ID and starts unverified.

Use the established schema → tenant → actor lock discipline, followed by deterministic domain/account locks. Any error rolls back proof consumption, metadata, ciphertext and audit together. Account deletion/replacement and future account-use grant changes invalidate affected persistent authorization epochs under the tenant lock; conservative tenant-wide invalidation is acceptable and must be documented. Preserve direct-SQL trigger locking/no-truncate protections so revoke/regrant cannot make an old task current again.

Deleting the live database ciphertext does not prove physical erasure from backups or guaranteed cryptographic destruction with a shared key. Retention and operator recovery remain a separate G06 contract. Do not silently delete past receipts/audit or remove their foreign-key identity anchor.

## 5. Vault and policy reuse

### Account-specific encryption

Reuse `ADTR_DOMAIN_KEY_ID`/`ADTR_DOMAIN_KEY` and the existing immutable runtime, preserving the separation from `ADTR_AUTH_KEY`, malformed-key startup failure, disabled state, redacted errors and AES-GCM random nonce behavior. Add narrowly typed methods such as:

```go
SealOperationCredential(tenantID, domainID, accountID string, credentialRevision int64, plaintext []byte) (OperationSealedCredential, error)
OpenOperationCredential(tenantID, domainID, accountID string, credentialRevision int64, sealed OperationSealedCredential) ([]byte, error)
```

The operation-account envelope has a distinct version/purpose and unambiguous length-prefixed AAD including application purpose, key ID, tenant ID, domain ID, account ID and credential revision. Existing F01 v1 envelopes keep their exact encoding. Account ciphertext must fail authentication if substituted across account IDs even within the same tenant/domain at the same revision; it must also fail at the F01 domain-secret open method. Do not obtain namespacing by concatenating domain/account into an ambiguous synthetic ID.

Encrypt the complete strict versioned pair. Keep plaintext buffers caller-owned and short-lived; clear owned byte buffers best-effort and avoid unnecessary immutable string copies. Return only fixed error codes; no JSON, Stringer, GoStringer, logger, task payload or debug dump may expose keys, plaintext or ciphertext. Database metadata projection never selects/decrypts the secret merely to list accounts.

Use `Vault.Fingerprint` with distinct purposes such as `operation-account-create-v1`, `operation-account-update-v1`, `operation-account-delete-v1`. Canonical fingerprint input includes exact normalized nonproof request fields, operation, target and expected revisions; when it includes supplied credentials, keep only the keyed digest. Never store canonical secret-bearing JSON or an unkeyed password hash. Reuse the same idempotency key after uncertainty, or read the receipt; do not create another logical action automatically.

The current vault has one active key, rejects ciphertext from other key IDs, and includes key ID in keyed fingerprints. A key change would also change replay fingerprints. Before an operator key-rotation feature can ship, define dual-read/single-write key availability, idempotency-fingerprint compatibility, transactional rewrap, retirement checks and backup recovery. This F03 design must not claim those capabilities already exist or equate key replacement with safe rotation.

### Credential validation and source limits

For a first implementation, extract a shared pure pair validator from the F01 contract rather than copying divergent validators. Use its current 1–50 codepoint/≤200-byte bounds as an explicitly provisional local compatibility limit, not a source F03 limit or a statement of every AD-supported credential form. Preserve password bytes; reject invalid UTF-8/NUL/empty password, do not trim or normalize, and do not apply platform password-complexity policy to an existing AD account. Username syntax and alternate UPN suffix behavior remain as F01 currently specifies. Freeze source-specific F03 limits before claiming parity; widening a shared validator requires F01 regression evidence.

### No network authority from storage

`Runtime.Enabled()` is sufficient for storage; `ProbeEnabled()`/CA/egress policy need not be enabled to save an account. CRUD is network-free even if probe settings happen to be enabled. Conversely, a saved account is never proof that a target is authorized or reachable.

A later LDAP consumer must still use F01's exact tenant/domain/server-name/CIDR policy, CA validation, policy revision, pinned destination and fixed-operation transport. An LDAP policy entry is not permission for WinRM/SMB/RPC or remote installation. F05 requires its own protocol/target/identity/deployment-policy contract; no arbitrary host, URL, port, command or credentials may come from task JSON.

## 6. HTTP and authorization contract

All paths below are a new local API, not claimed legacy endpoint compatibility. Prefix `/api/operation-accounts`:

| Route | Exact local inputs | Safe durable result |
| --- | --- | --- |
| `GET /api/operation-accounts` | bounded list filters below | `{page,List,exhausted}`; typed DTOs only |
| `GET /api/operation-accounts/detail` | exactly `accountId` | `{account:...}` with safe metadata, no decrypt/reveal |
| `GET /api/operation-accounts/mutation` | exactly `idempotencyKey` | original actor's safe mutation receipt, current lifecycle indicator |
| `POST /api/operation-accounts/create` | `domainId`, `expectedDomainRevision`, `username`, `password`, `idempotencyKey`, optional `label`, proof | `result:'SUCCESS'`, account/domain IDs, record/credential revisions, `replayed`, `verificationState:'unverified'` |
| `POST /api/operation-accounts/update` | `accountId`, `expectedRevision`, `idempotencyKey`, optional label and paired credentials, proof | safe saved result/revisions/replayed; no echoed input |
| `POST /api/operation-accounts/delete` | `accountId`, `expectedRevision`, `confirmAccountId`, `idempotencyKey`, proof | safe local-deletion receipt/revisions/replayed |

`proof` means the current platform `actorPassword` and fresh unused `totpCode`; it is unrelated to the stored domain-account password. Keep the distinction in Go fields, labels, validation and tests. Require existing session cookie, exact Origin, JSON, X-CSRF-Token, identity/forced-change gates and rate limits, just as F01 does. Bodies ≤32 KiB, UTF-8 only, exact allowed keys, no duplicate keys/nulls/trailing JSON/query string; scalar query duplicates and malformed encoding reject. A response or proxy must use `Cache-Control:no-store`.

Add a distinct `operation_accounts` function mark for metadata CRUD. Read routes need readable; modifications need readable/writeable and fresh proof. Custom roles receive no mark automatically. The builtin admin's local CRUD capability, if added under the existing fixed builtin model, is an explicit documented local administration decision, and still needs exact-domain resource membership. It does not imply credential-use or remote-operation permission. UI menu, `/api/access/check`, handlers, effective grants and persisted permission constraints must share the exact route registry. Do not reuse `users`/`roles` management permission or infer access from a route prefix.

Every route, including create and receipt lookup, requires live F47 tenant policy eligibility and explicit membership in the existing active parent domain. Unlike F01 domain enrollment, F03 has no creator-only no-domain-grant exception. Receipt lookup is tenant+original actor+idempotency key and must also recheck current domain membership/function permission; a receipt is not an enduring authorization token. If a domain was subsequently deleted or permission revoked, return the normal indistinguishable unavailable result. Do not weaken this to resolve UI uncertainty.

Object IDs from another tenant/domain, deleted records on normal detail, or ungranted objects are indistinguishable `404 not_found`. Missing route function permission is 403. Filter scope before count, ordering and pagination, including aggregate totals. Platform admin has no data-scope bypass. Client actor/tenant/role/permissions/status/remote-operation flags are rejected.

Safe DTO: accountId, domainId, canonical domain, label, record revision, credential revision, credentialConfigured, createdAt/updatedAt in UTC RFC3339, storageState and verificationState. No username/password/userInfo arbitrary map, ciphertext, key ID, password-length/mask/last-four, raw exception, remote privilege assertion, or inherited connection verification. A constant “credentials saved” indicator is not a password mask. Linked domain metadata, including its state, is a separate explicitly labeled object only if the caller also holds domains.readable; it must not become credential verification.

List decisions are explicitly local and need source parity review:

- pageIdx default 1, canonical integer 1–1,000,000; pageSize default 20, 1–100; -1 only at page 1, reject above 1,000 matching rows with `422 result_too_large`
- Order createdAt descending, accountId deterministic tie-break; do not accept an invented source `tmSort`
- filterDomain is repeated exact canonical DNS matching authorized parent records, max 100 distinct values; IDs remain the authoritative mutation scope
- filterKeyword ≤50 Unicode codepoints, literal case-insensitive substring of canonical domain and optional local label only; `%`/`_` are literal, no encrypted-username search
- Do not guess `filterStatus` semantics. Stage A accepts omission only; present nonempty values return `422 unsupported_status_filter`. Before enabling it, freeze whether it refers to parent-domain or account state and explicitly version/map the field
- isPlaintext omitted/0 uses the safe projection. Canonical 1 returns `422 plaintext_unavailable`; other integers/malformed values return 400. There is no password-reveal/export endpoint or enabled reveal toggle
- Empty result: `List:[]`, total 0, totalPage 0, exhausted true. Out-of-range page: empty list with real filtered total; no hidden-domain counts

Error classification: 400 malformed/invalid input; 401 session/proof failure; 403 function/tenant gate; 404 absent/foreign/ungranted; 409 stale revision/no change/idempotency conflict/account or domain in use; 422 unsupported known feature or bounded-all too large; 429 rate limit; 500 safe unexpected failure; 503 schema/vault unavailable. No raw SQL/crypto/AD message or input echo. An unsupported feature must not return an empty success.

Idempotency is actor-bound per tenant+key across mutation kinds. Same key+same request returns the original result only after current authorization/proof; a changed request or operation returns 409. Replay lookup precedes current expected-revision validation so a successfully committed prior action can be recognized after it advanced the revision, but follows current authorization. GET receipt is the preferred resolution of a lost response and does not repeat/consume proof. Fingerprints and receipts commit atomically with the action. UI must not infer from 404 alone that a deletion succeeded, because 404 may mean permission was revoked.

## 7. F01 integration without duplicated secrets or broader access

### Stage A, safe immediately after F01 baseline

F03 registry rows are independent supplied credential registrations. Existing F01 custom credentials are not automatically shown as operation accounts, copied into F03, decrypted for an editor, matched by username/password, promoted or granted to F05. Creating F03 data does not change F01 credentials, connection revisions, source mode, or verification. The domain dependency row only prevents unsafe domain removal. Both may legitimately contain separately supplied credentials, but the product never creates duplicate ciphertext copies automatically or pretends they are synchronized.

Do not promise an account selection UI in F01 while `credential_mode='custom'`, task payload v1, executor lookup and frontend exact DTO decoder still support only custom mode.

### Stage B, explicit optional references; prerequisite to any shared use

A separately reviewed integration can add `credential_mode='operation_account'` and a same-domain account/revision reference to F01. Enable it only with all of the following, including a safe detach path:

1. Domain configuration holds an account ID and exact account credential revision, not ciphertext. Its own `credential_revision` remains a monotonic **connection credential generation**; it is not equal to the account credential revision. The public names and task payload must distinguish the two
2. A tagged source is exactly one of custom, operation-account reference, or an explicitly unconfigured state used by detach. Database/app constraints prevent simultaneously active custom ciphertext and a referenced secret, or an operation-account pointer to another tenant/domain
3. A switch to a reference is a proof-bearing F01 mutation with expected connection revision, expected account record/credential revision, domains.writeable, operation_accounts.readable, explicit same-domain account-use permission for `domain.connection_test`, and exact-domain membership. It deletes the old live custom ciphertext, sets the pointer, increments connection/config credential generations, invalidates observations/epochs and adds a consumer dependency atomically. No external request occurs
4. Switching back to custom requires a newly supplied pair. The API cannot recover/reveal/copy the F03 pair into a custom secret. Seal under the existing domain v1 context and remove the reference/dependency in the same transaction
5. Provide an explicit proof-bearing detach action that sets the connection's credential source unconfigured, removes the pointer/dependency, clears verification, increments generations/epochs and makes tests fail closed with credential_not_configured. This is necessary to delete a referenced registry account and then delete the domain without requiring replacement credentials. It must not silently reconnect using another source or resurrect old ciphertext
6. F03 pair replacement/deletion rejects `409 account_in_use` while a persistent F01/F05 consumer binding or unresolved task-use dependency exists. Do not silently rotate an active connection's password, update pointers to “latest,” or cascade consumer changes using only F03 write permission. The user can detach/change each consumer under that consumer's authorization, then edit/delete; or register a separate new pair and explicitly switch consumers. Atomic replace-and-rebind is a future separate reviewed workflow
7. F01 domain deletion continues to reject live F03 account dependencies. Explicitly detach/delete the accounts first; do not orphan inaccessible encrypted records or invent a remote-account cascade. Receipts/history remain tombstoned and subject to their read permissions
8. Rev the F01 task payload/validator/public DTO and executor deliberately. Existing queued custom v1 tasks must either remain supported with their original custom-only semantics or be safely rejected/cancelled by an explicit migration policy. Never reinterpret v1 `credentialRevision` as an F03 account revision

Required task pin for a reference consumer includes tenant/domain from trusted task scope; connection revision and local credential generation; credential source; operation account ID and account credential revision; consumer-binding/use-policy revision; deployment policy; diagnostic generation; plus the engine's actor epoch/lease/fence/result version. Nothing may default to the latest account revision when executing.

Stage B also needs an explicit metadata-only `operation_account_dependencies` table or equivalent authoritative consumer registry keyed by tenant/domain/account, consumer kind and object/task ID. Persistent bindings and admitted task uses are registered atomically under the same tenant/account lock before secrets can be resolved. The account edit/delete check and consumer admission must therefore serialize. Terminal local tasks can release their use reference through their owning module; a remote effect left uncertain must keep a reconciliation dependency until that uncertainty is resolved, rather than treating the local `failed` label as proof no remote use occurred. Store no secret snapshots in this registry. Unknown live dependencies fail closed.

Only the exact trusted executor resolves a sealed snapshot in a short authorized `WithTx`, releases the transaction, decrypts with full account context, uses the pair in its fixed adapter, clears buffers and publishes safe evidence through another fenced check. Recheck source, bindings, use permission and all revisions before decryption, before the relevant external phases, and before result publication. Worker DB access is not an independent principal authorized to bypass the initiating actor. No HTTP endpoint returns a plaintext resolver result.

Bounded cancellation still cannot recall bytes already in flight. Do not promise instantaneous remote revocation, external exactly-once execution, automatic safe retry, or account lockout immunity.

### Account-use authority must be explicit

A reader of the credential registry does not automatically get to bind/install as every stored account. A registry writer can replace unused credentials, but does not automatically get to invoke a privileged consumer. A domains writer with access to F01 custom mode likewise does not acquire every F03 account in that domain.

Before Stage B/F05, add a purpose-specific account-use grant/delegation contract. Recommended model: `operation_account_use_grants` keyed by `(tenant,domain,account,consumer_kind,role_id)` with explicit allow rows and a revision; builtin roles are supported deliberately, as in F47's role-reference model, and have no implicit allow. Consumer kinds are server-owned allowlisted identifiers; there is no wildcard purpose, target or arbitrary command. An empty table denies all reference use, including builtin admins.

Grant management requires the established role/permission delegation limits plus account/domain management authority and fresh proof; it cannot be added as a side effect of account creation or consumer binding. For custom delegators require a valid existing grant for that same account/purpose and prevent broader role/domain delegation. Define a deliberate builtin-admin bootstrap operation for initial grants, still requiring exact-domain membership, proof and audit. If that authorization change is not implemented/reviewed, keep all shared-account consumer use disabled. Do not claim that the current F45 table can store builtin-role grants.

On submit/execute, require this use grant **and** the consumer's normal function/task grants **and** current exact-domain authorization. Grant mutations bump durable actor epochs; revoke/regrant cannot revive old work. Task read/cancel remain governed by their reviewed safe history/cancellation contract and must not expose secret material or prevent revocation processing merely because use permission was removed. Submission/admission alone is insufficient; the executor enforces use permission too.

## 8. Lifecycle for future F05 consumers and unresolved remote effects

F05 must depend on a stable account-reference resolver, not on the current F01 custom credential row or a plaintext download. Its installation task pins the same account/source/use grant and its own target/package/operation policy revisions. An agent package must not embed an AD password, reusable bearer token or arbitrary startup command. Installation-task secret handling and any bootstrap identity belong to a separate reviewed F05 protocol contract.

F05 needs immutable directory/DC identity and explicit target authorization. F01's rootDSE naming-context observation alone does not pin domain object GUID/SID or demonstrate permission to deploy software. Do not display “deployment ready/admin” because F01 bind/rootDSE succeeded or F03 stored a pair.

Until remote operations are designed and authorized, do not register a placeholder `create_account`, `rotate_password`, `delete_account` or `install_agent` executor returning success. Required unresolved contracts for actual remote changes include:

- Actual AD object identity, account type and ownership; distinguishing an existing account from one created/managed by this product
- Exact create container/name/attributes, permissions/delegation, intended password behavior and OS/account-policy compatibility
- How to detect whether an attempted change took effect after a lost response; durable intent/effect ledger, no dangerous blind retry, explicit reconciliation and compensating-action authority
- How consumers move from old to new credentials, old-account retirement, concurrent jobs, rollback and credentials unavailable after a successful remote change
- Protocol authentication, signing/channel binding/transport policy and precise permitted targets; each remains unverified unless independently implemented/tested
- Fresh approval and bounded operation scope for consequential writes, audit/evidence, cancellation meaning, deployment defaults and lab controls
- Windows Server 2008, 2008 R2, 2012, 2012 R2, 2016, 2019, 2022 and 2025 validation. Local registry CRUD is not evidence of remote compatibility on any of them

Do not add new user-facing account disable/enable, remote reset, automatic rotation, reveal, export or install actions merely because a generic lifecycle design might have them. The four source features remain the starting scope; unresolved capabilities stay recorded rather than invented.

## 9. Exact field and UI coverage ledger

Legend: **local** = proposed persisted/validated local mapping; **changed** = intentional local/security difference; **blocked** = source semantics or required behavior unresolved. None means implemented, tested or product accepted.

| Record | Source | Proposed disposition and necessary evidence |
| --- | --- | --- |
| FLD-0145 | input pageIdx | local: positive one-based bounded canonical integer, default 1; test missing/0/negative/overflow/repetition |
| FLD-0146 | input pageSize | local: 1–100/default20, bounded -1; limits are local, source capacity/default remains blocked |
| FLD-0147 | input filterDomain | local: repeated canonical parent DNS, OR within field and AND with others, scoped before count; source DNS-versus-ID and operator semantics blocked |
| FLD-0148 | input filterStatus | blocked: source says domain status but enum/meaning unknown; nonempty field returns unsupported_status_filter, no fabricated values |
| FLD-0149 | input filterKeyword | local: ≤50 codepoints literal domain/local-label search; search target semantics deliberately changed; no encrypted username lookup |
| FLD-0150 | input isPlaintext | changed: omitted/0 safe metadata, 1 explicit plaintext_unavailable; reveal/export parity remains blocked, no secret-read path |
| FLD-0151 | result page | local typed object based on committed authorized result set |
| FLD-0152 | result page.pageIdx | local exact validated request/default |
| FLD-0153 | result page.pageSize | local actual page size including bounded -1; source “all nodes” is not unlimited support |
| FLD-0154 | result page.total | local count only of live authorized matching records |
| FLD-0155 | result page.totalPage | local ceiling; 0 empty and 1 nonempty bounded-all |
| FLD-0156 | result List | local safe typed account array; [] on genuine empty result, errors never disguised as empty |
| FLD-0157 | result List[].ID | changed to accountId; stable opaque local registration identity, not AD object identity |
| FLD-0158 | result List[].domain | local canonical DNS from immutable parent domain, alongside explicit domainId; foreign same-name domains never join |
| FLD-0159 | result List[].status | changed to separate saved/unverified projection; parent status, remote account enablement/privilege and credential validity remain blocked |
| FLD-0160 | result List[].userInfo | changed to credentialConfigured + credentialRevision + optional local label; raw map/keys/username/password unavailable, source shape blocked |
| FLD-0161 | result List[].createTm | changed to createdAt; database UTC RFC3339, never a fake remote creation date |
| FLD-0162 | result List[].errMsg | blocked for unknown legacy lifecycle; local request errors fixed safe codes, future verification evidence consumer-specific; no fabricated per-row AD error |
| FLD-0163 | result List[].port | blocked as account field; if independently displayed as linked-domain metadata, label it F01 endpoint port and require domain read permission; never infer F05 protocol port |
| FLD-0164 | result exhausted | local derived from count/request/results, correct for empty/last/out-of-range pages |
| FLD-0165 | create input domain | changed to exact existing domainId + expectedDomainRevision; no DNS authority/implicit domain enrollment |
| FLD-0166 | create input username | local write-only complete credential member; provisional shared F01 validator; no normalization/equality/remote-user creation claim |
| FLD-0167 | create input password | local exact write-only nonempty secret, encrypted paired with username; no platform strength policy, log/echo/reveal/hash |
| FLD-0168 | create result status int 1/0 | changed to SUCCESS only after commit, typed errors on failure; saved locally is not AD creation/bind success |
| FLD-0169 | create result msg | changed to fixed error code/UI message; no raw service exception or submitted values |
| FLD-0170 | edit input ID | changed to scoped accountId plus expectedRevision; no identity/domain reassignment |
| FLD-0171 | edit input username | local paired replacement or both omitted for metadata-only edit; one/null/empty rejects; source omission rules blocked |
| FLD-0172 | edit input password | local replacement increments secret revision; no remote password reset, no masked-value preservation token |
| FLD-0173 | edit result status int 1/0 | changed to durable local SUCCESS + new revisions, HTTP failure/conflict otherwise |
| FLD-0174 | edit result msg | changed fixed safe codes/messages, no request/crypto/AD echo |
| FLD-0175 | delete input ID | changed to scoped accountId + exact expectedRevision + typed confirmation; active-use conflict is explicit |
| FLD-0176 | delete result result | local SUCCESS means tombstoned registration/live ciphertext removed; remote user/password/access unchanged |
| UI-0315 | radio value 1 | blocked source label/action/visibility; build explicit locally labeled existing-credential form instead, no guessed enum mapping |
| UI-0316 | radio value 2, disabled | blocked source meaning; do not enable a source-disabled option or implement managed-account behavior on assumption |

The userInfo/port/status/plaintext/control gaps are intentionally not disguised by fields with plausible names. These entries need source evidence or a documented accepted semantic change before F03 can close.

## 10. Browser workflow and interruption handling

Add an “操作账户” workspace controlled by the new fixed route registry. It selects existing authorized domains and lists domain, safe local label/account ID, saved/unverified state, created/updated time and credential revision. Distinguish no authorized records, no matching records, loading, forbidden and service/key failures. Label controls and create/edit outcomes as “保存操作凭据”; explain that the remote account is unchanged.

Creation requires the existing domain ID/revision, optional local label, newly entered username/password and distinct platform proof. Password inputs are empty, no prefilled mask. No test or remote operation starts automatically. Editing fetches only safe metadata and has a deliberate “替换已保存凭据” toggle; both inputs start empty. Deletion explains local scope and remote non-effect, shows full account ID/domain and requires typed confirmation. Show safe in-use failure without offering a destructive cascade.

The optional label solves record identification without adding a secret-reveal feature. Do not claim it is the AD username, and do not allow a server-supplied HTML label. The plaintext switch is absent or visibly unavailable with an accurate explanation; a browser-only show/hide control for the password the user is currently typing is a different local action, never a request to retrieve a stored password.

Credentials/proof live only in the active form. Clear them on submit completion, dismissal, navigation, Back/Forward, account change and session invalidation; cancel request controllers and invalidate response generations. Do not persist them in URL/history/sessionStorage/localStorage/indexedDB, telemetry, error reports, task payloads or screenshots used as evidence. No network auto-retry of proof-bearing mutations.

For uncertain mutation outcomes retain only safe `{operation,idempotencyKey,domainId,accountId?,expectedRevision?}` with an exact current-account owner; reuse F01's generation/AbortController design but use a separate storage key and reconstruct only allowlisted fields. Do not store username/password/label/proof or a secret-bearing fingerprint in the browser. Receipt recovery resolves the original action without retyping/resending its secret. If recovery is unavailable after authorization changes, say the result cannot currently be confirmed; neither display success nor create another action automatically.

Disable repeated submissions, preserve the original key until resolved, handle stale-revision conflicts through explicit reload/review, and do not let a late response reopen a dismissed form or overwrite newer navigation. A closed dialog or interrupted fetch is not server rollback. Test real browser→API→PostgreSQL persistence, not only mocked route responses.

## 11. Verification plan and test names

Tests below are required for the implementation; they were not run for this design. Record baseline, implementation evidence, exact head CI and independent review separately. Keep all data synthetic and isolated; no real passwords or directory targets.

| Test group | Required assertions |
| --- | --- |
| `OperationAccountSourceLedger` | Exactly FLD-0145–0176 and UI-0315/0316 mapped once; unsupported entries remain explicit; all four source AD-F remain unaccepted |
| `OperationAccountStrictHTTP` | Methods/paths/Origin/CSRF/proof/body size/unknown keys/duplicate keys/null/invalid UTF-8/scalar repetition/actor-tenant injection; fake flags cannot enable remote behavior |
| `OperationAccountScopeBeforePage` | Same DNS in two tenants, foreign account IDs, builtin admin without domain grant, custom reader/writer, hidden-domain counts, revoked role/membership, expiry/capacity gate |
| `OperationAccountPairBoundaries` | Empty/one-field/null/update omission, maximum codepoints and UTF-8 bytes, alternate UPN suffix, control/NUL handling, exact password-byte preservation and no platform-policy enforcement |
| `OperationAccountVaultContext` | Ciphertext swap across account/tenant/domain/revision/purpose/key, corruption/truncation, nonce freshness, disabled/malformed key, current F01 v1 regression, String/JSON/error redaction |
| `OperationAccountNoNetwork` | CRUD works with probes disabled and test transport/dialer configured to fail on any call; asserts zero DNS/bind/install calls, no task/outbox pretending remote execution |
| `OperationAccountPersistentCRUD` | Real Postgres round-trip/restart, no plaintext in rows/receipts/audit, pair replacement destroys old live ciphertext, tombstone stable, no orphan parent/secret/dependency |
| `OperationAccountCASAndAtomicAudit` | Concurrent writers/deletion/create versus domain deletion, overflow, audit/proof failure rollback, no partial credential or dependency change, deterministic lock order |
| `OperationAccountIdempotency` | Same actor/key/request replay, different payload/kind/actor/tenant conflict/isolation, lost committed response recovery, replay after revision advance, deleted receipt, revoked authority no bypass |
| `OperationAccountPaging` | Missing/defaults/-1 cap/zero/out-of-range/order ties/keyword literal wildcard chars; unsupported status and plaintext switch always explicit failure |
| `OperationAccountSecretSurfaces` | Synthetic sentinel never appears in list/detail, errors, logs/audit, generic task metadata, exports, receipts or persisted browser state; ordinary DTO path never decrypts |
| `OperationAccountDomainDependency` | Registration blocks F01 deletion; account deletion removes only its dependency; foreign/granted domains and concurrent attempts cannot orphan secrets |
| `OperationAccountBrowserLifecycle` | Slow/lost replies, repeated clicks, close/cancel, reload, Back/Forward, expired proof, account changes, stale responses, safe intent recovery, two-editor CAS; API/database values verified |
| `OperationAccountMigration` | Upgrade from finalized F01 schema, repeat/concurrent migration, mismatched API/worker schema fail closed, audit guards/FK/revision triggers and full rollback; no startup/request-time DDL |
| `OperationAccountReferenceUse` (Stage B) | Default deny including builtin admin, explicit exact account/purpose grant, no plaintext copy, no cross-domain binding, distinct revision pins, v1 compatibility policy, revocation/regrant fences, stale consumer denied, detach/delete order, active-use edit/delete conflict |

F01 and access/task/audit regression suites must run after any shared validator, permission, schema, DTO or executor change. New migration/audit producer must preserve F39's historical-schema compatibility and current domain-scoped export authorization. Do not reopen F39 publication as a side effect. The absence of Docker/CI/browser environment is a reported blocker, not a pass; successful mocks or make check cannot substitute for the required database/browser/AD gates.

## 12. Delivery slices and dependency gates

1. **Freeze this local contract and account crypto boundary.** Preserve all source gaps. Finalize F01 baseline and allocate the next schema number. Independently review account AAD, storage ownership, source-mode separation, permissions and audit boundaries
2. **Implement local F03 storage/API/permissions/audit with actual PostgreSQL tests.** No transport or consumer-use registration. Register live domain dependencies and idempotent safe receipts. All four AD-F have partial local evidence only
3. **Implement browser CRUD/recovery with actual browser→API→DB tests.** Validate secret clearance, stale navigation and uncertain completion. This is still not plaintext/source-control/remote parity
4. **Implement explicit reusable-account use contract and optional F01 reference integration**, only after the authorization/binding/detach/task-version design is reviewed. This is a distinct integration requirement; F03 Stage A must not masquerade as it. F05 can consume this safe boundary after its own operation contract is ready
5. **Resolve source semantics and real AD/Windows acceptance.** Obtain pinned reference repositories/commits and authorized eight-version lab evidence. Confirm actual “create/edit/delete” side effects, status/userInfo/plaintext/port/radio semantics and capacity/recovery. Add real consumers without reducing any requirement

Official F03 dependencies stay F43/F45/F46 in the canonical roadmap. This implementation also needs the already chosen F47/F01 domain identity/scope boundary and F39-style atomic immutable audit; task consumers need F48. Record these as discovered technical prerequisites, not silently rewritten roadmap facts. F05's declared dependency on F03 means a local record alone is insufficient for installation: explicit account use, target identity, platform permissions, policy, protocol and remote-effect handling remain required.

Completion reporting must distinguish design, code, local checks, real database/browser, exact-head CI, source parity, Windows/AD, publication and full product acceptance. Keep #17 open and full-product acceptance 0/209 until its actual remaining gates pass. No code, repository document, PR, issue, remote account or external publication was changed by this design task.

## Frozen implementation additions

The safe parent selector is GET /api/operation-accounts/domains with pageIdx, pageSize and filterKeyword. It returns {page,domains:[{domainId,domain,revision}],exhausted} under the same current function/domain authorization. It does not require broader domain-management read access or expose endpoint/credential/diagnostic fields.

All mutation responses and mutation receipts contain result,operation,accountId,domainId,revision,credentialRevision,replayed,verificationState,deleted,currentRevision,currentCredentialRevision. Original revision fields acknowledge the committed action; current fields and deleted describe current local lifecycle. Receipt GET returns {receipt:...}. Revisions are canonical positive decimal strings. The operation-specific encrypted envelope is version2/purpose ADTR/operation-account-credential/v2; existing F01 version1 remains unchanged.

Schema10 introduces the registry and protected operation_account.N audit producer. Every domain-bearing audit source and export snapshot is subject to current exact-domain scope and natural expiry. The operations browser suite uses a key-only runtime with probes disabled and no LDAP fixture; saving a pair performs no remote validation.

Stage B1 account-use governance is now implemented locally under [credential-use-contract.md](credential-use-contract.md), with its consumer disabled pending integrated validation. F01 references/detach, future F05 use and remote account lifecycle remain unimplemented gates. They are not inferred from metadata rights, including for builtin administrators.
