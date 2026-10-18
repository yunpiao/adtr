"""Offline source-set contracts for the pure TypeScript activity scope helper."""
import ast
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ActivityScopeContracts(unittest.TestCase):
    def test_three_provenance_sets_cover_helper_and_vitest(self):
        for kind in ('user', 'group', 'computer'):
            source = ROOT / 'scripts' / ('validate_' + kind + '_asset_export_v3_evidence.py')
            tree = ast.parse(source.read_text())
            assignment = next(node for node in tree.body if isinstance(node, ast.Assign)
                              and any(isinstance(t, ast.Name) and t.id == 'SOURCE_ROOTS'
                                      for t in node.targets))
            roots = ast.literal_eval(assignment.value)
            for path in ('web/e2e/owned-worker-activity-scope.ts',
                         'web/src/OwnedWorkerActivityScope.test.ts'):
                self.assertTrue((ROOT / path).is_file())
                self.assertTrue(any(path == root or path.startswith(root + '/') for root in roots))

    def test_module_has_no_browser_or_process_dependencies(self):
        source = (ROOT / 'web/e2e/owned-worker-activity-scope.ts').read_text()
        imports = [line for line in source.splitlines() if line.startswith('import ')]
        self.assertEqual(imports, ['import { createHash } from "node:crypto";'])
        self.assertIn('export function activityAlias(', source)
        self.assertIn('export function assertActivityScope(value: unknown): void', source)
        self.assertIn('Number.isSafeInteger(descriptor.value)', source)
        self.assertNotIn('console.', source)
        self.assertNotIn('process.', source)
        self.assertNotIn('@playwright', source)
        test = (ROOT / 'web/src/OwnedWorkerActivityScope.test.ts').read_text()
        self.assertIn('ci-activity-xjhrbwphlzfwschmfnm4kqsunp2qeowpnarubert6q2qx5ospmha', test)


if __name__ == '__main__':
    unittest.main()
