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
    reader_ui = copy.deepcopy(ui)
    reader_ui.update(actorId=3, authorization='custom_readonly_role')
    for case in reader_ui['cases']:
        old = case['screenshot']['filename']
        name = old.replace('group-members-v4-', 'group-members-v4-reader-')
        case['screenshot']['filename'] = name
        screenshots[name] = screenshots[old]
    snapshot = {'actorId': 3, 'roleId': 'r' * 24, 'baseRole': 'viewer',
                'readable': ['directory_assets', 'domains'], 'writeable': [],
                'domainIds': [q['domainId']], 'useGrants': 0,
                'taskCount': 2, 'useCount': 1, 'observationCount': 1,
                'observationSha256': backend['sha256'], 'mfaLastStep': 123}
    permissions = [{'mark': mark, 'auth': {'readable': mark in v.READABLE, 'writeable': False}} for mark in sorted(v.PERMISSIONS)]
    reader = {'actorId': 3, 'producerActorId': 2, 'roleId': 'r' * 24,
              'permissions': permissions,
              'menu': [copy.deepcopy(p) for p in permissions if p['auth']['readable']],
              'checks': [*({'method': 'GET', 'path': path, 'allowed': True} for path in v.READ_PATHS),
                         *({'method': 'POST', 'path': path, 'allowed': False} for path in v.WRITE_PATHS)],
              'provenance': copy.deepcopy(provenance),
              'before': copy.deepcopy(snapshot), 'after': copy.deepcopy(snapshot),
              'denied': [{'method': 'POST', 'path': path, 'status': 403, 'value': {'error': 'forbidden'}} for path in v.WRITE_PATHS]}
    data.update(readerUi=reader_ui, reader=reader)
    return data, screenshots


