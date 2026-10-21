"""Synthetic contract fixtures only. Never runtime receipts or acceptance evidence.

This draft has not been imported or executed. Future integrated tests exercise
actual validator functions, not merely token presence in source.
"""
import copy
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib

import validate_tasks_health_recovery_evidence as v


PARENT = '00000000-0000-4000-8000-000000000001'
CHILD = '00000000-0000-4000-8000-000000000002'
KEYS = {name: '00000000-0000-4000-8000-' + str(n).zfill(12)
        for name, n in [('submission', 11), ('cancelled', 12), ('submitted', 13)]}
STAMP = '2026-01-01T00:00:00Z'


def png(width, height, color=0):
    """Deliberately synthetic raster, never a screenshot witness."""
    def chunk(kind, payload):
        return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload) & 0xffffffff)
    rows = (b'\x00' + bytes([color, color, color]) * width) * height
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))


def binding(path, raw, source=False):
    value = dict(path=path, bytes=len(raw), sha256=v.digest(raw))
    if source:
        value['gitBlob'] = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
    return value


def make_task(child=False, queued=False):
    return dict(id=CHILD if child else PARENT, tenantID='synthetic', domainID='platform',
                kind='infrastructure.health', payloadVersion=1, payload={}, actorID=1,
                payloadHash=v.digest(('1:' + (PARENT if child else '') + ':{}').encode()),
                epoch='2' if child else '1', key=KEYS['submitted' if child else 'submission'],
                state='queued' if queued else 'succeeded' if child else 'failed',
                attempt=1 if child else 0, maxAttempts=5, progress=100 if child else 0,
                resultVersion=0 if queued else 3 if child else 1,
                error='' if queued or child else 'authorization_revoked', parentID=PARENT if child else '',
                result={'database': 'ready', 'queue': 'ready'} if child else {}, cursor={},
                createdAt=STAMP, updatedAt=STAMP, terminalAt=None if queued else STAMP,
                archived=False, visibilityVersion=0, leaseOwner='', leaseUntil=None,
                fencingToken=0 if queued else 1, nextAttemptAt=None)


def make_snapshot(child=False, queued=False):
    t = make_task(child, queued)
    actions = ['submitted'] if queued else ['recovered', 'leased', 'started', 'progress', 'finished'] if child else ['submitted', 'leased', 'authorization_rejected']
    states = ['queued'] if queued else ['queued', 'queued', 'running', 'running', 'succeeded'] if child else ['queued', 'queued', 'failed']
    attempts = [0] if queued else [0, 0, 1, 1, 1] if child else [0, 0, 0]
    versions = [0] if queued else [0, 0, 1, 2, 3] if child else [0, 0, 1]
    events = [dict(id=i + (4 if child else 1), taskID=t['id'], tenantID=t['tenantID'], domainID='platform',
                   actorID=1, action=action, state=states[i], attempt=attempts[i], resultVersion=versions[i],
                   epoch=t['epoch'], fencingToken=0 if i == 0 else 1, occurredAt=STAMP)
              for i, action in enumerate(actions)]
    return dict(task=t, events=events, outbox=[dict(id=e['id'], taskID=t['id'], eventID=e['id'], occurredAt=STAMP) for e in events])


