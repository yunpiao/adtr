//go:build integration

package auth_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const assetExportPath = "/api/user-assets/v3/exports"

type userExportHTTPFixture struct {
	*directoryV3HTTPFixture
	observation string
}

// The observation producer alone is synthetic, through the existing guarded
// publication fixture. Export generation, task engine, HTTP, proofs and PostgreSQL
// are real; a separate browser suite supplies real synthetic-TLS-LDAP provenance.
func newUserExportHTTPFixture(t *testing.T, invalidText bool, mutate ...func(*ldapconnection.DirectoryV3Observation)) *userExportHTTPFixture {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	f.grantV3("directory-grant", "platform_admin", "export-producer-grant")
	source := f.submitV3("directory-submit-one", "export-source")
	observation := directoryV3HTTPObservation(12)
	defer observation.Discard()
	if !invalidText {
		observation.Objects[len(observation.Objects)-1].Supplemental.MailBytes = []byte(`=HYPERLINK("https://example.invalid/?a=<tag>&b=1")`)
	}
	for _, change := range mutate {
		change(&observation)
	}
	f.finishV3(f.stageV3(observation), source, tasks.Succeeded)
	engine, err := tasks.New(f.conn.Config(), tasks.ProductionRegistry(f.store.UserAssetExportV3Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	// Exact list/detail routes must not shadow the longer export route prefix.
	mux.Handle(userAssetsV3HTTPPath, f.service.UserAssetV3Handler(f.store))
	mux.Handle(userAssetsV3HTTPPath+"/detail", f.service.UserAssetV3Handler(f.store))
	mux.Handle(assetExportPath, f.service.UserAssetExportV3Handler(f.store, engine))
	mux.Handle(assetExportPath+"/", f.service.UserAssetExportV3Handler(f.store, engine))
	mux.Handle("/api/tasks", f.service.TasksHandler(engine))
	mux.Handle("/api/tasks/", f.service.TasksHandler(engine))
	mux.Handle("/", f.handler)
	f.engine = engine
	f.handler = mux
	return &userExportHTTPFixture{f, source.ID}
}
func (f *userExportHTTPFixture) body(key string) map[string]any {
	return map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedCredentialRevision": "2", "observationId": f.observation, "screenColumns": []string{}, "idempotencyKey": key}
}
func (f *userExportHTTPFixture) submit(actor, key string, edit func(map[string]any)) string {
	f.t.Helper()
	body := f.body(key)
	if edit != nil {
		edit(body)
	}
	out := operationalDecode[struct {
		TaskID   string                           `json:"taskID"`
		Task     tasks.Task                       `json:"task"`
		Request  domains.UserAssetExportV3Payload `json:"request"`
		Replayed bool                             `json:"replayed"`
	}](f.t, f.request(actor, assetExportPath, f.proof(actor, body), 200).Body.Bytes())
	if out.TaskID == "" || out.TaskID != out.Task.ID || out.Task.Kind != domains.UserAssetExportV3KindName || string(out.Task.Result) != "{}" {
		f.t.Fatal("invalid submission", out)
	}
	optional := func(key string) string {
		if value, present := body[key]; present {
			text, ok := value.(string)
			if !ok {
				f.t.Fatalf("invalid test input %s", key)
			}
			return text
		}
		return ""
	}
	columns := body["screenColumns"].([]string)
	if len(columns) == 0 {
		columns = []string{"guid", "sAMAccountName", "sid", "mail", "dn", "whenCreated"}
	}
	want := domains.UserAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, Search: optional("search"), SAMAccountName: optional("sAMAccountName"), StartTm: optional("startTm"), EndTm: optional("endTm"), CreatTm: optional("creatTm"), ScreenColumns: columns}
	if value, present := body["uacDisableBit"]; present {
		text := value.(string)
		want.UACDisableBit = &text
	}
	if !reflect.DeepEqual(out.Request, want) {
		f.t.Fatal("submission canonical request changed")
	}
	return out.TaskID
}
func (f *userExportHTTPFixture) execute() (tasks.Lease, tasks.Outcome) {
	f.t.Helper()
	lease, err := f.engine.Claim(f.ctx, "user-export-test")
	if err != nil {
		f.t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		f.t.Fatal(err)
	}
	out := f.store.UserAssetExportV3Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	return lease, out
}
func (f *userExportHTTPFixture) finish(lease tasks.Lease, out tasks.Outcome) tasks.Task {
	f.t.Helper()
	task, err := f.engine.Finish(f.ctx, lease, out)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}
func (f *userExportHTTPFixture) detail(actor, id string) domains.UserAssetExportV3Detail {
	return operationalDecode[domains.UserAssetExportV3Detail](f.t, f.request(actor, assetExportPath+"/detail?taskID="+id, nil, 200).Body.Bytes())
}
func workbookRows(t *testing.T, raw []byte) [][]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, entry := range z.File {
		r, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if entry.Name == "xl/workbook.xml" && !bytes.Contains(body, []byte(`name="UserAssets"`)) {
			t.Fatal("wrong sheet")
		}
		if entry.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		var sheet struct {
			Rows []struct {
				Cells []struct {
					Type   string `xml:"t,attr"`
					Inline struct {
						Text string `xml:"t"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if xml.Unmarshal(body, &sheet) != nil {
			t.Fatal("invalid XML")
		}
		for _, row := range sheet.Rows {
			cells := []string{}
			for _, cell := range row.Cells {
				if cell.Type != "inlineStr" {
					t.Fatal("active/nonliteral cell")
				}
				cells = append(cells, cell.Inline.Text)
			}
			out = append(out, cells)
		}
	}
	return out
}

func TestUserAssetExportV3FullDatasetLiteralDownloadAndReceipt(t *testing.T) {
	f := newUserExportHTTPFixture(t, false)
	id := f.submit("directory-admin", "all-users", nil)
	if d := f.detail("directory-admin", id); d.DownloadReady || d.DownloadStatus != "pending" {
		t.Fatal(d)
	}
	f.request("directory-admin-other", assetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-foreign", assetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-http-reader", assetExportPath+"/detail?taskID="+id, nil, 403)
	replay := operationalDecode[map[string]any](t, f.request("directory-admin", assetExportPath, f.proof("directory-admin", f.body("all-users")), 200).Body.Bytes())
	if replay["taskID"] != id || replay["replayed"] != true {
		t.Fatal("original operation replay", replay)
	}
	receipt := operationalDecode[map[string]any](t, f.request("directory-admin", assetExportPath+"/receipt?domainId=domain-one&idempotencyKey=all-users", nil, 200).Body.Bytes())
	if receipt["taskID"] != id {
		t.Fatal(receipt)
	}
	lease, out := f.execute()
	if out.State != tasks.Succeeded {
		t.Fatal(out)
	}
	if f.count(`SELECT progress FROM adtr.tasks WHERE task_id=$1`, id) != 95 {
		t.Fatal("progress not persisted")
	}
	f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 409)
	done := f.finish(lease, out)
	if done.State != tasks.Succeeded || done.Progress != 100 {
		t.Fatal(done)
	}
	detail := f.detail("directory-admin", id)
	if !detail.DownloadReady || detail.DownloadStatus != "ready" || detail.Artifact == nil || detail.Artifact.RowCount != 12 || len(detail.Request.ScreenColumns) != 6 || string(detail.Task.Result) != "{}" {
		t.Fatal(detail)
	}
	downloaded := f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 200)
	raw := downloaded.Body.Bytes()
	sum := sha256.Sum256(raw)
	if downloaded.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(f.clients["directory-admin"].id, 10) || downloaded.Header().Get("Content-Length") != strconv.Itoa(len(raw)) || downloaded.Header().Get("X-ADTR-Artifact-SHA256") != hex.EncodeToString(sum[:]) || downloaded.Header().Get("X-ADTR-Row-Count") != "12" || downloaded.Header().Get("Content-Disposition") != `attachment; filename="`+domains.UserAssetExportV3Filename(id)+`"` {
		t.Fatal("download metadata mismatch")
	}
	rows := workbookRows(t, raw)
	if len(rows) != 13 || len(rows[0]) != 6 || rows[1][0] != "00000000-0000-0000-0000-000000000001" || rows[12][0] != "00000000-0000-0000-0000-00000000000c" || !strings.HasPrefix(rows[1][3], "=HYPERLINK(") {
		t.Fatal("all matching users/literal text lost", rows)
	}
	generic := operationalDecode[tasks.Detail](t, f.request("directory-admin", "/api/tasks/detail?taskUUID="+id, nil, 200).Body.Bytes())
	if string(generic.Task.Result) != "{}" || string(generic.Task.Cursor) != "{}" {
		t.Fatal("generic metadata exposure")
	}
	submitted, finished := false, false
	for _, event := range generic.Events {
		submitted = submitted || event.Action == "submitted"
		finished = finished || event.Action == "finished" && event.State == tasks.Succeeded
	}
	if !submitted || !finished {
		t.Fatal("generic task history lost persisted lifecycle")
	}
	emptyID := f.submit("directory-admin-other", "no-users", func(b map[string]any) { b["search"] = "unmatched-unique"; b["screenColumns"] = []string{"guid"} })
	l, o := f.execute()
	if f.finish(l, o).State != tasks.Succeeded {
		t.Fatal(o)
	}
	empty := f.request("directory-admin-other", assetExportPath+"/download?taskID="+emptyID, nil, 200)
	if len(workbookRows(t, empty.Body.Bytes())) != 1 {
		t.Fatal("zero match not real workbook")
	}
	// Chunk tampering must fail before success bytes, even with a valid manifest.
	f.exec(`UPDATE adtr.user_asset_export_chunks SET data=decode('00','hex') WHERE task_id=$1`, id)
	corrupt := f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 500)
	if bytes.HasPrefix(corrupt.Body.Bytes(), []byte("PK")) {
		t.Fatal("partial200 workbook")
	}
}

func TestUserAssetExportV3CancellationCleanupAndOriginalReceipt(t *testing.T) {
	for _, phase := range []string{"queued", "staged"} {
		t.Run(phase, func(t *testing.T) {
			f := newUserExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "cancel-users", nil)
			var lease tasks.Lease
			var out tasks.Outcome
			if phase == "staged" {
				lease, out = f.execute()
				if out.State != tasks.Succeeded {
					t.Fatal(out)
				}
			}
			f.request("directory-admin", assetExportPath+"/cancel", f.proof("directory-admin", map[string]any{"taskID": id}), 200)
			if phase == "staged" {
				if f.detail("directory-admin", id).Task.State != tasks.CancelRequested {
					t.Fatal("cancel falsely finished")
				}
				if task := f.finish(lease, out); task.State != tasks.Cancelled {
					t.Fatal(task)
				}
			}
			d := f.detail("directory-admin", id)
			if d.Task.State != tasks.Cancelled || d.DownloadReady || d.DownloadStatus != "cancelled" {
				t.Fatal(d)
			}
			if f.count(`SELECT count(*) FROM adtr.user_asset_export_snapshots WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.user_asset_export_manifests WHERE task_id=$1`, id) != 0 {
				t.Fatal("cancelled artifact retained")
			}
			f.request("directory-admin", assetExportPath+"/receipt?domainId=domain-one&idempotencyKey=cancel-users", nil, 200)
			f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 409)
		})
	}
}

func TestUserAssetExportV3FinishSourceRotationAndRevocation(t *testing.T) {
	for _, mode := range []string{"source", "actor"} {
		t.Run(mode, func(t *testing.T) {
			f := newUserExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "staged-race", nil)
			lease, out := f.execute()
			if out.State != tasks.Succeeded {
				t.Fatal(out)
			}
			if mode == "source" { // A real new policy snapshot changes source eligibility without bumping actor epoch.
				policy, err := os.ReadFile(f.environment["ADTR_LDAP_EGRESS_POLICY_FILE"])
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "changed-policy.json")
				if err = os.WriteFile(path, bytes.ReplaceAll(policy, []byte("192.0.2.0/24"), []byte("192.0.2.0/25")), 0600); err != nil {
					t.Fatal(err)
				}
				f.environment["ADTR_LDAP_EGRESS_POLICY_FILE"] = path
				changedStore := domains.New(f.runtimeWithGates(true, true))
				f.engine, err = tasks.New(f.conn.Config(), tasks.ProductionRegistry(changedStore.UserAssetExportV3Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
				if err != nil {
					t.Fatal(err)
				}
				f.store = changedStore
				mux := http.NewServeMux()
				mux.Handle(assetExportPath, f.service.UserAssetExportV3Handler(changedStore, f.engine))
				mux.Handle(assetExportPath+"/", f.service.UserAssetExportV3Handler(changedStore, f.engine))
				mux.Handle("/", f.handler)
				f.handler = mux
				f.error(f.request("directory-admin-other", assetExportPath, f.proof("directory-admin-other", f.body("new-key-with-stale-policy")), 409), "directory_observation_unavailable")
			} else {
				f.exec(`UPDATE adtr.users SET disabled=true WHERE id=$1`, f.clients["directory-admin"].id)
			}
			done := f.finish(lease, out)
			if done.State != tasks.Failed {
				t.Fatal("stale artifact published", done)
			}
			if mode == "source" && done.Error != "export_source_changed" || mode == "actor" && done.Error != "authorization_revoked" {
				t.Fatal(done)
			}
			if f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1`, id) != 0 {
				t.Fatal("failure did not clean artifact")
			}
			if mode == "source" {
				f.request("directory-admin", assetExportPath+"/receipt?domainId=domain-one&idempotencyKey=staged-race", nil, 200)
				replay := operationalDecode[map[string]any](t, f.request("directory-admin", assetExportPath, f.proof("directory-admin", f.body("staged-race")), 200).Body.Bytes())
				if replay["taskID"] != id || replay["replayed"] != true {
					t.Fatal("source loss hid original receipt")
				}
			}
		})
	}
}

func TestUserAssetExportV3SelectedInvalidXMLFailsWithoutFile(t *testing.T) {
	f := newUserExportHTTPFixture(t, true)
	id := f.submit("directory-admin", "invalid-xml", nil)
	lease, out := f.execute()
	if out.State != tasks.Failed || out.Code != "export_invalid_text" {
		t.Fatal(out)
	}
	if done := f.finish(lease, out); done.State != tasks.Failed {
		t.Fatal(done)
	}
	if d := f.detail("directory-admin", id); d.DownloadStatus != "failed" || d.DownloadReady {
		t.Fatal(d)
	}
	f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 409)
	clean := f.submit("directory-admin-other", "unselected-invalid-xml", func(b map[string]any) { b["screenColumns"] = []string{"guid"} })
	l, o := f.execute()
	if f.finish(l, o).State != tasks.Succeeded {
		t.Fatal("unselected codec-valid NUL became corruption", o)
	}
	f.request("directory-admin-other", assetExportPath+"/download?taskID="+clean, nil, 200)
}

