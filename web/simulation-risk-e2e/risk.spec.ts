import { expect, test, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

// Retain only intentional screenshots of synthetic result views, never auth.
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
const api = "/api/simulation/risk";
const evidence = resolve("../artifacts/simulation-risk");
async function openWorkspace(page: Page, capturePrerequisite = false) {
  await page.goto("/#simulation-risk");
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
    name: "模拟风险工作区",
    exact: true,
  });
  await expect(workspace).toContainText("SIMULATED");
  await expect(workspace).toContainText("illustrative-event-risk-v1");
  if (capturePrerequisite) {
    // Auth has completed; capture only the explicitly synthetic workspace.
    // Keep this before dataset selection so selector failures retain safe evidence.
    await workspace.screenshot({
      path: resolve(evidence, "SIMULATED-risk-pre-dataset.png"),
    });
  }
  await page
    .getByLabel("风险数据集", { exact: true })
    .selectOption("simulation-dataset");
  await page
    .getByLabel("证据操作者", { exact: true })
    .fill("synthetic-browser");
  await page.getByRole("button", { name: "查询模拟风险", exact: true }).click();
  return page.getByRole("article", { name: "模拟风险结果", exact: true });
}

test("SIMULATED risk: real mapping, reset, duplicate/lost response, same receipt and reload", async ({
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
  const result = await openWorkspace(page, true);
  await expect(result).toContainText("模拟分数：15");
  await expect(result).toContainText("版本：1");
  for (const text of [
    "风险分布（服务端权重）",
    "medium：1 条 × 15",
    "趋势 UTC",
    "风险记录",
    "重置账本",
    "event:" +
      createHash("sha256")
        .update(JSON.stringify("browser-event"))
        .digest("hex"),
  ])
    await expect(result).toContainText(text);
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-risk-before.png"),
    fullPage: true,
  });
  let posts = 0;
  let operationId = "";
  let receipt: unknown;
  await page.route(
    `**${api}/reset`,
    async (route) => {
      posts++;
      const body = route.request().postDataJSON();
      operationId = body.operationId;
      expect(body.expectedRevision).toBe(1);
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      receipt = await response.json();
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page
    .getByLabel("重置原因", { exact: true })
    .fill("SIMULATED browser reset");
  await page
    .getByRole("button", { name: "重置模拟风险", exact: true })
    .evaluate((button: HTMLButtonElement) => {
      button.click();
      button.click();
    });
  await expect(result).toContainText("模拟分数：0");
  await expect(result).toContainText("版本：2");
  await expect(result).toContainText("SIMULATED browser reset");
  expect(posts).toBe(1);
  expect(operationId).not.toBe("");
  const persisted = await context.request.get(
    `${api}/operation?domainId=simulation-domain-a&datasetId=simulation-dataset&operationId=${encodeURIComponent(operationId)}`,
  );
  expect(persisted.status()).toBe(200);
  expect(await persisted.json()).toEqual(receipt);
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-risk-reset.png"),
    fullPage: true,
  });
  await page.reload();
  await openWorkspace(page);
  await expect(result).toContainText("模拟分数：0");
  await expect(result).toContainText("版本：2");
  await expect(result).toContainText("SIMULATED browser reset");
  await page.screenshot({
    path: resolve(evidence, "SIMULATED-risk-reloaded.png"),
    fullPage: true,
  });
});
