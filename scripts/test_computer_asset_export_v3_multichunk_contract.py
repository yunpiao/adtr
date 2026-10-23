"""Offline adversarial contracts. Synthetic specimens are never runtime evidence.

All payload mutation cases rebind dependent file/chunk/header/manifest digests;
strict container admission, not a stale checksum, must reject the unchanged input.
"""
import ast
import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch
import zlib

import computer_asset_export_v3_multichunk_ci as gate
import validate_computer_asset_export_v3_multichunk_evidence as evidence

CANARY = 'SYNTHETIC_METADATA_CANARY_NOT_A_CREDENTIAL'
SOURCE_ID = '10000000-0000-0000-0000-000000000001'
DOMAIN_ID = 'D' * 24
TASK_ID = '30000000-0000-0000-0000-000000000001'
ACCOUNT_ID = 'A' * 24
ROOT = Path(__file__).resolve().parent.parent


def source_specimen():
    objects = []
    for index in range(1, 1025):
        kind = 'computer' if index <= 1020 else 'user' if index <= 1022 else 'group'
        name = f'{kind}-capacity-{index:04d}'
        if kind == 'computer':
            suffix = ''.join(',OU=' + hashlib.sha256(f'adtr-computer-capacity-v1|{index:04d}|{ou:02d}'.encode()).hexdigest() for ou in range(48))
            classes = ['computer', 'organizationalperson', 'person', 'top', 'user']
        else:
            suffix = ',OU=' + ('Users' if kind == 'user' else 'Groups')
            classes = ['organizationalperson', 'person', 'top', 'user'] if kind == 'user' else ['group', 'top']
        objects.append({'base': {'objectGUID': f'{index:08x}-1122-3344-5566-778899aabbcc',
                                 'distinguishedName': 'CN=' + name + suffix + ',DC=synthetic,DC=invalid',
                                 'kind': kind, 'objectClass': classes, 'samAccountName': name + ('$' if kind == 'computer' else ''),
                                 'userAccountControl': 4096 if kind == 'computer' else None},
                        'supplemental': {'objectSidBytes': base64.b64encode(b'\x01\x01\0\0\0\0\0\x05' + struct.pack('<I', index)).decode(),
                                         'mailBytes': None, 'descriptionBytes': None,
                                         'whenCreatedBytes': base64.b64encode(b'20260101000000.0Z').decode()},
                        'identity': {'primaryGroupIDBytes': None, 'servicePrincipalNameBytes': None}})
    return evidence.encoded({'dictionaryVersion': 3, 'objects': objects,
                             'source': {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid',
                                        'domain': 'synthetic.invalid', 'naming_context': 'dc=synthetic,dc=invalid',
                                        'started_at': '2026-10-23T00:00:00Z', 'completed_at': '2026-10-23T00:00:01Z',
                                        'elapsed_milliseconds': 1000, 'pages': 4}})


def zip_specimen(parts, level=0, envelope=None):
    """Produce a real contiguous signed-descriptor ZIP without using the validator."""
    envelope = envelope or {}
    local, central = bytearray(envelope.get('preamble', b'')), bytearray()
    for index, (name, body) in enumerate(parts):
        name_raw = name.encode('ascii')
        compress = zlib.compressobj(level, zlib.DEFLATED, -15)
        packed = compress.compress(body) + compress.flush()
        packed += envelope.get(('compressedSuffix', index), b'')
        crc = zlib.crc32(body)
        offset = len(local)
        local_name = envelope.get(('localName', index), name_raw)
        extra = envelope.get(('localExtra', index), b'')
        fields = [0x04034b50, 20, 8, 8, 0, 0, 0, 0, 0, len(local_name), len(extra)]
        for key, value in envelope.items():
            if type(key) is tuple and key[:2] == ('localField', index):
                fields[key[2]] = value
        local += struct.pack('<I5H3I2H', *fields) + local_name + extra + packed
        descriptor = [0x08074b50, crc, len(packed), len(body)]
        for key, value in envelope.items():
            if type(key) is tuple and key[:2] == ('descriptor', index):
                descriptor[key[2]] = value
        local += struct.pack('<IIII', *descriptor)
        local += envelope.get(('gap', index), b'')
        extra = envelope.get(('centralExtra', index), b'')
        comment = envelope.get(('memberComment', index), b'')
        fields = [0x02014b50, 20, 20, 8, 8, 0, 0, crc, len(packed), len(body), len(name_raw), len(extra), len(comment), 0, 0, 0, offset]
        for key, value in envelope.items():
            if type(key) is tuple and key[:2] == ('centralField', index):
                fields[key[2]] = value
        central += struct.pack('<I6H3I5H2I', *fields) + name_raw + extra + comment
    local += envelope.get('centralGap', b'')
    fields = [0x06054b50, 0, 0, len(parts), len(parts), len(central), len(local), len(envelope.get('comment', b''))]
    for key, value in envelope.items():
        if type(key) is tuple and key[0] == 'endField':
            fields[key[1]] = value
    return bytes(local + central + struct.pack('<IHHHHIIH', *fields) + envelope.get('comment', b'') + envelope.get('trailing', b''))


def png_chunk(kind, body):
    return struct.pack('>I', len(body)) + kind + body + struct.pack('>I', zlib.crc32(kind + body))


def png_specimen(chunks=None, pixels=None, segmentation=4096, suffix=b'', header=None):
    header = header or struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0)
    pixels = pixels if pixels is not None else (b'\0' + b'\0' * (1440 * 3)) * 900
    packed = zlib.compress(pixels) + suffix
    data = [png_chunk(b'IHDR', header)]
    if chunks:
        data.extend(chunks)
    data.extend(png_chunk(b'IDAT', packed[offset:offset + segmentation]) for offset in range(0, len(packed), segmentation))
    data.append(png_chunk(b'IEND', b''))
    return b'\x89PNG\r\n\x1a\n' + b''.join(data)


def file_ref(name, raw):
    return {'filename': name, 'byteCount': len(raw), 'sha256': evidence.digest(raw)}


