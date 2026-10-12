"""Validate real same-source old-pin/empty-refresh evidence, never synthesize proof."""
import argparse
import base64
import binascii
import hashlib
import json
from datetime import datetime
from pathlib import Path
import re
import struct
import subprocess

from validate_empty_asset_evidence import digest, exact, identifier, integer, no_symlinks, require

SOURCES = {
    'internal/testldapfixture/empty_transition_linux.go',
    'internal/testldapfixture/empty_transition_other.go',
    'internal/domains/group_assets_v3.go',
    'internal/domains/computer_assets_v3.go',
    'internal/domains/directory_v3_observation_codec.go',
    'internal/domains/directory_v2_observation_codec.go',

    'web/e2e/asset-empty-refresh.ts', 'web/e2e/asset-empty-transition-marker.ts', 'web/e2e/group-assets-v3.spec.ts',
    'web/e2e/computer-assets-v3.spec.ts', 'web/e2e/group-assets-v3-fixture.ts',
    'web/e2e/computer-assets-v3-fixture.ts', 'web/e2e/directory-v2-response-observer.ts',
    'web/src/GroupAssetsV3Workspace.tsx', 'web/src/ComputerAssetsV3Workspace.tsx',
    'web/src/group-assets-v3-api.ts', 'web/src/computer-assets-v3-api.ts',
    'internal/testldapfixture/main.go', 'internal/testldapfixture/server.go',
    'internal/testldapfixture/directory.go', 'internal/testldapfixture/empty_transition.go',
    'scripts/ldap_e2e_fixture.py', 'scripts/test_auth_e2e.py',
    'scripts/validate_asset_empty_refresh.py', 'scripts/validate_empty_asset_evidence.py',
    'web/package-lock.json', '.github/workflows/ci.yml',
}
SOURCE_FIELDS = ['server_name', 'dc_host_name', 'domain', 'naming_context',
                 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages']
PIN_FIELDS = ['accountId', 'accountCredentialRevision', 'policyRevision', 'grantRoleId', 'grantRevision']
ROW_FIELDS = ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName',
              'userAccountControl', 'objectSid', 'mail', 'description', 'whenCreated',
              'primaryGroupID', 'servicePrincipalName']


def same(a, b, label):
    require(json.dumps(a, sort_keys=True, ensure_ascii=True) == json.dumps(b, sort_keys=True, ensure_ascii=True), label)


def revision(value):
    return type(value) is str and re.fullmatch(r'[1-9][0-9]*', value) is not None


def git(root, *args):
    return subprocess.check_output(['git', *args], cwd=root, text=True, timeout=5).strip()


def validate_provenance(p, root):
    exact(p, ['checkoutCommit', 'checkoutTree', 'checkoutParents', 'dirty', 'eventHeadCommit',
              'eventBaseCommit', 'workflowCommit', 'runID', 'runAttempt', 'sourceSHA256'], 'provenance fields')
    require(p['dirty'] is False, 'clean evidence checkout')
    require(git(root, 'status', '--porcelain') == '', 'current clean checkout')
    require(p['checkoutCommit'] == git(root, 'rev-parse', 'HEAD'), 'checkout commit')
    require(p['checkoutTree'] == git(root, 'rev-parse', 'HEAD^{tree}'), 'checkout tree')
    parents = [line[7:] for line in git(root, 'cat-file', '-p', 'HEAD').split('\n\n', 1)[0].splitlines()
               if re.fullmatch(r'parent [0-9a-f]{40}', line)]
    require(p['checkoutParents'] == parents, 'raw checkout parents')
    for field in ['eventHeadCommit', 'eventBaseCommit', 'workflowCommit']:
        require(p[field] is None or digest(p[field], 40), 'event identity')
    for field in ['runID', 'runAttempt']:
        require(p[field] is None or revision(p[field]), 'run identity')
    exact(p['sourceSHA256'], SOURCES, 'source set')
    for filename, sha in p['sourceSHA256'].items():
        no_symlinks(root / filename)
        require(digest(sha) and hashlib.sha256((root / filename).read_bytes()).hexdigest() == sha, 'source digest')


