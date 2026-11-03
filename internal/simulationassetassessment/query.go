package simulationassetassessment

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/assetassessment"
)

func RowsTx(ctx context.Context, tx pgx.Tx, s Scope, id string, f Filter) (RowsPage, error) {
	if e := authorize(tx, s, false); e != nil {
		return RowsPage{}, e
	}
	if !validFilter(f) {
		return RowsPage{}, ErrInvalid
	}
	r, e := ReportTx(ctx, tx, s, id)
	if e != nil {
		return RowsPage{}, e
	}
	p := RowsPage{ReportID: r.ID, ReportHash: r.Hash, Summary: r.Summary, Offset: f.Offset, Limit: f.Limit, Rows: []assetassessment.Result{}}
	matches := func(want, got string) bool { return want == "" || want == got }
	for _, row := range r.Rows {
		if !matches(f.Kind, string(row.Kind)) || !matches(f.AccountStatus, row.AccountStatus.Value) || !matches(f.Activity, row.Activity.Value) || !matches(f.GroupType, row.GroupType.Value) || !matches(f.GroupScope, row.GroupScope.Value) {
			continue
		}
		if p.Matched >= f.Offset && len(p.Rows) < f.Limit {
			p.Rows = append(p.Rows, row)
		}
		p.Matched++
	}
	_, e = marshalBounded(p, MaxResponseBytes)
	if e != nil {
		return RowsPage{}, e
	}
	return p, nil
}
func HistoryTx(ctx context.Context, tx pgx.Tx, s Scope, offset, limit int) (HistoryPage, error) {
	p := HistoryPage{Offset: offset, Limit: limit, Reports: []ReportSummary{}}
	if e := authorize(tx, s, false); e != nil {
		return HistoryPage{}, e
	}
	if offset < 0 || offset > MaxReports || limit < 1 || limit > MaxPageSize {
		return HistoryPage{}, ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT report_id FROM adtr.simulation_asset_reports WHERE tenant_id=$1 AND domain_id=$2 AND actor_id=$3 AND epoch=$4 ORDER BY report_id LIMIT 65`, s.Tenant, s.Domain, s.ActorID, s.Epoch)
	if e != nil {
		return HistoryPage{}, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return HistoryPage{}, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return HistoryPage{}, e
	}
	if len(ids) > MaxReports {
		return HistoryPage{}, ErrLimit
	}
	p.Total = len(ids)
	for i, id := range ids {
		if i < offset || len(p.Reports) >= limit {
			continue
		}
		r, e := ReportTx(ctx, tx, s, id)
		if e != nil {
			return HistoryPage{}, e
		}
		p.Reports = append(p.Reports, ReportSummary{ID: r.ID, SourceHash: r.SourceHash, ScenarioID: r.ScenarioID, AsOf: r.AsOf, CreatedAt: r.CreatedAt, Hash: r.Hash, Summary: r.Summary})
	}
	_, e = marshalBounded(p, MaxResponseBytes)
	if e != nil {
		return HistoryPage{}, e
	}
	return p, nil
}
