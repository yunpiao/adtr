"""Refresh fixture launcher contracts; infrastructure calls are mocked only here."""
import inspect
import unittest
from unittest.mock import patch
import test_ldap_fixture_contract
from ldap_e2e_fixture import LDAPFixture

class RefreshFixtureContract(unittest.TestCase):
    def test_explicit_refresh_mode_and_marker_are_required(self):
        parameter = inspect.signature(LDAPFixture).parameters['group_members_v4_refresh']
        self.assertEqual(parameter.kind, inspect.Parameter.KEYWORD_ONLY)
        self.assertIs(parameter.default, False)
        valid = dict(directory_enabled=True, directory_v4=True, empty_transition=True, group_members_v4_refresh=True)
        for key in ('directory_enabled', 'directory_v4', 'empty_transition'):
            with self.subTest(missing=key), patch('ldap_e2e_fixture.tempfile.TemporaryDirectory') as allocation:
                with self.assertRaises(ValueError):
                    LDAPFixture(**{**valid, key: False})
                allocation.assert_not_called()
        for key in ('directory_empty', 'directory_slow', 'directory_v2', 'directory_v3', 'control_enabled', 'group_members_v4_pagination', 'user_assets_v2', 'computer_assets_v3', 'group_assets_v3'):
            with self.subTest(extra=key), patch('ldap_e2e_fixture.tempfile.TemporaryDirectory') as allocation:
                with self.assertRaises(ValueError):
                    LDAPFixture(**{**valid, key: True})
                allocation.assert_not_called()
        with self.assertRaises(TypeError):
            LDAPFixture(group_members_v4_refresh=1)

    def test_launch_is_private_readonly_marker_with_exact_v4_mode(self):
        harness = test_ldap_fixture_contract.LDAPFixtureContract()
        with harness.started_fixture(directory_enabled=True, directory_v4=True, empty_transition=True, group_members_v4_refresh=True) as (fixture, env, run):
            self.assertEqual(env['ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_REFRESH'], 'true')
            self.assertEqual(env['ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR'], str(fixture.empty_transition_path))
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ['docker', 'run'])
            self.assertIn('-group-members-v4-refresh', command)
            self.assertIn('-directory-v4', command)
            self.assertIn(f'{fixture.empty_transition_path}:/empty-transition:ro', command)
            self.assertNotIn('-directory-empty', command)
            self.assertNotIn('-group-members-v4-pagination', command)
            self.assertEqual(list(fixture.empty_transition_path.iterdir()), [])

if __name__ == '__main__':
    unittest.main()
