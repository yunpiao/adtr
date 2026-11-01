"""The standalone simulation suite is explicit coverage, not a matrix exemption."""
import ast
from pathlib import Path
import re
import subprocess
import unittest
import io
from email.message import Message
import json
from unittest.mock import patch

from test_simulation_events_integration import Client, safe_response_code, safe_preparation_code, prepare_database

ROOT = Path(__file__).resolve().parents[1]


class SimulationEventsWorkflowContract(unittest.TestCase):
    def text(self, path):
        return (ROOT / path).read_text()

    def test_exact_head_and_minimal_permissions(self):
        workflow = self.text('.github/workflows/simulation-events.yml')
        self.assertIn('permissions: {}', workflow)
        self.assertEqual(re.findall(r'^      (\w+): (read|write)$', workflow, re.M), [('contents', 'read')])
        self.assertIn('ref: ${{ github.event.pull_request.head.sha || github.sha }}', workflow)
        self.assertIn('persist-credentials: false', workflow)
        self.assertIn('  pull_request:', workflow)
        self.assertIn('  workflow_dispatch:', workflow)
        self.assertIn('  push:\n    branches: [main]', workflow)
        self.assertNotIn('secrets.', workflow)
        self.assertNotIn('paths-ignore:', workflow)
        actions = re.findall(r'uses: (\S+)', workflow)
        self.assertEqual(len(actions), 4)
        for action in actions:
            self.assertRegex(action, r'^actions/[\w-]+@[a-f0-9]{40}$')

    def test_workflow_runs_contract_and_real_harness(self):
        workflow = self.text('.github/workflows/simulation-events.yml')
        self.assertIn('python3 -m unittest discover -s scripts -p test_simulation_events_workflow_contract.py', workflow)
        self.assertIn('run: python3 scripts/test_simulation_events_integration.py', workflow)
        self.assertIn('simulation.playwright.config.ts simulation-e2e/simulation-events.spec.ts', workflow)
        self.assertIn('path: artifacts/simulation-events/', workflow)
        self.assertNotIn('continue-on-error:', workflow)

    def test_harness_selects_exact_standalone_spec(self):
        tree = ast.parse(self.text('scripts/test_simulation_events_integration.py'))
        commands = [node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str)]
        for expected in ('--config', 'simulation.playwright.config.ts', 'simulation-e2e/simulation-events.spec.ts',
                         '^TestPostgresAtomicSyntheticState$', '^TestSimulationEventHTTP'):
            self.assertIn(expected, commands)
        source = self.text('scripts/test_simulation_events_integration.py')
        self.assertIn('for phase in ("migrate-first", "migrate-repeat", "bootstrap"):', source)
        self.assertIn('report["status"] = "not-run"', source)
        self.assertIn('report["status"] = "passed"', source)
        self.assertLess(source.index('"simulation-e2e/simulation-events.spec.ts"'), source.index('report["status"] = "passed"'))

    def test_separate_directory_preserves_legacy_inventory(self):
        self.assertFalse((ROOT / 'web/e2e/simulation-events.spec.ts').exists())
        self.assertTrue((ROOT / 'web/simulation-e2e/simulation-events.spec.ts').is_file())
        config = self.text('web/simulation.playwright.config.ts')
        self.assertIn('import base from "./playwright.config"', config)
        self.assertIn('testDir: "./simulation-e2e"', config)
        self.assertIn('testMatch: "simulation-events.spec.ts"', config)
        for setting in ('trace: "off"', 'screenshot: "off"', 'video: "off"'):
            self.assertIn(setting, config)
        for path in ('scripts/ci_inventory.json', '.github/workflows/ci.yml', 'web/playwright.config.ts'):
            self.assertNotIn('simulation-events.spec.ts', self.text(path))


