"""Closed success-only access evidence validation; no business/API execution.

This attests a source-bound producer and post-cleanup runner observation, not
independent Docker absence or semantic truth of screenshot pixels. Real CI and
independent visual/source review remain separate acceptance gates.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
import subprocess
import sys
import zlib

from e2e_runtime_diagnostics import validate_report

ERROR = 'access role bulk evidence validation failed'
MAX_JSON = 65_536
MAX_PNG = 8 * 1024 * 1024
MAX_SOURCE = 2 * 1024 * 1024
INPUT = 'web/test-results/access-role-bulk'
OUTPUT = 'web/playwright-report/access-role-bulk-validated'
FILES = ('access-role-bulk-evidence.json', 'access-role-bulk-users-1440.png')
RUNTIME = '.e2e-diagnostics/runtime.json'
SOURCE_PATHS = (
    '.github/workflows/ci.yml', 'AGENTS.md', 'README.md',
    'docs/access-contract.md', 'docs/architecture.md', 'docs/delivery.md', 'docs/task-contract.md',
    'internal/audit/directory_v4_schema.go', 'internal/audit/query.go',
    'internal/audit/schema.go', 'internal/audit/types.go',
    'internal/auth/access_http.go', 'internal/auth/access_integration_test.go',
    'internal/auth/access_roles.go', 'internal/auth/access_schema.go',
    'internal/auth/access_types.go', 'internal/auth/access_users.go',
    'internal/auth/audit_http.go', 'internal/auth/crypto.go',
    'internal/auth/request_metadata.go', 'internal/auth/service.go',
    'requirements/catalog.json', 'scripts/ci_inventory.json', 'scripts/ci_selection.py',
    'scripts/ci_workflow_contract.py', 'scripts/e2e_owned_container.py',
    'scripts/e2e_runtime_diagnostics.py', 'scripts/fixtures/access_role_bulk_ci_delta.json',
    'scripts/fixtures/archive_target_observation_ci_delta.json',
    'scripts/test_access_role_bulk_contract.py', 'scripts/test_archive_target_observation_ci_contract.py',
    'scripts/test_auth_e2e.py', 'scripts/test_ci_selection_contract.py',
    'scripts/test_e2e_runtime_diagnostics_contract.py', 'scripts/validate_access_role_bulk_evidence.py',
    'web/e2e/access.spec.ts', 'web/e2e/navigation.ts', 'web/package.json',
    'web/playwright.config.ts', 'web/src/Access.test.tsx', 'web/src/AccessRoles.tsx',
    'web/src/AccessUsers.tsx', 'web/src/AccessWorkspace.tsx', 'web/src/App.tsx',
    'web/src/access-common.tsx', 'web/src/api.ts',
)
ALIASES = ['existing_reader', 'new_viewer']
PHASES = ['setup', 'api-start', 'api-ready', 'browser-start', 'browser-pass',
          'before-cleanup', 'after-cleanup']
ROOT_KEYS = ('schemaVersion', 'case', 'status', 'identity', 'actorID', 'customRoleID',
             'targets', 'rejectedBatch', 'committedBatch', 'targetSessions', 'audit',
             'checks', 'proofBudget', 'ownedContexts', 'screenshot', 'sourceSHA256')


def require(condition):
    if not condition:
        raise ValueError(ERROR)


def exact(value, keys):
    require(type(value) is dict and set(value) == set(keys))


def equals(actual, expected):
    """Structural equality that never accepts bool as int, or tuple as list."""
    require(type(actual) is type(expected))
    if type(expected) is dict:
        exact(actual, expected)
        for key, value in expected.items():
            equals(actual[key], value)
    elif type(expected) is list:
        require(len(actual) == len(expected))
        for left, right in zip(actual, expected):
            equals(left, right)
    else:
        require(actual == expected)


def integer(value, low=1, high=9_007_199_254_740_991):
    return type(value) is int and low <= value <= high


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def hex_value(value, length=64):
    return type(value) is str and re.fullmatch('[0-9a-f]{' + str(length) + '}', value) is not None


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result)
        result[key] = value
    return result


def reject_constant(_):
    raise ValueError(ERROR)


def safe_json(raw):
    require(type(raw) is bytes and 0 < len(raw) <= MAX_JSON)
    text = raw.decode('utf-8')
    # Bound nesting before json.loads itself can recurse. Brackets in quoted
    # strings do not contribute, escaped quotes cannot change parser state.
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
            require(depth <= 12)
        elif char in ']}':
            depth -= 1
            require(depth >= 0)
    require(not quoted and depth == 0)
    value = json.loads(text, object_pairs_hook=unique_object, parse_constant=reject_constant)
    pending, nodes = [(value, 1)], 0
    while pending:
        item, level = pending.pop()
        nodes += 1
        require(nodes <= 2048 and level <= 12)
        if type(item) is dict:
            nodes += len(item)  # Include property-name scalar nodes.
            require(nodes <= 2048)
            pending.extend((child, level + 1) for child in item.values())
        elif type(item) is list:
            pending.extend((child, level + 1) for child in item)
        else:
            require(type(item) in (str, int, bool, type(None)))
    return value


def checked_path(root, relative):
    root, relative = Path(root).absolute(), Path(relative)
    require('..' not in root.parts and not relative.is_absolute() and '..' not in relative.parts)
    path = Path(root.anchor)
    for part in root.parts[1:] + relative.parts:
        path /= part
        require(not path.is_symlink())
    require(path == root or root in path.parents)
    return path


def bounded_file(root, relative, limit):
    path = checked_path(root, relative)
    before = path.lstat()
    require(stat.S_ISREG(before.st_mode) and 0 < before.st_size <= limit)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, 'rb') as stream:
        opened = os.fstat(stream.fileno())
        require(stat.S_ISREG(opened.st_mode) and (opened.st_dev, opened.st_ino) == (before.st_dev, before.st_ino))
        raw = stream.read(limit + 1)
    require(0 < len(raw) <= limit and len(raw) == before.st_size)
    checked_path(root, relative)
    return raw


def png_size(raw):
    require(type(raw) is bytes and 32 < len(raw) <= MAX_PNG and raw[:8] == b'\x89PNG\r\n\x1a\n')
    offset, metadata, compressed, ended = 8, set(), bytearray(), False
    color = None
    image_started = False
    lengths = {b'sRGB': 1, b'gAMA': 4, b'cHRM': 32, b'pHYs': 9}
    while offset < len(raw):
        require(not ended and offset + 12 <= len(raw))
        length = int.from_bytes(raw[offset:offset + 4], 'big')
        kind = raw[offset + 4:offset + 8]
        end = offset + length + 12
        require(length <= MAX_PNG and end <= len(raw))
        payload = raw[offset + 8:end - 4]
        require(zlib.crc32(kind + payload) & 0xffffffff == int.from_bytes(raw[end - 4:end], 'big'))
        if offset == 8:
            require(kind == b'IHDR' and length == 13)
            width, height, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', payload)
            require((width, height) == (1440, 900) and depth == 8 and color in (2, 6)
                    and compression == filtering == interlace == 0)
        elif kind == b'IDAT':
            image_started = True
            compressed.extend(payload)
        elif kind == b'IEND':
            require(length == 0 and image_started and len(compressed) > 0 and end == len(raw))
            ended = True
        else:
            # Only this bounded nontext metadata, once each and before IDAT.
            # Therefore IDAT chunks cannot be interrupted and then restarted.
            require(not image_started and kind in lengths and kind not in metadata and length == lengths[kind])
            metadata.add(kind)
            if kind == b'sRGB':
                require(payload[0] <= 3)
            elif kind == b'gAMA':
                require(0 < int.from_bytes(payload, 'big') <= 2_147_483_647)
            elif kind == b'cHRM':
                values = struct.unpack('>8I', payload)
                require(all(0 <= number <= 100_000 for number in values)
                        and all(values[index] + values[index + 1] <= 100_000 for index in range(0, 8, 2)))
            else:
                x, y, unit = struct.unpack('>IIB', payload)
                require(x <= 2_147_483_647 and y <= 2_147_483_647 and unit in (0, 1))
        offset = end
    require(ended and color in (2, 6))
    stride = 1440 * (3 if color == 2 else 4) + 1
    expected = stride * 900
    decoder = zlib.decompressobj()
    pixels = decoder.decompress(bytes(compressed), expected + 1)
    require(len(pixels) == expected and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail)
    require(all(pixels[row * stride] <= 4 for row in range(900)))
    return (1440, 900)


def identity(environment, checkout_commit, checkout_tree):
    require(hex_value(checkout_commit, 40) and hex_value(checkout_tree, 40))
    require(environment.get('GITHUB_REPOSITORY') == 'yunpiao/adtr'
            and environment.get('GITHUB_SHA') == checkout_commit)
    for name in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
        value = environment.get(name)
        require(type(value) is str and re.fullmatch(r'[1-9][0-9]{0,19}', value) is not None)
    require(environment.get('GITHUB_EVENT_NAME') in ('push', 'pull_request'))
    return dict(repository='yunpiao/adtr', suite='access', runID=environment['GITHUB_RUN_ID'],
                runAttempt=environment['GITHUB_RUN_ATTEMPT'], event=environment['GITHUB_EVENT_NAME'],
                testedCommit=checkout_commit)


def validate_summary(data, run_identity, sources, png):
    exact(data, ROOT_KEYS)
    equals(data['schemaVersion'], 1)
    equals(data['case'], 'access_role_bulk_atomic_rollback_and_swap')
    equals(data['status'], 'browser_passed_owned_contexts_closed')
    equals(data['identity'], run_identity)
    require(integer(data['actorID']))
    role = data['customRoleID']
    require(type(role) is str and re.fullmatch(r'[A-Za-z0-9_-]{24}', role) is not None
            and role not in ('platform_admin', 'viewer', 'missing_bulk_role_000001'))
    targets = data['targets']
    require(type(targets) is list and len(targets) == 2)
    ids = []
    for index, target in enumerate(targets):
        exact(target, ('target', 'userID', 'beforeRole', 'afterRejectedRole', 'afterCommittedRole',
                       'beforePriv', 'afterRejectedPriv', 'afterCommittedPriv', 'profilePreserved', 'enabledPreserved'))
        require(integer(target['userID']))
        ids.append(target['userID'])
        before, after = ('users_read_only', 'viewer') if index == 0 else ('viewer', 'users_read_only')
        priv, new_priv = (2, 3) if index == 0 else (3, 2)
        equals(target, dict(target=ALIASES[index], userID=target['userID'], beforeRole=before,
                            afterRejectedRole=before, afterCommittedRole=after, beforePriv=priv,
                            afterRejectedPriv=priv, afterCommittedPriv=new_priv,
                            profilePreserved=True, enabledPreserved=True))
    require(len(set(ids + [data['actorID']])) == 3)
    equals(data['rejectedBatch'], dict(requestCount=1, itemCount=2, orderedTargets=ALIASES,
           orderedRoles=['viewer', 'missing_role'], httpStatus=404, error='not_found', auditDelta=0))
    equals(data['committedBatch'], dict(requestCount=1, itemCount=2, orderedTargets=ALIASES,
           orderedRoles=['viewer', 'users_read_only'], httpStatus=200, result='SUCCESS',
           actorSessionRevoked=False, auditDelta=2))
    sessions = data['targetSessions']
    require(type(sessions) is list and len(sessions) == 2)
    for index, session in enumerate(sessions):
        equals(session, dict(target=ALIASES[index], beforeHTTP=200, afterRejectedHTTP=200,
               afterCommittedHTTP=401, reloginHTTP=200, sameContextThroughRevocation=True,
               reloginAfterRevocation=True, sameIdentity=True, needChangePwd=False, hasMfa=index == 0,
               reloginMenu=[] if index == 0 else [dict(mark='users', auth=dict(readable=True, writeable=False))],
               usersHTTP=403 if index == 0 else 200, routeChecks=[index == 1] + [False] * 6))
    audit = data['audit']
    exact(audit, ('beforeTotal', 'afterRejectedTotal', 'afterCommittedTotal', 'exhausted',
                  'actorID', 'events', 'sameRequest', 'requestIDSHA256'))
    require(hex_value(audit['requestIDSHA256']))
    events = audit['events']
    require(type(events) is list and len(events) == 2)
    event_ids = []
    for index, event in enumerate(events):
        exact(event, ('eventID', 'targetID'))
        event_id = event['eventID']
        require(type(event_id) is str and re.fullmatch(r'auth\.[1-9][0-9]{0,18}', event_id) is not None
                and int(event_id[5:]) <= 9_223_372_036_854_775_807)
        equals(event['targetID'], ids[index])
        event_ids.append(event_id)
    require(len(set(event_ids)) == 2)
    equals(audit, dict(beforeTotal=0, afterRejectedTotal=0, afterCommittedTotal=2, exhausted=[True] * 3,
           actorID=data['actorID'], events=events, sameRequest=True, requestIDSHA256=audit['requestIDSHA256']))
    equals(data['checks'], dict(missingRoleHTTP=404, missingRoleError='not_found', filteredUserCount=2,
           creationOrderAscending=True, adminSessionHTTP=200, adminIdentityUnchanged=True,
           customRoleUserCount=1, localStorageEmpty=True, sessionStorageEmpty=True, oldReaderAfterLogoutHTTP=401))
    equals(data['proofBudget'], dict(administratorBefore=6, administratorAfter=9, administratorLimit=10,
           readerSensitive=3, newViewerSensitive=1, newAdministratorMutations=3, assignmentRequests=2))
    equals(data['ownedContexts'], dict(expected=2, closed=2))
    png_size(png)
    equals(data['screenshot'], dict(file=FILES[1], bytes=len(png), sha256=digest(png), width=1440, height=900,
           viewport=dict(width=1440, height=900), fullPage=False, proofControls=0, visibleTargets=2, finalRolesVerified=True))
    exact(sources, SOURCE_PATHS)
    require(all(hex_value(value) for value in sources.values()))
    equals(data['sourceSHA256'], sources)


def validate_runtime(raw):
    report = safe_json(raw)
    validate_report(report)  # Existing diagnostic schema is not modified here.
    equals([item['phase'] for item in report['phases']], PHASES)
    times = [item['at_ms'] for item in report['phases']]
    require(times == sorted(times))
    equals(report['processes'], dict(api=dict(started=True, before_cleanup=None, after_cleanup=0, forced_kill=False),
           worker=dict(started=False, before_cleanup=None, after_cleanup=None, forced_kill=False)))
    equals(report['http_panic'], False)
    equals(report['collection_errors'], [])
    equals(report['browser'], [])
    equals(report['browser_dropped'], 0)
    equals(report['browser_capture'], 'pending')
    return dict(status='runner_returned_success_with_post_cleanup_diagnostics', apiExitCode=0,
                apiForcedKill=False, workerStarted=False, httpPanic=False, afterCleanupPhase=True,
                runtimeSHA256=digest(raw), containerAbsence='not_independently_attested')


def validate_and_stage(root, run_identity, tested_tree, checkout_sources=None):
    # This entrypoint is useful to isolated offline fixtures. Only the CLI below
    # supplies checkout/run facts for real CI; caller input cannot add bindings.
    exact(run_identity, ('repository', 'suite', 'runID', 'runAttempt', 'event', 'testedCommit'))
    equals(run_identity, identity(dict(GITHUB_REPOSITORY=run_identity['repository'], GITHUB_RUN_ID=run_identity['runID'],
           GITHUB_RUN_ATTEMPT=run_identity['runAttempt'], GITHUB_EVENT_NAME=run_identity['event'],
           GITHUB_SHA=run_identity['testedCommit']), run_identity['testedCommit'], tested_tree))
    folder = checked_path(root, INPUT)
    require(folder.is_dir() and {path.name for path in folder.iterdir()} == set(FILES))
    destination = checked_path(root, OUTPUT)
    require(not destination.exists())
    original = bounded_file(root, INPUT + '/' + FILES[0], MAX_JSON)
    png = bounded_file(root, INPUT + '/' + FILES[1], MAX_PNG)
    sources = {path: digest(bounded_file(root, path, MAX_SOURCE)) for path in SOURCE_PATHS}
    if checkout_sources is not None:
        equals(sources, checkout_sources)
    data = safe_json(original)
    validate_summary(data, run_identity, sources, png)
    observation = validate_runtime(bounded_file(root, RUNTIME, MAX_JSON))
    staged = dict(browser=data, artifactBinding=dict(identity=run_identity, testedTree=tested_tree,
                  browserSummarySHA256=digest(original), sourceSHA256=sources), runnerObservation=observation)
    encoded = (json.dumps(staged, sort_keys=True, ensure_ascii=True, allow_nan=False, indent=2) + '\n').encode('utf-8')
    require(len(encoded) <= MAX_JSON)
    # All validation precedes exclusive creation; failed/partial stages are not
    # reused or uploaded (the workflow requires this step's success outcome).
    checked_path(root, OUTPUT)
    destination.mkdir(parents=True, exist_ok=False)
    outputs = {FILES[0]: encoded, FILES[1]: png}
    for name, content in outputs.items():
        checked_path(root, OUTPUT + '/' + name)
        with (destination / name).open('xb') as stream:
            stream.write(content)
    require({path.name for path in destination.iterdir()} == set(FILES))
    for name, content in outputs.items():
        equals(bounded_file(root, OUTPUT + '/' + name, MAX_JSON if name == FILES[0] else MAX_PNG), content)
    return staged


def validate_checkout(root, environment):
    checked_path(root, '.')
    def git(arguments, strip=True):
        completed = subprocess.run(['git', *arguments], cwd=root, check=True, stdout=subprocess.PIPE,
                                   stderr=subprocess.DEVNULL, timeout=10)
        require(len(completed.stdout) <= 16_384)
        decoded = completed.stdout.decode('ascii')
        return decoded.strip() if strip else decoded
    commit = git(['rev-parse', 'HEAD'])
    tree = git(['rev-parse', 'HEAD^{tree}'])
    current = identity(environment, commit, tree)
    # Check all tracked files, including staged changes; generated untracked
    # test results are allowed. Every bound source must also be tracked.
    equals(git(['diff', '--quiet', 'HEAD', '--']), '')
    tracked = git(['ls-files', '--error-unmatch', '--', *SOURCE_PATHS]).splitlines()
    equals(sorted(tracked), sorted(SOURCE_PATHS))
    # Index assume-unchanged/skip-worktree flags can hide worktree changes from
    # diff. Bind every source directly to its immutable HEAD tree blob instead.
    records = git(['ls-tree', '-z', 'HEAD', '--', *SOURCE_PATHS], strip=False).split('\0')
    require(records[-1] == '')
    blobs = {}
    for record in records[:-1]:
        match = re.fullmatch(r'100644 blob ([0-9a-f]{40})\t([^\0]+)', record)
        require(match is not None)
        blob, path = match.groups()
        require(path in SOURCE_PATHS and path not in blobs)
        blobs[path] = blob
    exact(blobs, SOURCE_PATHS)
    sources = {}
    for path in SOURCE_PATHS:
        raw = bounded_file(root, path, MAX_SOURCE)
        blob = hashlib.sha1(b'blob ' + str(len(raw)).encode('ascii') + b'\0' + raw).hexdigest()
        require(blob == blobs[path])
        sources[path] = digest(raw)
    return current, tree, sources


class ClosedParser(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError(ERROR)


def main(argv=None, environment=None):
    try:
        parser = ClosedParser()
        parser.add_argument('--source-root', type=Path, required=True)
        args = parser.parse_args(argv)
        root = args.source_root.absolute()
        current, tree, sources = validate_checkout(root, os.environ if environment is None else environment)
        validate_and_stage(root, current, tree, sources)
    except (OSError, ValueError, TypeError, KeyError, OverflowError, RecursionError, zlib.error,
            subprocess.SubprocessError):
        print(ERROR, file=sys.stderr)
        return 1
    print('access role bulk evidence validated')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
