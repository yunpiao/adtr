"""Current 16-way coverage plus exact inverses for historical source guards.

Offline contracts prove dispatch/selection, not PostgreSQL runtime acceptance.
Historical eight-shard/hash checks deliberately retain their original bytes.
"""
import contextlib
import copy
import hashlib
import io
import json
from pathlib import Path
import re
import unittest
from unittest.mock import patch

import ci_selection as selection
import ci_selection_summary as summary
import ci_workflow_contract as workflow
import run_integration as runner

DELTA = 'scripts/fixtures/auth_shards16_ci_delta.json'
BASE_WORKFLOW = '80c650cfd2184e2c9604d1a222843b64c642cb300ba63e2b1ffe9df20a899e61'
BASE_HELPER = '2b08139edc5c0613a62976ddfb6de8cb816c5610bc43c327db83f92f602a2542'
AUTH_HELPER_BLOCK = '9cf4a2cf8089e09c3dbaad59056aeb6a5082dbf1c72e76ec2e6cd9de810d9349'
BASE_SELECTOR = '8cfa0f286d50119ff26bce84b74c997a7fd0cc7efe49afe575caf95dc73e8523'
OLD_MATRIX = '        shard: [1, 2, 3, 4, 5, 6, 7, 8]\n'
NEW_MATRIX = '        shard: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]\n'
OLD_SELECTOR = b"            'fixed': fixed, 'auth': list(range(1, 9)) if auth else [], 'reasons': reasons}\n"
NEW_SELECTOR = b"            'fixed': fixed, 'auth': list(range(1, 17)) if auth else [], 'reasons': reasons}\n"


def require(condition):
    if not condition:
        raise ValueError('auth shard source differs from exact reviewed delta')


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def restore_auth_selector_source(raw):
    """One exact selector line only; retain the complete old source SHA guard."""
    require(type(raw) is bytes and raw.count(NEW_SELECTOR) == 1)
    restored = raw.replace(NEW_SELECTOR, OLD_SELECTOR, 1)
    require(digest(restored) == BASE_SELECTOR)
    return restored


def restore_auth_helper_source(text):
    """Remove only the frozen new function and its exact-position single hook."""
    start = 'def restore_auth_shards16(text):\n'
    end = 'def restore_access_role_bulk(text):\n'
    hook = '    text = restore_auth_shards16(text)\n'
    require(type(text) is str and text.count(start) == text.count(end) == 1)
    left, right = text.index(start), text.index(end)
    require(left < right and digest(text[left:right].encode()) == AUTH_HELPER_BLOCK)
    expected = end + '    """Reverse exactly the reviewed access-only validated two-file upload."""\n' + hook
    require(text.count(hook) == text.count(expected) == 1)
    restored = (text[:left] + text[right:]).replace(hook, '', 1)
    require(digest(restored.encode()) == BASE_HELPER)
    return restored


def actual_auth_ids(text):
    """Raw current matrix, never the intentionally eight-shard projection."""
    workflow.restore_auth_shards16(text)  # complete predecessor identity too
    require(text.count('  auth-integration:\n') == 1)
    auth = text.split('  auth-integration:\n', 1)[1].split('  browser:\n', 1)[0]
    matrices = re.findall(r'^        shard: \[([0-9, ]+)\]$', auth, re.M)
    require(len(matrices) == 1)
    ids = [int(value) for value in matrices[0].split(',')]
    require(ids == list(range(1, 17)))
    return ids


