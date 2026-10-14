"""Pure-Python corruption tests. Generated unit specimens are NOT E2E artifacts."""
import base64
import copy
import json
from pathlib import Path
import unittest
import validate_user_asset_export_v3_terminal_evidence as v
import test_user_asset_export_v3_evidence_contract as old


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
        data['worker'] = {'pause': {'pid': 20, 'parentPID': 10, 'starttime': '1000', 'binarySHA256': '1'*64, 'nonceSHA256': '2'*64, 'applicationNameSHA256': '3'*64, 'allThreadsStopped': True, 'threadCount': 6, 'connections': 1, 'activeTransactions': 0, 'workerID': 'worker-1', 'queueSuccessBefore': '1234.500000'}, 'resume': {'sameIdentity': True, 'allThreadsRunning': True, 'readyStatus': 200, 'workerID': 'worker-1', 'queueSuccessAfterFirst': '1234.750000', 'queueSuccessAfter': '1235.000000'}}
    return data, files


class TerminalEvidenceContract(unittest.TestCase):
    def check(self, data, files):
        v.validate(data, files.__getitem__, {'fixtureOnly': True})

    def test_both_positive_unit_specimens(self):
        for failure in (True, False):
            self.check(*specimen(failure))

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
            lambda d: d['worker']['pause'].update(connections=0),
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
        for name in ('failure', 'cancel'):
            source = (root / f'web/e2e/user-asset-export-v3-{name}.spec.ts').read_text()
            self.assertNotIn('route.fulfill', source)
            self.assertNotIn('waitForTimeout', source)
            self.assertIn('test.setTimeout(900_000)', source)
        self.assertIn('finally { await restoreOwnedExportWorker(); }', (root / 'web/e2e/user-asset-export-v3-cancel.spec.ts').read_text())


if __name__ == '__main__':
    unittest.main()
