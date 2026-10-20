from ci_workflow_contract import reviewed_workflow
"""Offline generation and static wiring checks; no browser/runtime acceptance."""
import contextlib
import hashlib
import io
import os
from pathlib import Path
import re
import unittest

from directory_e2e_credentials import (
    DRAFT_CODE_NAME, PASSWORD_NAMES, MEMBERSHIP_PASSWORD_NAMES,
    SUITE_PASSWORD_NAMES, configure_directory_credentials,
)


RUNNER_ADDITIONS = (
    "from directory_e2e_credentials import configure_directory_credentials\n",
    "        configure_directory_credentials(env, args.suite)\n",
)


def restore_credentials_runner(text):
    anchors = (
        RUNNER_ADDITIONS[0] + "from e2e_owned_container import OWNER_LABEL, remove_owned_container\n",
        '        env["ADTR_E2E_PASSWORD"] = env["ADTR_BOOTSTRAP_PASSWORD"]\n'
        + RUNNER_ADDITIONS[1] + '        if not Path("web/dist/index.html").is_file():\n',
    )
    if any(text.count(anchor) != 1 for anchor in anchors):
        raise ValueError("directory credential wiring moved or altered")
    for addition in RUNNER_ADDITIONS:
        if text.count(addition) != 1:
            raise ValueError("directory credential wiring absent, altered or duplicated")
        text = text.replace(addition, "")
    return text


