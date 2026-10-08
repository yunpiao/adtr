#requires -Version 5.1
[CmdletBinding(DefaultParameterSetName='SuppliedVhd')]
param(
    [Parameter(Mandatory,ParameterSetName='SuppliedVhd')][string]$BaseVhdPath,
    [Parameter(Mandatory,ParameterSetName='SuppliedVhd')][ValidatePattern('^[a-f0-9]{64}$')][string]$BaseVhdSha256,
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][string]$IsoPath,
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][ValidatePattern('^[a-f0-9]{64}$')][string]$IsoSha256,
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][ValidateRange(1,8)][int]$ImageIndex,
    # Pins embedded notice bytes only; linked full terms require separate user acceptance.
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][ValidatePattern('^[a-f0-9]{64}$')][string]$LicenseTermsSha256,
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][ValidatePattern('^Windows\\System32\\(?:en-US\\)?Licenses\\[A-Za-z0-9_-]+\\ServerStandardEval\\license\.rtf$')][string]$LicenseRelativePath,
    [Parameter(Mandatory,ParameterSetName='EvaluationIso')][switch]$LicenseAcceptanceConfirmed,
    [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{40}$')][string]$SourceSha,
    [Parameter(Mandatory)][securestring]$AdministratorPassword,
    [Parameter(Mandatory)][switch]$TrustedLicensedMedia,
    [Parameter(Mandatory)][switch]$DisposableLab,
    [ValidatePattern('^[a-f0-9]{40}$')][string]$ExpectedPullRequestHeadSha,
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$')][string]$ExpectedPullRequestHeadRef,
    [ValidateRange(1,2147483647)][int]$ExpectedPullRequestNumber,
    [ValidateRange(15,90)][int]$LeaseMinutes = 90
)
$ErrorActionPreference = 'Stop'
$script:FailureStage = 'host-preflight'
$script:StateCreated = $false
$report = $null
$dsrm = $null; $reader = $null; $watchdog = $null
$dcCredential = $null; $memberCredential = $null
$TestBinaryPath = $null; $TestBinarySha256 = $null
$evaluationMode = $PSCmdlet.ParameterSetName -eq 'EvaluationIso'

