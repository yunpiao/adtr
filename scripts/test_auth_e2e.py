"""Real browser/API/PostgreSQL acceptance on an isolated, owned test database."""
import argparse
import base64
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

from test_integration import IMAGE
from ldap_e2e_fixture import LDAPFixture
from account_barrier_e2e_fixture import AccountBarrierFixture
from e2e_owned_container import OWNER_LABEL, remove_owned_container


REAL_TAB_LIFECYCLE_SUITES = {"session-invalidation", "directory-readers", "directory-v2-readers"}
DIRECTORY_V2_SUITES = {"directory-v2", "directory-v2-controls", "directory-v2-readers"}
DIRECTORY_SUITES = {"directory", "directory-controls", "directory-readers"} | DIRECTORY_V2_SUITES
EMPTY_DIRECTORY_SUITES = {"directory-controls", "directory-readers", "directory-v2-controls", "directory-v2-readers"}
SLOW_DIRECTORY_SUITES = {"directory-controls", "directory-v2-controls"}


def browser_command(suite):
    command = ["npm", "run", "test:e2e", "--prefix", "web", "--", f"e2e/{suite}.spec.ts"]
    if suite in REAL_TAB_LIFECYCLE_SUITES:
        # These regressions require genuine background/foreground tab changes.
        # Headless shell keeps tabs visible even after focus emulation is off.
        # --headed overrides the ordinary suites' Playwright headless default.
        command.append("--headed")
        if sys.platform.startswith("linux") and not os.environ.get("DISPLAY"):
            if shutil.which("xvfb-run") is None:
                raise RuntimeError(
                    f"{suite} requires headed Chromium and a display; install Xvfb "
                    "with `cd web && npx playwright install --with-deps chromium` "
                    "or provide DISPLAY"
                )
            command = ["xvfb-run", "-a", *command]
    return command


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", choices=["auth", "access", "resource", "tasks", "audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", "account-references", "operational-logs", "session-invalidation", "account-reference-barrier", "directory", "directory-controls", "directory-readers", "directory-v2", "directory-v2-controls", "directory-v2-readers"], default="auth")
    parser.add_argument("--expired", action="store_true")
    args = parser.parse_args()
    # Check display support before creating a disposable database or fixtures.
    command = browser_command(args.suite)
    name = "adtr-auth-e2e-" + uuid.uuid4().hex[:12]
    password = secrets.token_hex(24)
    env = dict(os.environ, POSTGRES_PASSWORD=password)
    creation_attempted = False
    owner = uuid.uuid4().hex
    server = None
    worker = None
    ldap_fixture = None
    account_barrier = None
    logs = tempfile.TemporaryFile(mode="w+")
    try:
        creation_attempted = True
        subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                        "--label", f"{OWNER_LABEL}={owner}",
                        "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e",
                        "-p", "127.0.0.1::5432", IMAGE], env=env, check=True, capture_output=True, timeout=60)
        for _ in range(60):
            if subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "adtr_e2e"], capture_output=True, timeout=5).returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("isolated authentication database did not become ready")
        db_address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True, timeout=10).strip()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env.update(ADTR_DATABASE_URL=f"postgres://postgres:{password}@{db_address}/adtr_e2e?sslmode=disable",
                   ADTR_AUTH_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                   ADTR_DEVELOPMENT="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DIRECTORY_READ_V2_ENABLED="false", ADTR_ORIGIN=base, ADTR_LISTEN_ADDR=f"127.0.0.1:{port}",
                   ADTR_WEB_DIR=str(Path("web/dist").resolve()), ADTR_BOOTSTRAP_USERNAME="e2e-admin",
                   ADTR_BOOTSTRAP_PASSWORD=secrets.token_urlsafe(24), ADTR_E2E_BASE_URL=base)
        env["ADTR_E2E_USERNAME"] = env["ADTR_BOOTSTRAP_USERNAME"]
        env["ADTR_E2E_PASSWORD"] = env["ADTR_BOOTSTRAP_PASSWORD"]
        if not Path("web/dist/index.html").is_file():
            raise RuntimeError("build the real frontend before running acceptance")
        for mode in ["migrate", "bootstrap"]:
            subprocess.run(["./bin/adtr", "-mode", mode], env=env, check=True, timeout=60)
        if args.suite == "resource":
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('default','synthetic-domain-a','Synthetic Domain A',true)"],
                           check=True, capture_output=True, timeout=30)
        if args.expired:
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "UPDATE adtr.users SET must_change=false,password_updated_at=now()-interval '91 days' WHERE username='e2e-admin'"],
                           check=True, capture_output=True, timeout=30)
            env["ADTR_E2E_EXPIRED"] = "1"
        if args.suite in {"domains", "operations", "credential-use", "account-references", "account-reference-barrier"} | DIRECTORY_SUITES:
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('default',2,extract(epoch FROM clock_timestamp())::bigint+86400,'synthetic-e2e','Synthetic tenant')"],
                           check=True, capture_output=True, timeout=30)
            if args.suite in {"domains", "account-references", "account-reference-barrier"} | DIRECTORY_SUITES:
                ldap_fixture = LDAPFixture(control_enabled=args.suite == "account-reference-barrier",
                                           directory_enabled=args.suite in DIRECTORY_SUITES,
                                           directory_empty=args.suite in EMPTY_DIRECTORY_SUITES,
                                           directory_slow=args.suite in SLOW_DIRECTORY_SUITES,
                                           directory_v2=args.suite in DIRECTORY_V2_SUITES)
                ldap_fixture.start(env)
                if args.suite in DIRECTORY_SUITES:
                    env["ADTR_DIRECTORY_READ_ENABLED"] = "true"
                if args.suite in DIRECTORY_V2_SUITES:
                    env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "true"
                if args.suite in EMPTY_DIRECTORY_SUITES:
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in {"directory-readers", "directory-v2-readers"}:
                    env["ADTR_E2E_LDAP_DIRECTORY_SLOW"] = "false"
                if args.suite == "account-reference-barrier":
                    account_barrier = AccountBarrierFixture()
                    account_barrier.start(env, ldap_fixture.control_path)
            else:
                env.update(ADTR_DOMAIN_KEY_ID="synthetic-operations", ADTR_DOMAIN_KEY=base64.b64encode(secrets.token_bytes(32)).decode(), ADTR_DOMAIN_PROBE_ENABLED="false")
                for key in ["ADTR_LDAP_CA_FILE", "ADTR_LDAP_EGRESS_POLICY_FILE"]:
                    env.pop(key, None)
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
        if args.suite in {"tasks", "audit", "system", "maintenance", "domains", "account-references", "operational-logs", "account-reference-barrier"} | DIRECTORY_SUITES:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                worker_port = listener.getsockname()[1]
            worker_env = dict(runtime_env, ADTR_LISTEN_ADDR=f"127.0.0.1:{worker_port}")
            # Execution authorization needs persisted actors, not browser secrets.
            for key in ["ADTR_AUTH_KEY", "ADTR_ORIGIN", "ADTR_WEB_DIR"]:
                worker_env.pop(key, None)
            worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, stdout=logs, stderr=logs)
            for _ in range(100):
                if worker.poll() is not None:
                    raise RuntimeError("task worker exited during startup")
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{worker_port}/readyz", timeout=1) as response:
                        if response.status == 200:
                            break
                except (OSError, urllib.error.URLError):
                    pass
                time.sleep(0.1)
            else:
                raise RuntimeError("task worker failed readiness")
        if args.suite in {"system", "operational-logs"}:
            env["ADTR_E2E_WORKER_PID"] = str(worker.pid)
        # Trace/screenshot settings do not disable Playwright's automatic failure
        # DOM snapshot. Suppress that separate artifact so proof fields cannot
        # enter an error-context prompt; explicit safe screenshots remain intact.
        env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"
        subprocess.run(command, env=env, check=True, timeout=930 if args.suite in EMPTY_DIRECTORY_SUITES else 660 if args.suite in {"directory", "directory-v2"} else 600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", "operations", "credential-use", "account-references", "operational-logs", "account-reference-barrier"} else 480 if args.suite == "system" else 420)
        chain = "real browser -> API -> worker/PostgreSQL" if worker is not None else "real browser -> API -> PostgreSQL"
    finally:
        shutdown_error = None
        if account_barrier is not None:
            try:
                account_barrier.close()
            except Exception as error:
                shutdown_error = error
        for label, process in [("worker", worker), ("API", server)]:
            if process is None:
                continue
            process.terminate()
            try:
                code = process.wait(timeout=30)
                if code != 0:
                    shutdown_error = RuntimeError(f"{label} did not exit successfully")
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
                shutdown_error = RuntimeError(f"{label} did not stop gracefully")
        if ldap_fixture is not None:
            try:
                ldap_fixture.close()
            except Exception as error:
                shutdown_error = error
        logs.close()
        if creation_attempted:
            remove_owned_container(name, owner)
        if shutdown_error is not None:
            raise shutdown_error
    print(f"{args.suite} (expired={args.expired}): {chain} acceptance PASS")



if __name__ == "__main__":
    main()