class DirectoryCredentialsContract(unittest.TestCase):
    def test_suite_isolation_and_membership_generation(self):
        self.assertEqual(set(SUITE_PASSWORD_NAMES), {"directory-v3", "group-members-v4"})
        for suite, names in SUITE_PASSWORD_NAMES.items():
            env = {}
            output = io.StringIO()
            before = dict(os.environ)
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                configure_directory_credentials(env, suite)
            expected = set(names) | ({DRAFT_CODE_NAME} if suite == "directory-v3" else set())
            self.assertEqual(set(env), expected)
            first = dict(env)
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
                configure_directory_credentials(env, suite)
            self.assertTrue(all(first[n] != env[n] for n in names))
            self.assertTrue(all(re.fullmatch(r"[A-Za-z0-9_-]{32}", env[n]) for n in names))
            self.assertTrue(len({env[n] for n in names}) == len(names))
            self.assertTrue(dict(os.environ) == before)
            self.assertFalse(output.getvalue())

    def test_membership_required_inputs_and_leak_guards(self):
        code = Path("web/e2e/group-members-v4.spec.ts").read_text()
        guard = code.split("function requireFixture() {", 1)[1].split("function totp(", 1)[0]
        self.assertIn("names.some((name) => !process.env[name])", guard)
        self.assertIn("throw new Error(", guard)
        for name in MEMBERSHIP_PASSWORD_NAMES:
            self.assertIn(f'"{name}"', guard)
            self.assertIn(f"process.env.{name}!", code)
            self.assertNotRegex(code, rf"process\.env\.{name}\s*(?:\?\?|\|\|)")
        secret_list = code.split("const secrets = [", 1)[1].split("];", 1)[0]
        for variable in ("password", "readerInitial", "readerPassword", "producerInitial", "producerPassword"):
            self.assertNotRegex(code, rf"(?:let|const) {variable}\s*=\s*[\"'`]")
            self.assertIn(variable, secret_list)
        self.assertIn("test.beforeAll(() => requireFixture())", code)
        self.assertIn('test.use({ trace: "off", screenshot: "off", video: "off" })', code)
        self.assertIn("safe(consoles)", code)
        self.assertIn("command: python3 scripts/test_auth_e2e.py --suite group-members-v4\n",
                      reviewed_workflow())

    def test_mock_proof_inputs_and_replay_coverage(self):
        code = Path("web/src/Tasks.test.tsx").read_text()
        self.assertIn('import { randomBytes, randomInt } from "node:crypto";', code)
        self.assertIn('mockPassword = randomBytes(24).toString("base64url")', code)
        self.assertIn("const code = randomInt(1_000_000)", code)
        self.assertIn('replayCode = ((code + 1) % 1_000_000).toString().padStart(6, "0")', code)
        self.assertIn("const proof = (code = mockCode)", code)
        self.assertIn("proof(replayCode)", code)
        self.assertIn("expect(bodies[0].totpCode).toBe(mockCode)", code)
        self.assertIn("expect(bodies[1].totpCode).toBe(replayCode)", code)
        self.assertIn("expect(replayCode).not.toBe(mockCode)", code)
        self.assertNotRegex(code, r'(?:actorPassword|totpCode):\s*["\']')
        self.assertIn('vi.stubGlobal("fetch", fetcher)', code)
        self.assertIn(').not.toContain("actorPassword")', code)

    def test_exact_prior_runner_bytes_and_reject_drift(self):
        from test_owned_rescue_runner_contract import restore_owned_runner
        source = restore_owned_runner(Path("scripts/test_auth_e2e.py").read_text())
        restored = restore_credentials_runner(source)
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(),
                         "21b22d642bd3518deaf3a3659e29113d90682de063dd504ab68340a8173b69e5")
        for addition in RUNNER_ADDITIONS:
            for changed in (source + addition, source.replace(addition, ""),
                            source.replace(addition, "") + addition,
                            source.replace(addition, addition.replace("credentials", "credential"))):
                with self.assertRaises(ValueError):
                    restore_credentials_runner(changed)

    def test_fresh_policy_compliant_passwords(self):
        first, second = {}, {}
        configure_directory_credentials(first, "directory-v3")
        configure_directory_credentials(second, "directory-v3")
        values = [env[name] for env in (first, second) for name in PASSWORD_NAMES]
        self.assertTrue(all(re.fullmatch(r"[A-Za-z0-9_-]{32}", v) for v in values))
        self.assertTrue(len(set(values)) == len(values), "inputs must be independent")
        self.assertTrue(all(re.fullmatch(r"[0-9]{6}", env[DRAFT_CODE_NAME])
                            for env in (first, second)))

    def test_inherited_values_replaced_without_output(self):
        sentinel = object()
        env = dict.fromkeys((*PASSWORD_NAMES, DRAFT_CODE_NAME), sentinel)
        env["unrelated"] = sentinel
        before = dict(os.environ)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            configure_directory_credentials(env, "directory-v3")
        self.assertTrue(all(env[name] is not sentinel
                            for name in (*PASSWORD_NAMES, DRAFT_CODE_NAME)))
        self.assertIs(env["unrelated"], sentinel)
        self.assertTrue(dict(os.environ) == before, "global environment changed")
        self.assertFalse(output.getvalue(), "credential setup emitted output")

    def test_other_suites_unchanged(self):
        for suite in ("auth", "directory-v3-controls", "group-members-v4-empty",
                      "group-members-v4-pagination", "group-members-v4-refresh"):
            env = {"untouched": object()}
            before = dict(env)
            configure_directory_credentials(env, suite)
            self.assertEqual(env, before)

    def test_required_environment_and_no_literal_fallback(self):
        code = Path("web/e2e/directory-v3.spec.ts").read_text()
        guard = code.split("function requireFixture() {", 1)[1].split("function totp(", 1)[0]
        self.assertIn("names.some((name) => !process.env[name])", guard)
        self.assertIn("throw new Error(", guard)
        for name in (*PASSWORD_NAMES, DRAFT_CODE_NAME):
            self.assertIn(f'"{name}"', guard)
            self.assertIn(f"process.env.{name}!", code)
            self.assertNotRegex(code, rf"process\.env\.{name}\s*(?:\?\?|\|\|)")
        for variable in ("password", "producerInitial", "producerPassword", "draftPassword", "draftCode"):
            self.assertNotRegex(code, rf"(?:let|const) {variable}\s*=\s*[\"'`]")
        self.assertIn("test.beforeAll(() => requireFixture())", code)

    def test_runner_and_ci_wiring(self):
        runner = Path("scripts/test_auth_e2e.py").read_text()
        setup = runner.index("configure_directory_credentials(env, args.suite)")
        self.assertLess(setup, runner.index("subprocess.run(command, env=env"))
        workflow = reviewed_workflow()
        self.assertIn("command: python3 scripts/test_auth_e2e.py --suite directory-v3", workflow)
        self.assertIn("make check", workflow)
        self.assertIn("python3 -m unittest discover -s scripts -p '*_contract.py'",
                      Path("Makefile").read_text())

    def test_artifact_and_draft_guards_preserved(self):
        code = Path("web/e2e/directory-v3.spec.ts").read_text()
        self.assertIn('test.use({ trace: "off", screenshot: "off", video: "off" })', code)
        self.assertIn('"Unable to enter synthetic authentication proof"', code)
        self.assertIn("expect(governancePosts).toBe(0)", code)
        self.assertIn("safe(storage)", code)
        self.assertIn("safe(consoles)", code)
        secret_list = code.split("const secrets = [", 1)[1].split("];", 1)[0]
        for name in ("password", "producerInitial", "producerPassword", "draftPassword", "draftCode"):
            self.assertIn(name, secret_list)
        runner = Path("scripts/test_auth_e2e.py").read_text()
        self.assertIn('env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"', runner)


if __name__ == "__main__":
    unittest.main()
