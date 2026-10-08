//go:build integration

package auth_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Custom role IDs must use exactly 24 URL-safe characters, including in fixtures.
const historyReadRole = "history-read-role-000001"

// Query tracing observes the real HTTP connection. A history page must not
// perform an extra authorization/detail/artifact query for every returned row.
type historyQueryTrace struct {
	mu sync.Mutex
	n  int
}

func (s *historyQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return ctx
}
func (*historyQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (s *historyQueryTrace) reset() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.n
	s.n = 0
	return n
}

type historyHTTPFixture struct {
	*governanceHTTPFixture
	engine *tasks.Engine
	trace  *historyQueryTrace
}

type historyHTTPRow struct {
	TaskUUID       string  `json:"taskUUID"`
	FileName       string  `json:"fileName"`
	ModelType      string  `json:"modelType"`
	FileType       string  `json:"fileType"`
	State          string  `json:"state"`
	Progress       int     `json:"progress"`
	Error          string  `json:"error"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	Attempt        int     `json:"attempt"`
	MaxAttempts    int     `json:"maxAttempts"`
	NextAttemptAt  *string `json:"nextAttemptAt,omitempty"`
	DownloadReady  bool    `json:"downloadReady"`
	DownloadStatus string  `json:"downloadStatus"`
	RowCount       *int    `json:"rowCount,omitempty"`
	SnapshotAt     *string `json:"snapshotAt,omitempty"`
	DownloadPath   *string `json:"downloadPath,omitempty"`
}
type historyHTTPPage struct {
	Page struct {
		Index int `json:"pageIdx"`
		Size  int `json:"pageSize"`
		Total int `json:"total"`
		Pages int `json:"totalPage"`
	} `json:"page"`
	List      []historyHTTPRow `json:"list"`
	Exhausted bool             `json:"exhausted"`
}

func newHistoryHTTPFixture(t *testing.T) *historyHTTPFixture {
	t.Helper()
	base := newGovernanceHTTPFixture(t) // store.Migrate and store.Ready, with every production guard installed.
	base.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 VALUES($1,$2,'audit',true,true),($1,$2,'audit_exports',true,true)`, governanceHTTPTenant, governanceHTTPRole)
	base.exec(`INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'History read only')`, governanceHTTPTenant, historyReadRole)
	base.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable)
 VALUES($1,$2,'audit',true,false),($1,$2,'audit_exports',true,false)`, governanceHTTPTenant, historyReadRole)
	key := bytes.Repeat([]byte{'h'}, 32)
	base.seedActor("history-owner", "viewer", governanceHTTPRole, false, false, false, true, key)
	base.seedActor("history-other", "viewer", governanceHTTPRole, false, false, false, true, key)
	base.seedActor("history-reader", "viewer", historyReadRole, false, false, false, true, key)
	base.seedActor("history-denied", "viewer", "", false, false, false, true, key)
	trace := &historyQueryTrace{}
	cfg := base.conn.Config().Copy()
	cfg.Tracer = trace
	s, err := auth.New(cfg, key, governanceHTTPOrigin, true)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := tasks.New(cfg, tasks.ProductionRegistry(audit.Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := taskarchive.New(auth.NewArchiveAuthorizer())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/audit", s.AuditHandler(engine))
	mux.Handle("/api/audit/", s.AuditHandler(engine))
	mux.Handle("/api/tasks/archive", s.ArchiveHandler(archive))
	mux.Handle("/api/tasks/restore", s.ArchiveHandler(archive))
	mux.Handle("/api/tasks/", s.TasksHandler(engine))
	mux.Handle("/", base.handler)
	base.handler = mux
	return &historyHTTPFixture{base, engine, trace}
}

func (f *historyHTTPFixture) response(c *governanceHTTPClient, path string, body map[string]any, want int) *httptest.ResponseRecorder {
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
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
	r.RemoteAddr = "127.0.0.1:8721"
	r.Header.Set("Origin", governanceHTTPOrigin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("%s: got HTTP %d, want %d: %s", path, w.Code, want, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		f.t.Fatal("audit response lost no-store/nosniff")
	}
	if want == http.StatusOK && (strings.Contains(path, "/exports/history") || strings.Contains(path, "/exports/detail") || strings.Contains(path, "/exports/download")) {
		if c == nil || w.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(c.id, 10) {
			f.t.Fatal("export response is not bound to the authenticated actor")
		}
	}
	if want >= 400 {
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 1 || out["error"] == nil {
			f.t.Fatal("error exposed artifact bytes or private storage data")
		}
	}
	if c != nil {
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == "adtr_session" {
				c.cookie = cookie
			}
		}
	}
	return w
}

func (f *historyHTTPFixture) history(c *governanceHTTPClient, query string) historyHTTPPage {
	f.t.Helper()
	w := f.response(c, "/api/audit/exports/history"+query, nil, 200)
	var out historyHTTPPage
	d := json.NewDecoder(w.Body)
	d.DisallowUnknownFields() // This DTO must never expose raw task result/cursor, fences or paths.
	if err := d.Decode(&out); err != nil || out.List == nil {
		f.t.Fatal("invalid or overbroad history DTO", err)
	}
	for _, row := range out.List {
		if row.FileName != audit.ArtifactFilename(row.TaskUUID) || strings.ContainsAny(row.FileName, "/\\\r\n") || row.ModelType != "Audit" || row.FileType != "xlsx" {
			f.t.Fatal("history fabricated a producer/format or unsafe filename")
		}
		for _, stamp := range []string{row.CreatedAt, row.UpdatedAt} {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil || !strings.HasSuffix(stamp, "Z") {
				f.t.Fatal("history timestamp is not UTC RFC3339", stamp)
			}
		}
		if !row.DownloadReady && (row.RowCount != nil || row.SnapshotAt != nil || row.DownloadPath != nil) {
			f.t.Fatal("ineligible history row leaked stale artifact metadata")
		}
	}
	return out
}
func historyIDs(page historyHTTPPage) []string {
	ids := make([]string, len(page.List))
	for i, row := range page.List {
		ids[i] = row.TaskUUID
	}
	return ids
}
func historyFind(t *testing.T, page historyHTTPPage, id string) historyHTTPRow {
	t.Helper()
	for _, row := range page.List {
		if row.TaskUUID == id {
			return row
		}
	}
	t.Fatal("export missing from authorized history", id)
	return historyHTTPRow{}
}
func historyExportBody(key, event string) map[string]any {
	return map[string]any{"filterEvent": []string{event}, "startTm": "2020-02-01T00:00:00Z", "endTm": "2020-02-02T00:00:00Z", "createSort": 1, "selectColumn": []string{"event", "eventArgs"}, "idempotencyKey": key}
}
func (f *historyHTTPFixture) submitHTTP(actor, key, event string) string {
	f.t.Helper()
	body := historyExportBody(key, event)
	body["actorPassword"] = governanceHTTPPassword
	body["totpCode"] = governanceHTTPCode(f.t, governanceHTTPSecret)
	w := f.response(f.clients[actor], "/api/audit/exports", body, 200)
	var out struct {
		TaskUUID string `json:"taskUUID"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.TaskUUID == "" {
		f.t.Fatal("real HTTP submission did not return task UUID")
	}
	return out.TaskUUID
}

