//go:build integration

package messages

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

func bulkAllow(_ context.Context, _ pgx.Tx, _ tasks.Principal, _ tasks.Scope, _ tasks.Action) (string, error) {
	return "1", nil
}
func (f *fixture) bulk(in BulkRequest, id Identity, now time.Time) BulkOperation {
	f.t.Helper()
	var out BulkOperation
	f.must(f.tx(func(tx pgx.Tx) (err error) { out, err = f.store.SubmitBulkTx(f.ctx, tx, id, in, now); return err }))
	return out
}
func (f *fixture) bulkGet(id Identity, key string) BulkOperation {
	f.t.Helper()
	var out BulkOperation
	f.must(f.tx(func(tx pgx.Tx) (err error) { out, err = f.store.BulkOperationTx(f.ctx, tx, id, key); return err }))
	return out
}
func (f *fixture) bulkCancel(id Identity, key string) BulkOperation {
	f.t.Helper()
	var out BulkOperation
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		out, err = f.store.CancelBulkTx(f.ctx, tx, id, key, time.Now())
		return err
	}))
	return out
}
func assertBulk(t *testing.T, op BulkOperation, state string, target, processed, newly, already int) {
	t.Helper()
	want := Progress{Target: target, Processed: processed, NewlyRead: newly, AlreadyRead: already, Remaining: target - processed}
	if op.State != state || op.Progress != want || op.Receipt.Target != target {
		t.Fatalf("bulk=%+v want %s %+v", op, state, want)
	}
}
func TestBulkRealHealthCrossPageFrozenProgressRestartAndReceipts(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	for i := 0; i < 103; i++ {
		f.runHealth(id, fmt.Sprintf("bulk-real-health-%d", i))
	}
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	if snap.Total != 103 || len(snap.Items) != 20 {
		t.Fatal("expected actual cross-page health source", snap.Total)
	}
	var single ReadReceipt
	f.must(f.tx(func(tx pgx.Tx) (err error) {
		single, err = f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "single-before-bulk", now)
		return err
	}))
	in := BulkRequest{ResultID: snap.ResultID, OperationID: "all-health", Selection: "all"}
	original := f.bulk(in, id, now)
	assertBulk(t, original, "queued", 103, 0, 0, 0)
	// New health arrivals do not enter an already accepted fixed result.
	f.runHealth(id, "later-health")
	f.project()
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	first := f.bulkGet(id, in.OperationID)
	assertBulk(t, first, "running", 103, 50, 49, 1)
	_, err := f.db.Exec(f.ctx, `DELETE FROM adtr.message_results WHERE result_id=$1`, snap.ResultID)
	f.must(err)
	// Reconstruct a fresh Store with no process state, then resume original targets.
	f.store, err = New(f.cfg, schemaversion.Current)
	f.must(err)
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	assertBulk(t, f.bulkGet(id, in.OperationID), "running", 103, 100, 99, 1)
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	done := f.bulkGet(id, in.OperationID)
	assertBulk(t, done, "completed", 103, 103, 102, 1)
	if done.Receipt != original.Receipt {
		t.Fatal("original admission changed")
	}
	renewed := id
	renewed.SessionHash = strings.Repeat("b", 64)
	renewed.AuthorizationVersion = 2
	if replay := f.bulk(in, renewed, now.Add(time.Hour)); replay != done {
		t.Fatal("new session/expired deleted snapshot lost original result")
	}
	if f.bulkCancel(renewed, in.OperationID) != done {
		t.Fatal("cancel changed completed result")
	}
	var readCount, receiptCount int
	var firstRead time.Time
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_reads`).Scan(&readCount))
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_bulk_receipts`).Scan(&receiptCount))
	f.must(f.db.QueryRow(f.ctx, `SELECT first_read_at FROM adtr.message_reads WHERE message_id=$1`, snap.Items[0].ID).Scan(&firstRead))
	if readCount != 103 || receiptCount != 103 || !firstRead.Equal(single.FirstReadAt) {
		t.Fatal("first time/receipt/frozen targets changed", readCount, receiptCount)
	}
	fresh := f.query(renewed, now.Add(2*time.Second), messagecore.ReadUnread)
	if fresh.Total != 1 {
		t.Fatal("later arrival incorrectly marked", fresh.Total)
	}
	for _, other := range []Identity{{TenantID: id.TenantID, UserID: 42, SessionHash: id.SessionHash, AuthorizationVersion: 1}, {TenantID: "another", UserID: id.UserID, SessionHash: id.SessionHash, AuthorizationVersion: 1}} {
		err = f.tx(func(tx pgx.Tx) error { _, err := f.store.BulkOperationTx(f.ctx, tx, other, in.OperationID); return err })
		requireError(t, err, 404, "not_found")
	}
}
func TestBulkSelectedValidationConcurrentReplayCancelAndQuota(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	for i := 0; i < 3; i++ {
		f.runHealth(id, fmt.Sprint(i))
	}
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	in := BulkRequest{ResultID: snap.ResultID, OperationID: "selected", Selection: "selected", MessageIDs: []string{snap.Items[2].ID, snap.Items[0].ID}}
	var wg sync.WaitGroup
	out := make(chan BulkOperation, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var op BulkOperation
			err := f.tx(func(tx pgx.Tx) (err error) { op, err = f.store.SubmitBulkTx(f.ctx, tx, id, in, now); return err })
			if err != nil {
				t.Error(err)
			} else {
				out <- op
			}
		}()
	}
	wg.Wait()
	close(out)
	first := <-out
	for op := range out {
		if !reflect.DeepEqual(op, first) {
			t.Error("concurrent replay differs")
		}
	}
	reversed := in
	reversed.MessageIDs = []string{in.MessageIDs[1], in.MessageIDs[0]}
	if f.bulk(reversed, id, now) != first {
		t.Fatal("reordering changed canonical operation")
	}
	changed := in
	changed.MessageIDs = []string{snap.Items[1].ID}
	err := f.tx(func(tx pgx.Tx) error { _, err := f.store.SubmitBulkTx(f.ctx, tx, id, changed, now); return err })
	requireError(t, err, 409, "operation_conflict")
	for _, ids := range [][]string{{snap.Items[0].ID, "outside"}, {snap.Items[0].ID, snap.Items[0].ID}, {}} {
		invalid := in
		invalid.OperationID = "invalid"
		invalid.MessageIDs = ids
		err = f.tx(func(tx pgx.Tx) error { _, err := f.store.SubmitBulkTx(f.ctx, tx, id, invalid, now); return err })
		var e *Error
		if !errors.As(err, &e) || e.Status != 400 {
			t.Fatal(err)
		}
	}
	for i := 1; i < MaxUserBulk; i++ {
		other := in
		other.OperationID = fmt.Sprintf("quota-%d", i)
		f.bulk(other, id, now)
	}
	full := in
	full.OperationID = "quota-denied"
	err = f.tx(func(tx pgx.Tx) error { _, err := f.store.SubmitBulkTx(f.ctx, tx, id, full, now); return err })
	requireError(t, err, 429, "bulk_quota_exceeded")
	cancelled := f.bulkCancel(id, in.OperationID)
	assertBulk(t, cancelled, "cancelled", 2, 0, 0, 0)
	if f.bulkCancel(id, in.OperationID) != cancelled {
		t.Fatal("cancel replay changed receipt")
	}
	f.bulk(full, id, now)
	for range 6 {
		f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	}
	if f.bulkGet(id, in.OperationID) != cancelled {
		t.Fatal("cancelled operation resumed")
	}
	var n int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_bulk_receipts WHERE operation_id='selected'`).Scan(&n))
	if n != 0 {
		t.Fatal("cancelled targets processed")
	}
	// The unselected item is still unread; completed selected operations did not widen.
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_reads WHERE message_id=$1`, snap.Items[1].ID).Scan(&n))
	if n != 0 {
		t.Fatal("selected widened")
	}
}
func TestBulkFailureRollsBackBatchAndPersistentRoundAvoidsPoison(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "one")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	for _, key := range []string{"poison", "healthy"} {
		f.bulk(BulkRequest{ResultID: snap.ResultID, OperationID: key, Selection: "all"}, id, now)
	}
	_, err := f.db.Exec(f.ctx, `CREATE FUNCTION adtr.synthetic_bulk_poison() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='poison' THEN RAISE EXCEPTION 'synthetic receipt fault'; END IF; RETURN NEW; END; $$; CREATE TRIGGER synthetic_bulk_poison BEFORE INSERT ON adtr.message_bulk_receipts FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_bulk_poison();`)
	f.must(err)
	if f.store.BulkOnce(f.ctx, bulkAllow) == nil {
		t.Fatal("expected actual SQL rollback")
	}
	assertBulk(t, f.bulkGet(id, "poison"), "queued", 1, 0, 0, 0)
	var reads int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_reads`).Scan(&reads))
	if reads != 0 {
		t.Fatal("read escaped failed receipt transaction")
	}
	// Higher IDs arriving cannot extend the frozen scheduling round.
	f.bulk(BulkRequest{ResultID: snap.ResultID, OperationID: "new-arrival", Selection: "all"}, id, now)
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	assertBulk(t, f.bulkGet(id, "healthy"), "completed", 1, 1, 1, 0)
	_, err = f.db.Exec(f.ctx, `DROP TRIGGER synthetic_bulk_poison ON adtr.message_bulk_receipts`)
	f.must(err)
	// Round ends at the original upper; next finite round revisits low poison ID.
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	assertBulk(t, f.bulkGet(id, "poison"), "completed", 1, 1, 0, 1)
}
func TestBulkCancellationRacesAndRevocationRetainPartialReads(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	for i := 0; i < 51; i++ {
		f.runHealth(id, fmt.Sprintf("race-%d", i))
	}
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	f.bulk(BulkRequest{ResultID: snap.ResultID, OperationID: "cancel-race", Selection: "all"}, id, now)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := f.store.BulkOnce(f.ctx, bulkAllow); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		err := f.tx(func(tx pgx.Tx) error {
			_, err := f.store.CancelBulkTx(f.ctx, tx, id, "cancel-race", time.Now())
			return err
		})
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	op := f.bulkGet(id, "cancel-race")
	if op.State != "cancelled" || (op.Progress.Processed != 0 && op.Progress.Processed != 50) || op.Progress.Target != op.Progress.Processed+op.Progress.Remaining {
		t.Fatal(op)
	}
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	if f.bulkGet(id, "cancel-race") != op {
		t.Fatal("cancel resumed")
	}
	f.bulk(BulkRequest{ResultID: snap.ResultID, OperationID: "revoke", Selection: "all"}, id, now)
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	before := f.bulkGet(id, "revoke")
	if before.Progress.Processed != 50 {
		t.Fatal(before)
	}
	epochChanged := func(context.Context, pgx.Tx, tasks.Principal, tasks.Scope, tasks.Action) (string, error) {
		return "2", nil
	}
	f.must(f.store.BulkOnce(f.ctx, epochChanged))
	blocked := f.bulkGet(id, "revoke")
	if blocked.State != "authorization_blocked" || blocked.Progress != before.Progress {
		t.Fatal(blocked, before)
	}
	f.must(f.store.BulkOnce(f.ctx, bulkAllow))
	if f.bulkGet(id, "revoke") != blocked {
		t.Fatal("restored grant resumed terminal operation")
	}
}

func TestBulkTerminatedDatabaseConnectionRollsBackAndLoopRestartRecovers(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	for i := 0; i < 3; i++ {
		f.runHealth(id, fmt.Sprintf("terminate-%d", i))
	}
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	in := BulkRequest{ResultID: snap.ResultID, OperationID: "terminate", Selection: "all"}
	accepted := f.bulk(in, id, now)
	_, err := f.db.Exec(f.ctx, `CREATE FUNCTION adtr.synthetic_bulk_pause() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.ordinal=1 THEN PERFORM pg_sleep(20); END IF; RETURN NEW; END; $$; CREATE TRIGGER synthetic_bulk_pause BEFORE INSERT ON adtr.message_bulk_receipts FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_bulk_pause();`)
	f.must(err)
	tagged := func(ctx context.Context, tx pgx.Tx, p tasks.Principal, s tasks.Scope, a tasks.Action) (string, error) {
		_, err := tx.Exec(ctx, `SET LOCAL application_name='adtr-bulk-crash-fixture'`)
		return "1", err
	}
	done := make(chan error, 1)
	go func() { done <- f.store.BulkOnce(f.ctx, tagged) }()
	var pid int32
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err = f.db.QueryRow(f.ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND application_name='adtr-bulk-crash-fixture' AND wait_event='PgSleep'`).Scan(&pid)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("did not observe real partially written sleeping transaction")
	}
	var killed bool
	f.must(f.db.QueryRow(f.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed))
	if !killed {
		t.Fatal("database connection was not terminated")
	}
	if err = <-done; err == nil {
		t.Fatal("terminated batch falsely succeeded")
	}
	if got := f.bulkGet(id, in.OperationID); got != accepted {
		t.Fatal("partial batch escaped connection loss", got)
	}
	var count int
	f.must(f.db.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM adtr.message_reads)+(SELECT count(*) FROM adtr.message_bulk_receipts)`).Scan(&count))
	if count != 0 {
		t.Fatal("uncommitted history survived", count)
	}
	_, err = f.db.Exec(f.ctx, `DROP TRIGGER synthetic_bulk_pause ON adtr.message_bulk_receipts`)
	f.must(err)
	f.store, err = New(f.cfg, schemaversion.Current)
	f.must(err)
	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	go func() { done <- f.store.RunBulk(ctx, bulkAllow) }()
	deadline = time.Now().Add(3 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		if got := f.bulkGet(id, in.OperationID); got.State == "completed" {
			assertBulk(t, got, "completed", 3, 3, 3, 0)
			completed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	f.must(<-done)
	if !completed {
		t.Fatal("new worker loop did not recover original durable operation")
	}
}
func TestBulkTenantAndGlobalAdmissionBudgetsAreConcurrent(t *testing.T) {
	for _, scope := range []string{"tenant", "global"} {
		t.Run(scope, func(t *testing.T) {
			f := newFixture(t)
			id := testIdentity()
			other := id
			other.UserID++
			f.runHealth(id, "first-owner")
			f.runHealth(other, "second-owner")
			f.project()
			now := time.Now().Add(time.Second)
			snaps := []Snapshot{f.query(id, now, messagecore.ReadAll), f.query(other, now, messagecore.ReadAll)}
			// Deliberately synthetic active rows exercise admission storage bounds only.
			// No fake source is used for either real submission below.
			n := MaxTenantBulk - 1
			tenant := id.TenantID
			if scope == "global" {
				n = MaxGlobalBulk - 1
				tenant = "synthetic-quota-only"
			}
			_, err := f.db.Exec(f.ctx, `INSERT INTO adtr.message_bulk_operations(tenant_id,user_id,operation_id,result_id,selection,request_hash,authorization_version,accepted_at,updated_at,state,target) SELECT $1,900+g,'quota-'||g,'synthetic-result','all',repeat('a',64),1,clock_timestamp(),clock_timestamp(),'queued',1 FROM generate_series(1,$2::integer) g`, tenant, n)
			f.must(err)
			errorsCh := make(chan error, 2)
			var wg sync.WaitGroup
			for i, actor := range []Identity{id, other} {
				wg.Add(1)
				go func(i int, actor Identity) {
					defer wg.Done()
					errorsCh <- f.tx(func(tx pgx.Tx) error {
						_, err := f.store.SubmitBulkTx(f.ctx, tx, actor, BulkRequest{ResultID: snaps[i].ResultID, OperationID: "last-slot", Selection: "all"}, now)
						return err
					})
				}(i, actor)
			}
			wg.Wait()
			close(errorsCh)
			success, denied := 0, 0
			for err := range errorsCh {
				if err == nil {
					success++
				} else {
					requireError(t, err, 429, "bulk_quota_exceeded")
					denied++
				}
			}
			if success != 1 || denied != 1 {
				t.Fatal("quota race", success, denied)
			}
		})
	}
}
func TestBulkTwoWorkersSingleReadRaceAndImmutableHistory(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	f.runHealth(id, "multi-worker")
	f.project()
	now := time.Now().Add(time.Second)
	snap := f.query(id, now, messagecore.ReadAll)
	for _, key := range []string{"worker-a", "worker-b"} {
		f.bulk(BulkRequest{ResultID: snap.ResultID, OperationID: key, Selection: "all"}, id, now)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.store.BulkOnce(f.ctx, bulkAllow); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := f.tx(func(tx pgx.Tx) error {
			_, err := f.store.MarkReadTx(f.ctx, tx, id, snap.ResultID, snap.Items[0].ID, "single-race", now)
			return err
		})
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	for _, key := range []string{"worker-a", "worker-b"} {
		op := f.bulkGet(id, key)
		if op.State != "completed" || op.Progress.Processed != 1 || op.Progress.NewlyRead+op.Progress.AlreadyRead != 1 {
			t.Fatal(op)
		}
	}
	var totalFirst int
	f.must(f.db.QueryRow(f.ctx, `SELECT count(DISTINCT first_read_at) FROM (SELECT first_read_at FROM adtr.message_reads UNION ALL SELECT first_read_at FROM adtr.message_read_operations UNION ALL SELECT first_read_at FROM adtr.message_bulk_receipts) r`).Scan(&totalFirst))
	if totalFirst != 1 {
		t.Fatal("first time diverged", totalFirst)
	}
	for _, sql := range []string{`UPDATE adtr.message_bulk_operations SET result_id='different'`, `UPDATE adtr.message_bulk_operations SET state='queued'`, `UPDATE adtr.message_bulk_receipts SET newly_read=false`, `UPDATE adtr.message_bulk_targets SET ordinal=1`, `DELETE FROM adtr.message_bulk_operations`, `TRUNCATE adtr.message_bulk_receipts`} {
		if _, err := f.db.Exec(f.ctx, sql); err == nil {
			t.Fatal("history mutation accepted", sql)
		}
	}
}

func TestBulkEmptyAndUnreadFilteredMembershipAndAutomaticRetry(t *testing.T) {
	f := newFixture(t)
	id := testIdentity()
	now := time.Now().Add(time.Second)
	empty := f.query(id, now, messagecore.ReadAll)
	assertBulk(t, f.bulk(BulkRequest{ResultID: empty.ResultID, OperationID: "empty", Selection: "all"}, id, now), "completed", 0, 0, 0, 0)
	for i := 0; i < 2; i++ {
		f.runHealth(id, fmt.Sprintf("filter-%d", i))
	}
	f.project()
	now = time.Now().Add(time.Second)
	all := f.query(id, now, messagecore.ReadAll)
	f.must(f.tx(func(tx pgx.Tx) error {
		_, err := f.store.MarkReadTx(f.ctx, tx, id, all.ResultID, all.Items[0].ID, "known-before-filter", now)
		return err
	}))
	unread := f.query(id, now, messagecore.ReadUnread)
	if unread.Total != 1 || unread.Counts.All != 2 {
		t.Fatal(unread)
	}
	f.bulk(BulkRequest{ResultID: unread.ResultID, OperationID: "retry-unread", Selection: "all"}, id, now)
	_, err := f.db.Exec(f.ctx, `CREATE SEQUENCE adtr.synthetic_bulk_retry_count; CREATE FUNCTION adtr.synthetic_bulk_retry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('adtr.synthetic_bulk_retry_count')=1 THEN RAISE EXCEPTION 'synthetic transient receipt failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER synthetic_bulk_retry BEFORE INSERT ON adtr.message_bulk_receipts FOR EACH ROW EXECUTE FUNCTION adtr.synthetic_bulk_retry();`)
	f.must(err)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.store.RunBulk(ctx, bulkAllow) }()
	deadline := time.Now().Add(4 * time.Second)
	var op BulkOperation
	for time.Now().Before(deadline) {
		op = f.bulkGet(id, "retry-unread")
		if op.State == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	f.must(<-done)
	assertBulk(t, op, "completed", 1, 1, 1, 0)
	var attempts int
	f.must(f.db.QueryRow(f.ctx, `SELECT last_value FROM adtr.synthetic_bulk_retry_count`).Scan(&attempts))
	if attempts != 2 {
		t.Fatal("automatic loop did not retry real SQL failure", attempts)
	}
	var target string
	f.must(f.db.QueryRow(f.ctx, `SELECT message_id FROM adtr.message_bulk_targets WHERE operation_id='retry-unread'`).Scan(&target))
	if target != unread.Items[0].ID {
		t.Fatal("all used base counts rather than filtered membership")
	}
}
