"""Bounded SIGCONT safety net for this harness's exact owned export worker.

Only the queued-cancel CI suite opts in. No production behavior is changed.
The independent watchdog bounds cumulative stopped time even if the browser
hangs or exits without running its finally block.
"""
import os
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
        self.starttime = self._stat()[2]
        self._stop = threading.Event()
        self._failed = threading.Event()
        self._thread = None
        self.assert_identity()

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

    def assert_identity(self):
        if self.process.poll() is not None:
            raise RuntimeError('owned export worker exited')
        proc = self.root / str(self.process.pid)
        state, parent, starttime = self._stat()
        argv = (proc / 'cmdline').read_bytes().split(b'\0')
        environ = (proc / 'environ').read_bytes().split(b'\0')
        if (self.process.pid <= 1 or parent != self.parent or starttime != self.starttime
                or argv != [b'./bin/adtr', b'-mode', b'worker', b'']
                or (proc / 'exe').resolve() != self.binary
                or ((proc / 'cwd').resolve() / 'bin/adtr').resolve() != self.binary
                or ('ADTR_E2E_EXPORT_WORKER_NONCE=' + self.nonce).encode() not in environ):
            raise RuntimeError('owned export worker identity mismatch')
        return state

    def resume(self):
        if self.resume_delegate is not None and self.resume_delegate():
            return
        if self.process.poll() is not None:
            return
        self.assert_identity()
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
                previous_stopped = self.assert_identity() in ('T', 't')
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
