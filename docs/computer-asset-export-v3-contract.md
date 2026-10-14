# Computer asset XLSX export v3: bounded EXP-007 slice

Contract freeze 2026-10-14; PR162 merged, exact-revision evidence recorded below.
Original implementation base main `7ed455aecee1352f0261e7064253891af1c31c79`, tree
`bdfc54e0982cb07a4679c5fa022f9324da31832a`. Refs
[F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-053 / EXP-007,
[computer list](computer-assets-v3-contract.md) and
[group export lifecycle](group-asset-export-v3-contract.md).
AD-F-053 remains `in_progress`; Issues #30/#151 remain open; acceptance is **0/209**.

## Frozen source and shared query

One explicitly authorized domain and one immutable published dictionary3 observation.
Required domainId, expectedRevision, expectedCredentialRevision and observationId
pin the displayed source. Missing or stale pins never silently select latest.
Validate the complete observation, including excluded users/groups, before filtering.
This exporter performs no LDAP operation or credential use and creates no observation.

The computer list and exporter share their predicate and order helper. Optional
search is literal Go simple-lowercase contains on SAM/SID/DN, at most 50 UTF-16
units and 200 UTF-8 bytes; no trim, normalization, regex or DNS/IP/SPN inference.
Empty search matches all computers. Optional startTm/endTm must appear together
as RFC3339 instants with start before end: whenCreated is in [start,end), and
missing creation time is excluded whenever a time range is applied. This is an
instant range, not an implicit whole-day extension. Optional creatTm=asc|desc
sorts creation time with nulls last in both directions and GUID-ascending ties;
omission sorts by GUID ascending. Export captures ALL matching rows, not the page.
Unsupported pagination, status, overviewType, updateTm, group filters and invented
business fields are rejected rather than silently ignored.

Required screenColumns is an array. Empty array resolves to exactly
`guid,sAMAccountName,sid,dn,whenCreated` in that order. A nonempty array preserves
caller order; absent/null, duplicate or unknown columns fail validation. Headings
are `GUID,SAM,SID,DN,whenCreated`; absent scalar facts become empty cells.
No inferred device name, DNS/IP association, OS, business status or SPN-derived
values are exported. Zero computers or zero matches yields a real header-only
XLSX; unavailable, stale or corrupt source fails instead of becoming empty success.

## Independent authority and lifecycle

Schema23 installs the independent `computer_asset_exports` readable/writeable
capability and epoch invalidation, task kind `computer_assets.v3.export`, and three
separate snapshot/chunk/manifest tables. Existing custom roles gain no grants;
exact permission catalogs include explicit false/false marks. Built-in admin
capability conventions still require explicit domain resource scope. The new
permission is necessary to authorize this producer separately; omitting it fails
closed. Installing code does not enable collection or create credential grants.

Reads require domains.readable, directory_assets.readable,
computer_asset_exports.readable and tasks.readable. Submit/cancel also require
computer_asset_exports.writeable and tasks.writeable, same-origin JSON and fresh
password/TOTP proof. Every API/Worker transaction checks actor, tenant entitlement,
domain/resource scope, exact schema and authorization epoch. Only the creator
can read the receipt, task and artifact, including generic task APIs. An epoch
change permanently invalidates old artifacts. Other export permissions do not
authorize this producer. Generic submit/recover/cancel cannot bypass its checks.

Prefix `/api/computer-assets/v3/exports` follows the group-export HTTP contract:
POST submit, GET `/receipt`, GET `/detail`, POST `/cancel`, GET `/download`.
Canonical request contains domainId, expectedRevision, expectedCredentialRevision,
observationId, search, startTm, endTm, creatTm and screenColumns. Optional strings
canonicalize empty. Receipt/replay preserve the original operation after current
owner/epoch/authority checks; receipt404 never proves a lost submit did not commit.
Cancellation can proceed after source rotation and must not require obsolete pins.
Proof is absent from payloads, fingerprints, logs, browser storage and evidence.

Fixed worksheet `ComputerAssets` and filename `computer-assets-v3-<taskID>.xlsx`
bind the output to this producer. Download verifies the whole artifact and digest
before any bytes, with exact Content-Length, X-ADTR-Artifact-SHA256 and
X-ADTR-Row-Count. Successful replies include no-store/nosniff and X-ADTR-User-ID.
Pending means queued/running/retry_wait/cancel_requested; only succeeded with a
valid current manifest is ready. Failed/dead_letter/partial_failed are failures;
cancelled requires persisted cancellation. source_changed/source_unavailable/
artifact_invalid disable download; database/schema failures remain503. Resolve
uncertain cancellation through detail, not by inventing a successful terminal state.

UI binds responses to actor/domain/revisions/observation/query generation, states
all-matching scope and column order, and invalidates late results. Unknown submit
retains the original idempotency key; receipt lookup never automatically resubmits.
Session recovery stores only actorID/domainId/key/fixed version-kind with strict
type/size/actor checks. Download revalidates session, actor/kind/task metadata,
bounded bytes and SHA256 before creating a Blob URL; authority loss discards
protected results and URLs are revoked. JavaScript zeroization is not claimed.

## Execution and protective limits

Replay-safe execution follows the group lifecycle: maxAttempts3, lease30seconds,
heartbeat5seconds, timeout5minutes, database_unavailable retry after5/10seconds
(cap60seconds), and engine recovery of interrupted/expired-lease attempts under
the same budget. Authorization, source, corruption and deterministic limit errors
are not retried. These bounds prevent unlimited work; removing them is outside scope.

Capture matching rows and whole-observation SHA256 under fencing. Every persisted
transaction and final success gate rechecks source/digest/authority/time eligibility.
Staged output is not downloadable; only fenced succeeded publication is eligible.
Cancel/revocation wins over success. Chunks are at most1MiB with ordinal/digest;
manifest follows ZIP close. Download verifies contiguous chunks, individual/whole
digests, length and exact task result agreement. Failed/cancelled/dead-letter
cleanup removes generated snapshot/chunks/manifest without requiring a live
executor; original receipts/events remain. Successful artifacts have no invented
retention period. Old attempts are removed under fencing before retry.

Cells are inline text, preserving formula-like prefixes, XML characters, CR/tab/LF,
Unicode and literal OOXML escapes; no formulas, links, macros or implicit numeric
or time conversion. Invalid selected XML text fails without truncation or echoing.
Protective limits: source10000 objects/16MiB encoded; export10000 rows/five columns;
cell32767 UTF-16 units; XLSX32MiB. Overflow fails instead of silently truncating.
Selection and validated download use bounded memory; these are not demonstrated
legacy capacity, peak-memory or end-to-end streaming guarantees.

## Verification and acceptance boundary

Required tests cover list/export query equivalence, time boundaries/offsets/missing
time, null-last ties, all pages, reordered/default/invalid columns, header-only,
whole-source corruption, stale pins, actor/tenant/kind/epoch isolation, receipt
ambiguity, retries/fencing/final source rotation, cleanup and actual ZIP integrity.
Three dedicated browser jobs cover normal/all-matching and empty output, invalid
selected text followed by safe-column recovery, and queued cancellation with
owned-Worker quiescence, resumed activity and durable zero-artifact evidence.
Actual XLSX/PNG evidence must bind database/source/checkout/run provenance.

The original candidate preserved all65 baseline checks and added3 for68; no old failure or requirement is
removed. Local validation is static/gofmt and explicitly guarded pure Python;
local Go/Node/PostgreSQL/browser/build execution is not claimed. Each later candidate requires its own CI, independent actual-diff/artifact review,
current-main compatibility and separate post-merge main-push checks. The recorded
PR162 success below is baseline evidence only for later revisions.
Running/cancel_requested browser acceptance, full legacy filters/business columns,
DNS/IP exports, real AD/eight Windows versions, target capacity and production
acceptance remain open. No deployment or Issue closure is part of this slice.

## Recorded PR162 evidence (2026-10-14)

[PR162](https://github.com/yunpiao/adtr/pull/162) is merged. Final head
`09e1c52f8ceac2a2d85cdc8b4c71fbf515fa078a` and actual main merge
`94b098d6acf5187e1ecf9aa659f5abed00122ce7` have tree
`0c669bd750c2ec137311aad08b6a05744ddff907`.
[PR CI38550970449](https://github.com/yunpiao/adtr/actions/runs/38550970449)
finished68/68 on attempt3; independent
[main-push CI38561279766](https://github.com/yunpiao/adtr/actions/runs/38561279766)
finished68/68 on attempt1. These results bind those exact revisions, not the new
user running-cancel candidate or future documentation/code changes.

Fresh main artifacts11893251763,11893351368 and11892781839 independently passed
source/provenance and actual-file inspection:940 source objects,216 XLSX cells and
six PNGs across normal/empty, failure/recovery and queued-cancel scenarios. This
establishes the bounded synthetic browser/Worker/PostgreSQL/API slice, with queued
cancellation only. It does not establish running cancellation, real AD or product
capacity/compatibility acceptance.

Historical failures are retained:
- Initial head74af592/tree791bfd, run38545054964 attempt1:54 successful jobs and14
  failed (eight sync.WaitComputer compile failures, four stale membership-permission
  catalog expectations, one sorted-access expectation and aggregate verify)
- Corrected head09e1, run38550970449 attempt1:66 successful and two failed
  (reader socket-hangup and verify). The precise socket failure cause is unknown
- Attempt2's actual reader and verify jobs passed, but the latest rollup still had
  queued duplicate metadata. A rerun of current eligible attempt jobs and metadata
  convergence produced final attempt3's68/68. A rejected old-attempt job rerun403
  remains a failed operation; it is not a test pass or evidence that the old failure
  never occurred

AD-F-053 stays in_progress, product_accepted=false; Issues30/151 remain open and
product acceptance remains0/209. No production deployment is implied.
