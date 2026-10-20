from ci_workflow_contract import reviewed_workflow, reviewed_source
"""Exact new group-export delta. Removing only frozen additions restores all 62 existing jobs."""
import hashlib
from pathlib import Path
import re
import unittest
from test_computer_asset_export_v3_runner_contract import restore as restore_computer

CHANGES = {'.github/workflows/ci.yml': ('d6e51571d5a783650a4c142993f7bb28d00e986c0d7981c114bb43faaf5995f9',
                              [("${{ (matrix.name == 'user-asset-export-v3-failure'",
                                "${{ (matrix.name == 'group-asset-export-v3' || matrix.name == "
                                "'group-asset-export-v3-failure' || matrix.name == "
                                "'group-asset-export-v3-cancel' || matrix.name == "
                                "'user-asset-export-v3-failure'"),
                               ('          - name: user-asset-export-v3-failure\n',
                                '          - name: group-asset-export-v3\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3\n'
                                '          - name: group-asset-export-v3-failure\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3-failure\n'
                                '          - name: group-asset-export-v3-cancel\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3-cancel\n'
                                '          - name: user-asset-export-v3-failure\n'),
                               ('      - name: Validate user export failure browser evidence\n'
                                '        id: user-export-failure-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' "
                                '}}\n'
                                '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate '
                                '--suite user-asset-export-v3-failure\n'
                                '      - name: Retain validated user export failure browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' "
                                "&& steps.user-export-failure-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-failure-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/failed-terminal.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/recovery-ready.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-assets-v3-*.xlsx\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate user export cancel browser evidence\n'
                                '        id: user-export-cancel-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' "
                                '}}\n'
                                '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate '
                                '--suite user-asset-export-v3-cancel\n'
                                '      - name: Retain validated user export cancel browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' "
                                "&& steps.user-export-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-cancel-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/cancelled-terminal.png\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate user asset export dictionary3 browser evidence\n'
                                '        id: user-export-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3' }}\n"
                                '        run: python3 scripts/user_asset_export_v3_ci.py validate\n'
                                '      - name: Retain validated user asset export dictionary3 browser '
                                'evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3' && "
                                "steps.user-export-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-asset-export-v3-provenance.json\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-asset-export-v3-evidence.json\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-assets-v3-*.xlsx\n'
                                '            '
                                'web/playwright-report/user-export-validated/allMatched-ready.png\n'
                                '            web/playwright-report/user-export-validated/empty-ready.png\n'
                                '          # No trace, videos, logs, storage state, credentials or raw '
                                'failures.\n'
                                '          # Only independently validated synthetic JSON, XLSX and safe '
                                'PNGs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n',
                                '      - name: Validate group export failure browser evidence\n'
                                '        id: group-export-failure-evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3-failure' "
                                '}}\n'
                                '        run: python3 scripts/group_asset_export_v3_terminal_ci.py validate '
                                '--suite group-asset-export-v3-failure\n'
                                '      - name: Retain validated group export failure browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3-failure' "
                                "&& steps.group-export-failure-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: group-asset-export-v3-failure-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-failure-validated/group-asset-export-v3-failure-provenance.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-failure-validated/group-asset-export-v3-failure-evidence.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-failure-validated/failed-terminal.png\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-failure-validated/recovery-ready.png\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-failure-validated/group-assets-v3-*.xlsx\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate group export cancel browser evidence\n'
                                '        id: group-export-cancel-evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3-cancel' "
                                '}}\n'
                                '        run: python3 scripts/group_asset_export_v3_terminal_ci.py validate '
                                '--suite group-asset-export-v3-cancel\n'
                                '      - name: Retain validated group export cancel browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3-cancel' "
                                "&& steps.group-export-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: group-asset-export-v3-cancel-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-cancel-validated/group-asset-export-v3-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-cancel-validated/group-asset-export-v3-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-cancel-validated/cancelled-terminal.png\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate group asset export dictionary3 browser evidence\n'
                                '        id: group-export-evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3' }}\n"
                                '        run: python3 scripts/group_asset_export_v3_ci.py validate\n'
                                '      - name: Retain validated group asset export dictionary3 browser '
                                'evidence\n'
                                "        if: ${{ success() && matrix.name == 'group-asset-export-v3' && "
                                "steps.group-export-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: group-asset-export-v3-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/group-export-validated/group-asset-export-v3-provenance.json\n'
                                '            '
                                'web/playwright-report/group-export-validated/group-asset-export-v3-evidence.json\n'
                                '            '
                                'web/playwright-report/group-export-validated/group-assets-v3-*.xlsx\n'
                                '            web/playwright-report/group-export-validated/default-ready.png\n'
                                '            '
                                'web/playwright-report/group-export-validated/allMatched-ready.png\n'
                                '            web/playwright-report/group-export-validated/empty-ready.png\n'
                                '          # No trace, videos, logs, storage state, credentials or raw '
                                'failures.\n'
                                '          # Only independently validated synthetic JSON, XLSX and safe '
                                'PNGs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate user export failure browser evidence\n'
                                '        id: user-export-failure-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' "
                                '}}\n'
                                '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate '
                                '--suite user-asset-export-v3-failure\n'
                                '      - name: Retain validated user export failure browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' "
                                "&& steps.user-export-failure-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-failure-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/failed-terminal.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/recovery-ready.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-failure-validated/user-assets-v3-*.xlsx\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate user export cancel browser evidence\n'
                                '        id: user-export-cancel-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' "
                                '}}\n'
                                '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate '
                                '--suite user-asset-export-v3-cancel\n'
                                '      - name: Retain validated user export cancel browser evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' "
                                "&& steps.user-export-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-cancel-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-cancel-validated/cancelled-terminal.png\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and actual '
                                'recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate user asset export dictionary3 browser evidence\n'
                                '        id: user-export-evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3' }}\n"
                                '        run: python3 scripts/user_asset_export_v3_ci.py validate\n'
                                '      - name: Retain validated user asset export dictionary3 browser '
                                'evidence\n'
                                "        if: ${{ success() && matrix.name == 'user-asset-export-v3' && "
                                "steps.user-export-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-asset-export-v3-provenance.json\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-asset-export-v3-evidence.json\n'
                                '            '
                                'web/playwright-report/user-export-validated/user-assets-v3-*.xlsx\n'
                                '            '
                                'web/playwright-report/user-export-validated/allMatched-ready.png\n'
                                '            web/playwright-report/user-export-validated/empty-ready.png\n'
                                '          # No trace, videos, logs, storage state, credentials or raw '
                                'failures.\n'
                                '          # Only independently validated synthetic JSON, XLSX and safe '
                                'PNGs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n')]),
 'scripts/test_auth_e2e.py': ('88471e4ae89e14b85de9669f787118b78b618572cc7c1e11f5045d604e45f9ad',
                              [('from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n',
                                'from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n'
                                'from group_asset_export_v3_ci import configure as configure_group_export, '
                                'capture as capture_group_export, validate as validate_group_export\n'
                                'from group_asset_export_v3_terminal_ci import SUITES as '
                                'GROUP_EXPORT_TERMINAL_SUITES, configure as configure_group_terminal, '
                                'capture as capture_group_terminal, validate as validate_group_terminal\n'),
                               ('GROUP_ASSETS_V3_SUITES = ',
                                'GROUP_EXPORT_SUITES = GROUP_EXPORT_TERMINAL_SUITES | '
                                '{"group-asset-export-v3"}\n'
                                'GROUP_ASSETS_V3_SUITES = '),
                               ('DIRECTORY_V3_SUITES = EXPORT_TERMINAL_SUITES |',
                                'DIRECTORY_V3_SUITES = GROUP_EXPORT_SUITES | EXPORT_TERMINAL_SUITES |'),
                               ('choices=["user-asset-export-v3-failure"',
                                'choices=["group-asset-export-v3", "group-asset-export-v3-failure", '
                                '"group-asset-export-v3-cancel", "user-asset-export-v3-failure"'),
                               ('    logs = tempfile.TemporaryFile(mode="w+")',
                                '    if args.suite == "group-asset-export-v3":\n'
                                '        configure_group_export(env)\n'
                                '        capture_group_export()\n'
                                '    if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '        configure_group_terminal(env, args.suite)\n'
                                '        capture_group_terminal(args.suite)\n'
                                '    logs = tempfile.TemporaryFile(mode="w+")'),
                               ('group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES,',
                                'group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | '
                                'GROUP_EXPORT_SUITES,'),
                               ('user_export_failure_v3=args.suite == "user-asset-export-v3-failure")',
                                'user_export_failure_v3=args.suite == "user-asset-export-v3-failure",\n'
                                '                                           '
                                'group_export_failure_v3=args.suite == "group-asset-export-v3-failure")'),
                               ('if args.suite == "user-asset-export-v3-cancel":',
                                'if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel"}:'),
                               ('timeout=930 if args.suite in EXPORT_TERMINAL_SUITES',
                                'timeout=930 if args.suite in GROUP_EXPORT_SUITES else 930 if args.suite in '
                                'EXPORT_TERMINAL_SUITES'),
                               ('            validate_terminal_export(args.suite)',
                                '            validate_terminal_export(args.suite)\n'
                                '        if args.suite == "group-asset-export-v3":\n'
                                '            validate_group_export()\n'
                                '        if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '            validate_group_terminal(args.suite)')])}

def restore_group_only(text, name):
    for old, new in reversed(CHANGES[name][1]):
        expected = 2 if new == 'if args.suite in {"user-asset-export-v3-cancel", "group-asset-export-v3-cancel"}:' else 1
        if text.count(new) != expected:
            raise ValueError('group export addition absent or altered: ' + name)
        text = text.replace(new, old)
    return text

def restore(text, name):
    return restore_group_only(restore_computer(text, name), name)

def restore_runner(text):
    return restore(text, 'scripts/test_auth_e2e.py')

def restore_workflow(text):
    return restore(text, '.github/workflows/ci.yml')

class GroupExportRunnerContract(unittest.TestCase):
    def test_preserve_all_62_jobs_and_runner_bytes(self):
        for name, (digest, _) in CHANGES.items():
            self.assertEqual(hashlib.sha256(restore(reviewed_source(name), name).encode()).hexdigest(), digest)
    def test_65_jobs_and_limits(self):
        source = restore_computer(reviewed_workflow(), '.github/workflows/ci.yml')
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', source, re.M)
        self.assertEqual(len(names), 45)
        self.assertEqual(len(names) + 8 + 7 + 5, 65)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', source), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', source)
        for suffix in ('', '-failure', '-cancel'):
            self.assertEqual(names.count('group-asset-export-v3' + suffix), 1)
    def test_modified_or_duplicate_delta_fails(self):
        for name, (_, changes) in CHANGES.items():
            source = restore_computer(reviewed_source(name), name)
            for _, new in changes:
                with self.assertRaises(ValueError): restore_group_only(source + new, name)
                with self.assertRaises(ValueError): restore_group_only(source.replace(new, '!' + new[1:]), name)

if __name__ == '__main__': unittest.main()