def synthetic_manifest():
    parent, child, queued = make_snapshot(), make_snapshot(True), make_snapshot(queued=True)
    identity = dict(commit='1'*40, tree='2'*40, workflowSHA='3'*40, runID='1', runAttempt='1', nonce=KEYS['submission'])
    tasks = dict(queued=queued, parent=parent, child=child, parentAfterChild=copy.deepcopy(parent),
                 parentAfterReplay=copy.deepcopy(parent), childAfterReplay=copy.deepcopy(child))
    snapshots = dict(tasks=sorted([parent['task'], child['task']], key=lambda x: x['id']),
                     events=parent['events'] + child['events'], outbox=parent['outbox'] + child['outbox'])
    def receipt(task, replayed):
        return dict(status=200, replayed=replayed, taskID=task['id'], parentID=task['parentID'], key=task['key'],
                    state=task['state'], attempt=task['attempt'], resultVersion=task['resultVersion'])
    manifest = dict(schemaVersion=1, suite='tasks-health-recovery', status='provisional_pending_owned_cleanup',
                    identity=identity, checkout=dict(clean=True, sources=[binding('Makefile', b'synthetic', True)]),
                    build=dict(binary=binding('bin/adtr', b'synthetic binary'), frontend=[binding('web/dist/assets/test.js', b'synthetic js'), binding('web/dist/index.html', b'synthetic html')]),
                    browserVersion='1.2.3.4', actor=dict(id=1, tenantID='synthetic', epochBefore='1', epochAfter='2'),
                    tasks=tasks, keys=copy.deepcopy(KEYS), receipts=dict(submit=receipt(queued['task'], False), recover=receipt(child['task'], False), replay=receipt(child['task'], True)),
                    snapshots=dict(beforeReplay=copy.deepcopy(snapshots), afterReplay=copy.deepcopy(snapshots)),
                    cancelled=dict(before=dict(taskCount=0, eventCount=0), after=dict(taskCount=0, eventCount=0)),
                    session=dict(oldSessionStatus=401, replacementSameActor=True, rotated=True, csrfRefreshed=True),
                    ledger=dict(posts={name: 1 for name in ['initialPassword', 'mfaStart', 'mfaConfirm', 'submit', 'changePassword', 'recover', 'replay', 'controllerStart']},
                                reads=dict(authMe=2, oldSession=1, taskDetail=4, db=10, controllerState=2), unknownWrites=0, sensitiveActions=7, counterTransitions=4, counterWaitMs=100),
                    phases={**{name: 1 for name in v.rules()['phaseMaxMs']}, 'totalMs': 106},
                    controller=dict(before=dict(state='not_started', launchCount=0), after=dict(state='ready', launchCount=1), startPosts=1),
                    screenshots={}, lifecycle=dict(reloadParent=True, reloadChild=True, backForwardParent=True, backForwardChild=True, recoveryPostsBefore=1, recoveryPostsAfter=1))
    manifest['receipts']['recover'].update(state='queued', attempt=0, resultVersion=0)
    images = {}
    for n, (name, dimensions) in enumerate(v.rules()['screenshots'].items()):
        data = png(*dimensions, color=n)
        images[name] = data
        manifest['screenshots'][name] = dict(sha256=v.digest(data), bytes=len(data), width=dimensions[0], height=dimensions[1], proofEmpty=True)
    return manifest, images


def serialize(manifest):
    return json.dumps(manifest, sort_keys=True, separators=(',', ':')).encode()


def synthetic_cleanup(manifest, raw):
    inventory_sha = v.canonical_digest(manifest['checkout']['sources'])
    provenance = dict(producer='owned_runner', mode='built_in_run', identity=copy.deepcopy(manifest['identity']),
                      source=dict(commit=manifest['identity']['commit'], tree=manifest['identity']['tree'], inventorySHA256=inventory_sha),
                      steps=[dict(target=target, operation=operation, exitCode=0,
                                  sourceBeforeSHA256=inventory_sha, sourceAfterSHA256=inventory_sha)
                             for target, operation in [('backend', 'go_build'), ('frontend', 'npm_build')]],
                      outputs=copy.deepcopy(manifest['build']))
    return dict(schemaVersion=1, suite='tasks-health-recovery', identity=copy.deepcopy(manifest['identity']),
                manifestSHA256=v.digest(raw), launchCount=1, ownershipTransferred=True, workerReaped=True,
                apiReaped=True, controllerFinalState='closed', ownedContainerRemoved=True,
                operations=list(v.rules()['cleanupOperations']), failures=[], buildSource=provenance)


