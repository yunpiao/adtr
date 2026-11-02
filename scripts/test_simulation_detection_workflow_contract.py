"""Dedicated detection wiring cannot masquerade as runtime acceptance."""
import ast
import json
from pathlib import Path
import re
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from test_simulation_detection_integration import ROLE, run_go_integration

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
                         'worker.poll() is None', 'completed["task"]["state"] == "succeeded"',
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
