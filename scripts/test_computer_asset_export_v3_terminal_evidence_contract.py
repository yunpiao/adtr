"""Pure-Python corruption tests. Generated unit specimens are NOT E2E artifacts."""
import base64
import copy
import hashlib
import json
import re
from pathlib import Path
import unittest
import validate_computer_asset_export_v3_terminal_evidence as v
import test_computer_asset_export_v3_evidence_contract as old


# A closed source shape, not a general TypeScript parser. Keep the actual outer
# catch/finally, independent cleanup branches and failure propagation together.
# Merely mentioning restoreOwnedExportWorker (including in a comment) is not a
# cleanup witness. Browser/Worker runtime evidence remains independently needed.
QUEUED_CANCEL_CLEANUP = '''  } catch { failure = phase; }
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
  if (failure || cleanupFailed) throw new Error(`Computer export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}`);
});
'''
QUEUED_CANCEL_ARM = '''    phase = "pause"; restoreWorker = true;
    const pause = await pauseOwnedExportWorker();'''
QUEUED_CANCEL_IMAGE = 'const image = await screenshot(taskPage, info, "cancelled-terminal.png");'

QUEUED_RECEIPT_OBSERVER = '''// Two original GET witnesses, one explicit old-panel action. Both observers
// use fresh Request identity; nested allSettled branches join before returning.
async function observedReceiptAndDetail(page: Page, actor: number, taskID: string, domainId: string, idempotencyKey: string) {
  const button = panelFor(page).getByRole("button", { name: "核对原导出回执", exact: true });
  await expect(button).toBeEnabled();
  const receiptResponses: Response[] = [];
  const detailResponse = await observedAction(page,
    (r) => new URL(r.url()).pathname === `${exportPrefix}/detail` && new URL(r.url()).searchParams.get("taskID") === taskID && r.method() === "GET",
    async () => {
      receiptResponses.push(await observedAction(page,
        (r) => new URL(r.url()).pathname === `${exportPrefix}/receipt` && new URL(r.url()).searchParams.get("domainId") === domainId && new URL(r.url()).searchParams.get("idempotencyKey") === idempotencyKey && r.method() === "GET",
        () => button.click()));
    });
  const receiptResponse = receiptResponses[0];
  if (receiptResponses.length !== 1 || !receiptResponse) throw new Error("Original computer export receipt checkpoint failed");
  const receipt = await responseValue(receiptResponse, actor), detail = await responseValue(detailResponse, actor);
  expect(receipt.taskID).toBe(taskID);
  expect(detail.taskID).toBe(taskID);
  return { receipt, detail };
}
'''


# The diagnostic-only delta must reverse to the reviewed spec bytes. It does
# not change any Worker/browser helper, timeout, request or assertion semantics.
QUEUED_CANCEL_STAGE_BASELINE_SHA256 = "8f1acd105d4e35ff052efd14be88f8a4c5ea554f7c29e9fb6fd6050864473149"
QUEUED_CANCEL_PHASE_TYPE = '''type QueuedCancelPhase =
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
'''
QUEUED_CANCEL_PHASE_INIT = '''  let phase: QueuedCancelPhase = "setup", failure: QueuedCancelPhase | "" = "", cleanupFailed = false, restoreWorker = false;'''
QUEUED_CANCEL_ORIGINAL_INIT = '''  let phase = "setup", failure = "", cleanupFailed = false, restoreWorker = false;'''
QUEUED_CANCEL_POST_RESUME_STAGES = '''    phase = "resume-owned-worker";
    const resume = await resumeOwnedExportWorker(context, pause);
    phase = "post-resume-receipt-detail";
    const postResumeReads = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);
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
    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });
'''
QUEUED_CANCEL_ORIGINAL_POST_RESUME = '''    phase = "resume-owned-worker";
    const resume = await resumeOwnedExportWorker(context, pause);
    const postResume = { ...await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey), database: await terminalExport(originalTaskID), unavailableDownload: await unavailableDownload(context, originalTaskID) };
    expect(postResume.database).toEqual(database);
    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });
    expect(downloads).toBe(0);
    // Keep the old asset-panel DOM and visibility checks after both queue cycles.
    const cancelledStatus = panel.getByRole("status").filter({ hasText: "服务器已确认取消，没有可下载文件" });
    await cancelledStatus.scrollIntoViewIfNeeded();
    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });
    await expect(panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })).toBeInViewport({ ratio: 1 });
    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });
'''


# Each old-panel target has its own positioning precondition. This outer delta
# restores the exact reviewed diagnostic-only source before the unchanged inner
# inverse restores its own historical baseline. It is not a layout root-cause
# assertion and does not replace the actual browser visibility checks.
QUEUED_CANCEL_SCROLL_BASELINE_SHA256 = "f212d776058889294ace90bea5f2774cd0eceba45b2f6d257eaaaba1e0e2f80b"
QUEUED_CANCEL_TASK_ID_SCROLL_ENUM = '  | "old-panel-task-id-scroll"\n'
QUEUED_CANCEL_BUTTON_SCROLL_ENUM = '  | "old-panel-create-button-scroll"\n'
QUEUED_CANCEL_TASK_ID_SCROLL = '''    phase = "old-panel-task-id-scroll";
    await panel.getByText(`任务 ID：${originalTaskID}`, { exact: true }).scrollIntoViewIfNeeded();
'''
QUEUED_CANCEL_BUTTON_SCROLL = '''    phase = "old-panel-create-button-scroll";
    await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).scrollIntoViewIfNeeded();
'''
QUEUED_CANCEL_CURRENT_PHASE_TYPE = QUEUED_CANCEL_PHASE_TYPE.replace(
    '  | "old-panel-task-id-viewport"\n',
    QUEUED_CANCEL_TASK_ID_SCROLL_ENUM + '  | "old-panel-task-id-viewport"\n', 1,
).replace(
    '  | "old-panel-create-button-viewport"\n',
    QUEUED_CANCEL_BUTTON_SCROLL_ENUM + '  | "old-panel-create-button-viewport"\n', 1,
)
QUEUED_CANCEL_CURRENT_POST_RESUME_STAGES = QUEUED_CANCEL_POST_RESUME_STAGES.replace(
    '    phase = "old-panel-task-id-viewport";\n',
    QUEUED_CANCEL_TASK_ID_SCROLL + '    phase = "old-panel-task-id-viewport";\n', 1,
).replace(
    '    phase = "old-panel-create-button-viewport";\n',
    QUEUED_CANCEL_BUTTON_SCROLL + '    phase = "old-panel-create-button-viewport";\n', 1,
)


# This host-only diagnostic adds no browser work. Its outer inverse restores
# the exact reviewed c023 source before the unchanged scroll/stage chain.
QUEUED_CANCEL_RATIO_BASELINE_SHA256 = "c0235e779d4e174cbc4519b19c8a05221d46235a2b0aee7f454e0d9eacadec4a"
QUEUED_CANCEL_RATIO_IMPORT = 'import { classifyComputerViewportResult, computerViewportResultSuffix, type ComputerViewportRatioClass } from "./computer-asset-export-v3-viewport-result";\n'
QUEUED_CANCEL_RATIO_INIT = '  let viewportRatioClass: ComputerViewportRatioClass | "" = "";\n'
QUEUED_CANCEL_RATIO_OLD_CATCH = '  } catch { failure = phase; }'
QUEUED_CANCEL_RATIO_CATCH = '  } catch (error) {\n    failure = phase;\n    if (phase === "old-panel-create-button-viewport") {\n      try { viewportRatioClass = classifyComputerViewportResult(error); }\n      catch { viewportRatioClass = "unavailable"; }\n    }\n  }'
QUEUED_CANCEL_RATIO_OLD_FAILURE = '  if (failure || cleanupFailed) throw new Error(`Computer export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}`);\n'
QUEUED_CANCEL_RATIO_FAILURE = '  if (failure || cleanupFailed) throw new Error(`Computer export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}${computerViewportResultSuffix(viewportRatioClass)}`);\n'
QUEUED_CANCEL_RATIO_FIRST_IMPORT = 'import { test, expect, type Page, type Request as BrowserRequest, type Response } from "@playwright/test";\n'

QUEUED_CANCEL_RATIO_CLEANUP = QUEUED_CANCEL_CLEANUP.replace(
    QUEUED_CANCEL_RATIO_OLD_CATCH, QUEUED_CANCEL_RATIO_CATCH, 1,
).replace(QUEUED_CANCEL_RATIO_OLD_FAILURE, QUEUED_CANCEL_RATIO_FAILURE, 1)


# The one pre-assert sample restores the exact main1ec6 host-only source.
# Keep every historical classifier/positioning/stage hash below unchanged.
QUEUED_CANCEL_GEOMETRY_BASELINE_SHA256 = "0662e56d861111fc2161c2d9b59d9fd01914e4688ca94ced1e7ad54491138258"
QUEUED_CANCEL_GEOMETRY_IMPORT = 'import { captureComputerButtonGeometry, projectComputerButtonGeometry, computerButtonGeometrySuffix, type ComputerButtonGeometry, classifyComputerViewportResult, computerViewportResultSuffix, type ComputerViewportRatioClass } from "./computer-asset-export-v3-viewport-result";\n'
QUEUED_CANCEL_GEOMETRY_INIT = '  let buttonGeometry: ComputerButtonGeometry | null = null;\n'
QUEUED_CANCEL_GEOMETRY_ENUM = '  | "old-panel-create-button-geometry"\n'
QUEUED_CANCEL_GEOMETRY_SAMPLE = '    phase = "old-panel-create-button-geometry";\n    try {\n      buttonGeometry = projectComputerButtonGeometry(await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).evaluate(captureComputerButtonGeometry));\n    } catch { buttonGeometry = { status: "unavailable" }; }\n'
QUEUED_CANCEL_GEOMETRY_FAILURE = '  if (failure || cleanupFailed) throw new Error(`Computer export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}${computerViewportResultSuffix(viewportRatioClass)}${failure === "old-panel-create-button-viewport" ? computerButtonGeometrySuffix(buttonGeometry) : ""}`);\n'

QUEUED_CANCEL_GEOMETRY_PHASE_TYPE = QUEUED_CANCEL_CURRENT_PHASE_TYPE.replace(
    '  | "old-panel-create-button-viewport"\n',
    QUEUED_CANCEL_GEOMETRY_ENUM + '  | "old-panel-create-button-viewport"\n', 1,
)
QUEUED_CANCEL_GEOMETRY_POST_RESUME = QUEUED_CANCEL_CURRENT_POST_RESUME_STAGES.replace(
    '    phase = "old-panel-create-button-viewport";\n',
    QUEUED_CANCEL_GEOMETRY_SAMPLE + '    phase = "old-panel-create-button-viewport";\n', 1,
)
QUEUED_CANCEL_GEOMETRY_CLEANUP = QUEUED_CANCEL_RATIO_CLEANUP.replace(
    QUEUED_CANCEL_RATIO_FAILURE, QUEUED_CANCEL_GEOMETRY_FAILURE, 1,
)


