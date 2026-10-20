from ci_workflow_contract import reviewed_workflow, reviewed_source
"""Exact computer-export delta; reversal preserves all 65 existing CI jobs.

This static contract never starts a runner, browser, build or subprocess.
"""
import hashlib
from pathlib import Path
import re
import unittest

from test_user_asset_export_v3_running_cancel_runner_contract import restore as restore_running_cancel

CHANGES = {'.github/workflows/ci.yml': ('0c7af05d6b5a38b78e2a4d854d9768d214df32dafef46e8e040e9d9b40acb964',
                              [('  browser:\n'
                                '    needs: [prepare-postgres]\n'
                                '    env:\n'
                                '      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == '
                                "'group-asset-export-v3' || matrix.name == "
                                "'group-asset-export-v3-failure' || matrix.name == "
                                "'group-asset-export-v3-cancel' || matrix.name == "
                                "'user-asset-export-v3-failure' || matrix.name == "
                                "'user-asset-export-v3-cancel' || matrix.name == "
                                "'user-asset-export-v3' || matrix.name == 'user-assets-v2' || "
                                "matrix.name == 'user-assets-v2-readers' || matrix.name == "
                                "'user-assets-v3' || matrix.name == 'computer-assets-v3' || "
                                "matrix.name == 'computer-assets-v3-readers' || matrix.name == "
                                "'group-assets-v3' || matrix.name == 'group-assets-v3-readers') && "
                                "'stable-body' || 'native-finish' }}\n"
                                '    runs-on: ubuntu-24.04\n'
                                '    timeout-minutes: 20\n'
                                '    permissions:\n',
                                '  browser:\n'
                                '    needs: [prepare-postgres]\n'
                                '    env:\n'
                                '      ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == '
                                "'computer-asset-export-v3' || matrix.name == "
                                "'computer-asset-export-v3-failure' || matrix.name == "
                                "'computer-asset-export-v3-cancel' || matrix.name == "
                                "'group-asset-export-v3' || matrix.name == "
                                "'group-asset-export-v3-failure' || matrix.name == "
                                "'group-asset-export-v3-cancel' || matrix.name == "
                                "'user-asset-export-v3-failure' || matrix.name == "
                                "'user-asset-export-v3-cancel' || matrix.name == "
                                "'user-asset-export-v3' || matrix.name == 'user-assets-v2' || "
                                "matrix.name == 'user-assets-v2-readers' || matrix.name == "
                                "'user-assets-v3' || matrix.name == 'computer-assets-v3' || "
                                "matrix.name == 'computer-assets-v3-readers' || matrix.name == "
                                "'group-assets-v3' || matrix.name == 'group-assets-v3-readers') && "
                                "'stable-body' || 'native-finish' }}\n"
                                '    runs-on: ubuntu-24.04\n'
                                '    timeout-minutes: 20\n'
                                '    permissions:\n'),
                               ('            command: python3 scripts/test_auth_e2e.py --suite '
                                'user-assets-v2\n'
                                '          - name: user-assets-v2-readers\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'user-assets-v2-readers\n'
                                '          - name: group-asset-export-v3\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3\n'
                                '          - name: group-asset-export-v3-failure\n',
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'user-assets-v2\n'
                                '          - name: user-assets-v2-readers\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'user-assets-v2-readers\n'
                                '          - name: computer-asset-export-v3\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'computer-asset-export-v3\n'
                                '          - name: computer-asset-export-v3-failure\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'computer-asset-export-v3-failure\n'
                                '          - name: computer-asset-export-v3-cancel\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'computer-asset-export-v3-cancel\n'
                                '          - name: group-asset-export-v3\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'group-asset-export-v3\n'
                                '          - name: group-asset-export-v3-failure\n'),
                               ('          # Strict success validation above requires JSON and all '
                                '14 step/viewport PNGs.\n'
                                '          # Keep partial failure evidence without masking the '
                                'browser error.\n'
                                '          if-no-files-found: warn\n'
                                '          retention-days: 14\n'
                                '      - name: Validate group export failure browser evidence\n'
                                '        id: group-export-failure-evidence\n',
                                '          # Strict success validation above requires JSON and all '
                                '14 step/viewport PNGs.\n'
                                '          # Keep partial failure evidence without masking the '
                                'browser error.\n'
                                '          if-no-files-found: warn\n'
                                '          retention-days: 14\n'
                                '      - name: Validate computer export failure browser evidence\n'
                                '        id: computer-export-failure-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-failure' }}\n"
                                '        run: python3 '
                                'scripts/computer_asset_export_v3_terminal_ci.py validate --suite '
                                'computer-asset-export-v3-failure\n'
                                '      - name: Retain validated computer export failure browser '
                                'evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-failure' && "
                                "steps.computer-export-failure-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 '
                                '# v4.6.2\n'
                                '        with:\n'
                                '          name: computer-asset-export-v3-failure-${{ '
                                'github.run_id }}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-failure-validated/computer-asset-export-v3-failure-provenance.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-failure-validated/computer-asset-export-v3-failure-evidence.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-failure-validated/failed-terminal.png\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-failure-validated/recovery-ready.png\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-failure-validated/computer-assets-v3-*.xlsx\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and '
                                'actual recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate computer export cancel browser evidence\n'
                                '        id: computer-export-cancel-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-cancel' }}\n"
                                '        run: python3 '
                                'scripts/computer_asset_export_v3_terminal_ci.py validate --suite '
                                'computer-asset-export-v3-cancel\n'
                                '      - name: Retain validated computer export cancel browser '
                                'evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3-cancel' && "
                                "steps.computer-export-cancel-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 '
                                '# v4.6.2\n'
                                '        with:\n'
                                '          name: computer-asset-export-v3-cancel-${{ github.run_id '
                                '}}-${{ github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-cancel-validated/computer-asset-export-v3-cancel-provenance.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-cancel-validated/computer-asset-export-v3-cancel-evidence.json\n'
                                '            '
                                'web/playwright-report/computer-asset-export-v3-cancel-validated/cancelled-terminal.png\n'
                                '          # Only validated synthetic JSON, explicit safe PNGs and '
                                'actual recovery XLSX.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate computer asset export dictionary3 browser '
                                'evidence\n'
                                '        id: computer-export-evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3' }}\n"
                                '        run: python3 scripts/computer_asset_export_v3_ci.py '
                                'validate\n'
                                '      - name: Retain validated computer asset export dictionary3 '
                                'browser evidence\n'
                                '        if: ${{ success() && matrix.name == '
                                "'computer-asset-export-v3' && "
                                "steps.computer-export-evidence.outcome == 'success' }}\n"
                                '        uses: '
                                'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 '
                                '# v4.6.2\n'
                                '        with:\n'
                                '          name: computer-asset-export-v3-${{ github.run_id }}-${{ '
                                'github.run_attempt }}\n'
                                '          path: |\n'
                                '            '
                                'web/playwright-report/computer-export-validated/computer-asset-export-v3-provenance.json\n'
                                '            '
                                'web/playwright-report/computer-export-validated/computer-asset-export-v3-evidence.json\n'
                                '            '
                                'web/playwright-report/computer-export-validated/computer-assets-v3-*.xlsx\n'
                                '            '
                                'web/playwright-report/computer-export-validated/default-ready.png\n'
                                '            '
                                'web/playwright-report/computer-export-validated/allMatched-ready.png\n'
                                '            '
                                'web/playwright-report/computer-export-validated/empty-ready.png\n'
                                '          # No trace, videos, logs, storage state, credentials or '
                                'raw failures.\n'
                                '          # Only independently validated synthetic JSON, XLSX and '
                                'safe PNGs.\n'
                                '          if-no-files-found: error\n'
                                '          retention-days: 14\n'
                                '      - name: Validate group export failure browser evidence\n'
                                '        id: group-export-failure-evidence\n')]),
 'scripts/test_auth_e2e.py': ('cdfd699518e4289be7b2e241f6769b0c90a2490967350a562ffa62e66e6730eb',
                              [('from user_asset_export_v3_ci import configure as '
                                'configure_user_export, capture as capture_user_export, validate '
                                'as validate_user_export\n'
                                'from user_asset_export_v3_terminal_ci import SUITES as '
                                'EXPORT_TERMINAL_SUITES, configure as configure_terminal_export, '
                                'capture as capture_terminal_export, validate as '
                                'validate_terminal_export\n'
                                'from user_asset_export_v3_worker_guard import '
                                'OwnedExportWorkerGuard\n'
                                'from group_asset_export_v3_ci import configure as '
                                'configure_group_export, capture as capture_group_export, validate '
                                'as validate_group_export\n'
                                'from group_asset_export_v3_terminal_ci import SUITES as '
                                'GROUP_EXPORT_TERMINAL_SUITES, configure as '
                                'configure_group_terminal, capture as capture_group_terminal, '
                                'validate as validate_group_terminal\n'
                                'from ci_postgres_image import postgres_image_args\n',
                                'from user_asset_export_v3_ci import configure as '
                                'configure_user_export, capture as capture_user_export, validate '
                                'as validate_user_export\n'
                                'from user_asset_export_v3_terminal_ci import SUITES as '
                                'EXPORT_TERMINAL_SUITES, configure as configure_terminal_export, '
                                'capture as capture_terminal_export, validate as '
                                'validate_terminal_export\n'
                                'from user_asset_export_v3_worker_guard import '
                                'OwnedExportWorkerGuard\n'
                                'from computer_asset_export_v3_ci import configure as '
                                'configure_computer_export, capture as capture_computer_export, '
                                'validate as validate_computer_export\n'
                                'from computer_asset_export_v3_terminal_ci import SUITES as '
                                'COMPUTER_EXPORT_TERMINAL_SUITES, configure as '
                                'configure_computer_terminal, capture as '
                                'capture_computer_terminal, validate as '
                                'validate_computer_terminal\n'
                                'from group_asset_export_v3_ci import configure as '
                                'configure_group_export, capture as capture_group_export, validate '
                                'as validate_group_export\n'
                                'from group_asset_export_v3_terminal_ci import SUITES as '
                                'GROUP_EXPORT_TERMINAL_SUITES, configure as '
                                'configure_group_terminal, capture as capture_group_terminal, '
                                'validate as validate_group_terminal\n'
                                'from ci_postgres_image import postgres_image_args\n'),
                               ('USER_ASSETS_V2_SUITES = {"user-assets-v2", '
                                '"user-assets-v2-readers"}\n'
                                'DIRECTORY_V2_SUITES = {"directory-v2", "directory-v2-controls", '
                                '"directory-v2-readers"} | USER_ASSETS_V2_SUITES\n'
                                'COMPUTER_ASSETS_V3_SUITES = {"computer-assets-v3", '
                                '"computer-assets-v3-readers"}\n'
                                'GROUP_EXPORT_SUITES = GROUP_EXPORT_TERMINAL_SUITES | '
                                '{"group-asset-export-v3"}\n'
                                'GROUP_ASSETS_V3_SUITES = {"group-assets-v3", '
                                '"group-assets-v3-readers"}\n'
                                'EMPTY_TRANSITION_SUITES = {"group-assets-v3", '
                                '"computer-assets-v3"}\n'
                                'DIRECTORY_V3_SUITES = GROUP_EXPORT_SUITES | '
                                'EXPORT_TERMINAL_SUITES | {"user-asset-export-v3", '
                                '"directory-v3-backend", "user-assets-v3", "directory-v3", '
                                '"directory-v3-controls", "directory-v3-readers"} | '
                                'COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES\n'
                                'DIRECTORY_V4_SUITES = {"directory-v4-backend", '
                                '"group-members-v4", "group-members-v4-empty", '
                                '"group-members-v4-pagination", "group-members-v4-refresh"}\n'
                                'DIRECTORY_SUITES = DIRECTORY_V4_SUITES | DIRECTORY_V3_SUITES | '
                                '{"directory", "directory-controls", "directory-readers"} | '
                                'DIRECTORY_V2_SUITES\n'
                                'EMPTY_DIRECTORY_SUITES = {"directory-controls", '
                                '"directory-readers", "directory-v2-controls", '
                                '"directory-v2-readers", "directory-v3-controls", '
                                '"directory-v3-readers"}\n',
                                'USER_ASSETS_V2_SUITES = {"user-assets-v2", '
                                '"user-assets-v2-readers"}\n'
                                'DIRECTORY_V2_SUITES = {"directory-v2", "directory-v2-controls", '
                                '"directory-v2-readers"} | USER_ASSETS_V2_SUITES\n'
                                'COMPUTER_ASSETS_V3_SUITES = {"computer-assets-v3", '
                                '"computer-assets-v3-readers"}\n'
                                'COMPUTER_EXPORT_SUITES = COMPUTER_EXPORT_TERMINAL_SUITES | '
                                '{"computer-asset-export-v3"}\n'
                                'GROUP_EXPORT_SUITES = GROUP_EXPORT_TERMINAL_SUITES | '
                                '{"group-asset-export-v3"}\n'
                                'GROUP_ASSETS_V3_SUITES = {"group-assets-v3", '
                                '"group-assets-v3-readers"}\n'
                                'EMPTY_TRANSITION_SUITES = {"group-assets-v3", '
                                '"computer-assets-v3"}\n'
                                'DIRECTORY_V3_SUITES = COMPUTER_EXPORT_SUITES | '
                                'GROUP_EXPORT_SUITES | EXPORT_TERMINAL_SUITES | '
                                '{"user-asset-export-v3", "directory-v3-backend", '
                                '"user-assets-v3", "directory-v3", "directory-v3-controls", '
                                '"directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | '
                                'GROUP_ASSETS_V3_SUITES\n'
                                'DIRECTORY_V4_SUITES = {"directory-v4-backend", '
                                '"group-members-v4", "group-members-v4-empty", '
                                '"group-members-v4-pagination", "group-members-v4-refresh"}\n'
                                'DIRECTORY_SUITES = DIRECTORY_V4_SUITES | DIRECTORY_V3_SUITES | '
                                '{"directory", "directory-controls", "directory-readers"} | '
                                'DIRECTORY_V2_SUITES\n'
                                'EMPTY_DIRECTORY_SUITES = {"directory-controls", '
                                '"directory-readers", "directory-v2-controls", '
                                '"directory-v2-readers", "directory-v3-controls", '
                                '"directory-v3-readers"}\n'),
                               ('\n'
                                'def main():\n'
                                '    parser = argparse.ArgumentParser()\n'
                                '    parser.add_argument("--suite", '
                                'choices=["group-asset-export-v3", '
                                '"group-asset-export-v3-failure", "group-asset-export-v3-cancel", '
                                '"user-asset-export-v3-failure", "user-asset-export-v3-cancel", '
                                '"user-asset-export-v3", "auth", "access", "resource", "tasks", '
                                '"messages", "messages-bulk", "audit", "system", "maintenance", '
                                '"domains", "operations", "profile", "credential-use", '
                                '"account-references", "operational-logs", "session-invalidation", '
                                '"account-reference-barrier", "directory", "directory-controls", '
                                '"directory-readers", "directory-v2", "directory-v2-controls", '
                                '"directory-v2-readers", "directory-v3-backend", '
                                '"directory-v4-backend", "group-members-v4", '
                                '"group-members-v4-empty", "group-members-v4-pagination", '
                                '"group-members-v4-refresh", "user-assets-v3", '
                                '"computer-assets-v3", "computer-assets-v3-readers", '
                                '"group-assets-v3", "group-assets-v3-readers", "directory-v3", '
                                '"directory-v3-controls", "directory-v3-readers", '
                                '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'
                                '    parser.add_argument("--expired", action="store_true")\n'
                                '    args = parser.parse_args()\n'
                                '    # Check display support before creating a disposable database '
                                'or fixtures.\n',
                                '\n'
                                'def main():\n'
                                '    parser = argparse.ArgumentParser()\n'
                                '    parser.add_argument("--suite", '
                                'choices=["computer-asset-export-v3", '
                                '"computer-asset-export-v3-failure", '
                                '"computer-asset-export-v3-cancel", "group-asset-export-v3", '
                                '"group-asset-export-v3-failure", "group-asset-export-v3-cancel", '
                                '"user-asset-export-v3-failure", "user-asset-export-v3-cancel", '
                                '"user-asset-export-v3", "auth", "access", "resource", "tasks", '
                                '"messages", "messages-bulk", "audit", "system", "maintenance", '
                                '"domains", "operations", "profile", "credential-use", '
                                '"account-references", "operational-logs", "session-invalidation", '
                                '"account-reference-barrier", "directory", "directory-controls", '
                                '"directory-readers", "directory-v2", "directory-v2-controls", '
                                '"directory-v2-readers", "directory-v3-backend", '
                                '"directory-v4-backend", "group-members-v4", '
                                '"group-members-v4-empty", "group-members-v4-pagination", '
                                '"group-members-v4-refresh", "user-assets-v3", '
                                '"computer-assets-v3", "computer-assets-v3-readers", '
                                '"group-assets-v3", "group-assets-v3-readers", "directory-v3", '
                                '"directory-v3-controls", "directory-v3-readers", '
                                '"user-assets-v2", "user-assets-v2-readers"], default="auth")\n'
                                '    parser.add_argument("--expired", action="store_true")\n'
                                '    args = parser.parse_args()\n'
                                '    # Check display support before creating a disposable database '
                                'or fixtures.\n'),
                               ('    if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '        configure_group_terminal(env, args.suite)\n'
                                '        capture_group_terminal(args.suite)\n'
                                '    logs = tempfile.TemporaryFile(mode="w+")\n'
                                '    try:\n'
                                '        creation_attempted = True\n',
                                '    if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '        configure_group_terminal(env, args.suite)\n'
                                '        capture_group_terminal(args.suite)\n'
                                '    if args.suite == "computer-asset-export-v3":\n'
                                '        configure_computer_export(env)\n'
                                '        capture_computer_export()\n'
                                '    if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:\n'
                                '        configure_computer_terminal(env, args.suite)\n'
                                '        capture_computer_terminal(args.suite)\n'
                                '    logs = tempfile.TemporaryFile(mode="w+")\n'
                                '    try:\n'
                                '        creation_attempted = True\n'),
                               ('                                           '
                                'directory_v4=args.suite in DIRECTORY_V4_SUITES,\n'
                                '                                           '
                                'group_members_v4_pagination=args.suite == '
                                '"group-members-v4-pagination",\n'
                                '                                           '
                                'group_members_v4_refresh=args.suite == '
                                '"group-members-v4-refresh",\n'
                                '                                           '
                                'computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES,\n'
                                '                                           '
                                'group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | '
                                'GROUP_EXPORT_SUITES,\n'
                                '                                           '
                                'user_assets_v2=args.suite in USER_ASSETS_V2_SUITES,\n'
                                '                                           '
                                'user_export_failure_v3=args.suite == '
                                '"user-asset-export-v3-failure",\n'
                                '                                           '
                                'group_export_failure_v3=args.suite == '
                                '"group-asset-export-v3-failure")\n'
                                '                ldap_fixture.start(env)\n'
                                '                if args.suite in DIRECTORY_SUITES:\n'
                                '                    env["ADTR_DIRECTORY_READ_ENABLED"] = "true"\n',
                                '                                           '
                                'directory_v4=args.suite in DIRECTORY_V4_SUITES,\n'
                                '                                           '
                                'group_members_v4_pagination=args.suite == '
                                '"group-members-v4-pagination",\n'
                                '                                           '
                                'group_members_v4_refresh=args.suite == '
                                '"group-members-v4-refresh",\n'
                                '                                           '
                                'computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES | '
                                'COMPUTER_EXPORT_SUITES,\n'
                                '                                           '
                                'group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | '
                                'GROUP_EXPORT_SUITES,\n'
                                '                                           '
                                'user_assets_v2=args.suite in USER_ASSETS_V2_SUITES,\n'
                                '                                           '
                                'user_export_failure_v3=args.suite == '
                                '"user-asset-export-v3-failure",\n'
                                '                                           '
                                'group_export_failure_v3=args.suite == '
                                '"group-asset-export-v3-failure",\n'
                                '                                           '
                                'computer_export_failure_v3=args.suite == '
                                '"computer-asset-export-v3-failure")\n'
                                '                ldap_fixture.start(env)\n'
                                '                if args.suite in DIRECTORY_SUITES:\n'
                                '                    env["ADTR_DIRECTORY_READ_ENABLED"] = '
                                '"true"\n'),
                               ('            if args.suite == "messages-bulk":\n'
                                '                # Identify only our worker connections for '
                                'quiescent SIGSTOP tests.\n'
                                '                worker_env["ADTR_DATABASE_URL"] += '
                                '"&application_name=adtr-message-bulk-worker"\n'
                                '            if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel"}:\n'
                                '                export_worker_nonce = uuid.uuid4().hex\n'
                                '                export_worker_app = "adtr-user-export-" + '
                                'export_worker_nonce\n'
                                '                worker_env["ADTR_E2E_EXPORT_WORKER_NONCE"] = '
                                'export_worker_nonce\n',
                                '            if args.suite == "messages-bulk":\n'
                                '                # Identify only our worker connections for '
                                'quiescent SIGSTOP tests.\n'
                                '                worker_env["ADTR_DATABASE_URL"] += '
                                '"&application_name=adtr-message-bulk-worker"\n'
                                '            if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", '
                                '"computer-asset-export-v3-cancel"}:\n'
                                '                export_worker_nonce = uuid.uuid4().hex\n'
                                '                export_worker_app = "adtr-user-export-" + '
                                'export_worker_nonce\n'
                                '                worker_env["ADTR_E2E_EXPORT_WORKER_NONCE"] = '
                                'export_worker_nonce\n'),
                               ('                worker_env.pop(key, None)\n'
                                '            diagnostics.phase("worker-start")\n'
                                '            worker = subprocess.Popen(["./bin/adtr", "-mode", '
                                '"worker"], env=worker_env, stdout=logs, stderr=logs)\n'
                                '            if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel"}:\n'
                                '                export_worker_guard = '
                                'OwnedExportWorkerGuard(worker, export_worker_nonce, '
                                'Path("bin/adtr").resolve())\n'
                                '                export_worker_guard.start()\n'
                                '                env.update(ADTR_E2E_WORKER_PID=str(worker.pid), '
                                'ADTR_E2E_HARNESS_PID=str(os.getpid()),\n',
                                '                worker_env.pop(key, None)\n'
                                '            diagnostics.phase("worker-start")\n'
                                '            worker = subprocess.Popen(["./bin/adtr", "-mode", '
                                '"worker"], env=worker_env, stdout=logs, stderr=logs)\n'
                                '            if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", '
                                '"computer-asset-export-v3-cancel"}:\n'
                                '                export_worker_guard = '
                                'OwnedExportWorkerGuard(worker, export_worker_nonce, '
                                'Path("bin/adtr").resolve())\n'
                                '                export_worker_guard.start()\n'
                                '                env.update(ADTR_E2E_WORKER_PID=str(worker.pid), '
                                'ADTR_E2E_HARNESS_PID=str(os.getpid()),\n'),
                               ('        # enter an error-context prompt; explicit safe '
                                'screenshots remain intact.\n'
                                '        env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"\n'
                                '        diagnostics.phase("browser-start")\n'
                                '        subprocess.run(command, env=env, check=True, timeout=930 '
                                'if args.suite in GROUP_EXPORT_SUITES else 930 if args.suite in '
                                'EXPORT_TERMINAL_SUITES else 930 if args.suite == '
                                '"user-asset-export-v3" else 660 if args.suite == '
                                '"account-references" else 930 if args.suite in '
                                'EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | '
                                '{"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | '
                                'GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", '
                                '"directory-v2", "directory-v3-backend", "directory-v4-backend", '
                                '"group-members-v4", "group-members-v4-empty", '
                                '"group-members-v4-pagination", "group-members-v4-refresh", '
                                '"directory-v3"} else 600 if args.suite == "audit" else 540 if '
                                'args.suite in {"maintenance", "domains", "operations", '
                                '"credential-use", "account-references", "operational-logs", '
                                '"account-reference-barrier"} else 480 if args.suite == "system" '
                                'else 420)\n'
                                '        if args.suite == "user-asset-export-v3":\n'
                                '            validate_user_export()\n'
                                '        if export_worker_guard is not None:\n',
                                '        # enter an error-context prompt; explicit safe '
                                'screenshots remain intact.\n'
                                '        env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"\n'
                                '        diagnostics.phase("browser-start")\n'
                                '        subprocess.run(command, env=env, check=True, timeout=930 '
                                'if args.suite in COMPUTER_EXPORT_SUITES else 930 if args.suite in '
                                'GROUP_EXPORT_SUITES else 930 if args.suite in '
                                'EXPORT_TERMINAL_SUITES else 930 if args.suite == '
                                '"user-asset-export-v3" else 660 if args.suite == '
                                '"account-references" else 930 if args.suite in '
                                'EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | '
                                '{"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | '
                                'GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", '
                                '"directory-v2", "directory-v3-backend", "directory-v4-backend", '
                                '"group-members-v4", "group-members-v4-empty", '
                                '"group-members-v4-pagination", "group-members-v4-refresh", '
                                '"directory-v3"} else 600 if args.suite == "audit" else 540 if '
                                'args.suite in {"maintenance", "domains", "operations", '
                                '"credential-use", "account-references", "operational-logs", '
                                '"account-reference-barrier"} else 480 if args.suite == "system" '
                                'else 420)\n'
                                '        if args.suite == "user-asset-export-v3":\n'
                                '            validate_user_export()\n'
                                '        if export_worker_guard is not None:\n'),
                               ('            validate_group_export()\n'
                                '        if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '            validate_group_terminal(args.suite)\n'
                                '        validate_asset_refresh_evidence(args.suite)\n'
                                '        if args.suite == "group-members-v4":\n'
                                '            subprocess.run([sys.executable, '
                                '"scripts/validate_group_members_v4_evidence.py",\n',
                                '            validate_group_export()\n'
                                '        if args.suite in GROUP_EXPORT_TERMINAL_SUITES:\n'
                                '            validate_group_terminal(args.suite)\n'
                                '        if args.suite == "computer-asset-export-v3":\n'
                                '            validate_computer_export()\n'
                                '        if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:\n'
                                '            validate_computer_terminal(args.suite)\n'
                                '        validate_asset_refresh_evidence(args.suite)\n'
                                '        if args.suite == "group-members-v4":\n'
                                '            subprocess.run([sys.executable, '
                                '"scripts/validate_group_members_v4_evidence.py",\n')])}


