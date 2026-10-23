import { test, expect, type Request } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { navigateTo } from "./navigation";
import { setupComputerFixture, proof, readJSON } from "./computer-assets-v3-fixture";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";
import { sensitiveBudget, switchToExportOwner } from "./computer-asset-export-v3-helpers";
import type { ExportRequest } from "../src/computer-asset-export-v3-api";
import { suite, exportPath, columns, sha, downloadHeaders, requireEvidence, uuid, fileReference, captureProvenance, readSourceEvidence, readExportEvidence, sourceCellSummary, receiptProjection, observeAction, multichunkSetupFailureSuffix } from "./computer-asset-export-v3-multichunk-evidence";

// One isolated real LDAP/Worker/PG export. The original small suite is unchanged.
// No trace, video, automatic failure image, proof, body or raw error is uploaded.
test.use({ trace: "off", screenshot: "off", video: "off" });
type Phase = "fixture_guard" | "provenance" | "setup" | "source" | "owner" | "ledger_before" | "open_assets" | "filter" | "create" | "ready" | "receipt" | "download" | "database" | "ledger_after" | "screenshot" | "evidence";
const exactQuery = (expected: Record<string, string>) => (query: URLSearchParams) => [...query].length === Object.keys(expected).length && Object.entries(expected).every(([key, value]) => query.getAll(key).length === 1 && query.get(key) === value);

