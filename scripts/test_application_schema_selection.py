"""Real graph regression, run by the Node dependency suite after npm ci."""
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ApplicationSchemaSelection(unittest.TestCase):
    def test_schema_reader_preserves_selection_proof_and_fail_closed_edges(self):
        import ci_selection as selection
        inventory = json.loads((ROOT / 'scripts/ci_inventory.json').read_text())
        sources = selection.web_sources('HEAD')
        reader = 'web/e2e/application-schema.ts'
        sources[reader] = (ROOT / reader).read_text()
        graph, uncertain = selection.dependency_graph(sources)
        fixture = 'web/src/computer-asset-export-v3-fixtures.ts'
        self.assertNotIn(reader, uncertain)
        self.assertIn(fixture, selection.test_only_dependencies(graph, uncertain, inventory))
        plan = selection.select([reader], inventory, [sources, sources])
        self.assertEqual({e['name'] for e in plan['browser']}, {
            'computer-asset-export-v3', 'computer-asset-export-v3-failure',
            'computer-asset-export-v3-cancel', 'computer-asset-export-v3-running-cancel',
        })
        plan = selection.select(['internal/schemaversion/version.go'], inventory, [sources, sources])
        self.assertTrue(all(plan['groups'].values()))
        self.assertEqual(plan['browser'], inventory['browser'])
        self.assertEqual(plan['fixed'], inventory['fixed'])
        self.assertEqual(plan['auth'], list(range(1, 17)))
        for edge in ('import(target);', "import './missing-schema-reader';",
                     'new URL("../../internal/schemaversion/version.go", import.meta.url);'):
            with self.subTest(edge=edge):
                changed = dict(sources)
                changed[reader] += '\n' + edge
                graph, uncertain = selection.dependency_graph(changed)
                self.assertIn(reader, uncertain)
                self.assertNotIn(fixture, selection.test_only_dependencies(graph, uncertain, inventory))


if __name__ == '__main__':
    unittest.main()
