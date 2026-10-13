"""One independent pagination dictionary4 suite; preserve all 57 prior jobs byte-for-byte.

Static/unit checks establish the intended 58-job shape, not executed browser CI.
"""
import ast
import hashlib
import re
from pathlib import Path
import unittest
import test_auth_e2e as runner
from test_group_members_v4_refresh_runner_contract import (
    restore_workflow as restore_refresh_workflow, restore_runner as restore_refresh_runner,
)

BASE_WORKFLOW_SHA256 = 'afe5ee8b0cef0fe72b9d9b07ef1674d84dec7b1b474eb05f897ff4db7118049a'
BASE_RUNNER_SHA256 = '629b3258daa15aac2c559d9b5a700c7d82055950ce759dbf71f2d7a344d3ecc8'
ENTRY = '''          - name: group-members-v4-pagination
            command: python3 scripts/test_auth_e2e.py --suite group-members-v4-pagination
'''
VALIDATE = '''        if args.suite == "group-members-v4-pagination":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_pagination_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
'''
WORKFLOW_BLOCK = '''      - name: Validate paginated group membership dictionary4 browser evidence
        if: ${{ success() && matrix.name == 'group-members-v4-pagination' }}
        run: python3 scripts/validate_group_members_v4_pagination_evidence.py --source-root .
      - name: Retain paginated group membership dictionary4 browser evidence
        if: ${{ always() && matrix.name == 'group-members-v4-pagination' }}
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: group-members-v4-pagination-${{ github.run_id }}-${{ github.run_attempt }}
          path: |
            web/test-results/**/group-members-v4-pagination-evidence.json
            web/test-results/**/group-members-v4-pagination-reader-1440-discovery.png
            web/test-results/**/group-members-v4-pagination-reader-1440-next.png
            web/test-results/**/group-members-v4-pagination-reader-1440-detail.png
            web/test-results/**/group-members-v4-pagination-reader-1440-previous.png
            web/test-results/**/group-members-v4-pagination-reader-1440-size50.png
            web/test-results/**/group-members-v4-pagination-reader-1440-size25.png
            web/test-results/**/group-members-v4-pagination-reader-1440-reread.png
            web/test-results/**/group-members-v4-pagination-reader-390-discovery.png
            web/test-results/**/group-members-v4-pagination-reader-390-next.png
            web/test-results/**/group-members-v4-pagination-reader-390-detail.png
            web/test-results/**/group-members-v4-pagination-reader-390-previous.png
            web/test-results/**/group-members-v4-pagination-reader-390-size50.png
            web/test-results/**/group-members-v4-pagination-reader-390-size25.png
            web/test-results/**/group-members-v4-pagination-reader-390-reread.png
          # Strict success validation above requires JSON and all 14 step/viewport PNGs.
          # Keep partial failure evidence without masking the browser error.
          if-no-files-found: warn
          retention-days: 14
'''

FIXTURE_FLAG = '                                           group_members_v4_pagination=args.suite == "group-members-v4-pagination",\n'

def remove_exact(text, addition, count=1):
    if text.count(addition) != count:
        raise ValueError('pagination suite addition missing, duplicated or modified')
    return text.replace(addition, '')


def restore_workflow(text):
    text = restore_refresh_workflow(text)
    return remove_exact(remove_exact(text, ENTRY), WORKFLOW_BLOCK)


def restore_runner(text):
    text = restore_refresh_runner(text)
    text = remove_exact(text, VALIDATE)
    text = remove_exact(text, ', "group-members-v4-pagination"', 3)
    return remove_exact(text, FIXTURE_FLAG)


