#!/usr/bin/env python3
"""Static privacy/mutation contracts, not browser evidence.

Authored source-only; these tests must be executed only after integration is
separately authorized. A lexical guard complements, never replaces, source
review and a genuine production-worker/browser run.
"""
from pathlib import Path
import hashlib
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
    'lineage_parent', 'lineage_back', 'lineage_forward',
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


def screenshot_violations(source):
    required = (
        'const slot = state === "failed" ? (width === 1440 ? "parent_desktop" : "parent_mobile")',
        ': (width === 1440 ? "child_desktop" : "child_mobile");',
        'const captureStages = ["viewport", "proof_empty", "targets", "measure", "fit", "scroll", "contained", "unobscured", "png"] as const;',
        'type CaptureStage = typeof captureStages[number];',
        'stage: CaptureStage, work: () => Promise<T>',
        'fact(captureStages.includes(stage), "screenshot_stage_invalid");',
        'return safe(`screenshot_${slot}_${stage}`, () => ui(work));',
        'bounds.right - bounds.left <= width && bounds.bottom - bounds.top <= height',
        'window.scrollBy({ left: dx, top: dy, behavior: "instant" })',
        'box.x >= 0 && box.y >= 0 && box.x + box.width <= width && box.y + box.height <= height',
        'document.elementFromPoint(x, y)',
        'hit === element || element.contains(hit)',
        'document.createTreeWalker(element, NodeFilter.SHOW_TEXT)',
        'range.selectNodeContents(node);',
        'for (const rect of range.getClientRects())',
        'rect.right <= window.innerWidth && rect.bottom <= window.innerHeight',
        'return { present: fragments > 0, contained, unobscured };',
        'const targetSlot = index === 0 ? "first" : "second";',
        'fact(index === 0 || index === 1, "screenshot_target_index_invalid");',
        'fact(proof.present, `screenshot_${slot}_${targetSlot}_text_missing`);',
        'fact(proof.contained, `screenshot_${slot}_${targetSlot}_text_outside`);',
        'fact(proof.unobscured, `screenshot_${slot}_${targetSlot}_text_obscured`);',
        'fullPage: false, animations: "disabled"',
        'fact(empty, "proof_not_empty");',
    )
    failures = []
    if any(value not in source for value in required):
        failures.append('screenshot_boundary_missing')
    if 'await targets[0].scrollIntoViewIfNeeded();' in source:
        failures.append('single_target_framing')
    if re.search(r'\.style\s*[.\[]|setAttribute\s*\(|\b(?:innerHTML|outerHTML)\s*=', source):
        failures.append('screenshot_dom_rewrite')
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
    safe_regions = call_regions(masked, ('safe', 'safeWrite', 'proofStep', 'uiResponse', 'observe', 'ui', 'mutation', 'capture'))
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

    def test_screenshot_uses_real_union_and_closed_diagnostics(self):
        self.assertEqual(screenshot_violations(self.source), [])

    def test_screenshot_cannot_weaken_fit_privacy_or_diagnostics(self):
        for old, new in (
            ('stage: CaptureStage,', 'stage: string,'),
            ('fact(captureStages.includes(stage), "screenshot_stage_invalid");', ''),
            ('"parent_desktop"', 'id'),
            ('bounds.right - bounds.left <= width && bounds.bottom - bounds.top <= height', 'true'),
            ('box.x >= 0 && box.y >= 0 && box.x + box.width <= width && box.y + box.height <= height', 'true'),
            ('fact(proof.unobscured, `screenshot_${slot}_${targetSlot}_text_obscured`);', ''),
            ('fact(proof.contained, `screenshot_${slot}_${targetSlot}_text_outside`);', ''),
            ('fact(proof.present, `screenshot_${slot}_${targetSlot}_text_missing`);', ''),
            ('for (const rect of range.getClientRects())', 'for (const rect of [])'),
            ('hit === element || element.contains(hit)', 'hit !== null'),
            ('fullPage: false', 'fullPage: true'),
            ('fact(empty, "proof_not_empty");', ''),
        ):
            self.assertTrue(screenshot_violations(self.source.replace(old, new)))
        for extra in ('await targets[0].scrollIntoViewIfNeeded();', 'element.style.display = "none";'):
            self.assertTrue(screenshot_violations(self.source + '\n' + extra))

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




