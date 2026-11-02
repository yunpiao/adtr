"""Dedicated detection wiring cannot masquerade as runtime acceptance."""
import ast
import json
from pathlib import Path
import re
import socket
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch

from test_simulation_detection_integration import ROLE, run_go_integration, worker_environment, wait_for_worker

ROOT = Path(__file__).resolve().parents[1]


class SimulationDetectionWorkflowContract(unittest.TestCase):
    def text(self, path):
        return (ROOT / path).read_text()

    def test_role_entitlement_and_tcp_prerequisites(self):
        self.assertEqual(len(ROLE), 24)
        self.assertRegex(ROLE, r'^[A-Za-z0-9_-]{24}$')
        source = self.text('scripts/test_simulation_detection_integration.py')
        for expected in ('INSERT INTO adtr.resource_tenant_config', "interval '30 days'",
                         '/api/access/menu', '/api/domain-selection?pageIdx=1&pageSize=20',
                         '/api/domain-selection/resolve?', '"pg_isready", "-h", "127.0.0.1"'):
            self.assertIn(expected, source)
        self.assertLess(source.index('INSERT INTO adtr.resource_tenant_config'), source.index('        start()'))

    def test_exact_head_minimal_permissions_pinned_actions(self):
        workflow = self.text('.github/workflows/simulation-detection.yml')
        self.assertIn('permissions: {}', workflow)
        self.assertEqual(re.findall(r'^      (\w+): (read|write)$', workflow, re.M), [('contents', 'read')])
        self.assertIn('ref: ${{ github.event.pull_request.head.sha || github.sha }}', workflow)
        self.assertIn('persist-credentials: false', workflow)
        self.assertIn('  pull_request:', workflow)
        self.assertIn('  workflow_dispatch:', workflow)
        self.assertIn('  push:\n    branches: [main]', workflow)
        for forbidden in ('secrets.', 'paths-ignore:', 'continue-on-error:'):
            self.assertNotIn(forbidden, workflow)
        actions = re.findall(r'uses: (\S+)', workflow)
        self.assertEqual(len(actions), 4)
        for action in actions:
            self.assertRegex(action, r'^actions/[\w-]+@[a-f0-9]{40}$')

    def test_workflow_runs_exact_dedicated_contract_build_and_runtime(self):
        workflow = self.text('.github/workflows/simulation-detection.yml')
        for expected in ('python3 -m unittest discover -s scripts -p test_simulation_detection_workflow_contract.py',
                         'go build -o bin/adtr ./cmd/adtr', 'npm ci --prefix web', 'npm run build --prefix web',
                         'simulation-detection.playwright.config.ts simulation-detection-e2e/detection.spec.ts',
                         'run: python3 scripts/test_simulation_detection_integration.py',
                         'path: artifacts/simulation-detection/'):
            self.assertIn(expected, workflow)

    def test_separate_inventory_and_no_sensitive_artifacts(self):
        self.assertFalse((ROOT / 'web/e2e/detection.spec.ts').exists())
        self.assertTrue((ROOT / 'web/simulation-detection-e2e/detection.spec.ts').is_file())
        config = self.text('web/simulation-detection.playwright.config.ts')
        self.assertIn('testDir: "./simulation-detection-e2e"', config)
        self.assertIn('testMatch: "detection.spec.ts"', config)
        for setting in ('trace: "off"', 'screenshot: "off"', 'video: "off"'):
            self.assertIn(setting, config)
        for path in ('scripts/ci_inventory.json', '.github/workflows/ci.yml', 'web/playwright.config.ts'):
            self.assertNotIn('detection.spec.ts', self.text(path))
        spec = self.text('web/simulation-detection-e2e/detection.spec.ts')
        self.assertNotIn('storageState', spec)
        self.assertNotIn('page.screenshot', spec)
        self.assertIn('workspace.screenshot', spec)
        self.assertIn('getByLabel("检测合成数据集", { exact: true })', spec)
        self.assertLess(spec.index('SIMULATED-detection-preselection.png'), spec.index('.selectOption('))

    def test_real_worker_is_required_and_not_run_is_not_pass(self):
        source = self.text('scripts/test_simulation_detection_integration.py')
        for expected in ('["./bin/adtr", "-mode", "worker"]', 'ADTR_SIMULATION_DETECTION_ENABLED="true"',
                         'completed = wait_for_worker(worker, worker_base, report, lambda: client.call(active_path))', 'completed["task"]["state"] == "succeeded"',
                         'result["templateVersion"] == 2', 'latest["version"] == 3',
                         'stop_worker()', 'ADTR_DIRECTORY_READ_ENABLED="false"',
                         'ADTR_DOMAIN_PROBE_ENABLED="false"', 'report["status"] = "not-run"'):
            self.assertIn(expected, source)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")', source)
        self.assertNotIn('UPDATE adtr.tasks', source)
        self.assertLess(source.index('"simulation-detection-e2e/detection.spec.ts"'), source.index('report["status"] = "passed"'))
        strings = [n.value for n in ast.walk(ast.parse(source)) if isinstance(n, ast.Constant) and isinstance(n.value, str)]
        for expected in ('-json', '-tags=integration', '^TestPostgresSimulationDetection$', '^TestSimulationDetectionHTTPAuthorizationRecoveryAndAuditAtomicity$'):
            self.assertIn(expected, strings)

    def test_revoked_epoch_task_stays_hidden_after_regrant(self):
        source = self.text('scripts/test_simulation_detection_integration.py')
        revoked = source.index('SET writeable=false')
        restored = source.index('SET writeable=true', revoked)
        fresh = source.index('operationId="http-active"', restored)
        before = source[revoked:restored]
        after = source[restored:fresh]
        for phase in (before, after):
            self.assertIn('client.call(task_path, want=404)', phase)
            self.assertIn('client.call(listing)["templates"][0]["version"] == 2', phase)
            self.assertNotIn('client.call(task_path)["task"]', phase)
        # Bind the expectation to the production kind's owner-scoped contract.
        self.assertIn('OwnerScoped: true', self.text('internal/simulationdetection/executor.go'))
        self.assertIn('k.OwnerScoped && (t.ActorID != p.ActorID || t.AuthorizationVersion != version)',
                      self.text('internal/tasks/engine.go'))

    def test_worker_listener_is_distinct_on_each_start_and_secret_inputs_filtered(self):
        with socket.socket() as api:
            api.bind(("127.0.0.1", 0))
            api.listen()
            address = "127.0.0.1:" + str(api.getsockname()[1])
            env = {"ADTR_LISTEN_ADDR": address, "ADTR_DATABASE_URL": "synthetic-private",
                   "ADTR_BOOTSTRAP_PASSWORD": "never-forward", "ADTR_E2E_PASSWORD": "never-forward",
                   "POSTGRES_PASSWORD": "never-forward", "PATH": "/synthetic"}
            for _ in range(2):  # Initial launch and restart both allocate independently.
                runtime, base = worker_environment(env)
                self.assertNotEqual(runtime["ADTR_LISTEN_ADDR"], address)
                self.assertRegex(runtime["ADTR_LISTEN_ADDR"], r"^127\.0\.0\.1:[1-9][0-9]*$")
                self.assertEqual(base, "http://" + runtime["ADTR_LISTEN_ADDR"])
                self.assertEqual(runtime["ADTR_DATABASE_URL"], "synthetic-private")
                self.assertFalse(any(k.startswith(("ADTR_BOOTSTRAP_", "ADTR_E2E_")) for k in runtime))
                self.assertNotIn("POSTGRES_PASSWORD", runtime)
                self.assertEqual(env["ADTR_LISTEN_ADDR"], address)
        source = self.text('scripts/test_simulation_detection_integration.py')
        self.assertIn('runtime, worker_base = worker_environment(env)', source)
        self.assertIn('start_worker()\n        wait_for_worker(worker, worker_base, report)', source)

    @staticmethod
    def response(status):
        response = MagicMock()
        response.__enter__.return_value.status = status
        return response

    def test_worker_readiness_gates_result_and_restart(self):
        process = SimpleNamespace(poll=lambda: None)
        report = {}
        reader = MagicMock(return_value={"task": {"state": "succeeded"}})
        with patch('test_simulation_detection_integration.urllib.request.urlopen',
                   side_effect=[OSError("private-network-detail"), self.response(200)]) as probe, \
             patch('test_simulation_detection_integration.time.sleep') as sleep:
            self.assertEqual(wait_for_worker(process, "http://127.0.0.1:1234", report, reader), reader.return_value)
            self.assertEqual(probe.call_count, 2)
            reader.assert_called_once_with()
            sleep.assert_called_once_with(.1)
            probe.assert_called_with("http://127.0.0.1:1234/readyz", timeout=1)
        with patch('test_simulation_detection_integration.urllib.request.urlopen', return_value=self.response(200)):
            self.assertIsNone(wait_for_worker(process, "http://127.0.0.1:1235", report))
        self.assertEqual(report, {})

    def test_worker_exit_retains_only_closed_diagnostic(self):
        report = {}
        with patch('test_simulation_detection_integration.urllib.request.urlopen') as probe:
            with self.assertRaisesRegex(RuntimeError, '^isolated worker exited$'):
                wait_for_worker(SimpleNamespace(poll=lambda: 1), "http://127.0.0.1:1234", report)
            probe.assert_not_called()
        self.assertEqual(report, {"workerDiagnostic": {"phase": "worker", "reason": "process-exited", "exitCode": 1}})

    def test_readiness_uses_existing_result_poll_budget(self):
        process = SimpleNamespace(poll=lambda: None)
        report = {}
        reader = MagicMock()
        with patch('test_simulation_detection_integration.urllib.request.urlopen', return_value=self.response(503)) as probe, \
             patch('test_simulation_detection_integration.time.sleep') as sleep:
            with self.assertRaisesRegex(RuntimeError, '^isolated worker readiness timed out$'):
                wait_for_worker(process, "http://127.0.0.1:1234", report, reader)
            self.assertEqual(probe.call_count, 300)
            self.assertEqual(sleep.call_count, 300)
            reader.assert_not_called()
        self.assertEqual(report, {"workerDiagnostic": {"phase": "worker", "reason": "readiness-timeout"}})

    def test_go_guard_rejects_skips_missing_or_unrelated_pass(self):
        cases = [([], False), ([{"Action": "skip", "Test": "Required"}], False),
                 ([{"Action": "pass", "Test": "Other"}], False),
                 ([{"Action": "pass", "Test": "Required"}], True),
                 ([{"Action": "pass", "Test": "Required"}, {"Action": "skip", "Test": "Required/sub"}], False)]
        for events, succeeds in cases:
            with self.subTest(events=events), patch('test_simulation_detection_integration.run', return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps, events)))):
                if succeeds:
                    run_go_integration('./synthetic', '^Required$', 'Required', {})
                else:
                    with self.assertRaises(AssertionError):
                        run_go_integration('./synthetic', '^Required$', 'Required', {})


if __name__ == '__main__':
    unittest.main()
