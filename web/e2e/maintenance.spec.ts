import { navigateTo } from "./navigation";
import {
  test as base,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type APIResponse,
  type Request,
  type Response,
  type Route,
  type TestInfo,
} from "@playwright/test";
import { createHash, createHmac } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import type { TaskDetail } from "../src/task-api";

const test = base.extend<{ producerContext: BrowserContext }>({
  producerContext: async ({ browser, baseURL }, use) => {
    const context = await browser.newContext({ baseURL });
    try {
      await use(context);
    } finally {
      try { await context.close(); } catch {
        throw new Error("Maintenance producer context cleanup failed");
      }
    }
  },
});

// Proof-bearing DOM/network captures must never become failure artifacts.
test.use({ trace: "off", screenshot: "off", video: "off" });

type Permission = {
  mark: string;
  auth: { readable: boolean; writeable: boolean };
};
type Authenticator = { secret: string; lastCounter: number };

function totp(secret: string, now = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const char of secret.replace(/=+$/, "").toUpperCase()) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error("Invalid synthetic TOTP secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8)
    bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes))
    .update(counter)
    .digest();
  const offset = digest[digest.length - 1] & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000)
    .toString()
    .padStart(6, "0");
}

async function freshCode(authenticator: Authenticator): Promise<string> {
  // Enrollment and every privileged write consume a counter. Use real time;
  // never freeze the browser clock or bypass the server's replay protection.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
}

async function fillSecret(field: Locator, value: string) {
  try {
    await field.fill(value);
  } catch {
    throw new Error("Unable to enter synthetic authentication proof");
  }
}

async function login(page: Page, username: string, password: string, active = () => {}) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await fillSecret(page.getByLabel("密码", { exact: true }), password);
  active();
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
  active = () => {},
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), initial);
  await fillSecret(page.getByLabel("新密码", { exact: true }), changed);
  await fillSecret(page.getByLabel("确认新密码", { exact: true }), changed);
  active();
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string, active = () => {}): Promise<Authenticator> {
  await navigateTo(page, "多因素认证");
  await fillSecret(page.getByLabel("当前密码", { exact: true }), password);
  active();
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await fillSecret(
    page.getByLabel("认证器验证码", { exact: true }),
    totp(secret, now),
  );
  active();
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  try {
    const response = await context.request.get(path);
    expect(response.status(), "Maintenance verification GET status").toBe(200);
    return await response.json();
  } catch {
    // APIRequestContext errors can contain Cookie call logs. Never propagate
    // their message, stack, cause, headers or response body to the reporter.
    throw new Error("Maintenance verification GET failed");
  }
}

async function fillActorProof(
  page: Page,
  password: string,
  mfa: Authenticator,
) {
  await fillSecret(
    page.getByLabel("操作者当前密码", { exact: true }),
    password,
  );
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
}

async function submitMutation(page: Page, path: string, button: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(200);
  const result = await response.json();
  expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
  return result;
}

// These checks never stringify their input on failure. In particular, a
// proof-bearing Request/APIResponse must not become a Playwright assertion diff.
function requireSafe(value: unknown): asserts value {
  if (!value) throw new Error("Maintenance evidence verification failed");
}
const record = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const uuid = (v: unknown): v is string =>
  typeof v === "string" && /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v);
const version = (v: unknown): v is number =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= 0;
const utc = (v: unknown): v is string => typeof v === "string" &&
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$/.test(v) && Number.isFinite(Date.parse(v));
const keys = (v: Record<string, unknown>, expected: string[]) =>
  Object.keys(v).sort().join(",") === [...expected].sort().join(",");
