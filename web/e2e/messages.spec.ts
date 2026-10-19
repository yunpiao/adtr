import { navigateTo } from "./navigation";
import { test, expect, type Page } from "@playwright/test";
import { createHash, createHmac } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

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

type Message = {
  messageId: string;
  taskUUID: string;
  type: string;
  subType: string;
  read: boolean;
  firstReadAt: string | null;
  occurredAt: string;
};
type Snapshot = {
  resultId: string;
  confirmedAt: string;
  expiresAt: string;
  items: Message[];
  total: number;
  offset: number;
  limit: number;
  counts: { all: number; unread: number; read: number };
};

// Read-only provenance observation. The owning harness created this disposable
// database. No SQL producer, message seeding, route fulfilment or fake response.
function projectedSources(container: string, taskIDs: string[]) {
  for (const id of taskIDs) expect(id).toMatch(/^[0-9a-f-]{36}$/);
  const sql = `SELECT COALESCE(json_agg(row_to_json(proof)), '[]'::json) FROM (
    SELECT m.message_id AS "messageId", m.source_task_id AS "taskUUID",
      m.source_event_id AS "eventId", o.id AS "outboxId", e.action, e.state,
      t.kind, m.recipient_user_id=t.actor_id AS "originalRecipient"
    FROM adtr.messages m JOIN adtr.task_events e ON e.id=m.source_event_id
      AND e.task_id=m.source_task_id
    JOIN adtr.task_outbox o ON o.event_id=e.id AND o.task_id=e.task_id
    JOIN adtr.tasks t ON t.task_id=m.source_task_id
    WHERE m.source_task_id IN (${taskIDs.map((id) => `'${id}'`).join(",")})
    ORDER BY m.source_task_id
  ) proof`;
  return JSON.parse(
    execFileSync(
      "docker",
      [
        "exec",
        "-e",
        "PGOPTIONS=-c default_transaction_read_only=on",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "adtr_e2e",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 10_000 },
    ),
  );
}

type ReadIntent = { resultId: string; messageId: string; operationId: string };
type ReadReceipt = ReadIntent & {
  firstReadAt: string;
  committedAt: string;
  progress: {
    target: number;
    processed: number;
    newlyRead: number;
    alreadyRead: number;
    failed: number;
    remaining: number;
  };
};

function persistedReadProof(container: string, operationId: string) {
  expect(operationId).toMatch(/^[A-Za-z0-9_-]{1,128}$/);
  const sql = `SELECT COALESCE(json_agg(row_to_json(proof)), '[]'::json) FROM (
    SELECT o.operation_id AS "operationId", o.result_id AS "resultId",
      o.message_id AS "messageId", o.newly_read AS "newlyRead",
      o.first_read_at=r.first_read_at AS "sameFirstReadAt",
      o.user_id=m.recipient_user_id AS "originalRecipient"
    FROM adtr.message_read_operations o JOIN adtr.message_reads r
      ON r.tenant_id=o.tenant_id AND r.user_id=o.user_id AND r.message_id=o.message_id
    JOIN adtr.messages m ON m.tenant_id=o.tenant_id AND m.recipient_user_id=o.user_id AND m.message_id=o.message_id
    WHERE o.operation_id='${operationId}'
  ) proof`;
  return JSON.parse(
    execFileSync(
      "docker",
      [
        "exec",
        "-e",
        "PGOPTIONS=-c default_transaction_read_only=on",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "adtr_e2e",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 10_000 },
    ),
  );
}

async function submitAccessMutation(page: Page, path: string, button: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === path && r.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status()).toBe(200);
  const value = await response.json();
  expect(value).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
  return value;
}

