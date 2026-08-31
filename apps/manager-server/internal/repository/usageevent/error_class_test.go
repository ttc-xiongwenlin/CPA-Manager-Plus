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

func newErrorClassTestRepo(t *testing.T) Repository {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

func insertFailedEvent(t *testing.T, repo Repository, hash string, ts time.Time, statusCode int, summary string) {
	t.Helper()
	event := usage.Event{
		EventHash:      hash,
		TimestampMS:    ts.UnixMilli(),
		Timestamp:      ts.Format(time.RFC3339Nano),
		Model:          "gpt-test",
		Failed:         true,
		FailStatusCode: statusCode,
		FailSummary:    summary,
		CreatedAtMS:    ts.UnixMilli(),
	}
	if _, err := repo.InsertBatch(context.Background(), []usage.Event{event}); err != nil {
		t.Fatalf("insert event %s: %v", hash, err)
	}
}

func TestErrorClassStatsClassification(t *testing.T) {
	repo := newErrorClassTestRepo(t)
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)

	// 每条对应生产库里验证过的一种真实形态。
	cases := []struct {
		statusCode int
		summary    string
		wantClass  string
	}{
		{429, `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"pro"}}`, "quota_exhausted"},
		{429, `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded"}}`, "upstream_overloaded"},
		{503, ``, "upstream_overloaded"},
		{429, `{"error":{"type":"rpm_limit_exceeded"}}`, "rate_limited"},
		{429, `{"detail":"Rate limit exceeded"} {"Cf-Cache-Status":["DYNAMIC"]}`, "rate_limited"},
		{429, ``, "rate_limited"},
		{401, `{"error":{"message":"unauthorized"}}`, "auth"},
		{500, `{"error":{"message":"Your authentication token has been invalidated.","type":"authentication_error"}}`, "auth"},
		{500, `Post "https://chatgpt.com/backend-api/codex/responses": context canceled`, "client_canceled"},
		{499, ``, "client_canceled"},
		{408, `stream error: stream disconnected before completion: stream closed before response.completed`, "stream_aborted"},
		{500, `Post "https://chatgpt.com/backend-api/codex/responses": dial tcp: lookup chatgpt.com: no such host`, "network"},
		{200, `read tcp 192.168.120.14:55535->104.18.32.47:443: read: connection reset by peer`, "network"},
		{504, ``, "timeout"},
		{500, `upstream connect error or disconnect/reset before headers. reset reason: connection timeout`, "network"},
		{400, `{"error":{"message":"Your input exceeds the context window of this model."}}`, "invalid_request"},
		{502, ``, "upstream_error"},
		{200, `mystery failure body`, "other"},
	}
	for i, c := range cases {
		insertFailedEvent(t, repo, // hash 用 index 保证唯一
			fmt.Sprintf("evt-%d", i), base.Add(time.Duration(i)*time.Minute), c.statusCode, c.summary)
	}
	// 一条成功事件，验证不被计入。
	ok := usage.Event{
		EventHash:   "ok-1",
		TimestampMS: base.UnixMilli(),
		Timestamp:   base.Format(time.RFC3339Nano),
		Model:       "gpt-test",
		CreatedAtMS: base.UnixMilli(),
	}
	if _, err := repo.InsertBatch(context.Background(), []usage.Event{ok}); err != nil {
		t.Fatalf("insert ok event: %v", err)
	}

	stats, err := repo.ErrorClassStatsWithFilter(context.Background(), AnalyticsFilter{
		FromMS: base.UnixMilli(),
		ToMS:   base.Add(time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("error class stats: %v", err)
	}

	got := map[string]int64{}
	var total int64
	for _, stat := range stats {
		got[stat.Class] = stat.Count
		total += stat.Count
	}
	want := map[string]int64{}
	for _, c := range cases {
		want[c.wantClass]++
	}
	if total != int64(len(cases)) {
		t.Fatalf("total classified = %d, want %d (map: %#v)", total, len(cases), got)
	}
	for class, count := range want {
		if got[class] != count {
			t.Errorf("class %s = %d, want %d (map: %#v)", class, got[class], count, got)
		}
	}
}

func TestErrorClassTimelineBucketsByHour(t *testing.T) {
	repo := newErrorClassTestRepo(t)
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)

	insertFailedEvent(t, repo, "tl-1", base.Add(5*time.Minute), 429, "")
	insertFailedEvent(t, repo, "tl-2", base.Add(10*time.Minute), 429, "")
	insertFailedEvent(t, repo, "tl-3", base.Add(20*time.Minute), 502, "")
	insertFailedEvent(t, repo, "tl-4", base.Add(70*time.Minute), 429, "")

	points, err := repo.ErrorClassTimelineWithFilter(context.Background(), AnalyticsFilter{
		FromMS: base.UnixMilli(),
		ToMS:   base.Add(2 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}

	hour0 := base.UnixMilli()
	hour1 := base.Add(time.Hour).UnixMilli()
	got := map[[2]int64]map[string]int64{}
	for _, p := range points {
		key := [2]int64{p.BucketMS, 0}
		if got[key] == nil {
			got[key] = map[string]int64{}
		}
		got[key][p.Class] += p.Count
	}
	if got[[2]int64{hour0, 0}]["rate_limited"] != 2 {
		t.Errorf("hour0 rate_limited = %d, want 2", got[[2]int64{hour0, 0}]["rate_limited"])
	}
	if got[[2]int64{hour0, 0}]["upstream_error"] != 1 {
		t.Errorf("hour0 upstream_error = %d, want 1", got[[2]int64{hour0, 0}]["upstream_error"])
	}
	if got[[2]int64{hour1, 0}]["rate_limited"] != 1 {
		t.Errorf("hour1 rate_limited = %d, want 1", got[[2]int64{hour1, 0}]["rate_limited"])
	}
}

func TestErrorClassRecentOrdersAndTruncates(t *testing.T) {
	repo := newErrorClassTestRepo(t)
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)

	long := strings.Repeat("x", 500)
	insertFailedEvent(t, repo, "rc-1", base.Add(1*time.Minute), 500, "dial tcp: lookup example.com: no such host")
	insertFailedEvent(t, repo, "rc-2", base.Add(2*time.Minute), 429, long)
	insertFailedEvent(t, repo, "rc-3", base.Add(3*time.Minute), 401, "")

	recent, err := repo.ErrorClassRecentWithFilter(context.Background(), AnalyticsFilter{
		FromMS: base.UnixMilli(),
		ToMS:   base.Add(time.Hour).UnixMilli(),
	}, 2)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("len(recent) = %d, want 2", len(recent))
	}
	if recent[0].Class != "auth" {
		t.Errorf("recent[0].Class = %q, want auth (newest first)", recent[0].Class)
	}
	if recent[1].Class != "rate_limited" {
		t.Errorf("recent[1].Class = %q, want rate_limited", recent[1].Class)
	}
	if len(recent[1].Summary) != 300 {
		t.Errorf("summary length = %d, want 300 (truncated)", len(recent[1].Summary))
	}
	if !recent[0].StatusCode.Valid || recent[0].StatusCode.Int64 != 401 {
		t.Errorf("recent[0].StatusCode = %#v, want 401", recent[0].StatusCode)
	}
}
