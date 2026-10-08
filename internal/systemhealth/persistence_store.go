package systemhealth

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

const databaseTimeout = 3 * time.Second

// Store uses an already verified connection configuration. It never accepts a
// request-controlled connection string, filesystem path, or installation owner.
type Store struct {
	config         *pgx.ConnConfig
	expectedSchema int
	owner          string
	targets        []StorageTarget
	byID           map[string]StorageTarget
}

func NewStore(config *pgx.ConnConfig, expectedSchemaVersion int, options ...StoreOptions) (*Store, error) {
	if config == nil || expectedSchemaVersion <= 0 || len(options) > 1 {
		return nil, problem(500, "invalid_configuration")
	}
	opt := StoreOptions{OwnerTenantID: OwnerTenantID}
	if len(options) == 1 {
		opt = options[0]
		if opt.OwnerTenantID == "" {
			opt.OwnerTenantID = OwnerTenantID
		}
	}
	if !validID(opt.OwnerTenantID) {
		return nil, problem(500, "invalid_configuration")
	}
	checked, err := NewSampler(Config{Storage: opt.StorageTargets})
	if err != nil {
		return nil, problem(500, "invalid_configuration")
	}
	s := &Store{config: config.Copy(), expectedSchema: expectedSchemaVersion, owner: opt.OwnerTenantID, targets: append([]StorageTarget(nil), checked.storage...), byID: map[string]StorageTarget{}}
	for _, target := range s.targets {
		s.byID[target.ID] = target
	}
	return s, nil
}

func problem(status int, code string) error { return &Error{Status: status, Code: code} }
func (s *Store) OwnerTenantID() string      { return s.owner }
func (s *Store) CheckSchemaTx(ctx context.Context, tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	return safeError(tasks.CheckSchemaTx(ctx, tx, s.expectedSchema))
}
func safeError(err error) error {
	if err == nil {
		return nil
	}
	var public *Error
	if errors.As(err, &public) {
		return public
	}
	if errors.Is(err, tasks.ErrSchemaIncompatible) {
		return problem(503, "schema_incompatible")
	}
	if errors.Is(err, tasks.ErrSchemaGateRequired) {
		return problem(503, "schema_gate_required")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return problem(503, "health_timeout")
	}
	return problem(503, "health_storage_unavailable")
}

func (s *Store) scope(tenant, instance string) error {
	if tenant != s.owner {
		return problem(403, "forbidden")
	}
	if instance != LocalInstanceID {
		return problem(404, "instance_not_found")
	}
	return nil
}

// requireTx verifies the earlier migration gate before authentication's locks;
// it never introduces a late, potentially blocking advisory lock acquisition.
func (s *Store) requireTx(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return tasks.ErrSchemaGateRequired
	}
	var held bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode IN ('ShareLock','ExclusiveLock'))`).Scan(&held)
	if err != nil {
		return err
	}
	if !held {
		return tasks.ErrSchemaGateRequired
	}
	return tasks.CheckSchemaTx(ctx, tx, s.expectedSchema)
}

func (s *Store) withTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return safeError(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return safeError(err)
	}
	defer tx.Rollback(ctx)
	if err = tasks.CheckSchemaTx(ctx, tx, s.expectedSchema); err == nil {
		err = fn(ctx, tx)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return safeError(err)
}

// Persist records one actual sampler observation atomically. Unavailable
// attempts are retained, with null values; no previous values are carried over.
func (s *Store) Persist(ctx context.Context, snapshot Snapshot) error {
	if err := s.validateSnapshot(snapshot); err != nil {
		return err
	}
	return s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var now time.Time
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		if snapshot.ObservedAt.After(now.Add(time.Second)) {
			return problem(400, "future_observation")
		}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return problem(400, "invalid_observation")
		}
		tag, err := tx.Exec(ctx, `INSERT INTO adtr.system_resource_samples(tenant_id,instance_id,observed_at,cpu_percent,ram_percent,snapshot) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, s.owner, snapshot.Instance, snapshot.ObservedAt, snapshot.CPU.Percent, snapshot.Memory.Percent, data)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return problem(409, "observation_conflict")
		}
		for _, metric := range snapshot.Storage {
			data, err = json.Marshal(metric)
			if err != nil {
				return problem(400, "invalid_observation")
			}
			if _, err = tx.Exec(ctx, `INSERT INTO adtr.system_storage_samples(tenant_id,instance_id,mount_id,observed_at,use_percent,observation) VALUES($1,$2,$3,$4,$5,$6)`, s.owner, snapshot.Instance, metric.ID, snapshot.ObservedAt, metric.Percent, data); err != nil {
				return err
			}
		}
		return nil
	})
}

