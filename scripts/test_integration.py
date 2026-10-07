"""Run real PostgreSQL tests against a new, disposable, loopback-only container."""
import os
import secrets
import subprocess
import time
import uuid

IMAGE = "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"


def main():
    name = "adtr-test-" + uuid.uuid4().hex[:12]
    password = secrets.token_hex(24)
    env = dict(os.environ, POSTGRES_PASSWORD=password)
    created = False
    try:
        subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                        "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_test",
                        "-p", "127.0.0.1::5432", IMAGE], env=env, check=True, capture_output=True)
        created = True
        for _ in range(60):
            ready = subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "adtr_test"], capture_output=True)
            if ready.returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("disposable database did not become ready")
        address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True).strip()
        env["ADTR_TEST_DATABASE_URL"] = f"postgres://postgres:{password}@{address}/adtr_test?sslmode=disable"
        subprocess.run(["make", "integration"], env=env, check=True)
    finally:
        if created:
            subprocess.run(["docker", "rm", "--force", name], check=True, capture_output=True)


if __name__ == "__main__":
    main()
