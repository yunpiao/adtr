"""Explicit CI-only plan evidence; not a checkpoint or race reproduction.

Uses a new database in the integration runner's already-owned pinned container.
Raw plans, query text, identifiers, connection data and exceptions are never logged.
"""
import argparse
import hashlib
import json
import math
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

from asset_export_v3_running_cancel_guard import RunningExportCancelGuard

DATABASE = 'adtr_observer_plan'
APPLICATION = 'adtr-observer-plan-owned'
OBSERVER = 'adtr-observer-plan-observer'
TASK = 'observer-plan-synthetic-task'
NODE_TYPES = frozenset(('Aggregate', 'Result', 'Seq Scan', 'Index Scan', 'Index Only Scan',
                       'Bitmap Heap Scan', 'Bitmap Index Scan', 'BitmapAnd', 'BitmapOr',
                       'Function Scan', 'Nested Loop', 'Hash Join', 'Merge Join', 'Hash',
                       'Sort', 'Incremental Sort', 'Materialize', 'Memoize', 'Subquery Scan',
                       'Limit', 'Gather', 'Gather Merge', 'Append', 'Merge Append'))
RELATIONSHIPS = frozenset(('Outer', 'Inner', 'SubPlan', 'InitPlan', 'Member'))
EXPRESSIONS = frozenset(('Filter', 'Index Cond', 'Recheck Cond', 'Join Filter', 'Hash Cond',
                        'Merge Cond', 'Function Call'))
EXPRESSION_LISTS = frozenset(('Output', 'Sort Key', 'Presorted Key', 'Group Key', 'Hash Key'))
COUNTS = frozenset(('Plan Rows', 'Plan Width', 'Actual Rows', 'Actual Loops',
                    'Rows Removed by Filter', 'Rows Removed by Join Filter',
                    'Rows Removed by Index Recheck', 'Heap Fetches', 'Hash Buckets',
                    'Original Hash Buckets', 'Hash Batches', 'Original Hash Batches',
                    'Peak Memory Usage', 'Sort Space Used', 'Workers Planned',
                    'Workers Launched', 'Subplans Removed'))
TIMINGS = frozenset(('Startup Cost', 'Total Cost', 'Actual Startup Time', 'Actual Total Time'))
BOOLEANS = frozenset(('Parallel Aware', 'Async Capable', 'Inner Unique', 'Single Copy'))
ENUMS = {
    'Strategy': frozenset(('Plain', 'Sorted', 'Hashed', 'Mixed')),
    'Partial Mode': frozenset(('Simple', 'Partial', 'Finalize')),
    'Join Type': frozenset(('Inner', 'Left', 'Right', 'Full', 'Semi', 'Anti')),
    'Scan Direction': frozenset(('Forward', 'Backward', 'NoMovement')),
    'Sort Method': frozenset(('quicksort', 'top-N heapsort', 'external merge', 'external sort')),
    'Sort Space Type': frozenset(('Memory', 'Disk')),
}
NAMES = frozenset(('Schema', 'Relation Name', 'Index Name', 'Alias', 'Function Name'))
NODE_KEYS = (EXPRESSIONS | EXPRESSION_LISTS | COUNTS | TIMINGS | BOOLEANS | NAMES |
             set(ENUMS) | {'Node Type', 'Parent Relationship', 'Subplan Name', 'Plans'})


class PlanError(RuntimeError):
    pass


def require(condition):
    if not condition:
        raise PlanError('observer plan evidence rejected')


def sha(value):
    return hashlib.sha256(value.encode()).hexdigest()


def text_checked(value, limit=4096):
    require(type(value) is str and 0 < len(value) <= limit and
            not any(ord(char) < 32 for char in value))
    return value


def numeric(value, integer=False):
    require(type(value) in ((int,) if integer else (int, float)) and
            0 <= value <= 10**12 and math.isfinite(value))
    return value


def expression(value):
    value = text_checked(value)
    # A complete equality is syntax, not necessarily a cross-alias dependency.
    # Never infer dependency from a bare/self PID or an embedded substring.
    candidate = value[1:-1] if value.startswith('(') and value.endswith(')') else value
    match = re.fullmatch(r'\s*(?:([a-z_][a-z_0-9]*)\.)?pid\s*=\s*'
                         r'(?:([a-z_][a-z_0-9]*)\.)?pid\s*', candidate)
    aliases = sorted({sha(alias) for alias in match.groups() if alias}) if match else []
    return {'sha256': sha(value), 'simplePidEquality': match is not None,
            'crossAliasPidDependency': bool(match and all(match.groups()) and
                                           match.group(1) != match.group(2)),
            'pidAliasSHA256': aliases}


