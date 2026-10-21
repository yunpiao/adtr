"""Maintenance artifact contract and its bounded post-run validation entrypoint.

Normal unittest discovery tests the actual workflow and negative evidence cases.
--validate-evidence is the success-only CI step; it stages exactly three safe
files, never raw browser output, and explicitly does not attest runner cleanup.
"""
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import sys
import tempfile
import unittest
import zlib

from ci_workflow_contract import (restore_archive_target_observation,
                                  restore_health_recovery, restore_selection)

BASELINE = 'b0438a548e350c5748a4353e103e24938ae7119620eb78af9479be715e4221ed'
DELTA = 'scripts/fixtures/archive_target_observation_ci_delta.json'
FILES = ('archive-target-observation.json', 'archive-target-observation-1440.png',
         'archive-target-observation-390.png')
INPUT = 'web/test-results'
OUTPUT = 'web/playwright-report/archive-target-observation-validated'
PRODUCT_SOURCES = ('web/src/TaskMaintenance.tsx', 'web/src/Maintenance.test.tsx',
                   'web/e2e/maintenance.spec.ts', 'docs/task-archive-contract.md')
CI_SOURCES = ('.github/workflows/ci.yml', 'scripts/ci_workflow_contract.py', DELTA,
              'scripts/test_archive_target_observation_ci_contract.py')
COUNTS = dict(archiveForward=1, observationIdentity=4, observationDetail=2,
              healthSubmit=1, archiveBrowserRequests=2, archiveReplay=1, restore=0)
SUMMARY_KEYS = {'schemaVersion', 'case', 'status', 'originalTargetCount',
                'originalIntentSHA256', 'originalTargetsSHA256', 'operationUUID',
                'targets', 'details', 'scheduleSHA256', 'requestCounts',
                'screenshots', 'sourceSHA256'}


def require(condition):
    if not condition:
        raise ValueError('archive target evidence invalid')


def exact(value, names):
    require(type(value) is dict and set(value) == set(names))


def integer(value, low=0, high=9007199254740991):
    return type(value) is int and low <= value <= high


def digest(data):
    return hashlib.sha256(data).hexdigest()


def hash_value(value, length=64):
    return type(value) is str and re.fullmatch('[0-9a-f]{' + str(length) + '}', value) is not None


def uuid(value):
    return type(value) is str and re.fullmatch(r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}', value) is not None


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result)
        result[key] = value
    return result


def reject_constant(_):
    raise ValueError('archive target evidence invalid')


def checked_path(root, relative):
    root = Path(root).absolute()
    relative = Path(relative)
    require(not relative.is_absolute() and '..' not in relative.parts)
    # Reject symlinks in both the selected checkout and every child component.
    path = Path(root.anchor)
    for part in root.parts[1:] + relative.parts:
        path /= part
        require(not path.is_symlink())
    return path


def bounded_file(root, relative, limit):
    path = checked_path(root, relative)
    require(path.is_file() and 0 < path.stat().st_size <= limit)
    with path.open('rb') as stream:
        data = stream.read(limit + 1)
    require(0 < len(data) <= limit)
    return data


def png_size(data):
    require(32 < len(data) <= 8 * 1024 * 1024 and data[:8] == b'\x89PNG\r\n\x1a\n')
    offset, size, ended, image = 8, None, False, False
    # No arbitrary textual/EXIF metadata is copied into the safe artifact.
    metadata_lengths = {b'sRGB': 1, b'gAMA': 4, b'cHRM': 32, b'pHYs': 9}
    while offset < len(data):
        require(offset + 12 <= len(data) and not ended)
        length = int.from_bytes(data[offset:offset + 4], 'big')
        kind = data[offset + 4:offset + 8]
        end = offset + 12 + length
        require(end <= len(data))
        payload = data[offset + 8:end - 4]
        require(zlib.crc32(kind + payload) & 0xffffffff == int.from_bytes(data[end - 4:end], 'big'))
        if offset == 8:
            require(kind == b'IHDR' and length == 13)
            width, height, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', payload)
            require(integer(width, 1, 8192) and integer(height, 1, 8192) and
                    depth == 8 and color in (2, 6) and compression == filtering == interlace == 0)
            size = (width, height)
        elif kind == b'IDAT':
            require(length > 0)
            image = True
        elif kind == b'IEND':
            require(length == 0 and image and end == len(data))
            ended = True
        else:
            require(not image and kind in metadata_lengths and length == metadata_lengths[kind])
        offset = end
    require(ended and size is not None)
    return size