const sha256 = (v: string | Buffer) => createHash("sha256").update(v).digest("hex");
type ArchiveSnapshot = {
  action: "archive";
  idempotencyKey: string;
  before: string;
  reason: string;
  targets: { taskUUID: string; visibilityVersion: number }[];
};
function archiveSnapshot(input: unknown, key: unknown): ArchiveSnapshot {
  requireSafe(record(input) && keys(input, ["before", "reason", "targets"]));
  requireSafe(uuid(key) && utc(input.before));
  requireSafe(typeof input.reason === "string" && input.reason.length > 0 && input.reason.length <= 500);
  requireSafe(Array.isArray(input.targets) && input.targets.length >= 1 && input.targets.length <= 100);
  const targets = input.targets.map((target: unknown) => {
    requireSafe(record(target) && keys(target, ["taskUUID", "visibilityVersion"]) &&
      uuid(target.taskUUID) && version(target.visibilityVersion));
    return { taskUUID: target.taskUUID, visibilityVersion: target.visibilityVersion };
  }).sort((a, b) => a.taskUUID < b.taskUUID ? -1 : a.taskUUID > b.taskUUID ? 1 : 0);
  requireSafe(new Set(targets.map((target) => target.taskUUID)).size === targets.length);
  return { action: "archive", idempotencyKey: key, before: input.before, reason: input.reason, targets };
}
const intentDigest = (value: ArchiveSnapshot) => sha256(JSON.stringify(value));
const targetsDigest = (value: ArchiveSnapshot) => sha256(JSON.stringify(value.targets));
function requestSnapshot(request: Request): ArchiveSnapshot {
  const text = request.postData();
  requireSafe(text !== null && Buffer.byteLength(text, "utf8") <= 65_536);
  const raw: unknown = JSON.parse(text);
  requireSafe(record(raw) && keys(raw, ["before", "reason", "targets", "idempotencyKey", "actorPassword", "totpCode"]));
  requireSafe(typeof raw.actorPassword === "string" && raw.actorPassword.length > 0 &&
    typeof raw.totpCode === "string" && /^\d{6}$/.test(raw.totpCode));
  return archiveSnapshot({ before: raw.before, reason: raw.reason, targets: raw.targets }, raw.idempotencyKey);
}
async function receiptJSON(response: APIResponse | Response, original: ArchiveSnapshot, replayed: boolean) {
  requireSafe(response.status() === 200);
  const headers = response.headers();
  requireSafe(/^application\/json(?:;|$)/i.test(headers["content-type"] ?? ""));
  const length = headers["content-length"];
  requireSafe(length === undefined || (/^\d+$/.test(length) && Number(length) <= 65_536));
  // Playwright's APIResponse exposes a complete body, not a streaming reader.
  // Check declared and actual size, fatal UTF-8 and closed JSON before retaining
  // any receipt. This is bounded acceptance, not a streaming allocation claim.
  const bytes = await response.body();
  requireSafe(bytes.length > 0 && bytes.length <= 65_536);
  const raw: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  requireSafe(record(raw) && keys(raw, ["receipt", "replayed"]) && raw.replayed === replayed && record(raw.receipt));
  const receipt = raw.receipt;
  requireSafe(keys(receipt, ["operationUUID", "action", "before", "reason", "targets", "occurredAt"]) &&
    uuid(receipt.operationUUID) && receipt.action === "archive" && receipt.before === original.before &&
    receipt.reason === original.reason && utc(receipt.occurredAt) && Array.isArray(receipt.targets));
  const expected = original.targets.map((target) => ({ ...target, archived: true, visibilityVersion: target.visibilityVersion + 1 }));
  requireSafe(receipt.targets.length === expected.length);
  const targets = receipt.targets.map((target: unknown, index: number) => {
    requireSafe(record(target) && keys(target, ["taskUUID", "archived", "visibilityVersion"]) &&
      target.taskUUID === expected[index].taskUUID && target.archived === true &&
      target.visibilityVersion === expected[index].visibilityVersion);
    return expected[index];
  });
  return { operationUUID: receipt.operationUUID, action: "archive" as const,
    before: original.before, reason: original.reason, targets, occurredAt: receipt.occurredAt };
}
async function pendingSnapshot(page: Page, owner: { ID: number; username: string }) {
  const text = await page.evaluate((key) => sessionStorage.getItem(key),
    `adtr.pending-task-maintenance:${owner.ID}:${encodeURIComponent(owner.username)}`);
  requireSafe(text !== null && text.length <= 65_536);
  const raw: unknown = JSON.parse(text);
  requireSafe(record(raw) && keys(raw, ["owner", "username", "key", "action", "input"]) &&
    raw.owner === owner.ID && raw.username === owner.username && raw.action === "archive");
  return archiveSnapshot(raw.input, raw.key);
}
function healthDetail(value: unknown, id: string, archived: boolean, visibilityVersion: number): TaskDetail {
  requireSafe(record(value) && record(value.task) && Array.isArray(value.events));
  const task = value.task;
  requireSafe(task.taskUUID === id && task.taskName === "infrastructure.health" && task.domainId === "platform" &&
    task.state === "succeeded" && task.attempt === 1 && task.archived === archived &&
    task.visibilityVersion === visibilityVersion && typeof task.terminalAt === "string" &&
    Number.isFinite(Date.parse(task.terminalAt)) && record(task.result) &&
    task.result.database === "ready" && task.result.queue === "ready");
  return value as unknown as TaskDetail;
}
function immutableTaskDigest(detail: TaskDetail) {
  return sha256(JSON.stringify({ ...detail.task, archived: false, visibilityVersion: 0 }));
}

async function submitBootstrapHealth(page: Page, context: BrowserContext, password: string,
  mfa: Authenticator, active: () => void): Promise<TaskDetail> {
  await navigateTo(page, "后台任务");
  await page.getByRole("button", { name: "提交健康检查", exact: true }).click();
  await fillActorProof(page, password, mfa);
  active();
  const responsePromise = page.waitForResponse((response) =>
    new URL(response.url()).pathname === "/api/tasks/submit" && response.request().method() === "POST", { timeout: 30_000 });
  // The catch is attached before the click; a failed click cannot leave an
  // unhandled rejection containing a proof-bearing transport call log.
  const responseResult = responsePromise.catch(() => undefined);
  let response: Response | undefined;
  try {
    await page.getByRole("button", { name: "确认提交健康检查", exact: true }).click();
    response = await responseResult;
  } finally {
    await responseResult;
  }
  requireSafe(response && response.status() === 200);
  const submitted: unknown = await response.json();
  requireSafe(record(submitted) && record(submitted.task) && uuid(submitted.task.taskUUID) && submitted.replayed === false);
  const id = submitted.task.taskUUID;
  await expect(page.locator('.task-state[data-state="succeeded"]')).toBeVisible({ timeout: 30_000 });
  return healthDetail(await readJSON(context, `/api/tasks/detail?taskUUID=${id}`), id, false, 0);
}

