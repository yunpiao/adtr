# Resource and tenant isolation contract (F47)

Refs Issue #11, AD-F-179–183. Source workbook 2026-10-03-AD功能清单.xlsx,
AD 功能清单 rows 183–187; 业务支持字段 FLD-2991–3072. Those 82 fields are
reference clues, not evidence that source runtime semantics or numeric AD
application identifiers have been frozen. There are no F47-specific UI rows
or supporting detail rows in the extracted workbook.

This slice persists and checks local authorization scopes in real PostgreSQL.
It does not connect AD, create an AD inventory, run AD operations, implement
licensing/customer identity verification, or claim 1:1 source acceptance.
The initial production domain catalogue is empty. Synthetic domains are
inserted only by integration fixtures. Real AD consumption and Windows/AD
verification remain blocked until the separately authorized connection and
inventory modules exist.

## API, identity and permissions

Prefix `/api/resources`; the current HttpOnly session cookie is the only
identity. JSON token/tenant/actor/role assertions are rejected. GET query
parameters and POST bodies are strict: unknown keys, repeated scalar queries,
malformed query encoding, duplicate JSON keys (including nested duplicates),
null supplied fields and trailing JSON are errors. Bodies are limited to 256 KiB. POST requires trusted exact
Origin, JSON and the session-bound CSRF token, even for read-only `/check`.
A forced-password-change session is denied.

All modifying POST bodies additionally require `actorPassword` and fresh unused
`totpCode`, using the F45/F46 helper and rate limit. Function authorization comes
from the live current role and its persisted grants. No caller-supplied route,
role, tenant, UID or token becomes authority.

- Resource group management uses the existing `roles` readable/writeable function
  grant; this is an explicit local decision, not a claimed source permission mark
- Tenant configuration read and write require the builtin `platform_admin` role
- `/grants` and `/check` are current-user introspection, available to any valid
  non-forced-change session, with no management grant prerequisite
- Function administration never implies AD data authorization. Both builtin roles,
  `platform_admin` and `viewer`, can receive explicit resource group associations;
  neither has an implicit wildcard. Custom role IDs remain opaque F45 identifiers
- Non-admin writers cannot alter/delete a group attached to their own role or to
  `platform_admin`, add/remove their own association, or alter admin associations
- A non-admin writer may only administer/delegate domains present in their own
  persisted scope. Existing and proposed scopes are checked. Inactive domains and
  expired tenant configuration do not erase this ceiling and permit latent escalation.
  Associated roles also cannot carry function grants beyond the non-admin writer's own

Every operation holds the F45 tenant advisory transaction lock
`hashtextextended('adtr-access:' || tenant, 0)` and locks/rechecks the actor's live
session. Group and relation mutations invalidate sessions for every affected
role; config changes invalidate every session in the tenant, including the caller.
Affected user rows are locked in deterministic ID order before deleting sessions,
so a concurrently committed login/password/MFA session cannot survive an old
DELETE snapshot. The response `sessionRevoked:true` clears the caller cookie.
Authorization queries also read live membership and active catalogue state;
there is no cache of data grants in a cookie.

### Group reads

- GET `/groups`: pageIdx (default 1, 1–1,000,000), pageSize (default 20,
  1–100, or -1 for bounded all-results at page 1), name (literal case-insensitive
  substring, at most 50 Unicode codepoints), sort (false/default descending
  creation time; true ascending). ID is the deterministic tie-breaker
- GET `/groups/detail?id=...`: `{meta: ResourceMeta}`; absent and foreign IDs both 404
- GET `/groups/exists?name=...`: `{isExist:boolean}`, same-tenant case-insensitive check
- GET `/groups/roles?id=...&pageIdx=...&pageSize=...`: associated roles as
  `{page,details:[{id,name,mark}],exhausted}`, ordered by role ID; builtin names are IDs

Group list is `{page:{pageIdx,pageSize,total,totalPage},metas:[],exhausted}`.
A ResourceMeta has `id`, `name`, `datas:[{appName:"ad",resources:[domainId,...]}]`,
`mark`, `applyRoleCount`, `createTime` (UTC RFC3339). Empty datas and result lists
are `[]`. Resources within a group are sorted by ID. applyRoleCount is computed
from actual associations. pageSize=-1 rejects more than 1,000 matching records
with 422 `result_too_large`; it never silently truncates or promises unlimited output.
Zero results have totalPage=0 and exhausted=true. Pages beyond the end return an
empty list with exhausted=true and the true total.

### Group writes

- POST `/groups/create`: `{meta:{name,mark,datas},actorPassword,totpCode}`
- POST `/groups/update`: `{id,meta:{name,mark,datas},actorPassword,totpCode}`;
  replaces name, mark and full domain membership atomically
- POST `/groups/delete`: `{id,actorPassword,totpCode}`; deletes associations and
  members atomically, revokes affected sessions, preserves audit
