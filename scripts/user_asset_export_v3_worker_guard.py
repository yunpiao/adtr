"""Bounded SIGCONT safety net for this harness's exact owned export worker.

Only the queued-cancel CI suite opts in. No production behavior is changed.
The independent watchdog bounds cumulative stopped time even if the browser
hangs or exits without running its finally block.
"""
import os
import json
import sys
from pathlib import Path
import signal
import threading
import time


class OwnedExportWorkerGuard:
    MAX_STOPPED_SECONDS = 120.0

    def __init__(self, process, nonce, binary, proc_root=Path('/proc'), *, resume_delegate=None):
        self.resume_delegate = resume_delegate
        self.process = process
        self.nonce = nonce
        self.binary = Path(binary).resolve()
        self.root = proc_root
        self.parent = os.getpid()
        try:
            self.starttime = self._stat()[2]
        except Exception:
            self._diagnose_identity('constructor', 'identity_unavailable')
            raise
        self._stop = threading.Event()
        self._failed = threading.Event()
        self._thread = None
        self.assert_identity(phase='constructor')

    def _stat(self):
        try:
            text = (self.root / str(self.process.pid) / 'stat').read_text()
            # comm may contain spaces or parentheses; fields after the last ')' are fixed.
            end = text.rfind(')')
            if end < 0:
                raise ValueError('missing comm boundary')
            fields = text[end + 2:].split()
            if not fields[19].isdigit():
                raise ValueError('invalid starttime')
            return fields[0], int(fields[1]), fields[19]
        except (OSError, ValueError, IndexError, UnicodeError) as error:
            raise RuntimeError('owned export worker identity unavailable') from error

    def _diagnose_identity(self, phase, failure, poll_before='unavailable', before=None):
        # Failure-only observations cannot turn a rejected identity into an
        # accepted identity. Never reread or emit argv, environment or paths.
        try:
            phases = {'constructor', 'check', 'resume', 'watchdog'}
            failures = {'pid_invalid', 'parent_changed', 'starttime_changed',
                        'argv_mismatch', 'exe_mismatch', 'cwd_mismatch', 'nonce_missing',
                        'identity_unavailable', 'worker_exited'}
            if (type(phase) is not str or phase not in phases
                    or type(failure) is not str or failure not in failures):
                return
            if type(poll_before) is not str or poll_before not in ('running', 'exited', 'unavailable'):
                poll_before = 'unavailable'
            def state(value):
                if value in ('T', 't'): return 'stopped'
                if value in ('Z', 'X', 'x'): return 'zombie'
                if value in ('R', 'S', 'D', 'I'): return 'running'
                return 'other'
            report = {'event': 'owned_export_worker_identity_failure', 'phase': phase,
                      'failure': failure, 'pollBefore': poll_before,
                      'pollAfter': 'unavailable',
                      'procBefore': state(before[0]) if before is not None else 'unavailable',
                      'procAfter': 'unavailable', 'starttimeRelation': 'unavailable'}
            try:
                report['pollAfter'] = 'running' if self.process.poll() is None else 'exited'
            except Exception:
                pass
            try:
                after = self._stat()
                report['procAfter'] = state(after[0])
                if before is not None:
                    report['starttimeRelation'] = 'same' if before[2] == after[2] else 'changed'
            except Exception:
                pass
            print(json.dumps(report, separators=(',', ':')), file=sys.stderr, flush=True)
        except Exception:
            pass  # Sampling, serialization and output must preserve the original rejection.

    def assert_identity(self, *, phase='check'):
        poll_before, before = 'unavailable', None
        failure = 'identity_unavailable'
        try:
            poll_before = 'running' if self.process.poll() is None else 'exited'
            if poll_before == 'exited':
                failure = 'worker_exited'
                raise RuntimeError('owned export worker exited')
            proc = self.root / str(self.process.pid)
            before = self._stat()
            state, parent, starttime = before
            argv = (proc / 'cmdline').read_bytes().split(b'\0')
            environ = (proc / 'environ').read_bytes().split(b'\0')
            # Preserve the original predicate's short-circuit order exactly.
            if self.process.pid <= 1:
                failure = 'pid_invalid'
            elif parent != self.parent:
                failure = 'parent_changed'
            elif starttime != self.starttime:
                failure = 'starttime_changed'
            elif argv != [b'./bin/adtr', b'-mode', b'worker', b'']:
                failure = 'argv_mismatch'
            elif (proc / 'exe').resolve() != self.binary:
                failure = 'exe_mismatch'
            elif ((proc / 'cwd').resolve() / 'bin/adtr').resolve() != self.binary:
                failure = 'cwd_mismatch'
            elif ('ADTR_E2E_EXPORT_WORKER_NONCE=' + self.nonce).encode() not in environ:
                failure = 'nonce_missing'
            else:
                return state
            raise RuntimeError('owned export worker identity mismatch')
        except Exception:
            self._diagnose_identity(phase, failure, poll_before, before)
            raise

    def resume_queued_checkpoint(self):
        # Only the running checkpoint's already-verified initial boundary calls
        # this entry. Watchdogs/cleanup keep using resume and its delegate.
        if self.resume_delegate is None or self.process.poll() is not None:
            raise RuntimeError('owned queued checkpoint resume unavailable')
        if self.assert_identity(phase='resume') != 'T':
            raise RuntimeError('owned queued checkpoint is not stopped')
        self.process.send_signal(signal.SIGCONT)

    def resume(self):
        if self.resume_delegate is not None and self.resume_delegate():
            return
        if self.process.poll() is not None:
            return
        self.assert_identity(phase='resume')
        self.process.send_signal(signal.SIGCONT)

    def _watch(self):
        stopped = 0.0
        previous = time.monotonic()
        previous_stopped = False
        while not self._stop.wait(0.1):
            now = time.monotonic()
            if previous_stopped:
                stopped += now - previous
            previous = now
            try:
                previous_stopped = self.assert_identity(phase='watchdog') in ('T', 't')
                if stopped >= self.MAX_STOPPED_SECONDS:
                    self._failed.set()
                    self.resume()
                    return
            except (OSError, RuntimeError, ValueError, IndexError):
                self._failed.set()
                return

    def start(self):
        self._thread = threading.Thread(target=self._watch, name='owned-export-worker-watchdog', daemon=True)
        self._thread.start()

    def check(self):
        if self._failed.is_set():
            raise RuntimeError('owned export worker safety watchdog failed')
        self.assert_identity()

    def close(self):
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=2)
        self.resume()
        if self._thread is not None and self._thread.is_alive():
            self._failed.set()
        if self._failed.is_set():
            raise RuntimeError('owned export worker safety watchdog failed')
