"""Exercise start, dependency failure, recovery, restart and clean shutdown."""
import json
import os
import secrets
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    project = "adtr-smoke-" + uuid.uuid4().hex[:12]
    env = dict(os.environ, ADTR_DEV_PASSWORD=secrets.token_hex(24), ADTR_API_PORT="0", ADTR_WORKER_PORT="0")
    base = ["docker", "compose", "-p", project]

    def compose(*args, capture=False):
        return subprocess.run(base + list(args), env=env, check=True, text=True, capture_output=capture)

    def probe(address, path, expected):
        try:
            with urllib.request.urlopen(f"http://{address}{path}", timeout=4) as response:
                code, body = response.status, response.read()
        except urllib.error.HTTPError as response:
            code, body = response.code, response.read()
        if code != expected:
            raise AssertionError(f"{path}: {code} != {expected}")
        return json.loads(body)

    try:
        subprocess.run(["make", "prepare-image"], env=env, check=True)
        compose("up", "--build", "--detach", "--wait", "--wait-timeout", "120")
        addresses = {service: compose("port", service, port, capture=True).stdout.strip()
                     for service, port in [("api", "8080"), ("worker", "8081")]}
        for service, address in addresses.items():
            assert probe(address, "/readyz", 200)["service"] == service
        compose("stop", "db")
        for address in addresses.values():
            probe(address, "/livez", 200)
            probe(address, "/readyz", 503)
        compose("start", "db")
        for _ in range(60):
            try:
                for address in addresses.values():
                    probe(address, "/readyz", 200)
                break
            except (AssertionError, OSError):
                time.sleep(0.5)
        else:
            raise AssertionError("database recovery did not restore readiness")
        compose("restart", "api", "worker")
        compose("up", "--detach", "--wait", "--wait-timeout", "60")
        # Docker may allocate a different ephemeral host port after restart.
        addresses = {service: compose("port", service, port, capture=True).stdout.strip()
                     for service, port in [("api", "8080"), ("worker", "8081")]}
        for address in addresses.values():
            probe(address, "/readyz", 200)
        compose("stop", "api", "worker")
        # A graceful SIGTERM must exit successfully, not be killed after timeout.
        for service in ["api", "worker"]:
            container = compose("ps", "--all", "--quiet", service, capture=True).stdout.strip()
            state = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .State}}", container], text=True))
            assert state["ExitCode"] == 0 and not state["OOMKilled"], service
        print("Lifecycle: start, DB outage/recovery, restart and graceful stop PASS")
    finally:
        # This randomly named project belongs solely to this test.
        compose("down", "--volumes", "--remove-orphans")


if __name__ == "__main__":
    main()
