//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/tasks"
)

type scheduleFixture struct {
	*taskFixture
	scheduler *schedules.Engine
	startAt   string
}

func newScheduleFixture(t *testing.T) *scheduleFixture {
	t.Helper()
	f := newTaskFixture(t) // Real isolated PostgreSQL; unavailable means failure.
	f.exec(SchedulePermissionSchema + schedules.Schema + DomainPermissionMarks + OperationAccountPermissionMarks)
	f.exec("UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", schedules.SchemaVersion)
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one',$1,'schedules',true,true)", narrowResourceRole)
	e, err := tasks.New(f.s.database, tasks.ProductionRegistry(), taskAuthorizer(f.s.now), schedules.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = e
	scheduler, err := schedules.New(f.s.database, e, scheduleAuthorizer(f.s.now))
	if err != nil {
		t.Fatal(err)
	}
	return &scheduleFixture{taskFixture: f, scheduler: scheduler, startAt: f.now.Add(time.Hour).Format(time.RFC3339)}
}

func (f *scheduleFixture) createBody(key string) map[string]any {
	return map[string]any{
		"label": "Synthetic health", "taskName": "infrastructure.health", "domainId": "platform",
		"payloadVersion": 1, "payload": map[string]any{}, "startAt": f.startAt,
		"intervalSeconds": 3600, "idempotencyKey": key,
	}
}

func scheduleControlBody(id string, version int64, key string) map[string]any {
	return map[string]any{"scheduleUUID": id, "expectedControlVersion": version, "idempotencyKey": key}
}

func (f *scheduleFixture) callSchedule(c *resourceTestClient, path string, body map[string]any, want int) map[string]any {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "/api/tasks/schedules"+path, strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:5432"
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.SchedulesHandler(f.scheduler).ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("schedules %s got %d want %d: %s", path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func scheduleOutput(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	schedule, ok := out["schedule"].(map[string]any)
	if !ok {
		t.Fatal("missing schedule", out)
	}
	for _, field := range []string{"actorId", "tenantId", "authorizationVersion", "payload", "definitionHash", "creationKey", "nextIndex"} {
		if _, ok := schedule[field]; ok {
			t.Fatal("private schedule field disclosed", field)
		}
	}
	return schedule
}

func scheduleOutputID(t *testing.T, out map[string]any) string {
	t.Helper()
	id, ok := scheduleOutput(t, out)["scheduleUUID"].(string)
	if !ok || id == "" {
		t.Fatal("missing schedule UUID", out)
	}
	return id
}

func (f *scheduleFixture) proofStep(actor int64) int64 {
	f.t.Helper()
	var step int64
	if err := f.conn.QueryRow(f.ctx, "SELECT mfa_last_step FROM adtr.users WHERE id=$1", actor).Scan(&step); err != nil {
		f.t.Fatal(err)
	}
	return step
}

func (f *scheduleFixture) checkSchedulePaths(c *resourceTestClient, read, write bool) {
	f.t.Helper()
	paths := []string{
		"GET /api/tasks/schedules", "GET /api/tasks/schedules/detail", "POST /api/tasks/schedules/create",
		"POST /api/tasks/schedules/enable", "POST /api/tasks/schedules/pause", "GET /api/tasks/schedules/", "GET /api/tasks/schedules/create",
	}
	raw, err := json.Marshal(map[string]any{"paths": paths})
	if err != nil {
		f.t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/access/check", strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:5432"
	r.AddCookie(c.cookie)
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", c.csrf)
	w := httptest.NewRecorder()
	f.s.ServeAccessHTTP(w, r)
	if w.Code != 200 {
		f.t.Fatal("schedule access introspection failed", w.Code, w.Body)
	}
	var out struct {
		Results []bool `json:"results"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Results) != len(paths) {
		f.t.Fatal("invalid access introspection response", out, err)
	}
	for i, want := range []bool{read, read, write, write, write, false, false} {
		if out.Results[i] != want {
			f.t.Fatal("access introspection and schedule route grants disagree", paths[i], out.Results[i], want)
		}
	}
}

func TestScheduleHTTPFreshProofPausedCreateAndOwnerScope(t *testing.T) {
	f := newScheduleFixture(t)
	f.callSchedule(nil, "", nil, 401)
	f.callSchedule(f.client(f.viewer), "", nil, 403)
	operator := f.client(f.operator)
	admin := f.client(f.admin)
	foreign := f.client(f.other)
	initial := f.version(f.operator)
	badCSRF := *operator
	badCSRF.csrf = "wrong"
	f.callSchedule(&badCSRF, "/create", f.proof(f.createBody("bad-csrf")), 403)
	badPassword := f.proof(f.createBody("bad-password"))
	badPassword["actorPassword"] = "wrong password"
	f.callSchedule(operator, "/create", badPassword, 401)
	f.clearRate()
	good := f.proof(f.createBody("first"))
	out := f.callSchedule(operator, "/create", good, 200)
	id := scheduleOutputID(t, out)
	created := scheduleOutput(t, out)
	if out["replayed"] != false || created["state"] != "paused" || created["controlVersion"] != float64(1) || f.version(f.operator) != initial {
		t.Fatal("create state, version or proof epoch changed", out)
	}
	var actor int64
	var tenant, epoch string
	if err := f.conn.QueryRow(f.ctx, "SELECT actor_id,tenant_id,authorization_version FROM adtr.task_schedules WHERE schedule_id=$1", id).Scan(&actor, &tenant, &epoch); err != nil {
		t.Fatal(err)
	}
	if actor != f.operator || tenant != "one" || epoch != fmt.Sprint(initial) {
		t.Fatal("untrusted schedule principal persisted", actor, tenant, epoch)
	}
	f.callSchedule(operator, "/create", good, 401)
	replay := f.callSchedule(operator, "/create", f.proof(f.createBody("first")), 200)
	if replay["replayed"] != true || scheduleOutputID(t, replay) != id {
		t.Fatal("create replay lost original schedule", replay)
	}
	f.clearRate()
	for _, c := range []*resourceTestClient{admin, foreign} {
		f.callSchedule(c, "/detail?scheduleUUID="+id, nil, 404)
		list := f.callSchedule(c, "", nil, 200)
		if list["page"].(map[string]any)["total"] != float64(0) || len(list["schedules"].([]any)) != 0 {
			t.Fatal("creator scope leaked a schedule", list)
		}
		f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "foreign-enable")), 404)
		f.callSchedule(c, "/pause", f.proof(scheduleControlBody(id, 1, "foreign-pause")), 404)
		f.clearRate()
	}
	list := f.callSchedule(operator, "?state=paused&pageIdx=1&pageSize=1", nil, 200)
	if list["page"].(map[string]any)["total"] != float64(1) || len(list["schedules"].([]any)) != 1 {
		t.Fatal(list)
	}
	detail := f.callSchedule(operator, "/detail?scheduleUUID="+id+"&pageIdx=1&pageSize=1", nil, 200)
	if scheduleOutputID(t, detail) != id || len(detail["events"].([]any)) != 1 || detail["events"].([]any)[0].(map[string]any)["action"] != "created" {
		t.Fatal("missing durable creation history", detail)
	}
}

func TestScheduleHTTPControlReceiptsReplayAndVersionConflict(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.operator)
	id := scheduleOutputID(t, f.callSchedule(c, "/create", f.proof(f.createBody("control")), 200))
	epoch := f.version(f.operator)
	enable := f.proof(scheduleControlBody(id, 1, "enable-first"))
	out := f.callSchedule(c, "/enable", enable, 200)
	receipt := out["receipt"].(map[string]any)
	if out["replayed"] != false || scheduleOutput(t, out)["state"] != "enabled" || receipt["controlVersion"] != float64(2) || receipt["action"] != "enable" {
		t.Fatal(out)
	}
	f.callSchedule(c, "/enable", enable, 401)
	replay := f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "enable-first")), 200)
	if replay["replayed"] != true || replay["receipt"].(map[string]any)["operationUUID"] != receipt["operationUUID"] {
		t.Fatal("control replay created another operation", replay)
	}
	f.clearRate()
	paused := f.callSchedule(c, "/pause", f.proof(scheduleControlBody(id, 2, "pause-first")), 200)
	if scheduleOutput(t, paused)["state"] != "paused" || scheduleOutput(t, paused)["controlVersion"] != float64(3) {
		t.Fatal(paused)
	}
	replay = f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "enable-first")), 200)
	oldReceipt := replay["receipt"].(map[string]any)
	if replay["replayed"] != true || oldReceipt["state"] != "enabled" || oldReceipt["controlVersion"] != float64(2) || scheduleOutput(t, replay)["state"] != "paused" || scheduleOutput(t, replay)["controlVersion"] != float64(3) {
		t.Fatal("replay confused past receipt with current state", replay)
	}
	stale := f.proof(scheduleControlBody(id, 1, "stale-enable"))
	beforeStep := f.proofStep(f.operator)
	f.callSchedule(c, "/enable", stale, 409)
	if f.proofStep(f.operator) != beforeStep {
		t.Fatal("version conflict consumed proof")
	}
	stale["expectedControlVersion"] = 3
	out = f.callSchedule(c, "/enable", stale, 200)
	if scheduleOutput(t, out)["controlVersion"] != float64(4) || f.version(f.operator) != epoch {
		t.Fatal("control changed pinned actor epoch", out)
	}
	f.clearRate()
	conflict := f.proof(scheduleControlBody(id, 4, "enable-first"))
	f.callSchedule(c, "/pause", conflict, 409)
	var controls int
	var pinned string
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_schedule_operations WHERE schedule_id=$1", id).Scan(&controls); err != nil || controls != 3 {
		t.Fatal("replay/conflict added a control operation", controls, err)
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.task_schedules WHERE schedule_id=$1", id).Scan(&pinned); err != nil || pinned != fmt.Sprint(epoch) {
		t.Fatal("control refreshed schedule authorization", pinned, err)
	}
}

func TestScheduleHTTPCreateCanonicalTimestampAndIdempotencyConflict(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.operator)
	initial := f.callSchedule(c, "/create", f.proof(f.createBody("canonical")), 200)
	id := scheduleOutputID(t, initial)
	anchor, err := time.Parse(time.RFC3339, f.startAt)
	if err != nil {
		t.Fatal(err)
	}
	canonical := f.createBody("canonical")
	canonical["startAt"] = anchor.In(time.FixedZone("synthetic", 2*60*60)).Format(time.RFC3339)
	replay := f.callSchedule(c, "/create", f.proof(canonical), 200)
	if replay["replayed"] != true || scheduleOutputID(t, replay) != id || scheduleOutput(t, replay)["startAt"] != f.startAt {
		t.Fatal("equivalent qualified timestamp created another definition", replay)
	}
	for _, field := range []string{"label", "intervalSeconds", "startAt"} {
		changed := f.createBody("canonical")
		switch field {
		case "label":
			changed[field] = "Different health"
		case "intervalSeconds":
			changed[field] = 7200
		case "startAt":
			changed[field] = anchor.Add(time.Hour).Format(time.RFC3339)
		}
		before := f.proofStep(f.operator)
		out := f.callSchedule(c, "/create", f.proof(changed), 409)
		if out["error"] != "idempotency_conflict" || f.proofStep(f.operator) != before {
			t.Fatal("conflicting definition consumed proof or was accepted", field, out)
		}
		f.clearRate()
	}
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_schedules").Scan(&count); err != nil || count != 1 {
		t.Fatal("create retry created duplicate definitions", count, err)
	}
}

func TestScheduleHTTPRequiresBothCurrentFunctionGrants(t *testing.T) {
	for _, mark := range []string{"tasks", "schedules"} {
		for _, grant := range []string{"readable", "writeable"} {
			t.Run(mark+"/"+grant, func(t *testing.T) {
				f := newScheduleFixture(t)
				c := f.client(f.operator)
				f.checkSchedulePaths(c, true, true)
				id := scheduleOutputID(t, f.callSchedule(c, "/create", f.proof(f.createBody("grants")), 200))
				if grant == "readable" {
					f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
					f.callSchedule(c, "", nil, 403)
					f.callSchedule(c, "/detail?scheduleUUID="+id, nil, 403)
					f.checkSchedulePaths(c, false, false)
				} else {
					f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
					f.callSchedule(c, "", nil, 200)
					f.checkSchedulePaths(c, true, false)
				}
				f.callSchedule(c, "/create", f.proof(f.createBody("missing-grant")), 403)
				f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "missing-enable")), 403)
				f.clearRate()
				f.callSchedule(c, "/pause", f.proof(scheduleControlBody(id, 1, "missing-pause")), 403)
			})
		}
	}
}

func TestScheduleHTTPRejectsInvalidDefinitionsWithoutConsumingProof(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.operator)
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"blank label", "label", "  "}, {"long label", "label", strings.Repeat("界", 81)},
		{"control label", "label", "health\njob"}, {"unsupported kind", "taskName", "engineRestart"},
		{"unsupported export", "taskName", "audit.export"}, {"wrong scope", "domainId", "domain-a"},
		{"wrong version", "payloadVersion", 2}, {"nonempty payload", "payload", map[string]any{"command": "restart"}},
		{"short interval", "intervalSeconds", 59}, {"long interval", "intervalSeconds", 86401},
		{"negative interval", "intervalSeconds", -60}, {"past start", "startAt", "2020-01-01T00:00:00Z"},
		{"unqualified start", "startAt", "2026-10-08T00:00:00"},
		{"fractional start", "startAt", f.now.Add(time.Hour).Format("2006-01-02T15:04:05") + ".123Z"},
		{"offset hour overflow", "startAt", f.now.Add(72*time.Hour).Format("2006-01-02T15:04:05") + "+24:00"},
		{"offset minute overflow", "startAt", f.now.Add(72*time.Hour).Format("2006-01-02T15:04:05") + "+00:60"},
		{"empty key", "idempotencyKey", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := f.proof(f.createBody("invalid-" + strings.ReplaceAll(tc.name, " ", "-")))
			body[tc.field] = tc.value
			before := f.proofStep(f.operator)
			f.callSchedule(c, "/create", body, 400)
			if f.proofStep(f.operator) != before {
				t.Fatal("rejected definition consumed proof")
			}
			f.clearRate()
		})
	}
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_schedules").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid definition persisted", count, err)
	}
	f.callSchedule(c, "/create", f.proof(f.createBody("valid-after-rejection")), 200)
}

func TestScheduleHTTPRestoredGrantsCannotRefreshPinnedEpoch(t *testing.T) {
	for _, mark := range []string{"tasks", "schedules"} {
		t.Run(mark, func(t *testing.T) {
			f := newScheduleFixture(t)
			c := f.client(f.operator)
			id := scheduleOutputID(t, f.callSchedule(c, "/create", f.proof(f.createBody("old-definition")), 200))
			originalEpoch := f.version(f.operator)
			f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
			revokedEpoch := f.version(f.operator)
			f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark=$2", narrowResourceRole, mark)
			restoredEpoch := f.version(f.operator)
			if revokedEpoch <= originalEpoch || restoredEpoch <= revokedEpoch {
				t.Fatal("grant changes did not monotonically revoke old epoch", originalEpoch, revokedEpoch, restoredEpoch)
			}
			beforeStep := f.proofStep(f.operator)
			out := f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "old-enable")), 409)
			if out["error"] != "authorization_epoch_changed" || f.proofStep(f.operator) != beforeStep {
				t.Fatal("old definition was refreshed or consumed proof", out)
			}
			out = f.callSchedule(c, "/create", f.proof(f.createBody("old-definition")), 409)
			if out["error"] != "authorization_epoch_changed" || f.proofStep(f.operator) != beforeStep {
				t.Fatal("create replay refreshed old authorization", out)
			}
			var pinned, state string
			var controlVersion int64
			if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version,state,control_version FROM adtr.task_schedules WHERE schedule_id=$1", id).Scan(&pinned, &state, &controlVersion); err != nil || pinned != fmt.Sprint(originalEpoch) || state != "paused" || controlVersion != 1 {
				t.Fatal("rejected old epoch changed schedule", pinned, state, controlVersion, err)
			}
			fresh := scheduleOutputID(t, f.callSchedule(c, "/create", f.proof(f.createBody("new-definition")), 200))
			if fresh == id {
				t.Fatal("new authorized definition reused old schedule")
			}
			if err := f.conn.QueryRow(f.ctx, "SELECT authorization_version FROM adtr.task_schedules WHERE schedule_id=$1", fresh).Scan(&pinned); err != nil || pinned != fmt.Sprint(restoredEpoch) {
				t.Fatal("new schedule failed to pin current epoch", pinned, err)
			}
		})
	}
}

func TestScheduleHTTPRollsBackProofAndScheduleOnEventFailure(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.operator)
	beforeEpoch, beforeStep := f.version(f.operator), f.proofStep(f.operator)
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_schedule_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic schedule audit unavailable'; END; $$;
 CREATE TRIGGER reject_synthetic_schedule_event BEFORE INSERT ON adtr.task_schedule_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_schedule_event();`)
	body := f.proof(f.createBody("atomic-create"))
	f.callSchedule(c, "/create", body, 500)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_schedules)+(SELECT count(*) FROM adtr.task_schedule_events)+(SELECT count(*) FROM adtr.task_schedule_operations)").Scan(&count); err != nil || count != 0 {
		t.Fatal("schedule survived event failure", count, err)
	}
	if f.proofStep(f.operator) != beforeStep || f.version(f.operator) != beforeEpoch {
		t.Fatal("failed create changed proof or epoch")
	}
	f.exec("DROP TRIGGER reject_synthetic_schedule_event ON adtr.task_schedule_events")
	id := scheduleOutputID(t, f.callSchedule(c, "/create", body, 200))
	f.exec("CREATE TRIGGER reject_synthetic_schedule_event BEFORE INSERT ON adtr.task_schedule_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_synthetic_schedule_event()")
	beforeStep = f.proofStep(f.operator)
	body = f.proof(scheduleControlBody(id, 1, "atomic-enable"))
	f.callSchedule(c, "/enable", body, 500)
	var state string
	var version int64
	if err := f.conn.QueryRow(f.ctx, "SELECT state,control_version FROM adtr.task_schedules WHERE schedule_id=$1", id).Scan(&state, &version); err != nil || state != "paused" || version != 1 {
		t.Fatal("failed control changed schedule", state, version, err)
	}
	if f.proofStep(f.operator) != beforeStep || f.version(f.operator) != beforeEpoch {
		t.Fatal("failed control changed proof or epoch")
	}
	f.exec("DROP TRIGGER reject_synthetic_schedule_event ON adtr.task_schedule_events")
	f.callSchedule(c, "/enable", body, 200)
}

func TestScheduleHTTPSharedAuditIsAtomicAndReplayAddsNoSuccessAudit(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.operator)
	f.exec(`CREATE FUNCTION adtr.reject_synthetic_schedule_shared_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic shared audit unavailable'; END; $$;
 CREATE TRIGGER reject_synthetic_schedule_shared_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW WHEN (NEW.action LIKE 'schedule.%') EXECUTE FUNCTION adtr.reject_synthetic_schedule_shared_audit();`)
	body := f.proof(f.createBody("shared-atomic"))
	beforeEpoch, beforeStep := f.version(f.operator), f.proofStep(f.operator)
	f.callSchedule(c, "/create", body, 500)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_schedules)+(SELECT count(*) FROM adtr.task_schedule_events)+(SELECT count(*) FROM adtr.task_schedule_operations)").Scan(&count); err != nil || count != 0 {
		t.Fatal("schedule ledger survived shared audit failure", count, err)
	}
	if f.proofStep(f.operator) != beforeStep || f.version(f.operator) != beforeEpoch {
		t.Fatal("shared audit failure changed proof or epoch")
	}
	f.exec("DROP TRIGGER reject_synthetic_schedule_shared_audit ON adtr.auth_audit")
	id := scheduleOutputID(t, f.callSchedule(c, "/create", body, 200))
	f.callSchedule(c, "/create", f.proof(f.createBody("shared-atomic")), 200)
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action LIKE 'schedule.%'").Scan(&count); err != nil || count != 1 {
		t.Fatal("create replay duplicated shared success audit", count, err)
	}
	f.clearRate()
	f.exec("CREATE TRIGGER reject_synthetic_schedule_shared_audit BEFORE INSERT ON adtr.auth_audit FOR EACH ROW WHEN (NEW.action LIKE 'schedule.%') EXECUTE FUNCTION adtr.reject_synthetic_schedule_shared_audit()")
	body = f.proof(scheduleControlBody(id, 1, "shared-enable"))
	beforeStep = f.proofStep(f.operator)
	f.callSchedule(c, "/enable", body, 500)
	var state string
	var version int64
	if err := f.conn.QueryRow(f.ctx, "SELECT state,control_version FROM adtr.task_schedules WHERE schedule_id=$1", id).Scan(&state, &version); err != nil || state != "paused" || version != 1 {
		t.Fatal("control survived shared audit failure", state, version, err)
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_schedule_operations)+(SELECT count(*) FROM adtr.task_schedule_events)").Scan(&count); err != nil || count != 1 {
		t.Fatal("control receipt/event survived shared audit failure", count, err)
	}
	if f.proofStep(f.operator) != beforeStep || f.version(f.operator) != beforeEpoch {
		t.Fatal("failed shared control audit changed proof or epoch")
	}
	f.exec("DROP TRIGGER reject_synthetic_schedule_shared_audit ON adtr.auth_audit")
	f.callSchedule(c, "/enable", body, 200)
	f.callSchedule(c, "/enable", f.proof(scheduleControlBody(id, 1, "shared-enable")), 200)
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.auth_audit WHERE action LIKE 'schedule.%'").Scan(&count); err != nil || count != 2 {
		t.Fatal("control replay duplicated shared success audit", count, err)
	}
}

