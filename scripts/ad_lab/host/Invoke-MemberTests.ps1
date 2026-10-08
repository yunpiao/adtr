#requires -Version 5.1
# Copied to and executed ONLY inside the verified disposable domain member.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][guid]$LabId,
    [Parameter(Mandatory)][securestring]$ReaderPassword,
    [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$RootCaSha256,
    [Parameter(Mandatory)][ValidatePattern('^[a-f0-9]{64}$')][string]$TestBinarySha256,
    [Parameter(Mandatory)][string]$ExpiresUtc
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
try {
    . (Join-Path $PSScriptRoot 'Lab.Common.ps1')
    Assert-LabRuntime
    $marker = Get-LabMarker -LabId $LabId -Role Member
    Assert-LabRebootComplete $marker
    if ($marker.State -ne 'Verified') { throw 'Member verification must finish before tests.' }
    Assert-LabNetwork -ExpectedAddress $script:LabMemberIp -RequireConfigured | Out-Null
    $expires = [DateTimeOffset]::Parse($ExpiresUtc)
    if ($expires -le [DateTimeOffset]::UtcNow -or $expires -gt [DateTimeOffset]::UtcNow.AddMinutes(90)) {
        throw 'A current bounded lease is required.'
    }
    $binary = Join-Path $PSScriptRoot 'adlab.test.exe'
    if ((Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant() -cne $TestBinarySha256) {
        throw 'Test binary pin mismatch.'
    }
    $manifestPath = Join-Path $script:LabPublic 'manifest.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($manifest.lab_id -cne $LabId.ToString() -or $manifest.domain -cne 'adtr.test' -or
        $manifest.dc -cne 'dc01.adtr.test' -or $manifest.ip -cne '192.168.77.10' -or
        $manifest.root_ca_sha256 -cne $RootCaSha256 -or @($manifest.fixtures).Count -ne 5) {
        throw 'Public manifest ownership or fixture contract failed.'
    }
    # The runner's PEM path is local to this member, never a path supplied by the DC.
    $manifest.ca = Join-Path $script:LabPublic 'root-ca.pem'
    Write-LabJson -Value $manifest -Path $manifestPath
    Import-LabPublicCa -Path (Join-Path $script:LabPublic 'root-ca.cer') -Sha256 $RootCaSha256 -LabId $LabId
    $tests = @(
        'TestRealADTLSBindAndDirectory', 'TestRealADRejectsBadCredential',
        'TestRealADRejectsDisabledAccount', 'TestRealADRejectsWrongTLSIdentity',
        'TestRealADRejectsUntrustedCA', 'TestRealADRevokedAuthority',
        'TestRealADCancelled', 'TestRealADCancelledAfterTLS',
        'TestRealADDirectoryRevokedAfterPage', 'TestRealADDirectoryCancelledAfterPage'
    )
    $results = @{}
    foreach ($test in $tests) {
        if ([DateTimeOffset]::UtcNow.AddSeconds(135) -ge $expires) { throw 'Test budget exceeds remaining lease.' }
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = $binary
        $start.Arguments = "-test.v -test.count=1 -test.timeout=120s -test.run=^$test`$"
        $start.WorkingDirectory = $PSScriptRoot
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        # Give only OS essentials and the synthetic reader to the child process.
        # No Administrator/DSRM credentials, host environment, tokens or log files.
        $start.EnvironmentVariables.Clear()
        foreach ($name in @('SystemRoot','WINDIR','TEMP','TMP')) {
            $value = [Environment]::GetEnvironmentVariable($name, 'Process')
            if ($value) { $start.EnvironmentVariables[$name] = $value }
        }
        $start.EnvironmentVariables['PATH'] = "$env:SystemRoot\System32;$env:SystemRoot"
        $start.EnvironmentVariables['ADTR_REAL_AD_LAB'] = 'disposable-adtr-test-only'
        $start.EnvironmentVariables['ADTR_LAB_MANIFEST'] = $manifestPath
        $start.EnvironmentVariables['ADTR_LAB_CA_SHA256'] = $RootCaSha256
        $plain = $null
        $process = [Diagnostics.Process]::new()
        try {
            $plain = [Net.NetworkCredential]::new('', $ReaderPassword).Password
            $start.EnvironmentVariables['ADTR_LAB_READER_PASSWORD'] = $plain
            $process.StartInfo = $start
            if (-not $process.Start()) { throw 'Unable to start test process.' }
            # Drain both pipes concurrently; keep output only in guest memory.
            $stdout = $process.StandardOutput.ReadToEndAsync()
            $stderr = $process.StandardError.ReadToEndAsync()
            $start.EnvironmentVariables.Remove('ADTR_LAB_READER_PASSWORD')
            $plain = $null
            if (-not $process.WaitForExit(130000)) {
                $process.Kill(); [void]$process.WaitForExit(5000)
                throw 'Test exceeded its bounded execution window.'
            }
            $output = $stdout.GetAwaiter().GetResult()
            $errorOutput = $stderr.GetAwaiter().GetResult()
            $runLine = '(?m)^=== RUN   ' + [regex]::Escape($test) + '\r?$'
            $passLine = '(?m)^--- PASS: ' + [regex]::Escape($test) + ' \([0-9.]+s\)\r?$'
            $passed = $process.ExitCode -eq 0 -and [string]::IsNullOrWhiteSpace($errorOutput) -and
                $output -match $runLine -and $output -match $passLine -and
                $output -notmatch '(?m)^--- (FAIL|SKIP):' -and $output -match '(?m)^PASS\r?$'
            if ($test -eq 'TestRealADTLSBindAndDirectory') {
                foreach ($mode in @('ldaps','starttls')) {
                    $subRun = '(?m)^=== RUN   ' + [regex]::Escape("$test/$mode") + '\r?$'
                    $subPass = '(?m)^\s+--- PASS: ' + [regex]::Escape("$test/$mode") + ' \([0-9.]+s\)\r?$'
                    $passed = $passed -and $output -match $subRun -and $output -match $subPass
                }
            }
            $results[$test] = [bool]$passed
            $output = $null; $errorOutput = $null; $stdout = $null; $stderr = $null
        } finally {
            $start.EnvironmentVariables.Remove('ADTR_LAB_READER_PASSWORD')
            $plain = $null
            try { if ($process.Id -and -not $process.HasExited) { $process.Kill() } } catch { }
            $process.Dispose()
        }
    }
    [pscustomobject]@{
        LabId=$LabId.ToString(); Role='Member'; Stage='Tests'
        ldaps=$results['TestRealADTLSBindAndDirectory']; starttls=$results['TestRealADTLSBindAndDirectory']
        fixture_values=$results['TestRealADTLSBindAndDirectory']
        bad_credential=$results['TestRealADRejectsBadCredential']
        disabled_account=$results['TestRealADRejectsDisabledAccount']
        wrong_tls_identity=$results['TestRealADRejectsWrongTLSIdentity']
        untrusted_ca=$results['TestRealADRejectsUntrustedCA']
        revoked_authority=($results['TestRealADRevokedAuthority'] -and $results['TestRealADDirectoryRevokedAfterPage'])
        cancelled=($results['TestRealADCancelled'] -and $results['TestRealADCancelledAfterTLS'] -and
            $results['TestRealADDirectoryCancelledAfterPage'])
    }
} catch {
    throw 'Disposable member test execution failed; raw guest diagnostics are intentionally not exported.'
}
