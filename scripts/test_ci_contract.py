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
                        'scripts/test_auth_e2e.py --suite access', 'scripts/test_auth_e2e.py --suite resource', 'scripts/test_auth_e2e.py --suite tasks', 'scripts/test_auth_e2e.py --suite audit', 'scripts/test_auth_e2e.py --suite system', 'scripts/test_auth_e2e.py --suite maintenance', 'scripts/test_auth_e2e.py --suite domains', 'scripts/test_auth_e2e.py --suite operations', 'scripts/test_auth_e2e.py --suite profile', 'scripts/test_auth_e2e.py --suite credential-use', 'scripts/test_auth_e2e.py --suite account-references', 'scripts/test_auth_e2e.py --suite operational-logs', 'scripts/test_auth_e2e.py --suite session-invalidation', 'scripts/test_auth_e2e.py --suite account-reference-barrier', 'scripts/test_auth_e2e.py --suite directory', 'scripts/test_auth_e2e.py --suite directory-controls', 'scripts/test_auth_e2e.py --suite directory-readers', 'scripts/test_auth_e2e.py --suite directory-v2', 'scripts/test_auth_e2e.py --suite directory-v2-controls', 'scripts/test_auth_e2e.py --suite directory-v2-readers', 'scripts/test_auth_e2e.py --suite user-assets-v2', 'scripts/test_auth_e2e.py --suite user-assets-v2-readers']:
            self.assertIn(command, text)
        for suite in ['user-assets-v2', 'user-assets-v2-readers']:
            self.assertEqual(text.count(f'command: python3 scripts/test_auth_e2e.py --suite {suite}\n'), 1)
        self.assertIn('path: web/test-results/**/user-assets-v2-*', text)
        self.assertIn('scripts/test_integration.py --suite non-auth', text)
        self.assertIn('scripts/test_integration.py --suite auth --shard ${{ matrix.shard }}', text)
        self.assertIn('shard: [1, 2, 3, 4]', text)
        self.assertIn('needs: [contracts, auth-integration, browser]', text)
        self.assertIn('if: ${{ always() }}', text)
        self.assertIn('fail-fast: false', text)
        self.assertIn('max-parallel: 2', text)
        self.assertIn('AUTH_INTEGRATION_RESULT: ${{ needs.auth-integration.result }}', text)
        self.assertIn('test "$CONTRACT_RESULT" = success && test "$AUTH_INTEGRATION_RESULT" = success && test "$BROWSER_RESULT" = success', text)
        self.assertNotIn('continue-on-error', text)
        self.assertIn('permissions: {}', text)
        self.assertIn('contents: read', text)
        self.assertIn('persist-credentials: false', text)


if __name__ == '__main__':
    unittest.main()
