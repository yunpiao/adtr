"""Pure-Python offline validator tests. These specimens are NOT acceptance artifacts."""
import base64
import copy
import io
import json
from pathlib import Path
import unittest
import zipfile
from xml.sax.saxutils import escape
import validate_user_asset_export_v3_evidence as v


def b64(raw):
    return base64.b64encode(raw).decode()


def xlsx(rows):
    ns = v.NS
    rel = v.REL
    xml_rows = []
    for i, cells in enumerate(rows, 1):
        body = ''
        for j, text in enumerate(cells):
            encoded = v.re.sub(r'_(?=x[0-9a-fA-F]{4}_)', '_x005F_', text).replace('\r', '_x000D_')
            body += f'<c r="{chr(65+j)}{i}" t="inlineStr"><is><t xml:space="preserve">{escape(encoded)}</t></is></c>'
        xml_rows.append(f'<row r="{i}">{body}</row>')
    parts = {
        '[Content_Types].xml': '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>',
        '_rels/.rels': f'<Relationships xmlns="{rel}"><Relationship Target="xl/workbook.xml"/></Relationships>',
        'docProps/core.xml': '<core/>',
        'xl/workbook.xml': f'<workbook xmlns="{ns}"><sheets><sheet name="UserAssets"/></sheets></workbook>',
        'xl/_rels/workbook.xml.rels': f'<Relationships xmlns="{rel}"><Relationship Target="worksheets/sheet1.xml"/></Relationships>',
        'xl/worksheets/sheet1.xml': f'<worksheet xmlns="{ns}"><sheetData>{"".join(xml_rows)}</sheetData></worksheet>',
    }
    return repack(parts)


def repack(parts):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, 'w', zipfile.ZIP_DEFLATED) as z:
        for name, body in parts.items():
            z.writestr(name, body)
    return stream.getvalue()


def mutate_zip(raw, name, change):
    with zipfile.ZipFile(io.BytesIO(raw)) as z:
        parts = {n: z.read(n) for n in z.namelist()}
    parts[name] = change(parts.get(name, b''))
    return repack(parts)


