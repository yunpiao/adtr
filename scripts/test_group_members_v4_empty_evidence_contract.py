"""Synthetic fail-closed specimens only; these are never E2E acceptance."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib

import validate_group_members_v4_empty_evidence as v


def png(width, height):
    # Generated only for validator testing, never published as browser evidence.
    def chunk(kind, payload):
        return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress((b'\0' + b'\xff' * width * 3) * height)) + chunk(b'IEND', b'')


def rebody(data, body):
    raw = body if isinstance(body, bytes) else json.dumps(body).encode()
    data['backend']['bodyBase64'] = base64.b64encode(raw).decode()
    data['backend']['sha256'] = hashlib.sha256(raw).hexdigest()
    for phase in ['before', 'after']:
        data['reader'][phase]['observationSha256'] = data['backend']['sha256']


def specimen():
    source = {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid', 'domain': 'synthetic.invalid', 'naming_context': 'dc=synthetic,dc=invalid', 'started_at': '2026-10-13T01:02:03.123456789Z', 'completed_at': '2026-10-13T01:02:04.123456789Z', 'elapsed_milliseconds': 1000, 'pages': 1}
    provenance = {**{key: 'a' * 40 for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit']}, 'runId': '1234', 'runAttempt': '1', 'eventName': 'pull_request', 'sourceDigests': {p: 'b' * 64 for p in v.SOURCES}}
    b = {'taskUUID': 'fixture-empty-task', 'bodyBase64': '', 'sha256': '', 'dictionaryVersion': 4, 'count': 0, 'taskState': 'succeeded', 'purpose': 'domain.directory_read.v4', 'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'source': source, 'objects': []}
    query = {'domainId': 'synthetic-domain', 'kind': 'group', 'pageIdx': '1', 'pageSize': '25'}
    value = {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'], 'source': copy.deepcopy(source), 'list': [], 'page': copy.deepcopy(v.PAGE)}
    ui = {'actorId': 3, 'authorization': 'custom_readonly_role', 'discovery': {'query': query, 'value': value}, 'reread': {'query': {**query, 'observationId': b['taskUUID']}, 'value': copy.deepcopy(value)}, 'cases': [], 'writes': [], 'detailRequests': []}
    shots = {}
    for width, height in [(1440, 900), (390, 844)]:
        name = f'group-members-v4-empty-reader-{width}.png'
        shots[name] = png(width, height)
        ui['cases'].append({'viewport': {'width': width, 'height': height}, 'rendered': {'unknownText': v.UNKNOWN_TEXT, 'pagerText': v.PAGER_TEXT, 'previousDisabled': True, 'nextDisabled': True, 'groupCount': 0, 'detailCount': 0, 'sourceValues': [b['taskUUID'], source['dc_host_name'], source['domain'], source['naming_context'], source['started_at'], source['completed_at']]}, 'screenshot': {'filename': name, 'sha256': hashlib.sha256(shots[name]).hexdigest()}})
    snapshot = {'actorId': 3, 'roleId': 'r' * 24, 'baseRole': 'viewer', 'readable': ['directory_assets', 'domains'], 'writeable': [], 'domainIds': [query['domainId']], 'useGrants': 0, 'taskCount': 2, 'useCount': 2, 'observationCount': 1, 'observationSha256': '', 'mfaLastStep': 123}
    permissions = [{'mark': mark, 'auth': {'readable': mark in v.membership.READABLE, 'writeable': False}} for mark in sorted(v.membership.PERMISSIONS)]
    reader = {'actorId': 3, 'producerActorId': 2, 'roleId': 'r' * 24, 'permissions': permissions, 'menu': [copy.deepcopy(p) for p in permissions if p['auth']['readable']], 'checks': [*({'method': 'GET', 'path': path, 'allowed': True} for path in v.membership.READ_PATHS), *({'method': 'POST', 'path': path, 'allowed': False} for path in v.membership.WRITE_PATHS)], 'authentication': {'role': 'viewer', 'hasMfa': True, 'needChangePwd': False, 'loginMfa': True}, 'before': copy.deepcopy(snapshot), 'after': copy.deepcopy(snapshot)}
    data = {'format': 1, 'provenance': provenance, 'noObservation': {'query': copy.deepcopy(query), 'value': {'dictionaryVersion': 4, 'available': False, 'list': [], 'page': copy.deepcopy(v.PAGE)}}, 'backend': b, 'producer': {'actorId': 2, 'grantRevoked': True, 'explicitlyGranted': False, 'eligible': False, 'v4AllowedGrants': 0}, 'reader': reader, 'ui': ui}
    rebody(data, {'dictionaryVersion': 4, 'objects': [], 'source': source})
    return data, shots


class EmptyMembershipEvidence(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data, cls.shots = specimen()

    def accept(self, data=None, shots=None):
        v.validate(self.data if data is None else data, self.data['provenance'], (self.shots if shots is None else shots).__getitem__)

    def test_valid_contract_specimen(self):
        self.accept()

    def test_rejects_missing_or_extra_fields(self):
        for section in [[], ['backend'], ['producer'], ['reader'], ['ui'], ['provenance'], ['reader', 'before'], ['reader', 'after']]:
            original = self.data
            for key in section:
                original = original[key]
            for missing in original:
                altered = copy.deepcopy(self.data)
                target = altered
                for key in section:
                    target = target[key]
                target.pop(missing)
                with self.subTest(section=section, missing=missing), self.assertRaises(ValueError):
                    self.accept(altered)
            altered = copy.deepcopy(self.data)
            target = altered
            for key in section:
                target = target[key]
            target['unexpected'] = True
            with self.subTest(section=section, extra=True), self.assertRaises(ValueError):
                self.accept(altered)

    def test_rejects_tampering_and_type_confusion(self):
        mutations = [
            (['format'], True), (['backend', 'count'], 1), (['backend', 'count'], False),
            (['backend', 'objects'], [{}]), (['backend', 'dictionaryVersion'], 3),
            (['backend', 'taskState'], 'running'), (['backend', 'useState'], 'active'),
            (['backend', 'reason'], 'expired'), (['backend', 'purpose'], 'domain.directory_read'),
            (['backend', 'dependencies'], 1), (['backend', 'dependencies'], False),
            (['backend', 'taskUUID'], 'other'), (['backend', 'sha256'], '0' * 64),
            (['backend', 'source', 'pages'], 2), (['backend', 'source', 'pages'], True),
            (['backend', 'source', 'domain'], 'wrong.invalid'), (['backend', 'source', 'started_at'], 'bad'),
            (['backend', 'source', 'completed_at'], '2026-10-12T01:02:03Z'),
            (['backend', 'source', 'elapsed_milliseconds'], -1),
            (['producer', 'actorId'], 3), (['producer', 'grantRevoked'], False),
            (['producer', 'eligible'], True), (['producer', 'explicitlyGranted'], True),
            (['producer', 'v4AllowedGrants'], 1), (['producer', 'v4AllowedGrants'], False),
            (['reader', 'actorId'], 2), (['reader', 'roleId'], 'platform_admin'),
            (['reader', 'producerActorId'], 4), (['reader', 'permissions'], []),
            (['reader', 'menu'], []), (['reader', 'checks'], []),
            (['reader', 'permissions', 0, 'auth', 'writeable'], True),
            (['reader', 'permissions', 0, 'auth', 'readable'], True),
            (['reader', 'permissions', 0, 'auth', 'writeable'], 0),
            (['reader', 'checks', 0, 'allowed'], 1),
            (['reader', 'authentication', 'loginMfa'], False),
            (['reader', 'authentication', 'hasMfa'], 1),
            (['reader', 'authentication', 'role'], 'platform_admin'),
            (['ui', 'actorId'], 2), (['ui', 'authorization'], 'platform_admin'),
            (['ui', 'writes'], ['POST /api/directory/v4/sync']),
            (['ui', 'detailRequests'], ['GET /api/group-assets/v4/members']),
            (['ui', 'discovery', 'query', 'domainId'], 'other'),
            (['ui', 'reread', 'query', 'observationId'], 'other'),
            (['ui', 'discovery', 'value', 'available'], False),
            (['ui', 'discovery', 'value', 'list'], [{}]),
            (['ui', 'reread', 'value', 'source', 'pages'], 2),
            (['ui', 'cases'], []), (['ui', 'cases', 0, 'viewport', 'width'], 390),
            (['ui', 'cases', 0, 'rendered', 'unknownText'], '组为空'),
            (['ui', 'cases', 0, 'rendered', 'pagerText'], '第 1 页 · 共 1 页'),
            (['ui', 'cases', 0, 'rendered', 'previousDisabled'], False),
            (['ui', 'cases', 1, 'rendered', 'nextDisabled'], 1),
            (['ui', 'cases', 0, 'rendered', 'groupCount'], False),
            (['ui', 'cases', 1, 'rendered', 'detailCount'], 1),
            (['ui', 'cases', 0, 'rendered', 'sourceValues', 0], 'other'),
            (['ui', 'cases', 0, 'screenshot', 'filename'], '../outside.png'),
            (['ui', 'cases', 0, 'screenshot', 'sha256'], '0' * 64),
            (['noObservation', 'value', 'available'], True),
            (['noObservation', 'query', 'domainId'], 'other'),
            (['noObservation', 'value', 'page', 'total'], 1),
        ]
        for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit', 'runId', 'runAttempt', 'eventName']:
            mutations.append((['provenance', key], 'mismatch'))
        mutations.append((['provenance', 'sourceDigests', 'web/src/App.tsx'], 'f' * 64))
        for phase in ['before', 'after']:
            for key, value in [('actorId', 2), ('roleId', 'viewer'), ('baseRole', 'platform_admin'), ('readable', ['domains']), ('writeable', ['domains']), ('domainIds', []), ('domainIds', ['synthetic-domain', 'extra']), ('useGrants', 1), ('useGrants', False), ('taskCount', 0), ('useCount', False), ('observationCount', -1), ('observationSha256', '0' * 64), ('mfaLastStep', -1)]:
                mutations.append((['reader', phase, key], value))
            for key in ['taskCount', 'useCount', 'observationCount', 'mfaLastStep']:
                mutations.append((['reader', phase, key], self.data['reader'][phase][key] + 1))
        for section in ['discovery', 'reread']:
            for key, value in [('pageIdx', 2), ('pageSize', 100), ('total', 1), ('totalPage', 1), ('total', False)]:
                mutations.append((['ui', section, 'value', 'page', key], value))
        for path, value in mutations:
            altered = copy.deepcopy(self.data)
            target = altered
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
            with self.subTest(path=path, value=value), self.assertRaises((ValueError, KeyError)):
                self.accept(altered)

    def test_rehashed_nonempty_or_invalid_stored_body_rejected(self):
        original = json.loads(base64.b64decode(self.data['backend']['bodyBase64']))
        bad = [b'{"dictionaryVersion":4,"dictionaryVersion":4,"objects":[],"source":{}}',
               b'{"dictionaryVersion":4,"objects":[],"source":NaN}',
               {**original, 'objects': [{'kind': 'user'}]},
               {**original, 'objects': None}, {**original, 'dictionaryVersion': 3},
               {**original, 'source': {**original['source'], 'pages': 2}},
               {**original, 'extra': 'unbound'}]
        for body in bad:
            altered = copy.deepcopy(self.data)
            rebody(altered, body)
            with self.subTest(body=body), self.assertRaises(ValueError):
                self.accept(altered)

    def test_missing_corrupt_rehashed_or_wrong_dimensions_png_rejected(self):
        for index, case in enumerate(self.data['ui']['cases']):
            name = case['screenshot']['filename']
            missing = {key: value for key, value in self.shots.items() if key != name}
            with self.subTest(missing=name), self.assertRaises(KeyError):
                self.accept(shots=missing)
            original = self.shots[name]
            corrupt = bytearray(original)
            corrupt[29] ^= 1
            for raw in [b'not png', original[:24], bytes(corrupt), original + b'extra', png(2, 2), original[:-12]]:
                altered = copy.deepcopy(self.data)
                altered['ui']['cases'][index]['screenshot']['sha256'] = hashlib.sha256(raw).hexdigest()
                with self.subTest(name=name, size=len(raw)), self.assertRaises(ValueError):
                    self.accept(altered, {**self.shots, name: raw})

    def test_source_set_required_even_if_expected_is_mutated(self):
        for mutate in [lambda p: p['sourceDigests'].pop('web/src/App.tsx'), lambda p: p['sourceDigests'].update(extra='a' * 64), lambda p: p.update(runAttempt='0'), lambda p: p.update(checkoutTree='bad')]:
            altered = copy.deepcopy(self.data)
            mutate(altered['provenance'])
            with self.assertRaises(ValueError):
                v.validate(altered, altered['provenance'], self.shots.__getitem__)

    def test_rejects_symlinks_traversal_missing_and_duplicate_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            output = root / 'web/test-results/test'
            output.mkdir(parents=True)
            artifact = output / 'group-members-v4-empty-evidence.json'
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
            (output / name).unlink()
            (output / name).symlink_to(artifact)
            with self.assertRaises(ValueError):
                reader(name)
            with self.assertRaises(ValueError):
                v.evidence_file(root)
            (output / name).unlink()
            duplicate = output / 'duplicate'
            duplicate.mkdir()
            (duplicate / artifact.name).write_text('{}')
            with self.assertRaises(ValueError):
                v.evidence_file(root)
            (duplicate / artifact.name).unlink()
            artifact.unlink()
            with self.assertRaises(ValueError):
                v.evidence_file(root)

    def test_nonfinite_and_duplicate_envelope_json_rejected(self):
        for raw in ['{"format":1,"format":1}', '{"format":NaN}', '{"format":Infinity}']:
            with self.assertRaises(ValueError):
                v.load_json(raw)

    def test_dirty_or_untracked_checkout_rejected(self):
        def git_dirty(root, *args):
            return 'dirty' if args[:2] == ('diff', 'HEAD') else ''
        def git_untracked(root, *args):
            return 'web/e2e/other.ts' if args and args[0] == 'ls-files' else ''
        for function in [git_dirty, git_untracked]:
            with patch.object(v.backend, 'git', side_effect=function), self.assertRaises(ValueError):
                v.expected_provenance(Path('/unused'), {})

    def test_primary_validator_contract_stays_30_objects_two_pages(self):
        # This suite cannot make the legacy model accept its zero-object body.
        from test_directory_v4_evidence_contract import evidence
        legacy = evidence()
        v.backend.validate(legacy, legacy['provenance'])
        legacy['source']['pages'] = 1
        with self.assertRaises(ValueError):
            v.backend.validate(legacy, legacy['provenance'])
        legacy = evidence()
        legacy['objects'] = []
        with self.assertRaises(ValueError):
            v.backend.validate(legacy, legacy['provenance'])


if __name__ == '__main__':
    unittest.main()
