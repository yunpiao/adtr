"""Offline validation of real computer-export artifacts; unit specimens are not E2E evidence.

The caller supplies trusted checkout/run provenance. Source rows are derived from
captured PostgreSQL observation bytes, never from exporter-provided expected rows.
"""
import argparse
import base64
from datetime import datetime
import hashlib
import io
import json
import os
import subprocess
from pathlib import Path
import re
import struct
import zipfile
import zlib
import xml.etree.ElementTree as ET

COLUMNS = {'guid': 'GUID', 'sAMAccountName': 'SAM', 'sid': 'SID', 'dn': 'DN', 'whenCreated': 'whenCreated'}
NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
REL = 'http://schemas.openxmlformats.org/package/2006/relationships'
REQUEST_KEYS = {'domainId', 'expectedRevision', 'expectedCredentialRevision', 'observationId', 'search', 'startTm', 'endTm', 'creatTm', 'screenColumns'}
MAX_BYTES = 32 << 20
SOURCE_ROOTS = ['cmd', 'internal', 'scripts', 'web/src', 'web/e2e', 'web/playwright.config.ts', 'web/package.json', 'web/package-lock.json', '.github/workflows', 'go.mod', 'go.sum', 'Makefile']


def checkout_provenance(root, run_id=None, run_attempt=None):
    def git(*args):
        return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()
    require(not git('diff', 'HEAD', '--') and not git('ls-files', '--others', '--exclude-standard', '--', *SOURCE_ROOTS), 'dirty or untracked executable checkout')
    files = git('ls-files', '--', *SOURCE_ROOTS).splitlines()
    require('web/e2e/computer-asset-export-v3.spec.ts' in files and 'internal/computerassetxlsx/writer.go' in files, 'missing committed export sources')
    run_id = run_id or os.environ.get('GITHUB_RUN_ID', '')
    run_attempt = run_attempt or os.environ.get('GITHUB_RUN_ATTEMPT', '')
    require(re.fullmatch(r'[1-9][0-9]*', run_id) and re.fullmatch(r'[1-9][0-9]*', run_attempt), 'real CI run provenance required')
    return {'checkoutCommit': git('rev-parse', 'HEAD'), 'checkoutTree': git('rev-parse', 'HEAD^{tree}'), 'runID': run_id, 'runAttempt': run_attempt, 'sourceSHA256': {p: sha((root / p).read_bytes()) for p in sorted(files)}}


def require(condition, label):
    if not condition:
        raise ValueError(label)


def exact(obj, keys, label):
    require(type(obj) is dict and set(obj) == set(keys), label)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def strict_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, 'duplicate JSON member')
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=unique, parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite JSON')))


def decode_bytes(value):
    require(type(value) is str, 'source base64 type')
    raw = base64.b64decode(value, validate=True)
    require(base64.b64encode(raw).decode() == value, 'canonical base64')
    return raw


def source_rows(source):
    raw = decode_bytes(source['bodyBase64'])
    require(len(raw) <= 16 << 20 and sha(raw) == source['sha256'], 'source SHA/size')
    value = strict_json(raw)
    require(value['dictionaryVersion'] == 3 and source['dictionaryVersion'] == 3, 'dictionary 3 source')
    require(type(value['objects']) is list and len(value['objects']) == source['count'] <= 10000, 'source count')
    require(source['purpose'] == 'domain.directory_read.v3' and source['useState'] == 'quiesced' and source['reason'] == 'executor_returned' and source['dependencies'] == 0, 'completed producer credential use')
    require(source['payloadVersion'] == 1 and source['maxAttempts'] == 1 and source['attempt'] == 1, 'producer execution policy')
    require(source['taskState'] == 'succeeded' and source['taskName'] == 'domain.directory_read.v3', 'real producer task')
    require(len(value['objects']) == 30 and {kind: sum(o['base']['kind'] == kind for o in value['objects']) for kind in ('computer', 'user', 'group')} == {'computer': 23, 'user': 3, 'group': 4}, 'isolated computer fixture population')
    rows = []
    for obj in value['objects']:
        base, extra = obj['base'], obj['supplemental']
        if base['kind'] != 'computer':
            continue
        require(type(base['objectClass']) is list and {'computer', 'user'} <= set(base['objectClass']), 'computer class also inherits user')
        def text(key):
            return '' if extra[key] is None else decode_bytes(extra[key]).decode('utf-8', 'strict')
        sid = ''
        if extra['objectSidBytes'] is not None:
            binary = decode_bytes(extra['objectSidBytes'])
            require(len(binary) >= 8 and binary[0] == 1 and len(binary) == 8 + 4 * binary[1], 'SID encoding')
            sid = 'S-1-' + str(int.from_bytes(binary[2:8], 'big'))
            sid += ''.join('-' + str(v[0]) for v in struct.iter_unpack('<I', binary[8:]))
        created = text('whenCreatedBytes')
        if created:
            require(re.fullmatch(r'\d{14}(?:\.0)?Z', created) is not None, 'fixture generalized time')
            created = f'{created[:4]}-{created[4:6]}-{created[6:8]}T{created[8:10]}:{created[10:12]}:{created[12:14]}Z'
        rows.append({'guid': base['objectGUID'], 'sAMAccountName': base['samAccountName'] or '', 'sid': sid,
                     'dn': base['distinguishedName'], 'whenCreated': created})
    require(len({r['guid'] for r in rows}) == len(rows), 'source GUID uniqueness')
    require([r['whenCreated'] for r in sorted(rows, key=lambda r: r['guid'])] ==
            ['0001-01-01T00:00:00Z', *(['2026-01-01T00:00:00Z'] * 21), ''],
            'fixed source time boundaries and present year 0001')
    return rows


