"""Independent bounded multi-chunk evidence admission. Never repairs input bytes.

Synthetic unit specimens establish rejection behavior, not runtime capacity. The
actual source bytea is the cell oracle; exporter summaries are only cross-checks.
"""
import argparse
import base64
from datetime import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
import struct
import subprocess
import sys
import zlib

from validate_computer_asset_export_v3_evidence import (checkout_provenance,
    SOURCE_ROOTS, matched_rows, workbook_rows, validate_png)
from e2e_runtime_diagnostics import validate_report

SUITE = 'computer-asset-export-v3-multichunk'
FILES = ('source-observation.json', 'database-export.xlsx', 'download.xlsx',
         'ready.png', 'evidence.json', 'provenance.json', 'resources.json')
LIMITS = dict(zip(FILES, (16 << 20, 32 << 20, 32 << 20, 10 << 20, 1 << 20, 2 << 20, 96 << 10)))
HEADERS = ['GUID', 'SAM', 'SID', 'DN', 'whenCreated']
COLUMNS = ['guid', 'sAMAccountName', 'sid', 'dn', 'whenCreated']
LIMITATIONS = ['not_export_only', 'rss_approximate', 'hwm_process_lifetime', 'not_whole_system', 'not_product_slo']
ERROR = 'multichunk_evidence_rejected'


def require(condition):
    if not condition:
        raise ValueError(ERROR)


def exact(value, fields):
    require(type(value) is dict and set(value) == set(fields.split() if type(fields) is str else fields))


def number(value, low=0, high=(1 << 53) - 1):
    require(type(value) is int and low <= value <= high)
    return value


def decimal(value, low=1, high=(1 << 63) - 1):
    require(type(value) is str and re.fullmatch(r'0|[1-9][0-9]{0,18}', value) is not None)
    require(low <= int(value) <= high)
    return int(value)


def pattern(value, expression):
    require(type(value) is str and re.fullmatch(expression, value) is not None)
    return value


def uuid(value):
    return pattern(value, r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}')


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def sha(value):
    return pattern(value, r'[0-9a-f]{64}')


def encoded(value):
    return json.dumps(value, ensure_ascii=True, separators=(',', ':'), allow_nan=False).encode('ascii')


def strict_json(raw, limit):
    require(type(raw) is bytes and 0 < len(raw) <= limit)
    text = raw.decode('utf-8', 'strict')
    depth, quoted, escaped = 0, False, False
    for char in text:
        if quoted:
            if escaped:
                escaped = False
            elif char == '\\':
                escaped = True
            elif char == '"':
                quoted = False
        elif char == '"':
            quoted = True
        elif char in '[{':
            depth += 1
            require(depth <= 64)
        elif char in ']}':
            depth -= 1
            require(depth >= 0)
    require(depth == 0 and not quoted)
    def unique(pairs):
        value = {}
        for key, child in pairs:
            require(key not in value)
            value[key] = child
        return value
    return json.loads(text, object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError(ERROR)))


def safe_path(path):
    path = Path(path).absolute()
    require(all(not part.is_symlink() for part in (path, *path.parents)))
    return path


def read_file(path, limit):
    path = safe_path(path)
    with path.open('rb') as source:
        require(stat.S_ISREG(os.fstat(source.fileno()).st_mode))
        raw = source.read(limit + 1)
    require(0 < len(raw) <= limit)
    return raw


def file_ref(value, name, raw):
    exact(value, 'filename byteCount sha256')
    require(value['filename'] == name)
    number(value['byteCount'], 1, LIMITS[name])
    sha(value['sha256'])
    require(value['byteCount'] == len(raw) and value['sha256'] == digest(raw))


def provenance_shape(value):
    exact(value, 'checkoutCommit checkoutTree runID runAttempt sourceSHA256')
    for key in ('checkoutCommit', 'checkoutTree'):
        pattern(value[key], r'[0-9a-f]{40}')
    for key in ('runID', 'runAttempt'):
        decimal(value[key])
    require(type(value['sourceSHA256']) is dict and 1 <= len(value['sourceSHA256']) <= 10000)
    require(REQUIRED_SOURCES <= set(value['sourceSHA256']))
    for path, value_hash in value['sourceSHA256'].items():
        require(type(path) is str and 0 < len(path) <= 256 and not path.startswith('/')
                and all(part not in ('', '.', '..') for part in path.split('/'))
                and '\\' not in path and re.fullmatch(r'[A-Za-z0-9_.\-/]+', path))
        sha(value_hash)


