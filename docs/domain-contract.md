# Domain connections contract (F01)

Refs Issue #16, AD-F-001–005. This is a partial implementation contract; real AD/Windows and source-system parity remain unverified.

## 1. Scope and sources

Source coverage: AD-F-001–005, FLD-0001–0095, UI-0380–0410, DTL-001–006. Canonical catalogue maps these features to Issue https://github.com/yunpiao/adtr/issues/16 and source sheet `AD 功能清单`, rows 5–9. Workbook SHA-256 in `requirements/catalog.json`: `d59ccdf3eac3865b20c1f919f2cbc881df625714b2c499342b4cc1bcf2efae54`. The issue extract is reference evidence; the three original source repositories/commits and source runtime have not been supplied or inspected.

Repository contracts read: `AGENTS.md`, README, `docs/delivery.md`, `docs/architecture.md`, `docs/identity-contract.md`, `docs/access-contract.md`, `docs/resource-contract.md`, `docs/task-contract.md`, `docs/tasks-contract.md`, `docs/task-authorization.md`. Implementation inspected: resource schema/store/HTTP/types; access HTTP/types; task authorizer/schema/registry/HTTP/public projection; task registry/types/worker/WithTx; store migrations; existing frontend task API/request-generation helpers.

Deliver the following local slice:

1. Durable tenant-bound domain configuration, encrypted custom credentials, strict add/edit/delete, explicit domain grants, bounded list and detail
2. Secure, fixed-operation LDAP transport and asynchronous revision-fenced diagnostic tasks, exercised only against isolated synthetic protocol/TLS fixtures during this work
3. A browser workflow that explains saved/unverified state, explicit access assignment, latest test state and uncertainty/cancellation accurately
4. A full source-field coverage ledger that keeps unsupported dependent DC/agent/sync/managed-account fields blocked

This development uses synthetic credentials and isolated protocol fixtures; real AD connections, directory mutations and production deployment are outside this validation. No Windows/AD acceptance is claimed. F01 must remain open; full-product acceptance remains 0/209.

## 2. Decisions already frozen by this repository

The existing ADR requires LDAP 389 with successful StartTLS before any simple bind; LDAPS 636 validates certificate chain, configured DNS identity and validity times. Plaintext LDAP and `InsecureSkipVerify` are forbidden. These are not new discretionary interpretations of the old TLS checkbox.

F47 requires explicit role → resource group → exact domain membership for data use, including platform administrators. The tenant policy must exist, be unexpired, have positive capacity, and have no more active catalogue domains than its configured cap. Tenant UID/name are metadata, not verified licensing or customer identity. A diagnostic success neither grants access nor proves a commercial license.

F48 supplies schema gate → tenant → actor → task locking, live authorizer checks, monotonic actor authorization epochs, task leases/fencing, cancellation acknowledgment and atomic module checkpoints. Its `WithTx` callback has a five-second database-operation budget. Never keep that transaction open through a ten-second external probe.

## 3. Domain identity, catalogue ownership and enrollment

### Stable local identity

- Generate an opaque random `domainId` using the existing safe resource-ID alphabet; never use the DNS name as the primary key. Reserve `platform`; caller IDs/tenant/actor/role are rejected
- A connection row has composite identity `(tenant_id, domain_id)` and a composite FK to `resource_domains`. All reads, writes, joins, audit, credentials and tests include both values
- Canonical domain DNS name is lowercase ASCII, 2+ nonempty labels, labels 1–63 characters, total at most 253 characters without a final root dot, letters/digits/hyphen only with no leading/trailing hyphen. Accept and remove exactly one terminal dot; reject whitespace rather than trimming. Unicode names/automatic IDNA conversion, NetBIOS-only names and domain rename remain blocked until their contract is explicit
- Enforce a database unique constraint on `(tenant_id, canonical_domain)` for nondeleted connections, including unverified and failed connections. Case/trailing-dot aliases cannot bypass duplicate detection. Concurrent duplicate creation returns `409 domain_conflict`
- The same DNS string in different tenants never joins their scopes. This does not assert the tenants own distinct real directories
- `resource_domains.active=true` means the local configured resource consumes a slot and can be explicitly granted. It does **not** mean a live AD connection or discovered inventory. Connectivity is a separate observation

### Enrollment authority, accepted for this slice

`platform_admin` plus `domains.writeable` may create local configuration/catalogue rows under the existing tenant lock. Creation performs no DNS lookup, TCP connection, credential bind or resource-grant mutation. Before insert, enforce configured and unexpired tenant policy and `active_count < maxAdCount`. Concurrent last-slot creation and tenant-cap edits serialize on that same lock.

The mutation returns the new ID plus a clear `requiresResourceAssignment:true`. It grants neither creator nor admin implicit access. All ordinary subsequent list/detail/update/delete/test paths require their live domains function grant **and** explicit exact-domain membership. Existing F47 group creation/assignment is how an administrator deliberately grants access.

An interrupted create cannot strand the new ID behind this scope boundary. Use creator-bound idempotent creation receipts: `GET /api/domains/creation?idempotencyKey=...` returns `{receipt:{domainId,domain,revision,deleted,requiresResourceAssignment:true}}` to the same live builtin-admin creator only. It requires domains.readable but no AD resource grant, and exposes no hostname, IP, credential metadata, test result or AD data. Creation requires idempotencyKey; fingerprint the exact canonical nonproof request using `Vault.Fingerprint("domain-create-v1",tenant,canonicalBytes)`. The key scope is tenant+actor+idempotencyKey. Store only the keyed fingerprint and safe receipt, never canonical secret-bearing bytes or an unkeyed secret hash. Same key/same fingerprint returns the original receipt without another capacity reservation or mutation; changed fingerprint is `409 idempotency_conflict`. Receipt lookup resolves uncertain create even after local deletion (deleted=true); it never grants domain access. Current admin/function authorization is still required on replay/lookup. This replaces the earlier enrollment-list proposal.

### Remote identity evidence

The initial diagnostic requires a TLS-authenticated endpoint, successful bind, exactly one bounded rootDSE response, supported LDAPv3, AD DS capability evidence and a parsed default naming context equivalent to the configured domain DN. Construct expected DC RDNs from validated domain labels; parse the returned DN with escaping and case-insensitive attribute-name handling, never naive comma splitting or a raw string-prefix match. Referrals never alter this identity.

