"""Offline contracts for the explicit PG plan fixture; never start a service."""
import ast
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock, patch

import test_observer_plan_runtime as runtime
from asset_export_v3_running_cancel_guard import RunningExportCancelGuard


def plan(analyzed=True):
    def node(kind, **extra):
        result = {'Node Type': kind, 'Plan Rows': 1, 'Plan Width': 8,
                  'Parallel Aware': False, 'Async Capable': False, **extra}
        if analyzed:
            result.update({'Actual Rows': 1, 'Actual Loops': 1})
        return result
    activity = node('Function Scan', **{
        'Function Name': 'pg_stat_get_activity', 'Function Call': 'pg_stat_get_activity(NULL::integer)',
        'Alias': 's', 'Parent Relationship': 'Outer',
        'Filter': "(s.application_name = 'adtr-observer-plan-owned'::text)"})
    locks = node('Function Scan', **{
        'Function Name': 'pg_lock_status', 'Function Call': 'pg_lock_status()',
        'Alias': 'l', 'Parent Relationship': 'SubPlan', 'Subplan Name': 'SubPlan 12',
        'Filter': '(l.pid = s.pid)'})
    if analyzed:
        activity['Actual Rows'] = 2
        locks['Actual Loops'] = 2
    root = {'Plan': node('Result', Plans=[activity, locks])}
    if analyzed:
        root.update({'Planning Time': 0.5, 'Execution Time': 0.75, 'Triggers': []})
    return [root]


