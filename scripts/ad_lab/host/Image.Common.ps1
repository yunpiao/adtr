#requires -Version 5.1
# LOCAL IMPLEMENTATION DRAFT: dot-sourcing defines functions only. No downloaded tools.
# Caller holds Invoke-HostLock for every public operation. Never invoke on a shared host.
Set-StrictMode -Version Latest

function Assert-EvaluationImageContext {
    param([Parameter(Mandatory)][string]$SourceSha, [switch]$ForCleanup)
    Assert-HostedLabHost
    $s = Get-HostState
    if ($SourceSha -cne $env:ADTR_SOURCE_SHA -or $s.SourceSha -cne $SourceSha -or
        -not $s.RootCreateStarted -or -not (Test-Path -LiteralPath $s.Root -PathType Container)) {
        throw 'Evaluation image work requires the existing exact run-owned root and source.'
    }
    Assert-NoReparse $s.Root
    if (-not $ForCleanup) { Assert-HostLease $s -ReserveSeconds 30 }
    return $s
}

function Assert-EvaluationMediaReservation {
    param([Parameter(Mandatory)]$State)
    $p = $State.PSObject.Properties['Media']
    if (-not $p -or -not $p.Value) { throw 'No evaluation image reservation exists.' }
    $m = $p.Value
    $directory = Join-Path $State.Root 'media'
    if ($m.Directory -cne $directory -or $m.BasePath -cne (Join-Path $directory 'base.vhdx') -or
        $m.SourceSha -cne $State.SourceSha -or $m.IsoSha256 -notmatch '^[a-f0-9]{64}$' -or
        $m.LicenseTermsSha256 -notmatch '^[a-f0-9]{64}$' -or
        $m.LicenseRelativePath -notmatch '^Windows\\System32\\(?:en-US\\)?Licenses\\[A-Za-z0-9_-]+\\ServerStandardEval\\license\.rtf$' -or
        $m.WimMountPath -cne (Join-Path $directory 'license-readonly') -or
        $directory -match '["\r\n]' -or
        $m.ImageIndex -lt 1 -or $m.ImageIndex -gt 8 -or
        $m.Stage -notin @('Reserved','Building','Ready','Failed')) {
        throw 'Unexpected evaluation image reservation.'
    }
    Assert-NoReparse $directory
    Assert-NoReparse $m.IsoPath
    if ([IO.Path]::GetFullPath($m.IsoPath) -cne $m.IsoPath -or [IO.Path]::GetExtension($m.IsoPath) -ine '.iso') {
        throw 'Unexpected exact source ISO reservation.'
    }
    return $m
}

function Invoke-EvaluationNative {
    param([Parameter(Mandatory)][ValidateSet('dism.exe','bcdboot.exe')][string]$Name,
        [Parameter(Mandatory)][string]$Arguments,
        [Parameter(Mandatory)][ValidateRange(1,1200)][int]$TimeoutSeconds,
        [switch]$ReturnMetadata, [switch]$ForCleanup)
    $s = Get-HostState; $media = Assert-EvaluationMediaReservation $s
    if (-not $ForCleanup) { Assert-HostLease $s -ReserveSeconds ($TimeoutSeconds + 30) }
    $exe = Join-Path $env:SystemRoot "System32\$Name"
    Assert-NoReparse $exe
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf) -or
        [Diagnostics.FileVersionInfo]::GetVersionInfo($exe).FileBuildPart -lt 26100) {
        throw 'Built-in deployment tools must support Windows Server 2025.'
    }
    # Arguments contain only owned paths and nonsecret selectors, never an answer file.
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $exe; $start.Arguments = $Arguments
    $start.UseShellExecute = $false; $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
    $start.EnvironmentVariables.Clear()
    $start.EnvironmentVariables['SystemRoot'] = $env:SystemRoot
    $start.EnvironmentVariables['WINDIR'] = $env:SystemRoot
    $start.EnvironmentVariables['TEMP'] = $media.Directory
    $start.EnvironmentVariables['TMP'] = $media.Directory
    $process = [Diagnostics.Process]::new()
    try {
        $process.StartInfo = $start
        # Reserve ambiguity before launch; only a confirmed successful exit clears it.
        $media.NativeOperationUnresolved = $true; Write-HostJson $s $script:StatePath
        if (-not $process.Start()) { throw 'Native deployment operation did not start.' }
        $stdout = $process.StandardOutput.ReadToEndAsync(); $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
            $process.Kill(); [void]$process.WaitForExit(10000)
            throw 'Native deployment operation exceeded its deadline.'
        }
        $output = $stdout.GetAwaiter().GetResult(); $errors = $stderr.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0 -or $output.Length -gt 1048576 -or $errors.Length -gt 1048576) {
            throw 'Native deployment operation failed.'
        }
        $resolved = Get-HostState
        $resolved.Media.NativeOperationUnresolved = $false; Write-HostJson $resolved $script:StatePath
        if ($ReturnMetadata) { return $output }
    } finally {
        try { if ($process.Id -and -not $process.HasExited) { $process.Kill(); [void]$process.WaitForExit(10000) } } catch { }
        $process.Dispose(); $output = $null; $errors = $null
    }
}

