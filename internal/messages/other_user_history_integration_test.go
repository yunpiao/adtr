//go:build integration

package messages

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/messagecore"
)

// AD-F158 / DTL036: real PostgreSQL component evidence with the existing
// synthetic authorizer. This is not evidence of two authenticated browsers.
type isolationReadRow struct {
	tenant  string
	user    int64
	message string
	first   time.Time
}

func isolationReads(f *fixture, id Identity) []isolationReadRow {
	f.t.Helper()
	rows, err := f.db.Query(f.ctx, `SELECT tenant_id,user_id,message_id,first_read_at FROM adtr.message_reads WHERE tenant_id=$1 AND user_id=$2 ORDER BY message_id COLLATE "C"`, id.TenantID, id.UserID)
	f.must(err)
	defer rows.Close()
	out := []isolationReadRow{}
	for rows.Next() {
		var row isolationReadRow
		f.must(rows.Scan(&row.tenant, &row.user, &row.message, &row.first))
		row.first = row.first.UTC()
		out = append(out, row)
	}
	f.must(rows.Err())
	return out
}

func isolationMessageIDs(f *fixture, id Identity) []string {
	f.t.Helper()
	rows, err := f.db.Query(f.ctx, `SELECT message_id FROM adtr.messages WHERE tenant_id=$1 AND recipient_user_id=$2 ORDER BY message_id COLLATE "C"`, id.TenantID, id.UserID)
	f.must(err)
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var value string
		f.must(rows.Scan(&value))
		out = append(out, value)
	}
	f.must(rows.Err())
	return out
}

func isolationTargets(f *fixture, id Identity, operation string) []string {
	f.t.Helper()
	rows, err := f.db.Query(f.ctx, `SELECT message_id FROM adtr.message_bulk_targets WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3 ORDER BY ordinal`, id.TenantID, id.UserID, operation)
	f.must(err)
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var value string
		f.must(rows.Scan(&value))
		out = append(out, value)
	}
	f.must(rows.Err())
	return out
}