async function observeUnknownArchive(options: {
  page: Page; context: BrowserContext; owner: { ID: number; username: string };
  password: string; mfa: Authenticator; original: TaskDetail; additional: TaskDetail;
  scheduleID: string; schedule: unknown; scheduleContext: BrowserContext;
  active: () => void; cleanup: () => void; info: TestInfo;
}) {
  const { page, context, owner, password, mfa, original, additional, active, info } = options;
  const expectedTargets = [
    { taskUUID: original.task.taskUUID, visibilityVersion: 2 },
    { taskUUID: additional.task.taskUUID, visibilityVersion: 0 },
  ].sort((a, b) => a.taskUUID < b.taskUUID ? -1 : a.taskUUID > b.taskUUID ? 1 : 0);
  const origin = new URL(page.url()).origin;
  await navigateTo(page, "后台任务");
  await page.getByRole("button", { name: "任务归档", exact: true }).click();
  await page.getByLabel("归档截止时间（UTC）", { exact: true }).fill(new Date(Date.now() - 1000).toISOString());
  await page.getByRole("button", { name: "预览归档候选", exact: true }).click();
  for (const target of expectedTargets)
    await page.getByRole("checkbox", { name: `选择归档 ${target.taskUUID}`, exact: true }).check();
  await page.getByLabel("归档原因", { exact: true }).fill("Synthetic exact-target response-loss check");
  await page.getByRole("button", { name: "核对归档所选任务", exact: true }).click();
  const key = await page.getByLabel("维护操作幂等键", { exact: true }).inputValue();
  const frozen = archiveSnapshot(JSON.parse(await page.getByLabel("待确认的维护内容", { exact: true }).innerText()), key);
  requireSafe(JSON.stringify(frozen.targets) === JSON.stringify(expectedTargets));
  const originalIntentSHA256 = intentDigest(frozen), originalTargetsSHA256 = targetsDigest(frozen);
  let forwarded = 0, matchedRequests = 0, routeFailed = false;
  const routeJobs: Promise<void>[] = [];
  let committed: Awaited<ReturnType<typeof receiptJSON>> | undefined;
  let capturedRequest: Request | undefined;
  const matches = (url: URL) => url.origin === origin && url.pathname === "/api/tasks/archive" && url.search === "";
  const handler = (route: Route) => {
    if (route.request().method() !== "POST") return route.continue();
    const routeWork = (async () => {
      let upstream: APIResponse | undefined;
      try {
        active();
        requireSafe(++matchedRequests === 1);
        capturedRequest = route.request();
        requireSafe(capturedRequest.frame().page() === page && matches(new URL(capturedRequest.url())));
        const actual = requestSnapshot(capturedRequest);
        requireSafe(intentDigest(actual) === originalIntentSHA256 && targetsDigest(actual) === originalTargetsSHA256);
        // The original request is forwarded once, with no body/header/path/key
        // substitution. No fulfill, fake receipt, cache change or write replay.
        forwarded++;
        upstream = await route.fetch({ maxRetries: 0, maxRedirects: 0, timeout: 30_000 });
        committed = await receiptJSON(upstream, frozen, false);
        await route.abort("failed"); // Only browser delivery is deliberately lost.
      } catch {
        routeFailed = true; // Unknown commit is failure, never permission to replay.
        await route.abort("failed").catch(() => { routeFailed = true; });
      } finally {
        if (upstream) await upstream.dispose().catch(() => { routeFailed = true; options.cleanup(); });
      }
    })();
    routeJobs.push(routeWork);
    return routeWork;
  };
  await page.route(matches, handler);
  let originalRequest: Promise<Request | undefined> | undefined;
  try {
    await fillActorProof(page, password, mfa);
    active();
    originalRequest = page.waitForRequest((request) =>
      request.method() === "POST" && matches(new URL(request.url())), { timeout: 30_000 }).catch(() => undefined);
    await page.getByRole("button", { name: "确认归档所选任务", exact: true }).click();
    const browserRequest = await originalRequest;
    await expect.poll(() => matchedRequests).toBe(1);
    requireSafe(browserRequest && browserRequest === capturedRequest);
    requireSafe(routeJobs.length === 1);
    await Promise.allSettled(routeJobs);
    requireSafe(!routeFailed && matchedRequests === 1 && forwarded === 1 && committed);
    await expect(page.getByRole("button", { name: "使用原幂等键核对维护操作", exact: true })).toBeEnabled();
  } finally {
    // Settle the owned handler before removing it. Its fetch is bounded; an
    // already submitted mutation cannot be rolled back by navigation/cleanup.
    if (originalRequest) await originalRequest;
    await Promise.allSettled(routeJobs);
    try { await page.unroute(matches, handler); } catch {
      options.cleanup();
      throw new Error("Maintenance fault handler cleanup failed");
    }
  }
  requireSafe(committed && !routeFailed);
  const committedReceipt = committed;
  requireSafe(intentDigest(await pendingSnapshot(page, owner)) === originalIntentSHA256);
  await page.reload();
  await navigateTo(page, "后台任务");
  if (!(await page.getByLabel("维护操作幂等键", { exact: true }).isVisible()))
    await page.getByRole("button", { name: "继续核对维护操作", exact: true }).click();
  const section = page.getByRole("region", { name: "归档目标逐项核对", exact: true });
  const selector = page.getByLabel("选择待核对目标", { exact: true });
  const readButton = page.getByRole("button", { name: "读取所选目标当前状态", exact: true });
  const result = page.getByLabel("归档目标当前状态", { exact: true });
  const warning = "这里只核对该目标当前状态，不确认原操作是否提交。确认原操作仍须使用原幂等键、相同内容和新的验证码。";
  await expect(selector).toBeVisible();
  await expect(result).toHaveCount(0);
  await expect(section.getByText(warning, { exact: true })).toBeVisible();
  requireSafe(intentDigest(await pendingSnapshot(page, owner)) === originalIntentSHA256);
  requireSafe(await page.getByLabel("维护操作幂等键", { exact: true }).inputValue() === key);
  const emptyProof = async () => {
    requireSafe(await page.getByLabel("操作者当前密码", { exact: true }).inputValue() === "");
    requireSafe(await page.getByLabel("未使用的认证器验证码", { exact: true }).inputValue() === "");
  };
  await emptyProof();
  const observationRequests: string[] = [];
  let selectedID = "";
  const onObservationRequest = (request: Request) => {
    const url = new URL(request.url());
    if (url.origin !== origin || !url.pathname.startsWith("/api/")) return;
    if (request.method() !== "GET") { observationRequests.push("unexpected_method"); return; }
    if (url.pathname === "/api/auth/me" && url.search === "") observationRequests.push("identity");
    else if (url.pathname === "/api/tasks/detail" && url.searchParams.size === 2 &&
      url.searchParams.get("taskUUID") === selectedID && url.searchParams.get("visibility") === "all")
      observationRequests.push("detail");
    else observationRequests.push("unexpected_read");
  };
  page.on("request", onObservationRequest);
  const screenshots: { file: string; bytes: number; sha256: string; width: number; height: number;
    viewport: { width: number; height: number } }[] = [];
  try {
    for (const target of expectedTargets) {
      const start = observationRequests.length;
      selectedID = target.taskUUID;
      await selector.selectOption(selectedID);
      await expect(result).toHaveCount(0);
      requireSafe(observationRequests.length === start); // Selection is not a read.
      active();
      await readButton.click();
      await expect(result).toBeVisible();
      const field = (name: string) => result.locator("dt").filter({ hasText: new RegExp(`^${name}$`) }).locator("+ dd");
      requireSafe(await field("目标 UUID").innerText() === selectedID);
      requireSafe(await field("原预期可见性版本").innerText() === String(target.visibilityVersion));
      requireSafe(await field("当前可见性").innerText() === "已归档");
      requireSafe(await field("当前可见性版本").innerText() === String(target.visibilityVersion + 1));
      const observedAt = await result.locator("dt").filter({ hasText: "浏览器观察完成时间（UTC）" }).locator("+ dd").innerText();
      requireSafe(Number.isFinite(Date.parse(observedAt)));
      requireSafe(JSON.stringify(observationRequests.slice(start)) === JSON.stringify(["identity", "detail", "identity"]));
      requireSafe(intentDigest(await pendingSnapshot(page, owner)) === originalIntentSHA256);
      await expect(section.getByText(warning, { exact: true })).toBeVisible();
    }
    // Safe locator-only pixels contain observation controls/results, not proof
    // fields. Viewport and actual PNG size are recorded separately, not assumed.
    for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport);
      await emptyProof();
      await expect(result).toBeVisible();
      const file = `archive-target-observation-${viewport.width}.png`;
      const png = await section.screenshot({ path: info.outputPath(file) });
      requireSafe(png.length > 32 && png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])));
      requireSafe((await readFile(info.outputPath(file))).equals(png));
      screenshots.push({ file, bytes: png.length, sha256: sha256(png), width: png.readUInt32BE(16),
        height: png.readUInt32BE(20), viewport });
    }
    requireSafe(JSON.stringify(observationRequests) === JSON.stringify(["identity", "detail", "identity", "identity", "detail", "identity"]));
  } finally {
    page.off("request", onObservationRequest);
  }
  requireSafe(intentDigest(await pendingSnapshot(page, owner)) === originalIntentSHA256);
  await fillActorProof(page, password, mfa);
  active();
  const replayResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && matches(new URL(response.url())), { timeout: 30_000 }).catch(() => undefined);
  let replay: Response | undefined;
  try {
    await page.getByRole("button", { name: "使用原幂等键核对维护操作", exact: true }).click();
    replay = await replayResponse;
  } finally {
    await replayResponse;
  }
  requireSafe(replay);
  const replayInput = requestSnapshot(replay.request());
  requireSafe(intentDigest(replayInput) === originalIntentSHA256 && targetsDigest(replayInput) === originalTargetsSHA256);
  const replayReceipt = await receiptJSON(replay, frozen, true);
  requireSafe(JSON.stringify(replayReceipt) === JSON.stringify(committedReceipt));
  await expect.poll(() => page.evaluate((storageKey) => sessionStorage.getItem(storageKey) === null,
    `adtr.pending-task-maintenance:${owner.ID}:${encodeURIComponent(owner.username)}`)).toBe(true);
  const details = [];
  for (const initial of [original, additional]) {
    const currentVersion = initial.task.visibilityVersion + 1;
    const current = healthDetail(await readJSON(context,
      `/api/tasks/detail?taskUUID=${initial.task.taskUUID}&visibility=all`), initial.task.taskUUID, true, currentVersion);
    requireSafe(immutableTaskDigest(current) === immutableTaskDigest(initial));
    requireSafe(sha256(JSON.stringify(current.events)) === sha256(JSON.stringify(initial.events)));
    details.push({ taskUUID: current.task.taskUUID, visibilityVersion: currentVersion,
      immutableTaskSHA256: immutableTaskDigest(current), eventsSHA256: sha256(JSON.stringify(current.events)) });
  }
  const schedule = await readJSON(options.scheduleContext,
    `/api/tasks/schedules/detail?scheduleUUID=${options.scheduleID}&pageIdx=1&pageSize=20`);
  requireSafe(sha256(JSON.stringify(schedule)) === sha256(JSON.stringify(options.schedule)));
  const sourceSHA256: Record<string, string> = {};
  for (const path of ["web/src/TaskMaintenance.tsx", "web/src/Maintenance.test.tsx",
    "web/e2e/maintenance.spec.ts", "docs/task-archive-contract.md"])
    sourceSHA256[path] = sha256(await readFile(resolve(dirname(info.file), "../..", path)));
  const evidence = {
    schemaVersion: 1, case: "archive_exact_targets_after_lost_response",
    status: "browser_passed_cleanup_not_attested", originalTargetCount: frozen.targets.length,
    originalIntentSHA256, originalTargetsSHA256, operationUUID: committedReceipt.operationUUID,
    targets: committedReceipt.targets, details, scheduleSHA256: sha256(JSON.stringify(schedule)),
    requestCounts: { archiveForward: forwarded,
      observationIdentity: observationRequests.filter((kind) => kind === "identity").length,
      observationDetail: observationRequests.filter((kind) => kind === "detail").length },
    screenshots, sourceSHA256,
  };
  requireSafe(evidence.originalTargetCount === 2 && screenshots.length === 2 &&
    [...Object.values(sourceSHA256), originalIntentSHA256, originalTargetsSHA256, evidence.scheduleSHA256]
      .every((hash) => /^[0-9a-f]{64}$/.test(hash)) && screenshots.every((s) => s.width > 0 && s.height > 0));
  return evidence;
}

