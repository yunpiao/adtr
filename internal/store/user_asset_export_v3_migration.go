package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrUserAssetExportV3ReservedCollision = errors.New("migration blocked: reserved user asset export identity already exists")

func requireUserAssetExportV3Baseline(ctx context.Context, tx pgx.Tx) error {
	var exclusive, collision bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=0::oid AND objid::bigint=734192801 AND objsubid=1 AND mode='ExclusiveLock'),
 EXISTS(SELECT FROM adtr.tasks WHERE kind='user_assets.v3.export')
 OR EXISTS(SELECT FROM adtr.access_permissions WHERE mark='user_asset_exports')
 OR EXISTS(SELECT FROM pg_class WHERE relnamespace='adtr'::regnamespace AND relname IN ('user_asset_export_snapshots','user_asset_export_chunks','user_asset_export_manifests'))
 OR EXISTS(SELECT FROM pg_proc WHERE pronamespace='adtr'::regnamespace AND proname IN ('user_asset_export_cleanup','user_asset_export_grant_epoch'))
 OR EXISTS(SELECT FROM pg_trigger tr JOIN pg_class cl ON cl.oid=tr.tgrelid WHERE cl.relnamespace='adtr'::regnamespace AND NOT tr.tgisinternal AND tr.tgname IN ('user_asset_export_cleanup','user_asset_export_grant_epoch'))`).Scan(&exclusive, &collision)
	if err != nil || !exclusive {
		return errors.New("user asset export migration check failed")
	}
	if collision {
		return ErrUserAssetExportV3ReservedCollision
	}
	return nil
}
