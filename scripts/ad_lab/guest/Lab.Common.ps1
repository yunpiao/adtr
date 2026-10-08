#requires -Version 5.1
# Shared helpers for freshly created, disposable Windows Server guests only.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:LabRoot = Join-Path $env:ProgramData 'ADTR-Lab'
$script:LabMarkerPath = Join-Path $script:LabRoot 'ownership.json'
$script:LabPublic = Join-Path $script:LabRoot 'public'
$script:LabDomain = 'adtr.test'
$script:LabBaseDn = 'DC=adtr,DC=test'
$script:LabDc = 'dc01.adtr.test'
$script:LabDcIp = '192.168.77.10'
$script:LabMemberIp = '192.168.77.20'

function Assert-LabRuntime {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run in an elevated Windows PowerShell session on the disposable guest.'
    }
    if ($PSVersionTable.PSEdition -ne 'Desktop' -or -not [Environment]::Is64BitProcess) {
        throw 'Use 64-bit Windows PowerShell 5.1, not PowerShell Core.'
    }
    $os = Get-CimInstance Win32_OperatingSystem
    if ($os.ProductType -eq 1 -or [int]$os.BuildNumber -notin @(20348, 26100)) {
        throw 'This baseline supports Windows Server 2022 (20348) or 2025 (26100) only.'
    }
}