class ProjectionContracts(unittest.TestCase):
    def test_plain_and_analyze_are_distinct_and_preserve_tree_and_loops(self):
        plain = runtime.project_plan(plan(False), False)
        analyzed = runtime.project_plan(plan(), True)
        self.assertEqual(plain['mode'], 'plan_only')
        self.assertEqual(plain['twoOwnedSessionsVisibleAtActivityScan'], [])
        self.assertEqual(analyzed['mode'], 'analyze')
        self.assertEqual(analyzed['twoOwnedSessionsVisibleAtActivityScan'], [1])
        self.assertEqual(analyzed['nodes'][2]['parent'], 0)
        self.assertEqual(analyzed['nodes'][2]['subplanOrdinal'], 12)
        self.assertEqual(analyzed['nodes'][2]['counts']['Actual Loops'], 2)
        self.assertTrue(analyzed['loopCountIsNotFunctionInvocationCount'])

    def test_expression_dependency_requires_complete_equality(self):
        yes = runtime.expression('(l.pid = s.pid)')
        self.assertTrue(yes['simplePidEquality'])
        self.assertTrue(yes['crossAliasPidDependency'])
        self.assertCountEqual(yes['pidAliasSHA256'], [runtime.sha('l'), runtime.sha('s')])
        for value in ("'l.pid = s.pid'", '(l.pid = s.pid) OR true', 'l.pid = s.pid)',
                      '(l.pid = s.pid', '(l.pid > s.pid)', 'other_column = 1'):
            with self.subTest(value=value):
                self.assertFalse(runtime.expression(value)['simplePidEquality'])

    def test_bare_self_and_mixed_pid_equality_are_not_cross_alias_dependencies(self):
        for value in ('pid = pid', 'l.pid = l.pid', '(pid = s.pid)', '(l.pid = pid)'):
            with self.subTest(value=value):
                result = runtime.expression(value)
                self.assertTrue(result['simplePidEquality'])
                self.assertFalse(result['crossAliasPidDependency'])

    def test_projection_does_not_emit_expression_or_names(self):
        raw = plan()
        raw[0]['Plan']['Output'] = ["'private-query-marker'::text"]
        result = json.dumps(runtime.project_plan(raw, True))
        self.assertNotIn('private-query-marker', result)
        self.assertNotIn(runtime.APPLICATION, result)
        self.assertNotIn('pg_stat_get_activity', result)
        self.assertNotIn('(l.pid = s.pid)', result)
        self.assertIn(runtime.sha("'private-query-marker'::text"), result)

    def test_unknown_root_and_node_keys_are_rejected(self):
        for target in ('root', 'node'):
            raw = plan()
            (raw[0] if target == 'root' else raw[0]['Plan'])['Unreviewed Metadata'] = 'private'
            with self.subTest(target=target), self.assertRaises(runtime.PlanError):
                runtime.project_plan(raw, True)

    def test_types_sizes_nonfinite_and_negative_values_are_rejected(self):
        changes = [('Actual Loops', True), ('Actual Loops', 1.5), ('Actual Loops', -1),
                   ('Actual Total Time', float('nan')), ('Total Cost', float('inf')),
                   ('Plan Rows', 10**1000), ('Alias', 'bad alias'), ('Output', ['x'] * 65),
                   ('Filter', 'x' * 4097), ('Filter', 'x\ny'), ('Parallel Aware', 1),
                   ('Node Type', 'Unreviewed Scan'), ('Strategy', [])]
        for key, value in changes:
            raw = plan()
            raw[0]['Plan'][key] = value
            with self.subTest(key=key, value=type(value).__name__), self.assertRaises(runtime.PlanError):
                runtime.project_plan(raw, True)

    def test_actual_visibility_requires_two_owned_rows_and_exact_filter(self):
        for mutation in ('count', 'filter', 'loops', 'missing'):
            raw = plan()
            node = raw[0]['Plan']['Plans'][0]
            if mutation == 'count':
                node['Actual Rows'] = 3
            elif mutation == 'filter':
                node['Filter'] += ' OR true'
            elif mutation == 'loops':
                node['Actual Loops'] = 2
            else:
                del node['Actual Rows']
            with self.subTest(mutation=mutation), self.assertRaises(runtime.PlanError):
                runtime.project_plan(raw, True)

    def test_lock_function_no_arguments_and_execution_are_required(self):
        for mutation in ('argument', 'name', 'zero', 'missing'):
            raw = plan()
            node = raw[0]['Plan']['Plans'][1]
            if mutation == 'argument':
                node['Function Call'] = 'pg_lock_status(s.pid)'
            elif mutation == 'name':
                node['Function Name'] = 'unreviewed_function'
            elif mutation == 'zero':
                node['Actual Loops'] = 0
            else:
                raw[0]['Plan']['Plans'].pop()
            with self.subTest(mutation=mutation), self.assertRaises(runtime.PlanError):
                runtime.project_plan(raw, True)

    def test_bounded_depth_and_node_count(self):
        for deep in (False, True):
            raw = plan()
            if deep:
                node = raw[0]['Plan']
                for _ in range(34):
                    node['Plans'] = [{'Node Type': 'Result', 'Actual Rows': 1, 'Actual Loops': 1}]
                    node = node['Plans'][0]
            else:
                raw[0]['Plan']['Plans'] = [copy.deepcopy(raw[0]['Plan']['Plans'][1]) for _ in range(65)]
            with self.subTest(deep=deep), self.assertRaises(runtime.PlanError):
                runtime.project_plan(raw, True)

    def test_json_duplicates_constants_and_size_fail_closed(self):
        for raw in ('{"a":1,"a":2}', '[NaN]', ' ' * (1024 * 1024 + 1)):
            with self.subTest(raw=raw[:20]), self.assertRaises(runtime.PlanError):
                runtime.unique_json(raw)


