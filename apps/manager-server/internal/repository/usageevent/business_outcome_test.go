package usageevent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func businessOutcomeEvent(hash, requestID string, timestampMS int64, failed bool) usage.Event {
	return usage.Event{
		EventHash:   hash,
		RequestID:   requestID,
		TimestampMS: timestampMS,
		Timestamp:   time.UnixMilli(timestampMS).UTC().Format(time.RFC3339Nano),
		Model:       "claude-sonnet",
		AuthIndex:   "auth-1",
		Failed:      failed,
		CreatedAtMS: timestampMS,
	}
}

func businessOutcomeTimeFilter(fromMS, toMS int64) AnalyticsFilter {
	return AnalyticsFilter{FromMS: fromMS, ToMS: toMS, IncludeFailed: true}
}

func TestBusinessOutcomeTimelineFoldsAttemptsByRequestID(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}
	repo := New(db)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	hourB := hourA + 3_600_000
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// Single successful attempt.
		businessOutcomeEvent("bo-r1-a1", "r1", hourA+5_000, false),
		// Failed attempt rescued by a retry inside the same hour.
		businessOutcomeEvent("bo-r2-a1", "r2", hourA+10_000, true),
		businessOutcomeEvent("bo-r2-a2", "r2", hourA+20_000, false),
		// Event without a request_id counts as its own request.
		businessOutcomeEvent("bo-anon", "", hourA+30_000, false),
		// First attempt in hour A, rescue lands in hour B: the request
		// buckets on its first attempt.
		businessOutcomeEvent("bo-r4-a1", "r4", hourB-1_000, true),
		businessOutcomeEvent("bo-r4-a2", "r4", hourB+5_000, false),
		// Every attempt failed: a business failure in hour B.
		businessOutcomeEvent("bo-r3-a1", "r3", hourB+10_000, true),
		businessOutcomeEvent("bo-r3-a2", "r3", hourB+20_000, true),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, businessOutcomeTimeFilter(hourA, hourB+3_600_000))
	if err != nil {
		t.Fatalf("business outcome timeline: %v", err)
	}
	if !available {
		t.Fatalf("business outcome timeline unavailable")
	}
	if len(rows) != 2 {
		t.Fatalf("business outcome rows = %#v, want 2 hour rows", rows)
	}
	if rows[0].BucketMS != hourA || rows[0].Requests != 4 || rows[0].Failures != 0 || rows[0].RescuedRequests != 2 {
		t.Fatalf("hour A row = %#v, want bucket=%d requests=4 failures=0 rescued=2", rows[0], hourA)
	}
	if rows[1].BucketMS != hourB || rows[1].Requests != 1 || rows[1].Failures != 1 || rows[1].RescuedRequests != 0 {
		t.Fatalf("hour B row = %#v, want bucket=%d requests=1 failures=1 rescued=0", rows[1], hourB)
	}
}

func TestBusinessOutcomeTimelineUnavailableWithoutIndex(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// No RunDerivedStartupMaintenance: the covering index only ships through
	// the offline cleanup-derived command on populated databases.
	if _, err := db.Exec(`drop index if exists ` + businessOutcomeIndexName); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	repo := New(db)
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(context.Background(), businessOutcomeTimeFilter(0, 3_600_000))
	if err != nil {
		t.Fatalf("business outcome timeline: %v", err)
	}
	if available || rows != nil {
		t.Fatalf("business outcome = (%#v, %t), want unavailable without the covering index", rows, available)
	}
}

func TestBusinessOutcomeTimelineRejectsScopedFilters(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}
	repo := New(db)
	scoped := []AnalyticsFilter{
		{FromMS: 0, ToMS: 1, IncludeFailed: true, Models: []string{"claude-sonnet"}},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, AuthIndices: []string{"auth-1"}},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, APIKeyHashes: []string{"hash"}},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, SearchQuery: "query"},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, FailedOnly: true},
		{FromMS: 0, ToMS: 1, IncludeFailed: false},
	}
	for _, filter := range scoped {
		_, available, err := repo.BusinessOutcomeTimelineWithFilter(context.Background(), filter)
		if err != nil {
			t.Fatalf("business outcome timeline (%#v): %v", filter, err)
		}
		if available {
			t.Fatalf("business outcome available for scoped filter %#v, want unavailable", filter)
		}
	}
}

func TestBusinessOutcomeQueryUsesCoveringIndex(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	rows, err := db.Query(`explain query plan `+businessOutcomeTimelineSQL, int64(1_000), int64(2_000))
	if err != nil {
		t.Fatalf("explain business outcome query: %v", err)
	}
	defer rows.Close()

	details := make([]string, 0, 8)
	usesCoveringIndex := false
	usesRequestIDIndex := false
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
		usesCoveringIndex = usesCoveringIndex || strings.Contains(detail, "COVERING INDEX "+businessOutcomeIndexName)
		usesRequestIDIndex = usesRequestIDIndex || strings.Contains(detail, "idx_usage_events_request_id")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	if !usesCoveringIndex || usesRequestIDIndex {
		t.Fatalf("business outcome query is not pinned to the covering index: %v", details)
	}
}
