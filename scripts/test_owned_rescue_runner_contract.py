"""Offline contracts only: mock process, socket, mmap and signal operations."""
import ast
import json
from pathlib import Path
import signal
import unittest
from unittest.mock import Mock, patch

import owned_rescue_runner as r


# Exact reversible runner delta preserves all earlier byte-for-byte contracts.
BASE_RUNNER_SHA256 = '0e8134e1a94b4fdbe699f67302fd5b8960a122fdb0c61698d89ed9ac4d8944fb'
RUNNER_DELTAS = [('from user_asset_export_v3_terminal_ci import SUITES as EXPORT_TERMINAL_SUITES, configure as '
  'configure_terminal_export, capture as capture_terminal_export, validate as '
  'validate_terminal_export\n'
  'from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n'
  'from asset_export_v3_running_cancel_guard import RunningExportCancelGuard\n'
  'from user_asset_export_v3_running_cancel_ci import SUITE as RUNNING_CANCEL_SUITE, configure as '
  'configure_running_cancel, capture as capture_running_cancel, validate as '
  'validate_running_cancel\n'
  'from group_asset_export_v3_running_cancel_ci import SUITE as GROUP_RUNNING_CANCEL_SUITE, '
  'configure as configure_group_running_cancel, capture as capture_group_running_cancel, validate '
  'as validate_group_running_cancel\n'
  'from computer_asset_export_v3_running_cancel_ci import SUITE as COMPUTER_RUNNING_CANCEL_SUITE, '
  'configure as configure_computer_running_cancel, capture as capture_computer_running_cancel, '
  'validate as validate_computer_running_cancel\n',
  'from user_asset_export_v3_terminal_ci import SUITES as EXPORT_TERMINAL_SUITES, configure as '
  'configure_terminal_export, capture as capture_terminal_export, validate as '
  'validate_terminal_export\n'
  'from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n'
  'from asset_export_v3_running_cancel_guard import RunningExportCancelGuard\n'
  'from owned_rescue_runner import OwnedCIWorker, ENV_FDS\n'
  'from user_asset_export_v3_running_cancel_ci import SUITE as RUNNING_CANCEL_SUITE, configure as '
  'configure_running_cancel, capture as capture_running_cancel, validate as '
  'validate_running_cancel\n'
  'from group_asset_export_v3_running_cancel_ci import SUITE as GROUP_RUNNING_CANCEL_SUITE, '
  'configure as configure_group_running_cancel, capture as capture_group_running_cancel, validate '
  'as validate_group_running_cancel\n'
  'from computer_asset_export_v3_running_cancel_ci import SUITE as COMPUTER_RUNNING_CANCEL_SUITE, '
  'configure as configure_computer_running_cancel, capture as capture_computer_running_cancel, '
  'validate as validate_computer_running_cancel\n'),
 ('    parser.add_argument("--suite", choices=["group-asset-export-v3-running-cancel", '
  '"computer-asset-export-v3-running-cancel", "user-asset-export-v3-running-cancel", '
  '"computer-asset-export-v3", "computer-asset-export-v3-failure", '
  '"computer-asset-export-v3-cancel", "group-asset-export-v3", "group-asset-export-v3-failure", '
  '"group-asset-export-v3-cancel", "user-asset-export-v3-failure", "user-asset-export-v3-cancel", '
  '"user-asset-export-v3", "auth", "access", "resource", "tasks", "messages", "messages-bulk", '
  '"audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", '
  '"account-references", "operational-logs", "session-invalidation", "account-reference-barrier", '
  '"directory", "directory-controls", "directory-readers", "directory-v2", '
  '"directory-v2-controls", "directory-v2-readers", "directory-v3-backend", '
  '"directory-v4-backend", "group-members-v4", "group-members-v4-empty", '
  '"group-members-v4-pagination", "group-members-v4-refresh", "user-assets-v3", '
  '"computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", '
  '"group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", '
  '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'
  '    parser.add_argument("--expired", action="store_true")\n'
  '    args = parser.parse_args()\n'
  '    # Check display support before creating a disposable database or fixtures.\n'
  '    command = browser_command(args.suite)\n'
  '    name = "adtr-auth-e2e-" + uuid.uuid4().hex[:12]\n',
  '    parser.add_argument("--suite", choices=["group-asset-export-v3-running-cancel", '
  '"computer-asset-export-v3-running-cancel", "user-asset-export-v3-running-cancel", '
  '"computer-asset-export-v3", "computer-asset-export-v3-failure", '
  '"computer-asset-export-v3-cancel", "group-asset-export-v3", "group-asset-export-v3-failure", '
  '"group-asset-export-v3-cancel", "user-asset-export-v3-failure", "user-asset-export-v3-cancel", '
  '"user-asset-export-v3", "auth", "access", "resource", "tasks", "messages", "messages-bulk", '
  '"audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", '
  '"account-references", "operational-logs", "session-invalidation", "account-reference-barrier", '
  '"directory", "directory-controls", "directory-readers", "directory-v2", '
  '"directory-v2-controls", "directory-v2-readers", "directory-v3-backend", '
  '"directory-v4-backend", "group-members-v4", "group-members-v4-empty", '
  '"group-members-v4-pagination", "group-members-v4-refresh", "user-assets-v3", '
  '"computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", '
  '"group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", '
  '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'
  '    parser.add_argument("--expired", action="store_true")\n'
  '    args = parser.parse_args()\n'
  '    # Only the three explicit running-cancel suites install the CI controller.\n'
  '    # The default make build remains untagged. Build before source provenance.\n'
  '    if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:\n'
  '        subprocess.run(["make", "build-ci-running-cancel"], check=True, timeout=180)\n'
  '    # Check display support before creating a disposable database or fixtures.\n'
  '    command = browser_command(args.suite)\n'
  '    name = "adtr-auth-e2e-" + uuid.uuid4().hex[:12]\n'),
 ('    owner = uuid.uuid4().hex\n'
  '    server = None\n'
  '    worker = None\n'
  '    export_worker_guard = None\n'
  '    running_cancel_guard = None\n'
  '    ldap_fixture = None\n',
  '    owner = uuid.uuid4().hex\n'
  '    server = None\n'
  '    worker = None\n'
  '    owned_ci_worker = None\n'
  '    export_worker_guard = None\n'
  '    running_cancel_guard = None\n'
  '    ldap_fixture = None\n'),
 ('                for key in ["ADTR_LDAP_CA_FILE", "ADTR_LDAP_EGRESS_POLICY_FILE"]:\n'
  '                    env.pop(key, None)\n'
  '        # Credentials are only needed by explicit bootstrap; never pass them to the server.\n'
  '        runtime_env = {k: v for k, v in env.items() if not k.startswith("ADTR_BOOTSTRAP_") and '
  'not k.startswith("ADTR_E2E_")}\n'
  '        diagnostics.phase("api-start")\n'
  '        server = subprocess.Popen(["./bin/adtr", "-mode", "api"], env=runtime_env, stdout=logs, '
  'stderr=logs)\n'
  '        for _ in range(100):\n',
  '                for key in ["ADTR_LDAP_CA_FILE", "ADTR_LDAP_EGRESS_POLICY_FILE"]:\n'
  '                    env.pop(key, None)\n'
  '        # Credentials are only needed by explicit bootstrap; never pass them to the server.\n'
  '        runtime_env = {k: v for k, v in env.items() if not k.startswith("ADTR_BOOTSTRAP_") and '
  'not k.startswith("ADTR_E2E_") and k not in ENV_FDS}\n'
  '        diagnostics.phase("api-start")\n'
  '        server = subprocess.Popen(["./bin/adtr", "-mode", "api"], env=runtime_env, stdout=logs, '
  'stderr=logs)\n'
  '        for _ in range(100):\n'),
 ('            for key in ["ADTR_AUTH_KEY", "ADTR_ORIGIN", "ADTR_WEB_DIR"]:\n'
  '                worker_env.pop(key, None)\n'
  '            diagnostics.phase("worker-start")\n'
  '            worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, '
  'stdout=logs, stderr=logs)\n'
  '            if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", '
  '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | '
  'KIND_RUNNING_CANCEL_SUITES:\n'
  '                export_worker_guard = OwnedExportWorkerGuard(worker, export_worker_nonce, '
  'Path("bin/adtr").resolve())\n'
  '                export_worker_guard.start()\n',
  '            for key in ["ADTR_AUTH_KEY", "ADTR_ORIGIN", "ADTR_WEB_DIR"]:\n'
  '                worker_env.pop(key, None)\n'
  '            diagnostics.phase("worker-start")\n'
  '            if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:\n'
  '                owned_ci_worker = OwnedCIWorker.spawn(["./bin/adtr", "-mode", "worker"],\n'
  '                    env=worker_env, stdout=logs, stderr=logs, '
  'binary=Path("bin/adtr").resolve())\n'
  '                worker = owned_ci_worker.process\n'
  '            else:\n'
  '                worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, '
  'stdout=logs, stderr=logs)\n'
  '            if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", '
  '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | '
  'KIND_RUNNING_CANCEL_SUITES:\n'
  '                export_worker_guard = OwnedExportWorkerGuard(worker, export_worker_nonce, '
  'Path("bin/adtr").resolve())\n'
  '                export_worker_guard.start()\n'),
 ('                           '
  'ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")\n'
  '                if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:\n'
  '                    asset_kind = {GROUP_RUNNING_CANCEL_SUITE: "group", '
  'COMPUTER_RUNNING_CANCEL_SUITE: "computer"}.get(args.suite, "user")\n'
  '                    running_cancel_guard = RunningExportCancelGuard(worker, '
  'export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, '
  'env["ADTR_DATABASE_URL"], asset_kind=asset_kind)\n'
  '                    running_cancel_guard.start()\n'
  '                    env.update(ADTR_E2E_RUNNING_CANCEL_GUARD_URL=running_cancel_guard.url,\n'
  '                               '
  'ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN=running_cancel_guard.token)\n',
  '                           '
  'ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")\n'
  '                if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:\n'
  '                    asset_kind = {GROUP_RUNNING_CANCEL_SUITE: "group", '
  'COMPUTER_RUNNING_CANCEL_SUITE: "computer"}.get(args.suite, "user")\n'
  '                    running_cancel_guard = RunningExportCancelGuard(worker, '
  'export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, '
  'env["ADTR_DATABASE_URL"], asset_kind=asset_kind, controller=owned_ci_worker.controller)\n'
  '                    export_worker_guard.resume_delegate = '
  'running_cancel_guard._resume_capture_owned\n'
  '                    running_cancel_guard.start()\n'
  '                    env.update(ADTR_E2E_RUNNING_CANCEL_GUARD_URL=running_cancel_guard.url,\n'
  '                               '
  'ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN=running_cancel_guard.token)\n'),
 ('        diagnostics.phase("browser-pass")\n'
  '        chain = "real browser -> API -> worker/PostgreSQL" if worker is not None else "real '
  'browser -> API -> PostgreSQL"\n'
  '    finally:\n'
  '        try:\n'
  '            diagnostics.snapshot(server, worker)\n'
  '        except Exception:\n',
  '        diagnostics.phase("browser-pass")\n'
  '        chain = "real browser -> API -> worker/PostgreSQL" if worker is not None else "real '
  'browser -> API -> PostgreSQL"\n'
  '    finally:\n'
  '        if sys.exc_info()[0] is not None and owned_ci_worker is not None:\n'
  '            owned_ci_worker.report_failure_diagnostic()\n'
  '        try:\n'
  '            diagnostics.snapshot(server, worker)\n'
  '        except Exception:\n'),
 ('                    account_barrier.close()\n'
  '                except Exception as error:\n'
  '                    shutdown_error = error\n'
  '            for label, process in [("worker", worker), ("API", server)]:\n'
  '                if process is None:\n'
  '                    continue\n',
  '                    account_barrier.close()\n'
  '                except Exception as error:\n'
  '                    shutdown_error = error\n'
  '            if owned_ci_worker is not None and worker.poll() is None:\n'
  '                try:\n'
  '                    owned_ci_worker.controller.resume_and_release(abort=True)\n'
  '                except (OSError, RuntimeError):\n'
  '                    shutdown_error = RuntimeError("owned CI rescue cleanup failed")\n'
  '            for label, process in [("worker", worker), ("API", server)]:\n'
  '                if process is None:\n'
  '                    continue\n'),
 ('                    process.kill()\n'
  '                    process_record["after_cleanup"] = process.wait(timeout=10)\n'
  '                    shutdown_error = RuntimeError(f"{label} did not stop gracefully")\n'
  '            if ldap_fixture is not None:\n'
  '                try:\n'
  '                    ldap_fixture.close()\n',
  '                    process.kill()\n'
  '                    process_record["after_cleanup"] = process.wait(timeout=10)\n'
  '                    shutdown_error = RuntimeError(f"{label} did not stop gracefully")\n'
  '            if owned_ci_worker is not None:\n'
  '                try:\n'
  '                    owned_ci_worker.close_after_wait()\n'
  '                except (OSError, RuntimeError, subprocess.TimeoutExpired):\n'
  '                    shutdown_error = RuntimeError("owned CI final witness or cleanup failed")\n'
  '                owned_ci_worker.report_first_invalidation_after_wait(logs)\n'
  '            if running_cancel_guard is not None and worker is not None and worker.returncode is '
  'not None:\n'
  '                try:\n'
  '                    '
  'running_cancel_guard.cleanup_activity_fixture_after_wait(worker.returncode)\n'
  '                except Exception:\n'
  '                    shutdown_error = RuntimeError("owned CI activity fixture cleanup failed")\n'
  '            if running_cancel_guard is not None and shutdown_error is None:\n'
  '                try:\n'
  '                    running_cancel_guard.verify_recovery_cleanup_after_wait(worker.returncode, '
  'owned_ci_worker.recovery_cleanup_after_wait(logs))\n'
  '                except Exception:\n'
  '                    shutdown_error = RuntimeError("owned CI recovery cleanup proof failed")\n'
  '            if running_cancel_guard is not None and shutdown_error is None:\n'
  '                try:\n'
  "                    # The worker's successful Wait includes journal.Close/drain.\n"
  '                    # Query its final report while the isolated database is alive.\n'
  '                    running_cancel_guard.verify_journal_after_wait(worker.returncode, '
  'owned_ci_worker.final_journal_after_wait(logs))\n'
  '                except Exception:\n'
  '                    shutdown_error = RuntimeError("owned CI final journal proof failed")\n'
  '            if ldap_fixture is not None:\n'
  '                try:\n'
  '                    ldap_fixture.close()\n')]


