# F03 operation-account pagination response binding

Engineering issue: [#299](https://github.com/yunpiao/adtr/issues/299).
Parent scope: [#17](https://github.com/yunpiao/adtr/issues/17), AD-F-008,
FLD-0145/0146/0152/0153. This implements the existing local pagination decisions
in [the operation-account contract](operation-accounts-contract.md), not a new
source-system default or a product acceptance claim.

## Defect and correction

The list and authorized-domain selector previously checked a response's page
metadata only against its own rows, totals and exhaustion flag. A request for
page 2 could accept a self-consistent page 1 response. The UI would display page
1 while its request state remained page 2, so pressing Next again could leave
the same page displayed without another request. A wrong page size, including
the bounded-all mode, could likewise be accepted.

Both readers now bind successful response `pageIdx` and `pageSize` to the query
that produced the response. The parameters solve different problems:

- `pageIdx` identifies the requested one-based page. It is optional only when
  page 1 is intended; omission uses the existing default 1. Explicit values must
  be canonical integers from 1 to 1,000,000.
- `pageSize` identifies the requested page capacity. It is optional only when
  the existing default 20 is intended; explicit values are 1–100, or -1 for
  bounded-all results at page 1 with at most 1,000 matching rows.

Each scalar may occur at most once. URL-decoded values must equal the canonical
string of the validated response integer. Thus an empty value, repeated key,
leading zero, numeric alias or mismatch cannot turn a successful response into
accepted page data. Percent-encoded canonical keys/values remain valid; other
filter parameters and the outgoing query are unchanged. This is response
validation, not a replacement for server-side query/authorization checks.

The existing exact object shape, integer/range, row-count, total-page,
exhaustion, bounded-all, unique-ID and safe-record checks remain mandatory.
Non-success server errors are processed before this check and retain their
original code/status. The change does not alter identity checks, permissions,
credential handling or mutation routes.

Removing the binding would restore acceptance of wrong-page responses. Its
intentional visible effect is an `invalid_response` error instead of displaying
a mismatched page. A nonconforming server or test fixture that silently clamps
pages or changes page size must be corrected; the client does not silently
normalize it. Existing refresh controls retry the original requested page.

## Offline regression coverage

`web/src/OperationAccountPagination.test.tsx` exercises both readers:

- matching defaults, explicit/encoded pages, maximum index/size, empty and
  out-of-range results, and bounded-all capacity;
- internally consistent wrong page/size and omitted-default mismatches;
- empty/noncanonical/out-of-range values and repeated decoded scalar keys;
- unchanged 400/401/403/422 errors and existing row/count/exhaustion bounds;
- actual React DOM Next → wrong-page rejection → refresh → correct page
  recovery, with the request sequence 1/2/2 and no operation-account mutation.

On unchanged main `21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, the existing
32 operation-account tests passed. The new suite produced 58 failures and
26 passes, including failures in both wrong-page DOM cases. With the fix,
the new 84 tests plus the original 32 pass. The original empty/out-of-range/all
positive test now sends its matching page query; no assertion was removed.

Use already materialized locked dependencies and Node 24.19.0:

```sh
cd web
node node_modules/vitest/vitest.mjs run src/OperationAccountPagination.test.tsx src/OperationAccounts.test.tsx --pool=forks --maxWorkers=2
node node_modules/vitest/vitest.mjs run --pool=forks --maxWorkers=2
node node_modules/typescript/bin/tsc --noEmit
node node_modules/vite/bin/vite.js build
```

Full offline results and independent review are recorded in #299. These
synthetic DOM/API mocks do not establish real browser/PostgreSQL/AD/Windows
acceptance. Dependency audit, aggregate `make check`, PR/head CI, merge and
deployment are not run under the current restriction. A non-triggering branch
is backup only; #17 and product acceptance remain open.