function Assert-EvaluationOwnedVhd {
    param([Parameter(Mandatory)]$State, [Parameter(Mandatory)][ValidateSet('Base','DC','Member')][string]$Role,
        [switch]$RequireMounted, [switch]$ForCleanup)
    $media = Assert-EvaluationMediaReservation $State
    if ($Role -eq 'Base') {
        $path = $media.BasePath; $identity = $media.BaseIdentifier
        if (-not $media.BaseCreateStarted) { throw 'Base creation was not reserved.' }
    } else {
        $machines = @($State.Machines | Where-Object Role -ceq $Role)
        if ($machines.Count -ne 1 -or -not $machines[0].DiskCreateStarted) { throw 'Child disk was not reserved.' }
        $machine = $machines[0]; $path = $machine.DiskPath
        $p = $machine.PSObject.Properties['ImagePreparation']
        if (-not $p -or -not $p.Value) { throw 'Child image preparation was not reserved.' }
        $identity = $p.Value.DiskIdentifier
    }
    Assert-NoReparse $path
    $vhd = Get-VHD -Path $path
    if ($vhd.Path -ine $path -or $vhd.VhdFormat -ne 'VHDX' -or $vhd.Size -ne 40GB -or
        ($identity -and $vhd.DiskIdentifier.ToString() -ine $identity)) { throw 'Exact owned VHD identity mismatch.' }
    if ($Role -eq 'Base') {
        if ($vhd.VhdType -ne 'Dynamic' -or $vhd.ParentPath) { throw 'Base must be a new standalone dynamic VHDX.' }
    } elseif ($vhd.VhdType -ne 'Differencing' -or $vhd.ParentPath -ine $media.BasePath -or $media.Stage -ne 'Ready') {
        throw 'Only the exact run-owned base may parent the child.'
    }
    if ($RequireMounted) {
        if (-not $vhd.Attached) { throw 'The owned virtual disk is not mounted.' }
        # Map path -> VHD -> Storage disk and back. Disk numbers are never caller inputs.
        $disks = @($vhd | Get-Disk)
        if ($disks.Count -ne 1 -or $disks[0].Number -ne $vhd.DiskNumber -or
            $disks[0].Size -ne 40GB -or $disks[0].IsBoot -or $disks[0].IsSystem -or
            [int]$disks[0].BusType -ne 15 -or
            (-not $ForCleanup -and ($disks[0].IsReadOnly -or $disks[0].IsOffline))) {
            throw 'Refusing a system, physical, ambiguous or inaccessible disk.'
        }
        $back = @(Get-VHD -DiskNumber $disks[0].Number)
        if ($back.Count -ne 1 -or $back[0].Path -ine $path -or
            $back[0].DiskIdentifier -ne $vhd.DiskIdentifier) { throw 'Owned disk reverse mapping failed.' }
        return $disks[0]
    }
    return $vhd
}

function Get-EvaluationPartition {
    param([Parameter(Mandatory)]$State, [Parameter(Mandatory)][ValidateSet('Base','DC','Member')][string]$Role,
        [Parameter(Mandatory)][ValidateSet('EFI','Windows')][string]$Kind)
    $disk = Assert-EvaluationOwnedVhd $State $Role -RequireMounted
    if ($disk.PartitionStyle -ne 'GPT') { throw 'Owned image must use GPT.' }
    $parts = @($disk | Get-Partition)
    $type = if ($Kind -eq 'EFI') { '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}' }
        else { '{ebd0a0a2-b9e5-4433-87c0-68b6b72699c7}' }
    $selected = @($parts | Where-Object GptType -eq $type)
    $media = Assert-EvaluationMediaReservation $State
    $guid = if ($Kind -eq 'EFI') { $media.EfiPartitionGuid } else { $media.WindowsPartitionGuid }
    if ($parts.Count -ne 3 -or $selected.Count -ne 1 -or $selected[0].DiskNumber -ne $disk.Number -or
        -not $guid -or $selected[0].Guid -ine $guid -or
        ($Kind -eq 'EFI' -and $selected[0].Size -ne 260MB) -or
        ($Kind -eq 'Windows' -and $selected[0].Size -lt 39GB)) { throw 'Unexpected owned partition layout.' }
    return $selected[0]
}