def capture_provenance(root):
    root = safe_path(root)
    value = checkout_provenance(root)
    # The old complete source roots are retained, not replaced by a short list.
    # Vite config proves discovery of the new src test without changing config.
    inputs = [*SOURCE_ROOTS, 'web/vite.config.ts']
    def git(*args):
        result = subprocess.run(['git', '-C', str(root), *args], check=True,
                                capture_output=True, timeout=30)
        require(len(result.stdout) <= 2 << 20)
        return result.stdout
    require(not git('diff', 'HEAD', '--', *inputs).strip())
    require(not git('ls-files', '--others', '--exclude-standard', '--', *inputs).strip())
    require(git('ls-files', '--', 'web/vite.config.ts').strip() == b'web/vite.config.ts')
    require(os.environ.get('GITHUB_SHA') == value['checkoutCommit'])
    require(os.environ.get('GITHUB_RUN_ID') == value['runID'] and os.environ.get('GITHUB_RUN_ATTEMPT') == value['runAttempt'])
    value['sourceSHA256']['web/vite.config.ts'] = digest(read_file(root / 'web/vite.config.ts', 1 << 20))
    # Verify every captured path is an ordinary file and binds directly to HEAD.
    tree = git('ls-tree', '-r', '-z', 'HEAD', '--', *inputs)
    entries = tree.split(b'\0')
    require(entries[-1] == b'')
    seen = set()
    for entry in entries[:-1]:
        meta, path_raw = entry.split(b'\t', 1)
        mode, kind, oid = meta.split(b' ')
        path = path_raw.decode('utf-8', 'strict')
        require(mode in (b'100644', b'100755') and kind == b'blob' and path not in seen)
        require(path in value['sourceSHA256'])
        seen.add(path)
        body = read_file(root / path, 32 << 20)
        require(hashlib.sha1(b'blob ' + str(len(body)).encode() + b'\0' + body).hexdigest().encode() == oid)
        require(digest(body) == value['sourceSHA256'][path])
    require(seen == set(value['sourceSHA256']))
    provenance_shape(value)
    return value


def decode(value):
    require(type(value) is str)
    raw = base64.b64decode(value, validate=True)
    require(base64.b64encode(raw).decode('ascii') == value)
    return raw


