"""Pure-Python fake-/proc tests; never signal or launch a real worker."""
import os
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import Mock, patch

from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard


class WorkerGuardTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = self.root / 'checkout/bin/adtr'
        self.binary.parent.mkdir(parents=True)
        self.binary.write_bytes(b'synthetic binary, never execute')
        self.proc = self.root / '1234'
        self.proc.mkdir()
        self.nonce = 'a' * 32
        self.process = Mock(pid=1234)
        self.process.poll.return_value = None
        self.write_stat()
        (self.proc / 'cmdline').write_bytes(b'./bin/adtr\0-mode\0worker\0')
        (self.proc / 'environ').write_bytes(('ADTR_E2E_EXPORT_WORKER_NONCE=' + self.nonce).encode() + b'\0')
        (self.proc / 'exe').symlink_to(self.binary)
        (self.proc / 'cwd').symlink_to(self.binary.parent.parent)
        self.guard = OwnedExportWorkerGuard(self.process, self.nonce, self.binary, self.root)

    def write_stat(self, state='S', parent=None, start='999'):
        fields = [state, str(os.getpid() if parent is None else parent)] + ['0'] * 17 + [start]
        (self.proc / 'stat').write_text('1234 (name with ) spaces) ' + ' '.join(fields))

    def test_exact_identity_resume(self):
        self.assertEqual(self.guard.starttime, '999')
        self.guard.resume()
        self.process.send_signal.assert_called_once_with(signal.SIGCONT)

    def test_each_identity_mismatch_refuses_signal(self):
        for filename, data in [('stat', '1234 (adtr) S 1 ' + '0 ' * 17 + '999'),
                               ('cmdline', b'./bin/adtr\0-mode\0api\0'),
                               ('environ', b'ADTR_E2E_EXPORT_WORKER_NONCE=other\0')]:
            with self.subTest(filename=filename):
                path = self.proc / filename
                before = path.read_bytes()
                path.write_bytes(data.encode() if isinstance(data, str) else data)
                with self.assertRaises(RuntimeError):
                    self.guard.resume()
                self.process.send_signal.assert_not_called()
                path.write_bytes(before)
        self.write_stat(start='1000')
        with self.assertRaises(RuntimeError):
            self.guard.resume()
        self.process.send_signal.assert_not_called()

    def test_wrong_binary_and_cwd_refuse_signal(self):
        for name in ['exe', 'cwd']:
            original = (self.proc / name).readlink()
            (self.proc / name).unlink()
            (self.proc / name).symlink_to(self.root)
            with self.assertRaises(RuntimeError):
                self.guard.resume()
            self.process.send_signal.assert_not_called()
            (self.proc / name).unlink()
            (self.proc / name).symlink_to(original)

    def test_exited_handle_never_signals_reused_pid(self):
        self.process.poll.return_value = 0
        self.guard.resume()
        self.process.send_signal.assert_not_called()

    def test_watchdog_cumulative_stop_and_failure_gate(self):
        self.guard.MAX_STOPPED_SECONDS = 2
        self.guard._stop = Mock()
        self.guard._stop.wait.side_effect = [False] * 4
        with patch.object(self.guard, 'assert_identity', side_effect=['T', 'S', 'T', 'T', 'T']), patch('user_asset_export_v3_worker_guard.time.monotonic', side_effect=[0, 1, 2, 3, 4]):
            self.guard._watch()
        self.process.send_signal.assert_called_once_with(signal.SIGCONT)
        with self.assertRaises(RuntimeError):
            self.guard.check()

    def test_watchdog_identity_error_fails_without_signal(self):
        self.guard._stop = Mock()
        self.guard._stop.wait.return_value = False
        with patch.object(self.guard, 'assert_identity', side_effect=RuntimeError('identity mismatch')):
            self.guard._watch()
        self.process.send_signal.assert_not_called()
        with self.assertRaises(RuntimeError):
            self.guard.check()

    def test_malformed_stat_normalized_for_cleanup(self):
        for data in [b'invalid', b'1234 (adtr) S', b'\xff']:
            with self.subTest(data=data):
                (self.proc / 'stat').write_bytes(data)
                with self.assertRaises(RuntimeError):
                    self.guard.close()
                self.process.send_signal.assert_not_called()

    def test_unfinished_watchdog_fails_after_owned_resume(self):
        self.guard._thread = Mock()
        self.guard._thread.is_alive.return_value = True
        with self.assertRaises(RuntimeError):
            self.guard.close()
        self.guard._thread.join.assert_called_once_with(timeout=2)
        self.process.send_signal.assert_called_once_with(signal.SIGCONT)

    def test_close_resumes_before_reporting_failed_guard(self):
        self.guard._failed.set()
        with self.assertRaises(RuntimeError):
            self.guard.close()
        self.process.send_signal.assert_called_once_with(signal.SIGCONT)


if __name__ == '__main__':
    unittest.main()
