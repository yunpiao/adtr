"""One independent empty dictionary4 suite; preserve all 56 prior jobs byte-for-byte.

Static/unit checks establish the intended 57-job shape, not executed browser CI.
"""
import ast
import hashlib
from pathlib import Path
import unittest
import test_auth_e2e as runner

BASE_WORKFLOW_SHA256 = 'f07de954603cc02cd0d6abe8924ac345b5cef0845c0cd151d106a0068a39ce35'
BASE_RUNNER_SHA256 = 'f4d91b748962c57dc5c5d4ef3cf1af66280f31175dbfceb2b264ba013120afac'
ENTRY = '''          - name: group-members-v4-empty
            command: python3 scripts/test_auth_e2e.py --suite group-members-v4-empty
'''
VALIDATE = '''        if args.suite == "group-members-v4-empty":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_empty_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
'''
WORKFLOW_BLOCK = '''      - name: Validate empty group membership dictionary4 browser evidence
        if: ${{ success() && matrix.name == 'group-members-v4-empty' }}
        run: python3 scripts/validate_group_members_v4_empty_evidence.py --source-root .
      - name: Retain empty group membership dictionary4 browser evidence
        if: ${{ always() && matrix.name == 'group-members-v4-empty' }}
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: group-members-v4-empty-${{ github.run_id }}-${{ github.run_attempt }}
          path: |
            web/test-results/**/group-members-v4-empty-evidence.json
            web/test-results/**/group-members-v4-empty-reader-1440.png
            web/test-results/**/group-members-v4-empty-reader-390.png
          # Strict success validation above requires JSON and both PNGs.
          # Keep partial failure evidence without masking the browser error.
          if-no-files-found: warn
          retention-days: 14
'''


def remove_exact(text, addition, count=1):
    if text.count(addition) != count:
        raise ValueError('empty suite addition missing, duplicated or modified')
    return text.replace(addition, '')


def restore_workflow(text):
    return remove_exact(remove_exact(text, ENTRY), WORKFLOW_BLOCK)


def restore_runner(text):
    text = remove_exact(text, VALIDATE)
    text = remove_exact(text, ', "group-members-v4-empty"', 3)
    return remove_exact(text, ' or args.suite == "group-members-v4-empty"')


