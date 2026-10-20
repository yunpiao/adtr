"""Actual discovery/registration contracts, separate from historical CI projections."""
import ast
import hashlib
import json
from pathlib import Path
import re
import unittest

from ci_workflow_contract import restore_directory_v4, restore_selection
import validate_directory_v4_controls_evidence as evidence


class DirectoryV4ControlsCI(unittest.TestCase):
    def test_exact_three_new_matrix_entries_and_all_legacy_entries(self):
        inventory = json.loads(Path('scripts/ci_inventory.json').read_text())
        expected = ['directory-v4-controls', 'directory-v4-cancel', 'directory-v4-readers']
        additions = [entry for entry in inventory['browser'] if entry['name'] in expected]
        self.assertEqual(additions, [{'name': suite, 'command': 'python3 scripts/test_auth_e2e.py --suite ' + suite, 'specs': ['web/e2e/' + suite + '.spec.ts']} for suite in expected])
        self.assertEqual(len(inventory['browser']), 54)
        self.assertEqual(len(inventory['fixed']), 7)
        self.assertEqual(len(inventory['frontend_roots']['web/src/TaskWorkspace.tsx']), 16)
        self.assertIn('audit', inventory['frontend_roots']['web/src/TaskWorkspace.tsx'])
        legacy = restore_selection(Path('.github/workflows/ci.yml').read_text())
        matrix = legacy.split('  browser:\n', 1)[1].split('    steps:\n', 1)[0]
        self.assertEqual([(entry['name'], entry['command']) for entry in inventory['browser'] if entry['name'] not in expected], re.findall(r'          - name: ([^\n]+)\n            command: ([^\n]+)', matrix))
        self.assertEqual(54 + 7 + 8 + 6, 75)  # plan, prepare, contracts, two native, verify.

    def test_runtime_commands_budgets_and_real_discovery(self):
        runner = Path('scripts/test_auth_e2e.py').read_text()
        syntax = ast.parse(runner)
        assignment = next(node for node in syntax.body if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == 'V4_CONTROLS_BUDGETS' for target in node.targets))
        self.assertEqual(ast.literal_eval(assignment.value), {'directory-v4-controls': 660, 'directory-v4-cancel': 930, 'directory-v4-readers': 660})
        for suite, seconds in [('controls', 630), ('cancel', 900), ('readers', 630)]:
            source = Path(f'web/e2e/directory-v4-{suite}.spec.ts').read_text()
            self.assertIn(f'test.setTimeout({seconds}_000)', source)
            self.assertIn('test.use({ trace: "off", screenshot: "off", video: "off" })', source)
            self.assertIn('await writeEvidence(info, "' + suite + '",', source)
        self.assertIn("python3 -m unittest discover -s scripts -p '*_contract.py'", Path('Makefile').read_text())
        for filename in ['test_directory_v4_controls_ci_contract.py', 'test_directory_v4_controls_evidence_contract.py', 'test_directory_v4_runner_contract.py', 'test_directory_v4_worker_control_contract.py']:
            self.assertTrue(Path('scripts', filename).is_file(), filename)
        self.assertIn('include: ["src/**/*.test.{ts,tsx}"]', Path('web/vite.config.ts').read_text())
        for filename in ['directory-v4-ledger-observer.test.ts', 'directory-v4-runtime-contract.test.ts']:
            self.assertTrue(Path('web/src', filename).is_file())

    def test_post_runner_validation_and_only_validated_allowlist(self):
        text = Path('.github/workflows/ci.yml').read_text()
        rules = evidence.contract(Path('.'))
        run = text.index('run: ${{ matrix.command }}')
        for suite in ['controls', 'cancel', 'readers']:
            full = 'directory-v4-' + suite
            start = text.index(f'      - name: Validate dictionary4 {suite} original-operation evidence')
            end = text.index('          retention-days: 7', start)
            block = text[start:end]
            self.assertGreater(start, run)
            self.assertIn(f"if: ${{{{ success() && matrix.name == '{full}' }}}}", block)
            self.assertIn(f'ADTR_V4_EVIDENCE_SUITE: {full}', block)
            self.assertIn('run: python3 scripts/validate_directory_v4_controls_evidence.py --source-root .', block)
            self.assertIn(f"steps.v4-{suite}-evidence.outcome == 'success'", block)
            paths = re.findall(r'^            (web/[^\n]+)$', block, re.M)
            self.assertEqual(paths, [f'web/playwright-report/{full}-validated/evidence.json'] + [f'web/playwright-report/{full}-validated/' + name for name in rules['suites'][suite]['screenshots']])
            self.assertNotIn('*', '\n'.join(paths))
            self.assertNotIn('always()', block)
            self.assertIn('if-no-files-found: error', block)
        delta = json.loads(Path('scripts/fixtures/directory_v4_ci_delta.json').read_text())
        self.assertEqual(hashlib.sha256(restore_directory_v4(text).encode()).hexdigest(), delta['baselineSHA256'])
        for change in delta['replacements']:
            with self.assertRaises(ValueError):
                restore_directory_v4(text.replace(change['new'], change['old'], 1))
        for before, after in [('contents: read', 'contents: write'), ('run: make check', 'run: true'), ('max-parallel: 8', 'max-parallel: 9')]:
            with self.assertRaises(ValueError):
                restore_directory_v4(text.replace(before, after, 1))

    def test_both_native_families_direct_exports_and_all_legacy_assertions(self):
        source = Path('web/e2e/native-no-store-completion.spec.ts').read_text()
        for entry in ['../src/directory-v2-json.ts', '../src/directory-v4-controls-json.ts', 'parser.readDirectoryV2JSON', 'parser.readDirectoryV4ControlsJSON', 'parser.directoryV2BodyLimit', 'parser.directoryV4ControlsBodyLimit']:
            self.assertIn(entry, source)
        self.assertIn('for (const productionReader of productionReaders)', source)
        self.assertIn('for (const kind of [', source)
        for kind in evidence.NATIVE_KINDS:
            self.assertIn('"' + kind + '"', source)
        for assertion in ['expect(consumed.readers).toBe(1)', 'expect(consumed.released).toBe(1)', 'expect(consumed.locked).toBe(false)', 'expect(consumed.eof).toBe(true)', 'expect(consumed.cancelCalls).toBe(0)', 'expect(nativeTerminalCount).toBe(1)', 'expect(consumed.retainedBytes).toBeLessThanOrEqual(limit)', 'expect(consumed.cancelBeforeEOF).toBe(true)']:
            self.assertIn(assertion, source)
        text = Path('.github/workflows/ci.yml').read_text()
        for job in ['native-no-store', 'native-no-store-fixed']:
            block = text.split('  ' + job + ':\n', 1)[1]
            block = re.split(r'^  [a-z][a-z-]+:\n', block, maxsplit=1, flags=re.M)[0]
            self.assertIn('run: xvfb-run -a npm run test:e2e --prefix web -- e2e/native-no-store-completion.spec.ts --headed', block)
            self.assertIn('run: python3 scripts/validate_directory_v4_controls_evidence.py --source-root . --native', block)
            self.assertLess(block.index('run: xvfb-run'), block.index('run: python3 scripts/validate_directory_v4_controls_evidence.py'))
            self.assertIn('timeout-minutes: 10', block)

    def test_source_rules_cover_all_owners_and_are_shared_with_independent_validator(self):
        rules = evidence.contract(Path('.'))
        self.assertEqual(rules['sourceRoots'], ['.github/workflows/', 'cmd/', 'internal/', 'scripts/', 'web/'])
        self.assertEqual(rules['sourceFiles'], ['Makefile', 'go.mod', 'go.sum', 'web/index.html', 'web/package-lock.json', 'web/package.json', 'web/playwright.config.ts', 'web/tsconfig.json', 'web/vite.config.ts'])
        helper = Path('web/e2e/directory-v4-controls-evidence.ts').read_text()
        self.assertIn('const contractPath = "' + evidence.CONTRACT_PATH + '"', helper)
        self.assertIn('git("ls-files", "-z")', helper)
        self.assertIn("git(root, 'ls-tree', '-r', '--name-only', '-z', 'HEAD')", Path('scripts/validate_directory_v4_controls_evidence.py').read_text())
        required = ['web/src/directory-v4-controls-api.ts', 'web/src/directory-v4-controls-json.ts', 'web/src/DirectoryV4Workspace.tsx', 'web/src/DirectoryV4TaskResult.tsx', 'web/test-helpers/navigation.ts', 'web/e2e/directory-v4-ledger-observer.ts', 'scripts/directory_v4_worker_control.py', 'scripts/test_auth_e2e.py', 'scripts/directory_e2e_credentials.py', 'web/playwright.config.ts', 'internal/auth/directory_v4_http.go']
        all_files = [path for path in evidence.git(Path('.'), 'ls-files', '-z').split('\0') if path]
        selected = evidence.source_paths(all_files, rules)
        for path in required:
            self.assertTrue(Path(path).is_file(), path)
            self.assertIn(path, selected)
        self.assertIn('unclassified dictionary4 consumer must retain full coverage', Path('scripts/ci_selection.py').read_text())


if __name__ == '__main__':
    unittest.main()
