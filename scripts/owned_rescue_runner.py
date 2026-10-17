"""CI-only ownership and bounded socket transport for one worker, one epoch.

No attach API: the runner creates the worker, anonymous page and rescue sibling.
Runtime tests involving processes/signals run only in the isolated Linux CI job.
"""
import json
import base64
import hashlib
import re
from enum import Enum
from types import MappingProxyType
import mmap
import os
from pathlib import Path
import select
import socket
import signal
import struct
import subprocess
import sys
import threading
import time


class ControlFailure(RuntimeError):
    """Closed diagnostic: never expose frame data or process environment."""


class ControlOperation(Enum):
    ARM = 'arm'
    COMMIT_CAPTURE = 'commit_capture'
    PREPARE_SELECTION = 'prepare_selection'
    CONFIGURE_TARGET = 'configure_target'
    FINISH_TARGET = 'finish_target'
    SNAPSHOT = 'snapshot'
    PIN = 'pin'
    AFTER_STOP = 'after_stop'


class ControlStep(Enum):
    SEND = 'send'
    RECEIVE = 'receive'
    SCHEMA = 'schema'
    REPLY = 'reply'
    STATE = 'state'


class ControlCause(Enum):
    DEADLINE = 'deadline'
    EOF = 'eof'
    PARTIAL = 'partial'
    FRAME = 'frame'
    STATE = 'state'
    TRANSPORT = 'transport'
    OTHER = 'other'


CONTROL_DIAGNOSTIC_VALUES = MappingProxyType({
    'operation': frozenset(value.value for value in ControlOperation),
    'step': frozenset(value.value for value in ControlStep),
    'cause': frozenset(value.value for value in ControlCause),
})


def _fail(cause=ControlCause.OTHER):
    error = ControlFailure('owned CI worker control unavailable')
    error.control_cause = cause if type(cause) is ControlCause else ControlCause.OTHER
    raise error


def _unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            _fail(ControlCause.FRAME)
        value[key] = item
    return value


class PipeChannel:
    """One reader and one writer; bounded frames on owned socket endpoints.

    The absolute caller deadline is never renewed on EINTR/EAGAIN/partial input.
    Closing either endpoint cancels bounded select waits. No arbitrary io.Writer.
    """
    LIMIT = 65536

    def __init__(self, read_fd=None, write_fd=None, *, read_limit=65536, write_limit=1024):
        self.LIMIT, self.write_limit = read_limit, write_limit
        self.read_fd, self.write_fd = read_fd, write_fd
        self.buffer = bytearray()
        self.failed = False
        self.write_lock = threading.Lock()
        for fd in (read_fd, write_fd):
            if fd is not None:
                os.set_blocking(fd, False)

    def _ready(self, fd, writing, deadline):
        while True:
            left = deadline - time.monotonic()
            if self.failed or fd is None:
                _fail(ControlCause.STATE)
            if left <= 0:
                _fail(ControlCause.DEADLINE)
            try:
                ready = select.select([] if writing else [fd], [fd] if writing else [], [], left)
                if ready[1 if writing else 0]:
                    return
                _fail(ControlCause.DEADLINE)
            except InterruptedError:
                continue
            except (OSError, ValueError):
                _fail(ControlCause.TRANSPORT)

    def send(self, frame, deadline):
        data = json.dumps(frame, separators=(',', ':'), allow_nan=False).encode('ascii') + b'\n'
        if len(data) > self.write_limit:
            _fail(ControlCause.FRAME)
        # One bounded write. Partial socket writes fail permanently; no replay.
        if not self.write_lock.acquire(timeout=max(0, deadline - time.monotonic())):
            _fail(ControlCause.DEADLINE)
        try:
            while True:
                self._ready(self.write_fd, True, deadline)
                try:
                    if os.write(self.write_fd, data) != len(data):
                        self.failed = True
                        _fail(ControlCause.PARTIAL)
                    return
                except BlockingIOError:
                    continue
                except OSError:
                    self.failed = True
                    _fail(ControlCause.TRANSPORT)
        finally:
            self.write_lock.release()

    def receive(self, deadline):
        while True:
            if self.failed:
                _fail(ControlCause.STATE)
            if time.monotonic() >= deadline:
                _fail(ControlCause.DEADLINE)
            if b'\n' in self.buffer:
                raw, _, rest = self.buffer.partition(b'\n')
                self.buffer = bytearray(rest)
                if len(raw) + 1 > self.LIMIT:
                    _fail(ControlCause.FRAME)
                try:
                    result = json.loads(raw, object_pairs_hook=_unique_object,
                                        parse_constant=lambda _: _fail(ControlCause.FRAME))
                except (ValueError, UnicodeError):
                    self.failed = True
                    _fail(ControlCause.FRAME)
                if not isinstance(result, dict):
                    _fail(ControlCause.FRAME)
                return result
            if len(self.buffer) >= self.LIMIT:
                self.failed = True
                _fail(ControlCause.FRAME)
            self._ready(self.read_fd, False, deadline)
            try:
                chunk = os.read(self.read_fd, self.LIMIT)
            except BlockingIOError:
                continue
            except OSError:
                self.failed = True
                _fail(ControlCause.TRANSPORT)
            if not chunk:
                self.failed = True  # EOF and half frame are permanently invalid.
                _fail(ControlCause.PARTIAL if self.buffer else ControlCause.EOF)
            self.buffer.extend(chunk)

    def drain(self):
        """Nonblocking observation after all worker threads are stopped."""
        frames = []
        while True:
            if self.failed or self.read_fd is None:
                _fail(ControlCause.STATE)
            if b'\n' in self.buffer:
                frames.append(self.receive(time.monotonic() + .01))
                continue
            try:
                chunk = os.read(self.read_fd, self.LIMIT)
            except BlockingIOError:
                if self.buffer:
                    self.failed = True
                    _fail(ControlCause.PARTIAL)
                return frames
            except OSError:
                self.failed = True
                _fail(ControlCause.TRANSPORT)
            if not chunk:
                self.failed = True
                _fail(ControlCause.PARTIAL if self.buffer else ControlCause.EOF)
            self.buffer.extend(chunk)
            if len(self.buffer) > self.LIMIT and b'\n' not in self.buffer:
                self.failed = True
                _fail(ControlCause.FRAME)

    def close(self):
        self.failed = True
        fds = {self.read_fd, self.write_fd} - {None}
        self.read_fd = self.write_fd = None
        for fd in fds:
            os.close(fd)


