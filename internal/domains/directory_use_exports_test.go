package domains

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Test-only component access. Production registration/admission is deliberately
// absent; these helpers never manufacture a QuiescedAttempt.
func DirectoryUseKindForTest(s *Store, execute tasks.Executor) tasks.Kind {
	return tasks.Kind{Name: DirectoryKindName, Version: 1, MaxAttempts: 1, Lease: 30 * time.Second,
		Heartbeat: time.Second, Timeout: 10 * time.Second, RetryBase: time.Second, RetryCap: time.Second,
		SingleAttemptOnly: true, Validate: validateDirectoryPayload, Execute: execute, OnQuiesced: s.acknowledgeDirectoryUse}
}

func ReserveDirectoryUseForTest(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
	var p directoryPinnedPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return err
	}
	return reserveDirectoryUseTx(ctx, tx, t, p)
}

func OpenDirectoryUseForTest(ctx context.Context, tx pgx.Tx, t tasks.Task) error {
	var p directoryPinnedPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return err
	}
	return openDirectoryUseTx(ctx, tx, t, p)
}
