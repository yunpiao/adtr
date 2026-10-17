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
  '            if running_cancel_guard is not None and shutdown_error is None:\n'
  '                try:\n'
  "                    # The worker's successful Wait includes journal.Close/drain.\n"
  '                    # Query its final report while the isolated database is alive.\n'
  '                    running_cancel_guard.verify_journal_after_wait(worker.returncode)\n'
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

    def test_journal_proof_must_run_after_wait_before_database_cleanup(self):
        source = Path(__file__).with_name('test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Attribute)
                 and node.func.attr == 'verify_journal_after_wait']
        self.assertEqual(len(calls), 1)
        proof = calls[0]
        self.assertEqual(ast.unparse(proof),
                         'running_cancel_guard.verify_journal_after_wait(worker.returncode)')
        wait_loop = next(node for node in ast.walk(tree) if isinstance(node, ast.For)
                         and ast.unparse(node.target) == '(label, process)')
        self.assertLess(wait_loop.end_lineno, proof.lineno)
        self.assertIn('process.wait(timeout=30)', ast.get_source_segment(source, wait_loop))
        self.assertIn('if code != 0:', ast.get_source_segment(source, wait_loop))
        cleanup = next(node for node in ast.walk(tree) if isinstance(node, ast.Call)
                       and isinstance(node.func, ast.Name) and node.func.id == 'remove_owned_container')
        self.assertLess(proof.lineno, cleanup.lineno)
        gate = next(node for node in ast.walk(tree) if isinstance(node, ast.If)
                    and ast.unparse(node.test) == 'running_cancel_guard is not None and shutdown_error is None')
        self.assertTrue(gate.lineno < proof.lineno < gate.end_lineno)
        # Execute only this isolated cleanup gate with mocks: no processes/DB.
        block = ast.Module(body=[gate], type_ignores=[])
        code = compile(ast.fix_missing_locations(block), '<journal-cleanup-contract>', 'exec')
        for prior_error, journal_error in ((None, None), (None, RuntimeError('private')),
                                            (RuntimeError('earlier cleanup'), None)):
            guard = Mock()
            guard.verify_journal_after_wait.side_effect = journal_error
            scope = {'running_cancel_guard': guard, 'worker': Mock(returncode=0),
                     'shutdown_error': prior_error}
            exec(code, scope)
            if prior_error is None:
                guard.verify_journal_after_wait.assert_called_once_with(0)
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
        child = r.owned_worker_environment(env, (30, 31, 32))
        self.assertFalse(any(key.startswith('PG') for key in child))
        self.assertEqual(child['ADTR_DATABASE_URL'], env['ADTR_DATABASE_URL'])
        self.assertEqual([child[key] for key in r.ENV_FDS], ['30', '31', '32'])
        self.assertIn('PGHOST', env)  # Does not mutate the caller/global environment.
        self.assertEqual(child['PATH'], '/fixed')

    def test_rescuer_environment_and_descriptor_allowlist(self):
        source = Path(r.__file__).read_text()
        self.assertIn('pass_fds=(normal_worker, rescue_worker, self.page_fd)', source)
        self.assertLess(source.index('os.set_blocking(normal_worker, False)'), source.index('self.process = subprocess.Popen'))
        self.assertLess(source.index('os.set_blocking(rescue_worker, False)'), source.index('self.process = subprocess.Popen'))
        self.assertIn('pass_fds=(supervisor_child, rescue_parent)', source)
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