Keep local registration, verified DNS/domain naming context, and immutable directory identity distinct. RootDSE/hostname alone does not pin a domain object GUID/SID and cannot distinguish a later replacement domain with the same DNS name. Before any future inventory/sync consumer claims durable AD identity, add an authorized, bounded base read of the domain NC's `objectGUID`/`objectSid`, freeze representation and pin it; mismatches require explicit re-enrollment. That additional read and identity policy are outside the rootDSE-only transport slice. No future sync consumer may treat a rootDSE-only success as its complete identity gate.

## 4. Storage model and revision rules

Add a versioned migration after schema 8; never mutate schema in API/worker readiness. Use the next actual schema number only after confirming concurrent development has not allocated it.

Suggested tables:

`domain_connections`: tenant_id, domain_id, canonical_domain, dc_hostname (TLS reference DNS name), dial_ip nullable, transport_mode (`starttls`/`ldaps`), port constrained to the matching 389/636, connection_revision positive bigint, credential_revision positive bigint, credential_mode (`custom` only), created_at/updated_at, deleted_at nullable, latest_test_task_id nullable, diagnostic_generation positive bigint. FK to catalogue; unique live canonical domain; reject direct local identity reassignment. Catalogue name remains canonical domain, not an independently editable alias.

`domain_credentials`: tenant_id, domain_id, credential_revision, key_id, ciphertext, created_at. Composite FK to connection. Store the complete username/password pair in authenticated encryption; normal query projection never selects ciphertext/key material. No plaintext shadow column, searchable password hash, last-four password preview or recover-password endpoint. Credential username display is intentionally omitted in this first slice rather than decrypting credentials into generic list responses.

`domain_diagnostics`: tenant_id, domain_id, task_id, connection_revision, credential_revision, policy_revision, diagnostic_generation, stage, code, successful_observation boolean, dc_hostname nullable, naming_context nullable, observed_at, elapsed_ms. This contains typed allowlisted data only. No raw LDAP packets/diagnostic strings/certificate dumps. The task ID and exact revisions provide provenance. At most one immutable result row per task; no overwriting an old result in place.

`domain_audit` or an appropriate new F39 producer: safe action, object ID, old/new revision numbers, actor/tenant, result, task ID/correlation ID and timestamp. No submitted body, username/password, ciphertext, key contents, authorization header, DSN or raw network error. Configuration mutation, proof consumption and audit commit atomically; audit failure rolls all back.

Revision semantics:

- New configuration and credential revisions start at 1, are server-generated positive decimal strings in JSON, and never reset/reuse within an identity. Strings avoid JavaScript precision loss
- Edit/delete require exact `expectedRevision`. SQL compare-and-swap returns `409 revision_conflict`; no last-writer-wins or automatic mutation retry
- Any endpoint/mode/IP/credential change increments connection revision and clears the current verification observation. Credential replacement also increments credential revision and seals with new AAD. Trust/egress changes have a deployment-policy digest, independently pinned by tasks
- A no-op edit returns `409 no_change`; entering identical credential bytes may be treated as an explicit rotation and creates a new revision without comparing passwords
- Every meaningful configuration/credential/lifecycle change invalidates affected durable actor authorization epochs under the existing tenant lock. Conservative tenant-wide invalidation is acceptable for this slice, documented and tested; no need to invent cached permission state
- Add a domains-grant epoch trigger. Revoke/regrant of the domains function mark must invalidate old diagnostic tasks even when the final function/domain grants match their submission state
- A diagnostic task pins connection revision, credential revision and deployment-policy revision. Completion rechecks all of them under `WithTx`, in addition to the engine's epoch, owner, fence, live lease, state and result-version checks. Restoring previous endpoint values cannot make the old revision current again
- Each dedicated test submission also advances `diagnostic_generation` and records its task ID. An older test finishing later cannot replace the UI's latest requested observation; use generation/task-ID CAS
- Store observations as staged immutable evidence; expose a successful last test only when the associated task actually reached `succeeded`, revisions/policy match and the querying user passes current domain authorization. A checkpoint before task Finish is not a published terminal success

### Delete

Delete is local disconnection, not deletion of directory objects, account revocation, agent uninstall or removal of remote software. Soft-delete the configuration/tombstone its ID, mark catalogue inactive, remove domain resource memberships and destroy the live credential ciphertext in the same audited transaction. Increment revisions/epochs so workers cannot publish; existing tasks/history/audit remain for their retention contract. Do not falsely claim their external connections are already stopped. A later create uses a new ID and no previous grants; no tombstone restoration endpoint is included.

Do not advertise physical erasure of backup copies or guaranteed cryptographic erasure under a still-shared encryption key. Future retention/recovery and deleting a domain used by other actual modules require explicit dependency policies; those modules do not exist in this slice. If a live unsupported dependency is found, reject `409 domain_in_use` rather than cascading unknown business data.

## 5. Deployment policy and credential vault

These are operator safeguards, separate from source UI fields. This development does not configure actual credentials or authorize a real target.

Suggested standalone package `internal/domainconfig`, without auth/task/DB dependencies:

```
Load(getenv func(string) string) (*Runtime, error)
Vault.Seal(tenantID, domainID string, credentialRevision int64, plaintext []byte) (SealedCredential, error)
Vault.Open(tenantID, domainID string, credentialRevision int64, sealed SealedCredential) ([]byte, error)
SealedCredential { KeyID string; Ciphertext []byte }
Policy.AuthorizeTarget(tenantID, domain, serverName string, dialIP netip.Addr) ([]netip.Prefix, error)
Policy.Revision() string
Policy.Roots() *x509.CertPool
```

Frozen Runtime API: `Enabled() bool`, `ProbeEnabled() bool`, `Vault() *Vault`, `Policy() *Policy`; Vault additionally provides `Fingerprint(purpose, tenant string, canonicalBytes []byte) (string,error)`. `SealedCredential.Ciphertext` is []byte and persists as BYTEA. Policy exposes `Revision() string` and `Roots()` returning a cloned certificate pool. Runtime stores immutable validated configuration and provides safe availability flags and a policy digest. Keys/plaintext never get JSON/Stringer methods. Error strings are fixed codes; parser errors must not echo file contents or environment values.

Environment:

- `ADTR_DOMAIN_KEY_ID`: 1–64 `[A-Za-z0-9_.-]`, required with key
- `ADTR_DOMAIN_KEY`: exactly 32 decoded bytes from base64, separately managed, never copied from/fallback to `ADTR_AUTH_KEY`. Absent pair disables vault operations; malformed/partial pair fails startup. Never generate a replacement at restart
- `ADTR_LDAP_CA_FILE`: immutable startup-loaded explicit trust bundle, max 1 MiB, valid PEM certificates, CA constraints validated, reject unsupported/trailing PEM blocks. No request-selected path, arbitrary file read, trust-on-first-use or silent system-root fallback
- `ADTR_LDAP_EGRESS_POLICY_FILE`: immutable startup-loaded strict JSON, max 1 MiB, no unknown/duplicate/null fields
- `ADTR_DOMAIN_PROBE_ENABLED`: false by default; true requires usable vault, explicit valid CA, and nonempty valid egress policy. A false value blocks all production network probes with `503 domain_probe_disabled`, even if an API request is otherwise authorized

Egress-policy shape:

```
{"version":1,"targets":[{"tenantId":"tenant-a","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/24"]}]}
```

Limits are local implementation limits: at most 1,000 unique tenant/domain records, 32 exact DNS identities and 32 CIDRs per record. Reject wildcard names, duplicate tuples, invalid networks and unusably broad special-address allowances. Every configured hostname must match an exact operator-approved identity for that tenant/domain; there is no suffix-only wildcard.

The optional dial IP must be a single literal IPv4/IPv6 address, no zone, URL, port, credentials or DNS label. Unmap IPv4-mapped IPv6 before checks. Always reject unspecified, loopback, link-local, multicast and broadcast targets. Private unicast addresses are allowed only when explicitly covered by that tenant/domain's CIDRs. Reject known special metadata ranges via special-address exclusions. A public address is not automatically safe; it also needs exact name and CIDR approval.

The accepted package boundary has Policy.AuthorizeTarget return the exact allowed prefixes. The LDAP adapter owns the one DNS resolution. When DNS is needed, authorize tenant/domain/name before lookup, resolve once with a bound, reject more than 16 answers or any denied answer, and pin one allowed numeric destination. Do not feed the hostname back to a dialing function that resolves it again. Do not reconnect to an alternate destination after sending credentials. Operator-configured DNS resolution is used; source `dns` is not silently turned into a caller-selected resolver. Test-only injected transports/net.Pipe do not install a production bypass flag.

`Policy.Revision` is SHA-256 over an unambiguous versioned canonical encoding of the complete egress policy and CA bundle bytes. Any trust or route-policy change makes a different revision. Persist the submission revision; an executor with a different current revision fails closed as `policy_revision_changed`. No hot-reload source of mutable trust during a handshake. Mixed-revision worker fleets fail closed rather than silently executing under a different policy; controlled rollout is an operational gate.

Vault uses AES-256-GCM with a fresh random nonce per seal, prefixed in ciphertext. AAD is an unambiguous versioned encoding of application purpose, tenant, domain ID and credential revision; delimiter concatenation that could collide is forbidden. Changing any identity/revision, key, nonce or ciphertext must fail authentication. Credential plaintext encoding is strict UTF-8 structured data with username/password and a version; never accept arbitrary executable/config payloads.

Use only minimal memory lifetime; clear owned byte buffers best-effort after use. Do not claim guaranteed memory zeroization in Go or in the browser. Secrets never enter task payload/result/cursor, audit, URLs, log/metric labels, tracing, frontend persistence or exported diagnostics. Do not log failed JSON bodies. Data received in ordinary HTTP mutation bodies needs HTTPS outside the existing explicit local development origin.

Initial key handling supports a single externally managed key ID. A mismatched historical key returns `credential_key_unavailable`, without calling it invalid AD credentials or marking a connection healthy. Operational key rotation/rewrapping, keyring/KMS integration, recovery and backup retention remain explicit release gates; do not silently rewrite key IDs or reuse credential revision under different secret material. Missing key does not prevent authorized metadata reads, but create/credential replacement/test fail closed.

## 6. Backend API contract

Add closed function mark `domains`. Existing/custom roles get no implicit grant; builtin administrator has its usual function-management grant but no AD data wildcard. Register exact routes in both enforcement and `/api/access/check`/menu metadata. Extend all permission editor round-tripping and DB enum constraints.

Every business route requires the live cookie, current non-forced/nonexpired identity, exact tenant and live grants. All POSTs require exact trusted Origin, JSON, CSRF, current actor password and fresh unused TOTP. Strict exact key names, duplicate-key rejection including nested values, non-null supplied fields, valid UTF-8, a bounded 32 KiB request body, no trailing JSON and no identity assertions. Malformed requests fail before DB/proof writes. Check compiled schema compatibility before mutation/proof/audit/queue writes, following the task schema gate.

Proposed local route/DTO names deliberately do not claim wire compatibility with legacy handlers:

| Route | Request and result |
| --- | --- |
| GET `/api/domains` | `pageIdx`, `pageSize`, repeated `filterDomain`, repeated `filterStatus`, `filterKeyword`, `tmSort`; result `{page,List,exhausted}` |
| GET `/api/domains/detail?domainId=...` | `{connection:ConnectionDetail}`; missing/foreign/ungranted/tombstoned IDs all 404 |
| GET `/api/domains/creation?idempotencyKey=...` | Creator-bound local receipt recovery as section 3; `{receipt:{domainId,domain,revision,deleted,requiresResourceAssignment:true}}`, no AD data |
| POST `/api/domains/create` | `domain`, `dcHostName`, optional `ldapAddr`, `port`, `username`, `password`, `idempotencyKey`, proof; `{result:"SUCCESS",domainId,revision,requiresResourceAssignment:true,replayed}` after durable commit |
| POST `/api/domains/update` | `domainId`, `expectedRevision`, required complete nonsecret endpoint values `dcHostName`, optional `ldapAddr`, `port`; optional replacement `username`+`password` pair; proof; `{result:"SUCCESS",domainId,revision}` |
| POST `/api/domains/delete` | `domainId`, `expectedRevision`, `confirmDomain` matching canonical name, proof; `{result:"SUCCESS",domainId}` after local disconnection commits |
| POST `/api/domains/test` | `domainId`, `expectedRevision`, `idempotencyKey`, proof; `{task,replayed}`. Endpoint/credential/trust details come only from saved configuration and deployment policy |
| GET `/api/domains/test-result?taskUUID=...` | Current domain read permission plus normal task visibility checks; `{task,diagnostic}` where diagnostic may be null until terminal |
| POST `/api/tasks/cancel` | Existing API, taskUUID + fresh proof; additionally enforce the domains write grant/exact domain for this kind |

