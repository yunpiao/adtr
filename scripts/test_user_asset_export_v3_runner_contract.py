"""Export suite delta pins all 59 original jobs; static checks are not browser evidence."""
import ast
import hashlib
from pathlib import Path
import re
import unittest
from unittest.mock import patch
import user_asset_export_v3_ci as gate

BASE_WORKFLOW_SHA256 = '67dfbefd8aff88fe1089e969a2d60a99234f3023cd402c963829d2f9d021b4a7'
BASE_RUNNER_SHA256 = '32c82e2d6c19e3d2aec727f275e01277f04e6a1d4573945ed5ff530874591686'
RUNNER_CHANGES = [('',
  'from user_asset_export_v3_ci import configure as configure_user_export, capture as capture_user_export, '
  'validate as validate_user_export\n'),
 ('DIRECTORY_V3_SUITES = {"directory-v3-backend", "user-assets-v3", "directory-v3", "directory-v3-controls", '
  '"directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES\n',
  'DIRECTORY_V3_SUITES = {"user-asset-export-v3", "directory-v3-backend", "user-assets-v3", "directory-v3", '
  '"directory-v3-controls", "directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES\n'),
 ('    parser.add_argument("--suite", choices=["auth", "access", "resource", "tasks", "messages", '
  '"messages-bulk", "audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", '
  '"account-references", "operational-logs", "session-invalidation", "account-reference-barrier", '
  '"directory", "directory-controls", "directory-readers", "directory-v2", "directory-v2-controls", '
  '"directory-v2-readers", "directory-v3-backend", "directory-v4-backend", "group-members-v4", '
  '"group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "user-assets-v3", '
  '"computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", "group-assets-v3-readers", '
  '"directory-v3", "directory-v3-controls", "directory-v3-readers", "user-assets-v2", '
  '"user-assets-v2-readers"], default="auth")\n',
  '    parser.add_argument("--suite", choices=["user-asset-export-v3", "auth", "access", "resource", '
  '"tasks", "messages", "messages-bulk", "audit", "system", "maintenance", "domains", "operations", '
  '"profile", "credential-use", "account-references", "operational-logs", "session-invalidation", '
  '"account-reference-barrier", "directory", "directory-controls", "directory-readers", "directory-v2", '
  '"directory-v2-controls", "directory-v2-readers", "directory-v3-backend", "directory-v4-backend", '
  '"group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", '
  '"user-assets-v3", "computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", '
  '"group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", '
  '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'),
 ('',
  '    if args.suite == "user-asset-export-v3":\n'
  '        configure_user_export(env)\n'
  '        capture_user_export()\n'),
 ('        subprocess.run(command, env=env, check=True, timeout=660 if args.suite == "account-references" '
  'else 930 if args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | {"user-assets-v3"} | '
  'COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", '
  '"directory-v2", "directory-v3-backend", "directory-v4-backend", "group-members-v4", '
  '"group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "directory-v3"} else '
  '600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", "operations", '
  '"credential-use", "account-references", "operational-logs", "account-reference-barrier"} else 480 if '
  'args.suite == "system" else 420)\n',
  '        subprocess.run(command, env=env, check=True, timeout=930 if args.suite == "user-asset-export-v3" '
  'else 660 if args.suite == "account-references" else 930 if args.suite in EMPTY_DIRECTORY_SUITES | '
  'USER_ASSETS_V2_SUITES | {"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES else 660 '
  'if args.suite in {"directory", "directory-v2", "directory-v3-backend", "directory-v4-backend", '
  '"group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", '
  '"directory-v3"} else 600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", '
  '"operations", "credential-use", "account-references", "operational-logs", "account-reference-barrier"} '
  'else 480 if args.suite == "system" else 420)\n'
  '        if args.suite == "user-asset-export-v3":\n'
  '            validate_user_export()\n')]
