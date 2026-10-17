"""Run real PostgreSQL tests against a new, disposable, loopback-only container."""
import os
import secrets
import subprocess
import time
import uuid

from ci_postgres_image import postgres_image_args
from run_integration import parse_args, run_tests

IMAGE = "docker.io/library/postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"


def main():
    args = parse_args()
    # Real owned-child protocol tests are explicit CI work, never import-time contracts.
    if args.suite in ("all", "non-auth"):
        subprocess.run(["make", "test-owned-rescue-ci"], check=True)
    fixture_psql = args.suite in ("all", "non-auth")
    name = ("adtr-auth-e2e-" if fixture_psql else "adtr-test-") + uuid.uuid4().hex[:12]
    password = secrets.token_hex(24)
    env = dict(os.environ, POSTGRES_PASSWORD=password)
    created = False
    try:
        subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                        "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_test",
                        "-p", "127.0.0.1::5432", *postgres_image_args(IMAGE)], env=env, check=True, capture_output=True)
        created = True
        for _ in range(60):
            ready = subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "adtr_test"], capture_output=True)
            if ready.returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("disposable database did not become ready")
        if fixture_psql:
            subprocess.run(["python3", "scripts/test_activity_fixture_psql_runtime.py", "--container", name],
                           env=dict(env, ADTR_RUN_ACTIVITY_PSQL_TESTS="1"), check=True)
        address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True).strip()
        env["ADTR_TEST_DATABASE_URL"] = f"postgres://postgres:{password}@{address}/adtr_test?sslmode=disable"
        return run_tests(args, env)
    finally:
        if created:
            subprocess.run(["docker", "rm", "--force", name], check=True, capture_output=True)


if __name__ == "__main__":
    raise SystemExit(main())
