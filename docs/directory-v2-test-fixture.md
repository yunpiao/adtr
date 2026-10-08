# Dictionary 2 synthetic LDAP fixture

This integration-only fixture supplies the fixed dictionary-2 data in
[directory-v2-api-contract.md](directory-v2-api-contract.md). It contains
synthetic factual data, not a real AD client or evidence of real AD, database,
browser, Windows-version, capacity or product acceptance. Product acceptance
remains 0/209.

## Explicit selection and unchanged defaults

The Go executable accepts the boolean `--directory-v2` only with
`--directory-mode`. The existing single-dash spelling is also supported by
Go's flag parser. `--directory-v2=false` retains dictionary 1. There is no
arbitrary dictionary, attribute, filter, delay, page-count or destination
selector.

The Python launcher selects it with:

```python
LDAPFixture(directory_enabled=True, directory_v2=True)
```

`directory_v2` is a keyword-only boolean, defaulting to `False`. The existing
four positional arguments retain their meanings. All directory flags reject
non-booleans before allocating resources. The launcher passes
`-directory-mode -directory-v2` and exports
`ADTR_E2E_LDAP_DIRECTORY_MODE=true` and `ADTR_E2E_LDAP_DIRECTORY_V2=true` only
for the selected mode. Disabled markers are removed even if inherited. The
existing `EMPTY`, `SLOW` and control-directory markers retain their meanings.
These are test fixture metadata, not application deployment flags or grants;
the caller independently configures both collection deployment flags and exact
v2 credential-use authority.

No flags still means bind and RootDSE only. `--directory-mode` alone still
accepts only the original four-attribute request and emits the original
responses. Readiness output, credential handling, RootDSE, optional B2
controls, certificate verification, StartTLS port 389, LDAPS port 636,
connection shutdown and Python resource cleanup are unchanged. No credential
values or new sensitive data are written to readiness output or command-line
arguments.

## Exact request and transport

Dictionary 2 accepts only this fixed request after verified TLS, successful
bind and RootDSE:

- Base: `dc=synthetic,dc=invalid`; subtree scope; no alias dereferencing
- Size limit 0; time limit 3; values included
- Filter: `(|(objectClass=user)(objectClass=group))`
- Exact attribute order: `objectGUID`, `objectClass`, `sAMAccountName`,
  `userAccountControl`, `objectSid`, `mail`, `description`, `whenCreated`
- One critical RFC2696 control, page size 1000 and the currently issued
  opaque cookie

Requests for dictionary 1 are rejected in dictionary-2 mode and conversely.
Changed order, spelling, attributes, filters, base, bounds or paging controls
are rejected without advancing the session. There is no general filter
parser, referral, write operation or real network target.

Default data takes two pages of 15 rows. Cookies retain the 20-byte opaque
format and binary prefix, are fresh per connection/bind/page, and cannot be
replayed, transferred or rewound. Successful rebind clears paging state.
Directory-mode RootDSE still returns `DC.Synthetic.Invalid.` to exercise
canonicalization; default RootDSE retains `dc.synthetic.invalid`.

The unchanged budgets are 64 live connections, eight requests per connection,
64 KiB request bodies and a 15-second connection lifetime. V2 does not enlarge
production reader limits or retry behavior. Cancellation closes and joins the
owned transport and the production reader returns no partial observation.

## Frozen rows

All 30 GUIDs, DNs, base attributes and object kinds are the original dictionary:
12 users, 12 groups and six computers, in that order. A GUID is the one-based
row byte repeated 16 times. Row 1 retains explicitly present UAC 0; all groups
omit UAC; row 24 (`group-12`) also omits SAM. Computer SAM values retain `$`.

Row 24 omits all four supplemental attributes, which project as JSON null.
Every other row has exactly one value per supplemental attribute:

| Attribute | Row 1 | Other rows except 24 |
| --- | --- | --- |
| `objectSid` | Binary `S-1-5-21-1-2-3-1001` | Binary `S-1-5-21-1-2-3-(1000+row)` |
| `mail` | ` Mixed@example.test `, including both spaces | `row-%02d@example.test` |
| `description` | `Line 1\u0000\n\t😀\u202e` | `Synthetic row %02d` |
| `whenCreated` | Raw `00010101000000.0Z` | Raw `20261001020304.0Z` |

The description notation above names actual code points in one LDAP value:
NUL, newline, tab, U+1F600 and U+202E. They are actual UTF-8 bytes on the wire,
not literal backslash escapes. `description` projects as a one-element array.
The public creation times are `0001-01-01T00:00:00Z` and
`2026-10-01T02:03:04Z` respectively. The fixture does not infer absence reasons,
mail semantics, relationships or business status.

`--directory-empty` and `--directory-slow` work with either dictionary. Empty
returns zero rows. Slow returns five pages, each delayed exactly two seconds;
nonempty slow pages contain six rows each. Empty plus slow produces five
empty delayed pages. No user-supplied timing or page-count input exists.

## Verification and limits of evidence

Run with the repository's Go toolchain:

```sh
go test -race -count=1 -tags=integration ./internal/testldapfixture ./internal/ldapconnection
go vet -tags=integration ./internal/testldapfixture ./internal/ldapconnection
python3 -m unittest discover -s scripts -p 'test_ldap_fixture_contract.py'
```

The Go tests exchange actual certificate-verified StartTLS/LDAPS and BER over
isolated `net.Pipe` transports. The integration-only
`ReadDirectoryV2WithDialForIntegrationTest` entry point replaces only the dial;
the actual fixed production reader performs TLS, bind, RootDSE, paging,
authorization, decoding and cleanup. Tests verify every row through raw-byte,
canonical storage and public JSON projections, including omitted values, NUL,
non-BMP/bidi text and year 0001; all mode shapes and delays; wrong-profile and
changed-request rejection; auth prerequisites; opaque-cookie isolation; request
and envelope budgets; cancellation; and the existing default/v1 regressions.

A legacy golden locks the exact dictionary-1 search body, initial paging
control and all 30 entry packets (message ID 4) to the bytes generated from
the pre-change fixture. Existing v1 production-reader and transport tests
remain intact.

Python contracts mock Docker, OpenSSL and Go execution. They actually exercise
constructor validation, flag selection, marker clearing, environment/port
contract, control mount and resource cleanup, but are not a container-run
result. Local fixture tests do not provision Docker, publish to PostgreSQL,
run a browser, connect to real AD or provide product acceptance.
