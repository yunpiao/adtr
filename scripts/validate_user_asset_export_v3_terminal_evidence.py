"""Strict offline checks for isolated failure/recovery and queued-cancel CI artifacts.

Unit specimens are never acceptance evidence. Success XLSX cells are independently
reconstructed from raw PostgreSQL observation bytes, not the exporter response.
The original all-matched/empty acceptance gate is unchanged.
"""
import argparse
from decimal import Decimal
from pathlib import Path
import re
import json
import validate_user_asset_export_v3_evidence as base
from validate_user_asset_export_v3_evidence import require, exact, sha, strict_json, source_rows, matched_rows, workbook_rows, validate_png, no_secrets, decode_bytes, COLUMNS

SUITES = {'user-asset-export-v3-failure': 'failureRecovery', 'user-asset-export-v3-cancel': 'queuedCancel'}
SAFE_COLUMNS = ['sAMAccountName', 'guid']
ALL_COLUMNS = ['guid', 'sAMAccountName', 'sid', 'mail', 'dn', 'whenCreated']
COMMON_CASE = {'name', 'idempotencyKey', 'request', 'create', 'receipt', 'detail', 'database', 'screenshot'}
DB_KEYS = {'taskID', 'actorId', 'kind', 'state', 'error', 'attempt', 'maxAttempts', 'progress', 'resultVersion', 'result', 'idempotencyKey', 'leaseOwner', 'leaseUntil', 'snapshotCount', 'manifestCount', 'chunkCount', 'events'}


def identity(value):
    return type(value) is str and re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', value) is not None


def screenshot(case, name, read_artifact):
    image = case['screenshot']
    exact(image, ['filename', 'sha256'], 'screenshot reference')
    require(image['filename'] == name, 'fixed screenshot filename')
    raw = read_artifact(name)
    require(sha(raw) == image['sha256'], 'screenshot SHA')
    validate_png(raw)


def request_bound(request, source, rows):
    matched = matched_rows(rows, request)
    require(request['observationId'] == source['taskUUID'], 'original observation')
    require(request['domainId'] == source['selection']['domainId'] and request['expectedRevision'] == source['selection']['revision'] and request['expectedCredentialRevision'] == source['selection']['credentialRevision'], 'original source pins')
    require(request['search'] == 'user-' and request['creatTm'] == 'desc' and len(matched) == 12, 'full matching source rows')
    return matched


def responses(case, source, rows):
    request_bound(case['request'], source, rows)
    task_id = case['create']['taskID']
    require(identity(task_id) and identity(case['idempotencyKey']), 'task/key identity')
    require(case['create']['replayed'] is False, 'new operation')
    for key in ('create', 'receipt', 'detail'):
        response = case[key]
        require(response['taskID'] == task_id and response['request'] == case['request'], 'immutable response identity/request')
        require(response['task']['taskUUID'] == task_id and response['task']['taskName'] == 'user_assets.v3.export', 'response task kind')
    return task_id


def unavailable(value):
    exact(value, ['status', 'error', 'cacheControl'], 'download rejection fields')
    require(type(value['status']) is int and value == {'status': 409, 'error': 'export_not_ready', 'cacheControl': 'no-store'}, 'real download rejected')


def terminal_response(response, request, task_id, state, error):
    require(response['taskID'] == task_id and response['request'] == request, 'terminal receipt identity')
    task = response['task']
    require(task['taskUUID'] == task_id and task['taskName'] == 'user_assets.v3.export' and task['state'] == state and task['error'] == error, 'terminal response task')
    if 'downloadReady' in response:
        require(response['downloadReady'] is False and response['downloadStatus'] == state and response.get('artifact') is None, 'no terminal artifact')


