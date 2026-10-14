"""Pure-Python offline validator tests. These specimens are NOT acceptance artifacts."""
import base64
import copy
import io
import json
from pathlib import Path
import unittest
import zipfile
from xml.sax.saxutils import escape
import validate_computer_asset_export_v3_evidence as v


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
        'xl/workbook.xml': f'<workbook xmlns="{ns}"><sheets><sheet name="ComputerAssets"/></sheets></workbook>',
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
    for n in range(1, 31):
        raw_guid = f'{n:02x}' * 16
        kind = 'computer' if n <= 23 else 'user' if n <= 26 else 'group'
        ou = {'computer': 'Computers', 'user': 'Users', 'group': 'Groups'}[kind]
        name = f'{kind}-{n:02}'
        classes = ['computer', 'organizationalperson', 'person', 'top', 'user'] if kind == 'computer' else ['organizationalperson', 'person', 'top', 'user'] if kind == 'user' else ['group', 'top']
        objects.append({'base': {
            'objectGUID': '-'.join((raw_guid[:8], raw_guid[8:12], raw_guid[12:16], raw_guid[16:20], raw_guid[20:])),
            'kind': kind, 'objectClass': classes,
            'samAccountName': None if n == 23 else name + ('$' if kind == 'computer' else ''),
            'distinguishedName': f'CN={name},OU={ou},DC=synthetic,DC=invalid'},
            'supplemental': {'objectSidBytes': None if n == 23 else b64(bytes([1, 1, 0, 0, 0, 0, 0, 5, n, 0, 0, 0])),
                'mailBytes': None if n == 23 else b64(b'mail-only@example.test'),
                'whenCreatedBytes': None if n == 23 else b64(b'00010101000000.0Z' if n == 1 else b'20260101000000.0Z')}})
    raw = json.dumps({'dictionaryVersion': 3, 'objects': objects}).encode()
    source = {'bodyBase64': b64(raw), 'sha256': v.sha(raw), 'dictionaryVersion': 3, 'count': 30, 'taskState': 'succeeded', 'purpose': 'domain.directory_read.v3', 'useState': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'payloadVersion': 1, 'maxAttempts': 1, 'attempt': 1, 'taskName': 'domain.directory_read.v3', 'taskUUID': 'observation1', 'actorId': '2', 'selection': {'domainId': 'domain1', 'revision': '2', 'credentialRevision': '1'}}
    data = {'format': 1, 'provenance': {'fixtureOnly': True}, 'source': source, 'actorId': 1, 'sensitiveBudget': {'before': {'schemaVersion': 23, 'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}, 'after': {'schemaVersion': 23, 'owner': 10, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 3}}, 'cases': []}
    files = {}
    for index, name in enumerate(('default', 'allMatched', 'empty')):
        task = f'export{index}'
        request = {'domainId': 'domain1', 'expectedRevision': '2', 'expectedCredentialRevision': '1', 'observationId': 'observation1', 'search': 'computer-',
                   'startTm': ['', '0001-01-01T00:00:00Z', '2025-12-31T00:00:00Z'][index],
                   'endTm': ['', '2026-01-02T00:00:00Z', '2026-01-01T00:00:00Z'][index],
                   'creatTm': 'asc' if name == 'allMatched' else 'desc', 'screenColumns': list(v.COLUMNS) if name == 'default' else ['dn', 'guid']}
        rows = v.matched_rows(v.source_rows(source), request)
        blob = xlsx([[v.COLUMNS[c] for c in request['screenColumns']], *rows])
        filename = f'computer-assets-v3-{task}.xlsx'
        files[filename] = blob
        response = {'taskID': task, 'task': {'taskUUID': task, 'taskName': 'computer_assets.v3.export', 'state': 'succeeded'}, 'request': request}
        screenshot = f'{name}-ready.png'
        def png_chunk(kind, body):
            return len(body).to_bytes(4, 'big') + kind + body + v.zlib.crc32(kind + body).to_bytes(4, 'big')
        files[screenshot] = b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', v.struct.pack('>IIBBBBB', 1440, 900, 8, 2, 0, 0, 0)) + png_chunk(b'IDAT', v.zlib.compress((b'\x00' * (1 + 1440 * 3)) * 900)) + png_chunk(b'IEND', b'')
        count = len(rows)
        headers = {'content-disposition': f'attachment; filename="{filename}"', 'x-adtr-user-id': '1', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff', 'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'content-length': str(len(blob)), 'x-adtr-artifact-sha256': v.sha(blob), 'x-adtr-row-count': str(count)}
        data['cases'].append({'name': name, 'request': copy.deepcopy(request), 'create': {**copy.deepcopy(response), 'replayed': False}, 'receipt': copy.deepcopy(response), 'detail': {**copy.deepcopy(response), 'downloadReady': True, 'downloadStatus': 'ready', 'artifact': {'rowCount': count, 'byteCount': len(blob), 'sha256': v.sha(blob), 'filename': filename}}, 'download': {'filename': filename, 'headers': headers}, 'pageSize': 10, 'firstPageCount': min(10, count), 'database': {'taskID': task, 'actorId': '1', 'state': 'succeeded', 'kind': 'computer_assets.v3.export', 'progress': 100, 'attempt': 1, 'maxAttempts': 3, 'events': [{'action': a, 'state': 'succeeded' if a == 'finished' else 'running', 'resultVersion': i} for i, a in enumerate(['submitted', 'leased', 'started', 'progress', 'finished'])], 'sourceSHA256': source['sha256'], 'rowCount': count, 'byteCount': len(blob), 'sha256': v.sha(blob), 'chunkCount': 1, 'chunks': [{'ordinal': 0, 'bodyBase64': b64(blob), 'sha256': v.sha(blob)}]}, 'screenshot': {'filename': screenshot, 'sha256': v.sha(files[screenshot])}})
    return data, files


class EvidenceContract(unittest.TestCase):
    def setUp(self):
        self.data, self.files = specimen()

    def check(self):
        v.validate(self.data, self.files.__getitem__, {'fixtureOnly': True})

    def test_valid_unit_specimen(self):
        self.check()

    def test_inherited_user_class_is_computer_only(self):
        rows = v.source_rows(self.data['source'])
        self.assertEqual(len(rows), 23)
        self.assertTrue(all('OU=Computers,' in row['dn'] for row in rows))
        self.assertEqual(sum(row['sAMAccountName'].endswith('$') for row in rows), 22)
        self.assertEqual(rows[-1]['sAMAccountName'], '')
        self.assertEqual(rows[0]['sid'], 'S-1-5-1')
        self.assertEqual(rows[-1]['sid'], '')
        self.assertEqual(rows[0]['whenCreated'], '0001-01-01T00:00:00Z')
        self.assertEqual(rows[-1]['whenCreated'], '')

    def test_half_open_time_range_and_order_are_independent_of_workbook(self):
        source = sorted(v.source_rows(self.data['source']), key=lambda row: row['guid'])
        default, filtered, empty = [case['request'] for case in self.data['cases']]
        expected_desc = source[1:22] + source[:1] + source[22:]
        self.assertEqual(v.matched_rows(source, default), [[row[c] for c in v.COLUMNS] for row in expected_desc])
        self.assertEqual(v.matched_rows(list(reversed(source)), filtered), [[row['dn'], row['guid']] for row in source[:22]])
        self.assertEqual(v.matched_rows(source, empty), [])
        ascending = {**default, 'creatTm': 'asc'}
        self.assertEqual(v.matched_rows(list(reversed(source)), ascending), [[row[c] for c in v.COLUMNS] for row in source])

    def test_time_bounds_are_required_paired_and_strict(self):
        source = v.source_rows(self.data['source'])
        request = self.data['cases'][1]['request']
        changes = [
            {'startTm': ''}, {'endTm': ''}, {'startTm': None},
            {'startTm': '2026-01-02T00:00:00Z'},
            {'startTm': '2026-01-03T00:00:00Z'},
            {'startTm': '2026-02-30T00:00:00Z'},
            {'startTm': '2026-01-01T00:00:00+08:00'},
            {'startTm': '2026-01-01T00:00:00.1Z'},
        ]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                v.matched_rows(source, {**request, **change})
        for key in ('startTm', 'endTm'):
            changed = dict(request)
            del changed[key]
            with self.assertRaises(ValueError):
                v.matched_rows(source, changed)

    def test_source_population_classes_and_time_cannot_be_substituted(self):
        mutations = [
            lambda body: body['objects'][0]['base'].update(kind='user'),
            lambda body: body['objects'][0]['base'].update(objectClass=['computer']),
            lambda body: body['objects'][0]['base'].update(objectClass=['user']),
            lambda body: body['objects'][0]['supplemental'].update(whenCreatedBytes=None),
            lambda body: body['objects'][1]['supplemental'].update(whenCreatedBytes=b64(b'20260102000000.0Z')),
        ]
        for mutate in mutations:
            source = copy.deepcopy(self.data['source'])
            body = json.loads(base64.b64decode(source['bodyBase64']))
            mutate(body)
            raw = json.dumps(body).encode()
            source.update(bodyBase64=b64(raw), sha256=v.sha(raw))
            with self.assertRaises(ValueError):
                v.source_rows(source)

    def test_one_pass_ooxml_and_unicode_literal_fidelity(self):
        cells = ['=1+1', '+123', '-123', '@formula', '\t=1', '<&>"\r\n\t😀_x0041__x005F_', '00123', 'https://example.test']
        self.assertEqual(v.workbook_rows(xlsx([[c] for c in cells])), [[c] for c in cells])

    def test_corruption_matrix(self):
        mutations = [
            lambda d: d.update(format=True),
            lambda d: d['provenance'].update(wrong=True),
            lambda d: d['sensitiveBudget']['before'].update(schemaVersion=22),
            lambda d: d['sensitiveBudget']['after'].pop('schemaVersion'),
            lambda d: d['source'].update(sha256='0'*64),
            lambda d: d.update(actorId=2),
            lambda d: d['cases'].pop(),
            lambda d: d['cases'][0]['request'].update(observationId='changed'),
            lambda d: d['cases'][0]['request'].update(search='i'),
            lambda d: d['cases'][1]['request'].update(startTm=''),
            lambda d: d['cases'][1]['request'].update(creatTm='desc'),
            lambda d: d['cases'][2]['request'].update(startTm='', endTm=''),
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
                self.data, self.files = specimen()
                mutate(self.data)
                with self.assertRaises((ValueError, KeyError)):
                    self.check()

    def test_artifact_tamper(self):
        key = self.data['cases'][0]['download']['filename']
        self.files[key] += b'changed'
        with self.assertRaises(ValueError):
            self.check()

    def test_rehashed_page_only_or_wrong_order_workbook_is_rejected(self):
        for only_page in (True, False):
            self.data, self.files = specimen()
            case = self.data['cases'][0]
            filename = case['download']['filename']
            rows = v.workbook_rows(self.files[filename])
            altered = rows[:11] if only_page else [rows[0], *reversed(rows[1:])]
            raw = xlsx(altered)
            self.files[filename] = raw
            case['detail']['artifact'].update(byteCount=len(raw), sha256=v.sha(raw))
            case['download']['headers'].update({'content-length': str(len(raw)), 'x-adtr-artifact-sha256': v.sha(raw)})
            case['database'].update(byteCount=len(raw), sha256=v.sha(raw), chunks=[{'ordinal': 0, 'bodyBase64': b64(raw), 'sha256': v.sha(raw)}])
            with self.subTest(only_page=only_page), self.assertRaisesRegex(ValueError, 'full workbook'):
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
            ('xl/workbook.xml', lambda b: b.replace(b'ComputerAssets', b'Audit')),
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
        rows[0]['sAMAccountName'] = 'İ@example.test'
        with self.assertRaises(ValueError):
            v.matched_rows(rows, self.data['cases'][0]['request'])

    def test_observer_and_fixture_contract(self):
        root = Path(__file__).resolve().parents[1]
        observer = (root / 'web/e2e/directory-v2-response-observer.ts').read_text()
        for suffix in ('', '/receipt', '/detail', '/cancel'):
            self.assertEqual(observer.count('"/api/computer-assets/v3/exports' + suffix + '"'), 2)
        self.assertNotIn('"/api/computer-assets/v3/exports/download"', observer)
        suite = (root / 'web/e2e/computer-asset-export-v3.spec.ts').read_text()
        self.assertIn('owner: 7, producer: 9', suite)
        self.assertIn('owner: 10, exportTasks: 3', suite)
        self.assertNotIn('route.fulfill', suite)
        self.assertNotIn('waitForTimeout', suite)
        helper = (root / 'web/e2e/computer-asset-export-v3-helpers.ts').read_text()
        self.assertIn("'schemaVersion',(SELECT version FROM adtr.schema_version WHERE singleton)", helper)
        for source in ('go.mod', 'go.sum', 'Makefile'):
            self.assertIn(source, v.SOURCE_ROOTS)

    def test_computer_only_columns_and_request(self):
        for key in ('sAMAccountName', 'mail', 'computerStatus'):
            data, files = specimen()
            data['cases'][0]['request'][key] = ''
            with self.assertRaises(ValueError): v.validate(data, files.__getitem__, {'fixtureOnly': True})
        for column in ('mail', 'primaryGroupId', 'servicePrincipalNames'):
            data, files = specimen()
            data['cases'][0]['request']['screenColumns'] = [column]
            with self.assertRaises(ValueError): v.validate(data, files.__getitem__, {'fixtureOnly': True})

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