WORKFLOW_CHANGES = [("      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'user-assets-v2' || matrix.name == "
  "'user-assets-v2-readers' || matrix.name == 'user-assets-v3' || matrix.name == 'computer-assets-v3' || "
  "matrix.name == 'computer-assets-v3-readers' || matrix.name == 'group-assets-v3' || matrix.name == "
  "'group-assets-v3-readers') && 'stable-body' || 'native-finish' }}\n",
  "      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'user-asset-export-v3' || matrix.name == "
  "'user-assets-v2' || matrix.name == 'user-assets-v2-readers' || matrix.name == 'user-assets-v3' || "
  "matrix.name == 'computer-assets-v3' || matrix.name == 'computer-assets-v3-readers' || matrix.name == "
  "'group-assets-v3' || matrix.name == 'group-assets-v3-readers') && 'stable-body' || 'native-finish' }}\n"),
 ('',
  '          - name: user-asset-export-v3\n'
  '            command: python3 scripts/test_auth_e2e.py --suite user-asset-export-v3\n'),
 ('',
  '      - name: Validate user asset export dictionary3 browser evidence\n'
  '        id: user-export-evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3' }}\n"
  '        run: python3 scripts/user_asset_export_v3_ci.py validate\n'
  '      - name: Retain validated user asset export dictionary3 browser evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3' && steps.user-export-evidence.outcome "
  "== 'success' }}\n"
  '        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
  '        with:\n'
  '          name: user-asset-export-v3-${{ github.run_id }}-${{ github.run_attempt }}\n'
  '          path: |\n'
  '            web/playwright-report/user-export-validated/user-asset-export-v3-provenance.json\n'
  '            web/playwright-report/user-export-validated/user-asset-export-v3-evidence.json\n'
  '            web/playwright-report/user-export-validated/user-assets-v3-*.xlsx\n'
  '            web/playwright-report/user-export-validated/allMatched-ready.png\n'
  '            web/playwright-report/user-export-validated/empty-ready.png\n'
  '          # No trace, videos, logs, storage state, credentials or raw failures.\n'
  '          # Only independently validated synthetic JSON, XLSX and safe PNGs.\n'
  '          if-no-files-found: error\n'
  '          retention-days: 14\n')]

def restore(text, changes):
    for old, new in changes:
        if text.count(new) != 1:
            raise ValueError('export delta absent, duplicated or modified')
        text = text.replace(new, old)
    return text

def restore_workflow(text):
    return restore(text, WORKFLOW_CHANGES)

def restore_runner(text):
    return restore(text, RUNNER_CHANGES)

