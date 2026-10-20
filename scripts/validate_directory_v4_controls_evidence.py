"""Closed, exact-checkout dictionary4 controls evidence. Not product acceptance.

Invoked by CI only after the owned runner has exited successfully, including its
finally cleanup. No raw logs, requests, credentials or opener tuples are copied.
"""
import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess

from e2e_runtime_diagnostics import validate_report
from validate_group_members_v4_evidence import validate_png

CONTRACT_PATH = 'scripts/fixtures/directory_v4_controls_evidence.json'
SHA256 = re.compile(r'[0-9a-f]{64}')
SHA1 = re.compile(r'[0-9a-f]{40}')
MAX_JSON = 1024 * 1024


def require(condition, label):
    if not condition:
        raise ValueError(label)


def exact(value, keys, label):
    require(type(value) is dict and set(value) == set(keys), label)


def integer(value, low=0, high=100000):
    return type(value) is int and low <= value <= high


def digest(value):
    return type(value) is str and SHA256.fullmatch(value) is not None


def parse_json(raw):
    def unique(pairs):
        out = {}
        for key, value in pairs:
            require(key not in out, 'duplicate JSON key')
            out[key] = value
        return out
    def invalid_constant(_):
        raise ValueError('nonfinite JSON number')
    require(len(raw) <= MAX_JSON, 'bounded JSON')
    return json.loads(raw, object_pairs_hook=unique, parse_constant=invalid_constant)


def contract(root):
    value = parse_json((root / CONTRACT_PATH).read_bytes())
    exact(value, ['schemaVersion', 'sourceRoots', 'sourceFiles', 'suites'], 'contract fields')
    require(type(value['schemaVersion']) is int and value['schemaVersion'] == 1, 'contract version')
    exact(value['suites'], ['controls', 'cancel', 'readers'], 'three suites')
    return value


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True, timeout=10).rstrip('\n')


def safe_path(path):
    return (type(path) is str and re.fullmatch(r'[A-Za-z0-9_./-]+', path) is not None
            and not path.startswith('/') and all(p not in {'', '.', '..'} for p in path.split('/'))
            and str(PurePosixPath(path)) == path)


def source_paths(paths, rules):
    selected = sorted(path for path in paths if path in rules['sourceFiles'] or any(path.startswith(prefix) for prefix in rules['sourceRoots']))
    require(0 < len(selected) <= 4096 and len(set(selected)) == len(selected), 'complete bounded source list')
    require(all(safe_path(path) for path in selected) and set(rules['sourceFiles']) <= set(selected), 'source paths')
    return selected


def read_file(root, path, maximum):
    require(safe_path(path), 'safe file path')
    file = root / path
    require(file.is_file() and not file.is_symlink() and file.resolve().is_relative_to(root.resolve()), 'regular owned file')
    require(not any(parent.is_symlink() for parent in file.parents if parent != root and parent.is_relative_to(root)), 'nonsymlink ancestors')
    require(file.stat().st_size <= maximum, 'bounded file')
    return file.read_bytes()


