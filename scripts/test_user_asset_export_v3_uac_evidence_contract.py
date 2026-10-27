"""Offline unit specimens only: never substitute these for real C acceptance.

The oracle is the reviewed literal file. Test BER/XLSX bytes below are synthetic
inputs to the independent gate, not runtime evidence and not a generated oracle.
"""
import base64
import copy
import io
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zlib

import user_asset_export_v3_uac_ci as ci
import validate_user_asset_export_v3_uac_evidence as v
from test_user_asset_export_v3_evidence_contract import xlsx

ROOT = Path(__file__).resolve().parents[1]
NONCE = '0123456789abcdef0123456789abcdef'
PROVENANCE = {'checkoutCommit': 'a' * 40, 'checkoutTree': 'b' * 40, 'runID': '1', 'runAttempt': '1',
              'sourceSHA256': {'scripts/unit-only.py': 'c' * 64}}


def encoded(value):
    return json.dumps(value, ensure_ascii=True, separators=(',', ':')).encode()


def b64(raw):
    return base64.b64encode(raw).decode('ascii')


def tlv(tag, raw):
    length = bytes([len(raw)]) if len(raw) < 128 else bytes([0x80 | ((len(raw).bit_length() + 7) // 8)]) + len(raw).to_bytes((len(raw).bit_length() + 7) // 8, 'big')
    return bytes([tag]) + length + raw


def integer(n, tag=2):
    raw = n.to_bytes(max(1, (n.bit_length() + 7) // 8), 'big')
    return tlv(tag, b'\0' + raw if raw[0] & 128 else raw)


def wire_attrs(record):
    return [(a['name'], [bytes.fromhex(x) for x in a['valuesHex']] if 'valuesHex' in a else [x.encode('utf-8') for x in a['valuesUTF8']]) for a in record['wire']['attributesInWireOrder']]


def entry(record, mid):
    attrs = b''.join(tlv(0x30, tlv(4, name.encode()) + tlv(0x31, b''.join(tlv(4, val) for val in vals))) for name, vals in wire_attrs(record))
    return tlv(0x30, integer(mid) + tlv(0x64, tlv(4, record['wire']['dn'].encode()) + tlv(0x30, attrs)))


def done(mid, cookie):
    return tlv(0x30, integer(mid) + tlv(0x65, integer(0, 10) + tlv(4, b'') + tlv(4, b'')) +
               tlv(0xa0, tlv(0x30, tlv(4, b'1.2.840.113556.1.4.319') + tlv(4, tlv(0x30, integer(51) + tlv(4, cookie))))))


def observation_object(record):
    public, attrs = record['public12'], dict(wire_attrs(record))
    def raw(name, many=False):
        if name not in attrs:
            return None
        values = [b64(x) for x in attrs[name]]
        return values if many else values[0]
    return {'base': {k: public[k] for k in ('objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl')},
            'supplemental': {'objectSidBytes': raw('objectSid'), 'mailBytes': raw('mail'), 'descriptionBytes': raw('description', True), 'whenCreatedBytes': raw('whenCreated')},
            'identity': {'primaryGroupIDBytes': raw('primaryGroupID'), 'servicePrincipalNameBytes': raw('servicePrincipalName', True)}}


def task(task_id, domain):
    return {'taskUUID': task_id, 'terminalAt': '2026-10-27T00:00:02Z', 'archived': False, 'visibilityVersion': 0,
        'taskName': 'user_assets.v3.export', 'domainId': domain, 'payloadVersion': 1, 'state': 'succeeded', 'sourceState': 'SUCCESS',
        'error': '', 'createdAt': '2026-10-27T00:00:01Z', 'updatedAt': '2026-10-27T00:00:02Z', 'attempt': 1,
        'maxAttempts': 3, 'progress': 100, 'resultVersion': 4, 'result': {}, 'cursor': {}, 'parentTaskUUID': ''}


def png():
    def chunk(kind, body):
        return struct.pack('>I', len(body)) + kind + body + struct.pack('>I', zlib.crc32(kind + body))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(bytes(900 * (1 + 1440 * 3)))) + chunk(b'IEND', b'')


def specimen():
    oracle = v.load_oracle(ROOT)
    records = sorted(oracle['users'] + oracle['reservedUnchangedDecoys'], key=lambda r: r['wireSlot'])
    metadata = {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid', 'domain': 'synthetic.invalid',
        'naming_context': 'DC=synthetic,DC=invalid', 'started_at': '2026-10-27T00:00:00Z', 'completed_at': '2026-10-27T00:00:01Z', 'elapsed_milliseconds': 1000, 'pages': 4}
    observation = {'dictionaryVersion': 3, 'objects': [observation_object(r) for r in records], 'source': metadata}
    raw = encoded(observation)
    producer = {'credentialSource': 'operation_account', 'connectionRevision': '2', 'connectionCredentialGeneration': '1',
        'accountId': 'synthetic-account', 'accountCredentialRevision': '1', 'grantRoleId': 'platform_admin', 'grantRevision': '1', 'policyRevision': 'a' * 64, 'dictionaryVersion': 3}
    source = {'taskUUID': 'synthetic-observation', 'actorId': '2', 'selection': {'domainId': 'synthetic-domain', 'revision': '2', 'credentialRevision': '1'},
        'sourcePins': {k: producer[k] for k in ('accountId', 'accountCredentialRevision', 'policyRevision', 'grantRoleId', 'grantRevision')},
        'bodyBase64': b64(raw), 'sha256': v.sha(raw), 'dictionaryVersion': 3, 'count': 51, 'taskState': 'succeeded', 'taskName': 'domain.directory_read.v3',
        'payloadVersion': 1, 'maxAttempts': 1, 'attempt': 1, 'purpose': 'domain.directory_read.v3', 'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0,
        'taskBinding': {'payloadVersion': 1, 'parentTaskUUID': '', 'payloadHash': '48b68509ca9e5360f7833c1d8966834b3280129fb24903cea728efa2d35b6b9d', 'payload': producer}}
    transcript = {'format': 1, 'profile': v.PROFILE, 'nonce': NONCE, 'transport': 'starttls', 'claimOrdinal': 1, 'entryCount': 51, 'pageCount': 4, 'completionSequence': 123, 'pages': []}
    ordinal, previous = 0, v.sha(b'')
    for page_idx, count in enumerate([15, 15, 15, 6], 1):
        entries = []
        for record in records[ordinal:ordinal + count]:
            frame = entry(record, page_idx + 3)
            entries.append({'entryOrdinal': ordinal, 'messageID': page_idx + 3, 'frameBase64': b64(frame), 'sha256': v.sha(frame)})
            ordinal += 1
        cookie = bytes([page_idx]) * 20 if page_idx < 4 else b''
        original, redacted = done(page_idx + 3, cookie), done(page_idx + 3, bytes(len(cookie)))
        wrapped = {'redactedFrameBase64': b64(redacted), 'cookieValueOffset': len(redacted) - len(cookie), 'cookieValueLength': len(cookie),
            'originalFrameSHA256': v.sha(original), 'redactedFrameSHA256': v.sha(redacted), 'cookieSHA256': v.sha(cookie), 'requestCookieSHA256': previous}
        transcript['pages'].append({'pageIdx': page_idx, 'requestMessageID': page_idx + 3, 'requestCookieSHA256': previous, 'entries': entries, 'done': wrapped})
        previous = v.sha(cookie)
    wire = encoded(transcript)
    seal = {'format': 1, 'profile': v.PROFILE, 'nonce': NONCE, 'candidateTranscriptSHA256': v.sha(wire), 'claimCount': 1,
        'completedExchangeCount': 1, 'directoryAttemptCount': 4, 'pageCount': 4, 'entryCount': 51, 'activeHandlers': 0, 'inflightWrites': 0,
        'listenersClosed': True, 'participantClosed': True, 'poisoned': False, 'poisonCode': 'none', 'lateDirectoryAttempts': 0,
        'participantRebinds': 0, 'terminalState': 'sealed_ok', 'completionSequence': 123, 'finalSequence': 125}
    seal_raw = encoded(seal)
    envelope = {'format': 1, 'profile': v.PROFILE, 'nonce': NONCE, 'candidateTranscriptBase64': b64(wire), 'candidateTranscriptSHA256': v.sha(wire),
        'terminalSealBase64': b64(seal_raw), 'terminalSealSHA256': v.sha(seal_raw)}
    raw_envelope = encoded(envelope)
    cleanup = {'format': 1, 'profile': v.PROFILE, 'nonce': NONCE, 'workerExitCode': 0, 'apiExitCode': 0, 'ldapExitCode': 0,
        'ldapContainerRemoved': True, 'ldapNetworkRemoved': True, 'ldapPrivateDirectoriesRemoved': True, 'databaseContainerRemoved': True,
        'logsClosed': True, 'safeDiagnosticsPassed': True, 'allOwnedCleanupSucceeded': True}
    before = {'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}
    after_each = [{**before, 'owner': 8 + i, 'exportTasks': i + 1} for i in range(3)]
    data = {'format': 1, 'profile': v.PROFILE, 'provenance': copy.deepcopy(PROVENANCE), 'source': source, 'actorId': 1,
        'sensitiveBudget': {'before': before, 'afterEach': after_each, 'after': after_each[-1]}, 'cases': [], 'listPages': [],
        'postLedger': {'events': [], 'createCount': 3, 'cancelCount': 0, 'unexpectedExportWriteCount': 0, 'listenerClosed': True, 'finalSensitiveSnapshot': after_each[-1]},
        'oracleSHA256': v.ORACLE_SHA256, 'rawWire': {'filename': v.RAW_NAME, 'sha256': v.sha(raw_envelope), 'candidateTranscriptSHA256': v.sha(wire), 'terminalSealSHA256': v.sha(seal_raw)}, 'cleanupReceipt': cleanup}
    files = {v.RAW_NAME: raw_envelope}
    for mode in ['omitted', *v.MODES]:
        for page in oracle['queries'][mode]['pages']:
            idx = page['pageIdx']
            query = {'domainId': 'synthetic-domain', 'expectedRevision': '2', 'expectedCredentialRevision': '1', 'observationId': 'synthetic-observation',
                'search': 'user-', 'creatTm': 'desc', 'pageIdx': str(idx), 'pageSize': '10', **({} if mode == 'omitted' else {'uacDisableBit': mode})}
            data['listPages'].append({'mode': mode, 'pageIdx': idx, 'queryEntries': [list(p) for p in query.items()],
                'response': {'dictionaryVersion': 3, 'selection': source['selection'], 'available': True, 'observationId': source['taskUUID'], 'source': metadata,
                    'list': copy.deepcopy(page['public12Rows']), 'page': {'pageIdx': idx, 'pageSize': 10, 'total': 33 if mode == 'omitted' else 11, 'totalPage': 4 if mode == 'omitted' else 2}}})
    for index, mode in enumerate(v.MODES):
        request = {'domainId': 'synthetic-domain', 'expectedRevision': '2', 'expectedCredentialRevision': '1', 'observationId': 'synthetic-observation', 'search': 'user-',
            'sAMAccountName': '', 'startTm': '', 'endTm': '', 'creatTm': 'desc', 'screenColumns': ['mail', 'guid', 'sAMAccountName', 'dn', 'sid', 'whenCreated'], 'uacDisableBit': mode}
        task_id = 'export-' + mode
        filename = 'user-assets-v3-' + task_id + '.xlsx'
        files[filename] = xlsx(oracle['queries'][mode]['expectedWorkbookRows'])
        blob, digest = files[filename], v.sha(files[filename])
        receipt = {'taskID': task_id, 'task': task(task_id, request['domainId']), 'request': copy.deepcopy(request)}
        artifact = {'filename': filename, 'rowCount': 11, 'byteCount': len(blob), 'sha256': digest}
        detail = {**copy.deepcopy(receipt), 'downloadReady': True, 'downloadStatus': 'ready', 'artifact': artifact}
        headers = {'x-adtr-user-id': '1', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff',
            'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'content-length': str(len(blob)),
            'x-adtr-artifact-sha256': digest, 'x-adtr-row-count': '11', 'content-disposition': f'attachment; filename="{filename}"'}
        download = {'filename': filename, 'headers': headers}
        idem = v.sha(('unit-test-idem-' + mode).encode())
        stored = {'taskID': task_id, 'actorId': '1', 'state': 'succeeded', 'kind': 'user_assets.v3.export', 'progress': 100, 'attempt': 1, 'maxAttempts': 3,
            'sourceSHA256': source['sha256'], 'rowCount': 11, 'byteCount': len(blob), 'sha256': digest, 'chunkCount': 1,
            'chunks': [{'ordinal': 0, 'bodyBase64': b64(blob), 'sha256': digest}],
            'events': [{'action': a, 'state': 'succeeded' if a == 'finished' else 'running', 'resultVersion': i} for i, a in enumerate(['submitted', 'leased', 'started', 'progress', 'finished'])],
            'taskBinding': {'payloadVersion': 1, 'parentTaskUUID': '', 'payloadHash': v.sha(b'1::' + json.dumps(request, sort_keys=True, separators=(',', ':')).encode()), 'payload': copy.deepcopy(request)}, 'idempotencyKeySHA256': idem}
        screenshot = mode + '-ready.png'
        files[screenshot] = png()
        data['cases'].append({'name': mode, 'request': request, 'create': {**copy.deepcopy(receipt), 'replayed': False}, 'receipt': receipt,
            'detail': detail, 'download': download, 'database': stored, 'pageSize': 10, 'firstPageCount': 10, 'sourcePageIdx': 2,
            'screenshot': {'filename': screenshot, 'sha256': v.sha(files[screenshot])},
            'listQueryEntries': copy.deepcopy(next(p['queryEntries'] for p in data['listPages'] if p['mode'] == mode and p['pageIdx'] == 2)),
            'originalTaskReadback': {'detail': copy.deepcopy(detail), 'download': copy.deepcopy(download), 'downloadSHA256': digest, 'downloadByteCount': len(blob)}})
        data['postLedger']['events'].append({'ordinal': index + 1, 'case': mode, 'method': 'POST', 'pathname': '/api/user-assets/v3/exports',
            'bodyFieldNames': sorted([*request, 'actorPassword', 'totpCode', 'idempotencyKey']), 'safeBusinessRequest': copy.deepcopy(request), 'idempotencyKeySHA256': idem,
            'sanitizedRequestSHA256': v.sha(encoded(request)), 'status': 200, 'transport': 'response', 'taskID': task_id})
    return data, files, oracle


def repair_wire(data, files, transform=lambda x: None, transform_seal=lambda x: None):
    envelope = json.loads(files[v.RAW_NAME])
    transcript = json.loads(base64.b64decode(envelope['candidateTranscriptBase64']))
    seal = json.loads(base64.b64decode(envelope['terminalSealBase64']))
    transform(transcript)
    wire = encoded(transcript)
    seal['candidateTranscriptSHA256'] = v.sha(wire)
    transform_seal(seal)
    seal_raw = encoded(seal)
    envelope.update(candidateTranscriptBase64=b64(wire), candidateTranscriptSHA256=v.sha(wire), terminalSealBase64=b64(seal_raw), terminalSealSHA256=v.sha(seal_raw))
    files[v.RAW_NAME] = encoded(envelope)
    data['rawWire'].update(sha256=v.sha(files[v.RAW_NAME]), candidateTranscriptSHA256=v.sha(wire), terminalSealSHA256=v.sha(seal_raw))


class EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.template, cls.files, cls.oracle = specimen()

    def validate(self, data=None, files=None):
        v.validate_uac_evidence(data or self.template, (files or self.files).__getitem__, PROVENANCE, oracle=self.oracle)

    def rejected(self, change, code=None):
        data, files = copy.deepcopy(self.template), dict(self.files)
        change(data, files)
        with self.assertRaises(v.UACEvidenceError) as caught:
            self.validate(data, files)
        self.assertRegex(str(caught.exception), '^uac_evidence_invalid:[a-z_]+$')
        if code:
            self.assertEqual(str(caught.exception), 'uac_evidence_invalid:' + code)

    def test_complete_unit_specimen(self):
        self.validate()

    def test_literal_integrity_and_independent_rows(self):
        self.assertEqual(v.sha((ROOT / 'scripts/fixtures/user_uac_v3_mixed33_oracle.json').read_bytes()), v.ORACLE_SHA256)
        self.assertEqual((len(self.oracle['users']), len(self.oracle['reservedUnchangedDecoys'])), (33, 18))
        for record in self.oracle['users'] + self.oracle['reservedUnchangedDecoys']:
            self.assertEqual(v.decoded_public(record['wire']['dn'], wire_attrs(record)), record['public12'])

    def test_frozen_hash_vectors(self):
        self.assertEqual(v.task_hash(self.template['source']['taskBinding']['payload']), '48b68509ca9e5360f7833c1d8966834b3280129fb24903cea728efa2d35b6b9d')
        hashes = [('a48c205173aa53058491ed6531966622564ad83c603c2556f6743fff892c2b43', '8365962728bb11fd7e89fe7abb13ff28bf89bce3a1b3ce708a188d29913f8bd1'),
                  ('ed1e3944cf9aa59c4794ccad2e6b72e1cfac1a84a8f9628d98f01b6b14fc7b9a', 'c9a48b168943c3f691df1c5cb2237e4dbbf34b471da8d324157dcfccde55c781'),
                  ('f2cbe1e4a272c54a9af68e21d39a3d7613371d6d542043c8c697e8070984f008', '5d3252a3a284288e2ff14c4862bc74e52f25cee1a698dec14d6858c9b2091b60')]
        for case, (payload_hash, safe_hash) in zip(self.template['cases'], hashes):
            self.assertEqual(v.task_hash(case['request']), payload_hash)
            self.assertEqual(v.safe_request_hash(case['request']), safe_hash)
            self.assertNotEqual(payload_hash, safe_hash)

    def test_strict_json_utf8_bom_duplicates_surrogates_nonfinite(self):
        for raw in [b'\xef\xbb\xbf{}', b'{"a":1,"\\u0061":2}', b'"\xff"', b'"\\ud800"', b'"\\udfff"', b'NaN', b'Infinity', b'{bad:SECRET}']:
            with self.subTest(raw=raw), self.assertRaisesRegex(v.UACEvidenceError, '^uac_evidence_invalid:candidate_schema$'):
                v.strict_json(raw)
        self.assertEqual(v.strict_json(b'"\\ud83d\\ude00"'), '😀')

    def test_integer_not_boolean_or_float(self):
        for bad in (True, 1.0):
            self.rejected(lambda d, f: d.update(format=bad))
            self.rejected(lambda d, f: d['source']['taskBinding'].update(payloadVersion=bad))
        self.rejected(lambda d, f: d['cleanupReceipt'].update(ldapExitCode=False), 'candidate_schema')

    def test_closed_keys_at_every_envelope(self):
        paths = [(), ('source',), ('source', 'taskBinding'), ('sensitiveBudget',), ('cleanupReceipt',), ('postLedger',), ('rawWire',), ('cases', 0),
                 ('cases', 0, 'request'), ('cases', 0, 'create'), ('cases', 0, 'create', 'task'), ('cases', 0, 'database'), ('cases', 0, 'database', 'chunks', 0), ('listPages', 0, 'response', 'list', 0)]
        for target in paths:
            def mutate(d, f):
                obj = d
                for part in target:
                    obj = obj[part]
                obj['unexpected'] = 'SECRET'
            with self.subTest(path=target): self.rejected(mutate)

    def test_task_binding_different_domain_hash_and_payload(self):
        self.rejected(lambda d, f: d['source']['taskBinding']['payload'].update(connectionRevision='3'), 'task_binding')
        self.rejected(lambda d, f: d['cases'][0]['database']['taskBinding'].update(payloadHash='a' * 64), 'task_binding')
        self.rejected(lambda d, f: d['cases'][0]['database'].update(idempotencyKeySHA256='b' * 64), 'task_binding')
        self.rejected(lambda d, f: d['cases'][0]['detail']['task'].update(domainId='other'), 'task_binding')

    def test_all_public_task_fields_required(self):
        for field in v.SCHEMA['PublicTask']['required']:
            with self.subTest(field=field): self.rejected(lambda d, f: d['cases'][0]['receipt']['task'].pop(field))
        good = copy.deepcopy(self.template)
        good['cases'][0]['create']['task']['nextAttemptAt'] = '2026-10-27T00:00:03Z'
        self.validate(good)
        self.rejected(lambda d, f: d['cases'][0]['receipt']['task'].update(state='running', sourceState='STARTED', terminalAt=None, progress=0), 'task_binding')

    def test_source_nonuser_uac_and_raw_bytes_not_filtered_away(self):
        def mutate(d, f):
            body = json.loads(base64.b64decode(d['source']['bodyBase64']))
            body['objects'][12]['base']['userAccountControl'] = 2
            raw = encoded(body)
            d['source'].update(bodyBase64=b64(raw), sha256=v.sha(raw))
            for case in d['cases']: case['database']['sourceSHA256'] = v.sha(raw)
        self.rejected(mutate, 'source_binding')

    def test_calendar_and_nanosecond_ordering(self):
        with self.assertRaises(v.UACEvidenceError): v.instant('2026-02-30T00:00:00Z')
        self.assertLess(v.instant('2026-10-27T00:00:00.000000001Z'), v.instant('2026-10-27T00:00:00.000000002Z'))
        self.assertEqual(v.instant('0001-01-01T00:00:00Z'), v.instant('0001-01-01T00:00:00.0Z'))

    def test_list_all_pages_full_rows_query_duplicates(self):
        self.rejected(lambda d, f: d['listPages'][0]['queryEntries'].append(['search', 'user-']), 'source_binding')
        self.rejected(lambda d, f: d['listPages'][0]['response']['list'][0].update(userAccountControl=514), 'source_binding')
        self.rejected(lambda d, f: d['cases'][0].update(listQueryEntries=d['listPages'][4]['queryEntries']), 'source_binding')
        self.rejected(lambda d, f: d['listPages'].reverse(), 'source_binding')

    def test_post_lifecycle_counts_and_safe_hash(self):
        for key in ('createCount', 'cancelCount', 'unexpectedExportWriteCount'):
            self.rejected(lambda d, f: d['postLedger'].update({key: 4}))
        self.rejected(lambda d, f: d['postLedger']['events'][0].update(sanitizedRequestSHA256='0' * 64), 'post_binding')
        self.rejected(lambda d, f: d['postLedger']['events'][1].update(idempotencyKeySHA256=d['postLedger']['events'][0]['idempotencyKeySHA256']), 'post_binding')
        self.rejected(lambda d, f: d['sensitiveBudget']['after'].update(owner=9), 'post_count')

    def test_original_readback_byte_header_request_binding(self):
        self.rejected(lambda d, f: d['cases'][0]['originalTaskReadback'].update(downloadSHA256='0' * 64), 'artifact_binding')
        self.rejected(lambda d, f: d['cases'][0]['originalTaskReadback']['download']['headers'].update({'x-adtr-row-count': '10'}))
        self.rejected(lambda d, f: d['cases'][0]['originalTaskReadback']['detail']['request'].update(uacDisableBit='clear'), 'task_binding')

    def test_chunk_sequence_hash_and_full_join(self):
        self.rejected(lambda d, f: d['cases'][0]['database']['chunks'][0].update(ordinal=1), 'artifact_binding')
        self.rejected(lambda d, f: d['cases'][0]['database']['chunks'][0].update(bodyBase64=b64(b'wrong'), sha256=v.sha(b'wrong')), 'artifact_binding')

    def test_full_workbook_semantics_after_all_hashes_repaired(self):
        def mutate(d, f):
            case = d['cases'][0]
            rows = copy.deepcopy(self.oracle['queries']['set']['expectedWorkbookRows'])
            rows[-1][0] = 'forged-last-row'
            raw = xlsx(rows); digest = v.sha(raw)
            f[case['download']['filename']] = raw
            for detail in (case['detail'], case['originalTaskReadback']['detail']): detail['artifact'].update(sha256=digest, byteCount=len(raw))
            for download in (case['download'], case['originalTaskReadback']['download']): download['headers'].update({'content-length': str(len(raw)), 'x-adtr-artifact-sha256': digest})
            case['originalTaskReadback'].update(downloadSHA256=digest, downloadByteCount=len(raw))
            case['database'].update(sha256=digest, byteCount=len(raw), chunks=[{'ordinal': 0, 'bodyBase64': b64(raw), 'sha256': digest}])
        self.rejected(mutate, 'artifact_binding')

    def test_terminal_seal_all_shutdown_facts(self):
        for key, bad in [('activeHandlers', 1), ('inflightWrites', 1), ('listenersClosed', False), ('participantClosed', False), ('poisoned', True), ('lateDirectoryAttempts', 1), ('participantRebinds', 1), ('directoryAttemptCount', 5), ('completionSequence', 124), ('finalSequence', 122), ('terminalState', 'complete_unsealed')]:
            with self.subTest(key=key):
                self.rejected(lambda d, f: repair_wire(d, f, transform_seal=lambda s: s.update({key: bad})), 'terminal_seal')

    def test_transcript_nonce_cookie_chain_wrapper_and_ordinal(self):
        mutations = [lambda t: t.update(nonce='a' * 32), lambda t: t['pages'][0].update(requestMessageID=5),
            lambda t: t['pages'][0]['entries'][0].update(entryOrdinal=1), lambda t: t['pages'][1].update(requestCookieSHA256='b' * 64),
            lambda t: t['pages'][0]['done'].update(requestCookieSHA256='c' * 64), lambda t: t['pages'][0]['done'].update(cookieValueOffset=0),
            lambda t: t['pages'][3]['done'].update(originalFrameSHA256='d' * 64)]
        for change in mutations:
            self.rejected(lambda d, f: repair_wire(d, f, transform=change), 'raw_transcript')

    def test_actual_ber_semantics_not_repaired_hashes(self):
        def change(t):
            wrapped = t['pages'][0]['entries'][0]
            raw = bytearray(base64.b64decode(wrapped['frameBase64']))
            pos = raw.index(b'user-01')
            raw[pos:pos + 7] = b'user-99'
            wrapped.update(frameBase64=b64(raw), sha256=v.sha(raw))
        self.rejected(lambda d, f: repair_wire(d, f, transform=change), 'raw_transcript')

    def test_ber_rejects_indefinite_nonminimal_extra_control(self):
        for raw in (b'\x30\x80\x00\x00', b'\x30\x81\x01\x00', b'\x30\x82\x00\x80' + bytes(128)):
            with self.assertRaises(v.UACEvidenceError): v.entry_wire(raw)
        def change(t):
            wrapped = t['pages'][0]['done']
            raw = done(4, bytes(20)) + b'\0'
            wrapped.update(redactedFrameBase64=b64(raw), redactedFrameSHA256=v.sha(raw))
        self.rejected(lambda d, f: repair_wire(d, f, transform=change), 'raw_transcript')

    def test_cleanup_failed_never_final(self):
        for key, value in self.template['cleanupReceipt'].items():
            if type(value) is bool:
                self.rejected(lambda d, f: d['cleanupReceipt'].update({key: False}))

    def test_size_caps_and_canonical_base64(self):
        with self.assertRaises(v.UACEvidenceError): v.strict_json(b'{}' + b' ' * 20, cap=8)
        with self.assertRaises(v.UACEvidenceError): v.b64('Zh==', 10)
        with self.assertRaises(v.UACEvidenceError): v.b64('Zg==\n', 10)
        self.rejected(lambda d, f: d['source'].update(bodyBase64=b64(bytes((256 << 10) + 1))))


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data, self.files, self.oracle = specimen()
        (self.root / 'scripts/fixtures').mkdir(parents=True)
        (self.root / 'scripts/fixtures/user_uac_v3_mixed33_oracle.json').write_bytes((ROOT / 'scripts/fixtures/user_uac_v3_mixed33_oracle.json').read_bytes())
        (self.root / ci.PROVENANCE).parent.mkdir(parents=True)
        self.provenance = encoded(PROVENANCE)
        (self.root / ci.PROVENANCE).write_bytes(self.provenance)
        self.parent = self.root / 'web/test-results/unit-only'
        self.parent.mkdir(parents=True)
        envelope = json.loads(self.files[v.RAW_NAME])
        self.retained = (base64.b64decode(envelope['candidateTranscriptBase64']), base64.b64decode(envelope['terminalSealBase64']), 0)
        candidate = {k: val for k, val in self.data.items() if k not in ('rawWire', 'cleanupReceipt')}
        candidate.update(nonce=NONCE, candidateTranscriptSHA256=envelope['candidateTranscriptSHA256'])
        self.candidate = self.parent / ci.CANDIDATE_NAME
        self.candidate.write_bytes(encoded(candidate))
        for name, raw in self.files.items():
            if name != v.RAW_NAME: (self.parent / name).write_bytes(raw)
        self.current = patch.object(ci, '_current', return_value=copy.deepcopy(PROVENANCE))
        self.current.start(); self.addCleanup(self.current.stop)

    def finalize(self, **kw):
        args = dict(repo=self.root, run_nonce=NONCE, provenance_bytes=self.provenance, candidate_path=self.candidate,
                    retained_ldap=self.retained, cleanup_receipt=self.data['cleanupReceipt'])
        args.update(kw)
        return ci.finalize_uac_evidence(**args)

    def test_finalizer_exact_nine_readback_and_exclusive_publish(self):
        result = self.finalize()
        self.assertEqual(len(result.files), 9)
        self.assertEqual(result.relative_directory, ci.ARTIFACTS.as_posix())
        self.assertEqual(result.files, ci._validate_directory(self.root, self.root / ci.ARTIFACTS, PROVENANCE))
        with self.assertRaises(v.UACEvidenceError): self.finalize()
        self.assertEqual(len(list((self.root / ci.ARTIFACTS).iterdir())), 9)

    def test_no_publish_on_bad_cleanup_retained_or_provenance(self):
        for change in [dict(cleanup_receipt={**self.data['cleanupReceipt'], 'logsClosed': False}),
                       dict(retained_ldap=(*self.retained[:2], 1)), dict(retained_ldap=(*self.retained[:2], False)),
                       dict(run_nonce='a' * 32), dict(provenance_bytes=b'{}')]:
            with self.subTest(change=list(change)), self.assertRaises(v.UACEvidenceError): self.finalize(**change)
            self.assertFalse((self.root / ci.ARTIFACTS).exists())

    def test_symlink_fifo_escape_duplicate_discovery(self):
        external = self.root / 'external'
        external.write_bytes(b'{}')
        self.candidate.unlink(); self.candidate.symlink_to(external)
        with self.assertRaises(v.UACEvidenceError): self.finalize()
        self.candidate.unlink(); os.mkfifo(self.candidate)
        with self.assertRaises(v.UACEvidenceError): self.finalize()
        self.candidate.unlink(); self.candidate.write_bytes(b'{}')
        other = self.root / 'web/test-results/other'; other.mkdir()
        (other / ci.CANDIDATE_NAME).write_bytes(b'{}')
        with self.assertRaises(v.UACEvidenceError): ci.discover_uac_candidate(repo=self.root)

    def test_preexisting_directory_never_deleted_and_unknown_file_rejected(self):
        directory = self.root / ci.ARTIFACTS
        directory.mkdir(parents=True)
        sentinel = directory / 'keep'
        sentinel.write_text('keep')
        with self.assertRaises(v.UACEvidenceError): self.finalize()
        self.assertEqual(sentinel.read_text(), 'keep')

    def test_github_output_exact_paths_and_duplicate_refused(self):
        result = self.finalize()
        output = self.root / 'output'; output.touch()
        paths = [(ci.ARTIFACTS / f.filename).as_posix() for f in result.files]
        with patch.dict(os.environ, {'GITHUB_OUTPUT': str(output), 'RUNNER_TEMP': str(self.root)}):
            ci._github_output(paths)
            content = output.read_text()
            self.assertEqual(content.splitlines()[1:-1], paths)
            with self.assertRaises(v.UACEvidenceError): ci._github_output(paths)
            self.assertEqual(content, output.read_text())

    def test_github_output_traversal_never_writes_outside_temp(self):
        runner_temp = self.root / 'runner-temp'
        runner_temp.mkdir()
        outside = self.root / 'outside'
        outside.write_text('sentinel\n')
        paths = [(ci.ARTIFACTS / name).as_posix() for name in ci._artifact_names(self.data)]
        escaped = str(runner_temp) + '/../outside'
        with patch.dict(os.environ, {'GITHUB_OUTPUT': escaped, 'RUNNER_TEMP': str(runner_temp)}):
            with self.assertRaises(v.UACEvidenceError): ci._github_output(paths)
        self.assertEqual(outside.read_text(), 'sentinel\n')
        with patch.dict(os.environ, {'GITHUB_OUTPUT': str(outside), 'RUNNER_TEMP': str(runner_temp)}):
            with self.assertRaises(v.UACEvidenceError): ci._github_output(paths)
        self.assertEqual(outside.read_text(), 'sentinel\n')

    def test_configure_exact_mode_nonce_no_default_marker(self):
        env = {}
        ci.configure_uac(env, nonce=NONCE)
        self.assertEqual(env, {'ADTR_E2E_USER_ASSET_EXPORT_V3_UAC': 'true', ci.NONCE_ENV: NONCE})
        for key, value in [('ADTR_E2E_LDAP_DIRECTORY_V4', 'true'), ('ADTR_E2E_USER_ASSET_EXPORT_V3', 'true'), (ci.NONCE_ENV, 'a' * 32), ('ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE', 'wrong')]:
            with self.assertRaises(v.UACEvidenceError): ci.configure_uac({key: value}, nonce=NONCE)


class BrowserHelperOfflineTests(unittest.TestCase):
    def test_post_parser_and_observer_async_privacy_contract(self):
        # Transpile the actual TS helper and parser; fake Playwright objects are
        # explicitly unit inputs, never evidence of browser or HTTP execution.
        script = r'''const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto");
const { EventEmitter } = require("node:events");
const root = process.argv[1], ts = require(path.join(root, "web/node_modules/typescript"));
const hash = (v) => crypto.createHash("sha256").update(v).digest("hex");
function compile(name, imports) {
  const code = ts.transpileModule(fs.readFileSync(path.join(root, name), "utf8"), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  const module = { exports: {} };
  new Function("require", "module", "exports", code)((id) => imports[id] ?? require(id), module, module.exports);
  return module.exports;
}
const parser = compile("web/src/directory-v4-controls-json.ts", { "./api": { ApiError: class extends Error { constructor() { super("invalid_response"); } } } });
const h = compile("web/e2e/user-asset-export-v3-uac-helpers.ts", { "../src/directory-v4-controls-json": parser, "./user-asset-export-v3-helpers": { hash } });
const check = (v) => { if (!v) throw new Error("unit_assertion"); };
const origin = "http://127.0.0.1:12345";
const source = { taskUUID: "synthetic-observation", selection: { domainId: "synthetic-domain", revision: "2", credentialRevision: "1" } };
const key = "01234567-89ab-4cde-8123-456789abcdef";
const locator = (k=key) => JSON.stringify({ version: 1, kind: "user_assets.v3.export", actorID: 1, domainId: "synthetic-domain", idempotencyKey: k });
let storage = locator(), reads = 0;
const frame = {}, page = { mainFrame: () => frame, isClosed: () => false, evaluate: async () => { reads++; return storage; } };
const expected = h.expectedUACRequest(source, "set");
const body = (expected) => ({ ...expected, idempotencyKey: key, actorPassword: "unit-ephemeral-password", totpCode: "843921" });
function req(raw=JSON.stringify(body(expected)), options={}) {
  const bytes = Buffer.isBuffer(raw) ? Buffer.from(raw) : Buffer.from(raw);
  return { frame: () => options.frame ?? frame, method: () => options.method ?? "POST", url: () => options.url ?? origin + h.exportPrefix,
    postDataBuffer: () => bytes, response: async () => options.response === null ? null : ({ status: () => 200,
      headers: () => ({ "x-adtr-user-id": "1", "cache-control": "no-store" }), body: async () => Buffer.from(JSON.stringify({ taskID: options.taskID ?? "export-set", task: {}, request: options.expected ?? expected, replayed: false })) }) };
}
async function reject(input, reader=async () => storage, secrets=[]) {
  let message = "";
  try { await h.inspectUACCreateRequest(input, expected, 1, page, origin, reader, secrets); }
  catch (e) { message = e.message; check(e.cause === undefined); }
  check(/^uac_evidence_invalid:(post_parse|post_binding)$/.test(message));
}
(async () => {
 const runSecrets = [];
 const good = await h.inspectUACCreateRequest(req(), expected, 1, page, origin, async () => storage, runSecrets);
 // Reflected current proof must fail before any caller can persist the response.
 const reflected = { status: () => 200, headers: () => ({ "x-adtr-user-id": "1", "cache-control": "no-store" }),
   body: async () => Buffer.from(JSON.stringify({ taskID: "export-set", task: { error: "843921" }, request: expected, replayed: false })) };
 let reflectionRejected = false;
 try { await h.safeResponse(reflected, 1, runSecrets); } catch (e) { reflectionRejected = e.message === "uac_evidence_invalid:post_binding"; }
 check(reflectionRejected);
 for (const value of [{ candidate: { response: "843921" } }, ["console reflected 843921"], { escaped: "unit-ephemeral-password" }]) {
   let rejected = false; try { h.safeValue(value, runSecrets); } catch { rejected = true; } check(rejected);
 }
 // Current and older-case raw keys may appear in decoded keys or string values,
 // but only their safe SHA commitments may leave the private request owner.
 const keyReflected = { ...reflected, body: async () => Buffer.from(JSON.stringify({ taskID: "export-set", task: { error: key }, request: expected, replayed: false })) };
 for (const response of [keyReflected, { ...keyReflected, body: async () => Buffer.from(JSON.stringify({ nested: key }).replace(/0/g, "\\u0030")) }]) {
   let rejected = false; try { await h.safeResponse(response, 1, runSecrets); } catch (e) { rejected = e.message === "uac_evidence_invalid:post_binding"; } check(rejected);
 }
 for (const value of [{ candidate: { create: { task: { error: key } } } }, ["console reflected " + key], { [key]: "object-key-reflection" }, { headers: { arbitrary: key } }]) {
   let rejected = false; try { h.safeValue(value, runSecrets); } catch (e) { rejected = e.message === "uac_evidence_invalid:post_binding"; } check(rejected);
 }
 h.safeValue({ idempotencyKeySHA256: hash(key) }, runSecrets);
 h.safeValue({ unrelatedRunValue: key }, []); // no cross-run registry alias
 const secondKey = "fedcba98-7654-4321-8abc-def012345678", secondExpected = h.expectedUACRequest(source, "clear");
 storage = locator(secondKey);
 await h.inspectUACCreateRequest(req(JSON.stringify({ ...body(secondExpected), idempotencyKey: secondKey }), { expected: secondExpected }), secondExpected, 1, page, origin, async () => storage, runSecrets);
 let oldRejected = false; try { h.safeValue({ previousCaseKey: key }, runSecrets); } catch { oldRejected = true; } check(oldRejected);
 storage = locator();
 // Exercise the actual proof generation helper, not merely a fake registration.
 const fixture = compile("web/e2e/group-assets-v3-fixture.ts", { "./navigation": {}, "@playwright/test": {}, "./directory-v2-response-observer": {} });
 const generatedSecrets = [], capturedCodes = [];
 const proofPage = { getByLabel: (name) => ({ fill: async (value) => {
   if (name === "未使用的认证器验证码") { check(generatedSecrets.includes(value)); capturedCodes.push(value); }
 } }) };
 await fixture.proof(proofPage, "unit-generated-password", { secret: "JBSWY3DPEHPK3PXP", lastCounter: 0 }, generatedSecrets);
 check(capturedCodes.length === 1 && /^[0-9]{6}$/.test(capturedCodes[0]) && generatedSecrets.length === 1);
 let generatedRejected = false; try { h.safeValue({ reflected: capturedCodes[0] }, generatedSecrets); } catch { generatedRejected = true; } check(generatedRejected);
 check(good.idempotencyKeySHA256 === hash(key) && good.sanitizedRequestSHA256 === "8365962728bb11fd7e89fe7abb13ff28bf89bce3a1b3ce708a188d29913f8bd1");
 check(!JSON.stringify(good).includes(key) && !JSON.stringify(good).includes("unit-ephemeral-password") && Object.isFrozen(good.safeBusinessRequest.screenColumns));
 for (const raw of [Buffer.concat([Buffer.from([239,187,191]), Buffer.from(JSON.stringify(body(expected)))]), Buffer.from([255]),
   '{"domainId":"a","\\u0064omainId":"b"}', '{"actorPassword":"\\ud800"}', '{"x":NaN}', '{"x":1e999}',
   JSON.stringify({ ...body(expected), unexpected: "unit-ephemeral-password" }), JSON.stringify({ ...body(expected), totpCode: 843921 }),
   JSON.stringify({ ...body(expected), screenColumns: [null] }), JSON.stringify({ ...body(expected), idempotencyKey: "different" }),
   JSON.stringify({ ...body(expected), actorPassword: "" }), JSON.stringify({ ...body(expected), actorPassword: "a".repeat(257) }),
   JSON.stringify({ ...body(expected), startTm: null }), ' '.repeat(32769)]) await reject(req(raw));
 await reject(req(), async () => null);
 await reject(req(), async () => JSON.stringify({ ...JSON.parse(locator()), actorID: 2 }));
 await reject(req(), async () => locator(), [key]);
 let wrongReads = 0;
 for (const options of [{ frame: {} }, { url: "http://127.0.0.2:12345" + h.exportPrefix }, { method: "PUT" }])
   await reject(req(undefined, options), async () => { wrongReads++; return storage; });
 check(wrongReads === 0);
 await h.inspectUACCreateRequest(req(JSON.stringify({ ...body(expected), actorPassword: "valid-😀-proof" })), expected, 1, page, origin, async () => storage, []);
 class Context extends EventEmitter { constructor() { super(); this.tabs = [page]; } pages() { return this.tabs; } }
 const context = new Context(), observer = h.observeUACExportWrites(context, origin, []);
 for (const [i, mode] of ["set", "clear", "not_returned"].entries()) {
   const exp = h.expectedUACRequest(source, mode); storage = locator();
   observer.armCase(mode, exp, 1, page);
   context.emit("request", req(JSON.stringify(body(exp)), { taskID: "export-" + mode, expected: exp }));
   let blocked = false; try { observer.armCase(mode, exp, 1, page); } catch { blocked = true; } check(blocked);
   await observer.settleCase("export-" + mode);
 }
 context.tabs = [];
 const ledger = await observer.closeAndSnapshot({ owner: 10, producer: 9, observations: 1, directoryUses: 1, exportTasks: 3 });
 check(ledger.events.length === 3 && ledger.createCount === 3 && context.listenerCount("request") === 0 && !JSON.stringify(ledger).includes(key));
 for (const failure of ["double", "wrongpage", "cancel", "missing", "failed"]) {
   const c = new Context(), o = h.observeUACExportWrites(c, origin, []); storage = locator(); o.armCase("set", expected, 1, page);
   const r = req(undefined, failure === "wrongpage" ? { frame: {} } : failure === "missing" ? { response: null } : {});
   c.emit("request", r);
   if (failure === "double") c.emit("request", req());
   if (failure === "cancel") c.emit("request", req(undefined, { url: origin + h.exportPrefix + "/cancel" }));
   if (failure === "failed") c.emit("requestfailed", r);
   let failed = false; try { await o.settleCase("export-set"); } catch (e) { failed = /^uac_evidence_invalid:/.test(e.message); } check(failed);
   c.tabs = []; try { await o.closeAndSnapshot({}); } catch { /* expected sticky failure */ }
 }
 let screenshots = 0;
 function screenshotPage(options={}) {
   const p = {
     opener: async () => options.opener ?? null,
     url: () => origin + "/#tasks", viewportSize: () => ({ width: 1440, height: 900 }),
     evaluate: async (_, name) => name ? (options.locator ?? null) : true,
     request: { get: async () => ({ status: () => 200, json: async () => ({ ID: 1 }) }) },
     screenshot: async () => { screenshots++; },
     getByRole: (_, config) => config.name === "用户资产导出" ? { count: async () => 0 } : {
       isVisible: async () => true,
       getByText: () => ({ count: async () => 1, isVisible: async () => true }),
       getByLabel: (_, __) => ({ innerText: async () => JSON.stringify(expected), isVisible: async () => true }),
       getByRole: () => ({ isEnabled: async () => true }), scrollIntoViewIfNeeded: async () => {},
     },
     locator: (name) => name === 'input[type="password"]' ? { count: async () => options.password ?? 0 } : name === "body" ?
       { innerText: async () => options.text ?? "safe unit body" } : { evaluateAll: async () => options.values ?? [] },
   }; return p;
 }
 const info = { outputPath: (name) => name };
 await h.captureUACReadOnlyScreenshot(screenshotPage(), page, origin, 1, "export-set", expected, hash(key), ["unit-secret"], info, "set");
 check(screenshots === 1);
 for (const values of [{ text: key }, { values: [secondKey] }, { text: "843921" }, { values: ["843921"] }, { text: "unit-ephemeral-password" }]) {
   let rejected = false;
   try { await h.captureUACReadOnlyScreenshot(screenshotPage(values), page, origin, 1, "export-set", expected, hash(key), runSecrets, info, "set"); }
   catch (e) { rejected = e.message === "uac_evidence_invalid:artifact_binding"; }
   check(rejected && screenshots === 1);
 }
 for (const options of [{ text: key }, { values: [key] }, { text: "unit-secret" }, { values: ["unit-secret"] },
   { password: 1 }, { locator: locator() }, { opener: {} }]) {
   let rejected = false;
   try { await h.captureUACReadOnlyScreenshot(screenshotPage(options), page, origin, 1, "export-set", expected, hash(key), ["unit-secret"], info, "set"); }
   catch (e) { rejected = e.message === "uac_evidence_invalid:artifact_binding"; }
   check(rejected && screenshots === 1);
 }
 console.log("offline_parser_observer_contract_pass");
})().catch(() => { console.error("offline_parser_observer_contract_failed"); process.exitCode = 1; });'''
        result = subprocess.run(['node', '-e', script, str(ROOT)], capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), 'offline_parser_observer_contract_pass')

    def test_browser_and_sql_safety_source_contract(self):
        helper = (ROOT / 'web/e2e/user-asset-export-v3-uac-helpers.ts').read_text()
        spec = (ROOT / 'web/e2e/user-asset-export-v3-uac-mixed33.spec.ts').read_text()
        flat_helper = re.sub(r'\s+', ' ', helper)
        flat_spec = re.sub(r'\s+', ' ', spec)
        self.assertIn('trace: "off", screenshot: "off", video: "off"', flat_spec)
        self.assertEqual(helper.count('.screenshot('), 1)
        self.assertNotIn('.screenshot(', spec)
        self.assertNotIn('removeItem(', spec + helper)
        self.assertNotIn('route(', spec + helper)
        self.assertNotIn('mask:', spec + helper)
        fixture = (ROOT / 'web/e2e/group-assets-v3-fixture.ts').read_text()
        self.assertIn('options: { emptyRefresh?: boolean; userAssetExport?: boolean } = {}', fixture)
        self.assertIn('return setupGroupFixtureMode(page, context, options);', fixture)
        self.assertIn('setupUACGroupFixture(page, context, "mixed-33-v1")', flat_spec)
        self.assertRegex(flat_spec, r'await proof\(\s*page, fixture\.bootstrap\.password, fixture\.bootstrap\.auth, fixture\.secrets,?\s*\)')
        self.assertIn('await observer.settleCase(created.taskID)', flat_spec)
        self.assertIn('await readUACOriginalReceipt(', spec)
        self.assertIn('await captureUACReadOnlyScreenshot(', spec)
        self.assertIn('(await originalTab.opener()) === null', flat_helper)
        self.assertRegex(flat_helper, r'candidates\.some\(\s*\(candidate\) => hash\(candidate\) === expectedIdempotencyKeySHA256,?\s*\)')
        self.assertIn("encode(sha256(convert_to(t.idempotency_key,'UTF8')),'hex')", helper)
        self.assertNotIn("'idempotencyKey',t.idempotency_key", helper)
        self.assertIn("'payloadHash',t.payload_hash,'payload',t.payload", helper)
        self.assertIn('timeout: 5000, maxBuffer: cap', flat_helper)
        self.assertIn('PGOPTIONS=-c statement_timeout=2000', helper)


if __name__ == '__main__':
    unittest.main()
