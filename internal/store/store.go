package store

import (
	"context"
	"errors"
	"github.com/yunpiao/adtr/internal/schemaversion"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/systemhealth"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

const SchemaVersion = schemaversion.Current

// Ready checks the schema, not just the TCP port. Each probe owns its connection.
func Ready(ctx context.Context, config *pgx.ConnConfig) error {
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		return errors.New("database unavailable")
	}
	defer conn.Close(ctx)
	var version int
	if err := conn.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton = true").Scan(&version); err != nil || version != SchemaVersion {
		return errors.New("schema unavailable or incompatible")
	}
	return nil
}

// Migrate runs only as an explicit command. Runtime probes never mutate storage.
// Versioned migrations are serialized and atomic; runtime readiness never migrates.
func Migrate(ctx context.Context, config *pgx.ConnConfig) error {
	return migrateTo(ctx, config, SchemaVersion)
}

// migrateTo shares the historical migration sequence with integration fixtures.
// Production callers always enter through Migrate and target the exact current version.
func migrateTo(ctx context.Context, config *pgx.ConnConfig, target int) error {
	if target < 1 || target > SchemaVersion {
		return errors.New("unsupported migration target")
	}
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		return errors.New("migration connection failed")
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("migration transaction failed")
	}
	defer tx.Rollback(ctx)
	statements := []string{
		"SELECT pg_advisory_xact_lock(734192801)",
		"CREATE SCHEMA IF NOT EXISTS adtr",
		"CREATE TABLE IF NOT EXISTS adtr.schema_version (singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton), version integer NOT NULL CHECK (version > 0))",
		"INSERT INTO adtr.schema_version (singleton, version) VALUES (true, 1) ON CONFLICT (singleton) DO NOTHING",
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return errors.New("migration statement failed")
		}
	}
	var version int
	if err := tx.QueryRow(ctx, "SELECT version FROM adtr.schema_version WHERE singleton = true").Scan(&version); err != nil || (version < 1 || version > target) {
		return errors.New("unsupported schema version; refusing migration")
	}
	if err := requireCredentialUseQuiescence(ctx, tx, version, target); err != nil {
		return err
	}
	if version < 2 && target >= 2 {
		if _, err := tx.Exec(ctx, auth.Schema); err != nil {
			return errors.New("identity schema migration failed")
		}
		version = 2
	}
	if version < 3 && target >= 3 {
		if _, err := tx.Exec(ctx, auth.SchemaV3); err != nil {
			return errors.New("access schema migration failed")
		}
		version = 3
	}
	if version < 4 && target >= 4 {
		if _, err := tx.Exec(ctx, auth.ResourceSchema); err != nil {
			return errors.New("resource schema migration failed")
		}
		version = 4
	}
	if version < 5 && target >= 5 {
		if _, err := tx.Exec(ctx, auth.TaskPermissionSchema+auth.TaskAuthorizationSchema+tasks.Schema); err != nil {
			return errors.New("task schema migration failed")
		}
		version = 5
	}
	if version < 6 && target >= 6 {
		if _, err := tx.Exec(ctx, auth.AuditPermissionSchema+audit.Schema); err != nil {
			return errors.New("audit schema migration failed")
		}
		version = 6
	}
	if version < 7 && target >= 7 {
		if _, err := tx.Exec(ctx, auth.SystemPermissionMarks+systemhealth.Schema+audit.ViewSchema); err != nil {
			return errors.New("system health schema migration failed")
		}
		version = 7
	}
	if version < 8 && target >= 8 {
		if _, err := tx.Exec(ctx, tasks.ArchiveCoreSchema+auth.SchedulePermissionSchema+auth.ArchivePermissionSchema+schedules.Schema+taskarchive.Schema+audit.ViewSchema+systemhealth.SchedulerActivitySchema); err != nil {
			return errors.New("task maintenance schema migration failed")
		}
		version = 8
	}
	if version < 9 && target >= 9 {
		if _, err := tx.Exec(ctx, auth.DomainPermissionSchema+domains.Schema+audit.DomainViewSchema); err != nil {
			return errors.New("domain connection schema migration failed")
		}
		version = 9
	}
	if version < 10 && target >= 10 {
		if _, err := tx.Exec(ctx, auth.OperationAccountPermissionSchema+operationaccounts.Schema+audit.OperationAccountViewSchema); err != nil {
			return errors.New("operation account schema migration failed")
		}
		version = 10
	}
	if version < 11 && target >= 11 {
		if _, err := tx.Exec(ctx, auth.ProfileSchema+audit.OperationAccountViewSchema); err != nil {
			return errors.New("personal profile schema migration failed")
		}
		version = 11
	}
	if version < 12 && target >= 12 {
		if _, err := tx.Exec(ctx, credentialuse.Schema+audit.CredentialUseViewSchema); err != nil {
			return errors.New("credential use governance schema migration failed")
		}
		version = 12
	}
	if version < 13 && target >= 13 {
		if _, err := tx.Exec(ctx, domains.AccountReferenceSchema+audit.AccountReferenceViewSchema); err != nil {
			return errors.New("account reference schema migration failed")
		}
		version = 13
	}
	if version < 14 && target >= 14 {
		var collision bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.tasks WHERE kind='system.logs_bundle')`).Scan(&collision); err != nil {
			return errors.New("operational log migration check failed")
		}
		if collision {
			return ErrOperationalLogKindCollision
		}
		if _, err := tx.Exec(ctx, auth.OperationalLogPermissionSchema+operationallogs.Schema+operationallogs.BundleSchema+audit.OperationalLogViewSchema); err != nil {
			return errors.New("operational log schema migration failed")
		}
		version = 14
	}
	if version < 15 && target >= 15 {
		var taskCollision, dependencyCollision bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.directory_read'),
 EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read')`).Scan(&taskCollision, &dependencyCollision); err != nil {
			return errors.New("directory migration check failed")
		}
		if taskCollision {
			return ErrDirectoryTaskKindCollision
		}
		if dependencyCollision {
			return ErrDirectoryDependencyKindCollision
		}
		if _, err := tx.Exec(ctx, auth.DirectoryPermissionMarks+credentialuse.DirectoryPurposeSchema+domains.DirectoryDependencySchema+domains.DirectoryUseSchema+domains.DirectoryObservationSchema+domains.DirectoryAuditSchema+audit.DirectoryViewSchema); err != nil {
			return errors.New("directory schema migration failed")
		}
		version = 15
	}
	if version < 16 && target >= 16 {
		if err := requireDirectoryV2Baseline(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, credentialuse.DirectoryV2PurposeSchema+domains.DirectoryDependencyV2Schema+domains.DirectoryUseV2Schema+domains.DirectoryObservationV2Schema+domains.DirectoryAuditV2Schema+audit.DirectoryV2ViewSchema); err != nil {
			return errors.New("directory v2 schema migration failed")
		}
		version = 16
	}

	if _, err := tx.Exec(ctx, "UPDATE adtr.schema_version SET version=$1 WHERE singleton=true", version); err != nil {
		return errors.New("schema version update failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migration commit failed")
	}
	return nil
}