# Exact additive lineage projection. New mutations act on CURRENT raw source.
LINEAGE_BASE_SHA256 = 'ecc83cb71fdefa736efada3b1120c275cc436125b9493e56b6f3e758502260ef'
LINEAGE_IMPORT = 'import { test, expect, type BrowserContext, type Page, type Request } from "@playwright/test";\n'
LINEAGE_STAGES = 'const UI_STAGES = ["browser_proof_or_observation", "recovery_intent_open", "recovery_warning_count",\n  "recovery_warning_text", "recovery_cancelled_key", "recovery_intent_cancel", "recovery_intent_closed",\n  "recovery_intent_reopen", "recovery_submitted_key", "navigation_open_ready", "navigation_back_visible",\n  "navigation_return_list", "navigation_open_detail", "navigation_restore_ready", "navigation_detail_visible",\n  "navigation_reload", "navigation_history_back", "navigation_history_forward",\n  "lineage_parent", "lineage_back", "lineage_forward"] as const;\n'
LINEAGE_HELPER = '  // First host-observed detail request, not first successful response. This is\n  // the existing Playwright/CDP JSON evidence level, not a native-fetch witness.\n  async function lineageDetail(expected: { id: string; state: "failed" | "succeeded";\n    attempt: 0 | 1; parentID: string; error: string }, visibility: "active" | "all",\n    action: () => Promise<unknown>) {\n    const timeout = Math.min(10000, remaining());\n    let armed = false, listenerFailed = false, taskWrites = 0;\n    let selected: Request | null = null;\n    const old = new Set<Request>();\n    const requestObserved = (request: Request) => {\n      try {\n        if (!armed) {\n          if (old.size >= 32) listenerFailed = true;\n          else old.add(request);\n          return;\n        }\n        const url = new URL(request.url()), method = request.method();\n        if ((url.pathname === "/api/tasks" || url.pathname.startsWith("/api/tasks/")) &&\n          !["GET", "HEAD", "OPTIONS"].includes(method)) taskWrites++;\n        if (old.has(request) || selected !== null || url.pathname !== "/api/tasks/detail") return;\n        selected = request;\n        const keys = [...url.searchParams.keys()].sort();\n        fact(url.origin === origin && url.hash === "" && url.username === "" && url.password === "" &&\n          method === "GET" && request.resourceType() === "fetch" && request.serviceWorker() === null &&\n          request.frame() === page.mainFrame() && request.redirectedFrom() === null && request.redirectedTo() === null &&\n          url.searchParams.getAll("taskUUID").length === 1 && url.searchParams.get("taskUUID") === expected.id &&\n          equal(keys, visibility === "all" ? ["taskUUID", "visibility"] : ["taskUUID"]) &&\n          (visibility === "all" ? url.searchParams.get("visibility") === "all" : !url.searchParams.has("visibility")),\n          "lineage_request_invalid");\n      } catch { listenerFailed = true; }\n    };\n    try {\n      page.on("request", requestObserved);\n      const readOutcome = page.waitForResponse(response => response.request() === selected, { timeout })\n        .then(async response => {\n          fact(!listenerFailed && response.status() === 200 && !response.fromServiceWorker() &&\n            response.request().redirectedFrom() === null && response.request().redirectedTo() === null,\n            "lineage_response_invalid");\n          const contentType = await response.headerValue("content-type");\n          fact(contentType !== null && /^application\\/json(?:;\\s*charset=utf-8)?$/i.test(contentType) &&\n            await response.headerValue("cache-control") === "no-store" &&\n            await response.headerValue("x-content-type-options") === "nosniff", "lineage_metadata_invalid");\n          const value = await response.json();\n          fact(object(value) && object(value.task) && Array.isArray(value.events) &&\n            value.task.taskUUID === expected.id && value.task.state === expected.state &&\n            value.task.attempt === expected.attempt && value.task.parentTaskUUID === expected.parentID &&\n            value.task.error === expected.error, "lineage_detail_invalid");\n        }).then(() => true, () => false);\n      armed = true;\n      let actionOutcome: Promise<boolean>;\n      try { actionOutcome = action().then(() => true, () => false); }\n      catch { actionOutcome = Promise.resolve(false); }\n      // Both branches already have rejection handlers, so join cannot abandon\n      // the other branch on first rejection. No retry or detached body read.\n      const [readOK, actionOK] = await Promise.all([readOutcome, actionOutcome]);\n      fact(readOK && actionOK && !listenerFailed, "lineage_observation_failed");\n      await visible(expected.id, expected.state, expected.attempt, expected.parentID);\n      fact(!listenerFailed && taskWrites === 0, "lineage_wrote_task");\n    } finally {\n      armed = false;\n      page.off("request", requestObserved);\n      selected = null;\n      old.clear();\n    }\n  }\n'
LINEAGE_PROBES = '    const lineageBefore = snapshot();\n    const parentExpected = { id: parent.task.id, state: "failed" as const, attempt: 0 as const,\n      parentID: "", error: "authorization_revoked" };\n    const childExpected = { id: child.task.id, state: "succeeded" as const, attempt: 1 as const,\n      parentID: parent.task.id, error: "" };\n    await ui(() => lineageDetail(parentExpected, "all", () =>\n      page.getByRole("button", { name: `查看父任务 ${parent.task.id}`, exact: true }).click()), "lineage_parent");\n    await ui(() => lineageDetail(childExpected, "active", () => page.goBack()), "lineage_back");\n    await ui(() => lineageDetail(parentExpected, "all", () => page.goForward()), "lineage_forward");\n    fact(equal(lineageBefore, snapshot()), "lineage_changed_persistence");\n'
LINEAGE_BASE_IMPORT = 'import { test, expect, type BrowserContext, type Page } from "@playwright/test";\n'
LINEAGE_BASE_STAGES = 'const UI_STAGES = ["browser_proof_or_observation", "recovery_intent_open", "recovery_warning_count",\n  "recovery_warning_text", "recovery_cancelled_key", "recovery_intent_cancel", "recovery_intent_closed",\n  "recovery_intent_reopen", "recovery_submitted_key", "navigation_open_ready", "navigation_back_visible",\n  "navigation_return_list", "navigation_open_detail", "navigation_restore_ready", "navigation_detail_visible",\n  "navigation_reload", "navigation_history_back", "navigation_history_forward"] as const;\n'


