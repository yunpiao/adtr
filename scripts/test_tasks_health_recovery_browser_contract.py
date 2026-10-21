#!/usr/bin/env python3
"""Static privacy/mutation contracts, not browser evidence.

Authored source-only; these tests must be executed only after integration is
separately authorized. A lexical guard complements, never replaces, source
review and a genuine production-worker/browser run.
"""
from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT / 'web/e2e/tasks-health-recovery.spec.ts'
SQL_IDENTIFIER = r'(?:[a-z_][a-z0-9_$]*|"(?:[^"]|"")+")'
SQL_RELATION = rf'{SQL_IDENTIFIER}(?:\s*\.\s*{SQL_IDENTIFIER})?'
UI_STAGES = (
    'browser_proof_or_observation', 'recovery_intent_open', 'recovery_warning_count',
    'recovery_warning_text', 'recovery_cancelled_key', 'recovery_intent_cancel',
    'recovery_intent_closed', 'recovery_intent_reopen', 'recovery_submitted_key',
    'navigation_open_ready', 'navigation_back_visible', 'navigation_return_list',
    'navigation_open_detail', 'navigation_restore_ready', 'navigation_detail_visible',
    'navigation_reload', 'navigation_history_back', 'navigation_history_forward',
)


def recovery_dialog_violations(source):
    failures = []
    match = re.search(r'const UI_STAGES = \[(.*?)\] as const;', source, re.S)
    if not match or tuple(re.findall(r'"([^"]+)"', match.group(1))) != UI_STAGES:
        failures.append('unsafe_ui_stage_set')
    for guard in ('type UIStage = typeof UI_STAGES[number];',
                  'stage: UIStage = "browser_proof_or_observation"',
                  'fact(UI_STAGES.includes(stage), "ui_stage_invalid");',
                  'await safe(stage, work)'):
        if guard not in source:
            failures.append('unsafe_ui_stage_boundary')
    for stage in UI_STAGES[1:]:
        if f', "{stage}");' not in source:
            failures.append('missing_recovery_stage')
    if 'page.locator(".task-action .warning")' in source:
        failures.append('ambiguous_recovery_warning')
    for assertion in (
        'expect(page.locator(".task-action > .warning")).toHaveCount(1)',
        'expect(page.locator(".task-action > .warning")).toContainText("不修改原任务或重置其历史")',
    ):
        if assertion not in source:
            failures.append('missing_recovery_warning_assertion')
    return failures


def navigation_readiness_violations(source):
    failures = []
    if 'getByTestId("task-detail-identity").count()' in source:
        failures.append('instant_navigation_branch')
    if 'getByRole("button", { name: "返回任务列表", exact: true }).count()' in source:
        failures.append('instant_navigation_branch')
    expected = (
        'await expect(page.getByRole("button", { name: "返回任务列表", exact: true })',
        'await expect(page.getByTestId("task-detail-identity")',
        '.or(page.getByRole("button", { name: `详情 ${id}`, exact: true })).first()).toBeVisible();',
        'if (backVisible) await ui(',
        'if (!detailVisible) await openDetail(id);',
        'await restoredDetail(value.task.id);',
    )
    if any(token not in source for token in expected):
        failures.append('missing_navigation_readiness')
    if source.count('await restoredDetail(value.task.id);') != 2:
        failures.append('missing_navigation_restore')
    if '.first().click(' in source:
        failures.append('ambiguous_navigation_action')
    return failures


def mask_literals(source):
    # Preserve offsets so regions and actions can be compared without matching
    # sample text, comments or SQL words embedded in unrelated identifiers.
    pattern = r'"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|`(?:\\.|[^`\\])*`|//[^\n]*|/\*.*?\*/'
    return re.sub(pattern, lambda m: ' ' * len(m[0]), source, flags=re.S)


def call_regions(masked, names):
    regions = []
    for match in re.finditer(r'\b(?:' + '|'.join(names) + r')\s*\(', masked):
        if re.search(r'function\s+$', masked[max(0, match.start() - 30):match.start()]):
            continue
        opening = masked.index('(', match.start())
        depth = 0
        for end in range(opening, len(masked)):
            if masked[end] == '(':
                depth += 1
            elif masked[end] == ')':
                depth -= 1
                if depth == 0:
                    regions.append((opening, end))
                    break
    return regions


