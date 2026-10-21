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
        'db_mutation': r'\b(?:INSERT\s+INTO|UPDATE\s+adtr|DELETE\s+FROM|TRUNCATE|ALTER\s+TABLE)\b',
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
