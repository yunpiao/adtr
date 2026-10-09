"""Docker creation diagnostics/security contracts; no Docker calls are made."""
import contextlib
import io
import subprocess
import unittest
from unittest.mock import Mock, patch

from e2e_container_startup_diagnostics import (
    MAX_STDERR_INPUT, MAX_STDERR_OUTPUT, TRUNCATED,
    report_container_startup_failure, sanitized_stderr,
)
from e2e_owned_container import OWNER_LABEL
import test_auth_e2e


PASSWORD = "123456789abcdef0" * 3
PINNED_IMAGE = "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"


class ContainerStartupDiagnosticsContract(unittest.TestCase):
    def run_harness(self, failure=None):
        output = io.StringIO()
        errors = io.StringIO()
        logs = io.StringIO("synthetic-application-log-secret")
        process = Mock()
        process.poll.return_value = None
        process.wait.return_value = 0
        response = Mock()
        response.__enter__ = Mock(return_value=Mock(status=200))
        response.__exit__ = Mock(return_value=False)

        def run(command, **_options):
            if command[:2] == ["docker", "run"] and failure is not None:
                raise failure
            return subprocess.CompletedProcess(command, 0, stdout=b"synthetic-success-stdout", stderr=b"synthetic-success-stderr")

        with patch("sys.argv", ["test_auth_e2e.py", "--suite", "auth"]), \
             patch.object(test_auth_e2e.secrets, "token_hex", return_value=PASSWORD), \
             patch.object(test_auth_e2e.tempfile, "TemporaryFile", return_value=logs), \
             patch.object(test_auth_e2e.subprocess, "run", side_effect=run) as calls, \
             patch.object(test_auth_e2e.subprocess, "check_output", return_value="127.0.0.1:5432") as port, \
             patch.object(test_auth_e2e.subprocess, "Popen", return_value=process) as start, \
             patch.object(test_auth_e2e.Path, "is_file", return_value=True), \
             patch.object(test_auth_e2e.urllib.request, "urlopen", return_value=response), \
             patch.object(test_auth_e2e, "remove_owned_container") as cleanup, \
             contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            caught = None
            try:
                test_auth_e2e.main()
            except Exception as error:
                caught = error
        cleanup.assert_called_once()
        self.assertTrue(logs.closed)
        name, owner = cleanup.call_args.args
        first = calls.call_args_list[0]
        self.assertEqual(first.args[0], [
            "docker", "run", "--detach", "--rm", "--name", name,
            "--label", f"{OWNER_LABEL}={owner}",
            "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e",
            "-p", "127.0.0.1::5432", PINNED_IMAGE,
        ])
        self.assertEqual(set(first.kwargs), {"env", "check", "capture_output", "timeout"})
        self.assertEqual(first.kwargs["env"]["POSTGRES_PASSWORD"], PASSWORD)
        self.assertEqual(first.kwargs["timeout"], 60)
        self.assertIs(first.kwargs["capture_output"], True)
        self.assertIs(first.kwargs["check"], True)
        self.assertNotIn(PASSWORD, repr(first.args))
        if failure is not None:
            calls.assert_called_once()
            port.assert_not_called()
            start.assert_not_called()
        return caught, output.getvalue(), errors.getvalue()

    def test_success_preserves_command_cleanup_and_emits_no_diagnostics(self):
        error, output, errors = self.run_harness()
        self.assertIsNone(error)
        self.assertIn("acceptance PASS", output)
        self.assertEqual(errors, "")

    def test_exit_125_reports_stderr_and_reraises_identical_failure_after_cleanup(self):
        failure = subprocess.CalledProcessError(
            125, ["docker", "run", "synthetic-command-secret"],
            output=b"synthetic-stdout-secret", stderr=b"docker: toomanyrequests: pull rate limit\n",
        )
        error, output, errors = self.run_harness(failure)
        self.assertIs(error, failure)
        self.assertEqual(output, "")
        self.assertIn("exit code 125", errors)
        self.assertIn("toomanyrequests: pull rate limit", errors)
        self.assertEqual(len(errors.splitlines()), 1)
        self.assertNotIn("synthetic-command-secret", errors)
        self.assertNotIn("synthetic-stdout-secret", errors)
        self.assertNotIn("synthetic-application-log-secret", errors)
        self.assertEqual(failure.stderr, b"docker: toomanyrequests: pull rate limit\n")

    def test_timeout_and_missing_docker_keep_original_failures_and_owned_cleanup(self):
        for failure in [subprocess.TimeoutExpired(["docker", "run"], 60, stderr=b"private"),
                        FileNotFoundError("synthetic-private-path")]:
            with self.subTest(failure=type(failure)):
                error, output, errors = self.run_harness(failure)
                self.assertIs(error, failure)
                self.assertEqual(output + errors, "")

    def test_password_and_urls_are_redacted(self):
        raw = (f"docker: failed with {PASSWORD}\n"
               'Get "https://user:synthetic-url-secret@registry.example/v2?token=synthetic-query-secret": denied\n')
        for stderr in [raw, raw.encode()]:
            with self.subTest(kind=type(stderr)):
                result = sanitized_stderr(stderr, PASSWORD)
                self.assertNotIn(PASSWORD, result)
                self.assertNotIn("synthetic-", result)
                self.assertNotIn("registry.example", result)
                self.assertIn("[redacted URL]", result)

    def test_credential_and_environment_fields_withhold_all_multiline_values(self):
        for field in ["Authorization: Bearer synthetic-header-secret",
                      "Authorization: Basic synthetic-basic-secret",
                      '"password": "synthetic-json-secret with spaces"',
                      '"api_key": "synthetic-api-secret"',
                      "POSTGRES_PASSWORD=synthetic-env-secret",
                      "OTHER_ENV=synthetic-unrelated-env-secret",
                      '"auth": "synthetic-auth-secret"',
                      'Config.Env: ["synthetic-config-secret"]',
                      "password:", '"Env": [', '"auths": {']:
            raw = "synthetic-preceding-secret\n" + field + "\n synthetic-continuation-secret\n"
            for stderr in [raw, raw.encode()]:
                with self.subTest(field=field, kind=type(stderr)):
                    self.assertEqual(sanitized_stderr(stderr, PASSWORD),
                                     "[stderr withheld: sensitive diagnostic field]")

    def test_regular_docker_failure_reasons_remain_visible(self):
        for reason, expected in [
                ("docker: Error response from daemon: manifest unknown", "manifest unknown"),
                ("docker: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", "Cannot connect to the Docker daemon"),
                ("docker: Error response from daemon: unauthorized: authentication required", "unauthorized: authentication required"),
                ("docker: Error response from daemon: no space left on device", "no space left on device"),
                ("docker: Error response from daemon: failed to create task: OCI runtime create failed", "OCI runtime create failed")]:
            with self.subTest(reason=reason):
                result = sanitized_stderr(reason, PASSWORD)
                self.assertIn(expected, result)

    def test_output_is_bounded_after_escaping_and_controls_cannot_create_log_lines(self):
        result = sanitized_stderr("\x1b[31m\r\n::error::synthetic\u202e\x00\xff", PASSWORD)
        self.assertNotIn("\n", result)
        self.assertNotIn("\r", result)
        self.assertNotIn("\x1b", result)
        self.assertTrue(result.isascii())
        for stderr in ["x" * (MAX_STDERR_OUTPUT * 2),
                       "x-" * (MAX_STDERR_INPUT // 2),
                       "::" * (MAX_STDERR_OUTPUT // 2),
                       "\x00" * (MAX_STDERR_OUTPUT // 2),
                       b"\xff" * (MAX_STDERR_OUTPUT // 2)]:
            result = sanitized_stderr(stderr, PASSWORD)
            self.assertLessEqual(len(result), MAX_STDERR_OUTPUT)
            self.assertTrue(result.endswith(TRUNCATED))

    def test_both_actions_command_syntaxes_are_neutralized_even_after_a_prefix(self):
        for stderr in ["##[warning]synthetic", "docker: ##[stop-commands]synthetic-token",
                       "::warning::synthetic", "docker: ::stop-commands::synthetic-token",
                       "### [benign] ##[error]synthetic\n::error::synthetic"]:
            failure = subprocess.CalledProcessError(125, ["docker", "run"], stderr=stderr.encode())
            output = io.StringIO()
            with contextlib.redirect_stderr(output):
                report_container_startup_failure(failure, PASSWORD)
            rendered = output.getvalue()
            self.assertNotIn("##[", rendered)
            self.assertNotIn("::", rendered)
            self.assertEqual(len(rendered.splitlines()), 1)

    def test_input_cutoff_never_publishes_partial_secret_or_url(self):
        for tail in [PASSWORD, "https://user:synthetic-secret@registry.example"]:
            raw = "docker: manifest unknown\n" + "x" * (MAX_STDERR_INPUT - 30) + tail
            for stderr in [raw, raw.encode()]:
                result = sanitized_stderr(stderr, PASSWORD)
                self.assertIn("manifest unknown", result)
                self.assertNotIn("x", result.removesuffix(TRUNCATED))
                self.assertNotIn(tail[:6], result)
                self.assertTrue(result.endswith(TRUNCATED))
        self.assertEqual(sanitized_stderr("x" * (MAX_STDERR_INPUT + 1), PASSWORD), '""' + TRUNCATED)

    def test_missing_stderr_is_explicit_and_diagnostic_write_cannot_mask_failure(self):
        for stderr in [None, b"", ""]:
            self.assertEqual(sanitized_stderr(stderr, PASSWORD), "[no stderr captured]")
        failure = subprocess.CalledProcessError(125, ["docker", "run"], stderr=b"manifest unknown")
        for write_error in [BrokenPipeError("closed"), ValueError("closed")]:
            with patch("builtins.print", side_effect=write_error):
                report_container_startup_failure(failure, PASSWORD)


if __name__ == "__main__":
    unittest.main()
