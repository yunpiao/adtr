"""Isolated real PostgreSQL and browser acceptance for synchronous SIMULATED reports."""
import base64,json,os,secrets,re,shutil,signal,socket,subprocess,tempfile,time,urllib.error,urllib.request,urllib.parse,uuid
from pathlib import Path
from ci_postgres_image import postgres_image_args
from test_integration import IMAGE
from test_simulation_events_integration import Client
ROOT=Path(__file__).resolve().parents[1]
API="/api/simulation/asset-assessment"
ROLE="asset_assess_operator_01"
NAMED_TESTS=tuple(("./internal/simulationassetassessment",name) for name in (
"TestPostgresAssetAssessmentPersistenceReplayAndPaging","TestPostgresAssetAssessmentOwnerEpochAndSource","TestPostgresAssetAssessmentAtomicRollbackAndImmutable","TestPostgresAssetAssessmentTamperAndQuota","TestPostgresAssetAssessmentConcurrentIdempotence"))+(("./internal/auth","TestSimulationAssetAssessmentHTTPAuthorityEpochAndRollback"),)+tuple(("./internal/store",name) for name in ("TestSchema32UpgradePreservesHistoryAndGrants","TestSchema32FailureRollsBackPermissionAndTables","TestSchema32ConcurrentUpgrade"))
SEED=r'''
package main
import("context";"os";"github.com/jackc/pgx/v5";"github.com/yunpiao/adtr/internal/simulationassetassessment";"github.com/yunpiao/adtr/internal/tasks";"github.com/yunpiao/adtr/internal/schemaversion")
func main(){ctx:=context.Background();c,e:=pgx.Connect(ctx,os.Getenv("ADTR_DATABASE_URL"));if e!=nil{panic(e)};defer c.Close(ctx);tx,e:=c.Begin(ctx);if e!=nil{panic(e)};defer tx.Rollback(ctx);if e=tasks.CheckSchemaTx(ctx,tx,schemaversion.Current);e!=nil{panic(e)};for _,p:=range [][2]string{{"default","simulation-domain-a"},{"default","simulation-domain-b"},{"other","simulation-domain-a"}}{if _,e=simulationassetassessment.SeedFixtureTx(ctx,tx,p[0],p[1]);e!=nil{panic(e)}};if e=tx.Commit(ctx);e!=nil{panic(e)}}
'''
# Hand-authored expected facts, independent of Go Evaluate and server output.
# Exactly 30d is inactive; 30d-minus-1ns is recent; exactly 24h stays fresh.
def expected_rows(scenario):
    reasons={"enabled":"explicit_flags_clear","disabled":"explicit_disabled_flag","locked":"explicit_locked_flag","recent":"last_login_within_window","inactive":"last_login_at_or_before_boundary","never_observed":"explicit_never_assertion","unknown":"missing_evidence","not_applicable":"asset_kind","security":"explicit_security_flag","distribution":"explicit_security_flag","global":"explicit_scope","domain_local":"explicit_scope","universal":"explicit_scope"}
    facts=[
      ("user-enabled-recent","user","enabled","recent","not_applicable","not_applicable"),
      ("user-disabled-never","user","disabled","never_observed","not_applicable","not_applicable"),
      ("user-locked-boundary","user","locked","inactive","not_applicable","not_applicable"),
      ("user-unknown-disabled","user","unknown","unknown","not_applicable","not_applicable"),
      ("computer-recent-boundary","computer","enabled","recent","not_applicable","not_applicable"),
      ("computer-inactive-boundary","computer","enabled","inactive","not_applicable","not_applicable"),
      ("computer-missing","computer","unknown","unknown","not_applicable","not_applicable"),
      ("group-security-global","group","not_applicable","not_applicable","security","global"),
      ("group-distribution-local","group","not_applicable","not_applicable","distribution","domain_local"),
      ("group-security-universal","group","not_applicable","not_applicable","security","universal"),
      ("group-unknown","group","not_applicable","not_applicable","unknown","unknown")]
    result=[]
    for ident,kind,*values in facts:
        if scenario=="freshness-boundary" and ident=="computer-recent-boundary":values[1]="inactive"
        if scenario=="stale":values=["unknown" if v!="not_applicable" else v for v in values]
        row=dict(objectId=ident,kind=kind)
        for field,value in zip(("accountStatus","activity","groupType","groupScope"),values):
            row[field]=dict(value=value,reason="stale_observation" if scenario=="stale" and value=="unknown" else reasons[value])
        result.append(row)
    return result