function Build-OwnedTestBinary {
    param([Parameter(Mandatory)][string]$SourceRoot, [Parameter(Mandatory)][string]$OutputPath)
    $s = Get-HostState; Assert-HostLease $s -ReserveSeconds 660
    $go = (Get-Command go.exe -CommandType Application -ErrorAction Stop).Source
    Assert-NoReparse $go
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $go
    $buildArguments = 'test -c -mod=readonly -trimpath -buildvcs=true -tags=realad -o "' + $OutputPath + '" ./tests/adlab'
    $start.WorkingDirectory = $SourceRoot
    $start.UseShellExecute = $false; $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
    $start.EnvironmentVariables.Clear()
    $start.EnvironmentVariables['SystemRoot'] = $env:SystemRoot
    $start.EnvironmentVariables['WINDIR'] = $env:SystemRoot
    # Build with no inherited Actions tokens, GOFLAGS, workspace overrides or tool downloads.
    $start.EnvironmentVariables['PATH'] = (Split-Path -Parent $go) + ';' + (Split-Path -Parent (Get-Command git.exe -CommandType Application).Source) + ";$env:SystemRoot\System32;$env:SystemRoot"
    foreach ($setting in @(@('GOTOOLCHAIN','local'),@('GOENV','off'),@('GOWORK','off'),@('GOPROXY','off'),
        @('GOSUMDB','off'),@('GOTELEMETRY','off'),@('CGO_ENABLED','0'),@('GOOS','windows'),@('GOARCH','amd64'))) {
        $start.EnvironmentVariables[$setting[0]] = $setting[1]
    }
    foreach ($setting in @(@('GOCACHE','go-cache'),@('GOMODCACHE','go-modules'),@('TEMP','build-temp'),@('TMP','build-temp'))) {
        $path = Join-Path $s.Root $setting[1]
        Set-HostPrivateDirectory $path
        $start.EnvironmentVariables[$setting[0]] = $path
    }
    # Even version detection uses the sanitized environment: never allow an
    # inherited GOTOOLCHAIN/GOENV setting to select or download a different Go.
    $start.Arguments = 'version'
    $versionProcess = [Diagnostics.Process]::new()
    try {
        $versionProcess.StartInfo = $start
        if (-not $versionProcess.Start()) { throw 'Toolchain version check did not start.' }
        $versionOut = $versionProcess.StandardOutput.ReadToEndAsync()
        $versionErr = $versionProcess.StandardError.ReadToEndAsync()
        if (-not $versionProcess.WaitForExit(30000)) {
            $versionProcess.Kill(); [void]$versionProcess.WaitForExit(5000)
            throw 'Toolchain version check timed out.'
        }
        $versionText = $versionOut.GetAwaiter().GetResult().Trim()
        $versionErrors = $versionErr.GetAwaiter().GetResult()
        if ($versionProcess.ExitCode -ne 0 -or $versionText -cne 'go version go1.27.1 windows/amd64' -or
            -not [string]::IsNullOrWhiteSpace($versionErrors)) {
            throw 'The workflow must already provide the official pinned Go 1.27.1 Windows amd64 toolchain.'
        }
    } finally {
        try { if ($versionProcess.Id -and -not $versionProcess.HasExited) { $versionProcess.Kill() } } catch { }
        $versionProcess.Dispose()
    }
    $start.Arguments = $buildArguments
    $process = [Diagnostics.Process]::new()
    try {
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'Build process did not start.' }
        $stdout = $process.StandardOutput.ReadToEndAsync(); $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(600000)) {
            $process.Kill(); [void]$process.WaitForExit(5000)
            throw 'Pinned-source build exceeded ten minutes.'
        }
        # No raw compiler output is an artifact or a controller log.
        $buildOutput = $stdout.GetAwaiter().GetResult(); $buildErrors = $stderr.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $OutputPath -PathType Leaf)) {
            throw 'Pinned-source test build failed.'
        }
        $buildOutput = $null; $buildErrors = $null
    } finally {
        try { if ($process.Id -and -not $process.HasExited) { $process.Kill() } } catch { }
        $process.Dispose()
    }
    $head = @(& git -C $SourceRoot rev-parse HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or $head.Count -ne 1 -or $head[0] -cne $SourceSha) { throw 'Source changed during test build.' }
    $dirty = @(& git -C $SourceRoot status --porcelain --untracked-files=normal 2>$null)
    if ($LASTEXITCODE -ne 0 -or $dirty.Count) { throw 'Source changed during test build.' }
    if ((Get-Item -LiteralPath $OutputPath).Length -lt 1 -or (Get-Item -LiteralPath $OutputPath).Length -gt 64MB) {
        throw 'The compiled test input must be between 1 byte and 64 MiB.'
    }
    Assert-HostLease (Get-HostState)
    return (Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-EphemeralPassword {
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    $bytes = New-Object byte[] 36
    $secure = [securestring]::new()
    try {
        $random.GetBytes($bytes)
        foreach ($character in ('Aa1!' + [Convert]::ToBase64String($bytes)).ToCharArray()) {
            $secure.AppendChar($character)
        }
        $secure.MakeReadOnly()
        return $secure
    } finally { [Array]::Clear($bytes, 0, $bytes.Length); $random.Dispose() }
}

function Invoke-GuestCommand {
    param([Parameter(Mandatory)][ValidateSet('DC','Member')][string]$Role,
        [Parameter(Mandatory)][pscredential]$Credential,
        [Parameter(Mandatory)][scriptblock]$Command,
        [object[]]$Arguments = @(), [ValidateRange(1,1200)][int]$TimeoutSeconds = 600)
    $s = Get-HostState
    Assert-HostLease $s -ReserveSeconds 5
    Assert-PrivateTopology $s
    $machine = @($s.Machines | Where-Object Role -eq $Role)[0]
    $remaining = [int][Math]::Floor(([DateTimeOffset]::Parse($s.ExpiresUtc) - [DateTimeOffset]::UtcNow).TotalSeconds) - 5
    $budget = [Math]::Min($TimeoutSeconds, $remaining)
    if ($budget -lt 1) { throw 'No remaining guest-operation lease.' }
    $job = $null
    try {
        # PowerShell Direct travels through the Hyper-V host channel; no IP listener.
        $job = Invoke-Command -VMId ([guid]$machine.Id) -Credential $Credential -ScriptBlock $Command `
            -ArgumentList $Arguments -AsJob -ErrorAction Stop
        $job | Wait-Job -Timeout $budget | Out-Null
        if ($job.State -ne 'Completed') { throw 'Guest command failed or exceeded its deadline.' }
        $result = @(Receive-Job -Job $job -ErrorAction Stop 3>$null 4>$null 5>$null 6>$null)
        Assert-HostLease (Get-HostState)
        return $result
    } catch {
        throw 'Bounded PowerShell Direct operation did not complete successfully.'
    } finally {
        if ($job) {
            if ($job.State -in @('Running','NotStarted','Blocked')) { Stop-Job -Job $job -ErrorAction SilentlyContinue }
            Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
        }
    }
}

function Wait-GuestReady {
    param([Parameter(Mandatory)][string]$Role, [Parameter(Mandatory)][pscredential]$Credential,
        [string]$PreviousBoot = '')
    $deadline = [DateTimeOffset]::UtcNow.AddMinutes(10)
    do {
        Assert-HostLease (Get-HostState) -ReserveSeconds 10
        try {
            $items = @(Invoke-GuestCommand -Role $Role -Credential $Credential -TimeoutSeconds 25 -Command {
                $ErrorActionPreference = 'Stop'
                $os = Get-CimInstance Win32_OperatingSystem
                $computer = Get-CimInstance Win32_ComputerSystem
                if ($os.ProductType -eq 1 -or [int]$os.BuildNumber -notin @(20348,26100) -or
                    $PSVersionTable.PSEdition -ne 'Desktop' -or -not [Environment]::Is64BitProcess) {
                    throw 'Unsupported disposable Windows guest.'
                }
                [pscustomobject]@{ BootId=$os.LastBootUpTime.ToUniversalTime().ToString('o');
                    PartOfDomain=[bool]$computer.PartOfDomain; Domain=$computer.Domain; Role=[int]$computer.DomainRole }
            })
            if ($items.Count -eq 1 -and $items[0].BootId -and
                (-not $PreviousBoot -or $items[0].BootId -ne $PreviousBoot)) { return $items[0] }
        } catch { # Only readiness is retried. No stage mutation is retried after ambiguous failure.
        }
        Start-Sleep -Seconds 4
    } while ([DateTimeOffset]::UtcNow -lt $deadline)
    throw 'Guest readiness or required boot transition was not established.'
}

function Restart-OwnedGuest {
    param([Parameter(Mandatory)][string]$Role, [Parameter(Mandatory)][pscredential]$BeforeCredential,
        [Parameter(Mandatory)][pscredential]$AfterCredential)
    $old = $null; $selectedCredential = $null
    # Promotion changes the SAM identity around the reboot boundary. Probe only
    # these two explicitly supplied identities, with bounded attempts.
    foreach ($candidate in @($BeforeCredential,$AfterCredential)) {
        try {
            $probe = @(Invoke-GuestCommand -Role $Role -Credential $candidate -TimeoutSeconds 25 -Command {
                [pscustomobject]@{ BootId=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o') }
            })
            if ($probe.Count -eq 1 -and $probe[0].BootId) {
                $old = $probe[0]; $selectedCredential = $candidate; break
            }
        } catch { }
    }
    if (-not $old) { throw 'Pre-reboot guest authentication was not established.' }
    $response = @(Invoke-GuestCommand -Role $Role -Credential $selectedCredential -TimeoutSeconds 30 -Command {
        & "$env:SystemRoot\System32\shutdown.exe" /r /t 3 /f /d p:4:1 >$null
        if ($LASTEXITCODE -ne 0) { throw 'Reboot request failed.' }
        [pscustomobject]@{ Requested=$true }
    })
    if ($response.Count -ne 1 -or $response[0].Requested -ne $true) { throw 'Reboot request was not acknowledged.' }
    Wait-GuestReady -Role $Role -Credential $AfterCredential -PreviousBoot $old.BootId | Out-Null
}

function Copy-GuestBootstrap {
    param([Parameter(Mandatory)][string]$Role, [Parameter(Mandatory)][pscredential]$Credential,
        [Parameter(Mandatory)][string]$GuestSource)
    $s = Get-HostState
    if ($s.ImageMode -eq 'EvaluationIso') {
        $machine = @($s.Machines | Where-Object Role -ceq $Role)[0]
        if ($machine.ImagePreparation.Purged -ne $true) { throw 'Guest setup secrets must be purged before bootstrap transfer.' }
    }
    $bundle = @{}
    foreach ($name in @('Lab.Common.ps1','Initialize-LabDomainController.ps1','Initialize-LabMember.ps1')) {
        $bundle[$name] = [IO.File]::ReadAllBytes((Join-Path $GuestSource $name))
    }
    if ($Role -eq 'Member') {
        $bundle['Invoke-MemberTests.ps1'] = [IO.File]::ReadAllBytes((Join-Path $PSScriptRoot 'Invoke-MemberTests.ps1'))
    }
    $response = @(Invoke-GuestCommand -Role $Role -Credential $Credential -Arguments @($s.LabId,$bundle) -Command {
        param($LabId,$Bundle)
        $ErrorActionPreference = 'Stop'
        $computer = Get-CimInstance Win32_ComputerSystem
        if ($computer.PartOfDomain -or $computer.DomainRole -ge 4 -or
            (Test-Path -LiteralPath 'C:\ProgramData\ADTR-Lab')) { throw 'Media must be a fresh standalone guest.' }
        $path = "C:\ADTR-Stage-$LabId"
        if (Test-Path -LiteralPath $path) { throw 'Stage path must be unused.' }
        [void][IO.Directory]::CreateDirectory($path)
        $allowed = @('Lab.Common.ps1','Initialize-LabDomainController.ps1','Initialize-LabMember.ps1',
            'Invoke-MemberTests.ps1','adlab.test.exe')
        foreach ($name in $Bundle.Keys) {
            if ($name -notin $allowed) { throw 'Unexpected guest input.' }
            [IO.File]::WriteAllBytes((Join-Path $path $name), [byte[]]$Bundle[$name])
        }
        . (Join-Path $path 'Lab.Common.ps1')
        Set-LabPrivateDirectory $path
        [pscustomobject]@{ LabId=$LabId; Copied=$true }
    })
    if ($response.Count -ne 1 -or $response[0].LabId -cne $s.LabId -or $response[0].Copied -ne $true) {
        throw 'Guest inputs were not staged.'
    }
    if ($Role -eq 'Member') {
        # Keep individual remoting objects below normal PowerShell receive limits.
        $stream = [IO.File]::OpenRead($TestBinaryPath)
        $buffer = New-Object byte[] 524288
        $offset = [long]0
        try {
            while (($length = $stream.Read($buffer,0,$buffer.Length)) -gt 0) {
                $chunk = New-Object byte[] $length
                [Array]::Copy($buffer,$chunk,$length)
                $ack = @(Invoke-GuestCommand -Role Member -Credential $Credential -TimeoutSeconds 60 `
                    -Arguments @($s.LabId,$offset,$chunk) -Command {
                        param($LabId,$Offset,$Chunk)
                        $ErrorActionPreference = 'Stop'
                        $path = "C:\ADTR-Stage-$LabId\adlab.test.exe"
                        if ($Offset -eq 0) {
                            $file = [IO.File]::Open($path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                        } else {
                            if ((Get-Item -LiteralPath $path).Length -ne $Offset -or
                                ((Get-Item -LiteralPath $path).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
                                throw 'Binary transfer position mismatch.'
                            }
                            $file = [IO.File]::Open($path,[IO.FileMode]::Append,[IO.FileAccess]::Write,[IO.FileShare]::None)
                        }
                        try { $file.Write([byte[]]$Chunk,0,([byte[]]$Chunk).Length) } finally { $file.Dispose() }
                        [pscustomobject]@{ NextOffset=([long]$Offset + ([byte[]]$Chunk).Length) }
                    })
                $offset += $length
                if ($ack.Count -ne 1 -or $ack[0].NextOffset -ne $offset) { throw 'Binary transfer was not acknowledged.' }
            }
        } finally { $stream.Dispose(); [Array]::Clear($buffer,0,$buffer.Length) }
        $pin = @(Invoke-GuestCommand -Role Member -Credential $Credential -Arguments @($s.LabId) -Command {
            param($LabId)
            [pscustomobject]@{ Sha256=(Get-FileHash -LiteralPath "C:\ADTR-Stage-$LabId\adlab.test.exe" -Algorithm SHA256).Hash.ToLowerInvariant() }
        })
        if ($pin.Count -ne 1 -or $pin[0].Sha256 -cne $TestBinarySha256) { throw 'Transferred binary hash does not match its pin.' }
    }

}

