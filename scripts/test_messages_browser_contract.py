"""Keep the real message browser gate additive and bound to its owned runtime."""
import ast
from pathlib import Path
import re
import unittest


class MessagesBrowserContract(unittest.TestCase):
    def test_additive_gate_and_verify_aggregation(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        browser = workflow.split('  browser:\n', 1)[1].split('  native-no-store:\n', 1)[0]
        names = re.findall(r'^          - name: (.+)$', browser, re.M)
        self.assertEqual(len(names), 35)
        self.assertEqual(len(set(names)), 35)
        self.assertEqual(names.count('messages'), 1)
        self.assertIn('command: python3 scripts/test_auth_e2e.py --suite messages\n', browser)
        # 35 browser + 4 auth shards + 7 fixed assets + 5 scalar jobs = 51.
        jobs = re.findall(r'^  ([a-z][a-z-]+):$', workflow.split('jobs:\n', 1)[1], re.M)
        self.assertEqual(set(jobs), {'prepare-postgres', 'contracts', 'auth-integration', 'browser', 'native-no-store', 'native-no-store-fixed', 'user-assets-fixed', 'verify'})
        self.assertEqual(len(names) + 4 + 7 + len(jobs) - 3, 51)
        self.assertIn('BROWSER_RESULT: ${{ needs.browser.result }}', workflow)
        self.assertIn('test "$BROWSER_RESULT" = success', workflow)
        self.assertIn('path: web/test-results/**/messages-soc-*', browser)
        self.assertNotIn('continue-on-error', workflow)

    def test_runner_owns_real_worker_and_read_only_provenance(self):
        runner = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(runner)
        choices = [kw.value for node in ast.walk(tree) if isinstance(node, ast.Call)
                   for kw in node.keywords if kw.arg == 'choices']
        self.assertTrue(any('messages' in ast.literal_eval(value) for value in choices))
        worker_conditions = [node for node in ast.walk(tree) if isinstance(node, ast.If)
                             and 'worker = subprocess.Popen' in ast.get_source_segment(runner, node)]
        self.assertTrue(any('"messages"' in ast.get_source_segment(runner, node.test) for node in worker_conditions))
        self.assertIn('if args.suite == "messages":', runner)
        source = Path('web/e2e/messages.spec.ts').read_text()
        for fragment in ['projectedSources(container, taskIDs)', 'JOIN adtr.task_outbox',
                         'JOIN adtr.task_events', 'JOIN adtr.tasks',
                         'originalRecipient: true', '"/api/tasks/submit"',
                         '"/api/messages/query"', '"/api/messages/locate"',
                         '1440', '900', '390', '844', 'messages-soc-evidence.json']:
            self.assertIn(fragment, source)
        for forbidden in ['.fulfill(', 'INSERT INTO adtr.messages',
                          'INSERT INTO adtr.message_reads', 'UPDATE adtr.messages',
                          'page.waitForTimeout(']:
            self.assertNotIn(forbidden, source)

    def test_read_recovery_uses_real_commit_and_original_intent(self):
        source = Path('web/e2e/messages.spec.ts').read_text()
        for fragment in ['route.fetch({ maxRetries: 0 })', 'route.abort("failed")',
                         'persistedReadProof(container,', 'adtr.message_read_operations',
                         'JOIN adtr.message_reads', 'sameFirstReadAt',
                         '查询原操作结果', '重放原操作', '已确认单条已读',
                         'expect(replayedResponse.request().postDataJSON()).toEqual(firstIntent)',
                         'expect(await recoveredResponse.json()).toEqual(committedReceipt)',
                         'expect(await replayConfirmed.json()).toEqual(committedReceipt)',
                         'expect(await response.json()).toEqual(snapshot)',
                         'expect(newCSRF).not.toBe(oldCSRF)',
                         'expect(privateReceipt.status()).toBe(404)',
                         'expect(privateSnapshot.status()).toBe(404)',
                         'expect(privateReplay.status()).toBe(404)',
                         'messages-soc-read-${state}-${viewport.width}-${fullPage ? "full" : "viewport"}.png',
                         'web/src/message-read-intent.ts', 'web/src/message-read.tsx']:
            self.assertIn(fragment, source)
        self.assertEqual(len(re.findall(r'await page\.route\(\s*"\*\*/api/messages/read"', source)), 2)
        self.assertEqual(source.count('route.fetch('), 1)
        self.assertNotIn('route.fulfill', source)
        self.assertNotIn('maxRetries: 1', source)


if __name__ == '__main__':
    unittest.main()
