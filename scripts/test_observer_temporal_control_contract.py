"""Offline contracts for a separate explicit PG temporal control; no services."""
import ast
import copy
import json
import io
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock, patch

import test_observer_temporal_control_runtime as runtime


PIDS = {'a': 101, 'b': 102}
CONTROLLER = 201


def raw_plan(analyzed, variant=False):
    def node(kind, **extra):
        value = {'Node Type': kind, 'Plan Rows': 1, 'Plan Width': 8, **extra}
        if analyzed:
            value.update({'Actual Rows': 1, 'Actual Loops': 1})
        return value
    activity = node('Function Scan', **{
        'Function Name': 'pg_stat_get_activity', 'Function Call': 'pg_stat_get_activity(NULL::integer)',
        'Alias': 's', 'Parent Relationship': 'Outer',
        'Filter': "(s.application_name = 'adtr-observer-plan-owned'::text)"})
    locks = node('Function Scan', **{'Function Name': 'pg_lock_status', 'Function Call': 'pg_lock_status()',
                                   'Alias': 'l', 'Parent Relationship': 'Outer', 'Filter': '(l.pid = s.pid)'})
    if analyzed:
        activity['Actual Rows'] = 2
        locks['Actual Loops'] = 2
    subplan = node('Aggregate', **{'Parent Relationship': 'SubPlan', 'Subplan Name': 'SubPlan 7', 'Plans': [locks]})
    join = node('Nested Loop', Plans=[activity, node('Seq Scan', **{'Parent Relationship': 'Inner'})],
                **{'Parent Relationship': 'Outer'})
    owner = node('Aggregate', Plans=[join, subplan], **{'Parent Relationship': 'InitPlan',
                 'Subplan Name': 'InitPlan 8 (returns $7)', 'Output': ['wrapped' if variant else 'original']})
    root = {'Plan': node('Result', Plans=[owner])}
    if analyzed:
        root.update({'Planning Time': .1, 'Execution Time': .2, 'Triggers': []})
    return [root]


def plans():
    return {label + ('_static_analyze' if analyzed else '_plain'):
            runtime.plan_fixture.project_plan(raw_plan(analyzed, label == 'variant'), analyzed)
            for label in ('original', 'variant') for analyzed in (False, True)}


def row(role, marker=True, blocker=False):
    locks = [{'type': 'relation', 'mode': 'AccessShareLock', 'granted': True,
              'relation': runtime.MARKER, 'classid': None, 'objid': None, 'objsubid': None}] if marker else []
    return {'pid': PIDS[role], 'transaction': True, 'state': 'idle in transaction',
            'query': 'private-original-query', 'waitType': 'Client',
            'blockers': [CONTROLLER] if blocker else [], 'locks': locks}


def sample(first='a', marker=True, blocker=True, after=False):
    target = 'b' if first == 'a' else 'a'
    return {'otherNonterminal': 0, 'journalCount': 0, 'task': None,
            'sessions': [row(first), row(target, False if after else marker, blocker)]}


def state(markers=(0, 0), target=None):
    result = {'roles': sorted(PIDS.values())}
    for index, role in enumerate(runtime.ROLES):
        result.update({role + 'Marker': markers[index], role + 'Blocker': role == target,
                       role + 'BlockerCount': int(role == target), role + 'LockWait': role == target})
    return result


