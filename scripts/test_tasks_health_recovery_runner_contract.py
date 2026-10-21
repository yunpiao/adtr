"""Health actual-runner and exact legacy projection contracts; synthetic units only.

These tests execute extracted functions with owned process/filesystem mocks; no
fixture object is real browser evidence or permission to skip the CI scenario.
"""
import ast
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
import uuid
from unittest.mock import Mock, patch

from test_directory_v4_runner_contract import restore_health_runner, restore_v4_runner

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "scripts/test_auth_e2e.py"
OPERATIONS = ["seal_and_take", "worker_reap", "finish_after_wait", "api_reap", "owned_container_remove"]
FILES = ["manifest.json", "cleanup-receipt.json", "parent-failed-desktop.png",
         "parent-failed-mobile.png", "child-succeeded-desktop.png", "child-succeeded-mobile.png"]


def function(name, **overrides):
    scope = dict(Path=Path, hashlib=hashlib, json=json, os=os, shutil=shutil,
                 signal=signal, subprocess=subprocess, sys=sys, time=time, uuid=uuid)
    scope.update(overrides)
    tree = ast.parse(SOURCE.read_text())
    node = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == name)
    exec(compile(ast.Module(body=[node], type_ignores=[]), "<health-actual-runner>", "exec"), scope)
    return scope[name]


