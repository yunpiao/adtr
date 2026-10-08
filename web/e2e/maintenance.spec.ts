import {
  test as base,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
} from "@playwright/test";
import { createHmac } from "node:crypto";

const test = base.extend<{ producerContext: BrowserContext }>({
  producerContext: async ({ browser, baseURL }, use) => {
    const context = await browser.newContext({ baseURL });
    try {
      await use(context);
    } finally {
      await context.close();
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

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await fillSecret(page.getByLabel("密码", { exact: true }), password);
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
  await fillSecret(page.getByLabel("当前密码", { exact: true }), initial);
  await fillSecret(page.getByLabel("新密码", { exact: true }), changed);
  await fillSecret(page.getByLabel("确认新密码", { exact: true }), changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), password);
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
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
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

// Fresh synthetic database, real API, scheduler and worker; natural UTC time.
// No route interception, mocked executor, fake clock or direct SQL fixtures.
test("real scheduled health occurrence and reversible visibility through browser, API, Worker and PostgreSQL", async ({
  page: bootstrapPage,
  context: bootstrapContext,
  producerContext: context,
}) => {
  test.setTimeout(480_000);
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
  // password/enroll/confirm (1-3), producer creation (4), reader role/user (5-6).
  // Producer: password/enroll/confirm (1-3), create/enable/pause (4-6), enable
  // replay (7), archive/restore (8-9), create replay (10). Replays also count.
  // A new context only isolates cookies; the distinct UI-created user is what
  // supplies a separate budget. This producer owns every schedule/task action.
  const producerName = "synthetic.maintenance.producer";
  const producerInitial = "Initial Maintenance Producer 123";
  const changed = "Changed Maintenance Producer 456";
  await bootstrapPage
    .getByRole("button", { name: "访问管理", exact: true })
    .click();
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
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: button, exact: true }).click(),
    ]);
    expect(r.status(), `POST ${path}: ${await r.text()}`).toBe(200);
    return r.json();
  };
  const fillProof = () => fillActorProof(page, changed, mfa);
  await page.getByRole("button", { name: "后台任务", exact: true }).click();
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
  const replay = await context.request.post("/api/tasks/schedules/enable", {
    headers,
    data: {
      scheduleUUID: sid,
      expectedControlVersion: created.schedule.controlVersion,
      idempotencyKey: enableKey,
      actorPassword: changed,
      totpCode: await freshCode(mfa),
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
  const active = await readJSON(context, "/api/tasks?pageIdx=1&pageSize=100");
  expect(
    active.tasks.map((t: { taskUUID: string }) => t.taskUUID),
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
        totpCode: await freshCode(mfa),
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
  await bootstrapPage
    .getByRole("button", { name: "访问管理", exact: true })
    .click();
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
  const readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  try {
    const reader = await readerContext.newPage();
    await reader.goto("/");
    await login(reader, readerName, readerInitial);
    await changeInitialPassword(reader, readerInitial, readerChanged);
    const readerMfa = await enrollMfa(reader, readerChanged);
    await reader.getByRole("button", { name: "后台任务", exact: true }).click();
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
    const denied = await readerContext.request.post(
      "/api/tasks/schedules/enable",
      {
        headers: readerHeaders,
        data: {
          scheduleUUID: sid,
          expectedControlVersion: paused.schedule.controlVersion,
          idempotencyKey: "synthetic-reader-denied",
          actorPassword: readerChanged,
          totpCode: await freshCode(readerMfa),
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
    await reader.getByRole("button", { name: "退出登录", exact: true }).click();
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/tasks/schedules")).status(),
    ).toBe(401);
  } finally {
    await readerContext.close();
  }
});