def project_plan(raw, analyzed):
    require(type(analyzed) is bool and type(raw) is list and len(raw) == 1)
    root = raw[0]
    require(type(root) is dict and set(root) <= {'Plan', 'Planning Time', 'Execution Time', 'Triggers'}
            and 'Plan' in root)
    if 'Triggers' in root:
        require(root['Triggers'] == [])
    for key in ('Planning Time', 'Execution Time'):
        if key in root:
            numeric(root[key])
    if analyzed:
        require('Execution Time' in root)
    nodes = []

    def visit(node, parent, depth):
        require(depth <= 32 and len(nodes) < 256 and type(node) is dict and
                set(node) <= NODE_KEYS and type(node.get('Node Type')) is str and
                node['Node Type'] in NODE_TYPES)
        for key, value in node.items():
            if key in COUNTS:
                numeric(value, True)
            elif key in TIMINGS:
                numeric(value)
            elif key in BOOLEANS:
                require(type(value) is bool)
            elif key in ENUMS:
                require(type(value) is str and value in ENUMS[key])
            elif key in NAMES:
                require(re.fullmatch(r'[a-zA-Z_][a-zA-Z_0-9]{0,62}', text_checked(value, 63)) is not None)
        relationship = node.get('Parent Relationship')
        require(relationship is None or (type(relationship) is str and relationship in RELATIONSHIPS))
        subplan = node.get('Subplan Name')
        if subplan is not None:
            require(type(subplan) is str and re.fullmatch(r'(?:SubPlan|InitPlan) [1-9][0-9]{0,3}(?: \(returns \$[0-9]{1,4}\))?', subplan))
        projected = {'ordinal': len(nodes), 'parent': parent, 'nodeType': node['Node Type'],
                     'relationship': relationship,
                     'subplanOrdinal': int(re.search(r'\d+', subplan)[0]) if subplan else None,
                     'aliasSHA256': sha(node['Alias']) if 'Alias' in node else None,
                     'counts': {key: node[key] for key in sorted(COUNTS) if key in node},
                     'expressions': {key: expression(node[key]) for key in sorted(EXPRESSIONS) if key in node},
                     'expressionLists': {}, 'functionClass': 'not_function',
                     'lockFunctionHasNoArguments': None, 'ownedApplicationFilter': False}
        for key in sorted(EXPRESSION_LISTS):
            if key in node:
                values = node[key]
                require(type(values) is list and len(values) <= 64)
                projected['expressionLists'][key] = [expression(value) for value in values]
        if node['Node Type'] == 'Function Scan':
            name = node.get('Function Name')
            require(type(name) is str and name in {'pg_lock_status', 'pg_stat_get_activity'})
            projected['functionClass'] = 'lock_status' if name == 'pg_lock_status' else 'activity'
            call = node.get('Function Call')
            require(type(call) is str)
            if name == 'pg_lock_status':
                projected['lockFunctionHasNoArguments'] = call in ('pg_lock_status()', 'pg_catalog.pg_lock_status()')
            if name == 'pg_stat_get_activity':
                alias = node.get('Alias')
                require(type(alias) is str)
                expected = f"({alias}.application_name = '{APPLICATION}'::text)"
                projected['ownedApplicationFilter'] = node.get('Filter') == expected
        if analyzed:
            require('Actual Rows' in node and 'Actual Loops' in node)
        nodes.append(projected)
        children = node.get('Plans', [])
        require(type(children) is list and len(children) <= 64)
        for child in children:
            visit(child, projected['ordinal'], depth + 1)

    visit(root['Plan'], None, 0)
    locks = [node for node in nodes if node['functionClass'] == 'lock_status']
    require(locks and all(node['lockFunctionHasNoArguments'] for node in locks))
    visible = [node['ordinal'] for node in nodes if node['functionClass'] == 'activity' and
               node['ownedApplicationFilter'] and node['counts'].get('Actual Rows') == 2 and
               node['counts'].get('Actual Loops') == 1]
    if analyzed:
        require(visible and any(node['counts']['Actual Loops'] > 0 for node in locks))
    return {'mode': 'analyze' if analyzed else 'plan_only', 'nodes': nodes,
            'planningTimeMS': root.get('Planning Time'), 'executionTimeMS': root.get('Execution Time'),
            'twoOwnedSessionsVisibleAtActivityScan': visible,
            'loopCountIsNotFunctionInvocationCount': True}


def unique_json(raw):
    require(type(raw) is str and len(raw) <= 1024 * 1024)

    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result)
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=pairs,
                      parse_constant=lambda _: (_ for _ in ()).throw(PlanError()))


