"""Offline tests of the transfer trust boundaries, not Docker roundtrip proof."""
from contextlib import ExitStack
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_postgres_image as transfer
import probe_ci_postgres_image as probe


CHECKOUT = 'a' * 40
ENV = {'GITHUB_RUN_ID': '1234', 'GITHUB_RUN_ATTEMPT': '1',
       'ADTR_EVENT_HEAD_SHA': 'b' * 40, 'ADTR_EVENT_BASE_SHA': 'c' * 40}
CURRENT = {'run_id': '1234', 'checkout_sha': CHECKOUT,
           'event': {key: ENV[key] for key in ('ADTR_EVENT_HEAD_SHA', 'ADTR_EVENT_BASE_SHA')}}
LOADED = {'Id': transfer.IMAGE_ID, 'Os': 'linux', 'Architecture': 'amd64', 'RepoDigests': []}


def raw(value):
    return json.dumps(value, separators=(',', ':')).encode()


def fixture_chain():
    manifest = {'schemaVersion': 2, 'mediaType': transfer.MANIFEST_TYPE,
                'config': {'mediaType': transfer.CONFIG_TYPE, 'digest': transfer.IMAGE_ID, 'size': 8488},
                'layers': [{'mediaType': 'application/vnd.oci.image.layer.v1.tar+gzip',
                            'digest': 'sha256:' + 'd' * 64, 'size': 1} for _ in range(10)]}
    index = {'schemaVersion': 2, 'mediaType': transfer.INDEX_TYPE,
             'manifests': [{'mediaType': transfer.MANIFEST_TYPE,
                            'digest': transfer.sha256_bytes(raw(manifest)), 'size': len(raw(manifest)),
                            'platform': {'architecture': 'amd64', 'os': 'linux'}}]}
    return index, manifest


