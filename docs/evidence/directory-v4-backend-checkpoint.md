# Dictionary4 backend implementation checkpoint

2026-10-12, [#151](https://github.com/yunpiao/adtr/issues/151), [#30](https://github.com/yunpiao/adtr/issues/30).
Base main013ba239e5a6af5061a252125a92779c33bf1725. Contract: [dictionary4](../directory-v4-contract.md).

Executed locally:
- Clean-base make check passed
- First integrated make check passed: all Go race/vet/build,196 Python contract
  tests, frontend build/typecheck/DOM and dependency high-risk audit
- Full directoryassets/ldapconnection race and vet passed
- Real synthetic certificate-verified StartTLS/LDAPS transport and fixture tests
  passed: exact eleven attributes,30 rows/two pages, raw/absent/empty members,
  foreign/nested raw values, ranged and failed-page atomic rejection
- Integration-tag compilation passed for new storage/auth/runtime tests
- Formatting check passed
- Independent preliminary API review found shallow-checkout provenance and stored
  body/hash linkage gaps. Both fixed with six focused dictionary4 fixture/evidence
  contracts passing, including wrong-head/body/digest and null/empty rejection

Final frozen-tree checks and exact-head remote CI are separate gates. Local
`python3 scripts/test_auth_e2e.py --suite directory-v4-backend` was attempted and
failed because Docker is absent. Actual PostgreSQL migration/publication/auth
cases and registered Worker→TLS LDAP→PostgreSQL→API chain have not run locally;
compilation and synthetic transport proof do not count as those stages passing.
The mandatory new CI suite and validated artifact are required before merge.

No deployment, live AD, credentials, enrollment or production permissions were
changed. No UI or old memberCount semantics delivered; full AD-F-043 and the
Windows/capacity/production acceptance gates remain open, product acceptance0/209.