class RuntimeOwnershipContracts(unittest.TestCase):
    def environment(self):
        return {'ADTR_RUN_OBSERVER_PLAN_TESTS': '1', 'GITHUB_ACTIONS': 'true', 'GITHUB_SHA': 'a' * 40,
                'ADTR_EVENT_HEAD_SHA': 'b' * 40, 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
                'ADTR_TEST_DATABASE_URL': 'postgres://postgres:synthetic@127.0.0.1:1234/adtr_test?sslmode=disable'}

    def exercise(self, bad_analyze=False, close_failure=False, migrate_failure=False,
                 leftover=0, constructor_failure=False, preparation=None, visible=2):
        clients = [Mock(), Mock()]
        for client in clients:
            client.open_transaction.return_value = preparation if preparation is not None else {
                'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0}
        if close_failure:
            clients[0].close.side_effect = RuntimeError('private-cleanup-error')
        calls = []

        def run(command, **kwargs):
            calls.append((command, kwargs))
            if migrate_failure and command[0] == 'bin/adtr':
                raise subprocess.CalledProcessError(1, command, stderr='private-migration-error')
            return subprocess.CompletedProcess(command, 0, stdout='a' * 40 + '\n')

        def read(container, sql):
            if "server_version_num" in sql:
                return '170006'
            if sql.startswith('EXPLAIN (ANALYZE'):
                if bad_analyze:
                    raise RuntimeError('private-plan-error')
                return json.dumps(plan())
            return json.dumps(plan(False))

        constructed = RuntimeError('private-constructor-error') if constructor_failure else clients
        with patch.object(runtime.subprocess, 'run', side_effect=run), \
                patch.object(runtime, 'OwnedSession', side_effect=constructed), \
                patch.object(runtime, 'observer_read', side_effect=read), \
                patch.object(runtime, 'session_count', return_value=visible), \
                patch.object(runtime, 'other_database_backends', return_value=leftover):
            result = runtime.run_experiment('adtr-auth-e2e-123456abcdef', self.environment())
        return result, clients, calls

    def test_success_uses_exclusive_database_real_migration_and_safe_receipt(self):
        result, clients, calls = self.exercise()
        self.assertTrue(result['success'])
        self.assertIsNone(result['firstFailureStage'])
        self.assertEqual(result['cleanup'], {'clientsClosed': True, 'zeroOtherDatabaseBackends': True})
        self.assertEqual(len(result['plans']), 2)
        self.assertEqual(result['ownedPreparation'], [
            {'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0}] * 2)
        self.assertEqual(result['ownedSessionCounts'], {'beforePlan': 2, 'afterPlan': 2})
        self.assertEqual(calls[0][0], ['git', 'rev-parse', 'HEAD'])
        self.assertEqual(calls[1][0], ['docker', 'exec', 'adtr-auth-e2e-123456abcdef',
                                     'createdb', '-U', 'postgres', 'adtr_observer_plan'])
        self.assertEqual(calls[2][0], ['bin/adtr', '-mode=migrate'])
        self.assertEqual(calls[2][1]['env']['ADTR_DATABASE_URL'],
                         'postgres://postgres:synthetic@127.0.0.1:1234/adtr_observer_plan?sslmode=disable')
        for client in clients:
            client.open_transaction.assert_called_once()
            client.close.assert_called_once()
        self.assertNotIn('synthetic@', json.dumps(result))

    def test_first_plan_failure_survives_cleanup_failure(self):
        result, clients, _ = self.exercise(bad_analyze=True, close_failure=True)
        self.assertFalse(result['success'])
        self.assertEqual(result['firstFailureStage'], 'analyze')
        self.assertFalse(result['cleanup']['clientsClosed'])
        self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])
        self.assertNotIn('private-', json.dumps(result))
        for client in clients:
            client.close.assert_called_once()

    def test_migration_failure_never_opens_clients_and_still_verifies_cleanup(self):
        result, clients, _ = self.exercise(migrate_failure=True)
        self.assertEqual(result['firstFailureStage'], 'migrate')
        self.assertFalse(result['success'])
        self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])
        for client in clients:
            client.open_transaction.assert_not_called()

    def test_bad_scope_and_checkout_cannot_create_database(self):
        local = self.environment()
        local.pop('GITHUB_ACTIONS')
        cases = [('', self.environment()), ('adtr-auth-e2e-123456abcdef', {}),
                 ('adtr-auth-e2e-123456abcdef', local)]
        for container, environ in cases:
            with self.subTest(container=container), patch.object(runtime.subprocess, 'run') as run:
                result = runtime.run_experiment(container, environ)
                self.assertEqual(result['firstFailureStage'], 'inputs')
                run.assert_not_called()
        with patch.object(runtime.subprocess, 'run', return_value=Mock(stdout='c' * 40)) as run:
            result = runtime.run_experiment('adtr-auth-e2e-123456abcdef', self.environment())
            self.assertEqual(result['firstFailureStage'], 'inputs')
            self.assertEqual(run.call_count, 1)

    def test_url_is_only_existing_loopback_fixture_retargeted(self):
        for url in ('postgres://postgres:x@external/adtr_test?sslmode=disable',
                    'postgres://postgres:x@127.0.0.1:123/adtr_e2e?sslmode=disable',
                    'postgres://postgres:x@127.0.0.1:123/adtr_test?sslmode=disable&host=external',
                    'postgres://postgres:x@127.0.0.1:123/adtr_test?sslmode=disable#secret'):
            with self.subTest(url=url), self.assertRaises(runtime.PlanError):
                runtime.owned_url(url)

    def test_psql_is_bounded_readonly_and_has_no_connection_credentials(self):
        command = runtime.psql_command('adtr-auth-e2e-123456abcdef', runtime.APPLICATION)
        self.assertIn('-c statement_timeout=1000 -c idle_in_transaction_session_timeout=8000 '
                      '-c default_transaction_read_only=on',
                      command[4].removeprefix('PGOPTIONS='))
        self.assertIn(runtime.DATABASE, command)
        self.assertNotIn('postgres://', ' '.join(command))
        with self.assertRaises(runtime.PlanError):
            runtime.psql_command('external', runtime.APPLICATION)

    def test_explicit_appname_beats_psql_startup_fallback_model(self):
        # libpq's startup appname is PGAPPNAME when supplied, else psql's
        # fallback. The server applies it after PGOPTIONS at equal priority.
        # This is an offline constructor regression, not a real PG run.
        for application in (runtime.APPLICATION, runtime.OBSERVER):
            with self.subTest(application=application):
                command = runtime.psql_command('adtr-auth-e2e-123456abcdef', application)
                env = dict(command[index + 1].split('=', 1) for index, item in enumerate(command)
                           if item == '-e')
                self.assertEqual(env.get('PGAPPNAME', 'psql'), application)
                self.assertNotIn('application_name=', env['PGOPTIONS'])

    def test_preparation_projection_requires_exact_closed_types(self):
        good = {'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0}
        self.assertEqual(runtime.preparation_checked(good), good)
        bad = [False, 0, {**good, 'appMatches': 'actual-private-name'},
               {**good, 'transactionReadOnly': 1}, {**good, 'activityRows': False},
               {**good, 'activityRows': -1}, {**good, 'activityRows': 10**6 + 1},
               {**good, 'pid': 123}, {'appMatches': True, 'activityRows': 0}]
        for value in bad:
            with self.subTest(value=type(value).__name__), self.assertRaises(runtime.PlanError):
                runtime.preparation_checked(value)

    def test_existing_roundtrip_checks_actual_transaction_and_identity_once(self):
        session = runtime.OwnedSession.__new__(runtime.OwnedSession)
        session.process = Mock()
        session.lines = Mock()
        proof = {'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0}
        session.lines.get.return_value = json.dumps(proof)
        self.assertEqual(session.open_transaction(), proof)
        session.process.stdin.write.assert_called_once()
        sql = session.process.stdin.write.call_args.args[0]
        self.assertTrue(sql.startswith('BEGIN; SELECT jsonb_build_object('))
        self.assertEqual(sql.count(';'), 2)
        self.assertIn("current_setting('application_name')='" + runtime.APPLICATION + "'", sql)
        self.assertIn("current_setting('transaction_read_only')='on'", sql)
        self.assertNotIn("current_setting('default_transaction_read_only')", sql)
        self.assertIn('SELECT count(*) FROM adtr.system_worker_activity', sql)
        session.lines.get.assert_called_once_with(timeout=3)
        session.process.stdin.flush.assert_called_once()

    def test_wrong_identity_readonly_or_nonempty_fixture_stops_before_explain(self):
        for key, value in (('appMatches', False), ('transactionReadOnly', False), ('activityRows', 1)):
            proof = {'appMatches': True, 'transactionReadOnly': True, 'activityRows': 0}
            proof[key] = value
            with self.subTest(key=key):
                result, clients, _ = self.exercise(preparation=proof)
                self.assertEqual(result['firstFailureStage'], 'owned_sessions')
                self.assertEqual(result['ownedPreparation'], [proof])
                self.assertEqual(result['ownedSessionCounts'], {'beforePlan': None, 'afterPlan': None})
                self.assertEqual(result['planAttempts'], [])
                self.assertEqual(result['plans'], [])
                self.assertFalse(result['success'])
                self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])
                clients[0].close.assert_called_once()
                clients[1].open_transaction.assert_not_called()

    def test_safe_zero_count_is_retained_without_relaxing_two_session_requirement(self):
        result, _, _ = self.exercise(visible=0)
        self.assertEqual(result['firstFailureStage'], 'owned_visibility')
        self.assertEqual(result['ownedSessionCounts'], {'beforePlan': 0, 'afterPlan': None})
        self.assertEqual(len(result['ownedPreparation']), 2)
        self.assertFalse(result['success'])
        self.assertEqual(result['planAttempts'], [])

    def test_normal_reap_never_sends_signal(self):
        process = Mock(returncode=0)
        self.assertTrue(runtime.reap_process(process))
        process.stdin.close.assert_called_once()
        process.wait.assert_called_once_with(timeout=2)
        process.terminate.assert_not_called()
        process.kill.assert_not_called()

    def test_broken_stdin_still_reaps_and_marks_cleanup_failed(self):
        process = Mock(returncode=0)
        process.stdin.close.side_effect = BrokenPipeError('private')
        self.assertFalse(runtime.reap_process(process))
        process.wait.assert_called_once_with(timeout=2)

    def test_timeout_cleanup_is_bounded_and_never_reported_clean(self):
        process = Mock(returncode=-9)
        process.wait.side_effect = [subprocess.TimeoutExpired('owned', 2),
                                    subprocess.TimeoutExpired('owned', 2), None]
        self.assertFalse(runtime.reap_process(process))
        self.assertEqual(process.wait.call_count, 3)
        process.terminate.assert_called_once()
        process.kill.assert_called_once()

    def test_owned_session_close_joins_reader_even_after_process_failure(self):
        session = runtime.OwnedSession.__new__(runtime.OwnedSession)
        session.process = Mock(returncode=1)
        session.reader = Mock()
        session.reader.is_alive.return_value = False
        session.read_failed = False
        with self.assertRaises(runtime.PlanError):
            session.close()
        session.reader.join.assert_called_once_with(timeout=2)
        session.process.stdout.close.assert_called_once()

    def test_owned_reader_exception_is_private_and_signals_failure(self):
        session = runtime.OwnedSession.__new__(runtime.OwnedSession)
        session.process = Mock()
        session.process.stdout.readline.side_effect = OSError('private-reader-error')
        session.lines = runtime.queue.Queue(maxsize=4)
        session.read_failed = False
        session._read()
        self.assertTrue(session.read_failed)
        self.assertIsNone(session.lines.get_nowait())

    def test_leftover_observer_or_migration_backend_fails_cleanup(self):
        for count in (1, 2):
            with self.subTest(count=count):
                result, _, _ = self.exercise(leftover=count)
                self.assertFalse(result['success'])
                self.assertEqual(result['firstFailureStage'], 'cleanup')
                self.assertFalse(result['cleanup']['zeroOtherDatabaseBackends'])
        result, _, _ = self.exercise(bad_analyze=True, leftover=1)
        self.assertEqual(result['firstFailureStage'], 'analyze')

    def test_cleanup_query_covers_whole_owned_database(self):
        with patch.object(runtime, 'observer_read', return_value='0') as read:
            self.assertEqual(runtime.other_database_backends('synthetic'), 0)
        sql = read.call_args.args[1]
        self.assertIn('datname=current_database() AND pid<>pg_backend_pid()', sql)
        self.assertNotIn('application_name', sql)

    def test_thread_start_failure_escalates_and_reaps_exact_owned_process(self):
        process = Mock(returncode=-9)
        process.wait.side_effect = [subprocess.TimeoutExpired('owned', 2),
                                    subprocess.TimeoutExpired('owned', 2), None]
        reader = Mock()
        reader.start.side_effect = RuntimeError('private-thread-start')
        with patch.object(runtime.subprocess, 'Popen', return_value=process), \
                patch.object(runtime.threading, 'Thread', return_value=reader), \
                self.assertRaisesRegex(RuntimeError, '^private-thread-start$'):
            runtime.OwnedSession('adtr-auth-e2e-123456abcdef')
        self.assertEqual(process.wait.call_count, 3)
        process.terminate.assert_called_once()
        process.kill.assert_called_once()
        process.stdout.close.assert_called_once()

    def test_exhausted_constructor_reaping_cannot_report_clients_closed(self):
        process = Mock(returncode=None)
        process.wait.side_effect = OSError('private-unconfirmed-wait')
        reader = Mock()
        reader.start.side_effect = RuntimeError('private-original-start')
        with patch.object(runtime.subprocess, 'Popen', return_value=process), \
                patch.object(runtime.threading, 'Thread', return_value=reader), \
                self.assertRaisesRegex(RuntimeError, '^private-original-start$'):
            runtime.OwnedSession('adtr-auth-e2e-123456abcdef')
        self.assertEqual(process.wait.call_count, 3)
        process.terminate.assert_called_once()
        process.kill.assert_called_once()
        result, _, _ = self.exercise(constructor_failure=True)
        self.assertEqual(result['firstFailureStage'], 'owned_sessions')
        self.assertFalse(result['cleanup']['clientsClosed'])
        self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])
        self.assertFalse(result['success'])

    def test_failed_projector_retains_hash_without_raw_plan(self):
        raw = json.dumps(plan(False))
        with patch.object(runtime, 'project_plan', side_effect=runtime.PlanError()):
            result, _, _ = self.exercise()
        self.assertEqual(result['firstFailureStage'], 'explain')
        self.assertEqual(len(result['planAttempts']), 1)
        self.assertFalse(result['planAttempts'][0]['projected'])
        self.assertEqual(result['planAttempts'][0]['rawPlanSHA256'], runtime.sha(raw))
        self.assertEqual(result['plans'], [])
        self.assertNotIn(raw, json.dumps(result))


