#!/usr/bin/env python3
"""Source-only guards for the authenticated, nonempty isolation browser slice.

These are CI contracts, not a substitute for real browser/Worker/PostgreSQL
passage or independent screenshot review. No app, browser or DB is started.
"""
from pathlib import Path
import json
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT / "web/e2e/messages-isolation.spec.ts"


def violations(source):
    """Closed failure-boundary checks, also tested with deliberately bad edits."""
    failures = []
    required = [
        'test.use({ trace: "off", screenshot: "off", video: "off",',
        'catch { throw new Error(category); }',
        'return safe("message_read_transport", async () => {',
        'return await safe("message_write_transport", async () => {',
        'return await safe(category, async () => {',
        'value = await safe("auth_setup_transport", action)',
        'await safe("health_submit_transport", async () => {',
        'const value = await safe("browser_observation", action)',
        'Object.keys(r.body).length === 1 && r.body.error === code',
        'stages.push({ label, status, body: { error: code }, before, after })',
        'stdio: ["ignore", "pipe", "pipe"]',
        'catch { throw new Error("owned_db_observation_failed"); }',
    ]
    for fragment in required:
        if fragment not in source:
            failures.append("missing_safe_boundary")
    # Exactly one raw call of each method, inside its dedicated safe wrapper.
    transports = re.findall(r'\.request\.(get|post|fetch|put|delete|patch|head)\s*\(', source)
    if sorted(transports) != ["get", "post"]:
        failures.append("unwrapped_transport")
    for name, category, following in (("get", "message_read_transport", "profile"),
                                      ("post", "message_write_transport", "uiResponse")):
        match = re.search(rf'async function {name}\(.*?(?=  async function {following}\()', source, re.S)
        if not match or f'safe("{category}", async () => {{' not in match[0] or "maxRetries: 0" not in match[0]:
            failures.append("unsafe_transport_wrapper")
    forbidden = [
        r'catch\s*\(', r'\bcause\s*:', r'\bconsole\.', r'\.toHaveValue\(',
        r'\.toMatchObject\(', r'\.toEqual\(', r'\.postData(?:JSON)?\(',
        r'\.storageState\(', r'\.headers\(', r'\.allHeaders\(',
        r'\.route\(', r'\.routeFromHAR\(', r'\.fulfill\(', r'\.fetch\(',
        r'\blocalStorage\b', r'\bsessionStorage\b', r'\bSIGSTOP\b',
        r'\.clock\.', r'\bwaitForTimeout\(', r'\btest\.skip\(',
        r'\.on\("(?:console|response|requestfailed|pageerror)"',
        r'\b(?:INSERT\s+INTO|UPDATE\s+adtr|DELETE\s+FROM|TRUNCATE|ALTER\s+TABLE)\b',
        r'\b(?:session_hash|token_hash|mfa_last_step|auth_attempts)\b',
        r'\b(?:error|err|exception)\.(?:message|stack)\b',
    ]
    for pattern in forbidden:
        if re.search(pattern, source, re.I):
            failures.append("unsafe_source_construct")
    if source.count("maxRetries: 0") != 2:
        failures.append("transport_retry_policy")
    # Mask literals/comments without changing offsets, then identify complete
    # safe-wrapper argument ranges. A newly appended bare browser action must
    # fail even if every existing wrapper is still present elsewhere.
    masked = re.sub(r'"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|`(?:\\.|[^`\\])*`|//[^\n]*|/\*.*?\*/',
                    lambda m: " " * len(m[0]), source, flags=re.S)
    regions = []
    for call in re.finditer(r"\b(?:safe|setup|observe|proofStep|uiResponse)\s*\(", masked):
        if re.search(r"function\s+$", masked[max(0, call.start() - 30):call.start()]):
            continue
        opening = masked.index("(", call.start())
        depth = 0
        for end in range(opening, len(masked)):
            if masked[end] == "(":
                depth += 1
            elif masked[end] == ")":
                depth -= 1
                if depth == 0:
                    regions.append((opening, end))
                    break
    actions = r"\b(?:page|tab|A\.page|a\.page)\.(?:getBy\w+|locator|goto|goBack|goForward|evaluate|screenshot|reload)\s*\("
    for action in re.finditer(actions, masked):
        if not any(start < action.start() < end for start, end in regions):
            failures.append("unwrapped_browser_action")
    return failures


class PrivacyContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SPEC.read_text()

    def test_all_transport_and_proof_boundaries(self):
        self.assertEqual(violations(self.source), [])

    def test_added_raw_authenticated_get_is_rejected(self):
        self.assertIn("unwrapped_transport", violations(self.source + '\nawait a.context.request.get("/api/auth/me");'))

    def test_added_raw_post_is_rejected(self):
        self.assertIn("unwrapped_transport", violations(self.source + '\nawait a.context.request.post("/api/messages/read", input);'))

    def test_error_leak_variants_are_rejected(self):
        for bad in ('catch (error) { throw error; }', 'new Error("bad", { cause: error })',
                    'console.log(error)', 'error.message', 'exception.stack',
                    'expect(secret).toHaveValue(secret)', 'page.on("response", log)',
                    'await context.storageState()', 'response.headers()',
                    'response.request().postDataJSON()'):
            with self.subTest(kind=bad.split("(")[0]):
                self.assertIn("unsafe_source_construct", violations(self.source + "\n" + bad))

    def test_retry_and_boundary_removal_are_rejected(self):
        self.assertIn("transport_retry_policy", violations(self.source.replace("maxRetries: 0", "maxRetries: 1", 1)))
        self.assertIn("missing_safe_boundary", violations(self.source.replace('return await safe("message_write_transport", async () => {', 'return (async () => {', 1)))

    def test_arbitrary_response_diff_is_rejected(self):
        self.assertIn("unsafe_source_construct", violations(self.source + '\nexpect(response).toEqual({error:"not_found"});'))
        self.assertIn("missing_safe_boundary", violations(self.source.replace('Object.keys(r.body).length === 1 && r.body.error === code', 'r.body.error === code')))

    def test_appended_bare_proof_actions_are_rejected(self):
        for bad in ('await page.getByLabel("密码").fill(password);',
                    'await page.getByRole("button").click();',
                    'await expect(page.getByLabel("认证器验证码")).toBeVisible();'):
            self.assertIn("unwrapped_browser_action", violations(self.source + "\n" + bad))

    def test_uncertain_writes_only_observe_and_fail(self):
        for fragment in ('async function uncertain(', 'type: "uncertain_write"',
                         'observed ? "observed" : "unknown"', 'get(a, `/api/messages/${route}?operationId=${data.operationId}`)',
                         '"提交幂等键"', "idempotency_key='${taskKey}'", 'r.body.needChangePwd === false',
                         'r.body.hasMfa === true', 'versions(actor)', "r.name='Synthetic Isolation Task Writer'",
                         "target.username='synthetic.isolation.b'"):
            self.assertIn(fragment, self.source)

    def test_chromium_does_not_inherit_proof_environment(self):
        self.assertIn('launchOptions: { env: chromiumEnvironment()', self.source)
        self.assertIn('!/^(ADTR_|POSTGRES_|PG)/i.test(entry[0])', self.source)
        self.assertIn('executablePath: process.env.ADTR_E2E_CHROMIUM_PATH', self.source)

    def test_no_request_listener_serializes_proofs(self):
        listener = self.source.split('context.on("request", request => {', 1)[1].split('\n      });', 1)[0]
        self.assertIn('a.queries++', listener)
        self.assertIn('a.writes++', listener)
        for prohibited in ('push(', 'JSON.', 'writeFile', 'console', '.headers', '.postData', 'throw '):
            self.assertNotIn(prohibited, listener)

    def test_screenshots_only_after_nonempty_read_baseline(self):
        s = self.source
        self.assertLess(s.index('"nonempty_read_baseline_invalid"'), s.index('a.page.screenshot('))
        for value in ('"screenshot_proof_not_empty"', 'input[type="password"]', 'input[autocomplete="one-time-code"]',
                      '[role="dialog"]:visible', 'fullPage: true', 'deviceScaleFactor: 1',
                      '{ width: 1440, height: 900 }', '{ width: 390, height: 844 }',
                      'messages-soc-isolation-${a.label}-${viewport.width}.png'):
            self.assertIn(value, s)
        self.assertEqual(s.count('.screenshot('), 1)


class IsolationContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SPEC.read_text()

    def require_fragments(self, *fragments):
        for fragment in fragments:
            self.assertIn(fragment, self.source)

    def test_fresh_identities_real_minimum_authority(self):
        self.require_fragments('storageState: { cookies: [], origins: [] }',
            'initialize("A", username, initial', 'initialize("B", "synthetic.isolation.b"',
            'A.userId !== B.userId', 'r.body.hasMfa === true', 'r.body.needChangePwd === false',
            'r.body.isExpired === false', '"后台任务：读取"', '"后台任务：写入"',
            '"/api/access/roles/save"', '"/api/access/users/create"')
        self.assertNotIn('ADTR_E2E_MESSAGE_FIXTURE', self.source)

    def test_exact_six_http_health_tasks_and_proof_budget(self):
        self.require_fragments('for (const a of actors) for (let index = 0; index < 3; index++)',
            '"/api/tasks/submit"', 'value.replayed === false', 'uuid(value.task?.taskUUID)',
            'A.sensitive === 8 && B.sensitive === 6 && totpWaits === 8',
            'new Set(actors.flatMap(a => a.tasks)).size === 6',
            '(a.mfa.counter + 1) * 30_000 + 1100', 'totpWaits <= 8')

    def test_read_only_complete_provenance(self):
        self.require_fragments('PGOPTIONS=-c default_transaction_read_only=on',
            'm.tenant_id=t.tenant_id AND t.tenant_id=e.tenant_id AND e.tenant_id=u.tenant_id',
            'm.recipient_user_id=t.actor_id AND t.actor_id=u.id',
            'm.source_task_id=t.task_id AND t.task_id=e.task_id AND e.task_id=o.task_id',
            'm.source_event_id=e.id AND e.id=o.event_id',
            'p.sourceResultVersion === p.eventResultVersion', 'p.kind === "infrastructure.health"',
            'p.taskDomain === "platform" && p.eventDomain === "platform"',
            'p.action === "finished" && p.state === "succeeded"',
            'item.scopeKind === "platform" && item.domainId === ""',
            'item.title === p.title && item.description === p.description',
            'sameInstant(item.occurredAt, p.occurredAt)')

    def test_nonempty_read_history_before_negative_matrix(self):
        self.require_fragments('baseline.messages.length === 3 && baseline.reads.length === 1 && baseline.single.length === 1',
            'a.snapshot.counts.read === 1 && a.snapshot.counts.unread === 2',
            'sameInstant(baseline.reads[0].first_read_at, a.read.firstReadAt)',
            'sameInstant(baseline.single[0].first_read_at, a.read.firstReadAt)')
        self.assertLess(self.source.index('baselines.push(baseline)'), self.source.index('for (const [caller, owner]'))

    def test_history_timestamp_equality_preserves_submillisecond_precision(self):
        self.require_fragments('function sameInstant(', 'padEnd(9, "0")',
            'new Date(value).toISOString().slice(0, 19) !== match[1]',
            'a !== null && a === canonical(right)',
            'sameInstant(persistedOperation.accepted_at, operation.receipt.acceptedAt)',
            'sameInstant(persistedOperation.updated_at, operation.updatedAt)')
        self.assertNotRegex(self.source, r'Date\.parse\([^\n]+\) === Date\.parse')

    def test_exact_two_direction_negative_matrix(self):
        self.require_fragments('for (const [caller, owner] of [[A, B], [B, A]])',
            '"/api/messages/read"', '"/api/messages/bulk-read"', '"/api/messages/bulk-read-cancel"',
            'stages.length === 32', '400, "target_not_in_result"',
            'operationId: randomUUID()', 'operationId: owner.read.operationId',
            'messageIds: [...(mixed ? [own.items.find(i => !i.read)!.messageId] : []), foreign.items[0].messageId]')
        for suffix in ('foreign-page', 'foreign-locate', 'foreign-single-receipt', 'foreign-read',
                       'foreign-single-replay', 'foreign-bulk-${selection}', 'own-result-foreign-single',
                       'own-result-foreign-selected-${mixed}', 'foreign-bulk-receipt-${selection}',
                       'foreign-bulk-replay-${selection}', 'foreign-completed-cancel-${selection}'):
            self.assertIn(suffix, self.source)

    def test_full_persistence_not_only_counts(self):
        self.require_fragments('const before = actors.map(state), frozen = actors.map(versions)',
            'same(before, after) && same(frozen, actors.map(versions))',
            'message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version',
            '"message_reads"', '"message_read_operations"', '"message_bulk_operations"',
            '"message_bulk_targets"', '"message_bulk_receipts"', 'authorization_version',
            'projection_version', '"operation_id,ordinal"')

    def test_own_bulk_exact_deltas_and_protected_history(self):
        self.require_fragments('for (const [actor, protectedActor] of [[A, B], [B, A]])',
            '{ target: 1, processed: 1, newlyRead: 1, alreadyRead: 0, failed: 0, remaining: 0 }',
            '{ target: 3, processed: 3, newlyRead: 1, alreadyRead: 2, failed: 0, remaining: 0 }',
            '"bulk_admission_cross_user_change"', '"bulk_processing_cross_user_change"',
            '"bulk_terminal_cross_user_change"', '"bulk_persisted_receipts_invalid"',
            'before.reads.every', 'before.targets.every', 'before.receipts.every',
            'read.first_read_at === r.first_read_at', '"frozen_snapshot_changed"')

    def test_phase_deadlines_and_query_quota_not_suite_timeout_only(self):
        self.require_fragments('test.setTimeout(660_000)', 'started + 623_800',
            'snapshotStarted + 60_000', 'setupSpent <= 75_000', 'Date.now() + 30_000',
            'Date.now() + 60_000', 'Date.now() - begin <= 31_100',
            'Date.now() < Date.parse(s.expiresAt)', 'a.queries <= 7',
            'a.queries >= 5 && a.queries <= 7', 'Date.parse(s.expiresAt) - Date.parse(s.confirmedAt) === 600_000')
        self.assertLess(self.source.index('"projection_provenance_invalid"'), self.source.index('snapshotStarted = Date.now()'))

    def test_tabs_have_no_silent_write_or_foreign_restore(self):
        self.require_fragments('a.context.newPage()', 'tab.goto(a.page.url())',
            '"foreign_tab_history"', 'tab.goBack()', 'tab.goForward()', 'tabSnapshot = await query(a, tab)',
            'a.writes === writeCount && same(before, state(a))', '"tab_silent_mutation"')

    def test_final_manifest_is_not_a_pass_placeholder(self):
        self.require_fragments('status: "passed"', 'checkoutCommit:', 'checkoutTree:',
            'sourceSHA256:', 'artifactsSHA256:', 'apiWorker: hashFile("bin/adtr")',
            'frontendEntry: hashFile("web/dist/index.html")', 'frontendFiles', 'hashFrontend("web/dist")', 'workflowCommit:',
            'workflowRun:', 'workflowAttempt:', 'positiveBulks.length === 4', 'screenshots.length === 4',
            'messages-soc-isolation-evidence.json', 'screenshots require independent pixel review')

    def test_runner_and_ci_interface(self):
        runner = (ROOT / 'scripts/test_auth_e2e.py').read_text()
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        inventory = json.loads((ROOT / 'scripts/ci_inventory.json').read_text())
        self.assertIn('"messages-isolation"', runner)
        self.assertIn('validate_messages_isolation_evidence', runner)
        self.assertIn('ADTR_E2E_DB_CONTAINER', runner)
        self.assertIn('720', runner)
        self.assertIn('messages-isolation', workflow)
        self.assertIn('messages-soc-*', workflow)
        self.assertIn('timeout-minutes: 20', workflow)
        text = json.dumps(inventory)
        self.assertIn('python3 scripts/test_auth_e2e.py --suite messages-isolation', text)
        self.assertIn('web/e2e/messages-isolation.spec.ts', text)
        makefile = (ROOT / 'Makefile').read_text()
        self.assertIn("python3 -m unittest discover -s scripts -p '*_contract.py'", makefile)


if __name__ == '__main__':
    unittest.main()
