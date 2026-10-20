from ci_workflow_contract import reviewed_workflow, reviewed_source
"""Exact terminal-suite delta; stripping only its frozen additions restores all 60 jobs."""
import ast
import hashlib
from pathlib import Path
import re
import unittest
from test_group_asset_export_v3_runner_contract import restore_runner as restore_group_runner, restore_workflow as restore_group_workflow


def legacy_main_cleanup(source):
    """Select the actual legacy main-finally path, never a helper definition."""
    tree = ast.parse(source)
    mains = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "main"]
    if len(mains) != 1:
        raise ValueError("expected one runner main")
    finalizers = [node for node in mains[0].body if isinstance(node, ast.Try) and node.finalbody]
    if len(finalizers) != 1 or len(finalizers[0].finalbody) != 1:
        raise ValueError("expected one runner finally dispatch")
    dispatch = finalizers[0].finalbody[0]
    if (not isinstance(dispatch, ast.If)
            or ast.unparse(dispatch.test) != "args.suite in V4_CONTROLS_BUDGETS"
            or not dispatch.orelse):
        raise ValueError("legacy runner cleanup path changed")
    return "\n".join(ast.get_source_segment(source, node) for node in dispatch.orelse)


BASE_RUNNER_SHA256 = 'c2fb0e247fb11c078b05c9db097a341ed14bc6f16f2ed686a0b03583d017d7f3'
RUNNER_CHANGES = [('from user_asset_export_v3_ci import configure as configure_user_export, capture as capture_user_export, '
  'validate as validate_user_export\n',
  'from user_asset_export_v3_ci import configure as configure_user_export, capture as capture_user_export, '
  'validate as validate_user_export\n'
  'from user_asset_export_v3_terminal_ci import SUITES as EXPORT_TERMINAL_SUITES, configure as '
  'configure_terminal_export, capture as capture_terminal_export, validate as validate_terminal_export\n'
  'from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n'),
 ('DIRECTORY_V3_SUITES = {"user-asset-export-v3", "directory-v3-backend", "user-assets-v3", "directory-v3", '
  '"directory-v3-controls", "directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES\n',
  'DIRECTORY_V3_SUITES = EXPORT_TERMINAL_SUITES | {"user-asset-export-v3", "directory-v3-backend", '
  '"user-assets-v3", "directory-v3", "directory-v3-controls", "directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES '
  '| GROUP_ASSETS_V3_SUITES\n'),
 ('    parser.add_argument("--suite", choices=["user-asset-export-v3", "auth", "access", "resource", "tasks", '
  '"messages", "messages-bulk", "audit", "system", "maintenance", "domains", "operations", "profile", '
  '"credential-use", "account-references", "operational-logs", "session-invalidation", '
  '"account-reference-barrier", "directory", "directory-controls", "directory-readers", "directory-v2", '
  '"directory-v2-controls", "directory-v2-readers", "directory-v3-backend", "directory-v4-backend", '
  '"group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", '
  '"user-assets-v3", "computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", '
  '"group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", "user-assets-v2", '
  '"user-assets-v2-readers"], default="auth")\n',
  '    parser.add_argument("--suite", choices=["user-asset-export-v3-failure", "user-asset-export-v3-cancel", '
  '"user-asset-export-v3", "auth", "access", "resource", "tasks", "messages", "messages-bulk", "audit", "system", '
  '"maintenance", "domains", "operations", "profile", "credential-use", "account-references", "operational-logs", '
  '"session-invalidation", "account-reference-barrier", "directory", "directory-controls", "directory-readers", '
  '"directory-v2", "directory-v2-controls", "directory-v2-readers", "directory-v3-backend", '
  '"directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", '
  '"group-members-v4-refresh", "user-assets-v3", "computer-assets-v3", "computer-assets-v3-readers", '
  '"group-assets-v3", "group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", '
  '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'),
 ('    worker = None\n', '    worker = None\n    export_worker_guard = None\n'),
 ('        capture_user_export()\n',
  '        capture_user_export()\n'
  '    if args.suite in EXPORT_TERMINAL_SUITES:\n'
  '        configure_terminal_export(env, args.suite)\n'
  '        capture_terminal_export(args.suite)\n'),
 ('                                           user_assets_v2=args.suite in USER_ASSETS_V2_SUITES)\n',
  '                                           user_assets_v2=args.suite in USER_ASSETS_V2_SUITES,\n'
  '                                           user_export_failure_v3=args.suite == '
  '"user-asset-export-v3-failure")\n'),
 ('                worker_env["ADTR_DATABASE_URL"] += "&application_name=adtr-message-bulk-worker"\n',
  '                worker_env["ADTR_DATABASE_URL"] += "&application_name=adtr-message-bulk-worker"\n'
  '            if args.suite == "user-asset-export-v3-cancel":\n'
  '                export_worker_nonce = uuid.uuid4().hex\n'
  '                export_worker_app = "adtr-user-export-" + export_worker_nonce\n'
  '                worker_env["ADTR_E2E_EXPORT_WORKER_NONCE"] = export_worker_nonce\n'
  '                worker_env["ADTR_DATABASE_URL"] += "&application_name=" + export_worker_app\n'),
 ('            worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, stdout=logs, '
  'stderr=logs)\n',
  '            worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, stdout=logs, '
  'stderr=logs)\n'
  '            if args.suite == "user-asset-export-v3-cancel":\n'
  '                export_worker_guard = OwnedExportWorkerGuard(worker, export_worker_nonce, '
  'Path("bin/adtr").resolve())\n'
  '                export_worker_guard.start()\n'
  '                env.update(ADTR_E2E_WORKER_PID=str(worker.pid), ADTR_E2E_HARNESS_PID=str(os.getpid()),\n'
  '                           ADTR_E2E_EXPORT_WORKER_STARTTIME=export_worker_guard.starttime,\n'
  '                           ADTR_E2E_EXPORT_WORKER_NONCE=export_worker_nonce,\n'
  '                           ADTR_E2E_EXPORT_WORKER_BINARY=str(Path("bin/adtr").resolve()),\n'
  '                           ADTR_E2E_EXPORT_WORKER_APPLICATION_NAME=export_worker_app,\n'
  '                           ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")\n'),
 ('        subprocess.run(command, env=env, check=True, timeout=930 if args.suite == "user-asset-export-v3" else '
  '660 if args.suite == "account-references" else 930 if args.suite in EMPTY_DIRECTORY_SUITES | '
  'USER_ASSETS_V2_SUITES | {"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES else 660 if '
  'args.suite in {"directory", "directory-v2", "directory-v3-backend", "directory-v4-backend", '
  '"group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", '
  '"directory-v3"} else 600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", '
  '"operations", "credential-use", "account-references", "operational-logs", "account-reference-barrier"} else '
  '480 if args.suite == "system" else 420)\n',
  '        subprocess.run(command, env=env, check=True, timeout=930 if args.suite in EXPORT_TERMINAL_SUITES else '
  '930 if args.suite == "user-asset-export-v3" else 660 if args.suite == "account-references" else 930 if '
  'args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | {"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES '
  '| GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", "directory-v2", "directory-v3-backend", '
  '"directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", '
  '"group-members-v4-refresh", "directory-v3"} else 600 if args.suite == "audit" else 540 if args.suite in '
  '{"maintenance", "domains", "operations", "credential-use", "account-references", "operational-logs", '
  '"account-reference-barrier"} else 480 if args.suite == "system" else 420)\n'),
 ('            validate_user_export()\n',
  '            validate_user_export()\n'
  '        if export_worker_guard is not None:\n'
  '            export_worker_guard.check()\n'
  '        if args.suite in EXPORT_TERMINAL_SUITES:\n'
  '            validate_terminal_export(args.suite)\n'),
 ('            shutdown_error = None\n',
  '            shutdown_error = None\n'
  '            if export_worker_guard is not None:\n'
  '                try:\n'
  '                    export_worker_guard.close()\n'
  '                except (OSError, RuntimeError):\n'
  '                    shutdown_error = RuntimeError("owned export worker cleanup failed")\n')]