Do not accept legacy `ID`, `Name`, `server`, `isSystem`, actor, tenant, arbitrary mode flags or test-target overrides as accidental aliases. Document migration mapping in source coverage. A dedicated compatibility endpoint may be added only with its own exact contract.

List semantics: pageIdx default 1, 1–1,000,000; pageSize default 20, 1–100 or -1 only at page 1 with a 1,000-result cap (`422 result_too_large`). UI choices 10/20/30/40/50. Scope filtering occurs before count/order/page. Zero results: totalPage=0, List=[], exhausted=true. Out-of-range pages return empty list with true total. Unknown queries or repeated scalar keys reject. Filters OR within the same field and AND between fields; at most 100 distinct normalized domains and four distinct supported connection-status values. `filterKeyword` is literal case-insensitive substring of canonical domain/configured DC DNS name only, at most 50 Unicode codepoints; `%` and `_` have no wildcard behavior. Do not search encrypted usernames. tmSort absent/0/-1 means descending createdAt; 1 ascending; ID is deterministic tie-breaker. These choices are explicit local decisions, not proven source runtime behavior.

Endpoint validation: dcHostName follows canonical FQDN grammar and is required even with dial IP; source `dcHostName` ambiguity does not justify skipping TLS name verification. `ldapAddr` omitted/empty means use approved DNS; supplied null/whitespace/URL/host:port rejects. Port is the exact source-shaped string `"389"` or `"636"`; derive immutable mode `starttls`/`ldaps` rather than accept contradictory mode+port fields. Creation UI defaults to 389 and labels it “LDAP + StartTLS”; port must be explicitly submitted. No arbitrary port or global-catalog 3268/3269 support is claimed.

Credential validation: only custom credentials. Provisional local bounds are 1–50 Unicode codepoints and at most 200 UTF-8 bytes for username/password, applying the observed UI maximum on the server; do not apply the platform's 12-character password policy to an existing AD account. Preserve password bytes exactly, reject invalid UTF-8/NUL, never trim/normalize. Username disallows controls/edge whitespace; require unambiguous `DOMAIN\\user` or `user@dns-domain` syntax without inventing a same-UPN-suffix restriction (alternate UPN suffixes may exist). Anonymous/empty-password bind is rejected. Supported exact account-name forms/lengths still need real AD evidence.

Because credentials are not returned, an update omitting both username and password preserves the stored pair. Supplying only one, null, or an empty value rejects. Replace UI requires entering both fields. A string of asterisks has no “keep existing” semantics. `isSystem:true`/managed credentials are `422 unsupported_credential_mode` only if a deliberately supported compatibility field is later introduced; otherwise unknown-key validation applies. No managed account creation or rotation is implemented.

Public `ConnectionSummary`/`ConnectionDetail` contains ID, domain, dcHostName, optional configured dial IP, port, mode, revision, credentialRevision, credentialConfigured, createdAt/updatedAt and `lastDiagnostic` with safe code/stage/time/elapsed/task ID only. Never username/password/ciphertext/key material. Include `latestTaskUUID` (empty if absent) for exact in-progress polling and `connectionState` in `unverified|testing|verified|error`; verified explicitly means latest test succeeded at its timestamp, not continuous monitoring, sync readiness or user administrative privilege. Avoid reusing ambiguous source `run/stop/init/error` as factual runtime states.

Errors: malformed 400; unauthenticated 401; missing function/tenant policy eligibility 403 (or documented existing `tenant_not_configured` 409); absent/foreign/ungranted object 404; conflict/no change/domain duplicate/capacity 409; bounded-all too large or unsupported mode 422; rate limit 429; safe internal 500; key/policy/schema/probe unavailable 503. Do not turn DB outages into durable permission failures. A failed diagnostic is a failed task with safe error code, not `200 result:success`.

## 7. Diagnostic task admission and execution

Kind `domain.connection_test`, payload version 1, domain scope, `SingleAttemptOnly:true`, `ReplaySafe:false`, unschedulable, maximum attempts 1, retry-code allowlist empty, lease 30s, heartbeat 1s, execution deadline 10s (clamped by parent). RetryBase/RetryCap may use required positive registry placeholders but never authorize a retry. Invalid credentials/certificates never retry. A simple bind can affect AD lockout counters/audit; “read-only” here means no directory writes, not zero server side effects. Lost/interrupted attempts are not replayed automatically. The accepted core extension rejects generic RecoverTx for SingleAttemptOnly; lost non-replay-safe execution is `failed` with `executor_lost`, preserving uncertainty (not a claim of cancellation and not dead_letter). Manual resubmission needs new explicit proof and a new idempotency key.

Canonical task payload: `{connectionRevision:"...",credentialRevision:"...",policyRevision:"...",diagnosticGeneration:"..."}`; exact keys and canonical decimal strings. Domain identity comes from trusted task scope. No password, credential ciphertext, username, IP, DNS target, mode, CA path, timeout or retry setting is accepted in the payload.

Dedicated admission transaction: schema gate, live identity/function/data authorization, deployment availability, current expected config revision and fresh proof; record a submission intent/task audit/outbox and pin revisions atomically. Required function grants: `domains.readable/writeable` plus `tasks.readable/writeable`. `/api/tasks/submit` and `/recover` reject this kind with `domain_route_required`; scheduler does not offer it. Generic cancel/list/detail still recheck domains function and exact-domain permissions. Remove the kind from generic payload editors or show that it belongs in the domain page.

Same-key replay must be bound to tenant/domain/kind **and original actor** with identical pinned config/credential/policy values. Return the original task without advancing diagnostic generation again or creating another bind attempt. Changed revision/key payload yields `409 idempotency_conflict`/`revision_conflict`, not a silent fresh task. Verify current authorization on replay; an old key is no permission token. Do not use a new key automatically after uncertain HTTP outcome. A stored secret is referenced by revision only and never becomes part of an idempotency hash.

