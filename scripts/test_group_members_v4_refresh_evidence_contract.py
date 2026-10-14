"""Synthetic validator specimens only, never browser/Worker acceptance evidence."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import validate_group_members_v4_refresh_evidence as v
from test_group_members_v4_empty_evidence_contract import png, specimen as empty_specimen


def body(data, key, value=None):
    b = data[key]
    if value is None:
        value = {'dictionaryVersion': 4, 'source': b['source'], 'objects': v.stored_objects() if key == 'backend' else []}
    raw = value if type(value) is bytes else json.dumps(value, ensure_ascii=False).encode()
    b['bodyBase64'], b['sha256'] = base64.b64encode(raw).decode(), hashlib.sha256(raw).hexdigest()
    for phase in ['before', 'after']:
        data['reader'][phase]['oldObservationSha256' if key == 'backend' else 'observationSha256'] = b['sha256']


def specimen():
    data, _ = empty_specimen()
    data['provenance']['sourceDigests'] = {p: 'b' * 64 for p in v.SOURCES}
    new = data.pop('backend')
    old = copy.deepcopy(new)
    old.update(taskUUID='old-nonempty-task', count=30, objects=v.fixture_objects())
    old['source'].update(pages=2, started_at='2026-10-13T01:02:01.123456789Z', completed_at='2026-10-13T01:02:02.123456789Z')
    data.update(backend=old, emptyBackend=new)
    for phase in ['before', 'after']:
        data['reader'][phase].update(observationCount=2, oldObservationSha256='')
    body(data, 'backend')
    body(data, 'emptyBackend')
    query = copy.deepcopy(data['noObservation']['query'])
    detail_query = {'domainId': query['domainId'], 'expectedRevision': '2', 'expectedCredentialRevision': '1', 'observationId': old['taskUUID'], 'objectGUID': v.fixture_objects()[14]['objectGUID']}
    detail = {'query': detail_query, 'value': {'dictionaryVersion': 4, 'selection': {'domainId': query['domainId'], 'revision': '2', 'credentialRevision': '1'}, 'observationId': old['taskUUID'], 'source': copy.deepcopy(old['source']), 'object': v.fixture_objects()[14]}}
    shots = {}
    def shot(name, width, height):
        shots[name] = png(width, height)
        return {'filename': name, 'sha256': hashlib.sha256(shots[name]).hexdigest()}
    ui = {'actorId': 3, 'authorization': 'custom_readonly_role',
          'initialDiscovery': {'query': query, 'value': v.list_value(old, False)},
          'initialDetail': detail, 'initialRendered': v.rendered(old, False, True),
          'initialScreenshot': shot('group-members-v4-refresh-reader-1440-initial.png', 1440, 900),
          'retained': v.rendered(old, False, True), 'steps': [], 'writes': [], 'detailRequests': [{'query': copy.deepcopy(detail_query)} for _ in range(2)],
          'clearStates': [{'detailCount': 0, 'sourceCount': 0, 'listCount': 0} for _ in range(2)], 'cases': []}
    for name in v.STEPS:
        if name == 'old-detail-reopen':
            step = {'name': name, **copy.deepcopy(detail), 'rendered': v.rendered(old, False, True)}
        else:
            empty = name != 'pinned-reread'
            b = new if empty else old
            q = dict(query)
            if name in ['pinned-reread', 'empty-reread']:
                q['observationId'] = b['taskUUID']
            step = {'name': name, 'query': q, 'value': v.list_value(b, empty), 'rendered': v.rendered(b, empty)}
        ui['steps'].append(copy.deepcopy(step))
    for width, height in [(1440, 900), (390, 844)]:
        ui['cases'].append({'viewport': {'width': width, 'height': height}, 'rendered': {**v.rendered(new, True), 'unknownText': v.UNKNOWN_TEXT}, 'screenshot': shot(f'group-members-v4-refresh-reader-{width}.png', width, height)})
    data['ui'] = ui
    # Break shared references so mutations test individual evidence surfaces.
    return copy.deepcopy(data), shots


def at(value, path):
    for key in path:
        value = value[key]
    return value


class RefreshEvidence(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data, cls.shots = specimen()

    def accept(self, data=None, shots=None):
        v.validate(self.data if data is None else data, self.data['provenance'], (self.shots if shots is None else shots).__getitem__)

    def rejects(self, data, shots=None):
        with self.assertRaises((ValueError, KeyError, TypeError)):
            self.accept(data, shots)

    def test_user_export_mark_is_explicitly_denied(self):
        data, shots = specimen()
        permission = next(p for p in data['reader']['permissions'] if p['mark'] == 'user_asset_exports')
        self.assertEqual(permission['auth'], {'readable': False, 'writeable': False})
        self.assertEqual(len(data['reader']['permissions']), 15)
        # Registration expands the exact catalog, never this reader's authority.
        for field in ('readable', 'writeable'):
            for value in (True, 1):
                altered = copy.deepcopy(data)
                next(p for p in altered['reader']['permissions'] if p['mark'] == 'user_asset_exports')['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    self.accept(altered)
        altered = copy.deepcopy(data)
        altered['reader']['permissions'] = [p for p in altered['reader']['permissions'] if p['mark'] != 'user_asset_exports']
        with self.assertRaises(ValueError):
            self.accept(altered)

    def test_group_export_mark_is_explicitly_denied(self):
        data, shots = specimen()
        permission = next(p for p in data['reader']['permissions'] if p['mark'] == 'group_asset_exports')
        self.assertEqual(permission['auth'], {'readable': False, 'writeable': False})
        self.assertEqual(len(data['reader']['permissions']), 15)
        # Registration expands the exact catalog, never this reader's authority.
        for field in ('readable', 'writeable'):
            for value in (True, 1):
                altered = copy.deepcopy(data)
                next(p for p in altered['reader']['permissions'] if p['mark'] == 'group_asset_exports')['auth'][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    self.accept(altered)
        altered = copy.deepcopy(data)
        altered['reader']['permissions'] = [p for p in altered['reader']['permissions'] if p['mark'] != 'group_asset_exports']
        with self.assertRaises(ValueError):
            self.accept(altered)

    def test_valid_contract_specimen(self):
        self.accept()

    def test_fixed_fixture_boundary_values(self):
        objects = v.fixture_objects()
        self.assertEqual(len(objects), 30)
        self.assertEqual([o['kind'] for o in objects], ['user'] * 12 + ['group'] * 12 + ['computer'] * 6)
        self.assertEqual(objects[0]['userAccountControl'], 0)
        self.assertEqual(objects[0]['whenCreated'], '0001-01-01T00:00:00Z')
        self.assertEqual(objects[1]['primaryGroupID'], 0)
        self.assertEqual(objects[2]['primaryGroupID'], 4294967295)
        self.assertIsNone(objects[11]['primaryGroupID'])
        self.assertIsNone(objects[12]['rawMember'])
        self.assertEqual(objects[13]['rawMember'], [])
        self.assertEqual(objects[14]['objectGUID'], '0f0f0f0f-0f0f-0f0f-0f0f-0f0f0f0f0f0f')
        self.assertEqual(objects[14]['rawMember'][-1], 'CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid')
        self.assertIsNone(objects[23]['samAccountName'])
        self.assertEqual(objects[24]['samAccountName'], 'computer-01$')
        raw = v.stored_objects()
        self.assertEqual(base64.b64decode(raw[0]['supplemental']['objectSidBytes']), bytes.fromhex('010500000000000515000000010000000200000003000000e9030000'))
        self.assertEqual(base64.b64decode(raw[0]['identity']['servicePrincipalNameBytes'][1]), b'host/a\x00\n\xf0\x9f\x98\x80<script>')
        self.assertEqual(raw[13]['membership'], {'memberBytes': []})
        self.assertEqual(raw[12]['membership'], {'memberBytes': None})

    def test_missing_and_extra_fields_fail_at_each_schema(self):
        paths = [[], ['provenance'], ['backend'], ['backend', 'source'], ['backend', 'objects', 14], ['emptyBackend'], ['producer'], ['reader'], ['reader', 'before'], ['reader', 'after'], ['reader', 'authentication'], ['reader', 'permissions', 0], ['reader', 'permissions', 0, 'auth'], ['ui'], ['ui', 'initialDiscovery'], ['ui', 'initialDiscovery', 'query'], ['ui', 'initialDetail'], ['ui', 'initialDetail', 'query'], ['ui', 'initialDetail', 'value'], ['ui', 'initialDetail', 'value', 'selection'], ['ui', 'initialRendered'], ['ui', 'retained'], ['ui', 'initialScreenshot'], ['ui', 'clearStates', 0], ['ui', 'cases', 0], ['ui', 'cases', 0, 'viewport'], ['ui', 'cases', 0, 'rendered'], ['ui', 'cases', 0, 'screenshot']]
        for i in range(6):
            paths += [['ui', 'steps', i], ['ui', 'steps', i, 'query'], ['ui', 'steps', i, 'value'], ['ui', 'steps', i, 'rendered']]
        for path in paths:
            for key in at(self.data, path):
                changed = copy.deepcopy(self.data)
                at(changed, path).pop(key)
                with self.subTest(path=path, missing=key):
                    self.rejects(changed)
            changed = copy.deepcopy(self.data)
            at(changed, path)['extra'] = 'unbound'
            with self.subTest(path=path, extra=True):
                self.rejects(changed)

    def test_lifecycle_identity_source_and_authority_mutations(self):
        changes = [(['format'], True), (['producer', 'actorId'], 3), (['producer', 'grantRevoked'], False), (['producer', 'eligible'], True), (['producer', 'explicitlyGranted'], True), (['producer', 'v4AllowedGrants'], False), (['reader', 'actorId'], 2), (['reader', 'roleId'], 'platform_admin'), (['reader', 'producerActorId'], 7), (['reader', 'permissions'], []), (['reader', 'menu'], []), (['reader', 'checks'], []), (['reader', 'checks', 0, 'allowed'], 1), (['reader', 'authentication', 'loginMfa'], False), (['reader', 'authentication', 'hasMfa'], 1), (['reader', 'permissions', 0, 'auth', 'writeable'], 0), (['ui', 'actorId'], True), (['ui', 'authorization'], 'platform_admin'), (['ui', 'writes'], ['POST /api/directory/v4/sync']), (['noObservation', 'value', 'available'], True)]
        for section in ['backend', 'emptyBackend']:
            for key, value in [('dictionaryVersion', True), ('count', False), ('taskState', 'running'), ('useState', 'active'), ('purpose', 'domain.directory_read'), ('reason', 'expired'), ('dependencies', False), ('taskUUID', 'other'), ('sha256', '0' * 64), ('bodyBase64', 'e30='), ('objects', [{}])]:
                changes.append(([section, key], value))
            for key, value in [('domain', 'wrong.invalid'), ('pages', True), ('started_at', 'invalid'), ('completed_at', '2026-10-12T00:00:00Z'), ('elapsed_milliseconds', -1)]:
                changes.append(([section, 'source', key], value))
        for phase in ['before', 'after']:
            for key, value in [('actorId', True), ('roleId', 'viewer'), ('baseRole', 'platform_admin'), ('readable', []), ('writeable', ['domains']), ('domainIds', ['other']), ('useGrants', False), ('taskCount', 0), ('useCount', 0), ('observationCount', 1), ('oldObservationSha256', '0' * 64), ('observationSha256', '0' * 64), ('mfaLastStep', -1)]:
                changes.append((['reader', phase, key], value))
            for key in ['taskCount', 'useCount', 'observationCount', 'mfaLastStep']:
                changes.append((['reader', phase, key], self.data['reader'][phase][key] + 1))
        for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit', 'runId', 'runAttempt', 'eventName']:
            changes.append((['provenance', key], 'wrong'))
        changes.append((['provenance', 'sourceDigests', 'web/src/App.tsx'], 'f' * 64))
        for path, value in changes:
            changed = copy.deepcopy(self.data)
            at(changed, path[:-1])[path[-1]] = value
            with self.subTest(path=path, value=value):
                self.rejects(changed)

    def test_step_order_pin_clear_and_retention_mutations(self):
        changes = [(['ui', 'detailRequests'], []), (['ui', 'detailRequests', 1, 'query', 'observationId'], 'new'), (['ui', 'steps'], []), (['ui', 'clearStates'], []), (['ui', 'clearStates', 0, 'detailCount'], 1), (['ui', 'clearStates', 1, 'sourceCount'], False), (['ui', 'retained', 'observationId'], self.data['emptyBackend']['taskUUID']), (['ui', 'retained', 'rawMember'], []), (['ui', 'initialRendered', 'detailCount'], 0), (['ui', 'initialDetail', 'query', 'objectGUID'], v.fixture_objects()[13]['objectGUID']), (['ui', 'initialDetail', 'query', 'expectedRevision'], '0'), (['ui', 'initialDiscovery', 'query', 'pageSize'], '50')]
        for i in range(6):
            changes.extend([(['ui', 'steps', i, 'name'], 'other'), (['ui', 'steps', i, 'query', 'observationId'], 'other'), (['ui', 'steps', i, 'value', 'observationId'], 'other'), (['ui', 'steps', i, 'rendered', 'detailCount'], 5), (['ui', 'steps', i, 'rendered', 'groupCount'], False), (['ui', 'steps', i, 'rendered', 'sourceValues'], []), (['ui', 'steps', i, 'rendered', 'rawMember'], ['stale-member'])])
        for i in range(2):
            changes.extend([(['ui', 'cases', i, 'rendered', 'unknownText'], '组为空'), (['ui', 'cases', i, 'rendered', 'pagerText'], '第 1 页 · 共 1 页'), (['ui', 'cases', i, 'rendered', 'nextDisabled'], 1)])
        for path, value in changes:
            changed = copy.deepcopy(self.data)
            at(changed, path[:-1])[path[-1]] = value
            with self.subTest(path=path):
                self.rejects(changed)
        changed = copy.deepcopy(self.data)
        changed['ui']['steps'][0], changed['ui']['steps'][1] = changed['ui']['steps'][1], changed['ui']['steps'][0]
        self.rejects(changed)

    def test_synchronized_public_body_api_dom_forgery_rejected(self):
        # All evidence copies lie consistently; independent fixed fixture wins.
        changed = copy.deepcopy(self.data)
        def forge(value):
            if type(value) is dict:
                for key, item in value.items():
                    if key == 'rawMember' and item:
                        value[key] = ['forged'] * len(item)
                    else:
                        forge(item)
            elif type(value) is list:
                for item in value:
                    forge(item)
        forge(changed)
        stored = v.stored_objects()
        for obj in stored:
            if obj['membership']['memberBytes']:
                obj['membership']['memberBytes'] = [v.encoded('forged')] * len(obj['membership']['memberBytes'])
        body(changed, 'backend', {'dictionaryVersion': 4, 'source': changed['backend']['source'], 'objects': stored})
        self.rejects(changed)
        for section, key in [('base', 'samAccountName'), ('supplemental', 'mailBytes'), ('identity', 'primaryGroupIDBytes'), ('membership', 'memberBytes')]:
            changed = copy.deepcopy(self.data)
            stored = v.stored_objects()
            stored[14 if section == 'membership' else 0][section][key] = None
            body(changed, 'backend', {'dictionaryVersion': 4, 'source': changed['backend']['source'], 'objects': stored})
            with self.subTest(section=section):
                self.rejects(changed)

    def test_full_body_duplicates_unknown_keys_nonfinite_and_nonempty_rejected(self):
        for section in ['backend', 'emptyBackend']:
            original = v.load_json(base64.b64decode(self.data[section]['bodyBase64']))
            bad = [b'{"dictionaryVersion":4,"dictionaryVersion":4,"objects":[],"source":{}}', b'{"dictionaryVersion":4,"objects":[],"source":NaN}', {**original, 'extra': True}, {**original, 'dictionaryVersion': True}, {**original, 'objects': None}, {**original, 'source': {**original['source'], 'pages': True}}]
            bad.append({**original, 'objects': [] if section == 'backend' else v.stored_objects()})
            for value in bad:
                changed = copy.deepcopy(self.data)
                body(changed, section, value)
                with self.subTest(section=section, value=str(value)[:70]):
                    self.rejects(changed)

    def test_distinct_observations_and_strict_nanosecond_order(self):
        changed = copy.deepcopy(self.data)
        changed['emptyBackend']['taskUUID'] = changed['backend']['taskUUID']
        self.rejects(changed)
        changed = copy.deepcopy(self.data)
        changed['backend']['source']['completed_at'] = '2026-10-13T01:02:03.123456790Z'
        body(changed, 'backend')
        with self.assertRaisesRegex(ValueError, 'old completes before new starts'):
            v.validate(changed, self.data['provenance'], self.shots.__getitem__)
        self.assertLess(v.timestamp('2026-10-13T01:02:03.123456789Z'), v.timestamp('2026-10-13T01:02:03.123456790Z'))

    def test_every_screenshot_is_required_crc_checked_and_dimension_bound(self):
        paths = [['ui', 'initialScreenshot'], *[['ui', 'cases', i, 'screenshot'] for i in range(2)]]
        for path in paths:
            name = at(self.data, path)['filename']
            shots = {k: value for k, value in self.shots.items() if k != name}
            self.rejects(self.data, shots)
            original = self.shots[name]
            corrupt = bytearray(original); corrupt[29] ^= 1
            for raw in [b'not png', original[:24], bytes(corrupt), original + b'extra', png(2, 2), original[:-12]]:
                changed = copy.deepcopy(self.data)
                at(changed, path)['sha256'] = hashlib.sha256(raw).hexdigest()
                with self.subTest(path=path, size=len(raw)):
                    self.rejects(changed, {**self.shots, name: raw})

    def test_untrusted_provenance_cannot_drop_required_sources(self):
        for change in [lambda p: p['sourceDigests'].pop('web/src/App.tsx'), lambda p: p['sourceDigests'].update(extra='a' * 64), lambda p: p.update(runAttempt='0'), lambda p: p.update(checkoutTree='bad')]:
            changed = copy.deepcopy(self.data)
            change(changed['provenance'])
            with self.assertRaises(ValueError):
                v.validate(changed, changed['provenance'], self.shots.__getitem__)

    def test_envelope_json_duplicate_and_nonfinite_rejected(self):
        for raw in ['{"format":1,"format":1}', '{"nested":{"key":0,"key":0}}', '{"format":NaN}', '{"format":Infinity}']:
            with self.assertRaises(ValueError):
                v.load_json(raw)

    def test_filesystem_symlinks_traversal_duplicate_missing_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            output = root / 'web/test-results/test'
            output.mkdir(parents=True)
            artifact = output / 'group-members-v4-refresh-evidence.json'
            artifact.write_text('{}')
            self.assertEqual(v.evidence_file(root), artifact)
            reader = v.screenshot_reader(output)
            for name, raw in self.shots.items():
                (output / name).write_bytes(raw)
                self.assertEqual(reader(name), raw)
            for name in ['../outside.png', '/tmp/outside.png', 'missing.png']:
                with self.assertRaises(ValueError):
                    reader(name)
            name = next(iter(self.shots))
            (output / name).unlink(); (output / name).symlink_to(artifact)
            with self.assertRaises(ValueError):
                reader(name)
            with self.assertRaises(ValueError):
                v.evidence_file(root)
            (output / name).unlink()
            duplicate = output / 'duplicate'; duplicate.mkdir()
            (duplicate / artifact.name).write_text('{}')
            with self.assertRaises(ValueError):
                v.evidence_file(root)
            (duplicate / artifact.name).unlink(); artifact.unlink()
            with self.assertRaises(ValueError):
                v.evidence_file(root)

    def test_dirty_and_untracked_source_rejected(self):
        def dirty(root, *args):
            return 'dirty' if args[:2] == ('diff', 'HEAD') else ''
        def untracked(root, *args):
            return 'web/e2e/other.ts' if args and args[0] == 'ls-files' else ''
        for fake in [dirty, untracked]:
            with patch.object(v.backend, 'git', side_effect=fake), self.assertRaises(ValueError):
                v.expected_provenance(Path('/unused'), {})

    def test_legacy_primary_contract_remains_30_objects_two_pages(self):
        from test_directory_v4_evidence_contract import evidence
        legacy = evidence()
        v.backend.validate(legacy, legacy['provenance'])
        legacy['source']['pages'] = 1
        with self.assertRaises(ValueError):
            v.backend.validate(legacy, legacy['provenance'])
        legacy = evidence(); legacy['objects'] = []
        with self.assertRaises(ValueError):
            v.backend.validate(legacy, legacy['provenance'])


if __name__ == '__main__':
    unittest.main()