def restore_owned_runner(text):
    for old, new in reversed(RUNNER_DELTAS):
        if text.count(new) != 1:
            raise ValueError('owned rescue runner delta absent or altered')
        text = text.replace(new, old)
    return text


def frame(**changes):
    return dict(version=2, epoch=1, seq=1, kind='reply', status='open', pending=0,
                open=1, revision=2, target=0, records=[], **changes)


def controller():
    nonce = bytes(16)
    witness = r.HEADER.pack(b'ADTRGATE', 1, r.PAGE_SIZE, 1, 0, 1, nonce)
    witness += bytes(r.PAGE_SIZE - len(witness))
    c = r.WorkerController(Mock(), Mock(), Mock(), Mock(), witness, nonce)
    c.armed = True
    return c


class Contracts(unittest.TestCase):
    def channel(self):
        with patch.object(r.os, 'set_blocking'):
            return r.PipeChannel(51, 51)

    def test_selection_configure_split_ack_keeps_original_deadline(self):
        c = controller(); c.armed = False
        c.normal.receive.side_effect = [frame(), dict(frame(), seq=2)]
        c.prepare_selection('user_assets.v3.export', 20)
        c.normal.receive.assert_called_once_with(20)
        alias = c.prepare_target('task-1', 'worker-1', 21)
        self.assertEqual(len(alias), 64)
        self.assertRegex(alias, r'^ci-activity-[a-z2-7]{52}$')
        import hashlib, base64
        expected = base64.b32encode(hashlib.sha256(
            b'adtr-ci-activity-v1\x00' + bytes(16) + b'\x00task-1').digest())
        self.assertEqual(alias, 'ci-activity-' + expected.decode().lower().rstrip('='))
        c.normal.receive.assert_called_once_with(20)  # Stopped worker cannot ACK.
        self.assertEqual(c.normal.send.call_args.args, ({'version': 2, 'epoch': 1,
            'seq': 2, 'op': 'configure_target', 'taskID': 'task-1', 'workerID': 'worker-1'}, 21))
        c.supervisor.receive.return_value = {'version': 2, 'epoch': 1, 'status': 'armed'}
        c.arm(22)
        c.finish_target(22)
        self.assertEqual(c.normal.receive.call_args.args, (22,))
        self.assertEqual(c.normal.send.call_count, 2)
        with self.assertRaises(r.ControlFailure):
            c.finish_target(22)
        self.assertEqual(c.normal.receive.call_count, 2)

    def test_activity_alias_matches_go_fixed_vector(self):
        c = controller(); c.armed = False; c.selection_prepared = True
        c.nonce = bytes(range(16))
        self.assertEqual(c.prepare_target('task-one', 'worker-one', 2),
                         'ci-activity-xjhrbwphlzfwschmfnm4kqsunp2qeowpnarubert6q2qx5ospmha')
        c.normal.receive.assert_not_called()

    def test_configuration_rejects_replay_bad_schema_and_overtaking(self):
        for kind in ('unknown', True, 'x' * 10000):
            c = controller(); c.armed = False
            with self.assertRaises(r.ControlFailure): c.prepare_selection(kind, 2)
            c.normal.send.assert_not_called()
        for bad in ('', 'private\nvalue', 'x' * 129, True):
            c = controller(); c.armed = False; c.selection_prepared = True
            with self.assertRaises(r.ControlFailure): c.prepare_target(bad, 'worker', 2)
            c.normal.send.assert_not_called()
        for bad in ('x' * 65, 'Upper', 'worker.dot', True):
            c = controller(); c.armed = False; c.selection_prepared = True
            with self.assertRaises(r.ControlFailure): c.prepare_target('task', bad, 2)
            c.normal.send.assert_not_called()
        c = controller(); c.armed = False; c.selection_prepared = True
        c.prepare_target('task', 'worker', 2)
        with self.assertRaises(r.ControlFailure): c.prepare_target('task', 'worker', 2)
        c.normal.send.assert_called_once(); c.normal.receive.assert_not_called()
        c = controller(); c.target_sent = True
        with self.assertRaises(r.ControlFailure): c.snapshot(2)
        c.normal.send.assert_not_called()
        for response in (dict(frame(), seq=99), dict(frame(), private='secret'),
                         dict(frame(), status='draining')):
            c = controller(); c.target_sent = True; c.seq = 1
            c.normal.receive.return_value = response
            with self.assertRaises(r.ControlFailure) as caught: c.finish_target(2)
            self.assertEqual(caught.exception.control_diagnostic['operation'], 'finish_target')
            self.assertNotIn('secret', str(caught.exception))

    def test_candidate_activity_key_is_required_closed_and_bounded(self):
        candidate = dict(generation=1, taskID='task', kind='user_assets.v3.export',
                         attempt=1, leaseOwner='worker', fencingToken=1,
                         resultVersion=1, phase='first-fenced-progress-10', activityKey='key')
        value = frame(); value['records'] = [dict(generation=1, state='open', backend=123,
                                                  candidate=candidate)]
        self.assertIs(r._frame(value, 1), value)
        for bad in ('', 'x' * 65, 'private\nvalue', 'Upper', 'key.dot', True):
            candidate['activityKey'] = bad
            with self.assertRaises(r.ControlFailure): r._frame(value, 1)
        del candidate['activityKey']
        with self.assertRaises(r.ControlFailure): r._frame(value, 1)

    def test_capture_end_frame_matches_go_fixed_vector(self):
        value = r.CAPTURE_END.pack(b'ADTREND1', bytes([7]) * 16, 1, 2, 3)
        self.assertEqual(len(value), 48)
        self.assertEqual(value.hex(), '41445452454e4431' + '07' * 16
                         + '0000000000000001' + '0000000000000002' + '0000000000000003')

    def test_capture_commit_ack_is_bound_once_and_original_deadline(self):
        c = controller(); c.capture_deadline = 10
        ack = dict(version=2, epoch=1, status='committed', ticketGeneration=7, registrationGeneration=9)
        c.supervisor.receive.return_value = ack
        with patch.object(r.time, 'monotonic', return_value=1):
            self.assertIs(c.commit_capture(7, 9, 10), True)
            self.assertTrue(c.committed)
            with self.assertRaises(r.ControlFailure): c.commit_capture(7, 9, 10)
        c.supervisor.send.assert_called_once()
        self.assertEqual(c.supervisor.send.call_args.args[1], 10)
        self.assertEqual(c.supervisor.receive.call_args.args, (10,))
        for response in (ack | {'ticketGeneration': True}, ack | {'registrationGeneration': 8},
                         ack | {'private': 'secret'}):
            c = controller(); c.capture_deadline = 10
            c.supervisor.receive.return_value = response
            with patch.object(r.time, 'monotonic', return_value=1):
                with self.assertRaises(r.ControlFailure): c.commit_capture(7, 9, 10)
            self.assertTrue(c.invalid); self.assertFalse(c.committed)
        c = controller(); c.capture_deadline = 10
        c.supervisor.receive.return_value = ack
        with patch.object(r.time, 'monotonic', side_effect=[1, 10]):
            with self.assertRaises(r.ControlFailure): c.commit_capture(7, 9, 10)
        self.assertFalse(c.committed)
        c = controller(); c.armed = False
        c.supervisor.send.side_effect = r.ControlFailure()
        with self.assertRaises(r.ControlFailure): c.arm(10)
        self.assertTrue(c.armed)  # Uncertain ARM must retain the exclusive CONT route.

    def test_rescuer_capture_write_is_atomic_and_all_failures_recover(self):
        arm = dict(version=2, epoch=1, op='arm', deadline=1.5)
        commit = dict(version=2, epoch=1, op='commit_capture', ticketGeneration=7, registrationGeneration=9)
        for scenario in ('ok', 'not_stopped', 'identity', 'short', 'lost_ack', 'duplicate', 'pre_eof', 'expired'):
            supervisor, rescue, identity = Mock(), Mock(), Mock()
            events = []
            frames = [arm] + ([] if scenario == 'pre_eof' else [commit])
            if scenario == 'duplicate': frames.append(commit)
            supervisor.receive.side_effect = frames + [r.ControlFailure()]
            identity.threads_stopped.return_value = scenario != 'not_stopped'
            identity.resume.side_effect = lambda deadline: events.append('resume')
            if scenario == 'identity': identity.verify.side_effect = [None, r.ControlFailure()]
            def send(frame, deadline):
                if frame.get('status') == 'committed' and scenario == 'lost_ack':
                    raise r.ControlFailure()
            supervisor.send.side_effect = send
            def write(fd, payload):
                events.append('write')
                self.assertEqual(fd, 81)
                self.assertEqual(r.CAPTURE_END.unpack(payload), (b'ADTREND1', bytes(16), 1, 7, 9))
                self.assertEqual(len(payload), 48)
                return 47 if scenario == 'short' else 48
            clock = [0, 0, 0, 0, 0, 2] if scenario == 'expired' else None
            def now():
                return clock.pop(0) if clock else (2 if scenario == 'expired' else 0)
            with patch.object(r, 'PipeChannel', side_effect=[supervisor, rescue]), \
                    patch.object(r, 'OwnedIdentity', return_value=identity), \
                    patch.object(r, '_rescue_exchange'), patch.object(r.os, 'write', side_effect=write) as writing, \
                    patch.object(r.os, 'close'), patch.object(r.time, 'monotonic', side_effect=now), \
                    patch.object(r.select, 'select', return_value=([1], [], [])):
                self.assertEqual(r._rescue_main(1, 2, 100, 9, '/owned', 81, bytes(16)), 1)
            self.assertLessEqual(writing.call_count, 1)
            if scenario in ('not_stopped', 'identity', 'pre_eof', 'expired'):
                writing.assert_not_called()
            if 'write' in events: self.assertLess(events.index('write'), events.index('resume'))
            self.assertIn('resume', events)

    def test_capture_fd_and_secondary_guard_resume_are_exclusive(self):
        source = Path(r.__file__).read_text()
        self.assertIn('os.pipe2(os.O_NONBLOCK | os.O_CLOEXEC)', source)
        self.assertIn('os.close(capture_write); self.fds.remove(capture_write)', source)
        tree = ast.parse(source)
        commit = next(node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef)
                      and node.name == 'commit_capture')
        self.assertNotIn('SIGCONT', ast.get_source_segment(source, commit))
        runner = Path(__file__).with_name('test_auth_e2e.py').read_text()
        self.assertEqual(runner.count('export_worker_guard.resume_delegate = running_cancel_guard._resume_capture_owned'), 1)

    def test_close_same_socket_fd_once(self):
        c = self.channel()
        with patch.object(r.os, 'close') as close:
            c.close(); c.close()
            close.assert_called_once_with(51)

    def test_partial_write_is_terminal_no_replay(self):
        c = self.channel()
        with patch.object(c, '_ready'), patch.object(r.os, 'write', return_value=1) as write:
            with self.assertRaises(r.ControlFailure):
                c.send({'op': 'abort'}, r.time.monotonic() + 1)
            self.assertTrue(c.failed)
            self.assertEqual(write.call_count, 1)

    def test_eof_and_half_frame_latch(self):
        for data in (b'', b'{"version":2'):
            c = self.channel()
            with patch.object(c, '_ready'), patch.object(r.os, 'read', side_effect=[data, b'']):
                with self.assertRaises(r.ControlFailure):
                    c.receive(r.time.monotonic() + 1)
            self.assertTrue(c.failed)

    def test_duplicate_json_keys_rejected(self):
        c = self.channel(); c.buffer.extend(b'{"epoch":1,"epoch":1}\n')
        with self.assertRaises(r.ControlFailure):
            c.receive(r.time.monotonic() + 1)

    def test_partial_after_stop_rejected(self):
        c = self.channel(); c.buffer.extend(b'{')
        with patch.object(r.os, 'read', side_effect=BlockingIOError):
            with self.assertRaises(r.ControlFailure):
                c.drain()

    def test_expired_absolute_deadline_never_waits(self):
        c = self.channel()
        with patch.object(r.select, 'select') as select:
            with self.assertRaises(r.ControlFailure):
                c.receive(r.time.monotonic() - 1)
            select.assert_not_called()

    def test_invalid_normal_frame_cannot_be_overwritten(self):
        c = controller()
        c.normal.receive.return_value = frame() | {'kind': 'invalid', 'seq': 0, 'status': 'invalid'}
        with self.assertRaises(r.ControlFailure):
            c.snapshot(r.time.monotonic() + 1)
        c.normal.receive.return_value = frame() | {'status': 'sealed'}
        with self.assertRaises(r.ControlFailure):
            c.snapshot(r.time.monotonic() + 1)
        self.assertEqual(c.normal.send.call_count, 1)

    def test_witness_header_bit_bound_nonce_reserved_are_strict(self):
        for offset in (0, 8, 12, 16, 24, 28, 32, 4095):
            c = controller(); c.normal.drain.return_value = []
            value = bytearray(c.witness); value[offset] ^= 1; c.witness = value
            with self.assertRaises(r.ControlFailure):
                c.after_stop(r.time.monotonic() + 1)
        c = controller(); c.normal.drain.return_value = []
        self.assertTrue(c.after_stop(r.time.monotonic() + 1))
        c.normal.send.assert_not_called()

    def test_fallback_only_after_sibling_wait_and_cont(self):
        c = controller(); c.supervisor.send.side_effect = r.ControlFailure()
        events = []
        c.owner.stop_rescuer.side_effect = lambda: events.append('wait-rescuer')
        c.owner.identity.resume.side_effect = lambda deadline: events.append('cont-confirmed')
        with patch.object(r, '_rescue_exchange', side_effect=lambda *args: events.append('release')):
            with self.assertRaises(r.ControlFailure):
                c.resume_and_release()
        self.assertEqual(events, ['wait-rescuer', 'cont-confirmed', 'release'])
        self.assertTrue(c.invalid)

    def test_sibling_not_waited_forbids_fallback(self):
        c = controller(); c.supervisor.send.side_effect = r.ControlFailure()
        c.owner.stop_rescuer.side_effect = r.subprocess.TimeoutExpired('owned', 1)
        with patch.object(r, '_rescue_exchange') as exchange:
            with self.assertRaises(r.subprocess.TimeoutExpired):
                c.resume_and_release()
            exchange.assert_not_called()

    def test_identity_mismatch_never_signals(self):
        with patch.object(r.os, 'pidfd_open', return_value=91), patch.object(r.os, 'close'), \
                patch.object(r, 'read_identity', return_value=(11, '/owned', 'S')):
            identity = r.OwnedIdentity(100, 11, '/owned')
        with patch.object(r, 'read_identity', return_value=(12, '/owned', 'S')), \
                patch.object(r.signal, 'pidfd_send_signal') as send:
            with self.assertRaises(r.ControlFailure):
                identity.send(signal.SIGCONT)
            send.assert_not_called()

    def test_page_close_requires_worker_wait_first(self):
        owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
        with self.assertRaises(r.ControlFailure):
            owner.close_after_wait()

    def test_final_sticky_failure_releases_resources_after_wait(self):
        c = controller()
        page = bytearray(c.witness); page[24] = 1
        events = []
        class Page:
            def __getitem__(self, key):
                events.append('read-after-wait')
                return page[key]
            def close(self):
                events.append('unmap')
        owner = r.OwnedCIWorker()
        owner.process = Mock(returncode=0)
        owner.controller = c
        owner.witness = Page()
        owner.page_fd = 70
        owner.fds = {71, 72}
        owner.stop_rescuer = lambda: events.append('wait-rescuer')
        owner.identity = Mock()
        with patch.object(r.os, 'close', side_effect=lambda fd: events.append(('close', fd))):
            with self.assertRaises(r.ControlFailure):
                owner.close_after_wait()
        self.assertLess(events.index('read-after-wait'), events.index('unmap'))
        self.assertEqual(events[-1], ('close', 70))
        self.assertIsNone(owner.witness)
        self.assertIsNone(owner.page_fd)

    def test_first_connect_projection_is_closed_and_independent_of_eligibility(self):
        import io
        event = dict(event=r.FIRST_CONNECT_EVENT, birthPhase='released',
                     birthCaptureEnded=False, birthRecoveryAuthorized=True,
                     failurePhase='released', contextState='canceled',
                     errorCategory='network_error', errorMatchesContext=False,
                     rootContextState='active', rawNil=True)
        line = json.dumps(event) + '\n'
        logs = io.StringIO(line); logs.seek(4)
        self.assertEqual(r.first_connect_projection(logs), event)
        self.assertEqual(logs.tell(), 4)
        for category in ('none', 'canceled', 'deadline_exceeded', 'authentication', 'network_error', 'other'):
            value = event | {'errorCategory': category}
            self.assertEqual(r.first_connect_projection(io.StringIO(json.dumps(value) + '\n')), value)
        bad = [line + line, line.rstrip(), '', 'prefix ' + line, ' ' * 1024 + line,
               json.dumps(event | {'private': 'secret'}) + '\n',
               json.dumps(event | {'errorCategory': 'network'}) + '\n',
               json.dumps(event | {'birthPhase': 'private'}) + '\n',
               json.dumps(event | {'failurePhase': True}) + '\n',
               json.dumps(event | {'contextState': None}) + '\n',
               json.dumps(event | {'rootContextState': 'private'}) + '\n',
               line.replace('"rawNil": true', '"rawNil": true, "rawNil": false'),
               '\udcff' + line]
        for key in ('birthCaptureEnded', 'birthRecoveryAuthorized', 'errorMatchesContext', 'rawNil'):
            for value in (0, 1, None, 'false', {}):
                bad.append(json.dumps(event | {key: value}) + '\n')
        for key in event:
            bad.append(json.dumps({k: v for k, v in event.items() if k != key}) + '\n')
        for text in bad:
            result = r.first_connect_projection(io.StringIO(text))
            self.assertEqual(set(result), set(event))
            self.assertEqual(result['event'], r.FIRST_CONNECT_EVENT)
            self.assertTrue(all(value == 'unknown' for key, value in result.items() if key != 'event'))
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertEqual(r.first_connect_projection(io.StringIO(line))['rawNil'], 'unknown')
        broken = Mock(); broken.tell.return_value = 13
        broken.read.side_effect = RuntimeError('private details')
        self.assertEqual(r.first_connect_projection(broken)['rawNil'], 'unknown')
        self.assertEqual(broken.seek.call_args.args, (13,))

    def test_connect_detail_print_requires_matching_first_cause_and_wait_once(self):
        import io
        detail = dict(event=r.FIRST_CONNECT_EVENT, birthPhase='open',
                      birthCaptureEnded=False, birthRecoveryAuthorized=False,
                      failurePhase='open', contextState='active', errorCategory='authentication',
                      errorMatchesContext=False, rootContextState='active', rawNil=True)
        for reason in ('connect_failed', 'target_revoked'):
            first = dict(event=r.FINAL_INVALIDATION_EVENT, reason=reason, phase='open')
            for missing in (False, True):
                logs = io.StringIO(json.dumps(first) + '\n' +
                                   ('' if missing else json.dumps(detail) + '\n'))
                owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
                with patch.object(r.sys, 'stderr', io.StringIO()) as output:
                    owner.report_first_invalidation_after_wait(logs)
                    self.assertEqual(logs.tell(), 0)
                    self.assertEqual(output.getvalue(), '')
                    owner.process.returncode = 1
                    owner.report_first_invalidation_after_wait(logs)
                    owner.report_first_invalidation_after_wait(logs)
                    rows = [json.loads(line) for line in output.getvalue().splitlines()]
                self.assertEqual(rows[0], first)
                self.assertEqual(len(rows), 2 if reason == 'connect_failed' else 1)
                if reason == 'connect_failed':
                    if missing: self.assertEqual(rows[1]['rawNil'], 'unknown')
                    else: self.assertEqual(rows[1], detail)

    def test_unclean_operation_projection_preserves_null_and_rejects_nested_noise(self):
        import io
        record = dict(operation='statement', startPhase='draining', endPhase='released',
                      contextState='deadline_exceeded', errorCategory='deadline_exceeded',
                      errorMatchesContext=False, nativeClosedAtEnd=False,
                      rootContextState='deadline_exceeded', connectionMatched=False)
        event = dict(event=r.UNCLEAN_OPERATION_EVENT, birthPhase='released',
                     birthCaptureEnded=False, firstError=record,
                     firstNativeClosedError=record.copy(), rootAtClose='deadline_exceeded')
        line = json.dumps(event, separators=(',', ':')) + '\n'
        self.assertLessEqual(len(line.encode()), 1024)
        logs = io.StringIO(line); logs.seek(8)
        self.assertEqual(r.unclean_operation_projection(logs), event)
        self.assertEqual(logs.tell(), 8)
        nulls = event | {'firstError': None, 'firstNativeClosedError': None}
        self.assertEqual(r.unclean_operation_projection(io.StringIO(json.dumps(nulls) + '\n')), nulls)
        bad = [line + line, line.rstrip(), '', 'prefix ' + line, ' ' * 1024 + line,
               json.dumps(event | {'extra': ''}) + '\n',
               json.dumps(event | {'birthCaptureEnded': 1}) + '\n',
               json.dumps(event | {'birthPhase': 'private'}) + '\n',
               json.dumps(event | {'rootAtClose': True}) + '\n',
               line.replace('"operation":"statement"', '"operation":"statement","operation":"begin"', 1),
               '\udcff' + line]
        for name in ('firstError', 'firstNativeClosedError'):
            for value in ('unknown', {}, [], False, record | {'private': 'secret'},
                          record | {'operation': 'query'}, record | {'startPhase': 'private'},
                          record | {'contextState': None}, record | {'errorCategory': 'unknown'},
                          record | {'rootContextState': False}):
                bad.append(json.dumps(event | {name: value}) + '\n')
            for key in ('errorMatchesContext', 'nativeClosedAtEnd', 'connectionMatched'):
                for value in (0, 1, 'false', None):
                    bad.append(json.dumps(event | {name: record | {key: value}}) + '\n')
            for key in record:
                bad.append(json.dumps(event | {name: {k: v for k, v in record.items() if k != key}}) + '\n')
        for key in event:
            bad.append(json.dumps({k: v for k, v in event.items() if k != key}) + '\n')
        for text in bad:
            result = r.unclean_operation_projection(io.StringIO(text))
            self.assertEqual(result['firstError'], 'unknown')
            self.assertEqual(result['firstNativeClosedError'], 'unknown')
            self.assertEqual(result['birthCaptureEnded'], 'unknown')
            self.assertNotIn('secret', json.dumps(result))
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertEqual(r.unclean_operation_projection(io.StringIO(line))['firstError'], 'unknown')
        broken = Mock(); broken.tell.return_value = 11
        broken.read.side_effect = RuntimeError('private details')
        self.assertEqual(r.unclean_operation_projection(broken)['firstError'], 'unknown')
        self.assertEqual(broken.seek.call_args.args, (11,))

    def test_first_unclean_projection_is_closed_and_never_guesses_booleans(self):
        import io
        event = dict(event=r.FIRST_UNCLEAN_EVENT, alreadyClosed=False, closeFailed=True,
                     cleanupDone=False, role='epoch_target')
        line = json.dumps(event) + '\n'
        logs = io.StringIO(line); logs.seek(3)
        self.assertEqual(r.first_unclean_projection(logs), event)
        self.assertEqual(logs.tell(), 3)
        bad = [line + line, line.rstrip(), '', 'prefix ' + line, ' ' * 1024 + line,
               json.dumps(event | {'private': 'secret'}) + '\n',
               json.dumps(event | {'role': 'private identity'}) + '\n',
               line.replace('"alreadyClosed": false', '"alreadyClosed": false, "alreadyClosed": true'),
               '\udcff' + line]
        for key in ('alreadyClosed', 'closeFailed', 'cleanupDone'):
            for value in (0, 1, None, 'false', {}, 'unknown'):
                bad.append(json.dumps(event | {key: value}) + '\n')
        for key in event:
            bad.append(json.dumps({k: v for k, v in event.items() if k != key}) + '\n')
        unknown = dict(event=r.FIRST_UNCLEAN_EVENT, alreadyClosed='unknown',
                       closeFailed='unknown', cleanupDone='unknown', role='unknown')
        for text in bad:
            self.assertEqual(r.first_unclean_projection(io.StringIO(text)), unknown)
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertEqual(r.first_unclean_projection(io.StringIO(line)), unknown)
        broken = Mock(); broken.tell.return_value = 5
        broken.read.side_effect = RuntimeError('private details')
        self.assertEqual(r.first_unclean_projection(broken), unknown)
        self.assertEqual(broken.seek.call_args.args, (5,))

    def test_first_unclean_print_requires_matching_first_cause_wait_and_once(self):
        import io
        detail = dict(event=r.FIRST_UNCLEAN_EVENT, alreadyClosed=True, closeFailed=False,
                      cleanupDone=True, role='other')
        operation = dict(event=r.UNCLEAN_OPERATION_EVENT, birthPhase='open',
                         birthCaptureEnded=False, firstError=None,
                         firstNativeClosedError=None, rootAtClose='active')
        for reason in ('unclean_close', 'target_revoked'):
            first = dict(event=r.FINAL_INVALIDATION_EVENT, reason=reason, phase='released')
            for missing in (False, True):
                logs = io.StringIO(json.dumps(first) + '\n' +
                                   ('' if missing else json.dumps(detail) + '\n' + json.dumps(operation) + '\n'))
                owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
                with patch.object(r.sys, 'stderr', io.StringIO()) as output:
                    owner.report_first_invalidation_after_wait(logs)
                    self.assertEqual(logs.tell(), 0)
                    self.assertEqual(output.getvalue(), '')
                    owner.process.returncode = 1
                    owner.report_first_invalidation_after_wait(logs)
                    owner.report_first_invalidation_after_wait(logs)
                    rows = [json.loads(line) for line in output.getvalue().splitlines()]
                self.assertEqual(rows[0], first)
                self.assertEqual(len(rows), 3 if reason == 'unclean_close' else 1)
                if reason == 'unclean_close':
                    self.assertEqual(rows[2]['event'], r.UNCLEAN_OPERATION_EVENT)
                    if missing:
                        self.assertEqual(rows[2]['firstError'], 'unknown')
                    else:
                        self.assertEqual(rows[2], operation)
                if reason == 'unclean_close':
                    if missing:
                        self.assertEqual(rows[1]['role'], 'unknown')
                        self.assertEqual(rows[1]['alreadyClosed'], 'unknown')
                    else:
                        self.assertEqual(rows[1], detail)

    def test_recovery_cleanup_private_summary_is_strict_and_after_wait(self):
        import io
        summary = dict(event=r.RECOVERY_CLEANUP_EVENT, captureAuthorized=True,
                       released=True, settled=True,
                       **{key: 0 for key in r.RECOVERY_CLEANUP_COUNTS})
        line = json.dumps(summary) + '\n'
        owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
        logs = io.StringIO(line); logs.seek(6)
        with patch('builtins.print') as output:
            self.assertIsNone(owner.recovery_cleanup_after_wait(logs))
            self.assertEqual(logs.tell(), 6)
            owner.process.returncode = 0
            self.assertEqual(owner.recovery_cleanup_after_wait(logs), summary)
            self.assertEqual(logs.tell(), 6)
            output.assert_not_called()
        bad = [line + line, line.rstrip(), '', 'prefix ' + line, ' ' * 1024 + line,
               json.dumps(summary | {'private': 'secret'}) + '\n', '\udcff' + line,
               line.replace('"released": true', '"released": true, "released": false')]
        for key in r.RECOVERY_CLEANUP_COUNTS:
            for value in (True, -1, 2 ** 63, 1.0, '0', {}, None):
                bad.append(json.dumps(summary | {key: value}) + '\n')
            maximum = summary | {key: 2 ** 63 - 1}
            self.assertEqual(r.recovery_cleanup_projection(io.StringIO(json.dumps(maximum) + '\n')), maximum)
        for key in ('captureAuthorized', 'released', 'settled'):
            for value in (0, 1, None, 'true'):
                bad.append(json.dumps(summary | {key: value}) + '\n')
        for key in summary:
            bad.append(json.dumps({k: v for k, v in summary.items() if k != key}) + '\n')
        for text in bad:
            logs = io.StringIO(text); logs.seek(1 if text else 0); before = logs.tell()
            self.assertIsNone(r.recovery_cleanup_projection(logs))
            self.assertEqual(logs.tell(), before)
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertIsNone(r.recovery_cleanup_projection(io.StringIO(line)))
        broken = Mock(); broken.tell.return_value = 12
        broken.read.side_effect = RuntimeError('private failure')
        self.assertIsNone(r.recovery_cleanup_projection(broken))
        self.assertEqual(broken.seek.call_args.args, (12,))

    def test_recovery_cleanup_must_pass_before_journal_and_keeps_finally(self):
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        gates = [node for node in ast.walk(tree) if isinstance(node, ast.If)
                 and ast.unparse(node.test) == 'running_cancel_guard is not None and shutdown_error is None']
        self.assertEqual(len(gates), 2)
        gates.sort(key=lambda node: node.lineno)
        self.assertIn('verify_recovery_cleanup_after_wait', ast.get_source_segment(source, gates[0]))
        self.assertIn('verify_journal_after_wait', ast.get_source_segment(source, gates[1]))
        wait_loop = next(node for node in ast.walk(tree) if isinstance(node, ast.For)
                         and ast.unparse(node.target) == '(label, process)')
        self.assertLess(wait_loop.end_lineno, gates[0].lineno)
        self.assertLess(source.index('owned_ci_worker.close_after_wait()'),
                        source.index('running_cancel_guard.verify_recovery_cleanup_after_wait'))
        self.assertLess(source.index('cleanup_activity_fixture_after_wait(worker.returncode)'),
                        source.index('running_cancel_guard.verify_recovery_cleanup_after_wait'))
        self.assertLess(gates[1].end_lineno, next(node.lineno for node in ast.walk(tree)
                        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                        and node.func.id == 'remove_owned_container'))
        code = compile(ast.fix_missing_locations(ast.Module(body=gates, type_ignores=[])),
                       '<recovery-proof-contract>', 'exec')
        for prior, failure in ((None, None), (None, RuntimeError('private error')),
                               (RuntimeError('prior'), None)):
            guard, owner = Mock(), Mock()
            guard.verify_recovery_cleanup_after_wait.side_effect = failure
            owner.recovery_cleanup_after_wait.return_value = None
            owner.final_journal_after_wait.return_value = {'private': 'journal'}
            scope = dict(running_cancel_guard=guard, owned_ci_worker=owner,
                         worker=Mock(returncode=0), logs=object(), shutdown_error=prior)
            exec(code, scope)
            if prior is None:
                guard.verify_recovery_cleanup_after_wait.assert_called_once_with(0, None)
                if failure is None:
                    guard.verify_journal_after_wait.assert_called_once_with(0, {'private': 'journal'})
                else:
                    guard.verify_journal_after_wait.assert_not_called()
                    self.assertEqual(str(scope['shutdown_error']), 'owned CI recovery cleanup proof failed')
            else:
                guard.verify_recovery_cleanup_after_wait.assert_not_called()
                owner.recovery_cleanup_after_wait.assert_not_called()
                guard.verify_journal_after_wait.assert_not_called()
                self.assertIs(scope['shutdown_error'], prior)

    def test_final_journal_private_projection_is_strict_and_post_wait(self):
        import io
        stats = dict(event=r.FINAL_JOURNAL_EVENT, processIDSHA256='ab' * 32,
                     closeOK=True, acceptanceStopped=True,
                     **{key: 0 for key in r.FINAL_JOURNAL_COUNTS})
        line = json.dumps(stats) + '\n'
        owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
        logs = io.StringIO('private other log\n' + line); logs.seek(5)
        with patch('builtins.print') as output:
            self.assertIsNone(owner.final_journal_after_wait(logs))
            self.assertEqual(logs.tell(), 5)
            owner.process.returncode = 0
            self.assertEqual(owner.final_journal_after_wait(logs), stats)
            self.assertEqual(logs.tell(), 5)
            output.assert_not_called()
        for key in r.FINAL_JOURNAL_COUNTS:
            for bad in (True, -1, 2 ** 63, 1.0, '0', {}, None):
                self.assertIsNone(r.final_journal_projection(io.StringIO(
                    json.dumps(stats | {key: bad}) + '\n')))
            self.assertEqual(r.final_journal_projection(io.StringIO(
                json.dumps(stats | {key: 2 ** 63 - 1}) + '\n'))[key], 2 ** 63 - 1)
        malformed = [line + line, line.rstrip(), '', 'prefix ' + line, ' ' * 1024 + line,
                     json.dumps(stats | {'extra': ''}) + '\n',
                     json.dumps(stats | {'processIDSHA256': 'AB' * 32}) + '\n',
                     json.dumps(stats | {'processIDSHA256': 'ab' * 32 + '\n'}) + '\n',
                     json.dumps(stats | {'closeOK': 1}) + '\n',
                     json.dumps(stats | {'acceptanceStopped': 0}) + '\n',
                     line.replace('"closeOK": true', '"closeOK": true, "closeOK": false'),
                     '\udcff' + line]
        for key in stats:
            malformed.append(json.dumps({k: v for k, v in stats.items() if k != key}) + '\n')
        for text in malformed:
            logs = io.StringIO(text); logs.seek(1 if text else 0); before = logs.tell()
            self.assertIsNone(r.final_journal_projection(logs))
            self.assertEqual(logs.tell(), before)
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertIsNone(r.final_journal_projection(io.StringIO(line)))
        broken = Mock(); broken.tell.return_value = 9
        broken.read.side_effect = UnicodeError('private malformed bytes')
        self.assertIsNone(r.final_journal_projection(broken))
        self.assertEqual(broken.seek.call_args.args, (9,))

    def test_first_invalidation_projection_is_exact_bounded_and_private(self):
        import io
        event = dict(event=r.FINAL_INVALIDATION_EVENT, reason='unclean_close', phase='sealed')
        line = json.dumps(event) + '\n'
        logs = io.StringIO('private SQL nonce credentials\n' + line)
        logs.seek(7)
        self.assertEqual(r.first_invalidation_projection(logs), event)
        self.assertEqual(logs.tell(), 7)
        bad = [line + line, line.rstrip(), 'prefix ' + line, '',
               json.dumps(event | {'private': 'secret'}) + '\n',
               json.dumps(event | {'reason': 'secret'}) + '\n',
               json.dumps(event | {'phase': True}) + '\n',
               '{"event":"owned_ci_first_invalidation","reason":"none","reason":"abort","phase":"open"}\n',
               ' ' * 1024 + line]
        for text in bad:
            result = r.first_invalidation_projection(io.StringIO(text))
            self.assertEqual(result, dict(event=r.FINAL_INVALIDATION_EVENT, reason='unknown', phase='unknown'))
            self.assertNotIn('secret', json.dumps(result))
        with patch.object(r, 'MAX_FINAL_LOG_BYTES', len(line) - 1):
            self.assertEqual(r.first_invalidation_projection(io.StringIO(line))['reason'], 'unknown')
        broken = Mock(); broken.tell.return_value = 17
        broken.read.side_effect = RuntimeError('private failure')
        self.assertEqual(r.first_invalidation_projection(broken)['reason'], 'unknown')
        self.assertEqual(broken.seek.call_args.args, (17,))

    def test_first_invalidation_is_once_only_after_worker_wait(self):
        import io
        owner = r.OwnedCIWorker(); owner.process = Mock(returncode=None)
        logs = io.StringIO(json.dumps(dict(event=r.FINAL_INVALIDATION_EVENT,
                                          reason='abort', phase='released')) + '\n')
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            owner.report_first_invalidation_after_wait(logs)
            self.assertEqual(output.getvalue(), '')
            self.assertEqual(logs.tell(), 0)
            owner.process.returncode = 1
            owner.report_first_invalidation_after_wait(logs)
            owner.report_first_invalidation_after_wait(logs)
            self.assertEqual(len(output.getvalue().splitlines()), 1)
            self.assertEqual(json.loads(output.getvalue())['reason'], 'abort')
        owner = r.OwnedCIWorker(); owner.process = Mock(returncode=0)
        with patch('builtins.print', side_effect=RuntimeError('private output failure')):
            owner.report_first_invalidation_after_wait(logs)
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Attribute)
                 and node.func.attr == 'report_first_invalidation_after_wait']
        self.assertEqual(len(calls), 1)
        wait_loop = next(node for node in ast.walk(tree) if isinstance(node, ast.For)
                         and ast.unparse(node.target) == '(label, process)')
        self.assertLess(wait_loop.end_lineno, calls[0].lineno)
        self.assertLess(source.index('report_first_invalidation_after_wait(logs)'),
                        source.index('logs.close()'))

    def test_final_state_is_closed_post_wait_and_preserves_failure(self):
        import io
        class Page:
            def __init__(self, data): self.data = data
            def __getitem__(self, key): return self.data[key]
            def close(self): pass
        def owner_for(data, invalid=False, released=False):
            owner = r.OwnedCIWorker(); owner.process = Mock(returncode=0)
            owner.controller = controller(); owner.controller.invalid = invalid
            owner.controller.released = released
            owner.witness = Page(data); owner.identity = None
            owner.fds = set(); owner.page_fd = None; owner.stop_rescuer = Mock()
            return owner
        original = controller().witness
        for offset, value, sticky in ((24, 0, 'clear'), (24, 1, 'set'),
                                      (24, 2, 'other'), (28, 0, 'other'),
                                      (0, 0, 'other'), (100, 1, 'other')):
            data = bytearray(original); data[offset] = value
            owner = owner_for(data)
            with patch.object(r.sys, 'stderr', io.StringIO()) as output:
                if sticky == 'clear': owner.close_after_wait()
                else:
                    with self.assertRaises(r.ControlFailure): owner.close_after_wait()
                report = json.loads(output.getvalue())
            self.assertEqual(report, dict(event='owned_ci_final_state', exitZero=True,
                sticky=sticky, controllerInvalid=False, released=False, cleanup='completed'))
        owner = owner_for(original, invalid=True, released=True)
        owner.process.returncode = 1
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            with self.assertRaises(r.ControlFailure): owner.close_after_wait()
            report = json.loads(output.getvalue())
            self.assertEqual(report['sticky'], 'clear')
            self.assertTrue(report['controllerInvalid']); self.assertTrue(report['released'])
            self.assertFalse(report['exitZero'])
            # Repeated cleanup may still fail, but cannot emit a second projection.
            with self.assertRaises(Exception): owner.close_after_wait()
            self.assertEqual(len(output.getvalue().splitlines()), 1)
        owner = owner_for(original); owner.process.returncode = None
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            with self.assertRaises(r.ControlFailure): owner.close_after_wait()
            self.assertEqual(output.getvalue(), '')
        owner = owner_for(original); owner.identity = Mock()
        owner.identity.close.side_effect = RuntimeError('private identity')
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            with self.assertRaisesRegex(RuntimeError, 'private identity'): owner.close_after_wait()
            self.assertEqual(json.loads(output.getvalue())['cleanup'], 'identity')
            self.assertNotIn('private', output.getvalue())
        owner = owner_for(original); owner.controller = None
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            owner.close_after_wait()
            report = json.loads(output.getvalue())
            self.assertEqual(report['controllerInvalid'], 'unknown')
            self.assertEqual(report['released'], 'unknown')
            self.assertEqual(report['sticky'], 'unavailable')
        owner = owner_for(original, invalid=True)
        with patch('builtins.print', side_effect=RuntimeError('private output failure')):
            with self.assertRaises(r.ControlFailure): owner.close_after_wait()

    def test_parentdeath_half_frame_and_prearm_share_recovery(self):
        supervisor, rescue, identity = Mock(), Mock(), Mock()
        supervisor.receive.side_effect = r.ControlFailure()
        events = []
        identity.resume.side_effect = lambda deadline: events.append('cont-confirmed')
        identity.send.side_effect = lambda sig: events.append(sig)
        with patch.object(r, 'PipeChannel', side_effect=[supervisor, rescue]), \
                patch.object(r, 'OwnedIdentity', return_value=identity), \
                patch.object(r, '_rescue_exchange', side_effect=lambda *a: events.append('abort')), \
                patch.object(r.select, 'select', return_value=([1], [], [])):
            self.assertEqual(r._rescue_main(1, 2, 100, 9, '/owned'), 1)
        self.assertEqual(events[:2], ['cont-confirmed', 'abort'])
        self.assertIn(signal.SIGTERM, events)

    def test_activity_fixture_cleanup_runs_after_wait_even_on_prior_failure(self):
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        gate = next(node for node in ast.walk(tree) if isinstance(node, ast.If)
                    and ast.unparse(node.test) == 'running_cancel_guard is not None and worker is not None and (worker.returncode is not None)')
        wait_loop = next(node for node in ast.walk(tree) if isinstance(node, ast.For)
                         and ast.unparse(node.target) == '(label, process)')
        final_witness = source.index('owned_ci_worker.close_after_wait()')
        cleanup = source.index('running_cancel_guard.cleanup_activity_fixture_after_wait(worker.returncode)')
        proof = source.index('running_cancel_guard.verify_journal_after_wait(worker.returncode, owned_ci_worker.final_journal_after_wait(logs))')
        self.assertLess(wait_loop.end_lineno, gate.lineno)
        self.assertLess(final_witness, cleanup); self.assertLess(cleanup, proof)
        code = compile(ast.fix_missing_locations(ast.Module(body=[gate], type_ignores=[])),
                       '<activity-cleanup-contract>', 'exec')
        for returncode in (None, 0, 1, -9):
            for failure in (None, RuntimeError('private sensitive details')):
                guard = Mock(); guard.cleanup_activity_fixture_after_wait.side_effect = failure
                prior = RuntimeError('prior failure')
                scope = dict(running_cancel_guard=guard, worker=Mock(returncode=returncode),
                             shutdown_error=prior)
                exec(code, scope)
                if returncode is None:
                    guard.cleanup_activity_fixture_after_wait.assert_not_called()
                else:
                    guard.cleanup_activity_fixture_after_wait.assert_called_once_with(returncode)
                if returncode is not None and failure:
                    self.assertEqual(str(scope['shutdown_error']), 'owned CI activity fixture cleanup failed')
                else:
                    self.assertIs(scope['shutdown_error'], prior)

    def test_journal_proof_must_run_after_wait_before_database_cleanup(self):
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Attribute)
                 and node.func.attr == 'verify_journal_after_wait']
        self.assertEqual(len(calls), 1)
        proof = calls[0]
        self.assertEqual(ast.unparse(proof),
                         'running_cancel_guard.verify_journal_after_wait(worker.returncode, owned_ci_worker.final_journal_after_wait(logs))')
        wait_loop = next(node for node in ast.walk(tree) if isinstance(node, ast.For)
                         and ast.unparse(node.target) == '(label, process)')
        self.assertLess(wait_loop.end_lineno, proof.lineno)
        self.assertIn('process.wait(timeout=30)', ast.get_source_segment(source, wait_loop))
        self.assertIn('if code != 0:', ast.get_source_segment(source, wait_loop))
        cleanup = next(node for node in ast.walk(tree) if isinstance(node, ast.Call)
                       and isinstance(node.func, ast.Name) and node.func.id == 'remove_owned_container')
        self.assertLess(proof.lineno, cleanup.lineno)
        gate = next(node for node in ast.walk(tree) if isinstance(node, ast.If)
                    and ast.unparse(node.test) == 'running_cancel_guard is not None and shutdown_error is None'
                    and 'verify_journal_after_wait' in ast.get_source_segment(source, node))
        self.assertTrue(gate.lineno < proof.lineno < gate.end_lineno)
        # Execute only this isolated cleanup gate with mocks: no processes/DB.
        block = ast.Module(body=[gate], type_ignores=[])
        code = compile(ast.fix_missing_locations(block), '<journal-cleanup-contract>', 'exec')
        for prior_error, journal_error in ((None, None), (None, RuntimeError('private')),
                                            (RuntimeError('earlier cleanup'), None)):
            guard = Mock()
            guard.verify_journal_after_wait.side_effect = journal_error
            owner = Mock(); owner.final_journal_after_wait.return_value = {'private': 'stats'}
            scope = {'running_cancel_guard': guard, 'worker': Mock(returncode=0),
                     'owned_ci_worker': owner, 'logs': object(),
                     'shutdown_error': prior_error}
            exec(code, scope)
            if prior_error is None:
                guard.verify_journal_after_wait.assert_called_once_with(0, {'private': 'stats'})
                if journal_error is not None:
                    self.assertEqual(str(scope['shutdown_error']), 'owned CI final journal proof failed')
            else:
                guard.verify_journal_after_wait.assert_not_called()
                self.assertIs(scope['shutdown_error'], prior_error)

    def test_diagnostic_projection_rejects_private_or_malformed_fields(self):
        frame = {'version': 2, 'epoch': 1, 'seq': 1, 'kind': 'diagnostic',
                 'reason': 'capacity', 'phase': 'open', 'pending': 0,
                 'open': 2, 'total': 4096, 'retained': 4096}
        result = r.diagnostic_projection(frame, 1)
        self.assertNotIn('seq', result)
        self.assertEqual(result['reason'], 'capacity')
        for mutation in ({'records': ['private']}, {'reason': 'private SQL'},
                         {'phase': 'private'}, {'pending': True}, {'total': -1},
                         {'retained': 2 ** 64}, {'epoch': True}, {'seq': 2}):
            with self.assertRaises(r.ControlFailure):
                r.diagnostic_projection(frame | mutation, 1)

    def test_diagnostic_lifetime_total_may_exceed_retained_capacity(self):
        # Pruned clean tickets leave a monotonic lifetime count; the live
        # retained budget is a separate number, never a cap on total.
        frame = {'version': 2, 'epoch': 1, 'seq': 1, 'kind': 'diagnostic',
                 'reason': 'none', 'phase': 'open', 'pending': 0,
                 'open': 1, 'total': 4097, 'retained': 1}
        for total in (4097, 65536, 2 ** 64 - 1):
            with self.subTest(total=total):
                result = r.diagnostic_projection(frame | {'total': total}, 1)
                self.assertEqual(result['total'], total)
                self.assertEqual(result['retained'], 1)
        for total in (2 ** 64, 2 ** 64 + 1, -1, True, '4097'):
            with self.subTest(total=total), self.assertRaises(r.ControlFailure):
                r.diagnostic_projection(frame | {'total': total}, 1)

    def test_failed_fixture_diagnostic_unarmed_running_only(self):
        import io
        import threading
        owner = r.OwnedCIWorker()
        owner.controller = controller()
        owner.controller.armed = False
        owner.controller.mutex = threading.RLock()
        owner.process = Mock(); owner.process.poll.return_value = None
        owner.identity = Mock(); owner.identity.threads_running.return_value = True
        owner.controller.normal.receive.return_value = {
            'version': 2, 'epoch': 1, 'seq': 1, 'kind': 'diagnostic',
            'reason': 'unclean_close', 'phase': 'open', 'pending': 0,
            'open': 1, 'total': 17, 'retained': 17}
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            result = owner.report_failure_diagnostic()
            self.assertEqual(json.loads(output.getvalue()), result)
        self.assertEqual(result['reason'], 'unclean_close')
        self.assertEqual(owner.controller.normal.send.call_args.args[0]['op'], 'diagnostic')
        self.assertFalse(owner.controller.armed)
        self.assertFalse(owner.controller.invalid)
        owner.controller.target_sent = True
        owner.controller.normal.reset_mock()
        with patch.object(r.sys, 'stderr', io.StringIO()):
            self.assertEqual(owner.report_failure_diagnostic()['reason'], 'unavailable')
        owner.controller.normal.send.assert_not_called()
        owner.controller.normal.receive.assert_not_called()
        owner.controller.target_sent = False
        for armed, running, exited in ((True, True, None), (False, False, None), (False, True, 1)):
            owner.controller.armed = armed
            owner.identity.threads_running.return_value = running
            owner.process.poll.return_value = exited
            owner.controller.normal.reset_mock()
            with patch.object(r.sys, 'stderr', io.StringIO()):
                self.assertEqual(owner.report_failure_diagnostic()['reason'], 'unavailable')
            owner.controller.normal.send.assert_not_called()

    def test_failed_diagnostic_never_prints_exception_or_stops_cleanup(self):
        import io
        import threading
        owner = r.OwnedCIWorker(); owner.controller = controller()
        owner.controller.armed = False
        owner.controller.mutex = threading.RLock()
        owner.process = Mock(); owner.process.poll.return_value = None
        owner.identity = Mock(); owner.identity.threads_running.return_value = True
        owner.controller.normal.receive.side_effect = RuntimeError('private SQL secret')
        with patch.object(r.sys, 'stderr', io.StringIO()) as output:
            result = owner.report_failure_diagnostic()
            self.assertNotIn('private', output.getvalue())
        self.assertEqual(result, {'event': 'ci_worker_admission_failure',
                                 'reason': 'unavailable', 'phase': 'unavailable'})
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        self.assertIn('if sys.exc_info()[0] is not None and owned_ci_worker is not None:', source)
        self.assertLess(source.index('owned_ci_worker.report_failure_diagnostic()'),
                        source.index('diagnostics.snapshot(server, worker)'))

    def test_control_failure_records_real_operation_step_and_enum_cause(self):
        def failure(cause):
            try:
                r._fail(cause)
            except r.ControlFailure as error:
                return error
        for operation in ('snapshot', 'pin'):
            for method, step, cause in (('send', 'send', r.ControlCause.DEADLINE),
                                        ('receive', 'receive', r.ControlCause.EOF),
                                        ('receive', 'receive', r.ControlCause.PARTIAL),
                                        ('receive', 'receive', r.ControlCause.TRANSPORT)):
                with self.subTest(operation=operation, step=step, cause=cause):
                    c = controller()
                    getattr(c.normal, method).side_effect = failure(cause)
                    with self.assertRaises(r.ControlFailure) as caught:
                        (c.snapshot(123.0) if operation == 'snapshot' else c.pin(7, 123.0))
                    self.assertEqual(caught.exception.control_diagnostic,
                                     {'operation': operation, 'step': step, 'cause': cause.value})
                    self.assertTrue(c.invalid)
                    self.assertEqual(c.normal.send.call_args.args[1], 123.0)
                    self.assertEqual(c.normal.send.call_count, 1)
        c = controller(); c.armed = False
        c.supervisor.receive.side_effect = failure(r.ControlCause.DEADLINE)
        with self.assertRaises(r.ControlFailure) as caught:
            c.arm(123.0)
        self.assertEqual(caught.exception.control_diagnostic,
                         {'operation': 'arm', 'step': 'receive', 'cause': 'deadline'})
        c.supervisor.send.assert_called_once()
        c = controller(); c.normal.receive.return_value = frame() | {'private': 'SQL identity'}
        with self.assertRaises(r.ControlFailure) as caught:
            c.snapshot(123.0)
        self.assertEqual(caught.exception.control_diagnostic,
                         {'operation': 'snapshot', 'step': 'schema', 'cause': 'frame'})

    def test_control_failure_never_copies_text_subclass_or_unbounded_cause(self):
        class Forged(r.ControlFailure):
            pass
        for error in (r.ControlFailure('private SQL'), Forged('private SQL')):
            for raw in ('private' * 10000, {'private': 'identity'}, r.ControlCause.EOF):
                error.control_cause = raw
                c = controller(); c.normal.receive.side_effect = error
                with self.assertRaises(r.ControlFailure) as caught:
                    c.snapshot(123.0)
                cause = 'eof' if type(error) is r.ControlFailure and raw is r.ControlCause.EOF else 'other'
                self.assertEqual(caught.exception.control_diagnostic,
                                 {'operation': 'snapshot', 'step': 'receive', 'cause': cause})
                self.assertEqual(caught.exception.args, ('owned CI worker control unavailable',))
        with self.assertRaises(r.ControlFailure) as caught:
            r._fail('private arbitrary input')
        self.assertIs(caught.exception.control_cause, r.ControlCause.OTHER)
        self.assertNotIn('private', str(caught.exception))
        with self.assertRaises(TypeError):
            r.CONTROL_DIAGNOSTIC_VALUES['cause'] = frozenset({'private'})

    def test_transport_cause_comes_from_failure_branch_not_later_clock(self):
        for chunks, cause in (([b''], 'eof'), ([b'{', b''], 'partial')):
            c = self.channel()
            with patch.object(c, '_ready'), patch.object(r.os, 'read', side_effect=chunks), \
                    patch.object(r.time, 'monotonic', return_value=0):
                with self.assertRaises(r.ControlFailure) as caught:
                    c.receive(1)
            self.assertEqual(caught.exception.control_cause.value, cause)
        c = self.channel()
        with patch.object(r.time, 'monotonic', return_value=2):
            with self.assertRaises(r.ControlFailure) as caught:
                c.receive(1)
        self.assertIs(caught.exception.control_cause, r.ControlCause.DEADLINE)
        c = controller(); c.normal.drain.return_value = []
        with patch.object(r.time, 'monotonic', return_value=2):
            with self.assertRaises(r.ControlFailure) as caught:
                c.after_stop(1)
        self.assertEqual(caught.exception.control_diagnostic,
                         {'operation': 'after_stop', 'step': 'state', 'cause': 'deadline'})
        c.normal.send.assert_not_called()

    def test_exact_runner_delta_and_mutations(self):
        import hashlib
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        self.assertEqual(hashlib.sha256(restore_owned_runner(source).encode()).hexdigest(), BASE_RUNNER_SHA256)
        for _, new in RUNNER_DELTAS:
            with self.assertRaises(ValueError):
                restore_owned_runner(source + new)
            with self.assertRaises(ValueError):
                restore_owned_runner(source.replace(new, '!' + new[1:]))

    def test_owned_worker_excludes_ambient_pgx_inputs(self):
        env = {'PGHOST': 'external', 'PGOPTIONS': 'private', 'PGSERVICE': 'external',
               'PGSERVICEFILE': '/private', 'PGPASSFILE': '/private', 'PGSSLMODE': 'require',
               'ADTR_DATABASE_URL': 'explicit-isolated-test-dsn',
               'ADTR_CI_DB_NORMAL_FD': '999', 'PATH': '/fixed'}
        child = r.owned_worker_environment(env, (30, 31, 32, 33))
        self.assertFalse(any(key.startswith('PG') for key in child))
        self.assertEqual(child['ADTR_DATABASE_URL'], env['ADTR_DATABASE_URL'])
        self.assertEqual([child[key] for key in r.ENV_FDS], ['30', '31', '32', '33'])
        self.assertIn('PGHOST', env)  # Does not mutate the caller/global environment.
        self.assertEqual(child['PATH'], '/fixed')

    def test_rescuer_environment_and_descriptor_allowlist(self):
        source = Path(r.__file__).read_text()
        self.assertIn('pass_fds=(normal_worker, rescue_worker, self.page_fd, capture_read)', source)
        self.assertLess(source.index('os.set_blocking(normal_worker, False)'), source.index('self.process = subprocess.Popen'))
        self.assertLess(source.index('os.set_blocking(rescue_worker, False)'), source.index('self.process = subprocess.Popen'))
        self.assertIn('pass_fds=(supervisor_child, rescue_parent, capture_write)', source)
        self.assertIn('env={"PATH": os.defpath, "LC_ALL": "C.UTF-8"}', source)

    def test_rescue_ack_is_closed_and_sequence_bound(self):
        channel = Mock()
        good = {'version': 2, 'epoch': 1, 'seq': 2, 'kind': 'reply', 'status': 'released'}
        for update in ({'extra': 'private'}, {'epoch': True}, {'seq': 1}, {'version': True}):
            channel.receive.return_value = good | update
            with self.assertRaises(r.ControlFailure):
                r._rescue_exchange(channel, 2, True, r.time.monotonic() + 1)
        channel.receive.return_value = good
        r._rescue_exchange(channel, 2, True, r.time.monotonic() + 1)

    def test_three_kind_provenance_includes_build_and_modules(self):
        import validate_user_asset_export_v3_evidence as user
        import validate_group_asset_export_v3_evidence as group
        import validate_computer_asset_export_v3_evidence as computer
        for module in (user, group, computer):
            self.assertTrue({'go.mod', 'go.sum', 'Makefile', 'scripts', 'internal'} <= set(module.SOURCE_ROOTS))
        root = Path(__file__).resolve().parents[1]
        for kind in ('user', 'group', 'computer'):
            helper = (root / f'web/e2e/{kind}-asset-export-v3-helpers.ts').read_text()
            self.assertIn(f'scripts/validate_{kind}_asset_export_v3_evidence.py', helper)
            self.assertIn('--capture-provenance', helper)

    def test_only_three_suites_build_and_spawn(self):
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        for node in ast.walk(tree):
            if isinstance(node, ast.If) and isinstance(node.test, ast.Compare):
                body = ast.get_source_segment(source, node)
                if 'OwnedCIWorker.spawn' in body and 'worker = owned_ci_worker.process' in body:
                    self.assertIn('{RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES', body)
        make = Path(__file__).resolve().parents[1].joinpath('Makefile').read_text()
        self.assertNotIn('-tags', make.split('build:', 1)[1].split('test:', 1)[0])
        self.assertIn('-tags=adtr_ci_dbadmission', make.split('build-ci-running-cancel:', 1)[1])


if __name__ == '__main__':
    unittest.main()
