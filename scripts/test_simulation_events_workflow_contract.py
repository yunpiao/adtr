"""The standalone simulation suite is explicit coverage, not a matrix exemption."""
import ast
from pathlib import Path
import re
import subprocess
import unittest
import io
from email.message import Message
import json
from unittest.mock import patch

from test_simulation_events_integration import Client, safe_response_code, safe_preparation_code, prepare_database, ROLE, FIXTURE_PREFLIGHT_SQL, SAFE_ERROR_CODES

ROOT = Path(__file__).resolve().parents[1]


class SimulationEventsWorkflowContract(unittest.TestCase):
    def text(self, path):
        return (ROOT / path).read_text()

    def test_exact_head_and_minimal_permissions(self):
        workflow = self.text('.github/workflows/simulation-events.yml')
        self.assertIn('permissions: {}', workflow)
        self.assertEqual(re.findall(r'^      (\w+): (read|write)$', workflow, re.M), [('contents', 'read')])
        self.assertIn('ref: ${{ github.event.pull_request.head.sha || github.sha }}', workflow)
        self.assertIn('persist-credentials: false', workflow)
        self.assertIn('  pull_request:', workflow)
        self.assertIn('  workflow_dispatch:', workflow)
        self.assertIn('  push:\n    branches: [main]', workflow)
        self.assertNotIn('secrets.', workflow)
        self.assertNotIn('paths-ignore:', workflow)
        actions = re.findall(r'uses: (\S+)', workflow)
        self.assertEqual(len(actions), 4)
        for action in actions:
            self.assertRegex(action, r'^actions/[\w-]+@[a-f0-9]{40}$')

    def test_workflow_runs_contract_and_real_harness(self):
        workflow = self.text('.github/workflows/simulation-events.yml')
        self.assertIn('python3 -m unittest discover -s scripts -p test_simulation_events_workflow_contract.py', workflow)
        self.assertIn('run: python3 scripts/test_simulation_events_integration.py', workflow)
        self.assertIn('simulation.playwright.config.ts simulation-e2e/simulation-events.spec.ts', workflow)
        self.assertIn('path: artifacts/simulation-events/', workflow)
        self.assertNotIn('continue-on-error:', workflow)

    def test_harness_selects_exact_standalone_spec(self):
        tree = ast.parse(self.text('scripts/test_simulation_events_integration.py'))
        commands = [node.value for node in ast.walk(tree) if isinstance(node, ast.Constant) and isinstance(node.value, str)]
        for expected in ('--config', 'simulation.playwright.config.ts', 'simulation-e2e/simulation-events.spec.ts',
                         '^TestPostgresAtomicSyntheticState$', '^TestSimulationEventHTTP'):
            self.assertIn(expected, commands)
        source = self.text('scripts/test_simulation_events_integration.py')
        self.assertIn('for phase in ("migrate-first", "migrate-repeat", "bootstrap"):', source)
        self.assertIn('report["status"] = "not-run"', source)
        self.assertIn('report["status"] = "passed"', source)
        self.assertLess(source.index('"simulation-e2e/simulation-events.spec.ts"'), source.index('report["status"] = "passed"'))

    def test_separate_directory_preserves_legacy_inventory(self):
        self.assertFalse((ROOT / 'web/e2e/simulation-events.spec.ts').exists())
        self.assertTrue((ROOT / 'web/simulation-e2e/simulation-events.spec.ts').is_file())
        config = self.text('web/simulation.playwright.config.ts')
        self.assertIn('import base from "./playwright.config"', config)
        self.assertIn('testDir: "./simulation-e2e"', config)
        self.assertIn('testMatch: "simulation-events.spec.ts"', config)
        for setting in ('trace: "off"', 'screenshot: "off"', 'video: "off"'):
            self.assertIn(setting, config)
        for path in ('scripts/ci_inventory.json', '.github/workflows/ci.yml', 'web/playwright.config.ts'):
            self.assertNotIn('simulation-events.spec.ts', self.text(path))


