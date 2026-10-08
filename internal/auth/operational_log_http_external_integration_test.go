//go:build integration

package auth_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const (
	operationalHTTPPassword    = "Synthetic Operational HTTP Password 123"
	operationalHTTPSecret      = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	operationalHTTPOrigin      = "http://localhost:8080"
	operationalHTTPMissingTask = "00000000-0000-4000-8000-000000000001"
)

type operationalHTTPActor struct {
	id     int64
	name   string
	cookie *http.Cookie
	csrf   string
}

type operationalHTTPTrace struct {
	mu      sync.Mutex
	queries []string
}

func (t *operationalHTTPTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queries = append(t.queries, data.SQL) // Arguments may contain proof: never capture them.
	return ctx
}
func (*operationalHTTPTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (t *operationalHTTPTrace) take() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.queries...)
	t.queries = nil
	return out
}

type operationalHTTPFixture struct {
	t       *testing.T
	ctx     context.Context
	cfg     *pgx.ConnConfig
	conn    *pgx.Conn
	handler http.Handler
	engine  *tasks.Engine
	trace   *operationalHTTPTrace
	key     []byte
}

// Every normal fixture runs the real migrator. The sole migrate=false caller
// tests a genuinely uninitialized database, never a stamped schema version.
// Synthetic sessions are only an authentication transport fixture; /me, CSRF,
// password hashing, encrypted MFA, proof consumption and all SQL guards are real.
func newOperationalHTTPFixture(t *testing.T, migrate bool) *operationalHTTPFixture {
	t.Helper()
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("ADTR_TEST_DATABASE_URL required: actual PostgreSQL operational-log HTTP integration is blocked")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("isolated PostgreSQL unavailable", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	name := fmt.Sprintf("adtr_operational_http_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error("remove isolated operational HTTP database", err)
		}
	})
	cfg = cfg.Copy()
	cfg.Database = name
	if migrate {
		if err = store.Migrate(ctx, cfg); err != nil {
			t.Fatal("actual migration", err)
		}
		if err = store.Ready(ctx, cfg); err != nil {
			t.Fatal("actual schema readiness", err)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	f := &operationalHTTPFixture{t: t, ctx: ctx, cfg: cfg, conn: conn, key: bytes.Repeat([]byte{'o'}, 32), trace: &operationalHTTPTrace{}}
	f.installHandlers(cfg)
	if migrate {
		s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Bootstrap(ctx, "operational-bootstrap", operationalHTTPPassword); err != nil {
			t.Fatal("real bootstrap password hash", err)
		}
		if got := f.count(`SELECT version FROM adtr.schema_version WHERE singleton`); got != store.SchemaVersion {
			t.Fatalf("expected actual current migration, got %d", got)
		}
		if got := f.count(`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('operational_log_grant_epoch','operational_log_events_immutable','operational_log_audit_immutable','credential_use_user_guard')`); got != 4 {
			t.Fatalf("real security guards missing: %d", got)
		}
	}
	return f
}
func (f *operationalHTTPFixture) installHandlers(config *pgx.ConnConfig) {
	f.t.Helper()
	cfg := config.Copy()
	cfg.Tracer = f.trace
	s, err := auth.New(cfg, f.key, operationalHTTPOrigin, true)
	if err != nil {
		f.t.Fatal(err)
	}
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(operationallogs.Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", s)
	mux.Handle("/api/system/logs", s.OperationalLogsHandler(engine))
	mux.Handle("/api/system/logs/", s.OperationalLogsHandler(engine))
	mux.Handle("/api/tasks", s.TasksHandler(engine))
	mux.Handle("/api/tasks/", s.TasksHandler(engine))
	f.handler, f.engine = mux, engine
}
func (f *operationalHTTPFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *operationalHTTPFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.conn.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *operationalHTTPFixture) role(letter string, grants map[string]auth.AccessAuth) string {
	f.t.Helper()
	id := strings.Repeat(letter, 24)
	f.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default',$1,$2)`, id, "Synthetic operational "+letter)
	for mark, grant := range grants {
		f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default',$1,$2,$3,$4)`, id, mark, grant.Readable, grant.Writeable)
	}
	return id
}
func (f *operationalHTTPFixture) actor(name, tenant, custom string, mfa bool) *operationalHTTPActor {
	f.t.Helper()
	a := &operationalHTTPActor{name: name}
	role := "platform_admin"
	if custom != "" {
		role = "viewer"
	}
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at)
 SELECT $1,$2,password_hash,$3,$4,false,clock_timestamp()-interval '1 minute' FROM adtr.users WHERE username='operational-bootstrap' RETURNING id`, tenant, name, role, custom).Scan(&a.id); err != nil {
		f.t.Fatal(err)
	}
	if mfa {
		block, err := aes.NewCipher(f.key)
		if err != nil {
			f.t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			f.t.Fatal(err)
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			f.t.Fatal(err)
		}
		sealed := base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(operationalHTTPSecret), []byte(strconv.FormatInt(a.id, 10))))
		f.exec(`UPDATE adtr.users SET mfa_secret=$1 WHERE id=$2`, sealed, a.id)
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		f.t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(entropy[:])
	hash := sha256.Sum256([]byte(token))
	f.exec(`INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hex.EncodeToString(hash[:]), a.id)
	a.cookie = &http.Cookie{Name: "adtr_session", Value: token}
	out := operationalDecode[map[string]any](f.t, f.call(a, "/api/auth/me", nil, 200).Body.Bytes())
	a.csrf, _ = out["csrfToken"].(string)
	if a.csrf == "" {
		f.t.Fatal("real /me did not provide CSRF")
	}
	return a
}
func (f *operationalHTTPFixture) raw(a *operationalHTTPActor, method, path string, raw []byte, want int, edit func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.RemoteAddr = "127.0.0.1:8746"
	r.Header.Set("Origin", operationalHTTPOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ADTR-User-ID", "999999999")
	r.Header.Set("X-Request-ID", "untrusted-operational-request")
	r.Header.Set("X-Forwarded-For", "198.51.100.47")
	if a != nil {
		r.AddCookie(a.cookie)
		r.Header.Set("X-CSRF-Token", a.csrf)
	}
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("%s: got HTTP %d, want %d: %s", path, w.Code, want, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("sensitive response is cacheable")
	}
	if strings.HasPrefix(path, "/api/system/logs") {
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			f.t.Fatal("operational response lost nosniff")
		}
		if want == 200 && (a == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(a.id, 10)) {
			f.t.Fatal("operational response lost authenticated actor binding")
		}
		if want >= 400 && w.Header().Get("X-ADTR-User-ID") != "" {
			f.t.Fatal("failed response disclosed or trusted an actor header")
		}
	}
	if want >= 400 {
		out := operationalDecode[map[string]any](f.t, w.Body.Bytes())
		if len(out) != 1 || out["error"] == nil {
			f.t.Fatal("error exposed private metadata or artifact bytes")
		}
	}
	return w
}
func (f *operationalHTTPFixture) call(a *operationalHTTPActor, path string, body map[string]any, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	method := http.MethodGet
	var raw []byte
	if body != nil {
		method = http.MethodPost
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	return f.raw(a, method, path, raw, want, nil)
}
func operationalDecode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		t.Fatal("invalid or overbroad response DTO", err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		t.Fatal("trailing response data")
	}
	return out
}
func operationalCode(step int64) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(operationalHTTPSecret)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, key)
	_, _ = h.Write(counter[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
}

// Consume current or next still-valid steps without resetting replay state or
// changing the production clock. A third success can wait at most one real
// 30-second boundary; using the previous step would race its immediate expiry.
func (f *operationalHTTPFixture) proof(a *operationalHTTPActor, body map[string]any) map[string]any {
	f.t.Helper()
	out := make(map[string]any, len(body)+2)
	for k, v := range body {
		out[k] = v
	}
	last := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, a.id)
	for {
		now := time.Now().Unix() / 30
		candidate := max(now, last+1)
		if candidate > now+1 {
			until := time.Unix((candidate-1)*30, 0).Add(25 * time.Millisecond)
			timer := time.NewTimer(time.Until(until))
			select {
			case <-f.ctx.Done():
				timer.Stop()
				f.t.Fatal("timed out awaiting an actually fresh TOTP step")
			case <-timer.C:
			}
			continue
		}
		out["actorPassword"], out["totpCode"] = operationalHTTPPassword, operationalCode(candidate)
		return out
	}
}
func (f *operationalHTTPFixture) selection() operationallogs.Selection {
	f.t.Helper()
	var now time.Time
	if err := f.conn.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		f.t.Fatal(err)
	}
	start := now.UTC().Truncate(time.Microsecond).Add(-time.Hour)
	return operationallogs.Selection{StartTm: start.Format(time.RFC3339Nano), EndTm: start.Add(2 * time.Hour).Format(time.RFC3339Nano), SystemType: []string{"api", "worker"}}
}
func operationalBody(s operationallogs.Selection, key string) map[string]any {
	return map[string]any{"startTm": s.StartTm, "endTm": s.EndTm, "systemType": s.SystemType, "idempotencyKey": key}
}
func operationalQuery(s operationallogs.Selection) string {
	q := url.Values{"startTm": {s.StartTm}, "endTm": {s.EndTm}, "systemType": s.SystemType}
	return "?" + q.Encode()
}
func (f *operationalHTTPFixture) error(w *httptest.ResponseRecorder, code string) {
	f.t.Helper()
	if got := operationalDecode[map[string]any](f.t, w.Body.Bytes())["error"]; got != code {
		f.t.Fatalf("got error %v, want %s", got, code)
	}
}
func (f *operationalHTTPFixture) record() {
	f.t.Helper()
	s, err := operationallogs.NewStore(f.cfg, store.SchemaVersion)
	if err != nil {
		f.t.Fatal(err)
	}
	// Typed production Recorder -> production PostgreSQL Store. Runtime cmd
	// producer wiring is a separate browser/process acceptance test, not claimed
	// by these in-process HTTP/engine tests.
	for _, module := range []operationallogs.Module{operationallogs.API, operationallogs.Worker} {
		r, err := operationallogs.NewRecorder(s, module)
		if err != nil {
			f.t.Fatal(err)
		}
		if !r.Emit(operationallogs.ServiceStartRequested, operationallogs.Attempted, operationallogs.NoReason) || !r.Emit(operationallogs.ServiceStopped, operationallogs.Completed, operationallogs.NoReason) {
			f.t.Fatal("typed producer rejected event")
		}
		ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		err = r.Close(ctx)
		cancel()
		if err != nil {
			f.t.Fatal("drain actual recorder", err)
		}
		if stats := r.Stats(); stats.Acknowledged != 2 || stats.Unacknowledged != 0 || stats.Abandoned != 0 {
			f.t.Fatalf("recorder not durably acknowledged: %+v", stats)
		}
	}
}