func TestUserAssetExportV3RetryUsesSameSnapshotAndFencesOldChunks(t *testing.T) {
	f := newUserExportHTTPFixture(t, false)
	id := f.submit("directory-admin", "retry-users", nil)
	first, err := f.engine.Claim(f.ctx, "interrupted-export")
	if err != nil {
		t.Fatal(err)
	}
	first, err = f.engine.Start(f.ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var version int64
	out := f.store.UserAssetExportV3Kind().Execute(f.ctx, tasks.Execution{Task: first.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		calls++
		if calls == 3 {
			return 0, &pgconn.PgError{Code: "40001"}
		}
		next, e := f.engine.WithTx(ctx, first, v, work)
		version = next
		return next, e
	}})
	if out.Code != "database_unavailable" || !out.Retryable || f.finish(first, out).State != tasks.RetryWait {
		t.Fatal("transient not safely retryable", out)
	}
	if f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1 AND fencing_token=$2`, id, first.Token) != 1 {
		t.Fatal("test must interrupt after real chunk persistence")
	}
	f.exec(`UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id)
	second, secondOut := f.execute()
	if second.Token <= first.Token || secondOut.State != tasks.Succeeded {
		t.Fatal(secondOut)
	}
	_, err = f.engine.WithTx(f.ctx, first, version, func(context.Context, pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
		t.Fatal("old lease entered callback")
		return 1, []byte(`{}`), []byte(`{}`), nil
	})
	if !errors.Is(err, tasks.ErrLeaseLost) {
		t.Fatal("stale lease not fenced", err)
	}
	if f.finish(second, secondOut).State != tasks.Succeeded {
		t.Fatal(secondOut)
	}
	if f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1 AND fencing_token<>$2`, id, second.Token) != 0 {
		t.Fatal("obsolete attempt bytes retained")
	}
	f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 200)
}

func TestUserAssetExportV3IndependentPermissionAndPermanentEpochRevocation(t *testing.T) {
	f := newUserExportHTTPFixture(t, false)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'tasks',true,true),($1,$2,'user_asset_exports',true,true)`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	if f.count(`SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets' AND writeable`, governanceHTTPTenant, directoryHTTPReaderRole) != 0 || f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE role_id=$1 AND allowed`, directoryHTTPReaderRole) != 0 {
		t.Fatal("reader fixture accidentally gained collector rights")
	}
	id := f.submit("directory-http-reader", "reader-export", nil)
	lease, out := f.execute()
	if f.finish(lease, out).State != tasks.Succeeded {
		t.Fatal(out)
	}
	f.request("directory-http-reader", assetExportPath+"/download?taskID="+id, nil, 200)
	f.exec(`UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='user_asset_exports'`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	f.request("directory-http-reader", assetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-http-reader", assetExportPath+"/receipt?domainId=domain-one&idempotencyKey=reader-export", nil, 404)
	f.exec(`UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id=$1 AND role_id=$2 AND mark='user_asset_exports'`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	f.request("directory-http-reader", assetExportPath+"/download?taskID="+id, nil, 404)
}

func TestUserAssetExportV3ConcurrentOriginalKeyIsolation(t *testing.T) {
	f := newUserExportHTTPFixture(t, false)
	payload := domains.UserAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, ScreenColumns: []string{"guid"}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	principal := tasks.Principal{TenantID: governanceHTTPTenant, ActorID: f.clients["directory-admin"].id}
	input := tasks.SubmitInput{TaskName: domains.UserAssetExportV3KindName, DomainID: "domain-one", PayloadVersion: 1, Payload: raw, IdempotencyKey: "concurrent-export"}
	submit := func(p tasks.Principal, in tasks.SubmitInput) (tasks.Submission, error) {
		conn, e := pgx.ConnectConfig(f.ctx, f.conn.Config().Copy())
		if e != nil {
			return tasks.Submission{}, e
		}
		defer conn.Close(f.ctx)
		tx, e := conn.Begin(f.ctx)
		if e != nil {
			return tasks.Submission{}, e
		}
		defer tx.Rollback(f.ctx)
		if e = f.engine.CheckSchemaTx(f.ctx, tx); e != nil {
			return tasks.Submission{}, e
		}
		out, e := f.engine.SubmitTx(f.ctx, tx, p, in)
		if e == nil {
			e = tx.Commit(f.ctx)
		}
		return out, e
	}
	var wg sync.WaitGroup
	results := make([]tasks.Submission, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; results[i], errs[i] = submit(principal, input) }(i)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Task.ID != results[1].Task.ID || results[0].Replayed == results[1].Replayed {
		t.Fatal("concurrent replay split", errs, results)
	}
	other := tasks.Principal{TenantID: governanceHTTPTenant, ActorID: f.clients["directory-admin-other"].id}
	if _, err = submit(other, input); err == nil {
		t.Fatal("another actor adopted original key")
	}
	for _, bit := range []string{"set", "clear", "not_returned"} {
		payload.UACDisableBit = &bit
		input.Payload, _ = json.Marshal(payload)
		_, err = submit(principal, input)
		var failure *tasks.Error
		if !errors.As(err, &failure) || failure.Status != 409 || failure.Code != "idempotency_conflict" {
			t.Fatal("same key changed omitted filter instead of conflicting", err)
		}
	}
	payload.UACDisableBit = nil
	payload.ScreenColumns = []string{"dn"}
	input.Payload, _ = json.Marshal(payload)
	if _, err = submit(principal, input); err == nil {
		t.Fatal("same key changed frozen payload")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='user_assets.v3.export'`) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='submitted'`, results[0].Task.ID) != 1 {
		t.Fatal("duplicate task/event")
	}
}

func TestUserAssetExportV3LostExecutorRecoveryAndCancellationCleanup(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			f := newUserExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "lost-executor", nil)
			lease, out := f.execute()
			if out.State != tasks.Succeeded {
				t.Fatal(out)
			}
			if cancelled {
				f.request("directory-admin", assetExportPath+"/cancel", f.proof("directory-admin", map[string]any{"taskID": id}), 200)
			}
			f.exec(`UPDATE adtr.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id)
			if n, err := f.engine.RecoverExpired(f.ctx); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if _, err := f.engine.Finish(f.ctx, lease, out); !errors.Is(err, tasks.ErrLeaseLost) {
				t.Fatal("lost executor published", err)
			}
			if cancelled {
				d := f.detail("directory-admin", id)
				if d.Task.State != tasks.Failed || d.Task.Error != "cancellation_unconfirmed" || d.DownloadReady {
					t.Fatal("lost executor falsely acknowledged cancellation", d)
				}
				if f.count(`SELECT count(*) FROM adtr.user_asset_export_snapshots WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.user_asset_export_manifests WHERE task_id=$1`, id) != 0 {
					t.Fatal("lost cancellation retained generated bytes")
				}
			} else {
				if f.detail("directory-admin", id).Task.State != tasks.RetryWait {
					t.Fatal("lost replay-safe execution not queued for retry")
				}
				f.exec(`UPDATE adtr.tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1`, id)
				next, result := f.execute()
				if next.Token <= lease.Token || f.finish(next, result).State != tasks.Succeeded {
					t.Fatal(result)
				}
				if f.count(`SELECT count(*) FROM adtr.user_asset_export_chunks WHERE task_id=$1 AND fencing_token<>$2`, id, next.Token) != 0 {
					t.Fatal("old attempt retained")
				}
			}
			if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1`, id) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='lease_recovered'`, id) != 1 {
				t.Fatal("recovery erased receipt/events")
			}
		})
	}
}

