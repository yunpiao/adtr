# GitHub-hosted private Hyper-V AD lab: integrated, unexecuted draft

This directory contains concrete Windows PowerShell 5.1 implementations. It does
**not** establish a Windows installation or real AD acceptance pass. Local checks
are portable source-regression checks, not Windows parsing or lifecycle tests.

The separate capability probe started a **diskless disconnected VM** on hosted
`windows-2025` image `20260925.250.1`:
https://github.com/yunpiao/adtr/actions/runs/37787447038
It did not boot Windows or run this controller. Observed resources were 2 logical
CPUs, 7.99 GiB RAM and 73.01 GiB temporary disk free. Runtime checks still apply.

## Entrypoints and source checks

- `Invoke-HostedLab.ps1`: two mutually exclusive image-input modes, exact ownership
  journal, pinned-source test build, two isolated guests, real guest stages and
  reboots, fixed test evidence, and cleanup in `finally`.
- `Host.Common.ps1`: host/source/topology/lease guards and exact-resource cleanup.
  Its definition-only import of `Image.Common.ps1` exposes image cleanup to both
  the controller and independent cleanup without executing image preparation.
- `Image.Common.ps1`: reviewed separately; creates a never-booted evaluation base,
  injects per-child setup credentials, and removes exact owned mounts. See
  [IMAGE-PREPARATION.md](IMAGE-PREPARATION.md) for its full contract.
- `Invoke-MemberTests.ps1`: copied into the verified member; runs exact named Go
  tests with only the synthetic reader credential available to the test process.
- `Remove-HostedLab.ps1`: independent cleanup for an Actions `if: always()` step.
  `-WaitForLease` is also used by the controller's credential-free job-local
  watchdog. It is not a scheduled task, service or persistent reaper.
- `Test-HostScriptSyntax.ps1`: parse-only Windows PowerShell 5.1 check including
  `Image.Common.ps1`; no lab code is sourced/executed. Also run the guest helper.
- `test_host_contract.py`, `test_image_contract.py`: portable source-string checks.
  They do not validate PowerShell grammar, execute mocks, provision or boot guests.

## Common inputs and fixed-source build

Both modes require:

- `SourceSha`: full immutable 40-character commit, equal to clean checked-out HEAD,
  `ADTR_SOURCE_SHA`, `GITHUB_SHA` and `GITHUB_WORKFLOW_SHA`
- `AdministratorPassword`: caller-owned in-memory SecureString, newly generated
  for this one lab, at least 20 characters; no real/shared credentials
- `TrustedLicensedMedia`, `DisposableLab`: explicit switches confirming approved
  scope and trusted media; they do not grant license approval or purchase rights
- `LeaseMinutes`: 15–90, default 90; the current job's literal `timeout-minutes`
  must be no greater than this value and never greater than 90

The workflow must already provide the official Go 1.27.1 Windows amd64 toolchain.
The controller compiles `./tests/adlab` itself using `go test -c -tags=realad`,
`-mod=readonly`, `-buildvcs=true`, `-trimpath`, and CGO disabled. Both version
inspection and compilation use a cleared environment with local-only toolchain,
module downloads disabled, no workspace overrides, no inherited Go flags and no
Actions tokens. Build caches live inside the owned temporary root. Missing
required dependencies fail the build. The checkout is checked before and after
compilation. The resulting binary is freshly hashed, transferred in 512 KiB
chunks and checked again in the member. No caller-supplied test executable is
accepted as proof of this source SHA.

Keep downloaded inputs outside the checkout. Run earlier Python checks with `-B`
or equivalent bytecode suppression, so validation itself cannot dirty the source.
Pass the SecureString in the same PowerShell process, never in shell command
arguments, environment files, transcripts or artifacts; dispose it in the caller's
own `finally`. The controller generates/disposes separate DSRM and reader values.

### Mode 1: supplied boot-ready VHDX

Supply `BaseVhdPath` and independently trusted `BaseVhdSha256`. The image must be a
local, detached, standalone fixed/dynamic generation-2 UEFI VHDX, <=80 GiB virtual
size, using Microsoft Windows secure-boot keys. Guests must be Windows Server
2022 build 20348 or Server 2025 build 26100 and boot as fresh standalone machines.
The built-in Administrator must already authenticate with the supplied ephemeral
SecureString after specialization/OOBE. Merely being generalized is insufficient.

