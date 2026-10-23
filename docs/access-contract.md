# User and function access contract (F45/F46)

This slice implements AD-F-171/172/174–178 locally with PostgreSQL. It does not
claim F47 resource isolation or production acceptance. Source: workbook
2026-10-03-AD功能清单.xlsx, FLD-2786–2849 and FLD-2858–2990; UI-0576–0634.

## Frozen browser API

All URLs start `/api/access`. Existing authentication cookie is the only identity.
GET requests use query parameters. POST requests require exact Origin, JSON and
session-bound X-CSRF-Token. A forced-password-change session cannot use these APIs.
All modifying POST bodies include `actorPassword` and `totpCode` (current password
and a fresh unused six-digit TOTP). Login or enrollment consumes its time step;
wait for the next code before a privileged write. The created user's `password`
is a distinct field. A current session alone is never sufficient for a write.
`POST /check` is read-only and does not require these two proof fields.

- GET `/users`: pageIdx (default 1), pageSize (default 20, 1–100, or -1 bounded
  all-results mode), search (literal case-insensitive username substring, ≤50
  characters), isSelf (default false), repeated filterRole (role IDs), repeated
  filterMfaStatus (enable/stop), repeated filterPassStrength (high/middle/low),
  filterStartCreateTm/filterEndCreateTm/filterStartPassTm/filterEndPassTm
  (RFC3339 instants, start inclusive/end exclusive), sort (1/-1 create time,
  2/-2 password time; default -1), roleID (optional assignment-list filter)
- GET `/users/exists?username=...`: `{result: boolean}`; same-tenant lookup
- POST `/users/create`: username, password, roleID (default viewer), optional
  role (legacy platform_admin/viewer assertion), mobile, email, remark, address,
  realName, department, post, actorPassword, totpCode
- POST `/users/update`: username (immutable lookup key), optional profile fields
  above and optional roleID/role, optional disabled boolean, actorPassword, totpCode.
  Omitted fields stay unchanged; empty strings clear optional profile fields
- POST `/users/delete`: username, actorPassword, totpCode
- GET `/roles`: pageIdx/pageSize/search/sort (1/-1 creation time, default -1)
- GET `/roles/detail?roleID=...`: `{role:Role,permissions:[...]}`
- GET `/roles/exists?name=...`: `{exists: boolean}`
- POST `/roles/save`: optional roleID (absent creates), roleName, remark,
  permissions, optional empty dataSrc object, actorPassword, totpCode
- POST `/roles/delete`: roleID, actorPassword, totpCode
- POST `/assignments`: userRoles: [{username,roleID}], actorPassword, totpCode.
  One entry is single assignment; 1–100 distinct usernames is atomic bulk assignment
- GET `/permissions?roleID=...`: `{permissions: [...]}`
- POST `/permissions/save`: roleID, permissions, actorPassword, totpCode
- GET `/menu`: `{menu: [...]}` containing only readable function nodes
- POST `/check`: `{paths: ["GET /api/access/users", ...]}` returns
  `{results: [boolean,...]}` in input order; unknown methods/paths default deny

Mutation success is `{result:"SUCCESS",sessionRevoked:boolean}`, with `roleID`
on role save and `ID` on user create. No password, token or MFA secret is returned.
SessionRevoked means the caller must reload identity/sign in. Errors are
`{error:code}` with appropriate 400/401/403/404/409/422/429/500 status.

User results: `{page:{pageIdx,pageSize,total,totalPage},List:[...],exhausted}`.
Each item has ID, username, passStrength, role, priv, mobile, email, remark,
createTm, hasMfa, avatar, pwdUpdateTm, address, realName, department, post,
roleID, roleName, disabled. Dates are UTC RFC3339. role remains platform_admin
or viewer for identity compatibility; roleID is the authoritative function role.
priv is 1 for platform_admin, 2 for custom roles, 3 for viewer; no numeric privilege
is accepted as authorization input. avatar is a reserved empty string; upload is
not supported by this slice. Lists return [] rather than null. pageSize=-1 is
limited to 1,000 matching records; above that returns 422 result_too_large.