func TestScheduleHTTPSchemaPregatePrecedesIdentityProofAndWrites(t *testing.T) {
	f := newScheduleFixture(t)
	c := f.client(f.admin)
	beforeEpoch, beforeStep := f.version(f.admin), f.proofStep(f.admin)
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
	if err = lockIdentityTenant(f.ctx, tx, "one"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{schedules.SchemaVersion - 1, schedules.SchemaVersion + 1, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			if version == 0 {
				f.exec("DELETE FROM adtr.schema_version")
			} else {
				f.exec("UPDATE adtr.schema_version SET version=$1", version)
			}
			start := time.Now()
			for _, client := range []*resourceTestClient{c, nil} {
				f.callSchedule(client, "", nil, 503)
				f.callSchedule(client, "/detail?scheduleUUID=missing", nil, 503)
			}
			f.callSchedule(c, "/create", f.proof(f.createBody("schema")), 503)
			f.callSchedule(c, "/enable", f.proof(scheduleControlBody("missing", 1, "schema-enable")), 503)
			f.callSchedule(c, "/pause", f.proof(scheduleControlBody("missing", 1, "schema-pause")), 503)
			if time.Since(start) > 2*time.Second {
				t.Fatal("schema pregate waited for identity lock")
			}
			var count int
			if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.task_schedules)+(SELECT count(*) FROM adtr.task_schedule_events)+(SELECT count(*) FROM adtr.task_schedule_operations)+(SELECT count(*) FROM adtr.auth_attempts)+(SELECT count(*) FROM adtr.auth_audit)+(SELECT count(*) FROM adtr.tasks)").Scan(&count); err != nil || count != 0 {
				t.Fatal("incompatible schema wrote durable data", count, err)
			}
			if f.proofStep(f.admin) != beforeStep || f.version(f.admin) != beforeEpoch {
				t.Fatal("incompatible schema changed proof or actor epoch")
			}
		})
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec("INSERT INTO adtr.schema_version(singleton,version) VALUES(true,$1)", schedules.SchemaVersion)
	f.callSchedule(c, "/create", f.proof(f.createBody("schema-restored")), 200)
}

