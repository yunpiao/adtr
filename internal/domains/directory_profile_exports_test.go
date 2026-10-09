package domains

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/ldapconnection"
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

// Synthetic observation publication uses real profile validation, current
// authority, fenced use opening and SQL publication. It never reads credentials
// or pretends to prove a network transport result.
func PublishDirectoryV2ObservationForTest(s *Store, observation ldapconnection.DirectoryV2Observation) tasks.Executor {
	return func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		pins, err := directoryUseTaskPayloadForProfile(directoryTaskV2, ex.Task)
		if err != nil {
			return failed("invalid_input")
		}
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.currentDirectoryForProfile(ctx, tx, directoryTaskV2, ex.Task, pins)
			if err == nil {
				err = openDirectoryUseForProfileTx(ctx, tx, directoryTaskV2, ex.Task, pins)
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return directoryExecutionError(err)
		}
		_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.currentDirectoryForProfile(ctx, tx, directoryTaskV2, ex.Task, pins)
			if err == nil {
				err = publishDirectoryObservationV2Tx(ctx, tx, ex.Task, pins, observation)
			}
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return directoryExecutionError(err)
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
}