Worker sequence:

1. Existing Claim/Start performs durable actor/epoch/scope checks before any DNS/network or credential decryption. Extend the authorizer for domains grants and explicit domain kind allowlist
2. Short `Execution.WithTx`: recheck connection existence/lifecycle/revisions, policy revision and generation; select encrypted credential into worker-owned memory. Let this commit before network work. Preserve returned task resultVersion
3. Decrypt only with exact identity/revision AAD. Validate the structured credential and target policy. Zero bind calls if any check fails
4. Run secure probe with execution cancellation context. Before dial/bind/rootDSE phases, a trusted executor callback may perform short fresh revision/authorization checks; no HTTP field can supply/disable that callback. No DB transaction spans network I/O
5. Close/join the transport before recording its outcome. Clear plaintext buffers best-effort
6. Short `WithTx` rechecks current config/credential/policy/generation and engine epoch/fence/lease/resultVersion before writing safe immutable diagnostic evidence. Reject stale publication; never overwrite a newer config/test
7. Return terminal Outcome. The existing Finish path rechecks current authority and fencing. Domain UI derives the published latest test from terminal task state and matching evidence, not from a staged checkpoint alone

Revocation during network work is observed through phase checks and heartbeat cancellation. This design cannot promise that bytes already in flight are recalled or that no packet occurs in the narrow interval after an authorization transaction releases its lock. Document the bounded cancellation model; do not claim external exactly-once effects or instantaneous revocation. If policy requires a strict linearization point for an external write, this read-only probe design is insufficient and the write remains unsupported.

## 8. Secure transport contract

Package `internal/ldapconnection` owns no identity/DB/task persistence and never selects a target from HTTP:

```
Probe(ctx context.Context, cfg Config, credential Credential) (Result, error)
Config: Mode (StartTLS|LDAPS), ServerName, approved/pinned DialIP, Roots
Credential: Username string, Password []byte
Result: DCHostName, DefaultNamingContext, SupportedCapabilities,
        SupportedLDAPVersions, ElapsedMilliseconds
```

The adapter receives the exact approved prefixes from domainconfig and owns the sole DNS resolution when no IP is configured. It checks every answer, pins a numeric destination, and never resolves again while dialing. A test-only dial constructor can supply net.Pipe or an owned local fixture; production public input cannot select it. A trusted phase authorization callback, if added, is internal wiring and returns fixed failure classifications only.

- One connection; no separate TCP preflight or retry/failover loop. Total DNS+dial budget 2s, whole operation 10s, TLS/bind/search phase deadlines at most 3s each and clamped to remaining total
- StartTLS on 389: send only the fixed StartTLS extended request first; verify successful matching extended response; then complete a verified TLS handshake. Refusal, malformed response or handshake failure closes the connection; never bind in plaintext
- LDAPS on 636: TLS handshake is the first protocol operation, then bind
- TLS minimum 1.2; normal chain/path, DNS SAN, validity and server-auth usage checks. The configured DNS identity is used even when dialing an IP. No common-name-only compatibility override, insecure callback or TLS downgrade for old OS versions
- Use one nonempty LDAPv3 simple bind after verified TLS. No anonymous success; no SASL/Kerberos/NTLM/channel-binding support claim
- Read only rootDSE: base `""`, base scope, no alias dereference, fixed filter `(objectClass=*)`, sizeLimit 1, bounded time limit, exact attributes `dnsHostName`, `defaultNamingContext`, `supportedCapabilities`, `supportedLDAPVersion`
- BER parser must validate message ID/opcode/length bounds and reject indefinite lengths, malformed encodings, unexpected controls/messages/attributes and excessive response counts before allocation. Example local limits: at most 64 KiB total rootDSE response, 8 attributes, 64 total attribute values, 4 KiB each string. A fixed-operation parser needs explicit protocol tests; do not describe it as a general LDAP client
- Require exactly one rootDSE entry followed by success. Reject search referrals and referral result codes at every stage; never follow referral URLs or echo them. A result with data followed by error is failure, not partial success
- Callback/parent cancellation immediately closes the underlying net.Conn, interrupts blocked TLS/LDAP reads/writes and waits for every owned goroutine to return. There must be no detached “timeout wrapper” goroutine continuing a bind after Probe returned
- Set socket deadlines independently of library-level LDAP time limits. Do not rely on LDAP Abandon as proof of connection termination

Typed error carries fixed `Code` and `Stage`. Suggested codes: invalid_config, dns_failed, destination_denied, connect_failed, connection_timeout, tls_untrusted, tls_hostname, tls_expired, tls_failed, starttls_required, credentials_rejected, ldap_access_denied, ldap_timeout, referral_rejected, invalid_response, directory_mismatch, cancelled, authorization_revoked, connection_revision_changed, policy_revision_changed. Stages: authorization, resolve, connect, tls, bind, rootdse, identity, publish. Raw error text/diagnosticMessage/matchedDN/Referral/certificate identity never enters API/task/audit/log outputs. Preserve safe classification for fixed UI messages, not server-provided instructions.

## 9. Frontend behavior

Add a server-menu-controlled Domains workspace, following existing generation-token/AbortController patterns. Strictly validate returned DTO identity/revision/status before displaying it. React rendering escapes server text; no HTML from diagnostics.

List: show canonical domain, configured DC, mode/port, connection-state label, last tested time and safe error description. Search/reset/filter/page are synchronized with stable state. Empty list distinguishes “no granted connections” from an API error; do not invent discovery counts. Creation entry is shown only when its route check permits it. Creator receipt recovery metadata is clearly labeled a local registration awaiting assignment.

Create: domain/DC DNS/optional IP/389 StartTLS or 636 LDAPS/custom username/password, clear length/error feedback and fresh proof. Default 389 label makes encryption explicit. Save only persists configuration. After success show the new ID and the existing resource-group assignment step; never label it connected or start a probe automatically. Clear credentials/proof on successful submit, dismissal, route/account changes and reload. Do not put them in URL, history, browser storage or pending-intent persistence.

Edit: show safe endpoint values and saved-credential indicator, no masked value populated from storage. A distinct Replace credentials toggle exposes two empty inputs. Both omitted means preserve; both entered means replacement. Refresh mismatch yields a visible conflict; user must review current values before resubmitting. Any change makes previous diagnostic stale. Managed-account option is absent/disabled with an accurate unavailable label, not a functional radio that submits an empty success.

