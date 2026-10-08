# Disposable real Windows AD DS acceptance: partial implementation

Refs #1 and #65 (G05). The selected execution target is a standard GitHub-hosted
Windows runner with isolated Hyper-V guests. This implementation has **not**
installed Windows guests or established a real AD pass. The owner has accepted the disclosed full Windows Server agreement for this
bounded isolated evaluation. Usable Actions runner allocation and completed
runtime validation remain gates. Main-developer baseline/interface coordination:
https://github.com/yunpiao/adtr/issues/1#issuecomment-6060994088

## Delivered independently of provider

- `scripts/ad_lab/guest`: staged Windows Server 2022/2025 guest configuration for
  a new synthetic AD DS forest and joined member. These are real cmdlets, never
  invoked by ordinary CI or the contract workflow.
- `tests/adlab`: explicit `realad` build-tag tests call production ADTR restricted
  LDAP `Probe` and `ReadDirectory`, not an LDAP imitation. Both LDAPS and StartTLS
  must return real fixture values and multiple LDAP pages. Negative cases cover
  bad/disabled synthetic credentials, wrong TLS identity, revoked local authority
  before bind and pre-cancelled operation plus cancellation after the real TLS handshake. This is transport acceptance, not the
  product's HTTP/worker/authorization-ledger acceptance.
- `scripts/ad_lab/contract.py`: strict disposable lease/topology validation and
  whitelist-only boolean reports. It cannot create infrastructure or attest to
  isolation by itself.
- `.github/workflows/ad-lab.yml`: manual non-provisioning syntax/contract/compile
  check. It has no lab credentials, cloud token permission or self-hosted runner.
  Its success must never be reported as real Windows AD acceptance.

## Fixed synthetic guest contract

Use only an isolated, disposable network with no production routes or Internet:
`192.168.77.0/24`, DC `dc01.adtr.test` at `.10`, domain member `member01` at `.20`,
forest `adtr.test` / NetBIOS `ADTR`. No external domain/address input is supported.
The provider adapter must prove ownership and network isolation before executing
any guest script; a private IP or synthetic DNS name alone is not proof.

Root CA must be created inside this lab, self-signed, with CN
`ADTR disposable lab <lowercase UUID>` and validity at most 48 hours. Only public
CA material leaves the DC. Go tests require a SHA-256 root fingerprint pinned by
the trusted host, fixed domain/IP, explicit opt-in, and expected synthetic AD
object GUID/DN/SAM/type/status values exported by the DC.

Inside the joined disposable member, execute the compiled Go test binary with:

- `ADTR_REAL_AD_LAB=disposable-adtr-test-only`
- `ADTR_LAB_MANIFEST`: local JSON path containing `lab_id` (UUID), `ip`, `ca`
  (PEM path), and `fixtures` (`sam`, `guid`, `dn`, `kind`, `disabled`)
- `ADTR_LAB_CA_SHA256`: lowercase hex SHA-256 of the root certificate DER
- `ADTR_LAB_READER_PASSWORD`: generated per-run random synthetic password,
  minimum 20 characters; never a repository, domain-admin or production secret

The guest passwords must be injected over an approved secure host/guest channel,
never command-line text, logs, outputs, artifacts or job matrices. Clear temporary
buffers and destroy the entire isolated lab after execution. Test output uses
fixed failure strings; no raw LDAP diagnostics, credentials or directory values.

## Hosted implementation and verified milestones

`scripts/ad_lab/host` contains the concrete Hyper-V controller, exact ownership
journal, bounded PowerShell Direct operations, evaluation image preparation,
member test execution and cleanup. Its README describes the runtime contract and
limitations. No external cloud provider, persistent runner or repository secret
is needed. Guest Internet routing is deliberately absent; the host downloads only
pinned official inputs. This planned topology has not yet been exercised with a
booted Windows guest, so it is not evidence that no guest data was transmitted.

Verified independently:

- Hosted capability run [37787447038](https://github.com/yunpiao/adtr/actions/runs/37787447038)
  created, started, observed and removed a disconnected diskless VM. This was
  hypervisor capability only. That allocation had 2 CPUs, 7.99 GiB RAM and 73.01
  GiB temporary free disk; later allocations must pass their own capacity checks.
- Official Server 2025 evaluation ISO was downloaded read-only, hash matched
  across hosted and cloud archive inspection, and its selected image identified
  as index 1, ServerStandardEval, Server Core, build 26100.32230.
- The embedded `Windows/System32/en-US/Licenses/Eval/ServerStandardEval/license.rtf`
  is a 6,725-byte notice, SHA-256
  `ad893f939c901f156d68c36f8180b2f2f7ff424ffba892b9cb2ac717b1b1fe58`.
  It directs the user to [Microsoft's license portal](https://aka.ms/useterms).
  Its `Sept2020_DCSTD_EN-US` identifier is recorded as found; no explanation for
  that older identifier has been established. It is not the full agreement.
- The portal's Windows Server 2025 Datacenter and Standard English selection
  provides the [full agreement](https://www.microsoft.com/content/dam/microsoft/usetm/documents/windows-server/2025-datacenter-and-standard/retail/UseTerms_Retail_WindowsServer2025_DatacenterAndStandard_English.pdf).
  An observed notice hash or general permission to continue does not constitute
  acceptance of that agreement. Installation requires separate confirmed acceptance.

The latest focused media run
[37808359232](https://github.com/yunpiao/adtr/actions/runs/37808359232) failed before
any step or runner allocation. Its cause is not yet verified; do not infer quota
or billing from the empty job alone. Do not retry broad CI, change billing or
acquire paid runners to work around this blocker.

## Cleanup and evidence requirements

The host uses only the exact per-run private switch, two owned guests and owned
disks. No production peering, public LDAP/RDP/WinRM, NAT or default-switch attachment
is allowed. Setup credentials are fresh ephemeral values; answer-file copies are
purged after verified OOBE before AD configuration. Only whitelisted, validated
boolean results and source/media provenance may be uploaded. No ISO, VM disks,
private keys, passwords, directory exports or raw test logs are artifacts.

Cleanup runs in the controller's `finally`, a separate Actions `always()` step
and a credential-free job-local expiry watchdog. A killed runner can prevent those
checks from finishing; hosted runner decommission is the final infrastructure
backstop, not an independently observed deletion result. A cleanup failure must
remain a failed/unverified result. Native-operation ambiguity preserves the exact
ownership journal rather than deleting unrelated or still-mounted paths.

## Verification and remaining gates

Portable Python source/contract checks and Linux/Windows Go compilation have
passed. These do not validate Windows PowerShell grammar or cmdlet behavior.
No guest installation, AD promotion, guest reboot, domain join, LDAP acceptance
or complete two-guest cleanup has run. Native PowerShell parsing, installation,
all guest stages, real LDAP tests and independent lifecycle failure cases remain
unverified. The capability and earlier read-only media jobs do not cover them.

This first lab slice exercises the production restricted LDAP transport against
one Server 2025 environment. It does not satisfy the complete application
API/worker/authorization-ledger path, eight-version G05 / 72-scenario matrix,
collector release acceptance or overall product acceptance.

Official references:
- https://docs.github.com/en/actions/reference/security/secure-use
- https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/powershell-direct
- https://learn.microsoft.com/en-us/powershell/module/hyper-v/new-vmswitch
- https://learn.microsoft.com/en-us/powershell/module/addsdeployment/install-addsforest