def validate_report(report,scenario):
    assert scenario in ("baseline","freshness-boundary","stale")
    assert report["contract"]=="adtr.simulation-asset-assessment.v1" and report["simulation"] is True
    assert report["scenarioId"]==scenario and report["version"]=="simulated-asset-v1"
    times={"baseline":"2026-11-01T12:00:00Z","freshness-boundary":"2026-11-02T12:00:00Z","stale":"2026-11-02T12:00:00.000000001Z"}
    assert report["asOf"]==times[scenario]
    rows=expected_rows(scenario)
    assert len(report["rows"])==11
    for actual,expected in zip(report["rows"],rows):
        assert {k:actual[k] for k in expected}==expected
        assert actual["observedAt"]==times["baseline"] and actual["asOf"]==times[scenario]
        assert actual["version"]=="simulated-asset-v1" and actual["source"]=="synthetic" and actual["observationId"]=="synthetic-assets-v1"
        assert actual["tenantId"]==report["tenantId"] and actual["domainId"]==report["domainId"]
    summary=report["summary"]
    assert summary["total"]==11 and summary["complete"] is False and summary["kinds"]==dict(user=4,computer=3,group=4)
    assert summary["accountStatus"]==({"unknown":7,"not_applicable":4} if scenario=="stale" else {"enabled":3,"disabled":1,"locked":1,"unknown":2,"not_applicable":4})
    assert summary["activity"]==({"unknown":7,"not_applicable":4} if scenario=="stale" else {"recent":1 if scenario=="freshness-boundary" else 2,"inactive":3 if scenario=="freshness-boundary" else 2,"never_observed":1,"unknown":2,"not_applicable":4})
    assert summary["groupType"]==({"unknown":4,"not_applicable":7} if scenario=="stale" else {"security":2,"distribution":1,"unknown":1,"not_applicable":7})
    assert summary["groupScope"]==({"unknown":4,"not_applicable":7} if scenario=="stale" else {"global":1,"domain_local":1,"universal":1,"unknown":1,"not_applicable":7})
    expected_reasons={"asset_kind":22,"stale_observation":22} if scenario=="stale" else {"asset_kind":22,"explicit_flags_clear":3,"explicit_disabled_flag":1,"explicit_locked_flag":1,"missing_evidence":6,"last_login_within_window":1 if scenario=="freshness-boundary" else 2,"last_login_at_or_before_boundary":3 if scenario=="freshness-boundary" else 2,"explicit_never_assertion":1,"explicit_security_flag":3,"explicit_scope":3}
    assert summary["reasons"]==expected_reasons

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
            locations.update(re.findall(r"\b(integration_test\.go|simulation_asset_assessment_integration_test\.go|simulation_asset_assessment_upgrade_integration_test\.go):([0-9]{1,6}):", output))
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
    evidence = ROOT / "artifacts/simulation-asset-assessment"
    evidence.mkdir(parents=True, exist_ok=True)
    report = {"simulation": True, "realAD": False, "head": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "status": "running", "checks": [], "postgresTests": []}
    name = "adtr-simulation-asset-assessment-e2e-" + uuid.uuid4().hex[:12]
    owner = uuid.uuid4().hex
    created, server = False, None
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
                   ADTR_SIMULATION_ASSET_ASSESSMENT_ENABLED="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DOMAIN_PROBE_ENABLED="false")
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
INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES ('default','{ROLE}','Synthetic asset assessment operator'),('other','{ROLE}','Other synthetic operator');
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 SELECT tenant_id,id,mark,true,mark<>'domains' FROM adtr.access_roles CROSS JOIN (VALUES('simulation_asset_assessment'),('domains')) p(mark) WHERE id='{ROLE}';
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
        listing=get("/observation",observationId="synthetic-assets-v1")
        anonymous.call(listing,want=401);admin.call(listing,want=403)
        client.call(listing.replace("domain-a","domain-b"),want=404)
        observation=client.call(listing)
        assert observation["id"]=="synthetic-assets-v1" and len(observation["objects"])==11 and observation["complete"] is False
        body=dict(domain,id="http-baseline",operationId="http-baseline-op",observationId=observation["id"],expectedSourceHash=observation["hash"],scenarioId="baseline")
        client.call(API+"/create",dict(body,expectedSourceHash="0"*64),want=409)
        client.call(API+"/create",dict(body,passed=True),want=400)
        client.call(API+"/create",body,csrf=False,want=403)
        receipt=client.call(API+"/create",body)
        validate_report(receipt["report"],"baseline")
        assert receipt["input"]=={k:v for k,v in body.items() if k!="domainId"}
        assert receipt["report"]["sourceHash"]==observation["hash"] and receipt["operationId"]==body["operationId"]
        client.call(API+"/create",dict(body,scenarioId="stale"),want=409)
        assert client.call(API+"/create",body)==receipt
        assert client.call(get("/receipt",operationId=body["operationId"]))==receipt
        path=get("/detail",id=body["id"])
        assert client.call(path)==receipt["report"]
        peer.call(path,want=404);other.call(path,want=404)
        filters=dict(kind="",accountStatus="",activity="",groupType="",groupScope="")
        combined=[]
        for offset in (0,4,8):
            page=client.call(get("/rows",id=body["id"],offset=offset,limit=4,**filters))
            assert page["matched"]==11 and page["summary"]==receipt["report"]["summary"]
            combined+=page["rows"]
        assert combined==receipt["report"]["rows"]
        filtered=client.call(get("/rows",id=body["id"],offset=0,limit=20,**dict(filters,kind="group")))
        assert filtered["matched"]==4 and filtered["summary"]==receipt["report"]["summary"]
        assert filtered["rows"]==[r for r in combined if r["kind"]=="group"]
        for scenario in ("freshness-boundary","stale"):
            result=client.call(API+"/create",dict(body,id="http-"+scenario,operationId="http-"+scenario+"-op",scenarioId=scenario))
            validate_report(result["report"],scenario)
        history=client.call(get("/history",offset=0,limit=20));assert history["total"]==3
        stop();start()
        assert client.call(path)==receipt["report"]
        assert client.call(API+"/create",body)==receipt
        assert client.call(get("/history",offset=0,limit=20))==history
        report["checks"].append("synchronous real Evaluate; independent 11-object oracle and full counts for baseline/exact24h/stale; filtering/pagination, replay, persistence/restart and owner isolation")
        for package,test_name in NAMED_TESTS:
            record={"name":test_name,"status":"running","skipped":False}
            report["postgresTests"].append(record)
            try:
                run_go_integration(package,"^"+test_name+"$",test_name,dict(env,ADTR_TEST_DATABASE_URL=env["ADTR_DATABASE_URL"]))
            except BaseException:
                record["status"]="failed"
                raise
            record["status"]="passed"
        browser_env=dict({k:v for k,v in env.items() if not k.upper().startswith(("ADTR_","POSTGRES_","PG"))},ADTR_E2E_BASE_URL=base,ADTR_E2E_USERNAME="simulation-operator",ADTR_E2E_PASSWORD=account_password)
        run(["npx","--no-install","playwright","test","--config","simulation-asset-assessment.playwright.config.ts","simulation-asset-assessment-e2e/asset-assessment.spec.ts"],cwd=ROOT/"web",env=browser_env,timeout=240)
        sql(f"UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_asset_assessment'")
        client.call(path,want=403)
        sql(f"UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='default' AND role_id='{ROLE}' AND mark='simulation_asset_assessment'")
        client.call(path,want=404)
        client.call(get("/receipt",operationId=body["operationId"]),want=404)
        assert client.call(get("/history",offset=0,limit=20))["total"]==0
        client.call(API+"/create",dict(body,id="regrant-new-id"),want=409)
        report["checks"].append("actual browser create/history/detail/recovery; current permission and regrant epoch fencing")
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
