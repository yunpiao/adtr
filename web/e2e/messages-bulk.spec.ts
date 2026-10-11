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

type Snapshot = {
  resultId: string;
  total: number;
  counts: { all: number; read: number; unread: number };
  items: { messageId: string; taskUUID: string; read: boolean }[];
  [key: string]: unknown;
};
type Input = {
  resultId: string;
  operationId: string;
  selection: string;
  messageIds?: string[];
};
type Operation = {
  receipt: {
    operationId: string;
    resultId: string;
    selection: string;
    target: number;
  };
  state: string;
  progress: {
    target: number;
    processed: number;
    newlyRead: number;
    alreadyRead: number;
    failed: number;
    remaining: number;
  };
};
function conserved(op: Operation) {
  expect(op.progress.target).toBe(
    op.progress.processed + op.progress.remaining,
  );
  expect(op.progress.processed).toBe(
    op.progress.newlyRead + op.progress.alreadyRead,
  );
  expect(op.progress.failed).toBe(0);
}
function sql(container: string, query: string) {
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
        "-At",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        query,
      ],
      { encoding: "utf8", timeout: 10000 },
    ),
  );
}
function proof(container: string, operationId: string) {
  expect(operationId).toMatch(/^[A-Za-z0-9_-]{1,128}$/);
  return sql(
    container,
    `SELECT row_to_json(p) FROM (SELECT o.state,o.target,o.processed,o.newly_read,o.already_read,
    (SELECT count(*) FROM adtr.message_bulk_targets t WHERE t.operation_id=o.operation_id) AS targets,
    (SELECT count(*) FROM adtr.message_bulk_receipts r WHERE r.operation_id=o.operation_id) AS receipts,
    (SELECT count(*) FROM adtr.message_bulk_receipts r JOIN adtr.message_bulk_targets t USING(tenant_id,user_id,operation_id,ordinal)
      JOIN adtr.message_reads m ON m.tenant_id=t.tenant_id AND m.user_id=t.user_id AND m.message_id=t.message_id
      WHERE r.operation_id=o.operation_id AND r.first_read_at=m.first_read_at) AS preserved
    FROM adtr.message_bulk_operations o WHERE o.operation_id='${operationId}') p`,
  );
}
// No automatic authentication screenshots or proof-bearing traces.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("real internally submitted health bulk: cross-page freeze, lost response, cancellation, sessions and ownership", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(420000);
  const username = process.env.ADTR_E2E_USERNAME!,
    password = process.env.ADTR_E2E_PASSWORD!;
  const container = process.env.ADTR_E2E_DB_CONTAINER!,
    fixture = process.env.ADTR_E2E_MESSAGE_FIXTURE_BINARY!;
  const worker = Number(process.env.ADTR_E2E_WORKER_PID),
    harness = Number(process.env.ADTR_E2E_HARNESS_PID);
  if (
    !username ||
    !password ||
    !container ||
    !fixture ||
    !Number.isInteger(worker) ||
    worker <= 1 ||
    !Number.isInteger(harness) ||
    harness <= 1
  )
    throw new Error("owned bulk harness required");
  const changed = "Synthetic Bulk Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  // Production authorizer checks the now-valid actor. This is internal SubmitTx,
  // not HTTP submission; the unchanged messages.spec owns two HTTP/MFA chains.
  const taskIDs: string[] = JSON.parse(
    execFileSync(fixture, ["seed"], { encoding: "utf8", timeout: 40000 }),
  );
  expect(taskIDs).toHaveLength(61);
  const sourceProof = () =>
    sql(
      container,
      `SELECT COALESCE(json_agg(row_to_json(p)), '[]'::json) FROM (
    SELECT m.message_id AS "messageId",t.task_id AS "taskUUID",t.kind,e.state,e.action,
      m.recipient_user_id=t.actor_id AS "originalRecipient",o.id AS "outboxId"
    FROM adtr.messages m JOIN adtr.tasks t ON t.task_id=m.source_task_id
    JOIN adtr.task_events e ON e.id=m.source_event_id AND e.task_id=t.task_id
    JOIN adtr.task_outbox o ON o.event_id=e.id AND o.task_id=t.task_id
    WHERE t.idempotency_key LIKE 'browser-bulk-health-%' ORDER BY t.task_id) p`,
    );
  await expect.poll(() => sourceProof().length, { timeout: 120000 }).toBe(61);
  const sources = sourceProof();
  expect(sources.map((v: { taskUUID: string }) => v.taskUUID).sort()).toEqual(
    [...taskIDs].sort(),
  );
  for (const source of sources)
    expect(source).toMatchObject({
      kind: "infrastructure.health",
      state: "succeeded",
      action: "finished",
      originalRecipient: true,
    });
  const mfa = await enrollMfa(page, changed);
  await navigateTo(page, "站内消息");
  const query = async () => {
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/messages/query" &&
          r.request().method() === "POST",
      ),
      page.getByRole("button", { name: "确认查询", exact: true }).click(),
    ]);
    expect(r.status()).toBe(200);
    return (await r.json()) as Snapshot;
  };
  const snapshot = await query();
  expect(snapshot.total).toBe(61);
  expect(snapshot.counts).toEqual({ all: 61, read: 0, unread: 61 });
  expect(snapshot.items).toHaveLength(20);
  const oldSnapshot = async () => {
    const r = await context.request.get(
      `/api/messages/page?resultId=${snapshot.resultId}&offset=0&limit=20`,
    );
    expect(r.status()).toBe(200);
    expect(await r.json()).toEqual(snapshot);
  };
  const starts: Input[] = [];
  page.on("request", (r) => {
    if (
      new URL(r.url()).pathname === "/api/messages/bulk-read" &&
      r.method() === "POST"
    )
      starts.push(r.postDataJSON());
  });
  const region = page.getByRole("region", {
    name: "批量已读操作",
    exact: true,
  });
  const panel = (id: string) => region.locator(`[data-bulk-operation="${id}"]`);
  const lookup = async (id: string) => {
    const r = await context.request.get(
      `/api/messages/bulk-read-operation?operationId=${id}`,
    );
    expect(r.status()).toBe(200);
    expect(r.headers()["cache-control"]).toBe("no-store");
    const op: Operation = await r.json();
    conserved(op);
    return op;
  };
  const start = async () => {
    await page
      .getByRole("button", { name: "预览批量目标", exact: true })
      .click();
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/messages/bulk-read" &&
          r.request().method() === "POST",
      ),
      page
        .getByRole("button", { name: "确认启动批量已读", exact: true })
        .click(),
    ]);
    expect(r.status()).toBe(200);
    const op: Operation = await r.json();
    conserved(op);
    return op;
  };
  const firstID = snapshot.items[0].messageId;
  await page
    .getByRole("checkbox", { name: `选择消息 ${firstID}`, exact: true })
    .check();
  const [secondPageResponse] = await Promise.all([
    page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/api/messages/page",
    ),
    page.getByRole("button", { name: "下一页", exact: true }).click(),
  ]);
  const secondPage: Snapshot = await secondPageResponse.json();
  expect(secondPage.resultId).toBe(snapshot.resultId);
  const secondID = secondPage.items[0].messageId;
  await page
    .getByRole("checkbox", { name: `选择消息 ${secondID}`, exact: true })
    .check();
  await page.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(
    page.getByRole("checkbox", { name: `选择消息 ${firstID}`, exact: true }),
  ).toBeChecked();
  const selected = await start();
  expect(selected.receipt.target).toBe(2);
  expect(starts[0]).toMatchObject({
    resultId: snapshot.resultId,
    selection: "selected",
  });
  expect(starts[0].messageIds!.sort()).toEqual([firstID, secondID].sort());
  await expect
    .poll(async () => (await lookup(selected.receipt.operationId)).state, {
      timeout: 30000,
    })
    .toBe("completed");
  const selectedDone = await lookup(selected.receipt.operationId);
  expect(selectedDone.progress).toMatchObject({
    processed: 2,
    newlyRead: 2,
    alreadyRead: 0,
    remaining: 0,
  });
  await oldSnapshot();
  // Stop only this harness's own worker, with a bounded finally resume. No SQL
  // producer or fabricated bulk response: the helper invokes production BulkOnce.
  const status = readFileSync(`/proc/${worker}/status`, "utf8");
  expect(Number(status.match(/^PPid:\s+(\d+)/m)![1])).toBe(harness);
  expect(readFileSync(`/proc/${worker}/cmdline`, "utf8").split("\0")).toEqual(
    expect.arrayContaining(["./bin/adtr", "-mode", "worker"]),
  );
  const stopWorker = async () => {
    try {
      await expect
        .poll(
          () => {
            process.kill(worker, "SIGSTOP");
            const idle = sql(
              container,
              `SELECT to_json(NOT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND application_name='adtr-message-bulk-worker' AND xact_start IS NOT NULL))`,
            );
            if (!idle) process.kill(worker, "SIGCONT");
            return idle;
          },
          { timeout: 10000 },
        )
        .toBe(true);
    } catch (error) {
      process.kill(worker, "SIGCONT");
      throw error;
    }
  };
  let stopped = false;
  let allInput!: Input, accepted!: Operation, cancelled!: Operation;
  const screenshots: {
    file: string;
    viewport: { width: number; height: number };
    sha256: string;
    fullPage: boolean;
  }[] = [];
  const capture = async (state: string) => {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      await panel(allInput.operationId).scrollIntoViewIfNeeded();
      await expect(panel(allInput.operationId)).toBeVisible();
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
        const file = `messages-soc-bulk-${state}-${viewport.width}-${fullPage ? "full" : "viewport"}.png`,
          path = testInfo.outputPath(file);
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
  try {
    await stopWorker();
    stopped = true;
    await page
      .getByRole("button", { name: "选择固定结果全部", exact: true })
      .click();
    await page
      .getByRole("button", { name: "预览批量目标", exact: true })
      .click();
    await expect(
      page.getByRole("region", { name: "批量目标预览", exact: true }),
    ).toContainText("61 条");
    let committed!: (v: Operation) => void;
    const commit = new Promise<Operation>((resolve) => (committed = resolve));
    await page.route(
      "**/api/messages/bulk-read",
      async (route) => {
        allInput = route.request().postDataJSON();
        const saved = await page.evaluate(() =>
          Object.keys(localStorage)
            .filter((k) => k.startsWith("adtr.message-bulk.v1."))
            .map((k) => JSON.parse(localStorage.getItem(k)!).intent.input),
        );
        expect(saved).toContainEqual(allInput);
        const response = await route.fetch({ maxRetries: 0 });
        expect(response.status()).toBe(200);
        const value = await response.json();
        await route.abort("failed");
        committed(value);
      },
      { times: 1 },
    );
    await page
      .getByRole("button", { name: "确认启动批量已读", exact: true })
      .dblclick();
    accepted = await commit;
    expect(allInput).toEqual({
      resultId: snapshot.resultId,
      operationId: accepted.receipt.operationId,
      selection: "all",
    });
    expect(accepted.progress).toEqual({
      target: 61,
      processed: 0,
      newlyRead: 0,
      alreadyRead: 0,
      failed: 0,
      remaining: 61,
    });
    await expect(panel(allInput.operationId)).toBeVisible();
    await page.reload();
    await expect(panel(allInput.operationId)).toContainText(
      allInput.operationId,
    );
    expect(starts).toHaveLength(2);
    expect(await lookup(allInput.operationId)).toEqual(accepted);
    await expect(panel(allInput.operationId)).toContainText("已接受，等待处理");
    await expect(
      panel(allInput.operationId).locator(".message-bulk-counts"),
    ).toContainText("目标 Target61");
    await expect(
      panel(allInput.operationId).locator(".message-bulk-counts"),
    ).toContainText("已处理 Processed0");
    // A closed tab leaves only the persisted original intent. Each new tab
    // discovers and GETs that same operation, without starting/cancelling work.
    for (let opening = 0; opening < 2; opening++) {
      const recoveryTab = await context.newPage();
      const writes: string[] = [];
      recoveryTab.on("request", (request) => {
        if (
          request.method() === "POST" &&
          new URL(request.url()).pathname.startsWith("/api/messages/")
        )
          writes.push(request.url());
      });
      try {
        await recoveryTab.goto(page.url());
        const restored = recoveryTab.locator(
          `[data-bulk-operation="${allInput.operationId}"]`,
        );
        await expect(restored).toContainText("已接受，等待处理");
        await expect(restored.locator(".message-bulk-counts")).toContainText(
          "目标 Target61",
        );
        expect(writes).toEqual([]);
      } finally {
        await recoveryTab.close();
      }
    }
    await capture("recovered-queued");
    const lateTaskIDs: string[] = JSON.parse(
      execFileSync(fixture, ["seed-late"], {
        encoding: "utf8",
        timeout: 40000,
      }),
    );
    expect(lateTaskIDs).toHaveLength(1);
    expect(taskIDs).not.toContain(lateTaskIDs[0]);
    execFileSync(fixture, ["bulk-once"], { timeout: 40000 });
    const running = await lookup(allInput.operationId);
    expect(running.state).toBe("running");
    expect(running.progress).toEqual({
      target: 61,
      processed: 50,
      newlyRead: 48,
      alreadyRead: 2,
      failed: 0,
      remaining: 11,
    });
    await panel(allInput.operationId)
      .getByRole("button", { name: "查询原批次进度", exact: true })
      .click();
    await expect(
      panel(allInput.operationId).getByRole("button", {
        name: "取消剩余项目",
        exact: true,
      }),
    ).toBeEnabled();
    let cancelledCommit!: (op: Operation) => void;
    const cancelCommit = new Promise<Operation>(
      (resolve) => (cancelledCommit = resolve),
    );
    let cancelPosts = 0;
    const countCancel = (r: import("@playwright/test").Request) => {
      if (
        new URL(r.url()).pathname === "/api/messages/bulk-read-cancel" &&
        r.method() === "POST"
      )
        cancelPosts++;
    };
    page.on("request", countCancel);
    await page.route(
      "**/api/messages/bulk-read-cancel",
      async (route) => {
        expect(route.request().postDataJSON()).toEqual({
          operationId: allInput.operationId,
        });
        const response = await route.fetch({ maxRetries: 0 });
        expect(response.status()).toBe(200);
        const value = await response.json();
        await route.abort("failed");
        cancelledCommit(value);
      },
      { times: 1 },
    );
    await panel(allInput.operationId)
      .getByRole("button", { name: "取消剩余项目", exact: true })
      .click();
    cancelled = await cancelCommit;
    await expect(panel(allInput.operationId)).toContainText("已取消剩余项目");
    expect(cancelPosts).toBe(1);
    page.off("request", countCancel);
    conserved(cancelled);
    expect(cancelled.state).toBe("cancelled");
    expect(cancelled.progress).toEqual(running.progress);
    const persisted = proof(container, allInput.operationId);
    expect(persisted).toEqual({
      state: "cancelled",
      target: 61,
      processed: 50,
      newly_read: 48,
      already_read: 2,
      targets: 61,
      receipts: 50,
      preserved: 50,
    });
    execFileSync(fixture, ["bulk-once"], { timeout: 40000 });
    expect(await lookup(allInput.operationId)).toEqual(cancelled);
    expect(proof(container, allInput.operationId)).toEqual(persisted);
    await oldSnapshot();
    await capture("cancelled");
  } finally {
    if (stopped) process.kill(worker, "SIGCONT");
  }
  await expect.poll(() => sourceProof().length, { timeout: 30000 }).toBe(62);
  await oldSnapshot();
  expect(await lookup(allInput.operationId)).toEqual(cancelled);
  const oldState = await context.storageState();
  await navigateTo(page, "退出登录");
  await login(page, username, changed);
  await page
    .getByLabel("二次认证验证码", { exact: true })
    .fill(await freshCode(mfa));
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  await navigateTo(page, "站内消息");
  await expect(panel(allInput.operationId)).toBeVisible();
  expect(starts).toHaveLength(2);
  expect(await lookup(allInput.operationId)).toEqual(cancelled);
  const oldResult = await context.request.get(
    `/api/messages/page?resultId=${snapshot.resultId}&offset=0&limit=20`,
  );
  expect(oldResult.status()).toBe(410);
  const refreshed = await query();
  expect(refreshed.counts).toEqual({ all: 62, read: 50, unread: 12 });
  const oldContext = await context.browser()!.newContext({
    baseURL: new URL(page.url()).origin,
    storageState: oldState,
  });
  try {
    expect(
      (
        await oldContext.request.get(
          `/api/messages/bulk-read-operation?operationId=${allInput.operationId}`,
        )
      ).status(),
    ).toBe(401);
  } finally {
    await oldContext.close();
  }
  // Real access UI creates a different tasks-readable actor. Copied local intent
  // storage cannot confer ownership or trigger a POST in that actor's session.
  const mutation = async (path: string, button: string) => {
    const [r] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === path && r.request().method() === "POST",
      ),
      page.getByRole("button", { name: button, exact: true }).click(),
    ]);
    expect(r.status()).toBe(200);
    return await r.json();
  };
  const authorize = async () => {
    await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
    await page
      .getByLabel("未使用的认证器验证码", { exact: true })
      .fill(await freshCode(mfa));
  };
  await navigateTo(page, "访问管理");
  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page
    .getByLabel("角色名称", { exact: true })
    .fill("Synthetic Bulk Reader");
  await page
    .getByRole("checkbox", { name: "后台任务：读取", exact: true })
    .check();
  await authorize();
  const role = await mutation("/api/access/roles/save", "保存角色");
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page
    .getByLabel("用户名", { exact: true })
    .fill("synthetic.bulk.reader");
  await page
    .getByLabel("初始密码", { exact: true })
    .fill("Initial Bulk Reader 123");
  await page
    .getByLabel("确认初始密码", { exact: true })
    .fill("Initial Bulk Reader 123");
  await page.getByLabel("角色", { exact: true }).fill(role.roleID);
  await authorize();
  await mutation("/api/access/users/create", "创建用户");
  const other = await context.browser()!.newContext({
    baseURL: new URL(page.url()).origin,
    storageState: { cookies: [], origins: oldState.origins },
  });
  try {
    const reader = await other.newPage();
    await reader.goto("/");
    await login(reader, "synthetic.bulk.reader", "Initial Bulk Reader 123");
    await changeInitialPassword(
      reader,
      "Initial Bulk Reader 123",
      "Changed Bulk Reader 456",
    );
    await navigateTo(reader, "站内消息");
    await expect(
      reader.getByRole("region", { name: "批量已读操作", exact: true }),
    ).toHaveCount(0);
    expect(
      (
        await other.request.get(
          `/api/messages/bulk-read-operation?operationId=${allInput.operationId}`,
        )
      ).status(),
    ).toBe(404);
    expect(
      (
        await other.request.get(
          `/api/messages/page?resultId=${refreshed.resultId}&offset=0&limit=20`,
        )
      ).status(),
    ).toBe(404);
    const me = await other.request.get("/api/auth/me");
    const csrf = (await me.json()).csrfToken;
    for (const [path, data] of [
      ["bulk-read", allInput],
      ["bulk-read-cancel", { operationId: allInput.operationId }],
    ] as const) {
      expect(
        (
          await other.request.post(`/api/messages/${path}`, {
            headers: {
              Origin: new URL(reader.url()).origin,
              "X-CSRF-Token": csrf,
            },
            data,
          })
        ).status(),
      ).toBe(404);
    }
  } finally {
    await other.close();
  }
  // A real self-password change rotates the session and advances the durable
  // authorization epoch. Already accepted work must stop before writing a batch.
  await navigateTo(page, "站内消息");
  const epochSnapshot = await query();
  let epochBlocked!: Operation;
  await stopWorker();
  try {
    await page
      .getByRole("button", { name: "选择固定结果全部", exact: true })
      .click();
    const epochAccepted = await start();
    expect(epochAccepted.progress).toMatchObject({
      target: 62,
      processed: 0,
      remaining: 62,
    });
    const beforeEpoch = sql(
      container,
      `SELECT authorization_version FROM adtr.users WHERE username='e2e-admin'`,
    );
    await navigateTo(page, "修改密码");
    await changeInitialPassword(
      page,
      changed,
      "Synthetic Bulk Rotated Password 456",
    );
    const afterEpoch = sql(
      container,
      `SELECT authorization_version FROM adtr.users WHERE username='e2e-admin'`,
    );
    expect(afterEpoch).toBeGreaterThan(beforeEpoch);
    execFileSync(fixture, ["bulk-once"], { timeout: 40000 });
    epochBlocked = await lookup(epochAccepted.receipt.operationId);
    expect(epochBlocked.state).toBe("authorization_blocked");
    expect(epochBlocked.progress).toEqual(epochAccepted.progress);
    expect(proof(container, epochAccepted.receipt.operationId)).toEqual({
      state: "authorization_blocked",
      target: 62,
      processed: 0,
      newly_read: 0,
      already_read: 0,
      targets: 62,
      receipts: 0,
      preserved: 0,
    });
    expect(
      (
        await context.request.get(
          `/api/messages/page?resultId=${epochSnapshot.resultId}&offset=0&limit=20`,
        )
      ).status(),
    ).toBe(410);
    await navigateTo(page, "站内消息");
    await expect(panel(epochAccepted.receipt.operationId)).toContainText(
      "权限版本变化",
    );
    expect((await query()).counts).toEqual({ all: 62, read: 50, unread: 12 });
    expect(starts).toHaveLength(3);
  } finally {
    process.kill(worker, "SIGCONT");
  }
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], {
    encoding: "utf8",
  }).trim();
  const hash = (file: string) =>
    createHash("sha256")
      .update(readFileSync(resolve(root, file)))
      .digest("hex");
  const manifest = {
    formatVersion: 1,
    suite: "messages-bulk",
    closedAndReopenedRecoveryTabs: 2,
    chain:
      "internal authenticated SubmitTx (NOT HTTP) → real ProductionRegistry Worker → PostgreSQL outbox projection → real browser HTTP bulk → production BulkOnce → cancellation and receipt recovery",
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
    selectedDone,
    epochBlocked,
    allInput,
    accepted,
    cancelled,
    refreshed,
    persisted: proof(container, allInput.operationId),
    browserStarts: starts,
    screenshots,
    sourceSHA256: Object.fromEntries(
      [
        "web/e2e/messages-bulk.spec.ts",
        "internal/testmessagefixture/main.go",
        "scripts/test_auth_e2e.py",
        "web/src/message-bulk.tsx",
        "web/src/message-bulk-api.ts",
        "web/src/message-bulk-intent.ts",
        "web/src/MessagesWorkspace.tsx",
        "web/src/message-soc.css",
        "web/package-lock.json",
        ".github/workflows/ci.yml",
      ].map((file) => [file, hash(file)]),
    ),
    artifactsSHA256: {
      apiWorker: hash("bin/adtr"),
      frontendEntry: hash("web/dist/index.html"),
      fixture: createHash("sha256").update(readFileSync(fixture)).digest("hex"),
    },
  };
  const path = testInfo.outputPath("messages-soc-bulk-evidence.json");
  writeFileSync(path, JSON.stringify(manifest, null, 2) + "\n");
  await testInfo.attach("messages-soc-bulk-evidence", {
    path,
    contentType: "application/json",
  });
});
