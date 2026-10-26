"""Unit-only synthetic structures exercise rejection. They are never run evidence."""
import copy
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib

import validate_directory_v4_controls_evidence as v


def sha(label):
    return hashlib.sha256(label.encode()).hexdigest()


def original(label):
    return dict(taskSHA256=sha('task-' + label), domainSHA256=sha('domain'), actorSHA256=sha('producer'), sourceSHA256=sha('source'), keySHA256=sha('key-' + label))


def receipt(item, status=200):
    return {'status': status, **{key: item[key] for key in (['domainSHA256', 'keySHA256', 'taskSHA256'] if status == 200 else ['domainSHA256', 'keySHA256'])}}


def mutation(item, operation, at):
    return dict(operation=operation, dispatchMilliseconds=at, counterOffsetMilliseconds=at % 30000, currentProof=True,
                domainSHA256=item['domainSHA256'] if operation == 'sync' else None,
                sourceSHA256=item['sourceSHA256'] if operation == 'sync' else None,
                keySHA256=item['keySHA256'] if operation == 'sync' else None,
                taskSHA256=item['taskSHA256'] if operation == 'cancel' else None)


def result(item):
    return dict(absent=True, empty=True, values=True, observationSHA256=item['taskSHA256'], detailedGroups=12)


def ledger(item, state, task_state, reason, attempt, cancelled, executions, observations=0):
    return {**{key: item[key] for key in v.ORIGINAL[:4]}, 'identitySHA256': sha('identity' + item['taskSHA256']),
            'openerSHA256': sha('opener' + item['taskSHA256']) if attempt else None,
            'state': state, 'reason': reason, 'taskState': task_state, 'attempt': attempt, 'progress': 100 if task_state == 'succeeded' else 41 if attempt else 0,
            'dependencies': 0 if state == 'quiesced' else 1, 'observations': observations,
            'bindingPreserved': True, 'identityMatched': True, 'liveOpenerMatched': state == 'opened',
            'submittedEvents': 1, 'cancelledEvents': cancelled, 'executionEvents': executions,
            'matchedDependencies': 0 if state == 'quiesced' else 1, 'bindingDependencies': 1, 'otherEvents': 0, 'fencingZero': not bool(attempt)}


def persisted(item, count):
    return {**item, 'taskSHA256': item['taskSHA256'] if count else None, 'taskState': 'succeeded' if count else None,
            'tasks': count, 'uses': count, 'observations': count, 'observationSHA256': sha('stored-' + item['taskSHA256']) if count else None, 'identityMatched': True}