Test: saved configuration only, show exact revision and mode; require fresh proof and confirm user intent. Single-click lock prevents duplicate submissions. Retain only nonsecret intent `{domainId,expectedRevision,idempotencyKey}` for uncertain network outcomes, scoped to the current account, as existing task UI patterns permit. Poll while nonterminal; show queued/running/cancel_requested separately. Stopping polling/navigation/closing a dialog is not server cancellation. Cancel invokes the existing authenticated fresh-proof API; show “cancellation requested” until confirmed terminal state. Late successful finish is shown accurately with its task event, not overwritten by optimistic Cancelled.

Diagnostics: friendly code-specific messages and timestamp; no raw error dumps. Distinguish rejected credentials, certificate/hostname/trust errors, network timeout, target-policy denial, revision conflict and unavailable deployment configuration. “Verified at …” means a historical bind/rootDSE observation only. No auto-repeat on failure; credentials/certificate failures explicitly require a correction before new manual submission.

Delete: review canonical domain and type it for confirmation; include expected revision and fresh proof. Explain that the local connection/grants are removed and remote AD remains unchanged. Refresh list after durable success. A network failure is uncertain; re-read scoped state or safe creation receipt instead of blind repeat. Concurrent delete/edit/test updates cannot revive the old view through stale promises.

UI verification must cover repeated clicks, slow/lost replies, close/cancel, route changes, Back/Forward, account changes, expired login during proof entry, stale list/detail responses, two editors, pending diagnostic across refresh and actual browser→API→database persistence.

## 10. Exact source-field coverage and unresolved semantics

Status vocabulary below: **local** means a concrete proposed mapping exists; **changed** is an intentional local/security behavior difference; **blocked** means this slice does not implement that source field. None means source acceptance passed.

| Source record(s) | Source field(s) | Proposed mapping/status |
| --- | --- | --- |
| FLD-0001 | pageIdx | local bounded one-based list page |
| FLD-0002 | pageSize | local bounded 1–100/-1; source all-results capacity remains unproven |
| FLD-0003 | filterDomain | local repeated normalized DNS exact matches; OR semantics explicitly local |
| FLD-0004 | filterStatus | changed: local unverified/testing/verified/error; legacy run/stop/init meanings blocked |
| FLD-0005 | filterKeyword | local ≤50-rune literal domain/DC search; encrypted username search not implemented |
| FLD-0006 | tmSort | local -1/0 descending, 1 ascending, deterministic ID tie-break; source 0/default behavior unresolved |
| FLD-0007 | page | local page object |
| FLD-0008 | page.pageIdx | local actual requested page |
| FLD-0009 | page.pageSize | local actual validated page size, including -1 |
| FLD-0010 | page.total | local count after exact resource authorization and filters |
| FLD-0011 | page.totalPage | local ceiling count, 0 when empty |
| FLD-0012 | List | local safe persisted connection DTO array, [] empty |
| FLD-0013 | List[].ID | changed wire spelling to domainId; opaque stable local identity |
| FLD-0014 | List[].name | local canonical domain; independent data-source display-name semantics blocked |
| FLD-0015 | List[].dcHostname | local configured TLS DNS identity; discovered DC hostname is separately observed |
| FLD-0016 | List[].status | changed connectionState; no fabricated run/stop collector state |
| FLD-0017 | List[].domainInfo | changed typed allowlisted metadata; no arbitrary map/ciphertext/password |
| FLD-0018 | List[].createTm | changed createdAt, durable UTC RFC3339 |
| FLD-0019 | List[].errMsg | changed fixed safe diagnostic code + UI message |
| FLD-0020 | List[].port | local exact string 389/636, matching explicit mode |
| FLD-0021 | List[].isAdmin | blocked: bind success cannot infer AD administrative privilege; platform role is unrelated |
| FLD-0022 | List[].isLastAgent | blocked: no agent lifecycle/inventory source |
| FLD-0023 | List[].system_user_name | blocked: managed credentials not implemented; no credential-name exposure |
| FLD-0024 | exhausted | local computed from filtered count and returned page |
| FLD-0025 | domainID | changed exact local domainId; no case-insensitive aliases |
| FLD-0026 | PageSize | blocked DC-inventory paging; connection detail is not an invented DC table |
| FLD-0027 | PageIdx | blocked DC-inventory paging |
| FLD-0028 | Sort | blocked generic DC sort/default precedence |
| FLD-0029 | domainSort | blocked DC field sort |
| FLD-0030 | ipSort | blocked DC field sort |
| FLD-0031 | agentSort | blocked agent inventory sort |
| FLD-0032 | statusSort | blocked DC inventory status sort |
| FLD-0033 | systemSort | blocked OS inventory sort |
| FLD-0034 | domainRoleSort | blocked DC role sort |
| FLD-0035 | assetsActiveStatusSort | blocked asset inventory sort |
| FLD-0036 | isProxySort | blocked proxy inventory sort |
| FLD-0037 | Keyword | blocked DC search; uppercase alias not accepted by local API |
| FLD-0038 | dcList | blocked full inventory; a configured endpoint is not discovered DC coverage |
| FLD-0039 | dcList[].dcHostName | partial observation via last diagnostic only; inventory table blocked |
| FLD-0040 | dcList[].ip | blocked discovered IP list; configured dial IP is distinguished |
| FLD-0041 | dcList[].status | blocked DC monitoring semantics |
| FLD-0042 | dcList[].winRMStatus | blocked; no WinRM connection |
| FLD-0043 | dcList[].errMsg | blocked DC inventory error; connection diagnostic uses safe local codes |
| FLD-0044 | dcList[].platform | blocked OS discovery |
| FLD-0045 | dcList[].isPullLog | blocked collection configuration |
| FLD-0046 | dcList[].timeout | changed diagnostic elapsedMilliseconds only; not asserted network latency |
| FLD-0047 | dcList[].LastOnlineTm | changed last tested timestamp only; not last-online telemetry |
| FLD-0048 | dcList[].hasAgent | blocked 0/1/2 lifecycle; unknown never returned as 0 |
| FLD-0049 | dcList[].domainRole | blocked FSMO/DC role discovery |
| FLD-0050 | dcList[].errWinRMsg | blocked WinRM error |
| FLD-0051 | dcList[].assetsActiveStatus | blocked active-asset semantics; unknown never false |
| FLD-0052 | dcList[].isProxy | blocked agent proxy state |
| FLD-0053 | dcList[].proxyStatus | blocked proxy health |
| FLD-0054 | dns | blocked source resolver/address meaning; operator resolver not exposed as this value |
| FLD-0055 | createTime | local persisted connection creation time; wire createdAt |
| FLD-0056 | total | blocked DC count; do not substitute configured endpoint count |
| FLD-0057 | domain | local canonical configured domain, separately verified naming context |
| FLD-0058 | activeDeployStatus | blocked asset sync |
| FLD-0059 | groupDeployStatus | blocked GPO deployment |
| FLD-0060 | SACLDeployStatus | blocked SACL deployment |
| FLD-0061 | domainInfo | changed typed safe metadata; credential map never returned |
| FLD-0062 | activeDeploySchedule | blocked sync progress |
| FLD-0063 | aclSwitch | blocked ACL automation |
| FLD-0064 | aclActiveDeploySchedule | blocked ACL asset sync progress |
| FLD-0065 | userHashSyncStatus | blocked password-hash synchronization |
| FLD-0066 | aclDeployStatus | blocked ACL deployment |
| FLD-0067 | systemUserName | blocked managed username; custom credential remains encrypted/private |
| FLD-0068 | isSystem | changed fixed custom mode; system-managed mode blocked |
| FLD-0069 | ldapAddr | local optional literal dial IP; malformed legacy regex rejected, IPv6 is an explicit extension |
| FLD-0070 | domain | local immutable canonical DNS name; rename semantics blocked |
| FLD-0071 | username | local write-only custom username with provisional server bounds |
| FLD-0072 | password | local write-only secret; durable AEAD pair, never echoed |
| FLD-0073 | port | local exact string 389/636, derived secure mode |
| FLD-0074 | dcHostName | local required certificate reference DNS identity; source optional/default semantics unresolved |
| FLD-0075 | result | changed local uppercase SUCCESS only after durable commit |
| FLD-0076 | ID | changed domainId, session-tenant-bound opaque lookup |
| FLD-0077 | username | changed replace pair only; omitted pair preserves |
| FLD-0078 | password | changed omission preserves; empty/null rejected; no mask sentinel |
| FLD-0079 | port | local secure pair; change invalidates verification |
| FLD-0080 | server | changed explicit ldapAddr field; source target selection semantics unresolved |
| FLD-0081 | isSystem | blocked managed mode; no false managed-account creation success |
| FLD-0082 | result | changed audited local SUCCESS after revision CAS |
| FLD-0083 | msg | changed fixed safe UI message; raw source text not echoed |
| FLD-0084 | ID | changed domainId + expectedRevision |
| FLD-0085 | Name | changed confirmDomain assertion only, never target authority |
| FLD-0086 | agentStatus | blocked/rejected deprecated field; no uninstall effect |
| FLD-0087 | result | changed local SUCCESS after atomic disconnection/tombstone |
| FLD-0088 | ldapAddr | changed saved config only; no ad hoc arbitrary-target probe |
| FLD-0089 | domain | changed authenticated saved domainId scope, no independent request domain override |
| FLD-0090 | username | changed saved encrypted credential reference only |
| FLD-0091 | password | changed saved encrypted credential reference only; never task payload |
| FLD-0092 | port | changed saved configuration revision only |
| FLD-0093 | status | changed async task terminal state; no immediate legacy numeric 1/0 response |
| FLD-0094 | msg | changed typed safe diagnostic code/stage |
| FLD-0095 | dcHostName | local successful rootDSE observation, bounded and validated |

