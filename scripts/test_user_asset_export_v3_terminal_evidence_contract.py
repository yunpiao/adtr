"""Pure-Python corruption tests. Generated unit specimens are NOT E2E artifacts."""
import base64
import copy
import json
import re
from pathlib import Path
import unittest
import validate_user_asset_export_v3_terminal_evidence as v
import test_user_asset_export_v3_evidence_contract as old


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
  if (failure || cleanupFailed) throw new Error(`User export task cancel checkpoint failed: ${failure || "cleanup"}${cleanupFailed ? "; cleanup_failed" : ""}`);
});
'''
QUEUED_CANCEL_ARM = '''    phase = "pause"; restoreWorker = true;
    const pause = await pauseOwnedExportWorker();'''
QUEUED_CANCEL_IMAGE = 'const image = await screenshot(taskPage, info, "cancelled-terminal.png");'


# Exact diagnostic-only boundaries: no new action, retry, timeout, or raw error.
# These are source guards, not evidence that the browser or Worker ran.
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
QUEUED_CANCEL_PHASE_INIT = '  let phase: QueuedCancelPhase = "setup", failure: QueuedCancelPhase | "" = "", cleanupFailed = false, restoreWorker = false;'
QUEUED_CANCEL_POST_RESUME = '''    phase = "resume-owned-worker";
    const resume = await resumeOwnedExportWorker(context, pause);
    phase = "post-resume-receipt-detail";
    const postResumeReads = await receiptAndDetail(page, setup.actorId, originalTaskID);
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
    try {
      await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });
    } catch (originalFailure) {
      let diagnosticTimer: ReturnType<typeof setTimeout> | undefined;
      try {
        // Failure-only, one-shot DOM reads; count 2 means two or more matches.
        // Never emit text, identity, DOM, requests, or the original exception.
        const measure = (elements: Element[]) => {
          const count = elements.length === 0 ? 0 : elements.length === 1 ? 1 : 2;
          const box = elements.length === 1 ? elements[0].getBoundingClientRect() : null;
          const rect = box && [box.x, box.y, box.width, box.height].every((value) => Number.isFinite(value) && Math.abs(value) <= 1_000_000)
            && box.width >= 0 && box.height >= 0
            ? { x: box.x, y: box.y, width: box.width, height: box.height } : null;
          return { count, rect };
        };
        const size = page.viewportSize();
        const viewport = size && [size.width, size.height].every((value) => Number.isFinite(value) && value >= 0 && value <= 1_000_000)
          ? { width: size.width, height: size.height } : null;
        const deadline = new Promise<never>((_, reject) => {
          diagnosticTimer = setTimeout(() => reject(new Error("Viewport diagnostic deadline")), 1_000);
        });
        const [status, taskIdentity, nextExport] = await Promise.race([
          Promise.all([
            cancelledStatus.evaluateAll(measure),
            panel.getByText(`任务 ID：${originalTaskID}`, { exact: true }).evaluateAll(measure),
            panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).evaluateAll(measure),
          ]),
          deadline,
        ]);
        console.log("User export terminal viewport diagnostic: " + JSON.stringify({ viewport, status, taskIdentity, nextExport }));
      } catch {
        console.log("User export terminal viewport diagnostic unavailable");
      } finally {
        if (diagnosticTimer !== undefined) clearTimeout(diagnosticTimer);
      }
      throw originalFailure;
    }

