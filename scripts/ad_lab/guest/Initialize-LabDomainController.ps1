#requires -Version 5.1
<#
Run stages in order through a provider's authenticated guest channel, never a public listener.
Only Prepare creates ownership; every subsequent operation checks it. See README.md.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Prepare','Promote','Configure','Ready','Verify')][string]$Stage,
    [Parameter(Mandatory)][guid]$LabId,
    [Parameter(Mandatory)][switch]$DisposableLab,
    [securestring]$DSRMPassword,
    [securestring]$ReaderPassword
)
$ErrorActionPreference = 'Stop'
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
try {
. (Join-Path $PSScriptRoot 'Lab.Common.ps1')
if (-not $DisposableLab) { throw 'Explicit -DisposableLab opt-in is required.' }
Assert-LabRuntime
Assert-LabNetwork -ExpectedAddress $script:LabDcIp | Out-Null
$marker = Get-LabMarker -LabId $LabId -Role DC -Initialize:($Stage -eq 'Prepare')
$ownership = "ADTR disposable lab $LabId"

function Assert-OwnedForest {
    Import-Module ActiveDirectory
    $domain = Get-ADDomain -Server $script:LabDc
    $forest = Get-ADForest -Server $script:LabDc
    $dcs = @(Get-ADDomainController -Filter * -Server $script:LabDc)
    if ($domain.DNSRoot -ne $script:LabDomain -or $domain.NetBIOSName -ne 'ADTR' -or
            $forest.RootDomain -ne $script:LabDomain -or @($forest.Domains).Count -ne 1 -or
            $dcs.Count -ne 1 -or $dcs[0].HostName -ne $script:LabDc -or
            @(Get-ADTrust -Filter * -Server $script:LabDc).Count -ne 0) {
        throw 'Forest topology is not the expected single-DC disposable lab.'
    }
    if ($marker.DomainSid -and $marker.DomainSid -ne $domain.DomainSID.Value) {
        throw 'Forest SID does not match the owned lab.'
    }
    return $domain
}

function Wait-LabDirectory {
    Wait-LabCondition -Description 'AD DS, DNS, SYSVOL and NETLOGON readiness' -TimeoutSeconds 600 -Condition {
        foreach ($name in @('NTDS','DNS','Netlogon','ADWS')) {
            if ((Get-Service $name).Status -ne 'Running') { return $false }
        }
        if (-not (Get-SmbShare SYSVOL) -or -not (Get-SmbShare NETLOGON)) { return $false }
        Import-Module ActiveDirectory
        if ((Get-ADRootDSE -Server $script:LabDc).defaultNamingContext -ne $script:LabBaseDn) { return $false }
        Assert-LabDns
        return $true
    }
}

function Set-ReaderDenyAcl {
    param([Parameter(Mandatory)][string]$Dn, [Parameter(Mandatory)]$Sid)
    $path = "AD:\$Dn"
    $acl = Get-Acl -LiteralPath $path
    $rights = [DirectoryServices.ActiveDirectoryRights]'GenericWrite,CreateChild,DeleteChild,Delete,DeleteTree,ExtendedRight,WriteDacl,WriteOwner'
    $rule = [DirectoryServices.ActiveDirectoryAccessRule]::new($Sid, $rights,
        [Security.AccessControl.AccessControlType]::Deny,
        [DirectoryServices.ActiveDirectorySecurityInheritance]::All)
    $acl.SetAccessRule($rule)
    Set-Acl -LiteralPath $path -AclObject $acl
}

function Set-LabFixtures {
    $ouDn = "OU=LabFixtures,$script:LabBaseDn"
    $ou = Get-ADOrganizationalUnit -Filter "Name -eq 'LabFixtures'" -Properties Description -Server $script:LabDc
    if (-not $ou) {
        New-ADOrganizationalUnit -Name LabFixtures -Path $script:LabBaseDn -Description $ownership `
            -ProtectedFromAccidentalDeletion $true -Server $script:LabDc
    } elseif ($ou.DistinguishedName -ne $ouDn -or $ou.Description -ne $ownership) {
        throw 'An unowned fixture OU exists.'
    }
    foreach ($sam in @('lab-reader','lab-user','lab-disabled')) {
        $user = Get-ADUser -Filter "SamAccountName -eq '$sam'" -Properties Description -Server $script:LabDc
        if ($user -and ($user.DistinguishedName -ne "CN=$sam,$ouDn" -or $user.Description -ne "$ownership fixture")) {
            throw 'An unowned user collides with a fixture name.'
        }
        if (-not $user) {
            New-ADUser -Name $sam -SamAccountName $sam -UserPrincipalName "$sam@adtr.test" -Path $ouDn `
                -Description "$ownership fixture" -AccountPassword $ReaderPassword `
                -Enabled ($sam -ne 'lab-disabled') -PasswordNeverExpires $true `
                -CannotChangePassword $true -AccountNotDelegated $true -Server $script:LabDc
        } else {
            Set-ADAccountPassword -Identity $user -Reset -NewPassword $ReaderPassword -Server $script:LabDc
            Set-ADUser -Identity $user -Enabled ($sam -ne 'lab-disabled') -Server $script:LabDc
        }
    }
    $group = Get-ADGroup -Filter "SamAccountName -eq 'lab-group'" -Properties Description -Server $script:LabDc
    if ($group -and ($group.DistinguishedName -ne "CN=lab-group,$ouDn" -or $group.Description -ne $ownership)) {
        throw 'An unowned group collides with the fixture name.'
    }
    if (-not $group) {
        New-ADGroup -Name lab-group -SamAccountName lab-group -GroupCategory Security -GroupScope Global `
            -Path $ouDn -Description $ownership -Server $script:LabDc
    }
    Add-ADGroupMember -Identity lab-group -Members lab-user -Server $script:LabDc
    $reader = Get-ADUser lab-reader -Properties MemberOf,PrimaryGroupID -Server $script:LabDc
    if ($reader.PrimaryGroupID -ne 513 -or @($reader.MemberOf).Count -ne 0) {
        throw 'The synthetic reader must belong only to ordinary Domain Users.'
    }
    Set-ADObject -Identity $script:LabBaseDn -Replace @{'ms-DS-MachineAccountQuota' = 0; description = $ownership} -Server $script:LabDc
    # Do not confuse ordinary Domain Users with a read-only security boundary: deny writes explicitly.
    Set-ReaderDenyAcl -Dn $script:LabBaseDn -Sid $reader.SID
    Set-ReaderDenyAcl -Dn $ouDn -Sid $reader.SID
    foreach ($sam in @('lab-reader','lab-user','lab-disabled','lab-group')) {
        Set-ReaderDenyAcl -Dn "CN=$sam,$ouDn" -Sid $reader.SID
    }
}

function Set-LabCertificates {
    $rootSubject = "CN=$ownership"
    $roots = @(Get-ChildItem Cert:\LocalMachine\My | Where-Object Subject -eq $rootSubject)
    if ($roots.Count -gt 1) { throw 'Multiple run CA certificates exist; recreate this disposable guest.' }
    if ($roots.Count -eq 0) {
        $root = New-SelfSignedCertificate -Type Custom -Subject $rootSubject -FriendlyName "$ownership CA" `
            -CertStoreLocation Cert:\LocalMachine\My -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 `
            -KeyExportPolicy NonExportable -KeyUsage CertSign,CRLSign -NotBefore (Get-Date).AddMinutes(-5) `
            -NotAfter (Get-Date).AddHours(36) -TextExtension @('2.5.29.19={critical}{text}ca=true&pathlength=0')
    } else { $root = $roots[0] }
    if (-not $root.HasPrivateKey -or ($marker.RootCaThumbprint -and $marker.RootCaThumbprint -ne $root.Thumbprint)) {
        throw 'Run CA private key or recorded identity does not match.'
    }
    $marker.RootCaThumbprint = $root.Thumbprint
    Save-LabMarker $marker
    $rootPath = Join-Path $script:LabPublic 'root-ca.cer'
    Export-Certificate -Cert $root -FilePath $rootPath -Type CERT -Force | Out-Null
    $pem = "-----BEGIN CERTIFICATE-----`n" + [Convert]::ToBase64String($root.RawData, 'InsertLineBreaks') + "`n-----END CERTIFICATE-----`n"
    [IO.File]::WriteAllText((Join-Path $script:LabPublic 'root-ca.pem'), $pem, [Text.Encoding]::ASCII)
    Import-LabPublicCa -Path $rootPath -Sha256 (Get-LabCertificateSha256 $root) -LabId $LabId
    $leafName = "$ownership LDAPS"
    $leaves = @(Get-ChildItem Cert:\LocalMachine\My | Where-Object FriendlyName -eq $leafName)
    if ($leaves.Count -gt 1) { throw 'Multiple run LDAPS certificates exist.' }
    if ($leaves.Count -eq 0) {
        $leaf = New-SelfSignedCertificate -Type Custom -Subject "CN=$script:LabDc" -DnsName $script:LabDc `
            -FriendlyName $leafName -Signer $root -CertStoreLocation Cert:\LocalMachine\My `
            -Provider 'Microsoft RSA SChannel Cryptographic Provider' -KeySpec KeyExchange `
            -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 -KeyExportPolicy NonExportable `
            -KeyUsage DigitalSignature,KeyEncipherment -NotBefore (Get-Date).AddMinutes(-5) `
            -NotAfter (Get-Date).AddHours(24) -TextExtension @(
                '2.5.29.19={critical}{text}ca=false', '2.5.29.37={text}1.3.6.1.5.5.7.3.1')
    } else { $leaf = $leaves[0] }
    if (-not $leaf.HasPrivateKey -or ($marker.LeafThumbprint -and $marker.LeafThumbprint -ne $leaf.Thumbprint)) {
        throw 'LDAPS private key or recorded identity does not match.'
    }
    $competing = @(Get-ChildItem Cert:\LocalMachine\My | Where-Object {
        $_.Thumbprint -ne $leaf.Thumbprint -and $_.HasPrivateKey -and
        @($_.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.37' } |
            ForEach-Object { $_.EnhancedKeyUsages } | Where-Object Value -eq '1.3.6.1.5.5.7.3.1').Count -gt 0
    })
    if ($competing.Count -ne 0) { throw 'Unexpected server-authentication certificates could change LDAPS selection.' }
    $marker.LeafThumbprint = $leaf.Thumbprint
    Save-LabMarker $marker
    if (-not (Get-NetFirewallRule -Name 'ADTR-Lab-LDAPS' -ErrorAction SilentlyContinue)) {
        New-NetFirewallRule -Name 'ADTR-Lab-LDAPS' -DisplayName 'ADTR disposable lab LDAPS' `
            -Direction Inbound -Action Allow -Protocol TCP -LocalPort 636 `
            -RemoteAddress '192.168.77.0/24' -Profile Any | Out-Null
    }
}

function Test-ReaderWriteDenied {
    $user = Get-ADUser lab-user -Properties Description -Server $script:LabDc
    $before = $user.Description
    $connection = New-LabLdapConnection -Transport LDAPS -Password $ReaderPassword
    $denied = $false
    try {
        $change = [DirectoryServices.Protocols.DirectoryAttributeModification]::new()
        $change.Name = 'description'
        $change.Operation = [DirectoryServices.Protocols.DirectoryAttributeOperation]::Replace
        [void]$change.Add("ADTR write denial canary $LabId")
        $request = [DirectoryServices.Protocols.ModifyRequest]::new($user.DistinguishedName, @($change))
        try { [void]$connection.SendRequest($request) }
        catch [DirectoryServices.Protocols.DirectoryOperationException] {
            if ($null -ne $_.Exception.Response -and
                [int]$_.Exception.Response.ResultCode -eq 50) { $denied = $true }
            else { throw }
        }
    } finally { $connection.Dispose() }
    $after = (Get-ADUser lab-user -Properties Description -Server $script:LabDc).Description
    if (-not $denied -or $after -cne $before) {
        throw 'Read-only canary FAILED: write was not denied with LDAP code 50 or fixture changed. Destroy the lab.'
    }
}

function Export-LabGroundTruth {
    param([switch]$IncludeMember)
    $names = @('lab-reader','lab-user','lab-disabled','lab-group')
    if ($IncludeMember) { $names += 'member01$' }
    $fixtures = @(foreach ($sam in $names) {
        $objects = @(Get-ADObject -LDAPFilter "(sAMAccountName=$sam)" -SearchBase $script:LabBaseDn `
            -Properties sAMAccountName,objectGUID,userAccountControl -Server $script:LabDc)
        if ($objects.Count -ne 1) { throw "Expected one real AD fixture for $sam." }
        $object = $objects[0]
        $kind = if ($object.ObjectClass -eq 'computer') { 'computer' } elseif ($object.ObjectClass -eq 'group') { 'group' } else { 'user' }
        [pscustomobject]@{ sam = $object.sAMAccountName; guid = $object.ObjectGUID.ToString(); dn = $object.DistinguishedName;
            kind = $kind; disabled = ($null -ne $object.userAccountControl -and (([int]$object.userAccountControl -band 2) -ne 0)) }
    })
    Write-LabJson -Value $fixtures -Path (Join-Path $script:LabPublic 'fixtures.json')
    $root = Get-Item "Cert:\LocalMachine\My\$($marker.RootCaThumbprint)"
    $manifest = [ordered]@{ lab_id = $LabId.ToString(); domain = $script:LabDomain; dc = $script:LabDc;
        ip = $script:LabDcIp; ca = Join-Path $script:LabPublic 'root-ca.pem';
        root_ca_sha256 = Get-LabCertificateSha256 $root; fixtures = $fixtures }
    Write-LabJson -Value $manifest -Path (Join-Path $script:LabPublic 'manifest.json')
}

