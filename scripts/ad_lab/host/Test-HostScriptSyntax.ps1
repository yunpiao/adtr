#requires -Version 5.1
<# Parse-only. Does not dot-source or execute any controller/guest code. #>
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSEdition -ne 'Desktop' -or $PSVersionTable.PSVersion.Major -ne 5) {
    throw 'Validate this baseline with Windows PowerShell 5.1.'
}
$files = @('Host.Common.ps1','Image.Common.ps1','Get-EvaluationMedia.ps1','Invoke-HostedLab.ps1','Invoke-MemberTests.ps1','Remove-HostedLab.ps1')
foreach ($name in $files) {
    $tokens = $null; $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name), [ref]$tokens, [ref]$errors)
    if ($errors.Count) {
        foreach ($parseError in $errors) {
            Write-Output "$name line $($parseError.Extent.StartLineNumber): $($parseError.Message)"
        }
        throw 'Host script syntax validation failed.'
    }
    $parameters = $ast.FindAll({ param($node)
        $node -is [Management.Automation.Language.ParameterAst]
    }, $true)
    foreach ($parameter in $parameters) {
        if ($parameter.Name.VariablePath.UserPath -like '*Password' -and $parameter.StaticType -ne [securestring]) {
            throw "Password input in $name must be SecureString."
        }
    }
}
Write-Output 'Host scripts parsed and password input types checked; no Hyper-V or AD runtime was exercised.'
