"""Current native phase budgets and exact, current-only historical projection.

Static source contracts do not prove phase-cap sufficiency or native acceptance.
"""
import copy
import hashlib
import json
from pathlib import Path
import re
import unittest
from unittest.mock import patch

import ci_workflow_contract as workflow

DELTA = 'scripts/fixtures/native_phase_budgets_ci_delta.json'
BASE_WORKFLOW = '0fd9599e994b86e28d72d2fbf28be4fffa14f7bbff171b3d47c96069e592b195'
BASE_HELPER = '8cfce8da1c1bcd81b2d81b8dde6e27803a87195d5e33ccb3be817e0eb2d30244'
NATIVE_HELPER_BLOCK = 'e79637901d13e38968ee4863c86ddce8ea88fe4b5ff4acc4d1344482c8130ecf'
BUDGETS = {'native-no-store': (41, [1, 2, 2, 20, 10, 2, 2]),
           'native-no-store-fixed': (51, [1, 2, 2, 20, 10, 10, 2, 2])}


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('native budget helper differs from exact reviewed addition')


def restore_native_budget_helper_source(text):
    """Remove only this frozen function and the single exact-position auth hook."""
    start = 'def restore_native_phase_budgets(text):\n'
    end = 'def restore_auth_shards16(text):\n'
    hook = '    text = restore_native_phase_budgets(text)\n'
    require(type(text) is str and text.count(start) == text.count(end) == 1)
    left, right = text.index(start), text.index(end)
    require(left < right and digest(text[left:right].encode()) == NATIVE_HELPER_BLOCK)
    expected = end + '    """Reverse only the reviewed 16-to-8 auth matrix line, not runtime coverage."""\n' + hook
    require(text.count(hook) == text.count(expected) == 1)
    restored = (text[:left] + text[right:]).replace(hook, '', 1)
    require(digest(restored.encode()) == BASE_HELPER)
    return restored


def native_block(text, job):
    require(text.count('  ' + job + ':\n') == 1)
    return re.split(r'(?=^  [a-z][a-z-]+:\n)', text[text.index('  ' + job + ':\n'):], flags=re.M)[1]


