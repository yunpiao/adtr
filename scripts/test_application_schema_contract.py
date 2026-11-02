"""Fail-closed checkout schema expectations; no database/browser required."""
import json
from pathlib import Path
import subprocess
import unittest

from application_schema import CURRENT_SCHEMA, parse_current_schema

ROOT = Path(__file__).resolve().parents[1]
VALID = [b'package schemaversion\nconst Current = ' + str(n).encode() + b'\n' for n in (1, 23, 24, 25, 26, 999999)]
INVALID = [
    b'', b'package other\nconst Current = 24', b'package schemaversion\nconst Current = 0',
    b'package schemaversion\nconst Current = 024', b'package schemaversion\nconst Current = -1',
    b'package schemaversion\nconst Current = 24 + 1', b'package schemaversion\nconst Current = 24.0',
    b'package schemaversion\nconst Current = 1000000', b'package schemaversion\nconst Current = 24\nconst Current = 25',
    b'package schemaversion\n// const Current = 24', b'package schemaversion\nvar Current = 24',
    b'package schemaversion\nconst Current = 24\nvar Other = 1', b'\xff', b' ' * 4097,
]


class ApplicationSchemaContract(unittest.TestCase):
    def test_exact_checkout_and_parser(self):
        self.assertEqual(CURRENT_SCHEMA, parse_current_schema((ROOT / 'internal/schemaversion/version.go').read_bytes()))
        for source, expected in zip(VALID, (1, 23, 24, 25, 26, 999999)):
            self.assertEqual(parse_current_schema(source), expected)
        self.assertEqual(parse_current_schema(b'// const Current = 3\n' + VALID[2]), 24)
        for source in INVALID:
            with self.subTest(source=source[:100]), self.assertRaises(ValueError):
                parse_current_schema(source)

    def test_typescript_matches_fail_closed_python_parser(self):
        script = '''
import { parseCurrentSchema, currentApplicationSchema } from './web/e2e/application-schema.ts';
import { readFileSync } from 'node:fs';
const inputs = JSON.parse(readFileSync(0, 'utf8'));
console.log(JSON.stringify({ current: currentApplicationSchema, results: inputs.map(value => {
  try { return parseCurrentSchema(Buffer.from(value, 'hex')); } catch { return null; }
}) }));
'''
        inputs = VALID + INVALID
        result = subprocess.run(['node', '--input-type=module', '-e', script], cwd=ROOT,
                                input=json.dumps([source.hex() for source in inputs]), text=True,
                                capture_output=True, check=True, timeout=20)
        actual = json.loads(result.stdout)
        self.assertEqual(actual, {'current': CURRENT_SCHEMA, 'results': [1, 23, 24, 25, 26, 999999] + [None] * len(INVALID)})

    def test_all_evidence_validators_reject_noncurrent_schema(self):
        import test_computer_asset_export_v3_evidence_contract as success
        import test_computer_asset_export_v3_terminal_evidence_contract as terminal
        import test_computer_asset_export_v3_running_cancel_evidence_contract as running
        for module in (success, terminal, running):
            data, files = module.specimen()
            module.v.validate(data, files.__getitem__, {'fixtureOnly': True})
            for point in ('before', 'after'):
                for wrong in (23, CURRENT_SCHEMA - 1, CURRENT_SCHEMA + 1, True, str(CURRENT_SCHEMA)):
                    with self.subTest(module=module.__name__, point=point, wrong=wrong):
                        data, files = module.specimen()
                        data['sensitiveBudget'][point]['schemaVersion'] = wrong
                        with self.assertRaises(ValueError):
                            module.v.validate(data, files.__getitem__, {'fixtureOnly': True})

    def test_consumers_keep_exact_independent_schema_expectations(self):
        for filename in ('computer-asset-export-v3-terminal-helpers.ts', 'computer-asset-export-v3.spec.ts'):
            source = (ROOT / 'web/e2e' / filename).read_text()
            self.assertIn('import { currentApplicationSchema } from "./application-schema";', source)
            self.assertIn('schemaVersion: currentApplicationSchema, owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0', source)
            self.assertNotIn('schemaVersion: 23', source)
        for filename in ('validate_computer_asset_export_v3_evidence.py', 'validate_computer_asset_export_v3_terminal_evidence.py', 'validate_computer_asset_export_v3_running_cancel_evidence.py'):
            source = (ROOT / 'scripts' / filename).read_text()
            self.assertIn('from application_schema import CURRENT_SCHEMA', source)
            self.assertNotRegex(source, r"'schemaVersion':\s*23\b")


if __name__ == '__main__':
    unittest.main()
