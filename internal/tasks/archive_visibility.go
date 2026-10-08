package tasks

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func applyTaskVisibility(ctx context.Context, tx pgx.Tx, t *Task) error {
	err := tx.QueryRow(ctx, "SELECT archived,visibility_version FROM adtr.task_visibility WHERE tenant_id=$1 AND task_id=$2", t.TenantID, t.ID).Scan(&t.Archived, &t.VisibilityVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Archived = false
		t.VisibilityVersion = 0
		return nil
	}
	return err
}
func applyTaskListVisibility(ctx context.Context, tx pgx.Tx, list []Task) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	positions := map[string]int{}
	for i, t := range list {
		ids[i] = t.ID
		positions[t.ID] = i
	}
	rows, err := tx.Query(ctx, "SELECT task_id,archived,visibility_version FROM adtr.task_visibility WHERE tenant_id=$1 AND task_id=ANY($2::text[])", list[0].TenantID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var archived bool
		var version int64
		if err = rows.Scan(&id, &archived, &version); err != nil {
			return err
		}
		i := positions[id]
		list[i].Archived = archived
		list[i].VisibilityVersion = version
	}
	return rows.Err()
}
func validTaskVisibility(visibility string) bool {
	return visibility == "" || visibility == "active" || visibility == "archived" || visibility == "all"
}
