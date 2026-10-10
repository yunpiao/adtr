# Same-run PostgreSQL image preparation

Engineering issue: [#96](https://github.com/yunpiao/adtr/issues/96).

## Conclusion and measured baseline

The candidate removes repeated PostgreSQL registry pulls from 31 isolated
acceptance jobs and aligns npm's cache path. Browser concurrency is now a
bounded **4-slot canary**; auth/fixed matrices remain at **2**.
All original 34 jobs remain; `prepare-postgres` adds a required 35th job.
Every original test, assertion, timeout, permission and workflow event is retained.

The three sampled green 34-job runs took 74–76 minutes. Their actual timestamps
and explicitly labelled scheduling simulations are in
[ci-acceleration-baseline.json](evidence/ci-acceleration-baseline.json).
About 83% of browser runner time was real acceptance work, so cache changes alone
cannot remove most waiting. A four-slot simulation predicts roughly 37–38 minutes
for the existing browser workload. The measured four-slot canary is recorded
below; the candidate integrated with newer main still needs exact-head full CI.

The preceding unchanged two-slot head `aa6dab827986fae646f9e359579acffe38615ed2`
passed [all 35 jobs on attempt 1](https://github.com/yunpiao/adtr/actions/runs/38074594460)
in **76m31s**. That is slightly slower than the recent baseline, so this stage
proved stable delivery rather than an end-to-end speedup. All 31 consumers
loaded the verified artifact; download steps were 1–19s (median 4s), and
verification/load was 4–13s (median 7s). The later npm cache restored 26,777,873
bytes into the intended directory instead of the old 1,074-byte cache. Full
measurements are preserved in [the two-slot evidence](evidence/ci-image-sharing-two-slot.json).

The four-slot head `fd733de6ab50dc3d3863fa44e6de5ddf92d7546d` completed
[all 35 gates after a failed-job-only retry](https://github.com/yunpiao/adtr/actions/runs/38080292537).
Its first attempt took **39m01s**, with all 24 browser suites passing, but the
existing integration-tagged Go watchdog test timed out and `verify` correctly
failed. The unchanged contracts retry took 7m42s; total time to green, including
the observation gap, was **48m32s**, versus two slots' **76m31s**: an observed
27m59s (36.6%) reduction. The first attempt is not an all-green timing result.
The browser matrix itself took 38m28s; aggregate browser acceptance work was
7,050s versus the prior 7,072s. This is one measured comparison, not a guaranteed
speedup across runners or later source changes.

All 31 required consumer loads passed. Download steps were 1–5s (median 3s),
verification/load was 4–12s (median 7s), and the observed job-interval peak was
11 standard runners, including four browser jobs. The retry reused producer
attempt 1 and artifact `11679857692`; it did not rerun PostgreSQL preparation.
The watchdog subtest passed unchanged on retry. Its 40ms transient-state window
can be missed by its polling observer, but the original log lacks final-state
and scheduling/IO evidence, so the cause remains unestablished. No assertion,
timeout or Go source was changed. Details and the retained first failure are in
[the four-slot evidence](evidence/ci-image-sharing-four-slot.json).

Main `ebf6ce321b8d8e99a31c3b3ea7251352389d902c` (PR #94) is now integrated
normally, including the exact successful task-workspace artifact step. Its
expanded task coverage is newer than the timed canary above. The integrated
candidate's real probe, complete 35-job CI and post-merge main CI remain required.

## Trust chain and failure semantics

1. A single producer prepares the unchanged official source/tag/index digest from
   [container-image-provenance.md](container-image-provenance.md). It checks an
   existing local image first, then allows at most three pull attempts with 10s
   and 30s backoff for recognized throttling/network failures or a client timeout.
   Each retry inspects first. Permission, certificate, source and digest failures
   immediately fail, even if the same diagnostic also contains `429`.
2. The producer retrieves original registry index/manifest bytes. The fixed index
   SHA256 must select exactly one `linux/amd64` child with the known manifest
   digest; the child must name the known config digest. The config digest is the
   Docker image ID. Raw metadata is hashed before parsing; reserialization would
   change its digest. Unknown schemas/media types/platforms fail.
3. `docker image save` writes only that fixed image ID, then gzip compresses it.
   The bundle includes the original index, manifest, archive and a metadata file
   binding source, platform, exact checkout SHA, event head/base, run ID, producer
   attempt, archive size and SHA256. Nothing is published to a registry.
4. Each consumer requires one numeric immutable artifact ID from its producer's
   `needs` output. `download-artifact` receives neither a cross-run selector nor
   a token. Explicit guards prevent an empty ID from meaning “download all”.
   The producer's archive SHA256 is passed separately through its job output;
   consumers check it before `docker load`. GitHub's own digest warning is not
   the integrity gate.
5. After load, the exact config/image ID and `linux/amd64` platform must match.
   Runtime setup revalidates the bundle, receipt, current run/checkout and local
   image, then starts containers by that image ID with `--pull=never`.
   `RepoDigests` surviving save/load is not assumed. No consumer pulls on failure.
6. Integration, browser database and synthetic LDAP fixtures all use this route.
   Lifecycle checks get an ephemeral Compose override affecting only `db.image`
   and `db.pull_policy: never`; original Compose/Dockerfile source pins remain.
   The Go build image remains an ordinary pull in the existing contracts job.
   PostgreSQL has one successful pull chain, not one total HTTP request: registry
   metadata requests and bounded retries can still contact ECR.

A failed, cancelled or skipped preparation job blocks its consumers and fails
`verify`. Native browser-only diagnostics do not need the PostgreSQL artifact.
A failed-job-only rerun uses the original successful producer's artifact ID and
attempt, rather than rebuilding a name from the newer consumer run attempt.

## Configuration and costs

- `npm_config_cache=/tmp/adtr-npm-cache` is required while Makefile/CI explicitly
  use that directory. It makes setup-node restore the content cache used by
  `npm ci` and `npm audit`; removing it restores the previous path mismatch.
  Lockfile installation, npm integrity checking and audit always still run.
  A useful cache consumes more storage than the previous near-empty cache.
- `ADTR_CI_POSTGRES_BUNDLE`, `ADTR_CI_POSTGRES_ARCHIVE_SHA` and
  `ADTR_CI_POSTGRES_PRODUCER_ATTEMPT` are required on CI acceptance steps. They
  select the verified same-run artifact, not an arbitrary image override.
  Removing them returns that script to the original local fixed-pin behavior and
  would reintroduce remote pulls in CI; workflow contracts guard their presence.
  Local development without these variables remains unchanged.
- `--pull=never` / Compose `pull_policy: never` are necessary for artifact
  consumers. Missing/corrupt images fail rather than silently hitting ECR.
  Removing these settings would undermine the no-fallback guarantee.
- Browser `max-parallel: 4` allows four independent isolated suites to run
  together and targets the two-slot queue, after the complete shared-image run
  passed. Auth/fixed matrices keep `max-parallel: 2`; every matrix keeps
  `fail-fast: false`, and Playwright keeps `workers: 1`, `retries: 0`. The bound is
  required to limit peak resources; removing it permits a scheduling burst.
  Setting it back to two preserves every test but restores the earlier queue.
  The potential peak is 11 standard hosted jobs during overlap (4 browser +
  2 auth + 2 fixed + 1 contracts + 2 native diagnostics), versus 9 with two browser
  slots. The prepare/native startup peak is three, and verify runs afterward.
  Each consumer uses its own runner/database; no added ECR pulls are required.
  Real resource/transfer failures require investigating or reverting concurrency,
  not weakening checks. Account-wide runner availability is not assumed.
- The shared image is stored for one day. Consumers incur archive download,
  SHA256, load and runtime validation costs; exact bytes/times are recorded by
  the probe. This is a same-run immutable artifact, not a cross-run cache.

## Verification order

1. Run all Python contracts and `make check`; independently review the actual diff.
   Synthetic unit fixtures validate failure handling, not real Docker behavior.
2. Publish the reviewed commit to `ci/image-transfer-probe/**`, initially without
   a PR. Only the separate two-job diagnostic runs. It uses the real producer
   and consumer verifier, proves wrong-SHA/run/checkout/corrupt-archive hard
   failures, and checks real PostgreSQL 17.6 readiness in a network-isolated
   container running the exact loaded ID. Ownership-labelled cleanup is bounded.
3. Record preparation, upload/download/load time, archive size and exact SHA.
   Open the draft PR on that unchanged commit only after the probe succeeds.
4. Require all 35 jobs on the exact PR head/merge checkout, review/base/dependency
   checks, and post-merge main CI. A passing image probe is not a full CI pass.

No Docker executable is available in the current cloud development workspace.
The [two-job remote probe](https://github.com/yunpiao/adtr/actions/runs/38074448398)
and subsequent 35-job run verified the real transfer, database execution,
all prior acceptance gates and useful npm cache restoration. Independent byte
review additionally matched the fixed index, manifest, config, all ten ordered
layer tar hashes and all 22 content-addressed saved blobs. One four-slot canary
has completed with the retry and timing limits above; current integrated-head
and post-merge main validation remain open. Product acceptance
remains 0/209; this change does not deploy anything.

References: [artifact validation](https://docs.github.com/en/actions/tutorials/store-and-share-data#validating-artifacts),
[Docker raw image metadata](https://docs.docker.com/reference/cli/docker/buildx/imagetools/inspect/),
[OCI config/image ID](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md#imageid).
