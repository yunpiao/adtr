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
ENV_FDS = ('ADTR_CI_DB_NORMAL_FD', 'ADTR_CI_DB_RESCUE_FD', 'ADTR_CI_DB_WITNESS_FD')


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
            child_env = owned_worker_environment(env, (normal_worker, rescue_worker, self.page_fd))
            self.process = subprocess.Popen(args, env=child_env, stdout=stdout, stderr=stderr,
                    close_fds=True, pass_fds=(normal_worker, rescue_worker, self.page_fd))
            # Only these three descriptors cross worker exec. In particular the
            # worker never holds supervisor-parent, preserving parent-death EOF.
            for fd in (normal_worker, rescue_worker):
                os.close(fd); self.fds.remove(fd)
            start, executable, _ = read_identity(self.process.pid)
            if executable != str(Path(binary).resolve()):
                _fail()
            self.identity = OwnedIdentity(self.process.pid, start, executable)
            self.rescuer = subprocess.Popen([sys.executable, str(Path(__file__).resolve()),
                    '--rescue', str(supervisor_child), str(rescue_parent), str(self.process.pid),
                    str(start), executable], close_fds=True,
                    pass_fds=(supervisor_child, rescue_parent), env={"PATH": os.defpath, "LC_ALL": "C.UTF-8"},
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            os.close(supervisor_child); self.fds.remove(supervisor_child)
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

    def stop_rescuer(self):
        if self.rescuer is None:
            return
        if self.rescuer.poll() is None:
            self.rescuer.kill()
        self.rescuer.wait(timeout=3)

    def close_after_wait(self):
        if self.process is not None and self.process.returncode is None:
            _fail()
        self.stop_rescuer()
        final_invalid = False
        if self.controller is not None and self.controller.armed:
            expected = HEADER.pack(b'ADTRGATE', 1, PAGE_SIZE, 1, 0, 1, self.controller.nonce)
            page = self.witness[:]
            final_invalid = (self.controller.invalid or len(page) != PAGE_SIZE
                or page[:HEADER.size] != expected or any(page[HEADER.size:]))
        if self.identity is not None:
            self.identity.close(); self.identity = None
        for fd in list(self.fds):
            os.close(fd); self.fds.remove(fd)
        if self.witness is not None:
            self.witness.close(); self.witness = None
        if self.page_fd is not None:
            os.close(self.page_fd); self.page_fd = None
        if final_invalid:
            _fail()

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


def _rescue_main(supervisor_fd, rescue_fd, pid, starttime, executable):
    supervisor = PipeChannel(supervisor_fd, supervisor_fd, read_limit=1024, write_limit=256)
    rescue = PipeChannel(rescue_fd, rescue_fd, read_limit=1024, write_limit=256)
    identity = OwnedIdentity(pid, starttime, executable)
    armed = released = False
    watchdog = None
    seq = 0

    def recover(abort):
        nonlocal released, seq
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
                watchdog = frame['deadline'] - 1.5 + 9
                supervisor.send({'version': 2, 'epoch': 1, 'status': 'armed'}, frame['deadline'])
            elif op in ('release', 'abort') and set(frame) == {'version', 'epoch', 'op'}:
                if op == 'release' and not armed:
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
        identity.close()
        supervisor.close()
        rescue.close()


if __name__ == '__main__':
    if len(sys.argv) != 7 or sys.argv[1] != '--rescue':
        raise SystemExit(2)
    try:
        raise SystemExit(_rescue_main(int(sys.argv[2]), int(sys.argv[3]),
                                     int(sys.argv[4]), int(sys.argv[5]), sys.argv[6]))
    except (OSError, ControlFailure, ValueError):
        raise SystemExit(1)
