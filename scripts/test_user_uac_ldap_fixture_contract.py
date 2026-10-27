"""C-only launcher lifecycle contracts; subprocesses are explicitly mocked."""
import ast
import hashlib
import inspect
import json
import os
from pathlib import Path
import stat
import traceback
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
                expected_stage = {"stop": "ldap_stop", "inspect": "ldap_inspect", "exit": "ldap_exit", "read": "ldap_evidence_json", "container": "ldap_container_remove", "network": "ldap_network_remove", "directory": "ldap_private_remove", "uac_directory": "ldap_private_remove"}[failure]
                with patch.object(target, "cleanup", side_effect=cleanup_then_fail):
                    with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$") as first:
                        fixture.close()
                self.assertEqual(module.uac_ldap_failure_stage(first.exception), expected_stage)
                self.assertIsNone(fixture.retained_uac_evidence)
                calls = len(run.call_args_list)
                with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$") as repeated:
                    fixture.close()
                self.assertIsNot(first.exception, repeated.exception)
                self.assertEqual(module.uac_ldap_failure_stage(repeated.exception), expected_stage)
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
                entered = []
                try:
                    with self.assertRaises((ValueError, OSError)):
                        fixture._read_uac_evidence(_entered=entered.append)
                    expected_stage = {"duplicate": "ldap_evidence_json", "boolean": "ldap_evidence_counts", "nonce": "ldap_evidence_schema", "hash": "ldap_evidence_binding", "unknown": "ldap_evidence_schema", "inflight": "ldap_evidence_counts", "sequence": "ldap_evidence_sequence", "poison": "ldap_evidence_state"}.get(mutation, "ldap_evidence_file")
                    self.assertEqual(entered[-1], expected_stage)
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

    def test_owned_stage_accessor_rejects_forgery_and_hostile_types_without_hooks(self):
        def forbidden(*args, **kwargs):
            raise AssertionError("diagnostic inspected a hostile value")
        class HostileMeta(type):
            __eq__ = forbidden
            __hash__ = forbidden
        class HostileError(RuntimeError, metaclass=HostileMeta):
            __str__ = forbidden
            __repr__ = forbidden
            __getattribute__ = forbidden
            args = property(forbidden)
        class HostileString(str):
            __hash__ = forbidden
            __eq__ = forbidden
        class HostileDict(dict):
            get = forbidden
            __getitem__ = forbidden
        for error in (HostileError(), ValueError("uac_evidence_invalid:cleanup"), object(), None):
            self.assertIsNone(module.uac_ldap_failure_stage(error))
        for args in (("uac_evidence_invalid:cleanup\nPRIVATE",), ("uac_evidence_invalid:cleanup", "PRIVATE"), (HostileString("uac_evidence_invalid:cleanup"),), (), (object(),)):
            error = module._uac_ldap_error("ldap_stop")
            error.args = args
            self.assertIsNone(module.uac_ldap_failure_stage(error))
        forged = RuntimeError("uac_evidence_invalid:cleanup")
        forged._uac_ldap_tag = (object(), "ldap_stop")
        self.assertIsNone(module.uac_ldap_failure_stage(forged))
        for tag in (None, [], (object(),), (module._UAC_LDAP_MARKER, "ldap_stop\nPRIVATE"), (module._UAC_LDAP_MARKER, HostileString("ldap_stop"))):
            forged._uac_ldap_tag = tag
            self.assertIsNone(module.uac_ldap_failure_stage(forged))
        forged.__dict__ = HostileDict(_uac_ldap_tag=(module._UAC_LDAP_MARKER, "ldap_stop"))
        self.assertIsNone(module.uac_ldap_failure_stage(forged))
        class CollidingKey:
            def __hash__(self):
                return hash("_uac_ldap_tag")
            __eq__ = forbidden
        forged.__dict__ = {CollidingKey(): None}
        self.assertIsNone(module.uac_ldap_failure_stage(forged))
        for stage in module._UAC_LDAP_STAGES:
            first, second = module._uac_ldap_error(stage), module._uac_ldap_error(stage)
            self.assertIsNot(first, second)
            self.assertIs(type(first), RuntimeError)
            self.assertEqual(first.args, ("uac_evidence_invalid:cleanup",))
            self.assertEqual(module.uac_ldap_failure_stage(first), stage)
        for stage in (HostileString("ldap_stop"), object(), "ldap_stop\nPRIVATE"):
            self.assertIsNone(module.uac_ldap_failure_stage(module._uac_ldap_error(stage)))

    def test_actual_reader_each_entered_stage_keeps_baseexception_policy_and_cleanup(self):
        reader_stages = ("ldap_evidence_open", "ldap_evidence_file", "ldap_evidence_read", "ldap_evidence_json", "ldap_evidence_schema", "ldap_evidence_binding", "ldap_evidence_counts", "ldap_evidence_state", "ldap_evidence_sequence")
        class HostileInterrupt(BaseException):
            def __str__(self):
                raise AssertionError("raw exception formatted")
            __repr__ = __str__
        for stage in reader_stages:
            with self.subTest(stage=stage), patch.object(module.subprocess, "run") as run, patch.object(module.subprocess, "check_output", side_effect=output):
                fixture = module.LDAPFixture(**BASE)
                fixture.start({"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": NONCE})
                write_receipts(fixture)
                paths = (Path(fixture.directory.name), Path(fixture.uac_evidence_directory.name))
                original = fixture._read_uac_evidence
                visited = []
                def read_with_fault(*, _entered):
                    def enter(value):
                        visited.append(value)
                        _entered(value)
                        if value == stage:
                            raise HostileInterrupt("PRIVATE BYTES")
                    return original(_entered=enter)
                with patch.object(fixture, "_read_uac_evidence", side_effect=read_with_fault):
                    with self.assertRaisesRegex(RuntimeError, "^uac_evidence_invalid:cleanup$") as failed:
                        fixture.close()
                self.assertEqual(visited[-1], stage)
                self.assertEqual(module.uac_ldap_failure_stage(failed.exception), stage)
                self.assertIsNone(fixture.retained_uac_evidence)
                self.assertTrue(all(not path.exists() for path in paths))
                commands = [call.args[0] for call in run.call_args_list]
                self.assertIn(["docker", "rm", "--force", fixture.name], commands)
                self.assertIn(["docker", "network", "rm", fixture.network], commands)
                # from None suppresses displayed chaining, not physical context.
                self.assertTrue(failed.exception.__suppress_context__)
                rendered = "".join(traceback.format_exception(failed.exception))
                self.assertNotIn("PRIVATE BYTES", rendered)

    def test_first_failure_sticky_after_every_later_removal_and_repeated_close(self):
        for failure in ("stop", "inspect", "exit", "retained", "container", "network", "private"):
            with self.subTest(failure=failure), patch.object(module.subprocess, "run") as run, patch.object(module.subprocess, "check_output", side_effect=output) as inspect_exit:
                fixture = module.LDAPFixture(**BASE)
                fixture.start({"ADTR_LDAP_FIXTURE_UAC_RUN_NONCE": NONCE})
                write_receipts(fixture)
                order = []
                def fail_command(command, **kwargs):
                    name = "stop" if command[1] == "stop" else "container" if command[1] == "rm" else "network"
                    order.append(name)
                    if name == failure or name in ("container", "network") and failure not in ("private", "network") or name == "network" and failure != "private":
                        raise KeyboardInterrupt("PRIVATE command")
                run.side_effect = fail_command
                if failure == "inspect":
                    inspect_exit.side_effect = SystemExit("PRIVATE inspect")
                elif failure == "exit":
                    inspect_exit.side_effect = lambda *args, **kwargs: "137"
                original_read = fixture._read_uac_evidence
                def read(*, _entered):
                    if failure == "retained":
                        raise GeneratorExit("PRIVATE reader")
                    return original_read(_entered=_entered)
                def remove_then_fail(directory, name):
                    original_cleanup = directory.cleanup
                    def remove():
                        order.append(name)
                        original_cleanup()
                        raise SystemExit("PRIVATE removal")
                    return remove
                with patch.object(fixture, "_read_uac_evidence", side_effect=read), patch.object(fixture.directory, "cleanup", side_effect=remove_then_fail(fixture.directory, "directory")), patch.object(fixture.uac_evidence_directory, "cleanup", side_effect=remove_then_fail(fixture.uac_evidence_directory, "evidence")):
                    with self.assertRaises(RuntimeError) as failed:
                        fixture.close()
                expected = {"retained": "ldap_retained_read", "container": "ldap_container_remove", "network": "ldap_network_remove", "private": "ldap_private_remove"}.get(failure, "ldap_" + failure)
                self.assertEqual(module.uac_ldap_failure_stage(failed.exception), expected)
                self.assertEqual(order, ["stop", "container", "network", "directory", "evidence"])
                self.assertIsNone(fixture.retained_uac_evidence)
                with self.assertRaises(RuntimeError) as repeated:
                    fixture.close()
                self.assertIsNot(failed.exception, repeated.exception)
                self.assertEqual(module.uac_ldap_failure_stage(repeated.exception), expected)
                self.assertEqual(order, ["stop", "container", "network", "directory", "evidence"])

    def test_actual_reader_default_exception_and_success_identity_unchanged(self):
        fixture = module.LDAPFixture(**BASE)
        fixture._uac_nonce = NONCE
        fixture.uac_evidence_directory = tempfile.TemporaryDirectory(prefix="adtr-uac-test-")
        try:
            expected = write_receipts(fixture)
            entered = []
            self.assertEqual(fixture._read_uac_evidence(), expected)
            self.assertEqual(fixture._read_uac_evidence(_entered=entered.append), expected)
            self.assertEqual(entered, ["ldap_evidence_open", "ldap_evidence_file", "ldap_evidence_read", "ldap_evidence_file", "ldap_evidence_read", "ldap_evidence_json", "ldap_evidence_schema", "ldap_evidence_binding", "ldap_evidence_counts", "ldap_evidence_state", "ldap_evidence_sequence"])
            for error in (OSError("PRIVATE path"), KeyboardInterrupt("PRIVATE interrupt")):
                with patch.object(module.os, "open", side_effect=error):
                    with self.assertRaises(type(error)) as caught:
                        fixture._read_uac_evidence()
                    self.assertIs(caught.exception, error)
        finally:
            fixture.directory.cleanup()
            fixture.uac_evidence_directory.cleanup()

    def test_all_other_fixture_methods_are_byte_bound_to_published_tree242(self):
        # Frozen published a062/tree242: all original methods outside the two
        # C-only read/close implementations, including non-C close, stay exact.
        source = inspect.getsource(module.LDAPFixture)
        node = ast.parse(source).body[0]
        lines = source.splitlines(keepends=True)
        unchanged = []
        for method in node.body:
            if isinstance(method, (ast.FunctionDef, ast.AsyncFunctionDef)) and method.name not in ("_read_uac_evidence", "_close_uac"):
                start = min([method.lineno] + [item.lineno for item in method.decorator_list])
                unchanged.extend(lines[start - 1:method.end_lineno])
        self.assertEqual(hashlib.sha256("".join(unchanged).encode()).hexdigest(), "0889b1d33c2f7162cd4c96443b4a1a5ca836a2399018e6af26d611faff69b80c")
        self.assertEqual(module._UAC_LDAP_STAGES, frozenset({"ldap_stop", "ldap_inspect", "ldap_exit", "ldap_retained_read", "ldap_container_remove", "ldap_network_remove", "ldap_private_remove", "ldap_evidence_open", "ldap_evidence_file", "ldap_evidence_read", "ldap_evidence_json", "ldap_evidence_schema", "ldap_evidence_binding", "ldap_evidence_counts", "ldap_evidence_state", "ldap_evidence_sequence"}))

    def test_suppressed_chain_is_not_claimed_to_erase_physical_context(self):
        try:
            try:
                raise ValueError("PRIVATE original context")
            except ValueError:
                raise module._uac_ldap_error("ldap_stop") from None
        except RuntimeError as error:
            self.assertIs(type(error.__context__), ValueError)
            self.assertTrue(error.__suppress_context__)
            self.assertIsNone(error.__cause__)
            self.assertEqual(module.uac_ldap_failure_stage(error), "ldap_stop")
            self.assertNotIn("PRIVATE original context", "".join(traceback.format_exception(error)))

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
