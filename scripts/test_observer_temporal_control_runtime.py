"""Explicit CI-only synthetic temporal control, not production failure reproduction.

Static EXPLAIN controls and the single gated SELECT are different executions.
Only closed facts leave this process; original rows, SQL and identities do not.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import queue
import re
import subprocess
import sys
import threading
import time
from types import SimpleNamespace
from urllib.parse import urlsplit, urlunsplit

import test_observer_plan_runtime as plan_fixture
from asset_export_v3_running_cancel_guard import RunningExportCancelGuard

DATABASE = 'adtr_observer_temporal'
SCHEMA = 'observer_temporal'
MARKER = SCHEMA + '.marker'
APPLICATION = plan_fixture.APPLICATION
TASK = plan_fixture.TASK
GATE_NAMESPACE = 734192977
TRANSITION_SECONDS = .75
POLL_LIMIT = 16
POLL_SLEEP = .005
WORK_SECONDS = 100
CLEANUP_SECONDS = 118
ROLES = ('a', 'b')
LOCK_TYPES = frozenset(('relation', 'extend', 'frozenid', 'page', 'tuple', 'transactionid',
                        'virtualxid', 'spectoken', 'object', 'userlock', 'advisory', 'applytransaction'))
LOCK_MODES = frozenset(('AccessShareLock', 'RowShareLock', 'RowExclusiveLock',
                        'ShareUpdateExclusiveLock', 'ShareLock', 'ShareRowExclusiveLock',
                        'ExclusiveLock', 'AccessExclusiveLock', 'SIReadLock'))
CLIENT_ROLES = (*ROLES, 'controller', 'observer', 'verifier')
REASONS = frozenset(('schema', 'inputs', 'protocol', 'deadline_exceeded',
                     'inconclusive_plan_change', 'inconclusive_ordering',
                     'inconclusive_later_read_not_established', 'ownership', 'unexpected_failure'))
EVENTS = frozenset(('marker_zero_before_savepoints', 'markers_acquired', 'coherent_before',
                    'first_row_gate_observed', 'rollback_ack', 'marker_zero_before_barrier',
                    'barrier_confirmed', 'marker_zero_after_barrier', 'gates_released',
                    'gated_result', 'coherent_after'))
OUTCOMES = frozenset(('controlled_cross_time_combination_observed',
                      'controlled_combination_not_observed'))
GATE_BODY = f"""BEGIN
 IF actor_role NOT IN (1,2) OR payload IS NULL THEN
  RAISE EXCEPTION 'temporal control rejected';
 END IF;
 PERFORM pg_advisory_xact_lock({GATE_NAMESPACE}, actor_role);
 RETURN payload;
