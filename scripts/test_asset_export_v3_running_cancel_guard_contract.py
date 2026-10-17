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
            'attempt': 1, 'progress': 10, 'snapshotCount': 1, 'manifestCount': 0,
            'chunkCount': 0, 'fencingToken': 1, 'leaseOwner': 'worker-0',
            'databaseClock': 100., 'leaseUntilEpoch': 130., 'leaseRemainingSeconds': 30.}


class ContractTests(unittest.TestCase):
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
        guard.owned = factory.return_value
        guard.owned.assert_identity = Mock(return_value="T")
        return guard

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
        observer.query.side_effect = [{'task': initial, 'sessions': []}, RuntimeError('missed')]
        barrier.query.return_value = {'count': 1}
        events = []
        observer.query.side_effect = lambda *a, **k: events.append('observe') or ({'task': initial, 'sessions': []} if len(events) == 1 else (_ for _ in ()).throw(RuntimeError('missed')))
        guard.owned.resume.side_effect = lambda: events.append('resume')
        with patch.object(g, 'Psql', side_effect=[observer, barrier]):
            with self.assertRaises(RuntimeError):
                guard.checkpoint('task-one', 'worker')
        self.assertEqual(events[:3], ['observe', 'resume', 'observe'])
        self.assertEqual(events[-1], 'resume')
        observer.close.assert_called_once(); barrier.close.assert_called_once()
        self.assertTrue(guard.failed)

    def test_complete_checkpoint_locks_existing_row_and_releases(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'] * 4)
        queued = task(); queued.update(state='queued', attempt=0, snapshotCount=0)
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'task': queued, 'sessions': []},
                                      {'task': task(), 'sessions': [session()]},
                                      {'task': task(), 'sessions': [session()]}]
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

    def test_barrier_cleanup_failure_still_closes_observer(self):
        guard = self.guard(); guard.threads = Mock(return_value=['T'])
        queued = task(); queued.update(state='queued', attempt=0, snapshotCount=0)
        observer, barrier = Mock(), Mock(pid=99)
        observer.query.side_effect = [{'task': queued, 'sessions': []}, RuntimeError('missed')]
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
        self.assertIn("worker_id='\" + worker_id", source)
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

    def test_complete_checkpoint_each_new_kind_and_transitive_blockers(self):
        for kind in ('group', 'computer'):
            guard = self.guard(asset_kind=kind); guard.threads = Mock(return_value=['T'] * 4)
            running = task() | {'kind': kind + '_assets.v3.export'}
            queued = running | {'state': 'queued', 'attempt': 0, 'snapshotCount': 0}
            rows = [session(100), session(101)]
            rows[1]['blockers'] = [100]
            rows[1]['locks'][-1] = lock('tuple', 'ExclusiveLock', False, 'adtr.system_worker_activity')
            observer, barrier = Mock(), Mock(pid=99)
            observer.query.side_effect = [{'task': queued, 'sessions': []},
                {'task': running, 'sessions': rows}, {'task': running, 'sessions': rows}]
            barrier.query.side_effect = [{'count': 1}, True]
            with patch.object(g, 'Psql', side_effect=[observer, barrier]):
                result = guard.checkpoint('task-one', 'worker')
            self.assertEqual(result['kind'], running['kind'])
            self.assertEqual(result['blockedOwnedBackends'], 2)
            self.assertTrue(result['allThreadsStopped'])
            self.assertTrue(result['activityLockReleased'])
            guard.owned.process.send_signal.assert_called_once_with(signal.SIGSTOP)
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
            observer.query.return_value = {'task': queued, 'sessions': []}
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
            observer.query.side_effect = [{'task': queued, 'sessions': []}, {'task': running, 'sessions': [session()]}]
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
                observer.query.side_effect = [{'task': queued, 'sessions': []},
                    {'task': running, 'sessions': [session()]},
                    {'task': running, 'sessions': [bad]}]
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
                observer.query.side_effect = [{'task': queued, 'sessions': []},
                    {'task': running, 'sessions': [session()]},
                    {'task': running, 'sessions': [bad]}]
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
