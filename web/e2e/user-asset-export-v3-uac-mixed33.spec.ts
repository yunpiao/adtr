import { test, expect, type Page, type Response } from "@playwright/test";
import { lstat, open, readFile, writeFile, unlink } from "node:fs/promises";
import { constants } from "node:fs";
import path from "node:path";
import { navigateTo } from "./navigation";
import {
  setupUACGroupFixture,
  proof,
  readJSON,
} from "./group-assets-v3-fixture";
import { observeDirectoryV2Responses } from "./directory-v2-response-observer";
import {
  downloadHeaderNames,
  hash,
  switchToExportOwner,
  openAssets,
  sensitiveBudget,
} from "./user-asset-export-v3-helpers";
import {
  captureUACReadOnlyScreenshot,
  expectedUACRequest,
  observeUACExportWrites,
  readUACOriginalReceipt,
  safeResponse,
  safeValue,
  sameValue,
  storedUACExport,
  storedUACSource,
  uacRequire,
  uacFailureMessage,
  type UACFailureStage,
  exportPrefix,
  type UACMode,
  type ExportDetail,
  type Budget,
} from "./user-asset-export-v3-uac-helpers";

// Never inherit retain-on-failure artifacts: the main panel holds a private key.
test.use({ trace: "off", screenshot: "off", video: "off" });
const oracleSHA256 =
  "833245587fd68cca1f32f3b140703b8b18a13332a94f04e7f8c83db30a1d210b";
const recoveryStorage = "adtr.user-assets-v3.export-recovery";
async function boundedFile(filename: string, cap: number): Promise<Buffer> {
  let handle;
  try {
    const absolute = path.resolve(filename);
    for (let parent = absolute; ; parent = path.dirname(parent)) {
      uacRequire(!(await lstat(parent)).isSymbolicLink(), "artifact_binding");
      if (path.dirname(parent) === parent) break;
    }
    const metadata = await lstat(absolute);
    uacRequire(
      metadata.isFile() && metadata.size > 0 && metadata.size <= cap,
      "artifact_binding",
    );
    handle = await open(
      absolute,
      constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
    );
    const opened = await handle.stat();
    uacRequire(
      opened.isFile() &&
        metadata.ino === opened.ino &&
        metadata.dev === opened.dev,
      "artifact_binding",
    );
    const raw = Buffer.alloc(cap + 1);
    let count = 0;
    while (count <= cap) {
      const { bytesRead } = await handle.read(
        raw,
        count,
        Math.min(65536, cap + 1 - count),
        null,
      );
      if (!bytesRead) break;
      count += bytesRead;
    }
    uacRequire(count === opened.size && count <= cap, "artifact_binding");
    return raw.subarray(0, count);
  } catch {
    throw new Error("uac_evidence_invalid:artifact_binding");
  } finally {
    await handle?.close();
  }
}

