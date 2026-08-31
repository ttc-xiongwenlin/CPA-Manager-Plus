package usageevent

import (
	"context"
	"fmt"
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
		// Attempt-visibility conditions corrupt the coverage check's
		// per-request attempt counts.
		{FromMS: 0, ToMS: 1, IncludeFailed: true, FailedOnly: true},
		{FromMS: 0, ToMS: 1, IncludeFailed: false},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, MinLatencyMS: 100},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, CacheStatus: "hit"},
		// Dimensions the latency scope index does not cover.
		{FromMS: 0, ToMS: 1, IncludeFailed: true, SearchQuery: "query"},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, Providers: []string{"codex"}},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, Accounts: []string{"user@example.com"}},
		{FromMS: 0, ToMS: 1, IncludeFailed: true, CredentialIDs: []string{"auth.json"}},
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

func businessOutcomeScopedEvent(hash, requestID string, timestampMS int64, authIndex, model string, failed bool) usage.Event {
	event := businessOutcomeEvent(hash, requestID, timestampMS, failed)
	event.AuthIndex = authIndex
	event.Model = model
	return event
}

func openBusinessOutcomeRepo(t *testing.T) Repository {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}
	return New(db)
}

func TestBusinessOutcomeScopedFilterKeepsFullyCoveredRequests(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// Retry hops auth-1 -> auth-2 but stays inside the bucket: rescued.
		businessOutcomeScopedEvent("cov-b1-a1", "b1", hourA+10_000, "auth-1", "gpt-x", true),
		businessOutcomeScopedEvent("cov-b1-a2", "b1", hourA+20_000, "auth-2", "gpt-x", false),
		// Single attempt inside the bucket.
		businessOutcomeScopedEvent("cov-b2-a1", "b2", hourA+30_000, "auth-1", "gpt-x", false),
		// Both attempts failed inside the bucket: business failure.
		businessOutcomeScopedEvent("cov-b3-a1", "b3", hourA+40_000, "auth-2", "gpt-x", true),
		businessOutcomeScopedEvent("cov-b3-a2", "b3", hourA+50_000, "auth-2", "gpt-x", true),
		// Entirely outside the bucket: out of scope, not excluded.
		businessOutcomeScopedEvent("cov-x1-a1", "x1", hourA+60_000, "auth-3", "gpt-x", false),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1", "auth-2"}
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("bucket-scoped business outcome: %v", err)
	}
	if !available {
		t.Fatalf("bucket-scoped business outcome unavailable, want available")
	}
	if len(rows) != 1 || rows[0].Requests != 3 || rows[0].Failures != 1 || rows[0].RescuedRequests != 1 {
		t.Fatalf("bucket-scoped rows = %#v, want 1 bucket with requests=3 failures=1 rescued=1", rows)
	}

	// A single-account filter splits b1 (1 of 2 attempts): excluded share
	// 1/2 crosses the 5%% ceiling, so the fold hides itself.
	single := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	single.AuthIndices = []string{"auth-1"}
	_, available, err = repo.BusinessOutcomeTimelineWithFilter(ctx, single)
	if err != nil {
		t.Fatalf("single-account business outcome: %v", err)
	}
	if available {
		t.Fatalf("single-account business outcome available, want hidden (split coverage)")
	}
}

func TestBusinessOutcomeScopedFilterByModel(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// Retry hops accounts but keeps the model: fully covered, rescued.
		businessOutcomeScopedEvent("covm-m1-a1", "m1", hourA+10_000, "auth-1", "gpt-5.5", true),
		businessOutcomeScopedEvent("covm-m1-a2", "m1", hourA+20_000, "auth-2", "gpt-5.5", false),
		// Different model: out of scope.
		businessOutcomeScopedEvent("covm-m2-a1", "m2", hourA+30_000, "auth-1", "gpt-5.4", false),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.Models = []string{"gpt-5.5"}
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("model-scoped business outcome: %v", err)
	}
	if !available {
		t.Fatalf("model-scoped business outcome unavailable, want available")
	}
	if len(rows) != 1 || rows[0].Requests != 1 || rows[0].Failures != 0 || rows[0].RescuedRequests != 1 {
		t.Fatalf("model-scoped rows = %#v, want 1 bucket with requests=1 rescued=1", rows)
	}
}

func TestBusinessOutcomeCoverageProbeSharesTheFoldWindow(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// Failed attempt just before the window: both the fold and the probe
		// must ignore it, or the in-window request below would be misjudged
		// as split (n_all counting the boundary attempt that n_in never can).
		businessOutcomeScopedEvent("covw-w1-a0", "w1", hourA-5_000, "auth-1", "gpt-x", true),
		businessOutcomeScopedEvent("covw-w1-a1", "w1", hourA+5_000, "auth-1", "gpt-x", false),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1"}
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("windowed business outcome: %v", err)
	}
	if !available {
		t.Fatalf("windowed business outcome unavailable, want available (probe must share the fold window)")
	}
	if len(rows) != 1 || rows[0].Requests != 1 || rows[0].Failures != 0 || rows[0].RescuedRequests != 0 {
		t.Fatalf("windowed rows = %#v, want the in-window slice only (requests=1, no failure, no rescue)", rows)
	}
}