type operationalHTTPSubmission struct {
	TaskUUID string     `json:"taskUUID"`
	Task     tasks.Task `json:"task"`
	Replayed bool       `json:"replayed"`
}

func (f *operationalHTTPFixture) submit(a *operationalHTTPActor, s operationallogs.Selection, key string) operationalHTTPSubmission {
	f.t.Helper()
	out := operationalDecode[operationalHTTPSubmission](f.t, f.call(a, "/api/system/logs/bundles", f.proof(a, operationalBody(s, key)), 200).Body.Bytes())
	if out.TaskUUID == "" || out.Task.ID != out.TaskUUID || out.Task.State != tasks.Queued || out.Replayed {
		f.t.Fatal("fresh HTTP submit did not create a queued bundle")
	}
	operationalPublicTask(f.t, out.Task)
	return out
}
func operationalPublicTask(t *testing.T, task tasks.Task) {
	t.Helper()
	if task.Kind != operationallogs.BundleKindName || task.DomainID != "platform" || string(task.Result) != "{}" || string(task.Cursor) != "{}" || task.MaxAttempts != 1 {
		t.Fatal("bundle exposed private result/cursor or wrong policy")
	}
}
func operationalFullGrants() map[string]auth.AccessAuth {
	return map[string]auth.AccessAuth{"system": {Readable: true}, "system_logs": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}}
}

