# DNS/IP simulated contract v1

Scope: AD-F-041 (device/address mapping model) and AD-F-053 (DNS/IP XLSX export),
[Issue 25](https://github.com/yunpiao/adtr/issues/25) and
[Issue 30](https://github.com/yunpiao/adtr/issues/30). User instruction on
2026-11-01 permits explicit simulated business assumptions. This contract is a
new assumed model, not recovered legacy semantics or real AD acceptance.

## Implemented boundary

`internal/dnsipassets` validates and queries caller-supplied synthetic records;
`internal/dnsipxlsx.WriteRecords` writes the selected records to an actual XLSX.
No DNS/network/LDAP lookup, persistence, task scheduling, HTTP route, authorization
grant, or UI is introduced. AD-F-041 live synchronization remains unimplemented.
Product acceptance remains 0/209. The caller must authorize the domain before
calling either query or export; these pure libraries are not access controls.

Every record requires `simulation=true`, `version=dnsip-sim-v1`, and nonempty
source/observation/ID tokens (ASCII letters, digits, underscore, dash, dot;
maximum 128 bytes). These required fields prevent silently treating unmarked
real data as this assumed model. Removing them loses provenance separation.
No secret or raw invalid input is included in validation errors.

Domain and DNS name are lowercase ASCII DNS labels (1–63 bytes each, total at
most 253, no leading/trailing dash); a single terminal dot is removed. Unicode
IDNs, whitespace, empty labels and arbitrary display names are rejected rather
than guessed. IPv4/IPv6 use Go netip canonicalization, mapped IPv6 becomes IPv4;
scoped addresses are rejected. Addresses are supplied facts, not evidence that
DNS resolves to them. Multiple addresses per name are allowed as separate IDs.
Duplicate IDs within a normalized domain/observation are rejected; the same ID
in separate observations is allowed. The complete input is validated, including
rows outside the query scope, before any output is produced.

## Query and export equivalence

Both paths call `Select`: required exact normalized domain and observation,
optional ASCII literal case-insensitive substring of DNS name or canonical IP
(maximum 253 bytes), optional family `ipv4`/`ipv6`. Unknown family values fail.
Sort is DNS name, numerical IP order (IPv4 before IPv6), then ID. List is
zero-based offset with a required 1–1000 page size; out-of-range offset returns
an empty page and the true total. Export includes all matches in the same order,
not merely the visible page. Empty observations produce a header-only workbook.

Input is capped at 10,000 records; this is a safety budget, not a legacy capacity
claim. Selection copies records and sorts only its own output; caller inputs
are unchanged. This bounded implementation retains matching records in memory.

Workbook has one `SimulatedDNSIP` sheet and eight text columns: ID, Domain,
DNS name, IP, Observation, Source, Contract version, Simulation. Thus even
header-only workbooks are explicitly labeled simulated. The low-level `Write`
primitive supports 1–8 columns and 10,000 data rows, using the existing
computerassetxlsx pattern: inline strings, no formulas/hyperlinks/macros/external
relationships, UTF-8/XML validation, 32,767 UTF-16-unit cell limit, literal OOXML
escape preservation, cancellation and short-write errors. `WriteRecords` is the
provenance-enforcing public domain adapter; raw `Write` is only a format primitive.
Any write failure may leave partial bytes: discard the destination and publish
only after successful write and successful destination close.

## Verification

Before edits, existing computerassetxlsx and directoryassets tests passed.
New tests cover normalization, malformed inputs/provenance, duplicate IDs,
domain/observation isolation, stable numerical sorting, list/export selection,
pagination and integer bounds, header-only output, all-eight-column provenance,
formula-like literal text, invalid XML/UTF-8, cell/row/column limits, cancellation,
and sink failure. These are local pure-library tests, not DB/browser/AD acceptance.