// Additional owner submissions use the production transactional admission and
// authorizer. HTTP proof coverage uses submitHTTP; tests never reset TOTP replay
// counters to force two protected mutations into the same 30-second interval.
func (f *historyHTTPFixture) submitEngine(c *governanceHTTPClient, key, event string) string {
	f.t.Helper()
	// An empty filter is an array; explicit JSON null is rejected by admission.
	payload, err := json.Marshal(audit.ExportPayload{StartTm: "2020-02-01T00:00:00Z", EndTm: "2020-02-02T00:00:00Z", FilterEvent: []string{event}, CreateSort: 1, LogTypeList: []int{}, SelectColumn: []string{"event", "eventArgs"}})
	if err != nil {
		f.t.Fatal(err)
	}
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		f.t.Fatal(err)
	}
	out, err := f.engine.SubmitTx(f.ctx, tx, tasks.Principal{TenantID: governanceHTTPTenant, ActorID: c.id}, tasks.SubmitInput{TaskName: audit.ExportKind, DomainID: "platform", PayloadVersion: 1, Payload: payload, IdempotencyKey: key})
	if err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return out.Task.ID
}
func (f *historyHTTPFixture) run() {
	f.t.Helper()
	worked, err := f.engine.RunOne(f.ctx, "history-real-audit-worker")
	if err != nil || !worked {
		f.t.Fatal("real audit worker did not execute", worked, err)
	}
}
func (f *historyHTTPFixture) epoch(c *governanceHTTPClient) int64 {
	return f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, c.id)
}
func (f *historyHTTPFixture) session(c *governanceHTTPClient) {
	f.t.Helper()
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		f.t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(nonce[:])
	sum := sha256.Sum256([]byte(token))
	f.exec(`INSERT INTO adtr.sessions(token_hash,user_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 hour')`, hex.EncodeToString(sum[:]), c.id)
	c.cookie = &http.Cookie{Name: "adtr_session", Value: token}
	f.call(c, "/api/auth/me", nil, 200)
}
func (f *historyHTTPFixture) foreignActor() *governanceHTTPClient {
	f.t.Helper()
	c := &governanceHTTPClient{}
	// This actor has completed password setup so requests reach ownership checks.
	err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,must_change,password_updated_at)
 SELECT 'history-foreign','history-foreign',password_hash,'platform_admin',false,clock_timestamp()-interval '1 minute' FROM adtr.users WHERE username='synthetic-bootstrap' RETURNING id`).Scan(&c.id)
	if err != nil {
		f.t.Fatal(err)
	}
	block, err := aes.NewCipher(bytes.Repeat([]byte{'h'}, 32))
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
	sealed := base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(governanceHTTPSecret), []byte(strconv.FormatInt(c.id, 10))))
	f.exec(`UPDATE adtr.users SET mfa_secret=$1 WHERE id=$2`, sealed, c.id)
	f.session(c)
	return c
}

// Insert-only anomalies and fixed timestamps test historical-data defenses.
// No immutable admission/artifact is altered and no SQL guard is disabled.
func (f *historyHTTPFixture) seedTask(c *governanceHTTPClient, id, kind, state string, created time.Time, result any) {
	f.t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts,created_at,updated_at,progress,attempt,result,error_code)
 SELECT $2,tenant_id,'platform',$3,1,'{}','synthetic-history',$1,authorization_version::text,$2,$4,3,$5::timestamptz,$5::timestamptz+interval '1 second',17,1,$6::jsonb,CASE WHEN $4 IN ('failed','partial_failed','dead_letter') THEN 'export_failed' ELSE '' END FROM adtr.users WHERE id=$1`, c.id, id, kind, state, created, string(raw))
}
func (f *historyHTTPFixture) seedSnapshot(c *governanceHTTPClient, id string, captured time.Time, rows int, revision int64, domain, source string) {
	f.t.Helper()
	f.exec(`INSERT INTO adtr.audit_export_snapshots(task_id,tenant_id,actor_id,authorization_version,visibility_revision,captured_at,row_count,columns)
 SELECT $2,tenant_id,id,authorization_version::text,$3,$4,$5,'["event","eventArgs"]' FROM adtr.users WHERE id=$1`, c.id, id, revision, captured, rows)
	if domain != "" {
		f.exec(`INSERT INTO adtr.audit_export_rows(task_id,ordinal,source,source_id,domain_id,row_data) VALUES($1,1,$2,1,$3,'{}')`, id, source, domain)
	}
}