BASE_WORKFLOW_SHA256 = '03d295e3703680da27bd58c28f31fca2f78cc46a96b2d4206a4bb49afb36568a'
WORKFLOW_CHANGES = [("      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'user-asset-export-v3' || matrix.name == "
  "'user-assets-v2' || matrix.name == 'user-assets-v2-readers' || matrix.name == 'user-assets-v3' || matrix.name "
  "== 'computer-assets-v3' || matrix.name == 'computer-assets-v3-readers' || matrix.name == 'group-assets-v3' || "
  "matrix.name == 'group-assets-v3-readers') && 'stable-body' || 'native-finish' }}\n",
  "      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'user-asset-export-v3-failure' || matrix.name == "
  "'user-asset-export-v3-cancel' || matrix.name == 'user-asset-export-v3' || matrix.name == 'user-assets-v2' || "
  "matrix.name == 'user-assets-v2-readers' || matrix.name == 'user-assets-v3' || matrix.name == "
  "'computer-assets-v3' || matrix.name == 'computer-assets-v3-readers' || matrix.name == 'group-assets-v3' || "
  "matrix.name == 'group-assets-v3-readers') && 'stable-body' || 'native-finish' }}\n"),
 ('            command: python3 scripts/test_auth_e2e.py --suite user-assets-v2-readers\n',
  '            command: python3 scripts/test_auth_e2e.py --suite user-assets-v2-readers\n'
  '          - name: user-asset-export-v3-failure\n'
  '            command: python3 scripts/test_auth_e2e.py --suite user-asset-export-v3-failure\n'
  '          - name: user-asset-export-v3-cancel\n'
  '            command: python3 scripts/test_auth_e2e.py --suite user-asset-export-v3-cancel\n'),
 ('          if-no-files-found: warn\n',
  '          if-no-files-found: warn\n'
  '          retention-days: 14\n'
  '      - name: Validate user export failure browser evidence\n'
  '        id: user-export-failure-evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' }}\n"
  '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate --suite '
  'user-asset-export-v3-failure\n'
  '      - name: Retain validated user export failure browser evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3-failure' && "
  "steps.user-export-failure-evidence.outcome == 'success' }}\n"
  '        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
  '        with:\n'
  '          name: user-asset-export-v3-failure-${{ github.run_id }}-${{ github.run_attempt }}\n'
  '          path: |\n'
  '            '
  'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-provenance.json\n'
  '            '
  'web/playwright-report/user-asset-export-v3-failure-validated/user-asset-export-v3-failure-evidence.json\n'
  '            web/playwright-report/user-asset-export-v3-failure-validated/failed-terminal.png\n'
  '            web/playwright-report/user-asset-export-v3-failure-validated/recovery-ready.png\n'
  '            web/playwright-report/user-asset-export-v3-failure-validated/user-assets-v3-*.xlsx\n'
  '          # Only validated synthetic JSON, explicit safe PNGs and actual recovery XLSX.\n'
  '          if-no-files-found: error\n'
  '          retention-days: 14\n'
  '      - name: Validate user export cancel browser evidence\n'
  '        id: user-export-cancel-evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' }}\n"
  '        run: python3 scripts/user_asset_export_v3_terminal_ci.py validate --suite user-asset-export-v3-cancel\n'
  '      - name: Retain validated user export cancel browser evidence\n'
  "        if: ${{ success() && matrix.name == 'user-asset-export-v3-cancel' && "
  "steps.user-export-cancel-evidence.outcome == 'success' }}\n"
  '        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2\n'
  '        with:\n'
  '          name: user-asset-export-v3-cancel-${{ github.run_id }}-${{ github.run_attempt }}\n'
  '          path: |\n'
  '            '
  'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-provenance.json\n'
  '            '
  'web/playwright-report/user-asset-export-v3-cancel-validated/user-asset-export-v3-cancel-evidence.json\n'
  '            web/playwright-report/user-asset-export-v3-cancel-validated/cancelled-terminal.png\n'
  '          # Only validated synthetic JSON, explicit safe PNGs and actual recovery XLSX.\n'
  '          if-no-files-found: error\n')]

