"""Dedicated synthetic-only PostgreSQL/TCP/browser acceptance; owns all resources.

Requires prebuilt bin/adtr, web/dist and Playwright Chromium. Never accepts a
caller database URL and never touches AD. Run from repository root.
"""
import base64
import hashlib
import io
import sys
import zipfile
import xml.etree.ElementTree as ET
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

API = "/api/simulation/dns-ip"
ROLE = "dns_ip_operator_role_001"
NAMED_TESTS = (('./internal/simulationdnsip', 'TestPostgresDNSIPWorkerDownloadAndReplay'), ('./internal/simulationdnsip', 'TestPostgresDNSIPIsolationQuotaAndAudit'), ('./internal/simulationdnsip', 'TestPostgresDNSIPRejectedFinalizationAndCancellation'), ('./internal/simulationdnsip', 'TestPostgresDNSIPSourceTamperingAndIntegrity'), ('./internal/simulationdnsip', 'TestPostgresDNSIPFenceAndForgedSuccess'), ('./internal/auth', 'TestSimulationDNSIPHTTPWorkerDownloadAuthorityAndRevocation'), ('./internal/store', 'TestSchema31UpgradePreservesHistoryAndGrants'), ('./internal/store', 'TestSchema31FailureRollsBackPermissionAndTables'), ('./internal/store', 'TestSchema31ConcurrentUpgrade'))
HEADERS = ["ID", "Domain", "DNS name", "IP", "Observation", "Source", "Contract version", "Simulation"]
SEED = r'''
package main
import("context";"fmt";"os";"github.com/jackc/pgx/v5";"github.com/yunpiao/adtr/internal/dnsipassets";"github.com/yunpiao/adtr/internal/simulationdnsip";"github.com/yunpiao/adtr/internal/tasks";"github.com/yunpiao/adtr/internal/schemaversion")
func main(){ctx:=context.Background();c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL"));if e!=nil{panic(e)};defer c.Close(ctx);tx,e:=c.Begin(ctx);if e!=nil{panic(e)};defer tx.Rollback(ctx);if e=tasks.CheckSchemaTx(ctx,tx,schemaversion.Current);e!=nil{panic(e)}
 for _,p:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}}{
 rs:=[]dnsipassets.Record{};add:=func(id,dns,ip string){rs=append(rs,dnsipassets.Record{ID:id,Domain:"SIMULATION.EXAMPLE.TEST.",DNSName:dns,IP:ip,Observation:"synthetic-observation",Source:"SIMULATED-fixture",Version:dnsipassets.Version,Simulation:true})}
 for i:=1;i<=35;i++{add(fmt.Sprintf("row-%03d",i),fmt.Sprintf("HOST-%03d.SIMULATION.EXAMPLE.TEST.",i),fmt.Sprintf("192.0.2.%d",i))};add("mapped","mapped.simulation.example.test","::ffff:192.0.2.99");add("v6","v6.simulation.example.test","2001:db8::1");add("multi","host-001.simulation.example.test","192.0.2.200")
 _,e=simulationdnsip.SeedFixtureTx(ctx,tx,simulationdnsip.Scope{Tenant:p[0],Domain:p[1],ActorID:1,Read:true,Manage:true},simulationdnsip.Fixture{ID:"synthetic-observation",DNSDomain:"simulation.example.test",Source:"SIMULATED-fixture",Records:rs});if e!=nil{panic(e)}};if e=tx.Commit(ctx);e!=nil{panic(e)}}
'''

def expected_records():
    rows=[dict(id=f"row-{i:03d}",domain="simulation.example.test",dnsName=f"host-{i:03d}.simulation.example.test",ip=f"192.0.2.{i}",observation="synthetic-observation",source="SIMULATED-fixture",version="dnsip-sim-v1",simulation=True) for i in range(1,36)]
    for ident,name,ip in (("mapped","mapped","192.0.2.99"),("v6","v6","2001:db8::1"),("multi","host-001","192.0.2.200")):
        rows.append(dict(id=ident,domain="simulation.example.test",dnsName=name+".simulation.example.test",ip=ip,observation="synthetic-observation",source="SIMULATED-fixture",version="dnsip-sim-v1",simulation=True))
    import ipaddress
    return sorted(rows,key=lambda r:(r["dnsName"],ipaddress.ip_address(r["ip"]).version,int(ipaddress.ip_address(r["ip"])),r["id"]))

