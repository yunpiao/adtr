import { test, expect } from "@playwright/test";
import { configureV4Deadlines, v4Phase } from "./directory-v4-diagnostics";
import { setupV4, requireFixture } from "./directory-v4-fixture";
import { readDirectoryV4KeyCounts, readDirectoryV4Use } from "./directory-v4-ledger-observer";
import { V4, invariant, guardLeaks, evidenceDigest, originalEvidence, mutationEvidence, observeMutations, openCollection, prepareSync, enterProof, clickMutation, originalFrom, terminal, recover, refreshOriginal, acknowledge, committedLoss, pinnedResults, safeScreenshots, proofEmpty, form, writeEvidence } from "./directory-v4-runtime";

process.env.PLAYWRIGHT_NO_COPY_PROMPT = "1";
test.use({ trace: "off", screenshot: "off", video: "off" });
test.beforeAll(() => requireFixture("controls"));
function assertPersistedOriginal(value: Awaited<ReturnType<typeof readDirectoryV4KeyCounts>>, taskUUID: string) {
  invariant(value.tasks === 1 && value.uses === 1 && value.observations === 1 && value.taskState === "succeeded" &&
    value.taskSHA256 === evidenceDigest([taskUUID]), "Original key did not persist exactly the expected succeeded task/use/observation");
}
test("real UI admissions, original pinned results and two distinct response-loss recoveries", async ({ page, context }, info) => {
  test.setTimeout(630_000);
  configureV4Deadlines(context);
  const fixture = await setupV4(page, context, { reader: false });
  const actor = fixture.producer, mutations = observeMutations(context, actor);
  const leaks = guardLeaks(context, fixture.secrets);
  let newer: import("@playwright/test").Page | undefined;
  try {
    v4Phase("controls-first");
    await openCollection(page); await prepareSync(page); await enterProof(page, actor, "sync");
    const firstTask = await clickMutation(page, actor, fixture.domainId, "sync");
    const first = originalFrom(firstTask, mutations.entries.at(-1)!);
    await terminal(context, actor, first, "succeeded"); await refreshOriginal(page);
    const firstPersisted = await readDirectoryV4KeyCounts({ ...first, actorId: actor.actorId });
    assertPersistedOriginal(firstPersisted, first.taskUUID);
    const firstSemantics = await pinnedResults(page, actor, first);
    await safeScreenshots(page, info, "original-raw-members", fixture.secrets);

    // A new tab starts with no operation hint. Its key originates in its own UI
    // submit; no copied/injected storage, invented task link or historical key.
    v4Phase("controls-loss");
    newer = await context.newPage(); await newer.bringToFront(); await openCollection(newer);
    await expect(newer.getByRole("region", { name: "原操作恢复", exact: true })).toHaveCount(0);
    await prepareSync(newer); await enterProof(newer, actor, "sync");
    const loss = await committedLoss(newer, actor, fixture.domainId, "sync");
    await form(newer, "sync").getByRole("button", { name: "确认提交采集", exact: true }).click();
    const secondTask = await loss.completed;
    const second = originalFrom(secondTask, mutations.entries.at(-1)!);
    invariant(first.taskUUID !== second.taskUUID && first.idempotencyKey !== second.idempotencyKey, "Independent UI operations must remain distinct");
    const afterLoss = mutations.counts().sync;
    await terminal(context, actor, second, "succeeded");
    const lossBefore = await readDirectoryV4KeyCounts({ ...second, actorId: actor.actorId });
    assertPersistedOriginal(lossBefore, second.taskUUID);
    const lossIdentityBefore = (await readDirectoryV4Use({ ...second, actorId: actor.actorId })).evidence;
    const secondReceipt = await recover(newer, actor, second); const secondSemantics = await pinnedResults(newer, actor, second);
    const lossAfter = await readDirectoryV4KeyCounts({ ...second, actorId: actor.actorId });
    assertPersistedOriginal(lossAfter, second.taskUUID);
    const lossIdentityAfter = (await readDirectoryV4Use({ ...second, actorId: actor.actorId })).evidence;
    invariant(JSON.stringify(lossBefore) === JSON.stringify(lossAfter) && lossIdentityBefore.identitySHA256 === lossIdentityAfter.identitySHA256, "Receipt-found recovery changed persisted original identity/count/digest");
    invariant(mutations.counts().sync === afterLoss, "Receipt-found recovery sent another business POST");
    await page.bringToFront(); const originalReceipt = await recover(page, actor, first); const originalAfterNewer = await pinnedResults(page, actor, first);
    invariant(mutations.counts().sync === afterLoss, "Original tab re-entry changed business request count");
    await acknowledge(page);

    // Genuine before-dispatch loss: route aborts without route.fetch/continue.
    // Receipt 404 must be produced by the actual server, not interception.
    v4Phase("controls-absent");
    await prepareSync(page); await enterProof(page, actor, "sync");
    let prevented = 0;
    await page.route("**" + V4 + "sync", async route => { prevented++; await route.abort("failed"); }, { times: 1 });
    const before = mutations.entries.length;
    await form(page, "sync").getByRole("button", { name: "确认提交采集", exact: true }).click();
    await expect.poll(() => mutations.entries.length).toBe(before + 1);
    const uncertain = mutations.entries.at(-1)!;
    invariant(prevented === 1, "Expected exactly one genuine pre-dispatch network loss");
    await proofEmpty(page);
    const receipt = page.waitForResponse(r => new URL(r.url()).pathname === V4 + "receipt");
    await page.getByRole("button", { name: "重新核验并恢复", exact: true }).click();
    const notFound = await receipt;
    invariant(notFound.status() === 404 && new URL(notFound.url()).searchParams.get("idempotencyKey") === uncertain.original.idempotencyKey, "Expected genuine original-key receipt 404");
    const uncertainPins = uncertain.original;
    invariant(typeof uncertainPins.domainId === "string" && typeof uncertainPins.expectedRevision === "string" && typeof uncertainPins.expectedCredentialGeneration === "string" && typeof uncertainPins.idempotencyKey === "string", "Missing authentic pre-dispatch source/key");
    const absentPersisted = await readDirectoryV4KeyCounts({ domainId: uncertainPins.domainId, expectedRevision: uncertainPins.expectedRevision, expectedCredentialGeneration: uncertainPins.expectedCredentialGeneration, idempotencyKey: uncertainPins.idempotencyKey, actorId: actor.actorId });
    invariant(absentPersisted.tasks === 0 && absentPersisted.uses === 0 && absentPersisted.observations === 0, "Pre-dispatch key unexpectedly exists in PostgreSQL");
    const beforeExplicitRetry = mutations.counts().sync;
    await expect(page.getByRole("button", { name: "使用原键和原版本显式重试", exact: true })).toBeVisible();
    invariant(mutations.counts().sync === beforeExplicitRetry, "Receipt 404 automatically resent sync");
    await page.getByRole("button", { name: "使用原键和原版本显式重试", exact: true }).click();
    await enterProof(page, actor, "sync");
    const retriedTask = await clickMutation(page, actor, fixture.domainId, "sync");
    const retried = originalFrom(retriedTask, mutations.entries.at(-1)!);
    invariant(JSON.stringify(uncertain.original) === JSON.stringify(mutations.entries.at(-1)!.original), "Explicit retry changed original source pins or key");
    invariant(mutations.entries.at(-1)!.at > uncertain.at && mutations.entries.at(-1)!.currentProof, "Retry did not use a new current proof");
    await terminal(context, actor, retried, "succeeded"); await refreshOriginal(page); const retriedSemantics = await pinnedResults(page, actor, retried);
    const retryPersisted = await readDirectoryV4KeyCounts({ ...retried, actorId: actor.actorId });
    assertPersistedOriginal(retryPersisted, retried.taskUUID);
    invariant(retryPersisted.tasks === 1 && retryPersisted.uses === 1 && retryPersisted.keySHA256 === absentPersisted.keySHA256, "Explicit retry did not create exactly one original persisted task/use");
    await safeScreenshots(page, info, "recovered-original", fixture.secrets);
    invariant(mutations.counts().sync === 4 && mutations.counts().cancel === 0 && mutations.entries.every(e => e.currentProof), "Actual controls mutation cardinality/proof failure");
    leaks.assertClean();
    await writeEvidence(info, "controls", {
      actualUiSyncPosts: 4, actualServerAdmissions: 3, beforeDispatchAborts: 1, actualUiCancelPosts: 0,
      committedLossReceiptFound: true, committedLossFurtherPosts: 0, genuineReceipt404: true,
      explicitSameKeySamePinsRetry: true, retryFreshCurrentProof: true, independentTabIntents: true,
      originalPinnedAfterNewer: true, rawAbsent: firstSemantics.absent, rawEmpty: firstSemantics.empty,
      rawValuesOriginalOrder: firstSemantics.values, proofEmptyScreenshots: true,
    }, {
      first: originalEvidence(first, actor.actorId), second: originalEvidence(second, actor.actorId), retried: originalEvidence(retried, actor.actorId),
      mutations: mutationEvidence(mutations.entries), firstResult: firstSemantics, secondResult: secondSemantics,
      originalAfterNewerResult: originalAfterNewer, retriedResult: retriedSemantics,
      secondReceipt, originalReceipt,
      absentReceipt: { status: notFound.status(), keySHA256: evidenceDigest([new URL(notFound.url()).searchParams.get("idempotencyKey")]), domainSHA256: evidenceDigest([new URL(notFound.url()).searchParams.get("domainId")]) },
      firstPersisted, lossBefore, lossAfter, lossIdentityBefore, lossIdentityAfter, absentPersisted, retryPersisted,
    });
  } finally { leaks.close(); mutations.close(); await newer?.close(); }
});
