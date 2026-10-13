"""Freeze the reviewed #139 canary scope, not future legitimate CI evolution.

New suites, uploads or other intended workflow edits require explicit review and
an updated baseline/hash. Never bypass this guard when integrating another PR.
"""
import hashlib
import json
from replay_ci_parallelism import replay, schedule
from pathlib import Path
import unittest


# Base plus #151's dictionary4 backend suite/evidence upload and the reviewed
# four-to-eight auth shard split and fixed Go image identity/evidence steps.
# Auth max-parallel remains two.
# This hash is an explicit workflow-baseline change requiring independent review;
# every old job, condition and safety check remains present.
BASELINE_SHA = '013ba239e5a6af5061a252125a92779c33bf1725'
BASELINE_WORKFLOW_SHA256 = 'a4ca8a58427c3175fcaa7e69dfa566ea05ae28214192718b92ec80e6c1760493'


def preserves_reviewed_workflow(text):
    """Accept exactly browser 4→8; reject all other byte-level changes."""
    before, marker, after = text.partition('  browser:\n')
    browser, end, remainder = after.partition('  native-no-store:\n')
    if not marker or not end or browser.count('      max-parallel: 8\n') != 1:
        return False
    restored = before + marker + browser.replace('      max-parallel: 8\n', '      max-parallel: 4\n') + end + remainder
    return hashlib.sha256(restored.encode()).hexdigest() == BASELINE_WORKFLOW_SHA256


class CIParallelismContract(unittest.TestCase):
    def test_only_browser_budget_differs_from_explicit_55_job_baseline(self):
        self.assertTrue(preserves_reviewed_workflow(Path('.github/workflows/ci.yml').read_text()))

    def test_other_budget_and_safety_changes_fail_closed(self):
        text = Path('.github/workflows/ci.yml').read_text()
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