test("computer XLSX actual multi-chunk persistence and full-file integrity", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  let phase: Phase = "fixture_guard";
  let writes = 0, unsafeRequest = false;
  const countWrites = (request: Request) => {
    try { const path = new URL(request.url()).pathname; if (path.startsWith(exportPath) && request.method() !== "GET") { writes++; if (path !== exportPath || request.method() !== "POST") unsafeRequest = true; } }
    catch { unsafeRequest = true; }
  };
  try {
    requireEvidence(process.env.ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK === "true" && process.env.ADTR_E2E_LDAP_DIRECTORY_COMPUTER_EXPORT_MULTICHUNK_V3 === "true");
    await page.setViewportSize({ width: 1440, height: 900 });
    await observeDirectoryV2Responses(context);
    phase = "provenance";
    const provenance = await captureProvenance(info.outputPath("provenance.json"));
    phase = "setup";
    const fixture = await setupComputerFixture(page, context, {});
    // Deliberately never call fixture.evidence(): its old whole-body buffer is small.
    phase = "source";
    const sourcePath = info.outputPath("source-observation.json");
    const source = await readSourceEvidence(fixture.taskUUID, fixture.actorId, sourcePath);
    requireEvidence(source.domainId === fixture.domainId && source.sourcePins.accountId === fixture.accountId);
    const cells = sourceCellSummary(await readFile(sourcePath));
    phase = "owner";
    await switchToExportOwner(page, fixture.bootstrap);
    const profile = await readJSON(context, "/api/auth/me");
    const actorId = profile.ID as number;
    requireEvidence(Number.isSafeInteger(actorId) && actorId > 0 && actorId !== fixture.actorId);
    phase = "ledger_before";
    const before = await sensitiveBudget(actorId, fixture.actorId);
    expect(before).toEqual({ schemaVersion: 23, owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0 });
    page.on("request", countWrites);
    const initialQuery = { domainId: source.domainId, expectedRevision: source.selection.revision, expectedCredentialRevision: source.selection.credentialRevision, search: "", pageIdx: "1", pageSize: "10" };
    phase = "open_assets";
    await navigateTo(page, "计算机资产");
    await expect(page).toHaveURL(/#computer-assets-v3$/u);
    await page.getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true }).check();
    const opened = await observeAction(page, actorId, [{ path: "/api/computer-assets/v3", method: "GET", query: exactQuery(initialQuery) }], () => page.getByRole("button", { name: "核对并打开连接详情", exact: true }).click());
    requireEvidence(opened.values[0].available === true && opened.values[0].observationId === fixture.taskUUID && opened.values[0].dictionaryVersion === 3);
    phase = "filter";
    await page.locator("summary").filter({ hasText: /^创建时间筛选与排序$/u }).click();
    await page.getByLabel("计算机资产关键词", { exact: true }).fill("computer-");
    await page.getByLabel("创建时间开始（含）", { exact: true }).fill("2026-01-01T00:00:00Z");
    await page.getByLabel("创建时间结束（不含）", { exact: true }).fill("2026-01-02T00:00:00Z");
    await page.getByLabel("创建时间排序", { exact: true }).selectOption("asc");
    const filteredQuery = { ...initialQuery, observationId: fixture.taskUUID, search: "computer-", startTm: "2026-01-01T00:00:00Z", endTm: "2026-01-02T00:00:00Z", creatTm: "asc" };
    const filtered = await observeAction(page, actorId, [{ path: "/api/computer-assets/v3", method: "GET", query: exactQuery(filteredQuery) }], () => page.getByRole("button", { name: "应用计算机筛选", exact: true }).click());
    const actualList = filtered.values[0];
    requireEvidence(actualList.available === true && actualList.observationId === fixture.taskUUID && actualList.dictionaryVersion === 3 && actualList.page.pageIdx === 1 && actualList.page.pageSize === 10 && actualList.page.total === 1020 && actualList.list.length === 10);
    const list = { observationId: actualList.observationId, dictionaryVersion: actualList.dictionaryVersion, pageSize: actualList.page.pageSize, firstPageCount: actualList.list.length, total: actualList.page.total };
    const request: ExportRequest = { domainId: source.domainId, expectedRevision: source.selection.revision, expectedCredentialRevision: source.selection.credentialRevision, observationId: fixture.taskUUID, search: "computer-", startTm: "2026-01-01T00:00:00Z", endTm: "2026-01-02T00:00:00Z", creatTm: "asc", screenColumns: [...columns] };
    await page.getByRole("button", { name: "导出匹配计算机 XLSX", exact: true }).click();
    const panel = page.getByRole("region", { name: "计算机资产导出", exact: true });
    await expect(panel).toContainText("全部匹配计算机，不限当前页");
    await expect(panel.getByText(fixture.taskUUID, { exact: true })).toBeVisible();
    await expect(panel.getByLabel("导出列顺序", { exact: true })).toHaveText(columns.join(" → "));
    phase = "create";
    await proof(page, fixture.bootstrap.password, fixture.bootstrap.auth);
    const started = performance.now();
    const submitted = await observeAction(page, actorId, [{ path: exportPath, method: "POST", query: exactQuery({}) }], () => panel.getByRole("button", { name: "创建计算机导出", exact: true }).click());
    fixture.safe(submitted.values[0]);
    const create = receiptProjection(submitted.values[0], request, "create");
    requireEvidence(writes === 1 && !unsafeRequest);
    phase = "ready";
    const downloadButton = panel.getByRole("button", { name: "下载计算机 XLSX", exact: true });
    await expect(downloadButton).toBeVisible({ timeout: 30_000 });
    await expect(panel.getByRole("progressbar", { name: "计算机导出持久化进度", exact: true })).toHaveAttribute("value", "100");
    // Read only the original recovery locator; never clear or replace it.
    const locator = await page.evaluate(() => JSON.parse(sessionStorage.getItem("adtr.computer-assets-v3.export-recovery") ?? "null"));
    requireEvidence(locator?.actorID === actorId && locator.domainId === source.domainId); uuid(locator.idempotencyKey);
    phase = "receipt";
    const receiptRead = await observeAction(page, actorId, [{ path: `${exportPath}/receipt`, method: "GET", query: exactQuery({ domainId: source.domainId, idempotencyKey: locator.idempotencyKey }) }], () => panel.getByRole("button", { name: "核对原导出回执", exact: true }).click());
    fixture.safe(receiptRead.values[0]);
    const receipt = receiptProjection(receiptRead.values[0], request, "receipt", create.taskID);
    await expect(downloadButton).toBeEnabled();
    phase = "download";
    const query = exactQuery({ taskID: create.taskID });
    const retrieved = await observeAction(page, actorId, [{ path: `${exportPath}/detail`, method: "GET", query }, { path: `${exportPath}/download`, method: "GET", query, binary: true }], () => downloadButton.click(), true);
    fixture.safe(retrieved.values[0]);
    const detail = receiptProjection(retrieved.values[0], request, "detail", create.taskID);
    requireEvidence("artifact" in detail && detail.artifact && retrieved.download);
    const filename = `computer-assets-v3-${create.taskID}.xlsx`;
    requireEvidence(retrieved.download.suggestedFilename() === filename);
    await retrieved.download.saveAs(info.outputPath("download.xlsx"));
    const submitToDownloadMs = Math.round(performance.now() - started);
    const downloaded = await readFile(info.outputPath("download.xlsx"));
    requireEvidence(downloaded.length > 1048576 && downloaded.length <= (32 << 20));
    expect(detail.artifact).toEqual({ rowCount: 1020, byteCount: downloaded.length, sha256: sha(downloaded), filename });
    const headers = retrieved.headers[1];
    requireEvidence(downloadHeaders.every((key) => typeof headers[key] === "string") && Object.keys(headers).length === downloadHeaders.length);
    requireEvidence(headers["content-length"] === String(downloaded.length) && headers["x-adtr-artifact-sha256"] === sha(downloaded) && headers["x-adtr-row-count"] === "1020" && headers["content-disposition"] === `attachment; filename="${filename}"`);
    phase = "database";
    const database = await readExportEvidence(create.taskID, actorId, source, request, info.outputPath("database-export.xlsx"));
    const databaseBytes = await readFile(info.outputPath("database-export.xlsx"));
    requireEvidence(databaseBytes.equals(downloaded));
    phase = "ledger_after";
    const after = await sensitiveBudget(actorId, fixture.actorId);
    expect(after).toEqual({ ...before, owner: 8, exportTasks: 1 });
    requireEvidence(writes === 1 && !unsafeRequest);
    phase = "screenshot";
    await expect(panel.locator('input[type="password"]')).toHaveCount(0);
    await expect(page.getByLabel("未使用的认证器验证码", { exact: true })).toHaveCount(0);
    const status = panel.getByRole("status").filter({ hasText: "完整文件已就绪" });
    const identity = panel.getByText(`任务 ID：${create.taskID}`, { exact: true });
    const rowCount = panel.getByText(`实际导出 1020 行；${downloaded.length} 字节`, { exact: true });
    await status.scrollIntoViewIfNeeded();
    await status.evaluate((element) => element.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" }));
    for (const target of [status, identity, rowCount, downloadButton]) { await expect(target).toHaveCount(1); await expect(target).toBeVisible(); await expect(target).toBeInViewport({ ratio: 1 }); }
    requireEvidence(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    fixture.safe(await page.locator("body").innerText()); fixture.safe(fixture.consoles);
    await page.screenshot({ path: info.outputPath("ready.png"), fullPage: false });
    phase = "evidence";
    const screenshot = fileReference("ready.png", await readFile(info.outputPath("ready.png")));
    const evidence = { format: 1, suite, provenance, actorId, source, request, create, receipt, detail, list, database, download: { file: fileReference("download.xlsx", downloaded), suggestedFilename: filename, status: 200, headers }, sensitiveBudget: { before, after }, screenshot, cells, timings: { scope: "browser_export_submit_to_download", submitToDownloadMs } };
    fixture.safe(evidence); requireEvidence(writes === 1 && !unsafeRequest);
    await writeFile(info.outputPath("evidence.json"), JSON.stringify(evidence, null, 2), { flag: "wx" });
    // No info.attach: only the runner's strict seven-file gate may stage uploads.
  } catch {
    let setupDiagnostic = "";
    if (phase === "setup") {
      try { setupDiagnostic = await multichunkSetupFailureSuffix(phase); }
      catch { setupDiagnostic = "; setup_db_snapshot=unavailable"; }
    }
    throw new Error(`Computer multichunk acceptance failed at ${phase}${setupDiagnostic}`);
  }
  finally { page.off("request", countWrites); }
});
