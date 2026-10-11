"""Keep the real dictionary3 gate bounded, synthetic, and independent of v2."""
from pathlib import Path
import unittest
from ldap_e2e_fixture import LDAPFixture
from test_auth_e2e import DIRECTORY_SUITES, DIRECTORY_V2_SUITES

class DirectoryV3Contract(unittest.TestCase):
    def test_separate_fixture_and_suite(self):
        self.assertIn('directory-v3-backend',DIRECTORY_SUITES)
        self.assertNotIn('directory-v3-backend',DIRECTORY_V2_SUITES)
        for kwargs in ({'directory_v3':True},{'directory_enabled':True,'directory_v3':True,'directory_v2':True},{'directory_enabled':True,'directory_v3':True,'user_assets_v2':True}):
            with self.assertRaises(ValueError): LDAPFixture(**kwargs)
        with self.assertRaises(TypeError): LDAPFixture(directory_v3='true')
        f=LDAPFixture(directory_enabled=True,directory_v3=True)
        self.assertTrue(f.directory_v3)
        self.assertFalse(f.directory_v2)
        f.close()
    def test_real_chain_and_evidence_gate(self):
        workflow=Path('.github/workflows/ci.yml').read_text()
        self.assertIn('command: python3 scripts/test_auth_e2e.py --suite directory-v3-backend',workflow)
        self.assertIn('web/test-results/**/directory-v3-backend-evidence.json',workflow)
        code=Path('web/e2e/directory-v3-backend.spec.ts').read_text()
        for value in ['storedEvidence(taskUUID)','createHash("sha256")','primaryGroupIDBytes','servicePrincipalNameBytes','executor_returned','dependencies: 0','/api/user-assets/v3/detail','/api/directory/v3/sync']:
            self.assertIn(value,code)
        for forbidden in ['.fulfill(', 'INSERT INTO adtr.', 'UPDATE adtr.', 'DELETE FROM adtr.']:
            self.assertNotIn(forbidden,code)
        self.assertIn('trace: "off"',code)