def source_rows(raw):
    value = strict_json(raw, 16 << 20)
    exact(value, 'dictionaryVersion objects source')
    require(list(value) == ['dictionaryVersion', 'objects', 'source'])
    number(value['dictionaryVersion'], 3, 3)
    require(type(value['objects']) is list and len(value['objects']) == 1024)
    rows = []
    for index, obj in enumerate(value['objects'], 1):
        exact(obj, 'base supplemental identity')
        require(list(obj) == ['base', 'supplemental', 'identity'])
        base, extra, identity = obj['base'], obj['supplemental'], obj['identity']
        exact(base, 'objectGUID distinguishedName kind objectClass samAccountName userAccountControl')
        exact(extra, 'objectSidBytes mailBytes descriptionBytes whenCreatedBytes')
        exact(identity, 'primaryGroupIDBytes servicePrincipalNameBytes')
        require(list(base) == ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl'])
        require(list(extra) == ['objectSidBytes', 'mailBytes', 'descriptionBytes', 'whenCreatedBytes'])
        require(list(identity) == ['primaryGroupIDBytes', 'servicePrincipalNameBytes'])
        kind = 'computer' if index <= 1020 else 'user' if index <= 1022 else 'group'
        name = f'{kind}-capacity-{index:04d}'
        expected_guid = f'{index:08x}-1122-3344-5566-778899aabbcc'
        require(base['objectGUID'] == expected_guid and base['kind'] == kind)
        require(base['objectClass'] == (['computer', 'organizationalperson', 'person', 'top', 'user'] if kind == 'computer' else ['organizationalperson', 'person', 'top', 'user'] if kind == 'user' else ['group', 'top']))
        require(base['samAccountName'] == name + ('$' if kind == 'computer' else ''))
        if kind == 'computer':
            number(base['userAccountControl'], 4096, 4096)
            suffix = ''.join(',OU=' + digest(f'adtr-computer-capacity-v1|{index:04d}|{ou:02d}'.encode('ascii')) for ou in range(48))
        else:
            require(base['userAccountControl'] is None)
            suffix = ',OU=' + ('Users' if kind == 'user' else 'Groups')
        require(base['distinguishedName'] == 'CN=' + name + suffix + ',DC=synthetic,DC=invalid')
        sid_raw = decode(extra['objectSidBytes'])
        require(sid_raw == b'\x01\x01\0\0\0\0\0\x05' + struct.pack('<I', index))
        require(extra['mailBytes'] is None and extra['descriptionBytes'] is None
                and identity['primaryGroupIDBytes'] is None and identity['servicePrincipalNameBytes'] is None)
        require(decode(extra['whenCreatedBytes']) == b'20260101000000.0Z')
        require(len(encoded(obj)) <= 8192)
        # Values used below are decoded source fields, not generated expected cells.
        if kind == 'computer':
            created = decode(extra['whenCreatedBytes']).decode('ascii')
            rows.append({'guid': base['objectGUID'], 'sAMAccountName': base['samAccountName'],
                         'sid': f'S-1-{int.from_bytes(sid_raw[2:8], "big")}-{int.from_bytes(sid_raw[8:12], "little")}',
                         'dn': base['distinguishedName'],
                         'whenCreated': f'{created[:4]}-{created[4:6]}-{created[6:8]}T{created[8:10]}:{created[10:12]}:{created[12:14]}Z'})
    source = value['source']
    exact(source, 'server_name dc_host_name domain naming_context started_at completed_at elapsed_milliseconds pages')
    require(source['server_name'] == source['dc_host_name'] == 'dc.synthetic.invalid'
            and source['domain'] == 'synthetic.invalid' and source['naming_context'] == 'dc=synthetic,dc=invalid')
    require(list(source) == ['server_name', 'dc_host_name', 'domain', 'naming_context', 'started_at', 'completed_at', 'elapsed_milliseconds', 'pages'])
    number(source['pages'], 4, 4)
    number(source['elapsed_milliseconds'], 0, 125000)
    times = []
    for field in ('started_at', 'completed_at'):
        pattern(source[field], r'20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z')
        times.append(datetime.fromisoformat(source[field].replace('Z', '+00:00')))
    milliseconds = (times[1] - times[0]).total_seconds() * 1000
    require(0 <= milliseconds <= 125000 and abs(milliseconds - source['elapsed_milliseconds']) < 2)
    # Codec emits this ordered, compact ASCII envelope. Reject lexical channels.
    require(encoded(value) == raw)
    return rows


def bounded_inflate(raw, expected, wbits):
    require(type(expected) is int and 0 <= expected <= 256 << 20)
    inflater = zlib.decompressobj(wbits)
    body = inflater.decompress(raw, expected + 1)
    require(len(body) == expected and inflater.eof and not inflater.unused_data and not inflater.unconsumed_tail)
    return body


def worksheet_bytes(matrix):
    require(type(matrix) is list and len(matrix) == 1021 and matrix[0] == HEADERS)
    pieces = [WORKSHEET_START]
    for row_number, row in enumerate(matrix, 1):
        require(type(row) is list and len(row) == 5)
        pieces.append(f'<row r="{row_number}">'.encode('ascii'))
        for col, cell in enumerate(row):
            require(type(cell) is str and cell.isascii() and 0 < len(cell) <= 8192)
            require(re.fullmatch(r'[A-Za-z0-9 ,=.$:\-/]+', cell) is not None)
            pieces.append(f'<c r="{chr(65 + col)}{row_number}" t="inlineStr"><is><t xml:space="preserve">{cell}</t></is></c>'.encode('ascii'))
        pieces.append(b'</row>')
    pieces.append(WORKSHEET_END)
    result = b''.join(pieces)
    require(len(result) <= 256 << 20)
    return result


def validate_zip(raw, matrix):
    """Admit every byte of the actual Go writer envelope without recompression."""
    require(type(raw) is bytes and 0 < len(raw) <= 32 << 20 and len(raw) >= 22)
    end = struct.unpack_from('<IHHHHIIH', raw, len(raw) - 22)
    require(end[:5] == (0x06054b50, 0, 0, 6, 6) and end[7] == 0)
    central_size, central_start = end[5:7]
    require(central_start + central_size == len(raw) - 22)
    expected = [*STATIC_PARTS, ('xl/worksheets/sheet1.xml', worksheet_bytes(matrix))]
    cursor, local_cursor = central_start, 0
    for expected_name, expected_body in expected:
        name = expected_name.encode('ascii')
        require(cursor + 46 <= len(raw) - 22)
        record = struct.unpack_from('<I6H3I5H2I', raw, cursor)
        (signature, made, needed, flags, method, dos_time, dos_date, crc, compressed,
         expanded, name_len, extra_len, comment_len, disk, internal, external, local_offset) = record
        require((signature, made, needed, flags, method, dos_time, dos_date) == (0x02014b50, 20, 20, 8, 8, 0, 0))
        require((extra_len, comment_len, disk, internal, external) == (0, 0, 0, 0, 0))
        require(name_len == len(name) and cursor + 46 + name_len <= len(raw) - 22)
        require(raw[cursor + 46:cursor + 46 + name_len] == name)
        require(local_offset == local_cursor and expanded == len(expected_body) and 0 < compressed <= 32 << 20)
        require(local_cursor + 30 + name_len <= central_start)
        local = struct.unpack_from('<I5H3I2H', raw, local_cursor)
        require(local == (0x04034b50, 20, 8, 8, 0, 0, 0, 0, 0, len(name), 0))
        require(raw[local_cursor + 30:local_cursor + 30 + name_len] == name)
        body_start = local_cursor + 30 + name_len
        body_end = body_start + compressed
        require(body_end + 16 <= central_start)
        body = bounded_inflate(raw[body_start:body_end], expanded, -15)
        require(body == expected_body)
        require(zlib.crc32(body) == crc)
        require(struct.unpack_from('<IIII', raw, body_end) == (0x08074b50, crc, compressed, expanded))
        local_cursor = body_end + 16
        cursor += 46 + name_len
    require(local_cursor == central_start and cursor == len(raw) - 22)
    require(workbook_rows(raw) == matrix)


def strict_png(raw):
    require(type(raw) is bytes and 0 < len(raw) <= 10 << 20 and raw[:8] == b'\x89PNG\r\n\x1a\n')
    cursor, count, chunks, state = 8, 0, [], 'header'
    while cursor < len(raw):
        count += 1
        require(count <= 4096 and cursor + 12 <= len(raw))
        size, kind = struct.unpack_from('>I4s', raw, cursor)
        end = cursor + 12 + size
        require(end <= len(raw))
        body = raw[cursor + 8:end - 4]
        require(zlib.crc32(kind + body) == int.from_bytes(raw[end - 4:end], 'big'))
        if state == 'header':
            require(kind == b'IHDR' and body == struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0))
            state = 'data'
        elif kind == b'IDAT' and state == 'data':
            require(size > 0)
            chunks.append(body)
        else:
            require(kind == b'IEND' and size == 0 and state == 'data' and chunks and end == len(raw))
            state = 'end'
        cursor = end
    require(state == 'end' and cursor == len(raw))
    pixels = bounded_inflate(b''.join(chunks), 900 * (1 + 1440 * 3), 15)
    require(all(pixels[row * (1 + 1440 * 3)] <= 4 for row in range(900)))
    validate_png(raw)


