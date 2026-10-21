import { test, expect } from "@playwright/test";
import { configureV4Deadlines, v4Phase } from "./directory-v4-diagnostics";
import { waitForV4CancelUnknownUI } from "./directory-v4-runtime";
import { setupV4, requireFixture, lateFreshCode, fillSecret, totp } from "./directory-v4-fixture";
import { V4, invariant, guardLeaks, originalEvidence, mutationEvidence, observeMutations, openCollection, prepareSync, enterProof, clickMutation, originalFrom, terminal, recover, refreshOriginal, acknowledge, committedLoss, proofEmpty, form, workerControl, writeEvidence, taskResponse, type Original, type Actor } from "./directory-v4-runtime";
import { readDirectoryV4Use, assertQueuedReserved, assertQueuedQuiesced, assertRunningOpened, assertExecutorReturned, type DirectoryV4LedgerSnapshot } from "./directory-v4-ledger-observer";

process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
// Test-only, bounded scalar diagnostics. No raw task fields enter the buffer.
function cancelHeartbeatDiagnostics(startedAt: number) {
  const samples: string[] = [];
  let ordinal = 0;
  const bounded = (value: number) => Number.isFinite(value) ? Math.max(0, Math.min(900_000, Math.floor(value))) : 0;
  return {
    record(previous: Awaited<ReturnType<typeof taskResponse>>, current: Awaited<ReturnType<typeof taskResponse>>, now: number) {
      const before = Date.parse(previous.updatedAt), after = Date.parse(current.updatedAt);
      samples.push(JSON.stringify({
        sample: bounded(++ordinal), elapsedMs: bounded(now - startedAt),
        sameVersion: current.resultVersion === previous.resultVersion,
        updatedAt: !Number.isFinite(before) || !Number.isFinite(after) ? "invalid" : after > before ? "advanced" : after === before ? "unchanged" : "regressed",
        runningProgress: current.state === "running" && current.progress === 41 ? "running-41" : "outside-window",
      }));
      if (samples.length > 16) samples.shift();
    },
    report() { console.log("[v4-cancel-heartbeat] " + JSON.stringify(samples.map(sample => JSON.parse(sample)))); },
  };
}
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture("cancel"));
async function queryUI(page: import("@playwright/test").Page, actor: Actor, original: Original) {
  const pending = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "task" && r.request().method() === "GET");
  await refreshOriginal(page);
  return taskResponse(await pending, actor, original.domainId, "read", original.taskUUID);
}
test("queued never-opened cancellation and exact original executor return during genuine paged I/O", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  configureV4Deadlines(context);
  const control = workerControl();
  const initialControl = await control.state();
  invariant(initialControl.state === "not_started" && initialControl.launchCount === 0 && initialControl.failure === null, "Worker must not exist before queued scenario");
  const fixture = await setupV4(page, context, { reader: false }), actor = fixture.producer;
  const mutations = observeMutations(context, actor);
  const leaks = guardLeaks(context, fixture.secrets);
  let pendingOpened: Promise<DirectoryV4LedgerSnapshot> | undefined;
  try {
    v4Phase("cancel-queued");
    await openCollection(page); await prepareSync(page); await enterProof(page, actor, "sync");
    const queuedTask = await clickMutation(page, actor, fixture.domainId, "sync");
    invariant(queuedTask.state === "queued" && queuedTask.attempt === 0, "Queued fixture started unexpectedly");
    const queued = originalFrom(queuedTask, mutations.entries.at(-1)!), queuedPins = { ...queued, actorId: actor.actorId };
    const reserved = await readDirectoryV4Use(queuedPins); assertQueuedReserved(reserved);
    invariant((await control.state()).launchCount === 0, "Worker started during queued setup");
    await page.getByRole("button", { name: "申请取消原任务", exact: true }).click();
    await enterProof(page, actor, "cancel");
    const queuedLoss = await committedLoss(page, actor, fixture.domainId, "cancel", "cancelled", queued.taskUUID);
    await form(page, "cancel").getByRole("button", { name: "确认取消原任务", exact: true }).click();
    const queuedCancelled = await queuedLoss.completed;
    invariant(queuedCancelled.attempt === 0, "Queued cancellation consumed an executor attempt");
    await waitForV4CancelUnknownUI(page, mutations.counts, { sync: 1, cancel: 1 });
    const queuedReceipt = await recover(page, actor, queued);
    invariant(mutations.counts().sync === 1 && mutations.counts().cancel === 1, "Queued recovery repeated business POST");
    const cancelledBeforeWorker = await readDirectoryV4Use(queuedPins); assertQueuedReserved(cancelledBeforeWorker, true);
    invariant((await control.state()).launchCount === 0, "Queued original opened before controller release");
    v4Phase("cancel-worker"); await control.startOnce();
    await expect.poll(async () => (await readDirectoryV4Use(queuedPins)).facts.state, { timeout: 15_000, intervals: [100, 250] }).toBe("quiesced");
    const queuedQuiesced = await readDirectoryV4Use(queuedPins); assertQueuedQuiesced(queuedQuiesced, reserved);
    await acknowledge(page);

    v4Phase("cancel-running"); await prepareSync(page);
    await enterProof(page, actor, "sync", await lateFreshCode(actor.auth));
    const runningTask = await clickMutation(page, actor, fixture.domainId, "sync");
    const running = originalFrom(runningTask, mutations.entries.at(-1)!), runningPins = { ...running, actorId: actor.actorId };
    const dispatch = mutations.entries.at(-1)!;
    invariant(dispatch.currentProof && dispatch.at % 30_000 >= 26_000 && dispatch.at % 30_000 < 29_000, "Actual sync missed genuine late-counter window");
    let baseline = await queryUI(page, actor, running);
    await expect.poll(async () => { baseline = await queryUI(page, actor, running); invariant(["queued", "running"].includes(baseline.state) && baseline.progress <= 41, "Missed running page-two window"); return baseline.state === "running" && baseline.progress === 41; }, { timeout: 5_000, intervals: [100] }).toBe(true);
    await expect(page.getByRole("region", { name: "原任务状态", exact: true })).toContainText("进度 41%");
    // Begin the exact-opener observer now, not after cancellation. Its failure
    // is drained on all exits; no unowned process or detached rejection remains.
    pendingOpened = readDirectoryV4Use(runningPins);
    void pendingOpened.catch(() => undefined);
    await page.getByRole("button", { name: "申请取消原任务", exact: true }).click();
    const draft = totp(actor.auth.secret, Date.now());
    await enterProof(page, actor, "cancel", draft);
    const proofAt = Date.now();
    const first = await queryUI(page, actor, running);
    invariant(first.state === "running" && first.progress === 41, "Proof interval missed actual page-two I/O");
    let heartbeat = first, previous = first, heartbeatFirst = first;
    const heartbeatDiagnostics = cancelHeartbeatDiagnostics(performance.now());
    try { await expect.poll(async () => {
      heartbeat = await queryUI(page, actor, running);
      heartbeatDiagnostics.record(previous, heartbeat, performance.now());
      invariant(heartbeat.state === "running" && heartbeat.progress === 41, "Missed genuine stable-page heartbeat window");
      const matched = heartbeat.resultVersion === previous.resultVersion && Date.parse(heartbeat.updatedAt) > Date.parse(previous.updatedAt);
      if (matched) heartbeatFirst = previous;
      previous = heartbeat;
      return matched;
    }, { timeout: 3_000, intervals: [100] }).toBe(true);
    } catch (error) {
      try { heartbeatDiagnostics.report(); } finally { throw error; }
    }
    invariant(Date.now() > proofAt, "No real heartbeat interval observed");
    // Only booleans enter reporters; no secret-valued expect.toHaveValue.
    invariant(await form(page, "cancel").getByLabel("当前用户密码", { exact: true }).inputValue() === actor.password && await form(page, "cancel").getByLabel("当前六位验证码", { exact: true }).inputValue() === draft, "Proof draft did not survive genuine heartbeat");
    const opened = await pendingOpened; pendingOpened = undefined; assertRunningOpened(opened);
    let fourth = heartbeat;
    await expect.poll(async () => { fourth = await queryUI(page, actor, running); invariant(fourth.state === "running" && fourth.progress <= 42, "Missed genuine fourth-page I/O window"); return fourth.progress; }, { timeout: 4_000, intervals: [100] }).toBe(42);
    await expect(page.getByRole("region", { name: "原任务状态", exact: true })).toContainText("进度 42%");
    invariant(await form(page, "cancel").getByLabel("当前用户密码", { exact: true }).inputValue() === actor.password && await form(page, "cancel").getByLabel("当前六位验证码", { exact: true }).inputValue() === draft, "Proof draft lost during actual progress update");
    const now = Date.now(), counter = Math.floor(now / 30_000);
    invariant(counter > actor.auth.lastCounter, "Next current unused counter not available inside I/O window");
    actor.auth.lastCounter = counter;
    await fillSecret(form(page, "cancel").getByLabel("当前六位验证码", { exact: true }), totp(actor.auth.secret, now));
    const runningLoss = await committedLoss(page, actor, fixture.domainId, "cancel", "cancel_requested", running.taskUUID);
    await form(page, "cancel").getByRole("button", { name: "确认取消原任务", exact: true }).click();
    const accepted = await runningLoss.completed, cancelDispatch = mutations.entries.at(-1)!;
    invariant(accepted.attempt === 1 && accepted.progress >= 42 && accepted.progress < 95 && cancelDispatch.currentProof && cancelDispatch.at - dispatch.at < 10_000, "Real running cancel missed bounded I/O or proof window");
    await waitForV4CancelUnknownUI(page, mutations.counts, { sync: 2, cancel: 2 });
    const runningReceipt = await recover(page, actor, running);
    await terminal(context, actor, running, "cancelled");
    await expect.poll(async () => (await readDirectoryV4Use(runningPins)).facts.state, { timeout: 15_000, intervals: [100, 250] }).toBe("quiesced");
    const returned = await readDirectoryV4Use(runningPins); assertExecutorReturned(returned, opened);
    const queuedAfterRunning = await readDirectoryV4Use(queuedPins); assertQueuedQuiesced(queuedAfterRunning, reserved);
    await refreshOriginal(page); await proofEmpty(page);
    await expect(page.getByRole("region", { name: "原任务状态", exact: true })).toContainText("任务终态接口不提供执行器返回见证或依赖释放状态");
    await expect(page.getByRole("button", { name: "申请取消原任务", exact: true })).toHaveCount(0);
    invariant(mutations.counts().sync === 2 && mutations.counts().cancel === 2, "Cancel suite must have exactly two real sync and two real cancel POSTs");
    invariant((await control.state()).state === "ready" && (await control.state()).launchCount === 1, "Original one Worker ownership changed");
    // Last task observation immediately before returning ownership to runner cleanup.
    const queuedBeforeCleanup = await readDirectoryV4Use(queuedPins); assertQueuedQuiesced(queuedBeforeCleanup, reserved);
    leaks.assertClean();
    await writeEvidence(info, "cancel", {
      actualUiSyncPosts: 2, actualUiCancelPosts: 2, workerLaunchCount: 1,
      queuedCommittedLoss: true, queuedReloadFurtherPosts: 0, queuedAttempt: 0,
      queuedNeverOpened: true, queuedDependencies: 0, queuedObservations: 0,
      queuedRecheckedAfterRunning: true, queuedRecheckedBeforeCleanup: true,
      runningLateAdmission: true, stableHeartbeatDuringProof: true, proofPreserved: true,
      runningCurrentNextCounter: true, realCancelRequested: true, runningCommittedLoss: true,
      runningReloadFurtherPosts: 0, runningAttempt: 1, originalExecutorReturned: true,
      originalOpenerTupleUnchanged: true, runningDependencies: 0, runningObservations: 0,
      connectionBindingPreserved: true, terminal409Accepted: false,
    }, {
      queued: originalEvidence(queued, actor.actorId), running: originalEvidence(running, actor.actorId),
      queuedReceipt, runningReceipt,
      mutations: mutationEvidence(mutations.entries),
      queuedReserved: reserved.evidence, queuedCancelledBeforeWorker: cancelledBeforeWorker.evidence,
      queuedQuiesced: queuedQuiesced.evidence, queuedAfterRunning: queuedAfterRunning.evidence,
      queuedBeforeCleanup: queuedBeforeCleanup.evidence, runningOpened: opened.evidence, runningReturned: returned.evidence,
      runningAccepted: { state: accepted.state, attempt: accepted.attempt, progress: accepted.progress },
      heartbeat: { firstVersion: heartbeatFirst.resultVersion, laterVersion: heartbeat.resultVersion, firstUpdatedAt: heartbeatFirst.updatedAt, laterUpdatedAt: heartbeat.updatedAt, firstProgress: heartbeatFirst.progress, laterProgress: heartbeat.progress },
    });
  } finally { if (pendingOpened) await pendingOpened.catch(() => undefined); leaks.close(); mutations.close(); }
});
