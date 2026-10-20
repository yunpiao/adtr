import { test, expect, type Page, type APIResponse, type Route } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { setupV4, requireFixture, fillSecret, login, changePassword, enrollMfa, freshCode, readJSON } from "./directory-v4-fixture";
import { V4, invariant, bounded, guardLeaks, evidenceDigest, originalEvidence, mutationEvidence, observeMutations, openCollection, prepareSync, enterProof, clickMutation, originalFrom, terminal, refreshOriginal, pinnedResults, safeScreenshots, proofEmpty, form, writeEvidence, actualJSON } from "./directory-v4-runtime";
import { readDirectoryV4ReaderCounts } from "./directory-v4-ledger-observer";
import { navigateTo } from "./navigation";
import { useNativeTabLifecycle, requireSameNativeWindow } from "./native-tab-lifecycle";
import { parseGroupMembersV4Page } from "../src/group-members-v4-read";

process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture("readers"));
async function draft(page: Page) {
  await expect(form(page, "sync").locator('input[type="password"], input[inputmode="numeric"]')).toHaveCount(2);
  await fillSecret(form(page, "sync").getByLabel("当前用户密码", { exact: true }), process.env.ADTR_E2E_V4_CONTROLS_DRAFT_PASSWORD!);
  await fillSecret(form(page, "sync").getByLabel("当前六位验证码", { exact: true }), process.env.ADTR_E2E_V4_CONTROLS_DRAFT_CODE!);
  invariant(await page.locator('.directory-v4-workspace input[type="password"], .directory-v4-workspace input[inputmode="numeric"]').evaluateAll(nodes => nodes.length === 2 && nodes.every(node => (node as HTMLInputElement).value.length > 0)), "Proof-clearing probe did not reach actual mounted inputs");
}
async function privateCleared(page: Page) {
  for (const name of ["原操作恢复", "原任务状态", "本任务的原始组成员结果", "组原始成员详情"]) await expect(page.getByRole("region", { name, exact: true })).toHaveCount(0);
  await proofEmpty(page);
}
test("actual reader authority, native lifecycle and stale original reads never restore private state", async ({ page, context }, info) => {
  test.setTimeout(630_000);
  // Deliberately unavailable notification transports exercise the real native
  // focus fallback, not manufactured focus events or fake authentication.
  await page.addInitScript(() => {
    Object.defineProperty(window, "BroadcastChannel", { configurable: true, value: class { constructor() { throw new DOMException("Unavailable", "SecurityError"); } } });
    Object.defineProperty(window, "localStorage", { configurable: true, get() { throw new DOMException("Unavailable", "SecurityError"); } });
  });
  await useNativeTabLifecycle(page);
  const fixture = await setupV4(page, context, { reader: true }), actor = fixture.producer;
  invariant(fixture.reader, "Actual reader setup absent");
  const reader = fixture.reader, mutations = observeMutations(context, actor);
  const leaks = guardLeaks(context, fixture.secrets);
  const switching = await context.newPage(); await useNativeTabLifecycle(switching); await switching.goto("/");
  await requireSameNativeWindow([page, switching]);
  let release = () => {}, delayedFinished: Promise<void> | undefined;
  let readerMonitoring = false, readerUiBusinessPosts = 0;
  const writeListener = (request: import("@playwright/test").Request) => {
    const path = new URL(request.url()).pathname;
    if (readerMonitoring && request.method() !== "GET" && request.method() !== "HEAD" && path !== "/api/access/check" && !path.startsWith("/api/auth/")) readerUiBusinessPosts++;
  };
  context.on("request", writeListener);
  try {
    await page.bringToFront(); await openCollection(page); await prepareSync(page); await draft(page);
    const lifecycle = await page.evaluateHandle(() => {
      const events: { type: string; trusted: boolean; visible: boolean }[] = [];
      const record = (event: Event) => { if (event.type !== "focus" || event.target === window) events.push({ type: event.type, trusted: event.isTrusted, visible: document.visibilityState === "visible" }); };
      window.addEventListener("focus", record); document.addEventListener("visibilitychange", record);
      return { read: () => ({ visibility: document.visibilityState, focused: document.hasFocus(), events }), stop: () => { window.removeEventListener("focus", record); document.removeEventListener("visibilitychange", record); } };
    });
    await switching.bringToFront();
    await expect.poll(() => lifecycle.evaluate(v => v.read())).toMatchObject({ visibility: "hidden", focused: false });
    await proofEmpty(page); await privateCleared(page);
    await page.bringToFront();
    await expect.poll(() => lifecycle.evaluate(v => v.read())).toMatchObject({ visibility: "visible", focused: true, events: expect.arrayContaining([expect.objectContaining({ type: "focus", trusted: true }), { type: "visibilitychange", trusted: true, visible: true }]) });
    await lifecycle.evaluate(v => v.stop()); await lifecycle.dispose();
    await expect(form(page, "sync")).toHaveCount(0); await proofEmpty(page);
    await prepareSync(page); await draft(page);
    // Real browser history navigation, not synthetic popstate/hashchange events.
    await navigateTo(page, "账户概览"); await page.goBack();
    await expect(page.getByRole("heading", { name: "字典 4 目录采集", exact: true })).toBeVisible(); await proofEmpty(page);
    await page.goForward(); await page.goBack(); await proofEmpty(page);
    await prepareSync(page); await enterProof(page, actor, "sync");
    const task = await clickMutation(page, actor, fixture.domainId, "sync"), original = originalFrom(task, mutations.entries.at(-1)!);
    await terminal(context, actor, original, "succeeded"); await refreshOriginal(page); await pinnedResults(page, actor, original);
    await safeScreenshots(page, info, "producer-proof-cleared", fixture.secrets);
    await page.setViewportSize({ width: 1440, height: 900 });

    // Hold an actual old-actor success. Returning its exact server response after
    // native deactivation must not resurrect rows. A cancelled browser transport
    // is recorded as such; it is not claimed as an ignored-AbortSignal test.
    let fetched!: () => void, failed!: (error: Error) => void, finish!: () => void;
    const ready = new Promise<void>((yes, no) => { fetched = yes; failed = no; });
    delayedFinished = new Promise<void>(yes => { finish = yes; });
    const gate = new Promise<void>(yes => { release = yes; });
    let genuineOldResponse = false;
    const delivery: { value: "not_attempted" | "delivered" | "aborted" } = { value: "not_attempted" };
    const handler = async (route: Route) => {
      let response: APIResponse | undefined;
      try {
        response = await route.fetch({ maxRedirects: 0, maxRetries: 0, timeout: 15_000 });
        const value = await actualJSON(response, actor.actorId);
        invariant(value.observationId === original.taskUUID, "Delayed read lost original observation"); genuineOldResponse = true; fetched();
        await gate;
        try { await route.fulfill({ response }); delivery.value = "delivered"; }
        catch { delivery.value = "aborted"; }
      } catch { failed(new Error("Real delayed original read failed")); await route.abort().catch(() => undefined); }
      finally { await response?.dispose(); finish(); }
    };
    await page.route("**" + V4 + "observation?**", handler, { times: 1 });
    await page.getByRole("button", { name: "刷新原始结果", exact: true }).click(); await ready;
    await switching.bringToFront();
    await expect.poll(() => page.evaluate(() => ({ visibility: document.visibilityState, focused: document.hasFocus() }))).toEqual({ visibility: "hidden", focused: false });
    await privateCleared(page);
    await navigateTo(switching, "退出登录");
    await login(switching, reader.username, reader.initialPassword); await changePassword(switching, reader.initialPassword, reader.password);
    const readerAuth = await enrollMfa(switching, reader.password);
    fixture.secrets.push(readerAuth.secret);
    const me = await readJSON(context, "/api/auth/me");
    fixture.secrets.push(me.csrfToken);
    invariant(me.ID === reader.actorId && me.username === reader.username && me.role === "viewer" && me.hasMfa === true && me.needChangePwd === false, "Actual reader session identity mismatch");
    const menu = (await readJSON(context, "/api/access/menu")).menu as { mark: string; auth: { readable: boolean; writeable: boolean } }[];
    invariant(JSON.stringify(menu.map(v => v.mark).sort()) === JSON.stringify(["directory_assets", "domains"]) && menu.every(v => v.auth.readable && !v.auth.writeable), "Actual reader has unexpected authority");
    readerMonitoring = true;
    release(); await delayedFinished; delayedFinished = undefined;
    invariant(genuineOldResponse && delivery.value !== "not_attempted", "Held genuine response was not drained");
    await privateCleared(page);
    const currentMe = page.waitForResponse(r => new URL(r.url()).pathname === "/api/auth/me" && r.status() === 200);
    await page.bringToFront();
    const checked = await currentMe;
    invariant((await checked.json()).ID === reader.actorId, "Native focus did not recheck actual changed actor");
    await privateCleared(page);
    // The previous actor's genuine hint exists, but cannot authorize this reader.
    await openCollection(page);
    await expect(page.getByRole("button", { name: "准备采集字典 4", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "申请取消原任务", exact: true })).toHaveCount(0);
    await privateCleared(page);
    await navigateTo(page, "组原始成员 · 字典 4");
    await page.getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true }).check();
    const observation = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "observation");
    await page.getByRole("button", { name: "核对并打开连接详情", exact: true }).click();
    const read = await observation;
    const query = new URL(read.url()).searchParams;
    const parsed = parseGroupMembersV4Page(await actualJSON(read, reader.actorId), { domainId: fixture.domainId, pageIdx: Number(query.get("pageIdx")), pageSize: Number(query.get("pageSize")) });
    invariant(parsed.available && parsed.observationId === original.taskUUID && parsed.list.length > 0, "Standalone actual reader did not read genuine v4 observation");
    await safeScreenshots(page, info, "readonly-members", fixture.secrets);
    invariant(readerUiBusinessPosts === 0, "Read-only browser emitted a business mutation");

    // Harness probes are deliberately separate from the browser write counter.
    // Each denied route receives its own genuine unused current proof; no replay
    // assumption, counter reset, forged 403 or credential fallback is used.
    const countInput = { taskUUID: original.taskUUID, domainId: original.domainId, actorId: reader.actorId };
    const countsBefore = await readDirectoryV4ReaderCounts(countInput);
    const denialStatuses: number[] = [];
    for (const [operation, data] of [
      ["sync", { domainId: fixture.domainId, expectedRevision: original.expectedRevision, expectedCredentialGeneration: original.expectedCredentialGeneration, idempotencyKey: randomUUID() }],
      ["cancel", { taskUUID: original.taskUUID }],
    ] as const) {
      let response: APIResponse;
      try { response = await context.request.post(V4 + operation, { headers: { Origin: new URL(page.url()).origin, "X-CSRF-Token": me.csrfToken }, data: { ...data, actorPassword: reader.password, totpCode: await freshCode(readerAuth) }, maxRedirects: 0, timeout: 15_000 }); }
      catch { throw new Error("Actual reader harness denial request failed"); }
      denialStatuses.push(response.status());
      invariant(response.status() === 403 && response.headers()["cache-control"] === "no-store", "Reader harness write was not genuinely denied");
      fixture.safe(await response.json()); await response.dispose();
    }
    const countsAfter = await readDirectoryV4ReaderCounts(countInput);
    invariant(JSON.stringify(countsBefore) === JSON.stringify(countsAfter), "Reader probes changed actual task/use/dependency/observation evidence");
    invariant(readerUiBusinessPosts === 0 && mutations.counts().sync === 1 && mutations.counts().cancel === 0, "Reader UI/harness evidence was conflated");
    leaks.assertClean();
    await writeEvidence(info, "readers", {
      producerUiSyncPosts: 1, readerUiBusinessPosts: 0, harnessDeniedSyncPosts: 1, harnessDeniedCancelPosts: 1,
      actualReaderIdentity: true, actualReaderOnlyReadMenu: true, standaloneReaderWithoutTasks: true,
      nativeHiddenUnfocused: true, trustedNativeFocusVisibility: true, notificationUnavailableFallback: true,
      proofClearedOnBlur: true, proofClearedOnHistory: true, oldActorIntentNotRestored: true,
      genuineOldSuccessHeld: true, lateReadAfterSessionChangeCleared: true, staleDelivery: delivery.value,
      harnessDenials403: denialStatuses.every(status => status === 403), proofEmptyScreenshots: true,
    }, {
      original: originalEvidence(original, actor.actorId), mutations: mutationEvidence(mutations.entries),
      readerActorSHA256: evidenceDigest([String(reader.actorId)]),
      readerMenu: menu.map(value => ({ mark: value.mark, readable: value.auth.readable, writeable: value.auth.writeable })).sort((a,b) => a.mark.localeCompare(b.mark)),
      readerObservationSHA256: evidenceDigest([parsed.observationId]),
      countsBefore, countsAfter, denialStatuses,
    });
  } finally {
    release();
    try { if (delayedFinished) await bounded(delayedFinished, 20_000, "Delayed actual read cleanup did not finish"); }
    finally { leaks.close(); mutations.close(); context.off("request", writeListener); await switching.close(); }
  }
});
