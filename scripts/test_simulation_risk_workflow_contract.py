"""The standalone simulation suite is explicit coverage, not a matrix exemption."""
import ast
from pathlib import Path
import re
import unittest
import json
from types import SimpleNamespace
from unittest.mock import patch

from test_simulation_risk_integration import run_go_integration, ROLE


ROOT = Path(__file__).resolve().parents[1]


class SimulationRiskWorkflowContract(unittest.TestCase):
    def test_fixture_role_matches_production_contract(self):
        self.assertEqual(len(ROLE), 24)
        self.assertRegex(ROLE, r'^[A-Za-z0-9_-]{24}$')
        self.assertNotEqual(len("simulation_e2e_role_00001"), 24)
        source = (ROOT / "internal/auth/access_types.go").read_text()
        self.assertIn("if len(id) != 24", source)

    def text(self, path):
        return (ROOT / path).read_text()

    def test_live_tenant_and_source_prerequisites(self):
        source = self.text('scripts/test_simulation_risk_integration.py')
        for expected in ('INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)',
                         "interval '30 days'", "('default','synthetic-default','SIMULATED default')",
                         "('other','synthetic-other','SIMULATED other')", '/api/access/menu',
                         '/api/domain-selection?pageIdx=1&pageSize=20', '/api/domain-selection/resolve?',
                         '"expectedCredentialRevision": choice["credentialRevision"]'):
            self.assertIn(expected, source)
        self.assertLess(source.index('INSERT INTO adtr.resource_tenant_config'), source.index('        start()'))

    def test_exact_head_and_minimal_permissions(self):
        workflow = self.text('.github/workflows/simulation-risk.yml')
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
        workflow = self.text('.github/workflows/simulation-risk.yml')
        self.assertIn('python3 -m unittest discover -s scripts -p test_simulation_risk_workflow_contract.py', workflow)
        self.assertIn('run: python3 scripts/test_simulation_risk_integration.py', workflow)
        self.assertIn('simulation-risk.playwright.config.ts simulation-risk-e2e/risk.spec.ts', workflow)
        self.assertIn('path: artifacts/simulation-risk/', workflow)
        self.assertNotIn('continue-on-error:', workflow)

    def test_harness_selects_exact_standalone_spec(self):
        tree = ast.parse(self.text('scripts/test_simulation_risk_integration.py'))
        commands = [node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str)]
        for expected in ('--config', 'simulation-risk.playwright.config.ts', 'simulation-risk-e2e/risk.spec.ts',
                         '^TestPostgresRiskLedger$', '^TestSimulationRiskHTTP'):
            self.assertIn(expected, commands)
        source = self.text('scripts/test_simulation_risk_integration.py')
        self.assertIn('for mode in ("migrate", "migrate", "bootstrap"):', source)
        self.assertIn('report["status"] = "not-run"', source)
        self.assertIn('report["status"] = "passed"', source)
        self.assertLess(source.index('"simulation-risk-e2e/risk.spec.ts"'), source.index('report["status"] = "passed"'))

    def test_separate_directory_preserves_legacy_inventory(self):
        self.assertFalse((ROOT / 'web/e2e/risk.spec.ts').exists())
        self.assertTrue((ROOT / 'web/simulation-risk-e2e/risk.spec.ts').is_file())
        config = self.text('web/simulation-risk.playwright.config.ts')
        self.assertIn('import base from "./playwright.config"', config)
        self.assertIn('testDir: "./simulation-risk-e2e"', config)
        self.assertIn('testMatch: "risk.spec.ts"', config)
        for setting in ('trace: "off"', 'screenshot: "off"', 'video: "off"'):
            self.assertIn(setting, config)
        for path in ('scripts/ci_inventory.json', '.github/workflows/ci.yml', 'web/playwright.config.ts'):
            self.assertNotIn('risk.spec.ts', self.text(path))


    def test_risk_gate_mapping_and_no_auth_artifacts(self):
        source = self.text('scripts/test_simulation_risk_integration.py')
        for expected in ('ADTR_SIMULATION_RISK_ENABLED="true"', "mark='simulation_risk'", '"score"] == 5', '"score"] == 15', 'expectedRevision=2', 'ADTR_TEST_DATABASE_URL', 'test_simulation_events_integration import Client'):
            self.assertIn(expected, source)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")', source)
        spec = self.text('web/simulation-risk-e2e/risk.spec.ts')
        for expected in ('route.fetch()', 'route.abort("failed")', 'toEqual(receipt)', 'page.reload()', 'SIMULATED-risk-before.png'):
            self.assertIn(expected, spec)
        self.assertEqual(spec.count('button.click();'), 2)
        self.assertNotIn('storageState', spec)

    def test_go_integration_rejects_skips_and_empty_selection(self):
        for events, succeeds in (([], False), ([{"Action": "skip", "Test": "Required"}], False),
                                 ([{"Action": "pass", "Test": "Required"}], True),
                                 ([{"Action": "pass", "Test": "Required"}, {"Action": "skip", "Test": "Required/sub"}], False)):
            with patch('test_simulation_risk_integration.run', return_value=SimpleNamespace(stdout="\n".join(map(json.dumps, events)))):
                if succeeds:
                    run_go_integration("./synthetic", "^Required$", "Required", {})
                else:
                    with self.assertRaises(AssertionError):
                        run_go_integration("./synthetic", "^Required$", "Required", {})


if __name__ == '__main__':
    unittest.main()
