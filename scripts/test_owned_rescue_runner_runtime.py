"""Explicit isolated Linux CI tests. Never imported by offline *_contract tests.

Synthetic Python workers prove runner ownership/recovery only, not Go atomics,
PostgreSQL closure or three-kind running-cancel product acceptance.
"""
import json
import mmap
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import threading
import time
import unittest

import owned_rescue_runner as r


def fixture_worker():
    page = mmap.mmap(int(os.environ['ADTR_CI_DB_WITNESS_FD']), r.PAGE_SIZE)
    # Fixture handshake only. This is NOT a substitute for Go atomic CAS tests.
    page[28:32] = (1).to_bytes(4, 'little')
    normal = r.PipeChannel(int(os.environ['ADTR_CI_DB_NORMAL_FD']), int(os.environ['ADTR_CI_DB_NORMAL_FD']))
    rescue = r.PipeChannel(int(os.environ['ADTR_CI_DB_RESCUE_FD']), int(os.environ['ADTR_CI_DB_RESCUE_FD']))
    state = {'status': 'open', 'target': 0}

    def frame(seq, kind='reply'):
        return {'version': 2, 'epoch': 1, 'seq': seq, 'kind': kind,
                'status': state['status'], 'pending': 0, 'open': 0,
                'revision': 1, 'target': state['target'], 'records': []}

    def normal_loop():
        try:
            normal.send(frame(0, 'ready'), time.monotonic() + 3)
            while True:
                request = normal.receive(time.monotonic() + 60)
                if request['op'] == 'pin':
                    state.update(status='sealed', target=request['target'])
                normal.send(frame(request['seq']), time.monotonic() + 3)
        except (OSError, r.ControlFailure):
            return

    threading.Thread(target=normal_loop, daemon=True).start()
    previous = 0
    while True:
        request = rescue.receive(time.monotonic() + 60)
        if request['seq'] <= previous:
            raise RuntimeError('fixture rescue sequence')
        previous = request['seq']
        state['status'] = 'released'
        rescue.send({'version': 2, 'epoch': 1, 'seq': previous,
                     'kind': 'reply', 'status': 'released'}, time.monotonic() + 3)


def spawn():
    return r.OwnedCIWorker.spawn([sys.executable, str(Path(__file__).resolve()), '--fixture-worker'],
        env={'PATH': os.defpath, 'ADTR_E2E_EXPORT_WORKER_NONCE': '1' * 32},
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, binary=Path(sys.executable).resolve())


def parent_fixture():
    owner = spawn()
    start, exe, _ = r.read_identity(owner.process.pid)
    print(json.dumps({'pid': owner.process.pid, 'starttime': start, 'exe': exe,
                      'rescuer': owner.rescuer.pid}), flush=True)
    command = sys.stdin.readline().strip()
    if command == 'armed':
        owner.controller.arm(time.monotonic() + 1.5)
        owner.controller.pin(1, time.monotonic() + 1)
    owner.process.send_signal(signal.SIGSTOP)
    print('stopped', flush=True)
    # A test owns this parent process and kills it. Rescue sibling must survive.
    sys.stdin.read()
    owner.shutdown()


