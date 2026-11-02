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

API = "/api/simulation/labels"
REQUIRED_LEDGER_SUBTESTS = (
    "Identifier128SeedPolicyDetailAnd129Rejection",
    "OversizedPayloadsLeaveNoWrites",
    "ScopedCompositeFKRejectsTampering",
    "ConcurrentCreateAtFixedPolicyQuota",
)
ROLE = "simulation_e2e_role_0001"
SEED = r'''
package main
import (
 "context"
 "os"
 "github.com/jackc/pgx/v5"
 "github.com/yunpiao/adtr/internal/groupgraph"
 "github.com/yunpiao/adtr/internal/simulationlabels"
 "github.com/yunpiao/adtr/internal/tasks"
 "github.com/yunpiao/adtr/internal/schemaversion"
)
func main() {
 ctx:=context.Background()
 c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL")); if e!=nil {panic(e)}; defer c.Close(ctx)
 tx,e:=c.Begin(ctx); if e!=nil {panic(e)}; defer tx.Rollback(ctx)
 if e=tasks.CheckSchemaTx(ctx,tx,schemaversion.Current); e!=nil {panic(e)}
 for _,pair:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}} {
  s:=simulationlabels.Scope{Tenant:pair[0],Domain:pair[1],Read:true,Manage:true}
  observations:=[]simulationlabels.Observation{}
  for i,id:=range []string{"o1","o2","o3"} {
   member:="u"; if i>0 {member="c"}
   observations=append(observations,simulationlabels.Observation{ID:id,Sequence:uint64(i+1),Complete:i<2,
    Snapshot:groupgraph.Snapshot{Version:groupgraph.ContractVersion,Simulated:true,Scope:groupgraph.Scope{TenantID:s.Tenant,DomainID:s.Domain},ObservationID:id,
     Nodes:[]groupgraph.Node{{ID:"g",Kind:groupgraph.Group,MembersKnown:true},{ID:"nested",Kind:groupgraph.Group,MembersKnown:true},{ID:"u",Kind:groupgraph.User},{ID:"c",Kind:groupgraph.Computer}},
     Edges:[]groupgraph.Edge{{GroupID:"g",MemberID:"nested"},{GroupID:"nested",MemberID:member}}},
    Assets:[]simulationlabels.Asset{{ObjectID:"g",Name:"Synthetic sensitive group"},{ObjectID:"nested",Name:"Nested group"},{ObjectID:"u",Name:"Example user"},{ObjectID:"c",Name:"Example computer"}}})
  }
  _,e=simulationlabels.SeedFixtureTx(ctx,tx,s,simulationlabels.Fixture{ID:"simulation-dataset",Version:simulationlabels.Contract,Producer:"isolated-labels-e2e-v1",Simulated:true,Observations:observations}); if e!=nil {panic(e)}
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
            locations.update(re.findall(r"\b(integration_test\.go|simulation_labels_integration_test\.go|simulation_labels_upgrade_integration_test\.go):([0-9]{1,6}):", output))
            states.update(re.findall(r"\bSQLSTATE ([0-9A-Z]{5})\b", output))
        print(json.dumps({"phase": "named-postgres-test", "exitCode": failure.returncode,
                          "locations": [f"{name}:{line}" for name, line in sorted(locations)[:16]],
                          "sqlStates": sorted(states)[:16]}), flush=True)
        raise RuntimeError("required PostgreSQL integration failed; see closed diagnostic") from None
    events = [json.loads(line) for line in completed.stdout.splitlines() if line.strip()]
    passed = {event.get("Test") for event in events if isinstance(event, dict) and event.get("Action") == "pass"}
    skipped = {event.get("Test") for event in events if isinstance(event, dict) and event.get("Action") == "skip"}
    required = {expected}
    if expected == "TestPostgresLabelsLedger":
        required.update(expected + "/" + child for child in REQUIRED_LEDGER_SUBTESTS)
    assert required <= passed and not skipped, "required PostgreSQL integration did not execute fully"


def main():
    os.chdir(ROOT)
    # CI cancellation still tears down only this harness-owned process/container.
    def cancelled(_signum, _frame):
        raise KeyboardInterrupt("simulation acceptance cancelled")
    signal.signal(signal.SIGTERM, cancelled)
    evidence = ROOT / "artifacts/simulation-labels"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": []}
    name = "adtr-simulation-labels-e2e-" + uuid.uuid4().hex[:12]
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
                   ADTR_SIMULATION_LABELS_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
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
 SELECT tenant_id,id,mark,true,mark<>'domains' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_labels'),('domains')) p(mark);
INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change)
 SELECT t.tenant,t.username,u.password_hash,'viewer','{ROLE}',false FROM adtr.users u CROSS JOIN (VALUES('default','simulation-operator'),('default','simulation-peer'),('other','simulation-other')) t(tenant,username) WHERE u.username='simulation-admin';
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
        peer = Client(base, "operator")  # Shared Client has a closed stage vocabulary.
        peer.call("/api/auth/login", {"username": "simulation-peer", "password": account_password})
        scope = dict(domainId="simulation-domain-a", datasetId="simulation-dataset")
        def get(route, **query):
            return API + route + "?" + urllib.parse.urlencode(dict(scope, **query))
        def mutate(route, op, **fields):
            return client.call(API + route, dict(scope, operationId=op, reason="SIMULATED acceptance", **fields))
        datasets = API + "/datasets?domainId=simulation-domain-a&offset=0&limit=20"
        anonymous.call(datasets, want=401)
        admin.call(datasets, want=403)
        client.call(datasets.replace("domain-a", "domain-b"), want=404)
        assert client.call(datasets)["items"][0]["simulation"] is True
        spec = dict(name="HTTP policy", sensitiveGroupIds=["g"], manualLabels=[])
        create = dict(scope, policyId="http-policy", expectedRevision=0, spec=spec, operationId="http-create", reason="SIMULATED acceptance")
        receipt = client.call(API + "/create", create)
        assert client.call(API + "/create", create) == receipt
        client.call(API + "/create", dict(create, reason="different"), want=409)
        peer.call(get("/operation", operationId="http-create"), want=404)
        other.call(get("/policy", policyId="http-policy"), want=404)
        evaluations = []
        for observation in ("o1", "o2", "o3"):
            evaluations.append(mutate("/evaluate", "http-eval-"+observation, policyId="http-policy", expectedRevision=1, observationId=observation)["evaluation"])
        before, after, incomplete = evaluations
        comparison = mutate("/compare", "http-compare", beforeId=before["id"], afterId=after["id"])["comparison"]
        assert [(a["objectId"], a["changes"][0]["action"]) for a in comparison["alerts"]] == [("c", "added"), ("u", "removed")]
        assert all(a["simulation"] and a["delivery"] == "in_app_only" and a["changes"][0]["provenance"]["distance"] == 2 for a in comparison["alerts"])
        assert mutate("/compare", "http-compare-again", beforeId=before["id"], afterId=after["id"])["comparison"] == comparison
        client.call(API + "/compare", dict(scope, beforeId=after["id"], afterId=incomplete["id"], operationId="incomplete", reason="SIMULATED acceptance"), want=409)
        assets = client.call(get("/assets", evaluationId=after["id"], kind="", name="", offset=0, limit=20))
        assert assets["count"] == 4 and all(a["isHoneypot"] is None and a["honeypotAvailability"] == "unavailable" for a in assets["items"])
        assert client.call(get("/assets", evaluationId=after["id"], kind="user", name="not-present", offset=0, limit=20))["count"] == 0
        # No selected groups produces exactly zero change alerts, not unknown.
        mutate("/create", "zero-create", policyId="zero-policy", expectedRevision=0, spec=dict(name="Zero policy", sensitiveGroupIds=[], manualLabels=[]))
        zero = [mutate("/evaluate", "zero-"+o, policyId="zero-policy", expectedRevision=1, observationId=o)["evaluation"]["id"] for o in ("o1", "o2")]
        assert mutate("/compare", "zero-compare", beforeId=zero[0], afterId=zero[1])["comparison"]["alerts"] == []
        mutate("/update", "http-edit", policyId="http-policy", expectedRevision=1, spec=dict(spec, name="Edited policy"))
        edited = mutate("/evaluate", "http-edited-eval", policyId="http-policy", expectedRevision=2, observationId="o2")["evaluation"]
        client.call(API + "/compare", dict(scope, beforeId=before["id"], afterId=edited["id"], operationId="policy-not-asset", reason="SIMULATED acceptance"), want=409)
        client.call(API + "/archive", dict(scope, policyId="http-policy", expectedRevision=1, operationId="stale", reason="SIMULATED acceptance"), want=409)
        mutate("/archive", "archive", policyId="http-policy", expectedRevision=2)
        mutate("/restore", "restore", policyId="http-policy", expectedRevision=3)
        assert client.call(get("/history", policyId="http-policy", offset=0, limit=20))["count"] == 4
        assert client.call(get("/comparisons", offset=0, limit=20))["count"] == 2  # failures/edits never leave partial alerts
        for field in ("writeable", "readable"):
            revoked = "readable=false,writeable=false" if field == "readable" else "writeable=false"
            sql(f"UPDATE adtr.access_permissions SET {revoked} WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_labels'")
            client.call(API + "/archive", dict(scope, policyId="http-policy", expectedRevision=4, operationId="denied", reason="SIMULATED acceptance"), want=403)
            client.call(get("/operation", operationId="http-create"), want=403 if field == "readable" else 200)
            sql(f"UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_labels'")
        sql("UPDATE adtr.resource_domains SET active=false WHERE tenant_id='default' AND id='simulation-domain-a'")
        client.call(get("/operation", operationId="http-create"), want=404)
        sql("UPDATE adtr.resource_domains SET active=true WHERE tenant_id='default' AND id='simulation-domain-a'")
        stop(); start()
        assert client.call(get("/comparison", comparisonId=comparison["id"])) == comparison
        assert client.call(get("/operation", operationId="http-create")) == receipt
        report["checks"].append("real HTTP CAS/replay/actor/tenant/live grants; exact two alerts, zero alerts, incomplete and policy-change rejection; restart persistence")
        for package, test_name in (("./internal/auth", "TestSimulationLabelsHTTPAuthorityCASAndReceipts"), ("./internal/simulationlabels", "TestPostgresLabelsLedger"), ("./internal/store", "TestSchema28UpgradePreservesHistoryAndGrants"), ("./internal/store", "TestSchema28FailureRollsBackPermissionAndTables"), ("./internal/store", "TestSchema28ConcurrentUpgrade")):
            run_go_integration(package, "^" + test_name + "$", test_name, dict(env, ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
        report["checks"].append("named PostgreSQL authorization and immutable ledger cases executed without skip")
        browser_env = dict({k: v for k, v in env.items() if not k.upper().startswith(("ADTR_", "POSTGRES_", "PG"))},
                           ADTR_E2E_BASE_URL=base, ADTR_E2E_USERNAME="simulation-operator", ADTR_E2E_PASSWORD=account_password)
        run(["npx", "--no-install", "playwright", "test", "--config", "simulation-labels.playwright.config.ts", "simulation-labels-e2e/labels.spec.ts"],
            cwd=ROOT / "web", env=browser_env, timeout=240)
        report["checks"].append("real browser policy/evaluation/comparison, immutable alerts, reload and response-loss recovery")
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


if __name__ == '__main__':
    main()