// Automatic artifacts during login/MFA would expose synthetic proof fields.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("real health Worker → outbox → message snapshot → browser page and locate", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(240_000);
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD,
    container = process.env.ADTR_E2E_DB_CONTAINER;
  if (!username || !password || !container)
    throw new Error(
      "messages requires its fresh synthetic bootstrap and owned PostgreSQL container",
    );
  const changed = "Synthetic Messages Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const mfa = await enrollMfa(page, changed);
  const taskIDs: string[] = [];
  for (let index = 0; index < 2; index++) {
    await navigateTo(page, "后台任务");
    await page
      .getByRole("button", { name: "提交健康检查", exact: true })
      .click();
    await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
    await page
      .getByLabel("未使用的认证器验证码", { exact: true })
      .fill(await freshCode(mfa));
    const [response] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/tasks/submit" &&
          r.request().method() === "POST",
      ),
      page
        .getByRole("button", { name: "确认提交健康检查", exact: true })
        .click(),
    ]);
    expect(response.status()).toBe(200);
    const submitted = await response.json();
    expect(submitted.replayed).toBe(false);
    taskIDs.push(submitted.task.taskUUID);
    await expect(
      page.locator('.task-state[data-state="succeeded"]'),
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.getByLabel("任务实际结果")).toContainText(
      '"database": "ready"',
    );
    await page
      .getByRole("button", { name: "返回任务列表", exact: true })
      .click();
  }
  await expect
    .poll(() => projectedSources(container, taskIDs).length, {
      timeout: 30_000,
    })
    .toBe(2);
  const sources = projectedSources(container, taskIDs);
  for (const source of sources)
    expect(source).toMatchObject({
      kind: "infrastructure.health",
      state: "succeeded",
      action: "finished",
      originalRecipient: true,
    });

  const requests: { method: string; path: string; query: string }[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/messages/"))
      requests.push({
        method: request.method(),
        path: url.pathname,
        query: url.search,
      });
  });
  await navigateTo(page, "站内消息");
  await expect(
    page.getByRole("heading", { name: "站内消息", exact: true }),
  ).toBeVisible();
  expect(requests).toEqual([]);
  await expect(page.getByLabel("阅读状态", { exact: true })).toHaveValue("all");
  await expect(page.getByLabel("排序", { exact: true })).toHaveValue("desc");
  await expect(page.getByLabel("每页条数", { exact: true })).toHaveValue("20");
  await expect(
    page.getByLabel("开始时间（UTC）", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("结束时间（UTC）", { exact: true }),
  ).toBeVisible();
  const confirm = page.getByRole("button", { name: "确认查询", exact: true });
  await confirm.focus();
  const [queryResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/messages/query" &&
        r.request().method() === "POST",
    ),
    page.keyboard.press("Enter"),
  ]);
  expect(queryResponse.status()).toBe(200);
  expect(queryResponse.headers()["cache-control"]).toBe("no-store");
  expect(queryResponse.request().postDataJSON()).toMatchObject({
    readFilter: "all",
    sort: "desc",
    offset: 0,
    limit: 20,
  });
  const snapshot: Snapshot = await queryResponse.json();
  expect(snapshot).toMatchObject({
    total: 2,
    offset: 0,
    limit: 20,
    counts: { all: 2, unread: 2, read: 0 },
  });
  expect(
    Date.parse(snapshot.expiresAt) - Date.parse(snapshot.confirmedAt),
  ).toBe(600_000);
  expect(snapshot.items.map((item) => item.taskUUID).sort()).toEqual(
    [...taskIDs].sort(),
  );
  expect(snapshot.items.map((item) => item.messageId).sort()).toEqual(
    sources.map((source: Message) => source.messageId).sort(),
  );
  for (const item of snapshot.items) {
    expect(item).toMatchObject({
      type: "task_result",
      subType: "succeeded",
      read: false,
    });
    const article = page.locator(
      `article[data-message-id="${item.messageId}"]`,
    );
    await expect(article).toContainText(item.taskUUID);
    await expect(article).toContainText(item.messageId);
  }
  await expect(
    page.getByRole("list", { name: "站内消息列表", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(snapshot.resultId, { exact: true }),
  ).toBeVisible();

  await expect(
    page.getByRole("region", { name: "查询时消息统计" }),
  ).toContainText("查询时未读2");
  const utc = (value: string) =>
    `${value.replace("T", " ").replace(/Z$/, "")} UTC`;
  await expect(page.locator(".message-snapshot")).toContainText(
    utc(snapshot.confirmedAt),
  );
  await expect(page.locator(".message-snapshot")).toContainText(
    utc(snapshot.expiresAt),
  );
  await expect(
    page.getByRole("button", { name: "上一页", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();

  // Exercise fixed-result pagination without making 21 slow MFA-gated writes.
  // Production UI retains its supported 20/50/100/200 page sizes; the API's
  // supported limit=1 proves two real members across the same fixed result.
  const pages = [];
  for (const offset of [0, 1]) {
    const response = await context.request.get(
      `/api/messages/page?resultId=${snapshot.resultId}&offset=${offset}&limit=1`,
    );
    expect(response.status()).toBe(200);
    expect(response.headers()["cache-control"]).toBe("no-store");
    const value: Snapshot = await response.json();
    expect(value.resultId).toBe(snapshot.resultId);
    expect(value.counts).toEqual(snapshot.counts);
    expect(value.items).toEqual([snapshot.items[offset]]);
    pages.push(value);
  }
  // Rejected POST requests exercise real Origin/CSRF boundaries without allocating snapshots.
  const missingOrigin = await context.request.post("/api/messages/query", {
    data: queryResponse.request().postDataJSON(),
  });
  expect(missingOrigin.status()).toBe(403);
  const missingCSRF = await context.request.post("/api/messages/query", {
    headers: { Origin: new URL(page.url()).origin },
    data: queryResponse.request().postDataJSON(),
  });
  expect(missingCSRF.status()).toBe(403);
  const missingMember = await context.request.get(
    `/api/messages/locate?resultId=${snapshot.resultId}&messageId=health-not-a-member&limit=20`,
  );
  expect(missingMember.status()).toBe(200);
  expect(await missingMember.json()).toEqual({
    resultId: snapshot.resultId,
    found: false,
    index: -1,
    page: 0,
  });
  // Editing a draft cannot create a snapshot, replace members, or change locate.
  await page
    .getByLabel("关键词", { exact: true })
    .fill("unapplied-synthetic-draft");
  await page
    .getByLabel("消息 ID 定位", { exact: true })
    .fill(snapshot.items[1].messageId);
  const [locateResponse, pageResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/messages/locate" &&
        r.request().method() === "GET",
    ),
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/messages/page" &&
        r.request().method() === "GET",
    ),
    page.getByRole("button", { name: "定位", exact: true }).click(),
  ]);
  expect(locateResponse.status()).toBe(200);
  expect(await locateResponse.json()).toMatchObject({
    resultId: snapshot.resultId,
    found: true,
    index: 1,
  });
  expect(new URL(pageResponse.url()).searchParams.get("resultId")).toBe(
    snapshot.resultId,
  );
  expect(pageResponse.status()).toBe(200);
  expect((await pageResponse.json()).items).toEqual(snapshot.items);
  await expect(
    page.locator(`article[data-message-id="${snapshot.items[1].messageId}"]`),
  ).toBeFocused();
  await page.getByLabel("关键词", { exact: true }).fill("");

  // Derive date boundaries independently from the real accepted API rows.
  const timelineDates = snapshot.items.flatMap((item, index) => {
    const date = item.occurredAt.slice(0, 10);
    return index === 0 || snapshot.items[index - 1].occurredAt.slice(0, 10) !== date
      ? [date] : [];
  });
  const timeline = page.getByRole("region", { name: "当前页时间线", exact: true });
  await expect(timeline).toContainText("不代表全天消息汇总");
  await expect(timeline.getByRole("heading", { name: /^发生日期（UTC）/ }))
    .toHaveText(timelineDates.map((date) => `发生日期（UTC） · ${date}`));
  const messageRows = timeline.getByRole("article");
  await expect(messageRows).toHaveCount(snapshot.items.length);
  for (let index = 0; index < snapshot.items.length; index++) {
    await expect(messageRows.nth(index)).toHaveAttribute("data-message-id", snapshot.items[index].messageId);
    await expect(messageRows.nth(index).locator(".message-row-number"))
      .toHaveText(String(snapshot.offset + index + 1).padStart(2, "0"));
  }
  const screenshots = [];
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    const heading = page.locator(".message-heading");
    const boundary = page.locator(".message-boundary");
    const headingBox = await heading.boundingBox(),
      boundaryBox = await boundary.boundingBox();
    expect(headingBox).not.toBeNull();
    expect(boundaryBox).not.toBeNull();
    expect(headingBox!.y + headingBox!.height).toBeLessThanOrEqual(
      boundaryBox!.y,
    );
    for (const child of await heading.locator(":scope > *").all()) {
      const box = await child.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.y + box!.height).toBeLessThanOrEqual(
        headingBox!.y + headingBox!.height + 1,
      );
    }

    await page
      .locator(`article[data-message-id="${snapshot.items[1].messageId}"]`)
      .scrollIntoViewIfNeeded();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await expect(
      page.locator(`article[data-message-id="${snapshot.items[1].messageId}"]`),
    ).toBeVisible();
    for (const fullPage of [false, true]) {
      // Preserve the located viewport, then reset document scroll before the
      // full-page image so sticky/fixed chrome is not painted mid-document.
      if (fullPage) {
        await page.evaluate(() => window.scrollTo(0, 0));
        await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
      }
      const file = `messages-soc-${viewport.width}-${fullPage ? "full" : "viewport"}.png`;
      const path = testInfo.outputPath(file);
      const bytes = await page.screenshot({ path, fullPage });
      await testInfo.attach(file, { path, contentType: "image/png" });
      screenshots.push({
        file,
        viewport,
        fullPage,
        sha256: createHash("sha256").update(bytes).digest("hex"),
      });
    }
  }
  // Additional timeline-focused viewports make the new date heading inspectable.
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await timeline.locator(".message-day-heading").first().scrollIntoViewIfNeeded();
    await expect(timeline.locator(".message-day-heading").first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const file = `messages-soc-timeline-${viewport.width}.png`;
    const path = testInfo.outputPath(file);
    const bytes = await page.screenshot({ path, fullPage: false });
    await testInfo.attach(file, { path, contentType: "image/png" });
    screenshots.push({ file, viewport, fullPage: false, sha256: createHash("sha256").update(bytes).digest("hex") });
  }
  // No auto-refresh, polling, read mutation, or navigation-created POST query.
  expect(requests.filter((r) => r.method === "POST")).toEqual([
    { method: "POST", path: "/api/messages/query", query: "" },
  ]);
  expect(
    requests.every((r) =>
      [
        "/api/messages/query",
        "/api/messages/page",
        "/api/messages/locate",
      ].includes(r.path),
    ),
  ).toBe(true);
  // All original read-only assertions above stay intact. Fault injection below
  // drops transport only: it never fabricates a response or a message producer.
  await page.setViewportSize({ width: 1440, height: 900 });
  const operationPanel = page.getByRole("region", {
    name: "单条已读操作",
    exact: true,
  });
  const savedInputs = () =>
    page.evaluate(() =>
      Object.keys(localStorage)
        .filter((key) => key.startsWith("adtr.message-read.v1."))
        .map((key) => JSON.parse(localStorage.getItem(key)!).input),
    );
  const readRequests: ReadIntent[] = [];
  page.on("request", (request) => {
    if (
      new URL(request.url()).pathname === "/api/messages/read" &&
      request.method() === "POST"
    )
      readRequests.push(request.postDataJSON());
  });
  const receiptURL = (id: string) =>
    `/api/messages/read-operation?operationId=${encodeURIComponent(id)}`;
  const expectReceipt = (value: ReadReceipt, intent: ReadIntent) => {
    expect(value).toMatchObject({
      ...intent,
      progress: {
        target: 1,
        processed: 1,
        newlyRead: 1,
        alreadyRead: 0,
        failed: 0,
        remaining: 0,
      },
    });
    expect(Number.isFinite(Date.parse(value.firstReadAt))).toBe(true);
    expect(Number.isFinite(Date.parse(value.committedAt))).toBe(true);
  };
  const captureReadState = async (state: string) => {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      await operationPanel
        .locator(".message-read-operation")
        .last()
        .scrollIntoViewIfNeeded();
      await expect(operationPanel).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      for (const fullPage of [false, true]) {
        if (fullPage) {
          await page.evaluate(() => window.scrollTo(0, 0));
          await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
        }
        const file = `messages-soc-read-${state}-${viewport.width}-${fullPage ? "full" : "viewport"}.png`;
        const path = testInfo.outputPath(file);
        const bytes = await page.screenshot({ path, fullPage });
        await testInfo.attach(file, { path, contentType: "image/png" });
        screenshots.push({
          file,
          viewport,
          fullPage,
          sha256: createHash("sha256").update(bytes).digest("hex"),
        });
      }
    }
    await page.setViewportSize({ width: 1440, height: 900 });
  };
  const assertOldSnapshot = async () => {
    const response = await context.request.get(
      `/api/messages/page?resultId=${snapshot.resultId}&offset=0&limit=20`,
    );
    expect(response.status()).toBe(200);
    expect(await response.json()).toEqual(snapshot);
    await expect(
      page.getByRole("region", { name: "查询时消息统计" }),
    ).toContainText("查询时未读2");
  };

  // First message: request never reaches the server. A 404 receipt does not
  // authorize changing the intent; the explicit replay must use all three IDs.
  let releaseFirst!: () => void;
  const firstBlocked = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  let firstCaptured!: (value: ReadIntent) => void;
  const firstIntentPromise = new Promise<ReadIntent>((resolve) => {
    firstCaptured = resolve;
  });
  await page.route(
    "**/api/messages/read",
    async (route) => {
      const input = route.request().postDataJSON();
      expect(await savedInputs()).toContainEqual(input);
      firstCaptured(input);
      await firstBlocked;
      await route.abort("failed");
    },
    { times: 1 },
  );
  const firstButton = page
    .locator(`article[data-message-id="${snapshot.items[0].messageId}"]`)
    .getByRole("button", { name: "标记为已读", exact: true });
  await firstButton.dblclick();
  const firstIntent = await firstIntentPromise;
  expect(firstIntent).toEqual({
    resultId: snapshot.resultId,
    messageId: snapshot.items[0].messageId,
    operationId: expect.stringMatching(/^[A-Za-z0-9_-]{1,128}$/),
  });
  expect(readRequests).toEqual([firstIntent]);
  releaseFirst();
  await expect(
    operationPanel.getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  await expect(
    operationPanel.getByRole("button", { name: "重放原操作", exact: true }),
  ).toHaveCount(0);
  const [missingReceipt] = await Promise.all([
    page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/api/messages/read-operation",
    ),
    operationPanel
      .getByRole("button", { name: "查询原操作结果", exact: true })
      .click(),
  ]);
  expect(missingReceipt.status()).toBe(404);
  expect(persistedReadProof(container, firstIntent.operationId)).toEqual([]);
  const [replayedResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/messages/read" &&
        r.request().method() === "POST",
    ),
    operationPanel
      .getByRole("button", { name: "重放原操作", exact: true })
      .click(),
  ]);
  expect(replayedResponse.status()).toBe(200);
  expect(replayedResponse.headers()["cache-control"]).toBe("no-store");
  expect(replayedResponse.request().postDataJSON()).toEqual(firstIntent);
  const firstReceipt: ReadReceipt = await replayedResponse.json();
  expectReceipt(firstReceipt, firstIntent);
  await expect(operationPanel).toContainText("已确认单条已读");
  await assertOldSnapshot();
  expect(readRequests).toEqual([firstIntent, firstIntent]);

  // Second message: actually commit at the server, then discard the HTTP
  // response. This is a real ambiguous outcome, not a synthetic success body.
  let secondCommitted!: (value: {
    intent: ReadIntent;
    receipt: ReadReceipt;
  }) => void;
  const secondCommit = new Promise<{
    intent: ReadIntent;
    receipt: ReadReceipt;
  }>((resolve) => {
    secondCommitted = resolve;
  });
  await page.route(
    "**/api/messages/read",
    async (route) => {
      const intent: ReadIntent = route.request().postDataJSON();
      expect(await savedInputs()).toContainEqual(intent);
      const response = await route.fetch({ maxRetries: 0 });
      expect(response.status()).toBe(200);
      expect(response.headers()["cache-control"]).toBe("no-store");
      const receipt: ReadReceipt = await response.json();
      expectReceipt(receipt, intent);
      secondCommitted({ intent, receipt });
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page
    .locator(`article[data-message-id="${snapshot.items[1].messageId}"]`)
    .getByRole("button", { name: "标记为已读", exact: true })
    .dblclick();
  const { intent: secondIntent, receipt: committedReceipt } =
    await secondCommit;
  expect(secondIntent.resultId).toBe(snapshot.resultId);
  expect(secondIntent.messageId).toBe(snapshot.items[1].messageId);
  expect(secondIntent.operationId).not.toBe(firstIntent.operationId);
  await expect(
    operationPanel.getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  await assertOldSnapshot();
  await captureReadState("unknown");
  expect(await savedInputs()).toEqual([secondIntent]);
  const mutationCount = readRequests.length;
  expect(mutationCount).toBe(3);
  await navigateTo(page, "账户概览");
  await page.goBack();
  await expect(
    operationPanel.getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  await page.reload();
  await expect(
    operationPanel.getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  expect(readRequests).toHaveLength(mutationCount);

  const recoveryTab = await context.newPage();
  const tabWrites: string[] = [];
  recoveryTab.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname.startsWith("/api/messages/")
    )
      tabWrites.push(request.url());
  });
  await recoveryTab.goto(page.url());
  await expect(
    recoveryTab
      .getByRole("region", { name: "单条已读操作", exact: true })
      .getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  expect(tabWrites).toEqual([]);
  await recoveryTab.close();
  expect(await savedInputs()).toEqual([secondIntent]);

  // Logout revokes the old cookie, while a newly authenticated session for the
  // same actor can recover the original committed receipt, not the old result.
  const oldState = await context.storageState();
  const oldProfile = await context.request.get("/api/auth/me");
  expect(oldProfile.status()).toBe(200);
  const oldCSRF = (await oldProfile.json()).csrfToken;
  await navigateTo(page, "退出登录");
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await expect(operationPanel).toHaveCount(0);
  const oldContext = await context.browser()!.newContext({
    baseURL: new URL(page.url()).origin,
    storageState: oldState,
  });
  try {
    const denied = await oldContext.request.get(
      receiptURL(secondIntent.operationId),
    );
    expect(denied.status()).toBe(401);
  } finally {
    await oldContext.close();
  }
  await login(page, username, changed);
  await page
    .getByLabel("二次认证验证码", { exact: true })
    .fill(await freshCode(mfa));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  const newProfile = await context.request.get("/api/auth/me");
  expect(newProfile.status()).toBe(200);
  const newCSRF = (await newProfile.json()).csrfToken;
  expect(newCSRF).not.toBe(oldCSRF);
  await navigateTo(page, "站内消息");
  await expect(
    operationPanel.getByRole("button", { name: "查询原操作结果", exact: true }),
  ).toBeEnabled();
  expect(readRequests).toHaveLength(mutationCount);
  const [recoveredResponse] = await Promise.all([
    page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/api/messages/read-operation",
    ),
    operationPanel
      .getByRole("button", { name: "查询原操作结果", exact: true })
      .click(),
  ]);
  expect(recoveredResponse.status()).toBe(200);
  expect(recoveredResponse.headers()["cache-control"]).toBe("no-store");
  expect(await recoveredResponse.json()).toEqual(committedReceipt);
  await expect(operationPanel).toContainText("已确认单条已读");
  expect(readRequests).toHaveLength(mutationCount);
  await captureReadState("confirmed");
  const expiredSessionResult = await context.request.get(
    `/api/messages/page?resultId=${snapshot.resultId}&offset=0&limit=20`,
  );
  expect(expiredSessionResult.status()).toBe(410);
  const replayConfirmed = await context.request.post("/api/messages/read", {
    headers: { Origin: new URL(page.url()).origin, "X-CSRF-Token": newCSRF },
    data: secondIntent,
  });
  expect(replayConfirmed.status()).toBe(200);
  expect(await replayConfirmed.json()).toEqual(committedReceipt);
  expect(await savedInputs()).toEqual([]);
  expect(
    requests.filter(
      (r) => r.method === "POST" && r.path === "/api/messages/query",
    ),
  ).toHaveLength(1);
  const [refreshedResponse] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/messages/query" &&
        r.request().method() === "POST",
    ),
    page.getByRole("button", { name: "确认查询", exact: true }).click(),
  ]);
  expect(refreshedResponse.status()).toBe(200);
  const refreshed: Snapshot = await refreshedResponse.json();
  expect(refreshed.resultId).not.toBe(snapshot.resultId);
  expect(refreshed.counts).toEqual({ all: 2, unread: 0, read: 2 });
  expect(refreshed.total).toBe(2);
  for (const receipt of [firstReceipt, committedReceipt]) {
    expect(
      refreshed.items.find((item) => item.messageId === receipt.messageId),
    ).toMatchObject({ read: true, firstReadAt: receipt.firstReadAt });
    expect(persistedReadProof(container, receipt.operationId)).toEqual([
      {
        operationId: receipt.operationId,
        resultId: receipt.resultId,
        messageId: receipt.messageId,
        newlyRead: true,
        sameFirstReadAt: true,
        originalRecipient: true,
      },
    ]);
  }
  await expect(
    page.getByRole("region", { name: "查询时消息统计" }),
  ).toContainText("查询时未读0");
  await expect(
    page.getByRole("button", { name: "标记为已读", exact: true }),
  ).toHaveCount(0);

  // Another real user has tasks.readable but no task-write permission or MFA.
  // Its 404 proves receipt ownership rather than a permission-denied shortcut.
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic Message Reader");
  await page
    .getByRole("checkbox", { name: "后台任务：读取", exact: true })
    .check();
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  const role = await submitAccessMutation(
    page,
    "/api/access/roles/save",
    "保存角色",
  );
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  const readerName = "synthetic.message.reader",
    readerInitial = "Initial Message Reader 123",
    readerChanged = "Changed Message Reader 456";
  await page.getByLabel("用户名", { exact: true }).fill(readerName);
  await page.getByLabel("初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("确认初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("角色", { exact: true }).fill(role.roleID);
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(mfa));
  await submitAccessMutation(page, "/api/access/users/create", "创建用户");
  const readerContext = await context.browser()!.newContext({
    baseURL: new URL(page.url()).origin,
    storageState: { cookies: [], origins: oldState.origins },
  });
  try {
    const reader = await readerContext.newPage();
    await reader.goto("/");
    await login(reader, readerName, readerInitial);
    await changeInitialPassword(reader, readerInitial, readerChanged);
    await navigateTo(reader, "站内消息");
    await expect(
      reader.getByRole("region", { name: "单条已读操作", exact: true }),
    ).toHaveCount(0);
    const privateReceipt = await readerContext.request.get(
      receiptURL(secondIntent.operationId),
    );
    expect(privateReceipt.status()).toBe(404);
    const [ownQuery] = await Promise.all([
      reader.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/messages/query" &&
          r.request().method() === "POST",
      ),
      reader.getByRole("button", { name: "确认查询", exact: true }).click(),
    ]);
    expect(ownQuery.status()).toBe(200);
    expect(await ownQuery.json()).toMatchObject({
      total: 0,
      items: [],
      counts: { all: 0, unread: 0, read: 0 },
    });
    const privateSnapshot = await readerContext.request.get(
      `/api/messages/page?resultId=${refreshed.resultId}&offset=0&limit=20`,
    );
    expect(privateSnapshot.status()).toBe(404);
    const readerProfile = await readerContext.request.get("/api/auth/me");
    expect(readerProfile.status()).toBe(200);
    const readerCSRF = (await readerProfile.json()).csrfToken;
    const privateReplay = await readerContext.request.post(
      "/api/messages/read",
      {
        headers: {
          Origin: new URL(reader.url()).origin,
          "X-CSRF-Token": readerCSRF,
        },
        data: secondIntent,
      },
    );
    expect(privateReplay.status()).toBe(404);
  } finally {
    await readerContext.close();
  }
  const readRecovery = {
    firstIntent,
    firstReceipt,
    secondIntent,
    committedReceipt,
    refreshed,
    browserMutationCount: mutationCount,
    recoveredInNewSession: true,
    oldCookieStatus: 401,
    oldSnapshotNewSessionStatus: 410,
    otherUserReceiptStatus: 404,
    otherUserSnapshotStatus: 404,
    persisted: [
      persistedReadProof(container, firstIntent.operationId),
      persistedReadProof(container, secondIntent.operationId),
    ],
  };

  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const hashFile = (file: string) =>
    createHash("sha256")
      .update(readFileSync(resolve(root, file)))
      .digest("hex");
  const manifest = {
    formatVersion: 1,
    suite: "messages",
    chain:
      "real browser → API → Worker → PostgreSQL outbox → message projection → fixed snapshot → read transport loss → receipt recovery → explicit refreshed snapshot",
    recordedAt: new Date().toISOString(),
    checkoutCommit: execFileSync("git", ["rev-parse", "HEAD"], {
      encoding: "utf8",
    }).trim(),
    dirty:
      execFileSync("git", ["status", "--porcelain"], {
        encoding: "utf8",
      }).trim() !== "",
    workflowCommit: process.env.GITHUB_SHA ?? null,
    workflowRun: process.env.GITHUB_RUN_ID ?? null,
    workflowAttempt: process.env.GITHUB_RUN_ATTEMPT ?? null,
    browser: context.browser()!.version(),
    sources,
    snapshot,
    timeline: {
      scope: "accepted current page; adjacent occurredAt UTC dates; original order",
      dates: timelineDates,
      messageIds: snapshot.items.map((item) => item.messageId),
      realDistinctDateCount: new Set(snapshot.items.map((item) => item.occurredAt.slice(0, 10))).size,
      limitation: "real health messages may share one day; synthetic multi-day DOM tests are not real cross-day producer evidence",
    },
    pages,
    browserRequests: requests,
    readRecovery,
    screenshots,
    sourceSHA256: Object.fromEntries(
      [
        "web/e2e/messages.spec.ts",
        "web/src/MessagesWorkspace.tsx",
        "web/src/message-timeline.ts",
        "web/src/message-api.ts",
        "web/src/message-query.ts",
        "web/src/message-read-intent.ts",
        "web/src/message-read.tsx",
        "web/src/message-soc.css",
        "web/package-lock.json",
        "scripts/test_auth_e2e.py",
        ".github/workflows/ci.yml",
      ].map((file) => [file, hashFile(file)]),
    ),
    artifactsSHA256: {
      apiWorker: hashFile("bin/adtr"),
      frontendEntry: hashFile("web/dist/index.html"),
    },
  };
  const manifestPath = testInfo.outputPath("messages-soc-evidence.json");
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n");
  await testInfo.attach("messages-soc-evidence", {
    path: manifestPath,
    contentType: "application/json",
  });
});