UI coverage (these 31 source UI rows are repeated across all five feature sections; that repetition does not mean each control belongs on each operation):

| Source controls | Mapping and gaps |
| --- | --- |
| UI-0380–0381 | Managed/custom radio semantics not guessed. Custom mode supported; managed creation/rotation blocked |
| UI-0382–0383 | Configured optional dial-IP input is local; discovered serverOptions selection blocked |
| UI-0384 | Write-only username input, same provisional 50-codepoint cap server/client |
| UI-0385 | Changed explicit 389 StartTLS / 636 LDAPS selection; misleading “用户名” TLS label not reproduced |
| UI-0386 | Empty password input, 50-codepoint cap, no readback/mask persistence |
| UI-0387–0388 | Sensor/GPO WinRM options blocked |
| UI-0389 | Changed literal domain/DC search; encrypted username search blocked |
| UI-0390 | Paged connection row index from actual page offset |
| UI-0391 | Canonical domain/data-source label, no fabricated alternate name |
| UI-0392 | Username column omitted; credential-present indicator instead |
| UI-0393 | Changed last diagnostic/configuration state, not collector run/stop |
| UI-0394 | Durable creation time in UTC |
| UI-0395 | Original unlabeled slot/action meaning unresolved; local authorized detail/edit/test/delete controls documented separately |
| UI-0396 | Actual list paging 10/20/30/40/50, backend bound remains 100 |
| UI-0397 | ACL autosync disabled/unimplemented, not a toggle that does nothing |
| UI-0398 | DC-inventory keyword search blocked |
| UI-0399 | DC-inventory row index blocked |
| UI-0400 | Discovered DC IP column blocked; configured endpoint displayed elsewhere |
| UI-0401 | DC role column blocked |
| UI-0402 | RootDSE hostname observable; full DC table blocked |
| UI-0403 | Asset active state blocked |
| UI-0404 | Agent deployment state blocked |
| UI-0405–0406 | Proxy status columns blocked |
| UI-0407 | OS column blocked |
| UI-0408 | DC-inventory paging blocked |
| UI-0409–0410 | SACL sensor/WinRM options blocked |

DTL coverage: DTL-001 freezes only 389+StartTLS/636+LDAPS; DTL-002 strict certificate verification; DTL-003 durable local config with explicit duplicate/permission failure, while real synchronization consumption stays blocked; DTL-004 custom credential lifecycle/server lengths implemented locally, managed service identity/least privilege/rotation blocked; DTL-005 no automatic retry and cancellation resource release; DTL-006 explicit deployment trust/egress/timeouts and secure mode mapping, arbitrary custom ports/plaintext mode intentionally unsupported.