function Invoke-GuestStage {
    param([Parameter(Mandatory)][ValidateSet('DC','Member')][string]$Role,
        [Parameter(Mandatory)][string]$Stage, [Parameter(Mandatory)][pscredential]$Credential,
        [hashtable]$Extra = @{})
    $s = Get-HostState
    $parameters = @{ LabId=[guid]$s.LabId; DisposableLab=$true; Stage=$Stage }
    foreach ($key in $Extra.Keys) { $parameters[$key] = $Extra[$key] }
    $file = if ($Role -eq 'DC') { 'Initialize-LabDomainController.ps1' } else { 'Initialize-LabMember.ps1' }
    $path = "C:\ADTR-Stage-$($s.LabId)\$file"
    $result = @(Invoke-GuestCommand -Role $Role -Credential $Credential -TimeoutSeconds 1200 `
        -Arguments @($path,$parameters) -Command {
            param($Path,$Parameters)
            $ErrorActionPreference = 'Stop'
            & $Path @Parameters 3>$null 4>$null 5>$null 6>$null
        })
    if ($result.Count -ne 1 -or $result[0].LabId -cne $s.LabId -or $result[0].Role -cne $Role -or
        $result[0].Stage -cne $Stage -or $result[0].Domain -cne 'adtr.test' -or
        $result[0].RebootRequired -isnot [bool]) { throw 'Guest stage result contract failed.' }
    $expected = switch ($Stage) {
        Prepare { @('Prepared') }; Promote { @('FeatureInstalled','Promoted') }; Configure { @('Configured') }
        Ready { @('Ready') }; Join { @('Joined') }; Verify { @('Verified') }
        default { throw 'Unexpected stage.' }
    }
    if ($result[0].State -notin $expected) { throw 'Guest stage state was not the required state.' }
    return $result[0]
}

function Copy-PublicLabOutputs {
    param([Parameter(Mandatory)][string[]]$Names, [Parameter(Mandatory)][string]$CaPin)
    $s = Get-HostState
    $allowed = @('root-ca.cer','root-ca.pem','fixtures.json','manifest.json')
    if (@($Names | Where-Object { $_ -notin $allowed }).Count) { throw 'Only the public output allowlist can leave the DC.' }
    $files = @(Invoke-GuestCommand -Role DC -Credential $dcCredential -Arguments @($s.LabId,$Names) -Command {
        param($LabId,$Names)
        $ErrorActionPreference = 'Stop'
        . "C:\ADTR-Stage-$LabId\Lab.Common.ps1"
        $marker = Get-LabMarker -LabId ([guid]$LabId) -Role DC
        if ($marker.State -notin @('Ready','Verified')) { throw 'DC is not ready to export public files.' }
        foreach ($name in $Names) {
            if ($name -notin @('root-ca.cer','root-ca.pem','fixtures.json','manifest.json')) { throw 'Forbidden output.' }
            $path = Join-Path $script:LabPublic $name
            $item = Get-Item -LiteralPath $path
            if ($item.Length -gt 524288 -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe public output.' }
            [pscustomobject]@{ Name=$name; Bytes=[IO.File]::ReadAllBytes($path) }
        }
    })
    if ($files.Count -ne $Names.Count -or @($files | Select-Object -ExpandProperty Name -Unique).Count -ne $Names.Count) {
        throw 'Public output set is incomplete or duplicated.'
    }
    foreach ($file in $files) { if ($file.Name -notin $Names) { throw 'Unexpected public output.' } }
    $response = @(Invoke-GuestCommand -Role Member -Credential $memberCredential -Arguments @($s.LabId,$files,$CaPin) -Command {
        param($LabId,$Files,$CaPin)
        $ErrorActionPreference = 'Stop'
        . "C:\ADTR-Stage-$LabId\Lab.Common.ps1"
        $marker = Get-LabMarker -LabId ([guid]$LabId) -Role Member
        if ($marker.State -notin @('Prepared','Verified')) { throw 'Member is not ready for public outputs.' }
        foreach ($file in $Files) {
            if ($file.Name -notin @('root-ca.cer','root-ca.pem','fixtures.json','manifest.json') -or
                ([byte[]]$file.Bytes).Length -gt 524288) { throw 'Unexpected output input.' }
            [IO.File]::WriteAllBytes((Join-Path $script:LabPublic $file.Name), [byte[]]$file.Bytes)
        }
        $cert = [Security.Cryptography.X509Certificates.X509Certificate2]::new((Join-Path $script:LabPublic 'root-ca.cer'))
        try { if ((Get-LabCertificateSha256 $cert) -cne $CaPin) { throw 'Public CA pin mismatch.' } }
        finally { $cert.Dispose() }
        [pscustomobject]@{ LabId=$LabId; Copied=$true }
    })
    if ($response.Count -ne 1 -or $response[0].LabId -cne $s.LabId -or $response[0].Copied -ne $true) {
        throw 'Pinned public outputs were not copied.'
    }
}

try {
    . (Join-Path $PSScriptRoot 'Host.Common.ps1')
    Assert-HostedLabHost
    if (-not $TrustedLicensedMedia -or -not $DisposableLab -or $AdministratorPassword.Length -lt 20 -or
        $SourceSha -cne $env:ADTR_SOURCE_SHA) { throw 'Explicit media, credential and immutable-source inputs are required.' }
    if ($evaluationMode -and (-not $LicenseAcceptanceConfirmed -or $AdministratorPassword.Length -gt 127)) {
        throw 'Evaluation installation requires prior acceptance of the applicable full agreement and a 20-127 character ephemeral password.'
    }
    $sourceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..'))
    $head = (& git -C $sourceRoot rev-parse HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or $head -cne $SourceSha) { throw 'Checkout does not match the approved source SHA.' }
    $dirty = @(& git -C $sourceRoot status --porcelain --untracked-files=normal 2>$null)
    if ($LASTEXITCODE -ne 0 -or $dirty.Count) { throw 'Lab source must be a clean fixed-SHA checkout.' }
    $provenance = Assert-TaskSourceProvenance -SourceRoot $sourceRoot -SourceSha $SourceSha `
        -ExpectedPullRequestHeadSha $ExpectedPullRequestHeadSha -ExpectedPullRequestHeadRef $ExpectedPullRequestHeadRef `
        -ExpectedPullRequestNumber $ExpectedPullRequestNumber
    Assert-PinnedJobTimeout -SourceRoot $sourceRoot -SourceSha $SourceSha -LeaseMinutes $LeaseMinutes
    if (Test-Path -LiteralPath $script:StatePath) { throw 'Existing ownership state requires independent cleanup and a fresh job.' }
    if ($evaluationMode) {
        # Read-only early checks. The helper rechecks media and the exact embedded
        # notice after ownership allocation, before any image application.
        # Its hash does not substitute for acceptance of the linked full agreement.
        Assert-NoReparse $IsoPath
        if (-not (Test-Path -LiteralPath $IsoPath -PathType Leaf)) { throw 'Pinned local evaluation ISO is missing.' }
        $IsoPath = (Get-Item -LiteralPath $IsoPath).FullName
        if ([IO.Path]::GetExtension($IsoPath) -ine '.iso' -or
            (Get-FileHash -LiteralPath $IsoPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $IsoSha256) {
            throw 'Trusted evaluation ISO hash mismatch.'
        }
    } else {
        Assert-NoReparse $BaseVhdPath
        if (-not (Test-Path -LiteralPath $BaseVhdPath -PathType Leaf)) { throw 'Pinned local media is missing.' }
        $BaseVhdPath = (Get-Item -LiteralPath $BaseVhdPath).FullName
        if ((Get-FileHash -LiteralPath $BaseVhdPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $BaseVhdSha256) {
            throw 'Trusted image hash mismatch.'
        }
        $base = Get-VHD -Path $BaseVhdPath
        if ($base.VhdFormat -ne 'VHDX' -or $base.VhdType -notin @('Dynamic','Fixed') -or $base.ParentPath -or
            $base.Attached -or $base.Size -gt 80GB) { throw 'A detached standalone generation-2 bootable VHDX is required.' }
    }
    if (@(Get-VM).Count -ne 0) { throw 'This controller requires a fresh hosted runner with no existing VMs.' }
    $system = Get-CimInstance Win32_ComputerSystem
    $os = Get-CimInstance Win32_OperatingSystem
    if (-not $system.HypervisorPresent -or [Environment]::ProcessorCount -lt 2 -or
        $system.TotalPhysicalMemory -lt 7GB -or ($os.FreePhysicalMemory * 1KB) -lt 4608MB -or
        (Get-Item -LiteralPath $script:HostTemp).PSDrive.Free -lt 32GB) {
        throw 'The hosted runner lacks the bounded two-guest resource budget.'
    }
    $labId = [guid]::NewGuid().ToString()
    $now = [DateTimeOffset]::UtcNow
    $root = Join-Path $script:HostTemp "adtr-real-ad-$labId"
    $owner = "ADTR:$labId`:$script:RunKey"
    $machines = @(foreach ($item in @(@('DC','dc01'),@('Member','member01'))) {
        $directory = Join-Path $root $item[1]
        [pscustomobject]@{ Role=$item[0]; Name="adtr-$labId-$($item[1])"; Id=''; Directory=$directory;
            VmPath=(Join-Path $directory 'vm'); DiskPath=(Join-Path $directory 'os.vhdx');
            DiskCreateStarted=$false; CreateStarted=$false }
    })
    $s = [pscustomobject]@{ Schema=1; RunKey=$script:RunKey; SourceSha=$SourceSha; SourceHeadSha=$provenance.SourceHeadSha;
        ImageMode=$PSCmdlet.ParameterSetName; LabId=$labId;
        CreatedUtc=$now.ToString('o'); ExpiresUtc=$now.AddMinutes($LeaseMinutes).ToString('o');
        ControllerPid=$PID; ControllerStartTicks=(Get-Process -Id $PID).StartTime.ToUniversalTime().Ticks.ToString();
        Owner=$owner; Root=$root; RootCreateStarted=$false; CleanupVerified=$false;
        BaseVhdSha256=$BaseVhdSha256; TestBinarySha256='';
        Switch=[pscustomobject]@{ Name="adtr-$labId-private"; Id=''; CreateStarted=$false }; Machines=$machines }
    $cases = [ordered]@{ domain_join=$false; readonly_write_denied=$false; ldaps=$false; starttls=$false;
        fixture_values=$false; bad_credential=$false; disabled_account=$false; wrong_tls_identity=$false;
        untrusted_ca=$false; revoked_authority=$false; cancelled=$false }
    $report = [ordered]@{ schema=1; source_sha=$SourceSha; source_head_sha=$provenance.SourceHeadSha; lab_id=$labId;
        image_source_mode=$PSCmdlet.ParameterSetName; evaluation_installation_verified=$false;
        evidence_kind='real-ad-ds-transport';
        cases=$cases; cleanup_verified=$false; passed=$false; full_product_acceptance=$false; failure_stage='' }
    Invoke-HostLock {
        if (Test-Path -LiteralPath $script:StatePath) { throw 'Ownership state was concurrently created.' }
        Write-HostJson $s $script:StatePath
        $script:StateCreated = $true
        if (Test-Path -LiteralPath $root) { throw 'Lab root must be unused.' }
        $s.RootCreateStarted = $true; Write-HostJson $s $script:StatePath
        Set-HostPrivateDirectory $root
    }
    $script:FailureStage = 'lease-watchdog'
    $watchdogScript = Join-Path $PSScriptRoot 'Remove-HostedLab.ps1'
    $watchdog = Start-Process -FilePath "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" `
        -ArgumentList @('-NoLogo','-NoProfile','-NonInteractive','-File',('"' + $watchdogScript + '"'),'-WaitForLease') `
        -WindowStyle Hidden -PassThru
    $readyPath = Join-Path $root 'watchdog-ready.json'
    $watchdogDeadline = [DateTimeOffset]::UtcNow.AddSeconds(30)
    while (-not (Test-Path -LiteralPath $readyPath) -and [DateTimeOffset]::UtcNow -lt $watchdogDeadline -and -not $watchdog.HasExited) {
        Start-Sleep -Milliseconds 250
    }
    if (-not (Test-Path -LiteralPath $readyPath) -or $watchdog.HasExited) { throw 'Job-local lease watchdog did not start.' }
    $ready = Get-Content -LiteralPath $readyPath -Raw | ConvertFrom-Json
    if ($ready.LabId -cne $labId -or $ready.Pid -ne $watchdog.Id -or
        $ready.StartTicks -cne $watchdog.StartTime.ToUniversalTime().Ticks.ToString()) { throw 'Watchdog handshake mismatch.' }

    $script:FailureStage = 'test-build'
    $TestBinaryPath = Join-Path $root 'adlab.test.exe'
    $TestBinarySha256 = Build-OwnedTestBinary -SourceRoot $sourceRoot -OutputPath $TestBinaryPath
    Invoke-HostLock {
        $s = Get-HostState; Assert-HostLease $s
        $s.TestBinarySha256 = $TestBinarySha256
        Write-HostJson $s $script:StatePath
    }

    if ($evaluationMode) {
        $script:FailureStage = 'evaluation-image-prepare'
        $imageResult = @(Invoke-HostLock {
            New-HostedLabEvaluationBase -IsoPath $IsoPath -IsoSha256 $IsoSha256 -ImageIndex $ImageIndex `
                -SourceSha $SourceSha -LicenseTermsSha256 $LicenseTermsSha256 -LicenseRelativePath $LicenseRelativePath `
                -LicenseAcceptanceConfirmed:$LicenseAcceptanceConfirmed
        })
        $s = Get-HostState
        $media = Assert-EvaluationMediaReservation $s
        if ($imageResult.Count -ne 1 -or $media.Stage -cne 'Ready' -or -not $media.LicenseBytesVerified -or
            $media.NativeOperationUnresolved -or $media.BaseMountStarted -or $media.WimMountStarted -or $media.IsoMountStarted -or
            $imageResult[0].BaseVhdPath -cne $media.BasePath -or $imageResult[0].BaseVhdSha256 -cne $media.BaseSha256 -or
            $imageResult[0].ImageIndex -ne $ImageIndex -or $imageResult[0].Build -ne 26100) {
            throw 'Evaluation base preparation did not meet the verified detached-image contract.'
        }
        $BaseVhdPath = $media.BasePath; $BaseVhdSha256 = $media.BaseSha256
        Invoke-HostLock {
            $s = Get-HostState; Assert-HostLease $s
            $s.BaseVhdSha256 = $BaseVhdSha256
            Write-HostJson $s $script:StatePath
        }
    }
    $script:FailureStage = 'resource-create'
    Invoke-HostLock {
        $s = Get-HostState; Assert-HostLease $s -ReserveSeconds 600
        if (@(Get-VM).Count -ne 0) { throw 'Unexpected VM appeared on the dedicated hosted runner.' }
        if ((Get-FileHash -LiteralPath $BaseVhdPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $BaseVhdSha256) {
            throw 'Base image changed before child allocation.'
        }
        if (@(Get-VMSwitch | Where-Object Name -eq $s.Switch.Name).Count) { throw 'Switch reservation collision.' }
        $s.Switch.CreateStarted = $true; Write-HostJson $s $script:StatePath
        $sw = New-VMSwitch -Name $s.Switch.Name -SwitchType Private
        $s.Switch.Id = $sw.Id.ToString(); Write-HostJson $s $script:StatePath
        Set-VMSwitch -VMSwitch $sw -Notes $s.Owner | Out-Null
        # Prepare BOTH children while no VM exists. Helpers update the journal;
        # reload it after every helper rather than overwrite fields with a stale copy.
        foreach ($role in @('DC','Member')) {
            $s = Get-HostState; Assert-HostLease $s -ReserveSeconds 600
            $m = @($s.Machines | Where-Object Role -ceq $role)[0]
            if (@(Get-VM | Where-Object Name -eq $m.Name).Count -or (Test-Path -LiteralPath $m.Directory)) {
                throw 'Machine reservation collision.'
            }
            Set-HostPrivateDirectory $m.Directory
            Set-HostPrivateDirectory $m.VmPath
            $m.DiskCreateStarted = $true; Write-HostJson $s $script:StatePath
            New-VHD -Path $m.DiskPath -ParentPath $BaseVhdPath -Differencing | Out-Null
            if ($evaluationMode) {
                Set-HostedLabChildUnattend -Role $role -AdministratorPassword $AdministratorPassword `
                    -LicenseTermsSha256 $LicenseTermsSha256 -LicenseAcceptanceConfirmed:$LicenseAcceptanceConfirmed
                $s = Get-HostState
                Assert-ImagePreparationReady -State $s -AllowUnpreparedOtherChild
            }
        }
        $s = Get-HostState
        Assert-ImagePreparationReady -State $s
        foreach ($role in @('DC','Member')) {
            $s = Get-HostState; Assert-HostLease $s -ReserveSeconds 600
            $m = @($s.Machines | Where-Object Role -ceq $role)[0]
            $m.CreateStarted = $true; Write-HostJson $s $script:StatePath
            $vm = New-VM -Name $m.Name -Generation 2 -MemoryStartupBytes 2GB -VHDPath $m.DiskPath `
                -Path $m.VmPath -SwitchName $s.Switch.Name
            $m.Id = $vm.Id.ToString(); Write-HostJson $s $script:StatePath
            Set-VM -VM $vm -Notes $s.Owner -AutomaticStartAction Nothing -AutomaticStopAction TurnOff `
                -AutomaticCheckpointsEnabled $false -CheckpointType Disabled | Out-Null
            Set-VMProcessor -VM $vm -Count 1 | Out-Null
            Set-VMMemory -VM $vm -DynamicMemoryEnabled $false -StartupBytes 2GB | Out-Null
            Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate MicrosoftWindows | Out-Null
        }
        $s = Get-HostState
        Assert-ImagePreparationReady -State $s
        Assert-PrivateTopology $s
        foreach ($m in $s.Machines) { Assert-HostLease $s; Start-VM -VM (Get-ReservedVm $m $s) | Out-Null }
    }
    $dsrm = New-EphemeralPassword; $reader = New-EphemeralPassword
    $dcCredential = [pscredential]::new('.\Administrator', $AdministratorPassword)
    $memberCredential = [pscredential]::new('.\Administrator', $AdministratorPassword)
    $guestSource = Join-Path $PSScriptRoot '..\guest'
    $script:FailureStage = 'guest-bootstrap'
    foreach ($role in @('DC','Member')) {
        $credential = if ($role -eq 'DC') { $dcCredential } else { $memberCredential }
        $boot = Wait-GuestReady -Role $role -Credential $credential
        if ($boot.PartOfDomain -or $boot.Role -ge 4) { throw 'Trusted base must boot as an unjoined standalone server.' }
        if ($evaluationMode) {
            $script:FailureStage = 'setup-secret-purge'
            $purge = @(Invoke-GuestCommand -Role $role -Credential $credential -TimeoutSeconds 120 `
                -Arguments @($labId,$role) -Command (Get-HostedLabSetupCleanupCommand))
            if ($purge.Count -ne 1 -or $purge[0].LabId -cne $labId -or $purge[0].Role -cne $role -or
                $purge[0].Purged -isnot [bool] -or -not $purge[0].Purged) {
                throw 'Owned setup secrets were not verifiably purged.'
            }
            Invoke-HostLock {
                $s = Get-HostState; Assert-HostLease $s
                $machine = @($s.Machines | Where-Object Role -ceq $role)[0]
                $machine.ImagePreparation.Purged = $true
                Write-HostJson $s $script:StatePath
            }
        }
        $script:FailureStage = 'guest-bootstrap'
        Copy-GuestBootstrap -Role $role -Credential $credential -GuestSource $guestSource
    }
    if ($evaluationMode) {
        $s = Get-HostState
        if (@($s.Machines | Where-Object { $_.ImagePreparation.Purged -ne $true }).Count) {
            throw 'Both guest setup secret purges must be verified before AD setup.'
        }
        $report.evaluation_installation_verified = $true
    }
    $script:FailureStage = 'dc-prepare'
    $prepared = Invoke-GuestStage -Role DC -Stage Prepare -Credential $dcCredential
    if ($prepared.RebootRequired) { Restart-OwnedGuest -Role DC -BeforeCredential $dcCredential -AfterCredential $dcCredential }
    $script:FailureStage = 'dc-promote'
    $promotion = Invoke-GuestStage -Role DC -Stage Promote -Credential $dcCredential -Extra @{DSRMPassword=$dsrm}
    if ($promotion.State -eq 'FeatureInstalled') {
        if (-not $promotion.RebootRequired) { throw 'Feature installation requires its recorded reboot.' }
        Restart-OwnedGuest -Role DC -BeforeCredential $dcCredential -AfterCredential $dcCredential
        $promotion = Invoke-GuestStage -Role DC -Stage Promote -Credential $dcCredential -Extra @{DSRMPassword=$dsrm}
    }
    if ($promotion.State -ne 'Promoted' -or -not $promotion.RebootRequired) { throw 'Promotion did not reach the required reboot boundary.' }
    $promotedCredential = [pscredential]::new('ADTR\Administrator', $AdministratorPassword)
    Restart-OwnedGuest -Role DC -BeforeCredential $dcCredential -AfterCredential $promotedCredential
    $dcCredential = $promotedCredential
    $script:FailureStage = 'dc-configure'
    $configured = Invoke-GuestStage -Role DC -Stage Configure -Credential $dcCredential -Extra @{ReaderPassword=$reader}
    if (-not $configured.RebootRequired) { throw 'Certificate configuration requires a recorded reboot.' }
    Restart-OwnedGuest -Role DC -BeforeCredential $dcCredential -AfterCredential $dcCredential
    $script:FailureStage = 'dc-ready'
    $ready = Invoke-GuestStage -Role DC -Stage Ready -Credential $dcCredential -Extra @{ReaderPassword=$reader}
    if ($ready.RootCaSha256 -notmatch '^[a-f0-9]{64}$' -or $ready.LDAPS -ne $true -or
        $ready.StartTLS -ne $true -or $ready.ReaderWriteDenied -ne $true) { throw 'DC authenticated readiness failed.' }
    $caPin = $ready.RootCaSha256
    $script:FailureStage = 'member-prepare'
    $prepared = Invoke-GuestStage -Role Member -Stage Prepare -Credential $memberCredential
    $renamedMemberCredential = [pscredential]::new('member01\Administrator', $AdministratorPassword)
    if ($prepared.RebootRequired) { Restart-OwnedGuest -Role Member -BeforeCredential $memberCredential -AfterCredential $renamedMemberCredential }
    $memberCredential = $renamedMemberCredential
    Copy-PublicLabOutputs -Names @('root-ca.cer') -CaPin $caPin
    $script:FailureStage = 'member-join'
    $joined = Invoke-GuestStage -Role Member -Stage Join -Credential $memberCredential `
        -Extra @{DomainAdminPassword=$AdministratorPassword;RootCaSha256=$caPin}
    if (-not $joined.RebootRequired) { throw 'Domain join requires its recorded reboot.' }
    Restart-OwnedGuest -Role Member -BeforeCredential $memberCredential -AfterCredential $memberCredential
    $script:FailureStage = 'member-verify'
    $verified = Invoke-GuestStage -Role Member -Stage Verify -Credential $memberCredential `
        -Extra @{ReaderPassword=$reader;RootCaSha256=$caPin}
    if ($verified.SecureChannel -ne $true -or $verified.LDAPS -ne $true -or $verified.StartTLS -ne $true) {
        throw 'Member verification did not establish domain join and both transports.'
    }
    $report.cases.domain_join = $true
    $script:FailureStage = 'dc-verify'
    $verified = Invoke-GuestStage -Role DC -Stage Verify -Credential $dcCredential -Extra @{ReaderPassword=$reader}
    if ($verified.RootCaSha256 -cne $caPin -or $verified.ReaderWriteDenied -ne $true -or
        $verified.LDAPS -ne $true -or $verified.StartTLS -ne $true) { throw 'Final DC identity or write-denial canary failed.' }
    $report.cases.readonly_write_denied = $true
    Copy-PublicLabOutputs -Names @('root-ca.cer','root-ca.pem','fixtures.json','manifest.json') -CaPin $caPin
    $script:FailureStage = 'transport-tests'
    $s = Get-HostState
    $testParameters = @{LabId=[guid]$labId; ReaderPassword=$reader; RootCaSha256=$caPin;
        TestBinarySha256=$TestBinarySha256; ExpiresUtc=$s.ExpiresUtc}
    $testResult = @(Invoke-GuestCommand -Role Member -Credential $memberCredential -TimeoutSeconds 1200 `
        -Arguments @("C:\ADTR-Stage-$labId\Invoke-MemberTests.ps1",$testParameters) -Command {
            param($Path,$Parameters)
            & $Path @Parameters 3>$null 4>$null 5>$null 6>$null
        })
    if ($testResult.Count -ne 1 -or $testResult[0].LabId -cne $labId -or
        $testResult[0].Role -cne 'Member' -or $testResult[0].Stage -cne 'Tests') { throw 'Member test evidence contract failed.' }
    foreach ($case in @('ldaps','starttls','fixture_values','bad_credential','disabled_account',
        'wrong_tls_identity','untrusted_ca','revoked_authority','cancelled')) {
        $value = $testResult[0].$case
        if ($value -isnot [bool]) { throw 'Each test result must be an explicit boolean.' }
        $report.cases[$case] = $value
    }
    if (@($report.cases.Values | Where-Object { $_ -ne $true }).Count) { throw 'One or more required tests failed.' }
    $script:FailureStage = ''
} catch {
    # Never emit $_, native errors, environment dumps, passwords or guest test text.
    if ($report) { $report.failure_stage = $script:FailureStage }
} finally {
    if ($script:StateCreated) {
        try { Remove-HostedLabResources; if ($report) { $report.cleanup_verified = $true } }
        catch { if ($report) { $report.cleanup_verified = $false; $report.failure_stage = 'cleanup' } }
    }
    $dcCredential = $null; $memberCredential = $null; $promotedCredential = $null; $renamedMemberCredential = $null
    $testParameters = $null; $testResult = $null
    if ($dsrm) { $dsrm.Dispose() }; if ($reader) { $reader.Dispose() }
    # Caller owns the supplied SecureString; dispose its own instance in its finally.
    if ($watchdog) { $watchdog.Dispose() }
    if ($report) {
        $report.passed = $report.cleanup_verified -and -not $report.failure_stage -and
            @($report.cases.Values | Where-Object { $_ -ne $true }).Count -eq 0
        Write-HostJson $report $script:ReportPath
        $report | ConvertTo-Json -Depth 5
    }
}
if (-not $report -or -not $report.passed) {
    throw 'Disposable hosted AD acceptance did not pass. Use only the fixed report; do not upload raw diagnostics.'
}
