"""Synthetic launcher contract only; Go/Docker/OpenSSL are mocked, never launched."""
from itertools import product
from unittest.mock import patch
import unittest

import ldap_e2e_fixture
from test_ldap_fixture_contract import LDAPFixtureContract


class PaginationFixtureContract(LDAPFixtureContract):
    def test_pagination_requires_boolean_before_allocation(self):
        for value in (None, 0, 1, "true", [], {}):
            with self.subTest(value=value), patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaises(TypeError):
                    ldap_e2e_fixture.LDAPFixture(group_members_v4_pagination=value)
                allocate.assert_not_called()

    def test_pagination_exclusive_nonempty_fast_v4(self):
        for name in ("directory_v2", "directory_v3", "user_assets_v2", "computer_assets_v3", "group_assets_v3", "directory_empty", "directory_slow", "empty_transition", "control_enabled"):
            with self.subTest(name=name), patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaises(ValueError):
                    ldap_e2e_fixture.LDAPFixture(directory_enabled=True, directory_v4=True, group_members_v4_pagination=True, **{name:True})
                allocate.assert_not_called()
        for directory, v4 in ((False, False), (True, False), (False, True)):
            with self.assertRaises(ValueError):
                ldap_e2e_fixture.LDAPFixture(directory_enabled=directory, directory_v4=v4, group_members_v4_pagination=True)

    def test_pagination_exact_launch_and_marker(self):
        with self.started_fixture(directory_enabled=True, directory_v4=True, group_members_v4_pagination=True) as (fixture, env, run):
            command = next(c.args[0] for c in run.call_args_list if c.args[0][:2] == ["docker", "run"])
            self.assertEqual(command[-3:], ["-directory-mode", "-directory-v4", "-group-members-v4-pagination"])
            self.assertEqual(env["ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_PAGINATION"], "true")
            self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_EMPTY", env)
            self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_SLOW", env)

    def test_disabled_pagination_clears_inherited_marker(self):
        for modes in ({}, {"directory_enabled":True}, {"directory_enabled":True,"directory_v2":True}, {"directory_enabled":True,"directory_v3":True}, {"directory_enabled":True,"directory_v4":True}):
            fixture = ldap_e2e_fixture.LDAPFixture(**modes)
            env={"ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_PAGINATION":"true"}
            import subprocess
            with patch.object(ldap_e2e_fixture.subprocess,"run",return_value=subprocess.CompletedProcess([],0)) as run, patch.object(ldap_e2e_fixture.subprocess,"check_output",side_effect=self.fixture_output):
                try:
                    fixture.start(env)
                    self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_GROUP_MEMBERS_V4_PAGINATION",env)
                    command=next(c.args[0] for c in run.call_args_list if c.args[0][:2]==["docker","run"])
                    self.assertNotIn("-group-members-v4-pagination",command)
                finally: fixture.close()

if __name__ == "__main__":
    unittest.main()
