"""Unit-test lifecycle ordering and assertions, not actual PostgreSQL evidence."""
import contextlib
import io
import subprocess
import unittest

from message_bulk_lifecycle import verify_bulk_crash_recovery


class BulkLifecycleContract(unittest.TestCase):
    def test_crash_restores_progress_without_rewriting_receipts(self):
        calls = []
        worker_starts = 0
        pg_ready_calls = 0

        def compose(*args, capture=False):
            nonlocal worker_starts, pg_ready_calls
            calls.append(args)
            if args == ("start", "worker"):
                worker_starts += 1
            if "pg_isready" in args:
                pg_ready_calls += 1
                if pg_ready_calls == 1:
                    raise subprocess.CalledProcessError(1, args)
                out = "accepting connections"
            elif "psql" in args:
                query = args[-1]
                if "SELECT state||" in query:
                    out = "completed:51:0" if worker_starts == 2 else "running:50:1"
                elif "SELECT EXISTS" in query:
                    out = "t"
                elif "SELECT md5" in query:
                    out = "original-receipt-digest"
                elif "SELECT accepted_at" in query:
                    out = "original-acceptance"
                elif "SELECT count(*) FROM adtr.message_reads" in query:
                    out = "51" if worker_starts == 2 else "50"
                else:
                    out = ""
            else:
                out = ""
            return subprocess.CompletedProcess(args, 0, stdout=out)

        with contextlib.redirect_stdout(io.StringIO()):
            verify_bulk_crash_recovery(compose, pause=lambda _: None)
        kill = calls.index(("kill", "--signal", "SIGKILL", "db", "worker"))
        restart = calls.index(("start", "db"))
        self.assertLess(kill, restart)
        self.assertEqual(worker_starts, 2)
        self.assertEqual(pg_ready_calls, 2)
        self.assertFalse(any("--force-recreate" in c for c in calls))
        self.assertTrue(any("AND ordinal<50" in c[-1] for c in calls))

    def test_missing_partial_progress_is_a_failure(self):
        def compose(*args, capture=False):
            out = "queued:0:51" if "psql" in args else ""
            return subprocess.CompletedProcess(args, 0, stdout=out)
        with self.assertRaisesRegex(AssertionError, "did not reach"):
            verify_bulk_crash_recovery(compose, pause=lambda _: None)


if __name__ == "__main__":
    unittest.main()