END;"""


class Fault(RuntimeError):
    def __init__(self, reason='schema'):
        super().__init__('temporal control rejected')
        self.reason = reason if type(reason) is str and reason in REASONS else 'unexpected_failure'


def need(condition, reason='schema'):
    if not condition:
        raise Fault(reason)


def integer(value, maximum=2**32 - 1, minimum=0):
    need(type(value) is int and minimum <= value <= maximum)
    return value


def remaining(deadline):
    value = deadline - time.monotonic()
    need(value > 0, 'deadline_exceeded')
    return value


def decode(value):
    try:
        return plan_fixture.unique_json(value)
    except Exception:
        raise Fault('protocol') from None


def psql_command(container, role):
    need(type(container) is str and re.fullmatch(r'adtr-auth-e2e-[0-9a-f]{12}', container), 'inputs')
    need(role in CLIENT_ROLES, 'inputs')
    application = APPLICATION if role in ROLES else 'adtr-temporal-' + role
    readonly = role in ('observer', 'verifier')
    options = ('-c statement_timeout=1000 -c idle_in_transaction_session_timeout=8000 '
               '-c default_transaction_read_only=' + ('on' if readonly else 'off'))
    return ['docker', 'exec', '-i', '-e', 'PGOPTIONS=' + options, '-e', 'PGAPPNAME=' + application,
            container, 'psql', '-U', 'postgres', '-d', DATABASE, '-X', '-q', '-A', '-t', '-0',
            '-v', 'ON_ERROR_STOP=1']


def reap_process(process, deadline):
    """Every spawned process gets kill/reap attempts even after the shared limit."""
    clean = True
    try:
        process.stdin.close()
    except Exception:
        clean = False
    for action in (None, process.terminate, process.kill):
        try:
            if action is not None:
                action()
                clean = False
            # Zero-time wait is still a reap attempt, never permission to omit a child.
            process.wait(timeout=max(0, min(.4, deadline - time.monotonic())))
            return clean and process.returncode == 0
        except Exception:
            clean = False
    return False


def records(stream):
    # psql -0 frames one result row with NUL; EXPLAIN JSON contains newlines.
    # Read one buffered character at a time so a short live record never waits
    # for a full block or EOF. Every frame and the receive queue stay bounded.
    parts = []
    while True:
        char = stream.read(1)
        if not char:
            need(not parts, 'protocol')
            return
        if char == '\0':
            need(parts, 'protocol')
            yield ''.join(parts)
            parts = []
        else:
            parts.append(char)
            need(len(parts) <= 1024 * 1024, 'protocol')


def one_record(raw):
    need(type(raw) is str and 1 < len(raw) <= 1024 * 1024 + 1
         and raw.endswith('\0') and raw.count('\0') == 1, 'protocol')
    return raw[:-1]


class Client:
    def __init__(self, container, role, cleanup_deadline):
        self.process = subprocess.Popen(psql_command(container, role), stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                        text=True, bufsize=1)
        self.lines = queue.Queue(maxsize=8)
        self.failed = False
        self.pending = False
        self.reader = threading.Thread(target=self._read, daemon=True)
        try:
            self.reader.start()
        except Exception:
            reap_process(self.process, min(cleanup_deadline, time.monotonic() + 1.2))
            self.process.stdout.close()
            raise

    def _read(self):
        try:
            for value in records(self.process.stdout):
                self.lines.put_nowait(value)
        except Exception:
            self.failed = True
        finally:
            try:
                self.lines.put_nowait(None)
            except queue.Full:
                self.failed = True

    def send(self, sql, deadline):
        remaining(deadline)
        need(not self.pending and not self.failed and self.process.poll() is None, 'protocol')
        need(type(sql) is str and 0 < len(sql) < 65536, 'protocol')
        self.pending = True
        self.process.stdin.write(sql + ';\n')
        self.process.stdin.flush()

    def receive_raw(self, deadline):
        need(self.pending, 'protocol')
        try:
            value = self.lines.get(timeout=min(1.5, remaining(deadline)))
        except queue.Empty:
            raise Fault('deadline_exceeded') from None
        remaining(deadline)
        need(value is not None and not self.failed, 'protocol')
        self.pending = False
        return value

    def query_raw(self, sql, deadline):
        self.send(sql, deadline)
        return self.receive_raw(deadline)

    def query(self, sql, deadline):
        return decode(self.query_raw(sql, deadline))

    def close(self, deadline):
        clean = reap_process(self.process, deadline)
        self.reader.join(timeout=max(0, min(.4, deadline - time.monotonic())))
        joined = not self.reader.is_alive()
        # Closing a stream while a reader still holds its lock can itself block.
        if joined:
            self.process.stdout.close()
        return clean and joined and not self.failed


def variant_sql(original, pids):
    """One complete row wrapper; do not rewrite its locks/blockers expressions."""
    need(set(pids) == set(ROLES) and pids['a'] != pids['b'])
    for value in pids.values():
        integer(value, minimum=1)
    anchor = "jsonb_build_object('pid',a.pid"
    need(original.count(anchor) == 1)
    start = original.index(anchor)
    depth, quoted, end = 0, False, None
    i = start + len('jsonb_build_object')
    while i < len(original):
        char = original[i]
        if char == "'":
            if quoted and i + 1 < len(original) and original[i + 1] == "'":
                i += 2
                continue
            quoted = not quoted
        elif not quoted:
            if char == '(':
                depth += 1
            elif char == ')':
                depth -= 1
                if depth == 0:
                    end = i + 1
                    break
        i += 1
    need(end is not None and not quoted)
    row = original[start:end]
    prefix = (f'{SCHEMA}.row_gate(CASE a.pid WHEN {pids["a"]} THEN 1 '
              f'WHEN {pids["b"]} THEN 2 ELSE 0 END,')
    result = original[:start] + prefix + row + ')' + original[end:]
    need(result[:start] + result[start + len(prefix):start + len(prefix) + len(row)] +
         result[start + len(prefix) + len(row) + 1:] == original)
    return result


def structural(plan):
    return [{key: node[key] for key in ('nodeType', 'parent', 'relationship', 'subplanOrdinal',
             'aliasSHA256', 'functionClass', 'lockFunctionHasNoArguments',
             'ownedApplicationFilter', 'expressions')} for node in plan['nodes']]


def compare_plans(plans):
    need(set(plans) == {'original_plain', 'original_static_analyze',
                        'variant_plain', 'variant_static_analyze'}, 'inconclusive_plan_change')
    first = plans['original_plain']
    need(all(structural(value) == structural(first) for value in plans.values()),
         'inconclusive_plan_change')
    nodes = first['nodes']
    activities = [n for n in nodes if n['functionClass'] == 'activity' and n['ownedApplicationFilter']]
    locks = [n for n in nodes if n['functionClass'] == 'lock_status']
    need(len(activities) == len(locks) == 1, 'inconclusive_plan_change')
    activity, lock = activities[0], locks[0]
    aggregate = nodes[lock['parent']] if type(lock['parent']) is int else {}
    owner = nodes[aggregate['parent']] if type(aggregate.get('parent')) is int else {}
    join = nodes[activity['parent']] if type(activity['parent']) is int else {}
    predicate = lock['expressions'].get('Filter', {})
    need(lock['lockFunctionHasNoArguments'] is True and aggregate.get('nodeType') == 'Aggregate'
         and aggregate.get('relationship') == 'SubPlan' and owner.get('nodeType') == 'Aggregate'
         and join.get('nodeType') == 'Nested Loop' and join.get('parent') == owner.get('ordinal')
         and predicate.get('crossAliasPidDependency') is True
         and set(predicate.get('pidAliasSHA256', [])) == {activity['aliasSHA256'], lock['aliasSHA256']},
         'inconclusive_plan_change')
    # Do not silently accept a wrapper moved into another subtree.
    changed = [n['ordinal'] for n, v in zip(nodes, plans['variant_plain']['nodes'])
               if n['expressionLists'] != v['expressionLists']]
    need(changed == [owner['ordinal']], 'inconclusive_plan_change')
    for key in ('original_static_analyze', 'variant_static_analyze'):
        current = plans[key]['nodes']
        plain = plans[key.replace('_static_analyze', '_plain')]['nodes']
        need(all(n['expressionLists'] == v['expressionLists'] for n, v in zip(current, plain)),
             'inconclusive_plan_change')
        need(current[activity['ordinal']]['counts'].get('Actual Rows') == 2
             and current[activity['ordinal']]['counts'].get('Actual Loops') == 1
             and current[lock['ordinal']]['counts'].get('Actual Loops') == 2,
             'inconclusive_plan_change')
    return {'comparableVisibleTopology': True, 'sameExecutionAsGatedSelect': False,
            'functionInvocationCountMeasured': False, 'wrapperOwnerOrdinal': owner['ordinal'],
            'activityOrdinal': activity['ordinal'], 'lockOrdinal': lock['ordinal']}


def projected_rows(raw, pids, controller, marker_name):
    need(type(raw) is dict and set(raw) == {'otherNonterminal', 'journalCount', 'task', 'sessions'})
    need(type(raw['otherNonterminal']) is int and raw['otherNonterminal'] == 0
         and type(raw['journalCount']) is int and raw['journalCount'] == 0 and raw['task'] is None)
    rows = raw['sessions']
    need(type(rows) is list and len(rows) == 2)
    inverse = {value: key for key, value in pids.items()}
    result = []
    for row in rows:
        need(type(row) is dict and set(row) == {'pid', 'transaction', 'state', 'query',
                                              'waitType', 'blockers', 'locks'})
        pid = integer(row['pid'], minimum=1)
        need(pid in inverse and type(row['transaction']) is bool and row['transaction'], 'ownership')
        need(type(row['query']) is str and len(row['query']) <= 65536)
        need(row['state'] in ('active', 'idle in transaction', 'idle in transaction (aborted)')
             and (row['waitType'] is None or type(row['waitType']) is str and len(row['waitType']) <= 64))
        blockers = row['blockers']
        need(type(blockers) is list and len(blockers) <= 16)
        for blocker in blockers:
            integer(blocker, minimum=1)
        need(len(set(blockers)) == len(blockers))
        locks = row['locks']
        need(type(locks) is list and len(locks) <= 256)
        markers = []
        for lock in locks:
            need(type(lock) is dict and set(lock) == {'type', 'mode', 'granted', 'relation',
                                                     'classid', 'objid', 'objsubid'})
            need(type(lock['type']) is str and lock['type'] in LOCK_TYPES and
                 type(lock['mode']) is str and lock['mode'] in LOCK_MODES and type(lock['granted']) is bool)
            for key in ('classid', 'objid'):
                value = lock[key]
                if value is not None:
                    need(type(value) is str and re.fullmatch(r'0|[1-9][0-9]{0,9}', value)
                         and int(value) <= 2**32 - 1)
            if lock['objsubid'] is not None:
                integer(lock['objsubid'], minimum=-(2**15), maximum=2**15 - 1)
            need(lock['relation'] is None or type(lock['relation']) is str and len(lock['relation']) <= 256)
            if lock['relation'] == marker_name:
                need(lock['type'] == 'relation' and lock['mode'] == 'AccessShareLock' and lock['granted'])
                markers.append(lock)
        need(len(markers) <= 1)
        result.append({'role': inverse[pid], 'marker': bool(markers),
                       'controllerBlocker': blockers == [controller],
                       'otherBlockers': len([value for value in blockers if value != controller])})
    need({row['role'] for row in result} == set(ROLES), 'ownership')
    return result


def classify_target(row):
    need(type(row) is dict and set(row) == {'role', 'marker', 'controllerBlocker', 'otherBlockers'})
    need(row['role'] in ROLES and type(row['marker']) is bool and type(row['controllerBlocker']) is bool)
    need(type(row['otherBlockers']) is int and row['otherBlockers'] == 0, 'ownership')
    if not row['controllerBlocker']:
        return 'inconclusive_later_read_not_established'
    return ('controlled_cross_time_combination_observed' if row['marker'] else
            'controlled_combination_not_observed')


def state_sql(pids, controller, marker_oid, database_oid):
    for value in (*pids.values(), controller, marker_oid, database_oid):
        integer(value, minimum=1)
    fields = []
    for role in ROLES:
        pid = pids[role]
        fields.extend((f"'{role}Marker',(SELECT count(*) FROM pg_locks WHERE pid={pid} AND "
                       f"database={database_oid} AND relation={marker_oid} AND locktype='relation')",
                       f"'{role}Blocker',pg_blocking_pids({pid})=ARRAY[{controller}]",
                       f"'{role}BlockerCount',cardinality(pg_blocking_pids({pid}))",
                       f"'{role}LockWait',EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid={pid} "
                       "AND state='active' AND wait_event_type='Lock')"))
    fields.append("'roles',(SELECT jsonb_agg(pid ORDER BY pid) FROM pg_stat_activity WHERE "
                  f"datname=current_database() AND application_name='{APPLICATION}')")
    return 'SELECT jsonb_build_object(' + ','.join(fields) + ')'


def checked_state(value, pids):
    keys = {'roles'} | {role + field for role in ROLES for field in ('Marker', 'Blocker', 'BlockerCount', 'LockWait')}
    need(type(value) is dict and set(value) == keys)
    need(type(value['roles']) is list and all(type(pid) is int for pid in value['roles'])
         and value['roles'] == sorted(pids.values()), 'ownership')
    for role in ROLES:
        integer(value[role + 'Marker'], maximum=8)
        integer(value[role + 'BlockerCount'], maximum=16)
        need(type(value[role + 'Blocker']) is bool and type(value[role + 'LockWait']) is bool)
    return value


def poll(read, predicate, deadline):
    for attempt in range(POLL_LIMIT):
        remaining(deadline)
        value = read(deadline)
        remaining(deadline)
        if predicate(value):
            return value, attempt + 1
        if attempt + 1 < POLL_LIMIT:
            time.sleep(min(POLL_SLEEP, remaining(deadline)))
    raise Fault('inconclusive_ordering')


def transition(clients, pids, controller, marker_oid, database_oid, variant, marker_name, emit, receipt, work_deadline):
    """All phases, including after-control, share one non-renewing 750ms window."""
    deadline = min(work_deadline, time.monotonic() + TRANSITION_SECONDS)
    observer, verifier, control = (clients[name] for name in ('observer', 'verifier', 'controller'))
    query_state = state_sql(pids, controller, marker_oid, database_oid)
    gate_sql = ("SELECT jsonb_build_object('a',count(*) FILTER (WHERE objid=1),"
                "'b',count(*) FILTER (WHERE objid=2)) FROM pg_locks WHERE "
                f"pid={clients['observer'].pid} AND database={database_oid} AND locktype='advisory' "
                f"AND classid={GATE_NAMESPACE} AND objsubid=2 AND mode='ExclusiveLock' AND NOT granted")
    observer.send(variant, deadline)

    def gate_read(limit):
        value = verifier.query(gate_sql, limit)
        need(type(value) is dict and set(value) == set(ROLES))
        for count in value.values():
            integer(count, maximum=1)
        need(sum(value.values()) <= 1, 'inconclusive_ordering')
        return value

    gate, polls = poll(gate_read, lambda value: sum(value.values()) == 1, deadline)
    first = next(role for role in ROLES if gate[role] == 1)
    target = next(role for role in ROLES if role != first)
    emit('first_row_gate_observed', role=first, polls=polls)
    need(clients[target].query('ROLLBACK TO SAVEPOINT marker_phase; SELECT to_json(true)', deadline) is True)
    emit('rollback_ack', role=target)
    state = checked_state(verifier.query(query_state, deadline), pids)
    need(state[target + 'Marker'] == 0 and state[target + 'BlockerCount'] == 0
         and state[first + 'Marker'] == 1, 'inconclusive_ordering')
    emit('marker_zero_before_barrier', role=target)
    clients[target].send(barrier_sql(), deadline)
    state, polls = poll(lambda limit: checked_state(verifier.query(query_state, limit), pids),
                        lambda value: value[target + 'Blocker'] and value[target + 'LockWait'], deadline)
    emit('barrier_confirmed', role=target, polls=polls)
    state = checked_state(verifier.query(query_state, deadline), pids)
    need(state[target + 'Marker'] == 0 and state[first + 'Marker'] == 1
         and state[target + 'Blocker'] and state[target + 'LockWait'], 'inconclusive_ordering')
    emit('marker_zero_after_barrier', role=target)
    unlocked = control.query(f"SELECT jsonb_build_object('a',pg_advisory_unlock({GATE_NAMESPACE},1),"
                             f"'b',pg_advisory_unlock({GATE_NAMESPACE},2))", deadline)
    need(type(unlocked) is dict and unlocked == {'a': True, 'b': True}
         and all(type(value) is bool for value in unlocked.values()), 'ownership')
    emit('gates_released')
    raw = observer.receive_raw(deadline)
    receipt['gatedAttempt'] = {'caseID': 'single_gated_select', 'rawResultSHA256': plan_fixture.sha(raw),
                               'projected': False}
    rows = projected_rows(decode(raw), pids, controller, marker_name)
    receipt['gatedAttempt']['projected'] = True
    result = {'caseID': 'single_gated_select', 'sqlSHA256': plan_fixture.sha(variant),
              'rawResultSHA256': plan_fixture.sha(raw), 'rows': rows, 'outcome': None,
              'sameExecutionAsStaticPlans': False}
    # Retain the first closed observation even if its own state or later control fails.
    receipt['gated'] = result
    need([row['role'] for row in rows] == [first, target], 'inconclusive_ordering')
    need(rows[0] == {'role': first, 'marker': True, 'controllerBlocker': False, 'otherBlockers': 0},
         'inconclusive_ordering')
    outcome = classify_target(rows[1])
    result['outcome'], receipt['outcome'] = outcome, outcome
    emit('gated_result', role=target, marker=rows[1]['marker'], controllerBlocker=rows[1]['controllerBlocker'])
    # A later coherent sample can never rescue an inconclusive original result.
    if outcome not in OUTCOMES:
        return
    after_raw = observer.query_raw(original_sql(), deadline)
    after = projected_rows(decode(after_raw), pids, controller, marker_name)
    by_role = {row['role']: row for row in after}
    need(by_role[target] == {'role': target, 'marker': False, 'controllerBlocker': True, 'otherBlockers': 0}
         and by_role[first] == {'role': first, 'marker': True, 'controllerBlocker': False, 'otherBlockers': 0},
         'inconclusive_ordering')
    remaining(deadline)
    emit('coherent_after', role=target)
    receipt['after'] = {'caseID': 'exact_after', 'sqlSHA256': plan_fixture.sha(original_sql()),
                        'rawResultSHA256': plan_fixture.sha(after_raw), 'rows': after}


def original_sql():
    return RunningExportCancelGuard.observe_sql(SimpleNamespace(asset_kind='group', application_name=APPLICATION), TASK)


def barrier_sql():
    return ('SELECT to_json(count(*)) FROM (SELECT k FROM ' + SCHEMA +
            '.barrier WHERE k=1 FOR UPDATE) owned')


def cleanup(clients, constructions_confirmed, container, created, deadline):
    closed = constructions_confirmed
    for role in ('controller', 'a', 'b', 'observer', 'verifier'):
        if role not in clients:
            continue
        try:
            current = clients[role].close(deadline)
        except Exception:
            current = False
            # Do not skip a final per-process kill/reap attempt on wrapper errors.
            try:
                clients[role].process.kill()
                clients[role].process.wait(timeout=max(0, min(.2, deadline - time.monotonic())))
            except Exception:
                pass
        closed = current and closed
    zero = False
    if created and time.monotonic() < deadline:
        try:
            command = psql_command(container, 'verifier')
            raw = subprocess.run(command, input='SELECT to_json(count(*)) FROM pg_stat_activity WHERE '
                                 'datname=current_database() AND pid<>pg_backend_pid();\n',
                                 text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                 timeout=min(2, remaining(deadline)), check=True).stdout
            value = decode(one_record(raw))
            zero = type(value) is int and value == 0
        except Exception:
            pass
    return {'clientsClosed': closed, 'zeroOtherDatabaseBackends': zero}


def run_experiment(container, environ):
    start = time.monotonic()
    work_deadline, cleanup_deadline = start + WORK_SECONDS, start + CLEANUP_SECONDS
    clients, pids, plans = {}, {}, {}
    created, constructions_confirmed, first_failure = False, True, None
    stage = 'inputs'
    evidence = {'version': 1, 'purpose': 'controlled_variant_temporal_combination_only', 'success': False,
                'plans': {}, 'planAttempts': [], 'events': [], 'gated': None, 'gatedAttempt': None, 'before': None, 'after': None,
                'outcome': None, 'firstFailureStage': None, 'firstFailureClass': None}

    def emit(name, **facts):
        need(name in EVENTS and len(evidence['events']) < 24)
        need(set(facts) <= {'role', 'polls', 'marker', 'controllerBlocker'})
        for key, value in facts.items():
            if key == 'role':
                need(value in ROLES)
            elif key == 'polls':
                integer(value, POLL_LIMIT, 1)
            else:
                need(type(value) is bool)
        evidence['events'].append({'event': name, 'ordinal': len(evidence['events']),
                                    'elapsedMS': round((time.monotonic() - start) * 1000, 3), **facts})

    try:
        need(environ.get('ADTR_RUN_OBSERVER_TEMPORAL_TESTS') == '1'
             and environ.get('GITHUB_ACTIONS') == 'true' and sys.platform == 'linux', 'inputs')
        psql_command(container, 'observer')
        for key in ('GITHUB_SHA', 'ADTR_EVENT_HEAD_SHA'):
            need(re.fullmatch('[0-9a-f]{40}', environ.get(key, '')), 'inputs')
        for key in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
            need(re.fullmatch('[1-9][0-9]{0,19}', environ.get(key, '')), 'inputs')
        evidence['identity'] = {key: environ[key] for key in ('GITHUB_SHA', 'ADTR_EVENT_HEAD_SHA',
                                                            'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')}
        actual = subprocess.run(['git', 'rev-parse', 'HEAD'], text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, check=True, timeout=5).stdout.strip()
        need(actual == environ['GITHUB_SHA'], 'inputs')
        url = urlsplit(plan_fixture.owned_url(environ.get('ADTR_TEST_DATABASE_URL', '')))
        database_url = urlunsplit((url.scheme, url.netloc, '/' + DATABASE, url.query, ''))
        stage = 'create_database'
        subprocess.run(['docker', 'exec', container, 'createdb', '-U', 'postgres', DATABASE],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        created = True
        stage = 'migrate'
        subprocess.run(['bin/adtr', '-mode=migrate'], env=dict(environ, ADTR_DATABASE_URL=database_url,
                       ADTR_DEVELOPMENT='true'), check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, timeout=35)
        stage = 'clients'
        for role in CLIENT_ROLES:
            remaining(work_deadline)
            try:
                clients[role] = Client(container, role, cleanup_deadline)
            except Exception:
                constructions_confirmed = False
                raise
            value = clients[role].query("SELECT jsonb_build_object('pid',pg_backend_pid(),'version',"
                                        "current_setting('server_version_num')::integer,'appMatches',"
                                        "current_setting('application_name')='" +
                                        (APPLICATION if role in ROLES else 'adtr-temporal-' + role) +
                                        "','readOnly',current_setting('transaction_read_only')='on')", work_deadline)
            need(type(value) is dict and set(value) == {'pid', 'version', 'appMatches', 'readOnly'})
            clients[role].pid = integer(value['pid'], minimum=1)
            need(type(value['version']) is int and value['version'] == 170006
                 and value['appMatches'] is True and type(value['readOnly']) is bool
                 and value['readOnly'] == (role in ('observer', 'verifier')), 'ownership')
        need(len({client.pid for client in clients.values()}) == len(CLIENT_ROLES), 'ownership')
        pids = {role: clients[role].pid for role in ROLES}
        controller = clients['controller'].pid
        observer, verifier, control = (clients[role] for role in ('observer', 'verifier', 'controller'))
        stage = 'fixture_setup'
        ddl = (f'CREATE SCHEMA {SCHEMA}; CREATE TABLE {MARKER}(k integer); '
               f'CREATE TABLE {SCHEMA}.barrier(k integer PRIMARY KEY); INSERT INTO {SCHEMA}.barrier VALUES(1); '
               f'CREATE FUNCTION {SCHEMA}.row_gate(actor_role integer,payload jsonb) RETURNS jsonb '
               f'LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE AS $body${GATE_BODY}$body$; SELECT to_json(true)')
        need(control.query(ddl, work_deadline) is True)
        binding_sql = ("SELECT jsonb_build_object('database',current_database(),'databaseOID',"
                       "(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'markerOID',"
                       f"'{MARKER}'::regclass::oid::bigint,'markerName','{MARKER}'::regclass::text,"
                       "'searchPath',current_setting('search_path'))")
        binding = observer.query(binding_sql, work_deadline)
        need(type(binding) is dict and set(binding) == {'database', 'databaseOID', 'markerOID', 'markerName', 'searchPath'})
        need(binding['database'] == DATABASE and binding['markerName'] == MARKER
             and type(binding['searchPath']) is str and 0 < len(binding['searchPath']) <= 256)
        marker_oid = integer(binding['markerOID'], minimum=1)
        database_oid = integer(binding['databaseOID'], minimum=1)
        definition = control.query("SELECT jsonb_build_object('body',p.prosrc,'volatile',p.provolatile='v',"
                                   "'parallelUnsafe',p.proparallel='u','invoker',NOT p.prosecdef,"
                                   "'plpgsql',l.lanname='plpgsql') FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace "
                                   "JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='" + SCHEMA +
                                   "' AND p.proname='row_gate'", work_deadline)
        need(type(definition) is dict and set(definition) == {'body', 'volatile', 'parallelUnsafe', 'invoker', 'plpgsql'}
             and definition['body'] == GATE_BODY
             and all(definition[key] is True for key in ('volatile', 'parallelUnsafe', 'invoker', 'plpgsql')))
        evidence['fixtureBinding'] = {'sameObserverRegclassMapping': True,
                                     'rawObserverRowsContainOID': False,
                                     'searchPathSHA256': plan_fixture.sha(binding['searchPath']),
                                     'wrapperBodySHA256': plan_fixture.sha(GATE_BODY),
                                     'transactionLevelGate': True}
        state_query = state_sql(pids, controller, marker_oid, database_oid)
        stage = 'marker_precondition'
        state = checked_state(verifier.query(state_query, work_deadline), pids)
        need(all(state[role + 'Marker'] == 0 and state[role + 'BlockerCount'] == 0 for role in ROLES), 'ownership')
        emit('marker_zero_before_savepoints')
        for role in ROLES:
            need(clients[role].query(f'BEGIN; SAVEPOINT marker_phase; LOCK TABLE {MARKER} IN ACCESS SHARE MODE; '
                                     'SELECT to_json(true)', work_deadline) is True)
        locked = control.query('BEGIN; ' + barrier_sql(), work_deadline)
        need(type(locked) is int and locked == 1)
        state = checked_state(verifier.query(state_query, work_deadline), pids)
        need(all(state[role + 'Marker'] == 1 and state[role + 'BlockerCount'] == 0 for role in ROLES), 'ownership')
        emit('markers_acquired')
        original = original_sql()
        variant = variant_sql(original, pids)
        evidence['observerSQLSHA256'] = plan_fixture.sha(original)
        evidence['variantSQLSHA256'] = plan_fixture.sha(variant)
        evidence['sourceSHA256'] = {name: hashlib.sha256(Path(name).read_bytes()).hexdigest() for name in
                                   ('scripts/asset_export_v3_running_cancel_guard.py',
                                    'scripts/test_observer_plan_runtime.py',
                                    'scripts/test_observer_temporal_control_runtime.py')}
        stage = 'static_plans'
        for label, sql in (('original', original), ('variant', variant)):
            for analyzed in (False, True):
                remaining(work_deadline)
                case = label + ('_static_analyze' if analyzed else '_plain')
                prefix = 'EXPLAIN (ANALYZE, VERBOSE, FORMAT JSON) ' if analyzed else 'EXPLAIN (VERBOSE, FORMAT JSON) '
                raw = observer.query_raw(prefix + sql, work_deadline)
                attempt = {'caseID': case, 'sqlSHA256': plan_fixture.sha(sql),
                           'rawPlanSHA256': plan_fixture.sha(raw), 'projected': False, 'failure': None}
                evidence['planAttempts'].append(attempt)
                try:
                    plans[case] = plan_fixture.project_plan(decode(raw), analyzed)
                except Exception as error:
                    attempt['failure'] = plan_fixture.projection_failure(error)
                    raise
                attempt['projected'] = True
                evidence['plans'][case] = plans[case]
        stage = 'plan_comparison'
        evidence['planComparison'] = compare_plans(plans)
        static_gate_count = verifier.query(f"SELECT to_json(count(*)) FROM pg_locks WHERE pid={observer.pid} "
                                           "AND locktype='advisory'", work_deadline)
        need(type(static_gate_count) is int and static_gate_count == 0, 'ownership')
        need(observer.query(binding_sql, work_deadline) == binding, 'ownership')
        stage = 'coherent_before'
        before_raw = observer.query_raw(original, work_deadline)
        before = projected_rows(decode(before_raw), pids, controller, MARKER)
        need(all(row['marker'] is True and row['controllerBlocker'] is False and row['otherBlockers'] == 0
                 for row in before), 'inconclusive_ordering')
        evidence['before'] = {'caseID': 'exact_before', 'sqlSHA256': plan_fixture.sha(original),
                              'rawResultSHA256': plan_fixture.sha(before_raw), 'rows': before}
        emit('coherent_before')
        held = control.query(f"SELECT jsonb_build_object('a',pg_try_advisory_lock({GATE_NAMESPACE},1),"
                             f"'b',pg_try_advisory_lock({GATE_NAMESPACE},2))", work_deadline)
        need(type(held) is dict and held == {'a': True, 'b': True}
             and all(type(value) is bool for value in held.values()), 'ownership')
        stage = 'single_transition'
        transition(clients, pids, controller, marker_oid, database_oid, variant, MARKER, emit,
                   evidence, work_deadline)
        need(evidence['outcome'] in OUTCOMES, evidence['outcome'])
    except Exception as error:
        first_failure = stage
        evidence['firstFailureClass'] = (error.reason if type(error) is Fault and
            type(error.reason) is str and error.reason in REASONS else 'unexpected_failure')
    finally:
        evidence['cleanup'] = cleanup(clients, constructions_confirmed, container, created, cleanup_deadline)
    if first_failure is None and not all(evidence['cleanup'].values()):
        first_failure = 'cleanup'
        evidence['firstFailureClass'] = 'cleanup_failed'
    evidence['firstFailureStage'] = first_failure
    evidence['success'] = first_failure is None and all(evidence['cleanup'].values()) and evidence['outcome'] in OUTCOMES
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    args = parser.parse_args()
    result = run_experiment(args.container, dict(os.environ))
    print('observer_temporal_evidence: ' + json.dumps(result, sort_keys=True, separators=(',', ':')))
    return 0 if result['success'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