func TestExportHistoryMigratedHTTPRealXLSX(t *testing.T) {
	for _, rowCount := range []int{0, 10001} {
		t.Run(fmt.Sprint(rowCount), func(t *testing.T) {
			f := newHistoryHTTPFixture(t)
			c := f.clients["history-owner"]
			f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at)
 SELECT $1,$2,'user_update',g,'2020-02-01T12:00:00Z'::timestamptz FROM generate_series(1,$3::int) g`, governanceHTTPTenant, c.id, rowCount)
			id := f.submitHTTP("history-owner", "history-real-export", "user_update")
			queued := f.history(c, "")
			if queued.Page.Total != 1 || len(queued.List) != 1 || queued.List[0].TaskUUID != id || queued.List[0].State != "queued" || queued.List[0].DownloadStatus != "not_ready" || queued.List[0].DownloadReady {
				t.Fatal("real submission was not discoverable before capture", queued)
			}
			if f.count(`SELECT count(*) FROM adtr.audit_export_snapshots WHERE task_id=$1`, id) != 0 {
				t.Fatal("HTTP submission fabricated a snapshot")
			}
			f.run()
			// A fresh request models leaving and reopening without remembering UUID.
			ready := f.history(c, "?modelType=Audit&status=succeeded")
			row := historyFind(t, ready, id)
			if !row.DownloadReady || row.DownloadStatus != "eligible" || row.Progress != 100 || row.RowCount == nil || *row.RowCount != rowCount || row.SnapshotAt == nil || row.DownloadPath == nil || *row.DownloadPath != "/api/audit/exports/download?taskUUID="+id {
				t.Fatal("actual completed export metadata was not preserved", row)
			}
			f.response(c, "/api/audit/exports/detail?taskUUID="+id, nil, 200)
			w := f.response(c, *row.DownloadPath, nil, 200)
			if w.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || w.Header().Get("Content-Disposition") != `attachment; filename="`+row.FileName+`"` {
				t.Fatal("download format/name differs from history")
			}
			var digest string
			var length int64
			if err := f.conn.QueryRow(f.ctx, `SELECT sha256,byte_count FROM adtr.audit_export_manifests WHERE task_id=$1`, id).Scan(&digest, &length); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(w.Body.Bytes())
			if length != int64(w.Body.Len()) || digest != hex.EncodeToString(sum[:]) {
				t.Fatal("downloaded bytes differ from immutable manifest")
			}
			rows, texts := historyWorkbook(t, w.Body.Bytes())
			if rows != rowCount+1 || rowCount > 0 && (!strings.Contains(texts, `"targetId": 1}`) || !strings.Contains(texts, `"targetId": 10001}`)) {
				t.Fatal("actual workbook header/first/last rows differ", rows)
			}
			f.response(f.clients["history-other"], *row.DownloadPath, nil, 404)
			f.response(f.foreignActor(), *row.DownloadPath, nil, 404)
			if f.history(f.clients["history-other"], "").Page.Total != 0 {
				t.Fatal("history revealed another actor's export")
			}
		})
	}
}
func historyWorkbook(t *testing.T, data []byte) (int, string) {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal("actual download is not XLSX", err)
	}
	for _, file := range z.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		d := xml.NewDecoder(r)
		rows := 0
		var texts []string
		for {
			token, err := d.Token()
			if err == io.EOF {
				return rows, strings.Join(texts, "\n")
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := token.(xml.StartElement); ok {
				if start.Name.Local == "row" {
					rows++
				}
				if start.Name.Local == "t" {
					var text string
					if err := d.DecodeElement(&text, &start); err != nil {
						t.Fatal(err)
					}
					texts = append(texts, text)
				}
			}
		}
	}
	t.Fatal("XLSX has no worksheet")
	return 0, ""
}

func TestExportHistoryMigratedHTTPFiltersCountsStatesAndBoundedQueries(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	f.trace.reset()
	empty := f.history(c, "")
	emptyQueries := f.trace.reset()
	if empty.Page.Index != 1 || empty.Page.Size != 20 || empty.Page.Total != 0 || empty.Page.Pages != 0 || len(empty.List) != 0 || !empty.Exhausted {
		t.Fatal("empty/default page contract", empty)
	}
	stamp := time.Date(2024, 3, 4, 5, 6, 7, 123456000, time.UTC)
	states := []string{"queued", "running", "retry_wait", "cancel_requested", "succeeded", "failed", "partial_failed", "dead_letter", "cancelled"}
	wantIDs := make([]string, len(states))
	for i, state := range states {
		id := fmt.Sprintf("history-state-%02d", i)
		wantIDs[i] = id
		f.seedTask(c, id, audit.ExportKind, state, stamp, map[string]any{})
	}
	f.exec(`UPDATE adtr.tasks SET next_attempt_at=$1 WHERE task_id='history-state-02'`, stamp.Add(time.Hour))
	f.seedTask(f.clients["history-other"], "other-owner", audit.ExportKind, "queued", stamp, map[string]any{})
	foreign := f.foreignActor()
	f.seedTask(foreign, "other-tenant", audit.ExportKind, "queued", stamp, map[string]any{})
	f.seedTask(c, "health-is-not-export", "infrastructure.health", "queued", stamp, map[string]any{})
	// Every domain-bearing snapshot source participates in authorization, not
	// merely task rows; the source=task/platform exception is deliberately exact.
	for _, source := range []string{"task", "domain", "operation_account", "credential_use", "account_reference"} {
		id := "denied-" + source
		f.seedTask(c, id, audit.ExportKind, "failed", stamp.Add(-time.Hour), map[string]any{})
		f.seedSnapshot(c, id, stamp, 1, 0, "domain-two", source)
	}
	f.seedTask(c, "mixed-domain-snapshot", audit.ExportKind, "succeeded", stamp, map[string]any{})
	f.seedSnapshot(c, "mixed-domain-snapshot", stamp, 2, 0, "domain-one", "domain")
	f.exec(`INSERT INTO adtr.audit_export_rows(task_id,ordinal,source,source_id,domain_id,row_data)
 VALUES('mixed-domain-snapshot',2,'credential_use',2,'domain-two','{}')`)
	f.seedTask(c, "platform-name-is-not-authority", audit.ExportKind, "queued", stamp, map[string]any{})
	f.seedSnapshot(c, "platform-name-is-not-authority", stamp, 1, 0, "platform", "domain")
	f.trace.reset()
	all := f.history(c, "?sortTm=1&pageSize=100")
	manyQueries := f.trace.reset()
	if !reflect.DeepEqual(historyIDs(all), wantIDs) || all.Page.Total != 9 || all.Page.Pages != 1 || !all.Exhausted {
		t.Fatal("scope was applied after count/page or tie ordering is unstable", all)
	}
	if manyQueries != emptyQueries {
		t.Fatalf("history queries grew with returned rows: empty=%d, nine=%d", emptyQueries, manyQueries)
	}
	for i, row := range all.List {
		wantStatus := "not_ready"
		if states[i] == "succeeded" {
			wantStatus = "artifact_invalid" // A succeeded task alone does not prove an artifact exists.
		}
		if row.State != states[i] || row.Progress != 17 || row.Attempt != 1 || row.MaxAttempts != 3 || row.DownloadReady || row.DownloadStatus != wantStatus || row.CreatedAt != stamp.Format(time.RFC3339Nano) || row.UpdatedAt != stamp.Add(time.Second).Format(time.RFC3339Nano) {
			t.Fatal("history flattened canonical state/progress/time", row)
		}
		if states[i] == "retry_wait" && (row.NextAttemptAt == nil || *row.NextAttemptAt != stamp.Add(time.Hour).Format(time.RFC3339Nano)) {
			t.Fatal("retry schedule was lost")
		}
	}
	desc := f.history(c, "?pageSize=4")
	if !reflect.DeepEqual(historyIDs(desc), []string{wantIDs[8], wantIDs[7], wantIDs[6], wantIDs[5]}) || desc.Page.Total != 9 || desc.Page.Pages != 3 || desc.Exhausted {
		t.Fatal("default reverse ordering/page totals", desc)
	}
	last := f.history(c, "?pageIdx=3&pageSize=4")
	if !reflect.DeepEqual(historyIDs(last), []string{wantIDs[0]}) || !last.Exhausted {
		t.Fatal("last page", last)
	}
	beyond := f.history(c, "?pageIdx=4&pageSize=4")
	if len(beyond.List) != 0 || beyond.Page.Index != 4 || beyond.Page.Total != 9 || beyond.Page.Pages != 3 || !beyond.Exhausted {
		t.Fatal("out-of-range page lost true authorized total", beyond)
	}
	filtered := f.history(c, "?status=cancel_requested&status=cancelled&sortTm=1")
	if !reflect.DeepEqual(historyIDs(filtered), []string{wantIDs[3], wantIDs[8]}) || filtered.Page.Total != 2 {
		t.Fatal("canonical OR state filter flattened cancellation", filtered)
	}
	q := url.Values{"startTm": {stamp.In(time.FixedZone("offset", 2*60*60)).Format(time.RFC3339Nano)}, "endTm": {stamp.Add(time.Microsecond).Format(time.RFC3339Nano)}}
	if f.history(c, "?"+q.Encode()).Page.Total != 9 {
		t.Fatal("inclusive creation bound or explicit offset conversion is wrong")
	}
	if f.history(c, "?endTm="+url.QueryEscape(stamp.Format(time.RFC3339Nano))).Page.Total != 0 {
		t.Fatal("exclusive creation upper bound was included")
	}
	// A reader without tasks grants can use this dedicated history endpoint.
	reader := f.clients["history-reader"]
	f.seedTask(reader, "reader-owned", audit.ExportKind, "queued", stamp, map[string]any{})
	f.seedSnapshot(reader, "reader-owned", stamp, 1, 0, "platform", "task")
	if f.history(reader, "").Page.Total != 1 {
		t.Fatal("dedicated history accidentally requires tasks/write permission")
	}
	f.response(f.clients["history-denied"], "/api/audit/exports/history", nil, 403)
	f.response(nil, "/api/audit/exports/history", nil, 401)
	if f.history(foreign, "").Page.Total != 1 {
		t.Fatal("tenant scope lost the foreign actor's own task")
	}
	// A separate time window makes the bounded-all threshold exact.
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts,created_at)
 SELECT 'bulk-'||g,u.tenant_id,'platform','audit.export',1,'{}','synthetic',u.id,u.authorization_version::text,'bulk-'||g,'queued',3,'2025-01-01T00:00:00Z' FROM adtr.users u CROSS JOIN generate_series(1,1000) g WHERE u.id=$1`, c.id)
	f.seedTask(f.clients["history-other"], "bulk-other-owner", audit.ExportKind, "queued", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), map[string]any{})
	bulkQuery := "?pageSize=-1&startTm=2025-01-01T00:00:00Z&endTm=2025-01-02T00:00:00Z"
	f.trace.reset()
	bulk := f.history(c, bulkQuery)
	if queries := f.trace.reset(); queries != emptyQueries {
		t.Fatalf("history query count grew for 1000 rows: empty=%d, bulk=%d", emptyQueries, queries)
	}
	if bulk.Page.Size != -1 || bulk.Page.Total != 1000 || bulk.Page.Pages != 1 || len(bulk.List) != 1000 || !bulk.Exhausted {
		t.Fatal("bounded all did not return exactly the allowed 1000 rows")
	}
	f.seedTask(c, "bulk-1001", audit.ExportKind, "queued", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), map[string]any{})
	w := f.response(c, "/api/audit/exports/history"+bulkQuery, nil, 422)
	if !strings.Contains(w.Body.String(), "result_too_large") {
		t.Fatal("bounded all silently truncated an oversized authorized result")
	}
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, c.id) != -1 {
		t.Fatal("read-only history consumed a fresh mutation proof")
	}
}

