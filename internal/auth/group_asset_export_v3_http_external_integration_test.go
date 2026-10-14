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
	"github.com/yunpiao/adtr/internal/store"
	"github.com/yunpiao/adtr/internal/tasks"
)

const groupAssetExportPath = "/api/group-assets/v3/exports"

type groupExportHTTPFixture struct {
	*directoryV3HTTPFixture
	observation string
}

// The observation producer alone is synthetic, through the existing guarded
// publication fixture. Export generation, task engine, HTTP, proofs and PostgreSQL
// are real; a separate browser suite supplies real synthetic-TLS-LDAP provenance.
func newGroupExportHTTPFixture(t *testing.T, invalidText bool) *groupExportHTTPFixture {
	f := newDirectoryV3HTTPFixture(t)
	f.bind()
	f.grantV3("directory-grant", "platform_admin", "export-producer-grant")
	source := f.submitV3("directory-submit-one", "export-source")
	observation := directoryV3HTTPObservation(12)
	defer observation.Discard()
	for i := range observation.Objects {
		observation.Objects[i].Base.Kind = directoryassets.Group
		observation.Objects[i].Base.Classes = []string{"group", "top"}
	}
	sam := `=HYPERLINK("x")`
	if invalidText {
		sam = "group-invalid\ufffe"
	}
	observation.Objects[len(observation.Objects)-1].Base.SAMAccountName = &sam
	observation.Objects = append(observation.Objects, directoryassets.StoredObjectV3{Base: directoryassets.Object{GUID: "ffffffff-0000-0000-0000-000000000001", DN: "CN=excluded,DC=one,DC=invalid", Kind: directoryassets.User, Classes: []string{"top", "user"}}})
	f.finishV3(f.stageV3(observation), source, tasks.Succeeded)
	engine, err := tasks.New(f.conn.Config(), tasks.ProductionRegistry(f.store.GroupAssetExportV3Kind(), f.store.UserAssetExportV3Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(groupAssetExportPath, f.service.GroupAssetExportV3Handler(f.store, engine))
	mux.Handle(groupAssetExportPath+"/", f.service.GroupAssetExportV3Handler(f.store, engine))
	mux.Handle("/api/user-assets/v3/exports", f.service.UserAssetExportV3Handler(f.store, engine))
	mux.Handle("/api/user-assets/v3/exports/", f.service.UserAssetExportV3Handler(f.store, engine))
	mux.Handle("/api/group-assets/v3", f.service.GroupAssetsV3Handler(f.store))
	mux.Handle("/api/tasks", f.service.TasksHandler(engine))
	mux.Handle("/api/tasks/", f.service.TasksHandler(engine))
	mux.Handle("/", f.handler)
	f.engine = engine
	f.handler = mux
	return &groupExportHTTPFixture{f, source.ID}
}
func (f *groupExportHTTPFixture) body(key string) map[string]any {
	return map[string]any{"domainId": "domain-one", "expectedRevision": "2", "expectedCredentialRevision": "2", "observationId": f.observation, "screenColumns": []string{}, "idempotencyKey": key}
}
func (f *groupExportHTTPFixture) submit(actor, key string, edit func(map[string]any)) string {
	f.t.Helper()
	body := f.body(key)
	if edit != nil {
		edit(body)
	}
	out := operationalDecode[struct {
		TaskID   string                            `json:"taskID"`
		Task     tasks.Task                        `json:"task"`
		Request  domains.GroupAssetExportV3Payload `json:"request"`
		Replayed bool                              `json:"replayed"`
	}](f.t, f.request(actor, groupAssetExportPath, f.proof(actor, body), 200).Body.Bytes())
	if out.TaskID == "" || out.TaskID != out.Task.ID || out.Task.Kind != domains.GroupAssetExportV3KindName || string(out.Task.Result) != "{}" {
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
		columns = []string{"guid", "sAMAccountName", "sid", "dn", "whenCreated"}
	}
	want := domains.GroupAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, Search: optional("search"), CreatTm: optional("creatTm"), ScreenColumns: columns}
	if !reflect.DeepEqual(out.Request, want) {
		f.t.Fatal("submission canonical request changed")
	}
	return out.TaskID
}
func (f *groupExportHTTPFixture) execute() (tasks.Lease, tasks.Outcome) {
	f.t.Helper()
	lease, err := f.engine.Claim(f.ctx, "group-export-test")
	if err != nil {
		f.t.Fatal(err)
	}
	lease, err = f.engine.Start(f.ctx, lease)
	if err != nil {
		f.t.Fatal(err)
	}
	out := f.store.GroupAssetExportV3Kind().Execute(f.ctx, tasks.Execution{Task: lease.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
		return f.engine.WithTx(ctx, lease, v, work)
	}})
	return lease, out
}
func (f *groupExportHTTPFixture) finish(lease tasks.Lease, out tasks.Outcome) tasks.Task {
	f.t.Helper()
	task, err := f.engine.Finish(f.ctx, lease, out)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}
