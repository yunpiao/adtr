"""Guard dictionary3's additive real browser gate; never substitute for CI."""
import ast
from pathlib import Path
import unittest

from test_auth_e2e import (DIRECTORY_SUITES, DIRECTORY_V2_SUITES,
                           DIRECTORY_V3_SUITES, EMPTY_DIRECTORY_SUITES,
                           REAL_TAB_LIFECYCLE_SUITES, SLOW_DIRECTORY_SUITES)


class UserAssetsV3BrowserContract(unittest.TestCase):
    def test_bounded_headed_nonempty_real_v3_fixture(self):
        suite = 'user-assets-v3'
        for suites in [DIRECTORY_SUITES, DIRECTORY_V3_SUITES, REAL_TAB_LIFECYCLE_SUITES]:
            self.assertIn(suite, suites)
        for suites in [DIRECTORY_V2_SUITES, EMPTY_DIRECTORY_SUITES, SLOW_DIRECTORY_SUITES]:
            self.assertNotIn(suite, suites)
        runner = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(runner)
        choices = [kw.value for node in ast.walk(tree) if isinstance(node, ast.Call)
                   for kw in node.keywords if kw.arg == 'choices']
        self.assertTrue(any(suite in ast.literal_eval(value) for value in choices))
        self.assertIn('directory_v3=args.suite in DIRECTORY_V3_SUITES', runner)
        self.assertIn('if args.suite in DIRECTORY_V3_SUITES:', runner)
        self.assertIn('env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "true"', runner)
        self.assertIn('env["ADTR_E2E_DB_CONTAINER"] = name', runner)

    def test_original_and_fixed_engines_remain_required(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        self.assertEqual(workflow.count('command: python3 scripts/test_auth_e2e.py --suite user-assets-v3\n'), 1)
        self.assertIn("matrix.name == 'user-assets-v3' || matrix.name == 'computer-assets-v3' || matrix.name == 'computer-assets-v3-readers' || matrix.name == 'group-assets-v3' || matrix.name == 'group-assets-v3-readers') && 'stable-body'", workflow)
        self.assertIn('suite: [user-assets-v2, user-assets-v2-readers, user-assets-v3, computer-assets-v3, computer-assets-v3-readers, group-assets-v3, group-assets-v3-readers]', workflow)
        self.assertIn('path: web/test-results/**/user-assets-v3-*\n', workflow)
        self.assertIn('            web/test-results/**/user-assets-v3-*\n', workflow)
        self.assertIn('if-no-files-found: error', workflow)
        self.assertNotIn('continue-on-error', workflow)

    def test_producer_provenance_and_browser_evidence_are_explicit(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        for required in ['storedEvidence(taskUUID)', 'storedEvidence(newerId)',
                         'createHash("sha256")', 'primaryGroupIDBytes',
                         'servicePrincipalNameBytes', 'executor_returned',
                         '/api/directory/v3/sync', 'user-assets-v3-workspace-evidence.json',
                         'screenshotSHA256', 'observationSHA256', 'browserReads',
                         'width: 1440, height: 900', 'width: 390, height: 844',
                         'useNativeTabLifecycle', 'requireSameNativeWindow',
                         'complete-consumed', 'selection_changed',
                         'Input.dispatchDragEvent', 'navigator.clipboard.writeText']:
            self.assertIn(required, code)
        for forbidden in ['.fulfill(', 'INSERT INTO adtr.', 'UPDATE adtr.', 'DELETE FROM adtr.',
                          'test.skip(', 'test.fixme(']:
            self.assertNotIn(forbidden, code)
        for protection in ['trace: "off"', 'screenshot: "off"', 'video: "off"']:
            self.assertIn(protection, code)
        for helper in ['directory-v2-response-observer.ts', 'user-assets-v2-abort-isolation.ts',
                       'user-assets-v2-held-response.ts']:
            content = Path('web/e2e', helper).read_text()
            for route in ['/api/user-assets/v2', '/api/user-assets/v2/detail',
                          '/api/user-assets/v3', '/api/user-assets/v3/detail']:
                self.assertIn(f'"{route}"', content)


if __name__ == '__main__':
    unittest.main()
