"""Dedicated dashboard evidence wiring; source checks are not runtime acceptance."""
import ast
import json
from pathlib import Path
import re
import subprocess
import io
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from test_simulation_dashboard_integration import ROLE, SEED, run_go_integration

ROOT = Path(__file__).resolve().parents[1]


class SimulationDashboardWorkflowContract(unittest.TestCase):
    def text(self, path):
        return (ROOT / path).read_text()

    def test_failed_go_test_diagnostic_is_closed(self):
        output = 'null\n[]\n"private"\n' + json.dumps({"Output": "integration_test.go:77: secret DSN password SQLSTATE 42P01"})
        failure = subprocess.CalledProcessError(1, ["go"], output=output, stderr="private credential")
        with patch('test_simulation_dashboard_integration.run', side_effect=failure), \
             patch('sys.stdout', new_callable=io.StringIO) as printed:
            with self.assertRaisesRegex(RuntimeError, 'required PostgreSQL integration failed'):
                run_go_integration('./internal/simulationdashboard', '^TestPostgresDashboardSnapshot$', 'TestPostgresDashboardSnapshot', {})
        self.assertEqual(json.loads(printed.getvalue()), {"phase": "named-postgres-test", "exitCode": 1,
                         "locations": ["integration_test.go:77"], "sqlStates": ["42P01"]})

    def test_fixture_roles_live_grants_and_owned_resources(self):
        self.assertRegex(ROLE, r'^[A-Za-z0-9_-]{24}$')
        source = self.text('scripts/test_simulation_dashboard_integration.py')
        for expected in ('INSERT INTO adtr.resource_tenant_config', "interval '30 days'", '/api/domain-selection/resolve?',
                         "(VALUES('simulation_events'),('simulation_risk'),('simulation_detection'),('domains'))",
                         "adtr.simulation.owner", "actual != owner", 'POSTGRES_PASSWORD',
                         'runtime, worker_base = worker_environment(env)', 'wait_for_worker(worker, worker_base, report',
                         'ADTR_SIMULATION_DASHBOARD_ENABLED="true"'):
            self.assertIn(expected, source)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")', source)
        self.assertNotIn('UPDATE adtr.tasks', source)
        self.assertIn('SeedFixtureTx', SEED)
        self.assertIn('time.Now().UTC().Add(-2*time.Minute)', SEED)
        self.assertIn('"192.0.2.1"', SEED)

    def test_all_sources_are_real_and_expected_counts_are_independent(self):
        source = self.text('scripts/test_simulation_dashboard_integration.py')
        for expected in ('initial["cases"] == {"scope": "dataset_current", "total": 2, "open": 2, "closed": 0',
                         'initial["risk"]["score"] == 5', 'changed["risk"]["score"] == 0',
                         'changed["caseCloseThroughput"]["windowTotal"] == 1',
                         '"/api/simulation/events/transition"', '"/api/simulation/risk/reset"',
                         '"/api/simulation/detection/run"', 'completed["task"]["state"] == "succeeded"',
                         'latest["eventCount"] == 2', 'other.call(path)', 'client.call(path, want=403)',
                         'initial["productAccepted"] is False', 'initial["unavailable"]'):
            self.assertIn(expected, source)

    def test_by_kind_oracle_includes_explicit_zero_for_all_supported_kinds(self):
        tree = ast.parse(self.text('scripts/test_simulation_dashboard_integration.py'))
        assertions = [node.test for node in ast.walk(tree) if isinstance(node, ast.Assert)
                      and 'byKind' in ast.unparse(node.test)]
        self.assertEqual(len(assertions), 1)
        comparison = assertions[0]
        self.assertIsInstance(comparison, ast.Compare)
        self.assertEqual(len(comparison.ops), 1)
        self.assertIsInstance(comparison.ops[0], ast.Eq)
        self.assertIsInstance(comparison.left, ast.DictComp)
        self.assertEqual(ast.literal_eval(comparison.comparators[0]),
                         {"login_success": 0, "login_failure": 1, "service_access": 1})

    def test_worker_rule_fixture_uses_closed_http_dto(self):
        tree = ast.parse(self.text('scripts/test_simulation_dashboard_integration.py'))
        definitions = [node.value for node in ast.walk(tree) if isinstance(node, ast.Assign)
                       and any(isinstance(target, ast.Name) and target.id == 'rules'
                               for target in node.targets)]
        self.assertEqual(len(definitions), 1)
        self.assertEqual(ast.literal_eval(definitions[0]), [{
            "id": "dashboard-rule", "enabled": True, "combine": "all",
            "conditions": [{"field": "event.category", "operator": "exists", "values": []}],
        }])

    def test_named_postgres_tests_cannot_be_missing_or_skipped(self):
        source = self.text('scripts/test_simulation_dashboard_integration.py')
        constants = {n.value for n in ast.walk(ast.parse(source)) if isinstance(n, ast.Constant) and isinstance(n.value, str)}
        for name in ('TestPostgresDashboardSnapshot', 'TestSimulationDashboardHTTPGrantScopeSessionAndNoMutation',
                     'TestSimulationDashboardRechecksSessionAfterIdentityLock'):
            self.assertIn(name, constants)
            self.assertIn('^' + name + '$', constants)
        for events, succeeds in (([], False), ([{"Action": "skip", "Test": "Required"}], False),
                                 ([{"Action": "pass", "Test": "Other"}], False),
                                 ([{"Action": "pass", "Test": "Required"}], True),
                                 ([{"Action": "pass", "Test": "Required"}, {"Action": "skip", "Test": "Required/sub"}], False)):
            with patch('test_simulation_dashboard_integration.run', return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps, events)))):
                if succeeds:
                    run_go_integration('./synthetic', '^Required$', 'Required', {})
                else:
                    with self.assertRaises(AssertionError):
                        run_go_integration('./synthetic', '^Required$', 'Required', {})

    def test_exact_revision_minimal_permissions_and_pinned_actions(self):
        workflow = self.text('.github/workflows/simulation-dashboard.yml')
        self.assertIn('permissions: {}', workflow)
        self.assertEqual(re.findall(r'^      (\w+): (read|write)$', workflow, re.M), [('contents', 'read')])
        for expected in ('ref: ${{ github.event.pull_request.head.sha || github.sha }}', 'persist-credentials: false',
                         '  pull_request:', '  workflow_dispatch:', '  push:\n    branches: [main]',
                         'python3 -m unittest discover -s scripts -p test_simulation_dashboard_workflow_contract.py',
                         'run: python3 scripts/test_simulation_dashboard_integration.py',
                         'simulation-dashboard.playwright.config.ts simulation-dashboard-e2e/dashboard.spec.ts',
                         'path: artifacts/simulation-dashboard/', 'if-no-files-found: error'):
            self.assertIn(expected, workflow)
        self.assertNotIn('continue-on-error:', workflow)
        self.assertNotIn('secrets.', workflow)
        self.assertNotIn('paths-ignore:', workflow)
        actions = re.findall(r'uses: (\S+)', workflow)
        self.assertEqual(len(actions), 4)
        for action in actions:
            self.assertRegex(action, r'^actions/[\w-]+@[a-f0-9]{40}$')

    def test_separate_browser_inventory_and_safe_evidence(self):
        config = self.text('web/simulation-dashboard.playwright.config.ts')
        self.assertIn('testDir: "./simulation-dashboard-e2e"', config)
        self.assertIn('testMatch: "dashboard.spec.ts"', config)
        for setting in ('trace: "off"', 'screenshot: "off"', 'video: "off"'):
            self.assertIn(setting, config)
        spec = self.text('web/simulation-dashboard-e2e/dashboard.spec.ts')
        for expected in ('SIMULATED-dashboard-before.png', 'SIMULATED-dashboard-refreshed.png',
                         'SIMULATED-dashboard-reloaded.png', 'page.reload()', 'page.goBack()',
                         'status: 503', '"/api/simulation/events/transition"', '"/api/simulation/risk/reset"',
                         'snapshot.caseCloseThroughput.windowTotal', 'x-adtr-user-id', 'snapshot.unavailable.length'):
            self.assertIn(expected, spec)
        self.assertNotIn('storageState', spec)
        for path in ('scripts/ci_inventory.json', '.github/workflows/ci.yml', 'web/playwright.config.ts'):
            self.assertNotIn('dashboard.spec.ts', self.text(path))
        source = self.text('scripts/test_simulation_dashboard_integration.py')
        self.assertIn('report["status"] = "not-run"', source)
        self.assertLess(source.index('"simulation-dashboard-e2e/dashboard.spec.ts"'), source.index('report["status"] = "passed"'))


if __name__ == '__main__':
    unittest.main()