def witnesses(suite):
    a, b, c = [original(label) for label in ['a', 'b', 'c']]
    if suite == 'controls':
        identity = ledger(b, 'quiesced', 'succeeded', 'executor_returned', 1, 0, 5, 1)
        return dict(first=a, second=b, retried=c, mutations=[mutation(item, 'sync', at) for item, at in [(a, 30000), (b, 60000), (c, 90000), (c, 120000)]],
                    firstResult=result(a), secondResult=result(b), originalAfterNewerResult=result(a), retriedResult=result(c),
                    secondReceipt=receipt(b), originalReceipt=receipt(a), absentReceipt=receipt(c, 404),
                    firstPersisted=persisted(a, 1), lossBefore=persisted(b, 1), lossAfter=persisted(b, 1),
                    lossIdentityBefore=identity, lossIdentityAfter=copy.deepcopy(identity), absentPersisted=persisted(c, 0), retryPersisted=persisted(c, 1))
    if suite == 'cancel':
        quiesced = ledger(a, 'quiesced', 'cancelled', 'never_opened_terminal', 0, 1, 0)
        return dict(queued=a, running=b, queuedReceipt=receipt(a), runningReceipt=receipt(b),
                    mutations=[mutation(a, 'sync', 30000), mutation(a, 'cancel', 60000), mutation(b, 'sync', 87000), mutation(b, 'cancel', 92000)],
                    queuedReserved=ledger(a, 'reserved', 'queued', None, 0, 0, 0),
                    queuedCancelledBeforeWorker=ledger(a, 'reserved', 'cancelled', None, 0, 1, 0),
                    queuedQuiesced=quiesced, queuedAfterRunning=copy.deepcopy(quiesced), queuedBeforeCleanup=copy.deepcopy(quiesced),
                    runningOpened=ledger(b, 'opened', 'running', None, 1, 0, 4),
                    runningReturned=ledger(b, 'quiesced', 'cancelled', 'executor_returned', 1, 1, 5),
                    runningAccepted=dict(state='cancel_requested', attempt=1, progress=42),
                    heartbeat=dict(firstVersion=7, laterVersion=7, firstUpdatedAt='2026-10-20T12:00:00Z', laterUpdatedAt='2026-10-20T12:00:01Z', firstProgress=41, laterProgress=41))
    counts = dict(taskSHA256=a['taskSHA256'], domainSHA256=a['domainSHA256'], readerActorSHA256=sha('reader'), originalState='succeeded', originalResultVersion='7', observationSHA256=sha('stored'), tasks=2, uses=1, dependencies=1, observations=1, readerTasks=0, readerUses=0)
    return dict(original=a, mutations=[mutation(a, 'sync', 30000)], readerActorSHA256=sha('reader'),
                readerMenu=[dict(mark=mark, readable=True, writeable=False) for mark in ['directory_assets', 'domains']],
                readerObservationSHA256=a['taskSHA256'], countsBefore=counts, countsAfter=copy.deepcopy(counts), denialStatuses=[403, 403],
                identityWitness=dict(kind='split-app-account-and-workspace-v1', appPageRole='predeclared-account', workspacePageRole='original-workspace',
                                     samePageAtomicBinding=False, producerActorSHA256=a['actorSHA256'], appReaderActorSHA256=sha('reader'), workspaceReaderActorSHA256=sha('reader'),
                                     sessionContinuity='checked-identity-and-csrf-in-memory', identitySamples=4, browserAuthMutations=0,
                                     harnessAuthWrites='source-closed-none', auxiliaryPageCleanup='source-bound-after-finally'))


def png(width, height):
    def chunk(kind, payload):
        return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress((b'\0' + b'\0' * width * 3) * height)) + chunk(b'IEND', b'')


