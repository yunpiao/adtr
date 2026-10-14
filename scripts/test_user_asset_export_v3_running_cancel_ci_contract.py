"""Pure-Python artifact gate tests; every external subprocess is mocked."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch
import user_asset_export_v3_running_cancel_ci as gate


class RunningCancelGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.previous = Path.cwd()
        os.chdir(self.tmp.name)
        self.addCleanup(self.cleanup)

    def cleanup(self):
        os.chdir(self.previous)
        self.tmp.cleanup()

    def fixture(self):
        root = Path('web/test-results/current')
        root.mkdir(parents=True)
        for name in gate.FILENAMES:
            (root / name).write_bytes(b'unit-only')
        for name in ('trace.zip', 'secret.txt', 'forbidden.xlsx'):
            (root / name).write_bytes(b'never upload')
        (root / (gate.SUITE + '-evidence.json')).write_text(json.dumps({'cases': [{
            'running': {'screenshot': {'filename': gate.FILENAMES[0]}},
            'pending': {'screenshot': {'filename': gate.FILENAMES[1]}},
            'screenshot': {'filename': gate.FILENAMES[2]}}]}))
        gate.PROVENANCE.parent.mkdir(parents=True)
        gate.PROVENANCE.write_text('{}')
        return root

    def test_unmixed_configuration(self):
        env = {}
        gate.configure(env)
        self.assertEqual(env, {'ADTR_E2E_USER_ASSET_EXPORT_V3': 'true', 'ADTR_E2E_USER_ASSET_EXPORT_V3_RUNNING_CANCEL': 'true'})
        for mode in ('V2', 'V4', 'EMPTY', 'SLOW', 'GROUP_ASSETS_V3', 'COMPUTER_ASSETS_V3'):
            with self.assertRaises(RuntimeError):
                gate.configure({'ADTR_E2E_LDAP_DIRECTORY_' + mode: 'true'})

    def test_atomic_capture_and_failed_capture_clears_stale(self):
        with patch.object(gate.subprocess, 'run', return_value=Mock(stdout=b'{"complete":true}')) as run:
            gate.capture()
            self.assertEqual(gate.PROVENANCE.read_bytes(), b'{"complete":true}')
            self.assertEqual(list(gate.PROVENANCE.parent.glob('.running-provenance-*')), [])
            run.side_effect = subprocess.TimeoutExpired('validator', 30)
            with self.assertRaises(subprocess.TimeoutExpired):
                gate.capture()
            self.assertFalse(gate.PROVENANCE.exists())

    def test_exact_allowlist_repeatable_validation_and_stale_cleanup(self):
        self.fixture()
        with patch.object(gate.subprocess, 'run') as run:
            gate.validate()
            gate.validate()
            self.assertEqual(run.call_count, 2)
            self.assertEqual({p.name for p in gate.ARTIFACTS.iterdir()}, set(gate.FILENAMES) | {gate.SUITE + '-evidence.json', gate.PROVENANCE.name})
            run.side_effect = subprocess.CalledProcessError(1, 'validator')
            with self.assertRaises(subprocess.CalledProcessError):
                gate.validate()
            self.assertFalse(gate.ARTIFACTS.exists())

    def test_missing_duplicate_and_symlink_fail_closed(self):
        with patch.object(gate.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                gate.validate()
            run.assert_not_called()
            root = self.fixture()
            image = root / gate.FILENAMES[0]
            image.unlink()
            image.symlink_to((root / gate.FILENAMES[1]).resolve())
            with self.assertRaises(RuntimeError):
                gate.validate()
            self.assertFalse(gate.ARTIFACTS.exists())
            other = root.parent / 'other'
            other.mkdir()
            (other / (gate.SUITE + '-evidence.json')).write_text('{}')
            run.reset_mock()
            with self.assertRaises(RuntimeError):
                gate.validate()
            run.assert_not_called()

    def test_unexpected_artifact_reference_rejected(self):
        root = self.fixture()
        path = root / (gate.SUITE + '-evidence.json')
        data = json.loads(path.read_text())
        for bad in ('trace.zip', '../escape.png', 'forbidden.xlsx', gate.FILENAMES[1]):
            data['cases'][0]['running']['screenshot']['filename'] = bad
            path.write_text(json.dumps(data))
            with patch.object(gate.subprocess, 'run'), self.assertRaises(RuntimeError):
                gate.validate()
            self.assertFalse(gate.ARTIFACTS.exists())

if __name__ == '__main__':
    unittest.main()
