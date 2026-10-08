#requires -Version 5.1
# Host-only helpers. No credentials are accepted, serialized or recovered here.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$VerbosePreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
$script:HostedLabTaskBranch = 'feat/ad-lab-windows-install'

function Assert-HostedLabHost {
    if ($PSVersionTable.PSEdition -ne 'Desktop' -or -not [Environment]::Is64BitProcess -or
        $env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
        $env:RUNNER_OS -ne 'Windows' -or $env:GITHUB_REPOSITORY -ne 'yunpiao/adtr' -or
        $env:GITHUB_EVENT_NAME -notin @('workflow_dispatch','pull_request') -or
        $env:GITHUB_RUN_ID -notmatch '^\d+$' -or $env:GITHUB_RUN_ATTEMPT -cne '1' -or
        $env:GITHUB_ACTOR_ID -cne '11422136' -or $env:GITHUB_ACTOR -cne 'yunpiao' -or
        $env:GITHUB_TRIGGERING_ACTOR -cne 'yunpiao' -or
        $env:ADTR_SOURCE_SHA -notmatch '^[a-f0-9]{40}$') {
        throw 'Only an explicitly approved fixed-source GitHub-hosted Windows lab job is supported.'
    }
    $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'The hosted job must already be elevated.'
    }
    if (-not $env:RUNNER_TEMP -or -not (Test-Path -LiteralPath $env:RUNNER_TEMP -PathType Container)) {
        throw 'Hosted runner temporary directory is unavailable.'
    }
    $script:HostTemp = [IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\')
    Assert-NoReparse -Path $script:HostTemp
    $script:StatePath = Join-Path $script:HostTemp 'adtr-real-ad-state.json'
    $script:ReportPath = Join-Path $script:HostTemp 'adtr-real-ad-report.json'
    $script:CleanupPath = Join-Path $script:HostTemp 'adtr-real-ad-cleanup.json'
    $script:RunKey = "$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT"
    if (-not (Get-WindowsFeature Hyper-V).Installed -or (Get-Service vmms).Status -ne 'Running') {
        throw 'Hyper-V must already be installed and running; the controller does not install it.'
    }
}