def identity(environment, checkout_commit, checkout_tree):
    run_id, attempt = environment.get('GITHUB_RUN_ID'), environment.get('GITHUB_RUN_ATTEMPT')
    require(type(run_id) is str and re.fullmatch(r'[1-9][0-9]{0,19}', run_id) is not None)
    require(type(attempt) is str and re.fullmatch(r'[1-9][0-9]{0,9}', attempt) is not None)
    tested = environment.get('GITHUB_SHA')
    require(hash_value(tested, 40) and hash_value(checkout_commit, 40) and hash_value(checkout_tree, 40))
    require(tested == checkout_commit and environment.get('GITHUB_REPOSITORY') == 'yunpiao/adtr')
    event = environment.get('GITHUB_EVENT_NAME')
    require(event in ('pull_request', 'push', 'workflow_dispatch', 'merge_group'))
    # In a PR workflow this is normally GitHub's tested merge commit, NOT the PR
    # head. External publication evidence must map its head/base/merge identity.
    return dict(schemaVersion=1, repository='yunpiao/adtr', runID=run_id,
                runAttempt=attempt, event=event, testedCommit=tested, testedTree=checkout_tree)


def validate_summary(root, data, images):
    exact(data, SUMMARY_KEYS)
    require(type(data['schemaVersion']) is int and data['schemaVersion'] == 1)
    require(data['case'] == 'archive_exact_targets_after_lost_response' and
            data['status'] == 'browser_passed_cleanup_not_attested' and
            type(data['originalTargetCount']) is int and data['originalTargetCount'] == 2)
    for field in ('originalIntentSHA256', 'originalTargetsSHA256', 'scheduleSHA256'):
        require(hash_value(data[field]))
    require(uuid(data['operationUUID']))
    exact(data['requestCounts'], COUNTS)
    require(all(type(data['requestCounts'][key]) is int and data['requestCounts'][key] == value
                for key, value in COUNTS.items()))
    targets = data['targets']
    require(type(targets) is list and len(targets) == 2)
    for target in targets:
        exact(target, ('taskUUID', 'archived', 'visibilityVersion'))
        require(uuid(target['taskUUID']) and target['archived'] is True and integer(target['visibilityVersion'], 1))
    identifiers = [target['taskUUID'] for target in targets]
    require(identifiers == sorted(set(identifiers)) and {t['visibilityVersion'] for t in targets} == {1, 3})
    versions = {target['taskUUID']: target['visibilityVersion'] for target in targets}
    details = data['details']
    require(type(details) is list and len(details) == 2)
    for detail in details:
        exact(detail, ('taskUUID', 'visibilityVersion', 'immutableTaskSHA256', 'eventsSHA256'))
        require(uuid(detail['taskUUID']) and detail['taskUUID'] in versions and
                integer(detail['visibilityVersion'], 1) and detail['visibilityVersion'] == versions[detail['taskUUID']] and
                hash_value(detail['immutableTaskSHA256']) and hash_value(detail['eventsSHA256']))
    require(len({detail['taskUUID'] for detail in details}) == 2)
    exact(data['sourceSHA256'], PRODUCT_SOURCES)
    for path in PRODUCT_SOURCES:
        require(hash_value(data['sourceSHA256'][path]) and
                data['sourceSHA256'][path] == digest(bounded_file(root, path, 2 * 1024 * 1024)))
    screenshots = data['screenshots']
    require(type(screenshots) is list and len(screenshots) == 2)
    for screenshot, filename, viewport in zip(screenshots, FILES[1:], ((1440, 900), (390, 844))):
        exact(screenshot, ('file', 'bytes', 'sha256', 'width', 'height', 'viewport'))
        exact(screenshot['viewport'], ('width', 'height'))
        require(screenshot['file'] == filename and hash_value(screenshot['sha256']))
        png = images[filename]
        dimensions = png_size(png)
        require(integer(screenshot['bytes'], 1) and screenshot['bytes'] == len(png) and screenshot['sha256'] == digest(png))
        require(all(integer(screenshot[key], 1, 8192) for key in ('width', 'height')) and
                (screenshot['width'], screenshot['height']) == dimensions)
        require(all(type(screenshot['viewport'][key]) is int for key in ('width', 'height')) and
                (screenshot['viewport']['width'], screenshot['viewport']['height']) == viewport)