def durable(database, case, actor, state):
    exact(database, DB_KEYS, 'terminal database fields')
    require(database['taskID'] == case['create']['taskID'] and database['idempotencyKey'] == case['idempotencyKey'] and database['actorId'] == str(actor) and database['kind'] == 'user_assets.v3.export', 'durable owner/task/key')
    require(all(type(database[k]) is int for k in ('attempt', 'maxAttempts', 'progress', 'resultVersion', 'snapshotCount', 'manifestCount', 'chunkCount')), 'integer task and cleanup counts')
    require(database['state'] == state and database['maxAttempts'] == 3, 'durable task state/policy')
    error = {'failed': 'export_invalid_text', 'cancelled': 'cancelled', 'queued': ''}[state]
    require(database['error'] == error and database['attempt'] == (1 if state == 'failed' else 0), 'durable error/attempt')
    require(database['snapshotCount'] == database['manifestCount'] == database['chunkCount'] == 0, 'all artifact tables empty across fencing tokens')
    require(database['result'] == {} and database['leaseOwner'] == '' and database['leaseUntil'] is None, 'no artifact result or outstanding lease')
    require(0 <= database['progress'] < 100 and database['resultVersion'] >= (0 if state == 'queued' else 1), 'terminal non-success progress/version')
    events = database['events']
    require(type(events) is list and len(events) > 0, 'durable lifecycle events')
    for event in events:
        exact(event, ['action', 'state', 'attempt', 'resultVersion'], 'event fields')
        require(type(event['attempt']) is int and type(event['resultVersion']) is int, 'event integers')
    require(events[-1]['state'] == state and events[-1]['resultVersion'] == database['resultVersion'], 'last event matches durable state')
    require([e['resultVersion'] for e in events] == sorted(e['resultVersion'] for e in events), 'ordered durable versions')
    actions = [e['action'] for e in events]
    if state == 'failed':
        require(all(a in actions for a in ('submitted', 'leased', 'started', 'finished')) and actions[-1] == 'finished', 'actual Worker failure')
        require(not set(actions) & {'cancel_requested', 'recovered'}, 'unmodified failed operation')
    else:
        require(all(e['attempt'] == 0 for e in events), 'never executed queued operation')
        require(actions == (['submitted'] if state == 'queued' else ['submitted', 'cancel_requested']), 'queued cancellation lifecycle only')


def terminal_case(case, data, rows, read_artifact):
    failure = data['suite'] == 'failureRecovery'
    state, error = ('failed', 'export_invalid_text') if failure else ('cancelled', 'cancelled')
    extras = {'unavailableDownload', 'ui', 'afterRecovery'} if failure else {'unavailableDownload', 'ui', 'queued', 'cancel', 'postResume'}
    exact(case, COMMON_CASE | extras, 'terminal case fields')
    require(case['name'] == state, 'terminal case name')
    task_id = responses(case, data['source'], rows)
    require(case['request']['screenColumns'] == (ALL_COLUMNS if failure else SAFE_COLUMNS), 'terminal selected columns')
    for key in ('receipt', 'detail'):
        terminal_response(case[key], case['request'], task_id, state, error)
    require('downloadReady' in case['detail'], 'terminal detail readiness')
    durable(case['database'], case, data['actorId'], state)
    unavailable(case['unavailableDownload'])
    exact(case['ui'], ['downloadButtons', 'downloadEvents', 'error'] if failure else ['downloadButtons', 'downloadEvents'], 'terminal UI fields')
    require(type(case['ui']['downloadButtons']) is int and type(case['ui']['downloadEvents']) is int and case['ui']['downloadButtons'] == case['ui']['downloadEvents'] == 0, 'no UI button or actual browser download')
    screenshot(case, f'{state}-terminal.png', read_artifact)
    if failure:
        require(case['ui']['error'] == error, 'specific visible failure code')
        exact(case['afterRecovery'], ['receipt', 'database'], 'old failed receipt after success')
        terminal_response(case['afterRecovery']['receipt'], case['request'], task_id, state, error)
        require(case['afterRecovery']['database'] == case['database'], 'failed task unchanged after independent recovery')
    else:
        require(case['create']['task']['state'] == 'queued', 'created while Worker paused')
        durable(case['queued'], case, data['actorId'], 'queued')
        require(case['database']['resultVersion'] == case['queued']['resultVersion'] + 1, 'single ordinary queued cancellation transition')
        exact(case['cancel'], ['taskID', 'task'], 'cancel response fields')
        require(case['cancel']['taskID'] == task_id and case['cancel']['task']['taskUUID'] == task_id and case['cancel']['task']['taskName'] == 'user_assets.v3.export' and case['cancel']['task']['state'] == 'cancelled', 'ordinary cancellation response')
        post = case['postResume']
        exact(post, ['receipt', 'detail', 'database', 'unavailableDownload'], 'post-resume evidence')
        for key in ('receipt', 'detail'):
            terminal_response(post[key], case['request'], task_id, state, error)
        require('downloadReady' in post['detail'] and post['database'] == case['database'], 'no execution or artifact after actual queue cycle')
        unavailable(post['unavailableDownload'])
    return task_id