This mode does not mount, alter, license or remove the supplied base. It writes no
answer file. Media/initial-credential preparation is the caller's separately
approved responsibility; do not bridge it with real or reusable credentials.

### Mode 2: pinned evaluation ISO

Supply all of:

- `IsoPath`, `IsoSha256`: already-local, trusted official evaluation ISO and its
  independently checked SHA-256; no download or registration occurs here
- `ImageIndex`: explicit selected WIM index, 1–8; the helper verifies Standard
  Evaluation, Server Core, x64, English, build 26100 rather than trusting the index
- `LicenseRelativePath`: exact selected image's `license.rtf` under
  `Windows\System32\[en-US\]Licenses\<channel>\ServerStandardEval\`
- `LicenseTermsSha256`: historical parameter name for the SHA-256 of the exact
  embedded license **notice**. It pins that notice's bytes, not the linked full
  agreement, and is not evidence that the user accepted either document.
- `LicenseAcceptanceConfirmed`: caller assertion that the actual applicable full
  agreement was separately disclosed to and accepted by the user, including the
  terms linked from the notice. No automatic acceptance, default approval or
  substitution with permission to research/download/continue is allowed.

The ephemeral Administrator password must be 20–127 characters and satisfy the
helper's complexity checks. No credentials go into the shared base image.

Order is important:

1. Check host, source, explicit approval inputs and ISO pin without mounting it.
2. Persist exact run ownership, create its private temporary root, and verify the
   job-local watchdog handshake. Compile the pinned-source tests.
3. Under the resource mutex, prepare the evaluation base. The helper reserves all
   paths/mount intents, mounts the selected WIM read-only, matches the embedded
   notice bytes to the inspected notice hash, then creates/applies the owned
   40 GiB dynamic base. This byte check does not fetch, hash or approve the linked
   full agreement; the caller must already hold the separate user acceptance.
   The helper prepares the base and its own UEFI boot partition. It never selects the host boot store.
4. Verify the base is ready and detached, with no unresolved native operation.
   Record its resulting hash. Create two differencing children and inject each
   child's answer file. **Both** children must be ready and unmounted before
   either VM is created or started.
5. After authenticated PowerShell Direct readiness, require setup/OOBE completion
   and exact LabId/role setup ownership, purge the fixed answer-file/setup-log
   allowlist, and require `{LabId, Role, Purged=true}`. Persist each purge result
   before copying bootstrap scripts. Both purges must succeed before any AD stage.

Unlike supplied-image mode, this mode necessarily writes the ephemeral password
in plaintext into each disposable child's restricted-ACL `unattend.xml` for first
boot. It is never written to a host staging file, command argument, log, artifact
or shared base. The helper clears temporary native buffers; the guest purge
removes specified setup copies/logs after OOBE. Filesystem deletion is not a claim
of forensic erasure; the disposable disks and hosted runner must still be destroyed.
If first boot/purge is uncertain, fail and destroy the run rather than adopt it.

`evaluation_installation_verified` becomes true only after both Windows guests
actually reach owned, completed setup and pass the purge contract. Image-file
creation, boot intent, source checks or elapsed time cannot set it true. It is
separate from the final AD transport result and verified cleanup.

## Current-task authority and constrained source provenance

Only GitHub-hosted Windows in repository `yunpiao/adtr` is supported. Hyper-V and
an elevated Windows PowerShell 5.1 host must already be available. No host role
installation/reboot, self-hosted runner, persistent access, secrets, OIDC, scheduled
task, public listener or networking setup is introduced.

Authority is the user's existing bounded installation task and the separately
required license decision. The task publisher must review every commit it
publishes/runs for that task. Runtime provenance checks establish which commit
runs; they are not independent proof of human approval. There is no approval
comment, approval ref, repository secret, setting or other persistent gate.

Both dispatch and PR runs require:

- The literal dedicated branch `feat/ad-lab-windows-install`
- Repository `yunpiao/adtr`, owned by account ID `11422136`, login `yunpiao`
- `GITHUB_ACTOR_ID=11422136`, actor login `yunpiao`, and triggering-actor login
  `yunpiao`, plus matching event sender ID/login
- `GITHUB_RUN_ATTEMPT=1`. Reruns are rejected because the environment has no
  standard immutable triggering-actor ID; the controller does not expand access
  or invent an identity-lookup route to approve them.
- Clean checkout and one full immutable SHA shared by HEAD, `SourceSha`,
  `ADTR_SOURCE_SHA`, `GITHUB_SHA` and `GITHUB_WORKFLOW_SHA`

For `workflow_dispatch`, `GITHUB_REF` must name that exact task branch and no PR
expectation parameters are supplied. For `pull_request`, additionally require:

- `ExpectedPullRequestNumber`, `ExpectedPullRequestHeadRef` and
  `ExpectedPullRequestHeadSha` equal the actual event provenance. The expected
  head ref must be the fixed task branch. These values may be copied from the
  event as **expectations**, not as purported authorization.
- Event PR author ID/login is the same fixed owner. Both head/base repository
  names and owner identities match `yunpiao/adtr`; forks and
  `pull_request_target` are rejected.
- Checkout the immutable synthetic merge `GITHUB_SHA`, not a mutable branch or
  the head SHA while claiming it is the workflow commit.
- Fetch sufficient ancestry (`fetch-depth: 2` for an ordinary two-parent synthetic
  merge, or `fetch-depth: 0`) to prove exact event base/head parents. Checkout's
  default depth 1 is insufficient and fails closed. The merge's **entire Git tree**
  must equal the event head tree, including workflow and controller. Additional
  base changes reject the run rather than being silently attributed to the head.
- Report executed `source_sha` and its `source_head_sha` separately.

This removes the self-reference problem: no commit tries to embed its own future
SHA as an approval record. The caller should mirror these checks at job admission
before media work. This design assumes only reviewed, in-scope task commits are
published to the dedicated branch. Branch/actor checks cannot prove that review
occurred, defend against malicious owner-controlled workflow code, or authorize
unrelated future reuse of the branch. The parent retains responsibility for the
bounded task and its publication decisions. Exact EULA approval remains separate.
No remote branch, workflow, settings or approval mechanism is created by this
controller.

The timeout check deliberately accepts only conventional literal YAML: root
`jobs:`, a two-space job ID, and one four-space literal `timeout-minutes`. Ambiguous
keys, YAML merges or expressions are rejected. This is not a general YAML parser.

## Resource, lease and cleanup contract

Preflight requires >=7 GiB total RAM, >=4.5 GiB free RAM, >=32 GiB free temporary
disk and >=2 logical CPUs. Each guest uses 2 GiB static RAM and one vCPU. Automatic
checkpoints are disabled. A fresh host with no preexisting VMs is required; any
unowned VM later blocks continued work and cleanup. Shared hosts are unsupported.

The exact owned switch is private, with no management-OS adapter, host route,
NAT, bridge or external NIC. Each VM has exactly one NIC on that switch. The
controller repeatedly checks switch and VM IDs, notes, configuration paths and
absence of foreign VMs/adapters. Guest scripts also reject routes/forwarding and
unexpected addresses. No RDP/WinRM network listener, guest Internet or public LDAP
endpoint is created.

`RUNNER_TEMP/adtr-real-ad-state.json` is a private non-artifact journal containing
run ID, LabId, source/head pins, image mode, deadline, exact names/paths, allocation
intents and returned IDs. Every resource is reserved before creation. Interrupted
VM creation is recoverable only from its exact random name and configuration path.
Switch recovery requires its exact random name and private type. No prefix-wide
resource lookup or deletion is used.

Normal and independent cleanup remove exact owned VMs, then the switch, then use
`Remove-HostedLabImageMounts` before any recursive file deletion. Mount cleanup
runs even if the directory is absent, so absence cannot erase unresolved native
work. The newest journal is reloaded after helper errors: the controller never
clears `Media.NativeOperationUnresolved`. Unknown disk identity, an ambiguous
DISM operation, unresolved mount or unowned VM makes cleanup fail. All registered
VMs must be gone before deleting the exact owned subtree. Supplied base/ISO inputs
remain untouched; a newly built evaluation base is owned and removed with the run.

Guest calls use bounded PowerShell Direct jobs, absolute lease checks and verified
boot-ID changes. Promotion is repeated only after explicit `FeatureInstalled`
plus a completed reboot. Ambiguous promotion/join is never blindly retried.

The job-local watchdog is **best effort**. It may die with cancellation/runner
cleanup, and a hung native Hyper-V/DISM operation can block its mutex. `finally`
and an `always()` cleanup step can also be interrupted. The independent final
backstop is GitHub-hosted runner VM decommission, not a persistent local reaper.
The literal whole-job timeout must be <= the configured lease and <=90 minutes;
the job clock starts before the lease. Do not claim an exact 90-minute physical
resource-deletion guarantee. Actual timeout/cancellation/decommission behavior
remains a required deployment validation.

## Evidence boundaries

Only public `root-ca.cer`, `root-ca.pem`, `fixtures.json` and `manifest.json` cross
from DC to member via the authenticated host channel. They are kept out of host
artifacts. The independently captured CA pin must match; the manifest uses the
member's actual PEM path. No private key or directory dump is exported.

The test process receives minimal Windows environment plus four `ADTR_*` values,
including only the ephemeral reader password. It runs with a local Administrator
OS token; LDAP binds use the restricted reader. Reader plaintext exists transiently
in guest process environment/managed memory. Clearing references is not a promise
of deterministic .NET string zeroization; VM/host destruction bounds its lifetime.

Go stdout/stderr stay in guest memory. Exact required tests must emit matching
RUN/PASS records and exit zero without stderr or skips; both TLS subtests must run.
The cases establish real join, denied write, LDAPS/StartTLS, fixture identity,
bad/disabled credentials, wrong TLS identity, unrelated CA rejection, cancellation
before/after TLS and after a real page, and revocation after a real page.

Only `RUNNER_TEMP/adtr-real-ad-report.json` is an uploadable report. Its fixed
fields include source/head SHA, LabId, image mode, evaluation-installation boolean,
explicit case booleans, cleanup boolean, final pass, fixed failure stage, evidence
kind and `full_product_acceptance=false`. Never upload the journal, a broad temp
folder, setup/DISM/AD logs, raw diagnostics, transcripts, crash dumps, credentials,
ISOs or VM disks. `passed` requires every case and verified cleanup. This is
transport acceptance, not full product/API/worker, eight-version or G05 coverage.

## Future standalone workflow requirements; not enabled here

1. Keep the experiment in its own explicitly approved workflow/job; do not alter,
   merge or replace shared/main workflows as a shortcut to run it.
2. Review/publish only in-scope task commits to `feat/ad-lab-windows-install`.
   Mirror the first-attempt owner/repository/branch and source-provenance checks
   at job admission, using Expected PR values only as provenance. Use least permissions
   (`contents: read`), immutable action versions, no saved checkout credentials,
   Windows PowerShell 5.1, a literal <=90-minute job limit and installed official Go.
3. Separately obtain/verify authorized official media, show the selected edition's
   embedded notice and its linked full agreement, and obtain the required actual
   license approval. The notice hash alone does not pin or approve the agreement.
   Never populate the acceptance switch merely because a user said to continue.
4. Generate the Administrator SecureString in the invoking process, then call one
   parameter set. Do not use repository secrets, command-line passwords or broad
   artifact paths. Dispose caller-owned credential material in `finally`.
5. Always attempt the independent cleanup command, fail on an unverified cleanup,
   and upload only the exact fixed report file. Avoid actions that overwrite the
   immutable checkout or leave source changes before the controller's checks.
6. Establish actual cancellation/timeout and hosted-runner disposal evidence.
   A cancelled or cleanup-failed run must never become a green AD result.

## Still required before relying on this draft

- Windows PowerShell 5.1 parsing of controller, image and guest scripts
- Mocked partial allocation, source/approval mismatch, timeout and cleanup failures
- Approved real ISO/notice plus separately disclosed full-agreement acceptance;
  DISM, partition, first-boot and purge verification
- Actual promotion, certificate selection/reboots, joined secure channel, denied
  write, all named Go cases and exact-resource cleanup on success/failure
- Independent cleanup idempotence and cancellation/decommission proof

No item in that list is established by passing the portable source checks.