def specimen():
    source = source_specimen()
    request = {'domainId': DOMAIN_ID, 'expectedRevision': '2', 'expectedCredentialRevision': '2',
               'observationId': SOURCE_ID, 'startTm': '2026-01-01T00:00:00Z', 'endTm': '2026-01-02T00:00:00Z',
               'search': 'computer-', 'creatTm': 'asc', 'screenColumns': evidence.COLUMNS}
    matrix = [evidence.HEADERS, *evidence.matched_rows(evidence.source_rows(source), request)]
    parts = [*evidence.STATIC_PARTS, ('xl/worksheets/sheet1.xml', evidence.worksheet_bytes(matrix))]
    workbook = zip_specimen(parts)
    png = png_specimen()
    provenance = {'checkoutCommit': 'a' * 40, 'checkoutTree': 'b' * 40, 'runID': '123', 'runAttempt': '1',
                  'sourceSHA256': {path: 'c' * 64 for path in sorted(evidence.REQUIRED_SOURCES)}}
    public = {'taskUUID': TASK_ID, 'taskName': 'computer_assets.v3.export', 'domainId': DOMAIN_ID, 'payloadVersion': 1,
              'state': 'succeeded', 'progress': 100, 'attempt': 1, 'maxAttempts': 3, 'archived': False}
    source_meta = {'file': file_ref('source-observation.json', source), 'taskUUID': SOURCE_ID, 'tenantId': 'default',
                   'actorId': '7', 'domainId': DOMAIN_ID, 'dictionaryVersion': 3, 'count': 1024,
                   'selection': {'domainId': DOMAIN_ID, 'revision': '2', 'credentialRevision': '2'},
                   'sourcePins': {'accountId': ACCOUNT_ID, 'accountCredentialRevision': '1', 'policyRevision': 'd' * 64,
                                  'grantRoleId': 'platform_admin', 'grantRevision': '1'},
                   'producer': {'taskID': SOURCE_ID, 'tenantId': 'default', 'domainId': DOMAIN_ID, 'actorId': '7',
                                'kind': 'domain.directory_read.v3', 'payloadVersion': 1, 'state': 'succeeded',
                                'attempt': 1, 'maxAttempts': 1, 'fencingToken': '1'},
                   'observationOpener': {'owner': 'e' * 32 + '-0', 'fencingToken': '1', 'attempt': 1},
                   'useOpener': {'owner': 'e' * 32 + '-0', 'fencingToken': '1', 'attempt': 1},
                   'purpose': 'domain.directory_read.v3', 'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0,
                   'segments': [{'offset': offset, 'byteCount': len(source[offset:offset + 524288]),
                                 'sha256': evidence.digest(source[offset:offset + 524288])} for offset in range(0, len(source), 524288)]}
    data = {'format': 1, 'suite': gate.SUITE, 'provenance': provenance, 'actorId': 8, 'source': source_meta, 'request': request,
            'create': {'taskID': TASK_ID, 'request': request, 'replayed': False, 'task': public},
            'receipt': {'taskID': TASK_ID, 'request': request, 'task': public},
            'detail': {'taskID': TASK_ID, 'request': request, 'task': public, 'downloadReady': True, 'downloadStatus': 'ready', 'artifact': {}},
            'list': {'observationId': SOURCE_ID, 'dictionaryVersion': 3, 'pageSize': 10, 'firstPageCount': 10, 'total': 1020},
            'database': {'task': {'taskID': TASK_ID, 'tenantId': 'default', 'actorId': '8', 'domainId': DOMAIN_ID,
                                  'kind': 'computer_assets.v3.export', 'payloadVersion': 1, 'state': 'succeeded', 'progress': 100,
                                  'attempt': 1, 'maxAttempts': 3, 'resultVersion': '3', 'fencingToken': '1', 'authorizationVersion': '1',
                                  'payloadHash': evidence.digest(b'1::' + evidence.encoded({key: request[key] for key in sorted(request)})), 'payload': request, 'result': {}},
                         'snapshot': {'taskID': TASK_ID, 'tenantId': 'default', 'actorId': '8', 'authorizationVersion': '1',
                                      'observationId': SOURCE_ID, 'sourceSHA256': evidence.digest(source),
                                      'payloadHash': evidence.digest(b'1::' + evidence.encoded({key: request[key] for key in sorted(request)})), 'rowCount': 1020},
                         'manifest': {}, 'chunks': [],
                         'events': [{'action': action, 'state': state, 'resultVersion': version} for action, state, version in
                                    [('submitted', 'queued', '0'), ('leased', 'queued', '0'), ('started', 'running', '1'),
                                     ('progress', 'running', '2'), ('finished', 'succeeded', '3')]], 'file': {}},
            'download': {'file': {}, 'suggestedFilename': '', 'status': 200, 'headers': {}},
            'sensitiveBudget': {'before': {'schemaVersion': 23, 'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0},
                                'after': {'schemaVersion': 23, 'owner': 8, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 1}},
            'screenshot': file_ref('ready.png', png),
            'cells': {'rowCount': 1020, 'columnCount': 5, 'firstRowSHA256': evidence.digest(evidence.encoded(matrix[1])),
                      'lastRowSHA256': evidence.digest(evidence.encoded(matrix[-1])), 'matrixSHA256': evidence.digest(evidence.encoded(matrix))},
            'timings': {'scope': 'browser_export_submit_to_download', 'submitToDownloadMs': 1000}}
    blobs = {'source-observation.json': source, 'database-export.xlsx': workbook, 'download.xlsx': workbook,
             'ready.png': png, 'provenance.json': evidence.encoded(provenance) + b'\n', 'evidence.json': b''}
    rebind_workbook(blobs, data, workbook)
    return blobs, data, matrix, parts


def rebind_workbook(blobs, data, workbook):
    # All DB, browser, header and summary byte bindings deliberately agree with
    # the malicious payload. A stale SHA must not be the rejection reason.
    filename = f'computer-assets-v3-{TASK_ID}.xlsx'
    ref = file_ref('download.xlsx', workbook)
    result = {'artifactToken': '1', 'rowCount': 1020, 'byteCount': len(workbook), 'sha256': ref['sha256']}
    data['database']['task']['result'] = result
    data['database']['file'] = file_ref('database-export.xlsx', workbook)
    data['database']['manifest'] = {'taskID': TASK_ID, 'fencingToken': '1', 'byteCount': len(workbook),
                                   'chunkCount': (len(workbook) + 1048575) // 1048576, 'rowCount': 1020, 'sha256': ref['sha256']}
    data['database']['chunks'] = [{'taskID': TASK_ID, 'fencingToken': '1', 'ordinal': ordinal,
                                  'byteCount': len(workbook[offset:offset + 1048576]),
                                  'sha256': evidence.digest(workbook[offset:offset + 1048576])}
                                 for ordinal, offset in enumerate(range(0, len(workbook), 1048576))]
    data['download'] = {'file': ref, 'suggestedFilename': filename, 'status': 200,
                        'headers': {'x-adtr-user-id': '8', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff',
                                    'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
                                    'content-length': str(len(workbook)), 'x-adtr-artifact-sha256': ref['sha256'],
                                    'x-adtr-row-count': '1020', 'content-disposition': f'attachment; filename="{filename}"'}}
    data['detail']['artifact'] = {'rowCount': 1020, 'byteCount': len(workbook), 'sha256': ref['sha256'], 'filename': filename}
    blobs['database-export.xlsx'] = workbook; blobs['download.xlsx'] = workbook
    blobs['evidence.json'] = evidence.encoded(data)


def resources(reference, final=True):
    value = {'format': 1, 'suite': gate.SUITE, 'provenanceSHA256': evidence.digest(reference), 'scope': 'full_browser_phase',
             'clockTicksPerSecond': 100, 'elapsedMs': 1000, 'limitations': evidence.LIMITATIONS}
    for name, pid in [('api', 123), ('worker', 456)]:
        value[name] = {'pid': pid, 'startTicks': '20', 'before': {'userTicks': '10', 'systemTicks': '20', 'vmRSSKiB': 100, 'vmHWMKiB': 120},
                       'after': {'userTicks': '15', 'systemTicks': '22', 'vmRSSKiB': 110, 'vmHWMKiB': 130},
                       'userCpuSeconds': 0.05, 'systemCpuSeconds': 0.02}
    if final:
        value['runtime'] = {'schema': 1, 'phases': [{'phase': phase, 'at_ms': index * 1000} for index, phase in enumerate(
            ['setup', 'api-start', 'api-ready', 'worker-start', 'worker-ready', 'browser-start', 'browser-pass', 'before-cleanup', 'after-cleanup'])],
            'processes': {name: {'started': True, 'before_cleanup': None, 'after_cleanup': 0, 'forced_kill': False} for name in ('api', 'worker')},
            'http_panic': False, 'browser': [], 'browser_capture': 'pending', 'browser_dropped': 0, 'collection_errors': []}
    return value


class EvidenceContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.blobs, cls.data, cls.matrix, cls.parts = specimen()

    def test_positive_complete_specimen_and_alternate_deflate_and_idat(self):
        evidence.validate_core(self.blobs, self.data['provenance'])
        for level in (0, 1, 6, 9):
            raw = zip_specimen(self.parts, level=level)
            evidence.validate_zip(raw, self.matrix)
        for segmentation in (1, 4096, 65536):
            # Segment size one would exceed the4096 chunk safety budget for this
            # image, so use legal minimum segmentation below that fixed bound.
            raw = png_specimen(segmentation=max(segmentation, 4))
            evidence.strict_png(raw)
        for filter_byte in range(5):
            evidence.strict_png(png_specimen(pixels=(bytes([filter_byte]) + b'\0' * 4320) * 900))
        evidence.validate_resources(resources(self.blobs['provenance.json']), self.blobs['provenance.json'])

    def assert_rejected(self, blobs, data):
        blobs['evidence.json'] = evidence.encoded(data)
        with self.assertRaises((ValueError, TypeError, KeyError, UnicodeError, zlib.error)):
            evidence.validate_core(blobs, data['provenance'])
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); inputs = root / 'input'; inputs.mkdir()
            for name, raw in blobs.items():
                (inputs / name).write_bytes(raw)
            reference = root / 'trusted.json'; reference.write_bytes(blobs['provenance.json'])
            pre = root / 'pre.json'; pre.write_bytes(evidence.encoded(resources(blobs['provenance.json'], False)))
            target = root / 'stage'
            with patch.object(gate, 'OUTPUT', target), patch.object(gate, 'PROVENANCE', reference), \
                 patch.object(gate, 'PRE_RESOURCES', pre), patch.object(gate, 'discover', return_value=inputs), \
                 patch.object(evidence, 'capture_provenance', return_value=data['provenance']):
                with self.assertRaises(Exception):
                    gate.publish()
            self.assertFalse(target.exists())
            for name, raw in blobs.items():
                self.assertEqual((inputs / name).read_bytes(), raw)

    def test_every_static_xml_region_rejects_rehashed_canary_payload(self):
        canary = CANARY.encode()
        for index in range(5):
            name, original = self.parts[index]
            opening = original.index(b'>', original.index(b'?>') + 2)
            variants = [original[:opening] + b' canary="' + canary + b'"' + original[opening:],
                        original[:opening] + b' xmlns:canary="urn:' + canary + b'"' + original[opening:],
                        original[:opening + 1] + b'<!--' + canary + b'-->' + original[opening + 1:],
                        original[:opening + 1] + b'<?canary ' + canary + b'?>' + original[opening + 1:],
                        original[:opening + 1] + b'<canary>' + canary + b'</canary>' + original[opening + 1:],
                        original[:opening + 1] + canary + original[opening + 1:],
                        original[:opening + 1] + b' ' + original[opening + 1:],
                        original.replace(b'ComputerAssets', canary, 1) if b'ComputerAssets' in original else original + b'\n']
            for variant in variants:
                with self.subTest(part=name, sha=evidence.digest(variant)):
                    parts = list(self.parts); parts[index] = (name, variant)
                    raw = zip_specimen(parts)
                    with self.assertRaises(ValueError): evidence.validate_zip(raw, self.matrix)
                    blobs, data = dict(self.blobs), copy.deepcopy(self.data)
                    rebind_workbook(blobs, data, raw); self.assert_rejected(blobs, data)

    def test_worksheet_ignored_channels_keep_original_cells_but_reject(self):
        name, original = self.parts[-1]
        for tag in (b'worksheet', b'sheetData', b'row', b'c', b'is', b't'):
            match = __import__('re').search(rb'<' + tag + rb'(?=[ >])[^>]*>', original)
            self.assertIsNotNone(match)
            opening = match.end() - 1
            for payload in (b'<!--' + CANARY.encode() + b'-->', b'<?canary ' + CANARY.encode() + b'?>',
                            b'<ignored>' + CANARY.encode() + b'</ignored>', b' ', CANARY.encode()):
                variant = original[:opening + 1] + payload + original[opening + 1:]
                parts = [*self.parts[:-1], (name, variant)]
                blobs, data = dict(self.blobs), copy.deepcopy(self.data)
                raw = zip_specimen(parts); rebind_workbook(blobs, data, raw)
                with self.subTest(tag=tag, payload=payload[:10]): self.assert_rejected(blobs, data)
            variant = original[:opening] + b' canary="' + CANARY.encode() + b'"' + original[opening:]
            blobs, data = dict(self.blobs), copy.deepcopy(self.data)
            rebind_workbook(blobs, data, zip_specimen([*self.parts[:-1], (name, variant)]))
            self.assert_rejected(blobs, data)
        for old, new in [(b'<row r="2">', b'<row r="3">'), (b'r="A2"', b'r="B2"'), (b'computer-capacity-0001$', b'computer-capacity-0002$')]:
            raw = zip_specimen([*self.parts[:-1], (name, original.replace(old, new, 1))])
            blobs, data = dict(self.blobs), copy.deepcopy(self.data); rebind_workbook(blobs, data, raw)
            self.assert_rejected(blobs, data)

    def test_zip_non_cell_envelope_channels_reject_with_all_hashes_rebuilt(self):
        canary = CANARY.encode()
        variants = [{'comment': canary}, {'preamble': canary}, {'trailing': canary}, {'centralGap': canary}]
        for index in range(6):
            for key in ('localExtra', 'centralExtra', 'memberComment', 'gap', 'compressedSuffix'):
                variants.append({(key, index): canary})
            variants.extend([{('localName', index): b'bad-name.xml'},
                             {('compressedSuffix', index): zlib.compress(b'payload')[2:-4]},
                             {('descriptor', index, 0): 0}, {('descriptor', index, 1): 0},
                             {('descriptor', index, 2): 0}, {('descriptor', index, 3): 0}])
        for index, value in [(1, 0x0314), (2, 45), (3, 9), (4, 0), (5, 1), (6, 1),
                             (7, 0), (8, 0xffffffff), (9, 0xffffffff), (13, 1), (14, 1), (15, 1), (16, 1)]:
            variants.append({('centralField', 0, index): value})
        for index, value in [(1, 45), (2, 9), (3, 0), (4, 1), (5, 1), (6, 1), (7, 1), (8, 1)]:
            variants.append({('localField', 0, index): value})
        for index, value in [(1, 1), (2, 1), (3, 0xffff), (4, 0xffff), (5, 0xffffffff), (6, 0xffffffff)]:
            variants.append({('endField', index): value})
        for variant in variants:
            blobs, data = dict(self.blobs), copy.deepcopy(self.data)
            raw = zip_specimen(self.parts, envelope=variant); rebind_workbook(blobs, data, raw)
            with self.subTest(envelope=repr(variant)):
                self.assert_rejected(blobs, data)
        for parts in (self.parts[:-1], [*self.parts, self.parts[0]], self.parts[::-1],
                      [*self.parts[:-1], ('extra.xml', canary), self.parts[-1]]):
            blobs, data = dict(self.blobs), copy.deepcopy(self.data)
            rebind_workbook(blobs, data, zip_specimen(parts)); self.assert_rejected(blobs, data)

    def test_bounded_inflate_does_not_accept_overexpansion_or_partial_streams(self):
        body=b'a'*4096
        for wbits in (-15,15):
            compressor=zlib.compressobj(6,zlib.DEFLATED,wbits)
            raw=compressor.compress(body)+compressor.flush()
            self.assertEqual(evidence.bounded_inflate(raw,len(body),wbits),body)
            for mutated,size in [(raw,len(body)-1),(raw,len(body)+1),(raw[:-1],len(body)),
                                 (raw+CANARY.encode(),len(body)),(raw+raw,len(body))]:
                with self.assertRaises((ValueError,zlib.error)):
                    evidence.bounded_inflate(mutated,size,wbits)

    def test_png_canary_and_complete_stream_rejections_rebind_screenshot(self):
        variants = []
        for kind in (b'tEXt', b'zTXt', b'iTXt', b'eXIf', b'iCCP', b'tIME', b'caNy', b'UNKN', b'PLTE'):
            variants.append(png_specimen([png_chunk(kind, CANARY.encode())]))
        variants.extend([self.blobs['ready.png'] + CANARY.encode(),
                         png_specimen([png_chunk(b'IHDR', struct.pack('>IIBBBBB',1440,900,8,2,0,0,0))]),
                         png_specimen([png_chunk(b'IEND', b'')]),
                         png_specimen(suffix=CANARY.encode()), png_specimen(suffix=zlib.compress(b'')),
                         png_specimen(pixels=b'\0'), png_specimen(pixels=(b'\5' + b'\0' * 4320) * 900),
                         png_specimen(pixels=(b'\0' + b'\0' * 4320) * 900 + b'\0')])
        for field, value in [(0, 1439), (1, 901), (2, 16), (3, 6), (4, 1), (5, 1), (6, 1)]:
            fields = [1440,900,8,2,0,0,0]; fields[field] = value
            variants.append(png_specimen(header=struct.pack('>IIBBBBB',*fields)))
        packed=zlib.compress((b'\0'+b'\0'*4320)*900)
        header=png_chunk(b'IHDR',struct.pack('>IIBBBBB',1440,900,8,2,0,0,0))
        prefix=b'\x89PNG\r\n\x1a\n'+header
        variants.append(prefix+png_chunk(b'IDAT',packed[:-1])+png_chunk(b'IEND',b''))
        variants.append(prefix+png_chunk(b'IDAT',packed[:10])+png_chunk(b'tEXt',CANARY.encode())+png_chunk(b'IDAT',packed[10:])+png_chunk(b'IEND',b''))
        variants.append(prefix+png_chunk(b'IDAT',b'')+png_chunk(b'IDAT',packed)+png_chunk(b'IEND',b''))
        damaged = bytearray(self.blobs['ready.png']); damaged[-1] ^= 1; variants.append(bytes(damaged))
        variants.extend([self.blobs['ready.png'][:-1], self.blobs['ready.png'][:-12]])
        for raw in variants:
            blobs, data = dict(self.blobs), copy.deepcopy(self.data)
            blobs['ready.png'] = raw; data['screenshot'] = file_ref('ready.png', raw)
            with self.subTest(sha=evidence.digest(raw)):
                with self.assertRaises((ValueError,zlib.error)): evidence.strict_png(raw)
                self.assert_rejected(blobs, data)

    def test_closed_schema_source_and_all_binding_mutations(self):
        paths = []
        def walk(value, path=()):
            if type(value) is dict:
                for key, child in value.items():
                    paths.append(path + (key,)); walk(child,path + (key,))
            elif type(value) is list:
                for index, child in enumerate(value): walk(child,path + (index,))
        walk(self.data)
        for path in paths:
            data=copy.deepcopy(self.data); target=data
            for part in path[:-1]: target=target[part]
            del target[path[-1]]
            with self.subTest(missing=path), self.assertRaises(Exception):
                blobs=dict(self.blobs);blobs['evidence.json']=evidence.encoded(data);evidence.validate_core(blobs,self.data['provenance'])
        for mutate in [lambda d:d['source']['segments'].reverse(),lambda d:d['database']['chunks'].reverse(),
                       lambda d:d['database']['events'].reverse(),lambda d:d['database']['task'].update(actorId='7'),
                       lambda d:d['source']['useOpener'].update(owner='0'*32+'-1'),
                       lambda d:d['database']['manifest'].update(fencingToken='2'),
                       lambda d:d['database']['chunks'][0].update(byteCount=1),
                       lambda d:d['download']['headers'].update(extra=CANARY),
                       lambda d:d['cells'].update(rowCount=True),lambda d:d.update(format=True),
                       lambda d:d['source'].update(count=True),lambda d:d['sensitiveBudget']['after'].update(owner=True),
                       lambda d:d['timings'].update(submitToDownloadMs=True)]:
            data=copy.deepcopy(self.data);mutate(data);self.assert_rejected(dict(self.blobs),data)
        original=json.loads(self.blobs['source-observation.json'])
        for mutate in [lambda d:d.update(extra=CANARY),lambda d:d['objects'][0]['base'].update(extra=CANARY),
                       lambda d:d['objects'][0]['supplemental'].update(descriptionBytes=base64.b64encode(CANARY.encode()).decode()),
                       lambda d:d['objects'][0]['identity'].update(servicePrincipalNameBytes=[]),
                       lambda d:d['objects'][0]['base'].update(distinguishedName='CN=foreign,DC=private,DC=invalid'),
                       lambda d:d['source'].update(domain='private.invalid'),lambda d:d['objects'].reverse()]:
            value=copy.deepcopy(original);mutate(value)
            with self.assertRaises(Exception):evidence.source_rows(evidence.encoded(value))
        for raw in [b'{"a":1,"a":2}',b'{"a":NaN}',b'['*65+b']'*65,b'\xff',b'{} trailing']:
            with self.assertRaises(Exception):evidence.strict_json(raw,1<<20)

    def test_bundle_files_symlinks_and_successful_stage_readback_are_strict(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary)/'bundle';directory.mkdir()
            for name,raw in self.blobs.items():(directory/name).write_bytes(raw)
            value=resources(self.blobs['provenance.json']);(directory/'resources.json').write_bytes(evidence.encoded(value))
            evidence.validate_bundle(directory,self.blobs['provenance.json'])
            for name in evidence.FILES:
                original=(directory/name).read_bytes();(directory/name).unlink()
                with self.assertRaises(Exception):evidence.validate_bundle(directory,self.blobs['provenance.json'])
                external=Path(temporary)/'outside';external.write_bytes(original);(directory/name).symlink_to(external)
                with self.assertRaises(Exception):evidence.validate_bundle(directory,self.blobs['provenance.json'])
                (directory/name).unlink();(directory/name).write_bytes(original)
            (directory/'extra').write_text(CANARY)
            with self.assertRaises(Exception):evidence.validate_bundle(directory,self.blobs['provenance.json'])
            (directory/'extra').unlink()
            link=Path(temporary)/'link';link.symlink_to(directory,target_is_directory=True)
            with self.assertRaises(Exception):evidence.validate_bundle(link,self.blobs['provenance.json'])
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);inputs=root/'input';inputs.mkdir()
            for name,raw in self.blobs.items():(inputs/name).write_bytes(raw)
            reference=root/'reference';reference.write_bytes(self.blobs['provenance.json'])
            pre=root/'pre';pre.write_bytes(evidence.encoded(resources(reference.read_bytes(),False)))
            target=root/'output'
            runtime=evidence.encoded(resources(reference.read_bytes())['runtime'])
            read=evidence.read_file
            def read_input(path,limit):
                return runtime if str(path)=='.e2e-diagnostics/runtime.json' else read(path,limit)
            with patch.object(gate,'OUTPUT',target),patch.object(gate,'PROVENANCE',reference),patch.object(gate,'PRE_RESOURCES',pre),\
                 patch.object(gate,'discover',return_value=inputs),patch.object(evidence,'capture_provenance',return_value=self.data['provenance']),\
                 patch.object(evidence,'read_file',side_effect=read_input):
                gate.publish()
            self.assertEqual(set(p.name for p in target.iterdir()),set(evidence.FILES))
            evidence.validate_bundle(target,reference.read_bytes())


    def test_failed_cleanup_and_staged_readback_remove_all_success_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);inputs=root/'input';inputs.mkdir()
            for name,raw in self.blobs.items():(inputs/name).write_bytes(raw)
            reference=root/'reference';reference.write_bytes(self.blobs['provenance.json'])
            pre=root/'pre';pre.write_bytes(evidence.encoded(resources(reference.read_bytes(),False)))
            target=root/'output';report=resources(reference.read_bytes())['runtime'];report['processes']['worker']['after_cleanup']=1
            read=evidence.read_file
            def bad_runtime(path,limit):
                return evidence.encoded(report) if str(path)=='.e2e-diagnostics/runtime.json' else read(path,limit)
            with patch.object(gate,'OUTPUT',target),patch.object(gate,'PROVENANCE',reference),patch.object(gate,'PRE_RESOURCES',pre),\
                 patch.object(gate,'discover',return_value=inputs),patch.object(evidence,'capture_provenance',return_value=self.data['provenance']),\
                 patch.object(evidence,'read_file',side_effect=bad_runtime):
                with self.assertRaises(ValueError):gate.publish()
            self.assertFalse(target.exists())
            report['processes']['worker']['after_cleanup']=0
            original=evidence.validate_bundle
            def tamper(directory,provenance,final=True):
                if Path(directory)==target:
                    (target/'ready.png').write_bytes(b'bad staged readback')
                return original(directory,provenance,final)
            with patch.object(gate,'OUTPUT',target),patch.object(gate,'PROVENANCE',reference),patch.object(gate,'PRE_RESOURCES',pre),\
                 patch.object(gate,'discover',return_value=inputs),patch.object(evidence,'capture_provenance',return_value=self.data['provenance']),\
                 patch.object(evidence,'read_file',side_effect=bad_runtime),patch.object(evidence,'validate_bundle',side_effect=tamper):
                with self.assertRaises(ValueError):gate.publish()
            self.assertFalse(target.exists())
            for name,raw in self.blobs.items():self.assertEqual((inputs/name).read_bytes(),raw)


