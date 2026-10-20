"""Offline controller contracts: fake children, loopback only, no project runtime.

Authored under execution HOLD; not executed. Discovery: scripts/*_contract.py.
The owner adapter below MODELS required runner edits; it does not prove that the
existing runner, Node environment sanitizer, or browser integration is changed.
"""
import concurrent.futures
import contextlib
import io
import json
import socket
import subprocess
import threading
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

import directory_v4_worker_control as control


class Child:
    def __init__(self, *, exit_code=None, timeout=False, terminate_error=False):
        self.returncode = exit_code
        self.timeout = timeout
        self.terminate_error = terminate_error
        self.calls = []

    def poll(self):
        return self.returncode

    def terminate(self):
        self.calls.append("terminate")
        if self.terminate_error:
            raise OSError("private diagnostic must not escape")

    def kill(self):
        self.calls.append("kill")

    def wait(self, timeout):
        self.calls.append(("wait", timeout))
        if self.timeout and "kill" not in self.calls:
            raise subprocess.TimeoutExpired("private argv", timeout)
        if self.returncode is None:
            self.returncode = -9 if "kill" in self.calls else 0
        return self.returncode


def owner_cleanup(controller):
    """Test-only owner, NOT the controller. Required production runner ordering."""
    transfer = controller.seal_and_take()
    child = transfer.process
    forced = False
    reaped = child is None
    code = None
    if child is not None:
        try:
            if child.poll() is None:
                child.terminate()
            code = child.wait(timeout=30)
            reaped = True
        except (OSError, subprocess.TimeoutExpired):
            forced = True
            child.kill()
            code = child.wait(timeout=10)
            reaped = True
    clean = controller.finish_after_wait(reaped=reaped, returncode=code,
                                         forced_kill=forced)
    return transfer, clean


