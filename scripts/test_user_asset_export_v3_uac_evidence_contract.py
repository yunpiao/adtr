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
        'naming_context': 'dc=synthetic,dc=invalid', 'started_at': '2026-10-27T00:00:00Z', 'completed_at': '2026-10-27T00:00:01Z', 'elapsed_milliseconds': 1000, 'pages': 4}
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

    def test_directory_source_exact_naming_context_erratum_and_unchanged_host(self):
        metadata = self.template['listPages'][0]['response']['source']
        self.assertEqual(metadata['naming_context'], 'dc=synthetic,dc=invalid')
        self.assertEqual(metadata['dc_host_name'], 'dc.synthetic.invalid')
        v.schema(metadata, 'DirectorySource')
        old_defs = copy.deepcopy(v.SCHEMA)
        old_defs['DirectorySource']['properties']['naming_context']['const'] = 'DC=synthetic,DC=invalid'
        self.assertEqual(v.sha((json.dumps(old_defs, ensure_ascii=True, sort_keys=True, separators=(',', ':')) + '\n').encode()), 'ae0b82fb4b4af2d7819352aceab73817c01d38346cff56b97f81f8635e493897')
        for bad in ('DC=synthetic,DC=invalid', 'dc=Synthetic,dc=invalid', 'dc=synthetic,dc=invalid.',
                    'dc=synthetic, dc=invalid', 'dc=synthetic,dc=other', '', None):
            with self.assertRaisesRegex(v.UACEvidenceError, '^uac_evidence_invalid:candidate_schema$'):
                v.schema({**metadata, 'naming_context': bad}, 'DirectorySource')
            data, files, _ = specimen()
            for page in data['listPages']:
                page['response']['source']['naming_context'] = bad
            raw = json.loads(base64.b64decode(data['source']['bodyBase64']))
            raw['source']['naming_context'] = bad
            raw = encoded(raw)
            data['source'].update(bodyBase64=b64(raw), sha256=v.sha(raw))
            for case in data['cases']:
                case['database']['sourceSHA256'] = v.sha(raw)
            with self.assertRaisesRegex(v.UACEvidenceError, '^uac_evidence_invalid:candidate_schema$'):
                v.validate_uac_evidence(data, lambda name: files[name], PROVENANCE, oracle=self.oracle)
        for bad in ('DC.Synthetic.Invalid.', 'dc.synthetic.invalid.', 'DC.synthetic.invalid', 'other.synthetic.invalid'):
            with self.assertRaisesRegex(v.UACEvidenceError, '^uac_evidence_invalid:candidate_schema$'):
                v.schema({**metadata, 'dc_host_name': bad}, 'DirectorySource')

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
        # Node 24 (the pinned CI toolchain) executes the actual TS sources before
        # npm ci. Only the existing offline dependency seams are stubbed; fake
        # Playwright objects never establish browser or HTTP acceptance evidence.
        script = r'''const path = require("node:path"), crypto = require("node:crypto");
const { EventEmitter } = require("node:events");
const { registerHooks, isBuiltin } = require("node:module");
const { pathToFileURL } = require("node:url");
const root = process.argv[1];
const hash = (v) => crypto.createHash("sha256").update(v).digest("hex");
const local = (name) => pathToFileURL(path.join(root, name)).href;
const stub = (source) => "data:text/javascript," + encodeURIComponent(source);
const parserURL = local("web/src/directory-v4-controls-json.ts");
const helperURL = local("web/e2e/user-asset-export-v3-uac-helpers.ts");
const fixtureURL = local("web/e2e/group-assets-v3-fixture.ts");
const unused = stub('const fail = () => { throw new Error("unexpected_offline_dependency"); }; export { fail as navigateTo, fail as test, fail as expect, fail as directoryV2ResponseJSON };');
const imports = new Map([
  [parserURL, { "./api": stub('export class ApiError extends Error { constructor() { super("invalid_response"); } }') }],
  [helperURL, { "../src/directory-v4-controls-json": parserURL,
    "./directory-v2-response-observer": stub('export const directoryUACResponseBytes = async (response) => response.body(); export const directoryUACResponseFailureCheck = () => undefined;'),
    "./user-asset-export-v3-helpers": stub('import { createHash } from "node:crypto"; export const hash = (v) => createHash("sha256").update(v).digest("hex");') }],
  [fixtureURL, { "./navigation": unused, "@playwright/test": unused, "./directory-v2-response-observer": unused }],
]);
const hooks = registerHooks({ resolve(specifier, context, nextResolve) {
  const dependencies = imports.get(context.parentURL);
  if (dependencies && Object.hasOwn(dependencies, specifier)) return { url: dependencies[specifier], shortCircuit: true };
  if (dependencies && !isBuiltin(specifier)) throw new Error("unexpected_offline_dependency");
  return nextResolve(specifier, context);
} });
let h, fixture;
try {
  h = require(path.join(root, "web/e2e/user-asset-export-v3-uac-helpers.ts"));
  fixture = require(path.join(root, "web/e2e/group-assets-v3-fixture.ts"));
} finally { hooks.deregister(); }
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

    def test_safe_response_closed_checks_cache_and_late_secret(self):
        # Actual TS sources run in Node before npm ci. Explicit unit seams are
        # distinct from the native observer/Response/ReadableStream pipeline.
        # Test-only cache inspection never changes production exports or files.
        script = r'''const path=require('node:path'),crypto=require('node:crypto');
const {registerHooks,isBuiltin}=require('node:module');const{pathToFileURL}=require('node:url');
const root=process.argv[1],local=n=>pathToFileURL(path.join(root,n)).href,stub=s=>'data:text/javascript,'+encodeURIComponent(s);
const helperURL=local('web/e2e/user-asset-export-v3-uac-helpers.ts'),parserURL=local('web/src/directory-v4-controls-json.ts');
const tags=new WeakMap();globalThis.__uacUnitBytes=r=>r.read();globalThis.__uacUnitFailure=e=>e!==null&&typeof e==='object'?tags.get(e):undefined;
const imports=new Map([[helperURL,{'../src/directory-v4-controls-json':parserURL,'./user-asset-export-v3-helpers':stub('import{createHash}from"node:crypto";export const hash=v=>createHash("sha256").update(v).digest("hex");'),'./directory-v2-response-observer':stub('export const directoryUACResponseBytes=r=>globalThis.__uacUnitBytes(r);export const directoryUACResponseFailureCheck=e=>globalThis.__uacUnitFailure(e);')}],[parserURL,{'./api':stub('export class ApiError extends Error{}')}] ]);
const hooks=registerHooks({resolve(s,c,n){const d=imports.get(c.parentURL);if(d&&Object.hasOwn(d,s))return{url:d[s],shortCircuit:true};if(d&&!isBuiltin(s))throw new Error('unexpected_offline_dependency');return n(s,c);},load(url,c,n){const out=n(url,c);return url===helperURL?{...out,source:Buffer.from(out.source).toString()+'\nglobalThis.__uacUnitCache=safeResponses;\n'}:out;}});
let h;try{h=require(path.join(root,'web/e2e/user-asset-export-v3-uac-helpers.ts'));}finally{hooks.deregister();}
const check=x=>{if(!x)throw new Error('unit_assertion');};
const secret='UNIT_PRIVATE_ERROR_DO_NOT_REPORT';
const checks=['status','actor_header','cache_header','observed_body_read','observed_byte_binding','body_size','body_bom','body_utf8','body_json','body_object','body_privacy'];
function make(raw=JSON.stringify({nested:{value:'safe'}})){
 const r={http:200,actor:'1',cache:'no-store',reads:0,buffers:[],status(){return this.http;},headers(){return{'x-adtr-user-id':this.actor,'cache-control':this.cache};},async read(){this.reads++;const b=Buffer.isBuffer(raw)?Buffer.from(raw):Buffer.from(raw);this.buffers.push(b);return b;}};return r;
}
async function rejection(r,expected,secrets=[],actor=1,waitReady=true){let error;try{await h.safeResponse(r,actor,secrets);}catch(e){error=e;}
 check(error instanceof Error&&error.message==='uac_evidence_invalid:post_binding'&&error.cause===undefined&&!error.stack.includes(secret));
 check(h.uacFailureMessage(error,'list_read')===`uac_evidence_invalid:post_binding;stage=list_read;check=${expected}`);
 const entry=globalThis.__uacUnitCache.get(r);check(entry.state==='failed'&&entry.value===undefined&&checks.includes(entry.failure));if(waitReady)check(await entry.ready===undefined);
 return error;
}
(async()=>{
 const r=make();const [a,b]=await Promise.all([h.safeResponse(r,1,[]),h.safeResponse(r,1,[])]);check(r.reads===1&&a!==b&&a.nested!==b.nested);
 a.nested.value='changed';const c=await h.safeResponse(r,1,[]);check(c.nested.value==='safe'&&r.buffers.every(b=>b.every(x=>x===0))&&await globalThis.__uacUnitCache.get(r).ready===undefined);
 const cases=[['body_size',Buffer.alloc(0)],['body_size',Buffer.alloc((1<<20)+1)],['body_bom',Buffer.from('\uFEFF{}')],['body_utf8',Buffer.from([255])],['body_json','{"x":'],['body_json','{"x":1,"x":2}'],['body_json','{"x":1,"\\u0078":2}'],['body_object','null'],['body_object','[]'],['body_object','1'],['body_privacy','{"x":"\\ud800"}'],['body_privacy','{"x":1e999}']];
 for(const [category,raw]of cases){const x=make(raw);await rejection(x,category);check(x.reads===1&&x.buffers.every(b=>b.every(v=>v===0)));}
 for(const [field,value,category]of [['http',503,'status'],['actor','2','actor_header'],['cache','public','cache_header']]){const x=make();x[field]=value;await rejection(x,category);check(x.reads===0);}
 const headersChanged=make();await h.safeResponse(headersChanged,1,[]);headersChanged.actor='2';await rejection(headersChanged,'actor_header',[],2);check(headersChanged.reads===1);
 for(const [field,value,category]of [['http',503,'status'],['actor','2','actor_header'],['cache','public','cache_header']]){const x=make();await h.safeResponse(x,1,[]);x[field]=value;await rejection(x,category);check(x.reads===1);}
 for(const [method,category]of [['status','status'],['headers','actor_header'],['read','observed_body_read']]){const x=make();x[method]=()=>{throw new Error(secret,{cause:new Error(secret)});};await rejection(x,category);}
 const typed=make();typed.read=async()=>{const e=new Error('fixed_observed_error');tags.set(e,'observed_byte_binding');throw e;};await rejection(typed,'observed_byte_binding');
 const untyped=make();untyped.read=async()=>{const e=new Error(secret);tags.set(e,secret);throw e;};await rejection(untyped,'observed_body_read');
 let getterCalls=0;const hostile=make();hostile.read=async()=>{const e=new Error();Object.defineProperty(e,'message',{get(){getterCalls++;throw new Error(secret);}});throw e;};await rejection(hostile,'observed_body_read');check(getterCalls===0);
 const late=make('{"value":"late-private"}'),secrets=[];await h.safeResponse(late,1,secrets);secrets.push('late-private');const error=await rejection(late,'body_privacy',secrets);error.message=secret;error.stack=secret;const second=await rejection(late,'body_privacy',[]);check(error!==second&&late.reads===1);
 // Register a newly observed real parser proof, without mutating the private list.
 const proofList=[],reflected=make('{"value":"843921"}');await h.safeResponse(reflected,1,proofList);
 const source={taskUUID:'synthetic-observation',selection:{domainId:'synthetic-domain',revision:'2',credentialRevision:'1'}},expected=h.expectedUACRequest(source,'set');
 const frame={},page={mainFrame:()=>frame},key='01234567-89ab-4cde-8123-456789abcdef';
 const raw=Buffer.from(JSON.stringify({...expected,idempotencyKey:key,actorPassword:'unit-password',totpCode:'843921'}));
 await h.inspectUACCreateRequest({frame:()=>frame,method:()=>'POST',url:()=>'http://127.0.0.1:12345'+h.exportPrefix,postDataBuffer:()=>raw},expected,1,page,'http://127.0.0.1:12345',async()=>JSON.stringify({version:1,kind:'user_assets.v3.export',actorID:1,domainId:'synthetic-domain',idempotencyKey:key}),proofList);
 await rejection(reflected,'body_privacy',proofList);await rejection(reflected,'body_privacy',[]);check(reflected.reads===1);
 // Poison while a producer waits, then ensure neither producer nor other caller revives data.
 for(const kind of ['actor','secret']){const x=make('{"value":"late-private"}');let release;const barrier=new Promise(r=>release=r),read=x.read.bind(x);x.read=async()=>{await barrier;return read();};const list=[];
 const first=h.safeResponse(x,1,list).then(()=>false,e=>h.uacFailureMessage(e,'list_read'));
 if(kind==='actor'){const bad=await rejection(x,'actor_header',[],2,false);check(bad);}
 else list.push('late-private');
 release();const outcome=await first;check(outcome===`uac_evidence_invalid:post_binding;stage=list_read;check=${kind==='actor'?'actor_header':'body_privacy'}`);
 await rejection(x,kind==='actor'?'actor_header':'body_privacy',[]);check(x.reads===1&&x.buffers.every(b=>b.every(v=>v===0)));}
 // Metadata changes while awaiting are rechecked before cache data can return.
 const changing=make();let release;const gate=new Promise(r=>release=r),read=changing.read.bind(changing);changing.read=async()=>{await gate;return read();};
 const pending=h.safeResponse(changing,1,[]).then(()=>false,e=>h.uacFailureMessage(e,'list_read'));changing.cache='public';release();check(await pending==='uac_evidence_invalid:post_binding;stage=list_read;check=cache_header');await rejection(changing,'cache_header');
 const cap=make('{"x":"'+'a'.repeat((1<<20)-8)+'"}');check((await h.safeResponse(cap,1,[])).x.length===(1<<20)-8);check(cap.reads===1&&cap.buffers[0].every(v=>v===0));
 console.log('offline_safe_response_checks_cache_privacy_pass');
})().catch(()=>{console.error('offline_safe_response_checks_cache_privacy_failed');process.exitCode=1;});
'''
        result = subprocess.run(['node', '-e', script, str(ROOT)],
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), 'offline_safe_response_checks_cache_privacy_pass')

    def test_actual_native_observer_and_uac_strict_response_pipeline(self):
        # Actual TS sources run in Node before npm ci. Explicit unit seams are
        # distinct from the native observer/Response/ReadableStream pipeline.
        # Test-only cache inspection never changes production exports or files.
        script = r'''const path=require('node:path'),crypto=require('node:crypto');const{registerHooks,isBuiltin}=require('node:module'),{pathToFileURL}=require('node:url'),{EventEmitter}=require('node:events');
