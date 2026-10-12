# Bounded cross-language diagnostic test workload

Refs [engineering #145](https://github.com/yunpiao/adtr/issues/145),
[#137](https://github.com/yunpiao/adtr/issues/137) and
[#141](https://github.com/yunpiao/adtr/issues/141).
Base: main `54b560e03e944e7c5cd521ec0553c10b3a4247e1`.
This is a test-only correction, separate from PR #142's published credential-grant
instrumentation and PR #144's empty-observation evidence. No product acceptance
count changes and no Issue closes.

## Preserved failure and established facts

[Main-push run 38238968567](https://github.com/yunpiao/adtr/actions/runs/38238968567),
[contracts job 114776678469](https://github.com/yunpiao/adtr/actions/runs/38238968567/job/114776678469),
failed the existing `safe-request-diagnostics.test.ts` test “publishes actual
mixed TypeScript output through the independent Python schema”. Vitest reported
5775ms against its unchanged 5000ms test deadline; 2109/2110 frontend tests passed.
The failed aggregate did not proceed to that job's database/lifecycle gates.

The test source is byte-identical to prior main `8a6672b`. On the identical
application/test tree in [PR run 38235111833](https://github.com/yunpiao/adtr/actions/runs/38235111833),
[contracts job 114764405628](https://github.com/yunpiao/adtr/actions/runs/38235111833/job/114764405628)
passed the target in 1340ms. Its separate 1500-event grant stress test took
508ms versus 3813ms in the failing main job. This is a wall-clock test failure,
not an empty-observation API or main-checkout provenance failure. The exact
hosted resource cause is not established; no infrastructure fault is asserted.

The old cross-language case emitted one legacy event plus 1000 rounds of two
GET events and one snapshot event: 3001 immediate full-file writes, although its
purpose was bounded format compatibility, not an I/O benchmark.

## Local measurement before changing the test

An isolated fresh main worktree passed the unchanged full `make check`: 168
Python contracts, 2110 frontend tests, Go race/vet/build, type/build and dependency
audit with zero vulnerabilities. Unchanged focused and paired diagnostic tests
passed with target durations 225ms and 222ms. These passes do not erase the
hosted failure.

A temporary test-only timing probe, removed before the candidate checks, measured
the actual GET/checkpoint loop and the Python subprocess. No recorder/writer was
changed. Using the same unchanged test with temporary files on:

- dot tmpfs: total 207ms; GET 76.5ms, checkpoints 66.3ms, Python 28.1ms
- dot workspace overlay: total 620ms; GET 321.0ms, checkpoints 230.9ms, Python 26.4ms

Both outputs retained 242 events, dropped 2759, and were 61375 bytes. The large
change in recorder-containing segments with a stable Python segment demonstrates
local file-path sensitivity. It supports removing redundant I/O from this test;
it does not identify the exact hosted disk, CPU or scheduler cause. Syscall
tracing was unavailable under the local sandbox, so no syscall-level profile is
claimed.

## Minimum workload and stronger assertions

Only the cross-language test changes. It retains real TypeScript GET/checkpoint
recording, every synchronous write, real filesystem data and independent Python
validation/publication. There is no filesystem mock, fabricated final capture,
recorder change, larger timeout, fake timer or retry.

- `ceil(256 / 3) = 86` is the minimum whole-round count for the initial legacy
  event plus three events per round to exceed 256: `1 + 3 × 86 = 259`
- The actual first three emitted event shapes are encoded with every number
  conservatively replaced by the shortest numeric encoding, zero. Their combined
  minimum is 743 bytes including separators. `86 × 743 = 63898 > 61440`, so even
  this lower bound necessarily crosses the existing byte budget. The test
  asserts this relation instead of relying on a particular machine's timestamps
- Append one real sanitized legacy event and one real checkpoint: total 261.
  This makes the final bounded file actually retain legacy/API/step families;
  the old initial legacy event was evicted by its long stress loop
- Assert exact conservation `dropped + retained = 261`, positive drops, fewer
  than 256 retained records (byte budget was active), the existing encoded byte
  bound, exactly the three event families and canary absence in raw/published data
- The actual Python publisher must preserve every browser event field/value and
  exact dropped count. Python checks full equality with its input and TypeScript
  independently compares the published output with its pre-publication capture

The new local overlay run took 113ms and produced 245 retained / 16 dropped /
61394 bytes; a focused three-file run passed all 40 tests with the target at
64ms. These are observations, not a performance promise or a relaxed threshold.

The separate 1000-event exact count test still requires 256 retained / 744 dropped;
the separate 1001-event mixed/last-failure byte test and 1500-event grant stress
case remain unchanged. All invalid-schema, native lifecycle, fault injection,
canary, original-error and request-policy tests remain. No production code,
diagnostic writer, Python schema, workflow, concurrency, suite, timeout, retry,
authorization or artifact policy changes.

Final aggregate checks, independent exact-head review, draft PR CI and eventual
merge/main-push checks are recorded on the PR when actually completed. Original
main failure remains historical evidence; this candidate needs its own full
50-job check and cannot inherit PR #142 or #144 success.
