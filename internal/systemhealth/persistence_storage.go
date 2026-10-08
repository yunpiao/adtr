package systemhealth

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) alarm(ctx context.Context, tx pgx.Tx, tenant, instance, mount string) (AlarmSetting, error) {
	setting := AlarmSetting{Instance: instance, MountID: mount, AlarmPercent: DefaultAlarmPercent}
	err := tx.QueryRow(ctx, `SELECT alarm_percent,revision,updated_at FROM adtr.system_alarm_settings WHERE tenant_id=$1 AND instance_id=$2 AND mount_id=$3`, tenant, instance, mount).Scan(&setting.AlarmPercent, &setting.Revision, &setting.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return setting, err
}

func (s *Store) StorageTx(ctx context.Context, tx pgx.Tx, tenant, instance string, page, pageSize int) (StorageView, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	out, err := s.storage(ctx, tx, tenant, instance, page, pageSize)
	return out, safeError(err)
}
func (s *Store) storage(ctx context.Context, tx pgx.Tx, tenant, instance string, page, pageSize int) (StorageView, error) {
	out := StorageView{Instance: instance, Storage: []StorageItem{}, Page: page, PageSize: pageSize}
	if page < 1 || page > 100000 || pageSize < 1 || pageSize > 50 {
		return out, problem(400, "invalid_pagination")
	}
	current, err := s.current(ctx, tx, tenant, instance)
	if err != nil {
		return out, err
	}
	out.Availability, out.Reason, out.Stale, out.CheckedAt = current.Availability, current.Reason, current.Stale, current.CheckedAt
	metrics := make(map[string]StorageMetric, len(s.targets))
	if current.Snapshot != nil {
		out.ObservedAt = &current.Snapshot.ObservedAt
		for _, metric := range current.Snapshot.Storage {
			metrics[metric.ID] = metric
		}
	}
	out.Total = len(s.targets)
	start := (page - 1) * pageSize
	if start >= len(s.targets) {
		return out, nil
	}
	end := start + pageSize
	if end > len(s.targets) {
		end = len(s.targets)
	}
	for _, target := range s.targets[start:end] {
		metric, ok := metrics[target.ID]
		if !ok {
			metric = StorageMetric{Metadata: Metadata{Source: "statfs", Scope: FilesystemScope, Availability: Unavailable, Reason: "no_samples"}, ID: target.ID, Mount: target.Mount}
		}
		setting, err := s.alarm(ctx, tx, tenant, instance, target.ID)
		if err != nil {
			return out, err
		}
		item := StorageItem{StorageMetric: metric, AlarmPercent: setting.AlarmPercent, Revision: setting.Revision}
		if current.Availability == Available && metric.Availability == Available && metric.Percent != nil {
			exceeded := *metric.Percent >= float64(setting.AlarmPercent)
			item.AlarmExceeded = &exceeded
		}
		out.Storage = append(out.Storage, item)
	}
	return out, nil
}

func (s *Store) SetAlarmTx(ctx context.Context, tx pgx.Tx, tenant string, actorID int64, in AlarmInput) (AlarmSetting, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	out, err := s.setAlarm(ctx, tx, tenant, actorID, in)
	return out, safeError(err)
}
func (s *Store) setAlarm(ctx context.Context, tx pgx.Tx, tenant string, actorID int64, in AlarmInput) (AlarmSetting, error) {
	out := AlarmSetting{Instance: in.Instance, MountID: in.MountID}
	if err := s.scope(tenant, in.Instance); err != nil {
		return out, err
	}
	if actorID <= 0 {
		return out, problem(403, "forbidden")
	}
	if _, ok := s.byID[in.MountID]; !ok {
		return out, problem(404, "storage_not_found")
	}
	if in.Percent < 85 || in.Percent > 90 || in.ExpectedRevision < 0 || in.ExpectedRevision == math.MaxInt64 {
		return out, problem(400, "invalid_alarm_setting")
	}
	if err := s.requireTx(ctx, tx); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO adtr.system_alarm_settings(tenant_id,instance_id,mount_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, in.Instance, in.MountID); err != nil {
		return out, err
	}
	var previous int
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT alarm_percent,revision FROM adtr.system_alarm_settings WHERE tenant_id=$1 AND instance_id=$2 AND mount_id=$3 FOR UPDATE`, tenant, in.Instance, in.MountID).Scan(&previous, &revision); err != nil {
		return out, err
	}
	if revision != in.ExpectedRevision {
		return out, problem(409, "revision_conflict")
	}
	var updated time.Time
	err := tx.QueryRow(ctx, `UPDATE adtr.system_alarm_settings SET alarm_percent=$4,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND instance_id=$2 AND mount_id=$3 AND revision=$5 RETURNING alarm_percent,revision,updated_at`, tenant, in.Instance, in.MountID, in.Percent, in.ExpectedRevision).Scan(&out.AlarmPercent, &out.Revision, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, problem(409, "revision_conflict")
	}
	if err != nil {
		return out, err
	}
	out.UpdatedAt = &updated
	_, err = tx.Exec(ctx, `INSERT INTO adtr.system_alarm_audit(tenant_id,instance_id,mount_id,actor_id,previous_percent,alarm_percent,previous_revision,revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, in.Instance, in.MountID, actorID, previous, in.Percent, revision, out.Revision)
	return out, err
}
