#!/usr/bin/env python3
"""Joint, fail-closed validation. Browser manifests alone never constitute acceptance.

The sole receipt producer is the owned runner after its finally completes. This
module only reads receipts. Pixel readability/privacy requires a separate review.
No code in this source-only draft has been executed.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import struct
import subprocess
import sys
import zlib

RULES_PATH = Path(__file__).parent / 'fixtures/tasks_health_recovery_evidence.json'
SHA256 = re.compile(r'^[a-f0-9]{64}$')
SHA1 = re.compile(r'^[a-f0-9]{40}$')
UUID = re.compile(r'^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$')
DECIMAL = re.compile(r'^[1-9][0-9]{0,18}$')
SAFE_ID = re.compile(r'^[A-Za-z0-9_-]{1,128}$')
STAMP = re.compile(r'^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|\+00:00)$')
TASK_FIELDS = 'id tenantID domainID kind payloadVersion payload payloadHash actorID epoch key state attempt maxAttempts progress resultVersion error parentID result cursor createdAt updatedAt terminalAt archived visibilityVersion leaseOwner leaseUntil fencingToken nextAttemptAt'.split()
EVENT_FIELDS = 'id taskID tenantID domainID actorID action state attempt resultVersion epoch fencingToken occurredAt'.split()
OUTBOX_FIELDS = 'id taskID eventID occurredAt'.split()
IDENTITY_FIELDS = 'commit tree workflowSHA runID runAttempt nonce'.split()


class EvidenceError(ValueError):
    """Only fixed categories, never untrusted file contents or exception details."""


def require(condition, category='invalid_evidence'):
    if not condition:
        raise EvidenceError(category)


def exact(value, fields):
    require(type(value) is dict and set(value) == set(fields), 'schema_mismatch')


def integer(value, minimum=0, maximum=2**53-1):
    require(type(value) is int and minimum <= value <= maximum, 'invalid_integer')


def number(value, maximum):
    require(type(value) in (int, float) and math.isfinite(value) and 0 <= value <= maximum, 'invalid_budget')


def pattern(value, expression):
    require(type(value) is str and expression.fullmatch(value) is not None, 'invalid_identifier')


def sha(value):
    pattern(value, SHA256)


def no_duplicates(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate_json_key')
        result[key] = value
    return result


def parse_json(raw):
    try:
        return json.loads(raw, object_pairs_hook=no_duplicates,
                          parse_constant=lambda _: (_ for _ in ()).throw(EvidenceError('nonfinite_json')))
    except EvidenceError:
        raise
    except Exception:
        raise EvidenceError('invalid_json') from None


def rules():
    return parse_json(RULES_PATH.read_bytes())


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def canonical_digest(value):
    """UTF-8, sorted object keys, no whitespace, no ASCII escaping or NaN.

    Source inventory arrays are already strictly sorted by path. B must freeze
    this record before browser launch from its own build observations, not copy
    data from the later provisional manifest.
    """
    return digest(json.dumps(value, sort_keys=True, ensure_ascii=False,
                             separators=(',', ':'), allow_nan=False).encode('utf-8'))


def identity(value):
    exact(value, IDENTITY_FIELDS)
    for field in ('commit', 'tree', 'workflowSHA'):
        pattern(value[field], SHA1)
    for field in ('runID', 'runAttempt'):
        pattern(value[field], DECIMAL)
    pattern(value['nonce'], UUID)


def metadata(value, source=False):
    exact(value, ['path', 'sha256', 'bytes'] + (['gitBlob'] if source else []))
    path = value['path']
    require(type(path) is str and len(path) <= 1024 and '\\' not in path, 'invalid_path')
    parts = PurePosixPath(path).parts
    require(parts and not PurePosixPath(path).is_absolute() and all(p not in ('.', '..') for p in parts)
            and str(PurePosixPath(path)) == path, 'invalid_path')
    sha(value['sha256'])
    integer(value['bytes'], 0, 1024**3)
    if source:
        pattern(value['gitBlob'], SHA1)


def task(value):
    exact(value, TASK_FIELDS)
    pattern(value['id'], UUID)
    pattern(value['tenantID'], SAFE_ID)
    pattern(value['key'], UUID)
    pattern(value['epoch'], DECIMAL)
    integer(value['actorID'], 1)
    require(value['domainID'] == 'platform' and value['kind'] == 'infrastructure.health')
    require(type(value['payloadVersion']) is int and value['payloadVersion'] == 1)
    require(value['payload'] == {} and type(value['payload']) is dict)
    sha(value['payloadHash'])
    require(value['cursor'] == {} and type(value['cursor']) is dict)
    integer(value['attempt'], 0, 1)
    require(type(value['maxAttempts']) is int and value['maxAttempts'] == 5)
    integer(value['progress'], 0, 100)
    integer(value['resultVersion'], 0)
    require(value['state'] in ('queued', 'failed', 'succeeded'))
    require(value['error'] in ('', 'authorization_revoked'))
    require(value['parentID'] == '' or (type(value['parentID']) is str and UUID.fullmatch(value['parentID'])))
    require(type(value['result']) is dict and value['result'] in ({}, {'database': 'ready', 'queue': 'ready'}))
    for field in ('createdAt', 'updatedAt'):
        pattern(value[field], STAMP)
    require(value['terminalAt'] is None or (type(value['terminalAt']) is str and STAMP.fullmatch(value['terminalAt'])))
    require(value['archived'] is False)
    integer(value['visibilityVersion'], 0)
    # Only pre-launch queued and terminal rows are evidence snapshots. No live
    # worker owner identifier is permitted in artifacts, even in a failed run.
    require(value['leaseOwner'] == '' and value['leaseUntil'] is None
            and value['nextAttemptAt'] is None, 'lease_not_cleared')
    integer(value['fencingToken'], 0, 1)


def event(value):
    exact(value, EVENT_FIELDS)
    integer(value['id'], 1)
    pattern(value['taskID'], UUID)
    pattern(value['tenantID'], SAFE_ID)
    require(value['domainID'] == 'platform')
    integer(value['actorID'], 1)
    require(value['action'] in ('submitted', 'leased', 'authorization_rejected', 'recovered', 'started', 'progress', 'finished'))
    require(value['state'] in ('queued', 'failed', 'running', 'succeeded'))
    integer(value['attempt'], 0, 1)
    integer(value['resultVersion'], 0)
    integer(value['fencingToken'])
    pattern(value['epoch'], DECIMAL)
    pattern(value['occurredAt'], STAMP)


def outbox(value):
    exact(value, OUTBOX_FIELDS)
    integer(value['id'], 1)
    integer(value['eventID'], 1)
    pattern(value['taskID'], UUID)
    pattern(value['occurredAt'], STAMP)


def ordered(values, field):
    require(type(values) is list and len(values) <= 8)
    ids = [x[field] for x in values]
    require(ids == sorted(set(ids)), 'duplicate_or_unordered_identity')


def snapshot(value, role, actor, key, parent_id, contract):
    exact(value, ['task', 'events', 'outbox'])
    t = value['task']
    task(t)
    require(t['actorID'] == actor['id'] and t['tenantID'] == actor['tenantID'])
    require(t['key'] == key and t['parentID'] == parent_id)
    require(t['payloadHash'] == digest(('1:' + parent_id + ':{}').encode()), 'payload_hash_mismatch')
    queued = role == 'queued'
    child = role == 'child'
    require(t['fencingToken'] == (0 if queued else 1), 'task_fencing_mismatch')
    require(t['epoch'] == actor['epochAfter' if child else 'epochBefore'])
    require((t['state'], t['attempt'], t['progress'], t['error']) ==
            (('queued', 0, 0, '') if queued else ('succeeded', 1, 100, '') if child else ('failed', 0, 0, 'authorization_revoked')))
    require(t['result'] == ({'database': 'ready', 'queue': 'ready'} if child else {}))
    require((t['terminalAt'] is None) == queued)
    expected = ['submitted'] if queued else contract['childActions' if child else 'parentActions']
    attempts = [0] if queued else contract['childAttempts' if child else 'parentAttempts']
    require(type(value['events']) is list and len(value['events']) == len(expected))
    require(type(value['outbox']) is list and len(value['outbox']) == len(expected))
    for e in value['events']:
        event(e)
        require((e['taskID'], e['actorID'], e['tenantID'], e['epoch']) == (t['id'], t['actorID'], t['tenantID'], t['epoch']))
    for o in value['outbox']:
        outbox(o)
        require(o['taskID'] == t['id'])
    ordered(value['events'], 'id')
    ordered(value['outbox'], 'id')
    require([e['action'] for e in value['events']] == expected)
    require([e['attempt'] for e in value['events']] == attempts)
    require([e['fencingToken'] for e in value['events']] == [0] + [1] * (len(expected) - 1), 'fencing_sequence_mismatch')
    require([o['eventID'] for o in value['outbox']] == [e['id'] for e in value['events']], 'outbox_join_mismatch')
    last = value['events'][-1]
    require((last['state'], last['attempt'], last['resultVersion']) == (t['state'], t['attempt'], t['resultVersion']))
    versions = [e['resultVersion'] for e in value['events']]
    require(versions == ([0] if queued else [0, 0, 1, 2, 3] if child else [0, 0, 1]))
    require([e['state'] for e in value['events']] == (['queued'] if queued else ['queued', 'queued', 'running', 'running', 'succeeded'] if child else ['queued', 'queued', 'failed']))


def _validate_manifest(manifest, cleanup, manifest_bytes):
    """Pure closed-schema and relational validation; file verification is separate."""
    c = rules()
    exact(manifest, 'schemaVersion suite status identity checkout build browserVersion actor tasks keys receipts snapshots cancelled session ledger phases controller screenshots lifecycle'.split())
    require(type(manifest['schemaVersion']) is int and manifest['schemaVersion'] == 1)
    require(manifest['suite'] == c['suite'] and manifest['status'] == c['manifestStatus'])
    identity(manifest['identity'])
    require(canonical_digest(parse_json(manifest_bytes)) == canonical_digest(manifest)
            and len(manifest_bytes) <= c['maxManifestBytes'])
    checkout = manifest['checkout']
    exact(checkout, ['clean', 'sources'])
    require(checkout['clean'] is True and type(checkout['sources']) is list and 1 <= len(checkout['sources']) <= 50000)
    for s in checkout['sources']:
        metadata(s, source=True)
    paths = [s['path'] for s in checkout['sources']]
    require(paths == sorted(set(paths)), 'source_inventory_mismatch')
    build = manifest['build']
    exact(build, ['binary', 'frontend'])
    metadata(build['binary'])
    require(build['binary']['path'] == c['binaryPath'])
    require(type(build['frontend']) is list and 1 <= len(build['frontend']) <= 5000)
    for item in build['frontend']:
        metadata(item)
        require(item['path'].startswith(c['frontendPrefix']))
    front = [f['path'] for f in build['frontend']]
    require(front == sorted(set(front)) and 'web/dist/index.html' in front and any(p.endswith('.js') for p in front))
    require(type(manifest['browserVersion']) is str and re.fullmatch(r'[0-9]+(?:\.[0-9]+){1,3}', manifest['browserVersion']) is not None)
    a = manifest['actor']
    exact(a, ['id', 'tenantID', 'epochBefore', 'epochAfter'])
    integer(a['id'], 1)
    pattern(a['tenantID'], SAFE_ID)
    pattern(a['epochBefore'], DECIMAL)
    pattern(a['epochAfter'], DECIMAL)
    require(int(a['epochAfter']) > int(a['epochBefore']), 'epoch_not_rotated')
    k = manifest['keys']
    exact(k, ['submission', 'cancelled', 'submitted'])
    for value in k.values():
        pattern(value, UUID)
    require(len(set(k.values())) == 3, 'intent_key_lifecycle_mismatch')
    ts = manifest['tasks']
    exact(ts, ['queued', 'parent', 'child', 'parentAfterChild', 'parentAfterReplay', 'childAfterReplay'])
    parent_id = ts['parent']['task']['id']
    child_id = ts['child']['task']['id']
    require(parent_id != child_id)
    for name in ('queued', 'parent', 'parentAfterChild', 'parentAfterReplay'):
        snapshot(ts[name], 'queued' if name == 'queued' else 'parent', a, k['submission'], '', c)
        require(ts[name]['task']['id'] == parent_id)
    for name in ('child', 'childAfterReplay'):
        snapshot(ts[name], 'child', a, k['submitted'], parent_id, c)
        require(ts[name]['task']['id'] == child_id)
    require(ts['parent'] == ts['parentAfterChild'] == ts['parentAfterReplay'], 'parent_mutated')
    require(ts['child'] == ts['childAfterReplay'], 'replay_mutated_child')
    require(ts['queued']['events'][0] == ts['parent']['events'][0] and ts['queued']['outbox'][0] == ts['parent']['outbox'][0])
    receipts = manifest['receipts']
    exact(receipts, ['submit', 'recover', 'replay'])
    for name, snapshot_name, replayed in [('submit', 'queued', False), ('recover', None, False), ('replay', 'child', True)]:
        r = receipts[name]
        exact(r, ['status', 'replayed', 'taskID', 'parentID', 'state', 'attempt', 'resultVersion', 'key'])
        require(r['key'] == k['submission' if name == 'submit' else 'submitted'], 'receipt_key_mismatch')
        require(type(r['status']) is int and r['status'] == 200 and r['replayed'] is replayed)
        require(r['taskID'] == (parent_id if name == 'submit' else child_id))
        require(r['parentID'] == ('' if name == 'submit' else parent_id))
        integer(r['attempt'], 0, 1)
        integer(r['resultVersion'], 0, 3)
        require(r['state'] in ('queued', 'running', 'succeeded'))
        if snapshot_name:
            t = ts[snapshot_name]['task']
            require((r['state'], r['attempt'], r['resultVersion']) == (t['state'], t['attempt'], t['resultVersion']))
        else:
            require((r['state'], r['attempt'], r['resultVersion']) == ('queued', 0, 0), 'recovery_admission_mismatch')
    snaps = manifest['snapshots']
    exact(snaps, ['beforeReplay', 'afterReplay'])
    expected = {'tasks': sorted([ts['parent']['task'], ts['child']['task']], key=lambda x: x['id']),
                'events': sorted(ts['parent']['events'] + ts['child']['events'], key=lambda x: x['id']),
                'outbox': sorted(ts['parent']['outbox'] + ts['child']['outbox'], key=lambda x: x['id'])}
    for value in (snaps['beforeReplay'], snaps['afterReplay']):
        exact(value, ['tasks', 'events', 'outbox'])
        for name, validator in [('tasks', task), ('events', event), ('outbox', outbox)]:
            require(type(value[name]) is list, 'snapshot_schema_mismatch')
            for row in value[name]:
                validator(row)
        require(canonical_digest(value) == canonical_digest(expected), 'replay_added_rows')
    require(len({x['id'] for x in expected['events']}) == 8 and len({x['id'] for x in expected['outbox']}) == 8)
    exact(manifest['cancelled'], ['before', 'after'])
    for counts in manifest['cancelled'].values():
        exact(counts, ['taskCount', 'eventCount'])
        require(all(type(v) is int and v == 0 for v in counts.values()), 'cancelled_intent_wrote')
    session = manifest['session']
    exact(session, ['oldSessionStatus', 'replacementSameActor', 'rotated', 'csrfRefreshed'])
    require(type(session['oldSessionStatus']) is int and session['oldSessionStatus'] == 401)
    require(all(session[k] is True for k in ('replacementSameActor', 'rotated', 'csrfRefreshed')))
    ledger = manifest['ledger']
    exact(ledger, ['posts', 'reads', 'unknownWrites', 'sensitiveActions', 'counterTransitions', 'counterWaitMs'])
    exact(ledger['posts'], ['initialPassword', 'mfaStart', 'mfaConfirm', 'submit', 'changePassword', 'recover', 'replay', 'controllerStart'])
    require(all(type(v) is int and v == 1 for v in ledger['posts'].values()), 'write_budget_mismatch')
    exact(ledger['reads'], ['authMe', 'oldSession', 'taskDetail', 'db', 'controllerState'])
    for v in ledger['reads'].values():
        integer(v, 1, 2000)
    require(ledger['reads']['oldSession'] == 1)
    require(type(ledger['unknownWrites']) is int and ledger['unknownWrites'] == 0, 'unknown_write')
    require(type(ledger['sensitiveActions']) is int and ledger['sensitiveActions'] == 7)
    integer(ledger['counterTransitions'], 0, c['counterTransitionsMax'])
    number(ledger['counterWaitMs'], c['counterWaitMaxMs'])
    phases = manifest['phases']
    exact(phases, list(c['phaseMaxMs']) + ['totalMs'])
    for name, maximum in c['phaseMaxMs'].items():
        number(phases[name], maximum)
    number(phases['totalMs'], c['suiteMaxMs'])
    require(sum(phases[n] for n in c['phaseMaxMs']) + ledger['counterWaitMs'] <= phases['totalMs'] + 1, 'phase_accounting_mismatch')
    controller = manifest['controller']
    exact(controller, ['before', 'after', 'startPosts'])
    require(controller['before'] == {'state': 'not_started', 'launchCount': 0}
            and controller['after'] == {'state': 'ready', 'launchCount': 1}
            and type(controller['startPosts']) is int and controller['startPosts'] == 1, 'controller_mismatch')
    for state in (controller['before'], controller['after']):
        require(type(state['launchCount']) is int)
    exact(manifest['screenshots'], c['screenshots'])
    for name, dimensions in c['screenshots'].items():
        s = manifest['screenshots'][name]
        exact(s, ['sha256', 'bytes', 'width', 'height', 'proofEmpty'])
        sha(s['sha256'])
        integer(s['bytes'], 45, c['maxPNGBytes'])
        require([s['width'], s['height']] == dimensions and type(s['width']) is int and type(s['height']) is int and s['proofEmpty'] is True)
    life = manifest['lifecycle']
    exact(life, ['reloadParent', 'reloadChild', 'backForwardParent', 'backForwardChild', 'recoveryPostsBefore', 'recoveryPostsAfter'])
    require(all(life[n] is True for n in ('reloadParent', 'reloadChild', 'backForwardParent', 'backForwardChild')))
    require(type(life['recoveryPostsBefore']) is int and type(life['recoveryPostsAfter']) is int
            and life['recoveryPostsBefore'] == life['recoveryPostsAfter'] == 1)
    validate_cleanup(cleanup, manifest['identity'], digest(manifest_bytes), c)
    validate_build_source(cleanup['buildSource'], manifest)


def validate_manifest(manifest, cleanup, manifest_bytes):
    try:
        return _validate_manifest(manifest, cleanup, manifest_bytes)
    except EvidenceError:
        raise
    except Exception:
        raise EvidenceError("malformed_evidence") from None


def validate_cleanup(receipt, run_identity, manifest_sha, contract=None):
    c = contract or rules()
    exact(receipt, 'schemaVersion suite identity manifestSHA256 launchCount ownershipTransferred workerReaped apiReaped controllerFinalState ownedContainerRemoved operations failures buildSource'.split())
    require(type(receipt['schemaVersion']) is int and receipt['schemaVersion'] == 1 and receipt['suite'] == c['suite'])
    identity(receipt['identity'])
    require(receipt['identity'] == run_identity and receipt['manifestSHA256'] == manifest_sha, 'cleanup_binding_mismatch')
    require(type(receipt['launchCount']) is int and receipt['launchCount'] == 1)
    require(all(receipt[k] is True for k in ('ownershipTransferred', 'workerReaped', 'apiReaped', 'ownedContainerRemoved')), 'cleanup_incomplete')
    require(receipt['controllerFinalState'] == 'closed' and receipt['failures'] == [], 'cleanup_failed')
    require(type(receipt['operations']) is list and receipt['operations'] == c['cleanupOperations'], 'cleanup_operations_incomplete')


def validate_build_source(record, manifest):
    """Check independent B build record. This cannot authenticate a fabricated
    runner: B's reviewed producer must observe actual process completion and
    read source before/after each fixed build, freezing outputs before browser
    launch. Merely assigning these fields never establishes build provenance.
    Reuse has no approved provenance verification path and fails closed.
    """
    exact(record, ['producer', 'mode', 'identity', 'source', 'steps', 'outputs'])
    require(record['producer'] == 'owned_runner' and record['mode'] == 'built_in_run', 'unverified_build_provenance')
    identity(record['identity'])
    require(record['identity'] == manifest['identity'], 'build_run_mismatch')
    exact(record['source'], ['commit', 'tree', 'inventorySHA256'])
    inventory_sha = canonical_digest(manifest['checkout']['sources'])
    require(record['source'] == {'commit': manifest['identity']['commit'],
                                 'tree': manifest['identity']['tree'],
                                 'inventorySHA256': inventory_sha}, 'build_source_mismatch')
    require(type(record['steps']) is list and len(record['steps']) == 2, 'build_steps_incomplete')
    for step, target, operation in zip(record['steps'], ['backend', 'frontend'], ['go_build', 'npm_build']):
        exact(step, ['target', 'operation', 'exitCode', 'sourceBeforeSHA256', 'sourceAfterSHA256'])
        require(step['target'] == target and step['operation'] == operation, 'build_step_mismatch')
        require(type(step['exitCode']) is int and step['exitCode'] == 0, 'build_failed')
        require(step['sourceBeforeSHA256'] == inventory_sha == step['sourceAfterSHA256'], 'build_source_drift')
    exact(record['outputs'], ['binary', 'frontend'])
    metadata(record['outputs']['binary'])
    require(type(record['outputs']['frontend']) is list, 'build_output_schema_mismatch')
    for entry in record['outputs']['frontend']:
        metadata(entry)
    require(canonical_digest(record['outputs']) == canonical_digest(manifest['build']), 'build_output_mismatch')
    # verify_source_and_build subsequently reads every actual output byte.


def safe_file(root, relative, maximum):
    require(type(relative) is str and '\\' not in relative and str(PurePosixPath(relative)) == relative, 'invalid_path')
    parts = PurePosixPath(relative).parts
    require(parts and not PurePosixPath(relative).is_absolute() and '..' not in parts and '.' not in parts, 'invalid_path')
    p = root
    require(not p.is_symlink(), 'symlink_rejected')
    require(all(not ancestor.is_symlink() for ancestor in p.parents), "symlink_rejected")
    for component in parts:
        p = p / component
        require(not p.is_symlink(), 'symlink_rejected')
    require(all(not ancestor.is_symlink() for ancestor in p.parents), "symlink_rejected")
    require(p.is_file() and p.stat().st_size <= maximum, 'file_missing_or_oversized')
    require(p.resolve().is_relative_to(root.resolve()), 'path_escape')
    return p.read_bytes()


def verify_png(raw, width, height):
    require(raw[:8] == b'\x89PNG\r\n\x1a\n', 'invalid_png')
    position, chunks, image_data = 8, [], bytearray()
    while position < len(raw):
        require(position + 12 <= len(raw), 'invalid_png')
        size = struct.unpack('>I', raw[position:position+4])[0]
        kind = raw[position+4:position+8]
        end = position + 12 + size
        require(end <= len(raw), 'invalid_png')
        payload = raw[position+8:position+8+size]
        crc = struct.unpack('>I', raw[position+8+size:end])[0]
        require(zlib.crc32(kind + payload) & 0xffffffff == crc, 'invalid_png_crc')
        if not chunks:
            require(kind == b'IHDR' and size == 13, 'invalid_png_header')
            w, h, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', payload)
            require((w, h) == (width, height) and depth == 8 and color in (2, 6)
                    and compression == filtering == interlace == 0, 'invalid_png_header')
        elif kind == b'IHDR':
            raise EvidenceError('duplicate_png_header')
        if kind == b'IDAT':
            image_data.extend(payload)
        require(kind in (b'IHDR', b'IDAT', b'IEND', b'sRGB', b'gAMA', b'cHRM', b'pHYs'), 'unexpected_png_chunk')
        chunks.append(kind)
        position = end
        if kind == b'IEND':
            require(size == 0 and position == len(raw), 'invalid_png_end')
            break
    require(chunks and chunks[-1] == b'IEND' and image_data, 'invalid_png')
    expected = height * (1 + width * (4 if color == 6 else 3))
    try:
        decoder = zlib.decompressobj()
        pixels = decoder.decompress(bytes(image_data), expected + 1)
        require(len(pixels) == expected and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail, 'invalid_png_pixels')
        stride = expected // height
        require(all(pixels[row * stride] <= 4 for row in range(height)), 'invalid_png_filter')
    except EvidenceError:
        raise
    except Exception:
        raise EvidenceError('invalid_png_pixels') from None


def git_read(root, *args):
    try:
        result = subprocess.run(['git', '-C', str(root), *args], capture_output=True, timeout=30, check=False)
        require(result.returncode == 0, 'source_checkout_unavailable')
        return result.stdout
    except EvidenceError:
        raise
    except Exception:
        raise EvidenceError('source_checkout_unavailable') from None


def verify_run_context(run_identity):
    """Runtime acceptance requires authoritative runner/CI identity, not a self-claim."""
    for field, variable in [("commit", "GITHUB_SHA"), ("workflowSHA", "GITHUB_WORKFLOW_SHA"),
                            ("runID", "GITHUB_RUN_ID"), ("runAttempt", "GITHUB_RUN_ATTEMPT")]:
        require(os.environ.get(variable) == run_identity[field], "ci_identity_mismatch")
    require(os.environ.get("ADTR_E2E_HEALTH_RUN_NONCE") == run_identity["nonce"], "run_nonce_mismatch")


def verify_source_and_build(manifest, root):
    require(not root.is_symlink() and all(not p.is_symlink() for p in root.parents), 'symlink_rejected')
    verify_run_context(manifest["identity"])
    c = rules()
    require(git_read(root, 'rev-parse', 'HEAD').decode().strip() == manifest['identity']['commit'], 'commit_mismatch')
    require(git_read(root, 'rev-parse', 'HEAD^{tree}').decode().strip() == manifest['identity']['tree'], 'tree_mismatch')
    require(git_read(root, 'status', '--porcelain', '--untracked-files=normal') == b'', 'dirty_checkout')
    tree = git_read(root, 'ls-tree', '-rz', '--full-tree', 'HEAD')
    selected = {}
    for record in tree.split(b'\0'):
        if not record:
            continue
        try:
            head, name = record.split(b'\t', 1)
            mode, kind, blob = head.decode().split()
            path = name.decode('utf-8', 'strict')
        except Exception:
            raise EvidenceError('invalid_source_tree') from None
        if path in c['sourceNames'] or any(path.startswith(prefix) for prefix in c['sourcePrefixes']):
            require(kind == 'blob' and mode in ('100644', '100755'), 'nonregular_source')
            selected[path] = blob
    sources = manifest['checkout']['sources']
    require([s['path'] for s in sources] == sorted(selected), 'source_inventory_mismatch')
    for entry in sources:
        raw = safe_file(root, entry['path'], 1024**3)
        blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
        require(entry['gitBlob'] == selected[entry['path']] == blob and entry['sha256'] == digest(raw)
                and entry['bytes'] == len(raw), 'source_byte_mismatch')
    front_root = root / 'web/dist'
    require(front_root.is_dir() and not front_root.is_symlink(), 'frontend_missing')
    actual = []
    for p in front_root.rglob('*'):
        require(not p.is_symlink(), 'symlink_rejected')
        require(all(not ancestor.is_symlink() for ancestor in p.parents), "symlink_rejected")
        if p.is_file():
            actual.append(p.relative_to(root).as_posix())
        else:
            require(p.is_dir(), 'nonregular_bundle')
    require(sorted(actual) == [s['path'] for s in manifest['build']['frontend']], 'frontend_inventory_mismatch')
    for entry in [manifest['build']['binary'], *manifest['build']['frontend']]:
        raw = safe_file(root, entry['path'], 1024**3)
        require(entry['bytes'] == len(raw) and entry['sha256'] == digest(raw), 'build_byte_mismatch')


def validate_evidence(evidence_dir, source_root):
    """Final runner and workflow entry point; requires all six unchanged files."""
    try:
        root, source = Path(evidence_dir).absolute(), Path(source_root).absolute()
        c = rules()
        require(root.is_dir() and not root.is_symlink(), 'evidence_directory_invalid')
        require(sorted(p.name for p in root.iterdir()) == sorted(c['files']), 'evidence_file_set_mismatch')
        raw = safe_file(root, 'manifest.json', c['maxManifestBytes'])
        receipt_raw = safe_file(root, 'cleanup-receipt.json', c['maxReceiptBytes'])
        manifest, receipt = parse_json(raw), parse_json(receipt_raw)
        validate_manifest(manifest, receipt, raw)
        verify_source_and_build(manifest, source)
        hashes = []
        for name, dimensions in c['screenshots'].items():
            data = safe_file(root, name, c['maxPNGBytes'])
            entry = manifest['screenshots'][name]
            require(len(data) == entry['bytes'] and digest(data) == entry['sha256'], 'png_byte_mismatch')
            verify_png(data, *dimensions)
            hashes.append(digest(data))
        require(len(set(hashes)) == 4, 'duplicate_screenshot')
        return manifest
    except EvidenceError:
        raise
    except Exception:
        raise EvidenceError('evidence_read_failed') from None


def main():
    parser = argparse.ArgumentParser(description='Validate joint tasks health recovery evidence after owned cleanup')
    parser.add_argument('--evidence-dir', required=True)
    parser.add_argument('--source-root', required=True)
    args = parser.parse_args()
    try:
        validate_evidence(args.evidence_dir, args.source_root)
    except EvidenceError as error:
        print('tasks-health-recovery evidence rejected: ' + str(error), file=sys.stderr)
        return 1
    print('tasks-health-recovery structural/source/cleanup evidence validated; independent PNG pixel review remains required')
    return 0


if __name__ == '__main__':
    sys.exit(main())