func TestExportHistoryMigratedHTTPNaturalExpiryFiltersBeforePagination(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	// Set expiry through the actual proof-protected tenant route before the
	// submissions. Only passage of wall-clock time changes subsequent eligibility.
	expires := time.Now().Add(8 * time.Second).Unix()
	f.mutate("tenant", "/api/resources/tenant/save", map[string]any{"maxAdCount": 10, "expireTime": expires, "uid": "synthetic-http", "name": "HTTP governance"}, 200)
	f.session(c) // Tenant governance correctly invalidated the earlier session.
	epoch := f.epoch(c)
	f.exec(`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result,occurred_at)
 VALUES($1,$2,'domain-one','domain_update',1,2,1,'success','2020-02-01T12:00:00Z'),($1,$2,'domain-two','domain_update',1,2,1,'success','2020-02-01T12:00:00Z')`, governanceHTTPTenant, c.id)
	domainID := f.submitHTTP("history-owner", "history-expiring-domain", "domain_update")
	f.run()
	f.exec(`INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at)
 VALUES($1,$2,'user_update',$2,'2020-02-01T12:00:00Z')`, governanceHTTPTenant, c.id)
	platformID := f.submitEngine(c, "history-platform-only", "user_update")
	f.run()
	pendingID := f.submitEngine(c, "history-not-yet-captured", "domain_update")
	before := f.history(c, "?sortTm=1")
	if before.Page.Total != 3 || historyFind(t, before, domainID).RowCount == nil || *historyFind(t, before, domainID).RowCount != 1 {
		t.Fatal("real capture did not enforce initial AD domain scope", before)
	}
	if platform := historyFind(t, before, platformID); platform.RowCount == nil || *platform.RowCount != 1 || !platform.DownloadReady {
		t.Fatal("platform-only export must contain its real platform audit row", platform)
	}
	if f.count(`SELECT count(*) FROM adtr.audit_export_rows WHERE task_id=$1 AND source='domain' AND domain_id='domain-one'`, domainID) != 1 {
		t.Fatal("test lacks actual non-task AD snapshot provenance")
	}
	f.response(c, "/api/audit/exports/download?taskUUID="+domainID, nil, 200)
	wait := time.NewTimer(max(0, time.Until(time.Unix(expires, 0).Add(30*time.Millisecond))))
	defer wait.Stop()
	select {
	case <-wait.C:
	case <-f.ctx.Done():
		t.Fatal("expiry observation blocked", f.ctx.Err())
	}
	if f.epoch(c) != epoch {
		t.Fatal("test changed an authorization epoch instead of natural expiry")
	}
	after := f.history(c, "?sortTm=1&pageSize=1")
	if after.Page.Total != 2 || after.Page.Pages != 2 || !reflect.DeepEqual(historyIDs(after), []string{platformID}) || after.Exhausted {
		t.Fatal("expired snapshot leaked through total/page ordering", after)
	}
	pending := f.history(c, "?sortTm=1&pageSize=1&pageIdx=2")
	if !reflect.DeepEqual(historyIDs(pending), []string{pendingID}) || pending.List[0].DownloadStatus != "not_ready" || !pending.Exhausted {
		t.Fatal("pre-snapshot pending task was erased by blanket tenant checks", pending)
	}
	f.response(c, "/api/audit/exports/detail?taskUUID="+domainID, nil, 403)
	f.response(c, "/api/audit/exports/download?taskUUID="+domainID, nil, 403)
	f.response(c, "/api/audit/exports/download?taskUUID="+platformID, nil, 200)
}

