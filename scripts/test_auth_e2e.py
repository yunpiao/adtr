"""Real browser/API/PostgreSQL acceptance on an isolated, owned test database."""
import argparse
import base64
import os
from pathlib import Path
import secrets
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

from user_asset_export_v3_ci import configure as configure_user_export, capture as capture_user_export, validate as validate_user_export
from user_asset_export_v3_terminal_ci import SUITES as EXPORT_TERMINAL_SUITES, configure as configure_terminal_export, capture as capture_terminal_export, validate as validate_terminal_export
from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard
from asset_export_v3_running_cancel_guard import RunningExportCancelGuard
from user_asset_export_v3_running_cancel_ci import SUITE as RUNNING_CANCEL_SUITE, configure as configure_running_cancel, capture as capture_running_cancel, validate as validate_running_cancel
from group_asset_export_v3_running_cancel_ci import SUITE as GROUP_RUNNING_CANCEL_SUITE, configure as configure_group_running_cancel, capture as capture_group_running_cancel, validate as validate_group_running_cancel
from computer_asset_export_v3_running_cancel_ci import SUITE as COMPUTER_RUNNING_CANCEL_SUITE, configure as configure_computer_running_cancel, capture as capture_computer_running_cancel, validate as validate_computer_running_cancel
from computer_asset_export_v3_ci import configure as configure_computer_export, capture as capture_computer_export, validate as validate_computer_export
from computer_asset_export_v3_terminal_ci import SUITES as COMPUTER_EXPORT_TERMINAL_SUITES, configure as configure_computer_terminal, capture as capture_computer_terminal, validate as validate_computer_terminal
from group_asset_export_v3_ci import configure as configure_group_export, capture as capture_group_export, validate as validate_group_export
from group_asset_export_v3_terminal_ci import SUITES as GROUP_EXPORT_TERMINAL_SUITES, configure as configure_group_terminal, capture as capture_group_terminal, validate as validate_group_terminal
from ci_postgres_image import postgres_image_args
from test_integration import IMAGE
from ldap_e2e_fixture import LDAPFixture
from account_barrier_e2e_fixture import AccountBarrierFixture
from directory_e2e_credentials import configure_directory_credentials
from e2e_owned_container import OWNER_LABEL, remove_owned_container
from e2e_runtime_diagnostics import http_handler_panicked, RuntimeDiagnostics
from e2e_container_startup_diagnostics import report_container_startup_failure


