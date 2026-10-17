"""Explicit CI regression of the real Python Psql fixture lifecycle.

Uses only a new adtr_e2e database in the integration runner's disposable
container. This synthetic table test is not worker/capture acceptance proof.
No container creation, image pull, host connection, or existing database reset.
"""
import argparse
import base64
import hashlib
import os
import re
import subprocess
import sys
from types import SimpleNamespace
import unittest

from asset_export_v3_running_cancel_guard import Psql, RunningExportCancelGuard

CONTAINER = None
SCHEMA = """CREATE SCHEMA adtr;
CREATE TABLE adtr.system_worker_activity (
 tenant_id text NOT NULL, worker_id text NOT NULL, cycle text NOT NULL CHECK(cycle IN ('queue','recovery')),
 status text NOT NULL CHECK(status IN ('success','failure','progress')),
 error_code text NOT NULL DEFAULT '',
 last_activity_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_success_at timestamptz,
 PRIMARY KEY(tenant_id,worker_id,cycle),
 CHECK(status<>'progress' OR cycle='queue'),
 CHECK((status='failure' AND error_code IN ('cycle_failed','database_unavailable','execution_failed','schema_incompatible'))
    OR (status IN ('success','progress') AND error_code=''))
); SELECT to_json(1)"""


def alias(nonce, task):
    digest = hashlib.sha256(b'adtr-ci-activity-v1\x00' + nonce + b'\x00' + task.encode()).digest()
    return 'ci-activity-' + base64.b32encode(digest).decode().lower().rstrip('=')


class ActivityFixturePsql(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # createdb fails if the database already exists: never reuse/reset it.
        subprocess.run(['docker', 'exec', CONTAINER, 'createdb', '-U', 'postgres', 'adtr_e2e'],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        connection = Psql(CONTAINER, False)
        try:
            if connection.query(SCHEMA) != 1:
                raise RuntimeError('activity regression schema failed')
        finally:
            connection.close()

    def connection(self, readonly):
        connection = Psql(CONTAINER, readonly)
        self.addCleanup(connection.close)
        return connection

    def guard(self, task):
        guard = RunningExportCancelGuard.__new__(RunningExportCancelGuard)
        guard.container, guard.nonce = CONTAINER, bytes(range(16))
        guard.activity_fixture_created = False
        guard.controller = SimpleNamespace(prepare_target=lambda task_id, worker_id, deadline:
                                           alias(guard.nonce, task_id))
        # Only the guard's post-Wait precondition is synthetic; Psql is real.
        guard.owned = SimpleNamespace(process=SimpleNamespace(poll=lambda: 0))
        return guard

    def test_writer_insert_readonly_observe_lock_and_writer_cleanup(self):
        guard = self.guard('lifecycle')
        writer, observer = self.connection(False), self.connection(True)
        self.assertEqual(writer.query("WITH i AS (INSERT INTO adtr.system_worker_activity VALUES"
            "('default','ordinary-lifecycle','queue','success','',clock_timestamp(),clock_timestamp()) RETURNING 1) SELECT to_json(count(*)) FROM i"), 1)
        guard.prepare_activity_fixture('lifecycle', 'ordinary-lifecycle')
        self.assertTrue(guard.activity_fixture_created)
        key = guard.activity_key
        self.assertEqual(observer.query("SELECT jsonb_build_object('readonly',current_setting('default_transaction_read_only'),"
            "'status',status,'error',error_code,'success',last_success_at) FROM adtr.system_worker_activity WHERE worker_id='" + key + "'"),
            {'readonly': 'on', 'status': 'failure', 'error': 'cycle_failed', 'success': None})
        barrier = self.connection(False)
        self.assertEqual(barrier.query("BEGIN; SELECT to_json(count(*)) FROM (SELECT worker_id FROM adtr.system_worker_activity WHERE worker_id='" + key + "' FOR UPDATE) locked"), 1)
        # NOWAIT proves another actual backend cannot acquire this exact row.
        contender = self.connection(False)
        with self.assertRaisesRegex(RuntimeError, '^checkpoint database session ended$'):
            contender.query("SELECT to_json(count(*)) FROM (SELECT worker_id FROM adtr.system_worker_activity WHERE worker_id='" + key + "' FOR UPDATE NOWAIT) locked")
        contender.process.wait(timeout=2)
        self.assertNotEqual(contender.process.returncode, 0)
        barrier.close()  # EOF rollback releases the real row lock.
        unlocked = self.connection(False)
        self.assertEqual(unlocked.query("SELECT to_json(count(*)) FROM (SELECT worker_id FROM adtr.system_worker_activity WHERE worker_id='" + key + "' FOR UPDATE NOWAIT) locked"), 1)
        guard.cleanup_activity_fixture_after_wait(0)
        self.assertFalse(guard.activity_fixture_created)
        self.assertEqual(observer.query("SELECT to_json(count(*)) FROM adtr.system_worker_activity WHERE worker_id='" + key + "'"), 0)
        self.assertEqual(observer.query("SELECT to_json(count(*)) FROM adtr.system_worker_activity WHERE worker_id='ordinary-lifecycle'"), 1)

    def test_collision_is_fatal_and_never_deleted(self):
        guard = self.guard('collision')
        writer = self.connection(False)
        key = alias(guard.nonce, 'collision')
        self.assertEqual(writer.query("WITH i AS (INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code) VALUES('default','" + key + "','queue','failure','cycle_failed') RETURNING 1) SELECT to_json(count(*)) FROM i"), 1)
        with self.assertRaises(RuntimeError):
            guard.prepare_activity_fixture('collision', 'ordinary-collision')
        self.assertFalse(guard.activity_fixture_created)
        guard.cleanup_activity_fixture_after_wait(0)
        self.assertEqual(writer.query("SELECT to_json(count(*)) FROM adtr.system_worker_activity WHERE worker_id='" + key + "'"), 1)

    def test_readonly_insert_ends_real_psql_session(self):
        observer = self.connection(True)
        with self.assertRaisesRegex(RuntimeError, '^checkpoint database session ended$'):
            observer.query("WITH i AS (INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code) VALUES('default','readonly-negative','queue','failure','cycle_failed') RETURNING 1) SELECT to_json(count(*)) FROM i")
        observer.process.wait(timeout=2)
        self.assertNotEqual(observer.process.returncode, 0)
        verifier = self.connection(True)
        self.assertEqual(verifier.query("SELECT to_json(count(*)) FROM adtr.system_worker_activity WHERE worker_id='readonly-negative'"), 0)


def main():
    global CONTAINER
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    args = parser.parse_args()
    if (os.environ.get('ADTR_RUN_ACTIVITY_PSQL_TESTS') != '1' or sys.platform != 'linux'
            or re.fullmatch(r'adtr-auth-e2e-[0-9a-f]{12}', args.container) is None):
        raise SystemExit('explicit isolated Linux Psql test opt-in required')
    CONTAINER = args.container
    # Print only test totals: raw subprocess failures/SQL/identities stay private.
    result = unittest.TestResult()
    unittest.defaultTestLoader.loadTestsFromTestCase(ActivityFixturePsql).run(result)
    print('activity_fixture_psql: tests=%d failures=%d errors=%d' %
          (result.testsRun, len(result.failures), len(result.errors)))
    return 0 if result.wasSuccessful() and result.testsRun == 3 else 1


if __name__ == '__main__':
    raise SystemExit(main())
