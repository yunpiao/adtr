"""Synthetic LDAP launcher contracts. Docker/OpenSSL/Go are mocked here."""
from contextlib import contextmanager
import inspect
from itertools import product
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ldap_e2e_fixture


class LDAPFixtureContract(unittest.TestCase):
    def fixture_output(self, command, **options):
        self.assertEqual(options["timeout"], 5)
        if command[:2] == ["docker", "logs"]:
            return "synthetic LDAP fixture ready\n"
        if command[:4] == ["docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"]:
            return "172.29.0.2\n"
        if command[:4] == ["docker", "inspect", "-f", "{{.State.ExitCode}}"]:
            return "0\n"
        self.fail(f"unexpected fixture read: {command}")

    @contextmanager
    def started_fixture(self, *, inherited_transition="/unowned/inherited/path", **modes):
        fixture = ldap_e2e_fixture.LDAPFixture(**modes)
        env = {"ADTR_E2E_LDAP_DIRECTORY_" + suffix: "inherited"
               for suffix in ("USER_EXPORT_FAILURE_V3", "MODE", "EMPTY", "SLOW", "V2", "USER_ASSETS_V2", "V3", "V4", "COMPUTER_ASSETS_V3", "GROUP_ASSETS_V3")}
        env["ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR"] = inherited_transition

        with patch.object(ldap_e2e_fixture.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run, \
             patch.object(ldap_e2e_fixture.subprocess, "check_output", side_effect=self.fixture_output):
            try:
                fixture.start(env)
                yield fixture, env, run
            finally:
                fixture.close()

    def test_user_export_failure_requires_exact_mode_before_allocation(self):
        base = dict(directory_enabled=True, directory_v3=True, user_export_failure_v3=True)
        invalid = [dict(base, **{name: True}) for name in (
            "control_enabled", "directory_empty", "directory_slow", "directory_v2",
            "user_assets_v2", "directory_v4", "computer_assets_v3", "group_assets_v3",
            "empty_transition", "group_members_v4_pagination", "group_members_v4_refresh")]
        invalid += [dict(base, **{name: False}) for name in ("directory_enabled", "directory_v3")]
        for modes in invalid:
            with self.subTest(modes=modes), patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaisesRegex(ValueError, "export failure requires exclusive"):
                    ldap_e2e_fixture.LDAPFixture(**modes)
                allocate.assert_not_called()

    def test_user_export_failure_exact_launch_and_stale_marker_removal(self):
        for enabled in (False, True):
            with self.subTest(enabled=enabled), self.started_fixture(
                    directory_enabled=True, directory_v3=True,
                    user_export_failure_v3=enabled) as (fixture, env, run):
                command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
                self.assertEqual(command.count("-user-export-failure-v3"), int(enabled))
                self.assertIn("-directory-v3", command)
                key = "ADTR_E2E_LDAP_DIRECTORY_USER_EXPORT_FAILURE_V3"
                if enabled:
                    self.assertEqual(env[key], "true")
                else:
                    self.assertNotIn(key, env)
                self.assertIsNone(fixture.control_path)
                self.assertIsNone(fixture.empty_transition_path)

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
        for name in ("directory_enabled", "directory_empty", "directory_slow", "directory_v2", "user_assets_v2", "directory_v3", "directory_v4", "computer_assets_v3", "group_assets_v3", "empty_transition", "user_export_failure_v3"):
            for value in (None, 0, 1, 0.0, 1.0, "true", "false", [], {}):
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
            self.assertNotIn("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", env)

    def test_user_assets_mode_requires_v2_nonempty_fast_before_allocating(self):
        for enabled, v2, empty, slow in product((False, True), repeat=4):
            if enabled and v2 and not empty and not slow:
                continue
            with self.subTest(enabled=enabled, v2=v2, empty=empty, slow=slow), \
                 patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaises(ValueError):
                    ldap_e2e_fixture.LDAPFixture(directory_enabled=enabled, directory_v2=v2,
                                                directory_empty=empty, directory_slow=slow,
                                                user_assets_v2=True)
                allocate.assert_not_called()

    def test_user_assets_mode_wires_only_explicit_fixture_flag(self):
        with self.started_fixture(directory_enabled=True, directory_v2=True,
                                  user_assets_v2=True) as (fixture, env, run):
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            self.assertIn("-user-assets-v2", command)
            self.assertEqual(env["ADTR_E2E_LDAP_DIRECTORY_USER_ASSETS_V2"], "true")
            self.assertEqual(env["ADTR_E2E_LDAP_DIRECTORY_V2"], "true")
            self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_EMPTY", env)
            self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_SLOW", env)
            self.assertNotIn("ADTR_DIRECTORY_READ_ENABLED", env)
            self.assertNotIn("ADTR_DIRECTORY_READ_V2_ENABLED", env)
        with self.started_fixture(directory_enabled=True, directory_v2=True) as (_, env, run):
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            self.assertNotIn("-user-assets-v2", command)
            self.assertNotIn("ADTR_E2E_LDAP_DIRECTORY_USER_ASSETS_V2", env)

    def test_v2_can_keep_existing_b2_control_mount_and_cleanup(self):
        with self.started_fixture(control_enabled=True, directory_enabled=True, directory_v2=True) as (fixture, env, run):
            command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            self.assertIn(f"{fixture.control_path}:/control:rw", command)
            self.assertEqual(command[command.index("-control-dir") + 1], "/control")
            self.assertEqual(env["ADTR_E2E_LDAP_CONTROL_DIR"], str(fixture.control_path))
            control = fixture.control_path
        self.assertFalse(control.exists())

    def test_empty_transition_is_keyword_only_and_default_false_without_allocation(self):
        parameter = inspect.signature(ldap_e2e_fixture.LDAPFixture).parameters["empty_transition"]
        self.assertEqual(parameter.kind, inspect.Parameter.KEYWORD_ONLY)
        self.assertIs(parameter.default, False)
        with patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory", wraps=tempfile.TemporaryDirectory) as allocate:
            fixture = ldap_e2e_fixture.LDAPFixture()
            try:
                self.assertIs(fixture.empty_transition, False)
                self.assertIsNone(fixture.empty_transition_path)
                self.assertIsNone(fixture.empty_transition_directory)
                allocate.assert_called_once_with(prefix="adtr-ldap-e2e-")
            finally:
                fixture.close()

    def test_empty_transition_accepts_only_two_exact_mode_combinations_before_allocation(self):
        names = ("directory_enabled", "directory_v3", "computer_assets_v3", "group_assets_v3",
                 "directory_empty", "directory_slow", "directory_v2", "user_assets_v2", "control_enabled")
        accepted = []
        for values in product((False, True), repeat=len(names)):
            modes = dict(zip(names, values))
            valid = (modes["directory_enabled"] and modes["directory_v3"] and
                     modes["computer_assets_v3"] != modes["group_assets_v3"] and
                     not any(modes[name] for name in names[4:]))
            with self.subTest(**modes), \
                 patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory", wraps=tempfile.TemporaryDirectory) as allocate:
                if valid:
                    fixture = ldap_e2e_fixture.LDAPFixture(empty_transition=True, **modes)
                    try:
                        self.assertEqual(allocate.call_count, 2)
                        self.assertEqual(allocate.call_args.kwargs, {"prefix": "adtr-ldap-empty-transition-"})
                        self.assertIs(fixture.empty_transition, True)
                        accepted.append(modes)
                    finally:
                        fixture.close()
                else:
                    with self.assertRaisesRegex(ValueError, "empty transition requires"):
                        ldap_e2e_fixture.LDAPFixture(empty_transition=True, **modes)
                    allocate.assert_not_called()
        self.assertEqual(len(accepted), 2)

    def test_disabled_transition_preserves_existing_modes_and_clears_inherited_path(self):
        cases = ({}, {"directory_enabled": True},
                 {"directory_enabled": True, "directory_empty": True, "directory_slow": True},
                 {"directory_enabled": True, "directory_v2": True},
                 {"directory_enabled": True, "directory_v2": True, "user_assets_v2": True},
                 {"directory_enabled": True, "directory_v3": True},
                 {"directory_enabled": True, "directory_v3": True, "computer_assets_v3": True},
                 {"directory_enabled": True, "directory_v3": True, "group_assets_v3": True},
                 {"control_enabled": True})
        for modes in cases:
            with self.subTest(**modes), \
                 patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory", wraps=tempfile.TemporaryDirectory) as allocate:
                with self.started_fixture(**modes) as (fixture, env, run):
                    command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
                    self.assertIsNone(fixture.empty_transition_path)
                    self.assertNotIn("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", env)
                    self.assertNotIn("-empty-transition-dir", command)
                    self.assertFalse(any("/empty-transition" in arg for arg in command))
                    self.assertEqual(allocate.call_count, 2 if modes.get("control_enabled") else 1)

    def test_transition_owns_fresh_private_directory_mount_flag_and_local_env_only(self):
        for asset in ("computer_assets_v3", "group_assets_v3"):
            with self.subTest(asset=asset), tempfile.TemporaryDirectory(prefix="unowned-test-") as inherited:
                unowned = Path(inherited) / "keep"
                unowned.write_bytes(b"untouched")
                with self.started_fixture(directory_enabled=True, directory_v3=True,
                                          empty_transition=True, inherited_transition=inherited,
                                          **{asset: True}) as (fixture, env, run):
                    directory = Path(fixture.directory.name)
                    transition = fixture.empty_transition_path
                    self.assertTrue(transition.is_absolute())
                    self.assertRegex(transition.name, r"^adtr-ldap-empty-transition-[A-Za-z0-9_-]+$")
                    self.assertNotEqual(transition, directory)
                    self.assertNotEqual(transition, Path(inherited))
                    self.assertEqual(stat.S_IMODE(transition.stat().st_mode), 0o700)
                    self.assertEqual(list(transition.iterdir()), [])
                    self.assertIsNone(fixture.control_path)
                    command = next(call.args[0] for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
                    mounts = [command[index + 1] for index, arg in enumerate(command) if arg == "-v"]
                    self.assertEqual(mounts, [f"{directory}:/fixture:ro", f"{transition}:/empty-transition:ro"])
                    self.assertEqual(command.count("-empty-transition-dir"), 1)
                    self.assertEqual(command[command.index("-empty-transition-dir") + 1], "/empty-transition")
                    self.assertLess(command.index(f"{transition}:/empty-transition:ro"), command.index("-cert"))
                    self.assertGreater(command.index("-empty-transition-dir"), command.index("-cert"))
                    self.assertNotIn("-control-dir", command)
                    self.assertEqual([command[index + 1] for index, arg in enumerate(command) if arg == "-e"],
                                     ["ADTR_LDAP_FIXTURE_USERNAME", "ADTR_LDAP_FIXTURE_PASSWORD"])
                    self.assertEqual({key: value for key, value in env.items() if "EMPTY_TRANSITION" in key},
                                     {"ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR": str(transition)})
                    self.assertNotIn("ADTR_DIRECTORY_READ_ENABLED", env)
                    self.assertNotIn("ADTR_DIRECTORY_READ_V3_ENABLED", env)
                    # Cleanup owns the whole private directory, including published/staged markers.
                    (transition / "empty-v1").write_bytes(b"empty-v1\n")
                    (transition / ".empty-v1.pending").write_bytes(b"empty-v1\n")
                self.assertFalse(transition.exists())
                self.assertFalse(directory.exists())
                self.assertEqual(unowned.read_bytes(), b"untouched")

    def test_simultaneous_transition_fixtures_never_share_a_directory(self):
        modes = {"directory_enabled": True, "directory_v3": True,
                 "computer_assets_v3": True, "empty_transition": True}
        first = ldap_e2e_fixture.LDAPFixture(**modes)
        try:
            second = ldap_e2e_fixture.LDAPFixture(**modes)
            try:
                self.assertNotEqual(first.empty_transition_path, second.empty_transition_path)
                (first.empty_transition_path / "empty-v1").write_bytes(b"empty-v1\n")
                self.assertEqual(list(second.empty_transition_path.iterdir()), [])
            finally:
                second.close()
        finally:
            first.close()

    def test_allocation_failure_cleans_any_previously_owned_directory(self):
        allocate = tempfile.TemporaryDirectory
        for fail_at in (1, 2):
            owned = []

            def fail_allocation(**options):
                if len(owned) + 1 == fail_at:
                    raise OSError("synthetic allocation failure")
                directory = allocate(**options)
                owned.append(directory)
                return directory

            with self.subTest(fail_at=fail_at), \
                 patch.object(ldap_e2e_fixture.tempfile, "TemporaryDirectory", side_effect=fail_allocation), \
                 patch.object(ldap_e2e_fixture.subprocess, "run") as run:
                with self.assertRaisesRegex(OSError, "synthetic allocation failure"):
                    ldap_e2e_fixture.LDAPFixture(directory_enabled=True, directory_v3=True,
                                                computer_assets_v3=True, empty_transition=True)
                self.assertTrue(all(not Path(directory.name).exists() for directory in owned))
                run.assert_not_called()

    def test_startup_failures_still_release_owned_transition_directory_on_close(self):
        failures = (["openssl"], ["go"], ["docker", "network", "create"], ["docker", "run"],
                    ["docker", "logs"], ["docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"])
        for prefix in failures:
            fixture = ldap_e2e_fixture.LDAPFixture(directory_enabled=True, directory_v3=True,
                                                 computer_assets_v3=True, empty_transition=True)
            directory = Path(fixture.directory.name)
            transition = fixture.empty_transition_path

            def run_command(command, **options):
                if command[:len(prefix)] == prefix:
                    raise subprocess.CalledProcessError(1, command)
                return subprocess.CompletedProcess(command, 0)

            def read_command(command, **options):
                if command[:len(prefix)] == prefix:
                    raise subprocess.CalledProcessError(1, command)
                return self.fixture_output(command, **options)

            with self.subTest(prefix=prefix), \
                 patch.object(ldap_e2e_fixture.subprocess, "run", side_effect=run_command) as run, \
                 patch.object(ldap_e2e_fixture.subprocess, "check_output", side_effect=read_command):
                try:
                    with self.assertRaises(subprocess.CalledProcessError):
                        fixture.start({})
                finally:
                    fixture.close()
                self.assertFalse(directory.exists())
                self.assertFalse(transition.exists())
                commands = [call.args[0] for call in run.call_args_list]
                self.assertEqual(["docker", "network", "rm", fixture.network] in commands, fixture.created_network)
                self.assertEqual(["docker", "rm", "--force", fixture.name] in commands, fixture.created_container)

    def test_docker_cleanup_failures_still_remove_transition_directory(self):
        for prefix in (["docker", "stop"], ["docker", "rm"], ["docker", "network", "rm"]):
            with self.subTest(prefix=prefix):
                with self.assertRaises(subprocess.CalledProcessError):
                    with self.started_fixture(directory_enabled=True, directory_v3=True,
                                              computer_assets_v3=True, empty_transition=True) as (fixture, _, run):
                        directory = Path(fixture.directory.name)
                        transition = fixture.empty_transition_path

                        def fail_cleanup(command, **options):
                            if command[:len(prefix)] == prefix:
                                raise subprocess.CalledProcessError(1, command)
                            return subprocess.CompletedProcess(command, 0)

                        run.side_effect = fail_cleanup
                self.assertFalse(directory.exists())
                self.assertFalse(transition.exists())
                self.assertEqual([call.args[0] for call in run.call_args_list[-3:]], [
                    ["docker", "stop", "--time", "10", fixture.name],
                    ["docker", "rm", "--force", fixture.name],
                    ["docker", "network", "rm", fixture.network],
                ])

    def test_fixture_directory_cleanup_failure_still_cleans_transition_directory(self):
        fixture = ldap_e2e_fixture.LDAPFixture(directory_enabled=True, directory_v3=True,
                                             computer_assets_v3=True, empty_transition=True)
        directory = Path(fixture.directory.name)
        transition = fixture.empty_transition_path
        cleanup = fixture.directory.cleanup

        def fail_cleanup():
            cleanup()
            raise OSError("synthetic cleanup failure")

        with patch.object(fixture.directory, "cleanup", side_effect=fail_cleanup):
            with self.assertRaisesRegex(OSError, "synthetic cleanup failure"):
                fixture.close()
        self.assertFalse(directory.exists())
        self.assertFalse(transition.exists())


if __name__ == "__main__":
    unittest.main()
