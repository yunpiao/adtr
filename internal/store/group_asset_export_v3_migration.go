package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrGroupAssetExportV3ReservedCollision = errors.New("migration blocked: reserved group asset export identity already exists")

func requireGroupAssetExportV3Baseline(ctx context.Context, tx pgx.Tx) error {
	var exclusive, collision bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),
 EXISTS(SELECT FROM adtr.tasks WHERE kind='group_assets.v3.export')
 OR EXISTS(SELECT FROM adtr.access_permissions WHERE mark='group_asset_exports')
 OR EXISTS(SELECT FROM pg_class WHERE relnamespace='adtr'::regnamespace AND relname IN ('group_asset_export_snapshots','group_asset_export_chunks','group_asset_export_manifests'))
 OR EXISTS(SELECT FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('group_asset_export_cleanup','group_asset_export_grant_epoch'))
 OR EXISTS(SELECT FROM pg_trigger tr JOIN pg_class cl ON cl.oid=tr.tgrelid WHERE cl.relnamespace='adtr'::regnamespace AND NOT tr.tgisinternal AND tr.tgname IN ('group_asset_export_cleanup','group_asset_export_grant_epoch'))`).Scan(&exclusive, &collision)
	if err != nil || !exclusive {
		return errors.New("group asset export migration check failed")
	}
	if collision {
		return ErrGroupAssetExportV3ReservedCollision
	}
	return nil
}