class PureContracts(unittest.TestCase):
    def test_variant_is_one_complete_row_wrapper_without_rewriting_inputs(self):
        original = runtime.original_sql()
        variant = runtime.variant_sql(original, PIDS)
        self.assertEqual(variant.count('observer_temporal.row_gate('), 1)
        self.assertEqual(variant.count('pg_blocking_pids(a.pid)'), 1)
        self.assertEqual(variant.count('FROM pg_locks l WHERE l.pid=a.pid'), 1)
        prefix = 'observer_temporal.row_gate(CASE a.pid WHEN 101 THEN 1 WHEN 102 THEN 2 ELSE 0 END,'
        self.assertIn(prefix + "jsonb_build_object('pid',a.pid", variant)
        # Outer added closing parenthesis is removed only at the known row end.
        restored = variant.replace(prefix, '', 1)
        ending = "),'[]'::jsonb))))\n FROM pg_stat_activity"
        self.assertIn(ending, restored)
        self.assertEqual(restored.replace(ending, "),'[]'::jsonb)))\n FROM pg_stat_activity", 1), original)

    def test_variant_refuses_missing_duplicate_or_unbalanced_anchor_and_bad_roles(self):
        for original in ('SELECT 1', runtime.original_sql() * 2, "jsonb_build_object('pid',a.pid"):
            with self.subTest(original=original[:20]), self.assertRaises(runtime.Fault):
                runtime.variant_sql(original, PIDS)
        for pids in ({'a': True, 'b': 102}, {'a': 101, 'b': 101}, {'a': 101}, {'a': 'private', 'b': 102}):
            with self.subTest(pids=pids), self.assertRaises(runtime.Fault):
                runtime.variant_sql(runtime.original_sql(), pids)

    def test_comparable_plan_checks_correlation_topology_not_just_count(self):
        result = runtime.compare_plans(plans())
        self.assertTrue(result['comparableVisibleTopology'])
        self.assertFalse(result['sameExecutionAsGatedSelect'])
        self.assertFalse(result['functionInvocationCountMeasured'])
        self.assertEqual(result['wrapperOwnerOrdinal'], 1)

    def test_changed_topology_filter_parameters_or_loops_are_inconclusive(self):
        for mutation in ('parent', 'function', 'filter', 'output_elsewhere', 'static_output', 'loops', 'extra_node'):
            value = plans()
            nodes = value['variant_plain']['nodes']
            if mutation == 'parent':
                nodes[-1]['parent'] = 0
            elif mutation == 'function':
                nodes[-1]['lockFunctionHasNoArguments'] = False
            elif mutation == 'filter':
                nodes[-1]['expressions']['Filter']['crossAliasPidDependency'] = False
            elif mutation == 'output_elsewhere':
                nodes[0]['expressionLists'] = {'Output': []}
            elif mutation == 'static_output':
                value['variant_static_analyze']['nodes'][0]['expressionLists'] = {'Output': []}
            elif mutation == 'loops':
                value['variant_static_analyze']['nodes'][-1]['counts']['Actual Loops'] = 1
            else:
                nodes.append(copy.deepcopy(nodes[-1]))
            with self.subTest(mutation=mutation), self.assertRaises(runtime.Fault) as caught:
                runtime.compare_plans(value)
            self.assertEqual(caught.exception.reason, 'inconclusive_plan_change')

    def test_four_combinations_are_exhaustive_without_false_negative(self):
        expected = {(True, True): 'controlled_cross_time_combination_observed',
                    (False, True): 'controlled_combination_not_observed',
                    (True, False): 'inconclusive_later_read_not_established',
                    (False, False): 'inconclusive_later_read_not_established'}
        for (marker, blocker), outcome in expected.items():
            self.assertEqual(runtime.classify_target({'role': 'a', 'marker': marker,
                             'controllerBlocker': blocker, 'otherBlockers': 0}), outcome)

    def test_invalid_classification_never_becomes_negative(self):
        good = {'role': 'a', 'marker': True, 'controllerBlocker': True, 'otherBlockers': 0}
        for key, value in (('marker', 1), ('controllerBlocker', None), ('role', 'private'), ('otherBlockers', 1)):
            with self.subTest(key=key), self.assertRaises(runtime.Fault):
                runtime.classify_target({**good, key: value})
        with self.assertRaises(runtime.Fault):
            runtime.classify_target({'marker': False})

    def test_only_exact_prebound_rendering_and_target_role_are_used(self):
        value = sample()
        result = runtime.projected_rows(value, PIDS, CONTROLLER, runtime.MARKER)
        self.assertTrue(result[1]['marker'])
        value['sessions'][1]['locks'][0]['relation'] = 'other_schema.marker'
        self.assertFalse(runtime.projected_rows(value, PIDS, CONTROLLER, runtime.MARKER)[1]['marker'])
        text = json.dumps(result)
        for private in ('private-original-query', runtime.MARKER, 'pid', 'classid', 'objid'):
            self.assertNotIn(private, text)

    def test_result_rejects_duplicate_unowned_or_malformed_rows(self):
        for mutation in ('duplicate_role', 'unknown_role', 'bool_pid', 'extra_root', 'extra_lock',
                         'wrong_mode', 'extra_blocker', 'bool_transaction'):
            value = sample()
            if mutation == 'duplicate_role':
                value['sessions'][1]['pid'] = PIDS['a']
            elif mutation == 'unknown_role':
                value['sessions'][1]['pid'] = 999
            elif mutation == 'bool_pid':
                value['sessions'][1]['pid'] = True
            elif mutation == 'extra_root':
                value['private'] = 'private'
            elif mutation == 'extra_lock':
                value['sessions'][1]['locks'][0]['private'] = 'private'
            elif mutation == 'wrong_mode':
                value['sessions'][1]['locks'][0]['mode'] = 'AccessExclusiveLock'
            elif mutation == 'extra_blocker':
                value['sessions'][1]['blockers'] = [CONTROLLER, CONTROLLER]
            else:
                value['sessions'][1]['transaction'] = 1
            with self.subTest(mutation=mutation), self.assertRaises(runtime.Fault):
                runtime.projected_rows(value, PIDS, CONTROLLER, runtime.MARKER)

    def test_raw_lock_identity_fields_are_bounded_not_arbitrary_opaque_values(self):
        for key in ('classid', 'objid'):
            for value in ({'private': []}, ['nested'], True, 1, '00', '-1', '4294967296', '9' * 100):
                raw = sample()
                raw['sessions'][0]['locks'][0][key] = value
                with self.subTest(key=key, value=type(value).__name__), self.assertRaises(runtime.Fault):
                    runtime.projected_rows(raw, PIDS, CONTROLLER, runtime.MARKER)
        for value in (True, -(2**15) - 1, 2**15, 0.5, '1', {'private': 1}):
            raw = sample()
            raw['sessions'][0]['locks'][0]['objsubid'] = value
            with self.subTest(value=type(value).__name__), self.assertRaises(runtime.Fault):
                runtime.projected_rows(raw, PIDS, CONTROLLER, runtime.MARKER)
        # PG17 lockfuncs.c declares objsubid as INT2OID, not int32.
        for value in (None, -(2**15), -1, 0, 2**15 - 1):
            raw = sample()
            raw['sessions'][0]['locks'][0].update(classid='0', objid='4294967295', objsubid=value)
            self.assertTrue(runtime.projected_rows(raw, PIDS, CONTROLLER, runtime.MARKER)[0]['marker'])

    def test_verifier_requires_exact_typed_stable_role_set(self):
        self.assertEqual(runtime.checked_state(state(), PIDS)['aMarker'], 0)
        for key, item in (('roles', [101, 101]), ('aMarker', True), ('aBlocker', 1),
                          ('aMarker', -1), ('bMarker', 9)):
            with self.subTest(key=key), self.assertRaises(runtime.Fault):
                runtime.checked_state({**state(), key: item}, PIDS)

    def test_poll_is_finite_and_shares_deadline_without_renewing(self):
        read = Mock(return_value=False)
        with patch.object(runtime.time, 'monotonic', return_value=10), patch.object(runtime.time, 'sleep') as sleep:
            with self.assertRaises(runtime.Fault) as error:
                runtime.poll(read, bool, 10.75)
        self.assertEqual(error.exception.reason, 'inconclusive_ordering')
        self.assertEqual(read.call_count, 16)
        self.assertEqual(sleep.call_count, 15)
        self.assertTrue(all(call.args == (10.75,) for call in read.call_args_list))
        with patch.object(runtime.time, 'monotonic', return_value=11), self.assertRaises(runtime.Fault):
            runtime.poll(read, bool, 10.75)
        self.assertEqual(read.call_count, 16)

    def test_command_bounds_privacy_and_role_specific_readonly(self):
        for role in runtime.CLIENT_ROLES:
            command = runtime.psql_command('adtr-auth-e2e-123456abcdef', role)
            text = ' '.join(command)
            self.assertIn('statement_timeout=1000', text)
            self.assertIn('idle_in_transaction_session_timeout=8000', text)
            self.assertIn('default_transaction_read_only=' + ('on' if role in ('observer', 'verifier') else 'off'), text)
            self.assertNotIn('postgres://', text)
            self.assertIn('PGAPPNAME=', text)
        self.assertIn('pg_advisory_xact_lock', runtime.GATE_BODY)
        self.assertNotIn('pg_advisory_lock(', runtime.GATE_BODY)
        self.assertIn('RETURN payload', runtime.GATE_BODY)