func (f *groupExportHTTPFixture) detail(actor, id string) domains.GroupAssetExportV3Detail {
	return operationalDecode[domains.GroupAssetExportV3Detail](f.t, f.request(actor, groupAssetExportPath+"/detail?taskID="+id, nil, 200).Body.Bytes())
}
func groupWorkbookRows(t *testing.T, raw []byte) [][]string {
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
		if entry.Name == "xl/workbook.xml" && !bytes.Contains(body, []byte(`name="GroupAssets"`)) {
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

func TestGroupAssetExportV3FullDatasetLiteralDownloadAndReceipt(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	id := f.submit("directory-admin", "all-groups", nil)
	if d := f.detail("directory-admin", id); d.DownloadReady || d.DownloadStatus != "pending" {
		t.Fatal(d)
	}
	f.request("directory-admin-other", groupAssetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-foreign", groupAssetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-http-reader", groupAssetExportPath+"/detail?taskID="+id, nil, 403)
	replay := operationalDecode[map[string]any](t, f.request("directory-admin", groupAssetExportPath, f.proof("directory-admin", f.body("all-groups")), 200).Body.Bytes())
	if replay["taskID"] != id || replay["replayed"] != true {
		t.Fatal("original operation replay", replay)
	}
	receipt := operationalDecode[map[string]any](t, f.request("directory-admin", groupAssetExportPath+"/receipt?domainId=domain-one&idempotencyKey=all-groups", nil, 200).Body.Bytes())
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
	f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 409)
	done := f.finish(lease, out)
	if done.State != tasks.Succeeded || done.Progress != 100 {
		t.Fatal(done)
	}
	detail := f.detail("directory-admin", id)
	if !detail.DownloadReady || detail.DownloadStatus != "ready" || detail.Artifact == nil || detail.Artifact.RowCount != 12 || len(detail.Request.ScreenColumns) != 5 || string(detail.Task.Result) != "{}" {
		t.Fatal(detail)
	}
	downloaded := f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 200)
	raw := downloaded.Body.Bytes()
	sum := sha256.Sum256(raw)
	if downloaded.Header().Get("X-ADTR-User-ID") != strconv.FormatInt(f.clients["directory-admin"].id, 10) || downloaded.Header().Get("Content-Length") != strconv.Itoa(len(raw)) || downloaded.Header().Get("X-ADTR-Artifact-SHA256") != hex.EncodeToString(sum[:]) || downloaded.Header().Get("X-ADTR-Row-Count") != "12" || downloaded.Header().Get("Content-Disposition") != `attachment; filename="`+domains.GroupAssetExportV3Filename(id)+`"` {
		t.Fatal("download metadata mismatch")
	}
	rows := groupWorkbookRows(t, raw)
	if len(rows) != 13 || len(rows[0]) != 5 || rows[1][0] != "00000000-0000-0000-0000-000000000001" || rows[12][0] != "00000000-0000-0000-0000-00000000000c" || !strings.HasPrefix(rows[1][1], "=HYPERLINK(") {
		t.Fatal("all matching groups/literal text lost", rows)
	}
	var listed []directoryassets.PublicObjectV3
	for page := 1; page <= 2; page++ {
		query := "/api/group-assets/v3?" + userAssetsV3HTTPQuery("2") + "&observationId=" + f.observation + "&pageIdx=" + strconv.Itoa(page)
		got := operationalDecode[domains.GroupAssetsV3List](t, f.request("directory-admin", query, nil, 200).Body.Bytes())
		if got.Page.Total != 12 || page == 1 && len(got.List) != 10 {
			t.Fatal("unexpected current list page", got)
		}
		listed = append(listed, got.List...)
	}
	if len(listed) != len(rows)-1 {
		t.Fatal("export truncated to current page")
	}
	for i, row := range listed {
		if rows[i+1][0] != row.GUID || rows[i+1][3] != row.DN {
			t.Fatal("export differs from list facts/order")
		}
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
	emptyID := f.submit("directory-admin-other", "no-groups", func(b map[string]any) { b["search"] = "unmatched-unique"; b["screenColumns"] = []string{"guid"} })
	l, o := f.execute()
	if f.finish(l, o).State != tasks.Succeeded {
		t.Fatal(o)
	}
	empty := f.request("directory-admin-other", groupAssetExportPath+"/download?taskID="+emptyID, nil, 200)
	if len(groupWorkbookRows(t, empty.Body.Bytes())) != 1 {
		t.Fatal("zero match not real workbook")
	}
	// Chunk tampering must fail before success bytes, even with a valid manifest.
	f.exec(`UPDATE adtr.group_asset_export_chunks SET data=decode('00','hex') WHERE task_id=$1`, id)
	corrupt := f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 500)
	if bytes.HasPrefix(corrupt.Body.Bytes(), []byte("PK")) {
		t.Fatal("partial200 workbook")
	}
}