LINEAGE_MUTATIONS = (
    ('remaining_timeout', 'const timeout = Math.min(10000, remaining());', 'const timeout = 20000;'),
    ('bounded_old', 'if (old.size >= 32) listenerFailed = true;', 'if (false) listenerFailed = true;'),
    ('old_record', 'else old.add(request);', 'else old.clear();'),
    ('armed_gate', 'if (!armed) {', 'if (false) {'),
    ('no_old_upgrade', 'if (old.has(request) || selected !== null || url.pathname !== "/api/tasks/detail") return;', 'if (selected !== null || url.pathname !== "/api/tasks/detail") return;'),
    ('lock_first', 'if (old.has(request) || selected !== null || url.pathname !== "/api/tasks/detail") return;', 'if (old.has(request) || url.pathname !== "/api/tasks/detail") return;'),
    ('lock_before_validation', 'selected = request;', ''),
    ('no_success_prefilter', 'selected = request;', 'if (method !== "GET" || request.resourceType() !== "fetch") return; selected = request;'),
    ('canonical_origin', 'url.origin === origin && url.hash === ""', 'true'),
    ('no_url_credentials', 'url.username === "" && url.password === ""', 'true'),
    ('method', 'method === "GET" && request.resourceType() === "fetch"', 'request.resourceType() === "fetch"'),
    ('fetch', 'method === "GET" && request.resourceType() === "fetch"', 'method === "GET"'),
    ('worker', 'request.serviceWorker() === null', 'true'),
    ('frame', 'request.frame() === page.mainFrame()', 'true'),
    ('request_redirect', 'request.redirectedFrom() === null && request.redirectedTo() === null', 'true'),
    ('unique_id', 'url.searchParams.getAll("taskUUID").length === 1', 'true'),
    ('requested_id', 'url.searchParams.get("taskUUID") === expected.id', 'true'),
    ('exact_query', 'equal(keys, visibility === "all" ? ["taskUUID", "visibility"] : ["taskUUID"])', 'true'),
    ('visibility', '(visibility === "all" ? url.searchParams.get("visibility") === "all" : !url.searchParams.has("visibility"))', 'true'),
    ('listener_error', '} catch { listenerFailed = true; }', '} catch { listenerFailed = false; }'),
    ('raw_listener_error', '} catch { listenerFailed = true; }', '} catch (error) { throw error; }'),
    ('listener_install', 'page.on("request", requestObserved);', ''),
    ('same_request_response', 'response => response.request() === selected', 'response => new URL(response.url()).pathname === "/api/tasks/detail"'),
    ('first_not_successful', 'response => response.request() === selected', 'response => response.status() === 200'),
    ('invalid_request_fails', 'fact(!listenerFailed && response.status() === 200 && !response.fromServiceWorker() &&', 'fact(response.status() === 200 && !response.fromServiceWorker() &&'),
    ('response_status', 'response.status() === 200 && !response.fromServiceWorker()', '!response.fromServiceWorker()'),
    ('response_worker', 'response.status() === 200 && !response.fromServiceWorker()', 'response.status() === 200'),
    ('response_redirect', 'response.request().redirectedFrom() === null && response.request().redirectedTo() === null', 'true'),
    ('response_json_metadata', 'contentType !== null && /^application\\/json(?:;\\s*charset=utf-8)?$/i.test(contentType)', 'true'),
    ('no_store', 'await response.headerValue("cache-control") === "no-store"', 'true'),
    ('no_sniff', 'await response.headerValue("x-content-type-options") === "nosniff"', 'true'),
    ('json_once', 'const value = await response.json();', 'await response.json(); const value = await response.json();'),
    ('json_failure_no_fallback', 'const value = await response.json();', 'const value = await response.json().catch(() => ({ task: expected, events: [] }));'),
    ('body_shape', 'object(value) && object(value.task) && Array.isArray(value.events)', 'true'),
    ('body_identity', 'value.task.taskUUID === expected.id', 'true'),
    ('body_state', 'value.task.state === expected.state', 'true'),
    ('body_attempt', 'value.task.attempt === expected.attempt', 'true'),
    ('body_parent', 'value.task.parentTaskUUID === expected.parentID', 'true'),
    ('body_error', 'value.task.error === expected.error', 'true'),
    ('read_immediate_handler', '}).then(() => true, () => false);', '}).then(() => true);'),
    ('one_action', 'try { actionOutcome = action().then(() => true, () => false); }', 'try { await action(); actionOutcome = action().then(() => true, () => false); }'),
    ('action_immediate_handler', 'try { actionOutcome = action().then(() => true, () => false); }', 'try { actionOutcome = action().then(() => true); }'),
    ('sync_action_failure', 'catch { actionOutcome = Promise.resolve(false); }', 'catch { actionOutcome = Promise.resolve(true); }'),
    ('join_both', 'await Promise.all([readOutcome, actionOutcome]);', 'await Promise.race([readOutcome, actionOutcome]);'),
    ('action_must_succeed', 'fact(readOK && actionOK && !listenerFailed, "lineage_observation_failed");', 'fact(readOK && !listenerFailed, "lineage_observation_failed");'),
    ('read_must_succeed', 'fact(readOK && actionOK && !listenerFailed, "lineage_observation_failed");', 'fact(actionOK && !listenerFailed, "lineage_observation_failed");'),
    ('exact_dom', 'await visible(expected.id, expected.state, expected.attempt, expected.parentID);', ''),
    ('no_task_writes', 'fact(!listenerFailed && taskWrites === 0, "lineage_wrote_task");', ''),
    ('count_writes', '!["GET", "HEAD", "OPTIONS"].includes(method)) taskWrites++;', '!["GET", "HEAD", "OPTIONS"].includes(method)) taskWrites = 0;'),
    ('listener_remove', 'page.off("request", requestObserved);', ''),
    ('clear_selected', 'selected = null;', ''),
    ('clear_old', 'old.clear();', ''),
    ('parent_button_only', 'page.getByRole("button", { name: `查看父任务 ${parent.task.id}`, exact: true }).click()', 'openDetail(parent.task.id)'),
    ('parent_exact_name', 'name: `查看父任务 ${parent.task.id}`, exact: true', 'name: `查看父任务 ${child.task.id}`, exact: true'),
    ('parent_mode', 'lineageDetail(parentExpected, "all", () =>\n', 'lineageDetail(parentExpected, "active", () =>\n'),
    ('back_exact', 'lineageDetail(childExpected, "active", () => page.goBack())', 'lineageDetail(parentExpected, "active", () => page.goBack())'),
    ('back_mode', 'lineageDetail(childExpected, "active", () => page.goBack())', 'lineageDetail(childExpected, "all", () => page.goBack())'),
    ('back_no_fallback', 'lineageDetail(childExpected, "active", () => page.goBack())', 'lineageDetail(childExpected, "active", () => restoredDetail(child.task.id))'),
    ('forward_no_fallback', 'lineageDetail(parentExpected, "all", () => page.goForward())', 'lineageDetail(parentExpected, "all", () => openDetail(parent.task.id))'),
    ('persistence_before', 'const lineageBefore = snapshot();', 'const lineageBefore = {};'),
    ('persistence_after', 'fact(equal(lineageBefore, snapshot()), "lineage_changed_persistence");', ''),
    ('parent_expected_error', 'parentID: "", error: "authorization_revoked" };', 'parentID: "", error: "" };'),
    ('child_expected_parent', 'parentID: parent.task.id, error: "" };', 'parentID: child.task.id, error: "" };'),
    ('closed_parent_phase', '}).click()), "lineage_parent");', '}).click()), parent.task.id);'),
)

