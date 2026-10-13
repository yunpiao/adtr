"""Add one real primary membership suite; preserve all 55 prior job bytes.

These are static/unit contracts. Intended 56 jobs is not an executed CI result.
"""
import ast
import hashlib
from pathlib import Path
import unittest
import test_auth_e2e as runner

BASE_WORKFLOW_SHA256 = 'd2d4f3bd2c783c137e1c9195ba1c865d6bfe2eb1df54aa2347dff993dc187295'
BASE_RUNNER_SHA256 = 'c928e95aa401f6d6ec5a84b7cfe6a40f84311f4a5a3cc7075d76f1c44e19d56d'
ENTRY = '          - name: group-members-v4\n            command: python3 scripts/test_auth_e2e.py --suite group-members-v4\n'
VALIDATE = '''        if args.suite == "group-members-v4":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
'''


class GroupMembersRunnerContract(unittest.TestCase):
    def test_original_55_jobs_are_byte_exact_outside_addition(self):
        text = Path('.github/workflows/ci.yml').read_text()
        self.assertEqual(text.count(ENTRY), 1)
        start = text.index('      - name: Validate group membership dictionary4 browser evidence\n')
        end = text.index('      - name: Validate dictionary4 backend evidence\n', start)
        restored = (text[:start] + text[end:]).replace(ENTRY, '')
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), BASE_WORKFLOW_SHA256)
        block = text[start:end]
        self.assertIn("if: ${{ success() && matrix.name == 'group-members-v4' }}", block)
        self.assertIn('run: python3 scripts/validate_group_members_v4_evidence.py --source-root .', block)
        self.assertIn("if: ${{ always() && matrix.name == 'group-members-v4' }}", block)
        self.assertIn('actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02', block)
        self.assertIn('web/test-results/**/group-members-v4-evidence.json', block)
        self.assertIn('web/test-results/**/group-members-v4-*.png', block)
        self.assertIn('if-no-files-found: warn', block)
        self.assertNotIn('continue-on-error', text)
        self.assertNotIn('include-hidden-files', block)

    def test_original_runner_bytes_are_preserved(self):
        text = Path('scripts/test_auth_e2e.py').read_text()
        self.assertEqual(text.count(VALIDATE), 1)
        restored = text.replace(VALIDATE, '').replace(', "group-members-v4"', '')
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), BASE_RUNNER_SHA256)
        self.assertIn('group-members-v4', runner.DIRECTORY_V4_SUITES)
        self.assertIn('group-members-v4', runner.DIRECTORY_SUITES)
        for old_mode in [runner.DIRECTORY_V3_SUITES, runner.DIRECTORY_V2_SUITES,
                         runner.EMPTY_TRANSITION_SUITES, runner.EMPTY_DIRECTORY_SUITES,
                         runner.REAL_TAB_LIFECYCLE_SUITES]:
            self.assertNotIn('group-members-v4', old_mode)
        self.assertEqual(runner.browser_command('group-members-v4'),
                         ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/group-members-v4.spec.ts'])

    def test_validator_runs_only_after_browser_success_before_cleanup(self):
        text = Path('scripts/test_auth_e2e.py').read_text()
        # Both subprocesses fail closed. No validator in finally can replace
        # the originating browser exception; existing cleanup is byte-exact.
        body = next(n for n in ast.walk(ast.parse(text)) if isinstance(n, ast.FunctionDef) and n.name == 'main')
        owned = next(n for n in body.body if isinstance(n, ast.Try))
        browser = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.Expr) and 'subprocess.run(command,' in ast.get_source_segment(text, n))
        validator = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.If) and ast.get_source_segment(text, n.test) == 'args.suite == "group-members-v4"')
        self.assertGreater(validator, browser)
        self.assertIn('check=True', ast.get_source_segment(text, owned.body[browser]))
        self.assertNotIn('validate_group_members_v4_evidence', '\n'.join(ast.get_source_segment(text, n) for n in owned.finalbody))
        self.assertIn('"directory-v4-backend", "group-members-v4", "directory-v3"} else 600', text)


if __name__ == '__main__':
    unittest.main()