def privacy_violations(source):
    result = []
    forbidden = {
        'raw_error': r'catch\s*\(|\bcause\s*:|\b(?:error|err|exception)\.(?:message|stack)',
        'logging': r'\bconsole\.|\.on\(\s*["\'](?:console|response|requestfailed|pageerror)["\']',
        'proof_diff': r'\.to(?:HaveValue|Equal|MatchObject)\s*\(',
        'proof_export': r'\.(?:postDataJSON|postData|allHeaders)\s*\(',
        'network_mock': r'\.(?:route|routeFromHAR|fulfill)\s*\(',
        'state_seed': r'\b(?:localStorage|sessionStorage)\b|\.clock\.',
        'scope_escape': r'\b(?:SIGSTOP|pkill|killall)\b',
        # Match a complete UPDATE target and SET, not an "adtr" prefix whose
        # trailing word boundary misses adtr_users. Include schema-qualified,
        # quoted and unqualified targets. Keep the old UPDATE branch as well,
        # so this is additive and no previously detected write is permitted.
        'db_mutation': rf'\b(?:INSERT\s+INTO|UPDATE\s+adtr|UPDATE\s+(?:ONLY\s+)?{SQL_RELATION}(?:\s+(?:AS\s+)?{SQL_IDENTIFIER})?\s+SET|DELETE\s+FROM|TRUNCATE|ALTER\s+TABLE)\b',
        'private_row': r'\b(?:session_hash|token_hash|password_hash)\b|SELECT\s+\*',
        'skip': r'\btest\.(?:skip|fixme)\s*\(',
        'extra_artifact': r'\.(?:attach|attachments)\b|\bcopyFile(?:Sync)?\s*\(',
    }
    for label, pattern in forbidden.items():
        if re.search(pattern, source, re.I):
            result.append(label)
    masked = mask_literals(source)
    safe_regions = call_regions(masked, ('safe', 'safeWrite', 'proofStep', 'uiResponse', 'observe', 'ui', 'mutation'))
    # This detects newly appended raw authenticated transports/proof calls even
    # when all required safe helpers still exist elsewhere in the file.
    action = r'\b(?:page|tab|context|target|oldContext|replacementContext)\.(?:request\.)?(?:getBy\w+|locator|goto|goBack|goForward|evaluate|screenshot|reload|get|post|fetch|put|patch|delete)\s*\('
    for match in re.finditer(action, masked):
        if not any(begin < match.start() < end for begin, end in safe_regions):
            result.append('unwrapped_action')
    if re.search(r'maxRetries\s*:\s*[1-9]', masked):
        result.append('transport_retry')
    return result


class PrivacyMutationContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SPEC.read_text(encoding='utf-8')

    def test_safe_origin_boundaries(self):
        self.assertEqual(privacy_violations(self.source), [])

    def test_recovery_warning_and_fixed_stage_boundaries(self):
        self.assertEqual(recovery_dialog_violations(self.source), [])

    def test_recovery_warning_must_not_match_nested_proof_warning(self):
        changed = self.source.replace('.task-action > .warning', '.task-action .warning')
        self.assertIn('ambiguous_recovery_warning', recovery_dialog_violations(changed))
        changed = self.source.replace('expect(page.locator(".task-action > .warning")).toHaveCount(1)',
                                      'expect(page.locator(".task-action > .warning")).toHaveCount(0)')
        self.assertIn('missing_recovery_warning_assertion', recovery_dialog_violations(changed))

    def test_recovery_stage_set_cannot_accept_arbitrary_diagnostics(self):
        for changed in (
            self.source.replace('"recovery_cancelled_key",', '"untrusted_diagnostic",', 1),
            self.source.replace('fact(UI_STAGES.includes(stage), "ui_stage_invalid");', ''),
            self.source.replace('stage: UIStage =', 'stage: string ='),
        ):
            self.assertTrue(recovery_dialog_violations(changed))

    def test_navigation_waits_for_visible_real_branch(self):
        self.assertEqual(navigation_readiness_violations(self.source), [])

    def test_navigation_readiness_cannot_be_removed_or_used_as_action(self):
        for changed in (
            self.source.replace('.first()).toBeVisible();', '.first()).toBeAttached();'),
            self.source.replace('await restoredDetail(value.task.id);', '', 1),
            self.source + '\nawait page.getByTestId("task-detail-identity").count();',
            self.source + '\nawait locator.first().click();',
        ):
            self.assertTrue(navigation_readiness_violations(changed))

    def test_new_bare_actions_are_not_hidden_by_existing_wrappers(self):
        for extra in (
            'await context.request.get("/api/auth/me");',
            'await context.request.post("/api/tasks/recover", proof);',
            'await page.getByLabel("密码").fill(secret);',
            'await page.screenshot({path:"unsafe.png"});',
        ):
            with self.subTest(action=extra.split('(')[0]):
                self.assertIn('unwrapped_action', privacy_violations(self.source + '\n' + extra))

    def test_leaks_and_bypasses_are_rejected(self):
        for category, extra in (
            ('raw_error', 'catch (error) { throw error; }'),
            ('raw_error', 'new Error("bad", {cause:error});'),
            ('logging', 'console.log(proof);'),
            ('logging', 'page.on("response", dump);'),
            ('proof_diff', 'expect(secret).toHaveValue(secret);'),
            ('proof_export', 'request.postDataJSON();'),
            ('network_mock', 'page.route("**", handler);'),
            ('state_seed', 'localStorage.setItem("task", key);'),
            ('db_mutation', 'UPDATE adtr_users SET authorization_version=4;'),
            ('private_row', 'SELECT * FROM adtr_users;'),
            ('extra_artifact', 'testInfo.attach("proof", {body: proof});'),
            ('transport_retry', 'const options = {maxRetries: 1};'),
        ):
            with self.subTest(category=category):
                self.assertIn(category, privacy_violations(self.source + '\n' + extra))

    def test_masker_does_not_treat_comments_as_actions(self):
        self.assertEqual(privacy_violations('// await page.goto("secret");'), [])
        self.assertEqual(privacy_violations('safe("fixed", async () => { await page.goto("/"); });'), [])

    def test_update_target_variants_are_rejected(self):
        for statement in (
            'UPDATE adtr_users SET authorization_version=4;',
            'UPDATE adtr.users SET authorization_version=4;',
            'UPDATE adtr.users /* original guard remains */ SET authorization_version=4;',
            'UPDATE "adtr"."users" SET authorization_version=4;',
            'UPDATE users SET authorization_version=4;',
            'UPDATE ONLY adtr.users AS u SET authorization_version=4;',
            'update\n adtr.users u\n set authorization_version=4;',
        ):
            with self.subTest(statement=statement):
                # Check SQL itself and its realistic embedding in a TS string.
                self.assertIn('db_mutation', privacy_violations(statement))
                self.assertIn('db_mutation', privacy_violations(self.source + '\nsql(`' + statement + '`);'))

    def test_readonly_sql_and_update_identifiers_are_not_writes(self):
        for statement in (
            'SELECT authorization_version FROM adtr.users;',
            'SELECT updated_at AS "updatedAt" FROM adtr.tasks;',
            'const updateTask = "refresh";',
        ):
            with self.subTest(statement=statement):
                self.assertNotIn('db_mutation', privacy_violations(statement))

    def test_other_sql_write_guards_remain_closed(self):
        for statement in (
            'INSERT INTO adtr.tasks VALUES (1);',
            'DELETE FROM adtr.tasks;',
            'TRUNCATE adtr.tasks;',
            'ALTER TABLE adtr.tasks ADD COLUMN unsafe int;',
        ):
            with self.subTest(statement=statement):
                self.assertIn('db_mutation', privacy_violations(statement))

    def test_boundary_removal_is_detected(self):
        wrapped = 'safe("fixed", async () => { await page.getByLabel("密码").fill(secret); });'
        self.assertEqual(privacy_violations(wrapped), [])
        self.assertIn('unwrapped_action', privacy_violations(wrapped.replace('safe(', 'unsafe(')))

    def test_no_runtime_cleanup_receipt_forgery(self):
        # Reading/mentioning the filename is allowed; creating it is runner-only.
        self.assertNotRegex(self.source, r'(?:writeFileSync|writeFile)\s*\([^;]*cleanup-receipt',)
        self.assertNotRegex(self.source, r'ownershipTransferred\s*:\s*true')
        self.assertNotRegex(self.source, r'workerReaped\s*:\s*true')
        self.assertNotRegex(self.source, r'ownedContainerRemoved\s*:\s*true')




