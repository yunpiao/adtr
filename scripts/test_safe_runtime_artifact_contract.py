"""Fail-closed artifact schema, privacy and resource bounds; no browser/database."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

from e2e_runtime_diagnostics import MAX_BYTES, RuntimeDiagnostics, publish, validate_report


CANARY = "password=CANARY-secret Authorization: Bearer CANARY-token Cookie: session=CANARY-cookie postgres://CANARY-dsn?accountId=CANARY-id"


def event():
    return {"phase": "read-json", "method": "GET", "route": "/api/directory-credential-use/v2/effective",
            "elapsed_ms": 4, "at_ms": 200, "outcome": "transport-error", "status": None}


class SafeArtifactContract(unittest.TestCase):
    def report(self):
        diagnostics = RuntimeDiagnostics()
        diagnostics.phase("setup")
        diagnostics.phase("browser-start")
        diagnostics.snapshot(Mock(poll=Mock(return_value=-9)), None)
        diagnostics.report["browser"] = [event()]
        diagnostics.phase("after-cleanup")
        return diagnostics.report

    def test_records_precleanup_exit_separately_from_shutdown(self):
        report = self.report()
        report["processes"]["api"]["after_cleanup"] = 0
        decoded = json.loads(validate_report(report))
        self.assertEqual(decoded["processes"]["api"]["before_cleanup"], -9)
        self.assertEqual(decoded["processes"]["api"]["after_cleanup"], 0)
        self.assertFalse(decoded["processes"]["worker"]["started"])
        self.assertIsNone(decoded["http_panic"])

    def test_every_text_field_and_unknown_key_rejects_canary(self):
        report = self.report()
        for path in [("unexpected",), ("http_panic",), ("schema",), ("browser_dropped",), ("browser_capture",), ("collection_errors",),
                     ("phases", 0, "phase"), ("phases", 0, "extra"),
                     ("processes", "api", "started"), ("processes", "api", "before_cleanup"),
                     ("processes", "api", "extra")]:
            candidate = copy.deepcopy(report)
            value = candidate
            for key in path[:-1]:
                value = value[key]
            value[path[-1]] = CANARY
            with self.subTest(path=path), self.assertRaises((ValueError, TypeError)):
                validate_report(candidate)
        for field in list(event()) + ["headers", "body", "error", "cause", "call_log", "env"]:
            candidate = copy.deepcopy(report)
            candidate["browser"][0][field] = CANARY
            with self.subTest(field=field), self.assertRaises((ValueError, TypeError)):
                validate_report(candidate)

    def test_dropped_count_is_bounded_and_validated(self):
        report = self.report()
        report["browser_dropped"] = 12
        self.assertEqual(json.loads(validate_report(report))["browser_dropped"], 12)
        for value in [True, -1, 1000001, "CANARY-token", None]:
            report["browser_dropped"] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_report(report)

    def test_stale_artifact_is_removed_before_validation_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            source, output = Path(temporary) / "raw", Path(temporary) / "validated"
            source.mkdir()
            output.mkdir()
            (output / "diagnostics.json").write_text("old evidence")
            (source / "runtime.json").write_text("invalid")
            with self.assertRaises(ValueError):
                publish(source, output)
            self.assertFalse((output / "diagnostics.json").exists())

    def test_invalid_types_ranges_and_counts_reject(self):
        for field, value in [("elapsed_ms", True), ("elapsed_ms", -1), ("at_ms", 86400001),
                             ("status", 600), ("status", 0), ("status", True),
                             ("route", []), ("outcome", {}), ("phase", None)]:
            report = self.report()
            report["browser"][0][field] = value
            with self.subTest(field=field, value=value), self.assertRaises((ValueError, TypeError)):
                validate_report(report)
        report = self.report()
        report["browser"] *= 257
        with self.assertRaises(ValueError):
            validate_report(report)
        report = self.report()
        report["phases"] *= 10
        with self.assertRaises(ValueError):
            validate_report(report)

    def test_publish_reencodes_valid_report_without_copying_adjacent_logs(self):
        with tempfile.TemporaryDirectory() as temporary:
            source, output = Path(temporary) / "raw", Path(temporary) / "validated"
            source.mkdir()
            (source / "runtime.json").write_bytes(validate_report(self.report()))
            (source / "browser.json").write_text(json.dumps({"events": [event()], "dropped": 0}))
            (source / "api.log").write_text(CANARY)
            (source / "unknown.json").write_text(CANARY)
            publish(source, output)
            self.assertEqual([p.name for p in output.iterdir()], ["diagnostics.json"])
            artifact = (output / "diagnostics.json").read_bytes()
            self.assertNotIn(b"CANARY", artifact)
            self.assertLessEqual(len(artifact), MAX_BYTES)
            self.assertEqual(json.loads(artifact)["browser"], [event()])

    def test_malformed_oversized_or_secret_input_never_publishes(self):
        for filename, raw in [("runtime.json", b"{"), ("runtime.json", b" " * (MAX_BYTES + 1)),
                              ("browser.json", b"{"), ("browser.json", json.dumps({"events": [event()], "dropped": 0, "unknown": CANARY}).encode()), ("browser.json", b" " * (MAX_BYTES + 1)),
                              ("browser.json", json.dumps({"events": [dict(event(), route=CANARY)], "dropped": 0}).encode()),
                              ("browser.json", json.dumps({"events": [event()] * 257, "dropped": 0}).encode())]:
            with self.subTest(filename=filename, bytes=len(raw)), tempfile.TemporaryDirectory() as temporary:
                source, output = Path(temporary) / "raw", Path(temporary) / "validated"
                source.mkdir()
                (source / "runtime.json").write_bytes(validate_report(self.report()))
                (source / filename).write_bytes(raw)
                with self.assertRaises((ValueError, TypeError)):
                    publish(source, output)
                self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
