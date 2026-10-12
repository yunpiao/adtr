"""Independent request/step schema and fail-closed CI upload contracts."""
import copy
import json
from pathlib import Path
import unittest
from e2e_runtime_diagnostics import RuntimeDiagnostics, validate_report

CANARY = "CANARY-secret-token-cookie-url-body"


def event():
    return dict(origin="api-request", phase="fixture-get", operation=1, attempt=1,
                method="GET", route="/api/auth/me", outcome="start", status=None,
                error_class="none", at_ms=0, elapsed_ms=0, snapshot=None)


def snapshot():
    return dict(view="v3", picker="visible", source="radio-present", alert=False,
                permission_denied=False, page_closed=False, unavailable=False)


def step():
    return dict(event(), origin="step", phase="source-select", attempt=0,
                method="none", route="unknown", snapshot=snapshot())


def validate(value):
    report = RuntimeDiagnostics().report
    report["browser"] = [value]
    return json.loads(validate_report(report))["browser"][0]


class SafeRequestContract(unittest.TestCase):
    def test_known_statuses_attempts_routes_and_page_header_failure(self):
        for status in [200, 401, 403, 429, 500, 599]:
            for attempt in [1, 2]:
                value = dict(event(), status=status, outcome="response", attempt=attempt)
                self.assertEqual(validate(value), value)
        for route in ["/api/domain-selection", "/api/domain-selection/resolve", "/api/auth/me", "/api/access/menu", "/api/access/check", "/api/user-assets/v2", "/api/user-assets/v3"]:
            for status in [None, 200, 500]:
                value = dict(event(), origin="page-request", phase="page-request", attempt=0,
                             method="POST", route=route, status=status, outcome="transport-error", error_class="page_request_failed")
                self.assertEqual(validate(value), value)

    def test_each_error_is_constant_and_steps_only_carry_semantic_state(self):
        for error in ["econnreset", "socket_hang_up", "response_aborted", "context_disposed", "target_closed", "connection_refused", "timeout", "unknown_error", "non_error"]:
            self.assertEqual(validate(dict(event(), outcome="transport-error", error_class=error))["error_class"], error)
        for phase in ["test-start", "test-finish", "administrator-login", "version-switch", "source-select"]:
            for outcome in ["start", "complete", "failed"]:
                self.assertEqual(validate(dict(step(), phase=phase, outcome=outcome))["phase"], phase)
        for source in ["loading", "empty", "radio-present", "absent"]:
            self.assertEqual(validate(dict(step(), snapshot=dict(snapshot(), source=source)))["snapshot"]["source"], source)
        closed = dict(snapshot(), view="unavailable", picker="unavailable", source="unavailable", unavailable=True, page_closed=True)
        self.assertTrue(validate(dict(step(), snapshot=closed))["snapshot"]["page_closed"])

    def test_canary_in_every_new_field_and_unknown_key_fails_closed(self):
        for original in [event(), step()]:
            for key in list(original) + ["headers", "body", "error", "env", "query"]:
                with self.subTest(key=key), self.assertRaises((ValueError, TypeError)):
                    validate(dict(original, **{key: CANARY}))
        for key in list(snapshot()) + ["text", "html", "input", "source_name"]:
            with self.subTest(key=key), self.assertRaises((ValueError, TypeError)):
                validate(dict(step(), snapshot=dict(snapshot(), **{key: CANARY})))

    def test_invalid_number_enum_types_and_relations_fail_closed(self):
        for key in ["operation", "attempt", "at_ms", "elapsed_ms", "status"]:
            for value in [True, -1, 100000000, 1.5, [], {}, CANARY]:
                with self.subTest(key=key, value=value), self.assertRaises((ValueError, TypeError)):
                    validate(dict(event(), **{key: value}))
        invalid = [dict(phase="directory-get", attempt=2), dict(outcome="response"),
                   dict(outcome="transport-error"), dict(outcome="start", status=500),
                   dict(origin="page-request", phase="page-request", attempt=1),
                   dict(origin="api-request", phase="test-start"), dict(method="none"),
                   dict(route="/api/auth/me?token=" + CANARY), dict(operation=0),
                   dict(error_class="unknown"), dict(snapshot=snapshot())]
        for changes in invalid:
            with self.subTest(changes=changes), self.assertRaises((ValueError, TypeError)):
                validate(dict(event(), **changes))
        for changes in [dict(status=200), dict(error_class="socket_hang_up"), dict(method="GET"), dict(attempt=1), dict(outcome="response")]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                validate(dict(step(), **changes))
        for changes in [dict(page_closed=True), dict(unavailable=True), dict(source="unavailable"), dict(alert=1)]:
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                validate(dict(step(), snapshot=dict(snapshot(), **changes)))

    def test_worst_case_events_fail_at_size_boundary_and_cannot_leak(self):
        report = RuntimeDiagnostics().report
        value = dict(step(), phase="administrator-login", operation=1000000, at_ms=86400000, elapsed_ms=86400000)
        report["browser"] = [copy.deepcopy(value) for _ in range(256)]
        with self.assertRaises(ValueError):
            validate_report(report)
        report["browser"] = report["browser"][:100]
        self.assertLessEqual(len(validate_report(report)), 65536)
        self.assertNotIn(CANARY.encode(), validate_report(report))

    def test_grant_submit_fixed_phases_and_page_lifecycle_are_strict(self):
        base = dict(event(), origin="step", phase="grant-wait", attempt=0,
                    route="/api/credential-use/grant", method="none", outcome="registered")
        for phase in ["grant-wait", "grant-click"]:
            for outcome in ["start", "complete", "failed"]:
                value = dict(base, phase=phase, outcome=outcome)
                self.assertEqual(validate(value), value)
        self.assertEqual(validate(base), base)
        for field in list(base) + ["body", "error", "dom", "value"]:
            with self.subTest(field=field), self.assertRaises((ValueError, TypeError)):
                validate(dict(base, **{field: CANARY}))
        for changes in [dict(phase="grant-click"), dict(origin="page-request"),
                        dict(method="POST"), dict(attempt=1), dict(route="/api/auth/me"),
                        dict(status=200), dict(error_class="timeout"), dict(snapshot={}),
                        dict(operation=0), dict(elapsed_ms=float("nan")), dict(at_ms=True)]:
            with self.subTest(changes=changes), self.assertRaises((ValueError, TypeError)):
                validate(dict(base, **changes))
        for status in [200, 401, 403, 429, 500]:
            page = dict(event(), origin="page-request", phase="page-request", attempt=0,
                        route="/api/credential-use/grant", method="POST", outcome="response", status=status)
            self.assertEqual(validate(page), page)
        with self.assertRaises(ValueError):
            validate(dict(event(), outcome="registered"))

    def test_baseline_and_fixed_failure_uploads_require_successful_validation(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/ci.yml").read_text()
        for job, end, name in [("browser:", "native-no-store:", "safe-runtime-${{ matrix.name }}"),
                               ("user-assets-fixed:", "verify:", "safe-runtime-fixed-${{ matrix.suite }}")]:
            section = workflow.split("\n  " + job, 1)[1].split("\n  " + end, 1)[0]
            validate_step = section.index("- name: Validate safe failure diagnostics")
            upload_step = section.index("- name: Retain validated safe failure diagnostics")
            self.assertLess(validate_step, upload_step)
            self.assertIn("id: safe-diagnostics\n        if: ${{ failure() }}", section[validate_step:upload_step])
            self.assertIn("python3 scripts/e2e_runtime_diagnostics.py .e2e-diagnostics .e2e-validated", section[validate_step:upload_step])
            upload = section[upload_step:].split("\n      - name:", 1)[0]
            self.assertIn("if: ${{ failure() && steps.safe-diagnostics.outcome == 'success' }}", upload)
            self.assertIn("path: .e2e-validated/diagnostics.json\n", upload)
            self.assertIn("include-hidden-files: true", upload)
            self.assertIn(name + "-${{ github.run_id }}-${{ github.run_attempt }}", upload)
            self.assertNotIn("*", upload)
            self.assertNotIn(".e2e-diagnostics", upload)
            self.assertIn("retention-days: 7", upload)


if __name__ == "__main__":
    unittest.main()
