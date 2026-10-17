"""Frozen additive delta restores the exact prior 69-job runner and workflow."""
import ast
import hashlib
from pathlib import Path
import re
import unittest
from test_directory_e2e_credentials_contract import restore_credentials_runner
from test_owned_rescue_runner_contract import restore_owned_runner

CHANGES = {'scripts/test_auth_e2e.py': ('f88f83e8dfdb25928b5cb204f2b01162873ae23fcdd277f862b574dc4d0fa83b',
                              [('from computer_asset_export_v3_ci import',
                                'from group_asset_export_v3_running_cancel_ci import SUITE as '
                                'GROUP_RUNNING_CANCEL_SUITE, configure as configure_group_running_cancel, '
                                'capture as capture_group_running_cancel, validate as '
                                'validate_group_running_cancel\n'
                                'from computer_asset_export_v3_running_cancel_ci import SUITE as '
                                'COMPUTER_RUNNING_CANCEL_SUITE, configure as '
                                'configure_computer_running_cancel, capture as '
                                'capture_computer_running_cancel, validate as '
                                'validate_computer_running_cancel\n'
                                'from computer_asset_export_v3_ci import',
                                1),
                               ('DIRECTORY_V3_SUITES = {RUNNING_CANCEL_SUITE}',
                                'KIND_RUNNING_CANCEL_SUITES = {GROUP_RUNNING_CANCEL_SUITE, '
                                'COMPUTER_RUNNING_CANCEL_SUITE}\n'
                                'DIRECTORY_V3_SUITES = KIND_RUNNING_CANCEL_SUITES | {RUNNING_CANCEL_SUITE}',
                                1),
                               ('choices=["user-asset-export-v3-running-cancel"',
                                'choices=["group-asset-export-v3-running-cancel", '
                                '"computer-asset-export-v3-running-cancel", '
                                '"user-asset-export-v3-running-cancel"',
                                1),
                               ('    logs = tempfile.TemporaryFile(mode="w+")\n',
                                '    if args.suite == GROUP_RUNNING_CANCEL_SUITE:\n'
                                '        configure_group_running_cancel(env)\n'
                                '        capture_group_running_cancel()\n'
                                '    if args.suite == COMPUTER_RUNNING_CANCEL_SUITE:\n'
                                '        configure_computer_running_cancel(env)\n'
                                '        capture_computer_running_cancel()\n'
                                '    logs = tempfile.TemporaryFile(mode="w+")\n',
                                1),
                               ('computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES | '
                                'COMPUTER_EXPORT_SUITES,',
                                'computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES | '
                                'COMPUTER_EXPORT_SUITES | {COMPUTER_RUNNING_CANCEL_SUITE},',
                                1),
                               ('group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | GROUP_EXPORT_SUITES,',
                                'group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | GROUP_EXPORT_SUITES '
                                '| {GROUP_RUNNING_CANCEL_SUITE},',
                                1),
                               ('if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"}:',
                                'if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | '
                                'KIND_RUNNING_CANCEL_SUITES:',
                                2),
                               ('                if args.suite == RUNNING_CANCEL_SUITE:\n'
                                '                    running_cancel_guard = RunningExportCancelGuard(worker, '
                                'export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, '
                                'env["ADTR_DATABASE_URL"])\n',
                                '                if args.suite in {RUNNING_CANCEL_SUITE} | '
                                'KIND_RUNNING_CANCEL_SUITES:\n'
                                '                    asset_kind = {GROUP_RUNNING_CANCEL_SUITE: "group", '
                                'COMPUTER_RUNNING_CANCEL_SUITE: "computer"}.get(args.suite, "user")\n'
                                '                    running_cancel_guard = RunningExportCancelGuard(worker, '
                                'export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, '
                                'env["ADTR_DATABASE_URL"], asset_kind=asset_kind)\n',
                                1),
                               ('timeout=930 if args.suite == RUNNING_CANCEL_SUITE',
                                'timeout=930 if args.suite in KIND_RUNNING_CANCEL_SUITES else 930 if '
                                'args.suite == RUNNING_CANCEL_SUITE',
                                1),
                               ('            validate_running_cancel()\n',
                                '            if args.suite == GROUP_RUNNING_CANCEL_SUITE:\n'
                                '                validate_group_running_cancel()\n'
                                '            elif args.suite == COMPUTER_RUNNING_CANCEL_SUITE:\n'
                                '                validate_computer_running_cancel()\n'
                                '            else:\n'
                                '                validate_running_cancel()\n',
                                1)]),
 '.github/workflows/ci.yml': ('bc1da0963ff71f84ecf3b45e58f020011cac75e8bb27b7b35fed9a706e880afa',
                              [('ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == '
                                "'user-asset-export-v3-running-cancel'",
                                'ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == '
                                "'group-asset-export-v3-running-cancel' || matrix.name == "
                                "'computer-asset-export-v3-running-cancel' || matrix.name == "
                                "'user-asset-export-v3-running-cancel'",
                                1),
                               ('          - name: user-asset-export-v3-running-cancel\n',
                                '          - name: group-asset-export-v3-running-cancel\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3-running-cancel\n'
                                '          - name: computer-asset-export-v3-running-cancel\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'computer-asset-export-v3-running-cancel\n'
                                '          - name: user-asset-export-v3-running-cancel\n',
                                1),
                               ('      - name: Validate running user export cancellation evidence\n'
                                '        id: user-export-running-cancel-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'user-asset-export-v3-running-cancel' }}\n"
                                '        run: python3 scripts/user_asset_export_v3_running_cancel_ci.py '
                                'validate\n'
                                '      - name: Retain validated running user export cancellation evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'user-asset-export-v3-running-cancel' && "
                                "steps.user-export-running-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-running-cancel-${{ github.run_id '
                                '}}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/user-asset-export-v3-running-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/user-asset-export-v3-running-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/running-checkpoint.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/cancel-requested.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/cancelled-terminal.png\n'
                                '          # Exact independently validated synthetic evidence allowlist; no '
                                'XLSX, trace or proof inputs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n',
                                '      - name: Validate running group export cancellation evidence\n'
                                '        id: group-export-running-cancel-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'group-asset-export-v3-running-cancel' }}\n"
                                '        run: python3 scripts/group_asset_export_v3_running_cancel_ci.py '
                                'validate\n'
                                '      - name: Retain validated running group export cancellation evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'group-asset-export-v3-running-cancel' && "
                                "steps.group-export-running-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: group-asset-export-v3-running-cancel-${{ github.run_id '
                                '}}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-running-cancel-validated/group-asset-export-v3-running-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-running-cancel-validated/group-asset-export-v3-running-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-running-cancel-validated/running-checkpoint.png\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-running-cancel-validated/cancel-requested.png\n'
                                '            '
                                'web/playwright-report/group-asset-export-v3-running-cancel-validated/cancelled-terminal.png\n'
                                '          # Exact independently validated synthetic evidence allowlist; no '
                                'XLSX, trace or proof inputs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate running computer export cancellation evidence\n'
                                '        id: computer-export-running-cancel-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-running-cancel' }}\n"
                                '        run: python3 scripts/computer_asset_export_v3_running_cancel_ci.py '
                                'validate\n'
                                '      - name: Retain validated running computer export cancellation '
                                'evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-running-cancel' && "
                                "steps.computer-export-running-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: computer-asset-export-v3-running-cancel-${{ github.run_id '
                                '}}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-running-cancel-validated/computer-asset-export-v3-running-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-running-cancel-validated/computer-asset-export-v3-running-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-running-cancel-validated/running-checkpoint.png\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-running-cancel-validated/cancel-requested.png\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-running-cancel-validated/cancelled-terminal.png\n'
                                '          # Exact independently validated synthetic evidence allowlist; no '
                                'XLSX, trace or proof inputs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate running user export cancellation evidence\n'
                                '        id: user-export-running-cancel-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'user-asset-export-v3-running-cancel' }}\n"
                                '        run: python3 scripts/user_asset_export_v3_running_cancel_ci.py '
                                'validate\n'
                                '      - name: Retain validated running user export cancellation evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'user-asset-export-v3-running-cancel' && "
                                "steps.user-export-running-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
                                '        with:\n'
                                '          name: user-asset-export-v3-running-cancel-${{ github.run_id '
                                '}}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/user-asset-export-v3-running-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/user-asset-export-v3-running-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/running-checkpoint.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/cancel-requested.png\n'
                                '            '
                                'web/playwright-report/user-asset-export-v3-running-cancel-validated/cancelled-terminal.png\n'
                                '          # Exact independently validated synthetic evidence allowlist; no '
                                'XLSX, trace or proof inputs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n',
                                1)])}