Role results: `{page:...,list:[...],exhausted}`. A role has id, name, userNum,
remark, allowDelete, allowEdit, created, dataSrc. Builtin creation dates are the stable Unix epoch sentinel,
not fabricated tenant creation events. Builtin IDs are platform_admin
and viewer; their permission sets are immutable. Custom IDs are opaque. Role
names are case-insensitively unique per tenant, 1–50 Unicode characters. Names
are immutable after creation; remarks are ≤150 characters. Assigned roles cannot
be deleted. dataSrc is always {}; nonempty input returns
unsupported_data_source_scope until F47 supplies validated resource boundaries.

Permission input is `[{mark:"users",auth:{readable:true,writeable:false}}, ...]`.
Marks are users, roles, permissions. The permissions array is required on save;
[] explicitly means deny all. roles/save requires both roles.writeable and
permissions.writeable. Creating/assigning a non-viewer role also requires
roles.writeable. allow_auth describes registry capabilities; the caller may
only delegate the grants in its own menu auth. Omitted marks default deny; duplicates,
unknown marks and writeable=true/readable=false are rejected. Output metadata
is server-owned: name, mark, children:[], paths:[{name,url,auth}], checked,
auth:{readable,writeable}, allow_auth:{readable,writeable}, icon. Clients must
send only mark/auth when saving; submitted names, paths, icons, checked,
children or allow_auth are rejected, never treated as authority. The registry
contains implemented functions only. No wildcard permission is accepted.

## Security decisions and source-field mappings

Every query is scoped by the authenticated tenant; tenant/actor inputs are
unknown fields and rejected. Viewer gets no management permissions. All access
checks come from a fixed operation registry and persisted role grants. A custom
role cannot grant permissions it does not hold, modify its own role, manipulate
a platform administrator, or assign the administrator role. Only platform_admin
can change administrator membership. Reactivating a disabled account requires
roles.writeable and the same delegation check against the target's effective role,
even when roleID is omitted or unchanged. Self-delete and self-disable are forbidden;
a serialized tenant lock prevents removal of the last enabled administrator.
Every actual mutation and its audit record commit atomically. Role/assignment
changes revoke all affected sessions; disabling/deleting a user revokes theirs.
No-op mutations are rejected rather than returning empty success.

FLD-2786–2797 and FLD-2908–2911 map to GET /users query fields above; repeated
filters mean OR within a field and AND across fields. Unknown query fields,
duplicates of scalar fields and malformed dates are rejected. `disable` has no
safe distinct meaning in the source beyond MFA enable/stop; this slice rejects
it with unsupported_mfa_status rather than pretending that a policy exists.
The `disabled` account status is separate. All four time fields permit omission.
FLD-2798–2822 and FLD-2912–2934 map to the fully persisted user result above.
FLD-2823–2849 map to user create/update/delete/exists; username is immutable.
Password has the F43 12–64-codepoint policy, not the insecure 8-character UI hint.
Optional mainland-China mobile numbers are exactly 11 digits matching 1[3-9];
email is a bare valid mailbox ≤254 bytes; empty clears either. Profile text is
≤50 codepoints except remark ≤150; invalid UTF-8/control characters are rejected.
Broken unanchored source regular expressions are not copied.
FLD-2858–2881 map to role list/detail/name-exists. FLD-2882–2904 map to role
save/delete, with permission metadata and dataSrc restrictions above.
FLD-2905–2907 and FLD-2935–2938 map to atomic /assignments.
FLD-2939–2971 map to permission get/save; output fields are immutable metadata,
not client-selected routes or privilege ceilings. FLD-2972–2987 map to /menu.
FLD-2988 token is deliberately replaced by the HttpOnly cookie and cannot be
submitted in JSON; FLD-2989–2990 are /check paths and positional results.

The service uses tenant-scoped transaction locking for access mutations and
rechecks the current session after the user lock. Extending the registry and
introducing validated resource scopes is explicit future work; this document
does not imply access isolation for nonexistent AD objects or data sources.

