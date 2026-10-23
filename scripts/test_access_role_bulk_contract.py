"""Offline rejection contracts, not synthetic business/visual acceptance.

Real PostgreSQL/API/browser execution and pixel review remain separate gates.
All validator fixtures below are isolated files with explicitly fake identities.
"""
import copy
import hashlib
import io
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zlib

import ci_workflow_contract as workflow
import ci_selection as selection
import validate_access_role_bulk_evidence as evidence

CANARY = 'SYNTHETIC_DO_NOT_EXPORT_CANARY'
BASE_WORKFLOW = 'adca3ced51556884f35c1603dbfc86713280ec01abc3c6a275d2d42a7b6e2a47'
BASE_HELPER = '902e134b2aa547e83a4d5b6daa6bcf869e40f00b2231472d481945b9cb2f5d06'
BASE_SPEC = 'b80d95e6de96063f03f97ba06538a3c0e389d7237a10da77a1793f92e44f042a'
DELTA = 'scripts/fixtures/access_role_bulk_ci_delta.json'


def raw_source(path):
    # read_text would silently normalize CRLF, defeating exact-byte contracts.
    return Path(path).read_bytes().decode('utf-8')


def chunk(kind, payload):
    return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload) & 0xffffffff)


def png_fixture(width=1440, height=900, color=6, pixels=None, before=b'', after=b'', compressed=None):
    channels = 3 if color == 2 else 4
    if pixels is None:
        pixels = (b'\0' + b'\0' * width * channels) * height
    if compressed is None:
        compressed = zlib.compress(pixels)
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, color, 0, 0, 0))
            + before + chunk(b'IDAT', compressed) + after + chunk(b'IEND', b''))


def runtime_fixture():
    return dict(schema=1, phases=[dict(phase=name, at_ms=index) for index, name in enumerate(evidence.PHASES)],
                processes=dict(api=dict(started=True, before_cleanup=None, after_cleanup=0, forced_kill=False),
                               worker=dict(started=False, before_cleanup=None, after_cleanup=None, forced_kill=False)),
                http_panic=False, browser=[], browser_capture='pending', browser_dropped=0, collection_errors=[])


def json_bytes(value):
    return json.dumps(value, sort_keys=True, allow_nan=False).encode()


def object_paths(value, prefix=()):
    """Enumerate every actual fixture object, including menu/auth and 46 keys."""
    if type(value) is dict:
        yield prefix, value
        for key, child in value.items():
            yield from object_paths(child, prefix + (key,))
    elif type(value) is list:
        for index, child in enumerate(value):
            yield from object_paths(child, prefix + (index,))


def leaves(value, prefix=()):
    if type(value) is dict:
        for key, child in value.items():
            yield from leaves(child, prefix + (key,))
    elif type(value) is list:
        for index, child in enumerate(value):
            yield from leaves(child, prefix + (index,))
    else:
        yield prefix, value


def at(value, path):
    for key in path:
        value = value[key]
    return value


