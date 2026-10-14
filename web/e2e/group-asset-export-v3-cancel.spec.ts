import { test, expect } from "@playwright/test";
import { proof } from "./group-assets-v3-fixture";
import { exportPrefix, responseValue } from "./group-asset-export-v3-helpers";
import { chooseColumns, createExport, panelFor, pauseOwnedExportWorker, receiptAndDetail, restoreOwnedExportWorker, resumeOwnedExportWorker, safeColumns, screenshot, terminalExport, terminalSetup, unavailableDownload, writeTerminalEvidence } from "./group-asset-export-v3-terminal-helpers";

test.use({ trace: "off", screenshot: "off", video: "off" });
test("queued export is cancelled with fresh MFA and never executes after Worker resumes", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  expect(process.env.ADTR_E2E_GROUP_ASSET_EXPORT_V3_TERMINAL).toBe("group-asset-export-v3-cancel");
  const setup = await terminalSetup(page, context), panel = panelFor(page);
  let downloads = 0;
  page.on("download", () => { downloads++; });
  await chooseColumns(page, safeColumns);
  // Source collection has really finished before the dedicated Worker pauses.
  const pause = await pauseOwnedExportWorker();
  try {
    const operation = await createExport(page, setup);
    expect(operation.create.task.state).toBe("queued");
    await expect(panel).toContainText("等待执行");
    const queued = await terminalExport(operation.create.taskID);
    expect(queued).toMatchObject({ state: "queued", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    await proof(page, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);
    const [cancelResponse] = await Promise.all([
      page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/cancel` && r.request().method() === "POST"),
      panel.getByRole("button", { name: "取消组导出任务", exact: true }).click(),
    ]);
    const cancel = await responseValue(cancelResponse, setup.actorId);
    expect(cancel).toMatchObject({ taskID: operation.create.taskID, task: { state: "cancelled" } });
    await expect(panel).toContainText("服务器已确认取消，没有可下载文件");
    await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toHaveCount(0);
    const recovered = await receiptAndDetail(page, setup.actorId, operation.create.taskID);
    const database = await terminalExport(operation.create.taskID);
    expect(database).toMatchObject({ state: "cancelled", error: "cancelled", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    const unavailable = await unavailableDownload(context, operation.create.taskID);
    const resume = await resumeOwnedExportWorker(context, pause);
    const postResume = { ...await receiptAndDetail(page, setup.actorId, operation.create.taskID), database: await terminalExport(operation.create.taskID), unavailableDownload: await unavailableDownload(context, operation.create.taskID) };
    expect(postResume.database).toEqual(database);
    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    expect(downloads).toBe(0);
    // Capture the actual cancelled UI after the two resumed queue cycles and
    // immutable attempt-zero database check, without adding database data to UI.
    const cancelledStatus = panel.getByRole("status").filter({ hasText: "服务器已确认取消，没有可下载文件" });
    await cancelledStatus.scrollIntoViewIfNeeded();
    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });
    await expect(panel.getByText(`任务 ID：${operation.create.taskID}`, { exact: true })).toBeInViewport({ ratio: 1 });
    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });
    const image = await screenshot(page, info, "cancelled-terminal.png");
    await writeTerminalEvidence(info, setup, "queuedCancel", [{ name: "cancelled", ...operation, request: operation.create.request, queued, cancel, ...recovered, database, postResume, unavailableDownload: unavailable, ui: { downloadButtons: await panel.getByRole("button", { name: "下载组 XLSX", exact: true }).count(), downloadEvents: downloads }, screenshot: image }], { pause, resume });
  } finally { await restoreOwnedExportWorker(); }
});