- POST `/groups/assign`: `{id,roleIds:[...],actorPassword,totpCode}`; replaces the
  full association set atomically. Empty array removes all associations; missing
  or null array is invalid. At most 100 distinct role IDs; custom roles must exist
  in this tenant. Both documented builtin IDs are supported

Group names are 1–256 UTF-8 bytes, valid UTF-8 and no Unicode whitespace/control
characters, case-insensitively unique per tenant. Names are editable. mark is
optional/empty or at most 500 codepoints without controls. datas must be an array,
with zero or one `ad` entry and at most 1,000 distinct resource IDs. An empty group
is valid but grants nothing. Every nonempty membership must refer to an existing,
active domain in this tenant. Unknown and foreign domains both return 422
`unavailable_resource`. No write creates a catalogue entry. IDs are 1–128 ASCII
letters/digits/`_`/`-`/`.`; generated group IDs are random opaque identifiers.
Server fields id/createTime/applyRoleCount cannot be submitted inside meta.

Success is `{result:"SUCCESS",sessionRevoked:boolean}` plus group `id` on CRUD.
No-op updates/replacement return 409 `no_change`. A group name conflict returns
409 `name_conflict`. Mutation, structured object audit and identity audit commit
in one transaction; audit failure rolls everything back, including TOTP consumption.
There is no automatic mutation retry or last-writer-wins claim for concurrent users.
Operations serialize, but no client revision/ETag edit-conflict protocol is supplied.

### Tenant configuration

- GET `/tenant`: `{maxAdCount,expireTime,uid,name}`; unconfigured is 409
  `tenant_not_configured`, not an invented default
- POST `/tenant/save`: those same four required fields plus proof; saves only
  the session tenant. Success follows the mutation envelope and revokes all sessions

Explicit local decisions where source was silent:

- maxAdCount is 0–100,000 active catalogue domains; zero grants no data access.
  Reducing it below the current active catalogue count is rejected with 409
  `domain_limit_below_existing`; saving does not delete domains. A defensive runtime
  check denies all scope access with 403 `tenant_domain_limit_exceeded` if catalogue
  count exceeds the configured cap. Future domain registration/reactivation must
  serialize on the same tenant lock and enforce this cap before admitting a domain
- expireTime is a UTC Unix timestamp in **seconds**, integer 0–253402300799.
  There is no never-expires sentinel; omission is invalid and 0 is expired.
  Exactly now is expired, not inclusive.
  Expired access is 403 `tenant_expired`; admins can still repair configuration
- uid is 1–256 Unicode codepoints, name 1–32; no leading/trailing whitespace or
  control characters. uid is customer metadata, not a tenant selector, authenticated
  identity, verified external binding or a global uniqueness claim
- Saving an expired configuration is permitted and explicitly disables data access.
  This field is local policy configuration, not a claim of verified license expiry

### Current-session data scope

- GET `/grants`: `{list:[{application:"ad",resources:[...]}],authorizationScopeOnly:true}`.
  The list is empty when the role has no explicit active-domain grants. Missing or
  expired config returns the errors above. An empty grant list does not mean AD
  inventory is connected or queried
- POST `/check`: `{resourceType:2,resources:[{application:"ad",dataResource:[id,...]}]}`.
  Returns `{results:[boolean,...],authorizationScopeOnly:true}` in request order.
  Each result is AND over all explicitly listed IDs; 1–100 checks, 1–1,000 distinct
  IDs per check. Empty resource requests are errors, never vacuous authorization.
  Unknown/foreign/inactive/ungranted IDs are false without existence disclosure

`ad` is a deliberately documented **local API string**, not a guessed source
integer enum. Numeric/unknown applications are rejected. Only source resourceType
2 (DataResource) has safe implemented meaning. Types 0 (None) and 1 (Application)
return 422 `unsupported_resource_type`; no implicit application-wide wildcard is
inferred. Source nested string/integer application mismatch is not silently coerced.
The source `token` request is replaced by the live session, never accepted as JSON.

These endpoints prove membership only. Their response is not a token, promise of
future authorization, operation-level write permission, or capability to query AD.
Every future read, search, export, batch and worker must reauthenticate/revalidate
current actor, function permission, tenant and exact domain in its real operation
transaction. A `/check` response cannot be replayed to bypass that obligation.

## Storage and integration requirements

`ResourceSchema` runs after `SchemaV3`, only in the explicit versioned migrator.
It creates tenant config, domain catalogue, groups, members, role associations,
and append-only resource audit tables. Every object/member/relation query is
session-tenant scoped. Composite foreign keys prohibit cross-tenant references.
Custom-role associations use a generated nullable custom_role_id with a composite
FK to access_roles and ON DELETE CASCADE; NULL is reserved for the two immutable
builtin IDs. Group/domain deletion cascades memberships; group deletion cascades
associations. Deleting a custom role removes its dormant scope relations. A group
ID or domain ID coincidentally identical in two tenants never joins across them.
Audit triggers reject UPDATE/DELETE/TRUNCATE; separate production DB roles remain
an infrastructure gate, because a database owner can alter triggers.

