"""Offline wiring contracts; actual helper-body mocks run in Vitest CI."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class QueueCycleContracts(unittest.TestCase):
    def test_each_running_suite_passes_the_existing_pending_sample(self):
        for kind in ('user', 'group', 'computer'):
            spec = (ROOT / f'web/e2e/{kind}-asset-export-v3-running-cancel.spec.ts').read_text()
            self.assertEqual(spec.count('resumeOwnedExportWorker(context, pause, taskID, pendingDatabase.databaseClock)'), 1)
            helper = (ROOT / f'web/e2e/{kind}-asset-export-v3-terminal-helpers.ts').read_text()
            self.assertIn('pendingDatabaseClock?: number', helper)
            self.assertIn('Number(queue.success) > Number(pause.queueSuccessBefore)\n      && (pendingDatabaseClock === undefined || Number(queue.success) > pendingDatabaseClock)', helper)
            self.assertIn('expect(Number(first.success)).toBeGreaterThan(Number(pause.queueSuccessBefore));', helper)
            self.assertIn('expect(Number(first.success)).toBeGreaterThan(pendingDatabaseClock);', helper)
            self.assertIn('expect(Number(queue.success)).toBeGreaterThan(Number(first.success));', helper)
            for budget in ('5000', '10_000', '15_000', '20_000'):
                self.assertIn('timeout: ' + budget, helper)

    def test_mock_contract_executes_actual_helper_and_all_kinds(self):
        source = (ROOT / 'web/src/OwnedWorkerQueueCycles.test.ts').read_text()
        for marker in ('ts.transpileModule(body', 'return resumeOwnedExportWorker;',
                       '["user", "group", "computer"]', '[15, 20]', '[21, 20]',
                       '[21, 21, 22, 21]', '[11, 11, 12, 12]', '[5000, 10000, 15000, 20000]'):
            self.assertIn(marker, source)
        self.assertNotIn('from "@playwright', source)
