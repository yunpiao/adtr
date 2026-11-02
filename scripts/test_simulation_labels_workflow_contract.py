"""Acceptance wiring tests only: never substitute for the dedicated real runtime."""
import ast
import io
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from test_simulation_labels_integration import ROLE, SEED, REQUIRED_LEDGER_SUBTESTS, run_go_integration
ROOT=Path(__file__).resolve().parents[1]

class LabelsAcceptanceContract(unittest.TestCase):
    def text(self,p): return (ROOT/p).read_text()
    def test_pinned_workflow_and_isolated_browser_inventory(self):
        w=self.text('.github/workflows/simulation-labels.yml')
        self.assertEqual(len(re.findall(r'uses: [^\n]+@[a-f0-9]{40}',w)),4)
        for token in ('permissions: {}','contents: read','persist-credentials: false','github.event.pull_request.head.sha || github.sha','test_simulation_labels_workflow_contract.py','test_simulation_labels_integration.py','simulation-labels.playwright.config.ts','simulation-labels-e2e/labels.spec.ts','retention-days: 7','if-no-files-found: error'):
            self.assertIn(token,w)
        c=self.text('web/simulation-labels.playwright.config.ts')
        self.assertIn('testDir: "./simulation-labels-e2e"',c)
        self.assertNotIn('simulation-labels',self.text('.github/workflows/ci.yml'))
    def test_trusted_fixture_and_no_fabricated_results(self):
        self.assertIn('simulationlabels.SeedFixtureTx',SEED)
        self.assertIn('tasks.CheckSchemaTx(ctx,tx,schemaversion.Current)',SEED)
        self.assertIn('[]string{"o1","o2","o3"}',SEED)
        self.assertIn('Complete:i<2',SEED)
        self.assertNotIn('INSERT INTO',SEED)
        self.assertNotIn('Evaluation{',SEED)
        self.assertNotIn('Comparison{',SEED)
        self.assertNotIn('Alert{',SEED)
    def test_owned_resources_and_closed_client_stages(self):
        h=self.text('scripts/test_simulation_labels_integration.py')
        self.assertRegex(ROLE,r'^[A-Za-z0-9_-]{24}$')
        for token in ('adtr.simulation.owner','actual != owner','ADTR_SIMULATION_LABELS_ENABLED="true"','INSERT INTO adtr.resource_tenant_config','"not-run"','capture_output=True','"127.0.0.1"','POSTGRES_PASSWORD'):
            self.assertIn(token,h)
        self.assertNotIn('other-owner',h)
        self.assertNotIn('worker_environment',h)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")',h)
        self.assertNotIn('UPDATE adtr.simulation_label',h)
    def test_actual_named_tests_exist(self):
        for file,name in [('internal/auth/simulation_labels_integration_test.go','TestSimulationLabelsHTTPAuthorityCASAndReceipts'),('internal/simulationlabels/integration_test.go','TestPostgresLabelsLedger'),('internal/store/simulation_labels_upgrade_integration_test.go','TestSchema28UpgradePreservesHistoryAndGrants'),('internal/store/simulation_labels_upgrade_integration_test.go','TestSchema28FailureRollsBackPermissionAndTables'),('internal/store/simulation_labels_upgrade_integration_test.go','TestSchema28ConcurrentUpgrade')]:
            self.assertIn('func '+name+'(t *testing.T)',self.text(file))
            self.assertIn(name,self.text('scripts/test_simulation_labels_integration.py'))
    def test_exact_oracles_and_live_revocation(self):
        h=self.text('scripts/test_simulation_labels_integration.py')
        for token in ('[("c", "added"), ("u", "removed")]','["distance"] == 2','["isHoneypot"] is None','["alerts"] == []','"incomplete"','"policy-not-asset"','"writeable", "readable"','peer.call(get("/operation"','stop(); start()'):
            self.assertIn(token,h)
    def test_live_revocation_respects_read_before_write_constraint(self):
        h=self.text("scripts/test_simulation_labels_integration.py")
        self.assertIn('revoked = "readable=false,writeable=false" if field == "readable" else "writeable=false"',h)
        self.assertIn("SET readable=true,writeable=true",h)
        self.assertNotIn("SET {field}=false",h)
    def test_missing_and_skipped_named_tests_fail(self):
        for events in ([],[{'Action':'skip','Test':'Required'}],[{'Action':'pass','Test':'Other'}],[{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/child'}]):
            with patch('test_simulation_labels_integration.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)))):
                with self.assertRaises(AssertionError):run_go_integration('./internal/auth','^Required$','Required',{})
        with patch('test_simulation_labels_integration.run',return_value=SimpleNamespace(stdout=json.dumps({'Action':'pass','Test':'Required'}))):
            run_go_integration('./internal/auth','^Required$','Required',{})
    def test_required_ledger_subtests_exist_and_each_must_pass(self):
        expected = ("Identifier128SeedPolicyDetailAnd129Rejection", "OversizedPayloadsLeaveNoWrites", "ScopedCompositeFKRejectsTampering", "ConcurrentCreateAtFixedPolicyQuota")
        self.assertEqual(REQUIRED_LEDGER_SUBTESTS, expected)
        source = self.text("internal/simulationlabels/integration_test.go")
        for child in expected:
            self.assertIn('t.Run("' + child + '", func(t *testing.T)', source)
        parent = "TestPostgresLabelsLedger"
        passed = [{"Action": "pass", "Test": parent}] + [{"Action": "pass", "Test": parent + "/" + child} for child in expected]
        for omitted in range(1, len(passed)):
            events = passed[:omitted] + passed[omitted + 1:]
            with patch("test_simulation_labels_integration.run", return_value=SimpleNamespace(stdout="\n".join(map(json.dumps, events)))):
                with self.assertRaises(AssertionError):
                    run_go_integration("./internal/simulationlabels", "^" + parent + "$", parent, {})
        for events in (passed + [{"Action": "skip", "Test": parent + "/extra"}], [{"Action": "pass", "Test": parent}]):
            with patch("test_simulation_labels_integration.run", return_value=SimpleNamespace(stdout="\n".join(map(json.dumps, events)))):
                with self.assertRaises(AssertionError):
                    run_go_integration("./internal/simulationlabels", "^" + parent + "$", parent, {})
        with patch("test_simulation_labels_integration.run", return_value=SimpleNamespace(stdout="\n".join(map(json.dumps, passed)))):
            run_go_integration("./internal/simulationlabels", "^" + parent + "$", parent, {})
    def test_closed_failure_diagnostics(self):
        output='null\n[]\n"private"\n'+json.dumps({'Output':'simulation_labels_integration_test.go:77: secret password SQLSTATE 42P01'})
        failure=subprocess.CalledProcessError(1,['go'],output=output,stderr='private credential')
        with patch('test_simulation_labels_integration.run',side_effect=failure),patch('sys.stdout',new_callable=io.StringIO) as printed:
            with self.assertRaises(RuntimeError):run_go_integration('./internal/auth','^Required$','Required',{})
        self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':1,'locations':['simulation_labels_integration_test.go:77'],'sqlStates':['42P01']})
    def test_browser_loss_is_committed_real_response_not_fake_success(self):
        s=self.text('web/simulation-labels-e2e/labels.spec.ts')
        for token in ('route.fetch()','route.abort("failed")','恢复原操作回执','page.reload()','toHaveCount(2)','蜜罐状态：不可用','比较并生成站内告警','toBeDisabled()'):
            self.assertIn(token,s)
        self.assertNotIn('route.fulfill',s)
        self.assertNotIn('test.skip',s)

if __name__=='__main__':unittest.main()
