import { test, expect, type BrowserContext, type Page, type Request } from "@playwright/test";
import { createHash, createHmac, randomBytes } from "node:crypto";
import { execFileSync } from "node:child_process";
import { lstatSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { resolve, relative } from "node:path";
import { navigateTo } from "./navigation";

// Source-only draft. The dedicated runner integration is REQUIRED, not mocked.
// No browser-produced cleanup receipt. Final acceptance requires the runner's
// independent post-finally receipt plus source/file-byte validation.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const SHA = /^[0-9a-f]{40}$/;
const UI_STAGES = ["browser_proof_or_observation", "recovery_intent_open", "recovery_warning_count",
  "recovery_warning_text", "recovery_cancelled_key", "recovery_intent_cancel", "recovery_intent_closed",
  "recovery_intent_reopen", "recovery_submitted_key", "navigation_open_ready", "navigation_back_visible",
  "navigation_return_list", "navigation_open_detail", "navigation_restore_ready", "navigation_detail_visible",
  "navigation_reload", "navigation_history_back", "navigation_history_forward",
  "lineage_parent", "lineage_back", "lineage_forward"] as const;
type UIStage = typeof UI_STAGES[number];
const digest = (b: Buffer | string) => createHash("sha256").update(b).digest("hex");
// JSONB object key order is not semantic. Arrays retain their original order,
// while object comparison uses the full recursive key set without raw diffs.
function equal(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a === null || b === null || typeof a !== "object" || typeof b !== "object") return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((v, i) => equal(v, b[i]));
  }
  const left = a as Record<string, unknown>, right = b as Record<string, unknown>;
  const keys = Object.keys(left).sort(), other = Object.keys(right).sort();
  return keys.length === other.length && keys.every((key, i) => key === other[i] && equal(left[key], right[key]));
}
function fact(ok: unknown, category: string): asserts ok { if (!ok) throw new Error(category); }
async function safe<T>(category: string, work: () => Promise<T>): Promise<T> {
  try { return await work(); } catch { throw new Error(category); }
}
function object(v: any): boolean { return !!v && typeof v === "object" && !Array.isArray(v); }
function chromiumEnvironment(): Record<string, string> {
  return Object.fromEntries(Object.entries(process.env).filter(
    (x): x is [string, string] => x[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(x[0]),
  ));
}
test.use({ trace: "off", video: "off", screenshot: "off", launchOptions: {
  env: chromiumEnvironment(),
  ...(process.env.ADTR_E2E_CHROMIUM_PATH ? { executablePath: process.env.ADTR_E2E_CHROMIUM_PATH } : {}),
} });
function totp(secret: string, now: number): string {
  let bits = "";
  for (const c of secret.replace(/=+$/, "").toUpperCase()) {
    const n = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567".indexOf(c);
    fact(n >= 0, "mfa_secret_shape"); bits += n.toString(2).padStart(5, "0");
  }
  const bytes: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const step = Buffer.alloc(8); step.writeBigUInt64BE(BigInt(Math.floor(now / 30000)));
  const h = createHmac("sha1", Buffer.from(bytes)).update(step).digest();
  return ((h.readUInt32BE(h[h.length - 1] & 15) & 0x7fffffff) % 1000000).toString().padStart(6, "0");
}

test("real health epoch rejection, explicit child recovery and original-key replay", async ({ browser }, testInfo) => {
  test.setTimeout(600000);
  const started = Date.now();
  const base = process.env.ADTR_E2E_BASE_URL;
  const username = process.env.ADTR_E2E_USERNAME;
  const initial = process.env.ADTR_E2E_PASSWORD;
  const container = process.env.ADTR_E2E_DB_CONTAINER;
  const control = process.env.ADTR_E2E_V4_WORKER_CONTROL_URL;
  const token = process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN;
  const nonce = process.env.ADTR_E2E_HEALTH_RUN_NONCE;
  fact(base && username && initial && container && control && token && nonce, "dedicated_runner_contract_missing");
  fact(/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(control), "controller_origin_invalid");
  fact(/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(container), "owned_container_invalid");
  fact(/^[a-zA-Z0-9_-]{16,128}$/.test(nonce), "run_nonce_invalid");
  const origin = new URL(base).origin;
  fact(new URL(base).hostname === "127.0.0.1", "owned_api_origin_invalid");
  const context = await safe("browser_context", () => browser.newContext({ baseURL: base, viewport: { width: 1440, height: 900 } }));
  const page = await safe("browser_page", () => context.newPage());
  const changed = `Synthetic health A ${randomBytes(12).toString("hex")}`;
  const current = `Synthetic health B ${randomBytes(12).toString("hex")}`;
  const posts = { initialPassword: 0, mfaStart: 0, mfaConfirm: 0, submit: 0, changePassword: 0, recover: 0, replay: 0, controllerStart: 0 };
  const reads = { authMe: 0, oldSession: 0, taskDetail: 0, db: 0, controllerState: 0 };
  let unknownWrites = 0, halted = false, sensitiveActions = 0, counterTransitions = 0, counterWaitMs = 0;
  let csrf = "", actorID = 0, secret = "", counter = -1, recoveryPosts = 0;
  let submittedKey = "", cancelledKey = "", submissionKey = "";
  let phaseDeadline = started + 75000;
  const phases: Record<string, number> = {};
  // The listener counts only one fixed route; it never reads proof bodies/headers.
  page.on("request", request => {
    try { if (request.method() === "POST" && new URL(request.url()).pathname === "/api/tasks/recover") recoveryPosts++; }
    catch { halted = true; }
  });
  function remaining() {
    const value = Math.min(started + 600000, phaseDeadline) - Date.now();
    fact(value > 0, "aggregate_phase_deadline"); return Math.max(1, value);
  }
  async function ui<T>(work: () => Promise<T>, stage: UIStage = "browser_proof_or_observation"): Promise<T> {
    fact(UI_STAGES.includes(stage), "ui_stage_invalid");
    remaining(); page.setDefaultTimeout(Math.min(10000, remaining()));
    const value = await safe(stage, work); remaining(); return value;
  }
  async function phase<T>(name: string, budget: number, work: () => Promise<T>): Promise<T> {
    const begin = Date.now(), waits = counterWaitMs; phaseDeadline = begin + budget;
    const value = await work();
    phases[name] = Date.now() - begin - (counterWaitMs - waits);
    fact(phases[name] <= budget, "aggregate_phase_deadline"); return value;
  }
  async function freshCode() {
    fact(++counterTransitions <= 4, "counter_transition_budget");
    const begin = Date.now(), until = (counter + 1) * 30000 + 1100;
    if (Date.now() < until) await new Promise(r => setTimeout(r, until - Date.now()));
    const spent = Date.now() - begin; counterWaitMs += spent; phaseDeadline += spent;
    fact(spent <= 31100 && counterWaitMs <= 124400, "counter_wait_budget");
    remaining(); const now = Date.now(); counter = Math.floor(now / 30000); return totp(secret, now);
  }
  function sql(query: string): any {
    reads.db++; remaining();
    try {
      return JSON.parse(execFileSync("docker", ["exec", "-e", "PGOPTIONS=-c default_transaction_read_only=on",
        container!, "psql", "-U", "postgres", "-d", "adtr_e2e", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query],
      { encoding: "utf8", timeout: Math.min(10000, remaining()), stdio: ["ignore", "pipe", "pipe"] }));
    } catch { throw new Error("owned_readonly_observation_failed"); }
  }
  function actor() {
    fact(Number.isSafeInteger(actorID) && actorID > 0, "actor_identity_invalid");
    const rows = sql(`SELECT json_agg(p) FROM (SELECT id,tenant_id AS "tenantID",authorization_version::text AS epoch,
      password_updated_at AS "passwordUpdatedAt" FROM adtr.users WHERE id=${actorID}) p`);
    fact(Array.isArray(rows) && rows.length === 1 && /^[1-9][0-9]*$/.test(rows[0].epoch), "actor_epoch_invalid");
    return rows[0];
  }
  function snapshot() {
    fact(actorID > 0, "actor_identity_invalid");
    const tasks = sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (SELECT t.task_id AS id,t.tenant_id AS "tenantID",
      t.domain_id AS "domainID",t.kind,t.payload_version AS "payloadVersion",t.payload,t.payload_hash AS "payloadHash",t.actor_id AS "actorID",
      t.authorization_version AS epoch,t.idempotency_key AS key,t.state,t.attempt,t.max_attempts AS "maxAttempts",
      t.progress,t.result_version AS "resultVersion",t.error_code AS error,t.parent_task_id AS "parentID",t.result,t.cursor,
      t.created_at AS "createdAt",t.updated_at AS "updatedAt",t.terminal_at AS "terminalAt",
      t.lease_owner AS "leaseOwner",t.lease_until AS "leaseUntil",t.fencing_token AS "fencingToken",t.next_attempt_at AS "nextAttemptAt",
      COALESCE(v.archived,false) AS archived,COALESCE(v.visibility_version,0) AS "visibilityVersion"
      FROM adtr.tasks t LEFT JOIN adtr.task_visibility v ON v.task_id=t.task_id AND v.tenant_id=t.tenant_id
      WHERE t.actor_id=${actorID} ORDER BY t.task_id) p`);
    const events = sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (SELECT id,task_id AS "taskID",tenant_id AS "tenantID",
      domain_id AS "domainID",actor_id AS "actorID",action,state,attempt,result_version AS "resultVersion",
      authorization_version AS epoch,fencing_token AS "fencingToken",occurred_at AS "occurredAt"
      FROM adtr.task_events WHERE actor_id=${actorID} ORDER BY id) p`);
    const outbox = sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (SELECT o.id,o.task_id AS "taskID",o.event_id AS "eventID",
      o.occurred_at AS "occurredAt" FROM adtr.task_outbox o JOIN adtr.tasks t ON t.task_id=o.task_id
      WHERE t.actor_id=${actorID} ORDER BY o.id) p`);
    fact(Array.isArray(tasks) && Array.isArray(events) && Array.isArray(outbox) && tasks.length <= 2, "owned_snapshot_shape");
    return { tasks, events, outbox };
  }
  function one(id: string) {
    fact(UUID.test(id), "task_identity_invalid"); const all = snapshot();
    const selected = all.tasks.filter((t: any) => t.id === id); fact(selected.length === 1, "task_missing");
    const task = selected[0];
    fact(task.leaseOwner === "" && task.leaseUntil === null && task.nextAttemptAt === null &&
      task.fencingToken === (task.state === "queued" ? 0 : 1) &&
      task.payloadHash === digest(`1:${task.parentID}:{}`), "stable_task_fields_invalid");
    return { task: selected[0], events: all.events.filter((e: any) => e.taskID === id), outbox: all.outbox.filter((o: any) => o.taskID === id) };
  }
  function zero(key: string) {
    fact(UUID.test(key), "key_shape"); const s = snapshot();
    const ids = s.tasks.filter((t: any) => t.key === key).map((t: any) => t.id);
    const value = { taskCount: ids.length, eventCount: s.events.filter((e: any) => ids.includes(e.taskID)).length };
    fact(value.taskCount === 0 && value.eventCount === 0, "cancelled_intent_wrote"); return value;
  }
  async function get(path: string, target: BrowserContext = context): Promise<any> {
    return safe("authenticated_read_failed", async () => {
      remaining(); const r = await target.request.get(path, { timeout: Math.min(5000, remaining()), maxRetries: 0 });
      fact(r.status() === 200, "authenticated_read_status"); return r.json();
    });
  }
  async function profile() {
    reads.authMe++; const v = await get("/api/auth/me");
    fact(object(v) && Number.isSafeInteger(v.ID) && v.ID > 0 && v.hasMfa === true && v.needChangePwd === false &&
      v.isExpired === false && typeof v.csrfToken === "string" && v.csrfToken.length > 0, "current_authority_invalid");
    if (actorID) fact(v.ID === actorID, "actor_changed"); actorID = v.ID; csrf = v.csrfToken;
  }
  async function uncertain(inspect?: () => Promise<unknown>) {
    halted = true; unknownWrites++;
    // Read original state once; never repeat a password, proof or admission.
    let observed = false;
    try { if (inspect) { await inspect(); observed = true; } } catch { observed = false; }
    testInfo.annotations.push({ type: "unknown_write", description: observed ? "original_state_observed_no_retry" : "original_state_unknown_no_retry" });
  }
  async function mutation(name: keyof typeof posts, path: string, action: () => Promise<unknown>, inspect?: () => Promise<unknown>) {
    fact(!halted && posts[name] === 0, "mutation_forbidden"); posts[name]++;
    if (name !== "controllerStart") fact(++sensitiveActions <= 7, "sensitive_action_budget");
    try {
      return await safe("authenticated_ui_write_failed", async () => {
        const [r] = await Promise.all([page.waitForResponse(r => new URL(r.url()).pathname === path && r.request().method() === "POST",
          { timeout: Math.min(10000, remaining()) }), ui(action)]);
        fact(r.status() === 200, "authenticated_ui_write_status"); return r.json();
      });
    } catch { await uncertain(inspect); throw new Error("authenticated_ui_write_failed"); }
  }
  function receipt(value: any, replayed: boolean, key: string) {
    fact(UUID.test(key) && object(value) && value.replayed === replayed && object(value.task), "task_receipt_shape");
    const t = value.task;
    fact(UUID.test(t.taskUUID) && (t.parentTaskUUID === "" || UUID.test(t.parentTaskUUID)) &&
      ["queued", "running", "failed", "succeeded"].includes(t.state) && Number.isSafeInteger(t.attempt) &&
      Number.isSafeInteger(t.resultVersion), "task_receipt_shape");
    return { status: 200, replayed, key, taskID: t.taskUUID, parentID: t.parentTaskUUID,
      state: t.state, attempt: t.attempt, resultVersion: t.resultVersion };
  }
  async function detail(id: string) {
    fact(UUID.test(id), "task_identity_invalid"); reads.taskDetail++;
    const v = await get(`/api/tasks/detail?taskUUID=${id}`);
    fact(object(v) && object(v.task) && v.task.taskUUID === id && Array.isArray(v.events), "task_detail_shape"); return v;
  }
  async function controller(method: "GET" | "POST") {
    if (method === "POST") { fact(!halted && posts.controllerStart === 0, "controller_start_duplicate"); posts.controllerStart++; }
    else reads.controllerState++;
    try { return await safe("controller_transport_failed", async () => {
      const response = await context.request.fetch(`${control}/${method === "GET" ? "state" : "start"}`, {
        method, headers: { Authorization: `Bearer ${token}` }, timeout: Math.min(5000, remaining()), maxRetries: 0,
      });
      fact(response.status() === 200, "controller_status"); const v = await response.json();
      fact(object(v) && ["not_started", "starting", "ready", "failed", "closed"].includes(v.state) &&
        Number.isInteger(v.launchCount) && v.launchCount >= 0 && v.launchCount <= 1 && v.failure === null, "controller_state_invalid");
      return { state: v.state, launchCount: v.launchCount };
    }); } catch {
      if (method === "POST") await uncertain(async () => { await controller("GET"); snapshot(); });
      throw new Error("controller_transport_failed");
    }
  }
  async function visible(id: string, state: string, attempt: number, parentID = "") {
    await ui(async () => {
      await expect(page.getByTestId("task-detail-identity")).toHaveText(id);
      await expect(page.locator(`.task-state[data-state="${state}"]`)).toBeVisible();
      const metadata = page.locator(".task-metadata-panel");
      if (!await metadata.evaluate(e => (e as HTMLDetailsElement).open)) await metadata.locator("summary").click();
      await expect(metadata).toContainText(`${attempt}/5（含首次）`);
      if (state === "failed") await expect(page.getByRole("region", { name: "任务执行详情" })).toContainText("authorization_revoked");
      if (parentID) await expect(page.locator(".task-parent-identity")).toContainText(parentID);
      if (state === "succeeded") {
        const text = await page.getByLabel("任务实际结果").innerText();
        const value = JSON.parse(text); fact(object(value) && value.database === "ready" && value.queue === "ready" && Object.keys(value).length === 2, "rendered_result_invalid");
      }
    });
  }
  async function terminal(id: string, state: string) {
    while (true) {
      const v = await detail(id);
      if (v.task.state === state) break;
      fact(v.task.state === "queued" || v.task.state === "running", "unexpected_terminal_state");
      await new Promise(r => setTimeout(r, Math.min(300, remaining())));
    }
    await ui(() => page.getByRole("button", { name: "刷新任务详情", exact: true }).click());
  }
  function history(v: any, actions: string[], attempts: number[]) {
    fact(equal(v.events.map((e: any) => e.action), actions) && equal(v.events.map((e: any) => e.attempt), attempts), "event_history_invalid");
    fact(v.outbox.length === actions.length && new Set(v.outbox.map((o: any) => o.id)).size === actions.length &&
      equal(v.outbox.map((o: any) => o.eventID).sort((a: number,b: number) => a-b), v.events.map((e: any) => e.id)), "outbox_linkage_invalid");
  }
  await phase("setup", 75000, async () => {
    await ui(async () => {
      await page.goto("/");
      await page.getByLabel("用户名", { exact: true }).fill(username);
      await page.getByLabel("密码", { exact: true }).fill(initial);
      const [r] = await Promise.all([page.waitForResponse(r => new URL(r.url()).pathname === "/api/auth/login" && r.request().method() === "POST"),
        page.getByRole("button", { name: "登录", exact: true }).click()]);
      fact(r.status() === 200, "login_failed");
      await expect(page.getByRole("heading", { name: "修改密码", exact: true })).toBeVisible();
    });
    await mutation("initialPassword", "/api/auth/password", async () => {
      await page.getByLabel("当前密码", { exact: true }).fill(initial);
      await page.getByLabel("新密码", { exact: true }).fill(changed);
      await page.getByLabel("确认新密码", { exact: true }).fill(changed);
      await page.getByRole("button", { name: "更新密码", exact: true }).click();
    });
    await ui(async () => {
      await expect(page.getByRole("heading", { name: "账户概览", exact: true })).toBeVisible();
      await navigateTo(page, "多因素认证");
    });
    await mutation("mfaStart", "/api/auth/mfa/enroll", async () => {
      await page.getByLabel("当前密码", { exact: true }).fill(changed);
      await page.getByRole("button", { name: "开始设置", exact: true }).click();
    });
    await ui(async () => {
      secret = await page.getByLabel("设置密钥", { exact: true }).inputValue();
      fact(/^[A-Z2-7=]{16,128}$/.test(secret), "mfa_secret_shape");
    });
    const enrollmentNow = Date.now(); counter = Math.floor(enrollmentNow / 30000);
    await mutation("mfaConfirm", "/api/auth/mfa/confirm", async () => {
      await page.getByLabel("认证器验证码", { exact: true }).fill(totp(secret, enrollmentNow));
      await page.getByRole("button", { name: "验证并启用", exact: true }).click();
    });
    await ui(() => expect(page.getByRole("status")).toContainText("多因素认证已启用"));
    await profile();
    fact(snapshot().tasks.length === 0, "fixture_not_empty");
  });
  const beforeActor = actor();
  let queued: any, submitReceipt: any, controllerBefore: any, epochAfter = "";
  let session: any;
  await phase("queuedEpoch", 60000, async () => {
    await ui(async () => {
      await navigateTo(page, "后台任务");
      await page.getByRole("button", { name: "提交健康检查", exact: true }).click();
      submissionKey = await page.getByLabel("提交幂等键", { exact: true }).inputValue();
      fact(UUID.test(submissionKey), "submission_key_shape");
    });
    const code = await freshCode();
    const admitted = await mutation("submit", "/api/tasks/submit", async () => {
      await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
      await page.getByLabel("未使用的认证器验证码", { exact: true }).fill(code);
      await page.getByRole("button", { name: "确认提交健康检查", exact: true }).click();
    }, async () => snapshot());
    submitReceipt = receipt(admitted, false, submissionKey);
    fact(submitReceipt.parentID === "" && submitReceipt.state === "queued" && submitReceipt.attempt === 0, "queued_receipt_invalid");
    queued = one(submitReceipt.taskID);
    fact(queued.task.epoch === beforeActor.epoch && queued.task.key === submissionKey && queued.task.payloadVersion === 1 &&
      queued.task.kind === "infrastructure.health" && queued.task.domainID === "platform" && queued.task.actorID === actorID &&
      queued.task.tenantID === beforeActor.tenantID && equal(queued.task.payload, {}) && queued.task.state === "queued" && queued.task.attempt === 0,
      "queued_admission_invalid");
    history(queued, ["submitted"], [0]);
    await visible(submitReceipt.taskID, "queued", 0);
    controllerBefore = await controller("GET");
    fact(controllerBefore.state === "not_started" && controllerBefore.launchCount === 0, "worker_started_early");
    // No serialized auth state is ever written or attached. A separate memory-only
    // context performs exactly one old-session GET after password rotation.
    const oldState = await safe("session_snapshot", () => context.storageState());
    const oldContext = await safe("old_session_context", () => browser.newContext({ baseURL: base, storageState: oldState }));
    const oldCsrf = csrf;
    try {
      await ui(() => navigateTo(page, "修改密码"));
      await mutation("changePassword", "/api/auth/password", async () => {
        await page.getByLabel("当前密码", { exact: true }).fill(changed);
        await page.getByLabel("新密码", { exact: true }).fill(current);
        await page.getByLabel("确认新密码", { exact: true }).fill(current);
        await page.getByRole("button", { name: "更新密码", exact: true }).click();
      }, async () => { actor(); reads.authMe++; await get("/api/auth/me"); });
      const afterActor = actor(); epochAfter = afterActor.epoch;
      fact(BigInt(epochAfter) > BigInt(beforeActor.epoch) && afterActor.passwordUpdatedAt !== beforeActor.passwordUpdatedAt,
        "password_epoch_not_rotated");
      reads.oldSession++;
      const oldStatus = await safe("old_session_probe", async () => {
        const r = await oldContext.request.get("/api/auth/me", { timeout: Math.min(5000, remaining()), maxRetries: 0 });
        return r.status();
      });
      fact(oldStatus === 401, "old_session_still_authorized");
      await profile();
      const newState = await safe("session_snapshot", () => context.storageState());
      // Compare session cookie values in memory, never include either value/hash.
      const oldCookies = oldState.cookies.filter(c => c.httpOnly).map(c => c.value);
      const newCookies = newState.cookies.filter(c => c.httpOnly).map(c => c.value);
      fact(oldCookies.length > 0 && newCookies.length > 0 && !equal(oldCookies, newCookies) && csrf !== oldCsrf, "session_not_rotated");
      session = { oldSessionStatus: oldStatus, replacementSameActor: afterActor.id === actorID,
        rotated: true, csrfRefreshed: true };
      await ui(async () => { await page.reload(); await navigateTo(page, "后台任务"); });
      await openDetail(submitReceipt.taskID);
    } finally { await safe("old_session_close", () => oldContext.close()); }
  });
  let controllerAfter: any;
  await phase("workerReadiness", 110000, async () => {
    const still = await controller("GET");
    fact(still.state === "not_started" && still.launchCount === 0, "worker_started_early");
    controllerAfter = await controller("POST");
    while (controllerAfter.state === "starting") {
      await new Promise(r => setTimeout(r, Math.min(300, remaining())));
      controllerAfter = await controller("GET");
    }
    fact(controllerAfter.state === "ready" && controllerAfter.launchCount === 1, "worker_not_ready");
  });
  let parent: any;
  await phase("parentTerminal", 30000, async () => {
    await terminal(submitReceipt.taskID, "failed");
    parent = one(submitReceipt.taskID);
    fact(parent.task.state === "failed" && parent.task.error === "authorization_revoked" && parent.task.attempt === 0 &&
      parent.task.epoch === beforeActor.epoch && parent.task.resultVersion > queued.task.resultVersion && parent.task.terminalAt,
      "parent_rejection_invalid");
    history(parent, ["submitted", "leased", "authorization_rejected"], [0, 0, 0]);
    await visible(parent.task.id, "failed", 0);
  });
  let child: any, recoverReceipt: any, parentAfterChild: any, cancelledBefore: any;
  await phase("recoveryChild", 30000, async () => {
    await ui(() => page.getByRole("button", { name: "创建恢复任务", exact: true }).click(), "recovery_intent_open");
    // ProofFields has its own nested warning. Match only TaskAction's warning
    // rather than allowing a two-element locator or choosing an arbitrary first.
    await ui(() => expect(page.locator(".task-action > .warning")).toHaveCount(1), "recovery_warning_count");
    await ui(() => expect(page.locator(".task-action > .warning")).toContainText("不修改原任务或重置其历史"), "recovery_warning_text");
    await ui(async () => {
      cancelledKey = await page.getByLabel("恢复幂等键", { exact: true }).inputValue();
      fact(UUID.test(cancelledKey), "cancelled_key_shape");
    }, "recovery_cancelled_key");
    await ui(() => page.locator(".task-action").getByRole("button", { name: "取消", exact: true }).click(), "recovery_intent_cancel");
    await ui(() => expect(page.getByLabel("恢复幂等键", { exact: true })).toHaveCount(0), "recovery_intent_closed");
    fact(recoveryPosts === 0, "cancel_sent_recovery"); cancelledBefore = zero(cancelledKey);
    await ui(() => page.getByRole("button", { name: "创建恢复任务", exact: true }).click(), "recovery_intent_reopen");
    await ui(async () => {
      submittedKey = await page.getByLabel("恢复幂等键", { exact: true }).inputValue();
      fact(UUID.test(submittedKey) && submittedKey !== cancelledKey && submittedKey !== submissionKey, "reopened_key_invalid");
    }, "recovery_submitted_key");
    const code = await freshCode();
    const response = await mutation("recover", "/api/tasks/recover", async () => {
      // Reread actual key immediately before confirming the reopened intent.
      const actual = await page.getByLabel("恢复幂等键", { exact: true }).inputValue();
      fact(actual === submittedKey, "recovery_key_changed");
      await page.getByLabel("操作者当前密码", { exact: true }).fill(current);
      await page.getByLabel("未使用的认证器验证码", { exact: true }).fill(code);
      await page.getByRole("button", { name: "确认恢复任务", exact: true }).click();
    }, async () => { const s = snapshot(); fact(s.tasks.filter((t: any) => t.key === submittedKey && t.parentID === parent.task.id).length === 1, "recovery_outcome_unknown"); });
    recoverReceipt = receipt(response, false, submittedKey);
    fact(recoverReceipt.parentID === parent.task.id && recoverReceipt.taskID !== parent.task.id, "child_link_invalid");
    await terminal(recoverReceipt.taskID, "succeeded"); child = one(recoverReceipt.taskID);
    fact(child.task.state === "succeeded" && child.task.attempt === 1 && child.task.progress === 100 &&
      child.task.epoch === epochAfter && child.task.key === submittedKey && child.task.parentID === parent.task.id &&
      child.task.tenantID === parent.task.tenantID && child.task.actorID === parent.task.actorID &&
      child.task.kind === parent.task.kind && child.task.domainID === parent.task.domainID &&
      child.task.payloadVersion === parent.task.payloadVersion && equal(child.task.payload, parent.task.payload) &&
      equal(child.task.result, { database: "ready", queue: "ready" }), "child_result_invalid");
    history(child, ["recovered", "leased", "started", "progress", "finished"], [0, 0, 1, 1, 1]);
    parentAfterChild = one(parent.task.id); fact(equal(parentAfterChild, parent), "parent_history_changed");
    fact(snapshot().tasks.filter((t: any) => t.key === submittedKey && t.parentID === parent.task.id).length === 1, "duplicate_child");
    await visible(child.task.id, "succeeded", 1, parent.task.id);
  });
  async function openDetail(id: string) {
    // App session revalidation/remount can temporarily render neither branch.
    // first() is only a readiness union, never the target of an action.
    await ui(async () => {
      await expect(page.getByRole("button", { name: "返回任务列表", exact: true })
        .or(page.getByRole("button", { name: `详情 ${id}`, exact: true })).first()).toBeVisible();
    }, "navigation_open_ready");
    const backVisible = await ui(() => page.getByRole("button", { name: "返回任务列表", exact: true }).isVisible(), "navigation_back_visible");
    if (backVisible) await ui(() => page.getByRole("button", { name: "返回任务列表", exact: true }).click(), "navigation_return_list");
    await ui(() => page.getByRole("button", { name: `详情 ${id}`, exact: true }).click(), "navigation_open_detail");
  }
  async function restoredDetail(id: string) {
    // A missing identity during loading is not evidence that the list restored.
    await ui(async () => {
      await expect(page.getByTestId("task-detail-identity")
        .or(page.getByRole("button", { name: `详情 ${id}`, exact: true })).first()).toBeVisible();
    }, "navigation_restore_ready");
    const detailVisible = await ui(() => page.getByTestId("task-detail-identity").isVisible(), "navigation_detail_visible");
    if (!detailVisible) await openDetail(id);
    // Caller still checks exact identity, state, attempt, lineage and result.
  }
  // First host-observed detail request, not first successful response. This is
  // the existing Playwright/CDP JSON evidence level, not a native-fetch witness.
  async function lineageDetail(expected: { id: string; state: "failed" | "succeeded";
    attempt: 0 | 1; parentID: string; error: string }, visibility: "active" | "all",
    action: () => Promise<unknown>) {
    const timeout = Math.min(10000, remaining());
    let armed = false, listenerFailed = false, taskWrites = 0;
    let selected: Request | null = null;
    const old = new Set<Request>();
    const requestObserved = (request: Request) => {
      try {
        if (!armed) {
          if (old.size >= 32) listenerFailed = true;
          else old.add(request);
          return;
        }
        const url = new URL(request.url()), method = request.method();
        if ((url.pathname === "/api/tasks" || url.pathname.startsWith("/api/tasks/")) &&
          !["GET", "HEAD", "OPTIONS"].includes(method)) taskWrites++;
        if (old.has(request) || selected !== null || url.pathname !== "/api/tasks/detail") return;
        selected = request;
        const keys = [...url.searchParams.keys()].sort();
        fact(url.origin === origin && url.hash === "" && url.username === "" && url.password === "" &&
          method === "GET" && request.resourceType() === "fetch" && request.serviceWorker() === null &&
          request.frame() === page.mainFrame() && request.redirectedFrom() === null && request.redirectedTo() === null &&
          url.searchParams.getAll("taskUUID").length === 1 && url.searchParams.get("taskUUID") === expected.id &&
          equal(keys, visibility === "all" ? ["taskUUID", "visibility"] : ["taskUUID"]) &&
          (visibility === "all" ? url.searchParams.get("visibility") === "all" : !url.searchParams.has("visibility")),
          "lineage_request_invalid");
      } catch { listenerFailed = true; }
    };
    try {
      page.on("request", requestObserved);
      const readOutcome = page.waitForResponse(response => response.request() === selected, { timeout })
        .then(async response => {
          fact(!listenerFailed && response.status() === 200 && !response.fromServiceWorker() &&
            response.request().redirectedFrom() === null && response.request().redirectedTo() === null,
            "lineage_response_invalid");
          const contentType = await response.headerValue("content-type");
          fact(contentType !== null && /^application\/json(?:;\s*charset=utf-8)?$/i.test(contentType) &&
            await response.headerValue("cache-control") === "no-store" &&
            await response.headerValue("x-content-type-options") === "nosniff", "lineage_metadata_invalid");
          const value = await response.json();
          fact(object(value) && object(value.task) && Array.isArray(value.events) &&
            value.task.taskUUID === expected.id && value.task.state === expected.state &&
            value.task.attempt === expected.attempt && value.task.parentTaskUUID === expected.parentID &&
            value.task.error === expected.error, "lineage_detail_invalid");
        }).then(() => true, () => false);
      armed = true;
      let actionOutcome: Promise<boolean>;
      try { actionOutcome = action().then(() => true, () => false); }
      catch { actionOutcome = Promise.resolve(false); }
      // Both branches already have rejection handlers, so join cannot abandon
      // the other branch on first rejection. No retry or detached body read.
      const [readOK, actionOK] = await Promise.all([readOutcome, actionOutcome]);
      fact(readOK && actionOK && !listenerFailed, "lineage_observation_failed");
      await visible(expected.id, expected.state, expected.attempt, expected.parentID);
      fact(!listenerFailed && taskWrites === 0, "lineage_wrote_task");
    } finally {
      armed = false;
      page.off("request", requestObserved);
      selected = null;
      old.clear();
    }
  }
  const screenshots: Record<string, unknown> = {};
  async function screenshot(id: string, state: "failed" | "succeeded", width: number, height: number) {
    const slot = state === "failed" ? (width === 1440 ? "parent_desktop" : "parent_mobile")
      : (width === 1440 ? "child_desktop" : "child_mobile");
    const captureStages = ["viewport", "proof_empty", "targets", "measure", "fit", "scroll", "contained", "unobscured", "png"] as const;
    type CaptureStage = typeof captureStages[number];
    async function capture<T>(stage: CaptureStage, work: () => Promise<T>): Promise<T> {
      fact(captureStages.includes(stage), "screenshot_stage_invalid");
      // Both category components are closed literals, never IDs, DOM or errors.
      return safe(`screenshot_${slot}_${stage}`, () => ui(work));
    }
    await capture("viewport", async () => {
      fact((width === 1440 && height === 900) || (width === 390 && height === 844), "screenshot_viewport_invalid");
      await page.setViewportSize({ width, height });
    });
    // Do not mask existing navigation and identity helper diagnostics.
    await openDetail(id);
    await visible(id, state, state === "failed" ? 0 : 1, state === "succeeded" ? parent.task.id : "");
    await capture("proof_empty", async () => {
      await expect(page.locator(".task-action")).toHaveCount(0);
      const empty = await page.locator('input[type="password"],input[autocomplete="one-time-code"]').evaluateAll(nodes =>
        nodes.every(node => (node as HTMLInputElement).value === ""));
      fact(empty, "proof_not_empty");
    });
    const targets = await capture("targets", async () => state === "failed"
      ? [page.locator(".task-metadata-panel").getByText("0/5（含首次）", { exact: true }),
         page.locator(".task-metadata-panel").getByText("authorization_revoked", { exact: true })]
      : [page.locator(".task-parent-identity"), page.getByLabel("任务实际结果")]);
    const boxes = await capture("measure", async () => {
      const result = [];
      for (const target of targets) {
        await expect(target).toBeVisible();
        const box = await target.boundingBox();
        fact(box && box.width > 0 && box.height > 0, "screenshot_target_missing"); result.push(box);
      }
      return result;
    });
    const bounds = { left: Math.min(...boxes.map(b => b.x)), top: Math.min(...boxes.map(b => b.y)),
      right: Math.max(...boxes.map(b => b.x + b.width)), bottom: Math.max(...boxes.map(b => b.y + b.height)) };
    await capture("fit", async () => {
      fact(bounds.right - bounds.left <= width && bounds.bottom - bounds.top <= height, "screenshot_targets_cannot_fit");
    });
    // Normal document scrolling only. Frame the complete real target union,
    // never alter DOM/CSS, scale content, crop an image or hide an obstruction.
    await capture("scroll", () => page.evaluate(({ dx, dy }) => window.scrollBy({ left: dx, top: dy, behavior: "instant" }),
      { dx: (bounds.left + bounds.right - width) / 2, dy: (bounds.top + bounds.bottom - height) / 2 }));
    await capture("contained", async () => {
      for (const target of targets) {
        await expect(target).toBeVisible();
        const box = await target.boundingBox();
        fact(box && box.x >= 0 && box.y >= 0 && box.x + box.width <= width && box.y + box.height <= height,
          "screenshot_required_content_outside_viewport");
      }
    });
    for (const [index, target] of targets.entries()) {
      // Rounded element corners can be transparent. Test every actual text
      // fragment instead; parent hits still fail, never count as unobscured.
      const proof = await capture("unobscured", () => target.evaluate(element => {
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
        let fragments = 0, contained = true, unobscured = true;
        for (let node = walker.nextNode(); node; node = walker.nextNode()) {
          if (!node.textContent?.trim()) continue;
          const range = document.createRange(); range.selectNodeContents(node);
          for (const rect of range.getClientRects()) {
            if (rect.width <= 0 || rect.height <= 0) continue;
            fragments++;
            contained = contained && rect.left >= 0 && rect.top >= 0 &&
              rect.right <= window.innerWidth && rect.bottom <= window.innerHeight;
            const dx = Math.min(1, rect.width / 4), dy = Math.min(1, rect.height / 4);
            const points = [[rect.left + dx, rect.top + dy], [rect.right - dx, rect.top + dy],
              [rect.left + dx, rect.bottom - dy], [rect.right - dx, rect.bottom - dy],
              [(rect.left + rect.right) / 2, (rect.top + rect.bottom) / 2]];
            unobscured = unobscured && points.every(([x, y]) => {
              const hit = document.elementFromPoint(x, y); return hit !== null && (hit === element || element.contains(hit));
            });
          }
        }
        // Only booleans leave the page, never text, rectangles or DOM identity.
        return { present: fragments > 0, contained, unobscured };
      }));
      const targetSlot = index === 0 ? "first" : "second";
      fact(index === 0 || index === 1, "screenshot_target_index_invalid");
      fact(proof.present, `screenshot_${slot}_${targetSlot}_text_missing`);
      fact(proof.contained, `screenshot_${slot}_${targetSlot}_text_outside`);
      fact(proof.unobscured, `screenshot_${slot}_${targetSlot}_text_obscured`);
    }
    const name = `${state === "failed" ? "parent-failed" : "child-succeeded"}-${width === 1440 ? "desktop" : "mobile"}.png`;
    await capture("png", async () => {
      const bytes = await page.screenshot({ path: testInfo.outputPath(name), fullPage: false, animations: "disabled" });
      fact(bytes.subarray(0, 8).equals(Buffer.from([137,80,78,71,13,10,26,10])) &&
        bytes.readUInt32BE(16) === width && bytes.readUInt32BE(20) === height, "screenshot_dimensions_invalid");
      screenshots[name] = { sha256: digest(bytes), bytes: bytes.length, width, height, proofEmpty: true };
    });
  }
  let replayReceipt: any, beforeReplay: any, afterReplay: any, parentAfterReplay: any, childAfterReplay: any, cancelledAfter: any;
  let lifecycle: any;
  await phase("replayLifecycleEvidence", 60000, async () => {
    beforeReplay = snapshot(); fact(beforeReplay.tasks.length === 2 && beforeReplay.events.length === 8 && beforeReplay.outbox.length === 8, "complete_history_counts_invalid");
    await profile(); const code = await freshCode();
    fact(!halted && posts.replay === 0 && recoveryPosts === 1, "replay_not_authorized"); posts.replay++; sensitiveActions++;
    try {
      const value = await safe("explicit_replay_failed", async () => {
        const r = await context.request.post("/api/tasks/recover", { headers: { Origin: origin, "X-CSRF-Token": csrf },
          data: { taskUUID: parent.task.id, idempotencyKey: submittedKey, actorPassword: current, totpCode: code },
          timeout: Math.min(5000, remaining()), maxRetries: 0 });
        fact(r.status() === 200, "explicit_replay_status"); return r.json();
      });
      replayReceipt = receipt(value, true, submittedKey);
    } catch { await uncertain(async () => one(child.task.id)); throw new Error("explicit_replay_failed"); }
    fact(replayReceipt.taskID === child.task.id && replayReceipt.parentID === parent.task.id && replayReceipt.state === "succeeded" &&
      replayReceipt.attempt === child.task.attempt && replayReceipt.resultVersion === child.task.resultVersion, "replay_receipt_mismatch");
    afterReplay = snapshot(); fact(equal(beforeReplay, afterReplay), "replay_changed_persistence");
    parentAfterReplay = one(parent.task.id); childAfterReplay = one(child.task.id);
    fact(equal(parentAfterReplay, parent) && equal(childAfterReplay, child), "replay_changed_history"); cancelledAfter = zero(cancelledKey);
    const count = recoveryPosts;
    for (const value of [parent, child]) {
      await openDetail(value.task.id);
      await ui(() => page.reload(), "navigation_reload");
      // Reload may restore the list; reopen through actual list controls only.
      await restoredDetail(value.task.id);
      await visible(value.task.id, value.task.state, value.task.attempt, value.task.parentID);
      await ui(() => page.getByRole("button", { name: "返回任务列表", exact: true }).click());
      await ui(() => page.goBack(), "navigation_history_back");
      await restoredDetail(value.task.id);
      await visible(value.task.id, value.task.state, value.task.attempt, value.task.parentID);
      await ui(() => page.goForward(), "navigation_history_forward");
      await openDetail(value.task.id); await visible(value.task.id, value.task.state, value.task.attempt, value.task.parentID);
    }
    const lineageBefore = snapshot();
    const parentExpected = { id: parent.task.id, state: "failed" as const, attempt: 0 as const,
      parentID: "", error: "authorization_revoked" };
    const childExpected = { id: child.task.id, state: "succeeded" as const, attempt: 1 as const,
      parentID: parent.task.id, error: "" };
    await ui(() => lineageDetail(parentExpected, "all", () =>
      page.getByRole("button", { name: `查看父任务 ${parent.task.id}`, exact: true }).click()), "lineage_parent");
    await ui(() => lineageDetail(childExpected, "active", () => page.goBack()), "lineage_back");
    await ui(() => lineageDetail(parentExpected, "all", () => page.goForward()), "lineage_forward");
    fact(equal(lineageBefore, snapshot()), "lineage_changed_persistence");
    fact(recoveryPosts === count && count === 1, "navigation_recovered_again");
    lifecycle = { reloadParent: true, reloadChild: true, backForwardParent: true, backForwardChild: true,
      recoveryPostsBefore: count, recoveryPostsAfter: recoveryPosts };
    for (const [width,height] of [[1440,900],[390,844]]) {
      await screenshot(parent.task.id, "failed", width, height);
      await screenshot(child.task.id, "succeeded", width, height);
    }
  });
  const evidenceStarted = Date.now();
  // Complete tracked source inventory, derived from actual HEAD rather than a
  // hand-maintained shortlist or the provisional f123 drafting reference.
  function git(...args: string[]): string {
    try { return execFileSync("git", args, { encoding: "utf8", timeout: Math.min(5000, remaining()), stdio: ["ignore", "pipe", "pipe"] }); }
    catch { throw new Error("checkout_observation_failed"); }
  }
  const root = git("rev-parse", "--show-toplevel").trim();
  const commit = git("rev-parse", "HEAD").trim(), tree = git("rev-parse", "HEAD^{tree}").trim();
  fact(SHA.test(commit) && SHA.test(tree) && git("status", "--porcelain", "--untracked-files=all").trim() === "", "checkout_not_clean");
  function file(path: string) {
    try {
      fact(!path.startsWith("/") && !path.includes("\\") && path.split("/").every(p => p && p !== "." && p !== ".."), "source_path_invalid");
      const absolute = resolve(root, path); fact(relative(root, absolute) === path, "source_path_escape");
      let parentPath = root;
      for (const part of path.split("/")) { parentPath = resolve(parentPath, part); fact(!lstatSync(parentPath).isSymbolicLink(), "source_symlink_forbidden"); }
      fact(lstatSync(absolute).isFile(), "source_not_regular");
      const bytes = readFileSync(absolute); return { path, sha256: digest(bytes), bytes: bytes.length };
    } catch { throw new Error("source_bytes_failed"); }
  }
  const prefixes = [".github/workflows/", "cmd/", "internal/", "scripts/", "web/"];
  const names = ["Makefile", "go.mod", "go.sum"];
  const sources: {path:string;gitBlob:string;sha256:string;bytes:number}[] = [];
  for (const entry of git("ls-tree", "-rz", "--full-tree", "HEAD").split("\0").filter(Boolean)) {
    const match = /^(\d+) blob ([a-f0-9]{40})\t([\s\S]+)$/.exec(entry);
    fact(match, "tracked_tree_entry_invalid"); const path = match[3];
    if (!prefixes.some(p => path.startsWith(p)) && !names.includes(path)) continue;
    fact(match[1] === "100644" || match[1] === "100755", "tracked_source_mode_invalid");
    const metadata = file(path);
    const raw = readFileSync(resolve(root, path));
    const blob = createHash("sha1").update(Buffer.from(`blob ${raw.length}\0`)).update(raw).digest("hex");
    fact(blob === match[2], "source_checkout_drift"); sources.push({ ...metadata, gitBlob: blob });
  }
  sources.sort((a,b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
  fact(sources.length > 0 && sources.some(s => s.path === "web/e2e/tasks-health-recovery.spec.ts") &&
    sources.some(s => s.path === "scripts/validate_tasks_health_recovery_evidence.py") &&
    sources.some(s => s.path === ".github/workflows/ci.yml"), "source_inventory_incomplete");
  const frontend: {path:string;sha256:string;bytes:number}[] = [];
  function walk(directory: string) {
    try {
      fact(!lstatSync(resolve(root,directory)).isSymbolicLink(), "build_symlink_forbidden");
      for (const entry of readdirSync(resolve(root,directory)).sort()) {
        const path = `${directory}/${entry}`, stat = lstatSync(resolve(root,path));
        fact(!stat.isSymbolicLink(), "build_symlink_forbidden");
        if (stat.isDirectory()) walk(path); else frontend.push(file(path));
      }
    } catch { throw new Error("build_observation_failed"); }
  }
  walk("web/dist"); frontend.sort((a,b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
  const binary = file("bin/adtr");
  fact(frontend.some(f => f.path === "web/dist/index.html") && frontend.some(f => f.path.endsWith(".js")), "frontend_missing");
  const workflowSHA = process.env.GITHUB_WORKFLOW_SHA, runID = process.env.GITHUB_RUN_ID, runAttempt = process.env.GITHUB_RUN_ATTEMPT;
  fact(commit === process.env.GITHUB_SHA && workflowSHA && SHA.test(workflowSHA) && runID && /^[1-9][0-9]*$/.test(runID) &&
    runAttempt && /^[1-9][0-9]*$/.test(runAttempt) && UUID.test(nonce), "run_identity_missing");
  fact(!halted && unknownWrites === 0 && sensitiveActions === 7 && Object.values(posts).every(n => n === 1) && reads.oldSession === 1,
    "action_ledger_invalid");
  await safe("browser_context_close", () => context.close());
  phases.replayLifecycleEvidence += Date.now() - evidenceStarted;
  phases.totalMs = Date.now() - started;
  fact(phases.replayLifecycleEvidence <= 60000 && phases.totalMs <= 600000, "evidence_phase_deadline");
  const manifest = { schemaVersion: 1, suite: "tasks-health-recovery", status: "provisional_pending_owned_cleanup",
    identity: { commit, tree, workflowSHA, runID, runAttempt, nonce }, checkout: { clean: true, sources },
    build: { binary, frontend }, browserVersion: browser.version(),
    actor: { id: actorID, tenantID: beforeActor.tenantID, epochBefore: beforeActor.epoch, epochAfter },
    tasks: { queued, parent, child, parentAfterChild, parentAfterReplay, childAfterReplay },
    keys: { submission: submissionKey, cancelled: cancelledKey, submitted: submittedKey },
    receipts: { submit: submitReceipt, recover: recoverReceipt, replay: replayReceipt },
    snapshots: { beforeReplay, afterReplay }, cancelled: { before: cancelledBefore, after: cancelledAfter }, session,
    ledger: { posts, reads, unknownWrites, sensitiveActions, counterTransitions, counterWaitMs }, phases,
    controller: { before: controllerBefore, after: controllerAfter, startPosts: posts.controllerStart }, screenshots, lifecycle };
  try { writeFileSync(testInfo.outputPath("manifest.json"), JSON.stringify(manifest, null, 2) + "\n", { flag: "wx" }); }
  catch { throw new Error("provisional_manifest_write_failed"); }
  // No attach/copy/upload and no cleanup-receipt writer. The runner is the sole
  // cleanup owner, and independently validates only after owned finally closes.
});