class AccessRoleBulkEvidenceContract(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.folder = self.root / evidence.INPUT
        self.folder.mkdir(parents=True)
        for path in evidence.SOURCE_PATHS:
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(('isolated synthetic source: ' + path).encode())
        self.environment = dict(GITHUB_REPOSITORY='yunpiao/adtr', GITHUB_RUN_ID='123',
                                GITHUB_RUN_ATTEMPT='2', GITHUB_EVENT_NAME='pull_request', GITHUB_SHA='a' * 40)
        self.identity = evidence.identity(self.environment, 'a' * 40, 'b' * 40)
        self.tree = 'b' * 40
        self.png = png_fixture()
        self.summary = dict(schemaVersion=1, case='access_role_bulk_atomic_rollback_and_swap',
            status='browser_passed_owned_contexts_closed', identity=self.identity, actorID=1, customRoleID='R' * 24,
            targets=[dict(target=alias, userID=index + 2, beforeRole=before, afterRejectedRole=before,
                          afterCommittedRole=after, beforePriv=priv, afterRejectedPriv=priv,
                          afterCommittedPriv=new_priv, profilePreserved=True, enabledPreserved=True)
                     for index, (alias, before, after, priv, new_priv) in enumerate([
                         ('existing_reader', 'users_read_only', 'viewer', 2, 3),
                         ('new_viewer', 'viewer', 'users_read_only', 3, 2)])],
            rejectedBatch=dict(requestCount=1, itemCount=2, orderedTargets=list(evidence.ALIASES),
                               orderedRoles=['viewer', 'missing_role'], httpStatus=404, error='not_found', auditDelta=0),
            committedBatch=dict(requestCount=1, itemCount=2, orderedTargets=list(evidence.ALIASES),
                                orderedRoles=['viewer', 'users_read_only'], httpStatus=200,
                                result='SUCCESS', actorSessionRevoked=False, auditDelta=2),
            targetSessions=[dict(target=alias, beforeHTTP=200, afterRejectedHTTP=200, afterCommittedHTTP=401,
                                 reloginHTTP=200, sameContextThroughRevocation=True, reloginAfterRevocation=True,
                                 sameIdentity=True, needChangePwd=False, hasMfa=index == 0,
                                 reloginMenu=[] if index == 0 else [dict(mark='users', auth=dict(readable=True, writeable=False))],
                                 usersHTTP=403 if index == 0 else 200, routeChecks=[index == 1] + [False] * 6)
                            for index, alias in enumerate(evidence.ALIASES)],
            audit=dict(beforeTotal=0, afterRejectedTotal=0, afterCommittedTotal=2, exhausted=[True] * 3,
                       actorID=1, events=[dict(eventID='auth.10', targetID=2), dict(eventID='auth.11', targetID=3)],
                       sameRequest=True, requestIDSHA256=evidence.digest(b'FAKE_REQUEST_IDENTIFIER')),
            checks=dict(missingRoleHTTP=404, missingRoleError='not_found', filteredUserCount=2,
                        creationOrderAscending=True, adminSessionHTTP=200, adminIdentityUnchanged=True,
                        customRoleUserCount=1, localStorageEmpty=True, sessionStorageEmpty=True, oldReaderAfterLogoutHTTP=401),
            proofBudget=dict(administratorBefore=6, administratorAfter=9, administratorLimit=10,
                             readerSensitive=3, newViewerSensitive=1, newAdministratorMutations=3, assignmentRequests=2),
            ownedContexts=dict(expected=2, closed=2),
            screenshot=dict(file=evidence.FILES[1], bytes=len(self.png), sha256=evidence.digest(self.png),
                            width=1440, height=900, viewport=dict(width=1440, height=900),
                            fullPage=False, proofControls=0, visibleTargets=2, finalRolesVerified=True),
            sourceSHA256={path: evidence.digest((self.root / path).read_bytes()) for path in evidence.SOURCE_PATHS})
        self.runtime = runtime_fixture()
        (self.root / evidence.RUNTIME).parent.mkdir(parents=True)
        self.write_fixture()

    def write_fixture(self, summary=None, png=None, runtime=None):
        (self.folder / evidence.FILES[0]).write_bytes(json_bytes(self.summary if summary is None else summary))
        (self.folder / evidence.FILES[1]).write_bytes(self.png if png is None else png)
        (self.root / evidence.RUNTIME).write_bytes(json_bytes(self.runtime if runtime is None else runtime))

    def reject(self, summary=None):
        if summary is not None:
            (self.folder / evidence.FILES[0]).write_bytes(json_bytes(summary))
        with self.assertRaises((ValueError, OSError, zlib.error)) as result:
            evidence.validate_and_stage(self.root, self.identity, self.tree)
        self.assertNotIn(CANARY, str(result.exception))
        self.assertFalse((self.root / evidence.OUTPUT).exists())

    def test_positive_fixture_stages_exact_pair_and_does_not_claim_pixel_or_container_truth(self):
        staged = evidence.validate_and_stage(self.root, self.identity, self.tree)
        destination = self.root / evidence.OUTPUT
        self.assertEqual({p.name for p in destination.iterdir()}, set(evidence.FILES))
        self.assertEqual((destination / evidence.FILES[1]).read_bytes(), self.png)
        self.assertEqual(set(staged), {'browser', 'artifactBinding', 'runnerObservation'})
        self.assertEqual(staged['browser'], self.summary)
        self.assertEqual(staged['artifactBinding'], dict(identity=self.identity, testedTree=self.tree,
                         browserSummarySHA256=evidence.digest(json_bytes(self.summary)), sourceSHA256=self.summary['sourceSHA256']))
        self.assertEqual(staged['runnerObservation']['containerAbsence'], 'not_independently_attested')
        self.assertEqual(staged['runnerObservation']['runtimeSHA256'], evidence.digest(json_bytes(self.runtime)))
        self.assertNotIn(CANARY.encode(), (destination / evidence.FILES[0]).read_bytes())

    def test_every_root_and_nested_object_key_missing_unknown_and_wrong_type(self):
        count = 0
        for path, original in object_paths(self.summary):
            for key in original:
                for mode in ('missing', 'wrong'):
                    bad = copy.deepcopy(self.summary)
                    target = at(bad, path)
                    if mode == 'missing':
                        del target[key]
                    else:
                        target[key] = {CANARY: CANARY}
                    with self.subTest(path=path, key=key, mode=mode):
                        self.reject(bad)
                    count += 1
            bad = copy.deepcopy(self.summary)
            at(bad, path)[CANARY] = CANARY
            with self.subTest(path=path, mode='extra'):
                self.reject(bad)
        self.assertGreater(count, 300)

    def test_all_integer_and_boolean_leaf_types_are_strict(self):
        for path, original in leaves(self.summary):
            if type(original) not in (int, bool):
                continue
            bad = copy.deepcopy(self.summary)
            at(bad, path[:-1])[path[-1]] = (1 if original else 0) if type(original) is bool else True
            with self.subTest(path=path):
                self.reject(bad)

    def test_observed_ids_roles_order_status_access_audit_and_budget_relations(self):
        variants = [
            ('actorID', 0), ('actorID', 9_007_199_254_740_992), ('actorID', 2),
            ('customRoleID', 'viewer'), ('customRoleID', 'platform_admin'),
            ('customRoleID', 'missing_bulk_role_000001'), ('customRoleID', 'bad/role'),
        ]
        for key, value in variants:
            bad = copy.deepcopy(self.summary)
            bad[key] = value
            with self.subTest(key=key, value=value):
                self.reject(bad)
        mutations = [
            lambda d: d['targets'].reverse(), lambda d: d['targets'].pop(),
            lambda d: d['targets'].append(copy.deepcopy(d['targets'][0])),
            lambda d: d['targets'][1].update(userID=2), lambda d: d['targets'][0].update(afterRejectedRole='viewer'),
            lambda d: d['targets'][0].update(afterCommittedPriv=2), lambda d: d['targets'][1].update(profilePreserved=False),
            lambda d: d['targets'][0].update(enabledPreserved=False),
            lambda d: d['rejectedBatch']['orderedTargets'].reverse(), lambda d: d['rejectedBatch']['orderedRoles'].reverse(),
            lambda d: d['rejectedBatch'].update(httpStatus=400), lambda d: d['rejectedBatch'].update(auditDelta=1),
            lambda d: d['committedBatch'].update(requestCount=2), lambda d: d['committedBatch'].update(result='success'),
            lambda d: d['committedBatch'].update(actorSessionRevoked=True),
            lambda d: d['targetSessions'].reverse(), lambda d: d['targetSessions'][0].update(afterCommittedHTTP=200),
            lambda d: d['targetSessions'][1].update(afterRejectedHTTP=401),
            lambda d: d['targetSessions'][0].update(reloginAfterRevocation=False),
            lambda d: d['targetSessions'][1].update(sameContextThroughRevocation=False),
            lambda d: d['targetSessions'][0].update(reloginMenu=d['targetSessions'][1]['reloginMenu']),
            lambda d: d['targetSessions'][1]['reloginMenu'][0]['auth'].update(writeable=True),
            lambda d: d['targetSessions'][1].update(usersHTTP=403),
            lambda d: d['targetSessions'][0]['routeChecks'].__setitem__(0, True),
            lambda d: d['targetSessions'][1]['routeChecks'].__setitem__(6, True),
            lambda d: d['targetSessions'][1].update(needChangePwd=True),
            lambda d: d['audit'].update(actorID=2), lambda d: d['audit']['events'].reverse(),
            lambda d: d['audit']['events'][1].update(eventID='auth.10'),
            lambda d: d['audit']['events'][0].update(eventID='auth.01'),
            lambda d: d['audit']['events'][0].update(eventID='auth.9223372036854775808'),
            lambda d: d['audit']['events'][0].update(targetID=1),
            lambda d: d['audit'].update(sameRequest=False), lambda d: d['audit'].update(afterCommittedTotal=3),
            lambda d: d['audit'].update(requestIDSHA256=CANARY),
            lambda d: d['audit']['exhausted'].__setitem__(1, False),
            lambda d: d['proofBudget'].update(administratorAfter=10),
            lambda d: d['proofBudget'].update(newAdministratorMutations=4),
            lambda d: d['ownedContexts'].update(closed=1),
            lambda d: d['checks'].update(oldReaderAfterLogoutHTTP=200),
            lambda d: d['checks'].update(localStorageEmpty=False),
            lambda d: d['screenshot'].update(proofControls=1), lambda d: d['screenshot'].update(fullPage=True),
            lambda d: d['screenshot'].update(file='../' + evidence.FILES[1]),
            lambda d: d['screenshot'].update(sha256='0' * 64), lambda d: d['screenshot'].update(bytes=1),
        ]
        for index, mutate in enumerate(mutations):
            bad = copy.deepcopy(self.summary)
            mutate(bad)
            with self.subTest(mutation=index):
                self.reject(bad)

    def test_source_map_and_producer_identity_are_bound_to_current_run(self):
        self.assertEqual(len(evidence.SOURCE_PATHS), 46)
        self.assertEqual(list(evidence.SOURCE_PATHS), sorted(set(evidence.SOURCE_PATHS)))
        self.assertEqual(evidence.digest(json.dumps(list(evidence.SOURCE_PATHS), separators=(',', ':')).encode()),
                         '56df69dfed967025109e6e89440a4e799e0f2bdecb1212f05ab6fdf85f4898b2')
        for path in evidence.SOURCE_PATHS:
            bad = copy.deepcopy(self.summary)
            bad['sourceSHA256'][path] = '0' * 64
            with self.subTest(source=path):
                self.reject(bad)
        for key, value in [('runID', '124'), ('runAttempt', '3'), ('event', 'push'),
                           ('testedCommit', 'c' * 40), ('repository', 'other/repository'), ('suite', 'tasks')]:
            bad = copy.deepcopy(self.summary)
            bad['identity'][key] = value
            with self.subTest(identity=key):
                self.reject(bad)
        for key, value in [('GITHUB_RUN_ID', '01'), ('GITHUB_RUN_ID', '0'), ('GITHUB_RUN_ID', '1' * 21),
                           ('GITHUB_RUN_ATTEMPT', True), ('GITHUB_RUN_ATTEMPT', '1' * 21),
                           ('GITHUB_EVENT_NAME', 'workflow_dispatch'), ('GITHUB_SHA', 'A' * 40),
                           ('GITHUB_REPOSITORY', 'other/repository')]:
            environment = dict(self.environment, **{key: value})
            with self.subTest(environment=key, value=value), self.assertRaises(ValueError):
                evidence.identity(environment, 'a' * 40, self.tree)
        with self.assertRaises(ValueError):
            evidence.identity(self.environment, 'c' * 40, self.tree)
        with self.assertRaises(ValueError):
            evidence.identity(self.environment, 'a' * 40, 'not-a-tree')

    def test_bounded_duplicate_free_json_rejects_before_staging(self):
        raw = json_bytes(self.summary)
        variants = [b'', b'{', b'\xff', raw + b' trailing', b' ' * (evidence.MAX_JSON + 1),
                    b'{"schemaVersion":1,' + raw[1:], b'{"outer":{"x":1,"x":2}}',
                    b'{"x":NaN}', b'{"x":Infinity}', b'{"x":-Infinity}', b'{"x":1e999}',
                    b'[' * 13 + b'0' + b']' * 13, json_bytes([0] * 2048)]
        for index, bad in enumerate(variants):
            (self.folder / evidence.FILES[0]).write_bytes(bad)
            with self.subTest(case=index):
                self.reject()
        self.assertEqual(evidence.safe_json(b'{"x":"[\\\"{]"}'), {'x': '["{]'})

    def test_file_pair_membership_preexisting_output_and_traversal(self):
        for filename in evidence.FILES:
            target = self.folder / filename
            saved = target.read_bytes()
            target.unlink()
            self.reject()
            target.write_bytes(saved)
        extra = self.folder / 'unexpected.txt'
        extra.write_text(CANARY)
        self.reject()
        extra.unlink()
        destination = self.root / evidence.OUTPUT
        destination.mkdir(parents=True)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree)
        self.assertEqual(list(destination.iterdir()), [])
        for relative in ('../escape', '/tmp/escape', 'web/../../escape'):
            with self.subTest(path=relative), self.assertRaises(ValueError):
                evidence.checked_path(self.root, relative)

    def test_symlinked_input_source_output_and_ancestor_never_stage(self):
        for relative in (evidence.INPUT + '/' + evidence.FILES[0], evidence.INPUT + '/' + evidence.FILES[1],
                         evidence.RUNTIME, *evidence.SOURCE_PATHS):
            target = self.root / relative
            saved = target.read_bytes()
            target.unlink()
            external = self.root / 'synthetic-link-target'
            external.write_bytes(saved)
            target.symlink_to(external)
            with self.subTest(path=relative):
                self.reject()
            target.unlink()
            target.write_bytes(saved)
            external.unlink()
        for relative in (evidence.INPUT, 'web/test-results', 'internal/auth', 'scripts/fixtures'):
            target = self.root / relative
            relocated = target.with_name(target.name + '-real')
            target.rename(relocated)
            target.symlink_to(relocated, target_is_directory=True)
            with self.subTest(parent=relative):
                self.reject()
            target.unlink()
            relocated.rename(target)
        destination = self.root / evidence.OUTPUT
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.symlink_to(self.root / 'absent-destination', target_is_directory=True)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree)
        destination.unlink()
        destination.parent.rmdir()
        (self.root / 'web/playwright-report').symlink_to(self.root / 'elsewhere', target_is_directory=True)
        self.reject()

    def test_source_bytes_and_root_symlink_cannot_rebind_old_producer(self):
        changed = self.root / evidence.SOURCE_PATHS[0]
        changed.write_bytes(b'changed synthetic source')
        self.reject()
        linked = self.root / 'linked-root'
        linked.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(linked, self.identity, self.tree)
        self.assertFalse((self.root / evidence.OUTPUT).exists())

    def test_staging_readback_failure_is_not_cli_success_or_raw_error_export(self):
        original = evidence.bounded_file
        def damaged_read(root, relative, limit):
            if str(relative).startswith(evidence.OUTPUT + '/'):
                raise OSError(CANARY)
            return original(root, relative, limit)
        output, errors = io.StringIO(), io.StringIO()
        with patch.object(evidence, 'validate_checkout', return_value=(self.identity, self.tree, self.summary['sourceSHA256'])), \
             patch.object(evidence, 'bounded_file', side_effect=damaged_read), \
             patch('sys.stdout', output), patch('sys.stderr', errors):
            self.assertEqual(evidence.main(['--source-root', str(self.root)], self.environment), 1)
        self.assertEqual(output.getvalue(), '')
        self.assertEqual(errors.getvalue(), evidence.ERROR + '\n')
        # This partial directory is deliberately not reused, deleted or treated
        # as success. The upload gate requires the validator's success outcome.
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree)

    def test_runtime_phases_cleanup_health_and_raw_capture_state(self):
        mutations = [
            lambda d: d['phases'].pop(), lambda d: d['phases'].reverse(),
            lambda d: d['phases'].append(dict(phase='after-cleanup', at_ms=7)),
            lambda d: d['phases'][3].update(at_ms=0),
            lambda d: d['processes']['api'].update(started=False),
            lambda d: d['processes']['api'].update(before_cleanup=0),
            lambda d: d['processes']['api'].update(after_cleanup=None),
            lambda d: d['processes']['api'].update(after_cleanup=1),
            lambda d: d['processes']['api'].update(after_cleanup=True),
            lambda d: d['processes']['api'].update(forced_kill=True),
            lambda d: d['processes']['worker'].update(started=True),
            lambda d: d['processes']['worker'].update(after_cleanup=0),
            lambda d: d.update(http_panic=True), lambda d: d.update(http_panic=None),
            lambda d: d.update(collection_errors=['snapshot']), lambda d: d.update(browser_dropped=1),
            lambda d: d.update(browser_capture='present'), lambda d: d.update(browser_capture='missing'),
            lambda d: d.update(browser=[dict(phase='read-json', method='GET', route='unknown',
                                             elapsed_ms=0, at_ms=0, outcome='start', status=None)]),
            lambda d: d.update(untrusted=CANARY),
        ]
        for index, mutate in enumerate(mutations):
            bad = copy.deepcopy(self.runtime)
            mutate(bad)
            (self.root / evidence.RUNTIME).write_bytes(json_bytes(bad))
            with self.subTest(mutation=index):
                self.reject()
        runtime = self.root / evidence.RUNTIME
        runtime.unlink()
        self.reject()
        runtime.write_bytes(b'{"schema":1,"schema":1}')
        self.reject()

    def test_png_structure_content_and_metadata_corruption_never_stages(self):
        header = self.png[:33]
        raw_pixels = (b'\0' + b'\0' * 1440 * 4) * 900
        compressed = zlib.compress(raw_pixels)
        variants = [b'', b'not-a-png', self.png[:-1], self.png + b'after-iend',
                    self.png[:20] + bytes([self.png[20] ^ 1]) + self.png[21:],
                    self.png + b'\0' * evidence.MAX_PNG, png_fixture(width=1439), png_fixture(height=899),
                    png_fixture(color=0), png_fixture(before=chunk(b'IHDR', header[16:29])),
                    png_fixture(before=chunk(b'tEXt', CANARY.encode())),
                    png_fixture(before=chunk(b'iTXt', CANARY.encode())),
                    png_fixture(before=chunk(b'zTXt', CANARY.encode())),
                    png_fixture(before=chunk(b'eXIf', CANARY.encode())),
                    png_fixture(before=chunk(b'iCCP', CANARY.encode())),
                    png_fixture(before=chunk(b'zzZZ', b'')),
                    png_fixture(before=chunk(b'sRGB', b'\4')), png_fixture(before=chunk(b'sRGB', b'')),
                    png_fixture(before=chunk(b'sRGB', b'\0') * 2), png_fixture(before=chunk(b'gAMA', b'\0' * 4)),
                    png_fixture(before=chunk(b'cHRM', struct.pack('>8I', *([100001] * 8)))),
                    png_fixture(before=chunk(b'pHYs', struct.pack('>IIB', 1, 1, 2))),
                    png_fixture(after=chunk(b'sRGB', b'\0') + chunk(b'IDAT', compressed)),
                    png_fixture(compressed=b''), png_fixture(compressed=compressed[:-1]),
                    png_fixture(compressed=compressed + b'extra'), png_fixture(compressed=compressed + compressed),
                    png_fixture(pixels=raw_pixels[:-1]), png_fixture(pixels=raw_pixels + b'\0'),
                    png_fixture(pixels=b'\5' + raw_pixels[1:]),
                    header + chunk(b'IDAT', compressed) + chunk(b'IEND', b'x'),
                    header + chunk(b'IEND', b''), self.png[:8] + chunk(b'IDAT', compressed) + self.png[8:],
                    self.png[:8] + struct.pack('>I', 0xffffffff) + self.png[12:]]
        for index, bad_png in enumerate(variants):
            bad = copy.deepcopy(self.summary)
            bad['screenshot'].update(bytes=len(bad_png), sha256=evidence.digest(bad_png))
            self.write_fixture(summary=bad, png=bad_png)
            with self.subTest(case=index):
                self.reject()
        for color in (2, 6):
            self.assertEqual(evidence.png_size(png_fixture(color=color)), (1440, 900))
        valid_metadata = chunk(b'sRGB', b'\0') + chunk(b'gAMA', struct.pack('>I', 45455))
        self.assertEqual(evidence.png_size(png_fixture(before=valid_metadata)), (1440, 900))

    def test_closed_cli_failure_does_not_echo_canary_exception_environment_or_argv(self):
        for error in (ValueError(CANARY), OSError(CANARY), subprocess.TimeoutExpired(CANARY, 10)):
            output = io.StringIO()
            with patch.object(evidence, 'validate_checkout', side_effect=error), patch('sys.stderr', output):
                self.assertEqual(evidence.main(['--source-root', str(self.root)], dict(SECRET=CANARY)), 1)
            self.assertEqual(output.getvalue(), evidence.ERROR + '\n')
        output = io.StringIO()
        with patch('sys.stderr', output):
            self.assertEqual(evidence.main(['--unknown-' + CANARY]), 1)
        self.assertEqual(output.getvalue(), evidence.ERROR + '\n')

    def test_checkout_identity_clean_tracked_tree_and_fixed_bounded_git_commands(self):
        records = []
        for path in evidence.SOURCE_PATHS:
            raw = (self.root / path).read_bytes()
            blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
            records.append('100644 blob ' + blob + '\t' + path + '\0')
        results = [b'a' * 40 + b'\n', b'b' * 40 + b'\n', b'',
                   ('\n'.join(sorted(evidence.SOURCE_PATHS)) + '\n').encode(), ''.join(records).encode()]
        calls = []
        def fake_run(arguments, **kwargs):
            calls.append((arguments, kwargs))
            return subprocess.CompletedProcess(arguments, 0, stdout=results[len(calls) - 1])
        with patch.object(evidence.subprocess, 'run', side_effect=fake_run):
            self.assertEqual(evidence.validate_checkout(self.root, self.environment),
                             (self.identity, self.tree, self.summary['sourceSHA256']))
        self.assertEqual([call[0] for call in calls], [
            ['git', 'rev-parse', 'HEAD'], ['git', 'rev-parse', 'HEAD^{tree}'],
            ['git', 'diff', '--quiet', 'HEAD', '--'],
            ['git', 'ls-files', '--error-unmatch', '--', *evidence.SOURCE_PATHS],
            ['git', 'ls-tree', '-z', 'HEAD', '--', *evidence.SOURCE_PATHS]])
        for _, kwargs in calls:
            self.assertEqual(kwargs['timeout'], 10)
            self.assertTrue(kwargs['check'])
            self.assertNotIn('shell', kwargs)
        for bad in (subprocess.CalledProcessError(1, ['git']), subprocess.TimeoutExpired(['git'], 10)):
            with patch.object(evidence.subprocess, 'run', side_effect=bad), self.assertRaises(subprocess.SubprocessError):
                evidence.validate_checkout(self.root, self.environment)


    def test_head_blob_binding_rejects_hidden_worktree_changes_and_malformed_tree(self):
        records = []
        for path in evidence.SOURCE_PATHS:
            raw = (self.root / path).read_bytes()
            blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
            records.append(('100644 blob ' + blob + '\t' + path + '\0').encode())
        tree_raw = b''.join(records)
        prefix = [b'a' * 40 + b'\n', b'b' * 40 + b'\n', b'',
                  ('\n'.join(sorted(evidence.SOURCE_PATHS)) + '\n').encode()]
        def responses(raw):
            return [subprocess.CompletedProcess(['git'], 0, stdout=value) for value in prefix + [raw]]
        target = self.root / evidence.SOURCE_PATHS[0]
        original = target.read_bytes()
        for hidden_flag in ('assume-unchanged', 'skip-worktree'):
            # Simulate the same clean diff/tracked list returned when these
            # index flags hide altered bytes. No real index or repo is used.
            target.write_bytes(original + b' changed under hidden index flag')
            with self.subTest(flag=hidden_flag), \
                 patch.object(evidence.subprocess, 'run', side_effect=responses(tree_raw)), \
                 self.assertRaises(ValueError):
                evidence.validate_checkout(self.root, self.environment)
            target.write_bytes(original)
            self.assertFalse((self.root / evidence.OUTPUT).exists())
        variants = [b'', tree_raw[:-1], tree_raw + b'\n', b''.join(records[1:]),
                    tree_raw + records[0], tree_raw.replace(b'100644', b'100755', 1),
                    tree_raw.replace(b'100644', b'120000', 1), tree_raw.replace(b' blob ', b' tree ', 1),
                    tree_raw.replace(records[0][12:52], b'0' * 40, 1),
                    tree_raw.replace(records[0][12:52], b'G' * 40, 1),
                    tree_raw.replace(evidence.SOURCE_PATHS[0].encode(), b'../' + CANARY.encode(), 1),
                    tree_raw.replace(evidence.SOURCE_PATHS[0].encode(), b'/absolute', 1),
                    tree_raw + ('100644 blob ' + '0' * 40 + '\textra\0').encode()]
        for index, raw in enumerate(variants):
            with self.subTest(tree_mutation=index), \
                 patch.object(evidence.subprocess, 'run', side_effect=responses(raw)), \
                 self.assertRaises(ValueError):
                evidence.validate_checkout(self.root, self.environment)
            self.assertFalse((self.root / evidence.OUTPUT).exists())

    def test_source_snapshot_cannot_change_between_checkout_and_staging(self):
        bound = dict(self.summary['sourceSHA256'])
        path = evidence.SOURCE_PATHS[0]
        (self.root / path).write_bytes(b'changed after checkout verification')
        bad = copy.deepcopy(self.summary)
        bad['sourceSHA256'][path] = evidence.digest((self.root / path).read_bytes())
        self.write_fixture(summary=bad)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree, bound)
        self.assertFalse((self.root / evidence.OUTPUT).exists())