class RuntimeTests(unittest.TestCase):
    def test_ready_pin_stop_resume_and_wait_before_unmap(self):
        owner = spawn()
        try:
            c = owner.controller
            c.arm(time.monotonic() + 1.5)
            self.assertEqual(c.pin(1, time.monotonic() + 1)['status'], 'sealed')
            owner.process.send_signal(signal.SIGSTOP)
            deadline = time.monotonic() + 1
            states = []
            while time.monotonic() < deadline:
                states = [(p / 'stat').read_text().rsplit(')', 1)[1].split()[0]
                          for p in (Path('/proc') / str(owner.process.pid) / 'task').iterdir()]
                if states and all(state == 'T' for state in states):
                    break
                time.sleep(.002)
            self.assertTrue(states and all(state == 'T' for state in states))
            self.assertTrue(c.after_stop(deadline))
            c.resume_and_release()
            self.assertNotIn(owner.identity.verify(), ('T', 't'))
            with self.assertRaises(r.ControlFailure):
                owner.close_after_wait()
        finally:
            owner.shutdown()
        self.assertIsNotNone(owner.process.returncode)
        self.assertIsNone(owner.witness)
        self.assertIsNotNone(owner.rescuer.returncode)

    def test_rescuer_death_wait_before_fallback(self):
        owner = spawn()
        try:
            owner.controller.arm(time.monotonic() + 1.5)
            owner.controller.pin(1, time.monotonic() + 1)
            owner.process.send_signal(signal.SIGSTOP)
            owner.rescuer.kill()
            # The controller must Wait itself before it can own rescue ACK.
            with self.assertRaises(r.ControlFailure):
                owner.controller.resume_and_release()
            self.assertIsNotNone(owner.rescuer.returncode)
            self.assertNotIn(owner.identity.verify(), ('T', 't'))
        finally:
            try:
                owner.shutdown()
            except r.ControlFailure:
                pass  # Expected final invalid latch after recovered failure.
        self.assertIsNone(owner.witness)
        self.assertIsNotNone(owner.process.returncode)

    def test_parentdeath_prearm_and_armed_exits_owned_tree(self):
        for command in ('queued', 'armed'):
            with self.subTest(command=command):
                parent = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '--fixture-parent'],
                    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                    text=True, env={'PATH': os.defpath})
                workerfd = siblingfd = None
                try:
                    self.assertTrue(select.select([parent.stdout], [], [], 8)[0])
                    ready = json.loads(parent.stdout.readline())
                    workerfd = os.pidfd_open(ready['pid'])
                    siblingfd = os.pidfd_open(ready['rescuer'])
                    parent.stdin.write(command + '\n'); parent.stdin.flush()
                    self.assertTrue(select.select([parent.stdout], [], [], 3)[0])
                    self.assertEqual(parent.stdout.readline().strip(), 'stopped')
                    parent.kill(); parent.wait(timeout=3)
                    # pidfd proves exit only. These orphans are reaped by init;
                    # this test cannot waitpid children of the deceased parent.
                    self.assertTrue(select.select([workerfd], [], [], 10)[0], 'worker did not exit')
                    self.assertTrue(select.select([siblingfd], [], [], 3)[0], 'rescue did not exit')
                finally:
                    if parent.poll() is None:
                        parent.kill(); parent.wait(timeout=3)
                    # pidfds are obtained from this owned parent's explicit report.
                    # Cleanup never addresses an arbitrary process ID.
                    for fd in (workerfd, siblingfd):
                        if fd is not None:
                            if not select.select([fd], [], [], 0)[0]:
                                signal.pidfd_send_signal(fd, signal.SIGKILL)
                            os.close(fd)
                    parent.stdin.close(); parent.stdout.close()

    def test_supervisor_eof_and_half_frame_resume_and_terminate(self):
        for payload in (b'', b'{"version":2'):
            with self.subTest(payload=payload):
                owner = spawn()
                try:
                    owner.process.send_signal(signal.SIGSTOP)
                    if payload:
                        os.write(owner.controller.supervisor.write_fd, payload)
                    fd = owner.controller.supervisor.write_fd
                    owner.controller.supervisor.close()
                    owner.fds.remove(fd)
                    owner.process.wait(timeout=10)
                    owner.rescuer.wait(timeout=3)
                finally:
                    owner.shutdown()
                self.assertIsNone(owner.witness)

    def test_normal_socket_failure_keeps_independent_rescue(self):
        owner = spawn()
        try:
            c = owner.controller
            c.arm(time.monotonic() + 1.5)
            c.pin(1, time.monotonic() + 1)
            owner.process.send_signal(signal.SIGSTOP)
            fd = c.normal.read_fd
            c.normal.close(); owner.fds.remove(fd)
            with self.assertRaises(r.ControlFailure):
                c.snapshot(time.monotonic() + .1)
            c.resume_and_release(abort=True)
            self.assertNotIn(owner.identity.verify(), ('T', 't'))
        finally:
            try:
                owner.shutdown()
            except r.ControlFailure:
                pass
        self.assertIsNone(owner.witness)

    def test_real_full_socket_obeys_absolute_write_deadline(self):
        left, right = r._socket_pair()
        channel = r.PipeChannel(left, left)
        try:
            while True:
                try:
                    os.write(left, bytes(4096))
                except BlockingIOError:
                    break
            deadline = time.monotonic() + .05
            with self.assertRaises(r.ControlFailure):
                channel.send({'version': 2, 'epoch': 1, 'seq': 1, 'op': 'abort'}, deadline)
            self.assertLess(time.monotonic() - deadline, .5)
        finally:
            channel.close(); os.close(right)

    def test_wrong_identity_cannot_signal_owned_worker(self):
        owner = spawn()
        try:
            start, exe, _ = r.read_identity(owner.process.pid)
            with self.assertRaises(r.ControlFailure):
                r.OwnedIdentity(owner.process.pid, start + 1, exe)
            self.assertIsNone(owner.process.poll())
            self.assertNotIn(owner.identity.verify(), ('T', 't'))
        finally:
            owner.shutdown()


if __name__ == '__main__':
    if sys.argv[1:] == ['--fixture-worker']:
        fixture_worker()
    elif sys.argv[1:] == ['--fixture-parent']:
        parent_fixture()
    elif os.environ.get('ADTR_RUN_OWNED_RESCUE_TESTS') == '1' and sys.platform == 'linux':
        unittest.main()
    else:
        raise SystemExit('explicit isolated Linux rescue test opt-in required')
