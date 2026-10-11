//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/messages"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

type lostMessageResponse struct {
	header http.Header
	status int
}

func (w *lostMessageResponse) Header() http.Header       { return w.header }
func (w *lostMessageResponse) WriteHeader(status int)    { w.status = status }
func (w *lostMessageResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type messageFixture struct {
	*taskFixture
	inbox *messages.Store
}

func newMessageFixture(t *testing.T) *messageFixture {
	f := newTaskFixture(t)
	f.exec(messages.Schema)
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current)
	e, err := tasks.New(f.s.database, tasks.ProductionRegistry(), taskAuthorizer(f.s.now), schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = e
	inbox, err := messages.New(f.s.database, schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	return &messageFixture{f, inbox}
}
func (f *messageFixture) message(c *resourceTestClient, path string, body map[string]any, want int) []byte {
	f.t.Helper()
	method := "GET"
	var raw []byte
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/messages"+path, strings.NewReader(string(raw)))
	r.Header.Set("Origin", f.s.origin)
	r.Header.Set("Content-Type", "application/json")
	if c != nil {
		r.AddCookie(c.cookie)
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	f.s.MessagesHandler(f.inbox).ServeHTTP(w, r)
	if w.Code != want {
		f.t.Fatalf("messages %s got %d want %d: %s", path, w.Code, want, w.Body.String())
	}
	return append([]byte(nil), w.Body.Bytes()...)
}
func (f *messageFixture) query(c *resourceTestClient, read string) messages.Snapshot {
	f.t.Helper()
	var out messages.Snapshot
	raw := f.message(c, "/query", map[string]any{"readFilter": read, "sort": "asc", "offset": 0, "limit": 200, "start": f.now.Add(-24 * time.Hour), "end": f.now.Add(24 * time.Hour)}, 200)
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *messageFixture) realHealth(c *resourceTestClient, key string) string {
	f.t.Helper()
	out := f.callTask(c, "/submit", f.proof(taskSubmitBody(key)), 200)
	id := taskOutputID(f.t, out)
	worker, err := tasks.NewWorker(f.engine, tasks.WorkerConfig{Owner: "message-health-worker", Concurrency: 1, PollInterval: 10 * time.Millisecond})
	if err != nil {
		f.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(8 * time.Second)
	terminal := false
	for time.Now().Before(deadline) {
		state, _, _ := f.taskState(id)
		if state == "succeeded" {
			terminal = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		f.t.Fatal(err)
	}
	if !terminal {
		f.t.Fatal("real production health worker did not succeed")
	}
	if err := f.inbox.ProjectOnce(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func TestMessagesRealHealthWorkerSnapshotAndDurableRead(t *testing.T) {
	f := newMessageFixture(t)
	operator := f.client(f.operator)
	task := f.realHealth(operator, "real-health")
	// Current read-only grant suffices; no MFA remains on this actor.
	f.exec("UPDATE adtr.access_permissions SET writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	f.exec("UPDATE adtr.users SET mfa_secret=NULL WHERE id=$1", f.operator)
	snap := f.query(operator, "unread")
	if len(snap.Items) != 1 || snap.Total != 1 || snap.Counts.Unread != 1 || snap.Items[0].TaskID != task || snap.Items[0].SubType != "succeeded" || snap.Items[0].Read {
		t.Fatal(snap)
	}
	if snap.Items[0].ScopeKind != "platform" || snap.Items[0].DomainID != "" || snap.Items[0].OccurredAt.IsZero() || snap.Items[0].IngestedAt.IsZero() {
		t.Fatal("invalid projection", snap)
	}
	body := map[string]any{"resultId": snap.ResultID, "messageId": snap.Items[0].ID, "operationId": "read-health-1"}
	// Lose the response after a real successful database commit. Recovery must
	// obtain the first receipt rather than issue a new operation or change counts.
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "/api/messages/read", strings.NewReader(string(encoded)))
	request.Header.Set("Origin", f.s.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", operator.csrf)
	request.AddCookie(operator.cookie)
	lost := &lostMessageResponse{header: make(http.Header)}
	f.s.MessagesHandler(f.inbox).ServeHTTP(lost, request)
	if lost.status != 200 {
		t.Fatal("write failure happened before commit", lost.status)
	}
	first := f.message(operator, "/read-operation?operationId=read-health-1", nil, 200)
	var receipt messages.ReadReceipt
	if json.Unmarshal(first, &receipt) != nil || receipt.Progress.NewlyRead != 1 || receipt.Progress.Processed != 1 || receipt.FirstReadAt.IsZero() {
		t.Fatal(string(first))
	}
	f.now = f.now.Add(time.Second)
	again := f.message(operator, "/read", body, 200)
	if string(first) != string(again) {
		t.Fatal("same operation changed its original receipt", string(first), string(again))
	}
	if got := f.message(operator, "/read-operation?operationId=read-health-1", nil, 200); string(got) != string(first) {
		t.Fatal("recovery changed receipt")
	}
	var page messages.Snapshot
	raw := f.message(operator, "/page?resultId="+snap.ResultID+"&offset=0&limit=200", nil, 200)
	if json.Unmarshal(raw, &page) != nil || !reflect.DeepEqual(snap, page) {
		t.Fatal("read mutated frozen result", string(raw))
	}
	var loc messages.Location
	raw = f.message(operator, "/locate?resultId="+snap.ResultID+"&messageId="+snap.Items[0].ID+"&limit=20", nil, 200)
	if json.Unmarshal(raw, &loc) != nil || !loc.Found || loc.Index != 0 || loc.Page != 1 {
		t.Fatal(string(raw))
	}
	fresh := f.query(operator, "unread")
	if fresh.Total != 0 || fresh.Counts.Read != 1 || fresh.Counts.All != 1 {
		t.Fatal(fresh)
	}
	all := f.query(operator, "all")
	body["resultId"], body["operationId"] = all.ResultID, "read-health-2"
	var second messages.ReadReceipt
	raw = f.message(operator, "/read", body, 200)
	if json.Unmarshal(raw, &second) != nil || second.Progress.AlreadyRead != 1 || !second.FirstReadAt.Equal(receipt.FirstReadAt) {
		t.Fatal(string(raw))
	}
	body["operationId"] = "read-health-1"
	f.message(operator, "/read", body, 409)
	// Archived tasks retain messages. No task payload is used by the projection.
	f.exec("INSERT INTO adtr.task_visibility(task_id,tenant_id,archived,visibility_version,archived_at,archived_by,reason) VALUES($1,'one',true,1,$2,$3,'synthetic archive')", task, f.now, f.admin)
	if got := f.query(operator, "all"); got.Total != 1 {
		t.Fatal("archive hid durable message")
	}
}
func TestMessagesLivePermissionIdentitySessionAndReceiptRecovery(t *testing.T) {
	f := newMessageFixture(t)
	operator := f.client(f.operator)
	f.realHealth(operator, "identity-health")
	snap := f.query(operator, "all")
	body := map[string]any{"resultId": snap.ResultID, "messageId": snap.Items[0].ID, "operationId": "receipt-recovery"}
	receipt := f.message(operator, "/read", body, 200)
	for _, other := range []*resourceTestClient{f.client(f.admin), f.client(f.other)} {
		if s := f.query(other, "all"); s.Total != 0 {
			t.Fatal("private recipient leaked")
		}
		f.message(other, "/page?resultId="+snap.ResultID+"&offset=0&limit=20", nil, 404)
		f.message(other, "/read-operation?operationId=receipt-recovery", nil, 404)
		f.message(other, "/read", body, 404)
	}
	f.message(nil, "/read-operation?operationId=receipt-recovery", nil, 401)
	invalid := *operator
	invalid.csrf = "wrong"
	f.message(&invalid, "/read", body, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=false,writeable=false WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	f.message(operator, "/read-operation?operationId=receipt-recovery", nil, 403)
	f.message(operator, "/page?resultId="+snap.ResultID+"&offset=0&limit=20", nil, 403)
	f.exec("UPDATE adtr.access_permissions SET readable=true WHERE tenant_id='one' AND role_id=$1 AND mark='tasks'", narrowResourceRole)
	f.message(operator, "/page?resultId="+snap.ResultID+"&offset=0&limit=20", nil, 410)
	// Original committed outcome remains queryable under current authority.
	if got := f.message(operator, "/read", body, 200); string(got) != string(receipt) {
		t.Fatal("epoch change lost committed receipt")
	}
	fresh := f.query(operator, "all")
	newSession := f.client(f.operator)
	f.message(newSession, "/page?resultId="+fresh.ResultID+"&offset=0&limit=20", nil, 410)
	if got := f.message(newSession, "/read", body, 200); string(got) != string(receipt) {
		t.Fatal("new session lost receipt")
	}
	f.exec("DELETE FROM adtr.sessions WHERE token_hash=$1", digest(operator.cookie.Value))
	f.message(operator, "/read", body, 401)
	// Simulate derived-snapshot cleanup; receipt deliberately has no result FK.
	f.exec("DELETE FROM adtr.message_results WHERE result_id=$1", snap.ResultID)
	if got := f.message(newSession, "/read-operation?operationId=receipt-recovery", nil, 200); string(got) != string(receipt) {
		t.Fatal("snapshot cleanup lost receipt")
	}
	f.now = f.now.Add(11 * time.Minute)
	f.message(newSession, "/page?resultId="+fresh.ResultID+"&offset=0&limit=20", nil, 410)
	if got := f.message(newSession, "/read", body, 200); string(got) != string(receipt) {
		t.Fatal("expiry lost committed result")
	}
}
func TestMessagesSchemaGateAndQueryBudgets(t *testing.T) {
	f := newMessageFixture(t)
	operator := f.client(f.operator)
	for _, read := range []string{"", "unsupported"} {
		f.message(operator, "/query", map[string]any{"readFilter": read, "sort": "asc", "offset": 0, "limit": 20}, 400)
	}
	for _, limit := range []int{-1, 0, 201} {
		f.message(operator, "/query", map[string]any{"readFilter": "all", "sort": "asc", "offset": 0, "limit": limit}, 400)
	}
	for i := 0; i < messages.MaxUserSnapshots; i++ {
		if s := f.query(operator, "all"); s.Total != 0 {
			t.Fatal(s)
		}
	}
	f.message(operator, "/query", map[string]any{"readFilter": "all", "sort": "asc", "offset": 0, "limit": 20}, 429)
	f.now = f.now.Add(messages.SnapshotTTL + time.Second)
	f.query(operator, "all")
	f.exec("UPDATE adtr.schema_version SET version=$1", schemaversion.Current+1)
	f.message(operator, "/read-operation?operationId=unknown", nil, 503)
	if err := f.inbox.ProjectOnce(f.ctx); err == nil {
		t.Fatal("incompatible projector accepted schema")
	}
}

// Run is the same projector loop joined by cmd/adtr's worker process. A
// nontransactional sequence proves one database failure really happened before
// automatic retry produced the API-visible message; no manual ProjectOnce call.
func TestMessagesProjectorRunRecoversAfterDatabaseFailure(t *testing.T) {
	f := newMessageFixture(t)
	operator := f.client(f.operator)
	task := taskOutputID(t, f.callTask(operator, "/submit", f.proof(taskSubmitBody("run-retry")), 200))
	if worked, err := f.engine.RunOne(f.ctx, "message-run-producer"); err != nil || !worked {
		t.Fatal("real health execution", worked, err)
	}
	f.exec(`CREATE SEQUENCE adtr.synthetic_message_failures;
 CREATE FUNCTION adtr.synthetic_fail_first_message() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF nextval('adtr.synthetic_message_failures')=1 THEN RAISE EXCEPTION 'synthetic transient failure'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER synthetic_first_message_failure BEFORE INSERT ON adtr.messages FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_fail_first_message();`)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.inbox.Run(ctx) }()
	observed := false
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.messages WHERE source_task_id=$1", task).Scan(&count); err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if count == 1 {
			observed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("projector did not recover failed cycle")
	}
	var attempts int
	if err := f.conn.QueryRow(f.ctx, "SELECT last_value FROM adtr.synthetic_message_failures").Scan(&attempts); err != nil || attempts < 2 {
		t.Fatal("did not demonstrate first failure and retry", attempts, err)
	}
	snap := f.query(operator, "all")
	if snap.Total != 1 || len(snap.Items) != 1 || snap.Items[0].TaskID != task {
		t.Fatal("recovered result not API visible", snap)
	}
}
