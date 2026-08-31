package usageevent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestTopModelsQueryUsesTimestampIndexBeforePricingMaterialization(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	rows, err := db.Query(`explain query plan `+topModelsSQL, int64(1_000), int64(2_000), 5)
	if err != nil {
		t.Fatalf("explain top models query: %v", err)
	}
	defer rows.Close()

	details := make([]string, 0, 8)
	usesTimestampIndex := false
	fullUsageScan := false
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
		usesTimestampIndex = usesTimestampIndex || strings.Contains(detail, "SEARCH usage_events USING INDEX idx_usage_events_timestamp")
		fullUsageScan = fullUsageScan || strings.Contains(detail, "SCAN usage_events")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	if !usesTimestampIndex || fullUsageScan {
		t.Fatalf("top models query did not constrain usage_events with the timestamp index: %v", details)
	}
}

// The status and cache filters add failed / cache-token conditions to the p95
// latency window scan. Those columns have to live in the latency scope index or
// every candidate row falls back to the wide usage_events row: measured 32s per
// 50d window uncovered vs 0.5s covered on an 18GB production database.
func TestLatencyBreakdownFilteredReadsStayCovered(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	for name, filter := range map[string]AnalyticsFilter{
		"success only": {FromMS: 1_000, ToMS: 2_000},
		"failed only":  {FromMS: 1_000, ToMS: 2_000, IncludeFailed: true, FailedOnly: true},
		"cache hit":    {FromMS: 1_000, ToMS: 2_000, IncludeFailed: true, CacheStatus: "hit"},
		"cache miss":   {FromMS: 1_000, ToMS: 2_000, IncludeFailed: true, CacheStatus: "miss"},
	} {
		query, args := latencyBreakdownQuery(filter)
		rows, err := db.Query(`explain query plan `+query, args...)
		if err != nil {
			t.Fatalf("%s: explain latency breakdown query: %v", name, err)
		}

		details := make([]string, 0, 8)
		covered := false
		rowLookup := false
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s: scan query plan: %v", name, err)
			}
			details = append(details, detail)
			covered = covered || strings.Contains(detail, "COVERING INDEX idx_usage_events_latency_scope_v3")
			rowLookup = rowLookup ||
				(strings.Contains(detail, " USING INDEX ") && !strings.Contains(detail, "COVERING")) ||
				strings.Contains(detail, "SCAN usage_events")
		}
		queryErr := rows.Err()
		rows.Close()
		if queryErr != nil {
			t.Fatalf("%s: query plan rows: %v", name, queryErr)
		}
		if !covered || rowLookup {
			t.Fatalf("%s: filtered latency read is not covered: %v", name, details)
		}
	}
}
