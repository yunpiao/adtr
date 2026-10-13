"""Fail-closed 26-group pagination real-browser evidence.

This is a separate acceptance contract. It never calls or relaxes the primary
30-object / two-LDAP-page validator. Contract-test specimens are not browser
acceptance evidence. Only an exact, clean checkout and its own CI run qualify.
"""
import argparse
import base64
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re

import validate_directory_v4_evidence as backend
import validate_group_members_v4_evidence as membership

require, exact = backend.require, backend.exact
SOURCES = backend.SOURCES | membership.SOURCES | {
    'web/e2e/group-members-v4-empty.spec.ts',
    'web/e2e/group-members-v4-empty-helpers.ts',
    'scripts/validate_group_members_v4_empty_evidence.py',
    'scripts/test_group_members_v4_empty_evidence_contract.py',
    'scripts/test_group_members_v4_empty_runner_contract.py',
    'internal/testldapfixture/main.go', 'internal/testldapfixture/server.go',
    'internal/testldapfixture/directory.go', 'internal/testldapfixture/directory_v4_pagination.go',
    'internal/testldapfixture/directory_v4_pagination_test.go',
    'scripts/test_group_members_v4_pagination_fixture_contract.py',
    'web/e2e/group-members-v4-pagination.spec.ts', 'web/e2e/group-members-v4-pagination-helpers.ts',
    'scripts/validate_group_members_v4_pagination_evidence.py',
    'scripts/test_group_members_v4_pagination_evidence_contract.py',
    'scripts/test_group_members_v4_pagination_runner_contract.py',
    'web/src/GroupMembersV4Workspace.test.tsx',
}
PROVENANCE_FIELDS = ['checkoutCommit', 'checkoutTree', 'eventHeadCommit',
                     'eventBaseCommit', 'workflowCommit', 'runId', 'runAttempt',
                     'eventName', 'sourceDigests']
PAGE = {'pageIdx': 1, 'pageSize': 25, 'total': 0, 'totalPage': 0}


def same(actual, expected):
    # Python considers True == 1 and False == 0. Evidence must preserve types.
    return json.dumps(actual, sort_keys=True, ensure_ascii=False, allow_nan=False) == json.dumps(expected, sort_keys=True, ensure_ascii=False, allow_nan=False)


def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON key')
        result[key] = value
    return result


def load_json(raw):
    def reject_constant(value):
        raise ValueError('nonfinite JSON number: ' + value)
    return json.loads(raw, object_pairs_hook=unique, parse_constant=reject_constant)


def expected_provenance(root, environ=None):
    env = os.environ if environ is None else environ
    require(not backend.git(root, 'diff', 'HEAD', '--'), 'dirty tracked checkout')
    require(not backend.git(root, 'ls-files', '--others', '--exclude-standard', '--', 'web/src', 'web/e2e', 'internal', 'cmd', 'scripts'), 'untracked executable source')
    for path in SOURCES:
        require(backend.git(root, 'ls-files', '--error-unmatch', path) == path, 'untracked evidence source')
        require((root / path).is_file() and not (root / path).is_symlink(), 'regular evidence source')
    # Reuse the immutable backend provenance computation, not its legacy 12-group model.
    expected = backend.expected_provenance(root, env)
    expected['sourceDigests'] = {p: hashlib.sha256((root / p).read_bytes()).hexdigest() for p in sorted(SOURCES)}
    return expected


def validate_provenance(value, expected):
    exact(value, PROVENANCE_FIELDS, 'provenance fields')
    require(same(value, expected), 'checkout/event/source/run provenance mismatch')
    for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit']:
        require(type(value[key]) is str and re.fullmatch('[0-9a-f]{40}', value[key]) is not None, 'commit shape')
    require(type(value['runId']) is str and re.fullmatch(r'(local|[1-9][0-9]*)', value['runId']) is not None, 'run identity')
    require(type(value['runAttempt']) is str and re.fullmatch(r'[1-9][0-9]*', value['runAttempt']) is not None, 'run attempt')
    require(value['eventName'] in ['local', 'pull_request', 'push', 'workflow_dispatch'], 'event name')
    require(type(value['sourceDigests']) is dict and set(value['sourceDigests']) == SOURCES, 'source coverage')
    require(all(type(v) is str and re.fullmatch('[0-9a-f]{64}', v) for v in value['sourceDigests'].values()), 'source digest shape')


BASE_FIELDS = ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl']
DETAIL_MEMBERS = ['CN=page-two-only,OU=Users,DC=synthetic,DC=invalid',
                  'CN=raw\x00\n😀\u202e<script>,DC=synthetic,DC=invalid',
                  '\\u0000', 'CN=page-two-last,OU=Users,DC=synthetic,DC=invalid']


