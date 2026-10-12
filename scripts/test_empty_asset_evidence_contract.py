"""Validator unit tests use synthetic documents, never count as browser evidence."""
import copy
import hashlib
import json
import struct
import tempfile
import subprocess
from unittest.mock import patch
import unittest
from pathlib import Path
from validate_empty_asset_evidence import SOURCES, EXPECTED_FILES, discover, load, validate

ROOT = Path(__file__).resolve().parents[1]


class EmptyAssetEvidenceContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        selection = {'domainId': 'synthetic-domain', 'revision': '2', 'credentialRevision': '2'}
        observation = {'observationId': 'synthetic-task', 'source': {
            'server_name': 'dc.synthetic.invalid', 'dc_host_name': 'dc.synthetic.invalid',
            'domain': 'synthetic.invalid', 'naming_context': 'dc=synthetic,dc=invalid',
            'started_at': '2026-10-12T00:00:00Z', 'completed_at': '2026-10-12T00:00:11Z',
            'elapsed_milliseconds': 11000, 'pages': 5}}
        self.data = {'format': 1, 'actorId': '2', 'selection': selection, 'observation': observation,
                     'successfulTask': {'taskUUID': 'synthetic-task', 'state': 'succeeded', 'attempt': 1, 'progress': 100},
                     'successfulUse': {'state': 'quiesced', 'reason': 'executor_returned', 'dependencies': 0, 'taskState': 'succeeded'},
                     'syncPosts': 2, 'cancelPosts': 1, 'records': [], 'provenance': {
                         'checkoutCommit': 'a' * 40, 'checkoutTree': 'b' * 40, 'checkoutParents': ['c' * 40], 'dirty': False,
                         'eventHeadCommit': None, 'eventBaseCommit': None, 'workflowCommit': None, 'runID': None, 'runAttempt': None,
                         'sourceSHA256': {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest() for name in SOURCES}}}
        for stage in ['no-observation', 'observed-empty']:
            for kind in ['user', 'computer', 'group']:
                available = stage == 'observed-empty'
                value = {'dictionaryVersion': 3, 'selection': selection, 'available': available,
                         'list': [], 'page': {'pageIdx': 1, 'pageSize': 10, 'total': 0, 'totalPage': 0}}
                if kind != 'user':
                    value['observedComputerCount' if kind == 'computer' else 'observedGroupCount'] = 0 if available else None
                if available:
                    value.update(observation)
                record = {'stage': stage, 'kind': kind, 'path': f'/api/{kind}-assets/v3',
                          'query': {'domainId': 'synthetic-domain', 'expectedRevision': '2', 'expectedCredentialRevision': '2', 'pageIdx': '1', 'pageSize': '10', 'search': ''},
                          'actorId': '2', 'value': value, 'consumed': {
                              'byteLength': 500, 'sha256': 'd' * 64, 'state': 'complete', 'uncanceledEOF': True, 'eof': True,
                              'status': 200, 'actor': '2', 'cache': 'no-store', 'readers': 1, 'released': 1, 'signalAborted': False, 'cancelCalls': 0}, 'screenshots': []}
                for width, height in [(1440, 900), (390, 844)]:
                    filename = f'directory-v3-empty-assets-{stage}-{kind}-{width}.png'
                    # Minimal header tests only the validator's signature/dimensions/hash branch.
                    content = b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR' + struct.pack('>II', width, height)
                    (self.directory / filename).write_bytes(content)
                    record['screenshots'].append({'filename': filename, 'viewport': {'width': width, 'height': height},
                                                  'geometry': {'width': width, 'scroll': width}, 'sha256': hashlib.sha256(content).hexdigest()})
                record['pinned'] = {'query': {**record['query'], 'observationId': observation['observationId']},
                                    'value': copy.deepcopy(value), 'sha256': 'e' * 64, 'byteLength': 500} if available else None
                self.data['records'].append(record)

    def check(self, data=None):
        validate(self.data if data is None else data, self.directory, ROOT)

    def test_closed_shape_passes(self):
        self.check()

    def test_null_is_not_observed_zero(self):
        self.data['records'][1]['value']['observedComputerCount'] = 0
        with self.assertRaises(ValueError): self.check()

    def test_observed_zero_is_not_null(self):
        self.data['records'][5]['value']['observedGroupCount'] = None
        with self.assertRaises(ValueError): self.check()

    def test_unknown_private_fields_rejected(self):
        self.data['records'][0]['value']['unexpected'] = 'do not persist'
        with self.assertRaises(ValueError): self.check()

    def test_stale_rows_rejected(self):
        self.data['records'][3]['value']['list'] = [{'objectGUID': 'old'}]
        with self.assertRaises(ValueError): self.check()

    def test_pin_and_source_mismatch_rejected(self):
        for field in ['observationId', 'source']:
            data = copy.deepcopy(self.data)
            data['records'][3]['value'][field] = 'wrong'
            with self.subTest(field=field), self.assertRaises(ValueError): self.check(data)

    def test_explicit_pin_must_match_successful_task(self):
        self.data['records'][3]['pinned']['query']['observationId'] = 'other-task'
        with self.assertRaises(ValueError): self.check()

    def test_partial_or_cancelled_reader_rejected(self):
        for field, value in [('eof', False), ('released', 0), ('readers', 0), ('signalAborted', True), ('cancelCalls', 1), ('byteLength', 16385), ('actor', '3'), ('cache', 'public')]:
            data = copy.deepcopy(self.data)
            data['records'][3]['consumed'][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.check(data)

    def test_extra_filter_and_changed_source_revision_rejected(self):
        for field, value in [('search', 'hidden'), ('creatTm', 'asc'), ('observationId', 'wrong'), ('expectedRevision', '3')]:
            data = copy.deepcopy(self.data)
            data['records'][3]['query'][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.check(data)

    def test_missing_duplicate_or_reordered_workspace_rejected(self):
        for records in [self.data['records'][:-1], self.data['records'][::-1], [self.data['records'][0]] * 6]:
            data = copy.deepcopy(self.data)
            data['records'] = records
            with self.assertRaises(ValueError): self.check(data)

    def test_missing_or_tampered_screenshot_rejected(self):
        path = self.directory / self.data['records'][0]['screenshots'][0]['filename']
        path.write_bytes(path.read_bytes() + b'changed')
        with self.assertRaises(ValueError): self.check()
        path.unlink()
        with self.assertRaises(ValueError): self.check()

    def test_overflow_and_traversal_rejected(self):
        for field, value in [('filename', '../unsafe.png'), ('geometry', {'width': 1440, 'scroll': 1600})]:
            data = copy.deepcopy(self.data)
            data['records'][0]['screenshots'][0][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.check(data)

    def test_tampered_source_or_dirty_checkout_rejected(self):
        self.data['provenance']['sourceSHA256']['.github/workflows/ci.yml'] = '0' * 64
        with self.assertRaises(ValueError): self.check()
        self.data['provenance']['dirty'] = True
        with self.assertRaises(ValueError): self.check()

    def test_executor_and_original_controls_required(self):
        for field, value in [('successfulUse', {}), ('successfulTask', {}), ('syncPosts', 3), ('cancelPosts', 0)]:
            data = copy.deepcopy(self.data)
            data[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.check(data)

    def test_duplicate_json_keys_rejected(self):
        path = self.directory / 'evidence.json'
        path.write_text('{"format":1,"format":1}')
        with self.assertRaises(ValueError): load(path)

    def test_shallow_checkout_retains_actual_commit_parents(self):
        source, shallow = self.directory / 'git-source', self.directory / 'shallow'
        source.mkdir()
        def git(*args, cwd=source):
            return subprocess.check_output(['git', *args], cwd=cwd, text=True, stderr=subprocess.DEVNULL).strip()
        git('init', '-q', '-b', 'main')
        for message in ['first', 'second']:
            git('-c', 'user.name=Validator Test', '-c', 'user.email=validator@example.invalid', 'commit', '-q', '--allow-empty', '-m', message)
        expected = git('rev-parse', 'HEAD^')
        git('clone', '-q', '--depth=1', source.as_uri(), str(shallow))
        self.assertEqual(git('show', '-s', '--format=%P', 'HEAD', cwd=shallow), '')
        parents = [line[7:] for line in git('cat-file', '-p', 'HEAD', cwd=shallow).split('\n\n', 1)[0].splitlines() if line.startswith('parent ')]
        self.assertEqual(parents, [expected])
        helper = (ROOT / 'web/e2e/directory-v3-empty-assets.ts').read_text()
        self.assertIn('git("cat-file", "-p", "HEAD")', helper)
        self.assertNotIn('git("show", "-s", "--format=%P"', helper)

    def test_parent_directory_symlink_is_rejected(self):
        link = self.directory / 'alias'
        link.symlink_to(self.directory, target_is_directory=True)
        with self.assertRaises(ValueError): validate(self.data, link, ROOT)

    def test_fixed_root_discovers_only_unique_exact_upload_files(self):
        root = self.directory / 'checkout'
        script = root / 'scripts' / 'validate_empty_asset_evidence.py'
        output = root / 'web' / 'test-results'
        scenario = output / 'owned-scenario'
        scenario.mkdir(parents=True)
        for name in EXPECTED_FILES: (scenario / name).write_text('unit fixture only')
        with patch('validate_empty_asset_evidence.__file__', str(script)):
            expected = scenario / 'directory-v3-empty-assets-evidence.json'
            self.assertEqual(discover(output, root), expected)
            with self.assertRaises(ValueError): discover(self.directory, root)
            extra = output / 'extra'
            extra.mkdir()
            (extra / expected.name).write_text('{}')
            with self.assertRaises(ValueError): discover(output, root)
            (extra / expected.name).unlink()
            image = next(name for name in EXPECTED_FILES if name.endswith('.png'))
            (extra / image).write_text('duplicate would enter wildcard upload')
            with self.assertRaises(ValueError): discover(output, root)
            (extra / image).unlink()
            (extra / 'alias').symlink_to(self.directory, target_is_directory=True)
            with self.assertRaises(ValueError): discover(output, root)

    def test_fixed_root_rejects_missing_screenshot(self):
        root = self.directory / 'checkout'
        output = root / 'web' / 'test-results'
        output.mkdir(parents=True)
        (output / 'directory-v3-empty-assets-evidence.json').write_text('{}')
        with patch('validate_empty_asset_evidence.__file__', str(root / 'scripts' / 'validator.py')):
            with self.assertRaises(ValueError): discover(output, root)

    def test_exact_workflow_evidence_gate_and_upload(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        start = workflow.index('      - name: Validate real empty asset observation evidence\n')
        end = workflow.index('      - name: Retain synthetic profile visual evidence\n', start)
        steps = workflow[start:end]
        self.assertEqual(steps.count("if: ${{ success() && matrix.name == 'directory-v3-controls' }}"), 2)
        self.assertIn('run: python3 scripts/validate_empty_asset_evidence.py --evidence-root web/test-results --source-root .', steps)
        paths = {line.strip().split('/')[-1] for line in steps.splitlines() if line.strip().startswith('web/test-results/**/')}
        self.assertEqual(paths, EXPECTED_FILES)
        self.assertEqual(len(paths), 13)
        self.assertIn('uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02', steps)
        self.assertIn('if-no-files-found: error', steps)
        self.assertNotIn('include-hidden-files', steps)
        # This exact two-step delta is the only workflow change from published 3d9fee4.
        self.assertEqual(hashlib.sha256((workflow[:start] + workflow[end:]).encode()).hexdigest(), 'a0368741a173743aa8ffddfb6a54a115d7ac9f01d775d494475c51bac503072b')

    def test_original_controls_are_preserved_and_helpers_are_read_only(self):
        code = (ROOT / 'web/e2e/directory-v3-controls.spec.ts').read_text()
        helper = (ROOT / 'web/e2e/directory-v3-empty-assets.ts').read_text()
        for fragment in ['test.setTimeout(900_000)', 'lateFreshCode(auth)', 'progress === 41', 'executor_returned', 'expect(syncPosts).toBe(2)', 'responsesDrained', 'expect(syncPosts).toBe(0)']:
            self.assertIn(fragment, code)
        for forbidden in ['.route(', '.fulfill(', 'request.post(', 'INSERT ', 'UPDATE ', 'DELETE ', 'test.skip(', 'test.fixme(']:
            self.assertNotIn(forbidden, helper)
        self.assertIn('directoryV2ResponseWitness(response)', helper)
        self.assertIn('scripts/validate_empty_asset_evidence.py', helper)
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        self.assertIn('path: web/test-results/**/directory-v3-*', workflow)


if __name__ == '__main__': unittest.main()