func TestGroupAssetExportV3CancellationCleanupAndOriginalReceipt(t *testing.T) {
	for _, phase := range []string{"queued", "staged"} {
		t.Run(phase, func(t *testing.T) {
			f := newGroupExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "cancel-groups", nil)
			var lease tasks.Lease
			var out tasks.Outcome
			if phase == "staged" {
				lease, out = f.execute()
				if out.State != tasks.Succeeded {
					t.Fatal(out)
				}
			}
			f.request("directory-admin", groupAssetExportPath+"/cancel", f.proof("directory-admin", map[string]any{"taskID": id}), 200)
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
			if f.count(`SELECT count(*) FROM adtr.group_asset_export_snapshots WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.group_asset_export_manifests WHERE task_id=$1`, id) != 0 {
				t.Fatal("cancelled artifact retained")
			}
			f.request("directory-admin", groupAssetExportPath+"/receipt?domainId=domain-one&idempotencyKey=cancel-groups", nil, 200)
			f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 409)
		})
	}
}

func TestGroupAssetExportV3FinishSourceRotationAndRevocation(t *testing.T) {
	for _, mode := range []string{"source", "actor"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupExportHTTPFixture(t, false)
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
				f.engine, err = tasks.New(f.conn.Config(), tasks.ProductionRegistry(changedStore.GroupAssetExportV3Kind()), auth.NewTaskAuthorizer(), store.SchemaVersion)
				if err != nil {
					t.Fatal(err)
				}
				f.store = changedStore
				mux := http.NewServeMux()
				mux.Handle(groupAssetExportPath, f.service.GroupAssetExportV3Handler(changedStore, f.engine))
				mux.Handle(groupAssetExportPath+"/", f.service.GroupAssetExportV3Handler(changedStore, f.engine))
				mux.Handle("/", f.handler)
				f.handler = mux
				f.error(f.request("directory-admin-other", groupAssetExportPath, f.proof("directory-admin-other", f.body("new-key-with-stale-policy")), 409), "directory_observation_unavailable")
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
			if f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1`, id) != 0 {
				t.Fatal("failure did not clean artifact")
			}
			if mode == "source" {
				f.request("directory-admin", groupAssetExportPath+"/receipt?domainId=domain-one&idempotencyKey=staged-race", nil, 200)
				replay := operationalDecode[map[string]any](t, f.request("directory-admin", groupAssetExportPath, f.proof("directory-admin", f.body("staged-race")), 200).Body.Bytes())
				if replay["taskID"] != id || replay["replayed"] != true {
					t.Fatal("source loss hid original receipt")
				}
			}
		})
	}
}

func TestGroupAssetExportV3SelectedInvalidXMLFailsWithoutFile(t *testing.T) {
	f := newGroupExportHTTPFixture(t, true)
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
	f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 409)
	clean := f.submit("directory-admin-other", "unselected-invalid-xml", func(b map[string]any) { b["screenColumns"] = []string{"guid"} })
	l, o := f.execute()
	if f.finish(l, o).State != tasks.Succeeded {
		t.Fatal("unselected codec-valid noncharacter became corruption", o)
	}
	f.request("directory-admin-other", groupAssetExportPath+"/download?taskID="+clean, nil, 200)
}

func TestGroupAssetExportV3RetryUsesSameSnapshotAndFencesOldChunks(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	id := f.submit("directory-admin", "retry-groups", nil)
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
	out := f.store.GroupAssetExportV3Kind().Execute(f.ctx, tasks.Execution{Task: first.Task, WithTx: func(ctx context.Context, v int64, work tasks.FencedWork) (int64, error) {
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
	if f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1 AND fencing_token=$2`, id, first.Token) != 1 {
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
	if f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1 AND fencing_token<>$2`, id, second.Token) != 0 {
		t.Fatal("obsolete attempt bytes retained")
	}
	f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 200)
}

func TestGroupAssetExportV3IndependentPermissionAndPermanentEpochRevocation(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'tasks',true,true),($1,$2,'group_asset_exports',true,true)`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	if f.count(`SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='directory_assets' AND writeable`, governanceHTTPTenant, directoryHTTPReaderRole) != 0 || f.count(`SELECT count(*) FROM adtr.operation_account_use_grants WHERE role_id=$1 AND allowed`, directoryHTTPReaderRole) != 0 {
		t.Fatal("reader fixture accidentally gained collector rights")
	}
	id := f.submit("directory-http-reader", "reader-export", nil)
	lease, out := f.execute()
	if f.finish(lease, out).State != tasks.Succeeded {
		t.Fatal(out)
	}
	f.request("directory-http-reader", groupAssetExportPath+"/download?taskID="+id, nil, 200)
	f.exec(`UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id=$1 AND role_id=$2 AND mark='group_asset_exports'`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	f.request("directory-http-reader", groupAssetExportPath+"/detail?taskID="+id, nil, 404)
	f.request("directory-http-reader", groupAssetExportPath+"/receipt?domainId=domain-one&idempotencyKey=reader-export", nil, 404)
	f.exec(`UPDATE adtr.access_permissions SET writeable=true WHERE tenant_id=$1 AND role_id=$2 AND mark='group_asset_exports'`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	f.request("directory-http-reader", groupAssetExportPath+"/download?taskID="+id, nil, 404)
}

func TestGroupAssetExportV3ConcurrentOriginalKeyIsolation(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	payload := domains.GroupAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, ScreenColumns: []string{"guid"}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	principal := tasks.Principal{TenantID: governanceHTTPTenant, ActorID: f.clients["directory-admin"].id}
	input := tasks.SubmitInput{TaskName: domains.GroupAssetExportV3KindName, DomainID: "domain-one", PayloadVersion: 1, Payload: raw, IdempotencyKey: "concurrent-export"}
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
	payload.ScreenColumns = []string{"dn"}
	input.Payload, _ = json.Marshal(payload)
	if _, err = submit(principal, input); err == nil {
		t.Fatal("same key changed frozen payload")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='group_assets.v3.export'`) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='submitted'`, results[0].Task.ID) != 1 {
		t.Fatal("duplicate task/event")
	}
}

func TestGroupAssetExportV3LostExecutorRecoveryAndCancellationCleanup(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			f := newGroupExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "lost-executor", nil)
			lease, out := f.execute()
			if out.State != tasks.Succeeded {
				t.Fatal(out)
			}
			if cancelled {
				f.request("directory-admin", groupAssetExportPath+"/cancel", f.proof("directory-admin", map[string]any{"taskID": id}), 200)
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
				if f.count(`SELECT count(*) FROM adtr.group_asset_export_snapshots WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1`, id)+f.count(`SELECT count(*) FROM adtr.group_asset_export_manifests WHERE task_id=$1`, id) != 0 {
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
				if f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1 AND fencing_token<>$2`, id, next.Token) != 0 {
					t.Fatal("old attempt retained")
				}
			}
			if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1`, id) != 1 || f.count(`SELECT count(*) FROM adtr.task_events WHERE task_id=$1 AND action='lease_recovered'`, id) != 1 {
				t.Fatal("recovery erased receipt/events")
			}
		})
	}
}

