"""Owned, bounded SQL-lock helper for the synthetic account-use browser suite."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


class AccountBarrierFixture:
    def __init__(self):
        self.directory = tempfile.TemporaryDirectory(prefix="adtr-account-barrier-")
        self.logs = tempfile.TemporaryFile(mode="w+")
        self.process = None
        self.closed = False

    def start(self, env, control_path):
        control = Path(control_path).resolve(strict=True)
        if not control.is_dir() or not env.get("ADTR_DATABASE_URL"):
            raise RuntimeError("synthetic acknowledgement helper configuration unavailable")
        binary = Path(self.directory.name) / "account-barrier"
        subprocess.run(["go", "build", "-tags=integration", "-o", str(binary),
                        "./internal/testaccountbarrier"], check=True, capture_output=True, timeout=120)
        helper_env = dict(os.environ, ADTR_BARRIER_DATABASE_URL=env["ADTR_DATABASE_URL"])
        self.process = subprocess.Popen([str(binary), "-control-dir", str(control)],
                                        env=helper_env, stdin=subprocess.PIPE,
                                        stdout=self.logs, stderr=self.logs)
        ready = control / "barrier-ready.json"
        for _ in range(100):
            if self.process.poll() is not None:
                raise RuntimeError("synthetic acknowledgement helper exited before readiness")
            try:
                if ready.stat().st_size > 4096:
                    raise RuntimeError("invalid acknowledgement helper readiness")
                value = json.loads(ready.read_text())
                if value != {"version": 1, "phase": "ready"}:
                    raise RuntimeError("invalid acknowledgement helper readiness")
                env["ADTR_E2E_BARRIER_CONTROL_DIR"] = str(control)
                return
            except FileNotFoundError:
                pass
            time.sleep(0.1)
        raise RuntimeError("synthetic acknowledgement helper failed readiness")

    def close(self):
        if self.closed:
            return
        self.closed = True
        failure = None
        try:
            if self.process is not None:
                # EOF asks the helper to roll back its lock before API/worker
                # shutdown. No task state or cleanup witness is manufactured.
                if self.process.stdin is not None:
                    try:
                        self.process.stdin.close()
                    except BrokenPipeError:
                        pass
                    except (OSError, ValueError):
                        failure = RuntimeError("synthetic acknowledgement helper input did not close")
                try:
                    code = self.process.wait(timeout=10)
                    if code != 0:
                        failure = RuntimeError("synthetic acknowledgement helper failed")
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait(timeout=5)
                    failure = RuntimeError("synthetic acknowledgement helper did not stop gracefully")
        finally:
            self.logs.close()
            self.directory.cleanup()
        if failure is not None:
            raise failure
