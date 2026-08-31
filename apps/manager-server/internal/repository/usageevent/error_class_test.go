package usageevent

import (
	"context"
	"fmt"
	"path/filepath"
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
