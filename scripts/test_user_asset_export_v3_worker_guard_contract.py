"""Pure-Python fake-/proc tests; never signal or launch a real worker."""
import os
import io
import json
from contextlib import redirect_stderr
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

    def capture_rejection(self, action, failure, phase='resume'):
        output = io.StringIO()
        with redirect_stderr(output), self.assertRaises((RuntimeError, OSError)):
            action()
        self.process.send_signal.assert_not_called()
        lines = output.getvalue().splitlines()
        self.assertEqual(len(lines), 1)
        report = json.loads(lines[0])
        self.assertEqual(set(report), {'event', 'phase', 'failure', 'pollBefore', 'pollAfter',
                                       'procBefore', 'procAfter', 'starttimeRelation'})
        self.assertEqual(report['event'], 'owned_export_worker_identity_failure')
        self.assertEqual(report['failure'], failure)
        self.assertEqual(report['phase'], phase)
        for field in ('pollBefore', 'pollAfter'):
            self.assertIn(report[field], ('running', 'exited', 'unavailable'))
        for field in ('procBefore', 'procAfter'):
            self.assertIn(report[field], ('running', 'stopped', 'zombie', 'other', 'unavailable'))
        self.assertIn(report['starttimeRelation'], ('same', 'changed', 'unavailable'))
        for secret in (str(self.root), self.nonce, 'PRIVATE_SENTINEL', '1234', '999'):
            self.assertNotIn(secret, output.getvalue())
        return report

    def test_closed_first_failure_and_no_identity_values(self):
        cases = [('parent_changed', lambda: self.write_stat(parent=1)),
                 ('starttime_changed', lambda: self.write_stat(start='1000')),
                 ('argv_mismatch', lambda: (self.proc / 'cmdline').write_bytes(b'PRIVATE_SENTINEL')),
                 ('nonce_missing', lambda: (self.proc / 'environ').write_bytes(b'PRIVATE_SENTINEL'))]
        for failure, mutate in cases:
            before = {name: (self.proc / name).read_bytes() for name in ('stat', 'cmdline', 'environ')}
            mutate()
            report = self.capture_rejection(self.guard.resume, failure)
            self.assertEqual(report['pollBefore'], 'running')
            self.assertEqual(report['starttimeRelation'], 'same')
            for name, data in before.items(): (self.proc / name).write_bytes(data)
        for name, failure in (('exe', 'exe_mismatch'), ('cwd', 'cwd_mismatch')):
            original = (self.proc / name).readlink()
            (self.proc / name).unlink()
            (self.proc / name).symlink_to(self.root / 'PRIVATE_SENTINEL')
            self.capture_rejection(self.guard.resume, failure)
            (self.proc / name).unlink(); (self.proc / name).symlink_to(original)
        # Preserve the first failing predicate, even with several bad fields.
        self.write_stat(parent=1, start='1000')
        (self.proc / 'cmdline').write_bytes(b'PRIVATE_SENTINEL')
        self.capture_rejection(self.guard.resume, 'parent_changed')
        self.process.pid = 1
        with patch.object(self.guard, '_stat', return_value=('S', 1, '1000')), patch.object(Path, 'read_bytes', return_value=b'PRIVATE_SENTINEL'):
            self.capture_rejection(self.guard.resume, 'pid_invalid')

    def test_failed_identity_sampling_exit_change_and_unavailable(self):
        (self.proc / 'cmdline').write_bytes(b'PRIVATE_SENTINEL')
        with patch.object(self.guard, '_stat', side_effect=[('S', self.guard.parent, '999'), ('Z', self.guard.parent, '1000')]):
            self.process.poll.side_effect = [None, 0]
            report = self.capture_rejection(self.guard.assert_identity, 'argv_mismatch', 'check')
        self.assertEqual((report['pollBefore'], report['pollAfter'], report['procBefore'], report['procAfter'], report['starttimeRelation']),
                         ('running', 'exited', 'running', 'zombie', 'changed'))
        self.process.poll.side_effect = [None, OSError('PRIVATE_SENTINEL')]
        with patch.object(self.guard, '_stat', side_effect=[('T', self.guard.parent, '999'), RuntimeError('PRIVATE_SENTINEL')]):
            report = self.capture_rejection(self.guard.assert_identity, 'argv_mismatch', 'check')
        self.assertEqual((report['pollAfter'], report['procAfter'], report['starttimeRelation']), ('unavailable', 'unavailable', 'unavailable'))
        self.process.poll.side_effect = None; self.process.poll.return_value = 0
        report = self.capture_rejection(self.guard.assert_identity, 'worker_exited', 'check')
        self.assertEqual(report['procBefore'], 'unavailable')
        self.assertEqual(report['starttimeRelation'], 'unavailable')

    def test_constructor_read_failure_and_diagnostic_errors_still_reject(self):
        (self.proc / 'stat').write_bytes(b'PRIVATE_SENTINEL')
        report = self.capture_rejection(lambda: OwnedExportWorkerGuard(self.process, self.nonce, self.binary, self.root), 'identity_unavailable', 'constructor')
        self.assertEqual(report['pollBefore'], 'unavailable')
        self.write_stat()
        (self.proc / 'cmdline').write_bytes(b'PRIVATE_SENTINEL')
        self.capture_rejection(lambda: OwnedExportWorkerGuard(self.process, self.nonce, self.binary, self.root), 'argv_mismatch', 'constructor')
        with patch('user_asset_export_v3_worker_guard.json.dumps', side_effect=RuntimeError('PRIVATE_SENTINEL')), redirect_stderr(io.StringIO()) as output:
            with self.assertRaisesRegex(RuntimeError, '^owned export worker identity mismatch$'):
                self.guard.resume()
        self.assertEqual(output.getvalue(), '')
        self.process.send_signal.assert_not_called()
        (self.proc / 'cmdline').unlink()
        self.capture_rejection(self.guard.check, 'identity_unavailable', 'check')

    def test_diagnostic_output_failure_and_unknown_inputs_are_closed(self):
        (self.proc / 'cmdline').write_bytes(b'PRIVATE_SENTINEL')
        with patch('builtins.print', side_effect=OSError('PRIVATE_SENTINEL')):
            with self.assertRaisesRegex(RuntimeError, '^owned export worker identity mismatch$'):
                self.guard.resume()
        self.process.send_signal.assert_not_called()
        output = io.StringIO()
        with redirect_stderr(output):
            self.guard._diagnose_identity('PRIVATE_SENTINEL', 'argv_mismatch')
            self.guard._diagnose_identity('check', 'PRIVATE_SENTINEL')
        self.assertEqual(output.getvalue(), '')
        self.process.poll.side_effect = [OSError('PRIVATE_SENTINEL'), None]
        report = self.capture_rejection(self.guard.assert_identity, 'identity_unavailable', 'check')
        self.assertEqual(report['pollBefore'], 'unavailable')
        self.assertEqual(report['procBefore'], 'unavailable')
        self.assertEqual(report['starttimeRelation'], 'unavailable')

    def test_watchdog_failure_reports_phase_and_never_signals(self):
        self.write_stat(parent=1)
        self.guard._stop = Mock(); self.guard._stop.wait.return_value = False
        output = io.StringIO()
        with redirect_stderr(output): self.guard._watch()
        self.assertEqual(json.loads(output.getvalue())['phase'], 'watchdog')
        self.assertTrue(self.guard._failed.is_set())
        self.process.send_signal.assert_not_called()

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