func TestExportHistoryMigratedHTTPRevocationRegrantDoesNotReviveOldEpoch(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	id := f.submitHTTP("history-owner", "history-old-epoch", "user_update")
	f.run()
	if !historyFind(t, f.history(c, ""), id).DownloadReady {
		t.Fatal("initial export never became eligible")
	}
	before := f.epoch(c)
	permissions := func(write bool) []map[string]any {
		out := governanceHTTPPermissions("audit")
		return append(out, map[string]any{"mark": "audit_exports", "auth": map[string]bool{"readable": true, "writeable": write}})
	}
	f.mutate("permissions", "/api/access/permissions/save", map[string]any{"roleID": governanceHTTPRole, "permissions": permissions(false)}, 200)
	f.response(c, "/api/audit/exports/history", nil, 401) // Real governance invalidates affected sessions.
	f.mutate("roles", "/api/access/permissions/save", map[string]any{"roleID": governanceHTTPRole, "permissions": permissions(true)}, 200)
	f.session(c) // A new authenticated fixture session, without touching MFA proof or epoch.
	if f.epoch(c) <= before || f.history(c, "").Page.Total != 0 {
		t.Fatal("regrant revived an export admitted under an old epoch")
	}
	f.response(c, "/api/audit/exports/detail?taskUUID="+id, nil, 403)
	f.response(c, "/api/audit/exports/download?taskUUID="+id, nil, 403)
	newID := f.submitEngine(c, "history-current-epoch", "user_update")
	if !reflect.DeepEqual(historyIDs(f.history(c, "")), []string{newID}) {
		t.Fatal("current-epoch submissions did not become discoverable")
	}
}