def expected_provenance(root, environ=None):
    env = os.environ if environ is None else environ
    require(env.get('GITHUB_ACTIONS') == 'true', 'CI provenance is required')
    checkout = git(root, 'rev-parse', 'HEAD')
    value = {'checkoutCommit': checkout, 'checkoutTree': git(root, 'rev-parse', 'HEAD^{tree}'),
             'eventHeadCommit': env.get('ADTR_EVENT_HEAD_SHA'), 'eventBaseCommit': env.get('ADTR_EVENT_BASE_SHA'),
             'workflowCommit': env.get('GITHUB_SHA'), 'runId': env.get('GITHUB_RUN_ID'),
             'runAttempt': env.get('GITHUB_RUN_ATTEMPT'), 'eventName': env.get('GITHUB_EVENT_NAME')}
    for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit']:
        require(type(value[key]) is str and SHA1.fullmatch(value[key]), 'commit/tree identity')
    require(value['checkoutCommit'] == value['workflowCommit'], 'actual tested checkout')
    for key in ['runId', 'runAttempt']:
        require(type(value[key]) is str and re.fullmatch(r'[1-9][0-9]*', value[key]), 'run/attempt identity')
    require(value['eventName'] in {'pull_request', 'push', 'merge_group', 'workflow_dispatch'}, 'event identity')
    rules = contract(root)
    require(not git(root, 'diff', 'HEAD', '--', *rules['sourceRoots'], *rules['sourceFiles']), 'modified tracked sources')
    tracked = git(root, 'ls-tree', '-r', '--name-only', '-z', 'HEAD').split('\0')
    paths = source_paths([path for path in tracked if path], rules)
    untracked = git(root, 'ls-files', '--others', '--exclude-standard', '-z').split('\0')
    require(not any(path and (path in rules['sourceFiles'] or any(path.startswith(prefix) for prefix in rules['sourceRoots'])) for path in untracked), 'untracked source')
    value['sourceDigests'] = {path: hashlib.sha256(read_file(root, path, 16 * 1024 * 1024)).hexdigest() for path in paths}
    return value


ORIGINAL = ['taskSHA256', 'domainSHA256', 'actorSHA256', 'sourceSHA256', 'keySHA256']


def original(value):
    exact(value, ORIGINAL, 'original identity fields')
    require(all(digest(item) for item in value.values()), 'original identity digests')


def same_source(a, b):
    require(all(a[key] == b[key] for key in ['domainSHA256', 'actorSHA256', 'sourceSHA256']), 'same original source/actor')


def mutations(value, expected):
    require(type(value) is list and len(value) == len(expected), 'actual mutation count')
    previous = 0
    for row, (operation, item) in zip(value, expected):
        exact(row, ['operation', 'dispatchMilliseconds', 'counterOffsetMilliseconds', 'currentProof', 'domainSHA256', 'sourceSHA256', 'keySHA256', 'taskSHA256'], 'mutation fields')
        require(row['operation'] == operation and row['currentProof'] is True, 'actual operation/current proof')
        require(integer(row['dispatchMilliseconds'], 1, 9007199254740991) and row['dispatchMilliseconds'] > previous, 'ordered dispatch times')
        previous = row['dispatchMilliseconds']
        require(integer(row['counterOffsetMilliseconds'], 0, 29999) and row['counterOffsetMilliseconds'] == previous % 30000, 'actual counter offset')
        for key in ['domainSHA256', 'sourceSHA256', 'keySHA256']:
            require(row[key] == (item[key] if operation == 'sync' else None), 'mutation original pins')
        require(row['taskSHA256'] == (item['taskSHA256'] if operation == 'cancel' else None), 'cancel exact original task')


def result(value, item):
    exact(value, ['absent', 'empty', 'values', 'observationSHA256', 'detailedGroups'], 'result fields')
    require(all(value[key] is True for key in ['absent', 'empty', 'values']), 'actual raw semantics')
    require(value['observationSHA256'] == item['taskSHA256'] and integer(value['detailedGroups'], 1, 25), 'original observation and details')


def receipt(value, item, status=200):
    keys = ['status', 'domainSHA256', 'keySHA256'] + (['taskSHA256'] if status == 200 else [])
    exact(value, keys, 'receipt fields')
    require(type(value['status']) is int and value['status'] == status, 'actual receipt status')
    require(all(value[key] == item[key] for key in keys if key != 'status'), 'original receipt identity')


