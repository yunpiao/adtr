#requires -Version 5.1
<# Downloads only openly accessible official evaluation media for license inspection.
   No acceptance, installation, image application, executable launch, or guest boot.
   A measured hash is provenance, NOT an independently verified vendor checksum. #>
[CmdletBinding()]
param([switch]$CleanupOnly)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    $env:GITHUB_REPOSITORY -ne 'yunpiao/adtr') { throw 'Requires the approved GitHub-hosted repository job.' }
if ($env:ADTR_SOURCE_SHA -notmatch '^[a-f0-9]{40}$' -or $env:GITHUB_RUN_ID -notmatch '^\d+$' -or
    $env:GITHUB_RUN_ATTEMPT -notmatch '^\d+$') { throw 'Exact source SHA and run identity are required.' }
$actualHead = & git rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $actualHead -cne $env:ADTR_SOURCE_SHA) { throw 'Checked out source SHA mismatch.' }
$runKey = "$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT"
$tempRoot = [IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\')
$workRoot = Join-Path $tempRoot "adtr-media-work-$runKey"
$evidenceRoot = Join-Path $tempRoot "adtr-media-evidence-$runKey"
$statePath = Join-Path $tempRoot "adtr-media-state-$runKey.json"
$isoPath = Join-Path $workRoot 'server-2025-evaluation.iso'
$mountPath = Join-Path $workRoot 'wim-mount'
$scratchPath = Join-Path $workRoot 'dism-scratch'
$logPath = Join-Path $workRoot 'dism.log'
$manifestPath = Join-Path $evidenceRoot 'manifest.json'
$licenseRoot = Join-Path $evidenceRoot 'licenses'
$sourcePage = 'https://www.microsoft.com/en-us/evalcenter/download-windows-server-2025'
$sourceUrl = 'https://go.microsoft.com/fwlink/?linkid=2345730&clcid=0x409&culture=en-us&country=us'
$allowedHosts = @('go.microsoft.com', 'software-static.download.prss.microsoft.com')
$maxDownloadBytes = [long]8500000000
$downloadSeconds = 600
$minFreeBytes = [long]25GB
$maxLicenseBytes = [long]524288
$maxEvidenceBytes = [long]8388608
$maxLicenseFiles = 16
$state = $null
$report = $null

function Assert-NotReparse([string]$Path) {
    if (Test-Path -LiteralPath $Path) {
        if ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw 'Owned paths must not be reparse points.'
        }
    }
}
function Assert-ParentPaths([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        Assert-NotReparse $current
        $parent = [IO.Directory]::GetParent($current)
        if ($null -eq $parent) { break }
        $current = $parent.FullName
    }
}
Assert-ParentPaths $tempRoot
foreach ($ownedPath in @($workRoot, $evidenceRoot, $statePath, $licenseRoot)) { Assert-NotReparse $ownedPath }
function Save-State {
    Assert-NotReparse $statePath
    Assert-NotReparse "$statePath.tmp"
    $state | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath "$statePath.tmp" -Encoding UTF8
    Move-Item -LiteralPath "$statePath.tmp" -Destination $statePath -Force
}
function Read-OwnedState {
    if (-not (Test-Path -LiteralPath $statePath)) { return $null }
    Assert-NotReparse $statePath
    if ((Get-Item -LiteralPath $statePath).Length -gt 16384) { throw 'Oversized ownership record.' }
    $saved = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($saved.RunKey -cne $runKey -or $saved.SourceSha -cne $env:ADTR_SOURCE_SHA -or
        $saved.Owner -notmatch '^[a-f0-9]{32}$' -or $saved.WorkRoot -cne $workRoot -or
        $saved.IsoPath -cne $isoPath -or $saved.MountPath -cne $mountPath) { throw 'Unexpected ownership record.' }
    if ($saved.WimImagePath -and $saved.WimImagePath -notmatch '^[A-Z]:\\sources\\install\.wim$') {
        throw 'Unexpected owned WIM source.'
    }
    return $saved
}
function Assert-NoReparseTree([string]$Path) {
    # Used only AFTER all owned image mounts have been removed. Never traverse junctions.
    $pending = New-Object 'System.Collections.Generic.Queue[string]'
    $pending.Enqueue($Path)
    while ($pending.Count -gt 0) {
        $directory = $pending.Dequeue()
        Assert-NotReparse $directory
        foreach ($entry in @(Get-ChildItem -LiteralPath $directory -Force)) {
            Assert-NotReparse $entry.FullName
            if ($entry.PSIsContainer) { $pending.Enqueue($entry.FullName) }
        }
    }
}
function Remove-OwnedResources {
    $saved = Read-OwnedState
    if ($null -eq $saved) {
        if (Test-Path -LiteralPath $workRoot) { throw 'Refusing cleanup without ownership record.' }
        return
    }
    if (-not (Test-Path -LiteralPath $workRoot)) { return }
    Assert-NotReparse $workRoot
    Assert-NotReparse $mountPath
    Import-Module Dism
    $ownedMounts = @(Get-WindowsImage -Mounted -LogPath $logPath | Where-Object { $_.Path.TrimEnd('\') -ieq $mountPath })
    if ($ownedMounts.Count -gt 1) { throw 'Ambiguous owned image mount.' }
    foreach ($mounted in $ownedMounts) {
        if (-not $saved.WimMountAttempted -or $mounted.ImagePath -ine $saved.WimImagePath -or
            [int]$mounted.ImageIndex -ne [int]$saved.WimIndex) { throw 'Mounted image ownership mismatch.' }
        Dismount-WindowsImage -Path $mountPath -Discard -LogPath $logPath -ErrorAction Stop | Out-Null
    }
    if (@(Get-WindowsImage -Mounted -LogPath $logPath | Where-Object { $_.Path.TrimEnd('\') -ieq $mountPath }).Count) {
        throw 'Owned WIM mount cleanup did not complete.'
    }
    if ($saved.IsoMountAttempted -and (Test-Path -LiteralPath $isoPath)) {
        Assert-NotReparse $isoPath
        $disk = Get-DiskImage -ImagePath $isoPath -ErrorAction Stop
        if ($disk.Attached) {
            if (-not $saved.IsoMountAttempted -or $disk.ImagePath -ine $isoPath) { throw 'ISO ownership mismatch.' }
            Dismount-DiskImage -ImagePath $isoPath -ErrorAction Stop | Out-Null
        }
        if ((Get-DiskImage -ImagePath $isoPath -ErrorAction Stop).Attached) { throw 'Owned ISO is still mounted.' }
    }
    Assert-NoReparseTree $workRoot
    Remove-Item -LiteralPath $workRoot -Recurse -Force
    if (Test-Path -LiteralPath $workRoot) { throw 'Owned temporary media cleanup failed.' }
}
if ($CleanupOnly) {
    try {
        Remove-OwnedResources
        if (Test-Path -LiteralPath $manifestPath) {
            Assert-NotReparse $manifestPath
            $existing = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
            if ($existing.run_key -cne $runKey -or $existing.source_sha -cne $env:ADTR_SOURCE_SHA) { throw 'Manifest ownership mismatch.' }
            $existing.cleanup_verified = $true
            $existing | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding UTF8
        }
    } catch { throw 'This run owned-media cleanup could not be verified.' }
    return
}
foreach ($reserved in @($workRoot, $evidenceRoot, $statePath, "$statePath.tmp")) {
    if (Test-Path -LiteralPath $reserved) { throw 'Run-owned paths must be unused before inspection.' }
}
$state = [ordered]@{ RunKey=$runKey; SourceSha=$env:ADTR_SOURCE_SHA; Owner=[guid]::NewGuid().ToString('N')
    WorkRoot=$workRoot; IsoPath=$isoPath; MountPath=$mountPath; IsoMountAttempted=$false
    WimImagePath=''; WimIndex=0; WimMountAttempted=$false }
# Reserve the exact paths before any media or mounts can exist.
Save-State
New-Item -ItemType Directory -Path $workRoot, $evidenceRoot, $licenseRoot, $mountPath, $scratchPath | Out-Null
$report = [ordered]@{
    schema=1; run_key=$runKey; source_sha=$env:ADTR_SOURCE_SHA; inspected_utc=[DateTime]::UtcNow.ToString('o')
    source_page=$sourcePage; source_url=$sourceUrl; resolved_url=''; resolved_host=''; redirect_chain=@()
    declared_length_bytes=0; downloaded_bytes=0; sha256=''; sha256_vendor_verified=$false
    maximum_download_bytes=$maxDownloadBytes; download_deadline_seconds=$downloadSeconds; minimum_free_gib=25
    hash_note='Measured SHA-256 only; no independent vendor checksum was available for comparison.'
    image_os=$env:ImageOS; image_version=$env:ImageVersion; temp_free_gib=0; selected_image=$null
    licenses=@(); evidence_bytes=0; license_acceptance_occurred=$false; image_installed=$false; guest_booted=$false
    license_note='Actual candidate files from evaluation media only. Review applicability and terms before accepting, installing, or booting.'
    inspection_complete=$false; cleanup_verified=$false; passed=$false; failure_stage=''; failure_code=''; failure_hresult=''
}

function Assert-OfficialUri([uri]$Uri) {
    if (-not $Uri.IsAbsoluteUri -or $Uri.Scheme -cne 'https' -or $Uri.Port -ne 443 -or $Uri.UserInfo -or
        $Uri.Fragment -or $allowedHosts -cnotcontains $Uri.DnsSafeHost) {
        $report.failure_code = 'UNEXPECTED_DOWNLOAD_ORIGIN'
        throw 'Unexpected download origin; do not bypass login or registration.'
    }
}
function Get-OfficialResponse($Client, [string]$Method, [uri]$Uri, $Token) {
    $chain = @()
    for ($hop = 0; $hop -le 4; $hop++) {
        Assert-OfficialUri $Uri
        $chain += $Uri.AbsoluteUri
        $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($Method), $Uri)
        try { $response = $Client.SendAsync($request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $Token).GetAwaiter().GetResult() }
        finally { $request.Dispose() }
        $status = [int]$response.StatusCode
        if ($status -eq 200) { return @{ Response=$response; Uri=$Uri; Chain=$chain } }
        if ($status -notin @(301,302,303,307,308) -or $null -eq $response.Headers.Location -or $hop -eq 4) {
            $response.Dispose(); $report.failure_code = 'PUBLIC_DOWNLOAD_REQUIRES_REVIEW'
            throw 'Public download unavailable; stop for any form, login, consent, or unexpected response.'
        }
        $next = [uri]::new($Uri, $response.Headers.Location)
        $response.Dispose()
        Assert-OfficialUri $next
        $Uri = $next
    }
    throw 'Redirect bound exceeded.'
}
function Assert-IsoHeaders($Result) {
    $response = $Result.Response
    $length = $response.Content.Headers.ContentLength
    $mediaType = $response.Content.Headers.ContentType
    if ($Result.Uri.DnsSafeHost -cne 'software-static.download.prss.microsoft.com' -or
        $Result.Uri.AbsolutePath -notmatch '(?i)SERVER_EVAL_x64FRE_en-us\.iso$' -or
        $null -eq $length -or $length -lt 1GB -or $length -gt $maxDownloadBytes -or
        $null -eq $mediaType -or $mediaType.MediaType -notin @('application/octet-stream','application/x-iso9660-image')) {
        $report.failure_code = 'EXPECTED_PUBLIC_EVALUATION_ISO_NOT_RETURNED'
        throw 'Expected bounded public English evaluation ISO; refusing a form or changed media type.'
    }
    return [long]$length
}
function Receive-OfficialIso {
    Add-Type -AssemblyName System.Net.Http
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $handler = New-Object System.Net.Http.HttpClientHandler
    $handler.AllowAutoRedirect = $false
    $handler.UseCookies = $false
    $handler.UseDefaultCredentials = $false
    $handler.Credentials = $null
    $handler.AutomaticDecompression = [Net.DecompressionMethods]::None
    $client = [System.Net.Http.HttpClient]::new($handler)
    $client.Timeout = [Threading.Timeout]::InfiniteTimeSpan
    $cts = [Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds($downloadSeconds))
    $head = $null; $get = $null; $inputStream = $null; $outputStream = $null; $hasher = $null
    $clock = [Diagnostics.Stopwatch]::StartNew()
    try {
        $head = Get-OfficialResponse $client 'HEAD' ([uri]$sourceUrl) $cts.Token
        $length = Assert-IsoHeaders $head
        $report.resolved_url = $head.Uri.AbsoluteUri
        $report.resolved_host = $head.Uri.DnsSafeHost
        $report.redirect_chain = $head.Chain
        $report.declared_length_bytes = $length
        $head.Response.Dispose(); $head = $null
        $get = Get-OfficialResponse $client 'GET' ([uri]$report.resolved_url) $cts.Token
        if ((Assert-IsoHeaders $get) -ne $length -or $get.Uri.AbsoluteUri -cne $report.resolved_url) {
            throw 'Media changed between HEAD and GET.'
        }
        $inputStream = $get.Response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
        $outputStream = [IO.File]::Open($isoPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        $hasher = [Security.Cryptography.SHA256]::Create()
        $buffer = New-Object byte[] 1048576
        $total = [long]0
        while ($true) {
            $cts.Token.ThrowIfCancellationRequested()
            # A wall-clock wait also bounds streams whose async cancellation is delayed.
            $remainingMs = [int][Math]::Max(0, ($downloadSeconds * 1000) - $clock.ElapsedMilliseconds)
            if ($remainingMs -le 0) { throw 'Download deadline reached.' }
            $readTask = $inputStream.ReadAsync($buffer, 0, $buffer.Length, $cts.Token)
            if (-not $readTask.Wait($remainingMs)) { throw 'Download deadline reached.' }
            $count = $readTask.GetAwaiter().GetResult()
            if ($count -eq 0) { break }
            $total += $count
            if ($total -gt $maxDownloadBytes -or $total -gt $length) { throw 'ISO exceeds its strict byte bound.' }
            $outputStream.Write($buffer, 0, $count)
            [void]$hasher.TransformBlock($buffer, 0, $count, $buffer, 0)
        }
        $cts.Token.ThrowIfCancellationRequested()
        if ($total -ne $length) { throw 'Incomplete ISO download.' }
        [void]$hasher.TransformFinalBlock([byte[]]@(), 0, 0)
        $report.downloaded_bytes = $total
        $report.sha256 = ([BitConverter]::ToString($hasher.Hash)).Replace('-', '').ToLowerInvariant()
    } finally {
        foreach ($disposable in @($inputStream, $outputStream, $hasher)) { if ($null -ne $disposable) { $disposable.Dispose() } }
        if ($null -ne $head) { $head.Response.Dispose() }
        if ($null -ne $get) { $get.Response.Dispose() }
        $cts.Dispose(); $client.Dispose(); $handler.Dispose()
    }
}
function Find-LicenseFiles([string]$Root) {
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) { return }
    $pending = New-Object 'System.Collections.Generic.Queue[string]'
    $pending.Enqueue($Root)
    $visited = 0; $files = 0
    while ($pending.Count -gt 0) {
        $directory = $pending.Dequeue()
        if (++$visited -gt 512) { throw 'License directory scan limit reached.' }
        Assert-NotReparse $directory
        foreach ($entry in @(Get-ChildItem -LiteralPath $directory -Force)) {
            if (++$files -gt 4096) { throw 'License entry scan limit reached.' }
            if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
            if ($entry.PSIsContainer) { $pending.Enqueue($entry.FullName) }
            elseif ($entry.Name -match '^(?i)(license|eula)[a-z0-9_.-]*\.(rtf|txt)$') { $entry }
        }
    }
}
function Export-License($File, [string]$Origin, [string]$Root) {
    if ($report.licenses.Count -ge $maxLicenseFiles -or $File.Length -le 0 -or $File.Length -gt $maxLicenseBytes) {
        throw 'License count or individual size limit exceeded.'
    }
    Assert-NotReparse $File.FullName
    $relative = $File.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\')
    $index = $report.licenses.Count + 1
    $extension = $File.Extension.ToLowerInvariant()
    $leaf = '{0:D2}-{1}{2}' -f $index, $Origin, $extension
    $target = Join-Path $licenseRoot $leaf
    $bytes = [IO.File]::ReadAllBytes($File.FullName)
    if ($bytes.LongLength -ne $File.Length -or $bytes.LongLength -gt $maxLicenseBytes) { throw 'License length changed.' }
    if ($report.evidence_bytes + $bytes.LongLength -gt $maxEvidenceBytes) { throw 'License evidence byte limit exceeded.' }
    # Preserve the bounded actual source even if conservative native text conversion is refused.
    [IO.File]::WriteAllBytes($target, $bytes)
    $report.evidence_bytes += $bytes.LongLength
    $record = [ordered]@{ origin=$Origin; source_relative_path=$relative; file="licenses/$leaf"
        plain_file=''; bytes=$bytes.LongLength; plain_bytes=0; readable_plain_available=$false
        sha256=(Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() }
    $report.licenses += $record
    if ($extension -eq '.rtf') {
        $rtf = [Text.Encoding]::ASCII.GetString($bytes)
        if (-not $rtf.StartsWith('{\rtf') -or $rtf -match '(?i)\\(object|objdata|objemb|objlink|pict|bin|fldinst)(?![A-Za-z])') {
            $report.failure_code = 'RTF_REQUIRES_SAFE_TEXT_REVIEW'
            throw 'Unsupported object, binary, field instruction, or malformed RTF; preserve source without native conversion.'
        }
        # Built-in RichEdit parses text without showing UI, following links, or loading objects.
        Add-Type -AssemblyName System.Windows.Forms
        $box = New-Object System.Windows.Forms.RichTextBox
        try {
            $box.DetectUrls = $false
            $box.LoadFile($File.FullName, [System.Windows.Forms.RichTextBoxStreamType]::RichText)
            $plain = $box.Text
        }
        finally { $box.Dispose() }
    } else {
        $plain = [IO.File]::ReadAllText($File.FullName)
    }
    if ([string]::IsNullOrWhiteSpace($plain)) { throw 'License produced no readable text.' }
    $plainBytes = [Text.UTF8Encoding]::new($false).GetBytes($plain)
    if ($plainBytes.LongLength -gt $maxLicenseBytes -or
        $report.evidence_bytes + $plainBytes.LongLength -gt $maxEvidenceBytes) { throw 'License evidence byte limit exceeded.' }
    $plainLeaf = '{0:D2}-{1}.plain.txt' -f $index, $Origin
    [IO.File]::WriteAllBytes((Join-Path $licenseRoot $plainLeaf), $plainBytes)
    $report.evidence_bytes += $plainBytes.LongLength
    $record.plain_file = "licenses/$plainLeaf"
    $record.plain_bytes = $plainBytes.LongLength
    $record.readable_plain_available = $true
}
$stage = 'preflight'
try {
    $driveRoot = [IO.Path]::GetPathRoot($tempRoot)
    $free = [IO.DriveInfo]::new($driveRoot).AvailableFreeSpace
    $report.temp_free_gib = [Math]::Round($free / 1GB, 2)
    if ($free -lt $minFreeBytes) { throw 'At least 25 GiB free temporary disk space is required.' }
    Import-Module Dism
    $stage = 'public-iso-download'
    Receive-OfficialIso
    $stage = 'readonly-iso-mount'
    $state.IsoMountAttempted = $true; Save-State
    $disk = Mount-DiskImage -ImagePath $isoPath -StorageType ISO -Access ReadOnly -PassThru
    $volumes = @($disk | Get-Volume | Where-Object { $_.DriveLetter })
    if ($volumes.Count -ne 1 -or $volumes[0].DriveType -ne 'CD-ROM') { throw 'Expected one read-only optical media volume.' }
    $isoRoot = "$($volumes[0].DriveLetter):\"
    $wim = Join-Path $isoRoot 'sources\install.wim'
    if (-not (Test-Path -LiteralPath $wim -PathType Leaf)) { throw 'Official media lacks expected install.wim.' }
    $stage = 'evaluation-image-metadata'
    $images = @(Get-WindowsImage -ImagePath $wim -LogPath $logPath)
    if ($images.Count -eq 0 -or $images.Count -gt 16) { throw 'Unexpected image count; stop rather than scan unbounded metadata.' }
    $evaluationImages = @()
    foreach ($image in @($images | Sort-Object ImageIndex)) {
        if ([int]$image.ImageIndex -lt 1 -or [int]$image.ImageIndex -gt 16) { throw 'Unexpected image index.' }
        $imageDetails = Get-WindowsImage -ImagePath $wim -Index $image.ImageIndex -LogPath $logPath
        if ([int]$imageDetails.ImageIndex -ne [int]$image.ImageIndex) { throw 'Image metadata index mismatch.' }
        if ($imageDetails.EditionId -match '^ServerStandardEval$') { $evaluationImages += $imageDetails }
    }
    if ($evaluationImages.Count -eq 0) { throw 'No Standard Evaluation edition identified from actual image metadata.' }
    $coreImages = @($evaluationImages | Where-Object { $_.InstallationType -match '^(Server Core|ServerCore|Core)$' })
    # Prefer recognized Core metadata; otherwise inspect the first actual Standard Eval index.
    # This selection authorizes text inspection only, never installation or guest boot.
    $details = if ($coreImages.Count) { $coreImages[0] } else { $evaluationImages[0] }
    $report.selected_image = [ordered]@{ index=[int]$details.ImageIndex; name=$details.ImageName
        edition=$details.EditionId; version=$details.Version.ToString(); architecture=$details.Architecture.ToString()
        installation_type=$details.InstallationType; image_size_bytes=[long]$details.ImageSize }
    $imageVersion = [version]$details.Version
    if ($imageVersion.Major -ne 10 -or $imageVersion.Minor -ne 0 -or $imageVersion.Build -ne 26100 -or
        $details.Architecture.ToString() -notin @('9', 'x64', 'amd64')) {
        $report.failure_code = 'UNEXPECTED_EVALUATION_IMAGE_VERSION_OR_ARCHITECTURE'
        throw 'Actual image metadata must identify Windows Server 2025 build 26100, x64.'
    }
    $stage = 'iso-evaluation-license-search'
    $candidates = @(Find-LicenseFiles (Join-Path $isoRoot 'sources\license')) + @(Find-LicenseFiles (Join-Path $isoRoot 'sources\licenses'))
    # Never substitute generic retail/OEM terms when evaluation-specific candidates are absent.
    $evaluationCandidates = @($candidates | Where-Object { $_.FullName -match '(?i)[\\/][^\\/]*eval[^\\/]*[\\/]' } | Sort-Object FullName -Unique)
    foreach ($candidate in $evaluationCandidates) { Export-License $candidate 'iso-eval' $isoRoot }
    if ($report.licenses.Count -eq 0) {
        $stage = 'readonly-evaluation-wim-mount'
        $state.WimImagePath = $wim; $state.WimIndex = [int]$details.ImageIndex
        $state.WimMountAttempted = $true; Save-State
        Mount-WindowsImage -ImagePath $wim -Index $state.WimIndex -Path $mountPath -ReadOnly -ScratchDirectory $scratchPath -LogPath $logPath | Out-Null
        $stage = 'selected-edition-license-search'
        $wimCandidates = @(Find-LicenseFiles (Join-Path $mountPath 'Windows\System32\en-US\Licenses')) +
            @(Find-LicenseFiles (Join-Path $mountPath 'Windows\System32\Licenses'))
        $editionPattern = '(?i)[\\/]' + [regex]::Escape($details.EditionId) + '[\\/]'
        $editionCandidates = @($wimCandidates | Where-Object { $_.FullName -match $editionPattern } | Sort-Object FullName -Unique)
        foreach ($candidate in $editionCandidates) { Export-License $candidate 'wim-eval' $mountPath }
    }
    if ($report.licenses.Count -eq 0) {
        $report.failure_code = 'NO_EVALUATION_LICENSE_FOUND'
        throw 'No actual evaluation license found; do not guess or substitute retail license terms.'
    }
    $report.inspection_complete = $true
} catch {
    $report.failure_stage = $stage
    if (-not $report.failure_code) { $report.failure_code = 'INSPECTION_STOPPED' }
    $report.failure_hresult = '0x{0:X8}' -f $_.Exception.HResult
} finally {
    try { Remove-OwnedResources; $report.cleanup_verified = $true }
    catch { $report.cleanup_verified = $false; $report.failure_stage = 'cleanup'; $report.failure_code = 'OWNED_CLEANUP_NOT_VERIFIED' }
    $report.passed = $report.inspection_complete -and $report.cleanup_verified
    $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding UTF8
    Write-Host ('Inspection complete: {0}; licenses: {1}; cleanup verified: {2}; license acceptance occurred: false; failure stage: {3}; failure code: {4}' -f
        $report.inspection_complete, $report.licenses.Count, $report.cleanup_verified, $report.failure_stage, $report.failure_code)
}
if (-not $report.passed) { throw 'Evaluation media inspection did not pass; see the bounded manifest. No license was accepted.' }