Remaining semantic decisions requiring source/product/lab evidence: actual legacy state meanings and transitions; original list keyword fields and multi-sort precedence; domain display-name/default identity rules; optional dcHostName/server selection; source credential forms/maximums/empty edit behavior; managed account ownership/creation/minimum AD permissions/rotation; delete dependencies and last-agent behavior; `dns` meaning; full DC/OS/agent/GPO/ACL/SACL/hash-sync data; capacity/performance/pagination guarantees; immutable AD identity pinning; actual certificate revocation policy (standard Go verification does not by itself implement online CRL/OCSP checks); TLS compatibility of Windows 2008/2008R2 and each patched target.

## 11. Verification gates and evidence labels

No code or tests were changed/run by this design task. The following are required planned checks, not pass claims:

1. Unit/HTTP validation: all field valid/absent/empty/null/illegal cases; exact key/case/duplicate rejection; bounded pagination and literal search; fixed diagnostic mapping; no secret serialization; malformed deployment key/policy/CA
2. Vault: nonce uniqueness, roundtrip, ciphertext tamper, wrong key/tenant/domain/revision failure, absent key fail-closed, no plaintext in stored value/API/logs/task payload/audit; migration does not invent credentials
3. Protocol/TLS: real TLS handshakes over net.Pipe or owned isolated fixtures for both modes; trusted success, unknown CA, wrong DNS, expired/not-yet-valid cert, wrong EKU, malformed chain/response, refusal, zero bind before verified TLS; BER length/opcode/message-ID limits; referral no-follow; empty/bad credentials and success+malformed rootDSE; no second bind/network retry
4. Cancellation: blocked DNS/dial/TLS/bind/rootDSE and peer that never sends/reads; external cancellation and timeout close connection, join goroutines and produce no later credential writes. Completion/cancel/revoke/lease-loss races tested with synchronization, not arbitrary sleeps
5. Real PostgreSQL: migrate from schema 8 preserving old data; strict schema gate before proof writes; audited CRUD persistence/rollback; concurrent duplicate and last slot; admin has no data bypass; missing/foreign/ungranted IDs indistinguishable; expired/zero/overcap eligibility; explicit assignment; edit/delete/revoke/regrant while queued/running; old credential/config/policy/generation cannot publish; fixture-only synthetic domains remain distinguishable
6. Queue: maxAttempts=1 persists before first network call; duplicate key no extra task/bind/generation; same key from another actor denied; changed revisions conflict; no generic submit/recover/schedule escape; no stale policy worker execution; no DB lock held over probe
7. Browser→real API→PostgreSQL with a synthetic LDAP/TLS container on actual fixed ports 389/636, a synthetic trusted CA and a pinned container-IP policy (no production port override): add → obtain ID → explicit group grant → detail → test → safe result → credential replacement → stale result rejection → delete; include interrupted create recovery and all UX races from section 9. Test actors/keys/passwords are generated synthetic data only
8. Run applicable repository `make check`, integration/lifecycle/browser scripts and independent diff review against final integrated head. Missing Docker/DB/browser is blocked, not skipped/pass. Report exact commands/head/evidence separately from this plan
9. Later, separately authorized real AD acceptance across Windows Server 2008, 2008R2, 2012, 2012R2, 2016, 2019, 2022 and 2025 with representative domain policy, trust, StartTLS/LDAPS, credential failure/lockout, cancellation and least privilege. Synthetic TLS/protocol tests are engineering evidence only and fill zero real AD matrix slots

## 12. Primary protocol references and practical limits

- Microsoft [Enable LDAP over SSL](https://learn.microsoft.com/en-US/troubleshoot/windows-server/identity/enable-ldap-over-ssl-3rd-certification-authority): DC DNS identity, trusted chain and server-auth certificate requirements; TLS precedes LDAP on 636. Old example key sizes/options are not copied
- Microsoft [RootDSE schema](https://learn.microsoft.com/en-us/windows/win32/adschema/rootdse): `defaultNamingContext` identifies the server's domain NC; `dnsHostName`, capability/version attributes are server metadata. RootDSE is not a complete DC inventory or proof of administrative privilege
- Microsoft [MS-ADTS Using SSL/TLS](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/8e73932f-70cf-46d6-88b1-8d9f86235e81): direct TLS and StartTLS transport behavior
- IETF [RFC 4513](https://www.rfc-editor.org/info/rfc4513/), sections 3.1.1 and 3.1.3: StartTLS sequencing and server reference-identity checks; sections 5.1/6.3 inform empty-password rejection. Current TLS policy supersedes historical weak cipher examples
- Microsoft [LDAP channel binding](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/ldap-channel-binding): channel binding concerns SASL/TLS. A simple-bind TLS probe must not claim Kerberos/NTLM/SASL or channel-binding interoperability
- Go [crypto/x509](https://pkg.go.dev/crypto/x509#Certificate.VerifyHostname): use native certificate/DNS verification, with SAN behavior; a CN-only legacy certificate is a compatibility failure to fix, not justification for disabling verification

All web sources were consulted on 2026-10-07. Their existence does not establish compatibility with the user's unavailable real AD deployment.


## Explicit provider endpoint exclusions

LDAP destination policy excludes Alibaba metadata 100.100.100.200 ([provider documentation](https://www.alibabacloud.com/help/en/ecs/user-guide/view-instance-metadata/)), AWS IPv6 metadata fd00:ec2::254 ([provider documentation](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instancedata-data-retrieval.html)), and Azure platform virtual IP 168.63.129.16 ([provider documentation](https://learn.microsoft.com/en-us/azure/virtual-network/what-is-ip-address-168-63-129-16)), including mapped addresses and overlapping allowlist prefixes. This only restricts LDAP probe destinations; it does not change host firewall or DNS settings.

## Cancellation after a completed probe

The diagnostic kind sets CancelDiscardsResult=false. Cancellation observed before a phase or final checkpoint prevents further work/publication. If the probe and final fenced observation already completed before a late cancellation reaches Finish, the task preserves actual success and records completed-with-cancel-race. Cancellation never claims to undo an authentication bind or its remote counters. Public successful diagnostics use code=ok and stage=complete; internal staged audit observation code=success is not a terminal success claim.
