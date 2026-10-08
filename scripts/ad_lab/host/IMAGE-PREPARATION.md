# Windows Server 2025 evaluation image preparation

Implementation draft only. Writing or parsing these helpers neither accepts a
license nor establishes that Windows, OOBE, Hyper-V or AD DS works. No Windows
runtime is available in the Linux authoring environment. `test_image_contract.py`
is a portable source-regression check, not a PowerShell parser or runtime test.

`Image.Common.ps1` defines helpers only when loaded. It does not download media,
create a VM, start a VM, configure host networking, or install external tools.
The controller remains responsible for two generation-2 VMs, each with 2 GiB of
fixed RAM, their private switch, source/workflow identity, the run lease, secret
creation, boot, authenticated PowerShell Direct, and complete disposal.

## Authorization and source boundary

The caller must obtain explicit approval of the actual Microsoft evaluation
terms before requesting installation. Approval of a plan, writing these scripts,
or the existence of a Boolean parameter is not acceptance. There is no default
license hash, selected image index, license path, source ISO hash, or approval.
The helper does not decide whether evaluation licensing permits a particular
user or use case and does not bypass activation requirements or expiration.

Before installation it:

1. Requires the existing controller-owned root, exact run/source identity and
   remaining lease, no existing VMs, and a previously unused media reservation.
2. Hash-checks an existing local `.iso` against the caller's independently pinned
   official-media SHA-256, then mounts that exact ISO read-only.
3. Runs one bounded, English DISM metadata query for the explicit image index
   (1–8). Requires `ServerStandardEval`, `Server Core`, `x64`, `ServerNT`, build
   `10.0.26100`, and `en-US` as the default language. Unknown metadata fails.