func TestGroupAssetExportV3NaturalEntitlementExpiryWithoutEpochChange(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
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
	f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 200)
	time.Sleep(max(0, time.Until(time.Unix(expires, 0).Add(50*time.Millisecond))))
	if f.count(`SELECT authorization_version FROM adtr.users WHERE id=$1`, f.clients["directory-admin"].id) != epoch {
		t.Fatal("test used epoch mutation rather than natural expiry")
	}
	f.request("directory-admin", groupAssetExportPath+"/detail?taskID="+id, nil, 403)
	f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 403)
}

func TestGroupExportCrossKindKeysTasksAndPermissionsStayIsolated(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	groupID := f.submit("directory-admin", "same-key", nil)
	user := operationalDecode[struct {
		TaskID   string                           `json:"taskID"`
		Task     tasks.Task                       `json:"task"`
		Request  domains.UserAssetExportV3Payload `json:"request"`
		Replayed bool                             `json:"replayed"`
	}](t, f.request("directory-admin", assetExportPath, f.proof("directory-admin", f.body("same-key")), 200).Body.Bytes())
	wantUser := domains.UserAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, ScreenColumns: []string{"guid", "sAMAccountName", "sid", "mail", "dn", "whenCreated"}}
	if user.TaskID == "" || user.TaskID == groupID || user.TaskID != user.Task.ID || user.Task.Kind != domains.UserAssetExportV3KindName || user.Replayed || !reflect.DeepEqual(user.Request, wantUser) || string(user.Task.Result) != "{}" || string(user.Task.Cursor) != "{}" {
		t.Fatal("cross-kind key adopted")
	}
	// Both creates must belong to this same actor to test kind isolation rather
	// than the owner guard. They can consume the current and next TOTP steps.
	// Wait for the real clock to make a third step valid, as the operational
	// HTTP fixture does; never reset replay state, move clocks or widen limits.
	ownerID := f.clients["directory-admin"].id
	committedStep := f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, ownerID)
	for committedStep > time.Now().Unix()/30 {
		timer := time.NewTimer(time.Until(time.Unix(committedStep*30, 0).Add(25 * time.Millisecond)))
		select {
		case <-f.ctx.Done():
			timer.Stop()
			t.Fatal("timed out awaiting an actually fresh owner TOTP step")
		case <-timer.C:
		}
	}
	sensitiveDigest := sha256.Sum256([]byte("sensitive:" + strconv.FormatInt(ownerID, 10)))
	sensitiveBucket := hex.EncodeToString(sensitiveDigest[:])
	if f.count(`SELECT count FROM adtr.auth_attempts WHERE bucket=$1`, sensitiveBucket) != 2 {
		t.Fatal("two owner creates must consume exactly two sensitive attempts")
	}
	for _, tc := range []struct{ path, own, other string }{{groupAssetExportPath, groupID, user.TaskID}, {assetExportPath, user.TaskID, groupID}} {
		raw := f.request("directory-admin", tc.path+"/receipt?domainId=domain-one&idempotencyKey=same-key", nil, 200).Body.Bytes()
		var receiptID, kind string
		var task tasks.Task
		if tc.path == groupAssetExportPath {
			receipt := operationalDecode[struct {
				TaskID  string                            `json:"taskID"`
				Task    tasks.Task                        `json:"task"`
				Request domains.GroupAssetExportV3Payload `json:"request"`
			}](t, raw)
			want := domains.GroupAssetExportV3Payload{DomainID: "domain-one", ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: f.observation, ScreenColumns: []string{"guid", "sAMAccountName", "sid", "dn", "whenCreated"}}
			if !reflect.DeepEqual(receipt.Request, want) {
				t.Fatal("group receipt request crossed kind")
			}
			receiptID, task, kind = receipt.TaskID, receipt.Task, domains.GroupAssetExportV3KindName
		} else {
			receipt := operationalDecode[struct {
				TaskID  string                           `json:"taskID"`
				Task    tasks.Task                       `json:"task"`
				Request domains.UserAssetExportV3Payload `json:"request"`
			}](t, raw)
			if !reflect.DeepEqual(receipt.Request, wantUser) {
				t.Fatal("user receipt request crossed kind")
			}
			receiptID, task, kind = receipt.TaskID, receipt.Task, domains.UserAssetExportV3KindName
		}
		if receiptID != tc.own || task.ID != receiptID || task.Kind != kind || string(task.Result) != "{}" || string(task.Cursor) != "{}" {
			t.Fatal("receipt crossed kind or exposed private metadata")
		}
		for _, suffix := range []string{"/detail", "/download"} {
			f.request("directory-admin", tc.path+suffix+"?taskID="+tc.other, nil, 404)
		}
		f.request("directory-admin", tc.path+"/cancel", f.proof("directory-admin", map[string]any{"taskID": tc.other}), 404)
	}
	// Denied kind-crossing cancellations charge the independent sensitive
	// budget but roll back proof consumption with their failed transaction.
	if f.count(`SELECT mfa_last_step FROM adtr.users WHERE id=$1`, ownerID) != committedStep || f.count(`SELECT count FROM adtr.auth_attempts WHERE bucket=$1`, sensitiveBucket) != 4 {
		t.Fatal("denied cross-kind cancellation changed proof state or attempt accounting")
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id IN ($1,$2) AND state='queued'`, groupID, user.TaskID) != 2 {
		t.Fatal("cross-kind cancel mutated task")
	}
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'tasks',true,true),($1,$2,'user_asset_exports',true,true)`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	f.request("directory-http-reader", groupAssetExportPath+"/receipt?domainId=domain-one&idempotencyKey=same-key", nil, 403)
	check := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": []string{"POST " + groupAssetExportPath, "GET " + groupAssetExportPath + "/receipt", "GET " + groupAssetExportPath + "/detail", "POST " + groupAssetExportPath + "/cancel", "GET " + groupAssetExportPath + "/download"}}, 200).Body.Bytes())
	if !reflect.DeepEqual(check.Results, []bool{false, false, false, false, false}) {
		t.Fatal("user permission promoted group paths", check)
	}
	f.exec(`INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES($1,$2,'group_asset_exports',true,true)`, governanceHTTPTenant, directoryHTTPReaderRole)
	reissueSourceHTTPSession(t, f.governanceHTTPFixture, f.clients["directory-http-reader"])
	check = operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": []string{"POST " + groupAssetExportPath, "GET " + groupAssetExportPath + "/receipt", "GET " + groupAssetExportPath + "/detail", "POST " + groupAssetExportPath + "/cancel", "GET " + groupAssetExportPath + "/download"}}, 200).Body.Bytes())
	if !reflect.DeepEqual(check.Results, []bool{true, true, true, true, true}) {
		t.Fatal("explicit group paths not discoverable", check)
	}
}

