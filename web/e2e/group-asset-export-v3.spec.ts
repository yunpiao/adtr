import { test, expect } from "@playwright/test";
import { expectSpecializedTaskReadOnly } from "./specialized-task-controls";
import { readFile, writeFile } from "node:fs/promises";
import { setupGroupFixture, proof, readJSON } from "./group-assets-v3-fixture";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";
import { exportPrefix, downloadHeaderNames, hash, switchToExportOwner, responseValue, assetAction, openAssets, storedExport, sensitiveBudget, evidenceProvenance } from "./group-asset-export-v3-helpers";

// Only the dedicated isolated group dictionary-3 LDAP/Worker/PG runner may run
// this suite. No routes, response bodies, clock, grants or task states are mocked.
// Secrets are never recorded in trace/video/failure screenshots.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("group asset export real source, owner MFA, Worker persistence and XLSX download", async ({ page, context }, info) => {
  // Existing group-assets-v3 setup budget; no increase to timeout or concurrency.
  test.setTimeout(900_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await observeDirectoryV2Responses(context);
  expect(process.env.ADTR_E2E_GROUP_ASSET_EXPORT_V3).toBe("true");
  const fixture = await setupGroupFixture(page, context, {});
  const source = await fixture.evidence();
  expect(source.taskUUID).toBe(fixture.taskUUID);
  expect(source.actorId).toBe(String(fixture.actorId));
  expect(source.dictionaryVersion).toBe(3);
  expect(source.sha256).toBe(hash(Buffer.from(source.bodyBase64, "base64")));
  await switchToExportOwner(page, fixture.bootstrap);
  const actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).not.toBe(fixture.actorId);
  const before = await sensitiveBudget(actorId, fixture.actorId);
  expect(before).toMatchObject({ owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0 });
  const initial = await openAssets(page, actorId);
  expect(initial).toMatchObject({ dictionaryVersion: 3, available: true, observationId: fixture.taskUUID, page: { pageSize: 10, total: 23 } });
  await page.locator("summary").filter({ hasText: /^创建时间排序$/u }).click();
  await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
  const defaults = ["guid", "sAMAccountName", "sid", "dn", "whenCreated"];
  const cases = [];
  for (const name of ["default", "allMatched", "empty"] as const) {
    if (name !== "default") await page.getByRole("button", { name: "关闭导出面板", exact: true }).click();
    await page.getByLabel("组资产关键词", { exact: true }).fill(name !== "empty" ? "group-" : "absent-group");
    const list = await assetAction(page, actorId, () => page.getByRole("button", { name: "应用组筛选", exact: true }).click());
    expect(list.observationId).toBe(fixture.taskUUID);
    expect(list.page.total).toBe(name !== "empty" ? 23 : 0);
    await page.getByRole("button", { name: "导出匹配组 XLSX", exact: true }).click();
    const panel = page.getByRole("region", { name: "组资产导出", exact: true });
    if (name !== "default") {
      // A terminal receipt remains recoverable across a filter change. Only the
      // explicit new-operation control may replace its locator/idempotency key.
      await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).click();
    }
    await expect(panel).toContainText("全部匹配组，不限当前页");
    await expect(panel.getByText(fixture.taskUUID, { exact: true })).toBeVisible();
    const columns = name === "default" ? defaults : ["dn", "guid"];
    if (name !== "default") {
      for (const column of defaults) await panel.getByLabel(`导出列：${column}`, { exact: true }).uncheck();
      for (const column of columns) await panel.getByLabel(`导出列：${column}`, { exact: true }).check();
    }
    await expect(panel.getByLabel("导出列顺序", { exact: true })).toHaveText(columns.join(" → "));
    await proof(page, fixture.bootstrap.password, fixture.bootstrap.auth);
    const [createdResponse] = await Promise.all([
      page.waitForResponse((r) => new URL(r.url()).pathname === exportPrefix && r.request().method() === "POST"),
      panel.getByRole("button", { name: "创建组导出", exact: true }).click(),
    ]);
    const created = await responseValue(createdResponse, actorId);
    fixture.safe(created);
    expect(created).toMatchObject({ replayed: false, task: { taskName: "group_assets.v3.export" } });
    expect(created.request.screenColumns).toEqual(columns);
    await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toBeVisible({ timeout: 30_000 });
    await expect(panel.getByRole("progressbar", { name: "组导出持久化进度", exact: true })).toHaveAttribute("value", "100");
    const [receiptResponse] = await Promise.all([
      page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/receipt`),
      panel.getByRole("button", { name: "核对原导出回执", exact: true }).click(),
    ]);
    const receipt = await responseValue(receiptResponse, actorId);
    expect(receipt.taskID).toBe(created.taskID);
    await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toBeEnabled();
    // Download triggers the UI's own fresh session/detail checks and its bounded
    // length/SHA verification before this actual browser Blob download event.
    const [download, downloadResponse, detailResponse] = await Promise.all([
      page.waitForEvent("download"),
      page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/download`),
      page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/detail`),
      panel.getByRole("button", { name: "下载组 XLSX", exact: true }).click(),
    ]);
    const detail = await responseValue(detailResponse, actorId);
    expect(downloadResponse.status()).toBe(200);
    expect(detail).toMatchObject({ taskID: created.taskID, downloadReady: true, downloadStatus: "ready", task: { state: "succeeded", progress: 100 } });
    const filename = `group-assets-v3-${created.taskID}.xlsx`;
    expect(download.suggestedFilename()).toBe(filename);
    await download.saveAs(info.outputPath(filename));
    const bytes = await readFile(info.outputPath(filename));
    expect(detail.artifact).toEqual({ filename, sha256: hash(bytes), byteCount: bytes.length, rowCount: list.page.total });
    await expect(panel.locator('input[type="password"]')).toHaveCount(0);
    fixture.safe(await page.locator("body").innerText());
    const readyStatus = panel.getByRole("status").filter({ hasText: "完整文件已就绪" });
    await readyStatus.scrollIntoViewIfNeeded();
    await expect(readyStatus).toBeInViewport({ ratio: 1 });
    await expect(panel.getByText(`任务 ID：${created.taskID}`, { exact: true })).toBeInViewport({ ratio: 1 });
    await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toBeInViewport({ ratio: 1 });
    const screenshot = `${name}-ready.png`;
    await page.screenshot({ path: info.outputPath(screenshot), fullPage: false });
    cases.push({ name, request: created.request, create: created, receipt, detail, download: { filename, headers: Object.fromEntries(downloadHeaderNames.map((key) => [key, downloadResponse.headers()[key]])) }, database: await storedExport(created.taskID), pageSize: list.page.pageSize, firstPageCount: list.list.length, screenshot: { filename: screenshot, sha256: hash(await readFile(info.outputPath(screenshot))) } });
    await info.attach(filename, { path: info.outputPath(filename), contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" });
    await info.attach(screenshot, { path: info.outputPath(screenshot), contentType: "image/png" });
    if (name === "empty")
      await expectSpecializedTaskReadOnly(
        context,
        {
          actorId,
          taskUUID: created.taskID,
          taskName: "group_assets.v3.export",
          state: "succeeded",
        },
        fixture.safe,
      );
  }
  const after = await sensitiveBudget(actorId, fixture.actorId);
  expect(after).toEqual({ ...before, owner: 10, exportTasks: 3 });
  const evidence = { format: 1, provenance: evidenceProvenance(), source, actorId, sensitiveBudget: { before, after }, cases };
  fixture.safe(evidence);
  fixture.safe(fixture.consoles);
  const output = info.outputPath("group-asset-export-v3-evidence.json");
  await writeFile(output, JSON.stringify(evidence, null, 2));
  await info.attach("group-asset-export-v3-evidence", { path: output, contentType: "application/json" });
});
