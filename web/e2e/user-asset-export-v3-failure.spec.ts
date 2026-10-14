import { test, expect } from "@playwright/test";
import { allColumns, chooseColumns, createExport, panelFor, receiptAndDetail, recoveryDownload, safeColumns, screenshot, terminalExport, terminalSetup, unavailableDownload, writeTerminalEvidence } from "./user-asset-export-v3-terminal-helpers";

// Actual dedicated malformed-mail TLS LDAP -> observation -> Worker -> PG.
// No task, HTTP, clock, grants, source or XLSX data is fabricated here.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("failed XLSX retains receipt and explicitly recovers with ordered safe subset", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  expect(process.env.ADTR_E2E_USER_ASSET_EXPORT_V3_TERMINAL).toBe("user-asset-export-v3-failure");
  expect(process.env.ADTR_E2E_LDAP_DIRECTORY_USER_EXPORT_FAILURE_V3).toBe("true");
  const setup = await terminalSetup(page, context), panel = panelFor(page);
  let downloads = 0;
  page.on("download", () => { downloads++; });
  await chooseColumns(page, allColumns);
  const operation = await createExport(page, setup);
  await expect(panel).toContainText("任务失败，没有可下载文件", { timeout: 30_000 });
  await expect(panel).toContainText("任务错误代码：export_invalid_text");
  await expect(panel).toContainText("观测中存在 XLSX 无法原样表示的文本");
  await expect(panel.getByRole("button", { name: "下载用户 XLSX", exact: true })).toHaveCount(0);
  const recovered = await receiptAndDetail(page, setup.actorId, operation.create.taskID);
  expect(recovered.detail).toMatchObject({ downloadReady: false, downloadStatus: "failed", task: { state: "failed", error: "export_invalid_text" } });
  const database = await terminalExport(operation.create.taskID);
  expect(database).toMatchObject({ state: "failed", error: "export_invalid_text", attempt: 1, snapshotCount: 0, manifestCount: 0, chunkCount: 0, idempotencyKey: operation.idempotencyKey });
  const failed = { name: "failed", ...operation, request: operation.create.request, ...recovered, database,
    unavailableDownload: await unavailableDownload(context, operation.create.taskID),
    ui: { downloadButtons: await panel.getByRole("button", { name: "下载用户 XLSX", exact: true }).count(), downloadEvents: downloads, error: "export_invalid_text" },
    screenshot: await screenshot(page, info, "failed-terminal.png") };
  expect(downloads).toBe(0);
  // Explicitly replace only the terminal operation; never recollect or mutate
  // the failed immutable request. Selected column order must survive to XLSX.
  await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).click();
  await chooseColumns(page, safeColumns);
  const next = await createExport(page, setup);
  expect(next.idempotencyKey).not.toBe(operation.idempotencyKey);
  expect(next.create.taskID).not.toBe(operation.create.taskID);
  expect(next.create.request).toEqual({ ...operation.create.request, screenColumns: safeColumns });
  const recovery = await recoveryDownload(page, info, setup, next);
  expect(downloads).toBe(1);
  // The old receipt and its cleanup remain durable after the separate success.
  const oldReceiptResponse = await context.request.get(`/api/user-assets/v3/exports/receipt?${new URLSearchParams({ domainId: operation.create.request.domainId, idempotencyKey: operation.idempotencyKey })}`);
  expect(oldReceiptResponse.status()).toBe(200);
  expect(oldReceiptResponse.headers()["x-adtr-user-id"]).toBe(String(setup.actorId));
  expect(oldReceiptResponse.headers()["cache-control"]).toBe("no-store");
  const oldReceipt = await oldReceiptResponse.json();
  expect(oldReceipt.taskID).toBe(operation.create.taskID);
  expect(oldReceipt.task.state).toBe("failed");
  const failedAfterRecovery = await terminalExport(operation.create.taskID);
  expect(failedAfterRecovery).toEqual(database);
  await writeTerminalEvidence(info, setup, "failureRecovery", [{ ...failed, afterRecovery: { receipt: oldReceipt, database: failedAfterRecovery } }, recovery]);
});