func TestOperationalLogsMigratedHTTPOwnerAndReadWriteBoundaries(t *testing.T) {
	f := newOperationalHTTPFixture(t, true)
	owner := f.actor("operational-owner", "default", "", true)
	foreign := f.actor("operational-foreign", "other-installation", "", true)
	deniedRole := f.role("d", map[string]auth.AccessAuth{"system": {Readable: true, Writeable: true}, "tasks": {Readable: true, Writeable: true}})
	denied := f.actor("operational-denied", "default", deniedRole, true)
	readRole := f.role("r", map[string]auth.AccessAuth{"system": {Readable: true}, "system_logs": {Readable: true}})
	reader := f.actor("operational-reader", "default", readRole, true)
	f.record()
	for _, path := range []string{"/api/system/logs", "/api/system/logs/sources", "/api/system/logs/bundles/history"} {
		f.call(owner, path, nil, 200)
		f.call(reader, path, nil, 200)
		f.error(f.call(foreign, path, nil, 403), "forbidden")
		f.error(f.call(denied, path, nil, 403), "forbidden")
		f.error(f.call(nil, path, nil, 401), "unauthenticated")
	}
	// Dedicated package reads reach the existence check without tasks.readable.
	for _, path := range []string{"/api/system/logs/bundles/detail", "/api/system/logs/bundles/download"} {
		f.error(f.call(reader, path+"?taskUUID="+operationalHTTPMissingTask, nil, 404), "not_found")
		f.error(f.call(foreign, path+"?taskUUID="+operationalHTTPMissingTask, nil, 403), "forbidden")
	}
	f.error(f.call(reader, "/api/tasks", nil, 403), "forbidden")
	selection := f.selection()
	for _, a := range []*operationalHTTPActor{foreign, denied, reader} {
		f.error(f.call(a, "/api/system/logs/bundles", f.proof(a, operationalBody(selection, "denied-"+a.name)), 403), "forbidden")
		f.error(f.call(a, "/api/system/logs/bundles/cancel", f.proof(a, map[string]any{"taskUUID": operationalHTTPMissingTask}), 403), "forbidden")
		if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, a.id) != -1 {
			t.Fatal("permission denial consumed proof")
		}
	}
	for _, tc := range []struct {
		letter, mark string
		grant        auth.AccessAuth
	}{{"x", "tasks", auth.AccessAuth{}}, {"y", "tasks", auth.AccessAuth{Readable: true}}, {"z", "system_logs", auth.AccessAuth{Readable: true}}} {
		grants := operationalFullGrants()
		grants[tc.mark] = tc.grant
		a := f.actor("operational-split-"+tc.letter, "default", f.role(tc.letter, grants), true)
		f.error(f.call(a, "/api/system/logs/bundles", f.proof(a, operationalBody(selection, "split-"+tc.letter)), 403), "forbidden")
		f.error(f.call(a, "/api/system/logs/bundles/cancel", f.proof(a, map[string]any{"taskUUID": operationalHTTPMissingTask}), 403), "forbidden")
		if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, a.id) != -1 {
			t.Fatal("write split denial consumed proof")
		}
	}
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 0 || f.count(`SELECT count(*) FROM adtr.operational_log_audit`) != 0 {
		t.Fatal("denied requests mutated task/control state")
	}
}

