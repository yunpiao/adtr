import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";
import { readFileSync, statfsSync } from "node:fs";
import type {
  Current,
  History,
  StorageView,
  SystemHealth,
} from "../src/system-api";

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
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
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

// Fresh synthetic database, real local Linux API and worker are required.
// No route interception, fake measurements, test clock or SQL success fixture.
// The owning harness passes its exact worker PID for the final shutdown check.
test("real browser → Linux sampler → PostgreSQL history/settings and actual worker/permission boundaries", async ({
  page,
  context,
}) => {
  test.setTimeout(420_000);
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  const workerPID = Number(process.env.ADTR_E2E_WORKER_PID);
  if (
    !username ||
    !password ||
    !Number.isSafeInteger(workerPID) ||
    workerPID <= 1
  )
    throw new Error(
      "Provide fresh synthetic bootstrap credentials and the owning harness ADTR_E2E_WORKER_PID",
    );
  const workerCommand = readFileSync(
    `/proc/${workerPID}/cmdline`,
    "utf8",
  ).split("\0");
  expect(workerCommand[0]).toMatch(/(?:^|\/)adtr$/);
  expect(workerCommand).toEqual(expect.arrayContaining(["-mode", "worker"]));
  const changed = "Synthetic System Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const mfa = await enrollMfa(page, changed);
  await page.getByRole("button", { name: "系统健康", exact: true }).click();
  await expect(page.getByRole("table", { name: "已登记节点" })).toBeVisible();
  const nodes = await readJSON(context, "/api/system/nodes");
  expect(nodes.nodeList).toHaveLength(1);
  expect(nodes.nodeList[0]).toMatchObject({
    instance: "local-api",
    scope: "kernel_visible",
    ip: null,
  });
  expect(nodes.nodeList[0].hostName).toBe(
    readFileSync("/proc/sys/kernel/hostname", "utf8").trim(),
  );
  const info = await readJSON(context, "/api/system/info");
  expect(info.version.OSPlatform).toBe("linux");
  expect(info.basic.systemName).toBe("ADTR");
  expect(info.version.engineVersion).toBeNull();
  expect(info.upgrade.upgradeMajorVersion).toBeNull();
  expect(info.capabilities.license).toBe("unsupported");
  let current: Current;
  await expect
    .poll(
      async () => {
        current = await readJSON(
          context,
          "/api/system/resources/current?instance=local-api",
        );
        if (current.snapshot?.cpu.availability !== "available")
          return current.snapshot?.cpu.availability;
        // The first available CPU sample can be in the same second as checkedAt.
        // History excludes its end second, so current availability alone does
        // not establish that the initial chart has a persisted sample to draw.
        const endTime = Math.floor(Date.parse(current.checkedAt) / 1000);
        const history: History = await readJSON(
          context,
          `/api/system/resources/history?instance=local-api&graphType=cpu_basic&startTime=${endTime - 900}&endTime=${endTime}`,
        );
        return history.info[0].data.timestamp.length > 0
          ? "available"
          : "awaiting_history_sample";
      },
      { timeout: 40_000, intervals: [1000, 2000] },
    )
    .toBe("available");
  const sample = current!.snapshot!;
  const actualTotal =
    BigInt(
      readFileSync("/proc/meminfo", "utf8").match(
        /^MemTotal:\s+(\d+) kB$/m,
      )![1],
    ) * 1024n;
  expect(sample.memory.totalBytes).toBe(String(actualTotal));
  expect(
    BigInt(sample.memory.usedBytes!) + BigInt(sample.memory.availableBytes!),
  ).toBe(actualTotal);
  expect(sample.memory.percent!).toBeCloseTo(
    (Number(BigInt(sample.memory.usedBytes!)) * 100) / Number(actualTotal),
    6,
  );
  expect(sample.cpu.percent).toBeGreaterThanOrEqual(0);
  expect(sample.cpu.percent).toBeLessThanOrEqual(100);
  expect(sample.cpu.intervalStart).toBeTruthy();
  const uptime = Number(readFileSync("/proc/uptime", "utf8").split(" ")[0]);
  expect(uptime - sample.uptime.seconds!).toBeGreaterThanOrEqual(-1);
  expect(uptime - sample.uptime.seconds!).toBeLessThan(45);
  const stat = statfsSync("/", { bigint: true });
  const observedRoot = sample.storage.find((s) => s.id === "runtime-root")!;
  expect(observedRoot.totalBytes).toBe(String(stat.blocks * stat.bsize));
  expect(observedRoot.scope).toBe("runtime_filesystem");
  expect(observedRoot.mount).toBe("/");
  expect(
    BigInt(observedRoot.usedBytes!) +
      BigInt(observedRoot.freeBytes!) +
      BigInt(observedRoot.reservedBytes!),
  ).toBe(BigInt(observedRoot.totalBytes!));
  await page.getByRole("button", { name: "资源与历史", exact: true }).click();
  await expect(page.getByRole("img", { name: /实际样本/ })).toBeVisible();
  for (const [graph, range] of [
    ["cpu_basic", "900"],
    ["ram_basic", "3600"],
    ["disk_usage", "21600"],
    ["cpu_basic", "86400"],
  ]) {
    await page.getByLabel("历史指标", { exact: true }).selectOption(graph);
    await page.getByLabel("历史时间范围", { exact: true }).selectOption(range);
    await expect(page.getByRole("img", { name: /实际样本/ })).toBeVisible();
  }
  const endTime = Math.floor(Date.now() / 1000),
    startTime = endTime - 86400;
  const history: History = await readJSON(
    context,
    `/api/system/resources/history?instance=local-api&graphType=ram_basic&startTime=${startTime}&endTime=${endTime}`,
  );
  expect(history.availableSince).not.toBeNull();
  expect(history.gaps.length).toBeGreaterThan(0);
  const values = history.info[0].data.value.map(Number),
    timestamps = history.info[0].data.timestamp;
  expect(values.length).toBeGreaterThan(0);
  expect(values.length).toBe(timestamps.length);
  expect(history.sampleIntervalSeconds).toBe(15);
  expect(history.info[0].data.dataStatistics.max).toBe(Math.max(...values));
  expect(history.info[0].data.dataStatistics.min).toBe(Math.min(...values));
  expect(history.info[0].data.dataStatistics.avg).toBeCloseTo(
    values.reduce((a, b) => a + b, 0) / values.length,
    8,
  );
  expect(history.info[0].data.dataStatistics.current).toBe(values.at(-1));
  expect(
    timestamps.every(
      (t, i) => t >= startTime && t < endTime && (!i || t >= timestamps[i - 1]),
    ),
  ).toBe(true);
  const beforeSampling =
    Math.floor(Date.parse(history.availableSince!) / 1000) - 10;
  const empty: History = await readJSON(
    context,
    `/api/system/resources/history?instance=local-api&graphType=ram_basic&startTime=${beforeSampling - 300}&endTime=${beforeSampling}`,
  );
  expect(empty.info[0].data.value).toEqual([]);
  expect(empty.info[0].data.dataStatistics).toEqual({
    max: null,
    min: null,
    avg: null,
    current: null,
  });
  for (const query of [
    "instance=other&graphType=cpu_basic",
    "instance=local-api&graphType=unknown",
    "instance=local-api&graphType=disk_usage&storageId=/etc/passwd",
  ]) {
    const denied = await context.request.get(
      `/api/system/resources/history?${query}&startTime=${endTime - 900}&endTime=${endTime}`,
    );
    expect([400, 404]).toContain(denied.status());
  }
  expect(
    (
      await context.request.get(
        `/api/system/resources/history?instance=local-api&graphType=cpu_basic&startTime=${endTime - 86401}&endTime=${endTime}`,
      )
    ).status(),
  ).toBe(400);
  await page.getByRole("button", { name: "存储管理", exact: true }).click();
  await expect(
    page.getByRole("table", { name: "实际文件系统存储" }),
  ).toBeVisible();
  const initial: StorageView = await readJSON(
    context,
    "/api/system/storage?instance=local-api&page=1&pageSize=20",
  );
  expect(initial.storage[0]).toMatchObject({
    id: "runtime-root",
    alarmPercent: 85,
    revision: 0,
  });
  await page
    .getByRole("button", { name: "调整阈值 runtime-root", exact: true })
    .click();
  await page.getByLabel("存储告警阈值（%）").fill("90");
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  const [savedResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/system/storage/settings" &&
        r.request().method() === "POST",
    ),
    page.getByRole("button", { name: "保存告警阈值", exact: true }).click(),
  ]);
  expect(savedResponse.status()).toBe(200);
  expect(await savedResponse.json()).toMatchObject({
    result: 1,
    setting: {
      instance: "local-api",
      storageId: "runtime-root",
      alarmPercent: 90,
      revision: 1,
    },
  });
  const rootStorage = page
    .getByRole("table", { name: "实际文件系统存储" })
    .getByRole("row")
    .filter({
      has: page.getByRole("rowheader", { name: "runtime-root", exact: true }),
    });
  // The threshold and revision share a cell, whose exact name includes both.
  const savedThreshold = rootStorage.getByRole("cell", {
    name: "90% 版本 1",
    exact: true,
  });
  await expect(rootStorage.getByText("版本 1", { exact: true })).toBeVisible();
  await expect(savedThreshold).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "系统健康", exact: true }).click();
  await page.getByRole("button", { name: "存储管理", exact: true }).click();
  await expect(rootStorage.getByText("版本 1", { exact: true })).toBeVisible();
  await expect(savedThreshold).toBeVisible();
  const persisted: StorageView = await readJSON(
    context,
    "/api/system/storage?instance=local-api&page=1&pageSize=20",
  );
  expect(persisted.storage[0]).toMatchObject({ alarmPercent: 90, revision: 1 });
  const me = await readJSON(context, "/api/auth/me"),
    headers = {
      Origin: new URL(page.url()).origin,
      "X-CSRF-Token": me.csrfToken,
    };
  const input = {
    instance: "local-api",
    storageId: "runtime-root",
    setType: "alarm",
    percent: 89,
    expectedRevision: 0,
    actorPassword: changed,
    totpCode: await freshCode(mfa),
  };
  const conflict = await context.request.post("/api/system/storage/settings", {
    headers,
    data: input,
  });
  expect(conflict.status()).toBe(409);
  expect(await conflict.json()).toEqual({ error: "revision_conflict" });
  expect(
    (
      await readJSON(
        context,
        "/api/system/storage?instance=local-api&page=1&pageSize=20",
      )
    ).storage[0],
  ).toMatchObject({ alarmPercent: 90, revision: 1 });
  for (const percent of [84, 91, 85.5])
    expect(
      (
        await context.request.post("/api/system/storage/settings", {
          headers,
          data: { ...input, percent },
        })
      ).status(),
    ).toBe(400);
  expect(
    (
      await context.request.post("/api/system/storage/settings", {
        headers: { Origin: headers.Origin },
        data: input,
      })
    ).status(),
  ).toBe(403);
  expect(
    (
      await context.request.post("/api/system/storage/settings", {
        headers: { ...headers, Origin: "https://invalid.example" },
        data: input,
      })
    ).status(),
  ).toBe(403);
  await page.getByRole("button", { name: "平台依赖健康", exact: true }).click();
  await expect(page.getByRole("table", { name: "实际依赖检查" })).toBeVisible();
  await expect
    .poll(
      async () =>
        ((await readJSON(context, "/api/system/health")) as SystemHealth).worker
          .availability,
      { timeout: 20_000 },
    )
    .toBe("available");
  const healthy: SystemHealth = await readJSON(context, "/api/system/health");
  for (const id of ["api", "postgresql", "sampler", "worker"])
    expect(healthy.dependencies.find((d) => d.id === id)?.status).toBe(
      "healthy",
    );
  for (const id of ["cache", "engine"])
    expect(healthy.dependencies.find((d) => d.id === id)?.status).toBe(
      "not_configured",
    );
  expect(healthy.worker.cycles.map((c) => c.cycle)).toEqual(
    expect.arrayContaining(["queue", "recovery"]),
  );
  expect(
    healthy.worker.cycles.every(
      (c) =>
        c.lastActivityAt && c.lastSuccessAt && c.evidence === "completed_cycle",
    ),
  ).toBe(true);

  // Persist system-only read authority through the actual role editor and use
  // a second browser with valid MFA to prove server denial of settings writes.
  await page.getByRole("button", { name: "访问管理", exact: true }).click();
  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic System Reader");
  await page
    .getByRole("checkbox", { name: "系统健康：读取", exact: true })
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
  ).toEqual(
    [
      "users",
      "roles",
      "permissions",
      "tasks",
      "audit",
      "audit_exports",
      "system",
      "schedules",
      "task_archive",
      "domains",
      "operation_accounts",
      "system_logs",
    ].map((mark) => ({
      mark,
      auth: { readable: mark === "system", writeable: false },
    })),
  );
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  const readerName = "synthetic.system.reader",
    readerInitial = "Initial System Reader 123",
    readerChanged = "Changed System Reader 456";
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
    await reader.getByRole("button", { name: "系统健康", exact: true }).click();
    await reader.getByRole("button", { name: "存储管理", exact: true }).click();
    await expect(
      reader.getByRole("table", { name: "实际文件系统存储" }),
    ).toBeVisible();
    await expect(
      reader.getByRole("button", {
        name: "调整阈值 runtime-root",
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(reader.getByText("只读", { exact: true })).toBeVisible();
    const readerMe = await readJSON(readerContext, "/api/auth/me"),
      readerHeaders = {
        Origin: new URL(reader.url()).origin,
        "X-CSRF-Token": readerMe.csrfToken,
      };
    expect(readerMe.hasMfa).toBe(true);
    const check = await readerContext.request.post("/api/access/check", {
      headers: readerHeaders,
      data: {
        paths: [
          "GET /api/system/info",
          "GET /api/system/resources/history",
          "GET /api/system/storage",
          "GET /api/system/health",
          "POST /api/system/storage/settings",
        ],
      },
    });
    expect(await check.json()).toEqual({
      results: [true, true, true, true, false],
    });
    const denied = await readerContext.request.post(
      "/api/system/storage/settings",
      {
        headers: readerHeaders,
        data: {
          ...input,
          expectedRevision: 1,
          actorPassword: readerChanged,
          totpCode: await freshCode(readerMfa),
        },
      },
    );
    expect(denied.status()).toBe(403);
    expect(await denied.json()).toEqual({ error: "forbidden" });
    expect(
      (
        await readJSON(
          context,
          "/api/system/storage?instance=local-api&page=1&pageSize=20",
        )
      ).storage[0],
    ).toMatchObject({ alarmPercent: 90, revision: 1 });
    await reader.getByRole("button", { name: "退出登录", exact: true }).click();
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/system/health")).status(),
    ).toBe(401);
  } finally {
    await readerContext.close();
  }
  // Only the exact synthetic child supplied by the owning harness is stopped.
  // The product has no worker-stop endpoint, and no fake stale row is injected.
  process.kill(workerPID, "SIGTERM");
  await page.getByRole("button", { name: "系统健康", exact: true }).click();
  await page.getByRole("button", { name: "平台依赖健康", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await readJSON(context, "/api/system/health")) as SystemHealth).worker
          .availability,
      { timeout: 35_000, intervals: [2000] },
    )
    .toBe("unavailable");
  const stopped: SystemHealth = await readJSON(context, "/api/system/health");
  expect(stopped.result).toBe("degraded");
  expect(stopped.dependencies.find((d) => d.id === "worker")?.status).toBe(
    "unhealthy",
  );
  expect(stopped.dependencies.find((d) => d.id === "api")?.status).toBe(
    "healthy",
  );
  expect(stopped.dependencies.find((d) => d.id === "postgresql")?.status).toBe(
    "healthy",
  );
  expect(
    stopped.worker.cycles.every((c) => c.availability === "unavailable"),
  ).toBe(true);
  await expect(
    page
      .getByRole("table", { name: "实际依赖检查" })
      .getByRole("row")
      .filter({ hasText: "ADTR worker" }),
  ).toContainText("不健康", { timeout: 10_000 });
  const browserStorage = await page.evaluate(() =>
    JSON.stringify({
      local: { ...localStorage },
      session: { ...sessionStorage },
    }),
  );
  for (const secret of [changed, mfa.secret, me.csrfToken])
    expect(browserStorage).not.toContain(secret);
});
