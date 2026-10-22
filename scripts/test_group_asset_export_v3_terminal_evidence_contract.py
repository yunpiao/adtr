"""Pure-Python corruption tests. Generated unit specimens are NOT E2E artifacts."""
import base64
import copy
import json
import re
from pathlib import Path
import unittest
import validate_group_asset_export_v3_terminal_evidence as v
import test_group_asset_export_v3_evidence_contract as old


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
  if (failure || cleanupFailed) throw new Error(`Group export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}`);
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
  if (receiptResponses.length !== 1 || !receiptResponse) throw new Error("Original group export receipt checkpoint failed");
  const receipt = await responseValue(receiptResponse, actor), detail = await responseValue(detailResponse, actor);
  expect(receipt.taskID).toBe(taskID);
  expect(detail.taskID).toBe(taskID);
  return { receipt, detail };
}
'''


# Group-specific closed source bindings. These are structural regression guards,
# not claims of browser execution or a replacement for the artifact validator.
# Byte-sensitive entry points deliberately do not normalize CRLF.
QUEUED_PHASE_TYPE = '''type QueuedCancelPhase =
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
QUEUED_PHASE_INIT = '  let phase: QueuedCancelPhase = "setup", failure: QueuedCancelPhase | "" = "", cleanupFailed = false, restoreWorker = false;'
QUEUED_POST_RESUME = '''    phase = "resume-owned-worker";
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
QUEUED_REQUEST_OBSERVER = '''// Bind the response to a newly emitted original Request from this action.
// Join both owned branches and detach every listener/timer before cleanup.
// No replay, body interception, synthetic response or cache change.
async function observedAction(page: Page, predicate: (request: BrowserRequest) => boolean, action: () => Promise<unknown>) {
  const seen = new Set<BrowserRequest>();
  let armed = false, settled = false;
  let resolve!: (response: Response) => void, reject!: (error: Error) => void;
  const completed = new Promise<Response>((yes, no) => { resolve = yes; reject = no; });
  const fail = () => { if (!settled) { settled = true; reject(new Error("Original group export browser checkpoint failed")); } };
  const started = (request: BrowserRequest) => {
    try {
      if (!armed || !predicate(request)) return;
      if (seen.size >= 16) { fail(); return; }
      seen.add(request);
    } catch { fail(); }
  };
  const received = (response: Response) => {
    try {
      if (settled || !seen.has(response.request())) return;
      if (response.fromServiceWorker()) { fail(); return; }
      settled = true; resolve(response);
    } catch { fail(); }
  };
  const failed = (request: BrowserRequest) => { if (seen.has(request)) fail(); };
  page.on("request", started); page.on("response", received); page.on("requestfailed", failed);
  const timer = setTimeout(fail, 30_000);
  try {
    const actionResult = Promise.resolve().then(() => { armed = true; return action(); }).catch(() => {
      fail(); throw new Error("Original group export action failed");
    });
    const results = await Promise.allSettled([completed, actionResult] as const);
    if (results[0].status !== "fulfilled" || results[1].status !== "fulfilled")
      throw new Error("Original group export browser checkpoint failed");
    return results[0].value;
  } finally {
    clearTimeout(timer); page.off("request", started); page.off("response", received); page.off("requestfailed", failed);
    fail(); await completed.catch(() => undefined);
  }
}
'''
QUEUED_MARKER_OBSERVER = '''async function observeMarkerBeforeCancel(page: Page, key: string, expected: string) {
  // Read only the selected marker, before native fetch. No request body/proof,
  // changed arguments, route, replay, cache change or fabricated response.
  // Observation failure is counted but cannot suppress the native request.
  await page.addInitScript(({ key, expected, path }) => {
    const target = window as WitnessWindow;
    const witness: MarkerWitness = { calls: 0, matched: true, observationErrors: 0 };
    target.__adtrGroupCancelMarkerWitness = witness;
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

'''
QUEUED_GROUP_MARKER = '''    const request = operation.create.request;
    // Independent fixed order, including every original filter and column.
    const requestSHA256 = hash(JSON.stringify({ domainId: request.domainId,
      expectedRevision: request.expectedRevision, expectedCredentialRevision: request.expectedCredentialRevision,
      observationId: request.observationId, search: request.search,
      creatTm: request.creatTm, screenColumns: request.screenColumns }));
    const markerKey = `adtr.group-export-cancel.v1.${setup.actorId}.${originalTaskID}`;
    const expectedMarker = JSON.stringify({ version: 1, kind: "group_assets.v3.export", actorID: setup.actorId,
      taskID: originalTaskID, domainId: request.domainId, requestSHA256 });
    await observeMarkerBeforeCancel(taskPage, markerKey, expectedMarker);
'''
QUEUED_PHASES = ['setup', 'pause', 'create', 'default-read-only', 'new-tab', 'dedicated-original-detail', 'dedicated-cancel', 'original-asset-receipt', 'resume-owned-worker', 'post-resume-receipt-detail', 'post-resume-database', 'post-resume-download-unavailable', 'post-resume-database-invariant', 'post-resume-detail-invariant', 'post-resume-download-events', 'old-panel-status-scroll', 'old-panel-status-viewport', 'old-panel-task-id-viewport', 'old-panel-create-button-viewport', 'new-entry-terminal-pixels', 'evidence']


def queued_cancel_browser_contract(source):
    v.require(type(source) is str and source.endswith(QUEUED_CANCEL_CLEANUP), 'closed queued cleanup and safe failure propagation')
    v.require(source.count('  finally {\n') == 1, 'one outer queued cleanup')
    v.require(source.count('await taskPage.close();') == 1 and source.count('await restoreOwnedExportWorker();') == 1, 'await each owned cleanup exactly once')
    v.require(re.findall(r'\brestoreWorker\s*=\s*([^;,\n]+)', source) == ['false', 'true'], 'restore guard initialized once and never disarmed')
    v.require(source.count(QUEUED_CANCEL_ARM) == 1, 'arm restoration before attempting pause')
    outer_try = '  try {\n    expect(process.env.ADTR_E2E_GROUP_ASSET_EXPORT_V3_TERMINAL)'
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
    v.require('\r' not in source, 'exact LF source bytes')
    for block in (QUEUED_PHASE_TYPE, QUEUED_PHASE_INIT, QUEUED_POST_RESUME, QUEUED_REQUEST_OBSERVER, QUEUED_MARKER_OBSERVER, QUEUED_GROUP_MARKER):
        v.require(source.count(block) == 1, 'unique exact group checkpoint block')
    phase_assignments = re.findall(r'(?m)^    phase = "([a-z-]+)";', source)
    v.require(phase_assignments == QUEUED_PHASES[1:], 'closed phases preserve all original operation order')
    v.require(len(re.findall(r'\bphase\s*=', source)) == len(QUEUED_PHASES) - 1, 'no dynamic or additional phase assignment')
    for forbidden in ('startTm', 'endTm', 'request.sAMAccountName', 'request.groupType', 'request.groupScope'):
        v.require(forbidden not in source, 'group canonical request has only its seven fields')
    for token in (
        'const operation = await createExport(page, setup);',
        'await proof(taskPage, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);',
        'taskPage = await context.newPage();',
        'expect(await taskPage.evaluate(() => window.opener === null), "fresh tab has no opener").toBe(true);',
        'expect(await taskPage.evaluate((key) => sessionStorage.getItem(key) === null, locatorKey), "no locator cloned or seeded").toBe(true);',
        'await navigateTo(taskPage, "后台任务");',
        'await taskPage.getByLabel("任务种类筛选", { exact: true }).fill("group_assets.v3.export");',
        '() => taskPage!.getByRole("button", { name: `详情 ${originalTaskID}`, exact: true }).click());',
        'expect(businessRequests, "collapsed task detail has no export business request").toEqual([]);',
        'expect(businessRequests.filter((value) => value === `POST ${exportPrefix}/cancel`)).toHaveLength(1);',
        'expect(businessRequests.every((value) => [`GET ${exportPrefix}/detail`, `POST ${exportPrefix}/cancel`].includes(value)), "no create/receipt/download from new entry").toBe(true);',
        'expect(genericWrites, "no generic task writes").toEqual([]);',
        'expect(wrongDetailTarget || requestObservationFailed, "all observed reads bind original task").toBe(false);',
        'expect(await taskPage.evaluate((key) => localStorage.getItem(key) === null, markerKey), "verified terminal marker removed").toBe(true);',
        'expect(await taskPage.evaluate((key) => sessionStorage.getItem(key) === null, locatorKey), "new entry never writes a locator").toBe(true);',
    ):
        v.require(source.count(token) == 1, 'original-only new-tab workflow and unchanged mutation budget')
    v.require(source.count('await createExport(') == 1 and source.count('await proof(') == 1, 'one create plus one fresh cancel proof')
    v.require(source.count('() => control.getByRole("button", { name: "取消原组导出任务", exact: true }).click()') == 1, 'one dedicated cancel action')
    v.require('sessionStorage.setItem' not in source and 'sessionStorage.removeItem' not in source, 'never seed or erase recovery locator')


def terminal_db(task_id, key, state):
    actions = ['submitted', 'leased', 'started', 'progress', 'finished'] if state == 'failed' else ['submitted', 'cancel_requested'] if state == 'cancelled' else ['submitted']
    events = []
    for i, action in enumerate(actions):
        events.append({'action': action, 'state': state if i == len(actions)-1 else 'queued' if i == 0 else 'running', 'attempt': int(state == 'failed' and i >= 2), 'resultVersion': i})
    return {'taskID': task_id, 'actorId': '1', 'kind': 'group_assets.v3.export', 'state': state, 'error': {'failed': 'export_invalid_text', 'cancelled': 'cancelled', 'queued': ''}[state], 'attempt': int(state == 'failed'), 'maxAttempts': 3, 'progress': 10 if state == 'failed' else 0, 'resultVersion': len(actions)-1, 'result': {}, 'idempotencyKey': key, 'leaseOwner': '', 'leaseUntil': None, 'snapshotCount': 0, 'manifestCount': 0, 'chunkCount': 0, 'events': events}


def response(task_id, request, state):
    return {'taskID': task_id, 'request': copy.deepcopy(request), 'task': {'taskUUID': task_id, 'taskName': 'group_assets.v3.export', 'state': state, 'error': 'export_invalid_text' if state == 'failed' else 'cancelled' if state == 'cancelled' else ''}}


def specimen(failure=True):
    data, files = old.specimen()
    data['suite'] = 'failureRecovery' if failure else 'queuedCancel'
    data['worker'] = None
    data['sensitiveBudget']['after'].update(owner=9, exportTasks=2)
    if failure:
        source = data['source']
        body = json.loads(base64.b64decode(source['bodyBase64']))
        body['objects'][0]['base']['samAccountName'] = 'group-invalid\ufffe'
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
        body['objects'][0]['base']['samAccountName'] = 'group-00'
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
            v.validate(data, files.__getitem__, {'fixtureOnly': True}, 'group-asset-export-v3-cancel')
        files['failed-terminal.png'] += b'tamper'
        with self.assertRaises(ValueError):
            self.check(data, files)

    def test_browser_source_contract(self):
        root = Path(__file__).resolve().parents[1]
        helper = (root / 'web/e2e/group-asset-export-v3-terminal-helpers.ts').read_text()
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
            source = (root / f'web/e2e/group-asset-export-v3-{name}.spec.ts').read_text()
            self.assertNotIn('route.fulfill', source)
            self.assertNotIn('waitForTimeout', source)
            self.assertIn('test.setTimeout(900_000)', source)
        failure = (root / 'web/e2e/group-asset-export-v3-failure.spec.ts').read_text()
        self.assertIn('terminalError.scrollIntoViewIfNeeded()', failure)
        self.assertGreaterEqual(failure.count('toBeInViewport({ ratio: 1 })'), 4)
        cancel = (root / 'web/e2e/group-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(cancel)
        self.assertIn('cancelledStatus.scrollIntoViewIfNeeded()', cancel)
        self.assertGreaterEqual(cancel.count('toBeInViewport({ ratio: 1 })'), 3)
        self.assertLess(cancel.index('expect(postResume.database).toEqual(database)'), cancel.index('await screenshot(taskPage, info,'))


    def test_queued_browser_cleanup_and_pixel_contract_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/group-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
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
            ('outer failure swallowed', '  } catch { failure = phase; }', '  } catch {}'),
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


    def test_queued_group_identity_phase_and_byte_contract_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/group-asset-export-v3-cancel.spec.ts').read_bytes().decode('utf-8')
        queued_cancel_browser_contract(source)
        # Text replacements intentionally live here, never in callable artifact
        # corruption matrices. Every target must uniquely exist before mutation.
        mutations = [
            ('missing phase pause', '    phase = "pause";', ''),
            ('comment-only phase pause', '    phase = "pause";', '    // phase = "pause";'),
            ('missing phase create', '    phase = "create";', ''),
            ('comment-only phase create', '    phase = "create";', '    // phase = "create";'),
            ('missing phase default-read-only', '    phase = "default-read-only";', ''),
            ('comment-only phase default-read-only', '    phase = "default-read-only";', '    // phase = "default-read-only";'),
            ('missing phase new-tab', '    phase = "new-tab";', ''),
            ('comment-only phase new-tab', '    phase = "new-tab";', '    // phase = "new-tab";'),
            ('missing phase dedicated-original-detail', '    phase = "dedicated-original-detail";', ''),
            ('comment-only phase dedicated-original-detail', '    phase = "dedicated-original-detail";', '    // phase = "dedicated-original-detail";'),
            ('missing phase dedicated-cancel', '    phase = "dedicated-cancel";', ''),
            ('comment-only phase dedicated-cancel', '    phase = "dedicated-cancel";', '    // phase = "dedicated-cancel";'),
            ('missing phase original-asset-receipt', '    phase = "original-asset-receipt";', ''),
            ('comment-only phase original-asset-receipt', '    phase = "original-asset-receipt";', '    // phase = "original-asset-receipt";'),
            ('missing phase resume-owned-worker', '    phase = "resume-owned-worker";', ''),
            ('comment-only phase resume-owned-worker', '    phase = "resume-owned-worker";', '    // phase = "resume-owned-worker";'),
            ('missing phase post-resume-receipt-detail', '    phase = "post-resume-receipt-detail";', ''),
            ('comment-only phase post-resume-receipt-detail', '    phase = "post-resume-receipt-detail";', '    // phase = "post-resume-receipt-detail";'),
            ('missing phase post-resume-database', '    phase = "post-resume-database";', ''),
            ('comment-only phase post-resume-database', '    phase = "post-resume-database";', '    // phase = "post-resume-database";'),
            ('missing phase post-resume-download-unavailable', '    phase = "post-resume-download-unavailable";', ''),
            ('comment-only phase post-resume-download-unavailable', '    phase = "post-resume-download-unavailable";', '    // phase = "post-resume-download-unavailable";'),
            ('missing phase post-resume-database-invariant', '    phase = "post-resume-database-invariant";', ''),
            ('comment-only phase post-resume-database-invariant', '    phase = "post-resume-database-invariant";', '    // phase = "post-resume-database-invariant";'),
            ('missing phase post-resume-detail-invariant', '    phase = "post-resume-detail-invariant";', ''),
            ('comment-only phase post-resume-detail-invariant', '    phase = "post-resume-detail-invariant";', '    // phase = "post-resume-detail-invariant";'),
            ('missing phase post-resume-download-events', '    phase = "post-resume-download-events";', ''),
            ('comment-only phase post-resume-download-events', '    phase = "post-resume-download-events";', '    // phase = "post-resume-download-events";'),
            ('missing phase old-panel-status-scroll', '    phase = "old-panel-status-scroll";', ''),
            ('comment-only phase old-panel-status-scroll', '    phase = "old-panel-status-scroll";', '    // phase = "old-panel-status-scroll";'),
            ('missing phase old-panel-status-viewport', '    phase = "old-panel-status-viewport";', ''),
            ('comment-only phase old-panel-status-viewport', '    phase = "old-panel-status-viewport";', '    // phase = "old-panel-status-viewport";'),
            ('missing phase old-panel-task-id-viewport', '    phase = "old-panel-task-id-viewport";', ''),
            ('comment-only phase old-panel-task-id-viewport', '    phase = "old-panel-task-id-viewport";', '    // phase = "old-panel-task-id-viewport";'),
            ('missing phase old-panel-create-button-viewport', '    phase = "old-panel-create-button-viewport";', ''),
            ('comment-only phase old-panel-create-button-viewport', '    phase = "old-panel-create-button-viewport";', '    // phase = "old-panel-create-button-viewport";'),
            ('missing phase new-entry-terminal-pixels', '    phase = "new-entry-terminal-pixels";', ''),
            ('comment-only phase new-entry-terminal-pixels', '    phase = "new-entry-terminal-pixels";', '    // phase = "new-entry-terminal-pixels";'),
            ('missing phase evidence', '    phase = "evidence";', ''),
            ('comment-only phase evidence', '    phase = "evidence";', '    // phase = "evidence";'),
            ('unbounded phase type', 'type QueuedCancelPhase =', 'type QueuedCancelPhase = string |'),
            ('dynamic stage', '    phase = "post-resume-database";', '    phase = String(originalTaskID) as QueuedCancelPhase;'),
            ('extra phase assignment', '    phase = "create";', '    phase = "create"; phase = "setup";'),
            ('wrong await order', '    const postResumeReads = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);', '    const postResumeReads = await terminalExport(originalTaskID);'),
            ('unjoined post-resume receipt', '    const postResumeReads = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);', '    const postResumeReads = observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);'),
            ('cached post-resume detail', '    const postResumeReads = await observedReceiptAndDetail(page, setup.actorId, originalTaskID, request.domainId, operation.idempotencyKey);', '    const postResumeReads = recovered;'),
            ('cached post-resume database', '    const postResumeDatabase = await terminalExport(originalTaskID);', '    const postResumeDatabase = database;'),
            ('replayed post-resume database', '    const postResumeDatabase = await terminalExport(originalTaskID);', '    const postResumeDatabase = await terminalExport(originalTaskID); await terminalExport(originalTaskID);'),
            ('unjoined post-resume download', '    const postResumeUnavailableDownload = await unavailableDownload(context, originalTaskID);', '    const postResumeUnavailableDownload = unavailableDownload(context, originalTaskID);'),
            ('post-resume shape changed', '    const postResume = { ...postResumeReads, database: postResumeDatabase, unavailableDownload: postResumeUnavailableDownload };', '    const postResume = { ...recovered, database: postResumeDatabase, unavailableDownload: postResumeUnavailableDownload };'),
            ('weakened old status viewport', '    await expect(cancelledStatus).toBeInViewport({ ratio: 1 });', '    await expect(cancelledStatus).toBeInViewport({ ratio: 0.5 });'),
            ('raw exception cause', '  } catch { failure = phase; }', '  } catch (error) { throw new Error("failure", { cause: error }); }'),
            ('marker missing domain', '    const requestSHA256 = hash(JSON.stringify({ domainId: request.domainId,', '    const requestSHA256 = hash(JSON.stringify({'),
            ('marker missing credential pin', 'expectedCredentialRevision: request.expectedCredentialRevision,', ''),
            ('marker missing original search', 'observationId: request.observationId, search: request.search,', 'observationId: request.observationId,'),
            ('marker reorders columns', 'screenColumns: request.screenColumns }));', 'screenColumns: [...request.screenColumns].sort() }));'),
            ('foreign request filter', 'creatTm: request.creatTm, screenColumns: request.screenColumns }));', 'startTm: request.startTm, creatTm: request.creatTm, screenColumns: request.screenColumns }));'),
            ('marker key wrong task', 'const markerKey = `adtr.group-export-cancel.v1.${setup.actorId}.${originalTaskID}`;', 'const markerKey = `adtr.group-export-cancel.v1.${setup.actorId}.other`;'),
            ('native receiver replaced', 'return original.apply(this, args);', 'return original.apply(window, args);'),
            ('observer suppresses real request', '} catch { witness.observationErrors++; }', '} catch { witness.observationErrors++; throw new Error("capture"); }'),
            ('cancel counted twice', 'expect(businessRequests.filter((value) => value === `POST ${exportPrefix}/cancel`)).toHaveLength(1);', 'expect(businessRequests.filter((value) => value === `POST ${exportPrefix}/cancel`)).toHaveLength(2);'),
            ('new-tab no-opener unchecked', 'expect(await taskPage.evaluate(() => window.opener === null), "fresh tab has no opener").toBe(true);', ''),
            ('new-tab locator unchecked', 'expect(await taskPage.evaluate((key) => sessionStorage.getItem(key) === null, locatorKey), "no locator cloned or seeded").toBe(true);', ''),
            ('generic mutation unchecked', 'expect(genericWrites, "no generic task writes").toEqual([]);', ''),
            ('extra create proof budget', 'const operation = await createExport(page, setup);', 'const operation = await createExport(page, setup); await createExport(page, setup);'),
            ('extra cancel proof budget', 'await proof(taskPage, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);', 'await proof(taskPage, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth); await proof(taskPage, setup.fixture.bootstrap.password, setup.fixture.bootstrap.auth);'),
            ('seeded locator', 'taskPage = await context.newPage();', 'taskPage = await context.newPage(); await taskPage.evaluate(() => sessionStorage.setItem("locator", "seeded"));'),
            ('marker collection broadens', 'localStorage.getItem(key) === expected;', 'localStorage.length >= 0;'),
        ]
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation must target one actual source operation')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        # Same operations and names are insufficient: the frozen order and exact
        # physical bytes also matter. No parsing/execution of TypeScript here.
        receipt_phase = '    phase = "post-resume-receipt-detail";'
        database_phase = '    phase = "post-resume-database";'
        swapped = source.replace(receipt_phase, '__SWAP_STAGE__', 1).replace(database_phase, receipt_phase, 1).replace('__SWAP_STAGE__', database_phase, 1)
        for label, changed in (
            ('swapped phases', swapped),
            ('CRLF source bytes', source.replace('\n', '\r\n')),
            ('trailing newline', source + '\n'),
        ):
            with self.subTest(mutation=label):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(changed)


if __name__ == '__main__':
    unittest.main()