class ResourceContracts(unittest.TestCase):
    def stat(self, pid=123, user='10', system='20', start='30', comm=b'a (space) name'):
        fields=[b'0']*49
        fields[10]=user.encode();fields[11]=system.encode();fields[18]=start.encode()
        return str(pid).encode()+b' ('+comm+b') S '+b' '.join(fields)+b'\n'

    def test_stat_status_bounded_numeric_units_and_parentheses(self):
        self.assertEqual(gate.parse_stat(self.stat(),123),{'userTicks':'10','systemTicks':'20','startTicks':'30'})
        for value in ('0',str((1<<63)-1)):
            self.assertEqual(gate.parse_stat(self.stat(user=value),123)['userTicks'],value)
        for raw in (b'',b'x'*65537,self.stat(pid=124),self.stat(user='-1'),self.stat(user='01'),
                    self.stat(user=str(1<<63)),self.stat(system='NaN'),self.stat(start='1.5'),
                    self.stat().replace(b' S ',b'  S '),self.stat()[:-5]):
            with self.assertRaises(ValueError):gate.parse_stat(raw,123)
        good=b'Name:\tignored (name)\nVmRSS:\t100 kB\nVmHWM:\t200 kB\n'
        self.assertEqual(gate.parse_status(good),{'vmRSSKiB':100,'vmHWMKiB':200})
        for raw in (b'',b'x'*65537,good+b'VmRSS:\t100 kB\n',good.replace(b' kB',b' MB'),
                    good.replace(b'100',b'-1'),good.replace(b'200',b'NaN'),good.replace(b'200',b'99'),
                    good.replace(b'100',str((1<<40)+1).encode()),good.replace(b'VmHWM:',b'Wrong:')):
            with self.assertRaises(ValueError):gate.parse_status(raw)

    def test_owned_popen_only_live_stable_endpoints_and_no_raw_error(self):
        # isinstance(Mock(spec=Popen), Popen) mirrors an owned-handle boundary;
        # runtime code receives only the runner's original Popen objects.
        child=Mock(spec=subprocess.Popen);child.pid=123;child.poll.return_value=None
        good_stat=self.stat();good_status=b'VmRSS:\t100 kB\nVmHWM:\t200 kB\n'
        with patch.object(evidence,'read_file',side_effect=[good_stat,good_status]) as read:
            first=gate.sample(child)
        self.assertEqual([str(call.args[0]) for call in read.call_args_list],['/proc/123/stat','/proc/123/status'])
        for candidate in (123,{'pid':123},None):
            with self.assertRaises(ValueError):gate.sample(candidate)
        child.poll.return_value=0
        with self.assertRaises(ValueError):gate.sample(child)
        child.poll.return_value=None
        for samples in ([self.stat(start='31'),good_status],[self.stat(user='9'),good_status],
                        [good_stat,b'VmRSS:\t100 kB\nVmHWM:\t199 kB\n']):
            with patch.object(evidence,'read_file',side_effect=samples),self.assertRaises(ValueError):gate.sample(child,first)
        for error in (FileNotFoundError(CANARY),PermissionError(CANARY)):
            with patch.object(evidence,'read_file',side_effect=error),self.assertRaises(ValueError):gate.sample(child,first)
        child.poll.side_effect=[None,0]
        with patch.object(evidence,'read_file',side_effect=[good_stat,good_status]),self.assertRaises(ValueError):gate.sample(child)

    def test_resource_scope_cpu_cleanup_all_terminal_rejections(self):
        ref=b'{}';good=resources(ref);evidence.validate_resources(good,ref)
        variants=[]
        for key in good:
            bad=copy.deepcopy(good);del bad[key];variants.append(bad)
        for change in (lambda d:d.update(scope='export_only'),lambda d:d.update(extra=CANARY),
                       lambda d:d.update(clockTicksPerSecond=True),lambda d:d.update(elapsedMs=0),
                       lambda d:d.update(limitations=[]),lambda d:d['api'].update(userCpuSeconds=float('nan')),
                       lambda d:d['api'].update(systemCpuSeconds=-1),lambda d:d['api'].update(startTicks='01'),
                       lambda d:d['worker'].update(pid=123),lambda d:d['api']['after'].update(userTicks='9'),
                       lambda d:d['api']['after'].update(vmHWMKiB=119),lambda d:d['api']['before'].update(vmRSSKiB=True),
                       lambda d:d['runtime'].update(http_panic=True),lambda d:d['runtime'].update(http_panic=None),
                       lambda d:d['runtime'].update(collection_errors=['snapshot']),lambda d:d['runtime']['phases'].reverse()):
            bad=copy.deepcopy(good);change(bad);variants.append(bad)
        for process in ('api','worker'):
            for key,value in [('started',False),('before_cleanup',0),('after_cleanup',None),('after_cleanup',1),('forced_kill',True)]:
                bad=copy.deepcopy(good);bad['runtime']['processes'][process][key]=value;variants.append(bad)
        for index in range(len(good['runtime']['phases'])):
            bad=copy.deepcopy(good);del bad['runtime']['phases'][index];variants.append(bad)
        for bad in variants:
            with self.subTest(value=repr(bad)[:100]),self.assertRaises(Exception):evidence.validate_resources(bad,ref)
        # Multicore process CPU can exceed walltime; endpoint HWM is lifetime,
        # not a delta and not a fabricated export-specific peak.
        valid=copy.deepcopy(good);valid['api']['after']['userTicks']='10010';valid['api']['userCpuSeconds']=100
        evidence.validate_resources(valid,ref)

    def test_final_child_has_whole_thirty_second_budget_and_failed_stage_removed(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination=Path(temporary)/'stage'
            with patch.object(gate,'OUTPUT',destination),patch.object(gate.subprocess,'run') as run:
                gate.validate_final()
                run.assert_called_once_with([sys_module().executable,str(Path(gate.__file__).resolve()),'publish-owned'],
                                            check=True,capture_output=True,timeout=30)
            for failure in (subprocess.TimeoutExpired('owned',30),subprocess.CalledProcessError(1,'owned'),OSError(CANARY)):
                def fail(*args,**kwargs):
                    destination.mkdir();(destination/'ready.png').write_bytes(b'unaccepted')
                    raise failure
                with patch.object(gate,'OUTPUT',destination),patch.object(gate.subprocess,'run',side_effect=fail):
                    with self.assertRaisesRegex(ValueError,'^multichunk_gate_rejected$'):
                        gate.validate_final()
                self.assertFalse(destination.exists())
            destination.mkdir()
            with patch.object(gate,'OUTPUT',destination),patch.object(gate.subprocess,'run') as run:
                with self.assertRaisesRegex(ValueError,'^multichunk_gate_rejected$'):gate.validate_final()
                run.assert_not_called()
            self.assertTrue(destination.is_dir())

    def test_core_does_not_stage_and_cli_swallows_raw_failure(self):
        source=Path(gate.__file__).read_bytes().decode('utf-8')
        tree=ast.parse(source)
        core=next(node for node in tree.body if isinstance(node,ast.FunctionDef) and node.name=='validate_core')
        body=ast.get_source_segment(source,core)
        self.assertNotIn('publish(',body);self.assertNotIn('mkdir(',body);self.assertNotIn('OUTPUT',body)
        with patch.object(sys_module(),'argv',['gate','validate']),patch.object(gate,'validate_final',side_effect=ValueError(CANARY)):
            with self.assertRaises(SystemExit) as raised:gate.main()
            self.assertEqual(str(raised.exception),gate.ERROR)
            self.assertNotIn(CANARY,str(raised.exception))


def sys_module():
    import sys
    return sys


class FixtureAndCIContracts(unittest.TestCase):
    def test_exact_suite_configuration_and_no_other_suite_marker(self):
        env={};gate.configure(env,gate.SUITE)
        self.assertEqual(env,{gate.MARKER:'true',gate.LDAP_MARKER:'true',
                             'ADTR_E2E_COMPUTER_ASSET_EXPORT_V3':'true',
                             'ADTR_E2E_LDAP_DIRECTORY_COMPUTER_ASSETS_V3':'true'})
        gate.configure(env,'computer-asset-export-v3')
        self.assertNotIn(gate.MARKER,env);self.assertNotIn(gate.LDAP_MARKER,env)
        conflicts=['ADTR_E2E_LDAP_DIRECTORY_'+suffix for suffix in (
            'GROUP_ASSETS_V3','V2','V4','EMPTY','SLOW','USER_ASSETS_V2','GROUP_MEMBERS_V4_PAGINATION',
            'GROUP_MEMBERS_V4_REFRESH','USER_EXPORT_FAILURE_V3','GROUP_EXPORT_FAILURE_V3','COMPUTER_EXPORT_FAILURE_V3')]
        conflicts+=['ADTR_E2E_USER_ASSET_EXPORT_V3','ADTR_E2E_GROUP_ASSET_EXPORT_V3',
                    'ADTR_DIRECTORY_READ_V2_ENABLED','ADTR_DIRECTORY_READ_V4_ENABLED',
                    'ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR','ADTR_E2E_LDAP_CONTROL_DIR']
        for key in conflicts:
            with self.subTest(key=key),self.assertRaises(ValueError):gate.configure({key:'true'},gate.SUITE)

    def test_python_fixture_rejects_invalid_before_allocation_and_exact_mode(self):
        import ldap_e2e_fixture as fixture
        valid=dict(directory_enabled=True,directory_v3=True,computer_assets_v3=True,computer_export_multichunk_v3=True)
        cases=[]
        for key in valid:
            for value in (None,0,1,'true',[],{}):cases.append(dict(valid,**{key:value}))
            cases.append(dict(valid,**{key:False})) if key!='computer_export_multichunk_v3' else None
        for key in ('control_enabled','directory_empty','directory_slow','directory_v2','directory_v4','user_assets_v2',
                    'group_assets_v3','empty_transition','group_members_v4_pagination','group_members_v4_refresh',
                    'user_export_failure_v3','group_export_failure_v3','computer_export_failure_v3'):
            cases.append(dict(valid,**{key:True}))
        with patch.object(fixture.uuid,'uuid4',side_effect=AssertionError('allocation occurred')) as allocate,\
             patch.object(fixture.tempfile,'TemporaryDirectory',side_effect=AssertionError('allocation occurred')) as temp:
            for kwargs in cases:
                with self.subTest(kwargs=kwargs),self.assertRaises((TypeError,ValueError)):fixture.LDAPFixture(**kwargs)
            allocate.assert_not_called();temp.assert_not_called()
        for enabled in (False,True):
            with tempfile.TemporaryDirectory() as temporary:
                stub=Mock();stub.name=temporary
                with patch.object(fixture.tempfile,'TemporaryDirectory',return_value=stub):
                    instance=fixture.LDAPFixture(**dict(valid,computer_export_multichunk_v3=enabled))
                env={gate.LDAP_MARKER:'true'}
                with patch.object(fixture.subprocess,'run') as run,\
                     patch.object(fixture.subprocess,'check_output',side_effect=['synthetic LDAP fixture ready','172.18.0.2']),\
                     patch.object(fixture,'postgres_image_args',return_value=['postgres:reviewed']):
                    instance.start(env)
                commands=[call.args[0] for call in run.call_args_list]
                command=next(command for command in commands if command[:2]==['docker','run'])
                self.assertEqual(command.count('-computer-export-multichunk-v3'),int(enabled))
                self.assertEqual(env.get(gate.LDAP_MARKER),'true' if enabled else None)
                self.assertEqual(command.count('-directory-v3'),1)
                self.assertEqual(command.count('-computer-assets-v3'),1)

    def test_exact_inverse_for_current_bytes_and_all_new_historical_anchor_mutations(self):
        delta=json.loads(Path(gate.DELTA).read_bytes())
        for path,record in delta['paths'].items():
            raw=Path(path).read_bytes();current=raw.decode('utf-8')
            previous=gate.restore_multichunk_source(current,path)
            self.assertEqual(evidence.digest(previous.encode()),record['baselineSHA256'])
            variants=[previous,current+'\n',current[:-1],current.replace('\n','\r\n')]
            for change in record['replacements']:
                new,old=change['new'],change['old']
                self.assertEqual(current.count(new),1)
                variants.extend([current.replace(new,old,1),current.replace(new,'',1),
                                 current.replace(new,new*2,1),current.replace(new,'',1)+new])
                for index,line in enumerate(new.splitlines(keepends=True)):
                    lines=new.splitlines(keepends=True)
                    variants.append(current.replace(new,''.join(lines[:index]+lines[index+1:]),1))
            # Enumerate actual new source occurrences of every inherited critical
            # guard: none is tested only on an already projected predecessor.
            anchors={'.github/workflows/ci.yml':['contents: read','max-parallel: 8','run: make check',"matrix.name == 'tasks-health-recovery' && 30 || 20"],
                     'scripts/test_auth_e2e.py':['timeout=30','else 930 if args.suite in COMPUTER_EXPORT_SUITES','    finally:\n'],
                     'scripts/ci_inventory.json':['"fixed": [','"web/src/TaskWorkspace.tsx": ['],
                     'scripts/ci_workflow_contract.py':['access bulk workflow delta is invalid','non-access-bulk workflow bytes changed']}[path]
            for anchor in anchors:
                self.assertGreater(current.count(anchor),0)
                start=0
                for _ in range(current.count(anchor)):
                    index=current.index(anchor,start);variants.append(current[:index]+'MUTATED'+current[index+len(anchor):]);start=index+len(anchor)
            for variant in variants:
                with self.subTest(path=path,sha=evidence.digest(variant.encode())),self.assertRaises(ValueError):gate.restore_multichunk_source(variant,path)
        for raw in [b'{}',b'{"format":1,"format":1}',b'['*2000+b']'*2000,b'\xff',Path(gate.DELTA).read_bytes()+b'\n']:
            original=Path.read_bytes
            def replacement(path):return raw if str(path)==gate.DELTA else original(path)
            with patch.object(Path,'read_bytes',replacement),self.assertRaises(ValueError):gate.restore_multichunk_source(Path('scripts/test_auth_e2e.py').read_text(),'scripts/test_auth_e2e.py')

    def test_registration_full57_jobs78_historical56_and_exact_upload(self):
        inventory=json.loads(Path('scripts/ci_inventory.json').read_bytes())
        self.assertEqual(len(inventory['browser']),57);self.assertEqual(57+7+8+6,78)
        self.assertEqual(len(inventory['fixed']),7)
        self.assertEqual(len(inventory['frontend_roots']['web/src/TaskWorkspace.tsx']),19)
        self.assertEqual([entry for entry in inventory['browser'] if entry['name']==gate.SUITE],
                         [{'name':gate.SUITE,'command':'python3 scripts/test_auth_e2e.py --suite '+gate.SUITE,
                           'specs':['web/e2e/'+gate.SUITE+'.spec.ts']}])
        self.assertIn(gate.SUITE,inventory['frontend_roots']['web/src/ComputerAssetsV3Workspace.tsx'])
        self.assertNotIn(gate.SUITE,inventory['fixed']);self.assertNotIn(gate.SUITE,inventory['frontend_roots']['web/src/TaskWorkspace.tsx'])
        predecessor=json.loads(gate.restore_multichunk_source(Path('scripts/ci_inventory.json').read_text(),'scripts/ci_inventory.json'))
        self.assertEqual(len(predecessor['browser']),56)
        self.assertEqual(inventory['browser'][:-1],predecessor['browser'])
        workflow=Path('.github/workflows/ci.yml').read_text()
        start=workflow.index('      - name: Validate multi-chunk computer export seven-file evidence\n')
        end=workflow.index('          retention-days: 14\n',start)+len('          retention-days: 14\n')
        block=workflow[start:end]
        self.assertGreater(start,workflow.index('run: ${{ matrix.command }}'))
        self.assertEqual(block.count("success() && matrix.name == '"+gate.SUITE+"'"),2)
        self.assertIn("steps.computer-multichunk-evidence.outcome == 'success'",block)
        self.assertEqual([line.strip() for line in block.splitlines() if line.startswith('            web/')],
                         [str(gate.OUTPUT)+'/'+name for name in evidence.FILES])
        for needed in ('timeout-minutes: 1','if-no-files-found: error','github.run_id','github.run_attempt',
                       'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02'):
            self.assertIn(needed,block)
        for forbidden in ('always()','*','continue-on-error','include-hidden-files'):
            self.assertNotIn(forbidden,block)
        runner=Path('scripts/test_auth_e2e.py').read_text()
        old=gate.restore_multichunk_source(runner,'scripts/test_auth_e2e.py')
        # Last main finally suffix is exactly the predecessor; no new cleanup.
        anchor='    finally:\n        if args.suite == "tasks-health-recovery":\n'
        self.assertEqual(runner[runner.index(anchor):],old[old.index(anchor):])
        self.assertLess(runner.index('capture_computer_multichunk()'),runner.index('        creation_attempted = True'))
        self.assertLess(runner.index('start_multichunk_samples(server, worker)'),runner.index('        diagnostics.phase("browser-start")'))
        self.assertLess(runner.index('finish_multichunk_samples(multichunk_samples, server, worker)'),runner.index('            validate_computer_multichunk()'))
        self.assertNotIn('COMPUTER_MULTICHUNK_SUITE',next(line for line in runner.splitlines() if line.startswith('COMPUTER_ASSETS_V3_SUITES =')))
        self.assertIn('include: ["src/**/*.test.{ts,tsx}"]',Path('web/vite.config.ts').read_text())

    def test_current_dependency_edges_and_underselection_fail_closed(self):
        import ci_selection as selection
        inventory=json.loads(Path('scripts/ci_inventory.json').read_bytes())
        spec='web/e2e/'+gate.SUITE+'.spec.ts';helper='web/e2e/computer-asset-export-v3-multichunk-evidence.ts'
        sources={path:'' for entry in inventory['browser'] for path in entry['specs']}
        sources[helper]='';sources.update({path:'' for path in inventory['frontend_roots']})
        graph={path:set() for path in sources};graph[spec]={helper}
        def choose(path,uncertain=()):
            with patch.object(selection,'dependency_graph',return_value=(graph,set(uncertain))):
                return selection.select([path],inventory,[sources,sources])
        self.assertEqual([entry['name'] for entry in choose(helper)['browser']],[gate.SUITE])
        self.assertIn(gate.SUITE,[entry['name'] for entry in choose('web/src/ComputerAssetsV3Workspace.tsx')['browser']])
        for path in ('scripts/test_auth_e2e.py','scripts/ldap_e2e_fixture.py','internal/testldapfixture/computer_export_multichunk_v3.go'):
            self.assertEqual(choose(path)['browser'],inventory['browser'])
        self.assertEqual(choose(helper,[spec])['browser'],inventory['browser'])

    def test_actual_new_gate_and_validator_branches_cannot_be_removed_or_widened(self):
        for path,expected in SOURCE_GUARDS.items():
            raw=Path(path).read_bytes();self.assertEqual(evidence.digest(raw),expected)
            source=raw.decode('utf-8')
            anchors=MUTATION_ANCHORS[path]
            for anchor,count in anchors:
                self.assertEqual(source.count(anchor),count,(path,anchor))
                cursor=0
                for _ in range(count):
                    index=source.index(anchor,cursor)
                    for replacement in ('',anchor+' # widened',anchor*2):
                        candidate=source[:index]+replacement+source[index+len(anchor):]
                        self.assertNotEqual(evidence.digest(candidate.encode()),expected)
                    cursor=index+len(anchor)
            # Every guard-bearing line is independently counted on actual source,
            # including nested/generated mutations; no inverse-only fake anchor.
            for line in source.splitlines(keepends=True):
                if 'require(' in line or 'raise ' in line:
                    index=source.index(line)
                    self.assertNotEqual(evidence.digest((source[:index]+source[index+len(line):]).encode()),expected)


class ProvenanceContracts(unittest.TestCase):
    def test_actual_head_run_tree_blob_capture_and_no_stale_reference(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);bodies={path:b'reviewed executable\n' for path in evidence.REQUIRED_SOURCES}
            for path,body in bodies.items():
                (root/path).parent.mkdir(parents=True,exist_ok=True);(root/path).write_bytes(body)
            expected={'checkoutCommit':'a'*40,'checkoutTree':'b'*40,'runID':'123','runAttempt':'1',
                      'sourceSHA256':{path:evidence.digest(body) for path,body in sorted(bodies.items())}}
            tree=b''.join(b'100644 blob '+hashlib.sha1(b'blob '+str(len(body)).encode()+b'\0'+body).hexdigest().encode()+b'\t'+path.encode()+b'\0' for path,body in sorted(bodies.items()))
            def calls(tree_bytes=tree,dirty=b'',untracked=b''):
                return [subprocess.CompletedProcess([],0,stdout=value) for value in (dirty,untracked,b'web/vite.config.ts\n',tree_bytes)]
            env={'GITHUB_SHA':'a'*40,'GITHUB_RUN_ID':'123','GITHUB_RUN_ATTEMPT':'1'}
            with patch.dict(os.environ,env),patch.object(evidence,'checkout_provenance',return_value=copy.deepcopy(expected)),\
                 patch.object(evidence.subprocess,'run',side_effect=calls()):
                self.assertEqual(evidence.capture_provenance(root),expected)
            for key in env:
                bad=dict(env);bad[key]='wrong'
                with patch.dict(os.environ,bad),patch.object(evidence,'checkout_provenance',return_value=copy.deepcopy(expected)),\
                     patch.object(evidence.subprocess,'run',side_effect=calls()),self.assertRaises(Exception):
                    evidence.capture_provenance(root)
            records=tree.split(b'\0')[:-1]
            variants=[b'',tree[:-1],b'\0'.join(records[1:])+b'\0',tree+records[0]+b'\0',
                      tree.replace(b'100644',b'120000',1),tree.replace(b' blob ',b' tree ',1),
                      tree.replace(records[0][12:52],b'0'*40,1),tree+b'100644 blob '+b'0'*40+b'\textra\0']
            for invalid in variants:
                with patch.dict(os.environ,env),patch.object(evidence,'checkout_provenance',return_value=copy.deepcopy(expected)),\
                     patch.object(evidence.subprocess,'run',side_effect=calls(invalid)),self.assertRaises(Exception):
                    evidence.capture_provenance(root)
            for dirty,untracked in [(b'M tracked',b''),(b'',b'untracked.ts')]:
                with patch.dict(os.environ,env),patch.object(evidence,'checkout_provenance',return_value=copy.deepcopy(expected)),\
                     patch.object(evidence.subprocess,'run',side_effect=calls(dirty=dirty,untracked=untracked)),self.assertRaises(Exception):
                    evidence.capture_provenance(root)
            destination=root/'trusted.json'
            with patch.object(evidence,'capture_provenance',return_value=expected):gate.capture(root,destination)
            original=destination.read_bytes()
            with patch.object(evidence,'capture_provenance',side_effect=ValueError(CANARY)),self.assertRaises(ValueError):gate.capture(root,destination)
            self.assertEqual(destination.read_bytes(),original)

    def test_writer_literals_are_bound_to_actual_production_source(self):
        source=Path('internal/computerassetxlsx/writer.go').read_bytes()
        self.assertEqual(evidence.digest(source),'5d49a9d453bd0277469464951e72ea106f99b5a7b2a9e29b488a6a470bb5911e')
        for name,body in evidence.STATIC_PARTS:
            self.assertIn(name.encode(),source);self.assertIn(body,source)
        self.assertIn(evidence.WORKSHEET_START,source);self.assertIn(evidence.WORKSHEET_END,source)
        self.assertIn('web/vite.config.ts',evidence.REQUIRED_SOURCES)
        for path in ('scripts/validate_computer_asset_export_v3_multichunk_evidence.py',
                     'scripts/test_computer_asset_export_v3_multichunk_contract.py',
                     'web/src/ComputerExportMultichunkEvidence.test.ts'):
            self.assertIn(path,evidence.REQUIRED_SOURCES)

# Exact candidate sources and expanded actual mutation anchors.
SOURCE_GUARDS = {'scripts/computer_asset_export_v3_multichunk_ci.py': '5a9895297fac0f542a242f2a5c17eb0324bb8f4916a3e213fc8028071a549976', 'scripts/validate_computer_asset_export_v3_multichunk_evidence.py': 'e6e7d07611ff4ddd9a976d9436a9f8f189449d22d52dbe04d8a53c5595736e9d', 'internal/testldapfixture/main.go': '1c3042a38af9e014c2543ecac637cf31b83a039dd35322203608a8111a19aa2e', 'internal/testldapfixture/server.go': '77ba8d51183b3d730628976aef993804e0e6090531957cb2f8a0c26dc870f1e1', 'internal/testldapfixture/directory.go': 'e11269bfb05f3ddd0a82ad2f2156a5b1f8d0a95fb0a841f7b64113125b4b30c3', 'scripts/ldap_e2e_fixture.py': 'a3d42127c894eb9c884e29f644bfdf10e3d97ddcf28920b7634a98d465a502d9', 'internal/testldapfixture/computer_export_multichunk_v3.go': '75ed936da30e98f8b697df7686c51161cc7715576070a02a63d39a7b93cdf2ae', 'internal/testldapfixture/computer_export_multichunk_v3_test.go': 'f8abb30be58aa1fe021e8c4a362f4792f5ec15b685ffbc6c18ecadc778e93157', 'web/e2e/computer-asset-export-v3-multichunk.spec.ts': 'eabcd2a1ff939ebd28b4ac4f14f75155a3438ae00a36446e2f9af8f6925261d5', 'web/e2e/computer-asset-export-v3-multichunk-evidence.ts': '0c22d37ffde9655d82f02c9da56fde832d9c8709f3644ac0388ca4badd436591', 'web/src/ComputerExportMultichunkEvidence.test.ts': '2ffafed0b9c40514a8e3f5c7489860527be5dbd8206877c46528f1f773a1f03f'}
MUTATION_ANCHORS = {'scripts/computer_asset_export_v3_multichunk_ci.py': [('                       check=True, capture_output=True, timeout=30)\n', 1), ("                    '--repo', '.', '--core'], check=True, capture_output=True, timeout=30)\n", 1), ("            key = 'vmRSSKiB' if match[1] == b'VmRSS' else 'vmHWMKiB'\n", 1), ('            raise ValueError(ERROR) from None\n', 1), ("            raw = resource_bytes if name == 'resources.json' else original[name]\n", 1), ("            require(int(value['sample'][key]) >= int(expected['sample'][key]))\n", 1), ('            require(key not in result and int(match[2]) <= 1 << 40)\n', 1), ('            require(match is not None)\n', 1), ('            shutil.rmtree(destination)\n', 2), ('            validate_final()\n', 1), ("        elif args.action == 'core':\n", 1), ("        elif args.action == 'publish-owned':\n", 1), ("        if args.action == 'capture':\n", 1), ('        if destination.exists() and not destination.is_symlink():\n', 2), ("        if line.startswith((b'VmRSS:', b'VmHWM:')):\n", 1), ('        raise SystemExit(ERROR) from None\n', 1), ('        raise ValueError(ERROR)\n', 1), ('        raise ValueError(ERROR) from None\n', 3), ('        require(all(read_file(directory / name, LIMITS[name]) == original[name] for name in FILES[:-1]))\n', 1), ('        require(all(staged[name] == original[name] for name in FILES[:-1]))\n', 1), ("        require(pid == expected['pid'] and start == expected['startTicks'])\n", 1), ("        require(re.fullmatch(rb'0|[1-9][0-9]{0,18}', value) and int(value) <= (1 << 63) - 1)\n", 1), ("        require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(Path('.')))\n", 1), ('        require(text[offset:offset + len(new)] == new)\n', 1), ("        require(type(change) is dict and set(change) == {'old', 'new', 'offset'})\n", 1), ('        require(type(offset) is int and 0 <= offset <= len(text) and text.count(new) == 1)\n', 1), ('        require(type(old) is str and type(new) is str and old and new and old != new)\n', 1), ("        subprocess.run([sys.executable, str(Path(__file__).resolve()), 'publish-owned'],\n", 1), ('    if expected is not None:\n', 1), ('    if not condition:\n', 1), ('    if suite != SUITE:\n', 1), ("    parser.add_argument('action', choices=['capture', 'validate', 'core', 'publish-owned'])\n", 1), ('    require(0 < len(raw) <= 256 << 10 and hashlib.sha256(raw).hexdigest() == DELTA_SHA256)\n', 1), ("    require(49 <= len(fields) <= 61 and all(re.fullmatch(rb'-?[0-9]{1,20}', value) for value in fields))\n", 1), ('    require(api is not worker)\n', 1), ("    require(hashlib.sha256(text.encode('utf-8')).hexdigest() == PREDECESSORS[path])\n", 1), ("    require(hashlib.sha256(text.encode('utf-8')).hexdigest() == record['candidateSHA256'])\n", 1), ('    require(isinstance(process, subprocess.Popen) and process.poll() is None)\n', 1), ('    require(len(matches) == 1)\n', 1), ("    require(len(resource_bytes) <= LIMITS['resources.json'])\n", 1), ('    require(match is not None and int(match[1]) == expected_pid)\n', 1), ("    require(not any(env.get('ADTR_E2E_LDAP_DIRECTORY_' + key) == 'true' for key in conflicts))\n", 1), ("    require(not any(env.get(key) == 'true' for key in ('ADTR_E2E_USER_ASSET_EXPORT_V3',\n", 1), ('    require(not destination.exists())\n', 4), ("    require(not env.get('ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR') and not env.get('ADTR_E2E_LDAP_CONTROL_DIR'))\n", 1), ('    require(process.pid == pid and process.poll() is None)\n', 1), ("    require(record['baselineSHA256'] == PREDECESSORS[path])\n", 1), ("    require(set(delta['paths']) == set(PREDECESSORS) and path in PREDECESSORS)\n", 1), ("    require(set(result) == {'vmRSSKiB', 'vmHWMKiB'} and result['vmHWMKiB'] >= result['vmRSSKiB'])\n", 1), ("    require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(Path('.')))\n", 3), ("    require(type(delta) is dict and set(delta) == {'format', 'paths'} and type(delta['format']) is int and delta['format'] == 1)\n", 1), ('    require(type(hz) is int and 1 <= hz <= 1000000)\n', 1), ('    require(type(pid) is int and 1 <= pid < 1 << 31)\n', 1), ('    require(type(raw) is bytes and 0 < len(raw) <= 65536)\n', 2), ("    require(type(record) is dict and set(record) == {'baselineSHA256', 'candidateSHA256', 'replacements'})\n", 1), ("    require(type(record['replacements']) is list and 1 <= len(record['replacements']) <= 32)\n", 1), ("    require(type(text) is str and '\\r' not in text)\n", 1), ("    subprocess.run([sys.executable, VALIDATOR, str(directory), '--provenance', str(PROVENANCE),\n", 1), ('def require(condition):\n', 1), ('def validate_final():\n', 1), ('if __name__ == "__main__":\n', 1)], 'scripts/validate_computer_asset_export_v3_multichunk_evidence.py': [('            elif char == \'"\':\n', 1), ("            elif char == '\\\\':\n", 1), ('            if escaped:\n', 1), ("            require(base['userAccountControl'] is None)\n", 1), ('            require(depth <= 64)\n', 1), ('            require(depth >= 0)\n', 1), ('            require(expected >= 0 and type(process[field]) in (int, float) and math.isfinite(process[field]) and process[field] == expected)\n', 1), ("            require(index == 4 and event['action'] == 'progress')\n", 1), ('            require(key not in value)\n', 1), ("            require(kind == b'IEND' and size == 0 and state == 'data' and chunks and end == len(raw))\n", 1), ("            require(kind == b'IHDR' and body == struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0))\n", 1), ("            require(process == {'started': True, 'before_cleanup': None, 'after_cleanup': 0, 'forced_kill': False})\n", 1), ("            require(re.fullmatch(r'[A-Za-z0-9 ,=.$:\\-/]+', cell) is not None)\n", 1), ("            require(sample['vmHWMKiB'] >= sample['vmRSSKiB'])\n", 1), ('            require(size > 0)\n', 1), ('            require(type(cell) is str and cell.isascii() and 0 < len(cell) <= 8192)\n', 1), ("            suffix = ',OU=' + ('Users' if kind == 'user' else 'Groups')\n", 1), ('        elif char == \'"\':\n', 2), ("        elif char in '[{':\n", 1), ("        elif char in ']}':\n", 1), ("        elif kind == b'IDAT' and state == 'data':\n", 1), ("        fields = 'taskID request task' + (' replayed' if key == 'create' else ' downloadReady downloadStatus artifact' if key == 'detail' else '')\n", 1), ("        if index < len(required) and event['action'] == required[index]:\n", 1), ("        if kind == 'computer':\n", 2), ('        if quoted:\n', 1), ("        if state == 'header':\n", 1), ("        kind = 'computer' if index <= 1020 else 'user' if index <= 1022 else 'group'\n", 1), ('        raise SystemExit(ERROR) from None\n', 1), ('        raise ValueError(ERROR)\n', 1), ('        require((extra_len, comment_len, disk, internal, external) == (0, 0, 0, 0, 0))\n', 1), ('        require((signature, made, needed, flags, method, dos_time, dos_date) == (0x02014b50, 20, 20, 8, 8, 0, 0))\n', 1), ("        require([phase['phase'] for phase in report['phases']] == required)\n", 1), ("        require(base['distinguishedName'] == 'CN=' + name + suffix + ',DC=synthetic,DC=invalid')\n", 1), ("        require(base['objectClass'] == (['computer', 'organizationalperson', 'person', 'top', 'user'] if kind == 'computer' else ['organizationalperson', 'person', 'top', 'user'] if kind == 'user' else ['group', 'top']))\n", 1), ("        require(base['objectGUID'] == expected_guid and base['kind'] == kind)\n", 1), ("        require(base['samAccountName'] == name + ('$' if kind == 'computer' else ''))\n", 1), ('        require(body == expected_body)\n', 1), ('        require(body_end + 16 <= central_start)\n', 1), ('        require(count <= 4096 and cursor + 12 <= len(raw))\n', 1), ('        require(cursor + 46 <= len(raw) - 22)\n', 1), ("        require(decode(extra['whenCreatedBytes']) == b'20260101000000.0Z')\n", 1), ("        require(digest(body) == value['sourceSHA256'][path])\n", 1), ('        require(end <= len(raw))\n', 1), ("        require(event['action'] in required and event['state'] in ('queued', 'running', 'succeeded'))\n", 1), ("        require(event['state'] == ('queued' if event['action'] in ('submitted', 'leased') else 'succeeded' if event['action'] == 'finished' else 'running'))\n", 1), ("        require(extra['mailBytes'] is None and extra['descriptionBytes'] is None\n", 1), ("        require(hashlib.sha1(b'blob ' + str(len(body)).encode() + b'\\0' + body).hexdigest().encode() == oid)\n", 1), ("        require(len(body) == part['byteCount'] and digest(body) == part['sha256'])\n", 1), ('        require(len(encoded(obj)) <= 8192)\n', 1), ('        require(len(result.stdout) <= 2 << 20)\n', 1), ('        require(len(validate_report(report)) <= 64 << 10)\n', 1), ("        require(list(base) == ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl'])\n", 1), ("        require(list(extra) == ['objectSidBytes', 'mailBytes', 'descriptionBytes', 'whenCreatedBytes'])\n", 1), ("        require(list(identity) == ['primaryGroupIDBytes', 'servicePrincipalNameBytes'])\n", 1), ("        require(list(obj) == ['base', 'supplemental', 'identity'])\n", 1), ('        require(local == (0x04034b50, 20, 8, 8, 0, 0, 0, 0, 0, len(name), 0))\n', 1), ('        require(local_cursor + 30 + name_len <= central_start)\n', 1), ('        require(local_offset == local_cursor and expanded == len(expected_body) and 0 < compressed <= 32 << 20)\n', 1), ('        require(manifest[key] == result[key])\n', 1), ("        require(mode in (b'100644', b'100755') and kind == b'blob' and path not in seen)\n", 1), ('        require(name_len == len(name) and cursor + 46 + name_len <= len(raw) - 22)\n', 1), ('        require(ordinal == count - 1 or len(chunk) == 1 << 20)\n', 1), ("        require(part['offset'] == offset)\n", 1), ("        require(part['sha256'] == digest(chunk))\n", 1), ("        require(part['taskID'] == task_id and part['fencingToken'] == task['fencingToken'])\n", 1), ("        require(path in value['sourceSHA256'])\n", 1), ("        require(process['after']['vmHWMKiB'] >= process['before']['vmHWMKiB'])\n", 1), ("        require(projection['taskID'] == task_id and projection['request'] == request)\n", 1), ('        require(raw[cursor + 46:cursor + 46 + name_len] == name)\n', 1), ('        require(raw[local_cursor + 30:local_cursor + 30 + name_len] == name)\n', 1), ("        require(report['http_panic'] is False and report['collection_errors'] == [])\n", 1), ("        require(sid_raw == b'\\x01\\x01\\0\\0\\0\\0\\0\\x05' + struct.pack('<I', index))\n", 1), ('        require(snapshot[key] == task[key])\n', 1), ("        require(source[key]['fencingToken'] == producer['fencingToken'])\n", 1), ('        require(stat.S_ISREG(os.fstat(source.fileno()).st_mode))\n', 1), ("        require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(args.repo))\n", 1), ("        require(struct.unpack_from('<IIII', raw, body_end) == (0x08074b50, crc, compressed, expanded))\n", 1), ("        require(task['state'] == 'succeeded')\n", 1), ('        require(times == sorted(times))\n', 1), ("        require(type(path) is str and 0 < len(path) <= 256 and not path.startswith('/')\n", 1), ('        require(type(raw) is bytes and 0 < len(raw) <= LIMITS[name])\n', 1), ('        require(type(row) is list and len(row) == 5)\n', 1), ('        require(version >= prior)\n', 1), ('        require(zlib.crc32(body) == crc)\n', 1), ("        require(zlib.crc32(kind + body) == int.from_bytes(raw[end - 4:end], 'big'))\n", 1), ("    exact(value, 'format suite provenanceSHA256 scope clockTicksPerSecond elapsedMs api worker limitations' + (' runtime' if final else ''))\n", 1), ('    if final:\n', 3), ('    if not condition:\n', 1), ('    names = FILES if final else FILES[:-1]\n', 1), ("    number(data['format'], 1, 1); require(data['suite'] == SUITE and data['provenance'] == provenance)\n", 1), ('    require((1 << 20) < len(db) <= 32 << 20 and db == download)\n', 1), ('    require(0 < len(raw) <= limit)\n', 1), ("    require(0 <= milliseconds <= 125000 and abs(milliseconds - source['elapsed_milliseconds']) < 2)\n", 1), ("    require(REQUIRED_SOURCES <= set(value['sourceSHA256']))\n", 1), ('    require(all(not part.is_symlink() for part in (path, *path.parents)))\n', 1), ('    require(all(pixels[row * (1 + 1440 * 3)] <= 4 for row in range(900)))\n', 1), ("    require(all(type(request[key]) is str for key in request if key != 'screenColumns'))\n", 1), ("    require(artifact == {'rowCount': 1020, 'byteCount': len(download), 'sha256': digest(download), 'filename': filename})\n", 1), ("    require(base64.b64encode(raw).decode('ascii') == value)\n", 1), ("    require(blobs['provenance.json'] == provenance_raw)\n", 1), ('    require(central_start + central_size == len(raw) - 22)\n', 1), ("    require(data['cells'] == {'rowCount': 1020, 'columnCount': 5, 'firstRowSHA256': digest(encoded(matrix[1])),\n", 1), ("    require(data['create']['replayed'] is False and data['detail']['downloadReady'] is True and data['detail']['downloadStatus'] == 'ready')\n", 1), ("    require(data['timings']['scope'] == 'browser_export_submit_to_download')\n", 1), ('    require(depth == 0 and not quoted)\n', 1), ('    require(directory.is_dir() and {path.name for path in directory.iterdir()} == set(names))\n', 1), ('    require(encoded(value) == raw)\n', 1), ('    require(end[:5] == (0x06054b50, 0, 0, 6, 6) and end[7] == 0)\n', 1), ("    require(entries[-1] == b'')\n", 1), ("    require(git('ls-files', '--', 'web/vite.config.ts').strip() == b'web/vite.config.ts')\n", 1), ("    require(index == 5 and events[-1]['resultVersion'] == task['resultVersion'])\n", 1), ('    require(len(body) == expected and inflater.eof and not inflater.unused_data and not inflater.unconsumed_tail)\n', 1), ('    require(len(matrix) == 1021)\n', 1), ('    require(len(result) <= 256 << 20)\n', 1), ("    require(list(source) == ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'])\n", 1), ("    require(list(value) == ['dictionaryVersion', 'objects', 'source'])\n", 1), ("    require(listing['observationId'] == source['taskUUID'])\n", 1), ('    require(local_cursor == central_start and cursor == len(raw) - 22)\n', 1), ('    require(low <= int(value) <= high)\n', 1), ("    require(manifest['chunkCount'] == count and type(database['chunks']) is list and len(database['chunks']) == count)\n", 1), ("    require(manifest['taskID'] == task_id and manifest['fencingToken'] == task['fencingToken'])\n", 1), ("    require(not git('diff', 'HEAD', '--', *inputs).strip())\n", 1), ("    require(not git('ls-files', '--others', '--exclude-standard', '--', *inputs).strip())\n", 1), ('    require(offset == len(raw))\n', 1), ("    require(os.environ.get('GITHUB_RUN_ID') == value['runID'] and os.environ.get('GITHUB_RUN_ATTEMPT') == value['runAttempt'])\n", 1), ("    require(os.environ.get('GITHUB_SHA') == value['checkoutCommit'])\n", 1), ("    require(pins['grantRoleId'] == 'platform_admin')\n", 1), ("    require(producer['taskID'] == source['taskUUID'] and producer['tenantId'] == source['tenantId']\n", 1), ("    require(request['domainId'] == source['domainId'] and request['observationId'] == source['taskUUID'])\n", 1), ("    require(request['expectedRevision'] == source['selection']['revision']\n", 1), ("    require(request['search'] == 'computer-' and request['startTm'] == '2026-01-01T00:00:00Z'\n", 1), ("    require(result['artifactToken'] == task['fencingToken'])\n", 1), ("    require(result['byteCount'] == len(raw) and result['sha256'] == digest(raw))\n", 1), ("    require(seen == set(value['sourceSHA256']))\n", 1), ('    require(set(blobs) == set(FILES[:-1]))\n', 1), ("    require(snapshot['observationId'] == source['taskUUID'] and snapshot['sourceSHA256'] == source['file']['sha256'])\n", 1), ("    require(source['observationOpener'] == source['useOpener'])\n", 1), ("    require(source['purpose'] == 'domain.directory_read.v3' and source['useState'] == 'quiesced'\n", 1), ("    require(source['selection']['domainId'] == source['domainId'])\n", 1), ("    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid'\n", 1), ("    require(source['tenantId'] == 'default')\n", 1), ("    require(state == 'end' and cursor == len(raw))\n", 1), ("    require(str(actor) != source['actorId'])\n", 1), ("    require(strict_json(blobs['provenance.json'], LIMITS['provenance.json']) == provenance)\n", 1), ("    require(task['archived'] is False)\n", 1), ("    require(task['payloadHash'] == digest(b'1::' + encoded({key: request[key] for key in sorted(request)})))\n", 1), ("    require(task['state'] in ('queued', 'running', 'succeeded'))\n", 1), ("    require(task['taskID'] == task_id and task['tenantId'] == 'default' and task['actorId'] == str(actor)\n", 1), ("    require(task['taskUUID'] == task_id and task['domainId'] == domain and task['taskName'] == 'computer_assets.v3.export')\n", 1), ("    require(transfer['headers'] == expected_headers)\n", 1), ("    require(transfer['suggestedFilename'] == filename)\n", 1), ('    require(type(events) is list and 5 <= len(events) <= 1000)\n', 1), ('    require(type(expected) is int and 0 <= expected <= 256 << 20)\n', 1), ('    require(type(matrix) is list and len(matrix) == 1021 and matrix[0] == HEADERS)\n', 1), ("    require(type(raw) is bytes and 0 < len(raw) <= 10 << 20 and raw[:8] == b'\\x89PNG\\r\\n\\x1a\\n')\n", 1), ('    require(type(raw) is bytes and 0 < len(raw) <= 32 << 20 and len(raw) >= 22)\n', 1), ('    require(type(raw) is bytes and 0 < len(raw) <= limit)\n', 1), ("    require(type(source['segments']) is list and 1 <= len(source['segments']) <= 32)\n", 1), ('    require(type(value) is dict and set(value) == set(fields.split() if type(fields) is str else fields))\n', 1), ('    require(type(value) is int and low <= value <= high)\n', 1), ('    require(type(value) is str and re.fullmatch(expression, value) is not None)\n', 1), ("    require(type(value) is str and re.fullmatch(r'0|[1-9][0-9]{0,18}', value) is not None)\n", 1), ('    require(type(value) is str)\n', 1), ("    require(type(value['objects']) is list and len(value['objects']) == 1024)\n", 1), ("    require(type(value['sourceSHA256']) is dict and 1 <= len(value['sourceSHA256']) <= 10000)\n", 1), ("    require(value['api']['pid'] != value['worker']['pid'])\n", 1), ("    require(value['byteCount'] == len(raw) and value['sha256'] == digest(raw))\n", 1), ("    require(value['filename'] == name)\n", 1), ("    require(value['suite'] == SUITE and value['scope'] == 'full_browser_phase'\n", 1), ('    require(workbook_rows(raw) == matrix)\n', 1), ('def require(condition):\n', 1), ("if __name__ == '__main__':\n", 1)], 'internal/testldapfixture/main.go': [('\t\tif c == 0 {\n', 1), ('\tif _, err := fmt.Fprintln(output, "synthetic LDAP fixture ready"); err != nil {\n', 1), ('\tif control != nil {\n', 1), ('\tif emptyTransition != nil {\n', 1), ('\tif err != nil {\n', 7), ('\tif err != nil || port != wantPort {\n', 1), ('\tif flags.Parse(args) != nil || flags.NArg() != 0 || cfg.cert == "" || cfg.key == "" ||\n', 1), ('\tif len(cfg.username) == 0 || len(cfg.username) > 1024 || !utf8.Valid(cfg.username) ||\n', 1)], 'internal/testldapfixture/server.go': [('\t\t\t\t\tif heldTicket != "" && !s.control.wait(ctx, heldTicket) {\n', 1), ('\t\t\t\t\tif s.control.mark(heldTicket, "response_written") != nil {\n', 1), ('\t\t\t\tif err != nil {\n', 1), ('\t\t\t\tif err == nil && heldTicket != "" && !responseWritten {\n', 1), ('\t\t\t\tif err == nil {\n', 2), ('\t\t\t\tif heldTicket == "" {\n', 1), ('\t\t\t\tif result(conn, req.id, 0x78, 53, "synthetic operation rejected") != nil {\n', 1), ('\t\t\tif !secure || !bound {\n', 1), ('\t\t\tif !valid || secure || bound {\n', 1), ('\t\t\tif directoryMode && attribute.name == "dnsHostName" {\n', 1), ('\t\t\tif err != nil {\n', 2), ('\t\t\tif result(conn, req.id, 0x61, code, diagnostic) != nil {\n', 1), ('\t\t\tif result(conn, req.id, 0x78, 0, "") != nil || !upgrade() {\n', 1), ('\t\t\toid, parseErr := fields.expect(0x80)\n', 1), ('\t\t\t} else if !valid {\n', 1), ('\t\t\t} else if directorySearch {\n', 1), ('\t\tif !allowed || selected[key] {\n', 1), ('\t\tif _, err := fields.integer(0x02); err != nil {\n', 1), ('\t\tif err != nil {\n', 3), ('\t\tif err != nil || len(name) > 64 {\n', 1), ('\t\tif err != nil || value != 0 {\n', 1), ('\t\tif heldTicket != "" {\n', 1), ('\t\tif len(req.controls) != 0 && (!s.directoryMode || req.tag != 0x63) {\n', 1), ('\t\tif len(selected) == 0 || selected[strings.ToLower(attribute.name)] {\n', 1), ('\t\tif len(selected) >= len(rootAttributes) {\n', 1), ('\t\tif tlsConn.HandshakeContext(ctx) != nil {\n', 1), ('\t\tname, err := attributes.expect(0x04)\n', 1), ('\tbase, err := fields.expect(0x04)\n', 1), ('\tencoded, err := fields.expect(0x30)\n', 1), ('\tfilter, err := fields.expect(0x87)\n', 1), ('\tif !secure {\n', 1), ('\tif err != nil || !strings.EqualFold(string(filter), "objectClass") {\n', 1), ('\tif err != nil || len(base) != 0 {\n', 1), ('\tif err != nil || len(fields) != 0 {\n', 1), ('\tif err != nil || len(password) > 4096 || len(fields) != 0 {\n', 1), ('\tif err != nil || len(typesOnly) != 1 || typesOnly[0] != 0 {\n', 1), ('\tif err != nil || len(username) > 1024 {\n', 1), ('\tif err != nil || version != 3 {\n', 1), ('\tif ldaps && !upgrade() {\n', 1), ('\tif len(username) == 0 || len(password) == 0 ||\n', 1), ('\tif raw.SetDeadline(time.Now().Add(connectionTime)) != nil {\n', 1), ('\tpassword, err := fields.expect(0x80)\n', 1), ('\ttypesOnly, err := fields.expect(0x01)\n', 1), ('\tusername, err := fields.expect(0x04)\n', 1)], 'internal/testldapfixture/directory.go': [('\t\t\tif d.computerExportMultichunkV3 {\n', 1), ('\t\tif _, err := rand.Read(next[4:]); err != nil {\n', 1), ('\t\tif d.computerAssetsV3 {\n', 1), ('\t\tif d.groupAssetsV3 {\n', 1), ('\t\tif d.groupMembersV4Pagination {\n', 1), ('\t\tif d.userAssetsV2 {\n', 1), ('\t\tif d.v3 {\n', 1), ('\t\tif d.v4 {\n', 1), ('\t\tif err := ctx.Err(); err != nil {\n', 1), ('\t\tif err := writeAll(conn, entry); err != nil {\n', 1), ('\t\tif kind == "computer" {\n', 1), ('\tif d.computerExportMultichunkV3 {\n', 3), ('\tif d.empty {\n', 2), ('\tif d.emptyTransition != nil {\n', 1), ('\tif d.emptyTransition == nil || d.transitionSampled {\n', 1), ('\tif d.page >= d.pages() || !bytes.Equal(req.body, body) {\n', 1), ('\tif d.page+1 < d.pages() {\n', 1), ('\tif d.slow {\n', 2), ('\tif d.v2 {\n', 1), ('\tif d.v3 {\n', 2), ('\tif d.v4 {\n', 2), ('\tif err != nil {\n', 1), ('\tif err := ctx.Err(); err != nil {\n', 2), ('\tif err := writeAll(conn, done); err != nil {\n', 1), ('\tif row != 24 {\n', 1), ('\tif row == 1 {\n', 1), ('\tif row > 12 {\n', 1), ('\tif row > 24 {\n', 1), ('\tif uac != "" {\n', 1), ('\tif v2 && row != 24 {\n', 1)], 'scripts/ldap_e2e_fixture.py': [('                            if self.control_directory is not None:\n', 1), ('                            if self.empty_transition_directory is not None:\n', 1), ('                        if self.directory is not None:\n', 1), ('                    if self.created_network:\n', 1), ('                if code != "0":\n', 1), ('                if self.created_container:\n', 1), ('                raise RuntimeError("synthetic LDAP fixture exited during startup")\n', 1), ('            if "synthetic LDAP fixture ready" in output:\n', 1), ('            if enabled:\n', 1), ('            if running != "true":\n', 1), ('            if self.created_container:\n', 2), ('            raise RuntimeError("synthetic LDAP fixture failed readiness")\n', 1), ('            raise RuntimeError("synthetic LDAP fixture has an unexpected address")\n', 1), ('            raise TypeError("synthetic directory modes must be booleans")\n', 1), ('            raise ValueError("computer assets require dictionary3 nonempty fast mode")\n', 1), ('            raise ValueError("computer export failure requires exclusive nonempty fast computer dictionary3 mode")\n', 1), ('            raise ValueError("computer multichunk export requires exclusive nonempty fast computer dictionary3 mode")\n', 1), ('            raise ValueError("group assets require exclusive dictionary3 nonempty fast mode")\n', 1), ('            raise ValueError("group export failure requires exclusive nonempty fast group dictionary3 mode")\n', 1), ('            raise ValueError("group member pagination requires exclusive nonempty fast dictionary4 mode")\n', 1), ('            raise ValueError("group member refresh requires exclusive nonempty fast dictionary4 and private empty transition")\n', 1), ('            raise ValueError("synthetic dictionary 2 requires directory mode")\n', 1), ('            raise ValueError("synthetic dictionary 3 requires exclusive directory mode")\n', 1), ('            raise ValueError("synthetic dictionary 4 requires exclusive directory mode")\n', 1), ('            raise ValueError("synthetic empty and slow modes require directory mode")\n', 1), ('            raise ValueError("synthetic empty transition requires exactly one dictionary3 asset mode without empty, slow, v2 or B2 control modes")\n', 1), ('            raise ValueError("synthetic export failure requires exclusive nonempty fast dictionary3 mode")\n', 1), ('            raise ValueError("synthetic user assets require nonempty fast directory-v2 mode")\n', 1), ('            raise error\n', 1), ('            self.control_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-control-") if control_enabled else None\n', 1), ('            self.empty_transition_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-empty-transition-") if empty_transition else None\n', 1), ('        if (directory_empty or directory_slow) and not directory_enabled:\n', 1), ('        if any(not isinstance(value, bool) for value in (directory_enabled, directory_empty, directory_slow, directory_v2, user_assets_v2, directory_v3, directory_v4, computer_assets_v3, group_assets_v3, empty_transition, group_members_v4_pagination, group_members_v4_refresh, user_export_failure_v3, group_export_failure_v3, computer_export_failure_v3, computer_export_multichunk_v3)):\n', 1), ('        if computer_assets_v3 and (not directory_enabled or not directory_v3 or directory_empty or directory_slow or user_assets_v2 or group_assets_v3):\n', 1), ('        if computer_export_failure_v3 and (not directory_enabled or not directory_v3 or not computer_assets_v3 or\n', 1), ('        if computer_export_multichunk_v3 and (not directory_enabled or not directory_v3 or not computer_assets_v3 or\n', 1), ('        if directory_v2 and not directory_enabled:\n', 1), ('        if directory_v3 and (not directory_enabled or directory_v2 or user_assets_v2):\n', 1), ('        if directory_v4 and (not directory_enabled or directory_v2 or directory_v3 or user_assets_v2 or computer_assets_v3 or group_assets_v3 or (empty_transition and not group_members_v4_refresh) or control_enabled):\n', 1), ('        if empty_transition and not group_members_v4_refresh and (not directory_enabled or not directory_v3 or\n', 1), ('        if error:\n', 1), ('        if group_assets_v3 and (not directory_enabled or not directory_v3 or directory_empty or directory_slow or user_assets_v2 or computer_assets_v3):\n', 1), ('        if group_export_failure_v3 and (not directory_enabled or not directory_v3 or not group_assets_v3 or\n', 1), ('        if group_members_v4_pagination and (not directory_enabled or not directory_v4 or directory_empty or directory_slow):\n', 1), ('        if group_members_v4_refresh and (not directory_enabled or not directory_v4 or not empty_transition or directory_empty or directory_slow or group_members_v4_pagination):\n', 1), ('        if ip.version != 4 or not ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_unspecified:\n', 1), ('        if self.computer_assets_v3:\n', 1), ('        if self.computer_export_failure_v3:\n', 1), ('        if self.computer_export_multichunk_v3:\n', 1), ('        if self.control_path is not None:\n', 3), ('        if self.directory_empty:\n', 1), ('        if self.directory_enabled:\n', 1), ('        if self.directory_slow:\n', 1), ('        if self.directory_v2:\n', 1), ('        if self.directory_v3:\n', 1), ('        if self.directory_v4:\n', 1), ('        if self.empty_transition_path is not None:\n', 3), ('        if self.group_assets_v3:\n', 1), ('        if self.group_export_failure_v3:\n', 1), ('        if self.group_members_v4_pagination:\n', 1), ('        if self.group_members_v4_refresh:\n', 1), ('        if self.user_assets_v2:\n', 1), ('        if self.user_export_failure_v3:\n', 1), ('        if user_assets_v2 and (not directory_enabled or not directory_v2 or directory_empty or directory_slow):\n', 1), ('        if user_export_failure_v3 and (not directory_enabled or not directory_v3 or\n', 1), ('        return Path(self.control_directory.name) if self.control_directory else None\n', 1), ('        return Path(self.empty_transition_directory.name) if self.empty_transition_directory else None\n', 1)], 'internal/testldapfixture/computer_export_multichunk_v3.go': [('\tif kind == "computer" {\n', 3), ('\tif row < 1 || row > computerExportMultichunkV3ObjectCount {\n', 1), ('\tif row > computerExportMultichunkV3ComputerCount {\n', 1), ('\tif row > computerExportMultichunkV3ComputerCount+2 {\n', 1)], 'internal/testldapfixture/computer_export_multichunk_v3_test.go': [('\t\t\t\t\tif _, err := io.ReadFull(&wire, actual); err != nil || !bytes.Equal(actual, want) {\n', 1), ('\t\t\t\t\tif len(next) != 20 || !bytes.Equal(next[:4], []byte{0, 255, 128, 0}) || seenCookies[string(next)] {\n', 1), ('\t\t\t\tif !bytes.Equal(attribute.Values[0], want) {\n', 2), ('\t\t\t\tif err := session.writePage(context.Background(), &wire, page+4); err != nil {\n', 1), ('\t\t\t\tif page < 3 {\n', 1), ('\t\t\t\tif wire.Len() != 0 || page == 0 && len(cookie) != 20 || page == 1 && len(cookie) != 0 {\n', 1), ('\t\t\t\t} else if len(next) != 0 {\n', 1), ('\t\t\tif _, err := readConfiguration(append(append([]string{}, base...), conflict), fixtureEnvironment); err == nil {\n', 1), ('\t\t\tif attribute.Name != wantNames[i] || len(attribute.Values) == 0 || attribute.Name != "objectClass" && len(attribute.Values) != 1 {\n', 1), ('\t\t\tif attribute.Name == "objectGUID" {\n', 1), ('\t\t\tif attribute.Name == "objectSid" {\n', 1), ('\t\t\tif object.Base.GUID != fmt.Sprintf("%08x-1122-3344-5566-778899aabbcc", page*256+i+1) {\n', 1), ('\t\t\tif public.UserAccountControl == nil || *public.UserAccountControl != 4096 {\n', 1), ('\t\t\tif session.computerExportMultichunkV3 || session.pages() != 2 || session.objects() != 30 {\n', 1), ('\t\tif !bytes.Contains(sheet, []byte(expected.String())) {\n', 1), ('\t\tif !bytes.Equal(wire, computerExportMultichunkV3Entry(4, row)) {\n', 1), ('\t\tif !session.matches(request{body: body, controls: testPagingControl(1000, current)}) {\n', 1), ('\t\tif !utf8.Valid(stored) || !utf8.Valid(encoded) || !utf8.ValidString(public.DN) || len(public.DN) > 4096 {\n', 1), ('\t\tif _, err := readConfiguration(append(append([]string{}, base...), pair...), fixtureEnvironment); err == nil {\n', 1), ('\t\tif _, err := readConfiguration(args, fixtureEnvironment); err == nil {\n', 1), ('\t\tif computerExportMultichunkV3Entry(4, row) != nil {\n', 1), ('\t\tif err != nil {\n', 3), ('\t\tif err != nil || !reflect.DeepEqual(object, restored) {\n', 1), ('\t\tif err != nil || cfg.computerExportMultichunkV3 {\n', 2), ('\t\tif err != nil || closeErr != nil || len(sheet) > 16<<20 || bytes.Count(sheet, []byte("<row ")) != 1021 {\n', 1), ('\t\tif err != nil || len(encoded) == 0 || len(encoded) > 8192 {\n', 1), ('\t\tif err != nil || len(stored) == 0 || len(stored) > 8192 {\n', 1), ('\t\tif err != nil || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {\n', 1), ('\t\tif err := session.writePage(context.Background(), &wire, page+4); err != nil {\n', 2), ('\t\tif found || part.UncompressedSize64 > 16<<20 {\n', 1), ('\t\tif index == len(matrix) {\n', 1), ('\t\tif len(entry.Attributes) != len(wantNames) {\n', 1), ('\t\tif page > 0 && other.matches(request{body: body, controls: testPagingControl(1000, current)}) {\n', 1), ('\t\tif page > 0 && session.matches(request{body: body, controls: testPagingControl(1000, prior)}) {\n', 1), ('\t\tif part.Name != "xl/worksheets/sheet1.xml" {\n', 1), ('\t\tif public.GUID != wantGUID || guids[public.GUID] || public.ObjectSID == nil || *public.ObjectSID != wantSID || sids[*public.ObjectSID] {\n', 1), ('\t\tif public.Mail != nil || public.Description != nil || public.PrimaryGroupID != nil || public.ServicePrincipalName != nil {\n', 1), ('\t\tif public.WhenCreated == nil || *public.WhenCreated != "2026-01-01T00:00:00Z" || string(object.Supplemental.WhenCreatedBytes) != "20260101000000.0Z" {\n', 1), ('\t\tif row <= 1020 {\n', 2), ('\t\tif row > 1020 {\n', 2), ('\t\tif row > 1022 {\n', 1), ('\t\tif session.matches(invalid) {\n', 1), ('\t\tif string(public.Kind) != kind || !slices.Equal(public.Classes, classes) || public.SAMAccountName == nil || *public.SAMAccountName != wantSAM || len(*public.SAMAccountName) > 256 || public.DN != expectedMultichunkV3DN(row) {\n', 1), ('\t\tif test.session.pages() != test.pages || test.session.objects() != test.objects {\n', 1), ('\t\tif wire.Len() != 0 || !bytes.Equal(cookie, session.cookie) || page < 3 && len(cookie) != 20 || page == 3 && len(cookie) != 0 {\n', 1), ('\t\t} else if public.UserAccountControl != nil {\n', 1), ('\tif !found {\n', 1), ('\tif computerExportMultichunkV3ObjectCount != 1024 || computerExportMultichunkV3PageEntries != 256 || computerExportMultichunkV3ComputerCount != 1020 || computerExportMultichunkV3OUCount != 48 {\n', 1), ('\tif counts[directoryassets.Computer] != 1020 || counts[directoryassets.User] != 2 || counts[directoryassets.Group] != 2 || len(guids) != 1024 || len(sids) != 1024 {\n', 1), ('\tif err != nil {\n', 5), ('\tif err != nil || !cfg.computerExportMultichunkV3 {\n', 1), ('\tif err != nil || !json.Valid(raw) || len(raw) >= 16<<20 || worstEnvelopeBytes >= 16<<20 {\n', 1), ('\tif err != nil || reader.Len() != 0 || req.id != id || req.tag != 0x64 || len(req.controls) != 0 {\n', 1), ('\tif err := computerassetxlsx.Write(context.Background(), &workbook, headers, func() ([]string, error) {\n', 1), ('\tif len(matrix) != 1020 || workbook.Len() <= chunkBytes || workbook.Len() > 32<<20 || chunkCount < 2 || chunkCount > 32 {\n', 1), ('\tif len(objects) != 1024 {\n', 1), ('\tif row > 1020 {\n', 3), ('\tif row > 1022 {\n', 2), ('\tif session.matches(request{body: body, controls: testPagingControl(1000, nil)}) {\n', 1), ('\tif session.page != 0 || session.cookie != nil || !session.matches(request{body: body, controls: controls}) {\n', 1), ('\tif session.pages() != 4 || session.objects() != 1024 || maxRequests != 8 || connectionTime != 15*time.Second || directoryRequestPageSize != 1000 {\n', 1)], 'web/e2e/computer-asset-export-v3-multichunk.spec.ts': [('    await expect(downloadButton).toBeEnabled();\n', 1), ('    await expect(downloadButton).toBeVisible({ timeout: 30_000 });\n', 1), ('    await expect(page).toHaveURL(/#computer-assets-v3$/u);\n', 1), ('    await expect(page.getByLabel("未使用的认证器验证码", { exact: true })).toHaveCount(0);\n', 1), ('    await expect(panel).toContainText("全部匹配计算机，不限当前页");\n', 1), ('    await expect(panel.getByLabel("导出列顺序", { exact: true })).toHaveText(columns.join(" → "));\n', 1), ('    await expect(panel.getByRole("progressbar", { name: "计算机导出持久化进度", exact: true })).toHaveAttribute("value", "100");\n', 1), ('    await expect(panel.getByText(fixture.taskUUID, { exact: true })).toBeVisible();\n', 1), ('    await expect(panel.locator(\'input[type="password"]\')).toHaveCount(0);\n', 1), ('    await navigateTo(page, "计算机资产");\n', 1), ('    await observeDirectoryV2Responses(context);\n', 1), ('    await page.getByLabel("创建时间开始（含）", { exact: true }).fill("2026-01-01T00:00:00Z");\n', 1), ('    await page.getByLabel("创建时间排序", { exact: true }).selectOption("asc");\n', 1), ('    await page.getByLabel("创建时间结束（不含）", { exact: true }).fill("2026-01-02T00:00:00Z");\n', 1), ('    await page.getByLabel("计算机资产关键词", { exact: true }).fill("computer-");\n', 1), ('    await page.getByRole("button", { name: "导出匹配计算机 XLSX", exact: true }).click();\n', 1), ('    await page.getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true }).check();\n', 1), ('    await page.locator("summary").filter({ hasText: /^创建时间筛选与排序$/u }).click();\n', 1), ('    await page.screenshot({ path: info.outputPath("ready.png"), fullPage: false });\n', 1), ('    await page.setViewportSize({ width: 1440, height: 900 });\n', 1), ('    await proof(page, fixture.bootstrap.password, fixture.bootstrap.auth);\n', 1), ('    await retrieved.download.saveAs(info.outputPath("download.xlsx"));\n', 1), ('    await status.evaluate((element) => element.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" }));\n', 1), ('    await status.scrollIntoViewIfNeeded();\n', 1), ('    await switchToExportOwner(page, fixture.bootstrap);\n', 1), ('    await writeFile(info.outputPath("evidence.json"), JSON.stringify(evidence, null, 2), { flag: "wx" });\n', 1), ('    const after = await sensitiveBudget(actorId, fixture.actorId);\n', 1), ('    const before = await sensitiveBudget(actorId, fixture.actorId);\n', 1), ('    const cells = sourceCellSummary(await readFile(sourcePath));\n', 1), ('    const database = await readExportEvidence(create.taskID, actorId, source, request, info.outputPath("database-export.xlsx"));\n', 1), ('    const databaseBytes = await readFile(info.outputPath("database-export.xlsx"));\n', 1), ('    const downloaded = await readFile(info.outputPath("download.xlsx"));\n', 1), ('    const filtered = await observeAction(page, actorId, [{ path: "/api/computer-assets/v3", method: "GET", query: exactQuery(filteredQuery) }], () => page.getByRole("button", { name: "应用计算机筛选", exact: true }).click());\n', 1), ('    const fixture = await setupComputerFixture(page, context, {});\n', 1), ('    const locator = await page.evaluate(() => JSON.parse(sessionStorage.getItem("adtr.computer-assets-v3.export-recovery") ?? "null"));\n', 1), ('    const opened = await observeAction(page, actorId, [{ path: "/api/computer-assets/v3", method: "GET", query: exactQuery(initialQuery) }], () => page.getByRole("button", { name: "核对并打开连接详情", exact: true }).click());\n', 1), ('    const profile = await readJSON(context, "/api/auth/me");\n', 1), ('    const provenance = await captureProvenance(info.outputPath("provenance.json"));\n', 1), ('    const receiptRead = await observeAction(page, actorId, [{ path: `${exportPath}/receipt`, method: "GET", query: exactQuery({ domainId: source.domainId, idempotencyKey: locator.idempotencyKey }) }], () => panel.getByRole("button", { name: "核对原导出回执", exact: true }).click());\n', 1), ('    const retrieved = await observeAction(page, actorId, [{ path: `${exportPath}/detail`, method: "GET", query }, { path: `${exportPath}/download`, method: "GET", query, binary: true }], () => downloadButton.click(), true);\n', 1), ('    const screenshot = fileReference("ready.png", await readFile(info.outputPath("ready.png")));\n', 1), ('    const source = await readSourceEvidence(fixture.taskUUID, fixture.actorId, sourcePath);\n', 1), ('    const submitted = await observeAction(page, actorId, [{ path: exportPath, method: "POST", query: exactQuery({}) }], () => panel.getByRole("button", { name: "创建计算机导出", exact: true }).click());\n', 1), ('    expect(after).toEqual({ ...before, owner: 8, exportTasks: 1 });\n', 1), ('    expect(before).toEqual({ schemaVersion: 23, owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0 });\n', 1), ('    expect(detail.artifact).toEqual({ rowCount: 1020, byteCount: downloaded.length, sha256: sha(downloaded), filename });\n', 1), ('    fixture.safe(await page.locator("body").innerText()); fixture.safe(fixture.consoles);\n', 1), ('    fixture.safe(evidence); requireEvidence(writes === 1 && !unsafeRequest);\n', 1), ('    for (const target of [status, identity, rowCount, downloadButton]) { await expect(target).toHaveCount(1); await expect(target).toBeVisible(); await expect(target).toBeInViewport({ ratio: 1 }); }\n', 1), ('    requireEvidence("artifact" in detail && detail.artifact && retrieved.download);\n', 1), ('    requireEvidence(Number.isSafeInteger(actorId) && actorId > 0 && actorId !== fixture.actorId);\n', 1), ('    requireEvidence(actualList.available === true && actualList.observationId === fixture.taskUUID && actualList.dictionaryVersion === 3 && actualList.page.pageIdx === 1 && actualList.page.pageSize === 10 && actualList.page.total === 1020 && actualList.list.length === 10);\n', 1), ('    requireEvidence(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));\n', 1), ('    requireEvidence(databaseBytes.equals(downloaded));\n', 1), ('    requireEvidence(downloadHeaders.every((key) => typeof headers[key] === "string") && Object.keys(headers).length === downloadHeaders.length);\n', 1), ('    requireEvidence(downloaded.length > 1048576 && downloaded.length <= (32 << 20));\n', 1), ('    requireEvidence(headers["content-length"] === String(downloaded.length) && headers["x-adtr-artifact-sha256"] === sha(downloaded) && headers["x-adtr-row-count"] === "1020" && headers["content-disposition"] === `attachment; filename="${filename}"`);\n', 1), ('    requireEvidence(locator?.actorID === actorId && locator.domainId === source.domainId); uuid(locator.idempotencyKey);\n', 1), ('    requireEvidence(opened.values[0].available === true && opened.values[0].observationId === fixture.taskUUID && opened.values[0].dictionaryVersion === 3);\n', 1), ('    requireEvidence(process.env.ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK === "true" && process.env.ADTR_E2E_LDAP_DIRECTORY_COMPUTER_EXPORT_MULTICHUNK_V3 === "true");\n', 1), ('    requireEvidence(retrieved.download.suggestedFilename() === filename);\n', 1), ('    requireEvidence(source.domainId === fixture.domainId && source.sourcePins.accountId === fixture.accountId);\n', 1), ('    requireEvidence(writes === 1 && !unsafeRequest);\n', 2), ('    try { const path = new URL(request.url()).pathname; if (path.startsWith(exportPath) && request.method() !== "GET") { writes++; if (path !== exportPath || request.method() !== "POST") unsafeRequest = true; } }\n', 1), ('  } catch { throw new Error(`Computer multichunk acceptance failed at ${phase}`); }\n', 1)], 'web/e2e/computer-asset-export-v3-multichunk-evidence.ts': [('      await output.writeFile(bytes); hash.update(bytes);\n', 1), ('      await output.writeFile(bytes); hash.update(bytes); bytesWritten += bytes.length;\n', 1), ('      const index = match(request); if (index < 0) return;\n', 1), ("      const row = await query(`SELECT jsonb_build_object('taskID',task_id,'fencingToken',fencing_token::text,'ordinal',chunk_no,'byteCount',octet_length(data),'sha256',sha256,'bodyBase64',replace(encode(data,'base64'),chr(10),'')) FROM adtr.computer_asset_export_chunks WHERE task_id='${taskID}' AND fencing_token=${task.fencingToken} AND chunk_no=${ordinal}`, 2 << 20);\n", 1), ("      const slice = await query(`SELECT jsonb_build_object('offset',${offset},'byteCount',octet_length(substring(body FROM ${offset + 1} FOR ${size})),'bodyBase64',replace(encode(substring(body FROM ${offset + 1} FOR ${size}),'base64'),chr(10),'')) FROM adtr.domain_directory_observations WHERE tenant_id='default' AND task_id='${taskID}' AND sha256='${metadata.sha256}' AND octet_length(body)=${metadata.byteCount}`, 1 << 20);\n", 1), ('      const { stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer });\n', 1), ('      if (!armed) { old.add(request); return; }\n', 1), ('      if (index < 0 || old.has(request)) return;\n', 1), ('      if (old.has(request)) return;\n', 1), ('      if (routes[index].binary) { requireEvidence(metadata["content-type"] === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); values[index] = null; pending[index].resolve(); settle(false); return; }\n', 1), ('      if (selected[index] !== request || responseSeen[index]) return;\n', 1), ('      if (selected[index]) return;\n', 1), ('      requireEvidence(/^application\\/json(?:;\\s*charset=utf-8)?$/iu.test(metadata["content-type"] ?? ""));\n', 1), ('      requireEvidence(Buffer.byteLength(stdout) <= maxBuffer && stdout.trim().length > 0);\n', 1), ('      requireEvidence(bytes.length === expected && sha(bytes) === chunk.sha256);\n', 1), ('      requireEvidence(chunk.taskID === taskID && chunk.fencingToken === task.fencingToken && chunk.ordinal === ordinal && chunk.byteCount === expected && digestPattern.test(chunk.sha256));\n', 1), ('      requireEvidence(fresh.has(request) && selected[index]);\n', 1), ('      requireEvidence(metadata["x-adtr-user-id"] === String(actor) && metadata["cache-control"] === "no-store" && metadata["x-content-type-options"] === "nosniff");\n', 1), ('      requireEvidence(response.status() === 200 && !response.fromServiceWorker());\n', 1), ('      requireEvidence(segments.length < 32);\n', 1), ('      requireEvidence(slice.offset === offset && slice.byteCount === size && bytes.length === size);\n', 1), ('      requireEvidence(url.origin === origin && !url.username && !url.password && !url.hash && route.query(url.searchParams) && request.method() === route.method && request.resourceType() === "fetch" && request.frame() === page.mainFrame() && !request.redirectedFrom() && !request.redirectedTo());\n', 1), ('      void body.then((value) => { if (!live) return; values[index] = value; pending[index].resolve(); settle(false); }, fail);\n', 1), ('    await Promise.allSettled(pending.map((item) => item.promise));\n', 1), ('    await done;\n', 1), ('    await run("python3", ["scripts/computer_asset_export_v3_multichunk_ci.py", "capture", "--repo", root, "--output", output], { cwd: root, encoding: "utf8", timeout: 30_000, maxBuffer: 2 << 20 });\n', 1), ('    const raw = await readFile(output); requireEvidence(raw.length <= (2 << 20));\n', 1), ('    const root = stdout.trim(); requireEvidence(root.startsWith("/"));\n', 1), ('    const { stdout } = await run("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8", timeout: 5000, maxBuffer: 1 << 20 });\n', 1), ('    if (!live || !armed) return;\n', 1), ('    if (!live) return;\n', 1), ('    if (base.kind !== "computer") continue;\n', 1), ('    requireEvidence(created === "20260101000000.0Z");\n', 1), ('    requireEvidence(maxBuffer === (1 << 20) || maxBuffer === (2 << 20));\n', 1), ('    requireEvidence(sid.length === 12 && sid[0] === 1 && sid[1] === 1);\n', 1), ('    requireEvidence(typeof base.distinguishedName === "string" && base.distinguishedName.includes("computer-") && /^[\\x20-\\x7e]+$/u.test(base.distinguishedName));\n', 1), ('    requireEvidence(typeof base.samAccountName === "string" && /^computer-capacity-[0-9]{4}\\$$/u.test(base.samAccountName));\n', 1), ('    void snapshot.then((requests) => { if (!live) return; for (const request of requests) old.add(request); armed = true; try { const result = action(); void Promise.resolve(result).then(() => { if (!live) return; actionDone = true; settle(false); }, fail); } catch { fail(); } }, fail);\n', 1), ('    } catch { throw new Error("Multichunk read-only query failed"); }\n', 1), ('  catch { throw new Error("Multichunk export evidence failed"); }\n', 1), ('  catch { throw new Error("Multichunk source evidence failed"); }\n', 1), ('  const metadata = await query(exportSQL(taskID), 1 << 20);\n', 1), ('  const metadata = await query(sourceSQL(taskID), 1 << 20);\n', 1), ('  const onDownload = (download: Download) => { if (!live || !armed) return; if (downloaded) { fail(); return; } downloaded = download; downloadDone = true; settle(false); };\n', 1), ('  const onFrame = (frame: unknown) => { if (armed && frame === page.mainFrame()) fail(); };\n', 1), ('  const onRequestFailed = (request: Request) => { if (selected.includes(request)) fail(); };\n', 1), ('  const output = await open(path, "wx");\n', 2), ('  const settle = (bad: boolean) => { if (!live) return; failed ||= bad; if (bad || (actionDone && downloadDone && responseSeen.every(Boolean) && pending.every((_, i) => values[i] !== undefined))) { live = false; completed = !bad; for (const item of pending) item.resolve(); finish(); } };\n', 1), ('  const text = raw.toString("utf8"); requireEvidence(Buffer.from(text).equals(raw));\n', 1), ('  for (const action of ["submitted", "leased", "started", "progress", "finished"]) { index = actions.indexOf(action, index + 1); requireEvidence(index >= 0); }\n', 1), ('  for (const event of events) { requireEvidence(typeof event.resultVersion === "string" && /^(?:0|[1-9][0-9]{0,18})$/u.test(event.resultVersion) && BigInt(event.resultVersion) <= 9223372036854775807n && BigInt(event.resultVersion) >= previous); previous = BigInt(event.resultVersion); }\n', 1), ('  if (!ok) throw new Error("Multichunk evidence rejected");\n', 1), ('  if (failed || !completed) throw new Error("Multichunk original action failed");\n', 1), ('  if (mode === "create") { requireEvidence(parsed.replayed === false); return { ...projected, replayed: parsed.replayed }; }\n', 1), ('  if (mode === "detail") { requireEvidence(parsed.downloadReady === true && parsed.downloadStatus === "ready" && parsed.artifact); return { ...projected, downloadReady: parsed.downloadReady, downloadStatus: parsed.downloadStatus, artifact: parsed.artifact }; }\n', 1), ('  integer(actor, 1, Number.MAX_SAFE_INTEGER); requireEvidence(routes.length > 0 && routes.length <= 2 && new Set(routes.map((route) => route.path)).size === routes.length);\n', 1), ('  requireEvidence(/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container));\n', 1), ('  requireEvidence(Array.isArray(events) && events.length > 0 && events.length <= 1000);\n', 1), ('  requireEvidence(bytes.length > 0 && bytes.length <= maximum && bytes.toString("base64") === value);\n', 1), ('  requireEvidence(bytesWritten === manifest.byteCount && hash.digest("hex") === manifest.sha256);\n', 1), ('  requireEvidence(events[events.length - 1].state === "succeeded");\n', 1), ('  requireEvidence(hash.digest("hex") === metadata.sha256);\n', 1), ('  requireEvidence(manifest.taskID === taskID && manifest.fencingToken === task.fencingToken && task.result.artifactToken === task.fencingToken && manifest.rowCount === 1020 && digestPattern.test(manifest.sha256) && manifest.chunkCount === Math.ceil(manifest.byteCount / 1048576));\n', 1), ('  requireEvidence(producer.taskID === taskID && producer.tenantId === value.tenantId && producer.domainId === value.domainId && producer.actorId === value.actorId && producer.kind === "domain.directory_read.v3" && producer.state === "succeeded" && producer.payloadVersion === 1 && producer.attempt === 1 && producer.maxAttempts === 1);\n', 1), ('  requireEvidence(raw.length > 0 && raw.length <= (16 << 20));\n', 1), ('  requireEvidence(rows.length === 1020 && new Set(rows.map((row) => row[0])).size === rows.length);\n', 1), ('  requireEvidence(snapshot.taskID === taskID && snapshot.tenantId === task.tenantId && snapshot.actorId === task.actorId && snapshot.authorizationVersion === task.authorizationVersion && snapshot.payloadHash === task.payloadHash && snapshot.observationId === source.taskUUID && snapshot.sourceSHA256 === source.file.sha256 && snapshot.rowCount === 1020);\n', 1), ('  requireEvidence(task.payloadHash === sha("1::" + stable(request)));\n', 1), ('  requireEvidence(task.result.byteCount === manifest.byteCount && task.result.sha256 === manifest.sha256 && task.result.rowCount === manifest.rowCount && Array.isArray(chunks) && chunks.length === manifest.chunkCount);\n', 1), ('  requireEvidence(task.state === "succeeded" && task.progress === 100 && task.attempt === 1 && task.maxAttempts === 3);\n', 1), ('  requireEvidence(task.taskID === taskID && task.tenantId === "default" && task.actorId === String(actor) && task.domainId === source.domainId && task.kind === "computer_assets.v3.export" && task.payloadVersion === 1 && task.state === "succeeded" && task.progress === 100 && task.attempt === 1 && task.maxAttempts === 3);\n', 1), ('  requireEvidence(typeof value === "string" && value.length <= 4 * Math.ceil(maximum / 3));\n', 1), ('  requireEvidence(typeof value.sourcePins.policyRevision === "string" && digestPattern.test(value.sourcePins.policyRevision) && value.sourcePins.grantRoleId === "platform_admin");\n', 1), ('  requireEvidence(value.dictionaryVersion === 3 && Array.isArray(value.objects) && value.objects.length === 1024);\n', 1), ('  requireEvidence(value.observationOpener.fencingToken === producer.fencingToken && value.observationOpener.attempt === producer.attempt && typeof value.observationOpener.owner === "string" && /^[0-9a-f]{32}-[0-3]$/u.test(value.observationOpener.owner));\n', 1), ('  requireEvidence(value.purpose === "domain.directory_read.v3" && value.useState === "quiesced" && value.reason === "executor_returned" && value.dependencies === 0 && value.selection.domainId === value.domainId);\n', 1), ('  requireEvidence(value.taskUUID === taskID && value.actorId === String(actor) && value.tenantId === "default" && value.dictionaryVersion === 3 && value.count === 1024 && digestPattern.test(value.sha256));\n', 1), ('  same(metadata, await query(exportSQL(taskID), 1 << 20));\n', 1), ('  same(metadata, await query(sourceSQL(taskID), 1 << 20));\n', 1), ('  try { return await readExportEvidenceInternal(...args); }\n', 1), ('  try { return await readSourceEvidenceInternal(...args); }\n', 1), ('  } catch { throw new Error("Multichunk provenance capture failed"); }\n', 1), ('  } finally { await output.close(); }\n', 2), ('export function requireEvidence(ok: unknown): asserts ok {\n', 1), ('export function uuid(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && uuidPattern.test(value)); }\n', 1), ('function decimal(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && decimalPattern.test(value) && BigInt(value) <= 9223372036854775807n); }\n', 1), ('function exact(value: unknown, keys: string[]): asserts value is ObjectValue { record(value); requireEvidence(Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key))); }\n', 1), ('function integer(value: unknown, min: number, max: number): asserts value is number { requireEvidence(Number.isSafeInteger(value) && (value as number) >= min && (value as number) <= max); }\n', 1), ('function parse(raw: string): any { try { return JSON.parse(raw); } catch { throw new Error("Multichunk JSON rejected"); } }\n', 1), ('function record(value: unknown): asserts value is ObjectValue { requireEvidence(value !== null && typeof value === "object" && !Array.isArray(value)); }\n', 1), ('function resourceID(value: unknown): asserts value is string { requireEvidence(typeof value === "string" && /^[A-Za-z0-9_-]{24}$/u.test(value)); }\n', 1), ('function same(a: unknown, b: unknown) { requireEvidence(stable(a) === stable(b)); }\n', 1)], 'web/src/ComputerExportMultichunkEvidence.test.ts': [('    actionDone.resolve(); expect((await result).values[0]).toBe(value); expect(action).toHaveBeenCalledTimes(1); noListeners(page);\n', 1), ('    await Promise.resolve(); await Promise.resolve(); expect(returned).toBe(false);\n', 1), ('    await expect(observeAction(page.asPage(), 7, [route], action)).rejects.toThrow(/^Multichunk original action failed$/u);\n', 1), ('    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("request", first); page.emit("response", response(first, undefined, changes)); page.emit("request", second); page.emit("response", response(second, secondJSON)); })).rejects.toThrow("Multichunk original action failed");\n', 1), ('    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("request", req); page.emit("response", response(req)); page.emit("download", {}); page.emit("download", {}); }, true)).rejects.toThrow("Multichunk original action failed");\n', 1), ('    await expect(observeAction(page.asPage(), 7, [route], async () => { page.emit("response", response(request(page))); })).rejects.toThrow("Multichunk original action failed"); noListeners(page);\n', 1), ('    await expect(readExportEvidence(task, 7, { domainId: domain, taskUUID: task, file: { sha256: "b".repeat(64) } }, requestPayload, await output(), query)).rejects.toThrow(/^Multichunk export evidence failed$/u);\n', 1), ('    await expect(readSourceEvidence(task, 2, await output(), query)).rejects.toThrow(/^Multichunk source evidence failed$/u);\n', 1), ('    await expect(result).rejects.toThrow("Multichunk original action failed"); body.reject(new Error(canary)); await Promise.resolve(); noListeners(page);\n', 1), ('    await expect(result).rejects.toThrow(/^Multichunk original action failed$/u); expect(firstJSON).toHaveBeenCalledTimes(1); expect(secondJSON).not.toHaveBeenCalled(); noListeners(page);\n', 1), ('    await vi.advanceTimersByTimeAsync(30_000); await rejection; noListeners(page); expect(vi.getTimerCount()).toBe(0);\n', 1), ('    blocker.resolve(kind === "snapshot" ? [] : { marker: canary }); await Promise.resolve();\n', 1), ('    const action = vi.fn(() => { page.emit("request", req); page.emit("response", response(req, json)); expect(json).toHaveBeenCalledTimes(1); return actionDone.promise; });\n', 1), ('    const out = await observeAction(page.asPage(), 7, [route], async () => { page.emit("request", old); page.emit("response", response(old, oldJSON)); page.emit("request", fresh); page.emit("response", response(fresh)); });\n', 1), ('    const path = await output(), value = await readExportEvidence(task, 7, { domainId: domain, taskUUID: task, file: { sha256: "b".repeat(64) } }, requestPayload, path, query);\n', 1), ('    const path = await output(), value = await readSourceEvidence(task, 2, path, query);\n', 1), ('    const query: EvidenceQuery = async (sql) => { if (!sql.includes("substring(body")) { metadataReads++; return { ...metadata, ...(kind === "metadata_changed" && metadataReads === 2 ? { count: 1023 } : {}) }; } return { offset: kind === "wrong_offset" ? 1 : 0, byteCount: kind === "wrong_size" ? 19 : 20, bodyBase64: kind === "bad_base64" ? canary : bytes.toString("base64") }; };\n', 1), ('    const query: EvidenceQuery = async (sql) => { if (kind === "raw_failure") throw new Error(canary); const match = sql.match(/AND chunk_no=(\\d+)$/u); if (!match) { metadataReads++; return { ...metadata, ...(kind === "metadata_changed" && metadataReads === 2 ? { extra: canary } : {}) }; } const ordinal = Number(match[1]); return { ...metadata.chunks[ordinal], bodyBase64: bytes.subarray(ordinal * 1048576, (ordinal + 1) * 1048576).toString("base64") }; };\n', 1), ('    const query: EvidenceQuery = async (sql, limit) => { calls.push([sql, limit]); const match = sql.match(/AND chunk_no=(\\d+)$/u); if (!match) return structuredClone(metadata); const ordinal = Number(match[1]); return { ...metadata.chunks[ordinal], bodyBase64: bytes.subarray(ordinal * 1048576, (ordinal + 1) * 1048576).toString("base64") }; };\n', 1), ('    const query: EvidenceQuery = async (sql, limit) => { calls.push([sql, limit]); const match = sql.match(/substring\\(body FROM (\\d+) FOR (\\d+)\\)/u); if (!match) return structuredClone(metadata); const offset = Number(match[1]) - 1, size = Number(match[2]); return { offset, byteCount: size, bodyBase64: bytes.subarray(offset, offset + size).toString("base64") }; };\n', 1), ('    const rejection = expect(result).rejects.toThrow("Multichunk original action failed");\n', 1), ('    const result = await observeAction(page.asPage(), 7, [route, { ...route, path: "/api/computer-assets/v3/exports/download", binary: true }], async () => { page.emit("request", detail); page.emit("request", binary); page.emit("response", response(binary, binaryJSON, { headers: () => ({ "x-adtr-user-id": "7", "cache-control": "no-store", "x-content-type-options": "nosniff", "content-type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }) })); page.emit("download", download); page.emit("response", response(detail)); }, true);\n', 1), ('    const result = observeAction(page.asPage(), 7, [route], () => { page.emit("request", req); page.emit("response", response(req)); if (kind === "synchronous") throw error; return Promise.reject(error); });\n', 1), ('    expect(() => sourceCellSummary(Buffer.from(JSON.stringify({ dictionaryVersion: 3, objects })))).toThrow("Multichunk evidence rejected");\n', 1), ('    expect(action).not.toHaveBeenCalled(); noListeners(page);\n', 1), ('    expect(action).toHaveBeenCalledTimes(kind === "snapshot" ? 0 : 1);\n', 1), ('    expect(await readFile(path)).toEqual(bytes); expect(value.file.sha256).toBe(sha(bytes)); expect(calls.map((call) => call[1])).toEqual([1 << 20, 2 << 20, 2 << 20, 1 << 20]); expect(calls[1][0]).toContain("fencing_token=1 AND chunk_no=0"); expect(calls[0][0]).toBe(calls[3][0]);\n', 1), ('    expect(await readFile(path)).toEqual(bytes); expect(value.segments.map((part: { byteCount: number }) => part.byteCount)).toEqual([524288, 12]); expect(value.file.sha256).toBe(sha(bytes));\n', 1), ('    expect(calls).toHaveLength(4); expect(calls.every((call) => call[1] === (1 << 20))).toBe(true); expect(calls[0][0]).toBe(calls[3][0]); expect("bodyBase64" in value).toBe(false);\n', 1), ('    expect(out.values[0]).toEqual({ marker: "selected" }); expect(oldJSON).not.toHaveBeenCalled(); noListeners(page);\n', 1), ('    expect(result.download).toBe(download); expect(binaryJSON).not.toHaveBeenCalled(); noListeners(page);\n', 1), ('    expect(secondJSON).not.toHaveBeenCalled(); noListeners(page);\n', 1), ('    expect(value).toEqual({ rowCount: 1020, columnCount: 5, firstRowSHA256: sha(JSON.stringify(rows[0])), lastRowSHA256: sha(JSON.stringify(rows[1019])), matrixSHA256: sha(JSON.stringify([["GUID", "SAM", "SID", "DN", "whenCreated"], ...rows])) });\n', 1), ('    if (kind === "account_id") metadata.sourcePins.accountId = "short";\n', 1), ('    if (kind === "actor") headers["x-adtr-user-id"] = "8";\n', 1), ('    if (kind === "cache") headers["cache-control"] = "public";\n', 1), ('    if (kind === "digest") metadata.chunks[0].sha256 = "d".repeat(64);\n', 1), ('    if (kind === "domain_id") metadata.domainId = task;\n', 1), ('    if (kind === "duplicate") metadata.chunks.push(metadata.chunks[0]);\n', 1), ('    if (kind === "epoch") metadata.snapshot.authorizationVersion = "2";\n', 1), ('    if (kind === "event_order") metadata.events.reverse();\n', 1), ('    if (kind === "frame") requestChanges.frame = () => ({});\n', 1), ('    if (kind === "method") requestChanges.method = () => "POST";\n', 1), ('    if (kind === "mime") headers["content-type"] = "text/html";\n', 1), ('    if (kind === "missing") metadata.chunks.pop();\n', 1), ('    if (kind === "nosniff") headers["x-content-type-options"] = "";\n', 1), ('    if (kind === "opener_mismatch") metadata.useOpener.fencingToken = "2";\n', 1), ('    if (kind === "ordinal") metadata.chunks[0].ordinal = 1;\n', 1), ('    if (kind === "origin") requestChanges.url = () => "http://localhost:8080" + route.path + "?taskID=" + task;\n', 1), ('    if (kind === "oversize") metadata.byteCount = (16 << 20) + 1;\n', 1), ('    if (kind === "payload") metadata.task.payload = { ...requestPayload, search: "other" };\n', 1), ('    if (kind === "payload_hash") metadata.task.payloadHash = metadata.snapshot.payloadHash = "c".repeat(64);\n', 1), ('    if (kind === "query") requestChanges.url = () => "http://127.0.0.1:8080" + route.path + "?taskID=wrong";\n', 1), ('    if (kind === "redirect") requestChanges.redirectedFrom = () => ({});\n', 1), ('    if (kind === "resource") requestChanges.resourceType = () => "document";\n', 1), ('    if (kind === "snapshot") page.requests = () => blocker.promise;\n', 1), ('    if (kind === "tail") metadata.chunks[1].byteCount++;\n', 1), ('    if (kind === "token") metadata.chunks[0].fencingToken = "2";\n', 1), ('    if (kind === "wrong_actor") metadata.actorId = "3";\n', 1), ('    if (kind === "wrong_digest") metadata.sha256 = "d".repeat(64);\n', 1), ('    page.on = ((event: string, listener: (...args: any[]) => void) => { if (event === "response") throw new Error(canary); return on(event, listener); }) as typeof page.on;\n', 1), ('    try { await result; throw new Error("unexpected success"); } catch (caught) { expect(String(caught)).toBe("Error: Multichunk original action failed"); expect((caught as Error).cause).toBeUndefined(); }\n', 1), ('  it("does not interpret arbitrary source JSON as a row summary", () => { expect(() => sourceCellSummary(Buffer.from(\'{"objects":[],"dictionaryVersion":3}\'))).toThrow("Multichunk evidence rejected"); expect(() => sourceCellSummary(Buffer.from([0xff]))).toThrow("Multichunk evidence rejected"); });\n', 1), ('  it("rejects invalid identity before invoking a query", async () => { const query = vi.fn(); await expect(readSourceEvidence("bad\'" + canary, 2, await output(), query)).rejects.toThrow("Multichunk source evidence failed"); expect(query).not.toHaveBeenCalled(); expect(() => isolatedQuery("external-database")).toThrow("Multichunk evidence rejected"); });\n', 1), ('afterEach(async () => { vi.useRealTimers(); for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true }); });\n', 1), ('async function output() { const directory = await mkdtemp(join(tmpdir(), "adtr-multichunk-unit-")); directories.push(directory); return join(directory, "captured.bin"); }\n', 1), ('function noListeners(page: FakePage) { for (const event of listenerNames) expect(page.listenerCount(event)).toBe(0); }\n', 1)]}

if __name__ == "__main__":
    unittest.main()
