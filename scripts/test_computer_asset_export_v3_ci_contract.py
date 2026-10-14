"""Pure Python gate tests: all subprocess calls are mocked."""
from pathlib import Path
import unittest
from unittest.mock import patch
import computer_asset_export_v3_ci as gate

class ComputerExportGateContract(unittest.TestCase):
    def test_capture_and_validation_failure_propagate(self):
        import subprocess
        import tempfile
        with tempfile.TemporaryDirectory() as tmp, patch.object(gate, 'PROVENANCE', Path(tmp) / 'provenance.json'), patch.object(gate.subprocess, 'run') as run:
            for error in [subprocess.CalledProcessError(1, 'validator'), subprocess.TimeoutExpired('validator', 30)]:
                run.side_effect = error
                with self.assertRaises(type(error)):
                    gate.capture()
                self.assertFalse(gate.PROVENANCE.exists())

    def test_validation_stages_only_referenced_files_and_is_repeatable(self):
        import json
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            previous = Path.cwd()
            try:
                os.chdir(tmp)
                root = Path('web/test-results/real-test')
                root.mkdir(parents=True)
                cases = []
                for name in ['default', 'allMatched', 'empty']:
                    case = {'download': {'filename': f'computer-assets-v3-{name}.xlsx'}, 'screenshot': {'filename': f'{name}-ready.png'}}
                    cases.append(case)
                    for item in case.values():
                        (root / item['filename']).write_bytes(b'synthetic')
                evidence = root / 'computer-asset-export-v3-evidence.json'
                evidence.write_text(json.dumps({'cases': cases}))
                (root / 'trace.zip').write_bytes(b'never upload')
                gate.PROVENANCE.parent.mkdir(parents=True)
                gate.PROVENANCE.write_text('{}')
                with patch.object(gate.subprocess, 'run') as run:
                    gate.validate()
                    gate.validate()
                    self.assertEqual(run.call_count, 2)
                    self.assertEqual(len(list(gate.ARTIFACTS.iterdir())), 8)
                    self.assertFalse((gate.ARTIFACTS / 'trace.zip').exists())
                    run.assert_called_with([gate.sys.executable, gate.VALIDATOR, str(evidence), '--provenance', str(gate.PROVENANCE), '--repo', '.'], check=True, timeout=30)
                    run.side_effect = RuntimeError('invalid evidence')
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    self.assertFalse(gate.ARTIFACTS.exists())
            finally:
                os.chdir(previous)

    def test_missing_or_duplicate_evidence_fails_before_validator(self):
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            previous = Path.cwd()
            try:
                os.chdir(tmp)
                with patch.object(gate.subprocess, 'run') as run:
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    for name in ['a', 'b']:
                        root = Path('web/test-results') / name
                        root.mkdir(parents=True)
                        (root / 'computer-asset-export-v3-evidence.json').write_text('{}')
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    run.assert_not_called()
            finally:
                os.chdir(previous)

if __name__ == "__main__": unittest.main()
