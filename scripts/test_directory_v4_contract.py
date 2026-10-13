"""Dictionary4 must remain an additional fixed backend gate, not replace old jobs."""
from pathlib import Path
import unittest
from unittest.mock import patch
import ldap_e2e_fixture


class DirectoryV4Contract(unittest.TestCase):
    def test_real_backend_suite_and_evidence_are_mandatory(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        self.assertEqual(workflow.count('command: python3 scripts/test_auth_e2e.py --suite directory-v4-backend\n'), 1)
        self.assertEqual(workflow.count('command: python3 scripts/test_auth_e2e.py --suite directory-v3-backend\n'), 1)
        self.assertIn("if: ${{ success() && matrix.name == 'directory-v4-backend' }}", workflow)
        self.assertIn('path: web/test-results/**/directory-v4-backend-evidence.json', workflow)
        block = workflow.split('      - name: Upload dictionary4 backend evidence\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn('if-no-files-found: error', block)
        self.assertIn('run: python3 scripts/validate_directory_v4_evidence.py --source-root .', workflow)
        self.assertLess(workflow.index('      - name: Validate dictionary4 backend evidence'), workflow.index('      - name: Upload dictionary4 backend evidence'))
        test = Path('web/e2e/directory-v4-backend.spec.ts').read_text()
        for token in ['ADTR_E2E_LDAP_DIRECTORY_V4', 'storedMembership', '/api/group-assets/v4/members', 'returnedMemberCount', 'memberReadState', 'storedEvidence(taskUUID)', 'createHash("sha256")']:
            self.assertIn(token, test)
        self.assertNotIn('test.skip(', test)
        self.assertNotIn('test.fixme(', test)

    def test_v4_fixture_is_exclusive_and_defaults_off(self):
        with ldap_e2e_fixture.tempfile.TemporaryDirectory() as unused:
            fixture = ldap_e2e_fixture.LDAPFixture()
            try:
                self.assertFalse(fixture.directory_v4)
            finally:
                fixture.close()
        for modes in [{'directory_v4': True}, *[{'directory_enabled': True, 'directory_v4': True, field: True} for field in ['directory_v2', 'directory_v3', 'user_assets_v2', 'computer_assets_v3', 'group_assets_v3', 'empty_transition', 'control_enabled']]]:
            with self.subTest(modes=modes), patch.object(ldap_e2e_fixture.tempfile, 'TemporaryDirectory') as allocate:
                with self.assertRaises(ValueError):
                    ldap_e2e_fixture.LDAPFixture(**modes)
                allocate.assert_not_called()
        fixture = ldap_e2e_fixture.LDAPFixture(directory_enabled=True, directory_v4=True)
        try:
            self.assertTrue(fixture.directory_v4)
            self.assertFalse(fixture.directory_v3)
        finally:
            fixture.close()


if __name__ == '__main__':
    unittest.main()
