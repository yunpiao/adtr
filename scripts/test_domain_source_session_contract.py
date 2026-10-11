"""Keep the browser fixture inside the real custom-role input contract."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DomainSourceSessionFixtureContract(unittest.TestCase):
    def test_seeded_role_matches_browser_and_real_api_id_shape(self):
        runner = (ROOT / "scripts/test_auth_e2e.py").read_text()
        browser = (ROOT / "web/e2e/account-references.spec.ts").read_text()
        role = re.search(r"VALUES\('default','([^']+)','Source no domain scope'\)", runner).group(1)
        self.assertRegex(role, r"^[A-Za-z0-9_-]{24}$")
        self.assertIn(f'.fill("{role}")', browser)
        self.assertIn(f"('default','{role}','domains',true,true)", runner)
        self.assertIn(f"('default','{role}','operation_accounts',true,false)", runner)
        seed = runner[runner.index('if args.suite == "account-references":'):runner.index('if args.suite == "resource":')]
        self.assertNotIn("resource_group", seed)
        self.assertNotIn("UPDATE", seed)
        self.assertIn('"ON_ERROR_STOP=1"', seed)
        self.assertIn("BEGIN; INSERT INTO adtr.access_roles", seed)
        self.assertIn("COMMIT;", seed)


if __name__ == "__main__":
    unittest.main()
