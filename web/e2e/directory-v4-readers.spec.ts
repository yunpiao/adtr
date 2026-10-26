import { test, expect, type Page, type APIResponse, type Route } from "@playwright/test";
import { configureV4Deadlines, v4Phase } from "./directory-v4-diagnostics";
import { randomUUID } from "node:crypto";
import { setupV4, requireFixture, fillSecret, login, changePassword, enrollMfa, freshCode, readJSON } from "./directory-v4-fixture";
import { V4, invariant, bounded, guardLeaks, evidenceDigest, originalEvidence, mutationEvidence, observeMutations, openCollection, prepareSync, enterProof, clickMutation, originalFrom, terminal, refreshOriginal, pinnedResults, safeScreenshots, proofEmpty, form, writeEvidence, actualJSON, readNativeFocusIdentity } from "./directory-v4-runtime";
import { readDirectoryV4ReaderCounts } from "./directory-v4-ledger-observer";
import { navigateTo } from "./navigation";
import { useNativeTabLifecycle, requireSameNativeWindow } from "./native-tab-lifecycle";
import { parseGroupMembersV4Page } from "../src/group-members-v4-read";
import { directoryV4DisplayMember } from "../src/directory-v4-facts";
import { frameV4Screenshot } from "./directory-v4-runtime";

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
// No extra /me consumer is mounted at this fixed Account target. These are
// direct root DOM checks, not a claim linking this DOM commit to one Response.
async function accountIdentitySettled(page: Page, actorId: number, username: string) {
  await expect(page.getByRole("heading", { name: "账户概览", exact: true })).toBeVisible();
  await expect(page.locator(".soc-session-status")).toHaveCount(0);
  await expect(page.getByText("正在确认登录状态…", { exact: true })).toHaveCount(0);
  await expect.poll(() => page.evaluate(({ actorId, username }) => {
    const main = document.querySelector<HTMLElement>("main.soc-main");
    const id = Array.from(main?.querySelectorAll("dt") ?? []).find(node => node.textContent === "用户 ID")?.nextElementSibling?.textContent;
    return !!main && !main.hidden && !main.inert && id === String(actorId) &&
      document.querySelector(".signed-in strong")?.textContent === username &&
      !document.querySelector('.directory-v4-workspace, .group-members-v4-workspace');
  }, { actorId, username })).toBe(true);
}
async function observeHiddenNativeActivation(page: Page) {
  await expect.poll(() => page.evaluate(() => ({ visibility: document.visibilityState, focused: document.hasFocus() }))).toEqual({ visibility: "hidden", focused: false });
  return page.evaluateHandle(() => {
    const events: { type: string; trusted: boolean; visible: boolean }[] = [];
    let overflow = false;
    const record = (event: Event) => {
      if (event.type === "focus" && event.target !== window) return;
      if (events.length >= 16) { overflow = true; return; }
      events.push({ type: event.type, trusted: event.isTrusted, visible: document.visibilityState === "visible" });
    };
    window.addEventListener("focus", record); document.addEventListener("visibilitychange", record);
    return {
      read: () => ({ visibility: document.visibilityState, focused: document.hasFocus(), events, overflow }),
      stop: () => { window.removeEventListener("focus", record); document.removeEventListener("visibilitychange", record); },
    };
  });
}
async function requireTrustedActivation(lifecycle: Awaited<ReturnType<typeof observeHiddenNativeActivation>>) {
  await expect.poll(() => lifecycle.evaluate(value => value.read())).toMatchObject({
    visibility: "visible", focused: true, overflow: false,
    events: expect.arrayContaining([expect.objectContaining({ type: "focus", trusted: true }), { type: "visibilitychange", trusted: true, visible: true }]),
  });
}