class AuthShards16Contract(unittest.TestCase):
    def setUp(self):
        self.raw = Path('.github/workflows/ci.yml').read_text()
        self.inventory = selection.inventory_checked(json.loads(Path('scripts/ci_inventory.json').read_text()))

    def test_raw_workflow_runner_plan_and_summary_agree_on_current_85_gates(self):
        ids = actual_auth_ids(self.raw)
        self.assertEqual(runner.AUTH_SHARDS, 16)
        self.assertEqual(ids, list(range(1, runner.AUTH_SHARDS + 1)))
        self.assertEqual(self.raw.count('\njobs:\n'), 1)
        jobs = re.findall(r'^  ([a-z][a-z-]+):$', self.raw.split('\njobs:\n', 1)[1], re.M)
        self.assertCountEqual(jobs, ['plan', 'prepare-postgres', 'contracts', 'auth-integration',
                                    'browser', 'native-no-store', 'native-no-store-fixed',
                                    'user-assets-fixed', 'verify'])
        self.assertEqual(len(self.inventory['browser']), 56)
        self.assertEqual(len(self.inventory['fixed']), 7)
        for event in ('push', 'merge_group', 'workflow_dispatch'):
            with self.subTest(event=event):
                plan = selection.make_plan(event, 'a' * 40, 'b' * 40, self.inventory,
                                           loader=lambda _: self.fail('full event read PR snapshots'))
                self.assertEqual(plan['auth'], ids)
                self.assertEqual(plan['browser'], self.inventory['browser'])
                self.assertEqual(plan['fixed'], self.inventory['fixed'])
                self.assertTrue(all(plan['groups'].values()))
                self.assertEqual(len(jobs) - 3 + len(plan['auth']) + len(plan['browser']) + len(plan['fixed']), 85)
                needs = {name: {'result': 'success'} for name in summary.GROUPS + ('plan',)}
                summary.verify(plan, needs, event, 'a' * 40, 'b' * 40, plan)
        # Old checks stay historical rather than being silently relabelled16.
        old = workflow.reviewed_workflow()
        auth = old.split('  auth-integration:\n', 1)[1].split('  browser:\n', 1)[0]
        self.assertIn(OLD_MATRIX, auth)
        self.assertNotIn(NEW_MATRIX, old)

    def test_current_auth_flags_parallelism_commands_and_budget_are_unchanged(self):
        auth = self.raw.split('  auth-integration:\n', 1)[1].split('  browser:\n', 1)[0]
        for token in ('    timeout-minutes: 20\n', '      max-parallel: 2\n',
                      '      fail-fast: false\n', '      contents: read\n',
                      'run: python3 scripts/test_integration.py --suite auth --shard ${{ matrix.shard }}'):
            self.assertEqual(auth.count(token), 1)
        self.assertEqual(runner.TEST_FLAGS, ['-race', '-count=1', '-tags=integration', '-timeout=10m'])
        self.assertEqual(digest(workflow.restore_auth_shards16(self.raw).encode()), BASE_WORKFLOW)
        for path, old, new, sha in (
            ('scripts/run_integration.py', b'AUTH_SHARDS = 8\n', b'AUTH_SHARDS = 16\n',
             '86842972248063ecd63f1e9c1c24b0dda4dde717a5dbdebd113e6c3c4a452c2d'),
            ('scripts/ci_selection_summary.py', b'list(range(1,9)) if groups', b'list(range(1,17)) if groups',
             '531b876f105dd13eb0c4ee4afaf3225ba4538cea8a0285b83ac2aa74907d3fc8'),
        ):
            raw = Path(path).read_bytes()
            self.assertEqual(raw.count(new), 1)
            self.assertEqual(digest(raw.replace(new, old, 1)), sha)
        restore_auth_selector_source(Path('scripts/ci_selection.py').read_bytes())
        restore_auth_helper_source(Path('scripts/ci_workflow_contract.py').read_text())

    def test_matrix_missing_duplicate_reordered_old_and_extra_ids_fail_closed(self):
        variants = ['', OLD_MATRIX, NEW_MATRIX * 2]
        for ids in (list(range(1, 16)), list(range(2, 17)), list(range(1, 18)),
                    list(range(1, 16)) + [15], list(range(16, 0, -1)),
                    [1, 2, 3, 4, 5, 6, 7, 8, 10, 9, 11, 12, 13, 14, 15, 16]):
            variants.append('        shard: [' + ', '.join(map(str, ids)) + ']\n')
        for line in variants:
            with self.subTest(line=line), self.assertRaises(ValueError):
                actual_auth_ids(self.raw.replace(NEW_MATRIX, line, 1))
        moved = self.raw.replace(NEW_MATRIX, '', 1) + NEW_MATRIX
        with self.assertRaises(ValueError):
            actual_auth_ids(moved)

    def test_workflow_inverse_rejects_every_matrix_line_edit_and_unrelated_semantic_change(self):
        variants = [self.raw + '\n', self.raw.replace('\n', '\r\n')]
        for before, after in [('timeout-minutes: 20', 'timeout-minutes: 21'),
                              ('max-parallel: 2', 'max-parallel: 3'),
                              ('fail-fast: false', 'fail-fast: true'),
                              ('contents: read', 'contents: write'),
                              ('run: make check', 'run: true'),
                              ('--suite auth --shard', '--suite non-auth --shard')]:
            self.assertIn(before, self.raw)
            variants.append(self.raw.replace(before, after, 1))
        for offset in range(len(NEW_MATRIX)):
            variants.append(self.raw.replace(NEW_MATRIX, NEW_MATRIX[:offset] + NEW_MATRIX[offset + 1:], 1))
        for bad in variants:
            with self.subTest(sha256=digest(bad.encode())), self.assertRaises(ValueError):
                workflow.restore_auth_shards16(bad)

    def test_bounded_delta_schema_cannot_authorize_an_alternate_matrix_or_hash(self):
        delta = json.loads(Path(DELTA).read_text())
        self.assertEqual(delta, {'baselineSHA256': BASE_WORKFLOW,
                                'replacements': [{'old': OLD_MATRIX, 'new': NEW_MATRIX}]})
        variants = [None, [], {}, dict(delta, extra=True), dict(delta, baselineSHA256='0' * 64),
                    dict(delta, replacements=[]), dict(delta, replacements=delta['replacements'] * 2)]
        for key in delta:
            bad = copy.deepcopy(delta)
            del bad[key]
            variants.append(bad)
        for key in ('old', 'new'):
            for value in ('', None, True, OLD_MATRIX + NEW_MATRIX):
                bad = copy.deepcopy(delta)
                bad['replacements'][0][key] = value
                variants.append(bad)
            bad = copy.deepcopy(delta)
            del bad['replacements'][0][key]
            variants.append(bad)
        bad = copy.deepcopy(delta)
        bad['replacements'][0]['extra'] = True
        variants.append(bad)
        raws = [json.dumps(value).encode() for value in variants]
        raws += [b'', b'{' + b' ' * 8192, b'[' * 1000 + b']' * 1000,
                 b'{"baselineSHA256":1,"baselineSHA256":2}', b'{', b'\xff']
        original = Path.read_bytes
        for raw in raws:
            def read(path):
                return raw if str(path) == DELTA else original(path)
            with self.subTest(rawSHA256=digest(raw)), patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                workflow.restore_auth_shards16(self.raw)

    def test_new_helper_inverse_rejects_each_line_hook_move_duplicate_and_old_byte_change(self):
        text = Path('scripts/ci_workflow_contract.py').read_text()
        left = text.index('def restore_auth_shards16(text):\n')
        right = text.index('def restore_access_role_bulk(text):\n')
        block = text[left:right]
        hook = '    text = restore_auth_shards16(text)\n'
        variants = [text.replace(block, '', 1), text.replace(block, block * 2, 1),
                    text.replace(block, '', 1) + block, text.replace(hook, '', 1),
                    text.replace(hook, hook * 2, 1), text.replace(hook, '', 1) + hook,
                    text + '\n', text.replace('\n', '\r\n'),
                    text.replace('access bulk workflow delta is invalid', 'different old behavior', 1)]
        lines = block.splitlines(keepends=True)
        for index in range(len(lines)):
            variants.append(text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1))
        for bad in variants:
            with self.subTest(sha256=digest(bad.encode())), self.assertRaises(ValueError):
                restore_auth_helper_source(bad)
        self.assertEqual(digest(restore_auth_helper_source(text).encode()), BASE_HELPER)

    def test_selector_inverse_rejects_old_partial_displaced_and_unrelated_source_edits(self):
        raw = Path('scripts/ci_selection.py').read_bytes()
        variants = [raw.replace(NEW_SELECTOR, OLD_SELECTOR, 1), raw.replace(NEW_SELECTOR, b'', 1),
                    raw.replace(NEW_SELECTOR, NEW_SELECTOR * 2, 1), raw.replace(NEW_SELECTOR, b'', 1) + NEW_SELECTOR,
                    raw.replace(b'range(1, 17)', b'range(1, 16)', 1), raw + b'\n',
                    raw.replace(b'contracts or auth or bool(selected)', b'contracts or bool(selected)', 1)]
        for bad in variants:
            with self.subTest(sha256=digest(bad)), self.assertRaises(ValueError):
                restore_auth_selector_source(bad)
        self.assertEqual(digest(restore_auth_selector_source(raw)), BASE_SELECTOR)

    def test_shared_sources_and_unknown_paths_conservatively_select_all_sixteen(self):
        for path in ('.github/workflows/ci.yml', 'scripts/run_integration.py', 'scripts/ci_selection.py',
                     'scripts/ci_selection_summary.py', 'scripts/ci_workflow_contract.py', DELTA,
                     'internal/auth/new_future_test.go', 'unclassified/future.file'):
            with self.subTest(path=path), patch.object(selection, 'dependency_graph', return_value=({}, set())), \
                 patch.object(selection.subprocess, 'run', side_effect=AssertionError('unexpected subprocess')):
                plan = selection.select([path], self.inventory, [{}, {}])
            self.assertEqual(plan['auth'], actual_auth_ids(self.raw))
            self.assertEqual(plan['browser'], self.inventory['browser'])
            self.assertEqual(plan['fixed'], self.inventory['fixed'])
            self.assertTrue(all(plan['groups'].values()))

    def test_summary_rejects_partial_duplicate_reordered_old_and_disabled_auth(self):
        plan = selection.make_plan('push', 'a' * 40, 'b' * 40, self.inventory)
        needs = {name: {'result': 'success'} for name in summary.GROUPS + ('plan',)}
        for ids in ([], list(range(1, 9)), list(range(1, 16)), list(range(2, 17)),
                    list(range(1, 17)) + [16], list(range(1, 18)), list(range(16, 0, -1))):
            bad = copy.deepcopy(plan)
            bad['auth'] = ids
            # Even a corrupt matching recomputation cannot bypass cardinality.
            for expected in (plan, bad):
                with self.subTest(ids=ids, recomputed=expected == bad), self.assertRaises(ValueError):
                    summary.verify(bad, needs, 'push', 'a' * 40, 'b' * 40, expected)
        bad = copy.deepcopy(plan)
        bad['auth'] = []
        bad['groups']['auth-integration'] = False
        with self.assertRaises(ValueError):
            summary.verify(bad, needs, 'push', 'a' * 40, 'b' * 40, bad)
        for state in ('skipped', 'cancelled', 'failure'):
            bad_needs = copy.deepcopy(needs)
            bad_needs['auth-integration']['result'] = state
            with self.subTest(state=state), self.assertRaises(ValueError):
                summary.verify(plan, bad_needs, 'push', 'a' * 40, 'b' * 40, plan)

    def test_valid_first_and_last_cli_shards_and_undersized_discovery_fail_closed(self):
        for shard in (1, 16):
            args = runner.parse_args(['--suite', 'auth', '--shard', str(shard)])
            self.assertEqual(args.shard, shard)
        for count in range(16):
            with self.subTest(count=count), self.assertRaises(ValueError):
                runner.partition_auth_tests('\n'.join(f'TestCase{index}' for index in range(count)))

    def test_generated_commands_cover_future_odd_names_once_without_regex_overmatch(self):
        for count in (16, 17, 19, 351, 352, 353, 1001):
            names = [f'TestCase{index}' for index in range(count - 2)] + ['ExamplePermission', 'FuzzPermission']
            output = '\n'.join(reversed(names)) + '\nBenchmarkOnly\nok \tpackage\t1s\n'
            selected = []
            for shard in range(1, 17):
                with patch.object(runner.subprocess, 'check_output', return_value=output), contextlib.redirect_stdout(io.StringIO()):
                    commands = runner.test_commands(runner.parse_args(['--suite', 'auth', '--shard', str(shard)]), {'GO': 'synthetic-go'})
                self.assertEqual(len(commands), 1)
                command = commands[0]
                self.assertEqual(command[:6], ['synthetic-go', 'test', *runner.TEST_FLAGS])
                pattern = command[command.index('-run') + 1]
                matches = [name for name in names if re.fullmatch(pattern, name)]
                self.assertTrue(matches)
                self.assertIsNone(re.fullmatch(pattern, matches[0] + 'Extra'))
                self.assertIsNone(re.fullmatch(pattern, 'Prefix' + matches[0]))
                selected.extend(matches)
            self.assertCountEqual(selected, names)
            self.assertEqual(len(selected), len(set(selected)))

    def test_all_mode_runs_non_auth_and_every_auth_child_after_failure(self):
        names = '\n'.join(f'TestCase{index}' for index in range(352))
        packages = runner.AUTH_PACKAGE + '\nexample/other'
        outcomes = []
        def run(command, **kwargs):
            outcomes.append(command)
            return type('Outcome', (), {'returncode': int(len(outcomes) == 4)})()
        with patch.object(runner.subprocess, 'check_output', side_effect=[packages, names]), \
             patch.object(runner.subprocess, 'run', side_effect=run), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(runner.run_tests(runner.parse_args([]), {'ADTR_TEST_DATABASE_URL': 'synthetic'}), 1)
        self.assertEqual(len(outcomes), 3 + 16)
        self.assertTrue(all(command[-1] == runner.AUTH_PACKAGE for command in outcomes[3:]))
        self.assertTrue(all(command.count('-timeout=10m') == command.count('-race') == command.count('-count=1') == 1 for command in outcomes))


if __name__ == '__main__':
    unittest.main()
