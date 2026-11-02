"""Dedicated synthetic-only PostgreSQL/TCP/browser acceptance; owns all resources.

Requires prebuilt bin/adtr, web/dist and Playwright Chromium. Never accepts a
caller database URL and never touches AD. Run from repository root.
"""
import base64
import json
import os
from pathlib import Path
import secrets
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import urllib.parse
import uuid

from ci_postgres_image import postgres_image_args
from test_integration import IMAGE

from test_simulation_events_integration import Client

ROOT = Path(__file__).resolve().parents[1]

API = "/api/simulation/detection"
ROLE = "simulation_e2e_role_0001"
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
   Events:[]securityevents.RawEvent{{ID:"synthetic-event",Tenant:s.Tenant,Domain:s.Domain,Kind:"login_failure",Actor:"synthetic-actor",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:"2026-01-01T00:00:00Z",Provenance:p},{ID:"browser-event",Tenant:s.Tenant,Domain:s.Domain,Kind:"service_access",Actor:"synthetic-browser",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:"2026-01-01T00:00:01Z",Provenance:p}},
   Cases:[]simulationevents.FixtureCase{{ID:"http-case",EvidenceIDs:[]string{"synthetic-event"}},{ID:"browser-case",EvidenceIDs:[]string{"synthetic-event"}}}})
  if e!=nil {panic(e)}
 }
 if e=tx.Commit(ctx); e!=nil {panic(e)}
}
'''


def run(args, **kwargs):
    return subprocess.run(args, check=True, timeout=kwargs.pop("timeout", 120), **kwargs)


def run_go_integration(package, selector, expected, env):
    # A successful go process with no matching tests (or only skips) is not evidence.
    completed = run(["go", "test", "-json", "-tags=integration", package, "-run", selector, "-count=1"],
                    env=env, timeout=240, capture_output=True, text=True)
    events = [json.loads(line) for line in completed.stdout.splitlines() if line.strip()]
    passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
    skipped = {event.get("Test") for event in events if event.get("Action") == "skip"}
    assert expected in passed and not skipped, "required PostgreSQL integration did not execute fully"



def worker_environment(env):
    """Reserve a distinct loopback candidate while the API is already listening."""
    runtime = {k: v for k, v in env.items()
               if not k.startswith(("ADTR_BOOTSTRAP_", "ADTR_E2E_")) and k != "POSTGRES_PASSWORD"}
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        address = "127.0.0.1:" + str(sock.getsockname()[1])
    # Explicit port zero is rejected by production configuration. Never inherit
    # the API listener, including when this worker is restarted.
    if address == env["ADTR_LISTEN_ADDR"]:
        raise RuntimeError("isolated worker listener collided with API")
    runtime["ADTR_LISTEN_ADDR"] = address
    return runtime, "http://" + address


def wait_for_worker(worker, base, report, read_result=None):
    """Readiness consumes the existing 300 result-poll iterations, not a retry budget."""
    ready = False

    def exited():
        code = worker.poll()
        if code is not None:
            # A fixed vocabulary and integer exit status are safe to retain.
            # Raw process logs may contain authentication/database material.
            report["workerDiagnostic"] = {"phase": "worker", "reason": "process-exited", "exitCode": code}
            raise RuntimeError("isolated worker exited")

    for _ in range(300):
        exited()
        try:
            with urllib.request.urlopen(base + "/readyz", timeout=1) as response:
                ready = response.status == 200
        except (OSError, urllib.error.URLError):
            ready = False
        if ready:
            result = read_result() if read_result is not None else None
            exited()
            if read_result is None or result["task"]["state"] in ("succeeded", "failed", "cancelled", "dead_letter"):
                return result
        time.sleep(.1)
    report["workerDiagnostic"] = {"phase": "worker", "reason": "task-timeout" if ready else "readiness-timeout"}
    raise RuntimeError("real simulation worker result timed out" if ready else "isolated worker readiness timed out")


def main():
    os.chdir(ROOT)
    # CI cancellation still tears down only this harness-owned process/container.
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt("simulation acceptance cancelled")
    signal.signal(signal.SIGTERM, cancelled)
    evidence = ROOT / "artifacts/simulation-detection"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": []}
    name = "adtr-simulation-detection-e2e-" + uuid.uuid4().hex[:12]
    owner = uuid.uuid4().hex
    created, server, worker = False, None, None
    worker_base = None
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

    def stop_worker():
        nonlocal worker
        if worker is not None:
            worker.terminate()
            try:
                worker.wait(timeout=20)
            except subprocess.TimeoutExpired:
                worker.kill()
                worker.wait(timeout=10)
            worker = None

    def start_worker():
        nonlocal worker, worker_base
        runtime, worker_base = worker_environment(env)
        worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=runtime, stdout=log, stderr=log)

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
                   ADTR_SIMULATION_EVENTS_ENABLED="true", ADTR_SIMULATION_DETECTION_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
        for mode in ("migrate", "migrate", "bootstrap"):
            run(["./bin/adtr", "-mode", mode], env=env, capture_output=True)
        expected_schema = re.search(r"const Current = (\d+)", (ROOT / "internal/schemaversion/version.go").read_text()).group(1)
        assert sql("SELECT version FROM adtr.schema_version") == expected_schema
        report["checks"].append("real PostgreSQL migration and repeated migration")
        sql(f"""
BEGIN;
UPDATE adtr.users SET must_change=false WHERE username='simulation-admin';
-- Disposable synthetic tenants must satisfy the same live entitlement gates as the UI.
INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name)
 SELECT tenant,10,extract(epoch FROM (clock_timestamp() + interval '30 days'))::bigint,uid,name
 FROM (VALUES('default','synthetic-default','SIMULATED default'),('other','synthetic-other','SIMULATED other')) AS t(tenant,uid,name);
INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES ('default','{ROLE}','Synthetic simulation operator'),('other','{ROLE}','Other synthetic operator');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT tenant_id,id,mark,true,mark='simulation_detection' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_detection'),('domains')) p(mark);
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
        # Exercise the actual browser prerequisite APIs before entering Playwright.
        menu = client.call("/api/access/menu")
        grants = {item["mark"]: item["auth"] for item in menu["menu"]}
        assert grants["domains"]["readable"] and grants["simulation_detection"]["readable"]
        selection = client.call("/api/domain-selection?pageIdx=1&pageSize=20")
        assert len(selection["List"]) == 1
        choice = selection["List"][0]
        assert choice["domainId"] == "simulation-domain-a" and choice["domain"] == "simulation.example.test"
        assert not choice["credentialConfigured"] and choice["source"] == "configured_connection"
        resolved = client.call("/api/domain-selection/resolve?" + urllib.parse.urlencode({
            "domainId": choice["domainId"], "expectedRevision": choice["revision"],
            "expectedCredentialRevision": choice["credentialRevision"]}))
        assert resolved["selection"] == choice
        report["checks"].append("live tenant entitlement, explicit menu grants and saved-source list/resolve prerequisites")
        listing = API + "/templates?domainId=simulation-domain-a"
        anonymous.call(listing, want=401)
        admin.call(listing, want=403)
        assert client.call(listing)["templates"] == []
        client.call(listing.replace("domain-a", "domain-b"), want=404)
        domain = dict(domainId="simulation-domain-a")
        rules = [{"id": "rule-1", "enabled": True, "combine": "all",
                  "conditions": [{"field": "event.category", "operator": "exists", "values": []}]}]
        body = dict(domain, templateId="http-template", name="SIMULATED HTTP", operationId="http-create", rules=rules)
        client.call(API + "/create", body, want=403, csrf=False)
        client.call(API + "/create", dict(body, actorId=1), want=400)
        first_receipt = client.call(API + "/create", body)
        first = first_receipt["template"]
        assert first["version"] == 1 and first["simulation"]
        assert client.call(API + "/create", body) == first_receipt
        receipt_path = API + "/receipt?domainId=simulation-domain-a&operationId=http-create"
        receipt = client.call(receipt_path)
        assert receipt["template"] == first
        client.call(API + "/create", dict(body, name="different"), want=409)
        other.call(receipt_path, want=404)
        assert other.call(listing)["templates"] == []
        run_body = dict(domain, templateId="http-template", datasetId="simulation-dataset", templateVersion=1, operationId="http-run")
        submitted = client.call(API + "/run", run_body)
        task_id = submitted["submission"]["task"]["taskUUID"]
        assert submitted["submission"]["task"]["state"] == "queued"
        replay = client.call(API + "/run", run_body)
        assert replay["submission"]["task"]["taskUUID"] == task_id
        task_path = API + "/task?domainId=simulation-domain-a&taskUUID=" + task_id
        other.call(task_path, want=404)
        client.call(task_path.replace("domain-a", "domain-b"), want=404)
        updated = client.call(API + "/update", dict(body, operationId="http-update", expectedVersion=1, name="SIMULATED HTTP v2"))
        updated = updated["template"]
        assert updated["version"] == 2 and updated["hash"] != first["hash"]
        client.call(API + "/update", dict(body, operationId="stale-update", expectedVersion=1), want=409)
        # Queue a second real task and cancel before a worker exists: deterministic,
        # no sleeps, fabricated terminal rows or special execution hooks.
        cancel_submit = client.call(API + "/run", dict(run_body, templateVersion=2, operationId="http-run-cancel"))
        cancel_body = dict(domain, taskUUID=cancel_submit["submission"]["task"]["taskUUID"], operationId="http-cancel")
        cancelled = client.call(API + "/cancel", cancel_body)
        assert cancelled["task"]["state"] == "cancelled"
        assert client.call(API + "/cancel", cancel_body) == cancelled
        sql(f"UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_detection'")
        client.call(API + "/run", dict(run_body, templateVersion=2, operationId="read-only"), want=403)
        # Owner-scoped tasks pin the actor's authorization epoch. Retained
        # module read permission permits templates, not a prior-epoch task.
        client.call(task_path, want=404)
        assert client.call(listing)["templates"][0]["version"] == 2
        sql(f"UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_detection'")
        client.call(task_path, want=404)  # Regrant creates another epoch, not revival.
        assert client.call(listing)["templates"][0]["version"] == 2
        # Submission pins auth epoch. Run only after the read-only authorization
        # scenario, with a newly submitted immutable version-2 task.
        active = client.call(API + "/run", dict(run_body, templateVersion=2, operationId="http-active"))
        active_id = active["submission"]["task"]["taskUUID"]
        active_path = API + "/task?domainId=simulation-domain-a&taskUUID=" + active_id
        latest = client.call(API + "/update", dict(body, operationId="http-update-v3", expectedVersion=2, name="SIMULATED HTTP v3"))
        latest = latest["template"]
        assert latest["version"] == 3
        start_worker()
        completed = wait_for_worker(worker, worker_base, report, lambda: client.call(active_path))
        assert completed["task"]["state"] == "succeeded"
        result = completed["task"]["result"]
        assert result["simulation"] and result["templateVersion"] == 2
        assert result["templateHash"] == updated["hash"] and result["eventCount"] == 2
        assert result["matches"], "real worker must evaluate synthetic evidence"
        assert completed["task"]["attempt"] == 1
        archived = client.call(API + "/archive", dict(domain, templateId="http-template", operationId="http-archive", expectedVersion=3))
        assert archived["template"]["archived"]
        client.call(API + "/run", dict(run_body, templateVersion=3, operationId="archived-run"), want=409)
        assert client.call(active_path)["task"]["result"] == result
        stop_worker()
        stop()
        start()
        start_worker()
        wait_for_worker(worker, worker_base, report)
        assert client.call(active_path) == completed
        assert client.call(receipt_path) == receipt
        assert client.call(API + "/run", dict(run_body, templateVersion=2, operationId="http-active"))["submission"]["task"]["taskUUID"] == active_id
        report["checks"].append("real API/worker template CRUD, pinned result, queued cancel, replay, restart, CSRF and tenant/domain/write denial")
        run_go_integration("./internal/auth", "^TestSimulationDetectionHTTPAuthorizationRecoveryAndAuditAtomicity$", "TestSimulationDetectionHTTPAuthorizationRecoveryAndAuditAtomicity",
                           dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        run_go_integration("./internal/simulationdetection", "^TestPostgresSimulationDetection$", "TestPostgresSimulationDetection",
                           dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        report["checks"].append("named PostgreSQL integration cases executed without skip")
        browser_env = dict({k: v for k, v in env.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}, ADTR_E2E_BASE_URL=base, ADTR_E2E_USERNAME="simulation-operator", ADTR_E2E_PASSWORD=account_password)
        run(["npx", "--no-install", "playwright", "test", "--config", "simulation-detection.playwright.config.ts", "simulation-detection-e2e/detection.spec.ts"], cwd=ROOT / "web", env=browser_env, timeout=240)
        report["checks"].append("real browser SIMULATED template creation, edit, task execution, recovery and reload")
        report["status"] = "passed"
    except BaseException:
        if report["status"] != "not-run":
            report["status"] = "failed"
        raise
    finally:
        stop_worker()
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
