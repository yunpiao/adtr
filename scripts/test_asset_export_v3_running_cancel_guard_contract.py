"""Offline mocked contracts: no real process, database, socket or signal."""
import copy
import json
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

    def guard(self):
        with patch.object(g, 'OwnedExportWorkerGuard') as factory:
            guard = g.RunningExportCancelGuard(Mock(), 'a'*32, '/fake/bin/adtr',
                'adtr-auth-e2e-'+'a'*12, 'adtr-user-export-'+'a'*32, 'postgres://localhost/adtr_e2e')
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


if __name__ == '__main__':
    unittest.main()