def validate_and_stage(root, run_identity):
    exact(run_identity, ('schemaVersion', 'repository', 'runID', 'runAttempt', 'event', 'testedCommit', 'testedTree'))
    require(type(run_identity['schemaVersion']) is int and run_identity['schemaVersion'] == 1)
    require(run_identity == identity(dict(GITHUB_REPOSITORY=run_identity['repository'],
        GITHUB_RUN_ID=run_identity['runID'], GITHUB_RUN_ATTEMPT=run_identity['runAttempt'],
        GITHUB_EVENT_NAME=run_identity['event'], GITHUB_SHA=run_identity['testedCommit']),
        run_identity['testedCommit'], run_identity['testedTree']))
    input_root = checked_path(root, INPUT)
    require(input_root.is_dir())
    manifests = list(input_root.rglob(FILES[0]))
    require(len(manifests) == 1)
    manifest = manifests[0]
    folder = manifest.parent
    # Exactly one same-directory triple. Duplicate/prefix-spoofed evidence,
    # additional files, missing PNGs and symlinked parents all fail closed.
    discovered = list(input_root.rglob('archive-target-observation*'))
    require({p.absolute() for p in discovered} == {folder / name for name in FILES})
    require({p.name for p in folder.iterdir()} == set(FILES))
    original = bounded_file(root, manifest.relative_to(Path(root).absolute()), 65_536)
    data = json.loads(original.decode('utf-8'), object_pairs_hook=unique_object, parse_constant=reject_constant)
    images = {name: bounded_file(root, (folder / name).relative_to(Path(root).absolute()), 8 * 1024 * 1024) for name in FILES[1:]}
    validate_summary(root, data, images)
    binding = dict(run_identity, sourceSHA256={path: digest(bounded_file(root, path, 2 * 1024 * 1024)) for path in CI_SOURCES})
    # The original summary is source-validated, not relabeled as a cleanup
    # receipt. Hash-only CI provenance adds no headers, secrets or raw errors.
    staged = dict(data, artifactBinding=binding)
    encoded = (json.dumps(staged, sort_keys=True, ensure_ascii=False, allow_nan=False, indent=2) + '\n').encode()
    require(len(encoded) <= 65_536)
    destination = checked_path(root, OUTPUT)
    require(not destination.exists())
    destination.mkdir(parents=True, exist_ok=False)
    outputs = {FILES[0]: encoded, **images}
    for name, content in outputs.items():
        with (destination / name).open('xb') as stream:
            stream.write(content)
    require({p.name for p in destination.iterdir()} == set(FILES))
    for name, content in outputs.items():
        require(bounded_file(root, (destination / name).relative_to(Path(root).absolute()),
                             65_536 if name.endswith('.json') else 8 * 1024 * 1024) == content)
    return staged


def validate_cli():
    root = Path.cwd()
    def git(*arguments):
        completed = subprocess.run(['git', *arguments], cwd=root, check=True,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=10)
        require(len(completed.stdout) <= 4096)
        return completed.stdout.decode('ascii').strip()
    commit, tree = git('rev-parse', 'HEAD'), git('rev-parse', 'HEAD^{tree}')
    # Exact tracked source bytes, including this validator and the workflow;
    # generated test-results are intentionally outside this source-only check.
    git('ls-files', '--error-unmatch', '--', *PRODUCT_SOURCES, *CI_SOURCES)
    require(git('status', '--porcelain', '--untracked-files=normal', '--', *PRODUCT_SOURCES, *CI_SOURCES) == '')
    validate_and_stage(root, identity(os.environ, commit, tree))