# Literal mutations here intentionally cover the safety-critical decisions;
# these guards do not claim a full TypeScript control-flow proof.
DECISIONS = {
    'mutation_freeze': 'fact(!halted && posts[name] === 0, "mutation_forbidden")',
    'single_start': 'fact(!halted && posts.controllerStart === 0, "controller_start_duplicate")',
    'unknown_freeze': 'halted = true; unknownWrites++',
    'fresh_reopened_key': 'submittedKey !== cancelledKey && submittedKey !== submissionKey',
    'same_reopened_key': 'fact(actual === submittedKey, "recovery_key_changed")',
    'replay_key': 'idempotencyKey: submittedKey, actorPassword: current, totpCode: code',
    'cancel_zero_post': 'fact(recoveryPosts === 0, "cancel_sent_recovery")',
    'cancel_zero_rows': 'cancelledAfter = zero(cancelledKey)',
    'parent_history': 'history(parent, ["submitted", "leased", "authorization_rejected"], [0, 0, 0])',
    'child_history': 'history(child, ["recovered", "leased", "started", "progress", "finished"], [0, 0, 1, 1, 1])',
    'immutable_replay': 'fact(equal(beforeReplay, afterReplay), "replay_changed_persistence")',
    'readonly_db': 'PGOPTIONS=-c default_transaction_read_only=on',
    'terminal_lease_empty': 'task.leaseOwner === "" && task.leaseUntil === null && task.nextAttemptAt === null',
    'task_fencing': 'task.fencingToken === (task.state === "queued" ? 0 : 1)',
    'task_payload_hash': 'task.payloadHash === digest(`1:${task.parentID}:{}`)',
    'counter_ceiling': 'fact(++counterTransitions <= 4, "counter_transition_budget")',
    'fresh_counter_margin': '(counter + 1) * 30000 + 1100',
    'suite_deadline': 'test.setTimeout(600000)',
    'missing_runner': '"dedicated_runner_contract_missing"',
    'browser_env': '!/^(ADTR_|POSTGRES_|PG)/i.test(x[0])',
}


def scenario_violations(source):
    return [label for label, decision in DECISIONS.items() if decision not in source]


class ScenarioMutationContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SPEC.read_text(encoding='utf-8')

    def test_critical_decisions_present(self):
        self.assertEqual(scenario_violations(self.source), [])

    def test_each_decision_removal_fails_independently(self):
        for label, decision in DECISIONS.items():
            with self.subTest(decision=label):
                self.assertIn(decision, self.source)
                mutated = self.source.replace(decision, '/* removed decision */', 1)
                self.assertIn(label, scenario_violations(mutated))

    def test_replay_cannot_use_cancelled_or_submission_key(self):
        for wrong in ('cancelledKey', 'submissionKey'):
            mutated = self.source.replace('idempotencyKey: submittedKey, actorPassword: current, totpCode: code',
                                          f'idempotencyKey: {wrong}, actorPassword: current, totpCode: code')
            self.assertIn('replay_key', scenario_violations(mutated))

    def test_progress_event_cannot_be_omitted(self):
        changed = self.source.replace('["recovered", "leased", "started", "progress", "finished"]',
                                      '["recovered", "leased", "started", "finished"]')
        self.assertIn('child_history', scenario_violations(changed))




class SemanticEqualityContract(unittest.TestCase):
    """Future execution tests the actual pure helper without importing the spec."""
    def test_jsonb_order_is_irrelevant_but_values_and_array_order_are_not(self):
        source = SPEC.read_text(encoding='utf-8')
        match = re.search(r'function equal\(a: unknown, b: unknown\): boolean \{.*?\n\}(?=\nfunction fact)', source, re.S)
        self.assertIsNotNone(match, 'pure equality helper must remain independently testable')
        typed = match.group(0)
        self.assertEqual(typed.count(': unknown'), 2)
        self.assertEqual(typed.count(' as Record<string, unknown>'), 2)
        helper = typed.replace(': unknown', '').replace('): boolean', ')').replace(' as Record<string, unknown>', '')
        program = helper + '''
const cases = [
  [equal({queue:"ready",database:"ready"},{database:"ready",queue:"ready"}),true],
  [equal({a:{y:2,x:1},b:[{q:4,p:3}]},{b:[{p:3,q:4}],a:{x:1,y:2}}),true],
  [equal({queue:"stale",database:"ready"},{database:"ready",queue:"ready"}),false],
  [equal([1,2],[2,1]),false],
  [equal({a:1},{a:1,b:2}),false],
  [equal({a:null},{a:{}}),false],
  [equal({a:1},{a:"1"}),false],
  [equal(null,{}),false]
];
if (cases.some(([actual, expected]) => actual !== expected)) process.exit(1);
'''
        result = subprocess.run(['node', '-e', program], capture_output=True, timeout=10, check=False)
        self.assertEqual(result.returncode, 0, 'pure semantic equality contract failed')


if __name__ == '__main__':
    unittest.main()