def read_identity(pid):
    """Kernel identity, no argv/environment values enter diagnostics."""
    try:
        root = Path('/proc') / str(pid)
        fields = (root / 'stat').read_text().rsplit(')', 1)[1].split()
        return int(fields[19]), str((root / 'exe').resolve(strict=True)), fields[0]
    except (OSError, ValueError, IndexError):
        _fail()


class OwnedIdentity:
    def __init__(self, pid, starttime, executable):
        if pid <= 1 or starttime <= 0:
            _fail()
        self.pid, self.starttime, self.executable = pid, starttime, executable
        # pidfd binds signals to this incarnation even if PID is later reused.
        self.pidfd = os.pidfd_open(pid)
        try:
            self.verify()
        except BaseException:
            os.close(self.pidfd)
            raise

    def verify(self):
        start, executable, state = read_identity(self.pid)
        if start != self.starttime or executable != self.executable or state in ('Z', 'X', 'x'):
            _fail()
        return state

    def send(self, sig):
        self.verify()
        signal.pidfd_send_signal(self.pidfd, sig)

    def threads_running(self):
        self.verify()
        try:
            states = [(p / 'stat').read_text().rsplit(')', 1)[1].split()[0]
                      for p in (Path('/proc') / str(self.pid) / 'task').iterdir()]
        except (OSError, IndexError):
            _fail()
        return bool(states) and all(state not in ('T', 't', 'Z', 'X', 'x') for state in states)

    def threads_stopped(self):
        self.verify()
        try:
            states = [(p / 'stat').read_text().rsplit(')', 1)[1].split()[0]
                      for p in (Path('/proc') / str(self.pid) / 'task').iterdir()]
        except (OSError, IndexError):
            _fail()
        return bool(states) and all(state == 'T' for state in states)

    def resume(self, deadline):
        self.send(signal.SIGCONT)
        while time.monotonic() < deadline:
            if self.threads_running():
                return
            time.sleep(.002)
        _fail()

    def close(self):
        os.close(self.pidfd)


PAGE_SIZE = 4096
HEADER = struct.Struct('<8sIIQII16s')
ENV_FDS = ('ADTR_CI_DB_NORMAL_FD', 'ADTR_CI_DB_RESCUE_FD', 'ADTR_CI_DB_WITNESS_FD', 'ADTR_CI_CAPTURE_END_FD')
CAPTURE_END = struct.Struct('!8s16sQQQ')


def _int(value, minimum=0):
    return type(value) is int and value >= minimum


def _frame(frame, seq=None):
    expected = {'version', 'epoch', 'seq', 'kind', 'status', 'pending', 'open',
                'revision', 'target', 'records'}
    if (set(frame) != expected or frame['version'] != 2 or frame['epoch'] != 1
            or not _int(frame['version']) or not _int(frame['epoch'])
            or not all(_int(frame[k]) for k in ('seq', 'pending', 'open', 'revision', 'target'))
            or frame['kind'] not in ('reply', 'ready', 'invalid')
            or frame['status'] not in ('open', 'draining', 'sealed', 'invalid', 'released')
            or not isinstance(frame['records'], list) or len(frame['records']) > 128):
        _fail(ControlCause.FRAME)
    if seq is not None and frame['seq'] != seq:
        _fail(ControlCause.FRAME)
    seen = set()
    for record in frame['records']:
        if (not isinstance(record, dict) or set(record) != {'generation', 'state', 'backend', 'candidate'}
                or not _int(record['generation'], 1) or record['generation'] in seen
                or record['state'] not in ('pending', 'open', 'closing')
                or not _int(record['backend']) or record['backend'] > 2 ** 32 - 1):
            _fail(ControlCause.FRAME)
        seen.add(record['generation'])
        candidate = record['candidate']
        if candidate is not None:
            if (not isinstance(candidate, dict) or set(candidate) != {
                    'generation', 'taskID', 'kind', 'attempt', 'leaseOwner',
                    'fencingToken', 'resultVersion', 'phase', 'activityKey'}
                    or not all(_int(candidate[k], 1) for k in (
                        'generation', 'attempt', 'fencingToken', 'resultVersion'))
                    or not all(isinstance(candidate[k], str) and 0 < len(candidate[k]) <= 256
                               for k in ('taskID', 'kind', 'leaseOwner'))
                    or type(candidate['activityKey']) is not str
                    or re.fullmatch(r'[a-z0-9_-]{1,64}', candidate['activityKey']) is None
                    or candidate['phase'] != 'first-fenced-progress-10'):
                _fail(ControlCause.FRAME)
    return frame


def _rescue_exchange(channel, seq, abort, deadline):
    channel.send({'version': 2, 'epoch': 1, 'seq': seq,
                  'op': 'abort' if abort else 'release'}, deadline)
    frame = channel.receive(deadline)
    # Rescue acknowledgements are distinct from normal snapshot frames.
    if (set(frame) != {'version', 'epoch', 'seq', 'kind', 'status'}
            or type(frame.get('version')) is not int or type(frame.get('epoch')) is not int
            or frame.get('version') != 2 or frame.get('epoch') != 1
            or type(frame.get('seq')) is not int or frame['seq'] != seq
            or frame.get('kind') != 'reply' or frame.get('status') != 'released'):
        _fail()


DIAGNOSTIC_REASONS = frozenset({'none', 'capacity', 'connect_failed', 'invalid_connect_state',
    'duplicate_backend', 'invalid_close_state', 'unclean_close', 'target_closed',
    'target_closing', 'target_revoked', 'wait_cancelled', 'transport', 'record_capacity',
    'abort', 'explicit_invalidation'})