class PaginationGroupMembersRunnerContract(unittest.TestCase):
    def test_original_57_jobs_and_runner_are_byte_exact(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        source = Path('scripts/test_auth_e2e.py').read_text()
        self.assertEqual(hashlib.sha256(restore_workflow(workflow).encode()).hexdigest(), BASE_WORKFLOW_SHA256)
        self.assertEqual(hashlib.sha256(restore_runner(source).encode()).hexdigest(), BASE_RUNNER_SHA256)
        self.assertNotIn('continue-on-error', workflow)
        self.assertNotIn('include-hidden-files', WORKFLOW_BLOCK)

    def test_intended_59_jobs_and_existing_parallelism(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        jobs = workflow.split('jobs:\n', 1)[1]
        blocks = dict(re.findall(r'^  ([a-z][a-z-]*):\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', jobs, re.M | re.S))
        self.assertEqual(set(blocks), {'prepare-postgres', 'contracts', 'auth-integration',
                                      'browser', 'native-no-store', 'native-no-store-fixed',
                                      'user-assets-fixed', 'verify'})
        browser_suites = re.findall(r'^          - name: ([a-z0-9-]+)$', blocks['browser'], re.M)
        self.assertEqual(len(browser_suites), len(set(browser_suites)))
        self.assertEqual(len(browser_suites), 39)
        self.assertEqual(browser_suites.count('group-members-v4-pagination'), 1)
        shards = re.search(r'^        shard: \[(.*?)\]$', blocks['auth-integration'], re.M).group(1).split(', ')
        fixed = re.search(r'^        suite: \[(.*?)\]$', blocks['user-assets-fixed'], re.M).group(1).split(', ')
        self.assertEqual(len(shards), 8)
        self.assertEqual(len(fixed), 7)
        self.assertNotIn('group-members-v4-pagination', fixed)
        self.assertEqual(5 + len(shards) + len(browser_suites) + len(fixed), 59)
        for job, budget in [('browser', 8), ('auth-integration', 2), ('user-assets-fixed', 2)]:
            self.assertEqual(re.findall(r'^      max-parallel: (\d+)$', blocks[job], re.M), [str(budget)])
        self.assertEqual(len(re.findall(r'^            web/test-results/.*\.png$', WORKFLOW_BLOCK, re.M)), 14)

    def test_mutated_additions_fail_closed(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        for old, new in [('success() && matrix.name', 'always() && matrix.name'),
                         ('always() && matrix.name', 'success() && matrix.name'),
                         ('retention-days: 14', 'retention-days: 2'),
                         ('--source-root .', '--source-root elsewhere'),
                         ('group-members-v4-pagination-reader-390-reread.png', 'missing.png')]:
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
        for old, new in [('check=True', 'check=False'), ('timeout=30', 'timeout=300')]:
            with self.assertRaises(ValueError):
                restore_runner(source.replace(VALIDATE, VALIDATE.replace(old, new)))

    def test_pagination_fixture_is_v4_without_legacy_timeout_modes(self):
        suite = 'group-members-v4-pagination'
        self.assertIn(suite, runner.DIRECTORY_V4_SUITES)
        self.assertIn(suite, runner.DIRECTORY_SUITES)
        for modes in [runner.DIRECTORY_V3_SUITES, runner.DIRECTORY_V2_SUITES,
                      runner.EMPTY_DIRECTORY_SUITES, runner.EMPTY_TRANSITION_SUITES,
                      runner.REAL_TAB_LIFECYCLE_SUITES, runner.USER_ASSETS_V2_SUITES,
                      runner.COMPUTER_ASSETS_V3_SUITES, runner.GROUP_ASSETS_V3_SUITES]:
            self.assertNotIn(suite, modes)
        self.assertEqual(runner.browser_command(suite),
                         ['npm', 'run', 'test:e2e', '--prefix', 'web', '--', 'e2e/group-members-v4-pagination.spec.ts'])
        source = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        fixture = next(n for n in ast.walk(tree) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == 'LDAPFixture')
        keywords = {kw.arg: kw.value for kw in fixture.keywords}
        from types import SimpleNamespace
        for candidate, expected in [('group-members-v4-pagination', True), ('group-members-v4', False), ('directory-v4-backend', False), ('directory-v3-controls', False), ('group-members-v4-empty', False)]:
            context = dict(vars(runner), args=SimpleNamespace(suite=candidate))
            actual = eval(compile(ast.Expression(keywords['group_members_v4_pagination']), '<fixture>', 'eval'), context)
            self.assertEqual(actual, expected)
        context = dict(vars(runner), args=SimpleNamespace(suite=suite))
        for key in ['directory_enabled', 'directory_v4']:
            self.assertTrue(eval(compile(ast.Expression(keywords[key]), '<fixture>', 'eval'), context))
        for key in set(keywords) - {'directory_enabled', 'directory_v4', 'group_members_v4_pagination'}:
            self.assertFalse(eval(compile(ast.Expression(keywords[key]), '<fixture>', 'eval'), context), key)
        choices = next(kw.value for n in ast.walk(tree) if isinstance(n, ast.Call) for kw in n.keywords if kw.arg == 'choices')
        self.assertEqual(ast.literal_eval(choices).count(suite), 1)
        for candidate in ast.literal_eval(choices):
            context = dict(vars(runner), args=SimpleNamespace(suite=candidate))
            self.assertEqual(eval(compile(ast.Expression(keywords['group_members_v4_pagination']), '<fixture>', 'eval'), context), candidate == suite)

    def test_validator_dispatch_is_exclusive_and_propagates_failure(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        import subprocess
        import sys
        statement = compile(VALIDATE.lstrip(), '<validator>', 'exec')
        for suite in ['group-members-v4-pagination', 'group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            run = Mock()
            context = dict(args=SimpleNamespace(suite=suite),
                           subprocess=SimpleNamespace(run=run), sys=sys)
            exec(statement, context)
            if suite == 'group-members-v4-pagination':
                run.assert_called_once_with(
                    [sys.executable, 'scripts/validate_group_members_v4_pagination_evidence.py', '--source-root', '.'],
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
        validator = next(i for i, n in enumerate(owned.body) if isinstance(n, ast.If) and ast.get_source_segment(source, n.test) == 'args.suite == "group-members-v4-pagination"')
        self.assertGreater(validator, browser)
        call = owned.body[browser].value
        self.assertIs(next(kw.value.value for kw in call.keywords if kw.arg == 'check'), True)
        timeout = next(kw.value for kw in call.keywords if kw.arg == 'timeout')
        from types import SimpleNamespace
        for suite in ['group-members-v4-pagination', 'group-members-v4-empty', 'group-members-v4', 'directory-v4-backend']:
            self.assertEqual(eval(compile(ast.Expression(timeout), '<timeout>', 'eval'), dict(vars(runner), args=SimpleNamespace(suite=suite))), 660)
        self.assertNotIn('validate_group_members_v4_pagination_evidence', '\n'.join(ast.get_source_segment(source, n) for n in owned.finalbody))
        spec = Path('web/e2e/group-members-v4-pagination.spec.ts').read_text()
        self.assertIn('test.setTimeout(630_000)', spec)


if __name__ == '__main__':
    unittest.main()
