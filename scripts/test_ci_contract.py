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
        self.assertIn('needs: [prepare-postgres, contracts, auth-integration, browser, native-no-store, native-no-store-fixed, user-assets-fixed]', text)
        self.assertIn('if: ${{ always() }}', text)
        self.assertIn('fail-fast: false', text)
        self.assertIn('max-parallel: 2', text)
        self.assertIn('AUTH_INTEGRATION_RESULT: ${{ needs.auth-integration.result }}', text)
        self.assertIn('test "$CONTRACT_RESULT" = success && test "$AUTH_INTEGRATION_RESULT" = success && test "$BROWSER_RESULT" = success', text)
        self.assertIn('  native-no-store:\n', text)
        self.assertIn('NATIVE_STREAM_RESULT: ${{ needs.native-no-store.result }}', text)
        self.assertIn('&& test "$NATIVE_STREAM_RESULT" = success', text)
        self.assertEqual(text.count('run: xvfb-run -a npm run test:e2e --prefix web -- e2e/native-no-store-completion.spec.ts --headed\n'), 2)
        self.assertIn('path: web/test-results/**/native-no-store-completion.json', text)
        baseline = text.split('  native-no-store:\n', 1)[1].split('  native-no-store-fixed:\n', 1)[0]
        fixed = text.split('  native-no-store-fixed:\n', 1)[1].split('  user-assets-fixed:\n', 1)[0]
        assets_fixed = text.split('  user-assets-fixed:\n', 1)[1].split('  verify:\n', 1)[0]
        browser = text.split('  browser:\n', 1)[1].split('  native-no-store:\n', 1)[0]
        self.assertIn('npx playwright install --with-deps chromium', baseline)
        self.assertNotIn('ADTR_E2E_CHROMIUM_PATH', baseline)
        self.assertIn('NATIVE_STREAM_FIXED_RESULT: ${{ needs.native-no-store-fixed.result }}', text)
        self.assertIn('&& test "$NATIVE_STREAM_FIXED_RESULT" = success', text)
        self.assertIn('npx playwright install-deps chromium', fixed)
        self.assertIn('https://storage.googleapis.com/chrome-for-testing-public/157.0.8092.0/linux64/chrome-linux64.zip', fixed)
        self.assertIn('28d4f185c9047a91fe79ee8d31e5e0b5b858fc107129b3df361fdd095dbb2ace', fixed)
        self.assertIn('sha256sum --check --strict', fixed)
        self.assertIn('test "$(stat -c %s "$archive")" -eq 201751736', fixed)
        self.assertLess(fixed.index('sha256sum --check --strict'), fixed.index('unzip -q'))
        self.assertIn("test \"$version\" = 'Google Chrome for Testing 157.0.8092.0'", fixed)
        self.assertIn('ADTR_E2E_CHROMIUM_PATH=%s', fixed)
        self.assertIn('source_tag=2b9f0645d8f651a36f74683f22a3746379f13bc6', fixed)
        self.assertIn('engine_fix=62473ad0f747e245e514c93e741bb8e168a2a207', fixed)
        self.assertIn('${{ runner.temp }}/adtr-native-fixed/provenance.txt', fixed)
        self.assertIn('ADTR_E2E_NATIVE_STREAM_MODE: stable-body', baseline)
        self.assertIn("ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'user-assets-v2' || matrix.name == 'user-assets-v2-readers') && 'stable-body' || 'native-finish' }}", browser)
        self.assertNotIn('ADTR_E2E_NATIVE_STREAM_MODE: stable-body', browser)
        self.assertIn('name: Require complete application reads and record native outcomes', baseline)
        self.assertNotIn('name: Require native no-store stream completion', baseline)
        self.assertIn('ADTR_E2E_NATIVE_STREAM_MODE: native-finish', fixed)
        self.assertIn('ADTR_E2E_NATIVE_STREAM_MODE: native-finish', assets_fixed)
        self.assertNotIn('ADTR_E2E_CHROMIUM_PATH', browser)
        self.assertIn('USER_ASSETS_FIXED_RESULT: ${{ needs.user-assets-fixed.result }}', text)
        self.assertIn('&& test "$USER_ASSETS_FIXED_RESULT" = success', text)
        self.assertIn('suite: [user-assets-v2, user-assets-v2-readers]', assets_fixed)
        self.assertIn('run: python3 scripts/test_auth_e2e.py --suite "${{ matrix.suite }}"', assets_fixed)
        self.assertIn('run: go mod verify && make build web-build', assets_fixed)
        self.assertIn('fail-fast: false', assets_fixed)
        self.assertIn('max-parallel: 2', assets_fixed)
        self.assertIn('timeout-minutes: 20', assets_fixed)
        # Both fixed jobs use exactly the same official archive verification.
        verification = lambda job: job.split('      - name: Verify official fixed-build diagnostic browser\n', 1)[1].split('      - name:', 1)[0]
        self.assertEqual(verification(fixed), verification(assets_fixed))
        self.assertNotIn('continue-on-error', text)
        self.assertIn('permissions: {}', text)
        self.assertIn('contents: read', text)
        self.assertIn('persist-credentials: false', text)

    def test_native_revocation_artifact_keeps_actual_structured_outcome(self):
        reader = pathlib.Path('web/e2e/user-assets-v2-readers.spec.ts').read_text()
        self.assertIn('const heldDetailOutcome = await held.release();', reader)
        self.assertIn('          heldDetailOutcome,', reader)
        self.assertNotIn('heldDetailOutcome: "delivered"', reader)

    def test_native_reader_json_is_durable_and_matches_existing_upload_globs(self):
        reader = pathlib.Path('web/e2e/user-assets-v2-readers.spec.ts').read_text()
        workflow = pathlib.Path('.github/workflows/ci.yml').read_text()
        self.assertIn('import { writeFile } from "node:fs/promises";', reader)
        for name, variable in [
            ('user-assets-v2-native-transport-outcomes', 'nativeTransportEvidencePath'),
            ('user-assets-v2-revocation-native-evidence', 'revocationEvidencePath'),
        ]:
            self.assertRegex(reader, rf'const {variable} = testInfo.outputPath\(\s*"{name}\.json",?\s*\);')
            self.assertRegex(reader, rf'await writeFile\(\s*{variable},\s*JSON.stringify\(\{{')
            attachment = f'await testInfo.attach("{name}", {{\n        path: {variable},\n        contentType: "application/json",\n      }});'
            self.assertIn(attachment, reader)
            self.assertLess(reader.index(f'await writeFile(\n        {variable},'), reader.index(attachment))
            self.assertTrue(pathlib.PurePosixPath(f'web/test-results/reader/{name}.json').match('web/test-results/**/user-assets-v2-*'))
        browser = workflow.split('  browser:\n', 1)[1].split('  native-no-store:\n', 1)[0]
        fixed = workflow.split('  user-assets-fixed:\n', 1)[1].split('  verify:\n', 1)[0]
        self.assertIn('path: web/test-results/**/user-assets-v2-*\n', browser)
        self.assertIn('path: |\n            web/test-results/**/user-assets-v2-*\n            ${{ runner.temp }}/adtr-native-fixed/provenance.txt\n', fixed)


if __name__ == '__main__':
    unittest.main()
