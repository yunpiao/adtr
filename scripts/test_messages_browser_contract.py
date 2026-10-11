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
        self.assertEqual(len(names), 25)
        self.assertEqual(len(set(names)), 25)
        self.assertEqual(names.count('messages'), 1)
        self.assertIn('command: python3 scripts/test_auth_e2e.py --suite messages\n', browser)
        # 25 browser + 4 auth shards + 2 fixed user-assets + 5 scalar jobs = 36.
        jobs = re.findall(r'^  ([a-z][a-z-]+):$', workflow.split('jobs:\n', 1)[1], re.M)
        self.assertEqual(set(jobs), {'prepare-postgres', 'contracts', 'auth-integration', 'browser', 'native-no-store', 'native-no-store-fixed', 'user-assets-fixed', 'verify'})
        self.assertEqual(len(names) + 4 + 2 + len(jobs) - 3, 36)
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
        for forbidden in ['.route(', '.fulfill(', 'INSERT INTO adtr.messages',
                          "request.post('/api/messages/read'", 'page.waitForTimeout(']:
            self.assertNotIn(forbidden, source)


if __name__ == '__main__':
    unittest.main()
