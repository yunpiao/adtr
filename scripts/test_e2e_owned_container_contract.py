"""Ownership/uncertain-outcome unit contracts; every Docker call is mocked."""
import subprocess
import unittest
from unittest.mock import patch
import test_auth_e2e
from e2e_owned_container import OWNER_LABEL, owned_container_id, remove_owned_container

NAME = "adtr-auth-e2e-0123456789ab"
OWNER = "a" * 32
IDENTITY = "b" * 64


class OwnedContainerTest(unittest.TestCase):
    def test_uncertain_create_is_reconciled_and_removed_by_identity(self):
        calls = []
        def run(args, **options):
            calls.append((args, options))
            return subprocess.CompletedProcess(args, 0, stdout=f"{IDENTITY} {NAME}\n")
        remove_owned_container(NAME, OWNER, run)
        self.assertIn(f"label={OWNER_LABEL}={OWNER}", calls[0][0])
        self.assertEqual(calls[0][1]["timeout"], 10)
        self.assertEqual(calls[1][0], ["docker", "rm", "--force", IDENTITY])
        self.assertEqual(calls[1][1]["timeout"], 30)

    def test_runner_reconciles_when_create_times_out_before_confirmation(self):
        with patch("sys.argv", ["test_auth_e2e.py"]), \
             patch.object(test_auth_e2e.subprocess, "run", side_effect=subprocess.TimeoutExpired(["docker", "run"], 60)) as create, \
             patch.object(test_auth_e2e, "remove_owned_container") as cleanup:
            with self.assertRaises(subprocess.TimeoutExpired):
                test_auth_e2e.main()
        cleanup.assert_called_once()
        name, owner = cleanup.call_args.args
        self.assertRegex(name, r"^adtr-auth-e2e-[0-9a-f]{12}$")
        self.assertRegex(owner, r"^[0-9a-f]{32}$")
        self.assertIn(f"{OWNER_LABEL}={owner}", create.call_args.args[0])
        self.assertEqual(create.call_args.kwargs["timeout"], 60)

    def test_absent_or_similarly_named_container_is_not_removed(self):
        for text in ["", f"{IDENTITY} {NAME}-other\n"]:
            calls = []
            def run(args, **options):
                calls.append(args)
                return subprocess.CompletedProcess(args, 0, stdout=text)
            remove_owned_container(NAME, OWNER, run)
            self.assertEqual(len(calls), 1)

    def test_uncertain_removal_only_succeeds_after_confirmed_absence(self):
        for remains in [False, True]:
            calls = []
            def run(args, **options):
                calls.append(args)
                if args[1] == "rm":
                    raise subprocess.TimeoutExpired(args, 30)
                text = f"{IDENTITY} {NAME}\n" if len(calls) == 1 or remains else ""
                return subprocess.CompletedProcess(args, 0, stdout=text)
            if remains:
                with self.assertRaisesRegex(RuntimeError, "incomplete"):
                    remove_owned_container(NAME, OWNER, run)
            else:
                remove_owned_container(NAME, OWNER, run)
            self.assertEqual(sum(args[1] == "rm" for args in calls), 1)
            self.assertEqual(len(calls), 3)

    def test_unknown_lookup_fails_without_deletion(self):
        calls = []
        def run(args, **options):
            calls.append(args)
            raise subprocess.TimeoutExpired(args, 10)
        with self.assertRaises(subprocess.TimeoutExpired):
            remove_owned_container(NAME, OWNER, run)
        self.assertEqual(len(calls), 1)

    def test_unsafe_identity_never_reaches_docker(self):
        def never(*args, **kwargs):
            self.fail("unsafe identity reached Docker")
        for name, owner in [("unowned", OWNER), (NAME, ""), (NAME + ";x", OWNER)]:
            with self.assertRaises(ValueError):
                owned_container_id(name, owner, never)


if __name__ == "__main__":
    unittest.main()