func validPercent(meta Metadata, value *float64) bool {
	if meta.Availability == Available {
		return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 100
	}
	return value == nil && (meta.Availability == Unavailable || meta.Availability == WarmingUp || meta.Availability == Unsupported)
}
func (s *Store) validateSnapshot(snapshot Snapshot) error {
	bad := func() error { return problem(400, "invalid_observation") }
	if snapshot.Instance != LocalInstanceID || snapshot.ObservedAt.IsZero() || snapshot.ObservedAt.Unix() < 0 || len(snapshot.Storage) != len(s.targets) {
		return bad()
	}
	if !validPercent(snapshot.CPU.Metadata, snapshot.CPU.Percent) || !validPercent(snapshot.Memory.Metadata, snapshot.Memory.Percent) {
		return bad()
	}
	if !snapshot.CPU.ObservedAt.Equal(snapshot.ObservedAt) || !snapshot.Memory.ObservedAt.Equal(snapshot.ObservedAt) {
		return bad()
	}
	seen := map[string]bool{}
	for _, metric := range snapshot.Storage {
		target, ok := s.byID[metric.ID]
		if !ok || seen[metric.ID] || metric.Mount != target.Mount || !metric.ObservedAt.Equal(snapshot.ObservedAt) || !validPercent(metric.Metadata, metric.Percent) {
			return bad()
		}
		seen[metric.ID] = true
		for _, value := range []*string{metric.TotalBytes, metric.UsedBytes, metric.FreeBytes, metric.ReservedBytes} {
			if value != nil {
				if _, err := strconv.ParseUint(*value, 10, 64); err != nil {
					return bad()
				}
			}
		}
	}
	return nil
}

// CurrentTx and the other Tx methods require caller-owned authentication,
// authorization and exact schema gating. The caller rolls back on any error.
func (s *Store) CurrentTx(ctx context.Context, tx pgx.Tx, tenant, instance string) (Current, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	out, err := s.current(ctx, tx, tenant, instance)
	return out, safeError(err)
}
func (s *Store) current(ctx context.Context, tx pgx.Tx, tenant, instance string) (Current, error) {
	out := Current{Instance: instance, Availability: Unavailable, Reason: "no_samples"}
	if err := s.scope(tenant, instance); err != nil {
		return out, err
	}
	if err := s.requireTx(ctx, tx); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(),min(observed_at) FROM adtr.system_resource_samples WHERE tenant_id=$1 AND instance_id=$2`, tenant, instance).Scan(&out.CheckedAt, &out.AvailableSince); err != nil {
		return out, err
	}
	var data []byte
	err := tx.QueryRow(ctx, `SELECT snapshot FROM adtr.system_resource_samples WHERE tenant_id=$1 AND instance_id=$2 ORDER BY observed_at DESC LIMIT 1`, tenant, instance).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var snapshot Snapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return out, problem(503, "invalid_stored_observation")
	}
	if err = s.validateSnapshot(snapshot); err != nil {
		return out, problem(503, "invalid_stored_observation")
	}
	out.Snapshot = &snapshot
	out.Availability = Available
	out.Reason = ""
	if snapshot.ObservedAt.After(out.CheckedAt.Add(time.Second)) {
		out.Availability = Unavailable
		out.Reason = "clock_skew"
	} else if out.CheckedAt.Sub(snapshot.ObservedAt) > SampleStaleAfter {
		out.Stale = true
		out.Availability = Unavailable
		out.Reason = "stale_sample"
	}
	return out, nil
}
