"""Guard duplicate-free draft/stacked PR and merge validation event coverage."""
import pathlib
import unittest


class CIEventContract(unittest.TestCase):
    def test_expected_events_and_all_verification_stages(self):
        text = pathlib.Path('.github/workflows/ci.yml').read_text()
        self.assertIn('  pull_request:\n', text)
        self.assertIn('  merge_group:\n    types: [checks_requested]\n', text)
        self.assertIn('  push:\n    branches: [main]\n', text)
        self.assertNotIn("'feat/**'", text)
        self.assertNotIn("'docs/**'", text)
        self.assertNotIn('pull_request_target', text)
        self.assertNotIn('github.event.pull_request.draft', text)
        for command in ['make check', 'scripts/test_integration.py', 'scripts/test_lifecycle.py',
                        'scripts/test_auth_e2e.py', 'scripts/test_auth_e2e.py --expired',
                        'scripts/test_auth_e2e.py --suite access', 'scripts/test_auth_e2e.py --suite resource']:
            self.assertIn(command, text)
        self.assertIn('permissions: {}', text)
        self.assertIn('contents: read', text)
        self.assertIn('persist-credentials: false', text)


if __name__ == '__main__':
    unittest.main()
