"""One independent refresh dictionary4 suite; preserve all 58 prior jobs byte-for-byte.

Static/unit checks establish the intended 59-job shape, not executed browser CI.
"""
import ast
import hashlib
import re
from pathlib import Path
import unittest
import test_auth_e2e as runner
from test_user_asset_export_v3_runner_contract import restore_workflow as restore_export_workflow, restore_runner as restore_export_runner

BASE_WORKFLOW_SHA256 = 'd2644589c12d6e3a2f73725a7842d03201b13d70e6dd43cf67c25b123174a5ac'
BASE_RUNNER_SHA256 = 'b3d15dbb4ca5932589fbdda2ddccfad7d57b9a7be69a1fa654683069a7c74de3'
ENTRY = '''          - name: group-members-v4-refresh
            command: python3 scripts/test_auth_e2e.py --suite group-members-v4-refresh
'''
VALIDATE = '''        if args.suite == "group-members-v4-refresh":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_refresh_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
'''
WORKFLOW_BLOCK = '''      - name: Validate refreshed group membership dictionary4 browser evidence
        if: ${{ success() && matrix.name == 'group-members-v4-refresh' }}
        run: python3 scripts/validate_group_members_v4_refresh_evidence.py --source-root .
      - name: Retain refreshed group membership dictionary4 browser evidence
        if: ${{ always() && matrix.name == 'group-members-v4-refresh' }}
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: group-members-v4-refresh-${{ github.run_id }}-${{ github.run_attempt }}
          path: |
            web/test-results/**/group-members-v4-refresh-evidence.json
            web/test-results/**/group-members-v4-refresh-reader-1440-initial.png
            web/test-results/**/group-members-v4-refresh-reader-1440.png
            web/test-results/**/group-members-v4-refresh-reader-390.png
          # Strict success validation above requires JSON and all three PNGs.
          # Keep partial failure evidence without masking the browser error.
          if-no-files-found: warn
          retention-days: 14
'''

FIXTURE_FLAG = '                                           group_members_v4_refresh=args.suite == "group-members-v4-refresh",\n'

def remove_exact(text, addition, count=1):
    if text.count(addition) != count:
        raise ValueError('refresh suite addition missing, duplicated or modified')
    return text.replace(addition, '')


def restore_workflow(text):
    text = restore_export_workflow(text)
    return remove_exact(remove_exact(text, ENTRY), WORKFLOW_BLOCK)


def restore_runner(text):
    text = restore_export_runner(text)
    text = remove_exact(text, VALIDATE)
    text = remove_exact(text, ', "group-members-v4-refresh"', 3)
    text = remove_exact(text, ' or args.suite == "group-members-v4-refresh"')
    return remove_exact(text, FIXTURE_FLAG)


