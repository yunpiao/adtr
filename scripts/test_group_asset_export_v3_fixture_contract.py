"""Pure Python: fixture subprocesses are mocked; this is not acceptance evidence."""
import unittest
from unittest.mock import patch
import ldap_e2e_fixture as ldap
import test_ldap_fixture_contract as legacy_fixture
import group_asset_export_v3_ci as gate


class GroupExportFixtureContract(unittest.TestCase):
    def test_exclusive_failure_mode_rejected_before_allocation(self):
        base = dict(directory_enabled=True, directory_v3=True, group_assets_v3=True, group_export_failure_v3=True)
        invalid = [dict(base, **{name: True}) for name in (
            'directory_v2', 'directory_v4', 'user_assets_v2', 'computer_assets_v3',
            'directory_empty', 'directory_slow', 'empty_transition', 'control_enabled',
            'group_members_v4_pagination', 'group_members_v4_refresh', 'user_export_failure_v3')]
        invalid += [dict(base, **{name: False}) for name in ('directory_enabled', 'directory_v3', 'group_assets_v3')]
        for modes in invalid:
            with self.subTest(modes=modes), patch.object(ldap.tempfile, 'TemporaryDirectory') as allocation:
                with self.assertRaises(ValueError): ldap.LDAPFixture(**modes)
                allocation.assert_not_called()
        with self.assertRaises(TypeError): ldap.LDAPFixture(group_export_failure_v3=1)

    def test_exact_flag_and_stale_marker_cleanup(self):
        helper = legacy_fixture.LDAPFixtureContract()
        for enabled in (False, True):
            with helper.started_fixture(directory_enabled=True, directory_v3=True,
                    group_assets_v3=True, group_export_failure_v3=enabled) as (fixture, env, run):
                command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ['docker', 'run'])
                self.assertEqual(command.count('-group-export-failure-v3'), int(enabled))
                self.assertEqual(command.count('-group-assets-v3'), 1)
                self.assertNotIn('-user-export-failure-v3', command)
                self.assertEqual(env.get('ADTR_E2E_LDAP_DIRECTORY_GROUP_EXPORT_FAILURE_V3'), 'true' if enabled else None)
                self.assertIsNone(fixture.control_path)
                self.assertIsNone(fixture.empty_transition_path)

    def test_success_configuration_never_changes_production_limits(self):
        env = {}
        gate.configure(env)
        self.assertEqual(env, {'ADTR_E2E_GROUP_ASSET_EXPORT_V3': 'true', 'ADTR_E2E_LDAP_DIRECTORY_GROUP_ASSETS_V3': 'true'})
        for mode in gate.MIXED_MODES:
            with self.subTest(mode=mode), self.assertRaises(RuntimeError):
                gate.configure({'ADTR_E2E_LDAP_DIRECTORY_' + mode: 'true'})
        with self.assertRaises(RuntimeError): gate.configure({'ADTR_E2E_USER_ASSET_EXPORT_V3': 'true'})


if __name__ == '__main__': unittest.main()
