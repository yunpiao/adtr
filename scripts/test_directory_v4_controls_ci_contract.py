"""Actual discovery/registration contracts, separate from historical CI projections."""
import ast
import hashlib
import json
from pathlib import Path
import re
import unittest

from ci_workflow_contract import (restore_directory_v4, restore_selection,
                                  restore_health_recovery, restore_directory_v4_predecessor)
import validate_directory_v4_controls_evidence as evidence


class DirectoryV4ControlsCI(unittest.TestCase):
    def test_exact_three_new_matrix_entries_and_all_legacy_entries(self):
        inventory = json.loads(Path('scripts/ci_inventory.json').read_text())
        expected = ['directory-v4-controls', 'directory-v4-cancel', 'directory-v4-readers']
        additions = [entry for entry in inventory['browser'] if entry['name'] in expected]
        self.assertEqual(additions, [{'name': suite, 'command': 'python3 scripts/test_auth_e2e.py --suite ' + suite, 'specs': ['web/e2e/' + suite + '.spec.ts']} for suite in expected])
        self.assertEqual(len(inventory['browser']), 56)
        self.assertEqual(len(inventory['fixed']), 7)
        consumers = inventory['frontend_roots']['web/src/TaskWorkspace.tsx']
        self.assertEqual(len(consumers), 19)
        self.assertEqual(consumers, [
            'tasks', 'audit', 'messages', 'messages-isolation', 'maintenance', 'user-assets-v2',
            'directory-v3', 'group-members-v4',
            'user-asset-export-v3', 'user-asset-export-v3-failure', 'user-asset-export-v3-cancel',
            'computer-asset-export-v3', 'computer-asset-export-v3-failure', 'computer-asset-export-v3-cancel',
            'group-asset-export-v3', 'group-asset-export-v3-failure', 'group-asset-export-v3-cancel',
            'tasks-health-recovery', 'operational-logs',
        ])
        self.assertEqual(consumers.count('operational-logs'), 1)
        self.assertIn('audit', inventory['frontend_roots']['web/src/TaskWorkspace.tsx'])
        legacy = restore_selection(Path('.github/workflows/ci.yml').read_text())
        matrix = legacy.split('  browser:\n', 1)[1].split('    steps:\n', 1)[0]
        self.assertEqual([(entry['name'], entry['command']) for entry in inventory['browser'] if entry['name'] not in expected + ['messages-isolation', 'tasks-health-recovery']], re.findall(r'          - name: ([^\n]+)\n            command: ([^\n]+)', matrix))
        actual = Path(".github/workflows/ci.yml").read_text()
        auth = actual.split("  auth-integration:\n", 1)[1].split("  browser:\n", 1)[0]
        matrices = re.findall(r"^        shard: \[([0-9, ]+)\]$", auth, re.M)
        self.assertEqual(len(matrices), 1)
        shards = [int(value) for value in matrices[0].split(",")]
        self.assertEqual(shards, list(range(1, 17)))
        self.assertEqual(len(inventory["browser"]) + len(inventory["fixed"]) + len(shards) + 6, 85)
        # Six scalar/native gates: plan, prepare, contracts, two native, verify.

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

    def test_identity_diagnostics_leave_original_body_and_deadline_authority_unchanged(self):
        source = Path('web/e2e/directory-v4-runtime.ts').read_text()
        identity = source.split('export async function readNativeFocusIdentity(', 1)[1].split('export type Original =', 1)[0]
        self.assertEqual(identity.count('response.json()'), 1)
        self.assertIn('const deadline = Date.now() + 15_000', identity)
        self.assertIn('selectedRequest = request; selectedResponse = response; state.phase = "SELECTED"; pending++;', identity)
        self.assertIn('if (active && state.phase === "SELECTED") jsonFailureClass = classifyIdentityJSONFailure(reason);', identity)
        self.assertIn('if (state.phase !== "FAILURE") { state.failure = failure; state.phase = "FAILURE";', identity)
        self.assertEqual(identity.count('await '), 1)
        self.assertIn('await completion;', identity)
        self.assertNotIn('response.finished(', identity)
        for forbidden in ['response.body(', 'response.text(', 'page.evaluate(', 'page.request.', 'page.route(', 'newCDPSession', 'fetch(']:
            self.assertNotIn(forbidden, identity)
        passive = identity.split('const observeTerminal =', 1)[1].split('note("prepare");', 1)[0]
        for forbidden in ['live()', 'fail(', 'finish()', 'deadline', '.url()', '.headers(', '.body(', '.json(']:
            self.assertNotIn(forbidden, passive)
        self.assertIn('requestObservations.get(request)', passive)
        self.assertIn('classifyIdentityRequestFailure(request.failure())', passive)
        self.assertIn('events.length < 32', identity)
        self.assertIn('Math.min(65_535, eventsDropped + 1)', identity)
        self.assertIn('counterWindow: "selected_until_first_terminal_state"', identity)
        self.assertIn('requestTerminalWindow: "observed_request_until_first_terminal_state"', identity)
        self.assertIn('eventWindow: "prepare_until_cleanup"', identity)
        emission = identity.split('console.log("[v4-focus-identity] "', 1)[1].split('      }));', 1)[0]
        for forbidden in ['origin', 'request.url', 'response.url', 'headers', 'body', 'actorId', 'reason', 'stack']:
            self.assertNotIn(forbidden, emission)
        self.assertLess(identity.index('active = false;'), identity.index('console.log("[v4-focus-identity] "'))
        self.assertLess(identity.index('requestObservations.clear();'), identity.index('console.log("[v4-focus-identity] "'))
        readers = Path('web/e2e/directory-v4-readers.spec.ts').read_text()
        self.assertIn('invariant(checkedID === reader.actorId, "Native focus did not recheck actual changed actor")', readers)

    def assert_split_reader_source(self, readers):
        self.assertEqual(readers.count('readNativeFocusIdentity('), 1)
        self.assertIn('const checkedID = await readNativeFocusIdentity(appWitness);', readers)
        self.assertIn('appWitness.goto("/#account")', readers)
        self.assertIn('requireSameNativeWindow([page, switching, appWitness])', readers)
        self.assertLess(readers.index('const appWitness = await context.newPage(); ownedPages.push(appWitness);'), readers.index('await navigateTo(switching, "退出登录")'))
        self.assertIn('accountIdentitySettled(appWitness, actor.actorId, actor.username)', readers)
        self.assertLess(readers.index('accountIdentitySettled(appWitness, actor.actorId, actor.username)'), readers.index('await navigateTo(switching, "退出登录")'))
        self.assertEqual(readers.count('Object.defineProperty(window, "BroadcastChannel"'), 2)
        self.assertEqual(readers.count('Object.defineProperty(window, "localStorage"'), 2)
        for target, observer in [('appWitness', 'appActivation'), ('page', 'workspaceActivation')]:
            self.assertIn(f'const {observer} = await observeHiddenNativeActivation({target}); activationObservers.push({observer});', readers)
            self.assertIn(f'await requireTrustedActivation({observer});', readers)
            self.assertIn(f'await accountIdentitySettled({target}, reader.actorId, reader.username);', readers)
        self.assertIn('await bounded(page.bringToFront(), 15_000, "Original Workspace native focus deadline exceeded")', readers)
        self.assertIn('visibility: "hidden", focused: false', readers)
        self.assertIn('visibility: "visible", focused: true, overflow: false', readers)
        self.assertIn('type: "focus", trusted: true', readers)
        self.assertIn('type: "visibilitychange", trusted: true, visible: true', readers)
        self.assertIn('events.length >= 16', readers)
        for fragment in ['page.locator(".soc-session-status")', '正在确认登录状态…', 'main.soc-main', '!main.hidden && !main.inert', '.signed-in strong', 'node.textContent === "用户 ID"']:
            self.assertIn(fragment, readers)
        self.assertIn('const me = await readJSON(context, "/api/auth/me");', readers)
        self.assertLess(readers.index('readerMonitoring = true;'), readers.index('const me = await readJSON(context, "/api/auth/me");'))
        interval = readers.split('readerMonitoring = true;', 1)[1].split('leaks.assertClean();', 1)[0]
        self.assertEqual(re.findall(r'readJSON\(context, ([^,\)]+)', interval), ['"/api/auth/me"', '"/api/access/menu"', '"/api/auth/me"'])
        self.assertEqual(interval.count('readJSON(context, "/api/auth/me")'), 2)
        self.assertEqual(re.findall(r'context\.request\.([a-z]+)\(', interval), ['post'])
        self.assertIn('context.request.post(V4 + operation,', interval)
        self.assertIn('for (const [operation, data] of [', interval)
        self.assertIn('["sync", { domainId:', interval); self.assertIn('["cancel", { taskUUID:', interval)
        for forbidden in ['context.request;', 'context.request[', 'context.request.fetch', 'login(', 'changePassword(', 'enrollMfa(', 'submit(', '"/logout"', '"/login"']:
            self.assertNotIn(forbidden, interval)
        self.assertIn('const keys = ["ID", "username", "role", "priv", "hasMfa", "needChangePwd", "isExpired", "csrfToken"] as const;', interval)
        self.assertIn('fixture.secrets.push(current.csrfToken)', interval)
        self.assertIn('keys.every(key => current[key] === me[key]) && readerBrowserAuthMutations === 0', interval)
        self.assertEqual(interval.count('await assertReaderSessionUnchanged();'), 3)
        self.assertIn('readerSessionSamples === 4 && readerBrowserAuthMutations === 0', interval)
        self.assertIn('if (path.startsWith("/api/auth/")) readerBrowserAuthMutations++;', readers)
        for forbidden in ['expect(current)', 'expect(me)', 'console.log(current', 'console.log(me', 'evidenceDigest([me.csrfToken', 'evidenceDigest([current.csrfToken']:
            self.assertNotIn(forbidden, readers)
        self.assertIn('initialLifecycleCleanup = { stop: () => lifecycle.evaluate(v => v.stop()), dispose: () => lifecycle.dispose() };', readers)
        self.assertIn('await cleanup(initialLifecycleCleanup.stop);', readers)
        self.assertIn('await cleanup(initialLifecycleCleanup.dispose);', readers)
        for action in ['leaks.close();', 'mutations.close();', 'context.off("request", writeListener);']:
            self.assertIn('await cleanup(async () => { ' + action + ' });', readers)
        self.assertIn('const cleanupDeadline = Date.now() + 20_000;', readers)
        self.assertIn('Math.max(1, cleanupDeadline - Date.now())', readers)
        self.assertIn('for (const owned of [...ownedPages].reverse()) await cleanup(() => owned.close());', readers)
        self.assertIn('throw error; // Preserve the first test failure', readers)
        self.assertIn('if (!failed) throw new Error("Owned reader witness cleanup failed");', readers)
        self.assertIn('auxiliaryPageCleanup: "source-bound-after-finally"', readers)
        self.assertNotIn('closed: true', readers)
        self.assertIn('samePageAtomicBinding: false', readers)
        self.assertIn('test.setTimeout(630_000)', readers)

    def test_split_identity_source_roles_session_interval_and_owned_cleanup(self):
        readers = Path('web/e2e/directory-v4-readers.spec.ts').read_text()
        self.assert_split_reader_source(readers)
        app = Path('web/src/App.tsx').read_text()
        account = app.split('function Account(', 1)[1].split('type FormProps', 1)[0]
        self.assertIn('{active === "account" && <Account profile={profile} />}', app)
        for forbidden in ['useEffect(', 'request(', 'request<', 'fetch(', 'sessionChanged', 'Workspace']:
            self.assertNotIn(forbidden, account)
        navigation = Path('web/src/SocNavigation.tsx').read_text()
        for forbidden in ['request(', 'request<', 'fetch(', '"/me"', '"/api/auth/me"']:
            self.assertNotIn(forbidden, navigation)
        self.assertIn('headers()["x-adtr-user-id"] !== String(actorId)', Path('web/e2e/directory-v4-fixture.ts').read_text())

    def test_auth_me_body_contract_does_not_borrow_business_actor_header(self):
        source = Path('internal/auth/service.go').read_text()
        writer = source.split('func write(', 1)[1].split('func (s *Service) readUser(', 1)[0]
        for header in ['w.Header().Set("Content-Type", "application/json")',
                       'w.Header().Set("Cache-Control", "no-store")',
                       'w.Header().Set("X-Content-Type-Options", "nosniff")']:
            self.assertIn(header, writer)
        self.assertNotIn('x-adtr-user-id', source.lower())
        self.assertIn('if path == "/me" {\n\t\treturn u, "", false, tx.Commit(ctx)', source)
        self.assertIn('mux.Handle("/api/auth/", authentication)', Path('cmd/adtr/main.go').read_text())
        fixture = Path('web/e2e/directory-v4-fixture.ts').read_text()
        self.assertIn('actorId !== undefined && response.headers()["x-adtr-user-id"] !== String(actorId)', fixture)
        self.assertIn('response.status() !== 200 || response.headers()["cache-control"] !== "no-store"', fixture)
        runtime = Path('web/src/directory-v4-runtime-contract.test.ts').read_text()
        self.assertIn('import { readJSON } from "../e2e/directory-v4-fixture"', runtime)
        self.assertIn('const start = fromActualSpec(source);', runtime)
        self.assertIn('accepts legitimate auth /me without a business actor header', runtime)

    def test_split_identity_source_rejects_single_scope_and_session_cleanup_regressions(self):
        readers = Path('web/e2e/directory-v4-readers.spec.ts').read_text()
        replacements = [
            ('readNativeFocusIdentity(appWitness)', 'readNativeFocusIdentity(page)'),
            ('appWitness.goto("/#account")', 'appWitness.goto("/#directory-v4")'),
            ('await requireTrustedActivation(appActivation);', ''),
            ('await requireTrustedActivation(workspaceActivation);', ''),
            ('accountIdentitySettled(appWitness, actor.actorId, actor.username)', 'accountIdentitySettled(appWitness, reader.actorId, reader.username)'),
            ('readJSON(context, "/api/auth/me")', 'readJSON(context, "/api/auth/me", reader.actorId)'),
            ('"isExpired", "csrfToken"] as const', '"isExpired"] as const'),
            ('context.request.post(V4 + operation,', 'context.request.post("/api/auth/logout",'),
            ('readerBrowserAuthMutations === 0', 'readerBrowserAuthMutations >= 0'),
            ('await cleanup(() => owned.close());', 'await owned.close();'),
            ('samePageAtomicBinding: false', 'samePageAtomicBinding: true'),
            ('await cleanup(initialLifecycleCleanup.dispose);', ''),
            ('await cleanup(async () => { mutations.close(); });', 'mutations.close();'),
        ]
        for before, after in replacements:
            self.assertIn(before, readers)
            with self.subTest(before=before), self.assertRaises(AssertionError):
                self.assert_split_reader_source(readers.replace(before, after, 1))

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
        predecessor = restore_health_recovery(text)
        self.assertEqual(restore_directory_v4_predecessor(predecessor), restore_directory_v4(text))
        for change in delta['replacements']:
            with self.assertRaises(ValueError):
                restore_directory_v4_predecessor(predecessor.replace(change['new'], change['old'], 1))
        for before, after in [('contents: read', 'contents: write'), ('run: make check', 'run: true'), ('max-parallel: 8', 'max-parallel: 9')]:
            with self.assertRaises(ValueError):
                restore_directory_v4_predecessor(predecessor.replace(before, after, 1))

    def test_both_native_families_direct_exports_and_all_legacy_assertions(self):
        source = Path('web/e2e/native-no-store-completion.spec.ts').read_text()
        for entry in ['../src/directory-v2-json.ts', '../src/directory-v4-controls-json.ts', 'parser.readDirectoryV2JSON', 'parser.readDirectoryV4ControlsJSON', 'parser.directoryV2BodyLimit', 'parser.directoryV4ControlsBodyLimit']:
            self.assertIn(entry, source)
        self.assertIn('for (const productionReader of productionReaders)', source)
        # Playwright rejects worker-scoped options inside per-reader groups
        # before it can discover or execute either reader's five cases.
        worker_options = 'test.use({ headless: false, trace: "off", screenshot: "off", video: "off" });'
        self.assertEqual(source.count(worker_options), 1)
        self.assertLess(source.index(worker_options), source.index('for (const productionReader of productionReaders)'))
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
