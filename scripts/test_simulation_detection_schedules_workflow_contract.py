"""Offline wiring is not PostgreSQL/browser acceptance."""
import ast
import io
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch
from test_simulation_events_integration import Client
from test_simulation_detection_schedules_integration import REQUIRED_POSTGRES_TESTS, ROLE, run_go_integration
ROOT = Path(__file__).resolve().parents[1]
MODULE = 'test_simulation_detection_schedules_integration'
class ScheduledDetectionWorkflowContract(unittest.TestCase):
    def test_real_shared_clients_preserve_separate_peer_identity(self):
        source=(ROOT/'scripts/test_simulation_detection_schedules_integration.py').read_text()
        tree=ast.parse(source)
        main=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='main')
        assignment=next(n for n in ast.walk(main) if isinstance(n,ast.Assign)
            and isinstance(n.value,ast.ListComp) and isinstance(n.targets[0],ast.Tuple)
            and [v.id for v in n.targets[0].elts]==['anonymous','admin','client','other','peer'])
        stages=ast.literal_eval(assignment.value.generators[0].iter)
        # Real constructor validates the closed vocabulary, without HTTP calls.
        clients=dict(zip([v.id for v in assignment.targets[0].elts],
                         [Client('http://127.0.0.1:19000',stage) for stage in stages]))
        self.assertEqual([c.stage for c in clients.values()],
                         ['anonymous','admin','operator','other-tenant','operator'])
        self.assertIsNot(clients['peer'],clients['client'])
        self.assertIsNot(clients['peer'].opener,clients['client'].opener)
        login_loop=next(n for n in ast.walk(main) if isinstance(n,ast.For)
            and isinstance(n.target,ast.Tuple)
            and [v.id for v in n.target.elts]==['c','username'])
        identities={entry.elts[0].id:ast.literal_eval(entry.elts[1]) for entry in login_loop.iter.elts}
        self.assertEqual(identities,{'admin':'simulation-admin','client':'simulation-operator',
                                    'other':'simulation-other','peer':'simulation-peer'})
        self.assertIn("('default','simulation-peer')",source)
        for path in ('detail_path','receipt_path','task_path'):
            self.assertIn('peer.call('+path+', want=404)',source)

    def test_named_tests_exist_and_do_not_skip(self):
        self.assertEqual(len(REQUIRED_POSTGRES_TESTS),10)
        for package,name in REQUIRED_POSTGRES_TESTS:
            files=list((ROOT/package).glob('*integration_test.go'))
            self.assertTrue(any('func '+name+'(' in p.read_text() for p in files),name)
        for events, success in (([],False),([{'Action':'pass','Test':'Other'}],False),([{'Action':'skip','Test':'Required'}],False),([{'Action':'pass','Test':'Required'}],True),([{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/sub'}],False)):
            with patch(MODULE+'.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)))):
                if success: run_go_integration('./synthetic','^Required$','Required',{})
                else:
                    with self.assertRaises(AssertionError): run_go_integration('./synthetic','^Required$','Required',{})
    def test_closed_failure_diagnostic(self):
        output='null\n[]\n"private"\n'+json.dumps({'Output':'integration_test.go:77: secret password DSN SQLSTATE 42P01'})
        with patch(MODULE+'.run',side_effect=subprocess.CalledProcessError(1,['go'],output=output,stderr='secret')),patch('sys.stdout',new_callable=io.StringIO) as printed:
            with self.assertRaises(RuntimeError): run_go_integration('./synthetic','^Required$','Required',{})
        self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':1,'locations':['integration_test.go:77'],'sqlStates':['42P01']})
    def test_diagnostic_sources_are_finite_and_bounded(self):
        output='\n'.join(json.dumps({'Output': value}) for value in (
            'simulation_detection_schedules_integration_test.go:25: credential',
            'simulation_detection_schedule_upgrade_integration_test.go:32: credential',
            'operational_log_upgrade_integration_test.go:44: credential',
            '/private/integration_test.go:12: private',
            'arbitrary_secret_integration_test.go:20: secret',
            'integration_test.go:1234567: not bounded'))
        with patch(MODULE+'.run',side_effect=subprocess.CalledProcessError(2,['go'],output=output)),patch('sys.stdout',new_callable=io.StringIO) as printed:
            with self.assertRaises(RuntimeError): run_go_integration('./synthetic','^Required$','Required',{})
        self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':2,
            'locations':['operational_log_upgrade_integration_test.go:44','simulation_detection_schedule_upgrade_integration_test.go:32','simulation_detection_schedules_integration_test.go:25'],'sqlStates':[]})

    def test_isolated_real_producer_and_environment(self):
        self.assertRegex(ROLE,r'^[A-Za-z0-9_-]{24}$')
        s=(ROOT/'scripts/test_simulation_detection_schedules_integration.py').read_text()
        for v in ('actual != owner','report["status"] = "not-run"','worker_environment(env)','wait_for_worker(worker, worker_base, report','INSERT INTO adtr.resource_tenant_config','ADTR_SIMULATION_DETECTION_SCHEDULES_ENABLED="true"','historical-e2e-schedule','simulation_detection_schedule_intents','completed["task"]["state"] == "succeeded"','recovered["schedule"]["state"] == "enabled"'):
            self.assertIn(v,s)
        for v in ('UPDATE adtr.tasks','DISABLE TRIGGER','SET session_replication_role','os.environ.get("ADTR_DATABASE_URL")','UPDATE adtr.simulation_detection_schedule_revisions'):
            self.assertNotIn(v,s)
        self.assertLess(s.index('"simulation-detection-schedules-e2e/schedules.spec.ts"'),s.index('report["status"] = "passed"'))
    def test_container_identity_is_not_shadowed(self):
        tree=ast.parse((ROOT/'scripts/test_simulation_detection_schedules_integration.py').read_text())
        main=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='main')
        writes=[n for n in ast.walk(main) if isinstance(n,ast.Name) and isinstance(n.ctx,ast.Store) and n.id=='name']
        self.assertEqual(len(writes),1,'the owned Docker name must survive test inventory iteration')
        created=[n for n in ast.walk(main) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='created' for t in n.targets)]
        self.assertEqual(len(created),1)
        self.assertIsInstance(created[0].value,ast.Constant)
        self.assertIs(created[0].value.value,True)

    def test_exact_revision_and_safe_artifacts(self):
        w=(ROOT/'.github/workflows/simulation-detection-schedules.yml').read_text()
        for v in ('permissions: {}','run: go test -race ./...','contents: read','persist-credentials: false','ref: ${{ github.event.pull_request.head.sha || github.sha }}','python3 scripts/test_simulation_detection_schedules_integration.py','simulation-detection-schedules-e2e/schedules.spec.ts','if-no-files-found: error'):
            self.assertIn(v,w)
        for v in ('continue-on-error:','secrets.','paths-ignore:'): self.assertNotIn(v,w)
        self.assertEqual(len(re.findall(r'uses: actions/[\w-]+@[a-f0-9]{40}',w)),4)
        c=(ROOT/'web/simulation-detection-schedules.playwright.config.ts').read_text()
        for v in ('testDir: "./simulation-detection-schedules-e2e"','trace: "off"','video: "off"','screenshot: "off"'): self.assertIn(v,c)
        spec=(ROOT/'web/simulation-detection-schedules-e2e/schedules.spec.ts').read_text()
        for v in ('route.fetch()','route.abort("failed")','page.reload()','receipt.receipt.state','receipt.schedule.state','SIMULATED-schedules-worker-result.png','not.toHaveProperty("rate")'): self.assertIn(v,spec)
        for p in ('scripts/ci_inventory.json','.github/workflows/ci.yml','web/playwright.config.ts'):
            self.assertNotIn('schedules.spec.ts',(ROOT/p).read_text())
if __name__ == '__main__': unittest.main()