def restore(text, changes):
    for old, new in reversed(changes):
        if text.count(new) != 1:
            raise ValueError('terminal export delta absent, duplicated or modified')
        text = text.replace(new, old)
    return text


def restore_runner(text):
    return restore(restore_group_runner(text), RUNNER_CHANGES)


def restore_workflow(text):
    return restore(restore_group_workflow(text), WORKFLOW_CHANGES)


class TerminalRunnerContract(unittest.TestCase):
    def test_prior_60_jobs_and_runner_byte_exact(self):
        for name, changes, sha in [('scripts/test_auth_e2e.py', RUNNER_CHANGES, BASE_RUNNER_SHA256),
                                   ('.github/workflows/ci.yml', WORKFLOW_CHANGES, BASE_WORKFLOW_SHA256)]:
            self.assertEqual(hashlib.sha256(restore((restore_group_runner if name.endswith(".py") else restore_group_workflow)(reviewed_source(name)), changes).encode()).hexdigest(), sha)

    def test_mutated_additions_rejected(self):
        for name, changes in [('scripts/test_auth_e2e.py', RUNNER_CHANGES), ('.github/workflows/ci.yml', WORKFLOW_CHANGES)]:
            source = (restore_group_runner if name.endswith(".py") else restore_group_workflow)(reviewed_source(name))
            for old, new in changes:
                with self.assertRaises(ValueError):
                    restore(source + new, changes)
                with self.assertRaises(ValueError):
                    restore(source.replace(new, '!' + new[1:]), changes)

    def test_62_jobs_preserve_limits_and_failure_uploads(self):
        source = restore_group_workflow(reviewed_workflow())
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', source, re.M)
        self.assertEqual(len(names), 42)
        self.assertEqual(len(names) + 8 + 7 + 5, 62)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', source), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', source)
        for kind in ['failure', 'cancel']:
            self.assertEqual(names.count('user-asset-export-v3-' + kind), 1)
            self.assertIn("steps.user-export-" + kind + "-evidence.outcome == 'success'", source)
        delta = ''.join(new for old, new in WORKFLOW_CHANGES)
        self.assertNotIn('always()', delta)
        self.assertNotIn('web/test-results/**', delta)
        self.assertNotIn('include-hidden-files', delta)

    def test_owned_guard_before_validation_and_cleanup(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        self.assertLess(source.index('capture_terminal_export(args.suite)'), source.index('creation_attempted = True'))
        self.assertLess(source.index('export_worker_guard.check()'), source.index('validate_terminal_export(args.suite)'))
        cleanup = legacy_main_cleanup(source)
        self.assertLess(cleanup.index('export_worker_guard.close()'), cleanup.index('process.terminate()'))
        delta = ''.join(new for old, new in RUNNER_CHANGES)
        self.assertNotIn('RATE_LIMIT', delta)
        self.assertNotIn('MFA_BYPASS', delta)

    def test_cleanup_order_ignores_helper_decoys_but_rejects_reversed_main_order(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        cleanup = legacy_main_cleanup(source)
        decoy = "def unrelated_helper():\n    process.terminate()\n    export_worker_guard.close()\n\n"
        self.assertEqual(legacy_main_cleanup(decoy + source), cleanup)
        self.assertLess(cleanup.index('export_worker_guard.close()'), cleanup.index('process.terminate()'))
        # Swap the real calls in runner source, then prepend the unchanged
        # misleading helper. Re-select via AST to reject the real regression.
        reversed_source = source.replace('export_worker_guard.close()', '__guard_close__')
        reversed_source = reversed_source.replace('process.terminate()', 'export_worker_guard.close()')
        reversed_source = reversed_source.replace('__guard_close__', 'process.terminate()')
        reversed_cleanup = legacy_main_cleanup(decoy + reversed_source)
        with self.assertRaises(AssertionError):
            self.assertLess(reversed_cleanup.index('export_worker_guard.close()'),
                            reversed_cleanup.index('process.terminate()'))

    def test_cleanup_selector_rejects_missing_or_wrong_finally_dispatch(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        for changed in (
                source.replace('def main():', 'def other_main():'),
                source.replace('if args.suite in V4_CONTROLS_BUDGETS:', 'if False:')):
            with self.assertRaises(ValueError):
                legacy_main_cleanup(changed)


if __name__ == '__main__':
    unittest.main()