def validate(data, directory, root, kind):
    require(kind in {'group', 'computer'}, 'kind')
    no_symlinks(directory)
    no_symlinks(root)
    exact(data, ['format', 'kind', 'actorId', 'selection', 'request', 'firstStored', 'oldStored',
                 'emptyStored', 'oldBefore', 'oldPinned', 'oldSearch', 'oldCleared', 'latest', 'refreshed', 'emptyPinned',
                 'historyFirst', 'historySecond', 'screenshots', 'provenance'], 'evidence fields')
    require(type(data['format']) is int and data['format'] == 1 and data['kind'] == kind, 'format and kind')
    require(revision(data['actorId']), 'actor')
    selection = data['selection']
    exact(selection, ['domainId', 'revision', 'credentialRevision'], 'selection')
    require(identifier(selection['domainId']) and revision(selection['revision']) and revision(selection['credentialRevision']), 'selection identity')
    count_key = 'observedGroupCount' if kind == 'group' else 'observedComputerCount'
    source_identity = {'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid',
                       'domain': 'synthetic.invalid', 'naming_context': 'dc=synthetic,dc=invalid'}

    def stored(p, count):
        exact(p, ['taskUUID', 'actorId', 'selection', 'sourcePins', 'bodyBase64', 'sha256',
                  'dictionaryVersion', 'count', 'taskState', 'taskName', 'payloadVersion', 'maxAttempts', 'attempt', 'purpose', 'useState', 'reason', 'dependencies'], 'stored fields')
        require(identifier(p['taskUUID']) and p['actorId'] == data['actorId'], 'stored task actor')
        same(p['selection'], selection, 'stored selection')
        exact(p['sourcePins'], PIN_FIELDS, 'stored source pins')
        pins = p['sourcePins']
        require(identifier(pins['accountId']) and identifier(pins['grantRoleId']) and revision(pins['accountCredentialRevision'])
                and revision(pins['grantRevision']) and digest(pins['policyRevision']), 'stored source identity')
        same({key: p[key] for key in ['dictionaryVersion', 'count', 'taskState', 'taskName', 'payloadVersion', 'maxAttempts', 'attempt', 'purpose', 'useState', 'reason', 'dependencies']},
             {'dictionaryVersion': 3, 'count': count, 'taskState': 'succeeded', 'taskName': 'domain.directory_read.v3', 'payloadVersion': 1, 'maxAttempts': 1, 'attempt': 1, 'purpose': 'domain.directory_read.v3',
              'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0}, 'successful stored observation')
        require(type(p['bodyBase64']) is str and len(p['bodyBase64']) <= 100000, 'stored body budget')
        try:
            body = base64.b64decode(p['bodyBase64'], validate=True)
        except (binascii.Error, ValueError):
            raise ValueError('stored base64') from None
        require(base64.b64encode(body).decode() == p['bodyBase64'], 'canonical base64')
        require(digest(p['sha256']) and hashlib.sha256(body).hexdigest() == p['sha256'], 'stored body digest')
        envelope = json.loads(body, object_pairs_hook=unique)
        exact(envelope, ['dictionaryVersion', 'objects', 'source'], 'stored envelope')
        require(type(envelope['dictionaryVersion']) is int and envelope['dictionaryVersion'] == 3, 'stored dictionary')
        require(type(envelope['objects']) is list and len(envelope['objects']) == count, 'stored object count')
        source = envelope['source']
        exact(source, SOURCE_FIELDS, 'source fields')
        same({key: source[key] for key in source_identity}, source_identity, 'synthetic source')
        require(integer(source['pages'], 1 if count == 0 else 2, 1 if count == 0 else 2), 'wire page count')
        require(integer(source['elapsed_milliseconds'], 0, 30000), 'elapsed bound')
        for field in ['started_at', 'completed_at']:
            require(type(source[field]) is str and re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z', source[field]), 'source timestamp')
        require(datetime.fromisoformat(source['completed_at']) >= datetime.fromisoformat(source['started_at']), 'source chronology')
        if count == 0:
            canonical = '{"dictionaryVersion":3,"objects":[],"source":' + json.dumps(
                {key: source[key] for key in SOURCE_FIELDS}, separators=(',', ':')) + '}'
            require(body == canonical.encode(), 'canonical empty bytes')
        return envelope

    first, old, empty = [stored(data[name], count) for name, count in [('firstStored', 30), ('oldStored', 30), ('emptyStored', 0)]]
    first_id, old_id, empty_id = [data[name]['taskUUID'] for name in ['firstStored', 'oldStored', 'emptyStored']]
    require(len({first_id, old_id, empty_id}) == 3, 'three distinct observations')
    require(datetime.fromisoformat(empty['source']['started_at']) >= datetime.fromisoformat(old['source']['completed_at']) >= datetime.fromisoformat(first['source']['completed_at']), 'newer successful collection')
    same(data['firstStored']['sourcePins'], data['oldStored']['sourcePins'], 'unchanged first source pins')
    same(data['emptyStored']['sourcePins'], data['oldStored']['sourcePins'], 'unchanged empty source pins')
    same(data['request'], {'domainId': selection['domainId'], 'expectedRevision': selection['revision'],
         'expectedCredentialGeneration': selection['credentialRevision'], 'idempotencyKey': f'{kind}-third-empty-real-wire'}, 'third sync request')
    search_sam = f'{kind}-22' + ('$' if kind == 'computer' else '')
    base = {'domainId': selection['domainId'], 'expectedRevision': selection['revision'],
            'expectedCredentialRevision': selection['credentialRevision'], 'pageIdx': '1', 'pageSize': '10'}

    def record(name, task, body, page=1, size=10, pin=True, search=""):
        r = data[name]
        native = name in {'oldPinned', 'oldSearch', 'oldCleared', 'refreshed', 'emptyPinned'}
        exact(r, ['query', 'value', 'consumed'] if native else ['query', 'value'], 'read record')
        if native:
            consumed = r['consumed']
            exact(consumed, ['byteLength', 'sha256', 'state', 'uncanceledEOF', 'eof', 'status', 'actor', 'cache', 'readers', 'released', 'signalAborted', 'cancelCalls'], 'consumption fields')
            require(integer(consumed['byteLength'], 1, 65536) and digest(consumed['sha256']), 'native body digest')
            same({k: v for k, v in consumed.items() if k not in {'byteLength', 'sha256'}}, {'state': 'complete', 'uncanceledEOF': True, 'eof': True, 'status': 200, 'actor': data['actorId'], 'cache': 'no-store', 'readers': 1, 'released': 1, 'signalAborted': False, 'cancelCalls': 0}, 'complete native consumption')
        query = {**base, 'pageIdx': str(page), 'pageSize': str(size)}
        if pin:
            query['observationId'] = task
        if search:
            query['search'] = search
        # Blank UI search and omitted API search are equivalent. Only the explicit
        # oldSearch record may carry the single fixed keyword predicate.
        q = dict(r['query'])
        if q.get('search') == '':
            q.pop('search')
        same(q, query, 'exact query and observation pin')
        value = r['value']
        exact(value, ['dictionaryVersion', 'selection', 'available', 'observationId', 'source', count_key, 'list', 'page'], 'response fields')
        rows = [o['base'] for o in body['objects'] if o['base']['kind'] == kind]
        require(len(rows) in {0, 23}, 'real observed kind count')
        filtered = [row for row in rows if row['samAccountName'] == search_sam] if search else rows
        same({key: value[key] for key in value if key != 'list'},
             {'dictionaryVersion': 3, 'selection': selection, 'available': True, 'observationId': task,
              'source': body['source'], count_key: len(rows), 'page': {'pageIdx': page, 'pageSize': size,
              'total': len(filtered), 'totalPage': (len(filtered) + size - 1) // size}}, 'exact observation response')
        wanted = filtered[(page - 1) * size:page * size]
        require(type(value['list']) is list and len(value['list']) == len(wanted), 'response row count')
        for row, expected in zip(value['list'], wanted):
            exact(row, ROW_FIELDS, 'row fields')
            same({key: row[key] for key in expected}, expected, 'stored base facts')
        return value

    before = record('oldBefore', old_id, old, size=50)
    record('oldPinned', old_id, old, page=2)
    searched = record('oldSearch', old_id, old, search=f'{kind}-22')
    require(len(searched['list']) == 1, 'one exact old search match')
    same(searched['list'][0], next(row for row in before['list'] if row['samAccountName'] == search_sam), 'unchanged full search facts')
    same(record('oldCleared', old_id, old)['list'], before['list'][:10], 'cleared old pin facts')
    for name in ['latest', 'refreshed']:
        record(name, empty_id, empty, pin=False)
    record('emptyPinned', empty_id, empty)
    record('historyFirst', first_id, first, size=50)
    same(record('historySecond', old_id, old, size=50), before, 'historical second observation immutable')
    same(data['historyFirst']['value']['list'], before['list'], 'both original nonempty row sets retained')
    shots = data['screenshots']
    require(type(shots) is list and len(shots) == 4, 'four screenshots')
    for shot, (stage, width, height, task) in zip(shots, [
            ('old-pinned', 1440, 900, old_id), ('old-pinned', 390, 844, old_id),
            ('refreshed-empty', 1440, 900, empty_id), ('refreshed-empty', 390, 844, empty_id)]):
        exact(shot, ['stage', 'observationId', 'filename', 'viewport', 'geometry', 'sha256'], 'screenshot fields')
        filename = f'{kind}-assets-v3-empty-refresh-{stage}-{width}.png'
        require(shot['stage'] == stage and shot['observationId'] == task and shot['filename'] == filename, 'screenshot binding')
        same(shot['viewport'], {'width': width, 'height': height}, 'viewport')
        exact(shot['geometry'], ['width', 'scroll'], 'geometry')
        require(shot['geometry']['width'] == width and integer(shot['geometry']['scroll'], width, width + 1), 'viewport overflow')
        path = directory / filename
        no_symlinks(path)
        require(path.is_file() and 24 <= path.stat().st_size <= 4 * 1024 * 1024, 'bounded PNG')
        content = path.read_bytes()
        require(content[:16] == b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR', 'PNG signature')
        w, h = struct.unpack('>II', content[16:24])
        require(w == width and height <= h <= 8192, 'PNG dimensions')
        require(digest(shot['sha256']) and hashlib.sha256(content).hexdigest() == shot['sha256'], 'PNG digest')
    validate_provenance(data['provenance'], root)


def discover(evidence_root, source_root, kind):
    no_symlinks(evidence_root)
    no_symlinks(source_root)
    require(source_root.resolve() == Path(__file__).resolve().parents[1], 'source checkout')
    require(evidence_root.resolve() == source_root.resolve() / 'web/test-results', 'fixed evidence root')
    entries = list(evidence_root.rglob('*'))
    require(len(entries) <= 1000, 'bounded evidence directory')
    for entry in entries:
        no_symlinks(entry)
    name = f'{kind}-assets-v3-empty-refresh-evidence.json'
    matches = [p for p in entries if p.name == name]
    require(len(matches) == 1, 'unique evidence document')
    for stage in ['old-pinned', 'refreshed-empty']:
        for width in [1440, 390]:
            name = f'{kind}-assets-v3-empty-refresh-{stage}-{width}.png'
            require([p for p in entries if p.name == name] == [matches[0].parent / name], 'unique screenshot')
    return matches[0]


def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON key')
        result[key] = value
    return result


def load(path):
    no_symlinks(path)
    require(path.is_file() and path.stat().st_size <= 512 * 1024, 'bounded evidence JSON')
    return json.loads(path.read_text(), object_pairs_hook=unique)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('evidence', type=Path, nargs='?')
    parser.add_argument('--evidence-root', type=Path)
    parser.add_argument('--source-root', type=Path, required=True)
    parser.add_argument('--kind', choices=['group', 'computer'], required=True)
    args = parser.parse_args()
    require((args.evidence is None) != (args.evidence_root is None), 'choose one evidence input')
    path = args.evidence if args.evidence is not None else discover(args.evidence_root, args.source_root, args.kind)
    validate(load(path), path.parent, args.source_root, args.kind)
    print('Same-source historical pins and successful empty refresh evidence verified; visual review remains separate.')
