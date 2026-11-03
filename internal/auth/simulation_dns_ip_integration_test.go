//go:build integration

package auth

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/dnsipassets"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/simulationassetassessment"
	"github.com/yunpiao/adtr/internal/simulationdetection"
	"github.com/yunpiao/adtr/internal/simulationdetectionexceptions"
	"github.com/yunpiao/adtr/internal/simulationdetectionschedules"
	"github.com/yunpiao/adtr/internal/simulationdnsip"
	"github.com/yunpiao/adtr/internal/simulationevents"
	"github.com/yunpiao/adtr/internal/simulationlabels"
	"github.com/yunpiao/adtr/internal/simulationrisk"
	"github.com/yunpiao/adtr/internal/simulationrulecatalog"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Requires an isolated PostgreSQL database; missing DSN fails rather than skips.
func TestSimulationDNSIPHTTPWorkerDownloadAuthorityAndRevocation(t *testing.T) {
	f := newTaskFixture(t)
	f.exec(SimulationEventPermissionSchema + simulationevents.SchemaSQL + simulationevents.ImmutableSchemaSQL)
	f.exec(SimulationRiskPermissionSchema + simulationrisk.SchemaSQL + simulationrisk.ImmutableSchemaSQL)
	f.exec(SimulationDetectionPermissionSchema + simulationdetection.SchemaSQL + simulationdetection.ImmutableSchemaSQL)
	f.exec(SimulationDetectionSchedulesPermissionSchema + simulationdetectionschedules.SchemaSQL + simulationdetectionschedules.ImmutableSchemaSQL)
	f.exec(SimulationLabelsPermissionSchema + simulationlabels.SchemaSQL + simulationlabels.ImmutableSchemaSQL)
	f.exec(SimulationRuleCatalogPermissionSchema + simulationrulecatalog.SchemaSQL + simulationrulecatalog.ImmutableSchemaSQL)
	f.exec(SimulationDetectionExceptionsPermissionSchema + simulationdetectionexceptions.SchemaSQL + simulationdetectionexceptions.ImmutableSchemaSQL)
	f.exec(SimulationDNSIPPermissionSchema + simulationdnsip.SchemaSQL + simulationdnsip.ImmutableSchemaSQL)
	f.exec(SimulationAssetAssessmentPermissionSchema + simulationassetassessment.SchemaSQL + simulationassetassessment.ImmutableSchemaSQL)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	f.seedGroup("one", "dns-writers", narrowResourceRole, "domain-a")
	f.seedGroup("one", "dns-readers", widerResourceRole, "domain-a")
	f.exec("UPDATE adtr.users SET role_id=$1 WHERE id=$2", widerResourceRole, f.viewer)
	authorize := simulationDNSIPTaskAuthorizer(f.s.now)
	engine, e := tasks.New(f.s.database, tasks.ProductionRegistry(simulationdnsip.NewKind(authorize)), authorize, schemaversion.Current)
	if e != nil {
		t.Fatal(e)
	}
	enabled := true
	call := func(c *resourceTestClient, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		method := "GET"
		var raw []byte
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, simulationDNSIPPath+path, bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c.cookie)
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
		w := httptest.NewRecorder()
		f.s.SimulationDNSIPHandler(enabled, engine).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got%d want%d: %s", path, w.Code, want, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache")
		}
		if want == 200 && w.Header().Get("X-ADTR-User-ID") == "" {
			t.Fatal("actor missing")
		}
		return w
	}
	object := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var o map[string]any
		if json.Unmarshal(w.Body.Bytes(), &o) != nil {
			t.Fatal("not object", w.Body)
		}
		return o
	}
	obsPath := "/observations?domainId=domain-a"
	call(nil, obsPath, nil, 401)
	call(f.client(f.admin), obsPath, nil, 403)
	call(f.client(f.operator), obsPath, nil, 403)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'simulation_dns_ip',true,true),('one',$2,'simulation_dns_ip',true,false)", narrowResourceRole, widerResourceRole)
	author, reader := f.client(f.operator), f.client(f.viewer)
	call(author, obsPath, nil, 200)
	call(reader, obsPath, nil, 200)
	call(author, "/observations?domainId=domain-b", nil, 404)
	call(f.client(f.other), obsPath, nil, 403)
	tx, e := f.conn.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = tasks.CheckSchemaTx(f.ctx, tx, schemaversion.Current); e != nil {
		t.Fatal(e)
	}
	scope := simulationdnsip.Scope{Tenant: "one", Domain: "domain-a", ActorID: f.operator, Read: true, Manage: true}
	obs, e := simulationdnsip.SeedFixtureTx(f.ctx, tx, scope, simulationdnsip.Fixture{ID: "fixed", DNSDomain: "Example.TEST.", Source: "SIMULATED-auth", Records: []dnsipassets.Record{
		{ID: "v4", Domain: "EXAMPLE.test", DNSName: "HOST.example.test.", IP: "192.0.2.2", Observation: "fixed", Source: "SIMULATED-auth", Version: dnsipassets.Version, Simulation: true},
		{ID: "v6", Domain: "example.test", DNSName: "host.example.test", IP: "2001:db8::1", Observation: "fixed", Source: "SIMULATED-auth", Version: dnsipassets.Version, Simulation: true},
	}})
	if e != nil {
		tx.Rollback(f.ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	listed := object(call(author, "/records?domainId=domain-a&observationId=fixed&keyword=&family=&offset=0&limit=1", nil, 200))
	if listed["total"] != float64(2) || len(listed["rows"].([]any)) != 1 {
		t.Fatal("page", listed)
	}
	// DNS names are not resource IDs: reject their syntax before authorization.
	invalidScope := object(call(author, "/records?domainId=example.test&observationId=fixed&keyword=&family=&offset=0&limit=1", nil, 400))
	if invalidScope["error"] != "invalid_input" {
		t.Fatal("DNS name accepted as resource scope", invalidScope)
	}
	// A syntactically valid but unauthorized resource must remain hidden.
	unauthorizedScope := object(call(author, "/records?domainId=domain-b&observationId=fixed&keyword=&family=&offset=0&limit=1", nil, 404))
	if unauthorizedScope["error"] != "not_found" {
		t.Fatal("unauthorized resource scope exposed", unauthorizedScope)
	}
	input := map[string]any{"domainId": "domain-a", "observationId": "fixed", "observationHash": obs.Hash, "keyword": "", "family": "", "operationId": "export-all"}
	bad := *author
	bad.csrf = "forged"
	call(&bad, "/export", input, 403)
	call(reader, "/export", input, 403)
	created := call(author, "/export", input, 200)
	replay := call(author, "/export", input, 200)
	var receipt simulationdnsip.Receipt
	if json.Unmarshal(created.Body.Bytes(), &receipt) != nil || receipt.Submission == nil || receipt.Action != "export" || receipt.Intent.Export == nil || receipt.Intent.Export.ObservationHash != obs.Hash {
		t.Fatal("receipt contract", created.Body)
	}
	var replayReceipt simulationdnsip.Receipt
	if err := json.Unmarshal(replay.Body.Bytes(), &replayReceipt); err != nil || replayReceipt.Submission == nil {
		t.Fatal("replay receipt contract", replay.Body, err)
	}
	if receipt.Submission.Replayed || !replayReceipt.Submission.Replayed {
		t.Fatal("expected first submission then an explicit idempotent replay")
	}
	if receipt.ActorID != f.operator || receipt.OperationID != "export-all" || receipt.Intent.DomainID != "domain-a" || receipt.RequestHash == "" || receipt.Submission.Task.ID == "" {
		t.Fatal("receipt identity", receipt)
	}
	// Replayed is the sole intentional response difference. Compare typed
	// receipts after normalizing it, including task identity, intent and hash.
	replayReceipt.Submission.Replayed = false
	if !reflect.DeepEqual(receipt, replayReceipt) {
		t.Fatal("replay changed receipt identity, task or normalized intent", receipt, replayReceipt)
	}
	taskID := receipt.Submission.Task.ID
	input["family"] = "ipv4"
	call(author, "/export", input, 409)
	input["family"] = ""
	var taskCount, receiptCount, auditCount int
	if err := f.conn.QueryRow(f.ctx, `SELECT
		(SELECT count(*) FROM adtr.tasks WHERE kind=$1),
		(SELECT count(*) FROM adtr.simulation_dns_ip_receipts),
		(SELECT count(*) FROM adtr.auth_audit WHERE action='simulation_dns_ip_export')`, simulationdnsip.KindName).Scan(&taskCount, &receiptCount, &auditCount); err != nil {
		t.Fatal("replay atomicity counts", err)
	}
	if taskCount != 1 || receiptCount != 1 || auditCount != 1 {
		t.Fatalf("replay/conflict duplicated writes: tasks=%d receipts=%d audits=%d", taskCount, receiptCount, auditCount)
	}
	call(reader, "/task?domainId=domain-a&taskId="+taskID, nil, 404)
	call(reader, "/receipt?domainId=domain-a&operationId=export-all", nil, 404)
	call(author, "/download?domainId=domain-a&taskId="+taskID, nil, 409)
	worker, e := tasks.NewWorker(engine, tasks.WorkerConfig{Owner: "dns-auth-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	ctx, stop := context.WithTimeout(f.ctx, 20*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { stop(); <-done }()
	for {
		v := object(call(author, "/task?domainId=domain-a&taskId="+taskID, nil, 200))
		state := v["task"].(map[string]any)["state"].(string)
		if state == string(tasks.Succeeded) {
			break
		}
		if tasks.State(state).Terminal() {
			t.Fatal("worker failed", state)
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
	download := call(author, "/download?domainId=domain-a&taskId="+taskID, nil, 200)
	sum := sha256.Sum256(download.Body.Bytes())
	if download.Header().Get("X-ADTR-SHA256") != hex.EncodeToString(sum[:]) || download.Header().Get("X-ADTR-Row-Count") != "2" || download.Header().Get("Content-Length") != strconv.Itoa(download.Body.Len()) || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("download headers")
	}
	z, e := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range z.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Contains(b, []byte("192.0.2.2")) || !bytes.Contains(b, []byte("2001:db8::1")) {
				t.Fatal("not all matching rows")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing worksheet")
	}
	call(reader, "/download?domainId=domain-a&taskId="+taskID, nil, 404)
	enabled = false
	call(author, "/download?domainId=domain-a&taskId="+taskID, nil, 404)
	enabled = true
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_dns_ip'", narrowResourceRole)
	call(author, "/download?domainId=domain-a&taskId="+taskID, nil, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=true,writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='simulation_dns_ip'", narrowResourceRole)
	author = f.client(f.operator)
	call(author, "/download?domainId=domain-a&taskId="+taskID, nil, 404)
	call(author, "/task?domainId=domain-a&taskId="+taskID, nil, 404)
	call(author, "/receipt?domainId=domain-a&operationId=export-all", nil, 404)
	call(author, obsPath, nil, 200)
}