def restore_computer_only(text, name):
    for old, new in reversed(CHANGES[name][1]):
        if text.count(new) != 1:
            raise ValueError('computer export addition absent or altered: ' + name)
        text = text.replace(new, old)
    return text


def restore(text, name):
    return restore_computer_only(restore_running_cancel(text, name), name)


def restore_runner(text):
    return restore(text, 'scripts/test_auth_e2e.py')


def restore_workflow(text):
    return restore(text, '.github/workflows/ci.yml')


class ComputerExportRunnerContract(unittest.TestCase):
    def test_preserve_all_65_jobs_and_runner_bytes(self):
        for name, (digest, _) in CHANGES.items():
            self.assertEqual(hashlib.sha256(restore(reviewed_source(name), name).encode()).hexdigest(), digest)

    def test_68_jobs_and_unchanged_limits(self):
        source = restore_running_cancel(reviewed_workflow(), '.github/workflows/ci.yml')
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', source, re.M)
        self.assertEqual(len(names), 48)
        self.assertEqual(len(names) + 8 + 7 + 5, 68)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', source), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', source)
        for suffix in ('', '-failure', '-cancel'):
            self.assertEqual(names.count('computer-asset-export-v3' + suffix), 1)

    def test_modified_or_duplicate_delta_fails(self):
        for name, (_, changes) in CHANGES.items():
            source = restore_running_cancel(reviewed_source(name), name)
            for _, new in changes:
                with self.assertRaises(ValueError):
                    restore_computer_only(source + new, name)
                with self.assertRaises(ValueError):
                    restore_computer_only(source.replace(new, '!' + new[1:]), name)


if __name__ == '__main__':
    unittest.main()
