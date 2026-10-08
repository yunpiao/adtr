# F12 bounded directory observation: local implementation contract

Refs F12 / AD-F-034; AD-F-035 incremental synchronization and AD-F-041 address relationships remain separate. The reviewed source group requires users/groups/computers, explicit TLS modes/timeouts/authority, per-type field dictionaries, bounded paging and honest deletion semantics. It does not freeze a complete field dictionary or target capacity.

First durable increment is a pure field decoder and bounded page accumulator. It performs no network access, database write or credential use. No route or empty-success product flow will be exposed before the real producer, authority and persistence exist.

A later producer must use a new explicit default-denied directory-read purpose, never a connection-test grant. Its admission, each page and publication require live role/domain/account/binding checks and executor-return acknowledgement. Existing custom-pair authority is not implicitly broadened. The B2 diagnostic ledger's latest-test coupling must be resolved before a new consumer is wired.

Initial dictionary: immutable objectGUID binary16; bounded distinguished name; bounded multivalued objectClass; optional single sAMAccountName and uint32 userAccountControl. Classification is explicit user/group/computer, with computer checked before its inherited user class. Missing optional values remain missing, not empty or zero. Unknown attributes and partial/ranged attributes are not silently accepted. Group membership, ACLs/SIDs, timestamps, address resolution and complete source-system parity remain unimplemented in this increment.

Page accumulator: explicit positive row/byte/page/cookie caps, opaque binary cookies, completed terminal empty cookie only. Cookies are opaque and may repeat; explicit page caps bound no-progress loops. Duplicate object identities, invalid objects or exceeded limits fail the whole observation. No partial result may be published after failure. No network retries or page replay are inferred. Local atomic publication is distinct from directory point-in-time consistency; absence never becomes a deletion claim.

Primary references:
- Microsoft objectGUID schema: https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adls/f5f15ec2-427e-4ebe-bb64-2493cf1d032f
- Microsoft object identity: https://learn.microsoft.com/en-us/windows/win32/ad/object-names-and-identities
- RFC2696 paged results, including directory changes during paging: https://datatracker.ietf.org/doc/html/rfc2696

Production API/worker/database/browser and eight-version Windows/AD validation are not yet implemented or passed for F12. Pure module tests are not directory synchronization acceptance.

Binary GUID byte-order reference: https://devblogs.microsoft.com/oldnewthing/20190426-00/?p=102450

Local checkpoint validation (2026-10-07): full Go lint/race/vet/build and nine Python contracts passed; final focused race/vet passed after review fixes. A final two-second decoder fuzz run passed43,432 executions. Independent review rechecked allocation-before-bound and Unicode-descriptor issues; no outstanding finding in this pure-module scope. Frontend/AD/database/browser execution is not claimed. The next authority integration boundary is documented in directory-authority-plan.md and is not implemented by this checkpoint.
