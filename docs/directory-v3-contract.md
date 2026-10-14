# Dictionary 3: bounded primaryGroupID and servicePrincipalName observations

Status: independently reviewed by the coordinating developer on 2026-10-11;
backend implementation in progress; not product-accepted.
Base: main `6f7f44a4997f78b6a50eab1ba657ffcc15769c1a` (schema 18).
Refs: [F12 #25 / AD-F-034](https://github.com/yunpiao/adtr/issues/25),
[F14 #30 / AD-F-039](https://github.com/yunpiao/adtr/issues/30).

## Delivery boundary

Backend-first: real registered Worker → isolated synthetic TLS LDAP → immutable
PostgreSQL observation → current-authorized versioned HTTP list/detail. No UI is
claimed in this first slice. Existing dictionary-1/2 producers, bytes, digests,
DTOs and routes remain compatible. A later UI uses the explicitly versioned v3
API; it must never fill an old observation from a newer one. No membership,
primary-group name, member count, risk, privilege, exploitability, SPN ownership
or successful Kerberos authentication is inferred from these fields.

## Evidence and new local representation policy

Microsoft documents primaryGroupID as the single-valued 4-byte RID of the primary
group ([schema](https://learn.microsoft.com/en-us/windows/win32/adschema/a-primarygroupid),
[MS-ADA3](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-ada3/e12954a4-6865-4432-94e6-00c310ca87c0)).
[MS-SAMR common user fields](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-samr/899d68a9-8145-4334-a52f-2fbd92bec94f)
defines its API representation as unsigned 32-bit. However, MS-ADA3 specifies
attributeSyntax 2.5.5.9 / omSyntax 2, and Microsoft documents that
[Integer syntax](https://learn.microsoft.com/en-us/windows/win32/adschema/s-integer)
as signed int32. [MS-ADTS LDAP representations](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/7cda533e-d7a4-4aec-a517-91d02ff4a1aa)
maps it to LDAP INTEGER. The uint32 public type is the RID representation; it is
not proof that real LDAP servers emit the high half as unsigned decimal.
LDAP transmits attribute values
as octet strings; this reader's explicit supported integer profile is canonical
unsigned ASCII decimal, not binary little-endian or signed reinterpretation.
[servicePrincipalName schema](https://learn.microsoft.com/en-us/windows/win32/adschema/a-serviceprincipalname)
defines a multivalued Unicode string without a documented range bound. The bounds
below are application safety caps, not Microsoft's maximum supported SPN length.
[RFC 4511 §4.1.7](https://www.rfc-editor.org/rfc/rfc4511.html#section-4.1.7)
and [RFC 4512 §2.2](https://www.rfc-editor.org/rfc/rfc4512.html#section-2.2)
provide attribute-set semantics; receive order has no directory semantic meaning.
[RFC 4517 §3.3.27](https://www.rfc-editor.org/rfc/rfc4517.html#section-3.3.27)
defines LDAP Integer syntax. AD wire compatibility across the eight required
Windows versions remains unverified; synthetic fixtures prove the local contract.

## Closed dictionary and object shapes

The search remains the exact existing root/domain boundary, principal filter,
paged-results control and TLS policy. The fixed attributes are the existing eight
v2 names plus `primaryGroupID` and `servicePrincipalName`, in that order. Names
are ASCII case-insensitive, but aliases/OIDs/options/ranged forms/unknown names
and duplicate descriptors are rejected. No wildcard or caller-selected attribute.

Separate `StoredObjectV3` = `base` (unchanged Object), `supplemental` (unchanged
RawSupplementalV2 value), `identity` (new raw identity fields). Reusing the closed
v2 base/supplemental validation as a nested component does not widen v2 storage.
`identity` contains exactly `primaryGroupIDBytes` (nullable base64 bytes) and
`servicePrincipalNameBytes` (nullable base64 byte array). All keys always occur
in the canonical order. PublicObjectV3 is the existing ten fact fields plus
`primaryGroupID` (JSON uint32 number or null) and `servicePrincipalName`
(JSON string array or null). An observed zero remains 0, never null.

- primaryGroupID: missing descriptor → null; present exactly one nonempty value,
  1–10 ASCII bytes, `0` or `[1-9][0-9]*`, range 0–4294967295. Reject signs,
  whitespace, leading zeroes, fractional/exponent forms, UTF-8 digit lookalikes,
  embedded NUL, binary bytes and overflow. Never default to 513 or derive from SID.
  In particular, negative LDAP integers are unsupported by this local RID profile;
  do not reinterpret signed int32 bits. Values above 2147483647 are codec boundary
  tests, not evidence of actual AD LDAP representation or full source compatibility.
- servicePrincipalName: missing descriptor → null; present 1–128 nonempty values,
  each valid UTF-8, at most 1024 UTF-16 code units AND 4096 source bytes; all SPN
  source bytes combined at most 32768 per object. Preserve case, Unicode code
  points, NUL/control characters, received ordering and each original byte value.
  Do not trim, normalize, parse hostnames, or perform Unicode/casefold matching.
  Exact byte duplicates are rejected rather than silently deduplicated; distinct
  spellings remain distinct, without asserting AD equality semantics. A present
  empty set or empty string is invalid. No truncation and no ranged continuation.
  These preservation/rejection choices are a new local engineering profile, not
  a claim that old-system SPN behavior or AD SPN grammar has been reproduced.
  Preserving valid UTF-8 NUL/control text means recording received bytes; it does
  not certify that the directory accepts that value as a usable SPN.
  Stored raw base64 avoids PostgreSQL JSONB NUL limitations; public projection
  uses normal JSON escaping. Order is provenance, not priority or canonical AD
  ordering. Cross-observation order changes may change the digest.
- A returned null means the requested field was not returned in this observation;
  it does not distinguish absent attributes from directory ACL omission. Old v1/v2
  observations were not collected for these fields and are never converted to v3.
- All successful mutable buffers are owned copies. Failure clears retained buffers
  and returns no partial observation. Strings cannot be reliably memory-scrubbed.

## Resource and canonical-storage limits

Keep the existing 10000 rows / 100 pages / 1000 entries per page / 64 KiB cookie /
16 MiB accumulated observation budget and 125-second task deadline. Charge the
larger of canonical stored and public encoded object sizes plus each received
cookie. Use a v3 stored-object ceiling of 128 KiB; the entire stored envelope stays
within 16 MiB. SPN controls can expand public JSON, so the public API response has
an explicit 8 MiB bound including its newline; an over-budget page fails as a
whole. Caller limits may tighten the caps and never increase them.

Before allocating typed/binary values, bound the input body, exact key set,
base/supplemental component sizes, array cardinality and every encoded value.
Representation-cap/unsupported integer-profile failures map to
`unsupported_directory_profile`; aggregate resource limits map to
`directory_limit_exceeded`; malformed BER/invalid UTF-8/unknown attributes map to
`invalid_response`. The final precise malformed-versus-unsupported boundary must
be covered by named tests, without claiming source invalidity from local caps.
Decode strict padded canonical base64; reject duplicate/unknown/missing keys,
noncanonical JSON numbers/escapes/order/whitespace and trailing data. Reencode
and compare exact bytes. Versioned envelope contains dictionaryVersion=3,
objects, source; SHA256, source, count, duplicate GUID and domain scope are checked
on every full observation read, even for a single-object detail request.

## Schema19 and authorization boundaries

Schema19 is reserved for this slice. Migration adds v3 purpose/kind/version
constraints and exact v3 validation to existing credential-use, dependencies,
publication and audit gates. It preserves v1/v2 validators and does not rewrite
historical bodies/digests. It refuses upgrade when existing credential executions
cannot be proved quiescent, following the current migration barrier.

New code purpose and task kind: `domain.directory_read.v3`, task payload version 1,
dictionaryVersion 3. Fixed selectors bind admission, payload, use ledger, grants,
transport, publication and query. V1/v2/probe grants never imply v3. Migration adds
no grant rows, credentials, tokens or assignments and changes no real environment
settings. Isolated test DB grants are explicit synthetic fixtures only.

New deployment gate `ADTR_DIRECTORY_READ_V3_ENABLED`, default false, is required
alongside the existing global `ADTR_DIRECTORY_READ_ENABLED`. V2 gate does not
control v3. The gate prevents accidental producer activation; without it, v3
admission/execution is unavailable, but authorized reads of existing v3 facts
remain possible. Enabling the gate alone grants no credential use. No production
setting, grant or credential change is performed by this development task.

Retain current domain/actor/tenant, connection/credential/policy revisions, current
functional/domain permissions, exact lease owner/fence/attempt, cancellation and
publication-state rules. Every transport phase/page/final return rechecks v3
purpose; task quiescence is witnessed only after executor and owned I/O return.
A cancellation/publication race never exposes a cancelled task's staged result.
New observations become visible only for succeeded tasks with current pins.
No wider API, network/TLS exception, recovery shortcut, retry count, scheduling or
persistent-access capability is added. MaxAttempts remains 1.

## API and remaining UI

Separate `/api/directory/v3/{observation,receipt,task,sync,cancel}` follows the v2
methods, proof requirements and operation shapes with fixed v3 selection and the
same current directory permission checks. Add exact-purpose governance routes
`/api/directory-credential-use/v3/{accounts,grants,roles,effective,mutation,grant,revoke}`;
builtin-admin grant management, fresh proof, same-origin/CSRF, current scope and
mutation receipt semantics stay unchanged. Merely installing these endpoints
creates no grants. `consumerEnabled` reports registered code capability only.
Backend grant controls have no accompanying v3 UI in this slice. Separate
`/api/user-assets/v3/detail` accepts exact domainId/observationId/objectGUID pins
plus expectedRevision/expectedCredentialRevision and returns a user-only v3 fact
projection. No implicit latest fallback for a
supplied missing/stale observation. Existing v2 list/detail and UI remain v2.
The backend v3 list makes the new observation discoverable; a later v3 user
search/UI implementation is explicitly outside this first slice.

## Required verification before merge

1. Clean-base checks; isolated new codec/transport/accumulator/observation unit,
   race and fuzz-style bounded corruption tests. Golden v1/v2 bytes/hashes fixed.
2. Primary-group 0/max/ordinary/missing, all malformed forms and multi-value;
   SPN UTF-8/non-BMP/NUL/case/ordering, absent versus empty, exact duplicates,
   128/129 count, 1024/1025 units, byte/aggregate caps; ranged/options/unknown
   descriptors and duplicate aliases; failure yields zero partial output.
3. Real synthetic TLS server verifies exact ten requested attributes; complete
   Worker → TLS LDAP → PostgreSQL → versioned HTTP verifies each projected value
   and persisted raw byte, with no injected success observation.
4. Real PostgreSQL migration18→19: old hashes unchanged, no copied grants,
   quiescence blocker, repeat migration, exact catalog; SQL bypass corruption and
   full-read failure, cross-version purpose/payload/storage/query isolation.
5. Current reader/domain/actor authority, revocation during page and final return,
   cancellation, publication fences, response loss/idempotent receipt, read-only
   identity, disabled gates, stale revision, exact observation/detail pin.
6. Complete local checks and exact-head full CI; independent actual-diff security
   review and main compatibility, then coordinated merge window. Local Docker or
   DB absence is recorded as blocked, never counted as passing. No UI/browser
   delivery claim from this backend slice; no real AD/Windows/production claim.

The parent requirements Issues remain open because full scope and product
acceptance remain incomplete. Engineering evidence and PR merge do not change
0/209. Any engineering Issue closure separately awaits the existing user
confirmation requirement.

## AD-F-052 bounded observed object counts (candidate, 2026-10-14)

Issue [#30](https://github.com/yunpiao/adtr/issues/30), FLD-0586/0588/0590:
`GET /api/directory/v3/observation` adds required `observedCounts`.
Unavailable responses contain `observedCounts: null`; a successful empty
observation contains `{ "user": 0, "group": 0, "computer": 0 }`.
Available responses contain exactly these three nonnegative integer keys. Counts
are computed from all validated objects in the response's same immutable
observation before kind filtering or pagination. Each object increments its
validated `kind` once: a computer with inherited `objectClass=user` is counted
only as computer. No separate query may substitute a newer observation.

The existing `page.total` still means the filtered result total. The three counts
stay stable across kind and page changes on one observation; their sum equals
all-kind total, and each selected-kind count equals that kind's total. Each count
and their sum stay within the existing 10000-object observation limit. Existing
source/revision checks, explicit observation pins, current tenant/domain read
permission and no-store responses apply unchanged. A stale pin returns the
existing unavailable error; a refreshed source with no successful observation
returns null counts. Producer-grant revocation alone does not remove authorized
stored reads, while removal of current reader domain access denies those reads.

The browser labels this “本次观测中的对象数量” beside observation ID/source.
It clears the counts with the observation on source changes, errors, revocation,
account changes and stale-response rejection. It shows unavailable distinctly
from successful zero counts. This is an explicitly observed-count slice, not
verified legacy asset-total parity. Collection exclusions, complete source
compatibility, security/risk summaries, differences and charts remain outside
this slice. AD-F-052 is in_progress; product acceptance remains 0/209.

Existing dictionary3 backend/browser/controls/readers CI jobs are extended with
mixed 12-user/12-group/6-computer and actual empty synthetic TLS LDAP cases,
PostgreSQL observation binding, kind/page stability and existing source/session
isolation paths. No workflow job, schema or collector change is required.
Local verification is restricted to explicitly allowlisted offline pure-Python
checks with subprocess/network/signal audit denial. Runtime Go/Node/PostgreSQL/
browser execution and exact-head 71-job CI are pending; source assertions alone
do not establish runtime acceptance.