# Explicit native centering is a separate positioning action, never a mutation
# hidden inside the read-only geometry callback. Preserve every historical hash.
QUEUED_CANCEL_CENTER_BASELINE_SHA256 = "3050f592ea8f988aed99e2de4e15e47eb00422503fb3f5d8c22695a352c9d46e"
QUEUED_CANCEL_CENTER_ENUM = '  | "old-panel-create-button-center"\n'
QUEUED_CANCEL_CENTER_LINE = '    await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).evaluate(element => element.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" }));\n'
QUEUED_CANCEL_CENTER_BLOCK = '    phase = "old-panel-create-button-center";\n' + QUEUED_CANCEL_CENTER_LINE
QUEUED_CANCEL_CENTER_PHASE_TYPE = QUEUED_CANCEL_GEOMETRY_PHASE_TYPE.replace(
    QUEUED_CANCEL_GEOMETRY_ENUM, QUEUED_CANCEL_CENTER_ENUM + QUEUED_CANCEL_GEOMETRY_ENUM, 1,
)
QUEUED_CANCEL_CENTER_POST_RESUME = QUEUED_CANCEL_GEOMETRY_POST_RESUME.replace(
    QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_CENTER_BLOCK + QUEUED_CANCEL_GEOMETRY_SAMPLE, 1,
)


QUEUED_CANCEL_GEOMETRY_CALLBACK = 'export function captureComputerButtonGeometry(element: Element): ComputerButtonGeometry {\n  try {\n    const document = element.ownerDocument;\n    const view = document?.defaultView, root = document?.documentElement;\n    if (!view || !root) return { status: "unavailable" };\n    const rect = element.getBoundingClientRect();\n    const connected = element.isConnected;\n    const left = rect.left, top = rect.top, right = rect.right, bottom = rect.bottom;\n    const width = rect.width, height = rect.height;\n    const innerWidth = view.innerWidth, innerHeight = view.innerHeight;\n    const clientWidth = root.clientWidth, clientHeight = root.clientHeight;\n    if (typeof connected !== "boolean" ||\n        !Number.isFinite(left) || !Number.isFinite(top) || !Number.isFinite(right) || !Number.isFinite(bottom) ||\n        !Number.isFinite(width) || !Number.isFinite(height) ||\n        Math.abs(left) > 1_000_000 || Math.abs(top) > 1_000_000 || Math.abs(right) > 1_000_000 || Math.abs(bottom) > 1_000_000 ||\n        width < 0 || width > 1_000_000 || height < 0 || height > 1_000_000 || right < left || bottom < top ||\n        !Number.isInteger(innerWidth) || innerWidth < 1 || innerWidth > 16_384 ||\n        !Number.isInteger(innerHeight) || innerHeight < 1 || innerHeight > 16_384 ||\n        !Number.isInteger(clientWidth) || clientWidth < 1 || clientWidth > 16_384 ||\n        !Number.isInteger(clientHeight) || clientHeight < 1 || clientHeight > 16_384) return { status: "unavailable" };\n    return { status: "sampled", connected, left, top, right, bottom, width, height, innerWidth, innerHeight, clientWidth, clientHeight };\n  } catch {\n    return { status: "unavailable" };\n  }\n}\n\n'


def queued_cancel_geometry_helper_contract(source):
    v.require(type(source) is str, 'geometry helper source text')
    encoded = source.encode('utf-8')
    v.require(hashlib.sha256(encoded[:2774]).hexdigest() == 'db9d3121d410c1c1c6c420bfec22c4cff88709cf5c7775e38c2a9b1f0a77135b', 'original matcher helper prefix unchanged')
    v.require(source.count(QUEUED_CANCEL_GEOMETRY_CALLBACK) == 1, 'one exact self-contained synchronous callback')
    v.require(source.count('export function captureComputerButtonGeometry(') == 1, 'no duplicate browser callback export')


def restore_queued_cancel_center(source):
    v.require(type(source) is str, 'queued center source text')
    # Count-and-delete alone would admit an operation moved after its assertion.
    # Pin both current shapes, then pin every remaining raw UTF-8 byte.
    v.require(source.count(QUEUED_CANCEL_CENTER_PHASE_TYPE) == 1, 'complete current 25-phase vocabulary')
    v.require(source.count(QUEUED_CANCEL_CENTER_POST_RESUME) == 1, 'original scroll then awaited native center then geometry then ratio1')
    for addition in (QUEUED_CANCEL_CENTER_ENUM, QUEUED_CANCEL_CENTER_BLOCK):
        v.require(source.count(addition) == 1, 'one exact center positioning delta')
        source = source.replace(addition, '', 1)
    encoded = source.encode('utf-8')
    v.require(len(encoded) == 22859 and hashlib.sha256(encoded).hexdigest() == QUEUED_CANCEL_CENTER_BASELINE_SHA256,
              'center inverse preserves original geometry, requests, ratio and cleanup bytes')
    return source


def restore_queued_cancel_geometry(source):
    source = restore_queued_cancel_center(source)
    v.require(type(source) is str, 'queued geometry source text')
    v.require(source.startswith(QUEUED_CANCEL_GEOMETRY_IMPORT + QUEUED_CANCEL_RATIO_FIRST_IMPORT), 'expanded helper import in exact original position')
    v.require(source.count(QUEUED_CANCEL_RATIO_INIT + QUEUED_CANCEL_GEOMETRY_INIT) == 1, 'empty geometry state beside original matcher state')
    v.require(source.count(QUEUED_CANCEL_GEOMETRY_PHASE_TYPE) == 1, 'complete current 24-phase vocabulary')
    v.require(source.count(QUEUED_CANCEL_GEOMETRY_POST_RESUME) == 1, 'exact single pre-assert sample and original assertion order')
    v.require(source.count(QUEUED_CANCEL_GEOMETRY_CLEANUP) == 1 and source.endswith(QUEUED_CANCEL_GEOMETRY_CLEANUP), 'unchanged cleanup before target-gated geometry suffix')
    for current, previous in (
        (QUEUED_CANCEL_GEOMETRY_IMPORT, QUEUED_CANCEL_RATIO_IMPORT),
        (QUEUED_CANCEL_GEOMETRY_INIT, ''),
        (QUEUED_CANCEL_GEOMETRY_ENUM, ''),
        (QUEUED_CANCEL_GEOMETRY_SAMPLE, ''),
        (QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_RATIO_FAILURE),
    ):
        v.require(source.count(current) == 1, 'one exact pre-assert geometry delta')
        source = source.replace(current, previous, 1)
    v.require(hashlib.sha256(source.encode('utf-8')).hexdigest() == QUEUED_CANCEL_GEOMETRY_BASELINE_SHA256,
              'geometry inverse preserves original requests, ratio, catch and cleanup bytes')
    return source


def restore_queued_cancel_viewport_result(source):
    source = restore_queued_cancel_geometry(source)
    v.require(type(source) is str, 'queued host viewport source text')
    v.require(source.startswith(QUEUED_CANCEL_RATIO_IMPORT + QUEUED_CANCEL_RATIO_FIRST_IMPORT), 'host import in exact original position')
    v.require(source.count(QUEUED_CANCEL_PHASE_INIT + '\n' + QUEUED_CANCEL_RATIO_INIT) == 1, 'empty host result beside original phase initialization')
    v.require(source.count(QUEUED_CANCEL_CURRENT_PHASE_TYPE) == 1, 'unchanged closed phase vocabulary')
    v.require(source.count(QUEUED_CANCEL_CURRENT_POST_RESUME_STAGES) == 1, 'unchanged exact phase-to-assertion binding')
    v.require(source.count(QUEUED_CANCEL_RATIO_CLEANUP) == 1 and source.endswith(QUEUED_CANCEL_RATIO_CLEANUP), 'host capture and safe suffix enclose original cleanup')
    for current, previous in (
        (QUEUED_CANCEL_RATIO_IMPORT, ''),
        (QUEUED_CANCEL_RATIO_INIT, ''),
        (QUEUED_CANCEL_RATIO_CATCH, QUEUED_CANCEL_RATIO_OLD_CATCH),
        (QUEUED_CANCEL_RATIO_FAILURE, QUEUED_CANCEL_RATIO_OLD_FAILURE),
    ):
        v.require(source.count(current) == 1, 'one exact synchronous host-result delta')
        source = source.replace(current, previous, 1)
    v.require(hashlib.sha256(source.encode('utf-8')).hexdigest() == QUEUED_CANCEL_RATIO_BASELINE_SHA256,
              'host inverse preserves exact original browser and cleanup bytes')
    return source


def restore_queued_cancel_scrolls(source):
    source = restore_queued_cancel_viewport_result(source)
    v.require(type(source) is str, 'queued individual-target source text')
    # Deletion alone cannot distinguish a scroll moved after its assertion.
    # Pin each complete current block before removing the four additions.
    v.require(source.count(QUEUED_CANCEL_CURRENT_PHASE_TYPE) == 1, 'one exact current phase vocabulary')
    v.require(source.count(QUEUED_CANCEL_CURRENT_POST_RESUME_STAGES) == 1, 'one exact ordered current positioning block')
    for addition in (
        QUEUED_CANCEL_TASK_ID_SCROLL_ENUM, QUEUED_CANCEL_BUTTON_SCROLL_ENUM,
        QUEUED_CANCEL_TASK_ID_SCROLL, QUEUED_CANCEL_BUTTON_SCROLL,
    ):
        v.require(source.count(addition) == 1, 'one exact individual-target positioning delta')
        source = source.replace(addition, '', 1)
    v.require(hashlib.sha256(source.encode('utf-8')).hexdigest() == QUEUED_CANCEL_SCROLL_BASELINE_SHA256,
              'scroll inverse preserves exact original diagnostic source')
    return source


def restore_queued_cancel_stages(source):
    source = restore_queued_cancel_scrolls(source)
    v.require(type(source) is str, 'queued diagnostic source text')
    changes = (
        (QUEUED_CANCEL_PHASE_TYPE, ''),
        (QUEUED_CANCEL_PHASE_INIT, QUEUED_CANCEL_ORIGINAL_INIT),
        (QUEUED_CANCEL_POST_RESUME_STAGES, QUEUED_CANCEL_ORIGINAL_POST_RESUME),
    )
    for current, previous in changes:
        v.require(source.count(current) == 1, 'one exact closed diagnostic delta')
        source = source.replace(current, previous, 1)
    v.require(hashlib.sha256(source.encode('utf-8')).hexdigest() == QUEUED_CANCEL_STAGE_BASELINE_SHA256,
              'diagnostic inverse preserves exact original queued behavior')
    return source


