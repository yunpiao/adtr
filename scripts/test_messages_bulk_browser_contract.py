from ci_workflow_contract import reviewed_workflow
"""Additive bulk acceptance preserves the original HTTP-health gate and artifacts."""
import ast
from pathlib import Path
import unittest

from test_auth_e2e import browser_command


class BulkBrowserContract(unittest.TestCase):
    def test_same_gate_fresh_database_and_distinct_artifact_output(self):
        runner = Path('scripts/test_auth_e2e.py').read_text()
        ast.parse(runner)
        self.assertEqual(browser_command('messages'), ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/messages.spec.ts'])
        self.assertEqual(browser_command('messages-bulk')[-3:], ['e2e/messages-bulk.spec.ts', '--output', 'test-results/messages-bulk'])
        self.assertIn('if args.suite == "messages":\n        # Additive sequential acceptance', runner)
        self.assertIn('[sys.executable, __file__, "--suite", "messages-bulk"]', runner)
        self.assertLess(runner.index('remove_owned_container(name, owner)'), runner.index('[sys.executable, __file__, "--suite", "messages-bulk"]'))
        self.assertEqual(runner.count('[sys.executable, __file__, "--suite", "messages-bulk"]'), 1)
        self.assertIn('process.send_signal(signal.SIGCONT)', runner)
        self.assertIn('process.poll() is None', runner)
        self.assertIn('ADTR_E2E_HARNESS_PID', runner)
        self.assertIn('code != 0', runner)
        workflow = reviewed_workflow()
        self.assertNotIn('name: messages-bulk', workflow)
        self.assertIn('command: python3 scripts/test_auth_e2e.py --suite messages', workflow)
        self.assertIn('workers: 1', Path('web/playwright.config.ts').read_text())

    def test_internal_producer_has_production_authority_and_no_message_seeds(self):
        fixture = Path('internal/testmessagefixture/main.go').read_text()
        for required in ['//go:build integration', 'owned-isolated-health', 'cfg.Database != "adtr_e2e"',
                         'cfg.Host != "127.0.0.1"', 'auth.NewTaskAuthorizer()', 'tasks.ProductionRegistry()',
                         'engine.SubmitTx(', 'inbox.BulkOnce(', 'store.SchemaVersion', 'start, end := 0, 61']:
            self.assertIn(required, fixture)
        for forbidden in ['INSERT INTO adtr.messages', 'INSERT INTO adtr.message_reads', 'UPDATE adtr.message_bulk', 'func(context.Context, pgx.Tx']:
            self.assertNotIn(forbidden, fixture)
        self.assertLess(fixture.index('engine.CheckSchemaTx(ctx, tx)'), fixture.index('engine.SubmitTx('))
        self.assertIn('return fixtureStageError("schema_gate")', fixture)
        self.assertNotIn('RunOne(', fixture)  # health executes in the actual owned worker process

    def test_browser_proves_persistence_and_original_intent(self):
        source = Path('web/e2e/messages-bulk.spec.ts').read_text()
        for required in ['route.fetch({ maxRetries: 0 })', 'route.abort("failed")', 'selection: "all"',
                         '"seed-late"', '"bulk-once"', 'SIGSTOP', 'SIGCONT', '/proc/${worker}/status',
                         'JOIN adtr.task_outbox', 'JOIN adtr.task_events', 'default_transaction_read_only=on',
                         'adtr.message_bulk_receipts', 'r.first_read_at=m.first_read_at',
                         'target: 61', 'processed: 50', 'remaining: 11', 'alreadyRead: 2',
                         'expect(await r.json()).toEqual(snapshot)', 'authorization_blocked',
                         'expect(afterEpoch).toBeGreaterThan(beforeEpoch)', 'width: 390', 'width: 1440',
                         'sourceSHA256', 'artifactsSHA256', 'messages-soc-bulk-evidence.json',
                         'NOT HTTP']:
            self.assertIn(required, source)
        for forbidden in ['route.fulfill', 'page.waitForTimeout(', 'INSERT INTO adtr.messages', 'UPDATE adtr.message_reads']:
            self.assertNotIn(forbidden, source)
        self.assertIn('(authenticator.lastCounter + 1) * 30_000 + 1100', source)
        self.assertIn('expect(starts).toHaveLength(3)', source)


if __name__ == '__main__':
    unittest.main()
