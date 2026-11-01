"""Dedicated synthetic-only PostgreSQL/TCP/browser acceptance; owns all resources.

Requires prebuilt bin/adtr, web/dist and Playwright Chromium. Never accepts a
caller database URL and never touches AD. Run from repository root.
"""
import base64
from concurrent.futures import ThreadPoolExecutor
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

from ci_postgres_image import postgres_image_args
from test_integration import IMAGE

ROOT = Path(__file__).resolve().parents[1]
API = "/api/simulation/events"
ROLE = "simulation_e2e_role_00001"
SEED = r'''
package main
import (
 "context"
 "os"
 "github.com/jackc/pgx/v5"
 "github.com/yunpiao/adtr/internal/securityevents"
 "github.com/yunpiao/adtr/internal/simulationevents"
)
func main() {
 ctx:=context.Background()
 c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL")); if e!=nil {panic(e)}; defer c.Close(ctx)
 tx,e:=c.Begin(ctx); if e!=nil {panic(e)}; defer tx.Rollback(ctx)
 for _,pair:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}} {
  s:=securityevents.Scope{Tenant:pair[0],Domain:pair[1],Read:true,Manage:true}
  p:=securityevents.Provenance{Contract:securityevents.ContractVersion,Fixture:"SIMULATED-e2e-only",Synthetic:true}
  _,e=simulationevents.SeedFixtureTx(ctx,tx,s,simulationevents.Fixture{ID:"simulation-dataset",Provenance:p,
   Events:[]securityevents.RawEvent{{ID:"synthetic-event",Tenant:s.Tenant,Domain:s.Domain,Kind:"login_failure",Actor:"synthetic-actor",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:"2026-01-01T00:00:00Z",Provenance:p}},
   Cases:[]simulationevents.FixtureCase{{ID:"http-case",EvidenceIDs:[]string{"synthetic-event"}},{ID:"browser-case",EvidenceIDs:[]string{"synthetic-event"}}}})
  if e!=nil {panic(e)}
 }
 if e=tx.Commit(ctx); e!=nil {panic(e)}
}
'''


def run(args, **kwargs):
    return subprocess.run(args, check=True, timeout=kwargs.pop("timeout", 120), **kwargs)


# Error bodies can originate in an intermediary. Log only this closed vocabulary.
SAFE_ERROR_CODES = frozenset({"invalid_input", "forbidden", "unauthenticated", "invalid_credentials",
                              "mfa_required", "rate_limited", "internal", "not_found", "simulation_disabled",
                              "simulation_unavailable", "schema_incompatible", "revision_conflict"})
CLIENT_STAGES = frozenset({"anonymous", "admin", "operator", "other-tenant", "race-0", "race-1"})


def safe_response_code(raw):
    if len(raw) > 65536:
        return "oversized-response"
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeError):
        return "non-json-response"
    code = value.get("error") if isinstance(value, dict) else None
    return code if isinstance(code, str) and code in SAFE_ERROR_CODES else "unrecognized-response"


PREPARATION_PHASES = {"migrate-first": "migrate", "migrate-repeat": "migrate", "bootstrap": "bootstrap"}
PREPARATION_ERRORS = {
    b"migration connection failed": "database-connect",
    b"migration transaction failed": "database-transaction",
    b"migration statement failed": "migration-infrastructure",
    b"unsupported migration target": "unsupported-target",
    b"unsupported schema version; refusing migration": "unsupported-schema",
    b"simulation events schema migration failed": "simulation-schema",
    b"schema version update failed": "schema-version-update",
    b"migration commit failed": "migration-commit",
    b"invalid bootstrap input": "bootstrap-input",
    b"bootstrap database unavailable": "database-connect",
    b"bootstrap refused: accounts already exist": "bootstrap-existing-accounts",
    b"bootstrap failed": "bootstrap-write",
    b"invalid database configuration": "database-configuration",
}


def safe_preparation_code(stderr):
    # Only a complete, exact known CLI error is classified. Extra lines, SQL,
    # addresses, credentials or unexpected output are never copied to artifacts.
    if not isinstance(stderr, bytes) or len(stderr) > 1024:
        return "unclassified"
    return PREPARATION_ERRORS.get(stderr.strip(), "unclassified")


