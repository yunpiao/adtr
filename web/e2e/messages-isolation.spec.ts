import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { navigateTo } from "./navigation";
import { createHash, createHmac, randomUUID } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, readdirSync, lstatSync } from "node:fs";
import { resolve } from "node:path";

// All errors crossing an authentication/transport boundary are fixed categories.
// Never preserve an exception, cause, call log, request proof or arbitrary body.
type Category = "auth_setup_transport" | "health_submit_transport" |
  "message_read_transport" | "message_write_transport" | "browser_observation";
function requireFact(ok: unknown, category: string): asserts ok {
  if (!ok) throw new Error(category);
}
async function safe<T>(category: Category, action: () => Promise<T>): Promise<T> {
  try { return await action(); } catch { throw new Error(category); }
}
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
// PostgreSQL JSON uses +00:00; Go uses Z and may trim fractional zeros.
// Preserve nanoseconds rather than truncating immutable history to milliseconds.
function sameInstant(left: unknown, right: unknown): boolean {
  const canonical = (value: unknown): string | null => {
    if (typeof value !== "string") return null;
    const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(?:Z|\+00:00)$/.exec(value);
    if (!match || !Number.isFinite(Date.parse(value))) return null;
    if (new Date(value).toISOString().slice(0, 19) !== match[1]) return null;
    return `${match[1]}.${(match[2] ?? "").padEnd(9, "0")}Z`;
  };
  const a = canonical(left);
  return a !== null && a === canonical(right);
}
const uuid = (x: unknown): x is string => typeof x === "string" && /^[0-9a-f-]{36}$/.test(x);
const identifier = (x: unknown): x is string => typeof x === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(x);
const sleep = (ms: number) => new Promise<void>((done) => setTimeout(done, ms));
type MFA = { secret: string; counter: number };
type Entry = { messageId: string; taskUUID: string; title: string; description: string;
  scopeKind: string; domainId: string; occurredAt: string; read: boolean; firstReadAt: string | null };
type Snapshot = { resultId: string; confirmedAt: string; expiresAt: string; items: Entry[];
  counts: { all: number; unread: number; read: number }; total: number; offset: number; limit: number };
type Actor = { label: "A" | "B"; context: BrowserContext; page: Page; password: string;
  mfa: MFA; userId: number; csrf: string; tasks: string[]; sensitive: number; queries: number;
  writes: number; snapshots: Snapshot[]; snapshot: Snapshot; read: any; provenance: any[] };

function totp(secret: string, now: number): string {
  let bits = "";
  for (const char of secret.replace(/=+$/, "").toUpperCase()) {
    const index = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567".indexOf(char);
    requireFact(index >= 0, "auth_setup_transport");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes)).update(counter).digest();
  return ((digest.readUInt32BE(digest[digest.length - 1] & 15) & 0x7fffffff) % 1_000_000).toString().padStart(6, "0");
}

