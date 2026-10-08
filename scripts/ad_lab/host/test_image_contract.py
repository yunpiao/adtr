"""Portable source contracts only. No PowerShell, Windows, Hyper-V, or AD execution."""
from pathlib import Path
import re
import unittest

HERE = Path(__file__).parent
TEXT = (HERE / "Image.Common.ps1").read_text()

def body(name):
    match = re.search(r"(?ms)^function " + re.escape(name) + r" \{(.*?)(?=^function |\Z)", TEXT)
    assert match, name
    return match.group(1)

class EvaluationImageSourceContract(unittest.TestCase):
    def test_load_is_definition_only(self):
        prefix = TEXT[:TEXT.index("function ")]
        self.assertNotRegex(prefix, r"Mount-|New-VHD|Initialize-Disk|Format-Volume|Start-VM")
        self.assertNotIn("Start-VM", TEXT)

    def test_license_gate_is_required_and_bound_before_install(self):
        code = body("New-HostedLabEvaluationBase")
        for token in ("[Parameter(Mandatory)][switch]$LicenseAcceptanceConfirmed",
                      "if (-not $LicenseAcceptanceConfirmed)", "[string]$LicenseRelativePath",
                      "/ReadOnly /CheckIntegrity", "$media.LicenseBytesVerified = $true",
                      "Get-EvaluationLicenseSha256 $s", "$actualLicenseSha256 -cne $LicenseTermsSha256"):
            self.assertIn(token, code)
        self.assertLess(code.index("if (-not $LicenseAcceptanceConfirmed)"), code.index("Mount-DiskImage"))
        self.assertLess(code.index("$media.LicenseBytesVerified = $true"), code.index("New-VHD"))
        self.assertLess(code.index("$media.LicenseBytesVerified = $true"), code.index(" /Apply-Image"))

    def test_reparse_gate_matches_reviewed_inspector_pattern(self):
        inspector = (HERE.parent / "Inspect-EvaluationMedia.ps1").read_text()
        image = body("Initialize-EvaluationReparseReader")
        for name in ("IsKnownWimDataTag", "CanReadWimDataLeaf"):
            pattern = r"public static bool " + name + r"\([^)]*\) \{(.*?)\}"
            old = re.search(pattern, inspector, re.S)
            new = re.search(pattern, image, re.S)
            self.assertIsNotNone(old)
            self.assertIsNotNone(new)
            self.assertEqual(re.sub(r"\s+", "", old.group(1)), re.sub(r"\s+", "", new.group(1)))
        self.assertIn("namespace AdtrHostedImage", image)
        self.assertIn("FindFirstFileW(path, out data)", image)
        self.assertIn("Tag=data.Reserved0", image)
        self.assertNotIn("DeviceIoControl", image)
        self.assertNotIn("CreateFileW", image)

    def test_license_data_exception_is_leaf_only_under_live_readonly_mount(self):
        read = body("Get-EvaluationLicenseSha256")
        for token in ("Assert-EvaluationMediaReservation $State", "if ($media.NativeOperationUnresolved)",
                      "Assert-NoReparse $base", "$current = $base", "$isLicenseLeaf = $current -ieq $license",
                      "$info.Tag,$info.IsDirectory,$isLicenseLeaf,$ownedReadOnlyWim",
                      "[IO.Path]::GetFileName($license) -cne 'license.rtf'", "$leaf.Length -gt 2MB"):
            self.assertIn(token, read)
        self.assertEqual(read.count("Get-EvaluationReadOnlyWimMount $State"), 2)
        self.assertLess(read.index("Get-EvaluationReadOnlyWimMount $State"), read.index("foreach ($component"))
        self.assertLess(read.index("foreach ($component"), read.index("-LiteralPath $license -Algorithm SHA256"))
        self.assertGreater(read.rindex("Get-EvaluationReadOnlyWimMount $State"),
                           read.index("-LiteralPath $license -Algorithm SHA256"))
        self.assertNotIn("-Recurse", read)
        self.assertNotIn("[string]$Path", read)
        # The general-purpose guard remains untouched; the exception is not a new guard mode.
        common = (HERE / "Host.Common.ps1").read_text()
        self.assertNotIn("CanReadWimDataLeaf", common)
        self.assertNotIn("AllowWimData", common)

    def test_data_tag_runtime_gate_covers_negative_scopes_and_namesurrogates(self):
        code = body("Initialize-EvaluationReparseReader")
        self.assertIn("0x80000008U || tag == 0x80000017U", code)
        self.assertIn("(tag & 0x20000000U) == 0", code)
        for token in ("$tag,$false,$true,$true", "$tag,$true,$true,$true",
                      "$tag,$false,$false,$true", "$tag,$false,$true,$false",
                      "'A0000003'", "'A000000C'", "'A0000008'", "'A0000017'", "'80000009'", "'80000018'", "'00000000'"):
            self.assertIn(token, code)

    def test_license_path_is_bound_to_selected_edition(self):
        self.assertIn(r"Licenses\\[A-Za-z0-9_-]+\\ServerStandardEval\\license\.rtf$", TEXT)
        self.assertNotIn(r"(?:[A-Za-z0-9_-]+\\){1,3}", TEXT)

    def test_cleanup_allows_owned_readonly_offline_disks(self):
        self.assertIn("(-not $ForCleanup -and ($disks[0].IsReadOnly -or $disks[0].IsOffline))",
                      body("Assert-EvaluationOwnedVhd"))
        self.assertIn("Assert-EvaluationOwnedVhd $s $role -RequireMounted -ForCleanup",
                      body("Remove-HostedLabImageMounts"))

    def test_source_and_selected_index_are_pinned(self):
        code = body("New-HostedLabEvaluationBase")
        for token in ("'ServerStandardEval'", "'Server Core'", "'x64'", "'ServerNT'",
                      "10\\.0\\.26100", "en-US", "'/Index:'", "-cne $IsoSha256"):
            # The selector occurs after another argument, rather than as a bare quoted token.
            self.assertIn(token.replace("\"", "") if token != "'/Index:'" else "/Index:", code)
        self.assertIn("-TimeoutSeconds 120 -ReturnMetadata", code)
        self.assertIn("$s.SourceSha -cne $SourceSha", body("Assert-EvaluationImageContext"))

    def test_only_owned_dynamic_disk_and_reverse_mapping(self):
        code = body("Assert-EvaluationOwnedVhd")
        for token in ("$media.BasePath", "$machine.DiskPath", "$vhd.Size -ne 40GB",
                      "$vhd.VhdType -ne 'Dynamic'", "$vhd.VhdType -ne 'Differencing'",
                      "$vhd.ParentPath -ine $media.BasePath", "$vhd | Get-Disk",
                      "Get-VHD -DiskNumber $disks[0].Number", "$disks[0].IsBoot", "$disks[0].IsSystem",
                      "[int]$disks[0].BusType -ne 15", "$back[0].Path -ine $path"):
            self.assertIn(token, code)
        self.assertNotRegex(TEXT, r"param\([^\n]*(?:DiskNumber|DriveLetter|PartitionNumber)")

    def test_reserve_before_create_and_mount(self):
        code = body("New-HostedLabEvaluationBase")
        for first, second in (("Write-HostJson $s $script:StatePath # Reserve", "Set-HostPrivateDirectory $directory"),
                              ("$media.IsoMountStarted = $true", "Mount-DiskImage"),
                              ("$media.WimMountStarted = $true", " /Mount-Image"),
                              ("$media.BaseCreateStarted = $true", "New-VHD"),
                              ("$media.BaseMountStarted = $true", "Mount-VHD")):
            self.assertLess(code.index(first), code.index(second))
        child = body("Set-HostedLabChildUnattend")
        self.assertLess(child.index("$preparation.MountStarted = $true"), child.index("Mount-VHD"))

    def test_partition_mutations_are_guarded(self):
        code = body("New-HostedLabEvaluationBase")
        self.assertIn("$disk.PartitionStyle -ne 'RAW'", code)
        self.assertEqual(code.count("$disk = Assert-EvaluationOwnedVhd $s Base -RequireMounted"), 4)
        self.assertIn("Get-EvaluationPartition $s Base EFI | Format-Volume", code)
        self.assertIn("Get-EvaluationPartition $s Base Windows | Format-Volume", code)
        part = body("Get-EvaluationPartition")
        for token in ("$parts.Count -ne 3", "$selected[0].Guid -ine $guid", "$selected[0].DiskNumber -ne $disk.Number"):
            self.assertIn(token, part)

    def test_bcdboot_never_implicitly_selects_host_store(self):
        code = body("New-HostedLabEvaluationBase")
        self.assertIn("'Windows\" /s '", code)
        self.assertIn("$efiRoot.TrimEnd('\\') + ' /f UEFI /c'", code)
        self.assertIn("$efiRoot = Get-EvaluationVolumeRoot $s Base EFI", code)
        self.assertNotIn("bcdedit", TEXT.lower())
        self.assertNotIn(" /addlast", TEXT)
        self.assertNotIn(" /p'", TEXT)

    def test_credentials_only_in_child_secure_input(self):
        self.assertNotIn("$AdministratorPassword", body("New-HostedLabEvaluationBase"))
        child = body("Set-HostedLabChildUnattend")
        for token in ("[securestring]$AdministratorPassword", "SecureStringToGlobalAllocUnicode",
                      "ZeroFreeGlobalAllocUnicode", "Set-Acl -LiteralPath $answer", "[IO.FileMode]::CreateNew",
                      "$writer.WriteElementString('Value',$ns,$plain)", "$plain = $null"):
            self.assertIn(token, child)
        self.assertLess(child.index("Set-Acl -LiteralPath $answer"), child.index("SecureStringToGlobalAllocUnicode"))
        self.assertNotIn("Write-Output", child)
        self.assertNotIn("AutoLogon", TEXT)
        self.assertNotIn("SkipMachineOOBE", TEXT)

    def test_cleanup_is_exact_and_does_not_commit_wim(self):
        cleanup = body("Remove-EvaluationReadOnlyWimMount")
        self.assertIn(" /Discard /LogLevel:1", cleanup)
        self.assertIn("-TimeoutSeconds 180 -ForCleanup", cleanup)
        self.assertNotIn(" /Commit", TEXT)
        mounts = body("Remove-HostedLabImageMounts")
        self.assertIn("Dismount-VHD -Path $path", mounts)
        self.assertIn("Dismount-DiskImage -ImagePath $media.IsoPath", mounts)
        self.assertIn("Get-VMHardDiskDrive -VM $vm", mounts)
        self.assertIn("Remove-EvaluationReadOnlyWimMount $s", mounts)

    def test_post_boot_purge_requires_completed_owned_guest(self):
        code = body("Get-HostedLabSetupCleanupCommand")
        for token in ("$setup.SystemSetupInProgress -ne 0", "$setup.OOBEInProgress -ne 0",
                      "$marker.LabId -cne $LabId", "$marker.Role -cne $Role", "Assert-SetupNoReparse",
                      "Panther\\unattend.xml", "Panther\\UnattendGC\\setupact.log", "Purged=$true"):
            self.assertIn(token, code)
        self.assertNotIn("-Recurse", code)

    def test_ambiguous_native_exit_prevents_cleanup_success(self):
        native = body("Invoke-EvaluationNative")
        self.assertLess(native.index("$media.NativeOperationUnresolved = $true"), native.index("$process.Start()"))
        self.assertLess(native.index("$process.ExitCode -ne 0"), native.index("$resolved.Media.NativeOperationUnresolved = $false"))
        cleanup = body("Remove-HostedLabImageMounts")
        self.assertIn("if ($media.NativeOperationUnresolved)", cleanup)
        self.assertLess(cleanup.index("if ($media.NativeOperationUnresolved)"), cleanup.index("Dismount-VHD"))

    def test_no_network_or_external_software_changes(self):
        for forbidden in ("Invoke-WebRequest", "Invoke-RestMethod", "New-NetNat", "Set-NetIPInterface",
                          "New-NetFirewallRule", "Enable-PSRemoting", "Install-WindowsFeature", "DownloadFile",
                          "Start-Transcript", "Set-VM", "New-VM", "Clear-Disk", "diskpart"):
            self.assertNotIn(forbidden, TEXT)

if __name__ == "__main__":
    unittest.main()
