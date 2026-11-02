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

API = "/api/simulation/detection-exceptions"
DETECTION = "/api/simulation/detection"
NAMED_TESTS = (('./internal/simulationdetectionexceptions', 'TestPostgresExceptionsWorkerAnnotationsHistoryAndRevocation'), ('./internal/simulationdetectionexceptions', 'TestPostgresExceptionsAtomicImportCASQuotaAndAudit'), ('./internal/simulationdetectionexceptions', 'TestPostgresExceptionsRevisionExhaustionSafetyControls'), ('./internal/simulationdetectionexceptions', 'TestPostgresExceptionsSourceForgeryAndOversizeAtomicity'), ('./internal/auth', 'TestSimulationDetectionExceptionsHTTPAuthorityAndRevocation'), ('./internal/store', 'TestSchema30UpgradePreservesHistoryAndGrants'), ('./internal/store', 'TestSchema30FailureRollsBackPermissionAndTables'), ('./internal/store', 'TestSchema30ConcurrentUpgrade'))
ROLE = "exception_operator_00001"
SEED = r'''
package main
import (
 "context"
 "os"
 "github.com/jackc/pgx/v5"
 "github.com/yunpiao/adtr/internal/securityevents"
 "github.com/yunpiao/adtr/internal/simulationevents"
 "github.com/yunpiao/adtr/internal/tasks"
 "github.com/yunpiao/adtr/internal/schemaversion"
)
func main() {
 ctx:=context.Background()
 c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL")); if e!=nil {panic(e)}; defer c.Close(ctx)
 tx,e:=c.Begin(ctx); if e!=nil {panic(e)}; defer tx.Rollback(ctx)
 if e=tasks.CheckSchemaTx(ctx,tx,schemaversion.Current); e!=nil {panic(e)}
 for _,pair:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}} {
  s:=securityevents.Scope{Tenant:pair[0],Domain:pair[1],Read:true,Manage:true}
  p:=securityevents.Provenance{Contract:securityevents.ContractVersion,Fixture:"SIMULATED-e2e-only",Synthetic:true}
  _,e=simulationevents.SeedFixtureTx(ctx,tx,s,simulationevents.Fixture{ID:"simulation-dataset",Provenance:p,
   Events:[]securityevents.RawEvent{{ID:"exception-positive",Tenant:s.Tenant,Domain:s.Domain,Kind:"login_failure",Actor:"synthetic-actor",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:"2026-01-01T00:00:00Z",Provenance:p},{ID:"exception-negative",Tenant:s.Tenant,Domain:s.Domain,Kind:"service_access",Actor:"synthetic-browser",Device:"synthetic-device",Session:"synthetic-session",SourceIP:"192.0.2.1",OccurredAt:"2026-01-01T00:00:01Z",Provenance:p}},
   Cases:[]simulationevents.FixtureCase{{ID:"http-case",EvidenceIDs:[]string{"exception-positive"}},{ID:"browser-case",EvidenceIDs:[]string{"exception-positive"}}}})
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
            locations.update(re.findall(r"\b(integration_test\.go|simulation_detection_exceptions_integration_test\.go|simulation_detection_exceptions_upgrade_integration_test\.go):([0-9]{1,6}):", output))
            states.update(re.findall(r"\bSQLSTATE ([0-9A-Z]{5})\b", output))
        print(json.dumps({"phase": "named-postgres-test", "exitCode": failure.returncode,
                          "locations": [f"{name}:{line}" for name, line in sorted(locations)[:16]],
                          "sqlStates": sorted(states)[:16]}), flush=True)
        raise RuntimeError("required PostgreSQL integration failed; see closed diagnostic") from None
    events = [json.loads(line) for line in completed.stdout.splitlines() if line.strip()]
    passed = {event.get("Test") for event in events if isinstance(event, dict) and event.get("Action") == "pass"}
    skipped = {event.get("Test") for event in events if isinstance(event, dict) and event.get("Action") == "skip"}
    required = {expected}
    assert required <= passed and not skipped, "required PostgreSQL integration did not execute fully"


def main():
    os.chdir(ROOT)
    # CI cancellation still tears down only this harness-owned process/container.
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt("simulation acceptance cancelled")
    signal.signal(signal.SIGTERM, cancelled)
    evidence = ROOT / "artifacts/simulation-detection-exceptions"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": [], "postgresTests": []}
    name = "adtr-simulation-detection-exceptions-e2e-" + uuid.uuid4().hex[:12]
    owner = uuid.uuid4().hex
    created, server, worker = False, None, None
    worker_base = None
    log = None
    # Do not inherit deployment/database/LDAP/worker inputs into isolated tests.
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}
    password, account_password = secrets.token_hex(24), secrets.token_urlsafe(30)
    env["POSTGRES_PASSWORD"] = password

    def sql(statement):
        try:
            result = run(["docker", "exec", "-i", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-At"], input=statement, text=True, timeout=30, capture_output=True)
        except subprocess.CalledProcessError as failure:
            print(json.dumps({"phase":"fixture-sql", "exitCode":failure.returncode,
                              "sqlStates":sorted(set(re.findall(r"SQLSTATE ([0-9A-Z]{5})", failure.stderr or "")))[:16]}), flush=True)
            raise RuntimeError("isolated SQL failed; see closed diagnostic") from None
        return result.stdout.strip()

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
                   ADTR_SIMULATION_DETECTION_EXCEPTIONS_ENABLED="true", ADTR_SIMULATION_EVENTS_ENABLED="true", ADTR_SIMULATION_DETECTION_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
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
 SELECT tenant_id,id,mark,true,mark<>'domains' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_detection'),('simulation_detection_exceptions'),('simulation_events'),('domains')) p(mark);
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
        domain = dict(domainId="simulation-domain-a")
        def get(route, **query):
            return API + route + "?" + urllib.parse.urlencode(dict(domain, **query))
        snapshot = get("/snapshot")
        anonymous.call(snapshot, want=401)
        admin.call(snapshot, want=403)
        client.call(snapshot.replace("domain-a", "domain-b"), want=404)
        assert client.call(snapshot)["snapshot"]["revision"] == 1
        assert not client.call(snapshot)["snapshot"]["entries"]
        rules = [dict(id="http-rule",enabled=True,combine="all",conditions=[dict(field="event.outcome",operator="eq",values=["failure"])])]
        template_body = dict(domain,templateId="http-template",name="SIMULATED exception source",operationId="http-template-create",rules=rules)
        template = client.call(DETECTION+"/create",template_body)["template"]
        run_body = dict(domain,templateId=template["id"],templateVersion=1,datasetId="simulation-dataset",operationId="http-run")
        submitted = client.call(DETECTION+"/run",run_body)
        task_id = submitted["submission"]["task"]["taskUUID"]
        task_path = DETECTION+"/task?"+urllib.parse.urlencode(dict(domain,taskUUID=task_id))
        queued_eval = dict(domain,operationId="queued-eval",id="queued-eval",sourceTaskId=task_id,expectedRevision=1)
        client.call(API+"/evaluate",queued_eval,want=409)
        start_worker()
        completed = wait_for_worker(worker,worker_base,report,lambda:client.call(task_path))
        result = completed["task"]["result"]
        assert completed["task"]["state"] == "succeeded" and completed["task"]["attempt"] == 1
        assert result["eventCount"] == 2 and result["templateHash"] == template["hash"]
        assert [(m["rule_id"],m["event_id"]) for m in result["matches"]] == [("http-rule","exception-positive")]
        assert not any(m["event_id"] == "exception-negative" for m in result["matches"])
        revision = 1
        def mutate(action, op, **extra):
            nonlocal revision
            body = dict(domain,operationId=op,expectedRevision=revision,**extra)
            receipt = client.call(API+"/"+action,body)
            assert client.call(API+"/"+action,body) == receipt
            assert receipt["action"] == action and receipt["intent"]["operationId"] == op
            assert re.fullmatch(r"[0-9a-f]{64}",receipt["requestHash"])
            revision = receipt["revision"]
            return receipt
        def evaluate(name, source=task_id):
            receipt = mutate("evaluate",name,id=name,sourceTaskId=source)
            value = receipt["evaluation"]
            expected = client.call(DETECTION+"/task?"+urllib.parse.urlencode(dict(domain,taskUUID=source)))["task"]["result"]
            assert value["source"] == expected
            assert [m["match"] for m in value["matches"]] == expected["matches"]
            return value
        preview = client.call(API+"/preview",dict(domain,sourceTaskId=task_id,eventId="exception-positive",ruleId="http-rule",id="http-exception",reason="SIMULATED accepted source",notBefore="2020-01-01T00:00:00Z",expiresAt="2099-01-01T00:00:00Z"))
        create = dict(domain,operationId="http-create",expectedRevision=revision,declaration=preview)
        client.call(API+"/create",create,csrf=False,want=403)
        for key in ("matches","passed","ruleDigest","actorId"):
            client.call(API+"/create",dict(create,**{key:True}),want=400)
        mutate("create","http-create",declaration=preview)
        disabled = evaluate("http-disabled")
        assert not disabled["matches"][0]["exceptions"]
        mutate("enable","http-enable",id="http-exception")
        enabled = evaluate("http-enabled")
        assert [x["exception_id"] for x in enabled["matches"][0]["exceptions"]] == ["http-exception"]
        assert client.call(get("/evaluation",id="http-enabled")) == enabled
        other.call(get("/evaluation",id="http-enabled"),want=404)
        # A new executed rule digest must not inherit the source-bound exception.
        updated = client.call(DETECTION+"/update",dict(template_body,operationId="http-template-update",expectedVersion=1,name="SIMULATED revised execution"))["template"]
        second = client.call(DETECTION+"/run",dict(run_body,operationId="http-run-v2",templateVersion=2))["submission"]["task"]["taskUUID"]
        second_path = DETECTION+"/task?"+urllib.parse.urlencode(dict(domain,taskUUID=second))
        second_done = wait_for_worker(worker,worker_base,report,lambda:client.call(second_path))
        assert second_done["task"]["result"]["matches"][0]["rule_digest"] != result["matches"][0]["rule_digest"]
        mismatch = evaluate("http-digest-mismatch",second)
        assert not mismatch["matches"][0]["exceptions"]
        mutate("disable","http-disable",id="http-exception")
        assert not evaluate("http-after-disable")["matches"][0]["exceptions"]
        mutate("archive","http-archive",id="http-exception")
        assert client.call(get("/evaluation",id="http-enabled")) == enabled
        global_decl = dict(preview,id="http-global",global_=True,sourceTaskId="",ruleId="")
        global_decl["global"] = global_decl.pop("global_")
        before_import = client.call(snapshot)
        bad = dict(global_decl,id="http-bad",conditions=[dict(field="event.code",operator="eq",values=["4625"])])
        client.call(API+"/import",dict(domain,operationId="bad-import",expectedRevision=revision,entries=[global_decl,bad]),want=400)
        assert client.call(snapshot) == before_import
        mutate("import","http-import",entries=[global_decl])
        assert not evaluate("http-import-disabled",second)["matches"][0]["exceptions"]
        mutate("enable","http-global-enable",id="http-global")
        assert [x["exception_id"] for x in evaluate("http-global-enabled",second)["matches"][0]["exceptions"]] == ["http-global"]
        mutate("disable","http-global-disable",id="http-global")
        exported = client.call(get("/export"))
        assert exported == client.call(snapshot)
        assert client.call(task_path)["task"]["result"] == result
        stop_worker(); stop(); start(); start_worker()
        wait_for_worker(worker,worker_base,report)
        assert client.call(get("/evaluation",id="http-enabled")) == enabled
        assert client.call(get("/export")) == exported
        assert client.call(task_path)["task"]["result"] == result
        report["checks"].append("actual Worker positive1 negative0; disabled/enabled/source-digest mismatch/global annotations; original matches and prior history unchanged; atomic import; replay and restart")
        for package, test_name in NAMED_TESTS:
            run_go_integration(package,"^"+test_name+"$",test_name,dict(env,ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
            report["postgresTests"].append({"name":test_name,"status":"passed","skipped":False})
        report["checks"].append("all named PostgreSQL backend/auth/schema30 tests executed without skips")
        browser_env = dict({k:v for k,v in env.items() if not k.upper().startswith(("ADTR_","POSTGRES_","PG"))},
                           ADTR_E2E_BASE_URL=base,ADTR_E2E_USERNAME="simulation-operator",ADTR_E2E_PASSWORD=account_password)
        run(["npx","--no-install","playwright","test","--config","simulation-detection-exceptions.playwright.config.ts","simulation-detection-exceptions-e2e/exceptions.spec.ts"],cwd=ROOT/"web",env=browser_env,timeout=240)
        assert sql("SELECT count(*) FROM adtr.tasks WHERE tenant_id='default' AND domain_id='simulation-domain-a' AND kind='simulation.detection'") == "3"
        # Revocation affects source-dependent historical reads; it cannot be bypassed by stored receipts.
        sql(f"UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_detection'")
        client.call(get("/evaluation",id="http-enabled"),want=403)
        client.call(get("/receipt",operationId="http-enabled"),want=403)
        assert client.call(snapshot)["snapshot"]["revision"] >= revision
        sql(f"UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_detection'")
        client.call(get("/evaluation",id="http-enabled"),want=404)
        report["checks"].append("actual browser annotations, JSON bytes, response-loss recovery and original task preservation; source revocation/regrant does not revive prior epoch")
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
