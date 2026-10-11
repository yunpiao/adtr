package domains

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Isolated synthetic publication helper; network execution is tested separately.
func PublishDirectoryV3ObservationForTest(s *Store, observation ldapconnection.DirectoryV3Observation) tasks.Executor {
	return func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		pins, err := directoryUseTaskPayloadForProfile(directoryTaskV3, ex.Task)
		if err != nil {
			return failed("invalid_input")
		}
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.currentDirectoryForProfile(ctx, tx, directoryTaskV3, ex.Task, pins)
			if err == nil {
				err = openDirectoryUseForProfileTx(ctx, tx, directoryTaskV3, ex.Task, pins)
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return directoryExecutionError(err)
		}
		_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := s.currentDirectoryForProfile(ctx, tx, directoryTaskV3, ex.Task, pins)
			if err == nil {
				err = publishDirectoryObservationV3Tx(ctx, tx, ex.Task, pins, observation)
			}
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			return directoryExecutionError(err)
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
}
