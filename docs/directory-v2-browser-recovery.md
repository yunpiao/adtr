# Dictionary 2 browser response evidence recovery

2026-10-09. Refs #25 / AD-F-034 and PR #81. This is a test-harness repair,
not additional AD/product acceptance or a production deployment.

## Observed baseline and diagnosis

At published head `5c0c82a91470a7d333b3b5569c0d223f9a7f222f`,
[CI 37929468021](https://github.com/yunpiao/adtr/actions/runs/37929468021)
reported these actual browser failures:

- `directory-v2`: host `Response.json()` failed at the original committed
  sync receipt with Chromium `Network.getResponseBody` / no stored body
- `directory-v2-controls`: the task-poll body collector failed and an
  `AggregateError` hid the original scenario assertion
- `directory-v2-readers`: `observationAction` failed reading the browser's
  response body in the delayed privileged-metadata/account-switch scenario

V1 uses native Fetch JSON consumption; V2 retains its required bounded stream
reader and no-store responses. The symptom matches the separately reproduced
[upstream Chromium issue](https://github.com/microsoft/playwright/issues/42742):
a multi-chunk no-store Fetch stream can reach application EOF while the native
inspector reports an aborted request and cannot return its body. That report
also reproduces without application aborts or React unmounts. It supports the
harness diagnosis; it is not a local execution result for our pinned browser.

## Repair and evidence boundaries

The three V2 browser suites observe only their same-origin directory/profile
response routes. A test-only wrapper copies bytes from the production reader's
actual reads, with the unchanged 8 MiB limit and strict UTF-8/JSON/EOF handling.
It returns the original Fetch/read promises, Response, reader and read results.
It never makes extra reads or requests, clones/tees responses, changes cache
headers, rewrites delivery, captures request proofs or changes production code.
Transport rejection, cancellation before EOF, malformed content and overflow
remain failures. CDP headers are still checked against the observed response.

Exact method/URL/ordinal and per-document identities keep repeated polls and
navigation separate. Same-document history navigation preserves the sequence;
reload changes identity. A stale unread Response cannot borrow new-document
bytes. Pre-aborted Fetch calls do not consume a network ordinal, including
inherited Request signals and explicit overrides. Host admission matches only
same-origin main-frame Fetch. A host-before-page count snapshot rejects any
unresolved invocation/request mismatch within the existing 15-second observer
bound, including an abort before dispatch after the Fetch call. Repeated
consumers of the same Response reuse its original captured
result, with no repeated network request. Body text is released from the page
observer when inspected. Controls report collector failures separately while
preserving the original scenario assertion and source location.

All existing original-intent, no-replay, proof-cleanup, actor/profile isolation,
worker completion, heartbeat/cancellation, immutable observation, authorization
and PostgreSQL executor-return/dependency assertions remain in place.

## Verification

- Added 18 Node regressions for native object/promise identity, exact consumed
  chunks, no extra reads/clone/tee, reverse completion order, strict EOF,
  rejection/cancellation, malformed UTF-8/JSON, exact limit/overflow, route and
  proof exclusion, host-side duplicate-read and navigation correlation,
  pre-aborted signal overrides, host-only event exclusion, and missing-event
  failures without borrowing earlier poll bytes
- Added three actual-browser HTTP collector cases to the existing V2 suite:
  split no-store success, valid JSON followed by premature transport EOF, and
  explicit abort. The success case also checks duplicate reads, pushState,
  reload, a retained old response, and a pre-aborted request followed by
  distinct uninspected/inspected bodies. These use a separate owned loopback
  synthetic server and do not substitute for the real API/worker/LDAP scenario
- Independent source review identified and verified fixes for same-document
  navigation correlation, stale-document borrowing, duplicate body reads,
  and pre-dispatch aborts shifting host/page ordinals
- Full local `make check`, targeted observer/V2 tests, TypeScript, formatting
  and Playwright discovery are recorded with the change's test evidence

Actual browser execution remains pending exact-head CI. Local Chromium launch
was retried but is blocked by the executor's Unix-socket restriction
(`socket() failed: Operation not permitted`); Docker is absent. Discovery and
Node/DOM tests do not count as browser execution. Existing required suites and
all time/size/security limits are unchanged. Real AD/Windows, capacity, source
parity and product acceptance remain separate open gates.