def validate_controls(w):
    exact(w, ['first', 'second', 'retried', 'mutations', 'firstResult', 'secondResult', 'originalAfterNewerResult', 'retriedResult', 'secondReceipt', 'originalReceipt', 'absentReceipt', 'firstPersisted', 'lossBefore', 'lossAfter', 'lossIdentityBefore', 'lossIdentityAfter', 'absentPersisted', 'retryPersisted'], 'controls witnesses')
    first, second, retried = [w[key] for key in ['first', 'second', 'retried']]
    for item in [first, second, retried]:
        original(item)
        same_source(first, item)
    require(len({item['taskSHA256'] for item in [first, second, retried]}) == 3 and len({item['keySHA256'] for item in [first, second, retried]}) == 3, 'three distinct admitted operations')
    mutations(w['mutations'], [('sync', first), ('sync', second), ('sync', retried), ('sync', retried)])
    for key, item in [('firstResult', first), ('secondResult', second), ('originalAfterNewerResult', first), ('retriedResult', retried)]:
        result(w[key], item)
    require(w['firstResult'] == w['originalAfterNewerResult'], 'newer success never replaces original')
    receipt(w['secondReceipt'], second); receipt(w['originalReceipt'], first); receipt(w['absentReceipt'], retried, 404)
    for key, item, count in [('firstPersisted', first, 1), ('lossBefore', second, 1), ('lossAfter', second, 1), ('absentPersisted', retried, 0), ('retryPersisted', retried, 1)]:
        persisted_key(w[key], item, count)
    require(w['lossBefore'] == w['lossAfter'], 'receipt recovery preserves original persisted key/count/body')
    for key in ['lossIdentityBefore', 'lossIdentityAfter']:
        ledger(w[key], second, 'quiesced', 'succeeded', 'executor_returned', 1, 0, 3, observations=1)
        require(w[key]['progress'] == 100, 'persisted successful original')
    require(w['lossIdentityBefore'] == w['lossIdentityAfter'], 'original source/immutable/opener identity unchanged')


def persisted_key(value, item, count):
    exact(value, [*ORIGINAL, 'taskState', 'tasks', 'uses', 'observations', 'observationSHA256', 'identityMatched'], 'persisted key fields')
    require(all(value[key] == item[key] for key in ORIGINAL if key != 'taskSHA256'), 'persisted original key/source/actor')
    require(all(type(value[key]) is int and value[key] == count for key in ['tasks', 'uses', 'observations']) and value['identityMatched'] is True, 'exact persisted admission cardinality')
    require(value['taskSHA256'] == (item['taskSHA256'] if count else None) and value['taskState'] == ('succeeded' if count else None), 'persisted original task')
    require(digest(value['observationSHA256']) if count else value['observationSHA256'] is None, 'persisted observation body digest')


LEDGER = ['taskSHA256', 'domainSHA256', 'actorSHA256', 'sourceSHA256', 'identitySHA256', 'openerSHA256',
          'state', 'reason', 'taskState', 'attempt', 'progress', 'dependencies', 'observations',
          'bindingPreserved', 'identityMatched', 'liveOpenerMatched', 'submittedEvents', 'cancelledEvents',
          'executionEvents', 'matchedDependencies', 'bindingDependencies', 'otherEvents', 'fencingZero']


def ledger(value, item, state, task_state, reason, attempt, cancelled, executions, observations=0):
    exact(value, LEDGER, 'ledger witness fields')
    require(all(value[key] == item[key] for key in ORIGINAL[:4]), 'ledger original identity')
    require(digest(value['identitySHA256']), 'immutable tuple digest')
    require(value['state'] == state and value['taskState'] == task_state and value['reason'] == reason, 'ledger state/reason')
    for key in ['attempt', 'progress', 'dependencies', 'observations', 'submittedEvents', 'cancelledEvents', 'executionEvents', 'matchedDependencies', 'bindingDependencies', 'otherEvents']:
        require(integer(value[key], 0, 100000), 'ledger integer counts')
    require(value['attempt'] == attempt and value['progress'] <= 100 and value['observations'] == observations and value['otherEvents'] == 0, 'ledger attempt/progress/no observations')
    require(value['submittedEvents'] == 1 and value['cancelledEvents'] == cancelled and value['executionEvents'] >= executions, 'actual event counts')
    require(value['dependencies'] == value['matchedDependencies'] == (0 if state == 'quiesced' else 1), 'actual scoped dependency release')
    require(value['bindingDependencies'] == 1 and value['bindingPreserved'] is True and value['identityMatched'] is True, 'binding and pinned identity')
    require(type(value['liveOpenerMatched']) is bool and type(value['fencingZero']) is bool, 'ledger booleans')
    if attempt == 0:
        require(value['openerSHA256'] is None and value['fencingZero'] is True and value['executionEvents'] == 0 and value['liveOpenerMatched'] is False, 'queued never opened')
    else:
        require(digest(value['openerSHA256']) and value['fencingZero'] is False, 'original opener digest')
        require(value['liveOpenerMatched'] is (state == 'opened'), 'live opener and released lease')


