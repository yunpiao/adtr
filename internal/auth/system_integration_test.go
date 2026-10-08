//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/tasks"
)

type systemFixture struct {
	*taskFixture
	store *systemhealth.Store
}

func newSystemFixture(t *testing.T) *systemFixture {
	t.Helper()
	f := newResourceFixture(t) // Real isolated PostgreSQL is mandatory; never skip.
	// Installation owner is a server constant, never supplied by the browser.
	f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name,remark) SELECT 'default',id,name,remark FROM adtr.access_roles WHERE tenant_id='one';
INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) SELECT 'default',role_id,mark,readable,writeable FROM adtr.access_permissions WHERE tenant_id='one';
UPDATE adtr.users SET tenant_id='default' WHERE tenant_id='one';`)
	// Finish the fixture migration after establishing identities; schema 5
	// deliberately makes user IDs and tenant ownership immutable thereafter.
	f.exec(TaskAuthorizationSchema + tasks.Schema + audit.Schema + systemhealth.Schema + `CREATE TABLE adtr.schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),version integer NOT NULL CHECK(version>0)); INSERT INTO adtr.schema_version(singleton,version) VALUES(true,7);`)
	store, err := systemhealth.NewStore(f.s.database, 7)
	if err != nil {
		t.Fatal(err)
	}
	return &systemFixture{&taskFixture{resourceFixture: f}, store}
}

func (f *systemFixture) response(c *resourceTestClient, method, path, raw string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, "/api/system"+path, strings.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.SystemHandler(f.store).ServeHTTP(w, r)
	return w
}

func (f *systemFixture) callSystem(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method, raw := "GET", []byte(nil)
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	w := f.response(c, method, path, string(raw))
	if w.Code != want {
		f.t.Fatalf("system %s got %d want %d: %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func systemAlarmBody(percent int, revision int64) map[string]any {
	return map[string]any{"instance": "local-api", "storageId": "runtime-root", "setType": "alarm", "percent": percent, "expectedRevision": revision}
}

func (f *systemFixture) seedObservations() (time.Time, time.Time) {
	f.t.Helper()
	first := time.Now().UTC().Truncate(time.Second).Add(-35 * time.Second)
	at, counter := first, 100
	sampler, err := systemhealth.NewSampler(systemhealth.Config{
		Now: func() time.Time { return at },
		ReadProc: func(file systemhealth.ProcFile) ([]byte, error) {
			switch file {
			case systemhealth.ProcStat:
				return fmt.Appendf(nil, "cpu %d 0 0 %d 0 0 0 0 0 0\n", counter, 100+(counter-100)*3), nil
			case systemhealth.ProcMeminfo:
				return []byte("MemTotal: 1000 kB\nMemAvailable: 400 kB\n"), nil
			case systemhealth.ProcUptime:
				return []byte("3600.0 1800.0\n"), nil
			case systemhealth.ProcLoadavg:
				return []byte("0.1 0.2 0.3 1/100 123\n"), nil
			default:
				return nil, fmt.Errorf("unexpected synthetic source")
			}
		},
		StatFS: func(path string) (systemhealth.FileSystemStats, error) {
			if path != "/" {
				return systemhealth.FileSystemStats{}, fmt.Errorf("unexpected synthetic path")
			}
			return systemhealth.FileSystemStats{BlockSize: 4096, Blocks: 100, FreeBlocks: 20, AvailableBlocks: 10, FS: "synthetic"}, nil
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.store.Persist(f.ctx, sampler.Sample()); err != nil {
		f.t.Fatal(err)
	}
	at, counter = at.Add(15*time.Second), 110
	if err = f.store.Persist(f.ctx, sampler.Sample()); err != nil {
		f.t.Fatal(err)
	}
	return first, at
}

func TestSystemHTTPRealOwnerScopeAndCurrentObservations(t *testing.T) {
	f := newSystemFixture(t)
	admin, operator := f.client(f.admin), f.client(f.operator)
	f.callSystem(nil, "/info", nil, 401)
	f.callSystem(f.client(f.viewer), "/info", nil, 403)
	f.callSystem(operator, "/info", nil, 403)
	for _, path := range []string{"/info", "/nodes", "/resources/current?instance=local-api", "/resources/history?instance=local-api&startTime=0&endTime=10&graphType=cpu_basic", "/storage?instance=local-api", "/services", "/health"} {
		f.callSystem(f.client(f.other), path, nil, 403)
	}
	empty := f.callSystem(admin, "/resources/current?instance=local-api", nil, 200)
	if empty["snapshot"] != nil || empty["availability"] != "unavailable" || empty["reason"] != "no_samples" {
		t.Fatal("manufactured empty observation", empty)
	}
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'system',true,false)`, narrowResourceRole)
	f.callSystem(operator, "/info", nil, 200)
	f.callSystem(operator, "/storage/settings", f.proof(systemAlarmBody(87, 0)), 403)
	first, last := f.seedObservations()
	current := f.callSystem(admin, "/resources/current?instance=local-api", nil, 200)
	snapshot := current["snapshot"].(map[string]any)
	if snapshot["cpu"].(map[string]any)["percent"] != float64(25) || snapshot["memory"].(map[string]any)["percent"] != float64(60) {
		t.Fatal("measurement was not read from persistence", current)
	}
	info := f.callSystem(admin, "/info", nil, 200)
	if info["status"].(map[string]any)["cpuUsagePercent"] != float64(25) || info["version"].(map[string]any)["engineVersion"] != nil {
		t.Fatal(info)
	}
	history := f.callSystem(admin, fmt.Sprintf("/resources/history?instance=local-api&startTime=%d&endTime=%d&graphType=cpu_basic", first.Add(-time.Minute).Unix(), last.Add(time.Second).Unix()), nil, 200)
	data := history["info"].([]any)[0].(map[string]any)["data"].(map[string]any)
	values, timestamps := data["value"].([]any), data["timestamp"].([]any)
	if len(values) != 1 || values[0] != "25" || timestamps[0] != float64(last.Unix()) || len(history["gaps"].([]any)) == 0 {
		t.Fatal("first CPU sample became zero or historical gap disappeared", history)
	}
	storage := f.callSystem(admin, "/storage?instance=local-api&page=1&pageSize=10", nil, 200)
	row := storage["storage"].([]any)[0].(map[string]any)
	if row["totalBytes"] != "409600" || row["usedBytes"] != "327680" || row["freeBytes"] != "40960" || row["mount"] != "/" || row["scope"] != "runtime_filesystem" || row["alarmExceeded"] != true {
		t.Fatal(storage)
	}
	page := f.callSystem(admin, "/storage?instance=local-api&page=2&pageSize=10", nil, 200)
	if page["total"] != float64(1) || len(page["storage"].([]any)) != 0 {
		t.Fatal(page)
	}
	f.callSystem(admin, "/resources/current?instance=unknown", nil, 404)
	f.callSystem(admin, fmt.Sprintf("/resources/history?instance=local-api&startTime=%d&endTime=%d&graphType=disk_usage&storageId=unknown", first.Unix(), last.Unix()), nil, 404)
	f.exec(`UPDATE adtr.access_permissions SET readable=false WHERE tenant_id='default' AND role_id=$1 AND mark='system'`, narrowResourceRole)
	f.callSystem(operator, "/info", nil, 403)
}