function Assert-TaskSourceProvenance {
    param([Parameter(Mandatory)][string]$SourceRoot, [Parameter(Mandatory)][string]$SourceSha,
        [AllowEmptyString()][string]$ExpectedPullRequestHeadSha,
        [AllowEmptyString()][string]$ExpectedPullRequestHeadRef,
        [int]$ExpectedPullRequestNumber = 0)
    if ($env:GITHUB_SHA -cne $SourceSha -or $env:GITHUB_WORKFLOW_SHA -cne $SourceSha) {
        throw 'Checkout, event and executing workflow must use one immutable source commit.'
    }
    # These are provenance checks for the current delegated task, not an
    # independent approval mechanism. The task publisher must review each commit.
    # No standard immutable triggering-actor ID is available in the environment;
    # permit only the initial owner-triggered attempt, never infer rerun identity.
    if ($env:GITHUB_ACTOR_ID -cne '11422136' -or $env:GITHUB_ACTOR -cne 'yunpiao' -or
        $env:GITHUB_TRIGGERING_ACTOR -cne 'yunpiao' -or $env:GITHUB_RUN_ATTEMPT -cne '1') {
        throw 'Only an initial run triggered by the fixed task owner is supported.'
    }
    if (-not $env:GITHUB_EVENT_PATH) { throw 'GitHub event metadata is unavailable.' }
    Assert-NoReparse $env:GITHUB_EVENT_PATH
    if ((Get-Item -LiteralPath $env:GITHUB_EVENT_PATH).Length -gt 5MB) { throw 'Unexpected event metadata size.' }
    $event = Get-Content -LiteralPath $env:GITHUB_EVENT_PATH -Raw | ConvertFrom-Json
    if ($event.repository.full_name -cne 'yunpiao/adtr' -or
        $event.repository.owner.id -ne 11422136 -or $event.repository.owner.login -cne 'yunpiao' -or
        $event.sender.id -ne 11422136 -or $event.sender.login -cne 'yunpiao') {
        throw 'Event repository and sender must match the fixed task owner.'
    }
    if ($env:GITHUB_EVENT_NAME -eq 'workflow_dispatch') {
        if ($ExpectedPullRequestHeadSha -or $ExpectedPullRequestHeadRef -or $ExpectedPullRequestNumber -or
            $env:GITHUB_REF -cne ("refs/heads/" + $script:HostedLabTaskBranch)) {
            throw 'Dispatch requires the dedicated task branch and no pull-request expectations.'
        }
        return [pscustomobject]@{ SourceHeadSha=$SourceSha }
    }
    if ($env:GITHUB_EVENT_NAME -ne 'pull_request' -or
        $ExpectedPullRequestHeadSha -notmatch '^[a-f0-9]{40}$' -or
        $ExpectedPullRequestHeadRef -cne $script:HostedLabTaskBranch -or
        $env:GITHUB_HEAD_REF -cne $script:HostedLabTaskBranch -or $ExpectedPullRequestNumber -lt 1 -or
        $env:GITHUB_REF -cne "refs/pull/$ExpectedPullRequestNumber/merge") {
        throw 'Pull-request provenance must name the dedicated task branch and exact event head and PR.'
    }
    if ($event.number -ne $ExpectedPullRequestNumber -or
        $event.pull_request.user.id -ne 11422136 -or $event.pull_request.user.login -cne 'yunpiao' -or
        $event.pull_request.head.repo.full_name -cne 'yunpiao/adtr' -or $event.pull_request.head.repo.fork -ne $false -or
        $event.pull_request.head.repo.owner.id -ne 11422136 -or $event.pull_request.head.repo.owner.login -cne 'yunpiao' -or
        $event.pull_request.base.repo.full_name -cne 'yunpiao/adtr' -or $event.pull_request.base.repo.fork -ne $false -or
        $event.pull_request.base.repo.owner.id -ne 11422136 -or $event.pull_request.base.repo.owner.login -cne 'yunpiao' -or
        $event.pull_request.head.sha -cne $ExpectedPullRequestHeadSha -or
        $event.pull_request.head.ref -cne $script:HostedLabTaskBranch -or
        $event.pull_request.base.sha -notmatch '^[a-f0-9]{40}$') {
        throw 'Pull request, publisher and repositories do not match this dedicated task.'
    }
    $parents = @(& git -C $SourceRoot rev-list --parents -n 1 $SourceSha 2>$null)
    if ($LASTEXITCODE -ne 0 -or $parents.Count -ne 1) { throw 'Synthetic merge ancestry is unavailable.' }
    $ids = $parents[0] -split ' '
    if ($ids.Count -ne 3 -or $ids[0] -cne $SourceSha -or
        $ids[1] -cne $event.pull_request.base.sha -or $ids[2] -cne $ExpectedPullRequestHeadSha) {
        throw 'Source must be the exact event merge with expected head and base parents.'
    }
    $mergeTree = @(& git -C $SourceRoot rev-parse ($SourceSha + '^{tree}') 2>$null)
    if ($LASTEXITCODE -ne 0 -or $mergeTree.Count -ne 1 -or $mergeTree[0] -notmatch '^[a-f0-9]{40}$') {
        throw 'Synthetic merge tree is unavailable.'
    }
    $headTree = @(& git -C $SourceRoot rev-parse ($ExpectedPullRequestHeadSha + '^{tree}') 2>$null)
    if ($LASTEXITCODE -ne 0 -or $headTree.Count -ne 1 -or $mergeTree[0] -cne $headTree[0]) {
        throw 'The entire executed merge tree must equal the reviewed task head tree.'
    }
    return [pscustomobject]@{ SourceHeadSha=$ExpectedPullRequestHeadSha }
}

