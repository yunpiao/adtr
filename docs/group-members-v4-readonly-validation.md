# Dictionary 4 group membership: custom read-only actor

Refs F14 / AD-F-043 and Issue #30. This is an engineering evidence slice,
not complete product acceptance; all 209 requirements retain their existing
acceptance status. The synthetic domain and users exist only in the owned
`adtr-auth-e2e-*` disposable PostgreSQL fixture. No real AD, account, deployment,
production permission, schema, fixture, workflow or timeout change is included.

The existing producer still completes real Worker → TLS LDAP → PostgreSQL → API
collection and revokes its dictionary-4 use grant. Its original six desktop/mobile
screenshots and `platform_admin_after_v4_use_revoke` evidence remain separate.

The bootstrap actor additionally creates one custom role with exactly
`domains.readable` and `directory_assets.readable`, assigns the existing synthetic
domain resource group, and creates an independent user. Every writeable permission
is false. Bootstrap uses the existing ten-action sensitive-operation budget;
resource reassignment is followed by real password/MFA login. No rate reset or
session injection is used.

After producer evidence, the same browser signs out, signs in as the reader,
changes its initial password and enrolls real MFA. `/api/auth/me`, the menu,
route checks, and a read-only PostgreSQL snapshot independently establish actor,
custom role, exact permissions, domain scope and zero credential-use grant rows.
The reader selects the real source, reads the same twelve groups and saved
observation, rereads that pinned observation, and opens/closes null, empty and raw
member details at 1440×900 and 390×844. The six separate PNG filenames are
`group-members-v4-reader-{width}-{12|13|14}.png`.

`readerUi.writes` observes only browser UI business requests. Direct API probes
are a distinct `reader.denied` list: sync, cancel, grant and revoke must return
403/forbidden with real session, CSRF, password and a current unconsumed reader
TOTP. These production routes reject role authority before proof consumption.
A read-only PostgreSQL snapshot after each denial must exactly equal the initial
snapshot, including the MFA counter, before another probe can reuse a still
unconsumed code. This does not exercise MFA rejection or successful mutation.

Before/after snapshots bind the actor and custom role, allowed domain, readable
and writeable marks, zero grant rows, task/use/observation counts, and SHA-256
computed from the actual stored observation bytes. Session/auth audit activity is
legitimate and is not asserted absent. `reader.provenance` must exactly equal the
producer checkout/tree/source digests and run identity. The validator requires all
twelve actual PNGs and fails closed for mismatched identity, source, role, scope,
permissions, counts, hash, direct probes or UI business writes.

## Verification boundary

Local type checking, formatting, Playwright test discovery and evidence contract
checks validate implementation consistency only. Browser/worker/LDAP/PostgreSQL
behavior, the unchanged 630-second test / 660-second runner budgets, and actual
PNG contents require the real exact-head CI run. An estimate or contract fixture
is not a successful real-browser run. Existing local PostgreSQL/browser socket
restrictions are respected and not worked around. No acceptance item is marked
done by this document.