DIAGNOSTIC_PHASES = frozenset({'open', 'draining', 'sealed', 'released'})


FINAL_INVALIDATION_EVENT = 'owned_ci_first_invalidation'
MAX_FINAL_LOG_BYTES = 8 << 20


def first_invalidation_projection(logs):
    """Bounded private-log read; return only exact enum fields, never raw content."""
    unavailable = {'event': FINAL_INVALIDATION_EVENT, 'reason': 'unknown', 'phase': 'unknown'}
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return unavailable
        candidates = [line for line in text.splitlines(keepends=True) if FINAL_INVALIDATION_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return unavailable
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        if (type(frame) is not dict or set(frame) != {'event', 'reason', 'phase'}
                or frame['event'] != FINAL_INVALIDATION_EVENT
                or type(frame['reason']) is not str
                or frame['reason'] not in DIAGNOSTIC_REASONS | {'unknown'}
                or type(frame['phase']) is not str
                or frame['phase'] not in DIAGNOSTIC_PHASES | {'unknown'}):
            return unavailable
        return {'event': FINAL_INVALIDATION_EVENT, 'reason': frame['reason'], 'phase': frame['phase']}
    except Exception:
        return unavailable
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


FIRST_UNCLEAN_EVENT = 'owned_ci_first_unclean_close'


def first_unclean_projection(logs):
    """Closed diagnostic only; unknown never substitutes for cleanup success."""
    unknown = {'event': FIRST_UNCLEAN_EVENT, 'alreadyClosed': 'unknown',
               'closeFailed': 'unknown', 'cleanupDone': 'unknown', 'role': 'unknown'}
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return unknown
        candidates = [line for line in text.splitlines(keepends=True) if FIRST_UNCLEAN_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return unknown
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        if (type(frame) is not dict or set(frame) != set(unknown)
                or frame['event'] != FIRST_UNCLEAN_EVENT
                or any(type(frame[key]) is not bool for key in ('alreadyClosed', 'closeFailed', 'cleanupDone'))
                or type(frame['role']) is not str or frame['role'] not in ('epoch_target', 'other')):
            return unknown
        return {key: frame[key] for key in unknown}
    except Exception:
        return unknown
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


UNCLEAN_OPERATION_EVENT = 'owned_ci_unclean_operation'
OPERATION_PHASES = frozenset({'open', 'draining', 'sealed', 'released', 'unknown'})
OPERATION_CONTEXTS = frozenset({'active', 'canceled', 'deadline_exceeded', 'other', 'unknown'})


FIRST_CONNECT_EVENT = 'owned_ci_first_connect_failure'


def first_connect_projection(logs):
    """Diagnostic only: neither a network category nor context state grants recovery."""
    unknown = {'event': FIRST_CONNECT_EVENT, 'birthPhase': 'unknown',
               'birthCaptureEnded': 'unknown', 'birthRecoveryAuthorized': 'unknown',
               'failurePhase': 'unknown', 'contextState': 'unknown',
               'errorCategory': 'unknown', 'errorMatchesContext': 'unknown',
               'rootContextState': 'unknown', 'rawNil': 'unknown'}
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return unknown
        candidates = [line for line in text.splitlines(keepends=True) if FIRST_CONNECT_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return unknown
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        if (type(frame) is not dict or set(frame) != set(unknown)
                or frame['event'] != FIRST_CONNECT_EVENT
                or any(type(frame[key]) is not str or frame[key] not in OPERATION_PHASES
                       for key in ('birthPhase', 'failurePhase'))
                or any(type(frame[key]) is not str or frame[key] not in OPERATION_CONTEXTS
                       for key in ('contextState', 'rootContextState'))
                or type(frame['errorCategory']) is not str
                or frame['errorCategory'] not in ('none', 'canceled', 'deadline_exceeded',
                                                  'authentication', 'network_error', 'other')
                or any(type(frame[key]) is not bool for key in
                       ('birthCaptureEnded', 'birthRecoveryAuthorized', 'errorMatchesContext', 'rawNil'))):
            return unknown
        return {key: frame[key] for key in unknown}
    except Exception:
        return unknown
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


def unclean_operation_projection(logs):
    """Project only closed operation diagnostics; malformed evidence is not null."""
    unknown = {'event': UNCLEAN_OPERATION_EVENT, 'birthPhase': 'unknown',
               'birthCaptureEnded': 'unknown', 'firstError': 'unknown',
               'firstNativeClosedError': 'unknown', 'rootAtClose': 'unknown'}
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return unknown
        candidates = [line for line in text.splitlines(keepends=True) if UNCLEAN_OPERATION_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return unknown
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        if (type(frame) is not dict or set(frame) != set(unknown)
                or frame['event'] != UNCLEAN_OPERATION_EVENT
                or type(frame['birthPhase']) is not str or frame['birthPhase'] not in OPERATION_PHASES
                or type(frame['birthCaptureEnded']) is not bool
                or type(frame['rootAtClose']) is not str or frame['rootAtClose'] not in OPERATION_CONTEXTS):
            return unknown
        fields = {'operation', 'startPhase', 'endPhase', 'contextState', 'errorCategory',
                  'errorMatchesContext', 'nativeClosedAtEnd', 'rootContextState', 'connectionMatched'}
        records = {}
        for key in ('firstError', 'firstNativeClosedError'):
            record = frame[key]
            if record is None:
                records[key] = None
                continue
            if (type(record) is not dict or set(record) != fields
                    or type(record['operation']) is not str
                    or record['operation'] not in ('begin', 'commit', 'rollback', 'statement')
                    or any(type(record[k]) is not str or record[k] not in OPERATION_PHASES
                           for k in ('startPhase', 'endPhase'))
                    or any(type(record[k]) is not str or record[k] not in OPERATION_CONTEXTS
                           for k in ('contextState', 'rootContextState'))
                    or type(record['errorCategory']) is not str
                    or record['errorCategory'] not in ('none', 'canceled', 'deadline_exceeded', 'other')
                    or any(type(record[k]) is not bool for k in
                           ('errorMatchesContext', 'nativeClosedAtEnd', 'connectionMatched'))):
                return unknown
            records[key] = {field: record[field] for field in fields}
        return {'event': UNCLEAN_OPERATION_EVENT, 'birthPhase': frame['birthPhase'],
                'birthCaptureEnded': frame['birthCaptureEnded'],
                'rootAtClose': frame['rootAtClose'], **records}
    except Exception:
        return unknown
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


FINAL_JOURNAL_EVENT = 'owned_ci_final_journal'
FINAL_JOURNAL_COUNTS = frozenset({'accepted', 'acknowledged', 'rejected', 'queueFull',
                                  'writeFailures', 'unacknowledged', 'abandoned'})


def final_journal_projection(logs):
    """Private final Stats only; no event/hash/raw log is ever printed."""
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return None
        candidates = [line for line in text.splitlines(keepends=True) if FINAL_JOURNAL_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return None
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        fields = FINAL_JOURNAL_COUNTS | {'event', 'processIDSHA256', 'closeOK', 'acceptanceStopped'}
        if (type(frame) is not dict or set(frame) != fields
                or frame['event'] != FINAL_JOURNAL_EVENT
                or type(frame['processIDSHA256']) is not str
                or re.fullmatch(r'[0-9a-f]{64}', frame['processIDSHA256']) is None
                or type(frame['closeOK']) is not bool or type(frame['acceptanceStopped']) is not bool
                or not all(type(frame[key]) is int and 0 <= frame[key] <= 2 ** 63 - 1
                           for key in FINAL_JOURNAL_COUNTS)):
            return None
        return {key: frame[key] for key in fields}
    except Exception:
        return None
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


RECOVERY_CLEANUP_EVENT = 'owned_ci_recovery_cleanup'
RECOVERY_CLEANUP_COUNTS = frozenset({'qualified', 'completed', 'pending', 'open',
                                     'connectPending', 'failed'})


def recovery_cleanup_projection(logs):
    """Private shutdown proof; absent or malformed summaries are never zero."""
    position = None
    try:
        logs.flush()
        position = logs.tell()
        logs.seek(0)
        text = logs.read(MAX_FINAL_LOG_BYTES + 1)
        if type(text) is not str or len(text.encode('utf-8')) > MAX_FINAL_LOG_BYTES:
            return None
        candidates = [line for line in text.splitlines(keepends=True) if RECOVERY_CLEANUP_EVENT in line]
        if (len(candidates) != 1 or not candidates[0].endswith('\n')
                or len(candidates[0].encode('utf-8')) > 1024):
            return None
        frame = json.loads(candidates[0], object_pairs_hook=_unique_object,
                           parse_constant=lambda _: _fail())
        flags = {'captureAuthorized', 'released', 'settled'}
        fields = RECOVERY_CLEANUP_COUNTS | flags | {'event'}
        if (type(frame) is not dict or set(frame) != fields
                or frame['event'] != RECOVERY_CLEANUP_EVENT
                or not all(type(frame[key]) is bool for key in flags)
                or not all(type(frame[key]) is int and 0 <= frame[key] <= 2 ** 63 - 1
                           for key in RECOVERY_CLEANUP_COUNTS)):
            return None
        # Accounting/authorization and independent PostgreSQL proof belong to guard.
        return {key: frame[key] for key in fields}
    except Exception:
        return None
    finally:
        if position is not None:
            try:
                logs.seek(position)
            except Exception:
                pass


def diagnostic_projection(frame, seq):
    fields = {'version', 'epoch', 'seq', 'kind', 'reason', 'phase',
              'pending', 'open', 'total', 'retained'}
    if (not isinstance(frame, dict) or set(frame) != fields
            or type(frame['version']) is not int or frame['version'] != 2
            or type(frame['epoch']) is not int or frame['epoch'] != 1
            or type(frame['seq']) is not int or frame['seq'] != seq
            or frame['kind'] != 'diagnostic'
            or not isinstance(frame['reason'], str) or frame['reason'] not in DIAGNOSTIC_REASONS
            or not isinstance(frame['phase'], str) or frame['phase'] not in DIAGNOSTIC_PHASES
            or not all(_int(frame[key]) and frame[key] <= 2 ** 64 - 1
                       for key in ('pending', 'open', 'total', 'retained'))):
        _fail()
    # Construct an allowlisted projection; never print a private wire frame.
    return {'event': 'ci_worker_admission_failure', **{key: frame[key] for key in
            ('reason', 'phase', 'pending', 'open', 'total', 'retained')}}


class WorkerController:
    def __init__(self, owner, normal, supervisor, rescue_fallback, witness, nonce):
        self.owner, self.normal, self.supervisor = owner, normal, supervisor
        self.rescue_fallback, self.witness, self.nonce = rescue_fallback, witness, nonce
        self.seq = 0
        self.invalid = False
        self.armed = self.released = False
        self.capture_deadline = None
        self.commit_attempted = self.committed = False
        self.selection_prepared = self.target_sent = self.target_finished = False
        self.mutex = threading.RLock()

    def _latch(self, operation=None, step=ControlStep.STATE, cause=ControlCause.STATE):
        self.invalid = True
        error = ControlFailure('owned CI worker control unavailable')
        if (type(operation) is ControlOperation and type(step) is ControlStep
                and type(cause) is ControlCause):
            error.control_diagnostic = {'operation': operation.value,
                                        'step': step.value, 'cause': cause.value}
        raise error

    @staticmethod
    def _cause(error):
        cause = getattr(error, 'control_cause', None) if type(error) is ControlFailure else None
        return cause if type(cause) is ControlCause else ControlCause.OTHER

    def ready(self, deadline):
        try:
            frame = _frame(self.normal.receive(deadline), 0)
            if frame['kind'] != 'ready' or frame['status'] != 'open':
                self._latch()
            if self.supervisor.receive(deadline) != {'version': 2, 'epoch': 1, 'status': 'ready'}:
                self._latch()
        except (OSError, ControlFailure):
            self._latch()

    def _configuration_ack(self, deadline, operation):
        step = ControlStep.RECEIVE
        try:
            response = self.normal.receive(deadline)
            step = ControlStep.SCHEMA
            frame = _frame(response, self.seq)
            step = ControlStep.REPLY
            if frame['kind'] != 'reply' or frame['status'] != 'open':
                _fail(ControlCause.STATE)
            return frame
        except ControlFailure as error:
            self._latch(operation, step, self._cause(error))
        except OSError:
            self._latch(operation, step, ControlCause.TRANSPORT)

    def prepare_selection(self, kind, deadline):
        """Live-worker handshake before the existing queued pause."""
        operation = ControlOperation.PREPARE_SELECTION
        with self.mutex:
            if self.invalid or self.armed or self.released or self.selection_prepared:
                self._latch(operation)
            if type(kind) is not str or kind not in (
                    'user_assets.v3.export', 'group_assets.v3.export',
                    'computer_assets.v3.export'):
                self._latch(operation, ControlStep.SCHEMA, ControlCause.FRAME)
            self.selection_prepared = True  # No replay even if send/ACK fails.
            self.seq += 1
            try:
                self.normal.send({'version': 2, 'epoch': 1, 'seq': self.seq,
                                  'op': 'prepare_selection', 'kind': kind}, deadline)
            except ControlFailure as error:
                self._latch(operation, ControlStep.SEND, self._cause(error))
            except OSError:
                self._latch(operation, ControlStep.SEND, ControlCause.TRANSPORT)
            return self._configuration_ack(deadline, operation)

    def prepare_target(self, task_id, worker_id, deadline):
        """Queue one frame while stopped; NEVER wait for the stopped worker."""
        operation = ControlOperation.CONFIGURE_TARGET
        with self.mutex:
            if (self.invalid or self.armed or self.released or not self.selection_prepared
                    or self.target_sent):
                self._latch(operation)
            if (type(task_id) is not str or re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', task_id) is None
                    or type(worker_id) is not str
                    or re.fullmatch(r'[a-z0-9_-]{1,64}', worker_id) is None):
                self._latch(operation, ControlStep.SCHEMA, ControlCause.FRAME)
            self.target_sent = True
            self.seq += 1
            try:
                self.normal.send({'version': 2, 'epoch': 1, 'seq': self.seq,
                                  'op': 'configure_target', 'taskID': task_id,
                                  'workerID': worker_id}, deadline)
            except ControlFailure as error:
                self._latch(operation, ControlStep.SEND, self._cause(error))
            except OSError:
                self._latch(operation, ControlStep.SEND, ControlCause.TRANSPORT)
            digest = hashlib.sha256(b'adtr-ci-activity-v1\x00' + self.nonce + b'\x00'
                                    + task_id.encode('utf-8')).digest()
            return 'ci-activity-' + base64.b32encode(digest).decode('ascii').lower().rstrip('=')

    def finish_target(self, deadline):
        """Read the queued configuration ACK after CONT, within the original deadline."""
        operation = ControlOperation.FINISH_TARGET
        with self.mutex:
            if (self.invalid or not self.armed or self.released or not self.target_sent
                    or self.target_finished):
                self._latch(operation)
            self.target_finished = True
            return self._configuration_ack(deadline, operation)

    def arm(self, deadline):
        operation = ControlOperation.ARM
        with self.mutex:
            if self.invalid or self.armed or self.released:
                self._latch(operation)
            self.armed = True  # An uncertain ARM send must never permit parent CONT.
            self.capture_deadline = deadline
            step = ControlStep.SEND
            try:
                self.supervisor.send({'version': 2, 'epoch': 1, 'op': 'arm',
                                      'deadline': deadline}, deadline)
                step = ControlStep.RECEIVE
                response = self.supervisor.receive(deadline)
                step = ControlStep.REPLY
                if response != {'version': 2, 'epoch': 1, 'status': 'armed'}:
                    _fail(ControlCause.FRAME)
                self.armed = True
            except ControlFailure as error:
                self._latch(operation, step, self._cause(error))
            except OSError:
                self._latch(operation, step, ControlCause.TRANSPORT)

    def commit_capture(self, ticket_generation, registration_generation, deadline):
        operation = ControlOperation.COMMIT_CAPTURE
        with self.mutex:
            if (self.invalid or not self.armed or self.released or self.commit_attempted
                    or deadline != self.capture_deadline):
                self._latch(operation)
            self.commit_attempted = True
            if not all(_int(value, 1) and value <= 2 ** 64 - 1
                       for value in (ticket_generation, registration_generation)):
                self._latch(operation, ControlStep.SCHEMA, ControlCause.FRAME)
            step = ControlStep.SEND
            try:
                if time.monotonic() >= deadline:
                    _fail(ControlCause.DEADLINE)
                self.supervisor.send({'version': 2, 'epoch': 1, 'op': 'commit_capture',
                    'ticketGeneration': ticket_generation,
                    'registrationGeneration': registration_generation}, deadline)
                step = ControlStep.RECEIVE
                response = self.supervisor.receive(deadline)
                step = ControlStep.REPLY
                expected = {'version': 2, 'epoch': 1, 'status': 'committed',
                            'ticketGeneration': ticket_generation,
                            'registrationGeneration': registration_generation}
                if (response != expected or any(type(response.get(key)) is not int
                        for key in ('version', 'epoch', 'ticketGeneration', 'registrationGeneration'))):
                    _fail(ControlCause.FRAME)
                if time.monotonic() >= deadline:
                    _fail(ControlCause.DEADLINE)
                self.committed = True
                return True
            except ControlFailure as error:
                self._latch(operation, step, self._cause(error))
            except OSError:
                self._latch(operation, step, ControlCause.TRANSPORT)

    def _request(self, op, deadline, target=None):
        operation = ControlOperation(op)
        with self.mutex:
            if (self.invalid or not self.armed or self.released
                    or (self.target_sent and not self.target_finished)):
                self._latch(operation)
            self.seq += 1
            request = {'version': 2, 'epoch': 1, 'seq': self.seq, 'op': op}
            if target is not None:
                if not _int(target, 1):
                    self._latch(operation, ControlStep.SCHEMA, ControlCause.FRAME)
                request['target'] = target
            step = ControlStep.SEND
            try:
                self.normal.send(request, deadline)
                step = ControlStep.RECEIVE
                response = self.normal.receive(deadline)
                step = ControlStep.SCHEMA
                frame = _frame(response, self.seq)
                step = ControlStep.REPLY
                if frame['kind'] != 'reply' or frame['status'] in ('invalid', 'released'):
                    _fail(ControlCause.STATE)
                return frame
            except ControlFailure as error:
                self._latch(operation, step, self._cause(error))
            except OSError:
                self._latch(operation, step, ControlCause.TRANSPORT)

    def snapshot(self, deadline):
        return self._request('snapshot', deadline)

    def pin(self, generation, deadline):
        return self._request('pin', deadline, generation)

    def after_stop(self, deadline):
        """Caller MUST establish all owned threads T before entering this method."""
        operation = ControlOperation.AFTER_STOP
        with self.mutex:
            if self.invalid or not self.armed or self.released:
                self._latch(operation)
            if time.monotonic() >= deadline:
                self._latch(operation, ControlStep.STATE, ControlCause.DEADLINE)
            step = ControlStep.RECEIVE
            try:
                # No requests to a stopped process. Unsolicited/partial/EOF fails.
                if self.normal.drain():
                    _fail(ControlCause.FRAME)
                step = ControlStep.SCHEMA
                first = self.witness[:]
                second = self.witness[:]
                expected = HEADER.pack(b'ADTRGATE', 1, PAGE_SIZE, 1, 0, 1, self.nonce)
                if (first != second or len(first) != PAGE_SIZE or first[:HEADER.size] != expected
                        or any(first[HEADER.size:])):
                    _fail(ControlCause.FRAME)
                if time.monotonic() >= deadline:
                    _fail(ControlCause.DEADLINE)
            except ControlFailure as error:
                self._latch(operation, step, self._cause(error))
            except OSError:
                self._latch(operation, step, ControlCause.TRANSPORT)
            except ValueError:
                self._latch(operation, step, ControlCause.OTHER)
            return True

    def resume_and_release(self, abort=False):
        with self.mutex:
            if self.released:
                return
            if self.invalid or (self.armed and not self.committed):
                abort = True
            if abort:
                self.invalid = True
            deadline = time.monotonic() + 3
            try:
                self.supervisor.send({'version': 2, 'epoch': 1,
                                      'op': 'abort' if abort else 'release'}, deadline)
                response = self.supervisor.receive(deadline)
                if response != {'version': 2, 'epoch': 1, 'status': 'released'}:
                    _fail()
                self.released = True
                return
            except (OSError, ControlFailure):
                self.invalid = True
            # Never race a live sibling for the rescue socket/ACK. Kill only the
            # sibling Popen we created, and confirm Wait before taking ownership.
            self.owner.stop_rescuer()
            identity = self.owner.identity
            identity.resume(time.monotonic() + 1)
            _rescue_exchange(self.rescue_fallback, 2, True, time.monotonic() + 2)
            self.released = True
            # Recovery is not successful capture evidence.
            _fail()


def owned_worker_environment(env, descriptors):
    # pgx consults libpq-style PG* defaults in addition to its explicit DSN.
    # Only this isolated owned fixture removes those ambient connection inputs.
    child = {key: value for key, value in env.items()
             if key not in ENV_FDS and not key.startswith('PG')}
    child.update(zip(ENV_FDS, map(str, descriptors)))
    return child


def _socket_pair():
    a, b = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
    return a.detach(), b.detach()


class OwnedCIWorker:
    """Direct Popen owner. Witness remains valid until worker Wait completes."""
    _final_state_lock = threading.Lock()
    @classmethod
    def spawn(cls, args, env, stdout, stderr, binary):
        self = cls()
        self.process = self.rescuer = self.identity = self.controller = None
        self.witness = None
        self.page_fd = None
        self.fds = set()
        try:
            nonce = bytes.fromhex(env['ADTR_E2E_EXPORT_WORKER_NONCE'])
            if len(nonce) != 16 or sys.platform != 'linux':
                _fail()
            self.page_fd = os.memfd_create('adtr-ci-witness', os.MFD_CLOEXEC)
            os.ftruncate(self.page_fd, PAGE_SIZE)  # Once, before child sees it.
            os.pwrite(self.page_fd, HEADER.pack(b'ADTRGATE', 1, PAGE_SIZE, 1, 0, 0, nonce), 0)
            self.witness = mmap.mmap(self.page_fd, PAGE_SIZE, access=mmap.ACCESS_READ)
            capture_read, capture_write = os.pipe2(os.O_NONBLOCK | os.O_CLOEXEC)
            self.fds.update((capture_read, capture_write))
            normal_parent, normal_worker = _socket_pair()
            self.fds.update((normal_parent, normal_worker))
            rescue_parent, rescue_worker = _socket_pair()
            self.fds.update((rescue_parent, rescue_worker))
            supervisor_parent, supervisor_child = _socket_pair()
            self.fds.update((supervisor_parent, supervisor_child))
            # Go os.NewFile registers inherited sockets with its netpoller only
            # when O_NONBLOCK is already present; SetWriteDeadline must work.
            os.set_blocking(normal_worker, False)
            os.set_blocking(rescue_worker, False)
            child_env = owned_worker_environment(env, (normal_worker, rescue_worker, self.page_fd, capture_read))
            self.process = subprocess.Popen(args, env=child_env, stdout=stdout, stderr=stderr,
                    close_fds=True, pass_fds=(normal_worker, rescue_worker, self.page_fd, capture_read))
            # Only these four descriptors cross worker exec. In particular the
            # worker never holds supervisor-parent, preserving parent-death EOF.
            for fd in (normal_worker, rescue_worker, capture_read):
                os.close(fd); self.fds.remove(fd)
            start, executable, _ = read_identity(self.process.pid)
            if executable != str(Path(binary).resolve()):
                _fail()
            self.identity = OwnedIdentity(self.process.pid, start, executable)
            self.rescuer = subprocess.Popen([sys.executable, str(Path(__file__).resolve()),
                    '--rescue', str(supervisor_child), str(rescue_parent), str(self.process.pid),
                    str(start), executable, str(capture_write), nonce.hex()], close_fds=True,
                    pass_fds=(supervisor_child, rescue_parent, capture_write), env={"PATH": os.defpath, "LC_ALL": "C.UTF-8"},
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            os.close(supervisor_child); self.fds.remove(supervisor_child)
            os.close(capture_write); self.fds.remove(capture_write)
            normal = PipeChannel(normal_parent, normal_parent)
            supervisor = PipeChannel(supervisor_parent, supervisor_parent, read_limit=1024, write_limit=256)
            fallback = PipeChannel(rescue_parent, rescue_parent, read_limit=1024, write_limit=256)
            self.controller = WorkerController(self, normal, supervisor, fallback, self.witness, nonce)
            self.controller.ready(time.monotonic() + 5)
            return self
        except BaseException:
            self.shutdown()
            raise

    def report_failure_diagnostic(self):
        """Post-failure observation only; never capture eligibility or sticky read."""
        report = {'event': 'ci_worker_admission_failure',
                  'reason': 'unavailable', 'phase': 'unavailable'}
        locked = False
        try:
            c = self.controller
            if (c is None or c.armed or c.released or c.target_sent or self.process.poll() is not None
                    or not self.identity.threads_running()):
                _fail()
            locked = c.mutex.acquire(timeout=.02)
            if not locked or c.armed or c.released or c.target_sent or not self.identity.threads_running():
                _fail()
            deadline = time.monotonic() + .2
            c.seq += 1
            c.normal.send({'version': 2, 'epoch': 1, 'seq': c.seq, 'op': 'diagnostic'}, deadline)
            report = diagnostic_projection(c.normal.receive(deadline), c.seq)
        except Exception:
            # Missing/invalid evidence is explicitly unavailable; never serialize
            # exceptions, registry records, identifiers, descriptor values or env.
            pass
        finally:
            if locked:
                c.mutex.release()
        try:
            print(json.dumps(report, separators=(',', ':')), file=sys.stderr, flush=True)
        except Exception:
            pass  # Optional evidence must not interrupt owned cleanup.
        return report

    def recovery_cleanup_after_wait(self, logs):
        if self.process is None or type(self.process.returncode) is not int:
            return None
        return recovery_cleanup_projection(logs)

    def final_journal_after_wait(self, logs):
        if self.process is None or type(self.process.returncode) is not int:
            return None
        return final_journal_projection(logs)

    def report_first_invalidation_after_wait(self, logs):
        """Optional final evidence only; never changes qualification or cleanup."""
        with self._final_state_lock:
            if (self.process is None or type(self.process.returncode) is not int
                    or getattr(self, '_first_invalidation_reported', False)):
                return
            self._first_invalidation_reported = True
        report = first_invalidation_projection(logs)
        try:
            print(json.dumps(report, separators=(',', ':')), file=sys.stderr, flush=True)
        except Exception:
            pass
        if report['reason'] == 'connect_failed':
            detail = first_connect_projection(logs)
            try:
                print(json.dumps(detail, separators=(',', ':')), file=sys.stderr, flush=True)
            except Exception:
                pass
        if report['reason'] == 'unclean_close':
            detail = first_unclean_projection(logs)
            try:
                print(json.dumps(detail, separators=(',', ':')), file=sys.stderr, flush=True)
            except Exception:
                pass
            operation = unclean_operation_projection(logs)
            try:
                print(json.dumps(operation, separators=(',', ':')), file=sys.stderr, flush=True)
            except Exception:
                pass

    def stop_rescuer(self):
        if self.rescuer is None:
            return
        if self.rescuer.poll() is None:
            self.rescuer.kill()
        self.rescuer.wait(timeout=3)

    def close_after_wait(self):
        if self.process is not None and self.process.returncode is None:
            _fail()
        report = None
        with self._final_state_lock:
            if (self.process is not None and type(self.process.returncode) is int
                    and not getattr(self, '_final_state_reported', False)):
                self._final_state_reported = True
                report = {'event': 'owned_ci_final_state', 'exitZero': self.process.returncode == 0,
                          'sticky': 'unavailable', 'controllerInvalid': 'unknown',
                          'released': 'unknown', 'cleanup': 'rescuer'}
                if type(self.controller) is WorkerController:
                    for field, attribute in (('controllerInvalid', 'invalid'), ('released', 'released')):
                        value = getattr(self.controller, attribute, None)
                        if type(value) is bool:
                            report[field] = value
        try:
            self.stop_rescuer()
            final_invalid = False
            if report is not None:
                report['cleanup'] = 'other'
            if self.controller is not None and self.controller.armed:
                expected = HEADER.pack(b'ADTRGATE', 1, PAGE_SIZE, 1, 0, 1, self.controller.nonce)
                page = self.witness[:]
                final_invalid = (self.controller.invalid or len(page) != PAGE_SIZE
                    or page[:HEADER.size] != expected or any(page[HEADER.size:]))
                if report is not None:
                    # Classification cannot change the existing acceptance predicate.
                    report['sticky'] = 'other'
                    if (len(page) == PAGE_SIZE and page[:24] == expected[:24]
                            and page[28:HEADER.size] == expected[28:HEADER.size]
                            and not any(page[HEADER.size:])):
                        bit = int.from_bytes(page[24:28], 'little')
                        if bit in (0, 1):
                            report['sticky'] = 'set' if bit else 'clear'
            if report is not None:
                report['cleanup'] = 'identity'
            if self.identity is not None:
                self.identity.close(); self.identity = None
            if report is not None:
                report['cleanup'] = 'fd'
            for fd in list(self.fds):
                os.close(fd); self.fds.remove(fd)
            if report is not None:
                report['cleanup'] = 'witness'
            if self.witness is not None:
                self.witness.close(); self.witness = None
            if report is not None:
                report['cleanup'] = 'page_fd'
            if self.page_fd is not None:
                os.close(self.page_fd); self.page_fd = None
            if report is not None:
                report['cleanup'] = 'completed'
            if final_invalid:
                _fail()
        finally:
            if report is not None:
                try:
                    print(json.dumps(report, separators=(',', ':')), file=sys.stderr, flush=True)
                except Exception:
                    pass  # Diagnostic output must never replace cleanup/qualification errors.

    def shutdown(self):
        if self.process is not None and self.process.poll() is None:
            try:
                if self.controller is not None:
                    self.controller.resume_and_release(abort=True)
                elif self.identity is not None:
                    self.identity.resume(time.monotonic() + 1)
            except (OSError, ControlFailure, subprocess.TimeoutExpired):
                pass
            self.process.terminate()
            try:
                self.process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=10)
        if self.process is not None:
            self.process.wait(timeout=1)
        self.close_after_wait()


def _rescue_main(supervisor_fd, rescue_fd, pid, starttime, executable, capture_fd=None, nonce=None):
    supervisor = PipeChannel(supervisor_fd, supervisor_fd, read_limit=1024, write_limit=256)
    rescue = PipeChannel(rescue_fd, rescue_fd, read_limit=1024, write_limit=256)
    identity = OwnedIdentity(pid, starttime, executable)
    armed = released = committed = recovering = False
    capture_deadline = None
    watchdog = None
    seq = 0

    def recover(abort):
        nonlocal released, seq, recovering
        recovering = True
        identity.resume(time.monotonic() + 1)
        if not released:
            seq += 1
            _rescue_exchange(rescue, seq, abort, time.monotonic() + 2)
            released = True

    def terminate_orphan():
        # A sibling cannot waitpid the worker. Signal its pidfd, then observe
        # pidfd readiness (exit); the ordinary parent owns and performs Wait.
        try:
            identity.resume(time.monotonic() + 1)
            identity.send(signal.SIGTERM)
            if not select.select([identity.pidfd], [], [], 3)[0]:
                identity.send(signal.SIGKILL)
                if not select.select([identity.pidfd], [], [], 3)[0]:
                    _fail()
        except (ProcessLookupError, ControlFailure):
            # Identity mismatch must never signal a different process.
            return

    try:
        supervisor.send({'version': 2, 'epoch': 1, 'status': 'ready'}, time.monotonic() + 3)
        while True:
            deadline = watchdog if watchdog is not None else time.monotonic() + 3600
            frame = supervisor.receive(deadline)
            if (type(frame.get('version')) is not int or type(frame.get('epoch')) is not int
                    or frame.get('version') != 2 or frame.get('epoch') != 1):
                _fail()
            op = frame.get('op')
            if op == 'arm':
                if (set(frame) != {'version', 'epoch', 'op', 'deadline'} or armed or released
                        or type(frame['deadline']) not in (float, int)
                        or not time.monotonic() < frame['deadline'] <= time.monotonic() + 1.5):
                    _fail()
                identity.verify()
                armed = True
                capture_deadline = frame['deadline']
                watchdog = frame['deadline'] - 1.5 + 9
                supervisor.send({'version': 2, 'epoch': 1, 'status': 'armed'}, frame['deadline'])
            elif op == 'commit_capture':
                if (set(frame) != {'version', 'epoch', 'op', 'ticketGeneration', 'registrationGeneration'}
                        or not armed or released or recovering or committed or capture_fd is None
                        or type(nonce) is not bytes or len(nonce) != 16
                        or not all(_int(frame[key], 1) and frame[key] <= 2 ** 64 - 1
                                   for key in ('ticketGeneration', 'registrationGeneration'))
                        or time.monotonic() >= capture_deadline):
                    _fail()
                identity.verify()
                if not identity.threads_stopped() or time.monotonic() >= capture_deadline:
                    _fail()
                # One exclusive writer, one <= PIPE_BUF nonblocking write, no retry.
                committed = True
                payload = CAPTURE_END.pack(b'ADTREND1', nonce, 1,
                                          frame['ticketGeneration'], frame['registrationGeneration'])
                if os.write(capture_fd, payload) != len(payload):
                    _fail()
                if time.monotonic() >= capture_deadline:
                    _fail()
                supervisor.send({'version': 2, 'epoch': 1, 'status': 'committed',
                    'ticketGeneration': frame['ticketGeneration'],
                    'registrationGeneration': frame['registrationGeneration']}, capture_deadline)
            elif op in ('release', 'abort') and set(frame) == {'version', 'epoch', 'op'}:
                if op == 'release' and (not armed or not committed):
                    _fail()
                recover(op == 'abort')
                watchdog = None
                supervisor.send({'version': 2, 'epoch': 1, 'status': 'released'}, time.monotonic() + 2)
            else:
                _fail()
    except (OSError, ControlFailure):
        # Parent death, half-frame, EOF, pipe failure or watchdog expiry: never
        # leave even the pre-ARM queued worker stopped and unowned.
        try:
            recover(True)
        except (OSError, ControlFailure):
            pass
        terminate_orphan()
        return 1
    finally:
        if capture_fd is not None:
            os.close(capture_fd)
        identity.close()
        supervisor.close()
        rescue.close()


if __name__ == '__main__':
    if len(sys.argv) != 9 or sys.argv[1] != '--rescue':
        raise SystemExit(2)
    try:
        raise SystemExit(_rescue_main(int(sys.argv[2]), int(sys.argv[3]),
                                     int(sys.argv[4]), int(sys.argv[5]), sys.argv[6],
                                     int(sys.argv[7]), bytes.fromhex(sys.argv[8])))
    except (OSError, ControlFailure, ValueError):
        raise SystemExit(1)
