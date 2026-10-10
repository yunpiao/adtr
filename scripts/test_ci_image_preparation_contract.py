"""Offline executable failure-path tests; do not claim Docker runtime proof."""
import copy
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

import prepare_ci_image as prepare


REFERENCE = ('public.ecr.aws/docker/library/postgres:17.6-alpine@sha256:'
             'ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94')
IMAGE = {'Id': 'sha256:d741b376874687de90374fd34f55c6b2760e8f7bd7e4ae5cd47f50757fc08cf8',
         'Os': 'linux', 'Architecture': 'amd64',
         'RepoDigests': ['public.ecr.aws/docker/library/postgres@sha256:'
                         'ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94']}


def response(code=0, stdout='', stderr=''):
    return subprocess.CompletedProcess([], code, stdout, stderr)


def found(image=IMAGE):
    return response(stdout=json.dumps([image]))


def missing():
    return response(1, stdout='[]\n', stderr=f'Error response from daemon: No such image: {REFERENCE}\n')


class Docker:
    def __init__(self, outcomes):
        self.outcomes = iter(outcomes)
        self.calls = []
        self.delays = []

    def run(self, command, **kwargs):
        self.calls.append((command, kwargs))
        result = next(self.outcomes)
        if isinstance(result, Exception):
            raise result
        return result

    def prepare(self):
        return prepare.prepare_image(self.run, self.delays.append)

    @property
    def pulls(self):
        return [command for command, _ in self.calls if command[1] == 'pull']