def request_shape(request, source):
    exact(request, 'domainId expectedRevision expectedCredentialRevision observationId search startTm endTm creatTm screenColumns')
    require(all(type(request[key]) is str for key in request if key != 'screenColumns'))
    require(request['domainId'] == source['domainId'] and request['observationId'] == source['taskUUID'])
    require(request['expectedRevision'] == source['selection']['revision']
            and request['expectedCredentialRevision'] == source['selection']['credentialRevision'])
    require(request['search'] == 'computer-' and request['startTm'] == '2026-01-01T00:00:00Z'
            and request['endTm'] == '2026-01-02T00:00:00Z' and request['creatTm'] == 'asc'
            and request['screenColumns'] == COLUMNS)


def source_metadata(source, raw):
    exact(source, 'file taskUUID tenantId actorId domainId dictionaryVersion count selection sourcePins producer observationOpener useOpener purpose useState reason dependencies segments')
    file_ref(source['file'], FILES[0], raw)
    uuid(source['taskUUID']); pattern(source['domainId'], r'[A-Za-z0-9_-]{24}'); decimal(source['actorId'], high=(1 << 53) - 1)
    require(source['tenantId'] == 'default')
    number(source['dictionaryVersion'], 3, 3); number(source['count'], 1024, 1024)
    exact(source['selection'], 'domainId revision credentialRevision')
    require(source['selection']['domainId'] == source['domainId'])
    decimal(source['selection']['revision']); decimal(source['selection']['credentialRevision'])
    pins = source['sourcePins']
    exact(pins, 'accountId accountCredentialRevision policyRevision grantRoleId grantRevision')
    pattern(pins['accountId'], r'[A-Za-z0-9_-]{24}'); sha(pins['policyRevision'])
    require(pins['grantRoleId'] == 'platform_admin')
    decimal(pins['accountCredentialRevision']); decimal(pins['grantRevision'])
    producer = source['producer']
    exact(producer, 'taskID tenantId domainId actorId kind payloadVersion state attempt maxAttempts fencingToken')
    require(producer['taskID'] == source['taskUUID'] and producer['tenantId'] == source['tenantId']
            and producer['domainId'] == source['domainId'] and producer['actorId'] == source['actorId']
            and producer['kind'] == 'domain.directory_read.v3' and producer['state'] == 'succeeded')
    for key in ('payloadVersion', 'attempt', 'maxAttempts'):
        number(producer[key], 1, 1)
    decimal(producer['fencingToken'])
    for key in ('observationOpener', 'useOpener'):
        exact(source[key], 'owner fencingToken attempt')
        pattern(source[key]['owner'], r'[0-9a-f]{32}-[0-3]')
        require(source[key]['fencingToken'] == producer['fencingToken'])
        number(source[key]['attempt'], 1, 1)
    require(source['observationOpener'] == source['useOpener'])
    require(source['purpose'] == 'domain.directory_read.v3' and source['useState'] == 'quiesced'
            and source['reason'] == 'executor_returned')
    number(source['dependencies'], 0, 0)
    require(type(source['segments']) is list and 1 <= len(source['segments']) <= 32)
    offset = 0
    for part in source['segments']:
        exact(part, 'offset byteCount sha256')
        number(part['offset'], 0, len(raw)); number(part['byteCount'], 1, 524288); sha(part['sha256'])
        require(part['offset'] == offset)
        body = raw[offset:offset + part['byteCount']]
        require(len(body) == part['byteCount'] and digest(body) == part['sha256'])
        offset += len(body)
    require(offset == len(raw))


