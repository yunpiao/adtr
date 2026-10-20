from ci_workflow_contract import reviewed_workflow
"""Computer fixture/harness invariants; not real database/browser acceptance."""
import unittest
from pathlib import Path
from ldap_e2e_fixture import LDAPFixture
from test_auth_e2e import COMPUTER_ASSETS_V3_SUITES, DIRECTORY_V3_SUITES, REAL_TAB_LIFECYCLE_SUITES
class ComputerAssetsV3Contract(unittest.TestCase):
 def test_modes_are_explicit(self):
  for kwargs in ({}, {"directory_enabled":True}, {"directory_enabled":True,"directory_v2":True}, {"directory_enabled":True,"directory_v3":True,"directory_empty":True}, {"directory_enabled":True,"directory_v3":True,"directory_slow":True}):
   with self.assertRaises(ValueError): LDAPFixture(computer_assets_v3=True,**kwargs)
 def test_valid_mode_and_legacy_defaults(self):
  fixture=LDAPFixture(directory_enabled=True,directory_v3=True,computer_assets_v3=True)
  try:
   self.assertTrue(fixture.computer_assets_v3);self.assertFalse(fixture.user_assets_v2)
  finally: fixture.directory.cleanup()
  fixture=LDAPFixture(directory_enabled=True,directory_v3=True)
  try:self.assertFalse(fixture.computer_assets_v3)
  finally:fixture.directory.cleanup()
 def test_real_harness_includes_both_required_suites(self):
  self.assertEqual(COMPUTER_ASSETS_V3_SUITES,{"computer-assets-v3","computer-assets-v3-readers"})
  self.assertTrue(COMPUTER_ASSETS_V3_SUITES<=DIRECTORY_V3_SUITES)
  self.assertIn("computer-assets-v3-readers",REAL_TAB_LIFECYCLE_SUITES)
  workflow=reviewed_workflow()
  for suite in COMPUTER_ASSETS_V3_SUITES:
   self.assertIn(f'--suite {suite}',workflow)
   self.assertIn(suite,workflow.split('suite: [',1)[1].split(']',1)[0])
if __name__=='__main__':unittest.main()