def fixture_objects():
    objects = []
    for row in range(1, 31):
        kind, number, ou = ('group', row, 'Groups') if row <= 26 else ('user', row - 26, 'Users') if row <= 28 else ('computer', row - 28, 'Computers')
        name = f'pagination-{kind}-{number:02d}'
        h = f'{row:02x}' * 16
        raw = None if row == 1 or row > 26 else [] if row == 2 else DETAIL_MEMBERS[:] if row == 26 else [f'CN=page-member-{row:02d},OU=Users,DC=synthetic,DC=invalid']
        obj = dict.fromkeys(['userAccountControl', 'objectSid', 'mail', 'description', 'whenCreated', 'primaryGroupID', 'servicePrincipalName'])
        obj.update(objectGUID=f'{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:]}',
                   distinguishedName=f'CN={name},OU={ou},DC=synthetic,DC=invalid', kind=kind,
                   objectClass=['group', 'top'] if kind == 'group' else (['computer'] if kind == 'computer' else []) + ['organizationalperson', 'person', 'top', 'user'],
                   samAccountName=name + ('$' if kind == 'computer' else ''), rawMember=raw,
                   returnedMemberCount=None if raw is None else len(raw), memberReadState='not_returned' if raw is None else 'returned')
        objects.append(obj)
    return objects


def stored_objects(objects):
    return [{'base': {k: obj[k] for k in BASE_FIELDS},
             'supplemental': dict.fromkeys(['objectSidBytes', 'mailBytes', 'descriptionBytes', 'whenCreatedBytes']),
             'identity': dict.fromkeys(['primaryGroupIDBytes', 'servicePrincipalNameBytes']),
             'membership': {'memberBytes': None if obj['rawMember'] is None else [base64.b64encode(v.encode('utf8')).decode() for v in obj['rawMember']]}}
            for obj in objects]