func isolationSorted(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func TestBulkPreservesNonemptyOtherUserReadHistory(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		selection                string
		count, target, processed int
		cancel                   bool
	}{
		{"selected", "selected", 3, 2, 2, false},
		{"all", "all", 3, 3, 3, false},
		{"partial_cancel", "all", 51, 51, 50, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			current := testIdentity()
			other := current
			other.UserID++
			other.SessionHash = strings.Repeat("b", 64)
			for i := 0; i < tc.count; i++ {
				f.runHealth(current, fmt.Sprintf("isolation-current-%d", i))
			}
			for i := 0; i < 2; i++ {
				f.runHealth(other, fmt.Sprintf("isolation-other-%d", i))
			}
			f.project()
			now := time.Now().Add(time.Second)
			otherUnread := f.query(other, now, messagecore.ReadUnread)
			if otherUnread.Total != 2 || len(otherUnread.Items) != 2 || otherUnread.Counts != (Counts{All: 2, Unread: 2}) {
				t.Fatal("other identity requires two real unread health messages")
			}
			readID, unreadID := otherUnread.Items[0].ID, otherUnread.Items[1].ID
			var receipt ReadReceipt
			// f.tx commits before the independent history read below. No direct insert
			// into message_reads is used to create the nonempty isolation precondition.
			f.must(f.tx(func(tx pgx.Tx) (err error) {
				receipt, err = f.store.MarkReadTx(f.ctx, tx, other, otherUnread.ResultID, readID, "isolation-other-read", now)
				return err
			}))
			baseline := isolationReads(f, other)
			if len(baseline) != 1 || baseline[0].message != readID || baseline[0].tenant != other.TenantID || baseline[0].user != other.UserID || baseline[0].first.IsZero() || !baseline[0].first.Equal(receipt.FirstReadAt) || receipt.MessageID != readID || receipt.Progress != (Progress{Target: 1, Processed: 1, NewlyRead: 1}) {
				t.Fatal("other history must be nonempty and match committed MarkReadTx receipt")
			}
			otherView := f.query(other, now, messagecore.ReadAll)
			if otherView.Total != 2 || len(otherView.Items) != 2 || otherView.Counts != (Counts{All: 2, Read: 1, Unread: 1}) {
				t.Fatal("other identity requires one read and one unread message")
			}
			seenRead, seenUnread := false, false
			for _, entry := range otherView.Items {
				if entry.ID == readID {
					seenRead = entry.Read && entry.FirstReadAt != nil && entry.FirstReadAt.Equal(receipt.FirstReadAt)
				}
				if entry.ID == unreadID {
					seenUnread = !entry.Read && entry.FirstReadAt == nil
				}
			}
			if !seenRead || !seenUnread {
				t.Fatal("other query does not reflect the committed nonempty history")
			}
			assertOther := func(stage string) {
				t.Helper()
				if got := isolationReads(f, other); !reflect.DeepEqual(got, baseline) {
					t.Fatalf("%s: other complete read history or first_read_at changed", stage)
				}
				got := f.query(other, now, messagecore.ReadAll)
				if got.Total != otherView.Total || got.Counts != otherView.Counts || !reflect.DeepEqual(got.Items, otherView.Items) {
					t.Fatalf("%s: other entries, unread state or query counts changed", stage)
				}
			}
			originalIDs := isolationMessageIDs(f, current)
			snap := f.query(current, now, messagecore.ReadAll)
			if len(originalIDs) != tc.count || snap.Total != tc.count || snap.Counts != (Counts{All: tc.count, Unread: tc.count}) || len(isolationReads(f, current)) != 0 {
				t.Fatal("current identity requires real initially unread health messages")
			}
			in := BulkRequest{ResultID: snap.ResultID, OperationID: "isolation-bulk", Selection: tc.selection}
			expectedTargets := originalIDs
			if tc.selection == "selected" {
				if len(snap.Items) != 3 {
					t.Fatal("selected fixture page incomplete")
				}
				in.MessageIDs = []string{snap.Items[2].ID, snap.Items[0].ID}
				expectedTargets = isolationSorted(in.MessageIDs)
			}
			admitted := f.bulk(in, current, now)
			assertBulk(t, admitted, "queued", tc.target, 0, 0, 0)
			frozen := isolationTargets(f, current, in.OperationID)
			if len(frozen) != tc.target || !reflect.DeepEqual(isolationSorted(frozen), expectedTargets) {
				t.Fatal("frozen target set differs from exact requested current-user set")
			}
			assertOther("admitted")
			f.runHealth(current, "isolation-later-arrival")
			f.project()
			if len(isolationMessageIDs(f, current)) != tc.count+1 || !reflect.DeepEqual(isolationTargets(f, current, in.OperationID), frozen) {
				t.Fatal("new arrival changed frozen target set")
			}
			assertOther("later arrival")
			f.must(f.store.BulkOnce(f.ctx, bulkAllow))
			state := "completed"
			if tc.cancel {
				state = "running"
			}
			progressed := f.bulkGet(current, in.OperationID)
			assertBulk(t, progressed, state, tc.target, tc.processed, tc.processed, 0)
			if progressed.Receipt != admitted.Receipt {
				t.Fatal("execution changed frozen receipt")
			}
			currentHistory := isolationReads(f, current)
			gotIDs := []string{}
			for _, row := range currentHistory {
				if row.first.IsZero() || row.tenant != current.TenantID || row.user != current.UserID {
					t.Fatal("current actual read row invalid")
				}
				gotIDs = append(gotIDs, row.message)
			}
			if len(currentHistory) != tc.processed || !reflect.DeepEqual(gotIDs, isolationSorted(frozen[:tc.processed])) {
				t.Fatal("current exact processed set was not actually written")
			}
			var receipts int
			f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_bulk_receipts WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, current.TenantID, current.UserID, in.OperationID).Scan(&receipts))
			if receipts != tc.processed {
				t.Fatal("processed count does not match persisted bulk receipts")
			}
			assertOther("executed first batch")
			if tc.cancel {
				progressed = f.bulkCancel(current, in.OperationID)
				assertBulk(t, progressed, "cancelled", 51, 50, 50, 0)
				if progressed.Receipt != admitted.Receipt || !reflect.DeepEqual(isolationReads(f, current), currentHistory) {
					t.Fatal("cancel changed frozen receipt or already committed reads")
				}
				assertOther("cancelled")
			}
			// A completed/cancelled operation cannot process another target on a later
			// worker cycle; the cancelled case still has one frozen unprocessed target.
			f.must(f.store.BulkOnce(f.ctx, bulkAllow))
			if f.bulkGet(current, in.OperationID) != progressed || !reflect.DeepEqual(isolationTargets(f, current, in.OperationID), frozen) || !reflect.DeepEqual(isolationReads(f, current), currentHistory) {
				t.Fatal("terminal operation resumed or changed persistent history")
			}
			f.must(f.db.QueryRow(f.ctx, `SELECT count(*) FROM adtr.message_bulk_receipts WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, current.TenantID, current.UserID, in.OperationID).Scan(&receipts))
			if receipts != tc.processed {
				t.Fatal("terminal operation added or lost a persistent bulk receipt")
			}
			assertOther("terminal worker cycle")
			remainingOther := f.query(other, now, messagecore.ReadUnread)
			if remainingOther.Total != 1 || len(remainingOther.Items) != 1 || remainingOther.Items[0].ID != unreadID || remainingOther.Items[0].Read || remainingOther.Items[0].FirstReadAt != nil || remainingOther.Counts != otherView.Counts {
				t.Fatal("other original unread message or statistics changed")
			}
			currentView := f.query(current, time.Now().Add(time.Second), messagecore.ReadAll)
			if currentView.Counts != (Counts{All: tc.count + 1, Read: tc.processed, Unread: tc.count + 1 - tc.processed}) {
				t.Fatal("current query counts do not match actual writes and excluded arrival")
			}
		})
	}
}