func TestScheduleAuthorizationDatabaseErrorIsNotPermissionDenial(t *testing.T) {
	f := newScheduleFixture(t)
	tx, err := f.conn.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, _ = tx.Exec(f.ctx, "SELECT 1/0")
	_, err = NewScheduleAuthorizer()(f.ctx, tx, tasks.Principal{TenantID: "one", ActorID: f.operator}, tasks.Scope{TaskName: "infrastructure.health", DomainID: "platform", Platform: true}, tasks.Execute)
	if err == nil || errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("database failure became permission denial", err)
	}
}

func TestScheduleWorkerRealIdentityEpochRevocationAndHealthExecution(t *testing.T) {
	f := newScheduleFixture(t)
	queue, err := tasks.New(f.s.database, tasks.ProductionRegistry(), NewTaskAuthorizer(), schedules.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := schedules.New(f.s.database, queue, NewScheduleAuthorizer())
	if err != nil {
		t.Fatal(err)
	}
	seedDue := func(id string, epoch int64) {
		t.Helper()
		// Synthetic database setup bypasses only the public future-start rule.
		// The worker uses the real persisted identity, grants, epoch and UTC grid.
		f.exec(`WITH due AS (SELECT date_trunc('second',clock_timestamp())-interval '120 seconds' AS anchor)
 INSERT INTO adtr.task_schedules(schedule_id,tenant_id,actor_id,authorization_version,label,kind,domain_id,payload_version,payload,start_at,interval_seconds,definition_hash,creation_key,state,next_at)
 SELECT $1,'one',$2,$3,'Synthetic due health','infrastructure.health','platform',1,'{}',anchor,3600,$1,$1,'enabled',anchor FROM due`, id, f.operator, fmt.Sprint(epoch))
	}
	oldEpoch := f.version(f.operator)
	seedDue("synthetic-revoked-schedule", oldEpoch)
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='schedules'", narrowResourceRole)
	f.exec("UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id='one' AND role_id=$1 AND mark='schedules'", narrowResourceRole)
	currentEpoch := f.version(f.operator)
	if currentEpoch <= oldEpoch {
		t.Fatal("restored schedules grant reused the old authorization epoch")
	}
	if admitted, err := scheduler.Tick(f.ctx); err != nil || admitted != 0 {
		t.Fatal("old schedule admitted after revoke and restore", admitted, err)
	}
	var state, code, pinned string
	var controlVersion int64
	if err := f.conn.QueryRow(f.ctx, "SELECT state,error_code,authorization_version,control_version FROM adtr.task_schedules WHERE schedule_id='synthetic-revoked-schedule'").Scan(&state, &code, &pinned, &controlVersion); err != nil || state != "authorization_blocked" || code != "authorization_revoked" || pinned != fmt.Sprint(oldEpoch) || controlVersion != 2 {
		t.Fatal("revocation was not durably fenced", state, code, pinned, controlVersion, err)
	}
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM adtr.tasks)+(SELECT count(*) FROM adtr.task_schedule_occurrences)").Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked schedule created task or occurrence", count, err)
	}
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.task_schedule_events WHERE schedule_id='synthetic-revoked-schedule' AND action='authorization_blocked'").Scan(&count); err != nil || count != 1 {
		t.Fatal("revocation event missing", count, err)
	}
	seedDue("synthetic-current-schedule", currentEpoch)
	if admitted, err := scheduler.Tick(f.ctx); err != nil || admitted != 1 {
		t.Fatal("new current-epoch schedule failed admission", admitted, err)
	}
	var taskID string
	var attempt int
	if err := f.conn.QueryRow(f.ctx, `SELECT t.task_id,t.state,t.authorization_version,t.attempt FROM adtr.task_schedule_occurrences o JOIN adtr.tasks t ON t.task_id=o.task_id AND t.tenant_id=o.tenant_id WHERE o.tenant_id='one' AND o.schedule_id='synthetic-current-schedule'`).Scan(&taskID, &state, &pinned, &attempt); err != nil || state != "queued" || pinned != fmt.Sprint(currentEpoch) || attempt != 0 {
		t.Fatal("admitted task lost the current identity epoch", taskID, state, pinned, attempt, err)
	}
	worker, err := tasks.NewWorker(queue, tasks.WorkerConfig{Owner: "synthetic-schedule-health", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case err := <-done:
				if err != nil {
					t.Error("health worker shutdown", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("health worker did not join after cancellation")
			}
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var result []byte
	for {
		if err := f.conn.QueryRow(ctx, "SELECT state,error_code,attempt,result FROM adtr.tasks WHERE task_id=$1", taskID).Scan(&state, &code, &attempt, &result); err != nil {
			t.Fatal("read health task completion", err)
		}
		if state == "succeeded" {
			break
		}
		if tasks.State(state).Terminal() {
			t.Fatal("scheduled health execution failed", state, code)
		}
		select {
		case err := <-done:
			joined = true
			t.Fatal("health worker stopped before durable success", state, err)
		case <-ctx.Done():
			t.Fatal("health worker did not complete within the test budget", state, ctx.Err())
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal("health worker shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("health worker did not join after successful execution")
	}
	var health map[string]string
	if err := json.Unmarshal(result, &health); err != nil || health["database"] != "ready" || health["queue"] != "ready" || code != "" || attempt != 1 {
		t.Fatal("scheduled health executor did not produce actual queue/database evidence", string(result), code, attempt, err)
	}
	if f.version(f.operator) != currentEpoch {
		t.Fatal("worker execution changed the actor authorization epoch")
	}
}

var _ tasks.Authorizer = NewScheduleAuthorizer()