func TestGroupExportValidatesExcludedSourceFieldsBeforeCaptureAndFinish(t *testing.T) {
	for _, phase := range []string{"capture", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f := newGroupExportHTTPFixture(t, false)
			id := ""
			var lease tasks.Lease
			var out tasks.Outcome
			if phase == "finish" {
				id = f.submit("directory-admin", "source-corruption", nil)
				lease, out = f.execute()
				if out.State != tasks.Succeeded {
					t.Fatal(out)
				}
			}
			var raw []byte
			if err := f.conn.QueryRow(f.ctx, `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, f.observation).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			// The final object is an excluded user. Corrupt an unselected identity field
			// with a matching body hash so complete codec validation must catch it.
			var body map[string]json.RawMessage
			if json.Unmarshal(raw, &body) != nil {
				t.Fatal("body")
			}
			var objects []json.RawMessage
			if json.Unmarshal(body["objects"], &objects) != nil {
				t.Fatal("objects")
			}
			original := objects[len(objects)-1]
			objects[len(objects)-1] = bytes.Replace(original, []byte(`"primaryGroupIDBytes":null`), []byte(`"primaryGroupIDBytes":"@@@="`), 1)
			if bytes.Equal(original, objects[len(objects)-1]) {
				t.Fatal("corruption not installed")
			}
			body["objects"], _ = json.Marshal(objects)
			raw, _ = json.Marshal(body)
			sum := sha256.Sum256(raw)
			f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
			f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, f.observation, raw, hex.EncodeToString(sum[:]))
			f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
			if phase == "capture" {
				b := f.body("corrupt-source")
				b["search"] = "missing"
				b["screenColumns"] = []string{"guid"}
				f.error(f.request("directory-admin", groupAssetExportPath, f.proof("directory-admin", b), 409), "directory_observation_unavailable")
			} else {
				if done := f.finish(lease, out); done.State != tasks.Failed || done.Error != "export_source_changed" {
					t.Fatal(done)
				}
				if f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1`, id) != 0 {
					t.Fatal("failed artifact retained")
				}
			}
		})
	}
}