def public_task(task, task_id, domain, final):
    exact(task, 'taskUUID taskName domainId payloadVersion state progress attempt maxAttempts archived')
    require(task['taskUUID'] == task_id and task['domainId'] == domain and task['taskName'] == 'computer_assets.v3.export')
    number(task['payloadVersion'], 1, 1); number(task['maxAttempts'], 3, 3)
    require(task['archived'] is False)
    number(task['progress'], 0, 100); number(task['attempt'], 0, 1)
    require(task['state'] in ('queued', 'running', 'succeeded'))
    if final:
        require(task['state'] == 'succeeded')
        number(task['progress'], 100, 100); number(task['attempt'], 1, 1)


def database_metadata(database, task_id, actor, source, request, raw):
    exact(database, 'task snapshot manifest chunks events file')
    file_ref(database['file'], 'database-export.xlsx', raw)
    task = database['task']
    exact(task, 'taskID tenantId actorId domainId kind payloadVersion state progress attempt maxAttempts resultVersion fencingToken authorizationVersion payloadHash payload result')
    require(task['taskID'] == task_id and task['tenantId'] == 'default' and task['actorId'] == str(actor)
            and task['domainId'] == source['domainId'] and task['kind'] == 'computer_assets.v3.export'
            and task['state'] == 'succeeded' and task['payload'] == request)
    for key, expected in [('payloadVersion', 1), ('progress', 100), ('attempt', 1), ('maxAttempts', 3)]:
        number(task[key], expected, expected)
    for key in ('resultVersion', 'fencingToken', 'authorizationVersion'):
        decimal(task[key])
    sha(task['payloadHash'])
    # Task engine hashes version + empty parent + canonical map JSON, not the
    # HTTP request struct order or PostgreSQL JSONB presentation whitespace.
    require(task['payloadHash'] == digest(b'1::' + encoded({key: request[key] for key in sorted(request)})))
    result = task['result']
    exact(result, 'artifactToken rowCount byteCount sha256')
    require(result['artifactToken'] == task['fencingToken'])
    number(result['rowCount'], 1020, 1020); number(result['byteCount'], (1 << 20) + 1, 32 << 20)
    require(result['byteCount'] == len(raw) and result['sha256'] == digest(raw))
    snapshot = database['snapshot']
    exact(snapshot, 'taskID tenantId actorId authorizationVersion observationId sourceSHA256 payloadHash rowCount')
    for key in ('taskID', 'tenantId', 'actorId', 'authorizationVersion', 'payloadHash'):
        require(snapshot[key] == task[key])
    require(snapshot['observationId'] == source['taskUUID'] and snapshot['sourceSHA256'] == source['file']['sha256'])
    number(snapshot['rowCount'], 1020, 1020)
    manifest = database['manifest']
    exact(manifest, 'taskID fencingToken byteCount chunkCount rowCount sha256')
    require(manifest['taskID'] == task_id and manifest['fencingToken'] == task['fencingToken'])
    for key in ('byteCount', 'rowCount', 'sha256'):
        require(manifest[key] == result[key])
    count = (len(raw) + (1 << 20) - 1) // (1 << 20)
    number(manifest['chunkCount'], 2, 32)
    require(manifest['chunkCount'] == count and type(database['chunks']) is list and len(database['chunks']) == count)
    for ordinal, part in enumerate(database['chunks']):
        exact(part, 'taskID fencingToken ordinal byteCount sha256')
        require(part['taskID'] == task_id and part['fencingToken'] == task['fencingToken'])
        number(part['ordinal'], ordinal, ordinal)
        chunk = raw[ordinal * (1 << 20):(ordinal + 1) * (1 << 20)]
        number(part['byteCount'], len(chunk), len(chunk)); sha(part['sha256'])
        require(part['sha256'] == digest(chunk))
        require(ordinal == count - 1 or len(chunk) == 1 << 20)
    events = database['events']
    require(type(events) is list and 5 <= len(events) <= 1000)
    required = ['submitted', 'leased', 'started', 'progress', 'finished']
    index, prior = 0, 0
    for event in events:
        exact(event, 'action state resultVersion')
        require(event['action'] in required and event['state'] in ('queued', 'running', 'succeeded'))
        version = decimal(event['resultVersion'], 0)
        require(version >= prior)
        prior = version
        if index < len(required) and event['action'] == required[index]:
            index += 1
        else:
            require(index == 4 and event['action'] == 'progress')
        require(event['state'] == ('queued' if event['action'] in ('submitted', 'leased') else 'succeeded' if event['action'] == 'finished' else 'running'))
    require(index == 5 and events[-1]['resultVersion'] == task['resultVersion'])


