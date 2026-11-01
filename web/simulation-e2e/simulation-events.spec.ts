import { expect, test, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

// Authentication cookies and CSRF inputs must never enter retained traces.
// Explicit screenshots are taken only after login, on synthetic result views.
test.use({
  trace: "off",
  screenshot: "off",
  video: "off",
  launchOptions: {
    env: Object.fromEntries(
      Object.entries(process.env).filter(
        (entry): entry is [string, string] =>
          entry[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(entry[0]),
      ),
    ),
  },
});
const api = "/api/simulation/events";
const evidence = resolve("../artifacts/simulation-events");

async function openWorkspace(page: Page) {
  await page.goto("/#simulation-events");
  await page
    .getByRole("radio", {
      name: "选择数据源 simulation.example.test",
      exact: true,
    })
    .check();
  await page
    .getByRole("button", { name: "核对并打开连接详情", exact: true })
    .click();
  const workspace = page.getByRole("region", {
    name: "模拟事件工作区",
    exact: true,
  });
  await expect(workspace).toContainText("SIMULATED");
  await page
    .getByLabel("模拟数据集", { exact: true })
    .selectOption("simulation-dataset");
  await page.getByRole("button", { name: "查询模拟事件", exact: true }).click();
  await expect(
    workspace.getByRole("cell", { name: "synthetic-event", exact: true }),
  ).toBeVisible();
  return workspace;
}

test("SIMULATED only: durable lifecycle, duplicate/lost write and late detail cancellation", async ({
  page,
  context,
}) => {
  const username = process.env.ADTR_E2E_USERNAME;
  const password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error("Dedicated disposable simulation harness required");
  await mkdir(evidence, { recursive: true });
  await page.goto("/");
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeHidden();
  const workspace = await openWorkspace(page);
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-list.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "查看案例 browser-case", exact: true })
    .click();
  const detail = page.getByRole("article", {
    name: "模拟案例详情",
    exact: true,
  });
  await expect(detail).toContainText("版本：1");
  await expect(detail).toContainText("SIMULATED-e2e-only");
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-detail.png"),
    fullPage: true,
  });

  let posts = 0;
  // Forward the real mutation, discard only its response: browser must recover
  // the committed receipt via GET, not silently post a second operation.
  await page.route(
    `**${api}/transition`,
    async (route) => {
      posts++;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page
    .getByLabel("操作原因", { exact: true })
    .fill("SIMULATED browser review");
  // Two same-task native clicks exercise the synchronous duplicate-submit lock.
  await page
    .getByRole("button", { name: "标记可疑", exact: true })
    .evaluate((button: HTMLButtonElement) => {
      button.click();
      button.click();
    });
  await expect(detail).toContainText("标记：suspicious");
  await expect(detail).toContainText("版本：2");
  expect(posts).toBe(1);
  await page.getByRole("button", { name: "刷新详情", exact: true }).click();
  await expect(detail).toContainText("操作历史（1/1）");
  await page
    .getByLabel("操作原因", { exact: true })
    .fill("SIMULATED resolved investigation");
  await page.getByRole("button", { name: "关闭案例", exact: true }).click();
  await expect(detail).toContainText("状态：closed");
  await expect(detail).toContainText("版本：3");
  await expect(
    page.getByRole("button", { name: "关闭案例", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-closed.png"),
    fullPage: true,
  });

  await page.reload();
  await openWorkspace(page);
  await page
    .getByRole("button", { name: "查看案例 browser-case", exact: true })
    .click();
  await expect(detail).toContainText("状态：closed");
  await expect(detail).toContainText("操作历史（2/2）");
  const me = await (await context.request.get("/api/auth/me")).json();
  const response = await context.request.get(
    `${api}/detail?domainId=simulation-domain-a&datasetId=simulation-dataset&caseId=browser-case&historyOffset=0&historyLimit=100`,
  );
  expect(response.status()).toBe(200);
  const persisted = await response.json();
  expect(persisted.case.Revision).toBe(3);
  expect(persisted.historyTotal).toBe(2);
  expect(me.username).toBe(username);

  // Hold a real detail response, cancel the UI wait, then release it. No stale
  // detail is allowed to reappear after cancellation (not a mock success).
  await page.getByRole("button", { name: "返回列表", exact: true }).click();
  let release!: () => void;
  const held = new Promise<void>((done) => {
    release = done;
  });
  let arrived!: () => void;
  const started = new Promise<void>((done) => {
    arrived = done;
  });
  let finished!: () => void;
  const completed = new Promise<void>((done) => {
    finished = done;
  });
  await page.route(
    `**${api}/detail?**`,
    async (route) => {
      try {
        const real = await route.fetch();
        expect(real.status()).toBe(200);
        arrived();
        await held;
        // AbortController may already have canceled the transport; either way,
        // the stale result must not restore the dismissed component state.
        await route.fulfill({ response: real }).catch(() => undefined);
      } finally {
        finished();
      }
    },
    { times: 1 },
  );
  await page
    .getByRole("button", { name: "查看案例 browser-case", exact: true })
    .click();
  await started;
  await page.getByRole("button", { name: "取消等待", exact: true }).click();
  release();
  await completed;
  await expect(detail).toBeHidden();
  await expect(workspace).toContainText("SIMULATED");
  await page
    .getByRole("button", { name: "查看案例 browser-case", exact: true })
    .click();
  await expect(detail).toContainText("状态：closed");
  await expect(detail).toContainText("版本：3");
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-reloaded.png"),
    fullPage: true,
  });
});
