# Dictionary 2 integration boundary

Restored frozen design, 2026-10-08. Refs #25 / AD-F-034 and #30 / AD-F-038/039/040/042. Baseline source is PR77 head4f64d908/treeea38fc4, schema15, with25 successful CI jobs. This document specifies the next increment, not completed implementation or product acceptance. The main-target validation commit has the same source tree.

## Identity and security

Use exact kind/purpose `domain.directory_read.v2`, task payloadVersion1 and dictionaryVersion2. Preserve the v1 eight-key payload, routes, bytes, grants and recovery keys. V2 adds only its ninth immutable dictionary pin. A closed internal profile chooses purpose/kind/decoder/SQL; requests cannot select attributes, filters, serializers or SQL identifiers.

The fixed request order is objectGUID, objectClass, sAMAccountName, userAccountControl, objectSid, mail, description, whenCreated. Preserve the principal filter, trusted TLS/RootDSE/egress checks, single attempt and existing transport budgets. Collection requires both `ADTR_DIRECTORY_READ_ENABLED` and strict default-false `ADTR_DIRECTORY_READ_V2_ENABLED`, independently configured key/trust/egress, live tenant/domain/resource authority and a new exact v2 grant. Neither connection-test nor v1 grants imply v2 access. No migration copies grants.

Reuse the shared fenced use lifecycle. Commit opening before decrypting; recheck authority before stages/pages/publication. Only the original engine-issued executor-return witness releases opened use. Cancellation, revocation, lease loss, timeout or process loss is not that proof. Maintenance fairly clears only never-opened terminal reservations for both profiles.

Stored reads require current domains.read + directory_assets.read and domain scope, independent of credential grants. Task/receipt reads add tasks.read; sync/cancel add directory_assets.write + tasks.write and fresh password/TOTP, same-origin and CSRF proof. Grant administration keeps the builtin-admin boundary; qualified readers retain effective-self introspection. No new permission mark or automatic assignment is added.

Disabling collection does not remove authorized stored reads, receipts, task inspection, cancellation or original-opener cleanup. consumerEnabled means compiled capability, not deployment permission.

## HTTP contract

Add `/api/directory/v2/{observation,receipt,task,sync,cancel}` with the same corresponding v1 methods, query/body shapes and `{error: code}` envelope. Route choice fixes the profile. Sync accepts domainId, expectedRevision, expectedCredentialGeneration, idempotencyKey, actorPassword, totpCode; no caller dictionary pin. Cancel accepts taskUUID and proof. Task/receipt endpoints accept only the matching v2 kind, with no v1 fallback.

Add `/api/directory-credential-use/v2/{accounts,grants,roles,effective,mutation,grant,revoke}` with existing methods/envelopes and exact v2 purpose. Receipt lookup is exact-purpose scoped, and the browser keeps separate profile recovery identifiers. The existing durable mutation key remains tenant/actor/key scoped: reusing a committed key for another purpose returns 409 idempotency_conflict without consuming fresh proof; callers create a distinct key for a new profile intent.

Observation query retains domainId, optional observationId/kind, pageIdx default1 and pageSize default50 (25/50/100). Page2+ requires an exact snapshot pin. Sort by GUID. Select only v2 observations matching current source/policy and a succeeded v2 task. Missing current observation is available:false; observed empty is available:true/list:[]. Stale explicit pins retain409 directory_observation_unavailable and require explicit first-page refresh.

Response has mandatory integer dictionaryVersion:2 even when unavailable, plus the existing available/observationId/source/list/page fields. Preserve source and page names/types. Each object has exactly the original six fields plus objectSid (canonical string|null), mail (raw accepted UTF-8 string|null), description (one-element UTF-8 array|null for this profile), whenCreated (UTC whole-second string|null, preserving year0001). Null means requested but not returned; v1 means not collected. Do not infer omission reasons, aliases or business semantics.

Store raw supplemental bytes as canonical base64 inside the separately versioned envelope; never expose that representation as public field values. Preserve supported NUL/control/non-BMP text through PostgreSQL and JSON. V2 serialized observation pages are capped at8MiB before any output bytes; exceeding it returns422 directory_limit_exceeded with no partial rows. Browser reads must be bounded too. Retain all row/page/aggregate/LDAP/phase limits and distinct unsupported-profile versus malformed-response errors. These are safety limits, not capacity acceptance.

## Migration16 and storage

Under the existing exclusive schema gate, atomically install existing exact-purpose/dependency/use fragments, then new observation/audit guards, and stamp16 only after success. Promptly reject all opened B2 or directory uses; rollback leaves original schema15 acknowledgement possible. Reject reserved kind/purpose/dependency/provenance/function/action collisions and unexpected historical shapes instead of adopting or rewriting unknown records. Historical SQL definitions remain unchanged.

Add observation dictionary_version with legacy default1 without changing historical body/digest/timestamps/task payloads. V2 explicitly inserts2 and stores canonical raw/base64 bytea. SQL JSONB guards must never parse public NUL-containing text. Match task kind/version, use purpose/dictionary, opener/attempt/fence/lease and live publication authority. Revalidate exact source/count/digest/canonical bytes on reads; fail closed without partial rows or fallback. Keep immutable/no-truncate guarantees.

Add separate domain_directory_v2_submit, domain_directory_v2_cancel and domain_directory_v2_result actions, preserving legacy audit projections. Match v2 task-kind predicates for result projection. Schema15 binaries fail exact-schema readiness after upgrade; no mixed-version or downgrade promise.

## Module and browser contract

Expose fixed store counterparts DirectoryV2Kind, SubmitDirectoryV2Tx, DirectoryV2ReceiptTx, CancelDirectoryV2Tx, DirectoryV2ListTx and ReconcileReservedDirectoryV2Uses. Existing v1 entry points stay exact wrappers. DirectoryV2List carries the mandatory version and separately typed public objects.

Add explicit supplemental-directory and v2 grant views with independent strict parsers and actor/profile recovery keys. V1 stays initial/default. No silent record/grant/key upgrade. Render text via React text nodes, visibly escape control/bidi characters without altering stored bytes, and never add HTML, automatic mail links, inferred risk/status/login/relationships, arbitrary filters or exports. Null is 未返回. Preserve session invalidation, native focus/visibility revalidation, abort/stale-response handling, proof cleanup and original-intent recovery; never auto-replay mutations.

## Required gates

Actual production migration15→16, rollback/collision/opened-use races; separate-purpose/default-deny and original-opener cleanup; real worker→synthetic TLS LDAP→PostgreSQL lossless NUL/year0001; unchanged v1 goldens; protected/bounded v2 HTTP; real browser pagination, empty/unavailable, grant denial, cancellation, lost committed response and cross-tab reader isolation. Retain every CI suite and bounded parallelism. Compilation/component tests do not replace database/browser execution.

Real AD/eight Windows versions, full source field/filter/export parity, capacity/recovery and production deployment remain open. Incremental/deletion semantics, addresses, ACL/members/trust and business detections are outside this factual-profile increment. Product acceptance remains0/209.
