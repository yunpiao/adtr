import { test, expect, type Page, type Request as BrowserRequest, type Response } from "@playwright/test";
import { expectSpecializedTaskReadOnly } from "./specialized-task-controls";
import { proof } from "./group-assets-v3-fixture";
import { navigateTo } from "./navigation";
import { exportPrefix, hash, responseValue } from "./user-asset-export-v3-helpers";
import { chooseColumns, createExport, panelFor, pauseOwnedExportWorker, receiptAndDetail, restoreOwnedExportWorker, resumeOwnedExportWorker, safeColumns, screenshot, terminalExport, terminalSetup, unavailableDownload, writeTerminalEvidence } from "./user-asset-export-v3-terminal-helpers";

const locatorKey = "adtr.user-assets-v3.export-recovery";
type MarkerWitness = { calls: number; matched: boolean; observationErrors: number };
type WitnessWindow = Window & { __adtrUserCancelMarkerWitness?: MarkerWitness };

type QueuedCancelPhase =
  | "setup"
  | "pause"
  | "create"
  | "default-read-only"
  | "new-tab"
  | "dedicated-original-detail"
  | "dedicated-cancel"
  | "original-asset-receipt"
  | "resume-owned-worker"
  | "post-resume-receipt-detail"
  | "post-resume-database"
  | "post-resume-download-unavailable"
  | "post-resume-database-invariant"
  | "post-resume-detail-invariant"
  | "post-resume-download-events"
  | "old-panel-status-scroll"
  | "old-panel-status-viewport"
  | "old-panel-task-id-viewport"
  | "old-panel-create-button-viewport"
  | "new-entry-terminal-pixels"
  | "evidence";

