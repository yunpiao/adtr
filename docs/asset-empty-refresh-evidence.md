# Same-source nonempty-to-empty observation refresh evidence

Engineering [#147](https://github.com/yunpiao/adtr/issues/147), parent
[F14 #30](https://github.com/yunpiao/adtr/issues/30). Baseline main
`e1a4e6bed1b20a158f624990064f361db4e83f92`, schema19. Test-only extension of the
existing group/computer observation-selection contracts. Product acceptance stays
0/209; no real AD deletion, source parity, Windows or capacity claim.

## Gap and bounded coverage

The earlier [empty observation evidence](empty-asset-observation-evidence.md)
starts without a successful observation and then collects its first empty one.
This slice preserves both existing nonempty observations in each primary
`group-assets-v3` and `computer-assets-v3` browser spec, then adds a third real
registered Worker collection. No observation is inserted or rewritten by tests.
Only an isolated synthetic TLS LDAP fixture changes its next snapshot to empty.

After the third task succeeds and its use quiesces, the browser still pages the
second observation using its old explicit pin and displays its exact old rows.
A post-transition keyword search keeps that same old pin, returns the exact
matching historical row, and distinguishes observed count23 from filtered total1.
Clearing the search retains the old pin and restores unfiltered first-page rows.
An unpinned API read and explicit UI refresh select the new available empty
observation; applying blank filters then retains the new explicit pin. Both old
nonempty observations remain readable through their original pins and their
stored evidence is unchanged. Domain/config/credential revision, actor and
account/grant/policy source pins remain fixed; collection timestamps and page
counts correctly differ between observations. A real empty observation has
available=true, count0, list[], total0 and totalPage0, not unavailable/null count.

## Test fixture controls

The optional Python `empty_transition` keyword and integration executable
`-empty-transition-dir` solve only the test fixture's snapshot transition. They
are required only for these two primary suites; defaults are off. Without them,
all original fixtures remain static and the new transition test fails for missing
fixture setup. They cannot change application source selection or authorization.

The mode requires directory-v3 plus exactly one group/computer dictionary, and
rejects static-empty, slow, v2/user and B2 hold-control modes before allocating
resources. A fresh private local directory is mounted read-only into the fixture.
The Node test writes exact `empty-v1\n` bytes to a fixed staging filename and uses
an atomic no-overwrite hard link to publish the one fixed `empty-v1` marker.
Repeated publication fails; no network control endpoint or arbitrary command is
added. The fixture uses an anchored directory and Linux no-follow/nonblocking
open, requires a regular readable exact-size file, and rejects malformed or
unreadable data. Non-Linux transition mode fails closed; static fixtures keep
building. The launcher cleans up only its owned temporary directories/container.

Selection is sampled once after successful TLS, bind, RootDSE and exact first
paged directory request validation. The session keeps that selection across all
continuations; rebind clears the session latch. Marker publication during an old
paged snapshot cannot truncate it or mix old and new data. Missing marker means
original rows; any malformed marker aborts the read instead of reporting a
successful empty result. No production producer, schema, gate, grant, account or
security configuration is changed.

## Retained evidence and checks

Each primary suite writes a separate
`{group|computer}-assets-v3-empty-refresh-evidence.json` and four PNGs:
old-pinned and refreshed-empty at 1440x900 and 390x844. The closed-schema report
binds all three task UUIDs, actual read-only stored bodies/digests/use states,
unchanged source pins, exact queries and responses, native body-consumption
witnesses, screenshot identities/hashes and checkout/tree/raw parents/run/source
hashes. The zero-object body must match the canonical dictionary-3 bytes with its
actual source metadata. Old canonical bodies are hashed and read through the
existing complete-body-validating API; exact public field assertions remain in
the real browser tests. Unit validator fixtures are synthetic inputs, not runtime
proof. Image hashes and geometry do not replace human visual review.

`scripts/test_auth_e2e.py` runs the bounded validator after the two primary specs
succeed and before declaring browser-pass. Both their normal and existing fixed
reader engine executions therefore validate the evidence. The reader-specific
specs gain no new task. Existing artifact prefix paths retain the five files;
workflow, 50-job matrix, eight-slot guard, timeouts, native readers and retry
settings are unchanged. Each primary run adds one real proof-bearing sync and
its real MFA interval; no clock or replay bypass is used.

Commands:

```sh
go test -race -tags=integration ./internal/testldapfixture
python3 -m unittest discover -s scripts -p 'test_asset_empty_refresh_contract.py'
python3 -m unittest discover -s scripts -p 'test_ldap_fixture_contract.py'
make check
python3 scripts/test_auth_e2e.py --suite group-assets-v3
python3 scripts/test_auth_e2e.py --suite computer-assets-v3
python3 scripts/validate_asset_empty_refresh.py /path/to/group-assets-v3-empty-refresh-evidence.json --kind group --source-root .
```

The unmodified baseline passed make check (2110 DOM,168 Python,Go race/vet/build,
TypeScript/build,audit0), plus focused LDAP fixture checks. This executor has no
Docker; the two real browser commands remain blocked locally. Exact candidate
checks, independent review and real exact-head CI evidence must be recorded
separately before merge; baseline success is not candidate/runtime success.