class RefreshGroupMembersRunnerContract(unittest.TestCase):
    def test_original_58_jobs_and_runner_are_byte_exact(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        source = Path('scripts/test_auth_e2e.py').read_text()
        self.assertEqual(hashlib.sha256(restore_workflow(workflow).encode()).hexdigest(), BASE_WORKFLOW_SHA256)
        self.assertEqual(hashlib.sha256(restore_runner(source).encode()).hexdigest(), BASE_RUNNER_SHA256)
        self.assertNotIn('continue-on-error', workflow)
        self.assertNotIn('include-hidden-files', WORKFLOW_BLOCK)

    def test_intended_59_jobs_and_existing_parallelism(self):
        workflow = restore_export_workflow(Path('.github/workflows/ci.yml').read_text())
        jobs = workflow.split('jobs:\n', 1)[1]
        blocks = dict(re.findall(r'^  ([a-z][a-z-]*):\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', jobs, re.M | re.S))
        self.assertEqual(set(blocks), {'prepare-postgres', 'contracts', 'auth-integration',
                                      'browser', 'native-no-store', 'native-no-store-fixed',
                                      'user-assets-fixed', 'verify'})
        browser_suites = re.findall(r'^          - name: ([a-z0-9-]+)$', blocks['browser'], re.M)
        self.assertEqual(len(browser_suites), len(set(browser_suites)))
        self.assertEqual(len(browser_suites), 39)
        self.assertEqual(browser_suites.count('group-members-v4-refresh'), 1)
        shards = re.search(r'^        shard: \[(.*?)\]$', blocks['auth-integration'], re.M).group(1).split(', ')
        fixed = re.search(r'^        suite: \[(.*?)\]$', blocks['user-assets-fixed'], re.M).group(1).split(', ')
        self.assertEqual(len(shards), 8)
        self.assertEqual(len(fixed), 7)
        self.assertNotIn('group-members-v4-refresh', fixed)
        self.assertEqual(5 + len(shards) + len(browser_suites) + len(fixed), 59)
        for job, budget in [('browser', 8), ('auth-integration', 2), ('user-assets-fixed', 2)]:
            self.assertEqual(re.findall(r'^      max-parallel: (\d+)$', blocks[job], re.M), [str(budget)])
        self.assertEqual(len(re.findall(r'^            web/test-results/.*\.png$', WORKFLOW_BLOCK, re.M)), 3)

    def test_mutated_additions_fail_closed(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        for old, new in [('success() && matrix.name', 'always() && matrix.name'),
                         ('always() && matrix.name', 'success() && matrix.name'),
                         ('retention-days: 14', 'retention-days: 2'),
                         ('--source-root .', '--source-root elsewhere'),
                         ('group-members-v4-refresh-reader-390.png', 'missing.png')]:
            with self.subTest(change=(old, new)), self.assertRaises(ValueError):
                restore_workflow(workflow.replace(WORKFLOW_BLOCK, WORKFLOW_BLOCK.replace(old, new)))
        for extra in [ENTRY, WORKFLOW_BLOCK]:
            with self.assertRaises(ValueError):
                restore_workflow(workflow + extra)
        source = Path('scripts/test_auth_e2e.py').read_text()
        for extra in [VALIDATE, FIXTURE_FLAG]:
            with self.assertRaises(ValueError):
                restore_runner(source + extra)
        with self.assertRaises(ValueError):
            restore_runner(source.replace(FIXTURE_FLAG, FIXTURE_FLAG.replace('==', '!=')))
        with self.assertRaises(ValueError):
            restore_runner(source.replace(' or args.suite == "group-members-v4-refresh"',
                                          ' or args.suite != "group-members-v4-refresh"'))
        for old, new in [('check=True', 'check=False'), ('timeout=30', 'timeout=300')]:
            with self.assertRaises(ValueError):
                restore_runner(source.replace(VALIDATE, VALIDATE.replace(old, new)))

    def test_refresh_fixture_is_v4_without_legacy_timeout_modes(self):
        suite = 'group-members-v4-refresh'
        self.assertIn(suite, runner.DIRECTORY_V4_SUITES)
        self.assertIn(suite, runner.DIRECTORY_SUITES)
        for modes in [runner.DIRECTORY_V3_SUITES, runner.DIRECTORY_V2_SUITES,
                      runner.EMPTY_DIRECTORY_SUITES, runner.EMPTY_TRANSITION_SUITES,
                      runner.REAL_TAB_LIFECYCLE_SUITES, runner.USER_ASSETS_V2_SUITES,
                      runner.COMPUTER_ASSETS_V3_SUITES, runner.GROUP_ASSETS_V3_SUITES]:
            self.assertNotIn(suite, modes)
        self.assertEqual(runner.browser_command(suite),
                         ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/group-members-v4-refresh.spec.ts'])
        source = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        fixture = next(n for n in ast.walk(tree) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == 'LDAPFixture')
        keywords = {kw.arg: kw.value for kw in fixture.keywords}
        from types import SimpleNamespace
        for candidate, expected in [('group-members-v4-refresh', True), ('group-members-v4', False), ('directory-v4-backend', False), ('directory-v3-controls', False), ('group-members-v4-empty', False)]:
            context = dict(vars(runner), args=SimpleNamespace(suite=candidate))
            actual = eval(compile(ast.Expression(keywords['group_members_v4_refresh']), '<fixture>', 'eval'), context)
            self.assertEqual(actual, expected)
        context = dict(vars(runner), args=SimpleNamespace(suite=suite))
        for key in ['directory_enabled', 'directory_v4', 'empty_transition']:
            self.assertTrue(eval(compile(ast.Expression(keywords[key]), '<fixture>', 'eval'), context))
        for key in set(keywords) - {'directory_enabled', 'directory_v4', 'empty_transition', 'group_members_v4_refresh'}:
            self.assertFalse(eval(compile(ast.Expression(keywords[key]), '<fixture>', 'eval'), context), key)
        choices = next(kw.value for n in ast.walk(tree) if isinstance(n, ast.Call) for kw in n.keywords if kw.arg == 'choices')
        self.assertEqual(ast.literal_eval(choices).count(suite), 1)
        for candidate in ast.literal_eval(choices):
            context = dict(vars(runner), args=SimpleNamespace(suite=candidate))
            self.assertEqual(eval(compile(ast.Expression(keywords['group_members_v4_refresh']), '<fixture>', 'eval'), context), candidate == suite)

    def test_refresh_does_not_dispatch_legacy_asset_validator(self):
        from unittest.mock import patch
        with patch.object(runner.subprocess, 'run') as run:
            runner.validate_asset_refresh_evidence('group-members-v4-refresh')
            run.assert_not_called()

    def test_validator_dispatch_is_exclusive_and_propagates_failure(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        import subprocess
        import sys
        statement = compile(VALIDATE.lstrip(), '<validator>', 'exec')
        for suite in ['group-members-v4-refresh', 'group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            run = Mock()
            context = dict(args=SimpleNamespace(suite=suite),
                           subprocess=SimpleNamespace(run=run), sys=sys)
            exec(statement, context)
            if suite == 'group-members-v4-refresh':
                run.assert_called_once_with(
                    [sys.executable, 'scripts/validate_group_members_v4_refresh_evidence.py', '--source-root', '.'],
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
        validator = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.If) and ast.get_source_segment(source, n.test) == 'args.suite == "group-members-v4-refresh"')
        self.assertGreater(validator, browser)
        call = owned.body[browser].value
        self.assertIs(next(kw.value.value for kw in call.keywords if kw.arg == 'check'), True)
        timeout = next(kw.value for kw in call.keywords if kw.arg == 'timeout')
        from types import SimpleNamespace
        for suite in ['group-members-v4-refresh', 'group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            self.assertEqual(eval(compile(ast.Expression(timeout), '<timeout>', 'eval'), dict(vars(runner), args=SimpleNamespace(suite=suite))), 660)
        self.assertNotIn('validate_group_members_v4_refresh_evidence', '\n'.join(ast.get_source_segment(source, n) for n in owned.finalbody))
        spec = Path('web/e2e/group-members-v4-refresh.spec.ts').read_text()
        self.assertIn('test.setTimeout(630_000)', spec)


if __name__ == '__main__':
    unittest.main()
