package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type quiescenceCheckTx struct {
	pgx.Tx
	t       *testing.T
	rows    []quiescenceCheckRow
	queries []string
}
type quiescenceCheckRow struct {
	values []bool
	err    error
}

func (r quiescenceCheckRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("unexpected quiescence result width")
	}
	for i, value := range r.values {
		target, ok := dest[i].(*bool)
		if !ok {
			return errors.New("unexpected quiescence result type")
		}
		*target = value
	}
	return nil
}
func (tx *quiescenceCheckTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	tx.t.Helper()
	tx.queries = append(tx.queries, sql)
	if len(tx.rows) == 0 {
		tx.t.Fatal("unexpected migration query")
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func TestCredentialUseQuiescenceChecksOnlyInstalledLedgers(t *testing.T) {
	for _, test := range []struct {
		name                       string
		installed, target          int
		rows                       []quiescenceCheckRow
		blocked, failed, directory bool
	}{
		{name: "before_ledgers", installed: 12, target: 15},
		{name: "current_is_no_upgrade", installed: 15, target: 15},
		{name: "historical_baseline_repeat", installed: 14, target: 14},
		{name: "B2_quiescent_14_to_15", installed: 14, target: 15, rows: []quiescenceCheckRow{{values: []bool{true, false}}}},
		{name: "B2_opened_14_to_15", installed: 14, target: 15, rows: []quiescenceCheckRow{{values: []bool{true, true}}}, blocked: true},
		{name: "B2_opened_future", installed: 15, target: 16, rows: []quiescenceCheckRow{{values: []bool{true, true}}}, blocked: true},
		{name: "directory_opened_future", installed: 15, target: 16, rows: []quiescenceCheckRow{{values: []bool{true, false}}, {values: []bool{true}}}, blocked: true, directory: true},
		{name: "both_quiescent_future", installed: 15, target: 16, rows: []quiescenceCheckRow{{values: []bool{true, false}}, {values: []bool{false}}}, directory: true},
		{name: "exclusive_gate_required", installed: 15, target: 16, rows: []quiescenceCheckRow{{values: []bool{false, false}}}, failed: true},
		{name: "B2_query_failure", installed: 14, target: 15, rows: []quiescenceCheckRow{{err: errors.New("unavailable")}}, failed: true},
		{name: "directory_query_failure", installed: 15, target: 16, rows: []quiescenceCheckRow{{values: []bool{true, false}}, {err: errors.New("unavailable")}}, failed: true, directory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &quiescenceCheckTx{t: t, rows: test.rows}
			err := requireCredentialUseQuiescence(context.Background(), tx, test.installed, test.target)
			if errors.Is(err, ErrCredentialUseMigrationBlocked) != test.blocked || (err != nil) != (test.blocked || test.failed) {
				t.Fatal("unexpected migration preflight decision", err)
			}
			if len(tx.rows) != 0 {
				t.Fatal("required ledger check was omitted")
			}
			var directory bool
			for _, sql := range tx.queries {
				directory = directory || strings.Contains(sql, "domain_directory_task_uses")
			}
			if directory != test.directory {
				t.Fatal("preflight inspected an unavailable ledger or omitted the directory ledger")
			}
		})
	}
}
