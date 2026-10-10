import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Page,
  type TestInfo,
} from "@playwright/test";
import { createHash, createHmac } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { TaskDetail, TaskList } from "../src/task-api";

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

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(initial);
  await page.getByLabel("新密码", { exact: true }).fill(changed);
  await page.getByLabel("确认新密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await navigateTo(page, "多因素认证");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, now));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
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

type ReadEvidence = {
  step: string;
  path: string;
  status: number;
};
type VisualEvidence = {
  file: string;
  sha256: string;
  viewport: { width: number; height: number };
  fullPage: boolean;
};
const filteredQuery = {
  pageIdx: "1",
  pageSize: "10",
  taskName: "infrastructure.health",
  domainId: "platform",
  state: "succeeded",
};

async function browserRead<T>(
  page: Page,
  path: string,
  query: Record<string, string>,
  step: string,
  action: () => Promise<unknown>,
  reads: ReadEvidence[],
): Promise<T> {
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => {
      const url = new URL(candidate.url());
      return (
        candidate.request().method() === "GET" &&
        url.pathname === path &&
        Object.entries(query).every(
          ([key, value]) => url.searchParams.get(key) === value,
        )
      );
    }),
    action(),
  ]);
  // Page response observation proves that restoration performs a browser fetch;
  // a context.request check alone could hide stale UI or restored cached data.
  expect(response.request().resourceType()).toBe("fetch");
  expect(response.fromServiceWorker()).toBe(false);
  expect(response.status(), `${step}: GET ${path}`).toBe(200);
  const value = (await response.json()) as T;
  const url = new URL(response.url());
  expect([...url.searchParams.entries()].sort()).toEqual(
    Object.entries(query).sort(),
  );
  reads.push({
    step,
    path: url.pathname + url.search,
    status: response.status(),
  });
  return value;
}

