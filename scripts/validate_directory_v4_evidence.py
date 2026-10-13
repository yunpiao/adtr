"""Validate dictionary4 backend evidence against this checkout and CI event."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

SOURCES = {
    '.github/workflows/ci.yml', 'scripts/test_auth_e2e.py', 'scripts/ldap_e2e_fixture.py',
    'scripts/validate_directory_v4_evidence.py', 'web/e2e/directory-v4-backend.spec.ts',
    'internal/testldapfixture/directory_v4.go', 'internal/directoryassets/objects_v4.go',
    'internal/ldapconnection/directory_protocol_v4.go', 'internal/ldapconnection/directory_v4.go',
    'internal/domains/directory_v4_observation_codec.go', 'internal/domains/directory_observation_v4_schema.go',
    'internal/domains/group_members_v4.go', 'internal/auth/group_members_v4_http.go',
    'internal/store/directory_v4_migration.go', 'internal/domainconfig/config.go',
}


def require(value, label):
    if not value:
        raise ValueError(label)


def exact(value, keys, label):
    require(type(value) is dict and set(value) == set(keys), label)


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True, timeout=10).strip()


def expected_provenance(root, environ=None):
    env = os.environ if environ is None else environ
    checkout = git(root, 'rev-parse', 'HEAD')
    tree = git(root, 'rev-parse', 'HEAD^{tree}')
    require(not git(root, 'diff', 'HEAD', '--', *sorted(SOURCES)), 'modified evidence sources')
    if env.get('GITHUB_ACTIONS') == 'true':
        for key in ['GITHUB_SHA', 'ADTR_EVENT_HEAD_SHA', 'ADTR_EVENT_BASE_SHA', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT', 'GITHUB_EVENT_NAME']:
            require(bool(env.get(key)), 'missing CI provenance ' + key)
    return {'checkoutCommit': checkout, 'checkoutTree': tree,
            'eventHeadCommit': env.get('ADTR_EVENT_HEAD_SHA', checkout),
            'eventBaseCommit': env.get('ADTR_EVENT_BASE_SHA') or git(root, 'rev-parse', 'HEAD^'),
            'workflowCommit': env.get('GITHUB_SHA', checkout), 'runId': env.get('GITHUB_RUN_ID', 'local'),
            'runAttempt': env.get('GITHUB_RUN_ATTEMPT', '1'), 'eventName': env.get('GITHUB_EVENT_NAME', 'local'),
            'sourceDigests': {path: hashlib.sha256((root / path).read_bytes()).hexdigest() for path in sorted(SOURCES)}}


def validate(data, expected):
    exact(data, ['format', 'taskUUID', 'sha256', 'bodyBase64', 'source', 'purpose', 'reason', 'dependencies', 'objects', 'storedIdentity', 'storedMembership', 'provenance'], 'evidence fields')
    require(type(data['format']) is int and data['format'] == 1, 'format')
    exact(data['provenance'], expected.keys(), 'provenance fields')
    require(data['provenance'] == expected, 'checkout/event/source provenance mismatch')
    for key in ['checkoutCommit', 'checkoutTree', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit']:
        require(re.fullmatch(r'[0-9a-f]{40}', expected[key]) is not None, 'commit shape')
    require(re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', data['taskUUID']) is not None, 'task identity')
    require(re.fullmatch(r'[0-9a-f]{64}', data['sha256']) is not None, 'observation digest')
    require(data['purpose'] == 'domain.directory_read.v4' and data['reason'] == 'executor_returned' and type(data['dependencies']) is int and data['dependencies'] == 0, 'lifecycle evidence')
    source = data['source']
    exact(source, ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'], 'source fields')
    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid' and source['domain'] == 'synthetic.invalid' and source['naming_context'].lower() == 'dc=synthetic,dc=invalid' and source['pages'] == 2, 'synthetic source')
    import base64
    encoded = data['bodyBase64']
    require(type(encoded) is str and len(encoded) <= 4 * ((16777216 + 2) // 3), 'bounded stored body')
    raw_body = base64.b64decode(encoded, validate=True)
    require(base64.b64encode(raw_body).decode() == encoded and hashlib.sha256(raw_body).hexdigest() == data['sha256'], 'stored body digest')
    def unique(pairs):
        value = {}
        for key, item in pairs:
            require(key not in value, 'duplicate stored key')
            value[key] = item
        return value
    body = json.loads(raw_body, object_pairs_hook=unique)
    exact(body, ['dictionaryVersion', 'objects', 'source'], 'stored envelope')
    require(type(body['dictionaryVersion']) is int and body['dictionaryVersion'] == 4 and body['source'] == source, 'stored version/source')
    require(type(body['objects']) is list and len(body['objects']) == 30, 'stored object count')
    objects, members, identities = data['objects'], data['storedMembership'], data['storedIdentity']
    require(all(type(v) is list and len(v) == 30 for v in [objects, members, identities]), 'complete observations')
    require(len({o['objectGUID'] for o in objects}) == 30, 'unique GUIDs')
    for i, (obj, member, identity) in enumerate(zip(objects, members, identities), 1):
        exact(obj, ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl', 'objectSid', 'mail', 'description', 'whenCreated', 'primaryGroupID', 'servicePrincipalName', 'rawMember', 'returnedMemberCount', 'memberReadState'], 'public object fields')
        exact(member, ['memberBytes'], 'stored membership fields')
        exact(identity, ['primaryGroupIDBytes', 'servicePrincipalNameBytes'], 'stored identity fields')
        stored = body['objects'][i - 1]
        exact(stored, ['base', 'supplemental', 'identity', 'membership'], 'stored object fields')
        require(stored['membership'] == member and stored['identity'] == identity, 'body/extracted sections mismatch')
        exact(stored['base'], ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl'], 'stored base fields')
        require(all(obj[key] == value for key, value in stored['base'].items()), 'body/API base facts mismatch')
        primary = identity['primaryGroupIDBytes']
        spns = identity['servicePrincipalNameBytes']
        require(obj['primaryGroupID'] == (None if primary is None else int(base64.b64decode(primary, validate=True))) and
                obj['servicePrincipalName'] == (None if spns is None else [base64.b64decode(v, validate=True).decode('utf8') for v in spns]), 'body/API identity facts mismatch')
        raw = member['memberBytes']
        if raw is None:
            require(obj['rawMember'] is None and obj['returnedMemberCount'] is None and obj['memberReadState'] == 'not_returned', 'missing member state')
        else:
            require(type(raw) is list and len(raw) <= 256 and obj['kind'] == 'group', 'member profile')
            decoded = [base64.b64decode(v, validate=True).decode('utf8') for v in raw]
            require(obj['rawMember'] == decoded and type(obj['returnedMemberCount']) is int and obj['returnedMemberCount'] == len(decoded) and obj['memberReadState'] == 'returned', 'member values/count/state')
        expected = None if i <= 13 or i > 24 else [] if i == 14 else [
            'CN=user-01,OU=Users,DC=synthetic,DC=invalid', 'CN=group-02,OU=Groups,DC=synthetic,DC=invalid',
            'CN=S-1-5-21-foreign,CN=ForeignSecurityPrincipals,DC=elsewhere,DC=invalid', 'CN=raw\x00\n😀<script>,DC=synthetic,DC=invalid',
        ] if i == 15 else [f'CN=member-{i:02d},OU=Users,DC=synthetic,DC=invalid']
        require(obj['rawMember'] == expected, 'fixture membership truth')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    args = parser.parse_args()
    root = args.source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    output = root / 'web' / 'test-results'
    require(output.is_dir() and not output.is_symlink(), 'fixed evidence root')
    entries = list(output.rglob('*'))
    require(len(entries) <= 1000 and not any(p.is_symlink() for p in entries), 'bounded nonsymlink output')
    candidates = [p for p in entries if p.name == 'directory-v4-backend-evidence.json']
    require(len(candidates) == 1 and candidates[0].is_file() and candidates[0].stat().st_size <= 1048576, 'unique bounded evidence')
    validate(json.loads(candidates[0].read_text()), expected_provenance(root))
    print('Validated dictionary4 backend evidence for exact checkout/event/source files')


if __name__ == '__main__':
    main()
