# Bind domain-list pages and support existing bounded-all responses

Offline engineering candidate #314 within F01 #16 / AD-F-001. Baseline remote
main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`. Acceptance remains **0/209**.

## Existing contract and reproduced behavior

`domain-contract.md` list semantics and FLD-0008/0009 specify the actual requested
page and validated page size. `internal/domains/validation.go` defaults to page
1 / size20, requires canonical singleton integers, allows page1..1000000 and
size1..100. Size-1 is allowed only on page1. `query.go ListTx` keeps -1 in the
response, reads at most1000, and rejects larger totals with422 result_too_large.
An empty bounded-all result has zero pages; a nonempty one has one page.

Actual `domainAPI.list` accepted a self-consistent page1 response to a page2
request. In actual `DomainsWorkspace`, Next sent page2 but still displayed page1
rows; another Next sent no third request because internal page state was
already2 while the response-driven paginator still requested2. Separately, a
valid page1/size-1 response was rejected by the client's size1..100 check.
These were reproduced with synthetic fetch, not evidence of a live server
producing wrong pages or successful real database/all-results execution.

## Narrow implementation

Only `domain-api.ts` production code changes. After the existing DTO checks,
`URLSearchParams.getAll` binds pageIdx/pageSize to the successful response.
Omitted parameters use existing1/20 defaults. Duplicate, empty, noncanonical or
mismatched page scalars cannot match an otherwise valid successful page. Percent
encoding is decoded normally. Original query bytes, repeated domain/status
filters and keyword text are sent unchanged; this is response validation, not a
new frontend query parser or replacement for server validation.

The existing -1 sentinel is accepted only for page1 with total<=1000, complete
row count, expected totalPage and exhausted=true. Normal paged responses retain
their original DTO policy apart from request-page binding. This slice does not
attempt a complete new normal-page row-count/order/total consistency validator.
No new UI all option or greater result capacity is introduced.

Both page bindings are required to prevent a response for another page or page
size from becoming visible. Removing them restores the demonstrated wrong-page
state. Default values are necessary only when those query parameters are absent.
The -1 branch is needed only for the already-supported API bounded-all request;
removing it rejects valid existing responses. Its1000 limit reflects the server
protection, not a capacity claim. No configuration parameter is added.

Side effects: mismatched responses now become the existing invalid_response,
which clears rows and offers refresh through the unchanged read hook. Its normal
transient-error retry behavior is unchanged. Successful all responses previously
rejected now return to API callers; the current UI still offers10/20/30/40/50.
No transport/error classification, actor validation, other domain methods,
credential-source APIs, authentication, permissions or write behavior changes.

## Tests and discovered fixture correction

Original Domains baseline:71 passing. New tests use real API/components with
synthetic responses, covering defaults, encoded/repeated scalars, canonical
bounds, arbitrary legal1..100 sizes, empty/out-of-range pages, unchanged filters,
invalid DTOs/errors, bounded-all0/1/1000 and invalid all results. UI tests cover
wrong-page removal, explicit refresh, previous-page navigation, size mismatch,
retained UI options and late response suppression after a new size query.

The first focused run exposed an old fixture contradiction in
`Domains.test.tsx`'s “keeps applied filters, unsubmitted edits and row focus after
details”: it requested size10 but always returned size20. The production check
correctly rejected it. After confirming the file was not owned by another task,
only this test's response fixture was made to echo the requested pageSize. All
original assertions remain unchanged. No global fixture or production rule was
relaxed. The same first run found two new exact-text selectors that omitted the
paginator's surrounding count; selectors now check the unchanged page text
inside its actual text node. Original failures are retained in evidence.

Final code, focused/full tests, old-main control, independent review, file hashes
and backup are recorded in #314. Cached Node24.19.0 and dependencies only:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/DomainPaginationBinding.test.tsx src/Domains.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
node node_modules/prettier/bin/prettier.cjs --check src/domain-api.ts src/DomainPaginationBinding.test.tsx src/Domains.test.tsx ../docs/domain-pagination-binding.md
cd ..
python3 scripts/verify_requirements.py
```

The workspace component is untouched, so the independent table-sequence
candidate retains its file boundary. This candidate is independent of #312's
credential-source read status and does not fix malformed domain-read401 handling.
No audit, install, make check, PR, CI, Go/database/browser E2E, real AD/Windows,
deployment or merge is run. Source backup is not CI or product acceptance; the
Issue remains open for later authorized gates.
