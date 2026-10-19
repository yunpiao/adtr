import { captureAssetEmptyRefresh } from "./asset-empty-refresh";
import { test, expect, type Page, type BrowserContext } from "@playwright/test";
import { writeFile, readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { setupComputerFixture } from "./computer-assets-v3-fixture";
import { navigateTo } from "./navigation";
import {
  observeDirectoryV2Responses,
  directoryV2ResponseJSON,
} from "./directory-v2-response-observer";

test.use({ trace: "off", screenshot: "off", video: "off" });
import {
  openComputerList,
  computerResponse,
  expectedComputers,
  computerEvidenceProvenance,
} from "./computer-assets-v3-helpers";
// Assert positions against the real accepted API order, including fact expansion.
async function expectPagePositions(page: Page, rows: { samAccountName: string | null }[]) {
  const table = page.getByRole("table", { name: "计算机资产列表", exact: true });
  await expect(table.getByRole("columnheader", { name: "本页序号", exact: true }))
    .toHaveAttribute("scope", "col");
  const assets = table.locator("tbody > tr").filter({ has: page.getByRole("button") });
  await expect(assets).toHaveCount(rows.length);
  for (let i = 0; i < rows.length; i++) {
    const cells = assets.nth(i).getByRole("cell");
    await expect(cells).toHaveCount(6);
    await expect(cells.nth(0)).toHaveText(String(i + 1));
    await expect(cells.nth(1)).toHaveText(rows[i].samAccountName ?? "未返回");
  }
}

test("computer assets real Worker TLS LDAP PG API browser pinned facts", async ({
  page,
  context,
}, info) => {
  test.setTimeout(930000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await observeDirectoryV2Responses(context);
  const f = await setupComputerFixture(page, context, { emptyRefresh: true });
  const expected = expectedComputers();
  const params = new URLSearchParams({
    domainId: f.domainId,
    expectedRevision: f.beforeConnection.revision,
    expectedCredentialRevision: f.beforeConnection.credentialRevision,
    observationId: f.taskUUID,
  });
  const api = (more: Record<string, string> = {}) =>
    f.read(
      `/api/computer-assets/v3?${new URLSearchParams({ ...Object.fromEntries(params), ...more })}`,
    );
  const first = await api();
  expect(first).toMatchObject({
    dictionaryVersion: 3,
    available: true,
    observedComputerCount: 23,
    observationId: f.taskUUID,
    page: { pageIdx: 1, pageSize: 10, total: 23, totalPage: 3 },
  });
  expect(first.list).toEqual(expected.slice(0, 10));
  const second = await api({ pageIdx: "2" }),
    third = await api({ pageIdx: "3" });
  expect(second.list).toEqual(expected.slice(10, 20));
  expect(third.list).toEqual(expected.slice(20));
  expect((await api({ search: "computer-22" })).list).toEqual([expected[21]]);
  for (const search of ["mail-only", "HTTP/fixture", "user-24", "group-27"])
    expect(await api({ search })).toMatchObject({
      observedComputerCount: 23,
      list: [],
      page: { total: 0 },
    });
  expect(
    (
      await api({
        startTm: "2026-01-01T00:00:00Z",
        endTm: "2026-01-01T00:00:00.000000001Z",
        pageSize: "50",
      })
    ).list,
  ).toEqual(expected.slice(1, 22));
  expect((await api({ creatTm: "desc", pageSize: "50" })).list).toEqual([
    ...expected.slice(1, 22),
    expected[0],
    expected[22],
  ]);
  const evidence = await f.evidence();
  expect(evidence).toMatchObject({
    dictionaryVersion: 3,
    count: 30,
    taskState: "succeeded",
    purpose: "domain.directory_read.v3",
  });
  const body = Buffer.from(evidence.bodyBase64, "base64");
  expect(createHash("sha256").update(body).digest("hex")).toBe(evidence.sha256);
  const stored = JSON.parse(body.toString("utf8"));
  expect(stored.objects).toHaveLength(30);
  expect(stored.objects[0].identity.primaryGroupIDBytes).toBe(
    Buffer.from("0").toString("base64"),
  );
  expect(stored.objects[0].identity.servicePrincipalNameBytes).toEqual(
    ["HOST/Z", "host/a\u0000\n😀<script>", "HOST/a"].map((x) =>
      Buffer.from(x).toString("base64"),
    ),
  );
  expect(stored.objects[22].identity).toEqual({
    primaryGroupIDBytes: null,
    servicePrincipalNameBytes: null,
  });
  expect(
    stored.objects.filter((r: any) => r.base.kind === "computer"),
  ).toHaveLength(23);
  const ui = await computerResponse(page, () => openComputerList(page));
  expect(ui.value.list).toEqual(expected.slice(0, 10));
  await expectPagePositions(page, ui.value.list);
  await page
    .getByRole("button", { name: "展开事实", exact: true })
    .first()
    .click();
  await expect(
    page.getByText("主组 RID（primaryGroupID）", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("host/a\\u0000\\u000a😀<script>", { exact: true }),
  ).toBeVisible();
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: new URL(page.url()).origin,
  });
  const keyword = page.getByLabel("计算机资产关键词", { exact: true });
  // Real keyboard and clipboard editing, not fabricated input/composition events.
  const limitRequests: string[] = [];
  const observeLimitRequest = (request: { url(): string }) => {
    if (new URL(request.url()).pathname === "/api/computer-assets/v3")
      limitRequests.push(request.url());
  };
  page.on("request", observeLimitRequest);
  await keyword.focus();
  await keyword.pressSequentially("x".repeat(50));
  await expect(keyword).toHaveValue("x".repeat(50));
  await keyword.press("y");
  await expect(keyword).toHaveValue("x".repeat(50));
  const limitMessage = page.getByText("关键词最多 50 个 UTF-16 单元，已拒绝整次变更并保留此前内容；请重新编辑或清除关键词。", { exact: true });
  await expect(limitMessage).toBeVisible();
  await expect(keyword).toBeFocused();
  await expect(page.getByRole("button", { name: "应用计算机筛选", exact: true })).toBeDisabled();
  expect(await keyword.evaluate((node: HTMLInputElement) => [node.selectionStart, node.selectionEnd])).toEqual([50, 50]);
  await keyword.press("Home");
  await keyword.press("Shift+ArrowRight");
  await keyword.press("Shift+ArrowRight");
  await page.evaluate(() => navigator.clipboard.writeText("XYZ"));
  await keyword.press("Control+V");
  await expect(keyword).toHaveValue("x".repeat(50));
  expect(await keyword.evaluate((node: HTMLInputElement) => [node.selectionStart, node.selectionEnd])).toEqual([0, 2]);
  await expectPagePositions(page, ui.value.list);
  await page.screenshot({ path: info.outputPath("computer-assets-v3-keyword-limit-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  const keywordLimitGeometry = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
  expect(keywordLimitGeometry.scroll).toBeLessThanOrEqual(keywordLimitGeometry.width + 1);
  await page.screenshot({ path: info.outputPath("computer-assets-v3-keyword-limit-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.evaluate(() => navigator.clipboard.writeText("😀"));
  await keyword.press("Control+V");
  await expect(keyword).toHaveValue("😀" + "x".repeat(48));
  await expect(limitMessage).toHaveCount(0);
  // Replacing the whole selection is allowed; insertion of one pair at 49 is not.
  await keyword.press("Control+A");
  await page.evaluate(() => navigator.clipboard.writeText("x".repeat(49)));
  await keyword.press("Control+V");
  await expect(keyword).toHaveValue("x".repeat(49));
  await page.keyboard.insertText("😀");
  await expect(keyword).toHaveValue("x".repeat(49));
  await expect(limitMessage).toBeVisible();
  expect(limitRequests).toEqual([]);
  page.off("request", observeLimitRequest);
  await page.getByRole("button", { name: "清除计算机资产关键词", exact: true }).click();
  await expect(keyword).toHaveValue("");
  await expect(limitMessage).toHaveCount(0);
  const beforePaste = await keyword.inputValue();
  await page.evaluate(() => navigator.clipboard.writeText("computer-0\n2"));
  await keyword.focus();
  await keyword.press("Control+V");
  await expect(
    page.getByRole("button", { name: "应用计算机筛选", exact: true }),
  ).toBeDisabled();
  await expect(keyword).toHaveValue(beforePaste);
  await expect(page.getByText(/含控制字符，已拒绝粘贴或拖入/)).toBeVisible();
  await keyword.fill("computer-22");
  await keyword.fill("");
  await page
    .getByRole("button", { name: "展开事实", exact: true })
    .first()
    .click();
  await expectPagePositions(page, ui.value.list);
  await expect(page.locator(`[id="computer-facts-${expected[0].objectGUID}"] > td`)).toHaveAttribute("colspan", "6");
  await page.screenshot({
    path: info.outputPath("computer-assets-v3-desktop-facts.png"),
    fullPage: true,
  });
  const next = await computerResponse(page, () =>
    page.getByRole("button", { name: "下一页", exact: true }).click(),
  );
  expect(next.url.searchParams.get("observationId")).toBe(f.taskUUID);
  expect(next.value.list).toEqual(expected.slice(10, 20));
  await expectPagePositions(page, next.value.list);
  await expect(
    page.getByText("主组 RID（primaryGroupID）", { exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel("计算机资产关键词", { exact: true })
    .fill("computer-22");
  const found = await computerResponse(page, () =>
    page.getByRole("button", { name: "应用计算机筛选", exact: true }).click(),
  );
  expect(found.value.list).toEqual([expected[21]]);
  await expectPagePositions(page, found.value.list);
  expect(found.url.searchParams.get("observationId")).toBe(f.taskUUID);
  // UI-0437: clear only the draft, with native keyboard/focus behavior.
  await page.getByText("创建时间筛选与排序", { exact: true }).click();
  const startTime = "2026-01-01T00:00:00Z";
  const endTime = "2026-01-02T00:00:00Z";
  await page.getByLabel("创建时间开始（含）", { exact: true }).fill(startTime);
  await page.getByLabel("创建时间结束（不含）", { exact: true }).fill(endTime);
  await page.getByLabel("创建时间排序", { exact: true }).selectOption("desc");
  const beforeClear = await computerResponse(page, () =>
    page.getByRole("button", { name: "应用计算机筛选", exact: true }).click(),
  );
  expect(beforeClear.value.list).toEqual([expected[21]]);
  await page.getByRole("button", { name: "展开事实", exact: true }).click();
  const clearRequests: string[] = [];
  const observeClearRequest = (request: { url(): string }) => {
    if (new URL(request.url()).pathname === "/api/computer-assets/v3")
      clearRequests.push(request.url());
  };
  page.on("request", observeClearRequest);
  await keyword.focus();
  await keyword.press("Tab");
  const clearKeyword = page.getByRole("button", { name: "清除计算机资产关键词", exact: true });
  await expect(clearKeyword).toBeFocused();
  await clearKeyword.press("Enter");
  await expect(keyword).toHaveValue("");
  await expect(keyword).toBeFocused();
  await expect(clearKeyword).toBeDisabled();
  await expect(page.getByLabel("创建时间开始（含）", { exact: true })).toHaveValue(startTime);
  await expect(page.getByLabel("创建时间结束（不含）", { exact: true })).toHaveValue(endTime);
  await expect(page.getByLabel("创建时间排序", { exact: true })).toHaveValue("desc");
  await expect(page.getByText("主组 RID（primaryGroupID）", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "展开事实", exact: true })).toBeDisabled();
  await expectPagePositions(page, beforeClear.value.list);
  await page.screenshot({
    path: info.outputPath("computer-assets-v3-clear-keyword-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(keyword).toBeFocused();
  const clearGeometry = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }));
  expect(clearGeometry.scroll).toBeLessThanOrEqual(clearGeometry.width + 1);
  await page.screenshot({
    path: info.outputPath("computer-assets-v3-clear-keyword-mobile.png"),
    fullPage: true,
  });
  expect(clearRequests).toEqual([]);
  const cleared = await computerResponse(page, () =>
    page.getByRole("button", { name: "应用计算机筛选", exact: true }).click(),
  );
  expect(clearRequests).toHaveLength(1);
  page.off("request", observeClearRequest);
  expect(Object.fromEntries(cleared.url.searchParams)).toEqual({
    ...Object.fromEntries(beforeClear.url.searchParams), search: "", pageIdx: "1",
  });
  expect(cleared.value.list).toEqual(expected.slice(1, 11));
  await expectPagePositions(page, cleared.value.list);
  // Restore the pinned search for the existing mobile facts/scroll regression.
  await keyword.fill("computer-22");
  const restoredSearch = await computerResponse(page, () =>
    page.getByRole("button", { name: "应用计算机筛选", exact: true }).click(),
  );
  expect(restoredSearch.value.list).toEqual(found.value.list);
  await page.getByText("创建时间筛选与排序", { exact: true }).click();
  await expectPagePositions(page, found.value.list);
  await page.screenshot({
    path: info.outputPath("computer-assets-v3-mobile.png"),
    fullPage: true,
  });
  const geometry = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }));
  expect(geometry.scroll).toBeLessThanOrEqual(geometry.width + 1);
  const mobileScroller = page.locator(".table-scroll").filter({
    has: page.getByRole("table", { name: "计算机资产列表", exact: true }),
  });
  await mobileScroller.hover();
  await page.mouse.wheel(1600, 0);
  await expect
    .poll(() => mobileScroller.evaluate((node) => node.scrollLeft))
    .toBeGreaterThan(0);
  const mobileExpand = page.getByRole("button", {
    name: "展开事实",
    exact: true,
  });
  const expandBox = await mobileExpand.boundingBox();
  expect(expandBox).not.toBeNull();
  expect(expandBox!.x).toBeGreaterThanOrEqual(0);
  expect(expandBox!.x + expandBox!.width).toBeLessThanOrEqual(390);
  await mobileExpand.click();
  await mobileScroller.hover();
  await page.mouse.wheel(-1600, 0);
  await expect
    .poll(() => mobileScroller.evaluate((node) => node.scrollLeft))
    .toBe(0);
  const mobileFacts = page.locator(
    `[id="computer-facts-${expected[21].objectGUID}"]`,
  );
  await expect(
    mobileFacts.getByText(expected[21].objectGUID, { exact: true }),
  ).toBeVisible();
  await expect(mobileFacts.getByText("515", { exact: true })).toBeVisible();
  await expect(
    mobileFacts.getByText("HOST/computer-22.synthetic.invalid", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    mobileFacts.getByText("HTTP/fixture", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(`观测来源 · ${f.taskUUID}`, { exact: true }),
  ).toBeVisible();
  const expandedMobileGeometry = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }));
  expect(expandedMobileGeometry.scroll).toBeLessThanOrEqual(
    expandedMobileGeometry.width + 1,
  );
  await expectPagePositions(page, found.value.list);
  await expect(mobileFacts.locator(":scope > td")).toHaveAttribute("colspan", "6");
  await page.screenshot({
    path: info.outputPath("computer-assets-v3-mobile-facts.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 960 });
  await computerResponse(page, () =>
    page.getByRole("button", { name: "清除计算机筛选", exact: true }).click(),
  );
  await expectPagePositions(page, expected.slice(0, 10));
  const resized = await computerResponse(page, () =>
    page.getByLabel("计算机每页条数", { exact: true }).selectOption("20"),
  );
  expect(resized.value.list).toEqual(expected.slice(0, 20));
  await expectPagePositions(page, resized.value.list);
  await computerResponse(page, () =>
    page.getByLabel("计算机每页条数", { exact: true }).selectOption("10"),
  );
  await expectPagePositions(page, expected.slice(0, 10));
  await keyword.fill("no-computer-match");
  const empty = await computerResponse(page, () =>
    page.getByRole("button", { name: "应用计算机筛选", exact: true }).click(),
  );
  expect(empty.value.list).toEqual([]);
  await expect(page.getByRole("table", { name: "计算机资产列表", exact: true })).toHaveCount(0);
  await computerResponse(page, () =>
    page.getByRole("button", { name: "清除计算机筛选", exact: true }).click(),
  );
  await expectPagePositions(page, expected.slice(0, 10));
  const newer = await f.post("/api/directory/v3/sync", {
    ...f.sync,
    idempotencyKey: "computer-second-real-wire",
  });
  await expect
    .poll(
      async () =>
        (await f.read(`/api/directory/v3/task?taskUUID=${newer.task.taskUUID}`))
          .task.state,
      { timeout: 30000 },
    )
    .toBe("succeeded");
  const pinned = await computerResponse(page, () =>
    page.getByRole("button", { name: "下一页", exact: true }).click(),
  );
  expect(pinned.value.observationId).toBe(f.taskUUID);
  await expectPagePositions(page, pinned.value.list);
  const refreshed = await computerResponse(page, () =>
    page
      .getByRole("button", { name: "刷新计算机资产观测", exact: true })
      .click(),
  );
  expect(refreshed.url.searchParams.has("observationId")).toBe(false);
  expect(refreshed.value.observationId).toBe(newer.task.taskUUID);
  await expectPagePositions(page, refreshed.value.list);
  await captureAssetEmptyRefresh(
    page,
    info,
    "computer",
    f,
    newer.task.taskUUID,
    expected,
  );
  await navigateTo(page, "账户概览");
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "计算机资产", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("table", { name: "计算机资产列表", exact: true }),
  ).toHaveCount(0);
  f.safe({ first, second, third, ui: ui.value, newer });
  // Check actual fixture credentials/MFA secrets/CSRF token in every message,
  // including the browser advisory below; no console entry bypasses this guard.
  f.safe(f.consoles);
  const browserPasswordAdvisory =
    "[DOM] Password forms should have (optionally hidden) username fields for accessibility: (More info: https://goo.gl/9p2vKq) %o";
  expect(
    f.consoles.filter(
      (x) =>
        x !== browserPasswordAdvisory &&
        /password|totp|ciphertext|userinfo/i.test(x),
    ),
  ).toEqual([]);
  await writeFile(
    info.outputPath("computer-assets-v3-evidence.json"),
    JSON.stringify(
      {
        provenance: computerEvidenceProvenance(),
        screenshotBinding: {
          observationId: f.taskUUID,
          desktop: {
            viewport: { width: 1440, height: 900 },
            state: "page1 expanded facts",
          },
          mobileFacts: {
            viewport: { width: 390, height: 844 },
            state:
              "native horizontal wheel reached expand; pinned computer-22 facts",
          },
          mobile: {
            viewport: { width: 390, height: 844 },
            state: "pinned computer-22 search",
          },
        },
        pageLocalRowNumbers: {
          label: "本页序号",
          semantics: "accepted response order 1..N, restarted on every page",
          checked: ["page1", "page2", "pageSize20", "filter", "empty", "refresh", "expanded facts"],
          expandedFactColSpan: 6,
        },
        keywordClear: {
          semantics: "draft-only; time/order/source/pin preserved until explicit apply",
          keyboard: "Tab then Enter; focus returns to keyword input",
          noAutomaticListRequest: true,
          clearGeometry,
          screenshotBinding: {
            observationId: f.taskUUID,
            state: "empty keyword draft; applied computer-22 with time/order retained; facts closed",
            desktop: { width: 1440, height: 900 },
            mobile: { width: 390, height: 844 },
          },
          screenshotSHA256: {
            desktop: createHash("sha256").update(await readFile(info.outputPath("computer-assets-v3-clear-keyword-desktop.png"))).digest("hex"),
            mobile: createHash("sha256").update(await readFile(info.outputPath("computer-assets-v3-clear-keyword-mobile.png"))).digest("hex"),
          },
        },
        keywordLimit: {
          bound: "50 UTF-16 code units; whole-change rejection, no truncation",
          nativeChecks: ["50 accepted", "51 rejected", "selected paste rejected", "pair replacement accepted", "49 plus pair rejected", "no automatic request"],
          keywordLimitGeometry,
          screenshotBinding: {
            observationId: f.taskUUID,
            state: "rejected 51-unit selected paste; previous 50-unit draft and selection retained; applied page1 unchanged",
            desktop: { width: 1440, height: 900 },
            mobile: { width: 390, height: 844 },
          },
          screenshotSHA256: {
            desktop: createHash("sha256").update(await readFile(info.outputPath("computer-assets-v3-keyword-limit-desktop.png"))).digest("hex"),
            mobile: createHash("sha256").update(await readFile(info.outputPath("computer-assets-v3-keyword-limit-mobile.png"))).digest("hex"),
          },
          limitations: ["OS IME and native drag/drop not exercised; synthetic DOM event contracts only"],
        },
        dictionary: 3,
        computerCount: 23,
        observationId: f.taskUUID,
        newObservationId: newer.task.taskUUID,
        storedDigest: evidence.sha256,
        screenshotSHA256: {
          desktop: createHash("sha256")
            .update(
              await readFile(
                info.outputPath("computer-assets-v3-desktop-facts.png"),
              ),
            )
            .digest("hex"),
          mobileFacts: createHash("sha256")
            .update(
              await readFile(
                info.outputPath("computer-assets-v3-mobile-facts.png"),
              ),
            )
            .digest("hex"),
          mobile: createHash("sha256")
            .update(
              await readFile(info.outputPath("computer-assets-v3-mobile.png")),
            )
            .digest("hex"),
        },
        source: first.source,
        geometry,
        expandedMobileGeometry,
        apiFacts: first.list,
        transport:
          "registered Worker → owned TLS LDAP → PostgreSQL → API → browser",
        limitations: ["synthetic only", "no real AD/Windows/capacity"],
      },
      null,
      2,
    ),
  );
});
