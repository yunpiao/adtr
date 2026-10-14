"""CI-only, first-asset-export checkpoint barrier. Never imported by production.

Only SELECT FOR UPDATE of the pre-existing owned queue activity row mutates
lock state. Persistent psql sessions avoid docker startup in the 2s callback.
All identifiers are validated; credentials are never accepted or serialized.
"""
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import re
import secrets
import signal
import subprocess
import threading
import time
from urllib.parse import parse_qsl, urlsplit
from types import MappingProxyType

from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard

ACTIVITY_SQL = """WITH observed AS (SELECT clock_timestamp() AS at)
INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code,last_activity_at,last_success_at)
SELECT $1,$2,$3,$4,$5,at,CASE WHEN $4='success' THEN at ELSE NULL END FROM observed
ON CONFLICT(tenant_id,worker_id,cycle) DO UPDATE SET status=EXCLUDED.status,error_code=EXCLUDED.error_code,
 last_activity_at=EXCLUDED.last_activity_at,
 last_success_at=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.last_activity_at ELSE adtr.system_worker_activity.last_success_at END"""
SCHEMA_SQL = 'SELECT version FROM adtr.schema_version WHERE singleton=true FOR SHARE'
GATE_SQL = 'SELECT pg_advisory_xact_lock_shared($1::bigint)'
RELATIONS = {'adtr.schema_version', 'adtr.schema_version_pkey',
             'adtr.system_worker_activity', 'adtr.system_worker_activity_pkey',
             'adtr.system_worker_activity_recent'}


# Runner-only selection. Never accept task kinds, table names or SQL via HTTP.
EXPORT_KINDS = MappingProxyType({
    'user': ('user_assets.v3.export', 'adtr.user_asset_export_snapshots',
             'adtr.user_asset_export_manifests', 'adtr.user_asset_export_chunks'),
    'group': ('group_assets.v3.export', 'adtr.group_asset_export_snapshots',
              'adtr.group_asset_export_manifests', 'adtr.group_asset_export_chunks'),
    'computer': ('computer_assets.v3.export', 'adtr.computer_asset_export_snapshots',
                 'adtr.computer_asset_export_manifests', 'adtr.computer_asset_export_chunks'),
})


def export_kind(asset_kind):
    require(isinstance(asset_kind, str) and asset_kind in EXPORT_KINDS,
            'unsupported export asset kind')
    return EXPORT_KINDS[asset_kind]


def require(value, message):
    if not value:
        raise RuntimeError(message)


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


