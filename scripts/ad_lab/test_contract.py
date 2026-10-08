import copy
import unittest
from datetime import datetime, timezone, timedelta
from contract import validate_plan, redact_report, CASES

class ContractTests(unittest.TestCase):
    def setUp(self):
        self.now = datetime(2026, 10, 8, tzinfo=timezone.utc)
        self.plan = dict(schema=1,lab_id="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",source_sha="b"*40,
            created_at=self.now.isoformat(),expires_at=(self.now+timedelta(hours=1)).isoformat(),
            network=dict(cidr="192.168.77.0/24",internet=False,production_routes=False),
            machines=[dict(name="dc01",address="192.168.77.10",role="domain-controller"),
                      dict(name="member01",address="192.168.77.20",role="domain-member")])
    def test_valid(self): self.assertEqual(validate_plan(self.plan,self.now),self.plan)
    def test_source_ref_denied(self):
        for ref in ["main","abc123","a"*39,"A"*40,"x"*40]:
            with self.subTest(ref=ref), self.assertRaises(ValueError):
                p=copy.deepcopy(self.plan);p["source_sha"]=ref;validate_plan(p,self.now)
    def test_network_denied(self):
        for key,value in [("internet",True),("production_routes",True),("cidr","10.0.0.0/8")]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                p=copy.deepcopy(self.plan);p["network"][key]=value;validate_plan(p,self.now)
    def test_ttl(self):
        for delta in [0,121,-1]:
            with self.subTest(delta=delta), self.assertRaises(ValueError):
                p=copy.deepcopy(self.plan);p["expires_at"]=(self.now+timedelta(minutes=delta)).isoformat();validate_plan(p,self.now)
    def test_extra_field_denied(self):
        self.plan["password"]="DO-NOT-COPY"
        with self.assertRaises(ValueError): validate_plan(self.plan,self.now)
    def test_raw_reports_denied(self):
        results={key:True for key in CASES};results["ldaps"]="raw credential or network details"
        with self.assertRaises(ValueError): redact_report(self.plan,results,True)
    def test_cleanup_failure_not_pass(self):
        self.assertFalse(redact_report(self.plan,{key:True for key in CASES},False)["passed"])
    def test_missing_not_pass(self):
        with self.assertRaises(ValueError): redact_report(self.plan,{},True)
    def test_wrong_boolean_types(self):
        for key in ["internet", "production_routes"]:
            p=copy.deepcopy(self.plan);p["network"][key]=0
            with self.assertRaises(ValueError): validate_plan(p,self.now)
        self.plan["schema"]=True
        with self.assertRaises(ValueError): validate_plan(self.plan,self.now)
    def test_report_provenance(self):
        self.plan["source_sha"]="raw secret"
        with self.assertRaises(ValueError): redact_report(self.plan,{key:True for key in CASES},True)
    def test_result(self):
        report=redact_report(self.plan,{key:True for key in CASES},True)
        self.assertTrue(report["passed"]);self.assertFalse(report["full_product_acceptance"])
        self.assertNotIn("network",report)
if __name__ == '__main__': unittest.main()