switch ($Stage) {
    Prepare {
        if ($marker.State -notin @('New','Prepared')) { throw 'Prepare cannot reset an already promoted lab.' }
        if ($marker.PendingBootId -eq (Get-LabBootId)) { Write-LabResult $marker $Stage $true; return }
        Set-LabNetwork -Address $script:LabDcIp
        $reboot = $env:COMPUTERNAME -ine 'dc01'
        if ($reboot) { Rename-Computer -NewName dc01 -Force | Out-Null }
        Set-LabStage $marker Prepared -NeedsReboot:$reboot
        Write-LabResult $marker $Stage $reboot
    }
    Promote {
        Assert-LabRebootComplete $marker
        Assert-LabNetwork -ExpectedAddress $script:LabDcIp -RequireConfigured | Out-Null
        if ($marker.State -eq 'Promoted') { Write-LabResult $marker $Stage; return }
        if ($marker.State -notin @('Prepared','FeatureInstalled') -or $env:COMPUTERNAME -ine 'dc01') {
            throw 'Prepare and any required reboot must finish before Promote.'
        }
        if (-not $DSRMPassword -or $DSRMPassword.Length -lt 16) { throw 'Supply a strong ephemeral DSRMPassword SecureString (at least 16 characters).' }
        if ((Get-CimInstance Win32_ComputerSystem).PartOfDomain) { throw 'Promotion requires a standalone owned guest.' }
        $feature = Install-WindowsFeature AD-Domain-Services -IncludeManagementTools
        if (-not $feature.Success) { throw 'AD DS feature installation failed.' }
        if ($feature.RestartNeeded -eq 'Yes') {
            Set-LabStage $marker FeatureInstalled -NeedsReboot
            Write-LabResult $marker $Stage $true
            return
        }
        Import-Module ADDSDeployment
        Set-LabStage $marker PromotionStarted
        # Keep all prerequisites enabled. The host owns reboots and supplies no production domain input.
        $promotion = Install-ADDSForest -DomainName adtr.test -DomainNetbiosName ADTR `
            -ForestMode WinThreshold -DomainMode WinThreshold -InstallDns:$true -CreateDnsDelegation:$false `
            -SafeModeAdministratorPassword $DSRMPassword -NoRebootOnCompletion:$true -Force:$true
        if ($promotion -and $promotion.Status -eq 'Error') { throw 'Forest promotion failed; recreate the lab guest.' }
        Set-LabStage $marker Promoted -NeedsReboot
        Write-LabResult $marker $Stage $true
    }
    Configure {
        Assert-LabRebootComplete $marker
        if ($marker.State -notin @('Promoted','Configured')) { throw 'Complete promotion and reboot before Configure.' }
        if (-not $ReaderPassword -or $ReaderPassword.Length -lt 20) { throw 'Supply a strong ephemeral ReaderPassword SecureString (at least 20 characters).' }
        Wait-LabDirectory
        $domain = Assert-OwnedForest
        $marker.DomainSid = $domain.DomainSID.Value
        Save-LabMarker $marker
        Set-DnsServerRecursion -Enable $false
        Set-LabFixtures
        Set-LabCertificates
        Export-LabGroundTruth
        Set-LabStage $marker Configured -NeedsReboot
        Write-LabResult $marker $Stage $true
    }
    { $_ -in @('Ready','Verify') } {
        Assert-LabRebootComplete $marker
        if ($marker.State -notin @('Configured','Ready','Verified')) { throw 'Configure and reboot before validation.' }
        if (-not $ReaderPassword) { throw 'ReaderPassword SecureString is required for authenticated readiness.' }
        Assert-LabNetwork -ExpectedAddress $script:LabDcIp -RequireConfigured | Out-Null
        Wait-LabDirectory
        Assert-OwnedForest | Out-Null
        Test-LabReaderTransports -Password $ReaderPassword
        Test-ReaderWriteDenied
        if ($Stage -eq 'Verify') {
            $member = Get-ADComputer member01 -Properties DNSHostName -Server $script:LabDc
            if ($member.DNSHostName -ne 'member01.adtr.test' -or -not $member.Enabled) {
                throw 'Expected enabled joined member01.adtr.test computer account.'
            }
            Set-ReaderDenyAcl -Dn $member.DistinguishedName -Sid (Get-ADUser lab-reader -Server $script:LabDc).SID
        }
        Export-LabGroundTruth -IncludeMember:($Stage -eq 'Verify')
        $state = if ($Stage -eq 'Verify') { 'Verified' } else { 'Ready' }
        Set-LabStage $marker $state
        $root = Get-Item "Cert:\LocalMachine\My\$($marker.RootCaThumbprint)"
        Write-LabResult $marker $Stage $false @{
            Dc = $script:LabDc; Address = $script:LabDcIp; PublicDirectory = $script:LabPublic
            RootCaThumbprint = $root.Thumbprint; RootCaSha256 = Get-LabCertificateSha256 $root
            Reader = 'lab-reader@adtr.test'; LDAPS = $true; StartTLS = $true; ReaderWriteDenied = $true
        }
    }
}
} catch {
    # Do not forward native AD/LDAP exceptions or serialized credentials into CI logs.
    throw [InvalidOperationException]::new("ADTR disposable DC stage '$Stage' failed. Inspect the isolated guest; do not upload raw diagnostics.")
}