The integrated boundaries are:

1. Migration version 4 installs ResourceSchema atomically after version 3. The API
   mounts /api/resources/ only when authentication is configured; workers and
   auth-disabled processes expose no resource API
2. accessMayAssign checks the verified effective actor role and calls the persisted
   resource delegation ceiling for user creation, role changes, bulk assignments,
   and disabled-account reactivation even when roleID is unchanged
3. Existing-role /roles/save and /permissions/save run the same resource ceiling
   before function grants change. Inactive domains and expired tenant settings do
   not hide dormant scope that would become effective later
4. All these checks remain in the existing tenant-locked transaction; identity,
   tenant, actor and permission ceilings never come from body assertions
5. /api/access/check uses the same resourcePathAllowed policy as real routes. Group
   management uses roles flags, tenant configuration requires the builtin admin,
   and current grants/check are available to authenticated non-forced sessions.
   Exact unknown paths/methods deny. Group paths appear in roles menu metadata;
   a custom all-function role does not acquire tenant-administrator identity
6. The F45 test fixtures install the resource schema for hooked methods. A focused
   v3→v4 upgrade regression checks all six resource tables and preservation of old
   custom-role grants and sessions, without inventing catalogue entries

No public domain-catalogue write exists. Future domain registration, activation,
deletion and worker integration must obey the same locking, capacity and tenant
rules; SQL fixture seeding is only for owned synthetic test databases.

## Test evidence and remaining gates

New `resource_test.go` covers field limits, Unicode byte/rune distinctions,
empty/duplicate/unsupported scope, pagination, literal search inputs, exact query
parsing, strict JSON and trust-boundary rejection before database access.
`resource_integration_test.go` uses a unique CREATE DATABASE per fixture and fails
when ADTR_TEST_DATABASE_URL is missing. It covers real persistence, CRUD, search,
result fields, explicit builtin grants/no bypass, foreign IDs, inactive domains,
expiry/capacity/UID behavior, FK boundaries, rollback on audit failure, immediate
revocation, fresh-proof concurrency, concurrent session creation and cross-module
function-equal/domain-broader delegation denial, including latent inactive scopes.
Synthetic session rows are fixture setup; actual password/TOTP checks and HTTP
handlers execute. Existing auth E2E covers full browser login/MFA enrollment.

The integrated local `make check` passed: Go format/vet/race/build, full requirement
traceability, 76 DOM tests, TypeScript/build, and npm audit with zero vulnerabilities.
All integration-tag packages compile. Independent source review checked the backend,
UI and integration hooks; compilation and mock DOM tests are not database/browser passes.
The separate resource browser scenario uses the real API and PostgreSQL with exactly
one explicitly synthetic domain catalogue row; it tests tenant settings, no admin
bypass, group persistence, explicit association, positive/negative scope and revocation.

Final-head real PostgreSQL/browser CI remains pending. Source enum/result parity,
real AD/Windows consumption, measured capacity, production DB/secret controls and
external customer binding/license semantics remain gates. This slice does not close
F47 or claim all five source requirements fully accepted.

### Source-field correspondence and intentional gaps

- FLD-2991–3003: current-session `/check` and `/grants`; source token is deliberately
  rejected, ambiguous numeric application IDs are not guessed, resourceType 0/1
  is blocked, and the local `ad`/dataResource request replaces the inconsistent
  nested string/integer application shape
- FLD-3004–3032: paging, literal name search, ascending boolean, group metadata and
  detail map to the group read APIs. Dates/count/ID are produced from durable rows
- FLD-3033–3035: the additional unnamed `details:[{id,name}]` group-result variant
  has no sufficiently identified aggregation/endpoint semantics in the supplied
  source. It is not fabricated; group list/detail are supported, legacy parity for
  that result variant remains blocked pending reference handler evidence
- FLD-3036–3050: writable meta is name/mark/datas only; server id/count/time fields
  are rejected on input. Target identity is an explicit `id` outside meta. Local
  mutation result is uppercase `SUCCESS`, matching the current identity API,
  rather than claiming the source's undocumented result-string values
- FLD-3051–3067: associated-role paged detail and atomic full roleIds replacement
  implement the relation outcome. The source bulk pair-list add/remove/replacement
  semantics were unspecified; the local per-group replacement is explicit and no
  silent source-pair compatibility is claimed
- FLD-3068–3072: four tenant values are saved/read durably with the decisions above.
  Local success is `SUCCESS`; failures use HTTP errors, never `200 {result:"failed"}`.
  External customer binding is unimplemented, and max/time semantics are local policy