def validate_workbook(data, expected):
    assert 0 < len(data) <= 8*1024*1024
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        names=z.namelist()
        assert len(names)==len(set(names))
        assert all(not n.startswith("/") and ".." not in n.split("/") for n in names)
        assert not any("vba" in n.lower() or "externallink" in n.lower() or "macros" in n.lower() for n in names)
        for n in names:
            if n.endswith(".xml") or n.endswith(".rels"):
                tree=ET.fromstring(z.read(n))
                assert not any(e.tag.split("}")[-1] in ("f","hyperlink") or e.attrib.get("TargetMode")=="External" for e in tree.iter())
        ns={"s":"http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
        sheet=ET.fromstring(z.read("xl/worksheets/sheet1.xml"))
        got=[]
        for index,row in enumerate(sheet.findall("s:sheetData/s:row",ns),1):
            assert row.attrib["r"]==str(index)
            cells=row.findall("s:c",ns);assert len(cells)==8
            assert [c.attrib.get("r") for c in cells]==[chr(65+i)+str(index) for i in range(8)]
            assert all(c.attrib.get("t")=="inlineStr" for c in cells)
            got.append(["".join(c.itertext()) for c in cells])
        wanted=[HEADERS]+[[r[k] if k!="simulation" else "true" for k in ("id","domain","dnsName","ip","observation","source","version","simulation")] for r in expected]
        assert got==wanted, "all workbook cells/order must equal the independent fixture/filter oracle"
    return dict(rows=len(expected),sha256=hashlib.sha256(data).hexdigest(),bytes=len(data))


def download(client,path,want=200):
    try: response=client.opener.open(client.base+path,timeout=20)
    except urllib.error.HTTPError as error: response=error
    with response:
        assert response.status==want, "unexpected download status"
        data=response.read(8*1024*1024+1)
        if want==200:
            assert response.headers.get_content_type()=="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            assert response.headers.get("X-ADTR-SHA256")==hashlib.sha256(data).hexdigest()
            assert response.headers.get("Content-Length")==str(len(data))
        return data



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
            locations.update(re.findall(r"\b(integration_test\.go|simulation_dns_ip_integration_test\.go|simulation_dns_ip_upgrade_integration_test\.go):([0-9]{1,6}):", output))
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
    evidence = ROOT / "artifacts/simulation-dns-ip"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": [], "postgresTests": []}
    name = "adtr-simulation-dns-ip-e2e-" + uuid.uuid4().hex[:12]
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
                   ADTR_SIMULATION_DNS_IP_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
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
INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES ('default','{ROLE}','Synthetic DNS IP operator'),('other','{ROLE}','Other synthetic operator');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT tenant_id,id,mark,true,mark<>'domains' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_dns_ip'),('domains')) p(mark) WHERE id='{ROLE}';
INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change)
 SELECT t.tenant,t.username,u.password_hash,'viewer','{ROLE}',false FROM adtr.users u CROSS JOIN (VALUES('default','simulation-operator'),('other','simulation-other'),('default','simulation-peer')) t(tenant,username) WHERE u.username='simulation-admin';
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
        peer = Client(base,"race-0")
        peer.call("/api/auth/login", {"username":"simulation-peer","password":account_password})
        domain = dict(domainId="simulation-domain-a")
        def get(route, **query):
            return API + route + "?" + urllib.parse.urlencode(dict(domain, **query))
        listing=get("/observations")
        anonymous.call(listing,want=401);admin.call(listing,want=403)
        client.call(listing.replace("domain-a","domain-b"),want=404)
        observation=client.call(listing)["observations"][0]
        assert observation["id"]=="synthetic-observation" and observation["recordCount"]==38 and observation["complete"] is True
        expected=expected_records()
        combined=[]
        for offset in (0,20):
            page=client.call(get("/records",observationId=observation["id"],keyword="",family="",offset=offset,limit=20))
            assert page["total"]==38 and page["exhausted"]==(offset==20)
            combined+=page["rows"]
        assert combined==expected
        mapped=client.call(get("/records",observationId=observation["id"],keyword="mapped",family="ipv4",offset=0,limit=20))
        assert mapped["rows"]==[r for r in expected if r["id"]=="mapped"]
        body=dict(domain,observationId=observation["id"],observationHash=observation["hash"],keyword="",family="",operationId="http-all")
        client.call(API+"/export",dict(body,observationHash="0"*64),want=409)
        client.call(API+"/export",dict(body,passed=True),want=400)
        client.call(API+"/export",body,csrf=False,want=403)
        receipt=client.call(API+"/export",body)
        task=receipt["submission"]["task"]["taskUUID"]
        assert receipt["intent"]["export"]=={k:v for k,v in body.items() if k!="domainId"}
        assert client.call(API+"/export",body)["submission"]["task"]["taskUUID"]==task
        path=get("/task",taskId=task);file_path=get("/download",taskId=task)
        download(client,file_path,want=409)
        peer.call(path,want=404);other.call(path,want=404)
        start_worker()
        complete=wait_for_worker(worker,worker_base,report,lambda:client.call(path))
        assert complete["task"]["state"]=="succeeded"
        manifest=complete["task"]["result"]
        data=download(client,file_path)
        verified=validate_workbook(data,expected)
        assert manifest["columns"]==HEADERS and manifest["rowCount"]==38 and manifest["sha256"]==verified["sha256"] and manifest["bytes"]==len(data)
        assert manifest["payload"]["observationHash"]==observation["hash"]
        for op,keyword,family,rows in (("http-ipv6","","ipv6",[r for r in expected if r["id"]=="v6"]),("http-empty","no-such-host","",[])):
            q=client.call(API+"/export",dict(body,operationId=op,keyword=keyword,family=family))
            tid=q["submission"]["task"]["taskUUID"]
            done=wait_for_worker(worker,worker_base,report,lambda:client.call(get("/task",taskId=tid)))
            assert done["task"]["state"]=="succeeded"
            validate_workbook(download(client,get("/download",taskId=tid)),rows)
        stop_worker();stop();start();start_worker();wait_for_worker(worker,worker_base,report)
        assert client.call(path)==complete and download(client,file_path)==data
        assert client.call(API+"/export",body)["submission"]["task"]["taskUUID"]==task
        stop_worker()
        cancelled=client.call(API+"/export",dict(body,operationId="http-cancel"))["submission"]["task"]["taskUUID"]
        client.call(API+"/cancel",dict(domain,taskId=cancelled,operationId="http-cancel-op"))
        assert client.call(get("/task",taskId=cancelled))["task"]["state"]=="cancelled"
        download(client,get("/download",taskId=cancelled),want=409)
        start_worker();wait_for_worker(worker,worker_base,report)
        report["checks"].append("actual Worker; independent full38-row XLSX/order/columns, normalized IPv4/IPv6 and empty-header exports; owner/replay/restart/queued cancellation")
        for package,test_name in NAMED_TESTS:
            run_go_integration(package,"^"+test_name+"$",test_name,dict(env,ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
            report["postgresTests"].append({"name":test_name,"status":"passed","skipped":False})
        browser_env=dict({k:v for k,v in env.items() if not k.upper().startswith(("ADTR_","POSTGRES_","PG"))},ADTR_E2E_BASE_URL=base,ADTR_E2E_USERNAME="simulation-operator",ADTR_E2E_PASSWORD=account_password)
        run(["npx","--no-install","playwright","test","--config","simulation-dns-ip.playwright.config.ts","simulation-dns-ip-e2e/dns-ip.spec.ts"],cwd=ROOT/"web",env=browser_env,timeout=240)
        sql(f"UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_dns_ip'")
        client.call(path,want=403);download(client,file_path,want=403)
        sql(f"UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_dns_ip'")
        client.call(path,want=404);download(client,file_path,want=404)
        report["checks"].append("actual browser byte download and response-loss replay; permission revoke and regrant old epoch deny original download")
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
    if len(sys.argv)==4 and sys.argv[1]=="--validate-workbook":
        print(json.dumps(validate_workbook(Path(sys.argv[2]).read_bytes(),json.loads(Path(sys.argv[3]).read_text()))))
    else:
        main()
