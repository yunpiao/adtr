"""Validate bounded real empty-asset evidence; never manufactures runtime proof."""
import argparse
import hashlib
import json
import re
import struct
from datetime import datetime
from pathlib import Path

SOURCES = {
    'web/e2e/directory-v3-controls.spec.ts', 'web/e2e/directory-v3-empty-assets.ts',
    'web/e2e/directory-v2-response-observer.ts', 'web/e2e/directory-v3-ledger-observer.ts',
    'web/e2e/safe-runtime-diagnostics.ts',
    'scripts/e2e_runtime_diagnostics.py',
    'scripts/test_safe_navigation_diagnostics_contract.py',
    'web/src/e2e-navigation.test.ts',
    'web/src/SocNavigation.test.tsx',
    'web/e2e/navigation.ts', 'web/src/UserAssetsV3Workspace.tsx',
    'web/src/ComputerAssetsV3Workspace.tsx', 'web/src/GroupAssetsV3Workspace.tsx',
    'web/src/user-assets-v3-api.ts', 'web/src/computer-assets-v3-api.ts',
    'web/src/group-assets-v3-api.ts', 'scripts/validate_empty_asset_evidence.py',
    'scripts/test_auth_e2e.py', 'scripts/ldap_e2e_fixture.py', '.github/workflows/ci.yml',
    'web/package-lock.json',
}

EXPECTED_FILES = {'directory-v3-empty-assets-evidence.json'} | {
    f'directory-v3-empty-assets-{stage}-{kind}-{width}.png'
    for stage in ['no-observation', 'observed-empty']
    for kind in ['user', 'computer', 'group'] for width in [1440, 390]
}


def require(condition, label):
    if not condition:
        raise ValueError(label)


def exact(value, keys, label):
    require(type(value) is dict and set(value) == set(keys), label)


def identifier(value):
    return type(value) is str and re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', value) is not None


def digest(value, size=64):
    return type(value) is str and re.fullmatch(r'[0-9a-f]{%d}' % size, value) is not None


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def no_symlinks(path):
    require(not any(part.is_symlink() for part in [path, *path.parents]), 'symlink path')


def discover(evidence_root, source_root):
    # The workflow entry accepts only this checkout's normal Playwright output.
    # Positional evidence paths remain available for inspecting downloaded artifacts.
    no_symlinks(source_root)
    no_symlinks(evidence_root)
    require(source_root.resolve() == Path(__file__).resolve().parents[1], 'source checkout')
    expected = source_root.resolve() / 'web' / 'test-results'
    require(evidence_root.resolve() == expected and evidence_root.is_dir(), 'fixed evidence root')
    entries = list(evidence_root.rglob('*'))
    require(len(entries) <= 1000, 'bounded evidence output')
    for entry in entries:
        no_symlinks(entry)
    candidates = [p for p in entries if p.name == 'directory-v3-empty-assets-evidence.json']
    require(len(candidates) == 1, 'unique evidence document')
    no_symlinks(candidates[0])
    require(candidates[0].resolve().is_relative_to(expected), 'evidence containment')
    for filename in EXPECTED_FILES:
        matches = [p for p in entries if p.name == filename]
        require(matches == [candidates[0].parent / filename], 'unique fixed upload files')
    return candidates[0]


