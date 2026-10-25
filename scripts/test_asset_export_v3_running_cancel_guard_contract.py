"""Offline mocked contracts: no real process, database, socket or signal."""
import copy
import json
import io
from pathlib import Path
import signal
import unittest
from unittest.mock import Mock, patch

import asset_export_v3_running_cancel_guard as g


def lock(typ, mode, granted=True, relation=None, **extra):
    return {'type': typ, 'mode': mode, 'granted': granted, 'relation': relation,
            'classid': None, 'objid': None, 'objsubid': None, **extra}


def session(pid=100):
    return {'pid': pid, 'transaction': True, 'state': 'active', 'query': g.ACTIVITY_SQL,
            'waitType': 'Lock', 'blockers': [99], 'locks': [
                lock('advisory', 'ShareLock', classid='0', objid='734192801', objsubid=1),
                lock('relation', 'RowShareLock', relation='adtr.schema_version'),
                lock('relation', 'RowExclusiveLock', relation='adtr.system_worker_activity'),
                lock('virtualxid', 'ExclusiveLock'), lock('transactionid', 'ShareLock', False)]}


def task():
    return {'taskID': 'task-one', 'kind': 'user_assets.v3.export', 'state': 'running',
            'attempt': 1, 'progress': 10, 'resultVersion': 2, 'snapshotCount': 1, 'manifestCount': 0,
            'chunkCount': 0, 'fencingToken': 1, 'leaseOwner': 'worker-0',
            'databaseClock': 100., 'leaseUntilEpoch': 130., 'leaseRemainingSeconds': 30.}