class ImagePreparationContract(unittest.TestCase):
    def test_cached_verified_image_never_pulls(self):
        docker = Docker([found()])
        self.assertEqual(docker.prepare(), IMAGE)
        self.assertEqual(docker.pulls, [])

    def test_pull_retains_exact_source_tag_digest_and_platform(self):
        docker = Docker([missing(), response(), found()])
        self.assertEqual(docker.prepare(), IMAGE)
        self.assertEqual(docker.pulls, [['docker', 'pull', '--platform=linux/amd64', REFERENCE]])
        self.assertEqual(docker.calls[1][1]['timeout'], 180)

    def test_transient_retry_inspects_after_failure_and_before_next_pull(self):
        docker = Docker([missing(), response(1, stderr='toomanyrequests: Rate exceeded'),
                         missing(), missing(), response(), found()])
        self.assertEqual(docker.prepare(), IMAGE)
        self.assertEqual(docker.delays, [10])
        self.assertEqual(len(docker.pulls), 2)
        self.assertEqual([c[1] for c, _ in docker.calls], ['image', 'pull', 'image', 'image', 'pull', 'image'])

    def test_completed_transient_operation_is_reused(self):
        for failure in [response(1, stderr='connection reset by peer'),
                        subprocess.TimeoutExpired(['docker', 'pull'], 180),
                        subprocess.TimeoutExpired(['docker', 'pull'], 180, output=b'Pulling fs layer')]:
            docker = Docker([missing(), failure, found()])
            self.assertEqual(docker.prepare(), IMAGE)
            self.assertEqual(len(docker.pulls), 1)
            self.assertEqual(docker.delays, [])

    def test_image_appearing_during_backoff_is_reused(self):
        docker = Docker([missing(), response(1, stderr='HTTP 429 Too Many Requests'), missing(), found()])
        self.assertEqual(docker.prepare(), IMAGE)
        self.assertEqual(len(docker.pulls), 1)
        self.assertEqual(docker.delays, [10])

    def test_transient_retry_budget_is_exactly_three_pulls(self):
        docker = Docker([outcome for _ in range(3) for outcome in
                         [missing(), response(1, stderr='toomanyrequests'), missing()]])
        with self.assertRaisesRegex(prepare.ImagePreparationError, 'exhausted'):
            docker.prepare()
        self.assertEqual(len(docker.pulls), 3)
        self.assertEqual(docker.delays, [10, 30])

    def test_unknown_or_security_errors_fail_without_retry(self):
        for message in ['', 'exit status 125', 'unauthorized', 'permission denied; HTTP 429',
                        'x509 certificate expired; toomanyrequests', 'digest mismatch',
                        'manifest unknown', 'requested access denied', 'HTTP 403 Forbidden']:
            with self.subTest(message=message):
                docker = Docker([missing(), response(125, stderr=message)])
                with self.assertRaisesRegex(prepare.ImagePreparationError, 'Non-retryable'):
                    docker.prepare()
                self.assertEqual(len(docker.calls), 2)
                self.assertEqual(docker.delays, [])

    def test_timeout_does_not_mask_permission_failure(self):
        docker = Docker([missing(), subprocess.TimeoutExpired(['docker', 'pull'], 180,
                                                              stderr=b'permission denied')])
        with self.assertRaisesRegex(prepare.ImagePreparationError, 'Non-retryable'):
            docker.prepare()
        self.assertEqual(docker.delays, [])

    def test_inspect_missing_is_narrow_and_access_errors_do_not_pull(self):
        for message in ['permission denied', 'Cannot connect to the Docker daemon', '',
                        f'No such image: {REFERENCE}\npermission denied']:
            docker = Docker([response(1, stderr=message)])
            with self.assertRaisesRegex(prepare.ImagePreparationError, 'inspect failed'):
                docker.prepare()
            self.assertEqual(docker.pulls, [])

    def test_missing_docker_or_inspect_timeout_fails_without_pull(self):
        for error in [FileNotFoundError('docker'), subprocess.TimeoutExpired(['docker'], 15)]:
            docker = Docker([error])
            with self.assertRaisesRegex(prepare.ImagePreparationError, 'inspect failed'):
                docker.prepare()
            self.assertEqual(docker.pulls, [])

    def test_source_policy_change_fails_before_any_docker_call(self):
        docker = Docker([])
        with patch.object(prepare, 'IMAGE', REFERENCE.replace('public.ecr.aws/', 'other.example/')):
            with self.assertRaisesRegex(prepare.ImagePreparationError, 'source/tag/digest changed'):
                docker.prepare()
        self.assertEqual(docker.calls, [])

    def test_success_without_inspectable_fixed_image_fails(self):
        docker = Docker([missing(), response(), missing()])
        with self.assertRaisesRegex(prepare.ImagePreparationError, 'reported pull success'):
            docker.prepare()
        self.assertEqual(len(docker.pulls), 1)

    def test_mismatched_identity_is_never_retried(self):
        mutations = [('Id', 'sha256:' + 'a' * 64), ('Os', 'windows'), ('Architecture', 'arm64'),
                     ('RepoDigests', []), ('RepoDigests', None),
                     ('RepoDigests', ['other.example/postgres@' + REFERENCE.split('@')[1]])]
        for field, value in mutations:
            with self.subTest(field=field, value=value):
                image = copy.deepcopy(IMAGE)
                image[field] = value
                for outcomes in [[found(image)], [missing(), response(), found(image)],
                                 [missing(), response(1, stderr='toomanyrequests'), found(image)]]:
                    docker = Docker(outcomes)
                    with self.assertRaisesRegex(prepare.ImagePreparationError, 'identity validation failed'):
                        docker.prepare()
                    self.assertEqual(docker.delays, [])

    def test_malformed_inspection_is_rejected(self):
        for value in ['', '{}', '[]', '[{}, {}]', '[null]']:
            docker = Docker([response(stdout=value)])
            with self.assertRaisesRegex(prepare.ImagePreparationError, 'identity validation failed'):
                docker.prepare()
            self.assertEqual(docker.pulls, [])

    def test_npm_cache_is_aligned_before_setup_node_without_skipping_install(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        makefile = Path('Makefile').read_text()
        self.assertIn('env:\n  npm_config_cache: /tmp/adtr-npm-cache\n', workflow.split('jobs:', 1)[0])
        self.assertIn('npm ci --prefix web --cache /tmp/adtr-npm-cache', makefile)
        self.assertIn('npm audit --prefix web --audit-level=high --cache /tmp/adtr-npm-cache', makefile)
        self.assertNotIn('cache-hit', workflow)
        self.assertEqual(workflow.count('cache-dependency-path: web/package-lock.json'), 5)


if __name__ == '__main__':
    unittest.main()
