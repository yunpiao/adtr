from ci_workflow_contract import reviewed_workflow
"""Accept only the reviewed three-suite permutation, then retain all old hashes.

Matrix order is a scheduling experiment, not an execution-priority guarantee.
No job, command, budget, dependency, safety check or evidence step is removed.
"""
from collections import Counter
import hashlib
import itertools
from pathlib import Path
import re
import unittest


# Full workflow from alias candidate f994327 / tree 3104d2a4, before reordering.
# A legitimate future workflow edit needs independent review of this boundary.
BASELINE_WORKFLOW_SHA256 = 'f58165cff96414c50051b3914f24c0a3bf0da47ec84fc983191975d4c2f68449'
MATRIX_START = '      matrix:\n        include:\n'
ENTRY = re.compile(r'          - name: [a-z0-9-]+\n            command: [^\n]+\n')
CRITICAL = tuple(
    f'          - name: {kind}-asset-export-v3-running-cancel\n'
    f'            command: python3 scripts/test_auth_e2e.py --suite {kind}-asset-export-v3-running-cancel\n'
    for kind in ('group', 'computer', 'user')
)


def split_matrix(text):
    if text.count(MATRIX_START) != 1:
        raise ValueError('expected one reviewed browser include matrix')
    prefix, body = text.split(MATRIX_START)
    matrix, separator, suffix = body.partition('    steps:\n')
    entries = ENTRY.findall(matrix)
    if not separator or len(entries) != 51 or ''.join(entries) != matrix:
        raise ValueError('expected exactly 51 unaltered two-line matrix entries')
    return prefix + MATRIX_START, entries, separator + suffix


def restore_critical_priority(text):
    """Reverse exactly positions 1..3 to 13..15, with full baseline integrity."""
    prefix, entries, suffix = split_matrix(text)
    if tuple(entries[:3]) != CRITICAL or any(entries.count(e) != 1 for e in CRITICAL):
        raise ValueError('critical entries must occur once, in exact first-three order')
    ordinary = entries[3:]
    restored = prefix + ''.join(ordinary[:12] + list(CRITICAL) + ordinary[12:]) + suffix
    if hashlib.sha256(restored.encode()).hexdigest() != BASELINE_WORKFLOW_SHA256:
        raise ValueError('reordered workflow differs from the reviewed baseline')
    return restored


class CICriticalPriorityContract(unittest.TestCase):
    def setUp(self):
        self.text = reviewed_workflow()
        self.prefix, self.entries, self.suffix = split_matrix(self.text)

    def assert_rejected(self, text):
        with self.assertRaises(ValueError):
            restore_critical_priority(text)

    def test_exact_reversible_permutation_and_unchanged_outer_bytes(self):
        restored = restore_critical_priority(self.text)
        prefix, before, suffix = split_matrix(restored)
        self.assertEqual((prefix, suffix), (self.prefix, self.suffix))
        self.assertEqual(Counter(before), Counter(self.entries))
        self.assertEqual(before[:12] + before[15:], self.entries[3:])
        self.assertEqual(tuple(before[12:15]), CRITICAL)
        self.assertEqual(prefix + ''.join(before[12:15] + before[:12] + before[15:]) + suffix, self.text)
        self.assertEqual(len(self.entries) + 8 + 7 + 5, 71)

    def test_old_order_and_every_other_critical_order_are_rejected(self):
        self.assert_rejected(restore_critical_priority(self.text))
        for order in itertools.permutations(CRITICAL):
            if order != CRITICAL:
                self.assert_rejected(self.prefix + ''.join(list(order) + self.entries[3:]) + self.suffix)

    def test_every_non_initial_critical_position_is_rejected(self):
        ordinary = self.entries[3:]
        for position in range(1, len(ordinary) + 1):
            with self.subTest(position=position):
                moved = ordinary[:position] + list(CRITICAL) + ordinary[position:]
                self.assert_rejected(self.prefix + ''.join(moved) + self.suffix)

    def test_every_entry_deletion_duplication_or_command_change_is_rejected(self):
        for entry in self.entries:
            with self.subTest(entry=entry.splitlines()[0]):
                self.assert_rejected(self.text.replace(entry, '', 1))
                self.assert_rejected(self.text.replace(entry, entry + entry, 1))
                self.assert_rejected(self.text.replace(entry, entry.replace('python3 ', 'python3 -O ', 1), 1))

    def test_every_adjacent_ordinary_swap_is_rejected(self):
        for index in range(3, 50):
            changed = self.entries.copy()
            changed[index], changed[index + 1] = changed[index + 1], changed[index]
            self.assert_rejected(self.prefix + ''.join(changed) + self.suffix)

    def test_budget_dependency_safety_evidence_and_format_changes_are_rejected(self):
        changes = [
            ('max-parallel: 8', 'max-parallel: 9'),
            ('max-parallel: 2', 'max-parallel: 3'),
            ('needs: [prepare-postgres]', 'needs: []'),
            ('timeout-minutes: 20', 'timeout-minutes: 25'),
            ('fail-fast: false', 'fail-fast: true'),
            ('cancel-in-progress: true', 'cancel-in-progress: false'),
            ('contents: read', 'contents: write'),
            ('persist-credentials: false', 'persist-credentials: true'),
            ('run: make check', 'run: true'),
            ('if-no-files-found: error', 'if-no-files-found: warn'),
            ('retention-days: 14', 'retention-days: 1'),
            ('    steps:\n', '    steps: \n'),
            ('          - name:', '         - name:'),
        ]
        for old, new in changes:
            with self.subTest(old=old):
                self.assertIn(old, self.text)
                self.assert_rejected(self.text.replace(old, new, 1))
        self.assert_rejected(self.text + '\n')
        self.assert_rejected(self.text + MATRIX_START)


if __name__ == '__main__':
    unittest.main()
