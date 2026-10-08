#requires -Version 5.1
<# Parse only. Safe on a GitHub-hosted Windows runner; never executes bootstrap code. #>
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$files = @('Lab.Common.ps1','Initialize-LabDomainController.ps1','Initialize-LabMember.ps1')
foreach ($name in $files) {
    $tokens = $null
    $errors = $null
    $path = Join-Path $PSScriptRoot $name
    $ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) {
        foreach ($parseError in $errors) {
            Write-Output "$name line $($parseError.Extent.StartLineNumber): $($parseError.Message)"
        }
        throw 'Guest script syntax validation failed.'
    }
    if ($name -ne 'Lab.Common.ps1') {
        foreach ($parameter in $ast.ParamBlock.Parameters) {
            if ($parameter.Name.VariablePath.UserPath -like '*Password' -and
                    $parameter.StaticType -ne [securestring]) {
                throw "Password parameter in $name is not SecureString."
            }
        }
    }
}
Write-Output 'Guest script parsing and password parameter types passed; no AD DS runtime was exercised.'
