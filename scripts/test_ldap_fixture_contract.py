"""Synthetic LDAP launcher contracts. Docker/OpenSSL/Go are mocked here."""
from contextlib import contextmanager
from itertools import product
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

import ldap_e2e_fixture


class LDAPFixtureContract(unittest.TestCase):
    @contextmanager
    def started_fixture(self, **modes):
        fixture = ldap_e2e_fixture.LDAPFixture(**modes)
        env = {"ADTR_E2E_LDAP_DIRECTORY_" + suffix: "inherited"
               for suffix in ("MODE", "EMPTY", "SLOW", "V2")}

        def output(command, **options):
            self.assertEqual(options["timeout"], 5)
            if command[:2] == ["docker", "logs"]:
                return "synthetic LDAP fixture ready\n"
            if command[:4] == ["docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"]:
                return "172.29.0.2\n"
            if command[:4] == ["docker", "inspect", "-f", "{{.State.ExitCode}}"]:
                return "0\n"
            self.fail(f"unexpected fixture read: {command}")

        with patch.object(ldap_e2e_fixture.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run, \
             patch.object(ldap_e2e_fixture.subprocess, "check_output", side_effect=output):
            try:
                fixture.start(env)
                yield fixture, env, run
            finally:
                fixture.close()

    def test_directory_v2_is_keyword_only_and_default_false(self):
        fixture = ldap_e2e_fixture.LDAPFixture()
        try:
            self.assertFalse(fixture.directory_enabled)
            self.assertFalse(fixture.directory_v2)
        finally:
            fixture.close()
        with self.assertRaises(TypeError):
            ldap_e2e_fixture.LDAPFixture(False, True, False, False, True)

    def test_all_directory_flags_require_booleans_before_allocating_resources(self):
        for name in ("directory_enabled", "directory_empty", "directory_slow", "directory_v2"):
            for value in (None, 0, 1, "true", [], {}):
                with self.subTest(name=name, value=value), \
                     patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                    with self.assertRaisesRegex(TypeError, "must be booleans"):
                        ldap_e2e_fixture.LDAPFixture(**{name: value})
                    allocate.assert_not_called()

    def test_v2_empty_and_slow_require_explicit_directory_mode(self):
        for v2, empty, slow in product((False, True), repeat=3):
            if not any((v2, empty, slow)):
                continue
            with self.subTest(v2=v2, empty=empty, slow=slow), \
                 patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaisesRegex(ValueError, "require.*directory mode"):
                    ldap_e2e_fixture.LDAPFixture(directory_v2=v2, directory_empty=empty, directory_slow=slow)
                allocate.assert_not_called()

    def test_exact_flags_markers_and_owned_cleanup_for_each_dictionary_mode(self):
        for v2, empty, slow in product((False, True), repeat=3):
            with self.subTest(v2=v2, empty=empty, slow=slow):
                with self.started_fixture(directory_enabled=True, directory_v2=v2,
                                          directory_empty=empty, directory_slow=slow) as (fixture, env, run):
                    calls = run.call_args_list
                    launch = next(call for call in calls if call.args[0][:2] == ["docker", "run"])
                    command = launch.args[0]
                    expected = ["-directory-mode"]
                    if v2:
                        expected.append("-directory-v2")
                    if empty:
                        expected.append("-directory-empty")
                    if slow:
                        expected.append("-directory-slow")
                    flags = [arg for arg in command if arg.startswith("-directory-")]
                    self.assertEqual(flags, expected)
                    self.assertEqual(command[command.index("-starttls-listen") + 1], "0.0.0.0:389")
                    self.assertEqual(command[command.index("-ldaps-listen") + 1], "0.0.0.0:636")
                    self.assertEqual(launch.kwargs["timeout"], 60)
                    self.assertEqual(command[command.index("--network") + 1], fixture.network)
                    network = next(call.args[0] for call in calls if call.args[0][:3] == ["docker", "network", "create"])
                    self.assertEqual(network, ["docker", "network", "create", "--internal", fixture.network])
                    for suffix, enabled in (("MODE", True), ("V2", v2), ("EMPTY", empty), ("SLOW", slow)):
                        key = "ADTR_E2E_LDAP_DIRECTORY_" + suffix
                        if enabled:
                            self.assertEqual(env[key], "true")
                        else:
                            self.assertNotIn(key, env)
                    for name in ("ADTR_LDAP_FIXTURE_USERNAME", "ADTR_LDAP_FIXTURE_PASSWORD"):
                        self.assertIn(name, command)
                        self.assertNotIn(launch.kwargs["env"][name], command)
                    self.assertEqual(env["ADTR_E2E_LDAP_IP"], "172.29.0.2")
                    self.assertEqual(env["ADTR_DOMAIN_PROBE_ENABLED"], "true")
                    # Fixture metadata does not grant application collection access.
                    self.assertNotIn("ADTR_DIRECTORY_READ_ENABLED", env)
                    self.assertNotIn("ADTR_DIRECTORY_READ_V2_ENABLED", env)
                    directory = Path(fixture.directory.name)
                self.assertFalse(directory.exists())
                cleanup = [call.args[0] for call in run.call_args_list[-3:]]
                self.assertEqual(cleanup, [
                    ["docker", "stop", "--time", "10", fixture.name],
                    ["docker", "rm", "--force", fixture.name],
                    ["docker", "network", "rm", fixture.network],
                ])

    def test_default_rootdse_only_mode_clears_every_inherited_directory_marker(self):
        with self.started_fixture() as (_, env, run):
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            self.assertFalse(any(arg.startswith("-directory-") for arg in command))
            self.assertFalse(any(key.startswith("ADTR_E2E_LDAP_DIRECTORY_") for key in env))

    def test_v2_can_keep_existing_b2_control_mount_and_cleanup(self):
        with self.started_fixture(control_enabled=True, directory_enabled=True, directory_v2=True) as (fixture, env, run):
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            self.assertIn(f"{fixture.control_path}:/control:rw", command)
            self.assertEqual(command[command.index("-control-dir") + 1], "/control")
            self.assertEqual(env["ADTR_E2E_LDAP_CONTROL_DIR"], str(fixture.control_path))
            control = fixture.control_path
        self.assertFalse(control.exists())


if __name__ == "__main__":
    unittest.main()