'''


def queued_cancel_browser_contract(source):
    v.require(type(source) is str and source.endswith(QUEUED_CANCEL_CLEANUP), 'closed queued cleanup and safe failure propagation')
    v.require(source.count('  finally {\n') == 1, 'one outer queued cleanup')
    v.require(source.count('await taskPage.close();') == 1 and source.count('await restoreOwnedExportWorker();') == 1, 'await each owned cleanup exactly once')
    v.require(re.findall(r'\brestoreWorker\s*=\s*([^;,\n]+)', source) == ['false', 'true'], 'restore guard initialized once and never disarmed')
    v.require(source.count(QUEUED_CANCEL_ARM) == 1, 'arm restoration before attempting pause')
    outer_try = '  try {\n    expect(process.env.ADTR_E2E_USER_ASSET_EXPORT_V3_TERMINAL)'
    v.require(source.count(outer_try) == 1 and source.index(outer_try) < source.index(QUEUED_CANCEL_ARM), 'pause attempt belongs to protected outer try')
    v.require(source.count('test.setTimeout(900_000);') == 1, 'unchanged queued timeout')
    v.require(source.count(QUEUED_CANCEL_PHASE_TYPE) == 1, 'closed queued diagnostic phase type')
    v.require(source.count(QUEUED_CANCEL_PHASE_INIT) == 1, 'closed queued phase and failure initialization')
    v.require(source.count(QUEUED_CANCEL_POST_RESUME) == 1, 'exact post-resume actions and diagnostic boundaries')
    phases = re.findall(r'"([^"\n]+)"', QUEUED_CANCEL_PHASE_TYPE)
    v.require(re.findall(r'\bphase\s*=\s*([^;\n]+)', source) == [f'"{phase}"' for phase in phases[1:]], 'only ordered closed phase assignments')
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
        '''await expect(taskPage.locator('input[type="password"], input[name="totpCode"]')).toHaveCount(0);''',
        QUEUED_CANCEL_IMAGE,
        'await writeTerminalEvidence(info, setup, "queuedCancel",',
    ]
    for value in ordered:
        v.require(source.count(value) == 1, 'unique required queued checkpoint')
    positions = [source.index(value) for value in ordered]
    v.require(positions == sorted(positions), 'resumed DB then real new-entry pixels then evidence')
    v.require(source.count('await screenshot(') == 1, 'one retained queued screenshot')
    v.require(source.count('toBeInViewport({ ratio: 1 })') >= 5, 'old and new visible terminal identity')


def terminal_db(task_id, key, state):
    actions = ['submitted', 'leased', 'started', 'progress', 'finished'] if state == 'failed' else ['submitted', 'cancel_requested'] if state == 'cancelled' else ['submitted']
    events = []
    for i, action in enumerate(actions):
        events.append({'action': action, 'state': state if i == len(actions)-1 else 'queued' if i == 0 else 'running', 'attempt': int(state == 'failed' and i >= 2), 'resultVersion': i})
    return {'taskID': task_id, 'actorId': '1', 'kind': 'user_assets.v3.export', 'state': state, 'error': {'failed': 'export_invalid_text', 'cancelled': 'cancelled', 'queued': ''}[state], 'attempt': int(state == 'failed'), 'maxAttempts': 3, 'progress': 10 if state == 'failed' else 0, 'resultVersion': len(actions)-1, 'result': {}, 'idempotencyKey': key, 'leaseOwner': '', 'leaseUntil': None, 'snapshotCount': 0, 'manifestCount': 0, 'chunkCount': 0, 'events': events}


def response(task_id, request, state):
    return {'taskID': task_id, 'request': copy.deepcopy(request), 'task': {'taskUUID': task_id, 'taskName': 'user_assets.v3.export', 'state': state, 'error': 'export_invalid_text' if state == 'failed' else 'cancelled' if state == 'cancelled' else ''}}


def specimen(failure=True):
    data, files = old.specimen()
    data['suite'] = 'failureRecovery' if failure else 'queuedCancel'
    data['worker'] = None
    if failure:
        source = data['source']
        body = json.loads(base64.b64decode(source['bodyBase64']))
        body['objects'][0]['supplemental']['mailBytes'] = old.b64(b'export-invalid\x01@example.test')
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
    png = files['allMatched-ready.png']
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
        body['objects'][0]['supplemental']['mailBytes'] = old.b64(b'ordinary@example.test')
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
            v.validate(data, files.__getitem__, {'fixtureOnly': True}, 'user-asset-export-v3-cancel')
        files['failed-terminal.png'] += b'tamper'
        with self.assertRaises(ValueError):
            self.check(data, files)

    def test_browser_source_contract(self):
        root = Path(__file__).resolve().parents[1]
        helper = (root / 'web/e2e/user-asset-export-v3-terminal-helpers.ts').read_text()
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
            source = (root / f'web/e2e/user-asset-export-v3-{name}.spec.ts').read_text()
            self.assertNotIn('route.fulfill', source)
            self.assertNotIn('waitForTimeout', source)
            self.assertIn('test.setTimeout(900_000)', source)
        failure = (root / 'web/e2e/user-asset-export-v3-failure.spec.ts').read_text()
        self.assertIn('terminalError.scrollIntoViewIfNeeded()', failure)
        self.assertGreaterEqual(failure.count('toBeInViewport({ ratio: 1 })'), 4)
        cancel = (root / 'web/e2e/user-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(cancel)
        self.assertIn('cancelledStatus.scrollIntoViewIfNeeded()', cancel)
        self.assertGreaterEqual(cancel.count('toBeInViewport({ ratio: 1 })'), 3)
        self.assertLess(cancel.index('expect(postResume.database).toEqual(database)'), cancel.index('await screenshot(taskPage, info,'))

    def test_queued_browser_cleanup_and_pixel_contract_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/user-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(source)
        restore = '      try { await restoreOwnedExportWorker(); } catch { cleanupFailed = true; }'
        close = '      try { await taskPage.close(); } catch { cleanupFailed = true; }'
        proof_empty = '''await expect(taskPage.locator('input[type="password"], input[name="totpCode"]')).toHaveCount(0);'''
        mutations = [
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

    def test_queued_browser_diagnostic_stage_contract_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/user-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(source)
        phases = re.findall(r'"([^"\n]+)"', QUEUED_CANCEL_PHASE_TYPE)
        for phase in phases[1:]:
            before = f'    phase = "{phase}";'
            for label, after in [('missing', ''), ('duplicate', before + '\n' + before), ('dynamic', '    phase = originalTaskID;')]:
                with self.subTest(phase=phase, mutation=label):
                    self.assertEqual(source.count(before), 1)
                    with self.assertRaises(ValueError):
                        queued_cancel_browser_contract(source.replace(before, after, 1))
        for phase in phases[8:19]:
            before = f'    phase = "{phase}";'
            with self.subTest(phase=phase, mutation='comment-only'):
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, '    // ' + before.strip(), 1))
        mutations = [
            ('open phase type', QUEUED_CANCEL_PHASE_TYPE, 'type QueuedCancelPhase = string;\n\n'),
            ('open phase variable', 'phase: QueuedCancelPhase', 'phase: string'),
            ('open failure variable', 'failure: QueuedCancelPhase | ""', 'failure: string'),
            ('raw phase value', 'phase = "post-resume-database";', 'phase = String(originalTaskID);'),
            ('receipt action removed', 'await receiptAndDetail(page, setup.actorId, originalTaskID);\n    phase = "post-resume-database";', 'recovered;\n    phase = "post-resume-database";'),
            ('DB invariance weakened', 'expect(postResume.database).toEqual(database);', 'expect(postResume.database.state).toBe("cancelled");'),
            ('detail invariant removed', '    expect(postResume.detail).toMatchObject({ downloadReady: false, downloadStatus: "cancelled", task: { state: "cancelled" } });', ''),
            ('viewport threshold weakened', 'await expect(cancelledStatus).toBeInViewport({ ratio: 1 });', 'await expect(cancelledStatus).toBeInViewport({ ratio: 0.5 });'),
            ('business action retried', 'const postResumeReads = await receiptAndDetail(page, setup.actorId, originalTaskID);', 'let postResumeReads; for (let i = 0; i < 2; i++) postResumeReads = await receiptAndDetail(page, setup.actorId, originalTaskID);'),
        ]
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation must target one actual boundary')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))
        before = '    phase = "post-resume-database";\n    const postResumeDatabase = await terminalExport(originalTaskID);'
        after = '    const postResumeDatabase = await terminalExport(originalTaskID);\n    phase = "post-resume-database";'
        with self.assertRaises(ValueError):
            queued_cancel_browser_contract(source.replace(before, after, 1))


    def test_queued_browser_failure_geometry_diagnostic_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/user-asset-export-v3-cancel.spec.ts').read_text()
        queued_cancel_browser_contract(source)
        mutations = [
            ('raw error payload', 'JSON.stringify({ viewport, status, taskIdentity, nextExport })', 'JSON.stringify({ viewport, status, taskIdentity, nextExport, originalFailure })'),
            ('raw task identity payload', 'JSON.stringify({ viewport, status, taskIdentity, nextExport })', 'JSON.stringify({ viewport, status, taskIdentity, nextExport, originalTaskID })'),
            ('DOM text payload', 'return { count, rect };', 'return { count, rect, text: elements[0]?.textContent };'),
            ('unbounded count', 'const count = elements.length === 0 ? 0 : elements.length === 1 ? 1 : 2;', 'const count = elements.length;'),
            ('open rectangle object', '{ x: box.x, y: box.y, width: box.width, height: box.height }', '{ ...box }'),
            ('nonfinite rectangle allowed', 'Number.isFinite(value) && Math.abs(value) <= 1_000_000', 'Math.abs(value) <= 1_000_000'),
            ('unbounded rectangle allowed', 'Number.isFinite(value) && Math.abs(value) <= 1_000_000', 'Number.isFinite(value)'),
            ('negative rectangle size allowed', '&& box.width >= 0 && box.height >= 0', ''),
            ('unbounded viewport allowed', 'Number.isFinite(value) && value >= 0 && value <= 1_000_000', 'Number.isFinite(value)'),
            ('multiple-match geometry', 'elements.length === 1 ? elements[0].getBoundingClientRect() : null', 'elements.length > 0 ? elements[0].getBoundingClientRect() : null'),
            ('diagnostic deadline enlarged', 'setTimeout(() => reject(new Error("Viewport diagnostic deadline")), 1_000)', 'setTimeout(() => reject(new Error("Viewport diagnostic deadline")), 60_000)'),
            ('diagnostic deadline ignored', 'const [status, taskIdentity, nextExport] = await Promise.race([', 'const [status, taskIdentity, nextExport] = await Promise.all(['),
            ('diagnostic timer not cleared', 'if (diagnosticTimer !== undefined) clearTimeout(diagnosticTimer);', ''),
            ('diagnostic error exposed', 'console.log("User export terminal viewport diagnostic unavailable");', 'console.log(originalFailure);'),
            ('original failure swallowed', '      throw originalFailure;', '      return;'),
            ('original failure replaced', '      throw originalFailure;', '      throw new Error("diagnostic failure");'),
            ('button assertion weakened', 'await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 1 });', 'await expect(panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true })).toBeInViewport({ ratio: 0.5 });'),
            ('scroll target changed', 'await cancelledStatus.scrollIntoViewIfNeeded();', 'await panel.getByRole("button", { name: "按当前列表创建另一导出", exact: true }).scrollIntoViewIfNeeded();'),
            ('diagnostic adds scrolling', 'cancelledStatus.evaluateAll(measure),', 'cancelledStatus.scrollIntoViewIfNeeded(),'),
        ]
        for label, before, after in mutations:
            with self.subTest(mutation=label):
                self.assertEqual(source.count(before), 1, 'mutation must target one diagnostic boundary')
                with self.assertRaises(ValueError):
                    queued_cancel_browser_contract(source.replace(before, after, 1))


if __name__ == '__main__':
    unittest.main()