func TestSystemHTTPFreshProofCASAndAtomicAudit(t *testing.T) {
	f := newSystemFixture(t)
	admin := f.client(f.admin)
	f.seedObservations()
	body := f.proof(systemAlarmBody(90, 0))
	out := f.callSystem(admin, "/storage/settings", body, 200)
	if out["result"] != float64(1) || out["setting"].(map[string]any)["revision"] != float64(1) || out["setting"].(map[string]any)["storageId"] != "runtime-root" {
		t.Fatal(out)
	}
	f.callSystem(admin, "/storage/settings", body, 401) // Consumed TOTP cannot replay.
	f.callSystem(admin, "/storage/settings", f.proof(systemAlarmBody(89, 0)), 409)
	wrong := f.proof(systemAlarmBody(89, 1))
	wrong["actorPassword"] = "incorrect"
	f.callSystem(admin, "/storage/settings", wrong, 401)
	var previous, percent int
	var revision int64
	if err := f.conn.QueryRow(f.ctx, `SELECT previous_percent,alarm_percent,revision FROM adtr.system_alarm_audit WHERE tenant_id='default'`).Scan(&previous, &percent, &revision); err != nil || previous != 85 || percent != 90 || revision != 1 {
		t.Fatal(previous, percent, revision, err)
	}
	var actor int64
	var name, ip, path, requestID string
	if err := f.conn.QueryRow(f.ctx, `SELECT actor_id,audit_username,audit_ip,audit_path,audit_request_id FROM adtr.auth_audit WHERE tenant_id='default' AND action='system_storage_alarm'`).Scan(&actor, &name, &ip, &path, &requestID); err != nil || actor != f.admin || name != "admin" || ip != "127.0.0.1" || path != "/api/system/storage/settings" || requestID == "" {
		t.Fatal(actor, name, ip, path, requestID, err)
	}
	// A unified-audit failure rolls back the setting, module ledger and proof.
	f.exec(`CREATE FUNCTION adtr.reject_system_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='system_storage_alarm' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF;RETURN NEW;END;$$; CREATE TRIGGER reject_system_test_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_system_test_audit();`)
	retry := f.proof(systemAlarmBody(88, 1))
	f.callSystem(admin, "/storage/settings", retry, 500)
	if err := f.conn.QueryRow(f.ctx, `SELECT alarm_percent,revision FROM adtr.system_alarm_settings WHERE tenant_id='default'`).Scan(&percent, &revision); err != nil || percent != 90 || revision != 1 {
		t.Fatal("mutation escaped audit rollback", percent, revision, err)
	}
	f.exec(`DROP TRIGGER reject_system_test_audit ON adtr.auth_audit`)
	f.callSystem(admin, "/storage/settings", retry, 200)
	var count int
	if err := f.conn.QueryRow(f.ctx, `SELECT count(*) FROM adtr.system_alarm_audit`).Scan(&count); err != nil || count != 2 {
		t.Fatal("partial or duplicate audit", count, err)
	}
	// Reconstructing Store proves settings survive process-local state loss.
	var err error
	f.store, err = systemhealth.NewStore(f.s.database, 7)
	if err != nil {
		t.Fatal(err)
	}
	row := f.callSystem(admin, "/storage?instance=local-api", nil, 200)["storage"].([]any)[0].(map[string]any)
	if row["alarmPercent"] != float64(88) || row["revision"] != float64(2) {
		t.Fatal(row)
	}
}

