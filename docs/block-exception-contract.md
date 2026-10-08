# Blocking exception pure-data contract

Refs #45, AD-F-127/128/129; baseline PR #72 commit
`e437919967074bc544cf378d72eab51c37f351cc`.
Approved interface: https://github.com/yunpiao/adtr/issues/45#issuecomment-6051696071.

This package performs no I/O. `Match == true` means pure data equality only,
never an authorized exemption. No storage, routing, UI, AD operation or response
executor is connected. Only `internal/blockexceptions/**` and this document
belong to this slice.

## API and protection budgets

Every operation takes an explicit `Policy`: positive `MaxIDBytes`,
`MaxUserValueBytes`, `MaxListItems`, `MaxDeleteTargets`, and `MaxPageSize >= 10`.
Zero policies fail. These are caller protection budgets, not frozen or measured
product capacities. `MaxListItems` bounds the aggregate app, source, selection,
sort and nested selected-value counts, avoiding per-list amplification. Input
byte limits cover opaque IDs and user/query values. No default capacity is
invented. Integrators must provide suitable request-body limits before decoding.

`NormalizeRule`/`ValidateRule` validate a rule, allowing an absent storage ID for
creation. `NormalizeCandidate` validates matching input without requiring a
name or remark. `Match` validates both operands before comparing every Scope
field, ValueType and normalized value. Invalid input yields false plus a
structured error; valid unequal data yields false with no error.

Scope is exactly `TenantID`, `DataSourceID`, `AppType`. All are required.
DataSourceID is the stable `dataSrc` domain identifier, not the separate integer
`dataSource` selector. IDs are valid nonempty UTF-8, without leading/trailing
Unicode whitespace or Unicode control characters. They are never trimmed,
case-folded, Unicode-normalized, resolved as DNS or expanded through aliases.
AppType permits 1/2/3; the actual application mapping, including which means AD,
remains unfrozen.

ValueType 1 is a literal, case-sensitive opaque user value with the same text
rules and a caller byte budget. Wildcard or regex-looking text remains literal;
it never expands to a pattern. UPN, DOMAIN\\name, SID, composed/decomposed Unicode
and aliases are not converted. This does not establish an AD object identity.
ValueType 2 accepts only `netip.ParseAddr` single IPv4/IPv6 addresses and returns
`Addr.String()`. CIDRs, ranges, zones and IPv4-mapped IPv6 (dotted or hexadecimal)
are unsupported. No Unmap, DNS lookup, URL/port parsing or legacy numeric parser
is used. Rules and candidates use the same normalization.

Names require 1–50 Unicode scalar values (Go runes), valid UTF-8, no boundary
whitespace or controls. Remarks allow 0–150 runes, LF/tab but no other controls,
and preserve their text. Keywords allow 0–50 valid UTF-8 runes. These explicit
new semantics do not claim legacy UTF-16 maxlength or old regex compatibility.

`DuplicateKey` returns a comparable `Key{Scope, ValueType, RuleValue}`; names,
remarks and storage IDs are excluded. There is no delimiter encoding collision.
Equivalent IPv6 forms share a key. User case and every Scope field remain
significant. This does not enforce a database uniqueness constraint. No existing
rule-set duplicate scanner or name uniqueness policy is implemented.

`ApplyEdit` takes a loaded old rule and `Edit{RuleID, ValueType, RuleValue, Remark}`.
It validates old and new values and requires exact ID equality; neither name nor
Scope can be supplied in the edit. It returns a new rule without mutating the
old. `ValidateDetailID` requires an ID. `ValidateDeleteIDs` requires nonempty,
valid, unique IDs within the explicit target budget and returns a detached copy.
It neither deletes anything nor proves object existence/ownership.

## Structural list validation only

`ValidateQuery` produces `ValidatedQuery`, not SQL, a query plan or an exemption
matcher. Nil and nonnil empty collections, including nested value lists, remain
distinct. It validates only declared enums, token formats, duplicates and
budgets. Selection mode names are 1=partial, 2=unselected, 3=all; these are not
fuzzy/exact string operators. Mode is validated only for actual entries. Empty
valueList/mode combinations are preserved without interpreting their meaning.
Selected values are nonempty opaque tokens bounded by MaxUserValueBytes; they
are not IP-normalized and no cross-type combination semantics are implemented.
Duplicate apps, sources, selection value types, literal values within a group,
and sort fields are rejected. Sorting permits only createTime/modifyTime with
1/-1 direction; order is preserved and no default sorting is inserted.

PageIdx/PageSize pointers distinguish omission from zero: defaults are 1/10,
explicit zero and negatives are invalid, and -1 is unsupported. PageSize cannot
exceed the caller's MaxPageSize. Offset multiplication is checked for int
overflow. Any present creation/modification date filter, including an empty
string pointer, returns unsupported; no timezone/date semantics are guessed.
Dropdown endpoints and their return-all convention are out of scope.

All errors are `FieldError` with fixed field paths and codes required, invalid,
unsupported, duplicate or budget_exceeded. They never echo input values.
Successful outputs do not share mutable slices/maps with inputs.

## Verification and remaining work

Contract tests cover zero policies/rules, invalid UTF-8, Unicode length and
control boundaries, exact Scope isolation, literal case/Unicode values, address
forms and equivalence, key ambiguity, edit immutability, duplicate deletes,
query nil/empty and detached output, enum/duplicate failures, budgets and page
overflow. Fuzz tests assert fail-closed matching and normalization idempotence.
Run `go test -race -count=1 ./internal/blockexceptions`, `go vet
./internal/blockexceptions`, and the repository aggregate checks in final-head CI.

Still incomplete: name uniqueness, complex query combinations/date semantics,
real domain/user resolution and rename behavior, realtime authorization, storage
uniqueness and CRUD/API/UI, actual blocking/recovery, authorized Windows lab
acceptance and production acceptance. No PostgreSQL/browser/AD end-to-end result
is claimed for this unintegrated module. Full repository CI evidence belongs in
the PR and does not turn this pure slice into full F32 acceptance.