class EmptyGroupMembersRunnerContract(unittest.TestCase):
    def test_original_56_jobs_and_runner_are_byte_exact(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        source = Path('scripts/test_auth_e2e.py').read_text()
        self.assertEqual(hashlib.sha256(restore_workflow(workflow).encode()).hexdigest(), BASE_WORKFLOW_SHA256)
        self.assertEqual(hashlib.sha256(restore_runner(source).encode()).hexdigest(), BASE_RUNNER_SHA256)
        self.assertNotIn('continue-on-error', workflow)
        self.assertNotIn('include-hidden-files', WORKFLOW_BLOCK)

    def test_mutated_additions_fail_closed(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        for old, new in [('success() && matrix.name', 'always() && matrix.name'),
                         ('always() && matrix.name', 'success() && matrix.name'),
                         ('retention-days: 14', 'retention-days: 2'),
                         ('--source-root .', '--source-root elsewhere'),
                         ('group-members-v4-empty-reader-390.png', 'missing.png')]:
            with self.subTest(change=(old, new)), self.assertRaises(ValueError):
                restore_workflow(workflow.replace(WORKFLOW_BLOCK, WORKFLOW_BLOCK.replace(old, new)))
        for extra in [ENTRY, WORKFLOW_BLOCK]:
            with self.assertRaises(ValueError):
                restore_workflow(workflow + extra)
        source = Path('scripts/test_auth_e2e.py').read_text()
        for old, new in [('check=True', 'check=False'), ('timeout=30', 'timeout=300')]:
            with self.assertRaises(ValueError):
                restore_runner(source.replace(VALIDATE, VALIDATE.replace(old, new)))

    def test_empty_fixture_is_v4_without_legacy_timeout_modes(self):
        suite = 'group-members-v4-empty'
        self.assertIn(suite, runner.DIRECTORY_V4_SUITES)
        self.assertIn(suite, runner.DIRECTORY_SUITES)
        for modes in [runner.DIRECTORY_V3_SUITES, runner.DIRECTORY_V2_SUITES,
                      runner.EMPTY_DIRECTORY_SUITES, runner.EMPTY_TRANSITION_SUITES,
                      runner.REAL_TAB_LIFECYCLE_SUITES, runner.USER_ASSETS_V2_SUITES,
                      runner.COMPUTER_ASSETS_V3_SUITES, runner.GROUP_ASSETS_V3_SUITES]:
            self.assertNotIn(suite, modes)
        self.assertEqual(runner.browser_command(suite),
                         ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/group-members-v4-empty.spec.ts'])
        source = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        fixture = next(n for n in ast.walk(tree) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == 'LDAPFixture')
        keywords = {kw.arg: kw.value for kw in fixture.keywords}
        from types import SimpleNamespace
        for candidate, expected in [('group-members-v4-empty', True), ('group-members-v4', False), ('directory-v4-backend', False), ('directory-v3-controls', True)]:
            context = dict(vars(runner), args=SimpleNamespace(suite=candidate))
            actual = eval(compile(ast.Expression(keywords['directory_empty']), '<fixture>', 'eval'), context)
            self.assertEqual(actual, expected)
        context = dict(vars(runner), args=SimpleNamespace(suite=suite))
        for key in ['directory_enabled', 'directory_v4']:
            self.assertTrue(eval(compile(ast.Expression(keywords[key]), '<fixture>', 'eval'), context))
        choices = next(kw.value for n in ast.walk(tree) if isinstance(n, ast.Call) for kw in n.keywords if kw.arg == 'choices')
        self.assertEqual(ast.literal_eval(choices).count(suite), 1)

    def test_validator_dispatch_is_exclusive_and_propagates_failure(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        import subprocess
        import sys
        statement = compile(VALIDATE.lstrip(), '<validator>', 'exec')
        for suite in ['group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            run = Mock()
            context = dict(args=SimpleNamespace(suite=suite),
                           subprocess=SimpleNamespace(run=run), sys=sys)
            exec(statement, context)
            if suite == 'group-members-v4-empty':
                run.assert_called_once_with(
                    [sys.executable, 'scripts/validate_group_members_v4_empty_evidence.py', '--source-root', '.'],
                    check=True, timeout=30)
                for error in [subprocess.CalledProcessError(1, 'validator'),
                              subprocess.TimeoutExpired('validator', 30)]:
                    run.side_effect = error
                    with self.assertRaises(type(error)):
                        exec(statement, context)
            else:
                run.assert_not_called()

    def test_browser_660_seconds_then_strict_validation_before_cleanup(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        body = next(n for n in ast.walk(ast.parse(source)) if isinstance(n, ast.FunctionDef) and n.name == 'main')
        owned = next(n for n in body.body if isinstance(n, ast.Try))
        browser = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.Expr) and 'subprocess.run(command,' in ast.get_source_segment(source, n))
        validator = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.If) and ast.get_source_segment(source, n.test) == 'args.suite == "group-members-v4-empty"')
        self.assertGreater(validator, browser)
        call = owned.body[browser].value
        self.assertIs(next(kw.value.value for kw in call.keywords if kw.arg == 'check'), True)
        timeout = next(kw.value for kw in call.keywords if kw.arg == 'timeout')
        from types import SimpleNamespace
        for suite in ['group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            self.assertEqual(eval(compile(ast.Expression(timeout), '<timeout>', 'eval'), dict(vars(runner), args=SimpleNamespace(suite=suite))), 660)
        self.assertNotIn('validate_group_members_v4_empty_evidence', '\n'.join(ast.get_source_segment(source, n) for n in owned.finalbody))
        spec = Path('web/e2e/group-members-v4-empty.spec.ts').read_text()
        self.assertIn('test.setTimeout(630_000)', spec)


if __name__ == '__main__':
    unittest.main()
