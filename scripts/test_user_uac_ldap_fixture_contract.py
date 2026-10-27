"""C-only launcher lifecycle contracts; subprocesses are explicitly mocked."""
import hashlib
import inspect
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ldap_e2e_fixture as module

NONCE = "0123456789abcdef0123456789abcdef"
PROFILE = "mixed-33-v1"
BASE = dict(directory_enabled=True, directory_v3=True, user_uac_v3_profile=PROFILE)


def output(command, **options):
    if command[:2] == ["docker", "logs"]:
        return "synthetic LDAP fixture ready\n"
    if command[3] == "{{.State.ExitCode}}":
        return "0\n"
    return "172.29.0.2\n"


def write_receipts(fixture):
    candidate = dict(format=1, profile=PROFILE, nonce=NONCE, transport="starttls", claimOrdinal=1,
                     entryCount=51, pageCount=4, completionSequence=123, pages=[])
    candidate_bytes = json.dumps(candidate).encode()
    seal = dict(format=1, profile=PROFILE, nonce=NONCE,
                candidateTranscriptSHA256=hashlib.sha256(candidate_bytes).hexdigest(), claimCount=1,
                completedExchangeCount=1, directoryAttemptCount=4, pageCount=4, entryCount=51,
                activeHandlers=0, inflightWrites=0, listenersClosed=True, participantClosed=True,
                poisoned=False, poisonCode="none", lateDirectoryAttempts=0, participantRebinds=0,
                terminalState="sealed_ok", completionSequence=123, finalSequence=125)
    seal_bytes = json.dumps(seal).encode()
    root = Path(fixture.uac_evidence_directory.name)
    for name, value in (("candidate-wire.json", candidate_bytes), ("terminal-seal.json", seal_bytes)):
        (root / name).write_bytes(value)
        (root / name).chmod(0o600)
    return candidate_bytes, seal_bytes, 0


