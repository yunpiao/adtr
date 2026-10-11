//go:build integration

package systemhealth

import (
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestContainerMemoryPersistHistoryRestartLegacyAndCorruption(t *testing.T) {
	f := newHealthFixture(t)
	var now time.Time
	if err := f.db.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	source, sampler := newFixture(t)
	for i := 0; i < 7; i++ {
		source.now = now.Add(time.Duration(-180+i*15) * time.Second).Add(123 * time.Nanosecond)
		snapshot := sampler.Sample()
		if i == 0 {
			snapshot.ContainerMemory = nil
		} else {
			m := sampleFixture(cgroupFixture())
			m.ObservedAt = snapshot.ObservedAt
			snapshot.ContainerMemory = &m
			if i >= 4 {
				m.SourceID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			}
			if i >= 5 {
				m.LimitBytes = decimal(160)
				p := 37.5
				m.Percent = &p
			}
			if i == 6 {
				m.SourceID = ""
			}
			if i == 2 {
				m.Percent = nil
				m.LimitBytes = nil
				m.Availability = Unavailable
				m.Reason = "unlimited"
				m.LimitState = "unlimited"
			}
		}
		if err := f.store.Persist(f.ctx, snapshot); err != nil {
			t.Fatal(err)
		}
		var stored time.Time
		var rawTime string
		if err := f.db.QueryRow(f.ctx, `SELECT observed_at,snapshot->>'observedAt' FROM adtr.system_resource_samples WHERE observed_at=$1`, snapshot.ObservedAt).Scan(&stored, &rawTime); err != nil {
			t.Fatal(err)
		}
		raw, err := time.Parse(time.RFC3339Nano, rawTime)
		if err != nil || !raw.Equal(snapshot.ObservedAt) || !stored.Equal(snapshot.ObservedAt.Truncate(time.Microsecond)) || raw.Equal(stored) {
			t.Fatal("actual PostgreSQL microsecond normalization or lossless JSON timestamp incorrect")
		}

	}
	restarted, err := NewStore(f.cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	in := HistoryInput{Instance: LocalInstanceID, GraphType: "container_ram", StartTime: now.Add(-4 * time.Minute).Unix(), EndTime: now.Unix()}
	if err = f.transact(func(tx pgx.Tx) error {
		out, e := restarted.HistoryTx(f.ctx, tx, OwnerTenantID, in)
		if e != nil {
			return e
		}
		if len(out.Info[0].Data.Value) != 5 || out.Info[0].Data.Value[0] != "75" || out.Info[0].Data.Value[1] != "75" || len(out.Gaps) == 0 || out.AvailableSince == nil || len(out.Segments) != 4 || out.Segments[1].SourceID == out.Segments[0].SourceID || out.Segments[2].LimitBytes != "160" || out.Segments[2].SourceID != out.Segments[1].SourceID || out.Segments[3].SourceID != "" {
			t.Fatalf("persisted observations incorrect %+v", out)
		}
		current, e := restarted.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		if e != nil {
			return e
		}
		if !current.Stale || current.Availability == Available || current.Snapshot.ContainerMemory.Percent == nil {
			t.Fatal("stale state lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requireCode(t, f.transact(func(tx pgx.Tx) error { _, e := restarted.HistoryTx(f.ctx, tx, "other-tenant", in); return e }), 403, "forbidden")
	f.exec(`UPDATE adtr.system_resource_samples SET snapshot=jsonb_set(snapshot,'{containerMemory,percent}','0') WHERE snapshot->'containerMemory'->>'availability'='available'`)
	requireCode(t, f.transact(func(tx pgx.Tx) error { _, e := restarted.HistoryTx(f.ctx, tx, OwnerTenantID, in); return e }), 503, "invalid_stored_observation")
	requireCode(t, f.transact(func(tx pgx.Tx) error {
		_, e := restarted.CurrentTx(f.ctx, tx, OwnerTenantID, LocalInstanceID)
		return e
	}), 503, "invalid_stored_observation")
}
