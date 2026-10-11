# AD-F-038: exact stored-account and creation-time filters

Refs [engineering #111](https://github.com/yunpiao/adtr/issues/111),
[F14 #30](https://github.com/yunpiao/adtr/issues/30).
Base: main `77a6e2140eaa69e82d2d3a6e306c95fcf8bcb1d7`.
This is a bounded engineering extension to [the existing read contract](user-assets-v2-contract.md),
not source-system parity, AD server matching semantics, complete F14 or product acceptance.

## Evidence and deliberate choices

Issue #30 FLD-0334 requires exact SAM matching. FLD-0328/0329 name startTm/endTm
without field/interval/format semantics; FLD-0332 names an integer creatTm but
provides no enum. Dictionary 2 already stores optional SAM and whenCreated.
The previous handler explicitly rejected these four parameters with 422.
This extension chooses explicit text order values rather than inventing old
integer meanings. No directory fields, schema, producer, permissions or default
collection switches change.

## Optional query parameters

All apply only to GET `/api/user-assets/v2`; detail still rejects list predicates.

| Parameter | Contract | Omission and limits |
| --- | --- | --- |
| sAMAccountName | Exact raw UTF-8 byte equality, case-sensitive. No trim, casefold, Unicode normalization, wildcard, SQL or LDAP evaluation | Omitted/empty means no predicate. At most 256 UTF-16 code units AND 1024 UTF-8 bytes. A nonempty query never matches null |
| startTm, endTm | Together filter observed whenCreated as `start <= created < end` | Both absent or both nonempty, with start strictly before end; null does not match a range |
| creatTm | `asc` or `desc` by observed creation time; null last in both directions; equal times (and two nulls) use canonical GUID text byte ascending | Omitted preserves existing GUID ascending. Empty, integers and other enum values are invalid. No range is required for sorting |

SAM query bounds are a browser/server request budget, not an asserted AD account
limit. Existing stored SAM is limited separately by its unchanged codec; long
supplementary-Unicode account names can exceed the narrower UTF-16 query budget.
The original four-field `search` keeps its unchanged case-mapped literal contains
semantics and bounds. All supplied predicates intersect (AND). Removing a
predicate broadens the result; removing creatTm restores GUID order.

Times use uppercase `T` and `Z`, exactly four year/two other component digits,
optional dot plus 1–9 fractional digits, and either Z or an explicit numeric
±HH:MM offset (hours 00–23, minutes 00–59). Numeric −00:00 is treated as zero in
this engineering profile. Calendar invalidity, leap seconds, comma fractions,
missing offset, local year0000 and UTC-normalized years outside0001–9999 fail.
Bounds normalize to UTC for exact nanosecond comparisons; query spelling need
not be rewritten. No millisecond rounding or UnixNano year-range overflow is
allowed. Observed dictionary2 times remain whole-second UTC facts, including
present year0001; query precision does not fabricate source precision.

Malformed UTF-8, duplicates (including identical repeated values), unknown keys,
explicit empty range/order, unpaired/reversed/equal ranges and over-budget
inputs return 400 invalid_input. Unsupported filters retain 422
unsupported_user_asset_filter after structural validation. The existing raw
query cap32KiB, pageIdx1..10000 and pageSize10/20/30/40/50 remain unchanged.

## Read and browser invariants

Current authority → exact source selection → pinned/latest eligible observation
→ complete stored-body validation → user-only projection → all predicates →
chosen stable ordering → filtered total/page → bounded response.
A corrupt filtered-out/off-page object still invalidates the response. Filtering
occurs only in bounded Go memory, never SQL/LDAP. Null is never zero/year0001.
The new time comparator can use fixed-width whole-second UTC text because the
unchanged ProjectV2 codec guarantees that format; filter bounds use time.Time.

Each response retains the existing exact shape and observation/source identity.
The browser verifies added predicate membership, chosen ordering and GUID
uniqueness, retaining the original streaming/actor/size/UTF-8 checks. BigInt
nanosecond comparison preserves query precision and year0001.

Draft/applied filters remain separate. Apply and paging retain the current
observation; a newly published observation does not change those reads. Only
explicit refresh asks for latest, and source change resets query/pin/detail.
The UI rejects control-containing paste/drop into the new exact/time inputs before
native single-line sanitization, visibly marks that field invalid, and blocks
submission until it is edited or all filters are cleared. API raw-value equality
remains unchanged. Invalid drafts cannot silently become unfiltered reads. Existing generation,
abort, repeated-refresh, source-conflict, scope/session revocation and drawer
cleanup behavior remains required. Creation time is labeled as a stored fact,
not last login, modification time or observation completion.

## Verification and open scope

The unchanged base full make check passed: 1470 DOM tests, 102 Python contracts,
Go race/vet/build, typecheck/build and dependency audit0. This is baseline evidence
only. Candidate checks, exact-head CI and genuine chain artifacts are recorded
in the PR; local missing Docker/PostgreSQL is a blocked stage, never a pass.

Tests must cover exact/case/Unicode/null account values; half-open nanosecond and
offset boundaries; year0001; null placement; many equal-time pages; filter before
count/page; pinned reads after a new observation; current authority and delayed
responses. Existing stable and fixed browser native-reader assertions remain.
Actual Worker→synthetic TLS LDAP→PostgreSQL→API→browser evidence is separate from
synthetic Execute/HTTP unit fixtures. Screenshots must reflect real responses.

No export is implemented here. Original business status, modification/login,
risk, group/relationship fields, broader source semantics, real AD/eight Windows
versions and capacity remain open. Product acceptance remains0/209. #30 and
engineering #111 stay open pending their separate acceptance/closure decision.