func TestBusinessOutcomeExcludedShareThreshold(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	// The threshold denominator counts RETRIED requests only, so the fixture
	// pins the boundary with retried requests: one split request (attempts
	// on auth-1 and auth-2) plus 19 covered retried requests (both attempts
	// on auth-1). Excluded share 1/20 = 5%, exactly at the ceiling, stays
	// visible. Single-attempt requests must not dilute the ratio (see the
	// zero-retry and single-account tests below).
	events := []usage.Event{
		businessOutcomeScopedEvent("thr-s-a1", "split", hourA+1_000, "auth-1", "gpt-x", true),
		businessOutcomeScopedEvent("thr-s-a2", "split", hourA+2_000, "auth-2", "gpt-x", false),
	}
	for i := 0; i < 19; i++ {
		base := hourA + 10_000 + int64(i)*2_000
		requestID := fmt.Sprintf("cov-%d", i)
		events = append(events,
			businessOutcomeScopedEvent(fmt.Sprintf("thr-c%d-a1", i), requestID, base, "auth-1", "gpt-x", true),
			businessOutcomeScopedEvent(fmt.Sprintf("thr-c%d-a2", i), requestID, base+1_000, "auth-1", "gpt-x", false))
	}
	if _, err := repo.InsertBatch(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1"}
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("threshold business outcome (5%%): %v", err)
	}
	if !available {
		t.Fatalf("excluded share exactly at the ceiling should stay available")
	}
	// The split request is dropped from the fold, not counted as failed.
	if len(rows) != 1 || rows[0].Requests != 19 || rows[0].Failures != 0 || rows[0].RescuedRequests != 19 {
		t.Fatalf("threshold rows = %#v, want 19 covered rescued requests and no failures", rows)
	}

	// Shrink the window to drop the last covered retried request (attempts
	// at +46s/+47s): 18 covered + 1 split leaves 1/19 = 5.26%, which
	// crosses the ceiling and hides the fold.
	narrow := businessOutcomeTimeFilter(hourA, hourA+46_000)
	narrow.AuthIndices = []string{"auth-1"}
	_, available, err = repo.BusinessOutcomeTimelineWithFilter(ctx, narrow)
	if err != nil {
		t.Fatalf("threshold business outcome (5.26%%): %v", err)
	}
	if available {
		t.Fatalf("excluded share above the ceiling should hide the fold")
	}
}

func TestBusinessOutcomeSingleAccountWithMostlySingleAttemptsHides(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	// Production shape that motivated the retried-request denominator:
	// hundreds of single-attempt requests on one account and a handful of
	// retried requests, every one of which hopped to another account. With
	// the old all-requests denominator 3/63 = 4.8% stayed visible and
	// reported rescued=0 as if the account was never rescued; over retried
	// requests 3/3 = 100% hides the fold.
	events := make([]usage.Event, 0, 66)
	for i := 0; i < 60; i++ {
		events = append(events, businessOutcomeScopedEvent(
			fmt.Sprintf("sa-c%d", i), fmt.Sprintf("sa-req-%d", i), hourA+10_000+int64(i)*1_000, "auth-1", "gpt-x", false))
	}
	for i := 0; i < 3; i++ {
		requestID := fmt.Sprintf("sa-hop-%d", i)
		base := hourA + 100_000 + int64(i)*2_000
		events = append(events,
			businessOutcomeScopedEvent(fmt.Sprintf("sa-h%d-a1", i), requestID, base, "auth-1", "gpt-x", true),
			businessOutcomeScopedEvent(fmt.Sprintf("sa-h%d-a2", i), requestID, base+1_000, "auth-2", "gpt-x", false))
	}
	if _, err := repo.InsertBatch(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1"}
	_, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("single-account business outcome: %v", err)
	}
	if available {
		t.Fatalf("single-account fold with every retry hopping accounts should hide, not report rescued=0")
	}
}

