import { navigateTo } from "./navigation";
import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type TestInfo,
} from "@playwright/test";
import { execFileSync } from "node:child_process";
import { createHash, createHmac } from "node:crypto";
import { readdirSync, readFileSync, statfsSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type {
  Current,
  History,
  StorageView,
  SystemHealth,
} from "../src/system-api";

// Only explicit, read-only system views may be retained. Login, MFA and
// privileged-write proofs must never become automatic failure artifacts.
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
  await navigateTo(page, "多因素认证");
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

type SystemVisual = {
  file: string;
  state:
    | "healthy"
    | "history"
    | "container-memory"
    | "worker-stale"
    | "recovered";
  sha256: string;
  viewport: { width: number; height: number };
  documentWidth: number;
};

async function captureSystemViews(
  page: Page,
  testInfo: TestInfo,
  state: SystemVisual["state"],
  visuals: SystemVisual[],
  assertView: () => Promise<void>,
) {
  for (const [device, viewport] of [
    ["desktop", { width: 1440, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
  ] as const) {
    await page.setViewportSize(viewport);
    await assertView();
    const heading = page.locator(".system-soc-heading");
    const headingBounds = await heading.boundingBox();
    expect(headingBounds).not.toBeNull();
    for (const child of await heading.locator(":scope > *").all()) {
      const bounds = await child.boundingBox();
      expect(bounds).not.toBeNull();
      expect(bounds!.y).toBeGreaterThanOrEqual(headingBounds!.y);
      expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(
        headingBounds!.y + headingBounds!.height,
      );
    }
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    for (const label of ["未使用的认证器验证码", "认证器验证码", "设置密钥"])
      await expect(page.getByLabel(label, { exact: true })).toHaveCount(0);
    await page.evaluate(() => window.scrollTo(0, 0));
    const documentWidth = await page.evaluate(
      () => document.documentElement.scrollWidth,
    );
    expect(
      documentWidth,
      `${device} ${state} document overflow`,
    ).toBeLessThanOrEqual(viewport.width);
    const file = `system-soc-${device}-${state}.png`;
    const bytes = await page.screenshot({
      path: testInfo.outputPath(file),
      fullPage: true,
    });
    visuals.push({
      file,
      state,
      sha256: createHash("sha256").update(bytes).digest("hex"),
      viewport,
      documentWidth,
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

function healthEvidence(health: SystemHealth) {
  // These allowlisted status fields describe only this isolated runtime.
  // Never serialize authentication responses, headers, environment or proofs.
  return {
    result: health.result,
    checkedAt: health.checkedAt,
    dependencies: health.dependencies.map(
      ({ id, status, reason, source, scope, observedAt, checkedAt }) => ({
        id,
        status,
        reason,
        source,
        scope,
        observedAt,
        checkedAt,
      }),
    ),
    worker: {
      availability: health.worker.availability,
      reason: health.worker.reason ?? null,
      checkedAt: health.worker.checkedAt,
      staleAfterSeconds: health.worker.staleAfterSeconds,
      cycles: health.worker.cycles.map(
        ({
          workerId,
          cycle,
          status,
          evidence,
          lastActivityAt,
          lastSuccessAt,
          availability,
          reason,
        }) => ({
          workerId,
          cycle,
          status,
          evidence,
          lastActivityAt,
          lastSuccessAt,
          availability,
          reason: reason ?? null,
        }),
      ),
    },
  };
}

function expectedHealthResult(health: SystemHealth) {
  return health.dependencies.some(
    (d) => d.status === "unknown" || d.status === "unhealthy",
  )
    ? "degraded"
    : "healthy";
}
function expectContainerHealthEvidence(health: SystemHealth) {
  const container = health.dependencies.find(
    (d) => d.id === "container-memory",
  );
  expect(container).toBeDefined();
  expect(container!.scope).toBe("runtime_cgroup");
  expect(["cgroup", "cgroup_v1", "cgroup_v2"]).toContain(container!.source);
  expect(container!.reason.length).toBeGreaterThan(0);
  expect(container!.status).toBe(
    container!.reason === "fresh_cgroup_observation"
      ? "healthy"
      : container!.reason === "visible_limit_reached"
        ? "unhealthy"
        : "unknown",
  );
  expect(health.result).toBe(expectedHealthResult(health));
}

async function expectHealthWorkspace(page: Page, health: SystemHealth) {
  const region = page.getByRole("region", {
    name: "依赖诊断工作台",
    exact: true,
  });
  await expect(region).toBeVisible();
  await expect(
    region.getByRole("heading", { name: "需要处理的异常", exact: true }),
  ).toBeVisible();
  const anomalies = health.dependencies.filter((dependency) =>
    ["unhealthy", "unknown"].includes(dependency.status),
  );
  const findings = region.locator(".system-findings > li");
  await expect(findings).toHaveCount(anomalies.length, { timeout: 10_000 });
  for (const dependency of anomalies) {
    const finding = findings.filter({ hasText: dependency.name });
    await expect(finding).toBeVisible();
    await expect(finding).toContainText("处理建议：");
    await expect(finding).toContainText(dependency.reason);
    await expect(finding).toContainText(dependency.source);
  }
  if (anomalies.length === 0)
    await expect(region).toContainText("当前返回的依赖中没有异常或未知项。");
  const unconfigured = health.dependencies.filter(
    (dependency) => dependency.status === "not_configured",
  );
  expect(unconfigured.map(({ id }) => id).sort()).toEqual(["cache", "engine"]);
  const capabilities = region.locator("details.system-unconfigured");
  await expect(capabilities.locator("summary")).toHaveText("未配置能力（2）");
  if (
    !(await capabilities.evaluate((node) => (node as HTMLDetailsElement).open))
  )
    await capabilities.locator("summary").click();
  await expect(capabilities.getByRole("listitem")).toHaveCount(2);
  for (const dependency of unconfigured) {
    const item = capabilities
      .getByRole("listitem")
      .filter({ hasText: dependency.name });
    await expect(item).toBeVisible();
    await expect(item).toContainText("未配置");
    await expect(item.getByText("健康", { exact: true })).toHaveCount(0);
    await expect(findings.filter({ hasText: dependency.name })).toHaveCount(0);
  }
  const table = page.getByRole("table", { name: "实际依赖检查", exact: true });
  await expect(table.getByRole("columnheader")).toHaveText([
    "依赖",
    "类型",
    "状态",
    "检查依据",
    "观测时间（UTC）",
    "检查时间（UTC）",
  ]);
  const labels = {
    healthy: "健康",
    unhealthy: "不健康",
    unknown: "未知",
    not_configured: "未配置",
  };
  for (const dependency of health.dependencies) {
    const row = table.getByRole("row").filter({
      has: page.getByRole("rowheader").filter({ hasText: dependency.name }),
    });
    await expect(
      row.getByRole("cell", { name: labels[dependency.status], exact: true }),
    ).toBeVisible();
  }
}

// Fresh synthetic database, real local Linux API and worker are required.
// No route interception, fake measurements, test clock or SQL success fixture.
// The owning harness passes its exact worker PID for the pause/resume check.
test("real browser → Linux sampler → PostgreSQL history/settings and actual worker/permission boundaries", async ({
  page,
  context,
}, testInfo) => {
  // Allow real TOTP counters plus pause/resume while leaving the owning
  // harness's 480-second deadline time to run its cleanup.
  test.setTimeout(450_000);
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
  const workerStart = () => {
    const stat = readFileSync(`/proc/${workerPID}/stat`, "utf8");
    // Field 22 is the process start tick. The command in parentheses may
    // contain spaces, so split only after its final closing parenthesis.
    return stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19];
  };
  const workerStartTick = workerStart();
  expect(workerStartTick).toMatch(/^\d+$/);
  const signalWorker = (signal: "SIGSTOP" | "SIGCONT") => {
    expect(workerStart()).toBe(workerStartTick);
    expect(
      readFileSync(`/proc/${workerPID}/cmdline`, "utf8").split("\0"),
    ).toEqual(workerCommand);
    process.kill(workerPID, signal);
  };
  const visuals: SystemVisual[] = [];
  const changed = "Synthetic System Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const mfa = await enrollMfa(page, changed);
  await navigateTo(page, "系统健康");
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
  // This reads the actual persisted cgroup series. Unlimited/unsupported hosts
  // must show a real gap rather than a fabricated zero or kernel RAM series.
  const containerResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      url.pathname === "/api/system/resources/history" &&
      url.searchParams.get("graphType") === "container_ram"
    );
  });
  await page
    .getByLabel("历史指标", { exact: true })
    .selectOption("container_ram");
  const containerHistoryResponse = await containerResponse;
  expect(containerHistoryResponse.status()).toBe(200);
  const containerHistory: History = await containerHistoryResponse.json();
  expect(containerHistory.graphType).toBe("container_ram");
  const containerSeries = containerHistory.info[0].data;
  expect(containerSeries.timestamp).toHaveLength(containerSeries.value.length);
  for (const value of containerSeries.value) {
    expect(Number.isFinite(Number(value))).toBe(true);
    expect(Number(value)).toBeGreaterThanOrEqual(0);
  }
  if (containerSeries.value.length) {
    expect(containerHistory.segments?.length).toBeGreaterThan(0);
    expect(containerHistory.segments![0].startTime).toBe(
      containerSeries.timestamp[0],
    );
    for (const segment of containerHistory.segments!) {
      expect(containerSeries.timestamp).toContain(segment.startTime);
      expect(["cgroup_v1", "cgroup_v2"]).toContain(segment.source);
      expect(BigInt(segment.limitBytes)).toBeGreaterThan(0n);
      expect(segment.sourceID).toMatch(/^[a-f0-9]{64}$/);
    }
    await expect(
      page.getByText(`观测分段：${containerHistory.segments!.length} 段。`, {
        exact: false,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("img", {
        name: `容器内存使用率：${containerSeries.value.length} 个实际样本，${containerHistory.gaps.length} 个缺测区间`,
      }),
    ).toBeVisible();
  } else {
    await expect(
      page.getByText("此范围没有真实历史样本，统计值不可用。"),
    ).toBeVisible();
    expect(containerSeries.dataStatistics).toEqual({
      max: null,
      min: null,
      avg: null,
      current: null,
    });
  }
  await expect(
    page.getByRole("region", { name: "容器内存观测" }),
  ).toBeVisible();
  await expect(page.getByText(/隐藏祖先约束与同组进程竞争/)).toBeVisible();
  expect(sample.containerMemory).toBeDefined();
  expect(sample.containerMemory!.scope).toBe("runtime_cgroup");
  expect(sample.containerMemory!.hierarchy).toBe("visible");
  if (sample.containerMemory!.usageBytes !== null)
    expect(sample.containerMemory!.sourceID).toMatch(/^[a-f0-9]{64}$/);
  await captureSystemViews(
    page,
    testInfo,
    "container-memory",
    visuals,
    async () => {
      const card = page.getByRole("region", { name: "容器内存观测" });
      await expect(card).toBeVisible();
      await expect(card).toContainText(sample.containerMemory!.source);
      await expect(card).toContainText("runtime_cgroup");
      await expect(card).toContainText("可见层级上限");
      await expect(page.getByLabel("历史指标", { exact: true })).toHaveValue(
        "container_ram",
      );
      await expect(card.locator('[data-state="healthy"]')).toHaveCount(0);
    },
  );
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
  const [visibleHistoryResponse] = await Promise.all([
    page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname === "/api/system/resources/history" &&
        url.searchParams.get("graphType") === "ram_basic"
      );
    }),
    page.getByLabel("历史指标", { exact: true }).selectOption("ram_basic"),
  ]);
  expect(visibleHistoryResponse.status()).toBe(200);
  const visibleHistory = (await visibleHistoryResponse.json()) as History;
  expect(visibleHistory.info[0].data.timestamp.length).toBeGreaterThan(0);
  expect(visibleHistory.gaps.length).toBeGreaterThan(0);
  const historyChart = page.getByRole("img", { name: /实际样本/ });
  await expect(historyChart).toHaveAccessibleName(
    new RegExp(`${visibleHistory.info[0].data.timestamp.length} 个实际样本`),
  );
  await captureSystemViews(page, testInfo, "history", visuals, async () => {
    await expect(historyChart).toBeVisible();
    await expect(page.getByLabel("历史指标", { exact: true })).toHaveValue(
      "ram_basic",
    );
    await expect(page.getByLabel("历史时间范围", { exact: true })).toHaveValue(
      "86400",
    );
    await expect(
      page.getByText("仅绘制真实观测点", { exact: false }),
    ).toBeVisible();
  });
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
  await fillSecret(page.getByLabel("操作者当前密码", { exact: true }), changed);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
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
  await navigateTo(page, "系统健康");
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
  // Keep the real editor on revision 1, then persist a concurrent authorized
  // update. The browser must clear its proofs and require a reread after CAS.
  await page
    .getByRole("button", { name: "调整阈值 runtime-root", exact: true })
    .click();
  await expect(page.getByText(/当前版本：\s*1/)).toBeVisible();
  const concurrentSave = await context.request.post(
    "/api/system/storage/settings",
    {
      headers,
      data: {
        instance: "local-api",
        storageId: "runtime-root",
        setType: "alarm",
        percent: 89,
        expectedRevision: 1,
        actorPassword: changed,
        totpCode: await freshCode(mfa),
      },
    },
  );
  expect(concurrentSave.status()).toBe(200);
  expect(await concurrentSave.json()).toMatchObject({
    result: 1,
    setting: { alarmPercent: 89, revision: 2 },
  });
  await page.getByLabel("存储告警阈值（%）").fill("88");
  await fillSecret(page.getByLabel("操作者当前密码", { exact: true }), changed);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
  const [browserConflict] = await Promise.all([
    page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/system/storage/settings" &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "保存告警阈值", exact: true }).click(),
  ]);
  expect(browserConflict.status()).toBe(409);
  expect(await browserConflict.json()).toEqual({ error: "revision_conflict" });
  // A failing clear check must report a boolean, never the proof's value.
  for (const label of ["操作者当前密码", "未使用的认证器验证码"])
    await expect
      .poll(
        async () =>
          (await page.getByLabel(label, { exact: true }).inputValue()) === "",
      )
      .toBe(true);
  await expect(page.getByLabel("存储告警阈值（%）")).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "保存告警阈值", exact: true }),
  ).toBeDisabled();
  await expect(page.getByText(/本次结果或版本尚未确认/)).toBeVisible();
  await page
    .getByRole("button", { name: "重新读取存储设置", exact: true })
    .click();
  const currentThreshold = rootStorage.getByRole("cell", {
    name: "89% 版本 2",
    exact: true,
  });
  await expect(currentThreshold).toBeVisible();
  const afterConflict: StorageView = await readJSON(
    context,
    "/api/system/storage?instance=local-api&page=1&pageSize=20",
  );
  expect(afterConflict.storage[0]).toMatchObject({
    alarmPercent: 89,
    revision: 2,
  });
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
  ).toMatchObject({ alarmPercent: 89, revision: 2 });
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
  expectContainerHealthEvidence(healthy);
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
  await captureSystemViews(page, testInfo, "healthy", visuals, () =>
    expectHealthWorkspace(page, healthy),
  );

  // Persist system-only read authority through the actual role editor and use
  // a second browser with valid MFA to prove server denial of settings writes.
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic System Reader");
  await page
    .getByRole("checkbox", { name: "系统健康：读取", exact: true })
    .check();
  await fillSecret(page.getByLabel("操作者当前密码", { exact: true }), changed);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
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
      "directory_assets",
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
  await fillSecret(page.getByLabel("初始密码", { exact: true }), readerInitial);
  await fillSecret(
    page.getByLabel("确认初始密码", { exact: true }),
    readerInitial,
  );
  await page.getByLabel("角色", { exact: true }).fill(savedRole.roleID);
  await fillSecret(page.getByLabel("操作者当前密码", { exact: true }), changed);
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
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
    await navigateTo(reader, "系统健康");
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
          expectedRevision: 2,
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
    ).toMatchObject({ alarmPercent: 89, revision: 2 });
    await navigateTo(reader, "退出登录");
    await expect(
      reader.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/system/health")).status(),
    ).toBe(401);
  } finally {
    await readerContext.close();
  }
  await navigateTo(page, "系统健康");
  await page.getByRole("button", { name: "平台依赖健康", exact: true }).click();
  const beforePause: SystemHealth = await readJSON(
    context,
    "/api/system/health",
  );
  expect(beforePause.worker.availability).toBe("available");
  expect(beforePause.worker.cycles.map((c) => [c.workerId, c.cycle])).toEqual(
    healthy.worker.cycles.map((c) => [c.workerId, c.cycle]),
  );
  const expectedStoppedCycles = beforePause.worker.cycles.map(
    ({ workerId, cycle }) => ({
      workerId,
      cycle,
      availability: "unavailable",
      reason: "stale_worker_activity",
    }),
  );
  let workerPaused = false;
  let stopped!: SystemHealth;
  let recovered!: SystemHealth;
  let pausedAt = "";
  let resumedAt = "";
  try {
    // Pause only the exact owned synthetic process. The real clock and persisted
    // activity continue unchanged; no stale row or success fixture is injected.
    signalWorker("SIGSTOP");
    workerPaused = true;
    pausedAt = new Date().toISOString();
    await expect
      .poll(
        async () => {
          const { worker } = (await readJSON(
            context,
            "/api/system/health",
          )) as SystemHealth;
          // Each required cycle has an independent timestamp. Aggregate
          // unavailability alone does not prove every cycle became stale.
          return {
            availability: worker.availability,
            cycles: worker.cycles.map(
              ({ workerId, cycle, availability, reason }) => ({
                workerId,
                cycle,
                availability,
                reason,
              }),
            ),
          };
        },
        { timeout: 35_000, intervals: [2000] },
      )
      .toEqual({ availability: "unavailable", cycles: expectedStoppedCycles });
    stopped = await readJSON(context, "/api/system/health");
    expect(stopped.result).toBe("degraded");
    expect(stopped.dependencies.find((d) => d.id === "worker")?.status).toBe(
      "unhealthy",
    );
    for (const id of ["api", "postgresql", "sampler"])
      expect(stopped.dependencies.find((d) => d.id === id)?.status).toBe(
        "healthy",
      );
    expect(stopped.worker.cycles.map((c) => [c.workerId, c.cycle])).toEqual(
      beforePause.worker.cycles.map((c) => [c.workerId, c.cycle]),
    );
    for (const cycle of stopped.worker.cycles) {
      expect(cycle.availability).toBe("unavailable");
      expect(cycle.reason).toBe("stale_worker_activity");
      expect(cycle.lastActivityAt).not.toBeNull();
      expect(
        Date.parse(stopped.worker.checkedAt) -
          Date.parse(cycle.lastActivityAt!),
      ).toBeGreaterThan(stopped.worker.staleAfterSeconds * 1000);
    }
    await captureSystemViews(
      page,
      testInfo,
      "worker-stale",
      visuals,
      async () => {
        await expectHealthWorkspace(page, stopped);
        const finding = page
          .getByRole("region", { name: "依赖诊断工作台", exact: true })
          .locator(".system-findings > li")
          .filter({ hasText: "ADTR worker" });
        await expect(finding).toContainText("不健康");
        await expect(finding).toContainText("处理建议：");
        await expect(finding).toContainText("Worker");
        await expect(
          page
            .getByRole("table", { name: "Worker 循环观测", exact: true })
            .getByRole("cell", {
              name: "不可用 · stale_worker_activity",
              exact: true,
            }),
        ).toHaveCount(stopped.worker.cycles.length);
      },
    );

    signalWorker("SIGCONT");
    workerPaused = false;
    resumedAt = new Date().toISOString();
    await expect
      .poll(
        async () => {
          const next: SystemHealth = await readJSON(
            context,
            "/api/system/health",
          );
          if (
            next.result !== expectedHealthResult(next) ||
            ["api", "postgresql", "sampler", "worker"].some(
              (id) =>
                next.dependencies.find((d) => d.id === id)?.status !==
                "healthy",
            ) ||
            next.worker.availability !== "available"
          )
            return false;
          if (next.worker.cycles.length !== stopped.worker.cycles.length)
            return false;
          return stopped.worker.cycles.every((previous) => {
            const cycle = next.worker.cycles.find(
              (candidate) =>
                candidate.workerId === previous.workerId &&
                candidate.cycle === previous.cycle,
            );
            return (
              cycle?.availability === "available" &&
              cycle.status === "success" &&
              cycle.evidence === "completed_cycle" &&
              !!cycle.lastActivityAt &&
              !!cycle.lastSuccessAt &&
              Date.parse(cycle.lastActivityAt) >
                Date.parse(previous.lastActivityAt!) &&
              Date.parse(cycle.lastSuccessAt) >
                Date.parse(previous.lastSuccessAt!)
            );
          });
        },
        { timeout: 35_000, intervals: [1000, 2000] },
      )
      .toBe(true);
    recovered = await readJSON(context, "/api/system/health");
    expectContainerHealthEvidence(recovered);
    for (const id of ["api", "postgresql", "sampler", "worker"])
      expect(recovered.dependencies.find((d) => d.id === id)?.status).toBe(
        "healthy",
      );
    expect(recovered.worker.availability).toBe("available");
    expect(recovered.worker.cycles.map((c) => [c.workerId, c.cycle])).toEqual(
      stopped.worker.cycles.map((c) => [c.workerId, c.cycle]),
    );
    for (const cycle of recovered.worker.cycles) {
      const previous = stopped.worker.cycles.find(
        (item) =>
          item.workerId === cycle.workerId && item.cycle === cycle.cycle,
      )!;
      expect(cycle.availability).toBe("available");
      expect(cycle.status).toBe("success");
      expect(cycle.evidence).toBe("completed_cycle");
      expect(Date.parse(cycle.lastActivityAt!)).toBeGreaterThan(
        Date.parse(previous.lastActivityAt!),
      );
      expect(Date.parse(cycle.lastSuccessAt!)).toBeGreaterThan(
        Date.parse(previous.lastSuccessAt!),
      );
    }
    await captureSystemViews(page, testInfo, "recovered", visuals, async () => {
      await expectHealthWorkspace(page, recovered);
      await expect(
        page
          .getByRole("table", { name: "Worker 循环观测", exact: true })
          .getByRole("cell", { name: "可用", exact: true }),
      ).toHaveCount(recovered.worker.cycles.length);
    });
  } finally {
    // A failed assertion or screenshot must not leave the harness worker
    // suspended. Recheck its start tick before signalling to reject PID reuse.
    if (workerPaused) signalWorker("SIGCONT");
  }
  const browserStorage = await page.evaluate(() =>
    JSON.stringify({
      local: { ...localStorage },
      session: { ...sessionStorage },
    }),
  );
  for (const secret of [changed, mfa.secret, me.csrfToken])
    expect(
      browserStorage.includes(secret),
      "Browser storage contains no authentication proof",
    ).toBe(false);

  // Bind only allowlisted, non-secret result summaries and deliberate safe
  // screenshots to the exact checkout, sources and executable frontend bytes.
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const git = (...args: string[]) =>
    execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
  const hashFile = (file: string) =>
    createHash("sha256")
      .update(readFileSync(resolve(root, file)))
      .digest("hex");
  const checkoutCommit = git("rev-parse", "HEAD");
  const baseCommit =
    process.env.ADTR_EVENT_BASE_SHA ?? git("merge-base", "HEAD", "origin/main");
  const sourceFiles = [
    "web/e2e/system.spec.ts",
    "web/e2e/navigation.ts",
    "web/playwright.config.ts",
    "web/src/SystemWorkspace.tsx",
    "web/src/system-common.tsx",
    "web/src/system-api.ts",
    "web/src/system-soc.css",
    "web/src/App.tsx",
    "web/src/SocNavigation.tsx",
    "web/src/style.css",
    "web/src/soc-shell.css",
    "web/package-lock.json",
    "internal/auth/system_http.go",
    "internal/auth/system_types.go",
    "scripts/test_auth_e2e.py",
    ".github/workflows/ci.yml",
    ...readdirSync(resolve(root, "internal/systemhealth"))
      .filter((file) => file.endsWith(".go"))
      .map((file) => `internal/systemhealth/${file}`),
  ].sort();
  const frontendAssets = readdirSync(resolve(root, "web/dist/assets"), {
    withFileTypes: true,
  });
  expect(frontendAssets.length).toBeGreaterThan(0);
  expect(frontendAssets.every((entry) => entry.isFile())).toBe(true);
  const apiWorkerSHA256 = hashFile("bin/adtr");
  const runningWorkerSHA256 = createHash("sha256")
    .update(readFileSync(`/proc/${workerPID}/exe`))
    .digest("hex");
  expect(runningWorkerSHA256).toBe(apiWorkerSHA256);
  expect(visuals).toHaveLength(10);
  for (const state of [
    "healthy",
    "history",
    "container-memory",
    "worker-stale",
    "recovered",
  ])
    expect(visuals.filter((visual) => visual.state === state)).toHaveLength(2);
  const manifest = {
    formatVersion: 1,
    suite: "system",
    chain: "real browser → API → Linux sampler / owned worker → PostgreSQL",
    recordedAt: new Date().toISOString(),
    checkoutCommit,
    checkoutTree: git("rev-parse", "HEAD^{tree}"),
    checkoutParents: git("show", "-s", "--format=%P", "HEAD")
      .split(" ")
      .filter(Boolean),
    eventHeadCommit: process.env.ADTR_EVENT_HEAD_SHA ?? checkoutCommit,
    eventBaseCommit: baseCommit,
    dirty: git("status", "--porcelain") !== "",
    workflowCommit: process.env.GITHUB_SHA ?? null,
    workflowRun: process.env.GITHUB_RUN_ID ?? null,
    workflowAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    runtime: {
      node: process.version,
      browserVersion: context.browser()!.version(),
      nativeStreamMode: process.env.ADTR_E2E_NATIVE_STREAM_MODE ?? null,
      apiRevision: info.version.revision,
      apiModified: info.version.modified,
    },
    sourceSHA256: Object.fromEntries(
      sourceFiles.map((file) => [file, hashFile(file)]),
    ),
    artifactsSHA256: {
      apiWorker: apiWorkerSHA256,
      runningWorker: runningWorkerSHA256,
      frontendEntry: hashFile("web/dist/index.html"),
    },
    frontendAssetsSHA256: Object.fromEntries(
      frontendAssets
        .map((entry) => {
          const file = `web/dist/assets/${entry.name}`;
          return [file, hashFile(file)];
        })
        .sort(([left], [right]) => left.localeCompare(right)),
    ),
    actualAPI: {
      current: {
        checkedAt: current!.checkedAt,
        availability: current!.availability,
        stale: current!.stale,
        observedAt: sample.observedAt,
        cpuPercent: sample.cpu.percent,
        memoryPercent: sample.memory.percent,
        containerMemory: sample.containerMemory
          ? {
              observedAt: sample.containerMemory.observedAt,
              source: sample.containerMemory.source,
              scope: sample.containerMemory.scope,
              availability: sample.containerMemory.availability,
              reason: sample.containerMemory.reason ?? null,
              usageBytes: sample.containerMemory.usageBytes,
              limitBytes: sample.containerMemory.limitBytes,
              percent: sample.containerMemory.percent,
              limitState: sample.containerMemory.limitState,
              hierarchy: sample.containerMemory.hierarchy,
              sourceID: sample.containerMemory.sourceID ?? null,
            }
          : null,
        storageIDs: sample.storage.map(({ id }) => id),
      },
      history: {
        query: new URL(visibleHistoryResponse.url()).searchParams.toString(),
        graphType: visibleHistory.graphType,
        availableSince: visibleHistory.availableSince,
        sampleIntervalSeconds: visibleHistory.sampleIntervalSeconds,
        sampleCount: visibleHistory.info[0].data.timestamp.length,
        gapCount: visibleHistory.gaps.length,
        firstSample: visibleHistory.info[0].data.timestamp.at(0),
        lastSample: visibleHistory.info[0].data.timestamp.at(-1),
        statistics: visibleHistory.info[0].data.dataStatistics,
        responseSHA256: createHash("sha256")
          .update(JSON.stringify(visibleHistory))
          .digest("hex"),
      },
      containerHistory: {
        graphType: containerHistory.graphType,
        availableSince: containerHistory.availableSince,
        sampleIntervalSeconds: containerHistory.sampleIntervalSeconds,
        sampleCount: containerSeries.timestamp.length,
        gapCount: containerHistory.gaps.length,
        firstSample: containerSeries.timestamp.at(0) ?? null,
        lastSample: containerSeries.timestamp.at(-1) ?? null,
        statistics: containerSeries.dataStatistics,
        segments:
          containerHistory.segments?.map(
            ({ startTime, source, limitBytes, sourceID }) => ({
              startTime,
              source,
              limitBytes,
              sourceID: sourceID ?? null,
            }),
          ) ?? null,
        responseSHA256: createHash("sha256")
          .update(JSON.stringify(containerHistory))
          .digest("hex"),
      },
      storage: {
        initialRevision: initial.storage[0].revision,
        savedRevision: persisted.storage[0].revision,
        savedPercent: persisted.storage[0].alarmPercent,
        concurrentRevision: afterConflict.storage[0].revision,
        concurrentPercent: afterConflict.storage[0].alarmPercent,
        browserConflictStatus: browserConflict.status(),
        apiConflictStatus: conflict.status(),
        browserRereadConfirmed: true,
      },
      healthy: healthEvidence(healthy),
      beforePause: healthEvidence(beforePause),
      stopped: healthEvidence(stopped),
      recovered: healthEvidence(recovered),
    },
    workerInterruption: {
      pauseSignal: "SIGSTOP",
      resumeSignal: "SIGCONT",
      pausedAt,
      resumedAt,
      sameProcessAndCycles: true,
      allCyclesCompletedAfterResume: true,
    },
    checks: [
      "1440×900 and 390×844 kernel/worker-confirmed, history, container memory, stale worker and recovered views",
      "no document horizontal overflow in any captured view",
      "anomaly cards include actual evidence and a handling suggestion",
      "unconfigured capabilities remain separate and never show healthy status",
      "Linux sampler and persisted history statistics match actual API results",
      "owned worker activity becomes stale in real time and all cycles recover",
      "browser CAS conflict clears proofs and requires rereading revision 2",
      "system-only reader cannot change settings; logout removes API access",
    ],
    screenshots: visuals,
  };
  const manifestPath = testInfo.outputPath("system-soc-evidence.json");
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n");
  await testInfo.attach("system-soc-evidence", {
    path: manifestPath,
    contentType: "application/json",
  });
});