def queued_cancel_browser_contract(source):
    restore_queued_cancel_stages(source)
    v.require(type(source) is str and source.endswith(QUEUED_CANCEL_GEOMETRY_CLEANUP), 'closed queued cleanup and safe failure propagation')
    v.require(source.count('  finally {\n') == 1, 'one outer queued cleanup')
    v.require(source.count('await taskPage.close();') == 1 and source.count('await restoreOwnedExportWorker();') == 1, 'await each owned cleanup exactly once')
    v.require(re.findall(r'\brestoreWorker\s*=\s*([^;,\n]+)', source) == ['false', 'true'], 'restore guard initialized once and never disarmed')
    v.require(source.count(QUEUED_CANCEL_ARM) == 1, 'arm restoration before attempting pause')
    outer_try = '  try {\n    expect(process.env.ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_TERMINAL)'
    v.require(source.count(outer_try) == 1 and source.index(outer_try) < source.index(QUEUED_CANCEL_ARM), 'pause attempt belongs to protected outer try')
    v.require(source.count('test.setTimeout(900_000);') == 1, 'unchanged queued timeout')
    v.require(source.count(QUEUED_RECEIPT_OBSERVER) == 1, 'paired fresh receipt and detail with one joined action')
    v.require('receiptAndDetail' not in source, 'no legacy pathname-only receipt observer')
    v.require(source.count('await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey)') == 2, 'both old-panel recoveries use original request witnesses')
    # Preserve post-resume DB invariance and old-panel visibility, then require
    # a real fresh dedicated read and proof-free original identity before the
    # new-tab screenshot. The existing artifact validator still binds its bytes.
    ordered = [
        'const resume = await resumeOwnedExportWorker(context, pause);',
        'expect(postResume.database).toEqual(database);',
        'await cancelledStatus.scrollIntoViewIfNeeded();',
        'phase = "new-entry-terminal-pixels";',
        'const fresh = await responseValue(freshResponse, setup.actorId);',
        'expect(fresh).toMatchObject({ taskID: originalTaskID, request, downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });',
        'await taskCancelledStatus.scrollIntoViewIfNeeded();',
        'await expect(taskCancelledStatus).toBeInViewport({ ratio: 1 });',
        'await expect(originalID).toHaveText(originalTaskID);',
        'await expect(originalID).toBeInViewport({ ratio: 1 });',
        'await expect(control).toBeVisible();',
        '''const screenshotProofInputs = 'input[type="password"], input[name="actorPassword"], input[name="totpCode"]';''',
        'await expect(control.locator(screenshotProofInputs)).toHaveCount(0);',
        'await expect(taskPage.locator(screenshotProofInputs)).toHaveCount(0);',
        QUEUED_CANCEL_IMAGE,
        'await writeTerminalEvidence(info, setup, "queuedCancel",',
    ]
    for value in ordered:
        v.require(source.count(value) == 1, 'unique required queued checkpoint')
    positions = [source.index(value) for value in ordered]
    v.require(positions == sorted(positions), 'resumed DB then real new-entry pixels then evidence')
    v.require(source.count('await screenshot(') == 1, 'one retained queued screenshot')
    v.require(source.count('toBeInViewport({ ratio: 1 })') >= 5, 'old and new visible terminal identity')
    # These are actual request/cleanup operations, not merely named helpers.
    for token in (
        'const seen = new Set<BrowserRequest>();',
        'if (!armed || !predicate(request)) return;',
        'seen.add(request);',
        'if (settled || !seen.has(response.request())) return;',
        'if (response.fromServiceWorker()) { fail(); return; }',
        'const results = await Promise.allSettled([completed, actionResult] as const);',
        'clearTimeout(timer); page.off("request", started); page.off("response", received); page.off("requestfailed", failed);',
        'fail(); await completed.catch(() => undefined);',
        'return original.apply(this, args);',
        'witness.matched = witness.matched && localStorage.getItem(key) === expected;',
        '.toEqual({ calls: 1, matched: true, observationErrors: 0 });',
        'expect(queued).toMatchObject({ state: "queued", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });',
        'expect(database).toMatchObject({ state: "cancelled", error: "cancelled", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });',
        'await expect(panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })).toBeInViewport({ ratio: 1 });',
        'await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });',
    ):
        v.require(source.count(token) == 1, 'retained original state or fresh bounded browser observation')


def terminal_db(task_id, key, state):
    actions = ['submitted', 'leased', 'started', 'progress', 'finished'] if state == 'failed' else ['submitted', 'cancel_requested'] if state == 'cancelled' else ['submitted']
    events = []
    for i, action in enumerate(actions):
        events.append({'action': action, 'state': state if i == len(actions)-1 else 'queued' if i == 0 else 'running', 'attempt': int(state == 'failed' and i >= 2), 'resultVersion': i})
    return {'taskID': task_id, 'actorId': '1', 'kind': 'computer_assets.v3.export', 'state': state, 'error': {'failed': 'export_invalid_text', 'cancelled': 'cancelled', 'queued': ''}[state], 'attempt': int(state == 'failed'), 'maxAttempts': 3, 'progress': 10 if state == 'failed' else 0, 'resultVersion': len(actions)-1, 'result': {}, 'idempotencyKey': key, 'leaseOwner': '', 'leaseUntil': None, 'snapshotCount': 0, 'manifestCount': 0, 'chunkCount': 0, 'events': events}


def response(task_id, request, state):
    return {'taskID': task_id, 'request': copy.deepcopy(request), 'task': {'taskUUID': task_id, 'taskName': 'computer_assets.v3.export', 'state': state, 'error': 'export_invalid_text' if state == 'failed' else 'cancelled' if state == 'cancelled' else ''}}


def specimen(failure=True):
    data, files = old.specimen()
    data['suite'] = 'failureRecovery' if failure else 'queuedCancel'
    data['worker'] = None
    data['sensitiveBudget']['after'].update(owner=9, exportTasks=2)
    if failure:
        source = data['source']
        body = json.loads(base64.b64decode(source['bodyBase64']))
        body['objects'][0]['base']['samAccountName'] = 'computer-invalid\ufffe'
        raw = json.dumps(body).encode()
        source.update(bodyBase64=old.b64(raw), sha256=v.sha(raw))
    success = data['cases'][0]
    success['name'], success['idempotencyKey'] = 'recovery', 'recovery-key'
    request = {**success['request'], 'screenColumns': v.SAFE_COLUMNS}
    success['request'] = request
    for key in ('create', 'receipt', 'detail'):
        success[key]['request'] = copy.deepcopy(request)
    rows = v.matched_rows(v.source_rows(data['source']), request)
    raw = old.xlsx([[v.COLUMNS[c] for c in v.SAFE_COLUMNS], *rows])
    name = success['download']['filename']
    files[name] = raw
    success['detail']['artifact'].update(byteCount=len(raw), sha256=v.sha(raw))
    success['download']['headers'].update({'content-length': str(len(raw)), 'x-adtr-artifact-sha256': v.sha(raw)})
    success['database'].update(sourceSHA256=data['source']['sha256'], byteCount=len(raw), sha256=v.sha(raw), chunks=[{'ordinal': 0, 'bodyBase64': old.b64(raw), 'sha256': v.sha(raw)}])
    png = files['default-ready.png']
    files['recovery-ready.png'] = png
    success['screenshot'] = {'filename': 'recovery-ready.png', 'sha256': v.sha(png)}
    state = 'failed' if failure else 'cancelled'
    request = {**request, 'screenColumns': v.ALL_COLUMNS if failure else v.SAFE_COLUMNS}
    task_id, key = 'terminal-task', 'original-key'
    detail = {**response(task_id, request, state), 'downloadReady': False, 'downloadStatus': state}
    database = terminal_db(task_id, key, state)
    no_download = {'status': 409, 'error': 'export_not_ready', 'cacheControl': 'no-store'}
    filename = f'{state}-terminal.png'
    files[filename] = png
    case = {'name': state, 'idempotencyKey': key, 'request': request, 'create': {**response(task_id, request, 'queued'), 'replayed': False}, 'receipt': response(task_id, request, state), 'detail': detail, 'database': database, 'unavailableDownload': no_download, 'ui': {'downloadButtons': 0, 'downloadEvents': 0}, 'screenshot': {'filename': filename, 'sha256': v.sha(png)}}
    if failure:
        case['ui']['error'] = 'export_invalid_text'
        case['afterRecovery'] = {'receipt': copy.deepcopy(case['receipt']), 'database': copy.deepcopy(database)}
        data['cases'] = [case, success]
    else:
        case['queued'] = terminal_db(task_id, key, 'queued')
        case['cancel'] = {'taskID': task_id, 'task': copy.deepcopy(case['receipt']['task'])}
        case['postResume'] = {k: copy.deepcopy(case[k]) for k in ('receipt', 'detail', 'database', 'unavailableDownload')}
        data['cases'] = [case]
        data['sensitiveBudget']['after']['exportTasks'] = 1
        data['worker'] = {'pause': {'pid': 20, 'parentPID': 10, 'starttime': '1000', 'binarySHA256': '1'*64, 'nonceSHA256': '2'*64, 'applicationNameSHA256': '3'*64, 'allThreadsStopped': True, 'databaseIdentityVerified': True, 'threadCount': 6, 'connections': 1, 'activeTransactions': 0, 'workerID': 'worker-1', 'queueSuccessBefore': '1234.500000'}, 'resume': {'sameIdentity': True, 'allThreadsRunning': True, 'readyStatus': 200, 'workerID': 'worker-1', 'queueSuccessAfterFirst': '1234.750000', 'queueSuccessAfter': '1235.000000'}}
    return data, files


