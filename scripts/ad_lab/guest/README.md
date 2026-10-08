# Disposable AD DS guest bootstrap

This is a provider-neutral implementation draft, not evidence of a running lab.
It targets fresh Windows Server 2022 (build 20348) or 2025 (26100) guests with
64-bit Windows PowerShell 5.1. Neither baseline has been executed in this
Linux authoring environment. It does not claim support for eight server versions.

The scripts create exactly one `adtr.test` / `ADTR` forest, `dc01.adtr.test`
(`192.168.77.10/24`), and joined `member01.adtr.test` (`192.168.77.20/24`).
Names, domain, network and LDAP destinations are intentionally not configurable.

## Required host boundary

- Create two new disposable guests from appropriately licensed, trusted Windows
  media; do not invoke these scripts on a developer workstation, runner OS, or
  an existing domain machine. The host adapter is a separate deliverable.
- Attach exactly one guest NIC to a private lab-only network. No NAT, forwarding,
  default gateway, public listener, production routes, or second active NIC.
  Guest checks reject default routes, forwarding, global IPv6 and unexpected
  IPv4 addresses, but cannot prove the hypervisor's switching/routing boundary.
- Stage these files through an authenticated guest channel, such as the selected
  provider's guest agent. Network management listeners are not created here.
- Generate a fresh GUID LabId, a strong ephemeral image Administrator password,
  a distinct DSRM password (16+ characters), and reader password (20+ characters).
  All password inputs are typed SecureString. Supply them as in-memory objects,
  never command-line literals, environment dumps, JSON parameters, transcripts,
  or artifacts. The image's Administrator becomes `ADTR\Administrator` after
  promotion; the scripts neither generate nor reset that account's password.
- Preserve host access across hostname/promotion/join reboots. Use fresh sessions
  and the correct post-promotion identity. No autologon, scheduled secret store,
  password file, or self-restarting task is installed.
- Always destroy the two guests, their disks, private network and in-memory
  credentials on success, error, cancellation or timeout. No teardown of a
  production domain is implemented. A CA valid for 36 hours is not a cleanup TTL.

## Stage and reboot contract

Each invocation requires `-LabId <guid> -DisposableLab`. Success returns one
PSCustomObject containing `LabId`, `Role`, `Stage`, `State`, `RebootRequired` and
`Domain`; Ready/Verify include the additional verification/public-output fields.
These are objects, not pre-serialized JSON or special exit codes. The scripts do
not reboot themselves. A terminating exception means failure; the top-level
message is fixed and never forwards a native LDAP/AD exception to CI logs.
Do not upload `$Error`, event logs, AD DS logs, or full PowerShell diagnostics.

1. DC `Initialize-LabDomainController.ps1 -Stage Prepare`: sets the static
   address/DNS and hostname. Reboot if `RebootRequired` is true.
2. DC `-Stage Promote -DSRMPassword <SecureString>`: installs AD DS and a new
   forest with Windows Server 2016 functional levels. If returned State is
   `FeatureInstalled`, reboot and repeat Promote. If `Promoted`, reboot and
   advance. Prechecks are not bypassed; `NoRebootOnCompletion` exists only so
   the host can explicitly complete the mandatory reboot.
3. DC `-Stage Configure -ReaderPassword <SecureString>`: creates fixture users,
   group, explicit reader write-deny ACLs, CA and LDAPS certificate. Reboot.
4. DC `-Stage Ready -ReaderPassword <SecureString>`: waits for AD DS/DNS/ADWS,
   SYSVOL/NETLOGON, exact DNS records; performs normal-trust LDAPS and StartTLS
   reader binds; attempts an actual forbidden write and requires LDAP result
   code 50 plus an administrator read proving the value did not change.
5. Member `Initialize-LabMember.ps1 -Stage Prepare`: sets static address/DNS
   and hostname. Reboot if requested. Only now copy the DC's public `root-ca.cer`
   into the member's `C:\ProgramData\ADTR-Lab\public\root-ca.cer`.
6. Member `-Stage Join -DomainAdminPassword <SecureString> -RootCaSha256 <hex>`:
   imports only the run-pinned public CA, verifies domain ownership over trusted
   LDAPS, joins through the DC FQDN, and requests a reboot. Reboot.
7. Member `-Stage Verify -ReaderPassword <SecureString> -RootCaSha256 <hex>`:
   requires domain membership, exact DNS, a successful `Test-ComputerSecureChannel`,
   both trusted TLS transports, the pinned domain SID and actual member object.