func TestGroupExportFinishRejectsManifestAndChunkCorruption(t *testing.T) {
	for _, mode := range []string{"manifest", "chunk", "locator"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupExportHTTPFixture(t, false)
			id := f.submit("directory-admin", "broken-artifact", nil)
			lease, out := f.execute()
			if out.State != tasks.Succeeded {
				t.Fatal(out)
			}
			switch mode {
			case "manifest":
				f.exec(`UPDATE adtr.group_asset_export_manifests SET sha256=repeat('0',64) WHERE task_id=$1`, id)
			case "chunk":
				f.exec(`UPDATE adtr.group_asset_export_chunks SET data=decode('00','hex') WHERE task_id=$1`, id)
			case "locator":
				var result map[string]any
				if json.Unmarshal(out.Result, &result) != nil {
					t.Fatal("result")
				}
				result["artifactToken"] = float64(lease.Token + 1)
				out.Result, _ = json.Marshal(result)
			}
			done := f.finish(lease, out)
			if done.State != tasks.Failed || done.Error != "artifact_invalid" || string(done.Result) != "{}" {
				t.Fatal(done)
			}
			if f.count(`SELECT count(*) FROM adtr.group_asset_export_snapshots WHERE task_id=$1`, id) != 0 || f.count(`SELECT count(*) FROM adtr.group_asset_export_chunks WHERE task_id=$1`, id) != 0 || f.count(`SELECT count(*) FROM adtr.group_asset_export_manifests WHERE task_id=$1`, id) != 0 {
				t.Fatal("invalid artifact retained")
			}
			f.request("directory-admin", groupAssetExportPath+"/download?taskID="+id, nil, 409)
		})
	}
}

func TestGroupExportGenericMutationRoutesCannotBypassDedicatedChecks(t *testing.T) {
	f := newGroupExportHTTPFixture(t, false)
	id := f.submit("directory-admin", "dedicated-only", nil)
	for _, path := range []string{"/api/tasks/cancel", "/api/tasks/recover"} {
		body := map[string]any{"taskUUID": id}
		if path == "/api/tasks/recover" {
			body["idempotencyKey"] = "no-generic-recovery"
		}
		f.error(f.request("directory-admin", path, f.proof("directory-admin", body), 400), "group_asset_export_route_required")
	}
	f.error(f.request("directory-admin", "/api/tasks/submit", f.proof("directory-admin", map[string]any{"taskName": domains.GroupAssetExportV3KindName, "domainId": "domain-one", "payloadVersion": 1, "payload": map[string]any{}, "idempotencyKey": "no-generic-submit"}), 400), "group_asset_export_route_required")
	if f.detail("directory-admin", id).Task.State != tasks.Queued || f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='group_assets.v3.export'`) != 1 {
		t.Fatal("generic bypass mutated lifecycle")
	}
}