class NativePhaseBudgetsCI(unittest.TestCase):
    def setUp(self):
        self.raw = Path('.github/workflows/ci.yml').read_text()
        self.delta = json.loads(Path(DELTA).read_text())

    def reject_workflow(self, bad):
        # Exercise the new layer and every current full-workflow consumer, not
        # merely a direct helper detached from the historical chain.
        consumers = [workflow.restore_native_phase_budgets, workflow.restore_auth_shards16,
                     workflow.restore_access_role_bulk, workflow.restore_archive_target_observation,
                     workflow.restore_health_recovery, workflow.restore_messages_isolation,
                     workflow.restore_directory_v4, workflow.restore_selection]
        for consumer in consumers:
            with self.subTest(consumer=consumer.__name__, sha=digest(bad.encode())), self.assertRaises(ValueError):
                consumer(bad)

    def test_raw_current_seventeen_limits_and_explicit_resource_change(self):
        total = 0
        for job, (outer, budgets) in BUDGETS.items():
            block = native_block(self.raw, job)
            self.assertEqual(re.findall(r'^    timeout-minutes: (.+)$', block, re.M), [str(outer)])
            steps = re.split(r'(?=^      - )', block, flags=re.M)[1:]
            self.assertEqual(len(steps), len(budgets))
            self.assertEqual(block.count('timeout-minutes:'), len(budgets) + 1)
            for step, minutes in zip(steps, budgets):
                self.assertEqual(re.findall(r'^        timeout-minutes: (.+)$', step, re.M), [str(minutes)])
            self.assertEqual(outer - sum(budgets), 2)  # nominal capacity, not a post guarantee
            total += len(budgets) + 1
        self.assertEqual(total, 17)
        self.assertEqual([sum(v[1]) for v in BUDGETS.values()], [39, 49])
        inventory = json.loads(Path('scripts/ci_inventory.json').read_text())
        self.assertEqual((len(inventory['browser']), len(inventory['fixed'])), (56, 7))
        self.assertEqual(sum(row['name'] == 'tasks-health-recovery' for row in inventory['browser']), 1)
        old = 10 + 20 + 20 + 16 * 20 + 55 * 20 + 30 + 10 + 10 + 7 * 20 + 20
        self.assertEqual(old, 1680)
        self.assertEqual(old - 20 + 41 + 51, 1752)
        self.assertEqual(41 + 51 - 20, 72)
        self.assertEqual(digest(workflow.restore_native_phase_budgets(self.raw).encode()), BASE_WORKFLOW)
        self.assertEqual(digest(workflow.restore_auth_shards16(self.raw).encode()),
                         '80c650cfd2184e2c9604d1a222843b64c642cb300ba63e2b1ffe9df20a899e61')
        workflow.restore_selection(self.raw)

    def test_each_limit_change_remove_duplicate_type_expression_indent_move_and_decoy_rejected(self):
        for job in BUDGETS:
            block = native_block(self.raw, job)
            fields = list(re.finditer(r'^ +timeout-minutes: [0-9]+\n', block, re.M))
            for index, field in enumerate(fields):
                line = field.group(); indent = line[:len(line) - len(line.lstrip())]
                value = line.split(': ')[1].strip()
                variants = ['', line * 2, indent + 'timeout-minutes: 999\n',
                            indent + 'timeout-minutes: "' + value + '"\n',
                            indent + 'timeout-minutes: true\n', indent + 'timeout-minutes: null\n',
                            indent + 'timeout-minutes: ${{ 10 }}\n', ' ' + line,
                            indent + 'run: echo "timeout-minutes: ' + value + '"\n']
                for replacement in variants:
                    badblock = block[:field.start()] + replacement + block[field.end():]
                    with self.subTest(job=job, field=index, replacement=replacement):
                        self.reject_workflow(self.raw.replace(block, badblock, 1))
                removed = block[:field.start()] + block[field.end():]
                # Wrong step placement and movement outside the native job.
                first_step = removed.index('      - ')
                moved = removed[:first_step] + line + removed[first_step:]
                self.reject_workflow(self.raw.replace(block, moved, 1))
                self.reject_workflow(self.raw.replace(block, removed, 1) + line)

    def test_current_only_missing_duplicate_reordered_jobs_steps_and_partial_conversion_rejected(self):
        blocks = [native_block(self.raw, job) for job in BUDGETS]
        predecessor = workflow.restore_native_phase_budgets(self.raw)
        variants = [predecessor, self.raw.replace(blocks[0] + blocks[1], blocks[1] + blocks[0], 1)]
        for block, change in zip(blocks, self.delta['replacements']):
            variants += [self.raw.replace(block, '', 1), self.raw.replace(block, block * 2, 1),
                         self.raw.replace(block, change['old'], 1)]
            parts = re.split(r'(?=^      - )', block, flags=re.M)
            for index in range(1, len(parts)):
                variants.append(self.raw.replace(block, ''.join(parts[:index] + parts[index + 1:]), 1))
                variants.append(self.raw.replace(block, ''.join(parts[:index] + [parts[index]] + parts[index:]), 1))
            variants.append(self.raw.replace(block, parts[0] + parts[2] + parts[1] + ''.join(parts[3:]), 1))
        for bad in variants:
            self.reject_workflow(bad)

    def test_each_non_budget_native_line_remains_exact_through_all_consumers(self):
        # Includes all commands/flags/order, action SHAs, URLs/hash/version/size,
        # selectors/needs/env, permissions/cache, validator and upload conditions,
        # paths, retention and if-no-files-found. Even blank-line drift is refused.
        for job in BUDGETS:
            block = native_block(self.raw, job)
            lines = block.splitlines(keepends=True)
            for index, line in enumerate(lines):
                if 'timeout-minutes:' in line:
                    continue
                altered = ''.join(lines[:index] + [line.rstrip('\n') + ' # unauthorized\n'] + lines[index + 1:])
                with self.subTest(job=job, line=index):
                    self.reject_workflow(self.raw.replace(block, altered, 1))

    def test_other_jobs_health_deadline_matrix_permissions_selection_and_newline_cannot_normalize(self):
        variants = [self.raw + '\n', self.raw.replace('\n', '\r\n')]
        native_start = self.raw.index('  native-no-store:\n')
        for match in re.finditer(r'^    timeout-minutes: .+\n', self.raw, re.M):
            if native_start <= match.start() < self.raw.index('  user-assets-fixed:\n'):
                continue
            variants.append(self.raw[:match.start()] + '    timeout-minutes: 999\n' + self.raw[match.end():])
        for before, after in [('1620', '1621'), ('max-parallel: 2', 'max-parallel: 3'),
                              ('max-parallel: 8', 'max-parallel: 9'), ('fail-fast: false', 'fail-fast: true'),
                              ('contents: read', 'contents: write'), ('run: make check', 'run: true'),
                              ('shard: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]', 'shard: [1]'),
                              ('cancel-in-progress: true', 'cancel-in-progress: false'),
                              ('needs.plan.outputs', 'needs.other.outputs')]:
            self.assertIn(before, self.raw)
            variants.append(self.raw.replace(before, after, 1))
        deadline = next(line for line in self.raw.splitlines(keepends=True) if '1620' in line)
        variants.append(self.raw.replace(deadline, '', 1) + deadline)
        for bad in variants:
            self.reject_workflow(bad)

    def test_bounded_fixture_schema_wrong_blocks_order_types_and_authority_rejected(self):
        variants = [None, [], {}, dict(self.delta, extra=True), dict(self.delta, baselineSHA256='0' * 64),
                    dict(self.delta, replacements=[]), dict(self.delta, replacements=self.delta['replacements'][:1]),
                    dict(self.delta, replacements=list(reversed(self.delta['replacements']))),
                    dict(self.delta, replacements=self.delta['replacements'] * 2)]
        for key in self.delta:
            bad = copy.deepcopy(self.delta); del bad[key]; variants.append(bad)
        for index in range(2):
            for key in ('old', 'new'):
                for value in ('', None, True, 10, [], {}, self.delta['replacements'][1 - index][key]):
                    bad = copy.deepcopy(self.delta); bad['replacements'][index][key] = value; variants.append(bad)
                bad = copy.deepcopy(self.delta); del bad['replacements'][index][key]; variants.append(bad)
                bad = copy.deepcopy(self.delta); bad['replacements'][index][key] += '\n'; variants.append(bad)
            bad = copy.deepcopy(self.delta); bad['replacements'][index]['extra'] = True; variants.append(bad)
        raws = [json.dumps(bad).encode() for bad in variants]
        raws += [b'', b'{' + b' ' * 16384, b'[' * 1000 + b']' * 1000, b'{', b'\xff',
                 b'{"baselineSHA256":1,"baselineSHA256":2}',
                 json.dumps(self.delta).replace('"old":', '"old":"duplicate","old":', 1).encode()]
        self.assertLessEqual(Path(DELTA).stat().st_size, 16384)
        self.assertGreater(Path(DELTA).stat().st_size, 8192)
        original = Path.read_bytes
        for raw in raws:
            def read(path):
                return raw if str(path) == DELTA else original(path)
            with self.subTest(fixture=digest(raw)), patch.object(Path, 'read_bytes', read):
                self.reject_workflow(self.raw)

    def test_recomputed_fixture_cannot_conceal_unauthorized_native_or_third_job_change(self):
        original = Path.read_bytes
        for before, after in [('timeout-minutes: 41', 'timeout-minutes: 999'),
                              ('contents: read', 'contents: write'), ('run: make check', 'run: true')]:
            bad = self.raw.replace(before, after, 1)
            delta = copy.deepcopy(self.delta)
            for row in delta['replacements']:
                row['new'] = row['new'].replace(before, after, 1)
            restored = bad
            for row in delta['replacements']:
                restored = restored.replace(row['new'], row['old'], 1)
            delta['baselineSHA256'] = digest(restored.encode())
            encoded = json.dumps(delta).encode()
            def read(path):
                return encoded if str(path) == DELTA else original(path)
            with patch.object(Path, 'read_bytes', read):
                self.reject_workflow(bad)

    def test_frozen_helper_addition_hook_and_every_legacy_byte_remain_bound(self):
        text = Path('scripts/ci_workflow_contract.py').read_text()
        self.assertEqual(digest(restore_native_budget_helper_source(text).encode()), BASE_HELPER)
        left = text.index('def restore_native_phase_budgets(text):\n')
        right = text.index('def restore_auth_shards16(text):\n')
        block = text[left:right]; hook = '    text = restore_native_phase_budgets(text)\n'
        variants = [text.replace(block, '', 1), text.replace(block, block * 2, 1),
                    text.replace(block, '', 1) + block, text.replace(hook, '', 1),
                    text.replace(hook, hook * 2, 1), text.replace(hook, '', 1) + hook,
                    text + '\n', text.replace('\n', '\r\n'),
                    text.replace('access bulk workflow delta is invalid', 'different old behavior', 1)]
        for index, line in enumerate(block.splitlines(keepends=True)):
            lines = block.splitlines(keepends=True)
            variants.append(text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1))
            variants.append(text.replace(block, ''.join(lines[:index] + [line.rstrip('\n') + ' # changed\n'] + lines[index + 1:]), 1))
        for bad in variants:
            with self.subTest(sha=digest(bad.encode())), self.assertRaises(ValueError):
                restore_native_budget_helper_source(bad)

    def test_auth_test_migration_preserves_every_previous_hash_and_negative_test_byte(self):
        raw = Path('scripts/test_auth_shards16_contract.py').read_text()
        added_import = 'from test_native_phase_budgets_ci_contract import restore_native_budget_helper_source\n'
        hook = '    text = restore_native_budget_helper_source(text)\n'
        self.assertEqual(raw.count(added_import), 1); self.assertEqual(raw.count(hook), 1)
        restored = raw.replace(added_import, '', 1).replace(hook, '', 1)
        self.assertEqual(digest(restored.encode()), 'b672ecb9c70c82d50cb7593e4550a129dab432f9a287c0a089fcd15f46f9c26f')

    def test_independent_directory_guard_rejects_every_limit_and_outer999_step10_decoy(self):
        import test_directory_v4_controls_ci_contract as directory
        check = directory.DirectoryV4ControlsCI('test_both_native_families_direct_exports_and_all_legacy_assertions')
        original = Path.read_text
        variants = []
        for job in BUDGETS:
            block = native_block(self.raw, job)
            for match in re.finditer(r'^ +timeout-minutes: [0-9]+\n', block, re.M):
                badblock = block[:match.start()] + match.group().replace(': ', ': 9', 1) + block[match.end():]
                variants.append(self.raw.replace(block, badblock, 1))
            badblock = re.sub(r'^    timeout-minutes: [0-9]+$', '    timeout-minutes: 999', block, flags=re.M)
            self.assertIn('timeout-minutes: 10', badblock)  # the old pseudo-guard accepted this
            variants.append(self.raw.replace(block, badblock, 1))
        check.test_both_native_families_direct_exports_and_all_legacy_assertions()
        for bad in variants:
            def read(path, *args, **kwargs):
                return bad if str(path) == '.github/workflows/ci.yml' else original(path, *args, **kwargs)
            with patch.object(Path, 'read_text', read), self.assertRaises(AssertionError):
                check.test_both_native_families_direct_exports_and_all_legacy_assertions()
            self.reject_workflow(bad)


if __name__ == '__main__':
    unittest.main()
