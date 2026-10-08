# Supplemental directory field component

This pure component prepares a bounded field dictionary for F12/F14, Issue #30. It is not connected to the LDAP request, current Object type, accumulator, canonical observation storage, task authority, API, UI or asset export. Existing observations still contain only the original dictionary. No historical bytes or hashes are rewritten, and no additional field is collected by the running application.

## Workbook scope

- objectSid: FLD-0348/0380/0390/0434/0478/0505; binary directory evidence is present in the source workbook
- mail: FLD-0349 email and FLD-0404 mail; the business email alias remains a separate mapping decision
- description: FLD-0391 scalar describe and FLD-0508 description array; preserve observed multiplicity without choosing or joining a value
- whenCreated: FLD-0351/0376/0437/0479/0510; directory creation time, with no inherited zero default or assumed legacy output format

The component does not interpret updateTm as whenChanged. The workbook leaves sort/time semantics ambiguous, while whenChanged is nonreplicated. It also does not infer login, lockout, risk, membership, group scope, IP addresses or unavailable counts. Current UAC is not sufficient to populate the business active/disabled/locked enum.

## Independent value contract

DecodeSupplemental accepts only an explicitly selected set of at most four attributes. Names match case-insensitive ASCII objectSid, mail, description and whenCreated. Duplicate descriptors, unknown names, Unicode lookalikes and descriptor options such as range/binary are rejected. It does not silently filter a larger LDAP entry.

Omitted fields remain nil. This means not present in the supplied set; it does not prove an empty AD value or distinguish not requested from hidden by ACL. Present empty or malformed values fail rather than becoming missing. Any error returns a zero aggregate. Fixed errors and aggregate formatting must not expose supplied data. All successful values are owned copies; input mutation cannot change them. No I/O, lookup, authorization, persistence or fallback computation occurs.

Explicit local safety limits must be checked before value copies: mail at most 1024 UTF-8 bytes and 256 UTF-16 code units; each description at most 4096 bytes and 1024 UTF-16 units, at most 16 values; aggregate raw values at most 68 KiB. These local representation limits are not a claim about full AD schema or product capacity.

## Binary SID

Revision 1, a six-byte big-endian identifier authority and unsigned little-endian 32-bit subauthorities are decoded with exact length/count checks. Generic SID storage permits at most 15 subauthorities/68 bytes. The canonical textual principal profile requires at least one subauthority; authority below 2^32 is decimal and larger authority uses 0x plus 12 hex digits. Subauthorities use unsigned decimal without redundant leading zeroes. No authority-prefix privilege, membership or domain inference is made.

The objectSid attribute wrapper is tighter: exactly one binary value, at most 28 bytes, giving 1–5 subauthorities in this profile. A structurally valid longer generic SID must not be accepted as this AD attribute. GUID remains the existing observation identity. Textual SID bytes are not a binary attribute substitute.

Sources: [MS-DTYP binary SID](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-dtyp/f992ad60-0fe4-4b87-9fed-beb478836861), [SID string syntax](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-dtyp/c92a27b1-c772-4fa7-a432-15df5f1b66a1), [AD objectSid schema](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-ada3/afac8414-c614-4c6a-b316-41f5978308bd).

## Text and cardinality

Mail is one nonempty valid UTF-8 value. Preserve case, whitespace and valid UTF-8 control characters; no email regexp, lowercasing, trimming, splitting or deliverability claim. The schema's upper range is 256; the UTF-16/byte limits above are the conservative local character policy.

Descriptions remain an array. Preserve each accepted string, including documented line breaks/tabs, sort owned copies because LDAP multivalues are unordered, and reject exact duplicate bytes. No case-insensitive normalization, deduplication or scalar alias mapping occurs. Invalid UTF-8 and present-empty values are rejected. All otherwise valid UTF-8 bytes, including NUL and control characters, are preserved; this raw decoder does not inherit an account-name blacklist. Safe display and export rendering are separate future requirements. The generic decoder does not infer an object's class: future SAM-managed user/group/computer integration must separately enforce the single-description rule and never silently select a first value.

Sources: [mail schema](https://learn.microsoft.com/en-us/windows/win32/adschema/a-mail), [description schema and SAM restriction](https://learn.microsoft.com/en-us/windows/win32/adschema/a-description), [multivalue semantics](https://learn.microsoft.com/en-us/windows/win32/adsi/single-vs--multiple-value-attributes), [RFC4517 Directory String](https://datatracker.ietf.org/doc/html/rfc4517#section-3.3.6).

## Creation time profile

whenCreated is system-set replicated directory creation time, not import/observation/first-login time. The initial component accepts only exactly YYYYMMDDHHMMSS.0Z, seventeen ASCII bytes, validated calendar dates, years 1–9999, UTC and whole seconds. A valid year 0001 must remain a nonnil observed value even though Go IsZero returns true for that instant.

This is a partial GeneralizedTime profile. Official syntax also permits offsets; unsupported forms must not be labelled proof of malformed AD data, rounded or silently interpreted. Malformed calendar dates within the supported profile fail separately. RFC4517 permits leap-second syntax with seconds 60; an otherwise-valid such value is unsupported by this Go time profile, rather than labelled malformed or normalized. Seconds above 60 remain malformed. No current-time or Unix-epoch substitution is permitted. Output formatting for a future endpoint remains a separate contract.

Sources: [whenCreated](https://learn.microsoft.com/en-us/windows/win32/adschema/a-whencreated), [GeneralizedTime syntax](https://learn.microsoft.com/en-us/windows/win32/adschema/s-string-generalized-time), [LDAP representations and time granularity](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/7cda533e-d7a4-4aec-a517-91d02ff4a1aa), [nonreplicated whenChanged](https://learn.microsoft.com/en-us/windows/win32/adschema/a-whenchanged), [RFC4517 GeneralizedTime](https://datatracker.ietf.org/doc/html/rfc4517#section-3.3.13).

## Integration and evidence gates

Before collecting or exposing these fields, design explicit dictionary/version provenance and a version-aware canonical storage/API path. Preserve v1 bytes/hashes and strict clients; distinguish v1 not-collected from a requested field omitted in a later observation. Update request/BER allocation limits, publication revalidation, cleanup, storage bounds and actual chain tests together. Never backfill fabricated values or mutate immutable observations.

The pure component needs boundary/golden/aliasing/redaction/fuzz tests, unchanged current dictionary/packet/canonical-storage tests, and independent source review. Real PostgreSQL, browser, AD, eight-version Windows, full field parity and export/filter/capacity gates remain open. All 209 product acceptance remains 0/209.

## Local verification (2026-10-08)

Final make check passed all Go race/vet/build checks, fifteen Python contracts, 559 frontend tests, TypeScript/build and zero npm audit vulnerabilities. Focused package race/vet passed. Bounded fuzz passed 180,534 SID and 110,861 supplemental executions. Exact legacy JSON and LDAP request goldens, supplemental-name rejection by both legacy decoders, and existing canonical observation roundtrip/corruption checks passed.

Independent source review found and corrected the leap-second error classification; final review has no unresolved finding. Only this separate component, tests and documentation were added; no existing production caller, transport, persistence or authority was changed. These are local component/compatibility results, not DB/browser/AD/Windows or F14 product acceptance.
