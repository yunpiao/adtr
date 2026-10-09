# Dictionary 2 local integration checkpoint

2026-10-08. Refs #25 / AD-F-034 and the factual-field groundwork for #30. This records implementation and local PostgreSQL verification at code commit `df3b57543652b7244103b9884036436868c56150` (tree `88effc9e4f542182d4a0c17cc3c155970978a2ac`). The fixes remain unpublished; PR81 remote head `000f97353c4cfcefdb4f2759a111bfdabad96cb9` failed migration startup. This does not claim browser execution or full AD-F acceptance.

## Implemented boundary

Schema16 atomically installs separate dictionary2 purpose/dependency/use, observation and audit guards. Upgrade refuses opened credential uses, reserved collisions and unexpected historical schema shapes. Historical body/hash/grant records stay unchanged. Two independent source-review findings were fixed: omitted legacy audit/receipt/grant guard verification, and a duplicate-FK loophole in the expected foreign-key multiset. Negative fixtures cover both; canonical SQL fixtures include quotes, backslashes, HTML characters and Unicode separators.

The registered `domain.directory_read.v2` producer reuses fenced authority and original-executor cleanup with the reviewed fixed eight-attribute reader. It stores raw supplemental bytes in canonical base64 envelopes, while protected v2 routes expose the factual SID/mail/description/creation-time projection. Both default-false deployment gates and a separate live v2 grant are required. Stored readers need current data authority independently of credential-use grants; cancelled, failed, stale-source and cross-profile observations remain unavailable. V1 payloads, routes, grants and stored bytes are preserved.

Separate UI, strict parsers and recovery identifiers enforce current-session privacy and safe text display. Request bodies cannot choose an LDAP dictionary. Responses are bounded to8MiB before output and during browser reads. Cancellation keeps proof cleanup and durable error notices; unknown responses preserve the original actor/profile operation key. The generic task UI and API do not bypass dedicated controls or expose private results.

## Checks actually executed

- Full local `make check`: Go race/vet/build, 35 Python contracts, 734 frontend tests, TypeScript/build and dependency audit with0 vulnerabilities
- Integration-tag compilation/vet and focused producer, configuration, authorization and three-profile maintenance checks
- Actual synthetic TLS/BER fixture and production v2 reader race tests in both modes, including empty/slow/cancel paths, exact supplemental bytes, NUL/year0001 and legacy byte goldens
- Independent backend and frontend source reviews; confirmed findings fixed and rechecked

- Real PostgreSQL17.6, race-enabled: all non-auth integration packages passed (19 discovered, 18 containing tests); all four official auth shards passed (61 + 61 + 61 + 60 = 243 top-level tests). Each shard used a fresh synthetic cluster, preserved its exit status and confirmed shutdown
- Migration regression checks now cover constraint-trigger classification, exact trigger protections, valid PL/pgSQL CASE syntax, unmatched v2 audit availability and unchanged historical projections
- Strict HTTP fixtures now cover the documented consumerEnabled envelope, global actor/key collisions without MFA consumption, exact-purpose recovery, genuine task-finalization audit availability and deliberately simulated out-of-band corruption with restored guards

The initial combined database run was interrupted during auth shard1 by an executor restart; the later individual shard results above supersede that incomplete attempt. Shard3 ran at `b39dd10a9b10f8f956419f8f227421aaaa400918`, before the legacy corruption-fixture cleanup; that affected case passed again in shard4 at the final code checkpoint. Production code is identical across those checkpoints. Original failed logs remain retained alongside successful reruns.

Commands: `python3 scripts/run_integration.py --suite all` for the non-auth group and `python3 scripts/run_integration.py --suite auth --shard N` for each auth shard, wrapped in an ephemeral PostgreSQL17.6 lifecycle. The original 10-minute limits and all assertions remain. This local build uses Debian/glibc with C/UTF8 and differs from CI's Alpine image. Docker is not available on this executor; native Chromium previously hit process/Unix-socket restrictions, so no browser pass is claimed. Handwritten observation executors in HTTP/storage tests are explicitly synthetic and cannot replace the real producer chain.

## Required remote gates

CI retains all existing suites and adds directory-v2, directory-v2-controls and directory-v2-readers, keeping maximum browser parallelism2 and the final aggregate verify. All28 jobs are required for this increment:22 browser suites,4 authentication integration shards, contracts and verify. Real migrated PostgreSQL and browser/API/worker/synthetic-LDAP execution must establish the new schema/producer/UI behavior before merge; local compilation and source review are insufficient.

Real AD/eight Windows versions, source-system field/filter/export parity, capacity/recovery and production gates remain open. F14 business asset pages and relationships are not delivered merely by exposing these factual observations. Product acceptance remains0/209. The previous25-job successful PR77 checkpoint and its main integration are separate evidence, not v2 runtime evidence.

Contracts: [API and authority](directory-v2-api-contract.md), [fixture](directory-v2-test-fixture.md), [component history](directory-v2-component-contract.md), [governance history](directory-v2-governance-contract.md).
