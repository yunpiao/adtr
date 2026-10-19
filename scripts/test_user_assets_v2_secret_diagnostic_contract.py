"""Static wiring guard; behavioral TypeScript tests run only in CI."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class UserAssetSecretDiagnosticContract(unittest.TestCase):
    def test_original_credentials_and_failure_predicate_remain(self):
        source = (ROOT / "web/e2e/user-assets-v2-readers.spec.ts").read_text()
        initial = source.split("const secrets: CredentialProbe[] = [", 1)[1].split("];", 1)[0]
        self.assertEqual(re.findall(r'category: "([a-z_]+)", value: (\w+)', initial), [
            ("password", "initialPassword"), ("password", "password"),
            ("account_name", "accountUser"), ("password", "accountPassword"),
            ("password", "readerInitial"), ("password", "readerPassword"),
            ("password", "producerInitial"), ("password", "producerPassword"),
        ])
        safe = source.split("const safe = (value: unknown) => {", 1)[1].split(
            "  const consoles:", 1)[0]
        self.assertEqual(safe.count("JSON.stringify(value)"), 1)
        self.assertIn("const matched = serialized.includes(secret);", safe)
        self.assertIn("expect(\n        matched,\n        matched\n          ?", safe)
        self.assertEqual(safe.count(").toBe(false);"), 2)
        self.assertIn('category: "mfa_seed", value: auth.secret', safe)
        self.assertIn('category: "totp",', safe)
        self.assertIn("...auth.codes.map", safe)
        self.assertIn('secrets.push({ category: "csrf", value: readerSession.csrfToken });', source)
        self.assertIn('/"(?:actorPassword|totpCode|ciphertext|userInfo)"\\s*:/u.test(serialized)', safe)

    def test_suite_passwords_are_generated_once_at_runtime(self):
        source = (ROOT / "web/e2e/user-assets-v2-readers.spec.ts").read_text()
        self.assertIn('import { createHmac, randomBytes } from "node:crypto";', source)
        names = ["password", "readerInitial", "readerPassword", "producerInitial", "producerPassword"]
        for name in names:
            declarations = re.findall(r"(?:let|const) " + name + r" = ([^;]+);", source)
            self.assertEqual(declarations, ['randomBytes(24).toString("base64url")'])
        self.assertEqual(source.count('randomBytes(24).toString("base64url")'), 5)
        self.assertNotRegex(source, r'(?:let|const) \w*(?:[Pp]assword|Initial) = [\"\']')
        policy = (ROOT / "internal/auth/crypto.go").read_text()
        self.assertIn("n >= 12 && n <= 64 && len(s) <= 256", policy)
        # 24 random bytes encode to 32 ASCII characters, inside the actual policy.
        self.assertEqual((24 * 8 + 5) // 6, 32)
        self.assertIn("password = producerPassword;", source)

    def test_output_type_is_exact_and_has_no_external_sink(self):
        helper = (ROOT / "web/e2e/user-assets-v2-secret-diagnostic.ts").read_text()
        schema = helper.split("type Diagnostic = {", 1)[1].split("};", 1)[0]
        self.assertEqual(re.findall(r"(\w+):", schema), [
            "credentialCategory", "locationCategory", "exactValueMatch", "embeddedMatch",
        ])
        for forbidden in ["console.", "writeFile", "createHash", "fetch(", "process.env", "throw error"]:
            self.assertNotIn(forbidden, helper)
        self.assertIn('throw new Error("Invalid credential diagnostic category")', helper)
        self.assertIn("JSON.parse(serialized)", helper)

    def test_behavioral_tests_are_in_existing_vitest_discovery(self):
        config = (ROOT / "web/vite.config.ts").read_text()
        self.assertIn('include: ["src/**/*.test.{ts,tsx}"]', config)
        test = (ROOT / "web/src/user-assets-v2-secret-diagnostic.test.ts").read_text()
        self.assertIn('from "../e2e/user-assets-v2-secret-diagnostic"', test)
        for category in ["password", "account_name", "mfa_seed", "totp", "csrf"]:
            self.assertIn(f'"{category}"', test)
        self.assertIn("for (const embedded of [false, true])", test)
        self.assertIn(".toThrow();", test)


if __name__ == "__main__":
    unittest.main()