# Exact new workflow window is independently frozen, not inferred from the
# mutable fixture. Its inverse must recover the entire previous workflow.
ACCESS_WINDOW_SHA256 = '559821546900e8a45ce73d7e487b25ae7c5c60eede6d95b31f8163c39e4bb928'
HELPER_BLOCK_SHA256 = 'd4ae094998bb8aa183e965ce8352ab4ac56e4f4db09ea7f4e25e527562bb82c1'


def restore_helper_source(text):
    start = 'def restore_access_role_bulk(text):\n'
    end = 'def restore_archive_target_observation(text):\n'
    evidence.require(text.count(start) == 1 and text.count(end) == 1)
    left, right = text.index(start), text.index(end)
    evidence.require(left < right)
    block = text[left:right]
    evidence.require(evidence.digest(block.encode()) == HELPER_BLOCK_SHA256)
    hook = '    text = restore_access_role_bulk(text)\n'
    evidence.require(text.count(hook) == 1)
    expected = end + '    """Reverse only the exact maintenance three-file validation/upload wiring."""\n' + hook
    evidence.require(text.count(expected) == 1)
    restored = (text[:left] + text[right:]).replace(hook, '', 1)
    evidence.require(evidence.digest(restored.encode()) == BASE_HELPER)
    return restored


