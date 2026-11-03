"""Static tests only: these never claim PostgreSQL/browser acceptance."""
import copy,json,re,unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
from test_simulation_asset_assessment_integration import ROLE,SEED,NAMED_TESTS,expected_rows,validate_report,run_go_integration
ROOT=Path(__file__).resolve().parents[1]
class AssetAssessmentWorkflowContract(unittest.TestCase):
 def text(self,p):return (ROOT/p).read_text()
 def test_pinned_workflow_and_closed_artifacts(self):
  s=self.text('.github/workflows/simulation-asset-assessment.yml')
  self.assertEqual(len(re.findall(r'uses: [^\n]+@[a-f0-9]{40}',s)),4)
  for t in ('permissions: {}','contents: read','persist-credentials: false','github.event.pull_request.head.sha || github.sha','test_simulation_asset_assessment_workflow_contract.py','test_simulation_asset_assessment_integration.py','simulation-asset-assessment.playwright.config.ts','asset-assessment.spec.ts','retention-days: 7','if-no-files-found: error','SIMULATED-asset-assessment-'):self.assertIn(t,s)
  self.assertNotIn('simulation-asset-assessment',self.text('.github/workflows/ci.yml'))
 def test_exact_named_inventory(self):
  self.assertEqual(len(NAMED_TESTS),9)
  for package,name in NAMED_TESTS:
   source='\n'.join(p.read_text() for p in (ROOT/package).glob('*integration_test.go'));self.assertIn('func '+name+'(t *testing.T)',source)
  self.assertEqual(set(re.findall(r'func (TestPostgresAssetAssessment\w+)\(t \*testing.T\)',self.text('internal/simulationassetassessment/integration_test.go'))),{n for p,n in NAMED_TESTS if p=='./internal/simulationassetassessment'})
 def test_fixture_and_synchronous_no_worker(self):
  self.assertRegex(ROLE,r'^[a-zA-Z0-9_-]{24}$')
  self.assertIn('simulationassetassessment.SeedFixtureTx',SEED);self.assertIn('tasks.CheckSchemaTx(ctx,tx,schemaversion.Current)',SEED);self.assertNotIn('INSERT INTO',SEED)
  s=self.text('scripts/test_simulation_asset_assessment_integration.py')
  for t in ('stop();start()','validate_report(receipt["report"],"baseline")','"not-run"','actual != owner','127.0.0.1::5432','ADTR_SIMULATION_ASSET_ASSESSMENT_ENABLED="true"','client.call(path,want=403)'):self.assertIn(t,s)
  for t in ('start_worker','-mode", "worker','INSERT INTO adtr.tasks','UPDATE adtr.tasks'):self.assertNotIn(t,s)
 def test_boundaries_and_independent_oracle(self):
  baseline=expected_rows('baseline');boundary=expected_rows('freshness-boundary');stale=expected_rows('stale')
  self.assertEqual(len(baseline),11)
  self.assertEqual(baseline[2]['activity']['value'],'inactive');self.assertEqual(baseline[4]['activity']['value'],'recent');self.assertEqual(boundary[4]['activity']['value'],'inactive')
  self.assertEqual(stale[0]['accountStatus'],dict(value='unknown',reason='stale_observation'))
  self.assertEqual(stale[7]['accountStatus'],dict(value='not_applicable',reason='asset_kind'))
  self.assertEqual(baseline[3]['accountStatus']['value'],'unknown');self.assertEqual(baseline[1]['activity']['value'],'never_observed')
 def test_oracle_fails_missing_rows_and_modified_counts(self):
  # Build an offline specimen from independently authored expectations, not Evaluate.
  from collections import Counter
  for scenario in ('baseline','freshness-boundary','stale'):
   at={'baseline':'2026-11-01T12:00:00Z','freshness-boundary':'2026-11-02T12:00:00Z','stale':'2026-11-02T12:00:00.000000001Z'}[scenario]
   rows=[dict(r,version='simulated-asset-v1',source='synthetic',tenantId='default',domainId='simulation-domain-a',observationId='synthetic-assets-v1',observedAt='2026-11-01T12:00:00Z',asOf=at) for r in expected_rows(scenario)]
   summary=dict(total=11,complete=False,kinds=dict(Counter(r['kind'] for r in rows)),reasons=dict(Counter(r[f]['reason'] for r in rows for f in ('accountStatus','activity','groupType','groupScope'))))
   for f in ('accountStatus','activity','groupType','groupScope'):summary[f]=dict(Counter(r[f]['value'] for r in rows))
   report=dict(contract='adtr.simulation-asset-assessment.v1',simulation=True,scenarioId=scenario,version='simulated-asset-v1',asOf=at,tenantId='default',domainId='simulation-domain-a',rows=rows,summary=summary)
   validate_report(report,scenario)
   for mutate in (lambda r:r['rows'].pop(),lambda r:r['summary'].update(total=10),lambda r:r['rows'][0]['activity'].update(reason='invented'),lambda r:r['summary']['reasons'].update(asset_kind=21)):
    altered=copy.deepcopy(report);mutate(altered)
    with self.assertRaises(AssertionError):validate_report(altered,scenario)
 def test_browser_actual_delivery_loss_explicit_origin_and_geometry(self):
  s=self.text('web/simulation-asset-assessment-e2e/asset-assessment.spec.ts')
  for t in ('route.fetch()','route.abort("failed")','expect(posts).toBe(1)','page.reload()','toEqual(receipt)','profile.csrfToken','Origin:process.env.ADTR_E2E_BASE_URL!','boundingBox()','viewport!.height','SIMULATED-asset-assessment-populated-history.png','toHaveCount(11)','missing_evidence','stale_observation'):self.assertIn(t,s)
  for t in ('route.fulfill','test.skip','storageState','addStyleTag'):self.assertNotIn(t,s)
  self.assertEqual(s.count('page.request.post('),1)
 def test_missing_or_skipped_go_tests_rejected(self):
  for events in ([],[{'Action':'skip','Test':'Required'}],[{'Action':'pass','Test':'Other'}],[{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/child'}]):
   with patch('test_simulation_asset_assessment_integration.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)))):
    with self.assertRaises(AssertionError):run_go_integration('./internal/auth','^Required$','Required',{})
if __name__=='__main__':unittest.main()