REAL_TAB_LIFECYCLE_SUITES = {"account-references", "session-invalidation", "directory-readers", "directory-v2-readers", "user-assets-v2-readers", "user-assets-v3", "computer-assets-v3-readers", "group-assets-v3-readers", "directory-v3-readers"}
USER_ASSETS_V2_SUITES = {"user-assets-v2", "user-assets-v2-readers"}
DIRECTORY_V2_SUITES = {"directory-v2", "directory-v2-controls", "directory-v2-readers"} | USER_ASSETS_V2_SUITES
COMPUTER_ASSETS_V3_SUITES = {"computer-assets-v3", "computer-assets-v3-readers"}
COMPUTER_EXPORT_SUITES = COMPUTER_EXPORT_TERMINAL_SUITES | {"computer-asset-export-v3"}
GROUP_EXPORT_SUITES = GROUP_EXPORT_TERMINAL_SUITES | {"group-asset-export-v3"}
GROUP_ASSETS_V3_SUITES = {"group-assets-v3", "group-assets-v3-readers"}
EMPTY_TRANSITION_SUITES = {"group-assets-v3", "computer-assets-v3"}
KIND_RUNNING_CANCEL_SUITES = {GROUP_RUNNING_CANCEL_SUITE, COMPUTER_RUNNING_CANCEL_SUITE}
DIRECTORY_V3_SUITES = KIND_RUNNING_CANCEL_SUITES | {RUNNING_CANCEL_SUITE} | COMPUTER_EXPORT_SUITES | GROUP_EXPORT_SUITES | EXPORT_TERMINAL_SUITES | {"user-asset-export-v3", "directory-v3-backend", "user-assets-v3", "directory-v3", "directory-v3-controls", "directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES
DIRECTORY_V4_SUITES = {"directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh"}
DIRECTORY_SUITES = DIRECTORY_V4_SUITES | DIRECTORY_V3_SUITES | {"directory", "directory-controls", "directory-readers"} | DIRECTORY_V2_SUITES
EMPTY_DIRECTORY_SUITES = {"directory-controls", "directory-readers", "directory-v2-controls", "directory-v2-readers", "directory-v3-controls", "directory-v3-readers"}
SLOW_DIRECTORY_SUITES = {"directory-controls", "directory-v2-controls", "directory-v3-controls"}


def browser_command(suite):
    command = ["npm", "run", "test:e2e", "--prefix", "web", "--", f"e2e/{suite}.spec.ts"]
    if suite == "messages-bulk":
        command.extend(["--output", "test-results/messages-bulk"])
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


def validate_asset_refresh_evidence(suite):
    if suite in EMPTY_TRANSITION_SUITES:
        subprocess.run([sys.executable, "scripts/validate_asset_empty_refresh.py",
                        "--kind", suite.split("-")[0],
                        "--evidence-root", "web/test-results", "--source-root", "."],
                       check=True, timeout=30)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", choices=["group-asset-export-v3-running-cancel", "computer-asset-export-v3-running-cancel", "user-asset-export-v3-running-cancel", "computer-asset-export-v3", "computer-asset-export-v3-failure", "computer-asset-export-v3-cancel", "group-asset-export-v3", "group-asset-export-v3-failure", "group-asset-export-v3-cancel", "user-asset-export-v3-failure", "user-asset-export-v3-cancel", "user-asset-export-v3", "auth", "access", "resource", "tasks", "messages", "messages-bulk", "audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", "account-references", "operational-logs", "session-invalidation", "account-reference-barrier", "directory", "directory-controls", "directory-readers", "directory-v2", "directory-v2-controls", "directory-v2-readers", "directory-v3-backend", "directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "user-assets-v3", "computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", "group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", "user-assets-v2", "user-assets-v2-readers"], default="auth")
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
    export_worker_guard = None
    running_cancel_guard = None
    ldap_fixture = None
    account_barrier = None
    message_fixture = None
    diagnostics = RuntimeDiagnostics()
    diagnostic_dir = Path(".e2e-diagnostics")
    diagnostic_dir.mkdir(parents=True, exist_ok=True)
    for filename in ["runtime.json", "browser.json"]:
        (diagnostic_dir / filename).unlink(missing_ok=True)
    env["ADTR_E2E_SAFE_DIAGNOSTICS"] = str((diagnostic_dir / "browser.json").resolve())
    diagnostics.phase("setup")
    if args.suite == "user-asset-export-v3":
        configure_user_export(env)
        capture_user_export()
    if args.suite in EXPORT_TERMINAL_SUITES:
        configure_terminal_export(env, args.suite)
        capture_terminal_export(args.suite)
    if args.suite == "group-asset-export-v3":
        configure_group_export(env)
        capture_group_export()
    if args.suite in GROUP_EXPORT_TERMINAL_SUITES:
        configure_group_terminal(env, args.suite)
        capture_group_terminal(args.suite)
    if args.suite == "computer-asset-export-v3":
        configure_computer_export(env)
        capture_computer_export()
    if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:
        configure_computer_terminal(env, args.suite)
        capture_computer_terminal(args.suite)
    if args.suite == RUNNING_CANCEL_SUITE:
        configure_running_cancel(env)
        capture_running_cancel()
    if args.suite == GROUP_RUNNING_CANCEL_SUITE:
        configure_group_running_cancel(env)
        capture_group_running_cancel()
    if args.suite == COMPUTER_RUNNING_CANCEL_SUITE:
        configure_computer_running_cancel(env)
        capture_computer_running_cancel()
    logs = tempfile.TemporaryFile(mode="w+")
    try:
        creation_attempted = True
        try:
            subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                            "--label", f"{OWNER_LABEL}={owner}",
                            "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e",
                            "-p", "127.0.0.1::5432", *postgres_image_args(IMAGE)], env=env, check=True, capture_output=True, timeout=60)
        except subprocess.CalledProcessError as error:
            report_container_startup_failure(error, password)
            raise
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
        configure_directory_credentials(env, args.suite)
        if not Path("web/dist/index.html").is_file():
            raise RuntimeError("build the real frontend before running acceptance")
        for mode in ["migrate", "bootstrap"]:
            subprocess.run(["./bin/adtr", "-mode", mode], env=env, check=True, timeout=60)
        if args.suite == "account-references":
            # Isolated synthetic role: real function permission with no resource
            # groups. The browser creates its user and authenticates normally.
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "BEGIN; INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default','source_no_scope_role_001','Source no domain scope'); INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default','source_no_scope_role_001','domains',true,true),('default','source_no_scope_role_001','operation_accounts',true,false); COMMIT;"],
                           check=True, capture_output=True, timeout=30)
        if args.suite == "resource":
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('default','synthetic-domain-a','Synthetic Domain A',true)"],
                           check=True, capture_output=True, timeout=30)
        if args.suite == "messages":
            # Read-only provenance checks wait for the real health projection; no message fixtures.
            env["ADTR_E2E_DB_CONTAINER"] = name
        if args.suite == "messages-bulk":
            message_fixture = tempfile.TemporaryDirectory(prefix="adtr-message-fixture-")
            fixture_binary = str(Path(message_fixture.name) / "message-fixture")
            subprocess.run(["go", "build", "-tags=integration", "-o", fixture_binary,
                            "./internal/testmessagefixture"], check=True, capture_output=True, timeout=120)
            env.update(ADTR_E2E_DB_CONTAINER=name, ADTR_E2E_MESSAGE_FIXTURE="owned-isolated-health",
                       ADTR_E2E_MESSAGE_FIXTURE_BINARY=fixture_binary)
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
                                           empty_transition=args.suite in EMPTY_TRANSITION_SUITES or args.suite == "group-members-v4-refresh",
                                           directory_enabled=args.suite in DIRECTORY_SUITES,
                                           directory_empty=args.suite in EMPTY_DIRECTORY_SUITES or args.suite == "group-members-v4-empty",
                                           directory_slow=args.suite in SLOW_DIRECTORY_SUITES,
                                           directory_v2=args.suite in DIRECTORY_V2_SUITES,
                                           directory_v3=args.suite in DIRECTORY_V3_SUITES,
                                           directory_v4=args.suite in DIRECTORY_V4_SUITES,
                                           group_members_v4_pagination=args.suite == "group-members-v4-pagination",
                                           group_members_v4_refresh=args.suite == "group-members-v4-refresh",
                                           computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES | COMPUTER_EXPORT_SUITES | {COMPUTER_RUNNING_CANCEL_SUITE},
                                           group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | GROUP_EXPORT_SUITES | {GROUP_RUNNING_CANCEL_SUITE},
                                           user_assets_v2=args.suite in USER_ASSETS_V2_SUITES,
                                           user_export_failure_v3=args.suite == "user-asset-export-v3-failure",
                                           group_export_failure_v3=args.suite == "group-asset-export-v3-failure",
                                           computer_export_failure_v3=args.suite == "computer-asset-export-v3-failure")
                ldap_fixture.start(env)
                if args.suite in DIRECTORY_SUITES:
                    env["ADTR_DIRECTORY_READ_ENABLED"] = "true"
                if args.suite in DIRECTORY_V4_SUITES:
                    env["ADTR_DIRECTORY_READ_V4_ENABLED"] = "true"
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in DIRECTORY_V3_SUITES:
                    env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "true"
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in DIRECTORY_V2_SUITES:
                    env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "true"
                if args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES:
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in {"directory-readers", "directory-v2-readers", "directory-v3-readers"}:
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
        diagnostics.phase("api-start")
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
        diagnostics.phase("api-ready")
        if args.suite in {"tasks", "messages", "messages-bulk", "audit", "system", "maintenance", "domains", "account-references", "operational-logs", "account-reference-barrier"} | DIRECTORY_SUITES:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                worker_port = listener.getsockname()[1]
            worker_env = dict(runtime_env, ADTR_LISTEN_ADDR=f"127.0.0.1:{worker_port}")
            if args.suite == "messages-bulk":
                # Identify only our worker connections for quiescent SIGSTOP tests.
                worker_env["ADTR_DATABASE_URL"] += "&application_name=adtr-message-bulk-worker"
            if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", "group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | KIND_RUNNING_CANCEL_SUITES:
                export_worker_nonce = uuid.uuid4().hex
                export_worker_app = "adtr-user-export-" + export_worker_nonce
                worker_env["ADTR_E2E_EXPORT_WORKER_NONCE"] = export_worker_nonce
                worker_env["ADTR_DATABASE_URL"] += "&application_name=" + export_worker_app
            # Execution authorization needs persisted actors, not browser secrets.
            for key in ["ADTR_AUTH_KEY", "ADTR_ORIGIN", "ADTR_WEB_DIR"]:
                worker_env.pop(key, None)
            diagnostics.phase("worker-start")
            worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, stdout=logs, stderr=logs)
            if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", "group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | KIND_RUNNING_CANCEL_SUITES:
                export_worker_guard = OwnedExportWorkerGuard(worker, export_worker_nonce, Path("bin/adtr").resolve())
                export_worker_guard.start()
                env.update(ADTR_E2E_WORKER_PID=str(worker.pid), ADTR_E2E_HARNESS_PID=str(os.getpid()),
                           ADTR_E2E_EXPORT_WORKER_STARTTIME=export_worker_guard.starttime,
                           ADTR_E2E_EXPORT_WORKER_NONCE=export_worker_nonce,
                           ADTR_E2E_EXPORT_WORKER_BINARY=str(Path("bin/adtr").resolve()),
                           ADTR_E2E_EXPORT_WORKER_APPLICATION_NAME=export_worker_app,
                           ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")
                if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:
                    asset_kind = {GROUP_RUNNING_CANCEL_SUITE: "group", COMPUTER_RUNNING_CANCEL_SUITE: "computer"}.get(args.suite, "user")
                    running_cancel_guard = RunningExportCancelGuard(worker, export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, env["ADTR_DATABASE_URL"], asset_kind=asset_kind)
                    running_cancel_guard.start()
                    env.update(ADTR_E2E_RUNNING_CANCEL_GUARD_URL=running_cancel_guard.url,
                               ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN=running_cancel_guard.token)
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
            diagnostics.phase("worker-ready")
        if args.suite in {"system", "operational-logs", "messages-bulk"}:
            env["ADTR_E2E_WORKER_PID"] = str(worker.pid)
            if args.suite == "messages-bulk":
                env["ADTR_E2E_HARNESS_PID"] = str(os.getpid())
        # Trace/screenshot settings do not disable Playwright's automatic failure
        # DOM snapshot. Suppress that separate artifact so proof fields cannot
        # enter an error-context prompt; explicit safe screenshots remain intact.
        env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"
        diagnostics.phase("browser-start")
        subprocess.run(command, env=env, check=True, timeout=930 if args.suite in KIND_RUNNING_CANCEL_SUITES else 930 if args.suite == RUNNING_CANCEL_SUITE else 930 if args.suite in COMPUTER_EXPORT_SUITES else 930 if args.suite in GROUP_EXPORT_SUITES else 930 if args.suite in EXPORT_TERMINAL_SUITES else 930 if args.suite == "user-asset-export-v3" else 660 if args.suite == "account-references" else 930 if args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | {"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", "directory-v2", "directory-v3-backend", "directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "directory-v3"} else 600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", "operations", "credential-use", "account-references", "operational-logs", "account-reference-barrier"} else 480 if args.suite == "system" else 420)
        if args.suite == "user-asset-export-v3":
            validate_user_export()
        if export_worker_guard is not None:
            export_worker_guard.check()
        if running_cancel_guard is not None:
            running_cancel_guard.check()
            if args.suite == GROUP_RUNNING_CANCEL_SUITE:
                validate_group_running_cancel()
            elif args.suite == COMPUTER_RUNNING_CANCEL_SUITE:
                validate_computer_running_cancel()
            else:
                validate_running_cancel()
        if args.suite in EXPORT_TERMINAL_SUITES:
            validate_terminal_export(args.suite)
        if args.suite == "group-asset-export-v3":
            validate_group_export()
        if args.suite in GROUP_EXPORT_TERMINAL_SUITES:
            validate_group_terminal(args.suite)
        if args.suite == "computer-asset-export-v3":
            validate_computer_export()
        if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:
            validate_computer_terminal(args.suite)
        validate_asset_refresh_evidence(args.suite)
        if args.suite == "group-members-v4":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-empty":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_empty_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-pagination":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_pagination_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-refresh":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_refresh_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        diagnostics.phase("browser-pass")
        chain = "real browser -> API -> worker/PostgreSQL" if worker is not None else "real browser -> API -> PostgreSQL"
    finally:
        try:
            diagnostics.snapshot(server, worker)
        except Exception:
            # Optional evidence must never prevent owned cleanup or mask the
            # original browser failure. Never print diagnostic exception text.
            diagnostics.report["collection_errors"].append("snapshot")
        try:
            shutdown_error = None
            if running_cancel_guard is not None:
                try:
                    running_cancel_guard.close()
                except (OSError, RuntimeError):
                    shutdown_error = RuntimeError("running export worker cleanup failed")
            if export_worker_guard is not None:
                try:
                    export_worker_guard.close()
                except (OSError, RuntimeError):
                    shutdown_error = RuntimeError("owned export worker cleanup failed")
            if account_barrier is not None:
                try:
                    account_barrier.close()
                except Exception as error:
                    shutdown_error = error
            for label, process in [("worker", worker), ("API", server)]:
                if process is None:
                    continue
                if args.suite == "messages-bulk" and label == "worker" and process.poll() is None:
                    # A failed browser may leave our own child stopped. Resume before shutdown.
                    process.send_signal(signal.SIGCONT)
                process_record = diagnostics.report["processes"]["api" if label == "API" else "worker"]
                process.terminate()
                try:
                    code = process.wait(timeout=30)
                    process_record["after_cleanup"] = code
                    if code != 0:
                        shutdown_error = RuntimeError(f"{label} did not exit successfully")
                except subprocess.TimeoutExpired:
                    process_record["forced_kill"] = True
                    process.kill()
                    process_record["after_cleanup"] = process.wait(timeout=10)
                    shutdown_error = RuntimeError(f"{label} did not stop gracefully")
            if ldap_fixture is not None:
                try:
                    ldap_fixture.close()
                except Exception as error:
                    shutdown_error = error
            try:
                panicked = http_handler_panicked(logs)
                diagnostics.report["http_panic"] = panicked
            except (OSError, UnicodeError, ValueError):
                shutdown_error = RuntimeError(
                    "browser acceptance runtime diagnostics could not be checked"
                )
            else:
                if panicked:
                    shutdown_error = RuntimeError(
                        "browser acceptance runtime observed an HTTP handler panic; raw diagnostics withheld"
                    )
            finally:
                logs.close()
            if message_fixture is not None:
                message_fixture.cleanup()
            if creation_attempted:
                remove_owned_container(name, owner)
            if shutdown_error is not None:
                raise shutdown_error
        finally:
            try:
                diagnostics.phase("after-cleanup")
                diagnostics.write(diagnostic_dir / "runtime.json")
            except Exception:
                print("Safe runtime diagnostics could not be retained", file=sys.stderr)
    if args.suite == "messages":
        # Additive sequential acceptance, fresh owned database; never recurse from bulk.
        subprocess.run([sys.executable, __file__, "--suite", "messages-bulk"], check=True)
    print(f"{args.suite} (expired={args.expired}): {chain} acceptance PASS")



if __name__ == "__main__":
    main()
