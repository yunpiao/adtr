package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

func profileSchemaGate(ctx context.Context, tx pgx.Tx) error {
	if err := tasks.CheckSchemaTx(ctx, tx, schemaversion.Current); err != nil {
		if errors.Is(err, tasks.ErrSchemaIncompatible) {
			return fail(503, "schema_incompatible")
		}
		return fail(503, "schema_unavailable")
	}
	return nil
}
