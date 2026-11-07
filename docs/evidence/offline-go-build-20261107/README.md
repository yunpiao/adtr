# Offline Go build evidence, 2026-11-07

Compilation preparation passed for source `503022338044ddfaffb26836449da58db2a798ea`, tree `8fa959a7f56eea7493f745141893ea2fd9fec0a9`. This evidence-only addition extends the existing backup branch, with parent `ef482b08396230ffa4dedce1f7f2c3e013e0a047`. It does not change source or the earlier 32 evidence files.

`go mod verify`, `make build` and the integration-tagged synthetic LDAP fixture build each recorded exit 0. Application help recorded exit 0. Fixture help recorded exit 1: its existing flag parser maps `flag.ErrHelp` to the generic configuration error before credentials or listeners; it is not counted as passing.

The retained Go 1.27.1 archive SHA256 matches the historical official URL/digest record in `docs/validation-g03.md`; all 15,639 installed regular files match. Official metadata was not fetched again. Logs are ordinary receipts, not signed, hermetic or reproducible-build proof. Offline environment controls are producer-reported in the original summary, not captured in build.log.

## Evidence and interpretation

- `result.json` gives source and artifact identity, results and limits. Binaries are not included.
- `*.evidence.json` preserve the three retained source records losslessly as UTF-8 lines with line endings. Join `lines` without a separator and verify the recorded original SHA256.
- `command-exits.json` extracts explicit exit markers. A null exit code means none was retained; it is not inferred as success.
- `source-transforms.json` binds original hashes and documents the rewritten summary. No private review report is included.
- `evidence-manifest.json` hashes every other publication file. The manifest excludes itself to avoid recursive hashing; the separate publication plan binds all files.
- `workflow-trigger-check.json` records exact-ref read-only preflight, not a lasting guarantee. Recheck before writing remotely.

The application uses the existing `-buildvcs=false -trimpath` recipe. Disabling VCS stamping avoids VCS lookup but removes an embedded source identifier; deleting that option permits stamping and changes provenance/output. Trimpath removes build path details; removing it may expose local paths and change bytes. Neither is an application runtime requirement. Application source binding is therefore the clean source snapshot plus receipt, not binary-internal proof. The fixture embeds the exact source revision and `vcs.modified=false`.

The fixture uses `-tags=integration` to include its integration-only package; omitting the tag prevents this fixture build. `CGO_ENABLED=0` matches its original runner and avoids C toolchain dependencies; removing it may enable cgo. Explicit GOOS=linux/GOARCH=amd64 select the intended target and are necessary when the host differs; removing them uses host defaults. The runner still builds its own temporary fixture and has not been changed to use this prepared binary.

For this no-download preparation, `GOTOOLCHAIN=local` prevents automatic toolchain acquisition and `GOPROXY=off` prevents module downloads, so missing/incompatible cached inputs fail. Removing them can permit downloads. `GOSUMDB=off` prevents new checksum-database requests, with the tradeoff of no fresh upstream authentication; local module verification is not upstream provenance. `GOTELEMETRY=off` suppresses telemetry for this run; removing it restores configured behavior. These are build controls, not product functionality requirements, and do not prove that every network syscall was absent.

Real PostgreSQL/API/Worker/TLS LDAP/browser and real AD acceptance remain unexecuted. Docker and the required PG image, Node 24.19.0 and Chromium 141.0.7390.37 remain unavailable. No npm/audit/CI/PR/dispatch/merge-group operation was used for this package. No deployment or product acceptance is claimed; product acceptance remains 0/209.

## External frontend binding

The UI is external `web/dist`, not embedded in Go. `static-assets.json` records the current three-file dist manifest, complete JS/CSS entry references, 21 candidate file checks and five existing frontend receipt checks. Asset names match the Vite receipt. That older receipt has no full dist-byte hashes, so this current manifest cannot independently prove unchanged bytes since the build. Prior frontend build Node was 24.22.1, not required 24.19.0. No HTTP service or browser load occurred.

`ADTR_WEB_DIR` selects the static UI directory and is required to serve these files through the application route. Removing it leaves the existing handler instead of registering the static file server; an incorrect or stale directory can cause missing or stale UI. Relative values depend on the process working directory. The native runner resolves repository web/dist absolutely; the container uses /web. Go compilation alone does not update these assets.
