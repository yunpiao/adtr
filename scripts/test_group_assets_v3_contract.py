"""Group fixture/harness invariants; not real database/browser acceptance."""
import unittest
import subprocess
import sys
from pathlib import Path
from ldap_e2e_fixture import LDAPFixture
from test_auth_e2e import GROUP_ASSETS_V3_SUITES, DIRECTORY_V3_SUITES, REAL_TAB_LIFECYCLE_SUITES
class GroupAssetsV3Contract(unittest.TestCase):
 def test_group_and_computer_modes_are_mutually_exclusive(self):
  with self.assertRaises(ValueError): LDAPFixture(directory_enabled=True,directory_v3=True,group_assets_v3=True,computer_assets_v3=True)
 def test_cli_includes_both_group_suites(self):
  result=subprocess.run([sys.executable,"scripts/test_auth_e2e.py","--help"],capture_output=True,text=True,check=True)
  for suite in GROUP_ASSETS_V3_SUITES:self.assertIn(suite,result.stdout)
 def test_modes_are_explicit(self):
  for kwargs in ({}, {"directory_enabled":True}, {"directory_enabled":True,"directory_v2":True}, {"directory_enabled":True,"directory_v3":True,"directory_empty":True}, {"directory_enabled":True,"directory_v3":True,"directory_slow":True}):
   with self.assertRaises(ValueError): LDAPFixture(group_assets_v3=True,**kwargs)
 def test_valid_mode_and_legacy_defaults(self):
  fixture=LDAPFixture(directory_enabled=True,directory_v3=True,group_assets_v3=True)
  try:
   self.assertTrue(fixture.group_assets_v3);self.assertFalse(fixture.user_assets_v2)
  finally: fixture.directory.cleanup()
  fixture=LDAPFixture(directory_enabled=True,directory_v3=True)
  try:self.assertFalse(fixture.group_assets_v3)
  finally:fixture.directory.cleanup()
 def test_real_harness_includes_both_required_suites(self):
  self.assertEqual(GROUP_ASSETS_V3_SUITES,{"group-assets-v3","group-assets-v3-readers"})
  self.assertTrue(GROUP_ASSETS_V3_SUITES<=DIRECTORY_V3_SUITES)
  self.assertIn("group-assets-v3-readers",REAL_TAB_LIFECYCLE_SUITES)
  workflow=Path('.github/workflows/ci.yml').read_text()
  for suite in GROUP_ASSETS_V3_SUITES:
   self.assertIn(f'--suite {suite}',workflow)
   self.assertIn(suite,workflow.split('suite: [',1)[1].split(']',1)[0])
if __name__=='__main__':unittest.main()
