"""Frozen additive delta: removal restores every byte of the prior 68-job runner/workflow."""
import hashlib
from pathlib import Path
import re
import unittest

CHANGES = {'scripts/test_auth_e2e.py': ('1a2bfa9b04c97aa93fc53189302595be14d6eeb3ef0f4be61fc0c453fa41f6db',
                              [('from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n',
                                'from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard\n'
                                'from asset_export_v3_running_cancel_guard import RunningExportCancelGuard\n'
                                'from user_asset_export_v3_running_cancel_ci import SUITE as '
                                'RUNNING_CANCEL_SUITE, configure as configure_running_cancel, capture as '
                                'capture_running_cancel, validate as validate_running_cancel\n',
                                1),
                               ('DIRECTORY_V3_SUITES = COMPUTER_EXPORT_SUITES',
                                'DIRECTORY_V3_SUITES = {RUNNING_CANCEL_SUITE} | COMPUTER_EXPORT_SUITES',
                                1),
                               ('choices=["computer-asset-export-v3"',
                                'choices=["user-asset-export-v3-running-cancel", "computer-asset-export-v3"',
                                1),
                               ('    export_worker_guard = None\n',
                                '    export_worker_guard = None\n    running_cancel_guard = None\n',
                                1),
                               ('    logs = tempfile.TemporaryFile(mode="w+")\n',
                                '    if args.suite == RUNNING_CANCEL_SUITE:\n'
                                '        configure_running_cancel(env)\n'
                                '        capture_running_cancel()\n'
                                '    logs = tempfile.TemporaryFile(mode="w+")\n',
                                1),
                               ('if args.suite in {"user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"}:',
                                'if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", '
                                '"group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"}:',
                                2),
                               ('                           '
                                'ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")\n',
                                '                           '
                                'ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")\n'
                                '                if args.suite == RUNNING_CANCEL_SUITE:\n'
                                '                    running_cancel_guard = RunningExportCancelGuard(worker, '
                                'export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, env["ADTR_DATABASE_URL"])\n'
                                '                    running_cancel_guard.start()\n'
                                '                    '
                                'env.update(ADTR_E2E_RUNNING_CANCEL_GUARD_URL=running_cancel_guard.url,\n'
                                '                               '
                                'ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN=running_cancel_guard.token)\n',
                                1),
                               ('timeout=930 if args.suite in COMPUTER_EXPORT_SUITES',
                                'timeout=930 if args.suite == RUNNING_CANCEL_SUITE else 930 if args.suite in '
                                'COMPUTER_EXPORT_SUITES',
                                1),
                               ('        if export_worker_guard is not None:\n'
                                '            export_worker_guard.check()\n',
                                '        if export_worker_guard is not None:\n'
                                '            export_worker_guard.check()\n'
                                '        if running_cancel_guard is not None:\n'
                                '            running_cancel_guard.check()\n'
                                '            validate_running_cancel()\n',
                                1),
                               ('            shutdown_error = None\n',
                                '            shutdown_error = None\n'
                                '            if running_cancel_guard is not None:\n'
                                '                try:\n'
                                '                    running_cancel_guard.close()\n'
                                '                except (OSError, RuntimeError):\n'
                                '                    shutdown_error = RuntimeError("running export worker '
                                'cleanup failed")\n',
                                1)]),
 '.github/workflows/ci.yml': ('567e2574bb5a4d0eb45a07c3327b235873ba43abfaeaef6427b433c8996d9fdf',
                              [("ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == 'computer-asset-export-v3'",
                                'ADTR_E2E_NATIVE_STREAM_MODE: ${{ (matrix.name == '
                                "'user-asset-export-v3-running-cancel' || matrix.name == "
                                "'computer-asset-export-v3'",
                                1),
                               ('          - name: user-asset-export-v3\n',
                                '          - name: user-asset-export-v3-running-cancel\n'
                                '            command: python3 scripts/test_auth_e2e.py --suite '
                                'user-asset-export-v3-running-cancel\n'
                                '          - name: user-asset-export-v3\n',
                                1),
                               ('      - name: Validate user asset export dictionary3 browser evidence\n',
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
                                '          retention-days: 14\n'
                                '      - name: Validate user asset export dictionary3 browser evidence\n',
                                1)])}

def restore(text, name):
    for old, new, count in reversed(CHANGES[name][1]):
        if text.count(new) != count:
            raise ValueError('running cancel addition absent or altered: ' + name)
        text = text.replace(new, old)
    return text

def restore_runner(text):
    return restore(text, 'scripts/test_auth_e2e.py')

def restore_workflow(text):
    return restore(text, '.github/workflows/ci.yml')

class RunningCancelRunnerContract(unittest.TestCase):
    def test_exact_prior_runner_and_workflow(self):
        for name, (digest, _) in CHANGES.items():
            self.assertEqual(hashlib.sha256(restore(Path(name).read_text(), name).encode()).hexdigest(), digest)

    def test_69_jobs_with_one_new_slice(self):
        source = Path('.github/workflows/ci.yml').read_text()
        names = re.findall(r'^          - name: ([a-z0-9-]+)$', source, re.M)
        self.assertEqual(len(names), 49)
        self.assertEqual(len(names) + 8 + 7 + 5, 69)
        self.assertEqual(names.count('user-asset-export-v3-running-cancel'), 1)
        self.assertEqual(re.findall(r'max-parallel: (\d+)', source), ['2', '8', '2'])
        self.assertNotIn('continue-on-error', source)

    def test_mutations_and_duplicates_fail(self):
        for name, (_, changes) in CHANGES.items():
            source = Path(name).read_text()
            for _, new, _ in changes:
                with self.assertRaises(ValueError):
                    restore(source + new, name)
                with self.assertRaises(ValueError):
                    restore(source.replace(new, '!' + new[1:]), name)

if __name__ == '__main__':
    unittest.main()
