"""Synthetic validator specimens only; never real-browser acceptance."""
import copy
import hashlib
import struct
import unittest
import zlib
from pathlib import Path
from unittest.mock import patch
import validate_group_members_v4_evidence as v
from test_directory_v4_evidence_contract import evidence as backend_evidence


def specimen():
    backend = backend_evidence()
    # Match the real v4 fixture group positions; the reused specimen keeps all
    # rows group-shaped solely for backend validator mutation tests.
    for i, obj in enumerate(backend['objects']):
        obj['kind'] = 'group' if 12 <= i < 24 else 'user' if i < 12 else 'computer'
    import json, base64
    body = json.loads(base64.b64decode(backend['bodyBase64']))
    for stored, obj in zip(body['objects'], backend['objects']):
        stored['base']['kind'] = obj['kind']
    raw = json.dumps(body).encode()
    backend['bodyBase64'] = base64.b64encode(raw).decode()
    backend['sha256'] = hashlib.sha256(raw).hexdigest()
    provenance = {**{k: backend['provenance'][k] for k in ['checkoutCommit', 'checkoutTree', 'runId', 'runAttempt']}, 'sourceDigests': {p: 'e' * 64 for p in v.SOURCES}}
    q = {'domainId': 'synthetic-domain', 'kind': 'group', 'pageIdx': '1', 'pageSize': '25'}
    page = {'dictionaryVersion': 4, 'available': True, 'observationId': backend['taskUUID'], 'source': backend['source'], 'list': backend['objects'][12:24], 'page': {'pageIdx': 1, 'pageSize': 25, 'total': 12, 'totalPage': 1}}
    ui = {'actorId': 2, 'authorization': 'platform_admin_after_v4_use_revoke', 'writes': [], 'discovery': {'query': q, 'value': page}, 'reread': {'query': {**q, 'observationId': backend['taskUUID']}, 'value': page}, 'cases': []}
    screenshots = {}
    for width, height in [(1440, 900), (390, 844)]:
        for index in [12, 13, 14]:
            obj = backend['objects'][index]
            filename = f'group-members-v4-{width}-{index}.png'
            # Generated unit-test PNG, never an acceptance artifact.
            def chunk(kind, payload):
                return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload))
            png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress((b'\0' + b'\xff' * width * 3) * height)) + chunk(b'IEND', b'')
            screenshots[filename] = png
            ui['cases'].append({'viewport': {'width': width, 'height': height}, 'reply': {'query': {'domainId': q['domainId'], 'expectedRevision': '1', 'expectedCredentialRevision': '2', 'observationId': backend['taskUUID'], 'objectGUID': obj['objectGUID']}, 'value': {'dictionaryVersion': 4, 'selection': {'domainId': q['domainId'], 'revision': '1', 'credentialRevision': '2'}, 'observationId': backend['taskUUID'], 'source': backend['source'], 'object': obj}}, 'rendered': [v.display(x) for x in obj['rawMember'] or []], 'screenshot': {'filename': filename, 'sha256': hashlib.sha256(png).hexdigest()}})
    data = {'format': 1, 'backend': backend, 'uiProvenance': provenance, 'noObservation': {'query': q, 'value': {'dictionaryVersion': 4, 'available': False, 'list': [], 'page': {'pageIdx': 1, 'pageSize': 25, 'total': 0, 'totalPage': 0}}}, 'ui': ui}
    return data, screenshots


class GroupMembershipEvidence(unittest.TestCase):
    def test_valid_contract_specimen(self):
        data, shots = specimen()
        v.validate(data, data['backend']['provenance'], data['uiProvenance'], shots.__getitem__)

    def test_rejects_mutations(self):
        data, shots = specimen()
        mutations = [
            lambda d: d.update(format=True),
            lambda d: d['uiProvenance'].update(checkoutCommit='f' * 40),
            lambda d: d['uiProvenance'].update(checkoutTree='f' * 40),
            lambda d: d['uiProvenance'].update(runAttempt='3'),
            lambda d: d['uiProvenance']['sourceDigests'].pop('web/src/App.tsx'),
            lambda d: d['ui']['cases'].pop(),
            lambda d: d['ui']['cases'][0]['viewport'].update(width=390),
            lambda d: d['ui']['cases'][0]['reply']['query'].update(observationId='newer'),
            lambda d: d['ui']['cases'][0]['reply']['query'].update(objectGUID='other'),
            lambda d: d['ui']['cases'][0]['reply']['query'].update(expectedRevision='3'),
            lambda d: d['ui']['cases'][2]['rendered'].reverse(),
            lambda d: d['ui']['cases'][0]['screenshot'].update(filename='../outside.png'),
            lambda d: d['ui']['cases'][0]['screenshot'].update(sha256='0' * 64),
            lambda d: d['ui'].update(authorization='readonly_role'),
            lambda d: d['ui'].update(writes=['POST /api/directory/v4/sync']),
            lambda d: d['noObservation']['value'].update(available=True),
            lambda d: d['ui']['reread']['query'].pop('observationId'),
            lambda d: d['backend'].update(sha256='0' * 64),
        ]
        for i, mutate in enumerate(mutations):
            altered = copy.deepcopy(data)
            mutate(altered)
            with self.subTest(i=i), self.assertRaises((ValueError, KeyError)):
                v.validate(altered, data['backend']['provenance'], data['uiProvenance'], shots.__getitem__)

    def test_png_bytes_and_dimensions_bound(self):
        data, shots = specimen()
        for raw in [b'', b'not png' * 10, next(iter(shots.values()))[:16] + struct.pack('>II', 1, 1)]:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                v.validate(data, data['backend']['provenance'], data['uiProvenance'], lambda _: raw)

    def test_rehashed_truncated_and_corrupt_png_rejected(self):
        data, shots = specimen()
        name = data['ui']['cases'][0]['screenshot']['filename']
        original = shots[name]
        for raw in [original[:24], original[:-12], original[:40] + bytes([original[40] ^ 1]) + original[41:]]:
            altered = copy.deepcopy(data)
            altered['ui']['cases'][0]['screenshot']['sha256'] = hashlib.sha256(raw).hexdigest()
            with self.subTest(size=len(raw)), self.assertRaises(ValueError):
                v.validate(altered, data['backend']['provenance'], data['uiProvenance'], lambda filename: raw if filename == name else shots[filename])

    def test_dirty_or_untracked_runtime_sources_rejected(self):
        def dirty(_root, *args):
            return 'dirty transitive source' if args[:2] == ('diff', 'HEAD') else ''
        def untracked(_root, *args):
            return 'web/src/extra.ts' if args[:2] == ('ls-files', '--others') else ''
        for git in [dirty, untracked]:
            with patch.object(v.backend, 'git', side_effect=git), self.assertRaises(ValueError):
                v.expected_ui(Path('/unused'), {})


if __name__ == '__main__':
    unittest.main()
