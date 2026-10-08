#requires -Version 5.1
<# Runtime-only bounded downloader; parse without executing during source validation.
   Fetches the one independently inspected public Microsoft ISO. No login, registration,
   legal acceptance, image mounting, installation or boot. No ISO artifact/cache output. #>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][guid]$DownloadId,
    [Parameter(Mandatory)][string]$DownloadRoot,
    [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{40}$')][string]$SourceSha,
    [switch]$CleanupOnly
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$VerbosePreference = 'SilentlyContinue'; $DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'; $ProgressPreference = 'SilentlyContinue'

# Public URLs and measured pin copied from the reviewed read-only inspection manifest.
$sourceUrl = 'https://go.microsoft.com/fwlink/?linkid=2345730&clcid=0x409&culture=en-us&country=us'
$aliasUrl = 'https://aka.ms/WinServ2025iso-enus'
$inspectedUrl = 'https://software-static.download.prss.microsoft.com/dbazure/998969d5-f34g-4e03-ac9d-1f9786c66749/26100.32230.260111-0550.lt_release_svc_refresh_SERVER_EVAL_x64FRE_en-us.iso'
$expectedBytes = [long]8152356864
$expectedSha256 = '7b052573ba7894c9924e3e87ba732ccd354d18cb75a883efa9b900ea125bfd51'
$maxDownloadBytes = [long]8500000000
$downloadSeconds = 600
$minFreeBytes = [long]25GB
$state = $null
$lock = $null; $locked = $false

function Assert-DownloadNoReparse([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if ((Test-Path -LiteralPath $current) -and
            ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'Download paths and ancestors must never be reparse points.'
        }
        $parent = [IO.Directory]::GetParent($current)
        if ($null -eq $parent) { break }; $current = $parent.FullName
    }
}
function Set-DownloadPrivateDirectory([string]$Path) {
    Assert-DownloadNoReparse $Path
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true,$false)
    foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid),'FullControl','ContainerInherit,ObjectInherit','None','Allow'))
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}
function Write-DownloadState {
    Assert-DownloadNoReparse $statePath
    Assert-DownloadNoReparse $stateTemporaryPath
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($script:state | ConvertTo-Json -Depth 4))
    if ($bytes.Length -gt 16384) { throw 'Download ownership record exceeds its bound.' }
    $stream = [IO.File]::Open($stateTemporaryPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try { $stream.Write($bytes,0,$bytes.Length) } finally { $stream.Dispose() }
    if (Test-Path -LiteralPath $statePath) { [IO.File]::Replace($stateTemporaryPath,$statePath,$null) }
    else { [IO.File]::Move($stateTemporaryPath,$statePath) }
}
function Read-DownloadState {
    Assert-DownloadNoReparse $statePath
    if (-not (Test-Path -LiteralPath $statePath -PathType Leaf)) { return $null }
    if ((Get-Item -LiteralPath $statePath).Length -gt 16384) { throw 'Download ownership record exceeds its bound.' }
    $saved = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($saved.Schema -ne 1 -or $saved.DownloadId -cne $id -or $saved.RunKey -cne $runKey -or
        $saved.SourceSha -cne $SourceSha -or $saved.Root -cne $root -or $saved.IsoPath -cne $isoPath -or
        $saved.PartialPath -cne $partialPath -or $saved.ExpectedBytes -ne $expectedBytes -or
        $saved.ExpectedSha256 -cne $expectedSha256 -or $saved.Stage -notin @('Reserved','Downloading','Verified','Failed','Cleaned') -or
        $saved.ProcessId -lt 1 -or $saved.ProcessStartTicks -notmatch '^\d+$') {
        throw 'Download ownership record does not match this exact caller/run/source.'
    }
    return $saved
}
function Assert-DownloadHostDependency {
    # Read-only dependency check; the downloader never owns or changes the host lab journal.
    $hostStatePath = Join-Path $tempRoot 'adtr-real-ad-state.json'
    Assert-DownloadNoReparse $hostStatePath
    if (-not (Test-Path -LiteralPath $hostStatePath)) { return }
    if (-not (Test-Path -LiteralPath $hostStatePath -PathType Leaf) -or
        (Get-Item -LiteralPath $hostStatePath).Length -gt 131072) { throw 'Host dependency journal is ambiguous.' }
    $hostState = Get-Content -LiteralPath $hostStatePath -Raw | ConvertFrom-Json
    if ($hostState.Schema -ne 1 -or $hostState.RunKey -cne $runKey -or $hostState.SourceSha -cne $SourceSha -or
        $hostState.LabId -notmatch '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$' -or
        $hostState.Root -cne (Join-Path $tempRoot "adtr-real-ad-$($hostState.LabId)")) {
        throw 'Cannot reconcile the download with the exact host run.'
    }
    $mediaProperty = $hostState.PSObject.Properties['Media']
    if ($mediaProperty -and $mediaProperty.Value -and $mediaProperty.Value.IsoPath -ieq $isoPath) {
        $media = $mediaProperty.Value
        if (-not $hostState.CleanupVerified -or $media.NativeOperationUnresolved -or
            $media.IsoMountStarted -or $media.WimMountStarted -or $media.BaseMountStarted) {
            throw 'Host image cleanup is unresolved; preserve the ISO for runner disposal.'
        }
    }
}
function Remove-OwnedDownload {
    $saved = Read-DownloadState
    if (-not $saved) {
        if ((Test-Path -LiteralPath $root) -or (Test-Path -LiteralPath $stateTemporaryPath)) {
            throw 'Refusing download cleanup without an exact completed ownership reservation.'
        }
        return
    }
    if ($saved.Active) {
        $process = Get-Process -Id $saved.ProcessId -ErrorAction SilentlyContinue
        if ($process -and $process.StartTime.ToUniversalTime().Ticks.ToString() -ceq $saved.ProcessStartTicks) {
            throw 'The matching download process is still active; never race its file writes.'
        }
        # The owning process is gone: its HTTP and file handles cannot still write.
    }
    Assert-DownloadHostDependency
    Assert-DownloadNoReparse $root
    if (Test-Path -LiteralPath $root) {
        if (-not $saved.RootCreated) { throw 'An unconfirmed root creation cannot be adopted by cleanup.' }
        foreach ($entry in @(Get-ChildItem -LiteralPath $root -Force)) {
            Assert-DownloadNoReparse $entry.FullName
            if ($entry.PSIsContainer -or $entry.FullName -notin @($partialPath,$isoPath) -or -not $saved.FileCreated) {
                throw 'Unexpected content in the download-owned directory.'
            }
        }
        # No recursion. Mounted ISO images must first be detached by their owner/controller.
        if (Test-Path -LiteralPath $isoPath -PathType Leaf) {
            $disk = @(Get-DiskImage -ImagePath $isoPath -ErrorAction Stop)
            if ($disk.Count -ne 1 -or $disk[0].ImagePath -ine $isoPath -or $disk[0].Attached) {
                throw 'Do not remove an attached or ambiguously identified ISO.'
            }
        }
        foreach ($file in @($partialPath,$isoPath)) {
            if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force -ErrorAction Stop }
            if (Test-Path -LiteralPath $file) { throw 'Owned media file deletion was not verified.' }
        }
        Remove-Item -LiteralPath $root -Force -ErrorAction Stop
        if (Test-Path -LiteralPath $root) { throw 'Owned download root deletion was not verified.' }
    }
    # A leftover write temporary has no authority to replace a verified journal.
    Assert-DownloadNoReparse $stateTemporaryPath
    if (Test-Path -LiteralPath $stateTemporaryPath) { Remove-Item -LiteralPath $stateTemporaryPath -Force -ErrorAction Stop }
    $script:state = $saved; $script:state.Stage = 'Cleaned'; $script:state.Active = $false
    $script:state.CleanupVerified = $true; Write-DownloadState
}
function Assert-DownloadOfficialUri([uri]$Uri) {
    $knownAlias = $Uri.IsAbsoluteUri -and $Uri.AbsoluteUri -ceq $aliasUrl
    $allowedHosts = @('go.microsoft.com','software-static.download.prss.microsoft.com')
    if (-not $Uri.IsAbsoluteUri -or $Uri.Scheme -cne 'https' -or $Uri.Port -ne 443 -or $Uri.UserInfo -or
        $Uri.Fragment -or (($allowedHosts -cnotcontains $Uri.DnsSafeHost) -and -not $knownAlias) -or
        $Uri.AbsoluteUri -cnotin @($sourceUrl,$aliasUrl,$inspectedUrl)) {
        throw 'Unexpected public-media origin or changed inspected URL; no alternate content is allowed.'
    }
}
function Wait-DownloadTask($Task) {
    $cts.Token.ThrowIfCancellationRequested()
    $remainingMs = [int][Math]::Max(0,($downloadSeconds * 1000) - $clock.ElapsedMilliseconds)
    if ($remainingMs -le 0 -or -not $Task.Wait($remainingMs)) {
        $cts.Cancel(); throw 'Public-media download exceeded its deadline.'
    }
    return $Task.GetAwaiter().GetResult()
}
function Get-DownloadResponse($Client,[string]$Method,[uri]$Uri) {
    for ($hop=0; $hop -le 4; $hop++) {
        Assert-DownloadOfficialUri $Uri
        $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method),$Uri)
        try { $response = Wait-DownloadTask ($Client.SendAsync($request,[Net.Http.HttpCompletionOption]::ResponseHeadersRead,$cts.Token)) }
        finally { $request.Dispose() }
        $status = [int]$response.StatusCode
        if ($status -eq 200) { return @{ Response=$response; Uri=$Uri } }
        if ($status -notin @(301,302,303,307,308) -or $null -eq $response.Headers.Location -or $hop -eq 4) {
            $response.Dispose()
            throw 'Public ISO unavailable; do not complete a login, registration, consent, form or challenge.'
        }
        $next = [uri]::new($Uri,$response.Headers.Location)
        $response.Dispose(); Assert-DownloadOfficialUri $next; $Uri = $next
    }
    throw 'Public-media redirect bound exceeded.'
}
function Assert-DownloadIsoHeaders($Result) {
    $response = $Result.Response
    $length = $response.Content.Headers.ContentLength
    $mediaType = $response.Content.Headers.ContentType
    if ($Result.Uri.AbsoluteUri -cne $inspectedUrl -or $null -eq $length -or $length -ne $expectedBytes -or
        $length -gt $maxDownloadBytes -or $null -eq $mediaType -or
        $mediaType.MediaType -notin @('application/octet-stream','application/x-iso9660-image') -or
        @($response.Content.Headers.ContentEncoding).Count -ne 0) {
        throw 'Expected the exact inspected ISO headers, not a form or changed media.'
    }
    return [long]$length
}
function Receive-PinnedDownload {
    Add-Type -AssemblyName System.Net.Http
    $oldProtocol = [Net.ServicePointManager]::SecurityProtocol
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false; $handler.UseCookies = $false
    $handler.UseDefaultCredentials = $false; $handler.Credentials = $null
    $handler.PreAuthenticate = $false; $handler.UseProxy = $false
    $handler.AutomaticDecompression = [Net.DecompressionMethods]::None
    $client = [Net.Http.HttpClient]::new($handler)
    $client.Timeout = [Threading.Timeout]::InfiniteTimeSpan
    $cts = [Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds($downloadSeconds))
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $head = $null; $get = $null; $inputStream = $null; $outputStream = $null; $hasher = $null
    try {
        $script:state.Active = $true; $script:state.Stage = 'Downloading'; Write-DownloadState
        $head = Get-DownloadResponse $client 'HEAD' ([uri]$sourceUrl)
        $length = Assert-DownloadIsoHeaders $head
        $head.Response.Dispose(); $head = $null
        $get = Get-DownloadResponse $client 'GET' ([uri]$inspectedUrl)
        if ((Assert-DownloadIsoHeaders $get) -ne $length) { throw 'Public media changed between HEAD and GET.' }
        $inputStream = Wait-DownloadTask ($get.Response.Content.ReadAsStreamAsync())
        Assert-DownloadNoReparse $partialPath
        if ((Test-Path -LiteralPath $partialPath) -or (Test-Path -LiteralPath $isoPath)) { throw 'Never overwrite or adopt an existing ISO.' }
        $outputStream = [IO.FileStream]::new($partialPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None,1048576,$true)
        $script:state.FileCreated = $true; Write-DownloadState
        $hasher = [Security.Cryptography.SHA256]::Create()
        $buffer = New-Object byte[] 1048576
        $total = [long]0
        while ($true) {
            $count = Wait-DownloadTask ($inputStream.ReadAsync($buffer,0,$buffer.Length,$cts.Token))
            if ($count -eq 0) { break }
            $total += $count
            if ($total -gt $maxDownloadBytes -or $total -gt $length) { throw 'Public ISO exceeded its strict byte bound.' }
            [void](Wait-DownloadTask ($outputStream.WriteAsync($buffer,0,$count,$cts.Token)))
            [void]$hasher.TransformBlock($buffer,0,$count,$buffer,0)
        }
        $cts.Token.ThrowIfCancellationRequested()
        if ($clock.Elapsed.TotalSeconds -ge $downloadSeconds -or $total -ne $expectedBytes) { throw 'Incomplete or overdue public ISO download.' }
        [void]$hasher.TransformFinalBlock([byte[]]@(),0,0)
        $hash = ([BitConverter]::ToString($hasher.Hash)).Replace('-','').ToLowerInvariant()
        if ($hash -cne $expectedSha256) { throw 'Downloaded ISO differs from the independently inspected SHA-256 pin.' }
        [void](Wait-DownloadTask ($outputStream.FlushAsync($cts.Token)))
        $outputStream.Dispose(); $outputStream = $null
        Assert-DownloadNoReparse $isoPath
        [IO.File]::Move($partialPath,$isoPath) # No replacement of an existing destination.
        $script:state.Stage = 'Verified'; Write-DownloadState
    } finally {
        # Cancel and close every writer before marking the download inactive.
        $closed = $true
        try { $cts.Cancel() } catch { $closed = $false }
        foreach ($disposable in @($inputStream,$outputStream,$hasher)) {
            try { if ($null -ne $disposable) { $disposable.Dispose() } } catch { $closed = $false }
        }
        foreach ($response in @($head,$get)) {
            try { if ($null -ne $response) { $response.Response.Dispose() } } catch { $closed = $false }
        }
        foreach ($disposable in @($client,$handler,$cts)) {
            try { $disposable.Dispose() } catch { $closed = $false }
        }
        [Net.ServicePointManager]::SecurityProtocol = $oldProtocol
        if ($closed) { $script:state.Active = $false; Write-DownloadState }
        else { throw 'Download disposal is uncertain; preserve ownership for independent cleanup.' }
    }
}

