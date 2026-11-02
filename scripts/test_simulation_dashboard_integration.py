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
from test_simulation_detection_integration import worker_environment, wait_for_worker

ROOT = Path(__file__).resolve().parents[1]

API = "/api/simulation/dashboard"
ROLE = "simulation_e2e_role_0001"
SEED = r'''
package main
import (
 "context"
 "os"
 "time"
 "github.com/jackc/pgx/v5"
 "github.com/yunpiao/adtr/internal/securityevents"
 "github.com/yunpiao/adtr/internal/simulationevents"
)
func main() {
 ctx:=context.Background()
 c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL")); if e!=nil {panic(e)}; defer c.Close(ctx)
 tx,e:=c.Begin(ctx); if e!=nil {panic(e)}; defer tx.Rollback(ctx)
 observed:=time.Now().UTC().Add(-2*time.Minute)
 for _,pair:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}} {
  s:=securityevents.Scope{Tenant:pair[0],Domain:pair[1],Read:true,Manage:true}
  p:=securityevents.Provenance{Contract:securityevents.ContractVersion,Fixture:"SIMULATED-e2e-only",Synthetic:true}
  _,e=simulationevents.SeedFixtureTx(ctx,tx,s,simulationevents.Fixture{ID:"simulation-dataset",Provenance:p,
   Events:[]securityevents.RawEvent{{ID:"synthetic-event",Tenant:s.Tenant,Domain:s.Domain,Kind:"login_failure",Actor:"synthetic-actor",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:observed.Format(time.RFC3339Nano),Provenance:p},{ID:"browser-event",Tenant:s.Tenant,Domain:s.Domain,Kind:"service_access",Actor:"synthetic-browser",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:observed.Add(time.Second).Format(time.RFC3339Nano),Provenance:p}},
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
    try:
        completed = run(["go", "test", "-json", "-tags=integration", package, "-run", selector, "-count=1"],
                        env=env, timeout=240, capture_output=True, text=True)
    except subprocess.CalledProcessError as failure:
        # Retain only source locations and SQLSTATE codes, never raw test output,
        # connection details, fixture rows, credentials or arbitrary identifiers.
        locations, states = set(), set()
        for line in (failure.stdout or "").splitlines():
            try:
                event = json.loads(line)
            except (ValueError, TypeError):
                continue
            if not isinstance(event, dict):
                continue
            output = event.get("Output", "")
            if not isinstance(output, str):
                continue
            locations.update(re.findall(r"\b(integration_test\.go):([0-9]{1,6}):", output))
            states.update(re.findall(r"\bSQLSTATE ([0-9A-Z]{5})\b", output))
        print(json.dumps({"phase": "named-postgres-test", "exitCode": failure.returncode,
                          "locations": [f"{name}:{line}" for name, line in sorted(locations)[:16]],
                          "sqlStates": sorted(states)[:16]}), flush=True)
        raise RuntimeError("required PostgreSQL integration failed; see closed diagnostic") from None
    events = [json.loads(line) for line in completed.stdout.splitlines() if line.strip()]
    passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
    skipped = {event.get("Test") for event in events if event.get("Action") == "skip"}
    assert expected in passed and not skipped, "required PostgreSQL integration did not execute fully"


def main():
    os.chdir(ROOT)
    # CI cancellation still tears down only this harness-owned process/container.
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt("simulation acceptance cancelled")
    signal.signal(signal.SIGTERM, cancelled)
    evidence = ROOT / "artifacts/simulation-dashboard"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": []}
    name = "adtr-simulation-dashboard-e2e-" + uuid.uuid4().hex[:12]
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
                   ADTR_SIMULATION_EVENTS_ENABLED="true", ADTR_SIMULATION_RISK_ENABLED="true",
                   ADTR_SIMULATION_DETECTION_ENABLED="true", ADTR_SIMULATION_DASHBOARD_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
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
 SELECT tenant_id,id,mark,true,mark<>'domains' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_events'),('simulation_risk'),('simulation_detection'),('domains')) p(mark);
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
        assert grants["domains"]["readable"] and grants["simulation_risk"]["readable"]
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
        query = dict(domainId="simulation-domain-a", datasetId="simulation-dataset", subject="synthetic-actor", window="24h")
        path = API + "/summary?" + urllib.parse.urlencode(query)
        anonymous.call(path, want=401)
        admin.call(path, want=403)
        client.call(path.replace("domain-a", "domain-b"), want=404)
        client.call(path + "&window=1h", want=400)
        client.call(path + "&actorId=1", want=400)
        client.call(path, {}, want=405)
        missing_subject = client.call(API + "/summary?" + urllib.parse.urlencode({k: v for k, v in query.items() if k != "subject"}))
        assert missing_subject["risk"]["status"] == "not_selected"
        initial = client.call(path)
        assert initial["contract"] == "adtr.dashboard.simulated.v1"
        assert initial["simulation"] and initial["productAccepted"] is False
        assert initial["coverage"]["complete"] and initial["coverage"]["eventCount"] == 2
        assert initial["cases"] == {"scope": "dataset_current", "total": 2, "open": 2, "closed": 0,
                                    "marks": {"unmarked": 2, "benign": 0, "suspicious": 0}}
        assert initial["eventActivity"]["total"] == 2
        assert {x["kind"]: x["count"] for x in initial["eventActivity"]["byKind"]} == {"login_success": 0, "login_failure": 1, "service_access": 1}
        assert initial["eventActivity"]["distinctActors"] == 2 and initial["eventActivity"]["distinctDevices"] == 1
        assert initial["risk"]["score"] == 5 and initial["risk"]["revision"] == 1
        assert initial["latestManualDetection"]["status"] == "no_result"
        assert initial["caseCloseThroughput"]["windowTotal"] == 0
        assert initial["unavailable"] and all(x["reason"] for x in initial["unavailable"])
        assert re.fullmatch(r"[a-f0-9]{64}", initial["snapshotId"])
        for mark in ("simulation_events", "simulation_risk", "simulation_detection"):
            sql(f"UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='{mark}'")
            client.call(path, want=403)
            sql(f"UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='{mark}'")
        assert client.call(path)["cases"] == initial["cases"]
        report["checks"].append("exact independent event/case/risk oracle, unknown versus zero, all three grant conjunctions, admin/tenant/domain and malformed query denial")

        event_body = dict(domainId="simulation-domain-a", datasetId="simulation-dataset", caseId="http-case",
                          operationId="dashboard-close", action="close", reason="SIMULATED dashboard case close", expectedRevision=1)
        closed = client.call("/api/simulation/events/transition", event_body)
        assert client.call("/api/simulation/events/transition", event_body) == closed
        risk_body = dict(domainId="simulation-domain-a", datasetId="simulation-dataset", subject="synthetic-actor",
                         operationId="dashboard-reset", reason="SIMULATED dashboard risk reset", expectedRevision=1)
        reset = client.call("/api/simulation/risk/reset", risk_body)
        assert reset["result"]["score"] == 0
        changed = client.call(path)
        assert changed["cases"]["open"] == 1 and changed["cases"]["closed"] == 1
        assert changed["caseCloseThroughput"]["windowTotal"] == 1
        assert sum(x["count"] for x in changed["caseCloseThroughput"]["buckets"]) == 1
        assert changed["risk"]["score"] == 0 and changed["risk"]["revision"] == 2
        assert changed["snapshotId"] != initial["snapshotId"]
        isolated = other.call(path)
        assert isolated["cases"]["open"] == 2 and isolated["risk"]["score"] == 5
        report["checks"].append("real producer case close and risk reset reflected with exact counts and tenant isolation")

        rules = [{"id": "dashboard-rule", "enabled": True, "combine": "all",
                  "conditions": [{"field": "event.category", "operator": "exists", "values": []}]}]
        template = client.call("/api/simulation/detection/create", dict(domainId="simulation-domain-a", templateId="dashboard-template",
                               name="SIMULATED dashboard template", operationId="dashboard-template-create", rules=rules))["template"]
        submitted = client.call("/api/simulation/detection/run", dict(domainId="simulation-domain-a", templateId="dashboard-template",
                                datasetId="simulation-dataset", templateVersion=1, operationId="dashboard-manual-run"))
        task_id = submitted["submission"]["task"]["taskUUID"]
        task_path = "/api/simulation/detection/task?domainId=simulation-domain-a&taskUUID=" + task_id
        start_worker()
        completed = wait_for_worker(worker, worker_base, report, lambda: client.call(task_path))
        assert completed["task"]["state"] == "succeeded"
        latest = client.call(path)["latestManualDetection"]
        assert latest["status"] == "available" and latest["taskUUID"] == task_id
        assert latest["templateHash"] == template["hash"] and latest["templateVersion"] == 1
        assert len(completed["task"]["result"]["matches"]) == 2  # exists(category) matches each of the two seeded events
        assert latest["eventCount"] == 2 and latest["matchCount"] == len(completed["task"]["result"]["matches"])
        assert other.call(path)["latestManualDetection"]["status"] == "no_result"
        stop_worker()
        before = client.call(path)
        stop()
        start()
        after = client.call(path)
        for key in ("cases", "dataset", "latestManualDetection", "unavailable"):
            assert after[key] == before[key], key
        assert after["risk"]["score"] == 0 and after["risk"]["revision"] == 2
        report["checks"].append("actual manual worker result projection, current actor scope and restart persistence")

        run_go_integration("./internal/auth", "^TestSimulationDashboardHTTPGrantScopeSessionAndNoMutation$", "TestSimulationDashboardHTTPGrantScopeSessionAndNoMutation",
                           dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        run_go_integration("./internal/auth", "^TestSimulationDashboardRechecksSessionAfterIdentityLock$", "TestSimulationDashboardRechecksSessionAfterIdentityLock",
                           dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        run_go_integration("./internal/simulationdashboard", "^TestPostgresDashboardSnapshot$", "TestPostgresDashboardSnapshot",
                           dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        report["checks"].append("named PostgreSQL authorization and one-statement snapshot integration cases executed without skip")
        browser_env = dict({k: v for k, v in env.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))},
                           ADTR_E2E_BASE_URL=base, ADTR_E2E_USERNAME="simulation-operator", ADTR_E2E_PASSWORD=account_password)
        run(["npx", "--no-install", "playwright", "test", "--config", "simulation-dashboard.playwright.config.ts", "simulation-dashboard-e2e/dashboard.spec.ts"],
            cwd=ROOT / "web", env=browser_env, timeout=240)
        report["checks"].append("real browser SIMULATED facts, unavailable indicators, retry/reload and producer-driven refresh")
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


if __name__ == '__main__':
    main()
