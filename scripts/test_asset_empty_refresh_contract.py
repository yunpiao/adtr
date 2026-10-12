"""Validator fixtures are synthetic unit inputs, never real execution evidence."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import validate_asset_empty_refresh as v
import test_auth_e2e as harness


def sha(data):
    return hashlib.sha256(data).hexdigest()


def fixture(root, kind):
    selection = {'domainId': 'synthetic-domain', 'revision': '2', 'credentialRevision': '3'}
    pins = {'accountId': 'synthetic-account', 'accountCredentialRevision': '1', 'policyRevision': 'a' * 64,
            'grantRoleId': 'platform_admin', 'grantRevision': '1'}
    source = dict(zip(v.SOURCE_FIELDS, ['dc.synthetic.invalid', 'dc.synthetic.invalid', 'synthetic.invalid',
                  'dc=synthetic,dc=invalid', '2026-10-12T12:00:00Z', '2026-10-12T12:00:01Z', 1000, 2]))
    rows = [{**dict.fromkeys(v.ROW_FIELDS), 'objectGUID': f'{i:08x}-0101-0101-0101-010101010101',
             'distinguishedName': f'CN={kind}-{i},dc=synthetic,dc=invalid', 'kind': kind,
             'objectClass': [kind, 'top'], 'samAccountName': None if i == 23 else f'{kind}-{i:02d}' + ('$' if kind == 'computer' else ''), 'userAccountControl': 0} for i in range(1, 24)]
    base_fields = ['objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl']
    objects = [{'base': {k: row[k] for k in base_fields}, 'supplemental': {}, 'identity': {}} for row in rows]
    objects += [{'base': {'kind': 'user'}} for _ in range(7)]
    data = {'format': 1, 'kind': kind, 'actorId': '1', 'selection': selection,
            'request': {'domainId': selection['domainId'], 'expectedRevision': '2', 'expectedCredentialGeneration': '3',
                        'idempotencyKey': f'{kind}-third-empty-real-wire'}}
    sources = []
    for index, (key, task, count) in enumerate([('firstStored', 'first-task', 30), ('oldStored', 'old-task', 30), ('emptyStored', 'empty-task', 0)]):
        observed = {**source, 'started_at': f'2026-10-12T12:0{index}:00Z', 'completed_at': f'2026-10-12T12:0{index}:01Z', 'pages': 1 if count == 0 else 2}
        sources.append(observed)
        body = json.dumps({'dictionaryVersion': 3, 'objects': objects if count else [], 'source': observed}, separators=(',', ':')).encode()
        data[key] = {'taskUUID': task, 'actorId': '1', 'selection': selection.copy(), 'sourcePins': pins.copy(),
                     'bodyBase64': base64.b64encode(body).decode(), 'sha256': sha(body), 'dictionaryVersion': 3,
                     'count': count, 'taskState': 'succeeded', 'taskName': 'domain.directory_read.v3', 'payloadVersion': 1, 'maxAttempts': 1, 'attempt': 1, 'purpose': 'domain.directory_read.v3', 'useState': 'quiesced',
                     'reason': 'executor_returned', 'dependencies': 0}
    base = {'domainId': selection['domainId'], 'expectedRevision': '2', 'expectedCredentialRevision': '3', 'pageIdx': '1', 'pageSize': '10'}
    for name, task, index, page, size, pin in [
            ('oldBefore', 'old-task', 1, 1, 50, True), ('oldPinned', 'old-task', 1, 2, 10, True),
            ('latest', 'empty-task', 2, 1, 10, False), ('refreshed', 'empty-task', 2, 1, 10, False),
            ('emptyPinned', 'empty-task', 2, 1, 10, True), ('historyFirst', 'first-task', 0, 1, 50, True),
            ('historySecond', 'old-task', 1, 1, 50, True)]:
        query = {**base, 'pageIdx': str(page), 'pageSize': str(size)}
        if pin:
            query['observationId'] = task
        total = 0 if index == 2 else 23
        value = {'dictionaryVersion': 3, 'selection': selection.copy(), 'available': True, 'observationId': task,
                 'source': sources[index], 'observedGroupCount' if kind == 'group' else 'observedComputerCount': total,
                 'page': {'pageIdx': page, 'pageSize': size, 'total': total, 'totalPage': (total + size - 1) // size},
                 'list': copy.deepcopy(rows[(page - 1) * size:page * size]) if total else []}
        data[name] = {'query': query, 'value': value}
        if name in {'oldPinned', 'refreshed', 'emptyPinned'}:
            query['search'] = ''
            data[name]['consumed'] = {'byteLength': 1000, 'sha256': 'e' * 64, 'state': 'complete', 'uncanceledEOF': True,
                                      'eof': True, 'status': 200, 'actor': '1', 'cache': 'no-store', 'readers': 1,
                                      'released': 1, 'signalAborted': False, 'cancelCalls': 0}
    for name, search in [('oldSearch', f'{kind}-22'), ('oldCleared', '')]:
        data[name] = copy.deepcopy(data['oldPinned'])
        data[name]['query'].update(pageIdx='1', search=search)
        data[name]['value']['list'] = [copy.deepcopy(rows[21])] if search else copy.deepcopy(rows[:10])
        data[name]['value']['page'] = {'pageIdx': 1, 'pageSize': 10, 'total': 1 if search else 23, 'totalPage': 1 if search else 3}
    data['screenshots'] = []
    for stage, task in [('old-pinned', 'old-task'), ('refreshed-empty', 'empty-task')]:
        for width, height in [(1440, 900), (390, 844)]:
            name = f'{kind}-assets-v3-empty-refresh-{stage}-{width}.png'
            png = b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR' + struct.pack('>II', width, height)
            (root / name).write_bytes(png)
            data['screenshots'].append({'stage': stage, 'observationId': task, 'filename': name,
                                       'viewport': {'width': width, 'height': height}, 'geometry': {'width': width, 'scroll': width}, 'sha256': sha(png)})
    data['provenance'] = {'checkoutCommit': 'a' * 40, 'checkoutTree': 'b' * 40, 'checkoutParents': ['c' * 40],
                          'dirty': False, 'eventHeadCommit': 'd' * 40, 'eventBaseCommit': 'c' * 40,
                          'workflowCommit': 'a' * 40, 'runID': '1', 'runAttempt': '1',
                          'sourceSHA256': {p: sha((root / p).read_bytes()) for p in v.SOURCES}}
    return data


class EmptyRefreshContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='adtr-refresh-validator-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in v.SOURCES:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('synthetic validator input only\n')
        self.git = patch.object(v, 'git', side_effect=lambda root, *args: {
            ('status', '--porcelain'): '',
            ('rev-parse', 'HEAD'): 'a' * 40, ('rev-parse', 'HEAD^{tree}'): 'b' * 40,
            ('cat-file', '-p', 'HEAD'): 'tree ' + 'b' * 40 + '\nparent ' + 'c' * 40 + '\n\nmessage'}[args])
        self.git.start()
        self.addCleanup(self.git.stop)

    def test_transition_and_validation_are_only_in_existing_primary_suites(self):
        self.assertEqual(harness.EMPTY_TRANSITION_SUITES, {'group-assets-v3', 'computer-assets-v3'})
        with patch.object(harness.subprocess, 'run') as run:
            for kind in ['group', 'computer']:
                harness.validate_asset_refresh_evidence(kind + '-assets-v3')
                run.assert_called_with([sys.executable, 'scripts/validate_asset_empty_refresh.py',
                                        '--kind', kind, '--evidence-root', 'web/test-results', '--source-root', '.'],
                                       check=True, timeout=30)
            run.reset_mock()
            for suite in ['auth', 'directory-v3-controls', 'group-assets-v3-readers', 'computer-assets-v3-readers']:
                harness.validate_asset_refresh_evidence(suite)
            run.assert_not_called()
        text = Path('scripts/test_auth_e2e.py').read_text()
        self.assertIn('empty_transition=args.suite in EMPTY_TRANSITION_SUITES', text)
        self.assertLess(text.index('validate_asset_refresh_evidence(args.suite)'), text.index('diagnostics.phase("browser-pass")'))

    def test_failed_evidence_validation_is_not_a_browser_pass(self):
        with patch.object(harness.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, ['validator'])):
            with self.assertRaises(subprocess.CalledProcessError):
                harness.validate_asset_refresh_evidence('group-assets-v3')

    def test_primary_setup_only_moves_existing_writes_with_actor_local_proofs(self):
        for kind, title in [('group', 'Group'), ('computer', 'Computer')]:
            fixture_source = Path(f'web/e2e/{kind}-assets-v3-fixture.ts').read_text()
            primary = Path(f'web/e2e/{kind}-assets-v3.spec.ts').read_text()
            readers = Path(f'web/e2e/{kind}-assets-v3-readers.spec.ts').read_text()
            self.assertIn('options: { emptyRefresh?: boolean } = {}', fixture_source)
            self.assertIn('options.emptyRefresh && !process.env.ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR', fixture_source)
            self.assertIn(f'setup{title}Fixture(page, context, {{ emptyRefresh: true }})', primary)
            self.assertIn(f'setup{title}Fixture(page, context)', readers)
            self.assertNotIn('emptyRefresh', readers)
            provision = fixture_source.split('const provisionAccount = async (', 1)[1].split('const earlyAccountId', 1)[0]
            self.assertIn('await proof(page, actorPassword, actorAuth)', provision)
            self.assertIn('actorPassword,\n      actorAuth,\n      "/api/credential-use"', provision)
            self.assertEqual(provision.count('.ID).toBe(provisionActor)'), 2)
            self.assertIn('verificationState: "unverified"', provision)
            self.assertIn('domainId,', provision)
            self.assertEqual(fixture_source.count('"/api/operation-accounts/create"'), 1)
            early = fixture_source.index('const earlyAccountId = options.emptyRefresh')
            create = fixture_source.index('"/api/access/users/create"')
            login = fixture_source.index('await login(page, producerUsername, producerInitial)')
            reuse = fixture_source.index('earlyAccountId ?? (await provisionAccount(actorId, password, auth))')
            binding = fixture_source.index('"/api/domains/credential-source/reference"')
            csrf = fixture_source.index('const headers = {')
            self.assertLess(early, create)
            self.assertLess(create, login)
            self.assertLess(login, reuse)
            self.assertLess(reuse, binding)
            self.assertLess(binding, csrf)
            self.assertIn('const denied = await post("/api/directory/v3/sync", sync, 403)', fixture_source)
            self.assertIn('explicitlyGranted: false, eligible: false', fixture_source)
            self.assertIn('"/api/directory-credential-use/v2"', fixture_source)
            self.assertNotIn('UPDATE adtr.auth_attempts', fixture_source)
            self.assertNotIn('DELETE FROM adtr.auth_attempts', fixture_source)

    def test_provenance_manifest_matches_the_browser_capture(self):
        import re
        text = Path('web/e2e/asset-empty-refresh.ts').read_text().split('const sourceFiles = [', 1)[1].split('].sort()', 1)[0]
        self.assertEqual(set(re.findall(r'"([^"\n]+)"', text)), v.SOURCES)

    def test_valid_group_and_computer(self):
        for kind in ['group', 'computer']:
            with self.subTest(kind=kind):
                v.validate(fixture(self.root, kind), self.root, self.root, kind)

    def test_computer_search_requires_real_stored_sam_dollar_suffix(self):
        data = fixture(self.root, 'computer')
        for key in ['firstStored', 'oldStored']:
            body = json.loads(base64.b64decode(data[key]['bodyBase64']))
            body['objects'][21]['base']['samAccountName'] = 'computer-22'
            encoded = json.dumps(body, separators=(',', ':')).encode()
            data[key]['bodyBase64'], data[key]['sha256'] = base64.b64encode(encoded).decode(), sha(encoded)
        for key in ['oldBefore', 'oldPinned', 'oldSearch', 'oldCleared', 'historyFirst', 'historySecond']:
            for row in data[key]['value']['list']:
                if row['samAccountName'] == 'computer-22$':
                    row['samAccountName'] = 'computer-22'
        with self.assertRaisesRegex(ValueError, 'exact observation response'):
            v.validate(data, self.root, self.root, 'computer')

    def test_exact_source_pin_state_and_history_rejections(self):
        mutations = [
            ('format', True), ('actorId', '2'), ('kind', 'user'),
            ('selection.revision', '9'), ('request.expectedRevision', '9'), ('request.idempotencyKey', 'wrong'),
            ('emptyStored.taskUUID', 'old-task'), ('emptyStored.actorId', '2'), ('emptyStored.selection.credentialRevision', '4'),
            ('emptyStored.sourcePins.accountCredentialRevision', '2'), ('oldStored.sourcePins.policyRevision', 'f' * 64),
            ('emptyStored.taskState', 'failed'), ('emptyStored.taskName', 'domain.directory_read.v2'), ('emptyStored.attempt', 2), ('emptyStored.maxAttempts', 2), ('emptyStored.useState', 'opened'), ('emptyStored.reason', 'never_opened_terminal'),
            ('emptyStored.count', True), ('emptyStored.dictionaryVersion', 2), ('emptyStored.dependencies', 1),
            ('emptyStored.sha256', 'f' * 64), ('emptyStored.bodyBase64', '!!!'),
            ('oldPinned.query.observationId', 'empty-task'), ('oldPinned.query.search', 'hidden-filter'),
            ('oldPinned.query.pageIdx', '1'), ('oldSearch.query.observationId', 'empty-task'), ('oldSearch.query.search', ''), ('oldSearch.value.page.total', 23), ('oldSearch.value.observedGroupCount', 1), ('oldSearch.value.list', []), ('oldSearch.consumed.signalAborted', True), ('oldCleared.query.observationId', 'empty-task'), ('oldCleared.query.search', 'group-22'), ('latest.query.observationId', 'old-task'),
            ('latest.value.available', False), ('latest.value.observedGroupCount', None), ('latest.value.page.total', 23),
            ('latest.value.observationId', 'old-task'), ('emptyPinned.query.observationId', 'old-task'),
            ('refreshed.query.observationId', 'empty-task'), ('refreshed.value.source.domain', 'changed.invalid'),
            ('historyFirst.value.observationId', 'empty-task'), ('historySecond.value.list', []),
            ('oldPinned.consumed.actor', '2'), ('refreshed.consumed.signalAborted', True),
            ('emptyPinned.consumed.eof', False), ('emptyPinned.consumed.released', 0),
            ('provenance.dirty', True), ('provenance.checkoutCommit', 'f' * 40), ('provenance.checkoutTree', 'f' * 40),
            ('provenance.checkoutParents', []), ('provenance.sourceSHA256', {}),
        ]
        for path, value in mutations:
            with self.subTest(path=path):
                data = copy.deepcopy(fixture(self.root, 'group'))
                target = data
                parts = path.split('.')
                for part in parts[:-1]:
                    target = target[part]
                target[parts[-1]] = value
                with self.assertRaises((ValueError, KeyError, TypeError)):
                    v.validate(data, self.root, self.root, 'group')

    def test_canonical_empty_bytes_and_chronology_rejections_even_with_rehashed_body(self):
        for variant in ['whitespace', 'objects', 'duplicate', 'time', 'source', 'pages']:
            with self.subTest(variant=variant):
                data = fixture(self.root, 'group')
                p = data['emptyStored']
                body = base64.b64decode(p['bodyBase64'])
                obj = json.loads(body)
                if variant == 'whitespace':
                    body += b'\n'
                elif variant == 'duplicate':
                    body = body.replace(b'"objects":[]', b'"objects":[],"objects":[]')
                else:
                    if variant == 'objects': obj['objects'] = [{'base': {'kind': 'group'}}]
                    if variant == 'time': obj['source']['completed_at'] = '2026-10-12T11:00:00Z'
                    if variant == 'source': obj['source']['server_name'] = 'real.invalid'
                    if variant == 'pages': obj['source']['pages'] = 2
                    body = json.dumps(obj, separators=(',', ':')).encode()
                p['bodyBase64'], p['sha256'] = base64.b64encode(body).decode(), sha(body)
                with self.assertRaises(ValueError):
                    v.validate(data, self.root, self.root, 'group')

    def test_screenshot_binding_hash_geometry_and_symlink_rejections(self):
        for field, value in [('observationId', 'first-task'), ('filename', '../escape.png'), ('sha256', 'f' * 64),
                             ('viewport', {'width': 800, 'height': 600}), ('geometry', {'width': 1440, 'scroll': 1442})]:
            data = fixture(self.root, 'group')
            data['screenshots'][0][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                v.validate(data, self.root, self.root, 'group')
        data = fixture(self.root, 'group')
        path = self.root / data['screenshots'][0]['filename']
        target = self.root / 'other.png'
        path.rename(target)
        path.symlink_to(target)
        with self.assertRaises(ValueError):
            v.validate(data, self.root, self.root, 'group')

    def test_closed_json_budget_and_duplicate_keys(self):
        path = self.root / 'proof.json'
        for content in ['{"format":1,"format":1}', ' ' * (512 * 1024 + 1)]:
            path.write_text(content)
            with self.assertRaises(ValueError):
                v.load(path)
        path.unlink()
        path.symlink_to(self.root / 'scripts/validate_asset_empty_refresh.py')
        with self.assertRaises(ValueError):
            v.load(path)

    def test_raw_parent_headers_survive_a_shallow_clone(self):
        # Contract only: no evidence is manufactured from this temporary repo.
        self.git.stop()
        repo = self.root / 'repo'
        repo.mkdir()
        run = lambda *args: subprocess.check_output(['git', *args], cwd=repo, text=True, stderr=subprocess.DEVNULL).strip()
        run('init', '-q')
        run('config', 'user.email', 'fixture@example.invalid')
        run('config', 'user.name', 'Fixture')
        (repo / 'file').write_text('one')
        run('add', 'file'); run('commit', '-qm', 'one')
        parent = run('rev-parse', 'HEAD')
        (repo / 'file').write_text('two')
        run('commit', '-qam', 'two')
        shallow = self.root / 'shallow'
        subprocess.run(['git', 'clone', '-q', '--depth=1', repo.as_uri(), str(shallow)], check=True)
        self.assertEqual(v.git(shallow, 'show', '-s', '--format=%P', 'HEAD'), '')
        self.assertIn('parent ' + parent, v.git(shallow, 'cat-file', '-p', 'HEAD'))


if __name__ == '__main__':
    unittest.main()