func TestExportHistoryMigratedHTTPHideAndRestoreInvalidateSnapshot(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	var sourceID int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id,occurred_at)
 VALUES($1,$2,'user_update',$2,'2020-02-01T12:00:00Z') RETURNING id`, governanceHTTPTenant, c.id).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	id := f.submitHTTP("history-owner", "history-hide-snapshot", "user_update")
	f.run()
	if !historyFind(t, f.history(c, ""), id).DownloadReady {
		t.Fatal("initial artifact is unavailable")
	}
	for _, action := range []struct{ actor, path string }{{"grant", "/api/audit/delete"}, {"grant-admin", "/api/audit/restore"}} {
		f.mutate(action.actor, action.path, map[string]any{"id": []string{"auth." + strconv.FormatInt(sourceID, 10)}, "reason": "synthetic visibility change"}, 200)
		page := f.history(c, "")
		row := historyFind(t, page, id)
		if page.Page.Total != 1 || row.State != "succeeded" || row.DownloadReady || row.DownloadStatus != "snapshot_changed" {
			t.Fatal("visibility change erased truthful completion or exposed stale data", row)
		}
		f.response(c, "/api/audit/exports/detail?taskUUID="+id, nil, 409)
		f.response(c, "/api/audit/exports/download?taskUUID="+id, nil, 409)
	}
}

func TestExportHistoryMigratedHTTPRealFailureRetryAndStagedCancellation(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	f.exec(`INSERT INTO adtr.resource_audit(tenant_id,actor_id,action,target_id,occurred_at)
 VALUES($1,$2,'resource_group_update',repeat('x',40000),'2020-02-01T12:00:00Z')`, governanceHTTPTenant, c.id)
	failed := f.submitHTTP("history-owner", "history-real-cell-failure", "resource_group_update")
	f.run()
	row := historyFind(t, f.history(c, ""), failed)
	if row.State != "failed" || row.Error != "export_cell_too_long" || row.DownloadStatus != "not_ready" || row.DownloadReady || row.Progress == 100 {
		t.Fatal("real XLSX writer failure was flattened or published", row)
	}
	f.response(c, "/api/audit/exports/download?taskUUID="+failed, nil, 409)
	id := f.submitEngine(c, "history-retry-cancel", "user_update")
	lease, err := f.engine.Claim(f.ctx, "history-retry-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil || lease.Task.ID != id {
		t.Fatal("claim/start", err)
	}
	if historyFind(t, f.history(c, ""), id).State != "running" {
		t.Fatal("real running state was not discoverable")
	}
	calls := 0
	out := audit.Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
		calls++
		if calls > 1 {
			return 0, &pgconn.PgError{Code: "40001"} // Existing producer's retryable database failure path.
		}
		return f.engine.WithTx(ctx, lease, version, work)
	}})
	if out.State != tasks.Failed || !out.Retryable || out.Code != "database_unavailable" {
		t.Fatal("production exporter did not classify the injected database failure", out)
	}
	if _, err = f.engine.Finish(f.ctx, lease, out); err != nil {
		t.Fatal(err)
	}
	row = historyFind(t, f.history(c, ""), id)
	if row.State != "retry_wait" || row.NextAttemptAt == nil || row.DownloadReady || row.Error != "database_unavailable" {
		t.Fatal("real retry_wait was flattened", row)
	}
	f.exec(`UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id)
	lease, err = f.engine.Claim(f.ctx, "history-cancel-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	out = audit.Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, version, work)
	}})
	if out.State != tasks.Succeeded || f.count(`SELECT count(*) FROM adtr.audit_export_manifests WHERE task_id=$1`, id) != 1 {
		t.Fatal("real exporter did not stage its XLSX", out)
	}
	if historyFind(t, f.history(c, ""), id).DownloadStatus != "not_ready" {
		t.Fatal("staging a manifest published a running task")
	}
	tx, err := f.conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.CancelTx(f.ctx, tx, tasks.Principal{TenantID: governanceHTTPTenant, ActorID: c.id}, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	row = historyFind(t, f.history(c, ""), id)
	if row.State != "cancel_requested" || row.DownloadReady {
		t.Fatal("cancel requested became a false terminal state", row)
	}
	final, err := f.engine.Finish(f.ctx, lease, out)
	if err != nil || final.State != tasks.Cancelled {
		t.Fatal("real completion race did not preserve cancellation", err)
	}
	row = historyFind(t, f.history(c, ""), id)
	if row.State != "cancelled" || row.DownloadReady || row.DownloadStatus != "not_ready" || row.RowCount != nil {
		t.Fatal("cancelled task exposed its staged workbook", row)
	}
	f.response(c, "/api/audit/exports/download?taskUUID="+id, nil, 409)
}

func TestExportHistoryMigratedHTTPRejectsClientPathsAliasesAndUnknownFields(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	for _, query := range []string{
		"file_name=../../secret", "file_type=csv", "filePath=/etc/passwd", "tenantId=history-foreign", "actorId=1", "taskUUID=anything", "visibility=all", "state=succeeded",
		"pageIdx=0", "pageIdx=01", "pageIdx=1000001", "pageIdx=", "pageIdx=1&pageIdx=2", "pageSize=0", "pageSize=101", "pageSize=-1&pageIdx=2", "sortTm=0", "sortTm=%2B1",
		"status=success", "status=padding", "status=STARTED", "status=queued,failed", "status=queued&status=queued", "status=", "modelType=Audit&modelType=Audit", "modelType=",
		"startTm=2024-01-01", "startTm=2024-01-01T00:00:00", "startTm=2024-01-01T00:00:00.1234567Z", "startTm=2024-01-02T00:00:00Z&endTm=2024-01-01T00:00:00Z", "startTm=2024-01-01T00:00:00Z&endTm=2024-01-01T00:00:00Z", "status=%FF",
	} {
		w := f.response(c, "/api/audit/exports/history?"+query, nil, 400)
		if !strings.Contains(w.Body.String(), "invalid_input") {
			t.Fatal("untrusted history filter accepted or misclassified", query)
		}
	}
	for _, query := range []string{"modelType=Alert", "modelType=Audit&modelType=System", "modelType=audit"} {
		if !strings.Contains(f.response(c, "/api/audit/exports/history?"+query, nil, 400).Body.String(), "unsupported_model_type") {
			t.Fatal("unsupported producer was silently ignored")
		}
	}
	if !strings.Contains(f.response(c, "/api/audit/exports/history?appType=1", nil, 400).Body.String(), "unsupported_app_type") {
		t.Fatal("appType invented application authority")
	}
	for _, path := range []string{
		"/api/audit/exports/download?file_name=../../secret&file_type=xlsx",
		"/api/audit/exports/download?taskUUID=..%2F..%2Fsecret",
		"/api/audit/exports/detail?taskUUID=..%2Fsecret",
		"/api/audit/exports/download?taskUUID=x&filePath=secret",
		"/api/audit/exports/detail?taskUUID=x&taskUUID=y",
	} {
		f.response(c, path, nil, 400)
	}
	body := historyExportBody("history-unknown-submit", "user_update")
	body["actorPassword"], body["totpCode"], body["file_name"] = governanceHTTPPassword, governanceHTTPCode(t, governanceHTTPSecret), "../secret"
	f.response(c, "/api/audit/exports", body, 400)
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, c.id) != -1 || f.count(`SELECT count(*) FROM adtr.tasks`) != 0 {
		t.Fatal("rejected input consumed proof or admitted work")
	}
	w := f.response(c, "/api/audit/exports/history", map[string]any{}, 405)
	if w.Header().Get("Allow") != "GET" {
		t.Fatal("history mutation method was not rejected")
	}
	f.response(c, "/api/audit/exports/history/", nil, 404)
}

