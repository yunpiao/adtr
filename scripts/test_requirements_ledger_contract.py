"""Run the real ledger gate against independent copies of the checked-in data."""

from collections import Counter
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
CHECKER = ROOT / "scripts" / "verify_requirements.py"
CATALOG = ROOT / "requirements" / "catalog.json"
SUCCESS = (
    "Requirements: 209 unique AD-F records, 57 groups, 3258 fields, 804 controls, "
    "source rows and evidence boundaries verified\n"
)


class RequirementsLedgerContract(unittest.TestCase):
    def run_checker(self, mutate=None, *, optimized=False):
        with tempfile.TemporaryDirectory() as tmp:
            req = Path(tmp) / "requirements"
            req.mkdir()
            original = CATALOG.read_bytes()
            if mutate is None:
                (req / "catalog.json").write_bytes(original)
            else:
                ledger = json.loads(original)
                mutate(ledger)
                (req / "catalog.json").write_text(
                    json.dumps(ledger, ensure_ascii=False), encoding="utf-8"
                )
            args = [sys.executable, "-I"]
            if optimized:
                args.append("-O")
            return subprocess.run(
                [*args, str(CHECKER)], cwd=tmp, capture_output=True, text=True,
                encoding="utf-8", check=False, timeout=15,
            )

    def assert_rejected(self, mutate, message):
        for optimized in (False, True):
            with self.subTest(optimized=optimized):
                result = self.run_checker(mutate, optimized=optimized)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertIn(message, result.stderr)

    def test_actual_catalog_preserves_shared_ui_and_success_output(self):
        features = json.loads(CATALOG.read_bytes())["features"]
        ui = Counter(control for f in features for control in set(f["ui_records"]))
        self.assertTrue(any(owners > 1 for owners in ui.values()))
        for optimized in (False, True):
            with self.subTest(optimized=optimized):
                result = self.run_checker(optimized=optimized)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, SUCCESS)
                self.assertEqual(result.stderr, "")

    def test_duplicate_source_row_is_rejected(self):
        def mutate(ledger):
            first, second = ledger["features"][:2]
            second["source_row"] = first["source_row"]

        self.assert_rejected(mutate, "duplicate workbook source row")

    def test_fractional_source_row_is_rejected(self):
        def mutate(ledger):
            ledger["features"][0]["source_row"] += 0.5

        self.assert_rejected(mutate, "invalid workbook source row or sheet")

    def test_duplicate_field_ownership_is_rejected(self):
        def mutate(ledger):
            first, second = [
                f for f in ledger["features"] if f["field_records"]
            ][:2]
            second["field_records"].append(first["field_records"][0])

        self.assert_rejected(mutate, "duplicate field record")

    def test_repeated_field_on_same_feature_is_rejected(self):
        def mutate(ledger):
            fields = next(f for f in ledger["features"] if f["field_records"])["field_records"]
            fields.append(fields[0])

        self.assert_rejected(mutate, "duplicate field record")

    def test_missing_field_is_rejected(self):
        def mutate(ledger):
            next(f for f in ledger["features"] if f["field_records"])["field_records"].pop()

        self.assert_rejected(mutate, "field mapping reduced")

    def test_invalid_commit_evidence_is_rejected(self):
        def mutate(ledger):
            f = next(
                f for f in ledger["features"]
                if f["status"] == "implemented_verified_slice"
            )
            f["evidence"]["commit"] = "invalid"

        self.assert_rejected(mutate, "invalid evidence commit")

    def test_evidence_requires_repository_pr_and_actions_run(self):
        cases = (
            ("pr", True, "invalid evidence PR"),
            ("pr", "https://github.com/other/adtr/pull/69", "invalid evidence PR"),
            ("ci", "pending", "invalid evidence CI run"),
            ("ci", "https://github.com/yunpiao/adtr/actions/runs/0", "invalid evidence CI run"),
        )
        for field, value, message in cases:
            with self.subTest(field=field, value=value):
                def mutate(ledger):
                    feature = next(f for f in ledger["features"] if f["status"] == "implemented_verified_slice")
                    feature["evidence"][field] = value

                self.assert_rejected(mutate, message)

    def test_acceptance_requires_structured_evidence(self):
        cases = (
            ("verified", "done", "invalid verified evidence"),
            ("verified", [" "], "invalid verified evidence"),
            ("remaining", None, "invalid remaining gates"),
            ("remaining", {}, "invalid remaining gates"),
            ("remaining", ["Release not verified"], "unresolved acceptance gates"),
        )
        for field, value, message in cases:
            with self.subTest(field=field, value=value):
                def mutate(ledger):
                    feature = next(f for f in ledger["features"] if f["status"] == "implemented_verified_slice")
                    feature["status"] = "accepted"
                    feature["product_accepted"] = True
                    ledger["scope"]["product_accepted"] = 1
                    feature["evidence"]["remaining"] = []
                    feature["evidence"][field] = value

                self.assert_rejected(mutate, message)

    def test_acceptance_flags_and_count_require_exact_types(self):
        def numeric_flag(ledger):
            ledger["features"][0]["product_accepted"] = 0

        def boolean_count(ledger):
            ledger["scope"]["product_accepted"] = False

        self.assert_rejected(numeric_flag, "inconsistent acceptance status")
        self.assert_rejected(boolean_count, "inconsistent total product acceptance")