class DictionaryV4Evidence(unittest.TestCase):
    def setUp(self):
        self.rules = v.contract(Path('.'))
        self.expected = dict(checkoutCommit='a' * 40, checkoutTree='b' * 40, eventHeadCommit='c' * 40, eventBaseCommit='d' * 40, workflowCommit='a' * 40, runId='123', runAttempt='2', eventName='pull_request', sourceDigests={path: sha(path) for path in v.NATIVE_READERS.values()})

    def scenario(self, suite):
        rules = self.rules['suites'][suite]
        checks = {key: value[0] if type(value) is list else value for key, value in rules['checks'].items()}
        images = {name: png(*size) for name, size in rules['screenshots'].items()}
        entries = {name: dict(sha256=hashlib.sha256(raw).hexdigest(), bytes=len(raw), width=rules['screenshots'][name][0], height=rules['screenshots'][name][1]) for name, raw in images.items()}
        data = dict(schemaVersion=1, suite='directory-v4-' + suite, checks=checks, witnesses=witnesses(suite), provenance=copy.deepcopy(self.expected), screenshots=entries)
        return data, images

    def test_all_three_closed_positive_unit_structures(self):
        for suite in self.rules['suites']:
            with self.subTest(suite=suite):
                data, images = self.scenario(suite)
                v.validate(data, self.expected, self.rules, suite, images.__getitem__)

    def test_unknown_missing_duplicate_nonfinite_and_boolean_integer_fields(self):
        for raw in ['{"a":1,"a":1}', '{"a":{"key":0,"key":1}}', '{"a":NaN}', '{"a":Infinity}', '{"a":-Infinity}']:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                v.parse_json(raw)
        data, images = self.scenario('cancel')
        for key in data:
            bad = copy.deepcopy(data); del bad[key]
            with self.subTest(key=key), self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'cancel', images.__getitem__)
        for field, value in [('schemaVersion', True), ('suite', 'directory-v3-controls'), ('unexpected', 'secret')]:
            bad = copy.deepcopy(data); bad[field] = value
            with self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'cancel', images.__getitem__)
        for key, value in data['checks'].items():
            bad = copy.deepcopy(data); bad['checks'][key] = 1 if type(value) is bool else True
            with self.subTest(key=key), self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'cancel', images.__getitem__)

    def test_each_provenance_binding_and_missing_extra_source_is_rejected(self):
        data, images = self.scenario('cancel')
        for key in self.expected:
            bad = copy.deepcopy(data); bad['provenance'][key] = 'changed'
            with self.subTest(key=key), self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'cancel', images.__getitem__)
        for sources in [{}, {**self.expected['sourceDigests'], 'unexpected.ts': sha('x')}]:
            bad = copy.deepcopy(data); bad['provenance']['sourceDigests'] = sources
            with self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'cancel', images.__getitem__)

    def test_cancel_never_opened_exact_executor_and_count_negatives(self):
        source = witnesses('cancel')
        changes = [('queuedReserved', 'openerSHA256', sha('not-never-opened')),
                   ('queuedQuiesced', 'dependencies', 1), ('queuedAfterRunning', 'attempt', 1),
                   ('queuedBeforeCleanup', 'executionEvents', 1), ('queuedBeforeCleanup', 'identitySHA256', sha('another')),
                   ('runningOpened', 'liveOpenerMatched', False), ('runningReturned', 'openerSHA256', sha('replacement')),
                   ('runningReturned', 'identitySHA256', sha('other-pins')), ('runningReturned', 'reason', 'never_opened_terminal'),
                   ('runningReturned', 'observations', 1), ('runningReturned', 'bindingDependencies', 0),
                   ('runningReturned', 'matchedDependencies', 1), ('runningReturned', 'cancelledEvents', True),
                   ('runningAccepted', 'state', 'cancelled'), ('runningAccepted', 'progress', 95),
                   ('heartbeat', 'laterVersion', 8), ('heartbeat', 'laterUpdatedAt', '2026-10-20T12:00:00Z'),
                   ('heartbeat', 'laterProgress', 42)]
        for record, key, value in changes:
            bad = copy.deepcopy(source); bad[record][key] = value
            with self.subTest(record=record, key=key), self.assertRaises(ValueError):
                v.validate_cancel(bad)
        for index, field, value in [(2, 'counterOffsetMilliseconds', 25999), (3, 'dispatchMilliseconds', 100000), (3, 'currentProof', False), (3, 'taskSHA256', source['queued']['taskSHA256'])]:
            bad = copy.deepcopy(source); bad['mutations'][index][field] = value
            with self.subTest(index=index, field=field), self.assertRaises(ValueError):
                v.validate_cancel(bad)

    def test_controls_persisted_key_zero_to_one_and_original_result_negatives(self):
        source = witnesses('controls')
        for record, key, value in [('second', 'taskSHA256', source['first']['taskSHA256']), ('originalAfterNewerResult', 'observationSHA256', source['second']['taskSHA256']), ('absentPersisted', 'tasks', 1), ('retryPersisted', 'uses', 0), ('lossAfter', 'observationSHA256', sha('different-body')), ('lossIdentityAfter', 'identitySHA256', sha('other-pins')), ('secondReceipt', 'status', 404), ('absentReceipt', 'status', 200), ('retriedResult', 'values', False)]:
            bad = copy.deepcopy(source); bad[record][key] = value
            with self.subTest(record=record, key=key), self.assertRaises(ValueError):
                v.validate_controls(bad)
        bad = copy.deepcopy(source); bad['mutations'].append(copy.deepcopy(bad['mutations'][-1]))
        with self.assertRaises(ValueError):
            v.validate_controls(bad)

    def test_reader_denials_authority_and_persisted_counts_are_separate(self):
        source = witnesses('readers')
        changes = [('countsAfter', 'tasks', 3), ('countsAfter', 'readerTasks', 1), ('countsBefore', 'readerActorSHA256', sha('other-reader')), ('countsBefore', 'taskSHA256', sha('other-task')), ('countsBefore', 'domainSHA256', sha('other-domain')), ('countsAfter', 'observationSHA256', sha('replacement'))]
        for record, key, value in changes:
            bad = copy.deepcopy(source); bad[record][key] = value
            with self.subTest(record=record, key=key), self.assertRaises(ValueError):
                v.validate_readers(bad)
        for menu in [[dict(mark='tasks', readable=True, writeable=False), source['readerMenu'][1]], [dict(mark='directory_assets', readable=True, writeable=True), source['readerMenu'][1]]]:
            bad = copy.deepcopy(source); bad['readerMenu'] = menu
            with self.assertRaises(ValueError):
                v.validate_readers(bad)
        bad = copy.deepcopy(source); bad['denialStatuses'] = [403, 200]
        with self.assertRaises(ValueError):
            v.validate_readers(bad)
        data, images = self.scenario('readers')
        data['checks']['staleDelivery'] = 'aborted'
        v.validate(data, self.expected, self.rules, 'readers', images.__getitem__)
        data['checks']['staleDelivery'] = 'ignored_abort_proved'
        with self.assertRaises(ValueError):
            v.validate(data, self.expected, self.rules, 'readers', images.__getitem__)

    def test_reader_split_roles_session_and_non_atomic_claim_are_closed(self):
        source = witnesses('readers')
        for field in source['identityWitness']:
            bad = copy.deepcopy(source); del bad['identityWitness'][field]
            with self.subTest(missing=field), self.assertRaises(ValueError):
                v.validate_readers(bad)
        changes = [('kind', 'same-page-original-response-commit'), ('kind', 'unknown'),
                   ('appPageRole', 'late-selected-page'), ('workspacePageRole', 'replacement-workspace'),
                   ('samePageAtomicBinding', True), ('samePageAtomicBinding', 0),
                   ('producerActorSHA256', sha('other-producer')), ('appReaderActorSHA256', source['original']['actorSHA256']),
                   ('workspaceReaderActorSHA256', sha('other-reader')), ('sessionContinuity', 'same-cookie-bytes'),
                   ('identitySamples', 3), ('identitySamples', True), ('browserAuthMutations', 1), ('browserAuthMutations', False),
                   ('harnessAuthWrites', 'browser-listener-proves-all'), ('auxiliaryPageCleanup', 'already-closed')]
        for field, value in changes:
            bad = copy.deepcopy(source); bad['identityWitness'][field] = value
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                v.validate_readers(bad)
        for field in ['csrfToken', 'csrfSHA256', 'cookie', 'rawProfile', 'closed']:
            bad = copy.deepcopy(source); bad['identityWitness'][field] = 'not-allowed'
            with self.subTest(extra=field), self.assertRaises(ValueError):
                v.validate_readers(bad)
        bad = copy.deepcopy(source); del bad['identityWitness']
        with self.assertRaises(ValueError): v.validate_readers(bad)

    def test_reader_split_required_checks_cannot_be_missing_false_or_wrong_type(self):
        data, images = self.scenario('readers')
        fields = ['appWitnessPredeclaredOldActorSettled', 'appWitnessAccountOnly', 'appWitnessTrustedNativeActivation',
                  'appWitnessOriginalMarkedBody', 'originalWorkspaceTrustedNativeActivation', 'originalWorkspaceReaderGateOpen',
                  'readerSessionSamples', 'readerBrowserAuthMutations', 'checkedSessionContinuity']
        for field in fields:
            for value in [None, False, 'unknown']:
                bad = copy.deepcopy(data)
                if value is None: del bad['checks'][field]
                else: bad['checks'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    v.validate(bad, self.expected, self.rules, 'readers', images.__getitem__)

    def test_unknown_nested_witnesses_and_secret_fields_are_rejected(self):
        for suite in self.rules['suites']:
            data, images = self.scenario(suite)
            for field in ['actorPassword', 'totpCode', 'openerOwner', 'grantRole', 'rawResponse', 'token']:
                bad = copy.deepcopy(data); bad['witnesses'][field] = 'unit-only-secret'
                with self.subTest(suite=suite, field=field), self.assertRaises(ValueError):
                    v.validate(bad, self.expected, self.rules, suite, images.__getitem__)
            for key, row in data['witnesses'].items():
                if type(row) is dict:
                    bad = copy.deepcopy(data); bad['witnesses'][key]['unexpected'] = True
                    with self.subTest(suite=suite, key=key), self.assertRaises(ValueError):
                        v.validate(bad, self.expected, self.rules, suite, images.__getitem__)

    def test_png_bytes_actual_dimensions_crc_and_allowlists(self):
        data, images = self.scenario('controls')
        name = next(iter(images))
        for mutate in ['digest', 'bytes', 'dimensions', 'extra', 'crc']:
            bad = copy.deepcopy(data); files = dict(images)
            if mutate == 'digest': bad['screenshots'][name]['sha256'] = sha('wrong')
            elif mutate == 'bytes': bad['screenshots'][name]['bytes'] += 1
            elif mutate == 'dimensions': bad['screenshots'][name]['width'] = 390
            elif mutate == 'extra': bad['screenshots']['proof.png'] = dict(bad['screenshots'][name])
            else:
                files[name] = files[name][:-1] + bytes([files[name][-1] ^ 1])
                bad['screenshots'][name]['sha256'] = hashlib.sha256(files[name]).hexdigest()
            with self.subTest(mutate=mutate), self.assertRaises(ValueError):
                v.validate(bad, self.expected, self.rules, 'controls', files.__getitem__)

    def test_exact_original_result_scenes_and_retained_reader_inventory(self):
        self.assertEqual(self.rules['suites']['controls']['screenshots'], {
            'original-raw-members-1440.png': [1440, 900],
            'original-raw-members-390.png': [390, 844],
            'recovered-original-1440.png': [1440, 900],
            'recovered-original-390.png': [390, 844],
            'original-result-values-1440.png': [1440, 900],
            'original-result-values-390.png': [390, 844],
        })
        self.assertEqual(self.rules['suites']['cancel']['screenshots'], {})
        self.assertEqual(self.rules['suites']['readers']['screenshots'], {
            'producer-proof-cleared-1440.png': [1440, 900],
            'producer-proof-cleared-390.png': [390, 844],
            'readonly-members-1440.png': [1440, 900],
            'readonly-members-390.png': [390, 844],
        })

    def test_each_original_values_image_is_required_and_byte_bound(self):
        data, images = self.scenario('controls')
        for name in ['original-result-values-1440.png', 'original-result-values-390.png']:
            for mutate in ['missing', 'renamed', 'digest', 'bytes', 'actual_dimensions']:
                bad = copy.deepcopy(data); files = dict(images)
                if mutate == 'missing':
                    del bad['screenshots'][name]
                elif mutate == 'renamed':
                    bad['screenshots']['unreviewed-detail.png'] = bad['screenshots'].pop(name)
                elif mutate == 'digest':
                    bad['screenshots'][name]['sha256'] = sha('wrong-detail')
                elif mutate == 'bytes':
                    bad['screenshots'][name]['bytes'] += 1
                else:
                    width, height = self.rules['suites']['controls']['screenshots'][name]
                    files[name] = png(width - 1, height)
                    bad['screenshots'][name]['sha256'] = hashlib.sha256(files[name]).hexdigest()
                    bad['screenshots'][name]['bytes'] = len(files[name])
                with self.subTest(name=name, mutate=mutate), self.assertRaises(ValueError):
                    v.validate(bad, self.expected, self.rules, 'controls', files.__getitem__)

    def test_cleanup_requires_real_completed_phase_and_graceful_owned_exits(self):
        for suite in self.rules['suites']:
            phases = ['setup', 'api-start', 'api-ready'] + ([] if suite == 'cancel' else ['worker-start', 'worker-ready']) + ['browser-start', 'browser-pass', 'before-cleanup', 'after-cleanup']
            report = dict(schema=1, phases=[dict(phase=phase, at_ms=i) for i, phase in enumerate(phases)], processes={name: dict(started=True, before_cleanup=None, after_cleanup=0, forced_kill=False) for name in ['api', 'worker']}, http_panic=False, browser=[], browser_capture='pending', browser_dropped=0, collection_errors=[])
            v.validate_cleanup(report, suite)
            for field, value in [('started', False), ('before_cleanup', 0), ('after_cleanup', 1), ('after_cleanup', None), ('forced_kill', True)]:
                bad = copy.deepcopy(report); bad['processes']['worker'][field] = value
                with self.assertRaises(ValueError): v.validate_cleanup(bad, suite)
            for field, value in [('http_panic', True), ('collection_errors', ['snapshot']), ('phases', report['phases'][:-1])]:
                bad = copy.deepcopy(report); bad[field] = value
                with self.assertRaises(ValueError): v.validate_cleanup(bad, suite)

    def test_bounded_paths_reject_traversal_symlink_and_untracked_sources(self):
        for path in ['../secret', '/tmp/x', 'web/../x', 'web//x', 'web\\x', 'web/space x']:
            self.assertFalse(v.safe_path(path))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); (root / 'real').write_bytes(b'abc'); (root / 'link').symlink_to(root / 'real')
            self.assertEqual(v.read_file(root, 'real', 3), b'abc')
            with self.assertRaises(ValueError): v.read_file(root, 'real', 2)
            with self.assertRaises(ValueError): v.read_file(root, 'link', 3)
        rules = self.rules
        paths = rules['sourceFiles'] + ['web/src/new.ts', 'scripts/new.py', 'web/test-helpers/navigation.ts']
        self.assertIn('web/src/new.ts', v.source_paths(paths, rules))
        with self.assertRaises(ValueError): v.source_paths(paths + [paths[0]], rules)
        with self.assertRaises(ValueError): v.source_paths(paths[1:], rules)

    def native(self, reader='directory-v4-controls', mode='native-finish'):
        payload = json.dumps({'marker': 'synthetic-stream', 'value': '😀', 'count': 7}, ensure_ascii=False, separators=(',', ':')).encode()
        digest = hashlib.sha256(payload).hexdigest()
        return dict(kind='held-eof', mode=mode, stage='terminal-before-assertions', pwTerminalCount=1, nativeTerminalCount=1, browser='141.0.7390.37' if mode == 'stable-body' else '157.0.8092.0', productionReader=reader, provenance=copy.deepcopy(self.expected), parserSHA256=self.expected['sourceDigests'][v.NATIVE_READERS[reader]], parserBundleSHA256=sha('compiled-direct-export'), requestCount=1, nativeRequestCount=1,
                    server=dict(requests=1, releaseCalls=1, pendingReadWitnessed=True, bytesWritten=len(payload), finished=True, prematureClose=False), pageObservation='complete', expectedSHA256=digest,
                    page=dict(fulfilled=True, error=None, readers=1, reads=2, observedBytes=len(payload), retainedBytes=len(payload), rawDone=True, eof=True, cancelCalls=0, bodyCancelCalls=0, cancelBeforeEOF=False, released=1, aborted=False, locked=False, limit=8388608, status=200, cache='no-store', sha256=digest),
                    playwright=dict(event='finished', failure=None, cancelled=None), native=dict(event='finished', failure=None, cancelled=None))

    def test_native_direct_source_and_baseline_defect_are_not_conflated(self):
        for reader in v.NATIVE_READERS:
            for mode in ['stable-body', 'native-finish']:
                data = self.native(reader, mode); v.validate_native(data, self.expected, mode)
        data = self.native(mode='stable-body')
        data['playwright'].update(event='failed', failure='net::ERR_ABORTED')
        data['native'].update(event='failed', failure='net::ERR_ABORTED', cancelled=True)
        v.validate_native(data, self.expected, 'stable-body')
        data['mode'] = 'native-finish'; data['browser'] = '157.0.8092.0'
        with self.assertRaises(ValueError): v.validate_native(data, self.expected, 'native-finish')
        for record, key, value in [('page', 'eof', False), ('page', 'readers', 2), ('page', 'cancelCalls', 1), ('page', 'sha256', sha('other')), ('server', 'pendingReadWitnessed', False), ('native', 'event', 'failed')]:
            data = self.native(); data[record][key] = value
            with self.subTest(record=record, key=key), self.assertRaises(ValueError):
                v.validate_native(data, self.expected, 'native-finish')
        data = self.native(); data['parserSHA256'] = sha('different-source')
        with self.assertRaises(ValueError): v.validate_native(data, self.expected, 'native-finish')


if __name__ == '__main__':
    unittest.main()