def prepare_database(phase, env):
    if phase not in PREPARATION_PHASES:
        raise ValueError("invalid database preparation phase")
    try:
        run(["./bin/adtr", "-mode", PREPARATION_PHASES[phase]], env=env, capture_output=True)
    except subprocess.CalledProcessError as error:
        raise RuntimeError(f"database preparation failed: phase={phase}; exit={error.returncode}; code={safe_preparation_code(error.stderr)}") from None
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"database preparation failed: phase={phase}; exit=none; code=timeout") from None
    except OSError:
        raise RuntimeError(f"database preparation failed: phase={phase}; exit=none; code=launch-failed") from None


class Client:
    def __init__(self, base, stage="anonymous"):
        if stage not in CLIENT_STAGES:
            raise ValueError("invalid synthetic client stage")
        self.base, self.csrf, self.stage = base, "", stage
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, path, body=None, want=200, csrf=True):
        headers = {"Origin": self.base, "Content-Type": "application/json"}
        if csrf and self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        request = urllib.request.Request(self.base + path, headers=headers,
                                         data=None if body is None else json.dumps(body).encode())
        try:
            response = self.opener.open(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            status = response.status
            raw = response.read(65537) if status != want else response.read()
            response_type = "json" if response.headers.get_content_type() == "application/json" else "non-json"
        proxy_configured = any(isinstance(handler, urllib.request.ProxyHandler) and bool(handler.proxies)
                               for handler in self.opener.handlers)
        request_type = "json" if request.get_header("Content-type") == "application/json" else "other"
        method = request.get_method() if request.get_method() in ("GET", "POST") else "other"
        assert status == want, (f"{path.split('?')[0]}: HTTP {status}, expected {want}; stage={self.stage}; "
                                f"code={safe_response_code(raw)}; responseType={response_type}; "
                                f"method={method}; requestType={request_type}; proxyConfigured={proxy_configured}")
        value = json.loads(raw)
        if isinstance(value, dict) and "csrfToken" in value:
            self.csrf = value["csrfToken"]
        return value


def main():
    os.chdir(ROOT)
    # CI cancellation still tears down only this harness-owned process/container.
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt("simulation acceptance cancelled")
    signal.signal(signal.SIGTERM, cancelled)
    evidence = ROOT / "artifacts/simulation-events"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": []}
    name = "adtr-simulation-e2e-" + uuid.uuid4().hex[:12]
    owner = uuid.uuid4().hex
    created, server = False, None
    log = None
    # Do not inherit deployment/database/LDAP/worker inputs into isolated tests.
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}
    password, account_password = secrets.token_hex(24), secrets.token_urlsafe(30)
    env["POSTGRES_PASSWORD"] = password

    def sql(statement):
        return subprocess.check_output(["docker", "exec", "-i", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-At"], input=statement, text=True, timeout=30).strip()

    def stop():
        nonlocal server
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=20)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=10)
            server = None

    def start():
        nonlocal server
        runtime = {k: v for k, v in env.items() if not k.startswith("ADTR_BOOTSTRAP_") and not k.startswith("ADTR_E2E_") and k != "POSTGRES_PASSWORD"}
        server = subprocess.Popen(["./bin/adtr", "-mode", "api"], env=runtime, stdout=log, stderr=log)
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError("isolated API exited before readiness")
            try:
                with urllib.request.urlopen(base + "/readyz", timeout=1) as response:
                    if response.status == 200:
                        return
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(.1)
        raise RuntimeError("isolated API readiness timed out")

    try:
        if not shutil.which("docker"):
            report["status"] = "not-run"
            raise RuntimeError("Docker unavailable; real PostgreSQL/browser acceptance must run in dedicated CI")
        assert (ROOT / "bin/adtr").is_file() and (ROOT / "web/dist/index.html").is_file(), "build API and frontend first"
        run(["docker", "run", "--detach", "--rm", "--name", name, "--label", "adtr.simulation.owner=" + owner,
             "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e", "-p", "127.0.0.1::5432", *postgres_image_args(IMAGE)], env=env, capture_output=True)
        created = True
        # postgres entrypoint temporarily accepts Unix-socket connections while
        # initializing. Require its final TCP listener, used by the mapped port.
        for _ in range(100):
            if subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-U", "postgres", "-d", "adtr_e2e"], capture_output=True, timeout=5).returncode == 0:
                break
            time.sleep(.2)
        else:
            raise RuntimeError("owned PostgreSQL readiness timed out")
        address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True, timeout=10).strip()
        assert address.startswith("127.0.0.1:")
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env.update(ADTR_DATABASE_URL=f"postgres://postgres:{password}@{address}/adtr_e2e?sslmode=disable",
                   ADTR_AUTH_KEY=base64.b64encode(secrets.token_bytes(32)).decode(), ADTR_DEVELOPMENT="true",
                   ADTR_ORIGIN=base, ADTR_LISTEN_ADDR=f"127.0.0.1:{port}", ADTR_WEB_DIR=str(ROOT / "web/dist"),
                   ADTR_BOOTSTRAP_USERNAME="simulation-admin", ADTR_BOOTSTRAP_PASSWORD=account_password,
                   ADTR_SIMULATION_EVENTS_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
        for phase in ("migrate-first", "migrate-repeat", "bootstrap"):
            prepare_database(phase, env)
        assert sql("SELECT version FROM adtr.schema_version") == "24"
        report["checks"].append("real PostgreSQL migration and repeated migration")
        sql(f"""
BEGIN;
UPDATE adtr.users SET must_change=false WHERE username='simulation-admin';
INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES ('default','{ROLE}','Synthetic simulation operator'),('other','{ROLE}','Other synthetic operator');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT tenant_id,id,mark,true,mark='simulation_events' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_events'),('domains')) p(mark);
INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change)
 SELECT t.tenant,t.username,u.password_hash,'viewer','{ROLE}',false FROM adtr.users u CROSS JOIN (VALUES('default','simulation-operator'),('other','simulation-other')) t(tenant,username) WHERE u.username='simulation-admin';
INSERT INTO adtr.resource_domains(tenant_id,id,name) VALUES('default','simulation-domain-a','SIMULATED domain A'),('default','simulation-domain-b','SIMULATED domain B'),('other','simulation-domain-a','SIMULATED other tenant');
INSERT INTO adtr.resource_groups(tenant_id,id,name) VALUES('default','simulation-group','Synthetic group'),('other','simulation-group','Synthetic group');
INSERT INTO adtr.resource_group_members(tenant_id,group_id,domain_id) VALUES('default','simulation-group','simulation-domain-a'),('other','simulation-group','simulation-domain-a');
INSERT INTO adtr.resource_role_groups(tenant_id,role_id,group_id) VALUES('default','{ROLE}','simulation-group'),('other','{ROLE}','simulation-group');
INSERT INTO adtr.domain_connections(tenant_id,domain_id,canonical_domain,dc_hostname,transport_mode,port,credential_mode) VALUES('default','simulation-domain-a','simulation.example.test','dc.simulation.example.test','starttls','389','unconfigured');
COMMIT;
""")
        with tempfile.TemporaryDirectory(prefix="simulation-fixture-", dir=ROOT) as tmp:
            source = Path(tmp) / "main.go"
            source.write_text(SEED)
            run(["go", "run", str(source)], env=env, capture_output=True)
        log = tempfile.TemporaryFile()  # Raw server logs are deliberately not published.
        start()
        anonymous, admin, client, other = [Client(base, stage) for stage in ("anonymous", "admin", "operator", "other-tenant")]
        for c, username in ((admin, "simulation-admin"), (client, "simulation-operator"), (other, "simulation-other")):
            c.call("/api/auth/login", {"username": username, "password": account_password})
        listing = API + "/datasets?domainId=simulation-domain-a&offset=0&limit=100"
        anonymous.call(listing, want=401)
        admin.call(listing, want=403)
        result = client.call(listing)
        assert result["simulation"] and len(result["datasets"]) == 1
        assert result["datasets"][0]["fixture"] == "SIMULATED-e2e-only"
        client.call(listing.replace("domain-a", "domain-b"), want=404)
        detail_path = API + "/detail?domainId=simulation-domain-a&datasetId=simulation-dataset&caseId=http-case&historyOffset=0&historyLimit=100"
        body = dict(domainId="simulation-domain-a", datasetId="simulation-dataset", caseId="http-case", operationId="http-operation", action="mark_suspicious", reason="SIMULATED HTTP review", expectedRevision=1)
        client.call(API + "/transition", body, want=403, csrf=False)
        first = client.call(API + "/transition", body)
        assert first["case"]["Revision"] == 2
        assert client.call(API + "/transition", body) == first
        assert client.call(API + "/operation?domainId=simulation-domain-a&datasetId=simulation-dataset&operationId=http-operation") == first
        client.call(API + "/transition", dict(body, reason="different"), want=409)
        other.call(API + "/operation?domainId=simulation-domain-a&datasetId=simulation-dataset&operationId=http-operation", want=404)
        assert other.call(detail_path)["case"]["Revision"] == 1
        sql(f"UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_events'")
        client.call(API + "/transition", dict(body, operationId="read-only"), want=403)
        assert client.call(detail_path)["case"]["Revision"] == 2
        sql(f"UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_events'")
        # Separate authenticated clients race on one revision; exactly one wins.
        racers = [Client(base, "race-0"), Client(base, "race-1")]
        for racer in racers:
            racer.call("/api/auth/login", {"username": "simulation-operator", "password": account_password})
        def race(index):
            try:
                racers[index].call(API + "/transition", dict(body, operationId=f"race-{index}", action="mark_benign", expectedRevision=2))
                return 200
            except AssertionError as error:
                assert "HTTP 409" in str(error)
                return 409
        with ThreadPoolExecutor(max_workers=2) as executor:
            assert sorted(executor.map(race, (0, 1))) == [200, 409]
        before = client.call(detail_path)
        assert before["case"]["Revision"] == 3 and before["historyTotal"] == 2
        stop()
        start()
        assert client.call(detail_path) == before
        assert sql("SELECT count(*) FROM adtr.simulation_event_receipts WHERE tenant_id='default'") == "2"
        report["checks"].append("TCP auth, tenant/domain isolation, default admin denial, CSRF, read-only, revision race, receipt replay, restart persistence")
        # Existing auth integration uses a separate fresh database and injects
        # transactional audit failure, proving complete rollback and lost-response recovery.
        run(["go", "test", "-tags=integration", "./internal/auth", "-run", "^TestSimulationEventHTTP", "-count=1"],
            env=dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]), timeout=240)
        report["checks"].append("auth PostgreSQL rollback and lost-response recovery integration")
        run(["go", "test", "-tags=integration", "./internal/simulationevents", "-run", "^TestPostgresAtomicSyntheticState$", "-count=1"],
            env=dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]), timeout=240)
        report["checks"].append("PostgreSQL adapter atomic state, immutable fixtures and receipts")
        browser_env = dict({k: v for k, v in env.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}, ADTR_E2E_BASE_URL=base, ADTR_E2E_USERNAME="simulation-operator", ADTR_E2E_PASSWORD=account_password)
        run(["npx", "--no-install", "playwright", "test", "--config", "simulation.playwright.config.ts", "simulation-e2e/simulation-events.spec.ts"], cwd=ROOT / "web", env=browser_env, timeout=240)
        report["checks"].append("real browser synthetic list/detail/mark/close/reload/duplicate/late-response/screenshots")
        report["status"] = "passed"
    except BaseException:
        if report["status"] != "not-run":
            report["status"] = "failed"
        raise
    finally:
        stop()
        if log:
            log.close()
        if created:
            actual = subprocess.check_output(["docker", "inspect", "--format", '{{index .Config.Labels "adtr.simulation.owner"}}', name], text=True, timeout=10).strip()
            if actual != owner:
                raise RuntimeError("refusing cleanup: owned container identity changed")
            run(["docker", "rm", "--force", name], capture_output=True)
        (evidence / "result.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
