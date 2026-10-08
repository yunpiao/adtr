"""Portable source contracts ONLY: no PowerShell parser, HTTP request, or Windows runtime."""
from pathlib import Path
import re
import unittest

HERE = Path(__file__).parent
TEXT = (HERE / "Get-EvaluationMedia.ps1").read_text()
INSPECTOR = (HERE.parent / "Inspect-EvaluationMedia.ps1").read_text()

def body(name):
    found = re.search(r"(?ms)^function " + re.escape(name) + r"(?:\([^\n]*\))? \{(.*?)(?=^function |^# Preflight|\Z)", TEXT)
    assert found, name
    return found.group(1)

class PinnedDownloadSourceContract(unittest.TestCase):
    def test_fixed_inspected_media(self):
        for token in ("8152356864", "7b052573ba7894c9924e3e87ba732ccd354d18cb75a883efa9b900ea125bfd51",
                      "26100.32230.260111-0550.lt_release_svc_refresh_SERVER_EVAL_x64FRE_en-us.iso",
                      "https://go.microsoft.com/fwlink/?linkid=2345730&clcid=0x409&culture=en-us&country=us",
                      "https://aka.ms/WinServ2025iso-enus"):
            self.assertIn(token, TEXT)
        params = TEXT[TEXT.index("param("):TEXT.index("Set-StrictMode")]
        for caller_override in ("$Uri", "$Url", "$ExpectedBytes", "$ExpectedSha", "$Timeout", "$Maximum"):
            self.assertNotIn(caller_override, params)

    def test_inspector_network_boundaries_preserved_and_tightened(self):
        for token in ("8500000000", "$downloadSeconds = 600", "301,302,303,307,308",
                      "application/octet-stream", "application/x-iso9660-image"):
            self.assertIn(token, TEXT)
            self.assertIn(token, INSPECTOR)
        uri = body("Assert-DownloadOfficialUri")
        for token in ("$Uri.Scheme -cne 'https'", "$Uri.Port -ne 443", "$Uri.UserInfo", "$Uri.Fragment",
                      "$knownAlias", "$Uri.AbsoluteUri -cnotin @($sourceUrl,$aliasUrl,$inspectedUrl)"):
            self.assertIn(token, uri)
        self.assertIn("$hop -le 4", body("Get-DownloadResponse"))

    def test_head_and_get_require_exact_headers(self):
        headers = body("Assert-DownloadIsoHeaders")
        for token in ("$Result.Uri.AbsoluteUri -cne $inspectedUrl", "$length -ne $expectedBytes",
                      "$length -gt $maxDownloadBytes", "ContentEncoding).Count -ne 0"):
            self.assertIn(token, headers)
        receive = body("Receive-PinnedDownload")
        self.assertIn("Get-DownloadResponse $client 'HEAD' ([uri]$sourceUrl)", receive)
        self.assertIn("Get-DownloadResponse $client 'GET' ([uri]$inspectedUrl)", receive)
        self.assertEqual(receive.count("Assert-DownloadIsoHeaders"), 2)

    def test_http_has_no_credentials_cookies_proxy_or_auto_redirect(self):
        receive = body("Receive-PinnedDownload")
        for token in ("AllowAutoRedirect = $false", "UseCookies = $false", "UseDefaultCredentials = $false",
                      "Credentials = $null", "PreAuthenticate = $false", "UseProxy = $false",
                      "AutomaticDecompression = [Net.DecompressionMethods]::None"):
            self.assertIn(token, receive)
        self.assertNotIn("ServerCertificateValidationCallback", TEXT)
        self.assertNotIn("Authorization", TEXT)

    def test_all_network_and_file_io_tasks_share_one_deadline(self):
        wait = body("Wait-DownloadTask")
        self.assertIn("($downloadSeconds * 1000) - $clock.ElapsedMilliseconds", wait)
        self.assertIn("$Task.Wait($remainingMs)", wait)
        self.assertIn("$cts.Cancel()", wait)
        receive = body("Receive-PinnedDownload")
        for token in ("Wait-DownloadTask ($inputStream.ReadAsync", "Wait-DownloadTask ($outputStream.WriteAsync",
                      "Wait-DownloadTask ($outputStream.FlushAsync", "[TimeSpan]::FromSeconds($downloadSeconds)"):
            self.assertIn(token, receive)
        self.assertIn("Wait-DownloadTask ($Client.SendAsync", body("Get-DownloadResponse"))

    def test_size_and_hash_before_final_name_and_use(self):
        receive = body("Receive-PinnedDownload")
        for token in ("$total -gt $maxDownloadBytes -or $total -gt $length", "$total -ne $expectedBytes",
                      "$hash -cne $expectedSha256", "[IO.FileMode]::CreateNew", "[IO.FileShare]::None"):
            self.assertIn(token, receive)
        self.assertLess(receive.index("$hash -cne $expectedSha256"), receive.index("[IO.File]::Move($partialPath,$isoPath)"))
        self.assertLess(receive.index("[IO.File]::Move($partialPath,$isoPath)"), receive.index("$script:state.Stage = 'Verified'"))

    def test_unique_caller_identity_and_local_temp_path(self):
        for token in ("[Parameter(Mandatory)][guid]$DownloadId", "[Parameter(Mandatory)][string]$DownloadRoot",
                      "$DownloadId -eq [guid]::Empty", '"adtr-eval-download-$id"',
                      "[IO.Path]::GetFullPath($DownloadRoot).TrimEnd('\\') -cne $root",
                      "Local\\ADTR-EvalDownload-$runKey-$id"):
            self.assertIn(token, TEXT)
        self.assertIn("$saved.SourceSha -cne $SourceSha", body("Read-DownloadState"))
        self.assertIn("$saved.RunKey -cne $runKey", body("Read-DownloadState"))

    def test_preexisting_paths_are_never_adopted(self):
        self.assertIn("A new download never overwrites or adopts an existing reservation.", TEXT)
        self.assertIn("Never overwrite or adopt an existing ISO.", body("Receive-PinnedDownload"))
        self.assertIn("if (-not $saved.RootCreated)", body("Remove-OwnedDownload"))
        self.assertLess(TEXT.index("Write-DownloadState # Exact ownership"), TEXT.index("New-Item -ItemType Directory -Path $root"))
        receive = body("Receive-PinnedDownload")
        self.assertLess(receive.index("[IO.FileStream]::new($partialPath"), receive.index("$script:state.FileCreated = $true"))

    def test_own_cleanup_never_recurses_or_removes_unknown_files(self):
        cleanup = body("Remove-OwnedDownload")
        for token in ("$entry.PSIsContainer", "$entry.FullName -notin @($partialPath,$isoPath)",
                      "-not $saved.FileCreated", "Assert-DownloadNoReparse $entry.FullName"):
            self.assertIn(token, cleanup)
        self.assertNotIn("-Recurse", TEXT)
        self.assertNotIn("Dismount-", TEXT)
        self.assertIn("$disk[0].Attached", cleanup)
        self.assertLess(cleanup.index("$disk[0].Attached"), cleanup.index("Remove-Item -LiteralPath $file"))

    def test_active_matching_process_blocks_cleanup(self):
        cleanup = body("Remove-OwnedDownload")
        for token in ("if ($saved.Active)", "Get-Process -Id $saved.ProcessId", "$saved.ProcessStartTicks"):
            self.assertIn(token, cleanup)
        self.assertLess(cleanup.index("if ($saved.Active)"), cleanup.index("Remove-Item"))
        receive = body("Receive-PinnedDownload")
        self.assertLess(receive.index("$script:state.Active = $true"), receive.index("Get-DownloadResponse"))
        self.assertIn("if ($closed) { $script:state.Active = $false; Write-DownloadState }", receive)

    def test_host_cleanup_dependency_is_readonly_bounded_fail_closed(self):
        guard = body("Assert-DownloadHostDependency")
        for token in ("adtr-real-ad-state.json", "Length -gt 131072", "$hostState.RunKey -cne $runKey",
                      "$hostState.SourceSha -cne $SourceSha", "$mediaProperty.Value.IsoPath -ieq $isoPath",
                      "-not $hostState.CleanupVerified", "$media.NativeOperationUnresolved", "$media.IsoMountStarted",
                      "$media.WimMountStarted", "$media.BaseMountStarted"):
            self.assertIn(token, guard)
        for forbidden in ("Write-", "Set-Content", "Remove-Item", "Dismount-"):
            self.assertNotIn(forbidden, guard)
        cleanup = body("Remove-OwnedDownload")
        self.assertLess(cleanup.index("Assert-DownloadHostDependency"), cleanup.index("Remove-Item"))

    def test_no_lab_or_legal_execution(self):
        for forbidden in ("Mount-DiskImage", "Mount-WindowsImage", "New-VHD", "New-VM", "Start-VM",
                          "AcceptEula", "HideEULAPage", "AdministratorPassword", "Invoke-WebRequest",
                          "Start-Process", "Set-ExecutionPolicy", "Install-WindowsFeature"):
            self.assertNotIn(forbidden, TEXT)
        self.assertNotIn("Host.Common.ps1", TEXT)
        self.assertNotIn("Inspect-EvaluationMedia.ps1'", TEXT)

    def test_success_contract_and_parse_only_inclusion(self):
        self.assertIn("[pscustomobject]@{ IsoPath=$isoPath; IsoSha256=$expectedSha256; Bytes=$expectedBytes; DownloadId=$id; DownloadRoot=$root }", TEXT)
        self.assertEqual(TEXT.count("IsoPath=$isoPath; IsoSha256="), 1)
        self.assertIn("'Get-EvaluationMedia.ps1'", (HERE / "Test-HostScriptSyntax.ps1").read_text())
        self.assertNotIn("Write-Host", TEXT)
        self.assertNotIn("Write-Output", TEXT)

if __name__ == "__main__":
    unittest.main()