function Get-LabBootId {
    (Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')
}

function Assert-LabNetwork {
    param([Parameter(Mandatory)][string]$ExpectedAddress, [switch]$RequireConfigured)
    $adapters = @(Get-NetAdapter | Where-Object Status -eq 'Up')
    if ($adapters.Count -ne 1) { throw 'Exactly one active network adapter is required.' }
    $nic = $adapters[0]
    $routes = @(Get-NetRoute -ErrorAction Stop | Where-Object {
        $_.DestinationPrefix -in @('0.0.0.0/0', '::/0')
    })
    if ($routes.Count -ne 0) { throw 'Default routes are forbidden in the isolated lab guests.' }
    if (@(Get-NetIPInterface | Where-Object Forwarding -eq 'Enabled').Count -ne 0) {
        throw 'IP forwarding is forbidden in the lab guests.'
    }
    $addresses = @(Get-NetIPAddress -AddressFamily IPv4 | Where-Object {
        $_.IPAddress -ne '127.0.0.1' -and $_.IPAddress -notlike '169.254.*'
    })
    if (@($addresses | Where-Object {
        $_.InterfaceIndex -ne $nic.ifIndex -or $_.IPAddress -ne $ExpectedAddress -or $_.PrefixLength -ne 24
    }).Count -ne 0) { throw 'Unexpected guest IP configuration; refusing to repurpose this machine.' }
    if ($RequireConfigured -and $addresses.Count -ne 1) { throw 'The expected static lab address is not configured.' }
    $globalV6 = @(Get-NetIPAddress -AddressFamily IPv6 | Where-Object {
        $_.IPAddress -ne '::1' -and $_.IPAddress -notlike 'fe80:*'
    })
    if ($globalV6.Count -ne 0) { throw 'Only link-local IPv6 is allowed in this baseline.' }
    return $nic
}

function Set-LabNetwork {
    param([Parameter(Mandatory)][string]$Address)
    $nic = Assert-LabNetwork -ExpectedAddress $Address
    Set-NetIPInterface -InterfaceIndex $nic.ifIndex -AddressFamily IPv4 -Dhcp Disabled | Out-Null
    if (-not (Get-NetIPAddress -InterfaceIndex $nic.ifIndex -AddressFamily IPv4 |
            Where-Object IPAddress -eq $Address)) {
        New-NetIPAddress -InterfaceIndex $nic.ifIndex -IPAddress $Address -PrefixLength 24 | Out-Null
    }
    Set-DnsClientServerAddress -InterfaceIndex $nic.ifIndex -ServerAddresses $script:LabDcIp
    Set-DnsClient -InterfaceIndex $nic.ifIndex -RegisterThisConnectionsAddress $true
    Assert-LabNetwork -ExpectedAddress $Address -RequireConfigured | Out-Null
}

function Set-LabPrivateDirectory {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { New-Item -ItemType Directory -Path $Path | Out-Null }
    if ((Get-Item -LiteralPath $Path).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw 'Lab state must not be a reparse point.'
    }
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18', 'S-1-5-32-544')) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl',
            'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Save-LabMarker {
    param([Parameter(Mandatory)]$Marker)
    $temporary = "$script:LabMarkerPath.tmp"
    $Marker | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $temporary -Encoding UTF8
    Move-Item -LiteralPath $temporary -Destination $script:LabMarkerPath -Force
}

function Get-LabMarker {
    param([Parameter(Mandatory)][guid]$LabId, [Parameter(Mandatory)][ValidateSet('DC','Member')][string]$Role,
          [switch]$Initialize)
    if ($LabId -eq [guid]::Empty) { throw 'LabId must be a fresh nonempty run identifier.' }
    $computer = Get-CimInstance Win32_ComputerSystem
    $machineId = (Get-CimInstance Win32_ComputerSystemProduct).UUID
    if (-not (Test-Path -LiteralPath $script:LabMarkerPath)) {
        if (-not $Initialize) { throw 'Missing disposable-lab ownership marker. Start with Prepare.' }
        if ($computer.PartOfDomain -or $computer.DomainRole -ge 4 -or
                (Get-Service NTDS -ErrorAction SilentlyContinue)) {
            throw 'Refusing to adopt an existing domain or domain controller.'
        }
        if (Test-Path -LiteralPath $script:LabRoot) {
            throw 'Unowned ADTR-Lab directory already exists. Recreate the disposable guest.'
        }
        Set-LabPrivateDirectory -Path $script:LabRoot
        Set-LabPrivateDirectory -Path $script:LabPublic
        $marker = [pscustomobject]@{
            Schema = 1; LabId = $LabId.ToString(); MachineId = $machineId; Role = $Role
            Domain = $script:LabDomain; State = 'New'; PendingBootId = ''; DomainSid = ''
            RootCaThumbprint = ''; LeafThumbprint = ''; CreatedUtc = [DateTime]::UtcNow.ToString('o')
        }
        Save-LabMarker $marker
    }
    if ((Get-Item -LiteralPath $script:LabMarkerPath).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw 'Ownership marker must not be a reparse point.'
    }
    $marker = Get-Content -LiteralPath $script:LabMarkerPath -Raw | ConvertFrom-Json
    if ($marker.Schema -ne 1 -or $marker.LabId -ne $LabId.ToString() -or
            $marker.MachineId -ne $machineId -or $marker.Role -ne $Role -or $marker.Domain -ne $script:LabDomain) {
        throw 'Lab ownership does not match this run, guest, or role.'
    }
    if ($computer.PartOfDomain -and $computer.Domain -ne $script:LabDomain) {
        throw 'Refusing to operate on a non-lab domain.'
    }
    if ($Role -eq 'Member' -and $computer.DomainRole -ge 4) { throw 'Member guest must not be a DC.' }
    return $marker
}

function Assert-LabRebootComplete {
    param([Parameter(Mandatory)]$Marker)
    if ($Marker.PendingBootId -and $Marker.PendingBootId -eq (Get-LabBootId)) {
        throw 'A host-controlled reboot is required before this stage.'
    }
}

function Set-LabStage {
    param([Parameter(Mandatory)]$Marker, [Parameter(Mandatory)][string]$State, [switch]$NeedsReboot)
    $Marker.State = $State
    $Marker.PendingBootId = if ($NeedsReboot) { Get-LabBootId } else { '' }
    Save-LabMarker $Marker
}

function Write-LabResult {
    param([Parameter(Mandatory)]$Marker, [Parameter(Mandatory)][string]$Stage,
          [bool]$RebootRequired = $false, [hashtable]$Extra = @{})
    $result = [ordered]@{ LabId = $Marker.LabId; Role = $Marker.Role; Stage = $Stage;
        State = $Marker.State; RebootRequired = $RebootRequired; Domain = $script:LabDomain }
    foreach ($key in $Extra.Keys) { $result[$key] = $Extra[$key] }
    [pscustomobject]$result
}

function Wait-LabCondition {
    param([Parameter(Mandatory)][scriptblock]$Condition, [Parameter(Mandatory)][string]$Description,
          [int]$TimeoutSeconds = 300)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        try { if (& $Condition) { return } } catch { # Readiness errors are retried, never reported as success.
        }
        Start-Sleep -Seconds 5
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Timed out waiting for $Description. Inspect the disposable guest locally."
}

function Assert-LabDns {
    $answers = @(Resolve-DnsName -Name $script:LabDc -Type A -Server $script:LabDcIp -DnsOnly |
        Where-Object Type -eq 'A')
    if ($answers.Count -ne 1 -or $answers[0].IPAddress -ne $script:LabDcIp) {
        throw 'The lab DC DNS record does not match the isolated address.'
    }
    $srv = @(Resolve-DnsName -Name '_ldap._tcp.dc._msdcs.adtr.test' -Type SRV -Server $script:LabDcIp -DnsOnly |
        Where-Object Type -eq 'SRV')
    if ($srv.Count -ne 1 -or $srv[0].NameTarget.TrimEnd('.') -ne $script:LabDc) {
        throw 'Expected exactly the owned lab DC in domain SRV records.'
    }
}

function New-LabLdapConnection {
    param([Parameter(Mandatory)][ValidateSet('LDAPS','StartTLS')][string]$Transport,
          [Parameter(Mandatory)][securestring]$Password,
          [string]$User = 'lab-reader@adtr.test')
    Add-Type -AssemblyName System.DirectoryServices.Protocols
    $port = if ($Transport -eq 'LDAPS') { 636 } else { 389 }
    $identifier = [DirectoryServices.Protocols.LdapDirectoryIdentifier]::new($script:LabDc, $port, $true, $false)
    $connection = [DirectoryServices.Protocols.LdapConnection]::new($identifier)
    try {
        $connection.Timeout = [TimeSpan]::FromSeconds(20)
        $connection.SessionOptions.ProtocolVersion = 3
        $connection.SessionOptions.ReferralChasing = [DirectoryServices.Protocols.ReferralChasingOptions]::None
        $connection.AuthType = [DirectoryServices.Protocols.AuthType]::Basic
        $connection.Credential = [Net.NetworkCredential]::new($User, $Password)
        # Deliberately use native chain and hostname validation. No certificate callback/bypass.
        if ($Transport -eq 'LDAPS') { $connection.SessionOptions.SecureSocketLayer = $true }
        else { $connection.SessionOptions.StartTransportLayerSecurity($null) }
        $connection.Bind()
        return $connection
    } catch { $connection.Dispose(); throw }
}

function Test-LabReaderTransports {
    param([Parameter(Mandatory)][securestring]$Password)
    foreach ($transport in @('LDAPS','StartTLS')) {
        $connection = New-LabLdapConnection -Transport $transport -Password $Password
        try {
            $request = [DirectoryServices.Protocols.SearchRequest]::new(
                $script:LabBaseDn, '(sAMAccountName=lab-user)',
                [DirectoryServices.Protocols.SearchScope]::Subtree, @('objectGUID','distinguishedName'))
            $response = [DirectoryServices.Protocols.SearchResponse]$connection.SendRequest($request)
            if ($response.Entries.Count -ne 1) { throw "$transport fixture lookup did not return exactly one user." }
        } finally { $connection.Dispose() }
    }
}

function Import-LabPublicCa {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Sha256,
          [Parameter(Mandatory)][guid]$LabId)
    if ($Sha256 -notmatch '^[a-fA-F0-9]{64}$') { throw 'Expected a pinned SHA-256 public root fingerprint.' }
    $certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new($Path)
    try {
        if ($certificate.HasPrivateKey -or (Get-LabCertificateSha256 $certificate) -ne $Sha256 -or
                $certificate.Subject -ne "CN=ADTR disposable lab $LabId" -or
                $certificate.Issuer -ne $certificate.Subject -or
                $certificate.NotAfter -lt (Get-Date) -or $certificate.NotBefore -gt (Get-Date) -or
                ($certificate.NotAfter - $certificate.NotBefore).TotalHours -gt 48) {
            throw 'Root certificate does not match the public CA pinned for this disposable run.'
        }
        $constraints = @($certificate.Extensions | Where-Object { $_.Oid.Value -eq '2.5.29.19' })
        if ($constraints.Count -ne 1 -or -not $constraints[0].CertificateAuthority) {
            throw 'Expected a CA certificate.'
        }
        $store = [Security.Cryptography.X509Certificates.X509Store]::new('Root', 'LocalMachine')
        try { $store.Open('ReadWrite'); $store.Add($certificate) } finally { $store.Close() }
    } finally { $certificate.Dispose() }
}

function Get-LabCertificateSha256 {
    param([Parameter(Mandatory)]$Certificate)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash($Certificate.RawData))).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose() }
}

function Write-LabJson {
    param([Parameter(Mandatory)]$Value, [Parameter(Mandatory)][string]$Path)
    # Windows PowerShell's Set-Content -Encoding UTF8 writes a BOM; Go JSON does not accept it.
    $json = ConvertTo-Json -InputObject $Value -Depth 8
    [IO.File]::WriteAllText($Path, $json, [Text.UTF8Encoding]::new($false))
}
