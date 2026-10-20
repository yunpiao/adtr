import { expect, type Page, type BrowserContext, type APIResponse, type Response, type TestInfo } from "@playwright/test";
import { request as nodeRequest } from "node:http";
import { createHash } from "node:crypto";
import { writeV4ScenarioEvidence } from "./directory-v4-controls-evidence";
import { parseDirectoryV4Task } from "../src/directory-v4-controls-api";
import { parseDirectoryV4UniqueJSON } from "../src/directory-v4-controls-json";
import { parseGroupMembersV4Page } from "../src/group-members-v4-read";
import { parseGroupMembersV4 } from "../src/group-members-v4-api";
import { directoryV4DisplayMember } from "../src/directory-v4-facts";
import { freshCode, fillSecret, totp, type Authenticator } from "./directory-v4-fixture";
import type { Task } from "../src/task-api";
import { v4Phase } from "./directory-v4-diagnostics";
import { recordV4BodyClass, readV4ObservedObject, waitForV4RawMembers } from "./directory-v4-result-observation";

export const V4 = "/api/directory/v4/";
export function invariant(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }
export async function bounded<T>(operation: Promise<T>, milliseconds: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try { return await Promise.race([operation, new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error(message)), milliseconds); })]); }
  finally { if (timer) clearTimeout(timer); }
}
export type Original = { domainId: string; expectedRevision: string; expectedCredentialGeneration: string; idempotencyKey: string; taskUUID: string };
export type Actor = { username: string; password: string; auth: Authenticator; actorId: number };
export type Mutation = { operation: "sync" | "cancel"; at: number; currentProof: boolean; original: Partial<Original> };
export const evidenceDigest = (values: unknown[]) => createHash("sha256").update(JSON.stringify(values)).digest("hex");
export function originalEvidence(original: Original, actorId: number) {
  return { taskSHA256: evidenceDigest([original.taskUUID]), domainSHA256: evidenceDigest([original.domainId]),
    actorSHA256: evidenceDigest([String(actorId)]), sourceSHA256: evidenceDigest([original.domainId, original.expectedRevision, original.expectedCredentialGeneration]),
    keySHA256: evidenceDigest([original.idempotencyKey]) };
}
export function mutationEvidence(entries: Mutation[]) {
  return entries.map(entry => ({
    operation: entry.operation, dispatchMilliseconds: entry.at, counterOffsetMilliseconds: entry.at % 30_000, currentProof: entry.currentProof,
    domainSHA256: entry.original.domainId ? evidenceDigest([entry.original.domainId]) : null,
    sourceSHA256: entry.operation === "sync" ? evidenceDigest([entry.original.domainId, entry.original.expectedRevision, entry.original.expectedCredentialGeneration]) : null,
    keySHA256: entry.original.idempotencyKey ? evidenceDigest([entry.original.idempotencyKey]) : null,
    taskSHA256: entry.original.taskUUID ? evidenceDigest([entry.original.taskUUID]) : null,
  }));
}
export function guardLeaks(context: BrowserContext, secrets: string[]) {
  let leaked = false;
  const pages = new Set<Page>();
  const check = (text: string) => { if (secrets.some(secret => secret && text.includes(secret))) leaked = true; };
  const consoleMessage = (message: import("@playwright/test").ConsoleMessage) => check(message.text());
  const pageError = (error: Error) => check(error.message);
  const attach = (page: Page) => { pages.add(page); page.on("console", consoleMessage); page.on("pageerror", pageError); };
  const request = (r: import("@playwright/test").Request) => {
    if (r.method() !== "POST") return;
    try { const body = r.postDataJSON(); for (const key of ["actorPassword", "totpCode"]) if (typeof body?.[key] === "string" && body[key]) secrets.push(body[key]); } catch { /* Non-JSON transport has no decoded proof. */ }
  };
  context.pages().forEach(attach); context.on("page", attach); context.on("request", request);
  return {
    assertClean: () => invariant(!leaked, "Proof appeared in browser console/error output"),
    close: () => { context.off("page", attach); context.off("request", request); for (const page of pages) { page.off("console", consoleMessage); page.off("pageerror", pageError); } },
  };
}
// Never expose request bodies, secret strings, raw exceptions or arbitrary response objects.
export function observeMutations(context: BrowserContext, actor: Actor) {
  const entries: Mutation[] = [];
  const listener = (request: import("@playwright/test").Request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== "POST" || ![V4 + "sync", V4 + "cancel"].includes(path)) return;
    try {
      const body = request.postDataJSON(), at = Date.now();
      const operation = path.endsWith("sync") ? "sync" : "cancel";
      entries.push({ operation, at, currentProof: body.totpCode === totp(actor.auth.secret, at), original: operation === "sync" ? {
        domainId: body.domainId, expectedRevision: body.expectedRevision, expectedCredentialGeneration: body.expectedCredentialGeneration, idempotencyKey: body.idempotencyKey,
      } : { taskUUID: body.taskUUID } });
    } catch { throw new Error("Invalid actual UI mutation request"); }
  };
  context.on("request", listener);
  return { entries, counts: () => ({ sync: entries.filter(e => e.operation === "sync").length, cancel: entries.filter(e => e.operation === "cancel").length }), close: () => context.off("request", listener) };
}
export async function actualJSON(response: Response | APIResponse, actorId: number): Promise<Record<string, unknown>> {
  invariant(response.status() === 200, "Expected genuine successful API response");
  const headers = response.headers();
  invariant(headers["cache-control"] === "no-store" && headers["x-adtr-user-id"] === String(actorId) && headers["x-content-type-options"] === "nosniff", "Real response metadata did not match actor/no-store contract");
  v4Phase("body-start");
  // Only a closed transport/media-type enum, never actual headers or URLs.
  recordV4BodyClass("request" in response, headers["content-type"]);
  const value = await readV4ObservedObject(() => response.body());
  v4Phase("body-complete");
  return value;
}
export async function taskResponse(response: Response | APIResponse, actor: Actor, domainId: string, operation: "sync" | "cancel" | "read", taskUUID?: string) {
  const value = await actualJSON(response, actor.actorId);
  invariant(Object.keys(value).sort().join(",") === (operation === "sync" ? "replayed,task" : "task"), "Unexpected real task envelope");
  if (operation === "sync") invariant(value.replayed === false, "Expected a new actual UI admission");
  try { return parseDirectoryV4Task(value.task, domainId, taskUUID); }
  catch { throw new Error("Real task failed exact v4 identity/grammar checks"); }
}
export async function openCollection(page: Page) {
  v4Phase("workspace-open");
  await page.goto("/#directory-v4");
  await expect(page.getByRole("heading", { name: "字典 4 目录采集", exact: true })).toBeVisible();
  v4Phase("workspace-ready");
}
export async function prepareSync(page: Page) {
  v4Phase("source-select");
  await page.getByRole("region", { name: "字典 4 已保存来源", exact: true }).getByRole("button", { name: /^synthetic\.invalid · 配置 /u }).click();
  v4Phase("sync-prepare");
  await page.getByRole("button", { name: "准备采集字典 4", exact: true }).click();
}
export function form(page: Page, operation: "sync" | "cancel") {
  return page.getByRole("form", { name: operation === "sync" ? "字典 4 采集证明" : "原任务取消证明", exact: true });
}
export async function enterProof(page: Page, actor: Actor, operation: "sync" | "cancel", code?: string) {
  v4Phase("proof-start");
  await expect(form(page, operation).locator('input[type="password"], input[inputmode="numeric"]')).toHaveCount(2);
  await fillSecret(form(page, operation).getByLabel("当前用户密码", { exact: true }), actor.password);
  await fillSecret(form(page, operation).getByLabel("当前六位验证码", { exact: true }), code ?? await freshCode(actor.auth));
  invariant(await form(page, operation).locator('input[type="password"], input[inputmode="numeric"]').evaluateAll(nodes => nodes.length === 2 && nodes.every(node => (node as HTMLInputElement).value.length > 0)), "Actual proof inputs were not populated");
  v4Phase("proof-ready");
}
export async function clickMutation(page: Page, actor: Actor, domainId: string, operation: "sync" | "cancel", taskUUID?: string) {
  v4Phase("mutation-click");
  const pending = page.waitForResponse(r => new URL(r.url()).pathname === V4 + operation && r.request().method() === "POST");
  void pending.catch(() => undefined);
  await form(page, operation).getByRole("button", { name: operation === "sync" ? "确认提交采集" : "确认取消原任务", exact: true }).click();
  const response = await pending;
  v4Phase("mutation-response");
  return taskResponse(response, actor, domainId, operation, taskUUID);
}
export function originalFrom(task: Task, mutation: Mutation): Original {
  const v = mutation.original;
  invariant(mutation.operation === "sync" && typeof v.domainId === "string" && typeof v.expectedRevision === "string" && typeof v.expectedCredentialGeneration === "string" && typeof v.idempotencyKey === "string", "Missing actual admitted source pins");
  return { domainId: v.domainId, expectedRevision: v.expectedRevision, expectedCredentialGeneration: v.expectedCredentialGeneration, idempotencyKey: v.idempotencyKey, taskUUID: task.taskUUID };
}
export async function readTask(context: BrowserContext, actor: Actor, original: Original) {
  return taskResponse(await context.request.get(V4 + "task", { params: { taskUUID: original.taskUUID } }), actor, original.domainId, "read", original.taskUUID);
}
export async function terminal(context: BrowserContext, actor: Actor, original: Original, state: "succeeded" | "cancelled") {
  let result: Task | undefined;
  await expect.poll(async () => {
    result = await readTask(context, actor, original);
    invariant(!["failed", "dead_letter", "partial_failed", state === "succeeded" ? "cancelled" : "succeeded"].includes(result.state), "Actual task reached wrong terminal state");
    return result.state;
  }, { timeout: 60_000, intervals: [100, 250, 500], message: "Original real task must reach required terminal state" }).toBe(state);
  return result!;
}
export async function recover(page: Page, actor: Actor, original: Original) {
  const receipt = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "receipt" && r.request().method() === "GET");
  await page.reload();
  const response = await receipt, query = new URL(response.url()).searchParams;
  invariant(query.get("domainId") === original.domainId && query.get("idempotencyKey") === original.idempotencyKey, "Recovery did not query exact original receipt");
  await taskResponse(response, actor, original.domainId, "read", original.taskUUID);
  await expect(page.getByRole("region", { name: "原任务状态", exact: true })).toContainText(original.taskUUID);
  await proofEmpty(page);
  return { status: response.status(), taskSHA256: evidenceDigest([original.taskUUID]),
    keySHA256: evidenceDigest([query.get("idempotencyKey")]), domainSHA256: evidenceDigest([query.get("domainId")]) };
}
export async function refreshOriginal(page: Page) {
  await page.getByRole("button", { name: "查询原操作回执与状态", exact: true }).click();
}
export async function acknowledge(page: Page) {
  await page.getByRole("button", { name: "确认结束原操作并清除恢复记录", exact: true }).click();
  await expect(page.getByRole("region", { name: "原操作恢复", exact: true })).toHaveCount(0);
}
export async function proofEmpty(page: Page) {
  const fields = page.locator('.directory-v4-workspace input[type="password"], .directory-v4-workspace input[inputmode="numeric"]');
  invariant(await fields.evaluateAll(nodes => nodes.every(node => (node as HTMLInputElement).value === "")), "Proof not empty at safe boundary");
}
export async function safeScreenshots(page: Page, info: TestInfo, stem: string, secrets: readonly string[]) {
  invariant(/^[a-z0-9-]+$/u.test(stem), "Unsafe screenshot name");
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await proofEmpty(page);
    invariant(await page.locator('input[type="password"], input[autocomplete="one-time-code"], input[inputmode="numeric"]').evaluateAll(nodes => nodes.every(node => (node as HTMLInputElement).value === "")), "Unsafe screenshot proof field");
    const safety = await page.evaluate(known => {
      const contains = (value: string) => known.some(secret => secret && value.includes(secret));
      const bodyClean = !contains(document.body.textContent ?? "");
      try {
        let storageClean = true;
        for (let index = 0; index < sessionStorage.length; index++) {
          const key = sessionStorage.key(index);
          if (key !== null && (contains(key) || contains(sessionStorage.getItem(key) ?? ""))) storageClean = false;
        }
        return { bodyClean, storageClean, storageAccessible: true };
      } catch { return { bodyClean, storageClean: false, storageAccessible: false }; }
    }, [...secrets]);
    invariant(safety.bodyClean && safety.storageClean && safety.storageAccessible, "Screenshot body/storage proof safety not established");
    await page.screenshot({ path: info.outputPath(`${stem}-${width}.png`), fullPage: false });
  }
}
// The response is fetched exactly once from the server, validated, then withheld.
// Never fabricate a successful body and never repeat a POST after uncertain delivery.
export async function committedLoss(page: Page, actor: Actor, domainId: string, operation: "sync" | "cancel", expectedState?: Task["state"], taskUUID?: string) {
  let resolve!: (task: Task) => void, reject!: (error: Error) => void;
  const completed = new Promise<Task>((yes, no) => { resolve = yes; reject = no; });
  void completed.catch(() => undefined);
  await page.route("**" + V4 + operation, async route => {
    try {
      invariant(route.request().method() === "POST", "Loss route received unexpected method");
      const response = await route.fetch({ maxRetries: 0, maxRedirects: 0, timeout: 15_000 });
      const task = await taskResponse(response, actor, domainId, operation, taskUUID);
      if (expectedState) invariant(task.state === expectedState, "Real committed cancellation missed required state/window");
      await route.abort("failed"); resolve(task);
    } catch { await route.abort("failed").catch(() => undefined); reject(new Error("Genuine committed-response loss validation failed")); }
  }, { times: 1 });
  const boundedCompletion = bounded(completed, 20_000, "Committed-response loss did not finish");
  void boundedCompletion.catch(() => undefined);
  return { completed: boundedCompletion };
}
export async function pinnedResults(page: Page, actor: Actor, original: Original) {
  const pending = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
  await page.getByRole("button", { name: "查看本任务的原始组成员结果", exact: true }).click();
  const response = await pending, params = new URL(response.url()).searchParams;
  invariant(params.get("observationId") === original.taskUUID && params.get("domainId") === original.domainId && params.get("kind") === "group", "Original result read lost original pins");
  const value = parseGroupMembersV4Page(await actualJSON(response, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: 25 });
  invariant(value.available && value.observationId === original.taskUUID && value.list.length > 0, "Expected real nonempty v4 group observation");
  const semantics = { absent: false, empty: false, values: false };
  for (const row of value.list) {
    const next = page.waitForResponse(r => new URL(r.url()).pathname === "/api/group-assets/v4/members");
    await page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByRole("button", { name: directoryV4DisplayMember(row.samAccountName ?? row.distinguishedName), exact: true }).click();
    const detailResponse = await next, q = new URL(detailResponse.url()).searchParams;
    invariant(q.get("observationId") === original.taskUUID && q.get("domainId") === original.domainId && q.get("expectedRevision") === original.expectedRevision && q.get("expectedCredentialRevision") === original.expectedCredentialGeneration && q.get("objectGUID") === row.objectGUID, "Raw detail lost original five pins");
    const detail = parseGroupMembersV4(await actualJSON(detailResponse, actor.actorId), { domainId: original.domainId, expectedRevision: original.expectedRevision, expectedCredentialRevision: original.expectedCredentialGeneration, observationId: original.taskUUID, objectGUID: row.objectGUID });
    invariant(JSON.stringify(detail.object) === JSON.stringify(row), "Raw detail differs from the same original page object");
    const region = page.getByRole("region", { name: "组原始成员详情", exact: true });
    if (row.rawMember === null) { semantics.absent = true; await expect(region).toContainText("无法区分属性不存在与 ACL 隐藏"); }
    else if (!row.rawMember.length) { semantics.empty = true; await expect(region).toContainText("不证明该组为空"); }
    else { semantics.values = true; await waitForV4RawMembers(() => region.locator("ol > li").allTextContents(), row.rawMember.map(directoryV4DisplayMember)); }
  }
  invariant(Object.values(semantics).every(Boolean), "Real fixture did not cover absent/empty/value raw semantics");
  for (const size of [50, 25]) {
    const next = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
    await page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByLabel("每页组数", { exact: true }).selectOption(String(size));
    const resized = await next, q = new URL(resized.url()).searchParams;
    invariant(q.get("observationId") === original.taskUUID && q.get("domainId") === original.domainId && q.get("kind") === "group", "Page-size change fell back from original pins");
    parseGroupMembersV4Page(await actualJSON(resized, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: size });
  }
  const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
  await page.getByRole("button", { name: "刷新原始结果", exact: true }).click();
  const reread = await refreshed;
  invariant(new URL(reread.url()).searchParams.get("observationId") === original.taskUUID, "Refresh fell back from original observation");
  parseGroupMembersV4Page(await actualJSON(reread, actor.actorId), { domainId: original.domainId, observationId: original.taskUUID, pageIdx: 1, pageSize: 25 });
  await expect(page.getByRole("region", { name: "本任务的原始组成员结果", exact: true }).getByText(/本观测共有/u)).toBeVisible();
  return { ...semantics, observationSHA256: evidenceDigest([value.observationId]), detailedGroups: value.list.length };
}

