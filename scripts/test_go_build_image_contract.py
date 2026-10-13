"""Offline synthetic identity checks; never proof of a real registry pull."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import verify_go_build_image as image


def raw(value):
    return json.dumps(value, separators=(",", ":")).encode()


def manifest_bytes(value):
    data = raw(value)
    assert len(data) < 1921
    return data + b" " * (1920 - len(data)) + b"\n"


class GoBuildImageContract(unittest.TestCase):
    def setUp(self):
        self.manifest = {"schemaVersion": 2, "mediaType": image.MANIFEST_TYPE,
                         "config": {"mediaType": image.CONFIG_TYPE, "digest": image.CONFIG, "size": 2187},
                         "layers": [{"mediaType": image.LAYER_TYPE,
                                     "digest": "sha256:" + str(i) * 64,
                                     "size": 75175305 if i == 0 else 1} for i in range(5)]}
        self.manifest_raw = manifest_bytes(self.manifest)
        self.child = image.digest(self.manifest_raw)
        self.index = {"schemaVersion": 2, "mediaType": image.INDEX_TYPE,
                      "manifests": [{"mediaType": image.MANIFEST_TYPE, "digest": self.child,
                                     "size": 1921, "platform": {"os": "linux", "architecture": "amd64"}}]}
        self.index_raw = raw(self.index) + b"\n"
        self.index_digest = image.digest(self.index_raw)
        self.inspect = [{"Id": image.CONFIG, "Os": "linux", "Architecture": "amd64",
                         "RepoDigests": [image.REPOSITORY + "@" + self.index_digest]}]
        self.commands = []

    def fake_run(self, command, **kwargs):
        self.commands.append(command)
        self.assertNotIn("text", kwargs)
        self.assertTrue(kwargs["capture_output"])
        self.assertFalse(kwargs["check"])
        self.assertIn(kwargs["timeout"], (15, 60, 180))
        outputs = [self.index_raw, self.manifest_raw, b"native pull complete\n", raw(self.inspect)]
        return subprocess.CompletedProcess(command, 0, outputs[len(self.commands) - 1], b"")

    def constants(self):
        return patch.multiple(image, INDEX=self.index_digest, MANIFEST=self.child)

    def test_fixed_source_and_identity_literals(self):
        self.assertEqual(image.REFERENCE, "docker.io/library/golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414")
        self.assertEqual(image.MANIFEST, "sha256:cd9a32216aee5667f957a62d13a10032a63fd58e14b3f3d9cc8c2122f501e95e")
        self.assertEqual(image.CONFIG, "sha256:340a7d186a598086366ea5264e8c6e74e4dee36dc2d70ac0f621b6b28930a786")
        dockerfile = (Path(__file__).resolve().parents[1] / "Dockerfile").read_text()
        self.assertEqual(dockerfile.splitlines()[0], "FROM " + image.REFERENCE + " AS build")

    def test_workflow_verifies_before_expensive_gates_and_retains_failures(self):
        workflow = Path('.github/workflows/ci.yml').read_text()
        contracts = workflow.split('  contracts:\n', 1)[1].split('  auth-integration:\n', 1)[0]
        verify = contracts.index('      - name: Verify fixed official Go build image identity\n')
        retain = contracts.index('      - name: Retain Go build image verification evidence\n')
        build = contracts.index('      - name: Lint, race tests, type checking and build\n')
        lifecycle = contracts.index('      - name: API and Worker lifecycle\n')
        self.assertLess(verify, retain)
        self.assertLess(retain, build)
        self.assertLess(build, lifecycle)
        step = contracts[verify:retain]
        self.assertIn('        id: go_image\n', step)
        self.assertNotIn('        if:', step)
        self.assertNotIn('continue-on-error', contracts)
        upload = contracts[retain:build]
        self.assertIn("if: ${{ always() && (steps.go_image.outcome == 'success' || steps.go_image.outcome == 'failure') }}", upload)
        self.assertIn('actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02', upload)
        self.assertIn('name: go-build-image-${{ github.run_id }}-${{ github.run_attempt }}', upload)
        self.assertIn('path: ${{ runner.temp }}/adtr-go-image', upload)
        self.assertIn('if-no-files-found: error', upload)
        self.assertIn('retention-days: 7', upload)
        # Execute the exact shell wrapper with a synthetic verifier result. A
        # nonzero status must both survive and leave uploadable safe evidence.
        command = '\n'.join(line[10:] for line in step.split('        run: |\n', 1)[1].splitlines())
        for status in (0, 7):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                fake = root / 'python3'
                fake.write_text(f'#!/bin/sh\nprintf "synthetic safe result\\n"\nexit {status}\n')
                fake.chmod(0o700)
                env = dict(os.environ, RUNNER_TEMP=directory, PATH=directory + os.pathsep + os.environ['PATH'])
                result = subprocess.run(['bash', '-e', '-o', 'pipefail', '-c', command], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, status)
                evidence = root / 'adtr-go-image'
                self.assertEqual((evidence / 'exit-code.txt').read_text(), f'{status}\n')
                self.assertEqual((evidence / 'verification.log').read_text(), 'synthetic safe result\n')

    def test_exact_commands_and_unchanged_binary_evidence(self):
        with self.constants():
            index, manifest, report = image.verify(self.fake_run)
        self.assertEqual(index, self.index_raw)
        self.assertEqual(manifest, self.manifest_raw)
        self.assertFalse(report["independent_layer_byte_audit"])
        self.assertEqual(self.commands, [
            ["docker", "buildx", "imagetools", "inspect", "--raw", image.REFERENCE],
            ["docker", "buildx", "imagetools", "inspect", "--raw", image.REPOSITORY + "@" + self.child],
            ["docker", "pull", "--platform=linux/amd64", image.REFERENCE],
            ["docker", "image", "inspect", image.REFERENCE]])

    def test_docker_normalized_repository_digest(self):
        self.inspect[0]["RepoDigests"] = ["golang@" + self.index_digest]
        with self.constants():
            image.verify(self.fake_run)

    def test_raw_byte_mutations_fail(self):
        with self.constants():
            for changed in [self.index_raw.strip(), self.index_raw + b" ", self.index_raw.decode()]:
                with self.subTest(kind=type(changed)), self.assertRaises(image.ImageVerificationError):
                    image.validate_index(changed)
            for changed in [self.manifest_raw.strip(), self.manifest_raw[:-1] + b" "]:
                with self.assertRaises(image.ImageVerificationError):
                    image.validate_manifest(changed)

    def test_index_semantics_fail_closed(self):
        mutations = [lambda d: d.update(schemaVersion=1), lambda d: d.update(mediaType="wrong"),
                     lambda d: d.update(manifests=None),
                     lambda d: d["manifests"].append(copy.deepcopy(d["manifests"][0])),
                     lambda d: d["manifests"][0].update(platform={"os": "linux", "architecture": "arm64"}),
                     lambda d: d["manifests"][0].update(size=True),
                     lambda d: d["manifests"][0].update(digest="sha256:" + "f" * 64)]
        for mutate in mutations:
            data = copy.deepcopy(self.index)
            mutate(data)
            content = raw(data)
            with patch.multiple(image, INDEX=image.digest(content), MANIFEST=self.child):
                with self.assertRaises(image.ImageVerificationError):
                    image.validate_index(content)

    def test_manifest_semantics_fail_closed(self):
        mutations = [lambda d: d.update(schemaVersion=1), lambda d: d.update(config=None),
                     lambda d: d["config"].update(digest="sha256:" + "f" * 64),
                     lambda d: d["config"].update(size=2188),
                     lambda d: d.update(layers=[]), lambda d: d["layers"][0].update(size=-1),
                     lambda d: d["layers"][0].update(size=True),
                     lambda d: d["layers"][0].update(mediaType="wrong"),
                     lambda d: d["layers"][0].update(digest="bad"),
                     lambda d: d["layers"][0].update(size=1)]
        for mutate in mutations:
            data = copy.deepcopy(self.manifest)
            mutate(data)
            content = manifest_bytes(data)
            with patch.object(image, "MANIFEST", image.digest(content)):
                with self.assertRaises(image.ImageVerificationError):
                    image.validate_manifest(content)

    def test_loaded_identity_failures(self):
        for key, value in [("Id", "wrong"), ("Os", "windows"), ("Architecture", "arm64"),
                           ("RepoDigests", []), ("RepoDigests", ["untrusted.invalid/golang@" + self.index_digest])]:
            with self.subTest(key=key):
                self.commands = []
                data = copy.deepcopy(self.inspect)
                data[0][key] = value
                with self.constants(), patch.object(self, "inspect", data), self.assertRaises(image.ImageVerificationError):
                    image.verify(self.fake_run)

    def test_command_errors_do_not_retry_fallback_or_leak(self):
        for stderr in [b"429 toomanyrequests secret", b"denied secret", b"x509 secret", b"unknown secret"]:
            calls = []
            def fail(command, **kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(command, 1, b"", stderr)
            with self.assertRaises(image.ImageVerificationError) as failure:
                image.verify(fail)
            self.assertNotIn("secret", str(failure.exception))
            self.assertEqual(len(calls), 1)
        for error in [OSError("secret"), subprocess.TimeoutExpired("secret", 60)]:
            with self.assertRaises(image.ImageVerificationError) as failure:
                image.verify(lambda *a, **kw: (_ for _ in ()).throw(error))
            self.assertNotIn("secret", str(failure.exception))


if __name__ == "__main__":
    unittest.main()