// Join both owned branches before reporting failure or closing the tab. Never
// retry the requested action or replace the original browser request.
async function observedAction(page: Page, predicate: (response: Response) => boolean, action: () => Promise<unknown>) {
  const results = await Promise.allSettled([
    page.waitForResponse(predicate, { timeout: 30_000 }), Promise.resolve().then(action),
  ] as const);
  if (results[0].status !== "fulfilled" || results[1].status !== "fulfilled")
    throw new Error("Original user export browser checkpoint failed");
  return results[0].value;
}
async function observeMarkerBeforeCancel(page: Page, key: string, expected: string) {
  // Read only the selected marker, before native fetch. No request body/proof,
  // changed arguments, route, replay, cache change or fabricated response.
  // Observation failure is counted but cannot suppress the native request.
  await page.addInitScript(({ key, expected, path }) => {
    const target = window as WitnessWindow;
    const witness: MarkerWitness = { calls: 0, matched: true, observationErrors: 0 };
    target.__adtrUserCancelMarkerWitness = witness;
    const original = window.fetch;
    window.fetch = function (this: Window, ...args: Parameters<typeof original>) {
      try {
        const input = args[0];
        const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
        const method = (args[1]?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
        if (url.origin === window.location.origin && url.pathname === path && method === "POST") {
          witness.calls++;
          witness.matched = witness.matched && localStorage.getItem(key) === expected;
        }
      } catch { witness.observationErrors++; }
      return original.apply(this, args);
    };
  }, { key, expected, path: `${exportPrefix}/cancel` });
}

test.use({ trace: "off", screenshot: "off", video: "off" });
test("queued export is cancelled from its original task without a tab locator and never executes after Worker resumes", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  let phase: QueuedCancelPhase = "setup", failure: QueuedCancelPhase | "" = "", cleanupFailed = false, restoreWorker = false;
  let taskPage: Page | undefined;
  let downloads = 0;
  const downloaded = () => { downloads++; };
  const businessRequests: string[] = [], genericWrites: string[] = [];
  let wrongDetailTarget = false, requestObservationFailed = false, originalTaskID = "";
  const requested = (request: BrowserRequest) => {
    try {
      const url = new URL(request.url()), method = request.method();
      if (url.pathname === exportPrefix || url.pathname.startsWith(`${exportPrefix}/`)) {
        businessRequests.push(`${method} ${url.pathname}`);
        if (url.pathname === `${exportPrefix}/detail` && url.searchParams.get("taskID") !== originalTaskID)
          wrongDetailTarget = true;
      }
      if (/^\/api\/tasks(?:\/|$)/u.test(url.pathname) && !["GET", "HEAD"].includes(method))
        genericWrites.push(`${method} ${url.pathname}`);
    } catch { requestObservationFailed = true; }
  };
  page.on("download", downloaded);
  try {
    expect(process.env.ADTR_E2E_USER_ASSET_EXPORT_V3_TERMINAL).toBe("user-asset-export-v3-cancel");
    const setup = await terminalSetup(page, context), panel = panelFor(page);
    await chooseColumns(page, safeColumns);
    // Real source collection is finished before this owned export Worker pauses.
    phase = "pause"; restoreWorker = true;
    const pause = await pauseOwnedExportWorker();
    phase = "create";
    const operation = await createExport(page, setup);
    originalTaskID = operation.create.taskID;
    expect(operation.create.task.state).toBe("queued");
    await expect(panel).toContainText("等待执行");
    const queued = await terminalExport(originalTaskID);
    expect(queued).toMatchObject({ state: "queued", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    const originalLocator = await page.evaluate((key) => sessionStorage.getItem(key), locatorKey);
    expect(originalLocator !== null, "original tab has its actual submission locator").toBe(true);
    phase = "default-read-only";
    await expectSpecializedTaskReadOnly(context, {
      actorId: setup.actorId, taskUUID: originalTaskID, taskName: "user_assets.v3.export", state: "queued",
    }, setup.fixture.safe);

    phase = "new-tab";
    taskPage = await context.newPage();
    taskPage.on("request", requested); taskPage.on("download", downloaded);
    await taskPage.setViewportSize({ width: 1440, height: 900 });
    const request = operation.create.request;
    // Independent fixed order, including every original filter and column.
    const requestSHA256 = hash(JSON.stringify({ domainId: request.domainId,
      expectedRevision: request.expectedRevision, expectedCredentialRevision: request.expectedCredentialRevision,
      observationId: request.observationId, search: request.search, sAMAccountName: request.sAMAccountName,
      startTm: request.startTm, endTm: request.endTm, creatTm: request.creatTm, screenColumns: request.screenColumns }));
    const markerKey = `adtr.user-export-cancel.v1.${setup.actorId}.${originalTaskID}`;
    const expectedMarker = JSON.stringify({ version: 1, kind: "user_assets.v3.export", actorID: setup.actorId,
      taskID: originalTaskID, domainId: request.domainId, requestSHA256 });
    await observeMarkerBeforeCancel(taskPage, markerKey, expectedMarker);
    const session = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === "/api/auth/me" && r.request().method() === "GET",
      () => taskPage!.goto("/"));
    expect(session.status()).toBe(200);
    // Never retain/report the profile or its CSRF token.
    expect((await session.json()).ID === setup.actorId, "same authenticated owner").toBe(true);
    expect(await taskPage.evaluate(() => window.opener === null), "fresh tab has no opener").toBe(true);
    expect(await taskPage.evaluate((key) => sessionStorage.getItem(key) === null, locatorKey), "no locator cloned or seeded").toBe(true);
    await taskPage.bringToFront();
    await navigateTo(taskPage, "后台任务");
    await expect(taskPage.getByRole("table", { name: "任务列表", exact: true })).toBeVisible();
    await taskPage.locator(".task-filter-panel > summary").click();
    await taskPage.getByLabel("任务种类筛选", { exact: true }).fill("user_assets.v3.export");
    await taskPage.getByRole("button", { name: "筛选任务", exact: true }).click();
    const generic = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === "/api/tasks/detail" && new URL(r.url()).searchParams.get("taskUUID") === originalTaskID && r.request().method() === "GET",
      () => taskPage!.getByRole("button", { name: `详情 ${originalTaskID}`, exact: true }).click());
    expect(generic.status()).toBe(200);
    const genericDetail = await generic.json();
    setup.fixture.safe(genericDetail);
    expect(genericDetail.task).toMatchObject({ taskUUID: originalTaskID, taskName: "user_assets.v3.export", state: "queued" });
    const taskDetail = taskPage.getByRole("region", { name: "任务执行详情", exact: true });
    await expect(taskDetail.locator('input[type="password"], input[name="totpCode"], pre')).toHaveCount(0);
    for (const name of ["请求取消任务", "创建恢复任务", "确认请求取消", "确认恢复任务"])
      await expect(taskDetail.getByRole("button", { name, exact: true })).toHaveCount(0);
    expect(businessRequests, "collapsed task detail has no export business request").toEqual([]);

    phase = "dedicated-original-detail";
    const control = taskPage.getByRole("region", { name: "任务原用户导出取消", exact: true });
    const detailResponse = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === `${exportPrefix}/detail` && new URL(r.url()).searchParams.get("taskID") === originalTaskID && r.request().method() === "GET",
      () => control.getByRole("button", { name: "核对原用户导出取消", exact: true }).click());
    const originalDetail = await responseValue(detailResponse, setup.actorId);
    expect(detailResponse.headers()["x-content-type-options"]).toBe("nosniff");
    expect(originalDetail).toMatchObject({ taskID: originalTaskID, request, task: { state: "queued" } });
    await expect(control.getByRole("button", { name: "取消原用户导出任务", exact: true })).toBeEnabled();
    phase = "dedicated-cancel";
    await proof(taskPage, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);
    const cancelResponse = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === `${exportPrefix}/cancel` && r.request().method() === "POST",
      () => control.getByRole("button", { name: "取消原用户导出任务", exact: true }).click());
    const cancel = await responseValue(cancelResponse, setup.actorId);
    expect(cancelResponse.headers()["x-content-type-options"]).toBe("nosniff");
    expect(cancel).toMatchObject({ taskID: originalTaskID, task: { taskUUID: originalTaskID,
      taskName: "user_assets.v3.export", domainId: request.domainId, state: "cancelled" } });
    // A real generic-state transition may invalidate the dedicated preparation.
    // Wait for that existing poll, then explicitly read the original task again;
    // never stop polling or treat the POST response as a fresh detail receipt.
    await expect(taskDetail.locator(".task-state")).toHaveAttribute("data-state", "cancelled");
    const reconciledResponse = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === `${exportPrefix}/detail` && new URL(r.url()).searchParams.get("taskID") === originalTaskID && r.request().method() === "GET",
      () => control.getByRole("button", { name: "读取原用户导出当前状态", exact: true }).click());
    const reconciled = await responseValue(reconciledResponse, setup.actorId);
    expect(reconciled).toMatchObject({ taskID: originalTaskID, request, downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    await expect(control).toContainText("服务器已确认取消，没有可下载文件");
    await expect(control.locator('input[type="password"], input[name="totpCode"]')).toHaveCount(0);
    expect(await taskPage.evaluate(() => (window as WitnessWindow).__adtrUserCancelMarkerWitness))
      .toEqual({ calls: 1, matched: true, observationErrors: 0 });

    phase = "original-asset-receipt";
    await page.bringToFront();
    const recovered = await receiptAndDetail(page, setup.actorId, originalTaskID);
    await expect(panel).toContainText("服务器已确认取消，没有可下载文件");
    await expect(panel.getByRole("button", { name: "下载用户 XLSX", exact: true })).toHaveCount(0);
    expect(await page.evaluate(({ key, expected }) => sessionStorage.getItem(key) === expected,
      { key: locatorKey, expected: originalLocator }), "original locator unchanged").toBe(true);
    const database = await terminalExport(originalTaskID);
    expect(database).toMatchObject({ state: "cancelled", error: "cancelled", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    const unavailable = await unavailableDownload(context, originalTaskID);
    phase = "resume-owned-worker";
    const resume = await resumeOwnedExportWorker(context, pause);
    phase = "post-resume-receipt-detail";
    const postResumeReads = await receiptAndDetail(page, setup.actorId, originalTaskID);
    phase = "post-resume-database";
    const postResumeDatabase = await terminalExport(originalTaskID);
    phase = "post-resume-download-unavailable";
    const postResumeUnavailableDownload = await unavailableDownload(context, originalTaskID);
    const postResume = { ...postResumeReads, database: postResumeDatabase, unavailableDownload: postResumeUnavailableDownload };
    phase = "post-resume-database-invariant";
    expect(postResume.database).toEqual(database);
    phase = "post-resume-detail-invariant";
    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    phase = "post-resume-download-events";
    expect(downloads).toBe(0);
    // Keep the old asset-panel DOM and visibility checks after both queue cycles.
    const cancelledStatus = panel.getByRole("status").filter({ hasText: "服务器已确认取消，没有可下载文件" });
    phase = "old-panel-status-scroll";
    await cancelledStatus.scrollIntoViewIfNeeded();
    phase = "old-panel-status-viewport";
    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });
    phase = "old-panel-task-id-viewport";
    await expect(panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })).toBeInViewport({ ratio: 1 });
    phase = "old-panel-create-button-viewport";
    try {
      await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });
    } catch (originalFailure) {
      let diagnosticTimer: ReturnType<typeof setTimeout> | undefined;
      try {
        // Failure-only, one-shot DOM reads; count 2 means two or more matches.
        // Never emit text, identity, DOM, requests, or the original exception.
        const measure = (elements: Element[]) => {
          const count = elements.length === 0 ? 0 : elements.length === 1 ? 1 : 2;
          const box = elements.length === 1 ? elements[0].getBoundingClientRect() : null;
          const rect = box && [box.x, box.y, box.width, box.height].every((value) => Number.isFinite(value) && Math.abs(value) <= 1_000_000)
            && box.width >= 0 && box.height >= 0
            ? { x: box.x, y: box.y, width: box.width, height: box.height } : null;
          return { count, rect };
        };
        const size = page.viewportSize();
        const viewport = size && [size.width, size.height].every((value) => Number.isFinite(value) && value >= 0 && value <= 1_000_000)
          ? { width: size.width, height: size.height } : null;
        const deadline = new Promise<never>((_, reject) => {
          diagnosticTimer = setTimeout(() => reject(new Error("Viewport diagnostic deadline")), 1_000);
        });
        const [status, taskIdentity, nextExport] = await Promise.race([
          Promise.all([
            cancelledStatus.evaluateAll(measure),
            panel.getByText(`任务 ID：${originalTaskID}`, { exact: true }).evaluateAll(measure),
            panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).evaluateAll(measure),
          ]),
          deadline,
        ]);
        console.log("User export terminal viewport diagnostic: " + JSON.stringify({ viewport, status, taskIdentity, nextExport }));
      } catch {
        console.log("User export terminal viewport diagnostic unavailable");
      } finally {
        if (diagnosticTimer !== undefined) clearTimeout(diagnosticTimer);
      }
      throw originalFailure;
    }

    phase = "new-entry-terminal-pixels";
    await taskPage.bringToFront();
    const freshResponse = await observedAction(taskPage,
      (r) => new URL(r.url()).pathname === `${exportPrefix}/detail` && new URL(r.url()).searchParams.get("taskID") === originalTaskID && r.request().method() === "GET",
      () => control.getByRole("button", { name: "读取原用户导出当前状态", exact: true }).click());
    const fresh = await responseValue(freshResponse, setup.actorId);
    expect(fresh).toMatchObject({ taskID: originalTaskID, request, downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    const taskCancelledStatus = control.getByRole("status").filter({ hasText: "服务器已确认取消，没有可下载文件" });
    await taskCancelledStatus.scrollIntoViewIfNeeded();
    await expect(taskCancelledStatus).toBeInViewport({ ratio: 1 });
    const originalID = control.locator("dt").filter({ hasText: /^原任务 ID$/u }).locator("xpath=following-sibling::dd[1]");
    await expect(originalID).toHaveText(originalTaskID);
    await expect(originalID).toBeInViewport({ ratio: 1 });
    await expect(taskPage.locator('input[type="password"], input[name="totpCode"]')).toHaveCount(0);
    expect(await taskPage.evaluate((key) => sessionStorage.getItem(key) === null, locatorKey), "new entry never writes a locator").toBe(true);
    expect(await taskPage.evaluate((key) => localStorage.getItem(key) === null, markerKey), "verified terminal marker removed").toBe(true);
    expect(businessRequests.filter((value) => value === `POST ${exportPrefix}/cancel`)).toHaveLength(1);
    expect(businessRequests.every((value) => [`GET ${exportPrefix}/detail`, `POST ${exportPrefix}/cancel`].includes(value)), "no create/receipt/download from new entry").toBe(true);
    expect(genericWrites, "no generic task writes").toEqual([]);
    expect(wrongDetailTarget || requestObservationFailed, "all observed reads bind original task").toBe(false);
    expect(downloads).toBe(0);
    // Same queued-cancel artifact, now the actual new task entry, never a mock.
    const image = await screenshot(taskPage, info, "cancelled-terminal.png");
    phase = "evidence";
    await writeTerminalEvidence(info, setup, "queuedCancel", [{ name: "cancelled", ...operation,
      request, queued, cancel, ...recovered, database, postResume, unavailableDownload: unavailable,
      ui: { downloadButtons: await panel.getByRole("button", { name: "下载用户 XLSX", exact: true }).count(), downloadEvents: downloads }, screenshot: image }], { pause, resume });
  } catch { failure = phase; }
  finally {
    page.off("download", downloaded);
    if (taskPage) {
      taskPage.off("request", requested); taskPage.off("download", downloaded);
      try { await taskPage.close(); } catch { cleanupFailed = true; }
    }
    if (restoreWorker) {
      try { await restoreOwnedExportWorker(); } catch { cleanupFailed = true; }
    }
  }
  // Raw Playwright errors can contain Cookie call logs. Do not attach a cause.
  if (failure || cleanupFailed) throw new Error(`User export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}`);
});
