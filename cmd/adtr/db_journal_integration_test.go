//go:build integration && adtr_ci_dbadmission

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/store"
)

// Real Recorder + Store: the final memory acknowledgement must not be confused
// with the intentionally incomplete report stored before the last Append ACK.
func TestActivityCandidateFinalJournalActualRecorderAndPostgres(t *testing.T) {
	dsn := os.Getenv("ADTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("PostgreSQL configuration required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("PostgreSQL configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("PostgreSQL unavailable")
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		admin.Close(ctx)
		t.Fatal("fixture identity")
	}
	name := "adtr_ci_journal_" + hex.EncodeToString(nonce[:])
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		admin.Close(ctx)
		t.Fatal("fixture database")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+quoted); e != nil {
			t.Error("owned database cleanup")
		}
		admin.Close(cleanup)
	}()
	config = config.Copy()
	config.Database = name
	if store.Migrate(ctx, config) != nil {
		t.Fatal("actual migration")
	}
	probe, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("probe connection")
	}
	defer probe.Close(context.Background())
	sink, err := operationallogs.NewStore(config, store.SchemaVersion)
	if err != nil {
		t.Fatal("actual journal store")
	}
	recorder, err := operationallogs.NewRecorder(sink, operationallogs.Worker)
	if err != nil {
		t.Fatal("actual recorder")
	}
	// Close claims/drains a not-yet-started recorder, making the three accepted
	// events and the last pre-ACK report fully deterministic.
	for _, event := range []struct {
		code    operationallogs.EventCode
		outcome operationallogs.Outcome
	}{
		{operationallogs.ServiceStartRequested, operationallogs.Attempted},
		{operationallogs.QueueProgress, operationallogs.Progress},
		{operationallogs.ServiceStopped, operationallogs.Completed},
	} {
		if !recorder.Emit(event.code, event.outcome, operationallogs.NoReason) {
			t.Fatal("event rejected")
		}
	}
	closeErr := recorder.Close(ctx)
	if closeErr != nil {
		t.Fatal("actual recorder drain")
	}
	proof := ciJournalProjection(recorder.Stats(), closeErr == nil)
	var pid string
	var reports, accepted, acknowledged, events, progress int64
	if err = probe.QueryRow(ctx, `SELECT count(*),min(process_id),min(accepted),min(acknowledged) FROM adtr.operational_log_reports WHERE module='worker'`).Scan(&reports, &pid, &accepted, &acknowledged); err != nil {
		t.Fatal("persisted report")
	}
	if err = probe.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE code='queue_progress') FROM adtr.operational_log_events WHERE module='worker' AND process_id=$1`, pid).Scan(&events, &progress); err != nil {
		t.Fatal("persisted events")
	}
	sum := sha256.Sum256([]byte(pid))
	if reports != 1 || accepted != 3 || acknowledged != 2 || events != 3 || progress != 1 || proof.ProcessIDSHA256 != hex.EncodeToString(sum[:]) || !proof.CloseOK || !proof.AcceptanceStopped || proof.Accepted != 3 || proof.Acknowledged != 3 || proof.Rejected != 0 || proof.QueueFull != 0 || proof.WriteFailures != 0 || proof.Unacknowledged != 0 || proof.Abandoned != 0 {
		t.Fatal("final and incomplete observations conflated")
	}
}
