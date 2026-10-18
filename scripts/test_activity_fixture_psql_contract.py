"""Offline source contracts; never start PostgreSQL or subprocesses."""
import ast
from pathlib import Path
import unittest


class ActivityPsqlContracts(unittest.TestCase):
    def test_runtime_is_explicit_and_uses_only_existing_container(self):
        source = Path(__file__).with_name('test_activity_fixture_psql_runtime.py').read_text()
        tree = ast.parse(source)
        self.assertIn("os.environ.get('ADTR_RUN_ACTIVITY_PSQL_TESTS') != '1'", source)
        self.assertIn("sys.platform != 'linux'", source)
        self.assertIn("r'adtr-auth-e2e-[0-9a-f]{12}'", source)
        runs = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute) and ast.unparse(node.func) == 'subprocess.run']
        self.assertEqual(len(runs), 1)
        self.assertEqual(ast.unparse(runs[0].args[0]),
                         "['docker', 'exec', CONTAINER, 'createdb', '-U', 'postgres', 'adtr_e2e']")
        self.assertNotIn('DROP ', source)
        self.assertNotIn('TRUNCATE ', source)
        self.assertIn('result.testsRun == 3', source)
        self.assertIn('unittest.TestResult()', source)
        self.assertNotIn('unittest.main(', source)

    def test_real_guard_and_psql_lifecycle_are_exercised(self):
        source = Path(__file__).with_name('test_activity_fixture_psql_runtime.py').read_text()
        self.assertIn('Psql(CONTAINER, readonly)', source)
        self.assertIn("guard.prepare_activity_fixture('lifecycle', 'ordinary-lifecycle')", source)
        self.assertIn('guard.cleanup_activity_fixture_after_wait(0)', source)
        self.assertIn('FOR UPDATE NOWAIT', source)
        self.assertIn('barrier.close()', source)
        self.assertIn('self.assertEqual(unlocked.query(', source)
        self.assertIn('self.assertNotEqual(contender.process.returncode, 0)', source)
        self.assertIn("'^checkpoint database session ended$'", source)
        self.assertIn('self.assertFalse(guard.activity_fixture_created)', source)
        self.assertNotIn('patch(', source)


if __name__ == '__main__':
    unittest.main()