export type WorkerControlState = { state: "not_started" | "starting" | "ready" | "failed" | "closing" | "closed"; launchCount: number; failure: null | string };
export function parseWorkerControlState(text: string): WorkerControlState {
  try {
    invariant(Buffer.byteLength(text) <= 2048, "Controller body limit");
    const v = parseDirectoryV4UniqueJSON(text) as WorkerControlState;
    invariant(v && Object.keys(v).sort().join(",") === "failure,launchCount,state" &&
      ["not_started", "starting", "ready", "failed", "closing", "closed"].includes(v.state) &&
      [0, 1].includes(v.launchCount) &&
      (v.failure === null || ["listener_failed", "startup_thread_failed", "launch_failed", "readiness_failed", "cleanup_failed"].includes(v.failure)) &&
      (v.state !== "not_started" || v.launchCount === 0 && v.failure === null) &&
      (v.state !== "ready" || v.launchCount === 1 && v.failure === null) &&
      (v.state !== "failed" || v.failure !== null), "Controller shape failure");
    return v;
  } catch { throw new Error("Invalid owned Worker controller state"); }
}
// Node-only controller: no page API, browser context, logging or URL in error paths.
export function workerControl() {
  const endpoint = process.env.ADTR_E2E_V4_WORKER_CONTROL_URL ?? "", token = process.env.ADTR_E2E_V4_WORKER_CONTROL_TOKEN ?? "";
  let base: URL;
  try { base = new URL(endpoint); } catch { throw new Error("Missing private owned Worker controller"); }
  invariant(base.protocol === "http:" && base.hostname === "127.0.0.1" && !!base.port && base.pathname === "/" && !base.search && !base.hash && !base.username && !base.password && /^[A-Za-z0-9_-]{43}$/u.test(token), "Invalid private owned Worker controller");
  let started = false;
  const call = (method: "GET" | "POST", path: "/state" | "/start") => new Promise<WorkerControlState>((resolve, reject) => {
    const fail = () => reject(new Error("Owned Worker controller request failed"));
    const req = nodeRequest(new URL(path, base), { method, headers: { Authorization: `Bearer ${token}`, "Content-Length": "0" }, timeout: 3000 }, response => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", chunk => { body += chunk; if (Buffer.byteLength(body) > 2048) { response.destroy(); fail(); } });
      response.on("error", fail);
      response.on("end", () => {
        try {
          invariant(response.statusCode === 200, "Controller HTTP failure");
          resolve(parseWorkerControlState(body));
        } catch { fail(); }
      });
    });
    req.on("error", fail); req.on("timeout", () => { req.destroy(); fail(); }); req.end();
  });
  return {
    state: () => call("GET", "/state"),
    async startOnce() {
      invariant(!started, "Repeated Worker start forbidden"); started = true;
      // Uncertain POST is reconciled solely by GET; never resubmit.
      await call("POST", "/start").catch(() => undefined);
      await expect.poll(async () => { const v = await call("GET", "/state"); invariant(v.failure === null && !["failed", "closing", "closed"].includes(v.state), "Owned Worker startup failed"); if (v.state === "ready") invariant(v.launchCount === 1, "Worker launch cardinality failed"); return v.state; }, { timeout: 15_000, intervals: [100] }).toBe("ready");
    },
  };
}
export async function writeEvidence(info: TestInfo, suite: "controls" | "cancel" | "readers", checks: Record<string, boolean | number | string>, witnesses: Record<string, unknown>) {
  // Final owner must register strict per-suite schemas, source provenance and cleanup
  // evidence. This file alone cannot certify a run or bypass its validator.
  invariant(Object.values(checks).every(value => typeof value === "boolean" || typeof value === "number" && Number.isSafeInteger(value) && value >= 0 || typeof value === "string" && /^[a-z_]+$/u.test(value)), "Unsafe evidence field");
  await writeV4ScenarioEvidence(info, suite, checks, witnesses);
}