class AccessRoleBulkWorkflowContract(unittest.TestCase):
    def test_exact_success_only_two_file_steps_and_historical_consumers(self):
        text = raw_source('.github/workflows/ci.yml')
        delta = json.loads(raw_source(DELTA))
        self.assertEqual(set(delta), {'baselineSHA256', 'replacements'})
        self.assertEqual(delta['baselineSHA256'], BASE_WORKFLOW)
        self.assertEqual(len(delta['replacements']), 1)
        change = delta['replacements'][0]
        self.assertEqual(set(change), {'old', 'new'})
        self.assertEqual(change['old'], '      - name: Validate exact maintenance target observation evidence\n')
        self.assertEqual(evidence.digest(change['new'].encode()), ACCESS_WINDOW_SHA256)
        self.assertEqual(text.count(change['new']), 1)
        self.assertGreater(text.index(change['new']), text.index('run: ${{ matrix.command }}'))
        block = change['new'][:-len(change['old'])]
        self.assertEqual(block.count('      - name: '), 2)
        self.assertEqual(block.count('id: access-role-bulk-evidence'), 1)
        self.assertEqual(block.count('timeout-minutes: 1'), 2)
        for required in (
            "if: ${{ success() && matrix.name == 'access' }}",
            "if: ${{ success() && matrix.name == 'access' && steps.access-role-bulk-evidence.outcome == 'success' }}",
            'run: python3 scripts/validate_access_role_bulk_evidence.py --source-root .',
            'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2',
            'name: access-role-bulk-${{ github.run_id }}-${{ github.run_attempt }}',
            'if-no-files-found: error', 'retention-days: 7',
        ):
            self.assertIn(required, block)
        paths = [line.strip() for line in block.splitlines() if line.startswith('            web/')]
        self.assertEqual(paths, [evidence.OUTPUT + '/' + name for name in evidence.FILES])
        for denied in ('*', 'always()', 'continue-on-error', 'include-hidden-files'):
            self.assertNotIn(denied, block)
        self.assertEqual(evidence.digest(workflow.restore_access_role_bulk(text).encode()), BASE_WORKFLOW)
        consumers = [
            (workflow.restore_archive_target_observation, 'scripts/fixtures/archive_target_observation_ci_delta.json'),
            (workflow.restore_health_recovery, 'scripts/fixtures/tasks_health_recovery_ci_delta.json'),
            (workflow.restore_messages_isolation, 'scripts/fixtures/messages_isolation_ci_delta.json'),
            (workflow.restore_directory_v4, 'scripts/fixtures/directory_v4_ci_delta.json'),
            (workflow.restore_selection, 'scripts/ci_workflow_selection_delta.json'),
        ]
        for consumer, fixture in consumers:
            expected = json.loads(raw_source(fixture))['baselineSHA256']
            with self.subTest(consumer=consumer.__name__):
                self.assertEqual(evidence.digest(consumer(text).encode()), expected)
        self.assertEqual(workflow.reviewed_workflow(), workflow.restore_selection(text))
        self.assertEqual(workflow.reviewed_source('.github/workflows/ci.yml'), workflow.restore_selection(text))
        restore_helper_source(raw_source('scripts/ci_workflow_contract.py'))

    def test_workflow_actual_new_and_old_byte_mutations_reject_all_outer_consumers(self):
        text = raw_source('.github/workflows/ci.yml')
        change = json.loads(raw_source(DELTA))['replacements'][0]
        new, old = change['new'], change['old']
        self.assertEqual(text.count(new), 1)
        stripped = text.replace(new, old, 1)
        variants = [stripped, text.replace(new, '', 1), text.replace(new, new * 2, 1),
                    text.replace(new, old, 1) + new, text.replace('\n', '\r\n'), text + '\n',
                    text.replace(new, new.replace('\n', ' \n', 1), 1)]
        edits = [
            ("matrix.name == 'access'", "matrix.name == 'tasks'", 2),
            ('success()', 'always()', 2), ('timeout-minutes: 1', 'timeout-minutes: 2', 2),
            ('id: access-role-bulk-evidence', 'id: changed', 1),
            (" && steps.access-role-bulk-evidence.outcome == 'success'", '', 1),
            ('python3 scripts/validate_access_role_bulk_evidence.py --source-root .', 'true', 1),
            ('ea165f8d65b6e75b540449e92b4886f43607fa02', 'main', 1),
            ('github.run_id', 'github.run_number', 1), ('github.run_attempt', 'github.run_number', 1),
            (evidence.OUTPUT + '/' + evidence.FILES[0], 'web/test-results/**', 1),
            (evidence.OUTPUT + '/' + evidence.FILES[1], 'raw.log', 1),
            ('if-no-files-found: error', 'if-no-files-found: warn', 1), ('retention-days: 7', 'retention-days: 8', 1),
        ]
        for needle, replacement, expected_count in edits:
            self.assertEqual(new.count(needle), expected_count)
            # Mutate every occurrence independently, including both gates.
            cursor = 0
            for _ in range(expected_count):
                index = new.index(needle, cursor)
                changed = new[:index] + replacement + new[index + len(needle):]
                variants.append(text.replace(new, changed, 1))
                cursor = index + len(needle)
        lines = new.splitlines(keepends=True)
        for index, line in enumerate(lines):
            variants.append(text.replace(new, ''.join(lines[:index] + lines[index + 1:]), 1))
        variants.append(text.replace(new, new + '        continue-on-error: true\n', 1))
        for needle, replacement, expected_count in [('contents: read', 'contents: write', 9),
                                                    ('run: make check', 'run: true', 1),
                                                    ('max-parallel: 8', 'max-parallel: 9', 1)]:
            self.assertEqual(text.count(needle), expected_count)
            cursor = 0
            for _ in range(expected_count):
                index = text.index(needle, cursor)
                variants.append(text[:index] + replacement + text[index + len(needle):])
                cursor = index + len(needle)
        consumers = (workflow.restore_access_role_bulk, workflow.restore_archive_target_observation,
                     workflow.restore_health_recovery, workflow.restore_messages_isolation,
                     workflow.restore_directory_v4, workflow.restore_selection)
        for index, bad in enumerate(variants):
            for consumer in consumers:
                with self.subTest(mutation=index, consumer=consumer.__name__), self.assertRaises(ValueError):
                    consumer(bad)

    def test_delta_schema_missing_extra_wrong_baseline_and_unreviewed_replacement(self):
        text = raw_source('.github/workflows/ci.yml')
        delta = json.loads(raw_source(DELTA))
        variants = [None, [], {}, dict(delta, extra=True), dict(delta, baselineSHA256='0' * 64),
                    dict(delta, baselineSHA256=BASE_WORKFLOW.upper()), dict(delta, replacements=[]),
                    dict(delta, replacements=delta['replacements'] * 2), dict(delta, replacements=True)]
        for key in delta:
            bad = copy.deepcopy(delta)
            del bad[key]
            variants.append(bad)
        for key in ('old', 'new'):
            for value in ('', None, True, CANARY):
                bad = copy.deepcopy(delta)
                bad['replacements'][0][key] = value
                variants.append(bad)
            bad = copy.deepcopy(delta)
            del bad['replacements'][0][key]
            variants.append(bad)
        bad = copy.deepcopy(delta)
        bad['replacements'][0]['unknown'] = CANARY
        variants.append(bad)
        raw_variants = [json_bytes(bad) for bad in variants]
        raw_variants.extend([b'{' + b' ' * 8192, b'{"baselineSHA256":1,"baselineSHA256":2}', b'{', b'[' * 2000 + b']' * 2000])
        original = Path.read_bytes
        for index, raw in enumerate(raw_variants):
            def read(path):
                return raw if str(path) == DELTA else original(path)
            with self.subTest(mutation=index), patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                workflow.restore_access_role_bulk(text)

    def test_actual_helper_addition_hook_location_and_old_bytes_are_exact(self):
        text = raw_source('scripts/ci_workflow_contract.py')
        restore_helper_source(text)
        start = text.index('def restore_access_role_bulk(text):\n')
        end = text.index('def restore_archive_target_observation(text):\n')
        block = text[start:end]
        hook = '    text = restore_access_role_bulk(text)\n'
        self.assertEqual(text.count(hook), 1)
        variants = [text.replace(block, '', 1), text.replace(block, block * 2, 1),
                    text.replace(block, '', 1) + block, text.replace(hook, '', 1),
                    text.replace(hook, hook * 2, 1), text.replace(hook, '', 1) + hook,
                    text.replace('\n', '\r\n'), text + '\n']
        # Every new function line, not just a few hand-selected inverse tokens.
        lines = block.splitlines(keepends=True)
        for index, line in enumerate(lines):
            variants.append(text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1))
        for bad in variants:
            with self.subTest(sha256=evidence.digest(bad.encode())), self.assertRaises(ValueError):
                restore_helper_source(bad)

    def test_selector_and_runner_scope_remains_unchanged(self):
        selector = raw_source('scripts/ci_selection.py')
        inventory = json.loads(raw_source('scripts/ci_inventory.json'))
        self.assertIn("path.startswith('scripts/test_') and path.endswith('_contract.py')", selector)
        self.assertNotIn('validate_access_role_bulk_evidence.py', selector)
        self.assertNotIn('access-role-bulk', json.dumps(inventory))
        runner = raw_source('scripts/test_auth_e2e.py')
        config = raw_source('web/playwright.config.ts')
        self.assertIn('else 420)', runner)
        self.assertIn('workers: 1', config)
        self.assertIn('retries: 0', config)
        self.assertIn("python3 -m unittest discover -s scripts -p '*_contract.py'", raw_source('Makefile'))

        frozen = {
            'web/playwright.config.ts':
                '840d1fad95b3901c82e0d3a6b2b5c40fc7f70faa5df6c794c707d6e652118d3b',
            'scripts/test_auth_e2e.py':
                '6d55979bcaff97f5ab23b4c3d8eafbb65a080a3c49555eb2e17823750f6aa2a9',
            'scripts/e2e_owned_container.py':
                '694bb508066df1bc2cedb2a46a6cff72a19a88d3ba60cbb820a90d9efb6fe68c',
            'scripts/e2e_runtime_diagnostics.py':
                '284477912328c92531515df10eaa54c145012a8ad0aeea481549d3f56750b51e',
            'scripts/ci_selection.py':
                '8cfa0f286d50119ff26bce84b74c997a7fd0cc7efe49afe575caf95dc73e8523',
            'scripts/ci_inventory.json':
                '1d9ef348def9d9b55ee4d0f8bb7da340a3c9049f9e90b5826bd71f3a39c47f24',
        }
        for path, expected in frozen.items():
            self.assertEqual(evidence.digest(Path(path).read_bytes()), expected)


    def test_standalone_validator_and_ci_changes_conservatively_select_full_matrix(self):
        inventory = json.loads(raw_source('scripts/ci_inventory.json'))
        for path in ('scripts/validate_access_role_bulk_evidence.py', '.github/workflows/ci.yml',
                     'scripts/ci_workflow_contract.py', DELTA):
            plan = selection.select([path], inventory, [{}, {}])
            with self.subTest(path=path):
                self.assertEqual(plan['browser'], inventory['browser'])
                self.assertEqual(plan['fixed'], inventory['fixed'])
                self.assertEqual(plan['auth'], list(range(1, 9)))
                self.assertTrue(all(plan['groups'].values()))
        plan = selection.select(['scripts/test_access_role_bulk_contract.py'], inventory, [{}, {}])
        self.assertTrue(plan['groups']['contracts'])
        self.assertEqual(plan['browser'], [])
        path = 'web/e2e/access.spec.ts'
        plan = selection.select([path], inventory, [{path: ''}, {path: ''}])
        self.assertEqual([entry['name'] for entry in plan['browser']], ['access'])