const root=process.argv[1],local=n=>pathToFileURL(path.join(root,n)).href,stub=s=>'data:text/javascript,'+encodeURIComponent(s);
const helperURL=local('web/e2e/user-asset-export-v3-uac-helpers.ts'),observerURL=local('web/e2e/directory-v2-response-observer.ts'),parserURL=local('web/src/directory-v4-controls-json.ts'),readerURL=local('web/src/directory-v2-json.ts');
const api=stub('export class ApiError extends Error{constructor(code){super(code);}}');
const imports=new Map([[helperURL,{'../src/directory-v4-controls-json':parserURL,'./directory-v2-response-observer':observerURL,'./user-asset-export-v3-helpers':stub('import{createHash}from"node:crypto";export const hash=v=>createHash("sha256").update(v).digest("hex");')}],[observerURL,{'../src/directory-v4-controls-json':parserURL}],[parserURL,{'./api':api}],[readerURL,{'./api':api}]]);
const hooks=registerHooks({resolve(s,c,n){const d=imports.get(c.parentURL);if(d&&Object.hasOwn(d,s))return{url:d[s],shortCircuit:true};if(d&&!isBuiltin(s))throw new Error('unexpected_offline_dependency');return n(s,c);},load(url,c,n){const out=n(url,c);return url===helperURL?{...out,source:Buffer.from(out.source).toString()+'\nglobalThis.__uacPipelineCache=safeResponses;\n'}:out;}});
let h,o,reader;try{h=require(path.join(root,'web/e2e/user-asset-export-v3-uac-helpers.ts'));o=require(path.join(root,'web/e2e/directory-v2-response-observer.ts'));reader=require(path.join(root,'web/src/directory-v2-json.ts'));}finally{hooks.deregister();}
const check=x=>{if(!x)throw new Error('unit_assertion');};const origin='http://127.0.0.1:12345';
async function harness(chunks){let register,captured,inspectorCalls=0,nativeCalls=0,takeAttempts=0,successfulTakes=0;
 const frame={page:()=>page,url:()=>origin+'/'},page={mainFrame:()=>frame,evaluate:async(fn,arg)=>fn(arg),waitForFunction:async(fn,arg)=>{const until=Date.now()+3000;while(!fn(arg)){check(Date.now()<until);await new Promise(r=>setTimeout(r,1));}}};
 class Context extends EventEmitter{async exposeBinding(_,fn){register=fn;}async addInitScript(){}}
 const context=new Context();await o.observeDirectoryV2Responses(context);
 const native=new Response(new ReadableStream({start(c){for(const chunk of chunks)c.enqueue(new Uint8Array(chunk));c.close();}}),{headers:{'Cache-Control':'no-store','X-ADTR-User-ID':'1'}});
 const nativePromise=Promise.resolve(native);
 globalThis.location=new URL(origin);globalThis.window={__adtrDirectoryV2Document:async(epoch)=>register({page,frame},epoch),fetch:(url,init)=>{nativeCalls++;
  const request={frame:()=>frame,resourceType:()=>'fetch',url:()=>origin+url,method:()=>init?.method??'GET'};context.emit('request',request);
  captured={currentActor:'1',currentCache:'no-store',url:request.url,request:()=>request,status:()=>200,headers(){return{'x-adtr-user-id':this.currentActor,'cache-control':this.currentCache};},body:async()=>{inspectorCalls++;throw new Error('UNIT_PRIVATE_INSPECTOR_ERROR');},json:async()=>{inspectorCalls++;throw new Error('UNIT_PRIVATE_INSPECTOR_ERROR');}};
  return nativePromise;
 }};window.top=window;o.installDirectoryV2ResponseObserver();const obs=window.__adtrDirectoryV2ResponseObserver,take=obs.take.bind(obs);
 obs.take=(...args)=>{takeAttempts++;const value=take(...args);successfulTakes++;return value;};
 const pending=window.fetch('/api/user-assets/v3');check(pending===nativePromise);const response=await pending;check(response===native);
 let appAccepted=true;try{await reader.readDirectoryV2JSON(response,new AbortController().signal);}catch{appAccepted=false;}
 return{response:captured,appAccepted,counts:()=>({inspectorCalls,nativeCalls,takeAttempts,successfulTakes}),record:()=>obs.peek('GET '+origin+'/api/user-assets/v3',0)};
}
async function reject(response,category,secrets=[]){let error;try{await h.safeResponse(response,1,secrets);}catch(e){error=e;}
 check(error instanceof Error&&error.cause===undefined&&h.uacFailureMessage(error,'list_read')===`uac_evidence_invalid:post_binding;stage=list_read;check=${category}`);
 check(!error.stack.includes('UNIT_PRIVATE'));const entry=globalThis.__uacPipelineCache.get(response);check(entry.state==='failed'&&entry.value===undefined&&await entry.ready===undefined);return error;}