class ArchiveTargetObservationCIContract(unittest.TestCase):
    def test_exact_success_only_three_file_upload_and_order(self):
        text = Path('.github/workflows/ci.yml').read_text()
        start = text.index('      - name: Validate exact maintenance target observation evidence\n')
        end = text.index('      - name: Retain synthetic profile visual evidence\n', start)
        block = text[start:end]
        self.assertGreater(start, text.index('      - name: Validate real empty asset observation evidence'))
        self.assertGreater(start, text.index('run: ${{ matrix.command }}'))
        self.assertIn("if: ${{ success() && matrix.name == 'maintenance' }}", block)
        self.assertIn("if: ${{ success() && matrix.name == 'maintenance' && steps.archive-target-observation-evidence.outcome == 'success' }}", block)
        self.assertIn('run: python3 scripts/test_archive_target_observation_ci_contract.py --validate-evidence', block)
        self.assertIn('actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2', block)
        self.assertIn('name: archive-target-observation-${{ github.run_id }}-${{ github.run_attempt }}', block)
        self.assertEqual(re.findall(r'^            (web/[^\n]+)$', block, re.M), [OUTPUT + '/' + name for name in FILES])
        self.assertIn('if-no-files-found: error', block)
        self.assertIn('retention-days: 7', block)
        self.assertEqual(block.count('timeout-minutes: 1'), 2)
        self.assertNotIn('*', block)
        self.assertNotIn('always()', block)
        self.assertNotIn('include-hidden-files', block)

    def test_outer_inverse_keeps_every_historical_hash_and_rejects_changes(self):
        text = Path('.github/workflows/ci.yml').read_text()
        delta = json.loads(Path(DELTA).read_text())
        self.assertEqual(set(delta), {'baselineSHA256', 'replacements'})
        self.assertEqual(delta['baselineSHA256'], BASELINE)
        self.assertEqual(len(delta['replacements']), 1)
        predecessor = restore_archive_target_observation(text)
        self.assertEqual(digest(predecessor.encode()), BASELINE)
        # These calls exercise the full health/messages/dictionary/selection
        # chain; none of their historical baseline values is updated here.
        health = json.loads(Path('scripts/fixtures/tasks_health_recovery_ci_delta.json').read_text())
        self.assertEqual(digest(restore_health_recovery(text).encode()), health['baselineSHA256'])
        self.assertTrue(restore_selection(text))
        change = delta['replacements'][0]
        self.assertEqual(change['old'], '      - name: Retain synthetic profile visual evidence\n')
        self.assertEqual(text.count(change['new']), 1)
        variants = [predecessor, text.replace(change['new'], '', 1),
                    text.replace(change['new'], change['new'] * 2, 1),
                    text.replace(change['new'], change['new'] + ' changed\n', 1)]
        for old, new in [("matrix.name == 'maintenance'", "matrix.name == 'tasks'"),
                         (FILES[2], 'raw.log'), ('github.run_attempt', 'github.run_number'),
                         ('if-no-files-found: error', 'if-no-files-found: warn'),
                         ('retention-days: 7', 'retention-days: 8'),
                         ("steps.archive-target-observation-evidence.outcome == 'success'", 'true'),
                         ('success()', 'always()')]:
            self.assertIn(old, change['new'])
            variants.append(text.replace(change['new'], change['new'].replace(old, new, 1), 1))
        for old, new in [('contents: read', 'contents: write'), ('run: make check', 'run: true'),
                         ('max-parallel: 8', 'max-parallel: 9')]:
            self.assertIn(old, text)
            variants.append(text.replace(old, new, 1))
        for variant in variants:
            with self.subTest(sha256=digest(variant.encode())):
                with self.assertRaises(ValueError):
                    restore_archive_target_observation(variant)
                with self.assertRaises(ValueError):
                    restore_health_recovery(variant)
                with self.assertRaises(ValueError):
                    restore_selection(variant)

    def test_closed_cli_errors_and_normal_contract_discovery(self):
        source = Path(__file__).read_text()
        cli = source.rsplit("\nif __name__ == '__main__':", 1)[1]
        self.assertIn("python3 -m unittest discover -s scripts -p '*_contract.py'", Path('Makefile').read_text())
        self.assertIn("print('archive target evidence validation failed', file=sys.stderr)", cli)
        self.assertNotIn('print(error', cli)
        self.assertNotIn('traceback', cli)