function Get-EvaluationVolumeRoot {
    param([Parameter(Mandatory)]$State, [Parameter(Mandatory)][ValidateSet('Base','DC','Member')][string]$Role,
        [Parameter(Mandatory)][ValidateSet('EFI','Windows')][string]$Kind)
    $partition = Get-EvaluationPartition $State $Role $Kind
    if (-not $partition.DriveLetter) {
        # Storage chooses an unused drive letter. No hard-coded or caller-supplied drive IDs.
        $partition | Add-PartitionAccessPath -AssignDriveLetter -ErrorAction Stop
        $partition = Get-EvaluationPartition $State $Role $Kind
    }
    $letter = [string]$partition.DriveLetter
    if ($letter -notmatch '^[D-Z]$') { throw 'An owned temporary volume letter was not assigned.' }
    $lookup = @(Get-Partition -DriveLetter $letter)
    if ($lookup.Count -ne 1 -or $lookup[0].DiskNumber -ne $partition.DiskNumber -or
        $lookup[0].PartitionNumber -ne $partition.PartitionNumber -or $lookup[0].Guid -ne $partition.Guid) {
        throw 'Temporary volume letter does not resolve to the exact owned partition.'
    }
    $volume = @($partition | Get-Volume)
    $filesystem = if ($Kind -eq 'EFI') { 'FAT32' } else { 'NTFS' }
    if ($volume.Count -ne 1 -or $volume[0].FileSystem -ne $filesystem) { throw 'Unexpected owned filesystem.' }
    return ($letter + ':\')
}

function Get-EvaluationReadOnlyWimMount {
    param([Parameter(Mandatory)]$State, [switch]$AllowMissing)
    $media = Assert-EvaluationMediaReservation $State
    if (-not $media.WimMountStarted -or $media.SourceWimPath -notmatch '^[D-Z]:\\sources\\install\.wim$') {
        throw 'No exact read-only WIM mount reservation exists.'
    }
    $mounts = @(Get-WindowsImage -Mounted -LogPath (Join-Path $media.Directory 'dism-mount-query.log') -LogLevel Errors |
        Where-Object { $_.Path.TrimEnd('\') -ieq $media.WimMountPath })
    if ($mounts.Count -eq 0 -and $AllowMissing) {
        if (Test-Path -LiteralPath $media.WimMountPath) {
            Assert-NoReparse $media.WimMountPath
            if (@(Get-ChildItem -LiteralPath $media.WimMountPath -Force).Count) {
                throw 'Unresolved image contents remain without a matching DISM mount record.'
            }
        }
        return $null
    }
    if ($mounts.Count -ne 1 -or $mounts[0].ImagePath -ine $media.SourceWimPath -or
        $mounts[0].ImageIndex -ne $media.ImageIndex -or $mounts[0].MountMode.ToString() -ne 'ReadOnly') {
        throw 'The DISM mount is not the exact selected read-only source image.'
    }
    return $mounts[0]
}

function Initialize-EvaluationReparseReader {
    if ('AdtrHostedImage.ReparseReader' -as [type]) { return }
    # Same pure tag/scope gate as Inspect-EvaluationMedia.ps1, without running that script.
    # FindFirstFile returns the reparse tag in dwReserved0; it does not read file data.
    # https://learn.microsoft.com/en-us/windows/win32/fileio/reparse-point-tags
    # https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/c8e77b37-3909-4fe6-a4ea-2b9d423b1ee4
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace AdtrHostedImage {
    public sealed class ReparseInfo {
        public uint Tag;
        public bool IsDirectory;
    }
    public static class ReparseReader {
        [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
        private struct FindData {
            public uint Attributes;
            public System.Runtime.InteropServices.ComTypes.FILETIME Creation, Access, Write;
            public uint SizeHigh, SizeLow, Reserved0, Reserved1;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst=260)] public string Name;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst=14)] public string AlternateName;
        }
        [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true, ExactSpelling=true)]
        private static extern IntPtr FindFirstFileW(string path, out FindData data);
        [DllImport("kernel32.dll", SetLastError=true)]
        [return: MarshalAs(UnmanagedType.Bool)] private static extern bool FindClose(IntPtr handle);
        public static bool IsKnownWimDataTag(uint tag) {
            return tag == 0x80000008U || tag == 0x80000017U;
        }
        public static bool CanReadWimDataLeaf(uint tag, bool isDirectory, bool isLicenseLeaf, bool ownedReadOnlyWim) {
            return !isDirectory && isLicenseLeaf && ownedReadOnlyWim &&
                (tag & 0x20000000U) == 0 && IsKnownWimDataTag(tag);
        }
        public static ReparseInfo Read(string path) {
            FindData data;
            IntPtr search = FindFirstFileW(path, out data);
            if (search == new IntPtr(-1)) throw new Win32Exception(Marshal.GetLastWin32Error());
            try {
                if ((data.Attributes & 0x400U) == 0) throw new InvalidOperationException("Expected reparse metadata.");
                return new ReparseInfo { Tag=data.Reserved0, IsDirectory=(data.Attributes & 0x10U) != 0 };
            } finally { FindClose(search); }
        }
    }
}
'@
    # Runtime pure-gate checks only; no file read or mount is performed by these checks.
    foreach ($hex in @('80000008','80000017')) {
        $tag = [Convert]::ToUInt32($hex,16)
        if (-not [AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf($tag,$false,$true,$true) -or
            [AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf($tag,$true,$true,$true) -or
            [AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf($tag,$false,$false,$true) -or
            [AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf($tag,$false,$true,$false)) {
            throw 'WIM data-leaf scope gate failed.'
        }
    }
    foreach ($hex in @('A0000003','A000000C','A0000008','A0000017','80000009','80000018','00000000')) {
        if ([AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf([Convert]::ToUInt32($hex,16),$false,$true,$true)) {
            throw 'A name-surrogate or unknown tag must never pass.'
        }
    }
}

function Get-EvaluationLicenseSha256 {
    param([Parameter(Mandatory)]$State)
    $media = Assert-EvaluationMediaReservation $State
    if ($media.NativeOperationUnresolved) { throw 'Source servicing must be resolved before reading license data.' }
    # Verification is local to this read, not a cached promise about a former mount.
    [void](Get-EvaluationReadOnlyWimMount $State)
    $base = [IO.Path]::GetFullPath($media.WimMountPath).TrimEnd('\')
    Assert-NoReparse $base # No exception for the mount root or any host ancestor.
    $license = [IO.Path]::GetFullPath((Join-Path $base $media.LicenseRelativePath))
    if (-not $license.StartsWith($base + '\',[StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($license) -cne 'license.rtf') {
        throw 'License path escaped its exact selected-edition reservation.'
    }
    $current = $base
    $components = @($media.LicenseRelativePath.Split([char[]]@('\'),[StringSplitOptions]::RemoveEmptyEntries))
    # Walk outward from the verified root. Never inspect a child through a rejected parent.
    foreach ($component in $components) {
        $current = Join-Path $current $component
        $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
        $isLicenseLeaf = $current -ieq $license
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            Initialize-EvaluationReparseReader
            $info = [AdtrHostedImage.ReparseReader]::Read($current)
            $ownedReadOnlyWim = $base -ieq $media.WimMountPath.TrimEnd('\')
            if (-not [AdtrHostedImage.ReparseReader]::CanReadWimDataLeaf(
                $info.Tag,$info.IsDirectory,$isLicenseLeaf,$ownedReadOnlyWim)) {
                throw 'Only a documented WIM/WOF data license leaf may be read; every other reparse point is forbidden.'
            }
        }
        if (($isLicenseLeaf -and $item.PSIsContainer) -or (-not $isLicenseLeaf -and -not $item.PSIsContainer)) {
            throw 'Unexpected selected-edition license path type.'
        }
    }
    # The sole content read is after all ancestor and exact-leaf checks; no caller path is accepted.
    $leaf = Get-Item -LiteralPath $license -Force -ErrorAction Stop
    if ($leaf.Length -lt 1 -or $leaf.Length -gt 2MB) { throw 'Selected image license exceeds the bounded read size.' }
    $hash = (Get-FileHash -LiteralPath $license -Algorithm SHA256).Hash.ToLowerInvariant()
    [void](Get-EvaluationReadOnlyWimMount $State)
    return $hash
}

function Remove-EvaluationReadOnlyWimMount {
    param([Parameter(Mandatory)]$State)
    $media = Assert-EvaluationMediaReservation $State
    if (-not $media.WimMountStarted) { return }
    $mount = Get-EvaluationReadOnlyWimMount $State -AllowMissing
    if ($mount) {
        Invoke-EvaluationNative -Name dism.exe -Arguments ('/English /Unmount-Image /MountDir:"' +
            $media.WimMountPath + '" /Discard /LogLevel:1 /LogPath:"' +
            (Join-Path $media.Directory 'dism-unmount.log') + '"') -TimeoutSeconds 180 -ForCleanup
    }
    if (Get-EvaluationReadOnlyWimMount $State -AllowMissing) { throw 'Read-only source mount remains.' }
    $media.WimMountStarted = $false; Write-HostJson $State $script:StatePath
}

function Remove-HostedLabImageMounts {
    # Called under the shared host lock, including after an expired lease.
    $s = Get-HostState
    if (-not $s.PSObject.Properties['Media']) { return }
    $media = Assert-EvaluationMediaReservation $s
    if ($media.NativeOperationUnresolved) {
        # A killed DISM parent does not prove servicing workers/services are quiescent.
        throw 'Native deployment work is unresolved; retain owned files and require runner disposal.'
    }
    # VM removal must precede this in independent cleanup. Never detach a VM disk.
    foreach ($role in @('DC','Member','Base')) {
        $entry = if ($role -eq 'Base') { $media } else { @($s.Machines | Where-Object Role -ceq $role)[0] }
        $prepared = if ($role -eq 'Base') { $media } else { $entry.PSObject.Properties['ImagePreparation'] }
        if (-not $prepared) { continue }
        $reservation = if ($role -eq 'Base') { $media } else { $prepared.Value }
        $started = if ($role -eq 'Base') { $reservation.BaseMountStarted } else { $reservation.MountStarted }
        $path = if ($role -eq 'Base') { $media.BasePath } else { $entry.DiskPath }
        if (-not $started -or -not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
        foreach ($vm in @(Get-VM)) {
            if (@(Get-VMHardDiskDrive -VM $vm | Where-Object Path -eq $path).Count) {
                throw 'A prepared image is still assigned to a VM.'
            }
        }
        $vhd = Assert-EvaluationOwnedVhd $s $role
        if ($vhd.Attached) {
            [void](Assert-EvaluationOwnedVhd $s $role -RequireMounted -ForCleanup)
            Dismount-VHD -Path $path -ErrorAction Stop
        }
        if ((Get-VHD -Path $path).Attached) { throw 'Owned VHD mount cleanup was not verified.' }
        if ($role -eq 'Base') { $reservation.BaseMountStarted = $false } else { $reservation.MountStarted = $false }
        Write-HostJson $s $script:StatePath
    }
    Remove-EvaluationReadOnlyWimMount $s
    if ($media.IsoMountStarted) {
        $images = @(Get-DiskImage -ImagePath $media.IsoPath -ErrorAction Stop)
        if ($images.Count -ne 1 -or $images[0].ImagePath -ine $media.IsoPath -or $images[0].StorageType -ne 1) {
            throw 'Unexpected reserved ISO mount.'
        }
        if ($images[0].Attached) { Dismount-DiskImage -ImagePath $media.IsoPath -ErrorAction Stop | Out-Null }
        if ((Get-DiskImage -ImagePath $media.IsoPath).Attached) { throw 'Owned ISO mount cleanup was not verified.' }
        $media.IsoMountStarted = $false; Write-HostJson $s $script:StatePath
    }
}

function New-HostedLabEvaluationBase {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$IsoPath,
        [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$IsoSha256,
        [Parameter(Mandatory)][ValidateRange(1,8)][int]$ImageIndex,
        [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{40}$')][string]$SourceSha,
        [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$LicenseTermsSha256,
        [Parameter(Mandatory)][ValidatePattern('^Windows\\System32\\(?:en-US\\)?Licenses\\[A-Za-z0-9_-]+\\ServerStandardEval\\license\.rtf$')][string]$LicenseRelativePath,
        [Parameter(Mandatory)][switch]$LicenseAcceptanceConfirmed)
    # This switch is evidence supplied by the caller, never license approval by the script.
    if (-not $LicenseAcceptanceConfirmed) { throw 'Approval of the exact evaluation license terms is required.' }
    $s = Assert-EvaluationImageContext $SourceSha
    Assert-HostLease $s -ReserveSeconds 1800
    if ($s.PSObject.Properties['Media'] -or @(Get-VM).Count) { throw 'Image preparation needs a new reservation and no VMs.' }
    Assert-NoReparse $IsoPath
    $iso = Get-Item -LiteralPath $IsoPath -ErrorAction Stop
    if ($iso.PSIsContainer -or $iso.FullName -notmatch '^[A-Z]:\\' -or $iso.Extension -ine '.iso' -or $iso.Length -lt 1GB -or $iso.Length -gt 10GB -or
        $iso.FullName -match '["\r\n]' -or
        (Get-FileHash -LiteralPath $iso.FullName -Algorithm SHA256).Hash.ToLowerInvariant() -cne $IsoSha256) {
        throw 'Source must be the existing pinned official evaluation ISO.'
    }
    $existing = @(Get-DiskImage -ImagePath $iso.FullName -ErrorAction Stop)
    if ($existing.Count -ne 1 -or $existing[0].Attached) { throw 'The source ISO must not already be mounted.' }
    $directory = Join-Path $s.Root 'media'
    if (Test-Path -LiteralPath $directory) { throw 'Image preparation directory must be unused.' }
    $media = [pscustomobject]@{ Directory=$directory; BasePath=(Join-Path $directory 'base.vhdx');
        SourceSha=$SourceSha; IsoPath=$iso.FullName; IsoSha256=$IsoSha256; ImageIndex=$ImageIndex;
        LicenseTermsSha256=$LicenseTermsSha256; LicenseRelativePath=$LicenseRelativePath;
        WimMountPath=(Join-Path $directory 'license-readonly'); SourceWimPath=''; WimMountStarted=$false;
        LicenseBytesVerified=$false; NativeOperationUnresolved=$false; Stage='Reserved'; IsoMountStarted=$false;
        BaseCreateStarted=$false; BaseMountStarted=$false; BaseIdentifier=''; BaseSha256='';
        EfiPartitionGuid=''; WindowsPartitionGuid='' }
    $s | Add-Member -NotePropertyName Media -NotePropertyValue $media
    Write-HostJson $s $script:StatePath # Reserve every path before creation or attachment.
    try {
        Set-HostPrivateDirectory $directory
        Set-HostPrivateDirectory (Join-Path $directory 'scratch')
        $media.Stage = 'Building'; $media.IsoMountStarted = $true; Write-HostJson $s $script:StatePath
        Mount-DiskImage -ImagePath $media.IsoPath -StorageType ISO -Access ReadOnly -ErrorAction Stop | Out-Null
        $mounted = Get-DiskImage -ImagePath $media.IsoPath
        $volumes = @($mounted | Get-Volume)
        if (-not $mounted.Attached -or $mounted.ImagePath -ine $media.IsoPath -or $volumes.Count -ne 1 -or
            $volumes[0].DriveType -ne 'CD-ROM' -or -not $volumes[0].DriveLetter) { throw 'ISO volume mapping is ambiguous.' }
        $wim = Join-Path ($volumes[0].DriveLetter + ':\') 'sources\install.wim'
        if (-not (Test-Path -LiteralPath $wim -PathType Leaf)) { throw 'Pinned source must provide install.wim.' }
        $options = '/English /LogLevel:1 /LogPath:"' + (Join-Path $directory 'dism.log') +
            '" /ScratchDir:"' + (Join-Path $directory 'scratch') + '"'
        $metadata = Invoke-EvaluationNative -Name dism.exe -Arguments ($options +
            ' /Get-WimInfo /WimFile:"' + $wim + '" /Index:' + $ImageIndex) -TimeoutSeconds 120 -ReturnMetadata
        foreach ($field in @(@('Index',[string]$ImageIndex),@('Architecture','x64'),@('Edition','ServerStandardEval'),
            @('Installation','Server Core'),@('ProductType','ServerNT'))) {
            $matches = [regex]::Matches($metadata, '(?m)^\s*' + [regex]::Escape($field[0]) + '\s*:\s*([^\r\n]+)\s*$')
            if ($matches.Count -ne 1 -or $matches[0].Groups[1].Value.Trim() -cne $field[1]) {
                throw 'Selected image is not the exact Standard Evaluation Core x64 server image.'
            }
        }
        $versions = [regex]::Matches($metadata, '(?m)^\s*Version\s*:\s*(10\.0\.26100(?:\.\d+)?)\s*$')
        if ($versions.Count -ne 1 -or $metadata -notmatch '(?m)^\s*en-US \(Default\)\s*$') {
            throw 'Only build 26100 with the English installation language is supported.'
        }
        $metadata = $null
        # Confirm the approved license bytes in the selected read-only WIM BEFORE installation.
        Set-HostPrivateDirectory $media.WimMountPath
        $media.SourceWimPath = $wim; $media.WimMountStarted = $true; Write-HostJson $s $script:StatePath
        Invoke-EvaluationNative -Name dism.exe -Arguments ($options + ' /Mount-Image /ImageFile:"' + $wim +
            '" /Index:' + $ImageIndex + ' /MountDir:"' + $media.WimMountPath + '" /ReadOnly /CheckIntegrity') -TimeoutSeconds 300
        $actualLicenseSha256 = Get-EvaluationLicenseSha256 $s
        if ($actualLicenseSha256 -cne $LicenseTermsSha256) {
            throw 'Selected image license bytes do not match the explicitly approved terms.'
        }
        $media.LicenseBytesVerified = $true; Write-HostJson $s $script:StatePath
        Remove-EvaluationReadOnlyWimMount $s
        if ((Get-FileHash -LiteralPath $media.IsoPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $IsoSha256) {
            throw 'Pinned source changed during read-only inspection.'
        }
        $media.BaseCreateStarted = $true; Write-HostJson $s $script:StatePath
        New-VHD -Path $media.BasePath -Dynamic -SizeBytes 40GB -ErrorAction Stop | Out-Null
        $base = Assert-EvaluationOwnedVhd $s Base
        if ($base.Attached) { throw 'New base unexpectedly attached.' }
        $media.BaseIdentifier = $base.DiskIdentifier.ToString()
        $media.BaseMountStarted = $true; Write-HostJson $s $script:StatePath
        Mount-VHD -Path $media.BasePath -NoDriveLetter -ErrorAction Stop | Out-Null
        $disk = Assert-EvaluationOwnedVhd $s Base -RequireMounted
        if ($disk.PartitionStyle -ne 'RAW' -or @($disk | Get-Partition -ErrorAction SilentlyContinue).Count) {
            throw 'Initialization is permitted only for the new empty owned virtual disk.'
        }
        $disk | Initialize-Disk -PartitionStyle GPT -ErrorAction Stop | Out-Null
        $disk = Assert-EvaluationOwnedVhd $s Base -RequireMounted
        $efi = $disk | New-Partition -Size 260MB -GptType '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}' -ErrorAction Stop
        $media.EfiPartitionGuid = $efi.Guid; Write-HostJson $s $script:StatePath
        $disk = Assert-EvaluationOwnedVhd $s Base -RequireMounted
        $disk | New-Partition -Size 16MB -GptType '{e3c9e316-0b5c-4db8-817d-f92df00215ae}' -ErrorAction Stop | Out-Null
        $disk = Assert-EvaluationOwnedVhd $s Base -RequireMounted
        $windows = $disk | New-Partition -UseMaximumSize -GptType '{ebd0a0a2-b9e5-4433-87c0-68b6b72699c7}' -ErrorAction Stop
        $media.WindowsPartitionGuid = $windows.Guid; Write-HostJson $s $script:StatePath
        Get-EvaluationPartition $s Base EFI | Format-Volume -FileSystem FAT32 -NewFileSystemLabel ADTR-EFI -Confirm:$false -ErrorAction Stop | Out-Null
        Get-EvaluationPartition $s Base Windows | Format-Volume -FileSystem NTFS -NewFileSystemLabel ADTR-Windows -Confirm:$false -ErrorAction Stop | Out-Null
        $windowsRoot = Get-EvaluationVolumeRoot $s Base Windows
        Invoke-EvaluationNative -Name dism.exe -Arguments ($options + ' /Apply-Image /ImageFile:"' + $wim +
            '" /Index:' + $ImageIndex + ' /ApplyDir:' + $windowsRoot + ' /CheckIntegrity /Verify') -TimeoutSeconds 1200
        $windowsRoot = Get-EvaluationVolumeRoot $s Base Windows
        $efiRoot = Get-EvaluationVolumeRoot $s Base EFI
        # /s is mandatory: it selects this exact ESP and prevents host NVRAM registration.
        Invoke-EvaluationNative -Name bcdboot.exe -Arguments ('"' + $windowsRoot + 'Windows" /s ' +
            $efiRoot.TrimEnd('\') + ' /f UEFI /c') -TimeoutSeconds 120
        if (-not (Test-Path -LiteralPath (Join-Path $efiRoot 'EFI\Microsoft\Boot\BCD') -PathType Leaf) -or
            -not (Test-Path -LiteralPath (Join-Path $efiRoot 'EFI\Boot\bootx64.efi') -PathType Leaf)) {
            throw 'Owned UEFI boot files were not produced.'
        }
        foreach ($relative in @('Windows\Panther\unattend.xml','Windows\Panther\Unattend\unattend.xml',
            'Windows\System32\Sysprep\unattend.xml','unattend.xml','autounattend.xml')) {
            if (Test-Path -LiteralPath (Join-Path $windowsRoot $relative)) { throw 'Official base contains an unexpected answer file.' }
        }
        # No password or answer file has been written to this never-booted base.
        Remove-HostedLabImageMounts
        $s = Get-HostState; $media = Assert-EvaluationMediaReservation $s
        $media.BaseSha256 = (Get-FileHash -LiteralPath $media.BasePath -Algorithm SHA256).Hash.ToLowerInvariant()
        $media.Stage = 'Ready'; Write-HostJson $s $script:StatePath
        [pscustomobject]@{ BaseVhdPath=$media.BasePath; BaseVhdSha256=$media.BaseSha256; ImageIndex=$ImageIndex; Build=26100 }
    } catch {
        try { Remove-HostedLabImageMounts } catch { }
        $s = Get-HostState; $s.Media.Stage = 'Failed'; Write-HostJson $s $script:StatePath
        throw 'Evaluation image preparation failed; destroy this owned run instead of adopting a partial image.'
    }
}

function Set-HostedLabChildUnattend {
    [CmdletBinding()]
    param([Parameter(Mandatory)][ValidateSet('DC','Member')][string]$Role,
        [Parameter(Mandatory)][securestring]$AdministratorPassword,
        [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$LicenseTermsSha256,
        [Parameter(Mandatory)][switch]$LicenseAcceptanceConfirmed)
    if (-not $LicenseAcceptanceConfirmed -or $AdministratorPassword.Length -lt 20 -or $AdministratorPassword.Length -gt 127) {
        throw 'Exact license approval and an ephemeral 20-127 character administrator password are required.'
    }
    $s = Assert-EvaluationImageContext $env:ADTR_SOURCE_SHA
    $media = Assert-EvaluationMediaReservation $s
    if ($media.Stage -ne 'Ready' -or -not $media.LicenseBytesVerified -or $media.LicenseTermsSha256 -cne $LicenseTermsSha256 -or
        (Get-FileHash -LiteralPath $media.BasePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $media.BaseSha256) {
        throw 'The approved never-booted base must remain unchanged.'
    }
    $machine = @($s.Machines | Where-Object Role -ceq $Role)[0]
    if ($machine.CreateStarted -or $machine.Id -or $machine.PSObject.Properties['ImagePreparation']) {
        throw 'Unattended setup can be injected exactly once before VM creation.'
    }
    $preparation = [pscustomobject]@{ Stage='Reserved'; DiskIdentifier=''; MountStarted=$false; Purged=$false }
    $machine | Add-Member -NotePropertyName ImagePreparation -NotePropertyValue $preparation
    Write-HostJson $s $script:StatePath
    $pointer = [IntPtr]::Zero; $plain = $null; $writer = $null; $stream = $null
    try {
        $vhd = Assert-EvaluationOwnedVhd $s $Role
        if ($vhd.Attached) { throw 'Child was already mounted.' }
        $preparation.DiskIdentifier = $vhd.DiskIdentifier.ToString()
        $preparation.MountStarted = $true; $preparation.Stage = 'Writing'; Write-HostJson $s $script:StatePath
        Mount-VHD -Path $machine.DiskPath -NoDriveLetter -ErrorAction Stop | Out-Null
        $root = Get-EvaluationVolumeRoot $s $Role Windows
        $panther = Join-Path $root 'Windows\Panther'
        Assert-NoReparse $panther
        [void][IO.Directory]::CreateDirectory($panther)
        $answer = Join-Path $panther 'unattend.xml'
        if (Test-Path -LiteralPath $answer) { throw 'Do not replace any pre-existing answer file.' }
        # Create an empty file, restrict ACLs, then write the secret. No host staging copy.
        $stream = [IO.File]::Open($answer,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
        $stream.Dispose(); $stream = $null
        $acl = [Security.AccessControl.FileSecurity]::new(); $acl.SetAccessRuleProtection($true,$false)
        foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
                [Security.Principal.SecurityIdentifier]::new($sid),'FullControl','Allow'))
        }
        Set-Acl -LiteralPath $answer -AclObject $acl
        $stream = [IO.File]::Open($answer,[IO.FileMode]::Open,[IO.FileAccess]::Write,[IO.FileShare]::None)
        $settings = [Xml.XmlWriterSettings]::new(); $settings.Encoding = [Text.UTF8Encoding]::new($false)
        $settings.Indent = $true; $settings.CloseOutput = $false
        $writer = [Xml.XmlWriter]::Create($stream,$settings)
        $ns = 'urn:schemas-microsoft-com:unattend'
        $writer.WriteStartDocument(); $writer.WriteStartElement('unattend',$ns)
        $writer.WriteStartElement('settings',$ns); $writer.WriteAttributeString('pass','oobeSystem')
        foreach ($component in @('Microsoft-Windows-International-Core','Microsoft-Windows-Shell-Setup')) {
            $writer.WriteStartElement('component',$ns)
            foreach ($attribute in @(@('name',$component),@('processorArchitecture','amd64'),
                @('publicKeyToken','31bf3856ad364e35'),@('language','neutral'),@('versionScope','nonSxS'))) {
                $writer.WriteAttributeString($attribute[0],$attribute[1])
            }
            if ($component -eq 'Microsoft-Windows-International-Core') {
                foreach ($locale in @('InputLocale','SystemLocale','UILanguage','UserLocale')) {
                    $writer.WriteElementString($locale,$ns,'en-US')
                }
            } else {
                $writer.WriteStartElement('OOBE',$ns)
                foreach ($setting in @('HideEULAPage','HideLocalAccountScreen','HideOnlineAccountScreens','HideWirelessSetupInOOBE')) {
                    $writer.WriteElementString($setting,$ns,'true')
                }
                $writer.WriteElementString('ProtectYourPC',$ns,'3'); $writer.WriteEndElement()
                $writer.WriteStartElement('UserAccounts',$ns); $writer.WriteStartElement('AdministratorPassword',$ns)
                $pointer = [Runtime.InteropServices.Marshal]::SecureStringToGlobalAllocUnicode($AdministratorPassword)
                $plain = [Runtime.InteropServices.Marshal]::PtrToStringUni($pointer)
                if ($plain -cnotmatch '[A-Z]' -or $plain -cnotmatch '[a-z]' -or $plain -notmatch '[0-9]' -or
                    $plain -notmatch '[^A-Za-z0-9]' -or $plain -match '[\x00-\x1f]') { throw 'Ephemeral password complexity is insufficient.' }
                $writer.WriteElementString('Value',$ns,$plain)
                $plain = $null
                [Runtime.InteropServices.Marshal]::ZeroFreeGlobalAllocUnicode($pointer); $pointer = [IntPtr]::Zero
                $writer.WriteElementString('PlainText',$ns,'true')
                $writer.WriteEndElement(); $writer.WriteEndElement()
            }
            $writer.WriteEndElement()
        }
        $writer.WriteEndElement(); $writer.WriteEndElement(); $writer.WriteEndDocument()
        $writer.Dispose(); $writer = $null; $stream.Dispose(); $stream = $null
        Write-HostJson ([ordered]@{ LabId=$s.LabId; Role=$Role; SourceSha=$s.SourceSha }) (Join-Path $panther 'adtr-setup-owner.json')
        Remove-HostedLabImageMounts
        $s = Get-HostState
        (@($s.Machines | Where-Object Role -ceq $Role)[0]).ImagePreparation.Stage = 'Ready'
        Write-HostJson $s $script:StatePath
    } catch {
        throw 'Per-child unattended setup failed; destroy the child and run rather than reuse it.'
    } finally {
        # An I/O failure while closing XML must not skip native-buffer zeroing or unmounting.
        try { if ($writer) { $writer.Dispose() } } catch { }
        try { if ($stream) { $stream.Dispose() } } catch { }
        try {
            if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeGlobalAllocUnicode($pointer) }
            $plain = $null
        } finally {
            # Removes mounts even after a partial secret write. Failed child is never booted/reused.
            Remove-HostedLabImageMounts
        }
    }
}

function Get-HostedLabSetupCleanupCommand {
    # Run through authenticated PowerShell Direct immediately after initial readiness.
    # Supply only LabId and Role; no password is an argument to this guest command.
    return {
        param([string]$LabId,[string]$Role)
        $ErrorActionPreference = 'Stop'
        if ($LabId -notmatch '^[a-f0-9-]{36}$' -or $Role -notin @('DC','Member')) { throw 'Invalid setup ownership.' }
        $os = Get-CimInstance Win32_OperatingSystem
        $computer = Get-CimInstance Win32_ComputerSystem
        $setup = Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\Setup'
        if ([int]$os.BuildNumber -ne 26100 -or $os.ProductType -eq 1 -or $computer.PartOfDomain -or
            $setup.SystemSetupInProgress -ne 0 -or $setup.OOBEInProgress -ne 0) { throw 'Guest setup is not complete.' }
        function Assert-SetupNoReparse([string]$Path) {
            $current = [IO.Path]::GetFullPath($Path)
            while ($current) {
                if ((Test-Path -LiteralPath $current) -and
                    ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
                    throw 'Unexpected guest setup path.'
                }
                $parent = [IO.Directory]::GetParent($current)
                if (-not $parent) { break }; $current = $parent.FullName
            }
        }
        $markerPath = Join-Path $env:SystemRoot 'Panther\adtr-setup-owner.json'
        Assert-SetupNoReparse $markerPath
        $marker = Get-Content -LiteralPath $markerPath -Raw | ConvertFrom-Json
        if ($marker.LabId -cne $LabId -or $marker.Role -cne $Role) { throw 'Guest setup ownership mismatch.' }
        # Fixed paths only; no recursive deletion or guest logs returned to the host.
        $paths = @('Panther\unattend.xml','Panther\Unattend\unattend.xml','Panther\Unattend\autounattend.xml',
            'Panther\unattend-original.xml','Panther\setupact.log','Panther\setuperr.log',
            'Panther\UnattendGC\setupact.log','Panther\UnattendGC\setuperr.log')
        foreach ($relative in $paths) {
            $path = Join-Path $env:SystemRoot $relative; Assert-SetupNoReparse $path
            if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force -ErrorAction Stop }
            if (Test-Path -LiteralPath $path) { throw 'Setup secret cleanup could not be verified.' }
        }
        Remove-Item -LiteralPath $markerPath -Force -ErrorAction Stop
        [pscustomobject]@{ LabId=$LabId; Role=$Role; Purged=$true }
    }
}
