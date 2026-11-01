"""The standalone simulation suite is explicit coverage, not a matrix exemption."""
import ast
from pathlib import Path
import re
import unittest
import io
from email.message import Message
import json
from unittest.mock import patch

from test_simulation_events_integration import Client, safe_response_code

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
        self.assertIn('for mode in ("migrate", "migrate", "bootstrap"):', source)
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


if __name__ == '__main__':
    unittest.main()
