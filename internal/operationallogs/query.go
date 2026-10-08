package operationallogs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

const eventJSON = `jsonb_build_object('schemaVersion',schema_version,'eventId',event_id,'processId',process_id,'module',module,'code',code,'outcome',outcome,'severity',severity,'reason',reason,'observedAt',observed_at,'recordedAt',recorded_at)`
const reportJSON = `jsonb_build_object('processId',process_id,'module',module,'startedAt',started_at,'observedAt',observed_at,'accepted',accepted,'acknowledged',acknowledged,'rejected',rejected,'queueFull',queue_full,'writeFailures',write_failures,'unacknowledged',unacknowledged,'abandoned',abandoned,'acceptanceStopped',acceptance_stopped,'reportedAt',reported_at,'evidence','incomplete_process_observation')`

// ListTx resolves the default window, total, page and coverage in one statement
// snapshot. A zero-match page is evidence only of zero recorded matching rows.
func ListTx(ctx context.Context, tx pgx.Tx, p Principal, f Filter) (List, error) {
	out := List{List: []Row{}}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	if _, err := RequireReadTx(ctx, tx, p); err != nil {
		return out, safeError(err)
	}
	f, err := normalizeFilter(f)
	if err != nil {
		return out, err
	}
	size := f.PageSize
	if size == -1 {
		size = 1001
	}
	var total int
	var raw []byte
	var start, end time.Time
	out.Coverage = Coverage{Mode: "best_effort", GapsPossible: true}
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() now),
 bounds AS MATERIALIZED (SELECT COALESCE(NULLIF($1,'')::timestamptz,now-interval '24 hours') start_at,COALESCE(NULLIF($2,'')::timestamptz,now) end_at FROM instant),
 selected AS MATERIALIZED (SELECT e.* FROM adtr.operational_log_events e CROSS JOIN bounds b WHERE e.recorded_at>=b.start_at AND e.recorded_at<b.end_at AND e.module=ANY($3::text[])),
 page AS (SELECT recorded_at,event_id,`+eventJSON+` event_data FROM selected ORDER BY recorded_at DESC,event_id COLLATE "C" DESC LIMIT $4 OFFSET $5)
 SELECT (SELECT count(*) FROM selected),COALESCE((SELECT jsonb_agg(event_data ORDER BY recorded_at DESC,event_id COLLATE "C" DESC) FROM page),'[]'::jsonb),b.start_at,b.end_at,s.created_at,(SELECT min(recorded_at) FROM adtr.operational_log_events)
 FROM bounds b CROSS JOIN adtr.operational_log_state s WHERE s.singleton=true`, f.StartTm, f.EndTm, f.SystemType, size, (f.PageIdx-1)*size).Scan(&total, &raw, &start, &end, &out.Coverage.JournalCreatedAt, &out.Coverage.FirstRecordedAt)
	if err != nil {
		return out, safeError(err)
	}
	if f.PageSize == -1 && total > 1000 {
		return out, problem(422, "result_too_large")
	}
	var events []Event
	if err = json.Unmarshal(raw, &events); err != nil {
		return out, problem(503, "invalid_stored_event")
	}
	for _, e := range events {
		e.ObservedAt = e.ObservedAt.UTC()
		e.RecordedAt = e.RecordedAt.UTC()
		if validateEvent(e, true) != nil {
			return out, problem(503, "invalid_stored_event")
		}
		data, err := json.Marshal(e)
		if err != nil {
			return out, problem(503, "invalid_stored_event")
		}
		out.List = append(out.List, Row{Event: e, Log: string(data)})
	}
	out.Selection = Selection{StartTm: start.UTC().Format(time.RFC3339Nano), EndTm: end.UTC().Format(time.RFC3339Nano), SystemType: f.SystemType}
	normalizeCoverage(&out.Coverage)
	pages := (total + size - 1) / size
	out.Page = Page{PageIdx: f.PageIdx, PageSize: f.PageSize, Total: total, TotalPage: pages}
	out.Exhausted = f.PageIdx >= pages
	return out, nil
}
func SourcesTx(ctx context.Context, tx pgx.Tx, p Principal) (Sources, error) {
	out := Sources{Modules: []Source{}, Reports: []RecorderReport{}, Coverage: Coverage{Mode: "best_effort", GapsPossible: true}}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	if _, err := RequireReadTx(ctx, tx, p); err != nil {
		return out, safeError(err)
	}
	var sources, reports []byte
	err := tx.QueryRow(ctx, `WITH modules(module) AS (VALUES('api'::text),('worker'::text)),counts AS MATERIALIZED (SELECT module,min(recorded_at) first_at,max(recorded_at) last_at,count(*) recorded_count FROM adtr.operational_log_events GROUP BY module),
 latest_reports AS (SELECT * FROM adtr.operational_log_reports ORDER BY reported_at DESC,process_id COLLATE "C" LIMIT $1)
 SELECT (SELECT jsonb_agg(jsonb_build_object('module',m.module,'registered',true,'firstRecordedAt',c.first_at,'lastRecordedAt',c.last_at,'recordedCount',COALESCE(c.recorded_count,0)) ORDER BY m.module COLLATE "C") FROM modules m LEFT JOIN counts c ON c.module=m.module),
 COALESCE((SELECT jsonb_agg(`+reportJSON+` ORDER BY reported_at DESC,process_id COLLATE "C") FROM latest_reports),'[]'::jsonb),
 (SELECT count(*)>$1 FROM adtr.operational_log_reports),s.created_at,(SELECT min(first_at) FROM counts)
 FROM adtr.operational_log_state s WHERE s.singleton=true`, MaxRecorderReports).Scan(&sources, &reports, &out.ReportsTruncated, &out.Coverage.JournalCreatedAt, &out.Coverage.FirstRecordedAt)
	if err != nil {
		return out, safeError(err)
	}
	if json.Unmarshal(sources, &out.Modules) != nil || json.Unmarshal(reports, &out.Reports) != nil {
		return out, problem(503, "invalid_stored_observation")
	}
	for i := range out.Modules {
		utcPointer(&out.Modules[i].FirstRecordedAt)
		utcPointer(&out.Modules[i].LastRecordedAt)
	}
	for i := range out.Reports {
		r := &out.Reports[i]
		r.StartedAt = r.StartedAt.UTC()
		r.ObservedAt = r.ObservedAt.UTC()
		r.ReportedAt = r.ReportedAt.UTC()
		if !validUUID(r.ProcessID) || (r.Module != API && r.Module != Worker) || validateObservation(r.RecorderObservation, Event{ProcessID: r.ProcessID, Module: r.Module}) != nil || r.ReportedAt.IsZero() || r.Evidence != "incomplete_process_observation" {
			return out, problem(503, "invalid_stored_observation")
		}
	}
	normalizeCoverage(&out.Coverage)
	return out, nil
}
func utcPointer(t **time.Time) {
	if *t != nil {
		u := (**t).UTC()
		*t = &u
	}
}
func normalizeCoverage(c *Coverage) {
	c.JournalCreatedAt = c.JournalCreatedAt.UTC()
	utcPointer(&c.FirstRecordedAt)
}
