"""Real browser/API/PostgreSQL acceptance on an isolated, owned test database."""
import argparse
import base64
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

from test_integration import IMAGE


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", choices=["auth", "access"], default="auth")
    parser.add_argument("--expired", action="store_true")
    args = parser.parse_args()
    name = "adtr-auth-e2e-" + uuid.uuid4().hex[:12]
    password = secrets.token_hex(24)
    env = dict(os.environ, POSTGRES_PASSWORD=password)
    created = False
    server = None
    logs = tempfile.TemporaryFile(mode="w+")
    try:
        subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                        "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e",
                        "-p", "127.0.0.1::5432", IMAGE], env=env, check=True, capture_output=True)
        created = True
        for _ in range(60):
            if subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "adtr_e2e"], capture_output=True).returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("isolated authentication database did not become ready")
        db_address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True).strip()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env.update(ADTR_DATABASE_URL=f"postgres://postgres:{password}@{db_address}/adtr_e2e?sslmode=disable",
                   ADTR_AUTH_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                   ADTR_DEVELOPMENT="true", ADTR_ORIGIN=base, ADTR_LISTEN_ADDR=f"127.0.0.1:{port}",
                   ADTR_WEB_DIR=str(Path("web/dist").resolve()), ADTR_BOOTSTRAP_USERNAME="e2e-admin",
                   ADTR_BOOTSTRAP_PASSWORD=secrets.token_urlsafe(24), ADTR_E2E_BASE_URL=base)
        env["ADTR_E2E_USERNAME"] = env["ADTR_BOOTSTRAP_USERNAME"]
        env["ADTR_E2E_PASSWORD"] = env["ADTR_BOOTSTRAP_PASSWORD"]
        if not Path("web/dist/index.html").is_file():
            raise RuntimeError("build the real frontend before running acceptance")
        for mode in ["migrate", "bootstrap"]:
            subprocess.run(["./bin/adtr", "-mode", mode], env=env, check=True, timeout=60)
        if args.expired:
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "UPDATE adtr.users SET must_change=false,password_updated_at=now()-interval '91 days' WHERE username='e2e-admin'"],
                           check=True, capture_output=True)
            env["ADTR_E2E_EXPIRED"] = "1"
        # Credentials are only needed by explicit bootstrap; never pass them to the server.
        runtime_env = {k: v for k, v in env.items() if not k.startswith("ADTR_BOOTSTRAP_") and not k.startswith("ADTR_E2E_")}
        server = subprocess.Popen(["./bin/adtr", "-mode", "api"], env=runtime_env, stdout=logs, stderr=logs)
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError("authentication API exited during startup")
            try:
                with urllib.request.urlopen(base + "/readyz", timeout=1) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("authentication API failed readiness")
        subprocess.run(["npm", "run", "test:e2e", "--prefix", "web", "--", f"e2e/{args.suite}.spec.ts"], env=env, check=True, timeout=420)
        print(f"{args.suite} (expired={args.expired}): real browser -> API -> PostgreSQL acceptance PASS")
    finally:
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=10)
                raise RuntimeError("authentication server did not stop gracefully")
        logs.close()
        if created:
            subprocess.run(["docker", "rm", "--force", name], check=True, capture_output=True)


if __name__ == "__main__":
    main()
