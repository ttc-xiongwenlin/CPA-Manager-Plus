package usagemonitoring

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

// The rollup readers fetch the events past the rollup coverage by event id, and
// that range is usually empty. Left to the planner, the provider-scoped tail
// walked the 844MB scope index over the whole window instead: 9.5s per 30-day
// read on production for zero rows.
func TestProjectionTailReadsByEventIDRange(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	filter := AnalyticsFilter{
		FromMS:        1_000,
		ToMS:          2_000,
		IncludeFailed: true,
		Providers:     []string{"openai-compatible-deepseek"},
	}
	selectList := `p.failed, p.total_tokens, p.latency_ms`
	plan := func(coverageEventID, afterID int64) string {
		t.Helper()
		query, args := filteredEventSourceSQL(filter, coverageEventID, selectList, selectList,
			eventSourceOptions{AfterID: afterID, UseAfter: true, ProjectionComplete: true})
		rows, err := db.Query(`explain query plan `+query, args...)
		if err != nil {
			t.Fatalf("explain projection tail: %v", err)
		}
		defer rows.Close()
		details := make([]string, 0, 4)
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatalf("scan query plan: %v", err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("query plan rows: %v", err)
		}
		return strings.Join(details, "\n")
	}

	if got := plan(2_207_400, 2_207_368); !strings.Contains(got, "SEARCH p USING INTEGER PRIMARY KEY") {
		t.Fatalf("short projection tail does not read by event id: %s", got)
	}
	// A rollup still catching up leaves most of the projection past its
	// coverage, and there the time window is the tighter bound.
	if got := plan(2_207_400, 0); !strings.Contains(got, "idx_usage_monitoring_event_projection_scope_v2") {
		t.Fatalf("long projection tail left the scope index: %s", got)
	}
}
