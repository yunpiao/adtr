"""Pure-Python terminal gate tests with subprocess mocked throughout."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import group_asset_export_v3_terminal_ci as gate


class TerminalGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.previous = Path.cwd()
        os.chdir(self.tmp.name)
        self.addCleanup(self.cleanup)
        self.suite = 'group-asset-export-v3-failure'

    def cleanup(self):
        os.chdir(self.previous)
        self.tmp.cleanup()

    def test_configuration_rejects_mixed_fixture(self):
        env = {}
        gate.configure(env, self.suite)
        self.assertEqual(env, {'ADTR_E2E_LDAP_DIRECTORY_GROUP_ASSETS_V3': 'true', 'ADTR_E2E_GROUP_ASSET_EXPORT_V3': 'true', 'ADTR_E2E_GROUP_ASSET_EXPORT_V3_TERMINAL': self.suite})
        with self.assertRaises(RuntimeError):
            gate.configure({'ADTR_E2E_LDAP_DIRECTORY_V4': 'true'}, self.suite)
        with self.assertRaises(ValueError):
            gate.configure({}, 'auth')

    def test_capture_atomic_and_failed_capture_clears_stale(self):
        provenance, artifacts = gate.paths(self.suite)
        with patch.object(gate.subprocess, 'run', return_value=Mock(stdout=b'{"complete":true}')) as run:
            gate.capture(self.suite)
            self.assertEqual(provenance.read_bytes(), b'{"complete":true}')
            self.assertEqual(list(provenance.parent.glob('.export-provenance-*')), [])
            run.side_effect = subprocess.TimeoutExpired('validator', 30)
            with self.assertRaises(subprocess.TimeoutExpired):
                gate.capture(self.suite)
            self.assertFalse(provenance.exists())

    def fixture(self):
        root = Path('web/test-results/current')
        root.mkdir(parents=True)
        files = ['failed-terminal.png', 'recovery-ready.png', 'group-assets-v3-task.xlsx']
        for name in files:
            (root / name).write_bytes(b'synthetic')
        (root / 'trace.zip').write_bytes(b'never upload')
        (root / (self.suite + '-evidence.json')).write_text(json.dumps({'cases': [
            {'screenshot': {'filename': files[0]}},
            {'screenshot': {'filename': files[1]}, 'download': {'filename': files[2]}}]}))
        provenance, artifacts = gate.paths(self.suite)
        provenance.parent.mkdir(parents=True)
        provenance.write_text('{}')
        return root, files, artifacts

    def test_only_validated_allowlist_staged_atomically_repeatable(self):
        root, files, artifacts = self.fixture()
        with patch.object(gate.subprocess, 'run') as run:
            gate.validate(self.suite)
            gate.validate(self.suite)
            self.assertEqual(run.call_count, 2)
            self.assertEqual(set(p.name for p in artifacts.iterdir()), set(files + [self.suite + '-evidence.json', self.suite + '-provenance.json']))
            run.side_effect = subprocess.CalledProcessError(1, 'validator')
            with self.assertRaises(subprocess.CalledProcessError):
                gate.validate(self.suite)
            self.assertFalse(artifacts.exists())

    def test_missing_duplicate_and_symlink_rejected(self):
        with patch.object(gate.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                gate.validate(self.suite)
            run.assert_not_called()
            root, files, artifacts = self.fixture()
            (root / files[0]).unlink()
            (root / files[0]).symlink_to(root.resolve() / files[1])
            with self.assertRaises(RuntimeError):
                gate.validate(self.suite)
            self.assertFalse(artifacts.exists())
            other = root.parent / 'other'
            other.mkdir()
            (other / (self.suite + '-evidence.json')).write_text('{}')
            run.reset_mock()
            with self.assertRaises(RuntimeError):
                gate.validate(self.suite)
            run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