def owned_url(value):
    parsed = urlsplit(value)
    require(parsed.scheme == 'postgres' and parsed.hostname == '127.0.0.1' and
            parsed.port is not None and parsed.path == '/adtr_test' and
            parsed.query == 'sslmode=disable' and not parsed.fragment and
            parsed.username == 'postgres' and bool(parsed.password))
    return urlunsplit((parsed.scheme, parsed.netloc, '/' + DATABASE, parsed.query, ''))


def psql_command(container, application):
    require(re.fullmatch(r'adtr-auth-e2e-[0-9a-f]{12}', container) is not None and
            application in {APPLICATION, OBSERVER})
    options = ('-c statement_timeout=1000 -c idle_in_transaction_session_timeout=8000 '
               '-c default_transaction_read_only=on')
    # psql supplies fallback_application_name in its startup packet after the
    # server processes PGOPTIONS. Use libpq's explicit application_name field.
    return ['docker', 'exec', '-i', '-e', 'PGOPTIONS=' + options,
            '-e', 'PGAPPNAME=' + application, container,
            'psql', '-U', 'postgres', '-d', DATABASE, '-X', '-q', '-A', '-t',
            '-v', 'ON_ERROR_STOP=1']


def preparation_checked(value):
    require(type(value) is dict and set(value) == {'appMatches', 'transactionReadOnly', 'activityRows'})
    require(type(value['appMatches']) is bool and type(value['transactionReadOnly']) is bool and
            type(value['activityRows']) is int and 0 <= value['activityRows'] <= 10**6)
    return {key: value[key] for key in ('appMatches', 'transactionReadOnly', 'activityRows')}


class OwnedSession:
    def __init__(self, container):
        self.process = subprocess.Popen(psql_command(container, APPLICATION), stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                        text=True, bufsize=1)
        self.lines = queue.Queue(maxsize=4)
        self.read_failed = False
        self.reader = threading.Thread(target=self._read, daemon=True)
        try:
            self.reader.start()
        except Exception:
            reap_process(self.process)
            self.process.stdout.close()
            raise

    def _read(self):
        try:
            while True:
                line = self.process.stdout.readline(65537)
                if not line:
                    break
                if len(line) > 65536:
                    self.read_failed = True
                    break
                try:
                    self.lines.put_nowait(line)
                except queue.Full:
                    self.read_failed = True
                    break
        except Exception:
            self.read_failed = True
        finally:
            try:
                self.lines.put_nowait(None)
            except queue.Full:
                pass

    def open_transaction(self):
        self.process.stdin.write("BEGIN; SELECT jsonb_build_object('appMatches',"
                                 "current_setting('application_name')='" + APPLICATION + "',"
                                 "'transactionReadOnly',current_setting('transaction_read_only')='on',"
                                 "'activityRows',(SELECT count(*) FROM adtr.system_worker_activity));\n")
        self.process.stdin.flush()
        return preparation_checked(unique_json(self.lines.get(timeout=3)))

    def close(self):
        clean = reap_process(self.process)
        self.reader.join(timeout=2)
        require(not self.reader.is_alive())
        self.process.stdout.close()
        require(clean and not self.read_failed)


def reap_process(process):
    clean = True
    try:
        process.stdin.close()
    except (BrokenPipeError, OSError):
        clean = False
    for action in (None, process.terminate, process.kill):
        try:
            if action is not None:
                action()
                clean = False
            process.wait(timeout=2)
            return clean and process.returncode == 0
        except Exception:
            clean = False
    return False


def observer_read(container, sql):
    result = subprocess.run(psql_command(container, OBSERVER), input=sql + ';\n',
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                            text=True, timeout=4, check=True)
    return result.stdout


def session_count(container):
    raw = observer_read(container, "SELECT to_json(count(*)) FROM pg_stat_activity WHERE "
                        "datname=current_database() AND application_name='" + APPLICATION + "'")
    count = unique_json(raw)
    require(type(count) is int and 0 <= count <= 2)
    return count


def other_database_backends(container):
    # Cleanup is broader than target visibility: observer and migration clients
    # must also be gone. Exclude only this independent verifier's own backend.
    raw = observer_read(container, 'SELECT to_json(count(*)) FROM pg_stat_activity WHERE '
                        'datname=current_database() AND pid<>pg_backend_pid()')
    count = unique_json(raw)
    require(type(count) is int and 0 <= count <= 100)
    return count