LINEAGE_MOVES = (
    ('arm_after_action', '      armed = true;\n', '      try { actionOutcome = action().then(() => true, () => false); }\n      catch { actionOutcome = Promise.resolve(false); }\n'),
    ('lock_after_validation', '        selected = request;\n', '          "lineage_request_invalid");\n'),
    ('listener_removed_before_join', '      page.off("request", requestObserved);\n', '      armed = true;\n'),
    ('zero_writes_before_dom', '      fact(!listenerFailed && taskWrites === 0, "lineage_wrote_task");\n', '      fact(readOK && actionOK && !listenerFailed, "lineage_observation_failed");\n'),
)

def restore_lineage_source(source):
    if not source.startswith(LINEAGE_IMPORT):
        raise ValueError('lineage_import_position')
    for shape in (LINEAGE_STAGES, LINEAGE_HELPER, LINEAGE_PROBES):
        if source.count(shape) != 1:
            raise ValueError('lineage_shape')
    if source.count(LINEAGE_HELPER + '  const screenshots: Record<string, unknown> = {};') != 1:
        raise ValueError('lineage_helper_position')
    if source.count(LINEAGE_PROBES + '    fact(recoveryPosts === count && count === 1, "navigation_recovered_again");') != 1:
        raise ValueError('lineage_probes_position')
    restored = source
    for added, original in ((LINEAGE_IMPORT, LINEAGE_BASE_IMPORT), (LINEAGE_STAGES, LINEAGE_BASE_STAGES),
                            (LINEAGE_HELPER, ''), (LINEAGE_PROBES, '')):
        if restored.count(added) != 1:
            raise ValueError('lineage_addition_count')
        restored = restored.replace(added, original, 1)
    if hashlib.sha256(restored.encode('utf-8')).hexdigest() != LINEAGE_BASE_SHA256:
        raise ValueError('lineage_historical_bytes')
    return restored


class ParentLineageContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # read_text() would normalize physical CRLF and weaken the byte contract.
        cls.source = SPEC.read_bytes().decode('utf-8')

    def test_exact_lineage_additions_preserve_original_source(self):
        restored = restore_lineage_source(self.source)
        self.assertEqual(hashlib.sha256(restored.encode('utf-8')).hexdigest(), LINEAGE_BASE_SHA256)
        self.assertEqual(privacy_violations(self.source), [])
        self.assertEqual(recovery_dialog_violations(self.source), [])
        self.assertEqual(navigation_readiness_violations(self.source), [])
        self.assertEqual(screenshot_violations(self.source), [])
        self.assertEqual(scenario_violations(self.source), [])

    def test_lineage_addition_removal_duplicate_or_relocation_is_rejected(self):
        for label, shape in (('import', LINEAGE_IMPORT), ('stages', LINEAGE_STAGES),
                             ('helper', LINEAGE_HELPER), ('probes', LINEAGE_PROBES)):
            self.assertEqual(self.source.count(shape), 1, label)
            for mode, changed in (
                ('missing', self.source.replace(shape, '', 1)),
                ('duplicate', self.source.replace(shape, shape + shape, 1)),
                ('moved', self.source.replace(shape, '', 1) + shape),
            ):
                with self.subTest(label=label, mode=mode), self.assertRaises(ValueError):
                    restore_lineage_source(changed)

    def test_lineage_safety_decisions_on_actual_source(self):
        for label, before, after in LINEAGE_MUTATIONS:
            with self.subTest(label=label):
                self.assertEqual(self.source.count(before), 1, label)
                self.assertNotEqual(before, after)
                with self.assertRaises(ValueError):
                    restore_lineage_source(self.source.replace(before, after, 1))

    def test_lineage_async_ordering_on_actual_source(self):
        for label, moved, destination in LINEAGE_MOVES:
            with self.subTest(label=label):
                self.assertEqual(self.source.count(moved), 1, label)
                self.assertEqual(self.source.count(destination), 1, label)
                changed = self.source.replace(moved, '', 1).replace(destination, destination + moved, 1)
                self.assertNotEqual(changed, self.source)
                with self.assertRaises(ValueError):
                    restore_lineage_source(changed)

    def test_lineage_projection_rejects_legacy_bytes_and_line_endings_drift(self):
        for label, changed in (
            ('physical_crlf', self.source.replace('\n', '\r\n')),
            ('extra_newline', self.source + '\n'),
            ('suite_budget', self.source.replace('test.setTimeout(600000)', 'test.setTimeout(600001)', 1)),
            ('lifecycle_budget', self.source.replace('phase("replayLifecycleEvidence", 60000', 'phase("replayLifecycleEvidence", 60001', 1)),
            ('proof_budget', self.source.replace('sensitiveActions === 7', 'sensitiveActions === 8', 1)),
            ('screenshot_target', self.source.replace('page.locator(".task-parent-identity"), page.getByLabel("任务实际结果")', 'page.getByLabel("任务实际结果")', 1)),
            ('cleanup', self.source.replace('await safe("browser_context_close", () => context.close());', '', 1)),
        ):
            with self.subTest(label=label):
                self.assertNotEqual(changed, self.source)
                with self.assertRaises(ValueError):
                    restore_lineage_source(changed)


if __name__ == '__main__':
    unittest.main()
