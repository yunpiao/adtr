# User and role keyword clearing

Engineering [#296](https://github.com/yunpiao/adtr/issues/296) follows original
[#9](https://github.com/yunpiao/adtr/issues/9), AD-F-171 / UI-0578 and AD-F-174 /
UI-0627. Both source controls explicitly declare `clearable` and a 50-character
maximum. This bounded slice adds the missing per-keyword operation; the source
records alone do not establish legacy timing or full compatibility.

## Interaction contract

Each list has a separately labelled clear button next to its keyword input.
It is a non-submit button outside the input's wrapping label. Mouse or keyboard
activation empties only that uncontrolled input's draft value and focuses it.
Clearing an already empty value is harmless. The help text says that the existing
submit action is needed to update results.

The applied query, current page, rows, selected user identities, other filter
drafts, sort, page size, error and pending-read lifetime are unchanged. Refresh
and pagination continue using the previously applied search. Explicitly submitting
the empty input uses the existing FormData path: empty `search` is omitted from
the query and pageIdx returns to one. The existing whole-form reset still resets
all controls and the applied query; neither handler is changed.

The `maxLength={50}` attribute preserves the existing browser UTF-16 input limit.
It bounds ordinary input entry and is not a replacement for existing server
validation. Removing it would let ordinary input exceed that browser limit;
this change neither adds Unicode normalization nor alters backend search rules.
The new input ref is required only for draft clearing/focus. Removing the control
would remove that convenience without changing query or authorization behavior.
There are no new configuration parameters, API endpoints, permissions, storage,
mutations, automatic requests or background work. The extra button/help can make
the filter area taller; existing table scrolling and viewport behavior remain.

## Evidence boundaries

Synthetic DOM tests cover both list controls, labels/limits, Enter/Space focus,
repeated/empty clear, all other user-filter drafts, selected users, page 2,
refresh/pagination, explicit empty submission, whole-form reset, read-only mode,
pending/error recovery, newer reads and unmount rejection. These tests verify
client behavior, not PostgreSQL or native browser acceptance.

The additive native check uses the existing isolated access suite's settled role
list before role creation and settled user list before its unchanged final query.
It captures actual rows and other form fields, exercises draft clearing and
keyboard activation, and observes zero matching list requests during that bounded
action interval. It restores the original draft and always removes its listener.
It adds no API reads, submits, fixture writes, contexts, screenshots, viewport
changes, timeout extensions or proof operations. This interval is not a proof
against an arbitrarily delayed request. Existing final query, timestamp/ordinal
assertions, evidence JSON, screenshots and cleanup are preserved byte-for-byte
under an exact outer source inverse; earlier guards keep their hashes and meaning.

Local Docker/real PostgreSQL/browser execution is unavailable. Exact-head native
CI, actual artifacts, independent source review and current-main compatibility
remain separate gates. No screenshot specifically showing the new controls is
claimed by the existing safe table image. #9 remains open, all 209 requirements
remain in scope, and product acceptance remains 0/209.