def identifier(value):
    require(isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', value), 'invalid checkpoint identity')
    return value


def normalized(sql):
    return ' '.join(sql.split())


class Psql:
    """One persistent bounded connection, with no DSN in argv or output."""
    def __init__(self, container, readonly):
        require(re.fullmatch(r'adtr-auth-e2e-[0-9a-f]{12}', container), 'isolated database required')
        options = '-c statement_timeout=1000 -c idle_in_transaction_session_timeout=8000'
        if readonly:
            options += ' -c default_transaction_read_only=on'
        self.process = subprocess.Popen(['docker', 'exec', '-i', '-e', 'PGOPTIONS=' + options,
            container, 'psql', '-U', 'postgres', '-d', 'adtr_e2e', '-X', '-q', '-A', '-t',
            '-v', 'ON_ERROR_STOP=1'], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, text=True, bufsize=1)
        self.lines = queue.Queue()
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        try:
            self.pid = self.query('SELECT to_json(pg_backend_pid())')
        except Exception:
            self.close()
            raise

    def _read(self):
        try:
            for line in self.process.stdout:
                self.lines.put(line)
        finally:
            self.lines.put(None)

    def query(self, sql, timeout=1.5):
        require(self.process.poll() is None, 'checkpoint database session exited')
        self.process.stdin.write(sql + ';\n')
        self.process.stdin.flush()
        try:
            line = self.lines.get(timeout=timeout)
            require(line is not None, 'checkpoint database session ended')
            return json.loads(line)
        except (queue.Empty, ValueError) as error:
            raise RuntimeError('checkpoint database observation unavailable') from error

    def close(self):
        if self.process.poll() is None:
            self.process.stdin.close()  # EOF rolls back the barrier, never commits data.
            try:
                self.process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.process.terminate()
                self.process.wait(timeout=2)
        self.reader.join(timeout=2)


def validate_sessions(sessions, lock_pid):
    """Reject ANY tenant, authorization, task, domain or unexpected transaction.

    Four slots can all be in their real activity callback. A fifth scheduler or
    recovery session is accepted only if its exact locks/query are also safe.
    No sample count is treated as proof that unobserved sessions are safe.
    """
    require(isinstance(sessions, list), 'missing complete session sample')
    require(len({s['pid'] for s in sessions}) == len(sessions), 'duplicate owned backend')
    reachable = {lock_pid}
    # PostgreSQL serializes tuple updaters: later slots can wait behind an owned
    # updater rather than directly on the harness transaction. Every edge must
    # terminate at the exact barrier, never at an unrelated session or cycle.
    for _ in sessions:
        for session in sessions:
            if (session['blockers'] and set(session['blockers']) <= reachable and
                    normalized(session['query']) == normalized(ACTIVITY_SQL) and
                    session['transaction'] and session['waitType'] == 'Lock'):
                reachable.add(session['pid'])
    safe, blocked, transactions = [], 0, 0
    for session in sessions:
        locks = session['locks']
        kind = {normalized(ACTIVITY_SQL): 'activity', normalized(SCHEMA_SQL): 'schema',
                normalized(GATE_SQL): 'schemaGate'}.get(normalized(session['query']), 'idle')
        active = session['transaction']
        if active:
            require(kind != 'idle', 'unsafe owned transaction query')
            transactions += 1
        else:
            require(session['state'] == 'idle' and not locks, 'nonidle unclassified owned session')
        for lock in locks:
            typ, mode = lock['type'], lock['mode']
            if typ == 'relation':
                require(lock['relation'] in RELATIONS and lock['granted'] and mode in
                        ('AccessShareLock', 'RowShareLock', 'RowExclusiveLock'), 'unsafe owned relation lock')
            elif typ == 'advisory':
                require(lock['classid'] == '0' and lock['objid'] == '734192801' and
                        lock['objsubid'] == 1 and mode == 'ShareLock' and lock['granted'],
                        'unsafe owned advisory lock')
            elif typ == 'virtualxid':
                require(mode == 'ExclusiveLock' and lock['granted'], 'unsafe virtual transaction lock')
            elif typ == 'transactionid':
                require((mode == 'ExclusiveLock' and lock['granted']) or
                        (kind == 'activity' and mode == 'ShareLock' and not lock['granted'] and
                         session['pid'] in reachable), 'unsafe transaction lock')
            elif typ == 'tuple':
                require(kind == 'activity' and lock['relation'] == 'adtr.system_worker_activity'
                        and mode in ('ExclusiveLock', 'AccessExclusiveLock') and
                        session['pid'] in reachable, 'unsafe tuple lock')
            else:
                raise RuntimeError('unrecognized owned lock')
        if session['blockers']:
            require(session['pid'] in reachable, 'unowned or cyclic barrier blocker')
            require(kind == 'activity' and active and session['waitType'] == 'Lock', 'unexpected barrier waiter')
            blocked += 1
        safe.append({k: session[k] for k in ('pid', 'transaction', 'state', 'waitType', 'blockers', 'locks')} | {'queryKind': kind, 'querySHA256': digest(normalized(session['query']))})
    require(blocked > 0, 'missed actual activity checkpoint')
    return safe, blocked, transactions


def validate_checkpoint(row, task_id, worker_id, asset_kind='user'):
    task_kind = export_kind(asset_kind)[0]
    require(row and row['taskID'] == task_id and row['kind'] == task_kind, 'wrong exporter checkpoint')
    require(row['state'] == 'running' and row['attempt'] == 1 and row['progress'] == 10,
            'exporter did not enter first fenced transaction')
    require(row['snapshotCount'] == 1 and row['manifestCount'] == 0 and row['chunkCount'] == 0,
            'snapshot boundary missed')
    require(row['fencingToken'] > 0 and row['leaseOwner'] in [worker_id + '-' + str(n) for n in range(4)],
            'checkpoint fence is not owned by a configured slot')
    require(row['leaseRemainingSeconds'] > 20 and
            abs(row['leaseUntilEpoch'] - row['databaseClock'] - row['leaseRemainingSeconds']) < .01,
            'insufficient actual database-clock lease')
    return {k: v for k, v in row.items() if k != 'leaseOwner'} | {'leaseOwnerSHA256': digest(row['leaseOwner'])}


class RunningExportCancelGuard:
    MAX_RUNNING_STOP_SECONDS = 10.0

    def __init__(self, process, nonce, binary, database_container, application_name, database_url, proc_root=Path('/proc'), *, asset_kind='user'):
        export_kind(asset_kind)  # Reject invalid selectors before creating any guard resources.
        self.asset_kind = asset_kind
        self.owned = OwnedExportWorkerGuard(process, nonce, binary, proc_root)
        require(application_name == 'adtr-user-export-' + nonce and re.fullmatch('[0-9a-f]{32}', nonce), 'invalid application identity')
        self.application_name, self.container = application_name, database_container
        self.database_url = database_url
        self.token = secrets.token_hex(32)
        self.failed = False
        self.stopped_at = None
        self.used = False
        self._done = threading.Event()
        self._mutex = threading.RLock()
        self.server = None
        self._watcher = None

    def threads(self):
        self.owned.assert_identity()
        root = self.owned.root / str(self.owned.process.pid)
        env = (root / 'environ').read_bytes().split(b'\0')
        db = [v[len(b'ADTR_DATABASE_URL='):].decode() for v in env if v.startswith(b'ADTR_DATABASE_URL=')]
        require(len(db) == 1, 'missing owned database identity')
        base, worker = urlsplit(self.database_url), urlsplit(db[0])
        pairs = parse_qsl(worker.query, keep_blank_values=True)
        require(base.scheme in ('postgres', 'postgresql') and base.path == '/adtr_e2e' and
                worker._replace(query='') == base._replace(query='') and
                [v for k, v in pairs if k == 'application_name'] == [self.application_name] and
                [p for p in pairs if p[0] != 'application_name'] == parse_qsl(base.query, keep_blank_values=True) and
                not any(k == 'application_name' for k, _ in parse_qsl(base.query)), 'owned database mismatch')
        states = []
        for thread in (root / 'task').iterdir():
            require(thread.name.isdigit(), 'invalid owned thread')
            match = re.search(r'^State:\s+(\S)', (thread / 'status').read_text(), re.M)
            require(match, 'missing owned thread state')
            states.append(match[1])
        require(states, 'missing owned threads')
        return states

    def observe_sql(self, task_id):
        identifier(task_id)
        _, snapshots, manifests, chunks = export_kind(self.asset_kind)
        return f"""SELECT jsonb_build_object('task', (SELECT jsonb_build_object(
 'taskID',t.task_id,'kind',t.kind,'state',t.state,'attempt',t.attempt,'progress',t.progress,
 'fencingToken',t.fencing_token,'leaseOwner',t.lease_owner,
 'databaseClock',extract(epoch FROM n.at),'leaseUntilEpoch',extract(epoch FROM t.lease_until),
 'leaseRemainingSeconds',extract(epoch FROM t.lease_until-n.at),
 'snapshotCount',(SELECT count(*) FROM {snapshots} WHERE task_id=t.task_id),
 'manifestCount',(SELECT count(*) FROM {manifests} WHERE task_id=t.task_id),
 'chunkCount',(SELECT count(*) FROM {chunks} WHERE task_id=t.task_id))
 FROM adtr.tasks t CROSS JOIN (SELECT clock_timestamp() AS at) n
 WHERE tenant_id='default' AND task_id='{task_id}'),
 'sessions',COALESCE((SELECT jsonb_agg(jsonb_build_object('pid',a.pid,'transaction',a.xact_start IS NOT NULL,
 'state',a.state,'query',a.query,'waitType',a.wait_event_type,'blockers',pg_blocking_pids(a.pid),
 'locks',COALESCE((SELECT jsonb_agg(jsonb_build_object('type',l.locktype,'mode',l.mode,'granted',l.granted,
 'relation',CASE WHEN l.relation IS NOT NULL THEN l.relation::regclass::text ELSE NULL END,
 'classid',l.classid::text,'objid',l.objid::text,'objsubid',l.objsubid)) FROM pg_locks l WHERE l.pid=a.pid),'[]'::jsonb)))
 FROM pg_stat_activity a WHERE datname=current_database() AND application_name='{self.application_name}'),'[]'::jsonb))"""

    def checkpoint(self, task_id, worker_id):
        identifier(task_id); identifier(worker_id)
        require(not self.used and not self.failed, 'checkpoint may only run once')
        self.used = True
        require(all(s == 'T' for s in self.threads()), 'initial strict queued pause required')
        lock = observer = None
        try:
            observer = Psql(self.container, True)
            # Persistent observer exists AND successfully sampled before CONT.
            initial = observer.query(self.observe_sql(task_id))
            require(initial['task'] and initial['task']['kind'] == export_kind(self.asset_kind)[0] and initial['task']['state'] == 'queued' and initial['task']['attempt'] == 0
                    and initial['task']['snapshotCount'] == 0 and
                    all(not s['transaction'] for s in initial['sessions']), 'initial queued transaction-free boundary missed')
            lock = Psql(self.container, False)
            locked = lock.query("BEGIN; SELECT jsonb_build_object('count',count(*)) FROM (SELECT worker_id FROM adtr.system_worker_activity WHERE tenant_id='default' AND worker_id='" + worker_id + "' AND cycle='queue' FOR UPDATE) owned")
            require(locked['count'] == 1, 'exact pre-existing activity row required')
            observer_started = time.monotonic()
            self.owned.resume()
            resumed = time.monotonic()
            deadline = resumed + 1.5  # fail closed before the production 2s callback expires
            sample = None
            while time.monotonic() < deadline:
                sample = observer.query(self.observe_sql(task_id), timeout=.5)
                task = sample['task']
                if task and task['state'] == 'running' and task['progress'] == 10 and task['snapshotCount'] == 1 and any(lock.pid in s['blockers'] and normalized(s['query']) == normalized(ACTIVITY_SQL) for s in sample['sessions']):
                    observed = time.monotonic()
                    self.owned.assert_identity()
                    issued = time.monotonic()
                    with self._mutex:
                        self.stopped_at = issued
                        self.owned.process.send_signal(signal.SIGSTOP)
                    break
                time.sleep(.002)
            else:
                raise RuntimeError('missed two-second activity checkpoint')
            while time.monotonic() < issued + .5:
                states = self.threads()
                if all(s == 'T' for s in states):
                    break
                time.sleep(.002)
            require(all(s == 'T' for s in states), 'not all owned threads stopped')
            sample = observer.query(self.observe_sql(task_id), timeout=.5)
            task = validate_checkpoint(sample['task'], task_id, worker_id, self.asset_kind)
            sessions, blocked, txs = validate_sessions(sample['sessions'], lock.pid)
            require(time.monotonic() < deadline, 'activity checkpoint exceeded safe callback window')
            released = lock.query("ROLLBACK; SELECT to_json(true)", timeout=.5)
            require(released is True, 'activity barrier release unverified')
            return task | {'workerIDSHA256': digest(worker_id), 'applicationNameSHA256': digest(self.application_name),
                'allThreadsStopped': True, 'threadCount': len(states), 'ownedSessionCount': len(sessions),
                'allowedActivityTransactions': txs, 'unsafeTransactions': 0, 'blockedOwnedBackends': blocked,
                'activityLockReleased': True, 'observerStartedBeforeResume': True, 'checkpointCaught': True,
                'observerStartedMonotonic': observer_started, 'resumedForCheckpointMonotonic': resumed,
                'checkpointObservedMonotonic': observed, 'stopIssuedMonotonic': issued,
                'stopStartedMonotonic': issued, 'barrierBackendPID': lock.pid, 'sessions': sessions}
        except Exception:
            self.failed = True
            self.resume()
            raise RuntimeError('running checkpoint guard failed closed') from None
        finally:
            try:
                try:
                    if lock is not None:
                        lock.close()
                finally:
                    if observer is not None:
                        observer.close()
            except Exception:
                self.failed = True
                self.resume()
                raise RuntimeError('checkpoint session cleanup failed') from None

    def resume(self):
        with self._mutex:
            now = time.monotonic()
            duration = 0 if self.stopped_at is None else now - self.stopped_at
            started = self.stopped_at
            self.owned.resume()
            self.stopped_at = None
            if duration >= self.MAX_RUNNING_STOP_SECONDS:
                self.failed = True
            return {'resumed': True, 'runningStoppedSeconds': duration, 'watchdogExpired': self.failed,
                    'stopStartedMonotonic': started, 'resumedMonotonic': now}

    def _watch(self):
        while not self._done.wait(.025):
            with self._mutex:
                if self.stopped_at is not None and time.monotonic() - self.stopped_at >= 9.0:
                    self.failed = True
                    self.resume()

    def start(self):
        guard = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass  # No token, request or SQL logs.

            def do_POST(self):
                try:
                    require(self.client_address[0] == '127.0.0.1' and
                            self.headers.get('Host') == guard.url.removeprefix('http://') and
                            self.headers.get('Origin') in (None, guard.url) and
                            self.headers.get('Content-Type') == 'application/json' and
                            hmac.compare_digest(self.headers.get('X-ADTR-Guard-Token', ''), guard.token), 'invalid guard request')
                    length = int(self.headers.get('Content-Length', '0'))
                    require(0 < length <= 1024 and self.headers.get('Transfer-Encoding') is None, 'invalid control body')
                    data = json.loads(self.rfile.read(length))
                    if self.path == '/checkpoint' and set(data) == {'taskID', 'workerID'}:
                        result = guard.checkpoint(data['taskID'], data['workerID'])
                    elif self.path == '/resume' and data == {}:
                        result = guard.resume()
                    elif self.path == '/status' and data == {}:
                        result = {'failed': guard.failed, 'runningStopped': guard.stopped_at is not None}
                    else:
                        raise RuntimeError('unknown guard command')
                    body = json.dumps(result).encode()
                    self.send_response(200)
                except Exception:
                    body = b'{"error":"running_cancel_guard_failed"}'
                    self.send_response(409)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self._watcher = threading.Thread(target=self._watch, daemon=True)
        self._watcher.start()
        return self

    def check(self):
        require(not self.failed and self.stopped_at is None and self.used, 'running checkpoint guard incomplete or failed')
        self.owned.check()

    def close(self):
        try:
            self.resume()  # CONT precedes server shutdown, joins and failure reporting.
        finally:
            self._done.set()
            try:
                if self.server is not None:
                    self.server.shutdown()
                    self.server.server_close()
            finally:
                if self._watcher is not None:
                    self._watcher.join(timeout=2)
        require(not self.failed, 'running checkpoint guard failed')
