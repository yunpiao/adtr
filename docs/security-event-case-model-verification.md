# Synthetic case transition model verification

Refs #304, #42 and AD-F-112/115/116. The authority for these tests is the
existing [simulated event contract](events-simulated-contract.md), specifically
“Authorization and case transitions”. This is additional verification of an
existing offline model, not a new production workflow or a claimed bug fix.

## Independent model

`internal/securityevents/case_model_test.go` is an external-package test. It
uses only `NewCase`, `Apply`, `View` and public data types. The expected behavior
is a handwritten six-state, four-action table, not a call to private production
helpers or a result calculated by the implementation under test.

States are open/unmarked, open/suspicious, open/benign and the three corresponding
closed states. Actions are mark suspicious, mark benign, unmark and close. A close
preserves the existing mark; closed states reject further actions. Repeated
no-op marks conflict, and unsuccessful transitions return the unchanged case and
an empty audit. Terminal rejection does not assert an undocumented error class.

The test enumerates all 65,536 length-eight action histories, including rejected
operations and attempts after close. It checks 87,380 prefix transitions, every
resulting public state and revision, the complete audit value, and the unchanged
source value. Equal and increasing instants are represented in a non-UTC zone;
audits must preserve the instant at nanosecond precision and normalize to UTC.

Seven separately constructed states include all six state/mark combinations and
a longer successful open history. Each receives missing-grant and cross-scope
attempts, stale/future revisions, malformed actor/reason values, invalid times,
backward-by-one-nanosecond time and unknown actions. After each failure, all four
continuations are evaluated at the last successful time against both the original
and returned case. This also detects accidental updates of hidden timestamps.
Read-only views, denied views, detached returned values and two independent forks
from the same original case are checked separately.

Eight is a **test exploration bound**, not a product history limit. It keeps the
normal unit suite finite; increasing it grows the history count exponentially.
Removing the bound would not terminate enumeration. The tests do not claim
exhaustive coverage of all identifiers, timestamps, revisions or arbitrary-length
histories. Both pure forks may legitimately succeed: this is not persistent CAS.

## Compiled negative controls

`scripts/verify_case_model_mutations.py` creates a fresh evidence directory and
copies only the stdlib-only core plus the new model test into isolated Go modules.
It never edits the checkout or deletes its generated evidence. Existing output
paths are rejected to prevent overwriting a previous run. Use a new output path
for a repeat. The script forces local Go, disabled dependency/network lookup and
no workspace file; Go 1.27.1 is required to match the repository toolchain.

The unchanged copy must compile and pass first. Fifteen deliberate faults cover:
stale revision acceptance, no-op acceptance, close erasing a mark, mutable closed
state, missing Manage, tenant/domain isolation loss, unchanged revision, backward
time acceptance, unretained transition time, wrong prior mark or actor in audit,
non-UTC audit time, and case/audit mutation on failure. Every fault must compile
successfully and then fail a new `TestCaseModel` assertion with exit code 1.
Compiler errors, crashes and timeouts are not counted as detections. Exact source
and test hashes, compiled status, exit codes and log hashes are saved in
`report.json`; source copies, binaries and full logs remain alongside it.

These are intentionally broken **test copies**, never production patches. The
mutation list is finite and is not a proof that every possible implementation
error would be caught. Exact single-occurrence anchors deliberately fail when
the production source changes, requiring review rather than silently mutating
the wrong statement.

## Reproduction and verification boundary

Activate the verified repository toolchain, then run from the checkout:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -race -count=1 -v ./internal/securityevents
python3 scripts/verify_case_model_mutations.py --output /tmp/adtr-case-model-new-run
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -race ./...
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build ./...
python3 scripts/verify_requirements.py
```

The original package baseline passes before adding the new tests. The model and
all fifteen compiled negative controls pass their respective expectations.
No original test or production file is changed. Exact final commands, logs,
independent review and backup commit are recorded on #304, rather than treating
this document as evidence for a future or different source revision.

Open PR #256 at `775d58f54d2249106e9fdb4855e5d3055f996094` adds snapshot/restore
interfaces to this core, without changing its existing transition implementation.
The new test file does not overlap its changed filenames. Its exact `events.go`
blob `3b186e8369781583590144bccf14a8d5ceaf1d9a` is separately combined with this
model in an isolated module and tested. That check covers only this core/model
combination, not the PR's HTTP, database, browser or dependency stack. Any changed
PR head or main requires fresh compatibility verification.

Only an authorized non-triggering backup branch is published. No PR, CI run,
audit, install, merge or deployment is performed. APT/probe/R4/CI-selection frozen
work and the denied PR225 blob are untouched. Real PostgreSQL/browser/AD/Windows,
persistence transactions, runtime permissions, durability and product acceptance
are outside this offline test. Product acceptance remains 0/209 and the engineering
Issue remains open pending permitted downstream gates.