(async()=>{
 const valid=Buffer.from('{ "nested": {"x":"😀"}, "escape":"\\u0078" }');
 const good=await harness(Array.from(valid,b=>Buffer.from([b])));check(good.appAccepted);
 const [a,b]=await Promise.all([h.safeResponse(good.response,1,[]),h.safeResponse(good.response,1,[])]);check(a!==b&&a.nested!==b.nested&&a.nested.x==='😀');a.nested.x='changed';check((await h.safeResponse(good.response,1,[])).nested.x==='😀');
 check(JSON.stringify(good.counts())===JSON.stringify({inspectorCalls:0,nativeCalls:1,takeAttempts:1,successfulTakes:1}));check(good.record().text==='');
 const bom=Buffer.from([239,187,191]);for(const chunks of [[Buffer.concat([bom,valid])],[bom.subarray(0,1),bom.subarray(1),valid],[bom.subarray(0,2),bom.subarray(2),valid],[bom.subarray(0,1),bom.subarray(1,2),bom.subarray(2),valid],[Buffer.alloc(0),bom.subarray(0,1),Buffer.alloc(0),bom.subarray(1,2),Buffer.alloc(0),bom.subarray(2),valid]]){
  const x=await harness(chunks);check(x.appAccepted);await reject(x.response,'observed_byte_binding');check(x.counts().inspectorCalls===0&&x.counts().successfulTakes===1&&x.record().text==='');
 }
 for(const raw of ['{"x":1,"x":2}','{"x":1,"\\u0078":2}']){const x=await harness([Buffer.from(raw)]);check(x.appAccepted);await reject(x.response,'body_json');check(x.counts().inspectorCalls===0);}
 const invalid=await harness([Buffer.from('{"x":"'),Buffer.from([255]),Buffer.from('"}')]);check(!invalid.appAccepted);await reject(invalid.response,'observed_body_read');
 const exact=await harness([Buffer.from('{"x":"'+'a'.repeat((1<<20)-8)+'"}')]);check((await h.safeResponse(exact.response,1,[])).x.length===(1<<20)-8);
 const over=await harness([Buffer.from('{"x":"'+'a'.repeat((1<<20)-7)+'"}')]);check(over.appAccepted);await reject(over.response,'observed_byte_binding');
 const privateValue=await harness([Buffer.from('{"x":"UNIT_PRIVATE_VALUE"}')]);const secrets=[];await h.safeResponse(privateValue.response,1,secrets);secrets.push('UNIT_PRIVATE_VALUE');await reject(privateValue.response,'body_privacy',secrets);await reject(privateValue.response,'body_privacy',[]);check(privateValue.counts().successfulTakes===1&&privateValue.counts().inspectorCalls===0);
 const metadata=await harness([valid]);await h.safeResponse(metadata.response,1,[]);metadata.response.currentCache='public';await reject(metadata.response,'cache_header');check(metadata.counts().successfulTakes===1);
 console.log('offline_actual_native_observer_uac_pipeline_pass');
})().catch(()=>{console.error('offline_actual_native_observer_uac_pipeline_failed');process.exitCode=1;});
'''
        result = subprocess.run(['node', '-e', script, str(ROOT)],
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), 'offline_actual_native_observer_uac_pipeline_pass')

    def test_actual_spec_closed_failure_stages_and_secret_errors(self):
        # Execute the actual spec and its safety primitives with closed offline
        # browser/IO seams. The load hook only injects throws immediately after
        # stage assignments; it does not replace the real outer catch/finally.
        # This establishes diagnostics, never real browser/HTTP acceptance.
        data, files, _ = specimen()
        envelope = json.loads(files[v.RAW_NAME])
        payload = dict(data=data, files={name: b64(raw) for name, raw in files.items()},
                       oracle=b64((ROOT / 'scripts/fixtures/user_uac_v3_mixed33_oracle.json').read_bytes()),
                       wire=envelope['candidateTranscriptBase64'], nonce=NONCE, codes=sorted(v.CODES))
        script = r'''const fs = require('node:fs'), path = require('node:path'), crypto = require('node:crypto');
const { registerHooks, isBuiltin } = require('node:module');
const { pathToFileURL } = require('node:url');
const root = process.argv[1], unit = JSON.parse(fs.readFileSync(0, 'utf8'));
const local = (name) => pathToFileURL(path.join(root, name)).href;
const stub = (s) => 'data:text/javascript,' + encodeURIComponent(s);
const helperURL = local('web/e2e/user-asset-export-v3-uac-helpers.ts');
const specURL = local('web/e2e/user-asset-export-v3-uac-mixed33.spec.ts');
const parserURL = local('web/src/directory-v4-controls-json.ts');
const check = (v) => { if (!v) throw new Error('unit_assertion'); };
const hash = (v) => crypto.createHash('sha256').update(v).digest('hex');
const bytes = Object.fromEntries(Object.entries(unit.files).map(([k,v]) => [k, Buffer.from(v,'base64')]));
const origin = 'http://127.0.0.1:12345', exportPrefix = '/api/user-assets/v3/exports';
const headers = ['x-adtr-user-id','cache-control','x-content-type-options','content-type','content-length','x-adtr-artifact-sha256','x-adtr-row-count','content-disposition'];
process.env.ADTR_E2E_BASE_URL = origin;
process.env.ADTR_LDAP_FIXTURE_UAC_RUN_NONCE = unit.nonce;
process.env.ADTR_E2E_LDAP_UAC_EVIDENCE_DIR = '/unit-owned';
let callback, target, injected, stages, tabs, mode, index, budgetCalls, writes, closed, waits;
let mainPage;
const fixture = { taskUUID: unit.data.source.taskUUID, actorId: 2, secrets: [], consoles: [], bootstrap: {} };
function file(name) {
 const key = path.basename(name).replace(/^task-result-/, '');
 if (key === 'user_uac_v3_mixed33_oracle.json') return Buffer.from(unit.oracle, 'base64');
 if (key === 'user-asset-export-v3-uac-provenance.json') return Buffer.from(JSON.stringify(unit.data.provenance));
 if (key === 'candidate-wire.json') return Buffer.from(unit.wire,'base64');
 check(Object.hasOwn(bytes,key)); return bytes[key];
}
const stat = (name) => ({ isSymbolicLink: () => false, isFile: () => true, size: file(name).length, ino: 1, dev: 1 });
const fsMock = {
 lstat: async (name) => ({ isSymbolicLink: () => false, isFile: () => true, get size() { return file(name).length; }, ino: 1, dev: 1 }),
 open: async (name) => { let at = 0; return { stat: async () => stat(name), close: async () => {}, read: async (out, off, len) => {
   const b = file(name), n = Math.min(len, b.length-at); b.copy(out, off, at, at+n); at += n; return { bytesRead: n };
 } }; },
 readFile: async (name) => file(name), writeFile: async () => { writes++; }, unlink: async () => {},
};
const currentCase = () => unit.data.cases.find(c => c.name === mode);
function response(pathname, value, extra={}) {
 return { url: () => origin + pathname, request: () => ({ method: () => pathname === exportPrefix ? 'POST' : 'GET' }),
 status: () => 200, headers: () => ({ 'x-adtr-user-id':'1','cache-control':'no-store',...extra }),
 body: async () => Buffer.from(JSON.stringify(value)), json: async () => value };
}
function emit(page, value) {
 for (const item of [...waits]) if (item.page === page && item.kind === 'response' && item.predicate(value)) {
  waits.splice(waits.indexOf(item),1); item.resolve(value);
 }
}
function emitDownload(page) {
 const c = currentCase();
 for (const item of [...waits]) if(item.page === page && item.kind === 'download') {
  waits.splice(waits.indexOf(item),1);item.resolve({ suggestedFilename: () => c.download.filename, saveAs: async () => {} });
 }
 emit(page,response(exportPrefix+'/download',{},c.download.headers));
 emit(page,response(exportPrefix+'/detail',c.detail));
}
function locator(page, name) {
 const click = async () => {
  if(name === '搜索用户' || name === '用户资产下一页') {
   index = name === '搜索用户' ? 1 : index+1;
   const p=unit.data.listPages.find(p=>p.mode===mode && p.pageIdx===index);check(p);
   emit(page,response('/api/user-assets/v3?'+new URLSearchParams(p.queryEntries),p.response));
  } else if(name==='下载用户 XLSX' || name==='下载原用户 XLSX') emitDownload(page);
  else if(name==='查看原导出结果') emit(page,response(exportPrefix+'/detail',currentCase().detail));
 };
 return { click, dblclick: async () => emit(page,response(exportPrefix,currentCase().create)),
 fill: async () => {}, selectOption: async (value) => { if(name==='本次观测的禁用位') mode=value; },
 uncheck: async () => {},check: async () => {}, innerText: async () => currentCase().request.screenColumns.join(' → '),
 getByRole: (_,c) => locator(page,c.name),getByLabel: (n) => locator(page,n),isVisible: async () => true };
}
function pageObject(original=false) {
 let isClosed=false;
 const p={ setViewportSize: async()=>{}, getByText:(n)=>locator(p,n),getByLabel:(n)=>locator(p,n),
 getByRole:(_,c)=>locator(p,c.name),locator:(n)=>locator(p,n),keyboard:{press:async()=>{}},
 waitForResponse:(predicate)=>new Promise(resolve=>waits.push({page:p,kind:'response',predicate,resolve})),
 waitForEvent:()=>new Promise(resolve=>waits.push({page:p,kind:'download',resolve})),
 opener:async()=>null,goto:async()=>emit(p,response('/api/auth/me',{ID:1})),url:()=>origin+'/#tasks',evaluate:async()=>null,
 close:async()=>{if(!isClosed){isClosed=true;closed++;tabs=tabs.filter(t=>t!==p);}},isClosed:()=>isClosed };
 return p;
}
const context={ pages:()=>tabs,newPage:async()=>{const p=pageObject(true);tabs.push(p);return p;} };
const expectMock=()=>({toBeEnabled:async()=>{},toBeVisible:async()=>{}});
const testMock=(_,fn)=>{callback=fn;};testMock.use=()=>{};testMock.setTimeout=()=>{};
globalThis.__uacMocks = {test:testMock,expect:expectMock,navigateTo:async()=>{}, setupUACGroupFixture:async()=>fixture,
 proof:async()=>{},readJSON:async()=>({ID:1}),observeDirectoryV2Responses:async()=>{},downloadHeaderNames:headers,hash,
 switchToExportOwner:async()=>{},openAssets:async()=>unit.data.listPages[0].response,
 sensitiveBudget:async()=>{const at=budgetCalls++;return at===0?unit.data.sensitiveBudget.before:at<=3?unit.data.sensitiveBudget.afterEach[at-1]:unit.data.sensitiveBudget.after;},
 captureUACReadOnlyScreenshot:async()=>{},storedUACSource:async()=>unit.data.source,storedUACExport:async()=>currentCase().database,
 readUACOriginalReceipt:async()=>currentCase().receipt,
 observeUACExportWrites:()=>({armCase:()=>{},settleCase:async()=>{},closeAndSnapshot:async()=>unit.data.postLedger}),...fsMock};