class TransitionContracts(unittest.TestCase):
    def exercise(self, first='a', marker=True, blocker=True, wrong_zero=False, after_bad=False,
                 duplicate_gate=False, raw_bad=False, work_deadline=11, first_bad=False):
        target = 'b' if first == 'a' else 'a'
        clients = {role: Mock(pid={'a': 101, 'b': 102, 'controller': 201, 'observer': 202, 'verifier': 203}[role])
                   for role in runtime.CLIENT_ROLES}
        clients[target].query.return_value = True
        markers = (0, 1) if target == 'a' else (1, 0)
        before = state(markers)
        if wrong_zero:
            before[target + 'Marker'] = 1
        ready = state(markers, target)
        gate = {'a': int(first == 'a'), 'b': int(first == 'b')}
        if duplicate_gate:
            gate = {'a': 1, 'b': 1}
        clients['verifier'].query.side_effect = [gate, before, ready, ready]
        clients['controller'].query.return_value = {'a': True, 'b': True}
        original = sample(first, marker, blocker)
        if first_bad:
            original['sessions'][0]['blockers'] = [CONTROLLER]
        clients['observer'].receive_raw.return_value = '{private-invalid' if raw_bad else json.dumps(original)
        clients['observer'].query_raw.return_value = json.dumps(sample(first, marker=after_bad, blocker=True, after=not after_bad))
        events, receipt = [], {'gated': None, 'after': None, 'outcome': None}
        error = None
        with patch.object(runtime.time, 'monotonic', return_value=10):
            try:
                runtime.transition(clients, PIDS, CONTROLLER, 301, 401, 'private-variant-sql', runtime.MARKER,
                                   lambda name, **facts: events.append((name, facts)), receipt, work_deadline)
            except runtime.Fault as caught:
                error = caught
        return receipt, events, clients, error

    def test_dynamic_roles_and_release_ack_order_before_gate(self):
        for first in ('a', 'b'):
            receipt, events, clients, error = self.exercise(first=first)
            self.assertIsNone(error)
            self.assertEqual(receipt['outcome'], 'controlled_cross_time_combination_observed')
            self.assertEqual([name for name, _ in events], ['first_row_gate_observed', 'rollback_ack',
                             'marker_zero_before_barrier', 'barrier_confirmed', 'marker_zero_after_barrier',
                             'gates_released', 'gated_result', 'coherent_after'])
            self.assertEqual(events[0][1]['role'], first)
            self.assertEqual(receipt['gated']['rows'][0]['role'], first)
            for client in clients.values():
                for method in ('query', 'send', 'receive_raw', 'query_raw'):
                    self.assertTrue(all(call.args[-1] == 10.75 for call in getattr(client, method).call_args_list))

    def test_abnormal_first_row_is_retained_and_cannot_be_hidden_by_after_control(self):
        receipt, _, clients, error = self.exercise(first_bad=True)
        self.assertIsNotNone(error)
        self.assertEqual(error.reason, 'inconclusive_ordering')
        self.assertTrue(receipt['gated']['rows'][0]['controllerBlocker'])
        self.assertIsNone(receipt['outcome'])
        clients['observer'].query_raw.assert_not_called()

    def test_work_deadline_can_only_shorten_transition(self):
        _, _, clients, error = self.exercise(work_deadline=10.4)
        self.assertIsNone(error)
        self.assertEqual(clients['observer'].send.call_args.args[-1], 10.4)

    def test_false_blocker_never_runs_after_control_or_gets_rescued(self):
        for marker in (True, False):
            receipt, events, clients, error = self.exercise(marker=marker, blocker=False)
            self.assertIsNone(error)
            self.assertEqual(receipt['outcome'], 'inconclusive_later_read_not_established')
            self.assertIsNone(receipt['after'])
            clients['observer'].query_raw.assert_not_called()
            self.assertEqual(events[-1][0], 'gated_result')

    def test_valid_negative_still_requires_later_blocker_and_after_control(self):
        receipt, events, clients, error = self.exercise(marker=False, blocker=True)
        self.assertIsNone(error)
        self.assertEqual(receipt['outcome'], 'controlled_combination_not_observed')
        clients['observer'].query_raw.assert_called_once()

    def test_marker_not_released_or_role_gate_ambiguity_stops_before_barrier(self):
        for kwargs in ({'wrong_zero': True}, {'duplicate_gate': True}):
            receipt, _, clients, error = self.exercise(**kwargs)
            self.assertIsNotNone(error)
            self.assertIsNone(receipt['gated'])
            clients['a'].send.assert_not_called()
            clients['b'].send.assert_not_called()
            clients['controller'].query.assert_not_called()

    def test_first_returned_gated_evidence_survives_failed_after_control(self):
        receipt, _, clients, error = self.exercise(after_bad=True)
        self.assertIsNotNone(error)
        self.assertIsNotNone(receipt['gated'])
        self.assertTrue(receipt['gatedAttempt']['projected'])
        self.assertIsNone(receipt['after'])
        clients['observer'].receive_raw.assert_called_once()

    def test_bad_original_body_keeps_hash_without_second_parse_or_raw(self):
        receipt, _, clients, error = self.exercise(raw_bad=True)
        self.assertIsNotNone(error)
        self.assertEqual(receipt['gatedAttempt']['rawResultSHA256'], runtime.plan_fixture.sha('{private-invalid'))
        self.assertFalse(receipt['gatedAttempt']['projected'])
        self.assertNotIn('private-invalid', json.dumps(receipt))
        clients['observer'].query_raw.assert_not_called()