func TestExportHistoryMigratedHTTPMetadataCorruptionAndArchiveDefense(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	original := f.submitHTTP("history-owner", "history-integrity-original", "user_update")
	f.run()
	originalBytes := f.response(c, "/api/audit/exports/download?taskUUID="+original, nil, 200).Body.Bytes()
	var raw []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT result FROM adtr.tasks WHERE task_id=$1`, original).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result struct {
		ArtifactToken int64     `json:"artifactToken"`
		RowCount      int       `json:"rowCount"`
		SnapshotAt    time.Time `json:"snapshotAt"`
		SHA256        string    `json:"sha256"`
		ByteCount     int64     `json:"byteCount"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.RowCount != 0 || result.ByteCount != int64(len(originalBytes)) {
		t.Fatal("actual artifact fixture is incomplete", err)
	}
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing-snapshot", nil},
		{"missing-manifest", nil},
		{"wrong-manifest-digest", nil},
		{"wrong-manifest-rows", nil},
		{"wrong-result-rows", func(v map[string]any) { v["rowCount"] = 1 }},
		{"wrong-result-bytes", func(v map[string]any) { v["byteCount"] = result.ByteCount + 1 }},
		{"wrong-snapshot-time", func(v map[string]any) { v["snapshotAt"] = result.SnapshotAt.Add(time.Second) }},
		{"token-string", func(v map[string]any) { v["artifactToken"] = "not-an-integer" }},
		{"token-overflow", func(v map[string]any) { v["artifactToken"] = json.RawMessage("9223372036854775808") }},
		{"token-zero", func(v map[string]any) { v["artifactToken"] = 0 }},
		{"hash-object", func(v map[string]any) { v["sha256"] = map[string]any{"private": "must not leak"} }},
		{"missing-result-field", func(v map[string]any) { delete(v, "rowCount") }},
		{"unknown-result-field", func(v map[string]any) { v["privatePath"] = "/synthetic/private/artifact" }},
		{"oversized-result", func(v map[string]any) { v["privatePath"] = strings.Repeat("private", 1000) }},
		{"wrong-snapshot-actor", nil},
		{"wrong-snapshot-epoch", nil},
		{"missing-chunks", nil},
		{"bad-chunk-digest", nil},
		{"wrong-chunk-sequence", nil},
		{"tampered-chunk-bytes", nil},
	}
	stamp := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	for _, tc := range cases {
		id := "integrity-" + tc.name
		v := map[string]any{"artifactToken": result.ArtifactToken, "rowCount": result.RowCount, "snapshotAt": result.SnapshotAt, "sha256": result.SHA256, "byteCount": result.ByteCount}
		if tc.change != nil {
			tc.change(v)
		}
		f.seedTask(c, id, audit.ExportKind, "succeeded", stamp, v)
		if tc.name == "missing-snapshot" {
			continue
		}
		switch tc.name {
		case "wrong-snapshot-actor":
			f.seedSnapshot(f.clients["history-other"], id, result.SnapshotAt, result.RowCount, 0, "", "")
		case "wrong-snapshot-epoch":
			f.exec(`INSERT INTO adtr.audit_export_snapshots(task_id,tenant_id,actor_id,authorization_version,visibility_revision,captured_at,row_count,columns)
 SELECT $2,tenant_id,id,(authorization_version-1)::text,0,$3,0,'["event","eventArgs"]' FROM adtr.users WHERE id=$1`, c.id, id, result.SnapshotAt)
		default:
			f.seedSnapshot(c, id, result.SnapshotAt, result.RowCount, 0, "", "")
		}
		if tc.name == "missing-manifest" {
			continue
		}
		digest, rows := result.SHA256, result.RowCount
		if tc.name == "wrong-manifest-digest" {
			digest = strings.Repeat("a", 64)
		}
		if tc.name == "wrong-manifest-rows" {
			rows++
		}
		f.exec(`INSERT INTO adtr.audit_export_manifests(task_id,fencing_token,byte_count,chunk_count,row_count,sha256) VALUES($1,$2,$3,1,$4,$5)`, id, result.ArtifactToken, result.ByteCount, rows, digest)
		if tc.name == "missing-chunks" {
			continue
		}
		data, chunkDigest, index := append([]byte(nil), originalBytes...), result.SHA256, 0
		switch tc.name {
		case "bad-chunk-digest":
			chunkDigest = strings.Repeat("b", 64)
		case "wrong-chunk-sequence":
			index = 1
		case "tampered-chunk-bytes":
			data[0] ^= 0xff
		}
		f.exec(`INSERT INTO adtr.audit_export_chunks(task_id,fencing_token,chunk_no,data,sha256) VALUES($1,$2,$3,$4,$5)`, id, result.ArtifactToken, index, data, chunkDigest)
	}
	page := f.history(c, "?pageSize=100")
	if page.Page.Total != len(cases)+1 {
		t.Fatal("one corrupt artifact aborted or erased unrelated history rows", page.Page.Total)
	}
	for _, tc := range cases {
		id := "integrity-" + tc.name
		row := historyFind(t, page, id)
		metadataEligible := tc.name == "missing-chunks" || tc.name == "bad-chunk-digest" || tc.name == "wrong-chunk-sequence" || tc.name == "tampered-chunk-bytes"
		wantStatus := "artifact_invalid"
		if metadataEligible {
			wantStatus = "eligible"
		}
		if row.State != "succeeded" || row.DownloadStatus != wantStatus || row.DownloadReady != metadataEligible {
			t.Fatal("metadata eligibility confused with task success or full byte verification", tc.name, row)
		}
		// Every invalid metadata or byte fixture must fail before any artifact
		// response begins, including those intentionally metadata-eligible above.
		w := f.response(c, "/api/audit/exports/download?taskUUID="+id, nil, 500)
		if strings.Contains(w.Body.String(), "/synthetic/private") || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("failed download exposed storage metadata or began attachment")
		}
	}
	// Unsafe historical error text is never copied to a user-facing message.
	f.seedTask(c, "unsafe-error", audit.ExportKind, "failed", stamp, map[string]any{})
	f.exec(`UPDATE adtr.tasks SET error_code=$2 WHERE task_id=$1`, "unsafe-error", "driver secret /synthetic/private\nconnection")
	if historyFind(t, f.history(c, "?pageSize=100"), "unsafe-error").Error != "export_failed" {
		t.Fatal("historical unsafe error code reached the DTO")
	}
	// F49 still refuses ordinary audit-export archival. Defense against an
	// anomalous old visibility row does not grant a new archive capability.
	// Use the database clock and its exact microsecond precision for the cutoff.
	var archiveBefore time.Time
	if err := f.conn.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&archiveBefore); err != nil {
		t.Fatal(err)
	}
	denied := f.mutate("grant", "/api/tasks/archive", map[string]any{"targets": []map[string]any{{"taskUUID": original, "visibilityVersion": 0}}, "before": archiveBefore.UTC().Format(time.RFC3339Nano), "reason": "synthetic archive rejection", "idempotencyKey": "history-cannot-archive"}, 409)
	if denied["error"] != "task_not_archivable" {
		t.Fatal("audit export archival was not rejected by the eligibility boundary", denied)
	}
	f.exec(`INSERT INTO adtr.task_visibility(task_id,tenant_id,archived,visibility_version,archived_at,archived_by,reason)
 VALUES($1,$2,true,1,clock_timestamp(),$3,'synthetic anomalous historic archive')`, original, governanceHTTPTenant, c.id)
	active := f.history(c, "?pageSize=100")
	ids := historyIDs(active)
	sort.Strings(ids)
	if active.Page.Total != len(cases)+1 || sort.SearchStrings(ids, original) < len(ids) && ids[sort.SearchStrings(ids, original)] == original {
		t.Fatal("unexpectedly archived export survived the authorized count/page filter")
	}
	f.response(c, "/api/audit/exports/detail?taskUUID="+original, nil, 404)
	f.response(c, "/api/audit/exports/download?taskUUID="+original, nil, 404)
}