class EvidenceContracts(unittest.TestCase):
    def setUp(self):
        self.manifest, self.images = synthetic_manifest()

    def validate(self, manifest=None, mutate_receipt=None):
        m = self.manifest if manifest is None else manifest
        raw = serialize(m)
        r = synthetic_cleanup(m, raw)
        if mutate_receipt:
            mutate_receipt(r)
        v.validate_manifest(m, r, raw)

    def test_synthetic_valid_schema_not_runtime_evidence(self):
        self.validate()

    def test_closed_schema_and_wrong_types(self):
        for field, value in [('extra', 1), ('schemaVersion', True), ('status', 'passed'), ('browserVersion', 'secret payload')]:
            with self.subTest(field=field):
                m = copy.deepcopy(self.manifest)
                m[field] = value
                with self.assertRaises(v.EvidenceError):
                    self.validate(m)
        for raw in [b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":Infinity}', b'not json']:
            with self.assertRaises(v.EvidenceError):
                v.parse_json(raw)

    def test_nested_missing_shape_uses_safe_category(self):
        for broken in ({}, [], None, {'task': {}}, {'task': {'id': 'bad'}}):
            m = copy.deepcopy(self.manifest)
            m['tasks']['parent'] = broken
            with self.assertRaises(v.EvidenceError):
                self.validate(m)

    def test_actual_ci_identity_required(self):
        values = dict(GITHUB_SHA='1'*40, GITHUB_WORKFLOW_SHA='3'*40, GITHUB_RUN_ID='1',
                      GITHUB_RUN_ATTEMPT='1', ADTR_E2E_HEALTH_RUN_NONCE=KEYS['submission'])
        with patch.dict(v.os.environ, values, clear=True):
            v.verify_run_context(self.manifest['identity'])
        for name in values:
            bad = dict(values)
            bad[name] = 'wrong'
            with patch.dict(v.os.environ, bad, clear=True), self.assertRaises(v.EvidenceError):
                v.verify_run_context(self.manifest['identity'])

    def test_epoch_key_parent_and_event_mutations(self):
        mutations = [
            lambda m: m['actor'].update(epochAfter='1'),
            lambda m: m['keys'].update(submitted=m['keys']['cancelled']),
            lambda m: m['tasks']['parent']['task'].update(attempt=1),
            lambda m: m['tasks']['child']['task'].update(parentID=CHILD),
            lambda m: m['tasks']['child']['events'].pop(3),
            lambda m: m['tasks']['child']['events'][3].update(action='started'),
            lambda m: m['tasks']['parent']['outbox'][1].update(eventID=8),
            lambda m: m['tasks']['parentAfterReplay']['task'].update(updatedAt='2026-01-02T00:00:00Z'),
            lambda m: m['snapshots']['afterReplay']['events'].append(copy.deepcopy(m['tasks']['child']['events'][0])),
            lambda m: m['cancelled']['after'].update(taskCount=1),
            lambda m: m['receipts']['replay'].update(replayed=False),
            lambda m: m['receipts']['replay'].update(taskID=PARENT),
            lambda m: m['receipts']['replay'].update(key=KEYS['cancelled']),
            lambda m: m['session'].update(oldSessionStatus=200),
        ]
        for index, mutation in enumerate(mutations):
            with self.subTest(index=index):
                m = copy.deepcopy(self.manifest)
                mutation(m)
                with self.assertRaises(v.EvidenceError):
                    self.validate(m)

    def test_budgets_unknown_write_controller_and_lifecycle(self):
        mutations = [lambda m: m['ledger'].update(unknownWrites=1), lambda m: m['ledger']['posts'].update(recover=2),
                     lambda m: m['ledger']['reads'].update(oldSession=2), lambda m: m['ledger'].update(counterTransitions=5),
                     lambda m: m['ledger'].update(counterWaitMs=124401), lambda m: m['phases'].update(setup=75001),
                     lambda m: m['phases'].update(totalMs=600001), lambda m: m['controller']['after'].update(launchCount=2),
                     lambda m: m['lifecycle'].update(recoveryPostsAfter=2)]
        for mutation in mutations:
            m = copy.deepcopy(self.manifest)
            mutation(m)
            with self.assertRaises(v.EvidenceError):
                self.validate(m)

    def test_all_parent_persistence_fields_are_bound(self):
        mutations = {'payloadHash': '0'*64, 'leaseOwner': 'synthetic-worker',
                     'leaseUntil': STAMP, 'fencingToken': 2, 'nextAttemptAt': STAMP}
        for field, changed in mutations.items():
            with self.subTest(field=field):
                m = copy.deepcopy(self.manifest)
                m['tasks']['parentAfterReplay']['task'][field] = changed
                with self.assertRaises(v.EvidenceError):
                    self.validate(m)
        for field in mutations:
            m = copy.deepcopy(self.manifest)
            del m['tasks']['parent']['task'][field]
            with self.assertRaises(v.EvidenceError):
                self.validate(m)

    def test_cleanup_missing_wrong_binding_skipped_or_failed(self):
        mutations = [lambda r: r.pop('workerReaped'), lambda r: r.update(workerReaped=False),
                     lambda r: r.update(ownedContainerRemoved=False), lambda r: r.update(manifestSHA256='0'*64),
                     lambda r: r['identity'].update(runAttempt='2'), lambda r: r.update(operations=[]),
                     lambda r: r.update(failures=['worker_reap_failed']), lambda r: r.update(untrusted='secret')]
        for mutation in mutations:
            with self.assertRaises(v.EvidenceError):
                self.validate(mutate_receipt=mutation)

    def test_independent_build_binding_rejects_stale_source_reuse_and_output(self):
        mutations = [lambda r: r['buildSource'].update(mode='verified_reuse'),
                     lambda r: r['buildSource']['source'].update(tree='0'*40),
                     lambda r: r['buildSource']['identity'].update(runAttempt='2'),
                     lambda r: r['buildSource']['steps'][0].update(sourceBeforeSHA256='0'*64),
                     lambda r: r['buildSource']['steps'][1].update(sourceAfterSHA256='0'*64),
                     lambda r: r['buildSource']['steps'][0].update(exitCode=False),
                     lambda r: r['buildSource']['steps'].reverse(),
                     lambda r: r['buildSource']['outputs']['binary'].update(sha256='0'*64)]
        for mutation in mutations:
            with self.assertRaises(v.EvidenceError):
                self.validate(mutate_receipt=mutation)
        # Browser source/output claims cannot rewrite a frozen B record.
        raw = serialize(self.manifest)
        frozen = synthetic_cleanup(self.manifest, raw)
        for target in ('source', 'output'):
            changed = copy.deepcopy(self.manifest)
            if target == 'source':
                changed['checkout']['sources'][0]['sha256'] = '0'*64
            else:
                changed['build']['binary']['sha256'] = '0'*64
            changed_raw = serialize(changed)
            receipt = copy.deepcopy(frozen)
            receipt['manifestSHA256'] = v.digest(changed_raw)
            with self.assertRaises(v.EvidenceError):
                v.validate_manifest(changed, receipt, changed_raw)

    def test_canonical_inventory_encoding(self):
        self.assertEqual(v.canonical_digest({'z': '中文', 'a': 1}),
                         v.digest('{"a":1,"z":"中文"}'.encode('utf-8')))

    def test_nested_replay_and_build_numeric_types_are_closed(self):
        for replacement in (True, 1.0):
            for stage in ('beforeReplay', 'afterReplay'):
                for collection, field in [('tasks', 'actorID'), ('events', 'actorID'), ('outbox', 'id')]:
                    m = copy.deepcopy(self.manifest)
                    m['snapshots'][stage][collection][0][field] = replacement
                    with self.assertRaises(v.EvidenceError):
                        self.validate(m)
            for target in ('binary', 'frontend'):
                def mutate(receipt):
                    output = receipt['buildSource']['outputs'][target]
                    item = output[0] if target == 'frontend' else output
                    item['bytes'] = replacement
                with self.assertRaises(v.EvidenceError):
                    self.validate(mutate_receipt=mutate)
        # Keep values numerically equal to prove rejection is about type rather
        # than a different byte count or reference identity.
        for target in ('binary', 'frontend'):
            m = copy.deepcopy(self.manifest)
            item = m['build'][target][0] if target == 'frontend' else m['build'][target]
            item['bytes'] = 1
            raw = serialize(m)
            v.validate_manifest(m, synthetic_cleanup(m, raw), raw)
            for replacement in (True, 1.0):
                receipt = synthetic_cleanup(m, raw)
                output = receipt['buildSource']['outputs'][target]
                item = output[0] if target == 'frontend' else output
                item['bytes'] = replacement
                with self.assertRaises(v.EvidenceError):
                    v.validate_manifest(m, receipt, raw)

    def test_png_actual_bytes_dimensions_crc_truncation_and_payload(self):
        data = png(390, 844)
        v.verify_png(data, 390, 844)
        for invalid in [data[:-1], data + b'x', b'not a PNG', data[:25] + bytes([data[25] ^ 1]) + data[26:]]:
            with self.assertRaises(v.EvidenceError):
                v.verify_png(invalid, 390, 844)
        with self.assertRaises(v.EvidenceError):
            v.verify_png(data, 1440, 900)

    def test_safe_file_rejects_escape_symlink_and_oversize(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'regular').write_bytes(b'ab')
            (root / 'link').symlink_to(root / 'regular')
            for path, maximum in [('../regular', 10), ('link', 10), ('regular', 1), ('/regular', 10)]:
                with self.assertRaises(v.EvidenceError):
                    v.safe_file(root, path, maximum)

    def materialize(self, root):
        (root / 'manifest.json').write_bytes(serialize(self.manifest))
        (root / 'cleanup-receipt.json').write_bytes(serialize(synthetic_cleanup(self.manifest, serialize(self.manifest))))
        for name, raw in self.images.items():
            (root / name).write_bytes(raw)

    def test_joint_artifact_missing_extra_mutated_receipt_or_png(self):
        # Source validation is separately tested below. This mock does not produce
        # evidence; it isolates artifact rejection in a synthetic temporary tree.
        with tempfile.TemporaryDirectory() as temp, patch.object(v, 'verify_source_and_build'):
            root = Path(temp)
            self.materialize(root)
            v.validate_evidence(root, root)
            (root / 'extra.txt').write_text('synthetic')
            with self.assertRaises(v.EvidenceError):
                v.validate_evidence(root, root)
            (root / 'extra.txt').unlink()
            (root / 'cleanup-receipt.json').unlink()
            with self.assertRaises(v.EvidenceError):
                v.validate_evidence(root, root)
            self.materialize(root)
            (root / next(iter(self.images))).write_bytes(b'changed')
            with self.assertRaises(v.EvidenceError):
                v.validate_evidence(root, root)

    def test_source_inventory_git_blob_dirty_tree_and_bundle_binding(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            files = {'Makefile': b'synthetic', 'bin/adtr': b'synthetic binary', 'web/dist/assets/test.js': b'synthetic js', 'web/dist/index.html': b'synthetic html'}
            for path, raw in files.items():
                target = root / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(raw)
            blob = self.manifest['checkout']['sources'][0]['gitBlob']
            tree = ('100644 blob ' + blob + '\tMakefile\0').encode()
            def git_result(_root, *args):
                return {('rev-parse', 'HEAD'): b'1'*40+b'\n', ('rev-parse', 'HEAD^{tree}'): b'2'*40+b'\n',
                        ('status', '--porcelain', '--untracked-files=normal'): b'', ('ls-tree', '-rz', '--full-tree', 'HEAD'): tree}[args]
            with patch.object(v, 'git_read', side_effect=git_result), patch.object(v, 'verify_run_context'):
                v.verify_source_and_build(self.manifest, root)
                (root / 'Makefile').write_bytes(b'changed')
                with self.assertRaises(v.EvidenceError):
                    v.verify_source_and_build(self.manifest, root)
                (root / 'Makefile').write_bytes(files['Makefile'])
                (root / 'bin/adtr').write_bytes(b'stale build output')
                with self.assertRaises(v.EvidenceError):
                    v.verify_source_and_build(self.manifest, root)
                (root / 'bin/adtr').write_bytes(files['bin/adtr'])
                (root / 'web/dist/extra.js').write_bytes(b'extra')
                with self.assertRaises(v.EvidenceError):
                    v.verify_source_and_build(self.manifest, root)
            def dirty(_root, *args):
                return b' M Makefile\n' if args[0] == 'status' else git_result(_root, *args)
            with patch.object(v, 'git_read', side_effect=dirty), patch.object(v, 'verify_run_context'), self.assertRaises(v.EvidenceError):
                v.verify_source_and_build(self.manifest, root)


if __name__ == '__main__':
    unittest.main()