def matched_rows(rows, request):
    exact(request, REQUEST_KEYS, 'canonical request fields')
    columns = request['screenColumns']
    require(type(columns) is list and 1 <= len(columns) <= 5 and len(set(columns)) == len(columns) and all(c in COLUMNS for c in columns), 'resolved columns')
    require(all(type(request[k]) is str for k in REQUEST_KEYS - {'screenColumns'}), 'request strings')
    # The acceptance fixture deliberately uses ASCII predicates. This avoids
    # silently substituting Python full lowercase for Go simple lowercase.
    require(request['search'] == 'computer-', 'fixed acceptance predicates')
    require(all(row[k].isascii() or (k == 'sAMAccountName' and row[k] == 'computer-invalid\ufffe') for row in rows for k in ('sAMAccountName', 'sid', 'dn')), 'fixed fixture search fields')
    require(request['creatTm'] in ('', 'asc', 'desc'), 'order')
    require(bool(request['startTm']) == bool(request['endTm']), 'paired time bounds')
    if request['startTm']:
        for key in ('startTm', 'endTm'):
            require(re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z', request[key]), 'fixed UTC whole-second time bounds')
            datetime.fromisoformat(request[key].replace('Z', '+00:00'))
        require(request['startTm'] < request['endTm'], 'increasing time bounds')
    found = []
    for row in rows:
        if request['search'] and not any(request['search'].lower() in row[k].lower() for k in ('sAMAccountName', 'sid', 'dn')):
            continue
        if request['startTm'] and not (row['whenCreated'] and request['startTm'] <= row['whenCreated'] < request['endTm']):
            continue
        found.append(row)
    found.sort(key=lambda r: r['guid'])
    if request['creatTm']:
        present = [r for r in found if r['whenCreated']]
        present.sort(key=lambda r: r['whenCreated'], reverse=request['creatTm'] == 'desc')
        found = present + [r for r in found if not r['whenCreated']]
    return [[r[c] for c in columns] for r in found]


def ooxml_text(value):
    # One pass is essential: _x005F_x0041_ represents literal _x0041_, not A.
    units = re.sub(r'_x([0-9A-Fa-f]{4})_', lambda m: chr(int(m[1], 16)), value)
    return units.encode('utf-16-le', 'surrogatepass').decode('utf-16-le', 'strict')


def workbook_rows(raw):
    require(0 < len(raw) <= MAX_BYTES, 'XLSX bounds')
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        entries = archive.infolist()
        names = [entry.filename for entry in entries]
        allowed = {'[Content_Types].xml', '_rels/.rels', 'xl/workbook.xml', 'xl/_rels/workbook.xml.rels', 'xl/worksheets/sheet1.xml', 'docProps/core.xml'}
        require(len(names) == len(set(names)) and set(names) <= allowed, 'unexpected/duplicate ZIP member')
        require(allowed - {'docProps/core.xml'} <= set(names), 'missing XLSX part')
        require(sum(e.file_size for e in entries) <= 256 << 20, 'expanded ZIP bounds')
        parsed = {}
        for entry in entries:
            require(not entry.flag_bits & 1 and entry.compress_type in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED), 'ZIP encoding')
            data = archive.read(entry)
            text = data.decode('utf-8', 'strict')
            require('\x00' not in text and '<!DOCTYPE' not in text.upper() and '<!ENTITY' not in text.upper(), 'XML declarations/encoding')
            parsed[entry.filename] = ET.fromstring(text)
        for name, root in parsed.items():
            for node in root.iter():
                require(node.tag.rsplit('}', 1)[-1] not in ('f', 'hyperlink', 'hyperlinks', 'externalLink', 'externalReferences'), 'active XLSX content')
                require(node.attrib.get('TargetMode', 'Internal') == 'Internal', 'external relationship')
            if name.endswith('.rels'):
                for node in root:
                    require(node.tag == f'{{{REL}}}Relationship', 'relationship element')
                    require(node.attrib.get('Target') in ('xl/workbook.xml', 'worksheets/sheet1.xml', 'docProps/core.xml'), 'relationship target')
        types = parsed['[Content_Types].xml']
        content_ns = 'http://schemas.openxmlformats.org/package/2006/content-types'
        require(types.tag == f'{{{content_ns}}}Types', 'content types root')
        expected_types = {
            ('Default', 'rels'): 'application/vnd.openxmlformats-package.relationships+xml',
            ('Default', 'xml'): 'application/xml',
            ('Override', '/xl/workbook.xml'): 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml',
            ('Override', '/xl/worksheets/sheet1.xml'): 'application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml',
            ('Override', '/docProps/core.xml'): 'application/vnd.openxmlformats-package.core-properties+xml',
        }
        actual_types = {}
        for node in types:
            local = node.tag.rsplit('}', 1)[-1]
            key = (local, node.attrib.get('Extension' if local == 'Default' else 'PartName'))
            require(key not in actual_types, 'duplicate content type')
            actual_types[key] = node.attrib.get('ContentType')
        require(actual_types == expected_types, 'fixed nonmacro content types')
        sheets = parsed['xl/workbook.xml'].findall(f'{{{NS}}}sheets/{{{NS}}}sheet')
        require(len(sheets) == 1 and sheets[0].attrib['name'] == 'ComputerAssets', 'fixed sheet')
        sheet = parsed['xl/worksheets/sheet1.xml']
        data = sheet.find(f'{{{NS}}}sheetData')
        require(data is not None, 'sheet data')
        rows = []
        for i, row in enumerate(data, 1):
            require(row.tag == f'{{{NS}}}row' and row.attrib.get('r') == str(i), 'contiguous sheet rows')
            cells = []
            require(1 <= len(row) <= 5, 'cell count')
            for j, cell in enumerate(row):
                require(cell.tag == f'{{{NS}}}c' and cell.attrib.get('r') == f'{chr(65+j)}{i}' and cell.attrib.get('t') == 'inlineStr', 'inline text cell coordinates')
                require(len(cell) == 1 and cell[0].tag == f'{{{NS}}}is', 'inline cell body')
                inline = cell[0]
                require(len(inline) == 1 and inline[0].tag == f'{{{NS}}}t' and len(inline[0]) == 0, 'single text value')
                value = ooxml_text(inline[0].text or '')
                require(all(c in '\t\r\n' or 0x20 <= ord(c) <= 0x10ffff and ord(c) not in (0xfffe, 0xffff) for c in value), 'XML legal text after OOXML decoding')
                require(len(value.encode('utf-16-le')) // 2 <= 32767, 'cell UTF16 bounds')
                cells.append(value)
            rows.append(cells)
        require(1 <= len(rows) <= 10001, 'workbook row bounds')
        return rows


def validate_png(raw):
    require(raw.startswith(b'\x89PNG\r\n\x1a\n') and len(raw) <= 10 << 20, 'PNG signature/size')
    offset, kinds = 8, []
    while offset < len(raw):
        require(offset + 12 <= len(raw), 'PNG truncated chunk')
        size = int.from_bytes(raw[offset:offset+4], 'big')
        kind = raw[offset+4:offset+8]
        end = offset + 12 + size
        require(end <= len(raw), 'PNG chunk bounds')
        require(zlib.crc32(raw[offset+4:end-4]) == int.from_bytes(raw[end-4:end], 'big'), 'PNG CRC')
        if not kinds:
            require(kind == b'IHDR' and size == 13 and struct.unpack('>II', raw[offset+8:offset+16]) == (1440, 900), 'real viewport PNG dimensions')
        kinds.append(kind)
        offset = end
    require(kinds[-1:] == [b'IEND'] and b'IDAT' in kinds and kinds.count(b'IHDR') == 1 and kinds.count(b'IEND') == 1, 'PNG structure')


def no_secrets(value):
    if type(value) is dict:
        require(not set(value) & {'actorPassword', 'totpCode', 'ciphertext', 'userInfo', 'password', 'secret'}, 'secret field in evidence')
        for child in value.values():
            no_secrets(child)
    elif type(value) is list:
        for child in value:
            no_secrets(child)


def validate(data, read_artifact, provenance):
    exact(data, ['format', 'provenance', 'source', 'actorId', 'sensitiveBudget', 'cases'], 'evidence envelope')
    require(type(data['format']) is int and data['format'] == 1, 'evidence version')
    require(data['provenance'] == provenance, 'checkout/run/source provenance mismatch')
    no_secrets(data)
    budget = data['sensitiveBudget']
    exact(budget, ['before', 'after'], 'sensitive budget fields')
    require(budget['before'] == {'schemaVersion': 23, 'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}, 'setup schema23 sensitive budget and source ledger')
    require(budget['after'] == {**budget['before'], 'owner': 10, 'exportTasks': 3}, 'bounded owner budget; no new collection use')
    require(all(type(n) is int for point in budget.values() for n in point.values()), 'integer ledger counts')
    source = data['source']
    rows = source_rows(source)
    actor = data['actorId']
    require(type(actor) is int and actor > 0 and str(actor) != source['actorId'], 'independent export owner')
    require(type(data['cases']) is list and [c['name'] for c in data['cases']] == ['default', 'allMatched', 'empty'], 'required cases')
    task_ids = set()
    for case in data['cases']:
        exact(case, ['name', 'request', 'create', 'receipt', 'detail', 'download', 'database', 'pageSize', 'firstPageCount', 'screenshot'], 'case fields')
        request = case['request']
        expected = matched_rows(rows, request)
        require(request['observationId'] == source['taskUUID'], 'observation binding')
        require(request['domainId'] == source['selection']['domainId'] and request['expectedRevision'] == source['selection']['revision'] and request['expectedCredentialRevision'] == source['selection']['credentialRevision'], 'source pins')
        task_id = case['create']['taskID']
        require(type(task_id) is str and re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', task_id) and task_id not in task_ids, 'distinct export identity')
        task_ids.add(task_id)
        require(case['create']['replayed'] is False, 'new export task')
        for key in ('create', 'receipt', 'detail'):
            response = case[key]
            require(response['taskID'] == task_id and response['request'] == request, 'immutable response request')
            require(response['task']['taskUUID'] == task_id and response['task']['taskName'] == 'computer_assets.v3.export', 'export task identity')
        detail = case['detail']
        require(detail['task']['state'] == 'succeeded' and detail['downloadReady'] is True and detail['downloadStatus'] == 'ready', 'published ready state')
        artifact = detail['artifact']
        exact(artifact, ['rowCount', 'byteCount', 'sha256', 'filename'], 'artifact fields')
        filename = f'computer-assets-v3-{task_id}.xlsx'
        require(artifact['filename'] == filename, 'fixed artifact filename')
        download = case['download']
        exact(download, ['filename', 'headers'], 'download evidence')
        require(download['filename'] == filename, 'browser download filename')
        raw = read_artifact(filename)
        require(type(artifact['byteCount']) is int and artifact['byteCount'] == len(raw) and artifact['sha256'] == sha(raw), 'whole artifact digest/length')
        require(type(artifact['rowCount']) is int and artifact['rowCount'] == len(expected), 'artifact row count')
        headers = download['headers']
        exact(headers, ['x-adtr-user-id', 'cache-control', 'x-content-type-options', 'content-type', 'content-length', 'x-adtr-artifact-sha256', 'x-adtr-row-count', 'content-disposition'], 'safe download headers')
        require(headers['content-disposition'] == f'attachment; filename="{filename}"', 'download disposition')
        require(headers['x-adtr-user-id'] == str(actor) and headers['cache-control'] == 'no-store' and headers['x-content-type-options'] == 'nosniff', 'download security headers')
        require(headers['content-type'] == 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'XLSX MIME')
        require(headers['content-length'] == str(len(raw)) and headers['x-adtr-artifact-sha256'] == sha(raw) and headers['x-adtr-row-count'] == str(len(expected)), 'download manifest headers')
        actual = workbook_rows(raw)
        require(actual == [[COLUMNS[c] for c in request['screenColumns']], *expected], 'full workbook cells/order/source match')
        require(type(case['pageSize']) is int and case['pageSize'] == 10, 'observed page capacity')
        require(type(case['firstPageCount']) is int and case['firstPageCount'] == min(10, len(expected)), 'first page size')
        if case['name'] in ('default', 'allMatched'):
            require(len(expected) == (23 if case['name'] == 'default' else 22) and len(expected) > case['pageSize'], 'nontrivial all-matched count proof')
            require(request['screenColumns'] == (list(COLUMNS) if case['name'] == 'default' else ['dn', 'guid']), 'default or reordered subset columns')
            require((request['startTm'], request['endTm'], request['creatTm']) ==
                    (('', '', 'desc') if case['name'] == 'default' else ('0001-01-01T00:00:00Z', '2026-01-02T00:00:00Z', 'asc')),
                    'unbounded descending or inclusive-start filtered ascending order')
        else:
            require(not expected and request['screenColumns'] == ['dn', 'guid'], 'real zero-match workbook')
            require((request['startTm'], request['endTm'], request['creatTm']) ==
                    ('2025-12-31T00:00:00Z', '2026-01-01T00:00:00Z', 'desc'),
                    'exclusive-end empty time range proof')
        stored = case['database']
        exact(stored, ['taskID', 'actorId', 'state', 'kind', 'progress', 'attempt', 'maxAttempts', 'sourceSHA256', 'rowCount', 'byteCount', 'sha256', 'chunkCount', 'chunks', 'events'], 'persisted export fields')
        require(all(type(stored[k]) is int for k in ('progress', 'attempt', 'maxAttempts', 'rowCount', 'byteCount', 'chunkCount')), 'integer manifest/task values')
        require(stored['taskID'] == task_id and stored['actorId'] == str(actor) and stored['state'] == 'succeeded' and stored['kind'] == 'computer_assets.v3.export', 'durable export task')
        require(stored['sourceSHA256'] == source['sha256'] and stored['rowCount'] == len(expected) and stored['byteCount'] == len(raw) and stored['sha256'] == sha(raw), 'durable manifest/source')
        require(stored['progress'] == 100 and stored['attempt'] == 1 and stored['maxAttempts'] == 3, 'durable successful execution policy')
        events = stored['events']
        require(type(events) is list and all(type(e['resultVersion']) is int for e in events), 'task events')
        actions = [e['action'] for e in events]
        require(all(a in actions for a in ('submitted', 'leased', 'started', 'progress', 'finished')) and events[-1]['state'] == 'succeeded', 'durable Worker lifecycle')
        require([e['resultVersion'] for e in events] == sorted(e['resultVersion'] for e in events), 'monotonic durable versions')
        chunks = stored['chunks']
        require(type(chunks) is list and len(chunks) > 0 and stored['chunkCount'] == len(chunks), 'durable chunks')
        joined = bytearray()
        for ordinal, chunk in enumerate(chunks):
            exact(chunk, ['ordinal', 'bodyBase64', 'sha256'], 'chunk fields')
            require(type(chunk['ordinal']) is int, 'integer chunk ordinal')
            blob = decode_bytes(chunk['bodyBase64'])
            require(chunk['ordinal'] == ordinal and 0 < len(blob) <= 1 << 20 and sha(blob) == chunk['sha256'], 'chunk sequence/digest/bounds')
            joined.extend(blob)
        require(bytes(joined) == raw, 'download equals persisted chunks')
        image = case['screenshot']
        exact(image, ['filename', 'sha256'], 'screenshot reference')
        require(image['filename'] == f'{case["name"]}-ready.png', 'screenshot filename')
        png = read_artifact(image['filename'])
        require(sha(png) == image['sha256'], 'ready screenshot bytes')
        validate_png(png)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('evidence', type=Path, nargs='?')
    parser.add_argument('--repo', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--capture-provenance', action='store_true')
    parser.add_argument('--provenance', type=Path, help='independently captured trusted checkout/run/source provenance JSON')
    args = parser.parse_args()
    if args.capture_provenance:
        print(json.dumps(checkout_provenance(args.repo), sort_keys=True))
        return
    require(args.evidence is not None and args.provenance is not None, 'evidence and independent provenance required')
    trusted = strict_json(args.provenance.read_bytes())
    require(trusted == checkout_provenance(args.repo, trusted['runID'], trusted['runAttempt']), 'trusted checkout/source mismatch')
    data = strict_json(args.evidence.read_bytes())
    validate(data, lambda filename: (args.evidence.parent / filename).read_bytes(), trusted)
    print('computer asset export v3 evidence validated')


if __name__ == '__main__':
    main()
