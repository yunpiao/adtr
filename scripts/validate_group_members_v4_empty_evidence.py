"""Fail-closed zero-dictionary / zero-group real-browser evidence.

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


def validate_backend(b):
    exact(b, ['taskUUID', 'bodyBase64', 'sha256', 'dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'source', 'objects'], 'backend fields')
    require(type(b['taskUUID']) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', b['taskUUID']) is not None, 'task identity')
    require(type(b['sha256']) is str and re.fullmatch('[0-9a-f]{64}', b['sha256']) is not None, 'body digest shape')
    require(same({k: b[k] for k in ['dictionaryVersion', 'count', 'taskState', 'purpose', 'useState', 'reason', 'dependencies', 'objects']}, {
        'dictionaryVersion': 4, 'count': 0, 'taskState': 'succeeded',
        'purpose': 'domain.directory_read.v4', 'useState': 'quiesced',
        'reason': 'executor_returned', 'dependencies': 0, 'objects': [],
    }), 'zero dictionary and completed quiesced lifecycle')
    source = b['source']
    exact(source, ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'], 'source fields')
    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid' and source['domain'] == 'synthetic.invalid' and source['naming_context'] == 'dc=synthetic,dc=invalid', 'synthetic source')
    require(type(source['pages']) is int and source['pages'] == 1, 'one LDAP page')
    require(type(source['elapsed_milliseconds']) is int and source['elapsed_milliseconds'] >= 0, 'elapsed milliseconds')
    times = []
    for key in ['started_at', 'completed_at']:
        require(type(source[key]) is str and re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z', source[key]), 'source timestamp')
        times.append(datetime.fromisoformat(source[key].replace('Z', '+00:00')))
    require(times[0] <= times[1], 'ordered source times')
    encoded = b['bodyBase64']
    require(type(encoded) is str and 0 < len(encoded) <= 1048576, 'bounded raw body')
    raw = base64.b64decode(encoded, validate=True)
    require(base64.b64encode(raw).decode() == encoded and hashlib.sha256(raw).hexdigest() == b['sha256'], 'raw body digest')
    body = load_json(raw)
    require(same(body, {'dictionaryVersion': 4, 'objects': [], 'source': source}), 'raw zero dictionary/source binding')


def screenshot_reader(directory):
    directory = Path(directory)
    require(directory.is_dir() and not directory.is_symlink(), 'screenshot directory')
    root = directory.resolve()
    def read(name):
        require(type(name) is str and re.fullmatch(r'group-members-v4-empty-reader-(1440|390)\.png', name), 'safe screenshot name')
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


def validate_ui(ui, b, read_screenshot):
    exact(ui, ['actorId', 'authorization', 'discovery', 'reread', 'cases', 'writes', 'detailRequests'], 'UI fields')
    require(type(ui['actorId']) is int and ui['actorId'] > 0, 'UI actor')
    require(ui['authorization'] == 'custom_readonly_role' and ui['writes'] == [] and ui['detailRequests'] == [], 'read-only empty UI boundary')
    for response in [ui['discovery'], ui['reread']]:
        exact(response, ['query', 'value'], 'API response fields')
    query = ui['discovery']['query']
    exact(query, ['domainId', 'kind', 'pageIdx', 'pageSize'], 'discovery query')
    require(type(query['domainId']) is str and re.fullmatch('[A-Za-z0-9_.-]{1,128}', query['domainId']), 'query domain')
    require(query['kind'] == 'group' and query['pageIdx'] == '1' and query['pageSize'] == '25', 'group page query')
    result = {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'], 'source': b['source'], 'list': [], 'page': PAGE}
    require(same(ui['discovery']['value'], result), 'empty available API/body/source binding')
    require(same(ui['reread'], {'query': {**query, 'observationId': b['taskUUID']}, 'value': result}), 'same pinned empty reread')
    require(type(ui['cases']) is list and len(ui['cases']) == 2, 'two actual desktop/mobile cases')
    for case, (width, height) in zip(ui['cases'], [(1440, 900), (390, 844)]):
        exact(case, ['viewport', 'rendered', 'screenshot'], 'case fields')
        require(same(case['viewport'], {'width': width, 'height': height}), 'viewport')
        source = b['source']
        require(same(case['rendered'], {'unknownText': UNKNOWN_TEXT, 'pagerText': PAGER_TEXT, 'previousDisabled': True, 'nextDisabled': True, 'groupCount': 0, 'detailCount': 0,
            'sourceValues': [b['taskUUID'], source['dc_host_name'], source['domain'], source['naming_context'], source['started_at'], source['completed_at']]}), 'rendered empty truth/source/pager/detail')
        shot = case['screenshot']
        exact(shot, ['filename', 'sha256'], 'screenshot fields')
        require(shot['filename'] == f'group-members-v4-empty-reader-{width}.png' and type(shot['sha256']) is str and re.fullmatch('[0-9a-f]{64}', shot['sha256']), 'screenshot name/hash')
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
    require(same(data['noObservation'], {'query': ui['discovery']['query'], 'value': {'dictionaryVersion': 4, 'available': False, 'list': [], 'page': PAGE}}), 'same-domain preproduction unavailable observation')
    validate_reader(data['reader'], producer, ui, b)


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
    candidates = [p for p in entries if p.name == 'group-members-v4-empty-evidence.json']
    require(len(candidates) == 1 and candidates[0].is_file() and 0 < candidates[0].stat().st_size <= 1048576, 'one bounded empty evidence')
    return candidates[0]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    root = parser.parse_args().source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    path = evidence_file(root)
    validate(load_json(path.read_bytes()), expected_provenance(root), screenshot_reader(path.parent))
    print('Validated exact-checkout zero-dictionary/zero-group real-browser evidence')


if __name__ == '__main__':
    main()
