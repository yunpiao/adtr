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
      "real browser → API → Worker → PostgreSQL outbox → message projection → browser snapshot/page/locate",
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
    pages,
    browserRequests: requests,
    screenshots,
    sourceSHA256: Object.fromEntries(
      [
        "web/e2e/messages.spec.ts",
        "web/src/MessagesWorkspace.tsx",
        "web/src/message-api.ts",
        "web/src/message-query.ts",
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