function Assert-PinnedJobTimeout {
    param([Parameter(Mandatory)][string]$SourceRoot, [Parameter(Mandatory)][string]$SourceSha,
        [Parameter(Mandatory)][int]$LeaseMinutes)
    # Deliberately support only literal, conventional YAML for this dedicated job.
    # This is not a general YAML parser and never evaluates expressions/anchors.
    if ($env:GITHUB_WORKFLOW_SHA -cne $SourceSha -or $env:GITHUB_JOB -notmatch '^[A-Za-z_][A-Za-z0-9_-]*$' -or
        $env:GITHUB_WORKFLOW_REF -notmatch '^yunpiao/adtr/(?<file>\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml)@refs/') {
        throw 'The active workflow must come from the same immutable source.'
    }
    $workflow = Join-Path $SourceRoot $Matches['file']
    Assert-NoReparse $workflow
    $lines = @(Get-Content -LiteralPath $workflow)
    if (@($lines | Where-Object { $_ -match '^jobs:\s*(#.*)?$' }).Count -ne 1) {
        throw 'A conventional literal workflow jobs mapping is required.'
    }
    $header = '^  ' + [regex]::Escape($env:GITHUB_JOB) + ':\s*(#.*)?$'
    $starts = @(for ($i=0; $i -lt $lines.Count; $i++) { if ($lines[$i] -match $header) { $i } })
    if ($starts.Count -ne 1) { throw 'Current hosted job could not be resolved uniquely in pinned workflow.' }
    $block = @()
    for ($i=$starts[0]+1; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match '^\s*(#.*)?$') { continue }
        if ($lines[$i] -match '^\S|^ {1,2}\S') { break }
        $block += $lines[$i]
    }
    $timeouts = @($block | Where-Object { $_ -match '^    timeout-minutes:\s*[0-9]+\s*(#.*)?$' })
    $keys = @($block | Where-Object { $_ -match '^    timeout-minutes:' })
    if ($timeouts.Count -ne 1 -or $keys.Count -ne 1 -or
        @($block | Where-Object { $_ -match '^    <<:' }).Count) {
        throw 'Current job needs one literal timeout-minutes without YAML merges or expressions.'
    }
    [void]($timeouts[0] -match '^    timeout-minutes:\s*(?<minutes>[0-9]+)')
    $minutes = [int]$Matches['minutes']
    if ($minutes -lt 1 -or $minutes -gt $LeaseMinutes -or $minutes -gt 90) {
        throw 'The hosted job hard timeout must be no longer than the lab lease (maximum 90 minutes).'
    }
}

