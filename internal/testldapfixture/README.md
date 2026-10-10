# Isolated synthetic LDAP fixture

This executable and its tests require the `integration` build tag. It serves
only StartTLS on port 389 and LDAPS on port 636, with a short-lived test
certificate and exact synthetic credentials. The default remains bind and
RootDSE only, including the existing optional B2 control barriers.

`-directory-mode` additionally enables a single fixed dictionary search after
verified TLS, successful bind and RootDSE. `LDAPFixture(directory_enabled=True)`
passes this flag and sets `ADTR_E2E_LDAP_DIRECTORY_MODE=true` for browser tests.
The runner independently enables the application's directory-read deployment
flag. Neither switch grants a role or credential-use purpose.

The exact request matches `ldapconnection.ReadDirectory`: base
`dc=synthetic,dc=invalid`, subtree, no alias dereferencing, size limit 0,
time limit 3, values included, `(|(objectClass=user)(objectClass=group))`, and
exactly `objectGUID`, `objectClass`, `sAMAccountName`, `userAccountControl`.
One critical RFC2696 control must request size 1000 and the current opaque
cookie. There are two pages of 15 entries and a terminal empty cookie. The
continuation is connection/bind-specific; missing, altered, replayed or foreign
cookies and all changed requests are rejected. No writes, referrals, generic
filter evaluation or arbitrary directory searches are implemented.

The 30-row dictionary is stable in GUID order:

- Rows 1–12: `user-01` through `user-12`, `OU=Users`; UAC 512 except row 1,
  whose explicitly present UAC is 0
- Rows 13–24: `group-01` through `group-12`, `OU=Groups`; every UAC is absent
  and row 24 also has absent SAM
- Rows 25–30: `computer-01` through `computer-06`, `OU=Computers`; SAM has a
  trailing `$`, UAC 4096, and structural classes include both user and computer

DNs end with `DC=synthetic,DC=invalid`. Each GUID is its one-based row byte
repeated 16 times. Directory mode returns RootDSE hostname
`DC.Synthetic.Invalid.` to exercise downstream canonicalization; default mode
retains `dc.synthetic.invalid`.

The original bounds remain: 64 live connections, 8 requests per connection,
64 KiB request bodies and a 15-second connection lifetime. Shutdown closes and
joins active transports. Tests use actual verified TLS and BER, including the
production directory reader with only its dial mapped to a `net.Pipe`.
This is synthetic protocol evidence; it is not a real AD compatibility test,
database publication test, or browser acceptance result.

Run `go test -race -count=1 -tags=integration ./internal/testldapfixture`.

Dictionary 2 is separately selected with `-directory-mode -directory-v2`, or
`LDAPFixture(directory_enabled=True, directory_v2=True)`. It accepts only the
fixed eight-attribute request and preserves the original 30 base rows. Empty
and slow modes remain available for either dictionary. The frozen supplemental
data and full test boundary are documented in
[`docs/directory-v2-test-fixture.md`](../../docs/directory-v2-test-fixture.md).

User-asset browser evidence selects the independent opt-in combination
`-directory-mode -directory-v2 -user-assets-v2`, or
`LDAPFixture(directory_enabled=True, directory_v2=True, user_assets_v2=True)`.
It rejects empty/slow mode and preserves the existing v1/v2 dictionary bytes.
The same two LDAP pages contain 12 users, 12 groups and 6 user-inheriting computer
decoys. User row 12 omits SAM, UAC and all four supplemental attributes; row 1
contains present UAC zero, year 0001, and literal hostile HTML-looking, control,
format, whitespace and supplementary-plane text. Row 11 supplies distinct SAM,
SID, mail and DN search witnesses after the first ten users. Exact bytes are in
`directory_v2.go:userAssetsV2Entry` and `user_assets_v2_test.go`.

`scripts/test_auth_e2e.py --suite user-assets-v2` runs real producer/search/detail
browser evidence; `--suite user-assets-v2-readers` adds genuine headed native-tab
and current-reader revocation evidence. Both require a fresh owned Docker
PostgreSQL, real API/worker and this TLS LDAP fixture. The fixture marker
`ADTR_E2E_LDAP_DIRECTORY_USER_ASSETS_V2=true` only identifies data; it grants no
application capability. Browser publication/execution is a separate check from
the local protocol tests above. Synthetic screenshots are captured only after
leaving credential-entry forms and retained by the pinned CI artifact action.