class GroupMembershipEvidence(unittest.TestCase):
    def test_exact_computer_permission_catalog_rejects_duplicates_unknown_and_false_coercion(self):
        entries = [{'mark': mark, 'auth': {'readable': mark in v.READABLE, 'writeable': False}} for mark in sorted(v.PERMISSIONS)]
        v.validate_permissions(entries, v.PERMISSIONS, 'reader permissions')
        index = next(i for i, entry in enumerate(entries) if entry['mark'] == 'computer_asset_exports')
        for replacement in [entries[0], {'mark': 'unknown_asset_exports', 'auth': {'readable': False, 'writeable': False}}]:
            altered = copy.deepcopy(entries)
            altered[index] = copy.deepcopy(replacement)
            with self.assertRaises(ValueError):
                v.validate_permissions(altered, v.PERMISSIONS, 'reader permissions')
        for field in ('readable', 'writeable'):
            for value in (0, None, 'false'):
                altered = copy.deepcopy(entries)
                altered[index]['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    v.validate_permissions(altered, v.PERMISSIONS, 'reader permissions')

    def test_user_export_mark_is_explicitly_denied(self):
        data, shots = specimen()
        permission = next(p for p in data['reader']['permissions'] if p['mark'] == 'user_asset_exports')
        self.assertEqual(permission['auth'], {'readable': False, 'writeable': False})
        self.assertEqual(len(data['reader']['permissions']), 16)
        # Registration expands the exact catalog, never this reader's authority.
        for field in ('readable', 'writeable'):
            for value in (True, 1):
                altered = copy.deepcopy(data)
                next(p for p in altered['reader']['permissions'] if p['mark'] == 'user_asset_exports')['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)
        altered = copy.deepcopy(data)
        altered['reader']['permissions'] = [p for p in altered['reader']['permissions'] if p['mark'] != 'user_asset_exports']
        with self.assertRaises(ValueError):
            v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)

    def test_group_export_mark_is_explicitly_denied(self):
        data, shots = specimen()
        permission = next(p for p in data['reader']['permissions'] if p['mark'] == 'group_asset_exports')
        self.assertEqual(permission['auth'], {'readable': False, 'writeable': False})
        self.assertEqual(len(data['reader']['permissions']), 16)
        # Registration expands the exact catalog, never this reader's authority.
        for field in ('readable', 'writeable'):
            for value in (True, 1):
                altered = copy.deepcopy(data)
                next(p for p in altered['reader']['permissions'] if p['mark'] == 'group_asset_exports')['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)
        altered = copy.deepcopy(data)
        altered['reader']['permissions'] = [p for p in altered['reader']['permissions'] if p['mark'] != 'group_asset_exports']
        with self.assertRaises(ValueError):
            v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)

    def test_computer_export_mark_is_explicitly_denied(self):
        data, shots = specimen()
        permission = next(p for p in data['reader']['permissions'] if p['mark'] == 'computer_asset_exports')
        self.assertEqual(permission['auth'], {'readable': False, 'writeable': False})
        self.assertEqual(len(data['reader']['permissions']), 16)
        # Registration expands the exact catalog, never this reader's authority.
        for field in ('readable', 'writeable'):
            for value in (True, 1):
                altered = copy.deepcopy(data)
                next(p for p in altered['reader']['permissions'] if p['mark'] == 'computer_asset_exports')['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)
        altered = copy.deepcopy(data)
        altered['reader']['permissions'] = [p for p in altered['reader']['permissions'] if p['mark'] != 'computer_asset_exports']
        with self.assertRaises(ValueError):
            v.validate(altered, altered['backend']['provenance'], altered['uiProvenance'], shots.__getitem__)

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

    def test_reader_fail_closed_mutations(self):
        data, shots = specimen()
        mutations = [
            ('missing reader', lambda d: d.pop('reader')),
            ('missing reader UI', lambda d: d.pop('readerUi')),
            ('missing reader provenance', lambda d: d['reader'].pop('provenance')),
            ('reader run mismatch', lambda d: d['reader']['provenance'].update(runId='other')),
            ('reader source mismatch', lambda d: d['reader']['provenance']['sourceDigests'].pop('internal/auth/access_types.go')),
            ('reader tree mismatch', lambda d: d['reader']['provenance'].update(checkoutTree='f' * 40)),
            ('same producer', lambda d: d['reader'].update(producerActorId=3)),
            ('wrong producer', lambda d: d['reader'].update(producerActorId=4)),
            ('admin identity', lambda d: d['reader'].update(actorId=2)),
            ('admin UI label', lambda d: d['readerUi'].update(authorization='platform_admin_after_v4_use_revoke')),
            ('admin role', lambda d: d['reader'].update(roleId='platform_admin')),
            ('builtin role', lambda d: d['reader'].update(roleId='viewer')),
            ('missing permission', lambda d: d['reader']['permissions'].pop()),
            ('duplicate permission', lambda d: d['reader']['permissions'].append(d['reader']['permissions'][0])),
            ('extra read', lambda d: d['reader']['permissions'][0]['auth'].update(readable=True)),
            ('extra write', lambda d: d['reader']['permissions'][0]['auth'].update(writeable=True)),
            ('nonboolean permission', lambda d: d['reader']['permissions'][0]['auth'].update(writeable=0)),
            ('missing menu', lambda d: d['reader']['menu'].pop()),
            ('extra menu', lambda d: d['reader']['menu'].append(d['reader']['permissions'][0])),
            ('missing check', lambda d: d['reader']['checks'].pop()),
            ('write allowed', lambda d: d['reader']['checks'][4].update(allowed=True)),
            ('nonboolean check', lambda d: d['reader']['checks'][0].update(allowed=1)),
            ('wrong check route', lambda d: d['reader']['checks'][0].update(path='/api/domain-selection/options')),
            ('missing denial', lambda d: d['reader']['denied'].pop()),
            ('successful probe', lambda d: d['reader']['denied'][0].update(status=200)),
            ('bad denial body', lambda d: d['reader']['denied'][0].update(value={'error': 'unauthorized'})),
            ('browser write', lambda d: d['readerUi']['writes'].append('POST /api/directory/v4/sync')),
            ('missing case', lambda d: d['readerUi']['cases'].pop()),
            ('wrong observation', lambda d: d['readerUi']['discovery']['value'].update(observationId='other')),
            ('missing pinned observation', lambda d: d['readerUi']['reread']['query'].pop('observationId')),
            ('wrong detail observation', lambda d: d['readerUi']['cases'][0]['reply']['query'].update(observationId='other')),
            ('wrong detail facts', lambda d: d['readerUi']['cases'][1].update(rendered=['invented'])),
            ('admin screenshot', lambda d: d['readerUi']['cases'][0]['screenshot'].update(filename='group-members-v4-1440-12.png')),
            ('traversal screenshot', lambda d: d['readerUi']['cases'][0]['screenshot'].update(filename='../reader.png')),
            ('wrong screenshot hash', lambda d: d['readerUi']['cases'][0]['screenshot'].update(sha256='0' * 64)),
        ]
        for phase in ['before', 'after']:
            for key, value in [('actorId', 2), ('roleId', 'platform_admin'), ('baseRole', 'platform_admin'),
                               ('readable', ['domains']), ('writeable', ['domains']), ('domainIds', []),
                               ('domainIds', ['synthetic-domain', 'extra-domain']), ('useGrants', 1),
                               ('useGrants', False), ('taskCount', 0), ('useCount', True),
                               ('observationCount', -1), ('observationSha256', '0' * 64),
                               ('mfaLastStep', -1), ('mfaLastStep', True)]:
                mutations.append((f'{phase} {key} {value}', lambda d, phase=phase, key=key, value=value: d['reader'][phase].update({key: value})))
            for key in ['taskCount', 'useCount', 'observationCount', 'mfaLastStep']:
                mutations.append((f'{phase} changed {key}', lambda d, phase=phase, key=key: d['reader'][phase].update({key: d['reader'][phase][key] + 1})))
        for label, mutate in mutations:
            altered = copy.deepcopy(data)
            mutate(altered)
            with self.subTest(label=label), self.assertRaises((ValueError, KeyError)):
                v.validate(altered, data['backend']['provenance'], data['uiProvenance'], shots.__getitem__)

    def test_reader_missing_and_rehashed_bad_png_rejected(self):
        data, shots = specimen()
        for case in data['readerUi']['cases']:
            name = case['screenshot']['filename']
            remaining = {key: value for key, value in shots.items() if key != name}
            with self.subTest(missing=name), self.assertRaises(KeyError):
                v.validate(data, data['backend']['provenance'], data['uiProvenance'], remaining.__getitem__)
        name = data['readerUi']['cases'][0]['screenshot']['filename']
        original = shots[name]
        for raw in [b'', b'not png' * 10, original[:24], original[:-12], original[:40] + bytes([original[40] ^ 1]) + original[41:], shots['group-members-v4-reader-390-12.png']]:
            altered = copy.deepcopy(data)
            altered['readerUi']['cases'][0]['screenshot']['sha256'] = hashlib.sha256(raw).hexdigest()
            with self.subTest(size=len(raw)), self.assertRaises(ValueError):
                v.validate(altered, data['backend']['provenance'], data['uiProvenance'], lambda filename: raw if filename == name else shots[filename])

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