class ClientAndCleanupContracts(unittest.TestCase):
    def test_nul_record_framing_preserves_multiline_explain_without_truncation(self):
        raw = '[\n {"Plan": {"private": "kept only in memory"}}\n]'
        self.assertEqual(list(runtime.records(io.StringIO(raw + '\0true\0'))), [raw, 'true'])
        self.assertEqual(runtime.one_record(raw + '\0'), raw)
        self.assertIn('-0', runtime.psql_command('adtr-auth-e2e-123456abcdef', 'observer'))

    def test_empty_partial_oversized_and_multiple_record_payloads_fail_closed(self):
        for raw in ('\0', 'partial', 'x' * (1024 * 1024 + 1) + '\0'):
            with self.subTest(size=len(raw)), self.assertRaises(runtime.Fault):
                list(runtime.records(io.StringIO(raw)))
        for raw in ('', '0', '0\0extra', '0\0true\0'):
            with self.subTest(raw=raw), self.assertRaises(runtime.Fault):
                runtime.one_record(raw)

    def test_reader_handles_separate_records_and_marks_overflow_or_partial_eof(self):
        for raw, failed, size in (('true\0false\0', False, 3),
                                  ('true\0partial', True, 2), ('true\0' * 9, True, 8)):
            client = runtime.Client.__new__(runtime.Client)
            client.process = Mock(stdout=io.StringIO(raw))
            client.lines = runtime.queue.Queue(maxsize=8)
            client.failed = False
            client._read()
            self.assertEqual(client.failed, failed)
            self.assertEqual(client.lines.qsize(), size)
        self.assertEqual(list(runtime.records(io.StringIO('t' + 'r' + 'u' + 'e' + '\0'))), ['true'])

    def test_normal_close_does_not_signal_and_deadline_expiry_does_not_skip_kill(self):
        process = Mock(returncode=0)
        with patch.object(runtime.time, 'monotonic', return_value=10):
            self.assertTrue(runtime.reap_process(process, 11))
        process.terminate.assert_not_called()
        process.kill.assert_not_called()
        failed = Mock()
        failed.wait.side_effect = subprocess.TimeoutExpired('private', 0)
        with patch.object(runtime.time, 'monotonic', return_value=11):
            self.assertFalse(runtime.reap_process(failed, 10))
        failed.terminate.assert_called_once()
        failed.kill.assert_called_once()
        self.assertEqual(failed.wait.call_count, 3)
        self.assertTrue(all(call.kwargs['timeout'] == 0 for call in failed.wait.call_args_list))

    def test_cleanup_always_visits_all_clients_controller_first_even_expired(self):
        order = []
        clients = {role: Mock() for role in runtime.CLIENT_ROLES}
        for role, client in clients.items():
            def close(deadline, role=role):
                order.append(role)
                raise RuntimeError('private-close-error')
            client.close.side_effect = close
        with patch.object(runtime.time, 'monotonic', return_value=11), patch.object(runtime.subprocess, 'run') as run:
            result = runtime.cleanup(clients, True, 'adtr-auth-e2e-123456abcdef', True, 10)
        self.assertEqual(order, ['controller', 'a', 'b', 'observer', 'verifier'])
        self.assertEqual(result, {'clientsClosed': False, 'zeroOtherDatabaseBackends': False})
        run.assert_not_called()
        for client in clients.values():
            client.process.kill.assert_called_once()
            client.process.wait.assert_called_once_with(timeout=0)

    def test_fresh_cleanup_checks_whole_database_not_application_name(self):
        clients = {role: Mock() for role in runtime.CLIENT_ROLES}
        for client in clients.values():
            client.close.return_value = True
        with patch.object(runtime.time, 'monotonic', return_value=10), \
                patch.object(runtime.subprocess, 'run', return_value=Mock(stdout='0\0')) as run:
            result = runtime.cleanup(clients, True, 'adtr-auth-e2e-123456abcdef', True, 20)
        self.assertEqual(result, {'clientsClosed': True, 'zeroOtherDatabaseBackends': True})
        sql = run.call_args.kwargs['input']
        self.assertIn('pid<>pg_backend_pid()', sql)
        self.assertNotIn('application_name', sql)

    def test_unconfirmed_constructor_never_reports_all_clients_closed(self):
        with patch.object(runtime.time, 'monotonic', return_value=10):
            result = runtime.cleanup({}, False, 'adtr-auth-e2e-123456abcdef', False, 20)
        self.assertFalse(result['clientsClosed'])

    def test_client_receive_timeout_and_protocol_errors_are_closed(self):
        client = runtime.Client.__new__(runtime.Client)
        client.pending, client.failed = True, False
        client.lines = Mock()
        client.lines.get.side_effect = runtime.queue.Empty()
        with patch.object(runtime.time, 'monotonic', return_value=10), self.assertRaises(runtime.Fault) as error:
            client.receive_raw(10.75)
        self.assertEqual(error.exception.reason, 'deadline_exceeded')
        client.lines.get.side_effect = None
        client.lines.get.return_value = None
        with patch.object(runtime.time, 'monotonic', return_value=10), self.assertRaises(runtime.Fault):
            client.receive_raw(10.75)

    def test_thread_start_failure_reaps_spawned_process_before_propagation(self):
        process = Mock(returncode=0)
        thread = Mock()
        thread.start.side_effect = RuntimeError('private-thread-start')
        with patch.object(runtime.subprocess, 'Popen', return_value=process), \
                patch.object(runtime.threading, 'Thread', return_value=thread), \
                patch.object(runtime.time, 'monotonic', return_value=10), \
                self.assertRaisesRegex(RuntimeError, '^private-thread-start$'):
            runtime.Client('adtr-auth-e2e-123456abcdef', 'observer', 20)
        process.wait.assert_called_once()
        process.stdout.close.assert_called_once()

    def test_bad_inputs_never_spawn_services(self):
        with patch.object(runtime.subprocess, 'run') as run, patch.object(runtime, 'Client') as client:
            result = runtime.run_experiment('invalid', {})
        self.assertFalse(result['success'])
        self.assertEqual(result['firstFailureStage'], 'inputs')
        self.assertNotIn('invalid', json.dumps(result))
        run.assert_not_called()
        client.assert_not_called()


