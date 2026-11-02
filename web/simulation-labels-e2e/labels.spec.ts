import { expect, test, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

test.use({
  trace: "off",
  screenshot: "off",
  video: "off",
  launchOptions: {
    env: Object.fromEntries(
      Object.entries(process.env).filter(
        (x): x is [string, string] =>
          x[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(x[0]),
      ),
    ),
  },
});
const evidence = resolve("../artifacts/simulation-labels");
async function open(page: Page) {
  await page.goto("/#simulation-labels");
  await page
    .getByRole("radio", {
      name: "选择数据源 simulation.example.test",
      exact: true,
    })
    .check();
  await page
    .getByRole("button", { name: "核对并打开连接详情", exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "模拟敏感资产工作区", exact: true }),
  ).toContainText("SIMULATED");
}

test("SIMULATED labels: real policy/evaluation/change alerts, unknown, reload and committed response-loss recovery", async ({
  page,
}) => {
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
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
  await open(page);
  await page
    .getByLabel("合成数据集", { exact: true })
    .selectOption("simulation-dataset");
  await page.getByLabel("固定观测", { exact: true }).selectOption("o1");
  await page.getByLabel("策略名称", { exact: true }).fill("Browser policy");
  await page
    .getByLabel("操作原因", { exact: true })
    .fill("SIMULATED browser evidence");
  await page
    .getByRole("checkbox", {
      name: "Synthetic sensitive group (g)",
      exact: true,
    })
    .check();
  // Commit the actual HTTP request, discard only its response; no fabricated result.
  await page.route(
    "**/api/simulation/labels/create",
    async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "创建策略", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "待恢复操作", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("网络结果不确定");
  await page.reload();
  await open(page);
  await page
    .getByRole("button", { name: "恢复原操作回执", exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "历史操作回执", exact: true }),
  ).toContainText("create");
  const policies = page.getByLabel("当前策略", { exact: true });
  await expect(
    policies.locator("option").filter({ hasText: "Browser policy" }),
  ).toHaveCount(1);
  const policyId = await policies
    .locator("option")
    .filter({ hasText: "Browser policy" })
    .getAttribute("value");
  if (!policyId) throw new Error("Missing persisted policy");
  const evalIds: string[] = [];
  for (const observation of ["o1", "o2", "o3"]) {
    await page
      .getByLabel("固定观测", { exact: true })
      .selectOption(observation);
    await policies.selectOption(policyId);
    await page
      .getByLabel("操作原因", { exact: true })
      .fill("SIMULATED browser evaluation");
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/simulation/labels/evaluate") &&
        r.request().method() === "POST",
    );
    await page
      .getByRole("button", { name: "评估固定修订", exact: true })
      .click();
    const evaluated = await response;
    expect(evaluated.status()).toBe(200);
    evalIds.push((await evaluated.json()).evaluation.id);
    await expect(
      page
        .getByLabel("已保存评估", { exact: true })
        .locator(`option[value="${evalIds.at(-1)}"]`),
    ).toHaveCount(1);
  }
  await page.getByLabel("已保存评估", { exact: true }).selectOption(evalIds[2]);
  await expect(
    page.getByRole("region", { name: "模拟敏感资产工作区", exact: true }),
  ).toContainText("不完整／未知");
  // Await both real lazy details: pending requests must not masquerade as
  // evidence that an incomplete comparison has been definitively rejected.
  const incompleteDetails = evalIds.slice(1).map((id) =>
    page.waitForResponse((r) => {
      const url = new URL(r.url());
      return (
        url.pathname === "/api/simulation/labels/evaluation" &&
        url.searchParams.get("evaluationId") === id
      );
    }),
  );
  await page.getByLabel("前置评估", { exact: true }).selectOption(evalIds[1]);
  await page.getByLabel("后置评估", { exact: true }).selectOption(evalIds[2]);
  const fetchedDetails = await Promise.all(incompleteDetails);
  for (const fetched of fetchedDetails) expect(fetched.status()).toBe(200);
  expect((await fetchedDetails[0].json()).result.completeWithinSnapshot).toBe(
    true,
  );
  expect((await fetchedDetails[1].json()).result.completeWithinSnapshot).toBe(
    false,
  );
  await expect(
    page.getByRole("button", { name: "比较并生成站内告警", exact: true }),
  ).toBeDisabled();
  await page.getByLabel("已保存评估", { exact: true }).selectOption(evalIds[1]);
  await page
    .getByRole("button", { name: /^Example computer \(computer · c\)/ })
    .click();
  const detail = page.getByRole("region", {
    name: "资产标签详情",
    exact: true,
  });
  await expect(detail).toContainText("蜜罐状态：不可用");
  await expect(detail).toContainText("距离 2");
  await page.getByLabel("资产名称", { exact: true }).fill("not-present");
  await expect(page.getByText("匹配资产 0 个", { exact: true })).toBeVisible();
  await page.getByLabel("资产名称", { exact: true }).fill("");
  await page.getByLabel("前置评估", { exact: true }).selectOption(evalIds[0]);
  await page.getByLabel("后置评估", { exact: true }).selectOption(evalIds[1]);
  const compared = page.waitForResponse(
    (r) =>
      r.url().endsWith("/api/simulation/labels/compare") &&
      r.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "比较并生成站内告警", exact: true })
    .click();
  const response = await compared;
  expect(response.status()).toBe(200);
  const comparison = (await response.json()).comparison;
  expect(
    comparison.alerts.map((a: { objectId: string }) => a.objectId),
  ).toEqual(["c", "u"]);
  await page
    .getByLabel("已保存观测比较", { exact: true })
    .selectOption(comparison.id);
  const alerts = page.getByRole("region", {
    name: "资产变化告警",
    exact: true,
  });
  await expect(alerts.getByRole("article")).toHaveCount(2);
  await expect(alerts).toContainText("added");
  await expect(alerts).toContainText("removed");
  await expect(alerts).toContainText("in_app_only");
  await alerts.screenshot({
    path: resolve(evidence, "SIMULATED-labels-alerts.png"),
  });
  await page.reload();
  await open(page);
  await page
    .getByLabel("合成数据集", { exact: true })
    .selectOption("simulation-dataset");
  await page
    .getByLabel("已保存观测比较", { exact: true })
    .selectOption(comparison.id);
  await expect(
    page
      .getByRole("region", { name: "资产变化告警", exact: true })
      .getByRole("article"),
  ).toHaveCount(2);
  await policies.selectOption(policyId);
  await expect(
    page.getByRole("region", { name: "策略编辑历史", exact: true }),
  ).toContainText("r1");
});