class SimulationClientDiagnosticsContract(unittest.TestCase):
    def response(self, status, payload):
        result = io.BytesIO(payload)
        result.status = status
        result.headers = Message()
        result.headers["Content-Type"] = "application/json"
        return result

    def test_login_wire_shape_and_closed_failure_code(self):
        client = Client("http://127.0.0.1:18080", "admin")
        password = "Synthetic secret must never be logged"
        with patch.object(client.opener, "open", return_value=self.response(400, b'{"error":"invalid_input"}')) as opened:
            with self.assertRaises(AssertionError) as caught:
                client.call("/api/auth/login", {"username": "simulation-admin", "password": password})
        request = opened.call_args.args[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(request.get_header("Content-type"), "application/json")
        self.assertEqual(request.get_header("Origin"), "http://127.0.0.1:18080")
        self.assertEqual(json.loads(request.data), {"username": "simulation-admin", "password": password})
        self.assertIn("stage=admin; code=invalid_input", str(caught.exception))
        self.assertIn("responseType=json; method=POST; requestType=json; proxyConfigured=", str(caught.exception))
        self.assertNotIn(password, str(caught.exception))
        self.assertNotIn("simulation-admin", str(caught.exception))

    def test_untrusted_diagnostics_are_never_echoed(self):
        for raw, expected in ((b'<html>secret</html>', "non-json-response"),
                              (b'{"error":"secret"}', "unrecognized-response"),
                              (b'{"error": ["secret"]}', "unrecognized-response"),
                              (b'{"error":"invalid_input","password":"secret"}', "invalid_input"),
                              (b'x' * 65537, "oversized-response")):
            self.assertEqual(safe_response_code(raw), expected)
        with self.assertRaises(ValueError):
            Client("http://127.0.0.1:18080", "secret")


class SimulationDatabasePreparationContract(unittest.TestCase):
    def test_readiness_requires_final_tcp_listener(self):
        source = (ROOT / "scripts/test_simulation_events_integration.py").read_text()
        tree = ast.parse(source)
        commands = [node for node in ast.walk(tree) if isinstance(node, ast.List)
                    and any(isinstance(x, ast.Constant) and x.value == "pg_isready" for x in node.elts)]
        self.assertEqual(len(commands), 1)
        literals = [x.value for x in commands[0].elts if isinstance(x, ast.Constant)]
        self.assertEqual(literals, ["docker", "exec", "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-U", "postgres", "-d", "adtr_e2e"])
        self.assertIn("for _ in range(100):", source)
        self.assertIn("time.sleep(.2)", source)

    def test_preparation_preserves_commands_and_never_retries(self):
        with patch("test_simulation_events_integration.run") as command:
            for phase in ("migrate-first", "migrate-repeat", "bootstrap"):
                prepare_database(phase, {})
        self.assertEqual([call.args[0] for call in command.call_args_list],
                         [["./bin/adtr", "-mode", mode] for mode in ("migrate", "migrate", "bootstrap")])
        error = subprocess.CalledProcessError(1, ["secret"], output=b"secret", stderr=b"migration connection failed\n")
        with patch("test_simulation_events_integration.run", side_effect=error) as command:
            with self.assertRaises(RuntimeError) as caught:
                prepare_database("migrate-first", {"SECRET": "secret"})
            command.assert_called_once()
        self.assertEqual(str(caught.exception), "database preparation failed: phase=migrate-first; exit=1; code=database-connect")
        self.assertTrue(caught.exception.__suppress_context__)

    def test_preparation_output_is_closed_and_redacted(self):
        self.assertEqual(safe_preparation_code(b"simulation events schema migration failed\n"), "simulation-schema")
        for value in (b"secret", b"migration connection failed\nsecret", b"secret" * 500,
                      "migration connection failed", None):
            self.assertEqual(safe_preparation_code(value), "unclassified")
        with patch("test_simulation_events_integration.run", side_effect=subprocess.TimeoutExpired("secret", 120, stderr=b"secret")):
            with self.assertRaisesRegex(RuntimeError, "phase=bootstrap; exit=none; code=timeout"):
                prepare_database("bootstrap", {})
        with self.assertRaises(ValueError):
            prepare_database("secret", {})


class SimulationFixtureIdentifierContract(unittest.TestCase):
    def test_custom_role_matches_production_validator(self):
        source = (ROOT / "internal/auth/access_types.go").read_text()
        validator = source.split("func validRoleID(id string) bool {", 1)[1].split("func validateGrants", 1)[0]
        # Keep this test-side ASCII parity rule tied to the actual production
        # validator. Builtin IDs are irrelevant: this fixture is a custom role.
        self.assertIn("if len(id) != 24 {", validator)
        self.assertIn("c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'", validator)
        valid_custom_role = lambda value: re.fullmatch(r"[a-zA-Z0-9_-]{24}", value) is not None
        self.assertTrue(valid_custom_role(ROLE))
        self.assertEqual(len(ROLE.encode("ascii")), 24)
        # Actual run41295807399 operator login failed because this old SQL
        # fixture bypassed create validation, then loadAccessGrants rejected it.
        old_role = "simulation_e2e_role_00001"
        self.assertEqual(len(old_role), 25)
        self.assertFalse(valid_custom_role(old_role))
        self.assertFalse(valid_custom_role("!" * 24))
        self.assertFalse(valid_custom_role("é" * 24))

    def test_other_fixture_ids_fit_resource_and_event_boundaries(self):
        # resource IDs allow 1..128 ASCII alphanumeric/-/_, plus dot; event
        # HTTP IDs use the stricter set below. Neither requires role's length24.
        ids = ("default", "other", "simulation-domain-a", "simulation-domain-b",
               "simulation-group", "simulation-dataset", "synthetic-event",
               "http-case", "browser-case", "http-operation", "read-only", "race-0", "race-1")
        for value in ids:
            with self.subTest(identifier=value):
                self.assertRegex(value, r"^[a-zA-Z0-9_-]{1,128}$")
        resource_validator = (ROOT / "internal/auth/resource_types.go").read_text()
        event_validator = (ROOT / "internal/auth/message_http.go").read_text()
        self.assertIn("if len(s) < 1 || len(s) > 128 {", resource_validator)
        self.assertIn("if len(s) == 0 || len(s) > 128 {", event_validator)


class SimulationFixturePrerequisitesContract(unittest.TestCase):
    def test_both_tenants_have_current_configuration_before_seed(self):
        source = (ROOT / "scripts/test_simulation_events_integration.py").read_text()
        self.assertIn("INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)", source)
        self.assertIn("SELECT tenant_id,2,extract(epoch FROM clock_timestamp()+interval '30 days')::bigint,uid,name", source)
        self.assertIn("('default','synthetic-default','Synthetic default tenant'),('other','synthetic-other','Synthetic other tenant')", source)
        self.assertIn('assert sql(FIXTURE_PREFLIGHT_SQL) == "t"', source)
        self.assertLess(source.index('assert sql(FIXTURE_PREFLIGHT_SQL)'), source.index('anonymous, admin, client, other ='))
        self.assertIn("tenant_not_configured", SAFE_ERROR_CODES)

    def test_preflight_matches_actual_authorization_prerequisites(self):
        actual = (ROOT / "internal/auth/resource_store.go").read_text()
        for guard in ('fail(409, "tenant_not_configured")', 'config.ExpireTime <= s.now().Unix()',
                      'count > config.MaxADCount', 'JOIN adtr.resource_group_members',
                      'JOIN adtr.resource_role_groups'):
            self.assertIn(guard, actual)
        for guard in ('c.expire_time>extract(epoch FROM clock_timestamp())+86400',
                      '<=c.max_ad_count', "bool_and(d.id='simulation-domain-a')",
                      'JOIN adtr.resource_group_members', 'JOIN adtr.resource_role_groups',
                      'NOT u.must_change AND NOT u.disabled', "interval '1 day'",
                      "p.mark='simulation_events' AND p.readable AND p.writeable",
                      "dp.mark='domains' AND dp.readable", "credential_mode='unconfigured'",
                      'connection_revision=1 AND credential_revision=1'):
            self.assertIn(guard, FIXTURE_PREFLIGHT_SQL)


if __name__ == '__main__':
    unittest.main()