8. DC `-Stage Verify -ReaderPassword <SecureString>`: repeats DC checks and
   exports final real-AD fixture identities including the joined member account.

No later stage is allowed before a recorded reboot has actually occurred.
Same-stage state guards prevent silent promotion/join retries after a partial
failure. An interrupted `PromotionStarted` or `JoinStarted` state requires the
host to destroy and recreate the disposable guest rather than adopt a potentially
ambiguous machine. Prepare cannot reset already configured guests. No existing
domain is accepted without the matching per-machine, per-run ownership marker;
forest validation also rejects extra DCs, domains, trusts or changed domain SID.

## Certificates, fixtures and public outputs

`C:\ProgramData\ADTR-Lab\public\` contains only:

- `root-ca.cer`: public DER root, copied to member for pinned trust import
- `root-ca.pem`: one public PEM root, used by Go integration tests
- `fixtures.json`: array of `{sam,guid,dn,kind,disabled}` read from actual AD
- `manifest.json`: `lab_id`, fixed `ip`, `ca`, `fixtures`, and informational
  `domain`, `dc`, `root_ca_sha256` fields. After copying to the test runner the
  host must replace `ca` with that runner's real PEM path, preserve `lab_id`,
  and pass the independently captured `RootCaSha256` as `ADTR_LAB_CA_SHA256`.

The final fixture set is `lab-reader`, `lab-user`, `lab-disabled`, `lab-group`
and `member01$` (SAM casing is read from AD, not fabricated). The three synthetic
users share the ephemeral reader password; `lab-disabled` remains disabled.
Only normal Domain Users membership is allowed for the reader. Explicit deny
ACLs prevent directory writes, including writes to its own fixture object, and
the lab machine-account creation quota is zero. This is lab-only policy.

The self-signed lab CA has subject `CN=ADTR disposable lab <LabId>` and a 36-hour
lifetime. Its SHA-256 fingerprint is returned by DC Ready/Verify. A separate
24-hour leaf has SAN `dc01.adtr.test`, server-authentication EKU and a nonexportable
Schannel RSA key. Both private keys remain inside the disposable DC certificate
store; no PFX or private key file is exported. Member trust imports require an
exact SHA-256 pin, matching run subject, CA constraints and short validity.
Native LDAP certificate chain/name checks stay enabled for both TLS transports.

Only copy the public output allowlist and fixed structured stage results out of
the guests. The ownership marker and certificate stores are not artifacts.

## Verification and implementation sources

`Test-GuestScriptSyntax.ps1` parses the guest scripts and checks password types
without executing bootstrap code. It may run on a standard Windows Actions
runner; that is a syntax check, not an AD DS lab result. Live tests must still
exercise promotion, certificate selection after reboot, actual domain join,
both TLS transports and the negative-write canary on disposable Windows guests.

Official Microsoft references used for the implementation:

- [Install-ADDSForest and explicit reboot behavior](https://learn.microsoft.com/en-us/powershell/module/addsdeployment/install-addsforest?view=windowsserver2025-ps)
- [AD DS LDAPS certificate requirements, Schannel CSP and restart](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/configure-ldap-signing-certificates)
- [New-SelfSignedCertificate extensions and signer](https://learn.microsoft.com/en-us/powershell/module/pki/new-selfsignedcertificate?view=windowsserver2025-ps)
- [CA certificate basic-constraints example](https://learn.microsoft.com/en-us/dynamics365/fin-ops-core/dev-itpro/perf-test/rsat/certificate-based-authentication)
- [Add-Computer and required DC FQDN](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.management/add-computer?view=powershell-5.1)
- [Test-ComputerSecureChannel is for members, not DCs](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.management/test-computersecurechannel?view=powershell-5.1)
- [StartTransportLayerSecurity](https://learn.microsoft.com/en-us/dotnet/api/system.directoryservices.protocols.ldapsessionoptions.starttransportlayersecurity?view=netframework-4.8.1)
- [New-ADUser SecureString and enabled-state behavior](https://learn.microsoft.com/en-us/powershell/module/activedirectory/new-aduser?view=windowsserver2025-ps)
- [ActiveDirectoryAccessRule](https://learn.microsoft.com/en-us/dotnet/api/system.directoryservices.activedirectoryaccessrule?view=windowsdesktop-9.0)
- [Windows PowerShell encoding and UTF-8 BOM](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_character_encoding?view=powershell-5.1)
