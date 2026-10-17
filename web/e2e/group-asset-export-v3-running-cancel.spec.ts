import { test, expect } from "@playwright/test";
import { proof } from "./group-assets-v3-fixture";
import { exportPrefix, hash, responseValue } from "./group-asset-export-v3-helpers";
import { chooseColumns, createExport, panelFor, pauseOwnedExportWorker, receiptAndDetail, restoreOwnedExportWorker, resumeOwnedExportWorker, safeColumns, terminalExport, terminalSetup, unavailableDownload } from "./group-asset-export-v3-terminal-helpers";
import { captureRunningStatus, runningExport, runningGuard, saveRunningStatus, writeRunningEvidence } from "./group-asset-export-v3-running-cancel-helpers";

test.use({ trace: "off", screenshot: "off", video: "off" });
test("real group snapshot running export accepts cancellation then clears all artifacts", async ({ page, context }, info) => {
  test.setTimeout(900_000);
  expect(process.env.ADTR_E2E_GROUP_ASSET_EXPORT_V3_RUNNING_CANCEL).toBe("true");
  const setup = await terminalSetup(page, context), panel = panelFor(page);
  expect(setup.source.dictionaryVersion).toBe(3);
  const source = JSON.parse(Buffer.from(setup.source.bodyBase64, "base64").toString("utf8")) as { objects: { base: { kind: string; objectClass: string[] } }[] };
  expect(source.objects).toHaveLength(30);
  expect(setup.source.count).toBe(30);
  expect(Object.fromEntries(["user", "group", "computer"].map((kind) => [kind, source.objects.filter((object) => object.base.kind === kind).length]))).toEqual({ user: 3, group: 23, computer: 4 });
  let downloads = 0;
  page.on("download", () => { downloads++; });
  await chooseColumns(page, safeColumns);
  expect(await runningGuard("prepare")).toEqual({ prepared: true });
  const pause = await pauseOwnedExportWorker();
  let checkpointStarted = false, resumed = false;
  try {
    const operation = await createExport(page, setup), taskID = operation.create.taskID;
    expect(operation.create.task.state).toBe("queued");
    expect(operation.create.task.taskName).toBe("group_assets.v3.export");
    expect(operation.create.request).toMatchObject({ search: "group-", creatTm: "desc", screenColumns: safeColumns });
    expect(operation.create.request).not.toHaveProperty("startTm");
    expect(operation.create.request).not.toHaveProperty("endTm");
    const queued = await terminalExport(taskID);
    expect(queued).toMatchObject({ kind: "group_assets.v3.export", state: "queued", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    // Fresh-code waiting belongs to the initial 120-second pause. It must never
    // consume the separate 10-second running pause or the production lease.
    await proof(page, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);
    checkpointStarted = true;
    const checkpoint = await runningGuard("checkpoint", { taskID, workerID: pause.workerID });
    expect(checkpoint).toMatchObject({ taskID, kind: "group_assets.v3.export", state: "running", attempt: 1, progress: 10,
      snapshotCount: 1, manifestCount: 0, chunkCount: 0, allThreadsStopped: true, unsafeTransactions: 0,
      activityLockReleased: true, observerStartedBeforeResume: true, checkpointCaught: true });
    expect(checkpoint.leaseRemainingSeconds).toBeGreaterThan(20);
    expect(checkpoint.fencingToken).toBeGreaterThan(0);
    expect(checkpoint.blockedOwnedBackends).toBeGreaterThan(0);
    expect(checkpoint.workerIDSHA256).toBe(hash(pause.workerID));
    expect(checkpoint.applicationNameSHA256).toBe(pause.applicationNameSHA256);
    // Refresh the genuine UI without replacing or re-entering its prepared MFA.
    const runningReceipt = await receiptAndDetail(page, setup.actorId, taskID);
    expect(runningReceipt.detail).toMatchObject({ taskID, downloadReady: false, task: { state: "running", progress: 10, attempt: 1 } });
    const runningDatabase = await runningExport(taskID);
    expect(runningDatabase).toMatchObject({ kind: "group_assets.v3.export", state: "running", progress: 10, attempt: 1, snapshotCount: 1, manifestCount: 0, chunkCount: 0, result: {} });
    expect(runningDatabase.sourceSHA256).toBe(setup.source.sha256);
    expect(runningDatabase.observationID).toBe(setup.source.taskUUID);
    expect(runningDatabase.fencingToken).toBe(checkpoint.fencingToken);
    expect(runningDatabase.leaseUntilEpoch - runningDatabase.databaseClock).toBeGreaterThan(10);
    expect(hash(runningDatabase.leaseOwner)).toBe(checkpoint.leaseOwnerSHA256);
    const runningImage = await captureRunningStatus(page, taskID, "running", "running-checkpoint.png");
    const [cancelResponse] = await Promise.all([
      page.waitForResponse((r) => new URL(r.url()).pathname === `${exportPrefix}/cancel` && r.request().method() === "POST", { timeout: 2000 }),
      panel.getByRole("button", { name: "取消组导出任务", exact: true }).click({ timeout: 2000 }),
    ]);
    const cancel = await responseValue(cancelResponse, setup.actorId);
    expect(cancel).toMatchObject({ taskID, task: { state: "cancel_requested", attempt: 1 } });
    const pendingReceipt = await receiptAndDetail(page, setup.actorId, taskID);
    expect(pendingReceipt.detail).toMatchObject({ taskID, downloadReady: false, task: { state: "cancel_requested", attempt: 1 } });
    const pendingDatabase = await runningExport(taskID);
    expect(pendingDatabase).toMatchObject({ kind: "group_assets.v3.export", state: "cancel_requested", progress: 10, attempt: 1, snapshotCount: 1, manifestCount: 0, chunkCount: 0, result: {} });
    expect(pendingDatabase.sourceSHA256).toBe(setup.source.sha256);
    expect(pendingDatabase.observationID).toBe(setup.source.taskUUID);
    expect(pendingDatabase.fencingToken).toBe(checkpoint.fencingToken);
    expect(pendingDatabase.leaseUntilEpoch - pendingDatabase.databaseClock).toBeGreaterThan(10);
    expect(pendingDatabase.leaseOwner).toBe(runningDatabase.leaseOwner);
    expect(pendingDatabase.leaseUntil).toBe(runningDatabase.leaseUntil);
    await expect(panel.getByRole("button", { name: "取消组导出任务", exact: true })).toBeDisabled({ timeout: 1500 });
    const pendingImage = await captureRunningStatus(page, taskID, "cancel_requested", "cancel-requested.png");
    // Resume before attachments, terminal polling or another fresh proof wait.
    const resume = await runningGuard("resume");
    resumed = true;
    expect(resume).toMatchObject({ resumed: true, watchdogExpired: false });
    expect(resume.runningStoppedSeconds).toBeGreaterThanOrEqual(0);
    expect(resume.runningStoppedSeconds).toBeLessThan(10);
    await expect.poll(async () => (await terminalExport(taskID)).state, { timeout: 15_000 }).toBe("cancelled");
    const database = await terminalExport(taskID);
    expect(database).toMatchObject({ kind: "group_assets.v3.export", state: "cancelled", error: "cancelled", attempt: 1, result: {}, leaseOwner: "", leaseUntil: null,
      snapshotCount: 0, manifestCount: 0, chunkCount: 0 });
    const recovered = await receiptAndDetail(page, setup.actorId, taskID);
    expect(recovered.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    const unavailable = await unavailableDownload(context, taskID);
    const cycles = await resumeOwnedExportWorker(context, pause, taskID);
    expect(cycles.sameIdentity).toBe(true);
    const postResume = { ...await receiptAndDetail(page, setup.actorId, taskID), database: await terminalExport(taskID), unavailableDownload: await unavailableDownload(context, taskID) };
    expect(postResume.database).toEqual(database);
    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    await expect(panel.getByRole("button", { name: "下载组 XLSX", exact: true })).toHaveCount(0);
    expect(downloads).toBe(0);
    const terminalImage = await captureRunningStatus(page, taskID, "cancelled", "cancelled-terminal.png");
    for (const image of [runningImage, pendingImage, terminalImage]) await saveRunningStatus(info, image);
    const { leaseOwner: runningOwner, ...runningSafe } = runningDatabase;
    const { leaseOwner: pendingOwner, ...pendingSafe } = pendingDatabase;
    const { workerID: pauseWorker, ...pauseSafe } = pause;
    const { workerID: cyclesWorker, ...cyclesSafe } = cycles;
    await writeRunningEvidence(info, setup, [{ name: "cancelled", ...operation, request: operation.create.request, queued,
      running: { ...runningReceipt, database: { ...runningSafe, leaseOwnerSHA256: hash(runningOwner) }, screenshot: runningImage.metadata },
      pending: { cancel, ...pendingReceipt, database: { ...pendingSafe, leaseOwnerSHA256: hash(pendingOwner) }, screenshot: pendingImage.metadata },
      ...recovered, database, postResume, unavailableDownload: unavailable,
      ui: { downloadButtons: 0, downloadEvents: downloads }, screenshot: terminalImage.metadata,
    }], { pause: { ...pauseSafe, workerIDSHA256: hash(pauseWorker) }, checkpoint, resume, cycles: { ...cyclesSafe, workerIDSHA256: hash(cyclesWorker) } });
  } finally {
    try { if (checkpointStarted && !resumed) await runningGuard("resume"); }
    finally { await restoreOwnedExportWorker(); }
  }
});
