"""Fail-closed nonempty-to-empty refresh real-browser evidence.

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
    'internal/testldapfixture/directory.go', 'internal/testldapfixture/directory_v2.go',
    'internal/directoryassets/objects.go', 'internal/directoryassets/objects_v2.go', 'internal/directoryassets/objects_v3.go',
    'web/e2e/group-members-v4-refresh.spec.ts', 'web/e2e/group-members-v4-refresh-helpers.ts',
    'scripts/validate_group_members_v4_refresh_evidence.py',
    'scripts/test_group_members_v4_refresh_evidence_contract.py',
    'scripts/test_group_members_v4_refresh_runner_contract.py',
    'scripts/test_group_members_v4_refresh_fixture_contract.py',
    'scripts/test_group_members_v4_refresh_budget_contract.py',
    'internal/auth/service.go', 'internal/auth/credential_use_http.go', 'internal/auth/directory_http.go',
    'web/src/GroupMembersV4Workspace.test.tsx',
    'internal/testldapfixture/empty_transition.go', 'internal/testldapfixture/empty_transition_linux.go',
    'internal/testldapfixture/empty_transition_other.go', 'internal/testldapfixture/empty_transition_test.go',
    'internal/testldapfixture/empty_transition_config_test.go', 'internal/testldapfixture/directory_v4_refresh_test.go',
    'web/e2e/asset-empty-transition-marker.ts', 'web/src/asset-empty-transition-marker.test.ts',
}
PROVENANCE_FIELDS = ['checkoutCommit', 'checkoutTree', 'eventHeadCommit',
                     'eventBaseCommit', 'workflowCommit', 'runId', 'runAttempt',
                     'eventName', 'sourceDigests']
PAGE = {'pageIdx': 1, 'pageSize': 25, 'total': 0, 'totalPage': 0}
UNKNOWN_TEXT = '本页观测未返回组对象；成员情况未知。'
PAGER_TEXT = '第 1 页 · 共 0 页（仅本次观测中的组）'


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
    # Reuse the immutable backend provenance computation, not its 30-row model.
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
DETAIL_MEMBERS = ['CN=user-01,OU=Users,DC=synthetic,DC=invalid',
                  'CN=group-02,OU=Groups,DC=synthetic,DC=invalid',
                  'CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid',
                  'CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid']


def fixture_objects():
    """Independent exact truth for the fixed standard TLS LDAP fixture."""
    objects = []
    for row in range(1, 31):
        kind, number, ou = ('user', row, 'Users') if row <= 12 else ('group', row - 12, 'Groups') if row <= 24 else ('computer', row - 24, 'Computers')
        name, h = f'{kind}-{number:02d}', f'{row:02x}' * 16
        raw = None if row <= 13 or row > 24 else [] if row == 14 else DETAIL_MEMBERS[:] if row == 15 else [f'CN=member-{row:02d},OU=Users,DC=synthetic,DC=invalid']
        identity = row < 12 or row > 24
        objects.append({
            'objectGUID': f'{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:]}',
            'distinguishedName': f'CN={name},OU={ou},DC=synthetic,DC=invalid',
            'kind': kind,
            'objectClass': ['group', 'top'] if kind == 'group' else (['computer'] if kind == 'computer' else []) + ['organizationalperson', 'person', 'top', 'user'],
            'samAccountName': None if row == 24 else name + ('$' if kind == 'computer' else ''),
            'userAccountControl': (0 if row == 1 else 512) if kind == 'user' else 4096 if kind == 'computer' else None,
            'objectSid': None if row == 24 else f'S-1-5-21-1-2-3-{1000 + row}',
            'mail': None if row == 24 else ' Mixed@example.test ' if row == 1 else f'row-{row:02d}@example.test',
            'description': None if row == 24 else ['Line 1\x00\n\t😀\u202e'] if row == 1 else [f'Synthetic row {row:02d}'],
            'whenCreated': None if row == 24 else '0001-01-01T00:00:00Z' if row == 1 else '2026-10-01T02:03:04Z',
            'primaryGroupID': (0 if row == 2 else 4294967295 if row == 3 else 513) if identity else None,
            'servicePrincipalName': (['HOST/Z', 'host/a\x00\n😀<script>', 'HOST/a'] if row == 1 else [f'HOST/row-{row:02d}.synthetic.invalid', f'HTTP/row-{row:02d}.synthetic.invalid']) if identity else None,
            'rawMember': raw, 'returnedMemberCount': None if raw is None else len(raw),
            'memberReadState': 'not_returned' if raw is None else 'returned',
        })
    return objects


def encoded(value):
    return base64.b64encode(value if type(value) is bytes else value.encode('utf8')).decode('ascii')


def stored_objects():
    """Full fixed storage truth, including supplemental/identity byte encodings."""
    import struct
    result = []
    for row, obj in enumerate(fixture_objects(), 1):
        supplemental = dict.fromkeys(['objectSidBytes', 'mailBytes', 'descriptionBytes', 'whenCreatedBytes'])
        if row != 24:
            supplemental = {
                'objectSidBytes': encoded(bytes([1, 5, 0, 0, 0, 0, 0, 5]) + struct.pack('<IIIII', 21, 1, 2, 3, 1000 + row)),
                'mailBytes': encoded(obj['mail']),
                'descriptionBytes': [encoded(v) for v in obj['description']],
                'whenCreatedBytes': encoded('00010101000000.0Z' if row == 1 else '20261001020304.0Z'),
            }
        result.append({'base': {k: obj[k] for k in BASE_FIELDS}, 'supplemental': supplemental,
            'identity': {'primaryGroupIDBytes': None if obj['primaryGroupID'] is None else encoded(str(obj['primaryGroupID'])),
                         'servicePrincipalNameBytes': None if obj['servicePrincipalName'] is None else [encoded(v) for v in obj['servicePrincipalName']]},
            'membership': {'memberBytes': None if obj['rawMember'] is None else [encoded(v) for v in obj['rawMember']]}})
    return result


def timestamp(value):
    require(type(value) is str and re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z', value), 'source timestamp')
    # Preserve nanoseconds when ordering two successive observations.
    from decimal import Decimal
    moment = datetime.fromisoformat(value.replace('Z', '+00:00'))
    seconds = int(moment.replace(microsecond=0).timestamp())
    fraction = value.split('.', 1)[1][:-1] if '.' in value else '0'
    return Decimal(seconds) + Decimal('0.' + fraction)


def validate_backend(b, empty):
    exact(b, ['taskUUID', 'bodyBase64', 'sha256', 'dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'source', 'objects'], 'backend fields')
    require(type(b['taskUUID']) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', b['taskUUID']), 'task identity')
    require(type(b['sha256']) is str and re.fullmatch('[0-9a-f]{64}', b['sha256']), 'body digest shape')
    objects = [] if empty else fixture_objects()
    require(same({k: b[k] for k in ['dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'objects']}, {
        'dictionaryVersion': 4, 'count': len(objects), 'taskState': 'succeeded',
        'purpose': 'domain.directory_read.v4', 'useState': 'quiesced',
        'reason': 'executor_returned', 'dependencies': 0, 'objects': objects,
    }), 'fixed fixture truth and completed quiesced lifecycle')
    source = b['source']
    exact(source, ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'], 'source fields')
    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid' and source['domain'] == 'synthetic.invalid' and source['naming_context'] == 'dc=synthetic,dc=invalid', 'synthetic source')
    require(type(source['pages']) is int and source['pages'] == (1 if empty else 2), 'exact LDAP pages')
    require(type(source['elapsed_milliseconds']) is int and 0 <= source['elapsed_milliseconds'] <= 125000, 'elapsed milliseconds')
    require(0 <= timestamp(source['completed_at']) - timestamp(source['started_at']) <= 125, 'ordered bounded source times')
    encoded_body = b['bodyBase64']
    require(type(encoded_body) is str and 0 < len(encoded_body) <= 1048576, 'bounded raw body')
    raw = base64.b64decode(encoded_body, validate=True)
    require(base64.b64encode(raw).decode() == encoded_body and hashlib.sha256(raw).hexdigest() == b['sha256'], 'raw body digest')
    require(same(load_json(raw), {'dictionaryVersion': 4, 'objects': [] if empty else stored_objects(), 'source': source}), 'full stored fixture/source binding')


def screenshot_reader(directory):
    directory = Path(directory)
    require(directory.is_dir() and not directory.is_symlink(), 'screenshot directory')
    root = directory.resolve()
    def read(name):
        require(type(name) is str and re.fullmatch(r'group-members-v4-refresh-reader-(1440-initial|1440|390)\.png', name), 'safe screenshot name')
        path = root / name
        require(path.is_file() and not path.is_symlink() and path.resolve().parent == root, 'regular contained screenshot')
        require(24 <= path.stat().st_size <= 10000000, 'bounded screenshot bytes')
        return path.read_bytes()
    return read


def validate_reader(reader, producer, ui, old, b):
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
        exact(snapshot, ['actorId', 'roleId', 'baseRole', 'readable', 'writeable', 'domainIds', 'useGrants', 'taskCount', 'useCount', 'observationCount', 'observationSha256', 'oldObservationSha256', 'mfaLastStep'], 'reader database snapshot')
        require(type(snapshot['actorId']) is int and snapshot['actorId'] == reader['actorId'] and snapshot['roleId'] == reader['roleId'] and snapshot['baseRole'] == 'viewer', 'stored reader identity')
        require(snapshot['readable'] == membership.READABLE and snapshot['writeable'] == [] and snapshot['domainIds'] == [ui['initialDiscovery']['query']['domainId']], 'exact stored reader scope')
        require(type(snapshot['useGrants']) is int and snapshot['useGrants'] == 0, 'zero reader use grants')
        require(all(type(snapshot[key]) is int and snapshot[key] > 0 for key in ['taskCount', 'useCount', 'observationCount']), 'actual stored counts')
        require(type(snapshot['mfaLastStep']) is int and snapshot['mfaLastStep'] >= 0, 'stored MFA counter')
        require(snapshot['observationSha256'] == b['sha256'] and snapshot['oldObservationSha256'] == old['sha256'], 'both stored observation hashes')
        require(type(snapshot['observationCount']) is int and snapshot['observationCount'] == 2, 'two stored observations')
    require(same(reader['before'], reader['after']), 'reader reads preserve tasks/uses/observations/body/MFA')


STEPS = ['pinned-reread', 'old-detail-reopen', 'rediscover-empty', 'empty-reread', 'rediscover-repeat', 'return-empty']


def list_value(b, empty):
    return {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'],
            'source': b['source'], 'list': [] if empty else fixture_objects()[12:24],
            'page': PAGE if empty else {'pageIdx': 1, 'pageSize': 25, 'total': 12, 'totalPage': 1}}


def rendered(b, empty, detail=False):
    source = b['source']
    return {'observationId': b['taskUUID'], 'groupCount': 0 if empty else 12,
            'detailCount': 1 if detail else 0,
            'rawMember': [membership.display(v) for v in DETAIL_MEMBERS] if detail else [],
            'sourceValues': [b['taskUUID'], source['dc_host_name'], source['domain'], source['naming_context'], source['started_at'], source['completed_at']],
            'pagerText': PAGER_TEXT if empty else '第 1 页 · 共 1 页（仅本次观测中的组）',
            'previousDisabled': True, 'nextDisabled': True}


def validate_shot(shot, filename, width, height, read_screenshot):
    exact(shot, ['filename', 'sha256'], 'screenshot fields')
    require(shot['filename'] == filename and type(shot['sha256']) is str and re.fullmatch('[0-9a-f]{64}', shot['sha256']), 'screenshot name/hash')
    raw = read_screenshot(shot['filename'])
    membership.validate_png(raw, width, height)
    require(hashlib.sha256(raw).hexdigest() == shot['sha256'], 'screenshot digest')


def validate_ui(ui, old, new, read_screenshot):
    exact(ui, ['actorId', 'authorization', 'initialDiscovery', 'initialDetail', 'initialRendered', 'initialScreenshot', 'retained', 'steps', 'clearStates', 'cases', 'writes', 'detailRequests'], 'UI fields')
    require(type(ui['actorId']) is int and ui['actorId'] > 0, 'UI actor')
    require(ui['authorization'] == 'custom_readonly_role' and ui['writes'] == [], 'read-only UI boundary')
    discovery = ui['initialDiscovery']
    exact(discovery, ['query', 'value'], 'initial discovery fields')
    query = discovery['query']
    exact(query, ['domainId', 'kind', 'pageIdx', 'pageSize'], 'discovery query')
    require(type(query['domainId']) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', query['domainId']), 'query domain')
    require(same(query, {'domainId': query['domainId'], 'kind': 'group', 'pageIdx': '1', 'pageSize': '25'}), 'initial unpinned discovery')
    require(same(discovery['value'], list_value(old, False)), 'old complete list binding')
    detail = ui['initialDetail']
    exact(detail, ['query', 'value'], 'detail response fields')
    q = detail['query']
    exact(q, ['domainId', 'expectedRevision', 'expectedCredentialRevision', 'observationId', 'objectGUID'], 'detail query')
    require(all(type(q[k]) is str and re.fullmatch('[1-9][0-9]*', q[k]) for k in ['expectedRevision', 'expectedCredentialRevision']), 'selection revisions')
    require(same(ui['detailRequests'], [{'query': q}, {'query': q}]), 'only initial and reopened old detail requests')
    obj = fixture_objects()[14]
    require(same(q, {'domainId': query['domainId'], 'expectedRevision': q['expectedRevision'], 'expectedCredentialRevision': q['expectedCredentialRevision'], 'observationId': old['taskUUID'], 'objectGUID': obj['objectGUID']}), 'old selected detail identity')
    require(same(detail['value'], {'dictionaryVersion': 4, 'selection': {'domainId': query['domainId'], 'revision': q['expectedRevision'], 'credentialRevision': q['expectedCredentialRevision']}, 'observationId': old['taskUUID'], 'source': old['source'], 'object': obj}), 'old detail full fixture/source binding')
    for key in ['initialRendered', 'retained']:
        require(same(ui[key], rendered(old, False, True)), 'old detail retained until explicit reread')
    validate_shot(ui['initialScreenshot'], 'group-members-v4-refresh-reader-1440-initial.png', 1440, 900, read_screenshot)
    require(same(ui['clearStates'], [{'detailCount': 0, 'sourceCount': 0, 'listCount': 0}] * 2), 'both source resets clear old DOM')
    require(type(ui['steps']) is list and len(ui['steps']) == len(STEPS), 'complete ordered refresh steps')
    for step, name in zip(ui['steps'], STEPS):
        exact(step, ['name', 'query', 'value', 'rendered'], 'refresh step fields')
        require(step['name'] == name, 'refresh step order')
        if name == 'old-detail-reopen':
            require(same({k: step[k] for k in ['query', 'value']}, detail), 'reopened old detail pin')
            expected = rendered(old, False, True)
        else:
            empty = name != 'pinned-reread'
            observation = new if empty else old
            expected_query = {**query}
            if name in ['pinned-reread', 'empty-reread']:
                expected_query['observationId'] = observation['taskUUID']
            require(same(step['query'], expected_query), 'explicit discovery versus retained pin')
            require(same(step['value'], list_value(observation, empty)), 'step exact API/body/source binding')
            expected = rendered(observation, empty)
        require(same(step['rendered'], expected), 'step DOM/pin/detail/source binding')
    require(type(ui['cases']) is list and len(ui['cases']) == 2, 'two final desktop/mobile cases')
    for case, (width, height) in zip(ui['cases'], [(1440, 900), (390, 844)]):
        exact(case, ['viewport', 'rendered', 'screenshot'], 'case fields')
        require(same(case['viewport'], {'width': width, 'height': height}), 'viewport')
        require(same(case['rendered'], {**rendered(new, True), 'unknownText': UNKNOWN_TEXT}), 'final empty source/pager/unknown/detail truth')
        validate_shot(case['screenshot'], f'group-members-v4-refresh-reader-{width}.png', width, height, read_screenshot)


def validate(data, provenance, read_screenshot):
    exact(data, ['format', 'provenance', 'noObservation', 'backend', 'emptyBackend', 'producer', 'reader', 'ui'], 'envelope')
    require(type(data['format']) is int and data['format'] == 1, 'format')
    validate_provenance(data['provenance'], provenance)
    old, new = data['backend'], data['emptyBackend']
    validate_backend(old, False)
    validate_backend(new, True)
    require(old['taskUUID'] != new['taskUUID'] and old['sha256'] != new['sha256'], 'distinct real observations')
    require(timestamp(old['source']['completed_at']) <= timestamp(new['source']['started_at']), 'old completes before new starts')
    producer, ui = data['producer'], data['ui']
    exact(producer, ['actorId', 'grantRevoked', 'explicitlyGranted', 'eligible', 'v4AllowedGrants'], 'producer fields')
    require(type(producer['actorId']) is int and producer['actorId'] > 0, 'producer actor')
    require(producer['grantRevoked'] is True and producer['explicitlyGranted'] is False and producer['eligible'] is False and type(producer['v4AllowedGrants']) is int and producer['v4AllowedGrants'] == 0, 'producer v4 grant revoked before read')
    validate_ui(ui, old, new, read_screenshot)
    require(same(data['noObservation'], {'query': ui['initialDiscovery']['query'], 'value': {'dictionaryVersion': 4, 'available': False, 'list': [], 'page': PAGE}}), 'same-domain preproduction unavailable observation')
    validate_reader(data['reader'], producer, ui, old, new)


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
    candidates = [p for p in entries if p.name == 'group-members-v4-refresh-evidence.json']
    require(len(candidates) == 1 and candidates[0].is_file() and 0 < candidates[0].stat().st_size <= 1048576, 'one bounded refresh evidence')
    return candidates[0]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    root = parser.parse_args().source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    path = evidence_file(root)
    validate(load_json(path.read_bytes()), expected_provenance(root), screenshot_reader(path.parent))
    print('Validated exact-checkout nonempty-to-empty refresh real-browser evidence')


if __name__ == '__main__':
    main()
