from ci_workflow_contract import reviewed_workflow
"""Freeze the reviewed #139 canary scope, not future legitimate CI evolution.

New suites, uploads or other intended workflow edits require explicit review and
an updated baseline/hash. Never bypass this guard when integrating another PR.
"""
import hashlib
import json
from replay_ci_parallelism import replay, schedule
from pathlib import Path
import unittest
from test_group_members_v4_empty_runner_contract import restore_workflow


# Base plus #151's dictionary4 backend suite/evidence upload and the reviewed
# four-to-eight auth shard split and fixed Go image identity/evidence steps.
# Auth max-parallel remains two. Add only group-members-v4 plus evidence.
# Historical baseline retains all 55 prior jobs; its primary suite totals 56.
# Mechanically strip the independently guarded empty suite (intended total 57).
# This hash is an explicit workflow-baseline change requiring independent review;
# every old job, condition and safety check remains present.
BASELINE_SHA = 'bd302da441323891cec156d951b06f4f4369181d'
BASELINE_WORKFLOW_SHA256 = '4c9be470d365e9aa9abf1781bff30f699de4f0311351fec79a3d8aed086df232'


def preserves_reviewed_workflow(text):
    """Strip the exact empty suite, then accept only historical browser 4→8."""
    try:
        text = restore_workflow(text)
    except ValueError:
        return False
    before, marker, after = text.partition('  browser:\n')
    browser, end, remainder = after.partition('  native-no-store:\n')
    if not marker or not end or browser.count('      max-parallel: 8\n') != 1:
        return False
    restored = before + marker + browser.replace('      max-parallel: 8\n', '      max-parallel: 4\n') + end + remainder
    return hashlib.sha256(restored.encode()).hexdigest() == BASELINE_WORKFLOW_SHA256


class CIParallelismContract(unittest.TestCase):
    def test_only_browser_budget_differs_from_explicit_56_job_baseline(self):
        self.assertTrue(preserves_reviewed_workflow(reviewed_workflow()))

    def test_other_budget_and_safety_changes_fail_closed(self):
        text = reviewed_workflow()
        mutations = [
            ('max-parallel: 8', 'max-parallel: 4'),
            ('max-parallel: 8', 'max-parallel: 9'),
            ('max-parallel: 2', 'max-parallel: 3'),
            ('shard: [1, 2, 3, 4, 5, 6, 7, 8]', 'shard: [1, 2, 3, 4]'),
            ('fail-fast: false', 'fail-fast: true'),
            ('timeout-minutes: 20', 'timeout-minutes: 25'),
            ('runs-on: ubuntu-24.04', 'runs-on: ubuntu-latest'),
            ('retention-days: 1', 'retention-days: 2'),
            ('contents: read', 'contents: write'),
            ('if: ${{ always() }}', 'if: ${{ success() }}'),
            ('run: make check', 'run: true'),
            ('needs: [prepare-postgres]', 'needs: []'),
            ('cache: true', 'cache: false'),
            ('command: python3 scripts/test_auth_e2e.py --suite access', 'command: true'),
        ]
        for old, new in mutations:
            with self.subTest(change=(old, new)):
                self.assertIn(old, text)
                self.assertFalse(preserves_reviewed_workflow(text.replace(old, new, 1)))
        self.assertFalse(preserves_reviewed_workflow(text.replace('      max-parallel: 8\n', '', 1)))

    def test_offline_replay_preserves_measured_dag_and_order(self):
        samples = json.loads(Path('docs/evidence/ci-eight-slot-model-input.json').read_text())['runs']
        self.assertEqual([s['number'] for s in samples], [175, 177])
        for sample, four, eight, count in zip(samples, [3422, 3732], [1844, 2054], [46, 50]):
            self.assertEqual(replay(sample, 4)['modelElapsedSeconds'], four)
            self.assertEqual(replay(sample, 8)['modelElapsedSeconds'], eight)
            self.assertEqual(replay(sample, 8)['jobs'], count)
        self.assertEqual(replay(samples[1], 8)['fixedSpanSeconds'], 1974)
        self.assertEqual(schedule([3, 7, 2], 2), 7)
        with self.assertRaises(ValueError):
            schedule([1], 0)
        with self.assertRaises(ValueError):
            replay(dict(samples[1], jobs=samples[1]['jobs'][:-1]), 8)
        for altered in [
            dict(samples[1], fixedOrder=samples[1]['fixedOrder'] * 2),
            dict(samples[1], jobs=[dict(samples[1]['jobs'][0], conclusion='failure')] + samples[1]['jobs'][1:]),
            dict(samples[1], jobs=[dict(samples[1]['jobs'][0], completed_at='2000-01-01T00:00:00Z')] + samples[1]['jobs'][1:]),
        ]:
            with self.assertRaises(ValueError):
                replay(altered, 8)

    def test_explicit_single_run_peak_budget(self):
        # prepare/native startup <=3; verify follows every dependency.
        self.assertEqual(8 + 2 + 2 + 1 + 2, 15)
        self.assertEqual(4 + 2 + 2 + 1 + 2, 11)


if __name__ == '__main__':
    unittest.main()
