from ci_workflow_contract import reviewed_workflow
"""Dictionary3 controls are additive; static checks do not prove browser acceptance."""
from pathlib import Path
import ast
import unittest
from unittest.mock import patch
from test_auth_e2e import (DIRECTORY_V3_SUITES, DIRECTORY_V2_SUITES, DIRECTORY_SUITES,
                           EMPTY_DIRECTORY_SUITES, SLOW_DIRECTORY_SUITES,
                           REAL_TAB_LIFECYCLE_SUITES, browser_command)

class DirectoryV3ControlsContract(unittest.TestCase):
    def test_owned_fixture_and_fixed_version(self):
        runner = Path('scripts/test_auth_e2e.py').read_text()
        choices = [ast.literal_eval(kw.value) for node in ast.walk(ast.parse(runner))
                   if isinstance(node, ast.Call) for kw in node.keywords if kw.arg == 'choices']
        for suite in ['directory-v3', 'directory-v3-controls', 'directory-v3-readers']:
            self.assertIn(suite, DIRECTORY_SUITES)
            self.assertIn(suite, DIRECTORY_V3_SUITES)
            self.assertNotIn(suite, DIRECTORY_V2_SUITES)
            self.assertTrue(any(suite in values for values in choices))
            code = Path(f'web/e2e/{suite}.spec.ts').read_text()
            for guard in ['DIRECTORY_V3', 'synthetic.invalid', 'trace: "off"',
                          'screenshot: "off"', 'video: "off"', '/api/directory/v3/']:
                self.assertIn(guard, code)
            for forbidden in ['test.skip(', 'test.fixme(', 'UPDATE adtr.',
                              'INSERT INTO adtr.', 'DELETE FROM adtr.']:
                self.assertNotIn(forbidden, code)
            if suite == 'directory-v3-readers':
                self.assertEqual(code.count('await route.fulfill({ response });'), 1)
                self.assertEqual(code.count('.fulfill('), 1)
                self.assertIn('response = await route.fetch(', code)
            else:
                self.assertNotIn('.fulfill(', code)
        self.assertNotIn('directory-v3', EMPTY_DIRECTORY_SUITES)
        for suite in ['directory-v3-controls','directory-v3-readers']:
            self.assertIn(suite, EMPTY_DIRECTORY_SUITES)
        self.assertIn('directory-v3-controls', SLOW_DIRECTORY_SUITES)
        self.assertNotIn('directory-v3-readers', SLOW_DIRECTORY_SUITES)
        self.assertIn('directory-v3-readers', REAL_TAB_LIFECYCLE_SUITES)
        with patch.dict('os.environ', {'DISPLAY': ':99'}):
            self.assertIn('--headed', browser_command('directory-v3-readers'))

    def test_all_new_gates_and_provenance(self):
        workflow = reviewed_workflow()
        for suite in ['directory-v3','directory-v3-controls','directory-v3-readers']:
            self.assertEqual(workflow.count(f'command: python3 scripts/test_auth_e2e.py --suite {suite}\n'),1)
        self.assertIn('path: web/test-results/**/directory-v3-*',workflow)
        self.assertNotIn('continue-on-error',workflow)
        code = Path('web/e2e/directory-v3.spec.ts').read_text()
        for proof in ['storedEvidence(taskUUID)','executor_returned','observationSHA256',
                      'screenshotSHA256','directoryV2ResponseWitness','page.goBack()','page.goForward()',
                      'width: 1440','width: 390','governancePosts','receiptResponse',
                      'primaryGroupIDBytes','servicePrincipalNameBytes']:
            self.assertIn(proof,code)
        observer = Path('web/e2e/directory-v3-ledger-observer.ts').read_text()
        self.assertIn("u.purpose='domain.directory_read.v3'",observer)
        self.assertIn("d.consumer_kind='domain.directory_read.v3'",observer)
        for forbidden in ['INSERT ', 'UPDATE ', 'DELETE ', 'TRUNCATE ', 'shell:']:
            self.assertNotIn(forbidden,observer)

    def test_versioned_recovery_and_no_backend_change(self):
        for name in ['directory-v3-intent.ts','directory-v3-credential-use-intent.ts']:
            code = Path('web/src',name).read_text()
            self.assertIn('.v3:',code)
            self.assertNotIn('.v2:',code)
            self.assertNotIn('actorPassword:',code)
            self.assertNotIn('totpCode:',code)
        collection = Path('web/src/directory-v3-api.ts').read_text()
        self.assertIn('validDirectoryV3Object',collection)
        self.assertIn('./directory-v3-facts',collection)
        self.assertIn('readDirectoryV2JSON',collection)
        self.assertIn('v.dictionaryVersion !== 3',collection)
        ui = Path('web/src/DirectoryV3CredentialUseWorkspace.tsx').read_text()
        self.assertIn('ADTR_DIRECTORY_READ_V3_ENABLED',ui)
        self.assertIn('当前部署开关值未由此接口提供',ui)
        self.assertIn('查询仅恢复原回执',ui)

if __name__ == '__main__': unittest.main()
