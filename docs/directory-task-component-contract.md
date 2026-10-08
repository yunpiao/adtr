# F12 directory task authority component

Current local integration status (2026-10-07): migration15, production registry/HTTP/task-authority wiring and default-denied purpose APIs are now present in source. The compiled consumer capability is true, while the independent deployment switch remains defaultfalse. The component-stage limitations below describe their original checkpoints; actual PostgreSQL, browser and AD execution remain unverified. Current UI contract: [directory-api-contract.md](directory-api-contract.md).

The original586a675 checkpoint was unregistered. The current local integration installs the fragments in migration15 and registers the HTTP/worker paths. Compiled capability is true; deployment remains independently default-off. It follows the bounded transport/grant checkpoint and prepares the task boundary for AD-F-034.

## Independent identity

The reserved kind/purpose is exactly `domain.directory_read`, payload version1. Its eight pinned scalar fields are credentialSource=operation_account, connectionRevision, connectionCredentialGeneration, accountId, accountCredentialRevision, grantRoleId, grantRevision and policyRevision. The payload is bounded, exact and duplicate-free, with canonical positive revision strings and a lowercase SHA-256 policy revision. Caller base/filter/attributes and diagnosticGeneration are rejected. B2 connection-test payloads cannot substitute for directory task identity.

The current-authority helper rejects wrong kinds, versions and payload pins before database work. It checks the independent runtime deployment gate, current policy, source/binding/pair incarnations and the exact directory-purpose grant. Role/domain/tenant/epoch/fencing checks must also run through the engine's fenced transaction boundary. Account metadata renames and unrelated diagnostic tasks do not change these pins.

## Credential lifetime

A separate `domain_directory_task_uses` ledger records reserved/opened/quiesced state and immutable task/actor/tenant/domain/account/binding/policy/grant pins. The corresponding operation-account dependency has its own typed kind and unique task identity. Reservation, task and dependency must commit atomically. Opening must match a currently running first attempt, its valid lease and live authority; decryption cannot occur before the fenced opening transaction is known to have committed.

Cleanup compares the engine-issued exact executor-return witness with the immutable original payload and saved opener. It does not require a still-current role, source, grant or lease. A terminal task, revocation, source detach or lease expiration does not prove an opened executor has stopped. The separate terminal reconciler retires only never-opened reservations. Cleanup remains fail-closed on inconsistent evidence, and pending opened use continues to block account pair replacement/removal.

The migration15 dependency fragment preserves historical B2 immutability, adds directory revision pins and a unique object-id index, and refuses a pre-existing reserved dependency kind. The ledger fragment also refuses unknown prior directory tasks. Migration15 installs permission/purpose/dependency/ledger changes atomically under an exclusive schema gate and preflight every applicable opened-use ledger. The historical migration and existing B2 table/trigger contents are not rewritten.

## Remaining work

A local executor now calls the real bounded reader and stages a complete validated observation through an unapplied storage fragment. Admission/API, production registry and maintenance wiring, audited controls, the migration upgrade path and browser UI are now implemented locally. Database component test code and compilation do not establish PostgreSQL execution. Real worker/database/browser/AD behavior, source parity and complete product acceptance remain open.

## Local evidence

Focused domains/auth/store race tests, normal vet, whole-repository integration-tag compilation and vet passed. Six new ledger PostgreSQL component groups were invoked and stopped at fixture setup for absent `ADTR_TEST_DATABASE_URL`; no group ran against a database. The fixture uses a synthetic executor with no credential resolution or network activity and obtains its return witness from the actual task engine. This tests a proposed ledger boundary once executable, not the directory producer. Frontend tests were not rerun for these unregistered backend components.