const stubExports = (names) => stub(names.map(n=>`export const ${n}=globalThis.__uacMocks.${n};`).join('\n'));
const helperStub = stub('export const '+['captureUACReadOnlyScreenshot','storedUACSource','storedUACExport','readUACOriginalReceipt','observeUACExportWrites'].map(n=>`${n}=globalThis.__uacMocks.${n}`).join(',')+';\n'+['expectedUACRequest','safeResponse','safeValue','sameValue','uacRequire','uacFailureMessage','exportPrefix'].map(n=>`export const ${n}=globalThis.__actualUAC.${n};`).join('\n'));
const imports = new Map([
 [helperURL,{'../src/directory-v4-controls-json':parserURL,'./user-asset-export-v3-helpers':stubExports(['hash']),
 './directory-v2-response-observer':stub('export const directoryUACResponseBytes = async (response) => response.body(); export const directoryUACResponseFailureCheck = (error) => globalThis.__uacObservedCheck?.(error);')}],
 [parserURL,{'./api':stub('export class ApiError extends Error {}')}],
 [specURL,{'@playwright/test':stubExports(['test','expect']),'node:fs/promises':stubExports(Object.keys(fsMock)),
 './navigation':stubExports(['navigateTo']),'./group-assets-v3-fixture':stubExports(['setupUACGroupFixture','proof','readJSON']),
 './directory-v2-response-observer':stubExports(['observeDirectoryV2Responses']),
 './user-asset-export-v3-helpers':stubExports(['downloadHeaderNames','hash','switchToExportOwner','openAssets','sensitiveBudget']),
 './user-asset-export-v3-uac-helpers':helperStub}],
]);
globalThis.__uacEnterStage=(value)=>{stages.push(value);if(value===target)throw injected;};
const hooks=registerHooks({resolve(specifier,context,next){
 const map=imports.get(context.parentURL);if(map&&Object.hasOwn(map,specifier))return{url:map[specifier],shortCircuit:true};
 if(map&&!isBuiltin(specifier))throw new Error('unexpected_offline_dependency');return next(specifier,context);
},load(url,context,next){const out=next(url,context);if(url!==specURL)return out;
 let source=Buffer.from(out.source).toString();
 source=source.replace(/(\bstage = "([a-z_]+)";)/g,'$1 globalThis.__uacEnterStage("$2");');
 source=source.replace('  try {\n    const origin','  try {\n    globalThis.__uacEnterStage("configuration");\n    const origin');
 return {...out,source};
}});
let h;try{h=require(path.join(root,'web/e2e/user-asset-export-v3-uac-helpers.ts'));globalThis.__actualUAC=h;require(path.join(root,'web/e2e/user-asset-export-v3-uac-mixed33.spec.ts'));}finally{hooks.deregister();}
const secret='UNIT_SECRET_DO_NOT_REPORT_843921_01234567-89ab-4cde-8123-456789abcdef';
async function run(at,error){target=at;injected=error;stages=[];mode='omitted';index=0;budgetCalls=0;writes=0;closed=0;waits=[];mainPage=pageObject();tabs=[mainPage];
 let failure;try{await callback({page:mainPage,context},{outputPath:(n)=>'/unit-output/'+n});}catch(e){failure=e;}
 return {failure,stages:[...stages],writes,closed};
}
(async()=>{
 const success=await run(null,null);check(!success.failure&&success.writes===1&&success.closed===4);
 const stageSet=[...new Set(success.stages)];check(stageSet.length===h.uacFailureStages.length-1);
 for(const stage of stageSet)for(const error of [new Error(secret,{cause:new Error(secret)}),new Error('uac_evidence_invalid:post_count')]){
  error.stack=secret;const result=await run(stage,error);
  const code=error.message==='uac_evidence_invalid:post_count'?'post_count':'unexpected';
  check(result.failure instanceof Error && result.failure.message===`uac_evidence_invalid:${code};stage=${stage}`);
  check(result.writes===0 && result.closed>=1 && result.failure.cause===undefined && !result.failure.stack.includes(secret));
 }
 let getterCalls=0;const getterError=new Error();Object.defineProperty(getterError,'message',{get(){getterCalls++;throw new Error(secret);}});
 for(const error of [new Error('uac_evidence_invalid:post_count:'+secret),new Error('uac_evidence_invalid:post_count\n'+secret),
  {message:'uac_evidence_invalid:post_count'},secret,getterError,new Proxy(new Error(),{getOwnPropertyDescriptor(){throw new Error(secret);}})]){
  check(h.uacFailureMessage(error,'initial_list')==='uac_evidence_invalid:unexpected;stage=initial_list');
 }
 check(getterCalls===0 && h.uacFailureMessage(new Error(secret),secret)==='uac_evidence_invalid:unexpected;stage=unexpected');
 for(const code of unit.codes)check(h.uacFailureMessage(new Error('uac_evidence_invalid:'+code),'initial_list')===`uac_evidence_invalid:${code};stage=initial_list`);
 // Pass actual safeResponse-owned closed checks through the actual spec catch.
 const observedChecks=new WeakMap();globalThis.__uacObservedCheck=e=>e&&typeof e==='object'?observedChecks.get(e):undefined;
 const predicateCases=[['status',{}],['actor_header',{}],['cache_header',{}],['observed_body_read',{}],['observed_byte_binding',{}],
  ['body_size',Buffer.alloc(0)],['body_bom',Buffer.from('\uFEFF{}')],['body_utf8',Buffer.from([255])],['body_json',Buffer.from('{"x":1,"x":2}')],
  ['body_object',Buffer.from('[]')],['body_privacy',Buffer.from(JSON.stringify({value:secret}))]];
 for(const [category,raw]of predicateCases){
  const r=response('/api/user-assets/v3',{});
  if(category==='status')r.status=()=>503;
  if(category==='actor_header')r.headers=()=>({'x-adtr-user-id':'2','cache-control':'no-store'});
  if(category==='cache_header')r.headers=()=>({'x-adtr-user-id':'1','cache-control':'public'});
  if(category.startsWith('observed_'))r.body=async()=>{const e=new Error(secret,{cause:new Error(secret)});if(category==='observed_byte_binding')observedChecks.set(e,category);throw e;};
  else if(Buffer.isBuffer(raw))r.body=async()=>Buffer.from(raw);
  let tagged;try{await h.safeResponse(r,1,[secret]);}catch(e){tagged=e;}
  check(tagged instanceof Error);const result=await run('list_read',tagged);
  check(result.failure instanceof Error&&result.failure.message===`uac_evidence_invalid:post_binding;stage=list_read;check=${category}`);
  check(result.writes===0&&result.closed>=1&&result.failure.cause===undefined&&!result.failure.stack.includes(secret));
 }
 console.log('offline_actual_spec_closed_diagnostic_pass stages='+stageSet.length+' injections='+stageSet.length*2+' response_checks='+predicateCases.length);
})().catch(()=>{console.error('offline_actual_spec_closed_diagnostic_failed');process.exitCode=1;});
'''
        result = subprocess.run(['node', '-e', script, str(ROOT)], input=json.dumps(payload),
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(),
                         'offline_actual_spec_closed_diagnostic_pass stages=29 injections=58 response_checks=11')

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
        self.assertNotIn('response.body()', helper)
        self.assertNotIn('directoryV2ResponseJSON', helper)
        self.assertIn('await directoryUACResponseBytes(response)', helper)
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


class PythonDiagnosticTests(unittest.TestCase):
    def test_exact_codes_owned_stages_kinds_and_safe_defaults(self):
        self.assertEqual(ci._UAC_CODES, v.CODES)
        self.assertIn('runner_main', ci._UAC_STAGES)
        required = set('ldap_stop ldap_inspect ldap_exit ldap_retained_read ldap_container_remove ldap_network_remove ldap_private_remove ldap_evidence_open ldap_evidence_file ldap_evidence_read ldap_evidence_json ldap_evidence_schema ldap_evidence_binding ldap_evidence_counts ldap_evidence_state ldap_evidence_sequence candidate_discovery cleanup_snapshot'.split())
        self.assertTrue(required <= ci._UAC_STAGES)
        for code in v.CODES:
            raw = v.UACEvidenceError(code)
            self.assertEqual(ci.uac_failure_message(raw), 'uac_evidence_invalid:' + code + ';stage=runner_main')
            for stage in ci._UAC_STAGES:
                for kind in ('evidence', 'runtime', 'value'):
                    error = ci.uac_diagnostic_error(code, stage, kind=kind)
                    parent = v.UACEvidenceError if kind == 'evidence' else RuntimeError if kind == 'runtime' else ValueError
                    self.assertIs(type(error), parent)
                    self.assertEqual(BaseException.args.__get__(error), ('uac_evidence_invalid:' + code,))
                    self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:' + code + ';stage=' + stage)
                    self.assertIsNot(error, ci.uac_diagnostic_error(code, stage, kind=kind))
        for error in (RuntimeError('uac_evidence_invalid:cleanup'), ValueError('uac_evidence_invalid:terminal_seal'),
                      Exception('uac_evidence_invalid:publication'), None, {'message': 'uac_evidence_invalid:cleanup'}):
            self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
        self.assertEqual(ci.uac_failure_stage(None, fallback='candidate_discovery'), 'candidate_discovery')
        self.assertEqual(ci.uac_failure_code(None, fallback='cleanup'), 'cleanup')
        self.assertEqual(ci.uac_failure_message(ci.uac_diagnostic_error(None, object(), kind=None)),
                         'uac_evidence_invalid:publication;stage=runner_main')

    def test_hostile_types_args_tags_and_chains_never_read_or_report(self):
        calls = []
        secret = 'UNIT_PRIVATE_ERROR_DO_NOT_REPORT'
        class Meta(type):
            def __eq__(self, other): calls.append('meta-eq'); raise RuntimeError(secret)
            def __hash__(self): calls.append('meta-hash'); raise RuntimeError(secret)
        class Hostile(Exception, metaclass=Meta):
            def __getattribute__(self, name): calls.append('getattribute'); raise RuntimeError(secret)
            def __str__(self): calls.append('str'); raise RuntimeError(secret)
            def __repr__(self): calls.append('repr'); raise RuntimeError(secret)
        class ValidatorSubclass(v.UACEvidenceError):
            @property
            def args(self): calls.append('args'); raise RuntimeError(secret)
            def __str__(self): calls.append('str'); raise RuntimeError(secret)
        class StringSubclass(str):
            def __hash__(self): calls.append('string-hash'); raise RuntimeError(secret)
            def __eq__(self, other): calls.append('string-eq'); raise RuntimeError(secret)
        class TupleSubclass(tuple):
            def __len__(self): calls.append('tuple-len'); raise RuntimeError(secret)
        class DictSubclass(dict):
            def get(self, *args): calls.append('dict-get'); raise RuntimeError(secret)
        # Even an override on the known Python validator class must not run:
        # its original builtin BaseException storage is the sole args source.
        def args_getter(error):
            calls.append('known-args-getter')
            raise RuntimeError(secret)
        known = v.UACEvidenceError('cleanup')
        with patch.object(v.UACEvidenceError, 'args', property(args_getter)):
            self.assertEqual(ci.uac_failure_message(known), 'uac_evidence_invalid:cleanup;stage=runner_main')
        for error in (Hostile(secret), ValidatorSubclass('cleanup')):
            self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
        for args in [(secret,), ('uac_evidence_invalid:cleanup:' + secret,),
                     ('uac_evidence_invalid:cleanup\n' + secret,),
                     (StringSubclass('uac_evidence_invalid:cleanup'),), ('uac_evidence_invalid:cleanup', secret), ()]:
            error = v.UACEvidenceError('cleanup'); error.args = args
            self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
        for bad in (StringSubclass('cleanup'), Hostile(secret), None, [], {}):
            error = ci.uac_diagnostic_error(bad, bad, kind=bad)
            self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
            self.assertEqual(ci.uac_failure_stage(None, fallback=bad), 'runner_main')
            self.assertEqual(ci.uac_failure_code(None, fallback=bad), 'publication')
        owned = ci.uac_diagnostic_error('cleanup', 'cleanup_worker', kind='runtime')
        marker, code, stage = object.__getattribute__(owned, '__dict__')[ci._UAC_DIAGNOSTIC_ATTRIBUTE]
        for tag in (None, {}, [marker, code, stage], TupleSubclass((marker, code, stage)),
                    (object(), code, stage), (marker, StringSubclass(code), stage),
                    (marker, code, StringSubclass(stage)), (marker, code, secret),
                    (marker, 'provenance', stage), (marker, code, stage, secret)):
            error = ci.uac_diagnostic_error('cleanup', 'cleanup_worker', kind='runtime')
            object.__getattribute__(error, '__dict__')[ci._UAC_DIAGNOSTIC_ATTRIBUTE] = tag
            self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
        error = ci.uac_diagnostic_error('cleanup', 'cleanup_worker', kind='runtime')
        error.__dict__ = DictSubclass(error.__dict__)
        self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:publication;stage=runner_main')
        error = ci.uac_diagnostic_error('cleanup', 'cleanup_worker', kind='runtime')
        error.__cause__ = Hostile(secret); error.__context__ = Hostile(secret)
        error.private_value = secret
        self.assertEqual(ci.uac_failure_message(error), 'uac_evidence_invalid:cleanup;stage=cleanup_worker')
        # Exact dict does not imply safe lookup: an alien key may collide with
        # the private name and run __eq__ before a get()/subscription can fail.
        import contextlib
        enabled = False
        class CollidingKey:
            def __hash__(self): return hash(ci._UAC_DIAGNOSTIC_ATTRIBUTE)
            def __eq__(self, other):
                if enabled:
                    calls.append('dict-key-eq')
                    print(secret)
                return False
        for include_tag in (False, True):
            error = ci.uac_diagnostic_error('cleanup', 'cleanup_worker', kind='runtime')
            tag = error.__dict__[ci._UAC_DIAGNOSTIC_ATTRIBUTE]
            attributes = {CollidingKey(): secret}
            if include_tag: attributes[ci._UAC_DIAGNOSTIC_ATTRIBUTE] = tag
            error.__dict__ = attributes
            enabled = True
            output = io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                message = ci.uac_failure_message(error)
            enabled = False
            expected = 'cleanup;stage=cleanup_worker' if include_tag else 'publication;stage=runner_main'
            self.assertEqual(message, 'uac_evidence_invalid:' + expected)
            self.assertTrue(output.getvalue() == '', 'closed output')
        self.assertEqual(calls, [])

    def test_actual_finalizer_all_entered_stages_keep_codes_and_no_success(self):
        import ast
        import inspect
        node = ast.parse(inspect.getsource(ci.finalize_uac_evidence)).body[0]
        boundary = next(item for item in node.body if isinstance(item, ast.Try))
        def enter(stage):
            return ast.Expr(ast.Call(ast.Name('_unit_enter', ast.Load()), [ast.Constant(stage)], []))
        body = [enter('finalize_preflight')]
        for item in boundary.body:
            body.append(item)
            if (isinstance(item, ast.Assign) and len(item.targets) == 1
                    and isinstance(item.targets[0], ast.Name) and item.targets[0].id == 'stage'):
                body.append(enter(item.value.value))
        boundary.body = body
        compiled = compile(ast.fix_missing_locations(ast.Module([node], type_ignores=[])), '<actual-finalizer-stage-probe>', 'exec')
        stages = []
        def run(target=None, injected=None):
            unit = PublicationTests(); unit.setUp()
            try:
                def entered(stage):
                    if target is None: stages.append(stage)
                    if stage == target: raise injected
                scope = dict(ci.__dict__, _unit_enter=entered)
                exec(compiled, scope)
                args = dict(repo=unit.root, run_nonce=NONCE, provenance_bytes=unit.provenance,
                            candidate_path=unit.candidate, retained_ldap=unit.retained,
                            cleanup_receipt=unit.data['cleanupReceipt'])
                if target is None:
                    result = scope['finalize_uac_evidence'](**args)
                    self.assertEqual(len(result.files), 9)
                else:
                    caught = None
                    try: scope['finalize_uac_evidence'](**args)
                    except Exception as error: caught = error
                    self.assertIs(type(caught), v.UACEvidenceError)
                    code = 'task_binding' if type(injected) is v.UACEvidenceError else 'publication'
                    self.assertEqual(ci.uac_failure_message(caught), 'uac_evidence_invalid:' + code + ';stage=' + target)
                    self.assertIsNot(caught, injected)
                    self.assertFalse((unit.root / ci.ARTIFACTS).exists())
                    self.assertEqual(list((unit.root / ci.ARTIFACTS).parent.glob('.uac-validated-*')), [])
            finally: unit.doCleanups()
        run()
        self.assertEqual(len(stages), 16)
        self.assertEqual(len(set(stages)), 16)
        for stage in stages:
            run(stage, OSError('UNIT_PRIVATE_ERROR_DO_NOT_REPORT'))
            run(stage, v.UACEvidenceError('task_binding'))

    def test_actual_finalizer_preserves_owned_stage_and_primary_over_cleanup_failure(self):
        unit = PublicationTests(); unit.setUp()
        try:
            error = ci.uac_diagnostic_error('terminal_seal', 'ldap_evidence_state', kind='runtime')
            with patch.object(ci, '_write_private', side_effect=error), patch.object(ci.shutil, 'rmtree', side_effect=OSError('UNIT_PRIVATE_CLEANUP_ERROR')) as remove:
                caught = None
                try: unit.finalize()
                except Exception as failure: caught = failure
                self.assertIs(type(caught), v.UACEvidenceError)
                self.assertEqual(ci.uac_failure_message(caught), 'uac_evidence_invalid:terminal_seal;stage=ldap_evidence_state')
                self.assertIsNot(caught, error)
                self.assertFalse((unit.root / ci.ARTIFACTS).exists())
                remove.assert_called_once()
        finally: unit.doCleanups()

    def test_python_cli_uses_closed_formatter_for_unknown_and_known_errors(self):
        import ast
        source = (ROOT / 'scripts/user_asset_export_v3_uac_ci.py').read_text()
        tree = ast.parse(source)
        node = next(item for item in tree.body if isinstance(item, ast.FunctionDef) and item.name == 'main')
        self.assertNotIn('str(error)', ast.get_source_segment(source, node))
        self.assertIn('uac_failure_message(error)', ast.get_source_segment(source, node))
        for error, expected in [(RuntimeError('UNIT_PRIVATE_CLI_ERROR'), 'publication'), (v.UACEvidenceError('provenance'), 'provenance')]:
            with patch('sys.argv', ['uac_ci', 'capture', '--repo', '/unit-only']), patch.object(ci, 'capture_uac_provenance', side_effect=error), patch('sys.stderr', new_callable=io.StringIO) as stderr:
                self.assertEqual(ci.main(), 1)
                self.assertEqual(stderr.getvalue(), 'uac_evidence_invalid:' + expected + ';stage=runner_main\n')



class ObserverDiagnosticTests(unittest.TestCase):
    """Actual C helper diagnostics; synthetic inputs, never current CI evidence."""

    def _run_probe(self, source):
        with tempfile.TemporaryDirectory() as directory:
            script = Path(directory) / 'probe.mjs'
            script.write_text(source)
            result = subprocess.run(['node', str(script), str(ROOT)], cwd=ROOT,
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    timeout=60, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, '')
        return json.loads(result.stdout)

    def test_actual_host_observer_thirteen_scenarios_keep_ledger_and_classify(self):
        result = self._run_probe(r"""
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { registerHooks, isBuiltin, createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { EventEmitter } from 'node:events';
const root=path.resolve(process.argv[2]);
const local=n=>pathToFileURL(path.join(root,n)).href;
const stub=s=>'data:text/javascript,'+encodeURIComponent(s);
const helperURL=local('web/e2e/user-asset-export-v3-uac-helpers.ts'), observerURL=local('web/e2e/directory-v2-response-observer.ts'), parserURL=local('web/src/directory-v4-controls-json.ts');
const deps=new Map([[helperURL,{'../src/directory-v4-controls-json':parserURL,'./directory-v2-response-observer':observerURL,'./user-asset-export-v3-helpers':stub('import {createHash} from "node:crypto";export const hash=v=>createHash("sha256").update(v).digest("hex");')}],[observerURL,{'../src/directory-v4-controls-json':parserURL}],[parserURL,{'./api':stub('export class ApiError extends Error {constructor(){super("invalid_response");}}')}]]);
const hook=registerHooks({resolve(s,c,n){const d=deps.get(c.parentURL);if(d&&Object.hasOwn(d,s))return{url:d[s],shortCircuit:true};if(d&&!isBuiltin(s))throw Error('unexpected_offline_dependency');return n(s,c);}});
const require=createRequire(import.meta.url);let h,o;try{h=require(path.join(root,'web/e2e/user-asset-export-v3-uac-helpers.ts'));o=require(path.join(root,'web/e2e/directory-v2-response-observer.ts'));}finally{hook.deregister();}
const check=(x,label)=>{if(!x)throw Error('probe_assertion:'+label)};
const hash=b=>crypto.createHash('sha256').update(b).digest('hex');
const origin='http://127.0.0.1:12345';
const source={taskUUID:'synthetic-observation',selection:{domainId:'synthetic-domain',revision:'2',credentialRevision:'1'}};
const keys=['abcdefab-cdef-4abc-8def-abcdefabcdef','fedcbafe-dcba-4fed-8cba-fedcbafedcba','aabbccdd-eeff-4aab-8ccd-eeffaabbccdd'];
const modes=['set','clear','not_returned'];
const codes=['135790','246802','357913'];
const closed=async(fn,stage)=>{try{return{ok:true,value:await fn()}}catch(e){return{ok:false,message:h.uacFailureMessage(e,stage)}}};
async function runScenario(name,variation={}){
 let storage,gate,register,readCount=0,takes=0,bodyCalls=0,finalCount=0,ledgerSHA256=null;
 const records=new Map(), counts=new Map();
 const secrets=[];
 const frame={page:()=>page,url:()=>origin+'/'};
 const locator=(key)=>JSON.stringify({version:1,kind:'user_assets.v3.export',actorID:1,domainId:source.selection.domainId,idempotencyKey:key});
 const page={mainFrame:()=>frame,isClosed:()=>false,
  waitForFunction:async()=>{},
  evaluate:async(_fn,arg)=>{
   if(typeof arg==='string'){readCount++;if(gate)await gate.wait;return storage;}
   const rec=records.get(arg.id)?.[arg.identity.ordinal];check(rec,'record');check(!rec.taken,'atomic_take');if(arg.take){rec.taken=true;takes++;}
   return{count:counts.get(arg.id),record:{...rec,identity:{...rec.identity}}};
  },
  getByRole:()=>({getByRole:()=>({click:async()=>{}})}),
  waitForResponse:async()=>{throw Error('unarmed_receipt');}
 };
 class Context extends EventEmitter{constructor(){super();this.tabs=[page];}pages(){return this.tabs;}async exposeBinding(_n,f){register=f}async addInitScript(){}}
 const context=new Context();await o.observeDirectoryV2Responses(context);register({page,frame},'unit-document');
 const observer=h.observeUACExportWrites(context,origin,secrets);
 function requestFor(method,pathname,value,body,options={}){
  const url=origin+pathname,id=method+' '+url,ordinal=counts.get(id)||0;
  const raw=Buffer.from(JSON.stringify(value));const identity={epoch:'unit-document',method,url,ordinal};
  const record={identity,state:'complete',status:200,actor:'1',cache:'no-store',text:raw.toString('utf8'),byteLength:raw.length,sha256:hash(raw),eof:true,readers:1,released:1,cancelCalls:0,signalAborted:false,completedRead:true,taken:false};
  records.set(id,[...(records.get(id)||[]),record]);counts.set(id,ordinal+1);
  const req={frame:()=>frame,resourceType:()=>'fetch',url:()=>url,method:()=>method,postDataBuffer:()=>Buffer.from(JSON.stringify(body)),response:async()=>response};
  const response={request:()=>req,url:()=>url,status:()=>200,headers:()=>({'x-adtr-user-id':'1','cache-control':'no-store'}),body:async()=>{bodyCalls++;throw Error('inspector_body_must_not_be_used');}};
  context.emit('request',req);return{req,response};
 }
 const cases=[], seen=[];
 for(let i=0;i<3;i++){
  const mode=modes[i],exp=h.expectedUACRequest(source,mode);let key=keys[i];
  if(i===2&&variation.keyOldProofCollision)key='135790ab-cdef-4abc-8def-abcdefabcdef';
  storage=locator(key);
  const responseValue={taskID:'export-'+mode,task:{state:'queued'},request:exp,replayed:false};
  if(i===2&&variation.currentKeyReflection)responseValue.task.note=key;
  if(i===2&&variation.lateProof)responseValue.task.note='late-proof-marker';
  if(i===2&&variation.extraResponseKey)responseValue.extra=true;
  if(i===2&&variation.badTaskID)responseValue.taskID='platform';
  const postBody={...exp,idempotencyKey:key,actorPassword:'unit-private-password',totpCode:codes[i]};
  observer.armCase(mode,exp,1,page);
  if(i===2&&variation.deferLocator){let release;gate={wait:new Promise(r=>release=r),release:()=>{release();gate=undefined;}};}
  const beforeTakes=takes;
  const x=requestFor('POST',h.exportPrefix,responseValue,postBody); seen.push(x);
  const main=await closed(()=>h.safeResponse(x.response,1,secrets),'export_submit');
  check(main.ok,'main_pass:'+name);
  check(typeof main.value.taskID==='string'&&main.value.replayed===false&&h.sameValue(main.value.request,exp),'actual_main_predicates');
  if(i===2&&variation.mutateReturnedClone)main.value.task.secret='unit-private-password';
  if(i===2&&variation.lateProof)secrets.push('late-proof-marker');
  if(i===2&&variation.locatorPrevious)storage=locator(keys[1]);
  if(i===2&&variation.locatorMissing)storage=null;
  if(i===2&&variation.duplicate)requestFor('POST',h.exportPrefix,responseValue,postBody);
  if(i===2&&variation.requestFailed)context.emit('requestfailed',x.req);
  if(i===2&&variation.priorRequestFailed)context.emit('requestfailed',seen[0].req);
  if(gate)gate.release();
  const settled=await closed(()=>observer.settleCase(main.value.taskID),'export_settle');
  cases.push({mode,mainSafeResponse:true,mainPredicates:true,settled:settled.ok,closedFailure:settled.message??null,bodyTakes:takes-beforeTakes});
  if(!settled.ok){check(i===2&&variation.expectReject,'expected_third_rejection:'+name);const expectedBranch = variation.locatorPrevious ? 'request_inspection;case=not_returned;relation=current;inspect=key_equality'
 : variation.locatorMissing ? 'request_inspection;case=not_returned;relation=current;inspect=locator_shape'
 : variation.duplicate ? 'duplicate_create;case=not_returned;relation=current'
 : variation.priorRequestFailed ? 'request_failed;case=not_returned;relation=prior'
 : variation.requestFailed ? 'request_failed;case=not_returned;relation=current'
 : variation.extraResponseKey ? 'created_envelope;case=not_returned;relation=current'
 : variation.badTaskID ? 'created_task_id;case=not_returned;relation=current'
 : variation.keyOldProofCollision ? 'request_inspection;case=not_returned;relation=current;inspect=locator_privacy'
 : 'response_check;case=not_returned;relation=current';
 const responseCheck = variation.currentKeyReflection || variation.lateProof ? ';check=body_privacy' : '';
 check(settled.message==='uac_evidence_invalid:post_binding;stage=export_settle'+responseCheck+';observer='+expectedBranch,'exact_closed_failure');break;}
  // Actual original-key receipt helper follows settlement before the next case.
  const receiptValue={taskID:responseValue.taskID,task:{state:'succeeded'},request:exp};
  page.waitForResponse=async()=>requestFor('GET',h.exportPrefix+'/receipt?domainId='+exp.domainId+'&idempotencyKey='+key,receiptValue,null).response;
  const receipt=await closed(()=>h.readUACOriginalReceipt(page,origin,1,exp,hash(key),secrets),'original_receipt');
  check(receipt.ok&&receipt.value.taskID===responseValue.taskID,'actual_receipt_pass:'+name);finalCount++;
 }
 if(!variation.expectReject){context.tabs=[];const final=await observer.closeAndSnapshot({owner:10,producer:9,observations:1,directoryUses:1,exportTasks:3});check(final.events.length===3&&final.createCount===3&&final.cancelCount===0&&final.unexpectedExportWriteCount===0,'same_ledger');ledgerSHA256=hash(JSON.stringify(final));}
 else check(cases.length===3&&!cases[2].settled&&finalCount===2,'exact_prefix');
 check(bodyCalls===0,'no_inspector_body');
 return{name,cases,ledgerSHA256,successfulOriginalReceipts:finalCount,actualHostAtomicTakes:takes,inspectorBodyCalls:bodyCalls,locatorReads:readCount};
}
const variants=[
 ['three_case_same_response_control',{}],
 ['third_main_first_then_observer_same_cached_response',{deferLocator:true}],
 ['returned_clone_mutation_isolated',{deferLocator:true,mutateReturnedClone:true}],
 ['third_locator_previous_after_main',{deferLocator:true,locatorPrevious:true,expectReject:true}],
 ['third_locator_missing_after_main',{deferLocator:true,locatorMissing:true,expectReject:true}],
 ['third_duplicate_post_after_main',{deferLocator:true,duplicate:true,expectReject:true}],
 ['third_requestfailed_after_main',{deferLocator:true,requestFailed:true,expectReject:true}],
 ['earlier_set_requestfailed_during_third_case',{deferLocator:true,priorRequestFailed:true,expectReject:true}],
 ['third_extra_response_field_main_pass_observer_reject',{deferLocator:true,extraResponseKey:true,expectReject:true}],
 ['third_task_platform_main_pass_observer_reject',{deferLocator:true,badTaskID:true,expectReject:true}],
 ['third_current_key_reflection_detected_after_hash_registration',{deferLocator:true,currentKeyReflection:true,expectReject:true}],
 ['third_latest_secret_added_after_main',{deferLocator:true,lateProof:true,expectReject:true}],
 ['third_valid_uuid_contains_older_observed_proof',{deferLocator:true,keyOldProofCollision:true,expectReject:true}],
];

(async () => {
const results=[];for(const [n,v]of variants)results.push(await runScenario(n,v));
const sourceSHA256={};for(const file of ['web/e2e/user-asset-export-v3-uac-helpers.ts','web/e2e/directory-v2-response-observer.ts','web/src/directory-v4-controls-json.ts'])sourceSHA256[file]=hash(fs.readFileSync(path.join(root,file)));
process.stdout.write(JSON.stringify({scope:'Actual TS helper+unchanged host observer+unique parser; synthetic Playwright objects and completed observer records. No browser, HTTP, CI replay or observed current failure attribution. Actual receipt helper exercised after each successful case.',sourceSHA256,results},null,2)+'\n');

})().catch(() => { process.stderr.write("closed_observer_probe_failed\n"); process.exitCode = 1; });
""")
        self.assertEqual(len(result['results']), 13)
        for item in result['results'][:3]:
            self.assertEqual(item['ledgerSHA256'], '9f437ee2ea84b2181904068553f74b76a26f20256387f046672c6a583c9926e1')
            self.assertEqual(item['actualHostAtomicTakes'], 6)
            self.assertEqual(item['successfulOriginalReceipts'], 3)
            self.assertTrue(all(case['settled'] for case in item['cases']))
        for item in result['results'][3:]:
            self.assertEqual(item['actualHostAtomicTakes'], 5)
            self.assertEqual(item['successfulOriginalReceipts'], 2)
            self.assertTrue(all(case['mainSafeResponse'] and case['mainPredicates'] for case in item['cases']))
            self.assertFalse(item['cases'][-1]['settled'])
        self.assertTrue(all(item['inspectorBodyCalls'] == 0 for item in result['results']))

    def test_observer_all_branches_first_poison_local_recovery_and_hostile_errors(self):
        result = self._run_probe(r"""
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {registerHooks,isBuiltin,createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {EventEmitter} from 'node:events';
const root=path.resolve(process.argv[2]);
const local=n=>pathToFileURL(path.join(root,n)).href;
const stub=s=>'data:text/javascript,'+encodeURIComponent(s);
const helperURL=local('web/e2e/user-asset-export-v3-uac-helpers.ts');
const parserURL=local('web/src/directory-v4-controls-json.ts');
const observed=new WeakMap();globalThis.__uacObservedChecks=observed;
const deps=new Map([[helperURL,{'../src/directory-v4-controls-json':parserURL,
 './directory-v2-response-observer':stub('export const directoryUACResponseBytes=async r=>r.body();export const directoryUACResponseFailureCheck=e=>e!==null&&typeof e==="object"?globalThis.__uacObservedChecks.get(e):undefined;'),
 './user-asset-export-v3-helpers':stub('import {createHash} from "node:crypto";export const hash=v=>{globalThis.__uacHashFault?.(v);return createHash("sha256").update(v).digest("hex");};')}],
 [parserURL,{'./api':stub('export class ApiError extends Error{constructor(){super("invalid_response");}}')}]]);
const hook=registerHooks({resolve(s,c,n){const d=deps.get(c.parentURL);if(d&&Object.hasOwn(d,s))return{url:d[s],shortCircuit:true};if(d&&!isBuiltin(s))throw Error('unexpected_offline_dependency');return n(s,c);}});
const require=createRequire(import.meta.url);let h;try{h=require(path.join(root,'web/e2e/user-asset-export-v3-uac-helpers.ts'));}finally{hook.deregister();}
const check=(v,label)=>{if(!v)throw Error('observer_probe_assertion:'+label)};
const origin='http://127.0.0.1:12345',key='abcdefab-cdef-4abc-8def-abcdefabcdef',otherKey='fedcbafe-dcba-4fed-8cba-fedcbafedcba';
const source={taskUUID:'synthetic-observation',selection:{domainId:'synthetic-domain',revision:'2',credentialRevision:'1'}};
const locator=(k=key)=>JSON.stringify({version:1,kind:'user_assets.v3.export',actorID:1,domainId:source.selection.domainId,idempotencyKey:k});
const rows=[];let hookCalls=0,rawBodies=0,zeroedBodies=0,rawZeroingAssertions=0;
function hostile(){return new Proxy({}, {get(){hookCalls++;throw Error('private_hook');},getPrototypeOf(){hookCalls++;throw Error('private_hook');},getOwnPropertyDescriptor(){hookCalls++;throw Error('private_hook');},ownKeys(){hookCalls++;throw Error('private_hook');}})}
function setup(options={}){
 const known=[],frame={},raws=[],mustClear=new WeakSet();let storage=locator(),readLocator;
 const page={mainFrame:()=>frame,isClosed:()=>false,evaluate:async()=>readLocator?readLocator():storage};
 class Context extends EventEmitter{constructor(){super();this.tabs=[page];}pages(){return this.tabs;}}
 const context=new Context(),o=h.observeUACExportWrites(context,origin,known);
 const expected=h.expectedUACRequest(source,'set');
 const state={known,frame,page,context,o,expected,raws,setStorage:v=>storage=v,setReader:f=>readLocator=f};
 function request(opt={}){
  const exp=opt.expected??expected;
  const payload={...exp,idempotencyKey:key,actorPassword:'unit-private-proof',totpCode:'357913',...opt.body};
  const value={taskID:'export-set',task:{},request:exp,replayed:false,...opt.created};
  const bytes=opt.raw===null?null:Buffer.isBuffer(opt.raw)?Buffer.from(opt.raw):Buffer.from(opt.raw??JSON.stringify(payload));
  if(bytes){rawBodies++;raws.push(bytes);if(opt.fillThrow)bytes.fill=()=>{throw opt.fillThrow;};}
  const req={frame:()=>{if(opt.frameThrow)throw opt.frameThrow;return opt.frame??frame;},method:()=>opt.method??'POST',url:()=>{if(opt.urlThrow)throw opt.urlThrow;return opt.url??origin+h.exportPrefix;},postDataBuffer:()=>{if(bytes&&!opt.fillThrow)mustClear.add(bytes);return bytes;},
   response:async()=>{if(opt.responseThrow)throw opt.responseThrow;return opt.noResponse?null:response;}};
  const response={status:()=>opt.status??200,headers:()=>opt.headers??{'x-adtr-user-id':'1','cache-control':'no-store'},body:async()=>{if(opt.bodyThrow)throw opt.bodyThrow;return opt.responseBytes??Buffer.from(JSON.stringify(value));}};
  return{req,response};
 }
 state.request=request;state.emit=opt=>{const x=request(opt);context.emit('request',x.req);return x;};
 state.arm=()=>o.armCase('set',expected,1,page);
 state.dispose=async()=>{context.tabs=[];try{await o.closeAndSnapshot({});}catch{}for(const b of raws){if(mustClear.has(b)){rawZeroingAssertions++;check(b.every(v=>v===0),"all_inspected_raw_buffers_zeroed");}if(b.every(v=>v===0))zeroedBodies++;}};
 return state;
}
async function failure(fn,expected,label){let error;try{await fn();}catch(e){error=e;}check(error!==undefined,'must_reject_'+label);const message=h.uacFailureMessage(error,'export_settle');check(message===expected,'message_'+label);check(error.cause===undefined,'fresh_cause_'+label);const before=message;error.message='private-mutated-message';error.observer='forged';error.case='private-value';error.inspect='forged';check(h.uacFailureMessage(error,'export_settle')===before,'immutable_owned_'+label);rows.push({label,message});return error;}
const msg=(branch,{code='post_binding',mode='set',relation='current',inspect,check:responseCheck}={})=>`uac_evidence_invalid:${code};stage=export_settle${responseCheck?';check='+responseCheck:''};observer=${branch};case=${mode};relation=${relation}${inspect?';inspect='+inspect:''}`;
async function one(label,options,branch,extra={}){const s=setup();s.arm();if(options.secret)s.known.push(options.secret);if(Object.hasOwn(options,'storage'))s.setStorage(options.storage);if(options.reader)s.setReader(options.reader);if(options.before)options.before(s);const x=s.emit(options);if(options.after)options.after(s,x);await failure(()=>s.o.settleCase('export-set'),msg(branch,extra),label);if(options.restore)options.restore();await s.dispose();}

(async () => {
// Every synchronous original poison site; task count remains one for duplicate.
for(const [label,branch,extra,opt] of [
 ['url_parse','url_parse',{relation:'unknown'},{urlThrow:hostile()}],
 ['cancel_write','cancel_write',{}, {url:origin+h.exportPrefix+'/cancel'}],
 ['unexpected_write','unexpected_write',{}, {method:'PUT'}],
 ['duplicate_create','duplicate_create',{}, {}],
]){const s=setup();s.arm();s.emit();s.emit(opt);await failure(()=>s.o.settleCase('export-set'),msg(branch,extra),label);await s.dispose();}
{const s=setup();s.emit();await failure(()=>s.arm(),msg('inactive_case',{code:'post_count',mode:'unarmed',relation:'unarmed'}),'inactive_case');await s.dispose();}
{const s=setup();s.arm();s.emit();await s.o.settleCase('export-set');s.emit();await failure(()=>s.o.settleCase('export-set'),msg('settled_case',{code:'post_count'}),'settled_case');await s.dispose();}
// Actual request-inspector entered checks, including strict byte/JSON/privacy gates.
const inspections=[
 ['request_owned',{frame:{}}], ['body_size',{raw:null}], ['body_bom',{raw:Buffer.concat([Buffer.from([239,187,191]),Buffer.from('{}')])}],
 ['body_utf8',{raw:Buffer.from([255])}], ['body_json',{raw:'{"x":1,"x":2}'}],
 ['body_scalar',{body:{actorPassword:'\ud800'}}],['body_shape',{body:{unexpected:true}}],
 ['business_types',{body:{screenColumns:[null]}}],['proof_shape',{body:{totpCode:'invalid'}}],
 ['business_binding',{body:{search:'different'}}],['business_privacy',{secret:'user-'}],
 ['locator_read',{reader:()=>{throw hostile();}}],['locator_shape',{storage:null}],
 ['locator_json',{storage:'{"x":1,"x":2}'}],['locator_identity',{storage:JSON.stringify({...JSON.parse(locator()),actorID:2})}],
 ['locator_privacy',{secret:key}],['key_equality',{storage:locator(otherKey)}],
 ['unexpected',{fillThrow:hostile()}],
];
for(const [sub,options]of inspections)await one('inspect_'+sub,options,'request_inspection',{inspect:sub});
{const get=WeakMap.prototype.get;await one('inspect_proof_registration',{before:s=>{WeakMap.prototype.get=function(k){if(k===s.known)throw hostile();return get.call(this,k);};},after:()=>{WeakMap.prototype.get=get;},restore:()=>{WeakMap.prototype.get=get;}},'request_inspection',{inspect:'proof_registration'});}
{await one('inspect_request_commitment',{before:()=>{globalThis.__uacHashFault=v=>{if(v===key)throw hostile();};},restore:()=>{delete globalThis.__uacHashFault;}},'request_inspection',{inspect:'request_commitment'});}
await one('response_read',{responseThrow:hostile()},'response_read');
await one('response_missing',{noResponse:true},'response_missing');
for(const [branch,created]of [ ['created_envelope',{extra:true}],['created_task_id',{taskID:'platform'}],['created_replayed',{replayed:true}],['created_request',{request:{}}] ])await one(branch,{created},branch);
// Existing eleven owned response checks remain distinguishable through the observer.
const observedRead=new Error('private-observed-read'),observedBinding=new Error('private-observed-binding');observed.set(observedRead,'observed_body_read');observed.set(observedBinding,'observed_byte_binding');
for(const [category,options]of [
 ['status',{status:500}],['actor_header',{headers:{'x-adtr-user-id':'2','cache-control':'no-store'}}],['cache_header',{headers:{'x-adtr-user-id':'1','cache-control':'public'}}],
 ['observed_body_read',{bodyThrow:observedRead}],['observed_byte_binding',{bodyThrow:observedBinding}],
 ['body_size',{responseBytes:Buffer.alloc(0)}],['body_bom',{responseBytes:Buffer.from([239,187,191,123,125])}],
 ['body_utf8',{responseBytes:Buffer.from([255])}],['body_json',{responseBytes:Buffer.from('{"x":1,"x":2}')}],
 ['body_object',{responseBytes:Buffer.from('[]')}],['body_privacy',{created:{task:{password:'unit-private-proof'}}}],
])await one('response_'+category,options,'response_check',{check:category});
// Unknown values never gain response ownership from a forged string/property.
for(const [label,error]of [['proxy',hostile()],['forged',{message:'uac_evidence_invalid:post_binding;check=actor_header',check:'actor_header'}],['getter',Object.defineProperties({}, {message:{get(){hookCalls++;throw Error('private');}},cause:{get(){hookCalls++;throw Error('private');}},toString:{get(){hookCalls++;throw Error('private');}}})]])await one('unknown_'+label,{responseThrow:error},'response_read');
// First-poison ordering is sticky even when a later async failure completes.
for(const order of ['duplicate_then_failed','failed_then_duplicate']){const s=setup();s.arm();let release;s.setReader(()=>new Promise(r=>release=r));const x=s.emit();if(order.startsWith('duplicate')){s.emit();s.context.emit('requestfailed',x.req);}else{s.context.emit('requestfailed',x.req);s.emit();}release(locator());await failure(()=>s.o.settleCase('export-set'),msg(order.startsWith('duplicate')?'duplicate_create':'request_failed'),order);await s.dispose();}
for(const order of ['async_then_failed','failed_then_async']){const s=setup();s.arm();let release;s.setReader(()=>new Promise(r=>release=r));const x=s.emit({created:{extra:true}});if(order.startsWith('failed'))s.context.emit('requestfailed',x.req);release(locator());const branch=order.startsWith('failed')?'request_failed':'created_envelope';await failure(()=>s.o.settleCase('export-set'),msg(branch),order);s.context.emit('requestfailed',x.req);await failure(()=>s.o.settleCase('export-set'),msg(branch),order+'_sticky');await s.dispose();}
// Local assertion diagnostics do not create new sticky poison.
{const s=setup();s.context.tabs=[];await failure(()=>s.arm(),msg('arm_state',{code:'post_count',mode:'unarmed',relation:'unknown'}),'arm_state');s.context.tabs=[s.page];s.arm();s.emit();await s.o.settleCase('export-set');await s.dispose();}
{const s=setup();s.arm();await failure(()=>s.o.settleCase('export-set'),msg('settle_state',{code:'post_count'}),'settle_state');s.emit();await failure(()=>s.o.settleCase('wrong-task'),msg('settle_task_mismatch'),'settle_task_mismatch');await s.o.settleCase('export-set');await s.dispose();}
{const s=setup();s.arm();let writes=0;Object.defineProperty(Object.prototype,'event',{configurable:true,set(){writes++;},get(){return undefined;}});try{s.emit();await failure(()=>s.o.settleCase('export-set'),msg('settle_missing_event'),'settle_missing_event_injected_property');check(writes===1,'one_event_assignment');}finally{delete Object.prototype.event;}await s.dispose();}
{const s=setup();await failure(()=>s.o.closeAndSnapshot({}),msg('close_state',{code:'post_count',mode:'unarmed',relation:'unknown'}),'close_state');s.context.tabs=[];await failure(()=>s.o.closeAndSnapshot({}),msg('close_counts',{code:'post_count',mode:'unarmed',relation:'unknown'}),'close_counts');check(s.context.listenerCount('request')===0&&s.context.listenerCount('requestfailed')===0,'close_detached_before_failure');await failure(()=>s.o.closeAndSnapshot({}),msg('close_state',{code:'post_count',mode:'unarmed',relation:'unknown'}),'close_failure_not_newly_retryable');}
// GET/HEAD/OPTIONS/untracked failures remain ignored; listeners still drain.
{const s=setup();s.arm();for(const method of ['GET','HEAD','OPTIONS']){const x=s.emit({method});s.context.emit('requestfailed',x.req);}s.context.emit('requestfailed',s.request().req);s.emit();await s.o.settleCase('export-set');await s.dispose();rows.push({label:'ignored_reads_untracked_failure_control',passed:true});}
// Unrecognized external mode cannot contribute dynamic diagnostic text.
for(const [label,mode]of [['unknown_mode_closed','private-mode'],['output_only_unarmed_mode','unarmed'],['output_only_unknown_mode','unknown'],['nonstr_mode',Object.defineProperty({},'toString',{get(){hookCalls++;throw Error('private');}})]]){const s=setup();const exp=Object.freeze({...s.expected,uacDisableBit:mode,screenColumns:s.expected.screenColumns});s.o.armCase(mode,exp,1,s.page);s.emit({expected:exp});s.emit({expected:exp});await failure(()=>s.o.settleCase('export-set'),msg('duplicate_create',{mode:'unknown'}),label);await s.dispose();}
check(hookCalls===0,'unknown_error_hooks_never_called');
check(rows.length>=60,'nonvacuous_cases');
const branches=[...new Set(rows.map(r=>r.message?.match(/;observer=([^;]+)/)?.[1]).filter(Boolean))].sort();
const inspectionsSeen=[...new Set(rows.map(r=>r.message?.match(/;inspect=([^;]+)/)?.[1]).filter(Boolean))].sort();
const responseChecks=[...new Set(rows.map(r=>r.message?.match(/;check=([^;]+)/)?.[1]).filter(Boolean))].sort();
check(branches.length===21,'all_observer_branches');check(inspectionsSeen.length===20,'all_inspection_checks');check(responseChecks.length===11,'all_response_checks');
process.stdout.write(JSON.stringify({scope:'Actual helper/parser with synthetic response byte seam; missing event uses isolated property injection; no browser/CI/root-cause attribution.',rows,branches,inspections:inspectionsSeen,responseChecks,unknownErrorHookCalls:hookCalls,rawBodies,zeroedBodies,rawZeroingAssertions},null,2)+'\n');

})().catch(() => { process.stderr.write("closed_observer_probe_failed\n"); process.exitCode = 1; });
""")
        self.assertEqual(len(result['rows']), 64)
        self.assertEqual(result['unknownErrorHookCalls'], 0)
        self.assertGreaterEqual(result['rawZeroingAssertions'], 40)
        self.assertEqual(result['branches'], sorted([
            'url_parse', 'cancel_write', 'unexpected_write', 'inactive_case',
            'settled_case', 'duplicate_create', 'request_inspection', 'response_read',
            'response_missing', 'response_check', 'created_envelope', 'created_task_id',
            'created_replayed', 'created_request', 'request_failed', 'arm_state',
            'settle_state', 'settle_missing_event', 'settle_task_mismatch',
            'close_state', 'close_counts']))
        self.assertEqual(result['inspections'], sorted([
            'request_owned', 'body_size', 'body_bom', 'body_utf8', 'body_json',
            'body_scalar', 'body_shape', 'business_types', 'proof_shape',
            'proof_registration', 'business_binding', 'business_privacy', 'locator_read',
            'locator_shape', 'locator_json', 'locator_identity', 'locator_privacy',
            'key_equality', 'request_commitment', 'unexpected']))
        self.assertEqual(result['responseChecks'], sorted([
            'status', 'actor_header', 'cache_header', 'observed_body_read',
            'observed_byte_binding', 'body_size', 'body_bom', 'body_utf8',
            'body_json', 'body_object', 'body_privacy']))

if __name__ == '__main__':
    unittest.main()
