"""Keep the recovery boundary inside directory-v2 verification only."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DirectoryV2VerificationContract(unittest.TestCase):
    def test_callsite_keeps_assertions_parse_and_framework_budget(self):
        text = (ROOT / "web/e2e/directory-v2.spec.ts").read_text()
        body = text.split("async function readJSON(", 1)[1].split("async function submit(", 1)[0]
        self.assertEqual(body.count("directoryV2VerificationGET(context.request, path)"), 1)
        self.assertNotIn("catch", body)
        self.assertIn('expect(response.status(), `GET ${path}`).toBe(200)', body)
        self.assertIn('response.headers()["x-adtr-user-id"]', body)
        self.assertIn("return response.json()", body)
        self.assertIn("test.setTimeout(630_000)", text)

    def test_no_new_timeout_or_native_retry_and_no_other_e2e_consumer(self):
        helper = (ROOT / "web/e2e/directory-v2-verification-get.ts").read_text()
        self.assertIn("request.get(path)", helper)
        for forbidden in ["setTimeout(", "setDefaultTimeout(", "maxRetries:", "_timeoutSettings", "Promise.race("]:
            self.assertNotIn(forbidden, helper)
        consumers = [p.name for p in (ROOT / "web/e2e").glob("*.ts")
                     if p.name != "directory-v2-verification-get.ts"
                     and 'from "./directory-v2-verification-get"' in p.read_text()]
        self.assertEqual(consumers, ["directory-v2.spec.ts"])
