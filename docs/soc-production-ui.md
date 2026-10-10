# SOC production user-assets workspace

Refs [#91](https://github.com/yunpiao/adtr/issues/91), [F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-038/039, and [prototype PR89](https://github.com/yunpiao/adtr/pull/89).

## Design evidence and bounded scope

The visual/interaction reference is PR89 head `2ec70500881038251ddb1678691d9ecd10aebb90`, with actual screenshots from [prototype QA 38048701866 / artifact 11668832031](https://github.com/yunpiao/adtr/actions/runs/38048701866/artifacts/11668832031). The workbench, asset drawer, desktop investigation, mobile workspace/investigation, and deepest mobile management screenshots were inspected. All eight prototype runtime file hashes match the reference checkout; the archived prototype QA records 76/76. Those results establish the reference, not production verification.

This production slice started from main `ce8df2281b3766cb41b571452911e7f5959ec5a6`. The user merged PR89 at 2026-10-10 12:47:50 UTC; the development branch then fast-forwarded to main `665fcf7c2ab7373d9e8e9a7a4e289ce8a121a527` without overwriting production changes. That base movement adds only the isolated prototype and its own workflow. It adapts the approved structure to capabilities already backed by production APIs:

- 200px dark grouped rail, 56px compact context bar, pale canvas, thin-border panels, responsive navigation
- User assets, background tasks and operation audit as direct operational entrances; platform management, collection/access and account identity as discoverable collapsible groups
- Existing account/forced-password/session/authorization flows and all nineteen existing entrances retained; each workspace continues its server permission check
- Compact selected-source identity with an explicit change control; the existing choose → resolve workflow and exact source revisions are unchanged
- Real `/api/user-assets/v2` search/paging and `/detail`, with fixed observation/GUID, truthful raw facts, compact provenance, and mobile cards
- A 520px right-hand detail drawer (full viewport width on mobile), stable identity, source facts, close/return controls and keyboard focus restoration

No prototype runtime file is imported or modified. Synthetic alerts, enabled/risk badges, scores, relation edges, response results and unsupported filters are not exposed as production facts. Their corresponding backend Issues remain prerequisites. There is no all-domain asset aggregation, invented status, new collector, production deployment, credential transmission, schema migration or permission expansion in this slice.

## State, permissions and accessibility

All [user-assets API/source identity contracts](user-assets-v2-contract.md) remain in force. Search draft versus applied query, explicit submission, captured observation pins, refresh semantics, source conflict, invalid observation, denied/missing objects, and fatal read cleanup are unchanged. Closing details invalidates its request before returning focus. Source change discards list/detail/query identity. Details do not introduce a second cache, localStorage data, or URL-based permission assumptions.

The source chooser collapses only after successful resolution. Opening it alone does not change the selected source; a changed field/selection clears protected rows immediately, before another resolution. Failure clears the source and exposes selection again. Full provenance is expandable; the exact observation identifier and completion timestamp stay visible while browsing.

The custom modal stays inside the authenticated main subtree. It does not use a portal or native top layer that could escape the parent's hidden/inert session revalidation gate. While active, background siblings are inert, keyboard Tab wraps within visible drawer controls, Escape/Close cancel its read, and scrolling belongs to its body while its identity/close/footer remain reachable. Cleanup restores prior inert/scroll state and returns focus only to a still-connected, visible, non-inert originating row. Actor/source/workspace invalidation cannot restore detached private content.

Mobile navigation has its own focus loop and backdrop; main/topbar are inert while it is open. Navigation, history, breakpoint and session changes close it. The active navigation group opens automatically; inactive management groups initially stay folded. Test navigation helpers click the real group summaries and mobile menu rather than forcing visibility or rewriting URLs.

## Verification record

Clean unmodified baseline `make check`: passed, with 60 Python contracts, all Go race/vet/build checks, 31 front-end files / 1,017 tests, TypeScript/build and dependency audit (0 vulnerabilities).

Initial candidate `make check` passed: 60 Python contracts, Go race/vet/build, 33 front-end files / 1,042 tests, TypeScript/build and dependency audit (0 vulnerabilities). Formatting, Playwright discovery (31 tests) and a final post-typography build passed. After integrating main `665fcf7`, full `make check` passed again with 33 files / 1,043 front-end tests, 60 Python contracts, Go race/vet/build, TypeScript/build and audit (0 vulnerabilities). The isolated unchanged prototype also passed 27 static/VM checks. These local results do not replace exact-head CI.

Independent actual-diff review found two real lifecycle defects, both fixed and reproduced again against the actual App under StrictMode: same-identity session revalidation had removed sidebar inert while the drawer remained open; effect cleanup/replay had left initial focus on its now-inert originating row. The shell now declares modal ownership across revalidation, and the drawer owns setup focus. Focused independent review passed 119/119 tests and TypeScript. The maximum-length identity area is bounded and independently scrollable so its fixed close control and facts retain space; native narrow/landscape pixel checks remain part of browser verification.

Actual browser screenshots and exact-head full CI are recorded in the linked PR after they run. Local Chromium launch is blocked by the current cloud executor's OS socket limitation, including elevated launch. DOM/unit and TypeScript success are not recorded as browser or PostgreSQL success. The existing isolated GitHub CI remains responsible for actual API/worker/TLS LDAP/PostgreSQL/browser suites, container lifecycle and both supported browser tracks.

The first true browser screenshots must cover desktop visible rows, factual detail, a 390px list/detail, real mobile grouped navigation, Escape/focus, source/permission invalidation and native cross-tab session checks. Existing API assertions, security evidence, timeout thresholds and full CI gates remain intact.

This is an engineering/UI slice. F14 #30 remains open; product acceptance remains 0/209. Broader assets, real AD/eight Windows versions, capacity and release gates are not established by this layout change.
