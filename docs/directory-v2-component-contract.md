# Versioned directory components

This checkpoint implements only an independent dictionary 2 object/envelope codecs, accumulator and fixed extended LDAP transport. It does not register a product task, grant a purpose, migrate storage, add HTTP routes or expose a UI. Existing dictionary 1 behavior remains callable and exact. Real database/browser/AD acceptance remains open.

## Intended integrated identity

The later producer will use a separate exact kind/purpose domain.directory_read.v2 with task payloadVersion 1 and dictionaryVersion 2. Existing domain.directory_read and its eight-field payload remain unchanged. Existing grants are never upgraded or copied. Later admission requires the existing master directory gate plus a new default-false v2 gate and a new exact-purpose grant. Planned versioned routes are /api/directory/v2/* and /api/directory-credential-use/v2/*; current strict clients never receive widened records or new kinds. None of that registration is implemented in this component checkpoint.

The broader integration must reuse the current fenced authority/use/dependency lifecycle through a closed two-profile selector, including exact original-executor cleanup after revocation or lease loss. It needs an atomic schema 16 migration, historical v1 provenance without rewriting bodies/hashes, collision and opened-use gates, current-reader authorization and succeeded-only visibility. A schema 15 binary will not be promised readiness after schema 16 migration. These are future gates, not completed work.

## Exact factual dictionary

The v2 request is fixed in this order: objectGUID, objectClass, sAMAccountName, userAccountControl, objectSid, mail, description, whenCreated. DN remains the entry DN. No arbitrary attributes, filter, base, port, referrals, ranged options or controls are accepted. The current user-or-group subtree filter still includes computer class inheritance; it does not prove physical-device identity.

Validate the full eight-name allowlist, counts and case-folded duplicates before splitting core and supplemental sets. Reuse current Decode for the core and DecodeSupplemental for the additional fields. This fixed user/group/computer SAM profile permits only one description when supplied; the generic supplemental decoder's multivalue behavior is unchanged. Missing is distinct from present-empty. Unsupported time/representation profiles remain distinct from malformed responses; no inferred status, risk, login, address, relationships or business aliases are added.

Workbook evidence and primary sources remain in [supplemental-directory-fields-contract.md](supplemental-directory-fields-contract.md). Current v1 observations mean the additional fields were not collected. Later v2 null values mean requested but not returned; no reason is guessed and no data is backfilled.

## Lossless stored and public values

The stored v2 object has exact base and supplemental keys. Base is the unchanged six-field Object. Supplemental contains exactly objectSidBytes, mailBytes, descriptionBytes and whenCreatedBytes, represented as raw byte slices and a byte-slice array. JSON uses ordinary padded base64 strings/arrays; nil is null. Preserve original binary SID, supported timestamp bytes and raw text. Base64 is encoding, not encryption.

A public projection retains the six existing keys and adds objectSid, mail, description and whenCreated. SID/time come from the reviewed decoder; mail and description preserve accepted UTF-8 bytes, including NUL/controls. Public creation time is UTC with whole seconds and a year-safe four-digit representation. The raw stored encoding is never exposed as the user-facing field value.

Stored reads must check raw object JSON≤64KiB and base JSON≤32KiB before typed decoding, exact keys and types, and encoded/decoded byte caps before allocation. Base64 encoded limits are 40/1368/5464/24 bytes for SID/mail/one description/time. Reject noncanonical encoding, duplicate/unknown fields, wrong types, present zero bytes, a nil inner description, or a present empty description array. Decode and revalidate semantics, then require byte-for-byte canonical re-encoding. A failure returns no partial object.

This representation keeps raw NUL out of stored JSONB strings: PostgreSQL rejects direct \u0000 in JSONB, while raw byte values are ASCII base64. The later authoritative snapshot remains canonical bytea with an independently checked digest and version/source/count guards. Do not cast the public raw-text projection to JSONB. Actual PostgreSQL NUL/control roundtrip is a mandatory future test and is not established by this component's local JSON tests. [PostgreSQL JSON types](https://www.postgresql.org/docs/17/datatype-json.html)

## Resource and lifecycle limits

Keep existing bounds: 10,000 runtime rows, 100 pages, 1,000 entries/page, 16 MiB aggregate encoded objects/cookies, 64 KiB cookies, 128 KiB LDAP messages, 120-second reader and 3-second phase/page deadlines. These are local safety bounds, not product capacity acceptance. Charge each v2 object by the greater of stored-object and public-object encoded sizes, plus received cookies. The independent versioned envelope codec requires dictionaryVersion 2, validates source/DN scope/count/digest/canonical bytes, and enforces the full 16 MiB body cap. It does not insert data or prove SQL authority. Future serialized read pages also need an explicit bound.

Accumulator cookies are opaque, exact and bounded; repeated cookies do not imply completion. Empty response cookie terminates success. Duplicate GUIDs, wrong request cookie, any invalid entry, cancellation or resource failure discard the complete accumulated result. Deep-copy returned objects, buffers and cookies; clear owned raw bytes/cookies on failure/discard. Do not claim immutable Go strings or arbitrary caller buffers are erased.

V2 aggregate row/byte/cookie/page budget stops report a fixed directory_limit_exceeded code; unsupported supplemental representations use unsupported_directory_profile; malformed data/protocol remains distinct. V1 error classifications remain unchanged.

Transport keeps verified TLS, existing egress/RootDSE/domain checks, live authorization before all existing stages/pages/return, and no automatic retries. Public v1 and v2 functions/configurations select fixed internal profiles; callers cannot provide serializer/attribute/filter callbacks. Retain password, packet, cookie and partial-result cleanup on ordinary failure, cancellation and panic. No actual AD operation is authorized here.

## Verification boundary

Required local evidence: exact old JSON/request goldens and parsers; v2 raw/public canonical roundtrip with NUL/CRLF/tab/non-BMP and year 0001; strict base64/shape/bounds/cardinality failures; independent aliasing/cleanup/cookie-budget/fuzz tests; actual synthetic TLS/BER exchange for both transport modes and original mode regression; independent source review and final make check. Fixtures must not fabricate API observations or claim database/browser execution.

Later integration still needs migration/current-purpose/ledger tests, actual worker→TLS fixture→PostgreSQL→protected API/UI evidence, unchanged v1 visibility and grants, real read-only/session/cancel/recovery flows, and real AD/Windows/source-field/capacity acceptance. Product acceptance remains 0/209 and code publication remains paused.

## Verified local checkpoint (2026-10-08)

Final make check passed Go race/vet/build, fifteen Python contracts, 559 frontend tests, type/build and npm audit zero vulnerabilities. All-package integration-tag compilation and vet passed; these did not run database cases. Current focused codec/LDAP race passed1.118s/38.623s, and envelope/legacy observation race passed4.020s. Bounded fuzz passed63,035 stored-codec,46,752 raw-codec and57,589 wire-parser executions. Synthetic in-process TLS tests exercise both modes and actual BER; no real AD connection was made.

Independent final source review found no remaining confirmed defect or v1 regression. Earlier findings were fixed: private validated cloning, temporary raw/base buffer clearing, exact decoded-size preflight, distinct v2 resource/profile failures, and source length checks before DNS normalization. Envelope tests cover lossless NUL/year0001, ownership/discard, corruption, scope, source/aggregate limits and strict v1/v2 isolation. Handwritten BER/base64 golden fixture typos were corrected before the final checks; no assertion or limit was removed.

This is still a non-wired component. No schema migration, task/grant registration, HTTP/UI exposure, actual PostgreSQL JSONB roundtrip, browser acceptance or new credential authority is established.
