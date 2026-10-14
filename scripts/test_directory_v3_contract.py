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

    def test_observed_counts_existing_real_gates(self):
        # Static wiring only. The existing real CI suites must establish runtime evidence.
        mixed = Path('web/e2e/directory-v3.spec.ts').read_text()
        for required in ['observedCounts: null', '{ user: 12, group: 12, computer: 6 }',
                         'firstPage.value.observedCounts', 'secondPage.value.observedCounts',
                         'filtered.value.observedCounts', 'filtered.value.observationId',
                         'storedCounts[kind]++', 'afterReload.value.observedCounts',
                         'object.objectClass.includes("user")', 'toHaveLength(18)',
                         '本次观测中的对象数量：暂无可用观测', 'await expect(countsView).toHaveCount(0)']:
            self.assertIn(required, mixed)
        readers = Path('web/e2e/directory-v3-readers.spec.ts').read_text()
        for required in ['observedCounts: null', '{ user: 0, group: 0, computer: 0 }',
                         '本次观测中的对象数量：用户 0 · 组 0 · 计算机 0',
                         'expect(denied.status()).toBe(404)', 'expect(staleSession.status()).toBe(401)',
                         'expect(afterRevoke.value).toEqual(observation)']:
            self.assertIn(required, readers)
        self.assertNotIn('.fulfill(', mixed)
        self.assertEqual(readers.count('.fulfill('), 1)