class WorkerControlContract(unittest.TestCase):
    def make(self, ready=None, launch=None, child=None):
        child = child or Child()
        controller = control.DelayedOwnedWorker(launch or (lambda: child),
                                               ready or (lambda *args: True))
        self.addCleanup(self.ensure_cleanup, controller)
        return controller, child

    def ensure_cleanup(self, controller):
        if not controller._taken:
            owner_cleanup(controller)

    def headers(self, controller):
        return [("Host", "127.0.0.1"),
                ("Authorization", "Bearer " + controller._token)]

    def start(self, controller):
        return controller.request("POST", "/start", self.headers(controller))

    def settle(self, controller):
        if controller._startup is not None:
            controller._startup.join(2)
            self.assertFalse(controller._startup.is_alive())

    def assert_owned_once(self, controller, child):
        self.assertEqual(controller.state()["launchCount"], 1)
        transfer, clean = owner_cleanup(controller)
        self.assertIs(transfer.process, child)
        self.assertEqual(transfer.failures, ())
        self.assertTrue(clean)
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])
        self.assertEqual(controller.state()["state"], "closed")

    def test_simultaneous_and_duplicate_starts_launch_exactly_once(self):
        controller, child = self.make()
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            statuses = list(pool.map(lambda _: self.start(controller)[0], range(48)))
        self.settle(controller)
        self.assertEqual(statuses, [200] * 48)
        self.assertEqual(controller.state()["state"], "ready")
        self.assert_owned_once(controller, child)
        with self.assertRaisesRegex(RuntimeError, "already transferred"):
            controller.seal_and_take()
        self.assertEqual(child.calls.count("terminate"), 1)

    def test_lost_start_response_reconciles_get_without_second_launch(self):
        controller, child = self.make()
        self.start(controller)  # Deliberately discard response; no second POST.
        self.settle(controller)
        code, state = controller.request("GET", "/state", self.headers(controller))
        self.assertEqual((code, state["state"], state["launchCount"]), (200, "ready", 1))
        self.assert_owned_once(controller, child)

    def test_invalid_authentication_never_spawns(self):
        for authorization in (None, "", "Bearer wrong", "Basic wrong", "Bearer  bad",
                              "Bearer\tbad", "Bearer é"):
            with self.subTest(authorization=authorization):
                controller, child = self.make()
                headers = [] if authorization is None else [("Authorization", authorization)]
                self.assertIn(controller.request("POST", "/start", headers)[0], (400, 401))
                self.assertEqual(controller.state()["launchCount"], 0)
                transfer, clean = owner_cleanup(controller)
                self.assertIsNone(transfer.process)
                self.assertTrue(clean)
                self.assertEqual(child.calls, [])

    def test_method_path_query_body_and_framing_fail_closed(self):
        cases = [("GET", "/start", [], b""), ("POST", "/state", [], b""),
                 ("DELETE", "/start", [], b""), ("HEAD", "/state", [], b""),
                 ("POST", "/start?x=1", [], b""), ("POST", "/start?", [], b""),
                 ("POST", "/start#fragment", [], b""), ("POST", "/stop", [], b""),
                 ("POST", "http://127.0.0.1/start", [], b""),
                 ("POST", "/start", [], b"x"),
                 ("POST", "/start", [], b"x" * 9000),
                 ("POST", "/start", [("Content-Length", "1")], b""),
                 ("POST", "/start", [("Content-Length", "9999999")], b""),
                 ("POST", "/start", [("Content-Length", "00")], b""),
                 ("POST", "/start", [("Content-Length", "-1")], b""),
                 ("POST", "/start", [("Transfer-Encoding", "chunked")], b""),
                 ("POST", "/start", [("Expect", "100-continue")], b""),
                 ("POST", "/start", [("X-Long", "x" * 9000)], b""),
                 ("POST", "/start", [("Bad Name", "x")], b""),
                 ("POST", "/start", [("X", "y\r\nAuthorization: other")], b"")]
        for method, target, extra, body in cases:
            with self.subTest(method=method, target=target, headers=extra[:1]):
                controller, child = self.make()
                code, _ = controller.request(method, target, self.headers(controller) + extra, body)
                self.assertGreaterEqual(code, 400)
                self.assertEqual(controller.state()["launchCount"], 0)
                self.assertTrue(owner_cleanup(controller)[1])
                self.assertEqual(child.calls, [])

    def test_duplicate_auth_and_content_length_are_rejected(self):
        for name in ("Authorization", "Content-Length", "Host"):
            controller, child = self.make()
            headers = self.headers(controller)
            if name == "Content-Length":
                headers.append((name, "0"))
            value = dict(headers)[name]
            headers.append((name.lower(), value))
            self.assertEqual(controller.request("POST", "/start", headers)[0], 400)
            self.assertEqual(controller.state()["launchCount"], 0)
            self.assertTrue(owner_cleanup(controller)[1])
            self.assertEqual(child.calls, [])

    def test_listener_thread_start_failure_closes_without_unstarted_join(self):
        controller, child = self.make()
        with patch.object(control.threading.Thread, "start", side_effect=RuntimeError("private")):
            with self.assertRaisesRegex(RuntimeError, "controller listener failed"):
                controller.open()
        self.assertIsNone(controller._listener)
        self.assertIsNone(controller._serving)
        self.assertEqual(controller.state()["state"], "failed")
        self.assertEqual(controller.state()["launchCount"], 0)
        transfer, clean = owner_cleanup(controller)
        self.assertIsNone(transfer.process)
        self.assertTrue(clean)
        self.assertEqual(child.calls, [])

    def test_startup_thread_start_failure_never_spawns(self):
        controller, child = self.make()
        with patch.object(control.threading.Thread, "start", side_effect=RuntimeError("private")):
            self.start(controller)
        self.assertEqual(controller.state()["state"], "failed")
        self.assertIsNone(controller._startup)
        self.assertEqual(controller.state()["launchCount"], 0)
        self.assertTrue(owner_cleanup(controller)[1])
        self.assertEqual(child.calls, [])

    def test_listener_failure_during_readiness_cannot_become_ready(self):
        entered, release = threading.Event(), threading.Event()
        def ready(*args):
            entered.set()
            return release.wait(2)
        controller, child = self.make(ready=ready)
        controller.open()
        self.start(controller)
        self.assertTrue(entered.wait(2))
        # A real owned listening socket failure, not a phase-only mock.
        controller._listener.close()
        controller._serving.join(2)
        self.assertFalse(controller._serving.is_alive())
        self.assertEqual(controller.state()["state"], "failed")
        release.set()
        self.settle(controller)
        self.assertEqual(controller.state()["state"], "failed")
        self.assertEqual(controller.state()["failure"], "listener_failed")
        self.start(controller)
        self.assert_owned_once(controller, child)

    def test_close_before_start_never_signals_a_child(self):
        controller, child = self.make()
        self.assertTrue(owner_cleanup(controller)[1])
        self.assertEqual(self.start(controller)[0], 409)
        self.assertEqual(controller.state()["launchCount"], 0)
        self.assertEqual(child.calls, [])

    def test_close_during_spawn_captures_created_handle_before_transfer(self):
        entered, release = threading.Event(), threading.Event()
        child = Child()
        def launch():
            entered.set()
            if not release.wait(2):
                raise RuntimeError("test spawn gate timed out")
            return child
        controller, _ = self.make(launch=launch, child=child)
        self.start(controller)
        self.assertTrue(entered.wait(2))
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            closing = pool.submit(owner_cleanup, controller)
            self.assertTrue(controller._cancelled.wait(2))
            release.set()
            transfer, clean = closing.result(timeout=4)
        self.assertIs(transfer.process, child)
        self.assertTrue(clean)
        self.assertEqual(controller.state()["launchCount"], 1)
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_close_cancels_readiness_without_mutex_deadlock(self):
        entered = threading.Event()
        observations = []
        def ready(process, cancelled, deadline):
            entered.set()
            cancelled_seen = cancelled.wait(2)
            # Observe from worker thread; assertions belong in the main thread.
            # Cancellation precedes the closing phase lock by design.
            observations.append((cancelled_seen, controller.state()["state"]))
            return False
        controller, child = self.make(ready=ready)
        self.start(controller)
        self.assertTrue(entered.wait(2))
        self.assert_owned_once(controller, child)
        self.assertEqual(len(observations), 1)
        self.assertTrue(observations[0][0])
        self.assertIn(observations[0][1], ("starting", "closing"))

    def test_startup_exit_stays_failed_and_retains_owned_child(self):
        controller, child = self.make(child=Child(exit_code=7))
        self.start(controller)
        self.settle(controller)
        self.assertEqual(controller.state()["state"], "failed")
        self.start(controller)
        self.assertEqual(controller.state()["launchCount"], 1)
        transfer, clean = owner_cleanup(controller)
        self.assertIs(transfer.process, child)
        self.assertFalse(clean)
        self.assertEqual(child.calls, [("wait", 30)])
        self.assertEqual(controller.state()["state"], "closed")

    def test_launch_failure_is_permanent_without_child_cleanup(self):
        def fail():
            raise OSError("secret launch diagnostic")
        controller, child = self.make(launch=fail)
        self.start(controller)
        self.settle(controller)
        self.start(controller)
        self.assertEqual(controller.state(), {"state": "failed", "launchCount": 0,
                                               "failure": "launch_failed"})
        self.assertTrue(owner_cleanup(controller)[1])
        self.assertEqual(child.calls, [])

    def test_readiness_timeout_and_exception_retain_one_child(self):
        def timeout(*args):
            raise TimeoutError("private ready URL")
        for ready in (lambda *args: False, timeout):
            controller, child = self.make(ready=ready)
            self.start(controller)
            self.settle(controller)
            self.assertEqual(controller.state()["state"], "failed")
            self.start(controller)
            self.assert_owned_once(controller, child)

    def test_late_readiness_success_cannot_override_deadline(self):
        def ready(*args):
            clock[0] += control.READY_SECONDS + 1
            return True
        clock = [1.0]
        controller, child = self.make(ready=ready)
        with patch.object(control.time, "monotonic", side_effect=lambda: clock[0]):
            self.start(controller)
            self.settle(controller)
        self.assertEqual(controller.state()["state"], "failed")
        self.assert_owned_once(controller, child)

    def test_child_exit_after_probe_is_not_ready(self):
        child = Child()
        def ready(*args):
            child.returncode = 1
            return True
        controller, _ = self.make(ready=ready, child=child)
        self.start(controller)
        self.settle(controller)
        self.assertEqual(controller.state()["state"], "failed")
        self.assertFalse(owner_cleanup(controller)[1])
        self.assertEqual(controller.state()["launchCount"], 1)
        self.assertEqual(child.calls, [("wait", 30)])

    def test_browser_timeout_or_assertion_preserves_failure_and_cleans_once(self):
        for failure in (AssertionError("browser assertion"),
                        subprocess.TimeoutExpired("browser", 930)):
            controller, child = self.make()
            caught = None
            try:
                self.start(controller)
                self.settle(controller)
                raise failure
            except (AssertionError, subprocess.TimeoutExpired) as error:
                caught = error
            finally:
                transfer, clean = owner_cleanup(controller)
            self.assertIs(caught, failure)
            self.assertIs(transfer.process, child)
            self.assertTrue(clean)
            self.assertEqual(controller.state()["launchCount"], 1)
            self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_graceful_timeout_and_terminate_error_fail_cleanup_without_double_owner(self):
        for child in (Child(timeout=True), Child(terminate_error=True)):
            controller, _ = self.make(child=child)
            self.start(controller)
            self.settle(controller)
            transfer, clean = owner_cleanup(controller)
            self.assertIs(transfer.process, child)
            self.assertFalse(clean)
            self.assertEqual(controller.state()["state"], "closed")
            self.assertEqual(controller.state()["launchCount"], 1)
            self.assertEqual(child.calls.count("terminate"), 1)
            self.assertEqual(child.calls.count("kill"), 1)
            self.assertEqual(child.calls[-1], ("wait", 10))

    def test_join_exception_returns_child_and_safe_failure_instead_of_losing_owner(self):
        controller, child = self.make()
        self.start(controller)
        self.settle(controller)
        with patch.object(controller._startup, "join", side_effect=RuntimeError("private")):
            transfer = controller.seal_and_take()
        self.assertIs(transfer.process, child)
        self.assertEqual(transfer.failures, ("readiness_join_failed",))
        self.assertTrue(controller._taken)
        self.assertFalse(controller._taking)
        self.assertEqual(child.calls, [])
        child.terminate()
        code = child.wait(30)
        self.assertFalse(controller.finish_after_wait(reaped=True, returncode=code))
        self.assertEqual(controller.state()["state"], "closed")
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_not_reaped_cannot_be_closed(self):
        controller, child = self.make()
        self.start(controller)
        self.settle(controller)
        transfer = controller.seal_and_take()
        self.assertIs(transfer.process, child)
        self.assertFalse(controller.finish_after_wait(reaped=False, returncode=None))
        self.assertEqual(controller.state()["state"], "closing")
        self.assertEqual(child.calls, [])
        child.terminate()
        code = child.wait(30)
        self.assertTrue(controller.finish_after_wait(reaped=True, returncode=code))
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_join_failure_still_transfers_child_and_cannot_claim_clean(self):
        controller, child = self.make()
        self.start(controller)
        self.settle(controller)
        with patch.object(controller._startup, "is_alive", return_value=True):
            transfer = controller.seal_and_take()
            self.assertIs(transfer.process, child)
            self.assertIn("readiness_join_failed", transfer.failures)
            child.terminate()
            code = child.wait(30)
            self.assertFalse(controller.finish_after_wait(reaped=True, returncode=code))
            self.assertEqual(controller.state()["state"], "closing")
        self.assertFalse(controller.finish_after_wait(reaped=True, returncode=code))
        self.assertEqual(controller.state()["state"], "closed")
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_controller_has_no_process_wait_terminate_or_kill_ownership(self):
        controller, child = self.make()
        self.start(controller)
        self.settle(controller)
        transfer = controller.seal_and_take()
        self.assertIs(transfer.process, child)
        self.assertEqual(child.calls, [])
        child.terminate()
        code = child.wait(30)
        self.assertTrue(controller.finish_after_wait(reaped=True, returncode=code))
        self.assertEqual(child.calls, ["terminate", ("wait", 30)])

    def test_browser_environment_strips_fixture_and_controller_secrets(self):
        source = {control.URL_ENV: "private-url", control.TOKEN_ENV: "private-token",
                  "ADTR_BOOTSTRAP_PASSWORD": "proof", "ADTR_E2E_PASSWORD": "proof",
                  "ADTR_DOMAIN_KEY": "key", "POSTGRES_PASSWORD": "db",
                  "PGPASSWORD": "db", "PATH": "/bin", "DISPLAY": ":99",
                  "PLAYWRIGHT_NO_COPY_PROMPT": "1"}
        self.assertEqual(control.browser_environment(source),
                         {"PATH": "/bin", "DISPLAY": ":99", "PLAYWRIGHT_NO_COPY_PROMPT": "1"})
        self.assertIn(control.TOKEN_ENV, source)

    def test_safe_diagnostics_and_failures_do_not_emit_private_inputs(self):
        def fail(*args):
            raise ValueError("proof-and-controller-secret")
        controller, child = self.make(ready=fail)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            self.start(controller)
            self.settle(controller)
            status = json.dumps(controller.state()) + repr(controller)
            self.assert_owned_once(controller, child)
        self.assertEqual(output.getvalue(), "")
        self.assertFalse(controller._token in status)
        self.assertNotIn("proof-and-controller-secret", status)
        self.assertNotIn("127.0.0.1", status)

    def wire(self, private, request):
        parsed = urlsplit(private[control.URL_ENV])
        with socket.create_connection((parsed.hostname, parsed.port), timeout=2) as connection:
            connection.sendall(request)
            chunks = []
            while True:
                try:
                    chunk = connection.recv(4096)
                except ConnectionResetError:
                    if not chunks:
                        raise
                    break
                if not chunk:
                    break
                chunks.append(chunk)
        return b"".join(chunks)

    def test_loopback_protocol_bounded_framing_and_no_listener_leak(self):
        controller, child = self.make()
        private = controller.open()
        self.assertEqual(urlsplit(private[control.URL_ENV]).hostname, "127.0.0.1")
        auth = ("Authorization: Bearer " + private[control.TOKEN_ENV] + "\r\n").encode()
        bad = [b"POST /start HTTP/1.1\r\nHost: local\r\n" + auth + b"Content-Length: 1\r\n\r\n",
               b"POST /start HTTP/1.1\r\nHost: local\r\n" + auth + b"\r\nx",
               b"POST /start HTTP/1.0\r\nHost: local\r\n" + auth + b"\r\n",
               b"POST /start HTTP/1.1\r\n" + auth + b"\r\n",
               b"POST /start HTTP/1.1\r\nHost: local\r\n" + auth + b"X: " + b"x" * 8200 + b"\r\n\r\n"]
        for request in bad:
            response = self.wire(private, request)
            self.assertTrue(response.startswith(b"HTTP/1.1 400"))
            self.assertEqual(controller.state()["launchCount"], 0)
        request = b"POST /start HTTP/1.1\r\nHost: local\r\n" + auth + b"Content-Length: 0\r\n\r\n"
        self.assertTrue(self.wire(private, request).startswith(b"HTTP/1.1 200"))
        self.settle(controller)
        self.assert_owned_once(controller, child)
        self.assertFalse(controller._serving.is_alive())
        self.assertEqual(controller._listener.fileno(), -1)

    def test_incomplete_headers_timeout_without_spawn_or_listener_leak(self):
        controller, child = self.make()
        private = controller.open()
        parsed = urlsplit(private[control.URL_ENV])
        with patch.object(control, "REQUEST_SECONDS", 0.05):
            with socket.create_connection((parsed.hostname, parsed.port), timeout=2) as connection:
                # Never complete framing. Socket timeout in the test also bounds
                # a broken implementation, without accepting timeout as success.
                connection.sendall(b"POST /start HTTP/1.1\r\nHost: local\r\n")
                self.assertEqual(connection.recv(4096), b"")
        self.assertEqual(controller.state()["launchCount"], 0)
        self.assertTrue(owner_cleanup(controller)[1])
        self.assertEqual(child.calls, [])
        self.assertFalse(controller._serving.is_alive())
        self.assertEqual(controller._listener.fileno(), -1)

    def test_close_during_state_response_joins_listener(self):
        controller, child = self.make()
        private = controller.open()
        entered, release = threading.Event(), threading.Event()
        original = controller._receive
        def delayed(connection):
            result = original(connection)
            entered.set()
            release.wait(2)
            return result
        controller._receive = delayed
        auth = ("Authorization: Bearer " + private[control.TOKEN_ENV] + "\r\n").encode()
        request = b"GET /state HTTP/1.1\r\nHost: local\r\n" + auth + b"\r\n"
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            response = pool.submit(self.wire, private, request)
            self.assertTrue(entered.wait(2))
            closing = pool.submit(owner_cleanup, controller)
            self.assertTrue(controller._cancelled.wait(2))
            release.set()
            self.assertTrue(response.result(timeout=4).startswith(b"HTTP/1.1 200"))
            self.assertTrue(closing.result(timeout=4)[1])
        self.assertEqual(controller.state()["launchCount"], 0)
        self.assertEqual(child.calls, [])
        self.assertFalse(controller._serving.is_alive())


if __name__ == "__main__":
    unittest.main()