def worker_proof(worker):
    exact(worker, ['pause', 'resume'], 'owned Worker evidence')
    pause, resume = worker['pause'], worker['resume']
    exact(pause, ['pid', 'parentPID', 'starttime', 'binarySHA256', 'nonceSHA256', 'applicationNameSHA256', 'allThreadsStopped', 'threadCount', 'connections', 'activeTransactions', 'workerID', 'queueSuccessBefore'], 'pause proof')
    exact(resume, ['sameIdentity', 'allThreadsRunning', 'readyStatus', 'workerID', 'queueSuccessAfterFirst', 'queueSuccessAfter'], 'resume proof')
    require(all(type(pause[k]) is int and pause[k] > 0 for k in ('pid', 'parentPID', 'threadCount', 'connections')), 'owned Worker numeric identity')
    require(pause['pid'] > 1 and pause['parentPID'] > 1 and pause['pid'] != pause['parentPID'], 'owned child Worker')
    require(type(pause['starttime']) is str and re.fullmatch(r'[0-9]+', pause['starttime']), 'process birth identity')
    for key in ('binarySHA256', 'nonceSHA256', 'applicationNameSHA256'):
        require(type(pause[key]) is str and re.fullmatch(r'[0-9a-f]{64}', pause[key]), 'hashed owned Worker identity')
    require(pause['allThreadsStopped'] is True and type(pause['activeTransactions']) is int and pause['activeTransactions'] == 0, 'transaction-free complete stop')
    require(type(pause['workerID']) is str and pause['workerID'] and resume['workerID'] == pause['workerID'], 'same real queue worker')
    require(resume['sameIdentity'] is True and resume['allThreadsRunning'] is True and type(resume['readyStatus']) is int and resume['readyStatus'] == 200, 'verified resumed owned Worker')
    for value in (pause['queueSuccessBefore'], resume['queueSuccessAfterFirst'], resume['queueSuccessAfter']):
        require(type(value) is str and re.fullmatch(r'[0-9]+\.[0-9]+', value), 'database queue success timestamp')
    require(Decimal(resume['queueSuccessAfter']) > Decimal(resume['queueSuccessAfterFirst']) > Decimal(pause['queueSuccessBefore']), 'post-stop actual queue success after resume')


def success_case(case, data, rows, read_artifact):
    exact(case, COMMON_CASE | {'download', 'pageSize', 'firstPageCount'}, 'recovery success fields')
    require(case['name'] == 'recovery', 'recovery case')
    source, actor, request = data['source'], data['actorId'], case['request']
    task_id = responses(case, source, rows)
    expected = request_bound(request, source, rows)
    detail = case['detail']
    require(detail['task']['state'] == 'succeeded' and detail['downloadReady'] is True and detail['downloadStatus'] == 'ready', 'published ready state')
    artifact = detail['artifact']
    exact(artifact, ['rowCount', 'byteCount', 'sha256', 'filename'], 'artifact fields')
    filename = f'user-assets-v3-{task_id}.xlsx'
    require(artifact['filename'] == filename, 'fixed artifact filename')
    download = case['download']
    exact(download, ['filename', 'headers'], 'download evidence')
    require(download['filename'] == filename, 'browser download filename')
    raw = read_artifact(filename)
    require(type(artifact['byteCount']) is int and artifact['byteCount'] == len(raw) and artifact['sha256'] == sha(raw), 'whole artifact digest/length')
    require(type(artifact['rowCount']) is int and artifact['rowCount'] == len(expected), 'artifact row count')
    headers = download['headers']
    exact(headers, ['x-adtr-user-id', 'cache-control', 'x-content-type-options', 'content-type', 'content-length', 'x-adtr-artifact-sha256', 'x-adtr-row-count', 'content-disposition'], 'safe download headers')
    require(headers['content-disposition'] == f'attachment; filename="{filename}"', 'download disposition')
    require(headers['x-adtr-user-id'] == str(actor) and headers['cache-control'] == 'no-store' and headers['x-content-type-options'] == 'nosniff', 'download security headers')
    require(headers['content-type'] == 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'XLSX MIME')
    require(headers['content-length'] == str(len(raw)) and headers['x-adtr-artifact-sha256'] == sha(raw) and headers['x-adtr-row-count'] == str(len(expected)), 'download manifest headers')
    actual = workbook_rows(raw)
    require(actual == [[COLUMNS[c] for c in request['screenColumns']], *expected], 'full workbook cells/order/source match')
    require(type(case['pageSize']) is int and case['pageSize'] == 10, 'observed page capacity')
    require(type(case['firstPageCount']) is int and case['firstPageCount'] == min(10, len(expected)), 'first page size')
    require(len(expected) == 12 and request['screenColumns'] == SAFE_COLUMNS, 'ordered safe subset over all matched rows')
    stored = case['database']
    exact(stored, ['taskID', 'actorId', 'state', 'kind', 'progress', 'attempt', 'maxAttempts', 'sourceSHA256', 'rowCount', 'byteCount', 'sha256', 'chunkCount', 'chunks', 'events'], 'persisted export fields')
    require(all(type(stored[k]) is int for k in ('progress', 'attempt', 'maxAttempts', 'rowCount', 'byteCount', 'chunkCount')), 'integer manifest/task values')
    require(stored['taskID'] == task_id and stored['actorId'] == str(actor) and stored['state'] == 'succeeded' and stored['kind'] == 'user_assets.v3.export', 'durable export task')
    require(stored['sourceSHA256'] == source['sha256'] and stored['rowCount'] == len(expected) and stored['byteCount'] == len(raw) and stored['sha256'] == sha(raw), 'durable manifest/source')
    require(stored['progress'] == 100 and stored['attempt'] == 1 and stored['maxAttempts'] == 3, 'durable successful execution policy')
    events = stored['events']
    require(type(events) is list and all(type(e['resultVersion']) is int for e in events), 'task events')
    actions = [e['action'] for e in events]
    require(all(a in actions for a in ('submitted', 'leased', 'started', 'progress', 'finished')) and events[-1]['state'] == 'succeeded', 'durable Worker lifecycle')
    require([e['resultVersion'] for e in events] == sorted(e['resultVersion'] for e in events), 'monotonic durable versions')
    chunks = stored['chunks']
    require(type(chunks) is list and len(chunks) > 0 and stored['chunkCount'] == len(chunks), 'durable chunks')
    joined = bytearray()
    for ordinal, chunk in enumerate(chunks):
        exact(chunk, ['ordinal', 'bodyBase64', 'sha256'], 'chunk fields')
        require(type(chunk['ordinal']) is int, 'integer chunk ordinal')
        blob = decode_bytes(chunk['bodyBase64'])
        require(chunk['ordinal'] == ordinal and 0 < len(blob) <= 1 << 20 and sha(blob) == chunk['sha256'], 'chunk sequence/digest/bounds')
        joined.extend(blob)
    require(bytes(joined) == raw, 'download equals persisted chunks')
    screenshot(case, 'recovery-ready.png', read_artifact)
    return task_id