func TestOperationalLogsMigratedHTTPRecorderWorkerAndZIPBytes(t *testing.T) {
	f := newOperationalHTTPFixture(t, true)
	owner := f.actor("operational-bundle-owner", "default", "", true)
	other := f.actor("operational-bundle-other", "default", "", true)
	f.record()
	selection := f.selection()
	journal := operationalDecode[operationallogs.List](t, f.call(owner, "/api/system/logs"+operationalQuery(selection), nil, 200).Body.Bytes())
	if journal.Page.Total != 4 || len(journal.List) != 4 || journal.Coverage.Mode != "best_effort" || !journal.Coverage.GapsPossible || journal.Coverage.FirstRecordedAt == nil {
		t.Fatal("actual journal rows or best-effort provenance missing")
	}
	for _, row := range journal.List {
		decoded := operationalDecode[operationallogs.Event](t, []byte(row.Log))
		if !reflect.DeepEqual(decoded, row.Event) {
			t.Fatal("event and display JSON diverged")
		}
	}
	sources := operationalDecode[operationallogs.Sources](t, f.call(owner, "/api/system/logs/sources", nil, 200).Body.Bytes())
	if len(sources.Modules) != 2 || len(sources.Reports) != 2 || !sources.Coverage.GapsPossible {
		t.Fatal("actual Recorder sources/observations missing")
	}
	for _, source := range sources.Modules {
		if !source.Registered || source.RecordedCount != 2 || source.FirstRecordedAt == nil || source.LastRecordedAt == nil {
			t.Fatal("source catalogue invented or lost journal records")
		}
	}
	sub := f.submit(owner, selection, "actual-recorder-worker-zip")
	if worked, err := f.engine.RunOne(f.ctx, "synthetic-http-bundle-worker"); err != nil || !worked {
		t.Fatal("actual bundle worker did not execute", err)
	}
	detail := operationalDecode[operationallogs.BundleDetail](t, f.call(owner, "/api/system/logs/bundles/detail?taskUUID="+sub.TaskUUID, nil, 200).Body.Bytes())
	operationalPublicTask(t, detail.Task)
	if detail.Task.State != tasks.Succeeded || !detail.DownloadReady || detail.ArtifactStatus != "eligible" || detail.RowCount == nil || *detail.RowCount != 4 || detail.SnapshotAt == nil || detail.Coverage == nil || !detail.Coverage.GapsPossible {
		t.Fatal("completed worker artifact not exposed by dedicated HTTP detail")
	}
	if detail.DownloadPath != "/api/system/logs/bundles/download?taskUUID="+sub.TaskUUID {
		t.Fatal("unsafe or unbound download path")
	}
	// The completed task really stores private artifact metadata. Dedicated
	// bundle projection alone cannot guard the generic task routes' redaction.
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND result ? 'artifactToken'`, sub.TaskUUID) != 1 {
		t.Fatal("worker did not persist the private result being tested for redaction")
	}
	genericDetail := operationalDecode[tasks.Detail](t, f.call(owner, "/api/tasks/detail?taskUUID="+sub.TaskUUID, nil, 200).Body.Bytes())
	operationalPublicTask(t, genericDetail.Task)
	if genericDetail.Task.ID != sub.TaskUUID || genericDetail.Task.State != tasks.Succeeded || len(genericDetail.Events) == 0 {
		t.Fatal("generic task detail lost the actual completed task or its events")
	}
	genericList := operationalDecode[tasks.List](t, f.call(owner, "/api/tasks?taskName=system.logs_bundle", nil, 200).Body.Bytes())
	if genericList.Page.Total != 1 || len(genericList.Tasks) != 1 || genericList.Tasks[0].ID != sub.TaskUUID {
		t.Fatal("generic task list lost the current owner's completed bundle")
	}
	operationalPublicTask(t, genericList.Tasks[0])
	f.error(f.call(other, "/api/tasks/detail?taskUUID="+sub.TaskUUID, nil, 404), "not_found")
	otherGenericList := operationalDecode[tasks.List](t, f.call(other, "/api/tasks?taskName=system.logs_bundle", nil, 200).Body.Bytes())
	if otherGenericList.Page.Total != 0 || len(otherGenericList.Tasks) != 0 {
		t.Fatal("generic task list/count leaked another actor's completed bundle")
	}
	history := operationalDecode[operationallogs.BundleHistory](t, f.call(owner, "/api/system/logs/bundles/history?status=succeeded&pageSize=-1", nil, 200).Body.Bytes())
	if history.Page.Total != 1 || len(history.List) != 1 || history.List[0].Task.ID != sub.TaskUUID || !history.Exhausted {
		t.Fatal("current-owner history lost completed artifact")
	}
	for _, path := range []string{"/api/system/logs/bundles/detail?taskUUID=", "/api/system/logs/bundles/download?taskUUID="} {
		f.error(f.call(other, path+sub.TaskUUID, nil, 404), "not_found")
	}
	otherHistory := operationalDecode[operationallogs.BundleHistory](t, f.call(other, "/api/system/logs/bundles/history", nil, 200).Body.Bytes())
	if otherHistory.Page.Total != 0 || len(otherHistory.List) != 0 {
		t.Fatal("owner-scoped count leaked another actor's tasks")
	}
	w := f.call(owner, detail.DownloadPath, nil, 200)
	if w.Header().Get("Content-Type") != "application/zip" || w.Header().Get("Content-Disposition") != `attachment; filename="`+operationallogs.ArtifactFilename(sub.TaskUUID)+`"` {
		t.Fatal("unsafe ZIP HTTP delivery metadata")
	}
	operationalCheckZIP(t, w.Body.Bytes(), sub.TaskUUID, journal)
	// A genuinely empty window still produces a real archive, never a claim of
	// complete historical coverage or predeployment filesystem parity.
	emptySelection := selection
	start, _ := time.Parse(time.RFC3339Nano, selection.StartTm)
	emptySelection.StartTm = start.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	emptySelection.EndTm = start.Add(-time.Hour).Format(time.RFC3339Nano)
	empty := f.submit(owner, emptySelection, "actual-empty-window")
	if worked, err := f.engine.RunOne(f.ctx, "synthetic-http-empty-worker"); err != nil || !worked {
		t.Fatal("empty bundle worker", err)
	}
	emptyJournal := operationalDecode[operationallogs.List](t, f.call(owner, "/api/system/logs"+operationalQuery(emptySelection), nil, 200).Body.Bytes())
	if emptyJournal.Page.Total != 0 || !emptyJournal.Coverage.GapsPossible {
		t.Fatal("empty window made a completeness claim")
	}
	operationalCheckZIP(t, f.call(owner, "/api/system/logs/bundles/download?taskUUID="+empty.TaskUUID, nil, 200).Body.Bytes(), empty.TaskUUID, emptyJournal)
}

func operationalCheckZIP(t *testing.T, data []byte, id string, journal operationallogs.List) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 2 {
		t.Fatal("HTTP did not return a real two-entry ZIP", err)
	}
	parts := make([][]byte, 2)
	for i, name := range []string{"manifest.json", "events.jsonl"} {
		if z.File[i].Name != name || z.File[i].Method != zip.Store || z.File[i].FileInfo().IsDir() {
			t.Fatal("ZIP contains unsafe, unexpected, or compressed entries")
		}
		r, err := z.File[i].Open()
		if err != nil {
			t.Fatal(err)
		}
		parts[i], err = io.ReadAll(io.LimitReader(r, operationallogs.MaxBundleBytes+1))
		closeErr := r.Close()
		if err != nil || closeErr != nil || len(parts[i]) > operationallogs.MaxBundleBytes {
			t.Fatal("read bounded ZIP entry", err, closeErr)
		}
	}
	var manifest struct {
		FormatVersion int                       `json:"formatVersion"`
		TaskUUID      string                    `json:"taskUUID"`
		SnapshotAt    time.Time                 `json:"snapshotAt"`
		RowCount      int                       `json:"rowCount"`
		Selection     operationallogs.Selection `json:"selection"`
		Coverage      operationallogs.Coverage  `json:"coverage"`
		JSONLSHA256   string                    `json:"jsonlSHA256"`
	}
	manifest = operationalDecode[struct {
		FormatVersion int                       `json:"formatVersion"`
		TaskUUID      string                    `json:"taskUUID"`
		SnapshotAt    time.Time                 `json:"snapshotAt"`
		RowCount      int                       `json:"rowCount"`
		Selection     operationallogs.Selection `json:"selection"`
		Coverage      operationallogs.Coverage  `json:"coverage"`
		JSONLSHA256   string                    `json:"jsonlSHA256"`
	}](t, parts[0])
	digest := sha256.Sum256(parts[1])
	if manifest.FormatVersion != 1 || manifest.TaskUUID != id || manifest.SnapshotAt.IsZero() || manifest.RowCount != len(journal.List) || manifest.JSONLSHA256 != hex.EncodeToString(digest[:]) || !reflect.DeepEqual(manifest.Selection, journal.Selection) || manifest.Coverage.Mode != "best_effort" || !manifest.Coverage.GapsPossible {
		t.Fatal("ZIP manifest/digest/selection does not describe actual HTTP journal")
	}
	if len(journal.List) == 0 {
		if len(parts[1]) != 0 {
			t.Fatal("empty window has invented event bytes")
		}
		return
	}
	if !bytes.HasSuffix(parts[1], []byte{'\n'}) {
		t.Fatal("JSONL missing terminal newline")
	}
	lines := bytes.Split(bytes.TrimSuffix(parts[1], []byte{'\n'}), []byte{'\n'})
	if len(lines) != len(journal.List) {
		t.Fatal("ZIP truncated the selected actual events")
	}
	for i, line := range lines {
		event := operationalDecode[operationallogs.Event](t, line)
		if string(line) != journal.List[i].Log || !reflect.DeepEqual(event, journal.List[i].Event) {
			t.Fatalf("ZIP event %d changed journal identity/content/order, including first and last", i)
		}
	}
}

func TestOperationalLogsMigratedHTTPFreshReplayAndImmutableControlAudit(t *testing.T) {
	f := newOperationalHTTPFixture(t, true)
	owner := f.actor("operational-replay-owner", "default", "", true)
	selection := f.selection()
	body := f.proof(owner, operationalBody(selection, "stable-operational-intent"))
	first := operationalDecode[operationalHTTPSubmission](t, f.call(owner, "/api/system/logs/bundles", body, 200).Body.Bytes())
	if first.Replayed || first.TaskUUID == "" {
		t.Fatal("fresh submit did not persist the intent")
	}
	step := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id)
	if step < 0 {
		t.Fatal("real submit did not consume proof")
	}
	// A committed response lost by the client is recoverable by GET; repeating
	// its already-used proof is still rejected even with the same idempotency key.
	f.error(f.call(owner, "/api/system/logs/bundles", body, 401), "invalid_credentials")
	f.call(owner, "/api/system/logs/bundles/detail?taskUUID="+first.TaskUUID, nil, 200)
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) != step {
		t.Fatal("read recovery or spent proof changed replay state")
	}
	fresh := f.proof(owner, operationalBody(selection, "stable-operational-intent"))
	conflicting := make(map[string]any, len(fresh))
	for k, v := range fresh {
		conflicting[k] = v
	}
	conflicting["systemType"] = []string{"worker"}
	f.error(f.call(owner, "/api/system/logs/bundles", conflicting, 409), "idempotency_conflict")
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) != step {
		t.Fatal("idempotency conflict consumed a rolled-back proof")
	}
	// Module order and explicit-offset timestamps denote the same intent.
	fresh["systemType"] = []string{"worker", "api"}
	start, _ := time.Parse(time.RFC3339Nano, selection.StartTm)
	end, _ := time.Parse(time.RFC3339Nano, selection.EndTm)
	zone := time.FixedZone("synthetic", 2*60*60)
	fresh["startTm"], fresh["endTm"] = start.In(zone).Format(time.RFC3339Nano), end.In(zone).Format(time.RFC3339Nano)
	replay := operationalDecode[operationalHTTPSubmission](t, f.call(owner, "/api/system/logs/bundles", fresh, 200).Body.Bytes())
	if !replay.Replayed || replay.TaskUUID != first.TaskUUID || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) <= step {
		t.Fatal("fresh canonical replay did not recover exactly one existing task")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks`) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='submitted'`, first.TaskUUID) != 1 || f.count(`SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1 AND action='bundle_submit'`, first.TaskUUID) != 1 {
		t.Fatal("replay duplicated task or mutation audit")
	}
	var actor int64
	var tenant, username, ip, path, requestID string
	if err := f.conn.QueryRow(f.ctx, `SELECT actor_id,tenant_id,audit_username,audit_ip,audit_path,audit_request_id FROM adtr.operational_log_audit WHERE task_id=$1 AND action='bundle_submit'`, first.TaskUUID).Scan(&actor, &tenant, &username, &ip, &path, &requestID); err != nil {
		t.Fatal(err)
	}
	if actor != owner.id || tenant != "default" || username != owner.name || ip != "127.0.0.1" || path != "/api/system/logs/bundles" || requestID == "" || requestID == "untrusted-operational-request" {
		t.Fatal("control audit trusted client actor/IP/request ID or lost server metadata")
	}
	var args []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT event_args FROM adtr.audit_source WHERE source='operational_log' AND event='system_logs_bundle_submit' AND event_args->>'taskUUID'=$1`, first.TaskUUID).Scan(&args); err != nil {
		t.Fatal("protected operational audit projection", err)
	}
	projected := operationalDecode[struct {
		TaskUUID  string `json:"taskUUID"`
		Path      string `json:"path"`
		RequestID string `json:"requestId"`
	}](t, args)
	if projected.TaskUUID != first.TaskUUID || projected.Path != path || projected.RequestID != requestID {
		t.Fatal("operational audit projection includes private payload or lost control identity")
	}
	for _, query := range []string{`UPDATE adtr.operational_log_audit SET action=action WHERE task_id=$1`, `DELETE FROM adtr.operational_log_audit WHERE task_id=$1`} {
		if _, err := f.conn.Exec(f.ctx, query, first.TaskUUID); err == nil {
			t.Fatal("control audit is mutable")
		}
	}
	if _, err := f.conn.Exec(f.ctx, `TRUNCATE adtr.operational_log_audit`); err == nil {
		t.Fatal("control audit permits truncate")
	}
	if f.count(`SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1`, first.TaskUUID) != 1 {
		t.Fatal("rejected audit mutation removed evidence")
	}
}