class HealthActualRunner(unittest.TestCase):
    def test_frozen_predecessor_and_outer_mutation_rejection(self):
        source = SOURCE.read_text()
        delta = json.loads((ROOT / 'scripts/fixtures/tasks_health_recovery_runner_delta.json').read_text())
        self.assertEqual(delta['baselineSHA256'], '1a90e44c24886a7815efb724dadbe694d07f49da7c89136e0f875ab101aeba2c')
        predecessor = restore_health_runner(source)
        self.assertEqual(hashlib.sha256(predecessor.encode()).hexdigest(), delta['baselineSHA256'])
        restore_v4_runner(source)  # All prior hashes remain authoritative.
        with self.assertRaises(ValueError):
            restore_health_runner(predecessor)
        for before, after in [('"--suite", choices=', '"--other", choices='),
                              ('timeout=30', 'timeout=31'),
                              ('health_timeout(660, health_run_deadline)', '660'),
                              ('start_new_session=True', 'start_new_session=False')]:
            self.assertIn(before, source)
            with self.subTest(before=before), self.assertRaises(ValueError):
                restore_health_runner(source.replace(before, after, 1))
        for change in delta['replacements']:
            self.assertEqual(source.count(change['new']), 1)
            for replacement in ('', change['new'] * 2, change['old'], change['new'] + '# drift\n'):
                with self.assertRaises(ValueError):
                    restore_health_runner(source.replace(change['new'], replacement, 1))
        with self.assertRaises(ValueError):
            restore_health_runner(source + '# unrelated legacy drift\n')

    def test_registration_has_independent_health_without_directory_membership(self):
        source = SOURCE.read_text()
        tree = ast.parse(source)
        choices = next(kw.value for n in ast.walk(tree) if isinstance(n, ast.Call)
            and isinstance(n.func, ast.Attribute) and n.func.attr == 'add_argument'
            and n.args and isinstance(n.args[0], ast.Constant) and n.args[0].value == '--suite'
            for kw in n.keywords if kw.arg == 'choices')
        self.assertEqual(ast.literal_eval(choices).count('tasks-health-recovery'), 1)
        for n in tree.body:
            if isinstance(n, (ast.Assign, ast.AugAssign)):
                self.assertNotIn('tasks-health-recovery', ast.get_source_segment(source, n))
        self.assertIn('health_build_process(command, env, health_timeout(660, health_run_deadline), capture=False)', source)
        self.assertIn('close_health_evidence(health_cleanup, health_build_record, env, health_run_deadline)', source)
        main = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'main')
        outer = next(n for n in main.body if isinstance(n, ast.Try))
        close = next(n for n in main.body if isinstance(n, ast.If)
            and any(isinstance(c, ast.Call) and isinstance(c.func, ast.Name) and c.func.id == 'close_health_evidence' for c in ast.walk(n)))
        self.assertGreater(close.lineno, outer.end_lineno)
        self.assertEqual(ast.unparse(outer.finalbody[0].test), "args.suite == 'tasks-health-recovery'")

    def test_real_api_then_one_delayed_worker_branch_and_fixed_capture(self):
        source = SOURCE.read_text()
        tree = ast.parse(source)
        branches = [n for n in ast.walk(tree) if isinstance(n, ast.If)
            and ast.unparse(n.test) == "args.suite == 'tasks-health-recovery'"
            and any(isinstance(c, ast.Lambda) for b in n.body for c in ast.walk(b))]
        self.assertEqual(len(branches), 1)
        branch = branches[0]
        launch = next(c for b in branch.body for c in ast.walk(b) if isinstance(c, ast.Lambda))
        self.assertEqual(ast.unparse(launch.body),
            "subprocess.Popen([binary, '-mode', 'worker'], env=worker_env, stdout=logs, stderr=logs)")
        body = '\n'.join(ast.get_source_segment(source, b) for b in branch.body)
        self.assertIn('worker_env=dict(worker_env)', body)
        self.assertIn('Path("bin/adtr").resolve(strict=True)', body)
        self.assertIn('v4_delayed_worker(', body)
        self.assertIn('env.update(v4_worker_controller.open())', body)
        self.assertNotIn('OwnedCIWorker', body)
        self.assertLess(source.index('diagnostics.phase("api-ready")'), source.index(body.splitlines()[0]))
        self.assertIsInstance(branch.orelse[0], ast.If)
        self.assertEqual(ast.unparse(branch.orelse[0].test), "args.suite == 'directory-v4-cancel'")

    def test_environment_rejects_inherited_application_and_old_control_inputs(self):
        source = {'PATH': 'safe', 'GITHUB_SHA': 'a' * 40,
                  'ADTR_DATABASE_URL': 'private', 'ADTR_AUTH_KEY': 'private',
                  'ADTR_E2E_HEALTH_RUN_NONCE': 'stale', 'ADTR_E2E_V4_WORKER_CONTROL_TOKEN': 'private',
                  'POSTGRES_PASSWORD': 'private', 'PGPASSWORD': 'private', 'pgport': 'private',
                  'ADTR_CI_POSTGRES_BUNDLE': 'owned', 'ADTR_E2E_HEALTH_JOB_DEADLINE': '2000',
                  'ADTR_EVENT_HEAD_SHA': 'a' * 40, 'ADTR_EVENT_BASE_SHA': 'b' * 40}
        clean = function('health_environment')(source)
        self.assertEqual(clean, {k: source[k] for k in ('PATH', 'GITHUB_SHA', 'ADTR_CI_POSTGRES_BUNDLE', 'ADTR_E2E_HEALTH_JOB_DEADLINE', 'ADTR_EVENT_HEAD_SHA', 'ADTR_EVENT_BASE_SHA')})
        self.assertEqual(source['ADTR_E2E_HEALTH_RUN_NONCE'], 'stale')
        browser = (ROOT / 'web/e2e/tasks-health-recovery.spec.ts').read_text()
        self.assertIn('!/^(ADTR_|POSTGRES_|PG)/i.test(x[0])', browser)
        self.assertIn('env: chromiumEnvironment()', browser)
        self.assertIn('trace: "off", video: "off", screenshot: "off"', browser)

    def test_absolute_deadline_and_reserve_never_extend(self):
        clock = Mock()
        clock.time.return_value = 1000
        clock.monotonic.return_value = 10
        deadline = function('health_deadline', time=clock)
        self.assertEqual(deadline({'ADTR_E2E_HEALTH_JOB_DEADLINE': '2500'}), 1510)
        self.assertEqual(deadline({'ADTR_E2E_HEALTH_JOB_DEADLINE': '2000'}), 1010)
        for raw in ('', 'bad', '1300', '9000'):
            with self.assertRaises(RuntimeError):
                deadline({'ADTR_E2E_HEALTH_JOB_DEADLINE': raw})
        timeout = function('health_timeout', time=clock)
        self.assertEqual(timeout(660, 500), 190)
        clock.monotonic.return_value = 50
        self.assertEqual(timeout(660, 500), 150)
        with self.assertRaises(RuntimeError):
            timeout(660, 350)
        self.assertEqual(timeout(60, 100, reserve=0), 50)
        self.assertEqual(timeout(30, None), 30)

    def test_image_provenance_uses_sanitized_environment_and_shared_deadline(self):
        env = {'PATH': 'safe', 'ADTR_CI_POSTGRES_BUNDLE': 'synthetic'}
        calls = []
        def image(original, environ, run):
            self.assertEqual(original, 'pinned')
            self.assertIs(environ, env)
            run(['git', 'rev-parse', 'HEAD'], timeout=15, check=True)
            run(['docker', 'image', 'inspect', 'pinned'], timeout=15)
            return ['--pull=never', 'pinned']
        runner = Mock()
        def timeout(seconds, deadline):
            calls.append((seconds, deadline))
            return min(seconds, 5)
        args = function('health_postgres_args', health_timeout=timeout,
            postgres_image_args=image, IMAGE='pinned', subprocess=Mock(run=runner))
        self.assertEqual(args(env, 500), ['--pull=never', 'pinned'])
        self.assertEqual(calls, [(1, 500), (15, 500), (15, 500), (1, 500)])
        self.assertTrue(all(c.kwargs['timeout'] == 5 and c.kwargs['env'] is env for c in runner.call_args_list))

    def test_owned_build_waits_and_rejects_surviving_children(self):
        process = Mock(pid=321, returncode=0)
        popen = Mock(return_value=process)
        system = Mock()
        system.killpg.side_effect = ProcessLookupError()
        runner = function('health_build_process', subprocess=Mock(Popen=popen, PIPE=subprocess.PIPE), os=system)
        self.assertEqual(runner(['go', 'build'], {'PATH': 'safe'}, 10), 0)
        self.assertTrue(popen.call_args.kwargs['start_new_session'])
        process.communicate.assert_called_once_with(timeout=10)
        system.killpg.assert_called_once_with(321, 0)
        system.killpg.side_effect = None
        with self.assertRaisesRegex(RuntimeError, '^health_owned_process_failed$'):
            runner(['npm', 'run', 'build'], {}, 10)
        self.assertIn(((321, signal.SIGTERM),), [(c.args,) for c in system.killpg.call_args_list])
        self.assertIn(((321, signal.SIGKILL),), [(c.args,) for c in system.killpg.call_args_list])

    def test_build_timeout_or_interrupt_attempts_all_group_cleanup(self):
        for failure in (subprocess.TimeoutExpired('synthetic', 1), KeyboardInterrupt()):
            process = Mock(pid=123)
            process.communicate.side_effect = [failure, OSError('private'), None]
            system = Mock()
            system.killpg.side_effect = [OSError('private'), None]
            runner = function('health_build_process', subprocess=Mock(Popen=Mock(return_value=process), PIPE=subprocess.PIPE), os=system)
            with self.assertRaisesRegex(RuntimeError, '^health_owned_process_failed$'):
                runner(['fixed'], {}, 1)
            self.assertEqual([c.args for c in system.killpg.call_args_list], [(123, signal.SIGTERM), (123, signal.SIGKILL)])
            self.assertEqual([c.kwargs['timeout'] for c in process.communicate.call_args_list], [1, 5, 5])

    def test_build_record_comes_from_actual_owned_steps_before_browser(self):
        source = {'commit': 'a' * 40, 'tree': 'b' * 40, 'inventorySHA256': 'c' * 64}
        prior = Path.cwd()
        with tempfile.TemporaryDirectory() as tmp:
            try:
                os.chdir(tmp)
                Path('bin').mkdir()
                Path('bin/adtr').write_bytes(b'synthetic binary')
                Path('web/dist').mkdir(parents=True)
                Path('web/dist/index.html').write_bytes(b'synthetic frontend')
                environment_file = Path(tmp) / 'github-env'
                environment_file.touch()
                env = dict(GITHUB_SHA='a' * 40, GITHUB_WORKFLOW_SHA='d' * 40,
                    GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='1', GITHUB_ENV=str(environment_file))
                snapshots = Mock(return_value=source)
                process = Mock(return_value=0)
                build = function('health_build', health_source_snapshot=snapshots,
                    health_build_process=process, health_file=function('health_file', health_timeout=lambda seconds, deadline: seconds),
                    health_timeout=lambda seconds, deadline: seconds)
                record = build(env, 1500)
                self.assertEqual(snapshots.call_count, 5)
                self.assertEqual([call.args[0] for call in process.call_args_list], [
                    ['go', 'build', '-buildvcs=false', '-trimpath', '-o', 'bin/adtr', './cmd/adtr'],
                    ['npm', 'run', 'build', '--prefix', 'web']])
                self.assertEqual([call.args[2] for call in process.call_args_list], [180, 120])
                self.assertEqual(record['source'], source)
                self.assertEqual(record['mode'], 'built_in_run')
                self.assertEqual(record['producer'], 'owned_runner')
                self.assertEqual([s['exitCode'] for s in record['steps']], [0, 0])
                self.assertTrue(all(s['sourceBeforeSHA256'] == s['sourceAfterSHA256'] == 'c' * 64 for s in record['steps']))
                self.assertEqual(record['outputs']['binary']['sha256'], hashlib.sha256(b'synthetic binary').hexdigest())
                self.assertEqual(environment_file.read_text(), 'ADTR_E2E_HEALTH_RUN_NONCE=' + record['identity']['nonce'] + '\n')
                # Freeze copied scalar metadata, not later browser/file values.
                Path('bin/adtr').write_bytes(b'later changed binary')
                self.assertEqual(record['outputs']['binary']['sha256'], hashlib.sha256(b'synthetic binary').hexdigest())
                for after in (dict(source, inventorySHA256='e' * 64), dict(source, commit='f' * 40)):
                    snapshots.side_effect = [source, source, after]
                    process.reset_mock()
                    with self.assertRaisesRegex(RuntimeError, '^health_build_failed_or_source_drift$'):
                        build(env, 1500)
                    self.assertEqual(process.call_count, 1)
                snapshots.side_effect = None
                process.return_value = 9
                with self.assertRaisesRegex(RuntimeError, '^health_build_failed_or_source_drift$'):
                    build(env, 1500)
            finally:
                os.chdir(prior)

    def test_source_producer_uses_full_git_tree_and_rechecks_bytes(self):
        source = SOURCE.read_text()
        body = ast.get_source_segment(source, next(n for n in ast.parse(source).body
            if isinstance(n, ast.FunctionDef) and n.name == 'health_source_snapshot'))
        self.assertIn('"status", "--porcelain", "--untracked-files=normal"', body)
        self.assertIn('"ls-tree", "-rz", "--full-tree", "HEAD", deadline=deadline', body)
        self.assertIn('(".github/workflows/", "cmd/", "internal/", "scripts/", "web/")', body)
        self.assertIn('{"Makefile", "go.mod", "go.sum"}', body)
        self.assertIn('hashlib.sha1(b"blob "', body)
        self.assertIn('sources.sort(key=lambda entry: entry["path"])', body)
        self.assertIn('health_digest(sources)', body)
        self.assertEqual(function('health_digest')([{'z': '非ASCII', 'a': 1}]),
            hashlib.sha256(json.dumps([{'z': '非ASCII', 'a': 1}], sort_keys=True, ensure_ascii=False,
                separators=(',', ':'), allow_nan=False).encode()).hexdigest())

    def cleanup_fixture(self, *, worker=True, server=True, seal=False, finish=True, remove=False):
        events = []
        w, s = Mock(), Mock()
        controller = Mock()
        controller.seal_and_take.side_effect = OSError('private') if seal else lambda: (events.append('seal_and_take'), Mock(process=w if worker else None, failures=()))[1]
        controller.finish_after_wait.side_effect = lambda **kw: (events.append('finish_after_wait'), finish)[1]
        controller.state.return_value = {'state': 'closed', 'launchCount': 1, 'failure': None}
        def process(p, record):
            events.append('worker_reap' if p is w else 'api_reap')
            return True, 0 if p is not None else None, False, ()
        removal = Mock(side_effect=OSError('private') if remove else lambda *a: events.append('owned_container_remove'))
        diagnostics = Mock(report={'collection_errors': [], 'processes': {'worker': {}, 'api': {}}})
        logs = Mock()
        cleanup = function('cleanup_health_run', cleanup_v4_process=process,
            remove_owned_container=removal, http_handler_panicked=lambda _: False)
        result = cleanup(controller, None, s if server else None, diagnostics, logs, True, 'owned', 'nonce')
        return result, events, controller, removal, logs

    def test_cleanup_receipt_facts_follow_actual_completed_operations(self):
        result, events, controller, removal, logs = self.cleanup_fixture()
        self.assertEqual(events, OPERATIONS)
        self.assertEqual(result['operations'], OPERATIONS)
        self.assertEqual(result['failures'], [])
        self.assertTrue(all(result[k] for k in ('ownershipTransferred', 'workerReaped', 'apiReaped', 'ownedContainerRemoved')))
        controller.finish_after_wait.assert_called_once_with(reaped=True, returncode=0, forced_kill=False)
        removal.assert_called_once_with('owned', 'nonce')
        logs.close.assert_called_once()

    def test_cleanup_missing_child_or_earlier_failure_cannot_claim_success(self):
        for kwargs, missing in [({'worker': False}, 'worker_reap'), ({'server': False}, 'api_reap'),
                                ({'seal': True}, 'seal_and_take'), ({'finish': False}, 'finish_after_wait'),
                                ({'remove': True}, 'owned_container_remove')]:
            with self.subTest(kwargs=kwargs):
                result, events, controller, removal, logs = self.cleanup_fixture(**kwargs)
                self.assertNotIn(missing, result['operations'])
                removal.assert_called_once()
                logs.close.assert_called_once()
                self.assertNotIn('private', repr(result))
                close = function('close_health_evidence', health_timeout=lambda seconds, deadline, reserve: seconds)
                with self.assertRaisesRegex(RuntimeError, '^health_cleanup_incomplete$'):
                    close(result, {}, {}, 100)

    def test_primary_failure_survives_cleanup_error_and_no_receipt(self):
        tree = ast.parse(SOURCE.read_text())
        main = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'main')
        branch = next(n for n in main.body if isinstance(n, ast.Try)).finalbody[0]
        code = compile(ast.Module(body=[branch], type_ignores=[]), '<health-actual-finally>', 'exec')
        for primary in (False, True):
            scope = dict(args=Mock(suite='tasks-health-recovery'), sys=sys,
                cleanup_health_run=Mock(return_value={'failures': ['worker_reap_failed']}),
                v4_worker_controller=None, worker=None, server=None, diagnostics=Mock(),
                logs=None, creation_attempted=False, name='owned', owner='nonce', diagnostic_dir=Path('/unused'))
            if primary:
                original = AssertionError('original')
                try:
                    try:
                        raise original
                    finally:
                        exec(code, scope)
                except AssertionError as observed:
                    self.assertIs(observed, original)
            else:
                with self.assertRaisesRegex(RuntimeError, '^health_owned_cleanup_failed$'):
                    exec(code, scope)

    def test_receipt_then_independent_validation_then_exact_copy_then_validation(self):
        cleanup = dict(launchCount=1, ownershipTransferred=True, workerReaped=True, apiReaped=True,
                       controllerFinalState='closed', ownedContainerRemoved=True, operations=OPERATIONS, failures=[])
        build = {'identity': {'nonce': 'synthetic'}, 'mode': 'synthetic-unit-record'}
        prior = Path.cwd()
        with tempfile.TemporaryDirectory() as tmp:
            try:
                os.chdir(tmp)
                evidence = Path('web/test-results/tasks-health-recovery/one-test')
                evidence.mkdir(parents=True)
                for name in FILES:
                    if name != 'cleanup-receipt.json':
                        (evidence / name).write_bytes(b'synthetic-unit-only')
                calls = []
                def validate(command, **kwargs):
                    directory = Path(command[command.index('--evidence-dir') + 1])
                    self.assertEqual(sorted(p.name for p in directory.iterdir()), sorted(FILES))
                    calls.append(directory)
                close = function('close_health_evidence', subprocess=Mock(run=validate),
                                 health_timeout=lambda seconds, deadline, reserve: seconds)
                close(cleanup, build, {'ADTR_E2E_HEALTH_RUN_NONCE': 'synthetic'}, 100)
                self.assertEqual(calls, [evidence, Path('web/playwright-report/tasks-health-recovery-validated')])
                receipt = json.loads((evidence / 'cleanup-receipt.json').read_text())
                self.assertEqual(receipt['buildSource'], build)
                self.assertEqual(receipt['manifestSHA256'], hashlib.sha256(b'synthetic-unit-only').hexdigest())
                with self.assertRaises(FileExistsError):
                    close(cleanup, build, {}, 100)
            finally:
                os.chdir(prior)


if __name__ == '__main__':
    unittest.main()