def synthetic_png(width=2, height=2):
    def chunk(kind, payload):
        return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload) & 0xffffffff)
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0)) +
            chunk(b'IDAT', zlib.compress((b'\0' + b'\0' * width * 4) * height)) + chunk(b'IEND', b''))


class ArchiveTargetObservationEvidenceContract(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.folder = self.root / INPUT / 'synthetic-test-output'
        self.folder.mkdir(parents=True)
        for path in PRODUCT_SOURCES + CI_SOURCES:
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(('synthetic ' + path).encode())
        self.environment = dict(GITHUB_REPOSITORY='yunpiao/adtr', GITHUB_RUN_ID='123',
            GITHUB_RUN_ATTEMPT='2', GITHUB_EVENT_NAME='pull_request', GITHUB_SHA='a' * 40)
        self.identity = identity(self.environment, 'a' * 40, 'b' * 40)
        ids = ['00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000002']
        png = synthetic_png()
        for name in FILES[1:]:
            (self.folder / name).write_bytes(png)
        self.data = dict(schemaVersion=1, case='archive_exact_targets_after_lost_response',
            status='browser_passed_cleanup_not_attested', originalTargetCount=2,
            originalIntentSHA256='c' * 64, originalTargetsSHA256='d' * 64, operationUUID=ids[0],
            targets=[dict(taskUUID=key, archived=True, visibilityVersion=value) for key, value in zip(ids, (3, 1))],
            details=[dict(taskUUID=key, visibilityVersion=value, immutableTaskSHA256='e' * 64,
                eventsSHA256='f' * 64) for key, value in zip(ids, (3, 1))],
            scheduleSHA256='0' * 64, requestCounts=dict(COUNTS),
            screenshots=[dict(file=name, bytes=len(png), sha256=digest(png), width=2, height=2,
                viewport=dict(width=width, height=height)) for name, width, height in
                zip(FILES[1:], (1440, 390), (900, 844))],
            sourceSHA256={path: digest((self.root / path).read_bytes()) for path in PRODUCT_SOURCES})
        self.save()

    def save(self):
        (self.folder / FILES[0]).write_text(json.dumps(self.data))

    def rejected(self):
        with self.assertRaises(ValueError):
            validate_and_stage(self.root, self.identity)
        self.assertFalse((self.root / OUTPUT).exists())

    def test_only_three_validated_files_and_tested_not_pr_head_binding(self):
        staged = validate_and_stage(self.root, self.identity)
        destination = self.root / OUTPUT
        self.assertEqual({path.name for path in destination.iterdir()}, set(FILES))
        self.assertEqual(staged['status'], 'browser_passed_cleanup_not_attested')
        self.assertEqual(staged['artifactBinding']['testedCommit'], 'a' * 40)
        self.assertNotIn('prHead', staged['artifactBinding'])
        self.assertEqual(set(staged['artifactBinding']['sourceSHA256']), set(CI_SOURCES))
        for name in FILES[1:]:
            self.assertEqual((destination / name).read_bytes(), (self.folder / name).read_bytes())
        with self.assertRaises(ValueError):  # Never reuse a stale stage directory.
            validate_and_stage(self.root, self.identity)

    def test_missing_duplicate_and_extra_files(self):
        for name in FILES:
            with self.subTest(file=name):
                content = (self.folder / name).read_bytes()
                (self.folder / name).unlink()
                self.rejected()
                (self.folder / name).write_bytes(content)
        extra = self.folder / 'raw.log'
        extra.write_text('synthetic canary')
        self.rejected()
        extra.unlink()
        duplicate = self.root / INPUT / FILES[0]
        duplicate.write_text(json.dumps(self.data))
        self.rejected()

    def test_schema_hash_dimension_count_source_and_metadata_mutations(self):
        original = copy.deepcopy(self.data)
        mutations = [lambda d: d.update(raw='synthetic secret canary'),
                     lambda d: d.update(schemaVersion=True),
                     lambda d: d.update(status='cleanup_passed'),
                     lambda d: d['requestCounts'].update(archiveReplay=2),
                     lambda d: d['screenshots'][0].update(sha256='0' * 64),
                     lambda d: d['screenshots'][0].update(bytes=1),
                     lambda d: d['screenshots'][0].update(width=3),
                     lambda d: d['screenshots'][0]['viewport'].update(width=390),
                     lambda d: d['screenshots'][0].update(file='../raw.png'),
                     lambda d: d['sourceSHA256'].update({PRODUCT_SOURCES[0]: '0' * 64}),
                     lambda d: d['targets'][1].update(taskUUID=d['targets'][0]['taskUUID']),
                     lambda d: d['details'][1].update(taskUUID=d['details'][0]['taskUUID'])]
        for index, mutate in enumerate(mutations):
            with self.subTest(case=index):
                self.data = copy.deepcopy(original)
                mutate(self.data)
                self.save()
                self.rejected()
        self.data = original
        self.save()
        png = (self.folder / FILES[1]).read_bytes()
        (self.folder / FILES[1]).write_bytes(png + b'raw metadata')
        self.rejected()

    def test_duplicate_keys_nonfinite_oversized_and_source_bytes(self):
        path = self.folder / FILES[0]
        for value in ['{"schemaVersion":1,"schemaVersion":1}', '{"raw":NaN}', ' ' * 65_537]:
            path.write_text(value)
            self.rejected()
        self.save()
        (self.root / PRODUCT_SOURCES[0]).write_text('changed source')
        self.rejected()

    def test_symlink_file_parent_source_and_stage(self):
        name = FILES[1]
        source = self.folder / name
        outside = self.root / 'outside.png'
        outside.write_bytes(source.read_bytes())
        source.unlink()
        source.symlink_to(outside)
        self.rejected()
        source.unlink()
        source.write_bytes(outside.read_bytes())
        moved = self.root / 'moved'
        self.folder.rename(moved)
        self.folder.symlink_to(moved, target_is_directory=True)
        self.rejected()
        self.folder.unlink()
        moved.rename(self.folder)
        product = self.root / PRODUCT_SOURCES[0]
        content = product.read_bytes()
        product.unlink()
        product.symlink_to(outside)
        self.rejected()
        product.unlink()
        product.write_bytes(content)
        destination = self.root / OUTPUT
        destination.parent.mkdir(parents=True)
        destination.symlink_to(moved, target_is_directory=True)
        with self.assertRaises(ValueError):
            validate_and_stage(self.root, self.identity)

    def test_invalid_run_environment_and_checkout_mismatch(self):
        for key, value in [('GITHUB_SHA', 'synthetic secret'), ('GITHUB_RUN_ID', '0'),
                           ('GITHUB_RUN_ID', '1\nsecret'), ('GITHUB_RUN_ATTEMPT', '-1'),
                           ('GITHUB_REPOSITORY', 'other/repository'), ('GITHUB_EVENT_NAME', 'unknown')]:
            with self.subTest(field=key):
                environment = dict(self.environment, **{key: value})
                with self.assertRaises(ValueError):
                    identity(environment, 'a' * 40, 'b' * 40)
        with self.assertRaises(ValueError):
            identity(self.environment, 'c' * 40, 'b' * 40)


if __name__ == '__main__':
    if sys.argv[1:] == ['--validate-evidence']:
        try:
            validate_cli()
        except Exception:
            print('archive target evidence validation failed', file=sys.stderr)
            raise SystemExit(1) from None
        print('archive target evidence validated; cleanup remains independently reviewed')
    else:
        unittest.main()