def timestamp(value):
    require(type(value) is str and re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z', value), 'UTC timestamp')
    return datetime.fromisoformat(value.replace('Z', '+00:00'))


def validate_cancel(w):
    checkpoints = ['queuedReserved', 'queuedCancelledBeforeWorker', 'queuedQuiesced', 'queuedAfterRunning', 'queuedBeforeCleanup', 'runningOpened', 'runningReturned']
    exact(w, ['queued', 'running', 'queuedReceipt', 'runningReceipt', 'mutations', 'runningAccepted', 'heartbeat', *checkpoints], 'cancel witnesses')
    queued, running = w['queued'], w['running']
    original(queued); original(running); same_source(queued, running)
    receipt(w['queuedReceipt'], queued); receipt(w['runningReceipt'], running)
    require(queued['taskSHA256'] != running['taskSHA256'] and queued['keySHA256'] != running['keySHA256'], 'two original cancellations')
    mutations(w['mutations'], [('sync', queued), ('cancel', queued), ('sync', running), ('cancel', running)])
    dispatch, cancel = w['mutations'][2:]
    require(26000 <= dispatch['counterOffsetMilliseconds'] < 29000 and 0 < cancel['dispatchMilliseconds'] - dispatch['dispatchMilliseconds'] < 10000, 'genuine bounded late admission/cancel')
    require(cancel['dispatchMilliseconds'] // 30000 > dispatch['dispatchMilliseconds'] // 30000, 'next current counter')
    ledger(w['queuedReserved'], queued, 'reserved', 'queued', None, 0, 0, 0)
    ledger(w['queuedCancelledBeforeWorker'], queued, 'reserved', 'cancelled', None, 0, 1, 0)
    for key in ['queuedQuiesced', 'queuedAfterRunning', 'queuedBeforeCleanup']:
        ledger(w[key], queued, 'quiesced', 'cancelled', 'never_opened_terminal', 0, 1, 0)
    require(len({w[key]['identitySHA256'] for key in checkpoints[:5]}) == 1, 'queued immutable identity at every checkpoint')
    ledger(w['runningOpened'], running, 'opened', 'running', None, 1, 0, 2)
    ledger(w['runningReturned'], running, 'quiesced', 'cancelled', 'executor_returned', 1, 1, 3)
    for key in ['identitySHA256', 'openerSHA256']:
        require(w['runningOpened'][key] == w['runningReturned'][key], 'same original executor returned')
    require(w['runningReturned']['executionEvents'] > w['runningOpened']['executionEvents'], 'actual finish event')
    accepted = w['runningAccepted']
    exact(accepted, ['state', 'attempt', 'progress'], 'running acceptance')
    require(accepted['state'] == 'cancel_requested' and type(accepted['attempt']) is int and accepted['attempt'] == 1 and integer(accepted['progress'], 42, 94), 'genuine in-flight cancellation')
    heartbeat = w['heartbeat']
    exact(heartbeat, ['firstVersion', 'laterVersion', 'firstUpdatedAt', 'laterUpdatedAt', 'firstProgress', 'laterProgress'], 'heartbeat fields')
    require(integer(heartbeat['firstVersion'], 1, 9007199254740991) and type(heartbeat['laterVersion']) is int and heartbeat['firstVersion'] == heartbeat['laterVersion'], 'unchanged semantic version')
    require(timestamp(heartbeat['laterUpdatedAt']) > timestamp(heartbeat['firstUpdatedAt']), 'actual heartbeat advanced')
    require(type(heartbeat['firstProgress']) is int and type(heartbeat['laterProgress']) is int and heartbeat['firstProgress'] == heartbeat['laterProgress'] == 41, 'stable page-two proof interval')


def validate_readers(w):
    exact(w, ['original', 'mutations', 'readerActorSHA256', 'readerMenu', 'readerObservationSHA256', 'countsBefore', 'countsAfter', 'denialStatuses'], 'reader witnesses')
    item = w['original']; original(item)
    mutations(w['mutations'], [('sync', item)])
    require(digest(w['readerActorSHA256']) and w['readerActorSHA256'] != item['actorSHA256'], 'actual distinct reader')
    require(w['readerObservationSHA256'] == item['taskSHA256'], 'reader original observation')
    require(type(w['readerMenu']) is list and len(w['readerMenu']) == 2, 'read-only menu count')
    for row, mark in zip(w['readerMenu'], ['directory_assets', 'domains']):
        exact(row, ['mark', 'readable', 'writeable'], 'reader menu fields')
        require(row['mark'] == mark and row['readable'] is True and row['writeable'] is False, 'actual read-only authority without tasks')
    require(type(w['denialStatuses']) is list and len(w['denialStatuses']) == 2 and all(type(status) is int and status == 403 for status in w['denialStatuses']), 'actual denied harness probes')
    for phase in ['countsBefore', 'countsAfter']:
        counts = w[phase]
        exact(counts, ['taskSHA256', 'domainSHA256', 'readerActorSHA256', 'originalState', 'originalResultVersion', 'observationSHA256', 'tasks', 'uses', 'dependencies', 'observations', 'readerTasks', 'readerUses'], 'reader persisted counts')
        require(counts['taskSHA256'] == item['taskSHA256'] and counts['domainSHA256'] == item['domainSHA256'] and counts['readerActorSHA256'] == w['readerActorSHA256'], 'reader count scope')
        require(counts['originalState'] == 'succeeded' and type(counts['originalResultVersion']) is str and re.fullmatch(r'(0|[1-9][0-9]{0,18})', counts['originalResultVersion']) and digest(counts['observationSHA256']), 'original stored observation/version')
        for key in ['tasks', 'uses', 'dependencies', 'observations', 'readerTasks', 'readerUses']:
            require(integer(counts[key]), 'stored integer counts')
        require(all(counts[key] >= 1 for key in ['tasks', 'uses', 'observations']) and counts['readerTasks'] == counts['readerUses'] == 0, 'no reader tasks or uses')
    require(w['countsBefore'] == w['countsAfter'], 'reads and denial probes preserve persisted state')


def validate_cleanup(value, suite):
    validate_report(value)  # Existing safe diagnostic schema remains unchanged.
    phases = ['setup', 'api-start', 'api-ready']
    if suite != 'cancel':
        phases += ['worker-start', 'worker-ready']
    phases += ['browser-start', 'browser-pass', 'before-cleanup', 'after-cleanup']
    require([row['phase'] for row in value['phases']] == phases, 'successful browser then completed owned cleanup')
    require([row['at_ms'] for row in value['phases']] == sorted(row['at_ms'] for row in value['phases']), 'ordered cleanup phases')
    require(value['http_panic'] is False and value['collection_errors'] == [], 'panic-free complete diagnostics')
    for process in value['processes'].values():
        require(process['started'] is True and process['before_cleanup'] is None and type(process['after_cleanup']) is int and process['after_cleanup'] == 0 and process['forced_kill'] is False, 'owned process graceful zero exit')
    require(value['browser'] == [] and value['browser_capture'] == 'pending' and value['browser_dropped'] == 0, 'unchanged raw runtime diagnostics')


def validate(data, expected, rules, suite, read_screenshot):
    exact(data, ['schemaVersion', 'suite', 'checks', 'witnesses', 'provenance', 'screenshots'], 'scenario fields')
    require(type(data['schemaVersion']) is int and data['schemaVersion'] == 1 and data['suite'] == 'directory-v4-' + suite, 'scenario identity')
    exact(data['provenance'], expected.keys(), 'provenance fields')
    require(data['provenance'] == expected, 'exact checkout/tree/event/run/attempt/source parity')
    definition = rules['suites'][suite]
    exact(data['checks'], definition['checks'], 'closed scenario checks')
    for key, wanted in definition['checks'].items():
        value = data['checks'][key]
        require((type(value) is str and value in wanted) if type(wanted) is list else type(value) is type(wanted) and value == wanted, 'actual check ' + key)
    {'controls': validate_controls, 'cancel': validate_cancel, 'readers': validate_readers}[suite](data['witnesses'])
    exact(data['screenshots'], definition['screenshots'], 'screenshot allowlist')
    for name, (width, height) in definition['screenshots'].items():
        entry = data['screenshots'][name]
        exact(entry, ['sha256', 'bytes', 'width', 'height'], 'screenshot metadata')
        require(digest(entry['sha256']) and integer(entry['bytes'], 24, 10000000) and type(entry['width']) is int and type(entry['height']) is int and (entry['width'], entry['height']) == (width, height), 'screenshot bounds')
        raw = read_screenshot(name)
        require(len(raw) == entry['bytes'] and hashlib.sha256(raw).hexdigest() == entry['sha256'], 'screenshot bytes/digest')
        validate_png(raw, width, height)


def publish(root, suite):
    rules = contract(root)
    expected = expected_provenance(root)
    output = root / 'web/test-results'
    require(output.is_dir() and not output.is_symlink(), 'fixed result root')
    entries = list(output.rglob('*'))
    require(len(entries) <= 1000 and not any(path.is_symlink() for path in entries), 'bounded nonsymlink output')
    files = [path for path in entries if path.name == f'directory-v4-{suite}-scenario.json']
    require(len(files) == 1 and files[0].is_file(), 'unique exact scenario')
    scenario = files[0]
    raw = read_file(root, scenario.relative_to(root).as_posix(), MAX_JSON)
    data = parse_json(raw)
    screenshots = {}
    def read_screenshot(name):
        value = read_file(root, (scenario.parent / name).relative_to(root).as_posix(), 10000000)
        screenshots[name] = value
        return value
    validate(data, expected, rules, suite, read_screenshot)
    cleanup = parse_json(read_file(root, '.e2e-diagnostics/runtime.json', 65536))
    validate_cleanup(cleanup, suite)
    destination = root / f'web/playwright-report/directory-v4-{suite}-validated'
    require(not destination.exists() and not destination.is_symlink(), 'fresh validated output only')
    require(destination.resolve().is_relative_to(root.resolve()) and not any(parent.is_symlink() for parent in destination.parents if parent != root and parent.is_relative_to(root)), 'owned validated destination')
    destination.mkdir(parents=True)
    # All validation precedes publication. Fixed basenames, never raw-directory globs.
    artifact = {**data, 'cleanup': cleanup}
    (destination / 'evidence.json').write_text(json.dumps(artifact, sort_keys=True, indent=2) + '\n')
    for name, value in screenshots.items():
        (destination / name).write_bytes(value)
    print(f'Validated directory-v4-{suite}: exact checkout/tree/run/attempt, witnesses, PNG bytes and owned cleanup')


NATIVE_KINDS = {'held-eof', 'held-second-chunk', 'abort', 'truncated', 'byte-cap'}
NATIVE_READERS = {'directory-v2': 'web/src/directory-v2-json.ts', 'directory-v4-controls': 'web/src/directory-v4-controls-json.ts'}


def validate_native(data, expected, mode):
    exact(data, ['kind', 'mode', 'stage', 'pwTerminalCount', 'nativeTerminalCount', 'browser', 'productionReader', 'provenance', 'parserSHA256', 'parserBundleSHA256', 'requestCount', 'nativeRequestCount', 'server', 'pageObservation', 'page', 'expectedSHA256', 'playwright', 'native'], 'native fields')
    require(data['productionReader'] in NATIVE_READERS and data['kind'] in NATIVE_KINDS and data['mode'] == mode, 'native case identity')
    require(data['provenance'] == expected and data['parserSHA256'] == expected['sourceDigests'][NATIVE_READERS[data['productionReader']]] and digest(data['parserBundleSHA256']), 'direct production reader source binding')
    require(data['browser'] == ('141.0.7390.37' if mode == 'stable-body' else '157.0.8092.0') and data['stage'] == 'terminal-before-assertions' and data['pageObservation'] == 'complete', 'actual engine/settled stage')
    require(all(type(data[key]) is int and data[key] == 1 for key in ['pwTerminalCount', 'nativeTerminalCount', 'requestCount', 'nativeRequestCount']), 'single original request and terminals')
    server, page = data['server'], data['page']
    exact(server, ['requests', 'releaseCalls', 'pendingReadWitnessed', 'bytesWritten', 'finished', 'prematureClose'], 'native server fields')
    require(type(server['requests']) is int and type(server['releaseCalls']) is int and server['requests'] == server['releaseCalls'] == 1 and server['pendingReadWitnessed'] is True and integer(server['bytesWritten'], 1, 8 * 1024 * 1024 + 2), 'actual server gate')
    require(type(server['finished']) is bool and type(server['prematureClose']) is bool, 'native server flags')
    exact(page, ['fulfilled', 'error', 'readers', 'reads', 'observedBytes', 'retainedBytes', 'rawDone', 'eof', 'cancelCalls', 'bodyCancelCalls', 'cancelBeforeEOF', 'released', 'aborted', 'locked', 'limit', 'status', 'cache', 'sha256'], 'native original reader fields')
    for key in ['readers', 'reads', 'observedBytes', 'retainedBytes', 'cancelCalls', 'bodyCancelCalls', 'released', 'limit', 'status']:
        require(integer(page[key], 0, 16 * 1024 * 1024), 'native integer')
    for key in ['fulfilled', 'rawDone', 'eof', 'cancelBeforeEOF', 'aborted', 'locked']:
        require(type(page[key]) is bool, 'native boolean')
    require(page['readers'] == page['released'] == 1 and page['reads'] >= 2 and page['locked'] is False and page['limit'] == 8 * 1024 * 1024 and page['status'] == 200 and page['cache'] == 'no-store' and digest(page['sha256']), 'original bounded no-store reader')
    pw, cdp = data['playwright'], data['native']
    for terminal in [pw, cdp]:
        exact(terminal, ['event', 'failure', 'cancelled'], 'native terminal fields')
        require(terminal['event'] in {'finished', 'failed'} and terminal['failure'] in {None, 'net::ERR_ABORTED', 'net::ERR_CONTENT_LENGTH_MISMATCH'} and (terminal['cancelled'] is None or type(terminal['cancelled']) is bool), 'native terminal enum')
    require(pw['event'] == cdp['event'] and pw['failure'] == cdp['failure'] and pw['cancelled'] is None, 'independent terminal agreement')
    payload = json.dumps({'marker': 'synthetic-stream', 'value': '😀', 'count': 7}, ensure_ascii=False, separators=(',', ':')).encode()
    expected_hash = hashlib.sha256(payload).hexdigest()
    require(data['expectedSHA256'] == expected_hash, 'native synthetic byte identity')
    kind = data['kind']
    if kind in {'held-eof', 'held-second-chunk'}:
        require(page['fulfilled'] is True and page['error'] is None and page['rawDone'] is True and page['eof'] is True and page['observedBytes'] == page['retainedBytes'] == len(payload) and page['sha256'] == expected_hash and page['cancelCalls'] == page['bodyCancelCalls'] == 0 and page['cancelBeforeEOF'] is False and page['aborted'] is False, 'complete uncancelled original body')
        require(server['finished'] is True and server['prematureClose'] is False and server['bytesWritten'] == len(payload), 'positive server completion')
        finished = pw['event'] == 'finished' and pw['failure'] is None and cdp['cancelled'] is None
        baseline_defect = mode == 'stable-body' and pw['event'] == 'failed' and pw['failure'] == 'net::ERR_ABORTED' and cdp['cancelled'] is True
        require(finished or baseline_defect, 'native outcome policy; never relabel a failed request')
    else:
        require(page['fulfilled'] is False and page['eof'] is False and page['cancelCalls'] > 0 and page['cancelBeforeEOF'] is True, 'genuine rejected original read')
        if kind == 'byte-cap':
            require(page['error'] == 'directory_limit_exceeded' and page['aborted'] is False and page['observedBytes'] > page['limit'] and page['retainedBytes'] <= page['limit'] and server['bytesWritten'] == page['limit'] + 2, 'actual byte cap')
            require((pw['event'] == 'finished' and pw['failure'] is None) or (pw['event'] == 'failed' and pw['failure'] == 'net::ERR_ABORTED'), 'cap actual native terminal')
        else:
            abort = kind == 'abort'
            require(server['prematureClose'] is True and pw['event'] == 'failed' and pw['failure'] == ('net::ERR_ABORTED' if abort else 'net::ERR_CONTENT_LENGTH_MISMATCH') and page['aborted'] is abort and cdp['cancelled'] is abort and page['error'] == ('AbortError' if abort else 'invalid_response'), 'actual abort/truncated transport')
            retained = payload[:6] if abort else payload
            require(page['observedBytes'] == page['retainedBytes'] == len(retained) and page['sha256'] == hashlib.sha256(retained).hexdigest(), 'actual failed-read bytes')


def validate_native_files(root):
    expected = expected_provenance(root)
    mode = os.environ.get('ADTR_E2E_NATIVE_STREAM_MODE')
    require(mode in {'stable-body', 'native-finish'}, 'explicit engine mode')
    output = root / 'web/test-results'
    require(output.is_dir() and not output.is_symlink(), 'fixed native root')
    entries = list(output.rglob('*'))
    require(len(entries) <= 1000 and not any(path.is_symlink() for path in entries), 'bounded native output')
    files = [path for path in entries if path.name == 'native-no-store-completion.json']
    require(len(files) == 10, 'all five legacy and five dictionary4 cases')
    seen = set()
    for path in files:
        data = parse_json(read_file(root, path.relative_to(root).as_posix(), MAX_JSON))
        validate_native(data, expected, mode)
        identity = (data['productionReader'], data['kind'])
        require(identity not in seen, 'unique native case')
        seen.add(identity)
    require(seen == {(reader, kind) for reader in NATIVE_READERS for kind in NATIVE_KINDS}, 'complete native cross product')
    print('Validated both direct production readers in all five native cases for ' + mode)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    parser.add_argument('--native', action='store_true')
    args = parser.parse_args()
    root = args.source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    if args.native:
        validate_native_files(root)
        return
    suite = os.environ.get('ADTR_V4_EVIDENCE_SUITE', '')
    require(suite in {'directory-v4-controls', 'directory-v4-cancel', 'directory-v4-readers'}, 'exact CI suite')
    publish(root, suite.removeprefix('directory-v4-'))


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, TypeError, KeyError, subprocess.SubprocessError):
        raise SystemExit('Dictionary4 evidence rejected; no validated artifact is eligible for upload') from None