def run_experiment(container, environ):
    sessions = []
    constructions_confirmed = True
    created = False
    stage = 'inputs'
    first_failure = None
    cleanup = {'clientsClosed': False, 'zeroOtherDatabaseBackends': False}
    evidence = {'version': 1, 'purpose': 'original_observer_plan_only', 'success': False,
                'fixture': 'migrated_empty_task_two_idle_owned_transactions', 'plans': [], 'planAttempts': [],
                'ownedPreparation': [], 'ownedSessionCounts': {'beforePlan': None, 'afterPlan': None}}
    started = time.monotonic()
    try:
        require(environ.get('ADTR_RUN_OBSERVER_PLAN_TESTS') == '1' and
                environ.get('GITHUB_ACTIONS') == 'true' and sys.platform == 'linux')
        require(re.fullmatch(r'adtr-auth-e2e-[0-9a-f]{12}', container) is not None)
        for key in ('GITHUB_SHA', 'ADTR_EVENT_HEAD_SHA'):
            require(re.fullmatch(r'[0-9a-f]{40}', environ.get(key, '')) is not None)
        for key in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
            require(re.fullmatch(r'[1-9][0-9]{0,19}', environ.get(key, '')) is not None)
        evidence['identity'] = {key: environ[key] for key in
                                ('GITHUB_SHA', 'ADTR_EVENT_HEAD_SHA', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')}
        actual = subprocess.run(['git', 'rev-parse', 'HEAD'], stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, text=True, check=True, timeout=5).stdout.strip()
        require(actual == environ['GITHUB_SHA'])
        database_url = owned_url(environ.get('ADTR_TEST_DATABASE_URL', ''))
        stage = 'create_database'
        subprocess.run(['docker', 'exec', container, 'createdb', '-U', 'postgres', DATABASE],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15)
        created = True
        stage = 'migrate'
        subprocess.run(['bin/adtr', '-mode=migrate'],
                       env=dict(environ, ADTR_DATABASE_URL=database_url, ADTR_DEVELOPMENT='true'),
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=35)
        stage = 'server_version'
        version = unique_json(observer_read(container, "SELECT to_json(current_setting('server_version_num')::integer)"))
        require(version == 170006)
        evidence['serverVersionNum'] = version
        stage = 'owned_sessions'
        for _ in range(2):
            try:
                session = OwnedSession(container)
            except Exception:
                # A failed constructor may have spawned a client whose cleanup
                # could not be confirmed. An empty sessions list is not proof.
                constructions_confirmed = False
                raise
            sessions.append(session)
            proof = session.open_transaction()
            evidence['ownedPreparation'].append(proof)
            require(proof == {'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0})
        stage = 'owned_visibility'
        evidence['ownedSessionCounts']['beforePlan'] = session_count(container)
        require(evidence['ownedSessionCounts']['beforePlan'] == 2)
        stage = 'original_sql'
        context = SimpleNamespace(asset_kind='group', application_name=APPLICATION)
        sql = RunningExportCancelGuard.observe_sql(context, TASK)
        evidence['observerSQLSHA256'] = sha(sql)
        evidence['sourceSHA256'] = {name: hashlib.sha256(Path(name).read_bytes()).hexdigest() for name in
                                   ('scripts/asset_export_v3_running_cancel_guard.py',
                                    'scripts/test_observer_plan_runtime.py', 'internal/store/store.go')}
        for analyzed in (False, True):
            require(time.monotonic() - started < 90)
            stage = 'analyze' if analyzed else 'explain'
            prefix = 'EXPLAIN (ANALYZE, VERBOSE, FORMAT JSON) ' if analyzed else 'EXPLAIN (VERBOSE, FORMAT JSON) '
            raw = observer_read(container, prefix + sql)
            attempt = {'mode': 'analyze' if analyzed else 'plan_only',
                       'rawPlanSHA256': sha(raw), 'projected': False}
            evidence['planAttempts'].append(attempt)
            plan = project_plan(unique_json(raw), analyzed)
            plan['rawPlanSHA256'] = sha(raw)
            evidence['plans'].append(plan)
            attempt['projected'] = True
        stage = 'owned_visibility_after_plan'
        evidence['ownedSessionCounts']['afterPlan'] = session_count(container)
        require(evidence['ownedSessionCounts']['afterPlan'] == 2)
    except Exception:
        first_failure = stage
    finally:
        closed = constructions_confirmed
        for session in reversed(sessions):
            try:
                session.close()
            except Exception:
                closed = False
        cleanup['clientsClosed'] = closed
        if created:
            try:
                cleanup['zeroOtherDatabaseBackends'] = other_database_backends(container) == 0
            except Exception:
                pass
    if first_failure is None and not all(cleanup.values()):
        first_failure = 'cleanup'
    evidence['firstFailureStage'] = first_failure
    evidence['cleanup'] = cleanup
    evidence['success'] = (first_failure is None and all(cleanup.values()) and
                           len(evidence['plans']) == 2)
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    args = parser.parse_args()
    result = run_experiment(args.container, dict(os.environ))
    print('observer_plan_evidence: ' + json.dumps(result, sort_keys=True, separators=(',', ':')))
    return 0 if result['success'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