class UACLDAPFixtureContract(unittest.TestCase):
    def test_keyword_only_closed_profile_and_no_invalid_allocation(self):
        parameter = inspect.signature(module.LDAPFixture).parameters["user_uac_v3_profile"]
        self.assertEqual(parameter.kind, inspect.Parameter.KEYWORD_ONLY)
        self.assertEqual(parameter.default, "")
        for value in (None, False, 0, [], {}, "MIXED-33-V1", "mixed-33-v2", "true"):
            with self.subTest(value=value), patch.object(module.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaisesRegex(ValueError, "^uac_evidence_invalid:configuration$"):
                    module.LDAPFixture(user_uac_v3_profile=value)
                allocate.assert_not_called()
        for name in ("control_enabled", "directory_empty", "directory_slow", "directory_v2", "directory_v4",
                     "user_assets_v2", "computer_assets_v3", "group_assets_v3", "empty_transition",
                     "group_members_v4_pagination", "group_members_v4_refresh", "user_export_failure_v3",
                     "group_export_failure_v3", "computer_export_failure_v3", "directory_enabled", "directory_v3"):
            mode = dict(BASE, **{name: name not in ("directory_enabled", "directory_v3")})
            with self.subTest(mode=mode), patch.object(module.tempfile, "TemporaryDirectory") as allocate:
                with self.assertRaises(ValueError):
                    module.LDAPFixture(**mode)
                allocate.assert_not_called()

    def test_c_boolean_types_and_allocation_errors_are_fixed(self):
        for name in ("control_enabled", "directory_enabled", "directory_v3", "directory_slow"):
            for value in (0, 1, "true", None):
                with self.subTest(name=name, value=value), patch.object(module.tempfile, "TemporaryDirectory") as allocate:
                    with self.assertRaisesRegex(ValueError, "^uac_evidence_invalid:configuration$"):
                        module.LDAPFixture(**dict(BASE, **{name: value}))
                    allocate.assert_not_called()
        with patch.object(module.tempfile, "TemporaryDirectory", side_effect=OSError("PRIVATE FAILURE")):
            with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:configuration$"):
                module.LDAPFixture(**BASE)

    def test_default_allocates_no_uac_and_removes_inherited_values(self):
        with patch.object(module.subprocess, "run") as run, patch.object(module.subprocess, "check_output", side_effect=output):
            fixture = module.LDAPFixture()
            try:
                self.assertIsNone(fixture.uac_evidence_directory)
                env = {name: "inherited" for name in ("ADTR_LDAP_FIXTURE_UAC_RUN_NONCE", "ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE", "ADTR_E2E_LDAP_UAC_EVIDENCE_DIR", "ADTR_E2E_USER_ASSET_EXPORT_V3_UAC")}
                with patch.dict(os.environ, ADTR_LDAP_FIXTURE_UAC_RUN_NONCE=NONCE):
                    fixture.start(env)
                for name in list(env):
                    self.assertNotIn("UAC", name)
                launch = next(call for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
                self.assertNotIn("ADTR_LDAP_FIXTURE_UAC_RUN_NONCE", launch.kwargs["env"])
                self.assertNotIn("-user-uac-v3-profile", launch.args[0])
                self.assertNotIn("-user-uac-evidence-dir", launch.args[0])
                self.assertFalse(any(":/uac-evidence:" in value for value in launch.args[0]))
            finally:
                fixture.close()

    def test_nonce_required_before_c_resource_or_subprocess(self):
        for nonce in (None, "", "F" * 32, "f" * 31, "f" * 33, 1):
            fixture = module.LDAPFixture(**BASE)
            try:
                with patch.object(module.tempfile, "TemporaryDirectory") as allocate, patch.object(module.subprocess, "run") as run:
                    with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:configuration$"):
                        fixture.start({"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": nonce})
                    allocate.assert_not_called()
                    run.assert_not_called()
            finally:
                fixture.close()

    def test_exact_flags_nonce_mount_and_retention_only_after_cleanup(self):
        with patch.object(module.subprocess, "run") as run, patch.object(module.subprocess, "check_output", side_effect=output):
            fixture = module.LDAPFixture(**BASE)
            self.assertIsNone(fixture.retained_uac_evidence)
            env = {"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": NONCE, "ADTR_E2E_LDAP_UAC_EVIDENCE_DIR": "/unowned"}
            fixture.start(env)
            owned = Path(fixture.uac_evidence_directory.name)
            self.assertEqual(stat.S_IMODE(owned.stat().st_mode), 0o700)
            self.assertEqual(env["ADTR_E2E_LDAP_UAC_EVIDENCE_DIR"], str(owned))
            self.assertEqual(env["ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE"], PROFILE)
            launch = next(call for call in run.call_args_list if call.args[0][:2] == ["docker", "run"])
            command = launch.args[0]
            self.assertIn(f"{owned}:/uac-evidence:rw", command)
            self.assertEqual(command[command.index("-user-uac-v3-profile") + 1], PROFILE)
            self.assertEqual(command[command.index("-user-uac-evidence-dir") + 1], "/uac-evidence")
            self.assertEqual(launch.kwargs["env"]["ADTR_LDAP_FIXTURE_UAC_RUN_NONCE"], NONCE)
            self.assertNotIn(NONCE, command)
            expected = write_receipts(fixture)
            self.assertIsNone(fixture.retained_uac_evidence)
            self.assertIsNone(fixture.close())
            self.assertFalse(owned.exists())
            self.assertEqual(fixture.retained_uac_evidence, expected)
            with self.assertRaises(AttributeError):
                fixture.retained_uac_evidence = expected
            calls = len(run.call_args_list)
            fixture.close()
            self.assertEqual(calls, len(run.call_args_list))

    def test_every_failure_clears_retention_and_continues_all_cleanup(self):
        for failure in ("stop", "inspect", "exit", "read", "container", "network", "directory", "uac_directory"):
            with self.subTest(failure=failure), patch.object(module.subprocess, "run") as run, patch.object(module.subprocess, "check_output", side_effect=output) as read:
                fixture = module.LDAPFixture(**BASE)
                fixture.start({"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": NONCE})
                write_receipts(fixture)
                directories = (Path(fixture.directory.name), Path(fixture.uac_evidence_directory.name))
                def fail_run(command, **kwargs):
                    target = "stop" if command[1] == "stop" else "container" if command[1] == "rm" else "network"
                    if failure == target:
                        raise RuntimeError("PRIVATE FAILURE DO NOT OUTPUT")
                run.side_effect = fail_run
                if failure == "inspect":
                    read.side_effect = RuntimeError("PRIVATE FAILURE DO NOT OUTPUT")
                elif failure == "exit":
                    read.side_effect = lambda *args, **kwargs: "137\n"
                if failure == "read":
                    (directories[1] / "terminal-seal.json").write_bytes(b"invalid")
                target = fixture.directory if failure == "directory" else fixture.uac_evidence_directory
                original_cleanup = target.cleanup
                def cleanup_then_fail():
                    original_cleanup()
                    if failure in ("directory", "uac_directory"):
                        raise RuntimeError("PRIVATE FAILURE DO NOT OUTPUT")
                with patch.object(target, "cleanup", side_effect=cleanup_then_fail):
                    with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$"):
                        fixture.close()
                self.assertIsNone(fixture.retained_uac_evidence)
                calls = len(run.call_args_list)
                with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$"):
                    fixture.close()
                self.assertEqual(calls, len(run.call_args_list))
                self.assertTrue(all(not path.exists() for path in directories))
                commands = [call.args[0] for call in run.call_args_list]
                self.assertIn(["docker", "rm", "--force", fixture.name], commands)
                self.assertIn(["docker", "network", "rm", fixture.network], commands)
                self.assertEqual([call.kwargs["timeout"] for call in run.call_args_list if call.args[0][1] in ("stop", "rm")][-2:], [20, 20])

    def test_safe_read_rejects_filesystem_and_seal_mutations(self):
        for mutation in ("missing", "symlink", "fifo", "directory", "mode", "oversize", "duplicate", "boolean", "nonce", "hash", "unknown", "inflight", "sequence", "poison"):
            with self.subTest(mutation=mutation):
                fixture = module.LDAPFixture(**BASE)
                fixture._uac_nonce = NONCE
                fixture.uac_evidence_directory = tempfile.TemporaryDirectory(prefix="adtr-uac-test-")
                write_receipts(fixture)
                path = Path(fixture.uac_evidence_directory.name) / "terminal-seal.json"
                if mutation in ("missing", "symlink", "fifo", "directory"):
                    path.unlink()
                    if mutation == "symlink": path.symlink_to("candidate-wire.json")
                    if mutation == "fifo": os.mkfifo(path, 0o600)
                    if mutation == "directory": path.mkdir()
                elif mutation == "mode": path.chmod(0o644)
                elif mutation == "oversize": path.write_bytes(b" " * 8193)
                elif mutation == "duplicate": path.write_text('{"format":1,"format":1}')
                else:
                    value = json.loads(path.read_bytes())
                    key, changed = {"boolean": ("entryCount", True), "nonce": ("nonce", "f" * 32), "hash": ("candidateTranscriptSHA256", "0" * 64), "unknown": ("extra", 1), "inflight": ("inflightWrites", 1), "sequence": ("finalSequence", 122), "poison": ("poisoned", True)}[mutation]
                    value[key] = changed
                    path.write_text(json.dumps(value))
                try:
                    with self.assertRaises((ValueError, OSError)):
                        fixture._read_uac_evidence()
                finally:
                    fixture.directory.cleanup()
                    fixture.uac_evidence_directory.cleanup()

    def test_startup_errors_are_fixed_and_uncertain_creates_still_cleaned(self):
        for stage in ("openssl", "network", "container"):
            fixture = module.LDAPFixture(**BASE)
            commands = []
            def command_run(command, **kwargs):
                commands.append(command)
                if stage == "openssl" and command[0] == "openssl" or stage == "network" and command[:3] == ["docker", "network", "create"] or stage == "container" and command[:2] == ["docker", "run"]:
                    raise subprocess.TimeoutExpired(command, kwargs["timeout"], output=b"PRIVATE FAILURE")
            with self.subTest(stage=stage), patch.object(module.subprocess, "run", side_effect=command_run), patch.object(module.subprocess, "check_output", side_effect=output):
                with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:configuration$"):
                    fixture.start({"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": NONCE})
                with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$"):
                    fixture.close()
                if stage in ("network", "container"):
                    self.assertIn(["docker", "network", "rm", fixture.network], commands)
                if stage == "container":
                    self.assertIn(["docker", "rm", "--force", fixture.name], commands)
                self.assertIsNone(fixture.retained_uac_evidence)
                self.assertFalse(Path(fixture.directory.name).exists())
                self.assertFalse(Path(fixture.uac_evidence_directory.name).exists())

    def test_go_source_join_and_preclear_invariants(self):
        root = Path(__file__).resolve().parents[1]
        server = (root / "internal/testldapfixture/server.go").read_text()
        self.assertLess(server.index("s.uacRecorder.BeginDrain()"), server.index("acceptors.Wait()"))
        self.assertLess(server.index("handlers.Wait()"), server.index("s.uacRecorder.SealAfterJoin()"))
        body = server[server.index("for range maxRequests"):]
        self.assertLess(body.index("ObserveRequest"), body.index("directory.matches(req)"))
        self.assertLess(body.index("ObserveRequest"), body.index("clear(req.packet)"))
        fixture = (root / "internal/testldapfixture/user_uac_v3.go").read_text()
        self.assertNotIn("ReadFile", fixture)
        self.assertNotIn("literal-oracle", fixture)


if __name__ == "__main__":
    unittest.main()