test("mixed33 real UAC lists and full exports bind wire, source, original receipts and safe screenshots", async ({
  page,
  context,
}, info) => {
  test.setTimeout(900_000);
  let stage: UACFailureStage = "configuration";
  try {
    const origin = new URL(process.env.ADTR_E2E_BASE_URL ?? "").origin;
    const nonce = process.env.ADTR_LDAP_FIXTURE_UAC_RUN_NONCE ?? "";
    uacRequire(/^[0-9a-f]{32}$/u.test(nonce), "configuration");
    const oracleRaw = await boundedFile(
      path.resolve("../scripts/fixtures/user_uac_v3_mixed33_oracle.json"),
      1 << 20,
    );
    uacRequire(hash(oracleRaw) === oracleSHA256, "source_binding");
    const oracle = JSON.parse(oracleRaw.toString("utf8"));
    const provenance = JSON.parse(
      (
        await boundedFile(
          path.resolve(
            "../.e2e-diagnostics/user-asset-export-v3-uac-provenance.json",
          ),
          2 << 20,
        )
      ).toString("utf8"),
    );
    await page.setViewportSize({ width: 1440, height: 900 });
    await observeDirectoryV2Responses(context);
    stage = "fixture";
    const fixture = await setupUACGroupFixture(page, context, "mixed-33-v1");
    stage = "source_read";
    const source = await storedUACSource(fixture.taskUUID);
    safeValue(source, fixture.secrets);
    uacRequire(
      source.taskUUID === fixture.taskUUID &&
        source.actorId === String(fixture.actorId) &&
        source.count === 51 &&
        source.dictionaryVersion === 3 &&
        source.sha256 === hash(Buffer.from(source.bodyBase64, "base64")),
      "source_binding",
    );
    stage = "wire_read";
    const wire = await boundedFile(
      path.join(
        process.env.ADTR_E2E_LDAP_UAC_EVIDENCE_DIR ?? "",
        "candidate-wire.json",
      ),
      160 << 10,
    );
    const transcript = JSON.parse(wire.toString("utf8"));
    uacRequire(
      transcript.nonce === nonce &&
        transcript.profile === "mixed-33-v1" &&
        transcript.transport === "starttls",
      "raw_transcript",
    );
    const observer = observeUACExportWrites(context, origin, fixture.secrets);
    stage = "owner_switch";
    await switchToExportOwner(page, fixture.bootstrap);
    const actorId = (await readJSON(context, "/api/auth/me")).ID as number;
    uacRequire(
      Number.isSafeInteger(actorId) &&
        actorId > 0 &&
        actorId !== fixture.actorId,
      "source_binding",
    );
    stage = "budget_before";
    const before: Budget = await sensitiveBudget(actorId, fixture.actorId);
    uacRequire(
      sameValue(before, {
        owner: 7,
        producer: 9,
        observations: 1,
        directoryUses: 1,
        exportTasks: 0,
      }),
      "post_count",
    );
    stage = "initial_list";
    const initial = await openAssets(page, actorId);
    safeValue(initial, fixture.secrets);
    uacRequire(
      initial.dictionaryVersion === 3 &&
        initial.available === true &&
        initial.observationId === source.taskUUID &&
        initial.page.total === 33,
      "source_binding",
    );
    stage = "list_filters";
    await page.getByText("精确账户与创建时间筛选", { exact: true }).click();
    await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
    await page.getByLabel("用户资产关键词", { exact: true }).fill("user-");
    const listPages: {
      mode: string;
      pageIdx: number;
      queryEntries: string[][];
      response: Record<string, unknown>;
    }[] = [];
    const captureList = async (
      mode: "omitted" | UACMode,
      pageIdx: number,
      action: () => Promise<unknown>,
    ) => {
      stage = "list_read";
      const [response] = await Promise.all([
        page.waitForResponse(
          (r) =>
            new URL(r.url()).origin === origin &&
            new URL(r.url()).pathname === "/api/user-assets/v3" &&
            r.request().method() === "GET",
        ),
        action(),
      ]);
      const value = await safeResponse(response, actorId, fixture.secrets);
      const queryEntries = [...new URL(response.url()).searchParams.entries()];
      const expectedQuery = {
        domainId: source.selection.domainId,
        expectedRevision: source.selection.revision,
        expectedCredentialRevision: source.selection.credentialRevision,
        observationId: source.taskUUID,
        search: "user-",
        creatTm: "desc",
        pageIdx: String(pageIdx),
        pageSize: "10",
        ...(mode === "omitted" ? {} : { uacDisableBit: mode }),
      };
      stage = "list_query";
      uacRequire(
        sameValue(
          [...queryEntries].sort(),
          Object.entries(expectedQuery).sort(),
        ),
        "source_binding",
      );
      stage = "list_rows";
      uacRequire(
        sameValue(
          value.list,
          oracle.queries[mode].pages[pageIdx - 1].public12Rows,
        ),
        "source_binding",
      );
      stage = "list_source";
      uacRequire(
        sameValue(value.selection, source.selection) &&
          value.observationId === source.taskUUID,
        "source_binding",
      );
      stage = "list_page";
      uacRequire(
        sameValue(value.page, {
          pageIdx,
          pageSize: 10,
          total: mode === "omitted" ? 33 : 11,
          totalPage: mode === "omitted" ? 4 : 2,
        }),
        "source_binding",
      );
      listPages.push({ mode, pageIdx, queryEntries, response: value });
      return { value, queryEntries };
    };
    await captureList("omitted", 1, () =>
      page.getByRole("button", { name: "搜索用户", exact: true }).click(),
    );
    for (const index of [2, 3, 4])
      await captureList("omitted", index, () =>
        page
          .getByRole("button", { name: "用户资产下一页", exact: true })
          .click(),
      );
    const cases = [],
      afterEach: Budget[] = [];
    for (const mode of ["set", "clear", "not_returned"] as const) {
      stage = "list_filters";
      await page
        .getByLabel("本次观测的禁用位", { exact: true })
        .selectOption(mode);
      await captureList(mode, 1, () =>
        page.getByRole("button", { name: "搜索用户", exact: true }).click(),
      );
      const second = await captureList(mode, 2, () =>
        page
          .getByRole("button", { name: "用户资产下一页", exact: true })
          .click(),
      );
      stage = "export_prepare";
      const expected = expectedUACRequest(source, mode);
      observer.armCase(mode, expected, actorId, page);
      const panel = page.getByRole("region", {
        name: "用户资产导出",
        exact: true,
      });
      await page
        .getByRole("button", { name: "导出匹配用户 XLSX", exact: true })
        .click();
      if (mode !== "set") {
        // The prior case is fully settled, original-key GET/readback/screenshot
        // completed. Only now may this explicit UI action replace its locator.
        await panel
          .getByRole("button", { name: "按当前列表创建另一导出", exact: true })
          .click();
      }
      // Non-submitting dismissal/escape paths are inside the all-context ledger.
      await panel
        .getByRole("button", { name: "关闭导出面板", exact: true })
        .click();
      await page.keyboard.press("Escape");
      await page
        .getByRole("button", { name: "导出匹配用户 XLSX", exact: true })
        .click();
      await expect(
        panel.getByRole("button", { name: "创建用户导出", exact: true }),
      ).toBeEnabled();
      for (const column of expected.screenColumns)
        await panel.getByLabel(`导出列：${column}`, { exact: true }).uncheck();
      for (const column of expected.screenColumns)
        await panel.getByLabel(`导出列：${column}`, { exact: true }).check();
      uacRequire(
        (await panel.getByLabel("导出列顺序", { exact: true }).innerText()) ===
          expected.screenColumns.join(" → "),
        "post_binding",
      );
      stage = "export_submit";
      await proof(
        page,
        fixture.bootstrap.password,
        fixture.bootstrap.auth,
        fixture.secrets,
      );
      const [createdResponse] = await Promise.all([
        page.waitForResponse(
          (r) =>
            new URL(r.url()).pathname === exportPrefix &&
            r.request().method() === "POST",
        ),
        panel
          .getByRole("button", { name: "创建用户导出", exact: true })
          .dblclick(),
      ]);
      const created = await safeResponse(
        createdResponse,
        actorId,
        fixture.secrets,
      );
      uacRequire(
        typeof created.taskID === "string" &&
          created.replayed === false &&
          sameValue(created.request, expected),
        "post_binding",
      );
      stage = "export_settle";
      await observer.settleCase(created.taskID);
      stage = "export_ready";
      await expect(
        panel.getByRole("button", { name: "下载用户 XLSX", exact: true }),
      ).toBeVisible({ timeout: 30_000 });
      stage = "export_database";
      const database = await storedUACExport(created.taskID);
      safeValue(database, fixture.secrets);
      uacRequire(
        typeof database.idempotencyKeySHA256 === "string" &&
          /^[0-9a-f]{64}$/u.test(database.idempotencyKeySHA256),
        "task_binding",
      );
      stage = "original_receipt";
      const receipt = await readUACOriginalReceipt(
        page,
        origin,
        actorId,
        expected,
        database.idempotencyKeySHA256,
        fixture.secrets,
      );
      uacRequire(receipt.taskID === created.taskID, "task_binding");
      stage = "export_download";
      const [download, downloadResponse, detailResponse] = await Promise.all([
        page.waitForEvent("download"),
        page.waitForResponse(
          (r) => new URL(r.url()).pathname === `${exportPrefix}/download`,
        ),
        page.waitForResponse(
          (r) => new URL(r.url()).pathname === `${exportPrefix}/detail`,
        ),
        panel
          .getByRole("button", { name: "下载用户 XLSX", exact: true })
          .click(),
      ]);
      const detail = (await safeResponse(
        detailResponse,
        actorId,
        fixture.secrets,
      )) as unknown as ExportDetail;
      const filename = `user-assets-v3-${created.taskID}.xlsx`;
      uacRequire(
        download.suggestedFilename() === filename &&
          downloadResponse.status() === 200 &&
          detail.taskID === created.taskID &&
          detail.downloadReady &&
          detail.downloadStatus === "ready" &&
          sameValue(detail.request, expected),
        "artifact_binding",
      );
      await download.saveAs(info.outputPath(filename));
      const bytes = await boundedFile(info.outputPath(filename), 32 << 20);
      uacRequire(
        sameValue(detail.artifact, {
          filename,
          rowCount: 11,
          byteCount: bytes.length,
          sha256: hash(bytes),
        }),
        "artifact_binding",
      );
      const safeHeaders = (response: Response) =>
        Object.fromEntries(
          downloadHeaderNames.map((name) => [name, response.headers()[name]]),
        );
      stage = "original_task";
      const originalTab = await context.newPage();
      let originalTaskReadback;
      try {
        uacRequire((await originalTab.opener()) === null, "artifact_binding");
        await originalTab.setViewportSize({ width: 1440, height: 900 });
        const [identity] = await Promise.all([
          originalTab.waitForResponse(
            (r) => new URL(r.url()).pathname === "/api/auth/me",
          ),
          originalTab.goto("/"),
        ]);
        uacRequire(
          (await identity.json()).ID === actorId &&
            new URL(originalTab.url()).origin === origin,
          "artifact_binding",
        );
        uacRequire(
          (await originalTab.evaluate(
            (name) => sessionStorage.getItem(name),
            recoveryStorage,
          )) === null,
          "artifact_binding",
        );
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
        const original = (await safeResponse(
          originalResponse,
          actorId,
          fixture.secrets,
        )) as unknown as ExportDetail;
        uacRequire(
          original.taskID === created.taskID &&
            sameValue(original.request, expected) &&
            sameValue(original.artifact, detail.artifact),
          "artifact_binding",
        );
        const originalPanel = originalTab.getByRole("region", {
          name: "原导出只读详情",
          exact: true,
        });
        stage = "original_download";
        const [originalDownload, originalDownloadResponse] = await Promise.all([
          originalTab.waitForEvent("download"),
          originalTab.waitForResponse(
            (r) => new URL(r.url()).pathname === `${exportPrefix}/download`,
          ),
          originalPanel
            .getByRole("button", { name: "下载原用户 XLSX", exact: true })
            .click(),
        ]);
        uacRequire(
          originalDownload.suggestedFilename() === filename &&
            originalDownloadResponse.status() === 200,
          "artifact_binding",
        );
        const temporaryDownload = info.outputPath(`task-result-${filename}`);
        try {
          await originalDownload.saveAs(temporaryDownload);
          const originalBytes = await boundedFile(temporaryDownload, 32 << 20);
          uacRequire(bytes.equals(originalBytes), "artifact_binding");
          originalTaskReadback = {
            detail: original,
            download: {
              filename,
              headers: safeHeaders(originalDownloadResponse),
            },
            downloadSHA256: hash(originalBytes),
            downloadByteCount: originalBytes.length,
          };
        } finally {
          await unlink(temporaryDownload).catch(() => undefined);
        }
        stage = "screenshot";
        await captureUACReadOnlyScreenshot(
          originalTab,
          page,
          origin,
          actorId,
          created.taskID,
          expected,
          database.idempotencyKeySHA256,
          fixture.secrets,
          info,
          mode,
        );
      } finally {
        await originalTab.close();
      }
      stage = "case_evidence";
      const screenshot = `${mode}-ready.png`;
      cases.push({
        name: mode,
        request: expected,
        create: created,
        receipt,
        detail,
        download: { filename, headers: safeHeaders(downloadResponse) },
        database,
        pageSize: 10,
        firstPageCount: 10,
        sourcePageIdx: 2,
        screenshot: {
          filename: screenshot,
          sha256: hash(
            await boundedFile(info.outputPath(screenshot), 10 << 20),
          ),
        },
        listQueryEntries: second.queryEntries,
        originalTaskReadback,
      });
      stage = "case_budget";
      const budget = await sensitiveBudget(actorId, fixture.actorId);
      uacRequire(
        sameValue(budget, {
          ...before,
          owner: 8 + afterEach.length,
          exportTasks: 1 + afterEach.length,
        }),
        "post_count",
      );
      afterEach.push(budget);
      stage = "case_dismiss";
      if (mode === "not_returned") {
        await panel
          .getByRole("button", { name: "放弃原回执恢复", exact: true })
          .click();
        await panel
          .getByRole("button", { name: "确认放弃恢复记录", exact: true })
          .click();
      }
      await panel
        .getByRole("button", { name: "关闭导出面板", exact: true })
        .click();
    }
    stage = "budget_final";
    const after: Budget = await sensitiveBudget(actorId, fixture.actorId);
    uacRequire(
      sameValue(after, { ...before, owner: 10, exportTasks: 3 }),
      "post_count",
    );
    stage = "observer_close";
    for (const tab of context.pages()) await tab.close();
    const postLedger = await observer.closeAndSnapshot(after);
    stage = "candidate_write";
    const candidate = {
      format: 1,
      profile: "mixed-33-v1",
      provenance,
      source,
      actorId,
      sensitiveBudget: { before, afterEach, after },
      cases,
      listPages,
      postLedger,
      oracleSHA256,
      nonce,
      candidateTranscriptSHA256: hash(wire),
    };
    safeValue(candidate, fixture.secrets);
    safeValue(fixture.consoles, fixture.secrets);
    const serialized = JSON.stringify(candidate);
    uacRequire(
      Buffer.byteLength(serialized, "utf8") <= 8 << 20,
      "candidate_schema",
    );
    await writeFile(
      info.outputPath("user-asset-export-v3-uac-candidate.json"),
      serialized,
      { flag: "wx", mode: 0o600 },
    );
  } catch (error) {
    throw new Error(uacFailureMessage(error, stage));
  } finally {
    // Keep the all-context observer attached through every page close on failure.
    for (const tab of context.pages()) await tab.close().catch(() => undefined);
  }
});
