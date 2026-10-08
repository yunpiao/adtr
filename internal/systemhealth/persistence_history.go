package systemhealth

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) validateHistory(in HistoryInput, now time.Time) error {
	if in.StartTime < 0 || in.EndTime <= in.StartTime || in.EndTime > now.Unix() || in.EndTime-in.StartTime > int64(MaxHistoryWindow/time.Second) {
		return problem(400, "invalid_time_range")
	}
	switch in.GraphType {
	case "cpu_basic", "ram_basic":
		if in.StorageID != "" {
			return problem(400, "invalid_storage_id")
		}
	case "disk_usage":
		if _, ok := s.byID[in.StorageID]; !ok {
			return problem(404, "storage_not_found")
		}
	default:
		return problem(400, "invalid_graph_type")
	}
	return nil
}

func (s *Store) HistoryTx(ctx context.Context, tx pgx.Tx, tenant string, in HistoryInput) (History, error) {
	ctx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	out, err := s.history(ctx, tx, tenant, in)
	return out, safeError(err)
}
func (s *Store) history(ctx context.Context, tx pgx.Tx, tenant string, in HistoryInput) (History, error) {
	out := History{Instance: in.Instance, GraphType: in.GraphType, Info: []HistorySeries{{Mode: in.GraphType, Data: HistoryData{Timestamp: []int64{}, Value: []string{}}}}, Gaps: []HistoryGap{}, SampleIntervalSeconds: int64(SampleCadence / time.Second)}
	if err := s.scope(tenant, in.Instance); err != nil {
		return out, err
	}
	if err := s.requireTx(ctx, tx); err != nil {
		return out, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return out, err
	}
	if err := s.validateHistory(in, now); err != nil {
		return out, err
	}
	column, table, filter := "cpu_percent", "adtr.system_resource_samples", ""
	args := []any{tenant, in.Instance}
	if in.GraphType == "ram_basic" {
		column = "ram_percent"
	}
	if in.GraphType == "disk_usage" {
		column, table, filter = "use_percent", "adtr.system_storage_samples", " AND mount_id=$3"
		args = append(args, in.StorageID)
	}
	base := " FROM " + table + " WHERE tenant_id=$1 AND instance_id=$2" + filter + " AND " + column + " IS NOT NULL"
	if err := tx.QueryRow(ctx, "SELECT min(observed_at)"+base, args...).Scan(&out.AvailableSince); err != nil {
		return out, err
	}
	args = append(args, time.Unix(in.StartTime, 0).UTC(), time.Unix(in.EndTime, 0).UTC())
	startArg, endArg := strconv.Itoa(len(args)-1), strconv.Itoa(len(args))
	rows, err := tx.Query(ctx, "SELECT observed_at,"+column+base+" AND observed_at >= $"+startArg+" AND observed_at < $"+endArg+" ORDER BY observed_at ASC LIMIT 5761", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	times, values := []time.Time{}, []float64{}
	for rows.Next() {
		var at time.Time
		var value float64
		if err = rows.Scan(&at, &value); err != nil {
			return out, err
		}
		times = append(times, at)
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(times) > MaxHistoryPoints {
		return out, problem(422, "history_point_limit")
	}
	out.Info[0].Data, out.Gaps = historyData(in, times, values)
	return out, nil
}

func historyData(in HistoryInput, times []time.Time, values []float64) (HistoryData, []HistoryGap) {
	data := HistoryData{Timestamp: []int64{}, Value: []string{}}
	gaps := []HistoryGap{}
	if len(times) == 0 {
		return data, []HistoryGap{{StartTime: in.StartTime, EndTime: in.EndTime}}
	}
	minValue, maxValue, total := values[0], values[0], 0.0
	const cadence = int64(SampleCadence / time.Second)
	for i, at := range times {
		timestamp := at.Unix()
		value := values[i]
		data.Timestamp = append(data.Timestamp, timestamp)
		data.Value = append(data.Value, strconv.FormatFloat(value, 'f', -1, 64))
		if value < minValue {
			minValue = value
		}
		if value > maxValue {
			maxValue = value
		}
		total += value
		if i == 0 && timestamp-in.StartTime > cadence {
			gaps = append(gaps, HistoryGap{StartTime: in.StartTime, EndTime: timestamp})
		}
		// Allow one second of scheduler jitter; a missed sample remains a gap.
		if i > 0 && timestamp-times[i-1].Unix() > cadence+1 {
			gaps = append(gaps, HistoryGap{StartTime: times[i-1].Unix() + cadence, EndTime: timestamp})
		}
	}
	last := times[len(times)-1].Unix()
	if in.EndTime-last > cadence+1 {
		gaps = append(gaps, HistoryGap{StartTime: last + cadence, EndTime: in.EndTime})
	}
	avg, current := total/float64(len(values)), values[len(values)-1]
	data.DataStatistics = DataStatistics{Min: &minValue, Max: &maxValue, Avg: &avg, Current: &current}
	return data, gaps
}