# Preflight and cleanup authorization are independent of the Hyper-V lab journal.
if ($PSVersionTable.PSEdition -ne 'Desktop' -or -not [Environment]::Is64BitProcess -or
    $env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    $env:RUNNER_OS -ne 'Windows' -or $env:GITHUB_REPOSITORY -ne 'yunpiao/adtr' -or
    $SourceSha -cne $env:ADTR_SOURCE_SHA -or $env:GITHUB_RUN_ID -notmatch '^\d+$' -or
    $env:GITHUB_RUN_ATTEMPT -notmatch '^\d+$' -or $DownloadId -eq [guid]::Empty) {
    throw 'Pinned public media requires the exact authorized hosted Windows repository run.'
}
$sourceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..'))
$head = @(& git -C $sourceRoot rev-parse HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or $head.Count -ne 1 -or $head[0] -cne $SourceSha) { throw 'Pinned download source checkout mismatch.' }
$dirty = @(& git -C $sourceRoot status --porcelain --untracked-files=normal 2>$null)
if ($LASTEXITCODE -ne 0 -or $dirty.Count) { throw 'The download helper must come from a clean immutable source.' }
if (-not $env:RUNNER_TEMP -or -not (Test-Path -LiteralPath $env:RUNNER_TEMP -PathType Container)) { throw 'Hosted temporary storage is unavailable.' }
$tempRoot = [IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\')
Assert-DownloadNoReparse $tempRoot
$id = $DownloadId.ToString('D').ToLowerInvariant()
$root = Join-Path $tempRoot "adtr-eval-download-$id"
if ([IO.Path]::GetFullPath($DownloadRoot).TrimEnd('\') -cne $root -or $root -notmatch '^[A-Z]:\\' -or $root -match '["\r\n]') {
    throw 'DownloadRoot must be the exact fresh GUID-owned directory in RUNNER_TEMP.'
}
$runKey = "$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT"
$statePath = "$root.json"; $stateTemporaryPath = "$statePath.tmp"
$partialPath = Join-Path $root 'server-2025-evaluation.iso.part'
$isoPath = Join-Path $root 'server-2025-evaluation.iso'
foreach ($path in @($root,$statePath,$stateTemporaryPath)) { Assert-DownloadNoReparse $path }
$lock = [Threading.Mutex]::new($false,"Local\ADTR-EvalDownload-$runKey-$id")
try {
    try { $locked = $lock.WaitOne([TimeSpan]::FromSeconds(30)) }
    catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { throw 'An exact download/cleanup operation already owns the lock.' }
    if ($CleanupOnly) {
        Remove-OwnedDownload
        [pscustomobject]@{ DownloadId=$id; DownloadRoot=$root; CleanupVerified=$true }
        return
    }
    foreach ($path in @($root,$statePath,$stateTemporaryPath)) {
        if (Test-Path -LiteralPath $path) { throw 'A new download never overwrites or adopts an existing reservation.' }
    }
    if ([IO.DriveInfo]::new([IO.Path]::GetPathRoot($tempRoot)).AvailableFreeSpace -lt $minFreeBytes) {
        throw 'At least 25 GiB temporary free space is required for the pinned public ISO.'
    }
    $script:state = [pscustomobject]@{ Schema=1; DownloadId=$id; RunKey=$runKey; SourceSha=$SourceSha;
        Root=$root; IsoPath=$isoPath; PartialPath=$partialPath; ExpectedBytes=$expectedBytes; ExpectedSha256=$expectedSha256;
        Stage='Reserved'; RootCreated=$false; FileCreated=$false; Active=$false; CleanupVerified=$false;
        ProcessId=$PID; ProcessStartTicks=(Get-Process -Id $PID).StartTime.ToUniversalTime().Ticks.ToString() }
    Write-DownloadState # Exact ownership reservation precedes directory/file creation.
    try {
        New-Item -ItemType Directory -Path $root -ErrorAction Stop | Out-Null
        $script:state.RootCreated = $true; Write-DownloadState
        Set-DownloadPrivateDirectory $root
        Receive-PinnedDownload
        $saved = Read-DownloadState
        if ($saved.Stage -ne 'Verified' -or $saved.Active -or -not (Test-Path -LiteralPath $isoPath -PathType Leaf) -or
            (Get-Item -LiteralPath $isoPath).Length -ne $expectedBytes) { throw 'Pinned download completion was not verified.' }
        # Only this one success object escapes; neither media bytes nor raw HTTP content is emitted.
        [pscustomobject]@{ IsoPath=$isoPath; IsoSha256=$expectedSha256; Bytes=$expectedBytes; DownloadId=$id; DownloadRoot=$root }
    } catch {
        try {
            $saved = Read-DownloadState
            if ($saved) { $script:state = $saved; $script:state.Stage = 'Failed'; Write-DownloadState }
            Remove-OwnedDownload
        } catch { throw 'Pinned media acquisition failed and owned cleanup remains unverified; dispose this runner.' }
        throw 'Pinned media acquisition failed; its owned temporary files were removed.'
    }
} finally {
    if ($locked) { $lock.ReleaseMutex() }
    $lock.Dispose()
}