// Fresh synthetic database, real API, scheduler and worker; natural UTC time.
// The original schedule/archive/restore chain is unintercepted. The added
// exact-target case loses one browser response only after verifying a real
// forwarded commit. No mocked executor, fake clock or direct SQL fixtures.
test("real scheduled health occurrence and reversible visibility through browser, API, Worker and PostgreSQL", async ({
  page: bootstrapPage,
  context: bootstrapContext,
  producerContext: context,
}, info) => {
  test.setTimeout(480_000);
  type Phase = "original_setup" | "producer_schedule" | "bootstrap_health" |
    "producer_visibility" | "reader_setup" | "reader_checks" | "archive_observation" | "evidence";
  let phase: Phase = "original_setup", failedPhase: Phase | undefined;
  let stopped = false, cleanupFailed = false;
  let readerContext: BrowserContext | undefined;
  const branches: Promise<unknown>[] = [];
  const fail = (where: Phase) => { failedPhase ??= where; stopped = true; };
  const active = () => { if (stopped) throw new Error("Maintenance branch stopped"); };
  const branch = <T,>(where: Phase, work: () => Promise<T>): Promise<T | undefined> => {
    const settled = Promise.resolve().then(work).catch(() => { fail(where); return undefined; });
    branches.push(settled);
    return settled;
  };
  const taskWrites = { healthSubmit: 0, archive: 0, restore: 0, unexpected: 0 };
  const countTaskWrite = (request: Request) => {
    if (request.method() !== "POST") return;
    const path = new URL(request.url()).pathname;
    if (path === "/api/tasks/submit") taskWrites.healthSubmit++;
    else if (path === "/api/tasks/archive") taskWrites.archive++;
    else if (path === "/api/tasks/restore") taskWrites.restore++;
    else if (path.startsWith("/api/tasks/")) taskWrites.unexpected++;
  };
  bootstrapPage.on("request", countTaskWrite);
  try {
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "Maintenance E2E requires a fresh disposable synthetic bootstrap account",
    );
  const bootstrapChanged = "Synthetic Maintenance Password 123";
  await bootstrapPage.goto("/");
  await login(bootstrapPage, username, password);
  await changeInitialPassword(bootstrapPage, password, bootstrapChanged);
  const bootstrapMfa = await enrollMfa(bootstrapPage, bootstrapChanged);
  const bootstrapMe = await readJSON(bootstrapContext, "/api/auth/me");

  // Ten sensitive actions are allowed per user in fifteen minutes. Bootstrap:
  // password/enroll/confirm (1-3), producer creation (4), health submit (5),
  // reader role/user (6-7), batch archive/replay (8-9). One action stays unused.
  // Producer: password/enroll/confirm (1-3), create/enable/pause (4-6), enable
  // replay (7), archive/restore (8-9), create replay (10). Replays also count.
  // A new context only isolates cookies; the distinct UI-created user is what
  // supplies a separate budget. Producer owns the unchanged original chain;
  // bootstrap owns the additional health task and two-target archive/replay.
  const producerName = "synthetic.maintenance.producer";
  const producerInitial = "Initial Maintenance Producer 123";
  const changed = "Changed Maintenance Producer 456";
  await navigateTo(bootstrapPage, "访问管理");
  await bootstrapPage
    .getByRole("button", { name: "用户管理", exact: true })
    .click();
  await bootstrapPage
    .getByRole("button", { name: "新增用户", exact: true })
    .click();
  await bootstrapPage.getByLabel("用户名", { exact: true }).fill(producerName);
  await fillSecret(
    bootstrapPage.getByLabel("初始密码", { exact: true }),
    producerInitial,
  );
  await fillSecret(
    bootstrapPage.getByLabel("确认初始密码", { exact: true }),
    producerInitial,
  );
  await bootstrapPage
    .getByLabel("角色", { exact: true })
    .fill("platform_admin");
  await fillActorProof(bootstrapPage, bootstrapChanged, bootstrapMfa);
  const producer = await submitMutation(
    bootstrapPage,
    "/api/access/users/create",
    "创建用户",
  );
  expect(producer.ID).toBeGreaterThan(0);
  expect(producer.ID).not.toBe(bootstrapMe.ID);
  const page = await context.newPage();
  await page.goto("/");
  await login(page, producerName, producerInitial);
  await changeInitialPassword(page, producerInitial, changed);
  const mfa = await enrollMfa(page, changed);
  const producerMe = await readJSON(context, "/api/auth/me");
  expect(producerMe.ID).toBe(producer.ID);
  expect(producerMe.hasMfa).toBe(true);
  const write = async (path: string, button: string) => {
    active();
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: button, exact: true }).click(),
    ]);
    expect(r.status(), "Maintenance mutation status").toBe(200);
    return r.json();
  };
  const fillProof = async () => { await fillActorProof(page, changed, mfa); active(); };
  phase = "producer_schedule";
  await navigateTo(page, "后台任务");
  await page.getByRole("button", { name: "周期计划", exact: true }).click();
  await expect(page.getByRole("table", { name: "周期计划列表" })).toBeVisible();
  expect(
    (await readJSON(context, "/api/tasks/schedules?pageIdx=1&pageSize=20"))
      .schedules,
  ).toEqual([]);
  await page.getByRole("button", { name: "新建周期计划", exact: true }).click();
  await page
    .getByLabel("计划名称", { exact: true })
    .fill("Synthetic periodic health");
  const createCode = await freshCode(mfa),
    createdNotBefore = Date.now(),
    startAt = new Date(Math.ceil((Date.now() + 75_000) / 1000) * 1000)
      .toISOString()
      .replace(".000Z", "Z");
  await page.getByLabel("首次计划时间（UTC）", { exact: true }).fill(startAt);
  await page.getByLabel("执行间隔（秒）", { exact: true }).fill("60");
  await page.getByRole("button", { name: "核对暂停计划", exact: true }).click();
  const createKey = await page
    .getByLabel("维护操作幂等键", { exact: true })
    .inputValue();
  await fillSecret(page.getByLabel("操作者当前密码", { exact: true }), changed);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    createCode,
  );
  const created = await write(
    "/api/tasks/schedules/create",
    "确认创建暂停计划",
  );
  expect(created).toMatchObject({
    replayed: false,
    schedule: {
      label: "Synthetic periodic health",
      state: "paused",
      startAt,
      intervalSeconds: 60,
      taskName: "infrastructure.health",
      domainId: "platform",
      payloadVersion: 1,
    },
  });
  const sid = created.schedule.scheduleUUID;
  expect(sid).toMatch(/^[0-9a-f-]{36}$/);
  await expect(page.getByRole("table", { name: "计划发生记录" })).toBeVisible();
  expect(
    (await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")).tasks,
  ).toEqual([]);
  await page.getByRole("button", { name: "启用计划", exact: true }).click();
  const enableKey = await page
    .getByLabel("维护操作幂等键", { exact: true })
    .inputValue();
  await fillProof();
  const enabled = await write("/api/tasks/schedules/enable", "确认启用计划");
  expect(enabled).toMatchObject({
    replayed: false,
    schedule: { scheduleUUID: sid, state: "enabled" },
  });
  expect(enabled.schedule.controlVersion).toBeGreaterThan(
    created.schedule.controlVersion,
  );
  // Start only after the original empty-task assertion and successful enable.
  // This real standalone health task shares no schedule occurrence. Its own
  // actor/page/TOTP can progress during the producer's natural due-time wait.
  const bootstrapHealth = branch("bootstrap_health", () =>
    submitBootstrapHealth(bootstrapPage, bootstrapContext, bootstrapChanged, bootstrapMfa, active));
  await expect(
    page.getByRole("button", { name: "查看最近实际任务", exact: true }),
  ).toBeVisible({ timeout: 100_000 });
  expect(Date.now() - createdNotBefore).toBeGreaterThanOrEqual(60_000);
  const due = await readJSON(
    context,
    `/api/tasks/schedules/detail?scheduleUUID=${sid}&pageIdx=1&pageSize=20`,
  );
  const tid = due.schedule.lastTaskUUID;
  expect(due.schedule.controlVersion).toBe(enabled.schedule.controlVersion);
  expect(due.events).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        action: "admitted",
        taskUUID: tid,
        firstAt: startAt,
        lastAt: startAt,
        count: 1,
      }),
    ]),
  );
  await page
    .getByRole("button", { name: "查看最近实际任务", exact: true })
    .click();
  await expect(page.locator('.task-state[data-state="succeeded"]')).toBeVisible(
    { timeout: 30_000 },
  );
  await expect(page.getByLabel("任务实际结果")).toContainText(
    '"database": "ready"',
  );
  await expect(page.getByLabel("任务实际结果")).toContainText(
    '"queue": "ready"',
  );
  const original = await readJSON(context, `/api/tasks/detail?taskUUID=${tid}`);
  expect(original.task).toMatchObject({
    taskUUID: tid,
    state: "succeeded",
    archived: false,
    visibilityVersion: 0,
    attempt: 1,
  });
  expect(original.task.terminalAt).toBeTruthy();
  await page.getByRole("button", { name: "周期计划", exact: true }).click();
  await page
    .getByRole("button", { name: `计划详情 ${sid}`, exact: true })
    .click();
  await page.getByRole("button", { name: "暂停计划", exact: true }).click();
  await fillProof();
  const paused = await write("/api/tasks/schedules/pause", "确认暂停计划");
  expect(paused.schedule.state).toBe("paused");
  const me = await readJSON(context, "/api/auth/me"),
    headers = {
      Origin: new URL(page.url()).origin,
      "X-CSRF-Token": me.csrfToken,
    };
  expect(me.ID).toBe(producer.ID);
  active();
  const replayCode = await freshCode(mfa);
  active();
  const replay = await context.request.post("/api/tasks/schedules/enable", {
    headers,
    data: {
      scheduleUUID: sid,
      expectedControlVersion: created.schedule.controlVersion,
      idempotencyKey: enableKey,
      actorPassword: changed,
      totpCode: replayCode,
    },
  });
  expect(replay.status()).toBe(200);
  expect(await replay.json()).toMatchObject({
    replayed: true,
    receipt: { state: "enabled" },
    schedule: {
      state: "paused",
      controlVersion: paused.schedule.controlVersion,
    },
  });
  phase = "producer_visibility";
  await page.getByRole("button", { name: "任务归档", exact: true }).click();
  await page
    .getByLabel("归档截止时间（UTC）", { exact: true })
    .fill(new Date(Date.now() - 1000).toISOString());
  await page.getByRole("button", { name: "预览归档候选", exact: true }).click();
  await page
    .getByRole("checkbox", { name: `选择归档 ${tid}`, exact: true })
    .check();
  await page
    .getByLabel("归档原因", { exact: true })
    .fill("Synthetic reversible retention check");
  await page
    .getByRole("button", { name: "核对归档所选任务", exact: true })
    .click();
  await fillProof();
  const archived = await write("/api/tasks/archive", "确认归档所选任务");
  expect(archived).toMatchObject({
    replayed: false,
    receipt: {
      action: "archive",
      targets: [{ taskUUID: tid, archived: true, visibilityVersion: 1 }],
    },
  });
  await expect(
    page.getByText(
      "任务已归档，原执行结果和证据仍保留。请先恢复可见性，再进行允许的失败恢复。",
      { exact: true },
    ),
  ).toBeVisible();
  const activeList = await readJSON(context, "/api/tasks?pageIdx=1&pageSize=100");
  expect(
    activeList.tasks.map((t: { taskUUID: string }) => t.taskUUID),
  ).not.toContain(tid);
  expect(
    (await context.request.get(`/api/tasks/detail?taskUUID=${tid}`)).status(),
  ).toBe(404);
  const retained = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${tid}&visibility=archived`,
  );
  expect(retained.task).toMatchObject({
    ...original.task,
    archived: true,
    visibilityVersion: 1,
  });
  expect(retained.events).toEqual(original.events);
  await page
    .getByLabel("恢复可见性原因", { exact: true })
    .fill("Synthetic restore check");
  await page
    .getByRole("button", { name: "核对恢复可见性", exact: true })
    .click();
  await fillProof();
  const restored = await write("/api/tasks/restore", "确认恢复任务可见性");
  expect(restored).toMatchObject({
    replayed: false,
    receipt: {
      action: "restore",
      targets: [{ taskUUID: tid, archived: false, visibilityVersion: 2 }],
    },
  });
  await expect(page.getByText("默认可见", { exact: true })).toBeVisible();
  const visible = await readJSON(context, `/api/tasks/detail?taskUUID=${tid}`);
  expect(visible.task).toMatchObject({
    ...original.task,
    archived: false,
    visibilityVersion: 2,
  });
  expect(visible.events).toEqual(original.events);
  const history = await readJSON(
    context,
    `/api/tasks/schedules/detail?scheduleUUID=${sid}&pageIdx=1&pageSize=20`,
  );
  expect(history.schedule.state).toBe("paused");
  expect(history.schedule.lastTaskUUID).toBe(paused.schedule.lastTaskUUID);
  // The real fresh-TOTP waits crossed a later fixed-grid due time. A paused
  // scheduler must preserve its last task and admit no event after the pause.
  expect(Date.now()).toBeGreaterThan(Date.parse(startAt) + 60_000);
  expect(
    history.events.filter(
      (event: { action: string; createdAt: string }) =>
        event.action === "admitted" &&
        Date.parse(event.createdAt) > Date.parse(paused.receipt.createdAt),
    ),
  ).toEqual([]);
  expect(history.events).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ action: "admitted", taskUUID: tid }),
    ]),
  );
  active();
  const replayCreateCode = await freshCode(mfa);
  active();
  const replayCreate = await context.request.post(
    "/api/tasks/schedules/create",
    {
      headers,
      data: {
        label: "Synthetic periodic health",
        taskName: "infrastructure.health",
        domainId: "platform",
        payloadVersion: 1,
        payload: {},
        startAt,
        intervalSeconds: 60,
        idempotencyKey: createKey,
        actorPassword: changed,
        totpCode: replayCreateCode,
      },
    },
  );
  expect(replayCreate.status()).toBe(200);
  expect(await replayCreate.json()).toMatchObject({
    replayed: true,
    schedule: { scheduleUUID: sid, state: "paused" },
  });
  expect(
    (await readJSON(context, "/api/tasks/schedules?pageIdx=1&pageSize=20")).page
      .total,
  ).toBe(1);
  const additional = await bootstrapHealth;
  active();
  requireSafe(additional);
  phase = "reader_setup";
  await navigateTo(bootstrapPage, "访问管理");
  await bootstrapPage
    .getByRole("button", { name: "角色管理", exact: true })
    .click();
  await bootstrapPage
    .getByRole("button", { name: "新增角色", exact: true })
    .click();
  await bootstrapPage
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic Maintenance Reader");
  for (const label of ["后台任务：读取", "周期计划：读取", "任务归档：读取"])
    await bootstrapPage
      .getByRole("checkbox", { name: label, exact: true })
      .check();
  await fillActorProof(bootstrapPage, bootstrapChanged, bootstrapMfa);
  const savedRole = await submitMutation(
    bootstrapPage,
    "/api/access/roles/save",
    "保存角色",
  );
  const role = await readJSON(
    bootstrapContext,
    `/api/access/roles/detail?roleID=${encodeURIComponent(savedRole.roleID)}`,
  );
  for (const mark of ["tasks", "schedules", "task_archive"])
    expect(
      role.permissions.find((p: Permission) => p.mark === mark),
    ).toMatchObject({ auth: { readable: true, writeable: false } });
  await bootstrapPage
    .getByRole("button", { name: "用户管理", exact: true })
    .click();
  await bootstrapPage
    .getByRole("button", { name: "新增用户", exact: true })
    .click();
  const readerName = "synthetic.maintenance.reader",
    readerInitial = "Initial Maintenance Reader 123",
    readerChanged = "Changed Maintenance Reader 456";
  await bootstrapPage.getByLabel("用户名", { exact: true }).fill(readerName);
  await fillSecret(
    bootstrapPage.getByLabel("初始密码", { exact: true }),
    readerInitial,
  );
  await fillSecret(
    bootstrapPage.getByLabel("确认初始密码", { exact: true }),
    readerInitial,
  );
  await bootstrapPage
    .getByLabel("角色", { exact: true })
    .fill(savedRole.roleID);
  await fillActorProof(bootstrapPage, bootstrapChanged, bootstrapMfa);
  await submitMutation(bootstrapPage, "/api/access/users/create", "创建用户");
  readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  const archiveEvidence = branch("archive_observation", () => observeUnknownArchive({
    page: bootstrapPage, context: bootstrapContext, owner: bootstrapMe,
    password: bootstrapChanged, mfa: bootstrapMfa,
    original: healthDetail(visible, tid, false, 2), additional,
    scheduleID: sid, schedule: history, scheduleContext: context,
    active, cleanup: () => { cleanupFailed = true; }, info,
  }));
  phase = "reader_checks";
    const reader = await readerContext.newPage();
    await reader.goto("/");
    await login(reader, readerName, readerInitial, active);
    await changeInitialPassword(reader, readerInitial, readerChanged, active);
    const readerMfa = await enrollMfa(reader, readerChanged, active);
    await navigateTo(reader, "后台任务");
    await expect(reader.getByLabel("任务可见性筛选")).toHaveCount(0);
    await reader.getByRole("button", { name: "周期计划", exact: true }).click();
    await expect(
      reader.getByText("没有符合条件的周期计划。", { exact: true }),
    ).toBeVisible();
    await expect(
      reader.getByRole("button", { name: "新建周期计划", exact: true }),
    ).toHaveCount(0);
    expect(
      (
        await readerContext.request.get(
          `/api/tasks/schedules/detail?scheduleUUID=${sid}`,
        )
      ).status(),
    ).toBe(404);
    const readerMe = await readJSON(readerContext, "/api/auth/me"),
      readerHeaders = {
        Origin: new URL(reader.url()).origin,
        "X-CSRF-Token": readerMe.csrfToken,
      };
    const deniedCode = await freshCode(readerMfa);
    active();
    const denied = await readerContext.request.post(
      "/api/tasks/schedules/enable",
      {
        headers: readerHeaders,
        data: {
          scheduleUUID: sid,
          expectedControlVersion: paused.schedule.controlVersion,
          idempotencyKey: "synthetic-reader-denied",
          actorPassword: readerChanged,
          totpCode: deniedCode,
        },
      },
    );
    expect(denied.status()).toBe(403);
    expect(await denied.json()).toEqual({ error: "forbidden" });
    expect(
      (
        await readerContext.request.get("/api/tasks?visibility=archived")
      ).status(),
    ).toBe(403);
    active();
    await navigateTo(reader, "退出登录");
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/tasks/schedules")).status(),
    ).toBe(401);
  const evidence = await archiveEvidence;
  active();
  requireSafe(evidence);
  phase = "evidence";
  requireSafe(taskWrites.healthSubmit === 1 && taskWrites.archive === 2 && taskWrites.restore === 0 && taskWrites.unexpected === 0);
  await writeFile(info.outputPath("archive-target-observation.json"), JSON.stringify({
    ...evidence, requestCounts: { ...evidence.requestCounts, healthSubmit: taskWrites.healthSubmit,
      archiveBrowserRequests: taskWrites.archive, archiveReplay: taskWrites.archive - 1, restore: taskWrites.restore },
  }, null, 2) + "\n");
  } catch {
    // Closed phase only. Never attach raw Playwright errors or their cause:
    // authenticated transport failures may embed cookies or submitted proofs.
    fail(phase);
  } finally {
    stopped = true;
    // No early-rejecting Promise.all owns cleanup: every started branch settles
    // before page/context disposal, including its exact one-use route handler.
    await Promise.allSettled(branches);
    bootstrapPage.off("request", countTaskWrite);
    if (readerContext) {
      try { await readerContext.close(); } catch { cleanupFailed = true; }
    }
  }
  if (failedPhase || cleanupFailed)
    throw new Error(`Maintenance verification failed: phase=${failedPhase ?? "none"}; cleanup=${cleanupFailed ? "failed" : "settled"}`);
});