def restore_before_owned(text, name):
    if name == 'scripts/test_auth_e2e.py':
        text = restore_credentials_runner(text)
    for old, new, count in reversed(CHANGES[name][1]):
        if text.count(new) != count:
            raise ValueError('kind running cancel addition absent or altered: ' + name)
        text = text.replace(new, old)
    return text

def restore(text, name):
    if name == 'scripts/test_auth_e2e.py':
        text = restore_owned_runner(text)
    return restore_before_owned(text, name)

class KindRunningCancelRunnerContract(unittest.TestCase):
    def test_preserve_exact_prior_69_job_bytes(self):
        for name, (digest, _) in CHANGES.items():
            self.assertEqual(hashlib.sha256(restore(Path(name).read_text(), name).encode()).hexdigest(), digest)

    def test_71_jobs_and_unchanged_limits(self):
        source = Path('.github/workflows/ci.yml').read_text()
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', source, re.M)
        self.assertEqual(len(names), 51)
        self.assertEqual(len(names) + 8 + 7 + 5, 71)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', source), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', source)
        for kind in ('user', 'group', 'computer'):
            self.assertEqual(names.count(kind + '-asset-export-v3-running-cancel'), 1)

    def test_mutations_and_duplicates_fail(self):
        for name, (_, changes) in CHANGES.items():
            source = Path(name).read_text()
            if name == 'scripts/test_auth_e2e.py':
                source = restore_owned_runner(source)
            for _, new, _ in changes:
                with self.assertRaises(ValueError):
                    restore_before_owned(source + new, name)
                with self.assertRaises(ValueError):
                    restore_before_owned(source.replace(new, '!' + new[1:]), name)

    def test_running_suites_never_enter_terminal_sets(self):
        for kind in ('user', 'group', 'computer'):
            source = Path('scripts/' + kind + '_asset_export_v3_terminal_ci.py').read_text()
            self.assertNotIn('running-cancel', source)
        tree = ast.parse(Path('scripts/test_auth_e2e.py').read_text())
        for node in tree.body:
            if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id in ('GROUP_EXPORT_SUITES', 'COMPUTER_EXPORT_SUITES') for t in node.targets):
                self.assertNotIn('RUNNING_CANCEL', ast.unparse(node.value))

if __name__ == '__main__':
    unittest.main()