test("actual reader authority, native lifecycle and stale original reads never restore private state", async ({ page, context }, info) => {
  test.setTimeout(630_000);
  configureV4Deadlines(context);
  // Deliberately unavailable notification transports exercise the real native
  // focus fallback, not manufactured focus events or fake authentication.
  await page.addInitScript(() => {
    Object.defineProperty(window, "BroadcastChannel", { configurable: true, value: class { constructor() { throw new DOMException("Unavailable", "SecurityError"); } } });
    Object.defineProperty(window, "localStorage", { configurable: true, get() { throw new DOMException("Unavailable", "SecurityError"); } });
  });
  await bounded(useNativeTabLifecycle(page), 15_000, "Initial native lifecycle setup deadline exceeded");
  const fixture = await setupV4(page, context, { reader: true }), actor = fixture.producer;
  invariant(fixture.reader, "Actual reader setup absent");
  const reader = fixture.reader, mutations = observeMutations(context, actor);
  const leaks = guardLeaks(context, fixture.secrets);
  v4Phase("readers-tabs");
  const ownedPages: Page[] = [];
  const activationObservers: Awaited<ReturnType<typeof observeHiddenNativeActivation>>[] = [];
  let initialLifecycleCleanup: { stop: () => Promise<unknown>; dispose: () => Promise<void> } | undefined;
  let failed = false;
  let release = () => {}, delayedFinished: Promise<void> | undefined;
  let readerMonitoring = false, readerUiBusinessPosts = 0, readerBrowserAuthMutations = 0;
  const writeListener = (request: import("@playwright/test").Request) => {
    const path = new URL(request.url()).pathname;
    if (readerMonitoring && request.method() !== "GET" && request.method() !== "HEAD") {
      if (path.startsWith("/api/auth/")) readerBrowserAuthMutations++;
      else if (path !== "/api/access/check") readerUiBusinessPosts++;
    }
  };
  context.on("request", writeListener);
  try {
    // Every additional page is owned immediately, including failures during setup.
    const switching = await context.newPage(); ownedPages.push(switching);
    const appWitness = await context.newPage(); ownedPages.push(appWitness);
    await appWitness.addInitScript(() => {
      Object.defineProperty(window, "BroadcastChannel", { configurable: true, value: class { constructor() { throw new DOMException("Unavailable", "SecurityError"); } } });
      Object.defineProperty(window, "localStorage", { configurable: true, get() { throw new DOMException("Unavailable", "SecurityError"); } });
    });
    await bounded(useNativeTabLifecycle(appWitness), 15_000, "App witness native lifecycle setup deadline exceeded");
    await bounded(appWitness.goto("/#account"), 15_000, "App witness account setup deadline exceeded");
    await bounded(accountIdentitySettled(appWitness, actor.actorId, actor.username), 15_000, "Old actor account witness did not settle");
    invariant(new URL(appWitness.url()).hash === "#account", "App witness fixed account target changed");
    await bounded(useNativeTabLifecycle(switching), 15_000, "Switching native lifecycle setup deadline exceeded");
    await switching.goto("/");
    await bounded(requireSameNativeWindow([page, switching, appWitness]), 15_000, "Native window identity deadline exceeded");
    v4Phase("readers-focus");
    await page.bringToFront(); await openCollection(page); await prepareSync(page); await draft(page);
    const lifecycle = await page.evaluateHandle(() => {
      const events: { type: string; trusted: boolean; visible: boolean }[] = [];
      const record = (event: Event) => { if (event.type !== "focus" || event.target === window) events.push({ type: event.type, trusted: event.isTrusted, visible: document.visibilityState === "visible" }); };
      window.addEventListener("focus", record); document.addEventListener("visibilitychange", record);
      return { read: () => ({ visibility: document.visibilityState, focused: document.hasFocus(), events }), stop: () => { window.removeEventListener("focus", record); document.removeEventListener("visibilitychange", record); } };
    });
    initialLifecycleCleanup = { stop: () => lifecycle.evaluate(v => v.stop()), dispose: () => lifecycle.dispose() };
    await switching.bringToFront();
    await expect.poll(() => lifecycle.evaluate(v => v.read())).toMatchObject({ visibility: "hidden", focused: false });
    await proofEmpty(page); await privateCleared(page);
    await page.bringToFront();
    await expect.poll(() => lifecycle.evaluate(v => v.read())).toMatchObject({ visibility: "visible", focused: true, events: expect.arrayContaining([expect.objectContaining({ type: "focus", trusted: true }), { type: "visibilitychange", trusted: true, visible: true }]) });
    await lifecycle.evaluate(v => v.stop()); await lifecycle.dispose(); initialLifecycleCleanup = undefined;
    await expect(form(page, "sync")).toHaveCount(0); await proofEmpty(page);
    await prepareSync(page); await draft(page);
    // Real browser history navigation, not synthetic popstate/hashchange events.
    v4Phase("readers-history");
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
    v4Phase("readers-stale");
    let fetched!: () => void, failed!: (error: Error) => void, finish!: () => void;
    const ready = new Promise<void>((yes, no) => { fetched = yes; failed = no; });
    void ready.catch(() => undefined);
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
    await page.getByRole("button", { name: "刷新原始结果", exact: true }).click();
    await bounded(ready, 20_000, "Genuine delayed read was not ready");
    await switching.bringToFront();
    await expect.poll(() => page.evaluate(() => ({ visibility: document.visibilityState, focused: document.hasFocus() }))).toEqual({ visibility: "hidden", focused: false });
    await privateCleared(page);
    await navigateTo(switching, "退出登录");
    await login(switching, reader.username, reader.initialPassword); await changePassword(switching, reader.initialPassword, reader.password);
    const readerAuth = await enrollMfa(switching, reader.password);
    fixture.secrets.push(readerAuth.secret);
    readerMonitoring = true;
    const me = await readJSON(context, "/api/auth/me");
    fixture.secrets.push(me.csrfToken);
    invariant(me.ID === reader.actorId && me.username === reader.username && me.role === "viewer" && Number.isSafeInteger(me.priv) && me.priv >= 0 && me.hasMfa === true && me.needChangePwd === false && me.isExpired === false && typeof me.csrfToken === "string" && me.csrfToken.length > 0, "Actual reader session identity mismatch");
    const menu = (await readJSON(context, "/api/access/menu")).menu as { mark: string; auth: { readable: boolean; writeable: boolean } }[];
    invariant(JSON.stringify(menu.map(v => v.mark).sort()) === JSON.stringify(["directory_assets", "domains"]) && menu.every(v => v.auth.readable && !v.auth.writeable), "Actual reader has unexpected authority");
    // BrowserContext request events do not cover APIRequestContext. The source
    // contract separately closes harness calls to GETs and the two V4 denials.
    let readerSessionSamples = 1;
    const assertReaderSessionUnchanged = async () => {
      const current = await readJSON(context, "/api/auth/me");
      if (typeof current.csrfToken === "string") fixture.secrets.push(current.csrfToken);
      const keys = ["ID", "username", "role", "priv", "hasMfa", "needChangePwd", "isExpired", "csrfToken"] as const;
      invariant(keys.every(key => current[key] === me[key]) && readerBrowserAuthMutations === 0, "Reader session interval changed");
      readerSessionSamples++;
    };
    release(); await bounded(delayedFinished, 15_000, "Genuine delayed read did not drain"); delayedFinished = undefined;
    invariant(genuineOldResponse && delivery.value !== "not_attempted", "Held genuine response was not drained");
    await privateCleared(page);
    // This target was fixed and settled under the producer before actor change.
    // Strict original-body acceptance is isolated from lawful Workspace refresh.
    const appActivation = await observeHiddenNativeActivation(appWitness); activationObservers.push(appActivation);
    const checkedID = await readNativeFocusIdentity(appWitness);
    invariant(checkedID === reader.actorId, "Native focus did not recheck actual changed actor");
    await requireTrustedActivation(appActivation);
    await accountIdentitySettled(appWitness, reader.actorId, reader.username);
    invariant(new URL(appWitness.url()).hash === "#account", "App witness fixed account target changed");
    await assertReaderSessionUnchanged();

    // Independent original-Workspace obligation: allow its legitimate session
    // callback/forced refresh, without rescuing or replacing the App-only body.
    const workspaceActivation = await observeHiddenNativeActivation(page); activationObservers.push(workspaceActivation);
    await bounded(page.bringToFront(), 15_000, "Original Workspace native focus deadline exceeded");
    await requireTrustedActivation(workspaceActivation);
    await accountIdentitySettled(page, reader.actorId, reader.username);
    await assertReaderSessionUnchanged();
    await privateCleared(page);
    // The previous actor's genuine hint exists, but cannot authorize this reader.
    v4Phase("readers-authority"); await openCollection(page);
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
    await safeScreenshots(page, info, "readonly-members", fixture.secrets, async () => {
      const workspace = page.getByRole("region", { name: "组成员原始观测 · 字典 4", exact: true });
      const pin = workspace.locator("dd").filter({ hasText: original.taskUUID });
      await expect(pin).toHaveText(directoryV4DisplayMember(original.taskUUID));
      const rows = workspace.getByRole("list", { name: "本次观测的组", exact: true }).locator(":scope > li");
      await expect(rows).toHaveCount(parsed.list.length);
      for (let index = 0; index < parsed.list.length; index++) {
        await expect(rows.nth(index).locator("span").nth(0)).toHaveText(directoryV4DisplayMember(parsed.list[index].samAccountName ?? parsed.list[index].distinguishedName));
        await expect(rows.nth(index).locator("span").nth(1)).toHaveText(parsed.list[index].objectGUID);
      }
      await frameV4Screenshot(pin, [pin, rows.first()]);
    });
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
    await assertReaderSessionUnchanged();
    invariant(readerSessionSamples === 4 && readerBrowserAuthMutations === 0, "Reader session interval was not closed");
    leaks.assertClean();
    await writeEvidence(info, "readers", {
      producerUiSyncPosts: 1, readerUiBusinessPosts: 0, harnessDeniedSyncPosts: 1, harnessDeniedCancelPosts: 1,
      actualReaderIdentity: true, actualReaderOnlyReadMenu: true, standaloneReaderWithoutTasks: true,
      nativeHiddenUnfocused: true, trustedNativeFocusVisibility: true, notificationUnavailableFallback: true,
      proofClearedOnBlur: true, proofClearedOnHistory: true, oldActorIntentNotRestored: true,
      genuineOldSuccessHeld: true, lateReadAfterSessionChangeCleared: true, staleDelivery: delivery.value,
      harnessDenials403: denialStatuses.every(status => status === 403), proofEmptyScreenshots: true,
      appWitnessPredeclaredOldActorSettled: true, appWitnessAccountOnly: true,
      appWitnessTrustedNativeActivation: true, appWitnessOriginalMarkedBody: true,
      originalWorkspaceTrustedNativeActivation: true, originalWorkspaceReaderGateOpen: true,
      readerSessionSamples: 4, readerBrowserAuthMutations: 0, checkedSessionContinuity: true,
    }, {
      original: originalEvidence(original, actor.actorId), mutations: mutationEvidence(mutations.entries),
      readerActorSHA256: evidenceDigest([String(reader.actorId)]),
      readerMenu: menu.map(value => ({ mark: value.mark, readable: value.auth.readable, writeable: value.auth.writeable })).sort((a,b) => a.mark.localeCompare(b.mark)),
      readerObservationSHA256: evidenceDigest([parsed.observationId]),
      countsBefore, countsAfter, denialStatuses,
      identityWitness: {
        kind: "split-app-account-and-workspace-v1", appPageRole: "predeclared-account", workspacePageRole: "original-workspace",
        samePageAtomicBinding: false, producerActorSHA256: evidenceDigest([String(actor.actorId)]),
        appReaderActorSHA256: evidenceDigest([String(checkedID)]), workspaceReaderActorSHA256: evidenceDigest([String(reader.actorId)]),
        sessionContinuity: "checked-identity-and-csrf-in-memory", identitySamples: readerSessionSamples,
        browserAuthMutations: readerBrowserAuthMutations, harnessAuthWrites: "source-closed-none",
        auxiliaryPageCleanup: "source-bound-after-finally",
      },
    });
  } catch (error) {
    failed = true;
    throw error; // Preserve the first test failure even if owned cleanup also fails.
  } finally {
    release();
    // Shared wait budget, then bounded best-effort tail attempts; not a hard
    // total wall-clock guarantee if the event loop itself is delayed.
    const cleanupDeadline = Date.now() + 20_000;
    let cleanupFailed = false;
    const cleanup = async (action: () => Promise<unknown>) => {
      try { await bounded(action(), Math.max(1, cleanupDeadline - Date.now()), "Owned reader witness cleanup deadline exceeded"); }
      catch { cleanupFailed = true; }
    };
    if (delayedFinished) await cleanup(() => delayedFinished!);
    if (initialLifecycleCleanup) {
      await cleanup(initialLifecycleCleanup.stop);
      await cleanup(initialLifecycleCleanup.dispose);
    }
    for (const observer of activationObservers) {
      await cleanup(() => observer.evaluate(value => value.stop()));
      await cleanup(() => observer.dispose());
    }
    await cleanup(async () => { leaks.close(); });
    await cleanup(async () => { mutations.close(); });
    await cleanup(async () => { context.off("request", writeListener); });
    for (const owned of [...ownedPages].reverse()) await cleanup(() => owned.close());
    if (cleanupFailed) {
      if (!failed) throw new Error("Owned reader witness cleanup failed");
      console.log("[v4-readers-cleanup] failed-after-original-failure");
    }
  }
});