def validate_core(blobs, provenance):
    require(set(blobs) == set(FILES[:-1]))
    for name, raw in blobs.items():
        require(type(raw) is bytes and 0 < len(raw) <= LIMITS[name])
    provenance_shape(provenance)
    require(strict_json(blobs['provenance.json'], LIMITS['provenance.json']) == provenance)
    data = strict_json(blobs['evidence.json'], LIMITS['evidence.json'])
    exact(data, 'format suite provenance actorId source request create receipt detail list database download sensitiveBudget screenshot cells timings')
    number(data['format'], 1, 1); require(data['suite'] == SUITE and data['provenance'] == provenance)
    actor = number(data['actorId'], 1)
    source = data['source']
    source_metadata(source, blobs['source-observation.json'])
    require(str(actor) != source['actorId'])
    request = data['request']
    request_shape(request, source)
    rows = source_rows(blobs['source-observation.json'])
    matrix = [HEADERS, *matched_rows(rows, request)]
    require(len(matrix) == 1021)
    task_id = data['create']['taskID']
    uuid(task_id)
    for key in ('create', 'receipt', 'detail'):
        projection = data[key]
        fields = 'taskID request task' + (' replayed' if key == 'create' else ' downloadReady downloadStatus artifact' if key == 'detail' else '')
        exact(projection, fields)
        require(projection['taskID'] == task_id and projection['request'] == request)
        public_task(projection['task'], task_id, source['domainId'], key != 'create')
    require(data['create']['replayed'] is False and data['detail']['downloadReady'] is True and data['detail']['downloadStatus'] == 'ready')
    listing = data['list']
    exact(listing, 'observationId dictionaryVersion pageSize firstPageCount total')
    require(listing['observationId'] == source['taskUUID'])
    for key, expected in [('dictionaryVersion', 3), ('pageSize', 10), ('firstPageCount', 10), ('total', 1020)]:
        number(listing[key], expected, expected)
    db, download = blobs['database-export.xlsx'], blobs['download.xlsx']
    require((1 << 20) < len(db) <= 32 << 20 and db == download)
    database_metadata(data['database'], task_id, actor, source, request, db)
    validate_zip(db, matrix)
    validate_zip(download, matrix)
    filename = f'computer-assets-v3-{task_id}.xlsx'
    artifact = data['detail']['artifact']
    exact(artifact, 'rowCount byteCount sha256 filename')
    require(artifact == {'rowCount': 1020, 'byteCount': len(download), 'sha256': digest(download), 'filename': filename})
    transfer = data['download']
    exact(transfer, 'file suggestedFilename status headers')
    file_ref(transfer['file'], 'download.xlsx', download)
    number(transfer['status'], 200, 200)
    require(transfer['suggestedFilename'] == filename)
    expected_headers = {'x-adtr-user-id': str(actor), 'cache-control': 'no-store', 'x-content-type-options': 'nosniff',
                        'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
                        'content-length': str(len(download)), 'x-adtr-artifact-sha256': digest(download),
                        'x-adtr-row-count': '1020', 'content-disposition': f'attachment; filename="{filename}"'}
    require(transfer['headers'] == expected_headers)
    file_ref(data['screenshot'], 'ready.png', blobs['ready.png'])
    strict_png(blobs['ready.png'])
    exact(data['cells'], 'rowCount columnCount firstRowSHA256 lastRowSHA256 matrixSHA256')
    require(data['cells'] == {'rowCount': 1020, 'columnCount': 5, 'firstRowSHA256': digest(encoded(matrix[1])),
                             'lastRowSHA256': digest(encoded(matrix[-1])), 'matrixSHA256': digest(encoded(matrix))})
    budget = data['sensitiveBudget']
    exact(budget, 'before after')
    for key, owner, exports in [('before', 7, 0), ('after', 8, 1)]:
        expected = {'schemaVersion': 23, 'owner': owner, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': exports}
        exact(budget[key], expected)
        for field, number_expected in expected.items():
            number(budget[key][field], number_expected, number_expected)
    exact(data['timings'], 'scope submitToDownloadMs')
    require(data['timings']['scope'] == 'browser_export_submit_to_download')
    number(data['timings']['submitToDownloadMs'], 0, 900000)
    return data


def validate_resources(value, provenance_raw, final=True):
    exact(value, 'format suite provenanceSHA256 scope clockTicksPerSecond elapsedMs api worker limitations' + (' runtime' if final else ''))
    number(value['format'], 1, 1)
    require(value['suite'] == SUITE and value['scope'] == 'full_browser_phase'
            and value['provenanceSHA256'] == digest(provenance_raw) and value['limitations'] == LIMITATIONS)
    hz = number(value['clockTicksPerSecond'], 1, 1000000)
    number(value['elapsedMs'], 1, 86400000)
    for name in ('api', 'worker'):
        process = value[name]
        exact(process, 'pid startTicks before after userCpuSeconds systemCpuSeconds')
        number(process['pid'], 1, (1 << 31) - 1); decimal(process['startTicks'], 0)
        for endpoint in ('before', 'after'):
            sample = process[endpoint]
            exact(sample, 'userTicks systemTicks vmRSSKiB vmHWMKiB')
            decimal(sample['userTicks'], 0); decimal(sample['systemTicks'], 0)
            number(sample['vmRSSKiB'], 0, 1 << 40); number(sample['vmHWMKiB'], 0, 1 << 40)
            require(sample['vmHWMKiB'] >= sample['vmRSSKiB'])
        require(process['after']['vmHWMKiB'] >= process['before']['vmHWMKiB'])
        for field, ticks in [('userCpuSeconds', 'userTicks'), ('systemCpuSeconds', 'systemTicks')]:
            expected = (int(process['after'][ticks]) - int(process['before'][ticks])) / hz
            require(expected >= 0 and type(process[field]) in (int, float) and math.isfinite(process[field]) and process[field] == expected)
    require(value['api']['pid'] != value['worker']['pid'])
    if final:
        report = value['runtime']
        require(len(validate_report(report)) <= 64 << 10)
        require(report['http_panic'] is False and report['collection_errors'] == [])
        for process in report['processes'].values():
            require(process == {'started': True, 'before_cleanup': None, 'after_cleanup': 0, 'forced_kill': False})
        required = ['setup', 'api-start', 'api-ready', 'worker-start', 'worker-ready', 'browser-start', 'browser-pass', 'before-cleanup', 'after-cleanup']
        require([phase['phase'] for phase in report['phases']] == required)
        times = [phase['at_ms'] for phase in report['phases']]
        require(times == sorted(times))


def load_bundle(directory, final):
    directory = safe_path(directory)
    names = FILES if final else FILES[:-1]
    require(directory.is_dir() and {path.name for path in directory.iterdir()} == set(names))
    return {name: read_file(directory / name, LIMITS[name]) for name in names}


def validate_bundle(directory, provenance_raw, final=True):
    blobs = load_bundle(directory, final)
    provenance = strict_json(provenance_raw, LIMITS['provenance.json'])
    require(blobs['provenance.json'] == provenance_raw)
    validate_core({name: blobs[name] for name in FILES[:-1]}, provenance)
    if final:
        validate_resources(strict_json(blobs['resources.json'], LIMITS['resources.json']), provenance_raw)
    return blobs


# Reviewed literal writer templates; not read from the candidate workbook.
STATIC_PARTS = [('[Content_Types].xml', b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>'), ('_rels/.rels', b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>'), ('docProps/core.xml', b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>ComputerAssets</dc:title></cp:coreProperties>'), ('xl/workbook.xml', b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="ComputerAssets" sheetId="1" r:id="rId1"/></sheets></workbook>'), ('xl/_rels/workbook.xml.rels', b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>')]
WORKSHEET_START = b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>'
WORKSHEET_END = b'</sheetData></worksheet>'
REQUIRED_SOURCES = frozenset(['.github/workflows/ci.yml', 'Makefile', 'internal/auth/computer_asset_export_v3_http.go', 'internal/auth/computer_asset_export_v3_http_external_integration_test.go', 'internal/auth/computer_asset_export_v3_http_test.go', 'internal/auth/computer_asset_export_v3_permissions.go', 'internal/computerassetxlsx/writer.go', 'internal/computerassetxlsx/writer_test.go', 'internal/directoryassets/objects.go', 'internal/directoryassets/objects_v2.go', 'internal/directoryassets/objects_v3.go', 'internal/directoryassets/pages_v3.go', 'internal/domains/computer_asset_export_v3.go', 'internal/domains/computer_asset_export_v3_read.go', 'internal/domains/computer_asset_export_v3_schema.go', 'internal/domains/computer_asset_export_v3_test.go', 'internal/domains/computer_asset_export_v3_types.go', 'internal/domains/computer_assets_v3.go', 'internal/domains/directory_authority.go', 'internal/domains/directory_executor.go', 'internal/domains/directory_observation_schema.go', 'internal/domains/directory_observation_v3_schema.go', 'internal/domains/directory_profile.go', 'internal/domains/directory_query_v3.go', 'internal/domains/directory_use.go', 'internal/domains/directory_use_schema.go', 'internal/domains/directory_use_v3_schema.go', 'internal/domains/directory_v2_observation_codec.go', 'internal/domains/directory_v3_observation_codec.go', 'internal/ldapconnection/directory.go', 'internal/ldapconnection/directory_v3.go', 'internal/tasks/engine.go', 'internal/tasks/types.go', 'internal/testldapfixture/computer_assets_v3.go', 'internal/testldapfixture/computer_assets_v3_test.go', 'internal/testldapfixture/computer_export_failure_v3_test.go', 'internal/testldapfixture/computer_export_multichunk_v3.go', 'internal/testldapfixture/computer_export_multichunk_v3_test.go', 'internal/testldapfixture/directory.go', 'internal/testldapfixture/directory_test.go', 'internal/testldapfixture/directory_v2_test.go', 'internal/testldapfixture/directory_v3.go', 'internal/testldapfixture/directory_v4_pagination_test.go', 'internal/testldapfixture/main.go', 'internal/testldapfixture/server.go', 'internal/testldapfixture/server_test.go', 'scripts/ci_inventory.json', 'scripts/ci_selection.py', 'scripts/ci_selection_summary.py', 'scripts/ci_workflow_contract.py', 'scripts/computer_asset_export_v3_ci.py', 'scripts/computer_asset_export_v3_multichunk_ci.py', 'scripts/directory_e2e_credentials.py', 'scripts/e2e_owned_container.py', 'scripts/e2e_runtime_diagnostics.py', 'scripts/fixtures/access_role_bulk_ci_delta.json', 'scripts/fixtures/computer_asset_export_v3_multichunk_delta.json', 'scripts/ldap_e2e_fixture.py', 'scripts/test_access_role_bulk_contract.py', 'scripts/test_auth_e2e.py', 'scripts/test_ci_selection_contract.py', 'scripts/test_computer_asset_export_v3_ci_contract.py', 'scripts/test_computer_asset_export_v3_evidence_contract.py', 'scripts/test_computer_asset_export_v3_fixture_contract.py', 'scripts/test_computer_asset_export_v3_multichunk_contract.py', 'scripts/test_computer_asset_export_v3_runner_contract.py', 'scripts/test_computer_asset_export_v3_running_cancel_ci_contract.py', 'scripts/test_computer_asset_export_v3_running_cancel_evidence_contract.py', 'scripts/test_computer_asset_export_v3_terminal_ci_contract.py', 'scripts/test_computer_asset_export_v3_terminal_evidence_contract.py', 'scripts/test_computer_assets_v3_contract.py', 'scripts/test_directory_e2e_credentials_contract.py', 'scripts/test_directory_v4_controls_ci_contract.py', 'scripts/test_directory_v4_runner_contract.py', 'scripts/test_group_asset_export_v3_runner_contract.py', 'scripts/test_group_members_v4_empty_runner_contract.py', 'scripts/test_group_members_v4_pagination_runner_contract.py', 'scripts/test_group_members_v4_refresh_runner_contract.py', 'scripts/test_integration.py', 'scripts/test_ldap_fixture_contract.py', 'scripts/test_tasks_health_recovery_runner_contract.py', 'scripts/test_user_asset_export_v3_runner_contract.py', 'scripts/test_user_asset_export_v3_terminal_runner_contract.py', 'scripts/validate_asset_empty_refresh.py', 'scripts/validate_computer_asset_export_v3_evidence.py', 'scripts/validate_computer_asset_export_v3_multichunk_evidence.py', 'scripts/validate_directory_v4_evidence.py', 'scripts/validate_empty_asset_evidence.py', 'scripts/validate_group_members_v4_pagination_evidence.py', 'scripts/validate_group_members_v4_refresh_evidence.py', 'web/e2e/computer-asset-export-v3-helpers.ts', 'web/e2e/computer-asset-export-v3-multichunk-evidence.ts', 'web/e2e/computer-asset-export-v3-multichunk.spec.ts', 'web/e2e/computer-asset-export-v3.spec.ts', 'web/e2e/computer-assets-v3-fixture.ts', 'web/e2e/directory-v2-response-observer.ts', 'web/e2e/navigation.ts', 'web/e2e/specialized-task-controls.ts', 'web/package-lock.json', 'web/package.json', 'web/playwright.config.ts', 'web/src/ComputerAssetExportV3.tsx', 'web/src/ComputerAssetsV3Workspace.tsx', 'web/src/ComputerExportMultichunkEvidence.test.ts', 'web/src/computer-asset-export-v3-api.ts', 'web/vite.config.ts'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    parser.add_argument('--provenance', required=True, type=Path)
    parser.add_argument('--repo', default=Path('.'), type=Path)
    parser.add_argument('--core', action='store_true')
    args = parser.parse_args()
    try:
        reference = read_file(args.provenance, LIMITS['provenance.json'])
        require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(args.repo))
        validate_bundle(args.directory, reference, not args.core)
    except Exception:
        raise SystemExit(ERROR) from None


if __name__ == '__main__':
    main()
