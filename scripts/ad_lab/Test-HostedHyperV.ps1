#requires -Version 5.1
[CmdletBinding()]
param([switch]$CleanupOnly)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    $env:GITHUB_REPOSITORY -ne 'yunpiao/adtr') { throw 'This probe requires the approved GitHub-hosted repository job.' }
if ($env:ADTR_SOURCE_SHA -notmatch '^[a-f0-9]{40}$') { throw 'A fixed source SHA is required.' }
$statePath = Join-Path $env:RUNNER_TEMP 'adtr-hyperv-capability-state.json'
$reportPath = Join-Path $env:RUNNER_TEMP 'adtr-hyperv-capability.json'
function Save-State($State) {
    $State | ConvertTo-Json | Set-Content -LiteralPath "$statePath.tmp" -Encoding UTF8
    Move-Item -LiteralPath "$statePath.tmp" -Destination $statePath -Force
}
function Remove-OwnedResources {
    if (-not (Test-Path -LiteralPath $statePath)) { return }
    $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($state.RunId -ne "$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT" -or
        $state.Owner -notmatch '^adtr-capability-[a-f0-9]{32}$') { throw 'Unexpected probe ownership record.' }
    if (-not $state.VmId -and $state.NameReserved) {
        # The exact unpredictable name was reserved before New-VM. Recover its
        # returned ID if allocation succeeded but creation/state persistence threw.
        $owned = @(Get-VM -ErrorAction Stop | Where-Object Name -eq $state.Owner)
        if ($owned.Count -gt 1) { throw 'Ambiguous exact-name probe ownership.' }
        if ($owned.Count -eq 1) {
            if ($owned[0].Notes -and $owned[0].Notes -ne $state.Owner) { throw 'Unrecorded VM ownership mismatch.' }
            $state.VmId = $owned[0].Id.ToString(); Save-State $state
        }
    }
    if ($state.VmId) {
        $vm = Get-VM -ErrorAction Stop | Where-Object Id -eq ([guid]$state.VmId)
        if ($vm) {
            if ($vm.Name -ne $state.Owner -or ($vm.Notes -and $vm.Notes -ne $state.Owner)) { throw 'VM ownership mismatch.' }
            if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force -Confirm:$false }
            Remove-VM -VM $vm -Force -Confirm:$false
            if (@(Get-VM -ErrorAction Stop | Where-Object Id -eq ([guid]$state.VmId)).Count -ne 0) { throw 'VM cleanup verification failed.' }
        }
    }

}
if ($CleanupOnly) {
    try { Remove-OwnedResources } catch { throw 'Owned VM cleanup could not be verified.' }
    return
}
if (Test-Path -LiteralPath $statePath) { throw 'Existing capability state requires cleanup first.' }
$owner = 'adtr-capability-' + [guid]::NewGuid().ToString('N')
$state = [ordered]@{RunId="$env:GITHUB_RUN_ID-$env:GITHUB_RUN_ATTEMPT";Owner=$owner;VmId='';NameReserved=$false}
Save-State $state
$report = [ordered]@{
    schema=1; source_sha=$env:ADTR_SOURCE_SHA; image_os=$env:ImageOS; image_version=$env:ImageVersion
    hypervisor_present=$false; hyperv_installed=$false; vmms_running=$false
    diskless_vm_started=$false; disconnected_vm_verified=$false; cleanup_verified=$false
    windows_guest_booted=$false; ad_ds_verified=$false; passed=$false; failure_stage=''; failure_hresult=''
    logical_cpus=0; physical_memory_gib=0; temp_disk_free_gib=0
}
$stage = 'inventory'
try {
    $system = Get-CimInstance Win32_ComputerSystem
    $report.hypervisor_present = [bool]$system.HypervisorPresent
    $report.logical_cpus = [Environment]::ProcessorCount
    $report.physical_memory_gib = [Math]::Round($system.TotalPhysicalMemory / 1GB, 2)
    $report.temp_disk_free_gib = [Math]::Round((Get-Item -LiteralPath $env:RUNNER_TEMP).PSDrive.Free / 1GB, 2)
    $report.hyperv_installed = [bool](Get-WindowsFeature -Name Hyper-V).Installed
    $report.vmms_running = (Get-Service vmms).Status -eq 'Running'
    if (-not $report.hyperv_installed -or -not $report.vmms_running) { throw 'Hyper-V is not already ready.' }
    # No role install, host reboot, virtual switch, disk, Windows image or license acceptance.
    $stage = 'vm-create'
    if (@(Get-VM -ErrorAction Stop | Where-Object Name -eq $owner).Count -ne 0) { throw 'Probe name must be unused.' }
    $state.NameReserved = $true; Save-State $state
    $vm = New-VM -Name $owner -Generation 2 -MemoryStartupBytes 512MB -NoVHD
    $state.VmId = $vm.Id.ToString(); Save-State $state
    Set-VM -VM $vm -Notes $owner -AutomaticStartAction Nothing -AutomaticStopAction TurnOff
    $report.disconnected_vm_verified = @(Get-VMNetworkAdapter -VM $vm | Where-Object { -not [string]::IsNullOrEmpty($_.SwitchName) }).Count -eq 0
    if (-not $report.disconnected_vm_verified) { throw 'VM must have no connected network.' }
    $stage = 'vm-start'
    Start-VM -VM $vm
    Start-Sleep -Seconds 10
    $vm = Get-VM -Id ([guid]$state.VmId)
    $report.diskless_vm_started = $vm.State -eq 'Running'
    if (-not $report.diskless_vm_started) { throw 'Diskless VM did not remain running.' }
} catch {
    # Never export raw provider/host diagnostics, names, paths or exception text.
    $report.failure_stage = $stage
    $report.failure_hresult = '0x{0:X8}' -f $_.Exception.HResult
} finally {
    try { Remove-OwnedResources; $report.cleanup_verified = $true }
    catch { $report.cleanup_verified = $false; $report.failure_stage = 'cleanup' }
    $report.passed = $report.diskless_vm_started -and $report.disconnected_vm_verified -and $report.cleanup_verified
    $report | ConvertTo-Json | Set-Content -LiteralPath $reportPath -Encoding UTF8
    $report | ConvertTo-Json
}
if (-not $report.passed) { throw 'Hosted VM capability not established; see the sanitized report.' }
