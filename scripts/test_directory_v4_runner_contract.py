"""Source-only wiring contracts, authored under execution HOLD; not acceptance."""
import ast
import contextlib
import hashlib
import io
import json
from pathlib import Path
import threading
import urllib.error
import unittest
from unittest.mock import Mock


BASE_RUNNER_SHA256 = "4dba719fa8f92273bf1eddf272d26cca18b5ebf2cc06ff92c752e4943babbe15"
ROOT = Path(__file__).resolve().parent.parent


def restore_v4_runner(text):
    deltas = json.loads((ROOT / "scripts/fixtures/directory_v4_runner_delta.json").read_text())
    for old, new in reversed(deltas):
        if not new or text.count(new) != 1:
            raise ValueError("v4 runner delta absent, duplicated or altered")
        text = text.replace(new, old)
    if hashlib.sha256(text.encode()).hexdigest() != BASE_RUNNER_SHA256:
        raise ValueError("non-v4 runner bytes changed")
    return text


def runner_function(name, **scope):
    # Compile the real function body only: importing the full runner would load
    # unrelated integration modules. No process, HTTP or fixture is launched.
    tree = ast.parse((ROOT / "scripts/test_auth_e2e.py").read_text())
    node = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == name)
    exec(compile(ast.Module(body=[node], type_ignores=[]), "<v4-runner-contract>", "exec"), scope)
    return scope[name]