class SourceScopeContracts(unittest.TestCase):
    def test_original_observer_sql_is_used_without_rewrite(self):
        source = Path(runtime.__file__).read_text()
        self.assertIn('sql = RunningExportCancelGuard.observe_sql(context, TASK)', source)
        self.assertNotIn('sql.replace(', source)
        self.assertNotIn('pg_sleep', source)
        self.assertNotIn('SAVEPOINT', source)
        self.assertNotIn('QueryExecMode', source)
        context = runtime.SimpleNamespace(asset_kind='group', application_name=runtime.APPLICATION)
        sql = RunningExportCancelGuard.observe_sql(context, runtime.TASK)
        self.assertIn("application_name='adtr-observer-plan-owned'", sql)
        self.assertIn("task_id='observer-plan-synthetic-task'", sql)
        self.assertEqual(runtime.sha(sql), hashlib.sha256(sql.encode()).hexdigest())

    def test_additive_hook_is_only_non_auth_and_has_existing_budget(self):
        source = Path('scripts/test_integration.py').read_text()
        hook = ('        if fixture_psql and os.environ.get("GITHUB_ACTIONS") == "true":\n'
                '            subprocess.run(["python3", "scripts/test_observer_plan_runtime.py", "--container", name],\n'
                '                           env=dict(env, ADTR_RUN_OBSERVER_PLAN_TESTS="1"), check=True, timeout=120)\n')
        self.assertEqual(source.count(hook), 1)
        self.assertLess(source.index('env["ADTR_TEST_DATABASE_URL"]'), source.index(hook))
        self.assertLess(source.index(hook), source.index('return run_tests(args, env)'))
        self.assertIn('finally:\n        if created:', source)
        self.assertIn('["docker", "rm", "--force", name]', source)

    def test_original_runner_bytes_survive_exact_additive_hook_projection(self):
        source = Path('scripts/test_integration.py').read_text()
        tree = ast.parse(source)
        hooks = [node for node in ast.walk(tree) if isinstance(node, ast.If) and
                 ast.unparse(node.test) == "fixture_psql and os.environ.get('GITHUB_ACTIONS') == 'true'"]
        self.assertEqual(len(hooks), 1)
        lines = source.splitlines(keepends=True)
        original = ''.join(lines[:hooks[0].lineno - 1] + lines[hooks[0].end_lineno:])
        # Narrow existing-runner proof: no command, budget, fixture or cleanup
        # outside this new CI-only call may change under its additive scope.
        expected = 'ac325584ec702b174a1c9529ce862a26f374fb8e66e7c2256dee0799f35bd216'
        self.assertEqual(hashlib.sha256(original.encode()).hexdigest(), expected)

    def test_runtime_is_not_discovered_as_offline_contract(self):
        self.assertTrue(Path(runtime.__file__).name.endswith('_runtime.py'))
        tree = ast.parse(Path(runtime.__file__).read_text())
        top_calls = [node for node in tree.body if isinstance(node, ast.Expr) and isinstance(node.value, ast.Call)]
        self.assertEqual(top_calls, [])
        self.assertIn("if __name__ == '__main__':", Path(runtime.__file__).read_text())


if __name__ == '__main__':
    unittest.main()
