"""Exercise start, dependency failure, recovery, restart and clean shutdown."""
import json
import os
import secrets
import subprocess
import time
import urllib.error
import urllib.request
import uuid


from ci_postgres_image import postgres_compose_files


SERVICES = [("api", "8080"), ("worker", "8081")]


def container_ids(compose):
    result = {service: compose("ps", "--all", "--quiet", service, capture=True).stdout.strip()
              for service, _ in SERVICES}
    if any(not value or "\n" in value for value in result.values()):
        raise AssertionError("expected exactly one container for each service")
    return result


def wait_ready(compose, probe, expected_ids, attempts=120, pause=time.sleep):
    for _ in range(attempts):
        if container_ids(compose) != expected_ids:
            raise AssertionError("service container changed during readiness wait")
        try:
            for service, port in SERVICES:
                address = compose("port", service, port, capture=True).stdout.strip()
                if probe(address, "/readyz", 200)["service"] != service:
                    raise AssertionError("wrong service identity")
            return
        except (AssertionError, OSError):
            pause(0.5)
    raise AssertionError("services did not recover readiness in the expected containers")


def run_lifecycle(compose_files=()):
    project = "adtr-smoke-" + uuid.uuid4().hex[:12]
    env = dict(os.environ, ADTR_DEV_PASSWORD=secrets.token_hex(24), ADTR_API_PORT="0", ADTR_WORKER_PORT="0")
    base = ["docker", "compose", *compose_files, "-p", project]

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
        # A restart must recover the SAME containers. Running `up` here could
        # replace a broken container and turn a restart failure into a false pass.
        before = container_ids(compose)
        compose("restart", "api", "worker")
        wait_ready(compose, probe, before)
        # Recreation is a separate lifecycle behavior, not restart recovery.
        compose("up", "--detach", "--force-recreate", "--no-deps", "api", "worker")
        recreated = container_ids(compose)
        if any(recreated[service] == before[service] for service in before):
            raise AssertionError("explicit recreation did not replace every service")
        wait_ready(compose, probe, recreated)
        compose("stop", "api", "worker")
        # A graceful SIGTERM must exit successfully, not be killed after timeout.
        for service in ["api", "worker"]:
            container = compose("ps", "--all", "--quiet", service, capture=True).stdout.strip()
            state = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .State}}", container], text=True))
            assert state["ExitCode"] == 0 and not state["OOMKilled"], service
        print("Lifecycle: start, DB outage/recovery, same-container restart, separate recreation and graceful stop PASS")
    finally:
        # This randomly named project belongs solely to this test.
        compose("down", "--volumes", "--remove-orphans")


def main():
    with postgres_compose_files() as compose_files:
        run_lifecycle(compose_files)


if __name__ == "__main__":
    main()

