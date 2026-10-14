package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrComputerAssetExportV3ReservedCollision = errors.New("migration blocked: reserved computer asset export identity already exists")

func requireComputerAssetExportV3Baseline(ctx context.Context, tx pgx.Tx) error {
	var exclusive, collision bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),
 EXISTS(SELECT FROM adtr.tasks WHERE kind='computer_assets.v3.export')
 OR EXISTS(SELECT FROM adtr.access_permissions WHERE mark='computer_asset_exports')
 OR EXISTS(SELECT FROM pg_class WHERE relnamespace='adtr'::regnamespace AND relname IN ('computer_asset_export_snapshots','computer_asset_export_chunks','computer_asset_export_manifests'))
 OR EXISTS(SELECT FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('computer_asset_export_cleanup','computer_asset_export_grant_epoch'))
 OR EXISTS(SELECT FROM pg_trigger tr JOIN pg_class cl ON cl.oid=tr.tgrelid WHERE cl.relnamespace='adtr'::regnamespace AND NOT tr.tgisinternal AND tr.tgname IN ('computer_asset_export_cleanup','computer_asset_export_grant_epoch'))`).Scan(&exclusive, &collision)
	if err != nil || !exclusive {
		return errors.New("computer asset export migration check failed")
	}
	if collision {
		return ErrComputerAssetExportV3ReservedCollision
	}
	return nil
}