class TerminalEvidenceContract(unittest.TestCase):
    def check(self, data, files):
        v.validate(data, files.__getitem__, {'fixtureOnly': True})

    def test_both_positive_unit_specimens(self):
        for failure in (True, False):
            self.check(*specimen(failure))

    def test_verified_zero_connection_quiescence(self):
        data, files = specimen(False)
        data['worker']['pause']['connections'] = 0
        self.check(data, files)
        for changes in ({'activeTransactions': 1}, {'databaseIdentityVerified': False}, {'allThreadsStopped': False}):
            with self.subTest(changes=changes):
                changed = copy.deepcopy(data)
                changed['worker']['pause'].update(changes)
                with self.assertRaises(ValueError):
                    self.check(changed, files)

    def test_shared_corruption_matrix(self):
        mutations = [
            lambda d: d.update(format=True),
            lambda d: d['provenance'].update(wrong=True),
            lambda d: d.update(actorId=2),
            lambda d: d['source'].update(sha256='0'*64),
            lambda d: d['sensitiveBudget']['after'].update(owner=8),
            lambda d: d['sensitiveBudget']['after'].update(producer=10),
            lambda d: d['sensitiveBudget']['after'].update(observations=2),
            lambda d: d['sensitiveBudget']['before'].update(schemaVersion=22),
            lambda d: d['sensitiveBudget']['after'].pop('schemaVersion'),
            lambda d: d['cases'][0]['database'].update(snapshotCount=1),
            lambda d: d['cases'][0]['database'].update(manifestCount=1),
            lambda d: d['cases'][0]['database'].update(chunkCount=1),
            lambda d: d['cases'][0]['database'].update(chunkCount=False),
            lambda d: d['cases'][0]['database'].update(leaseOwner='still-leased'),
            lambda d: d['cases'][0]['database'].update(result={'artifactToken': 1}),
            lambda d: d['cases'][0]['database'].update(actorId='2'),
            lambda d: d['cases'][0]['database'].update(idempotencyKey='wrong-key'),
            lambda d: d['cases'][0]['detail'].update(downloadReady=True),
            lambda d: d['cases'][0]['detail'].update(artifact={'filename': 'partial.xlsx'}),
            lambda d: d['cases'][0]['receipt'].update(taskID='other'),
            lambda d: d['cases'][0]['ui'].update(downloadEvents=1),
            lambda d: d['cases'][0]['ui'].update(downloadButtons=False),
            lambda d: d['cases'][0]['unavailableDownload'].update(status=200),
            lambda d: d['cases'][0]['screenshot'].update(filename='../outside.png'),
            lambda d: d['cases'][0]['create'].update(password='secret-is-forbidden'),
        ]
        for failure in (True, False):
            for index, mutate in enumerate(mutations):
                with self.subTest(failure=failure, mutation=index):
                    data, files = specimen(failure)
                    mutate(data)
                    with self.assertRaises((ValueError, KeyError)):
                        self.check(data, files)

    def test_failure_recovery_corruption(self):
        mutations = [
            lambda d: d['cases'][0]['database'].update(attempt=0),
            lambda d: d['cases'][0]['database'].update(error='database_unavailable'),
            lambda d: d['cases'][0]['database']['events'].pop(1),
            lambda d: d['cases'][0]['afterRecovery']['database'].update(chunkCount=1),
            lambda d: d['cases'][0]['afterRecovery']['receipt']['task'].update(state='succeeded'),
            lambda d: d['cases'][1].update(idempotencyKey='original-key'),
            lambda d: d['cases'][1]['request'].update(screenColumns=list(reversed(v.SAFE_COLUMNS))),
            lambda d: d['cases'][1]['detail']['artifact'].update(rowCount=10),
            lambda d: d['cases'][1]['database'].update(sourceSHA256='0'*64),
            lambda d: d['cases'][1]['download']['headers'].update({'x-adtr-user-id': '2'}),
            lambda d: d['cases'][1]['database']['chunks'][0].update(sha256='0'*64),
        ]
        for i, mutate in enumerate(mutations):
            with self.subTest(mutation=i):
                data, files = specimen()
                mutate(data)
                with self.assertRaises((ValueError, KeyError)):
                    self.check(data, files)

    def test_poison_must_be_in_observed_ldap_source(self):
        data, files = specimen()
        source = data['source']
        body = json.loads(base64.b64decode(source['bodyBase64']))
        body['objects'][0]['base']['samAccountName'] = 'computer-00'
        raw = json.dumps(body).encode()
        source.update(bodyBase64=old.b64(raw), sha256=v.sha(raw))
        with self.assertRaisesRegex(ValueError, 'poisoned LDAP'):
            self.check(data, files)

    def test_unselected_column_or_page_only_workbook_rejected_even_with_matching_manifest(self):
        for rows in ([['SAM', 'GUID', 'mail'], ['user-00', 'guid', 'leak']], [['SAM', 'GUID']]):
            data, files = specimen()
            case = data['cases'][1]
            raw = old.xlsx(rows)
            files[case['download']['filename']] = raw
            case['detail']['artifact'].update(byteCount=len(raw), sha256=v.sha(raw))
            case['download']['headers'].update({'content-length': str(len(raw)), 'x-adtr-artifact-sha256': v.sha(raw)})
            with self.assertRaisesRegex(ValueError, 'full workbook'):
                self.check(data, files)

    def test_queued_cancel_corruption(self):
        mutations = [
            lambda d: d['cases'][0]['queued'].update(attempt=1),
            lambda d: d['cases'][0]['queued']['events'].append({'action': 'leased', 'state': 'queued', 'attempt': 0, 'resultVersion': 0}),
            lambda d: d['cases'][0]['cancel']['task'].update(state='cancel_requested'),
            lambda d: d['cases'][0]['postResume']['database'].update(attempt=1),
            lambda d: d['cases'][0]['postResume']['detail'].update(downloadStatus='pending'),
            lambda d: d['worker']['pause'].update(allThreadsStopped=False),
            lambda d: d['worker']['pause'].update(activeTransactions=1),
            lambda d: d['worker']['pause'].update(connections=-1),
            lambda d: d['worker']['pause'].update(connections=False),
            lambda d: d['worker']['pause'].update(databaseIdentityVerified=False),
            lambda d: d['worker']['pause'].pop('databaseIdentityVerified'),
            lambda d: d['worker']['resume'].update(sameIdentity=False),
            lambda d: d['worker']['resume'].update(workerID='different-worker'),
            lambda d: d['worker']['resume'].update(queueSuccessAfter='1234.500000'),
            lambda d: d['worker']['resume'].update(queueSuccessAfter='1234.499999'),
            lambda d: d['worker']['resume'].pop('queueSuccessAfter'),
            lambda d: d['worker']['resume'].update(queueSuccessAfterFirst='1234.500000'),
            lambda d: d['worker']['resume'].update(queueSuccessAfterFirst='1235.000000'),
        ]
        for i, mutate in enumerate(mutations):
            with self.subTest(mutation=i):
                data, files = specimen(False)
                mutate(data)
                with self.assertRaises((ValueError, KeyError)):
                    self.check(data, files)

    def test_expected_suite_and_png_binding(self):
        data, files = specimen()
        with self.assertRaises(ValueError):
            v.validate(data, files.__getitem__, {'fixtureOnly': True}, 'computer-asset-export-v3-cancel')
        files['failed-terminal.png'] += b'tamper'
        with self.assertRaises(ValueError):
            self.check(data, files)

    def test_browser_source_contract(self):
        root = Path(__file__).resolve().parents[1]
        helper = (root / 'web/e2e/computer-asset-export-v3-terminal-helpers.ts').read_text()
        self.assertIn('lastIndexOf(")")', helper)
        self.assertIn('xact_start IS NOT NULL', helper)
        self.assertIn('ADTR_E2E_EXPORT_WORKER_NONCE=', helper)
        self.assertIn('queueSuccessBefore', helper)
        self.assertIn('databaseEntries.length !== 1', helper)
        self.assertIn('database.searchParams.getAll("application_name").length !== 1', helper)
        self.assertIn('baseline.searchParams.has("application_name")', helper)
        self.assertIn('baseline.pathname !== "/adtr_e2e"', helper)
        self.assertIn('database.toString() !== baseline.toString()', helper)
        self.assertIn('datname=current_database() AND application_name=', helper)
        self.assertIn('w.databaseIdentityVerified && pg.activeTransactions === 0', helper)
        self.assertIn('Number.isSafeInteger(pg.connections) || pg.connections < 0', helper)
        self.assertNotIn('pg.connections > 0', helper)
        self.assertIn('Date.now() + 10_000', helper)
        for name in ('failure', 'cancel'):
            source = (root / f'web/e2e/computer-asset-export-v3-{name}.spec.ts').read_text()
            self.assertNotIn('route.fulfill', source)
            self.assertNotIn('waitForTimeout', source)
            self.assertIn('test.setTimeout(900_000)', source)
        failure = (root / 'web/e2e/computer-asset-export-v3-failure.spec.ts').read_text()
        self.assertIn('terminalError.scrollIntoViewIfNeeded()', failure)
        self.assertGreaterEqual(failure.count('toBeInViewport({ ratio: 1 })'), 4)
        cancel = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(cancel)
        self.assertIn('cancelledStatus.scrollIntoViewIfNeeded()', cancel)
        self.assertGreaterEqual(cancel.count('toBeInViewport({ ratio: 1 })'), 3)
        self.assertLess(cancel.index('expect(postResume.database).toEqual(database)'), cancel.index('await screenshot(taskPage, info,'))

    def test_queued_individual_scroll_inverse(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        diagnostic = restore_queued_cancel_scrolls(source)
        self.assertEqual(hashlib.sha256(diagnostic.encode('utf-8')).hexdigest(), QUEUED_CANCEL_SCROLL_BASELINE_SHA256)
        self.assertEqual(diagnostic.count(QUEUED_CANCEL_PHASE_TYPE), 1)
        self.assertEqual(diagnostic.count(QUEUED_CANCEL_POST_RESUME_STAGES), 1)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_PHASE_TYPE), 1)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_POST_RESUME), 1)
        phases = re.findall(r'^  \| "([a-z-]+)"', QUEUED_CANCEL_CENTER_PHASE_TYPE, re.MULTILINE)
        self.assertEqual(len(phases), 25)
        self.assertEqual(len(set(phases)), 25)
        historical = restore_queued_cancel_stages(source)
        self.assertEqual(hashlib.sha256(historical.encode('utf-8')).hexdigest(), QUEUED_CANCEL_STAGE_BASELINE_SHA256)
        # The four exact additions are the only source delta. Existing scrolls,
        # all three original ratio1 assertions and their order remain intact.
        ordered = (
            '    await cancelledStatus.scrollIntoViewIfNeeded();',
            '    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });',
            QUEUED_CANCEL_TASK_ID_SCROLL,
            '    await expect(panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })).toBeInViewport({ ratio: 1 });',
            QUEUED_CANCEL_BUTTON_SCROLL,
            '    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });',
            '    phase = "new-entry-terminal-pixels";',
        )
        for value in ordered:
            self.assertEqual(source.count(value), 1)
        positions = [source.index(value) for value in ordered]
        self.assertEqual(positions, sorted(positions))
        self.assertEqual(source.count('.scrollIntoViewIfNeeded()'), diagnostic.count('.scrollIntoViewIfNeeded()') + 2)
        self.assertEqual(source.count('toBeInViewport({ ratio: 1 })'), diagnostic.count('toBeInViewport({ ratio: 1 })'))
        queued_cancel_browser_contract(source)
        for invalid in (None, b'', 0, {}, []):
            with self.subTest(invalid_type=type(invalid).__name__):
                with self.assertRaises(ValueError):
                    restore_queued_cancel_scrolls(invalid)

    def test_queued_individual_scroll_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        task_locator = 'panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })'
        button_locator = 'panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })'
        targets = (
            ('old-panel-task-id', task_locator, QUEUED_CANCEL_TASK_ID_SCROLL_ENUM, QUEUED_CANCEL_TASK_ID_SCROLL),
            ('old-panel-create-button', button_locator, QUEUED_CANCEL_BUTTON_SCROLL_ENUM, QUEUED_CANCEL_BUTTON_SCROLL),
        )
        for target, locator, enum, block in targets:
            assignment = f'    phase = "{target}-scroll";\n'
            scroll = f'    await {locator}.scrollIntoViewIfNeeded();\n'
            assertion = f'    await expect({locator}).toBeInViewport({{ ratio: 1 }});\n'
            geometry_enum = (QUEUED_CANCEL_CENTER_ENUM + QUEUED_CANCEL_GEOMETRY_ENUM) if target == 'old-panel-create-button' else ''
            geometry_sample = (QUEUED_CANCEL_CENTER_BLOCK + QUEUED_CANCEL_GEOMETRY_SAMPLE) if target == 'old-panel-create-button' else ''
            viewport_enum = geometry_enum + f'  | "{target}-viewport"\n'
            viewport = geometry_sample + f'    phase = "{target}-viewport";\n' + assertion
            mutations = (
                ('missing enum', enum, ''),
                ('duplicate enum', enum, enum * 2),
                ('comment-only enum', enum, '  // ' + enum.strip() + '\n'),
                ('enum after viewport member', enum + viewport_enum, viewport_enum + enum),
                ('missing phase', assignment, ''),
                ('duplicate phase', assignment, assignment * 2),
                ('comment-only phase', assignment, '    // ' + assignment.strip() + '\n'),
                ('dynamic phase', assignment, f'    phase = "{target}:" + originalTaskID;\n'),
                ('wrong fixed phase', assignment, '    phase = "old-panel-status-scroll";\n'),
                ('missing scroll', scroll, ''),
                ('duplicate scroll', scroll, scroll * 2),
                ('comment-only scroll', scroll, '    // ' + scroll.strip() + '\n'),
                ('unawaited scroll', scroll, scroll.replace('await ', '', 1)),
                ('duplicate whole positioning block', block, block * 2),
                ('wrong page target', scroll, scroll.replace('panel.', 'taskPage.', 1)),
                ('inexact target', scroll, scroll.replace('exact: true', 'exact: false', 1)),
                ('first matching target', scroll, scroll.replace('.scrollIntoViewIfNeeded()', '.first().scrollIntoViewIfNeeded()', 1)),
                ('nth matching target', scroll, scroll.replace('.scrollIntoViewIfNeeded()', '.nth(0).scrollIntoViewIfNeeded()', 1)),
                ('phase after scroll', block, scroll + assignment),
                ('scroll after viewport assertion', block + viewport, viewport + block),
                ('removed original assertion', assertion, ''),
                ('weaker original ratio', assertion, assertion.replace('ratio: 1', 'ratio: 0.5', 1)),
                ('larger scroll timeout', scroll, scroll.replace('scrollIntoViewIfNeeded()', 'scrollIntoViewIfNeeded({ timeout: 120_000 })', 1)),
                ('click after positioning', scroll, scroll + f'    await {locator}.click();\n'),
                ('extra original receipt action', scroll, scroll + '    await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);\n'),
                ('extra proof action', scroll, scroll + '    await proof(taskPage);\n'),
            )
            for label, before, after in mutations:
                with self.subTest(target=target, mutation=label):
                    self.assertEqual(source.count(before), 1, 'mutation targets actual current positioning source')
                    with self.assertRaises(ValueError):
                        queued_cancel_browser_contract(source.replace(before, after, 1))
        task_viewport = '    phase = "old-panel-task-id-viewport";\n' + f'    await expect({task_locator}).toBeInViewport({{ ratio: 1 }});\n'
        button_viewport = QUEUED_CANCEL_CENTER_BLOCK + QUEUED_CANCEL_GEOMETRY_SAMPLE + '    phase = "old-panel-create-button-viewport";\n' + f'    await expect({button_locator}).toBeInViewport({{ ratio: 1 }});\n'
        pair = QUEUED_CANCEL_TASK_ID_SCROLL + task_viewport + QUEUED_CANCEL_BUTTON_SCROLL + button_viewport
        mutations = (
            ('reversed target order', pair, QUEUED_CANCEL_BUTTON_SCROLL + button_viewport + QUEUED_CANCEL_TASK_ID_SCROLL + task_viewport),
            ('parallel positioning', pair, f'    await Promise.all([{task_locator}.scrollIntoViewIfNeeded(), {button_locator}.scrollIntoViewIfNeeded()]);\n' + task_viewport + button_viewport),
            ('wrong original UUID', QUEUED_CANCEL_TASK_ID_SCROLL, QUEUED_CANCEL_TASK_ID_SCROLL.replace('${originalTaskID}', '${taskID}', 1)),
            ('wrong action name', QUEUED_CANCEL_BUTTON_SCROLL, QUEUED_CANCEL_BUTTON_SCROLL.replace('按当前列表创建另一导出', '创建导出', 1)),
            ('changed existing status scroll', '    await cancelledStatus.scrollIntoViewIfNeeded();', ''),
            ('weaker status ratio', '    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });', '    await expect(cancelledStatus).toBeInViewport({ ratio: 0.5 });'),
            ('larger viewport', QUEUED_CANCEL_TASK_ID_SCROLL, '    await page.setViewportSize({ width: 1440, height: 1800 });\n' + QUEUED_CANCEL_TASK_ID_SCROLL),
            ('larger suite timeout', 'test.setTimeout(900_000);', 'test.setTimeout(1_800_000);'),
            ('early new-tab focus', QUEUED_CANCEL_TASK_ID_SCROLL, '    await taskPage.bringToFront();\n' + QUEUED_CANCEL_TASK_ID_SCROLL),
            ('extra delay', QUEUED_CANCEL_TASK_ID_SCROLL, QUEUED_CANCEL_TASK_ID_SCROLL + '    await page.waitForTimeout(1000);\n'),
            ('raw failure cause', QUEUED_CANCEL_RATIO_CATCH, '  } catch (cause) { throw new Error("CANARY_PRIVATE", { cause }); }'),
            ('raw source logging', QUEUED_CANCEL_BUTTON_SCROLL, QUEUED_CANCEL_BUTTON_SCROLL + '    console.error("CANARY_PRIVATE", originalTaskID);\n'),
            ('cleanup failure swallowed', '      try { await restoreOwnedExportWorker(); } catch { cleanupFailed = true; }', '      try { await restoreOwnedExportWorker(); } catch {}'),
            ('unrelated source bytes', '// No replay, body interception, synthetic response or cache change.', '// Unrelated source changed.'),
        )
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation targets actual current source')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        # Whole-block moves still contain all required names exactly once.
        # They must be rejected by the pinned current shape plus exact inverse.
        for label, block in (
            ('moved entire current phase type', QUEUED_CANCEL_CENTER_PHASE_TYPE),
            ('moved entire current positioning block', QUEUED_CANCEL_CENTER_POST_RESUME),
        ):
            with self.subTest(mutation=label):
                self.assertEqual(source.count(block), 1, 'move targets one actual current block')
                changed = source.replace(block, '', 1) + block
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)

        for label, changed in (
            ('physical CRLF drift', source.replace('\n', '\r\n')),
            ('extra final newline', source + '\n'),
        ):
            with self.subTest(mutation=label):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)

    def test_queued_post_resume_diagnostic_inverse(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        restored = restore_queued_cancel_stages(source)
        self.assertEqual(hashlib.sha256(restored.encode('utf-8')).hexdigest(), QUEUED_CANCEL_STAGE_BASELINE_SHA256)
        self.assertEqual(restored.count(QUEUED_CANCEL_ORIGINAL_POST_RESUME), 1)
        self.assertEqual(restored.count(QUEUED_CANCEL_ORIGINAL_INIT), 1)
        self.assertNotIn(QUEUED_CANCEL_PHASE_TYPE, restored)
        queued_cancel_browser_contract(source)

    def test_queued_post_resume_diagnostic_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        phases = (
            'resume-owned-worker', 'post-resume-receipt-detail', 'post-resume-database',
            'post-resume-download-unavailable', 'post-resume-database-invariant',
            'post-resume-detail-invariant', 'post-resume-download-events',
            'old-panel-status-scroll', 'old-panel-status-viewport',
            'old-panel-task-id-viewport', 'old-panel-create-button-viewport',
        )
        for phase in phases:
            before = f'    phase = "{phase}";'
            self.assertEqual(source.count(before), 1, 'one actual diagnostic assignment')
            for label, after in (
                ('missing', ''), ('duplicate', before + '\n' + before),
                ('comment only', '    // ' + before.strip()),
                ('dynamic task data', f'    phase = "{phase}:" + originalTaskID;'),
            ):
                with self.subTest(phase=phase, mutation=label):
                    with self.assertRaises(ValueError):
                        queued_cancel_browser_contract(source.replace(before, after, 1))
        receipt = '    const postResumeReads = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);'
        database = '    const postResumeDatabase = await terminalExport(originalTaskID);'
        download = '    const postResumeUnavailableDownload = await unavailableDownload(context, originalTaskID);'
        combined = '    const postResume = { ...postResumeReads, database: postResumeDatabase, unavailableDownload: postResumeUnavailableDownload };'
        receipt_step = '    phase = "post-resume-receipt-detail";\n' + receipt
        database_step = '    phase = "post-resume-database";\n' + database
        download_step = '    phase = "post-resume-download-unavailable";\n' + download
        mutations = [
            ('untyped phase', QUEUED_CANCEL_PHASE_INIT, QUEUED_CANCEL_ORIGINAL_INIT),
            ('unknown phase vocabulary', '  | "evidence";', '  | "evidence" | "unknown";'),
            ('duplicate phase vocabulary', QUEUED_CANCEL_CENTER_PHASE_TYPE, QUEUED_CANCEL_CENTER_PHASE_TYPE * 2),
            ('missing phase vocabulary', QUEUED_CANCEL_CENTER_PHASE_TYPE, ''),
            ('duplicate diagnostic block', QUEUED_CANCEL_CENTER_POST_RESUME, QUEUED_CANCEL_CENTER_POST_RESUME * 2),
            ('stage after await', receipt_step, receipt + '\n    phase = "post-resume-receipt-detail";'),
            ('wrong fixed stage', '    phase = "post-resume-database";', '    phase = "post-resume-receipt-detail";'),
            ('DB before receipt', receipt_step + '\n' + database_step, database_step + '\n' + receipt_step),
            ('download before DB', database_step + '\n' + download_step, download_step + '\n' + database_step),
            ('unjoined receipt', receipt, receipt.replace('await ', '')),
            ('unjoined DB', database, database.replace('await ', '')),
            ('unjoined download', download, download.replace('await ', '')),
            ('extra receipt action', receipt, receipt + '\n    await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);'),
            ('extra DB read', database, database + '\n    await terminalExport(originalTaskID);'),
            ('extra download request', download, download + '\n    await unavailableDownload(context, originalTaskID);'),
            ('parallel reads', receipt, '    const postResumeReads = await Promise.all([observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey), terminalExport(originalTaskID)]);'),
            ('earlier receipt reused', receipt, '    const postResumeReads = recovered;'),
            ('earlier DB reused', database, '    const postResumeDatabase = database;'),
            ('earlier download reused', download, '    const postResumeUnavailableDownload = unavailable;'),
            ('wrong data binding', combined, combined.replace('database: postResumeDatabase', 'database')),
            ('changed schema key order', combined, '    const postResume = { database: postResumeDatabase, ...postResumeReads, unavailableDownload: postResumeUnavailableDownload };'),
            ('removed DB invariant', '    expect(postResume.database).toEqual(database);', ''),
            ('removed detail invariant', '    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });', ''),
            ('removed scroll', '    await cancelledStatus.scrollIntoViewIfNeeded();', ''),
            ('weaker status viewport', '    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });', '    await expect(cancelledStatus).toBeInViewport({ ratio: 0.5 });'),
            ('raw cause disclosure', QUEUED_CANCEL_RATIO_CATCH, '  } catch (cause) { throw new Error("CANARY_PRIVATE_ERROR", { cause }); }'),
            ('raw diagnostic logging', database, database + '\n    console.error("CANARY_PRIVATE_BODY", postResumeDatabase);'),
            ('unrelated source drift', '// No replay, body interception, synthetic response or cache change.', '// Unrelated source changed.'),
        ]
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation must target one actual checkpoint')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))

        # Decode the actual UTF-8 bytes without universal-newline translation.
        # Physical newline drift must not be normalized into the baseline hash.
        for label, changed in (
            ('physical CRLF drift', source.replace('\n', '\r\n')),
            ('extra final newline', source + '\n'),
        ):
            with self.subTest(mutation=label):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)

    def test_queued_browser_cleanup_and_pixel_contract_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(source)
        restore = '      try { await restoreOwnedExportWorker(); } catch { cleanupFailed = true; }'
        close = '      try { await taskPage.close(); } catch { cleanupFailed = true; }'
        proof_empty = 'await expect(taskPage.locator(screenshotProofInputs)).toHaveCount(0);'
        mutations = [
            ('legacy unbound receipt helper', 'const recovered = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);', 'const recovered = await receiptAndDetail(page, setup.actorId, originalTaskID);'),
            ('receipt action replayed', '() => button.click()));', 'async () => { await button.click(); await button.click(); }));'),
            ('receipt witness not joined', 'receiptResponses.push(await observedAction(page,', 'receiptResponses.push(observedAction(page,'),
            ('receipt domain unbound', 'new URL(r.url()).searchParams.get("domainId") === domainId && ', ''),
            ('receipt original key unbound', 'new URL(r.url()).searchParams.get("idempotencyKey") === idempotencyKey && ', ''),
            ('receipt response identity unchecked', 'expect(receipt.taskID).toBe(taskID);', ''),
            ('detail response identity unchecked', 'expect(detail.taskID).toBe(taskID);', ''),
            ('missing direct section proof absence', 'await expect(control.locator(screenshotProofInputs)).toHaveCount(0);', ''),
            ('new section may be absent', 'await expect(control).toBeVisible();', ''),
            ('old response accepted', 'if (settled || !seen.has(response.request())) return;', 'if (settled) return;'),
            ('request witness not armed', 'if (!armed || !predicate(request)) return;', 'if (!predicate(request)) return;'),
            ('service worker response accepted', 'if (response.fromServiceWorker()) { fail(); return; }', ''),
            ('observation not joined', 'const results = await Promise.allSettled([completed, actionResult] as const);', 'const results = await Promise.all([completed, actionResult] as const);'),
            ('native request arguments replaced', 'return original.apply(this, args);', 'return original.apply(this, []);'),
            ('marker not checked', 'witness.matched = witness.matched && localStorage.getItem(key) === expected;', 'witness.matched = true;'),
            ('queued zero artifacts unchecked', 'expect(queued).toMatchObject({ state: "queued", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });', ''),
            ('terminal zero artifacts unchecked', 'expect(database).toMatchObject({ state: "cancelled", error: "cancelled", attempt: 0, snapshotCount: 0, manifestCount: 0, chunkCount: 0 });', ''),
            ('old task identity no longer visible', 'await expect(panel.getByText(`任务 ID：${originalTaskID}`, { exact: true })).toBeInViewport({ ratio: 1 });', ''),
            ('old new-export action no longer visible', 'await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });', ''),
            ('missing restore', restore, ''),
            ('restore is only a comment', restore, '      try { /* await restoreOwnedExportWorker(); */ } catch { cleanupFailed = true; }'),
            ('unjoined restore', restore, restore.replace('await ', '')),
            ('restore failure swallowed', restore, '      try { await restoreOwnedExportWorker(); } catch {}'),
            ('restore depends on a tab', '    if (restoreWorker) {', '    if (taskPage && restoreWorker) {'),
            ('restore never armed', QUEUED_CANCEL_ARM, QUEUED_CANCEL_ARM.replace('= true', '= false')),
            ('pause before restoration armed', QUEUED_CANCEL_ARM, '    const pause = await pauseOwnedExportWorker();\n    phase = "pause"; restoreWorker = true;'),
            ('guard disarmed after pause', QUEUED_CANCEL_ARM, QUEUED_CANCEL_ARM + '\n    restoreWorker = false;'),
            ('missing tab close', close, ''),
            ('unjoined tab close', close, close.replace('await ', '')),
            ('close failure prevents restore', close, '      await taskPage.close();'),
            ('close failure swallowed', close, '      try { await taskPage.close(); } catch {}'),
            ('no finally protection', '  finally {\n', '  if (true) {\n'),
            ('cleanup failure treated as success', 'if (failure || cleanupFailed)', 'if (failure)'),
            ('outer failure swallowed', QUEUED_CANCEL_RATIO_CATCH, '  } catch {}'),
            ('old screenshot target', QUEUED_CANCEL_IMAGE, QUEUED_CANCEL_IMAGE.replace('screenshot(taskPage,', 'screenshot(page,')),
            ('wrong artifact name', QUEUED_CANCEL_IMAGE, QUEUED_CANCEL_IMAGE.replace('cancelled-terminal.png', 'other.png')),
            ('missing fresh terminal read', 'const fresh = await responseValue(freshResponse, setup.actorId);', 'const fresh = cancel;'),
            ('unbound screenshot identity', 'await expect(originalID).toHaveText(originalTaskID);', 'await expect(originalID).toBeVisible();'),
            ('missing proof destruction check', proof_empty, ''),
            ('larger timeout', 'test.setTimeout(900_000);', 'test.setTimeout(1_800_000);'),
        ]
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation must target one actual checkpoint')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        # Both moved calls still exist by name; order/outer-finally checks must
        # reject them rather than merely finding an expected function string.
        moved_restore = source.replace(restore, '', 1) + '\nawait restoreOwnedExportWorker();\n'
        db_check = 'expect(postResume.database).toEqual(database);'
        early_image = source.replace(QUEUED_CANCEL_IMAGE, '', 1).replace(db_check, QUEUED_CANCEL_IMAGE + '\n    ' + db_check, 1)
        late_db_check = source.replace(db_check, '', 1).replace(QUEUED_CANCEL_IMAGE, QUEUED_CANCEL_IMAGE + '\n    ' + db_check, 1)
        for label, changed in [('restore outside finally', moved_restore), ('pixels before DB verification', early_image), ('DB verification after pixels', late_db_check)]:
            with self.subTest(mutation=label):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)


    def test_queued_host_viewport_result_inverse(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        restored = restore_queued_cancel_viewport_result(source)
        self.assertEqual(len(restored.encode('utf-8')), 21760)
        self.assertEqual(hashlib.sha256(restored.encode('utf-8')).hexdigest(), QUEUED_CANCEL_RATIO_BASELINE_SHA256)
        self.assertEqual(hashlib.sha256(restore_queued_cancel_scrolls(source).encode('utf-8')).hexdigest(), QUEUED_CANCEL_SCROLL_BASELINE_SHA256)
        self.assertEqual(hashlib.sha256(restore_queued_cancel_stages(source).encode('utf-8')).hexdigest(), QUEUED_CANCEL_STAGE_BASELINE_SHA256)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_POST_RESUME), 1)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_PHASE_TYPE), 1)
        original_finally = QUEUED_CANCEL_CLEANUP.split('  finally {\n', 1)[1].split('  // Raw Playwright errors', 1)[0]
        actual_finally = source.split('  finally {\n', 1)[1].split('  // Raw Playwright errors', 1)[0]
        self.assertEqual(actual_finally, original_finally)
        self.assertEqual(source.count('computerViewportResultSuffix(viewportRatioClass)'), 1)
        self.assertLess(source.index('await restoreOwnedExportWorker();'), source.index('computerViewportResultSuffix(viewportRatioClass)'))
        self.assertNotIn('await', QUEUED_CANCEL_RATIO_CATCH)
        self.assertLess(QUEUED_CANCEL_RATIO_CATCH.index('failure = phase;'), QUEUED_CANCEL_RATIO_CATCH.index('classifyComputerViewportResult(error)'))
        queued_cancel_browser_contract(source)
        for invalid in (None, b'', 0, {}, []):
            with self.subTest(invalid_type=type(invalid).__name__):
                with self.assertRaises(ValueError):
                    restore_queued_cancel_viewport_result(invalid)

    def test_queued_host_viewport_result_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        guard = '    if (phase === "old-panel-create-button-viewport") {'
        capture = '      try { viewportRatioClass = classifyComputerViewportResult(error); }'
        fallback = '      catch { viewportRatioClass = "unavailable"; }'
        button = '    phase = "old-panel-create-button-viewport";\n'
        late_failure = QUEUED_CANCEL_RATIO_CATCH.replace('    failure = phase;\n', '', 1).replace('\n  }', '\n    failure = phase;\n  }', 1)
        mutations = (
            ('missing import', QUEUED_CANCEL_GEOMETRY_IMPORT, ''),
            ('duplicate import', QUEUED_CANCEL_GEOMETRY_IMPORT, QUEUED_CANCEL_GEOMETRY_IMPORT * 2),
            ('wrong helper import', QUEUED_CANCEL_GEOMETRY_IMPORT, QUEUED_CANCEL_GEOMETRY_IMPORT.replace('computer-asset-export-v3-viewport-result', 'user-asset-export-v3-viewport-result')),
            ('missing empty sentinel', QUEUED_CANCEL_RATIO_INIT, ''),
            ('duplicate empty sentinel', QUEUED_CANCEL_RATIO_INIT, QUEUED_CANCEL_RATIO_INIT * 2),
            ('pretend full ratio initially', QUEUED_CANCEL_RATIO_INIT, QUEUED_CANCEL_RATIO_INIT.replace('= "";', '= "exact_one";')),
            ('untyped diagnostic input', QUEUED_CANCEL_RATIO_INIT, '  let viewportRatioClass: any = "";\n'),
            ('original failure not recorded', QUEUED_CANCEL_RATIO_CATCH, QUEUED_CANCEL_RATIO_CATCH.replace('    failure = phase;\n', '')),
            ('failure recorded after projection', QUEUED_CANCEL_RATIO_CATCH, late_failure),
            ('unconditional capture', guard, '    if (true) {'),
            ('wrong target phase', guard, guard.replace('old-panel-create-button-viewport', 'old-panel-task-id-viewport')),
            ('broad phase capture', guard, '    if (phase.startsWith("old-panel")) {'),
            ('missing projection', capture, '      try { viewportRatioClass = "unavailable"; }'),
            ('different object projected', capture, capture.replace('(error)', '({})')),
            ('asynchronous projection', capture, capture.replace('= classify', '= await classify')),
            ('raw value projected', capture, capture.replace('classifyComputerViewportResult(error)', 'error.message')),
            ('fallback missing', fallback, ''),
            ('fallback swallowed', fallback, '      catch {}'),
            ('fallback raw cause', fallback, '      catch (cause) { throw new Error("CANARY", { cause }); }'),
            ('original error rethrown', capture, capture + '\n      throw error;'),
            ('new browser observation', capture, capture + '\n      await page.evaluate(() => document.body);'),
            ('new request', capture, capture + '\n      await context.request.get("/api/auth/me");'),
            ('new timer', capture, capture + '\n      await page.waitForTimeout(1000);'),
            ('new file write', capture, capture + '\n      writeFileSync("diagnostic.json", "CANARY");'),
            ('raw console error', capture, capture + '\n      console.error(error);'),
            ('intervening phase operation', button, button + '    await page.title();\n'),
            ('suffix before cleanup', capture, capture + '\n      computerViewportResultSuffix(viewportRatioClass);'),
            ('suffix removed', QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_RATIO_OLD_FAILURE),
            ('raw diagnostic interpolation', QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_GEOMETRY_FAILURE.replace('computerViewportResultSuffix(viewportRatioClass)', 'viewportRatioClass')),
            ('raw error interpolation', QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_GEOMETRY_FAILURE.replace('computerViewportResultSuffix(viewportRatioClass)', 'error.stack')),
            ('cleanup failure no longer fails', QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_GEOMETRY_FAILURE.replace('failure || cleanupFailed', 'failure')),
            ('diagnostic replaces original phase', QUEUED_CANCEL_GEOMETRY_FAILURE, QUEUED_CANCEL_GEOMETRY_FAILURE.replace('failure || "cleanup"', 'viewportRatioClass')),
            ('diagnostic treated as pass', QUEUED_CANCEL_GEOMETRY_FAILURE, '  if (viewportRatioClass) return;\n' + QUEUED_CANCEL_GEOMETRY_FAILURE),
            ('unrelated source drift', '// No replay, body interception, synthetic response or cache change.', '// Unrelated source changed.'),
        )
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation targets actual current host capture')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        # These removals/reinsertions preserve names/counts and would survive
        # deletion-only inverse logic; exact original positions must reject them.
        for label, block in (
            ('moved host import', QUEUED_CANCEL_GEOMETRY_IMPORT),
            ('moved diagnostic declaration', QUEUED_CANCEL_RATIO_INIT),
            ('moved complete capture', QUEUED_CANCEL_RATIO_CATCH),
            ('moved current cleanup suffix', QUEUED_CANCEL_GEOMETRY_CLEANUP),
        ):
            with self.subTest(mutation=label):
                self.assertEqual(source.count(block), 1, 'move targets one actual current block')
                remainder = source.replace(block, '', 1)
                changed = block + remainder if block == QUEUED_CANCEL_GEOMETRY_CLEANUP else remainder + block
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)
        for label, changed in (
            ('physical CRLF drift', source.replace('\n', '\r\n')),
            ('extra final newline', source + '\n'),
        ):
            with self.subTest(mutation=label):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)


    def test_queued_pre_assert_geometry_inverse(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        restored = restore_queued_cancel_geometry(source)
        self.assertEqual(len(restored.encode('utf-8')), 22233)
        self.assertEqual(hashlib.sha256(restored.encode('utf-8')).hexdigest(), QUEUED_CANCEL_GEOMETRY_BASELINE_SHA256)
        self.assertEqual(hashlib.sha256(restore_queued_cancel_viewport_result(source).encode('utf-8')).hexdigest(), QUEUED_CANCEL_RATIO_BASELINE_SHA256)
        self.assertEqual(hashlib.sha256(restore_queued_cancel_scrolls(source).encode('utf-8')).hexdigest(), QUEUED_CANCEL_SCROLL_BASELINE_SHA256)
        self.assertEqual(hashlib.sha256(restore_queued_cancel_stages(source).encode('utf-8')).hexdigest(), QUEUED_CANCEL_STAGE_BASELINE_SHA256)
        phases = re.findall(r'^  \| "([a-z-]+)"', QUEUED_CANCEL_CENTER_PHASE_TYPE, re.MULTILINE)
        self.assertEqual(len(phases), 25)
        self.assertEqual(len(set(phases)), 25)
        self.assertEqual(source.count(QUEUED_CANCEL_RATIO_CATCH), 1)
        expected_finally = QUEUED_CANCEL_RATIO_CLEANUP.split('  finally {\n', 1)[1].split('  // Raw Playwright errors', 1)[0]
        actual_finally = source.split('  finally {\n', 1)[1].split('  // Raw Playwright errors', 1)[0]
        self.assertEqual(actual_finally, expected_finally)
        scroll = 'await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).scrollIntoViewIfNeeded();'
        assertion = 'await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });'
        self.assertLess(source.index(scroll), source.index(QUEUED_CANCEL_GEOMETRY_SAMPLE))
        self.assertLess(source.index(QUEUED_CANCEL_GEOMETRY_SAMPLE), source.index(assertion))
        self.assertEqual(source.count('.evaluate(captureComputerButtonGeometry)'), 1)
        self.assertEqual(source.count('computerButtonGeometrySuffix(buttonGeometry)'), 1)
        self.assertLess(source.index('await restoreOwnedExportWorker();'), source.index('computerButtonGeometrySuffix(buttonGeometry)'))
        queued_cancel_browser_contract(source)
        helper = (root / 'web/e2e/computer-asset-export-v3-viewport-result.ts').read_bytes().decode('utf-8')
        queued_cancel_geometry_helper_contract(helper)
        unit = (root / 'web/src/ComputerExportViewportResult.test.ts').read_bytes()
        self.assertEqual(hashlib.sha256(unit[:9385]).hexdigest(), 'aaac0eb1cc1915d623f7802b6043fe3a73bcc23d55905a5bb816f09025cae611')
        for invalid in (None, b'', 0, {}, []):
            with self.subTest(invalid_type=type(invalid).__name__):
                with self.assertRaises(ValueError):
                    restore_queued_cancel_geometry(invalid)

    def test_queued_pre_assert_geometry_source_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        assignment = '      buttonGeometry = projectComputerButtonGeometry(await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).evaluate(captureComputerButtonGeometry));'
        gate = '${failure === "old-panel-create-button-viewport" ? computerButtonGeometrySuffix(buttonGeometry) : ""}'
        mutations = (
            ('missing geometry import', QUEUED_CANCEL_GEOMETRY_IMPORT, QUEUED_CANCEL_RATIO_IMPORT),
            ('missing geometry state', QUEUED_CANCEL_GEOMETRY_INIT, ''),
            ('duplicate geometry state', QUEUED_CANCEL_GEOMETRY_INIT, QUEUED_CANCEL_GEOMETRY_INIT * 2),
            ('unbound geometry state', QUEUED_CANCEL_GEOMETRY_INIT, QUEUED_CANCEL_GEOMETRY_INIT.replace('= null;', '= { status: "unavailable" };')),
            ('missing geometry phase', QUEUED_CANCEL_GEOMETRY_ENUM, ''),
            ('duplicate geometry phase', QUEUED_CANCEL_GEOMETRY_ENUM, QUEUED_CANCEL_GEOMETRY_ENUM * 2),
            ('missing sample', QUEUED_CANCEL_GEOMETRY_SAMPLE, ''),
            ('duplicate sample', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE * 2),
            ('different browser target', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE.replace('panel.getByRole', 'taskPage.getByRole')),
            ('different button name', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE.replace('按当前列表创建另一导出', '取消计算机导出任务')),
            ('nonexact locator', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE.replace('exact: true', 'exact: false')),
            ('evaluateAll changes lifecycle', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE.replace('.evaluate(', '.evaluateAll(')),
            ('browser function closure changed', QUEUED_CANCEL_GEOMETRY_SAMPLE, QUEUED_CANCEL_GEOMETRY_SAMPLE.replace('evaluate(captureComputerButtonGeometry)', 'evaluate(() => captureComputerButtonGeometry(document.body))')),
            ('unawaited capture', assignment, assignment.replace('(await panel', '(panel')),
            ('race leaves pending capture', assignment, assignment.replace('await panel.getByRole', 'await Promise.race([panel.getByRole').replace('.evaluate(captureComputerButtonGeometry));', '.evaluate(captureComputerButtonGeometry), Promise.resolve({ status: "unavailable" })]));')),
            ('new capture timeout', assignment, assignment.replace('evaluate(captureComputerButtonGeometry)', 'evaluate(captureComputerButtonGeometry, undefined, { timeout: 5_000 })')),
            ('raw sample cache', assignment, assignment.replace('projectComputerButtonGeometry(await ', '(await ')),
            ('sample decides test pass', assignment, assignment + '\n      if (buttonGeometry.status === "sampled") return;'),
            ('sample starts request', assignment, assignment + '\n      await context.request.get("/api/auth/me");'),
            ('sample retries', assignment, assignment + '\n' + assignment),
            ('sample error escapes', '    } catch { buttonGeometry = { status: "unavailable" }; }', '    } catch (error) { throw error; }'),
            ('sample error serialized', '    } catch { buttonGeometry = { status: "unavailable" }; }', '    } catch (error) { console.error(error); }'),
            ('failure catch opens browser work', QUEUED_CANCEL_RATIO_CATCH, QUEUED_CANCEL_RATIO_CATCH.replace('    failure = phase;', '    failure = phase;\n    await page.evaluate(() => document.body);')),
            ('suffix missing', gate, ''),
            ('suffix on every phase', gate, '${computerButtonGeometrySuffix(buttonGeometry)}'),
            ('suffix on wrong phase', gate, gate.replace('old-panel-create-button-viewport', 'old-panel-task-id-viewport')),
            ('raw geometry interpolation', gate, '${buttonGeometry}'),
            ('raw JSON interpolation', gate, '${JSON.stringify(buttonGeometry)}'),
            ('suffix before cleanup', assignment, assignment + '\n      computerButtonGeometrySuffix(buttonGeometry);'),
            ('larger total test budget', 'test.setTimeout(900_000);', 'test.setTimeout(1_800_000);'),
        )
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'geometry mutation must target actual current source once')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        button_scroll = '    await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).scrollIntoViewIfNeeded();\n'
        button_assert = '    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });\n'
        without_sample = source.replace(QUEUED_CANCEL_GEOMETRY_SAMPLE, '', 1)
        moved = (
            ('sample before original scroll', without_sample.replace(button_scroll, QUEUED_CANCEL_GEOMETRY_SAMPLE + button_scroll, 1)),
            ('sample after original assertion', without_sample.replace(button_assert, button_assert + QUEUED_CANCEL_GEOMETRY_SAMPLE, 1)),
            ('whole sample moved to end', without_sample + QUEUED_CANCEL_GEOMETRY_SAMPLE),
            ('complete phase type moved', source.replace(QUEUED_CANCEL_CENTER_PHASE_TYPE, '', 1) + QUEUED_CANCEL_CENTER_PHASE_TYPE),
            ('complete positioning block moved', source.replace(QUEUED_CANCEL_CENTER_POST_RESUME, '', 1) + QUEUED_CANCEL_CENTER_POST_RESUME),
            ('physical CRLF drift', source.replace('\n', '\r\n')),
            ('extra final newline', source + '\n'),
        )
        for label, changed in moved:
            with self.subTest(mutation=label):
                self.assertNotEqual(changed, source)
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)

    def test_queued_center_positioning_inverse(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        restored = restore_queued_cancel_center(source)
        self.assertEqual(len(restored.encode('utf-8')), 22859)
        self.assertEqual(hashlib.sha256(restored.encode('utf-8')).hexdigest(), QUEUED_CANCEL_CENTER_BASELINE_SHA256)
        self.assertEqual(restored.count(QUEUED_CANCEL_GEOMETRY_PHASE_TYPE), 1)
        self.assertEqual(restored.count(QUEUED_CANCEL_GEOMETRY_POST_RESUME), 1)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_PHASE_TYPE), 1)
        self.assertEqual(source.count(QUEUED_CANCEL_CENTER_POST_RESUME), 1)
        phases = re.findall(r'^  \| "([a-z-]+)"', QUEUED_CANCEL_CENTER_PHASE_TYPE, re.MULTILINE)
        self.assertEqual(len(phases), 25)
        self.assertEqual(len(set(phases)), 25)
        ordered = (
            QUEUED_CANCEL_BUTTON_SCROLL,
            QUEUED_CANCEL_CENTER_BLOCK,
            QUEUED_CANCEL_GEOMETRY_SAMPLE,
            '    phase = "old-panel-create-button-viewport";\n',
            '    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });\n',
        )
        for value in ordered:
            self.assertEqual(source.count(value), 1)
        positions = [source.index(value) for value in ordered]
        self.assertEqual(positions, sorted(positions))
        self.assertEqual(source.count('.scrollIntoView({'), 1)
        self.assertEqual(source.count('.evaluate(element => element.scrollIntoView('), 1)
        self.assertEqual(source.count('.evaluate(captureComputerButtonGeometry)'), 1)
        self.assertEqual(source.count('.scrollIntoViewIfNeeded()'), restored.count('.scrollIntoViewIfNeeded()'))
        self.assertEqual(source.count('toBeInViewport({ ratio: 1 })'), restored.count('toBeInViewport({ ratio: 1 })'))
        self.assertEqual(source.count(QUEUED_CANCEL_RATIO_CATCH), 1)
        self.assertTrue(source.endswith(QUEUED_CANCEL_GEOMETRY_CLEANUP))
        for path, expected in (
            ('web/e2e/computer-asset-export-v3-viewport-result.ts', '4e0027835fa329a1bb93ef78afe6c3506ad5b0c3a459d475f51f84a0cd924a81'),
            ('web/src/ComputerExportViewportResult.test.ts', '441195aead10829d0a0699caebeae0a1ef783060a31af3586467c98f396d4e64'),
        ):
            self.assertEqual(hashlib.sha256((root / path).read_bytes()).hexdigest(), expected)
        queued_cancel_browser_contract(source)
        for invalid in (None, b'', 0, {}, []):
            with self.subTest(invalid_type=type(invalid).__name__):
                with self.assertRaises(ValueError):
                    restore_queued_cancel_center(invalid)

    def test_queued_center_positioning_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        enum = QUEUED_CANCEL_CENTER_ENUM
        block = QUEUED_CANCEL_CENTER_BLOCK
        line = QUEUED_CANCEL_CENTER_LINE
        phase = '    phase = "old-panel-create-button-center";\n'
        call = line.strip().removeprefix('await ').removesuffix(';')
        mutations = (
            ('missing center enum', enum, ''),
            ('duplicate center enum', enum, enum * 2),
            ('comment-only center enum', enum, '  // ' + enum.strip() + '\n'),
            ('missing center phase', phase, ''),
            ('duplicate center phase', phase, phase * 2),
            ('dynamic center phase', phase, '    phase = "old-panel-create-button-center:" + originalTaskID;\n'),
            ('wrong center phase', phase, '    phase = "old-panel-create-button-scroll";\n'),
            ('center phase after operation', block, line + phase),
            ('missing center operation', line, ''),
            ('duplicate center operation', line, line * 2),
            ('missing center block', block, ''),
            ('duplicate center block', block, block * 2),
            ('wrong page target', line, line.replace('panel.', 'taskPage.', 1)),
            ('wrong button name', line, line.replace('按当前列表创建另一导出', '创建导出', 1)),
            ('inexact button target', line, line.replace('exact: true', 'exact: false', 1)),
            ('first button target', line, line.replace('.evaluate(', '.first().evaluate(', 1)),
            ('nth button target', line, line.replace('.evaluate(', '.nth(0).evaluate(', 1)),
            ('unawaited native operation', line, line.replace('await ', '', 1)),
            ('evaluateAll lifecycle', line, line.replace('.evaluate(', '.evaluateAll(', 1)),
            ('async callback', line, line.replace('element =>', 'async element =>', 1)),
            ('discarded native return', line, line.replace('element => element.', 'element => { element.', 1).replace('}));', '}); });', 1)),
            ('smooth instead of instant', line, line.replace('behavior: "instant"', 'behavior: "smooth"', 1)),
            ('auto instead of instant', line, line.replace('behavior: "instant"', 'behavior: "auto"', 1)),
            ('omitted explicit behavior', line, line.replace(', behavior: "instant"', '', 1)),
            ('start instead of center', line, line.replace('block: "center"', 'block: "start"', 1)),
            ('end instead of center', line, line.replace('block: "center"', 'block: "end"', 1)),
            ('omitted block alignment', line, line.replace('block: "center", ', '', 1)),
            ('expanded inline centering', line, line.replace('inline: "nearest"', 'inline: "center"', 1)),
            ('omitted explicit inline', line, line.replace('inline: "nearest", ', '', 1)),
            ('added evaluation timeout', line, line.replace('}));', '}), undefined, { timeout: 120_000 });', 1)),
            ('raced native operation', line, '    await Promise.race([' + call + ', Promise.resolve()]);\n'),
            ('parallel native operation', line, '    await Promise.all([' + call + ', panel.click()]);\n'),
            ('swallowed positioning rejection', line, '    await ' + call + '.catch(() => undefined);\n'),
            ('explicit retry', line, '    try {\n' + line + '    } catch {\n' + line + '    }\n'),
            ('post-scroll delay', line, line + '    await page.waitForTimeout(1000);\n'),
            ('post-scroll click', line, line + '    await panel.click();\n'),
            ('post-scroll focus', line, line + '    await taskPage.bringToFront();\n'),
            ('extra receipt read', line, line + '    await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);\n'),
            ('extra proof action', line, line + '    await proof(taskPage);\n'),
            ('larger viewport', line, line + '    await page.setViewportSize({ width: 1440, height: 1800 });\n'),
            ('CSS workaround', line, line + '    await page.addStyleTag({ content: "button { height: 1px }" });\n'),
            ('raw center error output', line, '    await ' + call + '.catch(error => { console.error(error); });\n'),
            ('longer suite budget', 'test.setTimeout(900_000);', 'test.setTimeout(1_800_000);'),
            ('unrelated source bytes', '// No replay, body interception, synthetic response or cache change.', '// Unrelated source changed.'),
        )
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'center mutation targets actual source once')
                self.assertNotEqual(before, after)
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        without = source.replace(block, '', 1)
        viewport = '    phase = "old-panel-create-button-viewport";\n'
        assertion = '    await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });\n'
        moved = (
            ('center before original stable scroll', without.replace(QUEUED_CANCEL_BUTTON_SCROLL, block + QUEUED_CANCEL_BUTTON_SCROLL, 1)),
            ('center after geometry sample', without.replace(viewport, block + viewport, 1)),
            ('center after original ratio1', without.replace(assertion, assertion + block, 1)),
            ('center block at end', without + block),
            ('center enum after geometry', source.replace(enum + QUEUED_CANCEL_GEOMETRY_ENUM, QUEUED_CANCEL_GEOMETRY_ENUM + enum, 1)),
            ('center enum at end', source.replace(enum, '', 1) + enum),
            ('current whole phase type moved', source.replace(QUEUED_CANCEL_CENTER_PHASE_TYPE, '', 1) + QUEUED_CANCEL_CENTER_PHASE_TYPE),
            ('current whole sequence moved', source.replace(QUEUED_CANCEL_CENTER_POST_RESUME, '', 1) + QUEUED_CANCEL_CENTER_POST_RESUME),
            ('physical CRLF drift', source.replace('\n', '\r\n')),
            ('extra final newline', source + '\n'),
        )
        for label, changed in moved:
            with self.subTest(mutation=label):
                self.assertNotEqual(changed, source)
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)

    def test_queued_geometry_callback_source_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/computer-asset-export-v3-viewport-result.ts').read_bytes().decode('utf-8')
        queued_cancel_geometry_helper_contract(source)
        start = 'export function captureComputerButtonGeometry('
        rect = '    const rect = element.getBoundingClientRect();'
        mutations = (
            ('callback removed', QUEUED_CANCEL_GEOMETRY_CALLBACK, ''),
            ('callback duplicated', QUEUED_CANCEL_GEOMETRY_CALLBACK, QUEUED_CANCEL_GEOMETRY_CALLBACK * 2),
            ('async browser callback', start, 'export async function captureComputerButtonGeometry('),
            ('repeated rect sample', rect, rect + '\n    element.getBoundingClientRect();'),
            ('DOM text disclosed', rect, rect + '\n    return { text: element.textContent };'),
            ('URL disclosed', rect, rect + '\n    return { url: document.URL };'),
            ('credentials inspected', rect, rect + '\n    return { cookie: document.cookie };'),
            ('computed style read', rect, rect + '\n    getComputedStyle(element);'),
            ('new timer', rect, rect + '\n    setTimeout(() => {}, 1);'),
            ('new request', rect, rect + '\n    fetch("/api/auth/me");'),
            ('new observer', rect, rect + '\n    new IntersectionObserver(() => {}).observe(element);'),
            ('loop introduced', rect, rect + '\n    for (const child of element.children) child.getBoundingClientRect();'),
            ('module closure dependency', rect, rect + '\n    projectComputerButtonGeometry(rect);'),
        )
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'callback mutation targets actual current helper')
                with self.assertRaises(ValueError):
                    queued_cancel_geometry_helper_contract(source.replace(before, after, 1))


if __name__ == '__main__':
    unittest.main()
