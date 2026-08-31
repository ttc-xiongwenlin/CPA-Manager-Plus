package usageevent

import (
	"context"
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
	f.IncludeFailed = true  // Prevent analyticsWhere from adding "failed = 0"

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
