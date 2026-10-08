"""Source assertions only; these do not execute Actions or Windows."""
from pathlib import Path
import unittest
ROOT = Path(__file__).resolve().parents[2]
TEXT = (ROOT / '.github/workflows/ad-lab-windows.yml').read_text()
class WindowsWorkflowSourceTests(unittest.TestCase):
    def test_runtime_remains_disabled_pending_execution_readiness(self):
        self.assertIn('false && github.repository', TEXT)
        self.assertIn('$licenseAcceptanceConfirmed = $true', TEXT)
        self.assertIn('Owner chose to continue after disclosure', TEXT)
    def test_standard_host_and_bounded_job(self):
        self.assertIn('runs-on: windows-2025', TEXT)
        self.assertIn('timeout-minutes: 90', TEXT)
        self.assertNotIn('self-hosted', TEXT)
        self.assertNotIn('pull_request_target', TEXT)
    def test_reviewed_source_and_owner_provenance(self):
        for value in ["github.actor_id == '11422136'", "github.actor == 'yunpiao'", "github.triggering_actor == 'yunpiao'", 'github.run_attempt == 1', "github.ref == 'refs/heads/feat/ad-lab-windows-install'", 'fetch-depth: 2', 'persist-credentials: false', 'ref: ${{ github.sha }}', 'ADTR_SOURCE_SHA: ${{ github.sha }}']:
            self.assertIn(value, TEXT)
    def test_exact_media_and_notice_pins(self):
        self.assertIn('7b052573ba7894c9924e3e87ba732ccd354d18cb75a883efa9b900ea125bfd51', TEXT)
        self.assertIn('ad893f939c901f156d68c36f8180b2f2f7ff424ffba892b9cb2ac717b1b1fe58', TEXT)
        self.assertIn('ImageIndex = 1', TEXT)
        self.assertIn('Licenses\\Eval\\ServerStandardEval\\license.rtf', TEXT)
    def test_no_persistent_secret_or_wildcard_artifact(self):
        for value in ['secrets.', 'id-token:', '*.iso', '*.vhd']:
            self.assertNotIn(value, TEXT)
        for value in ['adtr-real-ad-report.json', 'adtr-real-ad-cleanup.json', 'retention-days: 7']:
            self.assertIn(value, TEXT)
    def test_credentials_and_cleanup(self):
        self.assertIn('[securestring]::new()', TEXT)
        self.assertIn('[Security.Cryptography.RandomNumberGenerator]::Create()', TEXT)
        self.assertIn('$password.Dispose(); $rng.Dispose()', TEXT)
        self.assertLess(TEXT.index('Remove-HostedLab.ps1'), TEXT.index('-CleanupOnly'))
        self.assertIn('if: ${{ always() }}', TEXT)
if __name__ == '__main__':
    unittest.main()
