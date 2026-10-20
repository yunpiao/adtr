import {
  expect,
  type BrowserContext,
  type Page,
  type Request,
} from "@playwright/test";
import type { TaskDetail } from "../src/task-api";
import { navigateTo } from "./navigation";

// These independent route expectations cover only the already registered real
// LDAP/export suites. They must not inherit the production kind classification.
const destinations = {
  "domain.directory_read.v3": {
    hash: "#directory-v3",
    heading: "字典 3 目录采集",
  },
  "domain.directory_read.v4": {
    hash: "#group-members-v4",
    heading: "组成员原始观测 · 字典 4",
  },
  "user_assets.v3.export": {
    hash: "#user-assets-v3",
    heading: "用户资产",
  },
  "group_assets.v3.export": {
    hash: "#group-assets-v3",
    heading: "组资产",
  },
  "computer_assets.v3.export": {
    hash: "#computer-assets-v3",
    heading: "计算机资产",
  },
} as const;

type Checkpoint = {
  actorId: number;
  taskUUID: string;
  taskName: keyof typeof destinations;
  state: "queued" | "succeeded" | "failed";
  error?: string;
};
const privateFields =
  /artifactToken|fencingToken|leaseOwner|snapshotAt|sourceSHA256|grantRevision|accountCredentialRevision|connectionCredentialGeneration|rawMember|objectGUID|samAccountName|objectSid|actorPassword|totpCode|ciphertext/u;

async function readDetail(
  page: Page,
  checkpoint: Checkpoint,
  action: () => Promise<unknown>,
  safe: (value: unknown) => void,
) {
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => {
      const url = new URL(candidate.url());
      return (
        candidate.request().method() === "GET" &&
        url.pathname === "/api/tasks/detail" &&
        url.searchParams.get("taskUUID") === checkpoint.taskUUID
      );
    }),
    action(),
  ]);
  expect(response.request().resourceType()).toBe("fetch");
  expect(response.fromServiceWorker()).toBe(false);
  expect(response.status()).toBe(200);
  expect(response.headers()["cache-control"]).toBe("no-store");
  const value = (await response.json()) as TaskDetail;
  safe(value);
  // Report a boolean on disclosure failure, never the private payload itself.
  expect(
    privateFields.test(JSON.stringify(value)),
    "private generic task fields",
  ).toBe(false);
  expect(
    JSON.stringify(value.task.result) === "{}",
    "generic result must be exactly redacted",
  ).toBe(true);
  expect(
    JSON.stringify(value.task.cursor) === "{}",
    "generic cursor must be exactly redacted",
  ).toBe(true);
  expect(value.task).toMatchObject({
    taskUUID: checkpoint.taskUUID,
    taskName: checkpoint.taskName,
    state: checkpoint.state,
    error: checkpoint.error ?? "",
  });
  expect(value.events.length).toBeGreaterThan(0);
  return value;
}

async function expectReadOnlyDetail(
  page: Page,
  checkpoint: Checkpoint,
  value: TaskDetail,
  safe: (value: unknown) => void,
) {
  const detail = page.getByRole("region", {
    name: "任务执行详情",
    exact: true,
  });
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId("task-detail-identity")).toHaveText(
    checkpoint.taskUUID,
  );
  await expect(detail.locator(".task-state")).toHaveAttribute(
    "data-state",
    checkpoint.state,
  );
  await expect(
    detail.getByLabel("任务持久化进度", { exact: true }),
  ).toHaveJSProperty("value", value.task.progress);
  await expect(
    detail.getByText("当前执行状态", { exact: true }),
  ).toBeVisible();
  if (checkpoint.error)
    await expect(detail.locator(".task-error-code")).toContainText(
      checkpoint.error,
    );
  for (const name of [
    "请求取消任务",
    "创建恢复任务",
    "确认请求取消",
    "确认恢复任务",
  ])
    await expect(detail.getByRole("button", { name, exact: true })).toHaveCount(0);
  await expect(
    detail.locator(
      'input[type="password"], input[name="actorPassword"], input[name="totpCode"]',
    ),
  ).toHaveCount(0);
  await expect(
    detail.getByLabel("任务实际结果", { exact: true }),
  ).toHaveCount(0);
  await expect(
    detail.getByText("查看已提交的执行游标", { exact: true }),
  ).toHaveCount(0);
  await expect(detail.locator("pre")).toHaveCount(0);
  const events = detail.getByRole("list", {
    name: "持久化任务事件",
    exact: true,
  });
  await expect(events).toBeVisible();
  await expect(events.getByRole("listitem")).toHaveCount(value.events.length);
  for (const [index, event] of value.events.entries()) {
    const item = events.getByRole("listitem").nth(index);
    await expect(item).toContainText(event.action);
    await expect(item).toContainText(event.state);
    await expect(item).toContainText(event.createdAt);
  }
  const text = await detail.innerText();
  safe(text);
  expect(privateFields.test(text), "private task fields rendered").toBe(false);
  await expect(
    detail.locator(`a[href="${destinations[checkpoint.taskName].hash}"]`),
  ).toBeVisible();
  if (checkpoint.taskName === "domain.directory_read.v4") {
    await expect(detail).toContainText("只读");
    await expect(detail).toContainText("API");
  }
  if (checkpoint.taskName === "user_assets.v3.export") {
    await expect(detail).toContainText("查看原导出结果");
    await expect(detail).toContainText("重新核验专用权限");
  } else if (checkpoint.taskName.endsWith(".export"))
    await expect(detail).toContainText("不会按此任务 ID 自动恢复");
}

