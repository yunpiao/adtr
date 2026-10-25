import { navigateTo } from "./navigation";
import { test, expect, type Request } from "@playwright/test";
import { expectSpecializedTaskReadOnly } from "./specialized-task-controls";
import { readFile, writeFile } from "node:fs/promises";
import { setupGroupFixture, proof, readJSON } from "./group-assets-v3-fixture";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";
import {
  exportPrefix,
  downloadHeaderNames,
  hash,
  switchToExportOwner,
  responseValue,
  openAssets,
  storedExport,
  sensitiveBudget,
  evidenceProvenance,
} from "./user-asset-export-v3-helpers";

// Only the dedicated isolated default dictionary-3 LDAP/Worker/PG runner may run
// this suite. No routes, response bodies, clock, grants or task states are mocked.
// Secrets are never recorded in trace/video/failure screenshots.
test.use({ trace: "off", screenshot: "off", video: "off" });
test("user asset export real source, owner MFA, Worker persistence and XLSX download", async ({
  page,
  context,
}, info) => {
  // Existing user-assets-v3 setup budget; no increase to timeout or concurrency.
  test.setTimeout(900_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await observeDirectoryV2Responses(context);
  const fixture = await setupGroupFixture(page, context, {
    userAssetExport: true,
  });
  const source = await fixture.evidence();
  expect(source.taskUUID).toBe(fixture.taskUUID);
  expect(source.actorId).toBe(String(fixture.actorId));
  expect(source.dictionaryVersion).toBe(3);
  expect(source.sha256).toBe(hash(Buffer.from(source.bodyBase64, "base64")));
  await switchToExportOwner(page, fixture.bootstrap);
  const actorId = (await readJSON(context, "/api/auth/me")).ID as number;
  expect(actorId).not.toBe(fixture.actorId);
  const before = await sensitiveBudget(actorId, fixture.actorId);
  expect(before).toMatchObject({
    owner: 7,
    producer: 9,
    observations: 1,
    directoryUses: 1,
    exportTasks: 0,
  });
  const initial = await openAssets(page, actorId);
  expect(initial).toMatchObject({
    dictionaryVersion: 3,
    available: true,
    observationId: fixture.taskUUID,
    page: { pageSize: 10, total: 12 },
  });
  await page.getByText("精确账户与创建时间筛选", { exact: true }).click();
  await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
  const pins = {
    domainId: source.selection.domainId,
    expectedRevision: source.selection.revision,
    expectedCredentialRevision: source.selection.credentialRevision,
    observationId: fixture.taskUUID,
  };
  // Capture the genuine GET as pairs: duplicates must not disappear through
  // Object.fromEntries. Only these non-secret list query values are retained.
  const applyList = async (
    search: "user-" | "no-synthetic-user-match",
    uacDisableBit?: "set",
  ) => {
    const [response] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === "/api/user-assets/v3" &&
          r.request().method() === "GET",
      ),
      page.getByRole("button", { name: "搜索用户", exact: true }).click(),
    ]);
    const listQueryEntries = [
      ...new URL(response.url()).searchParams.entries(),
    ];
    expect([...listQueryEntries].sort()).toEqual(
      Object.entries({
        ...pins,
        search,
        pageIdx: "1",
        pageSize: "10",
        creatTm: "desc",
        ...(uacDisableBit === undefined ? {} : { uacDisableBit }),
      }).sort(),
    );
    const list = await responseValue(response, actorId);
    const total = search === "user-" && uacDisableBit === undefined ? 12 : 0;
    expect(list).toMatchObject({
      dictionaryVersion: 3,
      available: true,
      selection: source.selection,
      observationId: fixture.taskUUID,
      page: { pageIdx: 1, pageSize: 10, total, totalPage: total ? 2 : 0 },
    });
    expect(list.source).toEqual(initial.source);
    expect(list.list).toHaveLength(Math.min(10, total));
    return { list, listQueryEntries };
  };
  const columns = [
    "mail",
    "guid",
    "sAMAccountName",
    "dn",
    "sid",
    "whenCreated",
  ];
  const cases = [];
  for (const name of ["allMatched", "empty"] as const) {
    const panel = page.getByRole("region", {
      name: "用户资产导出",
      exact: true,
    });
    if (name === "empty") {
      await panel
        .getByRole("button", { name: "关闭导出面板", exact: true })
        .click();
      // Keep keyword-only empty-list coverage. Its former complete XLSX chain
      // is deliberately replaced by UI-selected set within the same two proofs.
      await page
        .getByLabel("用户资产关键词", { exact: true })
        .fill("no-synthetic-user-match");
      await applyList("no-synthetic-user-match");
      await page
        .getByRole("button", { name: "导出匹配用户 XLSX", exact: true })
        .click();
      const another = panel.getByRole("button", {
        name: "按当前列表创建另一导出",
        exact: true,
      });
      await expect(another).toBeEnabled();
      const draftRequests: string[] = [];
      const recordDraft = (request: Request) => {
        const path = new URL(request.url()).pathname;
        if (
          (path === "/api/user-assets/v3" && request.method() === "GET") ||
          (path === exportPrefix && request.method() === "POST")
        )
          draftRequests.push(request.method());
      };
      page.on("request", recordDraft);
      try {
        await page.getByLabel("用户资产关键词", { exact: true }).fill("user-");
        await page
          .getByLabel("本次观测的禁用位", { exact: true })
          .selectOption("set");
        await expect(
          page.getByText("筛选已编辑，尚未应用；请提交搜索后查看详情或翻页。", {
            exact: true,
          }),
        ).toBeVisible();
        await expect(another).toBeDisabled();
        await expect(
          panel.getByRole("button", { name: "创建用户导出", exact: true }),
        ).toHaveCount(0);
        expect(draftRequests).toEqual([]);
      } finally {
        page.off("request", recordDraft);
      }
    } else {
      await page.getByLabel("用户资产关键词", { exact: true }).fill("user-");
    }
    const { list, listQueryEntries } = await applyList(
      "user-",
      name === "empty" ? "set" : undefined,
    );
    // Construct independently before submission; never copy created.request.
    const expectedRequest = {
      ...pins,
      search: "user-",
      sAMAccountName: "",
      startTm: "",
      endTm: "",
      creatTm: "desc",
      screenColumns: columns,
      ...(name === "empty" ? { uacDisableBit: "set" } : {}),
    };
    await page
      .getByRole("button", { name: "导出匹配用户 XLSX", exact: true })
      .click();
    if (name === "empty") {
      await expect(
        page.getByText("已应用禁用位：禁用位已设置", { exact: true }),
      ).toBeVisible();
      await expect(
        page.getByText("此观测在当前筛选下没有匹配用户。", { exact: true }),
      ).toBeVisible();
      // Only this explicit transition replaces the terminal locator/key.
      await panel
        .getByRole("button", { name: "按当前列表创建另一导出", exact: true })
        .click();
      await expect(
        panel.getByLabel("固定导出来源", { exact: true }),
      ).toContainText("禁用位已设置");
    }
    await expect(panel).toContainText("全部匹配用户，不限当前页");
    await expect(
      panel.getByText(fixture.taskUUID, { exact: true }),
    ).toBeVisible();
    for (const column of columns)
      await panel.getByLabel(`导出列：${column}`, { exact: true }).uncheck();
    for (const column of columns)
      await panel.getByLabel(`导出列：${column}`, { exact: true }).check();
    await expect(panel.getByLabel("导出列顺序", { exact: true })).toHaveText(
      columns.join(" → "),
    );
    await proof(page, fixture.bootstrap.password, fixture.bootstrap.auth);
    const [createdResponse] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === exportPrefix &&
          r.request().method() === "POST",
      ),
      panel.getByRole("button", { name: "创建用户导出", exact: true }).click(),
    ]);
    const created = await responseValue(createdResponse, actorId);
    fixture.safe(created);
    expect(created).toMatchObject({
      replayed: false,
      task: { taskName: "user_assets.v3.export" },
    });
    expect(created.request).toEqual(expectedRequest);
    await expect(
      panel.getByRole("button", { name: "下载用户 XLSX", exact: true }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      panel.getByRole("progressbar", {
        name: "用户导出持久化进度",
        exact: true,
      }),
    ).toHaveAttribute("value", "100");
    const [receiptResponse] = await Promise.all([
      page.waitForResponse(
        (r) => new URL(r.url()).pathname === `${exportPrefix}/receipt`,
      ),
      panel
        .getByRole("button", { name: "核对原导出回执", exact: true })
        .click(),
    ]);
    const receipt = await responseValue(receiptResponse, actorId);
    expect(receipt.taskID).toBe(created.taskID);
    expect(receipt.request).toEqual(expectedRequest);
    await expect(
      panel.getByRole("button", { name: "下载用户 XLSX", exact: true }),
    ).toBeEnabled();
    // Download triggers the UI's own fresh session/detail checks and its bounded
    // length/SHA verification before this actual browser Blob download event.
    const [download, downloadResponse, detailResponse] = await Promise.all([
      page.waitForEvent("download"),
      page.waitForResponse(
        (r) => new URL(r.url()).pathname === `${exportPrefix}/download`,
      ),
      page.waitForResponse(
        (r) => new URL(r.url()).pathname === `${exportPrefix}/detail`,
      ),
      panel.getByRole("button", { name: "下载用户 XLSX", exact: true }).click(),
    ]);
    const detail = await responseValue(detailResponse, actorId);
    expect(detail.request).toEqual(expectedRequest);
    expect(downloadResponse.status()).toBe(200);
    expect(detail).toMatchObject({
      taskID: created.taskID,
      downloadReady: true,
      downloadStatus: "ready",
      task: { state: "succeeded", progress: 100 },
    });
    const filename = `user-assets-v3-${created.taskID}.xlsx`;
    expect(download.suggestedFilename()).toBe(filename);
    await download.saveAs(info.outputPath(filename));
    const bytes = await readFile(info.outputPath(filename));
    expect(detail.artifact).toEqual({
      filename,
      sha256: hash(bytes),
      byteCount: bytes.length,
      rowCount: list.page.total,
    });
    await expect(panel.locator('input[type="password"]')).toHaveCount(0);
    fixture.safe(await page.locator("body").innerText());
    const screenshot = `${name}-ready.png`;
    // A fresh no-opener tab must use only the original task ID. Keep the main
    // tab locator: applying the next query remounts its export consumer.
    const originalTab = await context.newPage();
    const originalRequests: { method: string; path: string }[] = [];
    originalTab.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith(exportPrefix))
        originalRequests.push({ method: request.method(), path: url.pathname });
    });
    try {
      await originalTab.setViewportSize({ width: 1440, height: 900 });
      const [me] = await Promise.all([
        originalTab.waitForResponse(
          (r) => new URL(r.url()).pathname === "/api/auth/me",
        ),
        originalTab.goto("/"),
      ]);
      expect((await me.json()).ID).toBe(actorId);
      await originalTab.evaluate(() =>
        sessionStorage.removeItem("adtr.user-assets-v3.export-recovery"),
      );
      expect(
        await originalTab.evaluate(() =>
          sessionStorage.getItem("adtr.user-assets-v3.export-recovery"),
        ),
      ).toBeNull();
      await navigateTo(originalTab, "后台任务");
      await originalTab.locator(".task-filter-panel > summary").click();
      await originalTab
        .getByLabel("任务种类筛选", { exact: true })
        .fill("user_assets.v3.export");
      await originalTab
        .getByRole("button", { name: "筛选任务", exact: true })
        .click();
      await originalTab
        .getByRole("button", { name: `详情 ${created.taskID}`, exact: true })
        .click();
      const [originalResponse] = await Promise.all([
        originalTab.waitForResponse(
          (r) => new URL(r.url()).pathname === `${exportPrefix}/detail`,
        ),
        originalTab
          .getByRole("button", { name: "查看原导出结果", exact: true })
          .click(),
      ]);
      const original = await responseValue(originalResponse, actorId);
      expect(original.taskID).toBe(created.taskID);
      expect(original.request).toEqual(expectedRequest);
      expect(original.artifact).toEqual(detail.artifact);
      const originalPanel = originalTab.getByRole("region", {
        name: "原导出只读详情",
        exact: true,
      });
      await expect(
        originalPanel.getByLabel("原导出规范请求", { exact: true }),
      ).toHaveText(JSON.stringify(created.request, null, 2));
      const [originalDownload] = await Promise.all([
        originalTab.waitForEvent("download"),
        originalPanel
          .getByRole("button", { name: "下载原用户 XLSX", exact: true })
          .click(),
      ]);
      expect(originalDownload.suggestedFilename()).toBe(filename);
      const originalPath = info.outputPath(`task-result-${filename}`);
      await originalDownload.saveAs(originalPath);
      expect(await readFile(originalPath)).toEqual(bytes);
      expect(originalRequests).toEqual([
        { method: "GET", path: `${exportPrefix}/detail` },
        { method: "GET", path: `${exportPrefix}/detail` },
        { method: "GET", path: `${exportPrefix}/download` },
        { method: "GET", path: `${exportPrefix}/detail` },
      ]);
      expect(
        await originalTab.evaluate(() =>
          sessionStorage.getItem("adtr.user-assets-v3.export-recovery"),
        ),
      ).toBeNull();
      await expect(originalPanel.locator('input[type="password"]')).toHaveCount(
        0,
      );
      fixture.safe(await originalTab.locator("body").innerText());
      await originalPanel.scrollIntoViewIfNeeded();
      expect(
        await originalTab.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await originalTab.screenshot({
        path: info.outputPath(screenshot),
        fullPage: false,
      });
    } finally {
      await originalTab.close();
    }
    cases.push({
      name,
      request: expectedRequest,
      listQueryEntries,
      create: created,
      receipt,
      detail,
      download: {
        filename,
        headers: Object.fromEntries(
          downloadHeaderNames.map((key) => [
            key,
            downloadResponse.headers()[key],
          ]),
        ),
      },
      database: await storedExport(created.taskID),
      pageSize: list.page.pageSize,
      firstPageCount: list.list.length,
      screenshot: {
        filename: screenshot,
        sha256: hash(await readFile(info.outputPath(screenshot))),
      },
    });
    await info.attach(filename, {
      path: info.outputPath(filename),
      contentType:
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    });
    await info.attach(screenshot, {
      path: info.outputPath(screenshot),
      contentType: "image/png",
    });
    if (name === "empty")
      await expectSpecializedTaskReadOnly(
        context,
        {
          actorId,
          taskUUID: created.taskID,
          taskName: "user_assets.v3.export",
          state: "succeeded",
        },
        fixture.safe,
      );
  }
  const after = await sensitiveBudget(actorId, fixture.actorId);
  expect(after).toEqual({ ...before, owner: 9, exportTasks: 2 });
  const evidence = {
    format: 2,
    provenance: evidenceProvenance(),
    source,
    actorId,
    sensitiveBudget: { before, after },
    cases,
  };
  fixture.safe(evidence);
  fixture.safe(fixture.consoles);
  const output = info.outputPath("user-asset-export-v3-evidence.json");
  await writeFile(output, JSON.stringify(evidence, null, 2));
  await info.attach("user-asset-export-v3-evidence", {
    path: output,
    contentType: "application/json",
  });
});
