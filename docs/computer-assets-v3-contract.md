# F14 / AD-F-040: stored dictionary-3 computer list

Refs [engineering #131](https://github.com/yunpiao/adtr/issues/131), [F14 #30](https://github.com/yunpiao/adtr/issues/30). Initial baseline main
`6beb495329e226ede42004a1063a1a57857d3216`. This is a bounded engineering slice,
not original-system parity, complete F14 or product acceptance.

## Source evidence and gaps

The source workbook `2026-10-03-AD功能清单.xlsx` is identified by catalog SHA256
`d59ccdf3eac3865b20c1f919f2cbc881df625714b2c499342b4cc1bcf2efae54`, sheet AD 功能清单,
row 44. Catalog retains FLD-0412–0458 and UI-0437–0445. Issue #30 supplies the
field definitions. The original workbook and original application source files
are not present in this execution environment; their contents are not claimed
re-inspected. Source references are the catalog and live Issue body.

The Issue declares computerStatus/domainName/startTm/endTm/search/updateTm/
creatTm/computerType, but does not settle complete interval/order/null semantics.
Its DNSName/DNSAddress/OS/login/application/status/name/address relationship
fields have no producer in current dictionary3. Their absence is a source gap,
not permission to invent null aliases or infer identity from SPNs. F12 #25 and
F47 #11 remain the producer/authority dependencies; F38 #26 broader export is
not implemented here. No parent Issue is closed.

Merged prototype PR89 head `2ec70500881038251ddb1678691d9ecd10aebb90` describes
AD-F-040 at `#/assets` as `read-only-fixture`; it informs compact directory
navigation/table/expandable facts only. Prototype status/risk/device/address
samples are not production facts.

## Read-only API

GET `/api/computer-assets/v3`; all other methods 405 with Allow: GET. No detail,
export, write or collection route is added. Unknown paths 404. Singleton
`domainId`, `expectedRevision`, `expectedCredentialRevision` are required, using
existing ID/canonical decimal revision grammar. Optional observationId is
permitted to be absent only on first page/explicit refresh; later pages require
it. Source pins are current-authorized in the same transaction as the full
eligible immutable v3 observation read.

Input bodies, malformed URL/UTF8, duplicate/unknown keys, noncanonical numbers,
raw query over 32KiB and invalid bounds return 400 invalid_input. Unsupported
computerStatus/domainName/updateTm/computerType/overviewType return 422
unsupported_computer_asset_filter after structural validation. sAMAccountName
is not a supported query parameter: Issue #30 does not declare computer exact
SAM filtering. No caller-selected object kind is accepted.

Chosen local filter semantics (not old-system or AD matching parity):
- search omitted/empty means no predicate. Otherwise ≤50 UTF16 units and ≤200
  UTF8 bytes. Go strings.Contains(strings.ToLower(field), strings.ToLower(query))
  independently on non-null SAM, SID and DN. No trim, normalization, regex,
  wildcards, SQL/LDAP interpretation or cross-field matches. SPN, mail,
  description, GUID, DNS/IP and times are not searched.
- startTm/endTm are both absent or both nonempty strict RFC3339 with up to 9
  fractional digits, explicit offset, representable UTC years 0001–9999, start
  strictly before end. Filter stored whenCreated with start <= value < end;
  missing values never match. Raw source strings are not modified.
- creatTm omitted means GUID ascending; asc/desc means creation-time order with
  null last both ways and GUID ascending ties. Integers/empty/other enum invalid.
- pageIdx defaults1, canonical integer1..10000. pageSize defaults10, exactly
  10/20/30/40/50. -1 rejected, not fetch-all. Beyond-result pages return empty
  list with real filtered total. Totals calculated before paging.

Success: exact dictionaryVersion=3, selection {domainId,revision,
credentialRevision}, available, observedComputerCount, list, page; observationId
and source appear iff available. observedComputerCount=null with no eligible
observation, else kind=computer count before predicates. Thus unavailable,
observed zero computers, and nonzero observed computers with zero matches are
distinct. This is a count within this observation, not a live/domain-wide count.
Each row reuses the closed PublicObjectV3 projection unchanged, with kind=computer: GUID/DN/kind/
objectClass/SAM/UAC/SID/mail/description/whenCreated/primaryGroupID/SPN. Optional
facts remain null when requested but unreturned, and real 0 remains 0. No aliases
id/name/status or physical-device identity are manufactured.

Current authority → current source pins → exact/newest eligible v3 observation
→ full body/digest/count/source validation including excluded kinds/rows →
computer restriction/count → predicates → stable ordering → filtered total/page.
No newer-observation fallback for a supplied pin; latest empty never falls back
to older nonempty. Source storage remains ≤10000 rows/16MiB, response ≤8MiB
including JSON newline. Entire response encodes before headers/bytes; failure
returns no partial rows. Cancellation checked throughout.

## Authority, state and browser

Reuse live non-forced session, exact schema and tenant/actor lock order,
domains.read AND directory_assets.read, current eligible tenant and F47 domain
scope. Admin has no bypass. Reads do not require task ownership, collection flags
or credential-use grants, and never create tasks/uses or open LDAP. Existing
v1/v2/v3 storage/producer/gates/permissions are unchanged.

No-store, nosniff, JSON and authenticated actor header remain mandatory. 401 or
password_change_required clears private state and revalidates session; lost
scope/403/404 clears protected selection/list. 409 selection_changed clears
source/pin; 409 directory_observation_unavailable requires explicit refresh,
never silent fallback. Transient failure retries preserve captured source/pin.

The separate computer-assets-v3 SOC page uses choose→resolve source picker,
explicit refresh, search/time/order/page controls. Applied/draft query distinct;
changes cannot reuse stale controls. Expandable row facts come only from that
pinned list response, with no separate endpoint or cross-observation fetch.
Source/filter/page/refresh/navigation/session changes close private expansion.
Abort/generation guards reject late/ignored-abort/same-query responses. Native
Back/Forward, background-tab visibility, cross-tab revocation and focus behavior
must be verified. Hostile raw text renders inertly with existing v2 display escaping; SPNs use
reversible backslash/control escaping and reuse
PR126 directory-v3-facts.ts, never hyperlink or infer hostnames/membership/risk.

## Required verification and exclusions

Clean baseline, final full make check and integration-tag compile/vet; pure
filter/order/null/Unicode/time/bounds/cancellation tests; exact query/auth HTTP
contracts; real PostgreSQL current-authority/source/observation full-validation
cases; isolated >10-computer TLS fixture with user/group/inherited-user decoys.
Registered real Worker→owned TLS LDAP→PostgreSQL→API→browser must verify actual
projected values, filtered totals, multi-page pins and newer observations.
Both browser engines/native reader completion, Back/Forward/session/source
revocation/late response plus desktop/mobile screenshots remain required,
alongside existing regression CI. No observation insertion or fabricated API
success may substitute for producer evidence.

Independent actual-diff/security/visual review and exact combined-head current
main CI are required before merge. Missing Docker/DSN/browser execution is
blocked, never passed. No new schema/producer/gate/default change, real AD,
eight-Windows compatibility, target capacity, export/address relationships,
production deployment, complete feature parity or product acceptance is claimed.

### Canonical facts versus LDAP bytes

ObjectClass follows the existing decoder contract: ASCII class names are lowercased,
deduplicated by rejection and sorted. These are canonical observed facts, not
unchanged LDAP attribute bytes. The computer list adds no further transformation.
The RID is the decoded uint32 value (including zero); SPN values preserve their
validated strings and order before safe display escaping. Neither makes claims
about device identity or relationships.
