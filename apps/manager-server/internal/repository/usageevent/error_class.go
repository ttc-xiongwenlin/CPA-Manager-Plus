package usageevent

import (
	"context"
	"database/sql"
	"fmt"
)

// errorClassExpression buckets one failed usage event into a stable error
// class. Order matters: body text patterns outrank the raw status code
// because upstreams reuse codes for unrelated conditions — codex returns
// 429 for server_is_overloaded, and the proxy surfaces client cancellation
// and its own transport errors as 500. json_extract is deliberately
// avoided: rows written before the upstream fail-body fix carry response
// headers appended after the JSON body, so LIKE is the only parser that
// holds across the full history.
const errorClassExpression = `case
	when fail_summary like '%usage_limit_reached%' or fail_summary like '%usage limit has been reached%' then 'quota_exhausted'
	when fail_summary like '%server_is_overloaded%' or fail_summary like '%service_unavailable%' or fail_status_code = 503 then 'upstream_overloaded'
	when fail_summary like '%rpm_limit_exceeded%' or fail_summary like '%Rate limit exceeded%' or fail_status_code = 429 then 'rate_limited'
	when fail_status_code in (401, 403) or fail_summary like '%authentication_error%' or fail_summary like '%token has been invalidated%' or header_error_kind = 'auth' then 'auth'
	when fail_summary like '%context canceled%' or fail_status_code = 499 then 'client_canceled'
	when fail_summary like '%stream error%' or fail_summary like '%stream disconnected%' or fail_summary like '%stream closed%' then 'stream_aborted'
	when fail_summary like '%no such host%' or fail_summary like '%dial tcp%' or fail_summary like '%read tcp%' or fail_summary like '%connection termination%' or fail_summary like '%connect error%' or fail_summary like '%connection refused%' or fail_summary like '%connection reset%' or fail_summary like '%connection timeout%' then 'network'
	when fail_status_code in (408, 504, 524) or fail_summary like '%timeout%' or fail_summary like '%timed out%' then 'timeout'
	when fail_summary like '%context window%' or fail_summary like '%invalid_request_error%' or fail_status_code in (400, 404, 422) then 'invalid_request'
	when fail_status_code >= 500 then 'upstream_error'
	else 'other'
end`

// ErrorClassStat is one error class with its event count inside the window.
type ErrorClassStat struct {
	Class string
	Count int64
}

func (r *repository) ErrorClassStatsWithFilter(ctx context.Context, filter AnalyticsFilter) ([]ErrorClassStat, error) {
	// Make a copy to avoid modifying caller's filter
	f := filter
	f.IncludeFailed = true // Prevent analyticsWhere from adding "failed = 0"

	where, args := analyticsWhere(f)
	rows, err := r.db.QueryContext(ctx, `select `+errorClassExpression+` as class, count(*)
from usage_events `+where+` and failed = 1
group by class
order by count(*) desc`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]ErrorClassStat, 0)
	for rows.Next() {
		var stat ErrorClassStat
		if err := rows.Scan(&stat.Class, &stat.Count); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

// ErrorClassTimelinePoint is one (UTC hour bucket, class) cell of the error
// timeline. Hour granularity is fine for the 14-day window cap (<= 337
// buckets).
type ErrorClassTimelinePoint struct {
	BucketMS int64
	Class    string
	Count    int64
}

func (r *repository) ErrorClassTimelineWithFilter(ctx context.Context, filter AnalyticsFilter) ([]ErrorClassTimelinePoint, error) {
	f := filter
	f.IncludeFailed = true // Prevent analyticsWhere from adding "failed = 0"
	where, args := analyticsWhere(f)
	rows, err := r.db.QueryContext(ctx, `select
	timestamp_ms / 3600000 * 3600000 as bucket_ms,
	`+errorClassExpression+` as class,
	count(*)
from usage_events `+where+` and failed = 1
group by bucket_ms, class
order by bucket_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]ErrorClassTimelinePoint, 0)
	for rows.Next() {
		var point ErrorClassTimelinePoint
		if err := rows.Scan(&point.BucketMS, &point.Class, &point.Count); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

// ErrorClassRecentFailure is one recent failed event with its class label,
// trimmed to what the error insight page renders.
type ErrorClassRecentFailure struct {
	Class       string
	TimestampMS int64
	StatusCode  sql.NullInt64
	Model       string
	Account     string
	Provider    string
	Summary     string
	LatencyMS   sql.NullInt64
}

func (r *repository) ErrorClassRecentWithFilter(ctx context.Context, filter AnalyticsFilter, limit int) ([]ErrorClassRecentFailure, error) {
	if limit <= 0 {
		limit = 50
	}
	f := filter
	f.IncludeFailed = true // Prevent analyticsWhere from adding "failed = 0"
	where, args := analyticsWhere(f)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `select
	`+errorClassExpression+` as class,
	timestamp_ms,
	fail_status_code,
	coalesce(nullif(requested_model, ''), model),
	coalesce(account_snapshot, ''),
	coalesce(nullif(auth_provider_snapshot, ''), coalesce(provider, '')),
	coalesce(substr(fail_summary, 1, 300), ''),
	latency_ms
from usage_events `+where+` and failed = 1
order by timestamp_ms desc, id desc
limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	failures := make([]ErrorClassRecentFailure, 0, limit)
	for rows.Next() {
		var failure ErrorClassRecentFailure
		if err := rows.Scan(
			&failure.Class,
			&failure.TimestampMS,
			&failure.StatusCode,
			&failure.Model,
			&failure.Account,
			&failure.Provider,
			&failure.Summary,
			&failure.LatencyMS,
		); err != nil {
			return nil, err
		}
		failures = append(failures, failure)
	}
	return failures, rows.Err()
}

// ErrorClassBreakdownRow is one (dimension key, class) cell.
type ErrorClassBreakdownRow struct {
	Key   string
	Class string
	Count int64
}

// errorClassBreakdownKeyExpressions maps a supported breakdown dimension to
// the SQL expression that derives its key, mirroring the coalesce fallbacks
// used by the recent-failures query above.
var errorClassBreakdownKeyExpressions = map[string]string{
	"provider": `coalesce(nullif(auth_provider_snapshot, ''), coalesce(provider, ''))`,
	"model":    `coalesce(nullif(requested_model, ''), model)`,
}

func (r *repository) ErrorClassBreakdownWithFilter(ctx context.Context, filter AnalyticsFilter, dimension string) ([]ErrorClassBreakdownRow, error) {
	keyExpr, ok := errorClassBreakdownKeyExpressions[dimension]
	if !ok {
		return nil, fmt.Errorf("unsupported error class breakdown dimension: %q", dimension)
	}
	f := filter
	f.IncludeFailed = true // Prevent analyticsWhere from adding "failed = 0"
	where, args := analyticsWhere(f)
	rows, err := r.db.QueryContext(ctx, `select `+keyExpr+` as k, `+errorClassExpression+` as class, count(*)
from usage_events `+where+` and failed = 1
group by k, class
order by k`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	breakdown := make([]ErrorClassBreakdownRow, 0)
	for rows.Next() {
		var row ErrorClassBreakdownRow
		if err := rows.Scan(&row.Key, &row.Class, &row.Count); err != nil {
			return nil, err
		}
		breakdown = append(breakdown, row)
	}
	return breakdown, rows.Err()
}