class ExportRunnerContract(unittest.TestCase):
    def test_prior_59_jobs_byte_exact(self):
        self.assertEqual(hashlib.sha256(restore_workflow(Path('.github/workflows/ci.yml').read_text()).encode()).hexdigest(), BASE_WORKFLOW_SHA256)
        self.assertEqual(hashlib.sha256(restore_runner(Path('scripts/test_auth_e2e.py').read_text()).encode()).hexdigest(), BASE_RUNNER_SHA256)

    def test_mutated_additions_rejected(self):
        for filename, changes in [('.github/workflows/ci.yml', WORKFLOW_CHANGES), ('scripts/test_auth_e2e.py', RUNNER_CHANGES)]:
            source = Path(filename).read_text()
            for old, new in changes:
                with self.assertRaises(ValueError):
                    restore(source + new, changes)
                with self.assertRaises(ValueError):
                    restore(source.replace(new, '!' + new[1:]), changes)

    def test_suite_60_preserves_concurrency_limits(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', workflow, re.M)
        self.assertEqual(len(names), 40)
        self.assertEqual(names.count('user-asset-export-v3'), 1)
        self.assertEqual(len(names) + 8 + 7 + 5, 60)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', workflow), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', workflow)
        self.assertNotIn('include-hidden-files', ''.join(new for old, new in WORKFLOW_CHANGES))

    def test_default_fixture_and_no_rate_limit_bypass(self):
        import test_auth_e2e as runner
        suite = 'user-asset-export-v3'
        self.assertIn(suite, runner.DIRECTORY_V3_SUITES)
        for modes in [runner.DIRECTORY_V4_SUITES, runner.DIRECTORY_V2_SUITES, runner.COMPUTER_ASSETS_V3_SUITES, runner.GROUP_ASSETS_V3_SUITES, runner.EMPTY_DIRECTORY_SUITES, runner.EMPTY_TRANSITION_SUITES, runner.REAL_TAB_LIFECYCLE_SUITES, runner.USER_ASSETS_V2_SUITES]:
            self.assertNotIn(suite, modes)
        self.assertEqual(runner.browser_command(suite), ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/user-asset-export-v3.spec.ts'])
        env = {}
        gate.configure(env)
        self.assertEqual(env, {'ADTR_E2E_USER_ASSET_EXPORT_V3': 'true'})
        for mode in gate.MIXED_MODES:
            with self.assertRaises(RuntimeError):
                gate.configure({'ADTR_E2E_LDAP_DIRECTORY_' + mode: 'true'})
        with self.assertRaises(RuntimeError):
            gate.configure({'ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR': '/fixture'})
        for key in ['ADTR_DIRECTORY_READ_V2_ENABLED', 'ADTR_DIRECTORY_READ_V4_ENABLED']:
            with self.assertRaises(RuntimeError):
                gate.configure({key: 'true'})

    def test_capture_precedes_database_and_validation_follows_browser(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        self.assertLess(source.index('capture_user_export()'), source.index('creation_attempted = True'))
        self.assertLess(source.index('subprocess.run(command,'), source.index('validate_user_export()'))
        self.assertIn('timeout=930 if args.suite == "user-asset-export-v3"', source)
        for changes in [RUNNER_CHANGES, WORKFLOW_CHANGES]:
            delta = ''.join(new for old, new in changes)
            self.assertNotIn('RATE_LIMIT', delta)
            self.assertNotIn('MFA_BYPASS', delta)

    def test_only_validated_artifacts_are_uploaded(self):
        block = next(new for old, new in WORKFLOW_CHANGES if 'id: user-export-evidence' in new)
        self.assertIn("steps.user-export-evidence.outcome == 'success'", block)
        self.assertIn('if-no-files-found: error', block)
        self.assertNotIn('always()', block)
        self.assertEqual(block.count('web/playwright-report/user-export-validated/'), 5)
        self.assertIn('allMatched-ready.png', block)
        self.assertIn('empty-ready.png', block)

    def test_capture_and_validation_failure_propagate(self):
        import subprocess
        import tempfile
        with tempfile.TemporaryDirectory() as tmp, patch.object(gate, 'PROVENANCE', Path(tmp) / 'provenance.json'), patch.object(gate.subprocess, 'run') as run:
            for error in [subprocess.CalledProcessError(1, 'validator'), subprocess.TimeoutExpired('validator', 30)]:
                run.side_effect = error
                with self.assertRaises(type(error)):
                    gate.capture()
                self.assertFalse(gate.PROVENANCE.exists())

    def test_validation_stages_only_referenced_files_and_is_repeatable(self):
        import json
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            previous = Path.cwd()
            try:
                os.chdir(tmp)
                root = Path('web/test-results/real-test')
                root.mkdir(parents=True)
                cases = []
                for name in ['allMatched', 'empty']:
                    case = {'download': {'filename': f'user-assets-v3-{name}.xlsx'}, 'screenshot': {'filename': f'{name}-ready.png'}}
                    cases.append(case)
                    for item in case.values():
                        (root / item['filename']).write_bytes(b'synthetic')
                evidence = root / 'user-asset-export-v3-evidence.json'
                evidence.write_text(json.dumps({'cases': cases}))
                (root / 'trace.zip').write_bytes(b'never upload')
                gate.PROVENANCE.parent.mkdir(parents=True)
                gate.PROVENANCE.write_text('{}')
                with patch.object(gate.subprocess, 'run') as run:
                    gate.validate()
                    gate.validate()
                    self.assertEqual(run.call_count, 2)
                    self.assertEqual(len(list(gate.ARTIFACTS.iterdir())), 6)
                    self.assertFalse((gate.ARTIFACTS / 'trace.zip').exists())
                    run.assert_called_with([gate.sys.executable, gate.VALIDATOR, str(evidence), '--provenance', str(gate.PROVENANCE), '--repo', '.'], check=True, timeout=30)
                    run.side_effect = RuntimeError('invalid evidence')
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    self.assertFalse(gate.ARTIFACTS.exists())
            finally:
                os.chdir(previous)

    def test_missing_or_duplicate_evidence_fails_before_validator(self):
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            previous = Path.cwd()
            try:
                os.chdir(tmp)
                with patch.object(gate.subprocess, 'run') as run:
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    for name in ['a', 'b']:
                        root = Path('web/test-results') / name
                        root.mkdir(parents=True)
                        (root / 'user-asset-export-v3-evidence.json').write_text('{}')
                    with self.assertRaises(RuntimeError):
                        gate.validate()
                    run.assert_not_called()
            finally:
                os.chdir(previous)

if __name__ == '__main__':
    unittest.main()