def validate(data, directory, source_root):
    no_symlinks(directory)
    no_symlinks(source_root)
    exact(data, ['format', 'actorId', 'selection', 'observation', 'successfulTask',
                 'successfulUse', 'syncPosts', 'cancelPosts', 'records', 'provenance'], 'top-level fields')
    require(type(data['format']) is int and data['format'] == 1, 'format')
    actor = data['actorId']
    require(type(actor) is str and re.fullmatch(r'[1-9][0-9]*', actor), 'actor')
    selection = data['selection']
    exact(selection, ['domainId', 'revision', 'credentialRevision'], 'selection')
    require(identifier(selection['domainId']), 'domain identity')
    for field in ['revision', 'credentialRevision']:
        require(type(selection[field]) is str and re.fullmatch(r'[1-9][0-9]*', selection[field]), 'revision')
    observation = data['observation']
    exact(observation, ['observationId', 'source'], 'observation')
    task = observation['observationId']
    require(identifier(task), 'observation identity')
    source = observation['source']
    exact(source, ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at',
                   'completed_at', 'elapsed_milliseconds', 'pages'], 'source fields')
    for field, value in {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid',
                         'domain': 'synthetic.invalid', 'naming_context': 'dc=synthetic,dc=invalid'}.items():
        require(source[field] == value, 'synthetic source')
    for field in ['started_at', 'completed_at']:
        require(type(source[field]) is str and re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z', source[field]), 'timestamp')
    require(datetime.fromisoformat(source['completed_at']) >= datetime.fromisoformat(source['started_at']), 'timestamp order')
    require(integer(source['pages'], 5, 5) and integer(source['elapsed_milliseconds'], 10000, 900000), 'five real slow pages')
    require(data['successfulTask'] == {'taskUUID': task, 'state': 'succeeded', 'attempt': 1, 'progress': 100}, 'successful worker')
    require(data['successfulUse'] == {'state': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'taskState': 'succeeded'}, 'executor witness')
    require(integer(data['syncPosts'], 2, 2) and integer(data['cancelPosts'], 1, 1), 'original write budget')
    records = data['records']
    require(type(records) is list and len(records) == 6, 'six workspace transitions')
    expected_order = [(stage, kind) for stage in ['no-observation', 'observed-empty'] for kind in ['user', 'computer', 'group']]
    require([(record.get('stage'), record.get('kind')) for record in records] == expected_order, 'workspace order')
    for record in records:
        exact(record, ['stage', 'kind', 'path', 'query', 'actorId', 'value', 'consumed', 'screenshots', 'pinned'], 'record fields')
        kind, stage = record['kind'], record['stage']
        available = stage == 'observed-empty'
        require(record['path'] == f'/api/{kind}-assets/v3' and record['actorId'] == actor, 'actual API identity')
        query = record['query']
        require(type(query) is dict and set(query) <= {'domainId', 'expectedRevision', 'expectedCredentialRevision', 'search', 'pageIdx', 'pageSize'}, 'unfiltered query fields')
        require({key: query.get(key) for key in ['domainId', 'expectedRevision', 'expectedCredentialRevision', 'pageIdx', 'pageSize']} == {
            'domainId': selection['domainId'], 'expectedRevision': selection['revision'],
            'expectedCredentialRevision': selection['credentialRevision'], 'pageIdx': '1', 'pageSize': '10'}, 'query pins')
        require(query.get('search', '') == '', 'no filtering')
        expected = {'dictionaryVersion': 3, 'selection': selection, 'available': available, 'list': [],
                    'page': {'pageIdx': 1, 'pageSize': 10, 'total': 0, 'totalPage': 0}}
        if kind != 'user':
            expected['observedComputerCount' if kind == 'computer' else 'observedGroupCount'] = 0 if available else None
        if available:
            expected.update(observation)
        # JSON-normalized exact equality rejects extra/private fields and type aliases.
        require(json.dumps(record['value'], sort_keys=True) == json.dumps(expected, sort_keys=True), 'closed empty response')
        pinned = record['pinned']
        if available:
            exact(pinned, ['query', 'value', 'sha256', 'byteLength'], 'pinned read fields')
            require(pinned['query'] == {**query, 'observationId': task}, 'exact explicit observation pin')
            require(json.dumps(pinned['value'], sort_keys=True) == json.dumps(expected, sort_keys=True), 'pinned empty response')
            require(digest(pinned['sha256']) and integer(pinned['byteLength'], 1, 16384), 'pinned response digest')
        else:
            require(pinned is None, 'absent observation cannot be pinned')
        consumed = record['consumed']
        exact(consumed, ['byteLength', 'sha256', 'state', 'uncanceledEOF', 'eof', 'status', 'actor', 'cache', 'readers', 'released', 'signalAborted', 'cancelCalls'], 'consumption fields')
        require(integer(consumed['byteLength'], 1, 16384) and digest(consumed['sha256']), 'bounded response digest')
        require(consumed['state'] == 'complete' and consumed['uncanceledEOF'] is True and consumed['eof'] is True and consumed['signalAborted'] is False, 'native complete response')
        require(integer(consumed['status'], 200, 200) and consumed['actor'] == actor and consumed['cache'] == 'no-store', 'response headers')
        require(integer(consumed['readers'], 1, 1) and integer(consumed['released'], 1, 1) and integer(consumed['cancelCalls'], 0, 0), 'released native reader')
        screenshots = record['screenshots']
        require(type(screenshots) is list and len(screenshots) == 2, 'two screenshots')
        for shot, (width, height) in zip(screenshots, [(1440, 900), (390, 844)]):
            exact(shot, ['filename', 'viewport', 'geometry', 'sha256'], 'screenshot fields')
            filename = f'directory-v3-empty-assets-{stage}-{kind}-{width}.png'
            require(shot['filename'] == filename, 'safe screenshot filename')
            require(shot['viewport'] == {'width': width, 'height': height}, 'viewport')
            exact(shot['geometry'], ['width', 'scroll'], 'geometry fields')
            require(shot['geometry']['width'] == width and integer(shot['geometry']['scroll'], width, width + 1), 'no viewport overflow')
            path = directory / filename
            require(not path.is_symlink() and path.is_file() and 24 <= path.stat().st_size <= 4 * 1024 * 1024, 'bounded PNG')
            content = path.read_bytes()
            require(content[:16] == b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR', 'PNG signature')
            actual_width, actual_height = struct.unpack('>II', content[16:24])
            require(actual_width == width and height <= actual_height <= 8192, 'PNG dimensions')
            require(digest(shot['sha256']) and hashlib.sha256(content).hexdigest() == shot['sha256'], 'PNG hash')
    provenance = data['provenance']
    exact(provenance, ['checkoutCommit', 'checkoutTree', 'checkoutParents', 'dirty', 'eventHeadCommit', 'eventBaseCommit', 'workflowCommit', 'runID', 'runAttempt', 'sourceSHA256'], 'provenance fields')
    require(digest(provenance['checkoutCommit'], 40) and digest(provenance['checkoutTree'], 40), 'checkout identity')
    parents = provenance['checkoutParents']
    require(type(parents) is list and 1 <= len(parents) <= 2 and all(digest(p, 40) for p in parents), 'checkout parents')
    require(provenance['dirty'] is False, 'clean checkout')
    for field in ['eventHeadCommit', 'eventBaseCommit', 'workflowCommit']:
        require(provenance[field] is None or digest(provenance[field], 40), 'event commit')
    for field in ['runID', 'runAttempt']:
        require(provenance[field] is None or type(provenance[field]) is str and re.fullmatch(r'[1-9][0-9]*', provenance[field]), 'run identity')
    hashes = provenance['sourceSHA256']
    exact(hashes, SOURCES, 'source set')
    for filename, sha in hashes.items():
        require(digest(sha), 'source digest')
        no_symlinks(source_root / filename)
        require(hashlib.sha256((source_root / filename).read_bytes()).hexdigest() == sha, 'source hash mismatch')


def load(path):
    no_symlinks(path)
    require(not path.is_symlink() and path.stat().st_size <= 128 * 1024, 'bounded evidence JSON')
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, 'duplicate JSON key')
            result[key] = value
        return result
    return json.loads(path.read_text(), object_pairs_hook=unique)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('evidence', type=Path, nargs='?')
    parser.add_argument('--evidence-root', type=Path)
    parser.add_argument('--source-root', type=Path, required=True)
    args = parser.parse_args()
    require((args.evidence is None) != (args.evidence_root is None), 'choose one evidence input')
    evidence = args.evidence if args.evidence is not None else discover(args.evidence_root, args.source_root)
    validate(load(evidence), evidence.parent, args.source_root)
    print('Empty-asset evidence and 12 screenshot/source hashes verified; visual review remains separate.')