func TestExportHistoryMigratedHTTPRechecksPermissionAfterTenantLock(t *testing.T) {
	f := newHistoryHTTPFixture(t)
	c := f.clients["history-owner"]
	id := f.submitHTTP("history-owner", "history-concurrent-revocation", "user_update")
	f.run()
	if !historyFind(t, f.history(c, ""), id).DownloadReady {
		t.Fatal("test requires an initially authorized completed export")
	}
	before := f.epoch(c)
	blocker, err := pgx.ConnectConfig(f.ctx, f.conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close(f.ctx)
	tx, err := blocker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if err = f.engine.CheckSchemaTx(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adtr-access:' || $1,0))`, governanceHTTPTenant); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/audit/exports/history", nil).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:8731"
	r.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		f.handler.ServeHTTP(w, r)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("blocked history request did not finish during cleanup")
		}
	})
	// Observe actual PostgreSQL lock contention. A timer alone would not prove
	// that the request reached the authorization boundary before revocation.
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for !waiting && time.Now().Before(deadline) {
		if err = f.conn.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_stat_activity
 WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'
 AND position('adtr-access:' in query)>0)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case <-finished:
				t.Fatalf("history bypassed tenant authorization lock: HTTP %d", w.Code)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if !waiting {
		t.Fatal("history did not reach the held tenant authorization lock")
	}
	// This isolated fixture has no credential-use grants. The installed
	// permission and epoch guards therefore permit this write under the real
	// tenant lock; no trigger, version, session or MFA counter is substituted.
	tag, err := tx.Exec(f.ctx, `UPDATE adtr.access_permissions SET readable=false,writeable=false
 WHERE tenant_id=$1 AND role_id=$2 AND mark='audit_exports'`, governanceHTTPTenant, governanceHTTPRole)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("guarded permission revocation", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("history did not resume after revocation committed", ctx.Err())
	}
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != http.StatusForbidden || len(out) != 1 || out["error"] != "forbidden" {
		t.Fatalf("history used pre-lock authority: HTTP %d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-ADTR-User-ID") != "" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || strings.Contains(w.Body.String(), id) {
		t.Fatal("revoked history request exposed bound success metadata or rows")
	}
	if f.epoch(c) <= before {
		t.Fatal("actual permission guard did not advance the actor epoch")
	}
}