func TestOperationalLogsMigratedHTTPGenericCancelCannotBypassControlAudit(t *testing.T) {
	f := newOperationalHTTPFixture(t, true)
	owner := f.actor("operational-cancel-owner", "default", "", true)
	other := f.actor("operational-cancel-other", "default", "", true)
	sub := f.submit(owner, f.selection(), "dedicated-cancel-intent")
	beforeStep := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id)
	beforeEvents := f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1`, sub.TaskUUID)
	body := f.proof(owner, map[string]any{"taskUUID": sub.TaskUUID})
	// Regression: generic cancel previously mutated a logs bundle without the
	// dedicated immutable bundle_cancel control record. It must reject in the
	// same transaction and roll back the proof it checked before kind dispatch.
	f.error(f.call(owner, "/api/tasks/cancel", body, 400), "operational_log_route_required")
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state='queued' AND result_version=0`, sub.TaskUUID) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1`, sub.TaskUUID) != beforeEvents || f.count(`SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1`, sub.TaskUUID) != 1 || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) != beforeStep {
		t.Fatal("generic cancel changed task/control audit or consumed proof")
	}
	f.error(f.call(other, "/api/system/logs/bundles/cancel", f.proof(other, map[string]any{"taskUUID": sub.TaskUUID}), 404), "not_found")
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, other.id) != -1 {
		t.Fatal("foreign-owner cancellation consumed proof")
	}
	out := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.call(owner, "/api/system/logs/bundles/cancel", body, 200).Body.Bytes())
	operationalPublicTask(t, out.Task)
	if out.Task.State != tasks.Cancelled || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) <= beforeStep || f.count(`SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1 AND action='bundle_cancel'`, sub.TaskUUID) != 1 {
		t.Fatal("dedicated cancel did not atomically consume proof and audit actual state change")
	}
	beforeEvents = f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1`, sub.TaskUUID)
	duplicate := operationalDecode[struct {
		Task tasks.Task `json:"task"`
	}](t, f.call(owner, "/api/system/logs/bundles/cancel", f.proof(owner, map[string]any{"taskUUID": sub.TaskUUID}), 200).Body.Bytes())
	if duplicate.Task.State != tasks.Cancelled || duplicate.Task.ResultVersion != out.Task.ResultVersion || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1`, sub.TaskUUID) != beforeEvents || f.count(`SELECT count(*) FROM adtr.operational_log_audit WHERE task_id=$1 AND action='bundle_cancel'`, sub.TaskUUID) != 1 {
		t.Fatal("fresh no-op cancellation duplicated a mutation audit or task event")
	}
	f.error(f.call(owner, "/api/system/logs/bundles/download?taskUUID="+sub.TaskUUID, nil, 409), "bundle_not_ready")
}

func TestOperationalLogsMigratedHTTPOriginCSRFProofAndExactInputs(t *testing.T) {
	f := newOperationalHTTPFixture(t, true)
	owner := f.actor("operational-proof-owner", "default", "", true)
	noMFA := f.actor("operational-no-mfa", "default", "", false)
	body := f.proof(owner, operationalBody(f.selection(), "proof-input-intent"))
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.invalid") }, func(r *http.Request) { r.Header.Del("Origin") }, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong-token") }} {
		f.error(f.raw(owner, http.MethodPost, "/api/system/logs/bundles", raw, 403, edit), "forbidden")
	}
	f.error(f.call(noMFA, "/api/system/logs/bundles", body, 403), "mfa_required")
	wrongPassword := make(map[string]any, len(body))
	for k, v := range body {
		wrongPassword[k] = v
	}
	wrongPassword["actorPassword"] = "wrong-synthetic-password"
	f.error(f.call(owner, "/api/system/logs/bundles", wrongPassword, 401), "invalid_credentials")
	wrongTOTP := make(map[string]any, len(body))
	for k, v := range body {
		wrongTOTP[k] = v
	}
	// Use a well-formed code guaranteed not to match any accepted current step.
	for candidate := 0; candidate < 1000000; candidate++ {
		code := fmt.Sprintf("%06d", candidate)
		now := time.Now().Unix() / 30
		if code != operationalCode(now-1) && code != operationalCode(now) && code != operationalCode(now+1) && code != operationalCode(now+2) {
			wrongTOTP["totpCode"] = code
			break
		}
	}
	f.error(f.call(owner, "/api/system/logs/bundles", wrongTOTP, 401), "invalid_credentials")
	for _, tc := range []struct {
		field string
		value any
	}{{"tenant", "other-installation"}, {"userInfo", map[string]any{"id": owner.id}}, {"src", "/var/log/private"}, {"fileName", "../../private.zip"}, {"systemType", nil}, {"systemType", []string{}}, {"systemType", []string{"api", "legacy"}}, {"systemType", []string{"api", "api"}}, {"startTm", nil}, {"actorPassword", nil}, {"totpCode", nil}, {"idempotencyKey", nil}} {
		copyBody := make(map[string]any, len(body)+1)
		for k, v := range body {
			copyBody[k] = v
		}
		copyBody[tc.field] = tc.value
		f.error(f.call(owner, "/api/system/logs/bundles", copyBody, 400), "invalid_input")
	}
	duplicate := append([]byte(`{"actorPassword":"duplicate",`), raw[1:]...)
	f.error(f.raw(owner, http.MethodPost, "/api/system/logs/bundles", duplicate, 400, nil), "invalid_input")
	f.error(f.raw(owner, http.MethodPost, "/api/system/logs/bundles?tenant=default", raw, 400, nil), "invalid_input")
	f.error(f.raw(owner, http.MethodPost, "/api/system/logs/bundles", raw, 400, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }), "invalid_input")
	for _, path := range []string{
		"/api/system/logs?tenant=default", "/api/system/logs?userInfo=1", "/api/system/logs?systemType=legacy", "/api/system/logs?systemType=api&systemType=api",
		"/api/system/logs?pageIdx=2&pageSize=-1", "/api/system/logs?pageIdx=1&pageIdx=2", "/api/system/logs?pageSize=101",
		"/api/system/logs?startTm=2026-10-07T00:00:00Z", "/api/system/logs?startTm=2026-10-07T00:00:00.1234567Z&endTm=2026-10-08T00:00:00Z",
		"/api/system/logs/sources?fileName=private", "/api/system/logs/bundles/history?status=queued&status=queued", "/api/system/logs/bundles/history?status=made_up",
		"/api/system/logs/bundles/detail?taskUUID=" + operationalHTTPMissingTask + "&tenant=default", "/api/system/logs/bundles/download?taskUUID=" + operationalHTTPMissingTask + "&src=/tmp/private",
	} {
		f.error(f.call(owner, path, nil, 400), "invalid_input")
	}
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) != -1 || f.count(`SELECT count(*) FROM adtr.tasks`) != 0 || f.count(`SELECT count(*) FROM adtr.operational_log_audit`) != 0 {
		t.Fatal("rejected proof/input request changed protected state")
	}
	// Replay state stayed unused. Request a currently valid code so a slow
	// malformed-input pass cannot turn this assertion into an expiry race.
	f.call(owner, "/api/system/logs/bundles", f.proof(owner, body), 200)
}

func TestOperationalLogsMigratedHTTPCurrentGrantsAndEpochRevocation(t *testing.T) {
	for _, mark := range []string{"system", "system_logs"} {
		t.Run(mark, func(t *testing.T) {
			f := newOperationalHTTPFixture(t, true)
			role := f.role("w", operationalFullGrants())
			owner := f.actor("operational-epoch-owner", "default", role, true)
			f.record()
			sub := f.submit(owner, f.selection(), "before-"+mark+"-revocation")
			if worked, err := f.engine.RunOne(f.ctx, "synthetic-epoch-worker"); err != nil || !worked {
				t.Fatal("actual worker before revocation", err)
			}
			f.call(owner, "/api/system/logs/bundles/download?taskUUID="+sub.TaskUUID, nil, 200)
			epoch := f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, owner.id)
			f.exec(`UPDATE adtr.access_permissions SET readable=readable WHERE tenant_id='default' AND role_id=$1 AND mark=$2`, role, mark)
			if f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, owner.id) != epoch {
				t.Fatal("no-op grant update changed epoch")
			}
			f.exec(`UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='default' AND role_id=$1 AND mark=$2`, role, mark)
			revokedEpoch := f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, owner.id)
			if revokedEpoch <= epoch {
				t.Fatal("system/log grant revoke did not advance durable authority")
			}
			for _, path := range []string{"/api/system/logs", "/api/system/logs/sources", "/api/system/logs/bundles/history", "/api/system/logs/bundles/detail?taskUUID=" + sub.TaskUUID, "/api/system/logs/bundles/download?taskUUID=" + sub.TaskUUID} {
				f.error(f.call(owner, path, nil, 403), "forbidden")
			}
			beforeProof := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id)
			f.error(f.call(owner, "/api/system/logs/bundles/cancel", f.proof(owner, map[string]any{"taskUUID": sub.TaskUUID}), 403), "forbidden")
			if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, owner.id) != beforeProof {
				t.Fatal("current grant denial consumed proof")
			}
			f.exec(`UPDATE adtr.access_permissions SET readable=true,writeable=$3 WHERE tenant_id='default' AND role_id=$1 AND mark=$2`, role, mark, mark == "system_logs")
			if f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, owner.id) <= revokedEpoch {
				t.Fatal("regrant failed to advance epoch")
			}
			f.call(owner, "/api/system/logs", nil, 200)
			for _, path := range []string{"/api/system/logs/bundles/detail?taskUUID=", "/api/system/logs/bundles/download?taskUUID="} {
				f.error(f.call(owner, path+sub.TaskUUID, nil, 404), "not_found")
			}
			history := operationalDecode[operationallogs.BundleHistory](t, f.call(owner, "/api/system/logs/bundles/history", nil, 200).Body.Bytes())
			if history.Page.Total != 0 || len(history.List) != 0 {
				t.Fatal("regrant revived stale-epoch artifact/count")
			}
			if f.count(`SELECT count(*) FROM adtr.operational_log_bundle_manifests WHERE task_id=$1`, sub.TaskUUID) != 1 {
				t.Fatal("revocation deleted protected artifact history")
			}
			fresh := f.submit(owner, f.selection(), "after-"+mark+"-regrant")
			if fresh.TaskUUID == sub.TaskUUID {
				t.Fatal("fresh intent revived old task")
			}
			if worked, err := f.engine.RunOne(f.ctx, "synthetic-regrant-worker"); err != nil || !worked {
				t.Fatal("actual worker after regrant", err)
			}
			f.call(owner, "/api/system/logs/bundles/download?taskUUID="+fresh.TaskUUID, nil, 200)
		})
	}
}

func TestOperationalLogsHTTPStrictSchemaGateBeforeAuthenticationAndBookkeeping(t *testing.T) {
	for _, mode := range []string{"unmigrated", "migration-lock-timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := newOperationalHTTPFixture(t, mode != "unmigrated")
			var actor *operationalHTTPActor
			var attempts, audits, proof int64
			code := "schema_incompatible"
			if mode == "migration-lock-timeout" {
				actor = f.actor("operational-gate-owner", "default", "", true)
				attempts = f.count(`SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts`)
				audits = f.count(`SELECT count(*) FROM adtr.auth_audit`)
				proof = f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, actor.id)
				cfg := f.cfg.Copy()
				cfg.RuntimeParams["statement_timeout"] = "100ms"
				f.installHandlers(cfg)
				f.exec(`SELECT pg_advisory_lock(734192801)`)
				defer f.exec(`SELECT pg_advisory_unlock(734192801)`)
				code = "schema_unavailable"
			}
			body := map[string]any{"startTm": "2026-10-07T00:00:00Z", "endTm": "2026-10-07T01:00:00Z", "systemType": []string{"api"}, "idempotencyKey": "schema-gate-intent", "actorPassword": operationalHTTPPassword, "totpCode": operationalCode(time.Now().Unix() / 30)}
			f.trace.take()
			for _, path := range []string{"/api/system/logs", "/api/system/logs/sources", "/api/system/logs/bundles/history", "/api/system/logs/bundles/detail?taskUUID=" + operationalHTTPMissingTask, "/api/system/logs/bundles/download?taskUUID=" + operationalHTTPMissingTask} {
				f.error(f.call(nil, path, nil, 503), code)
			}
			f.error(f.call(actor, "/api/system/logs/bundles", body, 503), code)
			f.error(f.call(actor, "/api/system/logs/bundles/cancel", map[string]any{"taskUUID": operationalHTTPMissingTask, "actorPassword": operationalHTTPPassword, "totpCode": operationalCode(time.Now().Unix() / 30)}, 503), code)
			queries := f.trace.take()
			gateQueries := 0
			for _, query := range queries {
				lower := strings.ToLower(query)
				if strings.Contains(lower, "pg_advisory_xact_lock_shared") {
					gateQueries++
				}
				for _, forbidden := range []string{"adtr.sessions", "adtr.users", "adtr.auth_attempts", "adtr.auth_audit", "adtr.tasks", "set_config("} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("failed schema gate accessed authentication, task, rate, or audit state: %s", query)
					}
				}
			}
			if gateQueries != 7 {
				t.Fatalf("not every valid route checked actual schema first: %d", gateQueries)
			}
			if mode == "unmigrated" {
				if f.count(`SELECT count(*) FROM pg_namespace WHERE nspname='adtr'`) != 0 {
					t.Fatal("HTTP manufactured schema or bookkeeping on an unmigrated database")
				}
			} else if f.count(`SELECT COALESCE(sum(count),0) FROM adtr.auth_attempts`) != attempts || f.count(`SELECT count(*) FROM adtr.auth_audit`) != audits || f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, actor.id) != proof {
				t.Fatal("schema failure changed rate/audit/proof state")
			}
		})
	}
}
