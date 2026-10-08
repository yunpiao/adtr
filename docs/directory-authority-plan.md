# F12 separate directory-read authority boundary

Current local integration status (2026-10-07): migration15, production registry/HTTP/task-authority wiring and default-denied purpose APIs are now present in source. The compiled consumer capability is true, while the independent deployment switch remains defaultfalse. The component-stage limitations below describe their original checkpoints; actual PostgreSQL, browser and AD execution remain unverified. Current UI contract: [directory-api-contract.md](directory-api-contract.md).

The pure decoder/pager is implemented separately. Transport, immutable deployment configuration and the purpose-specific grant extension are now integrated locally by migration15. The compiled capability is true; the independent deployment switch remains defaultfalse. Separate directory task pins, a credential-use ledger fragment, an unregistered executor and a complete-observation storage fragment now exist locally. Migration/registry/HTTP/UI code is present; actual PostgreSQL, browser and AD execution remains unverified. This document fixes the remaining integration boundary; it does not claim a functioning directory producer.

Existing B2 is connection-test-specific: account_admission.go increments diagnostic_generation and replaces latest_test_task; account_executor.go rechecks both on every phase/publication; account_reference_schema.go and account_use.go pin the connection-test kind/purpose. Merely adding a purpose enum or reusing that admission would corrupt diagnostic identity and silently broaden authority.

Implementation choice:
- Preserve the existing B2 kind, payload and diagnostic ledger semantics unchanged
- Introduce a separate explicit directory-read purpose, default-denied even for administrators, in the existing account/domain/role grant model; no existing grant is copied
- Initially use only an already-bound registered operation account. Custom connection-test credentials do not automatically authorize directory reads
- Introduce a separately typed directory-use ledger and typed dependency, pinning task/actor/tenant/domain/account/binding/pair/policy/grant revisions. It must not depend on latest connection-test task or diagnostic generation
- Reuse the engine's immutable OnQuiesced witness and retained cleanup retry. The consumer must compare its own exact saved opener, and account replacement/removal stays blocked until actual executor-return acknowledgement
- Extend migration preflight to cover every applicable opened-use ledger, while retaining the current guard for historical versions. Refuse unknown reserved-kind collisions; no fabricated opener evidence, force release, or age/terminal-state shortcut
- Validate live directory permission, domain scope, explicit purpose grant, source/pair/policy pins at admission, each network page and publication. Reject referrals and arbitrary caller base/filter/attribute controls
- Publish a complete bounded local observation only after all pages and final authorization succeed; no partial visibility, no deletion inferred from absence, no point-in-time AD snapshot claim

This requires coordinated grant SQL guards/epoch invalidation, dedicated task authority, immutable result storage, audit, HTTP/UI and real migration/worker/browser tests. Incremental synchronization, deletion authority, complete field parity, managed-service-account classification, target capacity and eight-version Windows validation remain open. Nothing in this plan creates real credentials, changes a real directory, or grants runtime production access.