class ImageTransferContract(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.directory = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        self.index, self.manifest = fixture_chain()
        self.stack.enter_context(patch.object(transfer, 'INDEX_DIGEST', transfer.sha256_bytes(raw(self.index))))
        self.stack.enter_context(patch.object(transfer, 'MANIFEST_DIGEST', transfer.sha256_bytes(raw(self.manifest))))
        self.archive = b'synthetic Docker archive; no Docker runtime claim'
        self.archive_sha = hashlib.sha256(self.archive).hexdigest()
        self.metadata = {**copy.deepcopy(CURRENT), 'schema': 1, 'producer_attempt': '1',
                         'reference': transfer.REFERENCE, 'platform': 'linux/amd64',
                         'image_id': transfer.IMAGE_ID, 'archive_sha256': self.archive_sha,
                         'archive_bytes': len(self.archive)}
        self.write_bundle()
        self.calls = []

    def write_bundle(self):
        (self.directory / 'index.json').write_bytes(raw(self.index))
        (self.directory / 'manifest.json').write_bytes(raw(self.manifest))
        (self.directory / 'metadata.json').write_bytes(raw(self.metadata))
        (self.directory / 'postgres.tar.gz').write_bytes(self.archive)

    def fake_run(self, command, **kwargs):
        self.calls.append(command)
        if command[:2] == ['git', 'rev-parse']:
            return subprocess.CompletedProcess(command, 0, CHECKOUT + '\n', '')
        if command[:3] == ['docker', 'image', 'inspect']:
            return subprocess.CompletedProcess(command, 0, json.dumps([LOADED]), '')
        if command[:3] == ['docker', 'image', 'load']:
            return subprocess.CompletedProcess(command, 0, '', '')
        raise AssertionError(f'Unexpected command, especially no registry fallback: {command}')

    def consume(self, environ=ENV):
        transfer.consume(self.directory, self.archive_sha, '1', environ, self.fake_run)

    def assert_no_docker(self):
        self.assertFalse(any(command[0] == 'docker' for command in self.calls))

    def test_producer_outputs_bind_the_archive_bytes_and_context(self):
        destination = self.directory / 'producer'
        output = self.directory / 'outputs'
        environ = dict(ENV, GITHUB_OUTPUT=str(output))
        saved = []
        def run(command, **kwargs):
            if command[:3] == ['docker', 'image', 'save']:
                saved.append(command)
                Path(command[command.index('--output') + 1]).write_bytes(b'synthetic saved image')
                return subprocess.CompletedProcess(command, 0)
            return self.fake_run(command, **kwargs)
        with patch('prepare_ci_image.prepare_image') as prepare, \
             patch.object(transfer, 'registry_bytes', side_effect=[raw(self.index), raw(self.manifest)]):
            transfer.produce(destination, environ, run)
        self.assertEqual(prepare.call_count, 1)
        self.assertEqual(saved[0][-1], transfer.IMAGE_ID)
        values = dict(line.split('=', 1) for line in output.read_text().splitlines())
        self.assertEqual(values['archive_sha256'], transfer.sha256_file(destination / 'postgres.tar.gz'))
        self.assertEqual(values['producer_attempt'], '1')
        self.assertEqual(values['checkout_sha'], CHECKOUT)
        self.assertEqual(set(p.name for p in destination.iterdir()), transfer.FILES)
        transfer.validate_bundle(destination, values['archive_sha256'], CURRENT, '1')

    def test_real_probe_negative_paths_reject_before_docker(self):
        cases = probe.prove_negative_paths(self.directory, self.archive_sha, '1', ENV, self.fake_run)
        self.assertEqual(cases, ['wrong-archive-sha', 'wrong-run', 'wrong-checkout', 'corrupt-archive'])
        self.assert_no_docker()

    def test_raw_registry_retry_is_bounded_and_never_changes_reference(self):
        calls = []
        delays = []
        results = iter([subprocess.CompletedProcess([], 1, b'', b'toomanyrequests'),
                        subprocess.CompletedProcess([], 0, b'exact raw bytes', b'')])
        def run(command, **kwargs):
            calls.append(command)
            return next(results)
        self.assertEqual(transfer.registry_bytes(transfer.REFERENCE, run, delays.append), b'exact raw bytes')
        self.assertEqual(delays, [10])
        self.assertTrue(all(command == ['docker', 'buildx', 'imagetools', 'inspect', '--raw', transfer.REFERENCE]
                            for command in calls))
        for diagnostic in [b'permission denied', b'unknown error', b'x509 error with toomanyrequests']:
            with self.assertRaisesRegex(transfer.ImageTransferError, 'Non-retryable'):
                transfer.registry_bytes(transfer.REFERENCE,
                    lambda *args, **kwargs: subprocess.CompletedProcess([], 1, b'', diagnostic),
                    lambda seconds: self.fail('hard failure slept'))

    def test_valid_chain_and_archive_load_exact_image_id_without_repo_digest(self):
        self.consume()
        self.assertTrue((self.directory / 'verified.json').is_file())
        self.assertEqual(self.calls[1:3], [
            ['docker', 'image', 'load', '--input', str(self.directory / 'postgres.tar.gz')],
            ['docker', 'image', 'inspect', transfer.IMAGE_ID]])

    def test_failed_job_rerun_reuses_earlier_producer_attempt_in_same_run(self):
        self.consume(dict(ENV, GITHUB_RUN_ATTEMPT='2'))
        self.assertTrue((self.directory / 'verified.json').is_file())

    def test_original_checkout_run_and_event_are_all_bound(self):
        for field, value in [('run_id', '999'), ('checkout_sha', 'd' * 40),
                             ('event', dict(CURRENT['event'], ADTR_EVENT_HEAD_SHA='e' * 40))]:
            with self.subTest(field=field):
                self.metadata[field] = value
                self.write_bundle()
                with self.assertRaisesRegex(transfer.ImageTransferError, 'different run, checkout or event'):
                    self.consume()
                self.assert_no_docker()
                self.metadata[field] = copy.deepcopy(CURRENT[field])

    def test_metadata_source_platform_id_attempt_and_archive_are_not_trusted(self):
        changes = [('reference', transfer.REFERENCE.replace('public.ecr.aws', 'other.invalid')),
                   ('platform', 'linux/arm64'), ('image_id', 'sha256:' + 'e' * 64),
                   ('producer_attempt', '2'), ('archive_sha256', 'f' * 64), ('archive_bytes', 1),
                   ('schema', 2)]
        for field, value in changes:
            with self.subTest(field=field):
                original = self.metadata[field]
                self.metadata[field] = value
                self.write_bundle()
                with self.assertRaises(transfer.ImageTransferError):
                    self.consume()
                self.assert_no_docker()
                self.metadata[field] = original

    def test_archive_mutation_fails_before_load_even_when_size_is_unchanged(self):
        (self.directory / 'postgres.tar.gz').write_bytes(b'!' + self.archive[1:])
        with self.assertRaisesRegex(transfer.ImageTransferError, 'SHA256 mismatch before docker load'):
            self.consume()
        self.assert_no_docker()

    def test_missing_or_malformed_expected_sha_is_not_replaced_by_metadata(self):
        for value in ['', 'xyz', 'f' * 64]:
            with self.assertRaises(transfer.ImageTransferError):
                transfer.consume(self.directory, value, '1', ENV, self.fake_run)
            self.assert_no_docker()

    def test_missing_extra_or_symlink_files_fail_before_load(self):
        for name in transfer.FILES:
            path = self.directory / name
            saved = path.read_bytes()
            path.unlink()
            with self.assertRaisesRegex(transfer.ImageTransferError, 'missing or unexpected'):
                self.consume()
            path.write_bytes(saved)
        extra = self.directory / 'unexpected'
        extra.write_text('x')
        with self.assertRaisesRegex(transfer.ImageTransferError, 'missing or unexpected'):
            self.consume()
        extra.unlink()
        # A malicious receipt symlink must not be followed by consume().
        (self.directory / 'verified.json').symlink_to(self.directory / 'metadata.json')
        with self.assertRaisesRegex(transfer.ImageTransferError, 'unsafe'):
            self.consume()
        self.assert_no_docker()

    def test_raw_index_and_manifest_bytes_cannot_be_reserialized_or_changed(self):
        for name in ['index.json', 'manifest.json']:
            path = self.directory / name
            saved = path.read_bytes()
            path.write_bytes(saved + b'\n')
            with self.assertRaises(transfer.ImageTransferError):
                self.consume()
            self.assert_no_docker()
            path.write_bytes(saved)

    def test_unknown_media_types_missing_fields_duplicate_platform_and_config_are_rejected(self):
        # Change the synthetic test trust root too, to exercise semantic checks
        # behind the digest guard. Production fixed constants are never changed.
        variants = []
        for field, value in [('mediaType', 'unknown'), ('schemaVersion', 1), ('manifests', [])]:
            index = copy.deepcopy(self.index); index[field] = value
            variants.append((index, self.manifest))
        index = copy.deepcopy(self.index); index['manifests'] *= 2
        variants.append((index, self.manifest))
        for path, value in [('mediaType', 'unknown'), ('config', {}), ('layers', []),
                            ('config', dict(self.manifest['config'], digest='sha256:' + 'f' * 64))]:
            manifest = copy.deepcopy(self.manifest); manifest[path] = value
            index = copy.deepcopy(self.index)
            index['manifests'][0].update(digest=transfer.sha256_bytes(raw(manifest)), size=len(raw(manifest)))
            variants.append((index, manifest))
        for index, manifest in variants:
            with patch.object(transfer, 'INDEX_DIGEST', transfer.sha256_bytes(raw(index))), \
                 patch.object(transfer, 'MANIFEST_DIGEST', transfer.sha256_bytes(raw(manifest))):
                with self.assertRaises(transfer.ImageTransferError):
                    transfer.validate_chain(raw(index), raw(manifest))

    def test_loaded_id_and_platform_mismatch_fail_without_a_receipt(self):
        for field, value in [('Id', 'sha256:' + 'f' * 64), ('Os', 'windows'), ('Architecture', 'arm64')]:
            image = dict(LOADED, **{field: value})
            def run(command, **kwargs):
                if command[:3] == ['docker', 'image', 'inspect']:
                    return subprocess.CompletedProcess(command, 0, json.dumps([image]), '')
                return self.fake_run(command, **kwargs)
            with self.assertRaisesRegex(transfer.ImageTransferError, 'identity/platform mismatch'):
                transfer.consume(self.directory, self.archive_sha, '1', ENV, run)
            self.assertFalse((self.directory / 'verified.json').exists())

    def test_load_failure_propagates_and_never_pulls(self):
        def run(command, **kwargs):
            if command[:3] == ['docker', 'image', 'load']:
                raise subprocess.CalledProcessError(1, command)
            return self.fake_run(command, **kwargs)
        with self.assertRaises(subprocess.CalledProcessError):
            transfer.consume(self.directory, self.archive_sha, '1', ENV, run)
        self.assertFalse((self.directory / 'verified.json').exists())

    def runtime_env(self):
        return dict(ENV, ADTR_CI_POSTGRES_BUNDLE=str(self.directory),
                    ADTR_CI_POSTGRES_ARCHIVE_SHA=self.archive_sha, ADTR_CI_POSTGRES_PRODUCER_ATTEMPT='1')

    def test_runtime_requires_receipt_and_keeps_pull_never(self):
        with self.assertRaisesRegex(transfer.ImageTransferError, 'receipt is required'):
            transfer.postgres_image_args(transfer.REFERENCE, self.runtime_env(), self.fake_run)
        self.consume()
        self.assertEqual(transfer.postgres_image_args(transfer.REFERENCE, self.runtime_env(), self.fake_run),
                         ['--pull=never', transfer.IMAGE_ID])
        (self.directory / 'verified.json').write_text('{}')
        with self.assertRaisesRegex(transfer.ImageTransferError, 'receipt differs'):
            transfer.postgres_image_args(transfer.REFERENCE, self.runtime_env(), self.fake_run)

    def test_local_flow_retains_original_pin_and_no_compose_override(self):
        self.assertEqual(transfer.postgres_image_args(transfer.REFERENCE, {}, self.fake_run), [transfer.REFERENCE])
        with transfer.postgres_compose_files({}, self.fake_run) as files:
            self.assertEqual(files, [])
        self.assertEqual(self.calls, [])
        with self.assertRaisesRegex(transfer.ImageTransferError, 'source pin mismatch'):
            transfer.postgres_image_args('postgres:latest', {}, self.fake_run)

    def test_compose_override_changes_only_db_identity_and_pull_policy(self):
        self.consume()
        with transfer.postgres_compose_files(self.runtime_env(), self.fake_run) as files:
            self.assertEqual(files[0], '-f')
            self.assertEqual(files[1], str(Path('compose.yaml').resolve()))
            override = Path(files[3])
            self.assertEqual(json.loads(override.read_text()),
                             {'services': {'db': {'image': transfer.IMAGE_ID, 'pull_policy': 'never'}}})
        self.assertFalse(override.exists())

    def test_bad_run_or_git_checkout_context_fails(self):
        for environ in [{}, dict(ENV, GITHUB_RUN_ID='123\n'), dict(ENV, GITHUB_RUN_ATTEMPT='0'),
                        dict(ENV, ADTR_EVENT_HEAD_SHA='bad')]:
            with self.assertRaises(transfer.ImageTransferError):
                transfer.context(environ, self.fake_run)
        with self.assertRaisesRegex(transfer.ImageTransferError, 'checkout SHA'):
            transfer.context(ENV, lambda *args, **kwargs: subprocess.CompletedProcess([], 0, 'bad', ''))


class RealDatabaseProbeContract(unittest.TestCase):
    def test_probe_uses_private_owned_no_pull_container_and_checks_real_version(self):
        for version in ['170006', '170005']:
            calls = []
            def run(command, **kwargs):
                calls.append(command)
                if command[1] == 'inspect':
                    return subprocess.CompletedProcess(command, 0, transfer.IMAGE_ID + '\n', '')
                if 'psql' in command:
                    return subprocess.CompletedProcess(command, 0, version + '\n', '')
                return subprocess.CompletedProcess(command, 0, '', '')
            with patch.object(probe, 'postgres_image_args', return_value=['--pull=never', transfer.IMAGE_ID]), \
                 patch.object(probe, 'remove_owned_container') as cleanup:
                if version == '170006':
                    result = probe.prove_database({}, run, lambda _: None)
                    self.assertTrue(result['database_ready'])
                else:
                    with self.assertRaisesRegex(transfer.ImageTransferError, 'PostgreSQL version'):
                        probe.prove_database({}, run, lambda _: None)
                cleanup.assert_called_once()
            launch = calls[0]
            self.assertIn('--pull=never', launch)
            self.assertEqual(launch[-1], transfer.IMAGE_ID)
            self.assertEqual(launch[launch.index('--network') + 1], 'none')
            self.assertIn('--label', launch)
            self.assertEqual(launch[launch.index('-e') + 1], 'POSTGRES_PASSWORD')
            ready = next(command for command in calls if 'pg_isready' in command)
            self.assertIn('127.0.0.1', ready)


class WorkflowImageGateContract(unittest.TestCase):
    def test_probe_is_two_jobs_on_an_explicit_diagnostic_branch_only(self):
        normal = Path('.github/workflows/ci.yml').read_text()
        text = Path('.github/workflows/ci-image-probe.yml').read_text()
        events = text.split('permissions: {}', 1)[0]
        self.assertIn("branches: ['ci/image-transfer-probe/**']", events)
        self.assertNotIn('pull_request:', events)
        self.assertNotIn('workflow_dispatch:', events)
        self.assertEqual(re.findall(r'^  ([a-z][a-z-]*):$', text.split('jobs:', 1)[1], re.MULTILINE),
                         ['prepare-postgres', 'image-roundtrip'])
        self.assertEqual(normal.split('  prepare-postgres:', 1)[1].split('  contracts:', 1)[0],
                         text.split('  prepare-postgres:', 1)[1].split('  image-roundtrip:', 1)[0])
        self.assertIn('scripts/probe_ci_postgres_image.py', text)
        self.assertIn('    branches: [main]', normal)
        self.assertNotIn('ci/image-transfer-probe/', normal)

    def test_immutable_output_preflight_rejects_missing_multiple_or_invalid_ids(self):
        text = Path('.github/workflows/ci.yml').read_text()
        block = text.split('      - name: Require explicit immutable producer outputs', 1)[1].split('      - name:', 1)[0]
        command = block.split('        run: |', 1)[1]
        valid = {'ARTIFACT_ID': '123', 'ARCHIVE_SHA': 'a' * 64, 'PRODUCER_ATTEMPT': '1'}
        self.assertEqual(subprocess.run(['bash', '-c', command], env=valid).returncode, 0)
        for key in valid:
            for value in ['', '0', '123,456', 'abc', '1\n']:
                with self.subTest(key=key, value=value):
                    self.assertNotEqual(subprocess.run(['bash', '-c', command], env=dict(valid, **{key: value})).returncode, 0)

    def test_all_35_jobs_preserve_their_gates_and_bounded_parallelism(self):
        text = Path('.github/workflows/ci.yml').read_text()
        matches = list(re.finditer(r'^  ([a-z][a-z-]*):\n', text.split('jobs:\n', 1)[1], re.MULTILINE))
        jobs_text = text.split('jobs:\n', 1)[1]
        jobs = {match[1]: jobs_text[match.start():matches[i + 1].start() if i + 1 < len(matches) else len(jobs_text)]
                for i, match in enumerate(matches)}
        self.assertEqual(set(jobs), {'prepare-postgres', 'contracts', 'auth-integration', 'browser',
                                    'native-no-store', 'native-no-store-fixed', 'user-assets-fixed', 'verify'})
        self.assertEqual(len(re.findall(r'^          - name:', jobs['browser'], re.MULTILINE)), 24)
        self.assertIn('shard: [1, 2, 3, 4]', jobs['auth-integration'])
        self.assertIn('suite: [user-assets-v2, user-assets-v2-readers]', jobs['user-assets-fixed'])
        self.assertEqual(1 + 1 + 4 + 24 + 1 + 1 + 2 + 1, 35)
        for name in ['auth-integration', 'browser', 'user-assets-fixed']:
            expected_parallelism = 4 if name == 'browser' else 2
            self.assertIn(f'max-parallel: {expected_parallelism}', jobs[name])
            self.assertIn('fail-fast: false', jobs[name])
        for name in ['contracts', 'auth-integration', 'browser', 'user-assets-fixed']:
            job = jobs[name]
            self.assertIn('needs: [prepare-postgres]', job)
            self.assertIn('artifact-ids: ${{ needs.prepare-postgres.outputs.artifact_id }}', job)
            self.assertNotIn('github-token:', job)
            self.assertNotIn('run-id:', job)
            self.assertNotIn('name: postgres-${{ github.run_id }}-${{ github.run_attempt }}', job)
            self.assertIn('ADTR_CI_POSTGRES_BUNDLE: ${{ runner.temp }}/adtr-postgres', job)
            self.assertLess(job.index('Require explicit immutable producer outputs'), job.index('Download this run'))
            self.assertLess(job.index('ci_postgres_image.py consume'), job.index('scripts/test_integration.py')
                            if name == 'auth-integration' else len(job))
        self.assertNotIn('needs: [prepare-postgres]', jobs['native-no-store'])
        self.assertNotIn('needs: [prepare-postgres]', jobs['native-no-store-fixed'])
        self.assertIn('permissions: {}', text)
        self.assertNotIn('continue-on-error', text)
        playwright = Path('web/playwright.config.ts').read_text()
        self.assertIn('workers: 1,', playwright)
        self.assertIn('retries: 0,', playwright)

    def test_prepare_and_every_original_gate_fail_closed_on_failure_cancel_or_skip(self):
        text = Path('.github/workflows/ci.yml').read_text().split('  verify:\n', 1)[1]
        self.assertIn('if: ${{ always() }}', text)
        expected = ['POSTGRES_RESULT', 'CONTRACT_RESULT', 'AUTH_INTEGRATION_RESULT', 'BROWSER_RESULT',
                    'NATIVE_STREAM_RESULT', 'NATIVE_STREAM_FIXED_RESULT', 'USER_ASSETS_FIXED_RESULT']
        command = text.split('        run: ', 1)[1].strip()
        env = {name: 'success' for name in expected}
        self.assertEqual(subprocess.run(['bash', '-c', command], env=env).returncode, 0)
        for name in expected:
            self.assertIn(f'"${name}" = success', command)
            for status in ['failure', 'cancelled', 'skipped', '']:
                self.assertNotEqual(subprocess.run(['bash', '-c', command], env=dict(env, **{name: status})).returncode, 0)


if __name__ == '__main__':
    unittest.main()