func TestSystemHTTPOriginCSRFMFAAndConcurrentCAS(t *testing.T) {
	f := newSystemFixture(t)
	admin, operator := f.client(f.admin), f.client(f.operator)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,'system',true,true)`, narrowResourceRole)
	body := f.proof(systemAlarmBody(86, 0))
	raw, _ := json.Marshal(body)
	for _, field := range []string{"origin", "csrf"} {
		r := httptest.NewRequest("POST", "/api/system/storage/settings", bytes.NewReader(raw))
		r.Header.Set("Origin", f.s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", admin.csrf)
		r.AddCookie(admin.cookie)
		if field == "origin" {
			r.Header.Set("Origin", "https://foreign.test")
		} else {
			r.Header.Set("X-CSRF-Token", "invalid")
		}
		w := httptest.NewRecorder()
		f.s.SystemHandler(f.store).ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(field, w.Code, w.Body)
		}
	}
	// Distinct authorized actors contend for one revision. Proof, CAS and both
	// ledgers serialize in PostgreSQL; exactly one response succeeds.
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, client := range []*resourceTestClient{admin, operator} {
		wg.Add(1)
		go func(c *resourceTestClient) {
			defer wg.Done()
			results <- f.response(c, "POST", "/storage/settings", string(raw)).Code
		}(client)
	}
	wg.Wait()
	close(results)
	seen := map[int]int{}
	for status := range results {
		seen[status]++
	}
	if seen[200] != 1 || seen[409] != 1 {
		t.Fatal(seen)
	}
	f.exec(`UPDATE adtr.users SET mfa_secret='' WHERE id=$1`, f.admin)
	f.callSystem(admin, "/storage/settings", f.proof(systemAlarmBody(87, 1)), 403)
	f.exec(`UPDATE adtr.users SET must_change=true WHERE id=$1`, f.operator)
	f.callSystem(operator, "/health", nil, 403)
	foreign := f.client(f.other)
	f.callSystem(foreign, "/storage/settings", f.proof(systemAlarmBody(87, 1)), 403)
	f.exec(`DELETE FROM adtr.sessions WHERE user_id=$1`, f.other)
	f.callSystem(foreign, "/health", nil, 401)
}

func TestSystemHTTPSchemaPregateOutageAndHealthEvidence(t *testing.T) {
	f := newSystemFixture(t)
	admin := f.client(f.admin)
	health := f.callSystem(admin, "/health", nil, 200)
	if health["result"] != "degraded" || health["worker"].(map[string]any)["reason"] != "no_worker_activity" {
		t.Fatal("never observed worker was healthy", health)
	}
	f.seedObservations()
	for _, cycle := range []systemhealth.WorkerCycle{systemhealth.WorkerQueue, systemhealth.WorkerRecovery} {
		if err := f.store.RecordWorkerCycle(f.ctx, "synthetic-worker", cycle, systemhealth.WorkerCycleSuccess, ""); err != nil {
			t.Fatal(err)
		}
	}
	health = f.callSystem(admin, "/health", nil, 200)
	if health["result"] != "healthy" {
		t.Fatal(health)
	}
	for _, dep := range health["dependencies"].([]any) {
		d := dep.(map[string]any)
		if (d["id"] == "cache" || d["id"] == "engine") && d["status"] != "not_configured" {
			t.Fatal("fabricated dependency", d)
		}
	}
	f.exec(`UPDATE adtr.system_worker_activity SET last_activity_at=clock_timestamp()-interval '16 seconds'`)
	if f.callSystem(admin, "/health", nil, 200)["result"] != "degraded" {
		t.Fatal("stale worker accepted")
	}
	// Schema incompatibility is checked before a deliberately blocked identity
	// lock, even for a previously authenticated cookie and for an absent cookie.
	f.exec(`UPDATE adtr.schema_version SET version=6 WHERE singleton=true`)
	locker, err := pgx.ConnectConfig(f.ctx, f.s.database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close(f.ctx)
	tx, err := locker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:default',0))`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, c := range []*resourceTestClient{admin, nil} {
		out := f.callSystem(c, "/health", nil, 503)
		if out["error"] != "schema_incompatible" {
			t.Fatal(out)
		}
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("schema gate waited for identity lock")
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE adtr.schema_version SET version=7 WHERE singleton=true`)
	f.callSystem(admin, "/health", nil, 200)
	// A failed connection does not reuse the previously successful identity.
	broken := *f.s
	broken.database = f.s.database.Copy()
	broken.database.Database = "adtr_nonexistent_synthetic_health_database"
	r := httptest.NewRequest("GET", "/api/system/health", nil)
	r.AddCookie(admin.cookie)
	w := httptest.NewRecorder()
	broken.SystemHandler(f.store).ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), broken.database.Database) || strings.Contains(w.Body.String(), "dependencies") {
		t.Fatal(w.Code, w.Body)
	}
	// Migration lock contention itself remains bounded by the two-second gate.
	tx, err = locker.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, "SELECT pg_advisory_xact_lock(734192801::bigint)"); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	f.callSystem(admin, "/info", nil, 503)
	if time.Since(start) > 3*time.Second {
		t.Fatal("unbounded schema gate")
	}
}
