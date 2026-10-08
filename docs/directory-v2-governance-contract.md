# Dictionary 2 exact-purpose governance components

This checkpoint prepares an explicit domain.directory_read.v2 purpose, immutable task profile and shared credential-use ledger. It does not install migration 16, register a task/consumer or expose routes. The application schema remains15 and the new compiled consumer capability remains false. Existing v1 wrappers must continue to function against schema 15.

The new kind/purpose uses task payloadVersion 1 with the original eight immutable pins plus dictionaryVersion 2. Old payload validation remains exactly the original eight-key v1 shape. The internal profile selector is closed to v1/v2; no caller supplies arbitrary kinds, purposes, SQL identifiers or field dictionaries. Existing grants are never copied, upgraded or treated as v2 authority. Unknown purpose/profile combinations fail closed.

## SQL fragments and future installation

New unapplied fragments are ordered DirectoryV2PurposeSchema, DirectoryDependencyV2Schema and DirectoryUseV2Schema. Historical SQL constants remain unchanged. Future production migration 16 must run all required fragments with the observation/audit extensions in one transaction under the existing exclusive schema gate, reject reserved-kind/purpose/column collisions and every opened use, and stamp the version only after complete success.

The purpose fragment widens only exact purpose constraints, eligibility and grant guarding. It retains administrator proof context, role/resource/tenant/epoch protections, current account-pair revision, immutable grant identity/incarnation, receipt purpose and automatic revoke semantics. It inserts no grant and duplicates no permission trigger.

The ledger gains explicit dictionary provenance. Historical v1 INSERTs omit the new column and remain valid before migration; the later default is 1. Any present provenance must be validated, not normalized from an explicit null to legacy absence. V2 INSERTs require provenance 2. The exact kind/payload/purpose/dictionary/dependency tuple is enforced; no v1 task or receipt may be reinterpreted as v2.

Reserve/use/dependency ownership remains atomic. Opening requires known commit before decrypting; all existing current-authority/fencing rules remain. Actual engine-issued return proof and the original immutable opener are necessary for quiescence after revocation, rebind or lease loss. Never-opened terminal reservations are separate; terminal state, elapsed time, disabled collection and process loss are not proof that opened credentials stopped.

Old fixed wrappers and maintenance remain v1-only at this checkpoint. Later integration must explicitly dispatch the two closed profiles through one shared lifecycle and preserve fair bounded maintenance. No new network consumer is activated by these components.

## Evidence boundary

The integration-only MigrateDirectoryV2BaselineForTest helper installs historical schema 15 through the real migration sequence for absence/collision tests. It cannot downgrade a database and is excluded from production. Runtime fixtures must use the current production migrator rather than altering version stamps to make incompatible fragments appear installed.

Required checks include known/unknown and pairwise purpose isolation, exact payloads, matching dependency/provenance, original-opener cleanup, unchanged schema 15 wrappers, and historical SQL preservation. Database-tag compilation does not prove SQL guards, races or migration behavior. Actual PostgreSQL, full v2 migration/producer/API/UI, browser, AD/Windows and product acceptance remain open. Publication remains paused and acceptance remains 0/209.

## Local verification (2026-10-08)

Final make check passed Go race/vet/build, fifteen Python contracts, 559 frontend tests, TypeScript/build and npm audit zero vulnerabilities. All-package integration-tag compilation and vet passed. Independent review reran credentialuse/domains race successfully and found no unresolved source finding after strict executor-stub and typed-provenance corrections. The stub now asserts the new exact legacy kind/provenance arguments without removing opening/commit/decryption safety cases.

The six purpose and seven ledger database groups were invoked without a database DSN and failed explicitly at fixture initialization. No SQL behavior, migration race or actual PostgreSQL pass is claimed. Current component ledger fixtures intentionally assert schema 15; later migration integration must adapt them to the real production upgrade rather than silently changing a version stamp. Complete reserved-purpose/audit/function/observation collision gates, observation/audit schema extensions and atomic migration 16 installation remain future work.