class SimulationClientDiagnosticsContract(unittest.TestCase):
    def response(self, status, payload):
        result = io.BytesIO(payload)
        result.status = status
        result.headers = Message()
        result.headers["Content-Type"] = "application/json"
        return result

    def test_login_wire_shape_and_closed_failure_code(self):
        client = Client("http://127.0.0.1:18080", "admin")
        password = "Synthetic secret must never be logged"
        with patch.object(client.opener, "open", return_value=self.response(400, b'{"error":"invalid_input"}')) as opened:
            with self.assertRaises(AssertionError) as caught:
                client.call("/api/auth/login", {"username": "simulation-admin", "password": password})
        request = opened.call_args.args[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(request.get_header("Content-type"), "application/json")
        self.assertEqual(request.get_header("Origin"), "http://127.0.0.1:18080")
        self.assertEqual(json.loads(request.data), {"username": "simulation-admin", "password": password})
        self.assertIn("stage=admin; code=invalid_input", str(caught.exception))
        self.assertIn("responseType=json; method=POST; requestType=json; proxyConfigured=", str(caught.exception))
        self.assertNotIn(password, str(caught.exception))
        self.assertNotIn("simulation-admin", str(caught.exception))

    def test_untrusted_diagnostics_are_never_echoed(self):
        for raw, expected in ((b'<html>secret</html>', "non-json-response"),
                              (b'{"error":"secret"}', "unrecognized-response"),
                              (b'{"error": ["secret"]}', "unrecognized-response"),
                              (b'{"error":"invalid_input","password":"secret"}', "invalid_input"),
                              (b'x' * 65537, "oversized-response")):
            self.assertEqual(safe_response_code(raw), expected)
        with self.assertRaises(ValueError):
            Client("http://127.0.0.1:18080", "secret")


class SimulationDatabasePreparationContract(unittest.TestCase):
    def test_readiness_requires_final_tcp_listener(self):
        source = (ROOT / "scripts/test_simulation_events_integration.py").read_text()
        tree = ast.parse(source)
        commands = [node for node in ast.walk(tree) if isinstance(node, ast.List)
                    and any(isinstance(x, ast.Constant) and x.value == "pg_isready" for x in node.elts)]
        self.assertEqual(len(commands), 1)
        literals = [x.value for x in commands[0].elts if isinstance(x, ast.Constant)]
        self.assertEqual(literals, ["docker", "exec", "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-U", "postgres", "-d", "adtr_e2e"])
        self.assertIn("for _ in range(100):", source)
        self.assertIn("time.sleep(.2)", source)

    def test_preparation_preserves_commands_and_never_retries(self):
        with patch("test_simulation_events_integration.run") as command:
            for phase in ("migrate-first", "migrate-repeat", "bootstrap"):
                prepare_database(phase, {})
        self.assertEqual([call.args[0] for call in command.call_args_list],
                         [["./bin/adtr", "-mode", mode] for mode in ("migrate", "migrate", "bootstrap")])
        error = subprocess.CalledProcessError(1, ["secret"], output=b"secret", stderr=b"migration connection failed\n")
        with patch("test_simulation_events_integration.run", side_effect=error) as command:
            with self.assertRaises(RuntimeError) as caught:
                prepare_database("migrate-first", {"SECRET": "secret"})
            command.assert_called_once()
        self.assertEqual(str(caught.exception), "database preparation failed: phase=migrate-first; exit=1; code=database-connect")
        self.assertTrue(caught.exception.__suppress_context__)

    def test_preparation_output_is_closed_and_redacted(self):
        self.assertEqual(safe_preparation_code(b"simulation events schema migration failed\n"), "simulation-schema")
        for value in (b"secret", b"migration connection failed\nsecret", b"secret" * 500,
                      "migration connection failed", None):
            self.assertEqual(safe_preparation_code(value), "unclassified")
        with patch("test_simulation_events_integration.run", side_effect=subprocess.TimeoutExpired("secret", 120, stderr=b"secret")):
            with self.assertRaisesRegex(RuntimeError, "phase=bootstrap; exit=none; code=timeout"):
                prepare_database("bootstrap", {})
        with self.assertRaises(ValueError):
            prepare_database("secret", {})


if __name__ == '__main__':
    unittest.main()