# Frozen source additions; independent review and original bytes are separate gates.
SPEC_SHA256 = 'af4cabcbbf242c09257033373936648e0bfebc0935f61b837b1ae2c65155503c'
SPEC_BYTES = 52056
SPEC_INSERTIONS = (
    ('helpers', 'type Permission = {\n', 'before', '// AD-F-176 bulk acceptance: additive host helpers, no production API changes.\n', '// End AD-F-176 additive host helpers.\n\n', '6d4808b3fd0cc6306d09e899b1d62719e3110d23b647be3b5c37402703c78a39', 12027),
    ('guard', '  test.setTimeout(300_000);\n', 'after', '  // AD-F-176 fixture and evidence guard; never target an external account.\n', '  // End AD-F-176 fixture and evidence guard.\n\n', '0fbbab6c28a53af36d67ea9c7c2e49774356f9274a9350b391273cf0284bfb29', 1307),
    ('scenario', '    await navigateTo(readerPage, "退出登录");\n', 'before', '    // AD-F-176 real two-user atomic rollback, swap, revocation and re-login.\n', '    // End AD-F-176 real two-user scenario; original logout and finally follow.\n\n', '7817f7aae2bed5fbbd803e008967e855d48d701a2b4dea085b39162a92865972', 20572),
    ('writer', '  } finally {\n    await readerContext.close();\n  }\n', 'after', '  // AD-F-176 success is emitted only after both owned target contexts close.\n', '  // End AD-F-176 success writer.\n', '4c3ac10fd927c87b6ff2e05444f3e9808ae5df3d1ad04ed8fa142e103e163b3d', 571),
)


