package domains

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// These fixed v2 helpers exist only in test builds. They do not widen the
// production registry, admission, task authorization or current API routes.
func DirectoryV2UseKindForTest(s *Store, execute tasks.Executor) tasks.Kind {
	kind := DirectoryUseKindForTest(s, execute)
	kind.Name, kind.Validate = DirectoryV2KindName, validateDirectoryV2Payload
	kind.OnQuiesced = func(ctx context.Context, tx pgx.Tx, q tasks.QuiescedAttempt) error {
		return s.acknowledgeDirectoryUseForProfile(ctx, tx, directoryTaskV2, q)
	}
	return kind
}

func ReserveDirectoryV2UseForTest(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
	p, err := directoryUseTaskPayloadForProfile(directoryTaskV2, t)
	if err != nil {
		return err
	}
	return reserveDirectoryUseForProfileTx(ctx, tx, directoryTaskV2, t, p)
}

func OpenDirectoryV2UseForTest(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
	p, err := directoryUseTaskPayloadForProfile(directoryTaskV2, t)
	if err != nil {
		return err
	}
	return openDirectoryUseForProfileTx(ctx, tx, directoryTaskV2, t, p)
}

func ReconcileReservedDirectoryV2UsesForTest(ctx context.Context, s *Store, cfg *pgx.ConnConfig, limit int) (int, error) {
	return s.reconcileReservedDirectoryUsesForProfile(ctx, cfg, directoryTaskV2, limit)
}
