"""Portable source-regression checks ONLY: not PowerShell or Hyper-V execution."""
from pathlib import Path
import re
import unittest

HERE = Path(__file__).parent
HOST = (HERE / 'Invoke-HostedLab.ps1').read_text()
COMMON = (HERE / 'Host.Common.ps1').read_text()
MEMBER = (HERE / 'Invoke-MemberTests.ps1').read_text()
CLEANUP = (HERE / 'Remove-HostedLab.ps1').read_text()
ALL = HOST + COMMON + MEMBER + CLEANUP


class HostSourceContract(unittest.TestCase):
    def test_hosted_approved_events_only(self):
        for token in ("$env:RUNNER_ENVIRONMENT -ne 'github-hosted'", "$env:GITHUB_EVENT_NAME -notin @('workflow_dispatch','pull_request')",
                      "$env:GITHUB_REPOSITORY -ne 'yunpiao/adtr'", "$env:RUNNER_OS -ne 'Windows'"):
            self.assertIn(token, COMMON)

    def test_fixed_clean_source_and_workflow(self):
        for token in ('rev-parse HEAD', 'status --porcelain', 'Assert-PinnedJobTimeout'):
            self.assertIn(token, HOST)
        self.assertIn('$env:GITHUB_WORKFLOW_SHA -cne $SourceSha', COMMON)
        self.assertIn('$minutes -gt $LeaseMinutes', COMMON)

    def test_pr_merge_tree_must_equal_task_head(self):
        for token in ("$env:GITHUB_SHA -cne $SourceSha", "$env:GITHUB_WORKFLOW_SHA -cne $SourceSha",
                      '$ExpectedPullRequestHeadSha', '$ExpectedPullRequestHeadRef', '$ExpectedPullRequestNumber',
                      '$event.pull_request.head.repo.full_name', '$event.pull_request.base.repo.full_name',
                      '$event.pull_request.head.sha -cne $ExpectedPullRequestHeadSha',
                      '$ids[1] -cne $event.pull_request.base.sha', '$ids[2] -cne $ExpectedPullRequestHeadSha',
                      '$mergeTree[0] -cne $headTree[0]', 'refs/pull/$ExpectedPullRequestNumber/merge'):
            self.assertIn(token, COMMON)
        self.assertNotIn("'pull_request_target'", COMMON)
        self.assertIn('Assert-TaskSourceProvenance -SourceRoot $sourceRoot', HOST)
        for token in ("$script:HostedLabTaskBranch = 'feat/ad-lab-windows-install'",
                      "$env:GITHUB_ACTOR_ID -cne '11422136'", "$env:GITHUB_ACTOR -cne 'yunpiao'",
                      "$env:GITHUB_TRIGGERING_ACTOR -cne 'yunpiao'", "$env:GITHUB_RUN_ATTEMPT -cne '1'",
                      '$event.sender.id -ne 11422136', '$event.pull_request.user.id -ne 11422136',
                      '$event.repository.owner.id -ne 11422136',
                      '$event.pull_request.head.repo.fork -ne $false', '$event.pull_request.base.repo.fork -ne $false',
                      '$ExpectedPullRequestHeadRef -cne $script:HostedLabTaskBranch'):
            self.assertIn(token, COMMON)
        self.assertNotIn('ApprovedPullRequest', COMMON + HOST)
        self.assertNotIn('approved_head_sha', HOST)
        self.assertIn('source_head_sha=$provenance.SourceHeadSha', HOST)

    def test_two_image_parameter_sets_and_explicit_license_gate(self):
        for token in ("DefaultParameterSetName='SuppliedVhd'", "ParameterSetName='EvaluationIso'",
                      '[string]$LicenseTermsSha256', '[string]$LicenseRelativePath',
                      '[switch]$LicenseAcceptanceConfirmed', '-not $LicenseAcceptanceConfirmed'):
            self.assertIn(token, HOST)
        self.assertLess(HOST.index('-not $LicenseAcceptanceConfirmed'), HOST.index('Write-HostJson $s $script:StatePath'))
        self.assertNotIn('LicenseAcceptanceConfirmed = $true', HOST)

    def test_owned_state_and_watchdog_before_evaluation_builder(self):
        call = HOST.index('New-HostedLabEvaluationBase -IsoPath')
        self.assertLess(HOST.index('$script:StateCreated = $true'), call)
        self.assertLess(HOST.index('Watchdog handshake mismatch'), call)
        self.assertIn('-LicenseAcceptanceConfirmed:$LicenseAcceptanceConfirmed', HOST)
        self.assertIn('$imageResult[0].BaseVhdPath -cne $media.BasePath', HOST)
        self.assertIn('$media.NativeOperationUnresolved', HOST)

    def test_both_children_prepared_before_either_vm(self):
        self.assertLess(HOST.index('Set-HostedLabChildUnattend -Role'), HOST.index('New-VM -Name'))
        self.assertLess(HOST.index('Assert-ImagePreparationReady -State $s\n'), HOST.index('New-VM -Name'))
        self.assertIn("$p.Value.Stage -cne 'Ready'", COMMON)
        self.assertIn('$p.Value.MountStarted', COMMON)

    def test_setup_secret_purge_before_bootstrap(self):
        main = HOST[HOST.index("$script:FailureStage = 'guest-bootstrap'"):]
        self.assertLess(main.index('Get-HostedLabSetupCleanupCommand'), main.index('Copy-GuestBootstrap -Role'))
        self.assertIn('$purge[0].LabId -cne $labId', HOST)
        self.assertIn('$purge[0].Role -cne $role', HOST)
        self.assertIn('$purge[0].Purged -isnot [bool]', HOST)
        self.assertIn('$machine.ImagePreparation.Purged = $true', HOST)
        self.assertIn('Guest setup secrets must be purged before bootstrap transfer.', HOST)
        self.assertIn('$report.evaluation_installation_verified = $true', HOST)

    def test_mount_cleanup_precedes_tree_removal_and_preserves_journal(self):
        cleanup = COMMON[COMMON.index('function Remove-HostedLabResources'):]
        self.assertLess(cleanup.index('Remove-VM -VM $vm'), cleanup.index('Remove-HostedLabImageMounts'))
        self.assertLess(cleanup.index('Remove-HostedLabImageMounts'), cleanup.index('Remove-Item -LiteralPath $s.Root'))
        self.assertIn('$s = Get-HostState\n        $s.CleanupVerified = -not $failed', cleanup)
        self.assertNotIn('NativeOperationUnresolved = $false', COMMON + HOST)
        self.assertIn(". (Join-Path $PSScriptRoot 'Image.Common.ps1')", COMMON)

    def test_lease_and_capacity(self):
        for token in ('[ValidateRange(15,90)]', '-MemoryStartupBytes 2GB', '-Count 1',
                      '-DynamicMemoryEnabled $false', '-AutomaticCheckpointsEnabled $false',
                      '-CheckpointType Disabled', '-lt 4608MB', '-lt 32GB'):
            self.assertIn(token, HOST)

    def test_private_switch(self):
        self.assertIn('-SwitchType Private', HOST)
        for token in (".SwitchType -ne 'Private'", 'Get-VMNetworkAdapter -ManagementOS', '$nics.Count -ne 1',
                      '$vm.Id.ToString() -notin $ownedIds'):
            self.assertIn(token, COMMON)
        for forbidden in ('New-NetNat', 'Set-NetIPInterface', '-SwitchType External', '-SwitchType Internal'):
            self.assertNotIn(forbidden, HOST + COMMON)

    def test_intent_before_allocation(self):
        self.assertLess(HOST.index('$s.Switch.CreateStarted = $true'), HOST.index('New-VMSwitch'))
        self.assertLess(HOST.index('$m.DiskCreateStarted = $true'), HOST.index('New-VHD'))
        self.assertLess(HOST.index('$m.CreateStarted = $true'), HOST.index('New-VM -Name'))
        self.assertLess(HOST.index('Watchdog handshake mismatch'), HOST.index('New-VMSwitch'))

    def test_watchdog_no_secret_arguments(self):
        self.assertIn("'-WaitForLease'", HOST)
        self.assertIn('$controller.StartTime.ToUniversalTime().Ticks.ToString()', CLEANUP)
        for forbidden in ('Password', 'PSCredential', 'Register-ScheduledTask', 'schtasks'):
            self.assertNotIn(forbidden, CLEANUP)

    def test_cleanup_not_prefix_based(self):
        self.assertIn('$location.StartsWith($expected', COMMON)
        self.assertIn('Assert-VmReservation -Vm', COMMON)
        self.assertIn('Remove-VM -VM $vm', COMMON)
        self.assertIn('Remove-VMSwitch -VMSwitch $sw[0]', COMMON)
        self.assertNotRegex(COMMON, r'Remove-(?:VM|VMSwitch|Item)[^\n]*\*')
        self.assertNotIn('Remove-Item -LiteralPath $BaseVhdPath', ALL)

    def test_reparse_and_foreign_resource_guards(self):
        self.assertIn('[IO.FileAttributes]::ReparsePoint', COMMON)
        self.assertIn('A reserved disk remains attached to another VM.', COMMON)
        self.assertIn('Do not remove a switch with remaining attached adapters.', COMMON)
        self.assertIn('Unexpected unowned VM; refusing cleanup on a shared host.', COMMON)
        self.assertIn("if (@(Get-VM).Count -ne 0)", COMMON)

    def test_stage_order(self):
        markers = ["$script:FailureStage = '" + x + "'" for x in ('dc-prepare', 'dc-promote', 'dc-configure',
                   'dc-ready', 'member-prepare', 'member-join', 'member-verify', 'dc-verify', 'transport-tests')]
        positions = [HOST.index(x) for x in markers]
        self.assertEqual(positions, sorted(positions))
        self.assertIn('$items[0].BootId -ne $PreviousBoot', HOST)

    def test_powershell_direct_bounded_jobs(self):
        self.assertIn('Invoke-Command -VMId', HOST)
        self.assertIn('-AsJob', HOST)
        self.assertIn('Wait-Job -Timeout $budget', HOST)
        self.assertIn('Assert-HostLease (Get-HostState)', HOST)
        self.assertNotIn('New-PSSession -ComputerName', ALL)

    def test_input_pins_and_chunking(self):
        self.assertIn('Get-FileHash -LiteralPath $BaseVhdPath', HOST)
        self.assertIn('Build-OwnedTestBinary -SourceRoot $sourceRoot', HOST)
        self.assertIn('test -c -mod=readonly -trimpath -buildvcs=true -tags=realad', HOST)
        self.assertIn('Get-FileHash -LiteralPath $OutputPath', HOST)
        self.assertNotIn('[string]$TestBinaryPath,', HOST)
        self.assertNotIn('& $go version', HOST)
        self.assertLess(HOST.index("@('GOTOOLCHAIN','local')"), HOST.index("$start.Arguments = 'version'"))
        self.assertIn('New-Object byte[] 524288', HOST)
        self.assertIn("$pin[0].Sha256 -cne $TestBinarySha256", HOST)
        self.assertIn('Get-FileHash -LiteralPath $binary', MEMBER)

    def test_public_export_allowlist(self):
        self.assertIn("@('root-ca.cer','root-ca.pem','fixtures.json','manifest.json')", HOST)
        self.assertIn('$verified.RootCaSha256 -cne $caPin', HOST)
        self.assertNotIn('Export-PfxCertificate', ALL)

    def test_required_real_test_names_match_source(self):
        go = (HERE.parents[2] / 'tests/adlab/real_ad_test.go').read_text()
        names = set(re.findall(r"'(?P<name>TestRealAD[A-Za-z]+)'", MEMBER))
        declared = set(re.findall(r'func (TestRealAD\w+)\(', go))
        self.assertEqual(names, declared)
        self.assertIn('TestRealADRejectsUntrustedCA', names)
        self.assertIn('TestRealADDirectoryRevokedAfterPage', names)
        self.assertIn('TestRealADDirectoryCancelledAfterPage', names)

    def test_empty_test_success_not_accepted(self):
        for token in ('$output -match $runLine', '$output -match $passLine',
                      "$mode in @('ldaps','starttls')", '$output -match $subRun', '$output -match $subPass',
                      '$process.ExitCode -eq 0'):
            self.assertIn(token, MEMBER)

    def test_only_ephemeral_reader_environment(self):
        self.assertIn('$start.EnvironmentVariables.Clear()', MEMBER)
        self.assertIn("$start.EnvironmentVariables['ADTR_LAB_READER_PASSWORD']", MEMBER)
        self.assertIn("$start.EnvironmentVariables.Remove('ADTR_LAB_READER_PASSWORD')", MEMBER)
        for forbidden in ('AdministratorPassword', 'DSRMPassword', 'Start-Transcript'):
            self.assertNotIn(forbidden, MEMBER)
        self.assertNotRegex(ALL, r'\$env:.*PASSWORD\s*=')

    def test_cleanup_gates_report(self):
        self.assertIn('finally {', HOST)
        self.assertIn('Remove-HostedLabResources', HOST)
        self.assertIn('$report.passed = $report.cleanup_verified', HOST)
        self.assertIn('full_product_acceptance=$false', HOST)
        self.assertIn("$report.failure_stage = 'cleanup'", HOST)
        self.assertNotIn('$_ | ConvertTo-Json', ALL)

    def test_no_host_download_or_persistent_access(self):
        for forbidden in ('Invoke-WebRequest', 'Invoke-RestMethod', 'Register-ScheduledTask',
                          'Install-WindowsFeature', 'New-NetNat', 'Enable-PSRemoting', 'Export-Clixml'):
            self.assertNotIn(forbidden, ALL)


if __name__ == '__main__':
    unittest.main(verbosity=2)