async function expectFilteredQueue(page: Page, id: string, value: TaskList) {
  expect(value.page).toEqual({
    pageIdx: 1,
    pageSize: 10,
    total: 1,
    totalPage: 1,
  });
  expect(value.exhausted).toBe(true);
  expect(value.tasks.map((task) => task.taskUUID)).toEqual([id]);
  const queue = page.getByRole("table", { name: "任务列表", exact: true });
  await expect(queue).toBeVisible();
  await expect(queue.locator("tbody tr")).toHaveCount(1);
  await expect(
    queue.getByRole("rowheader").filter({ hasText: id }),
  ).toBeVisible();
  await expect(page.getByLabel("任务种类筛选")).toHaveValue(
    filteredQuery.taskName,
  );
  await expect(page.getByLabel("域 ID 筛选")).toHaveValue(
    filteredQuery.domainId,
  );
  await expect(page.getByLabel("任务状态筛选")).toHaveValue(
    filteredQuery.state,
  );
  await expect(page.getByLabel("每页任务数")).toHaveValue(
    filteredQuery.pageSize,
  );
  // The real fixture has one task. Exercise its genuine page-size boundary
  // without fabricating rows or claiming that an unavailable second page ran.
  await expect(
    page.getByRole("button", { name: "上一页", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
}

async function expectPersistedDetail(
  page: Page,
  id: string,
  value: TaskDetail,
) {
  expect(value.task).toMatchObject({
    taskUUID: id,
    state: "succeeded",
    sourceState: "SUCCESS",
    attempt: 1,
    progress: 100,
    result: { database: "ready", queue: "ready" },
  });
  const detail = page.getByRole("region", {
    name: "任务执行详情",
    exact: true,
  });
  await expect(detail).toBeVisible();
  await expect(
    detail.getByRole("heading", { name: "任务详情", exact: true }),
  ).toBeVisible();
  await expect(detail.getByTestId("task-detail-identity")).toHaveText(id);
  await expect(detail.getByLabel("任务持久化进度")).toHaveJSProperty(
    "value",
    100,
  );
  await expect(detail.getByLabel("任务实际结果")).toContainText(
    '"database": "ready"',
  );
  expect(
    JSON.parse(await detail.getByLabel("任务实际结果").innerText()),
  ).toEqual(value.task.result);
  const events = detail.getByRole("list", {
    name: "持久化任务事件",
    exact: true,
  });
  await expect(events).toBeVisible();
  expect(await events.evaluate((element) => element.tagName)).toBe("OL");
  const items = events.getByRole("listitem");
  await expect(items).toHaveCount(value.events.length);
  expect(value.events.map((event) => event.action)).toEqual(
    expect.arrayContaining([
      "submitted",
      "leased",
      "started",
      "progress",
      "finished",
    ]),
  );
  for (const [index, event] of value.events.entries()) {
    await expect(items.nth(index)).toContainText(event.action);
    await expect(items.nth(index)).toContainText(event.state);
    await expect(items.nth(index)).toContainText(event.createdAt);
    if (index > 0) expect(event.id).toBeGreaterThan(value.events[index - 1].id);
  }
}

async function captureTaskView(
  page: Page,
  testInfo: TestInfo,
  name: string,
  visuals: VisualEvidence[],
) {
  const viewport = page.viewportSize();
  expect(viewport).not.toBeNull();
  expect(await page.evaluate(() => scrollY)).toBe(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  // Explicit captures are limited to safe synthetic queue/detail screens.
  await expect(page.getByLabel("操作者当前密码", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
  ).toHaveCount(0);
  for (const fullPage of [false, true]) {
    const file = `tasks-soc-${name}-${fullPage ? "full-page" : "first-screen"}.png`;
    const bytes = await page.screenshot({
      path: testInfo.outputPath(file),
      fullPage,
    });
    visuals.push({
      file,
      sha256: createHash("sha256").update(bytes).digest("hex"),
      viewport: viewport!,
      fullPage,
    });
  }
}

async function expectFirstTaskInViewport(page: Page, id: string) {
  expect(await page.evaluate(() => scrollY)).toBe(0);
  const queue = page.getByRole("table", { name: "任务列表", exact: true });
  const firstTask =
    page.viewportSize()!.width > 600
      ? queue.locator("tbody tr").first()
      : queue.getByRole("rowheader").filter({ hasText: id });
  await expect(firstTask).toContainText(id);
  const bounds = await firstTask.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(
    page.viewportSize()!.width,
  );
  expect(bounds!.y).toBeGreaterThanOrEqual(0);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(
    page.viewportSize()!.height,
  );
}

// Automatic failure artifacts must never capture passwords or enrollment codes.
// Only the explicit, proof-free queue and detail screenshots below are retained.
test.use({ trace: "off", screenshot: "off", video: "off" });

// This suite requires its own fresh database plus a real API and Worker. No
// route interception, fake clock, test executor, or direct SQL success fixture.
test("real browser → API → Worker → PostgreSQL task result, idempotency and role boundary", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(300_000);
  const reads: ReadEvidence[] = [];
  const visuals: VisualEvidence[] = [];
  let roleBoundary: Record<string, unknown> | undefined;
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a fresh disposable synthetic bootstrap account",
    );
  const changed = "Synthetic Tasks Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const mfa = await enrollMfa(page, changed);
  await navigateTo(page, "后台任务");
  await expect(page.getByRole("table", { name: "任务列表" })).toBeVisible();
  await page.locator(".task-scope-help > summary").click();
  await expect(
    page.getByText(/平台健康成功不代表\s*AD 业务验收通过。/),
  ).toBeVisible();
  await page.locator(".task-scope-help > summary").click();
  const catalogue = await readJSON(context, "/api/tasks/kinds");
  expect(catalogue).toEqual({
    kinds: [
      {
        taskName: "infrastructure.health",
        payloadVersion: 1,
        scope: "platform",
        maxAttempts: 5,
        timeoutSeconds: 10,
      },
    ],
  });
  const initial = await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20");
  expect(initial.tasks).toEqual([]);
  await page.getByRole("button", { name: "提交健康检查", exact: true }).click();
  const key = await page.getByLabel("提交幂等键", { exact: true }).inputValue();
  expect(key).toMatch(/^[0-9a-f-]{36}$/);
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  const [submittedResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/tasks/submit" &&
        r.request().method() === "POST",
    ),
    page.getByRole("button", { name: "确认提交健康检查", exact: true }).click(),
  ]);
  expect(submittedResponse.status()).toBe(200);
  const submitted = await submittedResponse.json();
  expect(submitted.replayed).toBe(false);
  const id = submitted.task.taskUUID;
  expect(id).toMatch(/^[0-9a-f-]{36}$/);
  expect(submitted.task).toMatchObject({
    taskName: "infrastructure.health",
    domainId: "platform",
    payloadVersion: 1,
    maxAttempts: 5,
    parentTaskUUID: "",
  });
  await expect(
    page.getByRole("list", { name: "持久化任务事件" }),
  ).toBeVisible();
  await expect(page.locator('.task-state[data-state="succeeded"]')).toBeVisible(
    { timeout: 30_000 },
  );
  await expect(page.getByLabel("任务持久化进度")).toHaveJSProperty(
    "value",
    100,
  );
  await expect(page.getByLabel("任务实际结果")).toContainText(
    '"database": "ready"',
  );
  await expect(page.getByLabel("任务实际结果")).toContainText(
    '"queue": "ready"',
  );
  const persisted = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${encodeURIComponent(id)}`,
  );
  expect(persisted.task).toMatchObject({
    taskUUID: id,
    state: "succeeded",
    sourceState: "SUCCESS",
    attempt: 1,
    progress: 100,
    result: { database: "ready", queue: "ready" },
  });
  expect(persisted.task.resultVersion).toBeGreaterThanOrEqual(2);
  expect(
    persisted.events.map((event: { action: string }) => event.action),
  ).toEqual(
    expect.arrayContaining([
      "submitted",
      "leased",
      "started",
      "progress",
      "finished",
    ]),
  );
  for (const hidden of [
    "payload",
    "actorPassword",
    "totpCode",
    "actorId",
    "tenantId",
    "idempotencyKey",
    "leaseOwner",
    "fencingToken",
    "authorizationVersion",
  ])
    expect(persisted.task).not.toHaveProperty(hidden);
  const me = await readJSON(context, "/api/auth/me");
  const headers = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": me.csrfToken,
  };
  const replay = await context.request.post("/api/tasks/submit", {
    headers,
    data: {
      taskName: "infrastructure.health",
      domainId: "platform",
      payloadVersion: 1,
      payload: {},
      idempotencyKey: key,
      actorPassword: changed,
      totpCode: await freshCode(mfa),
    },
  });
  expect(replay.status()).toBe(200);
  expect(await replay.json()).toMatchObject({
    replayed: true,
    task: { taskUUID: id, state: "succeeded", attempt: 1 },
  });
  const afterReplay = await readJSON(
    context,
    "/api/tasks?pageIdx=1&pageSize=20&state=succeeded&domainId=platform&taskName=infrastructure.health",
  );
  expect(afterReplay.page.total).toBe(1);
  expect(
    afterReplay.tasks.map((task: { taskUUID: string }) => task.taskUUID),
  ).toEqual([id]);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("button", { name: "返回任务列表", exact: true }).click();
  await page.locator(".task-filter-panel > summary").click();
  await page.getByLabel("任务状态筛选").selectOption("succeeded");
  await page.getByLabel("域 ID 筛选").fill("platform");
  await page.getByLabel("任务种类筛选").fill("infrastructure.health");
  await page.getByLabel("每页任务数").selectOption("10");
  const filtered = await browserRead<TaskList>(
    page,
    "/api/tasks",
    filteredQuery,
    "apply-filters",
    () => page.getByRole("button", { name: "筛选任务", exact: true }).click(),
    reads,
  );
  await expectFilteredQueue(page, id, filtered);
  await expectFirstTaskInViewport(page, id);
  await captureTaskView(page, testInfo, "desktop-queue", visuals);

  const openDetail = () =>
    page.getByRole("button", { name: `详情 ${id}`, exact: true }).click();
  const returnToQueue = () =>
    page.getByRole("button", { name: "返回任务列表", exact: true }).click();
  const detailQuery = { taskUUID: id };
  // A draft is not the applied query and must not replace it in task history.
  await page.locator(".task-filter-panel > summary").click();
  await page.getByLabel("域 ID 筛选").fill("synthetic-unapplied-domain");
  const desktopDetail = await browserRead<TaskDetail>(
    page,
    "/api/tasks/detail",
    detailQuery,
    "desktop-open-detail",
    openDetail,
    reads,
  );
  await expectPersistedDetail(page, id, desktopDetail);
  expect(desktopDetail).toEqual(persisted);
  await captureTaskView(page, testInfo, "desktop-detail", visuals);
  const detailURL = page.url();

  // Native browser reload and history navigation must re-fetch the same fixed
  // task, and restore the applied query rather than a default queue.
  const reloaded = await browserRead<TaskDetail>(
    page,
    "/api/tasks/detail",
    detailQuery,
    "reload-detail",
    () => page.reload(),
    reads,
  );
  await expectPersistedDetail(page, id, reloaded);
  expect(reloaded).toEqual(persisted);
  expect(page.url()).toBe(detailURL);
  const back = await browserRead<TaskList>(
    page,
    "/api/tasks",
    filteredQuery,
    "back-to-filtered-queue",
    () => page.goBack(),
    reads,
  );
  await expectFilteredQueue(page, id, back);
  const forward = await browserRead<TaskDetail>(
    page,
    "/api/tasks/detail",
    detailQuery,
    "forward-to-fixed-detail",
    () => page.goForward(),
    reads,
  );
  await expectPersistedDetail(page, id, forward);
  expect(forward).toEqual(persisted);
  expect(page.url()).toBe(detailURL);
  const returned = await browserRead<TaskList>(
    page,
    "/api/tasks",
    filteredQuery,
    "detail-return-preserves-query",
    returnToQueue,
    reads,
  );
  await expectFilteredQueue(page, id, returned);

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileQueue = await browserRead<TaskList>(
    page,
    "/api/tasks",
    filteredQuery,
    "mobile-reload-filtered-queue",
    () => page.reload(),
    reads,
  );
  await expectFilteredQueue(page, id, mobileQueue);
  await expectFirstTaskInViewport(page, id);
  await captureTaskView(page, testInfo, "mobile-queue", visuals);
  const mobileDetail = await browserRead<TaskDetail>(
    page,
    "/api/tasks/detail",
    detailQuery,
    "mobile-open-detail",
    openDetail,
    reads,
  );
  await expectPersistedDetail(page, id, mobileDetail);
  expect(mobileDetail).toEqual(persisted);
  await captureTaskView(page, testInfo, "mobile-detail", visuals);
  const mobileReturned = await browserRead<TaskList>(
    page,
    "/api/tasks",
    filteredQuery,
    "mobile-detail-return-preserves-query",
    returnToQueue,
    reads,
  );
  await expectFilteredQueue(page, id, mobileReturned);
  await page.setViewportSize({ width: 1440, height: 900 });

  // Create a persisted task-read-only role through the same expanded UI editor.
  // These are real synthetic credentials and MFA, so denial cannot be explained
  // by an absent enrollment, invalid proof, or a forged browser permission flag.
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic Task Reader");
  await page
    .getByRole("checkbox", { name: "后台任务：读取", exact: true })
    .check();
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  const savedRole = await submitMutation(
    page,
    "/api/access/roles/save",
    "保存角色",
  );
  const role = await readJSON(
    context,
    `/api/access/roles/detail?roleID=${encodeURIComponent(savedRole.roleID)}`,
  );
  expect(
    role.permissions.map(({ mark, auth }: Permission) => ({ mark, auth })),
  ).toEqual([
    { mark: "users", auth: { readable: false, writeable: false } },
    { mark: "roles", auth: { readable: false, writeable: false } },
    { mark: "permissions", auth: { readable: false, writeable: false } },
    { mark: "tasks", auth: { readable: true, writeable: false } },
    { mark: "audit", auth: { readable: false, writeable: false } },
    { mark: "audit_exports", auth: { readable: false, writeable: false } },
    { mark: "system", auth: { readable: false, writeable: false } },
    { mark: "schedules", auth: { readable: false, writeable: false } },
    { mark: "task_archive", auth: { readable: false, writeable: false } },
    { mark: "domains", auth: { readable: false, writeable: false } },
    { mark: "operation_accounts", auth: { readable: false, writeable: false } },
    { mark: "system_logs", auth: { readable: false, writeable: false } },
    { mark: "directory_assets", auth: { readable: false, writeable: false } },
  ]);
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  const readerName = "synthetic.task.reader",
    readerInitial = "Initial Task Reader 123",
    readerChanged = "Changed Task Reader 456";
  await page.getByLabel("用户名", { exact: true }).fill(readerName);
  await page.getByLabel("初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("确认初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("角色", { exact: true }).fill(savedRole.roleID);
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  await submitMutation(page, "/api/access/users/create", "创建用户");

  const readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  try {
    const reader = await readerContext.newPage();
    await reader.goto("/");
    await login(reader, readerName, readerInitial);
    await changeInitialPassword(reader, readerInitial, readerChanged);
    const readerMfa = await enrollMfa(reader, readerChanged);
    await navigateTo(reader, "后台任务");
    await expect(reader.getByRole("table", { name: "任务列表" })).toBeVisible();
    await expect(
      reader.getByRole("rowheader").filter({ hasText: id }),
    ).toBeVisible();
    await expect(
      reader.getByRole("button", { name: "提交健康检查", exact: true }),
    ).toHaveCount(0);
    const readerDetail = await browserRead<TaskDetail>(
      reader,
      "/api/tasks/detail",
      detailQuery,
      "read-only-user-opens-persisted-detail",
      () =>
        reader.getByRole("button", { name: `详情 ${id}`, exact: true }).click(),
      reads,
    );
    expect(readerDetail).toEqual(persisted);
    await expect(reader.getByLabel("任务实际结果")).toContainText(
      '"database": "ready"',
    );
    await expectPersistedDetail(reader, id, persisted);
    for (const name of [
      "提交健康检查",
      "请求取消任务",
      "创建恢复任务",
      "核对恢复可见性",
    ]) {
      await expect(
        reader.getByRole("button", { name, exact: true }),
      ).toHaveCount(0);
    }
    await expect(
      reader.getByLabel("操作者当前密码", { exact: true }),
    ).toHaveCount(0);
    await expect(
      reader.getByLabel("未使用的认证器验证码", { exact: true }),
    ).toHaveCount(0);
    const readerMe = await readJSON(readerContext, "/api/auth/me");
    expect(readerMe.hasMfa).toBe(true);
    const readerHeaders = {
      Origin: new URL(reader.url()).origin,
      "X-CSRF-Token": readerMe.csrfToken,
    };
    const check = await readerContext.request.post("/api/access/check", {
      headers: readerHeaders,
      data: {
        paths: [
          "GET /api/tasks",
          "GET /api/tasks/detail",
          "POST /api/tasks/submit",
          "POST /api/tasks/cancel",
          "POST /api/tasks/recover",
        ],
      },
    });
    expect(check.status()).toBe(200);
    const accessCheck = await check.json();
    expect(accessCheck).toEqual({
      results: [true, true, false, false, false],
    });
    const denied = await readerContext.request.post("/api/tasks/submit", {
      headers: readerHeaders,
      data: {
        taskName: "infrastructure.health",
        domainId: "platform",
        payloadVersion: 1,
        payload: {},
        idempotencyKey: "synthetic-denied-task",
        actorPassword: readerChanged,
        totpCode: await freshCode(readerMfa),
      },
    });
    expect(denied.status()).toBe(403);
    expect(await denied.json()).toEqual({ error: "forbidden" });
    const totalAfterDeniedSubmission = (
      await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")
    ).page.total;
    expect(totalAfterDeniedSubmission).toBe(1);
    await navigateTo(reader, "退出登录");
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    const loggedOutRead = await readerContext.request.get("/api/tasks");
    expect(loggedOutRead.status()).toBe(401);
    roleBoundary = {
      persistedPermissions: role.permissions,
      mfaEnrolled: readerMe.hasMfa,
      accessCheck: accessCheck.results,
      unauthorizedSubmitStatus: denied.status(),
      totalAfterDeniedSubmission,
      loggedOutReadStatus: loggedOutRead.status(),
    };
  } finally {
    await readerContext.close();
  }

  // Bind safe rendered evidence to the exact checkout, browser and isolated
  // acceptance run. Local dirty runs remain visibly distinct from clean CI.
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const checkoutCommit = execFileSync("git", ["rev-parse", "HEAD"], {
    encoding: "utf8",
  }).trim();
  const dirty =
    execFileSync("git", ["status", "--porcelain"], {
      encoding: "utf8",
    }).trim() !== "";
  const hashFile = (file: string) =>
    createHash("sha256")
      .update(readFileSync(resolve(root, file)))
      .digest("hex");
  expect(visuals).toHaveLength(8);
  expect(roleBoundary).toBeDefined();
  const manifest = {
    formatVersion: 1,
    suite: "tasks",
    chain: "real browser → API → Worker → PostgreSQL",
    recordedAt: new Date().toISOString(),
    checkoutCommit,
    dirty,
    workflowCommit: process.env.GITHUB_SHA ?? null,
    workflowRun: process.env.GITHUB_RUN_ID ?? null,
    workflowAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    runtime: {
      node: process.version,
      platform: process.platform,
      architecture: process.arch,
      browser: context.browser()!.version(),
      nativeStreamMode: process.env.ADTR_E2E_NATIVE_STREAM_MODE ?? null,
    },
    sourceSHA256: Object.fromEntries(
      [
        "web/e2e/tasks.spec.ts",
        "web/src/TaskWorkspace.tsx",
        "web/src/TaskPresentation.tsx",
        "web/src/task-view-state.ts",
        "web/src/soc-tasks.css",
        "web/src/task-api.ts",
        "web/src/task-common.tsx",
        "web/package-lock.json",
        ".github/workflows/ci.yml",
      ].map((file) => [file, hashFile(file)]),
    ),
    artifactsSHA256: {
      apiWorker: hashFile("bin/adtr"),
      frontendEntry: hashFile("web/dist/index.html"),
    },
    task: persisted.task,
    events: persisted.events,
    filteredPage: filtered.page,
    browserReads: reads,
    roleBoundary,
    screenshots: visuals,
  };
  const manifestPath = testInfo.outputPath("tasks-soc-evidence.json");
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n");
  await testInfo.attach("tasks-soc-evidence", {
    path: manifestPath,
    contentType: "application/json",
  });
});