// Open a separate same-session tab so the original specialized panel, source
// selection and protected receipt stay untouched. Every task ID is from the
// owning suite's real persisted task. No routes, responses, state or clocks are
// replaced, and no task is submitted or cancelled by this helper.
export async function expectSpecializedTaskReadOnly(
  context: BrowserContext,
  checkpoint: Checkpoint,
  safe: (value: unknown) => void,
) {
  const page = await context.newPage();
  const genericWrites: string[] = [];
  const onRequest = (request: Request) => {
    const path = new URL(request.url()).pathname;
    if (
      /^\/api\/tasks(?:\/|$)/u.test(path) &&
      !["GET", "HEAD"].includes(request.method())
    )
      genericWrites.push(`${request.method()} ${path}`);
  };
  page.on("request", onRequest);
  try {
    await page.setViewportSize({ width: 1440, height: 900 });
    const [session] = await Promise.all([
      page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === "/api/auth/me" &&
          response.request().method() === "GET",
      ),
      page.goto("/"),
    ]);
    expect(session.status()).toBe(200);
    // The generic task endpoint has no actor header. Bind the real browser
    // session here without retaining or reporting its CSRF token/profile.
    expect((await session.json()).ID).toBe(checkpoint.actorId);
    const [permissions] = await Promise.all([
      page.waitForResponse((response) => {
        if (
          new URL(response.url()).pathname !== "/api/access/check" ||
          response.request().method() !== "POST"
        )
          return false;
        const paths = response.request().postDataJSON().paths;
        return (
          Array.isArray(paths) &&
          paths.includes("POST /api/tasks/cancel") &&
          paths.includes("POST /api/tasks/recover")
        );
      }),
      navigateTo(page, "后台任务"),
    ]);
    expect(permissions.status()).toBe(200);
    const paths: string[] = permissions.request().postDataJSON().paths;
    const checks = await permissions.json();
    // Absence of controls must come from the task kind, not missing task-write
    // permission. Observe the workspace's own check; no extra grants or proof.
    for (const operation of ["cancel", "recover"])
      expect(checks.results[paths.indexOf(`POST /api/tasks/${operation}`)]).toBe(
        true,
      );
    await expect(
      page.getByRole("table", { name: "任务列表", exact: true }),
    ).toBeVisible();
    await page.locator(".task-filter-panel > summary").click();
    await page
      .getByLabel("任务种类筛选", { exact: true })
      .fill(checkpoint.taskName);
    await page.getByRole("button", { name: "筛选任务", exact: true }).click();
    const value = await readDetail(
      page,
      checkpoint,
      () =>
        page
          .getByRole("button", {
            name: `详情 ${checkpoint.taskUUID}`,
            exact: true,
          })
          .click(),
      safe,
    );
    await expectReadOnlyDetail(page, checkpoint, value, safe);
    const refreshed = await readDetail(
      page,
      checkpoint,
      () =>
        page
          .getByRole("button", { name: "刷新任务详情", exact: true })
          .click(),
      safe,
    );
    await expectReadOnlyDetail(page, checkpoint, refreshed, safe);
    const destination = destinations[checkpoint.taskName];
    await page
      .getByRole("region", { name: "任务执行详情", exact: true })
      .locator(`a[href="${destination.hash}"]`)
      .click();
    await expect.poll(() => new URL(page.url()).hash).toBe(destination.hash);
    await expect(
      page.getByRole("heading", { name: destination.heading, exact: true }),
    ).toBeVisible();
    // Export links open the workspace's normal safe initial view. A task ID
    // alone must not manufacture an export locator or a download operation.
    if (checkpoint.taskName.endsWith(".export")) {
      await expect(
        page.getByRole("button", { name: /^下载.*XLSX$/u }),
      ).toHaveCount(0);
      await expect(page.locator('input[type="password"]')).toHaveCount(0);
    }
    expect(genericWrites, "generic workspace must issue no task writes").toEqual(
      [],
    );
  } finally {
    page.off("request", onRequest);
    await page.close();
  }
}
