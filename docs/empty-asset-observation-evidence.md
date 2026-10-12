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

The unchanged CI artifact glob already retains `directory-v3-*` files. Each
successful run emits `directory-v3-empty-assets-evidence.json` and twelve PNGs:
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
vulnerabilities. Sixteen focused evidence-validator tests passed. Candidate
aggregate, independent exact-head review, remote CI and actual pixel inspection
are recorded on the PR when completed.

The local real controls command was attempted and failed at Docker startup
(`FileNotFoundError: docker`). No local runtime/screenshot pass is claimed.
CI workflows, fixture modes, producers, schemas, gates, grants, production
defaults, timeouts, retries and all native-reader guard code are unchanged.
There is no nonempty-to-empty transition, inferred deletion, relation or
classification coverage. This improves engineering evidence only; no new
product feature is declared complete, no Issue closes, and product acceptance
remains 0/209. Real AD, eight-version Windows, source-system compatibility,
capacity and production acceptance remain separate gates.