func TestUserAssetExportV3NaturalEntitlementExpiryWithoutEpochChange(t *testing.T) {
	f := newUserExportHTTPFixture(t, false)
	var maxADCount int
	var uid, name string
	if err := f.conn.QueryRow(f.ctx, `SELECT max_ad_count,uid,name FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant).Scan(&maxADCount, &uid, &name); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Unix() + 8
	// Existing allowed credential uses require the real proof-bound tenant
	// governance path. Configure the deadline before admission; after this
	// operation the test changes no grants, source pins, epoch or clock.
	f.request("directory-admin-other", "/api/resources/tenant/save", f.proof("directory-admin-other", map[string]any{"maxAdCount": maxADCount, "expireTime": expires, "uid": uid, "name": name}), 200)
	if f.count(`SELECT expire_time FROM adtr.resource_tenant_config WHERE tenant_id=$1`, governanceHTTPTenant) != expires {
		t.Fatal("governed expiry was not persisted")
	}
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-admin"])
	epoch := f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id)
	id := f.submit("directory-admin", "natural-expiry", nil)
	lease, out := f.execute()
	if f.finish(lease, out).State != tasks.Succeeded {
		t.Fatal(out)
	}
	f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 200)
	time.Sleep(max(0, time.Until(time.Unix(expires, 0).Add(50*time.Millisecond))))
	if f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != epoch {
		t.Fatal("test used epoch mutation rather than natural expiry")
	}
	f.request("directory-admin", assetExportPath+"/detail?taskID="+id, nil, 403)
	f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 403)
}

// Keep the strict receipt DTO shared with the no-database shape regression.
// The real endpoint returns all three fields, including its public task.
type userExportHTTPReceipt struct {
	TaskID  string                           `json:"taskID"`
	Task    tasks.Task                       `json:"task"`
	Request domains.UserAssetExportV3Payload `json:"request"`
}

func TestUserAssetExportV3UACReceiptDTOAcceptsCompleteShape(t *testing.T) {
	for _, bit := range []string{"", "set", "clear", "not_returned"} {
		t.Run("filter="+bit, func(t *testing.T) {
			payload := `{"domainId":"domain-one","expectedRevision":"2","expectedCredentialRevision":"2","observationId":"observed","search":"synthetic","sAMAccountName":"","startTm":"","endTm":"","creatTm":"desc","screenColumns":["guid"]}`
			want := domains.UserAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: "observed", Search: "synthetic", CreatTm: "desc", ScreenColumns: []string{"guid"}}
			if bit != "" {
				payload = strings.TrimSuffix(payload, "}") + `,"uacDisableBit":"` + bit + `"}`
				want.UACDisableBit = &bit
			}
			// Independent JSON mirrors the real receipt envelope, not a marshal
			// of the DTO that could hide a missing field in both directions.
			raw := `{"taskID":"export-one","task":{"taskUUID":"export-one","taskName":"user_assets.v3.export","domainId":"domain-one","state":"succeeded"},"request":` + payload + `}`
			receipt := operationalDecode[userExportHTTPReceipt](t, []byte(raw))
			if receipt.TaskID != "export-one" || receipt.Task.ID != receipt.TaskID || receipt.Task.Kind != domains.UserAssetExportV3KindName || receipt.Task.State != tasks.Succeeded || receipt.Task.DomainID != want.DomainID || !reflect.DeepEqual(receipt.Request, want) {
				t.Fatal("complete receipt lost task identity, state or request binding")
			}
		})
	}
}

func TestUserAssetExportV3UACFilteredPagesEqualWorkbookAndBoundReceipt(t *testing.T) {
	// Real PostgreSQL, HTTP and export Worker; only the directory producer's
	// Execute is synthetic. This does not claim TLS LDAP/real-AD set/null coverage.
	for _, bit := range []string{"", "set", "clear", "not_returned"} {
		t.Run("filter="+bit, func(t *testing.T) {
			f := newUserExportHTTPFixture(t, false, func(o *ldapconnection.DirectoryV3Observation) {
				o.Discard()
				*o = directoryV3HTTPObservation(36)
				for i := range o.Objects {
					row := &o.Objects[i]
					name := fmt.Sprintf("synthetic-%02d", i)
					row.Base.SAMAccountName = &name
					row.Supplemental.WhenCreatedBytes = []byte("20260101000000.0Z")
					switch i % 3 {
					case 0:
						row.Base.UserAccountControl = nil
					case 1:
						value := uint32(4294967295)
						row.Base.UserAccountControl = &value
					case 2:
						value := uint32(2147483648)
						row.Base.UserAccountControl = &value
					}
				}
				o.Objects = append(o.Objects, directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "10000000-0000-0000-0000-000000000001", DN: "CN=group,DC=one,DC=invalid", Kind: directoryassets.Group, Classes: []string{"group", "top"}}})
			})
			query := userAssetsV3HTTPPath + "?" + userAssetsV3HTTPQuery("2") + "&observationId=" + f.observation + "&search=synthetic&startTm=2026-01-01T00:00:00Z&endTm=2026-01-02T00:00:00Z&creatTm=desc"
			if bit != "" {
				query += "&uacDisableBit=" + bit
			}
			wantTotal := 12
			if bit == "" {
				wantTotal = 36
			}
			wantRows := [][]string{{"GUID", "SAM", "whenCreated"}}
			for index := 1; index <= (wantTotal+9)/10; index++ {
				page := operationalDecode[domains.UserAssetsV3List](t, f.request("directory-http-reader", query+"&pageIdx="+strconv.Itoa(index), nil, 200).Body.Bytes())
				if !page.Available || page.ObservationID != f.observation || page.Page.Total != wantTotal || page.Page.Pages != (wantTotal+9)/10 {
					t.Fatal("filter count/pin", page.Page)
				}
				for _, row := range page.List {
					if bit == "set" && (row.UserAccountControl == nil || *row.UserAccountControl&2 == 0) || bit == "clear" && (row.UserAccountControl == nil || *row.UserAccountControl&2 != 0) || bit == "not_returned" && row.UserAccountControl != nil {
						t.Fatal("wrong persisted UAC partition")
					}
					wantRows = append(wantRows, []string{row.GUID, *row.SAMAccountName, *row.WhenCreated})
				}
			}
			edit := func(b map[string]any) {
				b["screenColumns"] = []string{"guid", "sAMAccountName", "whenCreated"}
				b["search"], b["startTm"], b["endTm"], b["creatTm"] = "synthetic", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "desc"
				if bit != "" {
					b["uacDisableBit"] = bit
				}
			}
			id := f.submit("directory-admin", "uac-filtered", edit)
			// Conflicts roll back the proof update; exercise them before consuming
			// the actor's second successful current/next TOTP step on replay.
			proofStep := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id)
			for _, different := range []string{"", "set", "clear", "not_returned"} {
				if different == bit {
					continue
				}
				body := f.body("uac-filtered")
				edit(body)
				delete(body, "uacDisableBit")
				if different != "" {
					body["uacDisableBit"] = different
				}
				f.error(f.request("directory-admin", assetExportPath, f.proof("directory-admin", body), 409), "idempotency_conflict")
				if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != proofStep {
					t.Fatal("rejected payload committed a proof update")
				}
			}
			if replay := f.submit("directory-admin", "uac-filtered", edit); replay != id {
				t.Fatal("same key/filter made second task")
			}
			if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='user_assets.v3.export'`) != 1 {
				t.Fatal("changed filter created a second task")
			}
			lease, out := f.execute()
			if f.finish(lease, out).State != tasks.Succeeded {
				t.Fatal(out)
			}
			detail := f.detail("directory-admin", id)
			if detail.Artifact == nil || detail.Artifact.RowCount != wantTotal || detail.Request.Filter().UACDisableBit != bit {
				t.Fatal("artifact/receipt filter binding lost")
			}
			download := f.request("directory-admin", assetExportPath+"/download?taskID="+id, nil, 200)
			if got := workbookRows(t, download.Body.Bytes()); !reflect.DeepEqual(got, wantRows) {
				t.Fatal("whole filtered XLSX differs from paged list", got, wantRows)
			}
			receipt := operationalDecode[userExportHTTPReceipt](t, f.request("directory-admin", assetExportPath+"/receipt?domainId=domain-one&idempotencyKey=uac-filtered", nil, 200).Body.Bytes())
			if receipt.TaskID != id || receipt.Task.ID != id || receipt.Task.Kind != domains.UserAssetExportV3KindName || receipt.Task.State != tasks.Succeeded || receipt.Task.DomainID != detail.Request.DomainID || !reflect.DeepEqual(receipt.Request, detail.Request) {
				t.Fatal("recovered receipt did not preserve task identity, state or frozen request")
			}
			// A new empty export uses its own key and still yields a real header-only workbook.
			empty := f.submit("directory-admin-other", "uac-empty", func(b map[string]any) { edit(b); b["search"] = "does-not-exist" })
			lease, out = f.execute()
			if f.finish(lease, out).State != tasks.Succeeded {
				t.Fatal(out)
			}
			if rows := workbookRows(t, f.request("directory-admin-other", assetExportPath+"/download?taskID="+empty, nil, 200).Body.Bytes()); !reflect.DeepEqual(rows, wantRows[:1]) {
				t.Fatal("zero-row filter did not preserve headers")
			}
		})
	}
}
