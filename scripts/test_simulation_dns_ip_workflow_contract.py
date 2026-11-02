"""Static wiring/oracle checks, never PostgreSQL or browser acceptance."""
import io
import json
from pathlib import Path
import re
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile
from xml.sax.saxutils import escape
from test_simulation_dns_ip_integration import ROLE,SEED,NAMED_TESTS,HEADERS,expected_records,validate_workbook,run_go_integration
ROOT=Path(__file__).resolve().parents[1]
class DNSIPAcceptanceContract(unittest.TestCase):
 def text(self,p):return (ROOT/p).read_text()
 def test_exact_pinned_workflow(self):
  s=self.text('.github/workflows/simulation-dns-ip.yml')
  self.assertEqual(len(re.findall(r'uses: [^\n]+@[a-f0-9]{40}',s)),4)
  for t in ('permissions: {}','contents: read','persist-credentials: false','github.event.pull_request.head.sha || github.sha','test_simulation_dns_ip_workflow_contract.py','test_simulation_dns_ip_integration.py','simulation-dns-ip.playwright.config.ts','simulation-dns-ip-e2e/dns-ip.spec.ts','retention-days: 7','if-no-files-found: error','name: SIMULATED-dns-ip-${{ github.event.pull_request.head.sha || github.sha }}'):self.assertIn(t,s)
  self.assertNotIn('simulation-dns-ip',self.text('.github/workflows/ci.yml'))
  self.assertNotIn('SIMULATED-rule-catalog',s)
 def test_exact_inventory_matches_final_source(self):
  self.assertEqual(len(NAMED_TESTS),9)
  for package,name in NAMED_TESTS:
   s='\n'.join(p.read_text() for p in (ROOT/package).glob('*integration_test.go'));self.assertIn('func '+name+'(t *testing.T)',s)
  s=self.text('internal/simulationdnsip/integration_test.go')
  self.assertEqual(set(re.findall(r'func (TestPostgresDNSIP\w+)\(t \*testing.T\)',s)),{n for p,n in NAMED_TESTS if p=='./internal/simulationdnsip'})
  self.assertEqual(sum(n.startswith('TestSchema31') for _,n in NAMED_TESTS),3)
 def test_request_task_id_matches_route_contract(self):
  routes=self.text('internal/auth/simulation_dns_ip_http.go')
  self.assertIn('"domainId taskId"',routes)
  self.assertIn('"domainId taskId operationId"',routes)
  for path in ('scripts/test_simulation_dns_ip_integration.py','web/simulation-dns-ip-e2e/dns-ip.spec.ts'):
   s=self.text(path);self.assertNotIn('taskUUID=',s);self.assertIn('taskId=',s)
 def test_trusted_mixed_fixture_and_owner(self):
  self.assertRegex(ROLE,r'^[a-zA-Z0-9_-]{24}$')
  for t in ('simulationdnsip.SeedFixtureTx','tasks.CheckSchemaTx(ctx,tx,schemaversion.Current)','i<=35','::ffff:192.0.2.99','2001:db8::1','SIMULATION.EXAMPLE.TEST.'):self.assertIn(t,SEED)
  self.assertNotIn('INSERT INTO',SEED)
  rows=expected_records();self.assertEqual(len(rows),38);self.assertEqual(rows[0]['id'],'row-001');self.assertEqual(rows[1]['id'],'multi');self.assertEqual(rows[-1]['id'],'v6')
 def test_actual_oracle_worker_and_download_wiring(self):
  s=self.text('scripts/test_simulation_dns_ip_integration.py')
  for t in ('combined==expected','validate_workbook(data,expected)','validate_workbook(download(client','stop_worker();stop();start();start_worker()','download(client,file_path)==data','client.call(path,want=404);download(client,file_path,want=404)','adtr.simulation.owner','actual != owner','ADTR_SIMULATION_DNS_IP_ENABLED="true"','"not-run"','INSERT INTO adtr.resource_tenant_config'):self.assertIn(t,s)
  self.assertIn("p(mark) WHERE id='{ROLE}';",s)
  self.assertNotIn('UPDATE adtr.tasks',s);self.assertNotIn('INSERT INTO adtr.tasks',s)
 def test_oracle_rejects_cell_row_and_formula_tampering(self):
  def book(rows,formula=False):
   stream=io.BytesIO()
   with zipfile.ZipFile(stream,'w') as z:
    xml='<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>'
    for j,row in enumerate(rows,1):
     xml+=f'<row r="{j}">'+''.join(f'<c r="{chr(65+i)}{j}" t="inlineStr"><is><t>{escape(v)}</t></is>'+('<f>1+1</f>' if formula else '')+'</c>' for i,v in enumerate(row))+'</row>'
    z.writestr('xl/worksheets/sheet1.xml',xml+'</sheetData></worksheet>')
   return stream.getvalue()
  one=expected_records()[:1];cells=[one[0][k] if k!='simulation' else 'true' for k in ('id','domain','dnsName','ip','observation','source','version','simulation')]
  self.assertEqual(validate_workbook(book([HEADERS,cells]),one)['rows'],1)
  for rows,formula in (([HEADERS],False),([HEADERS,list(reversed(cells))],False),([HEADERS,cells,cells],False),([HEADERS,cells],True)):
   with self.assertRaises(AssertionError):validate_workbook(book(rows,formula),one)
 def test_browser_real_download_and_delivery_loss(self):
  s=self.text('web/simulation-dns-ip-e2e/dns-ip.spec.ts')
  for t in ('route.fetch()','route.abort("failed")','posts).toBe(1)','page.waitForEvent("download")','execFileSync("python3"','--validate-workbook','toEqual(expectedRows())','page.reload()','toEqual(bytes)','boundingBox()','viewport!.height'):self.assertIn(t,s)
  for t in ('route.fulfill','test.skip','storageState','addStyleTag'):self.assertNotIn(t,s)
 def test_missing_or_skipped_named_test_fails(self):
  for ev in ([],[{'Action':'skip','Test':'Required'}],[{'Action':'pass','Test':'Other'}],[{'Action':'pass','Test':'Required'},{'Action':'skip','Test':'Required/child'}]):
   with patch('test_simulation_dns_ip_integration.run',return_value=SimpleNamespace(stdout='\n'.join(map(json.dumps,ev)))):
    with self.assertRaises(AssertionError):run_go_integration('./internal/auth','^Required$','Required',{})
 def test_closed_diagnostics(self):
  output=json.dumps({'Output':'simulation_dns_ip_integration_test.go:77: private SQLSTATE 42P01'})
  failure=subprocess.CalledProcessError(1,['go'],output=output,stderr='private')
  with patch('test_simulation_dns_ip_integration.run',side_effect=failure),patch('sys.stdout',new_callable=io.StringIO) as printed:
   with self.assertRaises(RuntimeError):run_go_integration('./internal/auth','^Required$','Required',{})
  self.assertEqual(json.loads(printed.getvalue()),{'phase':'named-postgres-test','exitCode':1,'locations':['simulation_dns_ip_integration_test.go:77'],'sqlStates':['42P01']})
if __name__=='__main__':unittest.main()