def validate_backend(b):
    exact(b, ['taskUUID', 'bodyBase64', 'sha256', 'dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'source', 'objects', 'storedMembership'], 'backend fields')
    require(type(b['taskUUID']) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', b['taskUUID']), 'task identity')
    require(type(b['sha256']) is str and re.fullmatch('[0-9a-f]{64}', b['sha256']), 'body digest shape')
    objects = fixture_objects()
    require(same({k: b[k] for k in ['dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'objects']}, {
        'dictionaryVersion': 4, 'count': 30, 'taskState': 'succeeded', 'purpose': 'domain.directory_read.v4',
        'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'objects': objects}), 'exact fixture objects and completed lifecycle')
    source = b['source']
    exact(source, ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'], 'source fields')
    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid' and source['domain'] == 'synthetic.invalid' and source['naming_context'] == 'dc=synthetic,dc=invalid', 'synthetic source')
    require(type(source['pages']) is int and source['pages'] == 2, 'two LDAP pages')
    require(type(source['elapsed_milliseconds']) is int and 0 <= source['elapsed_milliseconds'] <= 125000, 'elapsed milliseconds')
    times = []
    for key in ['started_at', 'completed_at']:
        require(type(source[key]) is str and re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z', source[key]), 'source timestamp')
        times.append(datetime.fromisoformat(source[key].replace('Z', '+00:00')))
    require(0 <= (times[1] - times[0]).total_seconds() <= 125, 'ordered bounded source times')
    encoded = b['bodyBase64']
    require(type(encoded) is str and 0 < len(encoded) <= 1048576, 'bounded raw body')
    raw = base64.b64decode(encoded, validate=True)
    require(base64.b64encode(raw).decode() == encoded and hashlib.sha256(raw).hexdigest() == b['sha256'], 'raw body digest')
    stored = stored_objects(objects)
    require(same(load_json(raw), {'dictionaryVersion': 4, 'objects': stored, 'source': source}), 'full stored body fixture binding')
    require(same(b['storedMembership'], [o['membership'] for o in stored]), 'stored membership bytes binding')


def screenshot_reader(directory):
    directory = Path(directory)
    require(directory.is_dir() and not directory.is_symlink(), 'screenshot directory')
    root = directory.resolve()
    def read(name):
        require(type(name) is str and re.fullmatch(r'group-members-v4-pagination-reader-(1440|390)-(discovery|next|detail|previous|size50|size25|reread)\.png', name), 'safe screenshot name')
        path = root / name
        require(path.is_file() and not path.is_symlink() and path.resolve().parent == root, 'regular contained screenshot')
        require(24 <= path.stat().st_size <= 10000000, 'bounded screenshot bytes')
        return path.read_bytes()
    return read


def validate_reader(reader, producer, ui, b):
    exact(reader, ['actorId', 'producerActorId', 'roleId', 'permissions', 'menu', 'checks', 'authentication', 'before', 'after'], 'reader fields')
    require(type(reader['actorId']) is int and reader['actorId'] > 0 and reader['actorId'] == ui['actorId'], 'reader identity')
    require(type(reader['producerActorId']) is int and reader['producerActorId'] == producer['actorId'] and reader['actorId'] != reader['producerActorId'], 'independent reader identity')
    require(type(reader['roleId']) is str and re.fullmatch('[A-Za-z0-9_-]{24}', reader['roleId']), 'custom reader role')
    membership.validate_permissions(reader['permissions'], membership.PERMISSIONS, 'reader permissions')
    membership.validate_permissions(reader['menu'], set(membership.READABLE), 'reader menu')
    require(same(reader['checks'], [*({'method': 'GET', 'path': path, 'allowed': True} for path in membership.READ_PATHS), *({'method': 'POST', 'path': path, 'allowed': False} for path in membership.WRITE_PATHS)]), 'exact reader route checks')
    require(same(reader['authentication'], {'role': 'viewer', 'hasMfa': True, 'needChangePwd': False, 'loginMfa': True}), 'real reader password and MFA authentication')
    for phase in ['before', 'after']:
        snapshot = reader[phase]
        exact(snapshot, ['actorId', 'roleId', 'baseRole', 'readable', 'writeable', 'domainIds', 'useGrants', 'taskCount', 'useCount', 'observationCount', 'observationSha256', 'mfaLastStep'], 'reader database snapshot')
        require(type(snapshot['actorId']) is int and snapshot['actorId'] == reader['actorId'] and snapshot['roleId'] == reader['roleId'] and snapshot['baseRole'] == 'viewer', 'stored reader identity')
        require(snapshot['readable'] == membership.READABLE and snapshot['writeable'] == [] and snapshot['domainIds'] == [ui['discovery']['query']['domainId']], 'exact stored reader scope')
        require(type(snapshot['useGrants']) is int and snapshot['useGrants'] == 0, 'zero reader use grants')
        require(all(type(snapshot[key]) is int and snapshot[key] > 0 for key in ['taskCount', 'useCount', 'observationCount']), 'actual stored counts')
        require(type(snapshot['mfaLastStep']) is int and snapshot['mfaLastStep'] >= 0, 'stored MFA counter')
        require(snapshot['observationSha256'] == b['sha256'], 'stored observation hash')
    require(same(reader['before'], reader['after']), 'reader reads preserve tasks/uses/observations/body/MFA')


STEPS = [('discovery', 1, 25), ('next', 2, 25), ('detail', None, None),
         ('previous', 1, 25), ('size50', 1, 50), ('size25', 1, 25), ('reread', 1, 25)]


def validate_ui(ui, b, read_screenshot):
    exact(ui, ['actorId', 'authorization', 'cases', 'writes'], 'UI fields')
    require(type(ui['actorId']) is int and ui['actorId'] > 0, 'UI actor')
    require(ui['authorization'] == 'custom_readonly_role' and ui['writes'] == [], 'read-only UI boundary')
    require(type(ui['cases']) is list and len(ui['cases']) == 2, 'two desktop/mobile cases')
    domain = ui['cases'][0]['steps'][0]['query']['domainId']
    require(type(domain) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', domain), 'query domain')
    detail_selection = None
    for case, (width, height) in zip(ui['cases'], [(1440, 900), (390, 844)]):
        exact(case, ['viewport', 'steps'], 'case fields')
        require(same(case['viewport'], {'width': width, 'height': height}), 'viewport')
        require(type(case['steps']) is list and len(case['steps']) == len(STEPS), 'complete ordered navigation steps')
        for step, (name, page, size) in zip(case['steps'], STEPS):
            exact(step, ['name', 'query', 'value', 'rendered', 'screenshot'], 'step fields')
            require(step['name'] == name, 'step order')
            q, value = step['query'], step['value']
            if name == 'detail':
                obj = b['objects'][25]
                exact(q, ['domainId', 'expectedRevision', 'expectedCredentialRevision', 'observationId', 'objectGUID'], 'detail query')
                require(same({k: q[k] for k in ['domainId', 'observationId', 'objectGUID']}, {'domainId': domain, 'observationId': b['taskUUID'], 'objectGUID': obj['objectGUID']}), 'second-page detail pin')
                require(all(type(q[k]) is str and re.fullmatch('[1-9][0-9]*', q[k]) for k in ['expectedRevision', 'expectedCredentialRevision']), 'detail revisions')
                selection = {'domainId': domain, 'revision': q['expectedRevision'], 'credentialRevision': q['expectedCredentialRevision']}
                if detail_selection is None:
                    detail_selection = selection
                require(same(selection, detail_selection), 'same selection across viewports')
                require(same(value, {'dictionaryVersion': 4, 'selection': selection, 'observationId': b['taskUUID'], 'source': b['source'], 'object': obj}), 'detail full-body API binding')
                require(same(step['rendered'], {'rawMember': [membership.display(v) for v in obj['rawMember']], 'guid': obj['objectGUID'], 'observationId': b['taskUUID'], 'returnedMemberCountText': '本次返回 member 值数：4，不代表完整成员数。'}), 'rendered second-page raw detail')
            else:
                expected_query = {'domainId': domain, 'kind': 'group', 'pageIdx': str(page), 'pageSize': str(size)}
                if name != 'discovery':
                    expected_query['observationId'] = b['taskUUID']
                require(same(q, expected_query), 'exact pinned pagination query')
                rows = b['objects'][:26][(page - 1) * size:page * size]
                total_page = (26 + size - 1) // size
                require(same(value, {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'], 'source': b['source'], 'list': rows, 'page': {'pageIdx': page, 'pageSize': size, 'total': 26, 'totalPage': total_page}}), 'API page/full-body GUID order and source binding')
                source = b['source']
                require(same(step['rendered'], {'guids': [o['objectGUID'] for o in rows], 'pagerText': f'第 {page} 页 · 共 {total_page} 页（仅本次观测中的组）', 'previousDisabled': page == 1, 'nextDisabled': page == total_page, 'pageSize': str(size), 'sourceValues': [b['taskUUID'], source['dc_host_name'], source['domain'], source['naming_context'], source['started_at'], source['completed_at']], 'detailCount': 0}), 'rendered page GUID order/pager/source')
            shot = step['screenshot']
            exact(shot, ['filename', 'sha256'], 'screenshot fields')
            require(shot['filename'] == f'group-members-v4-pagination-reader-{width}-{name}.png' and type(shot['sha256']) is str and re.fullmatch('[0-9a-f]{64}', shot['sha256']), 'screenshot name/hash')
            raw = read_screenshot(shot['filename'])
            membership.validate_png(raw, width, height)
            require(hashlib.sha256(raw).hexdigest() == shot['sha256'], 'screenshot digest')


def validate(data, provenance, read_screenshot):
    exact(data, ['format', 'provenance', 'noObservation', 'backend', 'producer', 'reader', 'ui'], 'envelope')
    require(type(data['format']) is int and data['format'] == 1, 'format')
    validate_provenance(data['provenance'], provenance)
    validate_backend(data['backend'])
    b, ui, producer = data['backend'], data['ui'], data['producer']
    exact(producer, ['actorId', 'grantRevoked', 'explicitlyGranted', 'eligible', 'v4AllowedGrants'], 'producer fields')
    require(type(producer['actorId']) is int and producer['actorId'] > 0, 'producer actor')
    require(producer['grantRevoked'] is True and producer['explicitlyGranted'] is False and producer['eligible'] is False and type(producer['v4AllowedGrants']) is int and producer['v4AllowedGrants'] == 0, 'producer v4 grant revoked before read')
    validate_ui(ui, b, read_screenshot)
    require(same(data['noObservation'], {'query': ui['cases'][0]['steps'][0]['query'], 'value': {'dictionaryVersion': 4, 'available': False, 'list': [], 'page': PAGE}}), 'same-domain preproduction unavailable observation')
    validate_reader(data['reader'], producer, {**ui, 'discovery': {'query': ui['cases'][0]['steps'][0]['query']}}, b)


def evidence_file(root):
    output = root / 'web/test-results'
    require(output.is_dir() and not output.is_symlink(), 'fixed evidence directory')
    entries = []
    # Bound traversal while scanning, reject every symlink before opening files.
    for directory, dirs, files in os.walk(output, followlinks=False):
        for name in [*dirs, *files]:
            path = Path(directory) / name
            require(not path.is_symlink(), 'non-symlink evidence')
            entries.append(path)
            require(len(entries) <= 1000, 'bounded evidence entries')
    candidates = [p for p in entries if p.name == 'group-members-v4-pagination-evidence.json']
    require(len(candidates) == 1 and candidates[0].is_file() and 0 < candidates[0].stat().st_size <= 1048576, 'one bounded pagination evidence')
    return candidates[0]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    root = parser.parse_args().source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    path = evidence_file(root)
    validate(load_json(path.read_bytes()), expected_provenance(root), screenshot_reader(path.parent))
    print('Validated exact-checkout 26-group pagination real-browser evidence')


if __name__ == '__main__':
    main()