func TestBusinessOutcomeScopeWithoutRetriesStaysVisible(t *testing.T) {
	repo := openBusinessOutcomeRepo(t)
	ctx := context.Background()
	hourA := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	// No retried request in scope means no split risk: the fold stays
	// visible even though the scope is a single account.
	events := make([]usage.Event, 0, 7)
	for i := 0; i < 5; i++ {
		events = append(events, businessOutcomeScopedEvent(
			fmt.Sprintf("nr-c%d", i), fmt.Sprintf("nr-req-%d", i), hourA+10_000+int64(i)*1_000, "auth-1", "gpt-x", i == 0))
	}
	// A retried request entirely outside the scope must not count either.
	events = append(events,
		businessOutcomeScopedEvent("nr-x-a1", "nr-ext", hourA+50_000, "auth-2", "gpt-x", true),
		businessOutcomeScopedEvent("nr-x-a2", "nr-ext", hourA+51_000, "auth-2", "gpt-x", false))
	if _, err := repo.InsertBatch(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1"}
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("no-retry business outcome: %v", err)
	}
	if !available {
		t.Fatalf("scope without retried requests should stay visible")
	}
	if len(rows) != 1 || rows[0].Requests != 5 || rows[0].Failures != 1 || rows[0].RescuedRequests != 0 {
		t.Fatalf("no-retry rows = %#v, want requests=5 failures=1 rescued=0", rows)
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

func TestBusinessOutcomeScopedQueryStaysOnCoveringIndexes(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliterepo.RunDerivedStartupMaintenance(context.Background(), db); err != nil {
		t.Fatalf("prepare post-listen indexes: %v", err)
	}

	filter := businessOutcomeTimeFilter(1_000, 2_000)
	filter.AuthIndices = []string{"auth-1", "auth-2"}
	where, whereArgs := analyticsWhere(filter)
	args := append(whereArgs, filter.FromMS, filter.ToMS)
	rows, err := db.Query(`explain query plan `+fmt.Sprintf(businessOutcomeScopedTimelineSQL, businessOutcomeInScopeExpr, where), args...)
	if err != nil {
		t.Fatalf("explain scoped business outcome query: %v", err)
	}
	defer rows.Close()

	details := make([]string, 0, 8)
	coversFold := false
	coversProbe := false
	rowLookup := false
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
		coversFold = coversFold || strings.Contains(detail, "COVERING INDEX "+businessOutcomeIndexName)
		coversProbe = coversProbe || strings.Contains(detail, "COVERING INDEX "+latencyScopeIndexName)
		// A plain "USING INDEX" (without COVERING) or a table scan means the
		// query fell back to wide-row lookups: measured 4.4s vs 0.3s per 7d
		// window on production.
		rowLookup = rowLookup ||
			(strings.Contains(detail, " USING INDEX ") && !strings.Contains(detail, "COVERING")) ||
			strings.Contains(detail, "SCAN usage_events")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	if !coversFold || !coversProbe || rowLookup {
		t.Fatalf("scoped business outcome query left its covering indexes: %v", details)
	}
}

func bucketScopeEvent(hash, requestID, authIndex string, timestampMS int64, failed bool) usage.Event {
	event := businessOutcomeEvent(hash, requestID, timestampMS, failed)
	event.AuthIndex = authIndex
	return event
}

// A bucket filter expands to the accounts tagged with that bucket TODAY, so a
// request whose retry landed on an account that has since moved buckets looks
// split even though buckets are isolated and the retry never left the pool.
// BucketScope credits the whole request to the bucket instead of hiding the
// fold over the coverage threshold.
func TestBusinessOutcomeTimelineKeepsPartiallyCoveredBucketRequests(t *testing.T) {
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
	if _, err := repo.InsertBatch(ctx, []usage.Event{
		// Rescued retry, both attempts still in the pool.
		bucketScopeEvent("bs-r1-a1", "r1", "auth-1", hourA+1_000, true),
		bucketScopeEvent("bs-r1-a2", "r1", "auth-2", hourA+2_000, false),
		// Rescued retry whose second attempt sits on an account that left the
		// pool, so the expansion no longer covers it.
		bucketScopeEvent("bs-r2-a1", "r2", "auth-1", hourA+3_000, true),
		bucketScopeEvent("bs-r2-a2", "r2", "auth-moved", hourA+4_000, false),
		// Another bucket entirely: never in scope.
		bucketScopeEvent("bs-r3-a1", "r3", "auth-other", hourA+5_000, true),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := businessOutcomeTimeFilter(hourA, hourA+3_600_000)
	filter.AuthIndices = []string{"auth-1", "auth-2"}

	if _, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter); err != nil {
		t.Fatalf("business outcome timeline: %v", err)
	} else if available {
		t.Fatalf("plain scope kept the fold, want it hidden over the coverage threshold")
	}

	filter.BucketScope = true
	rows, available, err := repo.BusinessOutcomeTimelineWithFilter(ctx, filter)
	if err != nil {
		t.Fatalf("bucket scope timeline: %v", err)
	}
	if !available {
		t.Fatalf("bucket scope hid the fold, want it visible")
	}
	if len(rows) != 1 {
		t.Fatalf("bucket scope rows = %#v, want 1 hour row", rows)
	}
	if rows[0].Requests != 2 || rows[0].Failures != 0 || rows[0].RescuedRequests != 2 {
		t.Fatalf("bucket scope row = %#v, want requests=2 failures=0 rescued=2", rows[0])
	}
}
