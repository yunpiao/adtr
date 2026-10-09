"""Runtime panic/redaction contracts; database and browser calls are mocked."""
import contextlib
import io
import subprocess
import unittest
from unittest.mock import Mock, patch

from e2e_runtime_diagnostics import http_handler_panicked
import test_auth_e2e


PANIC = "2026/10/09 13:06:01 http: panic serving 127.0.0.1:45678: synthetic-sensitive-value\n"
DIAGNOSTIC = "browser acceptance runtime observed an HTTP handler panic; raw diagnostics withheld"


class RuntimeDiagnosticsContract(unittest.TestCase):
    def test_go_panic_header_is_found_after_the_current_write_position(self):
        for header in [PANIC, PANIC.replace("127.0.0.1", "[::1]")]:
            with self.subTest(header=header):
                logs = io.StringIO()
                logs.write("api service starting\n" + header + "goroutine 5 [running]:\n")
                self.assertTrue(http_handler_panicked(logs))

    def test_benign_messages_and_quoted_headers_do_not_match(self):
        for text in ["", "api service starting\n", "http: panic serving example\n",
                     "request message: " + PANIC, '"' + PANIC + '"',
                     PANIC.replace(" http: panic", " request message: http: panic"),
                     PANIC.replace("127.0.0.1:45678", "not-a-remote-address")]:
            with self.subTest(text=text):
                self.assertFalse(http_handler_panicked(io.StringIO(text)))

    def run_harness(self, content, browser_failed=False):
        process = Mock()
        process.poll.return_value = None
        process.wait.return_value = 0
        response = Mock()
        response.__enter__ = Mock(return_value=Mock(status=200))
        response.__exit__ = Mock(return_value=False)
        logs = io.StringIO(content)
        logs.seek(0, io.SEEK_END)
        output = io.StringIO()

        def run(command, **_options):
            if command[0] == "npm" and browser_failed:
                raise subprocess.CalledProcessError(1, command)
            return subprocess.CompletedProcess(command, 0)

        with patch("sys.argv", ["test_auth_e2e.py", "--suite", "auth"]), \
             patch.object(test_auth_e2e.tempfile, "TemporaryFile", return_value=logs), \
             patch.object(test_auth_e2e.subprocess, "run", side_effect=run), \
             patch.object(test_auth_e2e.subprocess, "check_output", return_value="127.0.0.1:5432"), \
             patch.object(test_auth_e2e.subprocess, "Popen", return_value=process), \
             patch.object(test_auth_e2e.Path, "is_file", return_value=True), \
             patch.object(test_auth_e2e.urllib.request, "urlopen", return_value=response), \
             patch.object(test_auth_e2e, "remove_owned_container") as remove, \
             contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            error = None
            try:
                test_auth_e2e.main()
            except Exception as caught:
                error = caught
        process.terminate.assert_called_once()
        process.wait.assert_called_once_with(timeout=30)
        remove.assert_called_once()
        self.assertTrue(logs.closed)
        return error, output.getvalue()

    def test_panic_cannot_turn_green_after_a_successful_browser_retry(self):
        error, output = self.run_harness("api service starting\n" + PANIC)
        self.assertIsInstance(error, RuntimeError)
        self.assertEqual(str(error), DIAGNOSTIC)
        self.assertNotIn("synthetic-sensitive-value", str(error) + output)
        self.assertNotIn("acceptance PASS", output)

    def test_panic_after_a_browser_failure_has_only_fixed_diagnostics(self):
        error, output = self.run_harness(PANIC, browser_failed=True)
        self.assertIsInstance(error, RuntimeError)
        self.assertEqual(str(error), DIAGNOSTIC)
        self.assertNotIn("synthetic-sensitive-value", str(error) + output)

    def test_normal_success_and_original_browser_failure_keep_their_outcomes(self):
        error, output = self.run_harness("api service starting\n")
        self.assertIsNone(error)
        self.assertIn("acceptance PASS", output)
        error, output = self.run_harness("api service starting\n", browser_failed=True)
        self.assertIsInstance(error, subprocess.CalledProcessError)
        self.assertNotIn("acceptance PASS", output)

    def test_unreadable_diagnostics_fail_closed_after_owned_cleanup(self):
        for failure in [OSError("synthetic-sensitive-value"),
                        UnicodeError("synthetic-sensitive-value"),
                        ValueError("synthetic-sensitive-value")]:
            with self.subTest(failure=type(failure)), \
                 patch.object(test_auth_e2e, "http_handler_panicked", side_effect=failure):
                error, output = self.run_harness("api service starting\n")
            self.assertIsInstance(error, RuntimeError)
            self.assertEqual(str(error), "browser acceptance runtime diagnostics could not be checked")
            self.assertNotIn("synthetic-sensitive-value", str(error) + output)
            self.assertNotIn("acceptance PASS", output)


if __name__ == "__main__":
    unittest.main()
