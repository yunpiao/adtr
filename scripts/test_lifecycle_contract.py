"""Fast regression tests for the lifecycle runner, without pretending to run Docker."""
import subprocess
import unittest
from unittest.mock import Mock

from test_lifecycle import container_ids, wait_ready


class LifecycleContract(unittest.TestCase):
    def runner(self, ids):
        def compose(*args, capture=False):
            if args[0] == "ps":
                return subprocess.CompletedProcess(args, 0, stdout=ids[args[-1]])
            if args[0] == "port":
                return subprocess.CompletedProcess(args, 0, stdout=args[1])
            raise AssertionError(f"readiness polling must not mutate containers: {args}")
        return compose

    def test_transient_failure_recovers_without_recreation(self):
        ids = {"api": "a", "worker": "w"}
        probe = Mock(side_effect=[OSError("starting"), {"service": "api"}, {"service": "worker"}])
        wait_ready(self.runner(ids), probe, ids, attempts=2, pause=lambda _: None)
        self.assertEqual(probe.call_count, 3)

    def test_replacement_fails_before_probe(self):
        probe = Mock()
        with self.assertRaisesRegex(AssertionError, "container changed"):
            wait_ready(self.runner({"api": "new", "worker": "w"}), probe,
                       {"api": "old", "worker": "w"}, pause=lambda _: None)
        probe.assert_not_called()

    def test_never_ready_fails(self):
        ids = {"api": "a", "worker": "w"}
        with self.assertRaisesRegex(AssertionError, "did not recover"):
            wait_ready(self.runner(ids), Mock(side_effect=OSError()), ids, attempts=2, pause=lambda _: None)

    def test_empty_or_multiple_ids_fail(self):
        for bad in ["", "a\nb"]:
            with self.assertRaises(AssertionError):
                container_ids(self.runner({"api": bad, "worker": "w"}))


if __name__ == "__main__":
    unittest.main()
