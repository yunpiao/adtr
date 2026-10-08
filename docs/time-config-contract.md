# AD-F-198 pure time configuration parser

Refs [Issue #63](https://github.com/yunpiao/adtr/issues/63), specifically the
[confirmed contract](https://github.com/yunpiao/adtr/issues/63#issuecomment-6040316360).
Base: `e437919967074bc544cf378d72eab51c37f351cc` (PR #72).
This slice owns only `internal/timeconfig/**` and this document. It does not
complete AD-F-198 or F54 and must not close the Issue.

## Value interface and presence

`Parse(Input, Policy) (Config, []FieldError)` has no I/O or side effects.
It does not read the clock, load a timezone, resolve DNS, probe a server, execute
commands, change system/NTP settings, register schedules or decide permission.
Callers must supply an explicit, already loaded timezone for manual mode.

- `Input.SyncType *int`: nil is missing, 0 automatic, 1 manual; other values fail
- `Input.Date *string`: nil absent; even an empty pointed-to string is present
- `Input.NTPServers []string`: nil absent; a non-nil empty slice is present
- `Policy.MaxServers`: required, 1–32 inclusive
- `Policy.MaxServerBytes`: required, 1–253 inclusive, measured on each raw input
  before normalization, including a trailing dot
- `Policy.Location *time.Location`: manual mode requires non-nil, not
  `time.Local`, and not a location named `Local`; automatic mode ignores it

The limits are newly agreed defensive module bounds, not evidence of historical
system capacity. There are no inferred defaults or zero-as-unlimited options.
This is a Go value API, not a JSON/HTTP decoder. The future adapter must preserve
presence and decide null handling and legacy single-string adaptation explicitly;
the parser never guesses comma/space delimiters or reads process timezone state.

Automatic mode requires at least one server and rejects any present Date field.
Manual mode requires Date and rejects any present NTPServers field, even empty.

## Manual wall-clock interpretation

Date must be exactly 19 ASCII bytes `YYYY-MM-DD HH:mm:ss`, a valid Gregorian
date in years 0001–9999. No fractional seconds, offsets, surrounding whitespace,
leap seconds or normalization of invalid dates is accepted.

The supplied location is used to find every instant matching the wall value.
No match returns `nonexistent_time` (gap); multiple matches return
`ambiguous_time` (fold). The algorithm considers actual timezone offsets,
including half-hour and whole-day transitions, rather than assuming a one-hour
change. It scans zone intervals over the complete signed-32-bit TZif offset
range and round-trips distinct offsets; a `time.Date` seed also covers
`time.FixedZone` offsets outside that range. It uses Unix seconds, not
UnixNano/Duration arithmetic over the full date range.

The Go 1.27.1 POSIX-footer leap-year nonadvancing `ZoneBounds` edge is handled
by advancing to the next UTC year once both yearly transitions are past.
Tests include a future leap-year Dec 31, Lord Howe gaps/folds and Apia's skip day.
Timezone data/version remains a caller/deployment input; tests embed Go tzdata
so missing host zoneinfo cannot silently skip the transition fixtures.

Successful manual Config contains SyncType=1, unchanged canonical ManualDate,
the corresponding ManualTime in UTC and the explicitly supplied Timezone name.
Other fields are zero. A valid year-0001 instant may be `time.Time{}`: success is
determined by the error list, never by `ManualTime.IsZero()` alone.

## NTP host syntax and normalization

Each entry is either an IPv4/IPv6 literal accepted by `net/netip`, or an ASCII
multi-label DNS hostname. IP literals are canonicalized with `Unmap().String()`.
Zones, brackets and ports are not accepted. A syntactically valid address is
not network authorization or a statement of reachability.

DNS labels are 1–63 bytes, contain ASCII letters/digits/hyphens and may not
start/end with a hyphen. At least two nonempty labels are required. One trailing
dot is allowed and removed; the normalized hostname is lowercase and at most
253 bytes. Non-ASCII/IDN Unicode input, URLs, paths, ports, wildcards, underscores and
whitespace are rejected. ASCII punycode labels are treated as ordinary DNS
syntax; no IDNA conversion occurs. Failed IP parses composed entirely of numeric
DNS labels (for example `127.1` or `999.1.1.1`) are rejected rather than
reinterpreted as DNS. Single numeric labels such as `2130706433` are also rejected.
The raw-byte policy still applies first: a 253-byte name plus a dot exceeds
even the largest allowed raw input limit.

Normalization preserves input ordering. Duplicate normalized values fail,
including DNS case/trailing-dot differences, compressed IPv6 alternatives and
IPv4-mapped IPv6 versus IPv4. No list is silently deduplicated or truncated.
Successful automatic Config contains SyncType=0 and a newly allocated server
slice, with other fields zero. Output never aliases the input slice.

## Errors, determinism and integration boundaries

On any error Config is entirely zero. Each FieldError has a stable Field path
and Code with no reflected raw value. Validation order is deterministic:

1. `policy.maxServers`, `policy.maxServerBytes` (`invalid_policy`); stop if invalid
2. `syncType` (`required` / `invalid_mode`); stop if invalid
3. Manual: `date` (`required`, `invalid_date`, `nonexistent_time`, `ambiguous_time`),
   `policy.location` (`explicit_timezone_required`), `ntpServers` (`mode_conflict`)
4. Automatic: `date` (`mode_conflict`), `ntpServers` (`required` / `too_many`),
   then `ntpServers[i]` in input order (`too_long`, `invalid_host`, `duplicate`)

An invalid count suppresses per-entry validation. Invalid policy or mode also
suppresses dependent checks. Input and Policy are never modified; caller-owned
values must not be mutated concurrently during a call.

No database, HTTP, authentication, current-time query, persistence, execution,
frontend, shared schema/migration or CI configuration is changed. Those layers
must still enforce permission, trust, availability, audit and system privileges.
No real system clock, NTP service, network, AD or production validation is claimed.

## Tests

Named unit tests cover presence/conflicts; policy boundaries; strict Gregorian
dates; explicit zones; DST gap/fold/unusual transitions; DNS/IP normalization and
duplicates; illegal hosts and length boundaries; deterministic error order and
input/output isolation. `FuzzParseHostDeterminism` covers arbitrary host bytes.

Run `go test -race -count=1 ./internal/timeconfig`, `go vet ./internal/timeconfig`,
and the existing repository `make check`. Actual run results and exact-head CI
are reported in the draft PR, separately from real database/browser evidence.
