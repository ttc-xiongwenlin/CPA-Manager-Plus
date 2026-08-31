package errorinsight

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// maxWindowMS caps the query window at 14 days: the class breakdown is a
// short-horizon operational view, and the cap keeps the failed-row scan
// bounded no matter what the caller sends.
const maxWindowMS = 14 * 24 * 60 * 60 * 1000

const recentLimit = 50

// breakdownLimit caps how many distinct keys (providers or models) the
// by_provider / by_model breakdowns return; the controller truncates to the
// top keys by summed count and discards the rest.
const breakdownLimit = 10

type Handler struct {
	App *app.Context
}

type insightRequest struct {
	FromMS      int64          `json:"from_ms"`
	ToMS        int64          `json:"to_ms"`
	SearchQuery string         `json:"search_query"`
	Filters     insightFilters `json:"filters"`
}

type insightFilters struct {
	Models       []string `json:"models"`
	Providers    []string `json:"providers"`
	Accounts     []string `json:"accounts"`
	AuthFiles    []string `json:"auth_files"`
	AuthIndices  []string `json:"auth_indices"`
	APIKeyHashes []string `json:"api_key_hashes"`
	SourceHashes []string `json:"source_hashes"`
	MinLatencyMS int64    `json:"min_latency_ms"`
	BucketScope  bool     `json:"bucket_scope"`
}

// buildInsightFilter maps the request onto store.AnalyticsFilter. It
// deliberately leaves IncludeFailed and FailedOnly at their zero value: the
// error-class repo methods (see error_class.go) force failed=1 semantics
// internally, so this endpoint must not set them.
func buildInsightFilter(req insightRequest) store.AnalyticsFilter {
	return store.AnalyticsFilter{
		FromMS:       req.FromMS,
		ToMS:         req.ToMS,
		SearchQuery:  req.SearchQuery,
		Models:       req.Filters.Models,
		Providers:    req.Filters.Providers,
		Accounts:     req.Filters.Accounts,
		AuthFiles:    req.Filters.AuthFiles,
		AuthIndices:  req.Filters.AuthIndices,
		APIKeyHashes: req.Filters.APIKeyHashes,
		SourceHashes: req.Filters.SourceHashes,
		MinLatencyMS: req.Filters.MinLatencyMS,
		BucketScope:  req.Filters.BucketScope,
	}
}

type classItem struct {
	Class string `json:"class"`
	Count int64  `json:"count"`
}

type timelineItem struct {
	BucketMS int64  `json:"bucket_ms"`
	Class    string `json:"class"`
	Count    int64  `json:"count"`
}

type recentItem struct {
	Class       string `json:"class"`
	TimestampMS int64  `json:"timestamp_ms"`
	StatusCode  int64  `json:"status_code,omitempty"`
	Model       string `json:"model,omitempty"`
	Account     string `json:"account,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Summary     string `json:"summary,omitempty"`
	LatencyMS   int64  `json:"latency_ms,omitempty"`
}

type breakdownItem struct {
	Key   string `json:"key"`
	Class string `json:"class"`
	Count int64  `json:"count"`
}

type insightResponse struct {
	Classes    []classItem     `json:"classes"`
	Timeline   []timelineItem  `json:"timeline"`
	Recent     []recentItem    `json:"recent"`
	ByProvider []breakdownItem `json:"by_provider"`
	ByModel    []breakdownItem `json:"by_model"`
}

func validateWindow(fromMS, toMS int64) error {
	if fromMS <= 0 || toMS <= 0 {
		return errors.New("from_ms and to_ms are required")
	}
	if toMS <= fromMS {
		return errors.New("to_ms must be greater than from_ms")
	}
	if toMS-fromMS > maxWindowMS {
		return errors.New("window exceeds the 14 day maximum")
	}
	return nil
}

func buildResponse(
	stats []store.ErrorClassStat,
	timeline []store.ErrorClassTimelinePoint,
	recent []store.ErrorClassRecentFailure,
	providerBreakdown []store.ErrorClassBreakdownRow,
	modelBreakdown []store.ErrorClassBreakdownRow,
) insightResponse {
	out := insightResponse{
		Classes:  make([]classItem, 0, len(stats)),
		Timeline: make([]timelineItem, 0, len(timeline)),
		Recent:   make([]recentItem, 0, len(recent)),
	}
	for _, stat := range stats {
		out.Classes = append(out.Classes, classItem{Class: stat.Class, Count: stat.Count})
	}
	for _, point := range timeline {
		out.Timeline = append(out.Timeline, timelineItem{BucketMS: point.BucketMS, Class: point.Class, Count: point.Count})
	}
	for _, failure := range recent {
		item := recentItem{
			Class:       failure.Class,
			TimestampMS: failure.TimestampMS,
			Model:       failure.Model,
			Account:     failure.Account,
			Provider:    failure.Provider,
			Summary:     failure.Summary,
		}
		if failure.StatusCode.Valid {
			item.StatusCode = failure.StatusCode.Int64
		}
		if failure.LatencyMS.Valid {
			item.LatencyMS = failure.LatencyMS.Int64
		}
		out.Recent = append(out.Recent, item)
	}
	out.ByProvider = topBreakdownKeys(providerBreakdown, breakdownLimit)
	out.ByModel = topBreakdownKeys(modelBreakdown, breakdownLimit)
	return out
}

// topBreakdownKeys keeps only the rows belonging to the top `limit` keys by
// summed count, ordering keys by that total descending (ties keep the key's
// first-seen order in rows). All rows of a kept key stay together and adjacent
// in the output, and their relative order is preserved.
func topBreakdownKeys(rows []store.ErrorClassBreakdownRow, limit int) []breakdownItem {
	totals := make(map[string]int64, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		if _, seen := totals[row.Key]; !seen {
			order = append(order, row.Key)
		}
		totals[row.Key] += row.Count
	}

	sort.SliceStable(order, func(i, j int) bool {
		return totals[order[i]] > totals[order[j]]
	})
	if len(order) > limit {
		order = order[:limit]
	}

	kept := make(map[string]bool, len(order))
	for _, key := range order {
		kept[key] = true
	}

	rowsByKey := make(map[string][]store.ErrorClassBreakdownRow, len(order))
	for _, row := range rows {
		if kept[row.Key] {
			rowsByKey[row.Key] = append(rowsByKey[row.Key], row)
		}
	}

	out := make([]breakdownItem, 0, len(rows))
	for _, key := range order {
		for _, row := range rowsByKey[key] {
			out = append(out, breakdownItem{Key: row.Key, Class: row.Class, Count: row.Count})
		}
	}
	return out
}

// Handle serves the error insight page: class distribution, hourly class
// timeline, and recent samples for one bounded time window.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		response.MethodNotAllowed(w)
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}

	var req insightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := validateWindow(req.FromMS, req.ToMS); err != nil {
		response.Error(w, http.StatusBadRequest, err)
		return
	}

	filter := buildInsightFilter(req)
	ctx := r.Context()

	stats, err := h.App.Store.ErrorClassStatsWithFilter(ctx, filter)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	timeline, err := h.App.Store.ErrorClassTimelineWithFilter(ctx, filter)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	recent, err := h.App.Store.ErrorClassRecentWithFilter(ctx, filter, recentLimit)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	providerBreakdown, err := h.App.Store.ErrorClassBreakdownWithFilter(ctx, filter, "provider")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	modelBreakdown, err := h.App.Store.ErrorClassBreakdownWithFilter(ctx, filter, "model")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}

	response.JSON(w, http.StatusOK, buildResponse(stats, timeline, recent, providerBreakdown, modelBreakdown))
}
