# Real empty dictionary-3 asset observation coverage

Refs [P1 engineering #143](https://github.com/yunpiao/adtr/issues/143),
[F14 #30](https://github.com/yunpiao/adtr/issues/30), AD-F-038/039/040/042,
and existing workspace contracts [#125](https://github.com/yunpiao/adtr/issues/125),
[#131](https://github.com/yunpiao/adtr/issues/131),
[#135](https://github.com/yunpiao/adtr/issues/135).
Base: main `8a6672b9710fce8768e5c276fa7775217e695563`.

## Bounded test-only slice

The existing `directory-v3-controls` suite provisions an isolated synthetic
source, registers the real directory Worker, reads five valid empty TLS LDAP
pages, and stores its successful observation in PostgreSQL. The additive test
visits all three actual read-only asset workspaces before the first sync and
after that successful observation. It neither inserts an observation nor
intercepts/fabricates API responses.

- Before sync: each actual browser-consumed API response is `available=false`,
  with no observation/source and no rows. Computer/group computed counts are
  null. The UI says the count is unknown and exposes no paging controls
- After sync: each response is `available=true`, bound to the exact completed
  task and identical source metadata, with empty rows, matching total 0 and
  page count 0. Computer/group pre-filter counts are 0. A subsequent real UI
  search with unchanged empty filters must send the exact observation pin and
  return the same empty response
- Current domain/configuration/credential revisions and actor/no-store headers
  are checked. No hidden search or sort filter can masquerade as an empty
  observation. The readonly page retains source metadata, has no asset table,
  detail/expand action or stale row, and disables both paging buttons
- User assets have no `observedUserCount` field. Their current UI distinguishes
  unavailable observation from “此观测在当前筛选下没有匹配用户。”; the test does not
  invent a new user-count contract. Computer/group wording explicitly states
  the completed observation returned no objects of that kind
- The original successful task, cancellation during paged I/O, proof retention,
  heartbeat, current TOTP, executor-return witness, dependency release,
  cancelled-observation rejection, original-observation retention, storage,
  console and logout assertions stay intact. The new visits use no mutations

## Evidence and verification

The dedicated success-only controls validation/upload steps retain exactly one
evidence JSON and twelve named PNGs. The original collection-suite uploader is
unchanged. Each successful run emits `directory-v3-empty-assets-evidence.json` and twelve PNGs:
three workspaces × two states × desktop 1440×900/mobile 390×844. Screenshots are
explicit captures of readonly pages after proving no password inputs exist;
setup traces, automatic screenshots and video remain disabled. Every screenshot
has measured viewport/document width and a SHA256 binding to its response record.

The native response observer proves complete, uncancelled EOF and released
readers. It never changes the production parser, substitutes a response, or
counts a separate API fetch as browser consumption. Evidence stores only
bounded public response values, digest/length and synthetic source/task identity;
no raw environment, authentication proof or connection credential is retained.

The strict validator checks closed field sets, six ordered distinct state/kind
records, null versus zero, source/config/observation pins, empty results,
uncancelled reader/header evidence, original two-sync/one-cancel budget,
executor-return witness, clean checkout/event metadata, twelve PNG hashes and
geometry, and an exact source-hash set. Run it against the matching checkout:

```sh
python3 scripts/validate_empty_asset_evidence.py \
  /path/to/directory-v3-empty-assets-evidence.json --source-root .
```

Hash/schema validation is separate from independent inspection of actual image
pixels. Validator unit fixtures are deliberately synthetic and cannot count as
real browser evidence.

Local baseline `make check` passed before edits: 147 Python contracts, 2110
frontend tests, Go race/vet/build, TypeScript/build and dependency audit with zero
vulnerabilities. The initial candidate aggregate passed 163 Python contracts and the same 2110
frontend tests and Go/type/build/audit gates. Its independently reviewed tree was
`3613bc981c58fe36605c755e269d2f254bd92d25`. Revised candidate checks and exact-head
review are recorded on the PR; actual pixel inspection remains separate.

The local real controls command was attempted and failed at Docker startup
(`FileNotFoundError: docker`). No local runtime/screenshot pass is claimed.
Fixture modes, producers, schemas, gates, grants, production defaults, timeouts,
retries and all native-reader guard code are unchanged. The workflow evolution
is limited to the two success-only evidence steps described below.
There is no nonempty-to-empty transition, inferred deletion, relation or
classification coverage. This improves engineering evidence only; no new
product feature is declared complete, no Issue closes, and product acceptance
remains 0/209. Real AD, eight-version Windows, source-system compatibility,
capacity and production acceptance remain separate gates.


## First real run and corrective evidence gate

Published head `3d9fee48a68aa70d5125c99822bd35493f319f87` ran in
[CI 38231945057](https://github.com/yunpiao/adtr/actions/runs/38231945057).
[Controls job 114753458548](https://github.com/yunpiao/adtr/actions/runs/38231945057/job/114753458548)
failed at the final evidence validator after 5.8 minutes: `checkout parents`.
The new no-observation/observed-empty API/UI/pinned rereads, twelve screenshot
captures, original cancellation/retained-observation and storage checks had run;
logout assertions after the evidence write were not reached. This is a failed
suite, not a runtime pass.

`git show --format=%P` suppresses parents at a shallow checkout boundary.
The correction reads the immutable commit object with `git cat-file -p HEAD`,
matching the existing provenance helper convention. The validator still requires
one or two valid parent SHAs; no assertion was relaxed. A real local depth-one
clone regression proves `%P` is empty while the raw parent header remains intact.

The first run also exposed an artifact-routing gap: the pre-existing artifact
named `directory-v3-controls-evidence` is uploaded only by `matrix.name ==
'directory-v3'`. Its artifact 11739890949 belongs to that collection suite and
cannot prove this controls slice. The twelve controls screenshots were not
uploaded. This original failure and gap are retained rather than relabeled as
successful evidence.

The explicitly reviewed workflow evolution adds exactly two steps to the
existing browser job, both gated by `success() && matrix.name ==
'directory-v3-controls'`: strict evidence validation followed by the existing
SHA-pinned upload action. The uploader lists thirteen exact basenames, never a
broad file suffix, raw diagnostics or hidden files; missing files fail. The
validator's workflow discovery accepts only this checkout's `web/test-results`,
requires one document and one copy of every fixed PNG in that document's directory,
rejects symlink paths/duplicate matching files and verifies the closed response,
source/PNG hashes and viewport evidence before upload. The original
`directory-v3` evidence step remains byte-identical.

The browser 8-to-4 restoration invariant is retained. Its new trusted restored
baseline is original main `8a6672b` plus only these two steps, with SHA256
`51fc3120f8f4770f6d3058dbe726e1a3da6847d2203e4f0facfac9598e37456f`.
An additional contract removes exactly the two added steps and verifies the
previous full workflow hash
`a0368741a173743aa8ffddfb6a54a115d7ac9f01d775d494475c51bac503072b`.
Both constraints remain fail-closed. Fifty jobs, dependency graph, matrix sizes,
concurrency, timeout, permissions, browser guards and retry policy do not change.
