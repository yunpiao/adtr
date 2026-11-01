"""The standalone simulation suite is explicit coverage, not a matrix exemption."""
import ast
from pathlib import Path
import re
import unittest

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


if __name__ == '__main__':
    unittest.main()