4. Mounts only that selected WIM index read-only into the exact owned
   `media/license-readonly` directory. Requires a matching DISM mount record,
   exact image path/index and read-only mode. Reads only the explicit license
   path under `Windows\System32\[en-US\]Licenses\<channel>\ServerStandardEval\`.
   Its bytes must match the explicitly approved license SHA-256. No recursive
   search or guessed alternate license is used. A differently arranged source
   image fails closed until its applicability has been reviewed.
   The exact license leaf may carry a documented WIM (`0x80000008`) or WOF
   (`0x80000017`) data-filter reparse tag in a read-only DISM mount even when the
   raw WIM entry is an ordinary archive file. Only that leaf receives the narrow
   data-read exception, after exact mount/path/index/read-only verification.
   The root and each ancestor are checked outward before inspecting descendants;
   reparse directories, junctions, symlinks, name-surrogate tags and unknown tags
   all fail closed. `FindFirstFile` reads the tag metadata without reading file
   contents. No link target is opened or followed. The general host/disk reparse
   guard is unchanged. The mount record is checked again after the bounded hash
   read. The pure tag/scope predicate matches the reviewed media inspector, in a
   separate `AdtrHostedImage` namespace; the inspector itself is never executed
   or dot-sourced by this helper.
5. Discards/unmounts the read-only WIM, verifies the ISO hash again, and only
   then creates and applies the image to a new virtual disk.

The build creates a never-booted 40 GiB dynamic VHDX with GPT, a 260 MiB FAT32 EFI
system partition, a 16 MiB Microsoft-reserved partition, and the remaining NTFS
Windows partition. The official WIM is applied with DISM integrity/verification
flags. No capture, Sysprep execution, answer file, or password is put in the base.
The generalized installation media is specialized independently on first boot
of each child. No existing or previously booted guest image is adopted.

Before initialization, every partition creation/format and boot-file generation,
the helper resolves the exact state-owned VHDX path to the Storage disk and back
to the same VHDX. It rejects physical/system/boot disks, the wrong parent, format,
size or VHD identifier, and unrecognized ownership. No disk numbers, partition
numbers or drive letters are accepted from callers. Storage chooses unused
letters; their exact partition GUID/disk mapping is verified before use.
BCDBoot always receives the owned EFI partition with `/s ... /f UEFI /c`.
The explicit `/s` prevents adding a host firmware/NVRAM boot entry. The host BCD
store is never selected implicitly, and no BCD-editing command is used.

## Inspected notice is not acceptance

The independently inspected source on 2026-10-08 was ISO SHA-256
`7b052573ba7894c9924e3e87ba732ccd354d18cb75a883efa9b900ea125bfd51`,
index 1. Its exact file was
`Windows\System32\en-US\Licenses\Eval\ServerStandardEval\license.rtf`,
6,725 bytes, SHA-256
`ad893f939c901f156d68c36f8180b2f2f7ff424ffba892b9cb2ac717b1b1fe58`.
The raw WIM entry had archive attributes (`0x20`). This records read-only
inspection evidence, not a default input or approval. The RTF is a notice with
identifier `Sept2020_DCSTD_EN-US` directing readers to
[Microsoft use terms](https://aka.ms/useterms); matching that notice's hash does
not establish approval of the separate full terms. Installation remains gated
on the caller obtaining the required actual terms and explicit authorization.
No license was accepted by this code change or inspection.

## Controller interface

Dot-source `Host.Common.ps1` and `Image.Common.ps1`. Call these under the existing
`Invoke-HostLock`; do not take a second independent lock. Root ownership and the
job-local watchdog must already exist.

- `New-HostedLabEvaluationBase -IsoPath <local ISO> -IsoSha256 <64 lowercase hex>
  -ImageIndex <exact index> -SourceSha <40 lowercase hex>
  -LicenseRelativePath <exact applicable path> -LicenseTermsSha256 <64 lowercase hex>
  -LicenseAcceptanceConfirmed`
  returns only `BaseVhdPath`, `BaseVhdSha256`, `ImageIndex`, and `Build=26100`.
- After creating each exactly reserved differencing VHDX, and before `New-VM`,
  call `Set-HostedLabChildUnattend -Role DC|Member
  -AdministratorPassword <SecureString> -LicenseTermsSha256 <same approved hash>
  -LicenseAcceptanceConfirmed`. The unchanged base hash and exact parent are
  checked. This preparation is one-shot; partial/ambiguous children are disposed,
  never repaired, adopted or silently retried.
- After initial authenticated `Wait-GuestReady` and before domain bootstrap,
  obtain `Get-HostedLabSetupCleanupCommand` and invoke that scriptblock through
  the controller's PowerShell Direct channel, with only `LabId` and `Role` as
  arguments. Require the exact `LabId`, `Role` and `Purged=true` result, then
  persist the child's nonsecret `ImagePreparation.Purged=true` marker. Any
  failed setup/purge fails the run and causes disposal.
- `Remove-HostedLabImageMounts` cleans the exact reserved VHD, read-only WIM and
  ISO mounts, including after lease expiration. Integrate it into independent
  cleanup after VM deletion and before recursive owned-root deletion. Failure
  to prove mount ownership/unmounting must stop root deletion and must never
  be reported as verified cleanup. Include `Image.Common.ps1` in parse-only
  script checks and load it in the independent cleanup process.

A native deployment-operation ambiguity flag is persisted before process launch.
Only a confirmed successful native exit clears it. A timeout, interrupted launch,
nonzero result or unverified completion keeps that flag set. Killing DISM alone
does not prove its servicing subprocesses/services have stopped; therefore mount
cleanup refuses to detach/delete or claim success in this ambiguous state. Owned
files remain for complete fresh-runner disposal. This intentionally favors false
failure over unsafe or misleading cleanup evidence.

All creation/attachment intentions are persisted before mutations. `state.Media`
records exact paths, pins, selected index, license verification, identifiers and
mount flags. Each child records an `ImagePreparation` entry. No credential is
serialized into these records. A failed base is marked `Failed` and cannot be
used as a child's parent by these helpers. Cleanup uses the same reservations,
including when creation/mounting was interrupted before a returned identifier.

## Unattended setup and credential handling

The supplied password must be a per-run generated 20–127 character SecureString
with uppercase/lowercase/numeric/special characters. The controller keeps it
only for authenticating to the two disposable guests and disposes it at exit.
The child answer file configures the English locale, Administrator password and
supported OOBE page settings. The license page is hidden only after the approved
license-byte gate. It creates no automatic login, scheduled credential store,
additional account, network listener, route, firewall change, or host setting.
It deliberately does not use deprecated OOBE-skip switches.

Windows unattended setup needs a recoverable password in its answer file. The
helper creates an empty `Windows\Panther\unattend.xml` directly on each private
child's offline volume, restricts its ACL to SYSTEM/Administrators, and then
writes the password with an XML writer. It does not write a host-side password
file, pass a password to DISM or a process command line, or print the XML.
The native plaintext allocation is zeroed/freed in all paths. Temporary managed
strings and framework/OS buffers cannot be guaranteed to be zeroed; memory and
pagefile exposure is therefore possible. SecureString is not a guarantee that
plaintext never exists.

The authenticated post-boot cleanup requires completed setup and exact run/role
ownership, then deletes the fixed answer-file/cache and setup-log allowlist.
Deletion is verified at those paths. This is not a claim of forensic erasure of
VHDX free space, NTFS metadata, paging, OS copies or immutable managed memory.
Never upload or cache the base/child disks, answer files, memory, crash dumps,
setup logs, DISM logs, whole owned directory, or detailed PowerShell errors.
Only the controller's existing public allowlist may leave the run. Full run
and runner disposal is still required on success, failure, cancellation and
timeout; failed secret-bearing children are never booted or reused.

## Validation and authoritative references

Portable source checks: `python3 -m unittest discover -s scripts/ad_lab/host
-p test_image_contract.py -v`. Separately run `Test-HostScriptSyntax.ps1` with
Windows PowerShell 5.1 in a parse-only job. Neither proves actual installation.
Live validation remains pending: mount records and Storage property behavior,
read-only license inspection, DISM apply, UEFI boot, headless OOBE/password
setup, PowerShell Direct access, post-boot purge and failure cleanup.

Official Microsoft documentation used:

- [Reparse tag metadata and name-surrogate semantics](https://learn.microsoft.com/en-us/windows/win32/fileio/reparse-point-tags)
- [Microsoft reparse-tag value definitions](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/c8e77b37-3909-4fe6-a4ea-2b9d423b1ee4)
- [DISM image management, read-only mounts, apply and discard](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism-image-management-command-line-options-s14?view=windows-11)
- [Mount-WindowsImage read-only behavior](https://learn.microsoft.com/en-us/powershell/module/dism/mount-windowsimage?view=windowsserver2025-ps)
- [Get-WindowsImage mounted-image metadata](https://learn.microsoft.com/en-us/powershell/module/dism/get-windowsimage?view=windowsserver2025-ps)
- [Mount-VHD and path-to-Storage disk mapping](https://learn.microsoft.com/en-us/powershell/module/hyper-v/mount-vhd?view=windowsserver2025-ps)
- [Initialize-Disk](https://learn.microsoft.com/en-us/powershell/module/storage/initialize-disk?view=windowsserver2025-ps)
- [New-Partition](https://learn.microsoft.com/en-us/powershell/module/storage/new-partition?view=windowsserver2025-ps)
- [Add-PartitionAccessPath automatic assignment](https://learn.microsoft.com/en-us/powershell/module/storage/add-partitionaccesspath?view=windowsserver2025-ps)
- [BCDBoot explicit system partition and UEFI behavior](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/bcdboot-command-line-options-techref-di?view=windows-11)
- [AdministratorPassword unattended setting](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/microsoft-windows-shell-setup-useraccounts-administratorpassword)
- [Supported OOBE automation settings](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/automate-oobe)
- [Answer-file search order and sensitive-data cleanup](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/windows-setup-automation-overview?view=windows-11)