class V4RunnerContracts(unittest.TestCase):
    def test_prior_runner_bytes_and_delta_mutations(self):
        source = (ROOT / "scripts/test_auth_e2e.py").read_text()
        restore_v4_runner(source)
        for old, new in json.loads((ROOT / "scripts/fixtures/directory_v4_runner_delta.json").read_text()):
            for changed in (source + new, source.replace(new, old), source.replace(new, new + "# drift\n")):
                with self.assertRaises(ValueError):
                    restore_v4_runner(changed)
        with self.assertRaises(ValueError):
            restore_v4_runner(source + "# unrelated drift\n")

    def test_exact_registration_nonempty_modes_and_budgets(self):
        source = (ROOT / "scripts/test_auth_e2e.py").read_text()
        tree = ast.parse(source)
        scope = {"RUNNING_CANCEL_SUITE": "legacy-user", "GROUP_RUNNING_CANCEL_SUITE": "legacy-group",
                 "COMPUTER_RUNNING_CANCEL_SUITE": "legacy-computer", "EXPORT_TERMINAL_SUITES": set(),
                 "COMPUTER_EXPORT_TERMINAL_SUITES": set(), "GROUP_EXPORT_TERMINAL_SUITES": set()}
        nodes = [n for n in tree.body if isinstance(n, (ast.Assign, ast.AugAssign, ast.Expr))]
        exec(compile(ast.Module(body=nodes, type_ignores=[]), "<v4-sets>", "exec"), scope)
        self.assertEqual(scope["V4_CONTROLS_BUDGETS"], {
            "directory-v4-controls": 660, "directory-v4-cancel": 930, "directory-v4-readers": 660})
        for suite in scope["V4_CONTROLS_BUDGETS"]:
            self.assertIn(suite, scope["DIRECTORY_SUITES"])
            self.assertIn(suite, scope["DIRECTORY_V4_SUITES"])
            self.assertNotIn(suite, scope["EMPTY_DIRECTORY_SUITES"])
            self.assertEqual(suite in scope["SLOW_DIRECTORY_SUITES"], suite == "directory-v4-cancel")
            self.assertEqual(suite in scope["REAL_TAB_LIFECYCLE_SUITES"], suite == "directory-v4-readers")
        self.assertIn("choices=[*V4_CONTROLS_BUDGETS,", source)
        self.assertIn("timeout=V4_CONTROLS_BUDGETS[args.suite]", source)

    def test_fixed_launch_is_only_exact_cancel_and_not_rescue(self):
        source = (ROOT / "scripts/test_auth_e2e.py").read_text()
        tree = ast.parse(source)
        gate = next(n for n in ast.walk(tree) if isinstance(n, ast.If)
                    and ast.unparse(n.test) == "args.suite == 'directory-v4-cancel'")
        launch = next(n for n in ast.walk(gate) if isinstance(n, ast.Lambda))
        self.assertEqual(ast.unparse(launch.body),
                         "subprocess.Popen([binary, '-mode', 'worker'], env=worker_env, stdout=logs, stderr=logs)")
        branch = "\n".join(ast.get_source_segment(source, n) for n in gate.body)
        self.assertNotIn("OwnedCIWorker", branch)
        self.assertNotIn("shell=", branch)
        self.assertIn('Path("bin/adtr").resolve(strict=True)', branch)
        self.assertIn('worker_env=dict(worker_env)', branch)
        self.assertIn("env.update(v4_worker_controller.open())", branch)
        self.assertIn('for key in (URL_ENV, TOKEN_ENV, "ADTR_E2E_V4_CONTROLS_SUITE"):', source)
        self.assertIn('not k.startswith("ADTR_E2E_")', source)

    def process(self, *, exited=None):
        process = Mock()
        process.poll.return_value = exited
        process.wait.return_value = 0 if exited is None else exited
        return process

    def cleanup(self, process):
        record = {}
        result = runner_function("cleanup_v4_process")(process, record)
        return result, record

    def test_no_child_means_no_signal_or_wait(self):
        self.assertEqual(self.cleanup(None)[0], (True, None, False, ()))

    def test_live_child_one_terminate_and_thirty_second_wait(self):
        process = self.process()
        self.assertEqual(self.cleanup(process)[0], (True, 0, False, ()))
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=30)
        process.kill.assert_not_called()

    def test_exited_child_reaped_without_signal(self):
        process = self.process(exited=7)
        result, _ = self.cleanup(process)
        self.assertEqual(result, (True, 7, False, ("process_nonzero_exit",)))
        process.terminate.assert_not_called()
        process.wait.assert_called_once_with(timeout=30)

    def test_terminate_failure_still_kills_and_reaps(self):
        process = self.process()
        process.terminate.side_effect = OSError("private sentinel")
        result, record = self.cleanup(process)
        self.assertEqual(result, (True, 0, True, ("process_graceful_cleanup",)))
        process.kill.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=10)
        self.assertTrue(record["forced_kill"])
        self.assertNotIn("private sentinel", repr(result))

    def test_wait_failure_uses_bounded_kill_and_failure_is_retained(self):
        process = self.process()
        process.wait.side_effect = [TimeoutError(), -9]
        result, _ = self.cleanup(process)
        self.assertEqual(result[:3], (True, -9, True))
        self.assertEqual([c.kwargs["timeout"] for c in process.wait.call_args_list], [30, 10])

    def test_failed_kill_or_reap_never_claims_reaped(self):
        for operation in ("kill", "wait"):
            process = self.process()
            process.terminate.side_effect = OSError()
            getattr(process, operation).side_effect = OSError()
            process.wait.side_effect = OSError()
            result, _ = self.cleanup(process)
            self.assertFalse(result[0])
            self.assertIn("process_reap", result[3])

    def test_real_transfer_precedes_diagnostics_and_other_cleanup_continues(self):
        events = []
        worker, server = self.process(), self.process()
        worker.terminate.side_effect = OSError("private")
        worker.kill.side_effect = OSError("private")
        worker.wait.side_effect = OSError("private")
        controller = Mock()
        transfer = Mock(process=worker, failures=("readiness_join_failed",))
        controller.seal_and_take.side_effect = lambda: (events.append("seal"), transfer)[1]
        controller.finish_after_wait.return_value = False
        diagnostics = Mock()
        diagnostics.report = {"processes": {"worker": {}, "api": {}}, "collection_errors": []}
        diagnostics.snapshot.side_effect = lambda s, w: events.append(("snapshot", s is server, w is worker))
        ldap, logs, remove = Mock(), Mock(), Mock()
        cleanup = runner_function("cleanup_v4_run",
            cleanup_v4_process=runner_function("cleanup_v4_process"),
            http_handler_panicked=lambda _: False, remove_owned_container=remove,
            sys=Mock(stderr=io.StringIO()))
        failures = cleanup(controller, None, server, diagnostics, logs, ldap, None, True, "owned", "nonce")
        self.assertEqual(events[:2], ["seal", ("snapshot", True, True)])
        controller.seal_and_take.assert_called_once_with()
        controller.finish_after_wait.assert_called_once_with(reaped=False, returncode=None, forced_kill=True)
        server.terminate.assert_called_once_with()
        ldap.close.assert_called_once_with()
        logs.close.assert_called_once_with()
        remove.assert_called_once_with("owned", "nonce")
        self.assertIn("process_reap", failures)
        self.assertNotIn("private", repr(failures))

    def test_readiness_cancellation_and_deadline_prevent_io(self):
        for cancel, deadline, exited in ((True, 20, None), (False, 0, None), (False, 20, 1)):
            clock, urlopen = Mock(), Mock()
            clock.monotonic.return_value = 1
            event = threading.Event()
            if cancel:
                event.set()
            factory = Mock(side_effect=lambda launch, ready: ready)
            urllib = Mock()
            urllib.request.urlopen = urlopen
            ready = runner_function("v4_delayed_worker", time=clock, urllib=urllib,
                                    DelayedOwnedWorker=factory)(Mock(), "fixed")
            self.assertFalse(ready(self.process(exited=exited), event, deadline))
            urlopen.assert_not_called()

    def test_first_error_preserved_and_cleanup_failure_fails_clean_run(self):
        source = (ROOT / "scripts/test_auth_e2e.py").read_text()
        self.assertIn("primary_error = sys.exc_info()[0] is not None", source)
        self.assertIn("if failures and not primary_error:", source)
        self.assertIn('raise RuntimeError("directory v4 owned cleanup failed")', source)
        self.assertIn('state != {"state": "ready", "launchCount": 1, "failure": None}', source)

    def test_readiness_checks_after_io_and_bounds_probe_timeout(self):
        for outcome in ("ready", "cancelled", "deadline", "exited"):
            clock = Mock()
            clock.monotonic.side_effect = [1, 3 if outcome == "deadline" else 1.1]
            event = threading.Event()
            process = self.process()
            process.poll.side_effect = [None, 1 if outcome == "exited" else None]
            response = Mock(status=200)
            manager = Mock()
            manager.__enter__ = Mock(return_value=response)
            manager.__exit__ = Mock(return_value=False)
            def probe(*args, **kwargs):
                if outcome == "cancelled":
                    event.set()
                return manager
            urlopen = Mock(side_effect=probe)
            network = Mock()
            network.request.urlopen = urlopen
            network.error = urllib.error
            ready = runner_function("v4_delayed_worker", time=clock, urllib=network,
                DelayedOwnedWorker=lambda launch, ready: ready)(Mock(), "fixed")
            self.assertEqual(ready(process, event, 1.5), outcome == "ready")
            urlopen.assert_called_once_with("fixed", timeout=.5)

    def test_readiness_keeps_original_hundred_probe_limit(self):
        clock, network, event = Mock(), Mock(), Mock()
        clock.monotonic.return_value = 1
        event.is_set.return_value = False
        event.wait.return_value = False
        network.error = urllib.error
        network.request.urlopen.side_effect = OSError("private")
        ready = runner_function("v4_delayed_worker", time=clock, urllib=network,
            DelayedOwnedWorker=lambda launch, ready: ready)(Mock(), "fixed")
        self.assertFalse(ready(self.process(), event, 200))
        self.assertEqual(network.request.urlopen.call_count, 100)
        self.assertEqual(event.wait.call_count, 100)
        self.assertTrue(all(c.args == (.1,) for c in event.wait.call_args_list))

    def test_primary_exception_survives_actual_finally_branch(self):
        tree = ast.parse((ROOT / "scripts/test_auth_e2e.py").read_text())
        main = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == "main")
        outer = next(n for n in main.body if isinstance(n, ast.Try))
        branch = outer.finalbody[0]
        for primary in (False, True):
            diagnostics = Mock()
            scope = dict(args=Mock(suite="directory-v4-cancel"),
                         V4_CONTROLS_BUDGETS={"directory-v4-cancel": 930},
                         cleanup_v4_run=Mock(return_value=["process_reap"]), diagnostics=diagnostics,
                         v4_worker_controller=None, worker=None, server=None, logs=None,
                         ldap_fixture=None, message_fixture=None, creation_attempted=False,
                         name="owned", owner="nonce", diagnostic_dir=Path("/unused"))
            import sys
            scope["sys"] = sys
            compiled = compile(ast.Module(body=[branch], type_ignores=[]), "<actual-finally>", "exec")
            if primary:
                original = AssertionError("original scenario failure")
                try:
                    try:
                        raise original
                    finally:
                        exec(compiled, scope)
                except AssertionError as observed:
                    self.assertIs(observed, original)
            else:
                with self.assertRaisesRegex(RuntimeError, "directory v4 owned cleanup failed"):
                    exec(compiled, scope)

    def test_actual_chromium_boundary_and_explicit_browser_budgets(self):
        source = (ROOT / "web/playwright.config.ts").read_text()
        self.assertIn('["directory-v4-controls", 630_000]', source)
        self.assertIn('["directory-v4-cancel", 900_000]', source)
        self.assertIn('["directory-v4-readers", 630_000]', source)
        self.assertIn("globalTimeout: v4Budget", source)
        self.assertIn("timeout: v4Budget ?? 120_000", source)
        launch = source.split("launchOptions: {", 1)[1]
        self.assertIn("env: chromiumEnvironment()", launch)
        self.assertIn("!/^(ADTR_|POSTGRES_|PG)/i.test(entry[0])", source)
        self.assertIn('trace: v4Controls ? "off" : "retain-on-failure"', source)
        self.assertIn('screenshot: v4Controls ? "off" : "only-on-failure"', source)
        self.assertIn('video: "off"', source)
        self.assertIn('process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1"', source)


if __name__ == "__main__":
    unittest.main()
