import { expect, test, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

test.use({
  trace: "off", screenshot: "off", video: "off",
  launchOptions: { env: Object.fromEntries(Object.entries(process.env).filter(
    (entry): entry is [string, string] => entry[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(entry[0]),
  )) },
});
const evidence = resolve("../artifacts/simulation-dashboard");
const api = "/api/simulation/dashboard/summary";
async function openWorkspace(page: Page, navigate = true) {
  if (navigate) await page.goto("/#simulation-dashboard");
  await page.getByRole("radio", { name: "选择数据源 simulation.example.test", exact: true }).check();
  await page.getByRole("button", { name: "核对并打开连接详情", exact: true }).click();
  const workspace = page.getByRole("region", { name: "合成态势工作台", exact: true });
  await expect(workspace).toContainText("SIMULATED");
  await page.getByLabel("合成态势数据集", { exact: true }).selectOption("simulation-dataset");
  await page.getByLabel("风险主体（可选）", { exact: true }).fill("synthetic-browser");
  await page.getByLabel("统计窗口", { exact: true }).selectOption("24h");
  await page.getByRole("button", { name: "加载快照", exact: true }).click();
  return page.getByRole("article", { name: "SIMULATED 合成态势快照", exact: true });
}

test("SIMULATED dashboard: exact facts, unavailable versus zero, retry, real producer refresh and reload", async ({ page, context }) => {
  const username = process.env.ADTR_E2E_USERNAME, password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password) throw new Error("Dedicated disposable simulation harness required");
  await mkdir(evidence, { recursive: true });
  await page.goto("/");
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "登录账户", exact: true })).toBeHidden();
  const result = await openWorkspace(page);
  await expect(result).toContainText("总数 2；待处理 1；已关闭 1");
  await expect(result).toContainText("示例分数 15；修订 1");
  await expect(result).toContainText("窗口内 2 条事件");
  await expect(result).toContainText("不可用");
  await expect(result).toContainText("dashboard-template");
  await result.screenshot({ path: resolve(evidence, "SIMULATED-dashboard-before.png") });

  await page.route(`**${api}?*`, route => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "unavailable" }) }), { times: 1 });
  await page.getByRole("button", { name: "刷新快照", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("不可用");
  await expect(result).toContainText("示例分数 15");
  await page.getByRole("button", { name: "刷新快照", exact: true }).click();
  await expect(page.getByRole("alert")).toBeHidden();

  const me = await context.request.get("/api/auth/me");
  expect(me.status()).toBe(200);
  const profile = await me.json();
  const headers = { Origin: new URL(page.url()).origin, "X-CSRF-Token": profile.csrfToken };
  const closed = await context.request.post("/api/simulation/events/transition", { headers, data: {
    domainId: "simulation-domain-a", datasetId: "simulation-dataset", caseId: "browser-case", operationId: "dashboard-browser-close",
    action: "close", reason: "SIMULATED dashboard browser close", expectedRevision: 1,
  } });
  expect(closed.status()).toBe(200);
  const reset = await context.request.post("/api/simulation/risk/reset", { headers, data: {
    domainId: "simulation-domain-a", datasetId: "simulation-dataset", subject: "synthetic-browser", operationId: "dashboard-browser-reset",
    reason: "SIMULATED dashboard browser reset", expectedRevision: 1,
  } });
  expect(reset.status()).toBe(200);
  await page.getByRole("button", { name: "刷新快照", exact: true }).click();
  await expect(result).toContainText("总数 2；待处理 0；已关闭 2");
  await expect(result).toContainText("示例分数 0；修订 2");
  const response = await context.request.get(`${api}?domainId=simulation-domain-a&datasetId=simulation-dataset&subject=synthetic-browser&window=24h`);
  expect(response.status()).toBe(200);
  expect(response.headers()["x-adtr-user-id"]).toBe(String(profile.ID));
  const snapshot = await response.json();
  expect(snapshot.cases).toMatchObject({ total: 2, open: 0, closed: 2 });
  expect(snapshot.caseCloseThroughput.windowTotal).toBe(2);
  expect(snapshot.risk).toMatchObject({ score: 0, revision: 2 });
  expect(snapshot.unavailable.length).toBeGreaterThan(0);
  await result.screenshot({ path: resolve(evidence, "SIMULATED-dashboard-refreshed.png") });

  await page.reload();
  await openWorkspace(page);
  await expect(result).toContainText("总数 2；待处理 0；已关闭 2");
  await expect(result).toContainText("示例分数 0；修订 2");
  await page.goto("/#simulation-risk");
  await page.goBack();
  await openWorkspace(page, false);
  await expect(result).toContainText("示例分数 0；修订 2");
  await result.screenshot({ path: resolve(evidence, "SIMULATED-dashboard-reloaded.png") });
});