class ContractTests(unittest.TestCase):
    def test_final_psql_carries_deadline_through_startup_query_and_close(self):
        with patch.object(g.subprocess, 'Popen'), patch.object(g.threading, 'Thread'), patch.object(g.Psql, 'query', return_value=44) as query, patch.object(g.time, 'monotonic', return_value=10):
            db = g.Psql('adtr-auth-e2e-' + 'b' * 12, True, deadline=11)
            self.assertEqual(db.deadline, 11)
            query.assert_called_once_with('SELECT to_json(pg_backend_pid())')
        db = g.Psql.__new__(g.Psql); db.deadline = 11
        db.process = Mock(); db.process.poll.return_value = None
        db.lines = Mock(); db.lines.get.return_value = '0'
        db.reader = Mock()
        with patch.object(g.time, 'monotonic', return_value=10):
            self.assertEqual(db.query('SELECT 0'), 0)
            self.assertEqual(db.lines.get.call_args.kwargs['timeout'], 1)
            db.close()
            self.assertEqual(db.process.wait.call_args.kwargs['timeout'], 1)
            self.assertEqual(db.reader.join.call_args.kwargs['timeout'], 1)
        db.process.reset_mock()
        db.process.stdin.close.side_effect = BrokenPipeError('synthetic private close failure')
        with patch.object(g.time, 'monotonic', return_value=10):
            with self.assertRaises(RuntimeError): db.close()
        db.process.wait.assert_called_once_with(timeout=1)
        db.process.stdin.close.side_effect = None
        db.process.reset_mock()
        db.process.wait.side_effect = [g.subprocess.TimeoutExpired('owned-observer', 0), 0]
        with patch.object(g.time, 'monotonic', return_value=11):
            with self.assertRaises(RuntimeError): db.close()
        db.process.kill.assert_called_once()
        self.assertEqual(db.process.wait.call_args_list[0].kwargs['timeout'], 0)
        self.assertEqual(db.process.wait.call_args_list[1].kwargs['timeout'], 2)

    def test_final_quiescence_requires_full_owned_zero_and_original_deadline(self):
        guard = self.guard(); guard.used = True; guard.failed = False
        guard.application_name = 'adtr-user-export-' + 'a' * 32
        guard.container = 'adtr-auth-e2e-' + 'a' * 12
        observer = Mock()
        observer.query.return_value = {'database': 'adtr_e2e', 'backendCount': 0}
        with patch.object(g, 'Psql', return_value=observer), patch.object(g.time, 'monotonic', return_value=10):
            self.assertTrue(guard.verify_final_quiescence(11))
        self.assertEqual(observer.query.call_args.kwargs['timeout'], 1)
        sql = observer.query.call_args.args[0]
        self.assertIn("application_name='" + guard.application_name + "'", sql)
        self.assertIn('datname=current_database()', sql)
        self.assertNotIn('pid=', sql)
        observer.close.assert_called_once()
        for proof in ({'database':'adtr_e2e','backendCount':1}, {'database':'other','backendCount':0},
                      {'database':'adtr_e2e','backendCount':False}, {'database':'adtr_e2e','backendCount':0,'extra':1}, None):
            observer.reset_mock(); observer.query.return_value = proof
            with self.subTest(proof=proof), patch.object(g, 'Psql', return_value=observer), patch.object(g.time, 'monotonic', return_value=10):
                with self.assertRaises(RuntimeError): guard.verify_final_quiescence(11)
            observer.close.assert_called_once()
        observer.query.side_effect = RuntimeError('synthetic private observation failure')
        with patch.object(g, 'Psql', return_value=observer), patch.object(g.time, 'monotonic', return_value=10):
            with self.assertRaises(RuntimeError): guard.verify_final_quiescence(11)
        for deadline in (10, float('nan'), float('inf'), True):
            with self.subTest(deadline=deadline), patch.object(g, 'Psql') as factory, patch.object(g.time, 'monotonic', return_value=10):
                with self.assertRaises(RuntimeError): guard.verify_final_quiescence(deadline)
                factory.assert_not_called()
        observer.query.side_effect = None
        observer.query.return_value = {'database':'adtr_e2e','backendCount':0}
        with patch.object(g, 'Psql', return_value=observer), patch.object(g.time, 'monotonic', side_effect=[10,10,12]):
            with self.assertRaises(RuntimeError): guard.verify_final_quiescence(11)

    def test_recovery_cleanup_requires_final_counts_and_all_owned_backends_zero(self):
        summary = dict(event='owned_ci_recovery_cleanup', captureAuthorized=True,
            released=True, settled=True, qualified=2, completed=2, pending=0,
            open=0, connectPending=0, failed=0)
        guard = self.guard(); guard.used = True; guard.failed = False
        guard.application_name = 'adtr-user-export-' + 'a' * 32
        guard.container = 'adtr-auth-e2e-' + 'a' * 12
        observer = Mock()
        observer.query.return_value = {'database': 'adtr_e2e', 'backendCount': 0}
        with patch.object(g, 'Psql', return_value=observer), patch('sys.stdout', new_callable=io.StringIO) as output:
            guard.verify_recovery_cleanup_after_wait(0, summary)
        self.assertTrue(json.loads(output.getvalue())['verified'])
        sql = observer.query.call_args.args[0]
        self.assertIn("application_name='" + guard.application_name + "'", sql)
        self.assertIn('datname=current_database()', sql)
        self.assertNotIn('pid=', sql)
        observer.close.assert_called_once()
        for bad in (None, summary | {'pending': 1}, summary | {'open': 1},
                    summary | {'connectPending': 1}, summary | {'failed': 1},
                    summary | {'qualified': 3}, summary | {'settled': False},
                    summary | {'captureAuthorized': False}, summary | {'released': False},
                    summary | {'qualified': True}, summary | {'private': 'secret'}):
            with self.subTest(summary=bad), patch.object(g, 'Psql', return_value=observer), patch('sys.stdout', new_callable=io.StringIO):
                with self.assertRaises(RuntimeError):
                    guard.verify_recovery_cleanup_after_wait(0, bad)
        for proof in ({'database': 'other', 'backendCount': 0}, {'database': 'adtr_e2e', 'backendCount': 1},
                      {'database': 'adtr_e2e', 'backendCount': False}, None, {}):
            observer.query.return_value = proof
            with patch.object(g, 'Psql', return_value=observer), patch('sys.stdout', new_callable=io.StringIO):
                with self.assertRaises(RuntimeError):
                    guard.verify_recovery_cleanup_after_wait(0, summary)
        observer.query.side_effect = RuntimeError('private SQL identity')
        with patch.object(g, 'Psql', return_value=observer), patch('sys.stdout', new_callable=io.StringIO) as output:
            with self.assertRaises(RuntimeError):
                guard.verify_recovery_cleanup_after_wait(0, summary)
        self.assertNotIn('private', output.getvalue())
        self.assertNotIn(guard.application_name, output.getvalue())

    def test_all_registry_and_checkpoint_require_messages_have_closed_reasons(self):
        import ast
        tree = ast.parse(Path(g.__file__).read_text())
        for node in ast.walk(tree):
            if isinstance(node, ast.FunctionDef) and node.name in ('candidate_identity', 'registered_candidate', 'registry_matches_pg', 'validate_sealed_registry', 'checkpoint'):
                for call in ast.walk(node):
                    if isinstance(call, ast.Call) and isinstance(call.func, ast.Name) and call.func.id == 'require':
                        message = call.args[1]
                        self.assertIsInstance(message, ast.Constant)
                        self.assertIn(message.value, g.FAILURE_REASONS)

    def test_controller_diagnostics_accept_only_exact_type_and_closed_fields(self):
        error = g.ControlFailure('private SQL credential environment')
        detail = {'operation': 'snapshot', 'step': 'receive', 'cause': 'deadline'}
        error.control_diagnostic = detail
        self.assertEqual(g.failure_event('registry_snapshot', error), {
            'event': 'running_cancel_guard_failure', 'stage': 'registry_snapshot',
            'reason': 'controller_failure', **detail})
        class Derived(g.ControlFailure):
            pass
        for value in (Derived('private'), RuntimeError('private'), ValueError('private')):
            value.control_diagnostic = detail
            result = g.failure_event('registry_snapshot', value)
            self.assertEqual(result['reason'], 'unclassified')
            self.assertNotIn('operation', result)
        for bad in (detail | {'private': 'private'}, detail | {'cause': 'x' * 100000},
                    detail | {'step': False}, {'operation': 'snapshot'}, ['private']):
            error.control_diagnostic = bad
            result = g.failure_event('registry_snapshot', error)
            self.assertEqual(result['reason'], 'unclassified')
            self.assertNotIn('private', json.dumps(result))

    def test_deadline_has_bounded_observation_count_and_last_wait(self):
        for missing_candidate in (False, True):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            if missing_candidate:
                guard.controller.snapshot.return_value['records'][0]['candidate'] = None
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': task() if missing_candidate else queued, 'sessions': [session()]}]
            barrier.query.return_value = {'count': 1}
            now = [0.]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'monotonic', side_effect=lambda: now[0]), patch.object(g.time, 'sleep', side_effect=lambda _: now.__setitem__(0, 1.5)):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            event = guard.failure_diagnostic
            self.assertEqual(event['reason'], 'capture_deadline_missed')
            self.assertEqual(event['observations'], 1)
            self.assertEqual(event['lastNotReady']['reason'], 'candidate_absent' if missing_candidate else 'task_boundary_pending')
            guard.controller.pin.assert_not_called()
            guard.owned.process.send_signal.assert_not_called()
        for count in (True, -1, 1000001, 'private', {'private': 1}):
            error = RuntimeError('missed two-second activity checkpoint')
            error.capture_observations = count
            self.assertNotIn('observations', g.failure_event('capture', error))

    def test_prepare_is_live_only_isolated_and_once(self):
        guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=['R'])
        observer = Mock(); observer.query.side_effect = [0, {'database': 'adtr_e2e', 'backendCount': 0}]
        with patch.object(g, 'Psql', return_value=observer):
            self.assertEqual(guard.prepare(), {'prepared': True})
            with self.assertRaises(RuntimeError): guard.prepare()
        guard.controller.prepare_selection.assert_called_once()
        for states, count in ((['T'], 0), (['R'], 1), (['R'], True)):
            guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=states)
            observer = Mock(); observer.query.return_value = count
            with patch.object(g, 'Psql', return_value=observer), self.assertRaises(RuntimeError): guard.prepare()
            guard.controller.prepare_selection.assert_not_called()

    def test_queued_prepare_shared_deadline_and_physical_zero(self):
        for proof in ({'database': 'adtr_e2e', 'backendCount': 1},
                      {'database': 'other', 'backendCount': 0},
                      {'database': 'adtr_e2e', 'backendCount': False},
                      {'database': 'adtr_e2e', 'backendCount': 0, 'extra': 0}):
            guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=['R'])
            observer = Mock(); observer.query.side_effect = [0, proof]
            with patch.object(g, 'Psql', return_value=observer), self.assertRaises(RuntimeError):
                guard.prepare()
            self.assertTrue(guard.failed)
            guard.controller.resume_and_release.assert_called_once_with(abort=True)
            self.assertFalse(guard.prepared)
        guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=['R'])
        observer = Mock(); observer.query.side_effect = [0, {'database': 'adtr_e2e', 'backendCount': 0}]
        with patch.object(g, 'Psql', return_value=observer) as psql, patch.object(g.time, 'monotonic', return_value=10):
            guard.prepare()
        self.assertEqual(psql.call_args.kwargs, {'deadline': 12.0})
        self.assertEqual(guard.controller.prepare_selection.call_args.args[1], 12.0)
        guard.resume()
        self.assertTrue(guard.failed)
        guard.controller.resume_and_release.assert_called_once_with(abort=True)
        with self.assertRaises(RuntimeError): guard.checkpoint('task-one', 'worker')

    def test_queued_watchdog_delegate_aborts_without_parent_cont(self):
        guard = self.guard(); guard.preparation_started = True; guard.used = False
        self.assertTrue(guard._resume_capture_owned())
        self.assertTrue(guard.failed)
        guard.controller.resume_and_release.assert_called_once_with(abort=True)
        guard.owned.process.send_signal.assert_not_called()
        guard = self.guard(); guard.preparation_started = True; guard.used = True
        self.assertTrue(guard._resume_capture_owned())
        self.assertTrue(guard.failed)
        guard.controller.resume_and_release.assert_called_once_with(abort=True)

    def test_queued_checkpoint_resume_is_unique_owned_entry(self):
        from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard
        import ast, inspect
        owned = object.__new__(OwnedExportWorkerGuard)
        owned.process = Mock(); owned.process.poll.return_value = None
        owned.resume_delegate = Mock(return_value=True)
        owned.assert_identity = Mock(return_value='T')
        owned.resume_queued_checkpoint()
        owned.process.send_signal.assert_called_once_with(g.signal.SIGCONT)
        owned.resume_delegate.assert_not_called()
        for delegate, exited, state in ((None, None, 'T'), (Mock(), 0, 'T'), (Mock(), None, 'R'), (Mock(), None, 't')):
            owned.process.reset_mock(); owned.process.poll.return_value = exited
            owned.resume_delegate = delegate; owned.assert_identity.return_value = state
            with self.assertRaises(RuntimeError): owned.resume_queued_checkpoint()
            owned.process.send_signal.assert_not_called()
        owned.process.poll.return_value = None; owned.resume_delegate = Mock()
        owned.assert_identity.side_effect = RuntimeError('identity changed')
        with self.assertRaises(RuntimeError): owned.resume_queued_checkpoint()
        owned.process.send_signal.assert_not_called()
        source = Path(g.__file__).read_text()
        calls = [n for n in ast.walk(ast.parse(source)) if isinstance(n, ast.Call) and isinstance(n.func, ast.Attribute) and n.func.attr == 'resume_queued_checkpoint']
        self.assertEqual(len(calls), 1)
        references = []
        for path in Path(g.__file__).parent.rglob('*.py'):
            if path.name.startswith('test_'):
                continue
            tree = ast.parse(path.read_text())
            parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
            for node in ast.walk(tree):
                if not isinstance(node, ast.Attribute) or node.attr != 'resume_queued_checkpoint':
                    continue
                caller = parents.get(node)
                self.assertIsInstance(caller, ast.Call)
                self.assertIs(caller.func, node)
                self.assertEqual(ast.unparse(node), 'self.owned.resume_queued_checkpoint')
                owner = caller
                while owner in parents and not isinstance(owner, ast.FunctionDef):
                    owner = parents[owner]
                references.append((path.name, owner.name))
        self.assertEqual(references, [('asset_export_v3_running_cancel_guard.py', 'checkpoint')])
        self.assertIn('self.owned.resume_queued_checkpoint()', inspect.getsource(g.RunningExportCancelGuard.checkpoint))
        self.assertIn("all(s == 'T' for s in self.threads())", inspect.getsource(g.RunningExportCancelGuard.checkpoint))

    def test_queued_prepare_close_failure_always_aborts(self):
        guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=['R'])
        observer = Mock(); observer.query.side_effect = [0, {'database': 'adtr_e2e', 'backendCount': 0}]
        observer.close.side_effect = RuntimeError('closed failure')
        with patch.object(g, 'Psql', return_value=observer), self.assertRaises(RuntimeError): guard.prepare()
        observer.close.assert_called_once()
        self.assertTrue(guard.failed)
        guard.controller.resume_and_release.assert_called_once_with(abort=True)

    def test_running_queued_helpers_require_empty_connections_and_no_retry(self):
        for kind in ('user', 'group', 'computer'):
            source = (Path(g.__file__).parent.parent / 'web/e2e' / f'{kind}-asset-export-v3-terminal-helpers.ts').read_text()
            self.assertIn('(!runningQueued || pg.connections === 0)', source)
            rejection = source.index('if (runningQueued) throw new Error("Owned running Worker queued connections were not empty")')
            self.assertLess(rejection, source.index('await signalWorker("SIGCONT"); ownedStopped = false;', rejection))
        import inspect
        self.assertIn("initial['sessions'] == []", inspect.getsource(g.RunningExportCancelGuard.checkpoint))

    def test_queued_prepare_lost_ack_aborts_without_ready(self):
        guard = self.guard(); guard.prepared = False; guard.threads = Mock(return_value=['R'])
        observer = Mock(); observer.query.return_value = 0
        guard.controller.prepare_selection.side_effect = RuntimeError('closed failure')
        with patch.object(g, 'Psql', return_value=observer), self.assertRaises(RuntimeError):
            guard.prepare()
        self.assertTrue(guard.failed)
        self.assertFalse(guard.prepared)
        guard.controller.resume_and_release.assert_called_once_with(abort=True)
        self.assertEqual(observer.query.call_count, 1)

    def test_activity_fixture_key_binding_collision_and_exact_wait_cleanup(self):
        import base64, hashlib
        guard = self.guard(); guard.activity_fixture_created = False
        expected = 'ci-activity-' + base64.b32encode(hashlib.sha256(
            b'adtr-ci-activity-v1\x00' + guard.nonce + b'\x00task-one').digest()).decode().lower().rstrip('=')
        guard.controller.prepare_target.return_value = expected
        observer = Mock(); observer.query.return_value = 1
        with patch.object(g, 'Psql', return_value=observer) as connect:
            g.RunningExportCancelGuard.prepare_activity_fixture(guard, 'task-one', 'worker')
        connect.assert_called_once_with(guard.container, False)
        observer.close.assert_called_once()
        self.assertTrue(guard.activity_fixture_created)
        insert = observer.query.call_args.args[0]
        self.assertIn("'failure','cycle_failed',NULL", insert)
        self.assertNotIn('ON CONFLICT', insert)
        self.assertEqual(guard.activity_key, expected)
        cleanup = Mock(); cleanup.query.side_effect = [1, {'remaining': 0, 'ordinary': 1}]
        guard.owned.process.poll.return_value = 1
        with patch.object(g, 'Psql', return_value=cleanup) as connect: guard.cleanup_activity_fixture_after_wait(1)
        connect.assert_called_once_with(guard.container, False)
        cleanup.close.assert_called_once()
        self.assertFalse(guard.activity_fixture_created)
        self.assertIn("tenant_id='default'", cleanup.query.call_args_list[0].args[0])
        self.assertIn("AND cycle='queue'", cleanup.query.call_args_list[0].args[0])
        for result in (0, True, RuntimeError('private collision')):
            guard.activity_fixture_created = False
            observer = Mock()
            if isinstance(result, Exception): observer.query.side_effect = result
            else: observer.query.return_value = result
            with patch.object(g, 'Psql', return_value=observer) as connect, self.assertRaises(RuntimeError):
                g.RunningExportCancelGuard.prepare_activity_fixture(guard, 'task-one', 'worker')
            connect.assert_called_once_with(guard.container, False)
            observer.close.assert_called_once()
            with patch.object(g, 'Psql') as connection:
                guard.cleanup_activity_fixture_after_wait(1)
            connection.assert_not_called()
        guard.controller.prepare_target.return_value = 'ci-wrong-nonce'
        observer = Mock()
        with patch.object(g, 'Psql') as connect, self.assertRaises(RuntimeError):
            g.RunningExportCancelGuard.prepare_activity_fixture(guard, 'task-one', 'worker')
        connect.assert_not_called()

    def test_resume_scopes_exact_original_and_alias_without_relaxing_initial_pause(self):
        root = Path(g.__file__).resolve().parents[1]
        for kind in g.EXPORT_KINDS:
            source = (root / f'web/e2e/{kind}-asset-export-v3-terminal-helpers.ts').read_text()
            pause = source.split('export async function pauseOwnedExportWorker()', 1)[1].split('export async function restoreOwnedExportWorker()', 1)[0]
            resume = source.split('export async function resumeOwnedExportWorker(', 1)[1]
            self.assertIn('await queueActivity()', pause)
            self.assertNotIn('queueActivity(scope)', pause)
            self.assertIn('expect(value.count).toBe(1)', source)
            self.assertIn('if (scope) assertActivityScope(value.scope)', source)
            self.assertIn("worker_id NOT IN ('${scope.workerID}','${scope.alias}')", source)
            self.assertIn("min(extract(epoch FROM last_success_at)) FILTER (WHERE worker_id='${scope.workerID}')", source)
            self.assertIn('activityAlias(w.nonce, activityTaskID)', resume)
            self.assertEqual(resume.count('await queueActivity(scope)'), 4)
            self.assertIn('Number(queue.success) > Number(pause.queueSuccessBefore)', resume)
            self.assertIn('Number(queue.success) > Number(first.success)', resume)
            spec = (root / f'web/e2e/{kind}-asset-export-v3-running-cancel.spec.ts').read_text()
            self.assertIn('resumeOwnedExportWorker(context, pause, taskID, pendingDatabase.databaseClock)', spec)

    def test_wrong_bound_activity_key_fails_before_pin(self):
        guard = self.guard(); guard.controller.snapshot.return_value['records'][0]['candidate']['activityKey'] = 'other-key'
        with self.assertRaises(RuntimeError): self.run_candidate_samples(guard, [[session()]])
        self.assertEqual(guard.failure_diagnostic['reason'], 'activity_alias_mismatch')
        guard.controller.pin.assert_not_called()
        guard.owned.process.send_signal.assert_not_called()

    def test_three_specs_prepare_before_initial_pause(self):
        root = Path(g.__file__).resolve().parents[1]
        for kind in g.EXPORT_KINDS:
            source = (root / 'web/e2e' / (kind + '-asset-export-v3-running-cancel.spec.ts')).read_text()
            self.assertLess(source.index('runningGuard("prepare")'), source.index('await pauseOwnedExportWorker()'))
            self.assertLess(source.index('await pauseOwnedExportWorker()'), source.index('await createExport('))

    def run_candidate_samples(self, guard, rows, frames=None, expire=False):
        guard.threads = Mock(return_value=['T'])
        running = task() | {'kind': g.export_kind(guard.asset_kind)[0]}
        queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            *[{'task': running, 'sessions': value} for value in rows]]
        barrier.query.side_effect = [{'count': 1}, True]
        if frames is not None:
            guard.controller.snapshot.side_effect = frames
        now = [0.]
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'monotonic', side_effect=lambda: now[0]), patch.object(g.time, 'sleep', side_effect=lambda _: now.__setitem__(0, 1.5 if expire else now[0])):
            return guard.checkpoint('task-one', 'worker')

    def test_early_binding_known_initialization_waits_then_direct_strict_every_kind(self):
        for kind in g.EXPORT_KINDS:
            for query in ('BEGIN', g.SCHEMA_SQL, g.GATE_SQL, '', g.ACTIVITY_SQL):
                guard = self.guard(asset_kind=kind)
                early = session() | {'query': query, 'blockers': [], 'waitType': 'Client'}
                result = self.run_candidate_samples(guard, [[early], [session()], [session()], [session()]])
                self.assertTrue(result['checkpointCaught'])
                guard.controller.pin.assert_called_once()
                guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)

    def test_indirect_candidate_only_waits_deadline_without_pin_or_stop(self):
        guard = self.guard()
        frame = copy.deepcopy(guard.controller.snapshot.return_value)
        frame.update(status='open', open=2)
        frame['records'].append({'generation': 3, 'state': 'open', 'backend': 101, 'candidate': None})
        with self.assertRaises(RuntimeError):
            self.run_candidate_samples(guard, [[session() | {'blockers': [101]}, session(101)]], [frame], expire=True)
        event = guard.failure_diagnostic
        self.assertEqual(event['reason'], 'capture_deadline_missed')
        self.assertEqual(event['lastNotReady']['blockerCategory'], 'indirect_owned_activity')
        guard.controller.pin.assert_not_called()
        guard.owned.process.send_signal.assert_not_called()

    def test_selected_candidate_cannot_disappear_change_or_hide_in_incomplete_registry(self):
        for field in ('missing', 'generation', 'ticket', 'backend', 'fencingToken', 'pending'):
            guard = self.guard()
            frame = copy.deepcopy(guard.controller.snapshot.return_value)
            changed = copy.deepcopy(frame)
            record = changed['records'][0]
            if field == 'missing': record['candidate'] = None
            elif field == 'ticket': record['generation'] += 10
            elif field == 'backend': record['backend'] += 1
            elif field == 'pending':
                record['candidate'] = None
                changed['pending'] = 1
                changed['records'].append({'generation': 3, 'state': 'pending', 'backend': 0, 'candidate': None})
            else: record['candidate'][field] += 10
            early = session() | {'query': 'BEGIN', 'blockers': []}
            with self.assertRaises(RuntimeError):
                self.run_candidate_samples(guard, [[early], [early]], [frame, changed])
            self.assertIn(guard.failure_diagnostic['reason'], ('selected_candidate_changed', 'candidate_backend_unavailable'))
            guard.controller.pin.assert_not_called()
            guard.owned.process.send_signal.assert_not_called()

    def test_candidate_unknown_sql_fatal_and_diagnostic_private(self):
        for query, category in (('private SQL secret', 'other'), ('COMMIT', 'commit'), ('ROLLBACK', 'rollback')):
            guard = self.guard()
            with self.assertRaises(RuntimeError):
                self.run_candidate_samples(guard, [[session() | {'query': query, 'blockers': []}]])
            event = guard.failure_diagnostic
            self.assertEqual(event['reason'], 'candidate_chain_mismatch')
            self.assertEqual(event['queryCategory'], category)
            self.assertNotIn('private', json.dumps(event))
            guard.controller.pin.assert_not_called()
            guard.owned.process.send_signal.assert_not_called()

    def test_candidate_sealed_regression_and_owned_cycle_fail_before_stop(self):
        guard = self.guard()
        with self.assertRaises(RuntimeError):
            self.run_candidate_samples(guard, [[session()], [session() | {'query': 'BEGIN', 'blockers': []}]])
        self.assertEqual(guard.failure_diagnostic['reason'], 'candidate_chain_mismatch')
        guard.controller.pin.assert_called_once()
        guard.owned.process.send_signal.assert_not_called()
        guard = self.guard()
        early = session() | {'query': 'BEGIN', 'blockers': []}
        with self.assertRaises(RuntimeError):
            self.run_candidate_samples(guard, [[early, session(101) | {'blockers': [102]},
                session(102) | {'blockers': [101]}]])
        self.assertEqual(guard.failure_diagnostic['reason'], 'unowned_barrier_blocker')
        guard.controller.pin.assert_not_called()
        guard.owned.process.send_signal.assert_not_called()

    def test_candidate_wait_does_not_hide_duplicate_identity_or_later_bad_sample(self):
        early = session() | {'query': 'BEGIN', 'blockers': []}
        for bad in (session(101) | {'blockers': [999]}, session(101) | {'state': 42},
                    session(101) | {'locks': [lock('unknown', 'ShareLock')]}):
            guard = self.guard()
            with self.assertRaises(RuntimeError): self.run_candidate_samples(guard, [[early, bad]])
            guard.controller.pin.assert_not_called()
        guard = self.guard()
        frame = copy.deepcopy(guard.controller.snapshot.return_value)
        record = copy.deepcopy(frame['records'][0]); record.update(generation=3, backend=101)
        record['candidate']['generation'] = 4
        frame['records'].append(record); frame['open'] = 2
        with self.assertRaises(RuntimeError):
            self.run_candidate_samples(guard, [[early, session(101)]], [frame])
        self.assertEqual(guard.failure_diagnostic['reason'], 'candidate_ambiguous')
        guard.controller.pin.assert_not_called()

    def test_selected_candidate_post_stop_begin_remains_immediate_failure(self):
        guard = self.guard()
        with self.assertRaises(RuntimeError):
            self.run_candidate_samples(guard, [[session()], [session()], [session() | {'query': 'BEGIN', 'blockers': []}]])
        self.assertEqual(guard.failure_diagnostic['reason'], 'unsafe_transaction_query')
        guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
        self.assertEqual(guard.owned.resume.call_count, 2)

    def test_candidate_diagnostic_rejects_extra_fields_subclasses_and_unknown_values(self):
        pending = g.candidate_activity_pending(session() | {'query': 'BEGIN', 'blockers': []}, [session()], 99)
        valid = pending.candidate_diagnostic
        for change in ({'transaction': 1}, {'queryCategory': 'private' * 10000}, {'extra': 'private'}):
            value = RuntimeError('candidate activity not ready'); value.candidate_diagnostic = valid | change
            self.assertNotIn('queryCategory', g.failure_event('candidate_binding', value))
        class Sub(RuntimeError): pass
        value = Sub('candidate activity not ready'); value.candidate_diagnostic = valid
        self.assertEqual(g.failure_event('candidate_binding', value)['reason'], 'unclassified')

    def test_runtime_projection_types_are_closed_and_never_freeze_whitelist(self):
        shapes = {
            'relation': lock('relation', 'AccessShareLock', relation='private_relation'),
            'extend': lock('extend', 'ExclusiveLock', relation='private_relation'),
            'page': lock('page', 'ShareLock', relation='private_relation'),
            'tuple': lock('tuple', 'ExclusiveLock', relation='private_relation'),
            'frozenid': lock('frozenid', 'ShareLock'),
            'transactionid': lock('transactionid', 'ExclusiveLock'),
            'virtualxid': lock('virtualxid', 'ExclusiveLock'),
            'spectoken': lock('spectoken', 'ExclusiveLock', objid='1'),
            'object': lock('object', 'AccessShareLock', classid='1', objid='2', objsubid=0),
            'userlock': lock('userlock', 'ExclusiveLock', classid='0', objid='4294967295', objsubid=-32768),
            'advisory': lock('advisory', 'ShareLock', classid='0', objid='1', objsubid=1),
            'applytransaction': lock('applytransaction', 'ShareLock', objid='1', objsubid=32767),
        }
        self.assertEqual(set(shapes), g.RUNTIME_LOCK_TYPES)
        for typ, value in shapes.items():
            with self.subTest(typ=typ):
                row = session(101) | {'query': 'private SQL', 'blockers': [], 'locks': [value]}
                g.validate_running_observation([session(), row], 99)
                with self.assertRaises(RuntimeError):
                    g.validate_sessions([session(), row], 99)
        for change in ({'type': 'private_unknown'}, {'mode': 'private_mode'}, {'granted': 1},
                       {'relation': 'private_relation'}, {'classid': None}, {'classid': '01'},
                       {'objid': '4294967296'}, {'objsubid': True}, {'objsubid': 32768}):
            with self.subTest(change=change):
                row = session(101) | {'blockers': [], 'locks': [shapes['object'] | change]}
                with self.assertRaises(RuntimeError) as caught:
                    g.validate_running_observation([session(), row], 99)
                self.assertNotIn('private', json.dumps(g.failure_event('capture_readiness', caught.exception)))

    def test_runtime_full_scan_does_not_hide_late_malformed_or_untrusted_rows(self):
        busy = session(101) | {'query': 'private SQL', 'blockers': [], 'locks': [
            lock('object', 'AccessShareLock', classid='1', objid='2', objsubid=0)]}
        bad_rows = [session(102) | {'state': 4}, session(102) | {'blockers': [999]},
            session(102) | {'locks': [lock('private_unknown', 'ShareLock')]},
            session(102) | {'blockers': [103]}, session(100)]
        for bad in bad_rows:
            with self.assertRaises(RuntimeError):
                g.validate_running_observation([session(), busy, bad], 99)
        with self.assertRaises(RuntimeError):
            g.validate_running_observation([session(), busy,
                session(102) | {'blockers': [103]}, session(103) | {'blockers': [102]}], 99)

    def test_owned_object_drains_after_pin_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            ready_frame = guard.controller.snapshot.return_value
            busy_frame = copy.deepcopy(ready_frame)
            busy_frame.update(status='draining', open=2)
            busy_frame['records'].append({'generation': 3, 'state': 'open', 'backend': 101, 'candidate': None})
            guard.controller.snapshot.side_effect = [busy_frame, ready_frame]
            guard.controller.pin.return_value = busy_frame
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            busy = session(101) | {'query': 'private SQL', 'blockers': [], 'locks': [
                lock('object', 'AccessShareLock', classid='1', objid='2', objsubid=0)]}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session(), busy]},
                *[{'task': running, 'sessions': [session()]} for _ in range(3)]]
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
                self.assertTrue(guard.checkpoint('task-one', 'worker')['admissionSealed'])
            guard.controller.pin.assert_called_once()
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)

    def test_pending_and_cleanup_set_mismatch_wait_before_pin(self):
        for pending in (False, True):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            frame = guard.controller.snapshot.return_value
            partial = copy.deepcopy(frame)
            if pending:
                partial['pending'] = 1
                partial['records'].append({'generation': 3, 'state': 'pending', 'backend': 0, 'candidate': None})
            guard.controller.snapshot.side_effect = [partial, frame]
            busy = session(101) | {'query': '', 'transaction': False, 'state': None, 'blockers': [], 'locks': []}
            rows = [session()] if pending else [session(), busy]
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observations = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': task(), 'sessions': rows}, *[{'task': task(), 'sessions': [session()]} for _ in range(3)]]
            def observe(*args, **kwargs):
                if len(observations) == 3:
                    guard.controller.pin.assert_not_called()
                return observations.pop(0)
            observer.query.side_effect = observe
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
                self.assertTrue(guard.checkpoint('task-one', 'worker')['checkpointCaught'])
            guard.controller.pin.assert_called_once()

    def test_sealed_cleanup_backend_waits_then_strictly_qualifies(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        busy = session(101) | {'query': 'private SQL', 'blockers': [], 'locks': [
            lock('object', 'ShareLock', classid='1', objid='2', objsubid=0)]}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session()]},
            {'task': task(), 'sessions': [session(), busy]},
            *[{'task': task(), 'sessions': [session()]} for _ in range(3)]]
        barrier.query.side_effect = [{'count': 1}, True]
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
            self.assertTrue(guard.checkpoint('task-one', 'worker')['checkpointCaught'])
        guard.controller.pin.assert_called_once()
        guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)

    def test_poststop_object_backend_fails_without_capture_retry(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        busy = session(101) | {'query': 'private SQL', 'blockers': [], 'locks': [
            lock('object', 'ShareLock', classid='1', objid='2', objsubid=0)]}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            *[{'task': task(), 'sessions': [session()]} for _ in range(2)],
            {'task': task(), 'sessions': [session(), busy]}]
        barrier.query.side_effect = [{'count': 1}, True]
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.controller.pin.assert_called_once()
        guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
        self.assertEqual(guard.owned.resume.call_count, 2)
        self.assertEqual(observer.query.call_count, 4)

    def test_registered_target_bad_chain_is_fatal_even_without_any_waiter(self):
        for update in ({'blockers': []}, {'query': 'BEGIN'}, {'transaction': False}):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': task(), 'sessions': [session() | update]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.controller.pin.assert_not_called()
            guard.owned.process.send_signal.assert_not_called()

    def test_target_object_is_fatal_before_pin(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        target = session()
        target['locks'].append(lock('object', 'AccessShareLock', classid='1', objid='2', objsubid=0))
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [target]}]
        barrier.query.return_value = {'count': 1}
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.controller.pin.assert_not_called()
        guard.owned.process.send_signal.assert_not_called()

    def test_pinned_object_still_open_expires_without_stop(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        frame = guard.controller.snapshot.return_value
        frame.update(status='draining', open=2)
        frame['records'].append({'generation': 3, 'state': 'closing', 'backend': 101, 'candidate': None})
        busy = session(101) | {'blockers': [], 'locks': [lock('object', 'ShareLock', classid='1', objid='2', objsubid=0)]}
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session(), busy]}]
        barrier.query.return_value = {'count': 1}
        now = [0.]
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'monotonic', side_effect=lambda: now[0]), patch.object(g.time, 'sleep', side_effect=lambda _: now.__setitem__(0, 1.5)):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.controller.pin.assert_called_once()
        guard.owned.process.send_signal.assert_not_called()
        self.assertEqual(guard.failure_diagnostic['lastNotReady']['reason'], 'registry_draining')

    def test_blocker_diagnostics_distinguish_three_fatal_paths(self):
        cases = {
            'outside_owned_sample': [session() | {'blockers': [999]}],
            'cyclic_owned_graph': [session() | {'blockers': [101]}, session(101) | {'blockers': [100]}],
            'strict_barrier_chain': [session(), session(101) | {'query': g.SCHEMA_SQL,
                'blockers': [102], 'locks': []}, session(102) | {'query': '', 'transaction': False,
                'state': 'idle', 'waitType': None, 'blockers': [], 'locks': []}],
        }
        for check, rows in cases.items():
            with self.subTest(check=check), self.assertRaisesRegex(RuntimeError,
                    '^unowned or cyclic barrier blocker$') as caught:
                g.freeze_readiness(rows, 99)
            self.assertEqual(g.failure_event('capture_readiness', caught.exception), {
                'event': 'running_cancel_guard_failure', 'stage': 'capture_readiness',
                'reason': 'unowned_barrier_blocker', 'blockerCheck': check})
        self.assertNotIn('unowned or cyclic barrier blocker', g.FREEZE_NOT_READY)
        self.assertIsNone(g.validate_session_observation(cases['strict_barrier_chain'], 99))
        with self.assertRaisesRegex(RuntimeError, 'unowned or cyclic'):
            g.validate_sessions(cases['strict_barrier_chain'], 99)

    def test_blocker_diagnostic_privacy_and_exact_reason_binding(self):
        for detail in ({'blockerCheck': 'private SQL PID secret'},
                {'blockerCheck': 'outside_owned_sample', 'pid': 999},
                {'blockerCheck': []}, {'blockerCheck': None}, 'private'):
            error = RuntimeError('unowned or cyclic barrier blocker'); error.blocker_diagnostic = detail
            self.assertEqual(set(g.failure_event('capture_readiness', error)), {'event', 'stage', 'reason'})
        for error in (RuntimeError('private'), ValueError('unowned or cyclic barrier blocker'),
                      RuntimeError('unowned or cyclic barrier blocker', 'private')):
            error.blocker_diagnostic = {'blockerCheck': 'strict_barrier_chain'}
            self.assertNotIn('blockerCheck', g.failure_event('capture_readiness', error))
        error = RuntimeError('unowned or cyclic barrier blocker')
        with patch.object(g, 'validate_sessions', side_effect=error):
            with self.assertRaises(RuntimeError) as caught:
                g.freeze_readiness([session()], 99)
        self.assertIs(caught.exception, error)
        self.assertEqual(error.blocker_diagnostic, {'blockerCheck': 'strict_barrier_chain'})

    def test_blocker_diagnostics_preserve_strict_guard_function_bytes(self):
        import ast
        import hashlib
        source = Path(g.__file__).read_text()
        # The sole checkpoint delta is the diagnostic wrapper at the existing
        # strict call. Restore that exact call before checking the prior bytes.
        wrapped = '                        validate_candidate_strict(target, lock.pid)'
        self.assertEqual(source.count(wrapped), 1)
        source = source.replace(wrapped, '                        validate_sessions([target], lock.pid)', 1)
        # Remove only the exact diagnostic-only wrapper; retain the original
        # strict-body hash and verify the full wrapper before normalization.
        tuple_wrapper = "                try:\n                    require(kind == 'activity' and lock['relation'] == 'adtr.system_worker_activity'\n                            and mode in ('ExclusiveLock', 'AccessExclusiveLock') and\n                            session['pid'] in reachable, 'unsafe tuple lock')\n                except RuntimeError as error:\n                    # Classify only the exact rejected lock and original sample.\n                    # Preserve the predicate, exception and fail-closed path.\n                    try:\n                        checks = (kind == 'activity',\n                            lock['relation'] == 'adtr.system_worker_activity',\n                            mode in ('ExclusiveLock', 'AccessExclusiveLock'),\n                            session['pid'] in reachable, session['waitType'] == 'Lock')\n                        error.tuple_diagnostic = {key: 'matched' if value else 'rejected'\n                            for key, value in zip(TUPLE_FAILURE_VALUES, checks)}\n                    except Exception:\n                        pass  # Diagnostic failure cannot replace strict rejection.\n                    raise"
        tuple_original = "                require(kind == 'activity' and lock['relation'] == 'adtr.system_worker_activity'\n                        and mode in ('ExclusiveLock', 'AccessExclusiveLock') and\n                        session['pid'] in reachable, 'unsafe tuple lock')"
        self.assertEqual(source.count(tuple_wrapper), 1)
        source = source.replace(tuple_wrapper, tuple_original, 1)
        # Exact diagnostic wrapper removal preserves the same original hash;
        # any predicate/body or wrapper edit still fails the source contract.
        transaction_wrapper = "                try:\n                    require((mode == 'ExclusiveLock' and lock['granted']) or\n                            (kind == 'activity' and mode == 'ShareLock' and not lock['granted'] and\n                             session['pid'] in reachable), 'unsafe transaction lock')\n                except RuntimeError as error:\n                    # Only the exact rejected lock and existing graph/sample.\n                    # Projection must not replace the original strict exception.\n                    try:\n                        if (type(error) is RuntimeError and error.args == ('unsafe transaction lock',)\n                                and type(lock['granted']) is bool):\n                            wait = session['waitType']\n                            error.transaction_lock_diagnostic = {\n                                'modeCategory': mode if type(mode) is str and mode in TRANSACTION_LOCK_FAILURE_VALUES['modeCategory'] else 'other',\n                                'queryKind': kind if type(kind) is str and kind in TRANSACTION_LOCK_FAILURE_VALUES['queryKind'] else 'unclassified',\n                                'waitTypeCategory': ('none' if wait is None else wait if type(wait) is str and wait in TRANSACTION_LOCK_FAILURE_VALUES['waitTypeCategory'] else 'other'),\n                                'granted': lock['granted'],\n                                'directBarrier': lock_pid in session['blockers'],\n                                'otherBlockers': any(pid != lock_pid for pid in session['blockers']),\n                                'barrierReachable': session['pid'] in reachable,\n                            }\n                    except Exception:\n                        pass  # Diagnostic failure cannot replace strict rejection.\n                    raise"
        transaction_original = "                require((mode == 'ExclusiveLock' and lock['granted']) or\n                        (kind == 'activity' and mode == 'ShareLock' and not lock['granted'] and\n                         session['pid'] in reachable), 'unsafe transaction lock')"
        self.assertEqual(source.count(transaction_wrapper), 1)
        source = source.replace(transaction_wrapper, transaction_original, 1)
        expected = {
            'validate_sessions': '4efb9b60ec57914d43c55c695e0916b07d71fbcd3f3cdac8c3cd06b630bbd092',
            'validate_checkpoint': '83ff1b7a711627ec7ee4e28761dde75f5e18ae3e81abb39927b8057022a06e74',
            'checkpoint': '4ec2efbe87439fde416066a5aa3cf757166ab7468a9946fbab2c533d58408cb7',
        }
        actual = {node.name: hashlib.sha256(ast.get_source_segment(source, node).encode()).hexdigest()
                  for node in ast.walk(ast.parse(source)) if isinstance(node, ast.FunctionDef)
                  and node.name in expected}
        self.assertEqual(actual, expected)

    def speculative_session(self, pid=101, **lock_fields):
        return session(pid) | {'query': 'synthetic private SQL', 'blockers': [],
            'locks': [lock('spectoken', 'ExclusiveLock', objid='1') | lock_fields]}

    def test_speculative_projection_is_pending_never_strictly_safe(self):
        for mode in ('ExclusiveLock', 'ShareLock'):
            for granted in (True, False):
                for token in ('1', '4294967295'):
                    row = self.speculative_session(mode=mode, granted=granted, objid=token)
                    rows = [session(), row]
                    pending = g.freeze_readiness(rows, 99)
                    self.assertEqual(str(pending), 'owned speculative insertion in progress')
                    self.assertEqual(g.failure_event('capture_readiness', pending)['reason'],
                                     'speculative_insert_in_progress')
                    with self.assertRaises(RuntimeError):
                        g.validate_sessions(rows, 99)
                    known = row | {'query': g.ACTIVITY_SQL, 'blockers': [99]}
                    with self.assertRaisesRegex(RuntimeError, '^unrecognized owned lock$'):
                        g.validate_sessions([known], 99)
        # Activity statistics need not be synchronous with the lock snapshot.
        row = self.speculative_session() | {'transaction': False, 'state': None}
        self.assertIsNotNone(g.freeze_readiness([session(), row], 99))

    def test_speculative_malformed_projection_still_fails(self):
        mutations = [{'mode': 'AccessShareLock'}, {'mode': 'private'}, {'relation': 'private'},
            {'classid': '0'}, {'objsubid': 1}, {'objid': None}, {'objid': ''}, {'objid': '0'},
            {'objid': '-1'}, {'objid': '+1'}, {'objid': '01'}, {'objid': '4294967296'},
            {'objid': '9' * 100}, {'objid': '١'}, {'objid': 1}, {'granted': 'true'}]
        for update in mutations:
            with self.subTest(update=update), self.assertRaises(RuntimeError):
                g.freeze_readiness([session(), self.speculative_session(**update)], 99)

    def test_speculative_sample_does_not_hide_fatal_later_observations(self):
        for update in ({'pid': True}, {'pid': 101}, {'query': None}, {'locks': [{}]},
                {'locks': [lock('private', 'ShareLock')]}, {'locks': [lock('object', 'ShareLock')]},
                {'blockers': [999]}, {'blockers': [102]}, {'state': 'private'}):
            with self.subTest(update=update), self.assertRaises(RuntimeError):
                g.freeze_readiness([self.speculative_session(), session(102) | update], 99)
        with self.assertRaisesRegex(RuntimeError, 'unowned or cyclic'):
            g.freeze_readiness([self.speculative_session(), session(102) | {'blockers': [103]},
                                session(103) | {'blockers': [102]}], 99)

    def test_speculative_transient_then_ready_single_stop_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session(), self.speculative_session()]},
                {'task': running, 'sessions': [session()]},
                {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session()]}]
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
                self.assertTrue(guard.checkpoint('task-one', 'worker')['checkpointCaught'])
            self.assertEqual(observer.query.call_count, 5)
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_speculative_persistent_deadline_zero_stop_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session(), self.speculative_session()]}]
            barrier.query.return_value = {'count': 1}
            now = [0.0]
            def sleep(_):
                now[0] = 1.5
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time,
                    'monotonic', side_effect=lambda: now[0]), patch.object(g.time, 'sleep', side_effect=sleep):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_not_called()
            self.assertEqual(guard.failure_diagnostic['reason'], 'capture_deadline_missed')
            self.assertEqual(guard.failure_diagnostic['lastNotReady'], {
                'event': 'running_cancel_guard_failure', 'stage': 'capture',
                'reason': 'registry_observation_incomplete'})
            self.assertNotIn('private', json.dumps(guard.failure_diagnostic))
            self.assertEqual(guard.owned.resume.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_speculative_after_stop_fails_and_continues_without_retry_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session()]},
                {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session(), self.speculative_session()]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            self.assertEqual(observer.query.call_count, 4)
            self.assertEqual(guard.owned.resume.call_count, 2)
            self.assertEqual(guard.failure_diagnostic['reason'], 'speculative_insert_in_progress')
            self.assertEqual(guard.failure_diagnostic['stage'], 'session_validation')
            self.assertIsNone(guard.stopped_at)
            observer.close.assert_called_once(); barrier.close.assert_called_once()
            self.assertEqual(barrier.query.call_count, 1)

    def test_speculative_cannot_hide_candidate_identity_or_lease_failure(self):
        for update in ({'taskID': 'wrong'}, {'kind': 'wrong'}, {'leaseRemainingSeconds': 20},
                       {'leaseOwner': 'wrong'}, {'attempt': 2}, {'chunkCount': 1}):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': task() | update, 'sessions': [session(), self.speculative_session()]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            self.assertEqual(guard.failure_diagnostic['stage'], 'checkpoint_validation')
            guard.owned.process.send_signal.assert_not_called()
            self.assertEqual(observer.query.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_four_activity_slots_are_checked(self):
        safe, blocked, transactions = g.validate_sessions([session(n) for n in range(100, 104)], 99)
        self.assertEqual((len(safe), blocked, transactions), (4, 4, 4))
        self.assertNotIn('query', safe[0])
        self.assertEqual(safe[0]['queryKind'], 'activity')

    def test_four_slot_tuple_wait_chain(self):
        rows = [session(n) for n in range(100, 104)]
        for n in range(1, 4):
            rows[n]['blockers'] = [99 + n]
            rows[n]['locks'][-1] = lock('tuple', 'ExclusiveLock', False, 'adtr.system_worker_activity')
        self.assertEqual(g.validate_sessions(rows, 99)[1], 4)
        for blockers in [[999], [103]]:
            broken = copy.deepcopy(rows); broken[0]['blockers'] = blockers
            with self.assertRaises(RuntimeError):
                g.validate_sessions(broken, 99)

    def test_duplicate_backend_refused(self):
        with self.assertRaises(RuntimeError):
            g.validate_sessions([session(), session()], 99)

    def test_task_running_alone_is_insufficient(self):
        for field, value in [('progress', 0), ('snapshotCount', 0), ('attempt', 2),
                             ('state', 'cancel_requested'), ('manifestCount', 1),
                             ('chunkCount', 1), ('fencingToken', 0), ('leaseOwner', 'worker-4'),
                             ('leaseOwner', 'other-0'), ('kind', 'computer_assets.v3.export'),
                             ('leaseRemainingSeconds', 20), ('leaseUntilEpoch', 140)]:
            with self.subTest(field=field, value=value):
                row = task(); row[field] = value
                with self.assertRaises(RuntimeError):
                    g.validate_checkpoint(row, 'task-one', 'worker')

    def test_all_four_lease_owners_and_private_identity_hash(self):
        for slot in range(4):
            row = task(); row['leaseOwner'] = f'worker-{slot}'
            safe = g.validate_checkpoint(row, 'task-one', 'worker')
            self.assertNotIn('leaseOwner', safe)
            self.assertEqual(safe['leaseOwnerSHA256'], g.digest(f'worker-{slot}'))

    def test_each_unsafe_session_rejected_even_after_valid_session(self):
        mutations = [
            lambda s: s.update(query='SELECT * FROM adtr.tasks FOR UPDATE'),
            lambda s: s['locks'].append(lock('relation', 'RowShareLock', relation='adtr.tasks')),
            lambda s: s['locks'].append(lock('relation', 'AccessShareLock', relation='adtr.access_users')),
            lambda s: s['locks'].append(lock('advisory', 'ExclusiveLock', classid='3', objid='1', objsubid=1)),
            lambda s: s['locks'].append(lock('advisory', 'ShareLock', classid='0', objid='734192801', objsubid=2)),
            lambda s: s['locks'].append(lock('page', 'ShareLock')),
            lambda s: s.update(query=g.SCHEMA_SQL),
            lambda s: s.update(waitType='Client'),
            lambda s: s.update(transaction=False),
        ]
        for change in mutations:
            with self.subTest(change=change):
                bad = session(101); change(bad)
                with self.assertRaises(RuntimeError):
                    g.validate_sessions([session(), bad], 99)

    def test_no_barrier_blocker_rejected(self):
        value = session(); value['blockers'] = []
        with self.assertRaises(RuntimeError):
            g.validate_sessions([value], 99)
        with self.assertRaises(RuntimeError):
            g.validate_sessions([], 99)

    def test_idle_session_needs_no_transaction_and_no_locks(self):
        idle = {'pid': 105, 'transaction': False, 'state': 'idle', 'query': 'COMMIT',
                'waitType': 'Client', 'blockers': [], 'locks': []}
        self.assertEqual(len(g.validate_sessions([session(), idle], 99)[0]), 2)
        idle['locks'] = [lock('virtualxid', 'ExclusiveLock')]
        with self.assertRaises(RuntimeError):
            g.validate_sessions([session(), idle], 99)

    def test_schema_only_transaction_allowed_with_verified_locks(self):
        schema = session(105)
        schema.update(query=g.SCHEMA_SQL, blockers=[], waitType=None)
        schema['locks'] = schema['locks'][:2]
        self.assertEqual(g.validate_sessions([session(), schema], 99)[2], 2)

    def test_identity_injection_rejected(self):
        for value in ['', "task' OR true --", 'x'*129, 123]:
            with self.assertRaises(RuntimeError):
                g.identifier(value)

    def guard(self, **options):
        with patch.object(g, 'OwnedExportWorkerGuard') as factory:
            guard = g.RunningExportCancelGuard(Mock(), 'a'*32, '/fake/bin/adtr',
                'adtr-auth-e2e-'+'a'*12, 'adtr-user-export-'+'a'*32, 'postgres://localhost/adtr_e2e', **options)
        identity = {key: task()[key] for key in ('taskID', 'kind', 'attempt', 'leaseOwner', 'fencingToken', 'resultVersion')}
        identity.update(activityKey='ci-fixture-key', kind=g.export_kind(options.get('asset_kind', 'user'))[0], generation=2, phase='first-fenced-progress-10')
        frame = {'status': 'sealed', 'target': 2, 'pending': 0, 'open': 1,
                 'records': [{'generation': 1, 'state': 'open', 'backend': 100, 'candidate': identity}]}
        guard.prepared = True
        guard.activity_key = "ci-fixture-key"
        guard.prepare_activity_fixture = Mock()
        guard.controller = Mock()
        guard.controller.snapshot.return_value = frame
        guard.controller.pin.return_value = frame
        guard.controller.after_stop.return_value = True
        guard.controller.commit_capture.return_value = True
        guard.controller.armed = False
        guard.owned = factory.return_value
        # Shared sequence spy preserves old event-order assertions; the real
        # dedicated entry is separately exercised below.
        guard.owned.resume_queued_checkpoint = guard.owned.resume
        guard.owned.assert_identity = Mock(return_value="T")
        return guard

    def test_registry_binding_requires_every_authoritative_field(self):
        guard = self.guard()
        frame = guard.controller.snapshot.return_value
        self.assertEqual(g.registered_candidate(frame, task(), [session()], 99), (2, 100))
        for key, value in (('taskID', 'wrong'), ('kind', 'wrong'), ('attempt', 2),
                           ('leaseOwner', 'worker-1'), ('fencingToken', 2), ('resultVersion', 3)):
            changed = copy.deepcopy(frame)
            changed['records'][0]['candidate'][key] = value
            self.assertIsNone(g.registered_candidate(changed, task(), [session()], 99))
        for update in ({'query': 'BEGIN'}, {'blockers': []}, {'transaction': False}):
            with self.assertRaises(RuntimeError):
                g.registered_candidate(frame, task(), [session() | update], 99)
        duplicate = copy.deepcopy(frame)
        duplicate['records'] *= 2
        with self.assertRaises(RuntimeError):
            g.registered_candidate(duplicate, task(), [session()], 99)
        for rows in ([], [session(), session(101)], [session(101)]):
            with self.assertRaises(RuntimeError):
                g.validate_sealed_registry(frame, (2, 100), rows)
        for update in ({'pending': 1}, {'open': 2}, {'status': 'draining'}, {'target': 3}):
            with self.assertRaises(RuntimeError):
                g.validate_sealed_registry(frame | update, (2, 100), [session()])

    def test_stopped_witness_failure_continues_before_rescue_release(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session()]}, {'task': task(), 'sessions': [session()]}]
        barrier.query.return_value = {'count': 1}
        events = []
        guard.owned.resume.side_effect = lambda: events.append('cont')
        guard.controller.resume_and_release.side_effect = lambda **kw: events.append(('release', kw['abort']))
        guard.controller.after_stop.side_effect = RuntimeError('private frame must not escape')
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaisesRegex(RuntimeError, '^running checkpoint guard failed closed$'):
                guard.checkpoint('task-one', 'worker')
        self.assertEqual(events, ['cont', 'cont', ('release', True)])
        self.assertNotIn('private', json.dumps(guard.failure_diagnostic))
        self.assertEqual(observer.query.call_count, 3)
        guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)

    def test_no_controller_cannot_fall_back_to_legacy_capture(self):
        guard = self.guard(); guard.controller = None
        guard.threads = Mock(return_value=['T'])
        with patch.object(g, 'Psql') as pg, self.assertRaises(RuntimeError):
            guard.checkpoint('task-one', 'worker')
        pg.assert_not_called()
        guard.owned.process.send_signal.assert_not_called()
        guard.owned.resume.assert_called_once()

    def journal_specimen(self):
        process = '12345678-1234-4123-8123-123456789012'
        final = {'event': 'owned_ci_final_journal', 'processIDSHA256': g.digest(process),
            'closeOK': True, 'acceptanceStopped': True, 'accepted': 20, 'acknowledged': 20,
            'rejected': 0, 'queueFull': 0, 'writeFailures': 0, 'unacknowledged': 0, 'abandoned': 0}
        proof = {'count': 5, 'processCount': 1, 'closed': True, 'reportConsistent': True,
                 'terminal': True, 'processID': process, 'eventCount': 20}
        return final, proof

    def test_actual_journal_check_requires_closed_delivery_without_identity_output(self):
        guard = self.guard(); guard.used = True; guard.journal_baseline = 4; guard.checkpoint_task_id = "task-one"
        final, proof = self.journal_specimen()
        observer = Mock(); observer.query.return_value = proof
        with patch.object(g, 'Psql', return_value=observer), patch('builtins.print') as output:
            guard.verify_journal_after_wait(0, final)
        observer.query.assert_called_once_with(g.JOURNAL_PROOF_SQL.format(task_id="task-one"))
        event = json.loads(output.call_args.args[0])
        self.assertEqual(event, {'event': 'running_cancel_journal_verified',
            'newQueueProgressEvents': 1, 'businessFieldsClosed': True, 'metadataFieldsClosed': True, 'finalDeliveryVerified': True})
        for update in ({'count': 4}, {'count': 6}, {'processCount': 2}, {'closed': False},
                       {'count': True}, {'candidate': 'private'}, {'reportConsistent': False}, {'terminal': False},
                       {'eventCount': 19}, {'eventCount': 21}, {'processID': '22345678-1234-4123-8123-123456789012'}):
            observer = Mock(); observer.query.return_value = proof | update
            with patch.object(g, 'Psql', return_value=observer), patch('builtins.print') as output, self.assertRaises(RuntimeError):
                guard.verify_journal_after_wait(0, final)
            observer.close.assert_called_once()
            event = json.loads(output.call_args.args[0])
            self.assertEqual(event['event'], 'running_cancel_journal_failure')
            self.assertNotIn(proof['processID'], json.dumps(event))
            self.assertNotIn(final['processIDSHA256'], json.dumps(event))

    def test_journal_final_witness_missing_bad_or_incomplete_always_fails(self):
        final, proof = self.journal_specimen()
        changes = [None, {}, final | {'closeOK': False}, final | {'acceptanceStopped': False},
            final | {'acknowledged': 19}, final | {'accepted': 21}, final | {'processIDSHA256': 'f'*64},
            final | {'processIDSHA256': 'private'}, final | {'extra': 'private'}]
        changes += [final | {key: 1} for key in ('rejected', 'queueFull', 'writeFailures', 'unacknowledged', 'abandoned')]
        changes += [final | {'accepted': value} for value in (True, -1, 2**63, '20')]
        for record in changes:
            guard = self.guard(); guard.used = True; guard.journal_baseline = 4; guard.checkpoint_task_id = 'task-one'
            observer = Mock(); observer.query.return_value = proof
            with patch.object(g, 'Psql', return_value=observer), patch('builtins.print') as output, self.assertRaises(RuntimeError):
                guard.verify_journal_after_wait(0, record)
            event = json.loads(output.call_args.args[0])
            self.assertFalse(all(event['checks'].values()))
            self.assertNotIn('private', json.dumps(event))
            self.assertNotIn('processIDSHA256', json.dumps(event))

    def test_journal_observer_read_and_close_failures_remain_closed(self):
        final, proof = self.journal_specimen()
        for failing in ('open', 'read', 'close'):
            guard = self.guard(); guard.used = True; guard.journal_baseline = 4; guard.checkpoint_task_id = 'task-one'
            observer = Mock(); observer.query.return_value = proof
            if failing == 'read': observer.query.side_effect = RuntimeError('private SQL')
            if failing == 'close': observer.close.side_effect = RuntimeError('private process')
            with patch.object(g, 'Psql', side_effect=RuntimeError('private config') if failing == 'open' else None,
                              return_value=observer), patch('builtins.print') as output, self.assertRaises(RuntimeError):
                guard.verify_journal_after_wait(0, final)
            event = json.loads(output.call_args.args[0])
            self.assertFalse(event['checks']['observation'])
            self.assertNotIn('private', json.dumps(event))

    def test_final_journal_real_pg_case_is_selected_and_default_does_not_take_stats(self):
        root = Path(g.__file__).resolve().parents[1]
        source = (root / 'cmd/adtr/db_journal_integration_test.go').read_text()
        self.assertIn('func TestActivityCandidateFinalJournalActualRecorderAndPostgres', source)
        self.assertIn('acknowledged != 2', source)
        self.assertIn('proof.Acknowledged != 3', source)
        runner = (root / 'scripts/run_integration.py').read_text()
        self.assertIn('"^TestActivityCandidate"', runner)
        self.assertIn('"./cmd/adtr"', runner)
        default = (root / 'cmd/adtr/db_journal_default.go').read_text()
        self.assertNotIn('.Stats(', default)
        self.assertNotIn('Stderr', default)

    def test_resume_records_real_monotonic_duration(self):
        guard = self.guard(); guard.stopped_at = 100.
        with patch.object(g.time, 'monotonic', return_value=107.):
            result = guard.resume()
        self.assertEqual(result, {'resumed': True, 'runningStoppedSeconds': 7.,
            'watchdogExpired': False, 'stopStartedMonotonic': 100., 'resumedMonotonic': 107.})
        guard.owned.resume.assert_called_once()
        self.assertIsNone(guard.stopped_at)

    def test_overlong_stop_fails_after_cont(self):
        guard = self.guard(); guard.stopped_at = 100.
        with patch.object(g.time, 'monotonic', return_value=110.):
            self.assertTrue(guard.resume()['watchdogExpired'])
        guard.owned.resume.assert_called_once()
        with self.assertRaises(RuntimeError):
            guard.check()

    def test_watchdog_cont_at_nine_seconds(self):
        guard = self.guard(); guard.stopped_at = 100.
        guard._done = Mock(); guard._done.wait.side_effect = [False, True]
        with patch.object(g.time, 'monotonic', return_value=109.):
            guard._watch()
        guard.owned.resume.assert_called_once()
        self.assertTrue(guard.failed)
        self.assertIsNone(guard.stopped_at)

    def test_close_resumes_before_shutdown_even_failure(self):
        guard = self.guard(); guard.failed = True; guard.server = Mock()
        calls = []
        guard.owned.resume.side_effect = lambda: calls.append('resume')
        guard.server.shutdown.side_effect = lambda: calls.append('shutdown')
        with self.assertRaises(RuntimeError):
            guard.close()
        self.assertEqual(calls, ['resume', 'shutdown'])

    def test_persistent_psql_does_not_put_credentials_in_argv(self):
        with patch.object(g.subprocess, 'Popen') as launch, patch.object(g.Psql, 'query', return_value=44), patch.object(g.threading, 'Thread'):
            db = g.Psql('adtr-auth-e2e-'+'b'*12, True)
        argv = launch.call_args.args[0]
        self.assertIn('-i', argv)
        self.assertIn('PGOPTIONS=-c statement_timeout=1000 -c idle_in_transaction_session_timeout=8000 -c default_transaction_read_only=on', argv)
        self.assertNotIn('ADTR_DATABASE_URL', ' '.join(argv))
        self.assertEqual(db.pid, 44)

    def test_fixture_writers_leave_observers_read_only_and_fail_closed_on_session_end(self):
        import ast
        source = Path(g.__file__).read_text()
        methods = {node.name: node for node in ast.walk(ast.parse(source))
                   if isinstance(node, ast.FunctionDef)}
        for name, expected in (('prepare', [True]), ('checkpoint', [True, False]),
                               ('prepare_activity_fixture', [False]),
                               ('cleanup_activity_fixture_after_wait', [False])):
            calls = [node for node in ast.walk(methods[name]) if isinstance(node, ast.Call)
                     and isinstance(node.func, ast.Name) and node.func.id == 'Psql']
            self.assertEqual([node.args[1].value for node in calls], expected)
        with patch.object(g.subprocess, 'Popen') as launch, patch.object(g.Psql, 'query', return_value=44), patch.object(g.threading, 'Thread'):
            g.Psql('adtr-auth-e2e-' + 'b' * 12, False)
        options = next(value for value in launch.call_args.args[0] if value.startswith('PGOPTIONS='))
        self.assertNotIn('default_transaction_read_only', options)
        self.assertIn('statement_timeout=1000', options)
        self.assertIn('ON_ERROR_STOP=1', launch.call_args.args[0])
        db = g.Psql.__new__(g.Psql)
        db.deadline = None
        db.process = Mock(); db.process.poll.return_value = None
        db.lines = Mock(); db.lines.get.return_value = None
        with self.assertRaises(RuntimeError) as caught:
            db.query('private synthetic SQL')
        self.assertEqual(g.failure_event('initial_boundary', caught.exception), {
            'event': 'running_cancel_guard_failure', 'stage': 'initial_boundary',
            'reason': 'database_session_ended'})
        db.process.stdin.write.assert_called_once()
        self.assertNotIn('private', str(caught.exception))

    def test_checkpoint_sql_read_only_and_exact_owned_scope(self):
        guard = self.guard(); sql = guard.observe_sql('task-one')
        self.assertIn("application_name='adtr-user-export-"+'a'*32+"'", sql)
        self.assertIn("tenant_id='default' AND task_id='task-one'", sql)
        self.assertIn('pg_blocking_pids', sql)
        self.assertIn('clock_timestamp()', sql)
        self.assertNotIn('UPDATE ', sql)
        self.assertNotIn('INSERT ', sql)

    def test_observer_precedes_cont_and_error_resumes(self):
        guard = self.guard()
        guard.threads = Mock(return_value=['T']*4)
        observer, barrier = Mock(), Mock(pid=99)
        initial = task(); initial.update(state='queued', attempt=0, snapshotCount=0)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': initial, 'sessions': []}, RuntimeError('missed')]
        barrier.query.return_value = {'count': 1}
        events = []
        observer.query.side_effect = lambda *a, **k: events.append('observe') or ({'otherNonterminal': 0, 'journalCount': 0, 'task': initial, 'sessions': []} if len(events) == 1 else (_ for _ in ()).throw(RuntimeError('missed')))
        guard.owned.resume.side_effect = lambda: events.append('resume')
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        self.assertEqual(events[:3], ['observe', 'resume', 'observe'])
        self.assertEqual(events[-1], 'resume')
        observer.close.assert_called_once(); barrier.close.assert_called_once()
        self.assertTrue(guard.failed)

    def test_capture_end_commit_precedes_barrier_release_and_failure_is_terminal(self):
        for commit in (True, False):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                *[{'task': task(), 'sessions': [session()]} for _ in range(3)]]
            events = []
            barrier.query.side_effect = lambda sql, **kw: (events.append('rollback') or True) if sql.startswith('ROLLBACK') else {'count': 1}
            guard.controller.commit_capture.side_effect = lambda *args: events.append('commit') or commit
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                if commit:
                    self.assertTrue(guard.checkpoint('task-one', 'worker')['checkpointCaught'])
                    self.assertEqual(events, ['commit', 'rollback'])
                else:
                    with self.assertRaises(RuntimeError): guard.checkpoint('task-one', 'worker')
                    self.assertEqual(events, ['commit'])
                    self.assertTrue(guard.failed)
            guard.controller.commit_capture.assert_called_once()
            args = guard.controller.commit_capture.call_args.args
            self.assertEqual(args[:2], (1, 2))
            self.assertEqual(args[2], guard.controller.pin.call_args.args[1])

    def test_browser_cont_routes_through_guard_without_signal_fallback(self):
        root = Path(g.__file__).resolve().parents[1]
        control = (root / 'web/e2e/owned-running-control.ts').read_text()
        self.assertNotIn('terminal-helpers', control)
        self.assertIn('redirect: "error"', control)
        for kind in ('user', 'group', 'computer'):
            source = (root / f'web/e2e/{kind}-asset-export-v3-terminal-helpers.ts').read_text()
            signal_body = source.split('async function signalWorker(', 1)[1].split('async function queueActivity', 1)[0]
            routing = signal_body.split('const worker = await checkedWorker()', 1)[0]
            self.assertIn('signal === "SIGCONT"', routing)
            self.assertIn('await runningGuard("resume");', routing)
            self.assertIn('return;', routing)
            self.assertNotIn('catch', routing)
            self.assertLess(signal_body.index('await runningGuard'), signal_body.index('process.kill'))
            spec = (root / f'web/e2e/{kind}-asset-export-v3-running-cancel.spec.ts').read_text()
            self.assertIn('finally { await restoreOwnedExportWorker(); }', spec)

    def test_all_armed_owned_resumes_delegate_and_never_signal_directly(self):
        from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard
        guard = self.guard()
        guard.controller.armed = True
        owned = object.__new__(OwnedExportWorkerGuard)
        owned.process = Mock(); owned.process.poll.return_value = None
        owned.assert_identity = Mock()
        owned.resume_delegate = guard._resume_capture_owned
        owned.resume()
        guard.controller.resume_and_release.assert_called_once_with(abort=False)
        owned.process.send_signal.assert_not_called()
        guard.controller.resume_and_release.side_effect = RuntimeError('closed')
        with self.assertRaises(RuntimeError): owned.resume()
        owned.process.send_signal.assert_not_called()
        guard.controller.armed = False
        owned.resume()
        owned.process.send_signal.assert_called_once_with(signal.SIGCONT)

    def test_complete_checkpoint_locks_existing_row_and_releases(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'] * 4)
        queued = task(); queued.update(state='queued', attempt=0, snapshotCount=0)
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                                      {'task': task(), 'sessions': [session()]},
                                      {'task': task(), 'sessions': [session()]}, {'task': task(), 'sessions': [session()]}]
        barrier.query.side_effect = [{'count': 1}, True]
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            result = guard.checkpoint('task-one', 'worker')
        self.assertTrue(result['checkpointCaught'])
        self.assertTrue(result['activityLockReleased'])
        self.assertGreater(result['leaseRemainingSeconds'], 20)
        self.assertEqual(result['blockedOwnedBackends'], 1)
        self.assertNotIn('leaseOwner', result)
        guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
        self.assertIn("AND cycle='queue' FOR UPDATE", barrier.query.call_args_list[0].args[0])
        self.assertEqual(barrier.query.call_args_list[1].args[0], 'ROLLBACK; SELECT to_json(true)')
        with self.assertRaises(RuntimeError):
            guard.checkpoint('task-one', 'worker')

    def busy_session(self):
        row = session(101)
        row.update(query='synthetic private query', state='idle in transaction',
                   waitType='Client', blockers=[],
                   locks=[lock('relation', 'RowShareLock', relation='private.tenant')])
        return row

    def test_busy_owned_transaction_waits_before_single_stop_for_all_kinds(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            busy = {'task': running, 'sessions': [session(), self.busy_session()]}
            ready = {'task': running, 'sessions': [session()]}
            samples = iter([{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []}, busy, busy, ready, ready, ready])
            observer, barrier = Mock(), Mock(pid=99)
            events = []
            def observe(*args, **kwargs):
                events.append('observe')
                return next(samples)
            observer.query.side_effect = observe
            barrier.query.side_effect = [{'count': 1}, True]
            guard.owned.process.send_signal.side_effect = lambda _: events.append('stop')
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
                result = guard.checkpoint('task-one', 'worker')
            self.assertTrue(result['checkpointCaught'])
            self.assertEqual(events, ['observe'] * 5 + ['stop', 'observe'])
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            self.assertIsNone(guard.failure_diagnostic)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_persistent_busy_deadline_keeps_closed_diagnostic_without_stop(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session(), self.busy_session()]}]
        barrier.query.return_value = {'count': 1}
        now = [0.0]
        def sleep(_):
            now[0] = 1.5
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), \
                patch.object(g.time, 'monotonic', side_effect=lambda: now[0]), \
                patch.object(g.time, 'sleep', side_effect=sleep):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.owned.process.send_signal.assert_not_called()
        self.assertEqual(guard.failure_diagnostic['reason'], 'capture_deadline_missed')
        pending = guard.failure_diagnostic['lastNotReady']
        self.assertEqual(pending['reason'], 'registry_observation_incomplete')
        self.assertNotIn('private', json.dumps(guard.failure_diagnostic))
        self.assertEqual(guard.owned.resume.call_count, 2)
        observer.close.assert_called_once(); barrier.close.assert_called_once()
        self.assertEqual(barrier.query.call_count, 1)

    def test_null_state_waits_until_ready_for_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            unavailable = session(101) | {'state': None, 'query': '', 'transaction': False,
                                         'waitType': None, 'blockers': [], 'locks': []}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session(), unavailable]},
                {'task': running, 'sessions': [session()]},
                {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session()]}]
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'):
                self.assertTrue(guard.checkpoint('task-one', 'worker')['checkpointCaught'])
            self.assertEqual(observer.query.call_count, 5)
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def transaction_lock_rejection(self, row):
        before = copy.deepcopy(row)
        with self.assertRaisesRegex(RuntimeError, '^unsafe transaction lock$') as caught:
            g.validate_candidate_strict(row, 99)
        self.assertEqual(row, before)
        self.assertEqual(caught.exception.args, ('unsafe transaction lock',))
        return caught.exception, g.failure_event('candidate_strict', caught.exception)

    def test_transaction_lock_same_sample_categories_distinguish_rejected_clauses(self):
        cases = [
            ('ExclusiveLock', False, 'Lock', [99], g.ACTIVITY_SQL, True, 'activity'),
            ('ShareLock', True, 'Lock', [99], g.ACTIVITY_SQL, True, 'activity'),
            ('RowExclusiveLock', False, 'Lock', [99], g.ACTIVITY_SQL, True, 'activity'),
            ('ShareLock', False, None, [99], g.ACTIVITY_SQL, False, 'activity'),
            ('ShareLock', False, 'IO', [99], g.ACTIVITY_SQL, False, 'activity'),
            ('ShareLock', False, 'Lock', [], g.ACTIVITY_SQL, False, 'activity'),
            ('ShareLock', False, 'Lock', [99, 101], g.ACTIVITY_SQL, False, 'activity'),
            ('ShareLock', False, 'Lock', [99], g.SCHEMA_SQL, False, 'schema'),
        ]
        for mode, granted, wait, blockers, query, reachable, query_kind in cases:
            with self.subTest(mode=mode, granted=granted, wait=wait, blockers=blockers, query_kind=query_kind):
                row = session() | {'waitType': wait, 'blockers': blockers, 'query': query,
                                   'locks': [lock('transactionid', mode, granted)]}
                error, event = self.transaction_lock_rejection(row)
                self.assertEqual(event, {'event': 'running_cancel_guard_failure',
                    'stage': 'candidate_strict', 'reason': 'unsafe_transaction_lock',
                    'modeCategory': mode, 'queryKind': query_kind,
                    'waitTypeCategory': 'none' if wait is None else wait, 'granted': granted,
                    'directBarrier': 99 in blockers, 'otherBlockers': any(pid != 99 for pid in blockers),
                    'barrierReachable': reachable})
                self.assertEqual(set(error.transaction_lock_diagnostic),
                    set(g.TRANSACTION_LOCK_FAILURE_VALUES) | set(g.TRANSACTION_LOCK_FAILURE_FLAGS))

    def test_transaction_lock_success_controls_preserve_exact_results(self):
        for locks in ([lock('transactionid', 'ExclusiveLock')],
                      [lock('transactionid', 'ShareLock', False)],
                      [lock('transactionid', 'ExclusiveLock'), lock('transactionid', 'ShareLock', False)]):
            row = session() | {'locks': locks}
            before = copy.deepcopy(row)
            self.assertEqual(g.validate_candidate_strict(row, 99), g.validate_sessions([row], 99))
            self.assertEqual(row, before)

    def test_transaction_lock_reports_first_rejected_lock_not_later_sample(self):
        row = session() | {'waitType': 'PRIVATE_WAIT', 'blockers': [99, 987654321],
                          'locks': [lock('transactionid', 'PRIVATE_MODE', False),
                                    lock('transactionid', 'ShareLock', True)]}
        _, event = self.transaction_lock_rejection(row)
        self.assertEqual((event['modeCategory'], event['waitTypeCategory'], event['granted']),
                         ('other', 'other', False))
        encoded = json.dumps(event)
        for private in ('PRIVATE', '987654321', 'ACTIVITY_SQL', '99'):
            self.assertNotIn(private, encoded)

    def test_transaction_lock_preserves_original_exception_identity_and_other_first_failures(self):
        row = session() | {'locks': [lock('transactionid', 'ShareLock', True)]}
        original = RuntimeError('unsafe transaction lock')
        prior_require = g.require
        def require(ok, message):
            if not ok and message == 'unsafe transaction lock': raise original
            return prior_require(ok, message)
        with patch.object(g, 'require', side_effect=require):
            error, _ = self.transaction_lock_rejection(row)
        self.assertIs(error, original)
        earlier = row | {'locks': [lock('relation', 'ShareLock', relation='PRIVATE_RELATION'), *row['locks']]}
        with self.assertRaisesRegex(RuntimeError, '^unsafe owned relation lock$') as caught:
            g.validate_candidate_strict(earlier, 99)
        self.assertFalse(hasattr(caught.exception, 'transaction_lock_diagnostic'))
        self.assertNotIn('barrierReachable', g.failure_event('candidate_strict', caught.exception))

    def test_transaction_lock_diagnostic_failure_never_replaces_strict_rejection(self):
        row = session() | {'locks': [lock('transactionid', 'ShareLock', True)]}
        with patch.object(g, 'TRANSACTION_LOCK_FAILURE_VALUES', None):
            with self.assertRaisesRegex(RuntimeError, '^unsafe transaction lock$') as caught:
                g.validate_candidate_strict(row, 99)
        self.assertFalse(hasattr(caught.exception, 'transaction_lock_diagnostic'))
        class ForeignError(RuntimeError): pass
        for original in (ForeignError('unsafe transaction lock'), RuntimeError('PRIVATE_EXCEPTION')):
            prior_require = g.require
            def require(ok, message):
                if not ok and message == 'unsafe transaction lock': raise original
                return prior_require(ok, message)
            with patch.object(g, 'require', side_effect=require):
                with self.assertRaises(RuntimeError) as caught:
                    g.validate_candidate_strict(row, 99)
            self.assertIs(caught.exception, original)
            self.assertFalse(hasattr(original, 'transaction_lock_diagnostic'))

    def test_transaction_lock_closed_projection_rejects_bad_details_and_wrong_scope(self):
        row = session() | {'locks': [lock('transactionid', 'ShareLock', True)]}
        error, _ = self.transaction_lock_rejection(row)
        valid = error.transaction_lock_diagnostic.copy()
        malformed = [None, [], {}, valid | {'private': 'SECRET'}]
        for key in valid:
            for value in ('SECRET', '', 1, None, [], {}):
                malformed.append(valid | {key: value})
            malformed.append({k: v for k, v in valid.items() if k != key})
        for key in g.TRANSACTION_LOCK_FAILURE_VALUES:
            malformed.append(valid | {key: True})
        for detail in malformed:
            error.transaction_lock_diagnostic = detail
            self.assertEqual(set(g.failure_event('candidate_strict', error)), {'event', 'stage', 'reason'})
        error.transaction_lock_diagnostic = valid
        for stage in g.FAILURE_STAGES - {'candidate_strict'}:
            self.assertEqual(set(g.failure_event(stage, error)), {'event', 'stage', 'reason'})
        for other in (RuntimeError('unsafe tuple lock'), ValueError('unsafe transaction lock'),
                      RuntimeError('unsafe transaction lock', 'PRIVATE')):
            other.transaction_lock_diagnostic = valid
            self.assertEqual(set(g.failure_event('candidate_strict', other)), {'event', 'stage', 'reason'})

    def test_transaction_lock_candidate_failure_never_resamples_pins_stops_or_waits(self):
        for kind in g.EXPORT_KINDS:
            with self.subTest(kind=kind):
                guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
                running = task() | {'kind': g.export_kind(kind)[0]}
                queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
                observer, barrier = Mock(), Mock(pid=99)
                row = session() | {'locks': [lock('transactionid', 'ShareLock', True)]}
                observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                    {'task': running, 'sessions': [row]}]
                barrier.query.return_value = {'count': 1}
                with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep') as sleep:
                    with self.assertRaisesRegex(RuntimeError, '^running checkpoint guard failed closed$'):
                        guard.checkpoint('task-one', 'worker')
                guard.controller.pin.assert_not_called(); guard.owned.process.send_signal.assert_not_called()
                sleep.assert_not_called(); self.assertEqual(observer.query.call_count, 2)
                self.assertEqual(guard.failure_diagnostic['stage'], 'candidate_strict')
                self.assertEqual(guard.failure_diagnostic['reason'], 'unsafe_transaction_lock')
                self.assertEqual(guard.failure_diagnostic['modeCategory'], 'ShareLock')
                self.assertTrue(guard.failure_diagnostic['granted']); self.assertTrue(guard.failed)
                self.assertEqual(guard.owned.resume.call_count, 2)
                observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_candidate_strict_same_sample_wait_none_rejects_lock_control_passes(self):
        target = session() | {'waitType': None, 'locks': []}
        original = copy.deepcopy(target)
        self.assertIsNone(g.validate_session_observation([target], 99))
        with self.assertRaisesRegex(RuntimeError, '^unowned or cyclic barrier blocker$') as caught:
            g.validate_candidate_strict(target, 99)
        self.assertEqual(target, original)
        event = g.failure_event('candidate_strict', caught.exception)
        self.assertEqual(event, {'event': 'running_cancel_guard_failure', 'stage': 'candidate_strict',
            'reason': 'unowned_barrier_blocker', 'blockerCheck': 'strict_barrier_chain',
            'queryCategory': 'activity', 'stateCategory': 'active', 'waitTypeCategory': 'none',
            'transaction': True, 'directBarrier': True, 'otherBlockers': False})
        control = target | {'waitType': 'Lock'}
        self.assertEqual(g.validate_candidate_strict(control, 99), g.validate_sessions([control], 99))

    def test_candidate_strict_preserves_original_error_and_private_values(self):
        original = RuntimeError('unowned or cyclic barrier blocker')
        target = session(123456789) | {'query': 'PRIVATE_SQL_SENTINEL', 'locks': [],
                                     'blockers': [987654321, 112233445], 'state': 'PRIVATE_STATE',
                                     'waitType': 'PRIVATE_WAIT'}
        with patch.object(g, 'validate_sessions', side_effect=original):
            with self.assertRaises(RuntimeError) as caught:
                g.validate_candidate_strict(target, 987654321)
        self.assertIs(caught.exception, original)
        event = g.failure_event('candidate_strict', original)
        self.assertEqual((event['queryCategory'], event['stateCategory'], event['waitTypeCategory']), ('other', 'other', 'other'))
        self.assertTrue(event['directBarrier']); self.assertTrue(event['otherBlockers'])
        encoded = json.dumps(event)
        for secret in ('PRIVATE', '123456789', '987654321', '112233445'):
            self.assertNotIn(secret, encoded)
        original = RuntimeError('unowned or cyclic barrier blocker')
        with patch.object(g, 'validate_sessions', side_effect=original), patch.object(g, 'transaction_query_failure', side_effect=ValueError('PRIVATE')):
            with self.assertRaises(RuntimeError) as caught: g.validate_candidate_strict(target, 99)
        self.assertIs(caught.exception, original)
        self.assertEqual(g.failure_event('candidate_strict', original)['blockerCheck'], 'strict_barrier_chain')
        self.assertNotIn('queryCategory', g.failure_event('candidate_strict', original))
        class ForeignError(RuntimeError): pass
        for error in (ForeignError('unowned or cyclic barrier blocker'), RuntimeError('PRIVATE')):
            with patch.object(g, 'validate_sessions', side_effect=error):
                with self.assertRaises(RuntimeError) as caught: g.validate_candidate_strict(target, 99)
            self.assertIs(caught.exception, error)
            self.assertFalse(hasattr(error, 'strict_candidate_diagnostic'))

    def test_candidate_strict_projection_rejects_bad_detail(self):
        valid = dict(queryCategory='activity', stateCategory='active', waitTypeCategory='none',
                     transaction=True, directBarrier=True, otherBlockers=False)
        for update in ({'transaction': 1}, {'directBarrier': 'PRIVATE'}, {'otherBlockers': None},
                       {'queryCategory': 'PRIVATE'}, {'stateCategory': 'active\nPRIVATE'},
                       {'waitTypeCategory': 'x'*10000}, {'extra': 'PRIVATE'}):
            error = RuntimeError('unowned or cyclic barrier blocker')
            error.blocker_diagnostic = {'blockerCheck': 'strict_barrier_chain'}
            error.strict_candidate_diagnostic = valid | update
            event = g.failure_event('candidate_strict', error)
            self.assertNotIn('queryCategory', event)
            self.assertNotIn('PRIVATE', json.dumps(event))
        for check in ('outside_owned_sample', 'cyclic_owned_graph'):
            error = g.blocker_failure(check); error.strict_candidate_diagnostic = valid
            self.assertNotIn('queryCategory', g.failure_event('candidate_strict', error))

    def test_candidate_strict_failure_never_pins_or_stops_and_cleans_up(self):
        for blockers, expected in (([99], 'strict_barrier_chain'), ([999], 'outside_owned_sample'), ([100], 'cyclic_owned_graph')):
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': task(), 'sessions': [session() | {'waitType': None, 'locks': [], 'blockers': blockers}]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep') as sleep:
                with self.assertRaises(RuntimeError): guard.checkpoint('task-one', 'worker')
            guard.controller.pin.assert_not_called()
            guard.owned.process.send_signal.assert_not_called()
            sleep.assert_not_called()
            self.assertEqual(observer.query.call_count, 2)
            self.assertEqual(guard.failure_diagnostic['blockerCheck'], expected)
            if expected == 'strict_barrier_chain':
                self.assertEqual(guard.failure_diagnostic['stage'], 'candidate_strict')
                self.assertEqual(guard.failure_diagnostic['waitTypeCategory'], 'none')
            else: self.assertNotIn('directBarrier', guard.failure_diagnostic)
            self.assertTrue(guard.failed)
            self.assertEqual(guard.owned.resume.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_null_target_is_fatal_before_pin_without_stop(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session() | {'state': None}]}]
        barrier.query.return_value = {'count': 1}
        now = [0.0]
        def sleep(_):
            now[0] = 1.5
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time,
                'monotonic', side_effect=lambda: now[0]), patch.object(g.time, 'sleep', side_effect=sleep):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.owned.process.send_signal.assert_not_called()
        self.assertEqual(guard.failure_diagnostic, {'event': 'running_cancel_guard_failure',
            'stage': 'candidate_strict', 'reason': 'state_unavailable'})
        guard.controller.pin.assert_not_called()
        self.assertEqual(guard.owned.resume.call_count, 2)
        observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_null_state_after_stop_fails_and_resumes_for_every_kind(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session()]},
                {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session() | {'state': None}]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            self.assertEqual(observer.query.call_count, 4)
            self.assertEqual(guard.owned.resume.call_count, 2)
            self.assertEqual(guard.failure_diagnostic['reason'], 'state_unavailable')
            self.assertEqual(guard.failure_diagnostic['stage'], 'session_validation')
            self.assertIsNone(guard.stopped_at)
            observer.close.assert_called_once(); barrier.close.assert_called_once()
            self.assertEqual(barrier.query.call_count, 1)

    def test_null_state_cannot_hide_later_invalid_rows_locks_or_blockers(self):
        unavailable = session() | {'state': None}
        for update in ({'query': None}, {'pid': True}, {'pid': 100}, {'locks': [{}]},
                {'locks': [lock('object', 'RowExclusiveLock')]}, {'locks': [lock('secret', 'ShareLock')]},
                {'blockers': [999]}, {'blockers': [102]}, {'state': 'secret'}, {'waitType': 'secret'}):
            with self.subTest(update=update), self.assertRaises(RuntimeError):
                g.freeze_readiness([unavailable, session(102) | update], 99)
        a, b = session(102) | {'blockers': [103]}, session(103) | {'blockers': [102]}
        with self.assertRaisesRegex(RuntimeError, 'unowned or cyclic'):
            g.freeze_readiness([unavailable, a, b], 99)
        self.assertEqual(str(g.freeze_readiness([unavailable], 99)), 'owned backend state unavailable')

    def test_observation_diagnostics_are_closed_field_categories(self):
        cases = [({'state': []}, 'state_type', 'list'), ({'state': 'private'}, 'state_value', 'str'),
            ({'query': None}, 'query', 'null'), ({'transaction': 1}, 'transaction', 'int'),
            ({'waitType': {}}, 'wait_type', 'dict'), ({'waitType': 'private'}, 'wait_value', 'str'),
            ({'blockers': [False]}, 'blocker_pid', 'bool'), ({'locks': None}, 'locks', 'null'),
            ({'locks': [lock('relation', 'ShareLock', relation=[])]}, 'lock_relation', 'list'),
            ({'locks': [lock('relation', 'ShareLock', objsubid=True)]}, 'lock_objsubid', 'bool')]
        for update, check, category in cases:
            with self.subTest(check=check), self.assertRaises(RuntimeError) as caught:
                g.freeze_readiness([session() | update], 99)
            event = g.failure_event('capture_readiness', caught.exception)
            self.assertEqual(event['observationCheck'], check)
            self.assertEqual(event['valueTypeCategory'], category)
            self.assertNotIn('private', json.dumps(event))
        for detail in ({'observationCheck': 'private', 'valueTypeCategory': 'str'},
                {'observationCheck': 'query', 'valueTypeCategory': 'private'},
                {'observationCheck': 'query', 'valueTypeCategory': 'str', 'extra': 'private'}):
            error = RuntimeError('invalid owned session observation'); error.observation_diagnostic = detail
            self.assertEqual(set(g.failure_event('capture_readiness', error)), {'event', 'stage', 'reason'})

    def test_pg_lock_categories_diagnose_but_never_accept_extra_types(self):
        extra = g.LOCK_TYPE_CATEGORIES - {'relation', 'advisory', 'virtualxid', 'transactionid', 'tuple'}
        for typ in extra | {'private-lock-value'}:
            with self.subTest(typ=typ), self.assertRaisesRegex(RuntimeError, 'unrecognized owned lock') as caught:
                g.freeze_readiness([session() | {'state': None}, session(102) | {
                    'locks': [lock(typ, 'RowExclusiveLock')]}], 99)
            event = g.failure_event('capture_readiness', caught.exception)
            self.assertEqual(event['lockTypeCategory'], typ if typ in extra else 'other')
            self.assertNotIn('private', json.dumps(event))
        for detail in ({'lockTypeCategory': 'private'}, {'lockTypeCategory': 'object', 'extra': 'private'},
                       {'lockTypeCategory': []}):
            error = RuntimeError('unrecognized owned lock'); error.lock_diagnostic = detail
            self.assertEqual(set(g.failure_event('capture_readiness', error)), {'event', 'stage', 'reason'})

    def test_deadline_is_checked_again_immediately_before_stop(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
            {'task': task(), 'sessions': [session()]}, {'task': task(), 'sessions': [session()]}]
        barrier.query.return_value = {'count': 1}
        now = [0.0]
        guard.owned.assert_identity.side_effect = lambda: now.__setitem__(0, 1.5)
        with patch.object(g, 'Psql', side_effect=[observer, barrier]), \
                patch.object(g.time, 'monotonic', side_effect=lambda: now[0]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        guard.owned.process.send_signal.assert_not_called()
        self.assertEqual(guard.failure_diagnostic['reason'], 'capture_deadline_missed')
        observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_complete_prescan_does_not_hide_invalid_later_sample_behind_busy_query(self):
        mutations = [lambda row: row.update(pid=True), lambda row: row.update(pid=100),
            lambda row: row.update(blockers=[999]), lambda row: row.update(blockers=[102]),
            lambda row: row.update(blockers=[False]), lambda row: row.update(locks=None),
            lambda row: row.update(query=None), lambda row: row.update(transaction=1),
            lambda row: row.update(state='private'), lambda row: row.update(waitType='private'),
            lambda row: row.update(locks=[lock('private', 'ShareLock')]),
            lambda row: row.update(locks=[lock('relation', 'ShareLock', granted='private')]),
            lambda row: row.update(locks=[lock('relation', 'ShareLock', relation=[])]),
            lambda row: row.update(locks=[{}]), lambda row: row.pop('state')]
        for mutation in mutations:
            bad = session(102); mutation(bad)
            with self.subTest(mutation=mutation), self.assertRaises(RuntimeError):
                g.freeze_readiness([session(), self.busy_session(), bad], 99)
        first, second = session(102), session(103)
        first['blockers'], second['blockers'] = [103], [102]
        with self.assertRaisesRegex(RuntimeError, 'unowned or cyclic'):
            g.freeze_readiness([self.busy_session(), first, second], 99)
        self.assertIsNotNone(g.freeze_readiness([], 99))
        self.assertIsNone(g.freeze_readiness([session()], 99))

    def test_invalid_sample_or_checkpoint_fails_before_stop_without_retry(self):
        bad = session(102); bad['blockers'] = [999]
        samples = [
            {'task': task(), 'sessions': [session(), self.busy_session(), bad]},
            {'task': task() | {'leaseRemainingSeconds': 20}, 'sessions': [session()]},
            {'task': task() | {'taskID': 'wrong'}, 'sessions': [session()]},
            {'task': task() | {'kind': 'wrong'}, 'sessions': [session()]},
        ]
        for sample in samples:
            guard = self.guard(); guard.threads = Mock(return_value=['T'])
            queued = task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []}, sample]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_not_called()
            self.assertEqual(observer.query.call_count, 2)
            self.assertEqual(guard.owned.resume.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_ready_before_stop_but_busy_after_stop_fails_without_reopening_capture(self):
        for kind in g.EXPORT_KINDS:
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            running = task() | {'kind': g.export_kind(kind)[0]}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': [session()]},
                {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session(), self.busy_session()]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            self.assertEqual(observer.query.call_count, 4)
            self.assertEqual(guard.owned.resume.call_count, 2)
            self.assertEqual(guard.failure_diagnostic['stage'], 'session_validation')
            self.assertEqual(guard.failure_diagnostic['reason'], 'unsafe_transaction_query')
            self.assertIsNone(guard.stopped_at)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_readiness_only_catches_exact_freeze_predicate_errors(self):
        class DerivedError(RuntimeError):
            pass
        for error in (RuntimeError('private unexpected'), DerivedError('unsafe owned transaction query'),
                      ValueError('private'), KeyError('query')):
            with patch.object(g, 'validate_sessions', side_effect=error):
                with self.assertRaises(type(error)):
                    g.freeze_readiness([session()], 99)
        error = RuntimeError('missed two-second activity checkpoint')
        error.last_not_ready_error = RuntimeError('private sensitive')
        self.assertNotIn('lastNotReady', g.failure_event('capture', error))
        error.last_not_ready_error = error
        self.assertNotIn('lastNotReady', g.failure_event('capture', error))

    def test_barrier_cleanup_failure_still_closes_observer(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task(); queued.update(state='queued', attempt=0, snapshotCount=0)
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []}, RuntimeError('missed')]
        barrier.query.return_value = {'count': 1}
        barrier.close.side_effect = RuntimeError('close failure')
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        observer.close.assert_called_once()
        self.assertTrue(guard.failed)

    def test_no_production_writes_or_checkpoint_hooks(self):
        source = Path(g.__file__).read_text()
        self.assertNotIn('UPDATE adtr.tasks', source)
        self.assertNotIn('SET lease_', source)
        self.assertNotIn('pg_terminate_backend', source)
        self.assertIn("worker_id='\" + self.activity_key", source)
        self.assertIn("AND cycle='queue' FOR UPDATE", source)
        self.assertIn("hmac.compare_digest", source)
        self.assertIn("ThreadingHTTPServer(('127.0.0.1', 0)", source)



    def test_closed_kind_mapping_and_default_user_sql_compatibility(self):
        self.assertEqual(set(g.EXPORT_KINDS), {'user', 'group', 'computer'})
        default_sql = self.guard().observe_sql('task-one')
        self.assertEqual(default_sql, self.guard(asset_kind='user').observe_sql('task-one'))
        for kind in ('user', 'group', 'computer'):
            with self.subTest(kind=kind):
                expected = (kind + '_assets.v3.export',) + tuple(
                    'adtr.' + kind + '_asset_export_' + suffix
                    for suffix in ('snapshots', 'manifests', 'chunks'))
                self.assertEqual(g.export_kind(kind), expected)
                sql = self.guard(asset_kind=kind).observe_sql('task-one')
                self.assertEqual(sql, default_sql.replace('adtr.user_asset_export_',
                                                         'adtr.' + kind + '_asset_export_'))
                for table in expected[1:]:
                    self.assertEqual(sql.count(table), 1)
        with self.assertRaises(TypeError):
            g.EXPORT_KINDS['user'] = ('injected',)

    def test_invalid_selectors_rejected_before_owned_resources(self):
        for kind in (None, 0, True, [], {}, '', 'Group', ' user', 'user ',
                     'user_assets.v3.export', 'adtr.group_asset_export_snapshots',
                     "group; SELECT pg_sleep(30)", ('user',)):
            with self.subTest(kind=kind), patch.object(g, 'OwnedExportWorkerGuard') as owned, patch.object(g, 'Psql') as pg:
                with self.assertRaises(RuntimeError):
                    g.RunningExportCancelGuard(Mock(), 'a'*32, '/fake/bin/adtr',
                        'adtr-auth-e2e-'+'a'*12, 'adtr-user-export-'+'a'*32,
                        'postgres://localhost/adtr_e2e', asset_kind=kind)
                with self.assertRaises(RuntimeError):
                    g.validate_checkpoint(task(), 'task-one', 'worker', kind)
                owned.assert_not_called(); pg.assert_not_called()
        for option in ('tables', 'snapshots', 'task_kind', 'sql'):
            with self.subTest(option=option), self.assertRaises(TypeError):
                self.guard(**{option: 'adtr.tasks'})

    def test_observation_rejects_identifier_injection(self):
        for kind in ('user', 'group', 'computer'):
            for value in ("task' OR true --", '', 'x'*129, None):
                with self.subTest(kind=kind, value=value), self.assertRaises(RuntimeError):
                    self.guard(asset_kind=kind).observe_sql(value)

    def test_each_kind_checkpoint_rejects_wrong_kind_and_unsafe_fence(self):
        for kind in ('user', 'group', 'computer'):
            row = task(); row['kind'] = kind + '_assets.v3.export'
            self.assertEqual(g.validate_checkpoint(row, 'task-one', 'worker', kind)['kind'], row['kind'])
            for field, value in [('kind', other + '_assets.v3.export') for other in ('user', 'group', 'computer') if other != kind] + [
                    ('taskID', 'another'), ('progress', 0), ('snapshotCount', 0),
                    ('manifestCount', 1), ('chunkCount', 1), ('attempt', 2),
                    ('state', 'cancel_requested'), ('fencingToken', 0),
                    ('leaseOwner', 'worker-4'), ('leaseOwner', 'other-0'),
                    ('leaseRemainingSeconds', 20), ('leaseUntilEpoch', 140)]:
                bad = row | {field: value}
                with self.subTest(kind=kind, field=field, value=value), self.assertRaises(RuntimeError):
                    g.validate_checkpoint(bad, 'task-one', 'worker', kind)

    def test_registry_rejects_additional_transitive_backend_each_kind(self):
        for kind in ('group', 'computer'):
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'] * 4)
            running = task() | {'kind': kind + '_assets.v3.export'}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            rows = [session(100), session(101)]
            rows[1]['blockers'] = [100]
            rows[1]['locks'][-1] = lock('tuple', 'ExclusiveLock', False, 'adtr.system_worker_activity')
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                {'task': running, 'sessions': rows}, {'task': running, 'sessions': rows}, {'task': running, 'sessions': rows}]
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            guard.owned.process.send_signal.assert_not_called()
            observer.close.assert_called_once(); barrier.close.assert_called_once()
            for call in observer.query.call_args_list:
                self.assertIn('adtr.' + kind + '_asset_export_snapshots', call.args[0])

    def test_wrong_initial_kind_fails_before_activity_lock_and_resume(self):
        for kind in ('group', 'computer'):
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            observer = Mock()
            observer.query.return_value = {'task': task() | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}, 'sessions': []}
            with patch.object(g, 'Psql', return_value=observer) as pg, self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
            pg.assert_called_once_with(guard.container, True)
            guard.owned.process.send_signal.assert_not_called()
            guard.owned.resume.assert_called_once()  # Fail-closed cleanup CONT.
            observer.close.assert_called_once()
            self.assertTrue(guard.failed)

    def test_new_kinds_still_fail_closed_on_capture_deadline(self):
        for kind in ('group', 'computer'):
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'])
            queued = task() | {'kind': kind + '_assets.v3.export', 'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.return_value = {'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []}
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'monotonic', side_effect=[0., 0., 1.5, 1.5]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            self.assertTrue(guard.failed)
            guard.owned.process.send_signal.assert_not_called()
            self.assertEqual(guard.owned.resume.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_new_kinds_still_require_every_thread_stopped(self):
        for kind in ('group', 'computer'):
            guard = self.guard(asset_kind=kind)
            guard.threads = Mock(side_effect=[['T'], ['T', 'S']])
            running = task() | {'kind': kind + '_assets.v3.export'}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []}, {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [session()]}]
            barrier.query.return_value = {'count': 1}
            with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g.time, 'sleep'), patch.object(g.time, 'monotonic', side_effect=[0., 0., .1, .1, .1, .2, .6, .6]):
                with self.assertRaises(RuntimeError):
                    guard.checkpoint('task-one', 'worker')
            self.assertTrue(guard.failed)
            self.assertIsNone(guard.stopped_at)
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
            self.assertEqual(guard.owned.resume.call_count, 2)
            observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_http_checkpoint_cannot_select_kind_tables_or_sql(self):
        for kind in ('user', 'group', 'computer'):
            guard = self.guard(asset_kind=kind)
            guard.checkpoint = Mock(return_value={'checkpointCaught': True})
            with patch.object(g, 'ThreadingHTTPServer') as server, patch.object(g.threading, 'Thread'):
                server.return_value.server_port = 12345
                guard.start()
                handler_type = server.call_args.args[1]
            for extra in ({}, {'asset_kind': 'computer'}, {'kind': 'user'},
                          {'taskKind': 'computer_assets.v3.export'},
                          {'tables': ['adtr.tasks']}, {'sql': 'SELECT 1'}):
                data = json.dumps({'taskID': 'task-one', 'workerID': 'worker'} | extra).encode()
                handler = object.__new__(handler_type)
                handler.client_address = ('127.0.0.1', 1234)
                handler.headers = {'Host': '127.0.0.1:12345', 'Content-Type': 'application/json',
                    'Content-Length': str(len(data)), 'X-ADTR-Guard-Token': guard.token}
                handler.path = '/checkpoint'; handler.rfile = io.BytesIO(data); handler.wfile = io.BytesIO()
                handler.send_response = Mock(); handler.send_header = Mock(); handler.end_headers = Mock()
                guard.checkpoint.reset_mock()
                handler.do_POST()
                handler.send_response.assert_called_once_with(409 if extra else 200)
                if extra:
                    guard.checkpoint.assert_not_called()
                else:
                    guard.checkpoint.assert_called_once_with('task-one', 'worker')
                self.assertEqual(guard.asset_kind, kind)


    def test_failure_diagnostics_are_closed_and_never_print_raw_values(self):
        secret = "postgres://sensitive:credential@host/db SELECT tenant worker token"
        event = g.failure_event('capture', RuntimeError(secret))
        other = g.failure_event(secret, ValueError(secret))
        self.assertEqual(event, {'event': 'running_cancel_guard_failure', 'stage': 'capture', 'reason': 'unclassified'})
        self.assertEqual(other['stage'], 'unclassified')
        for message, reason in list(g.FAILURE_REASONS.items()) + [(secret, 'unclassified')]:
            guard = self.guard()
            with patch.object(g.sys, 'stderr', new_callable=io.StringIO) as output:
                guard._report_failure('capture', RuntimeError(message))
                guard._emit_failure(); guard._emit_failure()
            self.assertEqual(guard.failure_diagnostic['reason'], reason)
            self.assertEqual(json.loads(output.getvalue()), guard.failure_diagnostic)
            self.assertNotIn(secret, output.getvalue())
            self.assertEqual(set(guard.failure_diagnostic), {'event', 'stage', 'reason'})
            self.assertEqual(len(output.getvalue().splitlines()), 1)

    def test_relation_diagnostics_preserve_exact_rejection_matrix(self):
        relations = {'adtr.schema_version', 'adtr.schema_version_pkey',
            'adtr.system_worker_activity', 'adtr.system_worker_activity_pkey',
            'adtr.system_worker_activity_recent'}
        allowed_modes = {'AccessShareLock', 'RowShareLock', 'RowExclusiveLock'}
        other_modes = {'ShareUpdateExclusiveLock', 'ShareLock',
            'ShareRowExclusiveLock', 'ExclusiveLock', 'AccessExclusiveLock'}
        secret_relation = 'private.tenant_worker_token'
        secret_mode = 'postgres://sensitive:credential@host/db SELECT private'
        self.assertEqual(g.RELATIONS, relations)
        for relation in sorted(relations | {secret_relation}):
            for mode in sorted(allowed_modes | other_modes | {secret_mode}):
                for granted in (False, True):
                    with self.subTest(relation=relation, mode=mode, granted=granted):
                        row = session()
                        row['locks'].append(lock('relation', mode, granted, relation))
                        if relation in relations and granted and mode in allowed_modes:
                            self.assertEqual(g.validate_sessions([row], 99)[1:], (1, 1))
                            continue
                        with self.assertRaisesRegex(RuntimeError, '^unsafe owned relation lock$') as caught:
                            g.validate_sessions([row], 99)
                        event = g.failure_event('session_validation', caught.exception)
                        self.assertEqual(event, {
                            'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
                            'reason': 'unsafe_relation_lock',
                            'subreason': ('relation_not_allowlisted' if relation not in relations else
                                          'lock_not_granted' if not granted else 'mode_not_allowlisted'),
                            'relationCategory': 'known_allowlist' if relation in relations else 'other',
                            'modeCategory': 'other' if mode == secret_mode else mode,
                            'granted': granted, 'queryKind': 'activity',
                        })
                        for raw in (relation, secret_mode, row['query']):
                            self.assertNotIn(raw, json.dumps(event))
                        self.assertEqual(set(caught.exception.__dict__), {'relation_diagnostic'})

    def test_relation_diagnostics_use_fixed_query_kinds(self):
        for query, kind in ((g.ACTIVITY_SQL, 'activity'), (g.SCHEMA_SQL, 'schema'),
                            (g.GATE_SQL, 'schemaGate')):
            row = session(101); row.update(query=query, blockers=[])
            row['locks'] = [lock('relation', 'ShareLock', relation='adtr.schema_version')]
            with self.subTest(kind=kind), self.assertRaises(RuntimeError) as caught:
                g.validate_sessions([session(), row], 99)
            event = g.failure_event('session_validation', caught.exception)
            self.assertEqual(event['queryKind'], kind)
            self.assertNotIn(query, json.dumps(event))
        row = session(); row['query'] = 'SELECT sensitive FROM private_table'
        with self.assertRaises(RuntimeError) as caught:
            g.validate_sessions([row], 99)
        self.assertEqual(g.failure_event('session_validation', caught.exception), {
            'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
            'reason': 'unsafe_transaction_query', 'queryCategory': 'other',
            'lockSafetyCategory': 'unsafe_transactionid',
            'stateCategory': 'active', 'waitTypeCategory': 'Lock', 'locksPresent': True})

    def test_transaction_diagnostics_never_expand_query_acceptance(self):
        known = ((g.ACTIVITY_SQL, 'activity'), (g.SCHEMA_SQL, 'schema'),
                 (g.GATE_SQL, 'schemaGate'))
        rejected = [('BEGIN', 'begin'), ('commit', 'commit'), ('  RollBack  ', 'rollback'),
            ('BEGIN; SELECT private', 'other'), ('COMMIT /* private */', 'other'),
            ('SELECT private FROM tenant', 'other'), ('', 'other')]
        rejected += [(query + ' /* private */', 'other') for query, _ in known]
        for query, category in list(known) + rejected:
            row = session(101); row.update(query=query, blockers=[], locks=[])
            with self.subTest(query=query):
                diagnostic = g.failure_event('session_validation', g.transaction_query_failure(row))
                self.assertEqual(diagnostic['queryCategory'], category)
                if (query, category) in known:
                    self.assertEqual(g.validate_sessions([session(), row], 99)[1:], (1, 2))
                else:
                    with self.assertRaisesRegex(RuntimeError, '^unsafe owned transaction query$') as caught:
                        g.validate_sessions([session(), row], 99)
                    self.assertEqual(g.failure_event('session_validation', caught.exception), diagnostic)
                    self.assertEqual(set(caught.exception.__dict__), {'transaction_diagnostic'})
                # Nontransactional idle remains accepted, including unknown SQL.
                row.update(transaction=False, state='idle')
                self.assertEqual(g.validate_sessions([session(), row], 99)[1:], (1, 1))

    def test_transaction_lock_summary_is_diagnostic_only_and_order_independent(self):
        cases = [([], 'none'), ([lock('virtualxid', 'ExclusiveLock')], 'allowlisted_shapes'),
            ([lock('relation', 'RowShareLock', relation='adtr.schema_version')], 'allowlisted_shapes'),
            ([lock('relation', 'AccessShareLock', relation='private.tenant')], 'unsafe_relation'),
            ([lock('relation', 'ShareLock', relation='adtr.schema_version')], 'unsafe_relation'),
            ([lock('relation', 'RowShareLock', False, 'adtr.schema_version')], 'unsafe_relation'),
            ([lock('advisory', 'ShareLock', classid='0', objid='734192801', objsubid=1)], 'allowlisted_shapes'),
            ([lock('advisory', 'ShareLock', classid='private')], 'unsafe_advisory'),
            ([lock('virtualxid', 'ShareLock')], 'unsafe_virtualxid'),
            ([lock('transactionid', 'ShareLock', False)], 'unsafe_transactionid'),
            ([lock('transactionid', 'ExclusiveLock')], 'allowlisted_shapes'),
            ([lock('tuple', 'ExclusiveLock')], 'unsafe_tuple'),
            ([lock('private', 'private')], 'unknown_lock'),
            ([None], 'unknown_lock'), (None, 'unknown_lock')]
        for locks, expected in cases:
            with self.subTest(category=expected, locks=locks):
                self.assertEqual(g.transaction_lock_category(locks), expected)
                if type(locks) is list:
                    row = session(); row.update(query='SELECT private', locks=locks)
                    with self.assertRaisesRegex(RuntimeError, '^unsafe owned transaction query$') as caught:
                        g.validate_sessions([row], 99)
                    event = g.failure_event('session_validation', caught.exception)
                    self.assertEqual(event['lockSafetyCategory'], expected)
                    self.assertNotIn('private', json.dumps(event))
        mixed = [lock('relation', 'private', relation='private'), lock('private', 'private')]
        self.assertEqual(g.transaction_lock_category(mixed), 'unknown_lock')
        self.assertEqual(g.transaction_lock_category(list(reversed(mixed))), 'unknown_lock')

    def test_transaction_lock_shapes_match_original_nonactivity_lock_rules(self):
        modes = ('AccessShareLock', 'RowShareLock', 'RowExclusiveLock', 'ShareLock',
                 'ExclusiveLock', 'AccessExclusiveLock', 'private')
        cases = []
        for typ in ('relation', 'advisory', 'virtualxid', 'transactionid', 'tuple', 'private'):
            for mode in modes:
                for granted in (False, True):
                    for relation in sorted(g.RELATIONS) + ['private']:
                        cases.append(lock(typ, mode, granted, relation,
                            classid='0', objid='734192801', objsubid=1))
        for key in ('classid', 'objid', 'objsubid'):
            for value in ('private', None, 0, 2):
                cases.append(lock('advisory', 'ShareLock', classid='0', objid='734192801',
                                  objsubid=1) | {key: value})
        for item in cases:
            row = session(101); row.update(query=g.SCHEMA_SQL, blockers=[], locks=[item])
            try:
                g.validate_sessions([session(), row], 99)
                accepted = True
            except RuntimeError:
                accepted = False
            self.assertEqual(g.transaction_lock_category([item]) == 'allowlisted_shapes',
                             accepted, item)

    def test_transaction_categories_are_closed_and_private(self):
        secret = 'postgres://private:credential@host/db SQL tenant worker token'
        states = list(g.TRANSACTION_FAILURE_VALUES['stateCategory']) + [secret, None, [], {}]
        waits = list(g.TRANSACTION_FAILURE_VALUES['waitTypeCategory']) + [secret, None, [], {}]
        for state in states:
            for wait in waits:
                for locks in ([], [lock(secret, secret, relation=secret)]):
                    row = session(); row.update(query=secret, state=state, waitType=wait,
                        locks=locks, pid=secret, blockers=[])
                    error = g.transaction_query_failure(row)
                    event = g.failure_event('session_validation', error)
                    self.assertEqual(event['stateCategory'], state if type(state) is str and
                        state in g.TRANSACTION_FAILURE_VALUES['stateCategory'] else 'other')
                    self.assertEqual(event['waitTypeCategory'], 'none' if wait is None else wait
                        if type(wait) is str and wait in g.TRANSACTION_FAILURE_VALUES['waitTypeCategory'] else 'other')
                    self.assertEqual(event['locksPresent'], bool(locks))
                    self.assertEqual(event['queryCategory'], 'other')
                    self.assertNotIn(secret, json.dumps(event))
                    self.assertNotIn(secret, json.dumps(error.__dict__))

    def test_transaction_diagnostic_fields_are_exactly_allowlisted(self):
        secret = 'postgres://private:credential@host/db SQL tenant worker token'
        error = g.transaction_query_failure(session())
        detail = error.transaction_diagnostic
        base = {'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
                'reason': 'unsafe_transaction_query'}
        mutations = [detail | {key: value} for key in detail
                     for value in (secret, [], {}, None, 1)]
        mutations += [detail | {'raw': secret}, {key: value for key, value in detail.items()
                      if key != 'queryCategory'}, [detail], None]
        for invalid in mutations:
            with self.subTest(detail=invalid):
                error.transaction_diagnostic = invalid
                self.assertEqual(g.failure_event('session_validation', error), base)
        unrelated = RuntimeError('checkpoint database session exited')
        unrelated.transaction_diagnostic = detail
        self.assertEqual(set(g.failure_event('capture', unrelated)), {'event', 'stage', 'reason'})

    def test_relation_diagnostic_fields_are_exactly_allowlisted(self):
        secret = 'postgres://private:credential@host/db SQL tenant worker token'
        error = g.relation_lock_failure(lock('relation', secret, False, secret), secret)
        detail = error.relation_diagnostic
        self.assertEqual(detail, {'subreason': 'relation_not_allowlisted',
            'relationCategory': 'other', 'modeCategory': 'other',
            'granted': False, 'queryKind': 'unclassified'})
        self.assertNotIn(secret, json.dumps(g.failure_event('session_validation', error)))
        base = {'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
                'reason': 'unsafe_relation_lock'}
        mutations = [detail | {key: value} for key in detail
                     for value in (secret, [], {}, None, 1)]
        mutations += [detail | {'raw': secret}, {key: value for key, value in detail.items()
                      if key != 'subreason'}, [detail], None]
        for invalid in mutations:
            with self.subTest(detail=invalid):
                error.relation_diagnostic = invalid
                self.assertEqual(g.failure_event('session_validation', error), base)
        unrelated = RuntimeError('checkpoint database session exited')
        unrelated.relation_diagnostic = detail
        self.assertEqual(set(g.failure_event('capture', unrelated)), {'event', 'stage', 'reason'})

    def test_relation_failure_emits_once_after_cont_and_all_cleanup(self):
        for cleanup_failure in (False, True):
            with self.subTest(cleanup_failure=cleanup_failure):
                guard = self.guard(asset_kind='computer')
                guard.threads = Mock(return_value=['T'])
                running = task() | {'kind': 'computer_assets.v3.export'}
                queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
                bad = session()
                bad['locks'].append(lock('relation', 'private_mode_token', False, 'private.tenant'))
                observer, barrier = Mock(), Mock(pid=99)
                observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                    {'task': running, 'sessions': [session()]},
                    {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [bad]}]
                barrier.query.return_value = {'count': 1}
                events, lines = [], []
                guard.owned.resume.side_effect = lambda: events.append('resume')
                guard.owned.process.send_signal.side_effect = lambda _: events.append('stop')
                def close_barrier():
                    events.append('barrier_close')
                    if cleanup_failure:
                        raise RuntimeError('private cleanup credential')
                barrier.close.side_effect = close_barrier
                observer.close.side_effect = lambda: events.append('observer_close')
                with patch.object(g, 'ThreadingHTTPServer') as server, patch.object(g.threading, 'Thread'):
                    server.return_value.server_port = 12345
                    guard.start()
                    handler_type = server.call_args.args[1]
                data = json.dumps({'taskID': 'task-one', 'workerID': 'worker'}).encode()
                handler = object.__new__(handler_type)
                handler.client_address = ('127.0.0.1', 1234)
                handler.headers = {'Host': '127.0.0.1:12345', 'Content-Type': 'application/json',
                    'Content-Length': str(len(data)), 'X-ADTR-Guard-Token': guard.token}
                handler.path = '/checkpoint'; handler.rfile = io.BytesIO(data); handler.wfile = io.BytesIO()
                handler.send_response = Mock(); handler.send_header = Mock(); handler.end_headers = Mock()
                def emit(line, **kwargs):
                    events.append('emit'); lines.append(line)
                with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g, 'print', side_effect=emit, create=True):
                    handler.do_POST()
                    guard._emit_failure()
                self.assertEqual(events, ['resume', 'stop', 'resume', 'barrier_close',
                    'observer_close'] + (['resume'] if cleanup_failure else []) + ['emit'])
                self.assertTrue(guard.failed)
                self.assertIsNone(guard.stopped_at)
                handler.send_response.assert_called_once_with(409)
                self.assertEqual(handler.wfile.getvalue(), b'{"error":"running_cancel_guard_failed"}')
                self.assertEqual(len(lines), 1)
                self.assertEqual(json.loads(lines[0]), guard.failure_diagnostic)
                self.assertEqual(guard.failure_diagnostic, {
                    'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
                    'reason': 'unsafe_relation_lock', 'subreason': 'relation_not_allowlisted',
                    'relationCategory': 'other', 'modeCategory': 'other',
                    'granted': False, 'queryKind': 'activity'})
                for raw in ('private', 'task-one', 'worker', guard.token, guard.database_url, g.ACTIVITY_SQL):
                    self.assertNotIn(raw, lines[0])
                observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_transaction_failure_emits_once_after_cont_and_all_cleanup(self):
        for cleanup_failure in (False, True):
            with self.subTest(cleanup_failure=cleanup_failure):
                guard = self.guard(asset_kind='computer')
                guard.threads = Mock(return_value=['T'])
                running = task() | {'kind': 'computer_assets.v3.export'}
                queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
                bad = session()
                bad['query'] = 'SELECT private_credential FROM private.tenant'
                observer, barrier = Mock(), Mock(pid=99)
                observer.query.side_effect = [{'otherNonterminal': 0, 'journalCount': 0, 'task': queued, 'sessions': []},
                    {'task': running, 'sessions': [session()]},
                    {'task': running, 'sessions': [session()]}, {'task': running, 'sessions': [bad]}]
                barrier.query.return_value = {'count': 1}
                events, lines = [], []
                guard.owned.resume.side_effect = lambda: events.append('resume')
                guard.owned.process.send_signal.side_effect = lambda _: events.append('stop')
                def close_barrier():
                    events.append('barrier_close')
                    if cleanup_failure:
                        raise RuntimeError('private cleanup credential')
                barrier.close.side_effect = close_barrier
                observer.close.side_effect = lambda: events.append('observer_close')
                with patch.object(g, 'ThreadingHTTPServer') as server, patch.object(g.threading, 'Thread'):
                    server.return_value.server_port = 12345
                    guard.start()
                    handler_type = server.call_args.args[1]
                data = json.dumps({'taskID': 'task-one', 'workerID': 'worker'}).encode()
                handler = object.__new__(handler_type)
                handler.client_address = ('127.0.0.1', 1234)
                handler.headers = {'Host': '127.0.0.1:12345', 'Content-Type': 'application/json',
                    'Content-Length': str(len(data)), 'X-ADTR-Guard-Token': guard.token}
                handler.path = '/checkpoint'; handler.rfile = io.BytesIO(data); handler.wfile = io.BytesIO()
                handler.send_response = Mock(); handler.send_header = Mock(); handler.end_headers = Mock()
                def emit(line, **kwargs):
                    events.append('emit'); lines.append(line)
                with patch.object(g, 'Psql', side_effect=[observer, barrier]), patch.object(g, 'print', side_effect=emit, create=True):
                    handler.do_POST()
                    guard._emit_failure()
                self.assertEqual(events, ['resume', 'stop', 'resume', 'barrier_close',
                    'observer_close'] + (['resume'] if cleanup_failure else []) + ['emit'])
                self.assertTrue(guard.failed)
                self.assertIsNone(guard.stopped_at)
                handler.send_response.assert_called_once_with(409)
                self.assertEqual(handler.wfile.getvalue(), b'{"error":"running_cancel_guard_failed"}')
                self.assertEqual(len(lines), 1)
                self.assertEqual(json.loads(lines[0]), guard.failure_diagnostic)
                self.assertEqual(guard.failure_diagnostic, {
                    'event': 'running_cancel_guard_failure', 'stage': 'session_validation',
                    'reason': 'unsafe_transaction_query', 'queryCategory': 'other',
                    'lockSafetyCategory': 'unsafe_transactionid',
                    'stateCategory': 'active', 'waitTypeCategory': 'Lock', 'locksPresent': True})
                for raw in ('private', 'task-one', 'worker', guard.token, guard.database_url, g.ACTIVITY_SQL):
                    self.assertNotIn(raw, lines[0])
                observer.close.assert_called_once(); barrier.close.assert_called_once()

    def test_first_failure_survives_cleanup_failure_without_raw_output(self):
        guard = self.guard(asset_kind='computer'); guard.threads = Mock(return_value=['T'])
        observer = Mock()
        observer.query.side_effect = RuntimeError('checkpoint database observation unavailable')
        observer.close.side_effect = RuntimeError('sensitive cleanup exception')
        with patch.object(g, 'Psql', return_value=observer), patch.object(g.sys, 'stderr', new_callable=io.StringIO) as output:
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
            guard._report_failure('http_request', RuntimeError('later failure'))
        self.assertEqual(guard.failure_diagnostic, {'event': 'running_cancel_guard_failure',
            'stage': 'initial_observation', 'reason': 'database_observation_unavailable'})
        self.assertEqual(output.getvalue(), '')  # Never log before CONT/session cleanup.
        self.assertNotIn('sensitive', output.getvalue())
        self.assertEqual(guard.owned.resume.call_count, 2)
        observer.close.assert_called_once()

    def test_failed_diagnostic_output_cannot_prevent_fail_closed_cleanup(self):
        guard = self.guard(asset_kind='computer'); guard.threads = Mock(return_value=['T'])
        observer = Mock(); observer.query.side_effect = RuntimeError('checkpoint database session exited')
        with patch.object(g, 'Psql', return_value=observer), patch.object(g, 'print', side_effect=OSError('output unavailable'), create=True):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
            guard._emit_failure()
        self.assertTrue(guard.failed)
        self.assertEqual(guard.failure_diagnostic['reason'], 'database_session_exited')
        guard.owned.resume.assert_called_once()
        observer.close.assert_called_once()


if __name__ == '__main__':
    unittest.main()