class ExperimentLifecycleContracts(unittest.TestCase):
    def exercise(self, outcome='controlled_cross_time_combination_observed', setup_bad=False,
                 leftover_gate=0, constructor_bad=False, cleanup_bad=False, transition_bad=False,
                 wrong_mapping=False, wrong_body=False):
        env = {'ADTR_RUN_OBSERVER_TEMPORAL_TESTS': '1', 'GITHUB_ACTIONS': 'true',
               'GITHUB_SHA': 'a' * 40, 'ADTR_EVENT_HEAD_SHA': 'b' * 40,
               'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
               'ADTR_TEST_DATABASE_URL': 'postgres://postgres:private@127.0.0.1:1234/adtr_test?sslmode=disable'}
        calls, sql_calls, clients, marker_roles = [], [], {}, set()
        binding = {'database': runtime.DATABASE, 'databaseOID': 401, 'markerOID': 301,
                   'markerName': 'other_schema.marker' if wrong_mapping else runtime.MARKER,
                   'searchPath': '"$user", public'}

        def construct(container, role, deadline):
            if constructor_bad and role == 'b':
                raise RuntimeError('private-constructor')
            client = Mock(pid={'a': 101, 'b': 102, 'controller': 201, 'observer': 202, 'verifier': 203}[role])
            client.close.return_value = not (cleanup_bad and role == 'controller')
            clients[role] = client

            def query(sql, limit):
                sql_calls.append((role, sql, limit))
                if 'server_version_num' in sql:
                    return {'pid': client.pid, 'version': 170006, 'appMatches': True,
                            'readOnly': role in ('observer', 'verifier')}
                if sql.startswith('CREATE SCHEMA'):
                    if setup_bad:
                        raise RuntimeError('private-setup')
                    return True
                if "'markerName'" in sql:
                    return binding
                if "'body',p.prosrc" in sql:
                    return {'body': 'private-wrong-body' if wrong_body else runtime.GATE_BODY,
                            'volatile': True, 'parallelUnsafe': True, 'invoker': True, 'plpgsql': True}
                if "'aMarker'" in sql:
                    return state(tuple(int(actor in marker_roles) for actor in runtime.ROLES))
                if sql.startswith('BEGIN; SAVEPOINT'):
                    marker_roles.add(role)
                    return True
                if sql.startswith('BEGIN; SELECT to_json(count(*))'):
                    return 1
                if "locktype='advisory'" in sql:
                    return leftover_gate
                if 'pg_try_advisory_lock' in sql:
                    return {'a': True, 'b': True}
                raise AssertionError('unreviewed synthetic query')

            def query_raw(sql, limit):
                sql_calls.append((role, sql, limit))
                if sql.startswith('EXPLAIN'):
                    return json.dumps(raw_plan('ANALYZE' in sql, 'observer_temporal.row_gate(' in sql))
                return json.dumps(sample(blocker=False))

            client.query.side_effect = query
            client.query_raw.side_effect = query_raw
            return client

        def run(command, **kwargs):
            calls.append((command, kwargs))
            if command[0] == 'git':
                return Mock(stdout='a' * 40 + '\n')
            if 'psql' in command:
                return Mock(stdout='0\0')
            return Mock()

        def transition(*args):
            receipt = args[-2]
            receipt['gated'] = {'caseID': 'single_gated_select', 'outcome': outcome}
            receipt['outcome'] = outcome
            if transition_bad:
                raise runtime.Fault('deadline_exceeded')
            receipt['after'] = {'caseID': 'exact_after'}

        with patch.object(runtime, 'Client', side_effect=construct), \
                patch.object(runtime.subprocess, 'run', side_effect=run), \
                patch.object(runtime, 'transition', side_effect=transition) as controlled, \
                patch.object(runtime.time, 'monotonic', return_value=10):
            result = runtime.run_experiment('adtr-auth-e2e-123456abcdef', env)
        return result, calls, sql_calls, clients, controlled

    def test_complete_mocked_setup_static_controls_and_cleanup_are_bound(self):
        result, calls, queries, clients, transition = self.exercise()
        self.assertTrue(result['success'])
        self.assertIsNone(result['firstFailureStage'])
        self.assertEqual(result['cleanup'], {'clientsClosed': True, 'zeroOtherDatabaseBackends': True})
        self.assertEqual(len(result['plans']), 4)
        self.assertEqual(len(result['planAttempts']), 4)
        self.assertTrue(all(p['projected'] and p['failure'] is None for p in result['planAttempts']))
        self.assertFalse(result['planComparison']['sameExecutionAsGatedSelect'])
        self.assertTrue(result['fixtureBinding']['transactionLevelGate'])
        self.assertFalse(result['fixtureBinding']['rawObserverRowsContainOID'])
        self.assertEqual(calls[1][0][-1], runtime.DATABASE)
        self.assertEqual(calls[2][0], ['bin/adtr', '-mode=migrate'])
        self.assertIn('/adtr_observer_temporal?sslmode=disable', calls[2][1]['env']['ADTR_DATABASE_URL'])
        self.assertNotIn('private', json.dumps(result))
        transition.assert_called_once()
        for client in clients.values():
            client.close.assert_called_once_with(128)
        ddl_index = next(i for i, (_, sql, _) in enumerate(queries) if sql.startswith('CREATE SCHEMA'))
        marker_indexes = [i for i, (_, sql, _) in enumerate(queries) if sql.startswith('BEGIN; SAVEPOINT')]
        self.assertTrue(all(i > ddl_index for i in marker_indexes))
        self.assertTrue(runtime.GATE_BODY.endswith('END;'))

    def test_inconclusive_outcome_is_not_fixture_success(self):
        result, _, _, _, _ = self.exercise(outcome='inconclusive_later_read_not_established')
        self.assertFalse(result['success'])
        self.assertEqual(result['firstFailureStage'], 'single_transition')
        self.assertEqual(result['firstFailureClass'], 'inconclusive_later_read_not_established')
        self.assertIsNotNone(result['gated'])

    def test_first_transition_failure_and_evidence_survive_cleanup_failure(self):
        result, _, _, _, _ = self.exercise(transition_bad=True, cleanup_bad=True)
        self.assertFalse(result['success'])
        self.assertEqual(result['firstFailureStage'], 'single_transition')
        self.assertEqual(result['firstFailureClass'], 'deadline_exceeded')
        self.assertIsNotNone(result['gated'])
        self.assertFalse(result['cleanup']['clientsClosed'])
        self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])

    def test_setup_failure_never_opens_marker_or_transition(self):
        result, _, queries, clients, controlled = self.exercise(setup_bad=True)
        self.assertEqual(result['firstFailureStage'], 'fixture_setup')
        self.assertFalse(result['success'])
        self.assertFalse(any(sql.startswith('BEGIN; SAVEPOINT') for _, sql, _ in queries))
        controlled.assert_not_called()
        for client in clients.values():
            client.close.assert_called_once()
        self.assertNotIn('private-setup', json.dumps(result))

    def test_constructor_failure_is_not_clean_even_with_no_backends(self):
        result, _, _, clients, controlled = self.exercise(constructor_bad=True)
        self.assertEqual(result['firstFailureStage'], 'clients')
        self.assertFalse(result['cleanup']['clientsClosed'])
        self.assertTrue(result['cleanup']['zeroOtherDatabaseBackends'])
        self.assertEqual(set(clients), {'a'})
        clients['a'].close.assert_called_once()
        controlled.assert_not_called()

    def test_leftover_static_gate_cannot_start_controller_hold_or_transition(self):
        result, _, queries, _, controlled = self.exercise(leftover_gate=1)
        self.assertEqual(result['firstFailureStage'], 'plan_comparison')
        self.assertFalse(result['success'])
        self.assertFalse(any('pg_try_advisory_lock' in sql for _, sql, _ in queries))
        controlled.assert_not_called()

    def test_wrong_regclass_mapping_or_wrapper_body_stops_before_marker(self):
        for kwargs in ({'wrong_mapping': True}, {'wrong_body': True}):
            result, _, queries, _, controlled = self.exercise(**kwargs)
            self.assertEqual(result['firstFailureStage'], 'fixture_setup')
            self.assertFalse(result['success'])
            self.assertFalse(any(sql.startswith('BEGIN; SAVEPOINT') for _, sql, _ in queries))
            controlled.assert_not_called()



