"""Static acceptance wiring only, never evidence of PostgreSQL/browser execution."""
import io
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from test_simulation_rule_catalog_integration import ROLE, REVIEW_ROLE, SEED, NAMED_TESTS, run_go_integration
from test_simulation_events_integration import Client
from test_simulation_detection_integration import worker_environment
ROOT = Path(__file__).resolve().parents[1]

class CatalogAcceptanceContract(unittest.TestCase):
    def text(self,path): return (ROOT/path).read_text()
    def test_pinned_exact_head_and_separate_inventory(self):
        workflow=self.text('.github/workflows/simulation-rule-catalog.yml')
        self.assertEqual(len(re.findall(r'uses: [^\n]+@[a-f0-9]{40}',workflow)),4)
        for token in ('permissions: {}','contents: read','persist-credentials: false','github.event.pull_request.head.sha || github.sha','test_simulation_rule_catalog_workflow_contract.py','test_simulation_rule_catalog_integration.py','simulation-rule-catalog.playwright.config.ts','simulation-rule-catalog-e2e/rule-catalog.spec.ts','retention-days: 7','if-no-files-found: error'):
            self.assertIn(token,workflow)
        self.assertNotIn('simulation-rule-catalog',self.text('.github/workflows/ci.yml'))
        config=self.text('web/simulation-rule-catalog.playwright.config.ts')
        for token in ('testDir: "./simulation-rule-catalog-e2e"','trace: "off"','video: "off"','screenshot: "off"'):
            self.assertIn(token,config)
    def test_fixture_and_real_worker_oracle(self):
        self.assertIn('simulationevents.SeedFixtureTx',SEED)
        self.assertIn('tasks.CheckSchemaTx(ctx,tx,schemaversion.Current)',SEED)
        self.assertIn('ID:"catalog-positive"',SEED)
        self.assertIn('ID:"catalog-negative"',SEED)
        self.assertNotIn('INSERT INTO',SEED)
        source=self.text('scripts/test_simulation_rule_catalog_integration.py')
        for token in ('wait_for_worker(worker,worker_base,report,lambda:client.call(task_path))','[("http-rule","catalog-positive")]','m["event_id"] == "catalog-negative"','["eventCount"] == 2','origin["publishedDigest"] != origin["templateHash"]','source["rule"]["version"] == 2','template["rules"][0]["version"] == 1','SELECT body FROM adtr.simulation_rule_catalog_origins'):
            self.assertIn(token,source)
        self.assertNotIn('UPDATE adtr.tasks',source)
        self.assertNotIn('INSERT INTO adtr.tasks',source)
    def test_identities_and_owned_resources(self):
        for role in (ROLE,REVIEW_ROLE): self.assertRegex(role,r'^[A-Za-z0-9_-]{24}$')
        self.assertNotEqual(ROLE,REVIEW_ROLE)
        for stage in ('anonymous','admin','operator','other-tenant','race-0'):
            Client('http://127.0.0.1:18080',stage)
        source=self.text('scripts/test_simulation_rule_catalog_integration.py')
        for token in ('adtr.simulation.owner','actual != owner','INSERT INTO adtr.resource_tenant_config','"not-run"','"127.0.0.1"','ADTR_SIMULATION_RULE_CATALOG_ENABLED="true"','mark=\'simulation_rule_catalog_publish\'','not grants["simulation_rule_catalog"]["writeable"]','("reviewer", "passed", "snapshot", "actorId")','("event.code", "object.kind")','templateId=rid),want=422)'):
            self.assertIn(token,source)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")',source)
    def test_worker_uses_different_explicit_loopback_port(self):
        env={'ADTR_LISTEN_ADDR':'127.0.0.1:1','ADTR_DATABASE_URL':'private','ADTR_BOOTSTRAP_PASSWORD':'secret','POSTGRES_PASSWORD':'secret'}
        worker,url=worker_environment(env)
        self.assertNotEqual(worker['ADTR_LISTEN_ADDR'],env['ADTR_LISTEN_ADDR'])
        self.assertRegex(worker['ADTR_LISTEN_ADDR'],r'^127\.0\.0\.1:[1-9][0-9]*$')
        self.assertEqual(url,'http://'+worker['ADTR_LISTEN_ADDR'])
        self.assertNotIn('ADTR_BOOTSTRAP_PASSWORD',worker)
        self.assertNotIn('POSTGRES_PASSWORD',worker)
    def test_exact_eight_named_tests_exist(self):
        self.assertEqual(len(NAMED_TESTS),8)
        for package,name in NAMED_TESTS:
            source='\n'.join(p.read_text() for p in (ROOT/package).glob('*integration_test.go'))
            self.assertIn('func '+name+'(t *testing.T)',source)
        self.assertEqual(sum(name.startswith('TestSchema29') for _,name in NAMED_TESTS),3)
        source=self.text('internal/simulationrulecatalog/integration_test.go')
        actual=set(re.findall(r'func (TestPostgresCatalog\w+)\(t \*testing.T\)',source))
        selected={name for package,name in NAMED_TESTS if package=='./internal/simulationrulecatalog'}
        self.assertEqual(actual,selected)
        self.assertEqual(len(selected),4)
    def test_missing_skipped_or_wrong_named_test_fails(self):
        for events in ([],[{'Action':'skip','Test':'Required'}],[{'Action':'pass','Test':'Other'}],[{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/child'}]):
            with patch('test_simulation_rule_catalog_integration.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)))):
                with self.assertRaises(AssertionError): run_go_integration('./internal/auth','^Required$','Required',{})
        with patch('test_simulation_rule_catalog_integration.run',return_value=SimpleNamespace(stdout=json.dumps({'Action':'pass','Test':'Required'}))):
            run_go_integration('./internal/auth','^Required$','Required',{})
    def test_closed_diagnostics(self):
        output='null\n[]\n"private"\n'+json.dumps({'Output':'simulation_rule_catalog_integration_test.go:77: password private SQLSTATE 42P01'})
        failure=subprocess.CalledProcessError(1,['go'],output=output,stderr='private credential')
        with patch('test_simulation_rule_catalog_integration.run',side_effect=failure),patch('sys.stdout',new_callable=io.StringIO) as printed:
            with self.assertRaises(RuntimeError): run_go_integration('./internal/auth','^Required$','Required',{})
        self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':1,'locations':['simulation_rule_catalog_integration_test.go:77'],'sqlStates':['42P01']})
    def test_browser_actual_bytes_dual_identity_and_nonempty_worker(self):
        source=self.text('web/simulation-rule-catalog-e2e/rule-catalog.spec.ts')
        for token in ('browser.newContext','reviewer.ID).not.toBe(author.ID)','route.fetch()','route.abort("failed")','createPosts).toBe(1)','readFile(file,"utf8")','[[true,true],[false,false]]','[["browser-rule","catalog-positive"]]','toHaveLength(0)','确认独立副本不级联','page.reload()','toEqual(exported)','"X-CSRF-Token":author.csrfToken'):
            self.assertIn(token,source)
        self.assertNotIn('route.fulfill',source)
        self.assertNotIn('test.skip',source)
        self.assertNotIn('storageState',source)

if __name__ == '__main__': unittest.main()
