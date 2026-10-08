# Dictionary 2 local integration checkpoint

2026-10-08. Refs #25 / AD-F-034 and the factual-field groundwork for #30. This records local implementation and checks before new integration CI. It does not claim actual PostgreSQL/browser execution or full AD-F acceptance.

## Implemented boundary

Schema16 atomically installs separate dictionary2 purpose/dependency/use, observation and audit guards. Upgrade refuses opened credential uses, reserved collisions and unexpected historical schema shapes. Historical body/hash/grant records stay unchanged. Two independent source-review findings were fixed: omitted legacy audit/receipt/grant guard verification, and a duplicate-FK loophole in the expected foreign-key multiset. Negative fixtures cover both; canonical SQL fixtures include quotes, backslashes, HTML characters and Unicode separators.

The registered `domain.directory_read.v2` producer reuses fenced authority and original-executor cleanup with the reviewed fixed eight-attribute reader. It stores raw supplemental bytes in canonical base64 envelopes, while protected v2 routes expose the factual SID/mail/description/creation-time projection. Both default-false deployment gates and a separate live v2 grant are required. Stored readers need current data authority independently of credential-use grants; cancelled, failed, stale-source and cross-profile observations remain unavailable. V1 payloads, routes, grants and stored bytes are preserved.

Separate UI, strict parsers and recovery identifiers enforce current-session privacy and safe text display. Request bodies cannot choose an LDAP dictionary. Responses are bounded to8MiB before output and during browser reads. Cancellation keeps proof cleanup and durable error notices; unknown responses preserve the original actor/profile operation key. The generic task UI and API do not bypass dedicated controls or expose private results.

## Checks actually executed

- Full local `make check`: Go race/vet/build, 35 Python contracts, 728 frontend tests, TypeScript/build and dependency audit with0 vulnerabilities
- Integration-tag compilation/vet and focused producer, configuration, authorization and three-profile maintenance checks
- Actual synthetic TLS/BER fixture and production v2 reader race tests in both modes, including empty/slow/cancel paths, exact supplemental bytes, NUL/year0001 and legacy byte goldens
- Independent backend and frontend source reviews; confirmed findings fixed and rechecked

The explicit PostgreSQL test attempts stopped at missing isolated test DSN. Docker is not available on this local executor. Those attempts are failures to execute, not database passes. Handwritten observation executors in HTTP/storage tests are clearly labeled synthetic and cannot replace the real producer chain.

## Required remote gates

CI retains all existing suites and adds directory-v2, directory-v2-controls and directory-v2-readers, keeping maximum browser parallelism2 and the final aggregate verify. All28 jobs are required for this increment:22 browser suites,4 authentication integration shards, contracts and verify. Real migrated PostgreSQL and browser/API/worker/synthetic-LDAP execution must establish the new schema/producer/UI behavior before merge; local compilation and source review are insufficient.

Real AD/eight Windows versions, source-system field/filter/export parity, capacity/recovery and production gates remain open. F14 business asset pages and relationships are not delivered merely by exposing these factual observations. Product acceptance remains0/209. The previous25-job successful PR77 checkpoint and its main integration are separate evidence, not v2 runtime evidence.

Contracts: [API and authority](directory-v2-api-contract.md), [fixture](directory-v2-test-fixture.md), [component history](directory-v2-component-contract.md), [governance history](directory-v2-governance-contract.md).
