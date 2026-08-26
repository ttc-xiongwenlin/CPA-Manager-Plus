package usagemonitoring

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	monitoringrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagemonitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const fallbackLogIntervalMS = int64(5 * time.Minute / time.Millisecond)

type Reader struct {
	store             *store.Store
	lastFallbackLogMS atomic.Int64
}

func New(store *store.Store) *Reader {
	return &Reader{store: store}
}

func SupportsStatsFilter(filter store.AnalyticsFilter) bool {
	return monitoringrepo.SupportsStatsFilter(filter)
}

func SupportsSelectorFilter(filter store.AnalyticsFilter) bool {
	return monitoringrepo.SupportsSelectorFilter(filter)
}

func PrefersEventProjection(filter store.AnalyticsFilter) bool {
	return monitoringrepo.PrefersEventProjection(filter)
}

func (r *Reader) AccountStats(ctx context.Context, filter store.AnalyticsFilter) ([]store.AccountModelStat, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringAccountStats(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("account daily rollup query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("account daily rollup unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return rows, true
}

func (r *Reader) AccountWindowStats(ctx context.Context, windows []store.AccountWindowUsageQuery) ([]store.AccountWindowModelStat, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringAccountWindowStats(ctx, windows)
	if err != nil {
		r.logFallback(fmt.Sprintf("account window projection query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("account window projection unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return rows, true
}

func (r *Reader) APIKeyStats(ctx context.Context, filter store.AnalyticsFilter) ([]store.APIKeyModelStat, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringAPIKeyStats(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("api key daily rollup query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("api key daily rollup unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return rows, true
}

func (r *Reader) FilterOptions(ctx context.Context, filter store.AnalyticsFilter) (store.FilterOptionValues, bool) {
	if r == nil || r.store == nil {
		return store.FilterOptionValues{}, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return store.FilterOptionValues{}, false
	}
	values, state, available, err := r.store.UsageMonitoringFilterOptions(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("filter option projection query failed: %v", err))
		return store.FilterOptionValues{}, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("filter option projection unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return store.FilterOptionValues{}, false
	}
	return values, true
}

func (r *Reader) FilterSelectors(ctx context.Context, filter store.AnalyticsFilter) (store.FilterSelectorValues, bool) {
	if r == nil || r.store == nil {
		return store.FilterSelectorValues{}, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return store.FilterSelectorValues{}, false
	}
	values, state, available, err := r.store.UsageMonitoringFilterSelectors(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("selector daily rollup query failed: %v", err))
		return store.FilterSelectorValues{}, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("selector daily rollup unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return store.FilterSelectorValues{}, false
	}
	return values, true
}

func (r *Reader) Aggregate(ctx context.Context, filter store.AnalyticsFilter) (store.Aggregate, bool) {
	if r == nil || r.store == nil {
		return store.Aggregate{}, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return store.Aggregate{}, false
	}
	aggregate, state, available, err := r.store.UsageMonitoringAggregate(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection aggregate query failed: %v", err))
		return store.Aggregate{}, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection aggregate unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return store.Aggregate{}, false
	}
	return aggregate, true
}

func (r *Reader) ModelStats(ctx context.Context, filter store.AnalyticsFilter) ([]store.ModelStat, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringModelStats(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection model stats query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection model stats unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return rows, true
}

// Timeline serves the analytics timeline from the event projection, which
// aggregates in SQLite instead of streaming every matching usage_events row.
// UTC hour rows are only remapped onto the requested granularity while each
// hour stays inside a single local bucket.
func (r *Reader) Timeline(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.TimelinePoint, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	if location == nil {
		location = time.UTC
	}
	if granularity != "day" {
		granularity = "hour"
	}
	if !usage.CanMapUTCWholeHours(floorHourMS(filter.FromMS), ceilHourMS(filter.ToMS), granularity, location) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringTimeline(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection timeline query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection timeline unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return timelineFromHourRows(rows, granularity, location), true
}

const timelineHourMS = int64(3600000)

func floorHourMS(value int64) int64 {
	return value - value%timelineHourMS
}

func ceilHourMS(value int64) int64 {
	floor := floorHourMS(value)
	if floor == value {
		return value
	}
	return floor + timelineHourMS
}

// APIKeyTimeline is Timeline with the API key kept as a grouping dimension.
func (r *Reader) APIKeyTimeline(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.APIKeyTimelinePoint, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	// The raw reader returns nothing for an unscoped request; declining here
	// keeps that contract instead of aggregating every key in the window.
	if len(filter.APIKeyHashes) == 0 && strings.TrimSpace(filter.SearchAPIKeyHash) == "" {
		return nil, false
	}
	if location == nil {
		location = time.UTC
	}
	if granularity != "day" {
		granularity = "hour"
	}
	if !usage.CanMapUTCWholeHours(floorHourMS(filter.FromMS), ceilHourMS(filter.ToMS), granularity, location) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringAPIKeyTimeline(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection api key timeline query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection api key timeline unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return apiKeyTimelineFromHourRows(rows, granularity, location), true
}

type apiKeyTimelineKey struct {
	apiKeyHash string
	timelineKey
}

type apiKeyTimelineAccumulator struct {
	point            store.APIKeyTimelinePoint
	latencySumMS     int64
	firstTimestampMS int64
}

func apiKeyTimelineFromHourRows(rows []store.UsageMonitoringAPIKeyTimelineHourRow, granularity string, location *time.Location) []store.APIKeyTimelinePoint {
	grouped := make(map[apiKeyTimelineKey]*apiKeyTimelineAccumulator, len(rows))
	order := make([]apiKeyTimelineKey, 0, len(rows))
	for _, row := range rows {
		key := apiKeyTimelineKey{
			apiKeyHash: row.APIKeyHash,
			timelineKey: timelineKey{
				bucketMS:               usage.AnalyticsBucketMS(row.BucketMS, granularity, location),
				model:                  row.Model,
				billingModel:           row.BillingModel,
				pricingModel:           row.PricingModel,
				serviceTier:            row.ServiceTier,
				contextThresholdTokens: row.ContextThresholdTokens,
			},
		}
		entry := grouped[key]
		if entry == nil {
			entry = &apiKeyTimelineAccumulator{
				point: store.APIKeyTimelinePoint{
					PricingBand:  row.PricingBand,
					APIKeyHash:   row.APIKeyHash,
					BucketMS:     key.bucketMS,
					Model:        row.Model,
					BillingModel: row.BillingModel,
					ServiceTier:  row.ServiceTier,
				},
			}
			entry.firstTimestampMS = row.FirstTimestampMS
			grouped[key] = entry
			order = append(order, key)
		} else if row.FirstTimestampMS < entry.firstTimestampMS {
			entry.firstTimestampMS = row.FirstTimestampMS
		}
		entry.point.Calls += row.Calls
		entry.point.Tokens += row.TotalTokens
		entry.point.Success += row.SuccessCalls
		entry.point.Failure += row.FailureCalls
		entry.point.InputTokens += row.InputTokens
		entry.point.OutputTokens += row.OutputTokens
		entry.point.ReasoningTokens += row.ReasoningTokens
		entry.point.CachedTokens += row.CachedTokens
		entry.point.CacheReadTokens += row.CacheReadTokens
		entry.point.CacheCreationTokens += row.CacheCreationTokens
		entry.point.LongInputTokens += row.LongInputTokens
		entry.point.LongOutputTokens += row.LongOutputTokens
		entry.point.LongCachedTokens += row.LongCachedTokens
		entry.point.LongCacheReadTokens += row.LongCacheReadTokens
		entry.point.LongCacheCreationTokens += row.LongCacheCreationTokens
		entry.point.LatencySamples += row.LatencySamples
		entry.latencySumMS += row.LatencySumMS
	}

	// Match the raw reader, which emits groups in earliest-event order.
	sort.SliceStable(order, func(i, j int) bool {
		left, right := grouped[order[i]], grouped[order[j]]
		if left.firstTimestampMS != right.firstTimestampMS {
			return left.firstTimestampMS < right.firstTimestampMS
		}
		if order[i].apiKeyHash != order[j].apiKeyHash {
			return order[i].apiKeyHash < order[j].apiKeyHash
		}
		return order[i].model < order[j].model
	})

	points := make([]store.APIKeyTimelinePoint, 0, len(order))
	for _, key := range order {
		entry := grouped[key]
		if entry.point.LatencySamples > 0 {
			entry.point.AvgLatencyMS.Valid = true
			entry.point.AvgLatencyMS.Float64 = float64(entry.latencySumMS) / float64(entry.point.LatencySamples)
		}
		points = append(points, entry.point)
	}
	return points
}

// CredentialTimeline is Timeline with the credential identity kept as
// grouping dimensions.
func (r *Reader) CredentialTimeline(ctx context.Context, filter store.AnalyticsFilter, granularity string, location *time.Location) ([]store.CredentialTimelinePoint, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) {
		return nil, false
	}
	if location == nil {
		location = time.UTC
	}
	if granularity != "day" {
		granularity = "hour"
	}
	if !usage.CanMapUTCWholeHours(floorHourMS(filter.FromMS), ceilHourMS(filter.ToMS), granularity, location) {
		return nil, false
	}
	rows, state, available, err := r.store.UsageMonitoringCredentialTimeline(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection credential timeline query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection credential timeline unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return credentialTimelineFromHourRows(rows, granularity, location), true
}

type credentialTimelineKey struct {
	id                    string
	authFileSnapshot      string
	authIndex             string
	source                string
	sourceHash            string
	accountSnapshot       string
	authLabelSnapshot     string
	authProviderSnapshot  string
	authProjectIDSnapshot string
	timelineKey
}

type credentialTimelineAccumulator struct {
	point            store.CredentialTimelinePoint
	latencySumMS     int64
	firstTimestampMS int64
}

func credentialTimelineFromHourRows(rows []store.UsageMonitoringCredentialTimelineHourRow, granularity string, location *time.Location) []store.CredentialTimelinePoint {
	grouped := make(map[credentialTimelineKey]*credentialTimelineAccumulator, len(rows))
	order := make([]credentialTimelineKey, 0, len(rows))
	for _, row := range rows {
		key := credentialTimelineKey{
			id:                    row.ID,
			authFileSnapshot:      row.AuthFileSnapshot,
			authIndex:             row.AuthIndex,
			source:                row.Source,
			sourceHash:            row.SourceHash,
			accountSnapshot:       row.AccountSnapshot,
			authLabelSnapshot:     row.AuthLabelSnapshot,
			authProviderSnapshot:  row.AuthProviderSnapshot,
			authProjectIDSnapshot: row.AuthProjectIDSnapshot,
			timelineKey: timelineKey{
				bucketMS:               usage.AnalyticsBucketMS(row.BucketMS, granularity, location),
				model:                  row.Model,
				billingModel:           row.BillingModel,
				pricingModel:           row.PricingModel,
				serviceTier:            row.ServiceTier,
				contextThresholdTokens: row.ContextThresholdTokens,
			},
		}
		entry := grouped[key]
		if entry == nil {
			entry = &credentialTimelineAccumulator{
				point: store.CredentialTimelinePoint{
					PricingBand:           row.PricingBand,
					ID:                    row.ID,
					AuthFileSnapshot:      row.AuthFileSnapshot,
					AuthIndex:             row.AuthIndex,
					Source:                row.Source,
					SourceHash:            row.SourceHash,
					AccountSnapshot:       row.AccountSnapshot,
					AuthLabelSnapshot:     row.AuthLabelSnapshot,
					AuthProviderSnapshot:  row.AuthProviderSnapshot,
					AuthProjectIDSnapshot: row.AuthProjectIDSnapshot,
					BucketMS:              key.bucketMS,
					Model:                 row.Model,
					BillingModel:          row.BillingModel,
					ServiceTier:           row.ServiceTier,
				},
			}
			entry.firstTimestampMS = row.FirstTimestampMS
			grouped[key] = entry
			order = append(order, key)
		} else if row.FirstTimestampMS < entry.firstTimestampMS {
			entry.firstTimestampMS = row.FirstTimestampMS
		}
		entry.point.Calls += row.Calls
		entry.point.Tokens += row.TotalTokens
		entry.point.Success += row.SuccessCalls
		entry.point.Failure += row.FailureCalls
		entry.point.InputTokens += row.InputTokens
		entry.point.OutputTokens += row.OutputTokens
		entry.point.ReasoningTokens += row.ReasoningTokens
		entry.point.CachedTokens += row.CachedTokens
		entry.point.CacheReadTokens += row.CacheReadTokens
		entry.point.CacheCreationTokens += row.CacheCreationTokens
		entry.point.LongInputTokens += row.LongInputTokens
		entry.point.LongOutputTokens += row.LongOutputTokens
		entry.point.LongCachedTokens += row.LongCachedTokens
		entry.point.LongCacheReadTokens += row.LongCacheReadTokens
		entry.point.LongCacheCreationTokens += row.LongCacheCreationTokens
		entry.point.LatencySamples += row.LatencySamples
		entry.latencySumMS += row.LatencySumMS
	}

	// Match the raw reader, which emits groups in earliest-event order.
	sort.SliceStable(order, func(i, j int) bool {
		left, right := grouped[order[i]], grouped[order[j]]
		if left.firstTimestampMS != right.firstTimestampMS {
			return left.firstTimestampMS < right.firstTimestampMS
		}
		if order[i].id != order[j].id {
			return order[i].id < order[j].id
		}
		return order[i].model < order[j].model
	})

	points := make([]store.CredentialTimelinePoint, 0, len(order))
	for _, key := range order {
		entry := grouped[key]
		if entry.point.LatencySamples > 0 {
			entry.point.AvgLatencyMS.Valid = true
			entry.point.AvgLatencyMS.Float64 = float64(entry.latencySumMS) / float64(entry.point.LatencySamples)
		}
		points = append(points, entry.point)
	}
	return points
}

type timelineKey struct {
	bucketMS               int64
	model                  string
	billingModel           string
	pricingModel           string
	serviceTier            string
	contextThresholdTokens int64
}

type timelineAccumulator struct {
	point        store.TimelinePoint
	latencySumMS int64
}

func timelineFromHourRows(rows []store.UsageMonitoringTimelineHourRow, granularity string, location *time.Location) []store.TimelinePoint {
	grouped := make(map[timelineKey]*timelineAccumulator, len(rows))
	order := make([]timelineKey, 0, len(rows))
	for _, row := range rows {
		key := timelineKey{
			bucketMS:               usage.AnalyticsBucketMS(row.BucketMS, granularity, location),
			model:                  row.Model,
			billingModel:           row.BillingModel,
			pricingModel:           row.PricingModel,
			serviceTier:            row.ServiceTier,
			contextThresholdTokens: row.ContextThresholdTokens,
		}
		entry := grouped[key]
		if entry == nil {
			entry = &timelineAccumulator{
				point: store.TimelinePoint{
					PricingBand:  row.PricingBand,
					BucketMS:     key.bucketMS,
					Model:        row.Model,
					BillingModel: row.BillingModel,
					ServiceTier:  row.ServiceTier,
				},
			}
			grouped[key] = entry
			order = append(order, key)
		}
		entry.point.Calls += row.Calls
		entry.point.Tokens += row.TotalTokens
		entry.point.Success += row.SuccessCalls
		entry.point.Failure += row.FailureCalls
		entry.point.InputTokens += row.InputTokens
		entry.point.OutputTokens += row.OutputTokens
		entry.point.ReasoningTokens += row.ReasoningTokens
		entry.point.CachedTokens += row.CachedTokens
		entry.point.CacheReadTokens += row.CacheReadTokens
		entry.point.CacheCreationTokens += row.CacheCreationTokens
		entry.point.LongInputTokens += row.LongInputTokens
		entry.point.LongOutputTokens += row.LongOutputTokens
		entry.point.LongCachedTokens += row.LongCachedTokens
		entry.point.LongCacheReadTokens += row.LongCacheReadTokens
		entry.point.LongCacheCreationTokens += row.LongCacheCreationTokens
		entry.point.LatencySamples += row.LatencySamples
		entry.latencySumMS += row.LatencySumMS
	}

	points := make([]store.TimelinePoint, 0, len(order))
	for _, key := range order {
		entry := grouped[key]
		if entry.point.LatencySamples > 0 {
			entry.point.AvgLatencyMS.Valid = true
			entry.point.AvgLatencyMS.Float64 = float64(entry.latencySumMS) / float64(entry.point.LatencySamples)
		}
		points = append(points, entry.point)
	}
	return points
}

func (r *Reader) EventsCount(ctx context.Context, filter store.AnalyticsFilter) (int64, bool) {
	if r == nil || r.store == nil {
		return 0, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) || !monitoringrepo.PrefersEventProjection(filter) {
		return 0, false
	}
	total, state, available, err := r.store.UsageMonitoringEventsCount(ctx, filter)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection count query failed: %v", err))
		return 0, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection count unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return 0, false
	}
	return total, true
}

func (r *Reader) EventsPage(ctx context.Context, filter store.AnalyticsFilter, beforeMS, beforeID int64, limit int) (store.EventsPage, bool) {
	if r == nil || r.store == nil {
		return store.EventsPage{}, false
	}
	if !monitoringrepo.SupportsEventProjectionFilter(filter) || !monitoringrepo.PrefersEventProjection(filter) {
		return store.EventsPage{}, false
	}
	page, state, available, err := r.store.UsageMonitoringEventsPage(ctx, filter, beforeMS, beforeID, limit)
	if err != nil {
		r.logFallback(fmt.Sprintf("event projection page query failed: %v", err))
		return store.EventsPage{}, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("event projection page unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return store.EventsPage{}, false
	}
	return page, true
}

func (r *Reader) HeaderSnapshots(ctx context.Context, sinceMS int64, limit int) ([]store.HeaderSnapshot, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	items, state, available, err := r.store.UsageMonitoringHeaderSnapshots(ctx, sinceMS, limit)
	if err != nil {
		r.logFallback(fmt.Sprintf("header latest rollup query failed: %v", err))
		return nil, false
	}
	if !available {
		r.logFallback(fmt.Sprintf("header latest rollup unavailable: schema_version=%d status=%s", state.SchemaVersion, state.Status))
		return nil, false
	}
	return items, true
}

func (r *Reader) logFallback(message string) {
	nowMS := time.Now().UnixMilli()
	lastMS := r.lastFallbackLogMS.Load()
	if lastMS > 0 && nowMS-lastMS < fallbackLogIntervalMS {
		return
	}
	if r.lastFallbackLogMS.CompareAndSwap(lastMS, nowMS) {
		log.Printf("[usage-monitoring] %s; falling back to usage_events", message)
	}
}
