"""Exercise owned helper cleanup without requiring Docker or claiming SQL execution."""
import io
import subprocess
import unittest
from unittest.mock import Mock

from account_barrier_e2e_fixture import AccountBarrierFixture


class AccountBarrierLifecycleContract(unittest.TestCase):
    def fixture(self, wait):
        fixture = AccountBarrierFixture.__new__(AccountBarrierFixture)
        fixture.closed = False
        fixture.logs = io.StringIO()
        fixture.directory = Mock()
        fixture.process = Mock()
        fixture.process.wait.side_effect = wait
        return fixture

    def test_eof_precedes_wait_and_close_is_idempotent(self):
        observed = []
        fixture = self.fixture(lambda **_: observed.append("wait") or 0)
        fixture.process.stdin.close.side_effect = lambda: observed.append("eof")
        fixture.close()
        fixture.close()
        self.assertEqual(observed, ["eof", "wait"])
        fixture.process.kill.assert_not_called()
        fixture.directory.cleanup.assert_called_once()
        self.assertTrue(fixture.logs.closed)

    def test_nonzero_exit_is_not_accepted_as_released_success(self):
        fixture = self.fixture(lambda **_: 1)
        with self.assertRaisesRegex(RuntimeError, "helper failed"):
            fixture.close()
        fixture.process.stdin.close.assert_called_once()
        fixture.directory.cleanup.assert_called_once()
        self.assertTrue(fixture.logs.closed)

    def test_timeout_kills_only_owned_child_and_still_releases_resources(self):
        fixture = self.fixture([subprocess.TimeoutExpired("owned-helper", 10), -9])
        with self.assertRaisesRegex(RuntimeError, "did not stop gracefully"):
            fixture.close()
        fixture.process.kill.assert_called_once()
        self.assertEqual(fixture.process.wait.call_args_list[0].kwargs, {"timeout": 10})
        self.assertEqual(fixture.process.wait.call_args_list[1].kwargs, {"timeout": 5})
        fixture.directory.cleanup.assert_called_once()
        self.assertTrue(fixture.logs.closed)

    def test_input_close_error_still_waits_and_is_not_hidden(self):
        fixture = self.fixture(lambda **_: 0)
        fixture.process.stdin.close.side_effect = OSError("synthetic pipe failure")
        with self.assertRaisesRegex(RuntimeError, "input did not close"):
            fixture.close()
        fixture.process.wait.assert_called_once_with(timeout=10)
        fixture.directory.cleanup.assert_called_once()


if __name__ == "__main__":
    unittest.main()