def validate(data, read_artifact, provenance, suite=None):
    exact(data, ['format', 'suite', 'provenance', 'source', 'actorId', 'sensitiveBudget', 'worker', 'cases'], 'terminal evidence envelope')
    require(type(data['format']) is int and data['format'] == 1, 'evidence version')
    require(data['suite'] in SUITES.values() and (suite is None or SUITES[suite] == data['suite']), 'isolated expected suite')
    require(data['provenance'] == provenance, 'trusted checkout/run/source provenance')
    no_secrets(data)
    failure = data['suite'] == 'failureRecovery'
    exact(data['sensitiveBudget'], ['before', 'after'], 'budget envelope')
    before = {'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}
    after = {**before, 'owner': 9, 'exportTasks': 2 if failure else 1}
    require(data['sensitiveBudget'] == {'before': before, 'after': after}, 'bounded MFA budget/no recollection')
    require(all(type(n) is int for point in data['sensitiveBudget'].values() for n in point.values()), 'integer budget')
    rows = source_rows(data['source'])
    require(type(data['actorId']) is int and data['actorId'] > 0 and str(data['actorId']) != data['source']['actorId'], 'independent owner')
    require(len(rows) == 12, 'real twelve-user source')
    require(type(data['cases']) is list and [c['name'] for c in data['cases']] == (['failed', 'recovery'] if failure else ['cancelled']), 'exact isolated cases')
    task_id = terminal_case(data['cases'][0], data, rows, read_artifact)
    if failure:
        require(data['worker'] is None, 'failure uses uninterrupted Worker')
        require(sum(row['mail'] == 'export-invalid\x01@example.test' for row in rows) == 1, 'actual poisoned LDAP source field')
        for row in rows:
            require(all(all(ch in '\t\r\n' or 0x20 <= ord(ch) <= 0x10ffff and ord(ch) not in (0xfffe, 0xffff) for ch in row[c]) for c in SAFE_COLUMNS), 'safe subset source fields')
        recovery = data['cases'][1]
        require(success_case(recovery, data, rows, read_artifact) != task_id, 'explicit independent recovery task')
        failed = data['cases'][0]
        require(recovery['idempotencyKey'] != failed['idempotencyKey'], 'new operation idempotency key')
        require(recovery['request'] == {**failed['request'], 'screenColumns': SAFE_COLUMNS}, 'same immutable source/filter with safe subset only')
    else:
        worker_proof(data['worker'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('evidence', type=Path, nargs='?')
    parser.add_argument('--suite', choices=SUITES, required=True)
    parser.add_argument('--repo', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--capture-provenance', action='store_true')
    parser.add_argument('--provenance', type=Path)
    args = parser.parse_args()
    if args.capture_provenance:
        print(json.dumps(base.checkout_provenance(args.repo), sort_keys=True))
        return
    require(args.evidence is not None and args.provenance is not None, 'evidence and independent provenance required')
    trusted = strict_json(args.provenance.read_bytes())
    require(trusted == base.checkout_provenance(args.repo, trusted['runID'], trusted['runAttempt']), 'trusted current checkout/run')
    validate(strict_json(args.evidence.read_bytes()), lambda filename: (args.evidence.parent / filename).read_bytes(), trusted, args.suite)
    print(f'{args.suite} evidence validated')


if __name__ == '__main__':
    main()
