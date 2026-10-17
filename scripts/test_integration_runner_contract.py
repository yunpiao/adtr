"""Guard complete integration coverage and fail-closed test discovery."""
import contextlib
import io
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch

import run_integration as runner


class IntegrationRunnerContract(unittest.TestCase):
    def setUp(self):
        self.names = [f"TestCase{index}" for index in range(17)] + [
            "ExamplePermission", "FuzzPermission"]
        self.discovery = "\n".join(reversed(self.names)) + "\nBenchmarkPermission\nok  \tpackage\t1.040s\n"
        self.env = {"GO": "/verified/toolchain/go", "ADTR_TEST_DATABASE_URL": "synthetic-test-url"}

    def quiet(self):
        return contextlib.redirect_stdout(io.StringIO())

    def test_owned_rescue_runtime_is_required_in_non_auth_entry(self):
        source = Path('scripts/test_integration.py').read_text()
        self.assertIn('if args.suite in ("all", "non-auth"):', source)
        self.assertIn('subprocess.run(["make", "test-owned-rescue-ci"], check=True)', source)
        makefile = Path('Makefile').read_text()
        self.assertIn('ADTR_RUN_OWNED_RESCUE_TESTS=1 python3 scripts/test_owned_rescue_runner_runtime.py', makefile)

    def test_partitions_are_disjoint_exhaustive_and_deterministic(self):
        shards = runner.partition_auth_tests(self.discovery)
        self.assertEqual(len(shards), runner.AUTH_SHARDS)
        flattened = [name for shard in shards for name in shard]
        self.assertCountEqual(flattened, self.names)
        self.assertEqual(len(flattened), len(set(flattened)))
        self.assertTrue(all(shards))
        self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)
        self.assertEqual(shards, runner.partition_auth_tests("\n".join(self.names)))

    def test_eight_shards_split_each_previous_four_shard_without_moving_coverage(self):
        self.assertEqual(runner.AUTH_SHARDS, 8)
        # Exercise uneven discovery sizes as well as the current 297-test suite.
        for count in (8, 19, 296, 297, 298):
            names = sorted(f"TestCase{index}" for index in range(count))
            shards = runner.partition_auth_tests("\n".join(reversed(names)))
            for index in range(4):
                with self.subTest(count=count, old_shard=index + 1):
                    old = names[index::4]
                    self.assertEqual(shards[index], old[::2])
                    self.assertEqual(shards[index + 4], old[1::2])
                    self.assertFalse(set(shards[index]) & set(shards[index + 4]))
                    self.assertCountEqual(shards[index] + shards[index + 4], old)

    def test_workflow_matrix_matches_every_runner_shard_exactly_once(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        auth = workflow.split('  auth-integration:\n', 1)[1].split('  browser:\n', 1)[0]
        matrices = re.findall(r'^        shard: \[([0-9, ]+)\]$', auth, re.M)
        self.assertEqual(len(matrices), 1)
        self.assertEqual([int(value) for value in matrices[0].split(',')],
                         list(range(1, runner.AUTH_SHARDS + 1)))

    def test_discovery_rejects_missing_duplicate_ambiguous_and_empty_shards(self):
        for output in ("", "ok \tpackage\t1s", "TestOnly", self.discovery + "TestCase0\n",
                       self.discovery + "unexpected output\n", self.discovery + "TestShell;exit\n"):
            with self.subTest(output=output), self.assertRaises(ValueError):
                runner.partition_auth_tests(output)

    def test_auth_commands_select_exact_names_and_preserve_execution_flags(self):
        selected = []
        for shard in range(1, runner.AUTH_SHARDS + 1):
            with patch.object(runner.subprocess, "check_output", return_value=self.discovery) as discover, self.quiet():
                commands = runner.test_commands(runner.parse_args(["--suite", "auth", "--shard", str(shard)]), self.env)
            self.assertEqual(len(commands), 1)
            command = commands[0]
            self.assertEqual(command[:6], [self.env["GO"], "test", "-race", "-count=1", "-tags=integration", "-timeout=10m"])
            self.assertEqual(command[-1], runner.AUTH_PACKAGE)
            pattern = command[command.index("-run") + 1]
            matches = [name for name in self.names if re.fullmatch(pattern, name)]
            self.assertTrue(matches)
            self.assertIsNone(re.fullmatch(pattern, matches[0] + "Extra"))
            self.assertIsNone(re.fullmatch(pattern, "Prefix" + matches[0]))
            selected.extend(matches)
            self.assertEqual(discover.call_args.args[0][-3:], ["-list", ".", runner.AUTH_PACKAGE])
            self.assertNotIn("shell", discover.call_args.kwargs)
        self.assertCountEqual(selected, self.names)
        self.assertEqual(len(selected), len(set(selected)))

    def test_default_preserves_all_other_packages_and_every_auth_shard(self):
        packages = ["example/cmd/service", runner.AUTH_PACKAGE, "example/internal/newpackage"]
        with patch.object(runner.subprocess, "check_output", side_effect=["\n".join(packages), self.discovery]), self.quiet():
            commands = runner.test_commands(runner.parse_args([]), self.env)
        self.assertEqual(len(commands), 3 + runner.AUTH_SHARDS)
        self.assertEqual(commands[0][-2:], [packages[0], packages[2]])
        self.assertNotIn(runner.AUTH_PACKAGE, commands[0])
        self.assertEqual(commands[1], [self.env["GO"], "test", "-race", "-count=1",
                                      "-tags=integration,adtr_ci_dbadmission", "-timeout=10m",
                                      "./internal/dbadmission"])
        self.assertTrue(all(command[-1] == runner.AUTH_PACKAGE for command in commands[3:]))

    def test_candidate_verification_uses_isolated_fixture_and_exact_packages(self):
        packages = [runner.AUTH_PACKAGE, "example/internal/service"]
        with patch.object(runner.subprocess, "check_output", return_value="\n".join(packages)), self.quiet():
            commands = runner.test_commands(runner.parse_args(["--suite", "non-auth"]), self.env)
        self.assertEqual(commands[2], [self.env["GO"], "test", "-race", "-count=1",
                                      "-tags=integration,adtr_ci_dbadmission", "-timeout=10m",
                                      "-run", "^TestActivityCandidate", "./internal/tasks",
                                      "./internal/systemhealth", "./cmd/adtr"])

    def test_non_auth_always_includes_tagged_lifecycle_contracts(self):
        packages = [runner.AUTH_PACKAGE, "example/internal/service"]
        with patch.object(runner.subprocess, "check_output", return_value="\n".join(packages)), self.quiet():
            commands = runner.test_commands(runner.parse_args(["--suite", "non-auth"]), self.env)
        self.assertEqual(len(commands), 3)
        self.assertEqual(commands[1], [self.env["GO"], "test", "-race", "-count=1",
                                      "-tags=integration,adtr_ci_dbadmission", "-timeout=10m",
                                      "./internal/dbadmission"])

    def test_non_auth_rejects_incomplete_or_duplicate_package_discovery(self):
        for output in ("", "example/other", runner.AUTH_PACKAGE,
                       f"{runner.AUTH_PACKAGE}\n{runner.AUTH_PACKAGE}\nexample/other",
                       f"{runner.AUTH_PACKAGE}\nexample/other\nexample/other"):
            with self.subTest(output=output), patch.object(runner.subprocess, "check_output", return_value=output), self.assertRaises(ValueError):
                runner.test_commands(runner.parse_args(["--suite", "non-auth"]), self.env)

    def test_discovery_failure_never_runs_partial_suite(self):
        with patch.object(runner.subprocess, "check_output", side_effect=subprocess.CalledProcessError(1, ["go", "list"])), patch.object(runner.subprocess, "run") as run, self.assertRaises(subprocess.CalledProcessError):
            runner.run_tests(runner.parse_args([]), self.env)
        run.assert_not_called()

    def test_missing_database_is_failure_before_discovery(self):
        with patch.object(runner.subprocess, "check_output") as discover, self.assertRaisesRegex(ValueError, "missing PostgreSQL is not a pass"):
            runner.run_tests(runner.parse_args([]), {})
        discover.assert_not_called()

    def test_failed_group_does_not_skip_remaining_groups_or_report_success(self):
        commands = [["go", "test", f"package-{index}"] for index in range(5)]
        outcomes = [subprocess.CompletedProcess(command, int(index == 1)) for index, command in enumerate(commands)]
        with patch.object(runner, "test_commands", return_value=commands), patch.object(runner.subprocess, "run", side_effect=outcomes) as run, self.quiet():
            self.assertEqual(runner.run_tests(runner.parse_args([]), self.env), 1)
        self.assertEqual([call.args[0] for call in run.call_args_list], commands)
        self.assertTrue(all(call.kwargs == {"env": self.env, "check": False} for call in run.call_args_list))

    def test_invalid_shard_configuration_is_rejected(self):
        for argv in (["--suite", "auth"], ["--shard", "1"], ["--suite", "non-auth", "--shard", "1"],
                     ["--suite", "auth", "--shard", "0"], ["--suite", "auth", "--shard", "9"]):
            with self.subTest(argv=argv), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                runner.parse_args(argv)


if __name__ == "__main__":
    unittest.main()
