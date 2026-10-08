#requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Prepare','Join','Verify')][string]$Stage,
    [Parameter(Mandatory)][guid]$LabId,
    [Parameter(Mandatory)][switch]$DisposableLab,
    [securestring]$DomainAdminPassword,
    [securestring]$ReaderPassword,
    [string]$RootCaPath = (Join-Path $env:ProgramData 'ADTR-Lab\public\root-ca.cer'),
    [ValidatePattern('^[a-fA-F0-9]{64}$')][string]$RootCaSha256
)
$ErrorActionPreference = 'Stop'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
try {
    . (Join-Path $PSScriptRoot 'Lab.Common.ps1')
    if (-not $DisposableLab) { throw 'Explicit -DisposableLab opt-in is required.' }
    Assert-LabRuntime
    Assert-LabNetwork -ExpectedAddress $script:LabMemberIp | Out-Null
    $marker = Get-LabMarker -LabId $LabId -Role Member -Initialize:($Stage -eq 'Prepare')
    switch ($Stage) {
        Prepare {
            if ($marker.State -notin @('New','Prepared')) { throw 'Prepare cannot reset a joined lab member.' }
            if ($marker.PendingBootId -eq (Get-LabBootId)) { Write-LabResult $marker $Stage $true; return }
            Set-LabNetwork -Address $script:LabMemberIp
            $reboot = $env:COMPUTERNAME -ine 'member01'
            if ($reboot) { Rename-Computer -NewName member01 -Force | Out-Null }
            Set-LabStage $marker Prepared -NeedsReboot:$reboot
            Write-LabResult $marker $Stage $reboot
        }
        Join {
            Assert-LabRebootComplete $marker
            Assert-LabNetwork -ExpectedAddress $script:LabMemberIp -RequireConfigured | Out-Null
            if ($marker.State -eq 'Joined') { Write-LabResult $marker $Stage; return }
            if ($marker.State -ne 'Prepared' -or $env:COMPUTERNAME -ine 'member01') {
                throw 'Prepare and the hostname reboot must finish before Join.'
            }
            if (-not $DomainAdminPassword -or -not $RootCaSha256) {
                throw 'Join needs DomainAdminPassword SecureString and the pinned public RootCaSha256.'
            }
            if ((Get-CimInstance Win32_ComputerSystem).PartOfDomain) { throw 'Refusing to adopt an already joined computer.' }
            Import-LabPublicCa -Path $RootCaPath -Sha256 $RootCaSha256 -LabId $LabId
            Wait-LabCondition -Description 'isolated DC DNS readiness' -Condition { Assert-LabDns; return $true }
            # Prove run ownership over pinned, normally validated TLS before submitting join credentials.
            $connection = New-LabLdapConnection -Transport LDAPS -Password $DomainAdminPassword -User 'Administrator@adtr.test'
            try {
                $request = [DirectoryServices.Protocols.SearchRequest]::new($script:LabBaseDn, '(objectClass=domainDNS)',
                    [DirectoryServices.Protocols.SearchScope]::Base, @('description','objectSid'))
                $response = [DirectoryServices.Protocols.SearchResponse]$connection.SendRequest($request)
                if ($response.Entries.Count -ne 1 -or
                        $response.Entries[0].Attributes['description'][0] -ne "ADTR disposable lab $LabId") {
                    throw 'The pinned DC does not carry this run ownership marker.'
                }
                $sid = [Security.Principal.SecurityIdentifier]::new(
                    [byte[]]$response.Entries[0].Attributes['objectSid'][0], 0)
                $marker.DomainSid = $sid.Value
                $root = [Security.Cryptography.X509Certificates.X509Certificate2]::new($RootCaPath)
                try { $marker.RootCaThumbprint = $root.Thumbprint } finally { $root.Dispose() }
                Save-LabMarker $marker
            } finally { $connection.Dispose() }
            $credential = [pscredential]::new('ADTR\Administrator', $DomainAdminPassword)
            Set-LabStage $marker JoinStarted
            $joined = Add-Computer -DomainName adtr.test -Server dc01.adtr.test -Credential $credential -PassThru -Force
            if (-not $joined.HasSucceeded) { throw 'The domain join did not succeed.' }
            Set-LabStage $marker Joined -NeedsReboot
            Write-LabResult $marker $Stage $true
        }
        Verify {
            Assert-LabRebootComplete $marker
            if ($marker.State -notin @('Joined','Verified')) { throw 'Complete the join and reboot first.' }
            if (-not $ReaderPassword -or -not $RootCaSha256) {
                throw 'Verify needs ReaderPassword SecureString and the pinned public RootCaSha256.'
            }
            Assert-LabNetwork -ExpectedAddress $script:LabMemberIp -RequireConfigured | Out-Null
            $computer = Get-CimInstance Win32_ComputerSystem
            if (-not $computer.PartOfDomain -or $computer.Domain -ne 'adtr.test' -or $computer.DomainRole -ne 3) {
                throw 'The guest is not an owned Windows Server domain member.'
            }
            Import-LabPublicCa -Path $RootCaPath -Sha256 $RootCaSha256 -LabId $LabId
            Wait-LabCondition -Description 'member secure channel and DC DNS readiness' -Condition {
                Assert-LabDns
                return (Test-ComputerSecureChannel -Server dc01.adtr.test)
            }
            Test-LabReaderTransports -Password $ReaderPassword
            # Verify actual current forest SID and member identity as the non-admin reader.
            $connection = New-LabLdapConnection -Transport LDAPS -Password $ReaderPassword
            try {
                $request = [DirectoryServices.Protocols.SearchRequest]::new($script:LabBaseDn, '(objectClass=domainDNS)',
                    [DirectoryServices.Protocols.SearchScope]::Base, @('objectSid','description'))
                $response = [DirectoryServices.Protocols.SearchResponse]$connection.SendRequest($request)
                if ($response.Entries.Count -ne 1) { throw 'Domain ownership lookup failed.' }
                $sid = [Security.Principal.SecurityIdentifier]::new(
                    [byte[]]$response.Entries[0].Attributes['objectSid'][0], 0)
                if ($sid.Value -ne $marker.DomainSid -or
                        $response.Entries[0].Attributes['description'][0] -ne "ADTR disposable lab $LabId") {
                    throw 'The current directory differs from the owned joined domain.'
                }
                $request = [DirectoryServices.Protocols.SearchRequest]::new($script:LabBaseDn,
                    '(&(objectClass=computer)(sAMAccountName=member01$)(dNSHostName=member01.adtr.test))',
                    [DirectoryServices.Protocols.SearchScope]::Subtree, @('objectGUID'))
                $response = [DirectoryServices.Protocols.SearchResponse]$connection.SendRequest($request)
                if ($response.Entries.Count -ne 1) { throw 'Joined member directory identity is absent.' }
            } finally { $connection.Dispose() }
            Set-LabStage $marker Verified
            Write-LabResult $marker $Stage $false @{
                Computer = 'member01.adtr.test'; Address = $script:LabMemberIp
                SecureChannel = $true; LDAPS = $true; StartTLS = $true
            }
        }
    }
} catch {
    throw [InvalidOperationException]::new("ADTR disposable member stage '$Stage' failed. Inspect the isolated guest; do not upload raw diagnostics.")
}