def specimen():
    objects = []
    for n in range(12):
        objects.append({'base': {'objectGUID': f'{n+1:08x}-0000-0000-0000-000000000000', 'kind': 'user', 'userAccountControl': 0 if n == 0 else 512, 'samAccountName': f'user-{n:02}', 'distinguishedName': f'CN=user-{n:02},DC=synthetic,DC=invalid'},
                        'supplemental': {'objectSidBytes': None, 'mailBytes': b64(('=SUM(1,2)<&>\r\n\t_x0041_' if n == 0 else 'mail@example.test').encode()), 'whenCreatedBytes': b64(b'20261001020304.0Z')}})
    raw = json.dumps({'dictionaryVersion': 3, 'objects': objects}).encode()
    source = {'bodyBase64': b64(raw), 'sha256': v.sha(raw), 'dictionaryVersion': 3, 'count': 12, 'taskState': 'succeeded', 'purpose': 'domain.directory_read.v3', 'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'payloadVersion': 1, 'maxAttempts': 1, 'attempt': 1, 'taskName': 'domain.directory_read.v3', 'taskUUID': 'observation1', 'actorId': '2', 'selection': {'domainId': 'domain1', 'revision': '2', 'credentialRevision': '1'}}
    data = {'format': 1, 'provenance': {'fixtureOnly': True}, 'source': source, 'actorId': 1, 'sensitiveBudget': {'before': {'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}, 'after': {'owner': 9, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 2}}, 'cases': []}
    files = {}
    for index, name in enumerate(('allMatched', 'empty')):
        task = f'export{index}'
        request = {'domainId': 'domain1', 'expectedRevision': '2', 'expectedCredentialRevision': '1', 'observationId': 'observation1', 'search': 'user-' if index == 0 else 'no-synthetic-user-match', 'sAMAccountName': '', 'startTm': '', 'endTm': '', 'creatTm': 'desc', 'screenColumns': ['mail', 'guid', 'sAMAccountName', 'dn', 'sid', 'whenCreated']}
        rows = v.matched_rows(v.source_rows(source), request)
        blob = xlsx([[v.COLUMNS[c] for c in request['screenColumns']], *rows])
        filename = f'user-assets-v3-{task}.xlsx'
        files[filename] = blob
        response = {'taskID': task, 'task': {'taskUUID': task, 'taskName': 'user_assets.v3.export', 'state': 'succeeded'}, 'request': request}
        screenshot = f'{name}-ready.png'
        def png_chunk(kind, body):
            return len(body).to_bytes(4, 'big') + kind + body + v.zlib.crc32(kind + body).to_bytes(4, 'big')
        files[screenshot] = b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', v.struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0)) + png_chunk(b'IDAT', v.zlib.compress((b'\x00' * (1 + 1440 * 3)) * 900)) + png_chunk(b'IEND', b'')
        count = len(rows)
        headers = {'content-disposition': f'attachment; filename="{filename}"', 'x-adtr-user-id': '1', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff', 'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'content-length': str(len(blob)), 'x-adtr-artifact-sha256': v.sha(blob), 'x-adtr-row-count': str(count)}
        data['cases'].append({'name': name, 'request': copy.deepcopy(request), 'create': {**copy.deepcopy(response), 'replayed': False}, 'receipt': copy.deepcopy(response), 'detail': {**copy.deepcopy(response), 'downloadReady': True, 'downloadStatus': 'ready', 'artifact': {'rowCount': count, 'byteCount': len(blob), 'sha256': v.sha(blob), 'filename': filename}}, 'download': {'filename': filename, 'headers': headers}, 'pageSize': 10, 'firstPageCount': min(10, count), 'database': {'taskID': task, 'actorId': '1', 'state': 'succeeded', 'kind': 'user_assets.v3.export', 'progress': 100, 'attempt': 1, 'maxAttempts': 3, 'events': [{'action': a, 'state': 'succeeded' if a == 'finished' else 'running', 'resultVersion': i} for i, a in enumerate(['submitted', 'leased', 'started', 'progress', 'finished'])], 'sourceSHA256': source['sha256'], 'rowCount': count, 'byteCount': len(blob), 'sha256': v.sha(blob), 'chunkCount': 1, 'chunks': [{'ordinal': 0, 'bodyBase64': b64(blob), 'sha256': v.sha(blob)}]}, 'screenshot': {'filename': screenshot, 'sha256': v.sha(files[screenshot])}})
    return data, files


def success_specimen():
    """Small format2 profile over the legacy shared specimen, never E2E proof."""
    data, files = specimen()
    data['format'] = 2
    empty = data['cases'][1]
    empty['request'].update(search='user-', uacDisableBit='set')
    for key in ('create', 'receipt', 'detail'):
        empty[key]['request'] = copy.deepcopy(empty['request'])
    for case in data['cases']:
        query = {key: value for key, value in case['request'].items() if key not in ('screenColumns', 'sAMAccountName', 'startTm', 'endTm')}
        query.update(pageIdx='1', pageSize='10')
        case['listQueryEntries'] = [list(pair) for pair in query.items()]
    return data, files


# Hand-authored cells and partition membership: never produced by matched_rows.
MIXED_CELLS = [
    ['=SUM(1,2)<&>\r\n\t_x0041_', '00000001-0000-0000-0000-000000000000', 'user-00', 'CN=user-00,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000002-0000-0000-0000-000000000000', 'user-01', 'CN=user-01,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000003-0000-0000-0000-000000000000', 'user-02', 'CN=user-02,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000004-0000-0000-0000-000000000000', 'user-03', 'CN=user-03,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000005-0000-0000-0000-000000000000', 'user-04', 'CN=user-04,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000006-0000-0000-0000-000000000000', 'user-05', 'CN=user-05,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
    ['mail@example.test', '00000007-0000-0000-0000-000000000000', 'user-06', 'CN=user-06,DC=synthetic,DC=invalid', '', '2026-10-01T02:03:04Z'],
]


def replace_source(data, body):
    """Rebind every copied source digest, so tampering reaches semantic checks."""
    raw = json.dumps(body).encode()
    data['source'].update(bodyBase64=b64(raw), sha256=v.sha(raw), count=len(body['objects']))
    for case in data['cases']:
        case['database']['sourceSHA256'] = v.sha(raw)


def mixed_source():
    data, _ = specimen()
    body = v.strict_json(v.decode_bytes(data['source']['bodyBase64']))
    body['objects'] = body['objects'][:7]
    for obj, uac in zip(body['objects'], [None, 0, 2, 512, 514, 2147483648, 4294967295]):
        obj['base']['userAccountControl'] = uac
    for kind in ('group', 'computer'):
        obj = copy.deepcopy(body['objects'][0])
        obj['base'].update(kind=kind, userAccountControl=2)
        body['objects'].append(obj)
    replace_source(data, body)
    return data, body


def success_browser_contract(source, config):
    # A small closed consumer-source contract, not a general TS framework.
    flat = v.re.sub(r'\s+', ' ', source)
    required = [
        'test.setTimeout(900_000);',
        'for (const name of ["allMatched", "empty"] as const)',
        'await applyList("no-synthetic-user-match");',
        '.getByLabel("本次观测的禁用位", { exact: true }) .selectOption("set");',
        'await expect(another).toBeDisabled();',
        'expect(draftRequests).toEqual([]);',
        'page.off("request", recordDraft);',
        '...new URL(response.url()).searchParams.entries(),',
        'expect([...listQueryEntries].sort()).toEqual(',
        'const list = await responseValue(response, actorId);',
        'domainId: source.selection.domainId,',
        'expectedRevision: source.selection.revision,',
        'expectedCredentialRevision: source.selection.credentialRevision,',
        'const expectedRequest = { ...pins, search: "user-",',
        'expect(created.request).toEqual(expectedRequest);',
        'expect(receipt.request).toEqual(expectedRequest);',
        'expect(detail.request).toEqual(expectedRequest);',
        'expect(original.request).toEqual(expectedRequest);',
        'request: expectedRequest, listQueryEntries,',
        'format: 2,',
        'owner: 7, producer: 9, observations: 1, directoryUses: 1, exportTasks: 0,',
        'expect(after).toEqual({ ...before, owner: 9, exportTasks: 2 });',
        'await originalTab.close();',
        'expect(await readFile(originalPath)).toEqual(bytes);',
    ]
    for token in required:
        v.require(flat.count(token) == 1, 'unique success source checkpoint: ' + token)
    for name in ('搜索用户', '创建用户导出', '下载用户 XLSX', '按当前列表创建另一导出', '下载原用户 XLSX'):
        v.require('name: "' + name + '", exact: true' in flat, 'real UI action: ' + name)
    v.require(flat.count('await proof(') == 1 and flat.count('await setupGroupFixture(') == 1, 'two-case proof and fixture bound')
    v.require(flat.index('const expectedRequest =') < flat.index('await proof(') < flat.index('const created ='), 'independent pre-submit request')
    v.require(flat.count('page.on("request", recordDraft);') == flat.count('page.off("request", recordDraft);') == 1, 'draft observer cleanup')
    v.require('page.evaluate(() => sessionStorage.removeItem(' not in flat, 'preserve main recovery across query remount')
    v.require('route.fulfill' not in flat and 'waitForTimeout' not in flat and 'test.describe.configure' not in flat, 'genuine bounded suite')
    for token in ('retries: 0,', 'workers: 1,', 'fullyParallel: false,'):
        v.require(config.count(token) == 1, 'unchanged concurrency/retry configuration')


class EvidenceContract(unittest.TestCase):
    def setUp(self):
        self.data, self.files = success_specimen()

    def check(self):
        v.validate(self.data, self.files.__getitem__, {'fixtureOnly': True})

    def test_valid_unit_specimen(self):
        self.check()


    def test_shared_format1_ten_key_defaults_preserved(self):
        data, files = specimen()
        self.assertEqual(data['format'], 1)
        for case in data['cases']:
            self.assertEqual(set(case['request']), v.REQUEST_KEYS)
            self.assertNotIn('listQueryEntries', case)
        self.assertEqual(data['cases'][1]['request']['search'], 'no-synthetic-user-match')
        self.assertEqual(v.matched_rows(v.source_rows(data['source']), data['cases'][1]['request']), [])
        self.assertEqual(v.workbook_rows(files[data['cases'][1]['download']['filename']]), [['mail', 'GUID', 'SAM', 'DN', 'SID', 'whenCreated']])
        with self.assertRaisesRegex(ValueError, 'evidence version'):
            v.validate(data, files.__getitem__, {'fixtureOnly': True})
        import validate_user_asset_export_v3_terminal_evidence as terminal
        for bit in ('set', 'clear', 'not_returned'):
            with self.subTest(legacy=bit), self.assertRaisesRegex(ValueError, 'canonical request fields'):
                terminal.request_bound({**data['cases'][0]['request'], 'uacDisableBit': bit}, data['source'], v.source_rows(data['source']))

    def test_nullable_uint32_partitions_have_independent_cells(self):
        data, _ = mixed_source()
        rows = v.source_rows(data['source'])
        self.assertEqual([row['userAccountControl'] for row in rows], [None, 0, 2, 512, 514, 2147483648, 4294967295])
        request = data['cases'][0]['request']
        for bit, indices in [(None, [0, 1, 2, 3, 4, 5, 6]), ('set', [2, 4, 6]), ('clear', [1, 3, 5]), ('not_returned', [0])]:
            with self.subTest(bit=bit):
                selected = {**request, **({} if bit is None else {'uacDisableBit': bit})}
                cells = [MIXED_CELLS[i] for i in indices]
                self.assertEqual(v.matched_rows(rows, selected, allow_uac=True), cells)
                self.assertEqual(v.workbook_rows(xlsx([['mail', 'GUID', 'SAM', 'DN', 'SID', 'whenCreated'], *cells]))[1:], cells)
                self.assertEqual(v.matched_rows(rows, {**selected, 'search': 'no-synthetic-user-match'}, allow_uac=True), [])

    def test_raw_uac_rejected_before_kind_and_search_filtering(self):
        for kind in ('user', 'group', 'computer'):
            for invalid in ('missing', True, False, -1, 4294967296, 1.5, '2', [], {}):
                for keyword in ('user-', 'no-synthetic-user-match'):
                    with self.subTest(kind=kind, invalid=invalid, keyword=keyword):
                        data, body = mixed_source()
                        base = body['objects'][-1]['base']
                        base.update(kind=kind, samAccountName='excluded', distinguishedName='CN=excluded')
                        if invalid == 'missing':
                            base.pop('userAccountControl')
                        else:
                            base['userAccountControl'] = invalid
                        replace_source(data, body)
                        with self.assertRaisesRegex(ValueError, 'raw UAC presence|raw nullable uint32 UAC'):
                            v.matched_rows(v.source_rows(data['source']), {**data['cases'][0]['request'], 'search': keyword, 'uacDisableBit': 'not_returned'}, allow_uac=True)

    def test_closed_request_shapes_and_enum_presence(self):
        data, _ = mixed_source()
        rows, request = v.source_rows(data['source']), data['cases'][0]['request']
        for value in (None, '', True, False, 2, [], {}, 'SET', 'unknown', ' set'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                v.matched_rows(rows, {**request, 'uacDisableBit': value}, allow_uac=True)
        for key in v.REQUEST_KEYS:
            broken = copy.deepcopy(request); broken.pop(key)
            with self.subTest(missing=key), self.assertRaises(ValueError):
                v.matched_rows(rows, broken)
        with self.assertRaises(ValueError):
            v.matched_rows(rows, {**request, 'unknown': 'x'})
        for value in ('{"uacDisableBit":"set","uacDisableBit":"set"}', '{"search":"user-","search":"user-"}'):
            with self.assertRaisesRegex(ValueError, 'duplicate JSON member'):
                v.strict_json(value)

    def test_response_filter_and_profile_mismatches_rejected(self):
        for key in ('create', 'receipt', 'detail'):
            for value in ('clear', 'not_returned', None, ''):
                self.data, self.files = success_specimen()
                self.data['cases'][1][key]['request']['uacDisableBit'] = value
                with self.subTest(response=key, value=value), self.assertRaisesRegex(ValueError, 'immutable response request'):
                    self.check()
        for change in ({'search': 'no-synthetic-user-match'}, {'uacDisableBit': 'clear'}, {'uacDisableBit': 'not_returned'}):
            self.data, self.files = success_specimen()
            case = self.data['cases'][1]
            case['request'].update(change)
            for key in ('create', 'receipt', 'detail'):
                case[key]['request'] = copy.deepcopy(case['request'])
            with self.subTest(profile=change), self.assertRaisesRegex(ValueError, 'independent canonical success request'):
                self.check()
        self.data, self.files = success_specimen()
        self.data['cases'][1]['request'].pop('uacDisableBit')
        with self.assertRaisesRegex(ValueError, 'independent canonical success request'):
            self.check()
        self.data, self.files = success_specimen()
        self.data['cases'][0]['request']['uacDisableBit'] = 'clear'
        with self.assertRaisesRegex(ValueError, 'independent canonical success request'):
            self.check()

    def test_complete_duplicate_preserving_query_binding(self):
        mutations = [
            lambda q: q.append(copy.deepcopy(q[0])),
            lambda q: q.append(['uacDisableBit', 'set']),
            lambda q: q.append(['actorId', '2']),
            lambda q: q.append(['unexpected', 'x']),
            lambda q: q.pop(),
            lambda q: q.__setitem__(0, ['domainId', 'other']),
            lambda q: q.__setitem__(1, ['expectedRevision', '999']),
            lambda q: q.__setitem__(2, ['expectedCredentialRevision', '999']),
            lambda q: q.__setitem__(3, ['observationId', 'other']),
            lambda q: q.__setitem__(4, ['search', 'no-synthetic-user-match']),
            lambda q: q.__setitem__(0, ['domainId', 1]),
            lambda q: q.__setitem__(0, 'domainId'),
        ]
        for mutate in mutations:
            self.data, self.files = success_specimen()
            mutate(self.data['cases'][1]['listQueryEntries'])
            with self.subTest(mutation=mutate), self.assertRaisesRegex(ValueError, 'list query'):
                self.check()
        for version in (1, True, 3):
            self.data, self.files = success_specimen(); self.data['format'] = version
            with self.subTest(version=version), self.assertRaisesRegex(ValueError, 'evidence version'):
                self.check()
        self.data, self.files = success_specimen()
        self.data['cases'][1].pop('listQueryEntries')
        with self.assertRaisesRegex(ValueError, 'case fields'):
            self.check()

    def test_semantic_source_tampering_after_all_source_hashes_rebound(self):
        for field, value, reason in [('userAccountControl', 514, 'default LDAP user UAC distribution'), ('userAccountControl', None, 'default LDAP user UAC distribution'), ('mailBytes', b64(b'changed@example.test'), 'full workbook cells/order/source match')]:
            self.data, self.files = success_specimen()
            body = v.strict_json(v.decode_bytes(self.data['source']['bodyBase64']))
            body['objects'][1]['base' if field == 'userAccountControl' else 'supplemental'][field] = value
            replace_source(self.data, body)
            with self.subTest(field=field, value=value), self.assertRaisesRegex(ValueError, reason):
                self.check()

    def test_mixed_uac_semantics_change_even_after_rehash(self):
        data, body = mixed_source()
        request = {**data['cases'][0]['request'], 'uacDisableBit': 'set'}
        expected_cells = [MIXED_CELLS[2], MIXED_CELLS[4], MIXED_CELLS[6]]
        workbook = xlsx([['mail', 'GUID', 'SAM', 'DN', 'SID', 'whenCreated'], *expected_cells])
        body['objects'][1]['base']['userAccountControl'] = 2
        replace_source(data, body)
        self.assertEqual(data['cases'][0]['database']['sourceSHA256'], data['source']['sha256'])
        self.assertNotEqual(v.matched_rows(v.source_rows(data['source']), request, allow_uac=True), v.workbook_rows(workbook)[1:])
        self.assertEqual(v.matched_rows(v.source_rows(data['source']), request, allow_uac=True), [MIXED_CELLS[1], *expected_cells])

    def test_ignored_set_filter_cannot_present_unfiltered_workbook(self):
        case, unfiltered = self.data['cases'][1], self.data['cases'][0]
        blob = self.files[unfiltered['download']['filename']]
        self.files[case['download']['filename']] = blob
        case['detail']['artifact'].update(rowCount=12, byteCount=len(blob), sha256=v.sha(blob))
        case['download']['headers'].update({'x-adtr-row-count': '12', 'content-length': str(len(blob)), 'x-adtr-artifact-sha256': v.sha(blob)})
        case['database'].update(rowCount=12, byteCount=len(blob), sha256=v.sha(blob), chunks=[{'ordinal': 0, 'bodyBase64': b64(blob), 'sha256': v.sha(blob)}])
        with self.assertRaisesRegex(ValueError, 'artifact row count'):
            self.check()

    def test_filtered_browser_source_contract_and_mutations(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'web/e2e/user-asset-export-v3.spec.ts').read_text()
        config = (root / 'web/playwright.config.ts').read_text()
        success_browser_contract(source, config)
        for old, new in [('selectOption("set")', 'selectOption("clear")'), ('name: "搜索用户"', 'name: "wrong"'), ('name: "创建用户导出"', 'name: "wrong"'), ('name: "下载用户 XLSX"', 'name: "wrong"'), ('900_000', '990_000'), ('owner: 9, exportTasks: 2', 'owner: 10, exportTasks: 3'), ('request: expectedRequest,', 'request: created.request,'), ('format: 2,', 'format: 1,'), ('source.selection.domainId', 'initial.selection.domainId')]:
            changed = source.replace(old, new)
            with self.subTest(mutation=old), self.assertRaises(ValueError):
                success_browser_contract(changed, config)
        with self.assertRaises(ValueError):
            success_browser_contract(source + '\nawait proof(page);', config)
        for old, new in [('retries: 0,', 'retries: 1,'), ('workers: 1,', 'workers: 2,'), ('fullyParallel: false,', 'fullyParallel: true,')]:
            with self.subTest(config=old), self.assertRaises(ValueError):
                success_browser_contract(source, config.replace(old, new))

    def test_one_pass_ooxml_and_unicode_literal_fidelity(self):
        cells = ['=1+1', '+123', '-123', '@formula', '\t=1', '<&>"\r\n\t😀_x0041__x005F_', '00123', 'https://example.test']
        self.assertEqual(v.workbook_rows(xlsx([[c] for c in cells])), [[c] for c in cells])

    def test_corruption_matrix(self):
        mutations = [
            lambda d: d.update(format=True),
            lambda d: d['provenance'].update(wrong=True),
            lambda d: d['source'].update(sha256='0'*64),
            lambda d: d.update(actorId=2),
            lambda d: d['cases'].pop(),
            lambda d: d['cases'][0]['request'].update(observationId='changed'),
            lambda d: d['cases'][0]['request'].update(search='i'),
            lambda d: d['cases'][0]['request'].update(startTm='2026-10-01T00:00:00+08:00', endTm='2026-10-02T00:00:00+08:00'),
            lambda d: d['cases'][0]['request'].update(screenColumns=['guid', 'guid']),
            lambda d: d['cases'][0]['receipt'].update(taskID='other'),
            lambda d: d['cases'][0]['detail'].update(downloadReady=False),
            lambda d: d['cases'][0]['detail']['artifact'].update(rowCount=10),
            lambda d: d['cases'][0]['detail']['artifact'].update(byteCount=True),
            lambda d: d['cases'][0]['download']['headers'].update({'x-adtr-user-id': '2'}),
            lambda d: d['cases'][0]['database'].update(sourceSHA256='0'*64),
            lambda d: d['cases'][0]['database']['chunks'][0].update(ordinal=1),
            lambda d: d['cases'][0]['database']['chunks'][0].update(sha256='0'*64),
            lambda d: d['cases'][0].update(pageSize=50),
            lambda d: d['cases'][0]['screenshot'].update(filename='../escape.png'),
            lambda d: d['cases'][0]['create'].update(actorPassword='not-a-real-secret'),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.data, self.files = success_specimen()
                mutate(self.data)
                with self.assertRaises((ValueError, KeyError)):
                    self.check()

    def test_artifact_tamper(self):
        key = self.data['cases'][0]['download']['filename']
        self.files[key] += b'changed'
        with self.assertRaises(ValueError):
            self.check()

    def test_xlsx_active_content_and_structure_rejected(self):
        raw = xlsx([['SAM'], ['=literal']])
        sheet = 'xl/worksheets/sheet1.xml'
        mutations = [
            (sheet, lambda b: b.replace(b'<is>', b'<f>1+1</f><is>', 1)),
            (sheet, lambda b: b.replace(b't="inlineStr"', b't="n"', 1)),
            (sheet, lambda b: b.replace(b'r="A2"', b'r="B2"')),
            (sheet, lambda b: b.replace(b'</worksheet>', b'<hyperlinks/></worksheet>')),
            (sheet, lambda b: b'<!DOCTYPE worksheet [<!ENTITY a "b">]>' + b),
            (sheet, lambda b: b.decode().encode('utf-16')),
            (sheet, lambda b: b.replace(b'=literal', b'_x0000_')),
            ('[Content_Types].xml', lambda b: b.replace(b'spreadsheetml.sheet.main+xml', b'ms-excel.sheet.macroEnabled.main+xml')),
            ('xl/vbaProject.bin', lambda _: b'forbidden'),
            ('xl/_rels/workbook.xml.rels', lambda b: b.replace(b'Target=', b'TargetMode="External" Target=')),
            ('xl/workbook.xml', lambda b: b.replace(b'UserAssets', b'Audit')),
        ]
        for name, change in mutations:
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    v.workbook_rows(mutate_zip(raw, name, change))

    def test_duplicate_zip_and_json_rejected(self):
        raw = xlsx([['GUID']])
        stream = io.BytesIO(raw)
        with zipfile.ZipFile(stream, 'a') as z:
            import warnings
            with warnings.catch_warnings():
                warnings.simplefilter('ignore', UserWarning)
                z.writestr('xl/workbook.xml', '<workbook/>')
        with self.assertRaises(ValueError):
            v.workbook_rows(stream.getvalue())
        with self.assertRaises(ValueError):
            v.strict_json('{"a":1,"a":2}')

    def test_nonascii_search_source_is_outside_fixed_suite(self):
        rows = v.source_rows(self.data['source'])
        rows[0]['mail'] = 'İ@example.test'
        with self.assertRaises(ValueError):
            v.matched_rows(rows, self.data['cases'][0]['request'])

    def test_observer_and_fixture_contract(self):
        root = Path(__file__).resolve().parents[1]
        observer = (root / 'web/e2e/directory-v2-response-observer.ts').read_text()
        for suffix in ('', '/receipt', '/detail', '/cancel'):
            self.assertEqual(observer.count('"/api/user-assets/v3/exports' + suffix + '"'), 2)
        self.assertNotIn('"/api/user-assets/v3/exports/download"', observer)
        helper = (root / 'web/e2e/group-assets-v3-fixture.ts').read_text()
        self.assertIn('userAssetExport?: boolean', helper)
        self.assertIn('ADTR_E2E_USER_ASSET_EXPORT_V3 !== "true"', helper)
        self.assertIn('if (options.emptyRefresh && options.userAssetExport)', helper)
        self.assertIn('requireFixture(options.userAssetExport)', helper)
        suite = v.re.sub(r'\s+', ' ', (root / 'web/e2e/user-asset-export-v3.spec.ts').read_text())
        self.assertIn('owner: 7, producer: 9', suite)
        self.assertIn('owner: 9, exportTasks: 2', suite)
        self.assertNotIn('route.fulfill', suite)
        self.assertNotIn('waitForTimeout', suite)

    def test_row_overflow_rejected(self):
        with self.assertRaises(ValueError):
            v.workbook_rows(xlsx([['GUID']] * 10002))

    def test_illegal_xml_and_utf16_bound(self):
        for value in ['bad\x00', '😀' * 16384]:
            with self.assertRaises((ValueError, v.ET.ParseError)):
                v.workbook_rows(xlsx([[value]]))
        self.assertEqual(v.workbook_rows(xlsx([['😀' * 16383 + 'x']])), [['😀' * 16383 + 'x']])


if __name__ == '__main__':
    unittest.main()