## AD-F-176 isolated bulk-assignment browser evidence

Issue 9's two-target bulk workflow is exercised in the existing `access` CI
suite, using only its fresh owner-labelled disposable PostgreSQL database and
loopback API. This is acceptance evidence for an existing API/UI capability,
not a new production permission or account policy. Never point this case at
real users, a persistent database, an external identity service, or a real AD.

The existing `e2e.viewer` custom-role reader and one new synthetic
`e2e.bulk-viewer` built-in viewer exchange two non-administrator roles. The
bootstrap administrator stays unchanged. The actual user-list UI is filtered
by the literal `e2e.` substring and creation-time ascending order. This is
necessary because the UI constructs the assignment array from server list
order, not checkbox click order.

The first submission changes the original reader to viewer before reaching a
syntactically valid, verified-absent second role. Its exact 404/not_found must
roll back both role mappings, target-session deletions and successful
user_role_assign audits. Both original target BrowserContexts must still
authenticate without logging in again. A separate denied audit is allowed.
The second submission uses fresh proof and succeeds with the two intended
roles exchanged. Both original target sessions must return 401 before either
re-login, while the administrator's ID/role remains unchanged against a real
pre-mutation auth/me baseline. Both real UI logins then demonstrate the stored
role permissions: the demoted viewer has no management menu and cannot read
users; the promoted custom-role user can read users but cannot perform the
original six other checked operations. The old password-reset, MFA, profile,
forbidden-write, storage, logout and cleanup assertions remain intact.

Each new privileged submission is observed through the first actual matching
Playwright Request and its own Response, with one original click and one JSON
read. Payload order is checked privately without exporting password/TOTP data.
A bad first result is not replaced with a later success. Controlled click/body
branches are handled and joined, listeners/timers removed, and late results
discarded. The 15-second logical observation deadline does not cancel or prove
completion of underlying CDP operations; the existing page fixture owns their
lifecycle. No route mock, fetch wrapper, response replay or mutation retry is
introduced. New errors contain only a fixed phase, not raw transport/body data.

The three added administrator-sensitive mutations are new viewer creation,
deliberate rejected batch and successful batch, making 9/10 total. The reader
retains 3 sensitive operations and the new viewer uses 1 initial password change.
The rejected batch still consumes its sensitive-rate entry. There are 24 explicit
added verification GETs, including the administrator baseline; normal UI reads
are additional. Five authentication POSTs and two read-only access/check POSTs
are separate from the three administrator mutations. Preserve the 300-second
test, 420-second browser-subprocess limit, one worker, and zero Playwright retries.
No production rate, TOTP replay or clock rule is changed to fit the case.

Spec-local automatic traces/screenshots/video are off so a failed proof-bearing
form is not retained. Success evidence consists of exactly a closed safe JSON
summary and one real 1440×900 PNG of the administrator's final filtered two-user
list. Both target names/roles must be fully visible with no proof input or
mutation form. The image does not claim pixels of both re-login menus; those
are actual DOM/API checks. The summary is written only after original logout
and successful awaited closure of both owned target contexts.

`scripts/validate_access_role_bulk_evidence.py` independently validates the exact
two-file pair, safe schema, PNG bytes/dimensions, 46 relevant source hashes, and
the current tested commit/run/attempt. It also validates the existing runner's
post-cleanup diagnostics before staging the success artifact. A normal API
exit 0 and successful runner cleanup are required; the current owned-container
helper trusts successful Docker removal, so this does not claim an independent
post-removal container-absence witness. Only validated staged files are retained
for 7 days. Raw requests, auth profiles, audit argument strings, credentials,
traces and runtime logs are not success artifacts.

Source review, validator fixtures and historical CI do not prove this added
scenario passed. Its exact current-head browser/API/PostgreSQL result, retained
artifact, independent PNG review and cleanup review are required. This narrow
two-user result does not cover every single/bulk input combination, source-field
compatibility, capacity, deployment, or full AD-F-176/product acceptance.
