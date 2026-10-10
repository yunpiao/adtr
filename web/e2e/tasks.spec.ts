import { navigateTo } from "./navigation";
import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";

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

// This suite requires its own fresh database plus a real API and Worker. No
// route interception, fake clock, test executor, or direct SQL success fixture.
test("real browser → API → Worker → PostgreSQL task result, idempotency and role boundary", async ({
  page,
  context,
}) => {
  test.setTimeout(300_000);
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
  await expect(
    page.getByText(/平台健康成功不代表\s*AD 业务验收通过。/),
  ).toBeVisible();
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
    page.getByRole("table", { name: "持久化任务事件" }),
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
  await page.getByRole("button", { name: "返回任务列表", exact: true }).click();
  await page.getByLabel("任务状态筛选").selectOption("succeeded");
  await page.getByLabel("域 ID 筛选").fill("platform");
  await page.getByRole("button", { name: "筛选任务", exact: true }).click();
  await expect(
    page.getByRole("rowheader").filter({ hasText: id }),
  ).toBeVisible();

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
    await reader
      .getByRole("button", { name: `详情 ${id}`, exact: true })
      .click();
    await expect(reader.getByLabel("任务实际结果")).toContainText(
      '"database": "ready"',
    );
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
    expect(await check.json()).toEqual({
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
    expect(
      (await readJSON(context, "/api/tasks?pageIdx=1&pageSize=20")).page.total,
    ).toBe(1);
    await navigateTo(reader, "退出登录");
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect((await readerContext.request.get("/api/tasks")).status()).toBe(401);
  } finally {
    await readerContext.close();
  }
});
