"""Static wiring, not evidence of PostgreSQL/browser execution."""
import io
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from test_simulation_detection_exceptions_integration import ROLE, SEED, NAMED_TESTS, run_go_integration
from test_simulation_events_integration import Client
from test_simulation_detection_integration import worker_environment
ROOT=Path(__file__).resolve().parents[1]

class ExceptionAcceptanceContract(unittest.TestCase):
    def text(self,path): return (ROOT/path).read_text()
    def test_pinned_exact_head_and_separate_inventory(self):
        workflow=self.text('.github/workflows/simulation-detection-exceptions.yml')
        self.assertEqual(len(re.findall(r'uses: [^\n]+@[a-f0-9]{40}',workflow)),4)
        for token in ('permissions: {}','contents: read','persist-credentials: false','github.event.pull_request.head.sha || github.sha','test_simulation_detection_exceptions_workflow_contract.py','test_simulation_detection_exceptions_integration.py','simulation-detection-exceptions.playwright.config.ts','simulation-detection-exceptions-e2e/exceptions.spec.ts','retention-days: 7','if-no-files-found: error'):
            self.assertIn(token,workflow)
        self.assertIn('name: SIMULATED-detection-exceptions-${{ github.event.pull_request.head.sha || github.sha }}',workflow)
        self.assertNotIn('SIMULATED-rule-catalog',workflow)
        self.assertNotIn('simulation-detection-exceptions',self.text('.github/workflows/ci.yml'))
        config=self.text('web/simulation-detection-exceptions.playwright.config.ts')
        for token in ('testDir: "./simulation-detection-exceptions-e2e"','trace: "off"','video: "off"','screenshot: "off"'):
            self.assertIn(token,config)
    def test_trusted_fixture_and_real_nonempty_worker(self):
        for token in ('simulationevents.SeedFixtureTx','tasks.CheckSchemaTx(ctx,tx,schemaversion.Current)','ID:"exception-positive"','ID:"exception-negative"'):
            self.assertIn(token,SEED)
        self.assertNotIn('INSERT INTO',SEED)
        source=self.text('scripts/test_simulation_detection_exceptions_integration.py')
        for token in ('wait_for_worker(worker,worker_base,report,lambda:client.call(task_path))','[("http-rule","exception-positive")]','m["event_id"] == "exception-negative"','["eventCount"] == 2','value["source"] == expected','[m["match"] for m in value["matches"]] == expected["matches"]','http-digest-mismatch','http-global-enabled','http-import-disabled'):
            self.assertIn(token,source)
        self.assertNotIn('UPDATE adtr.tasks',source)
        self.assertNotIn('INSERT INTO adtr.tasks',source)
    def test_owned_resources_and_source_permissions(self):
        self.assertRegex(ROLE,r'^[A-Za-z0-9_-]{24}$')
        for stage in ('anonymous','admin','operator','other-tenant'): Client('http://127.0.0.1:18080',stage)
        source=self.text('scripts/test_simulation_detection_exceptions_integration.py')
        for token in ('adtr.simulation.owner','actual != owner','INSERT INTO adtr.resource_tenant_config','"not-run"','"127.0.0.1"','ADTR_SIMULATION_DETECTION_EXCEPTIONS_ENABLED="true"',"('simulation_events')",'("matches","passed","ruleDigest","actorId")','readable=false,writeable=false','get("/evaluation",id="http-enabled"),want=404'):
            self.assertIn(token,source)
        self.assertNotIn('os.environ.get("ADTR_DATABASE_URL")',source)
    def test_worker_distinct_loopback_port(self):
        worker,url=worker_environment({'ADTR_LISTEN_ADDR':'127.0.0.1:1','ADTR_DATABASE_URL':'private','ADTR_BOOTSTRAP_PASSWORD':'secret','POSTGRES_PASSWORD':'secret'})
        self.assertNotEqual(worker['ADTR_LISTEN_ADDR'],'127.0.0.1:1')
        self.assertRegex(worker['ADTR_LISTEN_ADDR'],r'^127\.0\.0\.1:[1-9][0-9]*$')
        self.assertEqual(url,'http://'+worker['ADTR_LISTEN_ADDR'])
        self.assertNotIn('ADTR_BOOTSTRAP_PASSWORD',worker)
        self.assertNotIn('POSTGRES_PASSWORD',worker)
    def test_exact_eight_named_tests_exist(self):
        self.assertEqual(len(NAMED_TESTS),8)
        for package,name in NAMED_TESTS:
            source='\n'.join(p.read_text() for p in (ROOT/package).glob('*integration_test.go'))
            self.assertIn('func '+name+'(t *testing.T)',source)
        self.assertEqual(sum(name.startswith('TestSchema30') for _,name in NAMED_TESTS),3)
        actual=set(re.findall(r'func (TestPostgresExceptions\w+)\(t \*testing.T\)',self.text('internal/simulationdetectionexceptions/integration_test.go')))
        selected={name for package,name in NAMED_TESTS if package=='./internal/simulationdetectionexceptions'}
        self.assertEqual(actual,selected)
        self.assertEqual(len(selected),4)
    def test_missing_skipped_or_wrong_name_fails(self):
        for events in ([],[{'Action':'skip','Test':'Required'}],[{'Action':'pass','Test':'Other'}],[{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/child'}]):
            with patch('test_simulation_detection_exceptions_integration.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)))):
                with self.assertRaises(AssertionError): run_go_integration('./internal/auth','^Required$','Required',{})
        with patch('test_simulation_detection_exceptions_integration.run',return_value=SimpleNamespace(stdout=json.dumps({'Action':'pass','Test':'Required'}))):
            run_go_integration('./internal/auth','^Required$','Required',{})
    def test_closed_diagnostics(self):
        output='null\n[]\n"private"\n'+json.dumps({'Output':'simulation_detection_exceptions_integration_test.go:77: password private SQLSTATE 42P01'})
        failure=subprocess.CalledProcessError(1,['go'],output=output,stderr='private credential')
        with patch('test_simulation_detection_exceptions_integration.run',side_effect=failure),patch('sys.stdout',new_callable=io.StringIO) as printed:
            with self.assertRaises(RuntimeError): run_go_integration('./internal/auth','^Required$','Required',{})
        self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':1,'locations':['simulation_detection_exceptions_integration_test.go:77'],'sqlStates':['42P01']})
    def test_browser_source_posts_preserve_origin_and_csrf(self):
        source=self.text('web/simulation-detection-exceptions-e2e/exceptions.spec.ts')
        self.assertIn('const origin = new URL(page.url()).origin;',source)
        self.assertIn('page.request.post(path,{data:body,headers:{Origin:origin,"X-CSRF-Token":actor.csrfToken}})',source)
        # No production authorization fallback: both source mutations use this helper.
        self.assertEqual(source.count('page.request.post('),1)
        self.assertIn('post(detection+"/create",',source)
        self.assertIn('post(detection+"/run",',source)
    def test_browser_real_download_replay_history_and_geometry(self):
        source=self.text('web/simulation-detection-exceptions-e2e/exceptions.spec.ts')
        for token in ('route.fetch()','route.abort("failed")','createPosts).toBe(1)','readFile(file!,"utf8")','[["browser-rule","exception-positive"]]','toHaveLength(0)','page.reload()','toEqual(source)','"X-CSRF-Token":actor.csrfToken','receipt.intent).toEqual','header.y+header.height','page.viewportSize()!.height','从真实命中预览禁用声明','选择声明 JSON 文件'):
            self.assertIn(token,source)
        for token in ('route.fulfill','test.skip','storageState','addStyleTag'):
            self.assertNotIn(token,source)

if __name__=='__main__': unittest.main()