function Assert-NoReparse {
    param([Parameter(Mandatory)][string]$Path)
    $current = [IO.Path]::GetFullPath($Path)
    while ($current) {
        if (Test-Path -LiteralPath $current) {
            if ((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw 'Reparse points are forbidden in lab state and resource paths.'
            }
        }
        $parent = [IO.Directory]::GetParent($current)
        if ($null -eq $parent) { break }
        $current = $parent.FullName
    }
}

function Set-HostPrivateDirectory {
    param([Parameter(Mandatory)][string]$Path)
    Assert-NoReparse $Path
    [void][IO.Directory]::CreateDirectory($Path)
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl',
            'ContainerInherit,ObjectInherit', 'None', 'Allow')
        [void]$acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Write-HostJson {
    param([Parameter(Mandatory)]$Value, [Parameter(Mandatory)][string]$Path)
    Assert-NoReparse $Path
    $temporary = "$Path.$([guid]::NewGuid().ToString('N')).tmp"
    try {
        [IO.File]::WriteAllText($temporary, ($Value | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false))
        if (Test-Path -LiteralPath $Path) {
            [IO.File]::Replace($temporary, $Path, $null)
        } else { [IO.File]::Move($temporary, $Path) }
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}

function Get-HostState {
    Assert-NoReparse $script:StatePath
    $s = Get-Content -LiteralPath $script:StatePath -Raw | ConvertFrom-Json
    if ($s.Schema -ne 1 -or $s.RunKey -ne $script:RunKey -or $s.SourceSha -ne $env:ADTR_SOURCE_SHA -or
        $s.SourceHeadSha -notmatch '^[a-f0-9]{40}$' -or $s.ImageMode -notin @('SuppliedVhd','EvaluationIso') -or
        $s.LabId -notmatch '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$') {
        throw 'The ownership record is not for this hosted run.'
    }
    $root = Join-Path $script:HostTemp "adtr-real-ad-$($s.LabId)"
    if ($s.Root -cne $root -or $s.Owner -cne "ADTR:$($s.LabId):$script:RunKey" -or
        $s.Switch.Name -cne "adtr-$($s.LabId)-private" -or @($s.Machines).Count -ne 2) {
        throw 'Unexpected exact resource reservations.'
    }
    $created = [DateTimeOffset]::Parse($s.CreatedUtc)
    $expires = [DateTimeOffset]::Parse($s.ExpiresUtc)
    if ($expires -le $created -or $expires -gt $created.AddMinutes(90)) { throw 'Invalid bounded lab lease.' }
    $roles = @('DC','Member'); $names = @('dc01','member01')
    for ($i = 0; $i -lt 2; $i++) {
        $m = $s.Machines[$i]
        $directory = Join-Path $root $names[$i]
        if ($m.Role -cne $roles[$i] -or $m.Name -cne "adtr-$($s.LabId)-$($names[$i])" -or
            $m.Directory -cne $directory -or $m.VmPath -cne (Join-Path $directory 'vm') -or
            $m.DiskPath -cne (Join-Path $directory 'os.vhdx') -or
            ($m.Id -and $m.Id -notmatch '^[a-fA-F0-9-]{36}$')) { throw 'Unexpected reserved machine resource.' }
    }
    Assert-NoReparse $root
    return $s
}

function Invoke-HostLock {
    param([Parameter(Mandatory)][scriptblock]$Action)
    $mutex = [Threading.Mutex]::new($false, "Local\ADTR-RealAD-$script:RunKey")
    $locked = $false
    try {
        try { $locked = $mutex.WaitOne([TimeSpan]::FromMinutes(2)) }
        catch [Threading.AbandonedMutexException] { $locked = $true }
        if (-not $locked) { throw 'Timed out obtaining the lab resource lock.' }
        & $Action
    } finally {
        if ($locked) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

function Assert-HostLease {
    param([Parameter(Mandatory)]$State, [int]$ReserveSeconds = 0)
    if ($State.CleanupVerified -or [DateTimeOffset]::UtcNow.AddSeconds($ReserveSeconds) -ge
        [DateTimeOffset]::Parse($State.ExpiresUtc)) { throw 'The disposable lab lease has ended.' }
}

function Assert-VmReservation {
    param([Parameter(Mandatory)]$Vm, [Parameter(Mandatory)]$Machine, [Parameter(Mandatory)]$State)
    if ($Vm.Name -cne $Machine.Name -or ($Vm.Notes -and $Vm.Notes -cne $State.Owner)) {
        throw 'VM ownership mismatch.'
    }
    $location = [IO.Path]::GetFullPath($Vm.ConfigurationLocation).TrimEnd('\')
    $expected = [IO.Path]::GetFullPath($Machine.VmPath).TrimEnd('\')
    if ($location -ine $expected -and -not $location.StartsWith($expected + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'VM configuration is outside its exact reserved path.'
    }
    foreach ($disk in @(Get-VMHardDiskDrive -VM $Vm)) {
        if ($disk.Path -and $disk.Path -ine $Machine.DiskPath) { throw 'Unexpected disk attached to the owned VM.' }
    }
}

function Get-ReservedVm {
    param([Parameter(Mandatory)]$Machine, [Parameter(Mandatory)]$State)
    $all = @(Get-VM -ErrorAction Stop)
    if ($Machine.Id) { $matches = @($all | Where-Object { $_.Id.ToString() -eq $Machine.Id }) }
    elseif ($Machine.CreateStarted) { $matches = @($all | Where-Object Name -eq $Machine.Name) }
    else { return $null }
    if ($matches.Count -gt 1) { throw 'Ambiguous exact VM reservation.' }
    if ($matches.Count -eq 0) { return $null }
    Assert-VmReservation -Vm $matches[0] -Machine $Machine -State $State
    return $matches[0]
}

function Assert-PrivateTopology {
    param([Parameter(Mandatory)]$State)
    $switches = @(Get-VMSwitch | Where-Object { $_.Id.ToString() -eq $State.Switch.Id })
    if ($switches.Count -ne 1 -or $switches[0].Name -cne $State.Switch.Name -or
        $switches[0].SwitchType -ne 'Private' -or $switches[0].Notes -cne $State.Owner) {
        throw 'The owned switch is not private and isolated.'
    }
    if (@(Get-VMNetworkAdapter -ManagementOS | Where-Object { $_.SwitchId.ToString() -eq $State.Switch.Id }).Count) {
        throw 'Management OS must not be attached to the lab switch.'
    }
    $ownedIds = @($State.Machines | ForEach-Object Id)
    foreach ($vm in @(Get-VM)) {
        if ($vm.Id.ToString() -notin $ownedIds) { throw 'This fresh hosted runner cannot contain an unowned VM.' }
        foreach ($nic in @(Get-VMNetworkAdapter -VM $vm)) {
            if ($nic.SwitchId.ToString() -eq $State.Switch.Id -and $vm.Id.ToString() -notin $ownedIds) {
                throw 'An unowned VM is attached to the lab switch.'
            }
        }
    }
    foreach ($m in $State.Machines) {
        $vm = Get-ReservedVm -Machine $m -State $State
        if (-not $vm -or $vm.Notes -cne $State.Owner) { throw 'Owned VM is missing.' }
        $nics = @(Get-VMNetworkAdapter -VM $vm)
        if ($nics.Count -ne 1 -or $nics[0].SwitchId.ToString() -ne $State.Switch.Id) {
            throw 'Each guest must have exactly one NIC on the private switch.'
        }
    }
}

function Assert-ImagePreparationReady {
    param([Parameter(Mandatory)]$State, [switch]$AllowUnpreparedOtherChild)
    if ($State.ImageMode -eq 'SuppliedVhd') {
        if ($State.PSObject.Properties['Media']) { throw 'Supplied-image mode cannot adopt evaluation image state.' }
        return
    }
    if ($State.ImageMode -ne 'EvaluationIso') { throw 'Unknown image preparation mode.' }
    $media = Assert-EvaluationMediaReservation $State
    if ($media.Stage -cne 'Ready' -or -not $media.LicenseBytesVerified -or
        $media.NativeOperationUnresolved -or $media.BaseMountStarted -or $media.WimMountStarted -or $media.IsoMountStarted -or
        $State.BaseVhdSha256 -cne $media.BaseSha256) { throw 'Evaluation media is not verified, ready and detached.' }
    foreach ($machine in $State.Machines) {
        if (-not $machine.DiskCreateStarted -and $AllowUnpreparedOtherChild) { continue }
        $p = $machine.PSObject.Properties['ImagePreparation']
        if (-not $machine.DiskCreateStarted -or -not $p -or $p.Value.Stage -cne 'Ready' -or
            $p.Value.MountStarted -or -not $p.Value.DiskIdentifier) {
            throw 'Both exact child images must be prepared and detached before VM creation or boot.'
        }
    }
}

function Remove-HostedLabResources {
    # Lock coordinates controller, independent always() cleanup, and job-local watchdog.
    Invoke-HostLock {
        if (-not (Test-Path -LiteralPath $script:StatePath)) { return }
        $s = Get-HostState
        $failed = $false
        # Never operate a shared host. Resolve partial creations by the same exact
        # name/path checks before deciding whether every registered VM is owned.
        $ownedIds = @(foreach ($m in $s.Machines) {
            $reserved = Get-ReservedVm -Machine $m -State $s
            if ($reserved) { $reserved.Id.ToString() }
        })
        if (@(Get-VM | Where-Object { $_.Id.ToString() -notin $ownedIds }).Count) {
            $s.CleanupVerified = $false; Write-HostJson $s $script:StatePath
            Write-HostJson ([ordered]@{ schema=1; source_sha=$s.SourceSha; lab_id=$s.LabId;
                cleanup_verified=$false }) $script:CleanupPath
            throw 'Unexpected unowned VM; refusing cleanup on a shared host.'
        }
        foreach ($m in $s.Machines) {
            try {
                $vm = Get-ReservedVm -Machine $m -State $s
                if ($vm) {
                    # Recover an ID only from its pre-mutation exact name AND configuration path.
                    if (-not $m.Id) { $m.Id = $vm.Id.ToString(); Write-HostJson $s $script:StatePath }
                    if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force -Confirm:$false | Out-Null }
                    Remove-VM -VM $vm -Force -Confirm:$false | Out-Null
                }
                if (@(Get-VM | Where-Object { $_.Id.ToString() -eq $m.Id -or $_.Name -eq $m.Name }).Count) {
                    throw 'The exact reserved VM remains.'
                }
            } catch { $failed = $true }
        }
        try {
            $switches = @(Get-VMSwitch)
            $sw = @($switches | Where-Object { ($s.Switch.Id -and $_.Id.ToString() -eq $s.Switch.Id) -or
                (-not $s.Switch.Id -and $s.Switch.CreateStarted -and $_.Name -eq $s.Switch.Name) })
            if ($sw.Count -gt 1) { throw 'Ambiguous switch reservation.' }
            if ($sw.Count -eq 1) {
                if ($sw[0].Name -cne $s.Switch.Name -or $sw[0].SwitchType -ne 'Private' -or
                    ($sw[0].Notes -and $sw[0].Notes -cne $s.Owner)) { throw 'Switch ownership mismatch.' }
                if (-not $s.Switch.Id) { $s.Switch.Id = $sw[0].Id.ToString(); Write-HostJson $s $script:StatePath }
                $nics = @(Get-VMNetworkAdapter -ManagementOS)
                foreach ($vm in @(Get-VM)) { $nics += @(Get-VMNetworkAdapter -VM $vm) }
                if (@($nics | Where-Object { $_.SwitchId.ToString() -eq $s.Switch.Id }).Count) {
                    throw 'Do not remove a switch with remaining attached adapters.'
                }
                Remove-VMSwitch -VMSwitch $sw[0] -Force -Confirm:$false | Out-Null
            }
            if (@(Get-VMSwitch | Where-Object { $_.Id.ToString() -eq $s.Switch.Id -or $_.Name -eq $s.Switch.Name }).Count) {
                throw 'The exact reserved switch remains.'
            }
        } catch { $failed = $true }
        # Mount state is checked even when the resource directory is already
        # absent. Missing files cannot erase an unresolved native-operation flag.
        if (-not $failed) {
            try { Remove-HostedLabImageMounts; $s = Get-HostState }
            catch { $failed = $true }
        }
        # Never remove VM files while any VM deletion, mount or ownership check is unresolved.
        if (-not $failed -and $s.RootCreateStarted -and (Test-Path -LiteralPath $s.Root)) {
            try {
                # Also excludes foreign VM configurations and attached parent-chain
                # dependencies before any recursive deletion of the reserved root.
                if (@(Get-VM).Count -ne 0) { throw 'A VM remains; keep all owned files for runner decommission.' }
                Assert-NoReparse $s.Root
                foreach ($item in @(Get-ChildItem -LiteralPath $s.Root -Recurse -Force)) {
                    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unsafe owned resource subtree.' }
                }
                foreach ($vm in @(Get-VM)) {
                    foreach ($disk in @(Get-VMHardDiskDrive -VM $vm)) {
                        if ($disk.Path -and $disk.Path.StartsWith($s.Root + '\', [StringComparison]::OrdinalIgnoreCase)) {
                            throw 'A reserved disk remains attached to another VM.'
                        }
                    }
                }
                # Exact run-owned subtree only. An externally supplied base stays
                # outside it; a new evaluation base belongs to and dies with this run.
                Remove-Item -LiteralPath $s.Root -Recurse -Force
                if (Test-Path -LiteralPath $s.Root) { throw 'Owned resource files remain.' }
            } catch { $failed = $true }
        }
        # Helpers can persist stricter failure markers before throwing. Always
        # preserve their newest journal rather than write an older snapshot over it.
        $s = Get-HostState
        $s.CleanupVerified = -not $failed
        Write-HostJson $s $script:StatePath
        Write-HostJson ([ordered]@{ schema=1; source_sha=$s.SourceSha; lab_id=$s.LabId;
            cleanup_verified=[bool]$s.CleanupVerified }) $script:CleanupPath
        if ($failed) { throw 'Exact-resource cleanup could not be verified.' }
    }
}

# Definition-only import makes the same exact mount cleanup available to the
# controller, independent cleanup entrypoint and job-local watchdog.
. (Join-Path $PSScriptRoot 'Image.Common.ps1')