class SourceScopeContracts(unittest.TestCase):
    def test_hook_is_additive_ci_only_explicit_runtime_not_contract_discovery(self):
        source = Path('scripts/test_integration.py').read_text()
        stage1 = ('            subprocess.run(["python3", "scripts/test_observer_plan_runtime.py", "--container", name],\n'
                  '                           env=dict(env, ADTR_RUN_OBSERVER_PLAN_TESTS="1"), check=True, timeout=120)\n')
        stage2 = ('            subprocess.run(["python3", "scripts/test_observer_temporal_control_runtime.py", "--container", name],\n'
                  '                           env=dict(env, ADTR_RUN_OBSERVER_TEMPORAL_TESTS="1"), check=True, timeout=120)\n')
        self.assertEqual(source.count(stage1 + stage2), 1)
        tree = ast.parse(source)
        enclosing = [n for n in ast.walk(tree) if isinstance(n, ast.If) and
                     ast.unparse(n.test) == "fixture_psql and os.environ.get('GITHUB_ACTIONS') == 'true'"]
        self.assertEqual(len(enclosing), 1)
        self.assertEqual(len(enclosing[0].body), 2)
        self.assertTrue(Path(runtime.__file__).name.endswith('_runtime.py'))
        self.assertNotIn('DROP DATABASE', Path(runtime.__file__).read_text())
        self.assertNotIn('pg_notify', Path(runtime.__file__).read_text())
        self.assertNotIn('RAISE NOTICE', Path(runtime.__file__).read_text())
        self.assertNotIn('pg_sleep', Path(runtime.__file__).read_text())

    def test_original_observer_and_plan_fixture_are_imported_without_monkeypatch(self):
        source = Path(runtime.__file__).read_text()
        self.assertIn('RunningExportCancelGuard.observe_sql(', source)
        self.assertIn('plan_fixture.project_plan(', source)
        self.assertNotIn('plan_fixture.DATABASE =', source)
        self.assertIn('WORK_SECONDS = 100', source)
        self.assertIn('CLEANUP_SECONDS = 118', source)
        self.assertIn('TRANSITION_SECONDS = .75', source)
        self.assertIn('POLL_LIMIT = 16', source)
        self.assertIn('polls', source)


if __name__ == '__main__':
    unittest.main()
