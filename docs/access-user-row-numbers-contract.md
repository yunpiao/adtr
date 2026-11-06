# Platform-user page-local row numbers

Engineering Issue [#294](https://github.com/yunpiao/adtr/issues/294) follows
original [#9](https://github.com/yunpiao/adtr/issues/9), AD-F-171 / UI-0579:
`el-table-column`, label `序号`, `type: index`. The original record does not
freeze cross-page numbering. This is an explicitly bounded local interpretation,
consistent with existing asset workspaces, not a claim of legacy global-index
compatibility or full product acceptance.

The platform-user table labels the new column `本页序号`. It displays 1..N in
exactly the current accepted response's order and restarts at one on each page.
The persistent user ID, username, React row key and identity-based selection are
unchanged. The ordinal is neither a user identifier nor a cross-page rank. It
has no control, sorting or mutation behavior. Creation/password-time sorting,
filters, page sizes including the existing bounded all mode, and server order
retain their current meaning. Empty results contain no invented row or ordinal.

The column follows the role column. This keeps selection, username and role
positions unchanged for existing users and consumers. Existing wrapping and
horizontal table scrolling are reused; no shared style or content-security
policy changes. The extra column can increase internal horizontal scrolling on
small screens. Removing it only removes a position cue; it does not affect
identity, permissions, query behavior or assignment.

Numbers exist only while their current user rows exist. The existing read
lifetime clears rows during replacement/error and rejects late responses after
replacement or unmount. Read-only users get the same display column through
their existing user-read permission, without assignment or proof controls.
No new configuration or query parameter is required. No user data is persisted
in browser storage or navigation by this change.

The dedicated synthetic DOM suite covers distinct IDs, actual server order
under all four existing sorts, later pages, every exposed page size, applied
versus draft filters, time filters, empty/error/recovery states, selection
identity, read-only controls and late/unmounted responses. These are client
contracts, not actual API/PostgreSQL/browser results or capacity evidence.

The native addition runs after the existing final user response has been
validated against both original synthetic IDs and before the unchanged
1440×900 bulk-role screenshot. It verifies one labelled scoped header, exact
1/2 cells and full viewport visibility. It adds no request, click, context,
fixture, mutation, artifact, viewport adjustment, timeout or cleanup change.
An exact outer source inverse restores the previous complete access spec;
the unchanged time, role-user and bulk-role inverses then restore their frozen
predecessors. The earlier guards and source hashes retain their original
meaning. The old safe JSON does not independently attest row numbers: actual
current-source assertions and independent inspection of the real PNG are
required. No new mobile native screenshot is claimed.

Independent source review, exact-head required CI, actual native evidence and
main compatibility remain separate gates. Local Docker is unavailable, so
native runtime cannot be marked passed based on local checks. Issue #9 remains
open, all 209 original requirements remain in scope, and product acceptance is
0/209. No real AD, deployment or security-permission change is included.
