package usagemonitoring

import (
	"context"
	"database/sql"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usageidentity"
)

const timelineHourMS = int64(3600000)

// TimelineHourRow carries one UTC hour of timeline totals for a pricing band.
// Callers remap BucketMS onto the requested local granularity, which is only
// exact while every UTC hour lands inside a single local bucket.
type TimelineHourRow struct {
	usage.LongContextTokens
	usage.PricingBand
	BucketMS int64
	// FirstTimestampMS orders groups the way the raw readers do, by the
	// earliest event in the group rather than by bucket.
	FirstTimestampMS    int64
	Model               string
	BillingModel        string
	ServiceTier         string
	Calls               int64
	SuccessCalls        int64
	FailureCalls        int64
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalTokens         int64
	LatencySumMS        int64
	LatencySamples      int64
}

// APIKeyTimelineHourRow is TimelineHourRow scoped to one API key.
type APIKeyTimelineHourRow struct {
	TimelineHourRow
	APIKeyHash string
}

// LoadTimeline aggregates the timeline in SQLite over the narrow event
// projection instead of streaming every wide usage_events row into Go.
func (r *repository) LoadTimeline(ctx context.Context, filter AnalyticsFilter) ([]TimelineHourRow, State, bool, error) {
	if !SupportsEventProjectionFilter(filter) {
		return nil, State{}, false, nil
	}
	if filter.FromMS >= filter.ToMS {
		return []TimelineHourRow{}, State{}, true, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, State{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	state, available, projectionComplete, err := projectionReadState(ctx, tx)
	if err != nil || !available {
		return nil, state, available, err
	}

	source, args := filteredEventSourceSQL(
		filter,
		state.CoverageEventID,
		`p.timestamp_ms, p.requested_model as model, p.analytics_model, p.resolved_model, p.service_tier, p.failed,
		p.normalized_total_input_tokens, p.output_tokens, p.reasoning_tokens,
		p.cached_tokens, p.cache_tokens, p.cache_read_tokens,
		p.cache_creation_tokens, p.total_tokens, p.latency_ms`,
		`e.timestamp_ms, `+usageidentity.SQLEffectiveRequestedModelExpression("e.model", "e.requested_model")+`, `+usageidentity.SQLRequestAnalyticsModelExpression("e.model", "e.requested_model")+`, coalesce(e.resolved_model, ''),
		coalesce(e.service_tier, ''), coalesce(e.failed, 0),
		coalesce(e.normalized_total_input_tokens, e.input_tokens, 0),
		coalesce(e.output_tokens, 0), coalesce(e.reasoning_tokens, 0),
		coalesce(e.cached_tokens, 0), coalesce(e.cache_tokens, 0),
		coalesce(e.cache_read_tokens, 0), coalesce(e.cache_creation_tokens, 0),
		coalesce(e.total_tokens, 0), e.latency_ms`,
		eventSourceOptions{ProjectionComplete: projectionComplete},
	)
	query := monitoringBandedProjectedEventsCTE(source) + `
	select
		timestamp_ms / 3600000 * 3600000 as bucket_ms,
		analytics_model,
		billing_model_value,
		pricing_model_value,
		context_threshold_tokens_value,
		coalesce(service_tier, ''),
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed = 1 then 1 else 0 end), 0),
		coalesce(sum(normalized_input_tokens_value), 0),
		coalesce(sum(output_tokens), 0),
		coalesce(sum(reasoning_tokens), 0),
		coalesce(sum(compatible_cached_tokens_value), 0),
		coalesce(sum(cache_read_tokens), 0),
		coalesce(sum(cache_creation_tokens), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then normalized_input_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then output_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then compatible_cached_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_read_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_creation_tokens else 0 end), 0),
		coalesce(sum(total_tokens), 0),
		coalesce(sum(case when latency_ms is not null and latency_ms != 0 then latency_ms else 0 end), 0),
		count(nullif(latency_ms, 0)),
		min(timestamp_ms)
	from banded_events
	group by bucket_ms, analytics_model, billing_model_value, pricing_model_value,
		context_threshold_tokens_value, coalesce(service_tier, '')
	order by bucket_ms, analytics_model`
	args = appendLongContextThresholdArgs(args)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, state, false, err
	}
	defer rows.Close()

	result := make([]TimelineHourRow, 0)
	for rows.Next() {
		var row TimelineHourRow
		if err := rows.Scan(
			&row.BucketMS,
			&row.Model,
			&row.BillingModel,
			&row.PricingModel,
			&row.ContextThresholdTokens,
			&row.ServiceTier,
			&row.Calls,
			&row.SuccessCalls,
			&row.FailureCalls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.LatencySumMS,
			&row.LatencySamples,
			&row.FirstTimestampMS,
		); err != nil {
			return nil, state, false, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, state, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, state, false, err
	}
	return result, state, true, nil
}

// LoadAPIKeyTimeline mirrors LoadTimeline with the API key carried as an extra
// grouping dimension.
func (r *repository) LoadAPIKeyTimeline(ctx context.Context, filter AnalyticsFilter) ([]APIKeyTimelineHourRow, State, bool, error) {
	if !SupportsEventProjectionFilter(filter) {
		return nil, State{}, false, nil
	}
	if filter.FromMS >= filter.ToMS {
		return []APIKeyTimelineHourRow{}, State{}, true, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, State{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	state, available, projectionComplete, err := projectionReadState(ctx, tx)
	if err != nil || !available {
		return nil, state, available, err
	}

	source, args := filteredEventSourceSQL(
		filter,
		state.CoverageEventID,
		`p.timestamp_ms, p.api_key_hash, p.requested_model as model, p.analytics_model, p.resolved_model, p.service_tier, p.failed,
		p.normalized_total_input_tokens, p.output_tokens, p.reasoning_tokens,
		p.cached_tokens, p.cache_tokens, p.cache_read_tokens,
		p.cache_creation_tokens, p.total_tokens, p.latency_ms`,
		`e.timestamp_ms, coalesce(e.api_key_hash, ''), `+usageidentity.SQLEffectiveRequestedModelExpression("e.model", "e.requested_model")+`, `+usageidentity.SQLRequestAnalyticsModelExpression("e.model", "e.requested_model")+`,
		coalesce(e.resolved_model, ''), coalesce(e.service_tier, ''), coalesce(e.failed, 0),
		coalesce(e.normalized_total_input_tokens, e.input_tokens, 0),
		coalesce(e.output_tokens, 0), coalesce(e.reasoning_tokens, 0),
		coalesce(e.cached_tokens, 0), coalesce(e.cache_tokens, 0),
		coalesce(e.cache_read_tokens, 0), coalesce(e.cache_creation_tokens, 0),
		coalesce(e.total_tokens, 0), e.latency_ms`,
		eventSourceOptions{ProjectionComplete: projectionComplete},
	)
	query := monitoringBandedProjectedEventsCTE(source) + `
	select
		timestamp_ms / 3600000 * 3600000 as bucket_ms,
		coalesce(api_key_hash, ''),
		analytics_model,
		billing_model_value,
		pricing_model_value,
		context_threshold_tokens_value,
		coalesce(service_tier, ''),
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed = 1 then 1 else 0 end), 0),
		coalesce(sum(normalized_input_tokens_value), 0),
		coalesce(sum(output_tokens), 0),
		coalesce(sum(reasoning_tokens), 0),
		coalesce(sum(compatible_cached_tokens_value), 0),
		coalesce(sum(cache_read_tokens), 0),
		coalesce(sum(cache_creation_tokens), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then normalized_input_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then output_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then compatible_cached_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_read_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_creation_tokens else 0 end), 0),
		coalesce(sum(total_tokens), 0),
		coalesce(sum(case when latency_ms is not null and latency_ms != 0 then latency_ms else 0 end), 0),
		count(nullif(latency_ms, 0)),
		min(timestamp_ms)
	from banded_events
	group by bucket_ms, api_key_hash, analytics_model, billing_model_value, pricing_model_value,
		context_threshold_tokens_value, coalesce(service_tier, '')
	order by min(timestamp_ms), api_key_hash, analytics_model`
	args = appendLongContextThresholdArgs(args)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, state, false, err
	}
	defer rows.Close()

	result := make([]APIKeyTimelineHourRow, 0)
	for rows.Next() {
		var row APIKeyTimelineHourRow
		if err := rows.Scan(
			&row.BucketMS,
			&row.APIKeyHash,
			&row.Model,
			&row.BillingModel,
			&row.PricingModel,
			&row.ContextThresholdTokens,
			&row.ServiceTier,
			&row.Calls,
			&row.SuccessCalls,
			&row.FailureCalls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.LatencySumMS,
			&row.LatencySamples,
			&row.FirstTimestampMS,
		); err != nil {
			return nil, state, false, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, state, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, state, false, err
	}
	return result, state, true, nil
}

// CredentialTimelineHourRow is TimelineHourRow scoped to one credential, with
// the identity labels the timeline presents alongside each group.
type CredentialTimelineHourRow struct {
	TimelineHourRow
	ID                    string
	AuthFileSnapshot      string
	AuthIndex             string
	Source                string
	SourceHash            string
	AccountSnapshot       string
	AuthLabelSnapshot     string
	AuthProviderSnapshot  string
	AuthProjectIDSnapshot string
}

// LoadCredentialTimeline mirrors LoadTimeline with the credential identity
// carried as extra grouping dimensions.
func (r *repository) LoadCredentialTimeline(ctx context.Context, filter AnalyticsFilter) ([]CredentialTimelineHourRow, State, bool, error) {
	if !SupportsEventProjectionFilter(filter) {
		return nil, State{}, false, nil
	}
	if filter.FromMS >= filter.ToMS {
		return []CredentialTimelineHourRow{}, State{}, true, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, State{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	state, available, projectionComplete, err := projectionReadState(ctx, tx)
	if err != nil || !available {
		return nil, state, available, err
	}

	source, args := filteredEventSourceSQL(
		filter,
		state.CoverageEventID,
		`p.timestamp_ms, p.auth_file_snapshot, p.auth_index, p.source, p.source_hash,
		p.account_snapshot, p.auth_label_snapshot, p.provider, p.auth_provider_snapshot,
		p.auth_project_id_snapshot, p.requested_model as model, p.analytics_model,
		p.resolved_model, p.service_tier, p.failed,
		p.normalized_total_input_tokens, p.output_tokens, p.reasoning_tokens,
		p.cached_tokens, p.cache_tokens, p.cache_read_tokens,
		p.cache_creation_tokens, p.total_tokens, p.latency_ms`,
		`e.timestamp_ms, coalesce(e.auth_file_snapshot, ''), coalesce(e.auth_index, ''),
		coalesce(e.source, ''), coalesce(e.source_hash, ''),
		coalesce(e.account_snapshot, ''), coalesce(e.auth_label_snapshot, ''),
		coalesce(e.provider, ''), coalesce(e.auth_provider_snapshot, ''),
		coalesce(e.auth_project_id_snapshot, ''), `+usageidentity.SQLEffectiveRequestedModelExpression("e.model", "e.requested_model")+`,
		`+usageidentity.SQLRequestAnalyticsModelExpression("e.model", "e.requested_model")+`, coalesce(e.resolved_model, ''), coalesce(e.service_tier, ''), coalesce(e.failed, 0),
		coalesce(e.normalized_total_input_tokens, e.input_tokens, 0),
		coalesce(e.output_tokens, 0), coalesce(e.reasoning_tokens, 0),
		coalesce(e.cached_tokens, 0), coalesce(e.cache_tokens, 0),
		coalesce(e.cache_read_tokens, 0), coalesce(e.cache_creation_tokens, 0),
		coalesce(e.total_tokens, 0), e.latency_ms`,
		eventSourceOptions{ProjectionComplete: projectionComplete},
	)
	const credentialIDExpr = `coalesce(nullif(auth_file_snapshot, ''), nullif(auth_index, ''), nullif(source_hash, ''), nullif(source, ''), '-')`
	query := monitoringBandedProjectedEventsCTE(source) + `
	select
		timestamp_ms / 3600000 * 3600000 as bucket_ms,
		` + credentialIDExpr + ` as credential_id,
		coalesce(auth_file_snapshot, ''),
		coalesce(auth_index, ''),
		coalesce(source, ''),
		coalesce(source_hash, ''),
		coalesce(account_snapshot, ''),
		coalesce(auth_label_snapshot, ''),
		coalesce(nullif(auth_provider_snapshot, ''), provider, ''),
		coalesce(auth_project_id_snapshot, ''),
		analytics_model,
		billing_model_value,
		pricing_model_value,
		context_threshold_tokens_value,
		coalesce(service_tier, ''),
		count(*),
		coalesce(sum(case when failed = 0 then 1 else 0 end), 0),
		coalesce(sum(case when failed = 1 then 1 else 0 end), 0),
		coalesce(sum(normalized_input_tokens_value), 0),
		coalesce(sum(output_tokens), 0),
		coalesce(sum(reasoning_tokens), 0),
		coalesce(sum(compatible_cached_tokens_value), 0),
		coalesce(sum(cache_read_tokens), 0),
		coalesce(sum(cache_creation_tokens), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then normalized_input_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then output_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then compatible_cached_tokens_value else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_read_tokens else 0 end), 0),
		coalesce(sum(case when normalized_input_tokens_value > ? then cache_creation_tokens else 0 end), 0),
		coalesce(sum(total_tokens), 0),
		coalesce(sum(case when latency_ms is not null and latency_ms != 0 then latency_ms else 0 end), 0),
		count(nullif(latency_ms, 0)),
		min(timestamp_ms)
	from banded_events
	group by bucket_ms, credential_id, auth_file_snapshot, auth_index, source, source_hash,
		account_snapshot, auth_label_snapshot,
		coalesce(nullif(auth_provider_snapshot, ''), provider, ''), auth_project_id_snapshot,
		analytics_model, billing_model_value, pricing_model_value,
		context_threshold_tokens_value, coalesce(service_tier, '')
	order by min(timestamp_ms), credential_id, analytics_model`
	args = appendLongContextThresholdArgs(args)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, state, false, err
	}
	defer rows.Close()

	result := make([]CredentialTimelineHourRow, 0)
	for rows.Next() {
		var row CredentialTimelineHourRow
		if err := rows.Scan(
			&row.BucketMS,
			&row.ID,
			&row.AuthFileSnapshot,
			&row.AuthIndex,
			&row.Source,
			&row.SourceHash,
			&row.AccountSnapshot,
			&row.AuthLabelSnapshot,
			&row.AuthProviderSnapshot,
			&row.AuthProjectIDSnapshot,
			&row.Model,
			&row.BillingModel,
			&row.PricingModel,
			&row.ContextThresholdTokens,
			&row.ServiceTier,
			&row.Calls,
			&row.SuccessCalls,
			&row.FailureCalls,
			&row.InputTokens,
			&row.OutputTokens,
			&row.ReasoningTokens,
			&row.CachedTokens,
			&row.CacheReadTokens,
			&row.CacheCreationTokens,
			&row.LongInputTokens,
			&row.LongOutputTokens,
			&row.LongCachedTokens,
			&row.LongCacheReadTokens,
			&row.LongCacheCreationTokens,
			&row.TotalTokens,
			&row.LatencySumMS,
			&row.LatencySamples,
			&row.FirstTimestampMS,
		); err != nil {
			return nil, state, false, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, state, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, state, false, err
	}
	return result, state, true, nil
}
