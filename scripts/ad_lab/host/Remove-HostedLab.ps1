#requires -Version 5.1
[CmdletBinding()]
param([switch]$WaitForLease)
# Independent always() cleanup entry point. No credentials or arbitrary state paths.
try {
    . (Join-Path $PSScriptRoot 'Host.Common.ps1')
    Assert-HostedLabHost
    if (-not (Test-Path -LiteralPath $script:StatePath)) { return }
    if ($WaitForLease) {
        $s = Get-HostState
        $ready = Join-Path $s.Root 'watchdog-ready.json'
        Write-HostJson ([ordered]@{ LabId=$s.LabId; Pid=$PID;
            StartTicks=(Get-Process -Id $PID).StartTime.ToUniversalTime().Ticks.ToString() }) $ready
        do {
            $s = Get-HostState
            if ($s.CleanupVerified) { return }
            $controller = Get-Process -Id ([int]$s.ControllerPid) -ErrorAction SilentlyContinue
            if (-not $controller -or $controller.StartTime.ToUniversalTime().Ticks.ToString() -ne $s.ControllerStartTicks) { break }
            if ([DateTimeOffset]::UtcNow -ge [DateTimeOffset]::Parse($s.ExpiresUtc)) { break }
            Start-Sleep -Seconds 2
        } while ($true)
    }
    Remove-HostedLabResources
} catch {
    # No provider exception, path, credential, transcript or guest output is emitted.
    throw 'Disposable hosted AD lab cleanup was not verified; runner teardown remains the final backstop.'
}
