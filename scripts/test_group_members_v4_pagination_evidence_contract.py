"""Synthetic mutation specimens only: never substitute these for browser evidence."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import validate_group_members_v4_pagination_evidence as v
from test_group_members_v4_empty_evidence_contract import png, specimen as empty_specimen, rebody


def specimen():
    data, _ = empty_specimen()
    data['provenance']['sourceDigests'] = {p: 'b' * 64 for p in v.SOURCES}
    b = data['backend']
    b['count'] = 30
    b['source']['pages'] = 2
    objects, stored = [], []
    for i in range(1, 31):
        kind, index, ou, classes = ('group', i, 'Groups', ['group', 'top']) if i <= 26 else ('user', i - 26, 'Users', ['organizationalperson', 'person', 'top', 'user']) if i <= 28 else ('computer', i - 28, 'Computers', ['computer', 'organizationalperson', 'person', 'top', 'user'])
        h, name = f'{i:02x}', f'pagination-{kind}-{index:02d}'
        raw = None if i == 1 or i > 26 else [] if i == 2 else [
            'CN=page-two-only,OU=Users,DC=synthetic,DC=invalid', 'CN=raw\x00\n😀\u202e<script>,DC=synthetic,DC=invalid',
            '\\u0000', 'CN=page-two-last,OU=Users,DC=synthetic,DC=invalid'] if i == 26 else [f'CN=page-member-{i:02d},OU=Users,DC=synthetic,DC=invalid']
        base = {'objectGUID': f'{h*4}-{h*2}-{h*2}-{h*2}-{h*6}', 'distinguishedName': f'CN={name},OU={ou},DC=synthetic,DC=invalid', 'kind': kind, 'objectClass': classes, 'samAccountName': name + ('$' if i > 28 else ''), 'userAccountControl': None}
        membership = {'memberBytes': None if raw is None else [base64.b64encode(s.encode()).decode() for s in raw]}
        stored.append({'base': base, 'supplemental': dict.fromkeys(['objectSidBytes', 'mailBytes', 'descriptionBytes', 'whenCreatedBytes']), 'identity': {'primaryGroupIDBytes': None, 'servicePrincipalNameBytes': None}, 'membership': membership})
        objects.append({**base, **dict.fromkeys(['objectSid', 'mail', 'description', 'whenCreated', 'primaryGroupID', 'servicePrincipalName']), 'rawMember': raw, 'returnedMemberCount': None if raw is None else len(raw), 'memberReadState': 'not_returned' if raw is None else 'returned'})
    b['objects'], b['storedMembership'] = objects, [o['membership'] for o in stored]
    rebody(data, {'dictionaryVersion': 4, 'objects': stored, 'source': b['source']})
    domain = data['noObservation']['query']['domainId']
    ui = {'actorId': 3, 'authorization': 'custom_readonly_role', 'cases': [], 'writes': []}
    shots = {}
    for width, height in [(1440, 900), (390, 844)]:
        steps = []
        for name, page, size in [('discovery', 1, 25), ('next', 2, 25), ('detail', None, None), ('previous', 1, 25), ('size50', 1, 50), ('size25', 1, 25), ('reread', 1, 25)]:
            filename = f'group-members-v4-pagination-reader-{width}-{name}.png'
            shots[filename] = png(width, height)
            if name == 'detail':
                obj = objects[25]
                query = {'domainId': domain, 'expectedRevision': '1', 'expectedCredentialRevision': '2', 'observationId': b['taskUUID'], 'objectGUID': obj['objectGUID']}
                value = {'dictionaryVersion': 4, 'selection': {'domainId': domain, 'revision': '1', 'credentialRevision': '2'}, 'observationId': b['taskUUID'], 'source': copy.deepcopy(b['source']), 'object': copy.deepcopy(obj)}
                rendered = {'rawMember': ['CN=page-two-only,OU=Users,DC=synthetic,DC=invalid', 'CN=raw\\u0000\\u000a😀\\u202e<script>,DC=synthetic,DC=invalid', '\\\\u0000', 'CN=page-two-last,OU=Users,DC=synthetic,DC=invalid'], 'guid': obj['objectGUID'], 'observationId': b['taskUUID'], 'returnedMemberCountText': '本次返回 member 值数：4，不代表完整成员数。'}
            else:
                query = {'domainId': domain, 'kind': 'group', 'pageIdx': str(page), 'pageSize': str(size)}
                if name != 'discovery': query['observationId'] = b['taskUUID']
                rows, total_page = objects[:26][(page - 1)*size:page*size], 1 if size == 50 else 2
                value = {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'], 'source': copy.deepcopy(b['source']), 'list': copy.deepcopy(rows), 'page': {'pageIdx': page, 'pageSize': size, 'total': 26, 'totalPage': total_page}}
                rendered = {'guids': [o['objectGUID'] for o in rows], 'pagerText': f'第 {page} 页 · 共 {total_page} 页（仅本次观测中的组）', 'previousDisabled': page == 1, 'nextDisabled': page == total_page, 'pageSize': str(size), 'sourceValues': [b['taskUUID'], *[b['source'][k] for k in ['dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at']]], 'detailCount': 0}
            steps.append({'name': name, 'query': query, 'value': value, 'rendered': rendered, 'screenshot': {'filename': filename, 'sha256': hashlib.sha256(shots[filename]).hexdigest()}})
        ui['cases'].append({'viewport': {'width': width, 'height': height}, 'steps': steps})
    data['ui'] = ui
    return data, shots


class PaginationEvidenceContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data, cls.shots = specimen()

    def accept(self, data=None, shots=None):
        v.validate(self.data if data is None else data, self.data['provenance'], (self.shots if shots is None else shots).__getitem__)

    def reject_mutations(self, mutations):
        for path, value in mutations:
            altered = copy.deepcopy(self.data)
            target = altered
            for key in path[:-1]: target = target[key]
            target[path[-1]] = value
            with self.subTest(path=path, value=value), self.assertRaises((ValueError, KeyError)):
                self.accept(altered)

    def test_valid_independent_contract_specimen(self):
        self.accept()

    def test_all_fields_required_and_no_extra_fields(self):
        def walk(value, path):
            if isinstance(value, dict):
                yield path, value
                for key, child in value.items(): yield from walk(child, path + [key])
            elif isinstance(value, list):
                # Every step and every authority entry is covered; repeated object
                # shapes have their own full-body/API equality mutation tests.
                for i, child in enumerate(value):
                    if i == 0 or path[-1:] in [['steps'], ['cases']]: yield from walk(child, path + [i])
        for path, obj in walk(self.data, []):
            for key in [*obj, '__extra__']:
                altered = copy.deepcopy(self.data)
                target = altered
                for part in path: target = target[part]
                if key == '__extra__': target[key] = None
                else: del target[key]
                with self.subTest(path=path, key=key), self.assertRaises((ValueError, KeyError)):
                    self.accept(altered)

    def test_wrong_pages_pins_sources_order_rendering_and_missing_steps(self):
        mutations = [(['ui', 'cases'], []), (['ui', 'actorId'], 2), (['ui', 'authorization'], 'platform_admin'), (['ui', 'writes'], ['POST /api/directory/v4/sync'])]
        for c in range(2):
            cp = ['ui', 'cases', c]
            mutations += [(cp + ['viewport', 'width'], 1), (cp + ['steps'], self.data['ui']['cases'][c]['steps'][:-1])]
            for i, step in enumerate(self.data['ui']['cases'][c]['steps']):
                p = cp + ['steps', i]
                mutations += [(p + ['name'], 'next'), (p + ['query', 'domainId'], 'wrong'), (p + ['value', 'source', 'pages'], 1), (p + ['value', 'observationId'], 'other'), (p + ['value', 'dictionaryVersion'], True), (p + ['screenshot', 'sha256'], '0' * 64), (p + ['screenshot', 'filename'], '../outside.png')]
                if i != 0: mutations.append((p + ['query', 'observationId'], 'wrong'))
                if step['name'] == 'detail':
                    mutations += [(p + ['query', 'objectGUID'], self.data['backend']['objects'][0]['objectGUID']), (p + ['query', 'expectedRevision'], '0'), (p + ['query', 'expectedCredentialRevision'], '3'), (p + ['value', 'object', 'rawMember'], []), (p + ['rendered', 'rawMember'], list(reversed(step['rendered']['rawMember']))), (p + ['rendered', 'guid'], 'wrong'), (p + ['rendered', 'observationId'], 'wrong'), (p + ['rendered', 'returnedMemberCountText'], '4')]
                else:
                    mutations += [(p + ['query', 'pageIdx'], '0'), (p + ['query', 'pageSize'], '100'), (p + ['value', 'available'], 1), (p + ['value', 'list'], []), (p + ['value', 'page', 'total'], 30), (p + ['value', 'page', 'totalPage'], 0), (p + ['value', 'page', 'pageIdx'], True), (p + ['rendered', 'guids'], []), (p + ['rendered', 'pagerText'], 'wrong'), (p + ['rendered', 'previousDisabled'], 1), (p + ['rendered', 'nextDisabled'], 0), (p + ['rendered', 'detailCount'], False), (p + ['rendered', 'sourceValues', 0], 'wrong'), (p + ['rendered', 'pageSize'], 25)]
                    if len(step['value']['list']) > 1:
                        mutations += [(p + ['value', 'list'], list(reversed(step['value']['list']))), (p + ['rendered', 'guids'], list(reversed(step['rendered']['guids'])))]
        # Avoid including the unchanged next-name positive control.
        mutations = [(p, val) for p, val in mutations if not (p[-1] == 'name' and p[-2] == 1)]
        self.reject_mutations(mutations)

    def test_backend_authority_lifecycle_and_provenance_mutations(self):
        mutations = [(['format'], True), (['backend', 'count'], 26), (['backend', 'source', 'pages'], True), (['backend', 'source', 'domain'], 'wrong'), (['backend', 'source', 'completed_at'], '2020-01-01T00:00:00Z'), (['backend', 'source', 'elapsed_milliseconds'], -1), (['backend', 'taskState'], 'running'), (['backend', 'useState'], 'opened'), (['backend', 'purpose'], 'domain.directory_read.v3'), (['backend', 'reason'], 'expired'), (['backend', 'dependencies'], False), (['backend', 'storedMembership', 25, 'memberBytes'], []), (['backend', 'objects', 25, 'rawMember'], []), (['producer', 'actorId'], 3), (['producer', 'grantRevoked'], False), (['producer', 'v4AllowedGrants'], False), (['producer', 'eligible'], True), (['producer', 'explicitlyGranted'], True), (['reader', 'actorId'], 2), (['reader', 'producerActorId'], 5), (['reader', 'permissions'], []), (['reader', 'menu'], []), (['reader', 'checks'], []), (['reader', 'checks', 0, 'allowed'], 1), (['reader', 'authentication', 'hasMfa'], False), (['reader', 'authentication', 'loginMfa'], 1), (['noObservation', 'value', 'available'], True)]
        for key in self.data['provenance']:
            mutations.append((['provenance', key], {} if key == 'sourceDigests' else 'wrong'))
        for phase in ['before', 'after']:
            for key, val in [('actorId', 2), ('roleId', 'admin'), ('baseRole', 'platform_admin'), ('writeable', ['domains']), ('readable', ['domains']), ('domainIds', ['other']), ('useGrants', True), ('taskCount', 0), ('useCount', 0), ('observationCount', False), ('observationSha256', '0'*64), ('mfaLastStep', -1)]:
                mutations.append((['reader', phase, key], val))
            for key in ['taskCount', 'useCount', 'observationCount', 'mfaLastStep']:
                mutations.append((['reader', phase, key], self.data['reader'][phase][key] + 1))
        self.reject_mutations(mutations)

    def test_fully_rehashed_body_cannot_change_fixture_facts(self):
        body = v.load_json(base64.b64decode(self.data['backend']['bodyBase64']))
        mutations = [(['objects', 25, 'membership', 'memberBytes'], []), (['objects', 0, 'supplemental', 'mailBytes'], 'eA=='), (['objects', 0, 'base', 'samAccountName'], 'forged'), (['objects', 0, 'identity', 'primaryGroupIDBytes'], 'NTEz'), (['objects'], body['objects'][::-1]), (['source', 'pages'], 1), (['dictionaryVersion'], True)]
        for path, value in mutations:
            altered, changed = copy.deepcopy(self.data), copy.deepcopy(body)
            target = changed
            for part in path[:-1]: target = target[part]
            target[path[-1]] = value
            rebody(altered, changed)
            with self.subTest(path=path), self.assertRaises(ValueError): self.accept(altered)
        for raw in [b'{"dictionaryVersion":4,"dictionaryVersion":4}', b'{"objects":NaN}', b'{}']:
            altered = copy.deepcopy(self.data); rebody(altered, raw)
            with self.assertRaises(ValueError): self.accept(altered)

    def test_coordinated_api_body_and_rendered_forgery_still_rejected(self):
        # Rehashing the body and changing every API/DOM copy cannot replace
        # the independent fixture truth with a mutually consistent forgery.
        for old, new in [('pagination-group-26', 'forged-group-26'),
                         ('CN=page-two-last', 'CN=forged-last'),
                         ('1a1a1a1a-1a1a-1a1a-1a1a-1a1a1a1a1a1a', 'ffffffff-ffff-ffff-ffff-ffffffffffff')]:
            altered = json.loads(json.dumps(self.data).replace(old, new))
            body = v.load_json(base64.b64decode(self.data['backend']['bodyBase64']))
            body = json.loads(json.dumps(body).replace(old, new))
            if old.startswith('CN='):
                before = base64.b64encode((old + ',OU=Users,DC=synthetic,DC=invalid').encode()).decode()
                after = base64.b64encode((new + ',OU=Users,DC=synthetic,DC=invalid').encode()).decode()
                body = json.loads(json.dumps(body).replace(before, after))
                altered['backend']['storedMembership'] = json.loads(json.dumps(altered['backend']['storedMembership']).replace(before, after))
            rebody(altered, body)
            with self.subTest(forgery=old), self.assertRaises(ValueError): self.accept(altered)

    def test_every_png_required_crc_digest_bounds_and_dimensions(self):
        for c, case in enumerate(self.data['ui']['cases']):
            for i, step in enumerate(case['steps']):
                name, original = step['screenshot']['filename'], self.shots[step['screenshot']['filename']]
                missing = {k: v for k, v in self.shots.items() if k != name}
                with self.subTest(missing=name), self.assertRaises(KeyError): self.accept(shots=missing)
                corrupt = bytearray(original); corrupt[29] ^= 1
                for raw in [b'not png', original[:24], original[:-12], original + b'extra', bytes(corrupt), png(2, 2)]:
                    altered = copy.deepcopy(self.data)
                    altered['ui']['cases'][c]['steps'][i]['screenshot']['sha256'] = hashlib.sha256(raw).hexdigest()
                    with self.subTest(name=name, size=len(raw)), self.assertRaises(ValueError): self.accept(altered, {**self.shots, name: raw})

    def test_traversal_symlinks_duplicate_missing_artifacts_fail(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'web/test-results/test'; output.mkdir(parents=True)
            artifact = output / 'group-members-v4-pagination-evidence.json'; artifact.write_text('{}')
            self.assertEqual(v.evidence_file(root), artifact)
            reader = v.screenshot_reader(output)
            name = next(iter(self.shots)); (output / name).write_bytes(self.shots[name])
            self.assertEqual(reader(name), self.shots[name])
            for bad in ['../outside.png', '/tmp/outside.png', 'missing.png']:
                with self.assertRaises(ValueError): reader(bad)
            (output / name).unlink(); (output / name).symlink_to(artifact)
            with self.assertRaises(ValueError): reader(name)
            with self.assertRaises(ValueError): v.evidence_file(root)
            (output / name).unlink(); other = output / 'duplicate'; other.mkdir(); (other / artifact.name).write_text('{}')
            with self.assertRaises(ValueError): v.evidence_file(root)
            (other / artifact.name).unlink(); artifact.unlink()
            with self.assertRaises(ValueError): v.evidence_file(root)

    def test_source_types_coverage_and_clean_checkout(self):
        for mutate in [lambda p: p['sourceDigests'].pop('web/src/App.tsx'), lambda p: p['sourceDigests'].update(extra='a'*64), lambda p: p.update(runAttempt='0'), lambda p: p.update(checkoutTree=True)]:
            altered = copy.deepcopy(self.data); mutate(altered['provenance'])
            with self.assertRaises(ValueError): v.validate(altered, altered['provenance'], self.shots.__getitem__)
        for raw in ['{"format":1,"format":1}', '{"format":NaN}', '{"format":Infinity}']:
            with self.assertRaises(ValueError): v.load_json(raw)
        for field in ['dirty', 'untracked']:
            def git(root, *args):
                return 'bad' if (field == 'dirty' and args[:2] == ('diff', 'HEAD')) or (field == 'untracked' and args[0] == 'ls-files') else ''
            with patch.object(v.backend, 'git', side_effect=git), self.assertRaises(ValueError): v.expected_provenance(Path('/unused'), {})

    def test_previous_acceptance_contracts_unchanged(self):
        from test_directory_v4_evidence_contract import evidence
        original = evidence(); v.backend.validate(original, original['provenance'])
        original['source']['pages'] = 1
        with self.assertRaises(ValueError): v.backend.validate(original, original['provenance'])
        import validate_group_members_v4_empty_evidence as empty
        data, shots = empty_specimen(); empty.validate(data, data['provenance'], shots.__getitem__)
        data['backend']['count'] = 30
        with self.assertRaises(ValueError): empty.validate(data, data['provenance'], shots.__getitem__)


if __name__ == '__main__':
    unittest.main()
