# Credential cleanup catalogue page binding

Issue #311, under #17 and the existing credential-use governance contract, with
the #25 directory-purpose extensions. Base main:
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`.

## Confirmed defect

An independent actual API/Workspace reproduction requested page 2 with size 20
but supplied a self-consistent page-1 response containing 20 rows of 21 total.
The original API accepted it. The UI then showed PageOne and page 1 / 2 even
though its query state was page 2. Clicking Next again did not issue a third
request: Pagination derived page 2 from the returned page 1, equal to the
already-applied query state. This is wrong-page presentation and stuck
pagination **when a response is mismatched**, not proof that the normal backend
generates the mismatch or that a user crossed an authorization boundary.

All four existing account-cleanup readers have the same missing binding:
connection-test, directory v1, directory v2 and directory v3. Their backend
routes share `credentialuse.ParseFilter` and `AccountsForPurposeTx`;
`Page.Index` and `Page.Size` are explicitly populated from the request filter.
The directory profiles change the trusted purpose, not this pagination contract.
See [credential-use](credential-use-contract.md),
[directory components](directory-component-contract.md),
[directory v2](directory-v2-api-contract.md) and
[directory v3](directory-v3-contract.md).

## Minimal correction and parameter meanings

Each accounts-response validator now additionally requires its page index and
size to match the query it sent. Existing exact DTO, integer/range, total-page,
row-count, unique-ID, account-revision and exhausted checks remain in place.

| Query parameter | Existing requirement                                                       | Why binding is necessary                                                                                                   |
| --------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `pageIdx`       | Optional, defaults to 1; canonical decimal integer 1–1,000,000; singleton. | Identifies the requested slice. A different echoed index must not drive pagination or expose unrelated rows as that slice. |
| `pageSize`      | Optional, defaults to 20; exactly 10, 20, 30, 40 or 50; singleton.         | Binds the size selector and slice boundaries. An old-size response must not be shown after changing the selector.          |

Decoded parameter spellings are compared with the response number's canonical
string. Percent-encoded canonical names/values therefore work, but duplicate
pagination keys, empty values, signs, padding, fractional/exponent notation and
unsupported sizes cannot match an otherwise valid successful page. Omitting a parameter is
valid and uses the existing server default; supplying it is necessary only to
choose a nondefault index/size. Removing the binding restores the defect.

This is response validation **after** the existing transport, not a new client
query parser or preflight rejection. The exact request string is unchanged;
backend query length, vocabulary, keyword and authorization enforcement remain
authoritative. The client does not reapply search, recount server data, truncate
rows, normalize malformed inputs, or claim that the metadata proves database
provenance. A mismatch produces the existing `invalid_response`; the unchanged
read hook clears data and retains its ordinary read-only retry/refresh behavior.
Explicit refresh can obtain the correctly bound page without a stuck Next loop.

## Scope and regression evidence

Only the four `*credential-use-api.ts` accounts-response blocks change, each by
the same 12-line binding. There is no shared-helper dependency on unmerged #299
or another candidate. The new `CredentialCataloguePagination.test.tsx` exercises
all four actual APIs and workspaces; existing tests are unchanged.

The 228 new cases cover implicit and explicit defaults; all sizes; full, partial,
empty and maximum-index pages; encoded canonical query keys; keyword text that
resembles pagination; self-consistent wrong-page/size responses; ambiguous and
noncanonical page inputs; existing malformed DTO controls, actor and HTTP errors.
Each real Workspace rejects wrong-page/old-size rows, explicitly refreshes the
same request, resumes Back/Next behavior and ignores a late prior-page response
after a newer size reset. These flows make no business POST or permission change.

Validation uses cached Node 24.19.0/dependencies, with no install or audit:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/CredentialCataloguePagination.test.tsx src/CredentialUse.test.tsx src/DirectoryCredentialUse.test.tsx src/DirectoryV2CredentialUse.test.tsx src/DirectoryV3CredentialUse.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
```

The original four-suite baseline has 134 tests; focused candidate total is 362.
An identical-test unchanged-main control, full-suite, formatting, requirements
and independent review results are recorded separately in the Issue, with exact
hashes and the non-triggering backup commit.

## Unchanged authority and remaining gates

Grants, roles, effective authorization, receipts, POST/proof, persisted recovery
keys, backend scope/permission checks and the UI implementation are unchanged.
The separately discovered non-JSON 401 retry issue is **not fixed by this slice**;
its evidence is retained rather than mixed into these four files' pagination
change. System API and all frozen work remain untouched.

Fixtures are synthetic fetch responses with actual React/API code, not a live
PostgreSQL/API/worker/browser/AD/Windows acceptance run. No credential was read,
transmitted or changed, and no live grant/revoke was performed. No PR, CI, audit,
deployment or merge is part of this backup. #311 and parent product Issues remain
open, product acceptance stays 0/209, and exact future PR/main gates remain pending.