def access_blocks(text):
    evidence.require(type(text) is str)
    found = []
    for name, anchor, placement, begin, end, expected, size in SPEC_INSERTIONS:
        evidence.require(text.count(begin) == text.count(end) == text.count(anchor) == 1)
        left, right = text.index(begin), text.index(end) + len(end)
        evidence.require(left < right)
        block = text[left:right]
        evidence.require(len(block.encode()) == size and evidence.digest(block.encode()) == expected)
        joined = block + anchor if placement == 'before' else anchor + block
        evidence.require(text.count(joined) == 1)
        found.append((name, block))
    return found


def restore_access_spec(text):
    blocks = access_blocks(text)
    original = text
    for _, block in reversed(blocks):
        evidence.require(text.count(block) == 1)
        text = text.replace(block, '', 1)
    evidence.require(len(text.encode()) == 17579 and evidence.digest(text.encode()) == BASE_SPEC)
    evidence.require(len(original.encode()) == SPEC_BYTES and evidence.digest(original.encode()) == SPEC_SHA256)
    return text


class AccessRoleBulkBrowserSourceContract(unittest.TestCase):
    def test_exact_additive_inverse_and_source_key_identity(self):
        text = raw_source('web/e2e/access.spec.ts')
        restored = restore_access_spec(text)
        self.assertEqual(restored.count('test.setTimeout(300_000);'), 1)
        self.assertEqual(restored.count('    await readerContext.close();\n'), 1)
        source_block = text.split('  // SOURCE-PATHS-BEGIN\n', 1)[1].split('  // SOURCE-PATHS-END\n', 1)[0]
        paths = [json.loads(line.strip().rstrip(',')) for line in source_block.splitlines()]
        self.assertEqual(paths, list(evidence.SOURCE_PATHS))
        self.assertEqual(text.count('test.use({ trace: "off", screenshot: "off", video: "off" });'), 1)
        self.assertLess(text.index('await bulkDenied(readerContext, "/api/auth/me", 401);'),
                        text.index('bulkPhase = "reader_relogin";'))
        self.assertLess(text.index('await bulkDenied(viewerContext, "/api/auth/me", 401);'),
                        text.index('bulkPhase = "reader_relogin";'))
        self.assertLess(text.index('await viewerContext.close();'), text.index('await readerContext.close();'))
        self.assertLess(text.index('await readerContext.close();'), text.index('bulkEvidence.ownedContexts ='))
        self.assertLess(text.index('await navigateTo(readerPage, "退出登录");'), text.index('bulkEvidence.ownedContexts ='))
        for denied in ('page.route(', '.fulfill(', 'route.fetch(', 'window.fetch =', 'page.request.post("/api/access/assignments"'):
            self.assertNotIn(denied, ''.join(block for _, block in access_blocks(text)))

    def test_every_actual_added_source_line_is_mutated_not_only_historical_projection(self):
        text = raw_source('web/e2e/access.spec.ts')
        restore_access_spec(text)
        deleted = duplicated = moved = 0
        for name, block in access_blocks(text):
            self.assertEqual(text.count(block), 1)
            lines = block.splitlines(keepends=True)
            for index, line in enumerate(lines):
                if not line.strip():
                    continue
                # Positions identify repeated/generated assertions exactly; no
                # ambiguous replace-all can silently skip a later occurrence.
                variants = [lines[:index] + lines[index + 1:],
                            lines[:index] + [line] + lines[index:]]
                deleted += 1
                duplicated += 1
                if index + 1 < len(lines) and lines[index] != lines[index + 1]:
                    variants.append(lines[:index] + [lines[index + 1], line] + lines[index + 2:])
                    moved += 1
                for mutation, changed in enumerate(variants):
                    mutated = text.replace(block, ''.join(changed), 1)
                    self.assertNotEqual(mutated, text)
                    with self.subTest(block=name, line=index + 1, mutation=mutation), self.assertRaises(ValueError):
                        restore_access_spec(mutated)
        self.assertGreater(deleted, 700)
        self.assertEqual(deleted, duplicated)
        self.assertGreater(moved, 700)

    def test_every_block_sentinel_and_original_anchor_relocation_is_rejected(self):
        text = raw_source('web/e2e/access.spec.ts')
        blocks = dict(access_blocks(text))
        for name, anchor, _, begin, end, _, _ in SPEC_INSERTIONS:
            block = blocks[name]
            self.assertEqual(text.count(block), 1)
            for token in (block, begin, end, anchor):
                self.assertEqual(text.count(token), 1)
                missing = text.replace(token, '', 1)
                variants = (missing, text.replace(token, token * 2, 1), token + missing, missing + token)
                for index, bad in enumerate(variants):
                    self.assertNotEqual(bad, text)
                    with self.subTest(block=name, token=token[:50], mutation=index), self.assertRaises(ValueError):
                        restore_access_spec(bad)
        for bad in (text.replace('\n', '\r\n'), text + '\n',
                    text.replace('test.setTimeout(300_000);', 'test.setTimeout(600_000);', 1),
                    text.replace('expect(await deniedWrite.json()).toEqual({ error: "forbidden" });',
                                 'expect(true).toBe(true);', 1)):
            self.assertNotEqual(bad, text)
            with self.assertRaises(ValueError):
                restore_access_spec(bad)

    def test_new_raw_source_cannot_add_mock_retry_production_grant_or_secret_export(self):
        text = raw_source('web/e2e/access.spec.ts')
        name, block = access_blocks(text)[0]
        self.assertEqual(name, 'helpers')
        self.assertEqual(text.count(block), 1)
        additions = [
            'test.describe.configure({ retries: 1 });\n',
            'await page.route("**/*", async (route) => route.fulfill({ body: "{}" }));\n',
            'await page.evaluate(() => { window.fetch = async () => new Response("{}"); });\n',
            'await context.request.post("/api/access/assignments", { data: raw });\n',
            'await context.request.post("/api/access/permissions", { data: { writeable: true } });\n',
            'console.error(error.stack, error.cause, request.postData(), process.env);\n',
            'bulkEvidence.raw = JSON.stringify({ request, response, cookie, csrfToken, totpCode });\n',
            'for (let attempt = 0; attempt < 2; attempt++) await bulkSubmit();\n',
        ]
        for addition in additions:
            bad = text.replace(block, block + addition, 1)
            with self.subTest(addition=addition[:55]), self.assertRaises(ValueError):
                restore_access_spec(bad)


    def test_handwritten_semantic_mutations_target_every_actual_occurrence(self):
        text = raw_source('web/e2e/access.spec.ts')
        restore_access_spec(text)
        mutations = (
            ('fixture-loopback', 'base.protocol === "http:"', 'base.protocol === "https:"', 1),
            ('fixture-synthetic-admin', 'process.env.ADTR_E2E_USERNAME === "e2e-admin"', 'true', 1),
            ('fixture-exclusive-output', 'mkdirSync(bulkOutput);', 'mkdirSync(bulkOutput, { recursive: true });', 1),
            ('create-second-target', 'const created = await bulkSubmit(', 'const created = await forbiddenRetry(', 1),
            ('viewer-original-context', 'viewerContext = await context.browser()!.newContext({', 'viewerContext = context; void ({', 1),
            ('viewer-initial-login', 'await login(viewerPage, viewerUsername, viewerInitial);', 'void viewerInitial;', 1),
            ('viewer-forced-password-change', 'await changeInitialPassword(viewerPage, viewerInitial, viewerChanged);', 'void viewerChanged;', 1),
            ('baseline-actor', 'const adminBefore = await bulkRead(context, "/api/auth/me");', 'const adminBefore = adminAfter;', 1),
            ('actor-target-distinct', 'bulkRequire(actorID !== savedUser.ID && actorID !== viewerID);', 'bulkRequire(true);', 1),
            ('creation-order', '.selectOption("1");', '.selectOption("-1");', 2),
            ('strict-target-count', 'bulkRequire(Array.isArray(value.List) && value.List.length === 2);', 'bulkRequire(Array.isArray(value.List));', 1),
            ('first-request-selection', 'if (selected) {', 'if (false) {', 1),
            ('first-request-lock', 'selected = request;', 'selected = undefined;', 1),
            ('actual-ordered-payload', 'bulkRequire(payload(bulkObject(JSON.parse(raw))));', 'bulkRequire(true);', 1),
            ('private-payload-size', 'Buffer.byteLength(raw, "utf8") <= 8192', 'true', 1),
            ('request-origin-identity', 'request.url() === origin + path', 'true', 1),
            ('request-frame-identity', 'request.frame() === page.mainFrame()', 'true', 1),
            ('request-no-redirect', 'request.redirectedFrom() === null', 'true', 1),
            ('response-selected-object', 'if (closed || candidate.request() !== selected) return;', 'if (closed) return;', 1),
            ('response-read-once', 'const body = candidate.json();', 'const body = candidate.json(); void candidate.json();', 1),
            ('response-immediate-handler', 'void body.then(', 'void body.then(() => undefined); void body.then(', 1),
            ('response-first-result', 'if (responseSeen) {', 'if (false) {', 1),
            ('response-metadata-status', 'candidate.status() === status', 'candidate.status() >= 200', 1),
            ('response-service-worker', '!candidate.fromServiceWorker()', 'true', 1),
            ('response-closed-dto', 'bulkRequire(accepted(object));', 'bulkRequire(true);', 1),
            ('logical-deadline', 'const timer = setTimeout(stop, 15_000);', 'const timer = setTimeout(stop, 60_000);', 1),
            ('controlled-both-branches', 'await Promise.all([action, response])', 'await Promise.race([action, response])', 1),
            ('exact-one-request', '!closed && outcomes.every(Boolean) && count === 1 && result', 'outcomes.some(Boolean) && result', 1),
            ('late-generation-guard', 'if (closed) return;', 'if (false) return;', 3),
            ('timer-cleanup', 'clearTimeout(timer);', 'void timer;', 1),
            ('listener-request-detach', 'page.off("request", onRequest);', 'void onRequest;', 1),
            ('listener-response-detach', 'page.off("response", onResponse);', 'void onResponse;', 1),
            ('failed-request-detach', 'page.off("requestfailed", onFailed);', 'void onFailed;', 1),
            ('close-listener-detach', 'page.off("close", stop);', 'void stop;', 1),
            ('crash-listener-detach', 'page.off("crash", stop);', 'void stop;', 1),
            ('rollback-first-role', 'rejected[i].roleID === before[i].roleID', 'true', 1),
            ('rollback-profile', 'bulkUnchangedProfile(before[i], rejected[i]);', 'void before[i];', 1),
            ('commit-profile', 'bulkUnchangedProfile(before[i], committed[i]);', 'void before[i];', 1),
            ('old-reader-revocation', 'await bulkDenied(readerContext, "/api/auth/me", 401);', 'await bulkDenied(readerContext, "/api/auth/me", 200);', 1),
            ('old-viewer-revocation', 'await bulkDenied(viewerContext, "/api/auth/me", 401);', 'await bulkDenied(viewerContext, "/api/auth/me", 200);', 1),
            ('audit-success-source', 'row.source === "auth"', 'true', 1),
            ('audit-actor-id', 'row.userId === actorID', 'true', 1),
            ('audit-actor-username', 'row.loginUser === username', 'true', 1),
            ('audit-target-id', 'bulkObject(JSON.parse(row.eventArgs)).targetId === targetID', 'true', 1),
            ('audit-request-path', 'args.path === "/api/access/assignments"', 'true', 1),
            ('audit-shared-request', 'auditRequest === args.requestId', 'true', 1),
            ('audit-distinct-events', 'events[0].eventID !== events[1].eventID', 'true', 1),
            ('reader-effective-menu', 'bulkRequire(bulkExact(demotedMenu, []));', 'bulkRequire(true);', 1),
            ('reader-forbidden-users', 'await bulkDenied(readerContext, "/api/access/users", 403);', 'await bulkRead(readerContext, "/api/access/users");', 1),
            ('effective-route-vector', 'const results = [allowed, false, false, false, false, false, false];', 'const results = [true, true, true, true, true, true, true];', 1),
            ('effective-route-request', 'data: { paths: bulkRoutes },', 'data: { paths: [] },', 1),
            ('storage-empty', 'localStorage.length === 0 && sessionStorage.length === 0', 'true', 1),
            ('real-safe-screenshot', 'const screenshot = await page.screenshot({', 'const screenshot = await syntheticPng({', 1),
            ('proof-control-check', 'input[type="password"], input[name="totpCode"], .proof', 'input[data-never-present]', 1),
            ('complete-row-viewport', '.toBeInViewport({ ratio: 1 });', '.toBeInViewport({ ratio: 0 });', 2),
            ('actual-mutation-counters', 'Number(assignmentRequests) === 2', 'true', 2),
            ('new-context-close', 'await viewerContext.close();', 'void viewerContext;', 1),
            ('old-context-close', 'await readerContext.close();', 'void readerContext;', 1),
            ('closed-phase-errors', 'throw new Error(`access_role_bulk_failed:${bulkPhase}`);', 'throw new Error(JSON.stringify(error));', 1),
            ('writer-closed-contexts', 'bulkEvidence.ownedContexts = { expected: 2, closed: 2 };', 'bulkEvidence.ownedContexts = { expected: 2, closed: 1 };', 1),
            ('writer-current-sources', 'bulkEvidence.sourceSHA256 = bulkSources();', 'bulkEvidence.sourceSHA256 = {};', 1),
            ('proof-nine-of-ten', 'administratorAfter: 9,', 'administratorAfter: 10,', 1),
            ('proof-no-budget-extension', 'administratorLimit: 10,', 'administratorLimit: 20,', 1),
            ('missing-role-real-404', '`/api/access/roles/detail?roleID=${missingRole}`,\n            404,', '`/api/access/roles/detail?roleID=${missingRole}`,\n            400,', 1),
            ('rejected-not-found-dto', '(body) => bulkExact(body, { error: "not_found" }),', '(body) => true,', 1),
            ('successful-exact-dto', 'bulkExact(body, { result: "SUCCESS", sessionRevoked: false })', 'true', 1),
            ('rollback-reader-original-session', 'await bulkRead(readerContext, "/api/auth/me"),', '({ ID: savedUser.ID }),', 2),
            ('rollback-viewer-original-session', 'await bulkRead(viewerContext, "/api/auth/me"),', '({ ID: viewerID }),', 2),
            ('zero-success-audit-before-and-rejection', 'await auditRows(0);', 'await auditRows(1);', 2),
            ('two-committed-audits', 'const audit = await auditRows(2);', 'const audit = await auditRows(1);', 1),
            ('administrator-current-identity', 'adminAfter.ID === actorID', 'true', 1),
            ('reader-mfa-relogin', 'await freshCode(readerMfa)', '"000000"', 2),
            ('viewer-real-relogin', 'await login(viewerPage, viewerUsername, viewerChanged);', 'void viewerChanged;', 1),
            ('promoted-menu-exact-grant', '{ mark: "users", auth: { readable: true, writeable: false } }', '{ mark: "users", auth: { readable: true, writeable: true } }', 2),
            ('promoted-users-live-read', '(await bulkRead(viewerContext, "/api/access/users")).List', '[]', 1),
            ('enabled-profile-preservation', '"disabled",', '"ignored-disabled",', 1),
            ('safe-final-role-cells', '.toContainText(expectedRole);', '.toContainText("");', 1),
            ('proof-sensitive-reader-count', 'readerSensitive: 3,', 'readerSensitive: 4,', 1),
            ('proof-sensitive-viewer-count', 'newViewerSensitive: 1,', 'newViewerSensitive: 2,', 1),
            ('fixed-evidence-parent-create', 'mkdirSync(dirname(bulkOutput));', 'mkdirSync(dirname(bulkOutput), { recursive: true });', 1),
            ('fixed-evidence-parent-directory', 'lstatSync(dirname(bulkOutput)).isDirectory()', 'true', 1),
            ('fixed-evidence-parent-no-symlink', '!lstatSync(dirname(bulkOutput)).isSymbolicLink()', 'true', 1),
        )
        for name, needle, replacement, count in mutations:
            self.assertEqual(text.count(needle), count, name)
            cursor = 0
            for occurrence in range(count):
                index = text.index(needle, cursor)
                bad = text[:index] + replacement + text[index + len(needle):]
                self.assertNotEqual(bad, text)
                with self.subTest(semantic=name, occurrence=occurrence), self.assertRaises(ValueError):
                    restore_access_spec(bad)
                cursor = index + len(needle)


    def test_both_401_reads_cannot_move_after_relogin(self):
        text = raw_source('web/e2e/access.spec.ts')
        anchor = '      await login(readerPage, readerUsername, readerChanged);\n'
        self.assertEqual(text.count(anchor), 1)
        for target in ('readerContext', 'viewerContext'):
            call = '      await bulkDenied(' + target + ', "/api/auth/me", 401);\n'
            self.assertEqual(text.count(call), 1)
            bad = text.replace(call, '', 1).replace(anchor, anchor + call, 1)
            self.assertGreater(bad.index(call), bad.index(anchor) + len(anchor) - 1)
            with self.subTest(context=target), self.assertRaises(ValueError):
                restore_access_spec(bad)


if __name__ == '__main__':
    unittest.main()
