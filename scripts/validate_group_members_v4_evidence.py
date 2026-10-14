"""Fail-closed real-browser membership evidence for the registered primary suite.

This validates artifacts from the actual suite; unit-test specimens are not E2E
acceptance evidence. It binds UI/API facts to stored bytes and exact sources.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import unicodedata
import zlib
import validate_directory_v4_evidence as backend

SOURCES = {
    'internal/auth/access_types.go', 'internal/auth/access_http.go', 'internal/auth/resource_types.go',
    'web/e2e/group-members-v4-response-observer.ts', 'web/src/GroupMembersV4Observer.test.ts',
    'web/e2e/group-members-v4.spec.ts', 'web/e2e/group-members-v4-helpers.ts',
    'scripts/validate_group_members_v4_evidence.py', 'scripts/test_group_members_v4_evidence_contract.py',
    'web/e2e/safe-runtime-diagnostics.ts',
    'scripts/e2e_runtime_diagnostics.py',
    'scripts/test_safe_navigation_diagnostics_contract.py',
    'web/src/e2e-navigation.test.ts',
    'web/src/SocNavigation.test.tsx',
    'web/e2e/navigation.ts', 'web/test-helpers/navigation.ts', 'web/playwright.config.ts',
    'web/src/App.tsx', 'web/src/SocNavigation.tsx', 'web/src/GroupMembersV4Workspace.tsx',
    'web/src/group-members-v4-api.ts', 'web/src/group-members-v4-read.ts', 'web/src/directory-v4-facts.ts',
    'web/src/directory-v3-facts.ts', 'web/src/directory-v2-api.ts', 'web/src/SavedSourcePicker.tsx',
    'web/src/directory-v2-json.ts', 'web/e2e/directory-v2-response-observer.ts',
    'web/src/domain-selection-api.ts', 'web/src/style.css', 'web/src/soc-shell.css',
    'web/package-lock.json', 'internal/auth/group_members_v4_http.go', 'internal/domains/group_members_v4.go',
}
require, exact = backend.require, backend.exact


def display(raw):
    return ''.join(('\\u%04x' % ord(c) if ord(c) <= 65535 else '\\u{%x}' % ord(c))
                   if unicodedata.category(c) in ('Cc', 'Cf') or c in '\u2028\u2029' else c
                   for c in raw.replace('\\', '\\\\'))


def expected_ui(root, provenance):
    require(not backend.git(root, 'diff', 'HEAD', '--'), 'dirty tracked checkout')
    require(not backend.git(root, 'ls-files', '--others', '--exclude-standard', '--', 'web/src', 'web/e2e', 'internal', 'cmd', 'scripts'), 'untracked executable source')
    for path in SOURCES:
        require(backend.git(root, 'ls-files', '--error-unmatch', path) == path, 'untracked evidence source')
    require(not backend.git(root, 'diff', 'HEAD', '--', *sorted(SOURCES)), 'modified UI evidence source')
    return {**{k: provenance[k] for k in ['checkoutCommit', 'checkoutTree', 'runId', 'runAttempt']},
            'sourceDigests': {p: hashlib.sha256((root / p).read_bytes()).hexdigest() for p in SOURCES}}


def validate(data, provenance, ui_provenance, read_screenshot):
    exact(data, ['format', 'backend', 'uiProvenance', 'noObservation', 'ui', 'readerUi', 'reader'], 'envelope')
    require(type(data['format']) is int and data['format'] == 1, 'format')
    backend.validate(data['backend'], provenance)
    require(data['uiProvenance'] == ui_provenance, 'UI source/checkout/run mismatch')
    require(set(ui_provenance['sourceDigests']) == SOURCES and all(re.fullmatch('[0-9a-f]{64}', v) for v in ui_provenance['sourceDigests'].values()), 'source coverage')
    b, ui = data['backend'], data['ui']
    exact(data['noObservation'], ['query', 'value'], 'no-observation response')
    empty = data['noObservation']['value']
    exact(empty, ['dictionaryVersion', 'available', 'list', 'page'], 'no-observation fields')
    require(type(empty['dictionaryVersion']) is int and empty['dictionaryVersion'] == 4 and empty['available'] is False and empty['list'] == [] and empty['page'] == {'pageIdx': 1, 'pageSize': 25, 'total': 0, 'totalPage': 0}, 'no-observation truth')
    require(data['noObservation']['query'] == ui['discovery']['query'], 'same no-observation domain')
    validate_ui(b, ui, read_screenshot, 'platform_admin_after_v4_use_revoke', '')
    validate_ui(b, data['readerUi'], read_screenshot, 'custom_readonly_role', 'reader-')
    require(data['readerUi']['discovery'] == ui['discovery'] and data['readerUi']['reread'] == ui['reread'], 'same reader/producer observation')
    validate_reader(data, ui_provenance)


READABLE = ['directory_assets', 'domains']
PERMISSIONS = {'users', 'roles', 'permissions', 'tasks', 'audit', 'audit_exports',
               'system', 'schedules', 'task_archive', 'domains', 'operation_accounts',
               'system_logs', 'directory_assets', 'user_asset_exports', 'group_asset_exports'}
READ_PATHS = ['/api/domain-selection', '/api/domain-selection/resolve',
              '/api/directory/v4/observation', '/api/group-assets/v4/members']
WRITE_PATHS = ['/api/directory/v4/sync', '/api/directory/v4/cancel',
               '/api/directory-credential-use/v4/grant', '/api/directory-credential-use/v4/revoke']


def validate_permissions(entries, marks, label):
    require(type(entries) is list and len(entries) == len(marks), label)
    seen = set()
    for entry in entries:
        exact(entry, ['mark', 'auth'], label + ' fields')
        require(type(entry['mark']) is str and entry['mark'] in marks and entry['mark'] not in seen, label + ' marks')
        seen.add(entry['mark'])
        exact(entry['auth'], ['readable', 'writeable'], label + ' auth')
        require(entry['auth']['readable'] is (entry['mark'] in READABLE) and entry['auth']['writeable'] is False, label + ' authority')
    require(seen == set(marks), label + ' complete')


def validate_reader(data, ui_provenance):
    reader, ui, b = data['reader'], data['readerUi'], data['backend']
    exact(reader, ['actorId', 'producerActorId', 'roleId', 'permissions', 'menu', 'checks', 'provenance', 'before', 'after', 'denied'], 'reader fields')
    require(type(reader['actorId']) is int and reader['actorId'] > 0 and reader['actorId'] == ui['actorId'], 'reader identity')
    require(type(reader['producerActorId']) is int and reader['producerActorId'] == data['ui']['actorId'] and reader['actorId'] != reader['producerActorId'], 'independent reader actor')
    require(type(reader['roleId']) is str and re.fullmatch('[A-Za-z0-9_-]{24}', reader['roleId']) is not None, 'custom reader role')
    require(reader['provenance'] == ui_provenance, 'reader exact checkout/source/run')
    validate_permissions(reader['permissions'], PERMISSIONS, 'reader permissions')
    validate_permissions(reader['menu'], set(READABLE), 'reader menu')
    require(reader['checks'] == [*({'method': 'GET', 'path': path, 'allowed': True} for path in READ_PATHS), *({'method': 'POST', 'path': path, 'allowed': False} for path in WRITE_PATHS)], 'reader route checks')
    require(type(reader['checks']) is list and all(type(check.get('allowed')) is bool for check in reader['checks']), 'boolean route checks')
    require(type(reader['denied']) is list and all(type(probe.get('status')) is int for probe in reader['denied']), 'integer probe statuses')
    require(reader['denied'] == [{'method': 'POST', 'path': path, 'status': 403, 'value': {'error': 'forbidden'}} for path in WRITE_PATHS], 'direct denied probes')
    for phase in ['before', 'after']:
        snapshot = reader[phase]
        exact(snapshot, ['actorId', 'roleId', 'baseRole', 'readable', 'writeable', 'domainIds', 'useGrants', 'taskCount', 'useCount', 'observationCount', 'observationSha256', 'mfaLastStep'], 'reader database snapshot')
        require(type(snapshot['actorId']) is int and snapshot['actorId'] == reader['actorId'] and snapshot['roleId'] == reader['roleId'] and snapshot['baseRole'] == 'viewer', 'stored reader identity/role')
        require(snapshot['readable'] == READABLE and snapshot['writeable'] == [] and snapshot['domainIds'] == [ui['discovery']['query']['domainId']], 'exact stored reader permission/scope')
        require(type(snapshot['useGrants']) is int and snapshot['useGrants'] == 0, 'zero reader credential-use grants')
        require(all(type(snapshot[key]) is int and snapshot[key] > 0 for key in ['taskCount', 'useCount', 'observationCount']), 'actual stored counts')
        require(snapshot['observationSha256'] == b['sha256'], 'same stored reader observation body')
        require(type(snapshot['mfaLastStep']) is int and snapshot['mfaLastStep'] >= 0, 'stored reader MFA counter')
    require(reader['before'] == reader['after'], 'reads and denied probes preserve storage')


def validate_ui(b, ui, read_screenshot, authorization, filename_prefix):
    exact(ui, ['actorId', 'authorization', 'discovery', 'reread', 'cases', 'writes'], 'UI fields')
    require(type(ui['actorId']) is int and ui['actorId'] > 0, 'actor')
    require(ui['authorization'] == authorization and ui['writes'] == [], 'read-only interaction boundary')
    for response in [ui['discovery'], ui['reread']]:
        exact(response, ['query', 'value'], 'response fields')
    discovery, reread = ui['discovery'], ui['reread']
    query = discovery['query']
    exact(query, ['domainId', 'kind', 'pageIdx', 'pageSize'], 'discovery query')
    require(query['kind'] == 'group' and query['pageIdx'] == '1' and query['pageSize'] == '25' and re.fullmatch('[A-Za-z0-9_.-]{1,128}', query['domainId']), 'discovery selection')
    require(reread['query'] == {**query, 'observationId': b['taskUUID']} and reread['value'] == discovery['value'], 'pinned reread')
    groups = [o for o in b['objects'] if o['kind'] == 'group']
    require(discovery['value'] == {'dictionaryVersion': 4, 'available': True, 'observationId': b['taskUUID'], 'source': b['source'], 'list': groups, 'page': {'pageIdx': 1, 'pageSize': 25, 'total': 12, 'totalPage': 1}}, 'UI/API/stored list')
    require(type(ui['cases']) is list and len(ui['cases']) == 6, 'six actual desktop/mobile cases')
    for case, (width, height, index) in zip(ui['cases'], [(w, h, i) for w, h in [(1440, 900), (390, 844)] for i in [12, 13, 14]]):
        exact(case, ['viewport', 'reply', 'rendered', 'screenshot'], 'case fields')
        require(case['viewport'] == {'width': width, 'height': height}, 'viewport')
        obj = b['objects'][index]
        reply = case['reply']
        exact(reply, ['query', 'value'], 'detail response')
        q, v = reply['query'], reply['value']
        exact(q, ['domainId', 'expectedRevision', 'expectedCredentialRevision', 'observationId', 'objectGUID'], 'pinned query')
        require(q['domainId'] == query['domainId'] and q['observationId'] == b['taskUUID'] and q['objectGUID'] == obj['objectGUID'], 'pinned identity')
        require(all(re.fullmatch('[1-9][0-9]*', q[k]) for k in ['expectedRevision', 'expectedCredentialRevision']), 'revisions')
        exact(v, ['dictionaryVersion', 'selection', 'observationId', 'source', 'object'], 'detail fields')
        require(v['dictionaryVersion'] == 4 and v['object'] == obj and v['source'] == b['source'] and v['observationId'] == b['taskUUID'], 'detail stored identity/source/facts')
        exact(v['selection'], ['domainId', 'revision', 'credentialRevision'], 'selection fields')
        require(v['selection']['domainId'] == q['domainId'] and v['selection']['revision'] == q['expectedRevision'] and v['selection']['credentialRevision'] == q['expectedCredentialRevision'], 'selection binding')
        require(case['rendered'] == [display(x) for x in obj['rawMember'] or []], 'ordered escaped UI facts')
        shot = case['screenshot']
        exact(shot, ['filename', 'sha256'], 'screenshot fields')
        require(shot['filename'] == f'group-members-v4-{filename_prefix}{width}-{index}.png' and re.fullmatch('[0-9a-f]{64}', shot['sha256']), 'screenshot name/hash')
        raw = read_screenshot(shot['filename'])
        require(24 <= len(raw) <= 10000000 and raw[:16] == b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR' and struct.unpack('>II', raw[16:24]) == (width, height), 'actual PNG dimensions')
        validate_png(raw, width, height)
        require(hashlib.sha256(raw).hexdigest() == shot['sha256'], 'screenshot digest')


def validate_png(raw, width, height):
    require(24 <= len(raw) <= 10000000 and raw[:8] == b'\x89PNG\r\n\x1a\n', 'PNG signature/size')
    offset, chunks, compressed, color = 8, [], bytearray(), None
    while offset < len(raw):
        require(offset + 12 <= len(raw), 'PNG chunk header')
        length = struct.unpack('>I', raw[offset:offset + 4])[0]
        kind = raw[offset + 4:offset + 8]
        require(offset + 12 + length <= len(raw), 'PNG chunk bounds')
        payload = raw[offset + 8:offset + 8 + length]
        crc = struct.unpack('>I', raw[offset + 8 + length:offset + 12 + length])[0]
        require(zlib.crc32(kind + payload) == crc, 'PNG CRC')
        chunks.append(kind)
        if kind == b'IHDR':
            require(len(chunks) == 1 and length == 13, 'PNG IHDR')
            w, h, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', payload)
            require((w, h) == (width, height) and depth == 8 and color in (2, 6) and compression == filtering == interlace == 0, 'PNG actual dimensions/format')
        elif kind == b'IDAT':
            compressed.extend(payload)
        elif kind == b'IEND':
            require(length == 0 and offset + 12 == len(raw), 'PNG end')
        offset += 12 + length
    require(chunks and chunks[0] == b'IHDR' and chunks[-1] == b'IEND' and compressed and color in (2, 6), 'complete PNG')
    row = width * (3 if color == 2 else 4) + 1
    decoder = zlib.decompressobj()
    pixels = decoder.decompress(bytes(compressed), row * height + 1)
    require(len(pixels) == row * height and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail, 'PNG decoded bounds')
    require(all(pixels[i] <= 4 for i in range(0, len(pixels), row)), 'PNG scanline filters')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-root', type=Path, default=Path('.'))
    root = parser.parse_args().source_root.resolve()
    require(root == Path(__file__).resolve().parents[1], 'fixed checkout')
    output = root / 'web/test-results'
    require(output.is_dir() and not output.is_symlink(), 'evidence directory')
    entries = list(output.rglob('*'))
    require(len(entries) <= 1000 and not any(p.is_symlink() for p in entries), 'bounded non-symlink evidence')
    files = [p for p in entries if p.name == 'group-members-v4-evidence.json']
    require(len(files) == 1 and files[0].is_file() and files[0].stat().st_size <= 1048576, 'one bounded evidence')
    provenance = backend.expected_provenance(root)
    validate(json.loads(files[0].read_text()), provenance, expected_ui(root, provenance), lambda name: (files[0].parent / name).read_bytes())
    print('Validated exact-checkout group-members-v4 real-browser evidence')


if __name__ == '__main__':
    main()
