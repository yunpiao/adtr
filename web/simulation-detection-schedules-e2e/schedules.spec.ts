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
        (entry): entry is [string, string] =>
          entry[1] !== undefined && !/^(ADTR_|POSTGRES_|PG)/i.test(entry[0]),
      ),
    ),
  },
});
const api = "/api/simulation/detection-schedules";
const evidence = resolve("../artifacts/simulation-detection-schedules");
async function openWorkspace(page: Page) {
  await page.goto("/#simulation-detection-schedules");
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
    name: "模拟定时检测工作区",
    exact: true,
  });
  await expect(workspace).toContainText("SIMULATED");
  await expect(workspace).toContainText("无真实 AD、包速率控制");
  return workspace;
}
test("SIMULATED schedules: real CRUD/recovery and separately seeded historical worker result", async ({
  page,
  context,
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
  const identity = await (await context.request.get("/api/auth/me")).json();
  let workspace = await openWorkspace(page);
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-schedules-initial.png"),
  });
  await page
    .getByLabel("计划名称", { exact: true })
    .fill("SIMULATED browser schedule");
  await page.getByLabel("UTC 周期", { exact: true }).selectOption("2");
  await page
    .getByLabel("检测模板", { exact: true })
    .selectOption("schedule-template");
  await page
    .getByLabel("合成数据集", { exact: true })
    .selectOption("simulation-dataset");
  let operationId = "",
    scheduleId = "",
    posts = 0;
  await page.route(
    `**${api}/create`,
    async (route) => {
      posts++;
      operationId = route.request().postDataJSON().operationId;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      expect(response.headers()["x-adtr-user-id"]).toBe(String(identity.ID));
      const created = await response.json();
      scheduleId = created.schedule.id;
      expect(created.schedule.state).toBe("paused");
      expect(created.schedule.cycle).toBe(2);
      expect(created.schedule).not.toHaveProperty("rate");
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "创建暂停计划", exact: true }).click();
  await expect.poll(() => scheduleId).not.toBe("");
  await page
    .getByRole("button", { name: "核对回执并恢复", exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "计划详情", exact: true }),
  ).toContainText("SIMULATED browser schedule");
  expect(posts).toBe(1);
  const detailURL = () =>
    `${api}/detail?domainId=simulation-domain-a&id=${encodeURIComponent(scheduleId)}&pageIdx=1&pageSize=20`;
  await page.getByRole("button", { name: "编辑计划", exact: true }).click();
  await page
    .getByLabel("计划名称", { exact: true })
    .fill("SIMULATED browser edited");
  await page.getByLabel("UTC 周期", { exact: true }).selectOption("1");
  const update = page.waitForResponse(
    (r) => r.url().endsWith(api + "/update") && r.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "保存新修订并暂停", exact: true })
    .click();
  expect((await (await update).json()).schedule.revision).toBe(2);
  for (const [label, state] of [
    ["启用计划", "enabled"],
    ["暂停计划", "paused"],
    ["归档计划", "archived"],
  ]) {
    await page.getByRole("button", { name: label, exact: true }).click();
    await expect
      .poll(
        async () =>
          (await (await context.request.get(detailURL())).json()).schedule
            .state,
      )
      .toBe(state);
    await expect(
      page.getByRole("button", { name: "刷新当前状态", exact: true }),
    ).toBeEnabled();
  }
  await page.reload();
  workspace = await openWorkspace(page);
  await page.getByLabel("操作 ID", { exact: true }).fill(operationId);
  await page
    .getByRole("button", { name: "核对回执并恢复", exact: true })
    .click();
  const detail = page.getByRole("region", { name: "计划详情", exact: true });
  await expect(detail).toContainText("SIMULATED browser edited");
  await expect(detail).toContainText("已归档");
  const receipt = await (
    await context.request.get(
      `${api}/receipt?domainId=simulation-domain-a&operationId=${encodeURIComponent(operationId)}`,
    )
  ).json();
  expect(receipt.receipt.state).toBe("paused");
  expect(receipt.schedule.state).toBe("archived");
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-schedules-archived.png"),
  });
  // This is a separate isolated historical producer, not the future schedule
  // just created by the browser. Its actual worker result was checked by API.
  await page
    .getByRole("button", { name: "SIMULATED historical producer", exact: true })
    .click();
  const historical = await (
    await context.request.get(
      `${api}/detail?domainId=simulation-domain-a&id=historical-e2e-schedule&pageIdx=1&pageSize=20`,
    )
  ).json();
  const admitted = historical.events.filter(
    (e: { action: string }) => e.action === "admitted",
  );
  expect(admitted).toHaveLength(1);
  await page
    .getByRole("button", {
      name: `查看执行 ${admitted[0].taskId}`,
      exact: true,
    })
    .click();
  const result = page.getByRole("region", {
    name: "发生执行结果",
    exact: true,
  });
  await expect(result).toContainText("合成评估成功");
  await expect(result).toContainText('"simulation": true');
  await expect(result).toContainText('"eventCount": 2');
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-schedules-worker-result.png"),
  });
});