function chromiumEnvironment(): Record<string, string> {
  return Object.fromEntries(Object.entries(process.env).filter(
    (entry): entry is [string, string] => entry[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(entry[0]),
  ));
}
test.use({ trace: "off", screenshot: "off", video: "off",
  launchOptions: { env: chromiumEnvironment(),
    ...(process.env.ADTR_E2E_CHROMIUM_PATH ? { executablePath: process.env.ADTR_E2E_CHROMIUM_PATH } : {}) },
});
test("two authenticated nonempty message histories remain isolated", async ({ browser }, testInfo) => {
  test.setTimeout(660_000);
  const baseURL = process.env.ADTR_E2E_BASE_URL;
  const username = process.env.ADTR_E2E_USERNAME;
  const initial = process.env.ADTR_E2E_PASSWORD;
  const container = process.env.ADTR_E2E_DB_CONTAINER;
  requireFact(baseURL && username && initial && container, "owned_fixture_unavailable");
  const origin = new URL(baseURL).origin;
  const started = Date.now();
  let setupSpent = 0, setupDeadline = 0, totpWaits = 0, snapshotStarted = 0;
  const stages: any[] = [], screenshots: any[] = [], positiveBulks: any[] = [];
  const actors: Actor[] = [];
  function remaining(): number {
    const deadline = Math.min(snapshotStarted ? snapshotStarted + 60_000 : started + 623_800, setupDeadline || Infinity);
    requireFact(Date.now() < deadline, snapshotStarted ? "snapshot_phase_deadline" : "suite_phase_deadline");
    return Math.max(1, deadline - Date.now());
  }
  function live(...snapshots: Snapshot[]) {
    remaining();
    for (const s of [...snapshots, ...actors.flatMap(a => a.snapshots)]) requireFact(Date.now() < Date.parse(s.expiresAt), "snapshot_expired");
    for (const a of actors) requireFact(a.queries <= 7, "snapshot_admission_budget");
  }
  async function uncertain(category: Category, inspect?: () => Promise<boolean>) {
    let observed = false;
    try { if (inspect) observed = await inspect(); } catch { observed = false; }
    testInfo.annotations.push({ type: "uncertain_write", description: `${category}:${observed ? "observed" : "unknown"}` });
  }
  async function setup<T>(action: () => Promise<T>, inspect?: () => Promise<boolean>): Promise<T> {
    const begin = Date.now();
    requireFact(setupSpent < 75_000, "setup_phase_deadline");
    for (const a of actors) a.page.setDefaultTimeout(Math.min(10_000, 75_000 - setupSpent));
    setupDeadline = begin + 75_000 - setupSpent;
    let value: T;
    try { value = await safe("auth_setup_transport", action); }
    catch {
      if (inspect) await uncertain("auth_setup_transport", inspect);
      throw new Error("auth_setup_transport");
    }
    setupDeadline = 0;
    setupSpent += Date.now() - begin;
    requireFact(setupSpent <= 75_000, "setup_phase_deadline");
    return value;
  }
  async function proofStep<T>(action: () => Promise<T>): Promise<T> {
    remaining();
    for (const a of actors) a.page.setDefaultTimeout(Math.min(10_000, remaining()));
    const result = await safe("auth_setup_transport", action);
    remaining(); return result;
  }
  async function freshCode(a: Actor): Promise<string> {
    totpWaits++;
    requireFact(totpWaits <= 8, "totp_wait_budget");
    const begin = Date.now(), earliest = (a.mfa.counter + 1) * 30_000 + 1100;
    while (Date.now() < earliest) await sleep(earliest - Date.now());
    requireFact(Date.now() - begin <= 31_100, "totp_wait_deadline");
    a.mfa.counter = Math.floor(Date.now() / 30_000);
    return totp(a.mfa.secret, Date.now());
  }
  // Owned DB only; subprocess stderr is captured, never inherited or rethrown.
  function sql(query: string): any {
    remaining();
    try {
      return JSON.parse(execFileSync("docker", ["exec", "-e",
        "PGOPTIONS=-c default_transaction_read_only=on", container!, "psql", "-U", "postgres",
        "-d", "adtr_e2e", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query],
      { encoding: "utf8", timeout: Math.min(10_000, remaining()), stdio: ["ignore", "pipe", "pipe"] }));
    } catch { throw new Error("owned_db_observation_failed"); }
  }
  function identity(a: Actor): any {
    requireFact(Number.isSafeInteger(a.userId) && a.userId > 0, "identity_invalid");
    const rows = sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM
      (SELECT id,tenant_id,authorization_version FROM adtr.users WHERE id=${a.userId}) p`);
    requireFact(rows.length === 1, "identity_missing");
    return rows[0];
  }
  // Explicit columns avoid session/token hashes. Ordered arrays preserve every
  // message body, read timestamp, operation, target and receipt, not just counts.
  function state(a: Actor): any {
    const user = a.userId;
    const array = (table: string, cols: string, where: string, order: string) =>
      `(SELECT COALESCE(json_agg(p),'[]'::json) FROM (SELECT ${cols} FROM adtr.${table} WHERE ${where} ORDER BY ${order}) p)`;
    return sql(`SELECT json_build_object(
      'identity',${array("users", "id,tenant_id,authorization_version", `id=${user}`, "id")},
      'messages',${array("messages", "message_id,tenant_id,recipient_user_id,source_task_id,source_event_id,source_result_version,message_type,sub_type,title,description,occurred_at,ingested_at", `recipient_user_id=${user}`, "message_id")},
      'reads',${array("message_reads", "tenant_id,user_id,message_id,first_read_at", `user_id=${user}`, "message_id")},
      'single',${array("message_read_operations", "tenant_id,user_id,operation_id,result_id,message_id,first_read_at,committed_at,newly_read", `user_id=${user}`, "operation_id")},
      'bulk',${array("message_bulk_operations", "tenant_id,user_id,operation_id,queue_id,result_id,selection,authorization_version,accepted_at,updated_at,state,target,processed,newly_read,already_read", `user_id=${user}`, "operation_id")},
      'targets',${array("message_bulk_targets", "tenant_id,user_id,operation_id,ordinal,message_id", `user_id=${user}`, "operation_id,ordinal")},
      'receipts',${array("message_bulk_receipts", "tenant_id,user_id,operation_id,ordinal,first_read_at,committed_at,newly_read", `user_id=${user}`, "operation_id,ordinal")})`);
  }
  function versions(a: Actor): any {
    return sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (SELECT result_id,projection_version,
      authorization_version,confirmed_at,expires_at FROM adtr.message_results WHERE user_id=${a.userId} ORDER BY result_id) p`);
  }
  async function get(a: Actor, path: string): Promise<{ status: number; body: any }> {
    live();
    return safe("message_read_transport", async () => {
      const response = await a.context.request.get(path, { timeout: Math.min(5000, remaining()), maxRetries: 0 });
      return { status: response.status(), body: await response.json() };
    });
  }
  async function profile(a: Actor) {
    const r = await get(a, "/api/auth/me");
    requireFact(r.status === 200 && Number.isSafeInteger(r.body.ID) && r.body.ID > 0 &&
      r.body.hasMfa === true && r.body.needChangePwd === false && r.body.isExpired === false &&
      typeof r.body.csrfToken === "string" && r.body.csrfToken.length > 0, "current_authority_invalid");
    if (a.userId) requireFact(a.userId === r.body.ID, "identity_changed");
    a.userId = r.body.ID; a.csrf = r.body.csrfToken;
  }
  async function post(a: Actor, path: string, data: any): Promise<{ status: number; body: any }> {
    live();
    try { return await safe("message_write_transport", async () => {
      const response = await a.context.request.post(path, { headers: { Origin: origin, "X-CSRF-Token": a.csrf },
        data, timeout: Math.min(5000, remaining()), maxRetries: 0 });
      return { status: response.status(), body: await response.json() };
    }); } catch {
      await uncertain("message_write_transport", async () => {
        if (!identifier(data.operationId)) return false;
        const route = path === "/api/messages/read" ? "read-operation" : "bulk-read-operation";
        const receipt = await get(a, `/api/messages/${route}?operationId=${data.operationId}`);
        return receipt.status === 200 && (route === "read-operation" ?
          receipt.body.operationId === data.operationId : receipt.body.receipt?.operationId === data.operationId);
      });
      throw new Error("message_write_transport");
    }
  }
  async function uiResponse(page: Page, path: string, action: () => Promise<unknown>, category: Category,
    inspect?: () => Promise<boolean>) {
    try { return await safe(category, async () => {
      const [response] = await Promise.all([page.waitForResponse(r =>
        new URL(r.url()).pathname === path && r.request().method() === "POST",
      { timeout: Math.min(10_000, remaining()) }), setupDeadline ? proofStep(action) : action()]);
      requireFact(response.status() === 200, category);
      return response.json();
    }); } catch {
      await uncertain(category, inspect ?? (async () => {
        const actor = actors.find(a => a.context === page.context());
        if (!actor || !actor.userId) return false;
        if (path === "/api/messages/query") return versions(actor)
          .filter((row: any) => !actor.snapshots.some(s => s.resultId === row.result_id)).length === 1;
        if (path === "/api/messages/read" && actor.snapshot) {
          const resultId = actor.snapshot.resultId, messageId = actor.snapshot.items[0].messageId;
          requireFact(identifier(resultId) && identifier(messageId), "uncertain_identity_invalid");
          return sql(`SELECT to_json(EXISTS(SELECT FROM adtr.message_read_operations WHERE user_id=${actor.userId}
            AND result_id='${resultId}' AND message_id='${messageId}'))`) === true;
        }
        if (path === "/api/access/roles/save") return sql(`SELECT to_json(EXISTS(
          SELECT FROM adtr.access_roles r JOIN adtr.users u ON u.tenant_id=r.tenant_id
          WHERE u.id=${actor.userId} AND r.name='Synthetic Isolation Task Writer'))`) === true;
        if (path === "/api/access/users/create") return sql(`SELECT to_json(EXISTS(
          SELECT FROM adtr.users target JOIN adtr.users actor ON actor.tenant_id=target.tenant_id
          WHERE actor.id=${actor.userId} AND target.username='synthetic.isolation.b'))`) === true;
        return false;
      }));
      throw new Error(category);
    }
  }
  async function observe<T>(action: () => Promise<T>): Promise<T> {
    live();
    for (const a of actors) for (const p of a.context.pages()) p.setDefaultTimeout(Math.min(3000, remaining()));
    const value = await safe("browser_observation", action);
    live(); return value;
  }
  async function query(a: Actor, page = a.page): Promise<Snapshot> {
    live();
    const s: Snapshot = await uiResponse(page, "/api/messages/query", () =>
      page.getByRole("button", { name: "确认查询", exact: true }).click(), "message_write_transport");
    requireFact(identifier(s.resultId) && Array.isArray(s.items) && s.items.length === 3 && s.total === 3 &&
      s.counts.all === 3 && s.counts.unread + s.counts.read === 3 &&
      Date.parse(s.expiresAt) - Date.parse(s.confirmedAt) === 600_000, "nonempty_snapshot_invalid");
    requireFact(same(s.items.map(i => i.taskUUID).sort(), [...a.tasks].sort()), "own_snapshot_members_invalid");
    a.snapshots.push(s); live(s);
    const persisted = versions(a).find((v: any) => v.result_id === s.resultId);
    requireFact(persisted?.projection_version === 1 && persisted.authorization_version === identity(a).authorization_version,
      "snapshot_version_invalid");
    for (const item of s.items) {
      const p = a.provenance.find(p => p.messageId === item.messageId);
      requireFact(p && item.taskUUID === p.taskUUID && item.title === p.title && item.description === p.description &&
        sameInstant(item.occurredAt, p.occurredAt) && item.scopeKind === "platform" && item.domainId === "",
      "snapshot_provenance_invalid");
    }
    await observe(async () => {
      await expect(page.locator("article[data-message-id]")).toHaveCount(3);
      for (const item of s.items) await expect(page.locator(`article[data-message-id="${item.messageId}"]`)).toContainText(item.taskUUID);
      await expect(page.getByRole("region", { name: "查询时消息统计" })).toContainText(`查询时未读${s.counts.unread}`);
    });
    return s;
  }
  async function positive(a: Actor) {
    live(a.snapshot); await profile(a);
    const before = versions(a);
    for (const offset of [0, 1]) {
      const r = await get(a, `/api/messages/page?resultId=${a.snapshot.resultId}&offset=${offset}&limit=1`);
      requireFact(r.status === 200 && r.body.resultId === a.snapshot.resultId &&
        r.body.confirmedAt === a.snapshot.confirmedAt && r.body.expiresAt === a.snapshot.expiresAt &&
        r.body.offset === offset && r.body.limit === 1 && r.body.total === 3 &&
        same(r.body.counts, a.snapshot.counts) && same(r.body.items, [a.snapshot.items[offset]]), "owner_page_failed");
    }
    const r = await get(a, `/api/messages/locate?resultId=${a.snapshot.resultId}&messageId=${a.snapshot.items[1].messageId}&limit=1`);
    requireFact(r.status === 200 && r.body.found === true && r.body.index === 1 && r.body.page === 2 &&
      r.body.resultId === a.snapshot.resultId, "owner_locate_failed");
    const receipt = await get(a, `/api/messages/read-operation?operationId=${a.read.operationId}`);
    requireFact(receipt.status === 200 && same(receipt.body, a.read), "owner_receipt_failed");
    requireFact(same(before, versions(a)), "frozen_projection_version_changed");
  }
  async function negative(label: string, caller: Actor, owner: Actor, action: () => Promise<{status: number; body: any}>, status = 404, code = "not_found") {
    live(caller.snapshot, owner.snapshot);
    const before = actors.map(state), frozen = actors.map(versions);
    const r = await action();
    // Boolean validation before recording: unexpected private body never reaches
    // an assertion diff, artifact or exception diagnostic.
    requireFact(r.status === status && r.body !== null && typeof r.body === "object" &&
      Object.keys(r.body).length === 1 && r.body.error === code, "negative_response_invalid");
    const after = actors.map(state);
    requireFact(same(before, after) && same(frozen, actors.map(versions)), "negative_persistence_changed");
    stages.push({ label, status, body: { error: code }, before, after });
  }
  async function initialize(label: "A" | "B", name: string, password: string, changed: string): Promise<Actor> {
    let mutation: "login" | "password" | "mfa-start" | "mfa-confirm" = "login";
    return setup(async () => {
      const context = await browser.newContext({ baseURL, storageState: { cookies: [], origins: [] },
        viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
      const page = await context.newPage();
      page.setDefaultTimeout(10_000);
      const a = { label, context, page, password: changed, userId: 0, csrf: "", tasks: [],
        sensitive: 0, queries: 0, writes: 0, snapshots: [], provenance: [] } as unknown as Actor;
      actors.push(a);
      // Count only bounded numbers; never serialize request URLs, headers or data.
      context.on("request", request => {
        const path = new URL(request.url()).pathname;
        if (request.method() === "POST" && path === "/api/messages/query") a.queries++;
        if (request.method() === "POST" && ["/api/messages/read", "/api/messages/bulk-read", "/api/messages/bulk-read-cancel"].includes(path)) a.writes++;
      });
      await proofStep(() => page.goto("/"));
      await proofStep(() => page.getByLabel("用户名", { exact: true }).fill(name));
      await proofStep(() => page.getByLabel("密码", { exact: true }).fill(password));
      await proofStep(() => page.getByRole("button", { name: "登录", exact: true }).click());
      await proofStep(() => expect(page.getByRole("heading", { name: "修改密码", exact: true })).toBeVisible());
      await proofStep(() => page.getByLabel("当前密码", { exact: true }).fill(password));
      await proofStep(() => page.getByLabel("新密码", { exact: true }).fill(changed));
      await proofStep(() => page.getByLabel("确认新密码", { exact: true }).fill(changed));
      mutation = "password";
      await proofStep(() => page.getByRole("button", { name: "更新密码", exact: true }).click());
      a.sensitive++;
      await proofStep(() => expect(page.getByRole("heading", { name: "账户概览", exact: true })).toBeVisible());
      await proofStep(() => navigateTo(page, "多因素认证"));
      await proofStep(() => page.getByLabel("当前密码", { exact: true }).fill(changed));
      mutation = "mfa-start";
      await proofStep(() => page.getByRole("button", { name: "开始设置", exact: true }).click());
      a.sensitive++;
      const secret = await proofStep(() => page.getByLabel("设置密钥", { exact: true }).inputValue());
      requireFact(secret.length > 10, "auth_setup_transport");
      const now = Date.now();
      await proofStep(() => page.getByLabel("认证器验证码", { exact: true }).fill(totp(secret, now)));
      mutation = "mfa-confirm";
      await proofStep(() => page.getByRole("button", { name: "验证并启用", exact: true }).click());
      a.sensitive++;
      await proofStep(() => expect(page.getByRole("status")).toContainText("多因素认证已启用"));
      a.mfa = { secret, counter: Math.floor(now / 30_000) };
      await profile(a);
      return a;
    }, async () => {
      const actor = actors.find(a => a.label === label);
      if (!actor) return false;
      const r = await get(actor, "/api/auth/me");
      if (r.status !== 200 || r.body.username !== name || !Number.isSafeInteger(r.body.ID)) return false;
      return mutation === "login" || mutation === "password" && r.body.needChangePwd === false ||
        mutation === "mfa-confirm" && r.body.hasMfa === true;
    });
  }
  const A = await initialize("A", username, initial, "Synthetic Isolation A Changed 123");
  const roleCode = await freshCode(A);
  const role = await setup(async () => {
    await proofStep(() => navigateTo(A.page, "访问管理"));
    await proofStep(() => A.page.getByRole("button", { name: "角色管理", exact: true }).click());
    await proofStep(() => A.page.getByRole("button", { name: "新增角色", exact: true }).click());
    await proofStep(() => A.page.getByLabel("角色名称", { exact: true }).fill("Synthetic Isolation Task Writer"));
    await proofStep(() => A.page.getByRole("checkbox", { name: "后台任务：读取", exact: true }).check());
    await proofStep(() => A.page.getByRole("checkbox", { name: "后台任务：写入", exact: true }).check());
    await proofStep(() => A.page.getByLabel("操作者当前密码", { exact: true }).fill(A.password));
    await proofStep(() => A.page.getByLabel("未使用的认证器验证码", { exact: true }).fill(roleCode));
    const value = await uiResponse(A.page, "/api/access/roles/save", () =>
      A.page.getByRole("button", { name: "保存角色", exact: true }).click(), "auth_setup_transport");
    requireFact(value.result === "SUCCESS" && value.sessionRevoked === false && identifier(value.roleID), "auth_setup_transport");
    A.sensitive++; return value.roleID as string;
  });
  const createCode = await freshCode(A);
  const bInitial = "Synthetic Isolation B Initial 123";
  await setup(async () => {
    await proofStep(() => A.page.getByRole("button", { name: "用户管理", exact: true }).click());
    await proofStep(() => A.page.getByRole("button", { name: "新增用户", exact: true }).click());
    await proofStep(() => A.page.getByLabel("用户名", { exact: true }).fill("synthetic.isolation.b"));
    await proofStep(() => A.page.getByLabel("初始密码", { exact: true }).fill(bInitial));
    await proofStep(() => A.page.getByLabel("确认初始密码", { exact: true }).fill(bInitial));
    await proofStep(() => A.page.getByLabel("角色", { exact: true }).fill(role));
    await proofStep(() => A.page.getByLabel("操作者当前密码", { exact: true }).fill(A.password));
    await proofStep(() => A.page.getByLabel("未使用的认证器验证码", { exact: true }).fill(createCode));
    const value = await uiResponse(A.page, "/api/access/users/create", () =>
      A.page.getByRole("button", { name: "创建用户", exact: true }).click(), "auth_setup_transport");
    requireFact(value.result === "SUCCESS" && value.sessionRevoked === false, "auth_setup_transport");
    A.sensitive++;
  });
  const B = await initialize("B", "synthetic.isolation.b", bInitial, "Synthetic Isolation B Changed 456");
  requireFact(A.userId !== B.userId, "identities_not_distinct");
  const grants = sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (
    SELECT p.mark,p.readable,p.writeable FROM adtr.access_permissions p JOIN adtr.users u
      ON u.tenant_id=p.tenant_id AND u.role_id=p.role_id WHERE u.id=${B.userId} ORDER BY p.mark) p`);
  requireFact(grants.some((g: any) => g.mark === "tasks" && g.readable === true && g.writeable === true) &&
    grants.every((g: any) => g.mark === "tasks" || g.readable === false && g.writeable === false), "minimal_role_invalid");
  // Exactly six real HTTP/password/MFA submissions. No SubmitTx fixture or SQL producer.
  for (const a of actors) for (let index = 0; index < 3; index++) {
    const code = await freshCode(a);
    await setup(async () => {
      await safe("health_submit_transport", async () => {
        await proofStep(() => navigateTo(a.page, "后台任务"));
        await proofStep(() => a.page.getByRole("button", { name: "提交健康检查", exact: true }).click());
        const taskKey = await proofStep(() => a.page.getByLabel("提交幂等键", { exact: true }).inputValue());
        requireFact(identifier(taskKey), "health_submit_transport");
        await proofStep(() => a.page.getByLabel("操作者当前密码", { exact: true }).fill(a.password));
        await proofStep(() => a.page.getByLabel("未使用的认证器验证码", { exact: true }).fill(code));
        const value = await uiResponse(a.page, "/api/tasks/submit", () =>
          a.page.getByRole("button", { name: "确认提交健康检查", exact: true }).click(), "health_submit_transport", async () =>
            sql(`SELECT to_json(EXISTS(SELECT FROM adtr.tasks WHERE actor_id=${a.userId}
              AND kind='infrastructure.health' AND domain_id='platform' AND idempotency_key='${taskKey}'))`) === true);
        requireFact(value.replayed === false && uuid(value.task?.taskUUID), "health_submit_transport");
        a.tasks.push(value.task.taskUUID); a.sensitive++;
      });
    });
    const terminalDeadline = Date.now() + 30_000;
    await safe("health_submit_transport", async () => {
      await expect(a.page.locator('.task-state[data-state="succeeded"]')).toBeVisible({ timeout: 30_000 });
      requireFact(Date.now() <= terminalDeadline, "health_terminal_deadline");
    });
    await setup(() => a.page.getByRole("button", { name: "返回任务列表", exact: true }).click());
  }
  requireFact(A.sensitive === 8 && B.sensitive === 6 && totpWaits === 8 && new Set(actors.flatMap(a => a.tasks)).size === 6,
    "proof_accounting_invalid");
  const projectionDeadline = Date.now() + 60_000;
  function provenance(a: Actor): any[] {
    return sql(`SELECT COALESCE(json_agg(p),'[]'::json) FROM (
      SELECT m.message_id AS "messageId",m.source_task_id AS "taskUUID",m.title,m.description,m.occurred_at AS "occurredAt",
        m.source_event_id AS "eventId",o.id AS "outboxId",
        m.tenant_id AS "messageTenant",t.tenant_id AS "taskTenant",e.tenant_id AS "eventTenant",u.tenant_id AS "userTenant",
        m.recipient_user_id AS "recipientUserId",t.actor_id AS "taskActorId",
        e.task_id AS "eventTaskId",o.task_id AS "outboxTaskId",o.event_id AS "outboxEventId",m.source_result_version AS "sourceResultVersion",e.result_version AS "eventResultVersion",
        t.kind,e.action,e.state,t.domain_id AS "taskDomain",e.domain_id AS "eventDomain",
        m.tenant_id=t.tenant_id AND t.tenant_id=e.tenant_id AND e.tenant_id=u.tenant_id AS "tenantEqual",
        m.recipient_user_id=t.actor_id AND t.actor_id=u.id AS "actorEqual",
        m.source_task_id=t.task_id AND t.task_id=e.task_id AND e.task_id=o.task_id AS "taskEqual",
        m.source_event_id=e.id AND e.id=o.event_id AS "eventEqual"
      FROM adtr.messages m JOIN adtr.tasks t ON t.task_id=m.source_task_id
      JOIN adtr.task_events e ON e.id=m.source_event_id JOIN adtr.task_outbox o ON o.event_id=e.id
      JOIN adtr.users u ON u.id=m.recipient_user_id
      WHERE m.recipient_user_id=${a.userId} ORDER BY m.source_task_id) p`);
  }
  while (true) {
    for (const a of actors) a.provenance = provenance(a);
    if (actors.every(a => a.provenance.length === 3)) break;
    requireFact(Date.now() < projectionDeadline, "projection_deadline"); await sleep(200);
  }
  requireFact(Date.now() <= projectionDeadline, "projection_deadline");
  for (const a of actors) {
    requireFact(same(a.provenance.map(p => p.taskUUID).sort(), [...a.tasks].sort()), "projection_members_invalid");
    for (const p of a.provenance) requireFact(p.tenantEqual && p.actorEqual && p.taskEqual && p.eventEqual &&
      p.messageTenant === p.taskTenant && p.taskTenant === p.eventTenant && p.eventTenant === p.userTenant &&
      p.userTenant === identity(a).tenant_id && p.recipientUserId === a.userId && p.taskActorId === a.userId &&
      p.eventTaskId === p.taskUUID && p.outboxTaskId === p.taskUUID && p.outboxEventId === p.eventId &&
      p.sourceResultVersion === p.eventResultVersion && p.kind === "infrastructure.health" &&
      p.taskDomain === "platform" && p.eventDomain === "platform" && p.action === "finished" && p.state === "succeeded",
    "projection_provenance_invalid");
    requireFact(state(a).reads.length === 0, "fresh_history_not_empty");
  }
  requireFact(Date.now() <= projectionDeadline, "projection_deadline");
  // First query only after all six task projections exist. One global 60s
  // deadline covers ALL query, database, request, tab and screenshot assertions.
  snapshotStarted = Date.now();
  const baselines: any[] = [];
  for (const a of actors) {
    await observe(() => navigateTo(a.page, "站内消息"));
    a.snapshot = await query(a);
    requireFact(a.snapshot.counts.read === 0 && a.snapshot.counts.unread === 3, "initial_counts_invalid");
    live(a.snapshot);
    a.read = await uiResponse(a.page, "/api/messages/read", () =>
      a.page.locator(`article[data-message-id="${a.snapshot.items[0].messageId}"]`)
        .getByRole("button", { name: "标记为已读", exact: true }).click(), "message_write_transport");
    requireFact(identifier(a.read.operationId) && a.read.resultId === a.snapshot.resultId &&
      a.read.messageId === a.snapshot.items[0].messageId && same(a.read.progress,
        { target: 1, processed: 1, newlyRead: 1, alreadyRead: 0, failed: 0, remaining: 0 }), "own_read_receipt_invalid");
    a.snapshot = await query(a);
    const baseline = state(a);
    requireFact(baseline.messages.length === 3 && baseline.reads.length === 1 && baseline.single.length === 1 &&
      baseline.reads[0].message_id === a.read.messageId &&
      sameInstant(baseline.reads[0].first_read_at, a.read.firstReadAt) &&
      sameInstant(baseline.single[0].first_read_at, a.read.firstReadAt) &&
      a.snapshot.counts.read === 1 && a.snapshot.counts.unread === 2, "nonempty_read_baseline_invalid");
    baselines.push(baseline);
    await positive(a);
    for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
      await observe(async () => {
        await a.page.setViewportSize(viewport);
        // No credential field, open modal, or unrelated workspace may be captured.
        requireFact(await a.page.locator('input[type="password"], input[autocomplete="one-time-code"], [role="dialog"]:visible').count() === 0,
          "screenshot_proof_not_empty");
        await expect(a.page.getByRole("heading", { name: "站内消息", exact: true })).toBeVisible();
        await expect(a.page.locator("article[data-message-id]")).toHaveCount(3);
        await a.page.evaluate(() => window.scrollTo(0, 0));
        const file = `messages-soc-isolation-${a.label}-${viewport.width}.png`;
        const bytes = await a.page.screenshot({ path: testInfo.outputPath(file), fullPage: true, timeout: Math.min(5000, remaining()) });
        screenshots.push({ file, viewport, fullPage: true, identity: a.label, proofEmpty: true,
          sha256: createHash("sha256").update(bytes).digest("hex") });
      });
    }
    await observe(() => a.page.setViewportSize({ width: 1440, height: 900 }));
  }
  for (const a of actors) {
    const other = a === A ? B : A;
    const before = state(a), writeCount = a.writes;
    const tab = await observe(() => a.context.newPage());
    await observe(async () => {
      await tab.goto(a.page.url());
      await expect(tab.getByRole("heading", { name: "站内消息", exact: true })).toBeVisible();
      const text = await tab.locator("body").innerText();
      requireFact(!other.snapshot.items.some(i => text.includes(i.messageId) || text.includes(i.taskUUID)) &&
        !text.includes(other.read.operationId), "foreign_tab_history");
    });
    let tabSnapshot = await query(a, tab);
    for (let visit = 0; visit < 2; visit++) {
      await observe(async () => {
        if (visit === 0) await navigateTo(tab, "账户概览");
        else {
          await tab.goForward();
          await expect(tab.getByRole("heading", { name: "账户概览", exact: true })).toBeVisible();
        }
        await tab.goBack();
        await expect(tab.getByRole("heading", { name: "站内消息", exact: true })).toBeVisible();
      });
      if (await observe(() => tab.locator("article[data-message-id]").count()) === 0) tabSnapshot = await query(a, tab);
      else await observe(async () => {
        await expect(tab.getByText(tabSnapshot.resultId, { exact: true })).toBeVisible();
        await expect(tab.locator("article[data-message-id]")).toHaveCount(3);
        for (const item of tabSnapshot.items) await expect(tab.locator(`article[data-message-id="${item.messageId}"]`)).toBeVisible();
      });

    }
    requireFact(a.writes === writeCount && same(before, state(a)), "tab_silent_mutation");
    await observe(() => tab.close());
  }
  // Both current authorities are positively established on each side of each
  // directional phase. Each individual denial has a full independent DB delta.
  for (const [caller, owner] of [[A, B], [B, A]]) {
    await positive(caller); await positive(owner);
    const foreign = owner.snapshot, own = caller.snapshot;
    await negative(`${caller.label}-foreign-page`, caller, owner, () =>
      get(caller, `/api/messages/page?resultId=${foreign.resultId}&offset=0&limit=1`));
    await negative(`${caller.label}-foreign-locate`, caller, owner, () =>
      get(caller, `/api/messages/locate?resultId=${foreign.resultId}&messageId=${foreign.items[1].messageId}&limit=1`));
    await negative(`${caller.label}-foreign-single-receipt`, caller, owner, () =>
      get(caller, `/api/messages/read-operation?operationId=${owner.read.operationId}`));
    await negative(`${caller.label}-foreign-read`, caller, owner, () => post(caller, "/api/messages/read",
      { resultId: foreign.resultId, messageId: foreign.items[1].messageId, operationId: randomUUID() }));
    await negative(`${caller.label}-foreign-single-replay`, caller, owner, () => post(caller, "/api/messages/read",
      { resultId: owner.read.resultId, messageId: owner.read.messageId, operationId: owner.read.operationId }));
    for (const selection of ["selected", "all"]) await negative(`${caller.label}-foreign-bulk-${selection}`, caller, owner, () =>
      post(caller, "/api/messages/bulk-read", { resultId: foreign.resultId, operationId: randomUUID(), selection,
        ...(selection === "selected" ? { messageIds: [foreign.items[1].messageId] } : {}) }));
    await negative(`${caller.label}-own-result-foreign-single`, caller, owner, () => post(caller, "/api/messages/read",
      { resultId: own.resultId, messageId: foreign.items[0].messageId, operationId: randomUUID() }), 400, "target_not_in_result");
    for (const mixed of [false, true]) await negative(`${caller.label}-own-result-foreign-selected-${mixed}`, caller, owner, () =>
      post(caller, "/api/messages/bulk-read", { resultId: own.resultId, operationId: randomUUID(), selection: "selected",
        messageIds: [...(mixed ? [own.items.find(i => !i.read)!.messageId] : []), foreign.items[0].messageId] }), 400, "target_not_in_result");
    await positive(caller); await positive(owner);
  }
  function validBulk(value: any, input: any) {
    requireFact(value && value.receipt && value.progress &&
      same(Object.keys(value).sort(), ["progress", "receipt", "state", "updatedAt"]) &&
      same(Object.keys(value.receipt).sort(), ["acceptedAt", "operationId", "resultId", "selection", "target"]) &&
      same(Object.keys(value.progress).sort(), ["alreadyRead", "failed", "newlyRead", "processed", "remaining", "target"]) &&
      value.receipt.operationId === input.operationId && value.receipt.resultId === input.resultId &&
      value.receipt.selection === input.selection && ["queued", "running", "completed"].includes(value.state) &&
      Number.isFinite(Date.parse(value.receipt.acceptedAt)) && Number.isFinite(Date.parse(value.updatedAt)), "bulk_response_invalid");
    const p = value.progress;
    requireFact(Object.values(p).every(v => Number.isSafeInteger(v) && Number(v) >= 0) &&
      p.target === p.processed + p.remaining && p.processed === p.newlyRead + p.alreadyRead &&
      p.failed === 0 && value.receipt.target === p.target, "bulk_conservation_invalid");
  }
  for (const [actor, protectedActor] of [[A, B], [B, A]]) {
    const protectedBefore = state(protectedActor), protectedVersions = versions(protectedActor);
    await positive(actor); await positive(protectedActor);
    // Reuse the exact frozen three-member result for selected then all.
    const frozen = actor.snapshot;
    for (const selection of ["selected", "all"]) {
      live(frozen);
      const before = state(actor);
      const selectedId = frozen.items.find(i => !i.read)!.messageId;
      const input = { resultId: frozen.resultId, operationId: randomUUID(), selection,
        ...(selection === "selected" ? { messageIds: [selectedId] } : {}) };
      const accepted = await post(actor, "/api/messages/bulk-read", input);
      requireFact(accepted.status === 200, "bulk_admission_failed");
      validBulk(accepted.body, input);
      requireFact(same(protectedBefore, state(protectedActor)), "bulk_admission_cross_user_change");
      let operation = accepted.body;
      while (operation.state !== "completed") {
        live(frozen);
        requireFact(same(protectedBefore, state(protectedActor)), "bulk_processing_cross_user_change");
        await sleep(Math.min(100, remaining()));
        const r = await get(actor, `/api/messages/bulk-read-operation?operationId=${input.operationId}`);
        requireFact(r.status === 200, "bulk_lookup_failed"); validBulk(r.body, input); operation = r.body;
      }
      const expected = selection === "selected" ?
        { target: 1, processed: 1, newlyRead: 1, alreadyRead: 0, failed: 0, remaining: 0 } :
        { target: 3, processed: 3, newlyRead: 1, alreadyRead: 2, failed: 0, remaining: 0 };
      requireFact(same(operation.progress, expected), "bulk_terminal_totals_invalid");
      const after = state(actor);
      requireFact(same(before.messages, after.messages) && same(before.identity, after.identity) &&
        same(before.single, after.single) && after.reads.length === before.reads.length + 1 &&
        before.reads.every((row: any) => after.reads.some((other: any) => same(row, other))) &&
        after.bulk.length === before.bulk.length + 1 && before.bulk.every((row: any) => after.bulk.some((other: any) => same(row, other))),
      "bulk_own_delta_invalid");
      const persistedOperations = after.bulk.filter((row: any) => row.operation_id === input.operationId);
      requireFact(persistedOperations.length === 1, "bulk_operation_row_missing");
      const persistedOperation = persistedOperations[0];
      requireFact(persistedOperation.result_id === input.resultId && persistedOperation.selection === selection &&
        persistedOperation.state === "completed" && persistedOperation.target === expected.target &&
        persistedOperation.processed === expected.processed && persistedOperation.newly_read === expected.newlyRead &&
        persistedOperation.already_read === expected.alreadyRead &&
        sameInstant(persistedOperation.accepted_at, operation.receipt.acceptedAt) &&
        sameInstant(persistedOperation.updated_at, operation.updatedAt), "bulk_operation_row_mismatch");
      const targets = after.targets.filter((r: any) => r.operation_id === input.operationId);
      const receipts = after.receipts.filter((r: any) => r.operation_id === input.operationId);
      requireFact(targets.length === expected.target && receipts.length === expected.target &&
        after.targets.length === before.targets.length + expected.target &&
        after.receipts.length === before.receipts.length + expected.target &&
        before.targets.every((row: any) => after.targets.some((other: any) => same(row, other))) &&
        before.receipts.every((row: any) => after.receipts.some((other: any) => same(row, other))) &&
        same(targets.map((r: any) => r.message_id).sort(), selection === "selected" ? [selectedId] : frozen.items.map(i => i.messageId).sort()) &&
        receipts.filter((r: any) => r.newly_read).length === 1 && receipts.every((r: any) => {
          const target = targets.find((t: any) => t.ordinal === r.ordinal);
          return after.reads.some((read: any) => read.message_id === target?.message_id && read.first_read_at === r.first_read_at);
        }), "bulk_persisted_receipts_invalid");
      const ownerLookup = await get(actor, `/api/messages/bulk-read-operation?operationId=${input.operationId}`);
      requireFact(ownerLookup.status === 200 && same(ownerLookup.body, operation), "owner_bulk_receipt_failed");
      const protectedAfter = state(protectedActor);
      requireFact(same(protectedBefore, protectedAfter) && same(protectedVersions, versions(protectedActor)), "bulk_terminal_cross_user_change");
      positiveBulks.push({ actor: actor.label, selection, operation, before, after, protectedBefore, protectedAfter });
      await negative(`${protectedActor.label}-foreign-bulk-receipt-${selection}`, protectedActor, actor, () =>
        get(protectedActor, `/api/messages/bulk-read-operation?operationId=${input.operationId}`));
      await negative(`${protectedActor.label}-foreign-bulk-replay-${selection}`, protectedActor, actor, () =>
        post(protectedActor, "/api/messages/bulk-read", input));
      await negative(`${protectedActor.label}-foreign-completed-cancel-${selection}`, protectedActor, actor, () =>
        post(protectedActor, "/api/messages/bulk-read-cancel", { operationId: input.operationId }));
      const unchanged = await get(actor, `/api/messages/bulk-read-operation?operationId=${input.operationId}`);
      requireFact(unchanged.status === 200 && same(unchanged.body, operation), "terminal_cancel_changed_owner");
      actor.snapshot = await query(actor);
      requireFact(actor.snapshot.counts.read === (selection === "selected" ? 2 : 3) &&
        actor.snapshot.counts.unread === (selection === "selected" ? 1 : 0), "fresh_bulk_counts_invalid");
      const old = await get(actor, `/api/messages/page?resultId=${frozen.resultId}&offset=0&limit=20`);
      requireFact(old.status === 200 && same(old.body, frozen), "frozen_snapshot_changed");
    }
    await positive(actor); await positive(protectedActor);
    requireFact(same(protectedBefore, state(protectedActor)), "directional_protected_history_changed");
  }
  live(...actors.flatMap(a => a.snapshots));
  requireFact(actors.every(a => a.queries >= 5 && a.queries <= 7 && state(a).reads.length === 3), "final_counts_invalid");
  const snapshotElapsedMs = Date.now() - snapshotStarted;
  requireFact(snapshotElapsedMs <= 60_000 && stages.length === 32 && positiveBulks.length === 4 && screenshots.length === 4,
    "evidence_incomplete");
  // Hash collection is source observation only. No credentials or session state.
  function revision(...args: string[]): string {
    try { return execFileSync("git", args, { encoding: "utf8", timeout: Math.min(5000, remaining()), stdio: ["ignore", "pipe", "pipe"] }).trim(); }
    catch { throw new Error("source_observation_failed"); }
  }
  const root = revision("rev-parse", "--show-toplevel");
  const hashFile = (file: string) => {
    try { return createHash("sha256").update(readFileSync(resolve(root, file))).digest("hex"); }
    catch { throw new Error("source_observation_failed"); }
  };
  const sourcePaths = [
    "web/e2e/messages-isolation.spec.ts",
    "scripts/test_messages_isolation_browser_contract.py",
    "scripts/test_auth_e2e.py",
    "scripts/ci_inventory.json",
    "scripts/ci_selection.py",
    "scripts/test_ci_selection_contract.py",
    ".github/workflows/ci.yml",
    "docs/messages-authenticated-isolation-evidence.md",
    "web/playwright.config.ts",
    "scripts/ci_workflow_contract.py",
    "scripts/fixtures/messages_isolation_ci_delta.json",
    "scripts/test_directory_v4_controls_ci_contract.py",
    "scripts/test_directory_v4_runner_contract.py",
    "scripts/fixtures/messages_isolation_runner_delta.json",
    "internal/auth/message_http.go",
    "internal/messages/projection.go",
    "internal/messages/snapshot.go",
    "internal/messages/read.go",
    "internal/messages/bulk.go",
    "web/e2e/messages-bulk.spec.ts",
    "web/e2e/messages.spec.ts",
    "web/e2e/access.spec.ts",
    "internal/testmessagefixture/main.go",
    "internal/auth/access_users.go",
    "internal/auth/access_roles.go",
    "internal/auth/task_authorization.go",
    "internal/auth/task_http.go",
    "internal/messages/types.go",
    "docs/messages-other-user-read-history-evidence.md",
    "internal/auth/access_http.go",
    "internal/auth/service.go",
    "internal/messagecore/types.go",
    "web/e2e/navigation.ts",
    "web/e2e/safe-runtime-diagnostics.ts",
    "web/test-helpers/navigation.ts",
    "web/src/MessagesWorkspace.tsx",
    "web/src/TaskWorkspace.tsx",
    "web/src/message-api.ts",
    "web/src/message-read.tsx",
    "web/src/message-read-intent.ts",
    "web/package-lock.json",
    "internal/messages/schema.go",
    "internal/messages/bulk_schema.go",
    "internal/messagecore/result.go"
];
  const frontendFiles: Record<string, string> = {};
  function hashFrontend(directory: string) {
    remaining();
    try {
      for (const entry of readdirSync(resolve(root, directory)).sort()) {
        const file = `${directory}/${entry}`, stat = lstatSync(resolve(root, file));
        requireFact(!stat.isSymbolicLink(), "frontend_symlink_forbidden");
        if (stat.isDirectory()) hashFrontend(file);
        else {
          requireFact(stat.isFile(), "frontend_entry_invalid");
          frontendFiles[file] = hashFile(file);
        }
      }
    } catch { throw new Error("source_observation_failed"); }
  }
  hashFrontend("web/dist");
  const manifest = { formatVersion: 1, suite: "messages-isolation", status: "passed",
    checkoutCommit: revision("rev-parse", "HEAD"),
    checkoutTree: revision("rev-parse", "HEAD^{tree}"),
    dirty: revision("status", "--porcelain") !== "",
    workflowCommit: process.env.GITHUB_SHA, workflowRun: process.env.GITHUB_RUN_ID, workflowAttempt: process.env.GITHUB_RUN_ATTEMPT,
    browser: browser.version(), screenshots,
    identities: actors.map((a, i) => ({ label: a.label, userId: a.userId, tenantId: identity(a).tenant_id,
      authorizationVersion: identity(a).authorization_version, sensitiveActions: a.sensitive, snapshotAdmissions: a.queries,
      taskIds: a.tasks, provenance: a.provenance, baseline: baselines[i] })),
    stages, positiveBulks, phaseBudget: { snapshotElapsedMs, limitMs: 60_000, totpWaits, setupSpent },
    sourceSHA256: Object.fromEntries(sourcePaths.map(file => [file, hashFile(file)])),
    artifactsSHA256: { apiWorker: hashFile("bin/adtr"), frontendEntry: hashFile("web/dist/index.html"), frontendFiles },
    limitations: ["synthetic tasks-writer role, not reader-only", "API pagination, not two full UI pages",
      "completed-operation foreign cancellation only", "screenshots require independent pixel review"] };
  live();
  manifest.phaseBudget.snapshotElapsedMs = Date.now() - snapshotStarted;
  requireFact(manifest.phaseBudget.snapshotElapsedMs <= 60_000, "snapshot_phase_deadline");
  const path = testInfo.outputPath("messages-soc-isolation-evidence.json");
  writeFileSync(path, JSON.stringify(manifest, null, 2) + "\n");
  for (const shot of screenshots) await testInfo.attach(shot.file, { path: testInfo.outputPath(shot.file), contentType: "image/png" });
  await testInfo.attach("messages-soc-isolation-evidence", { path, contentType: "application/json" });
  for (const a of actors) await safe("browser_observation", () => a.context.close());
});
