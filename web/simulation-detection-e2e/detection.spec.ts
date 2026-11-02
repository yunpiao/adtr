import { expect, test, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

// Only intentional synthetic region captures; no login, cookie, trace or video.
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
const api = "/api/simulation/detection";
const evidence = resolve("../artifacts/simulation-detection");
async function openWorkspace(page: Page) {
  await page.goto("/#simulation-detection");
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
    name: "模拟检测模板",
    exact: true,
  });
  await expect(workspace).toContainText("SIMULATED");
  return workspace;
}

test("SIMULATED detection: real CRUD, worker result, lost response receipt and reload", async ({
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
  const identityResponse = await context.request.get("/api/auth/me");
  expect(identityResponse.status()).toBe(200);
  const identity = await identityResponse.json();
  const workspace = await openWorkspace(page);
  // Capture before selecting anything so selector failures leave useful safe evidence.
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-detection-preselection.png"),
  });
  await page.getByRole("button", { name: "新建模拟模板", exact: true }).click();
  await page.getByLabel("模板 ID", { exact: true }).fill("browser-template");
  await page
    .getByLabel("模板名称", { exact: true })
    .fill("SIMULATED browser template");
  const create = page.waitForResponse(
    (r) => r.url().endsWith(api + "/create") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "保存新模板", exact: true }).click();
  const createdResponse = await create;
  expect(createdResponse.status()).toBe(200);
  expect(createdResponse.headers()["x-adtr-user-id"]).toBe(String(identity.ID));
  const first = (await createdResponse.json()).template;
  expect(first.version).toBe(1);
  expect(first.simulation).toBe(true);
  await page
    .getByLabel("模板名称", { exact: true })
    .fill("SIMULATED browser v2");
  const update = page.waitForResponse(
    (r) => r.url().endsWith(api + "/update") && r.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "保存模板新版本", exact: true })
    .click();
  const updatedResponse = await update;
  expect(updatedResponse.status()).toBe(200);
  const updated = (await updatedResponse.json()).template;
  expect(updated.version).toBe(2);
  expect(updated.hash).not.toBe(first.hash);
  await page
    .getByLabel("检测合成数据集", { exact: true })
    .selectOption("simulation-dataset");
  let operationId = "";
  let taskUUID = "";
  let posts = 0;
  await page.route(
    `**${api}/run`,
    async (route) => {
      posts++;
      const body = route.request().postDataJSON();
      operationId = body.operationId;
      expect(body.templateVersion).toBe(2);
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      taskUUID = (await response.json()).submission.task.taskUUID;
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page
    .getByRole("button", { name: "手动运行模拟检测", exact: true })
    .evaluate((button: HTMLButtonElement) => {
      button.click();
      button.click();
    });
  await expect.poll(() => taskUUID).not.toBe("");
  await page.getByRole("button", { name: "核验原操作", exact: true }).click();
  expect(posts).toBe(1);
  const taskURL = `${api}/task?domainId=simulation-domain-a&taskUUID=${encodeURIComponent(taskUUID)}`;
  await expect
    .poll(async () => {
      const response = await context.request.get(taskURL);
      expect(response.status()).toBe(200);
      return (await response.json()).task.state;
    })
    .toBe("succeeded");
  await page.getByRole("button", { name: "刷新检测任务", exact: true }).click();
  await expect(workspace).toContainText(updated.hash);
  await expect(workspace).toContainText("adtr.simulation-detection.mapping.v1");
  const taskResponse = await context.request.get(taskURL);
  const completed = await taskResponse.json();
  expect(completed.task.result.templateVersion).toBe(2);
  expect(completed.task.result.templateHash).toBe(updated.hash);
  expect(completed.task.result.simulation).toBe(true);
  expect(completed.task.result.matches.length).toBeGreaterThan(0);
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-detection-result.png"),
  });
  const receiptURL = `${api}/receipt?domainId=simulation-domain-a&operationId=${encodeURIComponent(operationId)}`;
  const receipt = await context.request.get(receiptURL);
  expect(receipt.status()).toBe(200);
  expect((await receipt.json()).submission.task.taskUUID).toBe(taskUUID);
  await page.reload();
  await openWorkspace(page);
  await page
    .getByRole("button", { name: "查看模板 SIMULATED browser v2", exact: true })
    .click();
  await expect(page.getByLabel("模板名称", { exact: true })).toHaveValue(
    "SIMULATED browser v2",
  );
  await page.getByLabel("恢复操作 ID", { exact: true }).fill(operationId);
  await page
    .getByRole("button", { name: "恢复服务器回执", exact: true })
    .click();
  await page.getByRole("button", { name: "刷新检测任务", exact: true }).click();
  await expect(
    page.getByLabel("SIMULATED 检测结果", { exact: true }),
  ).toContainText(updated.hash);
  expect(await (await context.request.get(taskURL)).json()).toEqual(completed);
  const archive = page.waitForResponse(
    (r) =>
      r.url().endsWith(api + "/archive") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "归档模拟模板", exact: true }).click();
  const archived = await archive;
  expect(archived.status()).toBe(200);
  expect((await archived.json()).template.archived).toBe(true);
  await workspace.screenshot({
    path: resolve(evidence, "SIMULATED-detection-archived.png"),
  });
});
